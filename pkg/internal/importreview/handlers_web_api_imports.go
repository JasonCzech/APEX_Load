package importreview

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/agentforce"
	"github.com/jasonczech/apexload/pkg/convert/llmapi"
	"github.com/jasonczech/apexload/pkg/convert/mulesoft"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"net/http"
	"strings"
)

func DropMulesoftPolicies(review *mulesoft.Review, policies ...string) {
	if review == nil || len(policies) == 0 {
		return
	}
	dropped := make(map[string]struct{}, len(policies))
	for _, policy := range policies {
		dropped[policy] = struct{}{}
	}
	kept := review.PolicyFindings[:0]
	for _, finding := range review.PolicyFindings {
		if _, drop := dropped[finding.Policy]; !drop {
			kept = append(kept, finding)
		}
	}
	review.PolicyFindings = kept
	if _, drop := dropped["client_id_enforcement"]; !drop {
		return
	}
	blockers := review.Blockers[:0]
	for _, blocker := range review.Blockers {
		if !strings.Contains(blocker, "client_id/client_secret") {
			blockers = append(blockers, blocker)
		}
	}
	review.Blockers = blockers
}

func MulesoftReviewHasContent(review *mulesoft.Review) bool {
	return review != nil && (len(review.Domains) > 0 || len(review.PolicyFindings) > 0 || len(review.AuthFindings) > 0 || len(review.Blockers) > 0)
}

func AttachMuleSoftReview(script *dsl.Script, source string, sections []ScriptImportReviewSection, suppressedPolicies ...string) []ScriptImportReviewSection {
	review := mulesoft.Analyze(script)
	if !review.Detected {
		return sections
	}
	DropMulesoftPolicies(review, suppressedPolicies...)
	if !MulesoftReviewHasContent(review) {
		return sections
	}
	if script != nil {
		EnsureScriptImportPreserved(script, source)["mulesoft_review"] = review
	}
	return append(sections, MulesoftReviewSection(review))
}

