#!/bin/sh
set -eu

: "${AWS_REGION:?AWS_REGION must match the ACM certificate region}"
: "${CERTIFICATE_ARN:?CERTIFICATE_ARN must identify the ACM certificate}"
: "${GATEWAY_HOSTNAME:?GATEWAY_HOSTNAME must be the public HTTPS hostname}"

case "$CERTIFICATE_ARN" in
  arn:aws:acm:"$AWS_REGION":????????????:certificate/*|\
  arn:aws-us-gov:acm:"$AWS_REGION":????????????:certificate/*|\
  arn:aws-cn:acm:"$AWS_REGION":????????????:certificate/*)
    ;;
  *)
    echo "CERTIFICATE_ARN must be an ACM certificate ARN in AWS_REGION." >&2
    exit 2
    ;;
esac

hostname=$(printf '%s' "$GATEWAY_HOSTNAME" | tr '[:upper:]' '[:lower:]')
if [ "$hostname" != "$GATEWAY_HOSTNAME" ] ||
  ! printf '%s\n' "$hostname" | grep -Eq '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'; then
  echo "GATEWAY_HOSTNAME must be a lowercase fully qualified DNS hostname." >&2
  exit 2
fi

for command in aws jq; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required command not found: $command" >&2
    exit 2
  fi
done

certificate=$(aws acm describe-certificate \
  --region "$AWS_REGION" \
  --certificate-arn "$CERTIFICATE_ARN" \
  --query 'Certificate.{Status:Status,DomainName:DomainName,SubjectAlternativeNames:SubjectAlternativeNames}' \
  --output json \
  --no-cli-pager)

if ! printf '%s\n' "$certificate" | jq -e --arg hostname "$hostname" '
  def covers($host):
    . as $cert
    | if ($cert | startswith("*.")) then
        ($cert[2:]) as $suffix
        | ($host | split(".")) as $host_labels
        | ($suffix | split(".")) as $suffix_labels
        | (($host_labels | length) == (($suffix_labels | length) + 1))
          and ($host | endswith("." + $suffix))
      else
        . == $host
      end;
  .Status == "ISSUED"
  and ([.DomainName] + (.SubjectAlternativeNames // [])) as $names
  | any($names[]; covers($hostname))
' >/dev/null; then
  echo "ACM certificate must be ISSUED and cover GATEWAY_HOSTNAME." >&2
  exit 1
fi

printf 'ACM certificate is issued and covers %s in %s.\n' "$hostname" "$AWS_REGION"
