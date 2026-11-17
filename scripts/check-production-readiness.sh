#!/usr/bin/env bash
# Offline syntax/precondition checks only. NEVER authorizes deployment.
set -euo pipefail
export LC_ALL=C
source "${BASH_SOURCE[0]%/*}/staging-images-common.sh"
[[ $# -eq 0 ]] || die 'Use environment inputs only; no approval or bypass flags'

required=(AWS_REGION EXPECTED_AWS_ACCOUNT_ID GITHUB_OIDC_PROVIDER_ARN OSRM_ARTIFACT_BUCKET PRODUCTION_DOMAIN PUBLIC_GATEWAY_URL OIDC_ISSUER OIDC_AUDIENCE API_IMAGE ADMIN_IMAGE OSRM_IMAGE OSRM_DATASET_VERSION)
for name in "${required[@]}"; do
  [[ -n "${!name:-}" ]] || die "$name is required"
  validate_value "$name"
done
validate_identity_inputs
[[ "$EXPECTED_AWS_ACCOUNT_ID" != 123456789012 ]] || die 'Documentation sample account is not a release identity'
reject_aws_endpoint_overrides
[[ "$GITHUB_OIDC_PROVIDER_ARN" == "arn:aws:iam::${EXPECTED_AWS_ACCOUNT_ID}:oidc-provider/token.actions.githubusercontent.com" ]] || die 'GitHub OIDC provider must match the expected account'
validate_bucket_name "$OSRM_ARTIFACT_BUCKET"
[[ "$OSRM_ARTIFACT_BUCKET" == "shopstops-staging-osrm-${EXPECTED_AWS_ACCOUNT_ID}-${AWS_REGION}" ]] || die 'Dataset bucket must match the existing runtime contract; a production bucket needs separate reviewed changes'

dns_name() {
  local host="$1" label
  [[ ${#host} -le 253 && "$host" == *.* && "$host" =~ ^[a-z0-9.-]+$ ]] || return 1
  case "$host" in *..*|*.|.*|*.invalid|*.test|*.localhost|*.local|*.example|example.com|*.example.com|example.org|*.example.org|example.net|*.example.net) return 1 ;; esac
  [[ ! "$host" =~ ^[0-9.]+$ ]] || return 1
  [[ "${host##*.}" =~ ^[a-z]{2,}$ ]] || return 1
  local labels=()
  IFS=. read -r -a labels <<< "$host"
  for label in "${labels[@]}"; do
    [[ ${#label} -le 63 && "$label" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || return 1
  done
}
https_url() {
  local url="$1" host
  [[ ${#url} -le 2048 && "$url" =~ ^https://([a-z0-9.-]+)(/[A-Za-z0-9._~/-]*)?$ ]] || return 1
  host="${BASH_REMATCH[1]}"
  dns_name "$host" || return 1
  case "${url#https://}" in *//*|*/../*|*/..|*/./*|*/.) return 1 ;; esac
}
dns_name "$PRODUCTION_DOMAIN" || die 'PRODUCTION_DOMAIN must be a public DNS name, not a placeholder or address'
https_url "$PUBLIC_GATEWAY_URL" || die 'PUBLIC_GATEWAY_URL must be public HTTPS without credentials, ports, query or fragment'
gateway_host="${PUBLIC_GATEWAY_URL#https://}"; gateway_host="${gateway_host%%/*}"
[[ "$gateway_host" == "$PRODUCTION_DOMAIN" ]] || die 'Gateway hostname must equal PRODUCTION_DOMAIN'
https_url "$OIDC_ISSUER" || die 'OIDC_ISSUER must be a public HTTPS issuer without credentials, query or fragment'
[[ ${#OIDC_AUDIENCE} -le 256 && "$OIDC_AUDIENCE" =~ ^[A-Za-z0-9][A-Za-z0-9:/._-]*$ ]] || die 'OIDC_AUDIENCE must be a bounded nonempty identifier'
for name in API_IMAGE ADMIN_IMAGE OSRM_IMAGE; do
  image="${!name}"
  prefix="${EXPECTED_AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/"
  [[ "$image" == "$prefix"* ]] || die "$name must use ECR in the expected account and region"
  image="${image#"$prefix"}"
  [[ "$image" =~ ^[a-z0-9]+([._/-][a-z0-9]+)*@sha256:[a-f0-9]{64}$ ]] || die "$name must use a repository and immutable lowercase SHA256 digest, not a tag"
  digest="${image##*@sha256:}"
  [[ ! "$digest" =~ ^0+$ ]] || die "$name must not use a zero digest"
done
[[ ${#OSRM_DATASET_VERSION} -le 64 && "$OSRM_DATASET_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || die 'Dataset version must match the immutable publisher format'

printf '%s\n' \
  'INPUTS VALIDATED OFFLINE ONLY: existence, AWS authentication, image provenance and DNS/TLS are unverified.' \
  'UNRESOLVED gateway: identity/roles, actor-header stripping, internal-token isolation, CORS and public HTTPS.' \
  'UNRESOLVED ECS: reviewed infrastructure, private egress/discovery, task roles, health, capacity and dataset compatibility.' \
  'UNRESOLVED migrations: production migration ledger/locking, least-privilege roles, upgrade rehearsal and backup restore.' \
  'UNRESOLVED e2e: deployed customer/merchant/driver/admin, Stripe webhooks/reconciliation, real OSRM routes and rollback.' \
  'NOT PRODUCTION READY. No environment flags can clear these evidence gates. See docs/production-readiness.md.'
exit 2