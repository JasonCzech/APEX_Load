package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestScriptDebugConcurrentFailurePausesAtJoinedGroupBoundary(t *testing.T) {
	for _, failingIndex := range []int{0, 1} {
		t.Run([]string{"first sibling fails", "last sibling fails"}[failingIndex], func(t *testing.T) {
			arrivals := make(chan string, 2)
			release := make(chan struct{})
			var releaseOnce sync.Once
			var consumed atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/first":
					arrivals <- "first"
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					if failingIndex == 0 {
						w.WriteHeader(http.StatusInternalServerError)
					} else {
						w.WriteHeader(http.StatusOK)
					}
				case "/second":
					arrivals <- "second"
					if failingIndex == 1 {
						w.WriteHeader(http.StatusInternalServerError)
					} else {
						w.WriteHeader(http.StatusOK)
					}
				case "/consume":
					consumed.Add(1)
					w.WriteHeader(http.StatusOK)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(target.Close)
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			manager := NewScriptDebugManager(NewHub())
			t.Cleanup(manager.Close)
			script := &dsl.Script{Name: "Concurrent failure boundary", Protocol: "http", Version: "1", Config: dsl.ScriptConfig{BaseURL: target.URL}, Actions: []dsl.Action{
				{Name: "Joined group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "max_concurrency": 2, "requests": []dsl.Action{
					{Name: "Held first", Type: "GET", Target: "/first", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
					{Name: "Second sibling", Type: "GET", Target: "/second", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
				}}},
				{Name: "Consumer", Type: "GET", Target: "/consume"},
			}}
			script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(true)
			manager.Start("", script, ScriptDebugStartRequest{ReplaySessionID: "concurrent-failure-boundary", BreakOnFailure: true})
			for range 2 {
				select {
				case <-arrivals:
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent siblings did not both reach the loopback target")
				}
			}
			beforeJoin, ok := manager.Get("concurrent-failure-boundary")
			if !ok || beforeJoin.Status != ScriptDebugRunning || beforeJoin.CurrentAction == nil || beforeJoin.CurrentAction.ActionName != "Joined group" || consumed.Load() != 0 {
				t.Fatal("debugger left the group boundary before every sibling joined")
			}
			releaseOnce.Do(func() { close(release) })
			paused := waitForScriptDebugStatus(t, manager, "concurrent-failure-boundary", ScriptDebugPaused)
			if paused.CurrentAction == nil || paused.CurrentAction.ActionName != "Joined group" || paused.CurrentAction.ActionType != "http_sequence" || paused.CurrentAction.RequestIndex != nil || consumed.Load() != 0 {
				t.Fatal("failure polling snapshot did not preserve the joined group pause boundary")
			}
			if len(paused.Events) != 2 || paused.Events[0].RequestIndex == nil || *paused.Events[0].RequestIndex != 0 || paused.Events[1].RequestIndex == nil || *paused.Events[1].RequestIndex != 1 || paused.Events[failingIndex].Success {
				t.Fatal("joined sibling failure evidence lost its exact leaf identity")
			}
			if paused.CurrentEvent == nil || paused.CurrentEvent.Success || paused.CurrentEvent.RequestIndex == nil || *paused.CurrentEvent.RequestIndex != failingIndex || paused.CurrentEvent.EventID != paused.Events[failingIndex].EventID || paused.CurrentEventIndex != paused.Events[failingIndex].EventIndex {
				t.Fatal("joined failure pause selected a successful or stale sibling for the evidence inspector")
			}

			if _, ok, changed := manager.Resume("concurrent-failure-boundary"); !ok || !changed {
				t.Fatal("failed group could not resume under continue-on-error")
			}
			finished := waitForScriptDebugStatus(t, manager, "concurrent-failure-boundary", ScriptDebugFailed)
			if consumed.Load() != 1 || finished.FinalReport == nil || len(finished.FinalReport.Events) != 3 {
				t.Fatal("resume did not execute the consumer exactly once after the group")
			}
		})
	}
}

