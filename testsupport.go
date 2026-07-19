package kumo

import "github.com/optimiweb/kumo/internal/httpx"

// SetTestHTTPClient replaces the collector HTTP client.
//
// Intended for in-module integration tests that dial httptest through
// internal/httpx. Production callers must not use this.
func SetTestHTTPClient(c *Collector, client *httpx.Client) {
	c.client = client
}
