#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
temp="$(mktemp -d /tmp/shopstops-live-tests.XXXXXXXX)"
trap 'rm -rf "$temp"' EXIT
mkdir "$temp/bin"
cp "$repo_root/tests/live-services/fixtures/curl" "$temp/bin/curl"
chmod +x "$temp/bin/curl"
export PATH="$temp/bin:$PATH" CURL_LOG="$temp/curl.log"
passed=0
reset() {
  export PUBLIC_ORIGIN=https://live.shopstops.com STORE_LOCATION_ID=12345678-1234-1234-1234-123456789abc
  unset OSRM_PRIVATE_URL OSRM_COORDINATES FAKE_CURL_CASE
  : > "$CURL_LOG"
}
check() {
  local expected="$1"; shift
  local exit_code=0
  env "$@" bash "$repo_root/scripts/test-live-services.sh" > "$temp/output" 2>&1 || exit_code=$?
  [[ "$exit_code" == "$expected" ]] || { cat "$temp/output"; exit 1; }
  passed=$((passed + 1))
}
reject_input() {
  reset; check 1 "$@"
  [[ ! -s "$CURL_LOG" ]] || { printf 'Invalid input reached curl\n' >&2; exit 1; }
}
reset; check 0
grep -q 'Passed 8 read-only probes' "$temp/output"
[[ "$(grep -c '^--request$' "$CURL_LOG")" == 8 ]]
for value in '' PLACEHOLDER http://live.shopstops.com https://localhost https://127.0.0.1 https://live.invalid https://example.com https://live.shopstops.com:443 https://user:password@live.shopstops.com https://live.shopstops.com/v1 'https://live.shopstops.com?jwt=secret' https://live.shopstops.com/#secret https://-bad.shopstops.com https://live..shopstops.com https://live.shopstops.com. https://api.shopstops.internal; do
  reject_input "PUBLIC_ORIGIN=$value"
done
for value in '' PLACEHOLDER ../escape not-a-uuid; do reject_input "STORE_LOCATION_ID=$value"; done
for value in https://public.shopstops.com http://127.0.0.1:5001 http://osrm.shopstops.internal:5001/path http://user:secret@osrm.shopstops.internal:5001; do
  reject_input "OSRM_PRIVATE_URL=$value" 'OSRM_COORDINATES=-73.98,40.75;-73.97,40.76'
done
for value in '' '181,40;0,40' '0,91;0,40' '0,40;0,-91' '0,40;0,40;0,40' '0,40;0,40?token=secret'; do
  reject_input OSRM_PRIVATE_URL=http://osrm.shopstops.internal:5001 "OSRM_COORDINATES=$value"
done
reject_input 'OSRM_COORDINATES=0,40;0,40'
for scenario in transport redirect unauthorized-success; do reset; check 1 "FAKE_CURL_CASE=$scenario"; done
reset
check 0 OSRM_PRIVATE_URL=http://osrm.shopstops.internal:5001 'OSRM_COORDINATES=-73.98,40.75;-73.97,40.76'
grep -q 'Passed 9 read-only probes' "$temp/output"
reset
check 1 OSRM_PRIVATE_URL=http://osrm.shopstops.internal:5001 'OSRM_COORDINATES=-73.98,40.75;-73.97,40.76' FAKE_CURL_CASE=bad-json
if grep -Eq 'password|jwt=secret|token=secret|NoRoute' "$temp/output"; then printf 'Sensitive response/input echoed\n' >&2; exit 1; fi
printf 'All %s live-services offline tests passed (fake curl, no network).\n' "$passed"