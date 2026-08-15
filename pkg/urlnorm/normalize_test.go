package urlnorm_test

import (
	"context"
	"testing"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/pkg/urlnorm"
)

func TestNormalizationPolicy_Normalize(t *testing.T) {
	tests := []struct {
		name    string
		policy  urlnorm.NormalizationPolicy
		rawURL  string
		wantURL string
	}{
		{
			name:    "sort query parameters",
			policy:  urlnorm.NormalizationPolicy{SortQueryParams: true},
			rawURL:  "https://example.com/products?z=last&a=first&m=middle",
			wantURL: "https://example.com/products?a=first&m=middle&z=last",
		},
		{
			name:    "strip tracking params",
			policy:  urlnorm.NormalizationPolicy{SortQueryParams: true, StripTrackingParams: true},
			rawURL:  "https://example.com/page?utm_source=google&utm_medium=cpc&item=123&fbclid=abc",
			wantURL: "https://example.com/page?item=123",
		},
		{
			name:    "strip all query params if only tracking present",
			policy:  urlnorm.NormalizationPolicy{StripTrackingParams: true},
			rawURL:  "https://example.com/page?utm_source=newsletter&utm_campaign=summer",
			wantURL: "https://example.com/page",
		},
		{
			name:    "custom strip params",
			policy:  urlnorm.NormalizationPolicy{StripParams: []string{"session_id", "affiliate"}},
			rawURL:  "https://example.com/cart?session_id=98765&affiliate=partner&id=1",
			wantURL: "https://example.com/cart?id=1",
		},
		{
			name:    "remove trailing slash",
			policy:  urlnorm.NormalizationPolicy{TrailingSlash: urlnorm.TrailingSlashRemove},
			rawURL:  "https://example.com/about/",
			wantURL: "https://example.com/about",
		},
		{
			name:    "remove trailing slash root stays slash",
			policy:  urlnorm.NormalizationPolicy{TrailingSlash: urlnorm.TrailingSlashRemove},
			rawURL:  "https://example.com/",
			wantURL: "https://example.com/",
		},
		{
			name:    "add trailing slash",
			policy:  urlnorm.NormalizationPolicy{TrailingSlash: urlnorm.TrailingSlashAdd},
			rawURL:  "https://example.com/blog/article",
			wantURL: "https://example.com/blog/article/",
		},
		{
			name:    "add trailing slash preserves file extension",
			policy:  urlnorm.NormalizationPolicy{TrailingSlash: urlnorm.TrailingSlashAdd},
			rawURL:  "https://example.com/image.png",
			wantURL: "https://example.com/image.png",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.policy.Normalize(tc.rawURL)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if got != tc.wantURL {
				t.Errorf("Normalize() = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

func TestNewNormalizedIdentifier(t *testing.T) {
	policy := urlnorm.DefaultNormalizationPolicy()
	identifier := urlnorm.NewNormalizedIdentifier(policy)

	req1 := crawl.IdentityRequest{
		RawURL: "https://example.com/shop?utm_source=twitter&category=shoes&page=2",
		Method: crawl.MethodGET,
	}
	res1, err := identifier.Identify(context.Background(), req1)
	if err != nil || res1.State != crawl.IdentityAccepted {
		t.Fatalf("identify req1 failed: err=%v, res=%+v", err, res1)
	}

	req2 := crawl.IdentityRequest{
		RawURL: "https://example.com/shop?page=2&category=shoes&fbclid=12345",
		Method: crawl.MethodGET,
	}
	res2, err := identifier.Identify(context.Background(), req2)
	if err != nil || res2.State != crawl.IdentityAccepted {
		t.Fatalf("identify req2 failed: err=%v, res=%+v", err, res2)
	}

	if !res1.Identity.Equal(res2.Identity) {
		t.Fatalf("expected identical identity for tracking-stripped URLs: url1=%q url2=%q",
			res1.Identity.URL(), res2.Identity.URL())
	}
}
