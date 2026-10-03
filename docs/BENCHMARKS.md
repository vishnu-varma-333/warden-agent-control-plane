# Benchmarks

Each entry: what was measured, how, the result, and the setup — per the
spec, these are reported numbers, not estimates.

## Added gateway latency (milestone 2)

**Setup:** gateway running locally (`go build` binary, not `go run`),
Redis from the docker-compose stack, provider mocked with zero artificial
delay (isolates the gateway's own overhead from any real provider's network
time, per the spec's methodology). Load generated with k6
(`deploy/bench/chat_latency.js`), constant-arrival-rate executor, 200 req/s
for 20s, each request using a unique message so it always takes the
uncached routing+breaker path (a cache hit would trivially read near-zero
and hide the number this benchmark is actually for).

**Result (2026-10-03):**

| Metric | Value |
|---|---|
| Requests | 4001 |
| Failures | 0 (0.00%) |
| p90 | 2.62 ms |
| p95 | 2.91 ms |
| **p99** | **4.49 ms** |
| max | 20.46 ms |

**Target:** p99 under 15ms. **Met**, with headroom (4.49ms vs. 15ms).

**Caveat to say out loud in an interview:** this measures the gateway over
loopback HTTP with a mocked, zero-latency provider and no policy engine, no
auth, and no real tool/model backend in front of it yet — it's the floor for
"how much does Warden's own plumbing cost," not a claim about end-to-end
latency once the policy engine (milestone 5) and injection guard (milestone
8) are in the request path. Re-run and update this table once those land.

## Approval durability (milestone 6)

**Spec target:** "Kill tests during pending approvals — 0 lost or
duplicated approvals across 1,000 runs."

**What was actually done (2026-10-03):** one deliberate, fully-reasoned
kill test proving the mechanism is correct, not a 1,000-run statistical
soak test — that belongs in the full benchmark pass alongside the other
fault/load tests (see `docs/MILESTONES.md`'s spec-completeness tracking;
it's an explicit open item, not forgotten). Sequence:

1. Called `delete_data` (an approval-required tool) with an explicit
   idempotency key. Confirmed it paused, and the approval row existed in
   Postgres as `pending`.
2. `kill -9` the gateway process mid-wait (a real crash, not a graceful
   shutdown) — confirmed the process was dead, then re-checked the
   approval row: still `pending`, completely unaffected. **Zero loss.**
3. Restarted the gateway, approved the pending approval via the
   control-api endpoint.
4. Agent retried the exact same call (same idempotency key): executed
   successfully, no re-prompt for approval, `executed_at` set.
   Confirmed via the downstream tool's own call counter: exactly 1 real
   execution.
5. Agent retried again (same key, now already executed): gateway
   returned "already executed, not repeating" **without calling the
   downstream tool a second time.** Confirmed via the same counter: still
   exactly 1. **Zero duplication.**

**Result:** both halves of the spec's target demonstrated correct — 0
lost, 0 duplicated — across this test. A statistical 1,000-run version of
the same scenario (automated, looped) is tracked as a follow-up for the
milestone 10 benchmark pass, where it belongs alongside the other
fault-injection suites.
