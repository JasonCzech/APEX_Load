package recordbridge

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/engine"
	"testing"
)

func TestReplayTerminalBroadcastCarriesOneAuthoritativeReport(t *testing.T) {
	report := &engine.ReplayReport{
		ReplaySessionID: "terminal", Group: "replay", Protocol: "browser", Success: true,
		Events: []engine.ReplayActionEvent{{EventID: "event-1", EventIndex: 0, ActionName: "action", Success: true}},
	}
	t.Run("browser", func(t *testing.T) {
		hub := newTestEventSink()
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

}

func assertSingleTerminalReport(t *testing.T, broadcast testEvent) {
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
