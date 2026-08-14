package memory_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/memory"
)

func TestTransitionCommitSuccess(t *testing.T) {
	fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{})
	lease := enqueueClaim(t, fr, 1)
	var calls atomic.Int32
	res, err := fr.Transition(context.Background(), crawl.TransitionRequest{
		OperationID: crawl.OperationID{15: 1},
		Lease:       lease,
		Decision:    crawl.Ack(),
		Commit: func(context.Context) error {
			calls.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if res.ApplyState != crawl.TransitionApplied || res.FinalState != crawl.WorkHandled {
		t.Fatalf("transition = %+v, want handled", res)
	}
	if calls.Load() != 1 {
		t.Fatalf("commit calls = %d, want 1", calls.Load())
	}
}

func TestTransitionCommitNotCalledOnReplay(t *testing.T) {
	fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{})
	lease := enqueueClaim(t, fr, 2)
	var calls atomic.Int32
	req := crawl.TransitionRequest{
		OperationID: crawl.OperationID{15: 2},
		Lease:       lease,
		Decision:    crawl.Ack(),
		Commit: func(context.Context) error {
			calls.Add(1)
			return nil
		},
	}
	if _, err := fr.Transition(context.Background(), req); err != nil {
		t.Fatalf("first: %v", err)
	}
	replay, err := fr.Transition(context.Background(), req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.ApplyState != crawl.TransitionAlreadyApplied {
		t.Fatalf("replay = %+v, want already applied", replay)
	}
	if calls.Load() != 1 {
		t.Fatalf("commit calls = %d, want 1", calls.Load())
	}
}

func TestTransitionCommitErrorLeavesWorkLeased(t *testing.T) {
	fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{})
	lease := enqueueClaim(t, fr, 3)
	boom := errors.New("commit failed")
	_, err := fr.Transition(context.Background(), crawl.TransitionRequest{
		OperationID: crawl.OperationID{15: 3},
		Lease:       lease,
		Decision:    crawl.Ack(),
		Commit:      func(context.Context) error { return boom },
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	res, err := fr.Transition(context.Background(), crawl.TransitionRequest{
		OperationID: crawl.OperationID{15: 4},
		Lease:       lease,
		Decision:    crawl.Ack(),
	})
	if err != nil {
		t.Fatalf("retry transition: %v", err)
	}
	if res.ApplyState != crawl.TransitionApplied || res.FinalState != crawl.WorkHandled {
		t.Fatalf("retry = %+v, want handled", res)
	}
}

func TestTransitionCommitNotCalledOnStaleLease(t *testing.T) {
	fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{})
	ctx := context.Background()
	id, err := crawl.NewURLIdentity(crawl.IdentityKey{0: 4}, "https://example.com/test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fr.EnqueueSeed(ctx, crawl.EnqueueRequest{
		Identity: id, Method: crawl.MethodGET, Source: crawl.SourceSeed, ResourceClass: crawl.ResourceHTML,
	}); err != nil {
		t.Fatal(err)
	}
	original, err := fr.Claim(ctx, crawl.ClaimRequest{OperationID: crawl.OperationID{15: 10}, LeaseDuration: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(original.Lease.LeaseExpiresAt()); remaining > 0 {
		time.Sleep(remaining + 20*time.Millisecond)
	}
	claimed, err := fr.Claim(ctx, crawl.ClaimRequest{OperationID: crawl.OperationID{15: 11}, LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != crawl.FrontierLeased {
		t.Fatalf("reclaim = %+v", claimed)
	}
	var calls atomic.Int32
	_, err = fr.Transition(ctx, crawl.TransitionRequest{
		OperationID: crawl.OperationID{15: 12},
		Lease:       original.Lease,
		Decision:    crawl.Ack(),
		Commit: func(context.Context) error {
			calls.Add(1)
			return nil
		},
	})
	if !errors.Is(err, crawl.ErrLeaseConflict) {
		t.Fatalf("err = %v, want %v", err, crawl.ErrLeaseConflict)
	}
	if calls.Load() != 0 {
		t.Fatalf("commit calls = %d, want 0", calls.Load())
	}
}

func enqueueClaim(t *testing.T, fr *memory.MemoryFrontier, key byte) crawl.Lease {
	t.Helper()
	ctx := context.Background()
	id, err := crawl.NewURLIdentity(crawl.IdentityKey{0: key}, "https://example.com/test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fr.EnqueueSeed(ctx, crawl.EnqueueRequest{
		Identity: id, Method: crawl.MethodGET, Source: crawl.SourceSeed, ResourceClass: crawl.ResourceHTML,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := fr.Claim(ctx, crawl.ClaimRequest{OperationID: crawl.OperationID{15: key}, LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != crawl.FrontierLeased {
		t.Fatalf("claim = %+v", claimed)
	}
	return claimed.Lease
}
