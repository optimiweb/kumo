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
		fmt.Fprintln(os.Stderr, "usage: direct <url>")
		os.Exit(2)
	}
	cfg := kumo.DefaultCollectorConfig()
	cfg.Robots.Enabled = true
	c, err := kumo.NewCollector(cfg)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := c.RunDirect(ctx, kumo.DirectRunConfig{
		Seeds:            []string{os.Args[1]},
		Storage:          memory.NewMemoryStorage(nil),
		Identifier:       kumo.IdentityFunc(kumo.DefaultIdentity),
		MaxWorkItems:     20,
		MaxFetchAttempts: 40,
		MaxConcurrency:   2,
		MaxAttempts:      2,
		Handler: kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
			res := in.Result()
			fmt.Printf("%s outcome=%s\n", in.Lease().Work().URL(), res.Outcome())
			if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
				return kumo.Fail(res.ErrorCode())
			}
			return kumo.Ack()
		}),
	})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("handled=%d failed=%d stop=%s\n", report.Handled(), report.Failed(), report.StopReason())
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
