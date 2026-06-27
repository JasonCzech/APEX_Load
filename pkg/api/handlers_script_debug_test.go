package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestScriptDebugManagerBreakpointsAndStep(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name)
		mu.Unlock()
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	session := manager.Start("", &dsl.Script{
		Name:     "Debugger Session",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
			{Name: "third", Type: "GET", Target: "/third"},
		},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-step",
		Breakpoints:     []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 1}},
		LogLevel:        "debug",
	})
	if session.ID != "debug-step" {
		t.Fatalf("session.ID = %q, want debug-step", session.ID)
	}

	paused := waitForScriptDebugStatus(t, manager, "debug-step", ScriptDebugPaused)
	if paused.CurrentAction == nil || paused.CurrentAction.ActionIndex != 1 {
		t.Fatalf("current action = %#v, want action index 1", paused.CurrentAction)
	}
	mu.Lock()
	gotExecuted := append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(gotExecuted, []string{"first"}) {
		t.Fatalf("executed = %#v, want only first before breakpoint", gotExecuted)
	}

	stepped, ok, changed := manager.Step("debug-step")
	if !ok || !changed {
		t.Fatalf("Step ok=%v changed=%v session=%#v, want changed", ok, changed, stepped)
	}
	paused = waitForScriptDebugEventCount(t, manager, "debug-step", 2, ScriptDebugPaused)
	mu.Lock()
	gotExecuted = append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(gotExecuted, []string{"first", "second"}) {
		t.Fatalf("executed after step = %#v, want first second", gotExecuted)
	}
	if len(paused.RawOutput) == 0 || !strings.Contains(strings.Join(paused.RawOutput, "\n"), "second completed") {
		t.Fatalf("raw output = %#v, want second completion evidence", paused.RawOutput)
	}

	resumed, ok, changed := manager.Resume("debug-step")
	if !ok || !changed {
		t.Fatalf("Resume ok=%v changed=%v session=%#v, want changed", ok, changed, resumed)
	}
	completed := waitForScriptDebugStatus(t, manager, "debug-step", ScriptDebugCompleted)
	if len(completed.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(completed.Events))
	}
}

func TestScriptDebugManagerBreakOnFailurePauses(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name)
		mu.Unlock()
		if action.Name == "second" {
			return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "boom"}, nil
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	script := &dsl.Script{
		Name:     "Break On Failure",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Target: "/second"},
			{Name: "third", Type: "GET", Target: "/third"},
		},
	}
	// Continue-on-error so the run resumes past the failure once the operator continues.
	script.Config.ErrorHandling.ContinueOnError = true

	session := manager.Start("", script, ScriptDebugStartRequest{
		ReplaySessionID: "debug-bof",
		BreakOnFailure:  true,
		LogLevel:        "debug",
	})
	if !session.BreakOnFailure {
		t.Fatalf("session.BreakOnFailure = false, want true")
	}

	paused := waitForScriptDebugStatus(t, manager, "debug-bof", ScriptDebugPaused)
	mu.Lock()
	gotExecuted := append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(gotExecuted, []string{"first", "second"}) {
		t.Fatalf("executed at failure pause = %#v, want first second (third must wait)", gotExecuted)
	}
	if len(paused.RawOutput) == 0 || !strings.Contains(strings.Join(paused.RawOutput, "\n"), "Paused after failure") {
		t.Fatalf("raw output = %#v, want pause-after-failure evidence", paused.RawOutput)
	}

	resumed, ok, changed := manager.Resume("debug-bof")
	if !ok || !changed {
		t.Fatalf("Resume ok=%v changed=%v session=%#v, want changed", ok, changed, resumed)
	}
	// Under continue-on-error the run finishes after the failure, but a failed
	// action makes the terminal status "failed".
	finished := waitForScriptDebugStatus(t, manager, "debug-bof", ScriptDebugFailed)
	if len(finished.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(finished.Events))
	}
	mu.Lock()
	gotExecuted = append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(gotExecuted, []string{"first", "second", "third"}) {
		t.Fatalf("executed after resume = %#v, want all three", gotExecuted)
	}
}

