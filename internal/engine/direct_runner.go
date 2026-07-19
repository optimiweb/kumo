package engine

import (
	"context"
	"sync"
	"time"

	"github.com/optimiweb/kumo/crawl"
)

// Direct-mode dedup protocol:
//
//  1. Claims are HELD for the whole run. A seed or discovery identity is
//     claimed before enqueue and the claim stays active (renewed) until the
//     work's final transition, so concurrent direct runs cannot both process
//     the same identity.
//  2. Finalization happens at the effective transition, not at handler
//     return: Ack -> handled, Fail -> failed, Retry/cancel -> release. The
//     wrapper sees the decision after Kumo's validation and conversions, so
//     a zero or converted decision can never be finalized as handled.
func (c *Engine) RunDirect(ctx context.Context, cfg crawl.DirectRunConfig, mem crawl.Frontier) (crawl.RunReport, error) {

	dd := &directDedupe{
		storage:  cfg.Storage,
		leaseDur: c.cfg.LeaseDuration,
	}
	stopClaims := dd.renewClaims(ctx)
	defer stopClaims()

	for _, seed := range cfg.Seeds {
		if code := c.admit(seed, crawl.MethodGET); code != crawl.CodeNone {
			continue
		}
		idRes, err := cfg.Identifier.Identify(ctx, crawl.IdentityRequest{
			RawURL: seed,
			Method: crawl.MethodGET,
			Source: crawl.SourceSeed,
			Depth:  0,
		})
		if err != nil {
			return crawl.RunReport{}, err
		}
		if idRes.State != crawl.IdentityAccepted {
			continue
		}
		owned, err := dd.claim(ctx, idRes.Identity.Key())
		if err != nil {
			return crawl.RunReport{}, err
		}
		if !owned {
			continue
		}
		if _, err := mem.EnqueueSeed(ctx, crawl.EnqueueRequest{
			Identity:      idRes.Identity,
			Method:        crawl.MethodGET,
			Depth:         0,
			Source:        crawl.SourceSeed,
			Priority:      0,
			ResourceClass: crawl.ResourceHTML,
		}); err != nil {
			dd.release(ctx, idRes.Identity.Key())
			return crawl.RunReport{}, err
		}
	}
	if err := mem.SealSeeds(ctx); err != nil {
		return crawl.RunReport{}, err
	}

	baseHandler := cfg.Handler
	wrapped := crawl.HandlerFunc(func(ctx context.Context, input crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		claiming := &directClaimingSink{
			dd:         dd,
			identifier: cfg.Identifier,
			inner:      sink,
			parent:     input.Lease().Work(),
		}
		return baseHandler.Handle(ctx, input, claiming)
	})

	return c.RunFrontier(ctx, crawl.FrontierRunConfig{
		Frontier:     &directTransitionFrontier{Frontier: mem, dd: dd},
		Identifier:   cfg.Identifier,
		Handler:      wrapped,
		UntilDrained: true,
		PollInterval: 10 * time.Millisecond,
	})
}

// directDedupe holds storage claims for the duration of a direct run.
type directDedupe struct {
	storage  crawl.DirectStorage
	leaseDur time.Duration

	mu     sync.Mutex
	claims map[crawl.IdentityKey]crawl.DirectClaim
}

// claim acquires and holds the identity key. It reports false when another
// caller owns the key or it already has a terminal state.
func (d *directDedupe) claim(ctx context.Context, key crawl.IdentityKey) (bool, error) {
	op, err := newOperationID()
	if err != nil {
		return false, err
	}
	res, err := d.storage.Claim(ctx, crawl.DirectClaimRequest{
		OperationID:   op,
		Key:           key,
		LeaseDuration: d.leaseDur,
	})
	if err != nil {
		return false, err
	}
	if res.Status != crawl.DirectClaimAcquired {
		return false, nil
	}
	d.mu.Lock()
	if d.claims == nil {
		d.claims = make(map[crawl.IdentityKey]crawl.DirectClaim)
	}
	d.claims[key] = res.Claim
	d.mu.Unlock()
	return true, nil
}

// release drops a held claim so other runs may acquire the identity.
func (d *directDedupe) release(ctx context.Context, key crawl.IdentityKey) {
	claim, ok := d.take(key)
	if !ok {
		return
	}
	op, err := newOperationID()
	if err != nil {
		return
	}
	_, _ = d.storage.Release(ctx, crawl.DirectReleaseRequest{OperationID: op, Claim: claim})
}

