#!/usr/bin/env bash
# Generates the secrets docker-compose.prod.yml requires and writes them to
# deploy/docker/.env (gitignored): ADMIN_TOKEN (control-api/console shared
# bearer token) and an Ed25519 keypair for audit checkpoint signing
# (WARDEN_AUDIT_SIGNING_KEY is the seed the gateway signs with;
# WARDEN_AUDIT_PUBLIC_KEY is what control-api verifies against — see
# loadOrGenerateAuditSigningKey in cmd/gateway/main.go).
#
# Run once before the first deploy. Re-running overwrites the file, which
# invalidates any checkpoints already signed with the old key — don't
# re-run against a running deployment with real history.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

ADMIN_TOKEN=$(openssl rand -hex 16)

read -r SEED PUBLIC < <(go run ./cmd/genkey)

cd deploy/docker

cat > .env <<EOF
ADMIN_TOKEN=$ADMIN_TOKEN
WARDEN_AUDIT_SIGNING_KEY=$SEED
WARDEN_AUDIT_PUBLIC_KEY=$PUBLIC
EOF

echo "Wrote deploy/docker/.env"
echo "ADMIN_TOKEN=$ADMIN_TOKEN"
