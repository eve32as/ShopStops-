#!/usr/bin/env bash
# Fresh disposable CI database ONLY, never a production migration runner.
set -euo pipefail
export LC_ALL=C
[[ $# -eq 0 && "${PGHOST:-}" =~ ^(localhost|127\.0\.0\.1)$ && "${PGDATABASE:-}" == shopstops_ci && "${PGUSER:-}" == shopstops_ci ]] || {
  printf 'Requires local disposable PGDATABASE=shopstops_ci and PGUSER=shopstops_ci; no arguments.\n' >&2
  exit 1
}
repo_root="$(cd "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
psql_command=(psql -X --no-password --set=ON_ERROR_STOP=1)
# Refuse any non-extension schema objects. The service may preinstall PostGIS.
empty="$("${psql_command[@]}" -Atc "SELECT (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','S','f') AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype='e')) + (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e'));")"
[[ "$empty" == 0 ]] || { printf 'CI database must have a fresh public schema.\n' >&2; exit 1; }
shopt -s nullglob
migrations=("$repo_root"/db/migrations/*.sql)
expected=(001_delivery_foundation 002_unified_marketplace 003_stripe_checkout_payments 004_billing_refunds_disputes 005_driver_stop_progress 006_catalog_orders 007_merchant_catalog_management 008_finance_costs 009_priority_test_checkout 010_shopper_completion 011_extra_pickup_test_checkout)
[[ ${#migrations[@]} -eq ${#expected[@]} ]] || { printf 'Review CI migration ordering when the migration set changes.\n' >&2; exit 1; }
for ((i=0; i<${#migrations[@]}; i++)); do
  [[ "${migrations[i]##*/}" == "${expected[i]}.sql" ]] || { printf 'Unexpected migration ordering.\n' >&2; exit 1; }
done
for migration in "${migrations[@]}"; do
  # Inspected 001-011: no top-level BEGIN/COMMIT, no concurrent indexes.
  "${psql_command[@]}" --single-transaction --file="$migration"
done
fixtures=("$repo_root"/db/tests/*.sql)
[[ ${#fixtures[@]} -gt 0 ]] || { printf 'No SQL fixtures found.\n' >&2; exit 1; }
for fixture in "${fixtures[@]}"; do
  # Fixtures own BEGIN/ROLLBACK; do NOT add --single-transaction here.
  "${psql_command[@]}" --file="$fixture"
done
printf 'All ordered migrations and SQL fixtures passed on the disposable database.\n'