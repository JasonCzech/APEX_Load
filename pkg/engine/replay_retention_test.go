package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplayReportRetentionSealsRichEvidenceBudget(t *testing.T) {
	report := &ReplayReport{consumedEvidenceBytes: MaxRetainedReplayEventBytes - 1}
	first := appendReplayReportEvent(report, ReplayActionEvent{
		EventID:      "first",
		ActionName:   "first action",
		ActionType:   "request",
		Target:       "/first",
		Body:         strings.Repeat("a", 1024),
		Success:      true,
		StatusCode:   200,
		ErrorMessage: "",
	})
	if first.Body != "" || report.TruncatedEventEvidenceCount != 1 {
		t.Fatalf("first retained event/body = %#v, truncated=%d; want scalar-only event", first, report.TruncatedEventEvidenceCount)
	}
	if report.evidenceProjectionCount != 1 || report.consumedEvidenceBytes != MaxRetainedReplayEventBytes {
		t.Fatalf("projection count/budget = %d/%d, want 1/%d", report.evidenceProjectionCount, report.consumedEvidenceBytes, MaxRetainedReplayEventBytes)
	}

	second := appendReplayReportEvent(report, ReplayActionEvent{
		EventID:    "second",
		ActionName: "second action",
		ActionType: "request",
		Target:     "/second",
		Body:       strings.Repeat("b", 4<<20),
		Success:    false,
		StatusCode: 503,
	})
	if report.evidenceProjectionCount != 1 {
		t.Fatalf("full rich projections = %d, want no projection after budget seal", report.evidenceProjectionCount)
	}
	if second.Body != "" || second.EventIndex != 1 || second.ActionName != "second action" || second.StatusCode != 503 || second.Success {
		t.Fatalf("second compact event = %#v, want preserved scalar identity/status and no body", second)
	}
	if report.retainedEventJSONBytes > MaxRetainedReplayEventBytes {
		t.Fatalf("retained event JSON bytes = %d, limit %d", report.retainedEventJSONBytes, MaxRetainedReplayEventBytes)
	}
}

func TestReplayReportRetentionCapsEntriesAndPreservesAggregateManifest(t *testing.T) {
	report := &ReplayReport{ArtifactManifest: &ReplayArtifactManifest{}}
	sharedSnapshot := &protocol.ErrorSnapshot{
		HTML:             "<html>failure</html>",
		ScreenshotBase64: "image",
		ConsoleEntries:   []protocol.ConsoleEntry{{Level: "error", Text: "snapshot console"}},
		NetworkEntries:   []protocol.NetworkEntry{{Method: "GET", URL: "/snapshot"}},
	}
	for index := 0; index < MaxRetainedReplayEvents+1; index++ {
		event := ReplayActionEvent{
			EventID:    "event",
			ActionName: "action",
			ActionType: "request",
			Target:     "/target",
			Success:    true,
		}
		if index == 0 || index == 1 {
			event.Snapshot = sharedSnapshot
			event.ProtocolEvidence = map[string]any{"protocol": "database"}
		}
		if index == 0 {
			event.Success = false
			event.ErrorMessage = "failed"
			event.Console = []protocol.ConsoleEntry{{Level: "error", Text: "event console"}}
			event.Network = []protocol.NetworkEntry{{Method: "GET", URL: "/event"}}
		}
		appendReplayReportEvent(report, event)
	}
	setReplayReportSnapshots(report)

	if len(report.Events) != MaxRetainedReplayEvents || report.DroppedEventCount != 1 {
		t.Fatalf("retained/dropped events = %d/%d, want %d/1", len(report.Events), report.DroppedEventCount, MaxRetainedReplayEvents)
	}
	if report.Events[0].EventIndex != 1 || report.Events[len(report.Events)-1].EventIndex != MaxRetainedReplayEvents {
		t.Fatalf("retained event index window = %d..%d", report.Events[0].EventIndex, report.Events[len(report.Events)-1].EventIndex)
	}
	if report.retainedEventJSONBytes > MaxRetainedReplayEventBytes {
		t.Fatalf("retained event JSON bytes = %d, limit %d", report.retainedEventJSONBytes, MaxRetainedReplayEventBytes)
	}
	eventsJSON, err := json.Marshal(report.Events)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsJSON) > MaxRetainedReplayEventBytes+MaxRetainedReplayEvents+2 {
		t.Fatalf("events JSON bytes = %d, want bounded event payload", len(eventsJSON))
	}

	manifest := report.ArtifactManifest
	if manifest.EventCount != MaxRetainedReplayEvents+1 || manifest.FailureCount != 1 {
		t.Fatalf("manifest event/failure counts = %d/%d", manifest.EventCount, manifest.FailureCount)
	}
	if manifest.Snapshots != 1 || len(report.Snapshots) != 1 {
		t.Fatalf("manifest/retained snapshot counts = %d/%d, want unique shared snapshot", manifest.Snapshots, len(report.Snapshots))
	}
	if manifest.NetworkEntries != 3 || manifest.ConsoleEntries != 3 {
		t.Fatalf("manifest network/console counts = %d/%d, want event plus repeated snapshot evidence", manifest.NetworkEntries, manifest.ConsoleEntries)
	}
	if manifest.Screenshots != 2 || !manifest.HTMLCaptured || manifest.ProtocolArtifacts["database"] != 2 {
		t.Fatalf("manifest evidence aggregates = %#v", manifest)
	}
	if report.DiagnosisSummary == nil || report.DiagnosisSummary.FailureCount != 1 || report.DiagnosisSummary.FirstFailureEventID != "event" {
		t.Fatalf("diagnosis summary lost dropped failure: %#v", report.DiagnosisSummary)
	}
}

func TestReplayReportRetentionSharesRepeatedSnapshot(t *testing.T) {
	shared := &protocol.ErrorSnapshot{ResponseBody: "failure evidence"}
	report := &ReplayReport{ArtifactManifest: &ReplayArtifactManifest{}}
	appendReplayReportEvent(report, ReplayActionEvent{ActionName: "first", Snapshot: shared, Success: false})
	appendReplayReportEvent(report, ReplayActionEvent{ActionName: "second", Snapshot: shared, Success: false})
	setReplayReportSnapshots(report)
	if len(report.Snapshots) != 1 || report.Events[0].Snapshot != report.Events[1].Snapshot || report.Events[0].Snapshot != report.Snapshots[0] {
		t.Fatalf("repeated snapshot was not pointer-shared: events=%#v snapshots=%#v", report.Events, report.Snapshots)
	}
	if report.ArtifactManifest.Snapshots != 1 {
		t.Fatalf("manifest snapshots = %d, want one unique snapshot", report.ArtifactManifest.Snapshots)
	}
	if report.manifestSnapshots != nil || report.retainedSnapshotClones != nil {
		t.Fatal("terminal report retained build-time snapshot accounting maps")
	}
}
