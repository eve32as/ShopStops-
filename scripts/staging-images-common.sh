#!/usr/bin/env bash
# Shared validation only; sourcing this file performs no external commands.
die() { printf 'Error: %s\n' "$*" >&2; exit 1; }

# Uses Bash pattern matching rather than external normalization commands.
validate_value() {
  local name="$1" value="${!1}"
  [[ -n "$value" ]] || die "$name is required"
  case "$value" in
    *[Pp][Ll][Aa][Cc][Ee][Hh][Oo][Ll][Dd][Ee][Rr]*|*[Rr][Ee][Pp][Ll][Aa][Cc][Ee]*|*[Cc][Hh][Aa][Nn][Gg][Ee][Mm][Ee]*|*[Ee][Xx][Aa][Mm][Pp][Ll][Ee]*|*[Yy][Oo][Uu][Rr][_-]*|*'<'*|*'>'*)
      die "$name must not contain a placeholder" ;;
  esac
}

validate_identity_inputs() {
  validate_value AWS_REGION
  validate_value EXPECTED_AWS_ACCOUNT_ID
  [[ "$AWS_REGION" =~ ^(us|eu|ap|ca|sa|af|me|il|mx)-[a-z]+-[1-9][0-9]*$ ]] || die 'AWS_REGION must be a commercial AWS region'
  [[ "$EXPECTED_AWS_ACCOUNT_ID" =~ ^[0-9]{12}$ && "$EXPECTED_AWS_ACCOUNT_ID" != 000000000000 ]] || die 'EXPECTED_AWS_ACCOUNT_ID must be a real 12-digit account ID'
}

validate_bucket_name() {
  local value="$1"
  [[ ${#value} -ge 3 && ${#value} -le 63 && "$value" =~ ^[a-z0-9][a-z0-9-]*[a-z0-9]$ ]] || die 'Bucket names must be 3-63 lowercase letters, digits or hyphens'
  case "$value" in xn--*|sthree-*|amzn-s3-demo-*|*-s3alias|*--ol-s3|*--x-s3|*--table-s3) die 'Reserved bucket name' ;; esac
}

aws_read() {
  # Do not echo credentials, CLI diagnostics or caller-provided values.
  aws --region "$AWS_REGION" --no-cli-pager "$@" 2>/dev/null || die 'AWS verification failed; check credentials and read permissions directly in your terminal'
}

reject_aws_endpoint_overrides() {
  local name
  for name in ${!AWS_ENDPOINT_URL@}; do
    [[ -z "${!name}" ]] || die 'AWS endpoint overrides are not allowed'
  done
}

verify_account() {
  local actual
  actual="$(aws_read sts get-caller-identity --query Account --output text)" || exit 1
  [[ "$actual" == "$EXPECTED_AWS_ACCOUNT_ID" ]] || die 'AWS caller account does not match EXPECTED_AWS_ACCOUNT_ID'
}

verify_bucket_owner_region() {
  local bucket="$1" region
  aws_read s3api head-bucket --bucket "$bucket" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" >/dev/null || exit 1
  region="$(aws_read s3api get-bucket-location --bucket "$bucket" --expected-bucket-owner "$EXPECTED_AWS_ACCOUNT_ID" --query LocationConstraint --output text)" || exit 1
  [[ "$region" != None ]] || region=us-east-1
  [[ "$region" != EU ]] || region=eu-west-1
  [[ "$region" == "$AWS_REGION" ]] || die 'Bucket region does not match AWS_REGION'
}