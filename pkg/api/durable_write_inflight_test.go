package api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// gaugeObservingPersistence records the panic backstop's in-flight gauge from
// inside the storage driver, which is the only place the reading matters: the
// backstop consults it precisely while a write is blocked in there.
type gaugeObservingPersistence struct {
	runner *TestRunner

	saveTestGauge      atomic.Int64
	statusGauge        atomic.Int64
	narrowScaleGauge   atomic.Int64
	narrowScaleApplied atomic.Int32
}

func (s *gaugeObservingPersistence) observe(into *atomic.Int64) {
	into.Store(s.runner.durableWritesInFlight.Load())
}

func (s *gaugeObservingPersistence) SaveTest(*storage.TestRecord) error {
	s.observe(&s.saveTestGauge)
	return nil
}

func (s *gaugeObservingPersistence) GetTest(string) (*storage.TestRecord, error) { return nil, nil }

func (s *gaugeObservingPersistence) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *gaugeObservingPersistence) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *gaugeObservingPersistence) UpdateTestStatus(string, string, *time.Time, string) error {
	s.observe(&s.statusGauge)
	return nil
}

func (s *gaugeObservingPersistence) DeleteTest(string) error { return nil }

func (s *gaugeObservingPersistence) MoveTest(string, *string) error { return nil }

func (s *gaugeObservingPersistence) UpdateTestVUserScale(storage.TestVUserScaleUpdate) (bool, error) {
	s.narrowScaleApplied.Add(1)
	s.observe(&s.narrowScaleGauge)
	return true, nil
}

// TestEveryLockHeldDurableWriteIsPublishedToTheInFlightGauge covers the
// sibling-call-site hole the panic backstop shipped with: the gauge was
// incremented in persistStoredTestStatus only, so the equally lock-held record
// write (stopTest's pre-start abort, failClosedUncertainVUserScale) and the
// narrow VUser-scale write issued once per ramp step were invisible. A backstop
// waiting on tr.mu while one of those stalled read a zero gauge and re-panicked
// the controller, which is the crash-loop the extension exists to prevent.
func TestEveryLockHeldDurableWriteIsPublishedToTheInFlightGauge(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := &gaugeObservingPersistence{runner: runner}
	runner.testStore = store
	test := &ManagedTest{
		ID:          "gauge-run",
		Status:      "RUNNING",
		ProjectID:   storage.DefaultProjectID,
		TotalVUsers: 3,
		Groups: []TestGroup{{
			Name:   "buyers",
			VUsers: 3,
			script: &dsl.Script{ID: "script-1", Name: "checkout"},
		}},
	}
	runner.tests[test.ID] = test

	if err := runner.persistStoredTestStatus(test.ID, "", "FAILED", nil, "boom"); err != nil {
		t.Fatalf("persistStoredTestStatus: %v", err)
	}
	if got := store.statusGauge.Load(); got < 1 {
		t.Fatalf("in-flight gauge during the status write = %d, want at least 1", got)
	}

	if err := runner.saveManagedTestRecord(test); err != nil {
		t.Fatalf("saveManagedTestRecord: %v", err)
	}
	if got := store.saveTestGauge.Load(); got < 1 {
		t.Fatalf("in-flight gauge during the full-row record write = %d, want at least 1", got)
	}

	if err := runner.persistManagedTestVUserScale(test); err != nil {
		t.Fatalf("persistManagedTestVUserScale: %v", err)
	}
	if got := store.narrowScaleApplied.Load(); got != 1 {
		t.Fatalf("narrow VUser-scale writes = %d, want the narrow path to have been used", got)
	}
	if got := store.narrowScaleGauge.Load(); got < 1 {
		t.Fatalf("in-flight gauge during the narrow VUser-scale write = %d, want at least 1", got)
	}

	// The gauge must be a gauge, not a leak: nothing may still be counted once
	// the writes returned, or an orphaned mutex would extend forever.
	if got := runner.durableWritesInFlight.Load(); got != 0 {
		t.Fatalf("in-flight gauge after every write returned = %d, want 0", got)
	}
}

// slowRecordPersistence stalls the full-row record write - the durable write a
// pre-start abort makes while holding tr.mu - and leaves status writes instant,
// so a test can pin the runner lock on something other than a status write.
type slowRecordPersistence struct {
	*slowStatusPersistence

	release     chan struct{}
	releaseOnce sync.Once
	entered     chan struct{}
	saves       atomic.Int32
}

