package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	readinessStatusReady    = "ready"
	readinessStatusDegraded = "degraded"
	readinessStatusNotReady = "not_ready"
)

type SystemReadinessResponse struct {
	Status      string                 `json:"status"`
	GeneratedAt time.Time              `json:"generated_at"`
	Checks      []SystemReadinessCheck `json:"checks"`
}

type SystemReadinessCheck struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (s *Server) handleSystemReadiness(w http.ResponseWriter, r *http.Request) {
	response := s.systemReadiness(r.Context())
	statusCode := http.StatusOK
	if response.Status == readinessStatusNotReady {
		statusCode = http.StatusServiceUnavailable
	}
	writeJSON(w, statusCode, response)
}

func (s *Server) systemReadiness(ctx context.Context) SystemReadinessResponse {
	checks := []SystemReadinessCheck{
		s.metadataStoreReadiness(),
		s.archiveWriterReadiness(),
		s.cacheBackendReadiness(ctx),
		s.generatorRPCReadiness(ctx),
		s.telemetryFanoutReadiness(),
		s.authModeReadiness(),
		s.schedulerReadiness(),
	}

	return SystemReadinessResponse{
		Status:      aggregateReadinessStatus(checks),
		GeneratedAt: time.Now().UTC(),
		Checks:      checks,
	}
}

func (s *Server) cacheBackendReadiness(ctx context.Context) SystemReadinessCheck {
	if s == nil || s.cache == nil {
		return SystemReadinessCheck{
			Name:    "cache_backend",
			Status:  readinessStatusNotReady,
			Message: "cache backend is not configured",
		}
	}
	info := s.cache.Info()
	probeCtx, cancel := context.WithTimeout(ctx, defaultCacheProbeTimeout)
	defer cancel()
	if err := s.cache.Ping(probeCtx); err != nil {
		status := readinessStatusDegraded
		if info.Backend == cacheBackendRedis {
			status = readinessStatusNotReady
		}
		return SystemReadinessCheck{
			Name:    "cache_backend",
			Status:  status,
			Message: fmt.Sprintf("cache backend probe failed: %v", err),
			Details: cacheReadinessDetails(info),
		}
	}
	message := "in-memory cache backend is ready"
	if info.Backend == cacheBackendRedis {
		message = "redis cache backend is reachable"
	}
	return SystemReadinessCheck{
		Name:    "cache_backend",
		Status:  readinessStatusReady,
		Message: message,
		Details: cacheReadinessDetails(info),
	}
}

func aggregateReadinessStatus(checks []SystemReadinessCheck) string {
	status := readinessStatusReady
	for _, check := range checks {
		switch check.Status {
		case readinessStatusNotReady:
			return readinessStatusNotReady
		case readinessStatusDegraded:
			status = readinessStatusDegraded
		}
	}
	return status
}

func (s *Server) metadataStoreReadiness() SystemReadinessCheck {
	if s == nil || s.store == nil {
		return SystemReadinessCheck{
			Name:    "metadata_store",
			Status:  readinessStatusNotReady,
			Message: "metadata store is not configured",
		}
	}
	if settingsStore, ok := s.store.(settingsPersistence); ok {
		if _, err := settingsStore.GetSetting(systemSettingsStoreKey); err != nil {
			return SystemReadinessCheck{
				Name:    "metadata_store",
				Status:  readinessStatusNotReady,
				Message: fmt.Sprintf("metadata store probe failed: %v", err),
			}
		}
	}
	details := map[string]any{}
	if provider, ok := s.store.(storage.BackendInfoProvider); ok {
		info := provider.BackendInfo()
		if info.Type != "" {
			details["store_type"] = info.Type
		}
		if info.Driver != "" {
			details["driver"] = info.Driver
		}
		if info.MigrationVersion != "" {
			details["migration_version"] = info.MigrationVersion
		}
		details["lease_capability"] = info.LeaseCapability
		details["timescaledb_enabled"] = info.TimescaleDBEnabled
	}
	return SystemReadinessCheck{
		Name:    "metadata_store",
		Status:  readinessStatusReady,
		Message: "metadata store is reachable",
		Details: details,
	}
}

func (s *Server) archiveWriterReadiness() SystemReadinessCheck {
	backend := currentArchiveBackend()
	if err := backend.validate(); err != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusNotReady,
			Message: err.Error(),
			Details: backend.readinessDetails(),
		}
	}
	root := backend.LocalRoot
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusNotReady,
			Message: fmt.Sprintf("archive root cannot be created: %v", err),
			Details: backend.readinessDetails(),
		}
	}
	probe, err := os.CreateTemp(root, ".readiness-*.tmp")
	if err != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusNotReady,
			Message: fmt.Sprintf("archive root is not writable: %v", err),
			Details: backend.readinessDetails(),
		}
	}
	probePath := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(probePath)
	if closeErr != nil || removeErr != nil {
		return SystemReadinessCheck{
			Name:    "archive_writer",
			Status:  readinessStatusDegraded,
			Message: "archive root is writable but cleanup reported an error",
			Details: mergeReadinessDetails(backend.readinessDetails(), map[string]any{
				"probe_file":   filepath.Base(probePath),
				"close_error":  errorString(closeErr),
				"remove_error": errorString(removeErr),
			}),
		}
	}
	return SystemReadinessCheck{
		Name:    "archive_writer",
		Status:  readinessStatusReady,
		Message: "archive root is writable",
		Details: backend.readinessDetails(),
	}
}

