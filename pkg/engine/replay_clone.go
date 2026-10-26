package engine

import (
	"maps"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneIntMap(input map[string]int) map[string]int {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]int, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func cloneAnyMap(input map[string]any) map[string]any {
	return protocol.BoundProtocolEvidence(input)
}

func cloneReplayDebugAction(action ReplayDebugAction) *ReplayDebugAction {
	cloned := action
	if action.RequestIndex != nil {
		idx := *action.RequestIndex
		cloned.RequestIndex = &idx
	}
	if action.Iteration != nil {
		it := *action.Iteration
		cloned.Iteration = &it
	}
	if len(action.StepPath) > 0 {
		cloned.StepPath = append([]string(nil), action.StepPath...)
	}
	if len(action.IterationPath) > 0 {
		cloned.IterationPath = append([]int(nil), action.IterationPath...)
	}
	return &cloned
}

func cloneReplayActionEvent(event ReplayActionEvent) *ReplayActionEvent {
	event.AuthoredStep = event.AuthoredStep.Clone()
	cloned := event
	// Raw correlation evidence is single-use and must not survive into any
	// report/session/progress clone, even though its field is also JSON-excluded.
	cloned.CorrelationEvidence = nil
	cloned.Headers = cloneStringMap(event.Headers)
	cloned.Extractions = cloneStringMap(event.Extractions)
	cloned.Console = cloneConsoleEntries(event.Console)
	cloned.Network = cloneNetworkEntries(event.Network)
	cloned.DOMSummary = cloneDOMSummary(event.DOMSummary)
	cloned.ClientMetrics = cloneClientMetrics(event.ClientMetrics)
	cloned.ProtocolEvidence = cloneAnyMap(event.ProtocolEvidence)
	cloned.Diagnosis = cloneReplayDiagnosis(event.Diagnosis)
	cloned.RepairSuggestions = cloneReplayRepairSuggestions(event.RepairSuggestions)
	if event.RequestIndex != nil {
		idx := *event.RequestIndex
		cloned.RequestIndex = &idx
	}
	if event.Iteration != nil {
		it := *event.Iteration
		cloned.Iteration = &it
	}
	if len(event.StepPath) > 0 {
		cloned.StepPath = append([]string(nil), event.StepPath...)
	}
	if len(event.IterationPath) > 0 {
		cloned.IterationPath = append([]int(nil), event.IterationPath...)
	}
	if event.Snapshot != nil {
		snapshot := *event.Snapshot
		snapshot.Resources = protocol.CloneResourceSnapshots(event.Snapshot.Resources)
		snapshot.RequestHeaders = cloneStringMap(event.Snapshot.RequestHeaders)
		snapshot.ResponseHeaders = cloneStringMap(event.Snapshot.ResponseHeaders)
		snapshot.ConsoleEntries = cloneConsoleEntries(event.Snapshot.ConsoleEntries)
		snapshot.NetworkEntries = cloneNetworkEntries(event.Snapshot.NetworkEntries)
		snapshot.DOMSummary = cloneDOMSummary(event.Snapshot.DOMSummary)
		cloned.Snapshot = &snapshot
	}
	return &cloned
}

func cloneReplayReport(report *ReplayReport) *ReplayReport {
	if report == nil {
		return nil
	}
	cloned := *report
	cloned.NativeHARCaptureOmissions = maps.Clone(report.NativeHARCaptureOmissions)
	cloned.FinalVariables = cloneStringMap(report.FinalVariables)
	cloned.RuntimeProfile = *cloneReplayRuntimeProfile(&report.RuntimeProfile)
	cloned.ArtifactManifest = cloneReplayArtifactManifest(report.ArtifactManifest)
	cloned.DiagnosisSummary = cloneReplayDiagnosisSummary(report.DiagnosisSummary)
	cloned.RepairSuggestions = cloneReplayRepairSuggestions(report.RepairSuggestions)
	if len(report.eventJSONBytes) > 0 {
		cloned.eventJSONBytes = append([]int(nil), report.eventJSONBytes...)
	}
	snapshotClones := make(map[*protocol.ErrorSnapshot]*protocol.ErrorSnapshot, len(report.Snapshots))
	if len(report.Snapshots) > 0 {
		cloned.Snapshots = make([]*protocol.ErrorSnapshot, len(report.Snapshots))
		for i, snapshot := range report.Snapshots {
			if snapshot == nil {
				continue
			}
			bounded := BoundErrorSnapshot(snapshot, defaultErrorSnapshotMaxBytes)
			cloned.Snapshots[i] = bounded
			snapshotClones[snapshot] = bounded
		}
	}
	if len(report.Events) > 0 {
		cloned.Events = make([]ReplayActionEvent, len(report.Events))
		for i, event := range report.Events {
			cloned.Events[i] = *cloneReplayActionEvent(event)
			if shared := snapshotClones[event.Snapshot]; shared != nil {
				cloned.Events[i].Snapshot = shared
			}
		}
	}
	return &cloned
}

func cloneReplayRuntimeProfile(profile *ReplayRuntimeProfile) *ReplayRuntimeProfile {
	if profile == nil {
		return nil
	}
	cloned := *profile
	cloned.HAROptions = profile.HAROptions.Clone()
	if profile.Headless != nil {
		headless := *profile.Headless
		cloned.Headless = &headless
	}
	return &cloned
}

func cloneReplayArtifactManifest(manifest *ReplayArtifactManifest) *ReplayArtifactManifest {
	if manifest == nil {
		return nil
	}
	cloned := *manifest
	cloned.RuntimeProfile = *cloneReplayRuntimeProfile(&manifest.RuntimeProfile)
	if len(manifest.DataSources) > 0 {
		cloned.DataSources = append([]string(nil), manifest.DataSources...)
	}
	if len(manifest.SeedVariables) > 0 {
		cloned.SeedVariables = append([]string(nil), manifest.SeedVariables...)
	}
	if len(manifest.ProtocolArtifacts) > 0 {
		cloned.ProtocolArtifacts = make(map[string]int, len(manifest.ProtocolArtifacts))
		for key, value := range manifest.ProtocolArtifacts {
			cloned.ProtocolArtifacts[key] = value
		}
	}
	return &cloned
}

func cloneReplayDiagnosis(diagnosis *ReplayDiagnosis) *ReplayDiagnosis {
	if diagnosis == nil {
		return nil
	}
	cloned := *diagnosis
	if len(diagnosis.Signals) > 0 {
		cloned.Signals = append([]string(nil), diagnosis.Signals...)
	}
	return &cloned
}

func cloneReplayRepairSuggestions(suggestions []ReplayRepairSuggestion) []ReplayRepairSuggestion {
	if len(suggestions) == 0 {
		return nil
	}
	cloned := make([]ReplayRepairSuggestion, len(suggestions))
	for i, suggestion := range suggestions {
		cloned[i] = cloneReplayRepairSuggestion(suggestion)
	}
	return cloned
}

func cloneReplayRepairSuggestion(suggestion ReplayRepairSuggestion) ReplayRepairSuggestion {
	cloned := suggestion
	if len(suggestion.Fields) > 0 {
		cloned.Fields = cloneStringMap(suggestion.Fields)
	}
	return cloned
}

func cloneReplayDiagnosisSummary(summary *ReplayDiagnosisSummary) *ReplayDiagnosisSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.Categories) > 0 {
		cloned.Categories = make(map[string]int, len(summary.Categories))
		for key, value := range summary.Categories {
			cloned.Categories[key] = value
		}
	}
	return &cloned
}
func cloneConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.ConsoleEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	return protocol.CloneNetworkEntries(entries)
}

func cloneDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.VisibleText) > 0 {
		cloned.VisibleText = append([]string(nil), summary.VisibleText...)
	}
	return &cloned
}

func cloneClientMetrics(metrics *protocol.ClientMetrics) *protocol.ClientMetrics {
	if metrics == nil {
		return nil
	}
	cloned := *metrics
	cloned.DocumentEvidence = metrics.DocumentEvidence.Clone()
	cloned.RuntimeAvailability = metrics.RuntimeAvailability.Clone()
	return &cloned
}
