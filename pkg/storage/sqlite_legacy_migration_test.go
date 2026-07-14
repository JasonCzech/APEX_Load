package storage

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyHierarchyMigrationIsProjectScoped(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, projectID := range []string{"legacy-project-a", "legacy-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO tests (id, name, status, project_id, duration_ns, start_time, groups_json, runtime_settings_json, goal_config_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, "test-"+projectID, "Shared Name", "COMPLETE", projectID, int64(time.Minute), now, `[]`, `{}`, `{}`); err != nil {
			t.Fatalf("insert legacy test for %s: %v", projectID, err)
		}
	}

	if err := store.ensureLegacyApplication(); err != nil {
		t.Fatalf("ensureLegacyApplication: %v", err)
	}

	type migratedContext struct {
		applicationID string
		releaseID     string
		scenarioID    string
	}
	contexts := make(map[string]migratedContext)
	for _, projectID := range []string{"legacy-project-a", "legacy-project-b"} {
		var context migratedContext
		if err := store.db.QueryRow(`SELECT application_id, release_id, scenario_id FROM tests WHERE id = ?`, "test-"+projectID).
			Scan(&context.applicationID, &context.releaseID, &context.scenarioID); err != nil {
			t.Fatalf("read migrated test for %s: %v", projectID, err)
		}
		if context.applicationID != legacyApplicationID(projectID) || context.releaseID != legacyReleaseID(projectID) || context.scenarioID != legacyScenarioID(projectID, "Shared Name") {
			t.Fatalf("migrated context for %s = %+v", projectID, context)
		}
		var applicationProject, releaseProject string
		if err := store.db.QueryRow(`SELECT project_id FROM applications WHERE id = ?`, context.applicationID).Scan(&applicationProject); err != nil {
			t.Fatalf("read migrated application: %v", err)
		}
		if err := store.db.QueryRow(`SELECT project_id FROM releases WHERE id = ?`, context.releaseID).Scan(&releaseProject); err != nil {
			t.Fatalf("read migrated release: %v", err)
		}
		if applicationProject != projectID || releaseProject != projectID {
			t.Fatalf("hierarchy ownership for %s = application %q release %q", projectID, applicationProject, releaseProject)
		}
		contexts[projectID] = context
	}
	if contexts["legacy-project-a"].applicationID == contexts["legacy-project-b"].applicationID ||
		contexts["legacy-project-a"].releaseID == contexts["legacy-project-b"].releaseID ||
		contexts["legacy-project-a"].scenarioID == contexts["legacy-project-b"].scenarioID {
		t.Fatalf("cross-project legacy hierarchy collided: %+v", contexts)
	}
}

