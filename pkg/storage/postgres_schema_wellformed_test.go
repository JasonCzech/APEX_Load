package storage

import (
	"regexp"
	"strings"
	"testing"
)

// TestPostgresRewrittenSchemaIsWellFormed guards the whole schema, not one
// table: rewriteSQL strips inline REFERENCES and table-level FOREIGN KEY lines
// for Postgres (postgres_foreign_keys.go re-adds them as constraints once every
// table exists). If that strip ever leaves a dangling comma or an unbalanced
// paren, the CREATE TABLE becomes a syntax error and Postgres schema init fails
// outright — every Postgres deployment breaks at startup, and no SQLite test
// would notice.
func TestPostgresRewrittenSchemaIsWellFormed(t *testing.T) {
	tableName := regexp.MustCompile(`CREATE TABLE IF NOT EXISTS\s+(\w+)`)

	for _, statement := range schemaCreateTableStatements() {
		name := "unknown"
		if match := tableName.FindStringSubmatch(statement); match != nil {
			name = match[1]
		}
		rewritten := rewriteSQL(statement, storeDialectPostgres)
		compact := strings.Join(strings.Fields(rewritten), " ")

		// A trailing comma before the closing paren is the specific failure a
		// stripped FOREIGN KEY line produces.
		if strings.Contains(compact, ", )") || strings.Contains(compact, ",)") {
			t.Errorf("%s: dangling comma after the Postgres rewrite (syntax error): %s", name, compact)
		}
		if strings.Contains(compact, "( ,") || strings.Contains(compact, "(,") {
			t.Errorf("%s: leading comma after the Postgres rewrite: %s", name, compact)
		}
		if strings.Contains(compact, ", ,") || strings.Contains(compact, ",,") {
			t.Errorf("%s: doubled comma after the Postgres rewrite: %s", name, compact)
		}
		if strings.Count(rewritten, "(") != strings.Count(rewritten, ")") {
			t.Errorf("%s: unbalanced parentheses after the Postgres rewrite: %s", name, compact)
		}
		// FKs are re-added by postgres_foreign_keys.go, so none may survive the
		// CREATE — a forward reference to a not-yet-created table would fail.
		if strings.Contains(rewritten, "REFERENCES") || strings.Contains(rewritten, "FOREIGN KEY") {
			t.Errorf("%s: a foreign key survived the Postgres rewrite: %s", name, compact)
		}
		// DATETIME is not a Postgres type.
		if regexp.MustCompile(`(?i)\bDATETIME\b`).MatchString(rewritten) {
			t.Errorf("%s: DATETIME survived the Postgres rewrite: %s", name, compact)
		}
	}
}

// TestPostgresRewrittenColumnMigrationsAreWellFormed applies the same guard to
// the additive ALTER TABLE path, which upgrades run but fresh installs do not.
func TestPostgresRewrittenColumnMigrationsAreWellFormed(t *testing.T) {
	for _, migration := range schemaColumnMigrations() {
		rewritten := rewriteSQL(migration.query, storeDialectPostgres)
		compact := strings.Join(strings.Fields(rewritten), " ")
		if strings.Contains(rewritten, "REFERENCES") || strings.Contains(rewritten, "FOREIGN KEY") {
			t.Errorf("%s: a foreign key survived the Postgres rewrite: %s", migration.name, compact)
		}
		if regexp.MustCompile(`(?i)\bDATETIME\b`).MatchString(rewritten) {
			t.Errorf("%s: DATETIME survived the Postgres rewrite: %s", migration.name, compact)
		}
		if strings.Count(rewritten, "(") != strings.Count(rewritten, ")") {
			t.Errorf("%s: unbalanced parentheses: %s", migration.name, compact)
		}
	}
}
