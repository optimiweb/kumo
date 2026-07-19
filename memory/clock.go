package memory

import (
	"sync"
	"time"
)

// Clock abstracts time for deterministic adapters and tests.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// ManualClock is a controllable clock for tests.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock constructs a manual clock at t.
func NewManualClock(t time.Time) *ManualClock {
	return &ManualClock{now: t}
}

// Now returns the current manual time.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
