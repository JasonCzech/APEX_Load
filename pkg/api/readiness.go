package api

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	readinessStatusReady    = "ready"
	readinessStatusDegraded = "degraded"
	readinessStatusNotReady = "not_ready"

	// The shipped Kubernetes readinessProbe allows three seconds. Keep the
	// controller's complete response below that budget so JSON encoding and the
	// network write retain headroom even when one or more dependencies are slow.
	defaultSystemReadinessProbeTimeout     = 2500 * time.Millisecond
	defaultKubernetesReadinessProbeTimeout = 2 * time.Second
	defaultMetadataReadinessProbeTimeout   = 2 * time.Second
	uncancelableReadinessProbeCacheTTL     = time.Second
	readinessSettingsRefreshInterval       = systemSettingsAuthRefreshInterval
	readinessSettingsRefreshTimeout        = time.Second
	maxSchedulerReadinessTransitions       = 4096
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
	return s.systemReadinessWithin(ctx, defaultSystemReadinessProbeTimeout)
}

type systemReadinessProbe struct {
	name string
	run  func(context.Context) SystemReadinessCheck
}

type systemReadinessProbeResult struct {
	index int
	check SystemReadinessCheck
}

// readinessSettingsSnapshot is deliberately fixed-size. Readiness consumes only
// these scalar projections; publishing it must never retain the unbounded
// reservations, scopes, notification routes, or other slices in SystemSettings.
type readinessSettingsSnapshot struct {
	metricsIntervalSeconds int
	authMode               string
	prometheusEnabled      bool
	prometheusBaseURL      string
	github                 readinessGitHubSnapshot
}

type readinessGitHubSnapshot struct {
	enabled                    bool
	oauthConfigured            bool
	appConfigured              bool
	webhookConfigured          bool
	patConfigured              bool
	webhookSecretEnvVarPresent bool
	automationMode             string
	checksEnabled              bool
}

// runnerReadinessSnapshot is the immutable, atomically published projection of
// runner-owned readiness state. Every publication is serialized by
// TestRunner.mu, so a settings, execution-backend, or scheduler update cannot
// overwrite another axis with an older value.
type runnerReadinessSnapshot struct {
	capturedAt           time.Time
	settings             readinessSettingsSnapshot
	execution            testExecutionBackend
	schedulerEvaluatedAt time.Time
	enabledSchedules     int
	dueSchedules         int
	erroredSchedules     int
}

type schedulerReadinessProjection struct {
	evaluatedAt      time.Time
	enabledSchedules int
	dueSchedules     int
	erroredSchedules int
	futureSchedules  schedulerReadinessFutureHeap
	futureByID       map[string]*schedulerReadinessFuture
}

type schedulerReadinessFuture struct {
	scheduleID string
	at         time.Time
	index      int
}

type schedulerReadinessFutureHeap []*schedulerReadinessFuture

func (h schedulerReadinessFutureHeap) Len() int { return len(h) }
func (h schedulerReadinessFutureHeap) Less(left, right int) bool {
	if h[left].at.Equal(h[right].at) {
		return h[left].scheduleID < h[right].scheduleID
	}
	return h[left].at.Before(h[right].at)
}
func (h schedulerReadinessFutureHeap) Swap(left, right int) {
	h[left], h[right] = h[right], h[left]
	h[left].index = left
	h[right].index = right
}
func (h *schedulerReadinessFutureHeap) Push(value any) {
	item := value.(*schedulerReadinessFuture)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *schedulerReadinessFutureHeap) Pop() any {
	items := *h
	last := len(items) - 1
	item := items[last]
	items[last] = nil
	item.index = -1
	*h = items[:last]
	return item
}

type readinessSettingsCadence struct {
	cancel context.CancelFunc
}

// runtimeReadinessSnapshot adds the Server-owned scheduler initialization bit
// to the runner projection. The normal request path performs one atomic pointer
// load followed only by fixed-size shallow copies.
type runtimeReadinessSnapshot struct {
	capturedAt           time.Time
	settings             readinessSettingsSnapshot
	execution            testExecutionBackend
	schedulerInitialized bool
	schedulerEvaluatedAt time.Time
	enabledSchedules     int
	dueSchedules         int
	erroredSchedules     int
}

// projectReadinessSettings reduces the full settings document to the fixed set
// consumed by readiness. GitHub secrets are resolved to booleans at publication
// time; environment-variable names and every unbounded slice are discarded.
func projectReadinessSettings(settings SystemSettings) readinessSettingsSnapshot {
	auth := normalizeAuthSettings(AuthSettings{Mode: settings.Auth.Mode})
	prometheus := normalizePrometheusSettings(settings.Prometheus)
	githubInput := settings.GitHub
	githubInput.Scopes = nil
	github := normalizeGitHubSettings(githubInput)
	return readinessSettingsSnapshot{
		metricsIntervalSeconds: settings.General.MetricsIntervalSeconds,
		authMode:               auth.Mode,
		prometheusEnabled:      prometheus.Enabled,
		prometheusBaseURL:      prometheus.BaseURL,
		github: readinessGitHubSnapshot{
			enabled:                    github.Enabled,
			oauthConfigured:            github.oauthConfigured(),
			appConfigured:              github.appConfigured(),
			webhookConfigured:          github.webhookConfigured(),
			patConfigured:              github.patConfigured(),
			webhookSecretEnvVarPresent: strings.TrimSpace(github.WebhookSecretEnvVar) != "",
			automationMode:             github.AutomationMode,
			checksEnabled:              github.ChecksEnabled,
		},
	}
}

