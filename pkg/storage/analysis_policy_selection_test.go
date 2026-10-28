package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAnalysisPolicySelectionSnapshotKeepsHistoricalRevisionAndLatestDeletion(t *testing.T) {
	store, _, _ := monitorFixture(t)
	checkAnalysisPolicySelectionSnapshot(t, store)
}

func checkAnalysisPolicySelectionSnapshot(t *testing.T, store *SQLiteStore) {
	t.Helper()
	in := AnalysisDefinition{ID: "chosen", ProjectID: "monitor-p", Kind: AnalysisDefinitionKindBrowserPolicy, Name: "Original", Definition: json.RawMessage(`{"version":1}`)}
	if _, err := store.SaveAnalysisDefinition(in, 0); err != nil {
		t.Fatal(err)
	}
	in.Name = "Edited"
	if _, err := store.SaveAnalysisDefinition(in, 1); err != nil {
		t.Fatal(err)
	}
	old, current, err := store.GetAnalysisPolicyRevision(in.ProjectID, in.ID, 1)
	if err != nil || old.Revision != 1 || old.Name != "Original" || current.Revision != 2 || current.Deleted {
		t.Fatal(old, current, err)
	}
	for _, tc := range []struct {
		project  string
		revision int64
	}{{"other", 1}, {in.ProjectID, 0}, {in.ProjectID, 99}} {
		if _, _, err := store.GetAnalysisPolicyRevision(tc.project, in.ID, tc.revision); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("foreign or missing revision accepted", err)
		}
	}
	in.Deleted = true
	if _, err := store.SaveAnalysisDefinition(in, 2); err != nil {
		t.Fatal(err)
	}
	old, current, err = store.GetAnalysisPolicyRevision(in.ProjectID, in.ID, 1)
	if err != nil || old.Deleted || old.Revision != 1 || !current.Deleted || current.Revision != 3 {
		t.Fatal(old, current, err)
	}
}

func TestRunAnalysisPolicySelectionsPersistAndUpgradeLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policies.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	chosen := map[string]any{"browser": map[string]any{"id": "browser", "revision": float64(2)}}
	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "selected", ProjectID: DefaultProjectID, Name: "Selected", Status: "QUEUED", StartTime: now, AnalysisPolicySelections: chosen}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetTest("selected")
	if err != nil || !reflect.DeepEqual(loaded.AnalysisPolicySelections, chosen) {
		t.Fatal(loaded, err)
	}
	for _, statement := range []string{`ALTER TABLE tests DROP COLUMN analysis_policy_selections_json`, `DELETE FROM schema_migrations WHERE version>=73`} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetTest("selected")
	if err != nil || loaded.AnalysisPolicySelections != nil {
		t.Fatal("legacy row invented selection", loaded, err)
	}
	loaded.AnalysisPolicySelections = chosen
	if err := store.SaveTest(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetTest("selected")
	if err != nil || !reflect.DeepEqual(loaded.AnalysisPolicySelections, chosen) {
		t.Fatal(loaded, err)
	}

	if err := store.SaveScript(&dsl.Script{ID: "schedule-script", ProjectID: DefaultProjectID, Name: "Schedule script", Protocol: "http", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScenario(&Scenario{ID: "schedule-scenario", Name: "Schedule scenario", Duration: "1m", CreatedAt: now, UpdatedAt: now}, []ScenarioScript{{ScenarioID: "schedule-scenario", ScriptID: "schedule-script", Name: "group", VUsers: 1}}); err != nil {
		t.Fatal(err)
	}
	schedule := &ScenarioSchedule{ID: "selected-schedule", ScenarioID: "schedule-scenario", ProjectID: DefaultProjectID, Revision: 1, Name: "Nightly", CronExpr: "0 9 * * *", Enabled: true, AnalysisPolicySelections: chosen, CreatedAt: now, UpdatedAt: now}
	created, err := store.CreateScenarioScheduleIfAbsent(schedule)
	if err != nil || !created {
		t.Fatal(created, err)
	}
	loadedSchedule, err := store.GetScenarioSchedule("selected-schedule")
	if err != nil || !reflect.DeepEqual(loadedSchedule.AnalysisPolicySelections, chosen) {
		t.Fatal(loadedSchedule, err)
	}
	updatedSelection := map[string]any{"journey": map[string]any{"id": "journey", "revision": float64(3)}}
	loadedSchedule.AnalysisPolicySelections = updatedSelection
	updated, err := store.UpdateScenarioScheduleIfRevision(loadedSchedule, 1)
	if err != nil || !updated {
		t.Fatal(updated, err)
	}
	schedules, err := store.ListScenarioSchedules("schedule-scenario")
	if err != nil || len(schedules) != 1 || !reflect.DeepEqual(schedules[0].AnalysisPolicySelections, updatedSelection) {
		t.Fatal(schedules, err)
	}
	allSchedules, err := store.ListAllScenarioSchedules()
	if err != nil || len(allSchedules) != 1 || !reflect.DeepEqual(allSchedules[0].AnalysisPolicySelections, updatedSelection) {
		t.Fatal(allSchedules, err)
	}
	approvalSelection := map[string]any{"browser": map[string]any{"id": "browser", "revision": float64(1)}, "journey": map[string]any{"id": "journey", "revision": float64(2)}}
	loadedSchedule.PendingApprovalID = "approval-1"
	loadedSchedule.AnalysisPolicySelections = approvalSelection
	approved, err := store.SaveScenarioScheduleWithApproval(loadedSchedule, &ScenarioScheduleApproval{ID: "approval-1", ScheduleID: loadedSchedule.ID, ScenarioID: loadedSchedule.ScenarioID, ProjectID: DefaultProjectID, Policy: "manual_review", Status: "pending", RequestedBy: "editor", RequestedAt: now, RequiredRole: "project_admin", CreatedAt: now, UpdatedAt: now}, 2)
	if err != nil || !approved {
		t.Fatal(approved, err)
	}
	for _, statement := range []string{`ALTER TABLE scenario_schedules DROP COLUMN analysis_policy_selections_json`, `DELETE FROM schema_migrations WHERE version>=85`} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	loadedSchedule, err = store.GetScenarioSchedule("selected-schedule")
	if err != nil || loadedSchedule.AnalysisPolicySelections != nil {
		t.Fatal("legacy schedule row invented selection", loadedSchedule, err)
	}
	loadedSchedule.AnalysisPolicySelections = approvalSelection
	if err := store.SaveScenarioSchedule(loadedSchedule); err != nil {
		t.Fatal(err)
	}
	loadedSchedule, err = store.GetScenarioSchedule("selected-schedule")
	if err != nil || !reflect.DeepEqual(loadedSchedule.AnalysisPolicySelections, approvalSelection) {
		t.Fatal(loadedSchedule, err)
	}
}
