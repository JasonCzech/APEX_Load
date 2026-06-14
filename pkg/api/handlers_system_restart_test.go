package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSystemRestartCapabilitiesDisabledAndEnabled(t *testing.T) {
	disabled := NewServer(ServerConfig{MaxConcurrent: 2})
	resp := httptest.NewRecorder()
	disabled.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/system/restarts/capabilities", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("disabled status = %d body=%s", resp.Code, resp.Body.String())
	}
	var disabledCaps SystemRestartCapabilities
	if err := json.Unmarshal(resp.Body.Bytes(), &disabledCaps); err != nil {
		t.Fatalf("decode disabled capabilities: %v", err)
	}
	if disabledCaps.Enabled {
		t.Fatal("disabled capabilities Enabled = true")
	}
	if !strings.Contains(disabledCaps.DisabledReason, "disabled") {
		t.Fatalf("disabled reason = %q", disabledCaps.DisabledReason)
	}

	enabled, _, _, _ := newRestartServer(t, []GeneratorInventoryEntry{restartGeneratorEntry("10.0.0.1:9090", "connected", 0)})
	resp = httptest.NewRecorder()
	enabled.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/system/restarts/capabilities", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("enabled status = %d body=%s", resp.Code, resp.Body.String())
	}
	var enabledCaps SystemRestartCapabilities
	if err := json.Unmarshal(resp.Body.Bytes(), &enabledCaps); err != nil {
		t.Fatalf("decode enabled capabilities: %v", err)
	}
	if !enabledCaps.Enabled {
		t.Fatalf("enabled capabilities Enabled = false reason=%q", enabledCaps.DisabledReason)
	}
	if enabledCaps.ExecutionMode != "distributed" {
		t.Fatalf("execution mode = %q, want distributed", enabledCaps.ExecutionMode)
	}
	if !enabledCaps.Supports.GeneratorPod || !enabledCaps.Supports.GeneratorWorkload || !enabledCaps.Supports.ControllerWorkload {
		t.Fatalf("supports = %#v", enabledCaps.Supports)
	}
}

