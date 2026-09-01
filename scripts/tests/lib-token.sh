#!/usr/bin/env bash
# Shared helpers for obtaining real tokens from the running Keycloak.
#
# Everything the end-to-end suites send is a token the identity provider
# actually minted. A hand-rolled JWT would prove only that the tests can build
# one; these prove that a login, a signature, an audience and a role claim line
# up all the way from Keycloak to a Cerbos decision.
#
# Source this file; it defines KEYCLOAK_URL, REALM and the helpers below.

KEYCLOAK_URL="${KEYCLOAK_URL:-http://127.0.0.1:${KEYCLOAK_PORT:-8081}}"
REALM="${IDP_REALM:-tenant-a}"
DEMO_PASSWORD="${KEYCLOAK_DEMO_PASSWORD:-demo-password}"

# organization_for_realm <realm>
# Echoes the demo organization alias a realm's members belong to (issue
# #78): the hospital now comes only from a real, Keycloak-confirmed
# organization claim, never a free-form attribute, so every token this
# suite mints for a realm that has one requests it. Empty means the realm
# is a hostile fixture (other-issuer) with nothing to gain from one: its
# tests only care that the issuer check refuses it.
organization_for_realm() {
  case "$1" in
    tenant-a) echo "north-hospital" ;;
    tenant-b) echo "hospital-b1" ;;
    *) echo "" ;;
  esac
}

# token_for <username> [client-id] [realm]
# Echoes an access token, or exits non-zero with the provider's message.
#
# Every grant against a realm with a demo organization requests its scope
# (issue #78). A principal who is not a member - user-admin, the hostile
# fixture users - simply has it silently dropped by Keycloak (§75's spike
# finding), which is exactly the "no active organization" case the
# tenant-wide marker or the unscoped-token refusal then decides between.
token_for() {
  local username="$1" client="${2:-patient-app}" realm="${3:-${REALM}}"
  local org; org="$(organization_for_realm "${realm}")"
  local scope="openid"
  [[ -n "${org}" ]] && scope="openid organization:${org}"
  local response
  response="$(curl -sS --max-time 10 \
    -d "grant_type=password" \
    -d "client_id=${client}" \
    -d "username=${username}" \
    -d "password=${DEMO_PASSWORD}" \
    -d "scope=${scope}" \
    "${KEYCLOAK_URL}/realms/${realm}/protocol/openid-connect/token")" || return 1

  local token
  token="$(jq -r '.access_token // empty' <<<"${response}")"
  if [[ -z "${token}" ]]; then
    echo "token_for: ${username}@${client} did not receive a token: ${response}" >&2
    return 1
  fi
  printf '%s' "${token}"
}

# claim_of <token> <jq-expression>
# Reads a claim out of the payload segment without verifying anything: this is
# for asserting what the identity provider put in the token, never for deciding
# whether to trust it.
claim_of() {
  local token="$1" expression="$2" payload
  payload="$(cut -d. -f2 <<<"${token}")"
  # JWT uses base64url with the padding stripped; base64 wants both back.
  payload="${payload//-/+}"
  payload="${payload//_/\/}"
  while (( ${#payload} % 4 )); do payload+="="; done
  base64 -d <<<"${payload}" 2>/dev/null | jq -r "${expression}"
}

# tamper_with <token>
# Returns the same token with one signature byte changed, which is the cheapest
# possible forgery and must be refused.
#
# The changed character must not be the signature's last one: base64url's
# final character can carry as few as two significant bits (the rest is
# encoding padding, always zero), so a signature whose length isn't a
# multiple of 3 bytes - true for the 256-byte RSA signatures this realm
# mints - has a last character where flipping between certain values (A and
# B, notably) can leave every significant bit, and so the decoded signature
# itself, unchanged: an apparently "tampered" token that still verifies.
# The first character is always fully significant, so flipping it always
# changes the decoded bytes.
tamper_with() {
  local token="$1" header payload signature
  header="$(cut -d. -f1 <<<"${token}")"
  payload="$(cut -d. -f2 <<<"${token}")"
  signature="$(cut -d. -f3 <<<"${token}")"
  local first="${signature:0:1}"
  local replacement="A"
  [[ "${first}" == "A" ]] && replacement="B"
  printf '%s.%s.%s%s' "${header}" "${payload}" "${replacement}" "${signature:1}"
}

# wait_for_keycloak waits until the realm's discovery document is served.
wait_for_keycloak() {
  for _ in $(seq 1 60); do
    if curl -sS --max-time 5 -o /dev/null \
      "${KEYCLOAK_URL}/realms/${REALM}/.well-known/openid-configuration"; then
      return 0
    fi
    sleep 1
  done
  echo "keycloak never served the ${REALM} realm at ${KEYCLOAK_URL}" >&2
  return 1
}