func MulesoftReviewSection(review *mulesoft.Review) ScriptImportReviewSection {
	severity := "info"
	if len(review.Blockers) > 0 {
		severity = "warning"
	}
	items := make([]string, 0)
	for _, domain := range review.Domains {
		items = append(items, fmt.Sprintf("Anypoint domain: %s (%s, %d request(s))", domain.Host, domain.Kind, domain.Count))
	}
	for _, policy := range review.PolicyFindings {
		items = append(items, fmt.Sprintf("Policy: %s — %s", policy.Policy, policy.Detail))
	}
	for _, auth := range review.AuthFindings {
		items = append(items, fmt.Sprintf("Auth: %s — %s", auth.Type, auth.Detail))
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	summary := "MuleSoft/Anypoint signals were not detected."
	if review.Detected {
		summary = fmt.Sprintf("Detected MuleSoft/Anypoint signals across %d domain(s) with %d policy and %d auth finding(s).", len(review.Domains), len(review.PolicyFindings), len(review.AuthFindings))
	}
	return ScriptImportReviewSection{
		Key:      "mulesoft_review",
		Title:    "MuleSoft / Anypoint Review",
		Severity: severity,
		Summary:  summary,
		Items:    items,
	}
}

func AttachAgentforceReview(script *dsl.Script, source string, sections []ScriptImportReviewSection, transform bool) []ScriptImportReviewSection {
	review := agentforce.Analyze(script)
	if !review.Detected {
		return sections
	}
	var changes []agentforce.Change
	if transform && script != nil {
		changes = agentforce.Transform(script)
	}
	if script != nil {
		preserved := EnsureScriptImportPreserved(script, source)
		preserved["agentforce_review"] = review
		if len(changes) > 0 {
			preserved["agentforce_transform"] = changes
		}
	}
	return append(sections, AgentforceReviewSection(review, changes))
}

func AgentforceReviewSection(review *agentforce.Review, changes []agentforce.Change) ScriptImportReviewSection {
	severity := "info"
	if len(review.Blockers) > 0 {
		severity = "warning"
	}
	items := make([]string, 0)
	for _, step := range review.Lifecycle {
		items = append(items, fmt.Sprintf("Lifecycle: %s — %s %s", step.Step, step.Method, step.Path))
	}
	for _, sessionVar := range review.SessionVars {
		items = append(items, fmt.Sprintf("Correlation: extract %s via %s %q", sessionVar.Name, sessionVar.Type, sessionVar.Expression))
	}
	for _, hint := range review.Assertions {
		items = append(items, "Assertion: "+hint.Detail)
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	for _, change := range changes {
		if change.ActionName != "" {
			items = append(items, fmt.Sprintf("Applied (%s): %s — %s", change.Step, change.ActionName, change.Detail))
		} else {
			items = append(items, fmt.Sprintf("Applied (%s): %s", change.Step, change.Detail))
		}
	}
	streaming := "non-streaming"
	if review.Streaming {
		streaming = "streaming (SSE)"
	}
	summary := fmt.Sprintf("Detected an Agentforce Agent API conversation (%s) across %d lifecycle step(s).", streaming, len(review.Lifecycle))
	if len(changes) > 0 {
		summary += fmt.Sprintf(" Applied %d replay-readiness change(s).", len(changes))
	}
	return ScriptImportReviewSection{
		Key:      "agentforce_review",
		Title:    "Agentforce Agent API Review",
		Severity: severity,
		Summary:  summary,
		Items:    items,
	}
}

func AgentforceTransformRequested(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agentforce_transform"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func LLMTransformRequested(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("llm_transform"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func AttachLLMAPIReview(script *dsl.Script, source string, sections []ScriptImportReviewSection, transform bool) []ScriptImportReviewSection {
	review := llmapi.Analyze(script)
	if !review.Detected {
		return sections
	}
	var changes []llmapi.Change
	if transform && script != nil {
		type headerSnapshot struct {
			headers    map[string]string
			headerList dsl.HeaderList
		}
		snapshots := make(map[string]headerSnapshot)
		_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
			if ref.Action == nil {
				return nil
			}
			var headersCopy map[string]string
			if len(ref.Action.Headers) > 0 {
				headersCopy = make(map[string]string, len(ref.Action.Headers))
				for k, v := range ref.Action.Headers {
					headersCopy[k] = v
				}
			}
			var listCopy dsl.HeaderList
			if len(ref.Action.HeaderList) > 0 {
				listCopy = append(dsl.HeaderList(nil), ref.Action.HeaderList...)
			}
			snapshots[ref.Path] = headerSnapshot{headers: headersCopy, headerList: listCopy}
			return nil
		})
		changes = llmapi.Transform(script)
		_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
			snap, ok := snapshots[ref.Path]
			if !ok || ref.Action == nil {
				return nil
			}
			for k, priorVal := range snap.headers {
				if strings.TrimSpace(priorVal) != "" && strings.Contains(ref.Action.Headers[k], "{{secret.llm_api_key}}") && !strings.Contains(priorVal, "{{secret.llm_api_key}}") {
					ref.Action.Headers[k] = priorVal
				}
			}
			for i := range ref.Action.HeaderList {
				if i < len(snap.headerList) &&
					strings.EqualFold(ref.Action.HeaderList[i].Name, snap.headerList[i].Name) &&
					strings.TrimSpace(snap.headerList[i].Value) != "" &&
					strings.Contains(ref.Action.HeaderList[i].Value, "{{secret.llm_api_key}}") &&
					!strings.Contains(snap.headerList[i].Value, "{{secret.llm_api_key}}") {
					ref.Action.HeaderList[i].Value = snap.headerList[i].Value
				}
			}
			return nil
		})
		filtered := changes[:0]
		for _, ch := range changes {
			if ch.Step == "scrub_llm_auth" {
				continue
			}
			filtered = append(filtered, ch)
		}
		changes = filtered
	}
	if script != nil {
		preserved := EnsureScriptImportPreserved(script, source)
		preserved["llm_api_review"] = review
		if len(changes) > 0 {
			preserved["llm_api_transform"] = changes
		}
	}
	return append(sections, LLMAPIReviewSection(review, changes))
}

func LLMAPIReviewSection(review *llmapi.Review, changes []llmapi.Change) ScriptImportReviewSection {
	items := make([]string, 0, len(review.Calls)+len(changes))
	for _, call := range review.Calls {
		mode := "non-streaming"
		if call.Stream {
			mode = "streaming"
		}
		items = append(items, fmt.Sprintf("LLM call: %s — dialect=%s (%s) target=%s", call.ActionName, call.Dialect, mode, call.Target))
	}
	for _, ch := range changes {
		items = append(items, fmt.Sprintf("Applied (%s): %s — %s", ch.Step, ch.ActionName, ch.Detail))
	}
	summary := fmt.Sprintf("Detected %d LLM inference call(s) across dialect(s) %s.", len(review.Calls), strings.Join(review.Dialects, ", "))
	if len(changes) > 0 {
		summary += fmt.Sprintf(" Applied %d llm_request upgrade change(s).", len(changes))
	}
	return ScriptImportReviewSection{
		Key:      "llm_api_review",
		Title:    "LLM Inference API Review",
		Severity: "info",
		Summary:  summary,
		Items:    items,
	}
}