func TestHandleUpdateScriptDebugOptions(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	server.scriptDebug.Start("", &dsl.Script{
		Name:     "Options Draft",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "first", Type: "GET", Target: "/first"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-options", StartPaused: true})
	t.Cleanup(func() { _, _ = server.scriptDebug.Stop("debug-options") })

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/script-debug/sessions/debug-options/options", bytes.NewReader([]byte(`{"break_on_failure":true}`)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Session ScriptDebugSession `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !payload.Session.BreakOnFailure {
		t.Fatalf("session.break_on_failure = false, want true")
	}

	// Missing field is a bad request (partial update with nothing to change).
	badReq := httptest.NewRequest(http.MethodPatch, "/api/v1/script-debug/sessions/debug-options/options", bytes.NewReader([]byte(`{}`)))
	badReq.Header.Set("Content-Type", "application/json")
	badResp := httptest.NewRecorder()
	server.mux.ServeHTTP(badResp, badReq)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("missing-field status = %d, want %d body=%s", badResp.Code, http.StatusBadRequest, badResp.Body.String())
	}
}

func TestScriptDebugManagerBreakOnFailureAtFailingChild(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name)
		mu.Unlock()
		if action.Name == "child2" {
			return &protocol.Result{ActionName: action.Name, Success: false, StatusCode: 500, ErrorMessage: "boom"}, nil
		}
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	script := &dsl.Script{
		Name:     "Seq Break On Failure",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{{
			Name:   "txn",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Target: "https://example.test",
			Params: map[string]any{"requests": []dsl.Action{
				{Name: "child1", Type: "GET", Method: "GET", Target: "/1"},
				{Name: "child2", Type: "GET", Method: "GET", Target: "/2"},
				{Name: "child3", Type: "GET", Method: "GET", Target: "/3"},
			}},
		}},
	}
	// Continue-on-error so the sequence resumes past the failing child on Resume.
	script.Config.ErrorHandling.ContinueOnError = true

	manager.Start("", script, ScriptDebugStartRequest{
		ReplaySessionID: "debug-seq-bof",
		BreakOnFailure:  true,
		LogLevel:        "debug",
	})

	paused := waitForScriptDebugStatus(t, manager, "debug-seq-bof", ScriptDebugPaused)
	mu.Lock()
	got := append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(got, []string{"child1", "child2"}) {
		t.Fatalf("executed at failure pause = %#v, want child1 child2 (child3 held)", got)
	}
	if paused.CurrentAction == nil || paused.CurrentAction.RequestIndex == nil || *paused.CurrentAction.RequestIndex != 1 {
		t.Fatalf("current action = %#v, want sub-request index 1", paused.CurrentAction)
	}
	if !strings.Contains(strings.Join(paused.RawOutput, "\n"), "Paused after failure") {
		t.Fatalf("raw output = %#v, want pause-after-failure evidence", paused.RawOutput)
	}

	if _, ok, changed := manager.Resume("debug-seq-bof"); !ok || !changed {
		t.Fatalf("Resume ok=%v changed=%v, want changed", ok, changed)
	}
	finished := waitForScriptDebugStatus(t, manager, "debug-seq-bof", ScriptDebugFailed)
	if len(finished.Events) != 3 {
		t.Fatalf("events = %d, want 3 (all children once resumed)", len(finished.Events))
	}
	mu.Lock()
	got = append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(got, []string{"child1", "child2", "child3"}) {
		t.Fatalf("executed after resume = %#v, want all three children", got)
	}
}

