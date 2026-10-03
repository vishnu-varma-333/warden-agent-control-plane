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
