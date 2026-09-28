package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestWriteProvisioningCreateErrorPreservesComputeConflicts(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "ownership", err: fmt.Errorf("external owner: %w", compute.ErrOwnershipConflict)},
		{name: "unsupported", err: fmt.Errorf("target shape: %w", compute.ErrUnsupportedOperation)},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeProvisioningCreateError(response, test.err)
			if response.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
			}
		})
	}
}

// fakeFleetScaler injects a can-provision scaler without a real cluster.
type fakeFleetScaler struct {
	can      bool
	workload string
	err      error
	calls    int
	lastN    int
}

type blockingFleetScaler struct {
	entered chan struct{}
	release chan struct{}
}

type faultingGeneratorMetadataPatchStore struct {
	storage.Store
	delegate interface {
		PatchGeneratorMetadata(string, storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error)
	}
	commitThenError bool
}

func (s *faultingGeneratorMetadataPatchStore) PatchGeneratorMetadata(generatorID string, patch storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error) {
	if s.commitThenError {
		if _, err := s.delegate.PatchGeneratorMetadata(generatorID, patch); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("simulated generator metadata persistence failure")
}

func seedRuntimePoolTargets(t *testing.T, store storage.Store, poolID string, count int) {
	t.Helper()
	members := make([]*storage.FleetPoolMember, 0, count)
	for i := 0; i < count; i++ {
		members = append(members, &storage.FleetPoolMember{
			ID:          fmt.Sprintf("member-%d", i),
			PoolID:      poolID,
			GeneratorID: fmt.Sprintf("generator-%d", i),
			Target:      fmt.Sprintf("generator-%d:9090", i),
			Status:      "ready",
		})
	}
	if err := store.SaveFleetPoolMembers(poolID, members); err != nil {
		t.Fatalf("SaveFleetPoolMembers: %v", err)
	}
}

func (f *blockingFleetScaler) CanProvision() bool { return true }
func (f *blockingFleetScaler) Describe() ProvisionerInfo {
	return ProvisionerInfo{Name: "blocking-test", CanProvision: true}
}
func (f *blockingFleetScaler) ScalePool(ctx context.Context, _ *storage.FleetPool, desiredCount int) (string, int, error) {
	select {
	case f.entered <- struct{}{}:
	default:
	}
	select {
	case <-f.release:
		return "apexload-generator", desiredCount, nil
	case <-ctx.Done():
		return "apexload-generator", 0, ctx.Err()
	}
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
	server, store := newTimeslotServer(t)
	server.fleetScaler = &fakeFleetScaler{can: true, workload: "apexload-generator"}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "eu", "cloud_provider": "kubernetes", "max_size": 50})
	var pool storage.FleetPool
	_ = json.Unmarshal(create.Body.Bytes(), &pool)
	seedRuntimePoolTargets(t, store, pool.ID, 5)

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

func TestProvisioningRequestEnforcesPoolSizingBoundaries(t *testing.T) {
	server, store := newTimeslotServer(t)
	scaler := &fakeFleetScaler{can: true, workload: "apexload-generator"}
	server.fleetScaler = scaler

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "bounded", "cloud_provider": "kubernetes", "min_size": 2, "max_size": 4, "desired_size": 2})
	if create.Code != http.StatusCreated {
		t.Fatalf("create pool status = %d body=%s", create.Code, create.Body.String())
	}
	var pool storage.FleetPool
	if err := json.Unmarshal(create.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	seedRuntimePoolTargets(t, store, pool.ID, 4)

	for _, tc := range []struct {
		name    string
		count   int
		want    int
		message string
	}{
		{name: "below minimum", count: 1, want: http.StatusBadRequest, message: "min_size 2"},
		{name: "at minimum", count: 2, want: http.StatusCreated},
		{name: "at maximum", count: 4, want: http.StatusCreated},
		{name: "above maximum", count: 5, want: http.StatusBadRequest, message: "max_size 4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
				map[string]any{"fleet_pool_id": pool.ID, "desired_count": tc.count})
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.message != "" && !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("body=%s, want %q", rec.Body.String(), tc.message)
			}
			if tc.want == http.StatusCreated {
				var request storage.ProvisioningRequest
				if err := json.Unmarshal(rec.Body.Bytes(), &request); err != nil {
					t.Fatal(err)
				}
				if request.DesiredCount != tc.count || scaler.lastN != tc.count {
					t.Fatalf("request desired=%d scaler desired=%d, want effective count %d", request.DesiredCount, scaler.lastN, tc.count)
				}
			}
		})
	}
	if scaler.calls != 2 {
		t.Fatalf("scaler calls = %d, want only the two in-range boundary requests", scaler.calls)
	}
}

