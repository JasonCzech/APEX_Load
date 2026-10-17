package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

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
	refreshRuntimeReadinessForTest(t, server)

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
	kubernetes := assertReadinessCheck(t, payload, "kubernetes_api", readinessStatusReady)
	if kubernetes.Details["configured"] != false {
		t.Fatalf("Kubernetes configured = %#v, want false for local controller", kubernetes.Details["configured"])
	}
	assertReadinessCheck(t, payload, "metadata_store", readinessStatusReady)
	metadata := assertReadinessCheck(t, payload, "metadata_store", readinessStatusReady)
	if metadata.Details["store_type"] != storage.StoreBackendSQLite {
		t.Fatalf("metadata store type = %#v, want sqlite", metadata.Details["store_type"])
	}
	if _, ok := metadata.Details["probe_latency_ms"]; !ok {
		t.Fatalf("metadata store probe latency missing: %#v", metadata.Details)
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

func TestKubernetesAPIReadinessReportsHealthyPodInventory(t *testing.T) {
	const selectorSecret = "readiness-selector-sentinel-secret"
	selector := "app.kubernetes.io/name=apexload-generator,sentinel=" + selectorSecret
	restarter := &fakeKubeRestarter{pods: []KubePodRef{
		{Name: "apexload-generator-0", Namespace: "apexload", Ready: true},
		{Name: "apexload-generator-1", Namespace: "apexload", Ready: false, Terminating: true},
	}}
	server := NewServer(ServerConfig{
		MaxConcurrent: 2,
		K8sRestart: K8sRestartSettings{
			Enabled:           true,
			Namespace:         "apexload",
			GeneratorSelector: selector,
		},
		KubeRestarter: restarter,
	})

	check := server.kubernetesAPIReadiness(context.Background())
	if check.Status != readinessStatusReady {
		t.Fatalf("status = %q, want ready check=%#v", check.Status, check)
	}
	if check.Details["configured"] != true || check.Details["pod_count"] != 2 || check.Details["ready_pods"] != 1 || check.Details["terminating_pods"] != 1 {
		t.Fatalf("Kubernetes readiness details = %#v", check.Details)
	}
	if calls := restarter.listCalls(); len(calls) != 1 || calls[0].namespace != "apexload" || calls[0].selector != selector {
		t.Fatalf("pod list calls = %#v", calls)
	}
	payload, err := json.Marshal(check)
	if err != nil {
		t.Fatalf("marshal Kubernetes readiness: %v", err)
	}
	if strings.Contains(string(payload), selectorSecret) {
		t.Fatalf("anonymous Kubernetes readiness exposed generator selector: %s", payload)
	}
}

func TestKubernetesAPIReadinessFailuresRemainDegraded(t *testing.T) {
	tests := []struct {
		name       string
		restarter  KubeRestarter
		wantReason string
	}{
		{name: "client unavailable", wantReason: "client_unavailable"},
		{name: "RBAC or API failure", restarter: &fakeKubeRestarter{listErr: errors.New("forbidden")}, wantReason: "probe_failed"},
		{name: "probe timeout", restarter: blockingReadinessKubeRestarter{}, wantReason: "probe_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{
				MaxConcurrent: 2,
				K8sRestart: K8sRestartSettings{
					Enabled:           true,
					Namespace:         "apexload",
					GeneratorSelector: "app=generator",
				},
				KubeRestarter: test.restarter,
			})
			ctx := context.Background()
			if test.wantReason == "probe_timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Millisecond)
				defer cancel()
			}
			check := server.kubernetesAPIReadiness(ctx)
			if check.Status != readinessStatusDegraded {
				t.Fatalf("status = %q, want degraded check=%#v", check.Status, check)
			}
			if check.Details["reason"] != test.wantReason {
				t.Fatalf("reason = %#v, want %q details=%#v", check.Details["reason"], test.wantReason, check.Details)
			}
		})
	}
}

func TestIncompleteKubernetesReadinessRemainsNeutralWhenRestartControlsDisabled(t *testing.T) {
	server := &Server{}

	check := server.incompleteReadinessCheck("kubernetes_api", "probe_timeout")
	if check.Status != readinessStatusReady || check.Message != "Kubernetes integration is not configured for this runtime" {
		t.Fatalf("disabled Kubernetes fallback = %#v, want neutral ready status", check)
	}
	if check.Details["enabled"] != false || check.Details["configured"] != false {
		t.Fatalf("disabled Kubernetes details = %#v", check.Details)
	}
	if _, ok := check.Details["reason"]; ok {
		t.Fatalf("disabled Kubernetes fallback retained a failure reason: %#v", check.Details)
	}
}

func TestSystemReadinessListsKubernetesPodsOnlyOnceWhenProbeTimesOut(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	restarter := &countingBlockingReadinessKubeRestarter{}
	server := NewServer(ServerConfig{
		MaxConcurrent: 2,
		Addr:          ":8080",
		Store:         store,
		K8sRestart: K8sRestartSettings{
			Enabled:           true,
			Namespace:         "apexload",
			GeneratorSelector: "app=generator",
		},
		KubeRestarter: restarter,
	})
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{{
		Target: "generator-0:9090", GeneratorID: "generator-0", Status: "connected",
	}}})
	refreshRuntimeReadinessForTest(t, server)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	response := server.systemReadiness(ctx)
	kubernetes := assertReadinessCheck(t, response, "kubernetes_api", readinessStatusDegraded)
	if kubernetes.Details["reason"] != "probe_timeout" {
		t.Fatalf("Kubernetes reason = %#v, want probe_timeout", kubernetes.Details["reason"])
	}
	if got := restarter.callCount(); got != 1 {
		t.Fatalf("Kubernetes pod-list calls = %d, want exactly one per readiness response", got)
	}
}

