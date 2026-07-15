package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func openSharedHADeleteStores(t *testing.T) (*storage.SQLiteStore, *storage.SQLiteStore) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "shared-ha-delete.db")
	oldStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(old): %v", err)
	}
	t.Cleanup(func() { _ = oldStore.Close() })
	newStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(new): %v", err)
	}
	t.Cleanup(func() { _ = newStore.Close() })
	return oldStore, newStore
}

func TestDeleteTestDurableUsesDatabaseLeaseFenceAfterTakeover(t *testing.T) {
	oldStore, newStore := openSharedHADeleteStores(t)
	now := time.Now().UTC()
	record := &storage.TestRecord{
		ID:        "runner-stale-delete",
		Name:      "Runner stale delete",
		Status:    "COMPLETE",
		ProjectID: storage.DefaultProjectID,
		StartTime: now,
		Groups:    []any{},
	}
	if err := oldStore.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	oldLeadership := newExecutionLeadership(oldStore, "delete-runner-old")
	oldLeadership.tick()
	defer oldLeadership.relinquish()
	if !oldLeadership.IsAdmitted() {
		t.Fatal("old runner did not acquire execution leadership")
	}
	runner := NewTestRunner(1, NewHub(), oldStore, WithExecutionLeadership(oldLeadership))
	oldToken := oldLeadership.executionFenceToken()
	if oldToken == "" {
		t.Fatal("old runner has no execution fence token")
	}

	// The old runner remains locally admitted while the database-visible term is
	// transferred and the successor starts the row it still sees as terminal.
	if err := oldStore.ReleaseSchedulerLease(executionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(newStore, "delete-runner-new")
	newLeadership.tick()
	defer newLeadership.relinquish()
	newToken := newLeadership.executionFenceToken()
	if newToken == "" {
		t.Fatal("successor did not acquire execution leadership")
	}
	record.Status = "RUNNING"
	if saved, err := newStore.SaveTestIfLeaseHeld(record, executionLeaseName, newToken); err != nil || !saved {
		t.Fatalf("successor start = (%v, %v), want (true, nil)", saved, err)
	}

	deleted, err := runner.DeleteTestWithError(record.ID)
	if deleted || !errors.Is(err, errNotExecutionLeader) {
		t.Fatalf("stale runner delete = (%v, %v), want (false, errNotExecutionLeader)", deleted, err)
	}
	if got, err := newStore.GetTest(record.ID); err != nil || got.Status != "RUNNING" {
		t.Fatalf("stale runner delete changed successor row = (%#v, %v)", got, err)
	}
	runner.mu.RLock()
	_, retained := runner.tests[record.ID]
	runner.mu.RUnlock()
	if !retained {
		t.Fatal("stale runner removed local metadata after fenced durable delete")
	}
}

func TestHandleDeleteProjectReturnsUnavailableAfterDatabaseTakeover(t *testing.T) {
	oldStore, newStore := openSharedHADeleteStores(t)
	now := time.Now().UTC()
	projectID := "handler-stale-project-delete"
	if err := oldStore.SaveProject(&storage.Project{ID: projectID, Name: "Stale project delete", CreatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	record := &storage.TestRecord{
		ID:        "handler-stale-project-test",
		Name:      "Stale project test",
		Status:    "COMPLETE",
		ProjectID: projectID,
		StartTime: now,
		Groups:    []any{},
	}
	if err := oldStore.SaveTest(record); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
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
	oldToken := server.leadership.executionFenceToken()
	if err := oldStore.ReleaseSchedulerLease(executionLeaseName, oldToken); err != nil {
		t.Fatalf("release old lease: %v", err)
	}
	newLeadership := newExecutionLeadership(newStore, "delete-project-new")
	newLeadership.tick()
	defer newLeadership.relinquish()
	newToken := newLeadership.executionFenceToken()
	record.Status = "INITIALIZING"
	if saved, err := newStore.SaveTestIfLeaseHeld(record, executionLeaseName, newToken); err != nil || !saved {
		t.Fatalf("successor start = (%v, %v), want (true, nil)", saved, err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projectID, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale project delete status = %d, want %d body=%s", resp.Code, http.StatusServiceUnavailable, resp.Body.String())
	}
	if _, err := newStore.GetProject(projectID); err != nil {
		t.Fatalf("stale handler removed project: %v", err)
	}
	if got, err := newStore.GetTest(record.ID); err != nil || got.Status != "INITIALIZING" {
		t.Fatalf("stale handler changed successor run = (%#v, %v)", got, err)
	}
}