func TestScriptDebugManagerPausesAtChildBreakpoint(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		executed = append(executed, action.Name)
		mu.Unlock()
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})

	manager := NewScriptDebugManager(NewHub())
	target := 1
	manager.Start("", &dsl.Script{
		Name:     "Seq Child BP",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{{
			Name: "txn", Type: "http_sequence", Method: "SEQUENCE", Target: "https://example.test",
			Params: map[string]any{"requests": []dsl.Action{
				{Name: "c0", Type: "GET", Method: "GET", Target: "/0"},
				{Name: "c1", Type: "GET", Method: "GET", Target: "/1"},
				{Name: "c2", Type: "GET", Method: "GET", Target: "/2"},
			}},
		}},
	}, ScriptDebugStartRequest{
		ReplaySessionID: "debug-child-bp",
		Breakpoints:     []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 0, RequestIndex: &target}},
	})

	paused := waitForScriptDebugStatus(t, manager, "debug-child-bp", ScriptDebugPaused)
	mu.Lock()
	got := append([]string(nil), executed...)
	mu.Unlock()
	if !slices.Equal(got, []string{"c0"}) {
		t.Fatalf("executed at breakpoint = %#v, want only c0 (paused before c1)", got)
	}
	if paused.CurrentAction == nil || paused.CurrentAction.RequestIndex == nil || *paused.CurrentAction.RequestIndex != 1 {
		t.Fatalf("current action = %#v, want sub-request index 1", paused.CurrentAction)
	}

	if _, ok, changed := manager.Resume("debug-child-bp"); !ok || !changed {
		t.Fatalf("Resume failed")
	}
	completed := waitForScriptDebugStatus(t, manager, "debug-child-bp", ScriptDebugCompleted)
	if len(completed.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(completed.Events))
	}
}

func TestNormalizeDebugBreakpointsSubRequest(t *testing.T) {
	ri0, ri1, neg := 0, 1, -1
	out := normalizeDebugBreakpoints([]ScriptDebugBreakpoint{
		{Phase: "action", ActionIndex: 0},                                // whole-action
		{Phase: "action", ActionIndex: 0},                                // dup whole-action
		{Phase: "action", ActionIndex: 0, RequestIndex: &ri0},            // child 0 (distinct from whole)
		{Phase: "action", ActionIndex: 0, RequestIndex: &ri1},            // child 1
		{Phase: "action", ActionIndex: 0, RequestIndex: &ri1},            // dup child 1
		{Phase: "action", ActionIndex: 0, RequestIndex: &neg},            // rejected (negative)
		{Phase: "action", ActionIndex: 0, StepPath: []string{"then[0]"}}, // control-flow leaf
	})
	if len(out) != 4 {
		t.Fatalf("normalized = %d (%#v), want 4 (whole, child0, child1, then[0])", len(out), out)
	}
}

func TestHasBreakpointLockedSubRequest(t *testing.T) {
	ri0, ri1 := 0, 1

	child := &ScriptDebugSession{Breakpoints: []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 0, RequestIndex: &ri1}}}
	if !child.hasBreakpointLocked(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, RequestIndex: &ri1}) {
		t.Fatalf("child breakpoint should match request_index 1")
	}
	if child.hasBreakpointLocked(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, RequestIndex: &ri0, LeafOrdinal: 0}) {
		t.Fatalf("child breakpoint must NOT match request_index 0")
	}

	whole := &ScriptDebugSession{Breakpoints: []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 0}}}
	if !whole.hasBreakpointLocked(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, LeafOrdinal: 0}) {
		t.Fatalf("whole-action breakpoint should match the first leaf")
	}
	if whole.hasBreakpointLocked(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, LeafOrdinal: 3}) {
		t.Fatalf("whole-action breakpoint must NOT match a later leaf (would fire N times)")
	}

	step := &ScriptDebugSession{Breakpoints: []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 0, StepPath: []string{"loop[0]"}}}}
	if !step.hasBreakpointLocked(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"loop[0]"}, LeafOrdinal: 5}) {
		t.Fatalf("step-path breakpoint should match loop[0] on any iteration")
	}
	if step.hasBreakpointLocked(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0, StepPath: []string{"loop[1]"}}) {
		t.Fatalf("step-path breakpoint must NOT match a different step")
	}
}