func TestSystemReadinessBoundsRealDistributedInventoryProbe(t *testing.T) {
	if defaultSystemReadinessProbeTimeout >= 3*time.Second {
		t.Fatalf("default readiness timeout = %s, want below the shipped three-second probe timeout", defaultSystemReadinessProbeTimeout)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	server.staticFS = readinessTestStaticFS()
	backend := newDistributedExecutionBackend(
		[]string{"generator-0:9090"},
		10*time.Second,
		func(ctx context.Context, _ string) (generatorClient, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	server.runner.setExecutionBackend(backend)
	refreshRuntimeReadinessForTest(t, server)

	const responseBudget = 50 * time.Millisecond
	started := time.Now()
	response := server.systemReadinessWithin(context.Background(), responseBudget)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("readiness response took %s with a %s shared budget", elapsed, responseBudget)
	}
	generator := assertReadinessCheck(t, response, "generator_rpc", readinessStatusNotReady)
	if generator.Details["reason"] != "probe_timeout" {
		t.Fatalf("generator timeout details = %#v, want probe_timeout", generator.Details)
	}
}

func TestSystemReadinessDoesNotWaitForSettingsRefreshLock(t *testing.T) {
	server := newRunnerLockReadinessTestServer(t)
	// Warm every readiness cache and publish the current immutable runner copy
	// before simulating a concurrent settings refresh.
	server.systemReadinessWithin(context.Background(), time.Second)

	server.runner.settingsRefreshMu.Lock()
	result := make(chan struct {
		response SystemReadinessResponse
		elapsed  time.Duration
	}, 1)
	go func() {
		started := time.Now()
		response := server.systemReadinessWithin(context.Background(), 50*time.Millisecond)
		result <- struct {
			response SystemReadinessResponse
			elapsed  time.Duration
		}{response: response, elapsed: time.Since(started)}
	}()

	select {
	case got := <-result:
		server.runner.settingsRefreshMu.Unlock()
		if got.elapsed > 500*time.Millisecond {
			t.Fatalf("readiness took %s while settings refresh lock was held", got.elapsed)
		}
		assertReadinessCheck(t, got.response, "telemetry_fanout", readinessStatusReady)
	case <-time.After(time.Second):
		server.runner.settingsRefreshMu.Unlock()
		<-result
		t.Fatal("readiness blocked on settings refresh lock past its response budget")
	}
}

func TestSystemReadinessDoesNotMultiplyRunnerLockWaiters(t *testing.T) {
	server := newRunnerLockReadinessTestServer(t)
	// Publish a known-good settings/backend/scheduler snapshot before a durable
	// write takes the runner lock.
	server.systemReadinessWithin(context.Background(), time.Second)

	server.runner.mu.Lock()
	locked := true
	defer func() {
		if locked {
			server.runner.mu.Unlock()
		}
	}()

	const callers = 16
	responses := make(chan SystemReadinessResponse, callers)
	for index := 0; index < callers; index++ {
		go func() {
			responses <- server.systemReadinessWithin(context.Background(), 50*time.Millisecond)
		}()
	}

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for index := 0; index < callers; index++ {
		select {
		case response := <-responses:
			assertReadinessCheck(t, response, "generator_rpc", readinessStatusReady)
			assertReadinessCheck(t, response, "scheduler_state", readinessStatusReady)
		case <-timer.C:
			server.runner.mu.Unlock()
			locked = false
			t.Fatalf("only %d/%d readiness requests returned while runner lock was held", index, callers)
		}
	}

	// Every request has returned while the write lock remains held. Any probe
	// still visible in one of these lock-taking frames is therefore a stranded
	// per-request goroutine rather than useful work.
	stacks := allReadinessTestGoroutineStacks()
	for _, marker := range []string{
		"(*TestRunner).systemSettingsSnapshot",
		"(*TestRunner).getExecutionBackend",
		"(*Server).schedulerCounts",
	} {
		if strings.Contains(stacks, marker) {
			t.Errorf("readiness left a runner-lock waiter after requests returned; stack contains %s", marker)
		}
	}

	server.runner.mu.Unlock()
	locked = false
}

func TestSystemReadinessUsesPublishedSnapshotForLargeCatalog(t *testing.T) {
	server := newRunnerLockReadinessTestServer(t)

	const (
		scheduleCount    = 250_000
		reservationCount = 100_000
	)
	schedule := &storage.ScenarioSchedule{Enabled: true}
	schedules := make([]*storage.ScenarioSchedule, scheduleCount)
	for index := range schedules {
		schedules[index] = schedule
	}
	reservations := make([]CapacityReservation, reservationCount)
	for index := range reservations {
		reservations[index] = CapacityReservation{
			ProjectID:          "large-catalog-project",
			ReservedGenerators: 1,
		}
	}
	server.runner.mu.Lock()
	server.runner.scenarios["large-readiness-catalog"] = &StoredScenario{
		ID:        "large-readiness-catalog",
		Schedules: schedules,
	}
	server.runner.settings.CapacityPlanning.Reservations = reservations
	server.runner.resetSchedulerReadinessLocked(time.Now().UTC())
	server.runner.mu.Unlock()
	published := server.runner.readinessRuntime.Load()
	if published == nil || published.enabledSchedules != scheduleCount {
		t.Fatalf("published readiness projection = %#v, want %d enabled schedules", published, scheduleCount)
	}
	if got := server.readinessSystemSettingsSnapshot().metricsIntervalSeconds; got <= 0 {
		t.Fatalf("direct readiness projection metrics interval = %d, want positive", got)
	}

	// Keep the unbounded source fields and the runner write lock live while the
	// request runs. Changing the live source without publishing makes the
	// projection assertion deterministic: a direct catalog scan would report zero.
	// The timeout checks lock independence, not sub-millisecond OS scheduling.
	server.runner.mu.Lock()
	schedule.Enabled = false

	const responseBudget = 100 * time.Millisecond
	requestStarted := time.Now()
	response := server.systemReadinessWithin(context.Background(), responseBudget)
	server.runner.mu.Unlock()
	if elapsed := time.Since(requestStarted); elapsed > time.Second {
		t.Fatalf("readiness response took %s with large settings and schedule catalogs", elapsed)
	}
	if len(response.Checks) == 0 {
		t.Fatal("readiness response omitted checks")
	}
	scheduler := assertReadinessCheck(t, response, "scheduler_state", readinessStatusReady)
	if scheduler.Details["enabled_schedules"] != scheduleCount {
		t.Fatalf("scheduler projection = %#v, want %d enabled schedules", scheduler.Details, scheduleCount)
	}
}

func TestRuntimeReadinessProjectionPublishesCombinedMutations(t *testing.T) {
	server := newRunnerLockReadinessTestServer(t)
	settings := server.runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 7
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	backend := stubInventoryBackend{entries: []GeneratorInventoryEntry{{Target: "generator-0:9090", Status: "connected"}}}
	server.runner.setExecutionBackend(backend)
	now := time.Now().UTC()
	dueAt := now.Add(-time.Minute)
	schedule := &storage.ScenarioSchedule{ID: "readiness-schedule", Enabled: true, NextRunAt: &dueAt, LastError: "previous failure"}
	server.runner.mu.Lock()
	server.runner.scenarios["readiness-scenario"] = &StoredScenario{ID: "readiness-scenario", Schedules: []*storage.ScenarioSchedule{schedule}}
	server.runner.adjustSchedulerReadinessLocked(nil, schedule)
	server.runner.mu.Unlock()

	snapshot := server.runtimeReadinessSnapshot()
	if snapshot.settings.metricsIntervalSeconds != 7 || snapshot.execution == nil {
		t.Fatalf("combined readiness projection = %#v", snapshot)
	}
	if snapshot.enabledSchedules != 1 || snapshot.dueSchedules != 1 || snapshot.erroredSchedules != 1 {
		t.Fatalf("scheduler projection = %#v, want enabled/due/errored 1", snapshot)
	}

	settings = server.runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 9
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("second UpdateSystemSettings: %v", err)
	}
	afterSettings := server.runtimeReadinessSnapshot()
	if afterSettings.settings.metricsIntervalSeconds != 9 || afterSettings.enabledSchedules != 1 || afterSettings.execution == nil {
		t.Fatalf("settings publication overwrote another readiness axis: %#v", afterSettings)
	}
}

func TestSchedulerReadinessDeltaUsesStableEvaluationTime(t *testing.T) {
	server := newRunnerLockReadinessTestServer(t)
	evaluatedAt := time.Now().UTC()
	future := evaluatedAt.Add(time.Hour)
	past := evaluatedAt.Add(-time.Hour)
	before := &storage.ScenarioSchedule{ID: "schedule", Enabled: true, NextRunAt: &future}
	after := &storage.ScenarioSchedule{ID: "schedule", Enabled: true, NextRunAt: &past}
	server.runner.mu.Lock()
	server.runner.readinessScheduler = schedulerReadinessProjection{evaluatedAt: evaluatedAt, enabledSchedules: 1}
	server.runner.adjustSchedulerReadinessLocked(before, after)
	snapshot := server.runner.readinessRuntime.Load()
	server.runner.mu.Unlock()
	if snapshot == nil || snapshot.enabledSchedules != 1 || snapshot.dueSchedules != 1 {
		t.Fatalf("stable-time scheduler delta = %#v, want one due schedule", snapshot)
	}
}

func TestFollowerSchedulerAdvancesBoundedReadinessDueTransitions(t *testing.T) {
	runner := notLeaderRunner(t)
	runner.mu.Lock()
	evaluatedAt := runner.readinessScheduler.evaluatedAt
	dueAt := evaluatedAt.Add(time.Minute)
	const overflow = 11
	for index := 0; index < maxSchedulerReadinessTransitions+overflow; index++ {
		schedule := &storage.ScenarioSchedule{
			ID:        fmt.Sprintf("follower-readiness-%d", index),
			Enabled:   true,
			NextRunAt: &dueAt,
		}
		runner.adjustSchedulerReadinessCountersLocked(nil, schedule)
	}
	runner.publishRuntimeReadinessLocked(time.Now().UTC())
	runner.mu.Unlock()

	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return dueAt.Add(time.Minute) }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("follower readiness tick: %v", err)
	}
	first := runner.readinessRuntime.Load()
	if first == nil || first.dueSchedules != maxSchedulerReadinessTransitions || len(runner.readinessScheduler.futureSchedules) != overflow {
		t.Fatalf("first bounded follower transition = %#v future=%d, want due=%d backlog=%d", first, len(runner.readinessScheduler.futureSchedules), maxSchedulerReadinessTransitions, overflow)
	}
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatalf("follower readiness continuation: %v", err)
	}
	second := runner.readinessRuntime.Load()
	if second == nil || second.dueSchedules != maxSchedulerReadinessTransitions+overflow || len(runner.readinessScheduler.futureSchedules) != 0 {
		t.Fatalf("second follower transition = %#v future=%d, want complete due count", second, len(runner.readinessScheduler.futureSchedules))
	}
}

