#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
temp_dir="$(mktemp -d /tmp/shopstops-staging-tests.XXXXXXXX)"
trap 'rm -rf "$temp_dir"' EXIT
mkdir "$temp_dir/bin" "$temp_dir/dataset"
# Only temp fixtures are made executable; no source files are modified.
cp "$repo_root/tests/staging-images/fixtures/aws" "$temp_dir/bin/aws"
cp "$repo_root/tests/staging-images/fixtures/terraform" "$temp_dir/bin/terraform"
chmod +x "$temp_dir/bin/aws" "$temp_dir/bin/terraform"
export PATH="$temp_dir/bin:$PATH"
export FAKE_AWS_LOG="$temp_dir/aws.log" FAKE_TERRAFORM_LOG="$temp_dir/terraform.log"
export FAKE_UPLOAD_LOG="$temp_dir/uploads.log" FAKE_TEMP_LOG="$temp_dir/snapshot.log"
export FAKE_DATASET_DIR="$temp_dir/dataset"
# Avoid inherited AWS/Terraform settings changing the test; no cloud commands run.
unset TF_CLI_ARGS TF_CLI_ARGS_init TF_CLI_ARGS_plan TF_CLI_ARGS_apply TF_DATA_DIR TF_WORKSPACE TF_CLI_CONFIG_FILE
unset AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3 AWS_ENDPOINT_URL_STS AWS_ENDPOINT_URL_KMS AWS_ENDPOINT_URL_IAM
configure="$repo_root/scripts/configure-staging-images.sh"
publish="$repo_root/scripts/publish-osrm-data.sh"
passed=0

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
pass() { passed=$((passed + 1)); printf 'PASS: %s\n' "$*"; }
reset() {
  export AWS_REGION=us-east-1 EXPECTED_AWS_ACCOUNT_ID=123456789012
  export GITHUB_REPOSITORY=eve32as/ShopStops-
  export GITHUB_OIDC_PROVIDER_ARN=arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com
  export TF_STATE_BUCKET=approved-shopstops-state
  export TF_STATE_KEY=shopstops/staging-images/terraform.tfstate
  export TF_STATE_KMS_KEY_ARN=arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-123456789abc
  export OSRM_ARTIFACT_BUCKET=shopstops-staging-osrm-123456789012-us-east-1
  export FAKE_AWS_CASE=ok
  : > "$FAKE_AWS_LOG"; : > "$FAKE_TERRAFORM_LOG"; : > "$FAKE_UPLOAD_LOG"; : > "$FAKE_TEMP_LOG"
}
reject_without_tools() {
  if bash "$configure" > "$temp_dir/output" 2>&1; then fail 'invalid configuration accepted'; fi
  [[ ! -s "$FAKE_AWS_LOG" && ! -s "$FAKE_TERRAFORM_LOG" ]] || fail 'invalid inputs invoked external cloud tools'
}
for name in AWS_REGION EXPECTED_AWS_ACCOUNT_ID GITHUB_REPOSITORY GITHUB_OIDC_PROVIDER_ARN TF_STATE_BUCKET TF_STATE_KEY TF_STATE_KMS_KEY_ARN; do
  reset; export "$name=PLACEHOLDER"; reject_without_tools; pass "$name placeholder rejected before tools"
  reset; export "$name="; reject_without_tools; pass "$name empty rejected before tools"
done
for entry in \
  'AWS_REGION=us-gov-west-1' \
  'EXPECTED_AWS_ACCOUNT_ID=000000000000' \
  'GITHUB_REPOSITORY=another/repo' \
  'GITHUB_OIDC_PROVIDER_ARN=arn:aws:iam::999999999999:oidc-provider/token.actions.githubusercontent.com' \
  'TF_STATE_BUCKET=bad.bucket' \
  'TF_STATE_KEY=other/terraform.tfstate' \
  'TF_STATE_KMS_KEY_ARN=arn:aws:kms:eu-west-1:123456789012:key/12345678-1234-1234-1234-123456789abc' \
  'TF_STATE_KMS_KEY_ARN=arn:aws:kms:us-east-1:999999999999:key/12345678-1234-1234-1234-123456789abc'; do
  reset; export "$entry"; reject_without_tools; pass "malformed or unapproved ${entry%%=*} rejected"
done
reset
export TF_CLI_ARGS_apply=-auto-approve
reject_without_tools
unset TF_CLI_ARGS_apply
pass 'Terraform automatic approval override rejected'
reset
if bash "$configure" destroy > "$temp_dir/output" 2>&1; then fail 'destroy accepted'; fi
[[ ! -s "$FAKE_AWS_LOG" ]] || fail 'unsupported mode invoked AWS'
pass 'unsupported mode rejected'

for scenario in wrong-account sts-error wrong-owner wrong-region unversioned unblocked public-policy wrong-encryption disabled-key wrong-provider wrong-audience; do
  reset; export FAKE_AWS_CASE="$scenario"
  if bash "$configure" > "$temp_dir/output" 2>&1; then fail "$scenario accepted"; fi
  [[ ! -s "$FAKE_TERRAFORM_LOG" ]] || fail "$scenario invoked Terraform"
  pass "$scenario blocks Terraform"
