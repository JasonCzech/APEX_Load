package salesforce

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

// auraTokenProvenanceWarning reports Aura requests in the native script that
// still send the recorded aura.token. Salesforce rejects a stale token in a
// new session, so every such request fails at replay. The capture either has
// no producer (Chrome DevTools HAR exports drop Set-Cookie, which carries the
// desktop __Host-ERIC_* token) or has one that could not be applied
// automatically. sanitized names a sanitized export as the likely cause of a
// missing producer; it does not apply when the capture has one. When the
// token is read from the uncaptured bootstrap cookie instead, it notes that
// the first replay confirms that inferred extractor.
func auraTokenProvenanceWarning(script *dsl.Script, correlations []correlate.Correlation, sanitized bool) string {
	refs, err := flattenRequestActionsChecked(script, false)
	if err != nil {
		return ""
	}
	literal := 0
	values := map[string]bool{}
	for _, ref := range refs {
		if !isAuraBatch(ref) {
			continue
		}
		body := ref.Action.Body
		if len(body) > aura.MaxEnvelopeBytes || strings.Count(body, "&")+1 > aura.MaxFormFields || !strings.Contains(body, "aura.token=") {
			continue
		}
		fields, err := url.ParseQuery(body)
		if err != nil {
			continue
		}
		token := fields.Get("aura.token")
		// Guest and pre-login Lightning pages send a placeholder, not a token.
		if token == "" || strings.Contains(token, "{{") || token == "undefined" || token == "null" {
			continue
		}
		literal++
		values[token] = true
	}
	inferred := false
	for _, corr := range correlations {
		if corr.RuleID == correlate.RuleSalesforceAuraTokenUncapturedCookie {
			inferred = true
			break
		}
	}
	if literal == 0 {
		if inferred {
			return "Aura requests send the aura.token read at replay from the __Host-ERIC_* Set-Cookie of /one/one.app. The capture holds no Set-Cookie to confirm it (likely a sanitized export), so the first replay confirms it: if that response does not set the cookie, every Aura request fails with an invalid token."
		}
		return ""
	}
	for _, corr := range correlations {
		if values[corr.Value] && corr.RuleID != correlate.RuleSalesforceAuraTokenUncapturedCookie {
			return fmt.Sprintf("%d Aura request(s) send the recorded aura.token although the capture contains a producer for it; review the aura.token correlation before replay, or every one of these requests fails with a stale token.", literal)
		}
	}
	warning := fmt.Sprintf("%d Aura request(s) send the recorded aura.token and the capture contains no response that issues it, so every one of them fails with a stale token at replay. Desktop Lightning issues it in the __Host-ERIC_* Set-Cookie of /one/one.app, which Chrome DevTools HAR exports omit; the Salesforce Mobile SDK returns it as csrf_token from /services/oauth2/token. Re-capture with response cookies (a proxy capture or a HAR export that keeps Set-Cookie) so it can be correlated.", literal)
	if sanitized {
		warning += " This capture has no Cookie, Set-Cookie or Authorization header and no cookies[] entry at all, so it looks like a sanitized export: the cookie that issues aura.token was likely removed. Re-export it with Chrome DevTools \"Export HAR (with sensitive data)\", or record with the APEX recorder."
	}
	return warning
}
