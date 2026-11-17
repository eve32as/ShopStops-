package migrations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

// Minimal database/sql driver fake, not a SQL parser/server or external mock
// dependency. It verifies pinned connection closure and transaction boundaries.
type sqlFake struct {
	closed, inTx                    bool
	connections, commits, rollbacks int
	fail                            string
	queries                         []string
}
type fakeConnector struct{ s *sqlFake }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	c.s.connections++
	return c.s, nil
}
func (c fakeConnector) Driver() driver.Driver { return fakeDriver{c.s} }

type fakeDriver struct{ s *sqlFake }

func (d fakeDriver) Open(string) (driver.Conn, error)                           { return nil, errors.New("connector only") }
func (s *sqlFake) Prepare(string) (driver.Stmt, error)                          { return nil, errors.New("unexpected prepare") }
func (s *sqlFake) Close() error                                                 { s.closed = true; return nil }
func (s *sqlFake) Begin() (driver.Tx, error)                                    { s.inTx = true; return s, nil }
func (s *sqlFake) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return s.Begin() }
func (s *sqlFake) Commit() error                                                { s.commits++; s.inTx = false; return nil }
func (s *sqlFake) Rollback() error                                              { s.rollbacks++; s.inTx = false; return nil }
func (s *sqlFake) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	s.queries = append(s.queries, q)
	if !s.inTx {
		return nil, errors.New("mutation outside transaction")
	}
	if s.fail == "exec" {
		return nil, secretError
	}
	return driver.RowsAffected(1), nil
}
func (s *sqlFake) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	s.queries = append(s.queries, q)
	var values []driver.Value
	switch {
	case strings.Contains(q, "pg_try_advisory_lock"):
		values = []driver.Value{true}
	case strings.Contains(q, "pg_advisory_unlock"):
		if s.fail == "unlock" {
			return nil, secretError
		}
		values = []driver.Value{true}
	case strings.Contains(q, "current_database"):
		values = []driver.Value{"test"}
	case q == freshObjects:
		values = []driver.Value{int64(0)}
	case strings.Contains(q, "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class"):
		if s.fail == "catalog" {
			return nil, secretError
		}
		values = []driver.Value{false}
	default:
		return nil, errors.New("unexpected catalog query")
	}
	return &fakeRows{values: values}, nil
}

type fakeRows struct {
	values []driver.Value
	done   bool
}

func (r *fakeRows) Columns() []string { return []string{"value"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	copy(dest, r.values)
	r.done = true
	return nil
}

func TestSQLBackendPinnedLifecycle(t *testing.T) {
	for _, tc := range []struct {
		mode, fail         string
		commits, rollbacks int
	}{
		{"plan", "", 0, 0}, {"apply", "", 2, 0}, {"apply", "exec", 0, 1}, {"plan", "catalog", 0, 0}, {"plan", "unlock", 0, 0},
	} {
		t.Run(tc.mode+tc.fail, func(t *testing.T) {
			s := &sqlFake{fail: tc.fail}
			db := sql.OpenDB(fakeConnector{s})
			o := testOptions()
			o.Mode = tc.mode
			o.ExpectedDatabase = "test"
			_, err := runDatabase(context.Background(), db, manifestFixture(t), o)
			if (err != nil) != (tc.fail != "") {
				t.Fatal("wrong SQL backend result")
			}
			if err != nil && strings.Contains(err.Error(), "driver-secret") {
				t.Fatal("driver error leaked")
			}
			if !s.closed || s.connections != 1 || s.inTx || s.commits != tc.commits || s.rollbacks != tc.rollbacks {
				t.Fatalf("session/transaction lifecycle: %+v", s)
			}
			if !strings.Contains(s.queries[0], "pg_try_advisory_lock") || !strings.Contains(s.queries[len(s.queries)-1], "pg_advisory_unlock") {
				t.Fatal("lock must bracket catalog/SQL activity")
			}
			for _, q := range s.queries {
				if tc.mode == "plan" && (q == createLedger || q == insertLedger || strings.HasPrefix(q, "CREATE TABLE")) {
					t.Fatal("plan executed DDL/DML")
				}
			}
		})
	}
}
