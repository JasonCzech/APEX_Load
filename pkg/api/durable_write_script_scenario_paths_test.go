package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// refSequenceGaugeSettings observes the panic backstop's in-flight gauge from
// inside the reference-key sequence allocation, and records whether tr.mu was
// write-held at that moment.
//
// This is the durable write every script and scenario create performs:
// storeScriptDurable takes tr.mu.Lock, then reaches
// nextScriptSeqLocked -> allocateRefSequenceLocked -> NextSettingSequence, a
// durable INSERT ... ON CONFLICT DO UPDATE ... RETURNING against the same
// database instance that backs the test store.
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

// TestOrdinaryScriptCreateLockHeldWritesArePublishedToTheInFlightGauge closes
// the third instance of the same defect.
//
// The gauge was extended twice - first to the lifecycle writes, then to the
// operator-triggered test-store writes - but it stayed scoped to tr.testStore,
// and the runner pins tr.mu across durable writes to its other stores too. The
// commonest one there is is an ordinary script save: POST /api/v1/scripts ->
// handleCreateScript -> StoreScriptDurable -> storeScriptDurable, which takes
// tr.mu.Lock and then allocates the script's reference key through a durable
// settings write. While that stalled in a managed-database failover the gauge
// read 0, so the panic backstop classified a perfectly healthy mutex as orphaned
// and re-panicked the controller, and the liveness probe fell out of its cached
// answer into a blocking read.
func TestOrdinaryScriptCreateLockHeldWritesArePublishedToTheInFlightGauge(t *testing.T) {
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
	if !settings.writeHeld.Load() {
		t.Fatal("the reference-sequence write did not run with tr.mu write-locked; this test no longer reproduces the shape it guards")
	}
	if got := settings.gauge.Load(); got < 1 {
		t.Errorf("in-flight gauge during an ordinary script create's reference-key write = %d, want at least 1; at 0 the panic backstop re-panics the controller and the liveness probe blocks until the kubelet restarts the replica", got)
	}

	// The gauge must be a gauge, not a leak.
	if got := runner.durableWritesInFlight.Load(); got != 0 {
		t.Fatalf("in-flight gauge after the script create returned = %d, want 0", got)
	}
}

// TestScenarioCreateLockHeldWritesArePublishedToTheInFlightGauge is the same
// check for the scenario side of the reference-key allocation, which
// StoreScenario reaches with tr.mu held through nextScenarioSeqLocked.
func TestScenarioCreateLockHeldWritesArePublishedToTheInFlightGauge(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	settings := &refSequenceGaugeSettings{runner: runner}
	runner.settingsStore = settings

	runner.mu.Lock()
	sequence, err := runner.nextScenarioSeqLocked()
	runner.mu.Unlock()
	if err != nil {
		t.Fatalf("nextScenarioSeqLocked: %v", err)
	}
	if sequence < 1 {
		t.Fatalf("allocated scenario reference sequence = %d, want a positive sequence", sequence)
	}
	if !settings.writeHeld.Load() {
		t.Fatal("the scenario reference-sequence write did not run with tr.mu write-locked")
	}
	if got := settings.gauge.Load(); got < 1 {
		t.Errorf("in-flight gauge during a scenario reference-key write = %d, want at least 1", got)
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

// TestHealthProbeAnswersWhileAnOrdinaryScriptSaveHoldsTheRunnerLock is the
// end-to-end liveness half.
//
// /api/v1/system/health is the shipped livenessProbe (periodSeconds 20,
// timeoutSeconds 3, failureThreshold 3), and both runner reads in its payload
// take tr.mu. A script save that stalls in the storage driver holds tr.mu for
// the length of a database failover, so a probe that queues behind it has the
// container killed roughly 43s in - restarting the replica into the same
// unreachable store, which is the crash-loop the in-flight gauge exists to
// prevent.
func TestHealthProbeAnswersWhileAnOrdinaryScriptSaveHoldsTheRunnerLock(t *testing.T) {
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
			t.Fatalf("liveness probe status while an ordinary script save held the runner lock = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("liveness probe blocked past its 3s timeout while a healthy-but-stalled script save held the runner lock; kubernetes would restart the replica mid-failover")
	}

	settings.releaseAll()
	if err := <-saved; err != nil {
		t.Fatalf("the stalled script save failed: %v", err)
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
func TestRunTestPanicBackstopWaitsOutAnOrdinaryScriptSaveHoldingTheRunnerLock(t *testing.T) {
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

	// Hold the settings write open across many backstop expiries (50ms each). A
	// backstop blind to a non-test-store write re-panics at the first one.
	select {
	case recovered := <-repanicked:
		t.Fatalf("backstop re-panicked with %v while tr.mu was merely held by an ordinary script save stalled in the storage driver; the store was progressing, not wedged", recovered)
	case <-time.After(500 * time.Millisecond):
	}
	settings.releaseAll()

	select {
	case recovered := <-repanicked:
		if recovered != nil {
			t.Fatalf("backstop re-panicked with %v after the stalled script save completed", recovered)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runTest neither returned nor re-panicked after the stalled script save completed")
	}
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
