# Build milestones

Each milestone ends with something running and tested. Don't start the next
until the current one is done.

- [x] **1. Foundations** — repository, CI pipeline, local Kubernetes, PostgreSQL, Redis, Kafka, OpenTelemetry wired up.
- [x] **2. Model gateway** — streaming passthrough to 2+ providers, API keys, fallback, circuit breakers, exact caching. First latency benchmark.
- [x] **3. Identity and rate limits** — OAuth with Keycloak, on-behalf-of tokens, distributed rate limits and budgets.
- [x] **4. MCP gateway** — tool registry, proxying, definition pinning and change detection.
- [x] **5. Policy engine** — Cedar policies, versioning, decision cache, decision logging.
- [x] **6. Durable approvals** — approval state machine, webhook notifications, expiry, kill tests.
- [ ] **7. Audit log** — hash chain, signed checkpoints, verification command.
- [ ] **8. Guard classifier** — dataset, fine-tuning, ONNX service over gRPC, benchmark vs. LLM-as-judge.
- [ ] **9. Console** — tools, policies, approvals, audit and spend views.
- [ ] **10. Ship it** — AWS deploy with Terraform, full load and fault test reports, demo video, docs site, write-up.
- [ ] **11. Version 2** — delegation chains, just-in-time access, policy dry-run.

## Milestone 1 progress

- [x] Repository scaffolded (Go module, `cmd/gateway`, `cmd/control-api`, `services/guard-classifier`, `console`, `deploy/*`).
- [x] Local dev stack: Postgres, Redis, Redpanda, OTel Collector, Jaeger (`deploy/docker/docker-compose.yml`).
- [x] CI pipeline: build + vet + test on every push (`.github/workflows/ci.yml`).
- [x] Local Kubernetes manifests (`deploy/k8s-local/`, applied via Kustomize) for Postgres, Redis, Redpanda, OTel Collector, Jaeger — verified on a real `kind` cluster: all 5 pods reached Ready, PVCs bound, then cluster torn down (disposable, same cost principle as EKS in production).
- [x] OpenTelemetry wired into gateway/control-api (`internal/telemetry`, `otelhttp` middleware) — verified real spans reach Jaeger from both services.

**Milestone 1: done.**

## Milestone 2 progress

- [x] `/v1/chat/completions` endpoint, OpenAI-compatible request/response shape, both streaming (SSE) and non-streaming.
- [x] Router with per-provider fallback order (`internal/router`), unit-tested.
- [x] Circuit breaker per provider (`internal/breaker`), unit-tested (closed→open→half-open→closed transitions).
- [x] Exact-match response cache in Redis (`internal/cache`), verified via a real cache-hit log line.
- [x] Mock provider (`internal/provider/mock`) standing in for real backends, with on/off "unhealthy" switch to prove fallback deterministically.
- [x] End-to-end manual verification: non-stream call, cache hit, SSE stream, and fallback-when-primary-down — all observed in running logs, not just code review.
- [x] First latency benchmark: see `docs/BENCHMARKS.md` — p99 4.49ms vs. 15ms target.
- [ ] Real provider integration (OpenAI/Anthropic/other) — deferred until an API key is supplied; see DECISIONS.md.

**Milestone 2: core logic done; real-provider wiring open pending an API key.**

## Milestone 3 progress

- [x] Keycloak running locally, realm/client defined as code (`deploy/docker/keycloak-realm.json`), not clicked through an admin UI.
- [x] `internal/identity`: verifies OAuth access tokens against Keycloak's JWKS, extracts the agent identity (`azp` claim) and its "acting as" allowlist.
- [x] Every call now requires both identities: a valid Bearer token (the agent) + an `X-Acting-As` header naming a user the token permits (the human).
- [x] `internal/ratelimit`: distributed, Redis-backed, atomic via a Lua script — unit-tested per-key isolation and threshold behavior.
- [x] `internal/budget`: distributed spend cap, atomic reject-without-partial-charge — unit-tested specifically for that atomicity.
- [x] End-to-end verified live: 401 (no token) / 400 (no acting-as header) / 403 (disallowed acting-as user) / 200 (valid) / 429 (rate limit tripped, confirmed at a demo limit of 3).
- [ ] Real token-exchange (RFC 8693) instead of the hardcoded-allowlist simplification — noted as a deliberate v1 simplification in DECISIONS.md, not a gap to silently carry forward.

**Milestone 3: done** (simplification on token exchange documented, not hidden).

## Milestone 4 progress

- [x] Postgres introduced for the first time: `internal/db` runs embedded, versioned migrations (golang-migrate) on startup — not ad-hoc schema setup.
- [x] `internal/registry`: pin-on-first-sight / block-on-change tool registry, unit-tested against a real migrated Postgres, including the "doesn't self-heal by reverting" behavior.
- [x] `internal/mcpgateway`: Warden is itself an MCP server (`/mcp`, auth-gated same as the model endpoint) that proxies `tools/list`/`tools/call` to real upstream MCP servers, reconciling every listed tool against the registry.
- [x] `cmd/demo-mcp-server`: a real MCP server (official Go SDK) standing in for a third-party one, with an env-var-controlled tool description for simulating a poisoning attempt on demand.
- [x] Verified end-to-end with a real MCP client (not just unit tests): listed and called the proxied `echo` tool successfully through Warden; then restarted the upstream with a changed tool description and confirmed, after the periodic sync, that the tool disappeared from the list and calling it returned "unknown tool."
- [x] Found and fixed a real bug during that verification: the upstream client session doesn't survive the upstream process restarting; added reconnect-on-failure to both the sync loop and the call-proxying path.

**Milestone 4: done.**

## Milestone 5 progress

