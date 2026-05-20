package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleSystemReadinessReturnsReadyForConfiguredLocalController(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/readiness", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload SystemReadinessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Status != readinessStatusReady {
		t.Fatalf("status = %q, want ready payload=%#v", payload.Status, payload)
	}
	assertReadinessCheck(t, payload, "metadata_store", readinessStatusReady)
	metadata := assertReadinessCheck(t, payload, "metadata_store", readinessStatusReady)
	if metadata.Details["store_type"] != storage.StoreBackendSQLite {
		t.Fatalf("metadata store type = %#v, want sqlite", metadata.Details["store_type"])
	}
	archive := assertReadinessCheck(t, payload, "archive_writer", readinessStatusReady)
	if archive.Details["archive_backend"] != archiveBackendFilesystem {
		t.Fatalf("archive backend = %#v, want filesystem", archive.Details["archive_backend"])
	}
	cache := assertReadinessCheck(t, payload, "cache_backend", readinessStatusReady)
	if cache.Details["cache_backend"] != cacheBackendMemory {
		t.Fatalf("cache backend = %#v, want memory", cache.Details["cache_backend"])
	}
	assertReadinessCheck(t, payload, "generator_rpc", readinessStatusReady)
	assertReadinessCheck(t, payload, "telemetry_fanout", readinessStatusReady)
	assertReadinessCheck(t, payload, "auth_mode", readinessStatusReady)
	assertReadinessCheck(t, payload, "scheduler_state", readinessStatusReady)
}

func TestHandleSystemReadinessReturnsNotReadyWithoutMetadataStore(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/readiness", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusServiceUnavailable, resp.Body.String())
	}
	var payload SystemReadinessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Status != readinessStatusNotReady {
		t.Fatalf("status = %q, want not_ready payload=%#v", payload.Status, payload)
	}
	assertReadinessCheck(t, payload, "metadata_store", readinessStatusNotReady)
}

func TestHandleSystemReadinessReturnsNotReadyWhenDistributedGeneratorsUnreachable(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{{
		Target:      "gen-a:9090",
		GeneratorID: "gen-a",
		Status:      "unreachable",
	}}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/readiness", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusServiceUnavailable, resp.Body.String())
	}
	var payload SystemReadinessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	assertReadinessCheck(t, payload, "generator_rpc", readinessStatusNotReady)
}

func TestHandleSystemReadinessReturnsNotReadyWhenRedisConfiguredButUnavailable(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_CACHE_BACKEND", "redis")
	t.Setenv("APEXLOAD_REDIS_URL", "redis://127.0.0.1:1/0")
	t.Setenv("APEXLOAD_REDIS_TIMEOUT", "10ms")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/readiness", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusServiceUnavailable, resp.Body.String())
	}
	var payload SystemReadinessResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	cache := assertReadinessCheck(t, payload, "cache_backend", readinessStatusNotReady)
	if cache.Details["cache_backend"] != cacheBackendRedis {
		t.Fatalf("cache backend = %#v, want redis", cache.Details["cache_backend"])
	}
}

func assertReadinessCheck(t *testing.T, payload SystemReadinessResponse, name string, status string) SystemReadinessCheck {
	t.Helper()
	for _, check := range payload.Checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("%s status = %q, want %q check=%#v", name, check.Status, status, check)
			}
			return check
		}
	}
	t.Fatalf("missing readiness check %q in %#v", name, payload.Checks)
	return SystemReadinessCheck{}
}
