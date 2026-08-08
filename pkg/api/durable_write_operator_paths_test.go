package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// operatorGaugePersistence observes the panic backstop's in-flight gauge from
// inside the store calls the operator-triggered lifecycle paths make. Each of
// them is issued with tr.mu write-locked, so the reading taken here is exactly
// what the backstop and the liveness probe see while such a write is stalled.
type operatorGaugePersistence struct {
	*gaugeObservingPersistence

	pinGauge    atomic.Int64
	unpinGauge  atomic.Int64
	moveGauge   atomic.Int64
	deleteGauge atomic.Int64

	pinCalls    atomic.Int32
	unpinCalls  atomic.Int32
	moveCalls   atomic.Int32
	deleteCalls atomic.Int32
}

func (s *operatorGaugePersistence) SetTestComparisonBaseline(id string, pinnedAt *time.Time) ([]storage.TestBaselineState, error) {
	if pinnedAt != nil {
		s.pinCalls.Add(1)
		s.observe(&s.pinGauge)
	} else {
		s.unpinCalls.Add(1)
		s.observe(&s.unpinGauge)
	}
	return []storage.TestBaselineState{{ID: id, Pinned: pinnedAt != nil, PinnedAt: pinnedAt}}, nil
}

func (s *operatorGaugePersistence) MoveTest(string, *string) error {
	s.moveCalls.Add(1)
	s.observe(&s.moveGauge)
	return nil
}

func (s *operatorGaugePersistence) DeleteTest(string) error {
	s.deleteCalls.Add(1)
	s.observe(&s.deleteGauge)
	return nil
}

// baselineFallbackGaugePersistence implements the whole-map baseline write but
// not the scoped one, which is the branch PinComparisonBaseline takes for stores
// without the column-specific method.
type baselineFallbackGaugePersistence struct {
	*gaugeObservingPersistence

	baselineGauge atomic.Int64
	baselineCalls atomic.Int32
}

func (s *baselineFallbackGaugePersistence) UpdateTestBaselines(map[string]*time.Time) error {
	s.baselineCalls.Add(1)
	s.observe(&s.baselineGauge)
	return nil
}

// TestOperatorTriggeredLockHeldWritesArePublishedToTheInFlightGauge closes the
// sibling hole the backstop's gauge shipped with a second time.
//
// The lifecycle writes (status, full-row record, VUser scale) were counted, but
// the operator-triggered ones were not, and they hold tr.mu across their store
// call in exactly the same way: PinComparisonBaseline and UnpinComparisonBaseline
// (POST/DELETE /api/v1/tests/{id}/comparison-baseline/pin), MoveTestDurable
// (PUT .../folder) and DeleteTestDurable (DELETE /api/v1/tests/{id}). A single
// operator click during a managed-store failover therefore left the gauge at 0
// for the whole stall, and both readers of it misfired: the run-driver panic
// backstop classified the healthy-but-held mutex as orphaned and re-panicked the
// controller, and the liveness probe fell into its live path and blocked until
// the kubelet restarted the replica into the same unreachable store.
func TestOperatorTriggeredLockHeldWritesArePublishedToTheInFlightGauge(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := &operatorGaugePersistence{gaugeObservingPersistence: &gaugeObservingPersistence{runner: runner}}
	runner.testStore = store

	finished := &ManagedTest{
		ID:        "finished-run",
		Name:      "finished-run",
		Status:    "COMPLETE",
		ProjectID: storage.DefaultProjectID,
	}
	runner.tests[finished.ID] = finished

	if _, err := runner.PinComparisonBaseline(finished.ID); err != nil {
		t.Fatalf("PinComparisonBaseline: %v", err)
	}
	if got := store.pinCalls.Load(); got != 1 {
		t.Fatalf("scoped baseline pin writes = %d, want the durable pin path to have been used", got)
	}
	if got := store.pinGauge.Load(); got < 1 {
		t.Errorf("in-flight gauge during an operator comparison-baseline pin = %d, want at least 1; a stalled pin holds tr.mu with the backstop and the liveness probe both reading 0", got)
	}

	if _, err := runner.UnpinComparisonBaseline(finished.ID); err != nil {
		t.Fatalf("UnpinComparisonBaseline: %v", err)
	}
	if got := store.unpinCalls.Load(); got != 1 {
		t.Fatalf("scoped baseline unpin writes = %d, want the durable unpin path to have been used", got)
	}
	if got := store.unpinGauge.Load(); got < 1 {
		t.Errorf("in-flight gauge during an operator comparison-baseline unpin = %d, want at least 1", got)
	}

	folder := "archive-folder"
	moved, err := runner.MoveTestDurable(finished.ID, &folder)
	if err != nil {
		t.Fatalf("MoveTestDurable: %v", err)
	}
	if !moved {
		t.Fatal("MoveTestDurable reported no move for an existing test")
	}
	if got := store.moveCalls.Load(); got != 1 {
		t.Fatalf("durable folder moves = %d, want 1", got)
	}
	if got := store.moveGauge.Load(); got < 1 {
		t.Errorf("in-flight gauge during an operator folder move = %d, want at least 1", got)
	}

	deleted, err := runner.DeleteTestDurable(finished.ID)
	if err != nil {
		t.Fatalf("DeleteTestDurable: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteTestDurable reported no deletion for a completed test")
	}
	if got := store.deleteCalls.Load(); got != 1 {
		t.Fatalf("durable deletes = %d, want 1", got)
	}
	if got := store.deleteGauge.Load(); got < 1 {
		t.Errorf("in-flight gauge during an operator delete = %d, want at least 1", got)
	}

	// The gauge must be a gauge, not a leak: an unbalanced increment would extend
	// the backstop forever and pin the liveness bypass open.
	if got := runner.durableWritesInFlight.Load(); got != 0 {
		t.Fatalf("in-flight gauge after every operator write returned = %d, want 0", got)
	}
}

