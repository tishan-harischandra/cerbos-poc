#!/usr/bin/env bash
set -euo pipefail

KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:${KEYCLOAK_PORT:-8081}}"
KEYCLOAK_ADMIN="${KEYCLOAK_ADMIN:-admin}"
KEYCLOAK_ADMIN_PASSWORD="${KEYCLOAK_ADMIN_PASSWORD:-change-me}"

for command in curl jq; do
  command -v "${command}" >/dev/null 2>&1 || {
    echo "required command not found: ${command}" >&2
    exit 1
  }
done

response_file="$(mktemp)"
trap 'rm -f "${response_file}"' EXIT
HTTP_STATUS=""
HTTP_BODY=""

urlencode() {
  jq -nr --arg value "$1" '$value | @uri'
}

request() {
  local method="$1"
  local url="$2"
  local data="${3-}"
  local -a args=(
    --silent --show-error --output "${response_file}" --write-out '%{http_code}'
    --request "${method}" --header "Authorization: Bearer ${ADMIN_TOKEN}"
  )
  if [[ -n "${data}" ]]; then
    args+=(--header 'Content-Type: application/json' --data "${data}")
  fi
  if ! HTTP_STATUS="$(curl "${args[@]}" "${url}")"; then
    echo "Keycloak request failed: ${method} ${url}" >&2
    exit 1
  fi
  HTTP_BODY="$(cat "${response_file}")"
}

expect_status() {
  local expected="$1"
  local operation="$2"
  if [[ "${HTTP_STATUS}" != "${expected}" ]]; then
    echo "${operation}: expected HTTP ${expected}, got ${HTTP_STATUS}: ${HTTP_BODY}" >&2
    exit 1
  fi
}

get_json() {
  request GET "$1"
  expect_status 200 "GET $1"
}

resolve_user_id() {
  local realm="$1"
  local username="$2"
  get_json "${KEYCLOAK_URL}/admin/realms/${realm}/users?username=$(urlencode "${username}")&exact=true"
  local count
  count="$(jq --arg username "${username}" '[.[] | select(.username == $username)] | length' <<<"${HTTP_BODY}")"
  [[ "${count}" == "1" ]] || {
    echo "${realm}: expected exactly one user named ${username}, found ${count}" >&2
    exit 1
  }
  jq -r --arg username "${username}" '.[] | select(.username == $username) | .id' <<<"${HTTP_BODY}"
}

resolve_role() {
  local realm="$1"
  local role_name="$2"
  get_json "${KEYCLOAK_URL}/admin/realms/${realm}/roles/$(urlencode "${role_name}")"
  [[ "$(jq -r '.name' <<<"${HTTP_BODY}")" == "${role_name}" ]] || {
    echo "${realm}: role lookup did not return exact role ${role_name}" >&2
    exit 1
  }
  printf '%s\n' "${HTTP_BODY}"
}

resolve_organization_id() {
  local realm="$1"
  local alias="$2"
  # Keycloak 26.7.3's organization search matches names, not aliases. Fetch the
  # bounded fixture set and enforce exact alias equality ourselves.
  get_json "${KEYCLOAK_URL}/admin/realms/${realm}/organizations?first=0&max=100"
  local count
  count="$(jq --arg alias "${alias}" '[.[] | select(.alias == $alias)] | length' <<<"${HTTP_BODY}")"
  [[ "${count}" == "1" ]] || {
    echo "${realm}: expected exactly one organization with alias ${alias}, found ${count}" >&2
    exit 1
  }
  jq -r --arg alias "${alias}" '.[] | select(.alias == $alias) | .id' <<<"${HTTP_BODY}"
}

resolve_organization_group_id() {
  local realm="$1"
  local org_id="$2"
  local group_name="$3"
  local groups_url="${KEYCLOAK_URL}/admin/realms/${realm}/organizations/${org_id}/groups"
  get_json "${groups_url}"
  local count
  count="$(jq --arg name "${group_name}" '[.[] | select(.name == $name)] | length' <<<"${HTTP_BODY}")"
  if [[ "${count}" == "0" ]]; then
    request POST "${groups_url}" "$(jq -nc --arg name "${group_name}" '{name: $name}')"
    expect_status 201 "create organization group ${realm}/${org_id}/${group_name}"
    get_json "${groups_url}"
    count="$(jq --arg name "${group_name}" '[.[] | select(.name == $name)] | length' <<<"${HTTP_BODY}")"
  fi
  [[ "${count}" == "1" ]] || {
    echo "${realm}: expected exactly one organization group named ${group_name}, found ${count}" >&2
    exit 1
  }
  jq -r --arg name "${group_name}" '.[] | select(.name == $name) | .id' <<<"${HTTP_BODY}"
}

