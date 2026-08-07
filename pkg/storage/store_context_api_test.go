package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newDialectProbeDB(t *testing.T, dialect storeDialect) *storeDB {
	t.Helper()
	raw, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "probe.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	return &storeDB{db: raw, dialect: dialect}
}

// TestStoreDBContextMethodsApplyTheDialectRewrite is the reason the context
// methods have to live on storeDB rather than being hand-rolled at call sites.
// `DATETIME(...)` is a real SQLite function and a rewrite target, so a query that
// succeeds under the SQLite dialect must fail under the Postgres dialect — which
// can only happen if rewriteSQL ran inside the wrapper.
func TestStoreDBContextMethodsApplyTheDialectRewrite(t *testing.T) {
	const probe = `SELECT DATETIME('now')`
	ctx := context.Background()

	sqlite := newDialectProbeDB(t, storeDialectSQLite)
	var value string
	if err := sqlite.QueryRowContext(ctx, probe).Scan(&value); err != nil {
		t.Fatalf("QueryRowContext under the SQLite dialect: %v", err)
	}
	if _, err := sqlite.ExecContext(ctx, probe); err != nil {
		t.Fatalf("ExecContext under the SQLite dialect: %v", err)
	}
	rows, err := sqlite.QueryContext(ctx, probe)
	if err != nil {
		t.Fatalf("QueryContext under the SQLite dialect: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	postgres := newDialectProbeDB(t, storeDialectPostgres)
	rewritten := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "TIMESTAMPTZ")
	}
	if err := postgres.QueryRowContext(ctx, probe).Scan(&value); !rewritten(err) {
		t.Fatalf("QueryRowContext did not rewrite for Postgres; err = %v", err)
	}
	if _, err := postgres.ExecContext(ctx, probe); !rewritten(err) {
		t.Fatalf("ExecContext did not rewrite for Postgres; err = %v", err)
	}
	if _, err := postgres.QueryContext(ctx, probe); !rewritten(err) {
		t.Fatalf("QueryContext did not rewrite for Postgres; err = %v", err)
	}
}

// TestStoreDBContextMethodsHonourCancellation is the capability the wrapper was
// missing: before these methods existed, every non-transactional store query was
// unbounded, so a stalled backend pinned the caller with no way to give up.
func TestStoreDBContextMethodsHonourCancellation(t *testing.T) {
	db := newDialectProbeDB(t, storeDialectSQLite)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := db.ExecContext(ctx, `SELECT 1`); !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecContext with a canceled context err = %v, want context.Canceled", err)
	}
	if _, err := db.QueryContext(ctx, `SELECT 1`); !errors.Is(err, context.Canceled) {
		t.Fatalf("QueryContext with a canceled context err = %v, want context.Canceled", err)
	}
	var value int
	if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&value); !errors.Is(err, context.Canceled) {
		t.Fatalf("QueryRowContext with a canceled context err = %v, want context.Canceled", err)
	}
}

func TestReplaySessionLeaseContextQueriesStillRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	result, err := store.TryAcquireReplaySessionLease(ctx, "ctx-session", "owner-token", "ctx-project", time.Minute, 4, 2)
	if err != nil || result != ReplaySessionLeaseAcquired {
		t.Fatalf("acquire = %v, %v", result, err)
	}
	released, err := store.ReleaseReplaySessionLease(ctx, "ctx-session", "owner-token", "ctx-project")
	if err != nil || !released {
		t.Fatalf("release = %v, %v", released, err)
	}
	released, err = store.ReleaseReplaySessionLease(ctx, "ctx-session", "owner-token", "ctx-project")
	if err != nil || released {
		t.Fatalf("second release = %v, %v; want false, nil", released, err)
	}
}

// dialectWrapperBypassExemptions lists the only files allowed to reach the raw
// database/sql handles directly.
//
//	sqlite.go              — defines the wrapper, so it necessarily calls through.
//	postgres_foreign_keys.go — emits statements that are already PostgreSQL-native
//	                         and must NOT be rewritten (the rewriter strips the
//	                         very FOREIGN KEY clauses it is adding).
var dialectWrapperBypassExemptions = map[string]string{
	"sqlite.go":                "defines storeDB/storeTx",
	"postgres_foreign_keys.go": "emits PostgreSQL-native DDL that must skip the rewriter",
}

