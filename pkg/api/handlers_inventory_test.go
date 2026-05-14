package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubInventoryBackend struct {
	entries []GeneratorInventoryEntry
}

func (stubInventoryBackend) Run(tr *TestRunner, test *ManagedTest) {}

func (s stubInventoryBackend) SystemInventory(ctx context.Context) []GeneratorInventoryEntry {
	return append([]GeneratorInventoryEntry(nil), s.entries...)
}

func TestHandleGetSystemInventoryReturnsControllerAndGenerators(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{{
		Target:            "stormrunner-generator-0.stormrunner-generator:9090",
		GeneratorID:       "stormrunner-generator-0",
		Status:            "connected",
		AvailableCapacity: 9600,
		ActiveVUsers:      400,
		CPUPercent:        22.5,
		MemoryBytes:       96 * 1024 * 1024,
		GoroutineCount:    812,
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
	if len(payload.Generators) != 1 {
		t.Fatalf("generator count = %d, want 1", len(payload.Generators))
	}
	if payload.Generators[0].GeneratorID != "stormrunner-generator-0" {
		t.Fatalf("generator_id = %q, want stormrunner-generator-0", payload.Generators[0].GeneratorID)
	}
	if payload.Generators[0].AvailableCapacity != 9600 {
		t.Fatalf("available_capacity = %d, want 9600", payload.Generators[0].AvailableCapacity)
	}
}