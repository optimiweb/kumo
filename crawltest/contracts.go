// Package crawltest provides reusable conformance checks for crawl adapters.
package crawltest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/optimiweb/kumo/crawl"
)

// FrontierFactory constructs an isolated Frontier for a conformance check.
type FrontierFactory func(testing.TB) crawl.Frontier

// StorageFactory constructs an isolated DirectStorage for a conformance check.
type StorageFactory func(testing.TB) crawl.DirectStorage

// CheckFrontier verifies the baseline lifecycle and coordination guarantees
// required by Frontier. Adapter-specific suites should additionally cover
// their durable transaction, tenancy, and policy behavior.
func CheckFrontier(t testing.TB, newFrontier FrontierFactory) {
	t.Helper()
	checkWorkLifecycle(t, newFrontier)
	checkTransitionReplay(t, newFrontier)
	checkExpiredLeaseAndStaleFence(t, newFrontier)
	checkFetchReservationLifecycle(t, newFrontier)
	checkRobotsSingleFlight(t, newFrontier)
}

func checkWorkLifecycle(t testing.TB, newFrontier FrontierFactory) {
	t.Helper()
	frontier := newFrontier(t)
	ctx := context.Background()
	request := testEnqueueRequest(t, 1)

	first, err := frontier.EnqueueSeed(ctx, request)
	if err != nil {
		t.Fatalf("enqueue seed: %v", err)
	}
	if !first.Inserted || first.ID == "" {
		t.Fatalf("first enqueue = %+v, want inserted work", first)
	}

	duplicate, err := frontier.EnqueueSeed(ctx, request)
	if err != nil {
		t.Fatalf("enqueue duplicate seed: %v", err)
	}
	if duplicate.Inserted || duplicate.ID != first.ID {
		t.Fatalf("duplicate enqueue = %+v, want existing work %q", duplicate, first.ID)
	}

	if err := frontier.SealSeeds(ctx); err != nil {
		t.Fatalf("seal seeds: %v", err)
	}
	claim, err := frontier.Claim(ctx, crawl.ClaimRequest{
		OperationID:   operationID(2),
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.State != crawl.FrontierLeased || claim.Lease.Work().ID() != first.ID {
		t.Fatalf("claim = %+v, want lease for %q", claim, first.ID)
	}

	transition, err := frontier.Transition(ctx, crawl.TransitionRequest{
		OperationID: operationID(3),
		Lease:       claim.Lease,
		Decision:    crawl.Ack(),
	})
	if err != nil {
		t.Fatalf("ack transition: %v", err)
	}
	if transition.ApplyState != crawl.TransitionApplied || transition.FinalState != crawl.WorkHandled {
		t.Fatalf("transition = %+v, want handled", transition)
	}

	drained, err := frontier.Claim(ctx, crawl.ClaimRequest{
		OperationID:   operationID(4),
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("claim after settlement: %v", err)
	}
	if drained.State != crawl.FrontierDrained {
		t.Fatalf("claim after settlement = %+v, want drained", drained)
	}
}

func checkTransitionReplay(t testing.TB, newFrontier FrontierFactory) {
	t.Helper()
	frontier := newFrontier(t)
	ctx := context.Background()
	workID, lease := enqueueAndClaim(t, frontier, testEnqueueRequest(t, 2), operationID(10), time.Second)
	transition := crawl.TransitionRequest{
		OperationID: operationID(11),
		Lease:       lease,
		Decision:    crawl.Ack(),
	}

	first, err := frontier.Transition(ctx, transition)
	if err != nil {
		t.Fatalf("apply transition: %v", err)
	}
	if first.ApplyState != crawl.TransitionApplied || first.FinalState != crawl.WorkHandled {
		t.Fatalf("first transition = %+v, want applied handled", first)
	}
	replay, err := frontier.Transition(ctx, transition)
	if err != nil {
		t.Fatalf("replay transition: %v", err)
	}
	if replay.ApplyState != crawl.TransitionAlreadyApplied || replay.FinalState != first.FinalState || replay.Code != first.Code {
		t.Fatalf("replay transition = %+v, want recorded result %+v", replay, first)
	}
	resolved, err := frontier.ResolveTransition(ctx, workID, transition.OperationID)
	if err != nil {
		t.Fatalf("resolve transition: %v", err)
	}
	if !resolved.Known || resolved.Result != first {
		t.Fatalf("resolution = %+v, want %+v", resolved, first)
	}
}

func checkExpiredLeaseAndStaleFence(t testing.TB, newFrontier FrontierFactory) {
	t.Helper()
	frontier := newFrontier(t)
	ctx := context.Background()
	_, original := enqueueAndClaim(t, frontier, testEnqueueRequest(t, 3), operationID(20), 100*time.Millisecond)

	waitForLeaseExpiry(original)
	claimed, err := frontier.Claim(ctx, crawl.ClaimRequest{
		OperationID:   operationID(21),
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("claim expired work: %v", err)
	}
	if claimed.State != crawl.FrontierLeased {
		t.Fatalf("claim expired work = %+v, want a new lease", claimed)
	}
	if claimed.Lease.Fence().Token() == original.Fence().Token() || claimed.Lease.Fence().Generation() <= original.Fence().Generation() {
		t.Fatalf("reclaimed lease fence = %+v, original = %+v", claimed.Lease.Fence(), original.Fence())
	}
	_, err = frontier.Transition(ctx, crawl.TransitionRequest{
		OperationID: operationID(22),
		Lease:       original,
		Decision:    crawl.Ack(),
	})
	if !errors.Is(err, crawl.ErrLeaseConflict) {
		t.Fatalf("transition with stale lease error = %v, want %v", err, crawl.ErrLeaseConflict)
	}
	if _, err := frontier.Transition(ctx, crawl.TransitionRequest{
		OperationID: operationID(23),
		Lease:       claimed.Lease,
		Decision:    crawl.Ack(),
	}); err != nil {
		t.Fatalf("transition with current lease: %v", err)
	}
}

func checkFetchReservationLifecycle(t testing.TB, newFrontier FrontierFactory) {
	t.Helper()
	frontier := newFrontier(t)
	ctx := context.Background()
	_, lease := enqueueAndClaim(t, frontier, testEnqueueRequest(t, 4), operationID(30), time.Second)
	request := crawl.ReserveFetchRequest{
		OperationID: operationID(31),
		Intent: crawl.FetchIntent{
			URL:           "https://example.com/page",
			Method:        crawl.MethodGET,
			Purpose:       crawl.FetchPurposeWork,
			ResourceClass: crawl.ResourceHTML,
		},
		LeaseDuration: time.Second,
	}
	reserved, err := frontier.ReserveFetch(ctx, lease, request)
	if err != nil {
		t.Fatalf("reserve fetch: %v", err)
	}
	if reserved.State != crawl.FetchReserved {
		t.Fatalf("reserve fetch = %+v, want reserved", reserved)
	}
	replay, err := frontier.ReserveFetch(ctx, lease, request)
	if err != nil {
		t.Fatalf("replay reserve fetch: %v", err)
	}
	if replay.State != crawl.FetchReserved || replay.Reservation.ID() != reserved.Reservation.ID() {
		t.Fatalf("replay reserve fetch = %+v, want original reservation", replay)
	}
	renewed, err := frontier.RenewFetch(ctx, lease, crawl.RenewFetchRequest{
		OperationID:   operationID(32),
		Reservation:   reserved.Reservation,
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("renew fetch reservation: %v", err)
	}
	if !renewed.LeaseExpiresAt().After(reserved.Reservation.LeaseExpiresAt()) {
		t.Fatalf("renewed reservation expiry = %v, original = %v", renewed.LeaseExpiresAt(), reserved.Reservation.LeaseExpiresAt())
	}
	if err := frontier.FinishFetch(ctx, lease, crawl.FinishFetchRequest{
		OperationID: operationID(33),
		Reservation: renewed,
		Report: crawl.FetchReport{
			Outcome:     crawl.FetchOutcomeHTTPResponse,
			StatusClass: 2,
		},
	}); err != nil {
		t.Fatalf("finish fetch reservation: %v", err)
	}
}

func checkRobotsSingleFlight(t testing.TB, newFrontier FrontierFactory) {
	t.Helper()
	frontier := newFrontier(t)
	ctx := context.Background()
	_, first := enqueueAndClaim(t, frontier, testEnqueueRequest(t, 5), operationID(40), time.Second)
	_, second := enqueueAndClaim(t, frontier, testEnqueueRequest(t, 6), operationID(41), time.Second)
	key := crawl.RobotsKeyFor("https://example.com", "OCDN_crawler", "v1")
	request := crawl.AcquireRobotsRequest{
		OperationID:   operationID(42),
		Key:           key,
		Origin:        "https://example.com",
		LeaseDuration: time.Second,
	}
	acquired, err := frontier.AcquireRobots(ctx, first, request)
	if err != nil {
		t.Fatalf("acquire robots: %v", err)
	}
	if acquired.State != crawl.RobotsAcquired {
		t.Fatalf("first robots acquire = %+v, want acquired", acquired)
	}
	busy, err := frontier.AcquireRobots(ctx, second, crawl.AcquireRobotsRequest{
		OperationID:   operationID(43),
		Key:           key,
		Origin:        "https://example.com",
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("contended robots acquire: %v", err)
	}
	if busy.State != crawl.RobotsBusy || busy.RetryAfter <= 0 {
		t.Fatalf("contended robots acquire = %+v, want busy with retry", busy)
	}
	record := crawl.NewRobotsRecord(nil, 0, nil, time.Time{}, time.Hour, false, true, false)
	if err := frontier.PublishRobots(ctx, first, operationID(44), acquired.Lease, record, time.Hour); err != nil {
		t.Fatalf("publish robots: %v", err)
	}
	cached, err := frontier.AcquireRobots(ctx, second, crawl.AcquireRobotsRequest{
		OperationID:   operationID(45),
		Key:           key,
		Origin:        "https://example.com",
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("read cached robots: %v", err)
	}
	if cached.State != crawl.RobotsCached || !cached.Record.AllowAll() {
		t.Fatalf("cached robots = %+v, want cached allow-all record", cached)
	}
}

// CheckDirectStorage verifies direct claim, release, and terminal settlement.
func CheckDirectStorage(t testing.TB, newStorage StorageFactory) {
	t.Helper()
	storage := newStorage(t)
	ctx := context.Background()
	key := testIdentity(t).Key()
	claim, err := storage.Claim(ctx, crawl.DirectClaimRequest{
		OperationID:   operationID(10),
		Key:           key,
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Status != crawl.DirectClaimAcquired {
		t.Fatalf("claim = %+v, want acquired", claim)
	}

	if _, err := storage.Release(ctx, crawl.DirectReleaseRequest{
		OperationID: operationID(11),
		Claim:       claim.Claim,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	claim, err = storage.Claim(ctx, crawl.DirectClaimRequest{
		OperationID:   operationID(12),
		Key:           key,
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if claim.Status != crawl.DirectClaimAcquired {
		t.Fatalf("reclaim = %+v, want acquired", claim)
	}

	if _, err := storage.Finalize(ctx, crawl.DirectFinalizeRequest{
		OperationID: operationID(13),
		Claim:       claim.Claim,
		Terminal:    crawl.DirectTerminalHandled,
	}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	terminal, err := storage.Claim(ctx, crawl.DirectClaimRequest{
		OperationID:   operationID(14),
		Key:           key,
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("claim terminal key: %v", err)
	}
	if terminal.Status != crawl.DirectClaimTerminal || terminal.Terminal != crawl.DirectTerminalHandled {
		t.Fatalf("terminal claim = %+v, want handled terminal", terminal)
	}
}

func enqueueAndClaim(t testing.TB, frontier crawl.Frontier, request crawl.EnqueueRequest, op crawl.OperationID, duration time.Duration) (crawl.WorkID, crawl.Lease) {
	t.Helper()
	ctx := context.Background()
	enqueued, err := frontier.EnqueueSeed(ctx, request)
	if err != nil {
		t.Fatalf("enqueue seed: %v", err)
	}
	claimed, err := frontier.Claim(ctx, crawl.ClaimRequest{OperationID: op, LeaseDuration: duration})
	if err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if claimed.State != crawl.FrontierLeased {
		t.Fatalf("claim work = %+v, want lease", claimed)
	}
	return enqueued.ID, claimed.Lease
}

func testEnqueueRequest(t testing.TB, key byte) crawl.EnqueueRequest {
	t.Helper()
	identityKey := crawl.IdentityKey{0: key}
	identity, err := crawl.NewURLIdentity(identityKey, "https://example.com/test")
	if err != nil {
		t.Fatalf("new test identity: %v", err)
	}
	return crawl.EnqueueRequest{
		Identity:      identity,
		Method:        crawl.MethodGET,
		Source:        crawl.SourceSeed,
		ResourceClass: crawl.ResourceHTML,
	}
}

func waitForLeaseExpiry(lease crawl.Lease) {
	// Keep the suite portable for adapters that use their own durable clock.
	if remaining := time.Until(lease.LeaseExpiresAt()); remaining > 0 {
		time.Sleep(remaining + 20*time.Millisecond)
	}
}

func testIdentity(t testing.TB) crawl.URLIdentity {
	t.Helper()
	identity, err := crawl.NewURLIdentity(crawl.IdentityKey{1}, "https://example.com/")
	if err != nil {
		t.Fatalf("new test identity: %v", err)
	}
	return identity
}

func operationID(last byte) crawl.OperationID {
	return crawl.OperationID{15: last}
}
