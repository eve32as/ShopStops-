#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C AWS_PAGER='' AWS_CLI_AUTO_PROMPT=off AWS_IGNORE_CONFIGURED_ENDPOINT_URLS=true
source "${BASH_SOURCE[0]%/*}/staging-images-common.sh"

[[ $# -eq 0 ]] || die 'Runtime accepts environment configuration only; no OSRM argument overrides'
AWS_REGION="${AWS_REGION:-}"
EXPECTED_AWS_ACCOUNT_ID="${EXPECTED_AWS_ACCOUNT_ID:-}"
OSRM_ARTIFACT_BUCKET="${OSRM_ARTIFACT_BUCKET:-}"
OSRM_DATASET_VERSION="${OSRM_DATASET_VERSION:-}"
validate_identity_inputs
validate_value OSRM_ARTIFACT_BUCKET
validate_value OSRM_DATASET_VERSION
validate_bucket_name "$OSRM_ARTIFACT_BUCKET"
[[ "$OSRM_ARTIFACT_BUCKET" == "shopstops-staging-osrm-${EXPECTED_AWS_ACCOUNT_ID}-${AWS_REGION}" ]] || die 'Only the designated staging artifact bucket is allowed'
[[ ${#OSRM_DATASET_VERSION} -le 64 && "$OSRM_DATASET_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || die 'Dataset version must match the publisher safe version format'
reject_aws_endpoint_overrides

# A dedicated, private volume parent is the trust boundary for atomic rename.
# Reject symlinks in EVERY component, including dangling final targets.
parent="${OSRM_DATA_PARENT:-/data/osrm}"
[[ "$parent" == /* && "$parent" != / && "$parent" != */ && "$parent" != *//* ]] || die 'Use a clean absolute dataset parent path'
component_path=''
IFS=/ read -r -a components <<< "$parent"
for component in "${components[@]}"; do
  [[ -n "$component" ]] || continue
  [[ "$component" != . && "$component" != .. ]] || die 'Unsafe parent path'
  component_path="$component_path/$component"
  [[ -d "$component_path" && ! -L "$component_path" ]] || die 'Parent components must be existing nonsymlink directories'
done
mode="$(stat -c %a "$parent" 2>/dev/null || stat -f %Lp "$parent")"
owner="$(stat -c %u "$parent" 2>/dev/null || stat -f %u "$parent")"
[[ "$mode" == 700 && "$owner" == "$(id -u)" ]] || die 'Dataset parent must be owned by runtime user with mode 0700'
destination="$parent/verified"
[[ ! -e "$destination" && ! -L "$destination" ]] || die 'Existing dataset targets are never reused'
command -v aws >/dev/null || die 'AWS CLI v2 required'
osrm_executable="$(command -v osrm-routed)" || die 'osrm-routed required'
[[ -f "$osrm_executable" && -x "$osrm_executable" ]] || die 'Executable osrm-routed required'
if command -v sha256sum >/dev/null; then
  hash_command=(sha256sum)
elif command -v shasum >/dev/null; then
  hash_command=(shasum -a 256)
else
  die 'SHA256 checksum tools required'
fi
[[ "$(aws --version 2>/dev/null)" == aws-cli/2.* ]] || die 'AWS CLI v2 required'
umask 077
lock="$parent/.download-lock"
mkdir "$lock" 2>/dev/null || die 'Runtime download is already active or stale lock exists'
temp_dir=''
published=0
cleanup() {
  [[ -z "$temp_dir" ]] || rm -rf -- "$temp_dir"
  if [[ "$published" == 1 ]]; then rm -rf -- "$destination"; fi
  rmdir "$lock" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
temp_dir="$(mktemp -d "$parent/.download.XXXXXXXX")"
verify_account
prefix="datasets/new-york/$OSRM_DATASET_VERSION/"
get_object() {
  aws_read s3api get-object --bucket "$OSRM_ARTIFACT_BUCKET" \
    --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" --key "$prefix$1" "${@:2}"
}
# Range bounds even a hostile manifest download. Require the COMPLETE object,
# not merely a valid prefix of an oversized manifest. No Head/List calls needed.
range="$(get_object SHA256SUMS --range bytes=0-4096 --query ContentRange --output text "$temp_dir/SHA256SUMS")"
[[ "$range" =~ ^bytes\ 0-([0-9]+)/([0-9]+)$ ]] || die 'Missing or invalid manifest range metadata'
last="${BASH_REMATCH[1]}"; total="${BASH_REMATCH[2]}"
[[ ${#last} -le 4 && ${#total} -le 4 ]] || die 'Manifest too large'
size="$(wc -c < "$temp_dir/SHA256SUMS")"
[[ "$total" -gt 0 && "$total" -le 4096 && "$size" -eq "$total" && "$last" -eq $((total - 1)) ]] || die 'Manifest is oversized or truncated'
extensions=(cell_metrics cells cnbg cnbg_to_ebg datasource_names ebg ebg_nodes edges enw fileIndex geometry icd maneuver_overrides mldgr names nbg_nodes partition properties ramIndex restrictions timestamp tld tls turn_duration_penalties turn_penalties_index turn_weight_penalties)
files=(); hashes=()
entry_pattern='^([a-f0-9]{64})  (new-york\.osrm(\.[A-Za-z_]+)?)$'
: > "$temp_dir/parsed"
while IFS= read -r line; do
  [[ "$line" =~ $entry_pattern ]] || die 'Malformed checksum entry'
  checksum="${BASH_REMATCH[1]}"; filename="${BASH_REMATCH[2]}"
  allowed=0
  [[ "$filename" != new-york.osrm ]] || allowed=1
  for extension in "${extensions[@]}"; do
    [[ "$filename" != "new-york.osrm.$extension" ]] || allowed=1
  done
  [[ "$allowed" == 1 ]] || die 'Unknown runtime artifact'
  for seen in "${files[@]+${files[@]}}"; do
    [[ "$seen" != "$filename" ]] || die 'Duplicate checksum entry'
  done
  files+=("$filename"); hashes+=("$checksum")
  printf '%s  %s\n' "$checksum" "$filename" >> "$temp_dir/parsed"
done < "$temp_dir/SHA256SUMS"
# Catches NUL bytes discarded by Bash read and unterminated final entries.
cmp -s "$temp_dir/parsed" "$temp_dir/SHA256SUMS" || die 'Manifest must be exact newline-terminated ASCII checksum records'
for extension in "${extensions[@]}"; do
  found=0
  for filename in "${files[@]+${files[@]}}"; do
    [[ "$filename" != "new-york.osrm.$extension" ]] || found=1
  done
  [[ "$found" == 1 ]] || die 'Manifest missing required artifact'
done
for ((i=0; i<${#files[@]}; i++)); do
  filename="${files[i]}"
  get_object "$filename" "$temp_dir/$filename" >/dev/null
  [[ -f "$temp_dir/$filename" && ! -L "$temp_dir/$filename" && -s "$temp_dir/$filename" ]] || die 'Unsafe or empty downloaded artifact'
  size="$(wc -c < "$temp_dir/$filename")"
  [[ "$size" -le 5368709120 ]] || die 'Artifact exceeds publisher size limit'
  actual="$("${hash_command[@]}" "$temp_dir/$filename")"; actual="${actual%% *}"
  [[ "$actual" == "${hashes[i]}" ]] || die 'Artifact checksum mismatch'
done
rm "$temp_dir/parsed"
[[ ! -e "$destination" && ! -L "$destination" ]] || die 'Destination appeared during download'
# All cooperating starts hold the lock; no other user can write the private parent.
mv "$temp_dir" "$destination"
temp_dir=''; published=1
rmdir "$lock"
# Without execfail, noninteractive Bash can terminate on exec failure without
# running EXIT cleanup. Return control explicitly on failed process replacement.
shopt -s execfail
[[ -f "$osrm_executable" && -x "$osrm_executable" ]] || die 'OSRM executable became unavailable during startup'
exec "$osrm_executable" --algorithm mld --port 5001 "$destination/new-york.osrm" || {
  # exec can reset traps even when process replacement fails. Clean explicitly.
  cleanup
  die 'Unable to exec OSRM'
}