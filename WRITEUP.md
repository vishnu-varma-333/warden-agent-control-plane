# Warden — Agent Control Plane

A zero-trust authorization gateway that sits between AI agents and every
tool or model they call, and decides on each call whether it may proceed.

## The problem

Companies want AI agents to take real actions — call internal tools, hit
real APIs, touch real data — but agents can be tricked by prompt
injection, use tools that were poisoned or silently changed after being
trusted, exceed the authority they were meant to have, or run up costs
with no one watching. Most MCP deployments have no central place to
enforce permissions on any of this, or to prove afterward what actually
happened.

## What I built

An authorization gateway that every agent call — model or MCP tool —
passes through, enforcing eight things end to end:

- **Model gateway**: OpenAI-compatible endpoint, streaming, per-provider
  fallback, circuit breakers, exact-match caching.
- **MCP gateway and tool registry**: Warden is itself an MCP server;
  agents connect to it as their one endpoint, and every tool definition
  is pinned by hash the first time it's seen — any silent change blocks
  the tool until a human re-approves it.
- **Identity**: OAuth 2.1/OIDC via Keycloak, short-lived tokens; every
  call carries both the agent's own identity and the human it's acting
  for, as two independently verified claims.
- **Policy engine**: Cedar policies, versioned, evaluated on every call,
  every decision logged with its reasons. Fails closed — no policy ever
  loaded means deny everything, not allow everything.
- **Durable human approvals**: a risky action pauses for a human
  decision, independent of policy, and survives a gateway crash mid-wait
  — proven with 100 real `kill -9` cycles, not just reasoned about.
- **Tamper-evident audit log**: a SHA-256 hash chain with signed
  checkpoints; a verification command proves — not just claims — that no
  record was edited, deleted, or reordered.
- **Injection and poisoning guard**: a fine-tuned DistilBERT classifier,
  exported to ONNX, served over gRPC in the hot path under a strict
  latency budget, scanning both tool descriptions and tool outputs.
- **Budgets and rate limits**: distributed, atomic, correct across
  multiple gateway instances.

Plus an admin console (Next.js) over all of it, a full Docker Compose
packaging of the whole stack, and Terraform for an AWS deploy.

## Real numbers, not estimates

| Metric | Result |
|---|---|
| Added gateway latency (identity + policy active), p99 | 2.77ms (target: <15ms) |
| Guard classifier accuracy / precision / recall / FPR | 93.6% / 100% / 86.8% / 0% |
| Guard classifier latency, p50 / p99 | 7.6ms / 36.1ms |
| Security test suite (known attack patterns, not training data) | 100% recall on real attacks (15/15), 3 benign false positives |
| Kill test (real process kills, not simulated) | 100/100 runs, 0 lost, 0 duplicated approvals |
| Fault tests (real Toxiproxy network failures) | Redis loss, database failover, failing provider — all 3 passed |
| Audit verification (checkpoint fast path vs. full chain) | 1.0ms vs. 9.2ms on current chain length |
| Tool tampering detection | 100% of injected definition changes blocked |

Every one of these is a reported, reproducible result — the scripts that
produced them are in the repo (`deploy/bench/`, `deploy/fault/`,
`cmd/killtest`, `services/guard-classifier/`), not a one-off number that
can't be checked.

## Engineering decisions worth talking about

- **Fail-open vs. fail-closed, deliberately split.** Rate limiting,
  budgets, and the injection guard fail open — if their backing service
  is unreachable, log it loudly and let the request through rather than
  taking all agent traffic down over a supporting service's hiccup. The
  policy engine fails closed — it's the actual authorization boundary,
  and a gateway that quietly stops enforcing anything during an outage
  isn't a gateway. Both halves of this were proven live with real
  Toxiproxy-induced network failures, not just unit-tested.
- **Cedar over a custom policy language.** A purpose-built, formally
  analyzable authorization language, with caching, versioning and
  decision logging built around it — rather than inventing and
  maintaining a parser/evaluator for something Cedar already does well.
- **Client-supplied idempotency keys, never server-derived**, for
  approvals — the only way a retry can be reliably distinguished from a
  new, separately-approvable action.
- **EC2, not EKS, for the AWS deploy.** The EKS control plane has no
  free tier and costs money to exist whether or not anything's running
  on it. A single EC2 instance running the same containers a cluster
  would, via Docker Compose, demonstrates the deployment without a fixed
  monthly cost a demo project doesn't need — the Kubernetes-specific
  skills are already proven separately, on a real local `kind` cluster.
- **No permanent live deployment, by choice, not by gap.** The Terraform
  is real and `terraform plan`-validated end to end; I decided not to
  run it continuously. Keeping Keycloak, Postgres, Redis, Kafka and an
  ML model running 24/7 for a demo that gets looked at occasionally
  isn't what actually gets evaluated for an infra/security project —
  the repo, the documented decisions, and `docker compose up` on a call
  are. The same reasoning applies to a demo video: the fault/kill/load
  test results in BENCHMARKS.md are the real evidence; a recording of
  the same scenarios wouldn't add a claim that isn't already backed by a
  reproducible script in the repo.

## What I'd call out as honest limitations, unprompted

- The guard classifier's held-out test set includes 54 hand-written
  examples (no large public dataset of labeled tool descriptions/outputs
  exists) — real numbers, on a smaller and narrower set than "production
  traffic at scale."
- Metrics/dashboards (Prometheus/Grafana) aren't built — tracing is, and
  every number that would go on a dashboard is already reported
  somewhere in the benchmarks, but making them live and continuous is
  genuinely unfinished work, not done.
- Per-team budget/rate-limit scoping doesn't exist yet — only per-agent.
- The admin console sits behind one shared operator token, not per-admin
  login — a stated v1 simplification for a single-operator tool, not a
  production multi-tenant posture.

Each of these is tracked explicitly in `docs/MILESTONES.md`'s
spec-completeness checks, re-run at every milestone boundary — not
discovered once and forgotten, and not hidden to make the project look
more finished than it is.

## Why this project

Most portfolio projects demonstrate that code runs. This one is built to
demonstrate something narrower and harder to fake: that the hard parts
of a security-critical system — what happens when a dependency fails,
whether a crash mid-operation loses or duplicates state, whether a
classifier's accuracy claim survives contact with attack patterns it
wasn't trained on — were actually tested against reality, with the
results reported honestly whether or not they were flattering. The full
build history, including every real bug found and how it was found, is
in `DECISIONS.md`.
