package engine

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/optimiweb/kumo/crawl"
	"github.com/optimiweb/kumo/internal/httpx"
	"github.com/optimiweb/kumo/pkg/robotspolicy"
)

// processWork runs the shared safe fetch pipeline for one leased work item.
// The handler is invoked for every typed outcome — including policy denial,
// robots outcomes, reservation failures, and transport errors — so the
// embedding application can persist evidence before Kumo transitions the work.
func (c *Engine) processWork(
	ctx context.Context,
	lease crawl.Lease,
	frontier crawl.Frontier,
	identifier crawl.Identifier,
	handler crawl.WorkHandler,
) (crawl.Decision, crawl.FetchResult) {
	work := lease.Work()
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stopRenew := c.renewLoop(workCtx, cancel, lease.RenewAfter(), func(rctx context.Context) error {
		op, err := newOperationID()
		if err != nil {
			return err
		}
		_, err = frontier.Renew(rctx, crawl.RenewLeaseRequest{
			OperationID:   op,
			Lease:         lease,
			LeaseDuration: c.cfg.LeaseDuration,
		})
		return err
	})
	defer stopRenew()

	result := c.execute(workCtx, cancel, lease, frontier)

	// Redirect discovery before handler.
	var redirectRes crawl.DiscoveryResult
	var hasRedirect bool
	var hop crawl.RedirectHop
	if resp, ok := result.Response(); ok && resp.StatusCode() >= 300 && resp.StatusCode() < 400 {
		loc := resp.Headers().Get("location")
		redirectRes, hop = c.handleRedirect(workCtx, frontier, identifier, lease, work, resp.StatusCode(), loc)
		hasRedirect = true
		result = result.WithRedirect(redirectRes, hop)
	}

	sink := newDiscoverySink(c.cfg.MaxDiscoveries, func(ctx context.Context, d crawl.Discovery) (crawl.DiscoveryResult, error) {
		return c.submitDiscovery(ctx, frontier, identifier, lease, work, d)
	})
	defer sink.close()

	decision := handler.Handle(workCtx, crawl.NewHandleInput(lease, result, redirectRes, hasRedirect), sink)
	result.Invalidate()

	if sink.hasUnresolved() {
		return crawl.Retry(c.cfg.RetryBackoffOr(time.Second), crawl.CodeDiscoveryUnresolved), result
	}
	if err := requireDecision(decision); err != nil {
		return crawl.Fail(crawl.CodeInvalidDecision), result
	}
	if decision.Kind() == crawl.DecisionAck && workCtx.Err() != nil {
		// Never acknowledge when the lease or run context died mid-handler.
		out, ec := classifyCtx(workCtx.Err())
		return crawl.Retry(c.cfg.RetryBackoffOr(time.Second), ec), crawl.NewFetchResult(out, ec, work.URL(), result.Duration())
	}
	return decision, result
}

// execute runs admission, robots, reservation, and the physical fetch. It
// only produces a FetchResult; the handler owns the transition decision.
func (c *Engine) execute(ctx context.Context, cancel context.CancelFunc, lease crawl.Lease, frontier crawl.Frontier) crawl.FetchResult {
	work := lease.Work()

	if code := c.admit(work.URL(), work.Method()); code != crawl.CodeNone {
		return crawl.NewFetchResult(crawl.FetchOutcomePolicyDenied, code, work.URL(), 0)
	}

	var crawlDelay time.Duration
	if c.cfg.Robots.Enabled && !c.cfg.Robots.Override {
		rec, code, err := c.ensureRobots(ctx, cancel, lease, frontier, work.URL())
		if err != nil {
			out, ec := classifyCtx(err)
			return crawl.NewFetchResult(out, ec, work.URL(), 0)
		}
		switch code {
		case crawl.CodeNone:
		case crawl.CodeRobotsUnavailable:
			return crawl.NewFetchResult(crawl.FetchOutcomeRobotsUnavailable, code, work.URL(), 0)
		default:
			return crawl.NewFetchResult(crawl.FetchOutcomeRobotsDenied, code, work.URL(), 0)
		}
		if !robotsAllows(rec, work.URL()) {
			return crawl.NewFetchResult(crawl.FetchOutcomeRobotsDenied, crawl.CodeRobotsDenied, work.URL(), 0)
		}
		crawlDelay = rec.CrawlDelay()
	}

	res, code, err := c.reserveFetch(ctx, frontier, lease, work.URL(), work.Method(), work.ResourceClass(), crawlDelay, crawl.FetchPurposeWork, 0)
	if err != nil {
		out, ec := classifyCtx(err)
		return crawl.NewFetchResult(out, ec, work.URL(), 0)
	}
	if code != crawl.CodeNone {
		out := crawl.FetchOutcomePolicyDenied
		if code == crawl.CodeBudgetExhausted {
			out = crawl.FetchOutcomeBudgetExhausted
		}
		return crawl.NewFetchResult(out, code, work.URL(), 0)
	}

	lim := c.bodyLimits(work.ResourceClass())
	httpResp := c.doFetchWithRenewal(ctx, cancel, frontier, lease, res, work.URL(), work.Method(), lim)
	return fetchResultFromHTTP(httpResp, work.URL())
}

