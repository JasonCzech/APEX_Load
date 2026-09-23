package http

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	// MaxActionAssertions bounds both authored DSL and direct protocol callers.
	// Assertions execute once per response and body assertions independently scan
	// the captured payload, so an unbounded list would multiply one response into
	// unbounded CPU work on every VUser iteration.
	MaxActionAssertions = 256

	maxHTTPAssertionRegexBytes = 4 << 10
	// Permit at most two complete passes over a maximum-sized response body per
	// action. Normal small responses can still support the full assertion-count
	// limit, while large responses cannot amplify into gigabytes of logical scan
	// work on each VUser iteration.
	maxHTTPAssertionInputWorkBytes int64 = 2 * hardHTTPMaxBodyBytes
)

var errHTTPAssertionResourceLimit = errors.New("assertion resource limit exceeded")

type assertionInputWorkBudget struct {
	used        int64
	limit       int64
	bodyBytes   int64
	headerBytes int64
}

func newAssertionInputWorkBudget(headers http.Header, body []byte) *assertionInputWorkBudget {
	return &assertionInputWorkBudget{
		limit:       maxHTTPAssertionInputWorkBytes,
		bodyBytes:   int64(len(body)),
		headerBytes: flattenedHeadersByteSize(headers),
	}
}

func (budget *assertionInputWorkBudget) charge(assertion protocol.Assertion) error {
	if budget == nil {
		return nil
	}
	limit := budget.limit
	if limit <= 0 {
		limit = maxHTTPAssertionInputWorkBytes
	}
	var cost int64
	switch assertion.Type {
	case "body_contains", "body_not_contains", "body_regex_capture_equals":
		cost = budget.bodyBytes
	case "header_contains":
		// Header lookup scans the response header surface to honor
		// case-insensitive names, so charge the complete bounded surface.
		cost = budget.headerBytes
	default:
		return nil
	}
	if cost < 0 || budget.used < 0 || budget.used > limit || cost > limit-budget.used {
		return fmt.Errorf("%w: aggregate assertion input exceeds %d bytes", errHTTPAssertionResourceLimit, limit)
	}
	budget.used += cost
	return nil
}

type contentCheck struct {
	LeftBoundary    string
	RightBoundary   string
	Text            string
	Scope           string
	FailIfFound     bool
	CaseInsensitive bool
}

func evaluate(a protocol.Assertion, result *protocol.Result, bodyText string) error {
	return evaluateResponseAssertion(a, result, nil, bodyText)
}

