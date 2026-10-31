#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
terraform_dir="$repo_root/deploy/aws/staging-images"

required_vars=(
  AWS_REGION
  EXPECTED_AWS_ACCOUNT_ID
  GITHUB_REPOSITORY
  GITHUB_OIDC_PROVIDER_ARN
  TF_STATE_BUCKET
  TF_STATE_KEY
  TF_STATE_KMS_KEY_ARN
)

for name in "${required_vars[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    printf 'Missing required environment variable: %s\n' "$name" >&2
    exit 1
  fi
done

for command_name in aws gh jq terraform; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'Required command is not installed: %s\n' "$command_name" >&2
    exit 1
  fi
done

if [[ ! "$EXPECTED_AWS_ACCOUNT_ID" =~ ^[0-9]{12}$ ]]; then
  echo "EXPECTED_AWS_ACCOUNT_ID must be a 12-digit AWS account ID." >&2
  exit 1
fi

account_id="$(aws sts get-caller-identity --query Account --output text)"
if [[ "$account_id" != "$EXPECTED_AWS_ACCOUNT_ID" ]]; then
  echo "Active AWS credentials are for account $account_id, not $EXPECTED_AWS_ACCOUNT_ID." >&2
  exit 1
fi

gh auth status --hostname github.com
gh repo view "$GITHUB_REPOSITORY" --json nameWithOwner >/dev/null
default_branch="$(gh repo view "$GITHUB_REPOSITORY" --json defaultBranchRef --jq '.defaultBranchRef.name')"
if [[ -z "$default_branch" ]]; then
  echo "Could not determine the repository default branch." >&2
  exit 1
fi

terraform -chdir="$terraform_dir" init -input=false \
  -backend-config="bucket=$TF_STATE_BUCKET" \
  -backend-config="key=$TF_STATE_KEY" \
  -backend-config="region=$AWS_REGION" \
  -backend-config="kms_key_id=$TF_STATE_KMS_KEY_ARN" \
  -backend-config="encrypt=true" \
  -backend-config="use_lockfile=true"

terraform -chdir="$terraform_dir" apply -input=false \
  -var="aws_region=$AWS_REGION" \
  -var="expected_aws_account_id=$EXPECTED_AWS_ACCOUNT_ID" \
  -var="github_repository=$GITHUB_REPOSITORY" \
  -var="github_oidc_provider_arn=$GITHUB_OIDC_PROVIDER_ARN"

github_environment="$(terraform -chdir="$terraform_dir" output -raw github_environment)"
role_arn="$(terraform -chdir="$terraform_dir" output -raw image_publisher_role_arn)"
api_repository="$(terraform -chdir="$terraform_dir" output -raw api_repository_url)"
gateway_repository="$(terraform -chdir="$terraform_dir" output -raw gateway_repository_url)"
migration_repository="$(terraform -chdir="$terraform_dir" output -raw migration_repository_url)"

environment_payload="$(jq -n \
  '{deployment_branch_policy: {protected_branches: false, custom_branch_policies: true}}')"
gh api --method PUT \
  "repos/$GITHUB_REPOSITORY/environments/$github_environment" \
  --input - <<<"$environment_payload"

branch_policies="$(gh api --paginate \
  "repos/$GITHUB_REPOSITORY/environments/$github_environment/deployment-branch-policies")"
branch_policy_exists="$(jq --arg name "$default_branch" \
  '[.branch_policies[]? | select(.name == $name and .type == "branch")] | length' \
  <<<"$branch_policies")"
if [[ "$branch_policy_exists" == "0" ]]; then
  gh api --method POST \
    "repos/$GITHUB_REPOSITORY/environments/$github_environment/deployment-branch-policies" \
    -f "name=$default_branch" \
    -f type=branch
fi
while IFS= read -r policy_id; do
  [[ -n "$policy_id" ]] || continue
  gh api --method DELETE \
    "repos/$GITHUB_REPOSITORY/environments/$github_environment/deployment-branch-policies/$policy_id"
done < <(jq -r --arg name "$default_branch" \
  '.branch_policies[]? | select(.name != $name or .type != "branch") | .id' \
  <<<"$branch_policies")

gh variable set AWS_REGION --env "$github_environment" \
  --body "$AWS_REGION" --repo "$GITHUB_REPOSITORY"
gh variable set AWS_ROLE_ARN --env "$github_environment" \
  --body "$role_arn" --repo "$GITHUB_REPOSITORY"
gh variable set ECR_API_REPOSITORY --env "$github_environment" \
  --body "$api_repository" --repo "$GITHUB_REPOSITORY"
gh variable set ECR_GATEWAY_REPOSITORY --env "$github_environment" \
  --body "$gateway_repository" --repo "$GITHUB_REPOSITORY"
gh variable set ECR_MIGRATION_REPOSITORY --env "$github_environment" \
  --body "$migration_repository" --repo "$GITHUB_REPOSITORY"

printf 'Configured %s for %s (branch limited to %s).\n' \
  "$github_environment" "$GITHUB_REPOSITORY" "$default_branch"
printf 'Staging ECR repositories and OIDC role are provisioned in AWS account %s.\n' \
  "$EXPECTED_AWS_ACCOUNT_ID"
