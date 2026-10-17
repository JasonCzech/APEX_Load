package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// lateKubernetesScaler models a scale call that completes after its caller's
// lease term has ended: it ignores cancellation and returns once released.
type lateKubernetesScaler struct {
	entered chan struct{}
	release chan struct{}
}

func (s *lateKubernetesScaler) CanProvision() bool { return true }

func (s *lateKubernetesScaler) Describe() ProvisionerInfo {
	return ProvisionerInfo{Name: "late-test", CanProvision: true}
}

func (s *lateKubernetesScaler) ScalePool(_ context.Context, _ *storage.FleetPool, desiredCount int) (string, int, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return "apexload-generator", desiredCount, nil
}

// newLeasedKubernetesProvisioningServer holds a durable execution lease without
// the takeover recovery hook, so a same-process reacquisition changes only the
// lease term and the stale request's outcome is deterministic.
func newLeasedKubernetesProvisioningServer(t *testing.T) (*Server, *storage.SQLiteStore, *executionLeadership, *storage.FleetPool) {
	t.Helper()
	server, store := newTimeslotServer(t)
	sqliteStore, ok := store.(*storage.SQLiteStore)
	if !ok {
		t.Fatalf("store = %T, want *storage.SQLiteStore", store)
	}
	leadership := newExecutionLeadership(sqliteStore, "kubernetes-provisioning-owner")
	leadership.tick()
	waitForCondition(t, time.Second, leadership.IsLeader, "kubernetes provisioning leadership")
	t.Cleanup(leadership.relinquish)
	server.runner.leadership = leadership
	server.runner.durableExecutionFence = true
	pool := &storage.FleetPool{ID: "k8s-owner-pool", Name: "k8s owner pool", CloudProvider: "kubernetes", Status: "active", MaxSize: 10, DesiredSize: 1}
	saveCapacityTestPool(t, sqliteStore, pool)
	seedRuntimePoolTargets(t, sqliteStore, pool.ID, 3)
	return server, sqliteStore, leadership, pool
}

func reacquireSameProcessLease(t *testing.T, leadership *executionLeadership) {
	t.Helper()
	_, before := leadership.executionLeaseTerm()
	leadership.relinquish()
	leadership.tick()
	waitForCondition(t, time.Second, func() bool {
		_, generation := leadership.executionLeaseTerm()
		return leadership.IsLeader() && generation != before
	}, "same-process lease reacquisition")
}

func awaitProvisioningResponse(t *testing.T, responses <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-responses:
		return rec
	case <-time.After(5 * time.Second):
		t.Fatal("provisioning request did not finish")
		return nil
	}
}

func assertStaleProvisioningNotPublished(t *testing.T, store *storage.SQLiteStore, pool *storage.FleetPool, requestID string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "execution leadership changed") {
		t.Fatalf("response = %d %s, want 409 leadership change", rec.Code, rec.Body.String())
	}
	stored, err := store.GetProvisioningRequest(requestID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "provisioning" || stored.CompletedAt != nil {
		t.Fatalf("stored request = %s (completed %v), want it left for the new term to reconcile", stored.Status, stored.CompletedAt)
	}
	updated, err := store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredSize != pool.DesiredSize {
		t.Fatalf("pool desired size = %d, want %d unchanged by the stale attempt", updated.DesiredSize, pool.DesiredSize)
	}
}

func TestKubernetesProvisioningDoesNotPublishAfterLeaseReacquisition(t *testing.T) {
	server, store, leadership, pool := newLeasedKubernetesProvisioningServer(t)
	scaler := &lateKubernetesScaler{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server.fleetScaler = scaler

	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responses <- timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
			map[string]any{"fleet_pool_id": pool.ID, "desired_count": 3})
	}()
	waitCapacityTestSignal(t, scaler.entered, "kubernetes scale start")
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 1 {
		t.Fatalf("ListProvisioningRequests = %d, %v", len(requests), err)
	}
	reacquireSameProcessLease(t, leadership)
	close(scaler.release)

	assertStaleProvisioningNotPublished(t, store, pool, requests[0].ID, awaitProvisioningResponse(t, responses))
}

func TestKubernetesProvisioningRetryDoesNotPublishAfterLeaseReacquisition(t *testing.T) {
	server, store, leadership, pool := newLeasedKubernetesProvisioningServer(t)
	server.fleetScaler = &fakeFleetScaler{can: true, err: errors.New("first scale failed")}
	failed := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 3})
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 1 || requests[0].Status != "failed" {
		t.Fatalf("setup request = %d %s; requests=%+v err=%v", failed.Code, failed.Body.String(), requests, err)
	}
	scaler := &lateKubernetesScaler{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server.fleetScaler = scaler

	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		responses <- timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/"+requests[0].ID+"/retry", nil)
	}()
	waitCapacityTestSignal(t, scaler.entered, "kubernetes retry scale start")
	reacquireSameProcessLease(t, leadership)
	close(scaler.release)

	assertStaleProvisioningNotPublished(t, store, pool, requests[0].ID, awaitProvisioningResponse(t, responses))
}

func TestKubernetesProvisioningRecoveryDoesNotPublishAfterLeaseReacquisition(t *testing.T) {
	server, store, leadership, pool := newLeasedKubernetesProvisioningServer(t)
	scaler := &lateKubernetesScaler{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server.fleetScaler = scaler
	request := &storage.ProvisioningRequest{ID: "k8s-recovery-request", FleetPoolID: pool.ID, Revision: 1, AttemptToken: "abandoned-attempt", Status: "provisioning", DesiredCount: 3}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- server.reconcileProvisioningRequests(context.Background()) }()
	waitCapacityTestSignal(t, scaler.entered, "kubernetes recovery scale start")
	reacquireSameProcessLease(t, leadership)
	close(scaler.release)

	select {
	case err := <-done:
		// Reacquisition cancels the old term's operation context; recovery
		// reports the lost term as that cancellation or, if it sees the new
		// term first, as errNotExecutionLeader.
		if !errors.Is(err, errNotExecutionLeader) && !errors.Is(err, context.Canceled) {
			t.Fatalf("reconcile error = %v, want the lost term reported", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not finish")
	}
	stored, err := store.GetProvisioningRequest(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "provisioning" || stored.CompletedAt != nil {
		t.Fatalf("stored request = %s, want it left for the new term to reconcile", stored.Status)
	}
	if updated, err := store.GetFleetPool(pool.ID); err != nil || updated.DesiredSize != pool.DesiredSize {
		t.Fatalf("pool = %+v, %v; want desired size %d unchanged", updated, err, pool.DesiredSize)
	}
}
