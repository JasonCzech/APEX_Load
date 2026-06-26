package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// B2 — /auth/policy folds the role catalog + access matrix into one response, and the split
// endpoints still return the same data (kept for API parity).
func TestAuthPolicyFoldsRolesAndMatrix(t *testing.T) {
	store := phase1Store(t)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	get := func(path string, out any) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 body=%s", path, resp.Code, resp.Body.String())
		}
		if err := json.Unmarshal(resp.Body.Bytes(), out); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
	}

	var policy AuthPolicyResponse
	get("/api/v1/auth/policy", &policy)
	if len(policy.Roles) == 0 || len(policy.Resources) == 0 {
		t.Fatalf("policy missing roles/resources: %+v", policy)
	}
	if policy.ProviderModel == "" || policy.Version == "" {
		t.Fatalf("policy missing provider_model/version: %+v", policy)
	}

	// The split endpoints are still served and agree with the folded response.
	var roles AuthRoleCatalogResponse
	get("/api/v1/auth/roles", &roles)
	var matrix AuthAccessMatrixResponse
	get("/api/v1/auth/access-matrix", &matrix)

	if policy.ProviderModel != roles.ProviderModel {
		t.Fatalf("provider_model: policy=%q roles=%q", policy.ProviderModel, roles.ProviderModel)
	}
	if policy.Version != matrix.Version {
		t.Fatalf("version: policy=%q matrix=%q", policy.Version, matrix.Version)
	}
	if len(policy.Roles) != len(roles.Roles) {
		t.Fatalf("roles count: policy=%d roles=%d", len(policy.Roles), len(roles.Roles))
	}
	if len(policy.Resources) != len(matrix.Resources) {
		t.Fatalf("resources count: policy=%d matrix=%d", len(policy.Resources), len(matrix.Resources))
	}
}
