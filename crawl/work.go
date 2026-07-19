package crawl

import (
	"fmt"
	"time"
)

// WorkID uniquely identifies frontier work within an adapter scope.
type WorkID string

// LeaseToken is an unguessable lease ownership token.
type LeaseToken string

// OperationID is a caller-generated idempotency key.
type OperationID [16]byte

// SourceCode classifies how work was discovered.
type SourceCode string

const (
	SourceSeed      SourceCode = "seed"
	SourceLink      SourceCode = "link"
	SourceRedirect  SourceCode = "redirect"
	SourceSitemap   SourceCode = "sitemap"
	SourceRobots    SourceCode = "robots"
	SourceCanonical SourceCode = "canonical"
	SourceHreflang  SourceCode = "hreflang"
)

// Validate reports whether the source is known.
func (s SourceCode) Validate() error {
	switch s {
	case SourceSeed, SourceLink, SourceRedirect, SourceSitemap, SourceRobots, SourceCanonical, SourceHreflang:
		return nil
	default:
		return fmt.Errorf("%w: source", ErrInvalidConfig)
	}
}

// Work is the only durable crawl unit accepted by runners.
type Work struct {
	id            WorkID
	identity      URLIdentity
	method        Method
	depth         uint32
	redirectHops  uint16
	parentID      WorkID
	source        SourceCode
	priority      int32
	resourceClass ResourceClass
}

// NewWork constructs validated work.
func NewWork(
	id WorkID,
	identity URLIdentity,
	method Method,
	depth uint32,
	redirectHops uint16,
	parentID WorkID,
	source SourceCode,
	priority int32,
	resourceClass ResourceClass,
) (Work, error) {
	if id == "" {
		return Work{}, fmt.Errorf("%w: empty work id", ErrInvalidConfig)
	}
	if identity.URL() == "" {
		return Work{}, fmt.Errorf("%w: empty identity", ErrInvalidConfig)
	}
	if err := method.Validate(); err != nil {
		return Work{}, err
	}
	if err := source.Validate(); err != nil {
		return Work{}, err
	}
	if err := resourceClass.Validate(); err != nil {
		return Work{}, err
	}
	return Work{
		id:            id,
		identity:      identity,
		method:        method,
		depth:         depth,
		redirectHops:  redirectHops,
		parentID:      parentID,
		source:        source,
		priority:      priority,
		resourceClass: resourceClass,
	}, nil
}

func (w Work) ID() WorkID                   { return w.id }
func (w Work) Identity() URLIdentity        { return w.identity }
func (w Work) Method() Method               { return w.method }
func (w Work) Depth() uint32                { return w.depth }
func (w Work) RedirectHops() uint16         { return w.redirectHops }
func (w Work) ParentID() WorkID             { return w.parentID }
func (w Work) Source() SourceCode           { return w.source }
func (w Work) Priority() int32              { return w.priority }
func (w Work) ResourceClass() ResourceClass { return w.resourceClass }
func (w Work) URL() string                  { return w.identity.URL() }

// Fence is the exclusive ownership proof for a work lease.
type Fence struct {
	workID     WorkID
	token      LeaseToken
	generation uint64
}

// NewFence constructs a fence.
func NewFence(workID WorkID, token LeaseToken, generation uint64) (Fence, error) {
	if workID == "" || token == "" || generation == 0 {
		return Fence{}, fmt.Errorf("%w: fence", ErrInvalidConfig)
	}
	return Fence{workID: workID, token: token, generation: generation}, nil
}

func (f Fence) WorkID() WorkID     { return f.workID }
func (f Fence) Token() LeaseToken  { return f.token }
func (f Fence) Generation() uint64 { return f.generation }

// Lease is an exclusive claim over one work item.
type Lease struct {
	work           Work
	fence          Fence
	attempt        uint32
	maxAttempts    uint32
	leaseExpiresAt time.Time
	renewAfter     time.Duration
}

// NewLease constructs a lease.
func NewLease(work Work, fence Fence, attempt, maxAttempts uint32, expiresAt time.Time, renewAfter time.Duration) (Lease, error) {
	if fence.WorkID() != work.ID() {
		return Lease{}, fmt.Errorf("%w: fence work mismatch", ErrInvalidConfig)
	}
	if attempt == 0 || maxAttempts == 0 || attempt > maxAttempts {
		return Lease{}, fmt.Errorf("%w: attempt bounds", ErrInvalidConfig)
	}
	if expiresAt.IsZero() || renewAfter <= 0 {
		return Lease{}, fmt.Errorf("%w: lease timing", ErrInvalidConfig)
	}
	return Lease{
		work:           work,
		fence:          fence,
		attempt:        attempt,
		maxAttempts:    maxAttempts,
		leaseExpiresAt: expiresAt,
		renewAfter:     renewAfter,
	}, nil
}

func (l Lease) Work() Work                { return l.work }
func (l Lease) Fence() Fence              { return l.fence }
func (l Lease) Attempt() uint32           { return l.attempt }
func (l Lease) MaxAttempts() uint32       { return l.maxAttempts }
func (l Lease) LeaseExpiresAt() time.Time { return l.leaseExpiresAt }
func (l Lease) RenewAfter() time.Duration { return l.renewAfter }

// EnqueueRequest describes eligible discovered or seed work.
type EnqueueRequest struct {
	Identity      URLIdentity
	Method        Method
	Depth         uint32
	RedirectHops  uint16
	ParentID      WorkID
	Source        SourceCode
	Priority      int32
	ResourceClass ResourceClass
	AvailableAt   time.Time
}

// Validate checks enqueue fields.
func (r EnqueueRequest) Validate() error {
	if r.Identity.URL() == "" {
		return fmt.Errorf("%w: identity", ErrInvalidConfig)
	}
	if err := r.Method.Validate(); err != nil {
		return err
	}
	if err := r.Source.Validate(); err != nil {
		return err
	}
	if err := r.ResourceClass.Validate(); err != nil {
		return err
	}
	return nil
}

// EnqueueResult is the outcome of enqueueing work.
type EnqueueResult struct {
	ID       WorkID
	Inserted bool
}

// ClaimRequest configures a work claim.
type ClaimRequest struct {
	OperationID   OperationID
	LeaseDuration time.Duration
}

// FrontierClaimState classifies claim results.
type FrontierClaimState uint8

const (
	FrontierClaimUnspecified FrontierClaimState = iota
	FrontierLeased
	FrontierIdle
	FrontierDrained
)

// ClaimResult is returned by Frontier.Claim.
type ClaimResult struct {
	State      FrontierClaimState
	Lease      Lease
	RetryAfter time.Duration
}

// RenewLeaseRequest renews a work lease.
type RenewLeaseRequest struct {
	OperationID   OperationID
	Lease         Lease
	LeaseDuration time.Duration
}

// FrontierStats is advisory observability only.
type FrontierStats struct {
	Ready   int
	Leased  int
	Delayed int
	Total   int
	Sealed  bool
}
