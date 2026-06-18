package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// fakeFleetScaler injects a can-provision scaler without a real cluster.
type fakeFleetScaler struct {
	can      bool
	workload string
	err      error
	calls    int
	lastN    int
}

func (f *fakeFleetScaler) CanProvision() bool { return f.can }
func (f *fakeFleetScaler) Describe() ProvisionerInfo {
	return ProvisionerInfo{Name: "kubernetes", CanProvision: f.can, Description: "fake k8s scaler"}
}
func (f *fakeFleetScaler) ScalePool(_ context.Context, _ *storage.FleetPool, desiredCount int) (string, int, error) {
	f.calls++
	f.lastN = desiredCount
	if f.err != nil {
		return "", 0, f.err
	}
	return firstNonEmpty(f.workload, "apexload-generator"), desiredCount, nil
}

func TestProvisioningRequestWithKubernetesScaler(t *testing.T) {
	server, _ := newTimeslotServer(t)
	server.fleetScaler = &fakeFleetScaler{can: true, workload: "apexload-generator"}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "eu", "cloud_provider": "kubernetes", "max_size": 50})
	var pool storage.FleetPool
	_ = json.Unmarshal(create.Body.Bytes(), &pool)

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 5, "reason": "scale up"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("provision status = %d body=%s", rec.Code, rec.Body.String())
	}
	var req storage.ProvisioningRequest
	_ = json.Unmarshal(rec.Body.Bytes(), &req)
	if req.Status != "success" {
		t.Fatalf("status = %q, want success", req.Status)
	}
	if req.CompletedAt == nil {
		t.Fatal("expected CompletedAt set")
	}
	types := map[string]bool{}
	for _, e := range req.Events {
		types[e.EventType] = true
	}
	if !types["provisioning"] || !types["success"] {
		t.Fatalf("events = %+v, want provisioning+success", req.Events)
	}

	// The pool's desired size now reflects the scale.
	getPool := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/pools/"+pool.ID, nil)
	var updated storage.FleetPool
	_ = json.Unmarshal(getPool.Body.Bytes(), &updated)
	if updated.DesiredSize != 5 {
		t.Fatalf("pool desired_size = %d, want 5", updated.DesiredSize)
	}

	// /fleet/provisioner now reports the active kubernetes backend.
	prov := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/provisioner", nil)
	var info ProvisionerInfo
	_ = json.Unmarshal(prov.Body.Bytes(), &info)
	if info.Name != "kubernetes" || !info.CanProvision {
		t.Fatalf("provisioner = %+v, want kubernetes/can_provision", info)
	}
}

func TestProvisioningRequestScalerFailureRecordsFailed(t *testing.T) {
	server, _ := newTimeslotServer(t)
	server.fleetScaler = &fakeFleetScaler{can: true, err: errors.New("api server unreachable")}
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "x", "cloud_provider": "kubernetes"})
	var pool storage.FleetPool
	_ = json.Unmarshal(create.Body.Bytes(), &pool)
	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 3})
	var req storage.ProvisioningRequest
	_ = json.Unmarshal(rec.Body.Bytes(), &req)
	if req.Status != "failed" {
		t.Fatalf("status = %q, want failed", req.Status)
	}
}

// TestKubernetesFleetScalerScalesWorkload exercises the real scaler against a fake
// in-cluster client: clamping to the pool window + the per-pool workload tag.
func TestKubernetesFleetScalerScalesWorkload(t *testing.T) {
	fake := &fakeKubeRestarter{}
	scaler := kubernetesFleetScaler{kube: fake, namespace: "apex", defaultWorkload: "apexload-generator"}

	pool := &storage.FleetPool{ID: "p", Name: "eu", MaxSize: 10}
	workload, n, err := scaler.ScalePool(context.Background(), pool, 25) // clamped to MaxSize
	if err != nil {
		t.Fatalf("scale: %v", err)
	}
	if workload != "apexload-generator" || n != 10 {
		t.Fatalf("scale = %s/%d, want apexload-generator/10 (clamped to max)", workload, n)
	}
	if len(fake.scales) != 1 || fake.scales[0].replicas != 10 || fake.scales[0].name != "apexload-generator" {
		t.Fatalf("scale calls = %+v", fake.scales)
	}

	// A per-pool workload tag overrides the default StatefulSet name.
	pool.Tags = map[string]string{"kubernetes.statefulset": "apexload-generator-eu"}
	workload, _, _ = scaler.ScalePool(context.Background(), pool, 3)
	if workload != "apexload-generator-eu" {
		t.Fatalf("tag override workload = %s, want apexload-generator-eu", workload)
	}
}

