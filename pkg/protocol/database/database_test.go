package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const readOnlyProbeDriverName = "apexload-readonly-probe"

var (
	readOnlyProbeBegins  = make(chan driver.TxOptions, 8)
	readOnlyProbeCommits = make(chan struct{}, 8)
)

func init() {
	sql.Register(readOnlyProbeDriverName, readOnlyProbeDriver{})
}

type readOnlyProbeDriver struct{}

func (readOnlyProbeDriver) Open(string) (driver.Conn, error) { return readOnlyProbeConn{}, nil }

type readOnlyProbeConn struct{}

func (readOnlyProbeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (readOnlyProbeConn) Close() error { return nil }
func (readOnlyProbeConn) Begin() (driver.Tx, error) {
	return readOnlyProbeConn{}.BeginTx(context.Background(), driver.TxOptions{})
}
func (readOnlyProbeConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	readOnlyProbeBegins <- opts
	return readOnlyProbeTx{}, nil
}
func (readOnlyProbeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &readOnlyProbeRows{}, nil
}
func (readOnlyProbeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

type readOnlyProbeTx struct{}

func (readOnlyProbeTx) Commit() error {
	readOnlyProbeCommits <- struct{}{}
	return nil
}
func (readOnlyProbeTx) Rollback() error { return nil }

type readOnlyProbeRows struct{ emitted bool }

func (*readOnlyProbeRows) Columns() []string { return []string{"value"} }
func (*readOnlyProbeRows) Close() error      { return nil }
func (r *readOnlyProbeRows) Next(values []driver.Value) error {
	if r.emitted {
		return io.EOF
	}
	r.emitted = true
	values[0] = int64(1)
	return nil
}

func TestDatabaseProtocolExecutesQueryAndAssertions(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver":   "sqlite",
		"database_dsn":      ":memory:",
		"database_max_rows": float64(10),
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())

	for _, action := range []*protocol.Action{
		{Name: "create", Type: "db_exec", Target: "create table users (id integer primary key, name text)"},
		{Name: "insert", Type: "db_exec", Target: "insert into users(name) values (?)", Params: map[string]any{"binds": []any{"Ada"}}, Assertions: []protocol.Assertion{{Type: "rows_affected", Expected: 1}}},
	} {
		result, err := db.Execute(context.Background(), action)
		if err != nil {
			t.Fatalf("execute %s: %v", action.Name, err)
		}
		if !result.Success {
			t.Fatalf("execute %s success = false: %s", action.Name, result.ErrorMessage)
		}
	}

	result, err := db.Execute(context.Background(), &protocol.Action{
		Name:       "query",
		Type:       "db_query",
		Target:     "select name from users where name = ?",
		Params:     map[string]any{"binds": []any{"Ada"}},
		Extractors: []protocol.Extractor{{Name: "selected_name", Source: "scalar", Type: "jsonpath", Expression: "$.scalar"}},
		Assertions: []protocol.Assertion{{Type: "row_count", Expected: 1}, {Type: "scalar_equals", Expected: "Ada"}},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s", result.ErrorMessage)
	}
	if result.DependencyType != "database" || result.DependencyTarget != "sqlite" {
		t.Fatalf("dependency = %s/%s, want database/sqlite", result.DependencyType, result.DependencyTarget)
	}
	if !strings.Contains(result.ResponseBody, `"name":"Ada"`) || !strings.Contains(result.ResponseBody, `"row_count":1`) {
		t.Fatalf("response body = %s, want Ada row", result.ResponseBody)
	}
	if result.Extractions["selected_name"] != "Ada" {
		t.Fatalf("selected_name extraction = %q, want Ada", result.Extractions["selected_name"])
	}
	if result.ProtocolEvidence["protocol"] != "database" {
		t.Fatalf("protocol evidence = %#v, want database", result.ProtocolEvidence)
	}
	rows, ok := result.ProtocolEvidence["rows"].(map[string]any)
	if !ok || rows["row_count"] == nil || rows["scalar"] != "Ada" {
		t.Fatalf("protocol evidence rows = %#v, want row_count and scalar Ada", result.ProtocolEvidence["rows"])
	}
}

func TestDatabaseQueryMaxRowsOverrideCannotExceedConfiguredCap(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver": "sqlite", "database_dsn": ":memory:", "database_max_rows": float64(2),
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())

	result, err := db.Execute(context.Background(), &protocol.Action{
		Name: "bounded", Type: "db_query",
		Target: "SELECT 1 AS id UNION ALL SELECT 2 UNION ALL SELECT 3",
		Params: map[string]any{"max_rows": float64(1000)},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("query failed: %s", result.ErrorMessage)
	}
	if !strings.Contains(result.ResponseBody, `"row_count":2`) {
		t.Fatalf("response = %s, want configured two-row cap", result.ResponseBody)
	}
}

func TestDatabaseProtocolTransactionsAndRollback(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver":         "sqlite",
		"database_dsn":            "file:apexload_tx_test?mode=memory&cache=shared",
		"database_max_open_conns": float64(1),
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())

	_, _ = db.Execute(context.Background(), &protocol.Action{Name: "create", Type: "db_exec", Target: "create table items (id integer)"})
	if result, _ := db.Execute(context.Background(), &protocol.Action{Name: "begin", Type: "db_begin"}); !result.Success {
		t.Fatalf("begin failed: %s", result.ErrorMessage)
	}
	if result, _ := db.Execute(context.Background(), &protocol.Action{Name: "insert", Type: "db_exec", Target: "insert into items(id) values (1)"}); !result.Success {
		t.Fatalf("insert failed: %s", result.ErrorMessage)
	}
	if result, _ := db.Execute(context.Background(), &protocol.Action{Name: "rollback", Type: "db_rollback"}); !result.Success {
		t.Fatalf("rollback failed: %s", result.ErrorMessage)
	}
	result, _ := db.Execute(context.Background(), &protocol.Action{Name: "query", Type: "db_query", Target: "select id from items"})
	if !result.Success || !strings.Contains(result.ResponseBody, `"row_count":0`) {
		t.Fatalf("query after rollback = success %v body %s err %s, want zero rows", result.Success, result.ResponseBody, result.ErrorMessage)
	}
}

func TestDatabaseProtocolReadOnlyBlocksMutations(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver":    "sqlite",
		"database_dsn":       ":memory:",
		"database_read_only": true,
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())
	if result, _ := db.Execute(context.Background(), &protocol.Action{Name: "read", Type: "db_query", Target: "SELECT 1"}); !result.Success {
		t.Fatalf("standalone SQLite read-only query failed: %s", result.ErrorMessage)
	}

	result, err := db.Execute(context.Background(), &protocol.Action{Name: "create", Type: "db_exec", Target: "create table blocked (id integer)"})
	if err != nil {
		t.Fatalf("execute readonly mutation: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "read_only") {
		t.Fatalf("result = %#v, want read_only block", result)
	}
	if result.Snapshot == nil || !strings.Contains(result.Snapshot.RequestURL, "create table blocked") {
		t.Fatalf("snapshot = %#v, want failed SQL evidence", result.Snapshot)
	}
	if result.ProtocolEvidence["protocol"] != "database" || result.ProtocolEvidence["status"] != "failed" {
		t.Fatalf("protocol evidence = %#v, want failed database evidence", result.ProtocolEvidence)
	}
}

func TestDatabaseStandaloneReadOnlyActionsUseDriverReadOnlyTransaction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		actionType string
	}{
		{name: "query", actionType: "db_query"},
		{name: "exec", actionType: "db_exec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbHandle, err := sql.Open(readOnlyProbeDriverName, "")
			if err != nil {
				t.Fatalf("open probe driver: %v", err)
			}
			defer dbHandle.Close()
			db := &Database{db: dbHandle, driver: readOnlyProbeDriverName, readOnly: true, maxRows: 10}

			result, err := db.Execute(context.Background(), &protocol.Action{Name: tc.name, Type: tc.actionType, Target: "SELECT 1"})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !result.Success {
				t.Fatalf("result failed: %s", result.ErrorMessage)
			}
			select {
			case opts := <-readOnlyProbeBegins:
				if !opts.ReadOnly {
					t.Fatal("standalone action began a transaction without ReadOnly=true")
				}
			default:
				t.Fatal("standalone action did not begin a driver transaction")
			}
			select {
			case <-readOnlyProbeCommits:
			default:
				t.Fatal("standalone read-only transaction was not committed")
			}
		})
	}
}

