package browser

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func requiresBrowserAssertionHTML(assertions []protocol.Assertion) bool {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "body_contains", "body_not_contains":
			return true
		}
	}
	return false
}

func (b *Browser) evaluateBrowserAssertions(action *protocol.Action, assertions []protocol.Assertion, result *protocol.Result) error {
	for _, assertion := range assertions {
		switch assertion.Type {
		case "response_time_ms":
			if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
				return fmt.Errorf("browser response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max)
			}
		case "body_contains":
			if !strings.Contains(result.HTML, assertion.Value) {
				return fmt.Errorf("browser page HTML does not contain %q", assertion.Value)
			}
		case "body_not_contains":
			if strings.Contains(result.HTML, assertion.Value) {
				return fmt.Errorf("browser page HTML contains %q (unexpected)", assertion.Value)
			}
		case "page_title":
			expected := strings.TrimSpace(assertion.Value)
			if expected == "" && assertion.Expected != nil {
				expected = strings.TrimSpace(fmt.Sprint(assertion.Expected))
			}
			if expected == "" {
				return fmt.Errorf("page_title assertion requires value or expected")
			}
			if result.PageTitle != expected {
				recordAssertionMismatch(result, assertion.Type, expected, result.PageTitle)
				return fmt.Errorf("browser page title = %q, want %q", result.PageTitle, expected)
			}
		case "url_contains":
			if assertion.Value == "" {
				return fmt.Errorf("url_contains assertion requires value")
			}
			if !strings.Contains(result.PageURL, assertion.Value) {
				return fmt.Errorf("browser page URL %q does not contain %q", result.PageURL, assertion.Value)
			}
		case "element_visible":
			selector := assertion.Value
			if selector == "" {
				return fmt.Errorf("element_visible assertion requires selector in value")
			}
			visible, err := b.assertionSelectorVisible(action, selector)
			if err != nil {
				return fmt.Errorf("failed to check visibility for %q: %v", selector, err)
			}
			if !visible {
				return fmt.Errorf("element %q is not visible", selector)
			}
		case "element_text_equals":
			selector := assertion.Value
			if selector == "" {
				return fmt.Errorf("element_text_equals assertion requires selector in value")
			}
			text, err := b.assertionSelectorText(action, selector)
			if err != nil {
				return fmt.Errorf("failed to get text for %q: %v", selector, err)
			}
			text = strings.TrimSpace(text)
			expected := strings.TrimSpace(fmt.Sprint(assertion.Expected))
			if text != expected {
				recordAssertionMismatch(result, assertion.Type, expected, text)
				return fmt.Errorf("element %q text = %q, want %q", selector, text, expected)
			}
		case "element_text_contains":
			selector := assertion.Value
			if selector == "" {
				return fmt.Errorf("element_text_contains assertion requires selector in value")
			}
			text, err := b.assertionSelectorText(action, selector)
			if err != nil {
				return fmt.Errorf("failed to get text for %q: %v", selector, err)
			}
			expected := strings.TrimSpace(fmt.Sprint(assertion.Expected))
			if !strings.Contains(text, expected) {
				recordAssertionMismatch(result, assertion.Type, expected, text)
				return fmt.Errorf("element %q text %q does not contain %q", selector, text, expected)
			}
		default:
			return fmt.Errorf("unsupported browser assertion type: %s", assertion.Type)
		}
	}
	return nil
}

func (b *Browser) assertionSelectorVisible(action *protocol.Action, selector string) (bool, error) {
	if locator, ok := b.actionLocator(action, selector); ok {
		return locator.IsVisible()
	}
	page := b.currentPage()
	if page == nil {
		return false, fmt.Errorf("browser page is not initialized")
	}
	return page.IsVisible(selector)
}

func (b *Browser) assertionSelectorText(action *protocol.Action, selector string) (string, error) {
	if locator, ok := b.actionLocator(action, selector); ok {
		return locator.TextContent()
	}
	page := b.currentPage()
	if page == nil {
		return "", fmt.Errorf("browser page is not initialized")
	}
	return page.TextContent(selector)
}

// Structured evidence keeps an evaluated assertion distinct from a locator
// failure encountered while trying to read the element.
func recordAssertionMismatch(result *protocol.Result, kind, expected, actual string) {
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = make(map[string]any)
	}
	result.ProtocolEvidence["assertion_failure"] = map[string]any{"type": kind, "expected": expected, "actual": actual}
}