func TestScriptDebugManagerPrunesTerminalSessions(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	terminalAt := now.Add(-2 * time.Hour)
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["old"] = &ScriptDebugSession{
		ID:             "old",
		Status:         ScriptDebugCompleted,
		StartedAt:      &terminalAt,
		CompletedAt:    &terminalAt,
		lastActivityAt: terminalAt,
		terminalAt:     &terminalAt,
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := manager.Get("old"); ok {
		t.Fatal("expected old terminal session to be pruned")
	}
}

func TestScriptDebugManagerSessionCapEvictsOldestTerminal(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 2,
		now:         func() time.Time { return now },
	}
	for i, id := range []string{"oldest", "middle", "newest"} {
		terminalAt := now.Add(-time.Duration(3-i) * time.Minute)
		manager.sessions[id] = &ScriptDebugSession{
			ID:             id,
			Status:         ScriptDebugCompleted,
			StartedAt:      &terminalAt,
			CompletedAt:    &terminalAt,
			lastActivityAt: terminalAt,
			terminalAt:     &terminalAt,
		}
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := manager.sessions["oldest"]; ok {
		t.Fatal("expected oldest terminal session to be evicted")
	}
	if len(manager.sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(manager.sessions))
	}
}

func TestScriptDebugManagerSessionCapEvictsOldestNonTerminalWhenNoTerminalExists(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	cancelled := make(chan string, 1)
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 2,
		now:         func() time.Time { return now },
	}
	for i, id := range []string{"oldest", "middle", "newest"} {
		lastActivity := now.Add(-time.Duration(3-i) * time.Minute)
		sessionID := id
		manager.sessions[id] = &ScriptDebugSession{
			ID:             id,
			Status:         ScriptDebugRunning,
			StartedAt:      &lastActivity,
			lastActivityAt: lastActivity,
			cancel:         func() { cancelled <- sessionID },
		}
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := manager.sessions["oldest"]; ok {
		t.Fatal("expected oldest non-terminal session to be evicted")
	}
	select {
	case id := <-cancelled:
		if id != "oldest" {
			t.Fatalf("cancelled session = %q, want oldest", id)
		}
	default:
		t.Fatal("expected oldest non-terminal session cancel function to be called")
	}
}

func TestScriptDebugManagerCancelsIdleRunningSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-2 * time.Minute)
	cancelled := make(chan struct{})
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Minute,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["idle"] = &ScriptDebugSession{
		ID:             "idle",
		Status:         ScriptDebugRunning,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected idle session cancel function to be called")
	}
	session, ok := manager.Get("idle")
	if !ok {
		t.Fatal("idle session should remain fetchable until terminal TTL expires")
	}
	if session.Status != ScriptDebugStopped {
		t.Fatalf("status = %q, want stopped", session.Status)
	}
}

func TestScriptDebugManagerCancelsIdlePausedSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-2 * time.Minute)
	cancelled := make(chan struct{})
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Minute,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["paused-idle"] = &ScriptDebugSession{
		ID:             "paused-idle",
		Status:         ScriptDebugPaused,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
		stateChanged:   make(chan struct{}),
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected paused idle session cancel function to be called")
	}
	session, ok := manager.Get("paused-idle")
	if !ok {
		t.Fatal("paused idle session should remain fetchable until terminal TTL expires")
	}
	if session.Status != ScriptDebugStopped {
		t.Fatalf("status = %q, want stopped", session.Status)
	}
}

