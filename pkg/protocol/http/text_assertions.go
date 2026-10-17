package http

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/httpcontract"
)

// Text assertions check a response surface for literal text or an RE2
// pattern, as LoadRunner's web_reg_find does: source "body" (default), the
// raw "headers" block ("Name: value" lines ending in CRLF), or "all" (the
// headers, a blank line, then the body).

// IsTextAssertion reports whether an assertion type is a text check.
func IsTextAssertion(assertionType string) bool {
	return httpcontract.IsTextAssertion(assertionType)
}

// IsRegexTextAssertion reports whether a text check's value is a pattern.
func IsRegexTextAssertion(assertionType string) bool {
	return httpcontract.IsRegexTextAssertion(assertionType)
}

func textAssertionSource(assertion protocol.Assertion) string {
	return httpcontract.TextAssertionSource(assertion)
}

func textAssertionNeedsBody(assertion protocol.Assertion) bool {
	return IsTextAssertion(assertion.Type) && textAssertionSource(assertion) != "headers"
}

// rawHeaderBlock renders headers as LoadRunner's raw header buffer: one
// "Name: value" line per value, ending in CRLF, sorted by name.
func rawHeaderBlock(headers http.Header) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		for _, value := range headers.Values(key) {
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(value)
			builder.WriteString("\r\n")
		}
	}
	return builder.String()
}

func textAssertionSurface(assertion protocol.Assertion, headers http.Header, bodyText string) string {
	switch textAssertionSource(assertion) {
	case "headers":
		return rawHeaderBlock(headers)
	case "all":
		return rawHeaderBlock(headers) + "\r\n" + bodyText
	default:
		return bodyText
	}
}

func textAssertionPattern(assertion protocol.Assertion) string {
	return httpcontract.TextAssertionPattern(assertion)
}

func evaluateTextAssertion(assertion protocol.Assertion, headers http.Header, bodyText string) error {
	surface := textAssertionSurface(assertion, headers, bodyText)
	where := map[string]string{"body": "response body", "headers": "response headers", "all": "response headers and body"}[textAssertionSource(assertion)]
	var found bool
	if IsRegexTextAssertion(assertion.Type) {
		expression, err := compileRegexCached(textAssertionPattern(assertion))
		if err != nil {
			return fmt.Errorf("%s assertion: invalid pattern: %w", assertion.Type, err)
		}
		found = expression.MatchString(surface)
	} else {
		found = bodyContains(surface, assertion.Value, assertion.CaseInsensitive)
	}
	switch {
	case assertion.Type == "text_contains" && !found:
		return fmt.Errorf("%s does not contain %q", where, assertion.Value)
	case assertion.Type == "text_not_contains" && found:
		return fmt.Errorf("%s contains %q (unexpected)", where, assertion.Value)
	case assertion.Type == "text_matches" && !found:
		return fmt.Errorf("%s does not match %q", where, assertion.Value)
	case assertion.Type == "text_not_matches" && found:
		return fmt.Errorf("%s matches %q (unexpected)", where, assertion.Value)
	}
	return nil
}

func textAssertionCost(assertion protocol.Assertion, budget *assertionInputWorkBudget) int64 {
	switch textAssertionSource(assertion) {
	case "headers":
		return budget.headerBytes
	case "all":
		return budget.headerBytes + budget.bodyBytes
	default:
		return budget.bodyBytes
	}
}
