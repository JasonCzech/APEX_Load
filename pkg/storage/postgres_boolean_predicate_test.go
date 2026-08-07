package storage

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Postgres dialect has no schema of its own: rewriteSQL translates the
// SQLite DDL *and DML* by substring match (sqlite.go, postgresSQLRewrites).
// TestPostgresSchemaBooleanColumnsRewriteToBoolean already guards the two halves
// that regressed before — the CREATE/ALTER column types and COALESCE(col, 0|1)
// reads. The third half is unguarded: once a column is BOOLEAN in Postgres, any
// SQL that compares it to the integer literal 0/1 is a type error there
// ("operator does not exist: boolean = integer") and passes every SQLite test.
// The rewrite table carries exactly one such entry (`active = 1`), so the next
// `WHERE enabled = 1` written anywhere in the package breaks Postgres silently.
//
// Two things this guard deliberately does NOT bound, so nobody reads it as
// wider than it is: it only sees predicates written as literal SQL text (a
// column name compared to 0/1), so integer literals bound positionally in
// INSERT ... VALUES are outside it, and so is SQL assembled with fmt.Sprintf
// from a non-literal format.

var sqlBooleanLiteralPredicate = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s*(?:!=|<>|>=|<=|=|>|<)\s*([01])\b`)

// postgresBooleanRewriteDeclaration matches the replacement text of a rewrite
// entry that declares a column BOOLEAN, e.g. "base_missing BOOLEAN NOT NULL
// DEFAULT FALSE".
var postgresBooleanRewriteDeclaration = regexp.MustCompile(`(?i)^([a-z_][a-z0-9_]*)\s+BOOLEAN\b`)

// rewriteTableBooleanColumnNames returns the columns postgresSQLRewrites itself
// converts to Postgres BOOLEAN. The rewrite table — not looksBooleanColumn — is
// the authority on which columns are BOOLEAN over there, and the two sets have
// already diverged in production: base_missing (sqlite.go) matches none of the
// heuristic's naming conventions, so a guard keyed only on the heuristic is
// blind to it. That blind spot is how COALESCE(base_missing, 0) shipped.
func rewriteTableBooleanColumnNames() map[string]bool {
	columns := map[string]bool{}
	for _, rewrite := range postgresSQLRewrites {
		match := postgresBooleanRewriteDeclaration.FindStringSubmatch(rewrite.replacement)
		if match == nil {
			continue
		}
		columns[strings.ToLower(match[1])] = true
	}
	return columns
}

// schemaBooleanColumnNames collects every column that is BOOLEAN on Postgres:
// the conventionally-boolean columns declared as INTEGER anywhere in the live
// schema (looksBooleanColumn, factory_test.go, owns the naming convention) UNION
// the columns the rewrite table declares BOOLEAN outright. Neither source alone
// is complete — the heuristic misses names that follow no convention, and the
// rewrite table misses a boolean column that has no entry yet.
func schemaBooleanColumnNames(t *testing.T) map[string]bool {
	t.Helper()
	statements := append([]string{}, schemaCreateTableStatements()...)
	for _, migration := range schemaColumnMigrations() {
		statements = append(statements, migration.query)
	}
	intColumn := regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s+INTEGER\b`)
	columns := map[string]bool{}
	for _, statement := range statements {
		for _, match := range intColumn.FindAllStringSubmatch(statement, -1) {
			if name := strings.ToLower(match[1]); looksBooleanColumn(name) {
				columns[name] = true
			}
		}
	}
	if len(columns) == 0 {
		t.Fatal("no boolean columns detected in the live schema — the schema accessors or the naming heuristic are broken")
	}
	rewriteColumns := rewriteTableBooleanColumnNames()
	if len(rewriteColumns) == 0 {
		t.Fatal("postgresSQLRewrites declares no BOOLEAN column — the rewrite-table scan regressed")
	}
	for name := range rewriteColumns {
		columns[name] = true
	}
	return columns
}

// postgresBooleanLiteralPredicates reports SQL string literals in a Go source
// file that compare a boolean column to the integer literal 0 or 1 and are still
// doing so after the Postgres rewrite. src may be nil (read from disk) or a
// string/[]byte of source.
//
// EVERY string literal is scanned, not just whole statements: this package does
// not write one statement per literal, it appends bare predicate fragments to a
// base query in ~100 places (`query += " AND archived_at IS NULL"`,
// `conditions = append(conditions, "enabled = 1")`, …). Gating on a
// SELECT/INSERT/UPDATE/DELETE keyword skipped all of those. Membership in
// booleanColumns is what keeps the false-positive rate at zero — a non-SQL
// literal has to mention a real boolean column by name and compare it to 0/1.
func postgresBooleanLiteralPredicates(filename string, src any, booleanColumns map[string]bool) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var findings []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		rewritten := rewriteSQL(text, storeDialectPostgres)
		for _, match := range sqlBooleanLiteralPredicate.FindAllStringSubmatch(rewritten, -1) {
			column := strings.ToLower(match[1])
			if !booleanColumns[column] {
				continue
			}
			findings = append(findings, fmt.Sprintf("%s:%d: %s compared to the integer literal %s",
				filename, fset.Position(literal.Pos()).Line, column, match[2]))
		}
		return true
	})
	return findings, nil
}

