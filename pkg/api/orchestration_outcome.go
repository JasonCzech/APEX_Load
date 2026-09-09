package api

import "time"

// IntegrationOutcome is the additive, versioned cross-application outcome contract.
// It contains computed acceptance only; analyst annotations remain separate.
type IntegrationScope struct {
	ProjectID     string `json:"project_id,omitempty"`
	ApplicationID string `json:"application_id,omitempty"`
	ReleaseID     string `json:"release_id,omitempty"`
}

type IntegrationOutcome struct {
	Scope         IntegrationScope      `json:"scope"`
	SchemaVersion int                   `json:"schema_version"`
	ExecutionID   string                `json:"execution_id"`
	ObservedAt    time.Time             `json:"observed_at"`
	Assessment    IntegrationAssessment `json:"assessment"`
	Execution     IntegrationExecution  `json:"execution"`
	Evidence      IntegrationEvidence   `json:"evidence"`
}
type IntegrationAssessment struct {
	Status           string  `json:"status"`
	ReasonCode       string  `json:"reason_code"`
	Explanation      string  `json:"explanation"`
	CriteriaRevision *string `json:"criteria_revision"`
}
type IntegrationExecution struct {
	TerminationReason       string   `json:"termination_reason"`
	PlannedDurationSeconds  *float64 `json:"planned_duration_seconds"`
	ObservedDurationSeconds *float64 `json:"observed_duration_seconds"`
}
type IntegrationEvidence struct {
	Status          string   `json:"status"`
	CoveragePercent *float64 `json:"coverage_percent"`
}

func buildIntegrationOutcome(report *RunArchiveReport) *IntegrationOutcome {
	if report == nil || report.Manifest == nil {
		return nil
	}
	manifest := report.Manifest
	verdict := deriveReportVerdict(report)
	statuses := map[string]string{reportVerdictPass: "passed", reportVerdictFail: "failed", reportVerdictConditional: "inconclusive", reportVerdictNotAssessed: "not_assessed"}
	reasons := map[string]string{reportVerdictPass: "criteria_met", reportVerdictFail: "criteria_breached", reportVerdictConditional: "assessment_inconclusive", reportVerdictNotAssessed: "no_criteria"}
	outcome := &IntegrationOutcome{Scope: IntegrationScope{ProjectID: manifest.Provenance.ProjectID, ApplicationID: manifest.Provenance.ApplicationID, ReleaseID: manifest.Provenance.ReleaseID}, SchemaVersion: 1, ExecutionID: manifest.TestID, ObservedAt: time.Now().UTC(), Assessment: IntegrationAssessment{Status: statuses[verdict.Status], ReasonCode: reasons[verdict.Status], Explanation: verdict.Explanation}, Execution: IntegrationExecution{TerminationReason: manifest.TerminationReason}, Evidence: IntegrationEvidence{Status: "unavailable"}}
	if outcome.Assessment.Status == "" {
		outcome.Assessment.Status = "unavailable"
		outcome.Assessment.ReasonCode = "unknown_assessment"
	}
	if outcome.Execution.TerminationReason == "" {
		outcome.Execution.TerminationReason = "unknown"
	}
	provenance := manifest.Provenance
	if manifest.InitialProvenance != nil {
		provenance = *manifest.InitialProvenance
	}
	if duration, err := time.ParseDuration(provenance.Duration); err == nil && duration >= 0 {
		seconds := duration.Seconds()
		outcome.Execution.PlannedDurationSeconds = &seconds
	}
	if manifest.Provenance.StartedAt != nil && !manifest.CompletedAt.IsZero() && !manifest.CompletedAt.Before(*manifest.Provenance.StartedAt) {
		seconds := manifest.CompletedAt.Sub(*manifest.Provenance.StartedAt).Seconds()
		outcome.Execution.ObservedDurationSeconds = &seconds
	}
	if report.Completeness.Status != "" {
		score := report.Completeness.ScorePct
		outcome.Evidence.CoveragePercent = &score
		outcome.Evidence.Status = "partial"
		if report.Completeness.Status == "complete" {
			outcome.Evidence.Status = "complete"
		}
	}
	return outcome
}