func evaluateResponseAssertion(a protocol.Assertion, result *protocol.Result, responseHeaders http.Header, bodyText string) error {
	switch a.Type {
	case "status_code":
		expected, ok := toInt(a.Expected)
		if !ok {
			return fmt.Errorf("status_code assertion: expected integer, got %v", a.Expected)
		}
		if result.StatusCode != expected {
			return fmt.Errorf("expected status %d, got %d", expected, result.StatusCode)
		}

	case "status_code_not":
		expected, ok := toInt(a.Expected)
		if !ok {
			return fmt.Errorf("status_code_not assertion: expected integer, got %v", a.Expected)
		}
		if result.StatusCode == expected {
			return fmt.Errorf("expected status to not be %d", expected)
		}

	case "status_code_range":
		minStatus := a.Min
		maxStatus := a.Max
		if minStatus == 0 && maxStatus == 0 {
			return fmt.Errorf("status_code_range assertion: min or max is required")
		}
		if minStatus == 0 {
			minStatus = maxStatus
		}
		if maxStatus == 0 {
			maxStatus = minStatus
		}
		if minStatus > maxStatus {
			return fmt.Errorf("status_code_range assertion: min %d exceeds max %d", minStatus, maxStatus)
		}
		if result.StatusCode < minStatus || result.StatusCode > maxStatus {
			return fmt.Errorf("expected status between %d and %d, got %d", minStatus, maxStatus, result.StatusCode)
		}

	case "response_time_ms":
		if result.Duration.Milliseconds() > int64(a.Max) {
			return fmt.Errorf("response time %dms exceeded max %dms", result.Duration.Milliseconds(), a.Max)
		}

	case "body_contains":
		if !strings.Contains(bodyText, a.Value) {
			return fmt.Errorf("response body does not contain %q", a.Value)
		}

	case "body_not_contains":
		if strings.Contains(bodyText, a.Value) {
			return fmt.Errorf("response body contains %q (unexpected)", a.Value)
		}

	case "header_contains":
		expected, ok := a.Expected.(string)
		if !ok {
			return fmt.Errorf("header_contains assertion: expected string, got %T", a.Expected)
		}
		headerName := strings.TrimSpace(a.Value)
		if headerName == "" {
			return fmt.Errorf("header_contains assertion: header name is required")
		}
		values := responseHeaderValues(responseHeaders, headerName)
		if len(values) == 0 {
			return fmt.Errorf("response header %q is missing", headerName)
		}
		for _, value := range values {
			if strings.Contains(value, expected) {
				return nil
			}
		}
		return fmt.Errorf("response header %q does not contain %q", headerName, expected)

	case "body_regex_capture_equals":
		expected, ok := a.Expected.(string)
		if !ok {
			return fmt.Errorf("body_regex_capture_equals assertion: expected string, got %T", a.Expected)
		}
		if len(a.Value) > maxHTTPAssertionRegexBytes {
			return fmt.Errorf("body_regex_capture_equals assertion: pattern exceeds %d bytes", maxHTTPAssertionRegexBytes)
		}
		expression, err := compileRegexCached(a.Value)
		if err != nil {
			return fmt.Errorf("body_regex_capture_equals assertion: invalid pattern: %w", err)
		}
		match := expression.FindStringSubmatchIndex(bodyText)
		if match == nil {
			return fmt.Errorf("response body does not match assertion pattern")
		}
		actual := bodyText[match[0]:match[1]]
		if len(match) > 2 {
			if match[2] < 0 || match[3] < 0 {
				return fmt.Errorf("response body regex first capture group did not participate in the match")
			}
			actual = bodyText[match[2]:match[3]]
		}
		if actual != expected {
			return fmt.Errorf("response body regex value does not equal %q", expected)
		}

	default:
		return fmt.Errorf("unsupported assertion type: %s", a.Type)
	}
	return nil
}

func responseHeaderValues(headers http.Header, name string) []string {
	var matched []string
	for headerName, values := range headers {
		if strings.EqualFold(headerName, name) {
			matched = append(matched, values...)
		}
	}
	return matched
}

// ValidateAssertionDefinition validates the authored fields that must be
// rejected before an HTTP action is launched. The DSL validator calls this
// same function so authoring health and runtime admission cannot diverge.
func ValidateAssertionDefinition(assertion protocol.Assertion) error {
	switch assertion.Type {
	case "status_code", "status_code_not":
		if _, ok := toInt(assertion.Expected); !ok {
			return fmt.Errorf("%s assertion: expected integer, got %T", assertion.Type, assertion.Expected)
		}
	case "status_code_range":
		minStatus := assertion.Min
		maxStatus := assertion.Max
		if minStatus == 0 && maxStatus == 0 {
			return errors.New("status_code_range assertion: min or max is required")
		}
		if minStatus != 0 && maxStatus != 0 && minStatus > maxStatus {
			return fmt.Errorf("status_code_range assertion: min %d exceeds max %d", minStatus, maxStatus)
		}
	case "response_time_ms":
		if assertion.Max < 0 {
			return errors.New("response_time_ms assertion: max must not be negative")
		}
	case "body_contains", "body_not_contains":
		if assertion.Value == "" {
			return fmt.Errorf("%s assertion: value is required", assertion.Type)
		}
	case "header_contains":
		if _, ok := assertion.Expected.(string); !ok {
			return fmt.Errorf("header_contains assertion: expected string, got %T", assertion.Expected)
		}
		if strings.TrimSpace(assertion.Value) == "" {
			return fmt.Errorf("header_contains assertion: header name is required")
		}
	case "body_regex_capture_equals":
		if _, ok := assertion.Expected.(string); !ok {
			return fmt.Errorf("body_regex_capture_equals assertion: expected string, got %T", assertion.Expected)
		}
		if len(assertion.Value) > maxHTTPAssertionRegexBytes {
			return fmt.Errorf("body_regex_capture_equals assertion: pattern exceeds %d bytes", maxHTTPAssertionRegexBytes)
		}
		if _, err := compileRegexCached(assertion.Value); err != nil {
			return fmt.Errorf("body_regex_capture_equals assertion: invalid pattern: %w", err)
		}
	default:
		return fmt.Errorf("unsupported assertion type: %s", assertion.Type)
	}
	return nil
}