func TestFleetPoolCRUD(t *testing.T) {
	server, _ := newTimeslotServer(t)

	// Validation.
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"cloud_provider": "kubernetes"}},
		{"missing provider", map[string]any{"name": "p"}},
		{"min over max", map[string]any{"name": "p", "cloud_provider": "kubernetes", "min_size": 5, "max_size": 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "east-http", "cloud_provider": "kubernetes", "region": "eastus", "desired_size": 3})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var pool storage.FleetPool
	if err := json.Unmarshal(create.Body.Bytes(), &pool); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pool.ID == "" || pool.Status != "active" {
		t.Fatalf("created pool = %+v, want id + active", pool)
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/pools", nil)
	var listResp struct {
		FleetPools []storage.FleetPool `json:"fleet_pools"`
		Count      int                 `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Fatalf("list count = %d, want 1", listResp.Count)
	}

	upd := timeslotRequest(t, server, http.MethodPut, "/api/v1/fleet/pools/"+pool.ID,
		map[string]any{"name": "east-http", "cloud_provider": "kubernetes", "status": "suspended", "desired_size": 6})
	if upd.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", upd.Code, upd.Body.String())
	}
	var updated storage.FleetPool
	_ = json.Unmarshal(upd.Body.Bytes(), &updated)
	if updated.Status != "suspended" || updated.DesiredSize != 6 {
		t.Fatalf("update not applied: %+v", updated)
	}

	del := timeslotRequest(t, server, http.MethodDelete, "/api/v1/fleet/pools/"+pool.ID, nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
	if rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/pools/"+pool.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", rec.Code)
	}
}

func TestGeneratorMetadataPatchLifecycle(t *testing.T) {
	server, _ := newTimeslotServer(t)
	path := "/api/v1/fleet/generators/gen-1"

	// PATCH on an untracked generator creates the record.
	rec := timeslotRequest(t, server, http.MethodPatch, path,
		map[string]any{"cordoned": true, "region": "eu", "fleet_pool_id": "pool-1", "tags": map[string]string{"role": "soak"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", rec.Code, rec.Body.String())
	}
	var meta storage.GeneratorMetadata
	_ = json.Unmarshal(rec.Body.Bytes(), &meta)
	if !meta.Cordoned || meta.Region != "eu" || meta.FleetPoolID == nil || *meta.FleetPoolID != "pool-1" {
		t.Fatalf("patch not applied: %+v", meta)
	}

	listResp := struct {
		Generators []storage.GeneratorMetadata `json:"generators"`
		Count      int                         `json:"count"`
	}{}
	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/generators", nil)
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Fatalf("generator list count = %d, want 1", listResp.Count)
	}

	// Empty fleet_pool_id clears membership; omitted region stays unchanged.
	rec2 := timeslotRequest(t, server, http.MethodPatch, path, map[string]any{"fleet_pool_id": "", "cordoned": false})
	var meta2 storage.GeneratorMetadata
	_ = json.Unmarshal(rec2.Body.Bytes(), &meta2)
	if meta2.FleetPoolID != nil {
		t.Fatalf("fleet_pool_id not cleared: %v", *meta2.FleetPoolID)
	}
	if meta2.Cordoned {
		t.Fatalf("cordoned not unset")
	}
	if meta2.Region != "eu" {
		t.Fatalf("omitted region should be unchanged, got %q", meta2.Region)
	}
}

func TestProvisioningRequestNotConfigured(t *testing.T) {
	server, _ := newTimeslotServer(t)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "p", "cloud_provider": "kubernetes"})
	var pool storage.FleetPool
	_ = json.Unmarshal(create.Body.Bytes(), &pool)

	// Validation.
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests", map[string]any{"desired_count": 2}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing pool status = %d, want 400", rec.Code)
	}
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests", map[string]any{"fleet_pool_id": pool.ID, "desired_count": 0}); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero count status = %d, want 400", rec.Code)
	}
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests", map[string]any{"fleet_pool_id": "nope", "desired_count": 2}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown pool status = %d, want 404", rec.Code)
	}

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 3, "reason": "soak prep"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("provision status = %d body=%s", rec.Code, rec.Body.String())
	}
	var req storage.ProvisioningRequest
	_ = json.Unmarshal(rec.Body.Bytes(), &req)
	if req.Status != "not_configured" {
		t.Fatalf("status = %q, want not_configured (no provider wired)", req.Status)
	}
	if len(req.Events) < 2 {
		t.Fatalf("events = %d, want >= 2 (requested + not_configured)", len(req.Events))
	}

	get := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/provision-requests/"+req.ID, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d", get.Code)
	}
}

func TestGeneratorInventoryPoolEnrichment(t *testing.T) {
	server, store := newTimeslotServer(t)
	poolID := "pool-eu"
	if err := store.SaveFleetPool(&storage.FleetPool{ID: poolID, Name: "EU edge", CloudProvider: "kubernetes"}); err != nil {
		t.Fatalf("save pool: %v", err)
	}
	pid := poolID
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-a", FleetPoolID: &pid, Region: "eu-west", Cordoned: true}); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{
		{Target: "gen-a:9090", GeneratorID: "gen-a", Status: "connected", DrainState: "active", CapacityState: "available"},
		{Target: "gen-b:9090", GeneratorID: "gen-b", Status: "connected", DrainState: "active", CapacityState: "available"},
	}})

	rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/system/inventory", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("inventory status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp SystemInventoryResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	byID := map[string]GeneratorInventoryEntry{}
	for _, g := range resp.Generators {
		byID[g.GeneratorID] = g
	}
	a := byID["gen-a"]
	if a.FleetPoolID != poolID || a.PoolName != "EU edge" || !a.Cordoned {
		t.Fatalf("gen-a not pool-enriched: %+v", a)
	}
	if a.Region != "eu-west" {
		t.Fatalf("gen-a region not folded from metadata: %q", a.Region)
	}
	if a.DrainState != "cordoned" {
		t.Fatalf("gen-a drain_state = %q, want cordoned", a.DrainState)
	}
	if b := byID["gen-b"]; b.FleetPoolID != "" || b.Cordoned {
		t.Fatalf("gen-b (no metadata) should be untouched: %+v", b)
	}
}

func TestPoolAssignmentResolvesToMembers(t *testing.T) {
	server, store := newTimeslotServer(t)
	poolID := "pool-eu"
	if err := store.SaveFleetPool(&storage.FleetPool{ID: poolID, Name: "EU"}); err != nil {
		t.Fatalf("save pool: %v", err)
	}
	if err := store.SaveFleetPoolMembers(poolID, []*storage.FleetPoolMember{
		{ID: "m1", PoolID: poolID, GeneratorID: "gen-a", Target: "gen-a:9090"},
	}); err != nil {
		t.Fatalf("save members: %v", err)
	}
	pid := poolID
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-b", Target: "gen-b:9090", FleetPoolID: &pid}); err != nil {
		t.Fatalf("save metadata: %v", err)
	}

	server.runner.mu.Lock()
	resolved, err := server.runner.resolvePoolAssignmentLocked(&GeneratorAssignmentSettings{PoolID: poolID})
	server.runner.mu.Unlock()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Mode != generatorAssignmentModeSpecificHosts || resolved.PoolID != poolID {
		t.Fatalf("resolved = %+v, want specific_hosts + pool retained", resolved)
	}
	got := map[string]bool{}
	for _, target := range resolved.SelectedTargets {
		got[target] = true
	}
	if !got["gen-a:9090"] || !got["gen-b:9090"] {
		t.Fatalf("targets = %v, want gen-a + gen-b (members + metadata)", resolved.SelectedTargets)
	}

	// Unknown pool → error.
	server.runner.mu.Lock()
	_, err = server.runner.resolvePoolAssignmentLocked(&GeneratorAssignmentSettings{PoolID: "nope"})
	server.runner.mu.Unlock()
	if err == nil {
		t.Fatal("unknown pool should error")
	}
}

func TestFleetManagementRouteAccessRequirements(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/fleet/pools"},
		{http.MethodGet, "/api/v1/fleet/pools"},
		{http.MethodGet, "/api/v1/fleet/pools/p1"},
		{http.MethodPut, "/api/v1/fleet/pools/p1"},
		{http.MethodDelete, "/api/v1/fleet/pools/p1"},
		{http.MethodGet, "/api/v1/fleet/generators"},
		{http.MethodPatch, "/api/v1/fleet/generators/g1"},
		{http.MethodPost, "/api/v1/fleet/provision-requests"},
		{http.MethodGet, "/api/v1/fleet/provision-requests/r1"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok {
				t.Fatalf("no access requirement for %s %s", tc.method, tc.path)
			}
			if len(requirement.GlobalRoles) != 1 || requirement.GlobalRoles[0] != RoleSystemAdmin {
				t.Fatalf("GlobalRoles = %#v, want [system_admin] (fleet management is admin-only)", requirement.GlobalRoles)
			}
		})
	}
}
