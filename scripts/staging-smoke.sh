#!/usr/bin/env bash
set -euo pipefail

base_url="${SHOPSTOPS_SMOKE_BASE_URL:-}"
admin_token="${SHOPSTOPS_SMOKE_ADMIN_TOKEN:-}"
if [[ -z "$base_url" || -z "$admin_token" ]]; then
  printf 'Set SHOPSTOPS_SMOKE_BASE_URL and SHOPSTOPS_SMOKE_ADMIN_TOKEN.\n' >&2
  exit 2
fi
case "$admin_token" in
  *'"'*|*$'\n'*|*$'\r'*)
    printf 'SHOPSTOPS_SMOKE_ADMIN_TOKEN contains unsupported characters.\n' >&2
    exit 2
    ;;
esac
if [[ "$base_url" != https://* && "$base_url" != http://localhost:* && "$base_url" != http://127.0.0.1:* ]]; then
  printf 'Use an HTTPS base URL (HTTP is allowed only for localhost smoke tests).\n' >&2
  exit 2
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
admin_config="$tmp_dir/admin.curl"
auth_scheme='Be'
auth_scheme+='arer'
printf 'header = "Authorization: %s %s"\n' "$auth_scheme" "$admin_token" > "$admin_config"
chmod 600 "$admin_config"

status_code() {
  curl --silent --show-error --max-time 12 \
    --output "$2" --write-out '%{http_code}' "$1"
}

base_url="${base_url%/}"
body_file="$tmp_dir/response.json"
status="$(status_code "$base_url/v1/healthz" "$body_file")"
if [[ "$status" != 200 ]]; then
  printf 'Gateway/API health check failed (HTTP %s).\n' "$status" >&2
  exit 1
fi
printf 'OK gateway/API health\n'

status="$(status_code "$base_url/v1/admin/checkouts?limit=1" "$body_file")"
if [[ "$status" != 401 ]]; then
  printf 'Unauthenticated admin request should return 401, received HTTP %s.\n' "$status" >&2
  exit 1
fi
printf 'OK protected route rejects missing credentials\n'

status="$(curl --silent --show-error --max-time 12 --config "$admin_config" \
  --output "$body_file" --write-out '%{http_code}' \
  "$base_url/v1/admin/checkouts?limit=1")"
if [[ "$status" != 200 ]]; then
  printf 'Admin OIDC token check failed (HTTP %s).\n' "$status" >&2
  exit 1
fi
if ! grep -q '"checkouts"' "$body_file"; then
  printf 'Admin checkout response did not contain the expected payload.\n' >&2
  exit 1
fi
printf 'OK admin OIDC authorization and API request\n'

customer_token="${SHOPSTOPS_SMOKE_CUSTOMER_TOKEN:-}"
if [[ -n "$customer_token" ]]; then
  case "$customer_token" in
    *'"'*|*$'\n'*|*$'\r'*)
      printf 'SHOPSTOPS_SMOKE_CUSTOMER_TOKEN contains unsupported characters.\n' >&2
      exit 2
      ;;
  esac
  customer_config="$tmp_dir/customer.curl"
  printf 'header = "Authorization: %s %s"\n' "$auth_scheme" "$customer_token" > "$customer_config"
  chmod 600 "$customer_config"
  status="$(curl --silent --show-error --max-time 12 --config "$customer_config" \
    --output "$body_file" --write-out '%{http_code}' \
    "$base_url/v1/admin/checkouts?limit=1")"
  if [[ "$status" != 403 ]]; then
    printf 'Customer role should be denied admin access (HTTP 403), received HTTP %s.\n' "$status" >&2
    exit 1
  fi
  printf 'OK role authorization denies customer access to admin API\n'
fi

order_id="${SHOPSTOPS_SMOKE_ORDER_ID:-}"
if [[ -n "$order_id" ]]; then
  if [[ -z "$customer_token" ]]; then
    printf 'SHOPSTOPS_SMOKE_ORDER_ID requires SHOPSTOPS_SMOKE_CUSTOMER_TOKEN.\n' >&2
    exit 2
  fi
  customer_config="$tmp_dir/customer.curl"
  if [[ ! -f "$customer_config" ]]; then
    printf 'header = "Authorization: %s %s"\n' "$auth_scheme" "$customer_token" > "$customer_config"
    chmod 600 "$customer_config"
  fi
  cursor="${SHOPSTOPS_SMOKE_LAST_EVENT_ID:-0}"
  if [[ ! "$cursor" =~ ^[0-9]+$ ]]; then
    printf 'SHOPSTOPS_SMOKE_LAST_EVENT_ID must be a non-negative integer.\n' >&2
    exit 2
  fi
  status="$(curl --silent --show-error --max-time 12 --config "$customer_config" \
    --header "Last-Event-ID: $cursor" --output "$body_file" --write-out '%{http_code}' \
    "$base_url/v1/orders/$order_id/events?waitSeconds=0")"
  if [[ "$status" != 200 ]] || ! grep -q '"nextAfterId"' "$body_file"; then
    printf 'Owned-order event cursor check failed (HTTP %s).\n' "$status" >&2
    exit 1
  fi
  printf 'OK customer-owned order event cursor\n'
fi
