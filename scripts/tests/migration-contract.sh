#!/usr/bin/env bash
# Asserts the properties of the changelog set itself, as opposed to the schema it
# produces - that is what the store contract covers.
#
#   scripts/tests/migration-contract.sh <engine>
#
# Two of these can only be checked by running a migration twice, and the rest are
# properties of the changelog files that no amount of running would reveal.
set -uo pipefail

engine="${1:-postgres}"
failures=0
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
changelog_dir="${repo_root}/deploy/liquibase/changelog"

check() {
  local description="$1" ok="$2" detail="${3:-}"
  if [[ "${ok}" -eq 0 ]]; then
    echo "ok   ${description}"
  else
    echo "FAIL ${description}"
    [[ -n "${detail}" ]] && echo "     ${detail}"
    failures=$((failures + 1))
  fi
}

echo "--- the changelog set applies to ${engine} ---"

bash "${repo_root}/scripts/liquibase.sh" "${engine}" update >/tmp/migration-first.log 2>&1
check "the changelog set applies cleanly" "$?" "$(tail -5 /tmp/migration-first.log)"

# Idempotence: a second update must run nothing at all. Liquibase reports the
# count it ran, so "0" is the assertion, not "no error" - an update that
# re-applied a changeset would also exit zero.
bash "${repo_root}/scripts/liquibase.sh" "${engine}" update >/tmp/migration-second.log 2>&1
second_exit=$?
check "re-running the changelog set does not error" "${second_exit}" \
  "$(tail -5 /tmp/migration-second.log)"

ran="$(grep -oE '^Run:[[:space:]]+[0-9]+' /tmp/migration-second.log | grep -oE '[0-9]+' | head -1)"
[[ "${ran}" == "0" ]]
check "re-running the changelog set changes nothing" "$?" \
  "the second update ran ${ran:-an unknown number of} change sets"

bash "${repo_root}/scripts/liquibase.sh" "${engine}" status --verbose >/tmp/migration-status.log 2>&1
grep -qi "is up to date" /tmp/migration-status.log
check "the database reports itself up to date" "$?" "$(tail -5 /tmp/migration-status.log)"

echo
echo "--- the changelog set is portable by construction ---"

# Oracle's tightest identifier limit is 30 characters. Anything longer is
# silently fine on PostgreSQL and fails on Oracle, which is exactly the class of
# defect that only shows up on the engine nobody ran locally.
long_identifiers="$(grep -rhoE '(tableName|indexName|constraintName|primaryKeyName|name):[[:space:]]*[A-Za-z0-9_]+' "${changelog_dir}" \
  | awk -F': *' '{print $2}' | sort -u | awk 'length($0) > 30')"
[[ -z "${long_identifiers}" ]]
check "every identifier fits Oracle's 30-character limit" "$?" \
  "too long: ${long_identifiers}"

# A dialect-qualified changeset is a portability exception. Each one must say why
# it exists, or the next reader cannot tell a real divergence from a shortcut.
undocumented=""
while IFS= read -r file; do
  [[ -z "${file}" ]] && continue
  # Every changeSet carrying a dbms qualifier must also carry a comment.
  if ! python3 - "$file" <<'PY'
import sys, re
text = open(sys.argv[1]).read()
blocks = re.split(r'\n  - changeSet:', text)
for block in blocks[1:]:
    if 'dbms:' in block and 'comment:' not in block:
        sys.exit(1)
sys.exit(0)
PY
  then
    undocumented+="${file} "
  fi
done < <(grep -rl "dbms:" "${changelog_dir}" 2>/dev/null)

[[ -z "${undocumented}" ]]
check "every dialect-qualified change set explains the divergence" "$?" \
  "undocumented in: ${undocumented}"

# Engine-specific column types defeat the point of a generic changelog set.
native_types="$(grep -rniE 'type:[[:space:]]*(JSONB|SERIAL|BIGSERIAL|NUMBER\(|VARCHAR2|NVARCHAR2|TEXT|UUID|BYTEA|RAW)' "${changelog_dir}" || true)"
[[ -z "${native_types}" ]]
check "column types are generic rather than engine-specific" "$?" \
  "engine-specific types: ${native_types}"

if (( failures > 0 )); then
  echo
  echo "${failures} migration failure(s)"
  exit 1
fi

echo
echo "migration contract satisfied on ${engine}"
