package api

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// scrubPreAppliedSampleValue redacts a credential-shaped correlation sample
// before it leaves the server in an import response or the ParameterizationReview
// UI. Applied at THIS shared PreAppliedCorrelation boundary so every importer
// (HAR, OpenAPI, Postman, recorder) is covered, not just the HAR converter.
// scrub.Value only redacts secret-shaped values, so non-secret example values
// (the common case for spec importers) pass through unchanged.
func scrubPreAppliedSampleValue(value string) string {
	return scrub.Value(value, scrub.Policy{})
}

// buildImportParameterization is the single shared hook every importer calls
// before writing its response. Spec-only importers pass nil pre-applied
// correlations, which limits the pass to data-param and secret detection; the
// sample/HAR importers pass their converter-applied correlations so the review
// panel shows them as already wired in.
func buildImportParameterization(script *dsl.Script, preApplied []paramize.PreAppliedCorrelation) *paramize.Result {
	// Shared safety net: guarantee case-insensitively-unique action names for
	// every importer before the script is reviewed/persisted, so the dashboard
	// never flags false duplicates. Idempotent for importers (e.g.
	// enterprise-archive) that already uniquify at conversion time.
	uniquify.DeduplicateActionNames(script)
	result := paramize.Suggest(script, paramize.Options{
		PreApplied:       preApplied,
		EnableDataParams: true,
		EnableSecrets:    true,
	})
	return &result
}

// buildTrustedImportParameterization derives the selectable parameterization
// instructions from the disclosure-safe script held by the server. Browser
// supplied suggestion internals are descriptors only and are never replayed as
// mutation instructions. Credential-shaped and redacted values are excluded;
// those are handled exclusively by the guided Credentials workflow.
func buildTrustedImportParameterization(script *dsl.Script) *paramize.Result {
	result := buildImportParameterization(script, nil)
	trusted := make([]paramize.Suggestion, 0, len(result.Suggestions))
	for _, suggestion := range result.Suggestions {
		if suggestion.Kind != paramize.KindDataParam || parameterizationSuggestionContainsMaskedSecret(suggestion) {
			continue
		}
		trusted = append(trusted, suggestion)
	}
	result.Suggestions = trusted
	result.Summary = paramizeSummary(trusted)
	return result
}

