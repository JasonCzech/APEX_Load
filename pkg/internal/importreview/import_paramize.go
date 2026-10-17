package importreview

import (
	"bytes"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"strings"
)

func BuildImportParameterization(script *dsl.Script, preApplied []paramize.PreAppliedCorrelation) *paramize.Result {
	// Shared safety net: guarantee case-insensitively-unique action names for
	// every importer before the script is reviewed/persisted, so the dashboard
	// never flags false duplicates. Idempotent for importers (e.g.
	// enterprise-archive) that already uniquify at conversion time.
	uniquify.DeduplicateActionNames(script)
	result := paramize.Suggest(script, paramize.Options{
		PreApplied:       preApplied,
		EnableGenerated:  true,
		EnableDataParams: true,
		EnableSecrets:    true,
	})
	return &result
}

func BuildTrustedImportParameterization(script *dsl.Script) *paramize.Result {
	result := BuildImportParameterization(script, nil)
	trusted := make([]paramize.Suggestion, 0, len(result.Suggestions))
	for _, suggestion := range result.Suggestions {
		if (suggestion.Kind != paramize.KindDataParam && suggestion.Kind != paramize.KindGenerated) || ParameterizationSuggestionContainsMaskedSecret(suggestion) {
			continue
		}
		trusted = append(trusted, suggestion)
	}
	result.Suggestions = trusted
	result.Summary = ParamizeSummary(trusted)
	return result
}

func ParameterizationSuggestionContainsMaskedSecret(suggestion paramize.Suggestion) bool {
	contains := func(value string) bool {
		return strings.Contains(value, MaskedScriptSecret)
	}
	if contains(suggestion.LiteralValue) || contains(suggestion.SampleValue) || contains(suggestion.Replacement) {
		return true
	}
	for _, location := range suggestion.Locations {
		if contains(location.SearchText) || contains(location.ReplaceText) {
			return true
		}
	}
	if suggestion.FoundInAction != nil &&
		(contains(suggestion.FoundInAction.SearchText) || contains(suggestion.FoundInAction.ReplaceText)) {
		return true
	}
	return false
}

func ParamizeSummary(suggestions []paramize.Suggestion) paramize.Summary {
	summary := paramize.Summary{Total: len(suggestions), ByKind: map[paramize.Kind]int{}}
	for _, s := range suggestions {
		summary.ByKind[s.Kind]++
		switch {
		case s.PreApplied:
			summary.PreAppliedCount++
		case s.AutoApply:
			summary.AutoApplyCount++
		default:
			summary.ReviewCount++
		}
		if s.HighRisk {
			summary.HighRiskCount++
		}
	}
	return summary
}

func MatchesTrustedAuraDescriptor(provided, trusted paramize.Suggestion) bool {
	structured := false
	for _, location := range trusted.Locations {
		if len(location.AuraRoute) > 0 {
			structured = true
		}
	}
	for _, location := range provided.Locations {
		if len(location.AuraRoute) > 0 || location.ActionPath != "" || location.PayloadShape != "" {
			structured = true
		}
	}
	if !structured {
		return true
	}
	if provided.Kind != trusted.Kind || provided.VarName != trusted.VarName || provided.Replacement != trusted.Replacement || len(provided.Locations) != len(trusted.Locations) {
		return false
	}
	for i, location := range provided.Locations {
		expected := trusted.Locations[i]
		location.SearchText = ""
		expected.SearchText = ""
		providedJSON, providedErr := json.Marshal(location)
		expectedJSON, expectedErr := json.Marshal(expected)
		if providedErr != nil || expectedErr != nil || !bytes.Equal(providedJSON, expectedJSON) {
			return false
		}
	}
	return true
}

func CloneParameterizationLocations(input []paramize.Location) []paramize.Location {
	output := append([]paramize.Location(nil), input...)
	for i := range output {
		output[i].AuraRoute = append([]aurapayload.Segment(nil), input[i].AuraRoute...)
		output[i].EncodingLayers = append([]string(nil), input[i].EncodingLayers...)
	}
	return output
}

func RebindTrustedAuraParameterization(target *dsl.Script, advice []paramize.Suggestion, selected map[string]bool) ([]paramize.Suggestion, bool) {
	needsRebind := false
	for _, s := range advice {
		if !selected[s.ID] {
			continue
		}
		for _, l := range s.Locations {
			if len(l.AuraRoute) > 0 {
				needsRebind = true
				break
			}
		}
	}
	if !needsRebind {
		return advice, true
	}
	actual := BuildTrustedImportParameterization(target)
	byID := make(map[string]paramize.Suggestion, len(actual.Suggestions))
	for _, s := range actual.Suggestions {
		byID[s.ID] = s
	}
	rebound := append([]paramize.Suggestion(nil), advice...)
	for i, s := range advice {
		if !selected[s.ID] {
			continue
		}
		structured := false
		for _, l := range s.Locations {
			if len(l.AuraRoute) > 0 {
				structured = true
				break
			}
		}
		if !structured {
			continue
		}
		exact, exists := byID[s.ID]
		if !exists || exact.LiteralValue != s.LiteralValue || !MatchesTrustedAuraDescriptor(s, exact) {
			return nil, false
		}
		rebound[i] = exact
	}
	return rebound, true
}
