package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresForeignKeySpecsCoverDeclaredSchema(t *testing.T) {
	specs, err := postgresForeignKeySpecs()
	if err != nil {
		t.Fatalf("postgresForeignKeySpecs returned error: %v", err)
	}
	if got, want := len(specs), 65; got != want {
		t.Fatalf("foreign key count = %d, want %d", got, want)
	}

	byColumn := make(map[string]postgresForeignKeySpec, len(specs))
	constraintNames := make(map[string]string, len(specs))
	for _, spec := range specs {
		key := spec.table + "." + spec.column
		if _, exists := byColumn[key]; exists {
			t.Fatalf("duplicate foreign key for %s", key)
		}
		byColumn[key] = spec
		name := postgresForeignKeyConstraintName(spec)
		if len(name) > 63 {
			t.Fatalf("constraint name %q is longer than PostgreSQL's 63-byte limit", name)
		}
		if previous, exists := constraintNames[name]; exists {
			t.Fatalf("constraint name %q collides for %s and %s", name, previous, key)
		}
		constraintNames[name] = key
	}

	for key, want := range map[string]string{
		"applications.project_id":                           "projects.id",
		"bruno_source_artifacts.project_id":                 "projects.id",
		"bruno_source_revisions.raw_artifact_id":            "bruno_source_artifacts.id",
		"bruno_source_revisions.sanitized_artifact_id":      "bruno_source_artifacts.id",
		"bruno_source_revisions.script_id":                  "scripts.id",
		"bruno_native_import_batches.project_id":            "projects.id",
		"bruno_native_import_batches.raw_artifact_id":       "bruno_source_artifacts.id",
		"bruno_native_import_batches.sanitized_artifact_id": "bruno_source_artifacts.id",
		"direct_import_receipts.project_id":                 "projects.id",
		"scripts.folder_id":                                 "folders.id",
		"tests.application_id":                              "applications.id",
		"tests.scenario_id":                                 "scenarios.id",
		"archive_finalization_jobs.test_id":                 "tests.id",
		"execution_plan_schedule_occurrences.schedule_id":   "execution_plan_schedules.id",
		"github_check_intents.project_id":                   "projects.id",
		"github_script_sync_claims.project_id":              "projects.id",
		"github_script_sync_claims.script_id":               "scripts.id",
		"github_script_sync_target_claims.project_id":       "projects.id",
		"github_script_sync_target_claims.script_id":        "scripts.id",
		"github_script_sync_target_claims.target_key":       "github_script_sync_targets.target_key",
		"github_script_sync_target_owners.project_id":       "projects.id",
		"github_script_sync_target_owners.script_id":        "scripts.id",
		"github_script_sync_target_owners.target_key":       "github_script_sync_targets.target_key",
		"project_secrets.project_id":                        "projects.id",
		"protected_source_secret_sessions.project_id":       "projects.id",
		"runtime_secret_provisioning_attempts.project_id":   "projects.id",
		"grafana_dashboard_snapshots.test_id":               "tests.id",
	} {
		got, ok := byColumn[key]
		if !ok {
			t.Errorf("missing foreign key for %s", key)
			continue
		}
		if target := got.references + "." + got.refColumn; target != want {
			t.Errorf("foreign key %s targets %s, want %s", key, target, want)
		}
	}
	if got := byColumn["user_role_bindings.user_id"].onDelete; got != "CASCADE" {
		t.Errorf("user role binding ON DELETE = %q, want CASCADE", got)
	}
}

func TestFreshSQLiteSchemaEnforcesEveryDeclaredForeignKey(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	specs, err := postgresForeignKeySpecs()
	if err != nil {
		t.Fatal(err)
	}
	actual := make(map[string]bool, len(specs))
	tables := make(map[string]bool)
	for _, spec := range specs {
		tables[spec.table] = true
	}
	for table := range tables {
		rows, err := store.db.Query("PRAGMA foreign_key_list(" + table + ")")
		if err != nil {
			t.Fatalf("foreign_key_list(%s): %v", table, err)
		}
		for rows.Next() {
			var id, sequence int
			var referencedTable, column, refColumn, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &sequence, &referencedTable, &column, &refColumn, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			actual[fmt.Sprintf("%s.%s->%s.%s", table, column, referencedTable, refColumn)] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	for _, spec := range specs {
		key := fmt.Sprintf("%s.%s->%s.%s", spec.table, spec.column, spec.references, spec.refColumn)
		if !actual[key] {
			t.Errorf("fresh SQLite schema is missing %s", key)
		}
	}
}

func TestPostgresForeignKeyStatementsAreFencedAndValidated(t *testing.T) {
	spec := postgresForeignKeySpec{
		table:      "execution_plan_schedule_occurrences",
		column:     "schedule_id",
		references: "execution_plan_schedules",
		refColumn:  "id",
	}
	add := postgresAddForeignKeyStatement(spec)
	for _, fragment := range []string{
		"pg_constraint",
		"pg_get_constraintdef",
		"constraint_matches",
		`confrelid = 'execution_plan_schedules'::regclass`,
		`confdeltype = 'a'`,
		`confupdtype = 'a'`,
		`confmatchtype = 's'`,
		`NOT constraint_row.condeferrable`,
		`NOT constraint_row.condeferred`,
		`DROP CONSTRAINT`,
		"EXCEPTION WHEN duplicate_object",
		"FOREIGN KEY (\"schedule_id\")",
		"REFERENCES \"execution_plan_schedules\" (\"id\")",
		"NOT VALID",
		"END;\n$apexload$;",
	} {
		if !strings.Contains(add, fragment) {
			t.Errorf("add statement missing %q:\n%s", fragment, add)
		}
	}
	validate := postgresValidateForeignKeyStatement(spec)
	if !strings.Contains(validate, "VALIDATE CONSTRAINT") || !strings.Contains(validate, `ALTER TABLE "execution_plan_schedule_occurrences"`) {
		t.Fatalf("validation statement = %q", validate)
	}
}
