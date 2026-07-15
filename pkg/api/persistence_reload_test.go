package api

import (
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type serialScriptReloadStore struct {
	mu            sync.Mutex
	calls         int
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
}

func (s *serialScriptReloadStore) SaveScript(*dsl.Script) error     { return nil }
func (s *serialScriptReloadStore) DeleteScript(string) error        { return nil }
func (s *serialScriptReloadStore) MoveScript(string, *string) error { return nil }
func (s *serialScriptReloadStore) ListScripts(string, *string) ([]*dsl.Script, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		close(s.firstStarted)
		<-s.releaseFirst
	} else if call == 2 {
		close(s.secondStarted)
	}
	return []*dsl.Script{{ID: "script", Name: "script"}}, nil
}

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

type serialTestReloadStore struct {
	reloadRaceStore
	mu            sync.Mutex
	calls         int
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
}

func (s *serialTestReloadStore) ListTests(string, *string) ([]*storage.TestRecord, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		close(s.firstStarted)
		<-s.releaseFirst
	} else if call == 2 {
		close(s.secondStarted)
	}
	return nil, nil
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

func TestReloadStoredTestsDoesNotDowngradeTerminalState(t *testing.T) {
	const id = "terminal"
	current := &ManagedTest{ID: id, Status: "COMPLETE"}
	record := &storage.TestRecord{ID: id, Status: "PENDING"}
	store := &reloadRaceStore{records: []*storage.TestRecord{record}}
	store.inject = nil
	tr := &TestRunner{tests: map[string]*ManagedTest{id: current}, archives: map[string]*storage.RunArchiveRecord{}, testStore: store, historyStore: store}
	if err := tr.reloadStoredTests(); err != nil {
		t.Fatal(err)
	}
	if tr.tests[id] != current || tr.tests[id].Status != "COMPLETE" {
		t.Fatalf("terminal test was downgraded: %+v", tr.tests[id])
	}
}

func TestConcurrentStoredTestReloadsAreSerialized(t *testing.T) {
	store := &serialTestReloadStore{
		firstStarted: make(chan struct{}), secondStarted: make(chan struct{}), releaseFirst: make(chan struct{}),
	}
	tr := &TestRunner{tests: map[string]*ManagedTest{}, archives: map[string]*storage.RunArchiveRecord{}, testStore: store}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- tr.reloadStoredTests() }()
	<-store.firstStarted
	go func() { secondDone <- tr.reloadStoredTests() }()
	select {
	case <-store.secondStarted:
		t.Fatal("second test reload entered storage before the first completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(store.releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second test reload did not proceed")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
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

func TestConcurrentScriptReloadsAreSerialized(t *testing.T) {
	store := &serialScriptReloadStore{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		releaseFirst:  make(chan struct{}),
	}
	runner := &TestRunner{scripts: map[string]*StoredScript{}, refKeyIndex: map[string]string{}, scriptStore: store}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- runner.reloadStoredScripts() }()
	<-store.firstStarted
	go func() { secondDone <- runner.reloadStoredScripts() }()
	select {
	case <-store.secondStarted:
		t.Fatal("second reload entered storage before the first completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(store.releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second reload did not proceed after the first completed")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
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
