package paramize

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// correlationSuggestions runs the shared correlation engine over the supplied
// observations and proposes an extractor at the source action plus literal
// replacements at every action that reuses the value.
func correlationSuggestions(script *dsl.Script, observations []correlate.Observation, names *nameRegistry, zeroBasedIDs bool) ([]Suggestion, error) {
	if len(observations) < 2 {
		return nil, nil
	}
	detected, err := correlate.DetectChecked(observations)
	if err != nil {
		return nil, err
	}
	observationsByID := make(map[int]correlate.Observation, len(observations))
	for _, observation := range observations {
		observationsByID[observation.ID] = observation
	}
	encodingValidator := correlate.NewExactQueryEncodingValidator()
	suggestions := make([]Suggestion, 0, len(detected))
	sourceExtractionBytes := make(map[int]int)
	for _, corr := range detected {
		sourceIndex := observationActionIndex(corr.FoundInID, zeroBasedIDs)
		sourceAction := actionAt(script, "actions", sourceIndex)
		if sourceAction == nil || corr.Value == "" {
			continue
		}

		rawLocations := make([]Location, 0, len(corr.UsedInIDs))
		encodedLocations := make([]Location, 0, len(corr.UsedInIDs))
		unsupportedUsage := false
		for _, usedID := range corr.UsedInIDs {
			usedIndex := observationActionIndex(usedID, zeroBasedIDs)
			action := actionAt(script, "actions", usedIndex)
			if action == nil {
				continue
			}
			observation := observationsByID[usedID]
			locations := correlationLocations(action, usedIndex, corr.Value, observation.RequestBodyTransformed)
			rawLocations = append(rawLocations, locations.raw...)
			encodedLocations = append(encodedLocations, locations.encoded...)
			// Detection reports reuse at observation granularity, not by request
			// field. When that observation contains a transformed body, the same
			// value may also be embedded in wire bytes that cannot be safely
			// rewritten in the encoded DSL body. Suppress the whole executable
			// suggestion instead of repairing an editable URL/header occurrence
			// while silently leaving the transformed consumer stale.
			unsupportedUsage = unsupportedUsage || locations.unsupported || observation.RequestBodyTransformed
		}
		if unsupportedUsage {
			// A partial executable suggestion is unsafe: applying the raw/canonical
			// locations while leaving a path, %20, lowercase-escape, or unescaped-plus
			// consumer stale would make the review look complete when it is not.
			continue
		}
		queryEncoded := correlate.QueryEncodedFormOf(corr.Value)
		if len(encodedLocations) > 0 {
			if !correlate.CorrelationExtractionValueFits(corr.Name+"_encoded", queryEncoded) {
				// The HTTP runtime stores extraction keys and values in one
				// bounded result map. A suggestion whose canonical encoded
				// sibling cannot fit would only create a script that fails at
				// replay. Suppress the entire correlation rather than emitting
				// only its raw half and leaving an encoded consumer stale.
				continue
			}
		}
		if len(encodedLocations) > 0 && strings.EqualFold(strings.TrimSpace(corr.Convert), "url_decode") {
			// A url_decode candidate stores the decoded comparison value, but its
			// source extractor captures wire bytes. The encoded sibling deliberately
			// removes url_decode, so it is executable only when those captured bytes
			// are exactly the canonical QueryEscape spelling it will replace. A mere
			// decoded equality (%20 versus '+', lower-case escapes, and so on) would
			// silently change the request on replay.
			source, ok := observationsByID[corr.FoundInID]
			if !ok || queryEncoded == "" || !encodingValidator.Matches(corr.FoundInID, source.ResponseBody, source.ResponseHeaders, corr, queryEncoded) {
				if encodingValidator.Exhausted() {
					return nil, fmt.Errorf("%w: exact encoded-source validation exhausted its budget", correlate.ErrCorrelationWorkLimit)
				}
				continue
			}
		}
		if len(rawLocations) == 0 && len(encodedLocations) == 0 {
			continue
		}

		baseRationale := fmt.Sprintf(
			"The value %q is produced by %q and reused in %d later action(s). Without an extractor the replay sends a stale recorded value.",
			truncateValue(corr.Value), sourceAction.Name, len(corr.UsedInIDs))
		if len(corr.Reasons) > 0 {
			baseRationale += " " + strings.Join(corr.Reasons, "; ") + "."
		}

		varName := names.claim(corr.Name)
		makeSuggestion := func(name, convert, rationale string, locations []Location) Suggestion {
			replacement := "{{var." + name + "}}"
			for i := range locations {
				locations[i].ReplaceText = replacement
			}
			return Suggestion{
				Kind:         KindCorrelation,
				VarName:      name,
				Replacement:  replacement,
				LiteralValue: corr.Value,
				SampleValue:  corr.SampleValue,
				Locations:    locations,
				Confidence:   corr.Confidence,
				RuleID:       corr.RuleID,
				Rationale:    rationale,
				Extractor: &dsl.Extractor{
					Name:       name,
					Source:     corr.Source,
					Type:       corr.ExtractType,
					Expression: corr.Expression,
					OnMiss:     "fail",
					Convert:    convert,
				},
				FoundInAction: &Location{
					Section:     "actions",
					ActionIndex: sourceIndex,
					ActionName:  sourceAction.Name,
				},
			}
		}

		encodedName := varName
		if len(rawLocations) > 0 && len(encodedLocations) > 0 {
			encodedName = names.claim(varName + "_encoded")
		}
		requiredBytes := 0
		if len(rawLocations) > 0 {
			entryBytes, fits := correlate.CorrelationExtractionEntryBytes(varName, corr.Value)
			if !fits {
				continue
			}
			requiredBytes += entryBytes
		}
		if len(encodedLocations) > 0 {
			_, supported := correlate.EncodedReuseExtractorConvert(corr.Convert)
			entryBytes, fits := correlate.CorrelationExtractionEntryBytes(encodedName, queryEncoded)
			if !supported || !fits {
				continue
			}
			requiredBytes += entryBytes
		}
		// Raw and encoded consumers are two halves of one reviewed correlation.
		// Reserve their aggregate output atomically: emitting only the first half
		// when the sibling no longer fits leaves a stale consumer while making
		// the repair look executable.
		if requiredBytes > protocol.MaxExtractionBytes ||
			sourceExtractionBytes[sourceIndex] > protocol.MaxExtractionBytes-requiredBytes {
			continue
		}
		sourceExtractionBytes[sourceIndex] += requiredBytes
		if len(rawLocations) > 0 {
			suggestions = append(suggestions, makeSuggestion(varName, corr.Convert, baseRationale, rawLocations))
		}
		if len(encodedLocations) > 0 {
			encodedConvert, _ := correlate.EncodedReuseExtractorConvert(corr.Convert)
			encodedRationale := baseRationale + " These occurrences are URL/form encoded, so the extractor preserves or produces the encoded spelling before substitution."
			suggestions = append(suggestions, makeSuggestion(encodedName, encodedConvert, encodedRationale, encodedLocations))
		}
	}
	return suggestions, nil
}

