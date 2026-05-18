package api

import (
	"context"
	"net/http"
	"os"
	"sort"

	"github.com/jasonczech/stormrunner/pkg/protocol"
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
	Target             string   `json:"target"`
	GeneratorID        string   `json:"generator_id"`
	Region             string   `json:"region,omitempty"`
	Status             string   `json:"status"`
	Version            string   `json:"version,omitempty"`
	SupportedProtocols []string `json:"supported_protocols,omitempty"`
	DrainState         string   `json:"drain_state"`
	CapacityState      string   `json:"capacity_state"`
	HealthScore        int      `json:"health_score"`
	AvailableCapacity  int32    `json:"available_capacity"`
	ActiveVUsers       int32    `json:"active_vusers"`
	CPUPercent         float64  `json:"cpu_percent"`
	MemoryBytes        int64    `json:"memory_bytes"`
	GoroutineCount     int32    `json:"goroutine_count"`
	CPULimitCores      float64  `json:"cpu_limit_cores"`
	MemoryLimitBytes   int64    `json:"memory_limit_bytes"`
	MaxVUsers          int32    `json:"max_vusers"`
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
	for i := range entries {
		entries[i] = normalizeGeneratorInventoryEntry(entries[i])
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

func normalizeGeneratorInventoryEntry(entry GeneratorInventoryEntry) GeneratorInventoryEntry {
	reachable := entry.Status == "connected" || entry.Status == "healthy" || entry.Status == "ready"
	if entry.Version == "" {
		entry.Version = "unknown"
	}
	if len(entry.SupportedProtocols) == 0 {
		entry.SupportedProtocols = defaultSupportedProtocols()
	}
	if entry.DrainState == "" {
		entry.DrainState = "active"
	}
	if entry.CapacityState == "" {
		entry.CapacityState = generatorCapacityState(entry.AvailableCapacity, entry.MaxVUsers, reachable)
	}
	if entry.HealthScore == 0 && reachable {
		entry.HealthScore = generatorHealthScore(reachable, entry.CapacityState)
	}
	return entry
}

func defaultSupportedProtocols() []string {
	protocols := protocol.List()
	sort.Strings(protocols)
	return protocols
}

func generatorCapacityState(available int32, maxVUsers int32, reachable bool) string {
	if !reachable {
		return "unavailable"
	}
	if maxVUsers <= 0 {
		return "unknown"
	}
	if available <= 0 {
		return "saturated"
	}
	if float64(available)/float64(maxVUsers) <= 0.2 {
		return "constrained"
	}
	return "available"
}

func generatorHealthScore(reachable bool, capacityState string) int {
	if !reachable {
		return 0
	}
	switch capacityState {
	case "available":
		return 100
	case "constrained":
		return 70
	case "saturated":
		return 40
	default:
		return 80
	}
}
