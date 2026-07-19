//go:build integration

package kumo_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/optimiweb/kumo"
	"github.com/optimiweb/kumo/internal/httpx"
)

// --- Issue 1: handler must see policy/robots/transport failures ------------

func TestHandlerSeesNonHTTPOutcomes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	c := testCollector(t, srv)

	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 5, FetchBudget: 5})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	// Out-of-scope seed: policy denies before any fetch.
	seed, _ := id(context.Background(), kumo.IdentityRequest{
		RawURL: "https://other.example.com/page", Method: kumo.MethodGET, Source: kumo.SourceSeed,
	})
	_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	_ = fr.SealSeeds(context.Background())

	var outcomes []kumo.FetchOutcome
	var mu sync.Mutex
	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		mu.Lock()
		outcomes = append(outcomes, in.Result().Outcome())
		mu.Unlock()
		return kumo.DefaultDecision(in.Result())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0] != kumo.FetchOutcomePolicyDenied {
		t.Fatalf("outcomes=%v", outcomes)
	}
	if report.Failed() != 1 {
		t.Fatalf("report=%+v", report)
	}
}

// --- Issue 2: fetch reservation stays held during a slow request -----------

func TestFetchLeaseRenewalKeepsOriginSlot(t *testing.T) {
	var concurrent, maxConcurrent atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			io.WriteString(w, "User-agent: *\nAllow: /\n")
			return
		}
		n := concurrent.Add(1)
		for {
			m := maxConcurrent.Load()
			if n <= m || maxConcurrent.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(400 * time.Millisecond)
		concurrent.Add(-1)
		io.WriteString(w, "slow")
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	h, p, _ := net.SplitHostPort(host)
	cfg := kumo.DefaultCollectorConfig()
	cfg.Policy = kumo.BaselinePolicy{
		AllowedPorts: map[int]struct{}{mustAtoi(p): {}},
		AllowedHosts: map[string]struct{}{h: {}},
	}
	// Permit shorter than the response time: renewal must keep it alive.
	cfg.FetchLease = 100 * time.Millisecond
	cfg.LeaseDuration = 300 * time.Millisecond
	cfg.RenewSafety = 30 * time.Millisecond
	cfg.Workers = 3
	c, err := kumo.NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	loopback := netip.MustParseAddr("127.0.0.1")
	kumo.SetTestHTTPClient(c, httpx.NewClient(httpx.Config{
		UserAgent: cfg.UserAgent, ConnectTimeout: time.Second, TLSTimeout: time.Second,
		HeaderTimeout: time.Second, BodyTimeout: 3 * time.Second, TotalTimeout: 5 * time.Second,
		MaxHeaderBytes: cfg.MaxHeaderBytes, HeaderAllowlist: allowMap(cfg.HeaderAllowlist),
		Resolver: staticResolver{ip: loopback}, AllowAddrs: []netip.Addr{loopback},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", host)
		},
	}))

	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{
		MaxPages: 5, FetchBudget: 20, MaxAttempts: 2, MaxOriginConc: 1,
	})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	for _, path := range []string{"/1", "/2"} {
		seed, _ := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + path, Method: kumo.MethodGET, Source: kumo.SourceSeed})
		_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
			Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
		})
	}
	_ = fr.SealSeeds(context.Background())

	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		return kumo.DefaultDecision(in.Result())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Handled() != 2 {
		t.Fatalf("handled=%d report=%+v", report.Handled(), report)
	}
	// MaxOriginConc=1 with 400ms responses and 100ms permits: without renewal
	// the expired permit frees the slot mid-request and both run concurrently.
	if maxConcurrent.Load() != 1 {
		t.Fatalf("max concurrent origin requests=%d, expected 1", maxConcurrent.Load())
	}
}

// --- Issue 3: crawl-delay spacing ------------------------------------------

