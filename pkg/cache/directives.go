// Package cache provides RFC 9111 HTTP cache header parsing, TTL calculation,
// and X-Robots-Tag directive extraction.
package cache

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Directives represents parsed Cache-Control directives.
type Directives struct {
	MaxAge               *time.Duration `json:"maxAge,omitempty"`
	SMaxAge              *time.Duration `json:"sMaxAge,omitempty"`
	Public               bool           `json:"public,omitempty"`
	Private              bool           `json:"private,omitempty"`
	NoCache              bool           `json:"noCache,omitempty"`
	NoStore              bool           `json:"noStore,omitempty"`
	MustRevalidate       bool           `json:"mustRevalidate,omitempty"`
	ProxyRevalidate      bool           `json:"proxyRevalidate,omitempty"`
	Immutable            bool           `json:"immutable,omitempty"`
	StaleWhileRevalidate *time.Duration `json:"staleWhileRevalidate,omitempty"`
	StaleIfError         *time.Duration `json:"staleIfError,omitempty"`
}

// RobotsDirectives represents parsed robots meta and X-Robots-Tag directives.
type RobotsDirectives struct {
	NoIndex          bool       `json:"noindex,omitempty"`
	NoFollow         bool       `json:"nofollow,omitempty"`
	NoArchive        bool       `json:"noarchive,omitempty"`
	NoSnippet        bool       `json:"nosnippet,omitempty"`
	NoTranslate      bool       `json:"notranslate,omitempty"`
	NoImageIndex     bool       `json:"noimageindex,omitempty"`
	UnavailableAfter *time.Time `json:"unavailableAfter,omitempty"`
	MaxSnippet       *int       `json:"maxSnippet,omitempty"`
	MaxImagePreview  string     `json:"maxImagePreview,omitempty"`
	MaxVideoPreview  *int       `json:"maxVideoPreview,omitempty"`
}

// Indexable reports whether robots directives permit search indexing.
func (r RobotsDirectives) Indexable() bool {
	return !r.NoIndex
}

// Followable reports whether robots directives permit following links.
func (r RobotsDirectives) Followable() bool {
	return !r.NoFollow
}

// ParseDirectives decodes a Cache-Control header string into structured directives.
func ParseDirectives(raw string) Directives {
	var d Directives
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, val, hasVal := strings.Cut(part, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.Trim(strings.TrimSpace(val), `"'`)

		switch key {
		case "public":
			d.Public = true
		case "private":
			d.Private = true
		case "no-cache":
			d.NoCache = true
		case "no-store":
			d.NoStore = true
		case "must-revalidate":
			d.MustRevalidate = true
		case "proxy-revalidate":
			d.ProxyRevalidate = true
		case "immutable":
			d.Immutable = true
		case "max-age":
			if hasVal {
				if sec, err := strconv.Atoi(val); err == nil && sec >= 0 {
					dur := time.Duration(sec) * time.Second
					d.MaxAge = &dur
				}
			}
		case "s-maxage", "s-max-age":
			if hasVal {
				if sec, err := strconv.Atoi(val); err == nil && sec >= 0 {
					dur := time.Duration(sec) * time.Second
					d.SMaxAge = &dur
				}
			}
		case "stale-while-revalidate":
			if hasVal {
				if sec, err := strconv.Atoi(val); err == nil && sec >= 0 {
					dur := time.Duration(sec) * time.Second
					d.StaleWhileRevalidate = &dur
				}
			}
		case "stale-if-error":
			if hasVal {
				if sec, err := strconv.Atoi(val); err == nil && sec >= 0 {
					dur := time.Duration(sec) * time.Second
					d.StaleIfError = &dur
				}
			}
		}
	}
	return d
}

// ParseXRobotsTag parses X-Robots-Tag header values into RobotsDirectives.
func ParseXRobotsTag(values []string) RobotsDirectives {
	var r RobotsDirectives
	for _, val := range values {
		ParseRobotsStringInto(val, &r)
	}
	return r
}

// ParseRobotsStringInto parses a comma-separated robots directive string into an existing struct.
func ParseRobotsStringInto(val string, r *RobotsDirectives) {
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, param, hasParam := strings.Cut(part, ":")
		key = strings.ToLower(strings.TrimSpace(key))
		param = strings.Trim(strings.TrimSpace(param), `"'`)

		switch key {
		case "none":
			r.NoIndex = true
			r.NoFollow = true
		case "noindex":
			r.NoIndex = true
		case "nofollow":
			r.NoFollow = true
		case "noarchive":
			r.NoArchive = true
		case "nosnippet":
			r.NoSnippet = true
		case "notranslate":
			r.NoTranslate = true
		case "noimageindex":
			r.NoImageIndex = true
		case "max-snippet":
			if hasParam {
				if n, err := strconv.Atoi(param); err == nil {
					r.MaxSnippet = &n
				}
			}
		case "max-image-preview":
			if hasParam {
				r.MaxImagePreview = strings.ToLower(param)
			}
		case "max-video-preview":
			if hasParam {
				if n, err := strconv.Atoi(param); err == nil {
					r.MaxVideoPreview = &n
				}
			}
		case "unavailable_after":
			if hasParam {
				if t, err := time.Parse(time.RFC3339, param); err == nil {
					r.UnavailableAfter = &t
				} else if t, err := http.ParseTime(param); err == nil {
					r.UnavailableAfter = &t
				}
			}
		}
	}
}

// parseTime tries multiple standard date formats.
func parseTime(raw string) (time.Time, error) {
	if t, err := http.ParseTime(raw); err == nil {
		return t, nil
	}
	formats := []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, http.ErrNotSupported
}

// CalculateEffectiveTTL computes the cache lifetime and cacheability according to RFC 9111.
func CalculateEffectiveTTL(cacheControl, expiresHeader, ageHeader, dateHeader string, now time.Time) (time.Duration, bool) {
	directives := ParseDirectives(cacheControl)
	if directives.NoStore || directives.Private {
		return 0, false
	}

	// s-maxage takes precedence for shared/CDN caches
	if directives.SMaxAge != nil {
		return *directives.SMaxAge, true
	}

	// max-age is standard origin TTL
	if directives.MaxAge != nil {
		return *directives.MaxAge, true
	}

	// Fallback to Expires header if Cache-Control does not specify max-age
	if expiresHeader != "" {
		if expTime, err := parseTime(expiresHeader); err == nil {
			var baseTime time.Time
			if dateHeader != "" {
				if d, err := parseTime(dateHeader); err == nil {
					baseTime = d
				}
			}
			if baseTime.IsZero() {
				baseTime = now
			}
			diff := expTime.Sub(baseTime)
			if diff > 0 {
				return diff, true
			}
			return 0, false
		}
	}

	return 0, false
}
