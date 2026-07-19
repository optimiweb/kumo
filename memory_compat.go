package kumo

import "github.com/optimiweb/kumo/memory"

// Root aliases preserve the former in-memory adapter API. New code should import memory.
type (
	Clock                 = memory.Clock
	ManualClock           = memory.ManualClock
	MemoryFrontierOptions = memory.MemoryFrontierOptions
	MemoryFrontier        = memory.MemoryFrontier
	MemoryStorage         = memory.MemoryStorage
)

var (
	NewManualClock    = memory.NewManualClock
	NewMemoryFrontier = memory.NewMemoryFrontier
	NewMemoryStorage  = memory.NewMemoryStorage
)
