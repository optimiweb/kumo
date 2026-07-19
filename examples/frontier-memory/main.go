package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/optimiweb/kumo"
	"github.com/optimiweb/kumo/memory"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: frontier-memory <url>")
		os.Exit(2)
	}
	raw := os.Args[1]

	cfg := kumo.DefaultCollectorConfig()
	// Callers must set a real host policy for production targets.
	cfg.Robots.Enabled = true
	c, err := kumo.NewCollector(cfg)
	if err != nil {
		fatal(err)
	}

	fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{
		MaxAttempts:   3,
		MaxPages:      50,
		FetchBudget:   100,
		MaxOriginConc: 2,
	})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	seed, err := id(context.Background(), kumo.IdentityRequest{
		RawURL: raw,
		Method: kumo.MethodGET,
		Source: kumo.SourceSeed,
	})
	if err != nil || seed.State != kumo.IdentityAccepted {
		fatal(fmt.Errorf("identity rejected: %v %s", err, seed.Code))
	}
	if _, err := fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity:      seed.Identity,
		Method:        kumo.MethodGET,
		Source:        kumo.SourceSeed,
		ResourceClass: kumo.ResourceHTML,
	}); err != nil {
		fatal(err)
	}
	if err := fr.SealSeeds(context.Background()); err != nil {
		fatal(err)
	}

	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		res := in.Result()
		fmt.Printf("%s outcome=%s code=%s\n", in.Lease().Work().URL(), res.Outcome(), res.ErrorCode())
		if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
			return kumo.Fail(res.ErrorCode())
		}
		return kumo.Ack()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier:     fr,
		Identifier:   id,
		Handler:      handler,
		UntilDrained: true,
		PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("handled=%d failed=%d fetched=%d stop=%s\n",
		report.Handled(), report.Failed(), report.Fetched(), report.StopReason())
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
