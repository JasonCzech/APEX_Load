package engine

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBoundedErrorSnapshotPreservesClosedResponseBodyStates(t *testing.T) {
	for _, state := range []string{"captured", "partial", "empty", "redacted", "not_captured", "expired", "unknown-state"} {
		t.Run(state, func(t *testing.T) {
			bounded, size := boundedErrorSnapshot(&protocol.ErrorSnapshot{ResponseBodyState: state, ResponseBody: "fixture"}, 1024)
			want := state
			if state == "unknown-state" {
				want = ""
			}
			if bounded == nil || bounded.ResponseBodyState != want || size > 1024 {
				t.Fatal("bounded snapshot changed admitted response state or escaped its byte limit")
			}
		})
	}
}

type responseContentReplayObserver struct {
	incompleteCorrelation bool
}

func (*responseContentReplayObserver) BeforeAction(context.Context, ReplayDebugAction) error {
	return nil
}
func (o *responseContentReplayObserver) ObserveCorrelationEvidence(_ context.Context, action ReplayDebugAction, event *ReplayActionEvent) {
	if action.ActionName == "Collect compressed events" && event != nil && event.CorrelationEvidence != nil {
		o.incompleteCorrelation = event.CorrelationEvidence.UnavailableReason != "" && event.CorrelationEvidence.ResponseBody == ""
	}
}

func TestReplayCompressedSSEPreservesPartialSnapshotThroughRetentionAndJSON(t *testing.T) {
	var streams, accepted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			streams.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Content-Encoding", "gzip")
			writer := gzip.NewWriter(w)
			if _, err := io.WriteString(writer, "event: value\ndata: sample-42\n\nevent: done\ndata: finished\n\n"); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			// No gzip trailer or HTTP EOF: authored collection must finish before
			// the response does, and close its live source without claiming EOF.
			<-r.Context().Done()
		case "/consume":
			if r.URL.Query().Get("value") != "sample-42" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			accepted.Add(1)
			_, _ = io.WriteString(w, "accepted")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	script := &dsl.Script{Name: "Partial response retention", Version: "1", Protocol: "http", Config: dsl.ScriptConfig{
		BaseURL: server.URL,
		Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 1, MaxItems: 4, MaxItemBytes: 64 << 10, MaxTotalBytes: 128 << 10}},
	}, Actions: []dsl.Action{
		{Name: "Collect compressed events", Type: "GET", Target: "/events", Headers: map[string]string{"Accept-Encoding": "gzip"}, Params: map[string]any{"stream": true, "stop_on_event": "done", "stream_timeout_ms": 5000}, Extractors: []dsl.Extractor{{Name: "sampled_value", Source: "body", Type: "sse_event", Expression: "value", OnMiss: "fail"}}, Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
		{Name: "Consume sampled value", Type: "GET", Target: "/consume?value={{var.sampled_value}}", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
	}}
	observer := &responseContentReplayObserver{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, err := ReplayScript(ctx, script, ReplayOptions{IgnoreThinkTime: true, DebugController: observer})
	if err != nil || report == nil || !report.Success || report.RuntimeEvidenceUnavailable || len(report.Events) != 2 {
		t.Fatalf("sampled replay failed or lost public admission: error=%v", err)
	}
	if streams.Load() != 1 || accepted.Load() != 1 || !observer.incompleteCorrelation {
		t.Fatal("sampled response changed traffic, lost extraction, or became complete correlation input")
	}
	event := report.Events[0]
	if event.Snapshot == nil || event.ResponseBodyState != "partial" || event.Snapshot.ResponseBodyState != "partial" || !strings.Contains(event.Snapshot.ResponseBody, "event: done") {
		t.Fatal("partial snapshot was lost through result bounds or replay retention")
	}
	if event.ProtocolEvidence["response_content_state"] != "collection_window" || event.ProtocolEvidence["response_content_basis"] != "decoded" || event.ProtocolEvidence["response_content_source"] != "wire" {
		t.Fatal("collection receipt was lost or became a complete response claim")
	}
	if len(report.Snapshots) != 2 || report.Snapshots[0].ResponseBodyState != "partial" {
		t.Fatal("report-level snapshot projection lost partial qualification")
	}
	exported, err := json.Marshal(struct {
		Report *ReplayReport `json:"report"`
	}{Report: report})
	if err != nil {
		t.Fatal(err)
	}
	var reopened struct {
		Report ReplayReport `json:"report"`
	}
	if err := json.Unmarshal(exported, &reopened); err != nil {
		t.Fatal(err)
	}
	retained := reopened.Report.Events[0]
	if retained.Snapshot == nil || retained.ResponseBodyState != "partial" || retained.Snapshot.ResponseBodyState != "partial" || !strings.Contains(retained.Snapshot.ResponseBody, "event: done") {
		t.Fatal("JSON export omitted partial-response qualification")
	}
}
