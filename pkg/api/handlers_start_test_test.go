package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failFastStartBackend struct{}

func (failFastStartBackend) Run(runner *TestRunner, test *ManagedTest) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.tests[test.ID] != test {
		return
	}
	now := time.Now().UTC()
	test.Status = "FAILED"
	test.Error = "synthetic startup failure"
	test.EndedAt = &now
}

func TestHandleStartTestReturnsCapacityShortfallPayload(t *testing.T) {
	store := &metricsStoreSpy{}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-capacity",
		Name:     "API Capacity Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "API Start Block",
		Duration: "30s",
		ScriptID: "script-api-capacity",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/start", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
	}
	var payload struct {
		Error             string            `json:"error"`
		Code              string            `json:"code"`
		CapacityAdvisory  *CapacityAdvisory `json:"capacity_advisory"`
		OverrideAvailable bool              `json:"override_available"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Code != "capacity_shortfall" {
		t.Fatalf("code = %q, want capacity_shortfall", payload.Code)
	}
	if payload.CapacityAdvisory == nil || !payload.CapacityAdvisory.RequiresOverride {
		t.Fatalf("expected advisory requiring override, got %#v", payload.CapacityAdvisory)
	}
	if !payload.OverrideAvailable {
		t.Fatal("expected override_available to be true")
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "capacity.placement.reject", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
	if events[0].Status != "blocked" || events[0].ResourceID != testRun.ID {
		t.Fatalf("audit event = %#v, want blocked placement rejection for test", events[0])
	}
	if events[0].Details["recommended_generators"] == nil || events[0].Details["available_generators"] == nil || events[0].Details["reason"] != "capacity_shortfall" {
		t.Fatalf("audit details = %#v, want capacity shortfall sizing evidence", events[0].Details)
	}
}

func TestHandleStartTestAllowsOverridePayload(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	// Exercise HTTP admission and dispatch without starting 15,000 real VUsers
	// that outlive this handler test and starve unrelated execution tests.
	started := make(chan string, 1)
	server.runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-capacity-override",
		Name:     "API Capacity Override Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "API Start Override",
		Duration: "30s",
		ScriptID: "script-api-capacity-override",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	body, _ := json.Marshal(StartTestOptions{OverrideCapacityShortfall: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusAccepted, resp.Body.String())
	}
	admitted, ok := server.runner.GetTest(testRun.ID)
	if !ok || admitted.StartAuthorization == nil || admitted.StartAuthorization.CapacityShortfall == nil || admitted.StartAuthorization.CapacityShortfall.TotalVUsers != 15000 {
		t.Fatal("admitted run lost its explicit capacity override")
	}
	select {
	case id := <-started:
		if id != testRun.ID {
			t.Fatalf("dispatched %q, want %q", id, testRun.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("capacity override did not dispatch the admitted run")
	}
}

func TestWaitForStartResponseStateObservesRunningTransition(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-start-response-state",
		Name:     "API Start Response State Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "API Start Response State",
		Duration: "30s",
		ScriptID: "script-api-start-response-state",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.tests[testRun.ID].Status = "INITIALIZING"
	server.runner.mu.Unlock()

	transitioned := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		server.runner.mu.Lock()
		server.runner.tests[testRun.ID].Status = "RUNNING"
		server.runner.mu.Unlock()
		close(transitioned)
	}()

	observed, ok := server.waitForStartResponseState(testRun.ID)
	if !ok {
		t.Fatal("waitForStartResponseState returned missing test")
	}
	if observed.Status != "RUNNING" {
		t.Fatalf("status = %q, want RUNNING", observed.Status)
	}
	<-transitioned
}

func TestHandleStartTestDoesNotReportFastFailureAsStarted(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.execution = failFastStartBackend{}
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-fast-start-failure",
		Name:     "API Fast Start Failure Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "API Fast Start Failure",
		Duration: "30s",
		ScriptID: "script-api-fast-start-failure",
		VUsers:   1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/start", nil)
	req.Header.Set("X-Auth-Request-User", "starter@example.com")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusInternalServerError, resp.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["status"] == "started" {
		t.Fatalf("failed startup was reported as started: %#v", payload)
	}
	stored, ok := server.runner.GetTest(testRun.ID)
	if !ok || stored.Status != "FAILED" {
		t.Fatalf("stored status = %#v, want FAILED", stored)
	}
	if stored.StartedBySubject != "dev-mode" {
		t.Fatalf("started_by_subject = %q, want dev-mode", stored.StartedBySubject)
	}
}

func TestHandleStopScheduledTestPersistsActorAndClearsSchedule(t *testing.T) {
	store := &metricsStoreSpy{}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-scheduled-stop",
		Name:     "API Scheduled Stop Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	scheduledAt := time.Now().UTC().Add(time.Hour)
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:             "API Scheduled Stop",
		Duration:         "30s",
		ScriptID:         "script-api-scheduled-stop",
		VUsers:           1,
		ScheduledStartAt: &scheduledAt,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if testRun.Status != "SCHEDULED" {
		t.Fatalf("created status = %q, want SCHEDULED", testRun.Status)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/stop", nil)
	req.Header.Set("X-Auth-Request-User", "stopper@example.com")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	stored, ok := server.runner.GetTest(testRun.ID)
	if !ok || stored.Status != "ABORTED" {
		t.Fatalf("stored test = %#v, want ABORTED", stored)
	}
	if stored.ScheduledStartAt != nil {
		t.Fatalf("scheduled_start_at = %v, want nil", stored.ScheduledStartAt)
	}
	if stored.StoppedBySubject != "dev-mode" {
		t.Fatalf("stopped_by_subject = %q, want dev-mode", stored.StoppedBySubject)
	}
	if len(store.savedTests) == 0 {
		t.Fatal("expected durable test writes")
	}
	last := store.savedTests[len(store.savedTests)-1]
	if last.Status != "ABORTED" || last.StoppedBySubject != "dev-mode" || last.ScheduledStartAt != nil {
		t.Fatalf("durable terminal record = %#v", last)
	}
}

func TestHandleStartTestReturnsScriptShakeoutReviewPayload(t *testing.T) {
	store := &metricsStoreSpy{}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-shakeout-bad",
		Name:     "API Shakeout Broken Script",
		Protocol: "definitely-not-supported",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Broken", Type: "request", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:           "API Shakeout Gate",
		Duration:       "30s",
		ScriptShakeout: true,
		ScriptID:       "script-api-shakeout-bad",
		VUsers:         1,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/start", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
	}
	var payload struct {
		Error             string                `json:"error"`
		Code              string                `json:"code"`
		Status            string                `json:"status"`
		ScriptShakeout    *ScriptShakeoutReport `json:"script_shakeout"`
		OverrideAvailable bool                  `json:"override_available"`
		CancelAvailable   bool                  `json:"cancel_available"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Code != "script_shakeout_failed" || payload.Status != "shakeout_review" {
		t.Fatalf("payload = %#v, want shakeout review conflict", payload)
	}
	if payload.ScriptShakeout == nil || payload.ScriptShakeout.Passed || len(payload.ScriptShakeout.Results) != 1 {
		t.Fatalf("script_shakeout = %#v, want failing report", payload.ScriptShakeout)
	}
	if !payload.OverrideAvailable || !payload.CancelAvailable {
		t.Fatalf("payload = %#v, want continue and cancel options", payload)
	}
	blocked, ok := server.runner.GetTest(testRun.ID)
	if !ok || blocked.Status != "SHAKEOUT_REVIEW" {
		t.Fatalf("test = %#v, want SHAKEOUT_REVIEW", blocked)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "test.script_shakeout.review", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Status != "blocked" || events[0].ResourceID != testRun.ID {
		t.Fatalf("audit events = %#v, want blocked shakeout review audit", events)
	}
}

func TestHandleStartTestReturnsPendingWhenProjectQuotaReached(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {
			ID:   "project-1",
			Name: "Payments",
			Settings: storage.ProjectSettings{
				MaxConcurrentTests: 1,
			},
		},
	}}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-api-project-quota",
		Name:      "API Project Quota Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := server.runner.CreateTest(CreateTestRequest{Name: "First", Duration: "30s", ScriptID: "script-api-project-quota", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := server.runner.CreateTest(CreateTestRequest{Name: "Second", Duration: "30s", ScriptID: "script-api-project-quota", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.tests[first.ID].Status = "RUNNING"
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+second.ID+"/start", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Status  string `json:"status"`
		TestID  string `json:"test_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Status != "pending" {
		t.Fatalf("status = %q, want pending", payload.Status)
	}
	if payload.TestID != second.ID {
		t.Fatalf("test_id = %q, want %q", payload.TestID, second.ID)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "capacity.fair_share.defer", ProjectID: "project-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
	if events[0].Status != "blocked" || events[0].ResourceID != second.ID {
		t.Fatalf("audit event = %#v, want blocked fair-share deferral for second test", events[0])
	}
	if events[0].Details["pending_reason"] == "" {
		t.Fatalf("audit details = %#v, want pending reason", events[0].Details)
	}
}