func TestScriptDebugManagerDoesNotCancelRecentlyActivePausedSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-30 * time.Second)
	cancelled := make(chan struct{})
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Minute,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["paused-active"] = &ScriptDebugSession{
		ID:             "paused-active",
		Status:         ScriptDebugPaused,
		StartedAt:      &lastActivity,
		lastActivityAt: lastActivity,
		cancel:         func() { close(cancelled) },
		stateChanged:   make(chan struct{}),
	}

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	select {
	case <-cancelled:
		t.Fatal("recently active paused session should not be cancelled")
	default:
	}
	session, ok := manager.Get("paused-active")
	if !ok {
		t.Fatal("paused active session should remain fetchable")
	}
	if session.Status != ScriptDebugPaused {
		t.Fatalf("status = %q, want paused", session.Status)
	}
}

func TestScriptDebugManagerStartReusedIDCancelsPriorSession(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	cancelled := make(chan struct{})
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions["reuse"] = &ScriptDebugSession{
		ID:             "reuse",
		Status:         ScriptDebugRunning,
		StartedAt:      &now,
		lastActivityAt: now,
		cancel:         func() { close(cancelled) },
	}

	started := manager.Start("", &dsl.Script{Name: "Replacement", Version: "1.0.0", Protocol: "http"}, ScriptDebugStartRequest{
		ReplaySessionID: "reuse",
		StartPaused:     true,
	})
	if started.ID != "reuse" {
		t.Fatalf("started id = %q, want reuse", started.ID)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("expected prior session cancel function to be called")
	}
	session, ok := manager.Get("reuse")
	if !ok {
		t.Fatal("replacement session not found")
	}
	if session.Group != "Replacement-debug" {
		t.Fatalf("replacement group = %q, want Replacement-debug", session.Group)
	}
	_, _ = manager.Stop("reuse")
}

