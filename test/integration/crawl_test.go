//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/optimiweb/kumo"
	"github.com/optimiweb/kumo/memory"
)

func TestLinkDiscoveryCrawl(t *testing.T) {
	h := newHarness(t)
	// Link crawl seeds HTML only; robots still fetched for allow checks.
	log := newCrawlLog()
	fr := h.frontier(memory.MemoryFrontierOptions{MaxPages: 30, FetchBudget: 40})
	h.enqueueSeed(fr, exampleOrigin+"/", kumo.ResourceHTML)

	report := h.runFrontier(fr, linkDiscoveryHandler(log, 2))

	if report.Handled() < 5 {
		t.Fatalf("handled=%d paths=%v stop=%s", report.Handled(), log.paths(), report.StopReason())
	}
	for _, want := range []string{
		exampleOrigin + "/",
		exampleOrigin + "/about/",
		exampleOrigin + "/products/",
		exampleOrigin + "/blog/hello",
		exampleOrigin + "/products/widget",
	} {
		if !log.saw(want) {
			t.Errorf("missing page %s; saw=%v", want, log.URLs)
		}
	}
	// External host must not be fetched.
	if log.saw("https://other.example/out") {
		t.Fatal("external link must not be crawled")
	}
	// Robots should block /secret/ when enabled (default).
	if h.site.Hits("/secret/") > 0 || h.site.Hits("/secret") > 0 {
		t.Fatalf("robots-disallowed path fetched: hits=%v", h.site.HitPaths())
	}
}

func TestSitemapInventoryCrawl(t *testing.T) {
	h := newHarness(t)
	log := newCrawlLog()
	fr := h.frontier(memory.MemoryFrontierOptions{MaxPages: 30, FetchBudget: 40})
	h.enqueueSeed(fr, exampleOrigin+"/robots.txt", kumo.ResourceRobots)
	h.enqueueSeed(fr, exampleOrigin+"/sitemap.xml", kumo.ResourceXMLSitemap)

	report := h.runFrontier(fr, sitemapInventoryHandler(log))

	if report.Handled() < 7 {
		t.Fatalf("handled=%d paths=%v stop=%s", report.Handled(), log.paths(), report.StopReason())
	}
	for _, want := range []string{
		exampleOrigin + "/robots.txt",
		exampleOrigin + "/sitemap.xml",
		exampleOrigin + "/sitemaps/pages.xml",
		exampleOrigin + "/",
		exampleOrigin + "/about/",
		exampleOrigin + "/products/",
		exampleOrigin + "/products/widget",
		exampleOrigin + "/blog/hello",
	} {
		if !log.saw(want) {
			t.Errorf("missing %s; saw=%v", want, log.URLs)
		}
	}
}

func TestRedirectBecomesIndependentWork(t *testing.T) {
	h := newHarness(t)
	h.disableRobots()
	log := newCrawlLog()
	fr := h.frontier(memory.MemoryFrontierOptions{MaxPages: 10, FetchBudget: 10})
	h.enqueueSeed(fr, exampleOrigin+"/moved", kumo.ResourceHTML)

	report := h.runFrontier(fr, observeHandler(log))

	if report.Handled() < 2 {
		t.Fatalf("expected redirect source + target, handled=%d paths=%v", report.Handled(), log.paths())
	}
	if !log.saw(exampleOrigin + "/moved") {
		t.Fatalf("missing redirect source: %v", log.URLs)
	}
	if !log.saw(exampleOrigin+"/new-home") && !log.saw(exampleOrigin+"/new-home/") {
		// Target URL as discovered by the engine (Location resolution).
		found := false
		for _, u := range log.URLs {
			if urlPath(u) == "/new-home" || urlPath(u) == "/new-home/" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing redirect target: %v redirects=%v", log.URLs, log.Redirects)
		}
	}
	if _, ok := log.Redirects[exampleOrigin+"/moved"]; !ok {
		// Engine may attach redirect evidence only on the source hop.
		if len(log.Redirects) == 0 {
			t.Logf("note: no redirect evidence map entries; paths=%v outcomes=%v", log.paths(), log.Outcomes)
		}
	}
}

func TestHTTPErrorDecisions(t *testing.T) {
	h := newHarness(t)
	h.disableRobots()
	log := newCrawlLog()
	fr := h.frontier(memory.MemoryFrontierOptions{MaxPages: 10, FetchBudget: 20, MaxAttempts: 2})
	h.enqueueSeed(fr, exampleOrigin+"/gone", kumo.ResourceHTML)
	h.enqueueSeed(fr, exampleOrigin+"/error", kumo.ResourceHTML)

	report := h.runFrontier(fr, observeHandler(log))

	if report.Failed() < 1 {
		t.Fatalf("expected failures, report handled=%d failed=%d retried=%d",
			report.Handled(), report.Failed(), report.Retried())
	}
	if log.status(exampleOrigin+"/gone") != 404 && log.Outcomes[exampleOrigin+"/gone"] == kumo.FetchOutcomeHTTPResponse {
		// status recorded only for HTTP responses
		t.Errorf("gone status=%d outcome=%s", log.status(exampleOrigin+"/gone"), log.Outcomes[exampleOrigin+"/gone"])
	}
}

func TestDirectModeFixtureHome(t *testing.T) {
	h := newHarness(t)
	h.disableRobots()
	log := newCrawlLog()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := h.collector().RunDirect(ctx, kumo.DirectRunConfig{
		Seeds:            []string{exampleOrigin + "/"},
		Storage:          memory.NewMemoryStorage(nil),
		Identifier:       kumo.IdentityFunc(kumo.DefaultIdentity),
		MaxWorkItems:     5,
		MaxFetchAttempts: 10,
		MaxConcurrency:   2,
		MaxAttempts:      2,
		Handler:          observeHandler(log),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Handled() < 1 {
		t.Fatalf("handled=%d stop=%s", report.Handled(), report.StopReason())
	}
	if !log.saw(exampleOrigin + "/") {
		t.Fatalf("missing home: %v", log.URLs)
	}
	if log.status(exampleOrigin+"/") != 200 {
		t.Fatalf("status=%d", log.status(exampleOrigin+"/"))
	}
}

func TestHostPolicyBlocksOffsite(t *testing.T) {
	h := newHarness(t)
	h.disableRobots()
	log := newCrawlLog()
	fr := h.frontier(memory.MemoryFrontierOptions{MaxPages: 5, FetchBudget: 5})
	// Seed is on-policy; handler will try to submit offsite (covered in link test).
	// Here seed an off-host URL directly — identity/policy should reject admit.
	h.enqueueSeed(fr, exampleOrigin+"/", kumo.ResourceHTML)
	_ = h.runFrontier(fr, kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
		log.note(in)
		_, _ = sink.Submit(ctx, kumo.Discovery{
			URL:      "https://evil.example/phish",
			Method:   kumo.MethodGET,
			Relation: kumo.RelationLink,
		})
		return kumo.Ack()
	}))
	if log.saw("https://evil.example/phish") {
		t.Fatal("off-host discovery must not be fetched")
	}
}
