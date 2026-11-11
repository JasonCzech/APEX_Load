package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDatabaseInitRejectsUnboundedQueryTimeout(t *testing.T) {
	db := &Database{}
	err := db.Init(context.Background(), map[string]any{
		"database_driver":           "sqlite",
		"database_dsn":              ":memory:",
		"database_query_timeout_ms": dsl.MaxConfiguredProtocolTimeout.Milliseconds() + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "between 0") {
		t.Fatalf("Init error = %v, want timeout cap rejection", err)
	}
}

func TestDatabasePostgresDSLDriverSelectsBundledPGX(t *testing.T) {
	if got := normalizeDriver("postgres"); got != "pgx" {
		t.Fatalf("normalizeDriver(postgres) = %q, want bundled pgx database/sql driver", got)
	}
	if err := validateDatabaseDriver("postgres"); err != nil {
		t.Fatalf("public postgres driver rejected: %v", err)
	}
	if err := validateDatabaseDriver("pgx"); err == nil {
		t.Fatal("internal pgx spelling must not become a public DSL driver")
	}
	registered := false
	for _, driverName := range sql.Drivers() {
		if driverName == "pgx" {
			registered = true
			break
		}
	}
	if !registered {
		t.Fatal("bundled pgx database/sql driver is not registered")
	}
}

func TestDatabaseInitEnforcesResolvedCITargetPolicy(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		host string
	}{
		{name: "URL DSN", dsn: "postgres://user:super-secret@db.attacker.example:5432/app", host: "db.attacker.example"},
		{name: "failover DSN", dsn: "host=db.example.test,db.attacker.example port=5432 dbname=app", host: "db.attacker.example"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &Database{}
			err := db.Init(context.Background(), map[string]any{
				"database_driver":              "postgres",
				"database_dsn":                 test.dsn,
				protocol.TargetPolicyConfigKey: []string{"db.example.test"},
			})
			if err == nil || !strings.Contains(err.Error(), test.host) || strings.Contains(err.Error(), "super-secret") {
				t.Fatalf("Init policy error = %v, want sanitized rejection for %s", err, test.host)
			}
		})
	}

	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver":              "sqlite",
		"database_dsn":                 ":memory:",
		protocol.TargetPolicyConfigKey: []string{"db.example.test"},
	}); err != nil {
		t.Fatalf("local SQLite should remain available under target policy: %v", err)
	}
	_ = db.Teardown(context.Background())
}

func TestDatabaseInitEnforcesControllerResolvedDSNSecret(t *testing.T) {
	db := &Database{}
	err := db.Init(context.Background(), map[string]any{
		"database_driver":              "postgres",
		"database_dsn_secret":          "postgres://user:password@attacker.example.test/app",
		protocol.TargetPolicyConfigKey: []string{"db.example.test"},
	})
	if err == nil || !strings.Contains(err.Error(), "attacker.example.test") || strings.Contains(err.Error(), "password") {
		t.Fatalf("Init policy error = %v, want sanitized resolved secret target rejection", err)
	}
}

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

