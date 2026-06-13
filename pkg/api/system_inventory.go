package api

import (
	"context"
	"net/http"
	"os"
	"sort"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type SystemInventoryResponse struct {
	ExecutionMode string                    `json:"execution_mode"`
	Controller    ControllerInventoryEntry  `json:"controller"`
	Generators    []GeneratorInventoryEntry `json:"generators"`
	DataPlatform  DataPlatformInventory     `json:"data_platform,omitempty"`
}

type ControllerInventoryEntry struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Address       string `json:"address"`
	Version       string `json:"version"`
	ExecutionMode string `json:"execution_mode"`
}

type GeneratorInventoryEntry struct {
	Target                            string   `json:"target"`
	GeneratorID                       string   `json:"generator_id"`
	Region                            string   `json:"region,omitempty"`
	Status                            string   `json:"status"`
	Version                           string   `json:"version,omitempty"`
	SupportedProtocols                []string `json:"supported_protocols,omitempty"`
	CompatibleFeatures                []string `json:"compatible_features,omitempty"`
	CompatibilityState                string   `json:"compatibility_state,omitempty"`
	CompatibilityIssues               []string `json:"compatibility_issues,omitempty"`
	DrainState                        string   `json:"drain_state"`
	CapacityState                     string   `json:"capacity_state"`
	HealthScore                       int      `json:"health_score"`
	AvailableCapacity                 int32    `json:"available_capacity"`
	ActiveVUsers                      int32    `json:"active_vusers"`
	CPUPercent                        float64  `json:"cpu_percent"`
	MemoryBytes                       int64    `json:"memory_bytes"`
	GoroutineCount                    int32    `json:"goroutine_count"`
	CPULimitCores                     float64  `json:"cpu_limit_cores"`
	MemoryLimitBytes                  int64    `json:"memory_limit_bytes"`
	MaxVUsers                         int32    `json:"max_vusers"`
	PodName                           string   `json:"pod_name,omitempty"`
	Restartable                       bool     `json:"restartable,omitempty"`
	RestartBlockedReason              string   `json:"restart_blocked_reason,omitempty"`
	CPUUserPct                        float64  `json:"cpu_user_pct,omitempty"`
	CPUSystemPct                      float64  `json:"cpu_system_pct,omitempty"`
	CPUIOWaitPct                      float64  `json:"cpu_iowait_pct,omitempty"`
	CPUStealPct                       float64  `json:"cpu_steal_pct,omitempty"`
	ThreadCount                       int32    `json:"thread_count,omitempty"`
	OpenFDCount                       int32    `json:"open_fd_count,omitempty"`
	HeapAllocMB                       float64  `json:"heap_alloc_mb,omitempty"`
	HeapInUseMB                       float64  `json:"heap_in_use_mb,omitempty"`
	GCCountDelta                      uint32   `json:"gc_count_delta,omitempty"`
	GCPauseMs                         float64  `json:"gc_pause_ms,omitempty"`
	ContainerCPUUsagePct              float64  `json:"container_cpu_usage_pct,omitempty"`
	ContainerCPUThrottledPeriodsDelta uint64   `json:"container_cpu_throttled_periods_delta,omitempty"`
	ContainerCPUThrottledSecondsRate  float64  `json:"container_cpu_throttled_seconds_rate,omitempty"`
	ContainerMemoryWorkingSetMB       float64  `json:"container_memory_working_set_mb,omitempty"`
	ContainerMemoryUsagePct           float64  `json:"container_memory_usage_pct,omitempty"`
	ContainerMemoryFailCountDelta     uint64   `json:"container_memory_fail_count_delta,omitempty"`
	ContainerOOMKillCountDelta        uint64   `json:"container_oom_kill_count_delta,omitempty"`
	NetDropInPerSec                   float64  `json:"net_drop_in_per_sec,omitempty"`
	NetDropOutPerSec                  float64  `json:"net_drop_out_per_sec,omitempty"`
	NetErrorInPerSec                  float64  `json:"net_error_in_per_sec,omitempty"`
	NetErrorOutPerSec                 float64  `json:"net_error_out_per_sec,omitempty"`
	TCPRetransmitsPerSec              float64  `json:"tcp_retransmits_per_sec,omitempty"`
	TCPEstablished                    int      `json:"tcp_established,omitempty"`
	TCPTimeWait                       int      `json:"tcp_time_wait,omitempty"`
	DiskIOTimePct                     float64  `json:"disk_io_time_pct,omitempty"`
}

