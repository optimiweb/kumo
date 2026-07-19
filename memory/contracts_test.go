package memory_test

import (
	"testing"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/crawltest"
	"github.com/optimiweb/kumo/memory"
)

func TestMemoryFrontierConforms(t *testing.T) {
	crawltest.CheckFrontier(t, func(testing.TB) crawl.Frontier {
		return memory.NewMemoryFrontier(memory.MemoryFrontierOptions{})
	})
}

func TestMemoryStorageConforms(t *testing.T) {
	crawltest.CheckDirectStorage(t, func(testing.TB) crawl.DirectStorage {
		return memory.NewMemoryStorage(nil)
	})
}
