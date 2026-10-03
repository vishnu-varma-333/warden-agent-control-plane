---
---

# Architecture

## Request path

Every agent call — a model completion or an MCP tool call — goes through
the gateway (`cmd/gateway`), which runs these checks in order:

1. **Identity** (`internal/identity`): verifies the agent's OAuth access
   token against Keycloak's JWKS, extracts the agent's identity and its
   `X-Acting-As` allowlist. Both identities — the agent and the human it
   acts for — are required on every call.
2. **Rate limit / budget** (`internal/ratelimit`, `internal/budget`):
   distributed, Redis-backed, atomic via Lua scripts. Fails **open** on a
   Redis error — see "Fail-open vs. fail-closed" below.
3. **Policy** (`internal/policy`): a Cedar policy, compiled in memory,
   evaluated against (principal, action, resource, context). Decisions
   are cached in Redis keyed on the active policy version, so a version
   bump invalidates every cached decision for free. Fails **closed** if
   no policy has ever loaded.
4. **Injection guard** (`internal/guard`, only on the MCP tool path): a
   gRPC call to the guard classifier service, scanning tool descriptions
   before they're ever exposed and tool outputs before they reach the
   agent. Fails **open** on an unreachable classifier, under a strict
   latency budget (50ms timeout; measured p99 ~36ms — see BENCHMARKS.md).
5. **Approval** (`internal/approval`, only for actions an `approval_rules`
   row names): pauses the call, durably, until a human decides via the
   console or control-api. Survives a gateway crash — the call's state
   lives in Postgres, not gateway memory.
6. **Proxy**: the call reaches the real model provider or the real
   upstream MCP server.

Every decision (allow/deny, which policy, cache hit or not) is published
to Kafka/Redpanda off the hot path, and a single consumer
(`internal/audit`'s `ChainWriter`) appends it to a SHA-256 hash chain in
Postgres — tamper-evident, not just logged.

## Processes

| Process | Language | Role |
|---|---|---|
| `cmd/gateway` | Go | hot path — everything above |
| `cmd/control-api` | Go | admin API behind the console: tools, policies, approvals, audit, spend |
| `console/` | TypeScript (Next.js) | the admin UI itself |
| `services/guard-classifier` | Python | the injection/poisoning classifier, served over gRPC (hot path) and FastAPI (debugging) |
| `cmd/demo-mcp-server` | Go | a real MCP server standing in for a third-party one, used in local dev and the demo |
| `cmd/wardenctl` | Go | operator CLI — `audit verify` |

## State

- **PostgreSQL**: every durable record — tools (registry), policies,
  approvals, the audit chain and its checkpoints. Transactions matter
  here (e.g. the policy table's partial unique index enforcing exactly
  one active version; the audit chain's row-locked singleton serializing
  appends).
- **Redis**: everything derived and safe to lose — rate limit counters,
  budget counters, the policy decision cache. Nothing here is the system
  of record; a flushed Redis just means degraded performance until it
  refills, not lost state.
- **Kafka (Redpanda)**: the audit event stream, decoupling "a decision
  happened" from "it's durably chained," so the hot path never blocks on
  the chain write.

## Fail-open vs. fail-closed

Two different failure philosophies apply to two different kinds of
checks, deliberately:

- **Fail open** (rate limit, budget, injection guard): these are
  *mitigations*, not the core authorization boundary. If Redis or the
  classifier is unreachable, Warden logs it loudly and lets the request
  through rather than taking all agent traffic down because a supporting
  service hiccuped.
- **Fail closed** (policy engine): this *is* the authorization boundary.
  If no policy has ever successfully loaded, every request is denied —
  an authorization gateway that quietly stops enforcing anything during
  an outage isn't a gateway, it's a bypass. Once a policy HAS loaded,
  the gateway keeps enforcing that last-known-good version even if
  Postgres becomes unreachable afterward (proven live — see
  `deploy/fault/run_fault_tests.sh` and BENCHMARKS.md's fault-test
  section), rather than either blocking everything or falling back to
  allow-all the moment the database hiccups.

Each specific decision — not just this summary — is recorded with its
reasoning in `DECISIONS.md`.

## Why no custom policy language, no custom audit format, etc.

The recurring design principle: build the thing that's actually specific
to this problem (the authorization gateway, the tool registry, the
approval state machine), and use a purpose-built tool for everything
that isn't (Cedar for policy, standard OAuth/OIDC for identity, the
official MCP SDK for the agent protocol, ONNX for model serving). See
`DECISIONS.md` for the specific trade-offs considered for each.
