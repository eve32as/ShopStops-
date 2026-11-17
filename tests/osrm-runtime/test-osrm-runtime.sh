#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
wrapper="$repo_root/scripts/start-osrm-from-s3.sh"
temp="$(mktemp -d /tmp/shopstops-osrm-tests.XXXXXXXX)"
temp="$(cd "$temp" && pwd -P)"
trap 'rm -rf "$temp"' EXIT
mkdir "$temp/bin"
cp "$repo_root/tests/osrm-runtime/fixtures/aws" "$repo_root/tests/osrm-runtime/fixtures/osrm-routed" "$temp/bin/"
chmod +x "$temp/bin/aws" "$temp/bin/osrm-routed"
export PATH="$temp/bin:$PATH"
export FAKE_AWS_LOG="$temp/aws.log" FAKE_OSRM_LOG="$temp/osrm.log" FAKE_TEMP_LOG="$temp/temp.log"
export FAKE_RELEASE="$temp/release" OSRM_DATA_PARENT="$temp/runtime"
extensions=(cell_metrics cells cnbg cnbg_to_ebg datasource_names ebg ebg_nodes edges enw fileIndex geometry icd maneuver_overrides mldgr names nbg_nodes partition properties ramIndex restrictions timestamp tld tls turn_duration_penalties turn_penalties_index turn_weight_penalties)
passed=0
fail() { cat "$temp/output" >&2; printf 'FAIL: %s\n' "$*" >&2; exit 1; }
pass() { passed=$((passed + 1)); printf 'PASS: %s\n' "$*"; }
manifest() { (cd "$FAKE_RELEASE" && shasum -a 256 new-york.osrm*) > "$FAKE_RELEASE/SHA256SUMS"; }
reset() {
  rm -rf "$temp/runtime" "$temp/release" "$temp/linked" "$temp/bin/osrm-routed"
  mkdir -m 700 "$temp/runtime" "$temp/release"
  cp "$repo_root/tests/osrm-runtime/fixtures/osrm-routed" "$temp/bin/"
  chmod +x "$temp/bin/osrm-routed"
  export AWS_REGION=us-east-1 EXPECTED_AWS_ACCOUNT_ID=123456789012
  export OSRM_ARTIFACT_BUCKET=shopstops-staging-osrm-123456789012-us-east-1 OSRM_DATASET_VERSION=v1
  export OSRM_DATA_PARENT="$temp/runtime" FAKE_CASE='' FAKE_ACCOUNT=123456789012 FAKE_CLI_VERSION=2.31.0
  unset AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3 AWS_ENDPOINT_URL_STS AWS_ENDPOINT_URL_CUSTOM
  : > "$FAKE_AWS_LOG"; : > "$FAKE_OSRM_LOG"; : > "$FAKE_TEMP_LOG"; : > "$temp/output"
  for extension in "${extensions[@]}"; do printf 'artifact %s\n' "$extension" > "$FAKE_RELEASE/new-york.osrm.$extension"; done
  manifest
}
audit() {
  if grep -Eq 'list-|head-|get-bucket|put-|delete-| s3 ' "$FAKE_AWS_LOG"; then fail 'unexpected AWS command'; fi
  while IFS= read -r command_line; do
    case "$command_line" in
      --version|'--region us-east-1 --no-cli-pager sts get-caller-identity '*|'--region us-east-1 --no-cli-pager s3api get-object '*) ;;
      *) fail 'AWS request outside STS identity and exact GETs' ;;
    esac
  done < "$FAKE_AWS_LOG"
  if grep -q 'get-object' "$FAKE_AWS_LOG"; then
    if grep 'get-object' "$FAKE_AWS_LOG" | grep -v -- '--expected-bucket-owner 123456789012' >/dev/null; then fail 'owner check missing'; fi
  fi
}
reject() {
  if bash "$wrapper" > "$temp/output" 2>&1; then fail 'invalid runtime start succeeded'; fi
  [[ ! -s "$FAKE_OSRM_LOG" ]] || fail 'OSRM executed on failure'
  [[ ! -e "$temp/runtime/.download-lock" ]] || fail 'lock leaked'
  while IFS= read -r directory; do [[ ! -e "$directory" ]] || fail 'temporary dataset leaked'; done < "$FAKE_TEMP_LOG"
  audit
}
reject_before_aws() { reject; [[ ! -s "$FAKE_AWS_LOG" ]] || fail 'invalid input called AWS'; }
reset
bash "$wrapper" > "$temp/output" 2>&1 || fail 'valid release failed'
[[ -s "$FAKE_OSRM_LOG" && -f "$OSRM_DATA_PARENT/verified/SHA256SUMS" ]] || fail 'valid release not published/executed'
[[ "$(grep -c 'get-object' "$FAKE_AWS_LOG")" -eq 27 ]] || fail 'wrong required GET count'
[[ "$(stat -c %a "$OSRM_DATA_PARENT/verified" 2>/dev/null || stat -f %Lp "$OSRM_DATA_PARENT/verified")" == 700 ]] || fail 'dataset not private'
audit; pass '26 required artifacts verified before fixed OSRM exec'
reset; printf 'optional base\n' > "$FAKE_RELEASE/new-york.osrm"; manifest
bash "$wrapper" > "$temp/output" 2>&1 || fail 'optional base failed'
audit; pass 'optional base checksummed and allowed'
for scenario in missing-manifest corrupt missing-file traversal duplicate unknown missing-entry malformed binary-entry unterminated oversized empty-artifact wrong-account sts-error get-error artifact-symlink exec-error; do
  reset
  case "$scenario" in
    missing-manifest) rm "$FAKE_RELEASE/SHA256SUMS" ;;
    corrupt) printf 'corrupt\n' >> "$FAKE_RELEASE/new-york.osrm.cells" ;;
    missing-file) rm "$FAKE_RELEASE/new-york.osrm.cells" ;;
    traversal) printf '%064d  ../escape\n' 0 >> "$FAKE_RELEASE/SHA256SUMS" ;;
    duplicate) head -n 1 "$FAKE_RELEASE/SHA256SUMS" >> "$FAKE_RELEASE/extra"; cat "$FAKE_RELEASE/extra" >> "$FAKE_RELEASE/SHA256SUMS" ;;
    unknown) printf '%064d  new-york.osrm.unknown\n' 0 >> "$FAKE_RELEASE/SHA256SUMS" ;;
    missing-entry) grep -v 'new-york.osrm.cells$' "$FAKE_RELEASE/SHA256SUMS" > "$FAKE_RELEASE/reduced"; mv "$FAKE_RELEASE/reduced" "$FAKE_RELEASE/SHA256SUMS" ;;
    malformed) printf 'not-a-checksum\n' >> "$FAKE_RELEASE/SHA256SUMS" ;;
    binary-entry) printf '\0' >> "$FAKE_RELEASE/SHA256SUMS" ;;
    unterminated) printf '%064d  new-york.osrm' 0 >> "$FAKE_RELEASE/SHA256SUMS" ;;
    oversized) for ((i=0;i<100;i++)); do printf '%064d  new-york.osrm\n' 0 >> "$FAKE_RELEASE/SHA256SUMS"; done ;;
    empty-artifact) : > "$FAKE_RELEASE/new-york.osrm.cells"; manifest ;;
    wrong-account) export FAKE_ACCOUNT=999999999999 ;;
    *) export FAKE_CASE="$scenario" ;;
  esac
  reject
  [[ ! -e "$OSRM_DATA_PARENT/verified" ]] || fail 'unverified destination appeared'
  pass "$scenario rejected, no OSRM exec and temporary cleanup"
