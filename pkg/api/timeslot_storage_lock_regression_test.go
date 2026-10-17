package api

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// timeslotLockObservingStore retains the concrete SQLite method set so the
// production atomic binding paths remain enabled, while observing every
// storage boundary used by create-time and standalone timeslot binding.
type timeslotLockObservingStore struct {
	SQLiteStore         *storage.SQLiteStore
	runner              *TestRunner
	timeslotIOCalls     atomic.Int32
	timeslotIOUnderLock atomic.Bool
	expectedFenceID     string
	unexpectedFenceID   string
	canonicalFenceHeld  atomic.Bool
	wrongFenceHeld      atomic.Bool
}

func (s *timeslotLockObservingStore) observeTimeslotIO() {
	s.timeslotIOCalls.Add(1)
	if s.runner == nil {
		return
	}
	if s.runner.mu.TryLock() {
		s.runner.mu.Unlock()
		return
	}
	s.timeslotIOUnderLock.Store(true)
}

func (s *timeslotLockObservingStore) GetCapacityReservation(id string) (*storage.CapacityReservation, error) {
	s.observeTimeslotIO()
	return s.SQLiteStore.GetCapacityReservation(id)
}

func (s *timeslotLockObservingStore) GetTest(id string) (*storage.TestRecord, error) {
	s.observeTimeslotIO()
	return s.SQLiteStore.GetTest(id)
}

func (s *timeslotLockObservingStore) CreateTestAndBindCapacityReservation(test *storage.TestRecord, reservationID string, boundAt time.Time) (storage.CapacityBindingResult, error) {
	s.observeTimeslotIO()
	return s.SQLiteStore.CreateTestAndBindCapacityReservation(test, reservationID, boundAt)
}

func (s *timeslotLockObservingStore) BindCapacityReservationToTestWithWindow(reservationID, testID string, boundAt, windowStartAt, windowEndAt time.Time) (storage.CapacityBindingResult, error) {
	s.observeTimeslotIO()
	if s.runner != nil && s.expectedFenceID != "" {
		s.runner.archiveCommitMu.Lock()
		if gate := s.runner.archiveCommitGates[s.expectedFenceID]; gate != nil && gate.refs > 0 {
			s.canonicalFenceHeld.Store(true)
		}
		if gate := s.runner.archiveCommitGates[s.unexpectedFenceID]; gate != nil && gate.refs > 0 {
			s.wrongFenceHeld.Store(true)
		}
		s.runner.archiveCommitMu.Unlock()
	}
	return s.SQLiteStore.BindCapacityReservationToTestWithWindow(reservationID, testID, boundAt, windowStartAt, windowEndAt)
}

func newTimeslotLockObservingRunner(t *testing.T) (*TestRunner, *timeslotLockObservingStore) {
	t.Helper()
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &timeslotLockObservingStore{SQLiteStore: base}
	runner := NewTestRunner(10, NewHub(), sqliteOverlay(store))
	store.runner = runner
	store.timeslotIOCalls.Store(0)
	store.timeslotIOUnderLock.Store(false)
	return runner, store
}

