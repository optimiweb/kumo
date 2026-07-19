// Command kumo crawls all URLs declared by a domain's XML sitemaps.
//
// It seeds the domain's robots.txt and /sitemap.xml, discovers any
// robots-declared sitemaps and sitemap indexes, and fetches every page,
// respecting robots.txt rules throughout. All state lives in Kumo's
// deterministic in-memory adapters (frontier queue, fetch reservations,
// robots cache) — nothing touches disk or a database.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/optimiweb/kumo"
	"github.com/optimiweb/kumo/memory"
	"github.com/optimiweb/kumo/pkg/robotspolicy"
	"github.com/optimiweb/kumo/pkg/sitemap"
)

func main() {
	var (
		workers    = flag.Int("workers", 4, "concurrent crawl workers")
		maxPages   = flag.Int("max-pages", 5000, "maximum discovered URLs kept in the frontier")
		maxFetches = flag.Int("max-fetches", 10000, "maximum physical HTTP requests (includes robots and sitemaps)")
		attempts   = flag.Uint("attempts", 2, "maximum attempts per URL")
		timeout    = flag.Duration("timeout", 5*time.Minute, "overall crawl timeout")
		quiet      = flag.Bool("q", false, "only print the final summary")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: kumo [flags] <domain>\n\nCrawls every URL in the domain's sitemaps, respecting robots.txt.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *workers, *maxPages, *maxFetches, uint32(*attempts), *timeout, *quiet); err != nil {
		fmt.Fprintln(os.Stderr, "kumo:", err)
		os.Exit(1)
	}
}

func run(domain string, workers, maxPages, maxFetches int, attempts uint32, timeout time.Duration, quiet bool) error {
	base, host, err := normalizeDomain(domain)
	if err != nil {
		return err
	}

	cfg := kumo.DefaultCollectorConfig()
	cfg.Policy = kumo.HostPolicy(host)
	cfg.Workers = workers
	collector, err := kumo.NewCollector(cfg)
	if err != nil {
		return err
	}

	// In-memory frontier: the queue, dedupe, leases, fetch reservations,
	// and robots cache all live here.
	fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{
		MaxAttempts:   attempts,
		MaxPages:      maxPages,
		FetchBudget:   maxFetches,
		MaxOriginConc: workers,
	})
	id := kumo.IdentityFunc(kumo.DefaultIdentity)

	// Seed robots.txt first (it may declare sitemaps), then the
	// conventional /sitemap.xml location.
	seeds := []struct {
		path  string
		class kumo.ResourceClass
	}{
		{"/robots.txt", kumo.ResourceRobots},
		{"/sitemap.xml", kumo.ResourceXMLSitemap},
	}
	for _, s := range seeds {
		if err := enqueue(context.Background(), fr, id, base+s.path, kumo.SourceSeed, s.class, 20); err != nil {
			return err
		}
	}
	if err := fr.SealSeeds(context.Background()); err != nil {
		return err
	}

	handler := kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		return handle(ctx, in, sink, quiet)
	})

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	report, err := collector.RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier:     fr,
		Identifier:   id,
		Handler:      handler,
		UntilDrained: true,
		PollInterval: 50 * time.Millisecond,
	})

	fmt.Printf("\nhandled=%d failed=%d retried=%d fetched=%d stop=%s duration=%s\n",
		report.Handled(), report.Failed(), report.Retried(), report.Fetched(),
		report.StopReason(), report.Duration().Round(time.Millisecond))
	return err
}

// normalizeDomain validates the target and returns its origin and host.
func normalizeDomain(arg string) (string, string, error) {
	if !strings.Contains(arg, "://") {
		arg = "https://" + arg
	}
	u, err := url.Parse(arg)
	if err != nil {
		return "", "", fmt.Errorf("invalid domain: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("scheme must be http or https")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", "", fmt.Errorf("missing host")
	}
	base := u.Scheme + "://" + u.Host
	return base, host, nil
}

// enqueue identifies and submits one URL to the frontier.
func enqueue(
	ctx context.Context,
	fr *memory.MemoryFrontier,
	id kumo.Identifier,
	raw string,
	source kumo.SourceCode,
	class kumo.ResourceClass,
	priority int32,
) error {
	res, err := id.Identify(ctx, kumo.IdentityRequest{
		RawURL: raw,
		Method: kumo.MethodGET,
		Source: source,
	})
	if err != nil {
		return err
	}
	if res.State != kumo.IdentityAccepted {
		return fmt.Errorf("identity rejected for %s", raw)
	}
	_, err = fr.EnqueueSeed(ctx, kumo.EnqueueRequest{
		Identity:      res.Identity,
		Method:        kumo.MethodGET,
		Source:        source,
		Priority:      priority,
		ResourceClass: class,
	})
	return err
}

// handle processes one completed work item by resource class.
func handle(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink, quiet bool) kumo.Decision {
	work := in.Lease().Work()
	res := in.Result()

	if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
		if !quiet {
			fmt.Printf("%-8s %-12s %s\n", work.ResourceClass(), res.Outcome(), work.URL())
		}
		return kumo.DefaultDecision(res)
	}
	resp, _ := res.Response()

	if !quiet {
		fmt.Printf("%-8s %-12d %s\n", work.ResourceClass(), resp.StatusCode(), work.URL())
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
		return kumo.Ack()
	}
}

// handleRobots extracts Sitemap: declarations from a fetched robots.txt and
// enqueues each declared sitemap. If robots.txt is missing (404), nothing is
// discovered — the /sitemap.xml seed still runs.
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
		return kumo.Ack() // malformed robots: the pipeline already enforces deny-by-default
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

// handleSitemap expands sitemap indexes into child sitemaps and urlsets into
// page URLs.
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
			Priority:      0,
			ResourceClass: kumo.ResourceHTML,
		}); err != nil {
			return kumo.Retry(time.Second, kumo.CodeDiscoveryUnresolved)
		}
	}
	return kumo.Ack()
}
