#!/usr/bin/env bash
# Refuses a `make loadtest` run before it starts wasting time on a host that
# cannot sustain it (§15: "a documented minimum host specification and a
# preflight check, so that a run either starts valid or refuses clearly").
#
#   scripts/loadtest-preflight.sh
#
# Reads free memory from /proc/meminfo and free disk from `df` on the
# repository's own filesystem. Both paths are overridable so the check itself
# can be tested without depleting the host's real resources:
#   LOADTEST_PREFLIGHT_MEMINFO   - path to a /proc/meminfo-shaped file
#   LOADTEST_PREFLIGHT_DISK_PATH - path `df` is run against
#   LOADTEST_MIN_FREE_MEM_KB     - minimum free+cached memory required
#   LOADTEST_MIN_FREE_DISK_KB    - minimum free disk required
set -euo pipefail

# The full §15 load model (600,000 Keycloak users, 42,000,000 role mappings,
# 1,000 concurrent k6 VUs against the control plane) is the documented
# minimum this preflight enforces: 8 GiB of free/reclaimable memory and 10 GiB
# of free disk for the seeded databases, Kafka and the results directory.
MIN_FREE_MEM_KB="${LOADTEST_MIN_FREE_MEM_KB:-8388608}"
MIN_FREE_DISK_KB="${LOADTEST_MIN_FREE_DISK_KB:-10485760}"

meminfo_path="${LOADTEST_PREFLIGHT_MEMINFO:-/proc/meminfo}"
disk_path="${LOADTEST_PREFLIGHT_DISK_PATH:-.}"

if [[ ! -r "${meminfo_path}" ]]; then
  echo "loadtest-preflight: cannot read ${meminfo_path}" >&2
  exit 1
fi

# MemAvailable already accounts for reclaimable cache the kernel would hand
# back under pressure, which is what the load model actually gets to use.
free_mem_kb="$(awk '/^MemAvailable:/ {print $2}' "${meminfo_path}")"
if [[ -z "${free_mem_kb}" ]]; then
  echo "loadtest-preflight: ${meminfo_path} has no MemAvailable line" >&2
  exit 1
fi

free_disk_kb="$(df -Pk "${disk_path}" | awk 'NR==2 {print $4}')"
if [[ -z "${free_disk_kb}" ]]; then
  echo "loadtest-preflight: could not read free disk space for ${disk_path}" >&2
  exit 1
fi

failures=0

if (( free_mem_kb < MIN_FREE_MEM_KB )); then
  echo "FAIL available memory ${free_mem_kb}KB is below the ${MIN_FREE_MEM_KB}KB minimum for the §15 load profile" >&2
  failures=$((failures + 1))
else
  echo "ok   available memory ${free_mem_kb}KB meets the ${MIN_FREE_MEM_KB}KB minimum"
fi

if (( free_disk_kb < MIN_FREE_DISK_KB )); then
  echo "FAIL free disk ${free_disk_kb}KB is below the ${MIN_FREE_DISK_KB}KB minimum for the §15 load profile" >&2
  failures=$((failures + 1))
else
  echo "ok   free disk ${free_disk_kb}KB meets the ${MIN_FREE_DISK_KB}KB minimum"
fi

if (( failures > 0 )); then
  echo
  echo "loadtest-preflight: refusing to start (${failures} check(s) failed)" >&2
  exit 1
fi

echo
echo "loadtest-preflight: host meets the documented minimum for a full loadtest run"