func TestHandleStartScriptDebugDraftScriptReturnsSession(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"group_name":        "debug-editor",
		"project_id":        "project-debug-draft",
		"replay_session_id": "debug-api",
		"start_paused":      true,
		"log_level":         "trace",
		"breakpoints": []map[string]any{{
			"phase":        "action",
			"action_index": 0,
		}},
		"script": map[string]any{
			"name":     "HTTP Draft",
			"version":  "1.0.0",
			"protocol": protocolName,
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name":   "Get home",
				"type":   "GET",
				"method": "GET",
				"target": "/",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		DebugSessionID string             `json:"debug_session_id"`
		Session        ScriptDebugSession `json:"session"`
		Preflight      ReplayPreflight    `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.DebugSessionID != "debug-api" || payload.Session.Status != ScriptDebugPaused || payload.Session.LogLevel != "trace" {
		t.Fatalf("payload id/status/log_level = %q/%q/%q, want debug-api/%q/trace", payload.DebugSessionID, payload.Session.Status, payload.Session.LogLevel, ScriptDebugPaused)
	}
	if payload.Session.ProjectID != "project-debug-draft" {
		t.Fatalf("project_id = %q, want project-debug-draft", payload.Session.ProjectID)
	}
	if payload.Preflight.ActionCount != 1 || payload.Session.Preflight == nil || payload.Session.RuntimeProfile.LogLevel != "trace" {
		t.Fatalf("preflight/runtime = %#v/%#v, want preflight and trace runtime profile", payload.Preflight, payload.Session.RuntimeProfile)
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/script-debug/sessions/debug-api/export?format=log", nil)
	exportResp := httptest.NewRecorder()
	server.mux.ServeHTTP(exportResp, exportReq)
	if exportResp.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d body=%s", exportResp.Code, http.StatusOK, exportResp.Body.String())
	}
	if !strings.Contains(exportResp.Body.String(), "Debugger session started") {
		t.Fatalf("export body = %q, want debugger log", exportResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/script-debug/sessions/debug-api/stop", nil)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d body=%s", stopResp.Code, http.StatusOK, stopResp.Body.String())
	}
}

func TestHandleStartScriptDebugDraftScriptRejectsProjectMismatch(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := []byte(`{"project_id":"project-a","script":{"name":"HTTP Draft","version":"1.0.0","protocol":"http","project_id":"project-b","config":{"base_url":"https://example.com"},"actions":[{"name":"Get home","type":"GET","method":"GET","target":"/"}]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestHandleGetScriptDebugSessionNotFound(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/script-debug/sessions/missing", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
}

func TestHandleStartReplaySessionDraftScriptReturnsUnifiedPreflight(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"replay_session_id": "unified-replay-api",
		"start_paused":      true,
		"log_level":         "debug",
		"script": map[string]any{
			"name":     "Unified Replay",
			"version":  "1.0.0",
			"protocol": protocolName,
			"config": map[string]any{
				"base_url": "https://example.com",
			},
			"actions": []map[string]any{{
				"name":   "Get account",
				"type":   "GET",
				"method": "GET",
				"target": "/accounts/{{var.account_id}}",
			}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/replay/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		ReplaySessionID string             `json:"replay_session_id"`
		Session         ScriptDebugSession `json:"session"`
		Preflight       ReplayPreflight    `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.ReplaySessionID != "unified-replay-api" || payload.Session.Mode != "replay" || payload.Session.Preflight == nil {
		t.Fatalf("payload id/mode/preflight_nil = %q/%q/%t, want unified-replay-api/replay/false", payload.ReplaySessionID, payload.Session.Mode, payload.Session.Preflight == nil)
	}
	if !slices.Contains(payload.Preflight.MissingVariables, "account_id") || payload.Preflight.ActionCount != 1 {
		t.Fatalf("preflight = %#v, want missing account_id and one action", payload.Preflight)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/replay/sessions/unified-replay-api", nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("unified get status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/replay/sessions/unified-replay-api/stop", nil)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("unified stop status = %d, want %d body=%s", stopResp.Code, http.StatusOK, stopResp.Body.String())
	}
}

func TestHandleUpdateScriptDebugBreakpoints(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	server.scriptDebug.Start("", &dsl.Script{
		Name:     "Breakpoint Draft",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "first", Type: "GET", Target: "/first"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-breakpoints", StartPaused: true})

	body := []byte(`{"breakpoints":[{"phase":"action","action_index":0},{"phase":"action","action_index":0},{"phase":"init","action_index":-1}]}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/script-debug/sessions/debug-breakpoints/breakpoints", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Session ScriptDebugSession `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(payload.Session.Breakpoints) != 1 || payload.Session.Breakpoints[0].ActionIndex != 0 {
		t.Fatalf("breakpoints = %#v, want single normalized action breakpoint", payload.Session.Breakpoints)
	}
	_, _ = server.scriptDebug.Stop("debug-breakpoints")
}

func TestScriptDebugManagerLogLevelFiltersLowerSeverity(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	manager := NewScriptDebugManager(NewHub())
	session := manager.Start("", &dsl.Script{
		Name:     "Log Filter",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "first", Type: "GET", Target: "/first"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-log-filter", StartPaused: true, LogLevel: "warn"})
	if len(session.Logs) != 0 || len(session.RawOutput) != 0 {
		t.Fatalf("start logs = %#v raw=%#v, want info/debug filtered at warn level", session.Logs, session.RawOutput)
	}
	stopped, ok := manager.Stop("debug-log-filter")
	if !ok {
		t.Fatal("Stop returned ok=false")
	}
	if len(stopped.Logs) != 1 || stopped.Logs[0].Level != "warn" || !strings.Contains(stopped.Logs[0].Message, "Stop requested") {
		t.Fatalf("logs after stop = %#v, want only warn stop log", stopped.Logs)
	}
}

func TestHandleScriptDebugMalformedBodiesAndConflicts(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	server.scriptDebug.Start("", &dsl.Script{
		Name:     "Conflicts",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "first", Type: "GET", Target: "/first"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-conflict", StartPaused: true})
	t.Cleanup(func() { _, _ = server.scriptDebug.Stop("debug-conflict") })

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "pause paused session", method: http.MethodPost, path: "/api/v1/replay/sessions/debug-conflict/pause", want: http.StatusConflict},
		{name: "malformed step body", method: http.MethodPost, path: "/api/v1/replay/sessions/debug-conflict/step", body: `{`, want: http.StatusBadRequest},
		{name: "malformed breakpoints body", method: http.MethodPatch, path: "/api/v1/replay/sessions/debug-conflict/breakpoints", body: `{`, want: http.StatusBadRequest},
		{name: "malformed options body", method: http.MethodPatch, path: "/api/v1/replay/sessions/debug-conflict/options", body: `{`, want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, tc.want, resp.Body.String())
			}
		})
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/replay/sessions/debug-conflict/resume", nil)
	resumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resumeResp, resumeReq)
	if resumeResp.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want %d body=%s", resumeResp.Code, http.StatusOK, resumeResp.Body.String())
	}
	stepReq := httptest.NewRequest(http.MethodPost, "/api/v1/replay/sessions/debug-conflict/step", nil)
	stepResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stepResp, stepReq)
	if stepResp.Code != http.StatusConflict {
		t.Fatalf("step-running status = %d, want %d body=%s", stepResp.Code, http.StatusConflict, stepResp.Body.String())
	}
}

func TestScriptDebugManagerConcurrentPauseResumeAndStep(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Concurrent Controls",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions: []dsl.Action{
			{Name: "first", Type: "GET", Method: "GET", Target: "/first"},
			{Name: "second", Type: "GET", Method: "GET", Target: "/second"},
		},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-controls-race", StartPaused: true})
	waitForScriptDebugStatus(t, manager, "debug-controls-race", ScriptDebugPaused)

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, _, _ = manager.Pause("debug-controls-race")
		}()
		go func() {
			defer wg.Done()
			_, _, _ = manager.Resume("debug-controls-race")
		}()
		go func() {
			defer wg.Done()
			_, _, _ = manager.StepMode("debug-controls-race", "over")
		}()
	}
	wg.Wait()
	_, _ = manager.Stop("debug-controls-race")
}

