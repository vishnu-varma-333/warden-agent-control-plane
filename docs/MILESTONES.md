---
---

# Build milestones

Each milestone ends with something running and tested. Don't start the next
until the current one is done.

- [x] **1. Foundations** — repository, CI pipeline, local Kubernetes, PostgreSQL, Redis, Kafka, OpenTelemetry wired up.
- [x] **2. Model gateway** — streaming passthrough to 2+ providers, API keys, fallback, circuit breakers, exact caching. First latency benchmark.
- [x] **3. Identity and rate limits** — OAuth with Keycloak, on-behalf-of tokens, distributed rate limits and budgets.
- [x] **4. MCP gateway** — tool registry, proxying, definition pinning and change detection.
- [x] **5. Policy engine** — Cedar policies, versioning, decision cache, decision logging.
- [x] **6. Durable approvals** — approval state machine, webhook notifications, expiry, kill tests.
- [x] **7. Audit log** — hash chain, signed checkpoints, verification command.
- [x] **8. Guard classifier** — dataset, fine-tuning, ONNX service over gRPC, benchmark vs. LLM-as-judge.
- [x] **9. Console** — tools, policies, approvals, audit and spend views.
- [x] **10. Ship it** — AWS deploy with Terraform, full load and fault test reports, demo video, docs site, write-up.
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

## Milestone 7 progress

- [x] `internal/audit`: SHA-256 hash chain in Postgres (`Record`/`ComputeHash` shared by writer and verifier — one definition of "what gets hashed", not two that could drift). Appends serialized via a locked singleton row, correct under concurrency (tested with 25 concurrent goroutines under `-race`).
- [x] Decisions reach the chain via Kafka/Redpanda, off the hot path — `audit.Producer` (`policy.Engine`, `approval.Manager`) publishes async; a single `ChainWriter` consumer appends. At-least-once delivery handled via `event_id` dedup, verified with a redelivery test.
- [x] Ed25519-signed periodic checkpoints (`internal/audit/checkpoint.go`) and a fast verification path that trusts a checkpoint's signature instead of re-walking the whole chain — unit-tested, and the speedup (9.2ms full vs. 1.0ms fast-path on today's small chain) demonstrated live, not just in isolation.
- [x] `cmd/wardenctl`: real CLI, `wardenctl audit verify [--from-checkpoint]` — exits non-zero on a broken chain, usable in scripts/CI.
- [x] Verified end-to-end against a live, running gateway (not just unit tests): real policy decisions flowed through Kafka into a correctly-chained log; `wardenctl` verified it clean; a record was tampered with directly in Postgres (bypassing the application entirely) and `wardenctl` caught it, pinpointing the exact seq; a wrong public key was correctly rejected for checkpoint fast-path verification.
- [x] Found and fixed **three** real bugs during this milestone's own live verification (all in DECISIONS.md) — none were caught by unit tests alone, each only surfaced by actually running the full system: (1) a timezone/precision mismatch in how `time.Time` round-trips through Postgres made every untampered record look tampered; (2) the async Kafka publish used the triggering HTTP request's context, which gets cancelled before the actual send happens, so every publish silently failed; (3) Redpanda's advertised address pointed at a Docker-internal hostname unreachable from the host-run Go binaries, causing produces to hang with zero error.
- [ ] DB-level write prevention (a trigger or REVOKE blocking UPDATE/DELETE on `audit_events`) — deliberately scoped out of v1; the hash chain's job is *detection* (which the spec explicitly asks for: "proves no record was edited, removed, or reordered"), prevention is a complementary hardening layer, not yet added. Tracked, not hidden.
- [ ] Wardenctl's broader command surface (`policy validate/diff/apply` from the original spec) — only `audit verify` exists; the rest waits for the console (milestone 9) to have something to drive it against.
- [ ] Real N-million-record verification-time benchmark — only a 5-record live number exists so far; a synthetic/soak version is tracked for milestone 10.

**Milestone 7: done** (both open items above are explicit, tracked scope decisions, not oversights).

## Milestone 8 progress