func observationActionIndex(observationID int, zeroBasedIDs bool) int {
	if zeroBasedIDs {
		return observationID
	}
	return observationID - 1
}

type correlationLocationSet struct {
	raw         []Location
	encoded     []Location
	unsupported bool
}

// correlationLocations separates raw and URL/form-encoded occurrences. They
// cannot share one variable: inserting a raw value such as "a&b" into an
// encoded query occurrence changes request structure, while using an encoded
// value in a raw JSON/header occurrence changes application data.
func correlationLocations(action *dsl.Action, index int, value string, requestBodyTransformed bool) correlationLocationSet {
	locations := correlationLocationSet{
		raw:     make([]Location, 0, 2),
		encoded: make([]Location, 0, 2),
	}
	encodedRepresentations := correlationEncodedRepresentations(value)
	unsupportedRepresentations := correlationUnsupportedEncodedRepresentations(value)
	seen := make(map[string]struct{})
	appendLocation := func(field, content string) {
		for _, representation := range unsupportedRepresentations {
			if strings.Contains(content, representation) {
				locations.unsupported = true
			}
		}
		appendRepresentation := func(representation string, encoded bool) {
			if !strings.Contains(content, representation) {
				return
			}
			locationField := field
			if strings.HasPrefix(strings.ToLower(field), "headers.") {
				locationField = strings.ToLower(field)
			}
			key := locationField + "\x00" + representation
			if _, exists := seen[key]; exists {
				return
			}
			seen[key] = struct{}{}
			location := Location{
				Section:     "actions",
				ActionIndex: index,
				ActionName:  action.Name,
				Field:       field,
				SearchText:  representation,
			}
			if encoded {
				locations.encoded = append(locations.encoded, location)
			} else {
				locations.raw = append(locations.raw, location)
			}
		}
		for _, representation := range encodedRepresentations {
			appendRepresentation(representation, true)
		}
		contentWithoutEncoded := content
		for _, representation := range encodedRepresentations {
			contentWithoutEncoded = strings.ReplaceAll(contentWithoutEncoded, representation, "")
		}
		if strings.Contains(contentWithoutEncoded, value) {
			appendRepresentation(value, false)
		}
	}
	appendLocation("target", action.Target)
	if !requestBodyTransformed && strings.TrimSpace(action.BodyEncoding) == "" {
		appendLocation("body", action.Body)
	}
	if multipart := action.BodyMultipart; multipart != nil {
		for index, part := range multipart.Parts {
			if !strings.EqualFold(strings.TrimSpace(part.ValueEncoding), "base64") {
				appendLocation(fmt.Sprintf("%s%d", multipartFieldPrefix, index), part.Value)
			}
		}
	}
	appendLocation("value", action.Value)
	for _, headerKey := range sortedKeys(action.Headers) {
		appendLocation("headers."+headerKey, action.Headers[headerKey])
	}
	// HeaderList is the wire-faithful representation for duplicate and ordered
	// headers. Some converters intentionally leave the legacy Headers summary
	// empty, so discovery must scan HeaderList directly; replacement continues
	// to address the logical header name and updates both representations.
	for _, header := range action.HeaderList {
		if strings.TrimSpace(header.Name) == "" {
			continue
		}
		appendLocation("headers."+header.Name, header.Value)
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

// correlationEncodedRepresentations returns only the URL/form spelling that the
// DSL's url_encode conversion produces. Other valid spellings (path escaping,
// %20 spaces, lowercase escapes, or an unescaped plus) cannot be reproduced by
// that conversion and must remain unchanged for review.
func correlationEncodedRepresentations(value string) []string {
	encoded := correlate.QueryEncodedFormOf(value)
	if encoded == "" {
		return nil
	}
	return []string{encoded}
}

func correlationUnsupportedEncodedRepresentations(value string) []string {
	canonical := correlate.QueryEncodedFormOf(value)
	seen := map[string]bool{value: true, canonical: true}
	forms := make([]string, 0, 8)
	appendForm := func(form string) {
		if form == "" || seen[form] {
			return
		}
		seen[form] = true
		forms = append(forms, form)
	}
	for _, form := range correlate.EncodedFormsOf(value) {
		appendForm(form)
		appendForm(lowerPercentEscapes(form))
	}
	appendForm(lowerPercentEscapes(canonical))
	return forms
}

func lowerPercentEscapes(value string) string {
	encoded := []byte(value)
	for index := 0; index+2 < len(encoded); index++ {
		if encoded[index] != '%' {
			continue
		}
		for offset := 1; offset <= 2; offset++ {
			if encoded[index+offset] >= 'A' && encoded[index+offset] <= 'F' {
				encoded[index+offset] += 'a' - 'A'
			}
		}
		index += 2
	}
	return string(encoded)
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
				Convert:    corr.Convert,
			},
		})
	}
	return suggestions
}
