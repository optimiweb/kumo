//go:build integration

package integration_test

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/optimiweb/kumo"
	"github.com/optimiweb/kumo/internal/httpx"
	"github.com/optimiweb/kumo/memory"
)

type harness struct {
	t      *testing.T
	site   *fixtureSite
	cfg    kumo.CollectorConfig
	client *httpx.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	site := startExampleCom(t)

	cfg := kumo.DefaultCollectorConfig()
	cfg.Policy = kumo.HostPolicy(exampleHost)
	cfg.Workers = 2
	cfg.UserAgent = "KumoIntegrationTest/1.0"

	loopback := netip.MustParseAddr("127.0.0.1")
	dialHost := strings.TrimPrefix(site.Server.URL, "http://")
	client := httpx.NewClient(httpx.Config{
		UserAgent:       cfg.UserAgent,
		ConnectTimeout:  cfg.Timeouts.Connect,
		TLSTimeout:      cfg.Timeouts.TLS,
		HeaderTimeout:   cfg.Timeouts.Headers,
		BodyTimeout:     cfg.Timeouts.Body,
		TotalTimeout:    cfg.Timeouts.Total,
		MaxHeaderBytes:  cfg.MaxHeaderBytes,
		HeaderAllowlist: headerAllow(cfg.HeaderAllowlist),
		Resolver:        mapResolver{hosts: map[string]netip.Addr{exampleHost: loopback}},
		AllowAddrs:      []netip.Addr{loopback},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			// URL port is 80 (example.com); physical dial targets httptest.
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, "tcp", dialHost)
		},
	})

	return &harness{t: t, site: site, cfg: cfg, client: client}
}

func (h *harness) collector() *kumo.Collector {
	h.t.Helper()
	c, err := kumo.NewCollector(h.cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	kumo.SetTestHTTPClient(c, h.client)
	return c
}

func (h *harness) disableRobots() {
	h.cfg.Robots.Enabled = false
	h.cfg.Robots.Override = true
	h.cfg.Robots.OverrideReason = "integration-test"
}

func (h *harness) frontier(opts memory.MemoryFrontierOptions) *memory.MemoryFrontier {
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 2
	}
	if opts.MaxPages == 0 {
		opts.MaxPages = 50
	}
	if opts.FetchBudget == 0 {
		opts.FetchBudget = 100
	}
	if opts.MaxOriginConc == 0 {
		opts.MaxOriginConc = 2
	}
	return memory.NewMemoryFrontier(opts)
}

func (h *harness) enqueueSeed(fr *memory.MemoryFrontier, raw string, class kumo.ResourceClass) {
	h.t.Helper()
	id := kumo.IdentityFunc(kumo.DefaultIdentity)
	res, err := id(context.Background(), kumo.IdentityRequest{
		RawURL: raw,
		Method: kumo.MethodGET,
		Source: kumo.SourceSeed,
	})
	if err != nil || res.State != kumo.IdentityAccepted {
		h.t.Fatalf("identity %s: state=%v code=%s err=%v", raw, res.State, res.Code, err)
	}
	if _, err := fr.EnqueueSeed(context.Background(), kumo.EnqueueRequest{
		Identity:      res.Identity,
		Method:        kumo.MethodGET,
		Source:        kumo.SourceSeed,
		ResourceClass: class,
	}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) runFrontier(fr *memory.MemoryFrontier, handler kumo.WorkHandler) kumo.RunReport {
	h.t.Helper()
	if err := fr.SealSeeds(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := h.collector().RunFrontier(ctx, kumo.FrontierRunConfig{
		Frontier:     fr,
		Identifier:   kumo.IdentityFunc(kumo.DefaultIdentity),
		Handler:      handler,
		UntilDrained: true,
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return report
}

type mapResolver struct {
	hosts map[string]netip.Addr
}

func (m mapResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	host = strings.ToLower(host)
	if ip, ok := m.hosts[host]; ok {
		return []netip.Addr{ip}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func headerAllow(list []string) map[string]struct{} {
	out := make(map[string]struct{}, len(list))
	for _, h := range list {
		out[strings.ToLower(h)] = struct{}{}
	}
	return out
}

func urlPath(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.Index(raw, "/"); i >= 0 {
		return raw[i:]
	}
	return "/"
}