func TestActiveProvisioningCannotBeCancelledOrLoseItsPool(t *testing.T) {
	server, store := newTimeslotServer(t)
	scaler := &blockingFleetScaler{entered: make(chan struct{}, 1), release: make(chan struct{})}
	server.fleetScaler = scaler

	createPool := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "race-safe", "cloud_provider": "kubernetes"})
	if createPool.Code != http.StatusCreated {
		t.Fatalf("create pool = %d body=%s", createPool.Code, createPool.Body.String())
	}
	var pool storage.FleetPool
	if err := json.Unmarshal(createPool.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	seedRuntimePoolTargets(t, store, pool.ID, 3)

	created := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		created <- timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
			map[string]any{"fleet_pool_id": pool.ID, "desired_count": 3})
	}()
	select {
	case <-scaler.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provisioning scale did not start")
	}
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 1 {
		t.Fatalf("ListProvisioningRequests = %d, %v", len(requests), err)
	}

	cancel := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/"+requests[0].ID+"/cancel", nil)
	if cancel.Code != http.StatusConflict {
		t.Fatalf("cancel active scale = %d body=%s, want 409", cancel.Code, cancel.Body.String())
	}
	deletePool := timeslotRequest(t, server, http.MethodDelete, "/api/v1/fleet/pools/"+pool.ID, nil)
	if deletePool.Code != http.StatusConflict {
		t.Fatalf("delete pool during scale = %d body=%s, want 409", deletePool.Code, deletePool.Body.String())
	}

	close(scaler.release)
	select {
	case rec := <-created:
		if rec.Code != http.StatusCreated {
			t.Fatalf("provision response = %d body=%s", rec.Code, rec.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provisioning request did not finish")
	}
	updated, err := store.GetFleetPool(pool.ID)
	if err != nil || updated.DesiredSize != 3 {
		t.Fatalf("completed pool = %#v, %v; want desired size 3", updated, err)
	}
}

func TestDeleteFleetPoolReportsLiveCapacityConflict(t *testing.T) {
	server, store := newTimeslotServer(t)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "live-cloud-capacity", "cloud_provider": "aws"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create pool = %d body=%s", create.Code, create.Body.String())
	}
	var pool storage.FleetPool
	if err := json.Unmarshal(create.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}
	intentStore, ok := store.(storage.CapacityInstanceIntentStore)
	if !ok {
		t.Fatal("test store does not support capacity instance intents")
	}
	if err := intentStore.CreateCapacityInstanceIntent(&storage.CapacityInstanceIntent{
		ID: "intent-live-handler", ExecutionTargetID: "target-live-handler", FleetPoolID: pool.ID,
		ClientToken: "token-live-handler", LaunchNonce: "nonce-live-handler", GeneratorID: "generator-live-handler",
	}); err != nil {
		t.Fatalf("CreateCapacityInstanceIntent: %v", err)
	}

	deleted := timeslotRequest(t, server, http.MethodDelete, "/api/v1/fleet/pools/"+pool.ID, nil)
	if deleted.Code != http.StatusConflict {
		t.Fatalf("delete live pool = %d body=%s, want 409", deleted.Code, deleted.Body.String())
	}
	if !strings.Contains(deleted.Body.String(), "drain or terminate") {
		t.Fatalf("delete conflict = %s, want actionable drain guidance", deleted.Body.String())
	}
	if _, err := store.GetFleetPool(pool.ID); err != nil {
		t.Fatalf("conflicting delete removed pool: %v", err)
	}
}

