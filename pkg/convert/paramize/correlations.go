package paramize

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// correlationSuggestions runs the shared correlation engine over the supplied
// observations and proposes an extractor at the source action plus literal
// replacements at every action that reuses the value. Observation IDs are
// 1-based indexes into script.Actions.
func correlationSuggestions(script *dsl.Script, observations []correlate.Observation, names *nameRegistry) []Suggestion {
	if len(observations) < 2 {
		return nil
	}
	detected := correlate.Detect(observations)
	suggestions := make([]Suggestion, 0, len(detected))
	for _, corr := range detected {
		sourceAction := actionAt(script, "actions", corr.FoundInID-1)
		if sourceAction == nil || corr.Value == "" {
			continue
		}
		varName := names.claim(corr.Name)
		template := "{{var." + varName + "}}"

		locations := make([]Location, 0, len(corr.UsedInIDs))
		for _, usedID := range corr.UsedInIDs {
			action := actionAt(script, "actions", usedID-1)
			if action == nil {
				continue
			}
			locations = append(locations, correlationLocations(action, usedID-1, corr.Value, template)...)
		}
		if len(locations) == 0 {
			continue
		}

		rationale := fmt.Sprintf(
			"The value %q is produced by %q and reused in %d later action(s). Without an extractor the replay sends a stale recorded value.",
			truncateValue(corr.Value), sourceAction.Name, len(corr.UsedInIDs))
		if len(corr.Reasons) > 0 {
			rationale += " " + strings.Join(corr.Reasons, "; ") + "."
		}

		suggestions = append(suggestions, Suggestion{
			Kind:         KindCorrelation,
			VarName:      varName,
			Replacement:  template,
			LiteralValue: corr.Value,
			SampleValue:  corr.SampleValue,
			Locations:    locations,
			Confidence:   corr.Confidence,
			RuleID:       corr.RuleID,
			Rationale:    rationale,
			Extractor: &dsl.Extractor{
				Name:       varName,
				Source:     corr.Source,
				Type:       corr.ExtractType,
				Expression: corr.Expression,
			},
			FoundInAction: &Location{
				Section:     "actions",
				ActionIndex: corr.FoundInID - 1,
				ActionName:  sourceAction.Name,
			},
		})
	}
	return suggestions
}

// correlationLocations finds every field of an action containing the recorded
// value and emits one replacement location per field.
func correlationLocations(action *dsl.Action, index int, value, template string) []Location {
	locations := make([]Location, 0, 2)
	appendLocation := func(field, content string) {
		if !strings.Contains(content, value) {
			return
		}
		locations = append(locations, Location{
			Section:     "actions",
			ActionIndex: index,
			ActionName:  action.Name,
			Field:       field,
			SearchText:  value,
			ReplaceText: template,
		})
	}
	appendLocation("target", action.Target)
	appendLocation("body", action.Body)
	appendLocation("value", action.Value)
	for headerKey, headerValue := range action.Headers {
		appendLocation("headers."+headerKey, headerValue)
	}
	return locations
}

// preAppliedSuggestions surfaces correlations a converter already wired into
// the generated script (the sample/HAR importers apply correlations
// in-converter). They render as informational rows without checkboxes.
func preAppliedSuggestions(preApplied []PreAppliedCorrelation) []Suggestion {
	suggestions := make([]Suggestion, 0, len(preApplied))
	for _, corr := range preApplied {
		usedIn := make([]string, 0, len(corr.UsedInSteps))
		for _, step := range corr.UsedInSteps {
			usedIn = append(usedIn, fmt.Sprintf("%d", step))
		}
		suggestions = append(suggestions, Suggestion{
			Kind:         KindCorrelation,
			VarName:      corr.Name,
			Replacement:  "{{var." + corr.Name + "}}",
			LiteralValue: corr.SampleValue,
			SampleValue:  truncateValue(corr.SampleValue),
			Confidence:   1,
			RuleID:       "correlation.pre_applied",
			Rationale: fmt.Sprintf(
				"Already applied during conversion: extracted from step %d via %s and substituted in step(s) %s.",
				corr.FoundInStep, corr.Source, strings.Join(usedIn, ", ")),
			PreApplied: true,
			Extractor: &dsl.Extractor{
				Name:       corr.Name,
				Source:     corr.Source,
				Type:       corr.ExtractType,
				Expression: corr.Expression,
			},
		})
	}
	return suggestions
}
