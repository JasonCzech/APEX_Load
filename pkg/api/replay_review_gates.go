package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

func validateScriptForReplay(script *dsl.Script) []string {
	issues := validateScript(script)
	return appendUniqueStrings(issues, scriptReviewGateIssues(script, "replay")...)
}

func replayReviewGateIssues(script *dsl.Script) []string {
	return scriptReviewGateIssues(script, "replay")
}

func replayReviewGateError(script *dsl.Script) error {
	issues := replayReviewGateIssues(script)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("script replay is blocked: %s", strings.Join(issues, "; "))
}

// storedScriptReplayGateError protects immutable import gates before a
// stored-script endpoint applies a caller-supplied draft replacement. Historical
// browser review-policy gates remain subject to their existing per-replay
// overrides and are revalidated after those overrides are applied, but durable
// execution_blocker(s) and native Bruno gates may never be removed by replacing
// the request-time draft.
func storedScriptReplayGateError(script *dsl.Script) error {
	if script != nil &&
		strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		issues := scriptReviewGateIssuesWithBrowserPolicy(script, "replay", false)
		if len(issues) == 0 {
			return nil
		}
		return fmt.Errorf("script replay is blocked: %s", strings.Join(issues, "; "))
	}
	return replayReviewGateError(script)
}

func launchReviewGateIssues(script *dsl.Script) []string {
	return scriptReviewGateIssues(script, "launch")
}

func scriptReviewGateIssues(script *dsl.Script, operation string) []string {
	return scriptReviewGateIssuesWithBrowserPolicy(script, operation, true)
}

func scriptReviewGateIssuesWithBrowserPolicy(
	script *dsl.Script,
	operation string,
	enforceBrowserPolicy bool,
) []string {
	if script == nil {
		return nil
	}
	browserProtocol := strings.EqualFold(strings.TrimSpace(script.Protocol), "browser")
	policy := script.Config.Browser.ReviewPolicy
	var issues []string
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		action := reviewGateSurfaceAction(ref)
		label := fmt.Sprintf("%s %q", ref.Path, action.Name)
		if blockers := executionReviewBlockerCodes(action.Metadata); len(blockers) > 0 {
			detail := strings.Join(blockers, ", ")
			if len(blockers) == 1 && blockers[0] == "bruno_missing_variables" {
				if names := metadataStringSlice(action.Metadata, "missing_variables"); len(names) > 0 {
					detail = fmt.Sprintf("%s (%s)", blockers[0], strings.Join(names, ", "))
				}
			}
			issues = append(issues, fmt.Sprintf(
				"%s: execution blocker %s blocks %s until the imported behavior is resolved",
				label,
				detail,
				operation,
			))
		}
		if nativeBrunoReviewRequiredAction(action) {
			issues = append(issues, fmt.Sprintf("%s: review-required action blocks %s until review metadata is removed", label, operation))
		} else if browserProtocol && enforceBrowserPolicy && reviewGatedJavaScriptAction(action) {
			if !policy.AllowReviewedJavaScript {
				issues = append(issues, fmt.Sprintf("%s: review-gated JavaScript requires config.browser.review_policy.allow_reviewed_javascript=true before %s", label, operation))
			}
		} else if browserProtocol && enforceBrowserPolicy && reviewRequiredAction(action) {
			if !policy.AllowReviewRequired {
				issues = append(issues, fmt.Sprintf("%s: review-required action requires config.browser.review_policy.allow_review_required=true before %s", label, operation))
			}
		}
		return nil
	})
	if err != nil {
		issues = append(issues, "script review-gate traversal failed: "+err.Error())
	}
	return issues
}

func reviewGateSurfaceAction(ref dslwalk.SurfaceRef) dsl.Action {
	if ref.Action != nil {
		return *ref.Action
	}
	action := dsl.Action{
		Name:     ref.Name(),
		Metadata: ref.Metadata(),
	}
	if ref.Request != nil {
		action.Type, _ = ref.Request["type"].(string)
	}
	return action
}

func executionReviewBlockerCodes(metadata map[string]any) []string {
	codes := metadataStringSlice(metadata, "execution_blockers")
	if blocker := metadataString(metadata, "execution_blocker"); blocker != "" {
		codes = append(codes, blocker)
	}
	codes = appendUniqueStrings(nil, codes...)
	sort.Strings(codes)
	return codes
}

func scriptHasNativeBrunoReviewGate(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	found := false
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if nativeBrunoReviewRequiredAction(reviewGateSurfaceAction(ref)) {
			found = true
		}
		return nil
	})
	return found || err != nil
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
	return metadataBool(action.Metadata, "review_required") ||
		metadataBool(action.Metadata, "promotion_blocker") ||
		metadataString(action.Metadata, "review_gate") == "bruno_javascript"
}

func nativeBrunoReviewRequiredAction(action dsl.Action) bool {
	// The gate itself is the durable enforcement marker. Requiring its
	// companion flags would let an author bypass the blocker by deleting only
	// review_required or promotion_blocker while leaving untranslated Bruno
	// behavior in place.
	gate := metadataString(action.Metadata, "review_gate")
	return strings.HasPrefix(gate, "bruno_") ||
		(metadataString(action.Metadata, "source_profile") == "bruno_native" &&
			reviewRequiredAction(action))
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

func metadataStringSlice(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	var values []string
	switch raw := metadata[key].(type) {
	case []string:
		values = append(values, raw...)
	case []any:
		for _, item := range raw {
			if value, ok := item.(string); ok {
				values = append(values, value)
			}
		}
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
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