- [x] `internal/policy`: Cedar policy engine (official `cedar-go`), versioned policies in Postgres (partial-unique-index enforces exactly one active version), compiled policy refreshed on a 10s poll so activating a new version takes effect without a restart.
- [x] Redis decision cache keyed on `(policy version, agent, actingAs, action, resource)` — a version bump invalidates every cached decision implicitly, no explicit cache-busting pass needed.
- [x] Every decision logged (allow/deny, matching policy IDs, cache hit or not) — not yet tamper-evident (that's milestone 7's job specifically).
- [x] Wired into **both** call paths: model calls (`httpapi.ChatHandler`) and tool calls (`mcpgateway`'s per-call identity resolution via `RequestExtra.Header`, since one MCP session can carry calls acting as different users).
- [x] Fails closed: unit-tested (`TestFailsClosedBeforeAnyPolicyLoaded`) and contrasted explicitly with rate-limit/budget's fail-open in DECISIONS.md.
- [x] Verified live: permit → 200, default-deny (no matching rule) → 403, and forbid-overrides-permit through the real MCP proxy (`echo` allowed for user-1, denied for user-2 by an explicit forbid rule) — not just unit tests.
- [x] Found and fixed a real ordering bug during this milestone: the response cache is keyed on content only (not principal), so policy now runs *before* the cache lookup — otherwise a denied agent could receive another agent's cached answer. Documented in DECISIONS.md.
- [x] Found and documented a real, honest limitation: `tools/list` isn't policy-filtered (shows the same list to everyone); only `tools/call` is policy-gated. Visibility isn't the security boundary, invocation is — but it's worth knowing, not discovering later.

**Milestone 5: done.**

## Milestone 6 progress

- [x] `internal/approval`: full state machine (pending/approved/rejected/expired) in Postgres. Atomic decide (can't double-decide), atomic execution claim (can't double-execute, verified with 20 concurrent goroutines under `-race`), client-supplied idempotency keys (never server-derived — see DECISIONS.md), lazy + periodic expiry.
- [x] Notifications: `LogNotifier` (default, logs the approve/reject curl commands) and a real `WebhookNotifier` (configurable `WEBHOOK_URL`) — same mock-first/real-plug-in pattern as the rest of the project.
- [x] `approval_rules` table: independent of Cedar policy — a call can be policy-permitted and still require a human to sign off. Wired into the MCP tool-call path.
- [x] control-api wired up for real for the first time: its own Postgres connection, `POST /approvals/{id}/decide`, `GET /approvals/{id}`, `GET /approvals?state=`.
- [x] Verified with a real kill test, not just unit tests: `kill -9`'d the gateway mid-pending-approval, confirmed the approval survived in Postgres, restarted, approved, and confirmed via the downstream tool's own call counter that it executed exactly once even after two retries. Full writeup in `docs/BENCHMARKS.md`.
- [x] Found and fixed two real test-hygiene bugs while verifying this (not demo issues — bugs that would have silently broken a running dev gateway): the approval dedup test didn't clean up its rows, and the policy tests deactivated whatever policy was live and never restored it. Both documented in DECISIONS.md.
- [x] Found and fixed a real operational gap: editing `DefaultSeedPolicy` in code doesn't retroactively update an already-seeded database row — seeding only ever happens once. The correct fix is activating a new policy version, which is exactly what got demonstrated live (version 1 → version 2, picked up by the gateway's existing 10s poll with no restart needed).
- [ ] Statistical 1,000-run kill-test benchmark (vs. this milestone's one fully-reasoned kill test) — tracked as a follow-up for the milestone 10 benchmark pass.

**Milestone 6: done** (1,000-run statistical benchmark explicitly deferred, not silently skipped).

## Spec completeness check (2026-10-03, after milestone 4)

Per-milestone tracking above is necessarily scoped to that milestone's own
bullet. This section is a periodic cross-check against the FULL original
spec (tech stack table + production-readiness section + data model), so
nothing broader gets silently dropped. Re-run this check at each milestone
boundary.

**Found and tracked as explicit, not-forgotten gaps (not yet built):**

- [ ] **Metrics + dashboards.** Tech stack lists Prometheus + Grafana;
      production-readiness requires request rate, added latency (p50/p99),
      decisions by type, approval queue depth, provider errors, cache hit
      rate, plus alerts on error rate/latency SLO/audit-chain failures.
      Only tracing (OTel → Jaeger) exists so far — no metrics exporter, no
      Prometheus, no Grafana, no alerts. Natural point to add: as each
      feature matures enough to have a meaningful dashboard, and/or a
      dedicated pass before milestone 10 ("Ship it").
- [ ] **Per-team scoping for budgets/rate limits.** Spec says "per-team
      and per-agent"; only per-agent is wired (`internal/ratelimit`,
      `internal/budget` take an arbitrary string scope, so a team ID would
      work mechanically, but no Team entity/concept exists anywhere yet).
      Natural point to add: when the console (milestone 9) needs to
      manage teams anyway.
- [ ] **Fault/kill/security test suites** (Toxiproxy network-fault tests,
      kill tests beyond the manual ones done for milestone 1/4, a security
      test suite of known injection/poisoning attacks). Explicitly a
      production-readiness "Testing" requirement, not yet started.
      Natural point to add: a dedicated testing pass, likely alongside or
      just before milestone 10.
- [ ] **Runbook.** Production-readiness docs requirement alongside
      DECISIONS.md (actively maintained) and a written postmortem (N/A
      until a real failure happens worth writing up).

Nothing else found missing against the full spec as of this check — the 8
core v1 features' milestones so far (1-4) match what the spec actually
asked for them to do, including the harder-to-spot details (short-lived
tokens, exact-match-not-fuzzy caching, "every call carries both
identities," etc.).
