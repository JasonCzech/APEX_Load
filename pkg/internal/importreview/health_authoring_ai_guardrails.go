package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
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

func BuildAIGuardrailReport(script *dsl.Script, structurallyValid bool) *AIGuardrailReport {
	if !IsAIGeneratedDraft(script) {
		return nil
	}
	items := []AIGuardrailItem{
		AiGuardrailItem("provenance", HasAIProvenance(script), "AI source provenance is attached.", "AI-generated drafts must declare generated_by/generated_from or import source metadata."),
		AiGuardrailItem("validation", structurallyValid, "Draft passed structural validation.", "AI-generated drafts must pass structural validation before promotion."),
		AiGuardrailItem("diff_review", PreservedBool(script, "diff_reviewed") || PreservedBool(script, "review_approved"), "Diff review evidence is attached.", "AI-generated drafts require human diff-review evidence before save handoff."),
		AiGuardrailItem("replay_before_save", PreservedBool(script, "replay_validated") || PreservedString(script, "replay_status") == "passed", "Replay-before-save evidence is attached.", "AI-generated drafts require replay evidence before promotion."),
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

func AiGuardrailItem(key string, pass bool, passSummary string, failSummary string) AIGuardrailItem {
	if pass {
		return AIGuardrailItem{Key: key, Status: "ready", Summary: passSummary}
	}
	return AIGuardrailItem{Key: key, Status: "blocked", Summary: failSummary}
}

func IsAIGeneratedDraft(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	generatedBy := strings.ToLower(strings.TrimSpace(script.GeneratedBy))
	generatedFrom := strings.ToLower(strings.TrimSpace(script.GeneratedFrom))
	if generatedBy == "ai" || generatedBy == "llm" || generatedBy == "openai" ||
		strings.HasPrefix(generatedBy, "apexload-ai-") || generatedFrom == "ai_generated" || generatedFrom == "ai_repaired" {
		return true
	}
	if script.Metadata != nil && script.Metadata.Import != nil {
		source := strings.ToLower(strings.TrimSpace(script.Metadata.Import.Source))
		return source == "ai_generated" || source == "ai_repaired" || source == "ai" || source == "llm" || strings.HasPrefix(source, "apexload-ai-")
	}
	return false
}

func HasAIProvenance(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	if strings.TrimSpace(script.GeneratedBy) != "" && strings.TrimSpace(script.GeneratedFrom) != "" {
		return true
	}
	return script.Metadata != nil && script.Metadata.Import != nil && strings.TrimSpace(script.Metadata.Import.Source) != ""
}

func PreservedBool(script *dsl.Script, key string) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return false
	}
	value, ok := script.Metadata.Import.Preserved[key]
	if !ok {
		return false
	}
	BoolValue, ok := value.(bool)
	if ok {
		return BoolValue
	}
	StringValue, ok := value.(string)
	return ok && strings.EqualFold(strings.TrimSpace(StringValue), "true")
}

func PreservedString(script *dsl.Script, key string) string {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return ""
	}
	value, ok := script.Metadata.Import.Preserved[key]
	if !ok {
		return ""
	}
	StringValue, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(StringValue))
}
