package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/optimiweb/kumo/crawl"
)

// MemoryStorage is a deterministic in-memory DirectStorage implementation.
// It is the idempotency reference for Platform adapters:
//
//   - Replaying an operation ID returns the originally recorded result.
//   - Reusing an operation ID with a different payload is a conflict.
//   - Token plus generation fences every mutation.
type MemoryStorage struct {
	mu    sync.Mutex
	clock Clock
	items map[crawl.IdentityKey]*msItem
	ops   map[string]msOp
}

type msItem struct {
	token      crawl.DirectClaimToken
	generation uint64
	expiresAt  time.Time
	terminal   crawl.DirectTerminalKind
	leased     bool
}

// msOp is the recorded outcome of one operation ID.
type msOp struct {
	kind  string
	key   crawl.IdentityKey
	claim crawl.DirectClaim        // recorded result for claim/renew
	term  crawl.DirectTerminalKind // recorded payload for finalize
	token crawl.DirectClaimToken   // recorded claim token for finalize/release
}

// NewMemoryStorage constructs empty direct storage.
func NewMemoryStorage(clock Clock) *MemoryStorage {
	if clock == nil {
		clock = systemClock{}
	}
	return &MemoryStorage{
		clock: clock,
		items: make(map[crawl.IdentityKey]*msItem),
		ops:   make(map[string]msOp),
	}
}

func msOpKey(op crawl.OperationID) string { return hex.EncodeToString(op[:]) }

func msToken() (crawl.DirectClaimToken, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return crawl.DirectClaimToken(hex.EncodeToString(b[:])), nil
}

