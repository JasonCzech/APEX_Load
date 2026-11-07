package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

// Each per-event result line carries the replay event's event_index so
// clients can attribute log lines to one event by identity. Started/Ready,
// control and session lines carry none.
func TestScriptDebugResultLogLinesCarryEventIndex(t *testing.T) {
	hub := NewHub()
	t.Cleanup(hub.Stop)
	manager := NewScriptDebugManager(hub)
	session := &ScriptDebugSession{ID: "log-event-index", Status: ScriptDebugRunning, LogLevel: "debug"}
	manager.sessions[session.ID] = session
	ctx := context.Background()
	action := &engine.ReplayDebugAction{Phase: "action", ActionIndex: 1, ActionName: "Login", ActionType: "GET"}
	pass := func(eventIndex int, success bool) {
		manager.handleReplayProgress(ctx, session, engine.ReplayProgressEvent{Type: engine.ReplayProgressActionStarted, Action: action})
		event := &engine.ReplayActionEvent{EventIndex: eventIndex, Phase: "action", ActionIndex: 1, ActionName: "Login", ActionType: "GET", Success: success, StatusCode: 200}
		if !success {
			event.StatusCode = 500
		}
		manager.handleReplayProgress(ctx, session, engine.ReplayProgressEvent{Type: engine.ReplayProgressEventRun, EventIndex: eventIndex, Event: event})
	}
	pass(0, true)
	pass(1, false)
	async := &engine.ReplayActionEvent{EventIndex: 2, Phase: "async", ActionIndex: -1, ActionName: "poll", ActionType: "GET", Success: true, StatusCode: 200}
	manager.handleReplayProgress(ctx, session, engine.ReplayProgressEvent{Type: engine.ReplayProgressEventRun, EventIndex: 2, Event: async})
	session.mu.Lock()
	session.appendLogLocked("warn", "control", "Stop requested", "", nil)
	session.mu.Unlock()

	got, ok := manager.Get(session.ID)
	if !ok {
		t.Fatal("session missing")
	}
	var stamped []int
	for _, entry := range got.Logs {
		isResult := entry.Category == "action" && !strings.HasPrefix(entry.Message, "Started ") && !strings.HasPrefix(entry.Message, "Ready to execute ")
		if isResult {
			if entry.EventIndex == nil {
				t.Fatalf("result line %q has no event_index", entry.Message)
			}
			stamped = append(stamped, *entry.EventIndex)
			continue
		}
		if entry.EventIndex != nil {
			t.Fatalf("non-result line %q carries event_index %d", entry.Message, *entry.EventIndex)
		}
	}
	if len(stamped) != 3 || stamped[0] != 0 || stamped[1] != 1 || stamped[2] != 2 {
		t.Fatalf("result lines stamped %v, want [0 1 2]", stamped)
	}

	// event_index 0 must serialize (omitempty applies to the pointer only).
	var first ScriptDebugLogEntry
	for _, entry := range got.Logs {
		if entry.EventIndex != nil && *entry.EventIndex == 0 {
			first = entry
		}
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"event_index":0`) {
		t.Fatalf("event_index 0 was omitted: %s", encoded)
	}

	// Snapshots must not alias the session's stamped values.
	for i := range got.Logs {
		if got.Logs[i].EventIndex != nil {
			*got.Logs[i].EventIndex = 99
		}
	}
	again, _ := manager.Get(session.ID)
	for _, entry := range again.Logs {
		if entry.EventIndex != nil && *entry.EventIndex == 99 {
			t.Fatal("snapshot log entries alias the session's event_index")
		}
	}
}
