// Package handlers provides composable, pre-built WorkHandler implementations.
package handlers

import (
	"context"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/pkg/robotspolicy"
	"github.com/optimiweb/kumo/pkg/seo"
	"github.com/optimiweb/kumo/pkg/sitemap"
)

// SitemapHandlerOptions configures sitemap and robots discovery behavior.
type SitemapHandlerOptions struct {
	RobotsSitemapPriority int32
	ChildSitemapPriority  int32
	PageURLPriority       int32
}

// DefaultSitemapHandlerOptions returns standard discovery priorities.
func DefaultSitemapHandlerOptions() SitemapHandlerOptions {
	return SitemapHandlerOptions{
		RobotsSitemapPriority: 15,
		ChildSitemapPriority:  15,
		PageURLPriority:       0,
	}
}

// NewSitemapHandler handles robots.txt sitemap declarations and XML sitemap expansions.
func NewSitemapHandler(opts SitemapHandlerOptions, next crawl.WorkHandler) crawl.WorkHandler {
	return crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		work := in.Lease().Work()
		res := in.Result()

		if res.Outcome() != crawl.FetchOutcomeHTTPResponse {
			if next != nil {
				return next.Handle(ctx, in, sink)
			}
			return crawl.DefaultDecision(res)
		}

		resp, ok := res.Response()
		if !ok {
			if next != nil {
				return next.Handle(ctx, in, sink)
			}
			return crawl.Fail(crawl.CodeHandlerFailed)
		}

		switch work.ResourceClass() {
		case crawl.ResourceRobots:
			if resp.StatusCode() >= 400 {
				if next != nil {
					return next.Handle(ctx, in, sink)
				}
				return crawl.Ack()
			}
			body, err := io.ReadAll(resp.Body().Reader())
			if err != nil {
				return crawl.Fail(crawl.CodeContentDecodeFailed)
			}
			data, err := robotspolicy.FromBytes(body)
			if err != nil {
				if next != nil {
					return next.Handle(ctx, in, sink)
				}
				return crawl.Ack()
			}
			for _, loc := range data.Sitemaps {
				if _, err := sink.Submit(ctx, crawl.Discovery{
					URL:           loc,
					Method:        crawl.MethodGET,
					Relation:      crawl.RelationRobotsSitemap,
					Priority:      opts.RobotsSitemapPriority,
					ResourceClass: crawl.ResourceXMLSitemap,
				}); err != nil {
					return crawl.Retry(time.Second, crawl.CodeDiscoveryUnresolved)
				}
			}
			if next != nil {
				return next.Handle(ctx, in, sink)
			}
			return crawl.Ack()

		case crawl.ResourceXMLSitemap, crawl.ResourceXMLSitemapIndex:
			if resp.StatusCode() >= 400 {
				return crawl.Fail(crawl.CodeProtocolFailed)
			}
			parsed, err := sitemap.Parse(resp.Body().Reader())
			if err != nil {
				return crawl.Fail(crawl.CodeContentDecodeFailed)
			}
			for _, loc := range parsed.SitemapLocs() {
				if _, err := sink.Submit(ctx, crawl.Discovery{
					URL:           loc,
					Method:        crawl.MethodGET,
					Relation:      crawl.RelationSitemap,
					Priority:      opts.ChildSitemapPriority,
					ResourceClass: crawl.ResourceXMLSitemap,
				}); err != nil {
					return crawl.Retry(time.Second, crawl.CodeDiscoveryUnresolved)
				}
			}
			for _, loc := range parsed.URLLocs() {
				if _, err := sink.Submit(ctx, crawl.Discovery{
					URL:           loc,
					Method:        crawl.MethodGET,
					Relation:      crawl.RelationSitemap,
					Priority:      opts.PageURLPriority,
					ResourceClass: crawl.ResourceHTML,
				}); err != nil {
					return crawl.Retry(time.Second, crawl.CodeDiscoveryUnresolved)
				}
			}
			if next != nil {
				return next.Handle(ctx, in, sink)
			}
			return crawl.Ack()

		default:
			if next != nil {
				return next.Handle(ctx, in, sink)
			}
			if resp.StatusCode() >= 400 {
				return crawl.Fail(crawl.CodeProtocolFailed)
			}
			return crawl.Ack()
		}
	})
}

// LinkDiscoveryOptions configures HTML link discovery.
type LinkDiscoveryOptions struct {
	MaxDepth     uint32
	SameHostOnly bool
	Priority     int32
}

// NewLinkDiscoveryHandler extracts same-host links from HTML responses and enqueues them.
func NewLinkDiscoveryHandler(opts LinkDiscoveryOptions, next crawl.WorkHandler) crawl.WorkHandler {
	return crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		work := in.Lease().Work()
		res := in.Result()

		if res.Outcome() != crawl.FetchOutcomeHTTPResponse {
			if next != nil {
				return next.Handle(ctx, in, sink)
			}
			return crawl.DefaultDecision(res)
		}

		resp, ok := res.Response()
		if !ok {
			if next != nil {
				return next.Handle(ctx, in, sink)
			}
			return crawl.Fail(crawl.CodeHandlerFailed)
		}

		if work.ResourceClass() == crawl.ResourceHTML && resp.StatusCode() < 400 && (opts.MaxDepth == 0 || work.Depth() < opts.MaxDepth) {
			_, links, err := seo.Extract(resp.Body().Reader(), work.URL())
			if err == nil {
				baseHost := work.Identity().Host()
				for _, link := range links {
					if opts.SameHostOnly {
						u, err := url.Parse(link.ResolvedURL)
						if err != nil || !strings.EqualFold(u.Hostname(), baseHost) {
							continue
						}
					}
					if _, err := sink.Submit(ctx, crawl.Discovery{
						URL:           link.ResolvedURL,
						Method:        crawl.MethodGET,
						Relation:      crawl.RelationLink,
						Priority:      opts.Priority,
						ResourceClass: crawl.ResourceHTML,
					}); err != nil {
						return crawl.Retry(time.Second, crawl.CodeDiscoveryUnresolved)
					}
				}
			}
		}

		if next != nil {
			return next.Handle(ctx, in, sink)
		}
		if resp.StatusCode() >= 400 {
			return crawl.Fail(crawl.CodeProtocolFailed)
		}
		return crawl.Ack()
	})
}

// ChainHandlers executes a slice of WorkHandlers. The first handler to return a non-Ack decision halts the chain.
func ChainHandlers(handlers ...crawl.WorkHandler) crawl.WorkHandler {
	return crawl.HandlerFunc(func(ctx context.Context, in crawl.HandleInput, sink crawl.DiscoverySink) crawl.Decision {
		for _, h := range handlers {
			if h == nil {
				continue
			}
			dec := h.Handle(ctx, in, sink)
			if dec.Kind() != crawl.DecisionAck {
				return dec
			}
		}
		return crawl.Ack()
	})
}
