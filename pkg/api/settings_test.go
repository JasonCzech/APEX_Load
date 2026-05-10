package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestSystemSettingsAPIAndPersistence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("initial GET status = %d, want %d", getResp.Code, http.StatusOK)
	}

	var initial SystemSettings
	if err := json.Unmarshal(getResp.Body.Bytes(), &initial); err != nil {
		t.Fatalf("unmarshal initial settings: %v", err)
	}
	if initial.General.MaxConcurrentTests != 10 {
		t.Fatalf("initial max concurrent = %d, want 10", initial.General.MaxConcurrentTests)
	}

	maxErr := 1.5
	maxP99 := 1800.0
	update := SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":9090",
			MaxConcurrentTests:     3,
			MetricsIntervalSeconds: 2,
		},
		Thresholds: ThresholdSettings{
			MaxErrorRatePct: &maxErr,
			MaxP99LatencyMs: &maxP99,
		},
	}
	body, _ := json.Marshal(update)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	putResp := httptest.NewRecorder()
	server.mux.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d body=%s", putResp.Code, http.StatusOK, putResp.Body.String())
	}

	getResp2 := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp2, getReq)
	if getResp2.Code != http.StatusOK {
		t.Fatalf("second GET status = %d, want %d", getResp2.Code, http.StatusOK)
	}
	var updated SystemSettings
	if err := json.Unmarshal(getResp2.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated settings: %v", err)
	}
	if updated.General.APIEndpoint != ":9090" {
		t.Fatalf("api endpoint = %q, want :9090", updated.General.APIEndpoint)
	}
	if updated.General.MaxConcurrentTests != 3 {
		t.Fatalf("max concurrent = %d, want 3", updated.General.MaxConcurrentTests)
	}
	if updated.General.MetricsIntervalSeconds != 2 {
		t.Fatalf("metrics interval = %d, want 2", updated.General.MetricsIntervalSeconds)
	}
	if updated.Thresholds.MaxErrorRatePct == nil || *updated.Thresholds.MaxErrorRatePct != maxErr {
		t.Fatalf("max error rate = %#v, want %v", updated.Thresholds.MaxErrorRatePct, maxErr)
	}
	if updated.Thresholds.MaxP99LatencyMs == nil || *updated.Thresholds.MaxP99LatencyMs != maxP99 {
		t.Fatalf("max p99 latency = %#v, want %v", updated.Thresholds.MaxP99LatencyMs, maxP99)
	}

	runner := NewTestRunner(10, NewHub(), store)
	persisted := runner.GetSystemSettings()
	if persisted.General.MaxConcurrentTests != 3 {
		t.Fatalf("persisted max concurrent = %d, want 3", persisted.General.MaxConcurrentTests)
	}
	if persisted.General.MetricsIntervalSeconds != 2 {
		t.Fatalf("persisted metrics interval = %d, want 2", persisted.General.MetricsIntervalSeconds)
	}
}

func TestSystemSettingsValidation(t *testing.T) {
	server := NewServer(ServerConfig{Store: nil, MaxConcurrent: 10, Addr: ":8080"})

	invalid := SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     0,
			MetricsIntervalSeconds: 1,
		},
	}
	body, _ := json.Marshal(invalid)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
}

func TestStartTestRespectsConfiguredMaxConcurrent(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)

	_, err := runner.UpdateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     1,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
	})
	if err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests["running"] = &ManagedTest{ID: "running", Status: "RUNNING"}
	runner.tests["queued"] = &ManagedTest{ID: "queued", Status: "QUEUED"}
	runner.mu.Unlock()

	err = runner.StartTest("queued")
	if err == nil {
		t.Fatal("expected StartTest to fail when max concurrent limit is reached")
	}
}
