package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
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
		waitForStatusAny(t, server.runner, testID, 5*time.Second, "COMPLETE", "FAILED")
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