#!/bin/sh
set -eu

: "${PGHOST:?PGHOST must be set to the private PostgreSQL endpoint}"
: "${PGUSER:?PGUSER must be set to the authorized backup role}"
: "${PGDATABASE:?PGDATABASE must name the database to back up}"
: "${PGSSLMODE:?PGSSLMODE must be set to verify-full}"
: "${PGSSLROOTCERT:?PGSSLROOTCERT must identify the trusted database CA bundle}"
: "${BACKUP_S3_URI:?BACKUP_S3_URI must be an s3://bucket/prefix URI}"
: "${BACKUP_SSE_KMS_KEY_ID:?BACKUP_SSE_KMS_KEY_ID must identify the backup encryption key}"

if [ "$PGSSLMODE" != "verify-full" ] || [ ! -r "$PGSSLROOTCERT" ]; then
  echo "Require PGSSLMODE=verify-full and a readable PGSSLROOTCERT bundle." >&2
  exit 2
fi

case "$BACKUP_S3_URI" in
  s3://*) ;;
  *)
    echo "BACKUP_S3_URI must use s3://." >&2
    exit 2
    ;;
esac
destination=${BACKUP_S3_URI#s3://}
bucket=${destination%%/*}
if [ -z "$bucket" ] || [ "$destination" = "$bucket" ]; then
  echo "BACKUP_S3_URI must include a bucket and a non-empty prefix." >&2
  exit 2
fi
case "/${destination#"$bucket"/}/" in
  */../*|*/./*)
    echo "BACKUP_S3_URI must not contain dot path segments." >&2
    exit 2
    ;;
esac
case "$BACKUP_SSE_KMS_KEY_ID" in
  arn:aws:kms:*:*:key/*|arn:aws-us-gov:kms:*:*:key/*|arn:aws-cn:kms:*:*:key/*|alias/*)
    ;;
  *)
    case "$BACKUP_SSE_KMS_KEY_ID" in
      *[!A-Za-z0-9-]*|'')
        echo "BACKUP_SSE_KMS_KEY_ID must be a KMS key ARN, alias, or key ID." >&2
        exit 2
        ;;
    esac
    ;;
esac

for command in aws pg_dump pg_restore sha256sum openssl; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required command not found: $command" >&2
    exit 2
  fi
done

umask 077
temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/shopstops-pg-backup.XXXXXX")
cleanup() {
  rm -rf "$temp_dir"
}
trap cleanup EXIT HUP INT TERM

timestamp=$(date -u '+%Y%m%dT%H%M%SZ')
nonce=$(openssl rand -hex 8)
database_label=$(printf '%s' "$PGDATABASE" | tr -c 'A-Za-z0-9._-' '-')
object_name="shopstops-${database_label}-${timestamp}-${nonce}.dump"
object_uri="${BACKUP_S3_URI%/}/$object_name"
dump_file="$temp_dir/$object_name"
checksum_file="$temp_dir/$object_name.sha256"
verification_file="$temp_dir/verification.dump"

pg_dump --no-password --format=custom --no-owner --no-acl \
  --file="$dump_file" "$PGDATABASE"
pg_restore --list "$dump_file" >/dev/null
checksum=$(sha256sum "$dump_file" | awk '{print $1}')
printf '%s  %s\n' "$checksum" "$object_name" > "$checksum_file"

aws s3 cp "$dump_file" "$object_uri" \
  --sse aws:kms \
  --sse-kms-key-id "$BACKUP_SSE_KMS_KEY_ID" \
  --metadata "sha256=$checksum" \
  --only-show-errors
aws s3 cp "$checksum_file" "$object_uri.sha256" \
  --sse aws:kms \
  --sse-kms-key-id "$BACKUP_SSE_KMS_KEY_ID" \
  --only-show-errors
aws s3 cp "$object_uri" "$verification_file" --only-show-errors

downloaded_checksum=$(sha256sum "$verification_file" | awk '{print $1}')
if [ "$downloaded_checksum" != "$checksum" ]; then
  echo "Uploaded backup checksum verification failed." >&2
  exit 1
fi

printf 'Verified PostgreSQL backup: %s\nChecksum: %s\n' "$object_uri" "$checksum"
