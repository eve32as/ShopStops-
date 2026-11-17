package migrations

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Fixed session-level lock, intentionally unrelated to checkout/dispatch locks.
const advisoryLockID int64 = 0x53534d4947524154

func connectionConfig(dsn string, o Options) (*pgx.ConnConfig, error) {
	if dsn == "" {
		return nil, errors.New("DATABASE_URL environment variable is required")
	}
	c, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL configuration (details suppressed)")
	}
	localHost := func(host string) bool { return host == "localhost" || host == "127.0.0.1" }
	loopback := func(host string) bool {
		ip := net.ParseIP(host)
		return localHost(host) || ip != nil && ip.IsLoopback()
	}
	if o.AllowLocalTest {
		if !localHost(c.Host) || c.Database != "shopstops_ci" || c.User != "shopstops_ci" {
			return nil, errors.New("--allow-local-test requires local host and exact shopstops_ci database/user")
		}
		for _, f := range c.Fallbacks {
			if !localHost(f.Host) {
				return nil, errors.New("local test connection has a nonlocal fallback")
			}
		}
	} else {
		if loopback(c.Host) {
			return nil, errors.New("local disposable connections require --allow-local-test")
		}
		if c.TLSConfig == nil || c.TLSConfig.InsecureSkipVerify || c.TLSConfig.ServerName == "" {
			return nil, errors.New("production DATABASE_URL requires sslmode=verify-full and a trusted root certificate")
		}
		for _, f := range c.Fallbacks {
			if loopback(f.Host) || f.TLSConfig == nil || f.TLSConfig.InsecureSkipVerify || f.TLSConfig.ServerName == "" {
				return nil, errors.New("production connection fallback must also use verify-full TLS")
			}
		}
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 10 * time.Second
	}
	if c.ConnectTimeout < 5*time.Second {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.ConnectTimeout > 30*time.Second {
		c.ConnectTimeout = 30 * time.Second
	}
	// ParseConfig may have captured the original timeout in its dialer already.
	c.DialFunc = (&net.Dialer{Timeout: c.ConnectTimeout}).DialContext
	// Do not inherit options/search_path/timeouts/application_name from the DSN
	// or PGOPTIONS. pg_catalog is implicitly searched before public; omitting it
	// explicitly keeps public as the creation schema for unqualified SQL.
	readOnly := "on"
	if o.Mode == "apply" {
		readOnly = "off"
	}
	c.RuntimeParams = map[string]string{
		"application_name":                    "shopstops-migrate",
		"search_path":                         "public",
		"standard_conforming_strings":         "on",
		"default_transaction_read_only":       readOnly,
		"statement_timeout":                   strconv.FormatInt(o.Timeout.Milliseconds(), 10),
		"lock_timeout":                        "5000",
		"idle_in_transaction_session_timeout": "30000",
	}
	// pgx simple protocol supports reviewed multi-statement migration snapshots;
	// ledger parameters are escaped by pgx, never interpolated by this package.
	c.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return c, nil
}

// Run validates all input before connecting. It never logs or returns driver,
// filesystem, DSN, SQL text or operator-evidence error details.
func Run(ctx context.Context, dsn string, o Options) (Result, error) {
	if err := o.validate(); err != nil {
		return Result{}, err
	}
	files, err := loadManifest(o.Directory)
	if err != nil {
		return Result{}, err
	}
	c, err := connectionConfig(dsn, o)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	db := stdlib.OpenDB(*c)
	return runDatabase(ctx, db, files, o)
}

// runDatabase owns the pool and pinned session; the small SQL-driver fake in
// tests exercises this lifecycle without opening any network connection.
func runDatabase(ctx context.Context, db *sql.DB, files []migration, o Options) (Result, error) {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0) // Returning the pinned session physically closes it.
	defer db.Close()
	connectCtx, connectCancel := context.WithTimeout(ctx, 30*time.Second)
	conn, err := db.Conn(connectCtx)
	connectCancel()
	if err != nil {
		return Result{}, errors.New("cannot connect to migration database (details suppressed)")
	}
	defer conn.Close() // Also releases session lock if explicit unlock fails/cancels.
	return execute(ctx, &postgres{conn}, files, o)
}

type postgres struct{ conn *sql.Conn }

