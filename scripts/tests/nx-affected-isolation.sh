#!/usr/bin/env bash
# Guards the single-graph promise: Go and Angular live in one Nx graph, but a
# change to one language must not drag the other into `nx affected`.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
export PATH="${repo_root}/scripts/bin:${PATH}"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "nx-affected-isolation: working tree must be clean" >&2
  exit 1
fi

probe="apps/admin-console/src/styles.css"
restore() { git checkout -- "${probe}"; }
trap restore EXIT

printf '\n/* nx affected probe */\n' >> "${probe}"
affected="$(npx nx show projects --affected --base=HEAD)"

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

if (( failures > 0 )); then
  echo
  echo "${failures} affected-graph failure(s)"
  exit 1
fi

echo
echo "nx affected isolation holds"