// rawConnBypassExemptions lists the files allowed to pull a raw *sql.Conn out of
// storeDB. Conn is forbidden alongside Exec/Query/Begin because the handle it
// returns is a plain database/sql connection: every statement run on it skips
// rewriteSQL, so `?` placeholders, DATETIME and integer booleans reach
// PostgreSQL verbatim. Unlike the file-wide exemptions above this one is scoped
// to the Conn method only — the other bypass shapes stay forbidden in these
// files.
//
//	sqlite_auth.go — `BEGIN IMMEDIATE` is SQLite's only real write-lock
//	                 primitive and has to run on a pinned connection. Both sites
//	                 are hand-gated to storeDialectSQLite (s.db.dialect !=
//	                 storeDialectSQLite takes the *Tx path instead), so nothing
//	                 they run on the raw conn can reach PostgreSQL.
var rawConnBypassExemptions = map[string]string{
	"sqlite_auth.go": "SQLite-only BEGIN IMMEDIATE needs a session-pinned connection",
}

// TestStorageQueriesGoThroughTheDialectWrapper stops the bypass that motivated
// the context API from growing back. Reaching through `s.db.db` / `tx.tx` means
// hand-writing rewriteSQL at the call site, and a single omission ships
// SQLite-only SQL (`?` placeholders, DATETIME, integer booleans) to PostgreSQL
// where it fails at runtime and never in the SQLite test suite.
func TestStorageQueriesGoThroughTheDialectWrapper(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, exempt := dialectWrapperBypassExemptions[name]; exempt {
			continue
		}
		findings, err := dialectWrapperBypasses(name, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, finding := range findings {
			t.Errorf("%s bypasses the dialect wrapper — call it on storeDB/storeTx so rewriteSQL is applied", finding)
		}
	}
	if checked == 0 {
		t.Fatal("scanned no storage sources — the package scan regressed")
	}
}

// dialectWrapperBypasses reports `<x>.db.db.Exec(...)` / `tx.tx.QueryRow(...)`
// style calls: reaching past storeDB/storeTx into the raw database/sql handle.
// src may be nil (read filename from disk) or a string/[]byte of source.
func dialectWrapperBypasses(filename string, src any) ([]string, error) {
	forbidden := map[string]bool{
		"Exec": true, "ExecContext": true,
		"Query": true, "QueryContext": true,
		"QueryRow": true, "QueryRowContext": true,
		"Begin": true, "BeginTx": true,
		// Conn hands out the raw connection itself, so it is the same escape
		// hatch one indirection further along: everything executed on the
		// returned *sql.Conn bypasses rewriteSQL.
		"Conn": true,
		// Prepare/PrepareContext take the SQL text at the bypass site itself and
		// Stmt/StmtContext rebind an already-prepared one, so all four ship the
		// statement to the driver without rewriteSQL — `?` placeholders, DATETIME
		// and integer booleans reach PostgreSQL verbatim. storeDB/storeTx expose
		// no Prepare wrapper today, so there is deliberately no sanctioned way to
		// prepare a statement: a caller that needs one must add a wrapper method
		// that applies rewriteSQL first, not reach past the wrapper.
		"Prepare": true, "PrepareContext": true,
		"Stmt": true, "StmtContext": true,
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var findings []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !forbidden[method.Sel.Name] {
			return true
		}
		// A bypass reaches through the wrapper into the field of the same name:
		// `s.db.db.Exec(...)`, `tx.tx.QueryRow(...)`. A legitimate call
		// (`s.db.Exec`, `tx.Exec`) never repeats the field name.
		inner, ok := method.X.(*ast.SelectorExpr)
		if !ok || (inner.Sel.Name != "db" && inner.Sel.Name != "tx") {
			return true
		}
		switch receiver := inner.X.(type) {
		case *ast.SelectorExpr:
			if receiver.Sel.Name != inner.Sel.Name {
				return true
			}
		case *ast.Ident:
			if receiver.Name != inner.Sel.Name {
				return true
			}
		default:
			return true
		}
		if method.Sel.Name == "Conn" {
			if _, exempt := rawConnBypassExemptions[filepath.Base(filename)]; exempt {
				return true
			}
		}
		findings = append(findings, fmt.Sprintf("%s:%d: %s", filename, fset.Position(call.Pos()).Line, method.Sel.Name))
		return true
	})
	return findings, nil
}

