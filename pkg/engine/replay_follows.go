package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

// Reasons a failure is linked to the run's first failure. When several apply,
// the first in this order is reported.
const (
	ReplayFollowsReasonAuraInvalidSession = "aura_invalid_session"
	ReplayFollowsReasonHTTP401            = "http_401"
	ReplayFollowsReasonHTTP403            = "http_403"
	ReplayFollowsReasonUnresolvedVariable = "unresolved_variable"

	// ReplayFollowsFailureDraftPatchType marks the single repair suggestion a
	// linked follower carries: fix the first failure, not this one.
	ReplayFollowsFailureDraftPatchType = "follows_failure"

	maxReplayUnresolvedVariables = 10
)

// replayRequestVariables is the resolution-time record of the {{var.NAME}}
// references a request could not resolve. known is false for an event that was
// not produced from a resolved request.
type replayRequestVariables struct {
	known bool
	names []string
}

// noteUnresolvedVariable records one unresolved {{var.NAME}} reference by name.
func (vu *VUser) noteUnresolvedVariable(name string) {
	if name == "" {
		return
	}
	if vu.unresolvedVariableRefs == nil {
		vu.unresolvedVariableRefs = make(map[string]struct{})
	}
	vu.unresolvedVariableRefs[truncateReplayScalar(name, maxReplayIdentityBytes)] = struct{}{}
}

// takeReplayRequestVariables returns the sorted, bounded names the last
// resolveAction could not resolve on its request, and clears the record.
func (vu *VUser) takeReplayRequestVariables() replayRequestVariables {
	refs := vu.unresolvedVariableRefs
	vu.unresolvedVariableRefs = nil
	return replayRequestVariables{known: true, names: sortedReplayVariableNames(refs)}
}

// publishableReplayRequestVariables withholds the resolution-time record when
// the event's public request evidence is withheld (a concurrent child whose
// joined evidence could not be published, or an unredactable runtime
// credential). The authored {{var.NAME}} names are request identity, so the
// record is kept known but empty: the diagnosis then lists no names, and the
// fallback never scans whatever request surface the event still carries.
func (vu *VUser) publishableReplayRequestVariables(record replayRequestVariables) replayRequestVariables {
	if !vu.unredactedDebug && vu.resultPublicationUnavailable() {
		return replayRequestVariables{known: true}
	}
	return record
}

func sortedReplayVariableNames(seen map[string]struct{}) []string {
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) > maxReplayUnresolvedVariables {
		names = names[:maxReplayUnresolvedVariables]
	}
	return names
}

// annotateReplayFailureDiagnosis runs in the append seam, before the failure is
// counted in the summary. It records the {{var.NAME}} references a failed
// request could not resolve and, when the failure shows a session-loss signal
// after an earlier root failure, links it to that first failure. The
// rule-based Category/Cause/Action are left unchanged.
func annotateReplayFailureDiagnosis(report *ReplayReport, event *ReplayActionEvent) {
	if report == nil || report.RuntimeEvidenceUnavailable || event == nil || event.Success || event.Diagnosis == nil {
		return
	}
	// Detach the diagnosis so the annotation never writes through a pointer a
	// caller's copy of the event still shares.
	diagnosis := *event.Diagnosis
	if event.requestVariables.known {
		// Recorded at resolution time: only references that really had no
		// value, whatever redaction later did to the published request, and
		// never the set variables of a body that failed to resolve.
		diagnosis.UnresolvedVariables = slices.Clone(event.requestVariables.names)
	} else {
		diagnosis.UnresolvedVariables = replayUnresolvedVariables(*event)
	}
	event.Diagnosis = &diagnosis

	summary := report.DiagnosisSummary
	if summary == nil || summary.FirstFailureEventID == "" || !report.firstFailureIsRoot ||
		report.firstFailureEventIndex >= event.EventIndex || summary.FirstFailureEventID == event.EventID ||
		replayPhaseIs(event.Phase, "async") || replayRanAlongsideFirstFailure(report, *event) {
		return
	}
	reason := replayFollowsReason(*event, diagnosis.UnresolvedVariables)
	if reason == "" {
		return
	}
	diagnosis.FollowsFailureEventID = summary.FirstFailureEventID
	diagnosis.FollowsFailureAction = summary.FirstFailureAction
	diagnosis.FollowsReason = reason
	summary.FollowingFailureCount++
	event.RepairSuggestions = []ReplayRepairSuggestion{replayFollowsFailureSuggestion(*event, summary.FirstFailureAction)}
}

// replayRanAlongsideFirstFailure reports whether the event ran concurrently
// with the first failure: a sibling in the same concurrent HTTP group (whose
// requests cannot depend on one another), or a request that started before the
// first failure completed. Append order alone does not make it a consequence.
func replayRanAlongsideFirstFailure(report *ReplayReport, event ReplayActionEvent) bool {
	if report.firstFailureConcurrentGroup != "" && report.firstFailureConcurrentGroup == event.concurrentGroupID {
		return true
	}
	return !report.firstFailureCompletedAt.IsZero() && !event.StartedAt.IsZero() && event.StartedAt.Before(report.firstFailureCompletedAt)
}