func TestValidateReadOnlySQLRejectsBypasses(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		ok   bool
	}{
		{name: "select", sql: "-- comment\n SELECT * FROM items", ok: true},
		{name: "select semicolon", sql: "SELECT ';' AS value; -- trailing", ok: true},
		{name: "select into in quoted value", sql: "SELECT 'INTO OUTFILE' AS value", ok: true},
		{name: "read cte", sql: "WITH x AS (SELECT 1) SELECT * FROM x", ok: true},
		{name: "postgres select into table", sql: "SELECT * INTO archived_items FROM items"},
		{name: "postgres select into temp table", sql: "SELECT * INTO TEMP TABLE archived_items FROM items"},
		{name: "sql server select into", sql: "SELECT id, name INTO dbo.archived_items FROM dbo.items"},
		{name: "mysql select into outfile", sql: "SELECT * FROM items INTO OUTFILE '/tmp/items.csv'"},
		{name: "mysql select into dumpfile", sql: "SELECT payload INTO DUMPFILE '/tmp/payload.bin' FROM items"},
		{name: "mysql select into variable", sql: "SELECT id INTO @selected_id FROM items LIMIT 1"},
		{name: "mysql executable comment", sql: "SELECT 1 /*! INTO OUTFILE '/tmp/leak' */"},
		{name: "mysql versioned executable comment", sql: "SELECT 1 /*!50000 INTO OUTFILE '/tmp/leak' */"},
		{name: "mariadb executable comment", sql: "SELECT 1 /*M!100100 INTO OUTFILE '/tmp/leak' */"},
		{name: "optimizer executable comment", sql: "SELECT /*+ MAX_EXECUTION_TIME(1000) */ 1"},
		{name: "comment hidden delete", sql: "/* safe */ DELETE FROM items RETURNING id"},
		{name: "writable cte", sql: "WITH x AS (DELETE FROM items RETURNING id) SELECT * FROM x"},
		{name: "multi statement", sql: "SELECT 1; DELETE FROM items"},
		{name: "pragma", sql: "PRAGMA journal_mode=WAL"},
		{name: "unterminated comment", sql: "SELECT 1 /*"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReadOnlySQL(tc.sql)
			if (err == nil) != tc.ok {
				t.Fatalf("validateReadOnlySQL(%q) error = %v, want ok=%v", tc.sql, err, tc.ok)
			}
		})
	}
}

func TestDatabaseReadOnlyAppliesToQueryAction(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver": "sqlite", "database_dsn": ":memory:", "database_read_only": true,
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())
	result, err := db.Execute(context.Background(), &protocol.Action{
		Name: "query mutation", Type: "db_query", Target: "CREATE TABLE escaped(id integer) RETURNING id",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "read_only") {
		t.Fatalf("result = %#v, want read-only rejection", result)
	}
}
