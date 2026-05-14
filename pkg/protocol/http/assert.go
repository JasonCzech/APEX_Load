package http

import (
	"fmt"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type contentCheck struct {
	Text        string
	Scope       string
	FailIfFound bool
}

func evaluate(a protocol.Assertion, result *protocol.Result, body []byte) error {
	switch a.Type {
	case "status_code":
		expected, ok := toInt(a.Expected)
		if !ok {
			return fmt.Errorf("status_code assertion: expected integer, got %v", a.Expected)
		}
		if result.StatusCode != expected {
			return fmt.Errorf("expected status %d, got %d", expected, result.StatusCode)
		}

	case "response_time_ms":
		if result.Duration.Milliseconds() > int64(a.Max) {
			return fmt.Errorf("response time %dms exceeded max %dms", result.Duration.Milliseconds(), a.Max)
		}

	case "body_contains":
		if !strings.Contains(string(body), a.Value) {
			return fmt.Errorf("response body does not contain %q", a.Value)
		}

	case "body_not_contains":
		if strings.Contains(string(body), a.Value) {
			return fmt.Errorf("response body contains %q (unexpected)", a.Value)
		}

	default:
		return fmt.Errorf("unsupported assertion type: %s", a.Type)
	}
	return nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}

func parseContentChecks(config map[string]any) []contentCheck {
	rawChecks, ok := config["web_content_checks"]
	if !ok {
		return nil
	}
	items, ok := rawChecks.([]map[string]any)
	if ok {
		return parseContentCheckMaps(items)
	}
	rawItems, ok := rawChecks.([]any)
	if !ok {
		return nil
	}
	items = make([]map[string]any, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		items = append(items, item)
	}
	return parseContentCheckMaps(items)
}

func parseContentCheckMaps(items []map[string]any) []contentCheck {
	checks := make([]contentCheck, 0, len(items))
	for _, item := range items {
		text, _ := item["text"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		scope, _ := item["scope"].(string)
		scope = normalizeContentCheckScope(scope)
		failIfFound := true
		if value, ok := item["fail_if_found"].(bool); ok {
			failIfFound = value
		}
		checks = append(checks, contentCheck{
			Text:        text,
			Scope:       scope,
			FailIfFound: failIfFound,
		})
	}
	return checks
}

func normalizeContentCheckScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case "main_document", "resource":
		return strings.TrimSpace(scope)
	default:
		return "any_response"
	}
}

func evaluateContentChecks(checks []contentCheck, result *protocol.Result, body []byte) error {
	bodyText := string(body)
	for _, check := range checks {
		if check.Scope == "resource" {
			continue
		}
		contains := strings.Contains(bodyText, check.Text)
		if check.FailIfFound && contains {
			return fmt.Errorf("content check (%s) matched forbidden text %q", check.Scope, check.Text)
		}
		if !check.FailIfFound && !contains {
			return fmt.Errorf("content check (%s) did not find required text %q", check.Scope, check.Text)
		}
	}
	return nil
}
