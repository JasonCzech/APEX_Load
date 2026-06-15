package trueclient

// javascript.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertEvaluateJavaScript(code string, baseName string) ([]dsl.Action, []string) {
	statements := splitJavaScriptStatements(code)
	actions := make([]dsl.Action, 0, len(statements))
	unsupported := make([]string, 0)
	for i, statement := range statements {
		action, ok := convertJavaScriptStatement(statement, baseName, len(actions)+1)
		if !ok {
			actions = append(actions, dsl.Action{
				Name:   fmt.Sprintf("%s - Statement %d", baseName, i+1),
				Type:   "browser_evaluate_js",
				Target: statement,
				Metadata: map[string]any{
					"source":             "truclient_evaluate_javascript",
					"source_script":      statement,
					"review_required":    true,
					"review_gate":        "javascript",
					"review_reason":      "TruClient JavaScript could not be decomposed into native browser actions; replay and approval are required before promotion.",
					"promotion_blocker":  true,
					"compatibility_gate": "trueclient2_javascript_review",
				},
			})
			continue
		}
		actions = append(actions, action)
	}
	return actions, unsupported
}

func evaluateJavaScriptUnsupportedWarnings(unsupportedLines []string) []string {
	warnings := make([]string, 0, len(unsupportedLines))
	for _, line := range unsupportedLines {
		warnings = append(warnings, fmt.Sprintf("TruClient Evaluate JavaScript statement %q was skipped during import", line))
	}
	return warnings
}

func splitJavaScriptStatements(code string) []string {
	lines := strings.Split(code, "\n")
	parts := make([]string, 0)
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(stripLineComment(line))
		if trimmedLine == "" {
			continue
		}
		for _, statement := range strings.Split(trimmedLine, ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			parts = append(parts, statement)
		}
	}
	return parts
}

func stripLineComment(line string) string {
	var quote rune
	escaped := false
	for index, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '/' && index+1 < len(line) && line[index+1] == '/' {
			return line[:index]
		}
	}
	return line
}

func convertJavaScriptStatement(statement string, baseName string, position int) (dsl.Action, bool) {
	if matches := reJQueryVal.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reJQueryAttrValue.FindStringSubmatch(statement); len(matches) == 4 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		attribute, ok := decodeJSStringLiteral(matches[2])
		if !ok || !strings.EqualFold(strings.TrimSpace(attribute), "value") {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[3])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_jquery_attr",
			},
		}, true
	}
	if matches := reQuerySelectorValue.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reQuerySelectorAllValue.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok || strings.TrimSpace(selector) == "" {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector = strings.TrimSpace(selector)
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_query_selector_all",
			},
		}, true
	}
	if matches := reElementByIDValue.FindStringSubmatch(statement); len(matches) == 3 {
		id, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementIDSelector(id)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reElementsByNameValue.FindStringSubmatch(statement); len(matches) == 3 {
		name, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reElementsByClassValue.FindStringSubmatch(statement); len(matches) == 3 {
		className, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementClassSelector(className)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_class_element",
			},
		}, true
	}
	if matches := reElementsByTagValue.FindStringSubmatch(statement); len(matches) == 3 {
		tagName, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementTagSelector(tagName)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_tag_element",
			},
		}, true
	}
	if matches := reFormElementValue.FindStringSubmatch(statement); len(matches) == 4 {
		name, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[3])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_form_element",
			},
		}, true
	}
	if matches := reJQueryClick.FindStringSubmatch(statement); len(matches) == 2 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reJQueryTriggerClick.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		eventName, ok := decodeJSStringLiteral(matches[2])
		if !ok || !strings.EqualFold(strings.TrimSpace(eventName), "click") {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_jquery_trigger",
			},
		}, true
	}
	if matches := reQuerySelectorClick.FindStringSubmatch(statement); len(matches) == 2 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reQuerySelectorAllClick.FindStringSubmatch(statement); len(matches) == 2 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok || strings.TrimSpace(selector) == "" {
			return dsl.Action{}, false
		}
		selector = strings.TrimSpace(selector)
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_query_selector_all",
			},
		}, true
	}
	if matches := reElementByIDClick.FindStringSubmatch(statement); len(matches) == 2 {
		id, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementIDSelector(id)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reElementsByNameClick.FindStringSubmatch(statement); len(matches) == 2 {
		name, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reElementsByClassClick.FindStringSubmatch(statement); len(matches) == 2 {
		className, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementClassSelector(className)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_class_element",
			},
		}, true
	}
	if matches := reElementsByTagClick.FindStringSubmatch(statement); len(matches) == 2 {
		tagName, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementTagSelector(tagName)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_tag_element",
			},
		}, true
	}
	if matches := reFormElementClick.FindStringSubmatch(statement); len(matches) == 3 {
		name, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_form_element",
			},
		}, true
	}
	if matches := reWindowScroll.FindStringSubmatch(statement); len(matches) == 3 {
		x, xErr := strconv.ParseFloat(matches[1], 64)
		y, yErr := strconv.ParseFloat(matches[2], 64)
		if xErr != nil || yErr != nil {
			return dsl.Action{}, false
		}
		direction, distance := scrollGesture(x, y)
		if distance <= 0 {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Scroll %d", baseName, position),
			Type:     "browser_swipe",
			Target:   "body",
			Selector: "body",
			Params: map[string]any{
				"direction": direction,
				"distance":  distance,
			},
			Metadata: map[string]any{
				"source":        "truclient_evaluate_javascript",
				"source_script": statement,
			},
		}, true
	}
	if action, ok := convertJavaScriptLocationNavigation(statement, baseName, position); ok {
		return action, true
	}
	return dsl.Action{}, false
}

