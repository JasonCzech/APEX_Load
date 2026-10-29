package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
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

func boundedAssertionDeclared(raw string) string {
	declared := boundedInteractionText(strings.ToLower(strings.TrimSpace(raw)), 64)
	if declared == "" {
		return "assertion"
	}
	return declared
}

type browserAssertionHTMLCapture struct {
	attempted bool
	err       error
}

func (b *Browser) evaluateBrowserAssertions(action *protocol.Action, assertions []protocol.Assertion, result *protocol.Result) error {
	_, err, _ := b.evaluateBrowserAssertionsWithOutcomes(context.Background(), action, assertions, result, false)
	return err
}

func (b *Browser) evaluateBrowserAssertionsWithOutcomes(ctx context.Context, action *protocol.Action, assertions []protocol.Assertion, result *protocol.Result, actionFailed bool) ([]protocol.BrowserInteractionOutcome, error, browserAssertionHTMLCapture) {
	htmlCapture := browserAssertionHTMLCapture{attempted: result.HTML != ""}
	if len(assertions) == 0 {
		return nil, nil, htmlCapture
	}
	outcomes := make([]protocol.BrowserInteractionOutcome, 0, len(assertions))
	if actionFailed {
		for _, assertion := range assertions {
			outcomes = append(outcomes, protocol.BrowserInteractionOutcome{
				Kind:     protocol.InteractionOutcomeKindAssertion,
				Declared: boundedAssertionDeclared(assertion.Type),
				Observed: protocol.InteractionOutcomeNotReached,
				Reason:   "action_failed",
			})
		}
		return outcomes, nil, htmlCapture
	}
	var firstErr error
	for _, assertion := range assertions {
		declared := boundedAssertionDeclared(assertion.Type)
		if firstErr != nil {
			outcomes = append(outcomes, protocol.BrowserInteractionOutcome{
				Kind:     protocol.InteractionOutcomeKindAssertion,
				Declared: declared,
				Observed: protocol.InteractionOutcomeNotReached,
				Reason:   "prior_assertion_failed",
			})
			continue
		}
		outcome, err := b.evaluateSingleBrowserAssertion(ctx, action, assertion, declared, result, &htmlCapture)
		outcomes = append(outcomes, outcome)
		if err != nil {
			firstErr = err
		}
	}
	return outcomes, firstErr, htmlCapture
}