func validateHTTPAssertionDefinition(assertion protocol.Assertion) error {
	return ValidateAssertionDefinition(assertion)
}

func responseStatusExplicitlyAccepted(assertions []protocol.Assertion, result *protocol.Result) bool {
	checked := false
	for _, assertion := range assertions {
		switch assertion.Type {
		case "status_code", "status_code_not", "status_code_range":
			checked = true
			if err := evaluate(assertion, result, ""); err != nil {
				return false
			}
		}
	}
	return checked
}

func assertionNeedsResponseBody(assertion protocol.Assertion) bool {
	switch assertion.Type {
	case "body_contains", "body_not_contains", "body_regex_capture_equals":
		return true
	default:
		return false
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n ||
			n < -2147483648 || n > 2147483647 {
			return 0, false
		}
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
		left, _ := item["left_boundary"].(string)
		right, _ := item["right_boundary"].(string)
		if text == "" && (left == "" || right == "") {
			continue
		}
		scope, _ := item["scope"].(string)
		scope = normalizeContentCheckScope(scope)
		failIfFound := true
		if value, ok := item["fail_if_found"].(bool); ok {
			failIfFound = value
		}
		caseInsensitive := false
		if value, ok := item["case_insensitive"].(bool); ok {
			caseInsensitive = value
		}
		checks = append(checks, contentCheck{
			LeftBoundary: left, RightBoundary: right,
			Text:            text,
			Scope:           scope,
			FailIfFound:     failIfFound,
			CaseInsensitive: caseInsensitive,
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

func evaluateContentChecks(checks []contentCheck, result *protocol.Result, bodyText string) error {
	return evaluateContentChecksLimited(checks, result, bodyText, 0)
}
func evaluateContentChecksLimited(checks []contentCheck, result *protocol.Result, bodyText string, limit int) error {
	var first error
	for _, check := range checks {
		if check.Scope == "resource" {
			continue
		}
		haystack, needle, left, right := bodyText, check.Text, check.LeftBoundary, check.RightBoundary
		if check.CaseInsensitive {
			haystack = strings.ToLower(haystack)
			needle = strings.ToLower(needle)
			left = strings.ToLower(left)
			right = strings.ToLower(right)
		}
		matches := 0
		if left != "" && right != "" {
			rest := haystack
			for {
				i := strings.Index(rest, left)
				if i < 0 {
					break
				}
				rest = rest[i+len(left):]
				j := strings.Index(rest, right)
				if j < 0 {
					break
				}
				if needle == "" || strings.Contains(rest[:j], needle) {
					matches++
				}
				rest = rest[j+len(right):]
			}
		} else if needle != "" {
			matches = strings.Count(haystack, needle)
		}
		if (matches > 0) == check.FailIfFound {
			if first == nil {
				if check.FailIfFound {
					first = fmt.Errorf("content check (%s) matched forbidden text", check.Scope)
				} else {
					first = fmt.Errorf("content check (%s) did not find required text", check.Scope)
				}
			}
			if check.FailIfFound && left != "" && right != "" {
				errors := matches
				if limit > 0 {
					remaining := max(0, limit-result.ContentCheckErrorMatches)
					errors = min(errors, remaining)
				}
				result.ContentCheckErrorMatches += errors
				result.ContentCheckInfoMatches += matches - errors
			}
		}
	}
	return first
}