// replayPhaseCanRootFollowingFailures reports whether a first failure in this
// phase can explain later failures. Background async responses and end-of-run
// source-transaction finalizations are consequences, not roots.
func replayPhaseCanRootFollowingFailures(phase string) bool {
	return replayPhaseIs(phase, "init") || replayPhaseIs(phase, "action") || replayPhaseIs(phase, "teardown")
}

func replayPhaseIs(phase, want string) bool {
	return strings.EqualFold(strings.TrimSpace(phase), want)
}

func replayFollowsReason(event ReplayActionEvent, unresolved []string) string {
	switch {
	case replayEventHasAuraInvalidSession(event):
		return ReplayFollowsReasonAuraInvalidSession
	case event.StatusCode == 401:
		return ReplayFollowsReasonHTTP401
	case event.StatusCode == 403:
		return ReplayFollowsReasonHTTP403
	case len(unresolved) > 0:
		return ReplayFollowsReasonUnresolvedVariable
	default:
		return ""
	}
}

func replayFollowsFailureSuggestion(event ReplayActionEvent, firstAction string) ReplayRepairSuggestion {
	baseID := event.EventID
	if baseID == "" {
		baseID = fmt.Sprintf("%s-%d", event.Phase, event.EventIndex)
	}
	description := "This failure came after the run's first failure and shows a lost session or a value that was never set. Fix the first failure, then replay again before changing this step."
	if name := strings.TrimSpace(firstAction); name != "" {
		description = fmt.Sprintf("This failure came after the run's first failure, %q, and shows a lost session or a value that was never set. Fix %q first, then replay again before changing this step.", name, name)
	}
	return ReplayRepairSuggestion{
		ID:             baseID + ":fix-first-failure",
		Title:          "Fix the first failure first",
		Description:    description,
		DraftPatchType: ReplayFollowsFailureDraftPatchType,
		Action:         event.ActionName,
	}
}

// replayEventHasAuraInvalidSession reads the HTTP Aura validation report
// (ProtocolEvidence["aura"]) and the browser Aura action summary
// (ProtocolEvidence["aura_actions"]). Evidence is normally JSON-normalized
// maps; typed reports are accepted too.
func replayEventHasAuraInvalidSession(event ReplayActionEvent) bool {
	return replayAuraReportHasInvalidSession(event.ProtocolEvidence["aura"]) ||
		replayAuraActionsHaveInvalidSession(event.ProtocolEvidence["aura_actions"])
}

func replayAuraReportHasInvalidSession(raw any) bool {
	switch report := raw.(type) {
	case aura.Report:
		return auraIssuesHaveInvalidSession(report.Issues)
	case *aura.Report:
		return report != nil && auraIssuesHaveInvalidSession(report.Issues)
	case map[string]any:
		for _, issue := range replayEvidenceObjects(report["issues"]) {
			if replayEvidenceString(issue["code"]) == string(aura.InvalidSession) {
				return true
			}
		}
	}
	return false
}

func auraIssuesHaveInvalidSession(issues []aura.Issue) bool {
	for _, issue := range issues {
		if issue.Code == aura.InvalidSession {
			return true
		}
	}
	return false
}

func replayAuraActionsHaveInvalidSession(raw any) bool {
	evidence, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for _, action := range replayEvidenceObjects(evidence["actions"]) {
		if replayEvidenceString(action["state"]) == string(aura.InvalidSession) {
			return true
		}
	}
	return false
}

func replayEvidenceObjects(raw any) []map[string]any {
	switch values := raw.(type) {
	case []map[string]any:
		return values
	case []any:
		objects := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if object, ok := value.(map[string]any); ok {
				objects = append(objects, object)
			}
		}
		return objects
	default:
		return nil
	}
}

func replayEvidenceString(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case aura.Code:
		return string(value)
	case aura.State:
		return string(value)
	default:
		return ""
	}
}

// replayUnresolvedVariables names the {{var.NAME}} tokens still written
// literally into the request the event reports. It is the fallback for an
// event with no resolution-time record (requestVariables.known is false).
func replayUnresolvedVariables(event ReplayActionEvent) []string {
	seen := make(map[string]struct{})
	collect := func(value string) {
		if !strings.Contains(value, "{{") {
			return
		}
		for _, match := range templatePattern.FindAllStringSubmatch(value, -1) {
			expr := strings.TrimSpace(match[1])
			if !strings.HasPrefix(expr, "var.") {
				continue
			}
			name := strings.TrimSpace(strings.TrimPrefix(expr, "var."))
			if name == "" {
				continue
			}
			seen[truncateReplayScalar(name, maxReplayIdentityBytes)] = struct{}{}
		}
	}
	collect(event.Target)
	collect(event.Body)
	collect(event.Value)
	collect(event.Selector)
	for _, value := range event.Headers {
		collect(value)
	}
	return sortedReplayVariableNames(seen)
}