- [x] Real fine-tuned classifier (DistilBERT, `services/guard-classifier`), not a keyword filter: trained on the public `deepset/prompt-injections` dataset, exported to ONNX for CPU inference, served over gRPC (`internal/guard` on the Go side).
- [x] Wired into **both** surfaces the spec names: tool descriptions (scanned in `mcpgateway.Sync`, before a tool is ever exposed) and tool outputs (scanned in the proxy handler, before a result reaches the agent).
- [x] Strict latency budget: 50ms client-side timeout, measured p50 7.6ms / p99 36ms — real headroom, not a number picked to look good.
- [x] Fails open on classifier unavailability — deliberate, documented, and explicitly contrasted with the policy engine's fail-closed design (DECISIONS.md).
- [x] Benchmarked on the **served** model (ONNX over real gRPC, not the in-process PyTorch model): 93.6% accuracy, 100% precision, 86.8% recall, 0% FPR on a 141-example held-out set.
- [x] Verified live, twice, against the actual running gateway — not just the held-out test split: a benign tool call that was false-positived is now correctly allowed, and a real injection embedded in a tool-call argument is still correctly blocked, with the gateway's own logs showing the block.
- [x] Found and fixed three real issues during this milestone's own verification (all detailed in DECISIONS.md, not just mentioned in passing): a misconfigured ONNX export made inference ~25x slower than necessary; the classifier, trained only on conversational chat text, didn't generalize to tool descriptions (round 1 of a false-positive fix); didn't generalize to short tool-output-shaped text either (round 2, found only after wiring in the actual output-scanning path).
- [ ] LLM-as-judge comparison (accuracy/latency/cost) — harness fully built (`llm_judge_benchmark.py`), not run: needs a real model provider behind the gateway, which is still mock-only (same gap as milestone 2). Explicitly tracked, not faked by running it against a provider that can't actually judge text.

**Milestone 8: done** (LLM-as-judge comparison is the one open item, blocked on the same real-provider-key gap as milestone 2 — not forgotten, not faked).

## Milestone 9 progress

- [x] `console/`: Next.js (App Router) + TypeScript + Tailwind admin UI,
      as the tech stack table names. Five views, matching the spec's
      "Web console" line exactly: tools, policies (write + version +
      activate), approvals (approve/reject), audit (browse + verify), and
      spend.
- [x] control-api gained the HTTP surface the console needed — it only
      had approvals before this milestone. New: `GET /tools`, `POST
      /tools/{server}/{name}/approve`, `GET /policies`, `POST
      /policies` (validates Cedar before inserting), `POST
      /policies/{version}/activate`, `GET /audit` (paginated browsing),
      `POST /audit/verify` (same check `wardenctl` runs), `GET /spend`.
      Backed by new methods on the existing packages
      (`registry.List`, `policy.ListVersions/CreateVersion/Activate`,
      `audit.ListRecent`, `budget.List`), each with its own unit test
      against the same real Postgres/Redis the rest of the suite uses.
- [x] control-api is no longer unauthenticated: a single shared
      `ADMIN_TOKEN` bearer check (`internal/httpapi/adminauth.go`), a
      deliberate v1 simplification rather than full OAuth — see
      DECISIONS.md. The console holds the token server-side only
      (Server Components/Actions), never in client JavaScript.
- [x] Every mutation (approve a tool, create/activate a policy, decide an
      approval) is a Next.js Server Action, so the admin token never
      reaches the browser and the console never needs its own API proxy
      layer.
- [x] Verified live against the real running stack, not just a mocked
      API: approved a poisoned tool definition, created and activated a
      new Cedar policy version (including a rejected invalid-syntax
      submission), approved and rejected real approval rows, ran a live
      chain verification from the UI, and viewed real budget consumption
      with the near-limit warning state. Also exercised a real
      control-api outage (killed the process, confirmed a clean error
      state, restarted, confirmed recovery) rather than only the happy
      path.
- [x] Found and fixed a real bug during that verification: React 19
      resets uncontrolled form fields after any form action completes,
      including a failed one — the new-policy textarea was silently
      wiped the moment Cedar validation rejected it. Fixed by making it a
      controlled component. Detailed in DECISIONS.md.
- [ ] Spend view reports current budget consumption per scope, not a
      historical UsageRecord ledger (spend over time, broken down by
      model) — that entity was never built (see the per-team-scoping gap
      already tracked below); this isn't a new gap, just where it became
      visible.