func elementIDSelector(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "#" + strings.ReplaceAll(id, `"`, `\"`)
}

func elementNameSelector(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return `[name="` + strings.ReplaceAll(name, `"`, `\"`) + `"]`
}

func elementClassSelector(className string) string {
	tokens := strings.Fields(strings.TrimSpace(className))
	if len(tokens) == 0 {
		return ""
	}
	for _, token := range tokens {
		if !reSimpleCSSClassToken.MatchString(token) {
			return ""
		}
	}
	return "." + strings.Join(tokens, ".")
}

func elementTagSelector(tagName string) string {
	tagName = strings.ToLower(strings.TrimSpace(tagName))
	if tagName == "" || !reSimpleHTMLTagToken.MatchString(tagName) {
		return ""
	}
	return tagName
}

func convertJavaScriptLocationNavigation(statement string, baseName string, position int) (dsl.Action, bool) {
	matches := reLocationAssign.FindStringSubmatch(statement)
	if len(matches) != 2 {
		matches = reLocationMethod.FindStringSubmatch(statement)
	}
	if len(matches) != 2 {
		return dsl.Action{}, false
	}
	target, ok := decodeJSStringLiteral(matches[1])
	if !ok || strings.TrimSpace(target) == "" {
		return dsl.Action{}, false
	}
	return dsl.Action{
		Name:   fmt.Sprintf("%s Navigate %d", baseName, position),
		Type:   "browser_navigate",
		Target: strings.TrimSpace(target),
		Metadata: map[string]any{
			"source":        "truclient_evaluate_javascript",
			"source_script": statement,
		},
	}, true
}

func scrollGesture(x, y float64) (string, int) {
	if absFloat(y) >= absFloat(x) {
		if y > 0 {
			return "up", int(absFloat(y))
		}
		if y < 0 {
			return "down", int(absFloat(y))
		}
		return "", 0
	}
	if x > 0 {
		return "left", int(absFloat(x))
	}
	if x < 0 {
		return "right", int(absFloat(x))
	}
	return "", 0
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func decodeJSStringLiteral(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 {
		return "", false
	}
	quote := trimmed[0]
	if (quote != '\'' && quote != '"') || trimmed[len(trimmed)-1] != quote {
		return "", false
	}
	inner := trimmed[1 : len(trimmed)-1]
	replacer := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\'`, `'`, `\n`, "\n", `\t`, "\t")
	return replacer.Replace(inner), true
}
