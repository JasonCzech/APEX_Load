package api

import (
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
)

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

// buildMultiScriptParameterization merges suggestions across a multi-script
// import (JMeter thread groups). Suggestion locations stay scoped to their own
// script via the per-script apply flow, so the merged result is summary-level
// advice; each script re-runs Suggest at apply time.
func buildMultiScriptParameterization(scripts []*dsl.Script) *paramize.Result {
	merged := paramize.Result{Suggestions: []paramize.Suggestion{}}
	for _, script := range scripts {
		if script == nil {
			continue
		}
		uniquify.DeduplicateActionNames(script)
		result := paramize.Suggest(script, paramize.Options{
			EnableDataParams: true,
			EnableSecrets:    true,
		})
		for i := range result.Suggestions {
			result.Suggestions[i].ScriptName = script.Name
		}
		merged.Suggestions = append(merged.Suggestions, result.Suggestions...)
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
		preApplied = append(preApplied, paramize.PreAppliedCorrelation{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			SampleValue: corr.SampleValue,
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
			SampleValue: corr.SampleValue,
			FoundInStep: corr.FoundInStep,
			UsedInSteps: corr.UsedInSteps,
		})
	}
	return preApplied
}