// publishRuntimeReadinessLocked publishes one combined revision. The caller
// must hold TestRunner.mu; this single lock is the ordering authority for all
// three projected axes.
func (tr *TestRunner) publishRuntimeReadinessLocked(now time.Time) {
	if tr == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	execution := tr.execution
	if execution == nil {
		execution = localExecutionBackend{}
	}
	scheduler := tr.readinessScheduler
	tr.readinessRuntime.Store(&runnerReadinessSnapshot{
		capturedAt:           now,
		settings:             projectReadinessSettings(tr.settings),
		execution:            execution,
		schedulerEvaluatedAt: scheduler.evaluatedAt,
		enabledSchedules:     scheduler.enabledSchedules,
		dueSchedules:         scheduler.dueSchedules,
		erroredSchedules:     scheduler.erroredSchedules,
	})
}

func scheduleReadinessContribution(schedule *storage.ScenarioSchedule, evaluatedAt time.Time) (enabled, due, errored int) {
	if schedule == nil || !schedule.Enabled {
		return 0, 0, 0
	}
	enabled = 1
	if schedule.NextRunAt != nil && !schedule.NextRunAt.After(evaluatedAt) {
		due = 1
	}
	if strings.TrimSpace(schedule.LastError) != "" {
		errored = 1
	}
	return enabled, due, errored
}

func (projection *schedulerReadinessProjection) removeFutureSchedule(scheduleID string) {
	if projection == nil || scheduleID == "" || projection.futureByID == nil {
		return
	}
	item := projection.futureByID[scheduleID]
	if item == nil {
		return
	}
	heap.Remove(&projection.futureSchedules, item.index)
	delete(projection.futureByID, scheduleID)
}

func (projection *schedulerReadinessProjection) addFutureSchedule(schedule *storage.ScenarioSchedule) {
	if projection == nil || schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.ID == "" ||
		!schedule.NextRunAt.After(projection.evaluatedAt) {
		return
	}
	if projection.futureByID == nil {
		projection.futureByID = make(map[string]*schedulerReadinessFuture)
	}
	projection.removeFutureSchedule(schedule.ID)
	item := &schedulerReadinessFuture{scheduleID: schedule.ID, at: schedule.NextRunAt.UTC()}
	heap.Push(&projection.futureSchedules, item)
	projection.futureByID[schedule.ID] = item
}

func (projection *schedulerReadinessProjection) trackedScheduleContribution(schedule *storage.ScenarioSchedule) (enabled, due, errored int) {
	if projection == nil || schedule == nil || !schedule.Enabled {
		return 0, 0, 0
	}
	enabled = 1
	if _, pending := projection.futureByID[schedule.ID]; !pending && schedule.NextRunAt != nil && !schedule.NextRunAt.After(projection.evaluatedAt) {
		due = 1
	}
	if strings.TrimSpace(schedule.LastError) != "" {
		errored = 1
	}
	return enabled, due, errored
}

// adjustSchedulerReadinessLocked applies a schedule mutation at the same
// evaluatedAt used by the existing aggregate. Wall-clock advancement is handled
// separately by the scheduler tick's bounded future-transition pass.
func (tr *TestRunner) adjustSchedulerReadinessLocked(before, after *storage.ScenarioSchedule) {
	if tr == nil {
		return
	}
	tr.adjustSchedulerReadinessCountersLocked(before, after)
	tr.publishRuntimeReadinessLocked(time.Now().UTC())
}

// adjustSchedulerReadinessCountersLocked is used by the scheduler sweep, which
// may mutate hundreds of schedules under one lock. The sweep publishes one
// combined snapshot at the end instead of allocating and publishing per row.
func (tr *TestRunner) adjustSchedulerReadinessCountersLocked(before, after *storage.ScenarioSchedule) {
	if tr == nil {
		return
	}
	projection := &tr.readinessScheduler
	if projection.evaluatedAt.IsZero() {
		projection.evaluatedAt = time.Now().UTC()
	}
	beforeEnabled, beforeDue, beforeErrored := projection.trackedScheduleContribution(before)
	if before != nil {
		projection.removeFutureSchedule(before.ID)
	}
	afterEnabled, afterDue, afterErrored := scheduleReadinessContribution(after, projection.evaluatedAt)
	projection.addFutureSchedule(after)
	projection.enabledSchedules += afterEnabled - beforeEnabled
	projection.dueSchedules += afterDue - beforeDue
	projection.erroredSchedules += afterErrored - beforeErrored
}

// advanceSchedulerReadinessLocked promotes a bounded number of future schedule
// entries whose NextRunAt has crossed the supplied clock. Any larger backlog is
// retained in the heap and continued by the next tracked scheduler tick.
func (tr *TestRunner) advanceSchedulerReadinessLocked(now time.Time) {
	if tr == nil {
		return
	}
	projection := &tr.readinessScheduler
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if projection.evaluatedAt.IsZero() {
		projection.evaluatedAt = now
	}
	processed := 0
	for len(projection.futureSchedules) > 0 && processed < maxSchedulerReadinessTransitions {
		item := projection.futureSchedules[0]
		if item.at.After(now) {
			break
		}
		heap.Pop(&projection.futureSchedules)
		delete(projection.futureByID, item.scheduleID)
		projection.dueSchedules++
		processed++
	}
	if now.After(projection.evaluatedAt) {
		projection.evaluatedAt = now
	}
}

func (tr *TestRunner) removeScenarioReadinessLocked(scenario *StoredScenario) {
	if tr == nil || scenario == nil {
		return
	}
	tr.removeScenarioReadinessCountersLocked(scenario)
	tr.publishRuntimeReadinessLocked(time.Now().UTC())
}

