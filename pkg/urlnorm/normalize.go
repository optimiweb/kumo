// Package urlnorm provides configurable URL normalization and canonicalization.
package urlnorm

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/optimiweb/kumo/crawl"
)

// TrailingSlashOption controls trailing slash handling.
type TrailingSlashOption uint8

const (
	TrailingSlashPreserve TrailingSlashOption = iota
	TrailingSlashRemove
	TrailingSlashAdd
)

// DefaultTrackingParams is the standard set of tracking query parameters to remove.
var DefaultTrackingParams = map[string]struct{}{
	"utm_source":   {},
	"utm_medium":   {},
	"utm_campaign": {},
	"utm_term":     {},
	"utm_content":  {},
	"utm_id":       {},
	"fbclid":       {},
	"gclid":        {},
	"gclsrc":       {},
	"dclid":        {},
	"mc_eid":       {},
	"mc_cid":       {},
	"msclkid":      {},
	"yclid":        {},
	"_openstat":    {},
	"igshid":       {},
	"twclid":       {},
}

// NormalizationPolicy defines how URLs are normalized and deduplicated.
type NormalizationPolicy struct {
	SortQueryParams     bool
	StripTrackingParams bool
	StripParams         []string
	TrailingSlash       TrailingSlashOption
	RemoveEmptyQuery    bool
}

// DefaultNormalizationPolicy returns sensible production defaults for web crawling.
func DefaultNormalizationPolicy() NormalizationPolicy {
	return NormalizationPolicy{
		SortQueryParams:     true,
		StripTrackingParams: true,
		RemoveEmptyQuery:    true,
	}
}

// Normalize normalizes a raw URL according to the policy.
func (p NormalizationPolicy) Normalize(rawURL string) (string, error) {
	canonical, err := crawl.CanonicalFetchURL(rawURL)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(canonical)
	if err != nil {
		return "", fmt.Errorf("parse canonical url: %w", err)
	}

	// Normalise trailing slash on path
	p.applyTrailingSlash(u)

	// Normalise query parameters
	p.applyQuery(u)

	return u.String(), nil
}

func (p NormalizationPolicy) applyTrailingSlash(u *url.URL) {
	if u.Path == "" || u.Path == "/" {
		u.Path = "/"
		return
	}
	switch p.TrailingSlash {
	case TrailingSlashRemove:
		if strings.HasSuffix(u.Path, "/") && u.Path != "/" {
			u.Path = strings.TrimRight(u.Path, "/")
		}
	case TrailingSlashAdd:
		if !strings.HasSuffix(u.Path, "/") {
			ext := path.Ext(u.Path)
			if ext == "" {
				u.Path += "/"
			}
		}
	}
}

func (p NormalizationPolicy) applyQuery(u *url.URL) {
	if u.RawQuery == "" {
		return
	}
	values := u.Query()
	if len(values) == 0 {
		if p.RemoveEmptyQuery {
			u.RawQuery = ""
		}
		return
	}

	stripSet := make(map[string]struct{})
	if p.StripTrackingParams {
		for k := range DefaultTrackingParams {
			stripSet[k] = struct{}{}
		}
	}
	for _, custom := range p.StripParams {
		stripSet[strings.ToLower(strings.TrimSpace(custom))] = struct{}{}
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		lower := strings.ToLower(k)
		if _, strip := stripSet[lower]; strip {
			continue
		}
		keys = append(keys, k)
	}

	if len(keys) == 0 {
		u.RawQuery = ""
		return
	}

	if p.SortQueryParams {
		sort.Strings(keys)
	}

	var parts []string
	for _, k := range keys {
		vs := values[k]
		if p.SortQueryParams {
			sort.Strings(vs)
		}
		for _, v := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}

	u.RawQuery = strings.Join(parts, "&")
}

// NewNormalizedIdentifier returns an Identifier that applies NormalizationPolicy before deriving identity.
func NewNormalizedIdentifier(policy NormalizationPolicy) crawl.Identifier {
	return crawl.IdentityFunc(func(ctx context.Context, req crawl.IdentityRequest) (crawl.IdentityResult, error) {
		_ = ctx
		if err := req.Method.Validate(); err != nil {
			return crawl.IdentityResult{State: crawl.IdentityRejected, Code: crawl.CodeMethodDenied}, nil
		}
		normalized, err := policy.Normalize(req.RawURL)
		if err != nil {
			return crawl.IdentityResult{State: crawl.IdentityRejected, Code: crawl.CodeIdentityRejected}, nil
		}
		sum := crawl.IdentityKey(crawl.RobotsKeyFor(string(req.Method), normalized, ""))
		id, err := crawl.NewURLIdentity(sum, normalized)
		if err != nil {
			return crawl.IdentityResult{State: crawl.IdentityRejected, Code: crawl.CodeIdentityRejected}, nil
		}
		return crawl.IdentityResult{State: crawl.IdentityAccepted, Identity: id}, nil
	})
}