func TestCrawlDelaySpacing(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			io.WriteString(w, "User-agent: *\nCrawl-delay: 1\nAllow: /\n")
			return
		}
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	h, p, _ := net.SplitHostPort(host)
	cfg := kumo.DefaultCollectorConfig()
	cfg.Policy = kumo.BaselinePolicy{
		AllowedPorts: map[int]struct{}{mustAtoi(p): {}},
		AllowedHosts: map[string]struct{}{h: {}},
	}
	cfg.Workers = 3
	c, err := kumo.NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	loopback := netip.MustParseAddr("127.0.0.1")
	kumo.SetTestHTTPClient(c, httpx.NewClient(httpx.Config{
		UserAgent: cfg.UserAgent, ConnectTimeout: time.Second, TLSTimeout: time.Second,
		HeaderTimeout: time.Second, BodyTimeout: 2 * time.Second, TotalTimeout: 3 * time.Second,
		MaxHeaderBytes: cfg.MaxHeaderBytes, HeaderAllowlist: allowMap(cfg.HeaderAllowlist),
		Resolver: staticResolver{ip: loopback}, AllowAddrs: []netip.Addr{loopback},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", host)
		},
	}))

	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{
		MaxPages: 5, FetchBudget: 20, MaxAttempts: 2, MaxOriginConc: 4,
	})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	for _, path := range []string{"/a", "/b"} {
		seed, _ := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + path, Method: kumo.MethodGET, Source: kumo.SourceSeed})
		_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
			Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
		})
	}
	_ = fr.SealSeeds(context.Background())
	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		return kumo.DefaultDecision(in.Result())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 20 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if report.Handled() != 2 {
		t.Fatalf("handled=%d report=%+v", report.Handled(), report)
	}
	// 1s crawl-delay must space the two page fetches by >= ~1s.
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 2 {
		t.Fatalf("starts=%v", starts)
	}
	gap := starts[1].Sub(starts[0])
	if gap < 900*time.Millisecond {
		t.Fatalf("crawl-delay gap=%v, expected >=1s", gap)
	}
	_ = elapsed
}

// --- Issues 4+5: direct-mode held dedupe and final-decision finalization ---

func TestDirectConcurrentRunsDedupe(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(300 * time.Millisecond) // widen the overlap window
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	c := testCollector(t, srv)
	storage := kumo.NewMemoryStorage(nil)

	run := func() (int, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		report, err := c.RunDirect(ctx, kumo.DirectRunConfig{
			Seeds:            []string{srv.URL + "/"},
			Storage:          storage,
			Identifier:       kumo.IdentityFunc(kumo.DefaultIdentity),
			MaxWorkItems:     5,
			MaxFetchAttempts: 5,
			MaxConcurrency:   1,
			MaxAttempts:      1,
			Handler: kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
				return kumo.Ack()
			}),
		})
		return report.Handled(), err
	}

	var wg sync.WaitGroup
	var handled1, handled2 int
	var err1, err2 error
	wg.Add(2)
	go func() { defer wg.Done(); handled1, err1 = run() }()
	go func() { defer wg.Done(); handled2, err2 = run() }()
	wg.Wait()
	if err1 != nil || err2 != nil {
		t.Fatalf("err1=%v err2=%v", err1, err2)
	}
	if handled1+handled2 != 1 {
		t.Fatalf("handled1=%d handled2=%d hits=%d: concurrent direct runs processed the same identity", handled1, handled2, hits.Load())
	}
}

func TestDirectZeroDecisionFinalizesAsFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	c := testCollector(t, srv)
	storage := kumo.NewMemoryStorage(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := c.RunDirect(ctx, kumo.DirectRunConfig{
		Seeds:            []string{srv.URL + "/"},
		Storage:          storage,
		Identifier:       kumo.IdentityFunc(kumo.DefaultIdentity),
		MaxWorkItems:     5,
		MaxFetchAttempts: 5,
		MaxConcurrency:   1,
		MaxAttempts:      1,
		Handler: kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
			return kumo.Decision{} // zero/invalid decision
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed() != 1 {
		t.Fatalf("report=%+v", report)
	}
	// Storage must record failed, never handled, for an invalid decision.
	idRes, _ := kumo.DefaultIdentity(context.Background(), kumo.IdentityRequest{
		RawURL: srv.URL + "/", Method: kumo.MethodGET, Source: kumo.SourceSeed,
	})
	op, _ := kumo.NewTestOperationID()
	res, err := storage.Claim(context.Background(), kumo.DirectClaimRequest{
		OperationID: op, Key: idRes.Identity.Key(), LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != kumo.DirectClaimTerminal || res.Terminal != kumo.DirectTerminalFailed {
		t.Fatalf("claim=%+v, expected terminal failed", res)
	}
}

// --- Issue 6: memory storage op replay semantics ----------------------------

func TestMemoryStorageOpReplay(t *testing.T) {
	st := kumo.NewMemoryStorage(nil)
	var key kumo.IdentityKey
	key[0] = 7
	op, _ := kumo.NewTestOperationID()
	res, err := st.Claim(context.Background(), kumo.DirectClaimRequest{OperationID: op, Key: key, LeaseDuration: time.Second})
	if err != nil || res.Status != kumo.DirectClaimAcquired {
		t.Fatalf("%+v %v", res, err)
	}

	// Release and reclaim with a different operation.
	opR, _ := kumo.NewTestOperationID()
	if _, err := st.Release(context.Background(), kumo.DirectReleaseRequest{OperationID: opR, Claim: res.Claim}); err != nil {
		t.Fatal(err)
	}
	op2, _ := kumo.NewTestOperationID()
	res2, err := st.Claim(context.Background(), kumo.DirectClaimRequest{OperationID: op2, Key: key, LeaseDuration: time.Second})
	if err != nil || res2.Status != kumo.DirectClaimAcquired {
		t.Fatalf("%+v %v", res2, err)
	}

	// Replaying the ORIGINAL claim op must return the ORIGINAL token, not the newer fence.
	replay, err := st.Claim(context.Background(), kumo.DirectClaimRequest{OperationID: op, Key: key, LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Claim.Token != res.Claim.Token || replay.Claim.Generation != res.Claim.Generation {
		t.Fatalf("replay returned newer fence: replay=%+v original=%+v", replay.Claim, res.Claim)
	}

	// Reusing the release op ID for a different claim is a conflict.
	if _, err := st.Release(context.Background(), kumo.DirectReleaseRequest{OperationID: opR, Claim: res2.Claim}); err != kumo.ErrOperationConflict {
		t.Fatalf("err=%v, expected operation conflict", err)
	}

	// Replaying the same release op with the same claim succeeds idempotently.
	rr, err := st.Release(context.Background(), kumo.DirectReleaseRequest{OperationID: opR, Claim: res.Claim})
	if err != nil || !rr.Applied {
		t.Fatalf("replay release=%+v %v", rr, err)
	}

	// Finalize replay with a different terminal payload is a conflict.
	opF, _ := kumo.NewTestOperationID()
	if _, err := st.Finalize(context.Background(), kumo.DirectFinalizeRequest{
		OperationID: opF, Claim: res2.Claim, Terminal: kumo.DirectTerminalHandled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Finalize(context.Background(), kumo.DirectFinalizeRequest{
		OperationID: opF, Claim: res2.Claim, Terminal: kumo.DirectTerminalFailed,
	}); err != kumo.ErrOperationConflict {
		t.Fatalf("err=%v, expected operation conflict", err)
	}
}

// --- Issue 7: headers invalidated after handler completion ------------------

func TestHeaderViewInvalidatedAfterHandler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("ETag", "v1")
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	c := testCollector(t, srv)
	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 5, FetchBudget: 5})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	seed, _ := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + "/", Method: kumo.MethodGET, Source: kumo.SourceSeed})
	_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	_ = fr.SealSeeds(context.Background())

	var retained kumo.HeaderView
	var during string
	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		resp, ok := in.Result().Response()
		if !ok {
			return kumo.Fail(kumo.CodeHandlerFailed)
		}
		retained = resp.Headers()
		during = retained.Get("etag")
		return kumo.Ack()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 5 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	if during != "v1" {
		t.Fatalf("etag during handler=%q", during)
	}
	if got := retained.Get("etag"); got != "" {
		t.Fatalf("etag readable after handler: %q", got)
	}
	if vs := retained.Values("etag"); vs != nil {
		t.Fatalf("values readable after handler: %v", vs)
	}
}

// --- Issue 8: renew rejects invalid durations -------------------------------

func TestMemoryRenewRejectsInvalidDurations(t *testing.T) {
	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 5, FetchBudget: 5})
	id, _ := kumo.DefaultIdentity(context.Background(), kumo.IdentityRequest{
		RawURL: "https://example.com/", Method: kumo.MethodGET, Source: kumo.SourceSeed,
	})
	_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: id.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	op, _ := kumo.NewTestOperationID()
	claim, err := fr.Claim(context.Background(), kumo.ClaimRequest{OperationID: op, LeaseDuration: time.Second})
	if err != nil || claim.State != kumo.FrontierLeased {
		t.Fatalf("%+v %v", claim, err)
	}
	rop, _ := kumo.NewTestOperationID()
	if _, err := fr.Renew(context.Background(), kumo.RenewLeaseRequest{
		OperationID: rop, Lease: claim.Lease, LeaseDuration: 0,
	}); err == nil {
		t.Fatal("Renew accepted zero duration")
	}
	if _, err := fr.Renew(context.Background(), kumo.RenewLeaseRequest{
		OperationID: rop, Lease: claim.Lease, LeaseDuration: -time.Second,
	}); err == nil {
		t.Fatal("Renew accepted negative duration")
	}

	// Fetch reservation renewal validation.
	fop, _ := kumo.NewTestOperationID()
	res, err := fr.ReserveFetch(context.Background(), claim.Lease, kumo.ReserveFetchRequest{
		OperationID:   fop,
		Intent:        kumo.FetchIntent{URL: "https://example.com/", Method: kumo.MethodGET, Purpose: kumo.FetchPurposeWork, ResourceClass: kumo.ResourceHTML},
		LeaseDuration: time.Second,
	})
	if err != nil || res.State != kumo.FetchReserved {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := fr.RenewFetch(context.Background(), claim.Lease, kumo.RenewFetchRequest{
		OperationID: fop, Reservation: res.Reservation, LeaseDuration: 0,
	}); err == nil {
		t.Fatal("RenewFetch accepted zero duration")
	}

	// Robots renewal validation.
	key := kumo.RobotsKeyFor("https://example.com", "ua", "v1")
	aop, _ := kumo.NewTestOperationID()
	acq, err := fr.AcquireRobots(context.Background(), claim.Lease, kumo.AcquireRobotsRequest{
		OperationID: aop, Key: key, Origin: "https://example.com", LeaseDuration: time.Second,
	})
	if err != nil || acq.State != kumo.RobotsAcquired {
		t.Fatalf("%+v %v", acq, err)
	}
	if _, err := fr.RenewRobots(context.Background(), claim.Lease, aop, acq.Lease, 0); err == nil {
		t.Fatal("RenewRobots accepted zero duration")
	}
}

// --- Issue 9: ResolveTransition binds work ID --------------------------------

func TestResolveTransitionBindsWorkID(t *testing.T) {
	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 5, FetchBudget: 5})
	id, _ := kumo.DefaultIdentity(context.Background(), kumo.IdentityRequest{
		RawURL: "https://example.com/", Method: kumo.MethodGET, Source: kumo.SourceSeed,
	})
	_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: id.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	op, _ := kumo.NewTestOperationID()
	claim, _ := fr.Claim(context.Background(), kumo.ClaimRequest{OperationID: op, LeaseDuration: time.Second})
	top, _ := kumo.NewTestOperationID()
	if _, err := fr.Transition(context.Background(), kumo.TransitionRequest{
		OperationID: top, Lease: claim.Lease, Decision: kumo.Ack(),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := fr.ResolveTransition(context.Background(), claim.Lease.Work().ID(), top)
	if err != nil || !res.Known {
		t.Fatalf("resolve own work: %+v %v", res, err)
	}
	// Wrong work ID must not resolve.
	res2, err := fr.ResolveTransition(context.Background(), "w-999", top)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Known {
		t.Fatal("resolved transition for the wrong work item")
	}
}

// --- DefaultDecision mapping -------------------------------------------------

func TestDefaultDecision(t *testing.T) {
	cases := []struct {
		outcome kumo.FetchOutcome
		want    kumo.DecisionKind
	}{
		{kumo.FetchOutcomeHTTPResponse, kumo.DecisionAck},
		{kumo.FetchOutcomeRobotsUnavailable, kumo.DecisionRetry},
		{kumo.FetchOutcomeCancelled, kumo.DecisionRetry},
		{kumo.FetchOutcomeTimedOut, kumo.DecisionRetry},
		{kumo.FetchOutcomeRobotsDenied, kumo.DecisionFail},
		{kumo.FetchOutcomePolicyDenied, kumo.DecisionFail},
		{kumo.FetchOutcomeWireBodyTooLarge, kumo.DecisionFail},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.outcome), func(t *testing.T) {
			d := kumo.DefaultDecision(kumo.NewFetchResult(tc.outcome, "", "https://example.com/", 0))
			if d.Kind() != tc.want {
				t.Fatalf("kind=%v want %v", d.Kind(), tc.want)
			}
			if err := d.Validate(); err != nil && tc.want != kumo.DecisionAck {
				t.Fatalf("invalid decision: %v", err)
			}
		})
	}
}
