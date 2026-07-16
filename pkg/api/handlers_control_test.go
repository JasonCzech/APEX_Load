package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newRunningControlTestServer(t *testing.T) (*Server, *ManagedTest) {
	t.Helper()

	protocolName := "control-api-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       protocolName + "-script",
		Name:     "Controller Lifecycle Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Controller Lifecycle Test",
		Duration: "10s",
		Groups: []CreateGroupEntry{{
			Name:     "buyers",
			ScriptID: protocolName + "-script",
			VUsers:   1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := server.runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}
	waitForTestStatus(t, server.runner, testRun.ID, "RUNNING")

	t.Cleanup(func() {
		cleanupControlTestRun(t, server, testRun.ID)
	})

	return server, testRun
}

func newRunningGoalControlTestServer(t *testing.T) (*Server, *ManagedTest) {
	t.Helper()

	protocolName := "goal-control-api-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       protocolName + "-script",
		Name:     "Goal Controller Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	targetTPS := 25.0
	minVUsers := 1
	maxVUsers := 25
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:         "Goal Controller Test",
		Duration:     "10s",
		ScenarioMode: "goal_oriented",
		GoalConfig:   &GoalConfig{TargetAverageTPS: &targetTPS, MinVUsers: &minVUsers, MaxVUsers: &maxVUsers},
		Groups: []CreateGroupEntry{{
			Name:     "buyers",
			ScriptID: protocolName + "-script",
			VUsers:   2,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := server.runner.StartTest(testRun.ID); err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}
	waitForTestStatus(t, server.runner, testRun.ID, "RUNNING")

	t.Cleanup(func() {
		cleanupControlTestRun(t, server, testRun.ID)
	})

	return server, testRun
}

func newQueuedGoalControlTestServer(t *testing.T) (*Server, *ManagedTest) {
	t.Helper()

	protocolName := "queued-goal-control-api-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       protocolName + "-script",
		Name:     "Queued Goal Controller Script",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	targetTPS := 25.0
	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:         "Queued Goal Controller Test",
		Duration:     "10s",
		ScenarioMode: "goal_oriented",
		GoalConfig:   &GoalConfig{TargetAverageTPS: &targetTPS},
		Groups: []CreateGroupEntry{{
			Name:     "buyers",
			ScriptID: protocolName + "-script",
			VUsers:   2,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	return server, testRun
}

func newRunningControlAuditServer(t *testing.T) (*Server, *ManagedTest, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}

	protocolName := "control-audit-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	protocol.Register(protocolName, func() protocol.Protocol {
		return &archiveStubProtocol{}
	})

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        protocolName + "-script",
		Name:      "Controller Lifecycle Script",
		ProjectID: storage.DefaultProjectID,
		Protocol:  protocolName,
		Version:   "1.0.0",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "Controller Lifecycle Test",
		Duration: "10s",
		Groups: []CreateGroupEntry{{
			Name:     "buyers",
			ScriptID: protocolName + "-script",
			VUsers:   1,
		}},
	})
	if err != nil {
		store.Close()
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if err := server.runner.StartTest(testRun.ID); err != nil {
		store.Close()
		t.Fatalf("StartTest returned error: %v", err)
	}
	waitForTestStatus(t, server.runner, testRun.ID, "RUNNING")

	t.Cleanup(func() {
		cleanupControlTestRun(t, server, testRun.ID)
		store.Close()
	})

	return server, testRun, store
}

func cleanupControlTestRun(t *testing.T, server *Server, testID string) {
	t.Helper()

	testRun, ok := server.runner.GetTest(testID)
	if !ok {
		return
	}

	switch testRun.Status {
	case "QUEUED", "COMPLETE", "FAILED", "ABORTED":
		server.runner.DeleteTest(testID)
		return
	case "PAUSED":
		if err := server.runner.ResumeTest(testID); err != nil {
			t.Fatalf("ResumeTest during cleanup returned error: %v", err)
		}
		waitForTestStatus(t, server.runner, testID, "RUNNING")
	}

	testRun, ok = server.runner.GetTest(testID)
	if !ok {
		return
	}
	if testRun.Status == "INITIALIZING" {
		waitForStatusAny(t, server.runner, testID, 5*time.Second, "RUNNING", "COMPLETE", "FAILED")
		testRun, ok = server.runner.GetTest(testID)
		if !ok {
			return
		}
	}
	if testRun.Status == "RUNNING" {
		if err := server.runner.StopTest(testID); err != nil {
			t.Fatalf("StopTest during cleanup returned error: %v", err)
		}
	}
	if testRun.Status == "STOPPING" || testRun.Status == "RUNNING" {
		waitForStatusAny(t, server.runner, testID, 5*time.Second, "COMPLETE", "FAILED", "ABORTED")
	}
	server.runner.DeleteTest(testID)
}

