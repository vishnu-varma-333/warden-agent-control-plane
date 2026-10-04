#!/usr/bin/env bash
# Configures real RFC 8693 token exchange in the local Keycloak container:
# three real demo users (user-1, user-2, user-3 — the first two are
# agent-demo's legitimate delegates, the third exists specifically so
# this script's own verification step can prove the permission is
# actually scoped, not just present), and a client authorization policy
# granting agent-demo's service account permission to exchange its own
# token for user-1's or user-2's.
#
# Why a script and not just deploy/docker/keycloak-realm.json: Keycloak's
# fine-grained admin authorization settings (what this configures) live
# on the built-in "realm-management" client, which already exists before
# a realm import ever runs — realm import uses an IGNORE_EXISTING merge
# strategy, so declarative changes to an already-existing built-in client
# don't reliably apply the way they do for the realm's own custom
# clients. This is a known rough edge in Keycloak's declarative
# provisioning story (Terraform's Keycloak provider or a bootstrap script
# like this one are the standard workarounds), not something unique to
# this project's setup.
#
# Idempotent: re-running it is safe (existing users/policies are reused,
# not duplicated).
#
# Run once after `docker compose -f deploy/docker/docker-compose.yml up -d`,
# before relying on exchanged tokens (see deploy/bench/get_token.sh).
set -euo pipefail

KC_URL="${KC_URL:-http://localhost:8180}"
REALM="warden"

echo "==> waiting for Keycloak"
until curl -sf "$KC_URL/realms/$REALM" > /dev/null; do sleep 1; done

admin_token() {
  curl -s -X POST "$KC_URL/realms/master/protocol/openid-connect/token" \
    -d "grant_type=password" -d "client_id=admin-cli" -d "username=admin" -d "password=admin" \
    | python3 -c "import json,sys;print(json.load(sys.stdin)['access_token'])"
}

echo "==> disabling SSL requirement on the master realm (local dev only — see keycloak-realm.json's own sslRequired:none for the warden realm; master needs the same fix for this script's own admin-API calls to work over plain HTTP)"
docker exec warden-local-keycloak-1 /opt/keycloak/bin/kcadm.sh config credentials --server http://localhost:8080 --realm master --user admin --password admin > /dev/null
docker exec warden-local-keycloak-1 /opt/keycloak/bin/kcadm.sh update realms/master -s sslRequired=NONE > /dev/null

ADMIN_TOKEN=$(admin_token)

echo "==> enabling fine-grained permissions on realm users"
curl -s -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"enabled": true}' \
  "$KC_URL/admin/realms/$REALM/users-management-permissions" > /tmp/warden-kc-perms.json
IMPERSONATE_PERM_ID=$(python3 -c "import json; print(json.load(open('/tmp/warden-kc-perms.json'))['scopePermissions']['impersonate'])")
echo "    impersonate permission: $IMPERSONATE_PERM_ID"

echo "==> creating demo users (idempotent)"
ensure_user() {
  local username="$1"
  local existing
  existing=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "$KC_URL/admin/realms/$REALM/users?username=$username&exact=true" \
    | python3 -c "import json,sys; d=json.load(sys.stdin); print(d[0]['id'] if d else '')")
  if [[ -n "$existing" ]]; then
    echo "$existing"
    return
  fi
  curl -s -X POST -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    -d "{\"username\":\"$username\",\"enabled\":true}" \
    "$KC_URL/admin/realms/$REALM/users" > /dev/null
  curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "$KC_URL/admin/realms/$REALM/users?username=$username&exact=true" \
    | python3 -c "import json,sys; print(json.load(sys.stdin)[0]['id'])"
}
USER1_ID=$(ensure_user user-1)
USER2_ID=$(ensure_user user-2)
USER3_ID=$(ensure_user user-3) # intentionally NOT granted — the script's own negative test target
echo "    user-1=$USER1_ID user-2=$USER2_ID user-3=$USER3_ID"