// doFetchWithRenewal executes one reserved request while keeping the
// reservation alive for its full duration, then settles it idempotently.
// A short permit must never free capacity while a longer request is active.
func (c *Engine) doFetchWithRenewal(
	ctx context.Context,
	cancel context.CancelFunc,
	frontier crawl.Frontier,
	lease crawl.Lease,
	res crawl.FetchReservation,
	rawURL string,
	method crawl.Method,
	lim crawl.BodyLimits,
) httpx.Response {
	stopFetchRenew := c.renewLoop(ctx, cancel, res.RenewAfter(), func(rctx context.Context) error {
		op, err := newOperationID()
		if err != nil {
			return err
		}
		_, err = frontier.RenewFetch(rctx, lease, crawl.RenewFetchRequest{
			OperationID:   op,
			Reservation:   res,
			LeaseDuration: c.cfg.FetchLease,
		})
		return err
	})
	defer stopFetchRenew()

	httpResp := c.client.Do(ctx, httpx.Request{URL: rawURL, Method: string(method)}, lim.WireBytes, lim.DecodedBytes)
	out, _ := mapHTTPCode(httpResp.Code)
	_ = frontier.FinishFetch(context.WithoutCancel(ctx), lease, crawl.FinishFetchRequest{
		OperationID: res.ID(),
		Reservation: res,
		Report: crawl.FetchReport{
			Outcome:      outcomeOrHTTP(out),
			StatusClass:  uint8(httpResp.StatusCode / 100),
			Duration:     httpResp.Duration,
			WireBytes:    httpResp.WireBytes,
			DecodedBytes: httpResp.DecodedBytes,
		},
	})
	return httpResp
}

func outcomeOrHTTP(o crawl.FetchOutcome) crawl.FetchOutcome {
	if o == crawl.FetchOutcomeUnspecified {
		return crawl.FetchOutcomeHTTPResponse
	}
	return o
}