func TestPausedConcurrentFailureEventRejectsStaleOrOmittedEvidence(t *testing.T) {
	start := time.Now()
	index := 0
	action := engine.ReplayDebugAction{Phase: "action", ActionIndex: 2, ActionName: "Joined group", ActionType: "http_sequence", StepPath: []string{"body"}, IterationPath: []int{3}}
	group := engine.ReplayActionEvent{StartedAt: start, CompletedAt: start.Add(time.Second)}
	leaf := engine.ReplayActionEvent{EventID: "exact-failed-leaf", Phase: "action", ActionIndex: 2, ParentActionName: "Joined group", RequestIndex: &index, StepPath: []string{"body", "requests[0]"}, IterationPath: []int{3}, StartedAt: start.Add(time.Millisecond), CompletedAt: start.Add(time.Second), Success: false}
	cases := []struct {
		name   string
		change func(*engine.ReplayActionEvent)
	}{
		{"successful last sibling", func(e *engine.ReplayActionEvent) { e.Success = true }},
		{"previous invocation", func(e *engine.ReplayActionEvent) { e.StartedAt = start.Add(-time.Second) }},
		{"later invocation", func(e *engine.ReplayActionEvent) { e.CompletedAt = start.Add(2 * time.Second) }},
		{"foreign phase", func(e *engine.ReplayActionEvent) { e.Phase = "init" }},
		{"foreign top level", func(e *engine.ReplayActionEvent) { e.ActionIndex++ }},
		{"foreign parent", func(e *engine.ReplayActionEvent) { e.ParentActionName = "Other group" }},
		{"foreign branch", func(e *engine.ReplayActionEvent) { e.StepPath = []string{"else", "requests[0]"} }},
		{"foreign iteration", func(e *engine.ReplayActionEvent) { e.IterationPath = []int{2} }},
	}
	if pausedConcurrentFailureEvent(nil, action, group) != nil {
		t.Fatal("omitted failure invented evidence")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := leaf
			tc.change(&candidate)
			if pausedConcurrentFailureEvent([]engine.ReplayActionEvent{candidate}, action, group) != nil {
				t.Fatal("nonmatching evidence was selected for joined failure")
			}
		})
	}
	got := pausedConcurrentFailureEvent([]engine.ReplayActionEvent{leaf}, action, group)
	if got == nil || got.EventID != leaf.EventID {
		t.Fatal("exact retained failure was omitted")
	}
	got.StepPath[0] = "changed"
	if leaf.StepPath[0] != "body" {
		t.Fatal("failure selection retained mutable source identity")
	}
	parent := leaf
	parent.RequestIndex = nil
	parent.ParentActionName = ""
	parent.ActionName = action.ActionName
	parent.ActionType = action.ActionType
	parent.StepPath = action.StepPath
	parent.CompletedAt = group.CompletedAt.Add(time.Millisecond)
	if pausedConcurrentFailureEvent([]engine.ReplayActionEvent{parent}, action, group) == nil {
		t.Fatal("synthesized group admission failure was omitted")
	}
}

// Older projections were safe only under an earlier classification. A later
// failed admission must invalidate every public cache and remain sticky across
// delayed callbacks, polling, lifecycle broadcasts and downloads.
func TestScriptDebugRuntimeEvidenceUnavailableInvalidatesAllPublicCaches(t *testing.T) {
	const sentinel = "synthetic-late-classified-value"
	hub := NewHub()
	t.Cleanup(hub.Stop)
	manager := NewScriptDebugManager(hub)
	session := &ScriptDebugSession{ID: "unavailable-cache", Status: ScriptDebugRunning, LogLevel: "trace", Group: sentinel, RuntimeProfile: engine.ReplayRuntimeProfile{Name: sentinel}}
	manager.sessions[session.ID] = session
	action := &engine.ReplayDebugAction{EventID: "structural-event", Phase: "action", ActionIndex: 3, ActionName: sentinel, ActionType: "GET", Target: "https://example.test/" + sentinel, SourceStepID: sentinel, StepPath: []string{"requests[1]"}}
	event := &engine.ReplayActionEvent{EventID: "structural-event", Phase: "action", ActionIndex: 3, ActionName: sentinel, ActionType: "GET", Target: sentinel, Body: sentinel, Headers: map[string]string{"X-Ordinary": sentinel}, Extractions: map[string]string{"ordinary": sentinel}, Success: true, StatusCode: 200, DurationMs: 17, StepPath: []string{"requests[1]"}}
	condition := &engine.ConditionEvidence{Expr: sentinel, Left: sentinel, Right: sentinel, Result: true}
	send := func(kind string, unavailable bool) {
		manager.handleReplayProgress(context.Background(), session, engine.ReplayProgressEvent{Type: kind, Action: action, Event: event, Condition: condition, EventIndex: 4, RuntimeEvidenceUnavailable: unavailable})
	}
	send(engine.ReplayProgressActionStarted, false)
	send(engine.ReplayProgressEventRun, false)
	send(engine.ReplayProgressCondition, false)
	before, ok := manager.Get(session.ID)
	if !ok || len(before.Events) != 1 || len(before.Logs) == 0 || len(before.RawOutput) == 0 || len(before.ConditionEvidence) == 0 {
		t.Fatal("fixture did not populate each public cache")
	}
	for len(hub.broadcast) > 0 {
		<-hub.broadcast
	}
	send(engine.ReplayProgressCondition, true)
	if len(hub.broadcast) != 1 {
		t.Fatal("first unavailable condition failed to notify WebSocket clients")
	}
	send(engine.ReplayProgressActionStarted, false)
	send(engine.ReplayProgressEventRun, false)
	send(engine.ReplayProgressCondition, false)
	session.mu.Lock()
	session.appendLogLocked("error", "action", sentinel, sentinel, event)
	session.FinalReport = &engine.ReplayReport{Success: true, Group: sentinel, Error: sentinel, Events: []engine.ReplayActionEvent{*event}, FinalVariables: map[string]string{"ordinary": sentinel}}
	session.mu.Unlock()
	assertClean := func(label string, value any) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), sentinel) {
			t.Fatalf("%s retained invalidated runtime detail", label)
		}
	}
	got, ok := manager.Get(session.ID)
	if !ok || !got.RuntimeEvidenceUnavailable || len(got.Logs) != 0 || len(got.RawOutput) != 0 || len(got.ConditionEvidence) != 0 || got.FinalReport == nil || !got.FinalReport.RuntimeEvidenceUnavailable || len(got.FinalReport.FinalVariables) != 0 {
		t.Fatal("polling did not expose sticky unavailability and omit all derived caches")
	}
	if got.CurrentAction == nil || got.CurrentAction.ActionIndex != 3 || len(got.CurrentAction.StepPath) != 1 || len(got.Events) != 1 || !got.Events[0].Success || got.Events[0].StatusCode != 200 || got.Events[0].DurationMs != 17 {
		t.Fatal("omission lost structural boundary or historical numeric outcomes")
	}
	assertClean("polling", got)
	assertClean("terminal snapshot", session.terminalSnapshot())
	for _, format := range []string{"json", "text"} {
		data, _, ok := manager.Export(session.ID, format)
		if !ok || strings.Contains(string(data), sentinel) {
			t.Fatalf("%s export retained invalidated detail", format)
		}
	}
	manager.broadcast("script_debug_completed", session, 4, event, action, session.FinalReport)
	for len(hub.broadcast) > 0 {
		frame := <-hub.broadcast
		if strings.Contains(string(frame.payload), sentinel) || !strings.Contains(string(frame.payload), `"runtime_evidence_unavailable":true`) {
			t.Fatal("WebSocket delta/lifecycle failed sticky unavailable projection")
		}
	}
	session.mu.RLock()
	rejected := session.correlationEvidenceErr != ""
	session.mu.RUnlock()
	if !rejected {
		t.Fatal("private diagnosis remained advertised as complete")
	}
	// Projection must be detached from the engine callback graph.
	if action.ActionName != sentinel || event.Body != sentinel || condition.Expr != sentinel {
		t.Fatal("manager projection mutated the caller's graph")
	}
}

