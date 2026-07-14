package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestProvisioningAttemptCASFencesStaleCompletion(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	req := &ProvisioningRequest{
		ID: "provisioning-cas", Revision: 1, AttemptToken: "attempt-a", FleetPoolID: "pool-a",
		ProjectID: DefaultProjectID, Status: "provisioning", DesiredCount: 3, CreatedAt: now, UpdatedAt: now,
	}
	created, err := store.CreateProvisioningRequestIfAbsent(req)
	if err != nil || !created {
		t.Fatalf("CreateProvisioningRequestIfAbsent = %v, %v", created, err)
	}
	cancelled := *req
	cancelled.Revision = 2
	cancelled.AttemptToken = "cancel-fence"
	cancelled.Status = "cancelled"
	cancelled.CompletedAt = &now
	changed, err := store.TransitionProvisioningRequest(&cancelled, 1, "attempt-a")
	if err != nil || !changed {
		t.Fatalf("cancel transition = %v, %v", changed, err)
	}
	stale := *req
	stale.Revision = 2
	stale.Status = "success"
	stale.CompletedAt = &now
	changed, err = store.TransitionProvisioningRequest(&stale, 1, "attempt-a")
	if err != nil || changed {
		t.Fatalf("stale completion = %v, %v; want false, nil", changed, err)
	}
	loaded, err := store.GetProvisioningRequest(req.ID)
	if err != nil {
		t.Fatalf("GetProvisioningRequest: %v", err)
	}
	if loaded.Status != "cancelled" || loaded.Revision != 2 || loaded.AttemptToken != "cancel-fence" {
		t.Fatalf("durable request = %#v, want fenced cancellation", loaded)
	}
}
