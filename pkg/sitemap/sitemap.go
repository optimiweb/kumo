// Package sitemap parses XML sitemaps and sitemap indexes from bounded,
// already-fetched content. It performs no network I/O.
package sitemap

import (
	"encoding/xml"
	"fmt"
	"io"
)

// URLEntry is one urlset entry. Date and priority fields are raw strings.
type URLEntry struct {
	Loc        string
	LastMod    string
	ChangeFreq string
	Priority   string
}

// SitemapEntry is one sitemapindex child. LastMod is a raw string.
type SitemapEntry struct {
	Loc     string
	LastMod string
}

// Result is the extracted content of a sitemap document.
type Result struct {
	// IsIndex reports whether the root element is a sitemap index.
	IsIndex bool
	// Sitemaps holds child sitemap entries (sitemapindex documents).
	Sitemaps []SitemapEntry
	// URLs holds page entries (urlset documents).
	URLs []URLEntry
}

// SitemapLocs returns child sitemap locations.
func (r Result) SitemapLocs() []string {
	out := make([]string, len(r.Sitemaps))
	for i, s := range r.Sitemaps {
		out[i] = s.Loc
	}
	return out
}

// URLLocs returns page locations.
func (r Result) URLLocs() []string {
	out := make([]string, len(r.URLs))
	for i, u := range r.URLs {
		out[i] = u.Loc
	}
	return out
}

type urlset struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []struct {
		Loc        string `xml:"loc"`
		LastMod    string `xml:"lastmod"`
		ChangeFreq string `xml:"changefreq"`
		Priority   string `xml:"priority"`
	} `xml:"url"`
}

type index struct {
	XMLName  xml.Name `xml:"sitemapindex"`
	Sitemaps []struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
	} `xml:"sitemap"`
}

// Parse decodes a sitemap or sitemap index. The caller must bound r before
// calling (Kumo's fetch pipeline enforces body limits).
func Parse(r io.Reader) (Result, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Result{}, fmt.Errorf("read sitemap: %w", err)
	}

	var idx index
	if err := xml.Unmarshal(data, &idx); err == nil && idx.XMLName.Local == "sitemapindex" {
		res := Result{IsIndex: true}
		for _, s := range idx.Sitemaps {
			if s.Loc != "" {
				res.Sitemaps = append(res.Sitemaps, SitemapEntry{Loc: s.Loc, LastMod: s.LastMod})
			}
		}
		return res, nil
	}

	var set urlset
	if err := xml.Unmarshal(data, &set); err != nil {
		return Result{}, fmt.Errorf("parse sitemap: %w", err)
	}
	if set.XMLName.Local != "urlset" {
		return Result{}, fmt.Errorf("parse sitemap: unexpected root element %q", set.XMLName.Local)
	}
	res := Result{}
	for _, u := range set.URLs {
		if u.Loc != "" {
			res.URLs = append(res.URLs, URLEntry{
				Loc:        u.Loc,
				LastMod:    u.LastMod,
				ChangeFreq: u.ChangeFreq,
				Priority:   u.Priority,
			})
		}
	}
	return res, nil
}
