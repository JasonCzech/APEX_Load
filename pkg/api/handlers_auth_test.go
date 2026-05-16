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
	enableTrustedHeaderAuth(t, server)
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