// renewLoop runs renew at interval until the context ends, the returned stop
// function is called, or a renewal fails — in which case cancel aborts the
// owning operation so no work continues without a valid lease.
func (c *Engine) renewLoop(ctx context.Context, cancel context.CancelFunc, interval time.Duration, renew func(context.Context) error) func() {
	if interval <= 0 {
		interval = time.Second
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				if err := renew(ctx); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

func (c *Engine) reserveFetch(
	ctx context.Context,
	frontier crawl.Frontier,
	lease crawl.Lease,
	rawURL string,
	method crawl.Method,
	class crawl.ResourceClass,
	minDelay time.Duration,
	purpose crawl.FetchPurpose,
	redirectNum uint16,
) (crawl.FetchReservation, crawl.ErrorCode, error) {
	for {
		if err := ctx.Err(); err != nil {
			return crawl.FetchReservation{}, crawl.CodeCancelled, err
		}
		op, err := newOperationID()
		if err != nil {
			return crawl.FetchReservation{}, crawl.CodeAdapterFailure, err
		}
		res, err := frontier.ReserveFetch(ctx, lease, crawl.ReserveFetchRequest{
			OperationID: op,
			Intent: crawl.FetchIntent{
				URL:            rawURL,
				Method:         method,
				Purpose:        purpose,
				ResourceClass:  class,
				MinimumDelay:   minDelay,
				RedirectNumber: redirectNum,
			},
			LeaseDuration: c.cfg.FetchLease,
		})
		if err != nil {
			return crawl.FetchReservation{}, crawl.CodeAdapterFailure, err
		}
		switch res.State {
		case crawl.FetchReserved:
			return res.Reservation, crawl.CodeNone, nil
		case crawl.FetchDeferred:
			if err := waitBackoff(ctx, res.RetryAfter); err != nil {
				return crawl.FetchReservation{}, crawl.CodeCancelled, err
			}
			continue
		case crawl.FetchBudgetExhausted:
			return crawl.FetchReservation{}, crawl.CodeBudgetExhausted, nil
		default:
			code := res.Code
			if code == "" {
				code = crawl.CodeReservationDenied
			}
			return crawl.FetchReservation{}, code, nil
		}
	}
}

func (c *Engine) ensureRobots(ctx context.Context, cancel context.CancelFunc, lease crawl.Lease, frontier crawl.Frontier, pageURL string) (crawl.RobotsRecord, crawl.ErrorCode, error) {
	origin, err := crawl.OriginKey(pageURL)
	if err != nil {
		return crawl.RobotsRecord{}, crawl.CodePolicyDenied, nil
	}
	key := crawl.RobotsKeyFor(origin, c.cfg.Robots.UserAgentToken, "robots-v1")
	for {
		if err := ctx.Err(); err != nil {
			return crawl.RobotsRecord{}, crawl.CodeCancelled, err
		}
		op, err := newOperationID()
		if err != nil {
			return crawl.RobotsRecord{}, crawl.CodeAdapterFailure, err
		}
		acq, err := frontier.AcquireRobots(ctx, lease, crawl.AcquireRobotsRequest{
			OperationID:   op,
			Key:           key,
			Origin:        origin,
			LeaseDuration: c.cfg.RobotsLease,
		})
		if err != nil {
			return crawl.RobotsRecord{}, crawl.CodeAdapterFailure, err
		}
		switch acq.State {
		case crawl.RobotsCached:
			if acq.Record.Unavailable() {
				return crawl.RobotsRecord{}, crawl.CodeRobotsUnavailable, nil
			}
			return acq.Record, crawl.CodeNone, nil
		case crawl.RobotsBusy:
			if err := waitBackoff(ctx, acq.RetryAfter); err != nil {
				return crawl.RobotsRecord{}, crawl.CodeCancelled, err
			}
			continue
		case crawl.RobotsAcquired:
			// Keep robots ownership alive while fetching; another worker must
			// not acquire the origin policy mid-fetch.
			stopRobotsRenew := c.renewLoop(ctx, cancel, acq.Lease.RenewAfter(), func(rctx context.Context) error {
				rop, err := newOperationID()
				if err != nil {
					return err
				}
				_, err = frontier.RenewRobots(rctx, lease, rop, acq.Lease, c.cfg.RobotsLease)
				return err
			})
			rec, code := c.fetchRobots(ctx, cancel, frontier, lease, origin)
			stopRobotsRenew()
			pubOp, err := newOperationID()
			if err != nil {
				_ = frontier.ReleaseRobots(context.WithoutCancel(ctx), lease, op, acq.Lease)
				return crawl.RobotsRecord{}, crawl.CodeAdapterFailure, err
			}
			ttl := c.cfg.Robots.TTL
			if rec.Unavailable() {
				ttl = c.cfg.Robots.FailureTTL
			}
			if err := frontier.PublishRobots(ctx, lease, pubOp, acq.Lease, rec, ttl); err != nil {
				_ = frontier.ReleaseRobots(context.WithoutCancel(ctx), lease, op, acq.Lease)
				return crawl.RobotsRecord{}, crawl.CodeAdapterFailure, err
			}
			if code != crawl.CodeNone {
				return crawl.RobotsRecord{}, code, nil
			}
			if rec.Unavailable() {
				return crawl.RobotsRecord{}, crawl.CodeRobotsUnavailable, nil
			}
			return rec, crawl.CodeNone, nil
		default:
			return crawl.RobotsRecord{}, crawl.CodeAdapterFailure, nil
		}
	}
}

// unavailableRobotsRecord builds a transient-failure record with the failure TTL.
func (c *Engine) unavailableRobotsRecord() crawl.RobotsRecord {
	return crawl.NewRobotsRecord(nil, 0, nil, time.Now(), c.cfg.Robots.FailureTTL, true, false, false)
}

func (c *Engine) fetchRobots(ctx context.Context, cancel context.CancelFunc, frontier crawl.Frontier, lease crawl.Lease, origin string) (crawl.RobotsRecord, crawl.ErrorCode) {
	robotsURL := strings.TrimRight(origin, "/") + "/robots.txt"
	res, code, err := c.reserveFetch(ctx, frontier, lease, robotsURL, crawl.MethodGET, crawl.ResourceRobots, 0, crawl.FetchPurposeRobots, 0)
	if err != nil || code != crawl.CodeNone {
		return c.unavailableRobotsRecord(), crawl.CodeRobotsUnavailable
	}
	lim := c.bodyLimits(crawl.ResourceRobots)
	httpResp := c.doFetchWithRenewal(ctx, cancel, frontier, lease, res, robotsURL, crawl.MethodGET, lim)
	if httpResp.Code != "" {
		return c.unavailableRobotsRecord(), crawl.CodeRobotsUnavailable
	}
	// Follow a small number of robots redirects manually.
	hops := 0
	finalURL := robotsURL
	for httpResp.StatusCode >= 300 && httpResp.StatusCode < 400 && hops < int(c.cfg.Robots.RedirectLimit) {
		loc := ""
		if vs := httpResp.Headers["location"]; len(vs) > 0 {
			loc = vs[0]
		}
		next, err := resolveURL(finalURL, loc)
		if err != nil {
			return c.unavailableRobotsRecord(), crawl.CodeRobotsUnavailable
		}
		if code := c.admit(next, crawl.MethodGET); code != crawl.CodeNone {
			return c.unavailableRobotsRecord(), crawl.CodeRobotsUnavailable
		}
		r2, code, err := c.reserveFetch(ctx, frontier, lease, next, crawl.MethodGET, crawl.ResourceRobots, 0, crawl.FetchPurposeRobotsRedirect, uint16(hops+1))
		if err != nil || code != crawl.CodeNone {
			return c.unavailableRobotsRecord(), crawl.CodeRobotsUnavailable
		}
		httpResp = c.doFetchWithRenewal(ctx, cancel, frontier, lease, r2, next, crawl.MethodGET, lim)
		if httpResp.Code != "" {
			return c.unavailableRobotsRecord(), crawl.CodeRobotsUnavailable
		}
		finalURL = next
		hops++
	}

	now := time.Now()
	status := httpResp.StatusCode
	body := httpResp.Body
	switch {
	case status >= 200 && status < 300:
		data, err := robotspolicy.FromBytes(body)
		if err != nil {
			return crawl.NewRobotsRecord(nil, 0, nil, now, c.cfg.Robots.TTL, false, false, true), crawl.CodeRobotsInvalid
		}
		token := c.cfg.Robots.UserAgentToken
		var delay time.Duration
		if g := data.FindGroup(token); g != nil {
			delay = g.CrawlDelay
		}
		// Store only the derived rule list, never the raw body.
		derived := data.Rules(token)
		rules := make([]crawl.RobotsRule, len(derived))
		for i, r := range derived {
			rules[i] = crawl.RobotsRule{Path: r.Path, Pattern: r.Pattern, Allow: r.Allow}
		}
		return crawl.NewRobotsRecord(rules, delay, data.Sitemaps, now, c.cfg.Robots.TTL, false, false, false), crawl.CodeNone
	case status == 401 || status == 403:
		return crawl.NewRobotsRecord(nil, 0, nil, now, c.cfg.Robots.TTL, false, false, true), crawl.CodeNone
	case status >= 400 && status < 500:
		return crawl.NewRobotsRecord(nil, 0, nil, now, c.cfg.Robots.TTL, false, true, false), crawl.CodeNone
	default:
		return crawl.NewRobotsRecord(nil, 0, nil, now, c.cfg.Robots.FailureTTL, true, false, false), crawl.CodeRobotsUnavailable
	}
}

// robotsAllows evaluates a page URL against the derived policy.
func robotsAllows(rec crawl.RobotsRecord, pageURL string) bool {
	if rec.AllowAll() {
		return true
	}
	if rec.DenyAll() || rec.Unavailable() {
		return false
	}
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	rules := rec.Rules()
	derived := make([]robotspolicy.Rule, len(rules))
	for i, r := range rules {
		derived[i] = robotspolicy.Rule{Path: r.Path, Pattern: r.Pattern, Allow: r.Allow}
	}
	return robotspolicy.TestRules(derived, path)
}

func (c *Engine) handleRedirect(
	ctx context.Context,
	frontier crawl.Frontier,
	identifier crawl.Identifier,
	lease crawl.Lease,
	work crawl.Work,
	status int,
	location string,
) (crawl.DiscoveryResult, crawl.RedirectHop) {
	if location == "" {
		hop := crawl.NewRedirectHop(work.URL(), status, location, crawl.DiscoveryRejected, crawl.CodeRedirectInvalid, "")
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: crawl.CodeRedirectInvalid}, hop
	}
	if work.RedirectHops() >= c.cfg.MaxRedirectHops {
		hop := crawl.NewRedirectHop(work.URL(), status, location, crawl.DiscoveryRejected, crawl.CodeRedirectLimit, "")
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: crawl.CodeRedirectLimit}, hop
	}
	next, err := resolveURL(work.URL(), location)
	if err != nil {
		hop := crawl.NewRedirectHop(work.URL(), status, location, crawl.DiscoveryRejected, crawl.CodeRedirectInvalid, "")
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: crawl.CodeRedirectInvalid}, hop
	}
	res, err := c.submitDiscovery(ctx, frontier, identifier, lease, work, crawl.Discovery{
		URL:      next,
		Method:   work.Method(),
		Relation: crawl.RelationRedirect,
		Priority: work.Priority(),
	})
	if err != nil {
		hop := crawl.NewRedirectHop(work.URL(), status, location, crawl.DiscoveryRejected, crawl.CodeAdapterFailure, "")
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: crawl.CodeAdapterFailure}, hop
	}
	hop := crawl.NewRedirectHop(work.URL(), status, location, res.State, res.Code, res.ID)
	return res, hop
}

