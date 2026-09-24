package api

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
)

// Gate counts describe the batch; independent readiness checks are never cleared.
func applyJMeterReviewPreflight(p *ReplayPreflight, r *jmeter.ConvertResult) {
	if p == nil || r.ReviewSummary == nil {
		return
	}
	detail := fmt.Sprintf("%d blocking behaviors; %d behaviors requiring review", r.ReviewSummary.BlockingBehaviors, r.ReviewSummary.ReviewBehaviors)
	status := "pass"
	if r.ReviewSummary.BlockingBehaviors > 0 {
		status = "fail"
		p.Valid = false
	} else if r.ReviewSummary.ReviewBehaviors > 0 {
		status = "warn"
	}
	for i := range p.Checks {
		if p.Checks[i].ID == "review_gates" {
			p.Checks[i].Status = status
			p.Checks[i].Detail = detail
		}
	}
	// Replace only gate issues produced from these exact scripts; never remove
	// independent validation, data, credential, or runtime failures.
	gateIssues := map[string]bool{}
	for _, script := range r.Scripts {
		for _, issue := range replayReviewGateIssues(script) {
			gateIssues[issue] = true
		}
	}
	kept := p.Issues[:0]
	for _, issue := range p.Issues {
		if !gateIssues[issue] {
			kept = append(kept, issue)
		}
	}
	p.Issues = kept
	// Append the concrete, source-scoped reasons.
	for _, d := range r.Diagnostics {
		if d.Severity == diag.Blocker {
			p.Issues = appendUniqueStrings(p.Issues, d.String())
		}
	}
}
