package importreview

import (
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"sort"
	"strings"
)

func ReplayReviewGateIssues(script *dsl.Script) []string {
	return ScriptReviewGateIssues(script, "replay")
}

var ErrScriptReplayBlocked = errors.New("script replay is blocked")

type ScriptReplayBlockedError struct {
	issues []string
}

func (e *ScriptReplayBlockedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrScriptReplayBlocked, strings.Join(e.issues, "; "))
}

func (e *ScriptReplayBlockedError) Unwrap() error { return ErrScriptReplayBlocked }

func NewScriptReplayBlockedError(issues []string) error {
	return &ScriptReplayBlockedError{issues: append([]string(nil), issues...)}
}

func ReplayReviewGateError(script *dsl.Script) error {
	issues := ReplayReviewGateIssues(script)
	if len(issues) == 0 {
		return nil
	}
	return NewScriptReplayBlockedError(issues)
}

func ScriptReviewGateIssues(script *dsl.Script, operation string) []string {
	return ScriptReviewGateIssuesWithBrowserPolicy(script, operation, true)
}

func ScriptReviewGateIssuesWithBrowserPolicy(
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
		action := ReviewGateSurfaceAction(ref)
		label := fmt.Sprintf("%s %q", ref.Path, action.Name)
		if blockers := ExecutionReviewBlockerCodes(action.Metadata); len(blockers) > 0 {
			details := make([]string, 0, len(blockers))
			for _, blocker := range blockers {
				details = append(details, ExecutionBlockerDetail(blocker, action.Metadata))
			}
			detail := strings.Join(details, ", ")
			// The script editor titles this message by its shape
			// (dashboard/src/components/replayReadinessIssues.ts); a new
			// wording still shows, just untitled.
			issues = append(issues, fmt.Sprintf(
				"%s: execution blocker %s blocks %s until the imported behavior is resolved",
				label,
				detail,
				operation,
			))
		}
		if DurableImportReviewGateAction(action) {
			issues = append(issues, fmt.Sprintf("%s: review-required action blocks %s until review metadata is removed", label, operation))
		} else if browserProtocol && enforceBrowserPolicy && ReviewGatedJavaScriptAction(action) {
			if !policy.AllowReviewedJavaScript {
				issues = append(issues, fmt.Sprintf("%s: review-gated JavaScript requires config.browser.review_policy.allow_reviewed_javascript=true before %s", label, operation))
			}
		} else if browserProtocol && enforceBrowserPolicy && ReviewRequiredAction(action) {
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

func ReviewGateSurfaceAction(ref dslwalk.SurfaceRef) dsl.Action {
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

func ExecutionReviewBlockerCodes(metadata map[string]any) []string {
	codes := MetadataStringSlice(metadata, "execution_blockers")
	if blocker := MetadataString(metadata, "execution_blocker"); blocker != "" {
		codes = append(codes, blocker)
	}
	codes = AppendUniqueStrings(nil, codes...)
	sort.Strings(codes)
	return codes
}

func ReviewGatedJavaScriptAction(action dsl.Action) bool {
	if !strings.EqualFold(strings.TrimSpace(action.Type), "browser_evaluate_js") {
		return false
	}
	if MetadataString(action.Metadata, "review_gate") == "javascript" {
		return true
	}
	return MetadataBool(action.Metadata, "review_required") || MetadataBool(action.Metadata, "promotion_blocker")
}

func ReviewRequiredAction(action dsl.Action) bool {
	return MetadataBool(action.Metadata, "review_required") ||
		MetadataBool(action.Metadata, "promotion_blocker") ||
		MetadataString(action.Metadata, "review_gate") == "bruno_javascript"
}

type DurableReviewGateSource struct {
	// gatePrefix matches review_gate values this importer owns.
	gatePrefix string
	// sourceProfile additionally hardens an action carrying the ordinary
	// review_required/promotion_blocker flags when it came from this importer.
	sourceProfile string
}

var DurableReviewGateSources = []DurableReviewGateSource{
	{gatePrefix: "bruno_", sourceProfile: "bruno_native"},
}

func DurableImportReviewGateAction(action dsl.Action) bool {
	gate := MetadataString(action.Metadata, "review_gate")
	profile := MetadataString(action.Metadata, "source_profile")
	for _, source := range DurableReviewGateSources {
		if source.gatePrefix != "" && strings.HasPrefix(gate, source.gatePrefix) {
			return true
		}
		if source.sourceProfile != "" && profile == source.sourceProfile &&
			ReviewRequiredAction(action) {
			return true
		}
	}
	return false
}

func MetadataBool(metadata map[string]any, key string) bool {
	return dslwalk.MetadataBool(metadata, key)
}

func MetadataString(metadata map[string]any, key string) string {
	return strings.ToLower(dslwalk.MetadataString(metadata, key))
}

func ExecutionBlockerDetail(code string, metadata map[string]any) string {
	switch code {
	case "bruno_missing_variables":
		if names := MetadataStringSlice(metadata, "missing_variables"); len(names) > 0 {
			return fmt.Sprintf("%s (%s)", code, strings.Join(names, ", "))
		}
	case "import_review_required":
		if names := OmittedImportHeaderNames(metadata); len(names) > 0 {
			noun := "headers"
			if len(names) == 1 {
				noun = "header"
			}
			label := fmt.Sprintf("%d %s left out", len(names), noun)
			if !ImportReviewOnlyForHeaders(metadata) {
				label = "other review and " + label
			}
			return fmt.Sprintf("%s (%s: %s)", code, label, strings.Join(names, ", "))
		}
	}
	return code
}

func ImportReviewOnlyForHeaders(metadata map[string]any) bool {
	if metadata[dsl.ImportMultipartFidelityMetadataKey] != nil || metadata[requestimport.ImportHTTPVersionFidelityMetadataKey] != nil {
		return false
	}
	review, _ := metadata["import_review"].(map[string]any)
	for _, reason := range MetadataStringSlice(review, "reason_codes") {
		if !strings.HasPrefix(reason, "http_header_") {
			return false
		}
	}
	return true
}

func OmittedImportHeaderNames(metadata map[string]any) []string {
	marker, _ := metadata[dsl.ImportHeaderFidelityMetadataKey].(map[string]any)
	if marker == nil {
		return nil
	}
	var occurrences []map[string]any
	switch typed := marker["omitted_occurrences"].(type) {
	case []map[string]any:
		occurrences = typed
	case []any:
		for _, entry := range typed {
			if occurrence, ok := entry.(map[string]any); ok {
				occurrences = append(occurrences, occurrence)
			}
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, occurrence := range occurrences {
		name, _ := occurrence["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" || name == "<header>" || name == "<invalid-header>" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func MetadataStringSlice(metadata map[string]any, key string) []string {
	values := dslwalk.MetadataStrings(metadata, key)
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func AppendUniqueStrings(values []string, extra ...string) []string {
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
