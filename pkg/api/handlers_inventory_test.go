package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type stubInventoryBackend struct {
	entries []GeneratorInventoryEntry
}

func (stubInventoryBackend) Run(tr *TestRunner, test *ManagedTest) {}

func (s stubInventoryBackend) SystemInventory(ctx context.Context) []GeneratorInventoryEntry {
	return append([]GeneratorInventoryEntry(nil), s.entries...)
}

func TestHandleGetSystemInventoryReturnsControllerAndGenerators(t *testing.T) {
	safetyExitAt := time.Date(2026, time.August, 11, 15, 4, 5, 0, time.UTC)
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{{
		Target:            "apexload-generator-0.apexload-generator:9090",
		GeneratorID:       "apexload-generator-0",
		Region:            "us-east",
		Status:            "connected",
		AvailableCapacity: 9600,
		ActiveVUsers:      400,
		CPUPercent:        22.5,
		MemoryBytes:       96 * 1024 * 1024,
		GoroutineCount:    812,
		CPULimitCores:     2,
		MemoryLimitBytes:  2 * 1024 * 1024 * 1024,
		MaxVUsers:         10000,
		LastSafetyExit: &GeneratorSafetyExitEvidence{
			TestID: "generation-zero-run", LifecycleState: "FAILED",
			Reason: "test_failure_unquiesced", RequestedAt: safetyExitAt, OutstandingExecutions: 2,
		},
		SafetyExits: []GeneratorSafetyExitEvidence{
			{TestID: "lost-run", ExecutionGeneration: 7, LifecycleState: "FAILED", Reason: "controller_supervision_lost", RequestedAt: safetyExitAt.Add(-2 * time.Second), OutstandingExecutions: 2},
			{TestID: "generation-zero-run", LifecycleState: "FAILED", Reason: "controller_stop_unquiesced", RequestedAt: safetyExitAt.Add(-time.Second), OutstandingExecutions: 1},
			{TestID: "generation-zero-run", LifecycleState: "FAILED", Reason: "test_failure_unquiesced", RequestedAt: safetyExitAt, OutstandingExecutions: 2},
		},
	}}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/inventory", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload SystemInventoryResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ExecutionMode != "distributed" {
		t.Fatalf("execution_mode = %q, want distributed", payload.ExecutionMode)
	}
	if payload.Controller.Status != "healthy" {
		t.Fatalf("controller status = %q, want healthy", payload.Controller.Status)
	}
	if payload.Controller.Address != ":8080" {
		t.Fatalf("controller address = %q, want :8080", payload.Controller.Address)
	}
	if payload.DataPlatform.ArchiveBackend != archiveBackendFilesystem {
		t.Fatalf("archive backend = %q, want filesystem", payload.DataPlatform.ArchiveBackend)
	}
	if payload.DataPlatform.CacheBackend != cacheBackendMemory {
		t.Fatalf("cache backend = %q, want memory", payload.DataPlatform.CacheBackend)
	}
	if len(payload.Generators) != 1 {
		t.Fatalf("generator count = %d, want 1", len(payload.Generators))
	}
	if payload.Generators[0].GeneratorID != "apexload-generator-0" {
		t.Fatalf("generator_id = %q, want apexload-generator-0", payload.Generators[0].GeneratorID)
	}
	if payload.Generators[0].Region != "us-east" {
		t.Fatalf("region = %q, want us-east", payload.Generators[0].Region)
	}
	if payload.Generators[0].AvailableCapacity != 9600 {
		t.Fatalf("available_capacity = %d, want 9600", payload.Generators[0].AvailableCapacity)
	}
	if payload.Generators[0].DrainState != "active" {
		t.Fatalf("drain_state = %q, want active", payload.Generators[0].DrainState)
	}
	if payload.Generators[0].CapacityState != "available" {
		t.Fatalf("capacity_state = %q, want available", payload.Generators[0].CapacityState)
	}
	if payload.Generators[0].HealthScore != 100 {
		t.Fatalf("health_score = %d, want 100", payload.Generators[0].HealthScore)
	}
	if payload.Generators[0].Version != "unknown" {
		t.Fatalf("version = %q, want unknown", payload.Generators[0].Version)
	}
	if payload.Generators[0].CPULimitCores != 2 {
		t.Fatalf("cpu_limit_cores = %v, want 2", payload.Generators[0].CPULimitCores)
	}
	if payload.Generators[0].MemoryLimitBytes != 2*1024*1024*1024 {
		t.Fatalf("memory_limit_bytes = %d, want 2147483648", payload.Generators[0].MemoryLimitBytes)
	}
	if payload.Generators[0].MaxVUsers != 10000 {
		t.Fatalf("max_vusers = %d, want 10000", payload.Generators[0].MaxVUsers)
	}
	evidence := payload.Generators[0].LastSafetyExit
	if evidence == nil || evidence.TestID != "generation-zero-run" || evidence.ExecutionGeneration != 0 ||
		evidence.LifecycleState != "FAILED" || evidence.Reason != "test_failure_unquiesced" ||
		evidence.OutstandingExecutions != 2 || !evidence.RequestedAt.Equal(safetyExitAt) {
		t.Fatalf("last_safety_exit = %+v, want exact durable failure evidence", evidence)
	}
	if history := payload.Generators[0].SafetyExits; len(history) != 3 || history[0].TestID != "lost-run" ||
		history[0].ExecutionGeneration != 7 || history[0].Reason != "controller_supervision_lost" ||
		history[1].TestID != history[2].TestID || history[1].ExecutionGeneration != 0 || history[2].ExecutionGeneration != 0 ||
		history[1].Reason == history[2].Reason || history[1].RequestedAt.Equal(history[2].RequestedAt) {
		t.Fatalf("safety_exits = %+v, want both same-ID generation-zero incidents", history)
	}
}

