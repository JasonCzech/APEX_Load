package api

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *Server) buildAIExplainResponse(req AIExplainRequest, provider string, projectID string) (AIExplainResponse, error) {
	targetType := normalizeAITargetType(req.TargetType)
	if targetType == "" {
		return AIExplainResponse{}, fmt.Errorf("target_type is required")
	}
	response := AIExplainResponse{
		SchemaVersion: aiExplainSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Provider:      provider,
		ProjectID:     projectID,
		TargetType:    targetType,
		TargetID:      strings.TrimSpace(req.TargetID),
		Determinism:   aiDeterminism("explain_"+targetType, []string{"target_type", "target_id", "inline_payload"}),
	}
	switch targetType {
	case "script":
		script := req.Script
		if script == nil {
			stored, ok := s.runner.GetScript(strings.TrimSpace(req.TargetID))
			if !ok || stored == nil || stored.Script == nil {
				return response, fmt.Errorf("script not found")
			}
			script = stored.Script
			response.TargetID = stored.ID
			response.ProjectID = firstNonEmpty(response.ProjectID, stored.ProjectID)
		}
		response.Summary, response.Sections, response.Citations, response.NextActions = explainScript(script, response.TargetID)
	case "scenario":
		if req.Scenario != nil {
			response.Summary, response.Sections, response.Citations, response.NextActions = explainScenarioDraft(*req.Scenario)
		} else {
			scenario, ok := s.runner.GetScenario(strings.TrimSpace(req.TargetID))
			if !ok || scenario == nil {
				return response, fmt.Errorf("scenario not found")
			}
			response.TargetID = scenario.ID
			response.ProjectID = firstNonEmpty(response.ProjectID, scenario.ProjectID)
			response.Summary, response.Sections, response.Citations, response.NextActions = explainStoredScenario(scenario)
		}
	case "runtime_settings":
		response.Summary, response.Sections, response.Citations, response.NextActions = explainRuntimeSettings()
	case "report":
		if req.Report != nil {
			response.Summary, response.Sections, response.Citations, response.NextActions = explainRunArchiveReport(req.Report, response.TargetID)
		} else {
			record, ok := s.runner.GetRunArchive(strings.TrimSpace(req.TargetID))
			if !ok {
				return response, fmt.Errorf("run archive not found")
			}
			report, err := buildRunArchiveReport(record)
			if err != nil {
				return response, err
			}
			response.Summary, response.Sections, response.Citations, response.NextActions = explainRunArchiveReport(report, response.TargetID)
		}
	default:
		return response, fmt.Errorf("unsupported target_type %q", req.TargetType)
	}
	response.Limitations = []string{"Explanation is grounded in local APEX Load scripts, scenarios, runtime support matrices, or archived reports only."}
	return response, nil
}

func explainScript(script *dsl.Script, id string) (string, []AIExplainSection, []AICitation, []string) {
	if script == nil {
		return "No script evidence is available.", nil, nil, []string{"Attach a script or target a stored script before requesting explanation."}
	}
	validation := buildScriptValidationResponse(id, script)
	assertions := 0
	extractors := 0
	for _, action := range script.Actions {
		assertions += len(action.Assertions)
		extractors += len(action.Extractors)
	}
	sections := []AIExplainSection{
		{Title: "Protocol", Status: "ready", Summary: fmt.Sprintf("%s script with %d action(s).", firstNonEmpty(script.Protocol, "unknown"), len(script.Actions)), Details: []string{"Base URL: " + firstNonEmpty(script.Config.BaseURL, "(not configured)")}, Evidence: []string{"script.protocol", "script.actions", "script.config.base_url"}},
		{Title: "Validation", Status: validation.AuthoringHealth.Status, Summary: fmt.Sprintf("Authoring health score is %d.", validation.AuthoringHealth.Score), Details: validation.Issues, Evidence: []string{"/api/v1/scripts/validate"}},
		{Title: "Assertions And Correlation", Status: "needs_review", Summary: fmt.Sprintf("%d assertion(s), %d extractor(s), %d data source(s).", assertions, extractors, len(script.DataSources)), Evidence: []string{"script.actions.assertions", "script.actions.extractors", "script.data_sources"}},
	}
	if validation.AIGuardrails != nil {
		sections = append(sections, AIExplainSection{Title: "AI Guardrails", Status: validation.AIGuardrails.Status, Summary: "AI-generated draft guardrails are enforced before promotion.", Evidence: []string{"ai_guardrails", "/api/v1/ai/authoring/contract"}})
	}
	return "This script is a " + firstNonEmpty(script.Protocol, "unknown") + " workflow with backend-computed validation and authoring health evidence.", sections, []AICitation{
		{Label: "Script validation", Path: "/api/v1/scripts/validate"},
		{Label: "Runtime settings report", Path: "/api/v1/scripts/{id}/runtime-settings/report"},
		{Label: "AI contract", Path: "/api/v1/ai/authoring/contract"},
	}, []string{"Validate, replay, review runtime settings, then approve through an authoring session before reuse."}
}