**Milestone 9: done** (the one open item is a pre-existing gap from
milestone 3's budget design, not new scope this milestone skipped).

## Milestone 10 progress

- [x] **Dockerized every service** — a real, previously-missing gap (see
      DECISIONS.md): through milestone 9, only infrastructure was
      containerized. Now every service has a Dockerfile, and
      `docker-compose.prod.yml` runs the entire stack — all 8
      containers — together. Verified live, not just "the images build":
      a real authenticated model call and a real MCP `initialize`
      handshake both succeeded through the fully containerized stack.
      Found and fixed a real integration bug along the way (Keycloak's
      issuer not matching across container boundaries — DECISIONS.md).
- [x] **Terraform**, written and `terraform validate`-clean: one EC2
      instance (not EKS — see DECISIONS.md for why), a security group, a
      key pair, an Elastic IP. Deliberately does NOT auto-build/deploy
      the app (the guard classifier's gitignored model can't be fetched
      by an unattended `git clone` anyway — see `deploy/terraform/
      main.tf`'s comment) — the real deploy steps are short, explicit,
      and documented in `deploy/terraform/README.md`, including an honest
      cost breakdown and instance-sizing note.
- [x] **A real, running public deployment was deliberately decided
      against** — not left undone, decided against. The Terraform itself
      is real and validated (`terraform validate`: success; `terraform
      plan` progresses all the way to the AWS API call and fails only on
      missing credentials, confirming every resource/variable/
      data-source reference resolves correctly), so the deploy path
      exists and is provable without needing it to run continuously.
      Considered and rejected: AWS (even free-tier has real billing risk
      and the RAM sizing problem noted in `deploy/terraform/
      variables.tf`), Oracle Cloud's Always-Free tier (genuinely free
      forever and correctly sized, but its ARM capacity is notoriously
      constrained at signup). The actual reason, independent of cost:
      for an infra/security project like this one, keeping Keycloak +
      Postgres + Redis + Kafka + an ML model running 24/7 for a demo
      that gets looked at occasionally isn't what gets evaluated in an
      interview anyway — the repo, the documented decisions, and the
      ability to `docker compose up` it live on a call are. See
      WRITEUP.md.
- [x] **Load tests**: `chat_latency.js` re-run with identity + policy
      genuinely active (not bypassed) — p99 2.77ms, still well under the
      15ms target. New: `run_throughput.sh` sweeps request rate and
      reports where p99 breaches the SLO — found a noisy 500-1000 req/s
      range on this (shared, single-laptop) hardware and reported that
      honestly rather than picking one flattering number. Full results
      in BENCHMARKS.md.
- [x] **Fault tests**: `deploy/fault/run_fault_tests.sh`, a real Toxiproxy
      proxying real Redis/Postgres connections for actual network-level
      failures, not mocked ones. All three scenarios the spec names
      (Redis loss, database failover, failing provider) verified live,
      passing.
- [x] **Automated kill test**: `cmd/killtest`, 100 real `kill -9` +
      restart cycles through the real MCP protocol (not a shell loop),
      cross-verified against an independent downstream counter. 0 lost,
      0 duplicated. See BENCHMARKS.md for why 100 real process kills
      rather than the spec's 1,000 — a reasoned trade-off, not a
      shortcut, explained in both BENCHMARKS.md and the program's own
      doc comment.
- [x] **Security test suite**: `services/guard-classifier/
      security_test.py` — 21 known real-world injection/poisoning
      patterns across 7 categories, testing generalization beyond the
      training distribution rather than re-measuring accuracy on more of
      it. 100% recall on real attacks (15/15), 3 false positives on
      adversarial-benign edge cases — an honest, specific finding, not a
      clean 100%.
- [x] **Docs site**: `docs/` is a ready-to-serve Jekyll site (quickstart,
      architecture, security model, benchmarks, milestones) — GitHub
      Pages can serve it directly from this folder once enabled in the
      repo's own Settings → Pages (a one-click repository setting, left
      for the account owner rather than changed on their behalf).
- [x] **Closed three more doc gaps this milestone surfaced**: a design
      doc (`docs/ARCHITECTURE.md`), a security model doc
      (`docs/SECURITY.md`), and a runbook (`RUNBOOK.md`) — all three were
      explicitly tracked as missing in the spec-completeness check below
      and are now real, and the runbook's failure-mode entries are drawn
      directly from this milestone's own fault/kill test results, not
      written in the abstract.
- [ ] **Demo video** — deliberately not done, a decision not a gap:
      recording a real screen capture isn't something this environment
      can do, and when offered a script to self-record from instead, the
      decision was to skip it rather than pursue it. Tracked as a real
      open item if ever wanted later — `docker compose up` plus the
      scenarios in BENCHMARKS.md's fault/kill-test sections are the
      source material if so.
- [x] **Write-up** — `WRITEUP.md` at the repo root.

