package storage

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// These tables are handled explicitly by deleteProjectGraphTx rather than by
// an ON DELETE CASCADE foreign key. Keep this declaration beside the
// schema-derived guard: adding a project-owned table now requires either a real
// cascade or an intentional deletion/retention decision.
var manuallyDeletedProjectTables = stringSet(
	"baseline_comparison_snapshots", "scenario_comparison_policies",
	"script_library_assignments", "scenario_library_assignments", "script_library_assignments_releases", "scenario_library_assignments_releases",
	"applications", "application_secrets", "application_secret_provider_metadata", "archive_finalization_jobs", "archive_refresh_jobs",
	"authoring_sessions", "bruno_native_import_batches", "bruno_source_artifacts",
	"bruno_source_revisions", "capacity_reservations", "correlation_rulesets",
	"direct_import_receipts", "execution_plan_approvals", "execution_plan_runs",
	"execution_plan_schedule_occurrences", "execution_plan_schedules", "execution_plans",
	"external_result_imports", "folders", "github_check_intents", "github_script_sync_claims",
	"github_check_links", "github_script_sync_target_claims", "github_script_sync_target_owners", "github_sync_jobs",
	"dashboard_definitions", "dashboard_definition_revisions", "grafana_dashboard_snapshots", "grafana_dashboards", "grafana_import_sessions",
	"monitoring_collectors", "object_repositories", "powerbi_publishes",
	"project_secret_provider_metadata", "project_secrets", "protected_source_secret_sessions",
	"provisioning_requests", "release_sla_waivers", "releases", "reusable_blocks",
	"run_archives", "runtime_secret_provisioning_attempts", "scenario_data_pool_bindings",
	"scenario_schedule_approvals", "scenario_schedule_occurrences", "scenario_schedules",
	"scenario_scripts", "script_recordings", "scripts", "shared_data_pool_rows",
	"shared_data_pools", "test_history", "test_interval_summaries", "test_notes", "tests",
	"usage_accounting_snapshots", "import_assets",
)

// Cleanup jobs and audit history intentionally outlive their owner. The former
// finish physical archive deletion; the latter are immutable governance
// evidence. A declaration here is therefore a reviewed retention decision, not
// an omission from deleteProjectGraphTx.
var retainedAfterProjectDeletionTables = stringSet(
	"analysis_upload_writers",   // Exact upload owners survive project deletion.
	"analysis_attempt_journal",  // Exact writer/publication proof survives project deletion.
	"object_activity_events",    // Safe activity history follows audit retention independently of project deletion.
	"analysis_cleanup_jobs",     // Physical analytics cleanup must outlive project metadata.
	"pod_capacity_reservations", // Only generator-confirmed drainage or incarnation fencing releases capacity.
	"run_evidence_receipts",     // Tombstoned receipts outlive the run so a late evidence upload is refused.
	"archive_cleanup_jobs", "archive_version_cleanup_jobs", "audit_events", "capacity_instance_intents", "replay_session_leases",
)

// Test deletion either removes these rows or clears their test reference in
// deleteTestGraphTx. app_settings is key-owned rather than column-owned and is
// covered by the behavioral deletion test, so it does not appear here.
var manuallyHandledTestTables = stringSet(
	"archive_finalization_jobs", "archive_refresh_jobs", "capacity_reservations",
	"github_check_intents", "github_check_links", "grafana_dashboard_snapshots",
	"powerbi_publishes", "run_archives", "scenario_schedule_occurrences",
	"runtime_secret_provisioning_attempts", "scenario_schedules", "test_history",
	"test_interval_summaries", "test_notes",
)

var retainedAfterTestDeletionTables = stringSet(
	"pod_capacity_reservations", // Reconciler also drains claims whose test record has been deleted.
	"run_evidence_receipts",     // Tombstoned in deleteTestGraphTx; they outlive the run so a late upload is refused.
	"archive_cleanup_jobs", "archive_version_cleanup_jobs", "capacity_instance_intents",
)

