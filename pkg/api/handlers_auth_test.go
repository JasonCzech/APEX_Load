package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGetAuthStatusReturnsDisabledByDefault(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if payload.Enabled {
		t.Fatal("expected auth to be disabled by default")
	}
	if payload.Authenticated {
		t.Fatal("expected no authenticated user when auth is disabled")
	}
	if payload.Mode != authModeDisabled {
		t.Fatalf("mode = %q, want %q", payload.Mode, authModeDisabled)
	}
}

func TestHandleGetAuthStatusReturnsUnauthenticatedWhenTrustedHeadersEnabled(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := enableTrustedHeaderAuth(t, server)
	settings.Auth.ProviderName = "Entra ID"
	settings.Auth.LoginURL = "https://login.example.test/oauth2/start"
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !payload.Enabled {
		t.Fatal("expected auth to be enabled")
	}
	if payload.ProviderName != "Entra ID" {
		t.Fatalf("provider name = %q, want Entra ID", payload.ProviderName)
	}
	if payload.LoginURL != "https://login.example.test/oauth2/start" {
		t.Fatalf("login url = %q, want configured login url", payload.LoginURL)
	}
	if payload.Authenticated {
		t.Fatal("expected request without headers to remain unauthenticated")
	}
	if payload.User != nil {
		t.Fatalf("expected no user, got %#v", payload.User)
	}
	if payload.Mode != authModeTrustedHeaders {
		t.Fatalf("mode = %q, want %q", payload.Mode, authModeTrustedHeaders)
	}
}

func TestHandleGetAuthStatusReturnsTrustedHeaderIdentity(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("X-Auth-Request-User", "alice")
	req.Header.Set("X-Auth-Request-Name", "Alice Admin")
	req.Header.Set("X-Auth-Request-Email", "alice@example.com")
	req.Header.Set("X-Stormrunner-Roles", RoleSystemAdmin+","+RoleViewer)
	req.Header.Set("X-Stormrunner-Project-Roles", "project-a="+RoleEditor+"|"+RoleViewer+";project-b="+RoleProjectAdmin)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth status: %v", err)
	}
	if !payload.Authenticated || payload.User == nil {
		t.Fatalf("expected authenticated user, got %#v", payload)
	}
	if payload.User.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", payload.User.Subject)
	}
	if payload.User.DisplayName != "Alice Admin" {
		t.Fatalf("display name = %q, want Alice Admin", payload.User.DisplayName)
	}
	if payload.User.Email != "alice@example.com" {
		t.Fatalf("email = %q, want alice@example.com", payload.User.Email)
	}
	if len(payload.User.GlobalRoles) != 2 || payload.User.GlobalRoles[0] != RoleSystemAdmin || payload.User.GlobalRoles[1] != RoleViewer {
		t.Fatalf("global roles = %#v, want [system_admin viewer]", payload.User.GlobalRoles)
	}
	if got := payload.User.ProjectRoles["project-a"]; len(got) != 2 || got[0] != RoleEditor || got[1] != RoleViewer {
		t.Fatalf("project-a roles = %#v, want [editor viewer]", got)
	}
	if got := payload.User.ProjectRoles["project-b"]; len(got) != 1 || got[0] != RoleProjectAdmin {
		t.Fatalf("project-b roles = %#v, want [project_admin]", got)
	}
}

func TestHandleGetAuthRoleCatalogReturnsBuiltInRoles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/roles", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthRoleCatalogResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth role catalog: %v", err)
	}
	if payload.ProviderModel != "upstream_gateway" {
		t.Fatalf("provider model = %q, want upstream_gateway", payload.ProviderModel)
	}
	if len(payload.Roles) != 4 {
		t.Fatalf("role count = %d, want 4", len(payload.Roles))
	}
	if payload.Roles[0].ID != RoleSystemAdmin || payload.Roles[0].Name != "System Admin" {
		t.Fatalf("first role = %#v, want system admin descriptor", payload.Roles[0])
	}
	if len(payload.Roles[0].Capabilities) == 0 {
		t.Fatalf("expected system admin capabilities, got %#v", payload.Roles[0].Capabilities)
	}
	if payload.Roles[3].ID != RoleViewer {
		t.Fatalf("last role = %#v, want viewer descriptor", payload.Roles[3])
	}
}

func TestHandleGetAuthAccessMatrixReturnsResourcePolicies(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/access-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthAccessMatrixResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth access matrix: %v", err)
	}
	if payload.Version != "trusted_headers_v1" {
		t.Fatalf("version = %q, want trusted_headers_v1", payload.Version)
	}
	if len(payload.Resources) < 6 {
		t.Fatalf("resource count = %d, want >= 6", len(payload.Resources))
	}
	if payload.Resources[0].ID != "system_configuration" {
		t.Fatalf("first resource = %#v, want system configuration", payload.Resources[0])
	}
	if len(payload.Resources[0].Operations) != 2 {
		t.Fatalf("system configuration operations = %#v, want 2 operations", payload.Resources[0].Operations)
	}
	if payload.Resources[0].Operations[1].GlobalRoles[0] != RoleSystemAdmin {
		t.Fatalf("manage system roles = %#v, want system admin", payload.Resources[0].Operations[1].GlobalRoles)
	}
	if payload.Resources[1].Operations[1].ProjectRoles[0] != RoleProjectAdmin {
		t.Fatalf("project governance roles = %#v, want project admin project scope", payload.Resources[1].Operations[1].ProjectRoles)
	}
}

func TestHandleGetAuthEffectiveAccessReturnsOpenAccessWhenDisabled(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth effective access: %v", err)
	}
	if payload.Enabled {
		t.Fatal("expected disabled auth mode")
	}
	if payload.Authenticated {
		t.Fatal("expected no authenticated user in disabled mode")
	}
	if !payload.Resources[0].Operations[0].Allowed || payload.Resources[0].Operations[0].GrantSource != "disabled" {
		t.Fatalf("first operation = %#v, want allowed via disabled mode", payload.Resources[0].Operations[0])
	}
}

func TestHandleGetAuthEffectiveAccessEvaluatesProjectRoles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access?project_id=project-a", nil)
	req.Header.Set("X-Auth-Request-User", "editor-user")
	req.Header.Set("X-Stormrunner-Project-Roles", "project-a="+RoleEditor)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal auth effective access: %v", err)
	}
	if !payload.Enabled || !payload.Authenticated {
		t.Fatalf("expected enabled authenticated payload, got %#v", payload)
	}
	if payload.ProjectID != "project-a" {
		t.Fatalf("project id = %q, want project-a", payload.ProjectID)
	}
	var manageTests AuthEffectiveAccessOperation
	var manageSystem AuthEffectiveAccessOperation
	for _, resource := range payload.Resources {
		for _, operation := range resource.Operations {
			switch operation.ID {
			case "manage_tests":
				manageTests = operation
			case "manage_system":
				manageSystem = operation
			}
		}
	}
	if !manageTests.Allowed || manageTests.GrantSource != "project" {
		t.Fatalf("manage tests = %#v, want allowed via project role", manageTests)
	}
	if manageSystem.Allowed {
		t.Fatalf("manage system = %#v, want denied for project editor", manageSystem)
	}
}
