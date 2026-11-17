#!/usr/bin/env bash
# Read-only, unauthenticated staging probes. Never accepts or sends real JWTs.
set -euo pipefail
export LC_ALL=C
source "${BASH_SOURCE[0]%/*}/staging-images-common.sh"
[[ $# -eq 0 ]] || die 'Use non-secret environment inputs only; no token/approval arguments'
for name in PUBLIC_ORIGIN STORE_LOCATION_ID; do validate_value "$name"; done
[[ "$PUBLIC_ORIGIN" =~ ^https://([a-z0-9.-]+)$ ]] || die 'PUBLIC_ORIGIN must be a bare public HTTPS origin without credentials, port, path, query or fragment'
host="${BASH_REMATCH[1]}"
[[ ${#host} -le 253 && "$host" == *.* && "${host##*.}" =~ ^[a-z]{2,63}$ ]] || die 'Use a public DNS hostname'
case "$host" in *..*|*.|.*|*.invalid|*.test|*.localhost|*.local|*.internal|*.example|example.com|*.example.com|example.org|*.example.org|example.net|*.example.net) die 'Unsafe or placeholder public hostname' ;; esac
IFS=. read -r -a labels <<< "$host"
for label in "${labels[@]}"; do
  [[ ${#label} -le 63 && "$label" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || die 'Malformed public hostname'
done
[[ "$STORE_LOCATION_ID" =~ ^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$ ]] || die 'STORE_LOCATION_ID must be a literal UUID'
if [[ -n "${OSRM_PRIVATE_URL:-}${OSRM_COORDINATES:-}" ]]; then
  # Deliberate private-HTTP exception for the exact native CloudMap service.
  # Must run inside the approved VPC. Never expose OSRM publicly to run this.
  [[ "${OSRM_PRIVATE_URL:-}" == http://osrm.shopstops.internal:5001 ]] || die 'OSRM_PRIVATE_URL must be the exact private CloudMap origin'
  [[ "${OSRM_COORDINATES:-}" =~ ^-?[0-9]{1,3}(\.[0-9]{1,8})?,-?[0-9]{1,2}(\.[0-9]{1,8})?\;-?[0-9]{1,3}(\.[0-9]{1,8})?,-?[0-9]{1,2}(\.[0-9]{1,8})?$ ]] || die 'Supply exactly two explicit lon,lat coordinate pairs'
  awk -v coords="$OSRM_COORDINATES" 'BEGIN { n=split(coords,a,/[,;]/); exit !(n==4 && a[1]>=-180 && a[1]<=180 && a[3]>=-180 && a[3]<=180 && a[2]>=-90 && a[2]<=90 && a[4]>=-90 && a[4]<=90) }' || die 'Coordinates outside longitude/latitude bounds'
  command -v jq >/dev/null || die 'jq required for optional OSRM JSON validation'
fi
command -v curl >/dev/null || die 'curl is required'
umask 077
temp="$(mktemp -d /tmp/shopstops-live-services.XXXXXXXX)"
trap 'rm -rf "$temp"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
passed=0
probe() {
  local label="$1" expected="$2" url="$3" protocol="$4" code
  shift 4
  # --disable FIRST prevents .curlrc credential/proxy/debug overrides. No
  # redirects, proxy, TLS bypass, auth arguments, verbose output or body prints.
  code="$(curl --disable --silent --noproxy '*' --proto "=$protocol" --proto-redir '=https' \
    --max-redirs 0 --connect-timeout 5 --max-time 20 --max-filesize 4194304 \
    --request GET --output "$temp/body" --write-out '%{http_code}' "$@" "$url" 2>/dev/null)" || die "Probe failed: $label (transport error; details suppressed)"
  [[ "$code" == "$expected" ]] || die "Probe failed: $label (unexpected status; response suppressed)"
  passed=$((passed + 1))
  printf 'PASS: %s\n' "$label"
}
probe 'gateway database health' 200 "$PUBLIC_ORIGIN/v1/healthz" https
probe 'catalog requires identity' 401 "$PUBLIC_ORIGIN/v1/stores/$STORE_LOCATION_ID/catalog" https
probe 'spoofed internal actor/admin headers denied' 401 "$PUBLIC_ORIGIN/v1/stores/$STORE_LOCATION_ID/catalog" https \
  --header 'X-ShopStops-Actor: smoke-not-an-identity' --header 'X-ShopStops-Actor-Role: admin' --header 'X-Internal-API-Token: smoke-not-a-secret'
probe 'invalid bearer denied' 401 "$PUBLIC_ORIGIN/v1/stores/$STORE_LOCATION_ID/catalog" https --header 'Authorization: Bearer smoke-invalid-not-a-jwt'
probe 'merchant identity required' 401 "$PUBLIC_ORIGIN/v1/merchant/catalog" https
probe 'unknown gateway operation denied' 404 "$PUBLIC_ORIGIN/v1/not-an-operation" https
probe 'admin static health' 200 "$PUBLIC_ORIGIN/healthz" https
probe 'admin document' 200 "$PUBLIC_ORIGIN/" https
if [[ -n "${OSRM_PRIVATE_URL:-}" ]]; then
  probe 'private OSRM route response' 200 "$OSRM_PRIVATE_URL/route/v1/driving/$OSRM_COORDINATES?overview=false" http
  jq -e '.code == "Ok" and (.routes | type == "array" and length > 0) and (.routes[0].legs | length > 0) and (.routes[0].distance >= 0)' "$temp/body" >/dev/null 2>&1 || die 'OSRM route JSON invalid (response suppressed)'
fi
printf 'Passed %s read-only probes; identity, payments and production readiness remain unverified.\n' "$passed"