// TestDialectWrapperBypassDetectorFlagsTheShapesItIsMeantTo pins the detector
// itself against the exact code that used to live in replay_session_leases.go
// (raw ExecContext/BeginTx with a hand-written rewriteSQL) and against the
// wrapper calls that must stay allowed.
func TestDialectWrapperBypassDetectorFlagsTheShapesItIsMeantTo(t *testing.T) {
	const preFix = `package storage

func bypass(s *SQLiteStore, ctx context.Context, tx *storeTx) {
	_, _ = s.db.db.ExecContext(ctx, rewriteSQL("DELETE FROM t WHERE id = ?", s.db.dialect), 1)
	_, _ = s.db.db.BeginTx(ctx, nil)
	_, _ = tx.tx.ExecContext(ctx, rewriteSQL("DELETE FROM t", tx.dialect))
	_ = tx.tx.QueryRowContext(ctx, rewriteSQL("SELECT 1", tx.dialect))
	// Taking the raw connection is the same bypass one hop later: the returned
	// *sql.Conn never applies rewriteSQL, so a caller that copies this shape
	// without the SQLite dialect gate ships question-mark placeholders to
	// PostgreSQL.
	_, _ = s.db.db.Conn(ctx)
	// Prepared statements are the same bypass with the SQL text sitting right at
	// the call: nothing rewrites it, and the *sql.Stmt keeps re-running the
	// unrewritten text for the life of the handle.
	_, _ = s.db.db.Prepare("DELETE FROM t WHERE id = ?")
	_, _ = s.db.db.PrepareContext(ctx, "DELETE FROM t WHERE id = ?")
	_ = tx.tx.Stmt(nil)
	_ = tx.tx.StmtContext(ctx, nil)
}
`
	findings, err := dialectWrapperBypasses("prefix.go", preFix)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 9 {
		t.Fatalf("detector found %d bypasses in the pre-fix shape, want 9: %v", len(findings), findings)
	}

	const wrapped = `package storage

func allowed(s *SQLiteStore, ctx context.Context, tx *storeTx) {
	_, _ = s.db.ExecContext(ctx, "DELETE FROM t WHERE id = ?", 1)
	_, _ = s.db.BeginTx(ctx, nil)
	_, _ = s.db.Exec("DELETE FROM t")
	_, _ = tx.ExecContext(ctx, "DELETE FROM t")
	_ = tx.QueryRow("SELECT 1")
}
`
	findings, err = dialectWrapperBypasses("wrapped.go", wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("detector flagged wrapper calls: %v", findings)
	}

	// The Conn exemption is scoped to the method, not to the file: sqlite_auth.go
	// may pin a connection for BEGIN IMMEDIATE, but the ordinary bypass shapes
	// stay forbidden there.
	const exempted = `package storage

func exempted(s *SQLiteStore, ctx context.Context) {
	_, _ = s.db.db.Conn(ctx)
	_, _ = s.db.db.ExecContext(ctx, "DELETE FROM t")
	_, _ = s.db.db.PrepareContext(ctx, "DELETE FROM t")
}
`
	findings, err = dialectWrapperBypasses("sqlite_auth.go", exempted)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 ||
		!strings.HasSuffix(findings[0], "ExecContext") ||
		!strings.HasSuffix(findings[1], "PrepareContext") {
		t.Fatalf("exempted-file findings = %v, want the ExecContext and PrepareContext bypasses", findings)
	}
}
