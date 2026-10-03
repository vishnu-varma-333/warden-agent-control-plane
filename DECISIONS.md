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

## 2026-10-03 — Jaeger instead of Tempo, for local dev specifically

**Spec says:** Prometheus, Grafana, OpenTelemetry, **Tempo**.

**Built instead:** Jaeger, for trace storage/viewing in local dev
(`deploy/docker/docker-compose.yml`, `deploy/k8s-local/observability.yaml`).

**Why:** same reasoning as Redpanda-for-Kafka (see the earlier entry) —
Warden's own code only ever speaks OTLP to the collector; it has zero
awareness of what's downstream. Swapping the collector's export target
from Jaeger to Tempo later is a one-line config change
(`deploy/docker/otel-collector.yaml`'s exporter block), not an application
change. Jaeger was picked for local dev purely because it's a simpler
single-container run with a built-in UI, which is what mattered while
building milestone 1.

**Cost, stated plainly so it isn't mistaken for "done":** this is an
unresolved substitution, not a finished decision — Tempo (or an explicit
choice to keep Jaeger permanently) still needs to happen for the real AWS
deployment (milestone 10), since Tempo's usual pairing is Grafana+Loki+
Tempo as one coherent stack, which matters once Prometheus/Grafana
actually exist (see docs/MILESTONES.md's "Spec completeness check" —
metrics/dashboards aren't built yet either).

## 2026-10-03 — Policy gates the call, not the tool list

**What happens:** `tools/list` through the MCP gateway shows every tool
the registry trusts (active, unpoisoned), regardless of who's asking.
Policy is only evaluated on `tools/call`. So an agent can see a tool in
the list and then have the actual call denied by policy — proven live:
`echo` appears in `tools/list` for both `user-1` and `user-2`, but calling
it only succeeds for `user-1` (the seed policy's forbid rule blocks
`user-2` specifically).

**Why this is the right tradeoff, not just the easy one:** the tool list
is built once per upstream sync (`Gateway.Sync`), shared across every
caller — policy decisions are per-principal and would require either
re-listing per distinct (agent, actingAs) pair on every sync, or
filtering the list per-request instead of serving a precomputed one.
Neither is free, and critically, **visibility isn't the security
boundary — the call is**. Seeing a tool name and description isn't a
capability; invoking it is, and invocation is where the policy check
actually runs, which is why the live test above is still a legitimate
proof of enforcement despite the list looking the same to both users.

**Cost:** this can look confusing from an agent's perspective (why does
listing show a tool I then can't call?), and it means Warden can't yet
offer "only show me what I'm actually allowed to do" — which is a real,
better UX some authorization gateways provide. Worth revisiting once the
console (milestone 9) needs to show this kind of thing to a human anyway.

## 2026-10-03 — Idempotency keys are client-supplied, never server-derived

**Options:** derive an idempotency key automatically from the call's
content (e.g. hash of agent+action+resource), vs. require the caller to
supply one explicitly (`X-Idempotency-Key`), with no dedup at all if they
don't.

**Chose:** client-supplied only (`internal/approval`'s `Request`; NULL
key = Postgres never treats two NULLs as equal, so no dedup happens by
default — a deliberate, safe default, not an oversight).

**Why:** only the caller actually knows whether two calls are "the same
operation being retried" or "two separate operations that happen to look
identical" — e.g. an agent calling `delete_data` twice in a row, once for
real each time, is two separate approvable actions, not one. A
content-derived key would silently merge them into a single approval,
which means approving the first would also silently let the second
through without a human ever seeing it. This is the same reasoning Stripe
and most real payment/distributed-systems APIs use for idempotency keys.

**Cost:** a caller that doesn't supply a key gets no crash-safety for its
retries — every retry is a brand new approval request. That's the correct
default (safety through explicitness), but it does mean the durability
guarantee only helps callers that opt in.

## 2026-10-03 — Found via live testing: a code change to the seed policy doesn't reach an already-seeded database

**What happened:** `delete_data` was added to `DefaultSeedPolicy` in code,
but the live gateway still denied it — because `SeedIfEmpty` only ever
seeds once, the first time the `policies` table is empty. Changing the Go
constant afterward has no effect on a database that already has a version
1 row.

**This is not a bug in the versioning design — it's what versioning is
for.** The fix demonstrated during verification was the correct one:
insert a new version (2) with the updated source and activate it, rather
than mutating version 1's `cedar_source` in place (which was tried first,
then deliberately undone — see the live verification log — because it
defeats the entire point of "old versions kept for audit"). The gateway's
existing 10-second poll picked up version 2 automatically, no restart
needed.

**Why worth recording:** it's a realistic preview of the actual
operational question "how do you ship a policy change" — the answer is
"insert and activate a new version," never "edit the seed and redeploy."

## 2026-10-03 — Found via testing: two tests silently corrupted shared dev-database state

**What happened:** running the full test suite twice in a row (not an
exotic scenario — just re-running `go test ./...`) surfaced two real
hygiene bugs, both caught because tests here run against the same
Postgres/Redis a manually-run gateway uses, not an isolated throwaway
database:

1. `TestRequestWithSameKeyDeduplicatesAndNotifiesOnce` (approval package)
   never deleted its rows. A second run's "first" request collided with
   the first run's leftover row, making it look like a conflict instead
   of a fresh insert — the test failed, correctly, because the test
   itself was wrong, not the code.
2. `deactivateAllAndInsert` (policy package tests) deactivated whatever
   policy was currently active to install its own test policy, then only
   ever cleaned up its own row — never reactivating what it had turned
   off. Running the policy tests against the same database a dev gateway
   uses left that gateway with **no active policy at all**, which — per
   the fail-closed design — meant it started denying every single
   request. This was caught directly: the gateway logged
   `"initial policy load failed"` after a test run, mid-milestone-6
   verification.

**Fix:** approval tests now delete their own rows via `t.Cleanup`. Policy
tests now record which version(s) were active *before* swapping in a test
policy, and restore that in `t.Cleanup` — not just delete their own row.

**Why this is worth its own entry:** fail-closed (an earlier deliberate
decision, see milestone 3's DECISIONS.md entries) did exactly its job here
— it turned a test-hygiene bug into a loud, obvious failure (nothing
authorizes) instead of a silent one (everything authorizes). That's the
argument for fail-closed in concrete terms, not just in the abstract.

## 2026-10-03 — Polling for approval decisions, not Postgres LISTEN/NOTIFY

**Options:** `WaitForDecision` polls the approvals table on an interval
(1s in production use, faster in tests) vs. using Postgres's native
LISTEN/NOTIFY to be woken immediately when a decision lands.

**Chose:** polling.

**Why:** LISTEN/NOTIFY needs a dedicated, non-pooled connection per
listener — `database/sql`'s connection pooling model doesn't support it
cleanly; doing it properly means dropping to `pgx`'s native pool and
managing listener connections as a separate concern from everything else
already using `database/sql`. Polling is a few dozen lines, trivially
correct, and the added latency (up to one poll interval before a waiter
notices a decision) is irrelevant here — approvals are a human-timescale
operation (minutes), not a hot-path one, so shaving the last second off
detection latency isn't where the engineering effort belongs for v1.

**Cost:** every pending approval holds open a goroutine and a request
connection, polling, for as long as it waits — this doesn't scale to a
very large number of simultaneously-pending approvals the way a
notify-based wakeup would. Worth revisiting if that ever becomes a real
number instead of a hypothetical one.

## 2026-10-03 — Found via live testing: Postgres round-trips a timestamp through a different timezone, breaking every hash

**What happened:** every single `Verify` call failed, including on chains
with zero actual tampering — `TestVerifyPassesOnAnUntamperedChain` and
three others all failed identically right after being written. The hash
the writer computed and the hash the verifier recomputed from the exact
same stored row disagreed, for every record, every time.

**Root cause, found with a minimal isolated repro (not a guess):** pgx's
`stdlib` driver decodes a `TIMESTAMPTZ` column back as `time.Time` in the
*server process's local timezone* (IST on this machine), not UTC.
`time.Time.Equal()` still returns true (it's genuinely the same instant),
but `json.Marshal`'s RFC3339 output differs by `Location`
(`"...Z"` vs `"...+05:30"`) — and the hash is computed over that JSON
string, so two representations of the identical instant hash completely
differently. A second, smaller issue compounded it: Postgres
`TIMESTAMPTZ` only stores microsecond precision, while `time.Now()` is
nanosecond-precision, so even same-timezone round-trips would eventually
mismatch too.

**Fix:** `ComputeHash` (`internal/audit/record.go`) now normalizes with
`.UTC()` before hashing — in the one function both the writer and the
verifier call, not scattered across call sites where it's easy to forget
at one of them. `ChainWriter.Append` additionally truncates to
microsecond precision before hashing, matching what Postgres will
actually store.

**Why this one matters more than a typical bug:** this wasn't a
local-dev-only quirk — it would have made the audit log report **every
single record as tampered** on any machine not running in UTC, which is
most of them. A tamper-evidence system that cries wolf on legitimate data
is worse than no tamper-evidence system: it trains whoever's watching to
ignore the alarm. Caught here because the test suite runs for real against
a real Postgres on a non-UTC machine — it would not have been caught by
tests that mock the database or that happen to run in UTC (e.g. most CI
runners' default timezone).

## 2026-10-03 — Found via live testing: an async Kafka publish used the wrong context and silently failed on every call

**What happened:** real policy decisions were being made correctly, but
zero records ever reached the audit chain, with no errors visible in
in the main request-handling log lines — only once the promise callback's
own log line was checked did `"audit: publish failed", "error":"context
canceled"` show up, on every single publish.

**Root cause:** `Producer.Publish` initially called `kgo.Client.Produce`
with the *caller's* `ctx` — the HTTP request's context. `Produce` is
asynchronous: it returns immediately and the actual network send happens
later, often after the originating HTTP handler has already returned,
at which point `net/http` has already cancelled that request's context.
The send then fails against an already-cancelled context, every time.

**Fix:** `KafkaProducer` now takes a separate, long-lived `bgCtx` at
construction (the gateway's own top-level context, cancelled only on
shutdown) and uses that for the actual `Produce` call, ignoring the
per-call `ctx` entirely (`internal/audit/producer.go`).

**Why worth recording:** "fire-and-forget, off the hot path" is the
entire architectural point of routing audit events through Kafka instead
of a synchronous Postgres write (see the original tech-stack reasoning).
Wiring that pattern with the wrong context quietly defeats it in exactly
the way that's hardest to notice — no error at the call site, no crash,
just an audit log that silently never fills up. This is a general trap
worth remembering for any other fire-and-forget async work added later:
a context from a request is scoped to that request's lifetime, not to
"this background work I kicked off and no longer care about the result
of" — those need their own, separately-scoped context.

## 2026-10-03 — Found via live testing: Redpanda's advertised address was unreachable from host-run binaries

**What happened:** after fixing the context bug above, publishes still
silently went nowhere — no error, `ProduceSync` in an isolated debug
script simply hung forever, and the topic's high-watermark stayed at 0
no matter how many records were "successfully" produced.

**Root cause:** `deploy/docker/docker-compose.yml` advertised Redpanda at
`redpanda:9092` — correct for another *container* on the same Docker
network, but every Warden binary in this project runs directly on the
host (`go run`/built binaries), not containerized. A Kafka client's
bootstrap connection succeeds via the host-mapped port (`localhost:9092`
works fine for the initial metadata fetch), but the metadata response
then tells the client the *real* address to use for actual produce/fetch
requests — and `redpanda` doesn't resolve on the host, so those requests
hang indefinitely instead of failing with a clear error.

**Fix:** changed the advertised address to `localhost:9092`
(`deploy/docker/docker-compose.yml`). Also added
`kgo.AllowAutoTopicCreation()` so a fresh environment doesn't need a
manual `rpk topic create` step — though real topic provisioning belongs
in Terraform alongside the rest of the AWS infra (milestone 10), not
runtime auto-creation.

**Why this is the same lesson as the Redpanda-in-Kubernetes entry from
milestone 1, just mirrored:** there, the fix was using the *cluster* DNS
name because other pods resolve through cluster DNS, not `localhost`.
Here, the fix is the opposite, because the clients are on the host, not
in containers. The general principle is the same both times: an
advertised address has to be reachable from wherever the actual client
runs, and "it connects initially" is not evidence that it works — the
failure mode for a wrong advertised address is specifically that the
*first* connection succeeds and everything after it quietly doesn't.

## 2026-10-03 — Found via design review: policy must run before the cache lookup

**What was wrong:** `internal/cache`'s response cache (milestone 2) is
keyed only on model + messages — deliberately, so identical requests from
*different* agents can share a cache hit. But that means it has no concept
of who's asking. While wiring the policy engine into `httpapi.ChatHandler`,
checking the handler's existing order (rate limit → budget → cache →
router) surfaced a real bug: if the cache were checked before policy, an
agent *denied* access to a model could still receive another agent's
cached answer for that same model, since the cache has no idea a
particular caller isn't authorized.

**Fix:** reordered so policy runs first, before rate limit, budget, or the
cache lookup — see the ordering comment directly on `ChatHandler.ServeHTTP`
in `internal/httpapi/chat.go`. Policy also runs before rate-limit/budget
for a separate, non-security reason: a denied call shouldn't consume
either quota.

**Why this is worth recording on its own:** it was never exercised by any
request in testing — mock-only testing with one agent identity doesn't
surface an authorization bypass that only exists when *multiple* principals
share a cache. It was caught by re-reading the request path with the new
policy requirement in mind, which is the actual argument for doing a
design pass instead of only testing the happy path: some classes of bug
only show up when you ask "what's the right order for these checks," not
"does this specific test pass."

## 2026-10-03 — Guard classifier fails open, deliberately, same as rate limit/budget

**Decision:** `guard.Client.Scan` returns `ok=false` on any failure
(timeout, connection error, classifier down) and `mcpgateway` treats that
as "nothing to block" — the call proceeds, logged loudly but not denied.

**Why:** this is a defense-in-depth layer sitting alongside the policy
engine, which is the actual authorization boundary and already fails
*closed* (milestone 3/5). Refusing all tool calls because an ML sidecar
is unreachable would trade a detection gap (bad, but the policy engine is
still enforcing independently) for a total availability outage (worse).
The contrast with the policy engine's fail-closed design is the point —
see milestone 3's original fail-open/fail-closed entries for where that
line gets drawn and why.

## 2026-10-03 — Found via benchmarking: ONNX export latency was ~25x higher than it needed to be

**What happened:** the first served model measured ~123ms per
classification — against a "strict latency budget," this would have
meant either abandoning the hot-path requirement or quietly inflating the
timeout to hide a real performance problem.

**Root cause, found by isolating tokenization from inference:**
`export_onnx.py`'s `dynamic_axes` only marked the batch dimension as
dynamic, not sequence length — so the exported graph was hardcoded to
always compute the full 128-token path, regardless of how short the
actual input was. Combined with `intra_op_num_threads=1` and default
graph optimization, every single call paid for processing a full 128-token
sequence on one thread.

**Fix:** marked sequence length dynamic too (`{0: "batch", 1: "sequence"}`
on all three tensors), let each input use its own real length instead of
padding to `max_length`, set `intra_op_num_threads=4` and
`ORT_ENABLE_ALL` graph optimization. Measured result: ~123ms → ~4-9ms per
call, a ~25x difference from three compounding fixes, not one.

**Why worth recording with this much detail:** this is a good concrete
answer to "how do you make an ML model fast enough for a hot path" beyond
"export to ONNX" — the export itself can still be badly configured in a
way that defeats most of the benefit, and the fix came from profiling
(isolating tokenizer time from inference time, then testing one variable
at a time), not from guessing.

## 2026-10-03 — Found via live testing, twice: the classifier didn't generalize to its actual deployment domain

**What happened (round 1):** the moment the trained classifier was wired
into `mcpgateway.Sync` and the gateway restarted, it flagged **both**
demo tools' descriptions as injection attempts — including "Echoes back
whatever text you send it," which is about as benign as text gets.

**Root cause:** `deepset/prompt-injections` (the public training dataset)
is mostly conversational chat-style text. Its injection examples are
almost all imperative ("ignore your instructions and...") and its benign
examples are mostly declarative/conversational. The model learned
"imperative sentence" as a cheap, mostly-correct-on-this-dataset proxy for
"injection" — which fails completely on tool descriptions, since those
are imperative by genre ("Permanently deletes a dataset") regardless of
intent.

**Fix, round 1:** added hand-written examples in the tool-description
domain for **both classes** (`augment_data.py`'s `BENIGN_TOOL_TEXTS` /
`INJECTION_TOOL_TEXTS`), not just benign ones. Adding only benign examples
would have taught a different, equally wrong proxy ("tool-description-
shaped text = benign"), which would make the classifier blind to real
tool-poisoning attempts — and those are routinely phrased as exactly this
kind of short imperative instruction, hidden inside otherwise plausible
tool text. Verified the fix didn't just memorize the specific examples by
testing paraphrased variants, not the literal training strings.

**What happened (round 2):** fixing round 1 and testing the actual
output-scanning path (not just the description path) live surfaced a
*second* gap: `echo: hello through the proxy` — the literal shape of a
real tool output — was still flagged. Neither deepset's prompts nor the
round-1 additions look like that; both are full, grammatically complete
sentences, while tool outputs are routinely short fragments ("OK",
"42", "echo: hello world", a JSON blob).

**Fix, round 2:** added `SHORT_BENIGN_OUTPUTS` / `SHORT_INJECTION_OUTPUTS`
— short, fragment-like examples of both classes, matching what a tool
output actually looks like. Re-verified the exact live call that failed
before now succeeds, and that a real injection embedded in a tool-call
argument is still correctly blocked.

**Why this two-round story is worth keeping in full, not just the final
fix:** this is train/serve skew, a real and common ML failure mode, caught
only because the classifier was exercised against its actual deployment
inputs (tool descriptions, tool outputs) rather than just evaluated on a
held-out split of its own training distribution — which would have shown
great numbers right up until the first real tool it looked at. "The
held-out metrics look good" and "this works on the data it'll actually
see in production" are different claims, and conflating them is a classic
way ML systems look fine in testing and fail immediately in deployment.
