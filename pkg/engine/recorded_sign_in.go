package engine

import (
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// A browser recording's Salesforce sign-in runs in init, its steps tagged
// metadata.salesforce_auth (pkg/recorder/browser). An observer whose session
// strategy signs its browser in itself (storage_state_pool, salesforce_jwt)
// skips it: the tagged top-level init steps, and a transaction marked around
// only them, do not run, and an init navigation to a Salesforce sign-in page
// opens the page the recording reached after signing in instead. The script
// keeps every action, so Verify's steps keep their positions.

// recordedSignInPlan is how an observer runs its script's recorded sign-in.
type recordedSignInPlan struct {
	// skip holds the top-level init positions that do not run.
	skip map[int]bool
	// steps counts the skipped sign-in steps, markers aside.
	steps int
	// landing is the absolute URL the recording reached after signing in, or
	// "" when it is unknown.
	landing  string
	baseURL  string
	strategy string
}

// newRecordedSignInPlan is the observer's plan, or nil when it runs its
// recorded sign-in (no session, login_steps, or no tagged steps).
func newRecordedSignInPlan(script *dsl.Script) *recordedSignInPlan {
	if script == nil || script.Config.Browser.Session == nil || !dsl.BrowserSessionSeedsContext(script.Config.Browser.Session.Strategy) {
		return nil
	}
	plan := &recordedSignInPlan{skip: map[int]bool{}, baseURL: script.Config.BaseURL, strategy: script.Config.Browser.Session.Strategy}
	last := -1
	for index, action := range script.Init {
		if recordedSignInStep(action) {
			plan.skip[index] = true
			plan.steps++
			last = index
		}
	}
	if plan.steps == 0 {
		return nil
	}
	plan.skipEnclosingTransactions(script.Init)
	plan.landing = plan.landingURL(script, last)
	return plan
}

func recordedSignInStep(action dsl.Action) bool {
	tagged, _ := action.Metadata["salesforce_auth"].(bool)
	return tagged
}

// transactionMarker reports a recorded transaction marker: whether it starts
// its transaction, and the transaction's name.
func transactionMarker(action dsl.Action) (start bool, name string, ok bool) {
	if !strings.EqualFold(strings.TrimSpace(action.Type), "source_transaction") {
		return false, "", false
	}
	switch function, _ := action.Params["function"].(string); function {
	case "lr_start_transaction":
		return true, dsl.TransactionMarkerName(action), true
	case "lr_end_transaction":
		return false, dsl.TransactionMarkerName(action), true
	}
	return false, "", false
}

// skipEnclosingTransactions skips each transaction marked around skipped
// steps only, innermost first, so a Login transaction does not record an
// empty sign-in.
func (p *recordedSignInPlan) skipEnclosingTransactions(init []dsl.Action) {
	for changed := true; changed; {
		changed = false
		for start, action := range init {
			isStart, name, ok := transactionMarker(action)
			if !ok || !isStart || p.skip[start] {
				continue
			}
			for end := start + 1; end < len(init); end++ {
				if endStart, endName, endOK := transactionMarker(init[end]); endOK && !endStart && endName == name {
					if end > start+1 && p.skippedBetween(start, end) {
						p.skip[start], p.skip[end] = true, true
						changed = true
					}
					break
				}
			}
		}
	}
}

func (p *recordedSignInPlan) skippedBetween(start, end int) bool {
	for index := start + 1; index < end; index++ {
		if !p.skip[index] {
			return false
		}
	}
	return true
}

// landingURL is the recorded page of the first step after the sign-in: the
// page a seeded observer opens instead of the sign-in page.
func (p *recordedSignInPlan) landingURL(script *dsl.Script, last int) string {
	following := append(append([]dsl.Action(nil), script.Init[last+1:]...), script.Actions...)
	for index, action := range following {
		if index+last+1 < len(script.Init) && p.skip[index+last+1] {
			continue
		}
		if _, _, marker := transactionMarker(action); marker {
			continue
		}
		page, _ := action.Metadata["source_url"].(string)
		parsed, err := url.Parse(page)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || browserprotocol.SalesforceSignInURL(page) {
			return ""
		}
		return page
	}
	return ""
}

// skips reports a top-level init action the observer does not run.
func (p *recordedSignInPlan) skips(index int) bool {
	return p != nil && p.skip[index]
}

// action is the init action as the observer runs it: a navigation to a
// Salesforce sign-in page opens the recording's landing page instead.
func (p *recordedSignInPlan) action(action dsl.Action) dsl.Action {
	if p == nil || p.landing == "" || !strings.EqualFold(strings.TrimSpace(action.Type), "browser_navigate") {
		return action
	}
	target := strings.TrimSpace(action.Target)
	if strings.HasPrefix(target, "/") && p.baseURL != "" {
		target = strings.TrimRight(p.baseURL, "/") + target
	}
	if !browserprotocol.SalesforceSignInURL(target) {
		return action
	}
	action.Target = p.landing
	return action
}
