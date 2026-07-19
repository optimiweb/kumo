package crawl

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"time"
)

// BodyView is an ephemeral bounded body reader.
type BodyView struct {
	mu     sync.Mutex
	data   []byte
	closed bool
}

func newBodyView(data []byte) BodyView {
	// Defensive copy so caller mutations of original do not affect the view.
	cp := append([]byte(nil), data...)
	return BodyView{data: cp}
}

// Len returns the body length.
func (b *BodyView) Len() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0
	}
	return int64(len(b.data))
}

// Reader returns a reader over the body. Invalid after invalidate.
func (b *BodyView) Reader() io.Reader {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return bytes.NewReader(nil)
	}
	return bytes.NewReader(append([]byte(nil), b.data...))
}

// BytesCopy returns a copy of body bytes. Prefer Reader for large bodies.
func (b *BodyView) BytesCopy() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	return append([]byte(nil), b.data...)
}

func (b *BodyView) invalidate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.data = nil
}

// HeaderView is an ephemeral, allowlisted header map. Like BodyView, it is
// invalidated after the handler returns so response data cannot outlive the
// fenced handler execution.
type HeaderView struct {
	state *headerState
}

type headerState struct {
	mu     sync.Mutex
	values map[string][]string
	closed bool
}

func newHeaderView(in map[string][]string) HeaderView {
	out := make(map[string][]string, len(in))
	for k, vs := range in {
		out[canonicalHeader(k)] = append([]string(nil), vs...)
	}
	return HeaderView{state: &headerState{values: out}}
}

// Values returns a copy of header values for name, or nil after invalidation.
func (h HeaderView) Values(name string) []string {
	if h.state == nil {
		return nil
	}
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.state.closed {
		return nil
	}
	return append([]string(nil), h.state.values[canonicalHeader(name)]...)
}

// Get returns the first header value.
func (h HeaderView) Get(name string) string {
	vs := h.Values(name)
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}

func (h HeaderView) invalidate() {
	if h.state == nil {
		return
	}
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.closed = true
	h.state.values = nil
}