func TestNormalizeGeneratorInventoryPreservesFreshEmptyCapabilities(t *testing.T) {
	fresh := normalizeGeneratorInventoryEntry(GeneratorInventoryEntry{
		Target:             "gen-jvm:9090",
		Status:             "connected",
		SupportedProtocols: []string{},
		CompatibleFeatures: []string{},
		Labels:             map[string]string{"runtime": "java_vuser"},
	})
	if len(fresh.SupportedProtocols) != 0 || len(fresh.CompatibleFeatures) != 0 {
		t.Fatalf("fresh capabilities = protocols:%#v features:%#v, want authoritative empties", fresh.SupportedProtocols, fresh.CompatibleFeatures)
	}

	provisional := normalizeGeneratorInventoryEntry(GeneratorInventoryEntry{
		Target: "gen-jvm:9090",
		Status: "unreachable",
	})
	if len(provisional.SupportedProtocols) == 0 || len(provisional.CompatibleFeatures) == 0 {
		t.Fatalf("provisional capabilities = protocols:%#v features:%#v, want controller bootstrap defaults", provisional.SupportedProtocols, provisional.CompatibleFeatures)
	}
}

func TestHandleGetSystemServicesReturnsApexSurfaces(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", ProxyAddr: ":8081"})
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{{
		Target:            "apexload-generator-0.apexload-generator:9090",
		GeneratorID:       "apexload-generator-0",
		Region:            "us-east",
		Status:            "connected",
		Version:           "0.1.0",
		AvailableCapacity: 9600,
		ActiveVUsers:      400,
		MaxVUsers:         10000,
	}}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/services", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload SystemServicesResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.GeneratedAt.IsZero() {
		t.Fatal("generated_at is zero")
	}
	if len(payload.Services) != 3 {
		t.Fatalf("service count = %d, want 3: %#v", len(payload.Services), payload.Services)
	}

	services := map[string]SystemServiceEntry{}
	for _, service := range payload.Services {
		services[service.ID] = service
	}

	controller := services["controller-api"]
	if controller.Name != "Controller API" || controller.Kind != "backend" || controller.Status != "running" {
		t.Fatalf("controller service = %#v, want running backend controller", controller)
	}
	if controller.Address != ":8080" {
		t.Fatalf("controller address = %q, want :8080", controller.Address)
	}
	if controller.URL != "http://127.0.0.1:8080" {
		t.Fatalf("controller url = %q, want http://127.0.0.1:8080", controller.URL)
	}
	if controller.Metadata["execution_mode"] != "distributed" {
		t.Fatalf("controller execution_mode metadata = %#v, want distributed", controller.Metadata["execution_mode"])
	}

	recorder := services["recorder-proxy"]
	if recorder.Name != "Recorder Proxy" || recorder.Kind != "backend" || recorder.Status != "idle" {
		t.Fatalf("recorder service = %#v, want idle backend recorder", recorder)
	}
	if recorder.Address != ":8081" {
		t.Fatalf("recorder address = %q, want :8081", recorder.Address)
	}
	if recorder.URL != "http://127.0.0.1:8081" {
		t.Fatalf("recorder url = %q, want http://127.0.0.1:8081", recorder.URL)
	}

	generator := services["generator:apexload-generator-0"]
	if generator.Name != "apexload-generator-0" || generator.Kind != "backend" || generator.Status != "connected" {
		t.Fatalf("generator service = %#v, want connected backend generator", generator)
	}
	if generator.Address != "apexload-generator-0.apexload-generator:9090" {
		t.Fatalf("generator address = %q, want generator target", generator.Address)
	}
	if generator.Metadata["role"] != "generator" {
		t.Fatalf("generator role metadata = %#v, want generator", generator.Metadata["role"])
	}
}
