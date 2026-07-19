package engine

import (
	"context"
	"crypto/rand"
	"time"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/internal/httpx"
)

// Engine executes the crawl protocol against caller-supplied ports.
type Engine struct {
	cfg    crawl.CollectorConfig
	client *httpx.Client
}

// New constructs an engine with immutable collector configuration.
func New(cfg crawl.CollectorConfig, client *httpx.Client) *Engine {
	return &Engine{cfg: cfg, client: client}
}

func (e *Engine) bodyLimits(class crawl.ResourceClass) crawl.BodyLimits {
	if lim, ok := e.cfg.Bodies[class]; ok {
		return lim
	}
	return e.cfg.Bodies[crawl.ResourceHTML]
}

func (e *Engine) admit(raw string, method crawl.Method) crawl.ErrorCode {
	code, err := e.cfg.Policy.Admit(raw, method)
	if err != nil {
		return crawl.CodeAdapterFailure
	}
	return code
}

func newOperationID() (crawl.OperationID, error) {
	var id crawl.OperationID
	_, err := rand.Read(id[:])
	return id, err
}

func waitBackoff(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func classifyCtx(err error) (crawl.FetchOutcome, crawl.ErrorCode) {
	if err == nil {
		return crawl.FetchOutcomeUnspecified, crawl.CodeNone
	}
	if err == context.DeadlineExceeded {
		return crawl.FetchOutcomeTimedOut, crawl.CodeRequestTimeout
	}
	return crawl.FetchOutcomeCancelled, crawl.CodeCancelled
}

func requireDecision(d crawl.Decision) error { return d.Validate() }
