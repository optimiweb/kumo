//go:build integration

package kumo_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/optimiweb/kumo"
	"github.com/optimiweb/kumo/internal/httpx"
)

func testCollector(t *testing.T, srv *httptest.Server) *kumo.Collector {
	t.Helper()
	cfg := kumo.DefaultCollectorConfig()
	// Allow loopback ports used by httptest by overriding policy ports and hosts.
	u := srv.URL
	// Parse host/port from server URL
	host := strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
	h, p, _ := net.SplitHostPort(host)
	cfg.Policy = kumo.BaselinePolicy{
		AllowedPorts: map[int]struct{}{mustAtoi(p): {}},
		AllowedHosts: map[string]struct{}{h: {}, "127.0.0.1": {}, "localhost": {}},
	}
	// Disable robots for simple tests unless specifically testing robots.
	cfg.Robots.Enabled = false
	cfg.Robots.Override = true
	cfg.Robots.OverrideReason = "test"
	cfg.Workers = 2
	c, err := kumo.NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Inject dialer that still validates addresses but allows the test server IP.
	loopback := netip.MustParseAddr("127.0.0.1")
	client := httpx.NewClient(httpx.Config{
		UserAgent:       cfg.UserAgent,
		ConnectTimeout:  cfg.Timeouts.Connect,
		TLSTimeout:      cfg.Timeouts.TLS,
		HeaderTimeout:   cfg.Timeouts.Headers,
		BodyTimeout:     cfg.Timeouts.Body,
		TotalTimeout:    cfg.Timeouts.Total,
		MaxHeaderBytes:  cfg.MaxHeaderBytes,
		HeaderAllowlist: allowMap(cfg.HeaderAllowlist),
		Resolver:        staticResolver{host: h, ip: loopback},
		AllowAddrs:      []netip.Addr{loopback},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			// For tests only: dial the httptest listener via original host:port.
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, "tcp", host)
		},
	})
	kumo.SetTestHTTPClient(c, client)
	return c
}

type staticResolver struct {
	host string
	ip   netip.Addr
}

func (s staticResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return []netip.Addr{s.ip}, nil
}

func allowMap(list []string) map[string]struct{} {
	m := make(map[string]struct{}, len(list))
	for _, h := range list {
		m[strings.ToLower(h)] = struct{}{}
	}
	return m
}

func mustAtoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestFrontierCrawlBasic(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<html><body><a href="/a">A</a></body></html>`)
		case "/a":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<html><body>ok</body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := testCollector(t, srv)
	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{
		MaxAttempts:   2,
		MaxPages:      10,
		FetchBudget:   20,
		MaxOriginConc: 2,
	})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	seedID, err := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + "/", Method: kumo.MethodGET, Source: kumo.SourceSeed})
	if err != nil || seedID.State != kumo.IdentityAccepted {
		t.Fatalf("identity: %+v %v", seedID, err)
	}
	if _, err := fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: seedID.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fr.SealSeeds(context.Background()); err != nil {
		t.Fatal(err)
	}

	var saw atomic.Int64
	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		saw.Add(1)
		res := in.Result()
		if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
			return kumo.Fail(res.ErrorCode())
		}
		resp, ok := res.Response()
		if !ok {
			return kumo.Fail(kumo.CodeHandlerFailed)
		}
		body, _ := io.ReadAll(resp.Body().Reader())
		// Discover simple anchors.
		s := string(body)
		if i := strings.Index(s, `href="`); i >= 0 {
			rest := s[i+6:]
			if j := strings.Index(rest, `"`); j >= 0 {
				href := rest[:j]
				if strings.HasPrefix(href, "/") {
					_, _ = sink.Submit(ctx, kumo.Discovery{
						URL: srv.URL + href, Method: kumo.MethodGET, Relation: kumo.RelationLink,
					})
				}
			}
		}
		return kumo.Ack()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Handled() < 2 {
		t.Fatalf("handled=%d hits=%d saw=%d stop=%s", report.Handled(), hits.Load(), saw.Load(), report.StopReason())
	}
}