func TestHandlePauseAndResumeTest(t *testing.T) {
	server, testRun := newRunningControlTestServer(t)

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/pause", nil)
	pauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pauseResp, pauseReq)

	if pauseResp.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want %d body=%s", pauseResp.Code, http.StatusOK, pauseResp.Body.String())
	}
	var pausePayload map[string]string
	if err := json.Unmarshal(pauseResp.Body.Bytes(), &pausePayload); err != nil {
		t.Fatalf("json.Unmarshal pause returned error: %v", err)
	}
	if pausePayload["status"] != "paused" || pausePayload["test_id"] != testRun.ID {
		t.Fatalf("pause payload = %#v, want paused status for %s", pausePayload, testRun.ID)
	}
	waitForTestStatus(t, server.runner, testRun.ID, "PAUSED")

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/resume", nil)
	resumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resumeResp, resumeReq)

	if resumeResp.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want %d body=%s", resumeResp.Code, http.StatusOK, resumeResp.Body.String())
	}
	var resumePayload map[string]string
	if err := json.Unmarshal(resumeResp.Body.Bytes(), &resumePayload); err != nil {
		t.Fatalf("json.Unmarshal resume returned error: %v", err)
	}
	if resumePayload["status"] != "resumed" || resumePayload["test_id"] != testRun.ID {
		t.Fatalf("resume payload = %#v, want resumed status for %s", resumePayload, testRun.ID)
	}
	waitForTestStatus(t, server.runner, testRun.ID, "RUNNING")
}

func TestHandleDeleteTestRejectsPausedRun(t *testing.T) {
	server, testRun := newRunningControlTestServer(t)
	if err := server.runner.PauseTest(testRun.ID); err != nil {
		t.Fatalf("PauseTest returned error: %v", err)
	}
	waitForTestStatus(t, server.runner, testRun.ID, "PAUSED")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/"+testRun.ID, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "currently active") {
		t.Fatalf("delete body = %q, want currently active", resp.Body.String())
	}
}

func TestHandleScaleVUsersReturnsGroupError(t *testing.T) {
	server, testRun := newRunningControlTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"group":         "missing",
		"target_vusers": 2,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/vusers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "group not found") {
		t.Fatalf("scale body = %q, want group not found", resp.Body.String())
	}
}

func TestHandleScaleVUsersAcceptsGradualRampDuration(t *testing.T) {
	server, testRun := newRunningControlTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"group":         "buyers",
		"target_vusers": 3,
		"ramp_duration": "60ms",
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/vusers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload["status"] != "ramping" || payload["ramp_duration"] != "60ms" {
		t.Fatalf("scale payload = %#v, want ramping response", payload)
	}
	ramping, ok := server.runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist", testRun.ID)
	}
	if ramp := ramping.VUserRamps["buyers"]; ramp.TargetVUsers != 3 || ramp.RampDuration != "60ms" {
		t.Fatalf("active ramp status = %#v, want target 3 over 60ms", ramp)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		scaled, ok := server.runner.GetTest(testRun.ID)
		if !ok {
			t.Fatalf("expected managed test %q to exist", testRun.ID)
		}
		if len(scaled.Groups) == 1 && scaled.Groups[0].VUsers == 3 && scaled.TotalVUsers == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected gradual scale to reach 3 vusers, got groups=%#v total=%d", scaled.Groups, scaled.TotalVUsers)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHandleScaleVUsersAcceptsGroupAction(t *testing.T) {
	server, testRun := newRunningControlTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"group":         "buyers",
		"action":        "pause",
		"target_vusers": 3,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/vusers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload["action"] != "pause" || payload["status"] != "paused" || payload["target_vusers"] != float64(0) {
		t.Fatalf("scale payload = %#v, want pause action with zero target", payload)
	}
	scaled, ok := server.runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist", testRun.ID)
	}
	if len(scaled.Groups) != 1 || scaled.Groups[0].VUsers != 0 || scaled.TotalVUsers != 0 {
		t.Fatalf("paused group state = groups=%#v total=%d, want zero vusers", scaled.Groups, scaled.TotalVUsers)
	}
}

