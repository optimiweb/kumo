package memory_test

import (
	"context"
	"testing"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/memory"
)

func TestEnqueueWithAttrsSucceeds(t *testing.T) {
	fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{})
	id, err := crawl.NewURLIdentity(crawl.IdentityKey{0: 9}, "https://example.com/test")
	if err != nil {
		t.Fatal(err)
	}
	res, err := fr.EnqueueSeed(context.Background(), crawl.EnqueueRequest{
		Identity:      id,
		Method:        crawl.MethodGET,
		Source:        crawl.SourceSeed,
		ResourceClass: crawl.ResourceHTML,
		Attrs:         map[string]string{"lastmod": "2026-01-01", "relation_key": "en"},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !res.Inserted {
		t.Fatal("expected inserted")
	}
}
