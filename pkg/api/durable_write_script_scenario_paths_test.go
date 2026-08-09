package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// refSequenceGaugeSettings observes the panic backstop's in-flight gauge from
// inside the reference-key sequence allocation, and records whether tr.mu was
// write-held at that moment.
//
// Every script and scenario create performs this durable allocation. The
// dedicated allocator must keep its INSERT ... ON CONFLICT DO UPDATE ...
// RETURNING round trip outside the live runner mutex.
type refSequenceGaugeSettings struct {
	runner *TestRunner

	sequence    atomic.Int64
	calls       atomic.Int32
	gauge       atomic.Int64
	writeHeld   atomic.Bool
	saveGauge   atomic.Int64
	saveCalls   atomic.Int32
	saveHeldSet atomic.Bool
}

func (s *refSequenceGaugeSettings) GetSetting(string) (string, error) { return "", nil }

func (s *refSequenceGaugeSettings) SaveSetting(string, string) error {
	s.saveCalls.Add(1)
	s.saveGauge.Store(s.runner.durableWritesInFlight.Load())
	s.saveHeldSet.Store(true)
	return nil
}

func (s *refSequenceGaugeSettings) NextSettingSequence(string) (int, error) {
	s.calls.Add(1)
	s.gauge.Store(s.runner.durableWritesInFlight.Load())
	// TryRLock fails only while a writer holds the mutex, so a false here proves
	// this durable write is running with tr.mu write-locked - the state the
	// backstop must not read as an orphaned lock.
	if s.runner.mu.TryRLock() {
		s.runner.mu.RUnlock()
	} else {
		s.writeHeld.Store(true)
	}
	return int(s.sequence.Add(1)), nil
}

// TestOrdinaryScriptCreateReferenceSequenceRunsOutsideRunnerLock prevents the
// settings-backed reference allocator from reintroducing controller-wide head-
// of-line blocking on ordinary authoring saves.
func TestOrdinaryScriptCreateReferenceSequenceRunsOutsideRunnerLock(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	settings := &refSequenceGaugeSettings{runner: runner}
	runner.settingsStore = settings

	if _, err := runner.StoreScriptDurable(&dsl.Script{
		ID:        "gauge-script",
		ProjectID: "project-gauge",
		Name:      "Checkout",
		Version:   "1.0.0",
		Protocol:  "http",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:   []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	}); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}

	if got := settings.calls.Load(); got != 1 {
		t.Fatalf("reference-sequence allocations during a script create = %d, want 1", got)
	}
	if settings.writeHeld.Load() {
		t.Fatal("the reference-sequence write ran with tr.mu write-locked")
	}
	if got := settings.gauge.Load(); got != 0 {
		t.Errorf("lock-held durable-write gauge during off-lock reference allocation = %d, want 0", got)
	}

	// The gauge must be a gauge, not a leak.
	if got := runner.durableWritesInFlight.Load(); got != 0 {
		t.Fatalf("in-flight gauge after the script create returned = %d, want 0", got)
	}
}

func TestScenarioCreateReferenceSequenceRunsOutsideRunnerLock(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	settings := &refSequenceGaugeSettings{runner: runner}
	runner.settingsStore = settings

	sequence, err := runner.nextScenarioSeqOutsideRunnerLock()
	if err != nil {
		t.Fatalf("nextScenarioSeqLocked: %v", err)
	}
	if sequence < 1 {
		t.Fatalf("allocated scenario reference sequence = %d, want a positive sequence", sequence)
	}
	if settings.writeHeld.Load() {
		t.Fatal("the scenario reference-sequence write ran with tr.mu write-locked")
	}
	if got := settings.gauge.Load(); got != 0 {
		t.Errorf("lock-held durable-write gauge during off-lock scenario reference allocation = %d, want 0", got)
	}
	if got := runner.durableWritesInFlight.Load(); got != 0 {
		t.Fatalf("in-flight gauge after the scenario reference-key write returned = %d, want 0", got)
	}
}

// stallingRefSequenceSettings stalls inside the reference-key sequence
// allocation and leaves every other settings operation instant, so a test can
// pin tr.mu on an ordinary script save rather than on a run's lifecycle write.
type stallingRefSequenceSettings struct {
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	calls       atomic.Int32
}