map_organization_role() {
  local realm="$1"
  local org_id="$2"
  local group_id="$3"
  local role_name="$4"
  local role_json role_id mappings_url
  role_json="$(resolve_role "${realm}" "${role_name}")"
  role_id="$(jq -r '.id' <<<"${role_json}")"
  mappings_url="${KEYCLOAK_URL}/admin/realms/${realm}/organizations/${org_id}/groups/${group_id}/role-mappings/realm"
  get_json "${mappings_url}"
  if ! jq -e --arg id "${role_id}" --arg name "${role_name}" '.[] | select(.id == $id and .name == $name)' <<<"${HTTP_BODY}" >/dev/null; then
    request POST "${mappings_url}" "$(jq -nc --argjson role "${role_json}" '[$role]')"
    expect_status 204 "map organization role ${realm}/${org_id}/${group_id}/${role_name}"
  fi
  get_json "${mappings_url}"
  jq -e --arg id "${role_id}" --arg name "${role_name}" '.[] | select(.id == $id and .name == $name)' <<<"${HTTP_BODY}" >/dev/null || {
    echo "${realm}: organization role ${role_name} was not confirmed after mapping" >&2
    exit 1
  }
}

add_organization_member() {
  local realm="$1"
  local org_id="$2"
  local group_id="$3"
  local username="$4"
  local user_id members_url add_status
  user_id="$(resolve_user_id "${realm}" "${username}")"
  members_url="${KEYCLOAK_URL}/admin/realms/${realm}/organizations/${org_id}/groups/${group_id}/members"
  request PUT "${members_url}/${user_id}"
  add_status="${HTTP_STATUS}"
  if [[ "${add_status}" != "204" && "${add_status}" != "409" ]]; then
    echo "add organization member ${realm}/${username}: expected HTTP 204 or 409, got ${add_status}: ${HTTP_BODY}" >&2
    exit 1
  fi
  get_json "${members_url}?first=0&max=100"
  jq -e --arg id "${user_id}" --arg username "${username}" '.[] | select(.id == $id and .username == $username)' <<<"${HTTP_BODY}" >/dev/null || {
    echo "${realm}: organization group membership for ${username} was not confirmed after HTTP ${add_status}" >&2
    exit 1
  }
}

seed_organization_group() {
  local realm="$1"
  local alias="$2"
  local group_name="$3"
  local role_name="$4"
  shift 4
  local org_id group_id username
  org_id="$(resolve_organization_id "${realm}" "${alias}")"
  group_id="$(resolve_organization_group_id "${realm}" "${org_id}" "${group_name}")"
  map_organization_role "${realm}" "${org_id}" "${group_id}" "${role_name}"
  for username in "$@"; do
    add_organization_member "${realm}" "${org_id}" "${group_id}" "${username}"
  done
  echo "seeded ${realm}/${alias}/${group_name}: ${role_name} -> $*"
}

resolve_realm_group_id() {
  local realm="$1"
  local group_name="$2"
  local groups_url="${KEYCLOAK_URL}/admin/realms/${realm}/groups"
  get_json "${groups_url}?search=$(urlencode "${group_name}")&exact=true"
  local count
  count="$(jq --arg name "${group_name}" '[.[] | select(.name == $name)] | length' <<<"${HTTP_BODY}")"
  if [[ "${count}" == "0" ]]; then
    request POST "${groups_url}" "$(jq -nc --arg name "${group_name}" '{name: $name}')"
    expect_status 201 "create realm group ${realm}/${group_name}"
    get_json "${groups_url}?search=$(urlencode "${group_name}")&exact=true"
    count="$(jq --arg name "${group_name}" '[.[] | select(.name == $name)] | length' <<<"${HTTP_BODY}")"
  fi
  [[ "${count}" == "1" ]] || {
    echo "${realm}: expected exactly one realm group named ${group_name}, found ${count}" >&2
    exit 1
  }
  jq -r --arg name "${group_name}" '.[] | select(.name == $name) | .id' <<<"${HTTP_BODY}"
}

map_realm_group_role() {
  local realm="$1"
  local group_id="$2"
  local role_name="$3"
  local role_json role_id mappings_url
  role_json="$(resolve_role "${realm}" "${role_name}")"
  role_id="$(jq -r '.id' <<<"${role_json}")"
  mappings_url="${KEYCLOAK_URL}/admin/realms/${realm}/groups/${group_id}/role-mappings/realm"
  get_json "${mappings_url}"
  if ! jq -e --arg id "${role_id}" --arg name "${role_name}" '.[] | select(.id == $id and .name == $name)' <<<"${HTTP_BODY}" >/dev/null; then
    request POST "${mappings_url}" "$(jq -nc --argjson role "${role_json}" '[$role]')"
    expect_status 204 "map realm group role ${realm}/${group_id}/${role_name}"
  fi
  get_json "${mappings_url}"
  jq -e --arg id "${role_id}" --arg name "${role_name}" '.[] | select(.id == $id and .name == $name)' <<<"${HTTP_BODY}" >/dev/null || {
    echo "${realm}: realm group role ${role_name} was not confirmed after mapping" >&2
    exit 1
  }
}

