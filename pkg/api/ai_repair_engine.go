package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *Server) resolveAIRepairScript(req AIRepairRequest) (*dsl.Script, AIRepairTarget, error) {
	if req.Script != nil {
		target := AIRepairTarget{Type: "draft", Name: req.Script.Name}
		return req.Script, target, nil
	}
	if strings.TrimSpace(req.SessionID) != "" {
		session, err := s.loadAuthoringSession(strings.TrimSpace(req.SessionID))
		if err != nil || session == nil || session.Draft == nil {
			return nil, AIRepairTarget{}, fmt.Errorf("authoring session draft not found")
		}
		return session.Draft, AIRepairTarget{Type: "authoring_session", ID: session.ID, Name: session.Name}, nil
	}
	if strings.TrimSpace(req.ScriptID) != "" {
		stored, ok := s.runner.GetScript(strings.TrimSpace(req.ScriptID))
		if !ok || stored == nil || stored.Script == nil {
			return nil, AIRepairTarget{}, fmt.Errorf("script not found")
		}
		return stored.Script, AIRepairTarget{Type: "script", ID: stored.ID, Name: stored.Name}, nil
	}
	return nil, AIRepairTarget{}, fmt.Errorf("script, script_id, or session_id is required")
}

func buildAIRepairSuggestions(script *dsl.Script, validation scriptValidationResponse, categories []string) []AIRepairSuggestion {
	wanted := aiCategorySet(categories)
	suggestions := []AIRepairSuggestion{}
	add := func(category string, severity string, title string, summary string, action string, evidence []string, previewPath string) {
		if len(wanted) > 0 && !wanted[category] {
			return
		}
		id := fmt.Sprintf("ai-repair-%s-%02d", strings.ReplaceAll(category, "_", "-"), len(suggestions)+1)
		suggestions = append(suggestions, AIRepairSuggestion{
			ID:          id,
			Category:    category,
			Severity:    severity,
			Title:       title,
			Summary:     summary,
			Action:      action,
			Evidence:    evidence,
			PreviewPath: previewPath,
		})
	}
	for _, issue := range validation.Issues {
		add("validation", "blocker", "Fix structural validation issue", issue, "Update the draft DSL so validation passes before replay or promotion.", []string{"/api/v1/scripts/validate"}, "")
	}
	if validation.DataSourceFit != nil {
		for _, item := range validation.DataSourceFit.Items {
			if item.Status == "blocked" || item.Status == "needs_review" {
				add("data_source", severityForStatus(item.Status), "Repair data source fit", item.Summary, "Add the missing data source/column or adjust unique-row usage before high-volume execution.", []string{"data_source_fit"}, "data_sources")
			}
		}
	}
	if script != nil {
		for index, action := range script.Actions {
			path := fmt.Sprintf("actions[%d]", index)
			if len(action.Assertions) == 0 && actionAcceptsResponseAssertions(action) {
				add("assertions", "warning", "Add response assertion", "Action "+action.Name+" has no response assertion.", "Add at least one status_code or content assertion so replay can prove expected behavior.", []string{path + ".assertions"}, path+".assertions")
			}
			if actionNeedsCorrelation(action) {
				add("correlation", "warning", "Add correlation extractor", "Action "+action.Name+" appears to use session/token data without an extractor.", "Capture the dynamic token from an earlier response and replace the hard-coded value with {{var.name}}.", []string{path + ".headers", path + ".body"}, path+".extractors")
			}
			if actionHasHardCodedSensitiveData(action) {
				add("hard_coded_data", "warning", "Parameterize hard-coded sensitive data", "Action "+action.Name+" contains token, session, password, or bearer-like literals.", "Move the value into parameters, data sources, or env variables before sharing or approving.", []string{path + ".headers", path + ".body"}, path)
			}
			if script.Protocol == "browser" && browserActionNeedsWaitRepair(action) {
				add("unstable_waits", "warning", "Add stable browser wait", "Browser action "+action.Name+" has no explicit readiness wait or uses a brittle selector.", "Add a visible/network-idle wait and prefer stable data-testid or role selectors.", []string{path + ".selector", path + ".wait_for"}, path+".wait_for")
			}
			if script.Protocol == "browser" && scriptHasWeb2ImportProfile(script) && browserActionNeedsSelectorRepair(action) {
				add("selector", "warning", "Repair Web 2.0 selector", "Browser action "+action.Name+" needs selector review against imported object descriptors and replay DOM evidence.", "Open the Object Descriptors dialog, choose a stable candidate, and rerun browser replay before approval.", []string{path + ".selector", "metadata.import.preserved.trueclient_archive_inspection"}, path+".selector")
			}
		}
	}
	for _, entry := range validation.RuntimeSettingsParity {
		status := strings.ToLower(string(entry.Status))
		if status == "blocked" || status == "partial" || status == "compatibility_only" {
			add("runtime_parity", severityForStatus(status), "Review runtime setting parity", entry.Concept+" is "+status+".", "Use the runtime settings report to decide whether to remove, replace, or explicitly accept this setting.", []string{"/api/v1/runtime-settings/support-matrix"}, "config")
		}
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		if repairSeverityRank(suggestions[i].Severity) == repairSeverityRank(suggestions[j].Severity) {
			return suggestions[i].ID < suggestions[j].ID
		}
		return repairSeverityRank(suggestions[i].Severity) > repairSeverityRank(suggestions[j].Severity)
	})
	return suggestions
}

func applyAIRepairPreview(script *dsl.Script, suggestions []AIRepairSuggestion) *dsl.Script {
	if script == nil || len(suggestions) == 0 {
		return nil
	}
	preview := cloneScriptDefinition(script)
	for index := range preview.Actions {
		action := &preview.Actions[index]
		if len(action.Assertions) == 0 && actionAcceptsResponseAssertions(*action) {
			action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: 200})
		}
		if preview.Protocol == "browser" && action.WaitFor == nil && strings.TrimSpace(action.Selector) != "" {
			action.WaitFor = &dsl.WaitCondition{Type: "visible", Value: action.Selector, TimeoutMs: 5000}
		}
	}
	attachAIProvenance(preview, defaultAIProvider, "repair", "ai_repair_preview")
	preview.Metadata.Import.Preserved["ai_repair_preview"] = true
	return preview
}

func scriptHasWeb2ImportProfile(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return false
	}
	preserved := script.Metadata.Import.Preserved
	sourceType := strings.ToLower(strings.TrimSpace(stringValue(preserved["source_script_type"])))
	sourceProfile := strings.ToLower(strings.TrimSpace(stringValue(preserved["source_profile"])))
	return strings.Contains(sourceType, "truclient 2.0") || strings.Contains(sourceType, "truclient2") || strings.Contains(sourceProfile, "truclient2_web")
}

func browserActionNeedsSelectorRepair(action dsl.Action) bool {
	switch action.Type {
	case "browser_click", "browser_fill", "browser_select", "browser_tap", "browser_long_press", "browser_swipe":
	default:
		return false
	}
	selector := strings.TrimSpace(firstNonEmpty(action.Selector, action.Target))
	if selector == "" {
		return true
	}
	lower := strings.ToLower(selector)
	return !(strings.Contains(lower, "data-testid") || strings.Contains(lower, "aria-label") || strings.HasPrefix(selector, "#") || strings.Contains(lower, "[role="))
}
