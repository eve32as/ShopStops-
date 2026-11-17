#!/usr/bin/env bash
# Tests runner control flow only, not SQL correctness (that needs PostGIS).
set -euo pipefail
repo_root="$(cd "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
temp="$(mktemp -d /tmp/shopstops-sql-runner-tests.XXXXXXXX)"
trap 'rm -rf "$temp"' EXIT
mkdir "$temp/bin"
cp "$repo_root/tests/sql-integration/fixtures/psql" "$temp/bin/psql"
chmod +x "$temp/bin/psql"
export PATH="$temp/bin:$PATH" FAKE_PSQL_LOG="$temp/log"
export PGHOST=127.0.0.1 PGUSER=shopstops_ci PGDATABASE=shopstops_ci
unset FAKE_PSQL_FAIL FAKE_PSQL_COUNT
passed=0
fail() { cat "$temp/output" >&2; printf 'FAIL: %s\n' "$*" >&2; exit 1; }
reject() {
  local exit_code=0
  bash "$repo_root/scripts/test-sql-integration.sh" > "$temp/output" 2>&1 || exit_code=$?
  [[ "$exit_code" != 0 ]] || fail 'failed subprocess was reported successful'
  if grep -q 'All ordered migrations' "$temp/output"; then fail 'printed success on error'; fi
  passed=$((passed + 1))
}
: > "$temp/log"
bash "$repo_root/scripts/test-sql-integration.sh" > "$temp/output" 2>&1 || fail 'valid mocked runner failed'
migration_files=("$repo_root"/db/migrations/*.sql)
fixture_files=("$repo_root"/db/tests/*.sql)
[[ "$(wc -l < "$temp/log")" -eq $((1 + ${#migration_files[@]} + ${#fixture_files[@]})) ]] || fail 'wrong number of SQL calls'
index=0
while IFS= read -r line; do
  [[ "$line" == *'-X --no-password --set=ON_ERROR_STOP=1'* ]] || fail 'psql isolation/fail-fast options missing'
  if [[ "$line" == *'/db/migrations/'* ]]; then
    index=$((index + 1))
    printf -v migration_prefix '%03d' "$index"
    [[ "$line" == *'--single-transaction'* && "$line" == *"/${migration_prefix}_"* ]] || fail 'migration transaction/order wrong'
  elif [[ "$line" == *'/db/tests/'* ]]; then
    [[ "$index" == "${#migration_files[@]}" && "$line" != *'--single-transaction'* ]] || fail 'fixture transaction nesting/order wrong'
  fi
done < "$temp/log"
passed=$((passed + 1))
targets=(preflight)
for file_path in "${migration_files[@]}" "${fixture_files[@]}"; do targets+=("${file_path#"$repo_root"}"); done
for target in "${targets[@]}"; do
  : > "$temp/log"; export FAKE_PSQL_FAIL="$target"; reject
  [[ "$(tail -n 1 "$temp/log")" == *"$target"* || "$target" == preflight ]] || fail 'runner continued after failure'
done
unset FAKE_PSQL_FAIL
export FAKE_PSQL_COUNT=1; reject; unset FAKE_PSQL_COUNT
for setting in PGHOST PGUSER PGDATABASE; do
  original="${!setting}"; export "$setting=production"
  : > "$temp/log"; reject
  [[ ! -s "$temp/log" ]] || fail 'unsafe destination invoked psql'
  export "$setting=$original"
done
printf 'All %s SQL runner offline tests passed (mock psql, no database).\n' "$passed"