func explainScenarioDraft(req CreateScenarioRequest) (string, []AIExplainSection, []AICitation, []string) {
	totalVUsers := 0
	for _, group := range req.Groups {
		totalVUsers += group.VUsers
	}
	return "This scenario draft coordinates " + strconv.Itoa(len(req.Groups)) + " group(s) for " + firstNonEmpty(req.Duration, "(duration missing)") + ".", []AIExplainSection{
		{Title: "Load Model", Status: "needs_review", Summary: fmt.Sprintf("%d total VUser(s) across %d group(s).", totalVUsers, len(req.Groups)), Evidence: []string{"scenario.groups", "scenario.duration"}},
		{Title: "Validation", Status: "needs_review", Summary: "Run scenario validation to compile load plan, capacity fit, data fit, and runtime settings review.", Evidence: []string{"/api/v1/scenarios/validate-load-model"}},
	}, []AICitation{{Label: "Scenario validation", Path: "/api/v1/scenarios/validate-load-model"}}, []string{"Run scenario validation before save or execution."}
}

func explainStoredScenario(scenario *StoredScenario) (string, []AIExplainSection, []AICitation, []string) {
	totalVUsers := 0
	for _, ref := range scenario.Scripts {
		totalVUsers += ref.VUsers
	}
	return "Scenario " + scenario.Name + " coordinates stored script groups with release-ready validation hooks.", []AIExplainSection{
		{Title: "Composition", Status: "ready", Summary: fmt.Sprintf("%d script group(s), %d total VUser(s).", len(scenario.Scripts), totalVUsers), Evidence: []string{"scenario.scripts"}},
		{Title: "Runtime Settings", Status: "needs_review", Summary: "Runtime settings are explainable through the scenario report endpoint.", Evidence: []string{"/api/v1/scenarios/" + scenario.ID + "/runtime-settings/report"}},
	}, []AICitation{{Label: "Scenario runtime report", Path: "/api/v1/scenarios/" + scenario.ID + "/runtime-settings/report"}}, []string{"Export the runtime settings report for migration or release review."}
}

func explainRuntimeSettings() (string, []AIExplainSection, []AICitation, []string) {
	support := buildRuntimeSettingsSupportResponse(time.Now().UTC())
	counts := support.StatusCounts
	return "APEX Load classifies runtime settings with a code-owned matrix across supported protocols.", []AIExplainSection{
		{Title: "Coverage", Status: "ready", Summary: fmt.Sprintf("%d runtime setting entries across %d protocol family value(s).", len(support.SupportMatrix), len(support.Protocols)), Evidence: []string{"/api/v1/runtime-settings/support-matrix"}},
		{Title: "Risk", Status: "needs_review", Summary: fmt.Sprintf("%d blocked, %d partial, %d compatibility-only entries.", counts["blocked"], counts["partial"], counts["compatibility_only"]), Evidence: []string{"runtime_settings.status_counts"}},
	}, []AICitation{{Label: "Runtime settings support", Path: "/api/v1/runtime-settings/support-matrix"}}, []string{"Review compatibility-only entries before claiming execution parity."}
}

func explainRunArchiveReport(report *RunArchiveReport, testID string) (string, []AIExplainSection, []AICitation, []string) {
	if report == nil {
		return "No report evidence is available.", nil, nil, []string{"Attach an archive report or target a run archive."}
	}
	root := buildRunRootCauseSummary(testID, report)
	return root.Summary, []AIExplainSection{
			{Title: "Outcome", Status: root.Status, Summary: fmt.Sprintf("%d transaction(s), %d error(s), %.2f%% error rate.", report.Overview.Transactions, report.Overview.Errors, report.Overview.ErrorPct), Evidence: []string{"overview.transactions", "overview.errors", "overview.error_pct"}},
			{Title: "Root Cause", Status: root.Status, Summary: root.Summary, Evidence: []string{"/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/root-cause"}},
			{Title: "Completeness", Status: report.Completeness.Status, Summary: fmt.Sprintf("Archive completeness %.0f%%.", report.Completeness.ScorePct), Evidence: []string{"completeness"}},
		}, []AICitation{
			{Label: "Archive report", Path: "/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/report"},
			{Label: "Root cause", Path: "/api/v1/tests/" + strings.TrimSpace(testID) + "/archive/root-cause"},
		}, root.NextActions
}