func TestHandleScaleVUsersRejectsUnknownGroupAction(t *testing.T) {
	server, testRun := newRunningControlTestServer(t)

	body, _ := json.Marshal(map[string]any{
		"group":         "buyers",
		"action":        "restart",
		"target_vusers": 3,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/vusers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "action must be one of run, pause, or stop") {
		t.Fatalf("scale body = %q, want action validation error", resp.Body.String())
	}
}

func TestHandleUpdateGoalTargetAcceptsGoalRun(t *testing.T) {
	server, testRun := newRunningGoalControlTestServer(t)
	lastScaleAt := time.Date(2026, time.June, 8, 12, 0, 0, 0, time.UTC)
	server.runner.mu.Lock()
	if server.runner.tests[testRun.ID].goalControl == nil {
		server.runner.tests[testRun.ID].goalControl = &goalControllerState{}
	}
	server.runner.tests[testRun.ID].goalControl.lastScaleAt = &lastScaleAt
	server.runner.mu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"group":      "buyers",
		"target_tps": 42.5,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/goal-targets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload GoalTargetUpdate
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.Status != "adjusted" || payload.Group != "buyers" || payload.TargetTPS != 42.5 || payload.TargetAverageTPS != 42.5 {
		t.Fatalf("goal target payload = %#v, want adjusted buyers target 42.5", payload)
	}
	if payload.GoalTargets["buyers"].TargetTPS != 42.5 {
		t.Fatalf("goal targets = %#v, want buyers 42.5", payload.GoalTargets)
	}

	updated, ok := server.runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("expected managed test %q to exist", testRun.ID)
	}
	if updated.GoalConfig == nil || updated.GoalConfig.TargetAverageTPS == nil || *updated.GoalConfig.TargetAverageTPS != 42.5 {
		t.Fatalf("updated goal config = %#v, want target_average_tps 42.5", updated.GoalConfig)
	}
	if updated.GoalTargets["buyers"].TargetTPS != 42.5 {
		t.Fatalf("updated goal targets = %#v, want buyers 42.5", updated.GoalTargets)
	}
	server.runner.mu.Lock()
	if server.runner.tests[testRun.ID].goalControl.lastScaleAt != nil {
		t.Fatalf("expected goal scale throttle to reset, got %#v", server.runner.tests[testRun.ID].goalControl.lastScaleAt)
	}
	server.runner.mu.Unlock()
}

func TestHandleUpdateGoalTargetRejectsInvalidRequests(t *testing.T) {
	t.Run("non-goal run", func(t *testing.T) {
		server, testRun := newRunningControlTestServer(t)
		body, _ := json.Marshal(map[string]any{"group": "buyers", "target_tps": 42})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/goal-targets", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "goal_oriented") {
			t.Fatalf("status=%d body=%s, want goal_oriented validation error", resp.Code, resp.Body.String())
		}
	})

	t.Run("inactive goal run", func(t *testing.T) {
		server, testRun := newQueuedGoalControlTestServer(t)
		body, _ := json.Marshal(map[string]any{"group": "buyers", "target_tps": 42})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/goal-targets", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "must be RUNNING or PAUSED") {
			t.Fatalf("status=%d body=%s, want active-run validation error", resp.Code, resp.Body.String())
		}
	})

	t.Run("unknown group", func(t *testing.T) {
		server, testRun := newRunningGoalControlTestServer(t)
		body, _ := json.Marshal(map[string]any{"group": "missing", "target_tps": 42})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/goal-targets", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "group not found") {
			t.Fatalf("status=%d body=%s, want group validation error", resp.Code, resp.Body.String())
		}
	})

	t.Run("invalid target", func(t *testing.T) {
		server, testRun := newRunningGoalControlTestServer(t)
		body, _ := json.Marshal(map[string]any{"group": "buyers", "target_tps": 0})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/goal-targets", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)

		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "target_tps must be greater than zero") {
			t.Fatalf("status=%d body=%s, want target validation error", resp.Code, resp.Body.String())
		}
	})
}

