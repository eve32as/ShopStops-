#!/usr/bin/env bash
set -euo pipefail
export AWS_PAGER='' AWS_CLI_AUTO_PROMPT=off AWS_IGNORE_CONFIGURED_ENDPOINT_URLS=true
source "${BASH_SOURCE[0]%/*}/staging-images-common.sh"

[[ $# -ge 1 && $# -le 2 ]] || die 'Usage: bash scripts/publish-osrm-data.sh <version> [dataset-directory]'
version="$1"
AWS_REGION="${AWS_REGION:-}"
EXPECTED_AWS_ACCOUNT_ID="${EXPECTED_AWS_ACCOUNT_ID:-}"
OSRM_ARTIFACT_BUCKET="${OSRM_ARTIFACT_BUCKET:-}"
validate_identity_inputs
validate_value OSRM_ARTIFACT_BUCKET
validate_value version
[[ ${#version} -le 64 && "$version" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || die 'Version must be 1-64 safe letters, digits, underscores or hyphens; no dots or slashes'
validate_bucket_name "$OSRM_ARTIFACT_BUCKET"
[[ "$OSRM_ARTIFACT_BUCKET" == "shopstops-staging-osrm-${EXPECTED_AWS_ACCOUNT_ID}-${AWS_REGION}" ]] || die 'Only the designated staging artifact bucket is allowed'
[[ -z "${AWS_ENDPOINT_URL:-}${AWS_ENDPOINT_URL_S3:-}${AWS_ENDPOINT_URL_STS:-}" ]] || die 'AWS endpoint overrides are not allowed'

repo_root="$(cd "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
dataset_dir="${2:-$repo_root/data/osrm/north-america/us/new-york}"
[[ -d "$dataset_dir" && ! -L "$dataset_dir" ]] || die 'Dataset directory is missing or is a symlink'
dataset_dir="$(cd "$dataset_dir" && pwd -P)"
# Fixed allowlist matches this repository's generated MLD dataset. PBFs, hidden
# files, subdirectories, and arbitrary extensions are never included.
extensions=(cell_metrics cells cnbg cnbg_to_ebg datasource_names ebg ebg_nodes edges enw fileIndex geometry icd maneuver_overrides mldgr names nbg_nodes partition properties ramIndex restrictions timestamp tld tls turn_duration_penalties turn_penalties_index turn_weight_penalties)
files=()
for extension in "${extensions[@]}"; do
  filename="new-york.osrm.$extension"
  [[ -f "$dataset_dir/$filename" && ! -L "$dataset_dir/$filename" && -s "$dataset_dir/$filename" ]] || die "Missing, empty or symlinked required runtime artifact: $filename"
  files+=("$filename")
done
# Some OSRM versions also emit a base file; ship it only when present and safe.
if [[ -e "$dataset_dir/new-york.osrm" || -L "$dataset_dir/new-york.osrm" ]]; then
  [[ -f "$dataset_dir/new-york.osrm" && ! -L "$dataset_dir/new-york.osrm" && -s "$dataset_dir/new-york.osrm" ]] || die 'Unsafe or empty OSRM base file'
  files+=(new-york.osrm)
fi
command -v aws >/dev/null 2>&1 || die 'AWS CLI v2 with conditional PutObject support is required'
command -v openssl >/dev/null 2>&1 || die 'openssl is required for SHA256 checksums'
if command -v sha256sum >/dev/null 2>&1; then
  hash_command=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  hash_command=(shasum -a 256)
else
  die 'sha256sum or shasum is required'
fi
verify_account
verify_bucket_owner_region "$OSRM_ARTIFACT_BUCKET"
prefix="datasets/new-york/$version/"
# Listing rather than interpreting HeadObject errors: ALL AWS errors, including
# 403, fail closed. This manual operator needs read/list permissions, unlike CI.
count="$(aws_read s3api list-objects-v2 --bucket "$OSRM_ARTIFACT_BUCKET" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" --prefix "$prefix" --max-keys 1 --no-paginate --query KeyCount --output text)" || exit 1
[[ "$count" == 0 ]] || die 'Version already has objects or a completion manifest; choose a new version'

# Snapshot outside the dataset, so the manifest describes exactly uploaded bytes.
# Use /tmp intentionally: caller TMPDIR must not place the manifest in the dataset.
temp_dir="$(mktemp -d /tmp/shopstops-osrm-publish.XXXXXXXX)"
trap 'rm -rf "$temp_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
umask 077
for filename in "${files[@]}"; do
  cp "$dataset_dir/$filename" "$temp_dir/$filename"
  size="$(wc -c < "$temp_dir/$filename")"
  # Single-request PutObject only: no multipart permissions, no partial MPU state.
  [[ "$size" -gt 0 && "$size" -le 5368709120 ]] || die 'Each artifact must be nonempty and no larger than 5 GiB'
done
manifest="$temp_dir/SHA256SUMS"
(cd "$temp_dir" && "${hash_command[@]}" "${files[@]}") > "$manifest"

upload() {
  local filename="$1" checksum
  checksum="$(openssl dgst -sha256 -binary "$temp_dir/$filename" | openssl base64 -A)"
  [[ ${#checksum} -eq 44 ]] || die 'SHA256 checksum generation failed'
  aws --region "$AWS_REGION" --no-cli-pager s3api put-object \
    --bucket "$OSRM_ARTIFACT_BUCKET" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" \
    --key "$prefix$filename" --body "$temp_dir/$filename" \
    --server-side-encryption AES256 --if-none-match '*' \
    --checksum-algorithm SHA256 --checksum-sha256 "$checksum" \
    >/dev/null 2>&1 || die 'Conditional upload failed; no completion marker is written. Use a new version after investigating in your terminal'
}
for filename in "${files[@]}"; do upload "$filename"; done
upload SHA256SUMS
printf 'Published runtime artifacts; SHA256SUMS was uploaded last as the completion marker.\n'