type controlledContextSettingsStore struct {
	storage.Store
	contextual contextualSettingsPersistence

	mu           sync.Mutex
	calls        int
	callSignal   chan struct{}
	blocking     bool
	blockedCalls int
	blocked      chan struct{}
}

func newControlledContextSettingsStore(store *storage.SQLiteStore) *controlledContextSettingsStore {
	return &controlledContextSettingsStore{
		Store:      store,
		contextual: store,
		callSignal: make(chan struct{}, 32),
	}
}

func (s *controlledContextSettingsStore) GetSettingContext(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	s.calls++
	blocking := s.blocking
	blocked := s.blocked
	if blocking {
		s.blockedCalls++
		select {
		case <-blocked:
		default:
			close(blocked)
		}
	}
	s.mu.Unlock()
	select {
	case s.callSignal <- struct{}{}:
	default:
	}
	if blocking {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return s.contextual.GetSettingContext(ctx, key)
}

func (s *controlledContextSettingsStore) beginBlocking() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocking = true
	s.blocked = make(chan struct{})
	return s.blocked
}

func (s *controlledContextSettingsStore) callCounts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.blockedCalls
}

func drainReadinessSettingsSignals(signals <-chan struct{}) {
	for {
		select {
		case <-signals:
		default:
			return
		}
	}
}

