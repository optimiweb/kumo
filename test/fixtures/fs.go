// Package fixtures embeds static mock sites used by integration tests.
package fixtures

import "embed"

// ExampleCom is a small static mock of http://example.com.
//
//go:embed all:example.com
var ExampleCom embed.FS