func TestHandleStopScriptDebugSessionIsIdempotent(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	server.scriptDebug.Start("", &dsl.Script{
		Name:     "Stop Twice",
		Protocol: protocolName,
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "first", Type: "GET", Target: "/first"}},
	}, ScriptDebugStartRequest{ReplaySessionID: "debug-stop-twice", StartPaused: true})

	for attempt := 1; attempt <= 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/replay/sessions/debug-stop-twice/stop", nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want %d body=%s", attempt, resp.Code, http.StatusOK, resp.Body.String())
		}
		var payload struct {
			Session ScriptDebugSession `json:"session"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("attempt %d json.Unmarshal returned error: %v", attempt, err)
		}
		if payload.Session.Status != ScriptDebugStopped {
			t.Fatalf("attempt %d status = %q, want %q", attempt, payload.Session.Status, ScriptDebugStopped)
		}
	}
}

func waitForScriptDebugStatus(t *testing.T, manager *ScriptDebugManager, sessionID string, status string) *ScriptDebugSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := manager.Get(sessionID)
		if !ok {
			t.Fatalf("session %s not found", sessionID)
		}
		if session.Status == status {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := manager.Get(sessionID)
	t.Fatalf("session status = %#v, want %s", session, status)
	return nil
}

func waitForScriptDebugEventCount(t *testing.T, manager *ScriptDebugManager, sessionID string, count int, status string) *ScriptDebugSession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		session, ok := manager.Get(sessionID)
		if !ok {
			t.Fatalf("session %s not found", sessionID)
		}
		if len(session.Events) >= count && session.Status == status {
			return session
		}
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := manager.Get(sessionID)
	t.Fatalf("session = %#v, want %d events and status %s", session, count, status)
	return nil
}
