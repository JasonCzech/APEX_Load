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
	queryPairPattern  = regexp.MustCompile(`([A-Za-z0-9_.\-\[\]]+)=([^&#\s"']+)`)
	jsonStringPattern = regexp.MustCompile(`"([A-Za-z0-9_.\-]+)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	jsonNumberPattern = regexp.MustCompile(`"([A-Za-z0-9_.\-]+)"\s*:\s*(-?\d+(?:\.\d+)?)`)
)

// dataParamSuggestions proposes binding literal business values (ids, emails,
// SKUs, ...) to a generated data source. claimed lists values already owned by
// the correlation or secret passes, which take precedence.
func dataParamSuggestions(script *dsl.Script, names *nameRegistry, claimed map[string]bool) []Suggestion {
	collector := newDataParamCollector(names, claimed)
	forEachAction(script, func(section string, index int, action *dsl.Action) {
		collectQueryParams(collector, section, index, action)
		collectBodyParams(collector, section, index, action)
		collectFillParams(collector, section, index, action)
		collectUploadParams(collector, section, index, action)
		collectMessagingParams(collector, section, index, action)
	})
	return collector.suggestions()
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

func collectQueryParams(collector *dataParamCollector, section string, index int, action *dsl.Action) {
	queryStart := strings.Index(action.Target, "?")
	if queryStart < 0 {
		return
	}
	query := action.Target[queryStart+1:]
	for _, match := range queryPairPattern.FindAllStringSubmatch(query, -1) {
		key, value := match[1], match[2]
		if !LooksParameterName(key) || value == "" || isTemplated(value) {
			continue
		}
		collector.add(key, value, "query parameter", section, index, action,
			"target", key+"="+value, func(template string) string { return key + "=" + template })
	}
}

func collectBodyParams(collector *dataParamCollector, section string, index int, action *dsl.Action) {
	body := strings.TrimSpace(action.Body)
	if body == "" {
		return
	}
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		for _, match := range jsonStringPattern.FindAllStringSubmatch(action.Body, -1) {
			key, value := match[1], match[2]
			if !LooksParameterName(key) || value == "" || isTemplated(value) {
				continue
			}
			collector.add(key, value, "JSON body field", section, index, action,
				"body", match[0], func(template string) string {
					return strings.Replace(match[0], `"`+value+`"`, `"`+template+`"`, 1)
				})
		}
		for _, match := range jsonNumberPattern.FindAllStringSubmatch(action.Body, -1) {
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
		return
	}
	// Form-encoded bodies reuse the query-pair shape.
	for _, match := range queryPairPattern.FindAllStringSubmatch(action.Body, -1) {
		key, value := match[1], match[2]
		if !LooksParameterName(key) || value == "" || isTemplated(value) {
			continue
		}
		collector.add(key, value, "form field", section, index, action,
			"body", key+"="+value, func(template string) string { return key + "=" + template })
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