func TestStartRefreshesDurableReadinessSettingsBeforeFirstResponse(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := newControlledContextSettingsStore(base)
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: "127.0.0.1:0", Store: store})
	server.staticFS = readinessTestStaticFS()

	settings := server.runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 17
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal durable settings: %v", err)
	}
	if err := store.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
		t.Fatalf("save durable settings: %v", err)
	}
	drainReadinessSettingsSignals(store.callSignal)

	var startErr error
	startDone := make(chan struct{})
	runnerLocked := false
	unlockRunner := func() {
		if runnerLocked {
			server.runner.mu.Unlock()
			runnerLocked = false
		}
	}
	// Register teardown before taking the lock or launching Start so every fatal
	// assertion stops listeners and workers before the underlying store closes.
	// A closed completion channel can be observed by both the test and cleanup.
	t.Cleanup(func() {
		unlockRunner()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("cleanup Shutdown: %v", err)
		}
		select {
		case <-startDone:
		case <-shutdownCtx.Done():
			t.Errorf("cleanup waiting for Start: %v", shutdownCtx.Err())
		}
	})

	// Force the startup refresh to exercise its context-bounded TryLock retry.
	// A one-shot opportunistic apply would miss this update and serve stale data.
	server.runner.mu.Lock()
	runnerLocked = true
	go func() {
		startErr = server.Start()
		close(startDone)
	}()
	select {
	case <-store.callSignal:
	case <-time.After(time.Second):
		unlockRunner()
		t.Fatal("startup settings refresh did not read the contextual store")
	}
	if server.BoundAddrReady() {
		unlockRunner()
		t.Fatal("server published its listener before applying startup settings")
	}
	unlockRunner()

	deadline := time.Now().Add(3 * time.Second)
	for !server.BoundAddrReady() && time.Now().Before(deadline) {
		select {
		case <-startDone:
			t.Fatalf("Start returned before binding: %v", startErr)
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !server.BoundAddrReady() {
		t.Fatal("server did not publish a listener")
	}

	response, err := http.Get("http://" + server.BoundAddr() + "/api/v1/system/readiness") //nolint:gosec // loopback test server
	if err != nil {
		t.Fatalf("GET first readiness response: %v", err)
	}
	defer response.Body.Close()
	var payload SystemReadinessResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode first readiness response: %v", err)
	}
	telemetry := assertReadinessCheck(t, payload, "telemetry_fanout", readinessStatusReady)
	if telemetry.Details["metrics_interval_seconds"] != float64(17) {
		t.Fatalf("first served readiness settings = %#v, want durable interval 17", telemetry.Details)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-startDone:
		if startErr != nil {
			t.Fatalf("Start after Shutdown: %v", startErr)
		}
	case <-shutdownCtx.Done():
		t.Fatalf("waiting for Start after Shutdown: %v", shutdownCtx.Err())
	}
}

func TestStartFailsClosedWhenDurableReadinessSettingsCannotRefresh(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	store := newControlledContextSettingsStore(base)
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: "127.0.0.1:0", Store: store})
	blocked := store.beginBlocking()

	started := time.Now()
	err = server.Start()
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start error = %v, want bounded durable-settings deadline failure", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("failed startup took %s, want bounded refresh and rollback", elapsed)
	}
	select {
	case <-blocked:
	default:
		t.Fatal("startup did not attempt the contextual durable-settings read")
	}
	if server.BoundAddrReady() {
		t.Fatal("server published a listener after startup settings refresh failed")
	}
}

func TestReadinessSettingsCadenceConvergesSharedStore(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	writer := NewServer(ServerConfig{MaxConcurrent: 2, Store: base})
	standby := NewServer(ServerConfig{MaxConcurrent: 2, Store: base})

	settings := writer.runner.GetSystemSettings()
	settings.General.MetricsIntervalSeconds = 23
	if _, err := writer.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("writer UpdateSystemSettings: %v", err)
	}
	if got := standby.runtimeReadinessSnapshot().settings.metricsIntervalSeconds; got == 23 {
		t.Fatal("standby unexpectedly observed shared settings before its cadence refresh")
	}
	standby.runner.settingsRefreshMu.Lock()
	standby.runner.settingsAuthRefreshAt = time.Time{}
	standby.runner.settingsRefreshMu.Unlock()
	standby.startReadinessSettingsRefreshCadence()

	deadline := time.Now().Add(time.Second)
	for standby.runtimeReadinessSnapshot().settings.metricsIntervalSeconds != 23 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := standby.runtimeReadinessSnapshot().settings.metricsIntervalSeconds; got != 23 {
		t.Fatalf("standby readiness interval = %d, want shared durable value 23", got)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := standby.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("standby Shutdown: %v", err)
	}
	if err := writer.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("writer Shutdown: %v", err)
	}
}