func TestHandlePinAndUnpinComparisonBaseline(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	completedAt := time.Date(2026, time.May, 12, 12, 0, 0, 0, time.UTC)
	pinnedAt := completedAt.Add(-time.Minute)
	server.runner.tests["baseline-a"] = &ManagedTest{ID: "baseline-a", Name: "Checkout", Status: "COMPLETE", ScenarioID: "scenario-1", ProjectID: storage.DefaultProjectID, CreatedAt: completedAt.Add(-5 * time.Minute), EndedAt: &completedAt}
	server.runner.tests["baseline-b"] = &ManagedTest{ID: "baseline-b", Name: "Checkout", Status: "FAILED", ScenarioID: "scenario-1", ProjectID: storage.DefaultProjectID, CreatedAt: completedAt.Add(-15 * time.Minute), EndedAt: &completedAt, BaselinePinned: true, BaselinePinnedAt: &pinnedAt}

	pinReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/baseline-a/comparison-baseline/pin", nil)
	pinResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pinResp, pinReq)

	if pinResp.Code != http.StatusOK {
		t.Fatalf("pin status = %d, want %d body=%s", pinResp.Code, http.StatusOK, pinResp.Body.String())
	}
	var pinPayload ManagedTest
	if err := json.Unmarshal(pinResp.Body.Bytes(), &pinPayload); err != nil {
		t.Fatalf("json.Unmarshal pin returned error: %v", err)
	}
	if !pinPayload.BaselinePinned || pinPayload.BaselinePinnedAt == nil {
		t.Fatalf("expected pin payload to include baseline pin metadata, got %#v", pinPayload)
	}
	if server.runner.tests["baseline-b"].BaselinePinned {
		t.Fatal("expected previous comparable baseline pin to be cleared")
	}

	unpinReq := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/baseline-a/comparison-baseline/pin", nil)
	unpinResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unpinResp, unpinReq)

	if unpinResp.Code != http.StatusOK {
		t.Fatalf("unpin status = %d, want %d body=%s", unpinResp.Code, http.StatusOK, unpinResp.Body.String())
	}
	var unpinPayload ManagedTest
	if err := json.Unmarshal(unpinResp.Body.Bytes(), &unpinPayload); err != nil {
		t.Fatalf("json.Unmarshal unpin returned error: %v", err)
	}
	if unpinPayload.BaselinePinned || unpinPayload.BaselinePinnedAt != nil {
		t.Fatalf("expected unpin payload to clear baseline pin metadata, got %#v", unpinPayload)
	}
}

