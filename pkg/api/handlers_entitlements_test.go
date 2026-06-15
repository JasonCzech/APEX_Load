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
}

func TestEntitlementsRouteAccess(t *testing.T) {
	for _, p := range []string{"/api/v1/system/entitlements", "/api/v1/system/entitlements/usage"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		requirement, ok := accessRequirementForRequest(req)
		if !ok || len(requirement.GlobalRoles) != 4 {
			t.Fatalf("%s GlobalRoles = %#v (ok=%v), want all 4 read roles", p, requirement.GlobalRoles, ok)
		}
	}
}
