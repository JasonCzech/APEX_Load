package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/recordbridge"
)

func TestDecodeRetainedReplayReport(t *testing.T) {
	now := time.Now().UTC()
	session := &recordbridge.BrowserReplaySession{ID: "replay", ProjectID: "project", Status: recordbridge.BrowserReplayCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{ReplaySessionID: "replay", Success: true, NativeHARCaptureOmissions: map[string]int64{"native_capture_failed": 2}, Events: []engine.ReplayActionEvent{{ProtocolEvidence: map[string]any{"sequence": json.Number("9007199254740993")}}}}}
	report, err := json.Marshal(struct {
		Session *recordbridge.BrowserReplaySession `json:"session"`
	}{session})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRetainedReplayReport(report, "replay", "project")
	if err != nil {
		t.Fatal(err)
	}
	value := got.FinalReport.Events[0].ProtocolEvidence["sequence"]
	if value != json.Number("9007199254740993") {
		t.Fatalf("lost integer precision: %#v", value)
	}
	encoded, err := json.Marshal(struct {
		Session *recordbridge.BrowserReplaySession `json:"session"`
	}{got})
	if err != nil || !bytes.Equal(encoded, report) {
		t.Fatalf("evidence changed on recovery: %s %v", encoded, err)
	}
	if !got.WorkerSettled() || got.ArtifactDurability() != "durable" {
		t.Fatal("not settled durable evidence")
	}
	for _, tc := range []struct{ name, raw, id, project string }{
		{"empty", "", "replay", "project"},
		{"null", "null", "replay", "project"},
		{"missing", "{}", "replay", "project"},
		{"unknown-envelope", `{"session":null,"future":true}`, "replay", "project"},
		{"unknown-session", strings.Replace(string(report), `"id":"replay"`, `"id":"replay","future":true`, 1), "replay", "project"},
		{"unknown-report", strings.Replace(string(report), `"report":{`, `"report":{"future":true,`, 1), "replay", "project"},
		{"trailing", string(report) + ` {}`, "replay", "project"},
		{"trailing-null", string(report) + ` null`, "replay", "project"},
		{"malformed", string(report) + `!`, "replay", "project"},
		{"wrong-id", string(report), "another", "project"},
		{"wrong-project", string(report), "replay", "another"},
		{"unscoped", string(report), "replay", ""},
		{"oversize", strings.Repeat(" ", 16<<20+1), "replay", "project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeRetainedReplayReport([]byte(tc.raw), tc.id, tc.project)
			if got != nil || !errors.Is(err, recordbridge.ErrRetainedReplayUnavailable) {
				t.Fatalf("accepted invalid report: %+v %v", got, err)
			}
		})
	}
}
