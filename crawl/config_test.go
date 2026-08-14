package crawl

import "testing"

func TestDefaultHeaderAllowlistContainsLinkAndCFCacheStatus(t *testing.T) {
	got := DefaultHeaderAllowlist()
	want := map[string]bool{"link": false, "cf-cache-status": false}
	for _, h := range got {
		if _, ok := want[h]; ok {
			want[h] = true
		}
	}
	for h, found := range want {
		if !found {
			t.Fatalf("DefaultHeaderAllowlist missing %q: %v", h, got)
		}
	}
}
