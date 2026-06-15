package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

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
