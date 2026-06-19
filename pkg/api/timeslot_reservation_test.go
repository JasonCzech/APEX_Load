package api

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// failingReservationStore makes every reservation lookup error, to exercise the
// fail-closed admission path.
type failingReservationStore struct{ err error }

func (f failingReservationStore) GetCapacityReservation(string) (*storage.CapacityReservation, error) {
	return nil, f.err
}

func (f failingReservationStore) UpdateCapacityReservationBinding(string, *string, *string, time.Time) (bool, error) {
	return false, f.err
}

func (f failingReservationStore) ListCapacityReservationsByTimeRange(string, time.Time, time.Time) ([]*storage.CapacityReservation, error) {
	return nil, f.err
}

func reservedInt(v int) *int       { return &v }
func reservedStr(v string) *string { return &v }

func storeBackedRunner(t *testing.T) (*TestRunner, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(100, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID: "script-1", Name: "S", Protocol: "http", Version: "1",
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	})
	return runner, store
}

func approvedTimeslot(now time.Time) *storage.CapacityReservation {
	return &storage.CapacityReservation{
		ID: "ts-1", ProjectID: "proj-a",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		ApprovalState:     "approved",
		RequestedCapacity: &storage.CapacityReservationRequest{VUsers: reservedInt(1000)},
	}
}

func TestCreateTestBindsTimeslot(t *testing.T) {
	runner, store := storeBackedRunner(t)
	if err := store.SaveCapacityReservation(approvedTimeslot(time.Now())); err != nil {
		t.Fatalf("save timeslot: %v", err)
	}
	tid := "ts-1"
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "T", Duration: "10m", ScriptID: "script-1", VUsers: 100, ProjectID: "proj-a", TimeslotID: &tid,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if test.TimeslotID == nil || *test.TimeslotID != "ts-1" {
		t.Fatalf("test.TimeslotID = %v, want ts-1", test.TimeslotID)
	}
	reloaded, err := store.GetCapacityReservation("ts-1")
	if err != nil {
		t.Fatalf("get timeslot: %v", err)
	}
	if reloaded.BoundTestID == nil || *reloaded.BoundTestID != test.ID {
		t.Fatalf("timeslot not bound to the test: %v", reloaded.BoundTestID)
	}
}

func TestCreateTestTimeslotStrictRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*storage.CapacityReservation)
		wantErr string
	}{
		{"unapproved", func(ts *storage.CapacityReservation) { ts.ApprovalState = "pending" }, "approved"},
		{"wrong project", func(ts *storage.CapacityReservation) { ts.ProjectID = "proj-other" }, "different project"},
		{"capacity short", func(ts *storage.CapacityReservation) {
			ts.RequestedCapacity = &storage.CapacityReservationRequest{VUsers: reservedInt(50)}
		}, "reserves only"},
		{"window too small", func(ts *storage.CapacityReservation) { ts.EndAt = time.Now().Add(2 * time.Minute) }, "does not fit"},
		{"already bound", func(ts *storage.CapacityReservation) { ts.BoundTestID = reservedStr("other-test") }, "no longer bindable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner, store := storeBackedRunner(t)
			ts := approvedTimeslot(time.Now())
			tc.mutate(ts)
			if err := store.SaveCapacityReservation(ts); err != nil {
				t.Fatalf("save timeslot: %v", err)
			}
			tid := "ts-1"
			_, err := runner.CreateTest(CreateTestRequest{
				Name: "T", Duration: "10m", ScriptID: "script-1", VUsers: 100, ProjectID: "proj-a", TimeslotID: &tid,
			})
			if err == nil {
				t.Fatalf("expected create to fail (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want contains %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestReservationAdmissionFailsClosedOnLookupError(t *testing.T) {
	runner, _ := storeBackedRunner(t)
	runner.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
	// Swap in a store whose reservation lookup always errors (e.g. SQLITE_BUSY).
	runner.reservationStore = failingReservationStore{err: errors.New("database is locked")}

	test := &ManagedTest{ID: "x", ProjectID: "project-b", TotalVUsers: 100, Status: "QUEUED",
		CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 4}}

	runner.mu.Lock()
	blocked, reason := runner.evaluateReservedCapacityBlockLocked(test, "")
	runner.mu.Unlock()

	if !blocked {
		t.Fatal("admission must fail CLOSED (hold the test) when the reservation lookup errors")
	}
	if !strings.Contains(reason, "reservation state") {
		t.Fatalf("reason = %q, want a transient reservation-state hold", reason)
	}
}

func TestReservationAwareAdmissionBlocksOtherTenant(t *testing.T) {
	runner, store := storeBackedRunner(t)
	runner.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 10
	now := time.Now()
	// project-a holds an approved, bound timeslot reserving 8 of 10 generators.
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "ts-a", ProjectID: "project-a", StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		ApprovalState: "approved", BoundTestID: reservedStr("a-test"),
		RequestedCapacity: &storage.CapacityReservationRequest{Generators: reservedInt(8)},
	}); err != nil {
		t.Fatalf("save timeslot: %v", err)
	}

	testB := &ManagedTest{ID: "b-test", ProjectID: "project-b", TotalVUsers: 100, Status: "QUEUED", CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 4}}
	testA := &ManagedTest{ID: "a2", ProjectID: "project-a", TotalVUsers: 100, Status: "QUEUED", CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 4}}

	runner.mu.Lock()
	blockedB, reason := runner.evaluateReservedCapacityBlockLocked(testB, "")
	blockedA, _ := runner.evaluateReservedCapacityBlockLocked(testA, "")
	runner.mu.Unlock()

	if !blockedB {
		t.Fatal("project-b should be blocked: project-a's bound timeslot reserves 8 of 10 generators (shared=2 < 4)")
	}
	if !strings.Contains(reason, "shared generator capacity") {
		t.Fatalf("block reason = %q", reason)
	}
	if blockedA {
		t.Fatal("project-a holds the reservation and should admit its own run")
	}

	// Control: remove the timeslot → project-b is no longer blocked.
	if err := store.DeleteCapacityReservation("ts-a"); err != nil {
		t.Fatalf("delete timeslot: %v", err)
	}
	runner.mu.Lock()
	stillBlocked, _ := runner.evaluateReservedCapacityBlockLocked(testB, "")
	runner.mu.Unlock()
	if stillBlocked {
		t.Fatal("with no bound timeslot, project-b should admit")
	}
}
