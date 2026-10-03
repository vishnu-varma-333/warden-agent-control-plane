#!/usr/bin/env bash
# Sweeps request rate and reports p99 added latency at each, to find where
# it breaches the 15ms SLO — the spec's "throughput per instance" metric.
# Needs the gateway running with rate limit/budget raised out of the way
# (same reasoning as chat_latency.js — see its header), e.g.:
#   RATE_LIMIT_PER_MINUTE=1000000 BUDGET_LIMIT_PER_HOUR=1000000 go run ./cmd/gateway
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

RATES=(200 500 1000 2000 3000 4000 5000)
TOKEN=$(deploy/bench/get_token.sh)

echo "rate(req/s) | p99 added latency | failed requests"
echo "---|---|---"
for rate in "${RATES[@]}"; do
  out=$(k6 run --summary-trend-stats="p(99)" -e TOKEN="$TOKEN" -e RATE="$rate" deploy/bench/throughput_ramp.js 2>&1)
  p99=$(echo "$out" | grep "http_req_duration" | head -1 | grep -oE 'p\(99\)=[0-9.]+(µs|ms|s)')
  failed=$(echo "$out" | grep "http_req_failed" | grep -oE '[0-9]+\.[0-9]+%' | head -1)
  echo "$rate | $p99 | $failed"
done
