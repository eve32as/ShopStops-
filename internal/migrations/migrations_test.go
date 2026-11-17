package migrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testOptions() Options {
	return Options{Mode: "plan", Directory: "../../db/migrations", Timeout: time.Minute}
}
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
		t.Fatal("write fixture failed")
	}
}
func manifestFixture(t *testing.T) []migration {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "001_one.sql", "CREATE TABLE one(id integer);")
	writeFile(t, dir, "002_two.sql", "CREATE TABLE two(id integer);")
	m, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestReviewedSQL(t *testing.T) {
	m, err := loadManifest("../../db/migrations")
	if err != nil || len(m) != 11 {
		t.Fatalf("reviewed manifest: count=%d error=%v", len(m), err)
	}
	if m[10].name != "011_extra_pickup_test_checkout.sql" {
		t.Fatal("review new migration set")
	}
}

func TestManifestStrict(t *testing.T) {
	for _, names := range [][]string{
		{}, {"002_gap.sql"}, {"001_one.sql", "003_gap.sql"},
		{"001_one.sql", "001_duplicate.sql"}, {"001_Upper.sql"},
		{"001_one.sql", "notes.txt"}, {"000_zero.sql"}, {"01_short.sql"},
		{"001_double__underscore.sql"}, {"001_one.sql.bak"},
	} {
		t.Run(strings.Join(names, "/"), func(t *testing.T) {
			dir := t.TempDir()
			for _, n := range names {
				writeFile(t, dir, n, "SELECT 1;")
			}
			if _, err := loadManifest(dir); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	for _, body := range []string{"", strings.Repeat("x", (1<<20)+1)} {
		dir := t.TempDir()
		writeFile(t, dir, "001_one.sql", body)
		if _, err := loadManifest(dir); err == nil {
			t.Fatal("accepted empty/oversized SQL")
		}
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "001_directory.sql"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(dir); err == nil {
		t.Fatal("accepted directory")
	}
}

func TestManifestSymlinksAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	target := t.TempDir()
	writeFile(t, target, "original.sql", "SELECT 1;")
	if err := os.Symlink(filepath.Join(target, "original.sql"), filepath.Join(dir, "001_link.sql")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(dir); err == nil {
		t.Fatal("accepted symlink file")
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(link); err == nil {
		t.Fatal("accepted symlink directory")
	}
	dir = t.TempDir()
	writeFile(t, dir, "001_one.sql", "SELECT 1;")
	m, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "001_one.sql", "SELECT 2;")
	if string(m[0].sql) != "SELECT 1;" {
		t.Fatal("snapshot changed")
	}
	b := &fakeBackend{locked: true, database: "test"}
	o := testOptions()
	o.Mode = "apply"
	if _, err := execute(context.Background(), b, m, o); err != nil {
		t.Fatal(err)
	}
	if b.txs[0].queries[1] != "SELECT 1;" || b.txs[0].args[2][2] != m[0].checksum {
		t.Fatal("hash/execution bytes differ")
	}
}

func TestSQLBoundaryGuard(t *testing.T) {
	for _, s := range []string{
		"BEGIN; SELECT 1; COMMIT;", "-- safe\n rollback;", "SELECT 1; /* nested /* c */ c */ END;",
		"START TRANSACTION;", "SAVEPOINT x;", "RELEASE x;", "ABORT;", "PREPARE TRANSACTION 'x';",
		"CREATE INDEX CONCURRENTLY x ON t(id);", "VACUUM;", "SET statement_timeout=0;", "RESET ALL;", "DISCARD ALL;",
		"CALL commit_proc();", "SELECT 'unclosed", "SELECT $$unclosed", "/* unclosed", "\\include file", "SELECT 1; COMMIT AND CHAIN;",
	} {
		if validateSQL(s) == nil {
			t.Errorf("accepted prohibited SQL %q", s)
		}
	}
	for _, s := range []string{
		"CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $$ BEGIN RETURN; END; $$;",
		"DO $tag$ BEGIN RAISE NOTICE 'COMMIT'; END; $tag$;",
		`SELECT 'BEGIN; COMMIT;', 'it''s safe', E'hello';`,
		"/* nested /* BEGIN; */ COMMIT; */ SELECT 1; -- ROLLBACK;",
		`CREATE TABLE "COMMIT" ("BEGIN" text);`,
	} {
		if err := validateSQL(s); err != nil {
			t.Errorf("rejected quoted/function SQL %q", s)
		}
	}
}

func TestConnectionPolicy(t *testing.T) {
	// Isolate all libpq defaults; these tests parse only, never connect.
	for _, key := range []string{"PGSERVICE", "PGSERVICEFILE", "PGSSLMODE", "PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY", "PGHOST", "PGDATABASE", "PGUSER", "PGOPTIONS"} {
		t.Setenv(key, "")
	}
	cases := []struct {
		dsn       string
		local, ok bool
	}{
		{"", false, false}, {"postgres://u:secret@remote/prod?sslmode=verify-full", false, true},
		{"host=remote user=u dbname=prod sslmode=verify-full", false, true},
		{"host=remote user=u dbname=prod sslmode=disable", false, false},
		{"host=remote user=u dbname=prod sslmode=require", false, false},
		{"host=remote user=u dbname=prod sslmode=verify-ca", false, false},
		{"host=remote user=u dbname=prod sslmode=prefer", false, false},
		{"host=localhost user=shopstops_ci dbname=shopstops_ci sslmode=verify-full", false, false},
		{"host=localhost user=shopstops_ci dbname=shopstops_ci sslmode=disable", true, true},
		{"postgres://shopstops_ci:secret@127.0.0.1/shopstops_ci?sslmode=disable", true, true},
		{"host=remote user=shopstops_ci dbname=shopstops_ci sslmode=disable", true, false},
		{"host=localhost user=wrong dbname=shopstops_ci sslmode=disable", true, false},
		{"host=localhost user=shopstops_ci dbname=wrong sslmode=disable", true, false},
		{"host=/tmp user=shopstops_ci dbname=shopstops_ci sslmode=disable", true, false},
		{"host=localhost,remote user=shopstops_ci dbname=shopstops_ci sslmode=disable", true, false},
		{"host=remote,localhost user=u dbname=prod sslmode=verify-full", false, false},
		{"postgres://u:secret@remote/prod?sslmode=nonsense", false, false},
	}
	for i, tc := range cases {
		o := testOptions()
		o.AllowLocalTest = tc.local
		c, err := connectionConfig(tc.dsn, o)
		if (err == nil) != tc.ok {
			t.Errorf("case %d: unexpected policy result %v", i, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Fatal("secret leak")
		}
		if err == nil && (c.RuntimeParams["application_name"] != "shopstops-migrate" || c.RuntimeParams["default_transaction_read_only"] != "on") {
			t.Fatal("unsafe runtime defaults")
		}
	}
	for _, timeout := range []string{"0", "1", "999"} {
		o := testOptions()
		c, err := connectionConfig("host=remote user=u dbname=prod sslmode=verify-full connect_timeout="+timeout+" application_name=other options='-c statement_timeout=0'", o)
		if err != nil {
			t.Fatal(err)
		}
		if c.ConnectTimeout < 5*time.Second || c.ConnectTimeout > 30*time.Second || c.RuntimeParams["options"] != "" || c.RuntimeParams["statement_timeout"] != "60000" || c.RuntimeParams["search_path"] != "public" {
			t.Fatal("unbounded/inherited settings")
		}
	}
}

func TestOptions(t *testing.T) {
	o := testOptions()
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	o.Mode = "apply"
	if o.validate() == nil {
		t.Fatal("apply without expected database")
	}
	o.ExpectedDatabase = "prod"
	if o.validate() == nil {
		t.Fatal("apply without evidence")
	}
	o.BackupReference = "backup-ticket-123"
	o.RestoreTestReference = "restore-ticket-456"
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{" ", "secret\nvalue", strings.Repeat("a", 201), "https://user:password@host/backup"} {
		o.BackupReference = ref
		if o.validate() == nil {
			t.Fatal("accepted unsafe label")
		}
	}
	o = testOptions()
	o.AllowLocalTest = true
	o.Mode = "apply"
	o.ExpectedDatabase = "shopstops_ci"
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	for _, duration := range []time.Duration{0, -1, time.Hour * 3} {
		o.Timeout = duration
		if o.validate() == nil {
			t.Fatal("accepted unbounded timeout")
		}
	}
	o = testOptions()
	o.Mode = "down"
	if o.validate() == nil {
		t.Fatal("accepted down mode")
	}
}

type fakeBackend struct {
	locked   bool
	database string
	state    snapshot
	fail     string
	events   []string
	txs      []*fakeTx
}

var secretError = errors.New("postgres://user:driver-secret@private-host/prod: sensitive SQL")

func (b *fakeBackend) Lock(context.Context) (bool, error) {
	b.events = append(b.events, "lock")
	if b.fail == "lock" {
		return false, secretError
	}
	return b.locked, nil
}
func (b *fakeBackend) Unlock(context.Context) error {
	b.events = append(b.events, "unlock")
	if b.fail == "unlock" {
		return secretError
	}
	return nil
}
func (b *fakeBackend) Database(context.Context) (string, error) {
	b.events = append(b.events, "database")
	if b.fail == "database" {
		return "", secretError
	}
	return b.database, nil
}
func (b *fakeBackend) Snapshot(context.Context) (snapshot, error) {
	b.events = append(b.events, "snapshot")
	if b.fail == "snapshot" {
		return snapshot{}, secretError
	}
	return b.state, nil
}
func (b *fakeBackend) Begin(context.Context) (transaction, error) {
	b.events = append(b.events, "begin")
	if b.fail == "begin" {
		return nil, secretError
	}
	tx := &fakeTx{b: b}
	b.txs = append(b.txs, tx)
	return tx, nil
}

type fakeTx struct {
	b                     *fakeBackend
	queries               []string
	args                  [][]any
	committed, rolledBack bool
}

func (t *fakeTx) Exec(_ context.Context, q string, args ...any) error {
	t.queries = append(t.queries, q)
	t.args = append(t.args, args)
	stage := "sql"
	if q == createLedger {
		stage = "create"
	}
	if q == insertLedger {
		stage = "insert"
	}
	t.b.events = append(t.b.events, stage)
	if t.b.fail == stage {
		return secretError
	}
	return nil
}
func (t *fakeTx) Commit() error {
	t.b.events = append(t.b.events, "commit")
	if t.b.fail == "commit" {
		return secretError
	}
	t.committed = true
	return nil
}
func (t *fakeTx) Rollback() error {
	if !t.committed {
		t.rolledBack = true
	}
	t.b.events = append(t.b.events, "rollback")
	return nil
}

func TestEnginePlanAndApply(t *testing.T) {
	m := manifestFixture(t)
	o := testOptions()
	b := &fakeBackend{locked: true, database: "prod"}
	r, err := execute(context.Background(), b, m, o)
	if err != nil || len(r.Pending) != 2 || len(b.txs) != 0 || !reflect.DeepEqual(b.events, []string{"lock", "database", "snapshot", "unlock"}) {
		t.Fatalf("plan mutated or wrong result: %v", err)
	}
	o.Mode = "apply"
	o.ExpectedDatabase = "prod"
	o.BackupReference = "b"
	o.RestoreTestReference = "r"
	b = &fakeBackend{locked: true, database: "prod"}
	r, err = execute(context.Background(), b, m, o)
	if err != nil || r.Applied != 2 || len(b.txs) != 2 {
		t.Fatalf("apply failed: %v", err)
	}
	if !reflect.DeepEqual(b.events, []string{"lock", "database", "snapshot", "begin", "create", "sql", "insert", "commit", "rollback", "begin", "sql", "insert", "commit", "rollback", "unlock"}) {
		t.Fatal(b.events)
	}
	for _, tx := range b.txs {
		if !tx.committed || tx.rolledBack {
			t.Fatal("wrong transaction outcome")
		}
	}
	if b.txs[0].args[2][3] != "b" || b.txs[0].args[2][4] != "r" {
		t.Fatal("evidence not recorded")
	}
	state := snapshot{exists: true, records: []record{{1, m[0].name, m[0].checksum}, {2, m[1].name, m[1].checksum}}}
	b = &fakeBackend{locked: true, database: "prod", state: state}
	r, err = execute(context.Background(), b, m, o)
	if err != nil || len(r.Pending) != 0 || len(b.txs) != 0 {
		t.Fatal("repeat apply must be no-op")
	}
	state.records = state.records[:1]
	b = &fakeBackend{locked: true, database: "prod", state: state}
	r, err = execute(context.Background(), b, m, o)
	if err != nil || r.Applied != 1 || b.txs[0].queries[0] == createLedger {
		t.Fatal("incremental apply recreated ledger")
	}
}

func TestEngineFailuresSanitizedAndRollback(t *testing.T) {
	m := manifestFixture(t)
	o := testOptions()
	o.Mode = "apply"
	o.ExpectedDatabase = "prod"
	for _, stage := range []string{"lock", "database", "snapshot", "begin", "create", "sql", "insert", "commit", "unlock"} {
		t.Run(stage, func(t *testing.T) {
			b := &fakeBackend{locked: true, database: "prod", fail: stage}
			_, err := execute(context.Background(), b, m, o)
			if err == nil || strings.Contains(err.Error(), "driver-secret") || strings.Contains(err.Error(), "private-host") {
				t.Fatal("error missing or unsanitized")
			}
			if stage != "lock" && b.events[len(b.events)-1] != "unlock" {
				t.Fatal("lock not released")
			}
			if stage == "create" || stage == "sql" || stage == "insert" || stage == "commit" {
				if len(b.txs) != 1 || b.txs[0].committed || !b.txs[0].rolledBack {
					t.Fatal("missing rollback or continued after failure")
				}
			}
		})
	}
	b := &fakeBackend{locked: false, database: "prod"}
	if _, err := execute(context.Background(), b, m, o); err == nil || len(b.events) != 1 {
		t.Fatal("read before acquiring lock")
	}
	b = &fakeBackend{locked: true, database: "wrong"}
	if _, err := execute(context.Background(), b, m, o); err == nil || len(b.txs) != 0 || strings.Contains(strings.Join(b.events, ","), "snapshot") {
		t.Fatal("wrong database not blocked")
	}
}

func TestDirtyHistory(t *testing.T) {
	m := manifestFixture(t)
	for _, records := range [][]record{
		{}, {{2, m[1].name, m[1].checksum}}, {{1, m[0].name, "drift"}},
		{{1, "001_renamed.sql", m[0].checksum}}, {{1, m[0].name, m[0].checksum}, {1, m[0].name, m[0].checksum}},
		{{1, m[0].name, m[0].checksum}, {3, m[1].name, m[1].checksum}},
		{{1, m[0].name, m[0].checksum}, {2, m[1].name, m[1].checksum}, {3, "unknown", "unknown"}},
	} {
		b := &fakeBackend{locked: true, database: "prod", state: snapshot{true, records}}
		if _, err := execute(context.Background(), b, m, testOptions()); err == nil || len(b.txs) != 0 {
			t.Fatal("dirty history accepted")
		}
	}
}
