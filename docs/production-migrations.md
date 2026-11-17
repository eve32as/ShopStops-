# Bounded one-shot migrations — operator review required

[shopstops-migrate](../cmd/shopstops-migrate/main.go) uses the existing
pgx/database/sql dependency. It is a **separate operator-run executable**, never
API startup, CD, a backup verifier, or production approval. No database was
contacted or migrated during implementation. Existing authorization routes,
feature guards and release/security gates remain unchanged.

## Invocation contract

Build the executable from reviewed source through the normal Go release process;
package/run it as an approved one-shot process inside private networking with a
migration-specific role. No migration container/cloud job or permissions are
provisioned here. Use `--help` for the flag list. Do not run it in every API task.

- `DATABASE_URL` is environment-only. Inject privately; never print it, use shell
  tracing, put it in flags/history, or paste it into chat, tickets or CI artifacts.
- Production requires pgx-parsed **verify-full TLS** on every host/fallback.
  Configure `sslmode=verify-full` and an approved mounted `sslrootcert` bundle
  (or approved system trust). No downloads, hostname/CA bypass or plaintext
  fallback. `require`, `verify-ca`, `prefer` and disabled TLS are refused.
- Only explicit `--allow-local-test` permits disposable connections with parsed
  host exactly `localhost` or `127.0.0.1` (including every fallback), database
  **and** user exactly `shopstops_ci`. Only this mode permits `sslmode=disable`
  and omits mandatory production evidence labels. Never tunnel production under
  that identity or use the flag in production.
- `--mode=plan` is the default; `--mode=apply` is explicit. `--migrations-dir`
  defaults to repository-relative `db/migrations`; specify it outside repo root.
- Every apply requires `--expected-database`. Actual `current_database()` must
  match **before** catalog/history reads or DDL; URL database names are not
  treated as proof. Use the expected name in plan too. Independently confirm
  server/account/role: a database name is not globally unique target identity.
- Production apply requires nonempty `--backup-reference` and
  `--restore-test-reference`: non-secret evidence labels, 1–200 ASCII characters
  from letters, digits, dot, underscore, colon, slash and hyphen. Use ticket or
  artifact IDs, not credentials/presigned URLs. Labels are recorded per applied
  version, **not asserted verified**. A human owner must review actual backup
  recoverability, restored-copy upgrade rehearsal, compatibility and RPO/RTO.
- `--timeout` bounds the **whole run**: default 15 minutes, allowed 1s–2h.
  Budget for long spatial/index SQL and all pending versions together. Session
  statement timeout uses the same bound; lock waits are capped at 5s and idle
  transactions at 30s. Connection timeout is clamped to 5–30s (default 10s),
  with a separate 30s acquisition ceiling. Cleanup gets at most 5 more seconds.
  Runtime DSN/PGOPTIONS overrides are replaced, including timeouts,
  `application_name=shopstops-migrate`, read-only plan and public creation path.

## Read-only plan and fail-closed history

The tool snapshots/hashes files, opens one dedicated pinned session, takes
nonblocking **session** advisory lock `0x53534d4947524154`, then reads actual
database identity and catalogs/history. Plan and apply use the same fixed lock;
it stays held across every per-version transaction. A competing holder causes
immediate failure. This only coordinates cooperating tool invocations: prohibit
manual DDL and other migration tools during a run.

Plan defaults transactions to read-only and executes no migration SQL, ledger
DDL, baseline/stamp or user-data mutation. Session setup/locks are not persistent
schema changes. Fresh plan reports every version pending without creating a
ledger. Output is safe filenames/SHA-256 and counts, never DSNs or evidence.

The directory may contain **only** sequential `001_name.sql` through at most
`999_name.sql`, safe lowercase underscore-separated names, no gaps/duplicates,
unknown files, directories or symlinks. Each regular file is at most 1 MiB;
the total immutable in-memory snapshot is at most 16 MiB. SHA-256 is computed
once from the **same bytes executed**, never a later reread. Trust and protect
the directory and parents as immutable release inputs; do not edit during runs.
Never edit/rename applied files.

