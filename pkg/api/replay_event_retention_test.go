package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestReplaySessionEventRetentionBoundsRecentWindow(t *testing.T) {
	var events []engine.ReplayActionEvent
	var eventBytes []int
	retainedBytes := 0
	dropped := 0
	for index := 0; index < engine.MaxRetainedReplayEvents+1; index++ {
		retainReplaySessionEvent(&events, &eventBytes, &retainedBytes, &dropped, engine.ReplayActionEvent{
			EventID:    "event",
			EventIndex: index,
			ActionName: "action",
			ActionType: "request",
			Target:     "/target",
			Success:    true,
		})
	}
	if len(events) != engine.MaxRetainedReplayEvents || dropped != 1 {
		t.Fatalf("retained/dropped = %d/%d, want %d/1", len(events), dropped, engine.MaxRetainedReplayEvents)
	}
	if events[0].EventIndex != 1 || events[len(events)-1].EventIndex != engine.MaxRetainedReplayEvents {
		t.Fatalf("retained event index window = %d..%d", events[0].EventIndex, events[len(events)-1].EventIndex)
	}
	if retainedBytes > engine.MaxRetainedReplayEventBytes {
		t.Fatalf("retained event bytes = %d, limit %d", retainedBytes, engine.MaxRetainedReplayEventBytes)
	}
}

func TestReplayTerminalBroadcastCarriesOneAuthoritativeReport(t *testing.T) {
	report := &engine.ReplayReport{
		ReplaySessionID: "terminal",
		Group:           "replay",
		Protocol:        "browser",
		Success:         true,
		Events: []engine.ReplayActionEvent{{
			EventID:    "event-1",
			EventIndex: 0,
			ActionName: "action",
			Success:    true,
		}},
	}

	t.Run("browser", func(t *testing.T) {
		hub := NewHub()
		manager := NewBrowserReplayManager(hub)
		session := &BrowserReplaySession{
			ID:           "terminal",
			Status:       BrowserReplayCompleted,
			Events:       append([]engine.ReplayActionEvent(nil), report.Events...),
			CurrentEvent: &report.Events[0],
			FinalReport:  report,
		}
		manager.broadcast("browser_replay_completed", session, 0, nil, report)
		assertSingleTerminalReport(t, <-hub.broadcast)
	})

	t.Run("script debug", func(t *testing.T) {
		hub := NewHub()
		manager := NewScriptDebugManager(hub)
		session := &ScriptDebugSession{
			ID:           "terminal",
			Status:       ScriptDebugCompleted,
			LogLevel:     "info",
			Events:       append([]engine.ReplayActionEvent(nil), report.Events...),
			CurrentEvent: &report.Events[0],
			FinalReport:  report,
		}
		manager.broadcast("script_debug_completed", session, 0, nil, nil, report)
		assertSingleTerminalReport(t, <-hub.broadcast)
	})
}

func assertSingleTerminalReport(t *testing.T, broadcast hubBroadcast) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(broadcast.payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope["report"]) == 0 {
		t.Fatalf("terminal envelope has no top-level report: %s", broadcast.payload)
	}
	var session map[string]json.RawMessage
	if err := json.Unmarshal(envelope["session"], &session); err != nil {
		t.Fatalf("decode terminal session summary: %v", err)
	}
	for _, duplicate := range []string{"report", "events", "current_event"} {
		if _, exists := session[duplicate]; exists {
			t.Fatalf("terminal session summary duplicated %q payload: %s", duplicate, broadcast.payload)
		}
	}
}

func TestDebugRawEventRenderingRequiresDebugLevel(t *testing.T) {
	event := engine.ReplayActionEvent{
		Method:     "POST",
		Target:     "/payload",
		Body:       strings.Repeat("sensitive-body", 1024),
		StatusCode: 200,
		StartedAt:  time.Now(),
	}
	if raw := formatDebugEventRawForLogLevel("info", event); raw != "" {
		t.Fatalf("info raw output retained body evidence: %q", raw)
	}
	if raw := formatDebugEventRawForLogLevel("debug", event); !strings.Contains(raw, event.Body) {
		t.Fatalf("debug raw output = %q, want body evidence", raw)
	}
}
