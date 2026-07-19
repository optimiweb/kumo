package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/optimiweb/kumo/crawl"
)

// MemoryFrontierOptions configures the in-memory frontier.
type MemoryFrontierOptions struct {
	Clock          Clock
	MaxAttempts    uint32
	MaxPages       int
	FetchBudget    int
	MaxOriginConc  int
	RatePerOrigin  float64
	BurstPerOrigin float64
}

// MemoryFrontier is a deterministic in-memory Frontier implementation.
type MemoryFrontier struct {
	mu sync.Mutex

	clock Clock

	items       map[crawl.WorkID]*mfWorkItem
	byKey       map[crawl.IdentityKey]crawl.WorkID
	nextID      uint64
	sealed      bool
	maxAttempts uint32
	maxPages    int

	transitions map[string]mfTransitionRecord

	fetchBudget    int
	fetchUsed      int
	maxOriginConc  int
	originActive   map[string]int
	reservations   map[string]*mfFetchRes
	ratePerOrigin  float64
	burstPerOrigin float64
	originTokens   map[string]*mfTokenBucket
	originNext     map[string]time.Time

	robotsCache map[crawl.RobotsKey]*mfRobotsEntry
	robotsOps   map[string]struct{}
}

type mfWorkState uint8

const (
	mfReady mfWorkState = iota
	mfLeased
	mfRetryWait
	mfHandled
	mfFailed
)

type mfWorkItem struct {
	work       crawl.Work
	state      mfWorkState
	available  time.Time
	attempt    uint32
	maxAttempt uint32
	token      crawl.LeaseToken
	generation uint64
	expiresAt  time.Time
	priority   int32
}

// mfTransitionRecord binds an applied transition to its work item.
type mfTransitionRecord struct {
	workID crawl.WorkID
	result crawl.TransitionResult
}

type mfFetchRes struct {
	id         crawl.OperationID
	workID     crawl.WorkID
	token      crawl.LeaseToken
	generation uint64
	origin     string
	expiresAt  time.Time
	finished   bool
	report     crawl.FetchReport
}

type mfTokenBucket struct {
	tokens     float64
	lastRefill time.Time
	rate       float64
	burst      float64
}

type mfRobotsEntry struct {
	record     crawl.RobotsRecord
	token      crawl.RobotsToken
	generation uint64
	expiresAt  time.Time
	leased     bool
}

// NewMemoryFrontier constructs an in-memory frontier.
func NewMemoryFrontier(opts MemoryFrontierOptions) *MemoryFrontier {
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 3
	}
	if opts.MaxPages == 0 {
		opts.MaxPages = 1000
	}
	if opts.FetchBudget == 0 {
		opts.FetchBudget = 10000
	}
	if opts.MaxOriginConc == 0 {
		opts.MaxOriginConc = 2
	}
	if opts.RatePerOrigin <= 0 {
		opts.RatePerOrigin = 100
	}
	if opts.BurstPerOrigin <= 0 {
		opts.BurstPerOrigin = opts.RatePerOrigin
	}
	return &MemoryFrontier{
		clock:          opts.Clock,
		items:          make(map[crawl.WorkID]*mfWorkItem),
		byKey:          make(map[crawl.IdentityKey]crawl.WorkID),
		transitions:    make(map[string]mfTransitionRecord),
		maxAttempts:    opts.MaxAttempts,
		maxPages:       opts.MaxPages,
		fetchBudget:    opts.FetchBudget,
		maxOriginConc:  opts.MaxOriginConc,
		originActive:   make(map[string]int),
		reservations:   make(map[string]*mfFetchRes),
		ratePerOrigin:  opts.RatePerOrigin,
		burstPerOrigin: opts.BurstPerOrigin,
		originTokens:   make(map[string]*mfTokenBucket),
		originNext:     make(map[string]time.Time),
		robotsCache:    make(map[crawl.RobotsKey]*mfRobotsEntry),
		robotsOps:      make(map[string]struct{}),
	}
}

func mfOpHex(op crawl.OperationID) string { return hex.EncodeToString(op[:]) }

func mfNewToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (m *MemoryFrontier) nextWorkID() crawl.WorkID {
	m.nextID++
	return crawl.WorkID(fmt.Sprintf("w-%d", m.nextID))
}

// EnqueueSeed implements WorkFrontier.
func (m *MemoryFrontier) EnqueueSeed(ctx context.Context, req crawl.EnqueueRequest) (crawl.EnqueueResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.EnqueueResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		return crawl.EnqueueResult{}, fmt.Errorf("%w: seeds sealed", crawl.ErrInvalidConfig)
	}
	return m.enqueueLocked(req)
}

// EnqueueDiscovered implements WorkFrontier.
func (m *MemoryFrontier) EnqueueDiscovered(ctx context.Context, lease crawl.Lease, req crawl.EnqueueRequest) (crawl.EnqueueResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.EnqueueResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateLeaseLocked(lease); err != nil {
		return crawl.EnqueueResult{}, err
	}
	return m.enqueueLocked(req)
}

func (m *MemoryFrontier) enqueueLocked(req crawl.EnqueueRequest) (crawl.EnqueueResult, error) {
	if err := req.Validate(); err != nil {
		return crawl.EnqueueResult{}, err
	}
	if id, ok := m.byKey[req.Identity.Key()]; ok {
		return crawl.EnqueueResult{ID: id, Inserted: false}, nil
	}
	if len(m.items) >= m.maxPages {
		return crawl.EnqueueResult{}, crawl.ErrBudgetExhausted
	}
	id := m.nextWorkID()
	work, err := crawl.NewWork(id, req.Identity, req.Method, req.Depth, req.RedirectHops, req.ParentID, req.Source, req.Priority, req.ResourceClass)
	if err != nil {
		return crawl.EnqueueResult{}, err
	}
	avail := req.AvailableAt
	if avail.IsZero() {
		avail = m.clock.Now()
	}
	m.items[id] = &mfWorkItem{
		work:       work,
		state:      mfReady,
		available:  avail,
		maxAttempt: m.maxAttempts,
		priority:   req.Priority,
	}
	m.byKey[req.Identity.Key()] = id
	return crawl.EnqueueResult{ID: id, Inserted: true}, nil
}

// SealSeeds implements WorkFrontier.
func (m *MemoryFrontier) SealSeeds(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sealed = true
	return nil
}

// Claim implements WorkFrontier.
func (m *MemoryFrontier) Claim(ctx context.Context, req crawl.ClaimRequest) (crawl.ClaimResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.ClaimResult{}, err
	}
	if req.LeaseDuration <= 0 {
		return crawl.ClaimResult{}, fmt.Errorf("%w: lease duration", crawl.ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	m.reclaimExpiredLocked(now)

	var candidates []*mfWorkItem
	for _, it := range m.items {
		if (it.state == mfReady || it.state == mfRetryWait) && !it.available.After(now) {
			candidates = append(candidates, it)
		}
	}
	if len(candidates) == 0 {
		if m.isDrainedLocked() {
			return crawl.ClaimResult{State: crawl.FrontierDrained}, nil
		}
		return crawl.ClaimResult{State: crawl.FrontierIdle, RetryAfter: 50 * time.Millisecond}, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].available.Equal(candidates[j].available) {
			if candidates[i].priority == candidates[j].priority {
				return candidates[i].work.ID() < candidates[j].work.ID()
			}
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].available.Before(candidates[j].available)
	})
	it := candidates[0]
	tok, err := mfNewToken()
	if err != nil {
		return crawl.ClaimResult{}, err
	}
	it.attempt++
	if it.attempt > it.maxAttempt {
		it.state = mfFailed
		if m.isDrainedLocked() {
			return crawl.ClaimResult{State: crawl.FrontierDrained}, nil
		}
		return crawl.ClaimResult{State: crawl.FrontierIdle}, nil
	}
	it.state = mfLeased
	it.token = crawl.LeaseToken(tok)
	it.generation++
	it.expiresAt = now.Add(req.LeaseDuration)
	fence, err := crawl.NewFence(it.work.ID(), it.token, it.generation)
	if err != nil {
		return crawl.ClaimResult{}, err
	}
	lease, err := crawl.NewLease(it.work, fence, it.attempt, it.maxAttempt, it.expiresAt, req.LeaseDuration/3)
	if err != nil {
		return crawl.ClaimResult{}, err
	}
	return crawl.ClaimResult{State: crawl.FrontierLeased, Lease: lease}, nil
}

