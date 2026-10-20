package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	computeazure "github.com/jasonczech/apexload/pkg/compute/azure"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Keep real ACI planning semantics while replacing cloud mutations with a
// bounded, side-effect-free implementation.
type reviewMainACIPlanningBackend struct {
	*fakeCapacityBackend
	planner *computeazure.InstancesBackend
}

func (b *reviewMainACIPlanningBackend) Describe() compute.BackendInfo { return b.planner.Describe() }
func (b *reviewMainACIPlanningBackend) Plan(ctx context.Context, request compute.CapacityRequest) (compute.Plan, error) {
	return b.planner.Plan(ctx, request)
}

func TestReviewMainOperatorProvisioningRejectsRunlessACIBurst(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	connection := &storage.CloudConnection{
		ID: "review-aci-cloud", Name: "review Azure", Provider: storage.CloudProviderAzure,
		AuthMode: storage.CloudAuthAzureIdentity, AzureSubscriptionID: "subscription-review",
	}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &storage.ExecutionTarget{
		ID: "review-aci-target", Name: "review ACI", Kind: storage.ExecutionTargetAzureContainerInstances,
		CloudConnectionID: connection.ID, ConnectivityMode: storage.ConnectivityReverse,
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		AzureContainerInstances: &storage.AzureContainerInstancesTargetSpec{
			ResourceGroup: "review-rg", ContainerGroupTemplate: "review-template", Location: "eastus",
		},
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	pool := &storage.FleetPool{
		ID: "review-aci-pool", Name: "review ACI", ExecutionTargetID: target.ID,
		Status: "active", LifecycleMode: storage.FleetLifecycleOnDemand, ReadinessTimeoutSeconds: 5,
	}
	saveCapacityTestPool(t, store, pool)
	started := make(chan struct{}, 1)
	backend := &reviewMainACIPlanningBackend{
		fakeCapacityBackend: &fakeCapacityBackend{
			reconcileErr: errors.New("review probe: do not call a cloud provider"), reconcileStarted: started,
		},
		planner: &computeazure.InstancesBackend{Target: target},
	}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: target}
	request := &storage.ProvisioningRequest{
		ID: "review-aci-request", FleetPoolID: pool.ID, DesiredCount: 1,
		Revision: 1, AttemptToken: "review-aci-attempt", ProjectID: storage.DefaultProjectID,
	}
	recorder := httptest.NewRecorder()
	handled, err := server.startAsyncCapacityProvisioning(recorder,
		httptest.NewRequest(http.MethodPost, "/api/v1/fleet/provision-requests", nil), pool, request, time.Now().UTC())
	if !handled {
		t.Fatal("ACI provisioning did not use asynchronous capacity path")
	}
	if err != nil {
		return // Burst-only capacity must fail preflight for an operator request.
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("accepted operator ACI request did not reach the provider reconciliation probe")
	}
	backend.mu.Lock()
	launched := backend.lastRequest
	backend.mu.Unlock()
	t.Fatalf("operator request accepted HTTP %d and dispatched ACI burst launch with test_id=%q and %d launch intent(s); want preflight rejection before creating ephemeral capacity without a run owner", recorder.Code, launched.TestID, len(launched.LaunchIntents))
}