// finalize applies the terminal storage transition for an effective decision.
func (d *directDedupe) finalize(ctx context.Context, key crawl.IdentityKey, dec crawl.Decision) {
	claim, ok := d.take(key)
	if !ok {
		return
	}
	// Cleanup context: settle even when the run context is done.
	settleCtx := context.WithoutCancel(ctx)
	op, err := newOperationID()
	if err != nil {
		return
	}
	switch dec.Kind() {
	case crawl.DecisionAck:
		_, _ = d.storage.Finalize(settleCtx, crawl.DirectFinalizeRequest{
			OperationID: op,
			Claim:       claim,
			Terminal:    crawl.DirectTerminalHandled,
		})
	case crawl.DecisionFail:
		_, _ = d.storage.Finalize(settleCtx, crawl.DirectFinalizeRequest{
			OperationID: op,
			Claim:       claim,
			Terminal:    crawl.DirectTerminalFailed,
		})
	default:
		_, _ = d.storage.Release(settleCtx, crawl.DirectReleaseRequest{
			OperationID: op,
			Claim:       claim,
		})
	}
}

func (d *directDedupe) take(key crawl.IdentityKey) (crawl.DirectClaim, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	claim, ok := d.claims[key]
	if ok {
		delete(d.claims, key)
	}
	return claim, ok
}

// renewClaims keeps all held claims alive for the run's lifetime.
func (d *directDedupe) renewClaims(ctx context.Context) func() {
	interval := d.leaseDur / 3
	if interval <= 0 {
		interval = time.Second
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				d.renewAll(ctx)
			}
		}
	}()
	return func() { close(done) }
}

func (d *directDedupe) renewAll(ctx context.Context) {
	d.mu.Lock()
	keys := make([]crawl.IdentityKey, 0, len(d.claims))
	for k := range d.claims {
		keys = append(keys, k)
	}
	d.mu.Unlock()
	for _, k := range keys {
		d.mu.Lock()
		claim, ok := d.claims[k]
		d.mu.Unlock()
		if !ok {
			continue
		}
		op, err := newOperationID()
		if err != nil {
			continue
		}
		renewed, err := d.storage.Renew(ctx, crawl.DirectRenewRequest{
			OperationID:   op,
			Claim:         claim,
			LeaseDuration: d.leaseDur,
		})
		if err != nil {
			// Claim lost; another run owns it now.
			d.mu.Lock()
			delete(d.claims, k)
			d.mu.Unlock()
			continue
		}
		d.mu.Lock()
		if _, still := d.claims[k]; still {
			d.claims[k] = renewed
		}
		d.mu.Unlock()
	}
}

// directClaimingSink claims discovery identities before submitting them, so
// discovered work is dedupe-held exactly like seeds.
type directClaimingSink struct {
	dd         *directDedupe
	identifier crawl.Identifier
	inner      crawl.DiscoverySink
	parent     crawl.Work
}

func (s *directClaimingSink) Submit(ctx context.Context, d crawl.Discovery) (crawl.DiscoveryResult, error) {
	method := d.Method
	if method == "" {
		method = crawl.MethodGET
	}
	idRes, err := s.identifier.Identify(ctx, crawl.IdentityRequest{
		RawURL:   d.URL,
		Method:   method,
		ParentID: s.parent.ID(),
		Source:   sourceFromRelation(d.Relation),
		Depth:    s.parent.Depth() + 1,
	})
	if err != nil {
		return crawl.DiscoveryResult{}, err
	}
	if idRes.State != crawl.IdentityAccepted {
		code := idRes.Code
		if code == "" {
			code = crawl.CodeIdentityRejected
		}
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: code}, nil
	}
	owned, err := s.dd.claim(ctx, idRes.Identity.Key())
	if err != nil {
		return crawl.DiscoveryResult{}, err
	}
	if !owned {
		return crawl.DiscoveryResult{State: crawl.DiscoveryDuplicate}, nil
	}
	res, err := s.inner.Submit(ctx, d)
	if err != nil {
		s.dd.release(context.WithoutCancel(ctx), idRes.Identity.Key())
		return crawl.DiscoveryResult{}, err
	}
	if res.State == crawl.DiscoveryRejected || res.State == crawl.DiscoveryLimitReached {
		s.dd.release(ctx, idRes.Identity.Key())
	}
	// Duplicate: the already-enqueued work holds its own claim; release ours.
	if res.State == crawl.DiscoveryDuplicate {
		s.dd.release(ctx, idRes.Identity.Key())
	}
	return res, nil
}

// directTransitionFrontier finalizes direct storage at the effective
// transition, after Kumo's decision validation and conversions.
type directTransitionFrontier struct {
	crawl.Frontier
	dd *directDedupe
}

func (f *directTransitionFrontier) Transition(ctx context.Context, req crawl.TransitionRequest) (crawl.TransitionResult, error) {
	res, err := f.Frontier.Transition(ctx, req)
	if err == nil && res.ApplyState == crawl.TransitionApplied {
		f.dd.finalize(ctx, req.Lease.Work().Identity().Key(), req.Decision)
	}
	return res, err
}
