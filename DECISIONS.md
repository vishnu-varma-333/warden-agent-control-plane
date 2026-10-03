# Design decisions

Each entry: the options considered, what was chosen, why, and what it cost.
These double as interview talking points.

## 2026-10-03 — Monorepo, single Go module

**Options:** one Go module per service (`cmd/gateway`, `cmd/control-api` each
with their own `go.mod`, wired together via a `go.work` workspace) vs. a
single root module covering both binaries.

**Chose:** a single root module (`github.com/vishnu-varma-333/warden-agent-control-plane`)
with `cmd/gateway` and `cmd/control-api` as separate `main` packages, sharing
code from `internal/`.

**Why:** the gateway and control API are explicitly meant to share policy and
auth code. A single module makes that sharing free — no version pinning
between internal packages, one `go.mod` to keep tidy, one CI job to run. A
multi-module workspace only pays for itself once the services need
independent release cadences or diverge in dependency versions, neither of
which is true yet.

**Cost:** both binaries are forced onto the same Go version and the same
versions of shared third-party dependencies. If that becomes a real
constraint later (e.g. control-api needs a dependency upgrade the gateway
isn't ready for), this is the first thing to revisit — splitting a module
later is mechanical, so the decision isn't expensive to reverse.

## 2026-10-03 — Local dev via Docker Compose first, kind/k3s as a parallel track

**Options:** develop against a local Kubernetes cluster (kind/k3s) from day
one, as the original spec called for, vs. start with `docker-compose` for
dependencies and layer local Kubernetes manifests in alongside it.

**Chose:** `docker-compose` now (`deploy/docker/docker-compose.yml`) for
Postgres/Redis/Redpanda/OTel/Jaeger; `deploy/k8s-local/` is scaffolded but not
yet filled in, and is the next piece of milestone 1.

**Why:** the fastest useful iteration loop for writing gateway code right now
is `docker compose up` + `go run`. Kubernetes manifests for the same stack
are additive, not a replacement — they matter for later milestones
(approval kill-tests, rolling deploys) more than for today's "can I hit
Postgres from Go" loop. Building both at once would slow down getting the
first real code running.

**Cost:** there's a real gap between "works with docker-compose" and "works
on Kubernetes" (service discovery, config, resource limits) that will need
closing before the kill-test and rolling-deploy milestones. Tracked in
`docs/MILESTONES.md` under milestone 1 as still open.

**Update (same day):** closed the gap — `deploy/k8s-local/` holds plain
Kustomize manifests (no Helm yet; that's milestone 10) for the same five
services, applied to a real `kind` cluster and verified (all pods Ready,
PVCs bound) before tearing the cluster down. Two things changed going from
compose to Kubernetes: Redpanda's `--advertise-kafka-addr` has to be the
in-cluster DNS name (`redpanda.warden-local.svc.cluster.local`), not
`localhost`, since other pods resolve it through cluster DNS, not the docker
network; and Postgres's data volume is mounted with a `subPath` so the PVC's
root isn't handed to Postgres directly (it refuses to start if that
directory isn't empty on first init). `kind` clusters are disposable by
design — created, verified, deleted — same reasoning as the spec's "create
EKS only during benchmark runs" cost note, just applied locally.

## 2026-10-03 — OTel wiring: otelhttp middleware over manual spans

**Options:** hand-write middleware that starts/ends a span per request vs.
use `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`.

**Chose:** `otelhttp.NewHandler` wrapping the mux in both services
(`internal/telemetry` holds the shared TracerProvider setup; each binary's
`main.go` just calls `telemetry.Init(ctx, serviceName)`).

**Why:** this is the standard, maintained instrumentation path for Go's
`net/http` — it handles span naming, status codes, and context propagation
correctly, including edge cases (panics, hijacked connections) that a
hand-rolled version would get wrong first try. Writing it by hand would only
teach how *not* to do it.

**Cost:** one more third-party dependency in the hot path. Verified it
actually works end-to-end (not just "compiles"): ran the gateway against the
live collector, hit `/healthz` twice, queried Jaeger's API directly, and
confirmed two `gateway` spans arrived.

## 2026-10-03 — Mock provider now, real provider deferred

**Options:** wire a real provider (OpenAI/Anthropic/etc.) immediately for
milestone 2, vs. build and verify all the gateway logic (routing, fallback,
breaker, caching, streaming) against a mock first.

**Chose:** mock first (`internal/provider/mock`) — two instances
(`mock-primary`, `mock-secondary`) wired into one route. Real provider
adapters are a drop-in later: anything implementing `provider.Provider`
plugs into the same router.

**Why:** every behavior milestone 2 needs to prove — fallback, the circuit
breaker, exact-match caching, streaming — is about the gateway's own logic,
not about any specific vendor's API. A mock makes failure deterministic
(flip `SetUnhealthy(true)`, no need to actually take a real provider down)
and free to run repeatedly, including for the benchmark, where a real
provider's network latency would swamp the number actually being measured
(the gateway's own overhead).

**Cost:** nothing yet proves the real provider adapter's HTTP/SDK handling
works — that's new code still to write once an API key is supplied. The
`provider.Provider` interface is the seam designed to make that addition
not require touching the router, cache, or HTTP handler.

## 2026-10-03 — Streaming fallback only applies before the stream starts

**Decision:** `Router.ChatStream` tries providers in order but commits to
the first one whose *initial* call succeeds; it never switches providers
mid-stream.

**Why:** once bytes have been flushed to the HTTP client as SSE events,
there's no way to retract them — switching providers mid-stream would mean
either duplicating already-sent content or silently corrupting the
response. Real proxies (and the spec's own "streaming through a proxy"
talking point) hit this same constraint.

**Cost:** if a provider fails *after* starting to stream (not on the
initial call), the client currently just sees a truncated response with no
retry. Revisit this once the audit log (milestone 7) exists — mid-stream
failures need to be recorded either way, so that's a natural point to also
add a clear error-terminated SSE event instead of silent truncation.

## 2026-10-03 — "Acting as" via a hardcoded allowlist claim, not RFC 8693 token exchange

**Options:** implement real OAuth token exchange (RFC 8693) — where an
agent requests a fresh token scoped to exactly one user per call — vs. have
each agent's client-credentials token carry a fixed list of users it's
allowed to act for (via a Keycloak protocol mapper), and let the caller name
one per request via a header.

**Chose:** the hardcoded-allowlist version (`internal/identity`): the
token's `acting_as_allowed` claim is checked against an `X-Acting-As`
header on every call.

**Why:** token exchange is a genuinely more correct model (a token scoped
to one user can't be misused to claim a different one), but Keycloak's
token-exchange support needs its own admin-side feature configuration and a
second round-trip per call to actually request the exchanged token. The
allowlist version proves the exact requirement this milestone cares about —
"every call carries both the agent's identity and the user it acts for,
and that pairing is authorized, not just claimed" — without that setup cost.

**Cost:** the allowlist is static per-agent (configured once in Keycloak),
not dynamic per-call. If an agent's set of permitted users needs to change
at runtime without reissuing its client config, or if a true per-call
scoped token is required for compliance reasons, this is the first thing to
replace with real token exchange. Recorded here specifically so it's a
visible, deliberate gap — not a thing discovered during an interview
question about it.

## 2026-10-03 — Rate limit and budget both fail open on a Redis error

**Decision:** if the Redis call behind `ratelimit.Allow` or `budget.Charge`
errors (e.g. Redis is down), the request is allowed through anyway — the
error is logged, not enforced.

**Why:** this is the "fail-open vs. fail-closed" talking point the original
spec calls out, applied concretely: losing the rate limiter or budget
tracker is a cost-control and fairness problem, not a security boundary —
unlike the policy engine (milestone 5), where fail-open would mean
unauthorized actions proceeding. Taking down the entire gateway because
Redis hiccuped would trade a minor problem for a much bigger one.

**Cost:** during a Redis outage, limits and budgets are silently
unenforced rather than the gateway refusing traffic. Revisit this choice
specifically when the policy engine lands — that one should almost
certainly fail *closed*, and the contrast between the two is itself a good
interview answer about *where* fail-open is and isn't acceptable.

## 2026-10-03 — Official MCP Go SDK, composite natural key for the tool registry

**Options for the SDK:** the official `github.com/modelcontextprotocol/go-sdk`
vs. a community alternative (e.g. `mark3labs/mcp-go`), or hand-rolling the
JSON-RPC protocol.

**Chose:** the official SDK, per the original spec's tech stack table. It
provides both server (`mcp.NewServer`, `Server.AddTool`) and client
(`mcp.NewClient`, `ClientSession.CallTool`) sides plus the Streamable HTTP
transport, so Warden could be both an MCP server (to agents) and an MCP
client (to upstreams) with one dependency.

**Options for the registry key:** a generated UUID primary key (matching
the original data-model sketch's generic `id` field) vs. a composite
natural key of `(mcp_server, name)`.

**Chose:** the composite key (`migrations/0001_create_tools.up.sql`).

**Why:** a tool is only ever looked up by "which server, which tool name" —
every call site already has both. A surrogate UUID would add a layer of
indirection (look up the UUID, then use it) for no actual benefit here,
and the composite key is also exactly what enforces "one row per real
tool" as a database constraint instead of application logic.

**Cost:** if tools ever need to be renamed while preserving history, a
natural key makes that a bigger operation (the key itself changes) than it
would be with a surrogate key. Not a concern for v1.

## 2026-10-03 — Found via live testing: upstream session doesn't survive a restart

**What happened:** while verifying the tool-poisoning detection by
restarting the demo MCP server with a changed description, the gateway's
periodic sync failed with "session not found" — the previously-established
MCP client session died when its upstream process restarted (a new
process means a new SSE session on the wire), and nothing was reconnecting.

**Fix:** `mcpgateway.Gateway` now remembers each upstream's `UpstreamConfig`
and, on any `ListTools`/`CallTool` failure, attempts exactly one reconnect
before giving up — turning "upstream restarted" into a brief gap instead
of a permanent failure that needed a gateway restart to clear.

**Why this is worth a DECISIONS.md entry on its own:** it's a concrete,
true example of why milestone benchmarks/tests have to involve actually
*running* the thing, not just reading the code — this bug was invisible
in both the unit tests and a first read-through, and only showed up when
an upstream was deliberately restarted mid-session.
