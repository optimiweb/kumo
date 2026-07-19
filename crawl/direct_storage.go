package crawl

import (
	"context"
	"time"
)

// DirectClaimToken is an unguessable direct claim token.
type DirectClaimToken string

// DirectClaimStatus classifies direct claim outcomes.
type DirectClaimStatus uint8

const (
	DirectClaimUnspecified DirectClaimStatus = iota
	DirectClaimAcquired
	DirectClaimBusy
	DirectClaimTerminal
)

// DirectTerminalKind classifies terminal direct outcomes.
type DirectTerminalKind uint8

const (
	DirectTerminalUnspecified DirectTerminalKind = iota
	DirectTerminalHandled
	DirectTerminalFailed
)

// DirectClaim is an exclusive direct-mode ownership proof.
type DirectClaim struct {
	Key            IdentityKey
	Token          DirectClaimToken
	Generation     uint64
	LeaseExpiresAt time.Time
	RenewAfter     time.Duration
}

// DirectClaimRequest acquires a direct claim.
type DirectClaimRequest struct {
	OperationID   OperationID
	Key           IdentityKey
	LeaseDuration time.Duration
}

// DirectClaimResult is returned by Claim.
type DirectClaimResult struct {
	Status   DirectClaimStatus
	Claim    DirectClaim
	Terminal DirectTerminalKind
}

// DirectRenewRequest renews a direct claim.
type DirectRenewRequest struct {
	OperationID   OperationID
	Claim         DirectClaim
	LeaseDuration time.Duration
}

// DirectFinalizeRequest terminalizes a claim.
type DirectFinalizeRequest struct {
	OperationID OperationID
	Claim       DirectClaim
	Terminal    DirectTerminalKind
}

// DirectFinalizeResult reports finalize application.
type DirectFinalizeResult struct {
	Applied bool
}

// DirectReleaseRequest releases a non-terminal claim for retry.
type DirectReleaseRequest struct {
	OperationID OperationID
	Claim       DirectClaim
}

// DirectReleaseResult reports release application.
type DirectReleaseResult struct {
	Applied bool
}

// DirectStorage is the direct-mode atomic claim port.
type DirectStorage interface {
	Claim(context.Context, DirectClaimRequest) (DirectClaimResult, error)
	Renew(context.Context, DirectRenewRequest) (DirectClaim, error)
	Finalize(context.Context, DirectFinalizeRequest) (DirectFinalizeResult, error)
	Release(context.Context, DirectReleaseRequest) (DirectReleaseResult, error)
}
