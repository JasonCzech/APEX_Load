package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	server, store := newTimeslotServer(t)
	if err := store.SaveFleetPool(&storage.FleetPool{ID: "pool-1", Name: "pool", CloudProvider: "kubernetes", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
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

func TestGeneratorMetadataPatchRejectsDanglingPool(t *testing.T) {
	server, _ := newTimeslotServer(t)
	rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/fleet/generators/gen-1", map[string]any{"fleet_pool_id": "missing"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch status = %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestGeneratorMetadataCordonUpdatesEnforcedDrainState(t *testing.T) {
	server, _ := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)

	rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/fleet/generators/gen-a", map[string]any{"cordoned": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "cordoned" {
		t.Fatalf("enforced drain_state = %q, want cordoned", got)
	}
	allowed, err := backend.allowedTargetsForTest(&ManagedTest{})
	if err != nil {
		t.Fatalf("allowedTargetsForTest: %v", err)
	}
	if len(allowed) != 1 || allowed[0] != "gen-b:9090" {
		t.Fatalf("allowed targets = %#v, want only gen-b", allowed)
	}
}

func TestGeneratorLifecycleEndpointsUpdateEnforcedStateAndMetadata(t *testing.T) {
	server, store := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/gen-a/cordon", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cordon status = %d body=%s", rec.Code, rec.Body.String())
	}
	var cordon GeneratorLifecycleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cordon); err != nil {
		t.Fatalf("decode cordon: %v", err)
	}
	if cordon.GeneratorID != "gen-a" || cordon.Target != "gen-a:9090" || cordon.DrainState != "cordoned" || !cordon.Cordoned {
		t.Fatalf("cordon response = %+v", cordon)
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "cordoned" {
		t.Fatalf("drain_state after cordon = %q, want cordoned", got)
	}
	allowed, err := backend.allowedTargetsForTest(&ManagedTest{})
	if err != nil {
		t.Fatalf("allowedTargetsForTest after cordon: %v", err)
	}
	if len(allowed) != 1 || allowed[0] != "gen-b:9090" {
		t.Fatalf("allowed targets after cordon = %#v, want gen-b only", allowed)
	}
	meta, err := store.GetGeneratorMetadata("gen-a")
	if err != nil {
		t.Fatalf("metadata after cordon: %v", err)
	}
	if !meta.Cordoned || meta.Target != "gen-a:9090" {
		t.Fatalf("metadata after cordon = %+v", meta)
	}

	rec = timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/gen-a/uncordon", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("uncordon status = %d body=%s", rec.Code, rec.Body.String())
	}
	var uncordon GeneratorLifecycleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &uncordon); err != nil {
		t.Fatalf("decode uncordon: %v", err)
	}
	if uncordon.DrainState != "active" || uncordon.Cordoned {
		t.Fatalf("uncordon response = %+v", uncordon)
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "active" {
		t.Fatalf("drain_state after uncordon = %q, want active", got)
	}
	meta, err = store.GetGeneratorMetadata("gen-a")
	if err != nil {
		t.Fatalf("metadata after uncordon: %v", err)
	}
	if meta.Cordoned {
		t.Fatalf("metadata after uncordon = %+v, want not cordoned", meta)
	}
}

func TestGeneratorDrainEndpointUsesStableGeneratorID(t *testing.T) {
	server, _ := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"10.0.0.1:9090", "10.0.0.2:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("10.0.0.1:9090", "apexload-generator-0")
	server.runner.setExecutionBackend(backend)

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/apexload-generator-0/drain", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("drain status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := backend.generatorDrainState("10.0.0.1:9090"); got != "draining" {
		t.Fatalf("initial target drain_state = %q, want draining", got)
	}
	backend.SetTargets([]string{"10.0.0.9:9090", "10.0.0.2:9090"})
	backend.recordGeneratorIdentity("10.0.0.9:9090", "apexload-generator-0")
	if got := backend.generatorDrainState("10.0.0.9:9090"); got != "draining" {
		t.Fatalf("replacement target drain_state = %q, want draining", got)
	}
}