func (b *Browser) evaluateSingleBrowserAssertion(ctx context.Context, action *protocol.Action, assertion protocol.Assertion, declared string, result *protocol.Result, htmlCapture *browserAssertionHTMLCapture) (protocol.BrowserInteractionOutcome, error) {
	notReached := func(reason string, err error) (protocol.BrowserInteractionOutcome, error) {
		if ctx != nil && ctx.Err() != nil {
			reason = "context_canceled"
		}
		return protocol.BrowserInteractionOutcome{
			Kind:     protocol.InteractionOutcomeKindAssertion,
			Declared: declared,
			Observed: protocol.InteractionOutcomeNotReached,
			Reason:   reason,
		}, err
	}
	met := func() (protocol.BrowserInteractionOutcome, error) {
		now := time.Now().UTC()
		return protocol.BrowserInteractionOutcome{
			Kind:       protocol.InteractionOutcomeKindAssertion,
			Declared:   declared,
			Observed:   protocol.InteractionOutcomeMet,
			ObservedAt: &now,
		}, nil
	}
	missed := func(err error) (protocol.BrowserInteractionOutcome, error) {
		now := time.Now().UTC()
		return protocol.BrowserInteractionOutcome{
			Kind:       protocol.InteractionOutcomeKindAssertion,
			Declared:   declared,
			Observed:   protocol.InteractionOutcomeMissed,
			Reason:     "assertion_failed",
			ObservedAt: &now,
		}, err
	}
	switch assertion.Type {
	case "status_code":
		expected, ok := protocol.AssertionInt(assertion.Expected)
		if !ok {
			return notReached("invalid_assertion", fmt.Errorf("status_code assertion: expected integer, got %v", assertion.Expected))
		}
		if result.StatusCode != expected {
			recordAssertionMismatch(result, assertion.Type, fmt.Sprint(expected), fmt.Sprint(result.StatusCode))
			return missed(fmt.Errorf("browser page status = %d, want %d", result.StatusCode, expected))
		}
		return met()
	case "response_time_ms":
		if assertion.Max > 0 && result.Duration.Milliseconds() > int64(assertion.Max) {
			return missed(fmt.Errorf("browser response time %dms exceeded max %dms", result.Duration.Milliseconds(), assertion.Max))
		}
		return met()
	case "body_contains":
		if !htmlCapture.attempted && b.currentPage() != nil {
			htmlCapture.attempted = true
			html, err := b.captureHTML(ctx)
			if err != nil {
				htmlCapture.err = err
				return notReached("evaluation_error", err)
			}
			result.HTML = html
		}
		if !strings.Contains(result.HTML, assertion.Value) {
			return missed(fmt.Errorf("browser page HTML does not contain %q", assertion.Value))
		}
		return met()
	case "body_not_contains":
		if !htmlCapture.attempted && b.currentPage() != nil {
			htmlCapture.attempted = true
			html, err := b.captureHTML(ctx)
			if err != nil {
				htmlCapture.err = err
				return notReached("evaluation_error", err)
			}
			result.HTML = html
		}
		if strings.Contains(result.HTML, assertion.Value) {
			return missed(fmt.Errorf("browser page HTML contains %q (unexpected)", assertion.Value))
		}
		return met()
	case "page_title":
		expected := strings.TrimSpace(assertion.Value)
		if expected == "" && assertion.Expected != nil {
			expected = strings.TrimSpace(fmt.Sprint(assertion.Expected))
		}
		if expected == "" {
			return notReached("invalid_assertion", fmt.Errorf("page_title assertion requires value or expected"))
		}
		if result.PageTitle == "" && b.currentPage() != nil {
			title, err := b.currentPage().Title()
			if err != nil {
				return notReached("evaluation_error", err)
			}
			result.PageTitle = boundedtext.TruncateUTF8(strings.TrimSpace(title), browserDOMTitleLimitBytes)
		}
		if result.PageTitle != expected {
			recordAssertionMismatch(result, assertion.Type, expected, result.PageTitle)
			return missed(fmt.Errorf("browser page title = %q, want %q", result.PageTitle, expected))
		}
		return met()
	case "url_contains":
		if assertion.Value == "" {
			return notReached("invalid_assertion", fmt.Errorf("url_contains assertion requires value"))
		}
		if result.PageURL == "" && b.currentPage() != nil {
			result.PageURL = boundedtext.TruncateUTF8(b.currentPage().URL(), browserDOMURLLimitBytes)
		}
		if !strings.Contains(result.PageURL, assertion.Value) {
			return missed(fmt.Errorf("browser page URL %q does not contain %q", result.PageURL, assertion.Value))
		}
		return met()
	case "element_visible":
		selector := assertion.Value
		if selector == "" {
			return notReached("invalid_assertion", fmt.Errorf("element_visible assertion requires selector in value"))
		}
		visible, err := b.assertionSelectorVisible(action, selector)
		if err != nil {
			return notReached("evaluation_error", fmt.Errorf("failed to check visibility for %q: %v", selector, err))
		}
		if !visible {
			return missed(fmt.Errorf("element %q is not visible", selector))
		}
		return met()
	case "element_text_equals":
		selector := assertion.Value
		if selector == "" {
			return notReached("invalid_assertion", fmt.Errorf("element_text_equals assertion requires selector in value"))
		}
		text, err := b.assertionSelectorText(action, selector)
		if err != nil {
			return notReached("evaluation_error", fmt.Errorf("failed to get text for %q: %v", selector, err))
		}
		text = strings.TrimSpace(text)
		expected := strings.TrimSpace(fmt.Sprint(assertion.Expected))
		if text != expected {
			recordAssertionMismatch(result, assertion.Type, expected, text)
			return missed(fmt.Errorf("element %q text = %q, want %q", selector, text, expected))
		}
		return met()
	case "element_text_contains":
		selector := assertion.Value
		if selector == "" {
			return notReached("invalid_assertion", fmt.Errorf("element_text_contains assertion requires selector in value"))
		}
		text, err := b.assertionSelectorText(action, selector)
		if err != nil {
			return notReached("evaluation_error", fmt.Errorf("failed to get text for %q: %v", selector, err))
		}
		expected := strings.TrimSpace(fmt.Sprint(assertion.Expected))
		if !strings.Contains(text, expected) {
			recordAssertionMismatch(result, assertion.Type, expected, text)
			return missed(fmt.Errorf("element %q text %q does not contain %q", selector, text, expected))
		}
		return met()
	default:
		return protocol.BrowserInteractionOutcome{
			Kind:     protocol.InteractionOutcomeKindAssertion,
			Declared: declared,
			Observed: protocol.InteractionOutcomeUnsupported,
			Reason:   "unsupported_assertion",
		}, fmt.Errorf("unsupported browser assertion type: %s", assertion.Type)
	}
}

func (b *Browser) assertionSelectorVisible(action *protocol.Action, selector string) (bool, error) {
	if locator, ok := b.actionLocator(action, selector); ok {
		return locator.IsVisible()
	}
	page := b.currentPage()
	if page == nil {
		return false, fmt.Errorf("browser page is not initialized")
	}
	return firstMatch(page, selector).IsVisible()
}

func (b *Browser) assertionSelectorText(action *protocol.Action, selector string) (string, error) {
	if locator, ok := b.actionLocator(action, selector); ok {
		return locator.TextContent()
	}
	page := b.currentPage()
	if page == nil {
		return "", fmt.Errorf("browser page is not initialized")
	}
	return firstMatch(page, selector).TextContent()
}

// Structured evidence keeps an evaluated assertion distinct from a locator
// failure encountered while trying to read the element.
func recordAssertionMismatch(result *protocol.Result, kind, expected, actual string) {
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = make(map[string]any)
	}
	result.ProtocolEvidence["assertion_failure"] = map[string]any{"type": kind, "expected": expected, "actual": actual}
}
