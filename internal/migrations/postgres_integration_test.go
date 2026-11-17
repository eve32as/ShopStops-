package migrations

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
)

// TestPostgresIntegration needs its OWN fresh disposable service, not the
// populated SQL fixture database. No schema/database drops or adoption.
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("SHOPSTOPS_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("requires explicitly opted-in fresh local shopstops_ci PostGIS service")
	}
	o := testOptions()
	o.AllowLocalTest = true
	o.ExpectedDatabase = "shopstops_ci"
	o.Timeout = 3 * time.Minute
	c, err := connectionConfig(dsn, o)
	if err != nil {
		t.Fatal("integration DSN refused by disposable guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Separate observer checks catalog outcomes, never runs migration SQL.
	c.RuntimeParams["default_transaction_read_only"] = "off"
	db := stdlib.OpenDB(*c)
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(0)
	var database, user string
	if db.QueryRowContext(ctx, "SELECT current_database(), current_user").Scan(&database, &user) != nil || database != "shopstops_ci" || user != "shopstops_ci" {
		t.Fatal("actual disposable database/user mismatch")
	}
	var ledger bool
	if db.QueryRowContext(ctx, "SELECT to_regclass('public.shopstops_schema_migrations') IS NOT NULL").Scan(&ledger) != nil || ledger {
		t.Fatal("integration requires fresh service without ledger")
	}
	r, err := Run(ctx, dsn, o)
	if err != nil || len(r.Pending) != 11 {
		t.Fatal("fresh plan failed; service must have no untracked public objects")
	}
	if db.QueryRowContext(ctx, "SELECT to_regclass('public.shopstops_schema_migrations') IS NOT NULL").Scan(&ledger) != nil || ledger {
		t.Fatal("fresh plan created ledger")
	}
	bad := t.TempDir()
	writeFile(t, bad, "001_failure.sql", "CREATE TABLE public.migration_rollback_probe(id integer); SELECT 1/0;")
	apply := o
	apply.Mode = "apply"
	apply.Directory = bad
	if _, err := Run(ctx, dsn, apply); err == nil {
		t.Fatal("failing bootstrap succeeded")
	}
	var absent bool
	if db.QueryRowContext(ctx, "SELECT to_regclass('public.shopstops_schema_migrations') IS NULL AND to_regclass('public.migration_rollback_probe') IS NULL").Scan(&absent) != nil || !absent {
		t.Fatal("partial bootstrap survived rollback")
	}
	if _, err := db.ExecContext(ctx, "CREATE TYPE public.migration_untracked_probe AS ENUM ('probe')"); err != nil {
		t.Fatal("cannot create owned integration type")
	}
	_, planErr := Run(ctx, dsn, o)
	_, dropErr := db.ExecContext(ctx, "DROP TYPE public.migration_untracked_probe")
	if planErr == nil || dropErr != nil {
		t.Fatal("untracked type guard/owned fixture cleanup failed")
	}
	wrong := o
	wrong.ExpectedDatabase = "wrong"
	if _, err := Run(ctx, dsn, wrong); err == nil {
		t.Fatal("actual database guard failed")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal("cannot acquire lock-test session")
	}
	p := &postgres{conn}
	locked, err := p.Lock(ctx)
	if err != nil || !locked {
		conn.Close()
		t.Fatal("cannot acquire integration lock")
	}
	_, lockErr := Run(ctx, dsn, o)
	unlockErr := p.Unlock(ctx)
	conn.Close()
	if lockErr == nil || unlockErr != nil {
		t.Fatal("concurrent session was not excluded")
	}
	apply = o
	apply.Mode = "apply"
	apply.BackupReference = "ci-not-a-production-backup"
	apply.RestoreTestReference = "ci-not-a-production-restore"
	r, err = Run(ctx, dsn, apply)
	if err != nil || r.Applied != 11 {
		t.Fatalf("apply 001-011 failed (sanitized): %v", err)
	}
	r, err = Run(ctx, dsn, o)
	if err != nil || len(r.Pending) != 0 {
		t.Fatalf("post-apply history validation failed (sanitized): %v", err)
	}
	r, err = Run(ctx, dsn, apply)
	if err != nil || r.Applied != 0 {
		t.Fatal("repeat apply was not a no-op")
	}
	var evidence, private bool
	if db.QueryRowContext(ctx, `SELECT count(*)=11 AND bool_and(backup_reference=$1 AND restore_test_reference=$2 AND applied_at IS NOT NULL) FROM public.shopstops_schema_migrations`, apply.BackupReference, apply.RestoreTestReference).Scan(&evidence) != nil || !evidence {
		t.Fatal("missing transactional history/evidence")
	}
	if db.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM pg_class c CROSS JOIN LATERAL aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner))) a WHERE c.oid='public.shopstops_schema_migrations'::regclass AND a.grantee=0)`).Scan(&private) != nil || !private {
		t.Fatal("ledger grants privileges to PUBLIC")
	}
	files, err := loadManifest(o.Directory)
	if err != nil {
		t.Fatal(err)
	}
	copyDir := t.TempDir()
	for _, m := range files {
		writeFile(t, copyDir, m.name, string(m.sql))
	}
	writeFile(t, copyDir, files[0].name, string(files[0].sql)+"\n-- checksum drift fixture\n")
	drift := o
	drift.Directory = copyDir
	if _, err := Run(ctx, dsn, drift); err == nil {
		t.Fatal("checksum drift accepted")
	}
	writeFile(t, copyDir, files[0].name, string(files[0].sql))
	writeFile(t, copyDir, "012_rollback_probe.sql", "CREATE TABLE public.migration_rollback_probe(id integer); SELECT 1/0;")
	apply.Directory = copyDir
	if _, err := Run(ctx, dsn, apply); err == nil {
		t.Fatal("failing migration 012 succeeded")
	}
	if db.QueryRowContext(ctx, `SELECT to_regclass('public.migration_rollback_probe') IS NULL AND (SELECT count(*)=11 FROM public.shopstops_schema_migrations)`).Scan(&absent) != nil || !absent {
		t.Fatal("SQL/history did not roll back together")
	}
	// Repair ONLY an unapplied temporary fixture, never repository 001-011.
	writeFile(t, copyDir, "012_rollback_probe.sql", "CREATE TABLE public.migration_rollback_probe(id integer);")
	r, err = Run(ctx, dsn, apply)
	if err != nil || r.Applied != 1 {
		t.Fatal("retry after unapplied fixture repair failed")
	}
	drift.Directory = copyDir
	r, err = Run(ctx, dsn, drift)
	if err != nil || len(r.Pending) != 0 {
		t.Fatal("final plan failed; lock/session may have leaked")
	}
}
