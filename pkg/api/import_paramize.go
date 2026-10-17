package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"encoding/json"

	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"

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
// correlations, which limits the pass to generated-value, data-param and
// secret detection; the sample/HAR importers pass their converter-applied
// correlations so the review panel shows them as already wired in.
var buildImportParameterization = importreview.BuildImportParameterization

// buildTrustedImportParameterization derives the selectable parameterization
// instructions from the disclosure-safe script held by the server. Browser
// supplied suggestion internals are descriptors only and are never replayed as
// mutation instructions. Only data parameters and generated values are
// selectable. Credential-shaped and redacted values are excluded; those are
// handled exclusively by the guided Credentials workflow.
var buildTrustedImportParameterization = importreview.BuildTrustedImportParameterization

var parameterizationSuggestionContainsMaskedSecret = importreview.ParameterizationSuggestionContainsMaskedSecret

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
			EnableGenerated:      true,
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

var paramizeSummary = importreview.ParamizeSummary

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
var matchesTrustedAuraDescriptor = importreview.MatchesTrustedAuraDescriptor

var cloneParameterizationLocations = importreview.CloneParameterizationLocations

// Credential projection can remove a JSON-as-string boundary outside the
// selected business route. Re-derive the private full shape from the attested
// runnable target while retaining the already-validated safe public binding.
var rebindTrustedAuraParameterization = importreview.RebindTrustedAuraParameterization