func TestRedirectNotFollowedInline(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/from" {
			http.Redirect(w, r, "/to", http.StatusFound)
			return
		}
		io.WriteString(w, "dest")
	}))
	defer srv.Close()

	c := testCollector(t, srv)
	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 10, FetchBudget: 10, MaxAttempts: 2})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	seed, _ := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + "/from", Method: kumo.MethodGET, Source: kumo.SourceSeed})
	_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	_ = fr.SealSeeds(context.Background())

	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		if redir, ok := in.Result().Redirect(); ok {
			if redir.State != kumo.DiscoveryInserted && redir.State != kumo.DiscoveryDuplicate {
				t.Errorf("redirect discovery state=%v code=%s", redir.State, redir.Code)
			}
		}
		return kumo.Ack()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Handled() < 2 {
		t.Fatalf("expected both hops handled, got %d paths=%v", report.Handled(), paths)
	}
	// First response must be the redirect source only for first claim.
	if len(paths) < 2 || paths[0] != "/from" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestBodyTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytesRepeat('x', 1000))
	}))
	defer srv.Close()

	cfg := kumo.DefaultCollectorConfig()
	host := strings.TrimPrefix(srv.URL, "http://")
	h, p, _ := net.SplitHostPort(host)
	cfg.Policy = kumo.BaselinePolicy{AllowedPorts: map[int]struct{}{mustAtoi(p): {}}, AllowedHosts: map[string]struct{}{h: {}}}
	cfg.Robots.Enabled = false
	cfg.Robots.Override = true
	cfg.Robots.OverrideReason = "test"
	cfg.Bodies[kumo.ResourceHTML] = kumo.BodyLimits{WireBytes: 100, DecodedBytes: 100, ConvertedText: 100}
	c, err := kumo.NewCollector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	loopback := netip.MustParseAddr("127.0.0.1")
	client := httpx.NewClient(httpx.Config{
		UserAgent: cfg.UserAgent, ConnectTimeout: time.Second, TLSTimeout: time.Second,
		HeaderTimeout: time.Second, BodyTimeout: time.Second, TotalTimeout: 3 * time.Second,
		MaxHeaderBytes: cfg.MaxHeaderBytes, HeaderAllowlist: allowMap(cfg.HeaderAllowlist),
		Resolver: staticResolver{ip: loopback}, AllowAddrs: []netip.Addr{loopback},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", host)
		},
	})
	kumo.SetTestHTTPClient(c, client)

	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 5, FetchBudget: 5})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	seed, _ := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + "/", Method: kumo.MethodGET, Source: kumo.SourceSeed})
	_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	_ = fr.SealSeeds(context.Background())

	// The handler sees the oversized outcome and can persist it as evidence.
	var code kumo.ErrorCode
	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		res := in.Result()
		code = res.ErrorCode()
		if res.Outcome() != kumo.FetchOutcomeWireBodyTooLarge {
			t.Errorf("outcome=%s, expected wire_body_too_large", res.Outcome())
		}
		if _, hasBody := res.Response(); hasBody {
			t.Error("oversized result must not expose a body")
		}
		return kumo.Fail(res.ErrorCode())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed() != 1 {
		t.Fatalf("expected failed oversized body, report=%+v", report)
	}
	if code != kumo.CodeWireBodyTooLarge {
		t.Fatalf("code=%s", code)
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestUnsafeIPDenied(t *testing.T) {
	if !kumo.IsUnsafeIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback should be unsafe")
	}
	if !kumo.IsUnsafeIP(net.ParseIP("10.0.0.1")) {
		t.Fatal("private should be unsafe")
	}
	if kumo.IsUnsafeIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("public should be safe")
	}
}

func TestMemoryFrontierLeaseConflict(t *testing.T) {
	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxAttempts: 3, MaxPages: 5, FetchBudget: 5})
	id, _ := kumo.DefaultIdentity(context.Background(), kumo.IdentityRequest{
		RawURL: "https://example.com/", Method: kumo.MethodGET, Source: kumo.SourceSeed,
	})
	_, err := fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: id.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = fr.SealSeeds(context.Background())
	op, _ := kumo.NewTestOperationID()
	claim, err := fr.Claim(context.Background(), kumo.ClaimRequest{OperationID: op, LeaseDuration: time.Second})
	if err != nil || claim.State != kumo.FrontierLeased {
		t.Fatalf("%+v %v", claim, err)
	}
	// Stale fence cannot transition.
	badFence, _ := kumo.NewFence(claim.Lease.Work().ID(), "nope", 99)
	badLease, _ := kumo.NewLease(claim.Lease.Work(), badFence, 1, 3, time.Now().Add(time.Second), time.Millisecond)
	top, _ := kumo.NewTestOperationID()
	_, err = fr.Transition(context.Background(), kumo.TransitionRequest{
		OperationID: top, Lease: badLease, Decision: kumo.Ack(),
	})
	if err != kumo.ErrLeaseConflict {
		t.Fatalf("err=%v", err)
	}
}

func TestDirectStorageClaim(t *testing.T) {
	st := kumo.NewMemoryStorage(nil)
	var key kumo.IdentityKey
	key[0] = 1
	op, _ := kumo.NewTestOperationID()
	res, err := st.Claim(context.Background(), kumo.DirectClaimRequest{OperationID: op, Key: key, LeaseDuration: time.Second})
	if err != nil || res.Status != kumo.DirectClaimAcquired {
		t.Fatalf("%+v %v", res, err)
	}
	op2, _ := kumo.NewTestOperationID()
	res2, err := st.Claim(context.Background(), kumo.DirectClaimRequest{OperationID: op2, Key: key, LeaseDuration: time.Second})
	if err != nil || res2.Status != kumo.DirectClaimBusy {
		t.Fatalf("%+v %v", res2, err)
	}
	fop, _ := kumo.NewTestOperationID()
	_, err = st.Finalize(context.Background(), kumo.DirectFinalizeRequest{
		OperationID: fop, Claim: res.Claim, Terminal: kumo.DirectTerminalHandled,
	})
	if err != nil {
		t.Fatal(err)
	}
	op3, _ := kumo.NewTestOperationID()
	res3, err := st.Claim(context.Background(), kumo.DirectClaimRequest{OperationID: op3, Key: key, LeaseDuration: time.Second})
	if err != nil || res3.Status != kumo.DirectClaimTerminal {
		t.Fatalf("%+v %v", res3, err)
	}
}

