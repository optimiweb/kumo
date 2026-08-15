package cache_test

import (
	"testing"
	"time"

	"github.com/optimiweb/kumo/pkg/cache"
)

func TestParseDirectives(t *testing.T) {
	raw := "public, max-age=3600, s-maxage=86400, must-revalidate, stale-while-revalidate=60"
	d := cache.ParseDirectives(raw)

	if !d.Public {
		t.Errorf("expected Public=true")
	}
	if d.MaxAge == nil || *d.MaxAge != 3600*time.Second {
		t.Errorf("expected MaxAge=3600s, got %v", d.MaxAge)
	}
	if d.SMaxAge == nil || *d.SMaxAge != 86400*time.Second {
		t.Errorf("expected SMaxAge=86400s, got %v", d.SMaxAge)
	}
	if !d.MustRevalidate {
		t.Errorf("expected MustRevalidate=true")
	}
	if d.StaleWhileRevalidate == nil || *d.StaleWhileRevalidate != 60*time.Second {
		t.Errorf("expected StaleWhileRevalidate=60s, got %v", d.StaleWhileRevalidate)
	}
}

func TestParseXRobotsTag(t *testing.T) {
	headers := []string{
		"noindex, nofollow",
		"max-snippet:150, max-image-preview:large",
	}
	r := cache.ParseXRobotsTag(headers)

	if !r.NoIndex {
		t.Errorf("expected NoIndex=true")
	}
	if !r.NoFollow {
		t.Errorf("expected NoFollow=true")
	}
	if r.Indexable() {
		t.Errorf("expected Indexable=false")
	}
	if r.Followable() {
		t.Errorf("expected Followable=false")
	}
	if r.MaxSnippet == nil || *r.MaxSnippet != 150 {
		t.Errorf("expected MaxSnippet=150, got %v", r.MaxSnippet)
	}
	if r.MaxImagePreview != "large" {
		t.Errorf("expected MaxImagePreview=large, got %q", r.MaxImagePreview)
	}
}

func TestCalculateEffectiveTTL(t *testing.T) {
	now := time.Now()

	// s-maxage takes precedence over max-age
	ttl, cacheable := cache.CalculateEffectiveTTL("max-age=60, s-maxage=300", "", "", "", now)
	if !cacheable || ttl != 300*time.Second {
		t.Fatalf("expected s-maxage 300s, got cacheable=%v ttl=%v", cacheable, ttl)
	}

	// no-store not cacheable
	ttl, cacheable = cache.CalculateEffectiveTTL("no-store, max-age=3600", "", "", "", now)
	if cacheable || ttl != 0 {
		t.Fatalf("expected no-store uncacheable, got cacheable=%v ttl=%v", cacheable, ttl)
	}

	// Expires fallback
	expires := now.Add(2 * time.Hour).UTC().Format(time.RFC1123)
	ttl, cacheable = cache.CalculateEffectiveTTL("", expires, "", "", now)
	if !cacheable || ttl < 7100*time.Second || ttl > 7300*time.Second {
		t.Fatalf("expected Expires ~2h, got cacheable=%v ttl=%v", cacheable, ttl)
	}
}
