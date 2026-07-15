package api

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestStandaloneCapacityBindRefreshesDurableAndRunnerState(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "slot-refresh-runner", ProjectID: "proj-a",
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID: "test-refresh-runner", Name: "refresh runner", Status: "QUEUED", ProjectID: "proj-a",
		StartTime: now, Duration: int64(10 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}

	response := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/slot-refresh-runner/bind", map[string]any{
		"test_id": "test-refresh-runner",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("bind status = %d body=%s", response.Code, response.Body.String())
	}
	durable, err := store.GetTest("test-refresh-runner")
	if err != nil || durable.TimeslotID == nil || *durable.TimeslotID != "slot-refresh-runner" {
		t.Fatalf("durable test binding = (%#v, %v)", durable, err)
	}
	server.runner.mu.RLock()
	inMemory := server.runner.tests["test-refresh-runner"]
	server.runner.mu.RUnlock()
	if inMemory == nil || inMemory.TimeslotID == nil || *inMemory.TimeslotID != "slot-refresh-runner" {
		t.Fatalf("runner binding was not refreshed: %#v", inMemory)
	}
}

func TestStandaloneCapacityBindIsFencedAfterDatabaseTakeover(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shared-ha-capacity-bind.db")
	oldStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(old): %v", err)
	}
	defer oldStore.Close()
	newStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(new): %v", err)
	}
	defer newStore.Close()
	server := NewServer(ServerConfig{Store: oldStore, MaxConcurrent: 1, HAExecutionLeadership: true})
	server.leadership.tick()
	deadline := time.Now().Add(2 * time.Second)
	for !server.leadership.IsAdmitted() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !server.leadership.IsAdmitted() {
		t.Fatal("old server did not finish leadership recovery")
	}
	defer server.leadership.relinquish()

	now := time.Now().UTC()
	if err := oldStore.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "slot-stale-handler", ProjectID: storage.DefaultProjectID,
		StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour), ApprovalState: "approved",
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := oldStore.SaveTest(&storage.TestRecord{
		ID: "test-stale-handler", Name: "stale handler", Status: "QUEUED", ProjectID: storage.DefaultProjectID,
		StartTime: now, Duration: int64(10 * time.Minute), Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	oldToken := server.leadership.executionFenceToken()
	if err := oldStore.ReleaseSchedulerLease(executionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(newStore, "capacity-bind-successor")
	newLeadership.tick()
	defer newLeadership.relinquish()
	if newLeadership.executionFenceToken() == "" {
		t.Fatal("successor did not acquire execution lease")
	}

	response := timeslotRequest(t, server, http.MethodPost, "/api/v1/capacity/timeslots/slot-stale-handler/bind", map[string]any{
		"test_id": "test-stale-handler",
	})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale bind status = %d, want 503 body=%s", response.Code, response.Body.String())
	}
	reservation, err := newStore.GetCapacityReservation("slot-stale-handler")
	if err != nil || reservation.BoundTestID != nil {
		t.Fatalf("stale handler changed reservation = (%#v, %v)", reservation, err)
	}
	test, err := newStore.GetTest("test-stale-handler")
	if err != nil || test.TimeslotID != nil {
		t.Fatalf("stale handler changed test = (%#v, %v)", test, err)
	}
}

func TestTerminalCapacityReleaseClearsBothDurableSides(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	testID := "test-terminal-two-sided"
	slotID := "slot-terminal-two-sided"
	status := "RUNNING"
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: slotID, ProjectID: "proj-a", StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
		ApprovalState: "approved", BoundTestID: &testID, BoundTestStatus: &status,
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID: testID, Name: "terminal release", Status: status, ProjectID: "proj-a", StartTime: now,
		Duration: int64(10 * time.Minute), TimeslotID: &slotID, Groups: []any{},
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	server.runner.mu.Lock()
	server.runner.tests[testID] = &ManagedTest{ID: testID, Status: status, TimeslotID: &slotID}
	server.runner.mu.Unlock()
	endedAt := now.Add(5 * time.Minute)
	if err := server.runner.updateStoredTestStatus(testID, "COMPLETE", &endedAt, ""); err != nil {
		t.Fatalf("updateStoredTestStatus: %v", err)
	}
	reservation, err := store.GetCapacityReservation(slotID)
	if err != nil || reservation.BoundTestID != nil || reservation.BoundTestStatus != nil {
		t.Fatalf("reservation release = (%#v, %v)", reservation, err)
	}
	durable, err := store.GetTest(testID)
	if err != nil || durable.TimeslotID != nil {
		t.Fatalf("test release = (%#v, %v)", durable, err)
	}
	server.runner.mu.RLock()
	inMemory := server.runner.tests[testID]
	server.runner.mu.RUnlock()
	if inMemory == nil || inMemory.TimeslotID != nil {
		t.Fatalf("runner terminal release was not refreshed: %#v", inMemory)
	}
}