func TestCreateTimeTimeslotStorageRunsOutsideRunnerLock(t *testing.T) {
	runner, store := newTimeslotLockObservingRunner(t)
	now := time.Now().UTC()
	if err := store.SQLiteStore.SaveProject(&storage.Project{
		ID: "timeslot-lock-project", Name: "Timeslot lock project", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	storePreflightScript(runner, "timeslot-lock-script", "timeslot-lock-project")
	if err := store.SQLiteStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "timeslot-create-lock", ProjectID: "timeslot-lock-project",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	store.timeslotIOCalls.Store(0)
	store.timeslotIOUnderLock.Store(false)

	timeslotID := "timeslot-create-lock"
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "create-time timeslot lock", Duration: "10m", ScriptID: "timeslot-lock-script",
		VUsers: 1, ProjectID: "timeslot-lock-project", TimeslotID: &timeslotID,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if created == nil || created.TimeslotID == nil || *created.TimeslotID != timeslotID {
		t.Fatalf("created test = %#v, want bound timeslot %q", created, timeslotID)
	}
	if store.timeslotIOUnderLock.Load() {
		t.Fatal("create-time timeslot persistence ran while runner.mu was held")
	}
	if got := store.timeslotIOCalls.Load(); got < 4 {
		t.Fatalf("observed timeslot storage calls = %d, want evidence, atomic bind, and durable verification", got)
	}
}

func TestStandaloneTimeslotStorageRunsOutsideRunnerLock(t *testing.T) {
	runner, store := newTimeslotLockObservingRunner(t)
	now := time.Now().UTC()
	if err := store.SQLiteStore.SaveProject(&storage.Project{
		ID: "timeslot-lock-project", Name: "Timeslot lock project", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SQLiteStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "timeslot-standalone-lock", ProjectID: "timeslot-lock-project",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := store.SQLiteStore.SaveTest(&storage.TestRecord{
		ID: "timeslot-standalone-test", Name: "standalone timeslot lock", Status: "QUEUED",
		RefKey: "RUN-TIMESLOT-REF", ProjectID: "timeslot-lock-project", StartTime: now,
		Duration: int64(10 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	store.timeslotIOCalls.Store(0)
	store.timeslotIOUnderLock.Store(false)
	store.expectedFenceID = "timeslot-standalone-test"
	store.unexpectedFenceID = "RUN-TIMESLOT-REF"
	store.canonicalFenceHeld.Store(false)
	store.wrongFenceHeld.Store(false)

	// The durable row was created after runner startup, so this replica has not
	// indexed its ref key. GetTest must resolve it and hand the deletion fence to
	// the canonical ID before the atomic bind begins.
	reservation, result, err := runner.BindCapacityTimeslot("timeslot-standalone-lock", "RUN-TIMESLOT-REF")
	if err != nil {
		t.Fatalf("BindCapacityTimeslot: %v", err)
	}
	if result != storage.CapacityBindingBound || reservation == nil || reservation.BoundTestID == nil || *reservation.BoundTestID != "timeslot-standalone-test" {
		t.Fatalf("BindCapacityTimeslot = (%#v, %q), want durable bound result", reservation, result)
	}
	if store.timeslotIOUnderLock.Load() {
		t.Fatal("standalone timeslot persistence ran while runner.mu was held")
	}
	if !store.canonicalFenceHeld.Load() || store.wrongFenceHeld.Load() {
		t.Fatalf("atomic bind fences: canonical=%t ref=%t, want canonical ID only", store.canonicalFenceHeld.Load(), store.wrongFenceHeld.Load())
	}
	if got := store.timeslotIOCalls.Load(); got < 5 {
		t.Fatalf("observed timeslot storage calls = %d, want lookup, atomic bind, and durable verification", got)
	}
}

func TestStandaloneTimeslotCacheMissRestoresRunnableRuntime(t *testing.T) {
	runner, store := newTimeslotLockObservingRunner(t)
	creator := NewTestRunner(10, NewHub(), sqliteOverlay(store))
	storedScript, err := creator.StoreScriptDurable(httpScript("timeslot-hydration-script", "Timeslot hydration script"))
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	created, err := creator.CreateTest(CreateTestRequest{
		Name:      "timeslot hydration test",
		ProjectID: storage.DefaultProjectID,
		Duration:  "10m",
		Groups: []CreateGroupEntry{{
			ScriptID: storedScript.ID,
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runner.mu.RLock()
	_, alreadyCached := runner.tests[created.ID]
	runner.mu.RUnlock()
	if alreadyCached {
		t.Fatal("test unexpectedly existed in binding runner before cache-miss bind")
	}
	now := time.Now().UTC()
	if err := store.SQLiteStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID:            "timeslot-hydration-slot",
		ProjectID:     storage.DefaultProjectID,
		StartAt:       now.Add(-time.Hour),
		EndAt:         now.Add(time.Hour),
		ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}

	reservation, result, err := runner.BindCapacityTimeslot("timeslot-hydration-slot", created.RefKey)
	if err != nil || result != storage.CapacityBindingBound || reservation == nil {
		t.Fatalf("BindCapacityTimeslot = (%#v, %q, %v), want durable bound result", reservation, result, err)
	}
	runner.mu.RLock()
	hydrated := runner.tests[created.ID]
	if hydrated == nil || hydrated.runtimeHydrationError != "" || len(hydrated.groupSpecs) != 1 || hydrated.groupSpecs[0].Script == nil {
		runner.mu.RUnlock()
		t.Fatalf("hydrated runtime = %#v, want one runnable compiled group", hydrated)
	}
	if hydrated.TimeslotID == nil || *hydrated.TimeslotID != "timeslot-hydration-slot" {
		runner.mu.RUnlock()
		t.Fatalf("hydrated timeslot = %#v, want bound slot", hydrated.TimeslotID)
	}
	runner.mu.RUnlock()
}

func TestStandaloneTimeslotBindDoesNotMutateStaleSameIDRuntime(t *testing.T) {
	runner, store := newTimeslotLockObservingRunner(t)
	now := time.Now().UTC()
	if err := store.SQLiteStore.SaveProject(&storage.Project{
		ID: "timeslot-identity-project", Name: "Timeslot identity project", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.SQLiteStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "timeslot-identity-slot", ProjectID: "timeslot-identity-project",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	record := &storage.TestRecord{
		ID: "timeslot-reused-test-id", Name: "durable replacement", Status: "QUEUED",
		ProjectID: "timeslot-identity-project", StartTime: now, Duration: int64(10 * time.Minute), Groups: []any{},
	}
	if err := store.SQLiteStore.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	stale := managedTestFromRecord(record)
	stale.Name = "stale deleted runtime"
	stale.CreatedAt = now.Add(-time.Hour)
	runner.mu.Lock()
	runner.tests[record.ID] = stale
	runner.mu.Unlock()

	reservation, result, err := runner.BindCapacityTimeslot("timeslot-identity-slot", record.ID)
	if err != nil || result != storage.CapacityBindingBound || reservation == nil {
		t.Fatalf("BindCapacityTimeslot = (%#v, %q, %v), want durable bound result", reservation, result, err)
	}
	runner.mu.RLock()
	current := runner.tests[record.ID]
	if current != stale || current.TimeslotID != nil {
		runner.mu.RUnlock()
		t.Fatalf("stale runtime was replaced or mutated: %#v", current)
	}
	runner.mu.RUnlock()
	durable, err := store.GetTest(record.ID)
	if err != nil || durable == nil || durable.TimeslotID == nil || *durable.TimeslotID != "timeslot-identity-slot" {
		t.Fatalf("durable bound test = (%#v, %v)", durable, err)
	}
}
