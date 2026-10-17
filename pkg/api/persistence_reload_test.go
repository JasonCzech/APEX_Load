package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type serialScriptReloadStore struct {
	mu            sync.Mutex
	calls         int
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
}

type countingScriptReloadStore struct {
	mu    sync.Mutex
	calls int
}

func (s *countingScriptReloadStore) SaveScript(*dsl.Script) error     { return nil }
func (s *countingScriptReloadStore) DeleteScript(string) error        { return nil }
func (s *countingScriptReloadStore) MoveScript(string, *string) error { return nil }
func (s *countingScriptReloadStore) ListScripts(string, *string) ([]*dsl.Script, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return []*dsl.Script{{ID: "script", Name: "script"}}, nil
}

func (s *countingScriptReloadStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
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
	Store  storage.Store
	inject func()
}

type schedulerVersionStore struct {
	storage.Store
	mu            sync.Mutex
	version       string
	scriptLists   int
	scenarioLists int
}

type churningSchedulerVersionStore struct {
	schedulerVersionStore
	versionCalls int
}

type locallyFencedSchedulerVersionStore struct {
	schedulerVersionStore
	injectOnce sync.Once
	inject     func()
}

func (s *churningSchedulerVersionStore) SchedulerContentVersion() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versionCalls++
	return string(rune('a' + s.versionCalls)), nil
}

func (s *schedulerVersionStore) SchedulerContentVersion() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version, nil
}

func (s *schedulerVersionStore) ListScripts(string, *string) ([]*dsl.Script, error) {
	s.mu.Lock()
	s.scriptLists++
	s.mu.Unlock()
	return nil, nil
}

func (s *locallyFencedSchedulerVersionStore) ListScripts(string, *string) ([]*dsl.Script, error) {
	s.mu.Lock()
	s.scriptLists++
	s.mu.Unlock()
	s.injectOnce.Do(func() {
		if s.inject != nil {
			s.inject()
		}
	})
	return []*dsl.Script{{ID: "external", Name: "External", Protocol: "http"}}, nil
}

func (s *schedulerVersionStore) ListScenarios() ([]*storage.Scenario, error) {
	s.mu.Lock()
	s.scenarioLists++
	s.mu.Unlock()
	return nil, nil
}

func (s *schedulerVersionStore) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scriptLists, s.scenarioLists
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
	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	live := &ManagedTest{ID: id, Status: "RUNNING", executionCtx: liveCtx, cancel: liveCancel}

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

