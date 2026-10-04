#!/usr/bin/env bash
# Fetches a real token for the demo agent, already exchanged (RFC 8693)
# for a specific acting-as user — what every Warden caller presents now,
# not a plain client-credentials token plus a self-asserted X-Acting-As
# header (that was the old design; see internal/identity's doc comments
# for the full history and why it changed).
#
# Needs Keycloak up AND deploy/docker/setup_token_exchange.sh already run
# once (it configures the actual permission that makes this exchange
# succeed — without it, Keycloak correctly rejects the exchange).
#
# Usage: deploy/bench/get_token.sh [acting-as-user]   (default: user-1)
set -euo pipefail

ACTING_AS="${1:-user-1}"

SUBJECT_TOKEN=$(curl -s -X POST "http://localhost:8180/realms/warden/protocol/openid-connect/token" \
  -d "grant_type=client_credentials" -d "client_id=agent-demo" -d "client_secret=agent-demo-secret" \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['access_token'])")

curl -s -X POST "http://localhost:8180/realms/warden/protocol/openid-connect/token" \
  -d "grant_type=urn:ietf:params:oauth:grant-type:token-exchange" \
  -d "client_id=agent-demo" -d "client_secret=agent-demo-secret" \
  -d "subject_token=$SUBJECT_TOKEN" -d "requested_subject=$ACTING_AS" \
  | python3 -c "
import json, sys
d = json.load(sys.stdin)
if 'access_token' not in d:
    print(f'token exchange failed: {d}', file=sys.stderr)
    sys.exit(1)
print(d['access_token'])
"
