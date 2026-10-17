package importreview

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	"sort"
	"strings"
)

func SalesforceReviewSummary(review *salesforce.Review) string {
	if review == nil {
		return ""
	}
	if !review.Detected {
		return "Salesforce native review ran but did not detect Salesforce traffic."
	}
	coverage := review.HARMatchCoverage
	return fmt.Sprintf("Detected Salesforce traffic across %d domain(s); HAR matched %d of %d imported request(s) (%.1f%%).", len(review.Domains), coverage.MatchedRequests, coverage.ImportedRequests, coverage.CoveragePct)
}

func SalesforceReviewItems(review *salesforce.Review) []string {
	if review == nil {
		return nil
	}
	items := make([]string, 0)
	for _, domain := range review.Domains {
		items = append(items, fmt.Sprintf("Domain: %s (%s, %d request(s))", domain.Host, domain.Kind, domain.Count))
	}
	if len(review.AuraEndpoints) > 0 {
		items = append(items, fmt.Sprintf("Aura endpoints detected: %d", len(review.AuraEndpoints)))
	}
	if len(review.Transactions) > 0 {
		items = append(items, fmt.Sprintf("Transaction candidates: %d", len(review.Transactions)))
		flowKinds := map[string]bool{}
		for _, transaction := range review.Transactions {
			for _, flowKind := range transaction.FlowKinds {
				if strings.TrimSpace(flowKind) != "" {
					flowKinds[flowKind] = true
				}
			}
		}
		if len(flowKinds) > 0 {
			ordered := make([]string, 0, len(flowKinds))
			for flowKind := range flowKinds {
				ordered = append(ordered, flowKind)
			}
			sort.Strings(ordered)
			items = append(items, "Salesforce flow kinds: "+strings.Join(ordered, ", "))
		}
	}
	if len(review.Parameterizations) > 0 {
		keySet := map[string]bool{}
		for _, param := range review.Parameterizations {
			if strings.TrimSpace(param.ParamKey) != "" {
				keySet[param.ParamKey] = true
			}
		}
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items = append(items, fmt.Sprintf("Aura message parameterization candidates: %d (keys: %s)", len(review.Parameterizations), strings.Join(keys, ", ")))
	}
	if review.CorrelationConfidence != "" {
		items = append(items, "Correlation confidence: "+review.CorrelationConfidence)
	}
	if outcomes := review.AuraOutcomes; outcomes != nil {
		items = append(items, fmt.Sprintf("Aura capture outcomes: %d request action(s), %d response action(s), %d matched; %d complete batch(es), %d incomplete, %d omitted.", outcomes.RequestActions, outcomes.ResponseActions, outcomes.MatchedActions, outcomes.CompleteBatches, outcomes.IncompleteBatches, outcomes.OmittedBatches))
		if !outcomes.DetailsComplete {
			items = append(items, fmt.Sprintf("Aura retained detail limits: %d outcome(s), %d captured issue(s), and %d imported request issue(s) omitted; aggregate counts cover full inspected batches.", outcomes.OutcomesOmitted, outcomes.IssuesOmitted, outcomes.RequestIssuesOmitted))
		}
		items = append(items, fmt.Sprintf("Aura response evidence: %d batch(es) without a retained response; %d invalid imported request envelope(s).", outcomes.MissingResponses, outcomes.InvalidRequests))
		states := make([]string, 0, len(outcomes.StateCounts))
		for state := range outcomes.StateCounts {
			states = append(states, string(state))
		}
		sort.Strings(states)
		for _, state := range states {
			items = append(items, fmt.Sprintf("Aura action state %s: %d", state, outcomes.StateCounts[aura.State(state)]))
		}
		codes := make([]string, 0, len(outcomes.IssueCounts))
		for code := range outcomes.IssueCounts {
			codes = append(codes, string(code))
		}
		sort.Strings(codes)
		for _, code := range codes {
			items = append(items, fmt.Sprintf("Aura capture issue %s: %d", code, outcomes.IssueCounts[aura.Code(code)]))
		}
	}
	for _, warning := range review.Warnings {
		items = append(items, "Warning: "+warning)
	}
	for _, blocker := range review.Blockers {
		items = append(items, "Blocker: "+blocker)
	}
	for _, action := range review.NextActions {
		items = append(items, "Next action: "+action)
	}
	return items
}
