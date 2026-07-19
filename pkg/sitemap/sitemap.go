// Package sitemap parses XML sitemaps and sitemap indexes from bounded,
// already-fetched content. It performs no network I/O.
package sitemap

import (
	"encoding/xml"
	"fmt"
	"io"
)

// Result is the extracted content of a sitemap document.
type Result struct {
	// IsIndex reports whether the root element is a sitemap index.
	IsIndex bool
	// Sitemaps holds child sitemap URLs (sitemapindex documents).
	Sitemaps []string
	// URLs holds page URLs (urlset documents).
	URLs []string
}

type urlset struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

type index struct {
	XMLName  xml.Name `xml:"sitemapindex"`
	Sitemaps []struct {
		Loc string `xml:"loc"`
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
				res.Sitemaps = append(res.Sitemaps, s.Loc)
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
			res.URLs = append(res.URLs, u.Loc)
		}
	}
	return res, nil
}
