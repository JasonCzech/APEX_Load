package api

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
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

type GeneratorSafetyExitEvidence struct {
	TestID                string    `json:"test_id"`
	ExecutionGeneration   uint64    `json:"execution_generation"`
	LifecycleState        string    `json:"lifecycle_state"`
	Reason                string    `json:"reason"`
	RequestedAt           time.Time `json:"requested_at"`
	OutstandingExecutions int32     `json:"outstanding_executions"`
}

type GeneratorInventoryEntry struct {
	Target                            string                                 `json:"target"`
	GeneratorID                       string                                 `json:"generator_id"`
	Region                            string                                 `json:"region,omitempty"`
	FleetPoolID                       string                                 `json:"fleet_pool_id,omitempty"`
	PoolName                          string                                 `json:"pool_name,omitempty"`
	Cordoned                          bool                                   `json:"cordoned,omitempty"`
	Status                            string                                 `json:"status"`
	Version                           string                                 `json:"version,omitempty"`
	SupportedProtocols                []string                               `json:"supported_protocols,omitempty"`
	Labels                            map[string]string                      `json:"labels,omitempty"`
	CompatibleFeatures                []string                               `json:"compatible_features,omitempty"`
	RuntimeCapacities                 []generatorrpc.ProtocolRuntimeCapacity `json:"runtime_capacities,omitempty"`
	CompatibilityState                string                                 `json:"compatibility_state,omitempty"`
	CompatibilityIssues               []string                               `json:"compatibility_issues,omitempty"`
	DrainState                        string                                 `json:"drain_state"`
	CapacityState                     string                                 `json:"capacity_state"`
	HealthScore                       int                                    `json:"health_score"`
	AvailableCapacity                 int32                                  `json:"available_capacity"`
	ActiveVUsers                      int32                                  `json:"active_vusers"`
	CPUPercent                        float64                                `json:"cpu_percent"`
	MemoryBytes                       int64                                  `json:"memory_bytes"`
	GoroutineCount                    int32                                  `json:"goroutine_count"`
	CPULimitCores                     float64                                `json:"cpu_limit_cores"`
	MemoryLimitBytes                  int64                                  `json:"memory_limit_bytes"`
	MaxVUsers                         int32                                  `json:"max_vusers"`
	PodName                           string                                 `json:"pod_name,omitempty"`
	Restartable                       bool                                   `json:"restartable,omitempty"`
	RestartBlockedReason              string                                 `json:"restart_blocked_reason,omitempty"`
	CPUUserPct                        float64                                `json:"cpu_user_pct,omitempty"`
	CPUSystemPct                      float64                                `json:"cpu_system_pct,omitempty"`
	CPUIOWaitPct                      float64                                `json:"cpu_iowait_pct,omitempty"`
	CPUStealPct                       float64                                `json:"cpu_steal_pct,omitempty"`
	ThreadCount                       int32                                  `json:"thread_count,omitempty"`
	OpenFDCount                       int32                                  `json:"open_fd_count,omitempty"`
	HeapAllocMB                       float64                                `json:"heap_alloc_mb,omitempty"`
	HeapInUseMB                       float64                                `json:"heap_in_use_mb,omitempty"`
	GCCountDelta                      uint32                                 `json:"gc_count_delta,omitempty"`
	GCPauseMs                         float64                                `json:"gc_pause_ms,omitempty"`
	ContainerCPUUsagePct              float64                                `json:"container_cpu_usage_pct,omitempty"`
	ContainerCPUThrottledPeriodsDelta uint64                                 `json:"container_cpu_throttled_periods_delta,omitempty"`
	ContainerCPUThrottledSecondsRate  float64                                `json:"container_cpu_throttled_seconds_rate,omitempty"`
	ContainerMemoryWorkingSetMB       float64                                `json:"container_memory_working_set_mb,omitempty"`
	ContainerMemoryUsagePct           float64                                `json:"container_memory_usage_pct,omitempty"`
	ContainerMemoryFailCountDelta     uint64                                 `json:"container_memory_fail_count_delta,omitempty"`
	ContainerOOMKillCountDelta        uint64                                 `json:"container_oom_kill_count_delta,omitempty"`
	NetDropInPerSec                   float64                                `json:"net_drop_in_per_sec,omitempty"`
	NetDropOutPerSec                  float64                                `json:"net_drop_out_per_sec,omitempty"`
	NetErrorInPerSec                  float64                                `json:"net_error_in_per_sec,omitempty"`
	NetErrorOutPerSec                 float64                                `json:"net_error_out_per_sec,omitempty"`
	TCPRetransmitsPerSec              float64                                `json:"tcp_retransmits_per_sec,omitempty"`
	TCPEstablished                    int                                    `json:"tcp_established,omitempty"`
	TCPTimeWait                       int                                    `json:"tcp_time_wait,omitempty"`
	DiskIOTimePct                     float64                                `json:"disk_io_time_pct,omitempty"`
	RunSupervisionExpirationsTotal    uint64                                 `json:"run_supervision_expirations_total"`
	RunSupervisionEscalationsTotal    uint64                                 `json:"run_supervision_escalations_total"`
	LastSafetyExit                    *GeneratorSafetyExitEvidence           `json:"last_safety_exit,omitempty"`
	SafetyExits                       []GeneratorSafetyExitEvidence          `json:"safety_exits,omitempty"`
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

	executionMode, entries := s.baseGeneratorInventory(ctx)
	s.enrichGeneratorRestartability(ctx, entries)
	s.enrichGeneratorInventoryWithPools(entries)
	s.runner.setCachedGeneratorInventory(entries, time.Now())
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

// baseGeneratorInventory reads transport health without Kubernetes or storage
// enrichment. Readiness uses this narrow view so its dedicated Kubernetes API
// check is the only pod-list request made while serving a readiness response.
func (s *Server) baseGeneratorInventory(ctx context.Context) (string, []GeneratorInventoryEntry) {
	executionMode := "local"
	entries := []GeneratorInventoryEntry{}
	if backend, ok := s.runner.getExecutionBackend().(inventoryExecutionBackend); ok {
		executionMode = "distributed"
		entries = backend.SystemInventory(ctx)
	}
	for i := range entries {
		entries[i] = normalizeGeneratorInventoryEntry(entries[i])
	}
	return executionMode, entries
}

// enrichGeneratorInventoryWithPools folds persisted fleet metadata (pool
// membership, cordon, region) onto the live inventory so the inventory is
// pool-aware. No-op when the store has no generator metadata.
func (s *Server) enrichGeneratorInventoryWithPools(entries []GeneratorInventoryEntry) {
	if s == nil || s.store == nil || len(entries) == 0 {
		return
	}
	metadata, err := s.store.ListGeneratorMetadata(0, 0)
	if err != nil || len(metadata) == 0 {
		return
	}
	index := make(map[string]*storage.GeneratorMetadata, len(metadata)*2)
	for _, m := range metadata {
		if m == nil {
			continue
		}
		if m.GeneratorID != "" {
			index[m.GeneratorID] = m
		}
		if t := strings.TrimSpace(m.Target); t != "" {
			index[t] = m
		}
	}
	poolNames := map[string]string{}
	for i := range entries {
		m := index[entries[i].GeneratorID]
		if m == nil {
			m = index[entries[i].Target]
		}
		if m == nil {
			continue
		}
		if m.FleetPoolID != nil && strings.TrimSpace(*m.FleetPoolID) != "" {
			poolID := strings.TrimSpace(*m.FleetPoolID)
			entries[i].FleetPoolID = poolID
			name, ok := poolNames[poolID]
			if !ok {
				if pool, err := s.store.GetFleetPool(poolID); err == nil && pool != nil {
					name = pool.Name
				}
				poolNames[poolID] = name
			}
			entries[i].PoolName = name
		}
		if entries[i].Region == "" && strings.TrimSpace(m.Region) != "" {
			entries[i].Region = m.Region
		}
		if len(m.Tags) > 0 {
			entries[i].Labels = cloneStringMap(m.Tags)
		}
		entries[i].Cordoned = strings.EqualFold(entries[i].DrainState, "cordoned")
	}
}

func (s *Server) enrichGeneratorRestartability(ctx context.Context, entries []GeneratorInventoryEntry) {
	if s == nil || !s.k8sRestart.Enabled || s.kubeRestarter == nil || len(entries) == 0 {
		return
	}
	pods, err := s.cachedGeneratorPods(ctx)
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
	if configured := strings.TrimSpace(os.Getenv("APEXLOAD_CONTROLLER_ID")); configured != "" {
		return configured
	}
	instance := uuid.NewString()
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		return hostname + "/" + instance
	}
	return "controller/" + instance
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
	reachable := generatorInventoryEntryReachable(entry)
	if entry.Version == "" {
		entry.Version = "unknown"
	}
	// Controller defaults are only a provisional display/bootstrap hint before
	// a generator is observed. A reachable entry came from a successful
	// heartbeat, so its empty protocol and feature lists are authoritative.
	if !reachable && len(entry.SupportedProtocols) == 0 {
		entry.SupportedProtocols = defaultSupportedProtocols()
	}
	if !reachable && len(entry.CompatibleFeatures) == 0 {
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
	registered := protocol.List()
	protocols := registered[:0]
	for _, name := range registered {
		// Bruno requires a runner plus project-scoped immutable artifact
		// delivery. Unknown/legacy generator inventory must not inherit it from
		// the controller's compile-time registry; a live heartbeat or explicit
		// target declaration must prove support.
		if strings.EqualFold(strings.TrimSpace(name), "bruno") {
			continue
		}
		protocols = append(protocols, name)
	}
	sort.Strings(protocols)
	return protocols
}

// generatorCapacityState is the per-generator capacity badge: a deliberately
// relative heuristic (<=20% of that generator's own capacity = constrained) so
// it stays meaningful across heterogeneous generator sizes, and it may be
// overridden by the generator's self-reported state upstream. The absolute
// fleet slot floors in ThresholdSettings drive fleet ALERTS only and do not
// rewrite this badge — see the field comments on ThresholdSettings.
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