func (tr *TestRunner) removeScenarioReadinessCountersLocked(scenario *StoredScenario) {
	if tr == nil || scenario == nil {
		return
	}
	for _, schedule := range scenario.Schedules {
		tr.adjustSchedulerReadinessCountersLocked(schedule, nil)
	}
}

// resetSchedulerReadinessLocked rebuilds the aggregate only at authoritative
// bulk boundaries (startup and HA catalog replacement). It is never called by a
// readiness request.
func (tr *TestRunner) resetSchedulerReadinessLocked(evaluatedAt time.Time) {
	if tr == nil {
		return
	}
	if evaluatedAt.IsZero() {
		evaluatedAt = time.Now().UTC()
	}
	tr.readinessScheduler = schedulerReadinessProjectionForScenarios(tr.scenarios, evaluatedAt)
	tr.publishRuntimeReadinessLocked(time.Now().UTC())
}

func schedulerReadinessProjectionForScenarios(scenarios map[string]*StoredScenario, evaluatedAt time.Time) schedulerReadinessProjection {
	if evaluatedAt.IsZero() {
		evaluatedAt = time.Now().UTC()
	}
	projection := schedulerReadinessProjection{
		evaluatedAt: evaluatedAt,
		futureByID:  make(map[string]*schedulerReadinessFuture),
	}
	for _, scenario := range scenarios {
		if scenario == nil {
			continue
		}
		for _, schedule := range scenario.Schedules {
			enabled, due, errored := scheduleReadinessContribution(schedule, evaluatedAt)
			projection.enabledSchedules += enabled
			projection.dueSchedules += due
			projection.erroredSchedules += errored
			if schedule != nil && schedule.Enabled && schedule.NextRunAt != nil && schedule.ID != "" && schedule.NextRunAt.After(evaluatedAt) {
				item := &schedulerReadinessFuture{scheduleID: schedule.ID, at: schedule.NextRunAt.UTC(), index: len(projection.futureSchedules)}
				projection.futureSchedules = append(projection.futureSchedules, item)
				projection.futureByID[schedule.ID] = item
			}
		}
	}
	heap.Init(&projection.futureSchedules)
	return projection
}

// systemReadinessWithin runs independent probes concurrently under one response
// deadline. Individual dependency timeouts remain useful failure categories, but
// they can no longer add together or let the ten-second generator RPC timeout
// overrun the three-second Kubernetes readinessProbe.
func (s *Server) systemReadinessWithin(ctx context.Context, timeout time.Duration) SystemReadinessResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultSystemReadinessProbeTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	runtimeSnapshot := s.runtimeReadinessSnapshot()
	settings := runtimeSnapshot.settings

	probes := []systemReadinessProbe{
		{name: "kubernetes_api", run: s.kubernetesAPIReadiness},
		{name: "metadata_store", run: s.metadataStoreReadiness},
		{name: "archive_writer", run: s.archiveWriterReadiness},
		{name: "cache_backend", run: s.cacheBackendReadiness},
		{name: "generator_rpc", run: func(ctx context.Context) SystemReadinessCheck {
			return s.generatorRPCReadinessWithBackend(ctx, runtimeSnapshot.execution)
		}},
		{name: "telemetry_fanout", run: func(context.Context) SystemReadinessCheck { return s.telemetryFanoutReadinessWithSettings(settings) }},
		{name: "auth_mode", run: func(context.Context) SystemReadinessCheck { return s.authModeReadinessWithSettings(settings) }},
		{name: "platform_integrations", run: func(context.Context) SystemReadinessCheck {
			return s.platformIntegrationsReadinessWithSettings(settings)
		}},
		{name: "github_enterprise", run: func(context.Context) SystemReadinessCheck { return s.githubReadinessWithSettings(settings) }},
		{name: "scheduler_state", run: func(context.Context) SystemReadinessCheck {
			return schedulerReadinessFromSnapshot(runtimeSnapshot)
		}},
	}
	results := make(chan systemReadinessProbeResult, len(probes))
	for index, probe := range probes {
		go func(index int, probe systemReadinessProbe) {
			check := SystemReadinessCheck{}
			func() {
				defer func() {
					if recover() != nil {
						check = s.incompleteReadinessCheck(probe.name, "probe_panicked")
					}
				}()
				check = probe.run(probeCtx)
			}()
			results <- systemReadinessProbeResult{index: index, check: check}
		}(index, probe)
	}

	checks := make([]SystemReadinessCheck, len(probes))
	completed := make([]bool, len(probes))
	remaining := len(probes)
	for remaining > 0 {
		select {
		case result := <-results:
			if result.index < 0 || result.index >= len(probes) || completed[result.index] {
				continue
			}
			checks[result.index] = result.check
			completed[result.index] = true
			remaining--
		case <-probeCtx.Done():
			reason := readinessProbeFailureReason(probeCtx.Err())
			for index, probe := range probes {
				if !completed[index] {
					checks[index] = s.incompleteReadinessCheck(probe.name, reason)
				}
			}
			remaining = 0
		}
	}
	return SystemReadinessResponse{
		Status:      aggregateReadinessStatus(checks),
		GeneratedAt: time.Now().UTC(),
		Checks:      checks,
	}
}

