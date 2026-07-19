package sitemap

import (
	"strings"
	"testing"
)

func TestParseURLSet(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/</loc><lastmod>2026-01-01</lastmod></url>
  <url><loc>https://example.com/about</loc></url>
  <url></url>
</urlset>`
	res, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsIndex {
		t.Fatal("urlset reported as index")
	}
	if len(res.URLs) != 2 || res.URLs[0] != "https://example.com/" || res.URLs[1] != "https://example.com/about" {
		t.Fatalf("urls=%v", res.URLs)
	}
}

func TestParseIndex(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>https://example.com/sitemaps/pages.xml</loc></sitemap>
  <sitemap><loc>https://example.com/sitemaps/posts.xml</loc></sitemap>
</sitemapindex>`
	res, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsIndex {
		t.Fatal("index not detected")
	}
	if len(res.Sitemaps) != 2 {
		t.Fatalf("sitemaps=%v", res.Sitemaps)
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse(strings.NewReader("not xml")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Parse(strings.NewReader("<html></html>")); err == nil {
		t.Fatal("expected unexpected-root error")
	}
}