func canonicalHeader(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// HTTPResponse is an immutable HTTP response view.
type HTTPResponse struct {
	status  int
	headers HeaderView
	body    *BodyView
}

// NewHTTPResponse constructs an immutable response view from transport data.
func NewHTTPResponse(status int, headers map[string][]string, body []byte) HTTPResponse {
	bv := newBodyView(body)
	return HTTPResponse{
		status:  status,
		headers: newHeaderView(headers),
		body:    &bv,
	}
}

// StatusCode returns the HTTP status.
func (r HTTPResponse) StatusCode() int { return r.status }

// Headers returns the allowlisted headers.
func (r HTTPResponse) Headers() HeaderView { return r.headers }

// Body returns the body view.
func (r HTTPResponse) Body() *BodyView { return r.body }

func (r HTTPResponse) invalidate() {
	if r.body != nil {
		r.body.invalidate()
	}
	r.headers.invalidate()
}

// RedirectHop is evidence for one redirect observation.
type RedirectHop struct {
	fromURL  string
	status   int
	location string
	decision DiscoveryState
	code     ErrorCode
	toID     WorkID
}

// NewRedirectHop constructs redirect evidence.
func NewRedirectHop(from string, status int, location string, decision DiscoveryState, code ErrorCode, toID WorkID) RedirectHop {
	return RedirectHop{
		fromURL:  from,
		status:   status,
		location: location,
		decision: decision,
		code:     code,
		toID:     toID,
	}
}

func (h RedirectHop) FromURL() string          { return h.fromURL }
func (h RedirectHop) Status() int              { return h.status }
func (h RedirectHop) Location() string         { return h.location }
func (h RedirectHop) Decision() DiscoveryState { return h.decision }
func (h RedirectHop) Code() ErrorCode          { return h.code }
func (h RedirectHop) ToID() WorkID             { return h.toID }

// FetchResult is the typed outcome of one work fetch pipeline.
type FetchResult struct {
	outcome      FetchOutcome
	code         ErrorCode
	response     HTTPResponse
	hasResponse  bool
	redirect     DiscoveryResult
	hasRedirect  bool
	redirectHop  RedirectHop
	hasHop       bool
	wireBytes    int64
	decodedBytes int64
	duration     time.Duration
	finalURL     string
}

// NewFetchResult constructs a fetch result without a response body.
func NewFetchResult(outcome FetchOutcome, code ErrorCode, finalURL string, duration time.Duration) FetchResult {
	if code == "" {
		code = outcome.ErrorCode()
	}
	return FetchResult{
		outcome:  outcome,
		code:     code,
		finalURL: finalURL,
		duration: duration,
	}
}

// NewHTTPFetchResult constructs a successful HTTP response result.
func NewHTTPFetchResult(resp HTTPResponse, finalURL string, wire, decoded int64, duration time.Duration) FetchResult {
	return FetchResult{
		outcome:      FetchOutcomeHTTPResponse,
		response:     resp,
		hasResponse:  true,
		wireBytes:    wire,
		decodedBytes: decoded,
		duration:     duration,
		finalURL:     finalURL,
	}
}

// WithRedirect attaches redirect discovery evidence.
func (r FetchResult) WithRedirect(d DiscoveryResult, hop RedirectHop) FetchResult {
	r.redirect = d
	r.hasRedirect = true
	r.redirectHop = hop
	r.hasHop = true
	return r
}

// WithFetchMetrics attaches physical transfer accounting to a result.
func (r FetchResult) WithFetchMetrics(wire, decoded int64) FetchResult {
	r.wireBytes = wire
	r.decodedBytes = decoded
	return r
}

func (r FetchResult) Outcome() FetchOutcome   { return r.outcome }
func (r FetchResult) ErrorCode() ErrorCode    { return r.code }
func (r FetchResult) FinalURL() string        { return r.finalURL }
func (r FetchResult) Duration() time.Duration { return r.duration }
func (r FetchResult) WireBytes() int64        { return r.wireBytes }
func (r FetchResult) DecodedBytes() int64     { return r.decodedBytes }

// Response returns the HTTP response when present.
func (r FetchResult) Response() (HTTPResponse, bool) {
	return r.response, r.hasResponse
}

// Redirect returns redirect discovery evidence when present.
func (r FetchResult) Redirect() (DiscoveryResult, bool) {
	return r.redirect, r.hasRedirect
}

// RedirectHop returns hop evidence when present.
func (r FetchResult) RedirectHop() (RedirectHop, bool) {
	return r.redirectHop, r.hasHop
}

// Invalidate releases ephemeral response views after handler execution.
func (r FetchResult) Invalidate() {
	if r.hasResponse {
		r.response.invalidate()
	}
}

// RunReport summarizes a completed run.
type RunReport struct {
	handled    int
	failed     int
	retried    int
	cancelled  int
	skipped    int
	fetched    int
	stopReason ErrorCode
	duration   time.Duration
}

// NewRunReport constructs a run report.
func NewRunReport(handled, failed, retried, cancelled, skipped, fetched int, stop ErrorCode, d time.Duration) RunReport {
	return RunReport{
		handled:    handled,
		failed:     failed,
		retried:    retried,
		cancelled:  cancelled,
		skipped:    skipped,
		fetched:    fetched,
		stopReason: stop,
		duration:   d,
	}
}

func (r RunReport) Handled() int            { return r.handled }
func (r RunReport) Failed() int             { return r.failed }
func (r RunReport) Retried() int            { return r.retried }
func (r RunReport) Cancelled() int          { return r.cancelled }
func (r RunReport) Skipped() int            { return r.skipped }
func (r RunReport) Fetched() int            { return r.fetched }
func (r RunReport) StopReason() ErrorCode   { return r.stopReason }
func (r RunReport) Duration() time.Duration { return r.duration }