func TestReloadStoredTestsPreservesSameTermStoppingProofRuntime(t *testing.T) {
	for _, tc := range []struct {
		name           string
		remoteHandles  bool
		registeredStop bool
		priorTerm      bool
		wantPreserved  bool
	}{
		{name: "remote stop in flight", remoteHandles: true, wantPreserved: true},
		{name: "registered stop retry", registeredStop: true, wantPreserved: true},
		{name: "prior term must reload", remoteHandles: true, registeredStop: true, priorTerm: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store, leadership := newAdoptionTestRunner(t)
			created := time.Now().UTC().Truncate(time.Microsecond)
			record := &storage.TestRecord{ID: "stopping-reload", Name: "stopping-reload", Status: "STOPPING", Error: "older snapshot", StartTime: created}
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			record, err := store.GetTest(record.ID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			owner, _ := leadership.executionLeaseTerm()
			if tc.priorTerm {
				owner = "retired-controller-term"
			}
			test := &ManagedTest{ID: record.ID, Name: record.Name, Status: "STOPPING", Error: "local stop proof pending",
				CreatedAt: record.StartTime, ProjectID: record.ProjectID, executionCtx: ctx, cancel: cancel,
				executionFence: owner, executionBackend: runner.execution}
			test.remoteRunSupervisionToken[0] = 7
			if tc.remoteHandles {
				test.remoteClientsByTarget = map[string]generatorClient{"gen-a:9091": &adoptionFakeClient{}}
				test.remoteGroupTargets = map[string]string{"ga": "gen-a:9091"}
			}
			runner.tests[test.ID] = test
			if tc.registeredStop {
				runner.terminalRetryMu.Lock()
				runner.stopTeardownRetries[test.ID] = stopTeardownRetryIntent{force: true, version: 1}
				runner.terminalRetryMu.Unlock()
			}
			if err := runner.reloadStoredTests(); err != nil {
				t.Fatal(err)
			}
			got := runner.tests[test.ID]
			if (got == test) != tc.wantPreserved {
				t.Fatalf("stopping runtime pointer preserved=%v, want %v", got == test, tc.wantPreserved)
			}
			if tc.wantPreserved && (got.Status != "STOPPING" || got.Error != "local stop proof pending" || got.remoteRunSupervisionToken[0] != 7 ||
				(tc.remoteHandles && (got.remoteGroupTargets["ga"] != "gen-a:9091" || len(got.remoteClientsByTarget) != 1))) {
				t.Fatalf("stopping runtime lost generator stop identity: status=%s error=%q token=%d targets=%v clients=%d", got.Status, got.Error, got.remoteRunSupervisionToken[0], got.remoteGroupTargets, len(got.remoteClientsByTarget))
			}
			if tc.wantPreserved {
				durable, err := store.GetTest(test.ID)
				if err != nil || durable.Error != "older snapshot" {
					t.Fatalf("same-term refresh overwrote active stop-proof reason: %+v, %v", durable, err)
				}
			}
		})
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

func TestReloadStoredTestsCompactsReusedReplicaOnTerminalTransition(t *testing.T) {
	const id = "terminal-transition"
	createdAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	current := &ManagedTest{
		ID:             id,
		ProjectID:      "project-a",
		CreatedAt:      createdAt,
		Status:         "RUNNING",
		History:        []*LiveMetrics{{Timestamp: createdAt, TPS: 1}, {Timestamp: createdAt.Add(30 * time.Second), TPS: 2}, {Timestamp: endedAt, TPS: 3}},
		LiveMetrics:    &LiveMetrics{Timestamp: endedAt, TPS: 3},
		Groups:         []TestGroup{{script: &dsl.Script{}, runtimeSecretValues: map[string]string{"token": "secret"}}},
		groupSpecs:     []engine.GroupConfig{{Name: "group"}},
		multiEngine:    &engine.MultiGroupEngine{},
		goalControl:    &goalControllerState{},
		historyBytes:   3,
		historyWeights: []int64{1, 1, 1},
	}
	record := &storage.TestRecord{ID: id, ProjectID: "project-a", Status: "COMPLETE", StartTime: createdAt, EndTime: &endedAt}
	store := &reloadRaceStore{records: []*storage.TestRecord{record}}
	runner := &TestRunner{
		tests:              map[string]*ManagedTest{id: current},
		archives:           map[string]*storage.RunArchiveRecord{},
		testStore:          store,
		historyStore:       store,
		storedHistoryCache: newStoredTestHistoryCache(),
	}

	if err := runner.reloadStoredTests(); err != nil {
		t.Fatal(err)
	}
	got := runner.tests[id]
	if got != current || got.Status != "COMPLETE" {
		t.Fatalf("reloaded test = %#v, want reused terminal identity", got)
	}
	if len(got.History) != 1 || len(got.Sparkline) != 3 || got.Sparkline[0].TPS != 1 {
		t.Fatalf("terminal evidence = history:%#v sparkline:%#v", got.History, got.Sparkline)
	}
	if got.multiEngine != nil || got.goalControl != nil || got.groupSpecs != nil || got.Groups[0].script != nil || got.Groups[0].runtimeSecretValues != nil {
		t.Fatalf("terminal replica retained runtime state: %#v", got)
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

func TestHAReadScriptRefreshCoalescesRequestBurst(t *testing.T) {
	store := &countingScriptReloadStore{}
	runner := &TestRunner{
		scripts:     map[string]*StoredScript{},
		refKeyIndex: map[string]string{},
		scriptStore: store,
		leadership:  &executionLeadership{},
	}
	const callers = 20
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			runner.refreshScriptsForHARead()
		}()
	}
	group.Wait()
	if got := store.callCount(); got != 1 {
		t.Fatalf("ListScripts calls = %d, want one coalesced refresh", got)
	}

	runner.refreshScriptsForHARead(true)
	if got := store.callCount(); got != 2 {
		t.Fatalf("ListScripts calls after forced refresh = %d, want 2", got)
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
	tr.scenarioStore = storeOverlay(store)

	if err := tr.reloadStoredScenarios(); err != nil {
		t.Fatal(err)
	}
	if tr.scenarios[concurrent.ID] != concurrent || tr.scenarios["old"] != nil {
		t.Fatalf("stale reload replaced concurrent scenario mutation: %#v", tr.scenarios)
	}
}

func TestSchedulerContentRefreshSkipsUnchangedCatalog(t *testing.T) {
	store := &schedulerVersionStore{version: "v1"}
	runner := &TestRunner{
		scripts:            map[string]*StoredScript{},
		scenarios:          map[string]*StoredScenario{},
		refKeyIndex:        map[string]string{},
		privateCISnapshots: map[string]int{},
		scriptStore:        store,
		scenarioStore:      store,
	}
	if err := runner.refreshSchedulerContentForHASweep(); err != nil {
		t.Fatal(err)
	}
	if err := runner.refreshSchedulerContentForHASweep(); err != nil {
		t.Fatal(err)
	}
	if scripts, scenarios := store.counts(); scripts != 1 || scenarios != 1 {
		t.Fatalf("unchanged refresh lists = scripts:%d scenarios:%d, want 1 each", scripts, scenarios)
	}
	store.mu.Lock()
	store.version = "v2"
	store.mu.Unlock()
	if err := runner.refreshSchedulerContentForHASweep(); err != nil {
		t.Fatal(err)
	}
	if scripts, scenarios := store.counts(); scripts != 2 || scenarios != 2 {
		t.Fatalf("changed refresh lists = scripts:%d scenarios:%d, want 2 each", scripts, scenarios)
	}
}

func TestSchedulerContentRefreshRejectsContinuouslyChangingCatalog(t *testing.T) {
	store := &churningSchedulerVersionStore{}
	runner := &TestRunner{
		scripts:            map[string]*StoredScript{},
		scenarios:          map[string]*StoredScenario{},
		refKeyIndex:        map[string]string{},
		privateCISnapshots: map[string]int{},
		scriptStore:        store,
		scenarioStore:      store,
	}
	if err := runner.refreshSchedulerContentForHASweep(); err == nil {
		t.Fatal("continuously changing scheduler content was accepted as a coherent sweep snapshot")
	}
	if runner.schedulerContentKnown {
		t.Fatal("continuously changing scheduler content was marked authoritative")
	}
	if scripts, scenarios := store.counts(); scripts != 2 || scenarios != 2 {
		t.Fatalf("changing refresh attempts = scripts:%d scenarios:%d, want two bounded attempts", scripts, scenarios)
	}
}

func TestSchedulerContentRefreshDoesNotCacheTokenAfterLocallyFencedReload(t *testing.T) {
	store := &locallyFencedSchedulerVersionStore{}
	store.version = "v2"
	runner := &TestRunner{
		scripts:            map[string]*StoredScript{},
		scenarios:          map[string]*StoredScenario{},
		refKeyIndex:        map[string]string{},
		privateCISnapshots: map[string]int{},
		scriptStore:        store,
		scenarioStore:      store,
	}
	store.inject = func() {
		// A private CI snapshot add/remove is process-local and intentionally does
		// not advance the database trigger token. It still fences the in-flight
		// script snapshot, so the scheduler must retry before caching v2.
		runner.mu.Lock()
		runner.contentRevision++
		runner.mu.Unlock()
	}

	if err := runner.refreshSchedulerContentForHASweep(); err != nil {
		t.Fatal(err)
	}
	if !runner.schedulerContentKnown || runner.schedulerContentToken != "v2" {
		t.Fatalf("scheduler content cache = known:%v token:%q, want authoritative v2", runner.schedulerContentKnown, runner.schedulerContentToken)
	}
	if scripts, scenarios := store.counts(); scripts != 2 || scenarios != 2 {
		t.Fatalf("locally fenced refresh lists = scripts:%d scenarios:%d, want a complete retry", scripts, scenarios)
	}
	if got := runner.scripts["external"]; got == nil || got.Name != "External" {
		t.Fatalf("external database update was not installed before token caching: %#v", runner.scripts)
	}
}
