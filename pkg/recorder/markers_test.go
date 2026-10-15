package recorder

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recorder/marker"
)

// A tester capturing a device through the proxy names each step while
// performing it; the converted script times those steps as transactions.
func TestProxyRecordingMarkersTimeConvertedTransactions(t *testing.T) {
	rec := New(":0")
	var events []string
	session, err := rec.StartSession("tx", "Mobile sign-in", SessionFilters{}, func(event SessionEvent) {
		events = append(events, event.Type)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rec.AddMarker("missing", marker.Marker{Kind: marker.KindNote, Text: "x"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("marker on an unknown session: %v", err)
	}

	base := time.Now().Add(-time.Minute)
	at := func(seconds float64) time.Time { return base.Add(time.Duration(seconds * float64(time.Second))) }
	record := func(method, path string, startedAt time.Time) {
		t.Helper()
		req, _ := http.NewRequest(method, "https://login.example.com"+path, nil)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}}
		rec.recordRequestStarted(session, req, nil, resp, []byte(`{"ok":true}`), 10*time.Millisecond, startedAt)
	}
	mark := func(m marker.Marker) marker.Marker {
		t.Helper()
		_, added, err := rec.AddMarker("tx", m)
		if err != nil {
			t.Fatalf("AddMarker(%#v) = %v", m, err)
		}
		return added
	}

	record(http.MethodGet, "/api/config", at(0))
	mark(marker.Marker{Kind: marker.KindTransactionStart, Name: "Login", Timestamp: at(1)})
	record(http.MethodPost, "/api/authorize", at(2))
	mark(marker.Marker{Kind: marker.KindNote, Text: "approve on the device", Timestamp: at(2.5)})
	record(http.MethodPost, "/api/token", at(3))
	if ended := mark(marker.Marker{Kind: marker.KindTransactionEnd, Timestamp: at(4)}); ended.Name != "Login" {
		t.Fatalf("unnamed end closed %q, want Login", ended.Name)
	}
	if _, err := rec.PauseSession("tx"); err != nil {
		t.Fatal(err)
	}
	// A paused recording still takes markers, as the browser recorder's does.
	mark(marker.Marker{Kind: marker.KindTransactionStart, Name: "Search", Timestamp: at(5)})
	if _, err := rec.ResumeSession("tx"); err != nil {
		t.Fatal(err)
	}
	record(http.MethodGet, "/api/search", at(6))

	stopped, err := rec.StopSession("tx")
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.OpenTransactions) != 0 || !slices.ContainsFunc(stopped.Warnings, func(w string) bool {
		return strings.Contains(w, `"Search"`) && strings.Contains(w, "closed automatically")
	}) {
		t.Fatalf("stop left open=%v warnings=%v, want Search closed with a warning", stopped.OpenTransactions, stopped.Warnings)
	}
	if _, _, err := rec.AddMarker("tx", marker.Marker{Kind: marker.KindNote, Text: "late"}); !errors.Is(err, ErrSessionNotLive) {
		t.Fatalf("marker on a stopped session: %v", err)
	}
	if !slices.Contains(events, "recording_marker") {
		t.Fatalf("events = %v, want a recording_marker event", events)
	}

	snapshot, _ := rec.GetSession("tx")
	if len(snapshot.Markers) != 5 || !snapshot.Markers[4].Auto {
		t.Fatalf("markers = %#v, want four from the tester and the auto end", snapshot.Markers)
	}
	result := ConvertSession(snapshot, "")
	var shape []string
	for _, action := range result.Script.Actions {
		label := action.Type + ":" + action.Target
		if action.Type == "source_transaction" {
			label = action.Name
		}
		if name, _ := action.Metadata["source_transaction"].(string); name != "" {
			label += " [" + name + "]"
		}
		shape = append(shape, label)
	}
	want := []string{
		"request:/api/config",
		"Start Login transaction",
		"request:/api/authorize [Login]",
		"request:/api/token [Login]",
		"End Login transaction",
		"Start Search transaction",
		"request:/api/search [Search]",
		"End Search transaction",
	}
	if !slices.Equal(shape, want) {
		t.Fatalf("converted shape:\n%s\nwant:\n%s\nwarnings: %v", strings.Join(shape, "\n"), strings.Join(want, "\n"), result.Warnings)
	}
	if start := result.Script.Actions[1]; dsl.TransactionMarkerName(start) != "Login" {
		t.Fatalf("start marker = %#v", start)
	}
	// The note attaches to the request after it, as in the browser script.
	for index, action := range result.Script.Actions {
		if note, _ := action.Metadata["note"].(string); (index == 3) != (note == "approve on the device") {
			t.Fatalf("action %d (%s) note = %q; want the note on the token request only", index, action.Target, note)
		}
	}
}

func TestWebSocketOnlyRecordingReportsDroppedTransactions(t *testing.T) {
	session := &Session{
		ID:      "ws",
		Name:    "Socket",
		BaseURL: "wss://socket.example.com",
		Requests: []RecordedRequest{{
			Seq: 1, Method: http.MethodGet, URL: "wss://socket.example.com/feed", Path: "/feed", StatusCode: http.StatusSwitchingProtocols,
			WebSocketMessages: []RecordedWebSocketMessage{{Direction: webSocketDirectionClientToServer, Opcode: webSocketOpcodeText, Payload: "hello"}},
		}},
		Markers: []marker.Marker{
			{Kind: marker.KindTransactionStart, Name: "Feed", Timestamp: time.Now()},
			{Kind: marker.KindTransactionEnd, Name: "Feed", Timestamp: time.Now()},
		},
	}
	result := ConvertSession(session, "")
	if result.Script == nil || result.Script.Protocol != "websocket" {
		t.Fatalf("script = %#v, want a WebSocket script", result.Script)
	}
	if !slices.ContainsFunc(result.Warnings, func(w string) bool { return strings.Contains(w, "carries neither") }) {
		t.Fatalf("warnings = %v, want the dropped transactions reported", result.Warnings)
	}
}

func TestStopNamesAtMostTwentyAutoClosedTransactions(t *testing.T) {
	rec := New(":0")
	if _, err := rec.StartSession("many", "Many", SessionFilters{}, nil); err != nil {
		t.Fatal(err)
	}
	for index := range maxStopMarkerWarnings + 5 {
		if _, _, err := rec.AddMarker("many", marker.Marker{Kind: marker.KindTransactionStart, Name: fmt.Sprintf("T%d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	stopped, err := rec.StopSession("many")
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.Warnings) != maxStopMarkerWarnings+1 || !strings.HasPrefix(stopped.Warnings[maxStopMarkerWarnings], "5 more transaction(s)") {
		t.Fatalf("warnings = %d, last %q; want %d named and one counting the rest", len(stopped.Warnings), stopped.Warnings[len(stopped.Warnings)-1], maxStopMarkerWarnings)
	}
	if len(stopped.OpenTransactions) != 0 || len(stopped.Markers) != 2*(maxStopMarkerWarnings+5) {
		t.Fatalf("open=%v markers=%d; every transaction must still be ended", stopped.OpenTransactions, len(stopped.Markers))
	}
}
