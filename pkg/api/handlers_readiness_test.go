package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/jasonczech/apexload/pkg/storage"
)

// readinessTestStaticFS returns an in-memory dashboard asset filesystem so the
// readiness suite is deterministic regardless of whether `npm run build` has
// populated the compile-time embed (which is gitignored).
func readinessTestStaticFS() fstest.MapFS {
	return fstest.MapFS{
		"static/index.html":    {Data: []byte("<!doctype html><title>APEX Load</title>")},
		"static/assets/app.js": {Data: []byte("export default null")},
	}
}

func TestHandleSystemReadinessReturnsReadyForConfiguredLocalController(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	server.staticFS = readinessTestStaticFS()
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
	platform := assertReadinessCheck(t, payload, "platform_integrations", readinessStatusReady)
	if platform.Details["grpc_transport"] == "" {
		t.Fatalf("platform grpc_transport missing: %#v", platform.Details)
	}
	if _, ok := platform.Details["secret_resolver"].(map[string]any); !ok {
		t.Fatalf("platform secret_resolver details missing: %#v", platform.Details)
	}
	if _, ok := platform.Details["object_archive"].(map[string]any); !ok {
		t.Fatalf("platform object_archive details missing: %#v", platform.Details)
	}
	if _, ok := platform.Details["prometheus_scaling_source"].(map[string]any); !ok {
		t.Fatalf("platform prometheus_scaling_source details missing: %#v", platform.Details)
	}
	if _, ok := platform.Details["azure_identity"].(map[string]any); !ok {
		t.Fatalf("platform azure_identity details missing: %#v", platform.Details)
	}
	if _, ok := platform.Details["dashboard_static_assets"].(map[string]any); !ok {
		t.Fatalf("platform dashboard_static_assets details missing: %#v", platform.Details)
	}
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
	generatorRPC := assertReadinessCheck(t, payload, "generator_rpc", readinessStatusNotReady)
	if generatorRPC.Details["grpc_transport"] == "" {
		t.Fatalf("generator_rpc grpc_transport missing: %#v", generatorRPC.Details)
	}
}

func TestPlatformIntegrationsReadinessReportsAzureAndPrometheusSettings(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "azure_blob")
	t.Setenv("APEXLOAD_ARCHIVE_CONTAINER", "apexload-archives")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "https://apexload.vault.azure.net")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_AUTH", "azure_identity")
	t.Setenv("AZURE_CLIENT_ID", "client-id")
	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", "/var/run/secrets/azure/tokens/azure-identity-token")
	t.Setenv("AZURE_TENANT_ID", "tenant-id")
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "http://prometheus-operated.monitoring.svc:9090")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	server.staticFS = readinessTestStaticFS()
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	check := server.platformIntegrationsReadiness()
	if check.Status != readinessStatusReady {
		t.Fatalf("status = %q, want ready check=%#v", check.Status, check)
	}
	secretResolver := check.Details["secret_resolver"].(map[string]any)
	if secretResolver["azure_key_vault_configured"] != true || secretResolver["azure_key_vault_auth"] != "azure_identity" {
		t.Fatalf("secret resolver details = %#v", secretResolver)
	}
	objectArchive := check.Details["object_archive"].(map[string]any)
	if objectArchive["backend"] != archiveBackendAzureBlob || objectArchive["container"] != "apexload-archives" {
		t.Fatalf("object archive details = %#v", objectArchive)
	}
	azureIdentity := check.Details["azure_identity"].(map[string]any)
	if azureIdentity["client_id_configured"] != true || azureIdentity["federated_token_file"] != true || azureIdentity["tenant_id_configured"] != true {
		t.Fatalf("azure identity details = %#v", azureIdentity)
	}
	prometheus := check.Details["prometheus_scaling_source"].(map[string]any)
	if prometheus["settings_enabled"] != true || prometheus["base_url"] != "http://prometheus-operated.monitoring.svc:9090" {
		t.Fatalf("prometheus details = %#v", prometheus)
	}
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

func TestPlatformIntegrationsReadinessDegradedWhenDashboardMissing(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	// No dashboard assets staged: the embedded build is absent.
	server.staticFS = fstest.MapFS{}

	check := server.platformIntegrationsReadiness()
	if check.Status != readinessStatusDegraded {
		t.Fatalf("status = %q, want degraded when dashboard assets are missing", check.Status)
	}
	dashboard, ok := check.Details["dashboard_static_assets"].(map[string]any)
	if !ok {
		t.Fatalf("dashboard_static_assets details missing: %#v", check.Details)
	}
	if dashboard["index_html"] != false {
		t.Fatalf("dashboard index_html = %#v, want false", dashboard["index_html"])
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
