package crawl

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrorCode is a bounded stable classification safe to persist and log.
type ErrorCode string

const (
	CodeNone                  ErrorCode = ""
	CodeScopeDenied           ErrorCode = "scope_denied"
	CodeSchemeDenied          ErrorCode = "scheme_denied"
	CodeHostDenied            ErrorCode = "host_denied"
	CodePortDenied            ErrorCode = "port_denied"
	CodePathDenied            ErrorCode = "path_denied"
	CodeMethodDenied          ErrorCode = "method_denied"
	CodeIdentityRejected      ErrorCode = "identity_rejected"
	CodeIdentityMismatch      ErrorCode = "identity_mismatch"
	CodeDNSFailed             ErrorCode = "dns_failed"
	CodeAddressDenied         ErrorCode = "address_denied"
	CodeConnectFailed         ErrorCode = "connect_failed"
	CodeTLSFailed             ErrorCode = "tls_failed"
	CodeProtocolFailed        ErrorCode = "protocol_failed"
	CodeRequestTimeout        ErrorCode = "request_timeout"
	CodeCancelled             ErrorCode = "cancelled"
	CodeRobotsDenied          ErrorCode = "robots_denied"
	CodeRobotsUnavailable     ErrorCode = "robots_unavailable"
	CodeRobotsInvalid         ErrorCode = "robots_invalid"
	CodeRedirectInvalid       ErrorCode = "redirect_invalid"
	CodeRedirectOutOfScope    ErrorCode = "redirect_out_of_scope"
	CodeRedirectLoop          ErrorCode = "redirect_loop"
	CodeRedirectLimit         ErrorCode = "redirect_limit"
	CodeHeaderTooLarge        ErrorCode = "header_too_large"
	CodeWireBodyTooLarge      ErrorCode = "wire_body_too_large"
	CodeDecodedBodyTooLarge   ErrorCode = "decoded_body_too_large"
	CodeConvertedTextTooLarge ErrorCode = "converted_text_too_large"
	CodeContentDecodeFailed   ErrorCode = "content_decode_failed"
	CodeUnsupportedEncoding   ErrorCode = "unsupported_content_encoding"
	CodeBudgetExhausted       ErrorCode = "budget_exhausted"
	CodeReservationDeferred   ErrorCode = "reservation_deferred"
	CodeReservationDenied     ErrorCode = "reservation_denied"
	CodeLeaseLost             ErrorCode = "lease_lost"
	CodeLeaseConflict         ErrorCode = "lease_conflict"
	CodeHandlerFailed         ErrorCode = "handler_failed"
	CodeAdapterFailure        ErrorCode = "adapter_failure"
	CodeInvalidDecision       ErrorCode = "invalid_decision"
	CodeDiscoveryUnresolved   ErrorCode = "discovery_unresolved"
	CodeTransportFailed       ErrorCode = "transport_failed"
	CodePolicyDenied          ErrorCode = "policy_denied"
	CodeRetryExhausted        ErrorCode = "retry_exhausted"
	CodeOperationConflict     ErrorCode = "operation_conflict"
	CodeInvalidConfig         ErrorCode = "invalid_config"
	CodeInvalidWork           ErrorCode = "invalid_work"
	CodeDrained               ErrorCode = "drained"
)

var errorCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// Validate returns an error if the code is not a safe bounded value.
func (c ErrorCode) Validate() error {
	if c == "" {
		return errors.New("error code is empty")
	}
	if !errorCodePattern.MatchString(string(c)) {
		return fmt.Errorf("invalid error code %q", c)
	}
	return nil
}

// String returns the code text.
func (c ErrorCode) String() string { return string(c) }

// FetchOutcome classifies the result of one physical fetch attempt.
type FetchOutcome uint8

const (
	FetchOutcomeUnspecified FetchOutcome = iota
	FetchOutcomeHTTPResponse
	FetchOutcomeRobotsDenied
	FetchOutcomeRobotsUnavailable
	FetchOutcomePolicyDenied
	FetchOutcomeBudgetExhausted
	FetchOutcomeReservationDeferred
	FetchOutcomeCancelled
	FetchOutcomeTimedOut
	FetchOutcomeLeaseLost
	FetchOutcomeDNSFailed
	FetchOutcomeAddressDenied
	FetchOutcomeConnectFailed
	FetchOutcomeTLSFailed
	FetchOutcomeProtocolFailed
	FetchOutcomeHeaderTooLarge
	FetchOutcomeWireBodyTooLarge
	FetchOutcomeDecodedBodyTooLarge
	FetchOutcomeConvertedTextTooLarge
	FetchOutcomeContentDecodeFailed
	FetchOutcomeTransportFailed
)