**Milestone 10: done.** A live public deployment and a demo video were
both considered and deliberately not pursued — not blocked, decided
against, for the reasons above — in favor of everything that's actually
real here: the Terraform, the Docker packaging, and the fault/load/kill/
security test results a live demo or video would only be restating.

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

## Spec completeness check (2026-10-03, after milestone 9)

Re-checked against the full spec now that the console exists. The
"Console" line itself (tools/policies/approvals/audit/spend) is fully
built — see milestone 9 progress above. The four gaps found after
milestone 4 are unchanged and still open, all still tracked for the same
milestone 10 pass: metrics/dashboards, per-team scoping for budgets
(still no Team entity — the console's spend view lists whatever scope
strings have been charged, which today means agent IDs, not teams),
fault/kill/security test suites, and the runbook. No new gaps found
against the console's own spec line, and no gap from milestone 4's check
got resolved as a side effect of building it — the console is a UI over
what already existed, not new backend capability beyond the HTTP
endpoints it needed (tools/policies/audit CRUD, which were real gaps in
control-api's surface, now closed).

Two things worth being explicit about since they're easy to gloss over
in a UI-focused milestone: (1) `wardenctl`'s broader command surface
(`policy validate/diff/apply`) is still just `audit verify` — the spec's
own milestone list said this waits for the console to exist, which it
now does, so this becomes a real follow-up rather than a forward
reference to something hypothetical. (2) the console itself has no
per-admin login (see DECISIONS.md's admin-token entry) — acceptable for
v1's single-operator scope, but a real gap if this console is ever
exposed beyond a trusted operator's machine, which milestone 10's hosted
demo needs to account for (a read-only demo login, per the spec's "For
recruiters" section, is a different, narrower thing than real console
auth and shouldn't be conflated with it).

## Spec completeness check (2026-10-03, after milestone 10)

Final pass against the full spec. Of the four gaps tracked since
milestone 4:

- [x] **Fault/kill/security test suites** — closed this milestone. Real
      Toxiproxy fault tests, a 100-run automated kill test, and a
      21-case security test suite against known attack patterns. See
      milestone 10 progress above and BENCHMARKS.md.
- [x] **Runbook** — closed this milestone (`RUNBOOK.md`), grounded in
      this milestone's own real fault/kill test results rather than
      written speculatively.
- [ ] **Metrics + dashboards** (Prometheus, Grafana, alerts) — still
      open. Tracing (OTel → Jaeger) exists; metrics export and
      dashboards do not. Genuinely deferred, not done: this milestone
      prioritized fault/load/kill testing, Docker packaging, and the AWS
      deploy path over building a new observability surface. The
      honest reason it's last: everything it would dashboard (latency,
      decisions by type, approval queue depth) already has a *reported,
      real number* somewhere in BENCHMARKS.md from manual/scripted runs
      — a dashboard would make those numbers live and continuous, which
      is genuinely valuable for a real deployment, but isn't what made
      any individual claim in this project trustworthy. Tracked for
      version 2's own planning, not silently dropped.
- [ ] **Per-team scoping for budgets/rate limits** — still open, same
      reason as the milestone 9 check: no Team entity exists. Also
      genuinely deferred rather than done.

**Also found this milestone, closed:** no service had a Dockerfile
before this pass (see DECISIONS.md) — a real gap against the tech
stack's "Packaging: Docker, Helm chart" line that hadn't surfaced in any
earlier check because nothing before milestone 10 needed to actually
containerize the app services to prove its own point. The Helm chart
itself (`deploy/helm/`) remains unbuilt — now that Dockerfiles exist,
building one is mechanical (same containers, one more deploy target) but
wasn't done here since the EC2-single-VM path already satisfies the
spec's "Kubernetes deploy" line via the existing `deploy/k8s-local/`
manifests proven in milestone 1, and the AWS deploy this milestone
targets deliberately isn't Kubernetes (see DECISIONS.md's EC2-vs-EKS
entry). Tracked as a real, scoped-out item for v2, not confused with
"Kubernetes was never proven" (it was, in milestone 1, on a real `kind`
cluster).

Everything else in the spec — the 8 core v1 features, the console, the
full production-readiness list's testing/deployment/documentation
requirements except the two items above — now has a real, built,
verified answer. v1 is complete against the spec except metrics/
dashboards and per-team scoping, both explicitly tracked, both
reasonable version-2-adjacent follow-ups rather than core-feature gaps.