done
reset
bash "$configure" > "$temp_dir/output" 2>&1 || fail 'valid mocked plan failed'
grep -q -- '-backend-config=use_lockfile=true' "$FAKE_TERRAFORM_LOG" || fail 'backend locking missing'
grep -q -- '-backend-config=encrypt=true' "$FAKE_TERRAFORM_LOG" || fail 'backend encryption missing'
grep -q -- '-backend-config=expected_bucket_owner=123456789012' "$FAKE_TERRAFORM_LOG" || fail 'backend owner missing'
grep -q 'staging-images workspace select default' "$FAKE_TERRAFORM_LOG" || fail 'default workspace missing'
grep -q 'staging-images plan -input=false' "$FAKE_TERRAFORM_LOG" || fail 'default is not plan'
if grep -q ' apply' "$FAKE_TERRAFORM_LOG"; then fail 'default applied'; fi
pass 'default plan uses isolated encrypted locked backend'
reset
bash "$configure" apply > "$temp_dir/output" 2>&1 || fail 'mocked apply failed'
grep -q 'staging-images apply$' "$FAKE_TERRAFORM_LOG" || fail 'explicit apply missing'
if grep -q 'auto-approve' "$FAKE_TERRAFORM_LOG"; then fail 'apply auto approved'; fi
pass 'explicit apply preserves interactive approval'

# Small fake MLD files, not the real multi-GB dataset.
extensions=(cell_metrics cells cnbg cnbg_to_ebg datasource_names ebg ebg_nodes edges enw fileIndex geometry icd maneuver_overrides mldgr names nbg_nodes partition properties ramIndex restrictions timestamp tld tls turn_duration_penalties turn_penalties_index turn_weight_penalties)
for extension in "${extensions[@]}"; do printf 'fixture %s\n' "$extension" > "$FAKE_DATASET_DIR/new-york.osrm.$extension"; done
printf 'must never ship\n' > "$FAKE_DATASET_DIR/new-york.osm.pbf"
printf 'must never ship\n' > "$FAKE_DATASET_DIR/new-york.osrm.unexpected"
for version in '../escape' 'v/other' 'v..1' '-bad' 'has space' 'PLACEHOLDER'; do
  reset
  if bash "$publish" "$version" "$FAKE_DATASET_DIR" > "$temp_dir/output" 2>&1; then fail 'unsafe version accepted'; fi
  [[ ! -s "$FAKE_AWS_LOG" ]] || fail 'unsafe version invoked AWS'
  pass 'unsafe version rejected before AWS'
done
reset
mv "$FAKE_DATASET_DIR/new-york.osrm.cells" "$temp_dir/cells"
if bash "$publish" v1 "$FAKE_DATASET_DIR" > "$temp_dir/output" 2>&1; then fail 'incomplete dataset accepted'; fi
[[ ! -s "$FAKE_AWS_LOG" ]] || fail 'incomplete dataset invoked AWS'
mv "$temp_dir/cells" "$FAKE_DATASET_DIR/new-york.osrm.cells"
pass 'missing required runtime artifact rejected'
reset
mv "$FAKE_DATASET_DIR/new-york.osrm.cells" "$temp_dir/cells"
ln -s "$temp_dir/cells" "$FAKE_DATASET_DIR/new-york.osrm.cells"
if bash "$publish" v1 "$FAKE_DATASET_DIR" > "$temp_dir/output" 2>&1; then fail 'symlink accepted'; fi
[[ ! -s "$FAKE_AWS_LOG" ]] || fail 'symlink invoked AWS'
rm "$FAKE_DATASET_DIR/new-york.osrm.cells"
mv "$temp_dir/cells" "$FAKE_DATASET_DIR/new-york.osrm.cells"
pass 'symlinked artifact rejected'
for scenario in wrong-account wrong-owner version-exists list-denied; do
  reset; export FAKE_AWS_CASE="$scenario"
  if bash "$publish" v1 "$FAKE_DATASET_DIR" > "$temp_dir/output" 2>&1; then fail "upload accepted $scenario"; fi
  [[ ! -s "$FAKE_UPLOAD_LOG" ]] || fail "$scenario uploaded artifacts"
  pass "upload rejects $scenario without writes"
done
reset
export FAKE_AWS_CASE=upload-failure
if bash "$publish" v1 "$FAKE_DATASET_DIR" > "$temp_dir/output" 2>&1; then fail 'failed upload accepted'; fi
if grep -q -- '--key datasets/new-york/v1/SHA256SUMS' "$FAKE_AWS_LOG"; then fail 'manifest written after failure'; fi
pass 'upload failure never writes completion marker'
reset
bash "$publish" v1 "$FAKE_DATASET_DIR" > "$temp_dir/output" 2>&1 || { cat "$temp_dir/output"; fail 'mocked upload failed'; }
[[ "$(tail -n 1 "$FAKE_UPLOAD_LOG")" == datasets/new-york/v1/SHA256SUMS ]] || fail 'manifest not last'
[[ "$(wc -l < "$FAKE_UPLOAD_LOG")" -eq $((${#extensions[@]} + 1)) ]] || fail 'unexpected upload count'
if grep -Eq 'pbf|unexpected|--delete' "$FAKE_UPLOAD_LOG" "$FAKE_AWS_LOG"; then fail 'unsafe upload files or delete'; fi
[[ ! -e "$(cat "$FAKE_TEMP_LOG")" ]] || fail 'snapshot not cleaned'
[[ ! -e "$FAKE_DATASET_DIR/SHA256SUMS" ]] || fail 'manifest leaked into dataset'
pass 'runtime-only conditional uploads, verified SHA256, manifest last and temp cleanup'
printf '\nAll %s offline tests passed (fake AWS and Terraform only).\n' "$passed"