// String returns a stable outcome name.
func (o FetchOutcome) String() string {
	switch o {
	case FetchOutcomeHTTPResponse:
		return "http_response"
	case FetchOutcomeRobotsDenied:
		return "robots_denied"
	case FetchOutcomeRobotsUnavailable:
		return "robots_unavailable"
	case FetchOutcomePolicyDenied:
		return "policy_denied"
	case FetchOutcomeBudgetExhausted:
		return "budget_exhausted"
	case FetchOutcomeReservationDeferred:
		return "reservation_deferred"
	case FetchOutcomeCancelled:
		return "cancelled"
	case FetchOutcomeTimedOut:
		return "timed_out"
	case FetchOutcomeLeaseLost:
		return "lease_lost"
	case FetchOutcomeDNSFailed:
		return "dns_failed"
	case FetchOutcomeAddressDenied:
		return "address_denied"
	case FetchOutcomeConnectFailed:
		return "connect_failed"
	case FetchOutcomeTLSFailed:
		return "tls_failed"
	case FetchOutcomeProtocolFailed:
		return "protocol_failed"
	case FetchOutcomeHeaderTooLarge:
		return "header_too_large"
	case FetchOutcomeWireBodyTooLarge:
		return "wire_body_too_large"
	case FetchOutcomeDecodedBodyTooLarge:
		return "decoded_body_too_large"
	case FetchOutcomeConvertedTextTooLarge:
		return "converted_text_too_large"
	case FetchOutcomeContentDecodeFailed:
		return "content_decode_failed"
	case FetchOutcomeTransportFailed:
		return "transport_failed"
	default:
		return "unspecified"
	}
}

// ErrorCode maps the outcome to a stable error code when the outcome is a failure class.
func (o FetchOutcome) ErrorCode() ErrorCode {
	switch o {
	case FetchOutcomeRobotsDenied:
		return CodeRobotsDenied
	case FetchOutcomeRobotsUnavailable:
		return CodeRobotsUnavailable
	case FetchOutcomePolicyDenied:
		return CodePolicyDenied
	case FetchOutcomeBudgetExhausted:
		return CodeBudgetExhausted
	case FetchOutcomeReservationDeferred:
		return CodeReservationDeferred
	case FetchOutcomeCancelled:
		return CodeCancelled
	case FetchOutcomeTimedOut:
		return CodeRequestTimeout
	case FetchOutcomeLeaseLost:
		return CodeLeaseLost
	case FetchOutcomeDNSFailed:
		return CodeDNSFailed
	case FetchOutcomeAddressDenied:
		return CodeAddressDenied
	case FetchOutcomeConnectFailed:
		return CodeConnectFailed
	case FetchOutcomeTLSFailed:
		return CodeTLSFailed
	case FetchOutcomeProtocolFailed:
		return CodeProtocolFailed
	case FetchOutcomeHeaderTooLarge:
		return CodeHeaderTooLarge
	case FetchOutcomeWireBodyTooLarge:
		return CodeWireBodyTooLarge
	case FetchOutcomeDecodedBodyTooLarge:
		return CodeDecodedBodyTooLarge
	case FetchOutcomeConvertedTextTooLarge:
		return CodeConvertedTextTooLarge
	case FetchOutcomeContentDecodeFailed:
		return CodeContentDecodeFailed
	case FetchOutcomeTransportFailed:
		return CodeTransportFailed
	default:
		return CodeNone
	}
}

// Stable errors returned by Kumo.
var (
	ErrInvalidConfig     = errors.New("invalid configuration")
	ErrInvalidDecision   = errors.New("invalid work decision")
	ErrLeaseLost         = errors.New("lease lost")
	ErrLeaseConflict     = errors.New("lease conflict")
	ErrBudgetExhausted   = errors.New("budget exhausted")
	ErrOperationConflict = errors.New("operation conflict")
	ErrDrained           = errors.New("frontier drained")
	ErrCancelled         = errors.New("cancelled")
	ErrIdentityMismatch  = errors.New("identity mismatch")
)