func (c *Engine) submitDiscovery(
	ctx context.Context,
	frontier crawl.Frontier,
	identifier crawl.Identifier,
	lease crawl.Lease,
	parent crawl.Work,
	d crawl.Discovery,
) (crawl.DiscoveryResult, error) {
	method := d.Method
	if method == "" {
		method = crawl.MethodGET
	}
	if code := c.admit(d.URL, method); code != crawl.CodeNone {
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: code}, nil
	}
	if parent.Depth()+1 > c.cfg.MaxDepth {
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: crawl.CodeBudgetExhausted}, nil
	}
	idRes, err := identifier.Identify(ctx, crawl.IdentityRequest{
		RawURL:   d.URL,
		Method:   method,
		ParentID: parent.ID(),
		Source:   sourceFromRelation(d.Relation),
		Depth:    parent.Depth() + 1,
	})
	if err != nil {
		return crawl.DiscoveryResult{}, err
	}
	if idRes.State != crawl.IdentityAccepted {
		code := idRes.Code
		if code == "" {
			code = crawl.CodeIdentityRejected
		}
		return crawl.DiscoveryResult{State: crawl.DiscoveryRejected, Code: code}, nil
	}
	hops := parent.RedirectHops()
	if d.Relation == crawl.RelationRedirect {
		hops++
	}
	class := d.ResourceClass
	if class == crawl.ResourceUnspecified {
		// Pages from a urlset are the common sitemap discovery; robots
		// declarations point at XML sitemaps.
		class = crawl.ResourceHTML
		if d.Relation == crawl.RelationRobotsSitemap {
			class = crawl.ResourceXMLSitemap
		}
	}
	enq, err := frontier.EnqueueDiscovered(ctx, lease, crawl.EnqueueRequest{
		Identity:      idRes.Identity,
		Method:        method,
		Depth:         parent.Depth() + 1,
		RedirectHops:  hops,
		ParentID:      parent.ID(),
		Source:        sourceFromRelation(d.Relation),
		Priority:      d.Priority,
		ResourceClass: class,
	})
	if err != nil {
		if err == crawl.ErrBudgetExhausted {
			return crawl.DiscoveryResult{State: crawl.DiscoveryLimitReached, Code: crawl.CodeBudgetExhausted}, nil
		}
		return crawl.DiscoveryResult{}, err
	}
	state := crawl.DiscoveryDuplicate
	if enq.Inserted {
		state = crawl.DiscoveryInserted
	}
	return crawl.DiscoveryResult{State: state, ID: enq.ID}, nil
}

func sourceFromRelation(r crawl.DiscoveryRelation) crawl.SourceCode {
	switch r {
	case crawl.RelationRedirect:
		return crawl.SourceRedirect
	case crawl.RelationSitemap, crawl.RelationRobotsSitemap:
		return crawl.SourceSitemap
	case crawl.RelationCanonical:
		return crawl.SourceCanonical
	case crawl.RelationHreflang:
		return crawl.SourceHreflang
	default:
		return crawl.SourceLink
	}
}

func resolveURL(baseRaw, ref string) (string, error) {
	base, err := url.Parse(baseRaw)
	if err != nil {
		return "", err
	}
	rel, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(rel)
	return crawl.CanonicalFetchURL(resolved.String())
}
