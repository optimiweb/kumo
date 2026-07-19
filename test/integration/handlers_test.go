//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/optimiweb/kumo"
	htmlquery "github.com/optimiweb/kumo/pkg/markupquery/html"
	"github.com/optimiweb/kumo/pkg/robotspolicy"
	"github.com/optimiweb/kumo/pkg/sitemap"
)

// crawlLog records handler observations for assertions.
type crawlLog struct {
	mu        sync.Mutex
	URLs      []string
	Statuses  map[string]int
	Outcomes  map[string]kumo.FetchOutcome
	Redirects map[string]string
}

func newCrawlLog() *crawlLog {
	return &crawlLog{
		Statuses:  make(map[string]int),
		Outcomes:  make(map[string]kumo.FetchOutcome),
		Redirects: make(map[string]string),
	}
}

func (l *crawlLog) note(in kumo.HandleInput) {
	u := in.Lease().Work().URL()
	res := in.Result()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.URLs = append(l.URLs, u)
	l.Outcomes[u] = res.Outcome()
	if resp, ok := res.Response(); ok {
		l.Statuses[u] = resp.StatusCode()
	}
	if hop, ok := res.RedirectHop(); ok {
		l.Redirects[u] = hop.Location()
	} else if _, ok := res.Redirect(); ok {
		l.Redirects[u] = "discovered"
	}
}

func (l *crawlLog) saw(raw string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, u := range l.URLs {
		if u == raw {
			return true
		}
	}
	return false
}

func (l *crawlLog) status(raw string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Statuses[raw]
}

func (l *crawlLog) paths() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.URLs))
	for _, u := range l.URLs {
		out = append(out, urlPath(u))
	}
	return out
}

// linkDiscoveryHandler extracts same-host anchors and submits them.
func linkDiscoveryHandler(log *crawlLog, maxDepth uint32) kumo.WorkHandler {
	return kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		log.note(in)
		res := in.Result()
		if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
			return kumo.DefaultDecision(res)
		}
		resp, ok := res.Response()
		if !ok {
			return kumo.Fail(kumo.CodeHandlerFailed)
		}
		if resp.StatusCode() >= 400 {
			return kumo.Fail(kumo.CodeProtocolFailed)
		}
		work := in.Lease().Work()
		if work.Depth() >= maxDepth {
			return kumo.Ack()
		}
		if work.ResourceClass() != kumo.ResourceHTML {
			return kumo.Ack()
		}
		doc, err := htmlquery.Parse(resp.Body().Reader())
		if err != nil {
			return kumo.Fail(kumo.CodeContentDecodeFailed)
		}
		base, err := url.Parse(work.URL())
		if err != nil {
			return kumo.Fail(kumo.CodeHandlerFailed)
		}
		for _, n := range htmlquery.Find(doc, "//a[@href]") {
			href := strings.TrimSpace(htmlquery.SelectAttr(n, "href"))
			if href == "" || strings.HasPrefix(href, "#") {
				continue
			}
			ref, err := url.Parse(href)
			if err != nil {
				continue
			}
			abs := base.ResolveReference(ref)
			if abs.Scheme != "http" && abs.Scheme != "https" {
				continue
			}
			if !strings.EqualFold(abs.Hostname(), exampleHost) {
				continue
			}
			abs.Fragment = ""
			if _, err := sink.Submit(ctx, kumo.Discovery{
				URL:           abs.String(),
				Method:        kumo.MethodGET,
				Relation:      kumo.RelationLink,
				ResourceClass: kumo.ResourceHTML,
			}); err != nil {
				return kumo.Retry(time.Second, kumo.CodeDiscoveryUnresolved)
			}
		}
		return kumo.Ack()
	})
}

// sitemapInventoryHandler mirrors the cmd/kumo sitemap expansion path.
func sitemapInventoryHandler(log *crawlLog) kumo.WorkHandler {
	return kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		log.note(in)
		work := in.Lease().Work()
		res := in.Result()
		if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
			return kumo.DefaultDecision(res)
		}
		resp, ok := res.Response()
		if !ok {
			return kumo.Fail(kumo.CodeHandlerFailed)
		}
		switch work.ResourceClass() {
		case kumo.ResourceRobots:
			return handleRobots(ctx, resp, sink)
		case kumo.ResourceXMLSitemap, kumo.ResourceXMLSitemapIndex:
			return handleSitemap(ctx, resp, sink)
		default:
			if resp.StatusCode() >= 400 {
				return kumo.Fail(kumo.CodeProtocolFailed)
			}
			// Drain body so the fetch is fully consumed.
			_, _ = io.Copy(io.Discard, resp.Body().Reader())
			return kumo.Ack()
		}
	})
}

func handleRobots(ctx context.Context, resp kumo.HTTPResponse, sink kumo.DiscoverySink) kumo.Decision {
	if resp.StatusCode() >= 400 {
		return kumo.Ack()
	}
	body, err := io.ReadAll(resp.Body().Reader())
	if err != nil {
		return kumo.Fail(kumo.CodeContentDecodeFailed)
	}
	data, err := robotspolicy.FromBytes(body)
	if err != nil {
		return kumo.Ack()
	}
	for _, loc := range data.Sitemaps {
		if _, err := sink.Submit(ctx, kumo.Discovery{
			URL:           loc,
			Method:        kumo.MethodGET,
			Relation:      kumo.RelationRobotsSitemap,
			Priority:      15,
			ResourceClass: kumo.ResourceXMLSitemap,
		}); err != nil {
			return kumo.Retry(time.Second, kumo.CodeDiscoveryUnresolved)
		}
	}
	return kumo.Ack()
}

func handleSitemap(ctx context.Context, resp kumo.HTTPResponse, sink kumo.DiscoverySink) kumo.Decision {
	if resp.StatusCode() >= 400 {
		return kumo.Fail(kumo.CodeProtocolFailed)
	}
	parsed, err := sitemap.Parse(resp.Body().Reader())
	if err != nil {
		return kumo.Fail(kumo.CodeContentDecodeFailed)
	}
	for _, loc := range parsed.Sitemaps {
		if _, err := sink.Submit(ctx, kumo.Discovery{
			URL:           loc,
			Method:        kumo.MethodGET,
			Relation:      kumo.RelationSitemap,
			Priority:      15,
			ResourceClass: kumo.ResourceXMLSitemap,
		}); err != nil {
			return kumo.Retry(time.Second, kumo.CodeDiscoveryUnresolved)
		}
	}
	for _, loc := range parsed.URLs {
		if _, err := sink.Submit(ctx, kumo.Discovery{
			URL:           loc,
			Method:        kumo.MethodGET,
			Relation:      kumo.RelationSitemap,
			ResourceClass: kumo.ResourceHTML,
		}); err != nil {
			return kumo.Retry(time.Second, kumo.CodeDiscoveryUnresolved)
		}
	}
	return kumo.Ack()
}

// observeHandler records outcomes without discovering new URLs.
func observeHandler(log *crawlLog) kumo.WorkHandler {
	return kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		log.note(in)
		res := in.Result()
		if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
			return kumo.DefaultDecision(res)
		}
		resp, ok := res.Response()
		if !ok {
			return kumo.Fail(kumo.CodeHandlerFailed)
		}
		_, _ = io.Copy(io.Discard, resp.Body().Reader())
		if resp.StatusCode() >= 500 {
			return kumo.Retry(10*time.Millisecond, kumo.CodeTransportFailed)
		}
		if resp.StatusCode() >= 400 {
			return kumo.Fail(kumo.CodeProtocolFailed)
		}
		return kumo.Ack()
	})
}
