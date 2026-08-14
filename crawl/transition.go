package crawl

import (
	"context"
	"fmt"
	"time"
)

// DecisionKind selects the terminal work transition.
type DecisionKind uint8

const (
	DecisionUnspecified DecisionKind = iota
	DecisionAck
	DecisionRetry
	DecisionFail
)

// Decision is a constructor-created work transition.
//
// Handlers return Ack, Retry, or Fail. To persist page evidence in the same
// adapter transaction as the work row, attach a commit hook:
//
//	return crawl.Ack().WithCommit(func(ctx context.Context) error {
//	    return db.WriteEvidence(ctx, page)
//	})
//
// The engine copies Decision.Commit onto TransitionRequest. The Frontier
// adapter invokes it; the engine never does. Do not write evidence as a
// Handle aftereffect — the work fence has not settled yet.
type Decision struct {
	kind       DecisionKind
	retryAfter time.Duration
	code       ErrorCode
	commit     func(context.Context) error
}

// Ack acknowledges successful handling. Chain WithCommit to persist evidence
// when the adapter settles the work.
func Ack() Decision {
	return Decision{kind: DecisionAck}
}

// Retry schedules a retry after the given delay.
func Retry(after time.Duration, code ErrorCode) Decision {
	return Decision{kind: DecisionRetry, retryAfter: after, code: code}
}

// Fail marks work as permanently failed.
func Fail(code ErrorCode) Decision {
	return Decision{kind: DecisionFail, code: code}
}

// Kind returns the decision kind.
func (d Decision) Kind() DecisionKind { return d.kind }

// RetryAfter returns the retry delay.
func (d Decision) RetryAfter() time.Duration { return d.retryAfter }

// Code returns the associated error code.
func (d Decision) Code() ErrorCode { return d.code }

// WithCommit returns a copy of d with an adapter-owned commit hook.
//
// The Frontier calls fn at most once per new OperationID, never on replay
// or a stale fence. fn must be idempotent: if the fence is lost after fn
// succeeds, Transition returns ErrLeaseConflict and fn has already run.
// If fn fails, work stays leased for recovery. Durable adapters run fn in
// the same transaction as the work row and receipt.
func (d Decision) WithCommit(fn func(context.Context) error) Decision {
	d.commit = fn
	return d
}

// Commit returns the hook set by WithCommit, or nil.
// The engine assigns this to TransitionRequest.Commit; adapters read it
// from the request, not from Decision, when implementing Transition.
func (d Decision) Commit() func(context.Context) error { return d.commit }

// Validate checks decision completeness. The commit hook is ignored.
func (d Decision) Validate() error {
	switch d.kind {
	case DecisionAck:
		return nil
	case DecisionRetry:
		if d.retryAfter < 0 {
			return fmt.Errorf("%w: negative retry", ErrInvalidDecision)
		}
		if d.code != "" {
			if err := d.code.Validate(); err != nil {
				return err
			}
		}
		return nil
	case DecisionFail:
		if err := d.code.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDecision, err)
		}
		return nil
	default:
		return ErrInvalidDecision
	}
}

// TransitionRequest is the input to WorkFrontier.Transition.
type TransitionRequest struct {
	OperationID OperationID
	Lease       Lease
	Decision    Decision
	// Commit is optional. The engine sets it from Decision.Commit().
	// Adapters must call it at most once per new OperationID, skip it on
	// replay and stale fences, and treat it as idempotent. Run it in the
	// same transaction as the work row and receipt. On error, leave work
	// leased and do not record a receipt.
	Commit func(context.Context) error
}

// TransitionApplyState classifies transition application.
type TransitionApplyState uint8

const (
	TransitionApplyUnspecified TransitionApplyState = iota
	TransitionApplied
	TransitionAlreadyApplied
)

// FinalWorkState is the durable work state after transition.
type FinalWorkState uint8

const (
	FinalWorkUnspecified FinalWorkState = iota
	WorkHandled
	WorkRetryScheduled
	WorkFailed
	WorkRetryExhausted
)

// TransitionResult is returned by Transition.
type TransitionResult struct {
	ApplyState TransitionApplyState
	FinalState FinalWorkState
	Code       ErrorCode
}

// TransitionResolution resolves an ambiguous transition.
type TransitionResolution struct {
	Known  bool
	Result TransitionResult
}

// DefaultDecision maps a fetch outcome to a sensible transition decision for
// handlers that do not need outcome-specific evidence handling:
// transient outcomes retry, all others fail with their stable code.
// Handlers remain authoritative and may override any mapping.
func DefaultDecision(res FetchResult) Decision {
	retryAfter := time.Second
	switch res.Outcome() {
	case FetchOutcomeCancelled,
		FetchOutcomeTimedOut,
		FetchOutcomeLeaseLost,
		FetchOutcomeRobotsUnavailable,
		FetchOutcomeReservationDeferred:
		code := res.ErrorCode()
		if code == "" {
			code = CodeTransportFailed
		}
		return Retry(retryAfter, code)
	case FetchOutcomeHTTPResponse:
		return Ack()
	default:
		code := res.ErrorCode()
		if code == "" {
			code = CodeTransportFailed
		}
		return Fail(code)
	}
}
