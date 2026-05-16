package api

import (
	"context"
	"net/http"
	"os"
)

type SystemInventoryResponse struct {
	ExecutionMode string                    `json:"execution_mode"`
	Controller    ControllerInventoryEntry  `json:"controller"`
	Generators    []GeneratorInventoryEntry `json:"generators"`
}

type ControllerInventoryEntry struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Address       string `json:"address"`
	Version       string `json:"version"`
	ExecutionMode string `json:"execution_mode"`
}

type GeneratorInventoryEntry struct {
	Target            string  `json:"target"`
	GeneratorID       string  `json:"generator_id"`
	Region            string  `json:"region,omitempty"`
	Status            string  `json:"status"`
	AvailableCapacity int32   `json:"available_capacity"`
	ActiveVUsers      int32   `json:"active_vusers"`
	CPUPercent        float64 `json:"cpu_percent"`
	MemoryBytes       int64   `json:"memory_bytes"`
	GoroutineCount    int32   `json:"goroutine_count"`
	CPULimitCores     float64 `json:"cpu_limit_cores"`
	MemoryLimitBytes  int64   `json:"memory_limit_bytes"`
	MaxVUsers         int32   `json:"max_vusers"`
}

func (s *Server) handleGetSystemInventory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.systemInventory(r.Context()))
}

func (s *Server) systemInventory(ctx context.Context) SystemInventoryResponse {
	controllerID := "controller"
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		controllerID = hostname
	}

	executionMode := "local"
	entries := []GeneratorInventoryEntry{}
	if backend, ok := s.runner.getExecutionBackend().(inventoryExecutionBackend); ok {
		executionMode = "distributed"
		entries = backend.SystemInventory(ctx)
	}

	return SystemInventoryResponse{
		ExecutionMode: executionMode,
		Controller: ControllerInventoryEntry{
			ID:            controllerID,
			Status:        "healthy",
			Address:       s.httpServer.Addr,
			Version:       "0.1.0",
			ExecutionMode: executionMode,
		},
		Generators: entries,
	}
}
