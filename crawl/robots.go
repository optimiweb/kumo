package crawl

import (
	"context"
	"crypto/sha256"
	"time"
)

// RobotsKeyFor builds a stable robots cache key.
func RobotsKeyFor(origin, ua, parserVersion string) RobotsKey {
	return sha256.Sum256([]byte(origin + "\n" + ua + "\n" + parserVersion))
}

// RobotsKey identifies a robots policy cache entry.
type RobotsKey [32]byte

// RobotsToken is an unguessable robots claim token.
type RobotsToken string

// RobotsAcquireState classifies robots acquisition.
type RobotsAcquireState uint8

const (
	RobotsAcquireUnspecified RobotsAcquireState = iota
	RobotsCached
	RobotsAcquired
	RobotsBusy
)

// RobotsLease is exclusive ownership while fetching robots.
type RobotsLease struct {
	key            RobotsKey
	token          RobotsToken
	generation     uint64
	leaseExpiresAt time.Time
	renewAfter     time.Duration
}

// NewRobotsLease constructs a robots lease.
func NewRobotsLease(key RobotsKey, token RobotsToken, generation uint64, expiresAt time.Time, renewAfter time.Duration) RobotsLease {
	return RobotsLease{
		key:            key,
		token:          token,
		generation:     generation,
		leaseExpiresAt: expiresAt,
		renewAfter:     renewAfter,
	}
}

func (l RobotsLease) Key() RobotsKey            { return l.key }
func (l RobotsLease) Token() RobotsToken        { return l.token }
func (l RobotsLease) Generation() uint64        { return l.generation }
func (l RobotsLease) LeaseExpiresAt() time.Time { return l.leaseExpiresAt }
func (l RobotsLease) RenewAfter() time.Duration { return l.renewAfter }

// RobotsRule is a minimized, serializable path rule derived from robots.txt.
type RobotsRule struct {
	Path    string
	Pattern string
	Allow   bool
}

// RobotsRecord is a bounded derived robots policy, never a raw body.
type RobotsRecord struct {
	rules       []RobotsRule
	crawlDelay  time.Duration
	sitemaps    []string
	fetchedAt   time.Time
	ttl         time.Duration
	unavailable bool
	allowAll    bool
	denyAll     bool
}

// NewRobotsRecord constructs a derived robots record.
func NewRobotsRecord(
	rules []RobotsRule,
	crawlDelay time.Duration,
	sitemaps []string,
	fetchedAt time.Time,
	ttl time.Duration,
	unavailable, allowAll, denyAll bool,
) RobotsRecord {
	return RobotsRecord{
		rules:       append([]RobotsRule(nil), rules...),
		crawlDelay:  crawlDelay,
		sitemaps:    append([]string(nil), sitemaps...),
		fetchedAt:   fetchedAt,
		ttl:         ttl,
		unavailable: unavailable,
		allowAll:    allowAll,
		denyAll:     denyAll,
	}
}

// Rules returns a copy of the derived path rules.
func (r RobotsRecord) Rules() []RobotsRule       { return append([]RobotsRule(nil), r.rules...) }
func (r RobotsRecord) CrawlDelay() time.Duration { return r.crawlDelay }
func (r RobotsRecord) Sitemaps() []string        { return append([]string(nil), r.sitemaps...) }
func (r RobotsRecord) FetchedAt() time.Time      { return r.fetchedAt }
func (r RobotsRecord) TTL() time.Duration        { return r.ttl }
func (r RobotsRecord) Unavailable() bool         { return r.unavailable }
func (r RobotsRecord) AllowAll() bool            { return r.allowAll }
func (r RobotsRecord) DenyAll() bool             { return r.denyAll }

// Expired reports whether the record is past its TTL at now.
func (r RobotsRecord) Expired(now time.Time) bool {
	if r.ttl <= 0 {
		return true
	}
	return now.After(r.fetchedAt.Add(r.ttl))
}

// RobotsExplanation provides structured explanation for a path match.
type RobotsExplanation struct {
	Allowed     bool
	MatchedRule *RobotsRule
	Reason      string
}

// ExplainMatch evaluates a path against the record's rules.
func (r RobotsRecord) ExplainMatch(path string) RobotsExplanation {
	if r.allowAll {
		return RobotsExplanation{Allowed: true, Reason: "allow all"}
	}
	if r.denyAll {
		return RobotsExplanation{Allowed: false, Reason: "deny all"}
	}
	if r.unavailable {
		return RobotsExplanation{Allowed: false, Reason: "robots unavailable (fail closed)"}
	}
	rules := r.Rules()
	var prefixLen int
	var matched *RobotsRule
	allow := true
	for i := range rules {
		rl := &rules[i]
		if rl.Pattern != "" {
			if len(rl.Pattern) > prefixLen {
				prefixLen = len(rl.Pattern)
				allow = rl.Allow
				matched = rl
			}
		} else if rl.Path == "/" && prefixLen == 0 {
			prefixLen = 1
			allow = rl.Allow
			matched = rl
		} else if len(rl.Path) > prefixLen {
			prefixLen = len(rl.Path)
			allow = rl.Allow
			matched = rl
		}
	}
	if matched == nil {
		return RobotsExplanation{Allowed: true, Reason: "default allow (no matching disallow directive)"}
	}
	ruleType := "Disallow"
	if matched.Allow {
		ruleType = "Allow"
	}
	reason := ruleType + ": " + matched.Path
	if matched.Pattern != "" {
		reason = ruleType + " (pattern): " + matched.Pattern
	}
	return RobotsExplanation{Allowed: allow, MatchedRule: matched, Reason: reason}
}

// AcquireRobotsRequest acquires robots cache ownership.
type AcquireRobotsRequest struct {
	OperationID   OperationID
	Key           RobotsKey
	Origin        string
	LeaseDuration time.Duration
}

// AcquireRobotsResult is returned by AcquireRobots.
type AcquireRobotsResult struct {
	State      RobotsAcquireState
	Lease      RobotsLease
	Record     RobotsRecord
	RetryAfter time.Duration
}

// RobotsCoordinator coordinates distributed robots fetch ownership.
type RobotsCoordinator interface {
	AcquireRobots(ctx context.Context, lease Lease, req AcquireRobotsRequest) (AcquireRobotsResult, error)
	RenewRobots(ctx context.Context, lease Lease, op OperationID, robotsLease RobotsLease, duration time.Duration) (RobotsLease, error)
	PublishRobots(ctx context.Context, lease Lease, op OperationID, robotsLease RobotsLease, record RobotsRecord, ttl time.Duration) error
	ReleaseRobots(ctx context.Context, lease Lease, op OperationID, robotsLease RobotsLease) error
}
