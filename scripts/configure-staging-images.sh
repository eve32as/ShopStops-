#!/usr/bin/env bash
set -euo pipefail
export AWS_PAGER='' AWS_CLI_AUTO_PROMPT=off AWS_IGNORE_CONFIGURED_ENDPOINT_URLS=true
source "${BASH_SOURCE[0]%/*}/staging-images-common.sh"

[[ $# -le 1 ]] || die 'Usage: bash scripts/configure-staging-images.sh [plan|apply]'
mode="${1:-plan}"
[[ "$mode" == plan || "$mode" == apply ]] || die 'Only plan or explicit apply is supported'

# All input validation precedes external commands, including tool discovery.
AWS_REGION="${AWS_REGION:-}"
EXPECTED_AWS_ACCOUNT_ID="${EXPECTED_AWS_ACCOUNT_ID:-}"
GITHUB_REPOSITORY="${GITHUB_REPOSITORY:-}"
GITHUB_OIDC_PROVIDER_ARN="${GITHUB_OIDC_PROVIDER_ARN:-}"
TF_STATE_BUCKET="${TF_STATE_BUCKET:-}"
TF_STATE_KEY="${TF_STATE_KEY:-}"
TF_STATE_KMS_KEY_ARN="${TF_STATE_KMS_KEY_ARN:-}"
validate_identity_inputs
for name in GITHUB_REPOSITORY GITHUB_OIDC_PROVIDER_ARN TF_STATE_BUCKET TF_STATE_KEY TF_STATE_KMS_KEY_ARN; do
  validate_value "$name"
done
[[ "$GITHUB_REPOSITORY" == eve32as/ShopStops- ]] || die 'Only eve32as/ShopStops- is approved for this package'
[[ "$GITHUB_OIDC_PROVIDER_ARN" == "arn:aws:iam::${EXPECTED_AWS_ACCOUNT_ID}:oidc-provider/token.actions.githubusercontent.com" ]] || die 'OIDC ARN must reference the existing GitHub provider in the expected account'
validate_bucket_name "$TF_STATE_BUCKET"
[[ "$TF_STATE_KEY" == shopstops/staging-images/terraform.tfstate ]] || die 'Only the designated staging-images state key is allowed'
[[ "$TF_STATE_KMS_KEY_ARN" =~ ^arn:aws:kms:${AWS_REGION}:${EXPECTED_AWS_ACCOUNT_ID}:key/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] || die 'State KMS key ARN must identify a key in the expected account and region'
[[ -z "${TF_CLI_ARGS:-}${TF_CLI_ARGS_init:-}${TF_CLI_ARGS_plan:-}${TF_CLI_ARGS_apply:-}" ]] || die 'TF_CLI_ARGS overrides are not allowed'
[[ -z "${TF_DATA_DIR:-}${TF_WORKSPACE:-}${TF_CLI_CONFIG_FILE:-}" ]] || die 'Terraform data, workspace and CLI configuration overrides are not allowed'
[[ -z "${AWS_ENDPOINT_URL:-}${AWS_ENDPOINT_URL_S3:-}${AWS_ENDPOINT_URL_STS:-}${AWS_ENDPOINT_URL_KMS:-}${AWS_ENDPOINT_URL_IAM:-}" ]] || die 'AWS endpoint overrides are not allowed'
command -v aws >/dev/null 2>&1 || die 'AWS CLI v2 is required; install and authenticate separately in your terminal'
verify_account

# Approve the pre-existing backend; never bootstrap buckets or keys here.
verify_bucket_owner_region "$TF_STATE_BUCKET"
versioning="$(aws_read s3api get-bucket-versioning --bucket "$TF_STATE_BUCKET" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" --query Status --output text)" || exit 1
[[ "$versioning" == Enabled ]] || die 'State bucket versioning must be Enabled'
blocking="$(aws_read s3api get-public-access-block --bucket "$TF_STATE_BUCKET" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" --query 'PublicAccessBlockConfiguration.[BlockPublicAcls,IgnorePublicAcls,BlockPublicPolicy,RestrictPublicBuckets]' --output text)" || exit 1
[[ "$blocking" == $'True\tTrue\tTrue\tTrue' ]] || die 'State bucket must enable all four public access blocks'
public="$(aws_read s3api get-bucket-policy-status --bucket "$TF_STATE_BUCKET" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" --query PolicyStatus.IsPublic --output text)" || exit 1
[[ "$public" == False ]] || die 'State bucket must have a verified non-public bucket policy'
encryption="$(aws_read s3api get-bucket-encryption --bucket "$TF_STATE_BUCKET" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" --query 'ServerSideEncryptionConfiguration.Rules[].ApplyServerSideEncryptionByDefault.[SSEAlgorithm,KMSMasterKeyID]' --output text)" || exit 1
[[ "$encryption" == $'aws:kms\t'"$TF_STATE_KMS_KEY_ARN" ]] || die 'State bucket default encryption must use exactly the approved KMS key ARN'
key="$(aws_read kms describe-key --key-id "$TF_STATE_KMS_KEY_ARN" --query 'KeyMetadata.[Arn,KeyState,KeyUsage,KeySpec]' --output text)" || exit 1
[[ "$key" == "$TF_STATE_KMS_KEY_ARN"$'\tEnabled\tENCRYPT_DECRYPT\tSYMMETRIC_DEFAULT' ]] || die 'State KMS key must be an enabled symmetric encryption key'
provider="$(aws_read iam get-open-id-connect-provider --open-id-connect-provider-arn "$GITHUB_OIDC_PROVIDER_ARN" --query 'Url' --output text)" || exit 1
[[ "$provider" == token.actions.githubusercontent.com || "$provider" == https://token.actions.githubusercontent.com ]] || die 'Existing OIDC provider URL is not GitHub Actions'
audience="$(aws_read iam get-open-id-connect-provider --open-id-connect-provider-arn "$GITHUB_OIDC_PROVIDER_ARN" --query "contains(ClientIDList, 'sts.amazonaws.com')" --output text)" || exit 1
[[ "$audience" == True ]] || die 'Existing OIDC provider must include the STS audience'

command -v terraform >/dev/null 2>&1 || die 'Terraform >=1.10 and <2 is required; install separately'
repo_root="$(cd "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
module_dir="$repo_root/infra/aws/staging-images"
export TF_VAR_aws_region="$AWS_REGION" TF_VAR_expected_aws_account_id="$EXPECTED_AWS_ACCOUNT_ID"
export TF_VAR_github_repository="$GITHUB_REPOSITORY" TF_VAR_github_oidc_provider_arn="$GITHUB_OIDC_PROVIDER_ARN"
# Explicitly keep state separate from the full-app scaffold and use the default workspace.
terraform -chdir="$module_dir" init -reconfigure -input=false \
  -backend-config="bucket=$TF_STATE_BUCKET" -backend-config="key=$TF_STATE_KEY" \
  -backend-config="region=$AWS_REGION" -backend-config="expected_bucket_owner=$EXPECTED_AWS_ACCOUNT_ID" \
  -backend-config="allowed_account_ids=[\"$EXPECTED_AWS_ACCOUNT_ID\"]" \
  -backend-config="encrypt=true" -backend-config="kms_key_id=$TF_STATE_KMS_KEY_ARN" \
  -backend-config="use_lockfile=true"
terraform -chdir="$module_dir" workspace select default
terraform -chdir="$module_dir" validate
if [[ "$mode" == plan ]]; then
  terraform -chdir="$module_dir" plan -input=false
else
  # Deliberately interactive: no saved plan or auto-approval bypass.
  terraform -chdir="$module_dir" apply
fi