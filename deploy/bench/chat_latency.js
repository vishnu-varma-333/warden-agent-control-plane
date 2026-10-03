// Added gateway latency, re-run for milestone 10 now that identity
// (milestone 3) and the policy engine (milestone 5) are both in this
// request's path (the original milestone 2 version of this script
// predates both and would now get 401s — it never carried a token or an
// X-Acting-As header, since neither was enforced yet). The guard
// classifier (milestone 8) is NOT in this path — it only scans MCP tool
// descriptions/outputs, not /v1/chat/completions — so this still isolates
// gateway + identity + policy overhead, not guard latency (that's
// benchmarked separately against the classifier itself).
//
// The provider is still mocked with zero artificial delay, so this still
// measures the gateway's own overhead, not a real provider's network
// time. Run: TOKEN=$(deploy/bench/get_token.sh) k6 run -e TOKEN=$TOKEN deploy/bench/chat_latency.js
import http from "k6/http";
import { check } from "k6";

const TOKEN = __ENV.TOKEN;
if (!TOKEN) {
  throw new Error("set -e TOKEN=$(deploy/bench/get_token.sh)");
}

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
    headers: {
      "Content-Type": "application/json",
      "Authorization": `Bearer ${TOKEN}`,
      "X-Acting-As": "user-1",
    },
  });

  check(res, { "status is 200": (r) => r.status === 200 });
}