func TestProvisioningRecoveryRunsWhileAdmissionIsClosed(t *testing.T) {
	server, store := newTimeslotServer(t)
	pool := &storage.FleetPool{ID: "pool-acquire", Name: "pool", CloudProvider: "kubernetes", Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	seedRuntimePoolTargets(t, store, pool.ID, 3)
	req := &storage.ProvisioningRequest{ID: "request-acquire", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 3, ProjectID: storage.DefaultProjectID}
	if err := store.SaveProvisioningRequest(req); err != nil {
		t.Fatal(err)
	}
	leadership := newExecutionLeadership(&fakeLeaseStore{dfltOK: true}, "owner")
	leadership.leader.Store(true)
	leadership.reconciling.Store(true)
	server.runner.leadership = leadership
	scaler := &fakeFleetScaler{can: true}
	server.fleetScaler = scaler

	if err := server.reconcileProvisioningRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if scaler.calls != 1 {
		t.Fatalf("scale calls = %d, want 1 during acquire-time recovery", scaler.calls)
	}
	stored, err := store.GetProvisioningRequest(req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "success" {
		t.Fatalf("recovered status = %q, want success", stored.Status)
	}
}

func TestProvisioningRequestScalerFailureRecordsFailed(t *testing.T) {
	server, store := newTimeslotServer(t)
	server.fleetScaler = &fakeFleetScaler{can: true, err: errors.New("api server unreachable")}
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "x", "cloud_provider": "kubernetes"})
	var pool storage.FleetPool
	_ = json.Unmarshal(create.Body.Bytes(), &pool)
	seedRuntimePoolTargets(t, store, pool.ID, 3)
	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 3})
	var req storage.ProvisioningRequest
	_ = json.Unmarshal(rec.Body.Bytes(), &req)
	if req.Status != "failed" {
		t.Fatalf("status = %q, want failed", req.Status)
	}
}

