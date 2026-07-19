package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/optimiweb/kumo/crawl"
)

func (c *Engine) RunFrontier(ctx context.Context, cfg crawl.FrontierRunConfig) (crawl.RunReport, error) {
	start := time.Now()
	var (
		handled   atomic.Int64
		failed    atomic.Int64
		retried   atomic.Int64
		cancelled atomic.Int64
		skipped   atomic.Int64
		fetched   atomic.Int64
	)

	workers := c.cfg.Workers
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	stopReason := crawl.CodeNone

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if err := ctx.Err(); err != nil {
					return
				}
				op, err := newOperationID()
				if err != nil {
					errCh <- err
					return
				}
				claim, err := cfg.Frontier.Claim(ctx, crawl.ClaimRequest{
					OperationID:   op,
					LeaseDuration: c.cfg.LeaseDuration,
				})
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					errCh <- err
					return
				}
				switch claim.State {
				case crawl.FrontierDrained:
					if cfg.UntilDrained {
						return
					}
					if err := waitBackoff(ctx, cfg.PollInterval); err != nil {
						return
					}
					continue
				case crawl.FrontierIdle:
					if !cfg.UntilDrained {
						if err := waitBackoff(ctx, cfg.PollInterval); err != nil {
							return
						}
						continue
					}
					if err := waitBackoff(ctx, claim.RetryAfter); err != nil {
						return
					}
					if claim.RetryAfter == 0 {
						if err := waitBackoff(ctx, cfg.PollInterval); err != nil {
							return
						}
					}
					continue
				case crawl.FrontierLeased:
					// process
				default:
					if err := waitBackoff(ctx, cfg.PollInterval); err != nil {
						return
					}
					continue
				}

				decision, result := c.processWork(ctx, claim.Lease, cfg.Frontier, cfg.Identifier, cfg.Handler)
				if result.Outcome() == crawl.FetchOutcomeHTTPResponse {
					fetched.Add(1)
				}
				if ctx.Err() != nil && decision.Kind() != crawl.DecisionAck {
					cancelled.Add(1)
					continue
				}
				top, err := newOperationID()
				if err != nil {
					errCh <- err
					return
				}
				tres, err := cfg.Frontier.Transition(ctx, crawl.TransitionRequest{
					OperationID: top,
					Lease:       claim.Lease,
					Decision:    decision,
				})
				if err != nil {
					// Ambiguous: try resolve.
					if res, rerr := cfg.Frontier.ResolveTransition(ctx, claim.Lease.Work().ID(), top); rerr == nil && res.Known {
						tres = res.Result
					} else {
						if ctx.Err() != nil {
							cancelled.Add(1)
							return
						}
						// Leave unacked for lease recovery.
						skipped.Add(1)
						continue
					}
				}
				switch tres.FinalState {
				case crawl.WorkHandled:
					handled.Add(1)
				case crawl.WorkFailed, crawl.WorkRetryExhausted:
					failed.Add(1)
				case crawl.WorkRetryScheduled:
					retried.Add(1)
				default:
					skipped.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	var firstErr error
	for err := range errCh {
		if firstErr == nil {
			firstErr = err
		}
	}
	if ctx.Err() != nil {
		stopReason = crawl.CodeCancelled
		if ctx.Err() == context.DeadlineExceeded {
			stopReason = crawl.CodeRequestTimeout
		}
	} else if cfg.UntilDrained {
		stopReason = crawl.CodeDrained
	}
	report := crawl.NewRunReport(
		int(handled.Load()),
		int(failed.Load()),
		int(retried.Load()),
		int(cancelled.Load()),
		int(skipped.Load()),
		int(fetched.Load()),
		stopReason,
		time.Since(start),
	)
	return report, firstErr
}