func TestDatabaseQueryHardBoundsRowsColumnsAndValues(t *testing.T) {
	t.Run("configured rows are clamped", func(t *testing.T) {
		db := &Database{}
		if err := db.Init(context.Background(), map[string]any{
			"database_driver": "sqlite", "database_dsn": ":memory:", "database_max_rows": float64(hardDatabaseMaxRows + 1),
		}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		defer db.Teardown(context.Background())
		if db.maxRows != hardDatabaseMaxRows {
			t.Fatalf("maxRows = %d, want hard cap %d", db.maxRows, hardDatabaseMaxRows)
		}
	})

	t.Run("large blob fails before response retention", func(t *testing.T) {
		db := &Database{}
		if err := db.Init(context.Background(), map[string]any{"database_driver": "sqlite", "database_dsn": ":memory:"}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		defer db.Teardown(context.Background())
		result, err := db.Execute(context.Background(), &protocol.Action{
			Name: "large", Type: "db_query", Target: "SELECT zeroblob(?) AS payload", Params: map[string]any{"binds": []any{hardDatabaseValueBytes + 1}},
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if result.Success || !strings.Contains(result.ErrorMessage, "value exceeds") {
			t.Fatalf("result = %#v, want deterministic value-size failure", result)
		}
		if result.ResponseBody != "" {
			t.Fatalf("oversized result retained response body of %d bytes", len(result.ResponseBody))
		}
	})
}

func TestDatabaseProtocolEvidenceSamplesRatherThanDuplicatesRows(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{"database_driver": "sqlite", "database_dsn": ":memory:"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer db.Teardown(context.Background())
	result, err := db.Execute(context.Background(), &protocol.Action{
		Name: "sample", Type: "db_query", Target: "SELECT ? AS payload UNION ALL SELECT ?", Params: map[string]any{"binds": []any{strings.Repeat("x", maxDatabaseEvidenceValueBytes+100), "second"}},
	})
	if err != nil || !result.Success {
		t.Fatalf("Execute = success %v error %v message %s", result.Success, err, result.ErrorMessage)
	}
	rows, ok := result.ProtocolEvidence["rows"].(map[string]any)
	if !ok {
		t.Fatalf("evidence rows = %#v", result.ProtocolEvidence["rows"])
	}
	if _, retainedAllRows := rows["rows"]; retainedAllRows {
		t.Fatalf("protocol evidence duplicated all result rows: %#v", rows)
	}
	if scalar, _ := rows["scalar"].(string); len(scalar) != maxDatabaseEvidenceValueBytes {
		t.Fatalf("sampled scalar length = %d, want %d", len(scalar), maxDatabaseEvidenceValueBytes)
	}
}

func TestDatabaseSQLFailureKeepsRootCauseAheadOfAssertions(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{
		"database_driver": "sqlite",
		"database_dsn":    ":memory:",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer db.Teardown(context.Background())
	result, err := db.Execute(context.Background(), &protocol.Action{
		Name:   "missing table",
		Type:   "db_query",
		Target: "SELECT * FROM missing_orders",
		Assertions: []protocol.Assertion{{
			Type:     "body_contains",
			Expected: "order",
		}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "missing_orders") ||
		strings.Contains(result.ErrorMessage, "body_contains assertion failed") {
		t.Fatalf("database failure = %#v, want original SQL error", result)
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

func TestDatabaseInitRestrictsSQLiteToSafeInMemoryDSNs(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		dsn    string
		ok     bool
	}{
		{name: "anonymous memory", driver: "sqlite", dsn: ":memory:", ok: true},
		{name: "named memory", driver: "sqlite", dsn: "file:bounded_test?mode=memory&cache=shared", ok: true},
		{name: "memory alias", driver: "sqlite", dsn: "file::memory:?cache=shared", ok: true},
		{name: "absolute disk path", driver: "sqlite", dsn: "/tmp/apexload-untrusted.db"},
		{name: "relative disk path", driver: "sqlite", dsn: "untrusted.db"},
		{name: "disk file URI", driver: "sqlite", dsn: "file:untrusted.db"},
		{name: "URI pragma", driver: "sqlite", dsn: "file:bounded?mode=memory&_pragma=journal_mode(WAL)"},
		{name: "path-like memory name", driver: "sqlite", dsn: "file:/tmp/bounded?mode=memory"},
		{name: "registered internal pgx driver", driver: "pgx", dsn: "postgres://example.invalid/db"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &Database{}
			err := db.Init(context.Background(), map[string]any{"database_driver": tc.driver, "database_dsn": tc.dsn})
			if tc.ok {
				if err != nil {
					t.Fatalf("Init: %v", err)
				}
				_ = db.Teardown(context.Background())
				return
			}
			if err == nil {
				_ = db.Teardown(context.Background())
				t.Fatal("Init accepted an unsafe database configuration")
			}
		})
	}
}

func TestSQLiteSafetyPolicyBlocksFilesystemCapableSQL(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{"database_driver": "sqlite", "database_dsn": ":memory:"}); err != nil {
		t.Fatal(err)
	}
	defer db.Teardown(context.Background())

	blocked := []struct {
		name       string
		actionType string
		statement  string
	}{
		{name: "attach", actionType: "db_exec", statement: "ATTACH DATABASE '/tmp/escape.db' AS escaped"},
		{name: "vacuum into", actionType: "db_exec", statement: "VACUUM INTO '/tmp/escape.db'"},
		{name: "pragma", actionType: "db_query", statement: "PRAGMA database_list"},
		{name: "pragma table function", actionType: "db_query", statement: "SELECT * FROM pragma_database_list"},
		{name: "extension", actionType: "db_query", statement: "SELECT load_extension('/tmp/escape.so')"},
		{name: "quoted extension", actionType: "db_query", statement: "SELECT \"load_extension\"('/tmp/escape.so')"},
		{name: "file writer", actionType: "db_query", statement: "SELECT writefile('/tmp/escape', 'data')"},
		{name: "virtual table", actionType: "db_exec", statement: "CREATE VIRTUAL TABLE escaped USING fsdir('/tmp')"},
		{name: "multiple statements", actionType: "db_query", statement: "SELECT 1; ATTACH DATABASE '/tmp/escape.db' AS escaped"},
	}
	for _, tc := range blocked {
		t.Run(tc.name, func(t *testing.T) {
			result, err := db.Execute(context.Background(), &protocol.Action{Name: tc.name, Type: tc.actionType, Target: tc.statement})
			if err != nil {
				t.Fatal(err)
			}
			if result.Success || !strings.Contains(result.ErrorMessage, "SQLite safety policy") {
				t.Fatalf("result = success:%v error:%q", result.Success, result.ErrorMessage)
			}
		})
	}

	result, err := db.Execute(context.Background(), &protocol.Action{Name: "literal", Type: "db_query", Target: "SELECT 'attach' AS value"})
	if err != nil || !result.Success {
		t.Fatalf("safe quoted literal = result:%#v error:%v", result, err)
	}
}

func TestDatabaseStatementAndBindLimits(t *testing.T) {
	db := &Database{}
	if err := db.Init(context.Background(), map[string]any{"database_driver": "sqlite", "database_dsn": ":memory:"}); err != nil {
		t.Fatal(err)
	}
	defer db.Teardown(context.Background())

	result, err := db.Execute(context.Background(), &protocol.Action{Name: "large sql", Type: "db_query", Target: "SELECT 1 -- " + strings.Repeat("x", hardDatabaseSQLBytes)})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, "SQL exceeds") {
		t.Fatalf("large SQL = result:%#v error:%v", result, err)
	}
	result, err = db.Execute(context.Background(), &protocol.Action{
		Name: "large bind", Type: "db_query", Target: "SELECT ?", Params: map[string]any{"binds": []any{strings.Repeat("x", hardDatabaseBindValueBytes+1)}},
	})
	if err != nil || result.Success || !strings.Contains(result.ErrorMessage, "bind 0 exceeds") {
		t.Fatalf("large bind = result:%#v error:%v", result, err)
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

func TestDatabaseScalarAndDSNErrorRedaction(t *testing.T) {
	const (
		scalarSecret = "synth-db-scalar-secret-9101"
		dsnSecret    = "synth-db-dsn-secret-9102"
	)

	db := &Database{}
	db.SetRuntimeTextRedactor(func(s string) string {
		return strings.ReplaceAll(s, scalarSecret, "[REDACTED]")
	})
	if err := db.Init(context.Background(), map[string]any{
		"database_driver": "sqlite",
		"database_dsn":    ":memory:",
	}); err != nil {
		t.Fatalf("init database: %v", err)
	}
	defer db.Teardown(context.Background())

	res, err := db.Execute(context.Background(), &protocol.Action{
		Name:   "select-secret-scalar",
		Type:   "db_query",
		Target: "SELECT ? AS col_zero, 'visible-row' AS col_one",
		Params: map[string]any{"binds": []any{scalarSecret}},
	})
	if err != nil || !res.Success {
		t.Fatalf("db_query failed: err=%v msg=%s", err, res.ErrorMessage)
	}
	if strings.Contains(res.ResponseBody, scalarSecret) {
		t.Fatalf("database ResponseBody leaked scalar secret: %s", res.ResponseBody)
	}
	rowsEvidence, _ := res.ProtocolEvidence["rows"].(map[string]any)
	if scalar, _ := rowsEvidence["scalar"].(string); strings.Contains(scalar, scalarSecret) {
		t.Fatalf("database ProtocolEvidence scalar leaked secret: %q", scalar)
	}

	dsnHolder := &Database{dsn: "postgres://dbadmin:" + dsnSecret + "@db.example.test:5432/orders"}
	redactedErr := dsnHolder.redactError(errors.New("dial error on postgres://dbadmin:" + dsnSecret + "@db.example.test:5432/orders: connection refused"))
	if redactedErr == nil || strings.Contains(redactedErr.Error(), dsnSecret) {
		t.Fatalf("database redactError leaked DSN credential: %v", redactedErr)
	}

	failedRes, err := db.Execute(context.Background(), &protocol.Action{
		Name:    "failing-secret-query",
		Type:    "db_query",
		Target:  "SELECT * FROM missing_table WHERE secret = '" + scalarSecret + "'",
		Headers: map[string]string{"X-AccPassword": "short-db-hdr-pwd", "X-Custom": scalarSecret},
		Body:    []byte("body-" + scalarSecret),
	})
	if err != nil || failedRes.Success || failedRes.Snapshot == nil {
		t.Fatalf("expected failing query with snapshot, got err=%v res=%#v", err, failedRes)
	}
	snapJSON, _ := json.Marshal(failedRes.Snapshot)
	evJSON, _ := json.Marshal(failedRes.ProtocolEvidence)
	for _, secret := range []string{scalarSecret, "short-db-hdr-pwd"} {
		if strings.Contains(string(snapJSON), secret) || strings.Contains(string(evJSON), secret) {
			t.Fatalf("database failure snapshot/evidence leaked %q: snap=%s ev=%s", secret, snapJSON, evJSON)
		}
	}

	// Boundary: when "[REDACTED]" expansion pushes scrubbed JSON past hardDatabaseResponseBytes,
	// scrubQueryOutputs fails closed to "[REDACTED]" rather than retaining raw cells.
	const cellCount = 450_000
	rows := make([]map[string]any, cellCount)
	for i := range rows {
		rows[i] = map[string]any{"password": "a"}
	}
	nearLimitJSON, err := json.Marshal(map[string]any{"rows": rows})
	if err != nil || len(nearLimitJSON) >= hardDatabaseResponseBytes {
		t.Fatalf("unexpected fixture size=%d err=%v", len(nearLimitJSON), err)
	}
	overflowRes := &protocol.Result{ResponseBody: string(nearLimitJSON)}
	db.scrubQueryOutputs(overflowRes, nil)
	if overflowRes.ResponseBody != "[REDACTED]" {
		t.Fatalf("expected fail-closed [REDACTED] on scrubbed payload byte overflow, got len=%d", len(overflowRes.ResponseBody))
	}

	// Finding 5 regression: scalar_equals evaluates against raw DB scalar (passes when matching),
	// and scrubs DB-resident sensitive-column scalars (<24 chars, not in DSL) on assertion failure.
	const dbResidentShortKey = "db-key-77"
	passScalarRes, err := db.Execute(context.Background(), &protocol.Action{
		Name:   "assert-scalar-pass",
		Type:   "db_query",
		Target: "SELECT '" + dbResidentShortKey + "' AS api_key",
		Assertions: []protocol.Assertion{
			{Type: "scalar_equals", Expected: dbResidentShortKey},
		},
	})
	if err != nil || !passScalarRes.Success {
		t.Fatalf("expected scalar_equals assertion against raw DB scalar to pass, got err=%v res=%#v", err, passScalarRes)
	}
	failScalarRes, err := db.Execute(context.Background(), &protocol.Action{
		Name:   "assert-scalar-fail",
		Type:   "db_query",
		Target: "SELECT '" + dbResidentShortKey + "' AS api_key",
		Assertions: []protocol.Assertion{
			{Type: "scalar_equals", Expected: "different-expected-value"},
		},
	})
	if err != nil || failScalarRes.Success {
		t.Fatalf("expected scalar_equals mismatch to fail, got err=%v res=%#v", err, failScalarRes)
	}
	if strings.Contains(failScalarRes.ErrorMessage, dbResidentShortKey) || !strings.Contains(failScalarRes.ErrorMessage, "[REDACTED]") {
		t.Fatalf("scalar_equals failure ErrorMessage leaked DB-resident sensitive scalar %q: %q", dbResidentShortKey, failScalarRes.ErrorMessage)
	}

	// Structured slice/map cells and scalars must never panic on non-comparable Go
	// values and must recursively scrub nested sensitive keys and secret strings.
	structuredPayload := map[string]any{
		"columns": []string{"meta", "tags", "password"},
		"rows": []map[string]any{{
			"meta": map[string]any{
				"safe":        "visible",
				"accPassword": "short-nested-pin-99",
				"dsn":         scalarSecret,
			},
			"tags":     []any{"alpha", scalarSecret, map[string]any{"api_key": "short-tag-key"}},
			"password": []any{"secret-array-item"},
		}},
		"first_row": map[string]any{
			"meta": map[string]any{
				"safe":        "visible",
				"accPassword": "short-nested-pin-99",
			},
			"tags": []string{"alpha", scalarSecret},
		},
		"scalar": []any{"neutral-elem", map[string]any{"secretToken": "short-scalar-tok"}},
	}
	if !db.scrubQueryPayloadMap(structuredPayload) {
		t.Fatal("expected structured payload map with nested secrets to report changed=true")
	}
	structuredEncoded, err := json.Marshal(structuredPayload)
	if err != nil {
		t.Fatalf("marshal scrubbed structured payload: %v", err)
	}
	for _, secret := range []string{"short-nested-pin-99", scalarSecret, "short-tag-key", "secret-array-item", "short-scalar-tok"} {
		if strings.Contains(string(structuredEncoded), secret) {
			t.Fatalf("scrubbed structured database payload leaked %q: %s", secret, structuredEncoded)
		}
	}
	for _, keep := range []string{"visible", "alpha", "neutral-elem"} {
		if !strings.Contains(string(structuredEncoded), keep) {
			t.Fatalf("scrubbed structured database payload dropped benign value %q: %s", keep, structuredEncoded)
		}
	}

	// Clean non-comparable slice/map payload reports changed=false without panicking.
	cleanStructured := map[string]any{
		"columns": []string{"tags", "attrs"},
		"rows": []map[string]any{{
			"tags":  []any{"one", "two"},
			"attrs": map[string]any{"env": "staging"},
		}},
		"scalar": []any{"one", "two"},
	}
	if db.scrubQueryPayloadMap(cleanStructured) {
		t.Fatal("expected clean structured payload to report changed=false")
	}

	// Excessive nesting past maxDatabaseCellScrubDepth fails closed to [REDACTED].
	deep := any(map[string]any{"leaf": "deep-secret"})
	for range maxDatabaseCellScrubDepth + 2 {
		deep = map[string]any{"child": deep}
	}
	deepPayload := map[string]any{
		"columns": []string{"nested"},
		"rows":    []map[string]any{{"nested": deep}},
		"scalar":  deep,
	}
	if !db.scrubQueryPayloadMap(deepPayload) {
		t.Fatal("expected over-depth payload to fail closed with changed=true")
	}
	deepEncoded, _ := json.Marshal(deepPayload)
	if strings.Contains(string(deepEncoded), "deep-secret") {
		t.Fatalf("over-depth database payload leaked leaf value: %s", deepEncoded)
	}

	// Action Params containing map[string]string or over-depth nesting must be scrubbed in ProtocolEvidence.
	deepParam := any("deep-param-secret")
	for range 11 {
		deepParam = map[string]any{"child": deepParam}
	}
	paramEvRes, err := db.Execute(context.Background(), &protocol.Action{
		Name:   "param-map-string-string",
		Type:   "db_query",
		Target: "SELECT 1 AS ok",
		Params: map[string]any{
			"labels": map[string]string{
				"password": "short-param-map-pwd",
				"dsn":      scalarSecret,
				"env":      "staging",
			},
			"over_depth": deepParam,
		},
	})
	if err != nil || !paramEvRes.Success {
		t.Fatalf("param-map-string-string Execute: err=%v res=%#v", err, paramEvRes)
	}
	paramEvJSON, _ := json.Marshal(paramEvRes.ProtocolEvidence)
	for _, secret := range []string{"short-param-map-pwd", scalarSecret, "deep-param-secret"} {
		if strings.Contains(string(paramEvJSON), secret) {
			t.Fatalf("database ProtocolEvidence leaked param secret %q: %s", secret, paramEvJSON)
		}
	}
}