var detachedFromTestTables = stringSet(
	"capacity_reservations", "scenario_schedule_occurrences", "scenario_schedules",
)

func stringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

type deletionOwnership struct {
	table    string
	column   string
	root     string
	onDelete string
}

func TestSQLiteDeletionGraphsCoverOwnedTables(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "deletion-coverage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ownership, err := sqliteDeletionOwnership(store)
	if err != nil {
		t.Fatal(err)
	}
	assertDeletionOwnershipCovered(t, ownership, "projects", manuallyDeletedProjectTables, retainedAfterProjectDeletionTables)
	assertDeletionOwnershipCovered(t, ownership, "tests", manuallyHandledTestTables, retainedAfterTestDeletionTables)
	assertDeletionDeclarationsUsed(t, ownership, "projects", manuallyDeletedProjectTables, retainedAfterProjectDeletionTables)
	assertDeletionDeclarationsUsed(t, ownership, "tests", manuallyHandledTestTables, retainedAfterTestDeletionTables)
	assertFunctionMutatesDeclaredTables(t, "sqlite_projects.go", "deleteProjectGraphTx", manuallyDeletedProjectTables)
	assertFunctionMutatesDeclaredTables(t, "sqlite_tests.go", "deleteTestGraphTx", manuallyHandledTestTables)
}

func TestPostgresDeletionGraphsCoverOwnedTables(t *testing.T) {
	specs, err := postgresForeignKeySpecs()
	if err != nil {
		t.Fatal(err)
	}
	ownership := make([]deletionOwnership, 0, len(specs))
	for _, spec := range specs {
		ownership = append(ownership, deletionOwnership{
			table: spec.table, column: spec.column, root: spec.references, onDelete: spec.onDelete,
		})
	}
	// The two dialects share table definitions. Columns such as test_id on a
	// recovery journal may intentionally have no FK, so they do not appear in
	// postgresForeignKeySpecs but must still participate in deletion coverage.
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "postgres-deletion-coverage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	commonOwnership, err := sqliteDeletionOwnership(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range commonOwnership {
		if item.onDelete == "" {
			ownership = append(ownership, item)
		}
	}
	assertDeletionOwnershipCovered(t, ownership, "projects", manuallyDeletedProjectTables, retainedAfterProjectDeletionTables)
	assertDeletionOwnershipCovered(t, ownership, "tests", manuallyHandledTestTables, retainedAfterTestDeletionTables)
}

func assertFunctionMutatesDeclaredTables(t *testing.T, filename, functionName string, declared map[string]struct{}) {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve deletion coverage test source path")
	}
	path := filepath.Join(filepath.Dir(currentFile), filename)
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	var sqlText strings.Builder
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil {
				sqlText.WriteString(" ")
				sqlText.WriteString(value)
			}
			return true
		})
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(sqlText.String()), " "))
	if normalized == "" {
		t.Fatalf("function %s was not found in %s", functionName, filename)
	}
	var missing []string
	for table := range declared {
		if !strings.Contains(normalized, "delete from "+table) && !strings.Contains(normalized, "update "+table) {
			missing = append(missing, table)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%s declares tables with no production graph mutation: %s", functionName, strings.Join(missing, ", "))
	}
}

