---
---

# Security model

## Threat model

Warden assumes: agents can be tricked by prompt injection; MCP tools can
be poisoned or silently changed after being trusted once; an agent or the
human it acts for can exceed what they should be allowed to do; and any
of the supporting infrastructure (Redis, the classifier, even Postgres)
can fail without that failure becoming a silent bypass of the actual
authorization decision. Each of these has a specific, tested mechanism —
not a general "we have auth" claim:

| Threat | Mechanism | Where |
|---|---|---|
| Agent exceeds its authority | Cedar policy, evaluated per call, fail-closed | `internal/policy` |
| Prompt injection via tool output | Guard classifier scans every tool output before it reaches the agent | `internal/guard`, `internal/mcpgateway` |
| Tool poisoning (silent definition change) | Hash-pinned on first sight; any change blocks the tool until a human re-approves | `internal/registry` |
| Agent impersonation / confused deputy | Every call carries both the agent's own identity and the human it acts for, as two separate, independently verified claims | `internal/identity` |
| Runaway cost / abuse | Distributed rate limits and budgets, atomic across instances | `internal/ratelimit`, `internal/budget` |
| A risky action happening without a human signing off | Durable approval gate, independent of policy — a call can be policy-permitted and still require a human | `internal/approval` |
| "What actually happened" being disputable after the fact | Hash-chained, checkpoint-signed audit log; a verification command proves — not just claims — nothing was edited, deleted, or reordered | `internal/audit` |

## Identity

OAuth 2.1 / OIDC via Keycloak. Short-lived access tokens, verified against
Keycloak's JWKS on every call (`internal/identity`). The agent's own
identity (`azp` claim) and the human it's allowed to act as (a hardcoded
allowlist claim in v1 — see DECISIONS.md on why real RFC 8693 token
exchange is a deliberate v1 simplification, not forgotten) are both
required; a call carrying only one is rejected (400/401), not silently
defaulted.

## Authorization

Cedar policies (`internal/policy`), versioned, with exactly one active
version at a time enforced at the database level (a partial unique index,
not just application logic). Every decision — allow or deny — is logged
with its reasons and published to the audit chain. Fails closed: see
ARCHITECTURE.md.

## Secrets

**v1 state, stated plainly:** local dev secrets (Postgres password,
Keycloak client secret, the demo `ADMIN_TOKEN`) are in plaintext env vars
and a checked-in realm config meant only for local Keycloak — this is
explicitly a local-dev posture, not a production one. A real deployment
needs these in a real secrets manager (AWS Secrets Manager, per the
spec) with IAM-scoped access, not env vars baked into a compose file.
`deploy/docker/generate_env_secrets.sh` at least generates random,
non-default secrets per deployment rather than shipping one hardcoded
value everywhere — tracked as the next real step, not treated as done.

## Admin surface (console / control-api)

control-api is gated by a single shared bearer token
(`internal/httpapi/adminauth.go`), not per-admin OAuth — a deliberate v1
simplification for a single-operator, trusted-network-boundary tool, not
a public-internet admin panel. See DECISIONS.md and `console/README.md`
for the full reasoning and what a real multi-admin deployment would need
instead (the gateway's own OAuth flow, reused for the console).

## Fail-open vs. fail-closed

Covered in full in ARCHITECTURE.md and DECISIONS.md — the short version:
mitigations (rate limit, budget, the injection guard) fail open and log
loudly; the actual authorization boundary (the policy engine) fails
closed. This split is deliberate, not inconsistent — conflating "a
convenience check's dependency is down" with "the authorization decision
itself can't be trusted" would be the real security bug.

## What's explicitly NOT done yet

Tracked in `docs/MILESTONES.md`'s spec-completeness section, not hidden:
TLS (local dev and the single-VM demo are plain HTTP — a real deployment
needs TLS terminated somewhere, e.g. at a load balancer or via Caddy/
nginx on the VM); a security test suite beyond the one in
`services/guard-classifier/security_test.py` (known injection/poisoning
patterns against the classifier specifically — load/fault/kill tests
cover the rest of the system, see BENCHMARKS.md); DB-level write
prevention on the audit table (the hash chain's job is *detection*,
which is what the spec actually asks for — prevention via a trigger or
REVOKE is a complementary hardening layer, not yet added).
