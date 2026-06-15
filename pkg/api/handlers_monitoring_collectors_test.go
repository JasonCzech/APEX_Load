package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestMonitoringCollectorLifecycle(t *testing.T) {
	server, store := newTimeslotServer(t)

	// Validation.
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors", map[string]any{"project_id": "p"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing name status = %d, want 400", rec.Code)
	}
	if rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors", map[string]any{"name": "c"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing project status = %d, want 400", rec.Code)
	}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors",
		map[string]any{"project_id": "proj-a", "name": "edge-eu", "allowed_metric_packs": []string{"oracle-db"}})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var createResp struct {
		Collector storage.MonitoringCollector `json:"collector"`
		Token     string                      `json:"token"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if createResp.Token == "" {
		t.Fatal("create must return a one-time token")
	}
	if createResp.Collector.ID == "" || createResp.Collector.Health != "unreachable" {
		t.Fatalf("created collector = %+v", createResp.Collector)
	}
	id := createResp.Collector.ID

	// The token hash must never be serialized.
	if strings.Contains(create.Body.String(), "token_hash") {
		t.Fatal("token_hash must not appear in API responses")
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/monitoring/collectors?project_id=proj-a", nil)
	var listResp struct {
		Collectors []storage.MonitoringCollector `json:"collectors"`
		Count      int                           `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Fatalf("list count = %d, want 1", listResp.Count)
	}

	// PATCH: valid health + invalid health.
	if rec := timeslotRequest(t, server, http.MethodPatch, "/api/v1/monitoring/collectors/"+id, map[string]any{"health": "bogus"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid health status = %d, want 400", rec.Code)
	}
	patch := timeslotRequest(t, server, http.MethodPatch, "/api/v1/monitoring/collectors/"+id, map[string]any{"health": "healthy", "description": "eu edge"})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", patch.Code, patch.Body.String())
	}

	// Rotate token: new token differs and the stored hash changes.
	hashBefore := storedCollectorHash(t, store, id)
	rotate := timeslotRequest(t, server, http.MethodPost, "/api/v1/monitoring/collectors/"+id+"/rotate-token", nil)
	if rotate.Code != http.StatusOK {
		t.Fatalf("rotate status = %d", rotate.Code)
	}
	var rotateResp struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rotate.Body.Bytes(), &rotateResp)
	if rotateResp.Token == "" || rotateResp.Token == createResp.Token {
		t.Fatalf("rotate must return a new token, got %q (orig %q)", rotateResp.Token, createResp.Token)
	}
	if hashAfter := storedCollectorHash(t, store, id); hashAfter == hashBefore {
		t.Fatal("token hash must change after rotation")
	}

	if del := timeslotRequest(t, server, http.MethodDelete, "/api/v1/monitoring/collectors/"+id, nil); del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
}

func storedCollectorHash(t *testing.T, store storage.Store, id string) string {
	t.Helper()
	c, err := store.GetMonitoringCollector(id)
	if err != nil {
		t.Fatalf("load collector: %v", err)
	}
	return c.TokenHash
}

func TestMonitoringCollectorRouteAccessRequirements(t *testing.T) {
	for _, tc := range []struct {
		method    string
		path      string
		wantRoles []string
	}{
		{http.MethodPost, "/api/v1/monitoring/collectors", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/monitoring/collectors", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPatch, "/api/v1/monitoring/collectors/c1", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodDelete, "/api/v1/monitoring/collectors/c1", []string{RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/monitoring/collectors/c1/rotate-token", []string{RoleProjectAdmin, RoleSystemAdmin}},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok || len(requirement.GlobalRoles) != len(tc.wantRoles) {
				t.Fatalf("%s %s GlobalRoles = %#v, want %#v (ok=%v)", tc.method, tc.path, requirement.GlobalRoles, tc.wantRoles, ok)
			}
		})
	}
}
