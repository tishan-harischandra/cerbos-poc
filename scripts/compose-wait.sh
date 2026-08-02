#!/usr/bin/env bash
# Block until every compose container reports healthy.
#
# `docker compose up --wait` only exists in Compose v2; this works on both v1
# and v2 by inspecting the containers the project owns.
set -euo pipefail

DOCKER="${DOCKER:-docker}"
COMPOSE="${COMPOSE:-${DOCKER} compose}"
TIMEOUT_SECONDS="${COMPOSE_WAIT_TIMEOUT:-300}"

deadline=$(( SECONDS + TIMEOUT_SECONDS ))

while :; do
  mapfile -t containers < <(${COMPOSE} ps -q | tr -d '\r' | grep -v '^$' || true)

  if (( ${#containers[@]} == 0 )); then
    echo "compose-wait: no containers are running" >&2
    exit 1
  fi

  pending=()
  for container in "${containers[@]}"; do
    name="$(${DOCKER} inspect --format '{{ .Name }}' "${container}" 2>/dev/null || echo "${container}")"
    status="$(${DOCKER} inspect --format '{{ if .State.Health }}{{ .State.Health.Status }}{{ else }}no-healthcheck{{ end }}' "${container}" 2>/dev/null || echo unknown)"
    [[ "${status}" == "healthy" ]] || pending+=("${name#/}=${status}")
  done

  if (( ${#pending[@]} == 0 )); then
    echo "compose-wait: all ${#containers[@]} containers are healthy"
    exit 0
  fi

  if (( SECONDS >= deadline )); then
    echo "compose-wait: timed out after ${TIMEOUT_SECONDS}s waiting for: ${pending[*]}" >&2
    exit 1
  fi

  echo "compose-wait: waiting for ${pending[*]}"
  sleep 5
done
