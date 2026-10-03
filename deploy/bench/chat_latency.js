// First latency benchmark (milestone 2): "added gateway latency" with the
// provider mocked — the mock provider responds with zero artificial delay,
// so everything k6 measures here IS the gateway's own overhead (JSON
// decode, cache lookup, routing, breaker checks, response encode), not a
// real provider's network time. Run: k6 run deploy/bench/chat_latency.js
import http from "k6/http";
import { check } from "k6";

export const options = {
  scenarios: {
    fixed_rate: {
      executor: "constant-arrival-rate",
      rate: 200,            // requests per second
      timeUnit: "1s",
      duration: "20s",
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    // Spec's starting target: p99 added latency under 15ms.
    http_req_duration: ["p(99)<15"],
  },
};

export default function () {
  // A unique message per request defeats the exact-match cache on purpose —
  // this benchmark measures the uncached (worst-case routing+breaker) path,
  // not the cache-hit fast path, which would trivially be near-zero.
  const body = JSON.stringify({
    model: "mock-model",
    messages: [{ role: "user", content: `bench-${__VU}-${__ITER}-${Date.now()}` }],
  });

  const res = http.post("http://localhost:8080/v1/chat/completions", body, {
    headers: { "Content-Type": "application/json" },
  });

  check(res, { "status is 200": (r) => r.status === 200 });
}