func TestSystemRestartValidation(t *testing.T) {
	tests := []struct {
		name     string
		settings K8sRestartSettings
		request  SystemRestartRequest
		wantBody string
	}{
		{
			name: "missing reason",
			request: SystemRestartRequest{
				TargetType: restartTargetGenerator,
				Scope:      restartScopePod,
				Target:     "10.0.0.1:9090",
			},
			wantBody: "reason is required",
		},
		{
			name: "confirmation mismatch",
			request: SystemRestartRequest{
				TargetType:   restartTargetGenerator,
				Scope:        restartScopePod,
				Target:       "10.0.0.1:9090",
				Reason:       "stuck",
				Confirmation: "other",
			},
			wantBody: "confirmation must match target",
		},
		{
			name: "controller pod rejected",
			request: SystemRestartRequest{
				TargetType:   restartTargetController,
				Scope:        restartScopePod,
				Target:       "apexload-controller-0",
				Reason:       "wedged",
				Confirmation: "apexload-controller-0",
			},
			wantBody: "controller pod restart is not supported",
		},
		{
			name: "force disabled",
			settings: K8sRestartSettings{
				Enabled:              true,
				Namespace:            "apex",
				ControllerDeployment: "apexload-controller",
				GeneratorStatefulSet: "apexload-generator",
				GeneratorSelector:    "app=generator",
				AllowForce:           false,
			},
			request: SystemRestartRequest{
				TargetType:   restartTargetGenerator,
				Scope:        restartScopePod,
				Target:       "10.0.0.1:9090",
				Reason:       "stuck",
				Confirmation: "10.0.0.1:9090",
				Force:        true,
			},
			wantBody: "force restarts are disabled",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _, _, _ := newRestartServerWithSettings(t, tc.settings, []GeneratorInventoryEntry{restartGeneratorEntry("10.0.0.1:9090", "connected", 0)})
			resp, _ := postRestart(t, server, tc.request)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), tc.wantBody) {
				t.Fatalf("body = %s, want %q", resp.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestSystemRestartGeneratorPodSafetyAndForce(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		activeVUs   int32
		force       bool
		wantStatus  int
		wantResult  string
		wantDeletes int
		wantAudit   string
	}{
		{name: "safe idle reachable", status: "connected", activeVUs: 0, wantStatus: http.StatusOK, wantResult: "success", wantDeletes: 1, wantAudit: "success"},
		{name: "active VUsers blocked", status: "connected", activeVUs: 12, wantStatus: http.StatusConflict, wantResult: "blocked", wantAudit: "blocked"},
		{name: "active VUsers force", status: "connected", activeVUs: 12, force: true, wantStatus: http.StatusOK, wantResult: "success", wantDeletes: 1, wantAudit: "success"},
		{name: "unreachable blocked", status: "unreachable", activeVUs: 0, wantStatus: http.StatusConflict, wantResult: "blocked", wantAudit: "blocked"},
		{name: "unreachable force", status: "unreachable", activeVUs: 0, force: true, wantStatus: http.StatusOK, wantResult: "success", wantDeletes: 1, wantAudit: "success"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := "10.0.0.1:9090"
			server, backend, restarter, store := newRestartServer(t, []GeneratorInventoryEntry{restartGeneratorEntry(target, tc.status, tc.activeVUs)})
			resp, result := postRestart(t, server, SystemRestartRequest{
				TargetType:   restartTargetGenerator,
				Scope:        restartScopePod,
				Target:       target,
				Reason:       "operator recovery",
				Confirmation: target,
				Force:        tc.force,
			})
			if resp.Code != tc.wantStatus {
				t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
			}
			if result.Status != tc.wantResult {
				t.Fatalf("result status = %q, want %q", result.Status, tc.wantResult)
			}
			if len(restarter.deleteCalls()) != tc.wantDeletes {
				t.Fatalf("delete calls = %#v, want %d", restarter.deleteCalls(), tc.wantDeletes)
			}
			if tc.wantDeletes == 1 {
				if got := restarter.deleteCalls()[0].name; got != "apexload-generator-0" {
					t.Fatalf("deleted pod = %q", got)
				}
				if got := backend.drainState(target); got != "draining" {
					t.Fatalf("drain state = %q, want draining", got)
				}
			}
			assertLastRestartAudit(t, store, tc.wantAudit, restartTargetGenerator, result.Target)
		})
	}
}

func TestSystemRestartGeneratorPodResolvesDNSAndPodName(t *testing.T) {
	t.Run("statefulset DNS target", func(t *testing.T) {
		target := "apexload-generator-1.apexload-generator.apex.svc.cluster.local:9090"
		entry := restartGeneratorEntry(target, "connected", 0)
		entry.GeneratorID = "generator-one"
		server, _, restarter, _ := newRestartServer(t, []GeneratorInventoryEntry{entry})
		restarter.setPods([]KubePodRef{
			{Name: "apexload-generator-0", Namespace: "apex", IP: "10.0.0.1", Phase: "Running"},
			{Name: "apexload-generator-1", Namespace: "apex", IP: "10.0.0.2", Phase: "Running"},
		})

		resp, result := postRestart(t, server, SystemRestartRequest{
			TargetType:   restartTargetGenerator,
			Scope:        restartScopePod,
			Target:       target,
			Reason:       "dns recovery",
			Confirmation: target,
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
		}
		if result.PodName != "apexload-generator-1" {
			t.Fatalf("pod_name = %q", result.PodName)
		}
	})

	t.Run("enriched pod name target", func(t *testing.T) {
		entry := restartGeneratorEntry("10.0.0.1:9090", "connected", 0)
		server, _, _, _ := newRestartServer(t, []GeneratorInventoryEntry{entry})
		resp, result := postRestart(t, server, SystemRestartRequest{
			TargetType:   restartTargetGenerator,
			Scope:        restartScopePod,
			Target:       "apexload-generator-0",
			Reason:       "button target",
			Confirmation: "apexload-generator-0",
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
		}
		if result.PodName != "apexload-generator-0" {
			t.Fatalf("pod_name = %q", result.PodName)
		}
	})
}

func TestSystemRestartWorkloadGates(t *testing.T) {
	t.Run("generator workload blocks on active VUsers", func(t *testing.T) {
		server, _, restarter, store := newRestartServer(t, []GeneratorInventoryEntry{restartGeneratorEntry("10.0.0.1:9090", "connected", 7)})
		resp, result := postRestart(t, server, SystemRestartRequest{
			TargetType:   restartTargetGenerator,
			Scope:        restartScopeWorkload,
			Target:       "apexload-generator",
			Reason:       "roll fleet",
			Confirmation: "apexload-generator",
		})
		if resp.Code != http.StatusConflict {
			t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
		}
		if len(restarter.rolloutCalls()) != 0 {
			t.Fatalf("rollout calls = %#v", restarter.rolloutCalls())
		}
		if result.ActiveVUsers != 7 {
			t.Fatalf("active_vusers = %d", result.ActiveVUsers)
		}
		assertLastRestartAudit(t, store, "blocked", restartTargetGenerator, "apexload-generator")
	})

	t.Run("generator workload force rolls statefulset", func(t *testing.T) {
		server, _, restarter, _ := newRestartServer(t, []GeneratorInventoryEntry{restartGeneratorEntry("10.0.0.1:9090", "connected", 7)})
		resp, _ := postRestart(t, server, SystemRestartRequest{
			TargetType:   restartTargetGenerator,
			Scope:        restartScopeWorkload,
			Target:       "apexload-generator",
			Reason:       "roll fleet",
			Confirmation: "apexload-generator",
			Force:        true,
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
		}
		calls := restarter.rolloutCalls()
		if len(calls) != 1 || calls[0].kind != "statefulset" || calls[0].name != "apexload-generator" {
			t.Fatalf("rollout calls = %#v", calls)
		}
	})

	t.Run("controller workload blocks on active run and force rolls deployment", func(t *testing.T) {
		server, _, restarter, store := newRestartServer(t, []GeneratorInventoryEntry{restartGeneratorEntry("10.0.0.1:9090", "connected", 0)})
		addManagedTest(server, "run-1", "RUNNING")
		resp, result := postRestart(t, server, SystemRestartRequest{
			TargetType:   restartTargetController,
			Scope:        restartScopeWorkload,
			Target:       "apexload-controller",
			Reason:       "wedged",
			Confirmation: "apexload-controller",
		})
		if resp.Code != http.StatusConflict {
			t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
		}
		if result.ActiveRunCount != 1 {
			t.Fatalf("active_run_count = %d", result.ActiveRunCount)
		}
		assertLastRestartAudit(t, store, "blocked", restartTargetController, "apexload-controller")

		resp, _ = postRestart(t, server, SystemRestartRequest{
			TargetType:   restartTargetController,
			Scope:        restartScopeWorkload,
			Target:       "apexload-controller",
			Reason:       "wedged",
			Confirmation: "apexload-controller",
			Force:        true,
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("force status = %d body=%s", resp.Code, resp.Body.String())
		}
		calls := restarter.rolloutCalls()
		if len(calls) != 1 || calls[0].kind != "deployment" || calls[0].name != "apexload-controller" {
			t.Fatalf("rollout calls = %#v", calls)
		}
	})
}

func TestSystemRestartRecordsFailedAudit(t *testing.T) {
	target := "10.0.0.1:9090"
	server, _, restarter, store := newRestartServer(t, []GeneratorInventoryEntry{restartGeneratorEntry(target, "connected", 0)})
	restarter.deleteErr = errors.New("api server unavailable")
	resp, result := postRestart(t, server, SystemRestartRequest{
		TargetType:   restartTargetGenerator,
		Scope:        restartScopePod,
		Target:       target,
		Reason:       "stuck",
		Confirmation: target,
	})
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if result.Status != "failed" {
		t.Fatalf("result status = %q", result.Status)
	}
	assertLastRestartAudit(t, store, "failed", restartTargetGenerator, target)
}

func TestSystemRestartDuplicateInFlight(t *testing.T) {
	target := "10.0.0.1:9090"
	server, _, restarter, _ := newRestartServer(t, []GeneratorInventoryEntry{restartGeneratorEntry(target, "connected", 0)})
	restarter.deleteStarted = make(chan struct{})
	restarter.releaseDelete = make(chan struct{})
	// Capture the channel before the restart goroutine can nil the field under f.mu.
	deleteStarted := restarter.deleteStarted

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resp, _ := postRestart(t, server, SystemRestartRequest{
			TargetType:   restartTargetGenerator,
			Scope:        restartScopePod,
			Target:       target,
			Reason:       "first",
			Confirmation: target,
		})
		firstDone <- resp
	}()
	<-deleteStarted

	resp, result := postRestart(t, server, SystemRestartRequest{
		TargetType:   restartTargetGenerator,
		Scope:        restartScopePod,
		Target:       target,
		Reason:       "duplicate",
		Confirmation: target,
	})
	if resp.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(result.Message, "already in progress") {
		t.Fatalf("duplicate message = %q", result.Message)
	}

	close(restarter.releaseDelete)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", first.Code, first.Body.String())
	}
}

func newRestartServer(t *testing.T, entries []GeneratorInventoryEntry) (*Server, *restartInventoryBackend, *fakeKubeRestarter, *metricsStoreSpy) {
	t.Helper()
	return newRestartServerWithSettings(t, K8sRestartSettings{}, entries)
}

func newRestartServerWithSettings(t *testing.T, settings K8sRestartSettings, entries []GeneratorInventoryEntry) (*Server, *restartInventoryBackend, *fakeKubeRestarter, *metricsStoreSpy) {
	t.Helper()
	if !settings.Enabled && settings.Namespace == "" && settings.ControllerDeployment == "" && settings.GeneratorStatefulSet == "" && settings.GeneratorSelector == "" {
		settings = K8sRestartSettings{
			Enabled:              true,
			Namespace:            "apex",
			ControllerDeployment: "apexload-controller",
			GeneratorStatefulSet: "apexload-generator",
			GeneratorSelector:    "app=generator",
			AllowForce:           true,
		}
	}
	store := &metricsStoreSpy{}
	restarter := &fakeKubeRestarter{
		pods: []KubePodRef{
			{Name: "apexload-generator-0", Namespace: "apex", IP: "10.0.0.1", Phase: "Running"},
		},
	}
	server := NewServer(ServerConfig{
		MaxConcurrent:    2,
		Store:            store,
		K8sRestart:       settings,
		KubeRestarter:    restarter,
		GeneratorTargets: nil,
	})
	backend := &restartInventoryBackend{entries: append([]GeneratorInventoryEntry(nil), entries...), drainStates: map[string]string{}}
	server.runner.setExecutionBackend(backend)
	return server, backend, restarter, store
}

func restartGeneratorEntry(target string, status string, activeVUsers int32) GeneratorInventoryEntry {
	return GeneratorInventoryEntry{
		Target:       target,
		GeneratorID:  "apexload-generator-0",
		Status:       status,
		ActiveVUsers: activeVUsers,
		MaxVUsers:    10000,
	}
}

func postRestart(t *testing.T, server *Server, req SystemRestartRequest) (*httptest.ResponseRecorder, SystemRestartResult) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/v1/system/restarts", bytes.NewReader(body)))
	var result SystemRestartResult
	if strings.TrimSpace(resp.Body.String()) != "" {
		_ = json.Unmarshal(resp.Body.Bytes(), &result)
	}
	return resp, result
}

func addManagedTest(server *Server, id string, status string) {
	server.runner.mu.Lock()
	defer server.runner.mu.Unlock()
	server.runner.tests[id] = &ManagedTest{ID: id, Status: status}
}

func assertLastRestartAudit(t *testing.T, store *metricsStoreSpy, status string, targetType string, target string) {
	t.Helper()
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no audit events recorded")
	}
	event := events[len(events)-1]
	if event.Status != status {
		t.Fatalf("audit status = %q, want %q event=%#v", event.Status, status, event)
	}
	if event.ResourceType != "system_restart" || event.ResourceID != target {
		t.Fatalf("audit resource = %s/%s, want system_restart/%s", event.ResourceType, event.ResourceID, target)
	}
	if targetType == restartTargetGenerator && !strings.HasPrefix(event.Action, "system.restart.generator") {
		t.Fatalf("audit action = %q", event.Action)
	}
	if targetType == restartTargetController && event.Action != "system.restart.controller" {
		t.Fatalf("audit action = %q", event.Action)
	}
	if _, ok := event.Details["reason"]; !ok {
		t.Fatalf("audit details missing reason: %#v", event.Details)
	}
}

type restartInventoryBackend struct {
	mu          sync.Mutex
	entries     []GeneratorInventoryEntry
	drainStates map[string]string
}

func (b *restartInventoryBackend) Run(tr *TestRunner, test *ManagedTest) {}

func (b *restartInventoryBackend) SystemInventory(ctx context.Context) []GeneratorInventoryEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]GeneratorInventoryEntry(nil), b.entries...)
}

