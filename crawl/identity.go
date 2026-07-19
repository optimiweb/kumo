package crawl

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	whatwgurl "github.com/optimiweb/kumo/pkg/whatwgurl/url"
)

// IdentityKey is a collision-resistant opaque key.
type IdentityKey [32]byte

// URLIdentity binds a canonical fetch URL to its key.
type URLIdentity struct {
	key IdentityKey
	url string
}

// NewURLIdentity constructs an identity after structural validation.
func NewURLIdentity(key IdentityKey, canonicalURL string) (URLIdentity, error) {
	if err := validateCanonicalURL(canonicalURL); err != nil {
		return URLIdentity{}, err
	}
	return URLIdentity{key: key, url: canonicalURL}, nil
}

// Key returns the identity key.
func (i URLIdentity) Key() IdentityKey { return i.key }

// URL returns the canonical fetch URL.
func (i URLIdentity) URL() string { return i.url }

// Equal reports whether both identities match.
func (i URLIdentity) Equal(other URLIdentity) bool {
	return subtle.ConstantTimeCompare(i.key[:], other.key[:]) == 1 && i.url == other.url
}

// IdentityState classifies identity evaluation.
type IdentityState uint8

const (
	IdentityUnspecified IdentityState = iota
	IdentityAccepted
	IdentityRejected
)

// IdentityRequest is the input to an Identifier.
type IdentityRequest struct {
	RawURL   string
	Method   Method
	ParentID WorkID
	Source   SourceCode
	Depth    uint32
}

// IdentityResult is a typed identity evaluation outcome.
type IdentityResult struct {
	State    IdentityState
	Identity URLIdentity
	Code     ErrorCode
}

// Identifier derives crawl identity for a discovered URL.
type Identifier interface {
	Identify(context.Context, IdentityRequest) (IdentityResult, error)
}

// IdentityFunc adapts a function to Identifier.
type IdentityFunc func(context.Context, IdentityRequest) (IdentityResult, error)

// Identify implements Identifier.
func (f IdentityFunc) Identify(ctx context.Context, req IdentityRequest) (IdentityResult, error) {
	return f(ctx, req)
}

// DefaultIdentity derives a SHA-256 key over method and a structural canonical URL.
// Frontier mode should normally supply a Platform-bound Identifier instead.
func DefaultIdentity(ctx context.Context, req IdentityRequest) (IdentityResult, error) {
	_ = ctx
	if err := req.Method.Validate(); err != nil {
		return IdentityResult{State: IdentityRejected, Code: CodeMethodDenied}, nil
	}
	canonical, err := CanonicalFetchURL(req.RawURL)
	if err != nil {
		return IdentityResult{State: IdentityRejected, Code: CodeIdentityRejected}, nil
	}
	sum := sha256.Sum256([]byte(string(req.Method) + "\n" + canonical))
	id, err := NewURLIdentity(sum, canonical)
	if err != nil {
		return IdentityResult{State: IdentityRejected, Code: CodeIdentityRejected}, nil
	}
	return IdentityResult{State: IdentityAccepted, Identity: id}, nil
}

var urlParser = whatwgurl.NewParser(whatwgurl.WithPercentEncodeSinglePercentSign())

// CanonicalFetchURL parses and normalizes a crawl target for fetch use.
// Fragments and userinfo are rejected. Only http(s) are accepted.
func CanonicalFetchURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: empty url", ErrInvalidConfig)
	}
	parsed, err := urlParser.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	scheme := strings.ToLower(parsed.Protocol())
	scheme = strings.TrimSuffix(scheme, ":")
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: scheme", ErrInvalidConfig)
	}
	if parsed.Username() != "" || parsed.Password() != "" {
		return "", fmt.Errorf("%w: userinfo", ErrInvalidConfig)
	}
	if parsed.Hash() != "" {
		return "", fmt.Errorf("%w: fragment", ErrInvalidConfig)
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("%w: host", ErrInvalidConfig)
	}
	// Reject bare IPs that look like host for structural purposes is allowed;
	// address safety is enforced later at dial time.
	port := parsed.Port()
	path := parsed.Pathname()
	if path == "" {
		path = "/"
	}
	query := parsed.Search()
	var b strings.Builder
	b.WriteString(scheme)
	b.WriteString("://")
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		b.WriteByte('[')
		b.WriteString(host)
		b.WriteByte(']')
	} else {
		b.WriteString(host)
	}
	if port != "" {
		b.WriteByte(':')
		b.WriteString(port)
	}
	b.WriteString(path)
	if query != "" {
		if !strings.HasPrefix(query, "?") {
			b.WriteByte('?')
		}
		b.WriteString(query)
	}
	out := b.String()
	if err := validateCanonicalURL(out); err != nil {
		return "", err
	}
	return out, nil
}

func validateCanonicalURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: scheme", ErrInvalidConfig)
	}
	if u.User != nil {
		return fmt.Errorf("%w: userinfo", ErrInvalidConfig)
	}
	if u.Fragment != "" {
		return fmt.Errorf("%w: fragment", ErrInvalidConfig)
	}
	if u.Host == "" {
		return fmt.Errorf("%w: host", ErrInvalidConfig)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: host", ErrInvalidConfig)
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("%w: port", ErrInvalidConfig)
		}
	}
	// Ensure hostname is either a valid domain-like token or IP literal.
	if ip := net.ParseIP(host); ip == nil {
		if strings.Contains(host, " ") {
			return fmt.Errorf("%w: host", ErrInvalidConfig)
		}
	}
	return nil
}

// OriginKey returns scheme://host[:port] with default ports omitted.
func OriginKey(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("%w: host", ErrInvalidConfig)
	}
	port := u.Port()
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme == "http" && (port == "" || port == "80"):
		return "http://" + host, nil
	case scheme == "https" && (port == "" || port == "443"):
		return "https://" + host, nil
	case port != "":
		return scheme + "://" + host + ":" + port, nil
	default:
		return scheme + "://" + host, nil
	}
}