func mergeReadinessDetails(base map[string]any, extra map[string]any) map[string]any {
	merged := map[string]any{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func (s *Server) generatorRPCReadiness(ctx context.Context) SystemReadinessCheck {
	inventory := s.systemInventory(ctx)
	if inventory.ExecutionMode == "local" {
		return SystemReadinessCheck{
			Name:    "generator_rpc",
			Status:  readinessStatusReady,
			Message: "local execution mode does not require generator RPC",
			Details: map[string]any{
				"execution_mode": inventory.ExecutionMode,
			},
		}
	}

	available := 0
	unreachable := 0
	for _, generator := range inventory.Generators {
		status := strings.ToLower(strings.TrimSpace(generator.Status))
		if status == "" || status == "connected" || status == "healthy" || status == "ready" {
			available++
			continue
		}
		unreachable++
	}
	if available == 0 {
		return SystemReadinessCheck{
			Name:    "generator_rpc",
			Status:  readinessStatusNotReady,
			Message: "distributed execution is configured but no generator RPC targets are reachable",
			Details: map[string]any{
				"execution_mode": inventory.ExecutionMode,
				"generators":     len(inventory.Generators),
				"unreachable":    unreachable,
			},
		}
	}
	status := readinessStatusReady
	message := "generator RPC targets are reachable"
	if unreachable > 0 {
		status = readinessStatusDegraded
		message = "some generator RPC targets are not reachable"
	}
	return SystemReadinessCheck{
		Name:    "generator_rpc",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"execution_mode": inventory.ExecutionMode,
			"generators":     len(inventory.Generators),
			"available":      available,
			"unreachable":    unreachable,
		},
	}
}

func (s *Server) telemetryFanoutReadiness() SystemReadinessCheck {
	settings := s.runner.GetSystemSettings()
	interval := settings.General.MetricsIntervalSeconds
	status := readinessStatusReady
	message := "telemetry fan-out is initialized"
	if s.hub == nil {
		return SystemReadinessCheck{
			Name:    "telemetry_fanout",
			Status:  readinessStatusNotReady,
			Message: "websocket hub is not initialized",
		}
	}
	if interval <= 0 {
		status = readinessStatusDegraded
		message = "metrics interval is not configured; default runtime behavior may apply"
	}
	return SystemReadinessCheck{
		Name:    "telemetry_fanout",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"ws_clients":               s.hub.ClientCount(),
			"metrics_interval_seconds": interval,
		},
	}
}

func (s *Server) authModeReadiness() SystemReadinessCheck {
	auth := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	status := readinessStatusReady
	message := "auth mode is configured"
	if isAuthBypassMode(auth.Mode) {
		status = readinessStatusDegraded
		message = "auth bypass mode is active; suitable for local development only"
	}
	return SystemReadinessCheck{
		Name:    "auth_mode",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"mode": auth.Mode,
		},
	}
}

func (s *Server) schedulerReadiness() SystemReadinessCheck {
	if s == nil || s.scheduler == nil {
		return SystemReadinessCheck{
			Name:    "scheduler_state",
			Status:  readinessStatusNotReady,
			Message: "scenario scheduler is not initialized",
		}
	}
	enabled, due, errored := s.schedulerCounts(time.Now().UTC())
	status := readinessStatusReady
	message := "scenario scheduler is initialized"
	if errored > 0 {
		status = readinessStatusDegraded
		message = "scenario scheduler has schedules with previous errors"
	}
	return SystemReadinessCheck{
		Name:    "scheduler_state",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"enabled_schedules": enabled,
			"due_schedules":     due,
			"errored_schedules": errored,
			"ha_mode":           "single-active-controller",
		},
	}
}

func (s *Server) schedulerCounts(now time.Time) (int, int, int) {
	if s == nil || s.runner == nil {
		return 0, 0, 0
	}
	s.runner.mu.RLock()
	defer s.runner.mu.RUnlock()

	enabled := 0
	due := 0
	errored := 0
	for _, schedule := range s.runner.listAllScenarioSchedulesLocked() {
		if schedule == nil || !schedule.Enabled {
			continue
		}
		enabled++
		if schedule.NextRunAt != nil && !schedule.NextRunAt.After(now) {
			due++
		}
		if strings.TrimSpace(schedule.LastError) != "" {
			errored++
		}
	}
	return enabled, due, errored
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
