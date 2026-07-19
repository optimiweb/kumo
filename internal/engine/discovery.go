package engine

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/optimiweb/kumo/crawl"
)

type discoverySink struct {
	mu         sync.Mutex
	closed     atomic.Bool
	unresolved atomic.Bool
	submit     func(context.Context, crawl.Discovery) (crawl.DiscoveryResult, error)
	count      int
	max        int
}

func newDiscoverySink(max int, submit func(context.Context, crawl.Discovery) (crawl.DiscoveryResult, error)) *discoverySink {
	return &discoverySink{max: max, submit: submit}
}

func (s *discoverySink) Submit(ctx context.Context, d crawl.Discovery) (crawl.DiscoveryResult, error) {
	if s.closed.Load() {
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: crawl.CodeInvalidWork}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.max > 0 && s.count >= s.max {
		return crawl.DiscoveryResult{State: crawl.DiscoveryLimitReached, Code: crawl.CodeBudgetExhausted}, nil
	}
	result, err := s.submit(ctx, d)
	if err != nil {
		s.unresolved.Store(true)
		return crawl.DiscoveryResult{}, err
	}
	s.count++
	return result, nil
}

func (s *discoverySink) close()              { s.closed.Store(true) }
func (s *discoverySink) hasUnresolved() bool { return s.unresolved.Load() }