// TestPostgresBooleanColumnPredicatesAreDialectSafe closes the DML half of the
// boolean-rewrite trap: no SQL in the storage package may reach Postgres still
// comparing a BOOLEAN column to 0 or 1.
func TestPostgresBooleanColumnPredicatesAreDialectSafe(t *testing.T) {
	booleanColumns := schemaBooleanColumnNames(t)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		findings, err := postgresBooleanLiteralPredicates(name, nil, booleanColumns)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		for _, finding := range findings {
			t.Errorf("%s — Postgres rejects boolean = integer; parameterize the predicate (s.boolValue) or add a rewrite entry to postgresSQLRewrites", finding)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no storage sources — the package scan regressed")
	}
}

// TestPostgresBooleanPredicateDetectorFlagsTheShapesItIsMeantTo is the positive
// control for the guard above: the real sources are clean today, so without this
// the guard could silently stop detecting anything.
func TestPostgresBooleanPredicateDetectorFlagsTheShapesItIsMeantTo(t *testing.T) {
	booleanColumns := schemaBooleanColumnNames(t)
	// base_missing follows no naming convention, so it is in the set only because
	// the rewrite table declares it BOOLEAN. Pin that: dropping the rewrite-table
	// half of schemaBooleanColumnNames must fail here, not silently narrow the
	// guard back to the six columns looksBooleanColumn happens to recognize.
	for _, column := range []string{"enabled", "active", "shakeout_enabled", "baseline_pinned", "base_missing"} {
		if !booleanColumns[column] {
			t.Fatalf("boolean column %q is missing from the guard's column set — the schema scan or the rewrite-table scan regressed", column)
		}
	}

	const unsafe = "package storage\n\n" +
		"func unsafe(s *SQLiteStore) {\n" +
		"	_, _ = s.db.Query(`SELECT id FROM grafana_dashboards WHERE enabled = 1`)\n" +
		"	_, _ = s.db.Query(`SELECT id FROM scenarios WHERE shakeout_enabled != 0`)\n" +
		"	_, _ = s.db.Exec(`UPDATE tests SET baseline_pinned = 1 WHERE id = ?`, \"x\")\n" +
		"	_, _ = s.db.Query(`SELECT script_id FROM github_script_links WHERE base_missing = 1`)\n" +
		"}\n"
	findings, err := postgresBooleanLiteralPredicates("unsafe.go", unsafe, booleanColumns)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 4 {
		t.Fatalf("detector found %d unsafe predicates, want 4: %v", len(findings), findings)
	}

	// The shape this package actually writes: a bare predicate fragment appended
	// to a base query, and a condition slice joined later. Neither literal is a
	// statement, so neither carries a SELECT/UPDATE keyword — the detector must
	// still see them.
	const fragments = "package storage\n\n" +
		"func fragments(query string, conditions []string) (string, []string) {\n" +
		"	query += \" AND enabled = 1\"\n" +
		"	conditions = append(conditions, \"shakeout_enabled = 0\")\n" +
		"	conditions = append(conditions, \"base_missing = 1\")\n" +
		"	return query, conditions\n" +
		"}\n"
	findings, err = postgresBooleanLiteralPredicates("fragments.go", fragments, booleanColumns)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 {
		t.Fatalf("detector found %d unsafe predicate fragments, want 3: %v", len(findings), findings)
	}

	// `active = 1` is the one shape postgresSQLRewrites already translates, so it
	// must NOT be reported; neither may parameterized predicates, integer columns
	// that merely default to 0/1, or `?`-bound values inside string literals.
	// Scanning every literal (not just whole statements) must not start flagging
	// the fragment forms of those either.
	const safe = "package storage\n\n" +
		"func safe(s *SQLiteStore) {\n" +
		"	_, _ = s.db.Query(`SELECT id FROM users WHERE active = 1`)\n" +
		"	_, _ = s.db.Query(`SELECT id FROM users u WHERE u.active = ?`, true)\n" +
		"	_, _ = s.db.Query(`SELECT id FROM tests WHERE 1 = 1 AND revision = 1`)\n" +
		"	_, _ = s.db.Query(`SELECT id FROM fleet_pools WHERE desired_size > 0`)\n" +
		"	_, _ = s.db.Query(\" AND active = 1\")\n" +
		"	_, _ = s.db.Query(\" AND enabled = ?\", true)\n" +
		"	_, _ = s.db.Query(\" AND attempts > 0\")\n" +
		"}\n"
	findings, err = postgresBooleanLiteralPredicates("safe.go", safe, booleanColumns)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("detector flagged dialect-safe SQL: %v", findings)
	}
}