// runtimeReadinessSnapshot is the request-path operation: one atomic pointer
// load and fixed-size shallow copies. It never acquires runner.mu, clones
// settings, scans schedules, or launches background work.
func (s *Server) runtimeReadinessSnapshot() runtimeReadinessSnapshot {
	if s == nil || s.runner == nil {
		return runtimeReadinessSnapshot{
			settings: readinessSettingsSnapshot{
				metricsIntervalSeconds: 1,
				authMode:               authModeDev,
			},
			execution: localExecutionBackend{},
		}
	}
	if cached := s.runner.readinessRuntime.Load(); cached != nil {
		return runtimeReadinessSnapshot{
			capturedAt:           cached.capturedAt,
			settings:             cached.settings,
			execution:            cached.execution,
			schedulerInitialized: s.scheduler != nil,
			schedulerEvaluatedAt: cached.schedulerEvaluatedAt,
			enabledSchedules:     cached.enabledSchedules,
			dueSchedules:         cached.dueSchedules,
			erroredSchedules:     cached.erroredSchedules,
		}
	}
	// NewTestRunner publishes before NewServer exposes its handler. This fallback
	// only serves hand-built test/embedded values.
	return runtimeReadinessSnapshot{
		settings: readinessSettingsSnapshot{
			metricsIntervalSeconds: 1,
			authMode:               authModeDev,
		},
		execution:            localExecutionBackend{},
		schedulerInitialized: s.scheduler != nil,
	}
}

// readinessSystemSettingsSnapshot returns only the fixed-size published
// projection. Direct helper calls share the exact same O(1) path as the full
// readiness response.
func (s *Server) readinessSystemSettingsSnapshot() readinessSettingsSnapshot {
	return s.runtimeReadinessSnapshot().settings
}

// refreshReadinessSettingsForStartup performs one bounded forced read before
// listeners are published. Concrete SQLite and PostgreSQL stores implement the
// contextual extension; legacy decorators keep their constructor-loaded state
// rather than making startup or shutdown depend on an uncancelable call.
func (s *Server) refreshReadinessSettingsForStartup() error {
	if s == nil || s.runner == nil || s.runner.settingsStore == nil {
		return nil
	}
	if _, ok := s.runner.settingsStore.(contextualSettingsPersistence); !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), readinessSettingsRefreshTimeout)
	defer cancel()
	if err := s.runner.refreshSystemSettingsFromStoreContextStartup(ctx); err != nil {
		return fmt.Errorf("refresh durable system settings before startup: %w", err)
	}
	return nil
}

// startReadinessSettingsRefreshCadence is the only asynchronous freshness
// mechanism for the readiness settings projection. It is lifecycle-owned,
// context-cancelable, and tracked by Server.backgroundWG; readiness requests do
// not trigger it.
func (s *Server) startReadinessSettingsRefreshCadence() {
	if s == nil || s.runner == nil || s.runner.settingsStore == nil || s.shutdownRequested() {
		return
	}
	if _, ok := s.runner.settingsStore.(contextualSettingsPersistence); !ok {
		return
	}
	s.readinessSettingsMu.Lock()
	if s.readinessSettingsCadence != nil {
		s.readinessSettingsMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &readinessSettingsCadence{cancel: cancel}
	s.readinessSettingsCadence = state
	s.readinessSettingsMu.Unlock()
	if !s.launchBackground(func() {
		s.refreshReadinessSettingsOnce(ctx)
		ticker := time.NewTicker(readinessSettingsRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshReadinessSettingsOnce(ctx)
			}
		}
	}) {
		cancel()
		s.readinessSettingsMu.Lock()
		if s.readinessSettingsCadence == state {
			s.readinessSettingsCadence = nil
		}
		s.readinessSettingsMu.Unlock()
	}
}

func (s *Server) refreshReadinessSettingsOnce(parent context.Context) {
	if s == nil || s.runner == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, readinessSettingsRefreshTimeout)
	defer cancel()
	_ = s.runner.refreshSystemSettingsFromStoreContextOpportunistic(ctx, false)
}

func readinessProbeFailureReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "probe_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "probe_canceled"
	}
	return "probe_failed"
}

// incompleteReadinessCheck supplies a safe result when a probe does not return
// before the shared response deadline. Kubernetes and local in-memory cache
// failures remain advisory; configured remote/unavailable dependencies fail
// readiness closed.
func (s *Server) incompleteReadinessCheck(name, reason string) SystemReadinessCheck {
	status := readinessStatusNotReady
	details := map[string]any{"reason": reason}
	message := "readiness probe did not complete before the response deadline"

	switch name {
	case "kubernetes_api":
		enabled := s != nil && s.k8sRestart.Enabled
		details["enabled"] = enabled
		configured := enabled && s.kubeRestarter != nil && strings.TrimSpace(s.k8sRestart.Namespace) != ""
		details["configured"] = configured
		if s != nil && strings.TrimSpace(s.k8sRestart.Namespace) != "" {
			details["namespace"] = strings.TrimSpace(s.k8sRestart.Namespace)
		}
		if enabled {
			status = readinessStatusDegraded
			message = "Kubernetes API probe did not complete before the response deadline"
		} else {
			status = readinessStatusReady
			delete(details, "reason")
			message = "Kubernetes integration is not configured for this runtime"
		}
	case "cache_backend":
		if s != nil && s.cache != nil {
			info := s.cache.Info()
			details = mergeReadinessDetails(cacheReadinessDetails(info), details)
			if info.ConfigError == "" && !cacheBackendIsRemote(info.Backend) {
				status = readinessStatusDegraded
			}
		}
		message = "cache backend probe did not complete before the response deadline"
	case "platform_integrations":
		status = readinessStatusDegraded
	}

	return SystemReadinessCheck{Name: name, Status: status, Message: message, Details: details}
}

