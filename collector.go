package kumo

import (
	"context"
	"crypto/rand"
	"strings"

	"github.com/optimiweb/kumo/internal/engine"
	"github.com/optimiweb/kumo/internal/httpx"
	"github.com/optimiweb/kumo/memory"
)

// Collector is an immutable, fail-closed crawl executor.
type Collector struct {
	cfg    CollectorConfig
	client *httpx.Client
}

// newOperationID remains in the facade for the integration-test export seam.
func newOperationID() (OperationID, error) {
	var id OperationID
	_, err := rand.Read(id[:])
	return id, err
}

// NewCollector constructs a collector after validating configuration.
func NewCollector(cfg CollectorConfig) (*Collector, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// Deep-copy mutable config inputs.
	cfg.HeaderAllowlist = append([]string(nil), cfg.HeaderAllowlist...)
	bodies := make(ResourceLimits, len(cfg.Bodies))
	for k, v := range cfg.Bodies {
		bodies[k] = v
	}
	cfg.Bodies = bodies

	allow := make(map[string]struct{}, len(cfg.HeaderAllowlist))
	for _, h := range cfg.HeaderAllowlist {
		allow[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}
	client := httpx.NewClient(httpx.Config{
		UserAgent:       cfg.UserAgent,
		ConnectTimeout:  cfg.Timeouts.Connect,
		TLSTimeout:      cfg.Timeouts.TLS,
		HeaderTimeout:   cfg.Timeouts.Headers,
		BodyTimeout:     cfg.Timeouts.Body,
		TotalTimeout:    cfg.Timeouts.Total,
		MaxHeaderBytes:  cfg.MaxHeaderBytes,
		HeaderAllowlist: allow,
	})
	return &Collector{cfg: cfg, client: client}, nil
}

// RunDirect executes a bounded direct-mode crawl.
func (c *Collector) RunDirect(ctx context.Context, cfg DirectRunConfig) (RunReport, error) {
	if err := cfg.Validate(); err != nil {
		return RunReport{}, err
	}
	if cfg.Identifier == nil {
		cfg.Identifier = IdentityFunc(DefaultIdentity)
	}
	frontier := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{
		MaxAttempts:    cfg.MaxAttempts,
		MaxPages:       cfg.MaxWorkItems,
		FetchBudget:    cfg.MaxFetchAttempts,
		MaxOriginConc:  cfg.MaxConcurrency,
		RatePerOrigin:  cfg.PerOriginRate,
		BurstPerOrigin: float64(cfg.PerOriginBurst),
	})
	return engine.New(c.cfg, c.client).RunDirect(ctx, cfg, frontier)
}

// RunFrontier executes a durable frontier-mode crawl.
func (c *Collector) RunFrontier(ctx context.Context, cfg FrontierRunConfig) (RunReport, error) {
	if err := cfg.Validate(); err != nil {
		return RunReport{}, err
	}
	return engine.New(c.cfg, c.client).RunFrontier(ctx, cfg)
}
