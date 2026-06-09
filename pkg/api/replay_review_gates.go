package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func validateScriptForReplay(script *dsl.Script) []string {
	issues := validateScript(script)
	return appendUniqueStrings(issues, replayReviewGateIssues(script)...)
}

func replayReviewGateIssues(script *dsl.Script) []string {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		return nil
	}
	policy := script.Config.Browser.ReviewPolicy
	var issues []string
	collectReplayReviewGateIssues(&issues, "init", script.Init, policy)
	collectReplayReviewGateIssues(&issues, "actions", script.Actions, policy)
	collectReplayReviewGateIssues(&issues, "teardown", script.Teardown, policy)
	return issues
}

func collectReplayReviewGateIssues(issues *[]string, path string, actions []dsl.Action, policy dsl.BrowserReviewPolicy) {
	for i, action := range actions {
		label := fmt.Sprintf("%s[%d] %q", path, i, action.Name)
		if reviewGatedJavaScriptAction(action) {
			if !policy.AllowReviewedJavaScript {
				*issues = append(*issues, fmt.Sprintf("%s: review-gated JavaScript requires config.browser.review_policy.allow_reviewed_javascript=true before replay", label))
			}
		} else if reviewRequiredAction(action) && !policy.AllowReviewRequired {
			*issues = append(*issues, fmt.Sprintf("%s: review-required action requires config.browser.review_policy.allow_review_required=true before replay", label))
		}
		if action.Control != nil {
			collectReplayReviewGateIssues(issues, fmt.Sprintf("%s[%d].control.steps", path, i), action.Control.Steps, policy)
			collectReplayReviewGateIssues(issues, fmt.Sprintf("%s[%d].control.else", path, i), action.Control.Else, policy)
		}
	}
}

func reviewGatedJavaScriptAction(action dsl.Action) bool {
	if !strings.EqualFold(strings.TrimSpace(action.Type), "browser_evaluate_js") {
		return false
	}
	if metadataString(action.Metadata, "review_gate") == "javascript" {
		return true
	}
	return metadataBool(action.Metadata, "review_required") || metadataBool(action.Metadata, "promotion_blocker")
}

func reviewRequiredAction(action dsl.Action) bool {
	return metadataBool(action.Metadata, "review_required") || metadataBool(action.Metadata, "promotion_blocker")
}

func metadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func appendUniqueStrings(values []string, extra ...string) []string {
	if len(extra) == 0 {
		return values
	}
	seen := make(map[string]struct{}, len(values)+len(extra))
	out := make([]string, 0, len(values)+len(extra))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range extra {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
