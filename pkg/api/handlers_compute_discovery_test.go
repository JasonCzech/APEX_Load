package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

// stubCloudProbeResolver stands in for a live provider so the handler contract
// can be asserted without network access.
type stubCloudProbeResolver struct {
	probes    []ClusterProbe
	inventory CloudResourceInventory
	err       error
	instances []compute.Instance
}

func (s *stubCloudProbeResolver) ProbeCloudConnection(context.Context, *storage.CloudConnection) []ClusterProbe {
	return s.probes
}

func (s *stubCloudProbeResolver) DiscoverCloudResources(context.Context, *storage.CloudConnection) (CloudResourceInventory, error) {
	if s.err != nil {
		return CloudResourceInventory{}, s.err
	}
	return s.inventory, nil
}

func (s *stubCloudProbeResolver) BackendForTarget(context.Context, *storage.ExecutionTarget) (compute.Backend, error) {
	return &stubDiscoveryBackend{instances: s.instances}, nil
}

// BackendForPool satisfies capacityBackendPoolResolver, which Server.capacityBackends is typed as.
func (s *stubCloudProbeResolver) BackendForPool(context.Context, *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	return &stubDiscoveryBackend{instances: s.instances}, nil, nil
}

type stubDiscoveryBackend struct {
	compute.Backend
	instances []compute.Instance
}

func (b *stubDiscoveryBackend) Discover(context.Context, compute.CapacityRequest) ([]compute.Instance, error) {
	return b.instances, nil
}

func TestCloudConnectionTestReportsProviderProbes(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	seedReverseExecutionTarget(t, store, strings.Repeat("v", 32))
	server.capacityBackends = &stubCloudProbeResolver{probes: []ClusterProbe{
		{Probe: "provider_identity", OK: true, Message: "Azure identity acquired an ARM token"},
		{Probe: "subscription", Message: "subscription is not readable with this identity"},
	}}

	rec := httptest.NewRecorder()
	server.handleTestCloudConnection(rec, newPathRequest(http.MethodPost, "/api/v1/system/cloud-connections/cloud-reverse/test", "cloud-reverse"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response CloudConnectionTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	// A failing probe must degrade the rollup rather than be swallowed: the
	// whole point of the endpoint is telling an operator which capability is
	// missing before they build a target on the credential.
	if response.Status == "ok" {
		t.Fatalf("status = %q, want a non-ok rollup when a probe fails", response.Status)
	}
	if len(response.Probes) != 3 {
		t.Fatalf("probes = %d, want credential + 2 provider probes", len(response.Probes))
	}
	if response.Probes[0].Probe != "credential" {
		t.Fatalf("first probe = %q, want the local credential check to run first", response.Probes[0].Probe)
	}
}

func TestCloudConnectionResourcesSurfacesProviderFailureAsBadGateway(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	seedReverseExecutionTarget(t, store, strings.Repeat("v", 32))
	server.capacityBackends = &stubCloudProbeResolver{err: context.DeadlineExceeded}

	rec := httptest.NewRecorder()
	server.handleListCloudConnectionResources(rec, newPathRequest(http.MethodGet, "/api/v1/system/cloud-connections/cloud-reverse/resources", "cloud-reverse"))
	// A provider/permission failure is not a malformed request; 4xx would tell
	// the operator to fix their input when the fix is in the cloud account.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s, want 502", rec.Code, rec.Body.String())
	}
}

func TestCloudConnectionResourcesReturnsDiscoveredResources(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	seedReverseExecutionTarget(t, store, strings.Repeat("v", 32))
	server.capacityBackends = &stubCloudProbeResolver{inventory: CloudResourceInventory{
		CloudConnectionID: "cloud-reverse", Provider: storage.CloudProviderAzure,
		Resources: []CloudResource{{Kind: "container_app_environment", ID: "/subscriptions/s/env", Name: "env", Location: "eastus"}},
	}}

	rec := httptest.NewRecorder()
	server.handleListCloudConnectionResources(rec, newPathRequest(http.MethodGet, "/api/v1/system/cloud-connections/cloud-reverse/resources", "cloud-reverse"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var inventory CloudResourceInventory
	if err := json.Unmarshal(rec.Body.Bytes(), &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Resources) != 1 || inventory.Resources[0].Name != "env" {
		t.Fatalf("resources = %#v", inventory.Resources)
	}
}

func TestExecutionTargetCapacityAggregatesBoundPoolsAndLiveInstances(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	target := seedReverseExecutionTarget(t, store, strings.Repeat("v", 32))
	// A second real target: the store enforces the execution-target foreign key,
	// and the point of this pool is proving other targets' pools are excluded.
	other := &storage.ExecutionTarget{
		ID: "different-target", Name: "Other", Kind: storage.ExecutionTargetAzureContainerApps,
		CloudConnectionID: target.CloudConnectionID, ConnectivityMode: storage.ConnectivityReverse,
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerDisabled,
		ArchiveMode:        storage.ArchiveModeControllerPull,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", EnvironmentName: "env", WarmContainerApp: "warm"},
	}
	if err := storage.SetExecutionTargetEnrollmentToken(other, strings.Repeat("w", 32)); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateExecutionTarget(other); err != nil {
		t.Fatalf("CreateExecutionTarget: %v", err)
	}
	for _, pool := range []*storage.FleetPool{
		{ID: "pool-a", Name: "A", CloudProvider: "azure", Status: "active", ExecutionTargetID: target.ID, WarmFloor: 2, MaxSize: 10, DesiredSize: 3, LifecycleMode: storage.FleetLifecycleWarmBurst},
		{ID: "pool-b", Name: "B", CloudProvider: "azure", Status: "active", ExecutionTargetID: target.ID, WarmFloor: 1, MaxSize: 5, DesiredSize: 1, LifecycleMode: storage.FleetLifecycleWarmBurst},
		{ID: "pool-other", Name: "Other", CloudProvider: "azure", Status: "active", ExecutionTargetID: "different-target", WarmFloor: 99, MaxSize: 99},
	} {
		if err := store.SaveFleetPool(pool); err != nil {
			t.Fatalf("SaveFleetPool %s: %v", pool.ID, err)
		}
	}
	// The same provider instance is returned for both pools to prove dedupe by
	// provider ID; double-counting would overstate live capacity.
	server.capacityBackends = &stubCloudProbeResolver{instances: []compute.Instance{
		{ProviderID: "exec-1", Healthy: true},
		{ProviderID: "exec-2"},
	}}

	rec := httptest.NewRecorder()
	server.handleGetExecutionTargetCapacity(rec, newPathRequest(http.MethodGet, "/api/v1/system/execution-targets/"+target.ID+"/capacity", target.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response ExecutionTargetCapacityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Pools) != 2 {
		t.Fatalf("pools = %d, want only the two bound to this target", len(response.Pools))
	}
	if response.ConfiguredWarm != 3 || response.ConfiguredMax != 15 {
		t.Fatalf("warm=%d max=%d, want 3/15 summed across bound pools only", response.ConfiguredWarm, response.ConfiguredMax)
	}
	if response.LiveInstances != 2 || response.HealthyInstances != 1 {
		t.Fatalf("live=%d healthy=%d, want 2/1 after dedupe across pools", response.LiveInstances, response.HealthyInstances)
	}
}

func newPathRequest(method, path, id string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.SetPathValue("id", id)
	return req
}
