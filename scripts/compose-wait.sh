#!/usr/bin/env bash
# Block until compose containers report healthy.
#
#   scripts/compose-wait.sh [service...]
#
# With no arguments it waits for every container the project owns. Naming
# services waits for only those, which matters when a slow optional service is
# running alongside: waiting for Oracle under a timeout meant for the control
# plane would report a broken stack when it is merely still starting.
#
# `docker compose up --wait` only exists in Compose v2; this works on both v1
# and v2 by inspecting the containers the project owns.
set -euo pipefail

DOCKER="${DOCKER:-docker}"
COMPOSE="${COMPOSE:-${DOCKER} compose}"
TIMEOUT_SECONDS="${COMPOSE_WAIT_TIMEOUT:-300}"

deadline=$(( SECONDS + TIMEOUT_SECONDS ))
services=("$@")

while :; do
  mapfile -t containers < <(${COMPOSE} ps -q "${services[@]}" | tr -d '\r' | grep -v '^$' || true)

  if (( ${#containers[@]} == 0 )); then
    if (( ${#services[@]} > 0 )); then
      echo "compose-wait: none of these services are running: ${services[*]}" >&2
    else
      echo "compose-wait: no containers are running" >&2
    fi
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