// Claim implements DirectStorage.
func (m *MemoryStorage) Claim(ctx context.Context, req crawl.DirectClaimRequest) (crawl.DirectClaimResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.DirectClaimResult{}, err
	}
	if req.LeaseDuration <= 0 {
		return crawl.DirectClaimResult{}, fmt.Errorf("%w: lease duration", crawl.ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Replay: return the originally recorded claim result, never a newer fence.
	if rec, ok := m.ops[msOpKey(req.OperationID)]; ok {
		if rec.kind != "claim" || rec.key != req.Key {
			return crawl.DirectClaimResult{}, crawl.ErrOperationConflict
		}
		return crawl.DirectClaimResult{Status: crawl.DirectClaimAcquired, Claim: rec.claim}, nil
	}

	now := m.clock.Now()
	item := m.items[req.Key]
	if item != nil {
		if item.terminal != crawl.DirectTerminalUnspecified {
			return crawl.DirectClaimResult{Status: crawl.DirectClaimTerminal, Terminal: item.terminal}, nil
		}
		if item.leased && item.expiresAt.After(now) {
			return crawl.DirectClaimResult{Status: crawl.DirectClaimBusy}, nil
		}
	}
	tok, err := msToken()
	if err != nil {
		return crawl.DirectClaimResult{}, err
	}
	gen := uint64(1)
	if item != nil {
		gen = item.generation + 1
	}
	expires := now.Add(req.LeaseDuration)
	m.items[req.Key] = &msItem{token: tok, generation: gen, expiresAt: expires, leased: true}
	claim := crawl.DirectClaim{
		Key:            req.Key,
		Token:          tok,
		Generation:     gen,
		LeaseExpiresAt: expires,
		RenewAfter:     req.LeaseDuration / 3,
	}
	m.ops[msOpKey(req.OperationID)] = msOp{kind: "claim", key: req.Key, claim: claim}
	return crawl.DirectClaimResult{Status: crawl.DirectClaimAcquired, Claim: claim}, nil
}

// Renew implements DirectStorage.
func (m *MemoryStorage) Renew(ctx context.Context, req crawl.DirectRenewRequest) (crawl.DirectClaim, error) {
	if err := ctx.Err(); err != nil {
		return crawl.DirectClaim{}, err
	}
	if req.LeaseDuration <= 0 {
		return crawl.DirectClaim{}, fmt.Errorf("%w: lease duration", crawl.ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.ops[msOpKey(req.OperationID)]; ok {
		if rec.kind != "renew" || rec.key != req.Claim.Key ||
			rec.claim.Token != req.Claim.Token || rec.claim.Generation != req.Claim.Generation {
			return crawl.DirectClaim{}, crawl.ErrOperationConflict
		}
		return rec.claim, nil
	}

	item := m.items[req.Claim.Key]
	if item == nil || !item.leased || item.token != req.Claim.Token || item.generation != req.Claim.Generation {
		return crawl.DirectClaim{}, crawl.ErrLeaseConflict
	}
	now := m.clock.Now()
	if !item.expiresAt.After(now) {
		return crawl.DirectClaim{}, crawl.ErrLeaseLost
	}
	item.expiresAt = now.Add(req.LeaseDuration)
	renewed := crawl.DirectClaim{
		Key:            req.Claim.Key,
		Token:          item.token,
		Generation:     item.generation,
		LeaseExpiresAt: item.expiresAt,
		RenewAfter:     req.LeaseDuration / 3,
	}
	m.ops[msOpKey(req.OperationID)] = msOp{kind: "renew", key: req.Claim.Key, claim: renewed}
	return renewed, nil
}

// Finalize implements DirectStorage.
func (m *MemoryStorage) Finalize(ctx context.Context, req crawl.DirectFinalizeRequest) (crawl.DirectFinalizeResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.DirectFinalizeResult{}, err
	}
	if req.Terminal != crawl.DirectTerminalHandled && req.Terminal != crawl.DirectTerminalFailed {
		return crawl.DirectFinalizeResult{}, fmt.Errorf("%w: terminal", crawl.ErrInvalidConfig)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.ops[msOpKey(req.OperationID)]; ok {
		if rec.kind != "finalize" || rec.key != req.Claim.Key ||
			rec.token != req.Claim.Token || rec.term != req.Terminal {
			return crawl.DirectFinalizeResult{}, crawl.ErrOperationConflict
		}
		return crawl.DirectFinalizeResult{Applied: true}, nil
	}

	item := m.items[req.Claim.Key]
	if item == nil || item.token != req.Claim.Token || item.generation != req.Claim.Generation {
		return crawl.DirectFinalizeResult{}, crawl.ErrLeaseConflict
	}
	item.terminal = req.Terminal
	item.leased = false
	m.ops[msOpKey(req.OperationID)] = msOp{kind: "finalize", key: req.Claim.Key, term: req.Terminal, token: req.Claim.Token}
	return crawl.DirectFinalizeResult{Applied: true}, nil
}

// Release implements DirectStorage.
func (m *MemoryStorage) Release(ctx context.Context, req crawl.DirectReleaseRequest) (crawl.DirectReleaseResult, error) {
	if err := ctx.Err(); err != nil {
		return crawl.DirectReleaseResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Only a recorded release for the same key is a replay; anything else is
	// a conflict, not a falsely applied transition.
	if rec, ok := m.ops[msOpKey(req.OperationID)]; ok {
		if rec.kind != "release" || rec.key != req.Claim.Key || rec.token != req.Claim.Token {
			return crawl.DirectReleaseResult{}, crawl.ErrOperationConflict
		}
		return crawl.DirectReleaseResult{Applied: true}, nil
	}

	item := m.items[req.Claim.Key]
	if item == nil || item.token != req.Claim.Token || item.generation != req.Claim.Generation {
		return crawl.DirectReleaseResult{}, crawl.ErrLeaseConflict
	}
	if item.terminal != crawl.DirectTerminalUnspecified {
		return crawl.DirectReleaseResult{}, crawl.ErrLeaseConflict
	}
	item.leased = false
	item.expiresAt = m.clock.Now()
	m.ops[msOpKey(req.OperationID)] = msOp{kind: "release", key: req.Claim.Key, token: req.Claim.Token}
	return crawl.DirectReleaseResult{Applied: true}, nil
}

var _ crawl.DirectStorage = (*MemoryStorage)(nil)
