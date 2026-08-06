package paramize

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// consolidatedDataSourceName is the base name of the inline CSV data source
// ApplySuggestions materializes for accepted data-param suggestions.
const consolidatedDataSourceName = "imported_params"

// dataParamConfidence is intentionally below the auto-apply threshold: name
// heuristics alone cannot prove a value is per-VUser test data, so these
// always start unchecked in the review UI.
const dataParamConfidence = 0.6

var (
	// The leading alternation anchors each pair to a real separator so a key is
	// never harvested out of the middle of a longer token. `^` is matched in
	// multi-line mode and whitespace is accepted as a separator because bodies
	// are not always a bare query string starting at offset 0. `;` is a pair
	// separator, so it is excluded from the value class as well.
	queryPairPattern  = regexp.MustCompile(`(?m)(?:^|[?&;\s])([A-Za-z0-9_.\-\[\]]+)=([^&;#\s"']+)`)
	jsonStringPattern = regexp.MustCompile(`"([A-Za-z0-9_.\-]+)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	jsonNumberPattern = regexp.MustCompile(`"([A-Za-z0-9_.\-]+)"\s*:\s*(-?\d+(?:\.\d+)?)`)
)

// dataParamSuggestions proposes binding literal business values (ids, emails,
// SKUs, ...) to a generated data source. claimed lists values already owned by
// the correlation or secret passes, which take precedence.
func dataParamSuggestions(script *dsl.Script, names *nameRegistry, claimed map[string]bool) ([]Suggestion, error) {
	collector := newDataParamCollector(names, claimed)
	budget := &suggestionRegexBudget{}
	var scanErr error
	forEachAction(script, func(section string, index int, action *dsl.Action) {
		if scanErr != nil {
			return
		}
		scanErr = collectQueryParams(collector, budget, section, index, action)
		if scanErr != nil {
			return
		}
		scanErr = collectBodyParams(collector, budget, section, index, action)
		if scanErr != nil {
			return
		}
		collectFillParams(collector, section, index, action)
		collectUploadParams(collector, section, index, action)
		collectMessagingParams(collector, section, index, action)
	})
	if scanErr != nil {
		return nil, scanErr
	}
	return collector.suggestions(), nil
}

// messagingParamKeys are per-message identifier params carried in action.Params
// on kafka_*/jms_*/eventhubs_* actions. Binding them to a data source gives each
// VUser a distinct value instead of replaying one recorded literal — a fixed
// correlation_id or key across all VUsers cross-matches replies and skews
// partitioning. Structural params (destination_type, timeout_ms, consumer_group,
// partition, offset, subscription, ...) are intentionally excluded.
var messagingParamKeys = []string{"correlation_id", "key", "partition_key", "message_id"}

// collectMessagingParams surfaces hardcoded per-message identifiers in a
// messaging action's Params map. The generic data-param scanners only look at
// target/body/value/headers, so without this pass these values are invisible to
// parameterization (action.Params is where kafka/jms carry them).
func collectMessagingParams(collector *dataParamCollector, section string, index int, action *dsl.Action) {
	if !isMessagingActionType(action.Type) || len(action.Params) == 0 {
		return
	}
	for _, key := range messagingParamKeys {
		raw, ok := action.Params[key]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok || value == "" || isTemplated(value) {
			continue
		}
		collector.add(key, value, "message "+key, section, index, action,
			"params."+key, value, func(template string) string { return template })
	}
}

func isMessagingActionType(actionType string) bool {
	return strings.HasPrefix(actionType, "kafka_") ||
		strings.HasPrefix(actionType, "jms_") ||
		strings.HasPrefix(actionType, "eventhubs_")
}

func collectQueryParams(collector *dataParamCollector, budget *suggestionRegexBudget, section string, index int, action *dsl.Action) error {
	queryStart := strings.Index(action.Target, "?")
	if queryStart < 0 {
		return nil
	}
	query := action.Target[queryStart+1:]
	matches, err := budget.findAllSubmatchIndexes(queryPairPattern, query)
	if err != nil {
		return err
	}
	for _, match := range matches {
		keyStart, keyEnd := match[2], match[3]
		valueStart, valueEnd := match[4], match[5]
		key, value := query[keyStart:keyEnd], query[valueStart:valueEnd]
		if !LooksParameterName(key) || value == "" || isTemplated(value) {
			continue
		}
		search, buildReplace := delimitedPairReplacement(action.Target, queryStart+1+match[2], queryStart+1+match[1], key)
		collector.add(key, value, "query parameter", section, index, action,
			"target", search, buildReplace)
	}
	return nil
}

func collectBodyParams(collector *dataParamCollector, budget *suggestionRegexBudget, section string, index int, action *dsl.Action) error {
	body := strings.TrimSpace(action.Body)
	if body == "" {
		return nil
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		stringMatches, err := budget.findAllSubmatches(jsonStringPattern, action.Body)
		if err != nil {
			return err
		}
		for _, match := range stringMatches {
			key, value := match[1], match[2]
			if !LooksParameterName(key) || value == "" || isTemplated(value) {
				continue
			}
			collector.add(key, value, "JSON body field", section, index, action,
				"body", match[0], func(template string) string {
					return strings.Replace(match[0], `"`+value+`"`, `"`+template+`"`, 1)
				})
		}
		numberMatches, err := budget.findAllSubmatches(jsonNumberPattern, action.Body)
		if err != nil {
			return err
		}
		for _, match := range numberMatches {
			key, value := match[1], match[2]
			if !LooksParameterName(key) {
				continue
			}
			collector.add(key, value, "JSON body field", section, index, action,
				"body", match[0], func(template string) string {
					// Numbers stay unquoted so template expansion preserves the
					// JSON type when the data column holds a numeric value. The value
					// is the trailing token of match[0] (`"key": <number>`), so splice
					// at its last occurrence — a plain Replace of the bare number can
					// hit digits inside the key (e.g. order1_id with value 1).
					cut := strings.LastIndex(match[0], value)
					if cut < 0 {
						return match[0]
					}
					return match[0][:cut] + template + match[0][cut+len(value):]
				})
		}
		return nil
	}
	// Form-encoded bodies reuse the query-pair shape.
	matches, err := budget.findAllSubmatchIndexes(queryPairPattern, action.Body)
	if err != nil {
		return err
	}
	for _, match := range matches {
		key, value := action.Body[match[2]:match[3]], action.Body[match[4]:match[5]]
		if !LooksParameterName(key) || value == "" || isTemplated(value) {
			continue
		}
		search, buildReplace := delimitedPairReplacement(action.Body, match[2], match[1], key)
		collector.add(key, value, "form field", section, index, action,
			"body", search, buildReplace)
	}
	return nil
}

func delimitedPairReplacement(value string, pairStart, pairEnd int, key string) (string, func(string) string) {
	searchStart := pairStart
	if searchStart > 0 && (value[searchStart-1] == '?' || value[searchStart-1] == '&' || value[searchStart-1] == ';') {
		searchStart--
	}
	searchEnd := pairEnd
	if searchEnd < len(value) && (value[searchEnd] == '&' || value[searchEnd] == ';' || value[searchEnd] == '#') {
		searchEnd++
	}
	prefix := value[searchStart:pairStart]
	suffix := value[pairEnd:searchEnd]
	search := value[searchStart:searchEnd]
	return search, func(template string) string {
		return prefix + key + "=" + template + suffix
	}
}

// collectFillParams treats browser fill values as parameterizable when the
// selector names a business-data field (e.g. #user_email).
func collectFillParams(collector *dataParamCollector, section string, index int, action *dsl.Action) {
	if action.Type != "browser_fill" || strings.TrimSpace(action.Value) == "" || isTemplated(action.Value) {
		return
	}
	hint := SelectorFieldHint(action.Selector)
	if hint == "" || !LooksParameterName(hint) || strings.Contains(strings.ToLower(hint), "password") {
		return
	}
	collector.add(hint, action.Value, "browser fill value", section, index, action,
		"value", action.Value, func(template string) string { return template })
}

func collectUploadParams(collector *dataParamCollector, section string, index int, action *dsl.Action) {
	if action.Type != "browser_set_files" || strings.TrimSpace(action.Value) == "" || isTemplated(action.Value) {
		return
	}
	if action.Metadata == nil || action.Metadata["source_upload_fixture"] != true {
		return
	}
	key := SelectorFieldHint(action.Selector)
	if key == "" || !LooksParameterName(key) {
		key = "upload_file"
	}
	collector.add(key, action.Value, "browser upload fixture", section, index, action,
		"value", action.Value, func(template string) string { return template })
}

// dataParamCollector groups occurrences of the same (key, value) pair under a
// single suggestion that replaces every occurrence when applied.
type dataParamCollector struct {
	names   *nameRegistry
	claimed map[string]bool
	order   []string
	grouped map[string]*Suggestion
	seen    map[string]bool // location+search dedupe (regex scans can overlap)
}

func newDataParamCollector(names *nameRegistry, claimed map[string]bool) *dataParamCollector {
	return &dataParamCollector{
		names:   names,
		claimed: claimed,
		grouped: map[string]*Suggestion{},
		seen:    map[string]bool{},
	}
}

func (c *dataParamCollector) add(key, value, kind, section string, index int, action *dsl.Action, field, search string, buildReplace func(template string) string) {
	if c.claimed[value] {
		return
	}
	groupKey := strings.ToLower(key) + "|" + value
	locationKey := fmt.Sprintf("%s|%s|%d|%s|%s", groupKey, section, index, field, search)
	if c.seen[locationKey] {
		return
	}
	c.seen[locationKey] = true

	if existing, ok := c.grouped[groupKey]; ok {
		existing.Locations = append(existing.Locations, Location{
			Section:     section,
			ActionIndex: index,
			ActionName:  action.Name,
			Field:       field,
			SearchText:  search,
			ReplaceText: buildReplace(existing.Replacement),
		})
		return
	}

	varName := c.names.claim(key)
	template := "{{var." + varName + "}}"
	c.grouped[groupKey] = &Suggestion{
		Kind:         KindDataParam,
		VarName:      varName,
		Replacement:  template,
		LiteralValue: value,
		SampleValue:  truncateValue(value),
		Locations: []Location{{
			Section:     section,
			ActionIndex: index,
			ActionName:  action.Name,
			Field:       field,
			SearchText:  search,
			ReplaceText: buildReplace(template),
		}},
		Confidence: dataParamConfidence,
		RuleID:     "data_param.name_heuristic",
		Rationale: fmt.Sprintf(
			"The %s %q looks like per-VUser test data. Bind it to a data source column so each virtual user sends distinct values instead of the recorded literal %q.",
			kind, key, truncateValue(value)),
		Parameter: &dsl.Parameter{
			Name:       varName,
			Type:       "file",
			DataSource: consolidatedDataSourceName,
			Column:     varName,
		},
	}
	c.order = append(c.order, groupKey)
}

func (c *dataParamCollector) suggestions() []Suggestion {
	out := make([]Suggestion, 0, len(c.order))
	for _, key := range c.order {
		out = append(out, *c.grouped[key])
	}
	return out
}