func TestScriptDebugUnavailableProgressNotifiesBeforeTerminalPublication(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		cancelled  bool
	}{
		{"complete before teardown", engine.ReplayProgressComplete, false},
		{"started admission", engine.ReplayProgressStarted, false},
		{"failed without event", engine.ReplayProgressActionFailed, false},
		{"cancelled drain", engine.ReplayProgressEventRun, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const detail = "synthetic-prior-classification-detail"
			hub := NewHub()
			t.Cleanup(hub.Stop)
			manager := NewScriptDebugManager(hub)
			session := &ScriptDebugSession{ID: "signal-before-terminal", Status: ScriptDebugRunning, Group: detail, LogLevel: "trace", RawOutput: []string{detail}, CurrentAction: &engine.ReplayDebugAction{Phase: "action", ActionName: detail}}
			manager.sessions[session.ID] = session
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			manager.handleReplayProgress(ctx, session, engine.ReplayProgressEvent{Type: tc.kind, RuntimeEvidenceUnavailable: true})
			if len(hub.broadcast) != 1 {
				t.Fatal("trusted unavailable admission did not immediately notify the client")
			}
			frame := <-hub.broadcast
			if strings.Contains(string(frame.payload), detail) || !strings.Contains(string(frame.payload), `"runtime_evidence_unavailable":true`) {
				t.Fatal("immediate notification retained earlier evidence")
			}
			got, ok := manager.Get(session.ID)
			if !ok || !got.RuntimeEvidenceUnavailable || got.Status != ScriptDebugRunning || got.FinalReport != nil || len(got.RawOutput) != 0 {
				t.Fatal("admission invented terminal execution or retained stale evidence")
			}
			// Ordinary cancelled callbacks remain fenced out.
			cancel()
			manager.handleReplayProgress(ctx, session, engine.ReplayProgressEvent{Type: engine.ReplayProgressEventRun, Event: &engine.ReplayActionEvent{ActionName: detail, Body: detail}})
			if len(hub.broadcast) != 0 || len(session.Events) != 0 {
				t.Fatal("normal cancelled callback was admitted")
			}
		})
	}
}

func TestUnavailableScriptDebugEventProjectionAllowsBoundedOmission(t *testing.T) {
	if omitUnavailableScriptDebugEvent(nil) != nil {
		t.Fatal("nil evidence invented an event")
	}
	event := &engine.ReplayActionEvent{EventID: "structural-event", Phase: "action", ActionIndex: 2, StepPath: []string{"requests[1]"}, Body: "private-detail", Success: true, StatusCode: 200, DurationMs: 5}
	got := omitUnavailableScriptDebugEvent(event)
	if got == nil || !got.Success || got.StatusCode != 200 || got.DurationMs != 5 || got.EventID != event.EventID || got.Body != "" {
		t.Fatal("valid omission lost historical outcome or retained detail")
	}
	got.StepPath[0] = "changed"
	if event.StepPath[0] != "requests[1]" || event.Body != "private-detail" {
		t.Fatal("projection retained a mutable source graph")
	}
	event.DurationMs = math.NaN()
	if omitUnavailableScriptDebugEvent(event) != nil {
		t.Fatal("unencodable evidence was retained")
	}
}
