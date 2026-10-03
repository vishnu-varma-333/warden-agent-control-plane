// Throughput per instance (milestone 10): run this once per rate (driven
// by run_throughput.sh, which sweeps RATE across several values) rather
// than one continuous ramp — a discrete sweep gives a clean per-rate p99
// instead of one blended number that hides which stage actually breached
// the SLO. Per the spec: "Requests per second before p99 breaches the
// SLO. Report the number and the hardware."
// Run via: deploy/bench/run_throughput.sh (not directly)
import http from "k6/http";
import { check } from "k6";

const TOKEN = __ENV.TOKEN;
const RATE = parseInt(__ENV.RATE || "200", 10);
if (!TOKEN) {
  throw new Error("missing TOKEN env");
}

export const options = {
  scenarios: {
    fixed_rate: {
      executor: "constant-arrival-rate",
      rate: RATE,
      timeUnit: "1s",
      duration: "10s",
      preAllocatedVUs: Math.min(RATE, 500),
      maxVUs: Math.max(RATE, 500),
    },
  },
};

export default function () {
  const body = JSON.stringify({
    model: "mock-model",
    messages: [{ role: "user", content: `ramp-${__VU}-${__ITER}-${Date.now()}` }],
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