func TestReadinessSettingsCadenceSkipsRunnerContentionAndStops(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	store := newControlledContextSettingsStore(base)
	server := NewServer(ServerConfig{MaxConcurrent: 2, Store: store})
	drainReadinessSettingsSignals(store.callSignal)
	server.runner.settingsRefreshMu.Lock()
	server.runner.settingsAuthRefreshAt = time.Time{}
	server.runner.settingsRefreshMu.Unlock()

	server.runner.mu.Lock()
	server.startReadinessSettingsRefreshCadence()
	select {
	case <-store.callSignal:
	case <-time.After(time.Second):
		server.runner.mu.Unlock()
		t.Fatal("readiness settings cadence did not read the store")
	}
	deadline := time.Now().Add(time.Second)
	for {
		server.runner.settingsRefreshMu.Lock()
		inFlight := server.runner.settingsRefreshInFlight != nil
		server.runner.settingsRefreshMu.Unlock()
		if !inFlight || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	server.runner.settingsRefreshMu.Lock()
	inFlight := server.runner.settingsRefreshInFlight != nil
	server.runner.settingsRefreshMu.Unlock()
	server.runner.mu.Unlock()
	if inFlight {
		t.Fatal("cadence waited on runner.mu instead of skipping contended apply")
	}

	// Concurrent start attempts and Shutdown exercise ownership of the cadence
	// state; -race must see no unsynchronized cancel-field access.
	var starters sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < 16; index++ {
		starters.Add(1)
		go func() {
			defer starters.Done()
			<-start
			server.startReadinessSettingsRefreshCadence()
		}()
	}
	close(start)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	starters.Wait()
	server.startReadinessSettingsRefreshCadence()
	server.readinessSettingsMu.Lock()
	state := server.readinessSettingsCadence
	server.readinessSettingsMu.Unlock()
	if state != nil {
		t.Fatal("readiness cadence survived or restarted after Shutdown")
	}
}

func TestReadinessSettingsCadenceCancelsBlockedContextualRead(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	store := newControlledContextSettingsStore(base)
	server := NewServer(ServerConfig{MaxConcurrent: 2, Store: store})
	server.runner.settingsRefreshMu.Lock()
	server.runner.settingsAuthRefreshAt = time.Time{}
	server.runner.settingsRefreshMu.Unlock()
	blocked := store.beginBlocking()
	server.startReadinessSettingsRefreshCadence()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("cadence did not enter the blocked contextual settings read")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown did not cancel the settings read: %v", err)
	}
	_, blockedCalls := store.callCounts()
	if blockedCalls != 1 {
		t.Fatalf("blocked contextual settings calls = %d, want one tracked worker", blockedCalls)
	}
	server.startReadinessSettingsRefreshCadence()
	_, afterShutdown := store.callCounts()
	if afterShutdown != blockedCalls {
		t.Fatalf("post-Shutdown cadence calls = %d, want unchanged %d", afterShutdown, blockedCalls)
	}
}

func TestReadinessSettingsCadenceConcurrentStartAndShutdown(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{MaxConcurrent: 2, Store: base})
	server.runner.settingsRefreshMu.Lock()
	server.runner.settingsAuthRefreshAt = time.Time{}
	server.runner.settingsRefreshMu.Unlock()

	start := make(chan struct{})
	started := make(chan struct{})
	go func() {
		<-start
		server.startReadinessSettingsRefreshCadence()
		close(started)
	}()
	shutdownResult := make(chan error, 1)
	go func() {
		<-start
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownResult <- server.Shutdown(ctx)
	}()
	close(start)
	<-started
	if err := <-shutdownResult; err != nil {
		t.Fatalf("concurrent Shutdown: %v", err)
	}
	server.readinessSettingsMu.Lock()
	state := server.readinessSettingsCadence
	server.readinessSettingsMu.Unlock()
	if state != nil {
		t.Fatal("concurrent cadence start resurrected work after Shutdown")
	}
}

func newRunnerLockReadinessTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: store})
	server.staticFS = readinessTestStaticFS()
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	refreshRuntimeReadinessForTest(t, server)
	return server
}

func refreshRuntimeReadinessForTest(t *testing.T, server *Server) {
	t.Helper()
	if server == nil || server.runner == nil || server.runner.readinessRuntime.Load() == nil {
		t.Fatal("runtime readiness projection was not published")
	}
}

func allReadinessTestGoroutineStacks() string {
	size := 1 << 20
	for {
		buffer := make([]byte, size)
		written := runtime.Stack(buffer, true)
		if written < len(buffer) {
			return string(buffer[:written])
		}
		size *= 2
	}
}

func TestCachedGeneratorPodsNegativeCachesSanitizedFailure(t *testing.T) {
	const secret = "service-account-secret"
	restarter := &fakeKubeRestarter{listErr: errors.New("forbidden: bearer " + secret)}
	server := NewServer(ServerConfig{
		MaxConcurrent: 2,
		K8sRestart: K8sRestartSettings{
			Enabled:           true,
			Namespace:         "apexload",
			GeneratorSelector: "app=generator",
		},
		KubeRestarter: restarter,
	})

	for attempt := 0; attempt < 2; attempt++ {
		_, err := server.cachedGeneratorPods(context.Background())
		if !errors.Is(err, errGeneratorPodInventoryProbeFailed) {
			t.Fatalf("attempt %d error = %v, want sanitized pod-inventory failure", attempt, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("attempt %d exposed Kubernetes credentials: %v", attempt, err)
		}
	}
	if calls := len(restarter.listCalls()); calls != 1 {
		t.Fatalf("pod-list calls within negative TTL = %d, want 1", calls)
	}

	server.kubePodsCacheMu.Lock()
	server.kubePodsCache.expiresAt = time.Now().Add(-time.Second)
	server.kubePodsCacheMu.Unlock()
	if _, err := server.cachedGeneratorPods(context.Background()); !errors.Is(err, errGeneratorPodInventoryProbeFailed) {
		t.Fatalf("error after negative-cache expiry = %v", err)
	}
	if calls := len(restarter.listCalls()); calls != 2 {
		t.Fatalf("pod-list calls after negative-cache expiry = %d, want 2", calls)
	}
}

func TestCachedGeneratorPodsSharesConcurrentFailure(t *testing.T) {
	restarter := &blockingFailingReadinessKubeRestarter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	server := NewServer(ServerConfig{
		MaxConcurrent: 2,
		K8sRestart: K8sRestartSettings{
			Enabled:           true,
			Namespace:         "apexload",
			GeneratorSelector: "app=generator",
		},
		KubeRestarter: restarter,
	})

	const callers = 8
	errs := make(chan error, callers)
	go func() {
		_, err := server.cachedGeneratorPods(context.Background())
		errs <- err
	}()
	<-restarter.started
	for index := 1; index < callers; index++ {
		go func() {
			_, err := server.cachedGeneratorPods(context.Background())
			errs <- err
		}()
	}
	close(restarter.release)
	for index := 0; index < callers; index++ {
		if err := <-errs; !errors.Is(err, errGeneratorPodInventoryProbeFailed) {
			t.Fatalf("caller %d error = %v, want shared sanitized failure", index, err)
		}
	}
	if calls := restarter.callCount(); calls != 1 {
		t.Fatalf("concurrent pod-list calls = %d, want 1", calls)
	}
}

func TestMetadataStoreReadinessHonorsCanceledContext(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{MaxConcurrent: 2, Store: store})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	check := server.metadataStoreReadiness(ctx)
	if check.Status != readinessStatusNotReady || check.Details["reason"] != "probe_canceled" {
		t.Fatalf("metadata readiness = %#v, want canceled not_ready", check)
	}
}

