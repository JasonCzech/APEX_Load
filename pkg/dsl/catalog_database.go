package dsl

import (
	"fmt"
	"strings"
)

// databaseActionSpecs declares the database protocol action types. The
// database validator lowercases the incoming type before catalog lookup, so
// aliases are registered in lowercase. The shared max_rows rule applies to
// every database action (supported or not) and stays in
// validateDatabaseActionIssues.
func databaseActionSpecs() []ActionSpec {
	sqlFields := []FieldSpec{
		{Name: "target", Kind: FieldKindString, Doc: "SQL statement; target or body is required."},
		{Name: "body", Kind: FieldKindString, Doc: "SQL statement alternative to target (useful for multiline SQL)."},
		{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the SQL statement. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
		{Name: "params.args", Kind: FieldKindArray, Doc: "Positional bind arguments; template variables are expanded."},
		{Name: "params.max_rows", Kind: FieldKindInt, Doc: "Row cap override; zero or greater."},
	}
	return []ActionSpec{
		{
			Type:         "db_query",
			Aliases:      []string{"database_query", "query", "select"},
			Protocols:    []string{"database"},
			Summary:      "Runs a SQL query and reads the result rows.",
			RequireOneOf: [][]string{{"target", "body", "body_file"}},
			Fields:       sqlFields,
			Validate:     validateDatabaseSQLAction,
		},
		{
			Type:         "db_exec",
			Aliases:      []string{"database_exec", "exec", "execute", "insert", "update", "delete"},
			Protocols:    []string{"database"},
			Summary:      "Executes a SQL statement without reading rows (DML/DDL).",
			RequireOneOf: [][]string{{"target", "body", "body_file"}},
			Fields:       sqlFields,
			Validate:     validateDatabaseSQLAction,
		},
		{
			Type:         "db_procedure",
			Aliases:      []string{"stored_procedure"},
			Protocols:    []string{"database"},
			Summary:      "Calls a stored procedure.",
			RequireOneOf: [][]string{{"target", "body", "body_file"}},
			Fields:       sqlFields,
			Validate:     validateDatabaseSQLAction,
		},
		{
			Type:      "db_begin",
			Aliases:   []string{"database_begin", "begin"},
			Protocols: []string{"database"},
			Summary:   "Begins a transaction on the VUser's connection.",
		},
		{
			Type:      "db_commit",
			Aliases:   []string{"database_commit", "commit"},
			Protocols: []string{"database"},
			Summary:   "Commits the open transaction.",
		},
		{
			Type:      "db_rollback",
			Aliases:   []string{"database_rollback", "rollback"},
			Protocols: []string{"database"},
			Summary:   "Rolls back the open transaction.",
		},
		{
			Type:      "db_health",
			Aliases:   []string{"database_health", "ping"},
			Protocols: []string{"database"},
			Summary:   "Pings the database connection.",
		},
	}
}

func validateDatabaseSQLAction(index int, action Action) []string {
	if strings.TrimSpace(action.Target) == "" && strings.TrimSpace(action.Body) == "" {
		return []string{fmt.Sprintf("actions[%d] %q: database action requires target or body SQL", index, action.Name)}
	}
	return nil
}
