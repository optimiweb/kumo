package sitemap

import (
	"strings"
	"testing"
)

func TestParseURLSet(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/</loc><lastmod>2026-01-01</lastmod><changefreq>daily</changefreq><priority>0.8</priority></url>
  <url><loc>https://example.com/about</loc></url>
  <url><lastmod>2026-02-01</lastmod></url>
</urlset>`
	res, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsIndex {
		t.Fatal("urlset reported as index")
	}
	if len(res.URLs) != 2 {
		t.Fatalf("urls=%v", res.URLs)
	}
	if res.URLs[0] != (URLEntry{
		Loc:        "https://example.com/",
		LastMod:    "2026-01-01",
		ChangeFreq: "daily",
		Priority:   "0.8",
	}) {
		t.Fatalf("first = %+v", res.URLs[0])
	}
	if res.URLs[1] != (URLEntry{Loc: "https://example.com/about"}) {
		t.Fatalf("second = %+v", res.URLs[1])
	}
	locs := res.URLLocs()
	if len(locs) != 2 || locs[0] != "https://example.com/" || locs[1] != "https://example.com/about" {
		t.Fatalf("URLLocs=%v", locs)
	}
}

func TestParseIndex(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>https://example.com/sitemaps/pages.xml</loc><lastmod>2026-03-01</lastmod></sitemap>
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
	if res.Sitemaps[0] != (SitemapEntry{Loc: "https://example.com/sitemaps/pages.xml", LastMod: "2026-03-01"}) {
		t.Fatalf("first = %+v", res.Sitemaps[0])
	}
	if res.Sitemaps[1] != (SitemapEntry{Loc: "https://example.com/sitemaps/posts.xml"}) {
		t.Fatalf("second = %+v", res.Sitemaps[1])
	}
	locs := res.SitemapLocs()
	if len(locs) != 2 || locs[0] != "https://example.com/sitemaps/pages.xml" {
		t.Fatalf("SitemapLocs=%v", locs)
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
