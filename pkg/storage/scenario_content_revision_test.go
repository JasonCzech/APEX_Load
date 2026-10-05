package storage

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func newScenarioRevisionTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scenario-revision.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
func requireScenarioAuthoring(t *testing.T, store *SQLiteStore, id string) *ScenarioAuthoringSnapshot {
	t.Helper()
	snapshot, err := store.GetScenarioAuthoringSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.Scenario == nil || !ValidScenarioContentRevision(snapshot.ContentRevision) || snapshot.ProjectID == "" || snapshot.LibraryAssignment == nil || snapshot.MonitorBindings == nil || snapshot.Scripts == nil || snapshot.DataPools == nil {
		t.Fatalf("incomplete snapshot %#v", snapshot)
	}
	return snapshot
}
func scenarioAuthoringExpected(snapshot *ScenarioAuthoringSnapshot) ScenarioAuthoringPrecondition {
	return ScenarioAuthoringPrecondition{ProjectID: snapshot.ProjectID, CreatedAt: snapshot.Scenario.CreatedAt, ContentRevision: snapshot.ContentRevision}
}
func rawScenarioRevision(t *testing.T, store *SQLiteStore, id string) string {
	t.Helper()
	var nonce string
	if err := store.db.QueryRow(`SELECT content_revision FROM scenarios WHERE id=?`, id).Scan(&nonce); err != nil {
		t.Fatal(err)
	}
	if !ValidScenarioContentRevision(nonce) {
		t.Fatal("invalid raw nonce")
	}
	return nonce
}
func seedScenarioRevision(t *testing.T, store *SQLiteStore) *ScenarioAuthoringSnapshot {
	t.Helper()
	for _, id := range []string{"scn-project", "scn-foreign"} {
		if err := store.SaveProject(&Project{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"scn-script", "scn-script-two"} {
		if err := store.SaveScript(&dsl.Script{ID: id, ProjectID: "scn-project", Name: id, Protocol: "http"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveDataPool(&SharedDataPool{ID: "scn-pool", Name: "Pool", ProjectID: "scn-project"}); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{ID: "scn-parent", RefKey: "LOC-SCN-0010-v1", Name: "Scenario", Duration: "1m", RuntimeSettings: map[string]any{"enabled": false}}
	zero := 0
	sla := 0.0
	groups := []ScenarioScript{{ScriptID: "scn-script", Name: "Group", VUsers: 1, Iterations: &zero, StartOffset: "0s", Stages: []LoadStage{}, TransactionResponseTimeSLAMs: &sla}}
	pools := []ScenarioDataPoolBinding{{ID: "scn-binding", PoolID: "scn-pool", Alias: "users", AccessMode: "read_only", SelectionStrategy: "sequential"}}
	result, err := store.CreateScenarioAuthoringIfAbsent(scenario, groups, pools)
	if err != nil || !result.Applied {
		t.Fatalf("create %#v %v", result, err)
	}
	if result.Snapshot.Scripts[0].Stages == nil || result.Snapshot.Scripts[0].Iterations == nil || *result.Snapshot.Scripts[0].Iterations != 0 || result.Snapshot.Scripts[0].TransactionResponseTimeSLAMs == nil || *result.Snapshot.Scripts[0].TransactionResponseTimeSLAMs != 0 {
		t.Fatal("explicit empty/zero authored overrides lost during clone/write/read")
	}
	return result.Snapshot
}
func TestScenarioContentRevisionGrammar(t *testing.T) {
	valid := "scn-v1-" + strings.Repeat("a0", 32)
	for _, tc := range []struct {
		value string
		valid bool
	}{{valid, true}, {"", false}, {"scn-v1-" + strings.Repeat("A0", 32), false}, {valid + "0", false}, {valid[:len(valid)-1], false}, {"scr-v1-" + strings.Repeat("a0", 32), false}, {" " + valid, false}, {valid[:len(valid)-1] + "g", false}} {
		if ValidScenarioContentRevision(tc.value) != tc.valid {
			t.Fatal("grammar admission")
		}
	}
}

func exerciseScenarioRevisionTriggers(t *testing.T, store *SQLiteStore) {
	original := seedScenarioRevision(t, store)
	fields := []struct {
		column string
		value  any
	}{{"ref_key", "LOC-SCN-0011-v1"}, {"name", "Changed"}, {"workload_shape_id", "shape"}, {"duration", "2m"}, {"start_delay", "1s"}, {"scenario_mode", "advanced"}, {"termination_mode", "iterations"}, {"shakeout_enabled", true}, {"resource_baseline_enabled", true}, {"assignment_json", `{"enabled":false}`}, {"goal_config_json", `{"values":[]}`}, {"runtime_settings_json", `{"enabled":true}`}, {"traffic_profile_json", `{"points":[]}`}, {"materialization_key", "new-incarnation"}, {"created_at", original.Scenario.CreatedAt.Add(time.Hour)}}
	for _, field := range fields {
		before := rawScenarioRevision(t, store, "scn-parent")
		if _, err := store.db.Exec(`UPDATE scenarios SET `+field.column+`=? WHERE id='scn-parent'`, field.value); err != nil {
			t.Fatal(err)
		}
		after := rawScenarioRevision(t, store, "scn-parent")
		if after == before {
			t.Fatalf("root %s did not rotate", field.column)
		}
		if _, err := store.db.Exec(`UPDATE scenarios SET `+field.column+`=? WHERE id='scn-parent'`, field.value); err != nil {
			t.Fatal(err)
		}
		if rawScenarioRevision(t, store, "scn-parent") != after {
			t.Fatalf("equivalent %s rotated", field.column)
		}
	}
	before := rawScenarioRevision(t, store, "scn-parent")
	if _, err := store.db.Exec(`UPDATE scenarios SET updated_at=? WHERE id='scn-parent'`, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if rawScenarioRevision(t, store, "scn-parent") != before {
		t.Fatal("operational timestamp rotated")
	}
	groups := []struct {
		column string
		value  any
	}{{"sort_order", 3}, {"name", "Different"}, {"script_id", "scn-script-two"}, {"vusers", 0}, {"ramp_up", "1s"}, {"ramp_down", "2s"}, {"start_offset", "3s"}, {"stages_json", `null`}, {"iterations", nil}, {"initialization", "before"}, {"role", "observer"}, {"runtime_settings_json", `{"enabled":false}`}, {"assignment_json", `{}`}, {"transaction_response_time_sla_ms", 1.0}}
	for _, field := range groups {
		before := rawScenarioRevision(t, store, "scn-parent")
		if _, err := store.db.Exec(`UPDATE scenario_scripts SET `+field.column+`=? WHERE scenario_id='scn-parent'`, field.value); err != nil {
			t.Fatal(err)
		}
		after := rawScenarioRevision(t, store, "scn-parent")
		if after == before {
			t.Fatalf("group %s did not rotate", field.column)
		}
		if _, err := store.db.Exec(`UPDATE scenario_scripts SET `+field.column+`=? WHERE scenario_id='scn-parent'`, field.value); err != nil {
			t.Fatal(err)
		}
		if rawScenarioRevision(t, store, "scn-parent") != after {
			t.Fatal("equivalent group changed")
		}
	}
	before = rawScenarioRevision(t, store, "scn-parent")
	if err := store.DeleteScenarioDataPoolBinding("scn-binding"); err != nil {
		t.Fatal(err)
	}
	if rawScenarioRevision(t, store, "scn-parent") == before {
		t.Fatal("direct pool deletion did not rotate")
	}
	if err := store.SaveScenarioDataPoolBindings("scn-parent", original.DataPools); err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct {
		column string
		value  any
	}{{"alias", "accounts"}, {"access_mode", "read_write"}, {"selection_strategy", "random"}} {
		before := rawScenarioRevision(t, store, "scn-parent")
		if _, err := store.db.Exec(`UPDATE scenario_data_pool_bindings SET `+field.column+`=? WHERE id='scn-binding'`, field.value); err != nil {
			t.Fatal(err)
		}
		if rawScenarioRevision(t, store, "scn-parent") == before {
			t.Fatalf("pool %s did not rotate", field.column)
		}
	}
	before = rawScenarioRevision(t, store, "scn-parent")
	if _, err := store.db.Exec(`UPDATE scenario_data_pool_bindings SET updated_at=? WHERE id='scn-binding'`, time.Now()); err != nil {
		t.Fatal(err)
	}
	if rawScenarioRevision(t, store, "scn-parent") != before {
		t.Fatal("binding context clock rotated")
	}
}
func TestScenarioContentRevisionImmediateRootGroupPoolTriggers(t *testing.T) {
	exerciseScenarioRevisionTriggers(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioAuthoringCAS(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	candidate := *old.Scenario
	candidate.Name = "New name"
	candidate.RuntimeSettings = map[string]any{"enabled": false, "nested": map[string]any{"a": []any{0.0}}}
	beforeInput, _ := json.Marshal(struct {
		S Scenario
		G []ScenarioScript
		P []ScenarioDataPoolBinding
	}{candidate, old.Scripts, old.DataPools})
	result, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old))
	if err != nil || !result.Applied {
		t.Fatalf("save %#v %v", result, err)
	}
	afterInput, _ := json.Marshal(struct {
		S Scenario
		G []ScenarioScript
		P []ScenarioDataPoolBinding
	}{candidate, old.Scripts, old.DataPools})
	if string(beforeInput) != string(afterInput) {
		t.Fatal("mutation altered caller input")
	}
	current := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if !reflect.DeepEqual(result.Snapshot, current) || current.ContentRevision == old.ContentRevision || current.Scripts[0].Iterations == nil || *current.Scripts[0].Iterations != 0 || current.Scripts[0].TransactionResponseTimeSLAMs == nil || *current.Scripts[0].TransactionResponseTimeSLAMs != 0 {
		t.Fatal("final snapshot mismatch or presence loss")
	}
	result.Snapshot.Scenario.RuntimeSettings.(map[string]any)["enabled"] = true
	result.Snapshot.Scripts[0].Name = "local mutation"
	if requireScenarioAuthoring(t, store, old.Scenario.ID).Scripts[0].Name == "local mutation" {
		t.Fatal("returned snapshot aliased storage")
	}
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old)); !errors.Is(err, ErrScenarioAuthoringConflict) {
		t.Fatalf("old editor accepted %v", err)
	}
	expected := scenarioAuthoringExpected(current)
	expected.ProjectID = "scn-foreign"
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, current.Scripts, current.DataPools, expected); !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatalf("foreign owner %v", err)
	}
	expected = scenarioAuthoringExpected(current)
	expected.CreatedAt = expected.CreatedAt.Add(time.Second)
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, current.Scripts, current.DataPools, expected); !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatalf("wrong incarnation %v", err)
	}
	expected = scenarioAuthoringExpected(current)
	expected.ContentRevision = "bad"
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, current.Scripts, current.DataPools, expected); !errors.Is(err, ErrScenarioAuthoringInvalidRevision) {
		t.Fatalf("invalid nonce %v", err)
	}
	// Same timestamp and explicit restoration of the old timestamp cannot restore authority.
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET vusers=2 WHERE scenario_id=?`, current.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE scenarios SET updated_at=? WHERE id=?`, current.Scenario.UpdatedAt, current.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, current.Scripts, current.DataPools, scenarioAuthoringExpected(current)); !errors.Is(err, ErrScenarioAuthoringConflict) {
		t.Fatalf("child only edit accepted %v", err)
	}
	latest := requireScenarioAuthoring(t, store, current.Scenario.ID)
	invalid := append([]ScenarioDataPoolBinding(nil), latest.DataPools...)
	invalid[0].PoolID = "missing"
	candidate.Name = "Rollback candidate"
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, latest.Scripts, invalid, scenarioAuthoringExpected(latest)); err == nil {
		t.Fatal("invalid child accepted")
	}
	if !reflect.DeepEqual(requireScenarioAuthoring(t, store, latest.Scenario.ID), latest) {
		t.Fatal("failed child write left partial root/nonce")
	}
	project, err := store.GetProject(latest.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	legacy := scenarioAuthoringExpected(latest)
	legacy.LegacyOrdinarySave = true
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, latest.Scripts, latest.DataPools, legacy); !errors.Is(err, ErrScenarioAuthoringPreconditionRequired) {
		t.Fatalf("locked strict policy ignored %v", err)
	}
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, latest.Scripts, latest.DataPools, scenarioAuthoringExpected(latest)); err != nil {
		t.Fatal(err)
	}
	// Legacy internal/import timestamp wrappers do not opt into ordinary PUT policy.
	scenario, groups, err := store.GetScenario(latest.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	scenario.Name = "Legacy internal"
	if applied, err := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario, groups, latest.DataPools, scenario.UpdatedAt); err != nil || !applied {
		t.Fatalf("legacy wrapper policy widened %v", err)
	}
}
func TestScenarioAuthoringOpaqueCASAndFinalReceipt(t *testing.T) {
	exerciseScenarioAuthoringCAS(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioAuthoringPlacementAndMonitors(t *testing.T, store *SQLiteStore) {
	seedLibraryAssignments(t, store)
	before := requireScenarioAuthoring(t, store, "la-scenario")
	app := "la-app"
	if _, _, err := store.ReplaceLibraryAssignment("scenario", "la-scenario", "la-project", &app, []string{"la-r2", "la-r1"}, 0); err != nil {
		t.Fatal(err)
	}
	placed := requireScenarioAuthoring(t, store, "la-scenario")
	if placed.ContentRevision == before.ContentRevision || placed.LibraryAssignment.Application.ID != app || placed.LibraryAssignment.Application.Name != "" || placed.LibraryAssignment.Releases[0].ID != "la-r1" {
		t.Fatal("placement IDs missing")
	}
	if _, _, err := store.ReplaceLibraryAssignment("scenario", "la-scenario", "la-project", &app, []string{"la-r1", "la-r2", "la-r1"}, 1); err != nil {
		t.Fatal(err)
	}
	same := requireScenarioAuthoring(t, store, "la-scenario")
	if same.ContentRevision != placed.ContentRevision {
		t.Fatal("equivalent placement churn")
	}
	if _, err := store.db.Exec(`UPDATE applications SET name='Context rename' WHERE id=?`, app); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE releases SET name='Other label' WHERE id='la-r1'`); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, "la-scenario").ContentRevision != same.ContentRevision {
		t.Fatal("joined labels rotated")
	}
	bindings := MonitorBindings{ScenarioID: "la-scenario", ProjectID: "la-project", Revision: 1, Sources: []MonitorBinding{{SourceID: "source-one", Required: false}}}
	body, _ := json.Marshal(bindings)
	if _, err := store.db.Exec(`INSERT INTO monitor_bindings(scenario_id,project_id,revision,payload) VALUES(?,?,?,?)`, bindings.ScenarioID, bindings.ProjectID, bindings.Revision, string(body)); err != nil {
		t.Fatal(err)
	}
	monitored := requireScenarioAuthoring(t, store, "la-scenario")
	if monitored.ContentRevision == same.ContentRevision || len(monitored.MonitorBindings.Sources) != 1 || monitored.MonitorBindings.Sources[0].Required {
		t.Fatal("monitor selection failed")
	}
	bindings.Revision = 2
	bindings.Sources[0].Required = true
	body, _ = json.Marshal(bindings)
	if _, err := store.db.Exec(`UPDATE monitor_bindings SET revision=?,payload=? WHERE scenario_id=?`, bindings.Revision, string(body), bindings.ScenarioID); err != nil {
		t.Fatal(err)
	}
	next := requireScenarioAuthoring(t, store, "la-scenario")
	if next.ContentRevision == monitored.ContentRevision || !next.MonitorBindings.Sources[0].Required {
		t.Fatal("required selection did not rotate")
	}
	if _, err := store.db.Exec(`UPDATE monitor_bindings SET project_id='la-foreign' WHERE scenario_id='la-scenario'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetScenarioAuthoringSnapshot("la-scenario"); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		t.Fatalf("monitor owner drift %v", err)
	}
}
func TestScenarioAuthoringPlacementAndMonitorCoherence(t *testing.T) {
	exerciseScenarioAuthoringPlacementAndMonitors(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioAuthoringReferenceAndSourceBoundary(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	byRef, err := store.GetScenarioAuthoringSnapshotByRefKey(old.Scenario.RefKey)
	if err != nil || !reflect.DeepEqual(byRef, old) {
		t.Fatal("reference resolution not paired")
	}
	if _, err := store.GetScenarioAuthoringSnapshotByRefKey("missing"); !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatal("missing ref accepted")
	}
	script, err := store.GetScript("scn-script")
	if err != nil {
		t.Fatal(err)
	}
	script.Name = "Renamed source"
	script.Config.BaseURL = "https://example.invalid"
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != old.ContentRevision {
		t.Fatal("ordinary source content rotated scenario")
	}
	script.ProjectID = "scn-foreign"
	if err := store.SaveScript(script); err == nil {
		t.Fatal("supported save moved referenced source tenant")
	}
	// A raw ownership repair must invalidate the derived project body, even though
	// ordinary source content is intentionally outside the scenario's authority.
	if _, err := store.db.Exec(`UPDATE scripts SET project_id='scn-foreign' WHERE id='scn-script'`); err != nil {
		t.Fatal(err)
	}
	if rawScenarioRevision(t, store, old.Scenario.ID) == old.ContentRevision {
		t.Fatal("derived owner changed invisibly")
	}
	if _, err := store.GetScenarioAuthoringSnapshot(old.Scenario.ID); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		t.Fatalf("raw foreign owner/pool corruption invented a qualified snapshot: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE scripts SET project_id='scn-project' WHERE id='scn-script'`); err != nil {
		t.Fatal(err)
	}
	// Supported writes cannot create a missing reference. A separate deliberately
	// corrupted legacy fixture below exercises fail-closed read admission.
	before := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET script_id='missing-script' WHERE scenario_id=?`, old.Scenario.ID); err == nil {
		t.Fatal("missing source violated durable FK invariant")
	}
	if !reflect.DeepEqual(before, requireScenarioAuthoring(t, store, old.Scenario.ID)) {
		t.Fatal("rejected missing source changed snapshot")
	}
}
func TestScenarioAuthoringReferenceAndInheritedSourceBoundary(t *testing.T) {
	exerciseScenarioAuthoringReferenceAndSourceBoundary(t, newScenarioRevisionTestStore(t))
}

func removeScenarioRevisionTrackingForTest(store *SQLiteStore) error {
	if store.db.dialect == storeDialectPostgres {
		for _, trigger := range postgresScenarioRevisionTriggers() {
			if _, err := store.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name + ` ON ` + trigger.table); err != nil {
				return err
			}
		}
		for _, function := range postgresScenarioRevisionFunctions() {
			if _, err := store.db.Exec(`DROP FUNCTION IF EXISTS ` + function.name + `() `); err != nil {
				return err
			}
		}
	} else {
		for _, trigger := range sqliteScenarioRevisionTriggers() {
			if _, err := store.db.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
				return err
			}
		}
	}
	return nil
}
func TestScenarioRevisionMigration38ReopenAndShapeRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	old := seedScenarioRevision(t, store)
	before, err := store.SchemaStatus()
	if err != nil {
		t.Fatal(err)
	}
	if err := removeScenarioRevisionTrackingForTest(store); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`ALTER TABLE scenarios DROP COLUMN content_revision`, `DELETE FROM schema_migrations WHERE version=38`} {
		if _, err := store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.SchemaStatus()
	if err != nil || after.AppliedVersion != 38 || !reflect.DeepEqual(before.Applied[:37], after.Applied[:37]) || after.Applied[36].Fingerprint != "d741b8384d281d84172d60b888a72228892173bbbc96d4f04a0e480cc1e9b745" {
		t.Fatal("released migrations changed")
	}
	backfilled := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if reflect.DeepEqual(backfilled.ContentRevision, old.ContentRevision) {
		t.Fatal("missing nonce not backfilled")
	}
	if !reflect.DeepEqual(backfilled.Scripts, old.Scripts) {
		t.Fatal("migration changed authored groups")
	}
	if _, err := store.db.Exec(`DROP TRIGGER apexload_scenario_group_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER apexload_scenario_group_update AFTER UPDATE ON scenario_scripts BEGIN SELECT 1; END`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != backfilled.ContentRevision {
		t.Fatal("reopen reminted valid nonce")
	}
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET vusers=2 WHERE scenario_id=?`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision == backfilled.ContentRevision {
		t.Fatal("wrong trigger not repaired")
	}
	if _, err := store.db.Exec(`UPDATE scenarios SET content_revision='invalid' WHERE id=?`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetScenarioAuthoringSnapshot(old.Scenario.ID); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		t.Fatalf("invalid persisted nonce %v", err)
	}
	store.Close()
	if opened, err := NewSQLiteStore(path); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		if opened != nil {
			opened.Close()
		}
		t.Fatalf("invalid nonce silently repaired %v", err)
	}
}
func exerciseScenarioAuthoringCoherentRead(t *testing.T, reader, writer *SQLiteStore) {
	old := seedScenarioRevision(t, writer)
	tx, err := reader.beginScriptAuthoringRead()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	first, err := readScenarioAuthoringSnapshotTx(tx, old.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := writer.UpdateScenarioAuthoringIfCurrent(&Scenario{ID: old.Scenario.ID, RefKey: "LOC-SCN-0011-v1", Name: "Second snapshot", Duration: "2m"}, []ScenarioScript{{ScriptID: "scn-script-two", VUsers: 7}}, nil, scenarioAuthoringExpected(old))
	if err != nil {
		t.Fatal(err)
	}
	paired, err := readScenarioAuthoringSnapshotTx(tx, old.Scenario.ID)
	if err != nil || !reflect.DeepEqual(first, paired) {
		t.Fatal("repeatable snapshot mixed components")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	actual := requireScenarioAuthoring(t, reader, old.Scenario.ID)
	if !reflect.DeepEqual(actual, current.Snapshot) {
		t.Fatal("current read remained replica-stale")
	}
	if _, err := reader.GetScenarioAuthoringSnapshotByRefKey(old.Scenario.RefKey); !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatal("old ref retained")
	}
}
func TestScenarioAuthoringTwoStoreCoherenceAndCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseScenarioAuthoringCoherentRead(t, first, second)
	var wg sync.WaitGroup
	outcomes := make(chan *ScenarioAuthoringMutationResult, 2)
	failures := make(chan error, 2)
	for _, store := range []*SQLiteStore{first, second} {
		wg.Add(1)
		go func(store *SQLiteStore) {
			defer wg.Done()
			result, err := store.CreateScenarioAuthoringIfAbsent(&Scenario{ID: "concurrent-create", Name: "Created", Duration: "1m"}, nil, nil)
			outcomes <- result
			failures <- err
		}(store)
	}
	wg.Wait()
	close(outcomes)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	applied := 0
	nonce := ""
	for outcome := range outcomes {
		if outcome.Applied {
			applied++
		}
		if nonce != "" && nonce != outcome.Snapshot.ContentRevision {
			t.Fatal("same ID different creation receipts")
		}
		nonce = outcome.Snapshot.ContentRevision
	}
	if applied != 1 {
		t.Fatal("creation not single winner")
	}
}
