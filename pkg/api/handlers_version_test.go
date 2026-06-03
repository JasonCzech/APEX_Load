package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/buildinfo"
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

	var payload buildinfo.Info
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Version == "" {
		t.Errorf("version is empty, want a value or 'dev' fallback; payload=%#v", payload)
	}
	if payload.GoVersion == "" {
		t.Errorf("go_version is empty; payload=%#v", payload)
	}
	if payload.Environment != "local" {
		t.Errorf("environment = %q, want %q", payload.Environment, "local")
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
}
