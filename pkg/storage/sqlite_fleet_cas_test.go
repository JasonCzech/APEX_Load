package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
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
	if err := store.SaveFleetPool(&FleetPool{ID: "pool-a", Name: "pool-a", CloudProvider: "kubernetes", Status: "active"}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
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

func TestFleetPoolDeleteRejectsActiveProvisioning(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-active", Name: "active", CloudProvider: "kubernetes", Status: "active"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	req := &ProvisioningRequest{ID: "request-active", Revision: 1, AttemptToken: "attempt", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 4}
	if created, err := store.CreateProvisioningRequestIfAbsent(req); err != nil || !created {
		t.Fatalf("CreateProvisioningRequestIfAbsent = %v, %v", created, err)
	}
	if err := store.DeleteFleetPool(pool.ID); !errors.Is(err, ErrFleetPoolHasActiveProvisioning) {
		t.Fatalf("DeleteFleetPool active error = %v, want ErrFleetPoolHasActiveProvisioning", err)
	}
	if _, err := store.GetFleetPool(pool.ID); err != nil {
		t.Fatalf("active request deletion removed pool: %v", err)
	}

	done := time.Now().UTC()
	completed := *req
	completed.Revision++
	completed.Status = "success"
	completed.CompletedAt = &done
	if changed, err := store.TransitionProvisioningRequest(&completed, req.Revision, req.AttemptToken); err != nil || !changed {
		t.Fatalf("complete provisioning = %v, %v", changed, err)
	}
	if err := store.DeleteFleetPool(pool.ID); err != nil {
		t.Fatalf("DeleteFleetPool after completion: %v", err)
	}
}

func TestProvisioningCreateAndFleetPoolDeleteAreAtomic(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-create-delete", Name: "race", CloudProvider: "kubernetes", Status: "active"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	req := &ProvisioningRequest{ID: "request-create-delete", Revision: 1, AttemptToken: "attempt", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 2}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var created bool
	var createErr, deleteErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		created, createErr = store.CreateProvisioningRequestIfAbsent(req)
	}()
	go func() {
		defer wg.Done()
		<-start
		deleteErr = store.DeleteFleetPool(pool.ID)
	}()
	close(start)
	wg.Wait()

	if created {
		if createErr != nil || !errors.Is(deleteErr, ErrFleetPoolHasActiveProvisioning) {
			t.Fatalf("create won: createErr=%v deleteErr=%v", createErr, deleteErr)
		}
		if _, err := store.GetFleetPool(pool.ID); err != nil {
			t.Fatalf("created request references deleted pool: %v", err)
		}
		return
	}
	if !errors.Is(createErr, sql.ErrNoRows) || deleteErr != nil {
		t.Fatalf("delete won: createErr=%v deleteErr=%v", createErr, deleteErr)
	}
	if _, err := store.GetProvisioningRequest(req.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete-first race left request: %v", err)
	}
}

func TestProvisioningCreateRejectsInactiveFleetPool(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-suspended", Name: "suspended", CloudProvider: "kubernetes", Status: "suspended"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProvisioningRequestIfAbsent(&ProvisioningRequest{
		ID: "request-suspended", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 1,
	})
	if created || !errors.Is(err, ErrFleetPoolNotActive) {
		t.Fatalf("inactive provisioning create = %v, %v; want false, ErrFleetPoolNotActive", created, err)
	}
}
