# Runbook

Operational guide for running Warden — what to check, what each failure
mode actually means, and what to do about it. Each scenario below was
reproduced for real (not just reasoned about) in `deploy/fault/
run_fault_tests.sh` and `cmd/killtest`; see BENCHMARKS.md for the results.

## Health checks

| Service | Check |
|---|---|
| gateway | `curl localhost:8080/healthz` |
| control-api | `curl localhost:8081/healthz` |
| guard classifier | `curl localhost:8000/healthz` |
| console | loads `/tools` without error |
| audit chain integrity | `wardenctl audit verify` (exit 0 = clean) |

## "Everything is denying" (policy fail-closed)

**Symptom:** every call gets 403, logs show `"no policy loaded"`.

**What it means:** the gateway has never successfully loaded a policy
from Postgres — not "Postgres is currently down" (that's a different,
more forgiving case below), but "it never got one in the first place."
This is deliberate fail-closed behavior (see DECISIONS.md), not a bug.

**Fix:** check Postgres connectivity and that the `policies` table has an
`active = true` row. `policy.SeedIfEmpty` installs a default on a
genuinely empty table, but only at startup — if the table was emptied
after the gateway already started, restart it after fixing the table.

## "Requests are slow/failing, Redis looks fine" vs. "Redis is actually down"

**If Redis is down:** rate limiting and budget enforcement fail OPEN —
requests keep succeeding, not failing. Logs show `"rate limit check
failed, allowing request"` / `"budget check failed, allowing request"`.
This is intentional (see ARCHITECTURE.md's fail-open/fail-closed split);
it means your rate limits and budgets are temporarily not enforced, not
that anything is broken. Fix Redis, no restart needed — reconnects
automatically.

**If requests are actually failing:** that's not a Redis problem (Redis
loss doesn't produce failures, by design — see above). Check the policy
engine (fail-closed) or the actual provider/upstream.

## Postgres becomes unreachable mid-operation

**What still works:** the gateway keeps serving model/tool calls using
its last successfully loaded policy — `Authorize()` reads from an
in-memory compiled policy, not a live DB call per request. Verified live
in `run_fault_tests.sh`'s scenario 2.

**What stops working:** anything that needs a *write* — new approvals,
tool registry updates, audit checkpointing, the periodic policy refresh
(so a policy change made during the outage won't take effect until
Postgres is back). Logs show `"policy periodic refresh failed"`, `"mcp
tool reconcile failed"`, `"approval expiry sweep failed"` — all loud, all
expected during an outage, none of them crash the process.

**Fix:** restore Postgres connectivity. No gateway restart needed —
confirmed recovery within one refresh cycle (10s) in the same fault test.

## A provider is down or slow

The router (`internal/router`) + circuit breaker (`internal/breaker`)
handle this automatically: a failing primary provider falls back to the
next one in its route's provider list. No operator action needed unless
every provider in a route is down, in which case that model alias starts
returning errors — check provider status directly.

## A tool's definition changed (registry shows "changed")

**What it means:** an upstream MCP server's tool definition hash no
longer matches what was pinned — either a legitimate update or tool
poisoning. The tool is blocked (calls return "unknown tool") until a
human reviews it.

**Fix:** open the console's Tools view, inspect the pending hash, and
either approve it (if the change is legitimate) or investigate the
upstream server (if not). There is no automatic "revert and keep
working" path — that's deliberate (see `internal/registry`'s doc
comments): an attacker alternating between the real and poisoned
definition shouldn't be able to "heal" the block by reverting before
anyone looks.

## A pending approval needs attention

Console → Approvals → Pending. Approving/rejecting a crashed-and-restarted
gateway's pending approval is safe — approval state lives in Postgres,
not gateway memory (proven with 100 real `kill -9` cycles, see
BENCHMARKS.md's kill-test section). An agent's retry after the gateway
comes back executes exactly once, whether the retry happens before or
after a restart.

## Rotating the admin token

```bash
# generates a new ADMIN_TOKEN (and a new audit signing key — see its own
# warning about checkpoint continuity) into deploy/docker/.env
deploy/docker/generate_env_secrets.sh
docker compose -f deploy/docker/docker-compose.prod.yml --env-file deploy/docker/.env up -d control-api console gateway
```

Update the console's own `ADMIN_TOKEN` env var to match, or it'll start
failing every request with 401 — see `console/README.md`.

## Classifier (guard) is down or OOM-killed

Fails open — tool descriptions/outputs stop being scanned, but calls
still succeed (logged, not silent). Restart the `guard-classifier`
container/process; no gateway restart needed, it reconnects per-call.

## Restarting any single service

Every piece of durable state lives in Postgres, not in any process's
memory, by design. Restarting the gateway, control-api, or the classifier
individually is always safe — there is no "warm cache" or in-memory
queue that a restart would lose. The one exception: an audit checkpoint
signing key generated fresh on restart (no `WARDEN_AUDIT_SIGNING_KEY`
set) breaks continuity with checkpoints signed before that restart — see
that env var's own warning in `cmd/gateway/main.go`.
