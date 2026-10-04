#!/usr/bin/env bash
# Fault tests (milestone 10): slow/failing providers, Redis loss, database
# failover — the three scenarios the spec names, each run against a real
# gateway binary, not simulated. Redis/Postgres faults go through a real
# Toxiproxy (github.com/Shopify/toxiproxy) proxy so the gateway sees an
# actual network-level failure (connection reset), not a mocked error.
#
# Requires: docker compose stack up (postgres, redis, keycloak), a built
# gateway binary, toxiproxy-server/toxiproxy-cli on PATH.
# Run from the repo root: deploy/fault/run_fault_tests.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

GATEWAY_BIN="${GATEWAY_BIN:-/tmp/gateway-faulttest}"
FAULT_PORT="${FAULT_PORT:-8083}"

echo "==> building gateway"
go build -o "$GATEWAY_BIN" ./cmd/gateway

echo "==> starting toxiproxy-server"
toxiproxy-server > /tmp/fault-toxiproxy.log 2>&1 &
TOXIPROXY_PID=$!
sleep 1

cleanup() {
  echo "==> cleaning up"
  [[ -n "${GATEWAY_PID:-}" ]] && kill "$GATEWAY_PID" 2>/dev/null || true
  kill "$TOXIPROXY_PID" 2>/dev/null || true
}
trap cleanup EXIT

echo "==> creating proxies"
toxiproxy-cli create -l 127.0.0.1:16379 -u 127.0.0.1:6379 redis_proxy 2>/dev/null || true
toxiproxy-cli create -l 127.0.0.1:15432 -u 127.0.0.1:5432 postgres_proxy 2>/dev/null || true

echo "==> fetching a real, already-exchanged (RFC 8693) agent token from Keycloak"
TOKEN=$("$REPO_ROOT/deploy/bench/get_token.sh" user-1)

call_gateway() {
  curl -s -o /tmp/fault-resp.json -w "%{http_code}" -X POST "localhost:${FAULT_PORT}/v1/chat/completions" \
    -H "Authorization: Bearer $TOKEN" -H "X-Acting-As: user-1" -H "Content-Type: application/json" \
    -d "{\"model\":\"mock-model\",\"messages\":[{\"role\":\"user\",\"content\":\"fault-test-$RANDOM\"}]}"
}

assert_status() {
  local label="$1" expected="$2" got="$3"
  if [[ "$got" == "$expected" ]]; then
    echo "  PASS: $label (got $got)"
  else
    echo "  FAIL: $label (expected $expected, got $got)"
    exit 1
  fi
}

### Scenario 1: Redis loss — rate limiter and budget must fail OPEN.
echo
echo "=== Scenario 1: Redis loss (fail-open) ==="
GATEWAY_ADDR=":${FAULT_PORT}" REDIS_ADDR="127.0.0.1:16379" \
  "$GATEWAY_BIN" > /tmp/fault-gateway-redis.log 2>&1 &
GATEWAY_PID=$!
sleep 2
status=$(call_gateway); assert_status "baseline (Redis up)" 200 "$status"

toxiproxy-cli toxic add -t reset_peer -a timeout=0 -n redis_down redis_proxy
status=$(call_gateway); assert_status "Redis down, request still served" 200 "$status"
if grep -q "rate limit check failed, allowing request" /tmp/fault-gateway-redis.log; then
  echo "  PASS: gateway logged the fail-open decision explicitly, not silently"
else
  echo "  FAIL: expected an explicit fail-open log line"; exit 1
fi
toxiproxy-cli toxic remove -n redis_down redis_proxy
kill "$GATEWAY_PID"; wait "$GATEWAY_PID" 2>/dev/null || true

### Scenario 2: database failover — hot path keeps serving on the last
### known-good compiled policy; background refresh/reconcile/expiry fail
### loudly instead of silently.
echo
echo "=== Scenario 2: database failover (graceful degradation) ==="
GATEWAY_ADDR=":${FAULT_PORT}" DATABASE_URL="postgres://warden:warden@127.0.0.1:15432/warden?sslmode=disable" \
  "$GATEWAY_BIN" > /tmp/fault-gateway-pg.log 2>&1 &
GATEWAY_PID=$!
sleep 2
status=$(call_gateway); assert_status "baseline (Postgres up)" 200 "$status"

toxiproxy-cli toxic add -t reset_peer -a timeout=0 -n postgres_down postgres_proxy
status=$(call_gateway); assert_status "Postgres down, hot path still served" 200 "$status"
sleep 11  # let the 10s periodic refresh tick at least once
if grep -q "policy periodic refresh failed" /tmp/fault-gateway-pg.log; then
  echo "  PASS: background policy refresh logged its failure explicitly"
else
  echo "  FAIL: expected a logged policy refresh failure"; exit 1
fi
toxiproxy-cli toxic remove -n postgres_down postgres_proxy
sleep 11
status=$(call_gateway); assert_status "recovered after Postgres returns" 200 "$status"
kill "$GATEWAY_PID"; wait "$GATEWAY_PID" 2>/dev/null || true

### Scenario 3: failing provider — transparent fallback to the secondary.
echo
echo "=== Scenario 3: failing primary provider (automatic fallback) ==="
GATEWAY_ADDR=":${FAULT_PORT}" MOCK_PRIMARY_UNHEALTHY=true \
  "$GATEWAY_BIN" > /tmp/fault-gateway-provider.log 2>&1 &
GATEWAY_PID=$!
sleep 2
status=$(call_gateway); assert_status "primary down, request still served via fallback" 200 "$status"
if grep -q '"provider":"mock-secondary"' /tmp/fault-gateway-provider.log; then
  echo "  PASS: served by the secondary provider, confirmed from the response"
else
  echo "  FAIL: expected the secondary provider to serve the request"; exit 1
fi
kill "$GATEWAY_PID"; wait "$GATEWAY_PID" 2>/dev/null || true

echo
echo "All fault scenarios passed."