done
for name in AWS_REGION EXPECTED_AWS_ACCOUNT_ID OSRM_ARTIFACT_BUCKET OSRM_DATASET_VERSION; do
  for value in '' PLACEHOLDER; do
    reset; export "$name=$value"; reject_before_aws; pass "$name empty/placeholder rejected before AWS"
  done
done
for entry in 'AWS_REGION=us-gov-west-1' 'EXPECTED_AWS_ACCOUNT_ID=000000000000' 'OSRM_ARTIFACT_BUCKET=bad.bucket' 'OSRM_ARTIFACT_BUCKET=another-bucket' 'OSRM_DATASET_VERSION=../escape' 'OSRM_DATASET_VERSION=v..1' 'OSRM_DATASET_VERSION=-bad' 'OSRM_DATASET_VERSION=has space'; do
  reset; export "$entry"; reject_before_aws; pass 'malformed configuration rejected before AWS'
done
for name in AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3 AWS_ENDPOINT_URL_STS AWS_ENDPOINT_URL_CUSTOM; do
  reset; export "$name=https://untrusted.invalid"; reject_before_aws; pass "$name rejected before AWS"
done
reset; export FAKE_CLI_VERSION=1.0; reject; pass 'AWS CLI v1 rejected'
reset; mkdir "$OSRM_DATA_PARENT/verified"; reject_before_aws; pass 'existing destination rejected without cache reuse'
reset; printf 'existing\n' > "$OSRM_DATA_PARENT/verified"; reject_before_aws; pass 'existing regular target rejected'
reset; ln -s "$temp/absent" "$OSRM_DATA_PARENT/verified"; reject_before_aws; pass 'dangling target symlink rejected'
reset; ln -s "$FAKE_RELEASE" "$OSRM_DATA_PARENT/verified"; reject_before_aws; pass 'target symlink rejected'
reset; ln -s "$OSRM_DATA_PARENT" "$temp/linked"; export OSRM_DATA_PARENT="$temp/linked"; reject_before_aws; pass 'parent symlink rejected'
reset; ln -s "$temp/runtime" "$temp/linked"; mkdir "$temp/runtime/child"; export OSRM_DATA_PARENT="$temp/linked/child"; reject_before_aws; pass 'ancestor symlink rejected'
reset; chmod 755 "$OSRM_DATA_PARENT"; reject_before_aws; pass 'nonprivate parent rejected'
reset; rm "$temp/bin/osrm-routed"; reject_before_aws; pass 'missing OSRM executable rejected'
reset; chmod -x "$temp/bin/osrm-routed"; reject_before_aws; pass 'nonexecutable OSRM rejected'
reset; mkdir "$OSRM_DATA_PARENT/.download-lock"
if bash "$wrapper" > "$temp/output" 2>&1; then fail 'lock accepted'; fi
[[ ! -s "$FAKE_OSRM_LOG" && ! -s "$FAKE_TEMP_LOG" ]] || fail 'locked start progressed'
audit; pass 'concurrent/stale lock rejected without deleting another lock'
printf '\nAll %s OSRM runtime offline tests passed (fake AWS/OSRM only).\n' "$passed"