type DataPlatformInventory struct {
	MetadataStore    string `json:"metadata_store"`
	ArchiveBackend   string `json:"archive_backend"`
	ArchiveURIPrefix string `json:"archive_uri_prefix,omitempty"`
	CacheBackend     string `json:"cache_backend"`
	CacheKeyPrefix   string `json:"cache_key_prefix,omitempty"`
	CacheFanout      string `json:"cache_fanout,omitempty"`
	MetricsRetention string `json:"metrics_retention"`
}

func (s *Server) handleGetSystemInventory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.systemInventory(r.Context()))
}

func (s *Server) systemInventory(ctx context.Context) SystemInventoryResponse {
	controllerID := s.controllerID
	if controllerID == "" {
		controllerID = controllerInstanceID()
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
	s.enrichGeneratorRestartability(ctx, entries)
	s.cacheGeneratorInventory(ctx, entries)

	return SystemInventoryResponse{
		ExecutionMode: executionMode,
		Controller: ControllerInventoryEntry{
			ID:            controllerID,
			Status:        "healthy",
			Address:       s.httpServer.Addr,
			Version:       buildinfo.Get().Version,
			ExecutionMode: executionMode,
		},
		Generators:   entries,
		DataPlatform: s.dataPlatformInventory(),
	}
}

func (s *Server) enrichGeneratorRestartability(ctx context.Context, entries []GeneratorInventoryEntry) {
	if s == nil || !s.k8sRestart.Enabled || s.kubeRestarter == nil || len(entries) == 0 {
		return
	}
	pods, err := s.kubeRestarter.ListPods(ctx, s.k8sRestart.Namespace, s.k8sRestart.GeneratorSelector)
	if err != nil {
		for i := range entries {
			entries[i].RestartBlockedReason = "Kubernetes pod inventory is unavailable."
		}
		return
	}
	for i := range entries {
		pod := resolveGeneratorPodFromList(entries[i], pods, s.k8sRestart.GeneratorStatefulSet)
		if pod.Name != "" {
			entries[i].PodName = pod.Name
		}
		entries[i].Restartable, entries[i].RestartBlockedReason = generatorRestartability(entries[i], pod)
	}
}

func controllerInstanceID() string {
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname
	}
	return "controller"
}

func (s *Server) dataPlatformInventory() DataPlatformInventory {
	metadataStore := "unknown"
	if provider, ok := s.store.(storage.BackendInfoProvider); ok {
		metadataStore = provider.BackendInfo().Type
	}
	archiveBackend := currentArchiveBackend()
	cacheBackend := "memory"
	cacheKeyPrefix := ""
	cacheFanout := ""
	if s != nil && s.cache != nil {
		info := s.cache.Info()
		cacheBackend = info.Backend
		cacheKeyPrefix = info.KeyPrefix
		cacheFanout = info.FanoutChannel
	}
	return DataPlatformInventory{
		MetadataStore:    metadataStore,
		ArchiveBackend:   archiveBackend.Backend,
		ArchiveURIPrefix: archiveBackend.URIPrefix,
		CacheBackend:     cacheBackend,
		CacheKeyPrefix:   cacheKeyPrefix,
		CacheFanout:      cacheFanout,
		MetricsRetention: configuredMetricsRetention(),
	}
}

func (s *Server) cacheGeneratorInventory(ctx context.Context, generators []GeneratorInventoryEntry) {
	if s == nil || s.cache == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, defaultCacheWriteTimeout)
	defer cancel()
	if err := s.cache.SetGeneratorInventory(writeCtx, generators, defaultGeneratorSnapshotTTL); err != nil {
		return
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
	if len(entry.CompatibleFeatures) == 0 {
		entry.CompatibleFeatures = append([]string(nil), entry.SupportedProtocols...)
	}
	if entry.CompatibilityState == "" {
		entry.CompatibilityState = "compatible"
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
