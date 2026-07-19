package crawl

import (
	"context"
	"time"
)

// FetchPurpose classifies why a physical request is made.
type FetchPurpose uint8

const (
	FetchPurposeUnspecified FetchPurpose = iota
	FetchPurposeWork
	FetchPurposeRobots
	FetchPurposeRobotsRedirect
)

// FetchIntent describes one physical request reservation.
type FetchIntent struct {
	URL            string
	Method         Method
	Purpose        FetchPurpose
	ResourceClass  ResourceClass
	MinimumDelay   time.Duration
	RedirectNumber uint16
}

// ReserveFetchRequest reserves capacity for one physical request.
type ReserveFetchRequest struct {
	OperationID   OperationID
	Intent        FetchIntent
	LeaseDuration time.Duration
}

// ReserveFetchState classifies reservation outcomes.
type ReserveFetchState uint8

const (
	ReserveFetchUnspecified ReserveFetchState = iota
	FetchReserved
	FetchDeferred
	FetchDenied
	FetchBudgetExhausted
)

// FetchReservation is an exclusive physical-request permit.
type FetchReservation struct {
	id             OperationID
	leaseExpiresAt time.Time
	renewAfter     time.Duration
}

// NewFetchReservation constructs a reservation.
func NewFetchReservation(id OperationID, expiresAt time.Time, renewAfter time.Duration) FetchReservation {
	return FetchReservation{id: id, leaseExpiresAt: expiresAt, renewAfter: renewAfter}
}

func (r FetchReservation) ID() OperationID           { return r.id }
func (r FetchReservation) LeaseExpiresAt() time.Time { return r.leaseExpiresAt }
func (r FetchReservation) RenewAfter() time.Duration { return r.renewAfter }

// ReserveFetchResult is returned by ReserveFetch.
type ReserveFetchResult struct {
	State       ReserveFetchState
	Reservation FetchReservation
	RetryAfter  time.Duration
	Code        ErrorCode
}

// RenewFetchRequest renews a fetch reservation.
type RenewFetchRequest struct {
	OperationID   OperationID
	Reservation   FetchReservation
	LeaseDuration time.Duration
}

// FetchReport accounts for a finished physical request.
type FetchReport struct {
	Outcome      FetchOutcome
	StatusClass  uint8
	Duration     time.Duration
	WireBytes    int64
	DecodedBytes int64
}

// FinishFetchRequest settles a reservation.
type FinishFetchRequest struct {
	OperationID OperationID
	Reservation FetchReservation
	Report      FetchReport
}

// FetchReservations is the distributed physical-request permit port.
type FetchReservations interface {
	ReserveFetch(ctx context.Context, lease Lease, req ReserveFetchRequest) (ReserveFetchResult, error)
	RenewFetch(ctx context.Context, lease Lease, req RenewFetchRequest) (FetchReservation, error)
	FinishFetch(ctx context.Context, lease Lease, req FinishFetchRequest) error
}
