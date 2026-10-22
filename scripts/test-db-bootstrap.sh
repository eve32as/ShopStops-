#!/bin/sh
set -eu

: "${PGHOST:?PGHOST must identify the test database host}"
: "${PGPORT:=5432}"
: "${PGUSER:?PGUSER must be a test database administrator}"
: "${PGDATABASE:?PGDATABASE must identify the base test database}"
: "${PGSSLMODE:=disable}"
export PGPORT PGSSLMODE

repo_root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
migrations_dir="$repo_root/db/migrations"
test_prefix="shopstops_bootstrap_$$"
fresh_db="${test_prefix}_fresh"
upgrade_db="${test_prefix}_upgrade"
upgrade_migrations=$(mktemp -d)

cleanup() {
  PGDATABASE="$PGDATABASE" dropdb --if-exists --force "$fresh_db" >/dev/null 2>&1 || true
  PGDATABASE="$PGDATABASE" dropdb --if-exists --force "$upgrade_db" >/dev/null 2>&1 || true
  rm -rf "$upgrade_migrations"
}
trap cleanup EXIT HUP INT TERM

createdb "$fresh_db"
PGDATABASE="$fresh_db" SHOPSTOPS_MIGRATIONS_DIR="$migrations_dir" \
  "$repo_root/scripts/db-migrate.sh"

for sql_file in "$repo_root"/db/tests/*.sql; do
  psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="$fresh_db" --file="$sql_file"
done

PGDATABASE="$fresh_db" SHOPSTOPS_MIGRATIONS_DIR="$migrations_dir" \
  "$repo_root/scripts/db-migrate.sh"

PGDATABASE="$fresh_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 \
  --command="SELECT 1 FROM shopstops_meta.schema_migrations HAVING count(*) = 11"
PGDATABASE="$fresh_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 \
  --command="SELECT 1 FROM pg_roles WHERE rolname = 'shopstops_app' AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole"
PGDATABASE="$fresh_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 \
  --command="SELECT 1 WHERE has_table_privilege('shopstops_app', 'checkout_merchant_financials', 'INSERT') AND has_table_privilege('shopstops_app', 'checkout_financial_estimates', 'INSERT')"

app_read=$(PGUSER=shopstops_app PGPASSWORD="$SHOPSTOPS_DB_APP_PASSWORD" \
  PGDATABASE="$fresh_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 --tuples-only \
  --command="SELECT count(*) FROM shopstops_meta.schema_migrations" 2>&1 || true)
case "$app_read" in
  *"permission denied"*) ;;
  *)
    echo "Application role unexpectedly read migration metadata." >&2
    exit 1
    ;;
esac

app_data_read=$(PGUSER=shopstops_app PGPASSWORD="$SHOPSTOPS_DB_APP_PASSWORD" \
    PGDATABASE="$fresh_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 --tuples-only --no-align \
    --command="SELECT count(*) FROM delivery_zones" 2>&1 || true)
case "$app_data_read" in
    *[!0-9]*|'')
      echo "Application role could not read application data: $app_data_read" >&2
      exit 1
      ;;
esac

zone_id=10000000-0000-4000-8000-000000000001
store_id=20000000-0000-4000-8000-000000000001
order_id=30000000-0000-4000-8000-000000000001
PGDATABASE="$fresh_db" psql --no-psqlrc --quiet --set=ON_ERROR_STOP=1 \
  --command="INSERT INTO delivery_zones (id, name, boundary) VALUES ('$zone_id', 'application role smoke zone', ST_Multi(ST_MakeEnvelope(-74.1, 40.6, -73.8, 40.9, 4326)))"
PGDATABASE="$fresh_db" psql --no-psqlrc --quiet --set=ON_ERROR_STOP=1 \
  --command="INSERT INTO store_locations (id, name, address, location) VALUES ('$store_id', 'application role smoke store', '1 Test Way', ST_SetSRID(ST_MakePoint(-73.98, 40.75), 4326)::geography)"
PGDATABASE="$fresh_db" psql --no-psqlrc --quiet --set=ON_ERROR_STOP=1 \
  --command="INSERT INTO orders (id, customer_ref, store_location_id, delivery_zone_id, dropoff_location, delivery_window_start, delivery_window_end, volume_liters, state, ready_at) VALUES ('$order_id', 'bootstrap-smoke', '$store_id', '$zone_id', ST_SetSRID(ST_MakePoint(-73.97, 40.75), 4326)::geography, now(), now() + interval '2 hours', 2, 'ready', now())"
bundle_id=$(PGUSER=shopstops_app PGPASSWORD="$SHOPSTOPS_DB_APP_PASSWORD" \
  PGDATABASE="$fresh_db" psql --no-psqlrc --quiet --set=ON_ERROR_STOP=1 --tuples-only --no-align \
  --command="SELECT create_proximity_bundle('$order_id', 'bootstrap-app-role-smoke', 10)")
case "$bundle_id" in
  ????????-????-????-????-????????????) ;;
  *)
      echo "Application role could not perform an authorized bundle operation: $bundle_id" >&2
      exit 1
      ;;
esac

app_ddl=$(PGUSER=shopstops_app PGPASSWORD="$SHOPSTOPS_DB_APP_PASSWORD" \
  PGDATABASE="$fresh_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 \
  --command="CREATE TABLE public.shopstops_app_must_not_create_tables(id integer)" 2>&1 || true)
case "$app_ddl" in
  *"permission denied"*) ;;
  *)
    echo "Application role unexpectedly created a table." >&2
    exit 1
    ;;
esac

createdb "$upgrade_db"
for migration in "$migrations_dir"/00[1-8]_*.sql; do
  cp "$migration" "$upgrade_migrations/"
done
PGDATABASE="$upgrade_db" SHOPSTOPS_MIGRATIONS_DIR="$upgrade_migrations" \
  "$repo_root/scripts/db-migrate.sh"

PGDATABASE="$upgrade_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 \
  --command="INSERT INTO delivery_zones (name, boundary) VALUES ('upgrade preservation', ST_Multi(ST_MakeEnvelope(-74.1, 40.6, -73.8, 40.9, 4326)))"
PGDATABASE="$upgrade_db" SHOPSTOPS_MIGRATIONS_DIR="$migrations_dir" \
  "$repo_root/scripts/db-migrate.sh"
PGDATABASE="$upgrade_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 \
  --command="SELECT 1 FROM delivery_zones WHERE name = 'upgrade preservation'"
PGDATABASE="$upgrade_db" psql --no-psqlrc --set=ON_ERROR_STOP=1 \
  --command="SELECT 1 FROM shopstops_meta.schema_migrations HAVING count(*) = 11"

printf '\n-- checksum drift must fail closed\n' >> "$upgrade_migrations/008_merchant_order_fulfillment.sql"
if PGDATABASE="$upgrade_db" SHOPSTOPS_MIGRATIONS_DIR="$upgrade_migrations" \
  "$repo_root/scripts/db-migrate.sh" >/dev/null 2>&1; then
  echo "Migration checksum drift was not rejected." >&2
  exit 1
fi

printf 'Database bootstrap, fresh migration, permissions, idempotent rerun, and 008-to-011 upgrade passed.\n'
