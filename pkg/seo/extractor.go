// Package seo provides HTML metadata, OpenGraph, and link extraction for web crawlers.
package seo

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/pkg/cache"
	"golang.org/x/net/html"
)

// Hreflang represents an alternate language link.
type Hreflang struct {
	Lang string `json:"lang"`
	URL  string `json:"url"`
}

// PageMetadata holds structured SEO metadata extracted from an HTML document.
type PageMetadata struct {
	Title        string                 `json:"title,omitempty"`
	Description  string                 `json:"description,omitempty"`
	Robots       cache.RobotsDirectives `json:"robots,omitempty"`
	CanonicalURL string                 `json:"canonicalUrl,omitempty"`
	Hreflangs    []Hreflang             `json:"hreflangs,omitempty"`
	OpenGraph    map[string]string      `json:"openGraph,omitempty"`
	BaseHRef     string                 `json:"baseHref,omitempty"`
}

// ExtractedLink represents an anchor link extracted from HTML.
type ExtractedLink struct {
	RawURL      string `json:"rawUrl"`
	ResolvedURL string `json:"resolvedUrl"`
	Text        string `json:"text,omitempty"`
	Rel         string `json:"rel,omitempty"`
	IsNoFollow  bool   `json:"isNoFollow,omitempty"`
}

// Extract parses an HTML document and extracts both page metadata and hyperlinked URLs.
func Extract(r io.Reader, pageURL string) (PageMetadata, []ExtractedLink, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return PageMetadata{}, nil, fmt.Errorf("parse html: %w", err)
	}

	baseURL, err := url.Parse(pageURL)
	if err != nil {
		return PageMetadata{}, nil, fmt.Errorf("parse page url: %w", err)
	}

	meta := PageMetadata{
		OpenGraph: make(map[string]string),
	}
	var links []ExtractedLink

	// First find <base href> if present in <head>
	findBaseHref(doc, &meta.BaseHRef)
	effectiveBase := baseURL
	if meta.BaseHRef != "" {
		if resolvedBase, err := baseURL.Parse(meta.BaseHRef); err == nil {
			effectiveBase = resolvedBase
		}
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "title":
				if meta.Title == "" && n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					meta.Title = strings.TrimSpace(n.FirstChild.Data)
				}
			case "meta":
				handleMetaTag(n, &meta)
			case "link":
				handleLinkTag(n, effectiveBase, &meta)
			case "a", "area":
				if l, ok := handleAnchorTag(n, effectiveBase); ok {
					links = append(links, l)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}

	walk(doc)
	return meta, links, nil
}

func findBaseHref(n *html.Node, baseHRef *string) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "base") {
		for _, attr := range n.Attr {
			if strings.EqualFold(attr.Key, "href") {
				*baseHRef = strings.TrimSpace(attr.Val)
				return
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findBaseHref(c, baseHRef)
		if *baseHRef != "" {
			return
		}
	}
}

func handleMetaTag(n *html.Node, meta *PageMetadata) {
	var name, property, content string
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "name":
			name = strings.ToLower(strings.TrimSpace(a.Val))
		case "property":
			property = strings.ToLower(strings.TrimSpace(a.Val))
		case "content":
			content = strings.TrimSpace(a.Val)
		}
	}

	if name == "description" && meta.Description == "" {
		meta.Description = content
	} else if name == "robots" || name == "googlebot" {
		cache.ParseRobotsStringInto(content, &meta.Robots)
	}

	if strings.HasPrefix(property, "og:") {
		meta.OpenGraph[property] = content
	}
}

func handleLinkTag(n *html.Node, base *url.URL, meta *PageMetadata) {
	var rel, href, hreflang string
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "rel":
			rel = strings.ToLower(strings.TrimSpace(a.Val))
		case "href":
			href = strings.TrimSpace(a.Val)
		case "hreflang":
			hreflang = strings.TrimSpace(a.Val)
		}
	}

	if href == "" {
		return
	}

	resolved, err := base.Parse(href)
	if err != nil {
		return
	}
	resolved.Fragment = ""

	if rel == "canonical" && meta.CanonicalURL == "" {
		if canonical, err := crawl.CanonicalFetchURL(resolved.String()); err == nil {
			meta.CanonicalURL = canonical
		} else {
			meta.CanonicalURL = resolved.String()
		}
	} else if rel == "alternate" && hreflang != "" {
		canonical := resolved.String()
		if c, err := crawl.CanonicalFetchURL(canonical); err == nil {
			canonical = c
		}
		meta.Hreflangs = append(meta.Hreflangs, Hreflang{
			Lang: hreflang,
			URL:  canonical,
		})
	}
}

func handleAnchorTag(n *html.Node, base *url.URL) (ExtractedLink, bool) {
	var href, rel string
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "href":
			href = strings.TrimSpace(a.Val)
		case "rel":
			rel = strings.ToLower(strings.TrimSpace(a.Val))
		}
	}

	if href == "" || strings.HasPrefix(href, "#") {
		return ExtractedLink{}, false
	}

	lower := strings.ToLower(href)
	if strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(lower, "data:") {
		return ExtractedLink{}, false
	}

	resolved, err := base.Parse(href)
	if err != nil {
		return ExtractedLink{}, false
	}

	scheme := strings.ToLower(resolved.Scheme)
	if scheme != "http" && scheme != "https" {
		return ExtractedLink{}, false
	}

	resolved.Fragment = ""
	canonicalStr := resolved.String()
	if c, err := crawl.CanonicalFetchURL(canonicalStr); err == nil {
		canonicalStr = c
	}

	text := extractText(n)
	isNoFollow := strings.Contains(rel, "nofollow")

	return ExtractedLink{
		RawURL:      href,
		ResolvedURL: canonicalStr,
		Text:        text,
		Rel:         rel,
		IsNoFollow:  isNoFollow,
	}, true
}

func extractText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(curr *html.Node) {
		if curr.Type == html.TextNode {
			b.WriteString(curr.Data)
		}
		for c := curr.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