// kubernetesAPIReadiness verifies the same narrow, namespaced Kubernetes API
// surface used by generator inventory and restart controls. A control-plane or
// RBAC failure is advisory: the controller can still serve traffic and execute
// through already configured generator RPC targets, so this check degrades the
// aggregate rather than making the pod unready.
func (s *Server) kubernetesAPIReadiness(ctx context.Context) SystemReadinessCheck {
	details := map[string]any{
		"enabled":    false,
		"configured": false,
	}
	if s == nil || !s.k8sRestart.Enabled {
		return SystemReadinessCheck{
			Name:    "kubernetes_api",
			Status:  readinessStatusReady,
			Message: "Kubernetes integration is not configured for this runtime",
			Details: details,
		}
	}

	details["enabled"] = true
	if namespace := strings.TrimSpace(s.k8sRestart.Namespace); namespace != "" {
		details["namespace"] = namespace
	} else {
		details["reason"] = "namespace_missing"
		return SystemReadinessCheck{
			Name:    "kubernetes_api",
			Status:  readinessStatusDegraded,
			Message: "Kubernetes namespace is not configured",
			Details: details,
		}
	}
	if s.kubeRestarter == nil {
		details["reason"] = "client_unavailable"
		return SystemReadinessCheck{
			Name:    "kubernetes_api",
			Status:  readinessStatusDegraded,
			Message: "Kubernetes API client is unavailable",
			Details: details,
		}
	}

	details["configured"] = true
	probeCtx, cancel := context.WithTimeout(ctx, defaultKubernetesReadinessProbeTimeout)
	defer cancel()
	start := time.Now()
	pods, err := s.cachedGeneratorPods(probeCtx)
	details["probe_latency_ms"] = roundMilliseconds(time.Since(start))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			details["reason"] = "probe_timeout"
			return SystemReadinessCheck{
				Name:    "kubernetes_api",
				Status:  readinessStatusDegraded,
				Message: "Kubernetes API probe timed out",
				Details: details,
			}
		}
		details["reason"] = "probe_failed"
		return SystemReadinessCheck{
			Name:    "kubernetes_api",
			Status:  readinessStatusDegraded,
			Message: "Kubernetes API or pod-list permission is unavailable",
			Details: details,
		}
	}

	readyPods := 0
	terminatingPods := 0
	for _, pod := range pods {
		if pod.Ready && !pod.Terminating {
			readyPods++
		}
		if pod.Terminating {
			terminatingPods++
		}
	}
	details["pod_count"] = len(pods)
	details["ready_pods"] = readyPods
	details["terminating_pods"] = terminatingPods
	return SystemReadinessCheck{
		Name:    "kubernetes_api",
		Status:  readinessStatusReady,
		Message: "Kubernetes API and pod-list permission are available",
		Details: details,
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
	start := time.Now()
	err := s.cache.Ping(probeCtx)
	latency := time.Since(start)
	details := cacheReadinessDetails(info)
	details["probe_latency_ms"] = roundMilliseconds(latency)
	if err != nil {
		status := readinessStatusDegraded
		if info.ConfigError != "" || cacheBackendIsRemote(info.Backend) {
			status = readinessStatusNotReady
		}
		details["reason"] = cacheProbeFailureReason(probeCtx, err, info)
		return SystemReadinessCheck{
			Name:    "cache_backend",
			Status:  status,
			Message: "cache backend probe failed",
			Details: details,
		}
	}
	message := "in-memory cache backend is ready"
	if cacheBackendIsRemote(info.Backend) {
		message = fmt.Sprintf("%s cache backend is reachable", info.Backend)
	}
	return SystemReadinessCheck{
		Name:    "cache_backend",
		Status:  readinessStatusReady,
		Message: message,
		Details: details,
	}
}

func cacheProbeFailureReason(ctx context.Context, err error, info CacheBackendInfo) string {
	if info.ConfigError != "" {
		return "configuration_invalid"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "probe_timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "probe_canceled"
	}
	return "probe_failed"
}

