package api

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type AIGuardrailReport struct {
	Status       string            `json:"status"`
	StatusCounts map[string]int    `json:"status_counts"`
	Items        []AIGuardrailItem `json:"items"`
}

type AIGuardrailItem struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

func buildAIGuardrailReport(script *dsl.Script, structurallyValid bool) *AIGuardrailReport {
	if !isAIGeneratedDraft(script) {
		return nil
	}
	items := []AIGuardrailItem{
		aiGuardrailItem("provenance", hasAIProvenance(script), "AI source provenance is attached.", "AI-generated drafts must declare generated_by/generated_from or import source metadata."),
		aiGuardrailItem("validation", structurallyValid, "Draft passed structural validation.", "AI-generated drafts must pass structural validation before promotion."),
		aiGuardrailItem("diff_review", preservedBool(script, "diff_reviewed") || preservedBool(script, "review_approved"), "Diff review evidence is attached.", "AI-generated drafts require human diff-review evidence before save handoff."),
		aiGuardrailItem("replay_before_save", preservedBool(script, "replay_validated") || preservedString(script, "replay_status") == "passed", "Replay-before-save evidence is attached.", "AI-generated drafts require replay evidence before promotion."),
	}
	counts := map[string]int{"ready": 0, "needs_review": 0, "blocked": 0}
	for _, item := range items {
		counts[item.Status]++
	}
	status := "ready"
	if counts["blocked"] > 0 {
		status = "blocked"
	} else if counts["needs_review"] > 0 {
		status = "needs_review"
	}
	return &AIGuardrailReport{Status: status, StatusCounts: counts, Items: items}
}

func aiGuardrailItem(key string, pass bool, passSummary string, failSummary string) AIGuardrailItem {
	if pass {
		return AIGuardrailItem{Key: key, Status: "ready", Summary: passSummary}
	}
	return AIGuardrailItem{Key: key, Status: "blocked", Summary: failSummary}
}

func isAIGeneratedDraft(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	generatedBy := strings.ToLower(strings.TrimSpace(script.GeneratedBy))
	generatedFrom := strings.ToLower(strings.TrimSpace(script.GeneratedFrom))
	if strings.Contains(generatedBy, "ai") || strings.Contains(generatedBy, "llm") || strings.Contains(generatedBy, "openai") || generatedFrom == "ai_generated" {
		return true
	}
	if script.Metadata != nil && script.Metadata.Import != nil {
		source := strings.ToLower(strings.TrimSpace(script.Metadata.Import.Source))
		return source == "ai_generated" || source == "ai" || strings.Contains(source, "llm")
	}
	return false
}

func hasAIProvenance(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	if strings.TrimSpace(script.GeneratedBy) != "" && strings.TrimSpace(script.GeneratedFrom) != "" {
		return true
	}
	return script.Metadata != nil && script.Metadata.Import != nil && strings.TrimSpace(script.Metadata.Import.Source) != ""
}

func preservedBool(script *dsl.Script, key string) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	value, ok := script.Metadata.Import.Preserved[key]
	if !ok {
		return false
	}
	boolValue, ok := value.(bool)
	if ok {
		return boolValue
	}
	stringValue, ok := value.(string)
	return ok && strings.EqualFold(strings.TrimSpace(stringValue), "true")
}

func preservedString(script *dsl.Script, key string) string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return ""
	}
	value, ok := script.Metadata.Import.Preserved[key]
	if !ok {
		return ""
	}
	stringValue, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(stringValue))
}
