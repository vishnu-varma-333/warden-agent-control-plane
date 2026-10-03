#!/usr/bin/env bash
# Fetches a real Keycloak access token for the demo agent client, for use
# by the k6 scripts in this directory (which read it from $TOKEN). Needs
# Keycloak up (docker compose -f deploy/docker/docker-compose.yml up -d).
set -euo pipefail
curl -s -X POST "http://localhost:8180/realms/warden/protocol/openid-connect/token" \
  -d "grant_type=client_credentials" -d "client_id=agent-demo" -d "client_secret=agent-demo-secret" \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['access_token'])"
