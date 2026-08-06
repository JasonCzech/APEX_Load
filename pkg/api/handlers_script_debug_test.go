package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	debugAPITestSessionID   = "11111111-1111-4111-8111-111111111111"
	unifiedAPITestSessionID = "22222222-2222-4222-8222-222222222222"
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
	if len(completed.Events) != 0 || completed.FinalReport == nil || len(completed.FinalReport.Events) != 3 {
		t.Fatalf("session/report events = %d/%v, want 0/3", len(completed.Events), completed.FinalReport)
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
	if len(finished.Events) != 0 || finished.FinalReport == nil || len(finished.FinalReport.Events) != 3 {
		t.Fatalf("session/report events = %d/%v, want 0/3", len(finished.Events), finished.FinalReport)
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
	if len(finished.Events) != 0 || finished.FinalReport == nil || len(finished.FinalReport.Events) != 3 {
		t.Fatalf("session/report events = %d/%v, want 0/3 (all children once resumed)", len(finished.Events), finished.FinalReport)
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
	if len(completed.Events) != 0 || completed.FinalReport == nil || len(completed.FinalReport.Events) != 3 {
		t.Fatalf("session/report events = %d/%v, want 0/3", len(completed.Events), completed.FinalReport)
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

func TestScriptDebugSessionSnapshotDetachesCurrentActionAndTimestamps(t *testing.T) {
	now := time.Now().UTC()
	originalTimestamp := now
	requestIndex, iteration := 1, 3
	session := &ScriptDebugSession{
		ID:          "debug-detached",
		Status:      ScriptDebugPaused,
		StartedAt:   &now,
		CompletedAt: &now,
		CurrentAction: &engine.ReplayDebugAction{
			ActionName:    "request",
			RequestIndex:  &requestIndex,
			StepPath:      []string{"requests[1]"},
			Iteration:     &iteration,
			IterationPath: []int{3},
		},
	}

	snapshot := session.snapshot()
	if snapshot == nil || snapshot.CurrentAction == nil {
		t.Fatalf("snapshot = %#v, want current action", snapshot)
	}
	*session.StartedAt = now.Add(time.Hour)
	*session.CompletedAt = now.Add(2 * time.Hour)
	*session.CurrentAction.RequestIndex = 9
	session.CurrentAction.StepPath[0] = "updated"
	*session.CurrentAction.Iteration = 9
	session.CurrentAction.IterationPath[0] = 9

	if snapshot.StartedAt == nil || !snapshot.StartedAt.Equal(originalTimestamp) ||
		snapshot.CompletedAt == nil || !snapshot.CompletedAt.Equal(originalTimestamp) {
		t.Fatalf("snapshot timestamps were not detached: started=%v completed=%v", snapshot.StartedAt, snapshot.CompletedAt)
	}
	if snapshot.CurrentAction.RequestIndex == nil || *snapshot.CurrentAction.RequestIndex != 1 ||
		len(snapshot.CurrentAction.StepPath) != 1 || snapshot.CurrentAction.StepPath[0] != "requests[1]" ||
		snapshot.CurrentAction.Iteration == nil || *snapshot.CurrentAction.Iteration != 3 ||
		len(snapshot.CurrentAction.IterationPath) != 1 || snapshot.CurrentAction.IterationPath[0] != 3 {
		t.Fatalf("snapshot current action was not detached: %#v", snapshot.CurrentAction)
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

// Pruning a retained tombstone must perform the durable lease release after
// dropping the manager mutex (in HA mode the release is a synchronous store
// call that would otherwise freeze every handler and progress path).
func TestScriptDebugManagerPruneReleasesRetainedLeaseOffManagerLock(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	terminalAt := now.Add(-2 * time.Hour)
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	released := false
	releasedOffLock := false
	signal := newReplaySessionLeaseSignal()
	signal.releaseRetainedIDFn = func() {
		released = true
		if manager.mu.TryLock() {
			releasedOffLock = true
			manager.mu.Unlock()
		}
	}
	manager.sessions["old"] = &ScriptDebugSession{
		ID:             "old",
		Status:         ScriptDebugCompleted,
		StartedAt:      &terminalAt,
		CompletedAt:    &terminalAt,
		lastActivityAt: terminalAt,
		terminalAt:     &terminalAt,
		reservation:    signal,
	}

	if removed := manager.PruneExpired(); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if !released {
		t.Fatal("retained lease was not released by prune")
	}
	if !releasedOffLock {
		t.Fatal("retained lease release ran while the manager mutex was held")
	}
}

// The per-action correlation-evidence hot path must only need the manager read
// lock so concurrent debug replays do not serialize behind a write lock.
func TestObserveCorrelationEvidenceRunsUnderManagerReadLock(t *testing.T) {
	manager := NewScriptDebugManager(nil)
	session := &ScriptDebugSession{ID: "obs", ProjectID: "project-a", Status: ScriptDebugRunning}
	manager.sessions["obs"] = session
	controller := scriptDebugController{manager: manager, session: session}

	manager.mu.RLock()
	defer manager.mu.RUnlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		controller.ObserveCorrelationEvidence(context.Background(),
			engine.ReplayDebugAction{Phase: "action", ActionIndex: 0},
			&engine.ReplayActionEvent{
				Phase:       "action",
				ActionIndex: 0,
				Success:     true,
				CorrelationEvidence: &protocol.CorrelationEvidence{
					RequestURL:   "/raw",
					ResponseBody: "body",
				},
			})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ObserveCorrelationEvidence blocked behind a concurrent manager reader (write-lock hot path regression)")
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	if len(session.correlationObservations) != 1 || session.correlationEvidenceErr != "" {
		t.Fatalf("observation was not retained: n=%d err=%q", len(session.correlationObservations), session.correlationEvidenceErr)
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

func TestScriptDebugManagerCorrelationEvidenceBudgetsClearOnlyOldestPrivateEvidence(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	newEvidenceSession := func(id, projectID string, retainedAt time.Time, bytes int) *ScriptDebugSession {
		return &ScriptDebugSession{
			ID:                            id,
			ProjectID:                     projectID,
			Status:                        ScriptDebugCompleted,
			correlationObservations:       []correlate.Observation{{ID: 0, RequestURL: "/"}},
			correlationObservationIDs:     map[int]struct{}{0: {}},
			correlationEvidenceBytes:      bytes,
			correlationEvidenceRetainedAt: retainedAt,
		}
	}

	t.Run("project budget", func(t *testing.T) {
		manager := NewScriptDebugManager(nil)
		manager.correlationEvidenceProjectBytes = 10
		manager.correlationEvidenceGlobalBytes = 100
		manager.sessions["project-old"] = newEvidenceSession("project-old", "project-a", now.Add(-time.Minute), 6)
		manager.sessions["project-new"] = newEvidenceSession("project-new", "project-a", now, 6)
		manager.sessions["other-project"] = newEvidenceSession("other-project", "project-b", now.Add(-2*time.Minute), 8)

		manager.mu.Lock()
		manager.enforceCorrelationEvidenceBudgetsLocked()
		manager.mu.Unlock()

		oldest := manager.sessions["project-old"]
		if oldest.correlationEvidenceBytes != 0 || len(oldest.correlationObservations) != 0 || !strings.Contains(oldest.correlationEvidenceErr, "project retention") {
			t.Fatalf("oldest project evidence was not cleared: %#v", oldest)
		}
		if manager.sessions["project-new"].correlationEvidenceBytes != 6 || manager.sessions["other-project"].correlationEvidenceBytes != 8 {
			t.Fatal("project budget cleared newer or differently-owned evidence")
		}
		if len(manager.sessions) != 3 {
			t.Fatalf("public session count = %d, want 3", len(manager.sessions))
		}
	})

	t.Run("global budget", func(t *testing.T) {
		manager := NewScriptDebugManager(nil)
		manager.correlationEvidenceProjectBytes = 100
		manager.correlationEvidenceGlobalBytes = 10
		manager.sessions["global-old"] = newEvidenceSession("global-old", "project-a", now.Add(-time.Minute), 6)
		manager.sessions["global-new"] = newEvidenceSession("global-new", "project-b", now, 6)

		manager.mu.Lock()
		manager.enforceCorrelationEvidenceBudgetsLocked()
		manager.mu.Unlock()

		oldest := manager.sessions["global-old"]
		if oldest.correlationEvidenceBytes != 0 || !strings.Contains(oldest.correlationEvidenceErr, "global retention") {
			t.Fatalf("oldest global evidence was not cleared: %#v", oldest)
		}
		if manager.sessions["global-new"].correlationEvidenceBytes != 6 || len(manager.sessions) != 2 {
			t.Fatal("global budget removed the public session or cleared newer evidence")
		}
	})
}

func TestCorrelationObservationHeaderProofIsUnambiguousAndDetached(t *testing.T) {
	proof := editableRequestHeaderProof(
		map[string]string{"X-Token": "wire"},
		map[string]string{"X-Token": "wire", "x-token": "wire"},
	)
	if proof["X-Token"] {
		t.Fatal("ambiguous case-insensitive authored headers were marked editable")
	}
	original := correlate.Observation{EditableRequestHeaders: map[string]bool{"X-Token": true}}
	cloned := cloneCorrelationObservation(original)
	cloned.EditableRequestHeaders["X-Token"] = false
	if !original.EditableRequestHeaders["X-Token"] {
		t.Fatal("cloned editable-header proof aliased the private observation")
	}
}

func TestScriptDebugManagerSessionCapPreservesNonTerminalSessionsWhenOverCap(t *testing.T) {
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

	if removed := manager.PruneExpired(); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if len(manager.sessions) != 3 {
		t.Fatalf("session count = %d, want 3", len(manager.sessions))
	}
	for _, id := range []string{"oldest", "middle", "newest"} {
		if _, ok := manager.sessions[id]; !ok {
			t.Fatalf("active session %q was evicted", id)
		}
	}
	select {
	case id := <-cancelled:
		t.Fatalf("active session %q was cancelled to enforce the cap", id)
	default:
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

func TestScriptDebugManagerReplayProgressRefreshesActivity(t *testing.T) {
	before := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	now := before.Add(45 * time.Minute)
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  30 * time.Minute,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	session := &ScriptDebugSession{
		ID: "progress", Status: ScriptDebugRunning, StartedAt: &before, lastActivityAt: before,
	}
	manager.sessions[session.ID] = session
	manager.handleReplayProgress(context.Background(), session, engine.ReplayProgressEvent{Type: engine.ReplayProgressActionStarted})

	session.mu.RLock()
	activity := session.lastActivityAt
	session.mu.RUnlock()
	if !activity.Equal(now) {
		t.Fatalf("last activity = %s, want progress time %s", activity, now)
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

func TestScriptDebugManagerStartReusedIDRejectsCollision(t *testing.T) {
	const sessionID = "44444444-4444-4444-8444-444444444444"
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	cancelled := make(chan struct{})
	manager := NewScriptDebugManager(nil)
	manager.retention = replaySessionRetentionConfig{
		terminalTTL: time.Hour,
		idleCancel:  time.Hour,
		maxSessions: 250,
		now:         func() time.Time { return now },
	}
	manager.sessions[sessionID] = &ScriptDebugSession{
		ID:             sessionID,
		ProjectID:      "project-a",
		Status:         ScriptDebugRunning,
		StartedAt:      &now,
		lastActivityAt: now,
		cancel:         func() { close(cancelled) },
	}

	started, err := manager.StartChecked("", &dsl.Script{Name: "Replacement", Version: "1.0.0", Protocol: "http"}, ScriptDebugStartRequest{
		ReplaySessionID: sessionID,
		StartPaused:     true,
	})
	if !errors.Is(err, errScriptDebugSessionIDConflict) || started != nil {
		t.Fatalf("Start collision = session %#v, error %v", started, err)
	}
	select {
	case <-cancelled:
		t.Fatal("existing session was cancelled by colliding start")
	default:
	}
	session, ok := manager.Get(sessionID)
	if !ok {
		t.Fatal("existing session not found")
	}
	if session.Status != ScriptDebugRunning {
		t.Fatalf("existing status = %q, want running", session.Status)
	}
	_, _ = manager.Stop(sessionID)
}

func TestHandleStartScriptDebugDraftScriptReturnsSession(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"group_name":        "debug-editor",
		"project_id":        "project-debug-draft",
		"replay_session_id": debugAPITestSessionID,
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
	if payload.DebugSessionID != debugAPITestSessionID || payload.Session.Status != ScriptDebugPaused || payload.Session.LogLevel != "trace" {
		t.Fatalf("payload id/status/log_level = %q/%q/%q, want %s/%q/trace", payload.DebugSessionID, payload.Session.Status, payload.Session.LogLevel, debugAPITestSessionID, ScriptDebugPaused)
	}
	if payload.Session.ProjectID != "project-debug-draft" {
		t.Fatalf("project_id = %q, want project-debug-draft", payload.Session.ProjectID)
	}
	if payload.Preflight.ActionCount != 1 || payload.Session.Preflight == nil || payload.Session.RuntimeProfile.LogLevel != "trace" {
		t.Fatalf("preflight/runtime = %#v/%#v, want preflight and trace runtime profile", payload.Preflight, payload.Session.RuntimeProfile)
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v1/script-debug/sessions/"+debugAPITestSessionID+"/export?format=log", nil)
	exportResp := httptest.NewRecorder()
	server.mux.ServeHTTP(exportResp, exportReq)
	if exportResp.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d body=%s", exportResp.Code, http.StatusOK, exportResp.Body.String())
	}
	if !strings.Contains(exportResp.Body.String(), "Debugger session started") {
		t.Fatalf("export body = %q, want debugger log", exportResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/script-debug/sessions/"+debugAPITestSessionID+"/stop", nil)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d body=%s", stopResp.Code, http.StatusOK, stopResp.Body.String())
	}
}

func TestSavedScriptDebugStartsResolveRefKeyToCanonicalID(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	server := recordingTestServer(t)
	stored := server.runner.StoreScript(&dsl.Script{
		Name:      "Canonical debug identity",
		Version:   "1.0.0",
		Protocol:  protocolName,
		ProjectID: "p1",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Name: "load", Type: http.MethodGet, Method: http.MethodGet, Target: "/"}},
	})
	if stored == nil || stored.RefKey == "" {
		t.Fatalf("stored script has no reference key: %#v", stored)
	}

	tests := []struct {
		name      string
		sessionID string
		handle    func(http.ResponseWriter, *http.Request)
	}{
		{name: "debug", sessionID: "33333333-3333-4333-8333-333333333333", handle: server.handleStartScriptDebugScript},
		{name: "replay", sessionID: "44444444-4444-4444-8444-444444444444", handle: server.handleStartReplaySessionScript},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"replay_session_id": test.sessionID, "start_paused": true})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.RefKey+"/sessions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.SetPathValue("id", stored.RefKey)
			resp := httptest.NewRecorder()
			test.handle(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
			}
			var payload struct {
				ScriptID string             `json:"script_id"`
				Session  ScriptDebugSession `json:"session"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.ScriptID != stored.ID || payload.Session.ScriptID != stored.ID {
				t.Fatalf("script identities = response:%q session:%q, want canonical %q", payload.ScriptID, payload.Session.ScriptID, stored.ID)
			}
			_, _ = server.scriptDebug.Stop(test.sessionID)
		})
	}
}

func TestSavedImportedScriptDebugUsesDurableCredentialsForUnchangedProjection(t *testing.T) {
	const importedAuthorization = "Bearer imported-debug-password"
	executed := make(chan *protocol.Action, 1)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		executed <- action
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	server := recordingTestServer(t)
	stored, created, err := server.runner.CreateImportedScriptDurableReportingReplay(&dsl.Script{
		ID:        "trusted-debug-import",
		Name:      "Trusted debug import",
		Version:   "1.0.0",
		Protocol:  protocolName,
		ProjectID: "p1",
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   http.MethodGet,
			Method: http.MethodGet,
			Target: "/",
			Headers: map[string]string{
				"Authorization": importedAuthorization,
			},
		}},
	}, "enterprise_archive")
	if err != nil {
		t.Fatalf("store imported script: %v", err)
	}
	if !created || stored == nil || stored.Script == nil {
		t.Fatalf("stored import = %#v, created=%v", stored, created)
	}
	projected := maskScriptSecrets(stored.Script)
	if projected == nil || projected.Actions[0].Headers["Authorization"] != "Bearer "+maskedScriptSecret {
		t.Fatalf("disclosure projection did not mask imported authorization: %#v", projected)
	}
	edited := cloneScriptDefinition(projected)
	edited.Actions[0].Target = "/edited"
	if matchesStoredScriptDisclosureProjection(edited, stored.Script) {
		t.Fatal("edited disclosure projection inherited durable import trust")
	}
	resolved, trusted := resolveTrustedStoredReplayEcho(projected, stored.Script)
	if !trusted || resolved != stored.Script {
		t.Fatalf("unchanged disclosure echo resolved to %#v trusted=%v, want durable snapshot", resolved, trusted)
	}
	resolved, trusted = resolveTrustedStoredReplayEcho(edited, stored.Script)
	if trusted || resolved != edited {
		t.Fatalf("edited disclosure echo resolved to %#v trusted=%v, want untrusted edit", resolved, trusted)
	}

	body, err := json.Marshal(ScriptDebugStartRequest{
		ProjectID:       stored.ProjectID,
		ReplaySessionID: "55555555-5555-4555-8555-555555555555",
		Script:          projected,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/debug/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", stored.ID)
	resp := httptest.NewRecorder()

	server.handleStartScriptDebugScript(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	select {
	case action := <-executed:
		if got := action.Headers["Authorization"]; got != importedAuthorization {
			t.Fatalf("executed Authorization = %q, want durable imported value", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("debug action did not execute")
	}
	_, _ = server.scriptDebug.Stop("55555555-5555-4555-8555-555555555555")
}

func TestHandleStartScriptDebugRejectsInvalidAndCrossProjectCollidingSessionIDs(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		server := NewServer(ServerConfig{})
		body, _ := json.Marshal(map[string]any{
			"replay_session_id": "predictable-debug-id",
			"project_id":        "project-b",
			"script":            map[string]any{"name": "Draft", "protocol": "http"},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "canonical UUID v4") {
			t.Fatalf("status = %d body=%s, want invalid session-id rejection", resp.Code, resp.Body.String())
		}
	})

	t.Run("cross project collision", func(t *testing.T) {
		const sessionID = "55555555-5555-4555-8555-555555555555"
		server := NewServer(ServerConfig{})
		now := time.Now().UTC()
		cancelled := make(chan struct{})
		server.scriptDebug.mu.Lock()
		server.scriptDebug.sessions[sessionID] = &ScriptDebugSession{
			ID: sessionID, ProjectID: "project-a", Status: ScriptDebugRunning, StartedAt: &now,
			lastActivityAt: now, cancel: func() { close(cancelled) },
		}
		server.scriptDebug.mu.Unlock()
		body, _ := json.Marshal(map[string]any{
			"replay_session_id": sessionID,
			"project_id":        "project-b",
			"start_paused":      true,
			"script": map[string]any{
				"name": "Draft", "version": "1.0.0", "protocol": "http",
				"config":  map[string]any{"base_url": "https://example.test"},
				"actions": []map[string]any{{"name": "load", "type": "GET", "method": "GET", "target": "/"}},
			},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
		}
		select {
		case <-cancelled:
			t.Fatal("cross-project colliding start cancelled the established session")
		default:
		}
		existing, ok := server.scriptDebug.Get(sessionID)
		if !ok || existing.ProjectID != "project-a" || existing.Status != ScriptDebugRunning {
			t.Fatalf("existing session = %#v, want untouched project-a session", existing)
		}
		_, _ = server.scriptDebug.Stop(sessionID)
	})

	t.Run("cross project capacity", func(t *testing.T) {
		const (
			establishedSessionID = "99999999-9999-4999-8999-999999999999"
			newSessionID         = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		)
		server := NewServer(ServerConfig{})
		now := time.Now().UTC()
		cancelled := make(chan struct{})
		server.scriptDebug.mu.Lock()
		server.scriptDebug.retention.maxSessions = 1
		server.scriptDebug.sessions[establishedSessionID] = &ScriptDebugSession{
			ID: establishedSessionID, ProjectID: "project-a", Status: ScriptDebugRunning, StartedAt: &now,
			lastActivityAt: now, cancel: func() { close(cancelled) }, stateChanged: make(chan struct{}),
		}
		server.scriptDebug.mu.Unlock()

		body, _ := json.Marshal(map[string]any{
			"replay_session_id": newSessionID,
			"project_id":        "project-b",
			"start_paused":      true,
			"script": map[string]any{
				"name": "Draft", "version": "1.0.0", "protocol": "http",
				"config":  map[string]any{"base_url": "https://example.test"},
				"actions": []map[string]any{{"name": "load", "type": "GET", "method": "GET", "target": "/"}},
			},
		})
		start := func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			return resp
		}

		resp := start()
		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d body=%s, want script debug capacity rejection", resp.Code, resp.Body.String())
		}
		select {
		case <-cancelled:
			t.Fatal("cross-project capacity rejection cancelled the established session")
		default:
		}
		existing, ok := server.scriptDebug.Get(establishedSessionID)
		if !ok || existing.ProjectID != "project-a" || existing.Status != ScriptDebugRunning {
			t.Fatalf("existing session = %#v, want untouched project-a session", existing)
		}
		if _, ok := server.scriptDebug.Get(newSessionID); ok {
			t.Fatal("capacity-rejected script debug session was retained")
		}

		_, _ = server.scriptDebug.Stop(establishedSessionID)
		resp = start()
		if resp.Code != http.StatusOK {
			t.Fatalf("retry status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
		}
		_, _ = server.scriptDebug.Stop(newSessionID)
	})
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

func TestHandleStartScriptDebugDraftScriptWarnsWhenParameterDataDiffersFromScenarioLoad(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"start_paused": true,
		"script": map[string]any{
			"name":     "Parameterized Debug",
			"version":  "1.0.0",
			"protocol": protocolName,
			"config":   map[string]any{"base_url": "https://example.com"},
			"parameters": []map[string]any{{
				"name": "subscriber", "type": "file", "selection": "random", "data_source": "subscribers", "column": "id",
			}},
			"data_sources": []map[string]any{{
				"name": "subscribers", "type": "csv", "first_row_header": true, "inline_data": "id\n1\n2\n",
			}},
			"actions": []map[string]any{{"name": "Get account", "type": "GET", "method": "GET", "target": "/accounts/{{param.subscriber}}"}},
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
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !strings.Contains(strings.Join(payload.Warnings, "\n"), "deterministic VUser") {
		t.Fatalf("warnings = %#v, want deterministic VUser parity warning", payload.Warnings)
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
		"replay_session_id": unifiedAPITestSessionID,
		"start_paused":      true,
		"log_level":         "debug",
		"variables":         map[string]string{"account_id": "123"},
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
	if payload.ReplaySessionID != unifiedAPITestSessionID || payload.Session.Mode != "replay" || payload.Session.Preflight == nil {
		t.Fatalf("payload id/mode/preflight_nil = %q/%q/%t, want %s/replay/false", payload.ReplaySessionID, payload.Session.Mode, payload.Session.Preflight == nil, unifiedAPITestSessionID)
	}
	if len(payload.Preflight.MissingVariables) != 0 || payload.Preflight.ActionCount != 1 {
		t.Fatalf("preflight = %#v, want seeded variables and one action", payload.Preflight)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/replay/sessions/"+unifiedAPITestSessionID, nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("unified get status = %d, want %d body=%s", getResp.Code, http.StatusOK, getResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/replay/sessions/"+unifiedAPITestSessionID+"/stop", nil)
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

func TestHandleStartScriptDebugDraftScriptRejectsMalformedAndMultiJSONBodies(t *testing.T) {
	server := NewServer(ServerConfig{})
	cases := []struct {
		name          string
		body          string
		expectedError string
	}{
		{
			name:          "empty body",
			body:          "",
			expectedError: "request body is required",
		},
		{
			name:          "malformed json",
			body:          "{",
			expectedError: "invalid request body",
		},
		{
			name:          "multiple json values",
			body:          `{"project_id":"project-a","script":{"name":"Draft","version":"1.0.0","protocol":"http","actions":[{"name":"load","type":"GET","method":"GET","target":"/"}]}} {"project_id":"project-a"}`,
			expectedError: "multiple JSON values are not allowed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/debug/sessions", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()

			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), tc.expectedError) {
				t.Fatalf("body = %q, want %q", resp.Body.String(), tc.expectedError)
			}
		})
	}
}
