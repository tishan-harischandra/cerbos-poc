#!/usr/bin/env bash
# Guards the single-graph promise: Go and Angular live in one Nx graph, but a
# change to one language must not drag the other into `nx affected`.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
export PATH="${repo_root}/scripts/bin:${PATH}"

# Ask Nx what a hypothetical Angular-only change would affect. Using --files
# means this never mutates the working tree, so it is safe to run mid-change.
probe="apps/admin-console/src/styles.css"
affected="$(npx nx show projects --affected --files="${probe}")"

failures=0
if grep -qx 'admin-console' <<<"${affected}"; then
  echo "ok   an Angular change marks admin-console as affected"
else
  echo "FAIL an Angular change should mark admin-console as affected"
  failures=$((failures + 1))
fi

if grep -qx 'ads' <<<"${affected}"; then
  echo "FAIL an Angular change must not mark the Go service ads as affected"
  failures=$((failures + 1))
else
  echo "ok   an Angular change leaves the Go service ads untouched"
fi

# The architecture test scans every Go file in the repository, so Nx cannot infer
# its inputs from imports. If a Go change elsewhere left it unaffected, the guard
# against precedence logic in Go would sleep through the very commit that added
# some, and a cached pass would be reported instead.
go_affected="$(npx nx show projects --affected --files="apps/ads/internal/authz/authz.go")"

if grep -qx 'architecture' <<<"${go_affected}"; then
  echo "ok   a Go change anywhere marks the architecture test as affected"
else
  echo "FAIL a Go change must mark the architecture test as affected"
  failures=$((failures + 1))
fi

if grep -qx 'architecture' <<<"${affected}"; then
  echo "FAIL an Angular-only change must not mark the architecture test as affected"
  failures=$((failures + 1))
else
  echo "ok   an Angular-only change leaves the architecture test untouched"
fi

if (( failures > 0 )); then
  echo
  echo "${failures} affected-graph failure(s)"
  exit 1
fi

echo
echo "nx affected isolation holds"
