package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestOmitUnavailableReplayReportRemovesPriorPrefixesAndKeepsOutcomes(t *testing.T) {
	prefix := strings.Repeat("z", 512)
	started := time.Now().Add(-time.Second)
	request, iteration := 1, 2
	snapshot := &protocol.ErrorSnapshot{ResponseBody: prefix, HTML: prefix}
	report := &ReplayReport{
		ReplaySessionID: "synthetic-terminal", ScriptHash: "synthetic-hash", ScriptRevision: "3", Protocol: "http",
		Group: prefix, Success: true, StartedAt: started, CompletedAt: time.Now(), Error: prefix,
		RuntimeProfile:    ReplayRuntimeProfile{Name: prefix},
		FinalVariables:    map[string]string{"ordinary_alias": prefix},
		ArtifactManifest:  &ReplayArtifactManifest{SeedVariables: []string{prefix}},
		DiagnosisSummary:  &ReplayDiagnosisSummary{FirstFailureCause: prefix},
		RepairSuggestions: []ReplayRepairSuggestion{{Title: prefix}}, Snapshots: []*protocol.ErrorSnapshot{snapshot},
		Events: []ReplayActionEvent{{
			EventID: "synthetic-event", Phase: "action", ActionIndex: 3, EventIndex: 4, LeafOrdinal: 5,
			ActionName: prefix, ActionType: "POST", Method: "POST", ParentActionName: prefix,
			Target: prefix, Selector: prefix, Value: prefix, Body: prefix, Headers: map[string]string{"X-Echo": prefix},
			Extractions: map[string]string{"ordinary_alias": prefix}, ErrorMessage: prefix,
			Success: true, StatusCode: 200, StartedAt: started, CompletedAt: started.Add(250 * time.Millisecond), DurationMs: 250,
			SourceStepID: prefix, SourceActionName: prefix, SourceTransaction: prefix,
			PageURL: prefix, PageTitle: prefix, HTML: prefix, Screenshot: prefix,
			Console: []protocol.ConsoleEntry{{Text: prefix}}, Network: []protocol.NetworkEntry{{URL: prefix}},
			ProtocolEvidence: map[string]any{"ordinary": prefix}, Snapshot: snapshot,
			Diagnosis: &ReplayDiagnosis{Cause: prefix}, RepairSuggestions: []ReplayRepairSuggestion{{Fields: map[string]string{"value": prefix}}},
			RequestIndex: &request, RequestCount: 3, Iteration: &iteration, StepPath: []string{"actions[3]", "params.requests[1]"}, IterationPath: []int{2},
		}},
		manifestSnapshots:      map[*protocol.ErrorSnapshot]struct{}{snapshot: {}},
		retainedSnapshotClones: map[*protocol.ErrorSnapshot]*protocol.ErrorSnapshot{snapshot: snapshot},
		messageDetailBudget:    &replaySnapshotBudget{},
	}
	OmitUnavailableReplayReport(nil)
	OmitUnavailableReplayReport(report)
	first, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	OmitUnavailableReplayReport(report)
	second, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || strings.Contains(string(first), prefix) {
		t.Fatal("terminal omission was not idempotent or retained a prior unknown prefix")
	}
	if !report.RuntimeEvidenceUnavailable || report.Success || report.FinalVariables != nil || report.ArtifactManifest != nil || report.DiagnosisSummary != nil || report.RepairSuggestions != nil || report.Snapshots != nil || report.manifestSnapshots != nil || report.retainedSnapshotClones != nil || report.messageDetailBudget != nil {
		t.Fatal("terminal report retained runtime derivatives or lacked its unavailable flag")
	}
	event := report.Events[0]
	if !event.Success || event.StatusCode != 200 || event.DurationMs != 250 || event.EventID != "synthetic-event" || event.ActionIndex != 3 || event.EventIndex != 4 || event.LeafOrdinal != 5 || event.RequestIndex == nil || *event.RequestIndex != 1 || event.RequestCount != 3 || len(event.StepPath) != 2 || len(event.IterationPath) != 1 || event.StartedAt != started {
		t.Fatal("terminal omission changed historical success, status, timings or structural identity")
	}
	if report.retainedEventJSONBytes > MaxRetainedReplayEventBytes || len(report.eventJSONBytes) != 1 {
		t.Fatal("terminal report omitted retention accounting")
	}
}

func TestUnavailableReplayProjectionBoundsEntriesAndDebugIdentity(t *testing.T) {
	report := &ReplayReport{Events: make([]ReplayActionEvent, MaxRetainedReplayEvents+2)}
	for i := range report.Events {
		report.Events[i] = ReplayActionEvent{EventIndex: i, ActionType: "GET", Success: i%2 == 0, StatusCode: 200, StepPath: make([]string, maxReplayPathDepth+1)}
	}
	OmitUnavailableReplayReport(report)
	if len(report.Events) != MaxRetainedReplayEvents || report.DroppedEventCount != 2 || report.Events[0].Success != true || report.Events[1].Success != false || len(report.Events[0].StepPath) != maxReplayPathDepth {
		t.Fatal("unavailable report failed bounded entry/path retention or changed prior outcomes")
	}
	OmitUnavailableReplayReport(report)
	if report.DroppedEventCount != 2 {
		t.Fatal("idempotent projection double-counted omissions")
	}
	action := &ReplayDebugAction{Phase: "action", ActionIndex: 3, EventIndex: 4, LeafOrdinal: 5, ActionType: "http_sequence", ActionName: "unknown label", Target: "/unknown", Selector: "unknown", SourceStepID: "unknown", ParentActionName: "unknown", StepPath: []string{"actions[3]"}, IterationPath: []int{2}}
	OmitUnavailableReplayDebugAction(nil)
	OmitUnavailableReplayDebugAction(action)
	if action.ActionName != "Action evidence unavailable" || action.Target != "" || action.Selector != "" || action.SourceStepID != "" || action.ParentActionName != "" || action.Phase != "action" || action.ActionIndex != 3 || action.EventIndex != 4 || action.LeafOrdinal != 5 || len(action.StepPath) != 1 || action.ActionType != "http_sequence" {
		t.Fatal("unavailable debug projection lost bounded structural identity or retained detail")
	}
}