// roundMilliseconds converts a duration to milliseconds with microsecond precision.
func roundMilliseconds(d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(d.Microseconds()) / 1000.0
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

type contextualSettingsPersistence interface {
	GetSettingContext(context.Context, string) (string, error)
}

// legacyMetadataReadinessProbeState bounds compatibility stores that implement
// the public storage.Store contract without the optional cancellable settings
// extension. At most one uncancellable read may remain in flight per server.
type legacyMetadataReadinessProbeState struct {
	inFlight  chan struct{}
	err       error
	expiresAt time.Time
}

var errMetadataReadinessProbePanicked = errors.New("metadata readiness probe panicked")

func (s *Server) metadataStoreReadiness(ctx context.Context) SystemReadinessCheck {
	if s == nil || s.store == nil {
		return SystemReadinessCheck{
			Name:    "metadata_store",
			Status:  readinessStatusNotReady,
			Message: "metadata store is not configured",
		}
	}
	details := metadataStoreReadinessDetails(s.store)
	probeCtx, cancel := context.WithTimeout(ctx, defaultMetadataReadinessProbeTimeout)
	defer cancel()
	probeStart := time.Now()
	var probeErr error
	if settingsStore, ok := s.store.(contextualSettingsPersistence); ok {
		_, probeErr = settingsStore.GetSettingContext(probeCtx, systemSettingsStoreKey)
	} else if settingsStore, ok := s.store.(settingsPersistence); ok {
		probeErr = s.legacyMetadataStoreReadiness(probeCtx, settingsStore)
	} else {
		probeErr = errors.New("metadata store settings probe is unavailable")
	}
	if probeErr != nil {
		details["probe_latency_ms"] = roundMilliseconds(time.Since(probeStart))
		details["reason"] = metadataProbeFailureReason(probeCtx, probeErr)
		return SystemReadinessCheck{
			Name:    "metadata_store",
			Status:  readinessStatusNotReady,
			Message: "metadata store probe failed",
			Details: details,
		}
	}
	details["probe_latency_ms"] = roundMilliseconds(time.Since(probeStart))
	return SystemReadinessCheck{
		Name:    "metadata_store",
		Status:  readinessStatusReady,
		Message: "metadata store is reachable",
		Details: details,
	}
}

// metadataStoreReadinessDetails is the anonymous-readiness serialization
// boundary for storage metadata. BackendInfoProvider is an exported extension
// point, so its free-form strings must not cross that boundary verbatim.
func metadataStoreReadinessDetails(store storage.Store) map[string]any {
	details := map[string]any{}
	provider, ok := store.(storage.BackendInfoProvider)
	if !ok {
		return details
	}
	info := provider.BackendInfo()
	details["store_type"] = safeMetadataStoreReadinessLabel(info.Type)
	details["lease_capability"] = info.LeaseCapability
	details["timescaledb_enabled"] = info.TimescaleDBEnabled
	return details
}

func safeMetadataStoreReadinessLabel(storeType string) string {
	switch strings.ToLower(strings.TrimSpace(storeType)) {
	case storage.StoreBackendSQLite:
		return storage.StoreBackendSQLite
	case storage.StoreBackendPostgres, "postgresql":
		return storage.StoreBackendPostgres
	default:
		return "unknown"
	}
}

func (s *Server) legacyMetadataStoreReadiness(ctx context.Context, store settingsPersistence) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		now := time.Now()
		s.metadataReadinessMu.Lock()
		state := &s.metadataReadiness
		if state.inFlight == nil && now.Before(state.expiresAt) {
			err := state.err
			s.metadataReadinessMu.Unlock()
			return err
		}
		if done := state.inFlight; done != nil {
			s.metadataReadinessMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		done := make(chan struct{})
		state.inFlight = done
		s.metadataReadinessMu.Unlock()

		go func() {
			var probeErr error
			defer func() {
				if recover() != nil {
					probeErr = errMetadataReadinessProbePanicked
				}
				s.metadataReadinessMu.Lock()
				s.metadataReadiness.err = probeErr
				s.metadataReadiness.expiresAt = time.Now().Add(uncancelableReadinessProbeCacheTTL)
				s.metadataReadiness.inFlight = nil
				close(done)
				s.metadataReadinessMu.Unlock()
			}()
			_, probeErr = store.GetSetting(systemSettingsStoreKey)
		}()

		select {
		case <-done:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func metadataProbeFailureReason(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "probe_timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "probe_canceled"
	}
	return "probe_failed"
}

type archiveReadinessProbeState struct {
	inFlight  chan struct{}
	check     SystemReadinessCheck
	expiresAt time.Time
}

// archiveWriterReadiness serializes the filesystem probe because the os calls
// do not accept a context. A hung filesystem operation therefore consumes at
// most one goroutine per server instead of one goroutine per readiness request.
func (s *Server) archiveWriterReadiness(ctx context.Context) SystemReadinessCheck {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return s.incompleteReadinessCheck("archive_writer", readinessProbeFailureReason(err))
		}

		now := time.Now()
		s.archiveReadinessMu.Lock()
		state := &s.archiveReadiness
		if state.inFlight == nil && now.Before(state.expiresAt) {
			check := state.check
			s.archiveReadinessMu.Unlock()
			return check
		}
		if done := state.inFlight; done != nil {
			s.archiveReadinessMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return s.incompleteReadinessCheck("archive_writer", readinessProbeFailureReason(ctx.Err()))
			}
		}

		done := make(chan struct{})
		state.inFlight = done
		probe := s.archiveReadinessProbe
		if probe == nil {
			probe = s.probeArchiveWriterReadiness
		}
		s.archiveReadinessMu.Unlock()

		go func() {
			check := SystemReadinessCheck{}
			func() {
				defer func() {
					if recover() != nil {
						check = s.incompleteReadinessCheck("archive_writer", "probe_panicked")
					}
				}()
				check = probe()
			}()
			s.archiveReadinessMu.Lock()
			s.archiveReadiness.check = check
			s.archiveReadiness.expiresAt = time.Now().Add(uncancelableReadinessProbeCacheTTL)
			s.archiveReadiness.inFlight = nil
			close(done)
			s.archiveReadinessMu.Unlock()
		}()

		select {
		case <-done:
			continue
		case <-ctx.Done():
			return s.incompleteReadinessCheck("archive_writer", readinessProbeFailureReason(ctx.Err()))
		}
	}
}

func (s *Server) probeArchiveWriterReadiness() SystemReadinessCheck {
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
	snapshot := s.runtimeReadinessSnapshot()
	return s.generatorRPCReadinessWithBackend(ctx, snapshot.execution)
}

func (s *Server) generatorRPCReadinessWithBackend(ctx context.Context, backend testExecutionBackend) SystemReadinessCheck {
	executionMode := "local"
	generators := []GeneratorInventoryEntry{}
	if inventoryBackend, ok := backend.(inventoryExecutionBackend); ok {
		executionMode = "distributed"
		generators = inventoryBackend.SystemInventory(ctx)
	}
	for i := range generators {
		generators[i] = normalizeGeneratorInventoryEntry(generators[i])
	}
	transport := generatorrpc.TransportSecurityFromEnv()
	if executionMode == "local" {
		return SystemReadinessCheck{
			Name:    "generator_rpc",
			Status:  readinessStatusReady,
			Message: "local execution mode does not require generator RPC",
			Details: map[string]any{
				"execution_mode": executionMode,
				"grpc_transport": transport.String(),
			},
		}
	}
	if err := ctx.Err(); err != nil {
		return SystemReadinessCheck{
			Name:    "generator_rpc",
			Status:  readinessStatusNotReady,
			Message: "generator RPC probe did not complete before the response deadline",
			Details: map[string]any{
				"execution_mode": executionMode,
				"generators":     len(generators),
				"grpc_transport": transport.String(),
				"reason":         readinessProbeFailureReason(err),
			},
		}
	}

	available := 0
	unreachable := 0
	for _, generator := range generators {
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
				"execution_mode": executionMode,
				"generators":     len(generators),
				"unreachable":    unreachable,
				"grpc_transport": transport.String(),
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
			"execution_mode": executionMode,
			"generators":     len(generators),
			"available":      available,
			"unreachable":    unreachable,
			"grpc_transport": transport.String(),
		},
	}
}

