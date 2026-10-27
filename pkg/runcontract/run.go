package runcontract

import (
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// AnalysisPolicySelection pins an immutable browser or journey policy revision.
type AnalysisPolicySelection struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// AnalysisPolicySelections holds explicit run-level browser and journey policy
// revision selections.
type AnalysisPolicySelections struct {
	Browser *AnalysisPolicySelection `json:"browser,omitempty"`
	Journey *AnalysisPolicySelection `json:"journey,omitempty"`
}

// CreateTestRequest is the single-script client projection of the create-run
// wire contract. Controller-only creation state does not belong in clients.
type CreateTestRequest struct {
	Name         string      `json:"name"`
	Duration     string      `json:"duration"`
	ScenarioMode string      `json:"scenario_mode,omitempty"`
	GoalConfig   *GoalConfig `json:"goal_config,omitempty"`
	ScriptID     string      `json:"script_id,omitempty"`
	VUsers       int         `json:"vusers,omitempty"`
	RampUp       string      `json:"ramp_up,omitempty"`
	// TelemetryAcceptancePolicyID judges the run by one of its project's
	// telemetry acceptance policies instead of the default; omitted when unset.
	TelemetryAcceptancePolicyID string `json:"telemetry_acceptance_policy_id,omitempty"`
	// TelemetryBudgetPolicyID sets the run's live telemetry budgets from one
	// of its project's telemetry budget policies; omitted when unset.
	TelemetryBudgetPolicyID string `json:"telemetry_budget_policy_id,omitempty"`
	// AnalysisPolicySelections pins immutable browser and journey policy
	// revisions for the run instead of its project defaults; omitted when unset.
	AnalysisPolicySelections *AnalysisPolicySelections `json:"analysis_policy_selections,omitempty"`
}

// TestStatus is the managed-run projection consumed by first-party clients.
// Duration is the server's nanosecond duration, not its human-readable label.
type TestStatus struct {
	ID          string        `json:"id"`
	RefKey      string        `json:"ref_key,omitempty"`
	Name        string        `json:"name"`
	Status      string        `json:"status"`
	ProjectID   string        `json:"project_id,omitempty"`
	Duration    time.Duration `json:"duration_ns"`
	LiveMetrics *LiveMetrics  `json:"live_metrics,omitempty"`
	// TelemetryAcceptancePolicyID echoes the run's chosen telemetry
	// acceptance policy; a controller that predates the choice omits it.
	TelemetryAcceptancePolicyID string `json:"telemetry_acceptance_policy_id,omitempty"`
	// TelemetryBudgetPolicyID echoes the run's chosen telemetry budget
	// policy; a controller that predates the choice omits it.
	TelemetryBudgetPolicyID string `json:"telemetry_budget_policy_id,omitempty"`
	// AnalysisPolicySelections echoes the run's chosen browser and journey
	// policy revisions; a controller that predates the choice omits it.
	AnalysisPolicySelections *AnalysisPolicySelections `json:"analysis_policy_selections,omitempty"`
}

// LiveMetrics contains the metrics used by run/wait clients. Extra server
// metrics remain additive and are deliberately not re-emitted by this view.
type LiveMetrics struct {
	TotalTx      int64           `json:"total_transactions"`
	TotalErrors  int64           `json:"total_errors"`
	TPS          float64         `json:"tps"`
	HTTPVersions *HTTPVersionMix `json:"http_versions,omitempty"`
}

// HTTPVersionMix retains the wire's embedded version counts; ratios are
// calculated from these counts by clients rather than copied from findings.
type HTTPVersionMix struct{ protocol.HTTPVersionMix }

func IsTerminalTestStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}