func TestHandleAdjustDurationUpdatesRunningTest(t *testing.T) {
	server, testRun := newRunningControlTestServer(t)

	body, _ := json.Marshal(map[string]any{"duration": "5s"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/duration", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload["status"] != "adjusted" || payload["new_duration"] != "5s" {
		t.Fatalf("payload = %#v, want adjusted 5s response", payload)
	}

	updated, ok := server.runner.GetTest(testRun.ID)
	if !ok {
		t.Fatalf("test %s missing after duration adjust", testRun.ID)
	}
	if updated.Duration != 5*time.Second || updated.DurationStr != "5s" {
		t.Fatalf("duration = %v (%q), want 5s", updated.Duration, updated.DurationStr)
	}
}

func TestHandleMoveScaleAdjustAndDeleteTestWriteAuditEvents(t *testing.T) {
	server, testRun, store := newRunningControlAuditServer(t)

	moveReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/move", bytes.NewBufferString(`{"folder_id":null}`))
	moveReq.Header.Set("Content-Type", "application/json")
	moveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(moveResp, moveReq)
	if moveResp.Code != http.StatusOK {
		t.Fatalf("move status = %d, want %d body=%s", moveResp.Code, http.StatusOK, moveResp.Body.String())
	}

	scaleReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/vusers", bytes.NewBufferString(`{"group":"buyers","target_vusers":2}`))
	scaleReq.Header.Set("Content-Type", "application/json")
	scaleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scaleResp, scaleReq)
	if scaleResp.Code != http.StatusOK {
		t.Fatalf("scale status = %d, want %d body=%s", scaleResp.Code, http.StatusOK, scaleResp.Body.String())
	}

	adjustReq := httptest.NewRequest(http.MethodPatch, "/api/v1/tests/"+testRun.ID+"/duration", bytes.NewBufferString(`{"duration":"5s"}`))
	adjustReq.Header.Set("Content-Type", "application/json")
	adjustResp := httptest.NewRecorder()
	server.mux.ServeHTTP(adjustResp, adjustReq)
	if adjustResp.Code != http.StatusOK {
		t.Fatalf("adjust status = %d, want %d body=%s", adjustResp.Code, http.StatusOK, adjustResp.Body.String())
	}

	if err := server.runner.StopTest(testRun.ID); err != nil {
		t.Fatalf("StopTest returned error: %v", err)
	}
	waitForStatusAny(t, server.runner, testRun.ID, 5*time.Second, "COMPLETE", "FAILED", "ABORTED")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/"+testRun.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 10, ProjectID: storage.DefaultProjectID})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("audit event count = %d, want 4", len(events))
	}
	if events[0].Action != "test.delete" || events[1].Action != "test.duration.adjust" || events[2].Action != "test.vusers.scale" || events[3].Action != "test.move" {
		t.Fatalf("audit actions = [%s %s %s %s], want delete/adjust/scale/move", events[0].Action, events[1].Action, events[2].Action, events[3].Action)
	}
}

func TestHandlePinAndUnpinComparisonBaselineWriteAuditEvents(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	seededAt := time.Now().UTC()
	if err := store.SaveScenario(&storage.Scenario{ID: "scenario-1", Name: "Checkout", Duration: "1m", CreatedAt: seededAt, UpdatedAt: seededAt}, nil); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	completedAt := time.Date(2026, time.May, 12, 12, 0, 0, 0, time.UTC)
	pinnedAt := completedAt.Add(-time.Minute)
	for _, record := range []*storage.TestRecord{
		{ID: "baseline-a", Name: "Checkout", Status: "COMPLETE", ScenarioID: "scenario-1", ProjectID: storage.DefaultProjectID, StartTime: completedAt.Add(-5 * time.Minute), EndTime: &completedAt, Groups: []any{}},
		{ID: "baseline-b", Name: "Checkout", Status: "FAILED", ScenarioID: "scenario-1", ProjectID: storage.DefaultProjectID, StartTime: completedAt.Add(-15 * time.Minute), EndTime: &completedAt, BaselinePinned: true, BaselinePinnedAt: &pinnedAt, Groups: []any{}},
	} {
		if err := store.SaveTest(record); err != nil {
			t.Fatalf("SaveTest(%s) returned error: %v", record.ID, err)
		}
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	pinReq := httptest.NewRequest(http.MethodPost, "/api/v1/tests/baseline-a/comparison-baseline/pin", nil)
	pinResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pinResp, pinReq)
	if pinResp.Code != http.StatusOK {
		t.Fatalf("pin status = %d, want %d body=%s", pinResp.Code, http.StatusOK, pinResp.Body.String())
	}

	unpinReq := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/baseline-a/comparison-baseline/pin", nil)
	unpinResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unpinResp, unpinReq)
	if unpinResp.Code != http.StatusOK {
		t.Fatalf("unpin status = %d, want %d body=%s", unpinResp.Code, http.StatusOK, unpinResp.Body.String())
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 10, ProjectID: storage.DefaultProjectID})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("audit event count = %d, want 2", len(events))
	}
	if events[0].Action != "test.comparison_baseline.unpin" || events[1].Action != "test.comparison_baseline.pin" {
		t.Fatalf("audit actions = [%s %s], want unpin/pin", events[0].Action, events[1].Action)
	}
}
