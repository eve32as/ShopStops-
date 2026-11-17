package migrations

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Options contains non-secret operator choices. Evidence references are labels,
// not proof that a backup or restore test exists or has been verified.
type Options struct {
	Mode, Directory, ExpectedDatabase     string
	BackupReference, RestoreTestReference string
	AllowLocalTest                        bool
	Timeout                               time.Duration
}

func (o Options) validate() error {
	if o.Mode != "plan" && o.Mode != "apply" {
		return errors.New("mode must be plan or apply")
	}
	if o.Timeout < time.Second || o.Timeout > 2*time.Hour {
		return errors.New("timeout must be between 1s and 2h")
	}
	if o.Mode == "apply" {
		if strings.TrimSpace(o.ExpectedDatabase) == "" {
			return errors.New("apply requires --expected-database")
		}
		if !o.AllowLocalTest && (!evidenceLabel(o.BackupReference) || !evidenceLabel(o.RestoreTestReference)) {
			return errors.New("production apply requires bounded non-secret --backup-reference and --restore-test-reference labels")
		}
	}
	if (o.BackupReference != "" && !evidenceLabel(o.BackupReference)) || (o.RestoreTestReference != "" && !evidenceLabel(o.RestoreTestReference)) {
		return errors.New("evidence references must be non-secret labels of 1..200 safe characters")
	}
	return nil
}

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$`)

func evidenceLabel(s string) bool { return labelPattern.MatchString(s) }

// Result contains only safe manifest filenames/checksums, never DSNs or evidence.
type Result struct {
	Pending []Pending
	Applied int
}
type Pending struct{ Name, SHA256 string }
type record struct {
	version        int
	name, checksum string
}
type snapshot struct {
	exists  bool
	records []record
}

type transaction interface {
	Exec(context.Context, string, ...any) error
	Commit() error
	Rollback() error
}
type backend interface {
	Lock(context.Context) (bool, error)
	Unlock(context.Context) error
	Database(context.Context) (string, error)
	Snapshot(context.Context) (snapshot, error)
	Begin(context.Context) (transaction, error)
}

func pendingFrom(s snapshot, files []migration) ([]migration, error) {
	if s.exists && len(s.records) == 0 {
		return nil, errors.New("empty migration ledger requires independent review; automatic adoption is forbidden")
	}
	if len(s.records) > len(files) {
		return nil, errors.New("unknown applied migration version")
	}
	for i, r := range s.records {
		m := files[i]
		if r.version != i+1 || r.name != m.name || r.checksum != m.checksum {
			return nil, errors.New("migration history is nonsequential, unknown or has checksum/name drift")
		}
	}
	return files[len(s.records):], nil
}

// execute assumes a dedicated pinned session; the same session owns the lock
// across every per-migration transaction. All backend errors are replaced.
func execute(ctx context.Context, b backend, files []migration, o Options) (result Result, err error) {
	locked, e := b.Lock(ctx)
	if e != nil {
		return result, errors.New("migration lock query failed")
	}
	if !locked {
		return result, errors.New("another migration session holds the lock; no changes made")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if b.Unlock(cleanup) != nil && err == nil {
			err = errors.New("migration lock release failed; session closed; inspect ledger before retry")
		}
	}()
	database, e := b.Database(ctx)
	if e != nil {
		return result, errors.New("cannot verify actual database identity")
	}
	if o.ExpectedDatabase != "" && database != o.ExpectedDatabase {
		return result, errors.New("actual current_database does not match --expected-database; no changes made")
	}
	s, e := b.Snapshot(ctx) // Always read/revalidate AFTER acquiring the lock.
	if e != nil {
		return result, errors.New("cannot validate ledger or fresh public schema; existing databases require independent adoption review (no automatic baseline)")
	}
	pending, e := pendingFrom(s, files)
	if e != nil {
		return result, e
	}
	for _, m := range pending {
		result.Pending = append(result.Pending, Pending{m.name, m.checksum})
	}
	if o.Mode == "plan" {
		return result, nil
	}
	for _, m := range pending {
		if e := applyOne(ctx, b, m, !s.exists, o); e != nil {
			return result, e
		}
		s.exists = true
		result.Applied++
	}
	return result, nil
}

func applyOne(ctx context.Context, b backend, m migration, bootstrap bool, o Options) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return errors.New("cannot begin migration transaction")
	}
	defer tx.Rollback()
	if bootstrap {
		if tx.Exec(ctx, createLedger) != nil {
			return errors.New("cannot create migration ledger; transaction rolled back")
		}
	}
	if tx.Exec(ctx, string(m.sql)) != nil {
		return errors.New("migration SQL failed; transaction rolled back; earlier versions may be committed")
	}
	if tx.Exec(ctx, insertLedger, m.version, m.name, m.checksum, o.BackupReference, o.RestoreTestReference) != nil {
		return errors.New("migration ledger insert failed; transaction rolled back")
	}
	if tx.Commit() != nil {
		return errors.New("migration commit not confirmed; inspect ledger with plan before retry; do not assume rollback")
	}
	return nil
}

const createLedger = `CREATE TABLE public.shopstops_schema_migrations (
 version integer PRIMARY KEY CHECK (version BETWEEN 1 AND 999),
 name text NOT NULL UNIQUE,
 checksum text NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
 applied_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 backup_reference text NOT NULL,
 restore_test_reference text NOT NULL
);
REVOKE ALL ON public.shopstops_schema_migrations FROM PUBLIC;`

const insertLedger = `INSERT INTO public.shopstops_schema_migrations
 (version, name, checksum, backup_reference, restore_test_reference) VALUES ($1,$2,$3,$4,$5)`