func (m *MemoryFrontier) reclaimExpiredLocked(now time.Time) {
	for _, it := range m.items {
		if it.state == mfLeased && !it.expiresAt.After(now) {
			if it.attempt >= it.maxAttempt {
				it.state = mfFailed
			} else {
				it.state = mfRetryWait
				it.available = now
			}
			it.token = ""
		}
	}
	for k, r := range m.reservations {
		if !r.finished && !r.expiresAt.After(now) {
			m.releaseReservationLocked(r)
			delete(m.reservations, k)
		}
	}
	for k, e := range m.robotsCache {
		if e.leased && !e.expiresAt.After(now) {
			e.leased = false
			e.token = ""
			m.robotsCache[k] = e
		}
	}
}

func (m *MemoryFrontier) isDrainedLocked() bool {
	if !m.sealed {
		return false
	}
	for _, it := range m.items {
		switch it.state {
		case mfReady, mfLeased, mfRetryWait:
			return false
		}
	}
	return true
}

// Renew implements WorkFrontier.
func (m *MemoryFrontier) Renew(ctx context.Context, req crawl.RenewLeaseRequest) (crawl.Lease, error) {
	if err := ctx.Err(); err != nil {
		return crawl.Lease{}, err
	}
	if req.LeaseDuration <= 0 {
		return crawl.Lease{}, fmt.Errorf("%w: lease duration", crawl.ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, err := m.itemForLeaseLocked(req.Lease)
	if err != nil {
		return crawl.Lease{}, err
	}
	now := m.clock.Now()
	if !it.expiresAt.After(now) {
		return crawl.Lease{}, crawl.ErrLeaseLost
	}
	it.expiresAt = now.Add(req.LeaseDuration)
	fence, err := crawl.NewFence(it.work.ID(), it.token, it.generation)
	if err != nil {
		return crawl.Lease{}, err
	}
	return crawl.NewLease(it.work, fence, it.attempt, it.maxAttempt, it.expiresAt, req.LeaseDuration/3)
}

// Transition implements WorkFrontier.
func (m *MemoryFrontier) Transition(ctx context.Context, req crawl.TransitionRequest) (crawl.TransitionResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.TransitionResult{}, err
	}
	if err := req.Decision.Validate(); err != nil {
		return crawl.TransitionResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := mfOpHex(req.OperationID)
	if prev, ok := m.transitions[key]; ok {
		r := prev.result
		return crawl.TransitionResult{ApplyState: crawl.TransitionAlreadyApplied, FinalState: r.FinalState, Code: r.Code}, nil
	}
	it, err := m.itemForLeaseLocked(req.Lease)
	if err != nil {
		return crawl.TransitionResult{}, err
	}
	var final crawl.FinalWorkState
	var code crawl.ErrorCode
	switch req.Decision.Kind() {
	case crawl.DecisionAck:
		it.state = mfHandled
		final = crawl.WorkHandled
	case crawl.DecisionFail:
		it.state = mfFailed
		final = crawl.WorkFailed
		code = req.Decision.Code()
	case crawl.DecisionRetry:
		if it.attempt >= it.maxAttempt {
			it.state = mfFailed
			final = crawl.WorkRetryExhausted
			code = crawl.CodeRetryExhausted
		} else {
			it.state = mfRetryWait
			it.available = m.clock.Now().Add(req.Decision.RetryAfter())
			final = crawl.WorkRetryScheduled
			code = req.Decision.Code()
		}
	default:
		return crawl.TransitionResult{}, crawl.ErrInvalidDecision
	}
	it.token = ""
	res := crawl.TransitionResult{ApplyState: crawl.TransitionApplied, FinalState: final, Code: code}
	m.transitions[key] = mfTransitionRecord{workID: it.work.ID(), result: res}
	return res, nil
}

// ResolveTransition implements WorkFrontier.
func (m *MemoryFrontier) ResolveTransition(ctx context.Context, workID crawl.WorkID, op crawl.OperationID) (crawl.TransitionResolution, error) {
	if err := ctx.Err(); err != nil {
		return crawl.TransitionResolution{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.transitions[mfOpHex(op)]; ok && rec.workID == workID {
		return crawl.TransitionResolution{Known: true, Result: rec.result}, nil
	}
	return crawl.TransitionResolution{Known: false}, nil
}

// Stats implements WorkFrontier.
func (m *MemoryFrontier) Stats(ctx context.Context) (crawl.FrontierStats, error) {
	if err := ctx.Err(); err != nil {
		return crawl.FrontierStats{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var s crawl.FrontierStats
	s.Sealed = m.sealed
	now := m.clock.Now()
	for _, it := range m.items {
		s.Total++
		switch it.state {
		case mfReady:
			if !it.available.After(now) {
				s.Ready++
			} else {
				s.Delayed++
			}
		case mfRetryWait:
			s.Delayed++
		case mfLeased:
			s.Leased++
		}
	}
	return s, nil
}

func (m *MemoryFrontier) validateLeaseLocked(lease crawl.Lease) error {
	it, ok := m.items[lease.Work().ID()]
	if !ok || it.state != mfLeased {
		return crawl.ErrLeaseConflict
	}
	if it.token != lease.Fence().Token() || it.generation != lease.Fence().Generation() {
		return crawl.ErrLeaseConflict
	}
	if !it.expiresAt.After(m.clock.Now()) {
		return crawl.ErrLeaseLost
	}
	return nil
}

func (m *MemoryFrontier) itemForLeaseLocked(lease crawl.Lease) (*mfWorkItem, error) {
	if err := m.validateLeaseLocked(lease); err != nil {
		return nil, err
	}
	return m.items[lease.Work().ID()], nil
}

// ReserveFetch implements FetchReservations.
func (m *MemoryFrontier) ReserveFetch(ctx context.Context, lease crawl.Lease, req crawl.ReserveFetchRequest) (crawl.ReserveFetchResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.ReserveFetchResult{}, err
	}
	if req.LeaseDuration <= 0 {
		return crawl.ReserveFetchResult{}, fmt.Errorf("%w: lease duration", crawl.ErrInvalidConfig)
	}
	if err := req.Intent.Method.Validate(); err != nil {
		return crawl.ReserveFetchResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateLeaseLocked(lease); err != nil {
		return crawl.ReserveFetchResult{}, err
	}
	key := mfOpHex(req.OperationID)
	if prev, ok := m.reservations[key]; ok && !prev.finished {
		return crawl.ReserveFetchResult{
			State:       crawl.FetchReserved,
			Reservation: crawl.NewFetchReservation(prev.id, prev.expiresAt, req.LeaseDuration/3),
		}, nil
	}
	now := m.clock.Now()
	m.reclaimExpiredLocked(now)
	origin, err := crawl.OriginKey(req.Intent.URL)
	if err != nil {
		return crawl.ReserveFetchResult{State: crawl.FetchDenied, Code: crawl.CodePolicyDenied}, nil
	}
	if m.fetchUsed >= m.fetchBudget {
		return crawl.ReserveFetchResult{State: crawl.FetchBudgetExhausted, Code: crawl.CodeBudgetExhausted}, nil
	}
	if m.originActive[origin] >= m.maxOriginConc {
		return crawl.ReserveFetchResult{State: crawl.FetchDeferred, RetryAfter: 50 * time.Millisecond, Code: crawl.CodeReservationDeferred}, nil
	}
	// Crawl-delay: guarantee start-time spacing per origin. A delay can never
	// permanently defer the origin, because it is a time window, not a token
	// cost that could exceed bucket capacity.
	if next := m.originNext[origin]; now.Before(next) {
		return crawl.ReserveFetchResult{
			State:      crawl.FetchDeferred,
			RetryAfter: next.Sub(now),
			Code:       crawl.CodeReservationDeferred,
		}, nil
	}
	tb := m.bucketLocked(origin, now)
	if tb.tokens < 1 {
		wait := time.Duration((1-tb.tokens)/tb.rate*float64(time.Second)) + time.Millisecond
		return crawl.ReserveFetchResult{State: crawl.FetchDeferred, RetryAfter: wait, Code: crawl.CodeReservationDeferred}, nil
	}
	tb.tokens--
	if req.Intent.MinimumDelay > 0 {
		m.originNext[origin] = now.Add(req.Intent.MinimumDelay)
	}
	m.fetchUsed++
	m.originActive[origin]++
	expires := now.Add(req.LeaseDuration)
	m.reservations[key] = &mfFetchRes{
		id: req.OperationID, workID: lease.Work().ID(), token: lease.Fence().Token(),
		generation: lease.Fence().Generation(), origin: origin, expiresAt: expires,
	}
	return crawl.ReserveFetchResult{
		State:       crawl.FetchReserved,
		Reservation: crawl.NewFetchReservation(req.OperationID, expires, req.LeaseDuration/3),
	}, nil
}

func (m *MemoryFrontier) bucketLocked(origin string, now time.Time) *mfTokenBucket {
	tb, ok := m.originTokens[origin]
	if !ok {
		tb = &mfTokenBucket{tokens: m.burstPerOrigin, lastRefill: now, rate: m.ratePerOrigin, burst: m.burstPerOrigin}
		m.originTokens[origin] = tb
	}
	elapsed := now.Sub(tb.lastRefill).Seconds()
	if elapsed > 0 {
		tb.tokens += elapsed * tb.rate
		if tb.tokens > tb.burst {
			tb.tokens = tb.burst
		}
		tb.lastRefill = now
	}
	return tb
}

// RenewFetch implements FetchReservations.
func (m *MemoryFrontier) RenewFetch(ctx context.Context, lease crawl.Lease, req crawl.RenewFetchRequest) (crawl.FetchReservation, error) {
	if err := ctx.Err(); err != nil {
		return crawl.FetchReservation{}, err
	}
	if req.LeaseDuration <= 0 {
		return crawl.FetchReservation{}, fmt.Errorf("%w: lease duration", crawl.ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateLeaseLocked(lease); err != nil {
		return crawl.FetchReservation{}, err
	}
	res, ok := m.reservations[mfOpHex(req.Reservation.ID())]
	if !ok || res.finished {
		return crawl.FetchReservation{}, crawl.ErrLeaseConflict
	}
	if res.workID != lease.Work().ID() || res.token != lease.Fence().Token() || res.generation != lease.Fence().Generation() {
		return crawl.FetchReservation{}, crawl.ErrLeaseConflict
	}
	now := m.clock.Now()
	if !res.expiresAt.After(now) {
		return crawl.FetchReservation{}, crawl.ErrLeaseLost
	}
	res.expiresAt = now.Add(req.LeaseDuration)
	return crawl.NewFetchReservation(res.id, res.expiresAt, req.LeaseDuration/3), nil
}

// FinishFetch implements FetchReservations.
func (m *MemoryFrontier) FinishFetch(ctx context.Context, lease crawl.Lease, req crawl.FinishFetchRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.reservations[mfOpHex(req.Reservation.ID())]
	if !ok {
		return nil
	}
	if res.finished {
		return nil
	}
	if res.workID != lease.Work().ID() {
		return crawl.ErrLeaseConflict
	}
	res.finished = true
	res.report = req.Report
	m.releaseReservationLocked(res)
	return nil
}

func (m *MemoryFrontier) releaseReservationLocked(res *mfFetchRes) {
	if res == nil {
		return
	}
	if c := m.originActive[res.origin]; c > 0 {
		m.originActive[res.origin] = c - 1
	}
}

// AcquireRobots implements RobotsCoordinator.
func (m *MemoryFrontier) AcquireRobots(ctx context.Context, lease crawl.Lease, req crawl.AcquireRobotsRequest) (crawl.AcquireRobotsResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.AcquireRobotsResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateLeaseLocked(lease); err != nil {
		return crawl.AcquireRobotsResult{}, err
	}
	now := m.clock.Now()
	m.reclaimExpiredLocked(now)
	entry := m.robotsCache[req.Key]
	if entry != nil && !entry.leased && !entry.record.Expired(now) {
		return crawl.AcquireRobotsResult{State: crawl.RobotsCached, Record: entry.record}, nil
	}
	if entry != nil && entry.leased && entry.expiresAt.After(now) {
		return crawl.AcquireRobotsResult{State: crawl.RobotsBusy, RetryAfter: 50 * time.Millisecond}, nil
	}
	tok, err := mfNewToken()
	if err != nil {
		return crawl.AcquireRobotsResult{}, err
	}
	gen := uint64(1)
	if entry != nil {
		gen = entry.generation + 1
	}
	expires := now.Add(req.LeaseDuration)
	m.robotsCache[req.Key] = &mfRobotsEntry{
		token: crawl.RobotsToken(tok), generation: gen, expiresAt: expires, leased: true,
	}
	return crawl.AcquireRobotsResult{
		State: crawl.RobotsAcquired,
		Lease: crawl.NewRobotsLease(req.Key, crawl.RobotsToken(tok), gen, expires, req.LeaseDuration/3),
	}, nil
}

// RenewRobots implements RobotsCoordinator.
func (m *MemoryFrontier) RenewRobots(ctx context.Context, lease crawl.Lease, op crawl.OperationID, robotsLease crawl.RobotsLease, duration time.Duration) (crawl.RobotsLease, error) {
	if err := ctx.Err(); err != nil {
		return crawl.RobotsLease{}, err
	}
	if duration <= 0 {
		return crawl.RobotsLease{}, fmt.Errorf("%w: lease duration", crawl.ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateLeaseLocked(lease); err != nil {
		return crawl.RobotsLease{}, err
	}
	entry := m.robotsCache[robotsLease.Key()]
	if entry == nil || !entry.leased || entry.token != robotsLease.Token() || entry.generation != robotsLease.Generation() {
		return crawl.RobotsLease{}, crawl.ErrLeaseConflict
	}
	now := m.clock.Now()
	if !entry.expiresAt.After(now) {
		return crawl.RobotsLease{}, crawl.ErrLeaseLost
	}
	entry.expiresAt = now.Add(duration)
	return crawl.NewRobotsLease(robotsLease.Key(), entry.token, entry.generation, entry.expiresAt, duration/3), nil
}

// PublishRobots implements RobotsCoordinator.
func (m *MemoryFrontier) PublishRobots(ctx context.Context, lease crawl.Lease, op crawl.OperationID, robotsLease crawl.RobotsLease, record crawl.RobotsRecord, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateLeaseLocked(lease); err != nil {
		return err
	}
	key := mfOpHex(op)
	if _, ok := m.robotsOps[key]; ok {
		return nil
	}
	entry := m.robotsCache[robotsLease.Key()]
	if entry == nil || !entry.leased || entry.token != robotsLease.Token() || entry.generation != robotsLease.Generation() {
		return crawl.ErrLeaseConflict
	}
	now := m.clock.Now()
	if !entry.expiresAt.After(now) {
		return crawl.ErrLeaseLost
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	entry.record = crawl.NewRobotsRecord(
		record.Rules(), record.CrawlDelay(), record.Sitemaps(),
		now, ttl, record.Unavailable(), record.AllowAll(), record.DenyAll(),
	)
	entry.leased = false
	entry.token = ""
	m.robotsOps[key] = struct{}{}
	return nil
}

// ReleaseRobots implements RobotsCoordinator.
func (m *MemoryFrontier) ReleaseRobots(ctx context.Context, lease crawl.Lease, op crawl.OperationID, robotsLease crawl.RobotsLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.robotsCache[robotsLease.Key()]
	if entry == nil {
		return nil
	}
	if entry.leased && entry.token == robotsLease.Token() && entry.generation == robotsLease.Generation() {
		entry.leased = false
		entry.token = ""
	}
	return nil
}

// FetchUsed returns consumed fetch budget for tests.
func (m *MemoryFrontier) FetchUsed() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fetchUsed
}

// Ensure MemoryFrontier implements Frontier.
var _ crawl.Frontier = (*MemoryFrontier)(nil)
