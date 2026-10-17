package importreview

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/agentforce"
	"github.com/jasonczech/apexload/pkg/convert/mulesoft"
	"github.com/jasonczech/apexload/pkg/dsl"
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
