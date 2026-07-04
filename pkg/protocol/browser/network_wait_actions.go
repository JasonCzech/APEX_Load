package browser

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type browserResponseMatcher struct {
	URLContains string
	URLExact    string
	URLRegex    *regexp.Regexp
	Method      string
	StatusCode  int
	Resource    string
	IncludeFail bool
}

func (b *Browser) dispatchWaitResponse(ctx context.Context, action *protocol.Action, result *protocol.Result) (error, bool) {
	matcher, err := responseMatcherFromAction(action)
	if err != nil {
		return nil, failBrowserAction(result, err.Error())
	}
	_, networkStart := b.eventOffsets()
	timeoutMS := actionParamInt(action.Params, "timeout_ms", 0)
	if timeoutMS < 0 {
		return nil, failBrowserAction(result, "browser_wait_response params.timeout_ms must be >= 0")
	}
	timeout := b.waitTimeout(timeoutMS)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		if match, ok := firstMatchingNetworkEntry(b.networkEntriesSince(networkStart), matcher); ok {
			result.ProtocolEvidence = map[string]any{"browser_response_match": match}
			return nil, false
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("browser_wait_response timed out after %s waiting for matching response", timeout), false
		case <-ticker.C:
		}
	}
}

func responseMatcherFromAction(action *protocol.Action) (browserResponseMatcher, error) {
	if action == nil {
		return browserResponseMatcher{}, nil
	}
	matcher := browserResponseMatcher{
		URLContains: strings.TrimSpace(firstNonEmptyString(action.Target, actionParamString(action.Params, "url_contains", ""))),
		URLExact:    actionParamString(action.Params, "url", ""),
		Method:      strings.ToUpper(actionParamString(action.Params, "method", "")),
		StatusCode:  actionParamInt(action.Params, "status_code", actionParamInt(action.Params, "status", 0)),
		Resource:    strings.ToLower(actionParamString(action.Params, "resource_type", "")),
		IncludeFail: actionParamBool(action.Params, "include_failed", false),
	}
	if matcher.StatusCode < 0 {
		return matcher, fmt.Errorf("browser_wait_response params.status_code must be >= 0")
	}
	if matcher.Method != "" && !isHTTPMethod(matcher.Method) {
		return matcher, fmt.Errorf("browser_wait_response params.method must be a valid HTTP method")
	}
	if rawRegex := actionParamString(action.Params, "url_regex", ""); rawRegex != "" {
		compiled, err := regexp.Compile(rawRegex)
		if err != nil {
			return matcher, fmt.Errorf("browser_wait_response params.url_regex is invalid: %w", err)
		}
		matcher.URLRegex = compiled
	}
	return matcher, nil
}

func isHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func firstMatchingNetworkEntry(entries []protocol.NetworkEntry, matcher browserResponseMatcher) (protocol.NetworkEntry, bool) {
	for _, entry := range entries {
		if responseEntryMatches(entry, matcher) {
			return entry, true
		}
	}
	return protocol.NetworkEntry{}, false
}

func responseEntryMatches(entry protocol.NetworkEntry, matcher browserResponseMatcher) bool {
	if entry.StatusCode == 0 && !matcher.IncludeFail {
		return false
	}
	if matcher.URLContains != "" && !strings.Contains(entry.URL, matcher.URLContains) {
		return false
	}
	if matcher.URLExact != "" && entry.URL != matcher.URLExact {
		return false
	}
	if matcher.URLRegex != nil && !matcher.URLRegex.MatchString(entry.URL) {
		return false
	}
	if matcher.Method != "" && strings.ToUpper(entry.Method) != matcher.Method {
		return false
	}
	if matcher.StatusCode > 0 && entry.StatusCode != matcher.StatusCode {
		return false
	}
	if matcher.Resource != "" && strings.ToLower(entry.ResourceType) != matcher.Resource {
		return false
	}
	return true
}