func (b *restartInventoryBackend) SetGeneratorDrainState(target string, state string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.drainStates == nil {
		b.drainStates = map[string]string{}
	}
	b.drainStates[target] = state
	return nil
}

func (b *restartInventoryBackend) ApplyGeneratorDrainStates(states map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.drainStates = map[string]string{}
	for key, value := range states {
		b.drainStates[key] = value
	}
}

func (b *restartInventoryBackend) GeneratorDrainStates() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]string{}
	for key, value := range b.drainStates {
		out[key] = value
	}
	return out
}

func (b *restartInventoryBackend) drainState(target string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.drainStates[target]
}

type restartDeleteCall struct {
	namespace string
	name      string
}

type restartRolloutCall struct {
	kind      string
	namespace string
	name      string
}

type fakeKubeRestarter struct {
	mu            sync.Mutex
	pods          []KubePodRef
	deletes       []restartDeleteCall
	rollouts      []restartRolloutCall
	deleteErr     error
	rolloutErr    error
	listErr       error
	podByIPErr    error
	deleteStarted chan struct{}
	releaseDelete chan struct{}
}

func (f *fakeKubeRestarter) DeletePod(ctx context.Context, namespace string, name string) error {
	f.mu.Lock()
	f.deletes = append(f.deletes, restartDeleteCall{namespace: namespace, name: name})
	started := f.deleteStarted
	if started != nil {
		f.deleteStarted = nil
	}
	release := f.releaseDelete
	err := f.deleteErr
	f.mu.Unlock()

	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeKubeRestarter) RolloutRestart(ctx context.Context, kind string, namespace string, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rollouts = append(f.rollouts, restartRolloutCall{kind: kind, namespace: namespace, name: name})
	return f.rolloutErr
}

func (f *fakeKubeRestarter) PodByIP(ctx context.Context, namespace string, selector string, ip string) (KubePodRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.podByIPErr != nil {
		return KubePodRef{}, f.podByIPErr
	}
	var matches []KubePodRef
	for _, pod := range f.pods {
		if pod.IP == ip {
			matches = append(matches, pod)
		}
	}
	if len(matches) != 1 {
		return KubePodRef{}, errors.New("pod lookup did not return exactly one match")
	}
	return matches[0], nil
}

func (f *fakeKubeRestarter) ListPods(ctx context.Context, namespace string, selector string) ([]KubePodRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]KubePodRef(nil), f.pods...), nil
}

func (f *fakeKubeRestarter) setPods(pods []KubePodRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pods = append([]KubePodRef(nil), pods...)
}

func (f *fakeKubeRestarter) deleteCalls() []restartDeleteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]restartDeleteCall(nil), f.deletes...)
}

func (f *fakeKubeRestarter) rolloutCalls() []restartRolloutCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]restartRolloutCall(nil), f.rollouts...)
}