func parameterizationSuggestionContainsMaskedSecret(suggestion paramize.Suggestion) bool {
	contains := func(value string) bool {
		return strings.Contains(value, maskedScriptSecret)
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

// buildMultiScriptParameterization merges suggestions across a multi-script
// import (JMeter thread groups). Suggestion locations stay scoped to their own
// script via the per-script apply flow, so the merged result is summary-level
// advice; each script re-runs Suggest at apply time.
func buildMultiScriptParameterization(scripts []*dsl.Script) *paramize.Result {
	merged := paramize.Result{Suggestions: []paramize.Suggestion{}}
	auraBudget := paramize.NewAuraInspectionBudget()
	retainedAuraBytes, retainedAuraCount := 0, 0
	auraDetailsFull := false
	for _, script := range scripts {
		if script == nil {
			continue
		}
		uniquify.DeduplicateActionNames(script)
		result := paramize.Suggest(script, paramize.Options{
			EnableDataParams:     true,
			EnableSecrets:        true,
			AuraInspectionBudget: auraBudget,
		})

		if result.AuraPayload != nil {
			if merged.AuraPayload == nil {
				copy := *result.AuraPayload
				merged.AuraPayload = &copy
			} else {
				dst, src := merged.AuraPayload, result.AuraPayload
				dst.Complete = dst.Complete && src.Complete
				dst.RecognizedBodies += src.RecognizedBodies
				dst.InspectedBodies += src.InspectedBodies
				dst.RejectedBodies += src.RejectedBodies
				dst.OmittedBodies += src.OmittedBodies
				dst.StringFields += src.StringFields
				dst.UnsupportedNumberFields += src.UnsupportedNumberFields
				dst.UnsupportedOtherFields += src.UnsupportedOtherFields
				dst.UnsafePathFields += src.UnsafePathFields
				dst.CandidateFields += src.CandidateFields
				dst.RetainedCandidates += src.RetainedCandidates
				dst.OmittedCandidates += src.OmittedCandidates
			}
		}
		for _, warning := range result.Warnings {
			found := false
			for _, existing := range merged.Warnings {
				if warning == existing {
					found = true
					break
				}
			}
			if !found {
				merged.Warnings = append(merged.Warnings, warning)
			}
		}

		for i := range result.Suggestions {
			suggestion := result.Suggestions[i]
			suggestion.ScriptName = script.Name
			structured := false
			for _, location := range suggestion.Locations {
				if len(location.AuraRoute) > 0 {
					structured = true
					break
				}
			}
			if structured {
				row, marshalErr := json.Marshal(suggestion)
				if auraDetailsFull || marshalErr != nil || retainedAuraCount >= 2048 || len(row) > (256<<10)-retainedAuraBytes {
					auraDetailsFull = true
					if merged.AuraPayload != nil {
						merged.AuraPayload.RetainedCandidates--
						merged.AuraPayload.OmittedCandidates++
						merged.AuraPayload.Complete = false
					}
					continue
				}
				retainedAuraBytes += len(row)
				retainedAuraCount++
			}
			merged.Suggestions = append(merged.Suggestions, suggestion)
		}
	}
	if auraDetailsFull {
		merged.Warnings = append(merged.Warnings, "Aura payload review reached its shared multi-script candidate detail limit; candidate counts include omitted advice.")
	}
	merged.Summary = paramizeSummary(merged.Suggestions)
	return &merged
}

func paramizeSummary(suggestions []paramize.Suggestion) paramize.Summary {
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

// sampleCorrelationsToPreApplied adapts the sample/HAR converter's
// already-applied correlations into the shared pre-applied shape.
func sampleCorrelationsToPreApplied(correlations []sample.Correlation) []paramize.PreAppliedCorrelation {
	preApplied := make([]paramize.PreAppliedCorrelation, 0, len(correlations))
	for _, corr := range correlations {
		if !corr.PreApplied {
			continue
		}
		preApplied = append(preApplied, paramize.PreAppliedCorrelation{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			Convert:     corr.Convert,
			SampleValue: scrubPreAppliedSampleValue(corr.SampleValue),
			FoundInStep: corr.FoundInStep,
			UsedInSteps: corr.UsedInSteps,
		})
	}
	return preApplied
}

func openAPICorrelationsToPreApplied(correlations []openapi.Correlation) []paramize.PreAppliedCorrelation {
	preApplied := make([]paramize.PreAppliedCorrelation, 0, len(correlations))
	for _, corr := range correlations {
		preApplied = append(preApplied, paramize.PreAppliedCorrelation{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			SampleValue: scrubPreAppliedSampleValue(corr.SampleValue),
			FoundInStep: corr.FoundInStep,
			UsedInSteps: corr.UsedInSteps,
		})
	}
	return preApplied
}

func postmanCorrelationsToPreApplied(correlations []postman.Correlation) []paramize.PreAppliedCorrelation {
	preApplied := make([]paramize.PreAppliedCorrelation, 0, len(correlations))
	for _, corr := range correlations {
		preApplied = append(preApplied, paramize.PreAppliedCorrelation{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			SampleValue: scrubPreAppliedSampleValue(corr.SampleValue),
			FoundInStep: corr.FoundInStep,
			UsedInSteps: corr.UsedInSteps,
		})
	}
	return preApplied
}

// Aura browser descriptors cannot change the reviewed structural route. Raw
// expected values stay server-side and are regenerated from the current draft.
func matchesTrustedAuraDescriptor(provided, trusted paramize.Suggestion) bool {
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

func cloneParameterizationLocations(input []paramize.Location) []paramize.Location {
	output := append([]paramize.Location(nil), input...)
	for i := range output {
		output[i].AuraRoute = append([]aurapayload.Segment(nil), input[i].AuraRoute...)
		output[i].EncodingLayers = append([]string(nil), input[i].EncodingLayers...)
	}
	return output
}

// Credential projection can remove a JSON-as-string boundary outside the
// selected business route. Re-derive the private full shape from the attested
// runnable target while retaining the already-validated safe public binding.
func rebindTrustedAuraParameterization(target *dsl.Script, advice []paramize.Suggestion, selected map[string]bool) ([]paramize.Suggestion, bool) {
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
	actual := buildTrustedImportParameterization(target)
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
		if !exists || exact.LiteralValue != s.LiteralValue || !matchesTrustedAuraDescriptor(s, exact) {
			return nil, false
		}
		rebound[i] = exact
	}
	return rebound, true
}
