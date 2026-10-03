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

## Audit verification (milestone 7)

**Spec target:** "Audit verification — time to verify N million records —
report the number."

**Result (2026-10-03), at today's real scale (5 records, live dev
chain):**

| Verification | Records checked | Time |
|---|---|---|
| Full (from genesis) | 5 | 9.2 ms |
| Fast path (from latest signed checkpoint) | 1 | 1.0 ms |

**What this actually demonstrates:** not a meaningful N-million-record
number yet (there's no production traffic generating millions of
decisions), but the *mechanism* the spec's benchmark is really asking
about — that checkpoint-based verification is a real optimization, not
just a design on paper. The fast path correctly verified only the record
*after* the latest checkpoint instead of re-walking the whole chain, after
first verifying the checkpoint's Ed25519 signature. Both the full-chain
tamper detection (edited/deleted/reordered records, each independently
unit-tested) and this checkpoint fast path were also proven live against
a running gateway, not just in isolated tests — see DECISIONS.md for the
three real bugs that live run surfaced along the way.

**Honest scope note:** a real "N million records, how long does
verification take" number needs either a synthetic load generator seeding
millions of rows, or real accumulated production traffic — both belong in
the milestone 10 benchmark pass, tracked explicitly rather than
extrapolated from 5 records here.
