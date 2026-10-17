package httpcontract

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

const maxHTTPAssertionRegexBytes = MaxAssertionRegexBytes

var ErrAssertionSourceUnsupported = errors.New("source applies only to text assertions")

func ValidateAssertionDefinitionWithRegexCompiler(assertion protocol.Assertion, compileRegexCached func(string) (*regexp.Regexp, error)) error {
	if compileRegexCached == nil {
		compileRegexCached = regexp.Compile
	}
	if strings.TrimSpace(assertion.Source) != "" && !IsTextAssertion(assertion.Type) {
		return fmt.Errorf("%s assertion: %w", assertion.Type, ErrAssertionSourceUnsupported)
	}
	switch assertion.Type {
	case "text_contains", "text_not_contains", "text_matches", "text_not_matches":
		return validateTextAssertion(assertion, compileRegexCached)
	case "aura_actions_success":
		if assertion.Value != "" || assertion.Min != 0 || assertion.Max != 0 || assertion.LegacyHTMLCheck {
			return errors.New("aura_actions_success assertion accepts no value, min, max or legacy bypass")
		}
		if _, err := aura.ParseBaseline(assertion.Expected); err != nil {
			return fmt.Errorf("aura_actions_success assertion: %w", err)
		}
	case "status_code", "status_code_not":
		// An expected status may come from a parameter or variable: it is
		// resolved before the check runs, and must then be an integer.
		if _, ok := toInt(assertion.Expected); !ok && !isTemplateText(assertion.Expected) {
			if text, isText := assertion.Expected.(string); isText {
				return fmt.Errorf("%s assertion: expected an integer status, got %q", assertion.Type, text)
			}
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

func isTemplateText(v any) bool {
	text, ok := v.(string)
	return ok && strings.Contains(text, "{{")
}

func toInt(v any) (int, bool) {
	return protocol.AssertionInt(v)
}

func IsTextAssertion(assertionType string) bool {
	switch assertionType {
	case "text_contains", "text_not_contains", "text_matches", "text_not_matches":
		return true
	}
	return false
}

func IsRegexTextAssertion(assertionType string) bool {
	return assertionType == "text_matches" || assertionType == "text_not_matches"
}

func TextAssertionSource(assertion protocol.Assertion) string {
	source := strings.ToLower(strings.TrimSpace(assertion.Source))
	if source == "" {
		return "body"
	}
	return source
}

func TextAssertionPattern(assertion protocol.Assertion) string {
	if assertion.CaseInsensitive {
		return "(?i)" + assertion.Value
	}
	return assertion.Value
}

func validateTextAssertion(assertion protocol.Assertion, compileRegexCached func(string) (*regexp.Regexp, error)) error {
	if assertion.Value == "" {
		return fmt.Errorf("%s assertion: value is required", assertion.Type)
	}
	switch TextAssertionSource(assertion) {
	case "body", "headers", "all":
	default:
		return fmt.Errorf("%s assertion: source must be body, headers or all (got %q)", assertion.Type, assertion.Source)
	}
	if IsRegexTextAssertion(assertion.Type) {
		if len(assertion.Value) > maxHTTPAssertionRegexBytes {
			return fmt.Errorf("%s assertion: pattern exceeds %d bytes", assertion.Type, maxHTTPAssertionRegexBytes)
		}
		if _, err := compileRegexCached(TextAssertionPattern(assertion)); err != nil {
			return fmt.Errorf("%s assertion: invalid pattern: %w", assertion.Type, err)
		}
	}
	return nil
}

func ValidateAssertionDefinition(assertion protocol.Assertion) error {
	return ValidateAssertionDefinitionWithRegexCompiler(assertion, regexp.Compile)
}
