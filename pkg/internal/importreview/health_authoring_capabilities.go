package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type AuthoringHealth struct {
	Score  int                   `json:"score"`
	Status string                `json:"status"`
	Inputs []AuthoringHealthItem `json:"inputs"`
}

type AuthoringHealthItem struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Count   int    `json:"count,omitempty"`
	Summary string `json:"summary"`
}

func BuildStoredScriptAuthoringHealth(script *dsl.Script) *AuthoringHealth {
	if script == nil {
		return nil
	}
	health := BuildScriptValidationResponse(script.ID, script).AuthoringHealth
	return &health
}

func BuildAuthoringHealth(issues []string, warnings []string, parityCounts map[string]int, dataSourceFit *DataSourceFitReport, aiGuardrails *AIGuardrailReport, scriptHasProvenance bool, replayBlockers ...string) AuthoringHealth {
	score := 100
	inputs := []AuthoringHealthItem{
		{Key: "validation_issues", Status: "pass", Count: len(issues), Summary: "Script validation passed."},
		{Key: "warnings", Status: "pass", Count: len(warnings), Summary: "No validation warnings."},
		{Key: "runtime_parity", Status: "pass", Summary: "No runtime parity blockers were detected."},
		{Key: "data_source_fit", Status: "pass", Summary: "Data sources match referenced parameters and load-model needs."},
		{Key: "ai_guardrails", Status: "pass", Summary: "No AI-generation guardrails apply to this draft."},
		{Key: "replay_readiness", Status: "pass", Summary: "Draft is structurally ready for replay."},
		{Key: "provenance", Status: "info", Summary: "No source provenance is required for manually authored drafts."},
	}
	if len(issues) > 0 {
		score -= 45
		inputs[0] = AuthoringHealthItem{Key: "validation_issues", Status: "blocker", Count: len(issues), Summary: "Script has validation issues that block save or replay."}
		inputs[5] = AuthoringHealthItem{Key: "replay_readiness", Status: "blocker", Summary: "Replay should wait until validation blockers are fixed."}
	}
	if len(warnings) > 0 {
		score -= MinInt(15, len(warnings)*5)
		inputs[1] = AuthoringHealthItem{Key: "warnings", Status: "warning", Count: len(warnings), Summary: "Validation warnings need review before promotion."}
	}
	if len(replayBlockers) > 0 {
		score -= 45
		inputs[5] = AuthoringHealthItem{Key: "replay_readiness", Status: "blocker", Count: len(replayBlockers), Summary: "Replay is blocked until review gates and execution blockers are resolved."}
	}
	parityPenalty := RuntimeParityHealthPenalty(parityCounts)
	if parityPenalty > 0 {
		score -= parityPenalty
		inputs[2] = AuthoringHealthItem{Key: "runtime_parity", Status: "warning", Count: RuntimeParityIssueCount(parityCounts), Summary: "Runtime parity has partial, compatibility-only, or blocked settings."}
	}
	if dataSourceFit != nil {
		blocked := dataSourceFit.StatusCounts["blocked"]
		needsReview := dataSourceFit.StatusCounts["needs_review"]
		if blocked > 0 {
			score -= MinInt(35, blocked*15)
			inputs[3] = AuthoringHealthItem{Key: "data_source_fit", Status: "blocker", Count: blocked, Summary: "Data-source fit checks found missing columns, missing sources, or insufficient unique rows."}
		} else if needsReview > 0 {
			score -= MinInt(15, needsReview*5)
			inputs[3] = AuthoringHealthItem{Key: "data_source_fit", Status: "warning", Count: needsReview, Summary: "Data-source fit checks need review before high-volume execution."}
		}
	}
	if aiGuardrails != nil {
		blocked := aiGuardrails.StatusCounts["blocked"]
		needsReview := aiGuardrails.StatusCounts["needs_review"]
		if blocked > 0 {
			score -= MinInt(35, blocked*10)
			inputs[4] = AuthoringHealthItem{Key: "ai_guardrails", Status: "blocker", Count: blocked, Summary: "AI-generated drafts require provenance, diff review, validation, and replay evidence before promotion."}
		} else if needsReview > 0 {
			score -= MinInt(10, needsReview*5)
			inputs[4] = AuthoringHealthItem{Key: "ai_guardrails", Status: "warning", Count: needsReview, Summary: "AI-generation evidence is present but should be reviewed before promotion."}
		}
	}
	if scriptHasProvenance {
		inputs[6] = AuthoringHealthItem{Key: "provenance", Status: "pass", Summary: "Source provenance is attached to the draft."}
	}
	if score < 0 {
		score = 0
	}
	status := "ready"
	if score < 60 || len(issues) > 0 || len(replayBlockers) > 0 || (dataSourceFit != nil && dataSourceFit.StatusCounts["blocked"] > 0) || (aiGuardrails != nil && aiGuardrails.StatusCounts["blocked"] > 0) {
		status = "blocked"
	} else if score < 85 || len(warnings) > 0 || parityPenalty > 0 || (dataSourceFit != nil && dataSourceFit.StatusCounts["needs_review"] > 0) || (aiGuardrails != nil && aiGuardrails.StatusCounts["needs_review"] > 0) {
		status = "needs_review"
	}
	return AuthoringHealth{Score: score, Status: status, Inputs: inputs}
}

func RuntimeParityHealthPenalty(counts map[string]int) int {
	if len(counts) == 0 {
		return 0
	}
	penalty := 0
	penalty += counts[string(enterprisearchive.RuntimeSettingStatusBlocked)] * 20
	penalty += counts[string(enterprisearchive.RuntimeSettingStatusPartial)] * 5
	penalty += counts[string(enterprisearchive.RuntimeSettingStatusCompatibilityOnly)] * 8
	if penalty > 30 {
		return 30
	}
	return penalty
}

func RuntimeParityIssueCount(counts map[string]int) int {
	if len(counts) == 0 {
		return 0
	}
	return counts[string(enterprisearchive.RuntimeSettingStatusBlocked)] +
		counts[string(enterprisearchive.RuntimeSettingStatusPartial)] +
		counts[string(enterprisearchive.RuntimeSettingStatusCompatibilityOnly)]
}
