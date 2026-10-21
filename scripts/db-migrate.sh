#!/bin/sh
set -eu

: "${PGHOST:?PGHOST must identify the private database endpoint}"
: "${PGPORT:=5432}"
: "${PGDATABASE:?PGDATABASE must identify the target database}"
: "${PGUSER:?PGUSER must be the privileged migration operator}"
: "${PGSSLMODE:=verify-full}"
: "${SHOPSTOPS_DB_APP_PASSWORD:?SHOPSTOPS_DB_APP_PASSWORD must be supplied by the secret manager}"
: "${SHOPSTOPS_MIGRATIONS_DIR:=$(dirname "$0")/../db/migrations}"

export PGPORT PGSSLMODE

if [ "$PGUSER" = "shopstops_app" ]; then
  echo "Refusing to run migrations as the application role." >&2
  exit 2
fi

if [ "${#SHOPSTOPS_DB_APP_PASSWORD}" -lt 32 ]; then
  echo "SHOPSTOPS_DB_APP_PASSWORD must contain at least 32 characters." >&2
  exit 2
fi

command -v psql >/dev/null 2>&1 || {
  echo "psql is required." >&2
  exit 2
}
command -v sha256sum >/dev/null 2>&1 || {
  echo "sha256sum is required." >&2
  exit 2
}

set --
found_migration=false
for migration in "$SHOPSTOPS_MIGRATIONS_DIR"/*.sql; do
  [ -f "$migration" ] || continue
  filename=${migration##*/}
  case "$filename" in
    [0-9][0-9][0-9]_[a-z0-9_]*.sql) ;;
    *)
      echo "Invalid migration filename: $filename" >&2
      exit 2
      ;;
  esac
  set -- "$@" "$migration"
  found_migration=true
done

if [ "$found_migration" != true ]; then
  echo "No SQL migrations found in $SHOPSTOPS_MIGRATIONS_DIR." >&2
  exit 2
fi

{
  cat <<'SQL'
\set ON_ERROR_STOP on
\getenv app_password SHOPSTOPS_DB_APP_PASSWORD
\getenv database_name PGDATABASE
SELECT pg_advisory_lock(hashtextextended('shopstops-schema-migrations', 0));
SELECT format(
  'CREATE ROLE shopstops_migrator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT'
)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'shopstops_migrator')
\gexec
SELECT format('GRANT shopstops_migrator TO %I', current_user)
WHERE NOT pg_has_role(current_user, 'shopstops_migrator', 'MEMBER')
\gexec
SELECT format(
  'CREATE ROLE shopstops_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT PASSWORD %L',
  :'app_password'
)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'shopstops_app')
\gexec
ALTER ROLE shopstops_app
  WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT
  PASSWORD :'app_password';
GRANT CONNECT ON DATABASE :"database_name" TO shopstops_app;
CREATE EXTENSION IF NOT EXISTS postgis;
GRANT USAGE, CREATE ON SCHEMA public TO shopstops_migrator;
GRANT USAGE ON SCHEMA public TO shopstops_app;
SELECT format(
  'CREATE SCHEMA IF NOT EXISTS shopstops_meta AUTHORIZATION %I',
  'shopstops_migrator'
)
\gexec
REVOKE ALL ON SCHEMA shopstops_meta FROM PUBLIC;
SET ROLE shopstops_migrator;
CREATE TABLE IF NOT EXISTS shopstops_meta.schema_migrations (
  version text PRIMARY KEY,
  checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
  applied_at timestamptz NOT NULL DEFAULT now()
);
SQL

  for migration in "$@"; do
    filename=${migration##*/}
    version=${filename%.sql}
    checksum=$(sha256sum "$migration" | awk '{print $1}')

    printf "\nSELECT CASE WHEN EXISTS (\n"
    printf "  SELECT 1 FROM shopstops_meta.schema_migrations\n"
    printf "  WHERE version = '%s' AND checksum <> '%s'\n" "$version" "$checksum"
    printf ") THEN 'true' ELSE 'false' END AS checksum_mismatch\n"
    printf '%s\n' '\gset' '\if :checksum_mismatch'
    printf '%s\n' "\echo Migration checksum changed after applying $version." '\quit 3' '\endif'
    printf "\nSELECT CASE WHEN EXISTS (\n"
    printf "  SELECT 1 FROM shopstops_meta.schema_migrations WHERE version = '%s'\n" "$version"
    printf ") THEN 'true' ELSE 'false' END AS migration_applied\n"
    printf '%s\n' '\gset' '\if :migration_applied'
    printf '%s\n' "\echo Already applied: $version" '\else'
    printf "BEGIN;\n"
    cat "$migration"
    printf "\nINSERT INTO shopstops_meta.schema_migrations (version, checksum)\n"
    printf "VALUES ('%s', '%s');\n" "$version" "$checksum"
    printf "COMMIT;\n"
    printf '%s\n' "\echo Applied: $version" '\endif'
  done

  cat <<'SQL'
RESET ROLE;
GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA public TO shopstops_app;
GRANT DELETE ON TABLE checkout_invoice_lines TO shopstops_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO shopstops_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO shopstops_app;
SQL
} | psql --no-psqlrc --set=ON_ERROR_STOP=1 --dbname="$PGDATABASE"