func newStallingRefSequenceSettings() *stallingRefSequenceSettings {
	return &stallingRefSequenceSettings{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (s *stallingRefSequenceSettings) releaseAll() { s.releaseOnce.Do(func() { close(s.release) }) }

func (s *stallingRefSequenceSettings) GetSetting(string) (string, error) { return "", nil }

func (s *stallingRefSequenceSettings) SaveSetting(string, string) error { return nil }

func (s *stallingRefSequenceSettings) NextSettingSequence(string) (int, error) {
	s.calls.Add(1)
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return int(s.calls.Load()), nil
}

func TestHealthProbeAnswersWhileAnOrdinaryScriptSaveStallsOffLock(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := newStallingRefSequenceSettings()
	t.Cleanup(settings.releaseAll)
	server.runner.settingsStore = settings

	if warm := probeHealth(t, server); warm.Code != http.StatusOK {
		t.Fatalf("warm-up probe status = %d, want %d body=%s", warm.Code, http.StatusOK, warm.Body.String())
	}

	saved := make(chan error, 1)
	go func() {
		_, err := server.runner.StoreScriptDurable(&dsl.Script{
			ID:        "stalled-script",
			ProjectID: "project-gauge",
			Name:      "Checkout",
			Version:   "1.0.0",
			Protocol:  "http",
			Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions:   []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
		})
		saved <- err
	}()
	select {
	case <-settings.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the script save never reached the storage driver")
	}
	if !server.runner.mu.TryRLock() {
		t.Fatal("ordinary runner reads are blocked behind the stalled script save")
	}
	server.runner.mu.RUnlock()

	probed := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		probed <- resp.Code
	}()
	select {
	case code := <-probed:
		if code != http.StatusOK {
			t.Fatalf("liveness probe status while an ordinary script save stalled = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("liveness probe blocked past its 3s timeout while an authoring save stalled off-lock")
	}

	settings.releaseAll()
	if err := <-saved; err != nil {
		t.Fatalf("the stalled script save failed: %v", err)
	}
}

type stallingScenarioSaveStore struct {
	storage.Store
	entered chan struct{}
	release chan struct{}
}

func (s *stallingScenarioSaveStore) SaveScenarioWithDataPoolBindings(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return s.Store.(atomicScenarioPersistence).SaveScenarioWithDataPoolBindings(scenario, scripts, bindings)
}

func TestScenarioPersistenceRunsOutsideRunnerLock(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "scenario-off-lock.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	now := time.Now().UTC()
	if err := base.SaveProject(&storage.Project{ID: "project-scenario", Name: "Scenario", CreatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := base.SaveScript(&dsl.Script{
		ID: "scenario-script", ProjectID: "project-scenario", Name: "Script", Protocol: "http", CreatedAt: now,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
	}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	store := &stallingScenarioSaveStore{Store: base, entered: make(chan struct{}, 1), release: make(chan struct{})}
	runner := NewTestRunner(1, NewHub(), store)

	stored := make(chan error, 1)
	go func() {
		_, storeErr := runner.StoreScenario(CreateScenarioRequest{
			Name: "Scenario", Duration: "1m",
			Groups: []CreateGroupEntry{{ScriptID: "scenario-script", VUsers: 1}},
		})
		stored <- storeErr
	}()
	select {
	case <-store.entered:
	case <-time.After(10 * time.Second):
		close(store.release)
		t.Fatalf("scenario save did not reach persistence: %v", <-stored)
	}
	if !runner.mu.TryRLock() {
		close(store.release)
		t.Fatal("ordinary runner reads are blocked behind scenario persistence")
	}
	runner.mu.RUnlock()
	close(store.release)
	if err := <-stored; err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
}

// TestRunTestPanicBackstopWaitsOutAnOrdinaryScriptSaveHoldingTheRunnerLock is
// the backstop half, and the sibling of
// TestRunTestPanicBackstopWaitsOutASlowRecordWriteHoldingTheRunnerLock for a
// lock holder that is not a test-store write at all.
//
// An operator saves a script while the database fails over. That save holds
// tr.mu across its durable reference-key write. If an unrelated run driver
// panics in that window, a backstop that cannot see the write reads a zero gauge
// at the first expiry, calls the mutex orphaned, and re-panics - killing every
// other run on the replica and restarting into the same unreachable store.
func TestRunTestPanicBackstopDoesNotWaitForOffLockScriptSave(t *testing.T) {
	previousTimeout := runTestPanicRecoveryLockTimeout
	runTestPanicRecoveryLockTimeout = 50 * time.Millisecond
	previousExtensions := runTestPanicRecoveryDurableWriteExtensions
	runTestPanicRecoveryDurableWriteExtensions = 64
	defer func() {
		runTestPanicRecoveryLockTimeout = previousTimeout
		runTestPanicRecoveryDurableWriteExtensions = previousExtensions
	}()

	settings := newStallingRefSequenceSettings()
	t.Cleanup(settings.releaseAll)
	saved := make(chan error, 1)
	backend := &panickingExecutionBackend{
		panicValue: "driver panic during a database failover",
		before: func(tr *TestRunner, _ *ManagedTest) {
			tr.settingsStore = settings
			go func() {
				_, err := tr.StoreScriptDurable(&dsl.Script{
					ID:        "stalled-script",
					ProjectID: "project-gauge",
					Name:      "Checkout",
					Version:   "1.0.0",
					Protocol:  "http",
					Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
					Actions:   []dsl.Action{{Name: "health", Type: "request", Target: "/health"}},
				})
				saved <- err
			}()
			select {
			case <-settings.entered:
			case <-time.After(10 * time.Second):
				t.Error("the script save never reached the storage driver")
			}
		},
	}
	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()

	repanicked := make(chan any, 1)
	go func() {
		defer func() { repanicked <- recover() }()
		runner.runTest(test)
	}()

	select {
	case recovered := <-repanicked:
		if recovered != nil {
			t.Fatalf("backstop re-panicked with %v while an unrelated script save stalled off-lock", recovered)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runTest waited for an unrelated off-lock script save")
	}
	settings.releaseAll()
	if err := <-saved; err != nil {
		t.Fatalf("the stalled script save failed: %v", err)
	}
	runner.mu.RLock()
	status := test.Status
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status after waiting out the lock-holding script save = %s, want FAILED", status)
	}
}