func TestDecisionValidation(t *testing.T) {
	if err := kumo.Ack().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := kumo.Fail(kumo.CodeHandlerFailed).Validate(); err != nil {
		t.Fatal(err)
	}
	var zero kumo.Decision
	if err := zero.Validate(); err == nil {
		t.Fatal("zero decision should fail")
	}
}

func TestCanonicalURLRejectsFragment(t *testing.T) {
	if _, err := kumo.CanonicalFetchURL("https://example.com/a#x"); err == nil {
		t.Fatal("expected fragment rejection")
	}
	if _, err := kumo.CanonicalFetchURL("https://user:pass@example.com/"); err == nil {
		t.Fatal("expected userinfo rejection")
	}
	got, err := kumo.CanonicalFetchURL("https://Example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "example.com") && !strings.Contains(got, "Example.com") {
		t.Fatalf("got %s", got)
	}
}

func TestRobotsDenyNotFetched(t *testing.T) {
	var hitsPrivate atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "User-agent: *\nDisallow: /private\n")
		case "/":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<html><body><a href="/private/x">P</a><a href="/ok">O</a></body></html>`)
		case "/private/x":
			hitsPrivate.Add(1)
			io.WriteString(w, "secret")
		case "/ok":
			io.WriteString(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	h, p, _ := net.SplitHostPort(host)
	cfg := kumo.DefaultCollectorConfig()
	cfg.Policy = kumo.BaselinePolicy{
		AllowedPorts: map[int]struct{}{mustAtoi(p): {}},
		AllowedHosts: map[string]struct{}{h: {}},
	}
	cfg.Workers = 1
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

	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 10, FetchBudget: 20, MaxAttempts: 2})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	seed, _ := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + "/", Method: kumo.MethodGET, Source: kumo.SourceSeed})
	_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
	})
	_ = fr.SealSeeds(context.Background())

	var sawRobotsDenied atomic.Int64
	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		res := in.Result()
		if res.Outcome() == kumo.FetchOutcomeRobotsDenied {
			sawRobotsDenied.Add(1)
			return kumo.Fail(res.ErrorCode())
		}
		if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
			return kumo.Fail(res.ErrorCode())
		}
		resp, _ := res.Response()
		body, _ := io.ReadAll(resp.Body().Reader())
		s := string(body)
		for _, href := range []string{"/private/x", "/ok"} {
			if strings.Contains(s, `href="`+href+`"`) {
				_, _ = sink.Submit(ctx, kumo.Discovery{URL: srv.URL + href, Method: kumo.MethodGET, Relation: kumo.RelationLink})
			}
		}
		return kumo.Ack()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hitsPrivate.Load() != 0 {
		t.Fatalf("robots-denied page was fetched %d times", hitsPrivate.Load())
	}
	if sawRobotsDenied.Load() != 1 {
		t.Fatalf("handler saw robots_denied %d times, expected 1", sawRobotsDenied.Load())
	}
	if report.Handled() != 2 { // "/" and "/ok"
		t.Fatalf("handled=%d failed=%d report=%+v", report.Handled(), report.Failed(), report)
	}
	if report.Failed() != 1 { // "/private/x" robots denied
		t.Fatalf("failed=%d report=%+v", report.Failed(), report)
	}
}

func TestRobotsCachedSingleFlight(t *testing.T) {
	var robotsHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			robotsHits.Add(1)
			io.WriteString(w, "User-agent: *\nAllow: /\n")
			return
		}
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
	cfg.Workers = 4
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

	fr := kumo.NewMemoryFrontier(kumo.MemoryFrontierOptions{MaxPages: 10, FetchBudget: 50, MaxAttempts: 2})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	for _, path := range []string{"/1", "/2", "/3"} {
		seed, _ := id(context.Background(), kumo.IdentityRequest{RawURL: srv.URL + path, Method: kumo.MethodGET, Source: kumo.SourceSeed})
		_, _ = fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
			Identity: seed.Identity, Method: kumo.MethodGET, Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
		})
	}
	_ = fr.SealSeeds(context.Background())

	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		return kumo.Ack()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier: fr, Identifier: id, Handler: handler, UntilDrained: true, PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Handled() != 3 {
		t.Fatalf("handled=%d", report.Handled())
	}
	if robotsHits.Load() != 1 {
		t.Fatalf("robots fetched %d times, expected single-flight", robotsHits.Load())
	}
}
