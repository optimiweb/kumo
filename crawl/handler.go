package crawl

import (
	"context"
)

// DiscoveryRelation classifies a discovered URL relationship.
type DiscoveryRelation uint8

const (
	RelationUnspecified DiscoveryRelation = iota
	RelationLink
	RelationRedirect
	RelationSitemap
	RelationCanonical
	RelationHreflang
	RelationRobotsSitemap
)

// String returns a stable relation name.
func (r DiscoveryRelation) String() string {
	switch r {
	case RelationLink:
		return "link"
	case RelationRedirect:
		return "redirect"
	case RelationSitemap:
		return "sitemap"
	case RelationCanonical:
		return "canonical"
	case RelationHreflang:
		return "hreflang"
	case RelationRobotsSitemap:
		return "robots_sitemap"
	default:
		return "unspecified"
	}
}

// Discovery is a handler-submitted candidate URL.
type Discovery struct {
	URL      string
	Method   Method
	Relation DiscoveryRelation
	Priority int32
	// ResourceClass optionally overrides the class inferred from Relation.
	// Set it for child sitemaps (XML) discovered from a sitemap index;
	// page URLs from a urlset default to HTML.
	ResourceClass ResourceClass
}

// DiscoveryState classifies discovery submission.
type DiscoveryState uint8

const (
	DiscoveryUnspecified DiscoveryState = iota
	DiscoveryInserted
	DiscoveryDuplicate
	DiscoveryRejected
	DiscoveryLimitReached
)

// DiscoveryResult is returned by DiscoverySink.Submit.
type DiscoveryResult struct {
	State DiscoveryState
	ID    WorkID
	Code  ErrorCode
}

// DiscoverySink accepts discoveries during handler execution only.
type DiscoverySink interface {
	Submit(ctx context.Context, d Discovery) (DiscoveryResult, error)
}

// HandleInput is the immutable handler input.
type HandleInput struct {
	lease       Lease
	result      FetchResult
	redirect    DiscoveryResult
	hasRedirect bool
}

// NewHandleInput constructs handler input.
func NewHandleInput(lease Lease, result FetchResult, redirect DiscoveryResult, hasRedirect bool) HandleInput {
	return HandleInput{
		lease:       lease,
		result:      result,
		redirect:    redirect,
		hasRedirect: hasRedirect,
	}
}

func (i HandleInput) Lease() Lease        { return i.lease }
func (i HandleInput) Result() FetchResult { return i.result }
func (i HandleInput) Redirect() (DiscoveryResult, bool) {
	return i.redirect, i.hasRedirect
}

// WorkHandler processes one completed fetch under an active lease.
type WorkHandler interface {
	Handle(ctx context.Context, input HandleInput, sink DiscoverySink) Decision
}

// HandlerFunc adapts a function to WorkHandler.
type HandlerFunc func(context.Context, HandleInput, DiscoverySink) Decision

// Handle implements WorkHandler.
func (f HandlerFunc) Handle(ctx context.Context, input HandleInput, sink DiscoverySink) Decision {
	return f(ctx, input, sink)
}
