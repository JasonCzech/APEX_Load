package engine

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
)

func TestServerFailureDoesNotSuggestWeakeningAssertion(t *testing.T) {
	event := ReplayActionEvent{StatusCode: 503, ActionType: "request", Method: "GET", ErrorMessage: "assertion failed: expected 200, got 503"}
	attachReplayDiagnosis(&event)
	if event.Diagnosis.Category != "service_unavailable" {
		t.Fatal(event.Diagnosis)
	}
	for _, suggestion := range event.RepairSuggestions {
		if suggestion.DraftPatchType == "adjust_assertion" {
			t.Fatal(suggestion)
		}
	}
	if !strings.Contains(replayDiagnosisAction("service_unavailable"), "target availability") {
		t.Fatal(event.Diagnosis)
	}
}

func TestReplayRetentionExplainsRemovedResponseEvidence(t *testing.T) {
	event := ReplayActionEvent{ResponseBodyState: "captured", Snapshot: &protocol.ErrorSnapshot{ResponseBody: "fixture", ResponseBodyState: "captured"}}
	stripReplayEventRichEvidence(&event)
	if event.Snapshot != nil || event.ResponseBodyState != "not_retained" {
		t.Fatalf("retention state = %+v", event)
	}
}