func TestProvisioningRequiresPreassociatedRuntimeTargets(t *testing.T) {
	server, store := newTimeslotServer(t)
	scaler := &fakeFleetScaler{can: true}
	server.fleetScaler = scaler
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "fresh", "cloud_provider": "kubernetes", "desired_size": 2})
	var pool storage.FleetPool
	if err := json.Unmarshal(create.Body.Bytes(), &pool); err != nil {
		t.Fatal(err)
	}

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 2})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "associated runtime target") {
		t.Fatalf("provision without membership = %d body=%s, want explanatory 409", rec.Code, rec.Body.String())
	}
	if scaler.calls != 0 {
		t.Fatalf("scaler calls = %d, want 0 before membership is complete", scaler.calls)
	}
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 0 {
		t.Fatalf("provisioning requests = %d, %v; rejected request must not be persisted", len(requests), err)
	}

	// Explicit members and metadata bindings form one target-deduplicated union,
	// matching runner assignment. The duplicate does not inflate capacity.
	seedRuntimePoolTargets(t, store, pool.ID, 1)
	poolID := pool.ID
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{
		GeneratorID: "metadata-duplicate", Target: "generator-0:9090", FleetPoolID: &poolID,
	}); err != nil {
		t.Fatal(err)
	}
	rec = timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 2})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate target counted twice: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{
		GeneratorID: "metadata-distinct", Target: "generator-1:9090", FleetPoolID: &poolID,
	}); err != nil {
		t.Fatal(err)
	}
	rec = timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests",
		map[string]any{"fleet_pool_id": pool.ID, "desired_count": 2})
	if rec.Code != http.StatusCreated || scaler.calls != 1 {
		t.Fatalf("provision with complete membership = %d calls=%d body=%s", rec.Code, scaler.calls, rec.Body.String())
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
		{"negative min", map[string]any{"name": "p", "cloud_provider": "kubernetes", "min_size": -1}},
		{"negative desired", map[string]any{"name": "p", "cloud_provider": "kubernetes", "desired_size": -1}},
		{"desired below min", map[string]any{"name": "p", "cloud_provider": "kubernetes", "min_size": 2, "desired_size": 1}},
		{"desired above max", map[string]any{"name": "p", "cloud_provider": "kubernetes", "max_size": 2, "desired_size": 3}},
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

	invalidUpdate := timeslotRequest(t, server, http.MethodPut, "/api/v1/fleet/pools/"+pool.ID,
		map[string]any{"name": "east-http", "cloud_provider": "kubernetes", "min_size": 4, "max_size": 5, "desired_size": 6})
	if invalidUpdate.Code != http.StatusBadRequest {
		t.Fatalf("invalid update status = %d body=%s, want 400", invalidUpdate.Code, invalidUpdate.Body.String())
	}
	unchanged, err := server.store.GetFleetPool(pool.ID)
	if err != nil || unchanged.DesiredSize != 3 {
		t.Fatalf("invalid update changed pool = %+v err=%v", unchanged, err)
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
		map[string]any{"target": "gen-1:9090", "cordoned": true, "region": "eu", "fleet_pool_id": "pool-1", "tags": map[string]string{"role": "soak"}})
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

func TestGeneratorMetadataPoolAssignmentRequiresRuntimeTarget(t *testing.T) {
	server, store := newTimeslotServer(t)
	if err := store.SaveFleetPool(&storage.FleetPool{ID: "pool-1", Name: "pool", CloudProvider: "kubernetes", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/fleet/generators/gen-1", map[string]any{"fleet_pool_id": "pool-1"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "runtime target") {
		t.Fatalf("missing target assignment = %d body=%s, want explanatory 400", rec.Code, rec.Body.String())
	}
	if _, err := store.GetGeneratorMetadata("gen-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("metadata created despite missing target: %v", err)
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

func TestGeneratorMetadataPatchRestoresLiveCordonWhenPersistenceFails(t *testing.T) {
	server, store := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-a", Target: "gen-a:9090"}); err != nil {
		t.Fatal(err)
	}
	server.store = &faultingGeneratorMetadataPatchStore{Store: store, delegate: store.(interface {
		PatchGeneratorMetadata(string, storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error)
	})}

	rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/fleet/generators/gen-a", map[string]any{"cordoned": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("metadata failure status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "active" {
		t.Fatalf("live state = %q, want compensated active", got)
	}
	metadata, err := store.GetGeneratorMetadata("gen-a")
	if err != nil || metadata.Cordoned {
		t.Fatalf("metadata after failed patch = %+v err=%v, want unchanged", metadata, err)
	}
}

func TestGeneratorLifecycleRestoresLiveStateWhenMetadataPersistenceFails(t *testing.T) {
	server, store := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-a", Target: "gen-a:9090"}); err != nil {
		t.Fatal(err)
	}
	server.store = &faultingGeneratorMetadataPatchStore{Store: store, delegate: store.(interface {
		PatchGeneratorMetadata(string, storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error)
	})}

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/gen-a/cordon", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("lifecycle metadata failure status = %d body=%s, want 500", rec.Code, rec.Body.String())
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "active" {
		t.Fatalf("live state = %q, want compensated active", got)
	}
}

func TestGeneratorLifecycleReconcilesLostMetadataAcknowledgement(t *testing.T) {
	server, store := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-a", Target: "gen-a:9090"}); err != nil {
		t.Fatal(err)
	}
	server.store = &faultingGeneratorMetadataPatchStore{Store: store, delegate: store.(interface {
		PatchGeneratorMetadata(string, storage.GeneratorMetadataPatch) (*storage.GeneratorMetadata, error)
	}), commitThenError: true}

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/gen-a/cordon", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("lost acknowledgement status = %d body=%s, want reconciled 200", rec.Code, rec.Body.String())
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "cordoned" {
		t.Fatalf("live state = %q, want cordoned", got)
	}
	metadata, err := store.GetGeneratorMetadata("gen-a")
	if err != nil || !metadata.Cordoned {
		t.Fatalf("metadata after lost acknowledgement = %+v err=%v, want committed cordon", metadata, err)
	}
}

func TestGeneratorLifecycleEndpointsUpdateEnforcedStateAndMetadata(t *testing.T) {
	server, store := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{
		GeneratorID: "gen-a",
		Region:      "eastus",
		ProviderID:  "provider/gen-a",
		Tags:        map[string]string{"role": "soak"},
	}); err != nil {
		t.Fatalf("seed generator metadata: %v", err)
	}

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
	if !meta.Cordoned || meta.Target != "gen-a:9090" || meta.Region != "eastus" || meta.ProviderID != "provider/gen-a" || meta.Tags["role"] != "soak" {
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

func TestGeneratorLifecycleRejectsUnknownGeneratorID(t *testing.T) {
	server, _ := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/missing-gen/cordon", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cordon unknown generator status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "active" {
		t.Fatalf("known generator drain_state = %q, want unchanged active", got)
	}
	if _, err := server.store.GetGeneratorMetadata("missing-gen"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing generator metadata err = %v, want sql.ErrNoRows", err)
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

func TestEvacuateGeneratorPreflightRefusalDoesNotDrain(t *testing.T) {
	server, _ := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-b:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)
	test := &ManagedTest{
		ID:         "run-browser-evac",
		Name:       "Browser Evacuation Refusal",
		Status:     "RUNNING",
		Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true},
		Groups:     []TestGroup{{Name: "browser", Protocol: "browser", VUsers: 1}},
		remoteGroupShards: map[string][]GeneratorShardSummary{
			"browser": {{Group: "browser", Target: "gen-a:9090", VUsers: 1}},
		},
		remoteClientsByTarget: map[string]generatorClient{
			"gen-a:9090": newEvacuationFakeGeneratorClient("gen-a:9090"),
			"gen-b:9090": newEvacuationFakeGeneratorClient("gen-b:9090"),
		},
		remoteTargetStates: map[string]string{"gen-a:9090": "RUNNING"},
	}
	server.runner.mu.Lock()
	server.runner.tests[test.ID] = test
	server.runner.mu.Unlock()

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/generators/gen-a/evacuate", map[string]any{"reason": "maintenance"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("evacuate status = %d body=%s, want conflict", rec.Code, rec.Body.String())
	}
	var payload GeneratorEvacuationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Applied {
		t.Fatalf("response = %+v, want unapplied preflight refusal", payload)
	}
	if got := backend.generatorDrainState("gen-a:9090"); got != "active" {
		t.Fatalf("preflight refusal drain state = %q, want active", got)
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
	seedRuntimePoolTargets(t, store, pool.ID, 4)
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

func TestProvisioningRetryRejectsCountOutsideCurrentPoolBounds(t *testing.T) {
	server, store := newTimeslotServer(t)
	pool := &storage.FleetPool{
		ID: "pool-bounded", Name: "bounded", CloudProvider: "kubernetes", Status: "active",
		MinSize: 2, MaxSize: 4, DesiredSize: 2, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	request := &storage.ProvisioningRequest{
		ID: "req-outside-bounds", FleetPoolID: pool.ID, Status: "failed", DesiredCount: 5,
	}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatalf("SaveProvisioningRequest: %v", err)
	}
	scaler := &fakeFleetScaler{can: true}
	server.fleetScaler = scaler

	retry := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/req-outside-bounds/retry", nil)
	if retry.Code != http.StatusConflict || !strings.Contains(retry.Body.String(), "max_size 4") {
		t.Fatalf("retry status = %d body=%s, want explanatory 409", retry.Code, retry.Body.String())
	}
	if scaler.calls != 0 {
		t.Fatalf("scaler calls = %d, want none for an out-of-bounds retry", scaler.calls)
	}
	stored, err := store.GetProvisioningRequest(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" {
		t.Fatalf("stored status = %q, want original failed status", stored.Status)
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

// RN-2: cordoning a configured-but-offline generator via PATCH must (a) succeed
// (persist the metadata cordon, not 400 after a partial write), and (b) actually
// take effect when the generator reconnects — the identity-record hook applies the
// persisted metadata cordon to the live drain state.
func TestGeneratorMetadataCordonAppliesOnReconnect(t *testing.T) {
	server, store := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9090", "gen-off:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-a:9090", "gen-a")
	server.runner.setExecutionBackend(backend)
	// Register the reconnect hook the way NewServer does (this test builds the
	// backend after construction, so wire it explicitly).
	backend.setOnIdentityRecorded(func(generatorID string) {
		server.applyMetadataCordonOnReconnect(backend, generatorID)
	})

	// gen-off is configured but its identity was never recorded (offline) ->
	// GeneratorTargetForID("gen-off") == "".
	rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/fleet/generators/gen-off", map[string]any{"cordoned": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("cordon of offline generator status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	meta, err := store.GetGeneratorMetadata("gen-off")
	if err != nil || !meta.Cordoned {
		t.Fatalf("metadata after cordon = %+v err=%v, want cordoned persisted", meta, err)
	}
	// Not enforced yet (offline).
	if got := backend.generatorDrainState("gen-off:9090"); got != "active" {
		t.Fatalf("drain_state before reconnect = %q, want active (not yet mapped)", got)
	}

	// gen-off reconnects: its identity is recorded, firing the hook.
	backend.recordGeneratorIdentity("gen-off:9090", "gen-off")
	if got := backend.generatorDrainState("gen-off:9090"); got != "cordoned" {
		t.Fatalf("drain_state after reconnect = %q, want cordoned (metadata cordon applied)", got)
	}
	// And it's excluded from placement.
	allowed, err := backend.allowedTargetsForTest(&ManagedTest{})
	if err != nil {
		t.Fatalf("allowedTargetsForTest: %v", err)
	}
	for _, tgt := range allowed {
		if tgt == "gen-off:9090" {
			t.Fatalf("cordoned generator still placeable: %#v", allowed)
		}
	}
}

// RN-2 (both directions + draining guard): the reconnect reconcile must apply a
// metadata cordon, clear a stale operator cordon when metadata is un-cordoned, and
// never clobber an operator-owned "draining" state.
func TestApplyMetadataCordonOnReconnectReconcilesBothDirections(t *testing.T) {
	server, store := newTimeslotServer(t)
	backend := newDistributedExecutionBackend([]string{"gen-1:9090", "gen-2:9090", "gen-3:9090"}, time.Second, nil)
	backend.recordGeneratorIdentity("gen-1:9090", "gen-1")
	backend.recordGeneratorIdentity("gen-2:9090", "gen-2")
	backend.recordGeneratorIdentity("gen-3:9090", "gen-3")
	server.runner.setExecutionBackend(backend)

	// gen-1: metadata un-cordoned, live state stale-cordoned -> clear to active.
	if _, err := backend.SetGeneratorDrainStateByID("gen-1", "cordoned"); err != nil {
		t.Fatalf("seed gen-1: %v", err)
	}
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-1", Cordoned: false}); err != nil {
		t.Fatalf("meta gen-1: %v", err)
	}
	server.applyMetadataCordonOnReconnect(backend, "gen-1")
	if got := backend.GeneratorDrainStateByID("gen-1"); got != "active" {
		t.Fatalf("gen-1 = %q, want active (stale cordon cleared)", got)
	}

	// gen-2: metadata cordoned, operator draining -> must NOT be clobbered.
	if _, err := backend.SetGeneratorDrainStateByID("gen-2", "draining"); err != nil {
		t.Fatalf("seed gen-2: %v", err)
	}
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-2", Cordoned: true}); err != nil {
		t.Fatalf("meta gen-2: %v", err)
	}
	server.applyMetadataCordonOnReconnect(backend, "gen-2")
	if got := backend.GeneratorDrainStateByID("gen-2"); got != "draining" {
		t.Fatalf("gen-2 = %q, want draining preserved", got)
	}

	// gen-3: metadata cordoned, live active -> apply the cordon.
	if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-3", Cordoned: true}); err != nil {
		t.Fatalf("meta gen-3: %v", err)
	}
	server.applyMetadataCordonOnReconnect(backend, "gen-3")
	if got := backend.GeneratorDrainStateByID("gen-3"); got != "cordoned" {
		t.Fatalf("gen-3 = %q, want cordoned applied", got)
	}
}
