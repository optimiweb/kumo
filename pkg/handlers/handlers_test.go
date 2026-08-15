package handlers_test

import (
	"context"
	"testing"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/pkg/handlers"
)

type mockSink struct {
	discoveries []crawl.Discovery
}

func (s *mockSink) Submit(ctx context.Context, d crawl.Discovery) (crawl.DiscoveryResult, error) {
	s.discoveries = append(s.discoveries, d)
	return crawl.DiscoveryResult{State: crawl.DiscoveryInserted, ID: "test-id"}, nil
}

func TestChainHandlers(t *testing.T) {
	var step1, step2 bool
	h1 := crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		step1 = true
		return crawl.Ack()
	})
	h2 := crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		step2 = true
		return crawl.Ack()
	})

	chained := handlers.ChainHandlers(h1, h2)
	dec := chained.Handle(context.Background(), crawl.HandleInput{}, &mockSink{})

	if dec.Kind() != crawl.DecisionAck {
		t.Errorf("got decision kind %v, want Ack", dec.Kind())
	}
	if !step1 || !step2 {
		t.Errorf("step1=%v step2=%v, want both true", step1, step2)
	}
}

func TestChainHandlers_EarlyExit(t *testing.T) {
	var step2 bool
	h1 := crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		return crawl.Fail(crawl.CodeProtocolFailed)
	})
	h2 := crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		step2 = true
		return crawl.Ack()
	})

	chained := handlers.ChainHandlers(h1, h2)
	dec := chained.Handle(context.Background(), crawl.HandleInput{}, &mockSink{})

	if dec.Kind() != crawl.DecisionFail {
		t.Errorf("got decision kind %v, want Fail", dec.Kind())
	}
	if step2 {
		t.Errorf("step2 executed, expected early halt on Fail")
	}
}

func TestDefaultSitemapHandlerOptions(t *testing.T) {
	opts := handlers.DefaultSitemapHandlerOptions()
	if opts.RobotsSitemapPriority != 15 || opts.ChildSitemapPriority != 15 || opts.PageURLPriority != 0 {
		t.Errorf("unexpected default opts: %+v", opts)
	}
}
