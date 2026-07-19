# Kumo

Kumo is a fail-closed Go library for production web crawlers.

It handles safe HTTP fetching, work lifecycle (claim, lease, settle), robots.txt,
and redirect handling. Your app supplies handlers, host policy, and optionally a
durable queue adapter for distributed crawls.

```bash
go get github.com/optimiweb/kumo
```

## Features

- **Typed handlers** — immutable input, explicit `Ack` / `Retry` / `Fail`
- **Two run modes**
  - `RunDirect` — bounded local crawl
  - `RunFrontier` — queue-backed crawl (in-memory or your own adapter)
- **Controlled egress** — GET/HEAD only, no proxies or cookies, private IPs denied
- **Robots by default** — fetched and enforced through the same pipeline
- **Manual redirects** — each hop is independent claimed work
- **Bounded bodies** — wire and decoded size limits; no silent truncation
- **In-memory adapters included** — no database driver required to start

## Usage

### Direct mode

Best for one-shot or small bounded crawls:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/optimiweb/kumo"
	"github.com/optimiweb/kumo/memory"
)

func main() {
	cfg := kumo.DefaultCollectorConfig()
	cfg.Policy = kumo.HostPolicy("example.com")

	c, err := kumo.NewCollector(cfg)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report, err := c.RunDirect(ctx, kumo.DirectRunConfig{
		Seeds:            []string{"https://example.com/"},
		Storage:          memory.NewMemoryStorage(nil),
		Identifier:       kumo.IdentityFunc(kumo.DefaultIdentity),
		MaxWorkItems:     50,
		MaxFetchAttempts: 100,
		MaxConcurrency:   4,
		MaxAttempts:      2,
		Handler: kumo.HandlerFunc(func(ctx context.Context, in kumo.HandleInput, sink kumo.DiscoverySink) kumo.Decision {
			res := in.Result()
			fmt.Println(in.Lease().Work().URL(), res.Outcome())
			if res.Outcome() != kumo.FetchOutcomeHTTPResponse {
				return kumo.Fail(res.ErrorCode())
			}
			// Discover more URLs with sink.Submit(...)
			return kumo.Ack()
		}),
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("handled=%d failed=%d stop=%s\n",
		report.Handled(), report.Failed(), report.StopReason())
}
```

### Frontier mode

Use when you want an explicit queue (in-memory here; swap in your own
`Frontier` for durable/distributed crawls):

```go
cfg := kumo.DefaultCollectorConfig()
cfg.Policy = kumo.HostPolicy("example.com")
c, err := kumo.NewCollector(cfg)
if err != nil {
	panic(err)
}

fr := memory.NewMemoryFrontier(memory.MemoryFrontierOptions{
	MaxPages: 100, FetchBudget: 200, MaxAttempts: 3, MaxOriginConc: 4,
})
id := kumo.IdentityFunc(kumo.DefaultIdentity)

seed, err := id(ctx, kumo.IdentityRequest{
	RawURL: "https://example.com/",
	Method: kumo.MethodGET,
	Source: kumo.SourceSeed,
})
if err != nil || seed.State != kumo.IdentityAccepted {
	panic(err)
}
if _, err := fr.EnqueueSeed(ctx, kumo.EnqueueRequest{
	Identity: seed.Identity, Method: kumo.MethodGET,
	Source: kumo.SourceSeed, ResourceClass: kumo.ResourceHTML,
}); err != nil {
	panic(err)
}
if err := fr.SealSeeds(ctx); err != nil {
	panic(err)
}

report, err := c.RunFrontier(ctx, kumo.FrontierRunConfig{
	Frontier:     fr,
	Identifier:   id,
	Handler:      handler,
	UntilDrained: true,
	PollInterval: 50 * time.Millisecond,
})
```

See `examples/direct` and `examples/frontier-memory` for full programs.

### Handlers

Handlers receive one finished fetch and return a decision:

| Decision | Meaning |
|---|---|
| `kumo.Ack()` | Success — settle the work |
| `kumo.Retry(after, code)` | Transient failure — schedule retry |
| `kumo.Fail(code)` | Terminal failure |
| `kumo.DefaultDecision(res)` | Map common fetch outcomes automatically |

Submit related URLs during the handler with `sink.Submit`. Kumo does not
auto-extract HTML links; discovery is application-defined.

### CLI

Sitemap-oriented domain crawl (in-memory):

```bash
go run ./cmd/kumo example.com
go run ./cmd/kumo -workers 8 -max-pages 1000 example.com
```

## Architecture

```text
your app / cmd/kumo / examples
        │
        ▼
      kumo                 public facade (NewCollector, RunDirect, RunFrontier)
        │
        ├──► memory        in-memory Frontier + DirectStorage
        │
        ▼
 internal/engine ──────► crawl     contracts (work, handlers, ports, config)
        │
        ▼
 internal/httpx                    controlled single-hop HTTP
```

| Package | Role |
|---|---|
| `kumo` | Facade and defaults |
| `crawl` | Stable protocol: work, leases, handlers, `Frontier`, policy, results |
| `memory` | Reference in-memory adapters |
| `internal/engine` | Workers, fetch pipeline, robots, redirects, settlement |
| `internal/httpx` | Safe dial, address policy, bounded bodies |
| `crawltest` | Adapter conformance helpers |
| `pkg/*` | Standalone helpers (URL, robots, sitemap, HTML query, …) |

**Library vs app responsibilities**

| Kumo owns | Your app owns |
|---|---|
| Safe fetch pipeline | Durable storage / queue (optional) |
| Work claim / lease / settle | Host and product policy beyond defaults |
| Robots + redirect orchestration | Link/sitemap discovery logic |
| In-memory adapters | Evidence, inventory, multi-tenant auth |

Implement `crawl.Frontier` (and related ports) to back the crawler with Postgres,
Redis, or any other store. `memory` is the reference implementation.

## Safety defaults

| Concern | Default |
|---|---|
| Methods | GET, HEAD |
| Schemes | http, https |
| Ports | 80, 443 |
| Robots | on |
| Redirects | not auto-followed |
| Cookies / cache / proxies | off |
| Private / loopback IPs | denied |

Always set a host policy for real targets (`kumo.HostPolicy("example.com")`).

## Development

```bash
make check              # fmt, vet, modules, tests, build
make test-integration   # HTTP fixture crawls (-race)
make coverage-html
make help
```

Integration tests under `test/integration` crawl an embedded mock of
`example.com` (`test/fixtures/example.com`).

## License

[MIT](LICENSE)