Dedicated **public.shopstops_schema_migrations** records integer version,
filename, lowercase SHA-256, server application timestamp, backup label and
restore-test label. The tool validates its expected table/column layout and
version primary key. History must be an exact prefix of the manifest: unknown,
skipped, duplicate versions, name/checksum drift and empty existing ledgers fail
closed. Catalog/privilege/read errors are never mistaken for ledger absence.
This is not a full live-schema drift detector or tamper-proof audit service.

**Populated databases without this ledger are refused, including plan.** Catalog
checks cover non-extension public relations/indexes, routines, types and
operators; preinstalled PostGIS/extension-owned implicit objects are allowed.
A standalone enum/domain must not permit silent adoption. Other schemas and
extension provisioning require separate review. Databases populated by the
disposable SQL fixture runner are intentionally incompatible with fresh
bootstrap. There is **no** baseline, stamp, force, repair-history or adoption
option. Do not manufacture an empty ledger or manually copy rows to bypass
this guard. Existing deployments need an independently reviewed adoption and
backup/restore process before this tool can be used.

## Transaction, privilege and failure semantics

Fresh ledger creation, PUBLIC ledger privilege revocation, migration 001 and
its history insert share **one transaction**; bootstrap failure leaves neither
schema DDL nor empty ledger. Every subsequent file has its own transaction:
SQL and history insert commit atomically. Earlier committed versions remain
committed when later SQL fails. No down migrations, automatic schema drops,
restore or destructive cleanup. Re-plan after failure and resume only approved
pending versions. An uncertain commit is reported **unconfirmed**, never as
proof of rollback: inspect history before retrying.

Cleanup explicitly unlocks with a bounded fresh context and always physically
closes the pinned connection (no idle pooled session lock). Process/connection
loss releases the lock once PostgreSQL observes disconnect. All driver/parser,
filesystem, network and SQL error details are suppressed in favor of fixed
stage errors, because raw errors can contain credentials or SQL. Diagnose
privately with an authorized database operator; do not enable raw logging.

Migrations 001–011 are reviewed as transaction-wrappable: no top-level
BEGIN/COMMIT or concurrent indexes. A conservative lexical guard skips strings,
quoted identifiers, nested comments and dollar-quoted function bodies; PL/pgSQL
BEGIN/END is not rejected by naive regex. Top-level transaction/session controls
and known incompatible operations are rejected. This is **not a SQL parser or
sandbox**. Review all future SQL for transaction/session effects, locks, timing,
extension assumptions, API compatibility and grants. Nontransactional changes
need a separately reviewed process, not a bypass flag.

Use a migration-specific owner role; API roles must not own/alter the ledger or
schema. This tool grants nothing to PUBLIC and revokes PUBLIC ledger access.
Review default privileges, memberships and explicit least-privilege app grants
separately. It does not rewrite ACLs in 001–011 or assert legacy function grants
are production-safe. Restrict owners/superusers: they can bypass ACLs/history.

## Tests and unresolved evidence

[Offline engine tests](../internal/migrations/migrations_test.go) cover manifest
safety, reviewed SQL lexical compatibility, exact snapshot execution, TLS/local
guards, bounds, dirty history, nonmutating plan, lock ordering, transactional
success/rollback and sanitized/uncertain errors. [CLI tests](../cmd/shopstops-migrate/main_test.go)
cover safe help and secret suppression. They do not prove SQL correctness.

[Opt-in integration](../internal/migrations/postgres_integration_test.go) skips
without `SHOPSTOPS_MIGRATION_TEST_DSN`. It enforces parsed local disposable and
actual database/user guards plus fresh catalogs. It tests no-ledger plan, failed
bootstrap rollback, untracked enum rejection, wrong database rejection,
competing sessions, apply 001–011, no-op repeat, evidence/ACLs, temporary-copy
checksum drift, failing synthetic 012 SQL/history rollback and repaired pending
012 retry. It never edits repository SQL. The service is disposable afterward;
never point it at the existing fixture or any persistent database.

[CI](../.github/workflows/ci.yml) adds a separate **migrations** job after Go
tests and the existing SQL fixture job, with a different fresh PostGIS service.
Image build checks depend on it. Require it in branch protection. No production
secrets/cloud permissions/deployment are added. This CI job and real database
tests were not run locally; production TLS, restored-data rehearsal, verified
backups and every [production gate](production-readiness.md) remain outstanding.