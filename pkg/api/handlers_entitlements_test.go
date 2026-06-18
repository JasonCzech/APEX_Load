package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEntitlementsEndpoints(t *testing.T) {
	server, _ := newTimeslotServer(t)

	ent := timeslotRequest(t, server, http.MethodGet, "/api/v1/system/entitlements", nil)
	if ent.Code != http.StatusOK {
		t.Fatalf("entitlements status = %d body=%s", ent.Code, ent.Body.String())
	}
	var entResp map[string]any
	if err := json.Unmarshal(ent.Body.Bytes(), &entResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entResp["edition"] != "self-hosted" {
		t.Fatalf("edition = %v, want self-hosted", entResp["edition"])
	}
	bundles, _ := entResp["protocol_bundles"].(map[string]any)
	if bundles == nil || bundles["count"].(float64) <= 0 {
		t.Fatalf("expected protocol bundles, got %v", entResp["protocol_bundles"])
	}
	limits, _ := entResp["limits"].(map[string]any)
	if limits == nil {
		t.Fatal("expected limits object")
	}
	if _, ok := limits["max_concurrent_tests"]; !ok {
		t.Fatal("expected limits.max_concurrent_tests")
	}

	usage := timeslotRequest(t, server, http.MethodGet, "/api/v1/system/entitlements/usage", nil)
	if usage.Code != http.StatusOK {
		t.Fatalf("usage status = %d", usage.Code)
	}
	var usageResp map[string]any
	_ = json.Unmarshal(usage.Body.Bytes(), &usageResp)
	ct, _ := usageResp["concurrent_tests"].(map[string]any)
	if ct == nil {
		t.Fatal("expected concurrent_tests object")
	}
	if _, ok := ct["used"]; !ok {
		t.Fatal("expected concurrent_tests.used")
	}
	if _, ok := ct["limit"]; !ok {
		t.Fatal("expected concurrent_tests.limit")
	}

	// Richer read-only breakdowns: per-limit, per-bundle consumption, per project.
	limitsArr, ok := usageResp["limits"].([]any)
	if !ok || len(limitsArr) == 0 {
		t.Fatalf("expected non-empty limits array, got %v", usageResp["limits"])
	}
	firstLimit, _ := limitsArr[0].(map[string]any)
	if firstLimit["key"] == nil || firstLimit["scope"] != "global" {
		t.Fatalf("limit view missing key/scope: %v", firstLimit)
	}
	if pb, ok := usageResp["protocol_bundles"].([]any); !ok || len(pb) == 0 {
		t.Fatalf("expected non-empty protocol_bundles consumption array, got %v", usageResp["protocol_bundles"])
	}
	if _, ok := usageResp["by_project"].([]any); !ok {
		t.Fatalf("expected by_project array, got %v", usageResp["by_project"])
	}
}

func TestProjectEntitlementsUsage(t *testing.T) {
	server, _ := newTimeslotServer(t)
	rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/projects/default/entitlements/usage", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["project_id"] == nil {
		t.Fatal("expected project_id")
	}
	limits, ok := resp["limits"].([]any)
	if !ok || len(limits) == 0 {
		t.Fatalf("expected limits array, got %v", resp["limits"])
	}
	lv, _ := limits[0].(map[string]any)
	if lv["scope"] != "project" {
		t.Fatalf("expected project-scoped limit, got %v", lv["scope"])
	}
	if _, ok := resp["protocol_bundles"].([]any); !ok {
		t.Fatalf("expected protocol_bundles array, got %v", resp["protocol_bundles"])
	}
}

func TestEntitlementsRouteAccess(t *testing.T) {
	for _, p := range []string{"/api/v1/system/entitlements", "/api/v1/system/entitlements/usage"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		requirement, ok := accessRequirementForRequest(req)
		if !ok || len(requirement.GlobalRoles) != 4 {
			t.Fatalf("%s GlobalRoles = %#v (ok=%v), want all 4 read roles", p, requirement.GlobalRoles, ok)
		}
	}
	// The per-project usage route is project-scoped (project read roles), not global.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/p1/entitlements/usage", nil)
	requirement, ok := accessRequirementForRequest(req)
	if !ok || len(requirement.ProjectRoles) != 3 {
		t.Fatalf("project entitlements ProjectRoles = %#v (ok=%v), want 3 project read roles", requirement.ProjectRoles, ok)
	}
}