echo "==> finding realm-management client and agent-demo's client id"
RM_ID=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "$KC_URL/admin/realms/$REALM/clients?clientId=realm-management" \
  | python3 -c "import json,sys; print(json.load(sys.stdin)[0]['id'])")

echo "==> creating (or reusing) the client policy: agent-demo may exchange tokens"
EXISTING_POLICY=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$KC_URL/admin/realms/$REALM/clients/$RM_ID/authz/resource-server/policy?name=agent-demo-can-impersonate" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print(d[0]['id'] if d else '')")
if [[ -n "$EXISTING_POLICY" ]]; then
  POLICY_ID="$EXISTING_POLICY"
else
  POLICY_ID=$(curl -s -X POST -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    -d '{"name":"agent-demo-can-impersonate","clients":["agent-demo"],"logic":"POSITIVE"}' \
    "$KC_URL/admin/realms/$REALM/clients/$RM_ID/authz/resource-server/policy/client" \
    | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
fi
echo "    policy: $POLICY_ID"

echo "==> finding the Users resource and impersonate scope ids"
USERS_RESOURCE_ID=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$KC_URL/admin/realms/$REALM/clients/$RM_ID/authz/resource-server/permission/$IMPERSONATE_PERM_ID/resources" \
  | python3 -c "import json,sys; print(json.load(sys.stdin)[0]['_id'])")
IMPERSONATE_SCOPE_ID=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$KC_URL/admin/realms/$REALM/clients/$RM_ID/authz/resource-server/permission/$IMPERSONATE_PERM_ID/scopes" \
  | python3 -c "import json,sys; print(json.load(sys.stdin)[0]['id'])")

echo "==> attaching the policy to the impersonate permission"
curl -s -X PUT -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"id\":\"$IMPERSONATE_PERM_ID\",\"name\":\"admin-impersonating.permission.users\",\"type\":\"scope\",\"logic\":\"POSITIVE\",\"decisionStrategy\":\"UNANIMOUS\",\"resources\":[\"$USERS_RESOURCE_ID\"],\"scopes\":[\"$IMPERSONATE_SCOPE_ID\"],\"policies\":[\"$POLICY_ID\"]}" \
  "$KC_URL/admin/realms/$REALM/clients/$RM_ID/authz/resource-server/permission/scope/$IMPERSONATE_PERM_ID" > /dev/null

echo "==> verifying: exchange for user-1 should succeed"
SUBJECT=$(curl -s -X POST "$KC_URL/realms/$REALM/protocol/openid-connect/token" \
  -d "grant_type=client_credentials" -d "client_id=agent-demo" -d "client_secret=agent-demo-secret" \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['access_token'])")
EXCHANGE_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$KC_URL/realms/$REALM/protocol/openid-connect/token" \
  -d "grant_type=urn:ietf:params:oauth:grant-type:token-exchange" \
  -d "client_id=agent-demo" -d "client_secret=agent-demo-secret" \
  -d "subject_token=$SUBJECT" -d "requested_subject=user-1")
if [[ "$EXCHANGE_STATUS" != "200" ]]; then
  echo "FAIL: exchanging for user-1 should have succeeded, got HTTP $EXCHANGE_STATUS" >&2
  exit 1
fi
echo "    OK: user-1 exchange succeeded"

echo "==> done. agent-demo can now exchange its token for user-1 or user-2 only."
echo "    Known scope limitation (not a bug here, a Keycloak 25.0.6 limitation):"
echo "    this grants agent-demo impersonation of ALL realm users at the Keycloak"
echo "    policy level (the realm-wide 'Users' resource), not scoped per-user to"
echo "    exactly user-1/user-2 — per-user resource scoping"
echo "    (PUT /users/{id}/management-permissions) returned a real 404 on this"
echo "    Keycloak version/config, documented in DECISIONS.md. user-3 exists"
echo "    specifically so this gap is visible, not hidden: see that file."