func TestUnavailableReplayProjectionEnforcesAggregateStructuralByteCeiling(t *testing.T) {
	const events = 550
	path := make([]string, maxReplayPathDepth)
	for i := range path {
		path[i] = strings.Repeat("p", maxReplayIdentityBytes)
	}
	report := &ReplayReport{Events: make([]ReplayActionEvent, events)}
	for i := range report.Events {
		report.Events[i] = ReplayActionEvent{EventIndex: i, ActionType: "GET", StepPath: path, Success: true, StatusCode: 200}
	}
	OmitUnavailableReplayReport(report)
	if report.DroppedEventCount == 0 || report.retainedEventJSONBytes > MaxRetainedReplayEventBytes || len(report.Events) != len(report.eventJSONBytes) {
		t.Fatal("unavailable projection retained structural events beyond the aggregate byte ceiling")
	}
	if report.Events[len(report.Events)-1].EventIndex != events-1 || report.Events[0].EventIndex != report.DroppedEventCount {
		t.Fatal("byte admission failed to retain the most recent source-ordered outcomes")
	}
	previousDrops, previousBytes := report.DroppedEventCount, report.retainedEventJSONBytes
	OmitUnavailableReplayReport(report)
	if report.DroppedEventCount != previousDrops || report.retainedEventJSONBytes != previousBytes {
		t.Fatal("bounded terminal projection was not idempotent")
	}
}

func TestReplayUnavailableProgressInvalidatesPriorEvidenceWithoutCallback(t *testing.T) {
	for _, callbackPresent := range []bool{false, true} {
		t.Run(map[bool]string{false: "without_callback", true: "with_callback"}[callbackPresent], func(t *testing.T) {
			prefix := strings.Repeat("z", 512)
			report := &ReplayReport{Success: true, Events: []ReplayActionEvent{{Success: true, StatusCode: 200, Body: prefix}}}
			vu := &VUser{runtimeCredentialRedactionErr: errors.New("synthetic classification failure")}
			var events []ReplayProgressEvent
			var callback func(ReplayProgressEvent)
			if callbackPresent {
				callback = func(event ReplayProgressEvent) { events = append(events, event) }
			}
			publish := vu.guardedReplayProgress(report, callback)
			action := &ReplayDebugAction{ActionName: prefix, Phase: "action", ActionIndex: 1}
			condition := &ConditionEvidence{Expr: prefix, Left: prefix, Right: prefix, Op: "eq", Result: true, Branch: "then"}
			publish(ReplayProgressEvent{Type: ReplayProgressCondition, Action: action, Condition: condition})
			publish(ReplayProgressEvent{Type: ReplayProgressComplete, Report: cloneReplayReport(report)})
			if !report.RuntimeEvidenceUnavailable || report.Events[0].Body != "" || !report.Events[0].Success || condition.Expr != "" || !condition.Result || condition.Branch != "then" {
				t.Fatal("first unavailable signal failed to invalidate prior evidence while preserving branch/outcome")
			}
			for _, event := range events {
				data, _ := json.Marshal(event)
				if !event.RuntimeEvidenceUnavailable || strings.Contains(string(data), prefix) {
					t.Fatal("progress/terminal callback retained prior unknown detail or lacked the signal")
				}
			}
		})
	}
}

func TestRuntimeUnavailablePreservesDeclaredMetricIdentityOnly(t *testing.T) {
	vu := &VUser{Group: "Declared group", Metrics: NewMetricsCollector(8), Script: &dsl.Script{}, runtimeCredentialRedactionErr: errors.New("synthetic classification failure")}
	sink := &unavailableMetricIdentitySink{}
	vu.ResultSink = sink
	result := vu.recordTransaction("Declared transaction", time.Millisecond, false, &protocol.Result{ErrorMessage: "unknown detail", DependencyTarget: "unknown target"})
	metric := <-vu.Metrics.ch
	if metric.Group != "Declared group" || metric.ActionName != "Declared transaction" || !metric.BusinessTransaction || metric.DependencyTarget != "" || metric.ErrorMessage == "unknown detail" {
		t.Fatal("declared metric identity was changed or arbitrary detail restored")
	}
	if result.Group != "" || result.ActionName != "Action evidence unavailable" || len(sink.transactions) != 1 || sink.transactions[0] == metric || sink.transactions[0].ActionName == "Declared transaction" {
		t.Fatal("private metrics-only identity copy reached a public transaction sink")
	}
}

type unavailableMetricIdentitySink struct{ transactions []*protocol.Result }

func (*unavailableMetricIdentitySink) RecordResult(*protocol.Result) error { return nil }
func (s *unavailableMetricIdentitySink) RecordTransaction(result *protocol.Result) error {
	s.transactions = append(s.transactions, result)
	return nil
}
