package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSQLiteExecutableJSONCorruptionFailsClosed(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SaveScript(&dsl.Script{ID: "script-corrupt", Name: "Corrupt", Protocol: "http"}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE scripts SET actions = ? WHERE id = ?`, `{`, "script-corrupt"); err != nil {
		t.Fatalf("corrupt script: %v", err)
	}
	if _, err := store.GetScript("script-corrupt"); err == nil || !strings.Contains(err.Error(), "actions") {
		t.Fatalf("GetScript error = %v, want contextual actions decode error", err)
	}
	if _, err := store.ListScripts("", nil); err == nil || !strings.Contains(err.Error(), "actions") {
		t.Fatalf("ListScripts error = %v, want contextual actions decode error", err)
	}

	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "test-corrupt", Name: "Corrupt", Status: "QUEUED", ProjectID: DefaultProjectID, StartTime: now}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE tests SET groups_json = ? WHERE id = ?`, `{`, "test-corrupt"); err != nil {
		t.Fatalf("corrupt test: %v", err)
	}
	if _, err := store.GetTest("test-corrupt"); err == nil || !strings.Contains(err.Error(), "groups_json") {
		t.Fatalf("GetTest error = %v, want contextual groups_json decode error", err)
	}
	if _, err := store.ListTests("", nil); err == nil || !strings.Contains(err.Error(), "groups_json") {
		t.Fatalf("ListTests error = %v, want contextual groups_json decode error", err)
	}
}

func TestSQLiteScriptMarshalFailureDoesNotPersistPartialJSON(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	script := &dsl.Script{
		ID: "script-unmarshalable", Name: "Bad Metadata", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"bad": func() {}}}},
	}
	if err := store.SaveScript(script); err == nil || !strings.Contains(err.Error(), "metadata_json") {
		t.Fatalf("SaveScript error = %v, want contextual marshal error", err)
	}
	if created, err := store.CreateScriptIfAbsent(script); err == nil || created || !strings.Contains(err.Error(), "metadata_json") {
		t.Fatalf("CreateScriptIfAbsent = %v, %v, want false contextual marshal error", created, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scripts WHERE id = ?`, script.ID).Scan(&count); err != nil {
		t.Fatalf("count scripts: %v", err)
	}
	if count != 0 {
		t.Fatalf("persisted scripts = %d, want zero", count)
	}
}

func TestSQLiteTestMarshalFailureDoesNotOverwriteDurableRun(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record := &TestRecord{ID: "test-unmarshalable", Name: "Original", Status: "QUEUED", StartTime: time.Now().UTC(), Groups: []any{}}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest original: %v", err)
	}
	record.Name = "Should Not Persist"
	record.RuntimeSettings = func() {}
	if err := store.SaveTest(record); err == nil || !strings.Contains(err.Error(), "runtime_settings_json") {
		t.Fatalf("SaveTest error = %v, want contextual marshal error", err)
	}
	loaded, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if loaded.Name != "Original" {
		t.Fatalf("durable name = %q, want original after marshal failure", loaded.Name)
	}
}

func TestSQLiteScenarioJSONFailuresAreAtomicAndFailClosed(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	scenario := &Scenario{ID: "scenario-json", Name: "Original", Duration: "1m", CreatedAt: now}
	if err := store.SaveScenario(scenario, nil); err != nil {
		t.Fatalf("SaveScenario original: %v", err)
	}
	scenario.Name = "Should Not Persist"
	if err := store.SaveScenario(scenario, []ScenarioScript{{ScriptID: "unused", RuntimeSettings: func() {}}}); err == nil || !strings.Contains(err.Error(), "runtime_settings_json") {
		t.Fatalf("SaveScenario error = %v, want contextual marshal error", err)
	}
	loaded, _, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario after rollback: %v", err)
	}
	if loaded.Name != "Original" {
		t.Fatalf("durable scenario name = %q, want original", loaded.Name)
	}
	if _, err := store.db.Exec(`UPDATE scenarios SET runtime_settings_json = ? WHERE id = ?`, `{`, scenario.ID); err != nil {
		t.Fatalf("corrupt scenario runtime settings: %v", err)
	}
	if _, _, err := store.GetScenario(scenario.ID); err == nil || !strings.Contains(err.Error(), "runtime_settings_json") {
		t.Fatalf("GetScenario error = %v, want contextual runtime decode error", err)
	}
	if _, err := store.ListScenarios(); err == nil || !strings.Contains(err.Error(), "runtime_settings_json") {
		t.Fatalf("ListScenarios error = %v, want contextual runtime decode error", err)
	}
}

func TestSQLiteHistoryJSONFailuresAreReported(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "history-json", Name: "History", Status: "COMPLETE", StartTime: now, Groups: []any{}}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if err := store.SaveHistory("history-json", now, func() {}); err == nil || !strings.Contains(err.Error(), "history metrics") {
		t.Fatalf("SaveHistory error = %v, want contextual marshal error", err)
	}
	if _, err := store.db.Exec(`INSERT INTO test_history (test_id, timestamp, metrics_json) VALUES (?, ?, ?)`, "history-json", now, `{`); err != nil {
		t.Fatalf("insert corrupt history: %v", err)
	}
	if _, err := store.GetHistory("history-json"); err == nil || !strings.Contains(err.Error(), "history metrics") {
		t.Fatalf("GetHistory error = %v, want contextual decode error", err)
	}
}

func TestSQLiteScheduleGovernanceCorruptionFailsClosed(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	if err := store.SaveScenario(&Scenario{ID: "scenario-corrupt", Name: "Corrupt", Duration: "1m", CreatedAt: now}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.SaveScenarioSchedule(&ScenarioSchedule{
		ID: "schedule-corrupt", ScenarioID: "scenario-corrupt", ProjectID: DefaultProjectID,
		Name: "Corrupt", CronExpr: "0 * * * *", Enabled: true, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE scenario_schedules SET governance_json = ? WHERE id = ?`, `{`, "schedule-corrupt"); err != nil {
		t.Fatalf("corrupt schedule: %v", err)
	}
	if _, err := store.GetScenarioSchedule("schedule-corrupt"); err == nil || !strings.Contains(err.Error(), "governance_json") {
		t.Fatalf("GetScenarioSchedule error = %v, want contextual governance decode error", err)
	}
}