func sqliteDeletionOwnership(store *SQLiteStore) ([]deletionOwnership, error) {
	rows, err := store.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	var ownership []deletionOwnership
	for _, table := range tables {
		foreignKeys, err := store.db.Query(`PRAGMA foreign_key_list(` + table + `)`)
		if err != nil {
			return nil, err
		}
		for foreignKeys.Next() {
			var id, sequence int
			var root, column, refColumn, onUpdate, onDelete, match string
			if err := foreignKeys.Scan(&id, &sequence, &root, &column, &refColumn, &onUpdate, &onDelete, &match); err != nil {
				foreignKeys.Close()
				return nil, err
			}
			ownership = append(ownership, deletionOwnership{table: table, column: column, root: root, onDelete: onDelete})
		}
		if err := foreignKeys.Err(); err != nil {
			foreignKeys.Close()
			return nil, err
		}
		foreignKeys.Close()

		columns, err := store.db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			return nil, err
		}
		for columns.Next() {
			var ordinal, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := columns.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				columns.Close()
				return nil, err
			}
			var root string
			switch strings.ToLower(name) {
			case "project_id":
				root = "projects"
			case "test_id", "bound_test_id", "last_run_test_id":
				root = "tests"
			}
			if root != "" && !hasDeletionOwnership(ownership, table, name, root) {
				ownership = append(ownership, deletionOwnership{table: table, column: name, root: root})
			}
		}
		if err := columns.Err(); err != nil {
			columns.Close()
			return nil, err
		}
		columns.Close()
	}
	return ownership, nil
}

func hasDeletionOwnership(ownership []deletionOwnership, table, column, root string) bool {
	for _, item := range ownership {
		if item.table == table && item.column == column && item.root == root {
			return true
		}
	}
	return false
}

func assertDeletionOwnershipCovered(t *testing.T, ownership []deletionOwnership, root string, manual, retained map[string]struct{}) {
	t.Helper()
	ownership = expandedDeletionOwnership(ownership, root)
	var uncovered []string
	for _, item := range ownership {
		if item.root != root || item.table == root || strings.EqualFold(item.onDelete, "CASCADE") {
			continue
		}
		if _, ok := manual[item.table]; ok {
			continue
		}
		if _, ok := retained[item.table]; ok {
			continue
		}
		uncovered = append(uncovered, fmt.Sprintf("%s.%s", item.table, item.column))
	}
	sort.Strings(uncovered)
	if len(uncovered) > 0 {
		t.Fatalf("%s deletion has unclassified owned columns: %s; add a cascade, an explicit graph mutation, or a reviewed retention declaration",
			root, strings.Join(uncovered, ", "))
	}
}

func assertDeletionDeclarationsUsed(t *testing.T, ownership []deletionOwnership, root string, declarations ...map[string]struct{}) {
	t.Helper()
	ownership = expandedDeletionOwnership(ownership, root)
	ownedTables := make(map[string]struct{})
	for _, item := range ownership {
		if item.root == root && item.table != root {
			ownedTables[item.table] = struct{}{}
		}
	}
	var stale []string
	for _, declaration := range declarations {
		for table := range declaration {
			if _, ok := ownedTables[table]; !ok {
				stale = append(stale, table)
			}
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("%s deletion has stale manual declarations: %s", root, strings.Join(stale, ", "))
	}
}

func expandedDeletionOwnership(ownership []deletionOwnership, root string) []deletionOwnership {
	expanded := append([]deletionOwnership(nil), ownership...)
	ownedTables := map[string]struct{}{root: {}}
	nonPropagating := retainedAfterProjectDeletionTables
	if root == "tests" {
		nonPropagating = stringSet()
		for table := range retainedAfterTestDeletionTables {
			nonPropagating[table] = struct{}{}
		}
		for table := range detachedFromTestTables {
			nonPropagating[table] = struct{}{}
		}
	}
	for {
		changed := false
		for _, item := range ownership {
			if _, parentOwned := ownedTables[item.root]; !parentOwned {
				continue
			}
			_, stopsAtTable := nonPropagating[item.table]
			if _, alreadyOwned := ownedTables[item.table]; !alreadyOwned && !stopsAtTable {
				ownedTables[item.table] = struct{}{}
				changed = true
			}
			if item.root != root && !hasDeletionOwnership(expanded, item.table, item.column, root) {
				item.root = root
				expanded = append(expanded, item)
			}
		}
		if !changed {
			return expanded
		}
	}
}
