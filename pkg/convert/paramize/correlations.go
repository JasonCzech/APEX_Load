package paramize

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// correlationSuggestions runs the shared correlation engine over the supplied
// observations and proposes an extractor at the source action plus literal
// replacements at every action that reuses the value.
func correlationSuggestions(script *dsl.Script, observations []correlate.Observation, names *nameRegistry, zeroBasedIDs bool) []Suggestion {
	if len(observations) < 2 {
		return nil
	}
	detected := correlate.Detect(observations)
	suggestions := make([]Suggestion, 0, len(detected))
	for _, corr := range detected {
		sourceIndex := observationActionIndex(corr.FoundInID, zeroBasedIDs)
		sourceAction := actionAt(script, "actions", sourceIndex)
		if sourceAction == nil || corr.Value == "" {
			continue
		}
		varName := names.claim(corr.Name)
		template := "{{var." + varName + "}}"

		locations := make([]Location, 0, len(corr.UsedInIDs))
		for _, usedID := range corr.UsedInIDs {
			usedIndex := observationActionIndex(usedID, zeroBasedIDs)
			action := actionAt(script, "actions", usedIndex)
			if action == nil {
				continue
			}
			locations = append(locations, correlationLocations(action, usedIndex, corr.Value, template)...)
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
				ActionIndex: sourceIndex,
				ActionName:  sourceAction.Name,
			},
		})
	}
	return suggestions
}

func observationActionIndex(observationID int, zeroBasedIDs bool) int {
	if zeroBasedIDs {
		return observationID
	}
	return observationID - 1
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
	for _, headerKey := range sortedKeys(action.Headers) {
		appendLocation("headers."+headerKey, action.Headers[headerKey])
	}
	// Messaging actions carry correlatable values (correlation_id, key, ...) in
	// Params rather than Headers; scan string-valued params so a correlation can
	// be located and replaced there too.
	for _, paramKey := range sortedAnyKeys(action.Params) {
		if str, ok := action.Params[paramKey].(string); ok {
			appendLocation("params."+paramKey, str)
		}
	}
	return locations
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
