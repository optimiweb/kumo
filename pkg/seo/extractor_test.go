package seo_test

import (
	"strings"
	"testing"

	"github.com/optimiweb/kumo/pkg/seo"
)

const sampleHTML = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Acme Corporation — Modern Cloud Delivery</title>
    <meta name="description" content="Acme provides fast and secure cloud edge delivery.">
    <meta name="robots" content="index, follow, max-snippet:160, max-image-preview:large">
    <link rel="canonical" href="/products/cloud">
    <link rel="alternate" hreflang="fr" href="/fr/produits/cloud">
    <link rel="alternate" hreflang="es" href="/es/productos/cloud">
    <meta property="og:title" content="Acme Cloud">
    <meta property="og:type" content="website">
</head>
<body>
    <header>
        <a href="/">Home</a>
        <a href="/about" rel="nofollow">About Us</a>
        <a href="https://blog.example.com/latest">Blog</a>
    </header>
    <main>
        <h1>Welcome</h1>
        <p>Check out our <a href="pricing.html">Pricing Details</a>.</p>
        <a href="#section-2">Skip to section</a>
        <a href="javascript:void(0)">Click me</a>
        <a href="mailto:support@example.com">Email support</a>
    </main>
</body>
</html>
`

func TestExtract(t *testing.T) {
	pageURL := "https://example.com/company/index.html"
	meta, links, err := seo.Extract(strings.NewReader(sampleHTML), pageURL)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if meta.Title != "Acme Corporation — Modern Cloud Delivery" {
		t.Errorf("got Title = %q, want %q", meta.Title, "Acme Corporation — Modern Cloud Delivery")
	}
	if meta.Description != "Acme provides fast and secure cloud edge delivery." {
		t.Errorf("got Description = %q", meta.Description)
	}
	if meta.CanonicalURL != "https://example.com/products/cloud" {
		t.Errorf("got CanonicalURL = %q, want %q", meta.CanonicalURL, "https://example.com/products/cloud")
	}
	if len(meta.Hreflangs) != 2 {
		t.Fatalf("got %d hreflangs, want 2", len(meta.Hreflangs))
	}
	if meta.Hreflangs[0].Lang != "fr" || meta.Hreflangs[0].URL != "https://example.com/fr/produits/cloud" {
		t.Errorf("got hreflang[0] = %+v", meta.Hreflangs[0])
	}
	if meta.OpenGraph["og:title"] != "Acme Cloud" {
		t.Errorf("got og:title = %q", meta.OpenGraph["og:title"])
	}
	if !meta.Robots.Indexable() || !meta.Robots.Followable() {
		t.Errorf("expected Indexable and Followable true, got %+v", meta.Robots)
	}
	if meta.Robots.MaxSnippet == nil || *meta.Robots.MaxSnippet != 160 {
		t.Errorf("expected MaxSnippet=160, got %v", meta.Robots.MaxSnippet)
	}

	// Links: Home, About Us, Blog, Pricing Details (Skip, javascript, mailto ignored)
	if len(links) != 4 {
		t.Fatalf("got %d links, want 4: %+v", len(links), links)
	}

	expectedLinks := []struct {
		resolved   string
		text       string
		isNoFollow bool
	}{
		{"https://example.com/", "Home", false},
		{"https://example.com/about", "About Us", true},
		{"https://blog.example.com/latest", "Blog", false},
		{"https://example.com/company/pricing.html", "Pricing Details", false},
	}

	for i, want := range expectedLinks {
		got := links[i]
		if got.ResolvedURL != want.resolved {
			t.Errorf("link[%d] ResolvedURL = %q, want %q", i, got.ResolvedURL, want.resolved)
		}
		if got.Text != want.text {
			t.Errorf("link[%d] Text = %q, want %q", i, got.Text, want.text)
		}
		if got.IsNoFollow != want.isNoFollow {
			t.Errorf("link[%d] IsNoFollow = %v, want %v", i, got.IsNoFollow, want.isNoFollow)
		}
	}
}

func TestExtract_WithBaseHref(t *testing.T) {
	htmlWithBase := `
	<!DOCTYPE html>
	<html>
	<head>
		<base href="https://cdn.example.com/assets/">
		<title>Base Test</title>
	</head>
	<body>
		<a href="doc.html">Document</a>
	</body>
	</html>
	`
	meta, links, err := seo.Extract(strings.NewReader(htmlWithBase), "https://example.com/page")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if meta.BaseHRef != "https://cdn.example.com/assets/" {
		t.Errorf("BaseHRef = %q", meta.BaseHRef)
	}
	if len(links) != 1 || links[0].ResolvedURL != "https://cdn.example.com/assets/doc.html" {
		t.Fatalf("links = %+v", links)
	}
}