func TestEvacuateGeneratorEndpointDryRunListsImpactedShards(t *testing.T) {
	server, _ := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)
	test := &ManagedTest{
		ID:         "run-evac",
		Name:       "Checkout Evac",
		Status:     "RUNNING",
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 4}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"buyers": {
				{Group: "buyers", Target: "gen-a:9090", VUsers: 2},
				{Group: "buyers", Target: "gen-b:9090", VUsers: 2},
			},
		},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-a:9090": newEvacuationFakeGeneratorClient("gen-a:9090"),
			"gen-b:9090": newEvacuationFakeGeneratorClient("gen-b:9090"),
		},
		remoteTargetStates: map[string]string{"gen-a:9090": "RUNNING", "gen-b:9090": "RUNNING"},
	}
	server.runner.mu.Lock()
	server.runner.tests[test.ID] = test
	server.runner.mu.Unlock()

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/gen-a/evacuate", map[string]any{"dry_run": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("evacuate dry-run status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload GeneratorEvacuationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.DryRun || payload.Applied || payload.GeneratorID != "gen-a" || payload.Target != "gen-a:9090" {
		t.Fatalf("response = %+v, want dry-run gen-a/gen-a:9090 without apply", payload)
	}
	if payload.ImpactedTests != 1 || len(payload.Impacts) != 1 || payload.Impacts[0].Group != "buyers" {
		t.Fatalf("impacts = %+v, want buyers impact", payload.Impacts)
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "active" {
		t.Fatalf("dry-run drain state = %q, want active", got)
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

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/provision-requests?fleet_pool_id="+pool.ID, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", list.Code, list.Body.String())
	}
	var listPayload struct {
		ProvisioningRequests []storage.ProvisioningRequest `json:"provisioning_requests"`
		Count                int                           `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listPayload)
	if listPayload.Count != 1 || len(listPayload.ProvisioningRequests) != 1 || listPayload.ProvisioningRequests[0].ID != req.ID {
		t.Fatalf("list payload = %+v, want created request", listPayload)
	}
}

func TestProvisioningRequestCancelAndRetryLifecycle(t *testing.T) {
	server, store := newTimeslotServer(t)
	pool := &storage.FleetPool{ID: "pool-1", Name: "pool", CloudProvider: "kubernetes", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	req := &storage.ProvisioningRequest{ID: "req-1", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 4, ProjectID: "checkout"}
	if err := store.SaveProvisioningRequest(req); err != nil {
		t.Fatalf("SaveProvisioningRequest: %v", err)
	}

	cancel := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/req-1/cancel", nil)
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", cancel.Code, cancel.Body.String())
	}
	var cancelled storage.ProvisioningRequest
	_ = json.Unmarshal(cancel.Body.Bytes(), &cancelled)
	if cancelled.Status != "cancelled" || cancelled.CompletedAt == nil {
		t.Fatalf("cancelled request = %+v, want cancelled with CompletedAt", cancelled)
	}
	events := map[string]bool{}
	for _, event := range cancelled.Events {
		events[event.EventType] = true
	}
	if !events["cancelled"] {
		t.Fatalf("cancel events = %+v, want cancelled event", cancelled.Events)
	}

	scaler := &fakeFleetScaler{can: true, workload: "apexload-generator"}
	server.fleetScaler = scaler
	retry := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/req-1/retry", nil)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d body=%s", retry.Code, retry.Body.String())
	}
	var retried storage.ProvisioningRequest
	_ = json.Unmarshal(retry.Body.Bytes(), &retried)
	if retried.Status != "success" || scaler.calls != 1 || scaler.lastN != 4 {
		t.Fatalf("retried request = %+v scaler=%+v, want success scale to 4", retried, scaler)
	}
	events = map[string]bool{}
	for _, event := range retried.Events {
		events[event.EventType] = true
	}
	if !events["retry"] || !events["success"] {
		t.Fatalf("retry events = %+v, want retry+success", retried.Events)
	}
	updatedPool, err := store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatalf("GetFleetPool: %v", err)
	}
	if updatedPool.DesiredSize != 4 {
		t.Fatalf("pool desired_size = %d, want 4 after retry", updatedPool.DesiredSize)
	}

	cancelSucceeded := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/req-1/cancel", nil)
	if cancelSucceeded.Code != http.StatusConflict {
		t.Fatalf("cancel succeeded request status = %d, want 409", cancelSucceeded.Code)
	}
}

func TestProvisioningRequestRejectsInactivePool(t *testing.T) {
	server, _ := newTimeslotServer(t)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "p", "cloud_provider": "kubernetes", "status": "suspended"})
	var pool storage.FleetPool
	_ = json.Unmarshal(create.Body.Bytes(), &pool)

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 3})
	if rec.Code != http.StatusConflict {
		t.Fatalf("provision status = %d, want 409 body=%s", rec.Code, rec.Body.String())
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
	if a.FleetPoolID != poolID || a.PoolName != "EU edge" || a.Cordoned {
		t.Fatalf("gen-a not pool-enriched: %+v", a)
	}
	if a.Region != "eu-west" {
		t.Fatalf("gen-a region not folded from metadata: %q", a.Region)
	}
	if a.DrainState != "active" {
		t.Fatalf("gen-a drain_state = %q, want active when only metadata is cordoned", a.DrainState)
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
		{http.MethodGet, "/api/v1/fleet/provision-requests"},
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
