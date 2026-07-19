// Package storage provides the direct-mode in-memory claim adapter.
//
// Prefer the memory package constructors:
//
//	memory.NewMemoryStorage(nil)
//
// This package re-exports aliases for adapter-focused imports.
package storage

import (
	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/memory"
)

// Storage is the direct-mode claim port.
type Storage = crawl.DirectStorage

// Memory is the deterministic in-memory implementation.
type Memory = memory.MemoryStorage

// NewMemory constructs empty direct storage.
func NewMemory(clock memory.Clock) *Memory {
	return memory.NewMemoryStorage(clock)
}