// TestBaselineFallbackWriteIsPublishedToTheInFlightGauge covers the other
// baseline branch: a store without the column-specific method persists the whole
// changed set through persistBaselineChanges, still with tr.mu held.
func TestBaselineFallbackWriteIsPublishedToTheInFlightGauge(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := &baselineFallbackGaugePersistence{gaugeObservingPersistence: &gaugeObservingPersistence{runner: runner}}
	runner.testStore = store

	finished := &ManagedTest{
		ID:        "finished-run",
		Name:      "finished-run",
		Status:    "ABORTED",
		ProjectID: storage.DefaultProjectID,
	}
	runner.tests[finished.ID] = finished

	if _, err := runner.PinComparisonBaseline(finished.ID); err != nil {
		t.Fatalf("PinComparisonBaseline: %v", err)
	}
	if got := store.baselineCalls.Load(); got != 1 {
		t.Fatalf("whole-map baseline writes = %d, want the fallback path to have been used", got)
	}
	if got := store.baselineGauge.Load(); got < 1 {
		t.Fatalf("in-flight gauge during the fallback baseline write = %d, want at least 1", got)
	}
	if got := runner.durableWritesInFlight.Load(); got != 0 {
		t.Fatalf("in-flight gauge after the fallback baseline write returned = %d, want 0", got)
	}
}

// stallingBaselinePersistence stalls inside the scoped comparison-baseline write
// and leaves every other operation instant, so a test can pin tr.mu on an
// operator action rather than on a run's lifecycle transition.
type stallingBaselinePersistence struct {
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newStallingBaselinePersistence() *stallingBaselinePersistence {
	return &stallingBaselinePersistence{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (s *stallingBaselinePersistence) releaseAll() { s.releaseOnce.Do(func() { close(s.release) }) }

func (s *stallingBaselinePersistence) SaveTest(*storage.TestRecord) error { return nil }

func (s *stallingBaselinePersistence) GetTest(string) (*storage.TestRecord, error) {
	return nil, nil
}

func (s *stallingBaselinePersistence) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *stallingBaselinePersistence) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (s *stallingBaselinePersistence) UpdateTestStatus(string, string, *time.Time, string) error {
	return nil
}

func (s *stallingBaselinePersistence) DeleteTest(string) error { return nil }

func (s *stallingBaselinePersistence) MoveTest(string, *string) error { return nil }

func (s *stallingBaselinePersistence) SetTestComparisonBaseline(id string, pinnedAt *time.Time) ([]storage.TestBaselineState, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return []storage.TestBaselineState{{ID: id, Pinned: pinnedAt != nil, PinnedAt: pinnedAt}}, nil
}

// TestHealthProbeAnswersWhileAnOperatorBaselinePinHoldsTheRunnerLock is the
// end-to-end half of the same defect: the liveness probe must survive an
// operator action that stalls in the store, not just a run's own lifecycle
// write. /api/v1/system/health is the shipped livenessProbe (periodSeconds 20,
// timeoutSeconds 3, failureThreshold 3), so a handler that queues behind the
// pin's tr.mu has the container killed ~43s into a database failover.
func TestHealthProbeAnswersWhileAnOperatorBaselinePinHoldsTheRunnerLock(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	store := newStallingBaselinePersistence()
	t.Cleanup(store.releaseAll)
	server.runner.testStore = store

	finished := &ManagedTest{ID: "finished-run", Name: "finished-run", Status: "COMPLETE"}
	server.runner.mu.Lock()
	server.runner.tests[finished.ID] = finished
	server.runner.mu.Unlock()

	if warm := probeHealth(t, server); warm.Code != http.StatusOK {
		t.Fatalf("warm-up probe status = %d, want %d body=%s", warm.Code, http.StatusOK, warm.Body.String())
	}

	pinned := make(chan error, 1)
	go func() {
		_, err := server.runner.PinComparisonBaseline(finished.ID)
		pinned <- err
	}()
	select {
	case <-store.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the operator baseline pin never reached the storage driver")
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
			t.Fatalf("liveness probe status while an operator baseline pin held the runner lock = %d, want %d", code, http.StatusOK)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("liveness probe blocked past its 3s timeout while a healthy operator baseline pin held the runner lock; kubernetes would restart the replica mid-failover")
	}

	store.releaseAll()
	if err := <-pinned; err != nil {
		t.Fatalf("the stalled operator baseline pin failed: %v", err)
	}
}
