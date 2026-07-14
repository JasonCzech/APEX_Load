package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSQLiteStoreEnforcesDeclaredForeignKeys(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var enabled int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", enabled)
	}

	now := time.Now().UTC()
	err = store.SaveRunArchive(&RunArchiveRecord{
		TestID:       "missing-test",
		ArchivePath:  "/archive/missing-test",
		ManifestPath: "/archive/missing-test/manifest.json",
		CreatedAt:    now,
		CompletedAt:  now,
	})
	if err == nil {
		t.Fatal("orphan archive metadata was accepted with foreign keys enabled")
	}
	if _, getErr := store.GetRunArchive("missing-test"); getErr != sql.ErrNoRows {
		t.Fatalf("GetRunArchive after rejected orphan = %v, want sql.ErrNoRows", getErr)
	}
}

func TestSaveDataPoolRowRejectsCrossPoolIDConflict(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, pool := range []*SharedDataPool{
		{ID: "pool-a", Name: "A", CreatedAt: now},
		{ID: "pool-b", Name: "B", CreatedAt: now},
	} {
		if err := store.SaveDataPool(pool); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{ID: "row-1", PoolID: "pool-a", Data: map[string]string{"owner": "a"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{ID: "row-1", PoolID: "pool-b", Data: map[string]string{"owner": "b"}}); err == nil {
		t.Fatal("expected cross-pool row ID conflict to be rejected")
	}
	row, err := store.GetDataPoolRowFromPool("pool-a", "row-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.Data["owner"] != "a" {
		t.Fatalf("row was mutated across pools: %#v", row.Data)
	}
	if deleted, err := store.DeleteDataPoolRowFromPool("pool-b", "row-1"); err != nil || deleted {
		t.Fatalf("cross-pool delete = %v, %v; want false, nil", deleted, err)
	}
}

func TestSaveScenarioWithDataPoolBindingsRollsBackAsUnit(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveScript(&dsl.Script{ID: "script-1", Name: "Script", Protocol: "http", ProjectID: DefaultProjectID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDataPool(&SharedDataPool{ID: "pool-1", Name: "Pool", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{ID: "scenario-1", Name: "Before", Duration: "1m", CreatedAt: now}
	scripts := []ScenarioScript{{ScenarioID: scenario.ID, ScriptID: "script-1"}}
	bindings := []ScenarioDataPoolBinding{{ID: "binding-1", ScenarioID: scenario.ID, PoolID: "pool-1", Alias: "data", AccessMode: "read_only", SelectionStrategy: "sequential", CreatedAt: now}}
	if err := store.SaveScenarioWithDataPoolBindings(scenario, scripts, bindings); err != nil {
		t.Fatal(err)
	}

	scenario.Name = "After"
	duplicateAliases := []ScenarioDataPoolBinding{
		{ID: "binding-2", PoolID: "pool-1", Alias: "duplicate", AccessMode: "read_only", SelectionStrategy: "sequential"},
		{ID: "binding-3", PoolID: "pool-1", Alias: "duplicate", AccessMode: "read_only", SelectionStrategy: "sequential"},
	}
	if err := store.SaveScenarioWithDataPoolBindings(scenario, scripts, duplicateAliases); err == nil {
		t.Fatal("expected duplicate binding aliases to fail")
	}
	stored, _, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Before" {
		t.Fatalf("scenario name = %q, want rolled-back value Before", stored.Name)
	}
	storedBindings, err := store.ListScenarioDataPoolBindings(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedBindings) != 1 || storedBindings[0].ID != "binding-1" {
		t.Fatalf("bindings = %#v, want original binding", storedBindings)
	}
}

func TestDeleteProjectRemovesOwnedGraphAndGrants(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &Project{ID: "project-delete", Name: "Delete Me", CreatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO applications (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"app-delete", project.ID, "App", now, now}},
		{`INSERT INTO releases (id, application_id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, []any{"release-delete", "app-delete", project.ID, "Release", now, now}},
		{`INSERT INTO scripts (id, name, protocol, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, []any{"script-delete", "Script", "http", project.ID, now, now}},
		{`INSERT INTO scenarios (id, name, duration, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"scenario-delete", "Scenario", "1m", now, now}},
		{`INSERT INTO scenario_scripts (scenario_id, sort_order, script_id) VALUES (?, ?, ?)`, []any{"scenario-delete", 0, "script-delete"}},
		{`INSERT INTO shared_data_pools (id, name, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"pool-delete", "Pool", project.ID, now, now}},
		{`INSERT INTO shared_data_pool_rows (id, pool_id, data_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"row-delete", "pool-delete", `{}`, now, now}},
		{`INSERT INTO scenario_data_pool_bindings (id, scenario_id, pool_id, alias, access_mode, selection_strategy, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []any{"binding-delete", "scenario-delete", "pool-delete", "data", "read_only", "sequential", now, now}},
		{`INSERT INTO tests (id, name, status, project_id, scenario_id, start_time) VALUES (?, ?, ?, ?, ?, ?)`, []any{"test-delete", "Test", "COMPLETE", project.ID, "scenario-delete", now}},
		{`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, []any{"test-delete", now, `{}`}},
		{`INSERT INTO run_archives (test_id, schema_version, archive_path, manifest_path, transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, []any{"test-delete", archiveRecordSchemaVersion, "/archive", "/archive/manifest.json", 0, 0, 0, now, now}},
		{`INSERT INTO authoring_sessions (id, project_id, source, name, status, created_at, updated_at, last_accessed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []any{"author-delete", project.ID, "test", "Author", "draft", now, now, now}},
		{`INSERT INTO capacity_reservations (id, project_id, start_at, end_at, approval_state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, []any{"capacity-delete", project.ID, now, now.Add(time.Hour), "pending", now, now}},
		{`INSERT INTO execution_plans (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"plan-delete", project.ID, "Plan", now, now}},
		{`INSERT INTO execution_plan_runs (id, plan_id, project_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, []any{"plan-run-delete", "plan-delete", project.ID, "complete", now, now}},
		{`INSERT INTO object_repositories (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"objects-delete", project.ID, "Objects", now, now}},
		{`INSERT INTO correlation_rulesets (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"rules-delete", project.ID, "Rules", now, now}},
		{`INSERT INTO monitoring_collectors (id, project_id, name, token_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, []any{"collector-delete", project.ID, "Collector", "hash", now, now}},
		{`INSERT INTO reusable_blocks (id, project_id, name, component_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, []any{"block-delete", project.ID, "Block", `{}`, now, now}},
		{`INSERT INTO external_result_imports (id, provider, name, project_id, created_at) VALUES (?, ?, ?, ?, ?)`, []any{"import-delete", "test", "Import", project.ID, now}},
		{`INSERT INTO usage_accounting_snapshots (id, project_id, generated_at, payload_json, created_at) VALUES (?, ?, ?, ?, ?)`, []any{"usage-delete", project.ID, now, `{}`, now}},
		{`INSERT INTO users (id, subject, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"user-delete", "subject-delete", 1, now, now}},
		{`INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, []any{"role-delete", "user-delete", "project", project.ID, "viewer", now, now}},
	}
	for _, statement := range statements {
		if _, err := store.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed %q: %v", statement.query, err)
		}
	}
	if err := store.SaveServiceAccount(&ServiceAccount{
		ID: "service-delete", Name: "Service", KeyHash: "hash-delete", Active: true,
		ProjectRoles: map[string][]string{project.ID: {"editor"}, DefaultProjectID: {"viewer"}},
		CreatedAt:    now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		query string
		arg   string
	}{
		{`SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID},
		{`SELECT COUNT(*) FROM applications WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM releases WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM scripts WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM scenarios WHERE id = ?`, "scenario-delete"},
		{`SELECT COUNT(*) FROM tests WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM run_archives WHERE test_id = ?`, "test-delete"},
		{`SELECT COUNT(*) FROM shared_data_pools WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM authoring_sessions WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM execution_plans WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM monitoring_collectors WHERE project_id = ?`, project.ID},
		{`SELECT COUNT(*) FROM user_role_bindings WHERE scope_type = 'project' AND scope_id = ?`, project.ID},
	}
	for _, check := range checks {
		var count int
		if err := store.db.QueryRow(check.query, check.arg).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("query %q count = %d, want 0", check.query, count)
		}
	}
	account, err := store.GetServiceAccount("service-delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := account.ProjectRoles[project.ID]; exists {
		t.Fatalf("deleted project grant remains: %#v", account.ProjectRoles)
	}
	if len(account.ProjectRoles[DefaultProjectID]) != 1 {
		t.Fatalf("unrelated service account grant was removed: %#v", account.ProjectRoles)
	}
}