type postgresReadinessStoreDecorator struct {
	storage.Store
}

func (*postgresReadinessStoreDecorator) BackendInfo() storage.StoreBackendInfo {
	return storage.StoreBackendInfo{
		Type:             storage.StoreBackendPostgres,
		Driver:           "github.com/jackc/pgx/v5/stdlib",
		MigrationVersion: "test-schema",
		LeaseCapability:  true,
	}
}

func TestMetadataStoreReadinessSupportsStoreDecoratorAndReportsPostgres(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	// Embed the public storage.Store interface, intentionally hiding the
	// concrete store's optional GetSettingContext method. Valid decorators must
	// retain readiness compatibility through the bounded legacy path.
	server := &Server{store: &postgresReadinessStoreDecorator{Store: store}}
	check := server.metadataStoreReadiness(context.Background())
	if check.Status != readinessStatusReady {
		t.Fatalf("decorated PostgreSQL readiness = %#v, want ready", check)
	}
	if check.Details["store_type"] != storage.StoreBackendPostgres {
		t.Fatalf("PostgreSQL provider details = %#v", check.Details)
	}
	if _, ok := check.Details["driver"]; ok {
		t.Fatalf("anonymous PostgreSQL readiness exposed driver metadata: %#v", check.Details)
	}
	if _, ok := check.Details["migration_version"]; ok {
		t.Fatalf("anonymous PostgreSQL readiness exposed migration metadata: %#v", check.Details)
	}
	if _, ok := check.Details["probe_latency_ms"].(float64); !ok {
		t.Fatalf("PostgreSQL probe latency missing or has unexpected type: %#v", check.Details)
	}
}

type injectedMetadataBackendInfoStore struct {
	storage.Store
	info storage.StoreBackendInfo
}

func (s *injectedMetadataBackendInfoStore) BackendInfo() storage.StoreBackendInfo {
	return s.info
}

func TestMetadataStoreReadinessSanitizesInjectedBackendInfo(t *testing.T) {
	const secret = "metadata-readiness-sentinel-secret"
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer base.Close()

	for _, tc := range []struct {
		name      string
		storeType string
		wantLabel string
	}{
		{name: "untrusted provider label", storeType: "enterprise-" + secret, wantLabel: "unknown"},
		{name: "known provider with untrusted metadata", storeType: storage.StoreBackendPostgres, wantLabel: storage.StoreBackendPostgres},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{store: &injectedMetadataBackendInfoStore{
				Store: base,
				info: storage.StoreBackendInfo{
					Type:             tc.storeType,
					Driver:           "driver://" + secret,
					MigrationVersion: "migration-" + secret,
					DSN:              "postgres://user:" + secret + "@db.example/apexload",
				},
			}}
			check := server.metadataStoreReadiness(context.Background())
			if check.Status != readinessStatusReady {
				t.Fatalf("metadata readiness = %#v, want ready", check)
			}
			if check.Details["store_type"] != tc.wantLabel {
				t.Fatalf("injected metadata provider label = %#v, want %q", check.Details, tc.wantLabel)
			}
			for _, key := range []string{"driver", "migration_version"} {
				if _, ok := check.Details[key]; ok {
					t.Fatalf("anonymous metadata readiness exposed %s: %#v", key, check.Details)
				}
			}
			payload, err := json.Marshal(check)
			if err != nil {
				t.Fatalf("marshal metadata readiness: %v", err)
			}
			if strings.Contains(string(payload), secret) {
				t.Fatalf("metadata readiness exposed injected backend secret: %s", payload)
			}
		})
	}
}

type blockingLegacyMetadataReadinessStore struct {
	Store       storage.Store
	mu          sync.Mutex
	calls       int
	startedOnce sync.Once
	started     chan struct{}
	release     chan struct{}
}

func (s *blockingLegacyMetadataReadinessStore) GetSetting(string) (string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.startedOnce.Do(func() { close(s.started) })
	<-s.release
	return "", nil
}

func (s *blockingLegacyMetadataReadinessStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestMetadataStoreReadinessDoesNotMultiplyBlockedLegacyProbes(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer base.Close()

	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	store := &blockingLegacyMetadataReadinessStore{
		Store:   base,
		started: make(chan struct{}),
		release: release,
	}
	server := &Server{store: storeOverlay(store)}

	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		check := server.metadataStoreReadiness(ctx)
		cancel()
		if check.Status != readinessStatusNotReady || check.Details["reason"] != "probe_timeout" {
			t.Fatalf("attempt %d metadata readiness = %#v, want bounded timeout", attempt, check)
		}
	}
	if got := store.callCount(); got != 1 {
		t.Fatalf("blocked legacy metadata calls = %d, want one shared in-flight probe", got)
	}

	releaseOnce.Do(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if check := server.metadataStoreReadiness(ctx); check.Status != readinessStatusReady {
		t.Fatalf("metadata readiness after release = %#v, want cached ready result", check)
	}
}

type blockingSystemReadinessSettingsStore struct {
	Store        storage.Store
	mu           sync.Mutex
	blocking     bool
	blockedCalls int
	startedOnce  sync.Once
	started      chan struct{}
	release      chan struct{}
	finished     chan struct{}
}

func (s *blockingSystemReadinessSettingsStore) GetSetting(key string) (string, error) {
	s.mu.Lock()
	blocking := s.blocking
	if blocking {
		s.blockedCalls++
	}
	s.mu.Unlock()
	if !blocking {
		return s.Store.GetSetting(key)
	}
	s.startedOnce.Do(func() { close(s.started) })
	<-s.release
	raw, err := s.Store.GetSetting(key)
	s.finished <- struct{}{}
	return raw, err
}

func (s *blockingSystemReadinessSettingsStore) beginBlocking() {
	s.mu.Lock()
	s.blocking = true
	s.mu.Unlock()
}

func (s *blockingSystemReadinessSettingsStore) blockedCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.blockedCalls
}

func TestSystemReadinessDoesNotMultiplyBlockedSettingsReads(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer base.Close()

	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	store := &blockingSystemReadinessSettingsStore{
		Store:    base,
		started:  make(chan struct{}),
		release:  release,
		finished: make(chan struct{}, 4),
	}
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080", Store: storeOverlay(store)})
	server.staticFS = readinessTestStaticFS()
	store.beginBlocking()

	for attempt := 0; attempt < 2; attempt++ {
		// Make a contextless GetSystemSettings call eligible on every pass. Full
		// readiness must still use its single active-process snapshot and leave
		// the only storage read to the serialized metadata probe.
		server.runner.settingsRefreshMu.Lock()
		server.runner.settingsAuthRefreshAt = time.Now().Add(-2 * systemSettingsAuthRefreshInterval)
		server.runner.settingsRefreshMu.Unlock()

		response := server.systemReadinessWithin(context.Background(), 25*time.Millisecond)
		metadata := assertReadinessCheck(t, response, "metadata_store", readinessStatusNotReady)
		if metadata.Details["reason"] != "probe_timeout" {
			t.Fatalf("attempt %d metadata readiness = %#v, want bounded timeout", attempt, metadata)
		}
	}

	select {
	case <-store.started:
	default:
		t.Fatal("metadata settings probe never reached the blocked store")
	}
	if got := store.blockedCallCount(); got != 1 {
		t.Fatalf("blocked settings reads = %d, want one serialized metadata probe", got)
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case <-store.finished:
	case <-time.After(time.Second):
		t.Fatal("blocked metadata settings probe did not finish after release")
	}
}

func TestArchiveReadinessDoesNotMultiplyBlockedFilesystemProbes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var calls int
	var startedOnce sync.Once
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	server := &Server{}
	server.archiveReadinessProbe = func() SystemReadinessCheck {
		mu.Lock()
		calls++
		mu.Unlock()
		startedOnce.Do(func() { close(started) })
		<-release
		return SystemReadinessCheck{Name: "archive_writer", Status: readinessStatusReady, Message: "test archive ready"}
	}

	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		check := server.archiveWriterReadiness(ctx)
		cancel()
		if check.Status != readinessStatusNotReady || check.Details["reason"] != "probe_timeout" {
			t.Fatalf("attempt %d archive readiness = %#v, want bounded timeout", attempt, check)
		}
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("blocked archive probe calls = %d, want one shared in-flight probe", gotCalls)
	}

	releaseOnce.Do(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if check := server.archiveWriterReadiness(ctx); check.Status != readinessStatusReady {
		t.Fatalf("archive readiness after release = %#v, want cached ready result", check)
	}
}

func TestCacheReadinessNeverExposesMalformedURLCredentials(t *testing.T) {
	const secret = "sentinel-password"
	t.Setenv("APEXLOAD_CACHE_BACKEND", "redis")
	t.Setenv("APEXLOAD_REDIS_URL", "redis://user:"+secret+"%zz@redis.example.net:6379/0")
	server := &Server{cache: configuredCacheBackend()}

	check := server.cacheBackendReadiness(context.Background())
	payload, err := json.Marshal(check)
	if err != nil {
		t.Fatalf("marshal readiness check: %v", err)
	}
	if strings.Contains(string(payload), secret) {
		t.Fatalf("readiness response exposed a Redis credential: %s", payload)
	}
	if check.Details["config_error"] != "remote cache configuration is invalid" {
		t.Fatalf("config_error = %#v, want sanitized category", check.Details["config_error"])
	}
	if check.Details["reason"] != "configuration_invalid" || check.Message != "cache backend probe failed" {
		t.Fatalf("cache failure was not categorized safely: %#v", check)
	}
	if check.Status != readinessStatusNotReady {
		t.Fatalf("cache status = %q, want not_ready for invalid configuration", check.Status)
	}
}

func TestCacheReadinessRejectsUnsupportedBackendConfiguration(t *testing.T) {
	const invalidBackend = "redsi-secret-dsn"
	t.Setenv("APEXLOAD_CACHE_BACKEND", invalidBackend)
	t.Setenv("APEXLOAD_REDIS_URL", "")
	server := &Server{cache: configuredCacheBackend()}

	check := server.cacheBackendReadiness(context.Background())
	if check.Status != readinessStatusNotReady || check.Details["reason"] != "configuration_invalid" {
		t.Fatalf("unsupported cache readiness = %#v, want configuration-invalid not_ready", check)
	}
	payload, err := json.Marshal(check)
	if err != nil {
		t.Fatalf("marshal readiness check: %v", err)
	}
	if strings.Contains(string(payload), invalidBackend) || check.Details["cache_backend"] != "unknown" {
		t.Fatalf("unsupported cache backend was not sanitized: %s", payload)
	}
}

