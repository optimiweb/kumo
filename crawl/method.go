package crawl

import (
	"fmt"
	"strings"
)

// Method is an allowed HTTP method for crawl work.
type Method string

const (
	MethodGET  Method = "GET"
	MethodHEAD Method = "HEAD"
)

// Validate reports whether the method is allowed.
func (m Method) Validate() error {
	switch m {
	case MethodGET, MethodHEAD:
		return nil
	default:
		return fmt.Errorf("%w: method %q", ErrInvalidConfig, m)
	}
}

// String returns the method text.
func (m Method) String() string { return string(m) }

// NormalizeMethod uppercases and validates a method string.
func NormalizeMethod(s string) (Method, error) {
	m := Method(strings.ToUpper(strings.TrimSpace(s)))
	if err := m.Validate(); err != nil {
		return "", err
	}
	return m, nil
}

// ResourceClass selects body limits and parsing expectations.
type ResourceClass uint8

const (
	ResourceUnspecified ResourceClass = iota
	ResourceHTML
	ResourceXMLSitemap
	ResourceXMLSitemapIndex
	ResourceRobots
	ResourceText
)

// String returns a stable resource class name.
func (c ResourceClass) String() string {
	switch c {
	case ResourceHTML:
		return "html"
	case ResourceXMLSitemap:
		return "xml_sitemap"
	case ResourceXMLSitemapIndex:
		return "xml_sitemap_index"
	case ResourceRobots:
		return "robots"
	case ResourceText:
		return "text"
	default:
		return "unspecified"
	}
}

// Validate reports whether the class is known.
func (c ResourceClass) Validate() error {
	switch c {
	case ResourceHTML, ResourceXMLSitemap, ResourceXMLSitemapIndex, ResourceRobots, ResourceText:
		return nil
	default:
		return fmt.Errorf("%w: resource class", ErrInvalidConfig)
	}
}
