package crawl

import (
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

// Decision is a constructor-created work transition request.
type Decision struct {
	kind       DecisionKind
	retryAfter time.Duration
	code       ErrorCode
}

// Ack acknowledges successful handling.
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

// Validate checks decision completeness.
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

// TransitionRequest commits a handler decision.
type TransitionRequest struct {
	OperationID OperationID
	Lease       Lease
	Decision    Decision
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