func TestLegacyScenarioMigrationRejectsCorruptJSONAtomically(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	projectID := "legacy-corrupt-project"
	if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO tests (id, name, status, project_id, duration_ns, start_time, groups_json, runtime_settings_json, goal_config_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-corrupt-test", "Corrupt", "COMPLETE", projectID, int64(time.Minute), now, `{`, `{}`, `{}`); err != nil {
		t.Fatalf("insert corrupt legacy test: %v", err)
	}

	err = store.ensureLegacyApplication()
	if err == nil || !strings.Contains(err.Error(), "groups_json") {
		t.Fatalf("ensureLegacyApplication error = %v, want contextual groups_json error", err)
	}
	var applicationCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM applications WHERE id = ?`, legacyApplicationID(projectID)).Scan(&applicationCount); err != nil {
		t.Fatalf("count rolled-back application: %v", err)
	}
	if applicationCount != 0 {
		t.Fatalf("legacy application count = %d, want transaction rollback", applicationCount)
	}
	var applicationID, scenarioID sql.NullString
	if err := store.db.QueryRow(`SELECT application_id, scenario_id FROM tests WHERE id = ?`, "legacy-corrupt-test").Scan(&applicationID, &scenarioID); err != nil {
		t.Fatalf("read corrupt test after rollback: %v", err)
	}
	if applicationID.Valid || scenarioID.Valid {
		t.Fatalf("corrupt migration partially updated test: application=%#v scenario=%#v", applicationID, scenarioID)
	}
}

func TestLegacyGlobalScenarioMigrationRepairsProjectScopedReferences(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const scenarioName = "Shared Global Scenario"
	oldScenarioID := legacyGlobalScenarioID(scenarioName)
	now := time.Now().UTC().Truncate(time.Second)
	type projectFixture struct {
		projectID  string
		suffix     string
		scriptID   string
		testID     string
		scheduleID string
		planID     string
		runID      string
		poolID     string
	}
	fixtures := []projectFixture{
		{projectID: "legacy-upgrade-a", suffix: "a", scriptID: "legacy-script-a", testID: "legacy-test-a", scheduleID: "legacy-schedule-a", planID: "legacy-plan-a", runID: "legacy-run-a", poolID: "legacy-pool-a"},
		{projectID: "legacy-upgrade-b", suffix: "b", scriptID: "legacy-script-b", testID: "legacy-test-b", scheduleID: "legacy-schedule-b", planID: "legacy-plan-b", runID: "legacy-run-b", poolID: "legacy-pool-b"},
	}
	for _, fixture := range fixtures {
		if err := store.SaveProject(&Project{ID: fixture.projectID, Name: fixture.projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO scripts (id, name, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, fixture.scriptID, fixture.scriptID, fixture.projectID, now, now); err != nil {
			t.Fatalf("insert script for %s: %v", fixture.projectID, err)
		}
	}
	if _, err := store.db.Exec(`INSERT INTO scenarios
		(id, ref_key, name, duration, scenario_mode, runtime_settings_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, oldScenarioID, "legacy-ref-scenario", scenarioName, "9m", "edited", `{"request_timeout":"45s"}`, now, now); err != nil {
		t.Fatalf("insert old global scenario: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO scenario_scripts
		(scenario_id, sort_order, name, script_id, vusers, stages_json)
		VALUES (?, ?, ?, ?, ?, ?)`, oldScenarioID, 0, "old-global", fixtures[0].scriptID, 1, `[]`); err != nil {
		t.Fatalf("insert old global scenario script: %v", err)
	}

	for _, fixture := range fixtures {
		groupsJSON, err := json.Marshal([]map[string]any{{
			"name":      "group-" + fixture.suffix,
			"script_id": fixture.scriptID,
			"vusers":    2,
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO tests
			(id, name, status, scenario_id, project_id, duration_ns, start_time, groups_json, runtime_settings_json, goal_config_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, fixture.testID, scenarioName, "COMPLETE", oldScenarioID, fixture.projectID, int64(time.Minute), now, string(groupsJSON), `{}`, `{}`); err != nil {
			t.Fatalf("insert old migrated test for %s: %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO scenario_schedules
			(id, scenario_id, project_id, revision, name, cron_expr, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, fixture.scheduleID, oldScenarioID, fixture.projectID, 1, "schedule-"+fixture.suffix, "0 * * * *", true, now, now); err != nil {
			t.Fatalf("insert old schedule for %s: %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO scenario_schedule_occurrences
			(id, schedule_id, scenario_id, project_id, schedule_revision, planned_at, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-occurrence-"+fixture.suffix, fixture.scheduleID, oldScenarioID, fixture.projectID, 1, now.Add(time.Hour), "planned", now, now); err != nil {
			t.Fatalf("insert old occurrence for %s: %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO scenario_schedule_approvals
			(id, schedule_id, scenario_id, project_id, schedule_revision, policy, status, requested_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-approval-"+fixture.suffix, fixture.scheduleID, oldScenarioID, fixture.projectID, 1, "manual", "pending", now, now, now); err != nil {
			t.Fatalf("insert old schedule approval for %s: %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO capacity_reservations
			(id, project_id, scenario_id, start_at, end_at, approval_state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-capacity-"+fixture.suffix, fixture.projectID, oldScenarioID, now, now.Add(time.Hour), "pending", now, now); err != nil {
			t.Fatalf("insert old capacity reservation for %s: %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO external_result_imports
			(id, provider, name, project_id, scenario_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, "legacy-import-"+fixture.suffix, "legacy", "import-"+fixture.suffix, fixture.projectID, oldScenarioID, now); err != nil {
			t.Fatalf("insert old external import for %s: %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO shared_data_pools
			(id, name, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, fixture.poolID, fixture.poolID, fixture.projectID, now, now); err != nil {
			t.Fatalf("insert data pool for %s: %v", fixture.projectID, err)
		}
		if _, err := store.db.Exec(`INSERT INTO scenario_data_pool_bindings
			(id, scenario_id, pool_id, alias, access_mode, selection_strategy, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-binding-"+fixture.suffix, oldScenarioID, fixture.poolID, "data-"+fixture.suffix, "read_only", "sequential", now, now); err != nil {
			t.Fatalf("insert data binding for %s: %v", fixture.projectID, err)
		}
		entriesJSON, err := json.Marshal([]ExecutionPlanEntry{{Order: 0, ScenarioID: oldScenarioID}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO execution_plans
			(id, project_id, name, entries_json, run_mode, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, fixture.planID, fixture.projectID, fixture.planID, string(entriesJSON), "sequential", "draft", now, now); err != nil {
			t.Fatalf("insert execution plan for %s: %v", fixture.projectID, err)
		}
		runEntriesJSON, err := json.Marshal([]ExecutionPlanRunEntry{{Order: 0, ScenarioID: oldScenarioID, Status: "pending"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO execution_plan_runs
			(id, plan_id, project_id, run_mode, status, entries_json, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, fixture.runID, fixture.planID, fixture.projectID, "sequential", "running", string(runEntriesJSON), 1, now, now); err != nil {
			t.Fatalf("insert execution plan run for %s: %v", fixture.projectID, err)
		}
	}

	if err := store.ensureLegacyApplication(); err != nil {
		t.Fatalf("repair old global legacy scenario: %v", err)
	}
	// A second startup must be a no-op rather than re-keying scoped scenarios.
	if err := store.ensureLegacyApplication(); err != nil {
		t.Fatalf("repeat legacy repair: %v", err)
	}

	for _, fixture := range fixtures {
		wantScenarioID := legacyScenarioID(fixture.projectID, scenarioName)
		var gotScenarioID string
		if err := store.db.QueryRow(`SELECT scenario_id FROM tests WHERE id = ?`, fixture.testID).Scan(&gotScenarioID); err != nil {
			t.Fatalf("read repaired test for %s: %v", fixture.projectID, err)
		}
		if gotScenarioID != wantScenarioID {
			t.Errorf("test scenario for %s = %q, want %q", fixture.projectID, gotScenarioID, wantScenarioID)
		}
		for _, reference := range []struct {
			table string
			id    string
		}{
			{table: "scenario_schedules", id: fixture.scheduleID},
			{table: "scenario_schedule_occurrences", id: "legacy-occurrence-" + fixture.suffix},
			{table: "scenario_schedule_approvals", id: "legacy-approval-" + fixture.suffix},
			{table: "capacity_reservations", id: "legacy-capacity-" + fixture.suffix},
			{table: "external_result_imports", id: "legacy-import-" + fixture.suffix},
			{table: "scenario_data_pool_bindings", id: "legacy-binding-" + fixture.suffix},
		} {
			if err := store.db.QueryRow(`SELECT scenario_id FROM `+reference.table+` WHERE id = ?`, reference.id).Scan(&gotScenarioID); err != nil {
				t.Fatalf("read repaired %s for %s: %v", reference.table, fixture.projectID, err)
			}
			if gotScenarioID != wantScenarioID {
				t.Errorf("%s scenario for %s = %q, want %q", reference.table, fixture.projectID, gotScenarioID, wantScenarioID)
			}
		}
		plan, err := store.GetExecutionPlan(fixture.planID)
		if err != nil {
			t.Fatalf("GetExecutionPlan(%s): %v", fixture.planID, err)
		}
		if len(plan.Entries) != 1 || plan.Entries[0].ScenarioID != wantScenarioID {
			t.Errorf("execution plan entries for %s = %+v, want scenario %s", fixture.projectID, plan.Entries, wantScenarioID)
		}
		run, err := store.GetExecutionPlanRun(fixture.runID)
		if err != nil {
			t.Fatalf("GetExecutionPlanRun(%s): %v", fixture.runID, err)
		}
		if len(run.Entries) != 1 || run.Entries[0].ScenarioID != wantScenarioID {
			t.Errorf("execution plan run entries for %s = %+v, want scenario %s", fixture.projectID, run.Entries, wantScenarioID)
		}
		scenario, scripts, err := store.GetScenario(wantScenarioID)
		if err != nil {
			t.Fatalf("GetScenario(%s): %v", wantScenarioID, err)
		}
		if scenario.Duration != "9m" || scenario.ScenarioMode != "edited" {
			t.Errorf("scenario edits were not preserved for %s: %+v", fixture.projectID, scenario)
		}
		if len(scripts) != 1 || scripts[0].ScriptID != fixture.scriptID {
			t.Errorf("scenario scripts for %s = %+v, want project script %s", fixture.projectID, scripts, fixture.scriptID)
		}
	}
	if _, _, err := store.GetScenario(oldScenarioID); err != sql.ErrNoRows {
		t.Fatalf("old global scenario lookup error = %v, want sql.ErrNoRows", err)
	}
	var preservedRefKeys int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scenarios WHERE ref_key = ?`, "legacy-ref-scenario").Scan(&preservedRefKeys); err != nil {
		t.Fatalf("read preserved legacy scenario ref key: %v", err)
	}
	if preservedRefKeys != 1 {
		t.Fatalf("preserved legacy scenario ref key count = %d, want 1", preservedRefKeys)
	}
}