func newSlowRecordPersistence() *slowRecordPersistence {
	return &slowRecordPersistence{
		slowStatusPersistence: &slowStatusPersistence{},
		release:               make(chan struct{}),
		entered:               make(chan struct{}, 1),
	}
}

// releaseAll lets every stalled write complete. It is idempotent so a failing
// test can unblock the store from t.Cleanup without racing the happy path.
func (s *slowRecordPersistence) releaseAll() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func (s *slowRecordPersistence) SaveTest(*storage.TestRecord) error {
	s.saves.Add(1)
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return nil
}

// TestRunTestPanicBackstopWaitsOutASlowRecordWriteHoldingTheRunnerLock is the
// sibling of TestRunTestPanicBackstopWaitsOutASlowDurableWriteHoldingTheRunnerLock
// for the write that pins tr.mu without being a status write.
//
// stopTest holds tr.mu across saveManagedTestRecord when it aborts a run that
// never started, exactly as a terminal transition holds it across
// persistStoredTestStatus. During a managed-store failover that write stalls for
// as long, so a driver panic in that window must be tolerated the same way -
// otherwise the controller re-panics, every other run on the replica dies with
// it, and the restart lands on the same unreachable store.
func TestRunTestPanicBackstopWaitsOutASlowRecordWriteHoldingTheRunnerLock(t *testing.T) {
	previousTimeout := runTestPanicRecoveryLockTimeout
	runTestPanicRecoveryLockTimeout = 50 * time.Millisecond
	previousExtensions := runTestPanicRecoveryDurableWriteExtensions
	runTestPanicRecoveryDurableWriteExtensions = 64
	defer func() {
		runTestPanicRecoveryLockTimeout = previousTimeout
		runTestPanicRecoveryDurableWriteExtensions = previousExtensions
	}()

	store := newSlowRecordPersistence()
	t.Cleanup(store.releaseAll)
	stopped := make(chan error, 1)
	backend := &panickingExecutionBackend{
		panicValue: "driver panic during a database failover",
		before: func(tr *TestRunner, _ *ManagedTest) {
			// An unrelated, perfectly healthy run is aborted before it started. Its
			// durable record write stalls inside the storage driver while tr.mu is
			// held - the same shape as a stalled terminal status write, but through
			// saveManagedTestRecord.
			unrelated := &ManagedTest{ID: "unrelated-queued-run", Name: "unrelated-queued-run", Status: "QUEUED"}
			tr.mu.Lock()
			tr.tests[unrelated.ID] = unrelated
			tr.mu.Unlock()
			go func() { stopped <- tr.stopTest(unrelated.ID, false) }()
			select {
			case <-store.entered:
			case <-time.After(10 * time.Second):
				t.Error("the unrelated record write never reached the storage driver")
			}
		},
	}
	runner, test, _, cancel := newPanicBackstopRunner(t, backend)
	defer cancel()
	runner.testStore = store

	repanicked := make(chan any, 1)
	go func() {
		defer func() { repanicked <- recover() }()
		runner.runTest(test)
	}()

	// Hold the store open across many backstop expiries (50ms each), then let it
	// complete. A backstop blind to this write re-panics at the first expiry,
	// long before the release below.
	select {
	case recovered := <-repanicked:
		t.Fatalf("backstop re-panicked with %v while tr.mu was merely held by an unrelated run's slow-but-healthy record write; the store was progressing, not wedged", recovered)
	case <-time.After(500 * time.Millisecond):
	}
	store.releaseAll()

	select {
	case recovered := <-repanicked:
		if recovered != nil {
			t.Fatalf("backstop re-panicked with %v after the stalled record write completed", recovered)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runTest neither returned nor re-panicked after the stalled record write completed")
	}
	if err := <-stopped; err != nil {
		t.Fatalf("the unrelated pre-start abort failed: %v", err)
	}
	if got := store.saves.Load(); got < 1 {
		t.Fatalf("record writes = %d, want the stalled abort to have reached the storage driver", got)
	}
	runner.mu.RLock()
	status := test.Status
	runner.mu.RUnlock()
	if status != "FAILED" {
		t.Fatalf("status after waiting out the lock-holding slow record write = %s, want FAILED", status)
	}
}
