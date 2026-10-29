package engine

import (
	"encoding/json"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Replay reports are interactive diagnostics, not an unbounded event archive.
// Keep enough recent scalar history for debugging while placing a hard bound on
// the event portion of the terminal JSON document. EventIndex remains the
// monotonic execution index even when older retained entries are evicted.
const (
	MaxRetainedReplayEvents     = 5000
	MaxRetainedReplayEventBytes = 32 << 20

	maxReplayIdentityBytes = 1 << 10
	maxReplayEndpointBytes = 16 << 10
	maxReplayErrorBytes    = 16 << 10
	maxReplayPathDepth     = 64
)

// This budget counts snapshots without holding an additional copy of their payloads.
type replaySnapshotBudget struct {
	maxItems, maxItemBytes, remainingBytes, retainedItems int
}

func (report *ReplayReport) nextEventIndex() int {
	if report == nil {
		return 0
	}
	return report.totalEventCount
}

// appendReplayReportEvent is the only seam that adds an event to a replay
// report. It preserves scalar identity/outcome fields, admits rich evidence
// only while the aggregate evidence budget remains, and then enforces both the
// entry and serialized-event budgets by evicting the oldest retained entries.
func appendReplayReportEvent(report *ReplayReport, event ReplayActionEvent) ReplayActionEvent {
	if report != nil && report.RuntimeEvidenceUnavailable {
		omitUnredactableReplayActionEvent(&event)
	}
	if report == nil {
		return BoundReplayActionEventForRetention(event, false)
	}

	event.EventIndex = report.totalEventCount
	report.totalEventCount++
	recordReplayReportDiagnosis(report, event)
	recordReplayArtifactManifestEvent(report, event)
	if budget := report.messageDetailBudget; budget != nil && event.Snapshot != nil {
		if budget.retainedItems >= budget.maxItems || min(budget.maxItemBytes, budget.remainingBytes) < snapshotBaseOverhead {
			if event.Screenshot == "" && (event.Snapshot.ScreenshotBase64 != "" || event.Snapshot.ScreenshotOmitted) {
				event.ScreenshotOmitted = true
			}
			event.Snapshot = nil
			event.ResponseBodyState = "not_retained"
			report.TruncatedEventEvidenceCount++
		} else {
			var retainedBytes int
			event.Snapshot, retainedBytes = boundedErrorSnapshot(event.Snapshot, min(budget.maxItemBytes, budget.remainingBytes))
			budget.remainingBytes -= retainedBytes
			budget.retainedItems++
		}
	}

	compact := BoundReplayActionEventForRetention(event, false)
	compactBytes := ReplayActionEventRetentionBytes(compact)
	retained := compact
	retainedBytes := compactBytes
	hasRichEvidence := replayEventHasRichEvidence(event)
	if hasRichEvidence && report.consumedEvidenceBytes < MaxRetainedReplayEventBytes {
		report.evidenceProjectionCount++
		full := BoundReplayActionEventForRetention(event, true)
		fullBytes := ReplayActionEventRetentionBytes(full)
		richBytes := max(0, fullBytes-compactBytes)
		if fullBytes <= MaxRetainedReplayEventBytes &&
			report.consumedEvidenceBytes+richBytes <= MaxRetainedReplayEventBytes {
			retained = full
			retainedBytes = fullBytes
			report.consumedEvidenceBytes += richBytes
		} else {
			// Seal the evidence budget after the first event that cannot fit. All
			// later appends go directly to the compact projection without cloning
			// or JSON-marshalling their potentially multi-megabyte rich payloads.
			report.consumedEvidenceBytes = MaxRetainedReplayEventBytes
			if richBytes > 0 {
				report.TruncatedEventEvidenceCount++
			}
		}
	} else if hasRichEvidence {
		report.TruncatedEventEvidenceCount++
	}
	if event.Snapshot != nil && retained.Snapshot != nil {
		if report.retainedSnapshotClones == nil {
			report.retainedSnapshotClones = make(map[*protocol.ErrorSnapshot]*protocol.ErrorSnapshot)
		}
		if shared := report.retainedSnapshotClones[event.Snapshot]; shared != nil {
			retained.Snapshot = shared
		} else {
			report.retainedSnapshotClones[event.Snapshot] = retained.Snapshot
		}
	}

	report.Events = append(report.Events, retained)
	report.eventJSONBytes = append(report.eventJSONBytes, retainedBytes)
	report.retainedEventJSONBytes += retainedBytes
	for len(report.Events) > MaxRetainedReplayEvents ||
		report.retainedEventJSONBytes > MaxRetainedReplayEventBytes {
		report.dropOldestRetainedEvent()
	}

	// Scalar bounds make a single compact event comfortably smaller than the
	// aggregate limit, so the newly appended event is always the retained tail.
	if len(report.Events) == 0 {
		return compact
	}
	return report.Events[len(report.Events)-1]
}

func (report *ReplayReport) dropOldestRetainedEvent() {
	if report == nil || len(report.Events) == 0 {
		return
	}
	if len(report.eventJSONBytes) > 0 {
		report.retainedEventJSONBytes -= report.eventJSONBytes[0]
		if report.retainedEventJSONBytes < 0 {
			report.retainedEventJSONBytes = 0
		}
		report.eventJSONBytes[0] = 0
		report.eventJSONBytes = report.eventJSONBytes[1:]
	}
	report.Events[0] = ReplayActionEvent{}
	report.Events = report.Events[1:]
	report.DroppedEventCount++
}

// BoundReplayActionEventForRetention returns a detached event projection. When
// includeRichEvidence is false, request/response bodies, browser artifacts,
// protocol evidence, snapshots, and repair detail are removed while execution
// identity, timings, status, and compact failure classification remain.
func BoundReplayActionEventForRetention(event ReplayActionEvent, includeRichEvidence bool) ReplayActionEvent {
	if !includeRichEvidence {
		// Clear rich references before cloning so compact retention never copies a
		// body, screenshot, network trace, or snapshot it will immediately drop.
		bounded := event
		stripReplayEventRichEvidence(&bounded)
		bounded = *cloneReplayActionEvent(bounded)
		boundReplayEventScalarFields(&bounded)
		return bounded
	}
	source := event
	source.Snapshot = nil
	bounded := *cloneReplayActionEvent(source)
	bounded.Snapshot, _ = boundedErrorSnapshot(event.Snapshot, defaultErrorSnapshotMaxBytes)
	boundReplayEventScalarFields(&bounded)
	return bounded
}

// ReplayActionEventRetentionBytes accounts for the event's JSON plus evidence
// duplicated by ReplayReport's report-level snapshot and repair-suggestion
// collections.
func ReplayActionEventRetentionBytes(event ReplayActionEvent) int {
	payload, err := json.Marshal(event)
	if err != nil {
		return MaxRetainedReplayEventBytes + 1
	}
	retained := len(payload)
	if event.Snapshot != nil {
		if snapshot, snapshotErr := json.Marshal(event.Snapshot); snapshotErr == nil {
			retained += len(snapshot)
		} else {
			return MaxRetainedReplayEventBytes + 1
		}
	}
	if len(event.RepairSuggestions) > 0 {
		if suggestions, suggestionsErr := json.Marshal(event.RepairSuggestions); suggestionsErr == nil {
			retained += len(suggestions)
		} else {
			return MaxRetainedReplayEventBytes + 1
		}
	}
	return retained
}

func boundReplayEventScalarFields(event *ReplayActionEvent) {
	if event == nil {
		return
	}
	event.CorrelationEvidence = nil
	event.EventID = truncateReplayScalar(event.EventID, maxReplayIdentityBytes)
	event.Phase = truncateReplayScalar(event.Phase, maxReplayIdentityBytes)
	event.ActionName = truncateReplayScalar(event.ActionName, maxReplayIdentityBytes)
	event.ActionType = truncateReplayScalar(event.ActionType, maxReplayIdentityBytes)
	event.Method = truncateReplayScalar(event.Method, maxReplayIdentityBytes)
	event.Target = truncateReplayScalar(event.Target, maxReplayEndpointBytes)
	event.Selector = truncateReplayScalar(event.Selector, maxReplayEndpointBytes)
	event.ErrorMessage = truncateReplayScalar(event.ErrorMessage, maxReplayErrorBytes)
	event.CredentialClassificationError = truncateReplayScalar(event.CredentialClassificationError, maxReplayErrorBytes)
	event.PageURL = truncateReplayScalar(event.PageURL, maxReplayEndpointBytes)
	event.PageTitle = truncateReplayScalar(event.PageTitle, maxReplayIdentityBytes)
	event.SourceStepID = truncateReplayScalar(event.SourceStepID, maxReplayIdentityBytes)
	event.SourceAction = truncateReplayScalar(event.SourceAction, maxReplayIdentityBytes)
	event.SourceActionName = truncateReplayScalar(event.SourceActionName, maxReplayIdentityBytes)
	event.SourceTransaction = truncateReplayScalar(event.SourceTransaction, maxReplayIdentityBytes)
	event.SourceProfile = truncateReplayScalar(event.SourceProfile, maxReplayIdentityBytes)
	event.ParentActionName = truncateReplayScalar(event.ParentActionName, maxReplayIdentityBytes)
	if len(event.StepPath) > maxReplayPathDepth {
		event.StepPath = event.StepPath[:maxReplayPathDepth]
	}
	for i := range event.StepPath {
		event.StepPath[i] = truncateReplayScalar(event.StepPath[i], maxReplayIdentityBytes)
	}
	if len(event.IterationPath) > maxReplayPathDepth {
		event.IterationPath = event.IterationPath[:maxReplayPathDepth]
	}
}

func stripReplayEventRichEvidence(event *ReplayActionEvent) {
	if event == nil {
		return
	}
	event.Value = ""
	event.Headers = nil
	event.Body = ""
	event.Extractions = nil
	event.UnappliedExtractionsOmittedCount += len(event.UnappliedExtractions)
	event.UnappliedExtractions = nil
	event.StreamedChildrenOmittedCount += len(event.StreamedChildren)
	event.StreamedChildren = nil
	event.HTML = ""
	if event.Screenshot != "" || (event.Snapshot != nil && (event.Snapshot.ScreenshotBase64 != "" || event.Snapshot.ScreenshotOmitted)) {
		event.ScreenshotOmitted = true
	}
	event.Screenshot = ""
	event.Console = nil
	event.Network = nil
	event.DOMSummary = nil
	if len(event.ProtocolEvidence) > 0 {
		event.ProtocolEvidenceOmittedCount++
	}
	event.ProtocolEvidence = nil
	if event.Snapshot != nil {
		event.ResponseBodyState = "not_retained"
	}
	event.Snapshot = nil
	event.RepairSuggestions = nil
	if event.Diagnosis != nil {
		diagnosis := *event.Diagnosis
		diagnosis.Category = truncateReplayScalar(diagnosis.Category, maxReplayIdentityBytes)
		diagnosis.Severity = truncateReplayScalar(diagnosis.Severity, maxReplayIdentityBytes)
		diagnosis.Cause = truncateReplayScalar(diagnosis.Cause, maxReplayErrorBytes)
		diagnosis.Action = truncateReplayScalar(diagnosis.Action, maxReplayErrorBytes)
		diagnosis.Evidence = ""
		diagnosis.Signals = nil
		diagnosis.FrameContext = ""
		diagnosis.SuggestedLocator = ""
		event.Diagnosis = &diagnosis
	}
}

func replayEventHasRichEvidence(event ReplayActionEvent) bool {
	if event.Value != "" || len(event.Headers) > 0 || event.Body != "" || len(event.Extractions) > 0 || len(event.UnappliedExtractions) > 0 || len(event.StreamedChildren) > 0 ||
		event.HTML != "" || event.Screenshot != "" || len(event.Console) > 0 || len(event.Network) > 0 ||
		event.DOMSummary != nil || len(event.ProtocolEvidence) > 0 || event.Snapshot != nil ||
		len(event.RepairSuggestions) > 0 {
		return true
	}
	return event.Diagnosis != nil && (event.Diagnosis.Evidence != "" || len(event.Diagnosis.Signals) > 0 ||
		event.Diagnosis.FrameContext != "" || event.Diagnosis.SuggestedLocator != "")
}

func truncateReplayScalar(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return boundedtext.TruncateUTF8(value, maxBytes)
}

func recordReplayReportDiagnosis(report *ReplayReport, event ReplayActionEvent) {
	if report == nil || report.RuntimeEvidenceUnavailable || event.Success {
		return
	}
	if report.DiagnosisSummary == nil {
		report.DiagnosisSummary = &ReplayDiagnosisSummary{Categories: make(map[string]int)}
	}
	summary := report.DiagnosisSummary
	summary.FailureCount++
	category := "unknown"
	cause := strings.TrimSpace(event.ErrorMessage)
	if event.Diagnosis != nil {
		category = event.Diagnosis.Category
		cause = event.Diagnosis.Cause
	}
	summary.Categories[category]++
	if summary.FirstFailureEventID == "" {
		summary.FirstFailureEventID = truncateReplayScalar(event.EventID, maxReplayIdentityBytes)
		summary.FirstFailureAction = truncateReplayScalar(event.ActionName, maxReplayIdentityBytes)
		summary.FirstFailureCause = truncateReplayScalar(cause, maxReplayErrorBytes)
	}
}

func recordReplayArtifactManifestEvent(report *ReplayReport, event ReplayActionEvent) {
	if report == nil || report.ArtifactManifest == nil {
		return
	}
	manifest := report.ArtifactManifest
	manifest.EventCount++
	if !event.Success {
		manifest.FailureCount++
	}
	if event.Snapshot != nil {
		if report.manifestSnapshots == nil {
			report.manifestSnapshots = make(map[*protocol.ErrorSnapshot]struct{})
		}
		if _, exists := report.manifestSnapshots[event.Snapshot]; !exists {
			report.manifestSnapshots[event.Snapshot] = struct{}{}
			manifest.Snapshots++
		}
	}
	if event.Screenshot != "" || event.Snapshot != nil && event.Snapshot.ScreenshotBase64 != "" {
		manifest.Screenshots++
	}
	manifest.NetworkEntries += len(event.Network)
	manifest.ConsoleEntries += len(event.Console)
	if event.Snapshot != nil {
		manifest.NetworkEntries += len(event.Snapshot.NetworkEntries)
		manifest.ConsoleEntries += len(event.Snapshot.ConsoleEntries)
	}
	if event.HTML != "" || event.Snapshot != nil && event.Snapshot.HTML != "" {
		manifest.HTMLCaptured = true
	}
	if event.PageURL != "" || event.PageTitle != "" || event.DOMSummary != nil || len(event.Console) > 0 ||
		len(event.Network) > 0 || event.Screenshot != "" {
		manifest.BrowserEvidence = true
	}
	if protocolName, ok := event.ProtocolEvidence["protocol"].(string); ok && strings.TrimSpace(protocolName) != "" {
		if manifest.ProtocolArtifacts == nil {
			manifest.ProtocolArtifacts = make(map[string]int)
		}
		manifest.ProtocolArtifacts[protocolName]++
	}
}

// setReplayReportSnapshots keeps the report-level view pointer-shared with the
// retained event view. Evidence removed by aggregate retention is therefore not
// reintroduced from SnapshotStore during terminal report construction.
func setReplayReportSnapshots(report *ReplayReport) {
	if report == nil {
		return
	}
	report.Snapshots = nil
	seen := make(map[*protocol.ErrorSnapshot]struct{})
	for i := range report.Events {
		snapshot := report.Events[i].Snapshot
		if snapshot == nil {
			continue
		}
		if _, ok := seen[snapshot]; ok {
			continue
		}
		seen[snapshot] = struct{}{}
		report.Snapshots = append(report.Snapshots, snapshot)
	}
	// These maps are build-time accounting only. Clearing them at the terminal
	// boundary prevents dropped/original snapshots from being retained through
	// otherwise invisible map keys after the bounded report is returned.
	report.manifestSnapshots = nil
	report.retainedSnapshotClones = nil
}
