package crawl

import "context"

// WorkFrontier is the durable crawl work lifecycle port.
type WorkFrontier interface {
	EnqueueSeed(ctx context.Context, request EnqueueRequest) (EnqueueResult, error)
	EnqueueDiscovered(ctx context.Context, lease Lease, request EnqueueRequest) (EnqueueResult, error)
	SealSeeds(ctx context.Context) error

	Claim(ctx context.Context, request ClaimRequest) (ClaimResult, error)
	Renew(ctx context.Context, request RenewLeaseRequest) (Lease, error)

	Transition(ctx context.Context, request TransitionRequest) (TransitionResult, error)
	ResolveTransition(ctx context.Context, workID WorkID, op OperationID) (TransitionResolution, error)

	Stats(ctx context.Context) (FrontierStats, error)
}

// Frontier composes work lifecycle, fetch reservations, and robots coordination.
// Platform adapters implement the full composed contract under one scoped runtime.
type Frontier interface {
	WorkFrontier
	FetchReservations
	RobotsCoordinator
}
