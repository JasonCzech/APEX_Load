package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleSystemVersionReturnsBuildInfoWithoutAuth(t *testing.T) {
	// No auth header is supplied; the version endpoint must be reachable so the
	// dashboard can render the indicator before/independent of authentication.
	t.Setenv("APEXLOAD_ENVIRONMENT", "local")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/version", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload VersionResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Version == "" {
		t.Errorf("version is empty, want a value or 'dev' fallback; payload=%#v", payload)
	}
	if payload.DisplayVersion == "" {
		t.Errorf("display_version is empty, want a user-facing label; payload=%#v", payload)
	}
	if payload.GoVersion == "" {
		t.Errorf("go_version is empty; payload=%#v", payload)
	}
	if payload.Environment != "local" {
		t.Errorf("environment = %q, want %q", payload.Environment, "local")
	}
	if payload.Deployment.Kind != "server" {
		t.Errorf("deployment.kind = %q, want server for wide local bind", payload.Deployment.Kind)
	}
	if payload.Deployment.BindScope != "wide" {
		t.Errorf("deployment.bind_scope = %q, want wide", payload.Deployment.BindScope)
	}
	if !payload.Deployment.AuthEnforced {
		t.Errorf("deployment.auth_enforced = false, want true")
	}
	if payload.Deployment.DesktopAvailable {
		t.Errorf("deployment.desktop_available = true, want false by default")
	}
}

func TestSystemVersionReflectsDesktopPromotionPolicy(t *testing.T) {
	t.Setenv("APEXLOAD_ENVIRONMENT", "kubernetes")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.ClientApp.Availability = clientAppEnabled
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/version", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload VersionResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Deployment.Kind != "kubernetes" {
		t.Fatalf("deployment.kind = %q, want kubernetes", payload.Deployment.Kind)
	}
	if !payload.Deployment.DesktopAvailable {
		t.Fatalf("deployment.desktop_available = false, want true")
	}
}

func TestHandleHealthCheckIncludesEnvironment(t *testing.T) {
	t.Setenv("APEXLOAD_ENVIRONMENT", "docker")

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["environment"] != "docker" {
		t.Errorf("environment = %#v, want docker", payload["environment"])
	}
	if _, ok := payload["version"]; !ok {
		t.Errorf("health payload missing version key: %#v", payload)
	}
	if _, ok := payload["display_version"]; !ok {
		t.Errorf("health payload missing display_version key: %#v", payload)
	}
}
