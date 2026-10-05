package storage

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/dsl"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func exerciseScenarioRevisionReparent(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	if err := store.SaveScenario(&Scenario{ID: "scn-second", Name: "Second", Duration: "1m"}, []ScenarioScript{{ScriptID: "scn-script-two", Order: 0, VUsers: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET sort_order=1 WHERE scenario_id='scn-parent'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"scenario_scripts", "scenario_data_pool_bindings", "monitor_bindings"} {
		if table == "monitor_bindings" {
			body, _ := json.Marshal(&MonitorBindings{ScenarioID: old.Scenario.ID, ProjectID: old.ProjectID, Revision: 1, Sources: []MonitorBinding{{SourceID: "monitor", Required: false}}})
			if _, err := store.db.Exec(`INSERT INTO monitor_bindings(scenario_id,project_id,revision,payload) VALUES(?,?,?,?)`, old.Scenario.ID, old.ProjectID, 1, string(body)); err != nil {
				t.Fatal(err)
			}
		}
		beforeOld, beforeNew := rawScenarioRevision(t, store, "scn-parent"), rawScenarioRevision(t, store, "scn-second")
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`UPDATE ` + table + ` SET scenario_id='scn-second' WHERE scenario_id='scn-parent'`); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		for _, id := range []string{"scn-parent", "scn-second"} {
			var nonce string
			if err := tx.QueryRow(`SELECT content_revision FROM scenarios WHERE id=?`, id).Scan(&nonce); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			prior := beforeOld
			if id == "scn-second" {
				prior = beforeNew
			}
			if nonce == prior {
				tx.Rollback()
				t.Fatalf("%s reparent did not immediately invalidate %s", table, id)
			}
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if rawScenarioRevision(t, store, "scn-parent") != beforeOld || rawScenarioRevision(t, store, "scn-second") != beforeNew {
			t.Fatal("rollback leaked child or nonce")
		}
		if _, err := store.db.Exec(`UPDATE ` + table + ` SET scenario_id='scn-second' WHERE scenario_id='scn-parent'`); err != nil {
			t.Fatal(err)
		}
		if rawScenarioRevision(t, store, "scn-parent") == beforeOld || rawScenarioRevision(t, store, "scn-second") == beforeNew {
			t.Fatal("committed reparent missed parent")
		}
	}
}
func TestScenarioContentRevisionImmediateReparentAndRollback(t *testing.T) {
	exerciseScenarioRevisionReparent(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioAuthoringLifecycle(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	if changed, err := store.SetObjectArchived("scenario", old.Scenario.ID, old.Scenario.UpdatedAt, true); err != nil || !changed {
		t.Fatal(err)
	}
	archived := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if archived.ContentRevision != old.ContentRevision || archived.Scenario.ArchivedAt == nil {
		t.Fatal("archive changed authored nonce or lost context")
	}
	candidate := *old.Scenario
	candidate.Name = "Refused"
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old)); !errors.Is(err, ErrObjectArchived) {
		t.Fatalf("archived parent mutation %v", err)
	}
	if _, err := store.SetObjectArchived("scenario", old.Scenario.ID, archived.Scenario.UpdatedAt, false); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != old.ContentRevision {
		t.Fatal("restore rotated authored nonce")
	}
	source, err := store.GetScript("scn-script")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetObjectArchived("script", source.ID, source.UpdatedAt, true); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != old.ContentRevision {
		t.Fatal("source lifecycle rotated parent")
	}
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old)); !errors.Is(err, ErrObjectArchived) {
		t.Fatalf("archived source admitted %v", err)
	}
	source, err = store.GetScript(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetObjectArchived("script", source.ID, source.UpdatedAt, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE shared_data_pools SET name='Display rename',archived_at=? WHERE id='scn-pool'`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != old.ContentRevision {
		t.Fatal("pool lifecycle/display changed authored body")
	}
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old)); err == nil {
		t.Fatal("archived pool admitted")
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != old.ContentRevision {
		t.Fatal("refused lifecycle save changed nonce")
	}
	if _, err := store.db.Exec(`UPDATE shared_data_pools SET archived_at=NULL WHERE id='scn-pool'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, old.DataPools, scenarioAuthoringExpected(old)); err != nil {
		t.Fatal(err)
	}
}
func TestScenarioAuthoringActiveDependencyAdmissionAndContext(t *testing.T) {
	exerciseScenarioAuthoringLifecycle(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioAuthoringMonitorCorruption(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	good := &MonitorBindings{ScenarioID: old.Scenario.ID, ProjectID: old.ProjectID, Revision: 1, Sources: []MonitorBinding{{SourceID: "a", Required: false}, {SourceID: "z", Required: true}}}
	body, _ := json.Marshal(good)
	if _, err := store.db.Exec(`INSERT INTO monitor_bindings(scenario_id,project_id,revision,payload) VALUES(?,?,?,?)`, old.Scenario.ID, old.ProjectID, 1, string(body)); err != nil {
		t.Fatal(err)
	}
	baseline := requireScenarioAuthoring(t, store, old.Scenario.ID)
	for _, tc := range []struct {
		name     string
		body     string
		revision int64
		owner    string
	}{
		{"malformed", `{`, 1, old.ProjectID},
		{"null", `null`, 1, old.ProjectID},
		{"owner", `{"scenario_id":"scn-parent","project_id":"scn-foreign","revision":1,"sources":[]}`, 1, old.ProjectID},
		{"resource", `{"scenario_id":"other","project_id":"scn-project","revision":1,"sources":[]}`, 1, old.ProjectID},
		{"sql_revision", string(body), 2, old.ProjectID},
		{"duplicate_source", `{"scenario_id":"scn-parent","project_id":"scn-project","revision":1,"sources":[{"source_id":"a"},{"source_id":"a"}]}`, 1, old.ProjectID},
		{"blank_source", `{"scenario_id":"scn-parent","project_id":"scn-project","revision":1,"sources":[{"source_id":" "}]}`, 1, old.ProjectID},
	} {
		if _, err := store.db.Exec(`UPDATE monitor_bindings SET project_id=?,revision=?,payload=? WHERE scenario_id=?`, tc.owner, tc.revision, tc.body, old.Scenario.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetScenarioAuthoringSnapshot(old.Scenario.ID); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
			t.Fatalf("%s admitted corrupt monitor: %v", tc.name, err)
		}
		if _, err := store.db.Exec(`UPDATE monitor_bindings SET project_id=?,revision=1,payload=? WHERE scenario_id=?`, old.ProjectID, string(body), old.Scenario.ID); err != nil {
			t.Fatal(err)
		}
	}
	after := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if !reflect.DeepEqual(baseline.MonitorBindings, after.MonitorBindings) {
		t.Fatal("coherent monitor selection changed")
	}
}
func TestScenarioAuthoringMonitorCorruptionRefusesSnapshot(t *testing.T) {
	exerciseScenarioAuthoringMonitorCorruption(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioRevisionBlankIDRepair(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	// Build a historical blank source through the serializer, then deliberately
	// repair its persisted ID through the same startup helper production uses.
	source, err := store.GetScript("scn-script")
	if err != nil {
		t.Fatal(err)
	}
	source.ID = " "
	encoded, err := marshalScriptForStorage(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO scripts(id,name,protocol,version,project_id,config,data_sources,parameters,init,actions,teardown,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, source.ID, source.Name, source.Protocol, source.Version, source.ProjectID, encoded.config, encoded.dataSources, encoded.parameters, encoded.init, encoded.actions, encoded.teardown, source.CreatedAt, source.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET script_id=? WHERE scenario_id=?`, source.ID, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	before := rawScenarioRevision(t, store, old.Scenario.ID)
	if err := store.backfillBlankScriptIDs(); err != nil {
		t.Fatal(err)
	}
	snapshot := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if snapshot.ContentRevision == before || snapshot.Scripts[0].ScriptID == source.ID || snapshot.Scripts[0].ScriptID == "" {
		t.Fatal("blank reference repair invisible")
	}
	if err := store.backfillBlankScriptIDs(); err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != snapshot.ContentRevision {
		t.Fatal("idempotent repair changed nonce")
	}
}
func TestScenarioContentRevisionBlankScriptRepair(t *testing.T) {
	exerciseScenarioRevisionBlankIDRepair(t, newScenarioRevisionTestStore(t))
}

func TestScenarioAuthoringMissingSourceCorruptionFailsClosed(t *testing.T) {
	store := newScenarioRevisionTestStore(t)
	old := seedScenarioRevision(t, store)
	if _, err := store.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	defer store.db.Exec(`PRAGMA foreign_keys=ON`)
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET script_id='missing' WHERE scenario_id=?`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetScenarioAuthoringSnapshot(old.Scenario.ID); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		t.Fatalf("legacy corrupt source silently derived default owner: %v", err)
	}
}

func TestScenarioAuthoringCreateRollbackAndUnsupportedInput(t *testing.T) {
	store := newScenarioRevisionTestStore(t)
	seedScenarioRevision(t, store)
	candidate := &Scenario{ID: "rollback-create", Name: "Candidate", Duration: "1m", RuntimeSettings: map[string]any{"feature": false}}
	groups := []ScenarioScript{{ScriptID: "scn-script", Stages: []LoadStage{}}}
	saved := *candidate
	if result, err := store.CreateScenarioAuthoringIfAbsent(candidate, groups, []ScenarioDataPoolBinding{{PoolID: "missing", Alias: "x"}}); err == nil || result != nil {
		t.Fatal("invalid create published receipt")
	}
	if !reflect.DeepEqual(*candidate, saved) || candidate.CreatedAt.IsZero() == false || groups[0].Stages == nil {
		t.Fatal("failed create mutated caller")
	}
	if _, err := store.GetScenarioAuthoringSnapshot(candidate.ID); !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatal("create rollback leaked root")
	}
	if _, err := store.CreateScenarioAuthoringIfAbsent(nil, nil, nil); err == nil {
		t.Fatal("nil create admitted")
	}
	candidate.RuntimeSettings = make(chan int)
	if _, err := store.CreateScenarioAuthoringIfAbsent(candidate, groups, nil); err == nil {
		t.Fatal("non-json create admitted")
	}
	// Qualified read with no groups remains historical default ownership.
	if result, err := store.CreateScenarioAuthoringIfAbsent(&Scenario{ID: "empty-default", Name: "Empty", Duration: "1m"}, nil, nil); err != nil || result.Snapshot.ProjectID != DefaultProjectID {
		t.Fatal("empty default invariant")
	}
}

func TestScenarioAuthoringCreateCollisionPreservesLegacyReplay(t *testing.T) {
	store := newScenarioRevisionTestStore(t)
	old := seedScenarioRevision(t, store)
	foreign := &Scenario{ID: old.Scenario.ID, Name: "Foreign collision", Duration: "1m"}
	if err := store.SaveScript(&dsl.Script{ID: "foreign-source", ProjectID: "scn-foreign", Name: "Foreign", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	groups := []ScenarioScript{{ScriptID: "foreign-source", VUsers: 1}}
	if applied, err := store.CreateScenarioWithDataPoolBindingsIfAbsent(foreign, groups, nil); err != nil || applied {
		t.Fatalf("legacy create collision changed replay semantics: %v", err)
	}
	if result, err := store.CreateScenarioAuthoringIfAbsent(foreign, groups, nil); result != nil || !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatalf("foreign create published snapshot: %v", err)
	}
	if !reflect.DeepEqual(old, requireScenarioAuthoring(t, store, old.Scenario.ID)) {
		t.Fatal("collision rewrote existing scenario")
	}
}

func TestScenarioAuthoringDuplicateReferenceRefusesAuthority(t *testing.T) {
	store := newScenarioRevisionTestStore(t)
	old := seedScenarioRevision(t, store)
	if _, err := store.db.Exec(`DROP INDEX idx_scenarios_ref_key`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScenario(&Scenario{ID: "duplicate-ref", RefKey: old.Scenario.RefKey, Name: "Duplicate", Duration: "1m"}, nil); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := store.GetScenarioAuthoringSnapshotByRefKey(old.Scenario.RefKey); snapshot != nil || !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		t.Fatalf("ambiguous ref admitted: %v", err)
	}
}
func TestScenarioAuthoringCommitFailurePublishesNoReceipt(t *testing.T) {
	store := newScenarioRevisionTestStore(t)
	old := seedScenarioRevision(t, store)
	if _, err := store.db.Exec(`CREATE TABLE scenario_commit_failure(scenario_id TEXT REFERENCES scenarios(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER scenario_commit_failure AFTER UPDATE OF name ON scenarios WHEN NEW.name='Commit failure' BEGIN INSERT INTO scenario_commit_failure(scenario_id) VALUES('missing'); END`); err != nil {
		t.Fatal(err)
	}
	candidate := *old.Scenario
	candidate.Name = "Commit failure"
	result, err := store.UpdateScenarioAuthoringIfCurrent(&candidate, old.Scripts, nil, scenarioAuthoringExpected(old))
	if err == nil || result != nil {
		t.Fatal("failed final commit exposed snapshot authority")
	}
	if !reflect.DeepEqual(old, requireScenarioAuthoring(t, store, old.Scenario.ID)) {
		t.Fatal("failed final commit leaked root/groups/pool/nonce")
	}
}
func TestScenarioRevisionMissingOnlyBackfillReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-only.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	old := seedScenarioRevision(t, store)
	if _, err := store.db.Exec(`UPDATE scenarios SET content_revision=NULL WHERE id=?`, old.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetScenarioAuthoringSnapshot(old.Scenario.ID); !errors.Is(err, ErrScenarioAuthoringUnavailable) {
		t.Fatal("missing nonce granted authority before controlled initialization")
	}
	store.Close()
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	once := requireScenarioAuthoring(t, store, old.Scenario.ID)
	if once.ContentRevision == old.ContentRevision {
		t.Fatal("missing nonce not freshly minted")
	}
	store.Close()
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if requireScenarioAuthoring(t, store, old.Scenario.ID).ContentRevision != once.ContentRevision {
		t.Fatal("valid backfill repeated on reopen")
	}
}
