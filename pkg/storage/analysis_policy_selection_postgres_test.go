//go:build postgres_integration

package storage

import (
	"reflect"
	"testing"
	"time"
)

func TestPostgresRunAnalysisPolicySelectionSnapshotAndRestart(t *testing.T) {
	store, dsn := managementPostgresFixture(t)
	seedMonitorFixture(t, store.SQLiteStore)
	checkAnalysisPolicySelectionSnapshot(t, store.SQLiteStore)
	chosen := map[string]any{"browser": map[string]any{"id": "chosen", "revision": float64(1)}}
	if err := store.SaveTest(&TestRecord{ID: "selected-run", ProjectID: "monitor-p", ScenarioID: "monitor-scenario", Name: "Selected", Status: "QUEUED", StartTime: time.Now().UTC(), AnalysisPolicySelections: chosen}); err != nil {
		t.Fatal(err)
	}
	peer, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	loaded, err := peer.GetTest("selected-run")
	if err != nil || !reflect.DeepEqual(loaded.AnalysisPolicySelections, chosen) {
		t.Fatal(loaded, err)
	}
	if _, err := store.db.Exec(`ALTER TABLE tests DROP COLUMN analysis_policy_selections_json`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version>=73`); err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	loaded, err = upgraded.GetTest("selected-run")
	if err != nil || loaded.AnalysisPolicySelections != nil {
		t.Fatal("legacy upgrade invented selection", loaded, err)
	}
	loaded.AnalysisPolicySelections = chosen
	if err := upgraded.SaveTest(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = peer.GetTest("selected-run")
	if err != nil || !reflect.DeepEqual(loaded.AnalysisPolicySelections, chosen) {
		t.Fatal("peer lost upgraded selection", loaded, err)
	}
	now := time.Now().UTC()
	schedule := &ScenarioSchedule{ID: "selected-schedule", ScenarioID: "monitor-scenario", ProjectID: "monitor-p", Revision: 1, Name: "Nightly", CronExpr: "0 9 * * *", Enabled: true, AnalysisPolicySelections: chosen, CreatedAt: now, UpdatedAt: now}
	if err := upgraded.SaveScenarioSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	loadedSchedule, err := peer.GetScenarioSchedule("selected-schedule")
	if err != nil || !reflect.DeepEqual(loadedSchedule.AnalysisPolicySelections, chosen) {
		t.Fatal(loadedSchedule, err)
	}
	if _, err := store.db.Exec(`ALTER TABLE scenario_schedules DROP COLUMN analysis_policy_selections_json`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version>=85`); err != nil {
		t.Fatal(err)
	}
	reupgraded, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reupgraded.Close()
	loadedSchedule, err = reupgraded.GetScenarioSchedule("selected-schedule")
	if err != nil || loadedSchedule.AnalysisPolicySelections != nil {
		t.Fatal("legacy schedule upgrade invented selection", loadedSchedule, err)
	}
	loadedSchedule.AnalysisPolicySelections = chosen
	if err := reupgraded.SaveScenarioSchedule(loadedSchedule); err != nil {
		t.Fatal(err)
	}
	loadedSchedule, err = peer.GetScenarioSchedule("selected-schedule")
	if err != nil || !reflect.DeepEqual(loadedSchedule.AnalysisPolicySelections, chosen) {
		t.Fatal("peer lost upgraded schedule selection", loadedSchedule, err)
	}
}
