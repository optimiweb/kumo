package crawl_test

import (
	"context"
	"fmt"

	"github.com/optimiweb/kumo/crawl"
)

func ExampleDecision_WithCommit() {
	_ = crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		page := in.Result()
		return crawl.Ack().WithCommit(func(ctx context.Context) error {
			// Persist evidence in the same adapter txn as Transition.
			_ = page
			return nil
		})
	})
}

func ExampleDiscovery() {
	d := crawl.Discovery{
		URL:      "https://example.com/fr",
		Method:   crawl.MethodGET,
		Relation: crawl.RelationHreflang,
		Attrs:    map[string]string{"relation_key": "fr"},
	}
	fmt.Println(d.Relation.String(), d.Attrs["relation_key"])
	// Output: hreflang fr
}

func ExampleSourceLog() {
	req := crawl.EnqueueRequest{
		Source: crawl.SourceLog,
	}
	fmt.Println(req.Source)
	// Output: log
}
