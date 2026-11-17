#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
bash_bin="$(command -v bash)"
temp="$(mktemp -d /tmp/shopstops-readiness-tests.XXXXXXXX)"
trap 'rm -rf "$temp"' EXIT
mkdir "$temp/bin"
# Any attempted cloud/container/network command fails and leaves evidence.
for tool in aws docker terraform curl git; do
  printf '#!/bin/bash\nprintf "called\\n" >> "$TOOL_LOG"\nexit 99\n' > "$temp/bin/$tool"
  chmod +x "$temp/bin/$tool"
done
passed=0
fail() { cat "$temp/output" >&2; printf 'FAIL: %s\n' "$*" >&2; exit 1; }
reset() {
  # Synthetic fixtures, not claims of real account/domain/image existence.
  digest=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789
  config=(AWS_REGION=us-east-1 EXPECTED_AWS_ACCOUNT_ID=246813579024
    GITHUB_OIDC_PROVIDER_ARN=arn:aws:iam::246813579024:oidc-provider/token.actions.githubusercontent.com
    OSRM_ARTIFACT_BUCKET=shopstops-staging-osrm-246813579024-us-east-1
    PRODUCTION_DOMAIN=api.shopstops.com PUBLIC_GATEWAY_URL=https://api.shopstops.com/v1
    OIDC_ISSUER=https://identity.shopstops.com/tenant OIDC_AUDIENCE=shopstops-api
    "API_IMAGE=246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/api@sha256:$digest"
    "ADMIN_IMAGE=246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/admin@sha256:$digest"
    "OSRM_IMAGE=246813579024.dkr.ecr.us-east-1.amazonaws.com/shopstops/osrm@sha256:$digest"
    OSRM_DATASET_VERSION=ny-20261114)
  : > "$temp/tool-log"
}
check_exit() {
  local expected="$1"; shift
  local exit_code=0
  env -i PATH="$temp/bin" TOOL_LOG="$temp/tool-log" "${config[@]}" "$@" "$bash_bin" "$repo_root/scripts/check-production-readiness.sh" > "$temp/output" 2>&1 || exit_code=$?
  [[ "$exit_code" == "$expected" ]] || fail "expected exit $expected, got $exit_code"
  [[ ! -s "$temp/tool-log" ]] || fail 'preflight invoked an external tool'
  passed=$((passed + 1))
}
reset; check_exit 2
for gate in gateway ECS migrations e2e; do grep -q "UNRESOLVED $gate:" "$temp/output" || fail "missing $gate gate"; done
grep -q 'NOT PRODUCTION READY' "$temp/output" || fail 'misleading success output'
for name in AWS_REGION EXPECTED_AWS_ACCOUNT_ID GITHUB_OIDC_PROVIDER_ARN OSRM_ARTIFACT_BUCKET PRODUCTION_DOMAIN PUBLIC_GATEWAY_URL OIDC_ISSUER OIDC_AUDIENCE API_IMAGE ADMIN_IMAGE OSRM_IMAGE OSRM_DATASET_VERSION; do
  for value in '' PLACEHOLDER; do reset; check_exit 1 "$name=$value"; done
done
for entry in \
  'AWS_REGION=us-gov-west-1' 'EXPECTED_AWS_ACCOUNT_ID=000000000000' 'EXPECTED_AWS_ACCOUNT_ID=123456789012' \
  'EXPECTED_AWS_ACCOUNT_ID=123' 'GITHUB_OIDC_PROVIDER_ARN=arn:aws:iam::999999999999:oidc-provider/token.actions.githubusercontent.com' \
  'OSRM_ARTIFACT_BUCKET=another-bucket' 'AWS_ENDPOINT_URL_CUSTOM=https://untrusted.invalid' \
  'PRODUCTION_DOMAIN=localhost' 'PRODUCTION_DOMAIN=127.0.0.1' 'PRODUCTION_DOMAIN=api.invalid' \
  'PRODUCTION_DOMAIN=-api.shopstops.com' 'PRODUCTION_DOMAIN=api..shopstops.com' \
  'PRODUCTION_DOMAIN=api.shopstops.com.' 'PRODUCTION_DOMAIN=api.local' \
  'PRODUCTION_DOMAIN=api.1' 'PRODUCTION_DOMAIN=api.c' \
  'PUBLIC_GATEWAY_URL=http://api.shopstops.com' 'PUBLIC_GATEWAY_URL=https://other.shopstops.com' \
  'PUBLIC_GATEWAY_URL=https://user:pass@api.shopstops.com' 'PUBLIC_GATEWAY_URL=https://api.shopstops.com:443' \
  'PUBLIC_GATEWAY_URL=https://api.shopstops.com?token=x' 'PUBLIC_GATEWAY_URL=https://api.shopstops.com/#fragment' \
  'PUBLIC_GATEWAY_URL=https://api.shopstops.com/../v1' 'PUBLIC_GATEWAY_URL=https://api.shopstops.com//v1' \
  'OIDC_ISSUER=http://identity.shopstops.com' 'OIDC_ISSUER=https://localhost/tenant' \
  'OIDC_ISSUER=https://issuer.example.com' 'OIDC_ISSUER=https://identity.shopstops.com?key=x' \
  'OIDC_AUDIENCE=has space' 'OIDC_AUDIENCE=<audience>' \
  'API_IMAGE=shopstops-api:latest' 'ADMIN_IMAGE=246813579024.dkr.ecr.us-east-1.amazonaws.com/admin:latest' \
  'OSRM_IMAGE=999999999999.dkr.ecr.us-east-1.amazonaws.com/osrm@sha256:abcdef' \
  "API_IMAGE=246813579024.dkr.ecr.eu-west-1.amazonaws.com/api@sha256:$digest" \
  'API_IMAGE=246813579024.dkr.ecr.us-east-1.amazonaws.com/api@sha256:0000000000000000000000000000000000000000000000000000000000000000' \
  'OSRM_DATASET_VERSION=../escape' 'OSRM_DATASET_VERSION=v..1' 'OSRM_DATASET_VERSION=has space'; do
  reset; check_exit 1 "$entry"
done
reset; check_exit 1 "OIDC_AUDIENCE=$(printf '%257s' x | tr ' ' a)"
reset; check_exit 1 "OSRM_DATASET_VERSION=$(printf '%65s' x | tr ' ' a)"
# Self-reported approvals cannot turn the check green.
reset; check_exit 2 GATEWAY_READY=true ECS_READY=true MIGRATIONS_READY=true E2E_READY=true
exit_code=0
env -i PATH="$temp/bin" "${config[@]}" "$bash_bin" "$repo_root/scripts/check-production-readiness.sh" --approve > "$temp/output" 2>&1 || exit_code=$?
[[ "$exit_code" == 1 ]] || fail 'approval argument accepted'
passed=$((passed + 1))
printf 'All %s readiness offline tests passed; valid inputs still block release.\n' "$passed"