func TestCacheReadinessSanitizesInjectedConfigurationError(t *testing.T) {
	const secret = "postgres://operator:sentinel-password@cache.example.net"
	server := &Server{cache: &unavailableCacheBackend{
		info: CacheBackendInfo{Backend: "enterprise-cache-" + secret, ConfigError: "connect " + secret},
		err:  errors.New("cache configuration unavailable"),
	}}

	check := server.cacheBackendReadiness(context.Background())
	payload, err := json.Marshal(check)
	if err != nil {
		t.Fatalf("marshal readiness check: %v", err)
	}
	if strings.Contains(string(payload), secret) || strings.Contains(string(payload), "sentinel-password") {
		t.Fatalf("readiness response exposed an injected cache credential: %s", payload)
	}
	if check.Status != readinessStatusNotReady || check.Details["config_error"] != "cache backend configuration is invalid" || check.Details["cache_backend"] != "unknown" {
		t.Fatalf("injected cache readiness = %#v, want sanitized not_ready", check)
	}
}

func TestCacheReadinessSanitizesInjectedRemoteOperationalFields(t *testing.T) {
	const (
		passwordSecret = "sentinel-cache-password"
		pathSecret     = "sentinel-cache-path"
		prefixSecret   = "sentinel-cache-prefix"
		channelSecret  = "sentinel-cache-channel"
	)
	tests := []struct {
		backend      string
		tlsEnabled   bool
		wantEndpoint string
	}{
		{backend: cacheBackendRedis, wantEndpoint: "redis://cache.example.net:6379"},
		{backend: cacheBackendValkey, tlsEnabled: true, wantEndpoint: "rediss://cache.example.net:6379"},
	}
	for _, test := range tests {
		t.Run(test.backend, func(t *testing.T) {
			server := &Server{cache: &unavailableCacheBackend{
				info: CacheBackendInfo{
					Backend:       test.backend,
					Endpoint:      "redis://operator:" + passwordSecret + "@cache.example.net:6379/" + pathSecret + "?token=" + passwordSecret,
					KeyPrefix:     prefixSecret,
					FanoutChannel: channelSecret,
					RedisURLSet:   true,
					TLSEnabled:    test.tlsEnabled,
					ConfigError:   "connect failed with " + passwordSecret,
				},
				err: errors.New("cache unavailable"),
			}}

			check := server.cacheBackendReadiness(context.Background())
			payload, err := json.Marshal(check)
			if err != nil {
				t.Fatalf("marshal readiness check: %v", err)
			}
			for _, secret := range []string{passwordSecret, pathSecret, prefixSecret, channelSecret} {
				if strings.Contains(string(payload), secret) {
					t.Fatalf("readiness response exposed injected cache field %q: %s", secret, payload)
				}
			}
			if check.Details["endpoint"] != test.wantEndpoint {
				t.Fatalf("sanitized endpoint = %#v, want %q details=%#v", check.Details["endpoint"], test.wantEndpoint, check.Details)
			}
			if _, ok := check.Details["key_prefix"]; ok {
				t.Fatalf("public readiness exposed key_prefix: %#v", check.Details)
			}
			if _, ok := check.Details["fanout_channel"]; ok {
				t.Fatalf("public readiness exposed fanout_channel: %#v", check.Details)
			}
		})
	}
}

type blockingReadinessKubeRestarter struct{}

func (blockingReadinessKubeRestarter) DeletePod(context.Context, string, string) error { return nil }
func (blockingReadinessKubeRestarter) RolloutRestart(context.Context, string, string, string) error {
	return nil
}
func (blockingReadinessKubeRestarter) PodByIP(context.Context, string, string, string) (KubePodRef, error) {
	return KubePodRef{}, nil
}
func (blockingReadinessKubeRestarter) ListPods(ctx context.Context, _, _ string) ([]KubePodRef, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingReadinessKubeRestarter) ScaleStatefulSet(context.Context, string, string, int) error {
	return nil
}

type countingBlockingReadinessKubeRestarter struct {
	mu    sync.Mutex
	calls int
}

type blockingFailingReadinessKubeRestarter struct {
	mu          sync.Mutex
	calls       int
	startedOnce sync.Once
	started     chan struct{}
	release     chan struct{}
}

func (*blockingFailingReadinessKubeRestarter) DeletePod(context.Context, string, string) error {
	return nil
}
func (*blockingFailingReadinessKubeRestarter) RolloutRestart(context.Context, string, string, string) error {
	return nil
}
func (*blockingFailingReadinessKubeRestarter) PodByIP(context.Context, string, string, string) (KubePodRef, error) {
	return KubePodRef{}, nil
}
func (r *blockingFailingReadinessKubeRestarter) ListPods(context.Context, string, string) ([]KubePodRef, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	r.startedOnce.Do(func() { close(r.started) })
	<-r.release
	return nil, errors.New("forbidden: secret response body")
}
func (*blockingFailingReadinessKubeRestarter) ScaleStatefulSet(context.Context, string, string, int) error {
	return nil
}
func (r *blockingFailingReadinessKubeRestarter) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (*countingBlockingReadinessKubeRestarter) DeletePod(context.Context, string, string) error {
	return nil
}
func (*countingBlockingReadinessKubeRestarter) RolloutRestart(context.Context, string, string, string) error {
	return nil
}
func (*countingBlockingReadinessKubeRestarter) PodByIP(context.Context, string, string, string) (KubePodRef, error) {
	return KubePodRef{}, nil
}
func (r *countingBlockingReadinessKubeRestarter) ListPods(ctx context.Context, _, _ string) ([]KubePodRef, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*countingBlockingReadinessKubeRestarter) ScaleStatefulSet(context.Context, string, string, int) error {
	return nil
}
func (r *countingBlockingReadinessKubeRestarter) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
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
	refreshRuntimeReadinessForTest(t, server)

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