func (s *Server) telemetryFanoutReadiness() SystemReadinessCheck {
	return s.telemetryFanoutReadinessWithSettings(s.readinessSystemSettingsSnapshot())
}

func (s *Server) telemetryFanoutReadinessWithSettings(settings readinessSettingsSnapshot) SystemReadinessCheck {
	interval := settings.metricsIntervalSeconds
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
	return s.authModeReadinessWithSettings(s.readinessSystemSettingsSnapshot())
}

func (s *Server) authModeReadinessWithSettings(settings readinessSettingsSnapshot) SystemReadinessCheck {
	auth := s.authSettingsForRuntime(AuthSettings{Mode: settings.authMode})
	status := readinessStatusReady
	message := "auth mode is configured"
	if isAuthBypassMode(auth.Mode) {
		status = readinessStatusDegraded
		message = "auth bypass mode is active; suitable for local development only"
	} else if auth.Mode == authModeLocal {
		status = readinessStatusDegraded
		message = "local test login is active; fixed test credentials are suitable for non-production only"
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

func (s *Server) platformIntegrationsReadiness() SystemReadinessCheck {
	return s.platformIntegrationsReadinessWithSettings(s.readinessSystemSettingsSnapshot())
}

func (s *Server) platformIntegrationsReadinessWithSettings(settings readinessSettingsSnapshot) SystemReadinessCheck {
	backend := currentArchiveBackend()
	prometheus := PrometheusSettings{Enabled: settings.prometheusEnabled, BaseURL: settings.prometheusBaseURL}
	dashboard := dashboardStaticAssetsReadiness(s.dashboardStaticFS())
	wrapping := wrappingKeyReadinessDetails()
	status := readinessStatusReady
	message := "deployment integration surfaces are configured"
	if dashboard["status"] != readinessStatusReady {
		status = readinessStatusDegraded
		message = "deployment integration surfaces are configured with dashboard asset warnings"
	}
	// Two wrapping keys configured at once is not a warning to bury: server
	// startup only logs it, the wrapper stays nil, and every credential write
	// then fails with a 503 that does not explain why. Readiness is where an
	// operator will actually see the cause.
	if conflict, _ := wrapping["conflict"].(bool); conflict {
		status = readinessStatusDegraded
		message = "two wrapping keys are configured; set exactly one of " +
			keywrap.KeyVaultKeyIDEnvVar + " or " + keywrap.KMSKeyIDEnvVar
	}
	return SystemReadinessCheck{
		Name:    "platform_integrations",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"grpc_transport":            generatorrpc.TransportSecurityFromEnv().String(),
			"secret_resolver":           secretResolverReadinessDetails(),
			"object_archive":            objectArchiveReadinessDetails(backend),
			"prometheus_scaling_source": prometheusScalingSourceDetails(prometheus),
			"azure_identity":            azureIdentityReadinessDetails(),
			"aws_identity":              awsIdentityReadinessDetails(),
			"wrapping_key":              wrapping,
			"dashboard_static_assets":   dashboard,
		},
	}
}

// wrappingKeyReadinessDetails reports which envelope-wrapping key is selected.
// It is a separate axis from secret_resolver: the wrapper protects cluster
// credentials and project-secret ciphertext, while the secret store is where
// project secret VALUES live.
func wrappingKeyReadinessDetails() map[string]any {
	provider, conflict := keywrap.ConfiguredWrappingProvider()
	return map[string]any{
		"provider": provider,
		"conflict": conflict,
	}
}

func secretResolverReadinessDetails() map[string]any {
	keyVaultURL := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_URL"))
	mode := strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_KEY_VAULT_AUTH"))
	if mode == "" {
		mode = "auto"
	}
	details := map[string]any{
		"env_secret_prefix_enabled":  true,
		"azure_key_vault_configured": keyVaultURL != "",
		"aws_secrets_manager_configured": strings.EqualFold(
			strings.TrimSpace(os.Getenv("APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED")), "true"),
	}
	if keyVaultURL != "" {
		details["azure_key_vault_auth"] = mode
	}
	return details
}

func objectArchiveReadinessDetails(backend archiveBackendRuntime) map[string]any {
	details := map[string]any{
		"backend":        backend.Backend,
		"object_backend": backend.Backend != archiveBackendFilesystem,
		"staging_root":   backend.LocalRoot,
	}
	if backend.URIPrefix != "" {
		details["uri_prefix"] = backend.URIPrefix
	}
	if backend.Container != "" {
		details["container"] = backend.Container
	}
	if backend.Bucket != "" {
		details["bucket"] = backend.Bucket
	}
	return details
}

func prometheusScalingSourceDetails(settings PrometheusSettings) map[string]any {
	mode := strings.TrimSpace(os.Getenv("APEXLOAD_PROMETHEUS_SCALING_SOURCE"))
	if mode == "" {
		mode = "prometheus"
	}
	details := map[string]any{
		"source":           mode,
		"settings_enabled": settings.Enabled,
	}
	if settings.BaseURL != "" {
		details["base_url"] = settings.BaseURL
	}
	if remoteWrite := strings.TrimSpace(os.Getenv("APEXLOAD_PROMETHEUS_REMOTE_WRITE_URL")); remoteWrite != "" {
		details["remote_write_configured"] = true
	}
	return details
}

func azureIdentityReadinessDetails() map[string]any {
	clientID := firstNonEmpty(
		strings.TrimSpace(os.Getenv("AZURE_CLIENT_ID")),
		strings.TrimSpace(os.Getenv("APEXLOAD_AZURE_CLIENT_ID")),
	)
	tokenFile := strings.TrimSpace(os.Getenv("AZURE_FEDERATED_TOKEN_FILE"))
	return map[string]any{
		"client_id_configured": clientID != "",
		"federated_token_file": tokenFile != "",
		"tenant_id_configured": strings.TrimSpace(os.Getenv("AZURE_TENANT_ID")) != "",
		"authority_host_set":   strings.TrimSpace(os.Getenv("AZURE_AUTHORITY_HOST")) != "",
	}
}

// awsIdentityReadinessDetails mirrors azureIdentityReadinessDetails: booleans
// only, never an identifier's value, so /readyz can be read by anyone who can
// reach it without disclosing which account the controller runs as.
func awsIdentityReadinessDetails() map[string]any {
	return map[string]any{
		// IRSA projects a token file and a role ARN into the pod.
		"web_identity_token_file": strings.TrimSpace(os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")) != "",
		"role_arn_configured":     strings.TrimSpace(os.Getenv("AWS_ROLE_ARN")) != "",
		"region_configured": firstNonEmpty(
			strings.TrimSpace(os.Getenv("AWS_REGION")),
			strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION")),
		) != "",
		// EKS Pod Identity delivers credentials over the container endpoint.
		"container_credentials": strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI")) != "" ||
			strings.TrimSpace(os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI")) != "",
		"static_keys_in_env": strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")) != "",
	}
}

// dashboardStaticFS returns the filesystem used to probe embedded dashboard
// assets, defaulting to the compile-time embed when no override is set.
func (s *Server) dashboardStaticFS() fs.FS {
	if s != nil && s.staticFS != nil {
		return s.staticFS
	}
	return StaticFS
}

func dashboardStaticAssetsReadiness(staticFS fs.FS) map[string]any {
	details := map[string]any{
		"status": readinessStatusReady,
	}
	if _, err := fs.Stat(staticFS, "static/index.html"); err != nil {
		details["status"] = readinessStatusDegraded
		details["index_html"] = false
		details["error"] = err.Error()
		return details
	}
	details["index_html"] = true
	assets, err := fs.Glob(staticFS, "static/assets/*")
	if err != nil {
		details["status"] = readinessStatusDegraded
		details["error"] = err.Error()
		return details
	}
	details["asset_count"] = len(assets)
	if len(assets) == 0 {
		details["status"] = readinessStatusDegraded
	}
	return details
}

func (s *Server) githubReadiness() SystemReadinessCheck {
	return s.githubReadinessWithSettings(s.readinessSystemSettingsSnapshot())
}

func (s *Server) githubReadinessWithSettings(systemSettings readinessSettingsSnapshot) SystemReadinessCheck {
	settings := systemSettings.github
	details := map[string]any{
		"enabled":              settings.enabled,
		"oauth_configured":     settings.oauthConfigured,
		"app_configured":       settings.appConfigured,
		"webhook_configured":   settings.webhookConfigured,
		"pat_configured":       settings.patConfigured,
		"encryption_available": githubTokenEncryptionAvailable(),
		"automation_mode":      settings.automationMode,
		"checks_enabled":       settings.checksEnabled,
	}
	if !settings.enabled {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusReady, Message: "github integration is disabled", Details: details}
	}
	if !settings.oauthConfigured && !settings.appConfigured && !settings.patConfigured {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusNotReady, Message: "github integration is enabled but no authentication method is configured", Details: details}
	}
	if !githubTokenEncryptionAvailable() {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusNotReady, Message: "github token encryption key is not configured", Details: details}
	}
	if settings.checksEnabled && !settings.appConfigured {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusDegraded, Message: "github checks are enabled but GitHub App authentication is not configured", Details: details}
	}
	if settings.webhookSecretEnvVarPresent && !settings.webhookConfigured {
		return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusDegraded, Message: "github webhook secret env var is set but the secret value is unavailable", Details: details}
	}
	return SystemReadinessCheck{Name: "github_enterprise", Status: readinessStatusReady, Message: "github enterprise integration is configured", Details: details}
}

func (s *Server) schedulerReadiness() SystemReadinessCheck {
	return schedulerReadinessFromSnapshot(s.runtimeReadinessSnapshot())
}

func schedulerReadinessFromSnapshot(snapshot runtimeReadinessSnapshot) SystemReadinessCheck {
	if !snapshot.schedulerInitialized {
		return SystemReadinessCheck{
			Name:    "scheduler_state",
			Status:  readinessStatusNotReady,
			Message: "scenario scheduler is not initialized",
		}
	}
	status := readinessStatusReady
	message := "scenario scheduler is initialized"
	if snapshot.erroredSchedules > 0 {
		status = readinessStatusDegraded
		message = "scenario scheduler has schedules with previous errors"
	}
	return SystemReadinessCheck{
		Name:    "scheduler_state",
		Status:  status,
		Message: message,
		Details: map[string]any{
			"enabled_schedules": snapshot.enabledSchedules,
			"due_schedules":     snapshot.dueSchedules,
			"errored_schedules": snapshot.erroredSchedules,
			"ha_mode":           "single-active-controller",
		},
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
