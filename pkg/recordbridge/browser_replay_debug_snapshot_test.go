package recordbridge

import (
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func debugSnapshotFixture() engine.ReplayActionEvent {
	return engine.ReplayActionEvent{
		UnappliedExtractions:             map[string]string{"token": "raw-unapplied"},
		UnappliedExtractionsOmittedCount: 2,
		ProtocolEvidence: map[string]any{
			"large":  strings.Repeat("raw", 20000),
			"nested": []any{map[string]any{"secret": "raw-evidence"}},
		},
		ProtocolEvidenceOmittedCount: 3,
		StreamedChildren: []engine.ReplayStreamedChildEvidence{{
			Extractions: map[string]string{"id": "raw-child"}, UnappliedExtractions: map[string]string{"token": "raw-child-unapplied"},
			ExtractionsOmittedCount: 4, UnappliedExtractionsOmittedCount: 5,
		}},
		StreamedChildrenOmittedCount: 6,
		Snapshot:                     &protocol.ErrorSnapshot{Resources: []protocol.ResourceSnapshot{{RequestHeaders: map[string]string{"Authorization": "raw-resource"}}}},
	}
}

func assertDebugSnapshot(t *testing.T, event engine.ReplayActionEvent) {
	t.Helper()
	if event.UnappliedExtractions["token"] != "raw-unapplied" || event.UnappliedExtractionsOmittedCount != 2 ||
		event.ProtocolEvidence["large"] != strings.Repeat("raw", 20000) || event.ProtocolEvidenceOmittedCount != 3 ||
		event.ProtocolEvidence["nested"].([]any)[0].(map[string]any)["secret"] != "raw-evidence" ||
		len(event.StreamedChildren) != 1 || event.StreamedChildrenOmittedCount != 6 ||
		event.StreamedChildren[0].Extractions["id"] != "raw-child" || event.StreamedChildren[0].UnappliedExtractions["token"] != "raw-child-unapplied" ||
		event.StreamedChildren[0].ExtractionsOmittedCount != 4 || event.StreamedChildren[0].UnappliedExtractionsOmittedCount != 5 ||
		event.Snapshot == nil || event.Snapshot.Resources[0].RequestHeaders["Authorization"] != "raw-resource" {
		t.Fatalf("debug evidence changed during snapshot: %+v", event)
	}
}

func mutateDebugSnapshot(event *engine.ReplayActionEvent) {
	event.UnappliedExtractions["token"] = "changed"
	event.ProtocolEvidence["nested"].([]any)[0].(map[string]any)["secret"] = "changed"
	event.StreamedChildren[0].Extractions["id"] = "changed"
	event.StreamedChildren[0].UnappliedExtractions["token"] = "changed"
	event.Snapshot.Resources[0].RequestHeaders["Authorization"] = "changed"
}

func TestReplayDebugSnapshotsPreserveAndDetachEvidence(t *testing.T) {
	event := debugSnapshotFixture()
	cloned := CloneBrowserReplayEvent(event)
	assertDebugSnapshot(t, cloned)
	mutateDebugSnapshot(&cloned)
	assertDebugSnapshot(t, event)

	report := &engine.ReplayReport{Events: []engine.ReplayActionEvent{event}, Snapshots: []*protocol.ErrorSnapshot{event.Snapshot}}
	session := &BrowserReplaySession{CurrentEvent: &event, Events: []engine.ReplayActionEvent{event}, FinalReport: report}
	first := session.snapshot()
	assertDebugSnapshot(t, *first.CurrentEvent)
	assertDebugSnapshot(t, first.Events[0])
	assertDebugSnapshot(t, first.FinalReport.Events[0])
	if first.FinalReport.Events[0].Snapshot != first.FinalReport.Snapshots[0] {
		t.Fatal("report lost its shared snapshot identity")
	}
	mutateDebugSnapshot(first.CurrentEvent)
	mutateDebugSnapshot(&first.Events[0])
	mutateDebugSnapshot(&first.FinalReport.Events[0])
	again := session.snapshot()
	assertDebugSnapshot(t, *again.CurrentEvent)
	assertDebugSnapshot(t, again.Events[0])
	assertDebugSnapshot(t, again.FinalReport.Events[0])
}

func TestReplayDebugSnapshotsConcurrentMutationIsIsolated(t *testing.T) {
	event := debugSnapshotFixture()
	session := &BrowserReplaySession{CurrentEvent: &event, Events: []engine.ReplayActionEvent{event}, FinalReport: &engine.ReplayReport{Events: []engine.ReplayActionEvent{event}, Snapshots: []*protocol.ErrorSnapshot{event.Snapshot}}}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 20; j++ {
				snapshot := session.snapshot()
				mutateDebugSnapshot(snapshot.CurrentEvent)
				mutateDebugSnapshot(&snapshot.Events[0])
				mutateDebugSnapshot(&snapshot.FinalReport.Events[0])
			}
		}()
	}
	workers.Wait()
	assertDebugSnapshot(t, event)
}

func TestReplayDebugSnapshotPreservesOmissionReceiptAcrossCopies(t *testing.T) {
	event := engine.ReplayActionEvent{
		ProtocolEvidence:             map[string]any{"value": strings.Repeat("x", 300<<10)},
		ProtocolEvidenceOmittedCount: 7,
	}
	first := CloneBrowserReplayEvent(event)
	value, ok := first.ProtocolEvidence["value"].(string)
	if !ok || len(value) == 0 || len(value) >= len(event.ProtocolEvidence["value"].(string)) || first.ProtocolEvidenceOmittedCount != 8 {
		t.Fatal("oversized debug evidence was not bounded with an omission receipt")
	}
	second := CloneBrowserReplayEvent(first)
	if second.ProtocolEvidence["value"] != value || second.ProtocolEvidenceOmittedCount != first.ProtocolEvidenceOmittedCount {
		t.Fatal("copying bounded debug evidence altered it or added another omission")
	}
}

func TestReplayClientMetricsCloneDetachesAvailability(t *testing.T) {
	metrics := &protocol.ClientMetrics{RuntimeAvailability: &protocol.BrowserRuntimeMetricAvailability{
		Version: 1,
		Metrics: []protocol.BrowserRuntimeMetricCapability{{Metric: "js_heap_used_mb", Source: "cdp", Scope: "active_page", Available: true}},
	}}
	copy := CloneBrowserClientMetrics(metrics)
	if copy.RuntimeAvailability == nil || len(copy.RuntimeAvailability.Metrics) != 1 {
		t.Fatal("valid runtime availability was lost")
	}
	copy.RuntimeAvailability.Metrics[0].Source = "changed"
	if metrics.RuntimeAvailability.Metrics[0].Source != "cdp" {
		t.Fatal("runtime availability aliases the source")
	}
}
