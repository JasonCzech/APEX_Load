package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scriptReloadRaceStore struct {
	listed []*dsl.Script
	inject func()
}

type scenarioReloadRaceStore struct {
	storage.Store
	inject func()
}

func (s *scenarioReloadRaceStore) ListScenarios() ([]*storage.Scenario, error) {
	if s.inject != nil {
		s.inject()
	}
	return []*storage.Scenario{{ID: "old", Name: "stale"}}, nil
}
func (s *scenarioReloadRaceStore) GetScenario(string) (*storage.Scenario, []storage.ScenarioScript, error) {
	return &storage.Scenario{ID: "old", Name: "stale"}, nil, nil
}
func (s *scenarioReloadRaceStore) ListScenarioDataPoolBindings(string) ([]storage.ScenarioDataPoolBinding, error) {
	return nil, nil
}
func (s *scenarioReloadRaceStore) ListScenarioSchedules(string) ([]*storage.ScenarioSchedule, error) {
	return nil, nil
}

func (s *scriptReloadRaceStore) SaveScript(*dsl.Script) error     { return nil }
func (s *scriptReloadRaceStore) DeleteScript(string) error        { return nil }
func (s *scriptReloadRaceStore) MoveScript(string, *string) error { return nil }
func (s *scriptReloadRaceStore) ListScripts(string, *string) ([]*dsl.Script, error) {
	if s.inject != nil {
		s.inject()
	}
	return s.listed, nil
}

// reloadRaceStore is a minimal testPersistence + historyPersistence stub. Its
// GetHistory hook fires inside reloadStoredTests' check-then-act window (after
// the RLock liveness read, before the final write Lock), letting the test
// deterministically simulate a run starting concurrently with the reload.
type reloadRaceStore struct {
	records []*storage.TestRecord
	inject  func()
}

func (s *reloadRaceStore) SaveTest(*storage.TestRecord) error          { return nil }
func (s *reloadRaceStore) GetTest(string) (*storage.TestRecord, error) { return nil, nil }
func (s *reloadRaceStore) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return s.records, nil
}
func (s *reloadRaceStore) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}
func (s *reloadRaceStore) UpdateTestStatus(string, string, *time.Time, string) error { return nil }
func (s *reloadRaceStore) DeleteTest(string) error                                   { return nil }
func (s *reloadRaceStore) MoveTest(string, *string) error                            { return nil }
func (s *reloadRaceStore) SaveHistory(string, time.Time, any) error                  { return nil }
func (s *reloadRaceStore) GetHistory(string) ([]any, error) {
	if s.inject != nil {
		s.inject()
	}
	return nil, nil
}

// TestReloadStoredTestsPreservesConcurrentlyStartedRun reproduces the HA failover
// race: reloadStoredTests must not overwrite a test that became live (cancel set)
// in the window between its liveness read and its write, or Stop/Abort would lose
// the cancel func and could never stop the run.
func TestReloadStoredTestsPreservesConcurrentlyStartedRun(t *testing.T) {
	const id = "t1"
	live := &ManagedTest{ID: id, Status: "RUNNING", cancel: func() {}}

	tr := &TestRunner{
		tests:    map[string]*ManagedTest{},
		archives: map[string]*storage.RunArchiveRecord{},
	}
	store := &reloadRaceStore{
		records: []*storage.TestRecord{{ID: id, Status: "COMPLETED"}},
		// Simulate startManagedTestRun landing in the check-then-act window.
		inject: func() {
			tr.mu.Lock()
			tr.tests[id] = live
			tr.mu.Unlock()
		},
	}
	tr.testStore = store
	tr.historyStore = store

	tr.reloadStoredTests()

	got := tr.tests[id]
	if got != live {
		t.Fatalf("reload clobbered the concurrently-started run (got %p, want live %p)", got, live)
	}
	if got.cancel == nil {
		t.Fatal("live run lost its cancel func; Stop/Abort could no longer stop it")
	}
}

func TestReloadStoredScriptsDoesNotOverwriteConcurrentMutation(t *testing.T) {
	tr := &TestRunner{scripts: map[string]*StoredScript{}, refKeyIndex: map[string]string{}}
	concurrent := &StoredScript{ID: "new", Name: "concurrent", Script: &dsl.Script{ID: "new", Name: "concurrent"}}
	store := &scriptReloadRaceStore{
		listed: []*dsl.Script{{ID: "old", Name: "stale"}},
		inject: func() {
			tr.mu.Lock()
			tr.scripts[concurrent.ID] = concurrent
			tr.contentRevision++
			tr.mu.Unlock()
		},
	}
	tr.scriptStore = store

	if err := tr.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}
	if tr.scripts[concurrent.ID] != concurrent || tr.scripts["old"] != nil {
		t.Fatalf("stale reload replaced concurrent mutation: %#v", tr.scripts)
	}
}

func TestReloadStoredScenariosDoesNotOverwriteConcurrentMutation(t *testing.T) {
	tr := &TestRunner{scripts: map[string]*StoredScript{}, scenarios: map[string]*StoredScenario{}, refKeyIndex: map[string]string{}}
	concurrent := &StoredScenario{ID: "new", Name: "concurrent"}
	store := &scenarioReloadRaceStore{inject: func() {
		tr.mu.Lock()
		tr.scenarios[concurrent.ID] = concurrent
		tr.contentRevision++
		tr.mu.Unlock()
	}}
	tr.scenarioStore = store

	if err := tr.reloadStoredScenarios(); err != nil {
		t.Fatal(err)
	}
	if tr.scenarios[concurrent.ID] != concurrent || tr.scenarios["old"] != nil {
		t.Fatalf("stale reload replaced concurrent scenario mutation: %#v", tr.scenarios)
	}
}
