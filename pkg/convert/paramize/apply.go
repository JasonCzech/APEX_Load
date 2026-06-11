package paramize

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Diff records one concrete change ApplySuggestions made, in the same shape as
// the authoring-correlation diffs so the dashboard reuses one renderer.
type Diff struct {
	SuggestionID string `json:"suggestion_id"`
	Kind         string `json:"kind"` // value_replaced | extractor_added | data_source_added | parameter_added
	Path         string `json:"path"`
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
}

// ApplySuggestions returns a new script clone with only the selected
// suggestions applied: literals replaced with templates, extractors appended,
// and a consolidated data source + parameters materialized for accepted
// data-param suggestions. The input script is never mutated. Pre-applied
// suggestions are skipped even when selected.
func ApplySuggestions(script *dsl.Script, suggestions []Suggestion, selected map[string]bool) (*dsl.Script, []Diff, error) {
	if script == nil {
		return nil, nil, fmt.Errorf("script is required")
	}
	clone, err := cloneScript(script)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to clone script: %w", err)
	}

	diffs := make([]Diff, 0)
	dataColumns := make(map[string]string) // column -> seed value
	dataColumnOrder := make([]string, 0)

	for _, suggestion := range suggestions {
		if !selected[suggestion.ID] || suggestion.PreApplied {
			continue
		}
		diffs = append(diffs, applyReplacements(clone, suggestion)...)

		switch suggestion.Kind {
		case KindCorrelation:
			if diff, ok := appendExtractor(clone, suggestion); ok {
				diffs = append(diffs, diff)
			}
		case KindDataParam:
			if suggestion.Parameter == nil {
				continue
			}
			column := suggestion.Parameter.Column
			if _, exists := dataColumns[column]; !exists {
				dataColumns[column] = suggestion.LiteralValue
				dataColumnOrder = append(dataColumnOrder, column)
			}
		}
	}

	if len(dataColumnOrder) > 0 {
		sourceName, sourceDiff := materializeDataSource(clone, dataColumnOrder, dataColumns)
		diffs = append(diffs, sourceDiff)
		diffs = append(diffs, materializeParameters(clone, suggestions, selected, sourceName)...)
	}

	return clone, diffs, nil
}

func applyReplacements(script *dsl.Script, suggestion Suggestion) []Diff {
	diffs := make([]Diff, 0, len(suggestion.Locations))
	for _, location := range suggestion.Locations {
		action := actionAt(script, location.Section, location.ActionIndex)
		if action == nil || location.SearchText == "" {
			continue
		}
		before, after, ok := replaceInField(action, location)
		if !ok {
			continue
		}
		diffs = append(diffs, Diff{
			SuggestionID: suggestion.ID,
			Kind:         "value_replaced",
			Path:         fmt.Sprintf("/%s/%d/%s", location.Section, location.ActionIndex, strings.ReplaceAll(location.Field, ".", "/")),
			Before:       truncateValue(before),
			After:        truncateValue(after),
		})
	}
	return diffs
}

// replaceInField substitutes location.SearchText in the addressed field
// ("target", "body", "value", or "headers.<Name>") and reports the before and
// after values when a replacement happened.
func replaceInField(action *dsl.Action, location Location) (before, after string, ok bool) {
	replace := func(current string) (string, bool) {
		if !strings.Contains(current, location.SearchText) {
			return current, false
		}
		return strings.ReplaceAll(current, location.SearchText, location.ReplaceText), true
	}
	switch location.Field {
	case "target":
		before = action.Target
		if updated, changed := replace(action.Target); changed {
			action.Target = updated
			return before, updated, true
		}
	case "body":
		before = action.Body
		if updated, changed := replace(action.Body); changed {
			action.Body = updated
			return before, updated, true
		}
	case "value":
		before = action.Value
		if updated, changed := replace(action.Value); changed {
			action.Value = updated
			return before, updated, true
		}
	default:
		headerKey, isHeader := strings.CutPrefix(location.Field, "headers.")
		if !isHeader {
			return "", "", false
		}
		current, exists := action.Headers[headerKey]
		if !exists {
			return "", "", false
		}
		if updated, changed := replace(current); changed {
			action.Headers[headerKey] = updated
			return current, updated, true
		}
	}
	return "", "", false
}

func appendExtractor(script *dsl.Script, suggestion Suggestion) (Diff, bool) {
	if suggestion.Extractor == nil || suggestion.FoundInAction == nil {
		return Diff{}, false
	}
	action := actionAt(script, suggestion.FoundInAction.Section, suggestion.FoundInAction.ActionIndex)
	if action == nil {
		return Diff{}, false
	}
	for _, existing := range action.Extractors {
		if existing.Name == suggestion.Extractor.Name {
			return Diff{}, false
		}
	}
	action.Extractors = append(action.Extractors, *suggestion.Extractor)
	return Diff{
		SuggestionID: suggestion.ID,
		Kind:         "extractor_added",
		Path:         fmt.Sprintf("/%s/%d/extractors", suggestion.FoundInAction.Section, suggestion.FoundInAction.ActionIndex),
		After:        fmt.Sprintf("%s (%s %s)", suggestion.Extractor.Name, suggestion.Extractor.Type, suggestion.Extractor.Expression),
	}, true
}

// materializeDataSource creates one inline CSV data source seeded with the
// recorded literals (one row), following the enterprise-archive importer's
// inline data-source pattern. A free name is chosen if the base name is taken.
func materializeDataSource(script *dsl.Script, columnOrder []string, seeds map[string]string) (string, Diff) {
	name := consolidatedDataSourceName
	taken := map[string]bool{}
	for _, source := range script.DataSources {
		taken[source.Name] = true
	}
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s_%d", consolidatedDataSourceName, i)
	}

	values := make([]string, 0, len(columnOrder))
	for _, column := range columnOrder {
		values = append(values, csvEscape(seeds[column]))
	}
	script.DataSources = append(script.DataSources, dsl.DataSource{
		Name:           name,
		Type:           "csv",
		InlineData:     strings.Join(columnOrder, ",") + "\n" + strings.Join(values, ","),
		Strategy:       "sequential",
		FirstRowHeader: true,
	})
	return name, Diff{
		Kind:  "data_source_added",
		Path:  "/data_sources",
		After: name + " (" + strings.Join(columnOrder, ", ") + ")",
	}
}

func materializeParameters(script *dsl.Script, suggestions []Suggestion, selected map[string]bool, sourceName string) []Diff {
	existing := map[string]bool{}
	for _, param := range script.Parameters {
		existing[param.Name] = true
	}
	diffs := make([]Diff, 0)
	for _, suggestion := range suggestions {
		if !selected[suggestion.ID] || suggestion.Kind != KindDataParam || suggestion.Parameter == nil {
			continue
		}
		if existing[suggestion.Parameter.Name] {
			continue
		}
		param := *suggestion.Parameter
		param.DataSource = sourceName
		script.Parameters = append(script.Parameters, param)
		existing[param.Name] = true
		diffs = append(diffs, Diff{
			SuggestionID: suggestion.ID,
			Kind:         "parameter_added",
			Path:         "/parameters",
			After:        fmt.Sprintf("%s -> %s.%s", param.Name, sourceName, param.Column),
		})
	}
	sort.SliceStable(diffs, func(i, j int) bool { return diffs[i].After < diffs[j].After })
	return diffs
}

func csvEscape(value string) string {
	if strings.ContainsAny(value, ",\"\n") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}

func cloneScript(in *dsl.Script) (*dsl.Script, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out dsl.Script
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
