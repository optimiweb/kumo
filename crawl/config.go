package crawl

import (
	"fmt"
	"time"
)

// BodyLimits bounds compressed, decoded, and text conversion sizes.
type BodyLimits struct {
	WireBytes     int64
	DecodedBytes  int64
	ConvertedText int64
}

// Validate checks body limits are finite and positive.
func (b BodyLimits) Validate() error {
	if b.WireBytes <= 0 || b.DecodedBytes <= 0 || b.ConvertedText <= 0 {
		return fmt.Errorf("%w: body limits must be positive", ErrInvalidConfig)
	}
	return nil
}

// ResourceLimits maps resource classes to body limits.
type ResourceLimits map[ResourceClass]BodyLimits

// Validate checks all configured classes.
func (r ResourceLimits) Validate() error {
	if len(r) == 0 {
		return fmt.Errorf("%w: resource limits required", ErrInvalidConfig)
	}
	for class, lim := range r {
		if err := class.Validate(); err != nil {
			return err
		}
		if err := lim.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DefaultResourceLimits returns conservative production defaults.
func DefaultResourceLimits() ResourceLimits {
	return ResourceLimits{
		ResourceHTML:            {WireBytes: 2 << 20, DecodedBytes: 5 << 20, ConvertedText: 5 << 20},
		ResourceXMLSitemap:      {WireBytes: 5 << 20, DecodedBytes: 10 << 20, ConvertedText: 10 << 20},
		ResourceXMLSitemapIndex: {WireBytes: 2 << 20, DecodedBytes: 5 << 20, ConvertedText: 5 << 20},
		ResourceRobots:          {WireBytes: 256 << 10, DecodedBytes: 512 << 10, ConvertedText: 512 << 10},
		ResourceText:            {WireBytes: 1 << 20, DecodedBytes: 2 << 20, ConvertedText: 2 << 20},
	}
}

// TimeoutConfig bounds network and processing phases.
type TimeoutConfig struct {
	Connect time.Duration
	TLS     time.Duration
	Headers time.Duration
	Body    time.Duration
	Total   time.Duration
	Process time.Duration
}

// Validate checks timeouts.
func (t TimeoutConfig) Validate() error {
	if t.Connect <= 0 || t.TLS <= 0 || t.Headers <= 0 || t.Body <= 0 || t.Total <= 0 || t.Process <= 0 {
		return fmt.Errorf("%w: timeouts must be positive", ErrInvalidConfig)
	}
	return nil
}

// DefaultTimeouts returns conservative defaults.
func DefaultTimeouts() TimeoutConfig {
	return TimeoutConfig{
		Connect: 5 * time.Second,
		TLS:     5 * time.Second,
		Headers: 10 * time.Second,
		Body:    30 * time.Second,
		Total:   60 * time.Second,
		Process: 30 * time.Second,
	}
}

// RobotsConfig configures robots behavior.
type RobotsConfig struct {
	Enabled           bool
	TTL               time.Duration
	FailureTTL        time.Duration
	RedirectLimit     uint16
	UserAgentToken    string
	MaxHeaderBytes    int
	Override          bool
	OverrideReason    string
	OverrideExpiresAt time.Time
}

// Validate checks robots configuration.
func (r RobotsConfig) Validate() error {
	if !r.Enabled {
		if !r.Override {
			return fmt.Errorf("%w: robots disabled without override", ErrInvalidConfig)
		}
		if r.OverrideReason == "" {
			return fmt.Errorf("%w: robots override reason required", ErrInvalidConfig)
		}
	}
	if r.TTL <= 0 || r.FailureTTL <= 0 {
		return fmt.Errorf("%w: robots ttl", ErrInvalidConfig)
	}
	if r.UserAgentToken == "" {
		return fmt.Errorf("%w: robots user agent token", ErrInvalidConfig)
	}
	if r.MaxHeaderBytes <= 0 {
		return fmt.Errorf("%w: robots header limit", ErrInvalidConfig)
	}
	return nil
}

// DefaultRobotsConfig returns robots-on-by-default settings.
func DefaultRobotsConfig() RobotsConfig {
	return RobotsConfig{
		Enabled:        true,
		TTL:            time.Hour,
		FailureTTL:     10 * time.Minute,
		RedirectLimit:  3,
		UserAgentToken: "OCDN_crawler",
		MaxHeaderBytes: 32 << 10,
	}
}

// CollectorConfig is shared immutable collector configuration.
type CollectorConfig struct {
	UserAgent       string
	Policy          TargetPolicy
	Bodies          ResourceLimits
	Timeouts        TimeoutConfig
	Robots          RobotsConfig
	MaxRedirectHops uint16
	MaxDiscoveries  int
	MaxHeaderBytes  int
	HeaderAllowlist []string
	MaxDepth        uint32
	LeaseDuration   time.Duration
	FetchLease      time.Duration
	RobotsLease     time.Duration
	RenewSafety     time.Duration
	Workers         int
}

// Validate checks shared configuration.
func (c CollectorConfig) Validate() error {
	if c.UserAgent == "" {
		return fmt.Errorf("%w: user agent", ErrInvalidConfig)
	}
	if c.Policy == nil {
		return fmt.Errorf("%w: target policy", ErrInvalidConfig)
	}
	if err := c.Bodies.Validate(); err != nil {
		return err
	}
	if err := c.Timeouts.Validate(); err != nil {
		return err
	}
	if err := c.Robots.Validate(); err != nil {
		return err
	}
	if c.MaxRedirectHops == 0 {
		return fmt.Errorf("%w: redirect hops", ErrInvalidConfig)
	}
	if c.MaxDiscoveries <= 0 {
		return fmt.Errorf("%w: max discoveries", ErrInvalidConfig)
	}
	if c.MaxHeaderBytes <= 0 {
		return fmt.Errorf("%w: max header bytes", ErrInvalidConfig)
	}
	if len(c.HeaderAllowlist) == 0 {
		return fmt.Errorf("%w: header allowlist", ErrInvalidConfig)
	}
	if c.LeaseDuration <= 0 || c.FetchLease <= 0 || c.RobotsLease <= 0 {
		return fmt.Errorf("%w: lease durations", ErrInvalidConfig)
	}
	if c.RenewSafety <= 0 || c.RenewSafety >= c.LeaseDuration {
		return fmt.Errorf("%w: renew safety", ErrInvalidConfig)
	}
	if c.Workers <= 0 {
		return fmt.Errorf("%w: workers", ErrInvalidConfig)
	}
	return nil
}

// DefaultHeaderAllowlist returns safe response headers visible to handlers.
func DefaultHeaderAllowlist() []string {
	return []string{
		"content-type",
		"content-length",
		"content-encoding",
		"cache-control",
		"expires",
		"etag",
		"last-modified",
		"location",
		"x-robots-tag",
		"x-cache",
		"age",
	}
}

// DefaultCollectorConfig returns a fail-closed baseline config.
func DefaultCollectorConfig() CollectorConfig {
	return CollectorConfig{
		UserAgent:       "OCDN_crawler_Desktop_d; +https://optimi.com/",
		Policy:          DefaultBaselinePolicy(),
		Bodies:          DefaultResourceLimits(),
		Timeouts:        DefaultTimeouts(),
		Robots:          DefaultRobotsConfig(),
		MaxRedirectHops: 5,
		MaxDiscoveries:  100,
		MaxHeaderBytes:  64 << 10,
		HeaderAllowlist: DefaultHeaderAllowlist(),
		MaxDepth:        10,
		LeaseDuration:   30 * time.Second,
		FetchLease:      30 * time.Second,
		RobotsLease:     30 * time.Second,
		RenewSafety:     5 * time.Second,
		Workers:         2,
	}
}

// RetryBackoffOr returns the renewal safety interval or the fallback. It is a
// safe default delay before retrying a transient failure.
func (c CollectorConfig) RetryBackoffOr(fallback time.Duration) time.Duration {
	if c.RenewSafety > 0 {
		return c.RenewSafety
	}
	return fallback
}

// DirectRunConfig configures a direct collector run.
type DirectRunConfig struct {
	Seeds            []string
	Handler          WorkHandler
	Storage          DirectStorage
	Identifier       Identifier
	MaxWorkItems     int
	MaxFetchAttempts int
	PerOriginRate    float64
	PerOriginBurst   int
	MaxConcurrency   int
	MaxAttempts      uint32
	RetryBackoff     time.Duration
}

// Validate checks direct run configuration.
func (c DirectRunConfig) Validate() error {
	if len(c.Seeds) == 0 {
		return fmt.Errorf("%w: seeds required", ErrInvalidConfig)
	}
	if c.Handler == nil {
		return fmt.Errorf("%w: handler", ErrInvalidConfig)
	}
	if c.Storage == nil {
		return fmt.Errorf("%w: storage", ErrInvalidConfig)
	}
	if c.MaxWorkItems <= 0 || c.MaxFetchAttempts <= 0 {
		return fmt.Errorf("%w: budgets", ErrInvalidConfig)
	}
	if c.MaxConcurrency <= 0 {
		return fmt.Errorf("%w: concurrency", ErrInvalidConfig)
	}
	if c.MaxAttempts == 0 {
		return fmt.Errorf("%w: max attempts", ErrInvalidConfig)
	}
	if c.RetryBackoff < 0 {
		return fmt.Errorf("%w: retry backoff", ErrInvalidConfig)
	}
	return nil
}

// FrontierRunConfig configures a durable frontier run.
type FrontierRunConfig struct {
	Frontier     Frontier
	Identifier   Identifier
	Handler      WorkHandler
	UntilDrained bool
	PollInterval time.Duration
}

// Validate checks frontier run configuration.
func (c FrontierRunConfig) Validate() error {
	if c.Frontier == nil {
		return fmt.Errorf("%w: frontier", ErrInvalidConfig)
	}
	if c.Identifier == nil {
		return fmt.Errorf("%w: identifier", ErrInvalidConfig)
	}
	if c.Handler == nil {
		return fmt.Errorf("%w: handler", ErrInvalidConfig)
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("%w: poll interval", ErrInvalidConfig)
	}
	return nil
}