func (p *postgres) Lock(ctx context.Context) (bool, error) {
	var locked bool
	err := p.conn.QueryRowContext(ctx, `SELECT pg_catalog.pg_try_advisory_lock($1)`, advisoryLockID).Scan(&locked)
	return locked, err
}
func (p *postgres) Unlock(ctx context.Context) error {
	var unlocked bool
	err := p.conn.QueryRowContext(ctx, `SELECT pg_catalog.pg_advisory_unlock($1)`, advisoryLockID).Scan(&unlocked)
	if err == nil && !unlocked {
		return errors.New("lock not held")
	}
	return err
}
func (p *postgres) Database(ctx context.Context) (string, error) {
	var name string
	err := p.conn.QueryRowContext(ctx, `SELECT pg_catalog.current_database()`).Scan(&name)
	return name, err
}
func (p *postgres) Snapshot(ctx context.Context) (snapshot, error) {
	var s snapshot
	// Catalog inspection, not SELECT followed by guessing from permission errors.
	err := p.conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class c
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relname='shopstops_schema_migrations')`).Scan(&s.exists)
	if err != nil {
		return s, err
	}
	if !s.exists {
		var count int64
		err = p.conn.QueryRowContext(ctx, freshObjects).Scan(&count)
		if err == nil && count != 0 {
			err = errors.New("untracked public objects")
		}
		return s, err
	}
	var valid bool
	err = p.conn.QueryRowContext(ctx, validLedger).Scan(&valid)
	if err != nil {
		return s, err
	}
	if !valid {
		return s, errors.New("unexpected ledger schema")
	}
	rows, err := p.conn.QueryContext(ctx, `SELECT version, name, checksum FROM public.shopstops_schema_migrations ORDER BY version LIMIT 1000`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.version, &r.name, &r.checksum); err != nil {
			return s, err
		}
		s.records = append(s.records, r)
	}
	return s, rows.Err()
}
func (p *postgres) Begin(ctx context.Context) (transaction, error) {
	tx, err := p.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return sqlTransaction{tx}, nil
}

type sqlTransaction struct{ *sql.Tx }

func (t sqlTransaction) Exec(ctx context.Context, query string, args ...any) error {
	_, err := t.ExecContext(ctx, query, args...)
	return err
}

// Include relations (including standalone indexes), routines AND types. Array
// types automatically owned by an extension type are allowed via pg_depend.
// This only permits extension-owned objects, not arbitrary pre-existing data.
const freshObjects = `WITH RECURSIVE extension_objects(catalog, object) AS (
 SELECT classid, objid FROM pg_catalog.pg_depend WHERE deptype='e'
 UNION
 SELECT d.classid, d.objid FROM pg_catalog.pg_depend d JOIN extension_objects e
 ON d.refclassid=e.catalog AND d.refobjid=e.object
 WHERE d.deptype='i' OR (d.deptype='a' AND d.classid IN ('pg_type'::regclass,'pg_constraint'::regclass))
 ) SELECT count(*) FROM (
 SELECT 'pg_class'::regclass AS catalog, c.oid AS object FROM pg_catalog.pg_class c
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'
 UNION ALL
 SELECT 'pg_proc'::regclass, p.oid FROM pg_catalog.pg_proc p
 JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public'
 UNION ALL
 SELECT 'pg_type'::regclass, t.oid FROM pg_catalog.pg_type t
 JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='public'
 UNION ALL
 SELECT 'pg_operator'::regclass, o.oid FROM pg_catalog.pg_operator o
 JOIN pg_catalog.pg_namespace n ON n.oid=o.oprnamespace WHERE n.nspname='public'
 ) objects WHERE NOT EXISTS (
 SELECT 1 FROM extension_objects e WHERE e.catalog=objects.catalog AND e.object=objects.object
 )`

const validLedger = `SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class c
 WHERE c.oid='public.shopstops_schema_migrations'::regclass AND c.relkind='r'
 AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
 AND (SELECT string_agg(a.attname || ':' || a.atttypid::regtype::text || ':' || a.attnotnull::text, ',' ORDER BY a.attnum)
 FROM pg_catalog.pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)
 = 'version:integer:true,name:text:true,checksum:text:true,applied_at:timestamp with time zone:true,backup_reference:text:true,restore_test_reference:text:true'
 AND EXISTS (SELECT 1 FROM pg_catalog.pg_index i WHERE i.indrelid=c.oid AND i.indisprimary AND i.indisvalid AND i.indkey::text='1'))`