add_realm_group_member() {
  local realm="$1"
  local group_id="$2"
  local username="$3"
  local user_id members_url
  user_id="$(resolve_user_id "${realm}" "${username}")"
  members_url="${KEYCLOAK_URL}/admin/realms/${realm}/groups/${group_id}/members"
  get_json "${members_url}?first=0&max=100"
  if ! jq -e --arg id "${user_id}" --arg username "${username}" '.[] | select(.id == $id and .username == $username)' <<<"${HTTP_BODY}" >/dev/null; then
    request PUT "${KEYCLOAK_URL}/admin/realms/${realm}/users/${user_id}/groups/${group_id}"
    expect_status 204 "add realm group member ${realm}/${username}"
  fi
  get_json "${members_url}?first=0&max=100"
  jq -e --arg id "${user_id}" --arg username "${username}" '.[] | select(.id == $id and .username == $username)' <<<"${HTTP_BODY}" >/dev/null || {
    echo "${realm}: realm group membership for ${username} was not confirmed" >&2
    exit 1
  }
}

seed_tenant_admins() {
  local realm="tenant-a"
  local group_id
  group_id="$(resolve_realm_group_id "${realm}" tenant-admins)"
  map_realm_group_role "${realm}" "${group_id}" admin
  map_realm_group_role "${realm}" "${group_id}" administrator
  add_realm_group_member "${realm}" "${group_id}" user-admin
  add_realm_group_member "${realm}" "${group_id}" user-admin-clinician
  echo "seeded ${realm}/tenant-admins: admin,administrator -> user-admin user-admin-clinician"
}

configure_browser_ports() {
  local realm="$1"
  local clients_url="${KEYCLOAK_URL}/admin/realms/${realm}/clients"
  get_json "${clients_url}?clientId=patient-app"
  local count client client_id host redirects origins
  count="$(jq '[.[] | select(.clientId == "patient-app")] | length' <<<"${HTTP_BODY}")"
  [[ "${count}" == "1" ]] || {
    echo "${realm}: expected exactly one patient-app client, found ${count}" >&2
    exit 1
  }
  client="$(jq '.[] | select(.clientId == "patient-app")' <<<"${HTTP_BODY}")"
  client_id="$(jq -r '.id' <<<"${client}")"
  host="${realm}.localtest.me"
  redirects="$(jq -nc --arg host "${host}" --arg admin "${ADMIN_CONSOLE_PORT:-4200}" --arg business "${BUSINESS_UI_PORT:-4201}" '["http://\($host):\($admin)/*", "http://\($host):\($business)/*"]')"
  origins="$(jq -nc --arg host "${host}" --arg admin "${ADMIN_CONSOLE_PORT:-4200}" --arg business "${BUSINESS_UI_PORT:-4201}" '["http://\($host):\($admin)", "http://\($host):\($business)"]')"
  client="$(jq --argjson redirects "${redirects}" --argjson origins "${origins}" '
    .redirectUris = ((.redirectUris // []) + $redirects | unique)
    | .webOrigins = ((.webOrigins // []) + $origins | unique)
    | .attributes = (.attributes // {})
    | .attributes["post.logout.redirect.uris"] = (((.attributes["post.logout.redirect.uris"] // "") | split("##") | map(select(length > 0))) + $redirects | unique | join("##"))
  ' <<<"${client}")"
  request PUT "${clients_url}/${client_id}" "${client}"
  expect_status 204 "configure browser ports for ${realm}/patient-app"
  echo "configured ${realm}/patient-app browser ports: ${ADMIN_CONSOLE_PORT:-4200},${BUSINESS_UI_PORT:-4201}"
}

token_response="$(curl --silent --show-error --fail-with-body \
  --data-urlencode 'grant_type=password' \
  --data-urlencode 'client_id=admin-cli' \
  --data-urlencode "username=${KEYCLOAK_ADMIN}" \
  --data-urlencode "password=${KEYCLOAK_ADMIN_PASSWORD}" \
  "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token")"
ADMIN_TOKEN="$(jq -er '.access_token | select(type == "string" and length > 0)' <<<"${token_response}")" || {
  echo "Keycloak master token response did not contain an access token" >&2
  exit 1
}

seed_organization_group tenant-a north-hospital Doctors doctor \
  user-doctor user-doctor-multi user-doctor-revoked user-admin-clinician
seed_organization_group tenant-a north-hospital Auditors auditor user-auditor
seed_organization_group tenant-a north-hospital Clerks clerk user-clerk-granted
seed_organization_group tenant-a south-hospital Auditors auditor user-doctor-multi
seed_organization_group tenant-b hospital-b1 Doctors doctor user-doctor-b
seed_organization_group tenant-c hospital-c1 Doctors doctor user-doctor-c
seed_tenant_admins
configure_browser_ports tenant-a
configure_browser_ports tenant-b
configure_browser_ports tenant-c

echo "Keycloak organization and tenant-admin role fixtures are seeded and confirmed"
