package correlate

import (
	"math"
	"regexp"
	"strings"
)

// Rule identifiers. Each correlation candidate is tagged with the rule that
// produced it so the dashboard, migration review, and replay-repair surfaces can
// explain and group findings by response family.
const (
	RuleJSONAuthToken                = "json.auth_token"
	RuleJSONCSRFToken                = "json.csrf_token"
	RuleJSONSessionID                = "json.session_id"
	RuleJSONNumericID                = "json.numeric_id"
	RuleJSONDynamicID                = "json.dynamic_id"
	RuleHTMLHiddenToken              = "html.hidden_token"
	RuleASPNetState                  = "html.aspnet_state"
	RuleXMLSOAPToken                 = "xml.soap_token"
	RuleXMLDynamicID                 = "xml.dynamic_id"
	RuleSetCookieSession             = "cookie.session"
	RuleSetCookieOther               = "cookie.other"
	RuleDynamicHeader                = "header.dynamic"
	RuleLocationRedirect             = "header.location"
	RuleLocationParam                = "header.location_param"
	RuleWebSocketID                  = "messaging.websocket_id"
	RuleJMSCorrelationID             = "messaging.jms_correlation_id"
	RuleSalesforceAuraToken          = "salesforce.aura_token"
	RuleSalesforceAuraContextFWUID   = "salesforce.aura_context_fwuid"
	RuleSalesforceAuraAppDescriptor  = "salesforce.aura_app_descriptor"
	RuleSalesforceAuraLoaded         = "salesforce.aura_loaded"
	RuleSalesforceAuraPageURI        = "salesforce.aura_page_uri"
	RuleSalesforcePageScopeID        = "salesforce.page_scope_id"
	RuleSalesforceViewState          = "salesforce.viewstate"
	RuleSalesforceFrontdoorSID       = "salesforce.frontdoor_sid"
	RuleSalesforceConfirmationToken  = "salesforce.confirmation_token"
	RuleSalesforceCSHC               = "salesforce.cshc"
	RuleSalesforceNextRecordsURL     = "salesforce.next_records_url"
	RuleSalesforceBulkContentURL     = "salesforce.bulk_content_url"
	RuleSalesforceBulkQueryLocator   = "salesforce.bulk_query_locator"
	RuleSalesforceCompositeReference = "salesforce.composite_reference"
	RuleSalesforceCometdClientID     = "salesforce.cometd_client_id"
	RuleSalesforceAuraTokenCookie    = "salesforce.aura_token_cookie"
)

// ResponseFamily groups rules by the kind of response they inspect.
type ResponseFamily string

const (
	FamilyJSON    ResponseFamily = "json"
	FamilyHTML    ResponseFamily = "html"
	FamilyXML     ResponseFamily = "xml"
	FamilyHeader  ResponseFamily = "header"
	FamilyCookie  ResponseFamily = "cookie"
	FamilyMessage ResponseFamily = "messaging"
	FamilyForm    ResponseFamily = "form"
)

// RuleInfo documents a detection rule for UX/catalog surfaces (e.g. an API that
// lists what the correlation engine can detect). It is descriptive metadata, not
// the executable matcher.
type RuleInfo struct {
	ID             string         `json:"id"`
	Family         ResponseFamily `json:"family"`
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	BaseConfidence float64        `json:"base_confidence"`
}

// RuleCatalog returns the rule set the engine applies, ordered by family then
// confidence. Exposed so the dashboard can show "what we detect" and so docs
// stay in sync with behavior.
func RuleCatalog() []RuleInfo {
	return []RuleInfo{
		{RuleSalesforceAuraToken, FamilyForm, "Salesforce Aura token", "aura.token values from Lightning bootstrap/form traffic that are echoed in later /aura requests.", 0.95},
		{RuleSalesforceAuraContextFWUID, FamilyForm, "Salesforce Aura fwuid", "fwuid values inside aura.context that bind a Lightning component/app bundle version.", 0.94},
		{RuleSalesforceViewState, FamilyForm, "Salesforce Visualforce ViewState", "com.salesforce.visualforce.ViewState and related ViewStateMAC/CSRF hidden fields.", 0.94},
		{RuleSalesforceConfirmationToken, FamilyForm, "Salesforce confirmation token", "_CONFIRMATIONTOKEN values used by Salesforce login and challenge flows.", 0.93},
		{RuleSalesforceCSHC, FamilyForm, "Salesforce cshc handoff", "cshc values passed through Salesforce content-door and Visualforce session handoff requests.", 0.92},
		{RuleSalesforceAuraAppDescriptor, FamilyForm, "Salesforce Aura app descriptor", "Aura app descriptors from aura.context/app that are replay-sensitive in Lightning flows.", 0.9},
		{RuleSalesforceAuraLoaded, FamilyForm, "Salesforce Aura loaded map", "Aura loaded component maps from Lightning bootstrap/context payloads.", 0.88},
		{RuleSalesforceAuraPageURI, FamilyForm, "Salesforce Aura page URI", "Aura pageURI values that may need environment-aware replacement across Salesforce orgs.", 0.86},
		{RuleSalesforcePageScopeID, FamilyForm, "Salesforce pageScopeId", "Per-page pageScopeId values from aura.context that bind a Lightning page instance and are echoed in later requests.", 0.85},
		{RuleSalesforceFrontdoorSID, FamilyForm, "Salesforce frontdoor sid", "sid/frontdoor session values passed through Salesforce frontdoor redirects.", 0.85},
		{RuleSalesforceNextRecordsURL, FamilyForm, "Salesforce SOQL nextRecordsUrl", "SOQL REST Query API pagination cursor (nextRecordsUrl) reused verbatim as the next page request URL.", 0.9},
		{RuleSalesforceBulkContentURL, FamilyForm, "Salesforce Bulk contentUrl", "Bulk API 2.0 ingest job contentUrl — the relative path the CSV batch is PUT to.", 0.9},
		{RuleSalesforceBulkQueryLocator, FamilyHeader, "Salesforce Bulk query locator", "Bulk API 2.0 query result pagination cursor returned in the Sforce-Locator response header and reused as the next locator query parameter.", 0.9},
		{RuleSalesforceCompositeReference, FamilyForm, "Salesforce composite referenceId", "Server-assigned record ids from a /composite subrequest, addressed by referenceId, reused in later requests.", 0.85},
		{RuleSalesforceAuraTokenCookie, FamilyCookie, "Salesforce aura.token cookie", "__Host-ERIC_* cookie set by the Lightning bootstrap whose value later /aura requests echo as the aura.token form field.", 0.95},
		{RuleSalesforceCometdClientID, FamilyForm, "Salesforce CometD clientId", "Bayeux clientId from a /cometd /meta/handshake response (a JSON array) reused in every later /meta/connect and /meta/subscribe request.", 0.95},
		{RuleASPNetState, FamilyHTML, "ASP.NET state field", "WebForms __VIEWSTATE / __EVENTVALIDATION style hidden fields that change every request.", 0.95},
		{RuleHTMLHiddenToken, FamilyHTML, "Hidden anti-CSRF/state token", "Hidden form inputs named like csrf, xsrf, token, nonce, state, viewstate, authenticity.", 0.9},
		{RuleJSONCSRFToken, FamilyJSON, "JSON CSRF token", "JSON fields whose name contains csrf/xsrf.", 0.9},
		{RuleXMLSOAPToken, FamilyXML, "XML/SOAP token", "XML/SOAP elements or attributes named like token, csrf, nonce, session, MessageID, RelatesTo, or CorrelationID.", 0.88},
		{RuleJMSCorrelationID, FamilyMessage, "JMS correlation id", "JMSCorrelationID/JMSMessageID response fields, properties, or headers that later requests/messages reuse.", 0.88},
		{RuleJSONAuthToken, FamilyJSON, "JSON auth token", "JSON fields named like access_token, id_token, refresh_token, token.", 0.85},
		{RuleJSONSessionID, FamilyJSON, "JSON session id", "JSON fields named like session_id, sid.", 0.85},
		{RuleWebSocketID, FamilyMessage, "WebSocket correlation id", "WebSocket message ids, subscription ids, operation ids, and correlation ids returned by server messages.", 0.84},
		{RuleSetCookieSession, FamilyCookie, "Session cookie", "Set-Cookie values for session/auth/csrf cookies.", 0.8},
		{RuleDynamicHeader, FamilyHeader, "Dynamic response header", "Response headers that look dynamic (token/auth/id/csrf).", 0.8},
		{RuleLocationParam, FamilyHeader, "Redirect query parameter", "Dynamic code/state/token/session/id values extracted from Location redirect query parameters.", 0.78},
		{RuleJSONDynamicID, FamilyJSON, "JSON dynamic id", "JSON token-shaped string fields ending in id.", 0.6},
		{RuleXMLDynamicID, FamilyXML, "XML dynamic id", "XML/SOAP id-shaped elements or attributes with reusable values.", 0.6},
		{RuleLocationRedirect, FamilyHeader, "Redirect target", "Location header that may carry a one-time code/state.", 0.6},
		{RuleSetCookieOther, FamilyCookie, "Other cookie", "Set-Cookie values that are not obviously session-scoped.", 0.55},
		{RuleJSONNumericID, FamilyJSON, "JSON numeric id", "JSON numeric id fields with 4+ digits.", 0.5},
	}
}

var (
	highEntropyChars = regexp.MustCompile(`[^A-Za-z0-9]`)
	allDigits        = regexp.MustCompile(`^\d+$`)
	base64TokenChars = regexp.MustCompile(`^[A-Za-z0-9+/_-]{16,}={0,2}$`)
)

// scoreConfidence combines a candidate's rule base confidence with runtime
// signals — how many later requests reuse the value, and how "dynamic" the value
// looks — into a final 0..1 ranking, along with the reasons that moved it.
func scoreConfidence(candidate Candidate, usageCount int) (float64, []string) {
	score := candidate.BaseConfidence
	if score == 0 {
		score = 0.5
	}
	reasons := make([]string, 0, 4)
	if candidate.Reason != "" {
		reasons = append(reasons, candidate.Reason)
	}

	// Reuse is the strongest real-world signal: a value echoed back by the client
	// in several later requests is almost certainly a value that must correlate.
	switch {
	case usageCount >= 3:
		score += 0.12
		reasons = append(reasons, "reused in 3+ later requests")
	case usageCount == 2:
		score += 0.08
		reasons = append(reasons, "reused in 2 later requests")
	case usageCount == 1:
		score += 0.04
		reasons = append(reasons, "reused in a later request")
	}

	// Value shape: long, mixed-character values are more likely opaque dynamic
	// tokens; short all-digit values are weaker signals (could be static ids).
	value := candidate.Value
	switch {
	case looksJWTLike(value):
		score += 0.08
		reasons = append(reasons, "JWT-shaped token")
	case looksBase64TokenLike(value):
		score += 0.06
		reasons = append(reasons, "base64-shaped opaque token")
	case len(value) >= 24 && highEntropyChars.MatchString(value):
		score += 0.06
		reasons = append(reasons, "long, high-entropy value")
	case len(value) >= 16:
		score += 0.03
		reasons = append(reasons, "long value")
	case allDigits.MatchString(value) && len(value) <= 4:
		score -= 0.15
		reasons = append(reasons, "short numeric value (may be static)")
	}

	return clampConfidence(score), reasons
}

func looksJWTLike(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 {
		return false
	}
	total := 0
	for _, part := range parts {
		if len(part) < 6 || !base64TokenChars.MatchString(part) {
			return false
		}
		total += len(part)
	}
	return total >= 24
}

func looksBase64TokenLike(value string) bool {
	value = strings.TrimSpace(value)
	if !base64TokenChars.MatchString(value) {
		return false
	}
	hasUpper := false
	hasLower := false
	hasDigit := false
	hasSymbol := false
	for _, r := range value {
		if r >= '0' && r <= '9' {
			hasDigit = true
			continue
		}
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
			continue
		}
		if r >= 'a' && r <= 'z' {
			hasLower = true
			continue
		}
		hasSymbol = true
	}
	return len(value) >= 24 && (hasSymbol || (hasUpper && hasLower && hasDigit))
}

func clampConfidence(score float64) float64 {
	if score < 0.05 {
		return 0.05
	}
	if score > 0.99 {
		return 0.99
	}
	// Round to 2 decimals for stable, readable output.
	return math.Round(score*100) / 100
}

// HighConfidence reports whether a correlation is confident enough to apply
// automatically vs. merely suggest for review. The threshold is intentionally
// conservative — auto-applying a wrong correlation is worse than suggesting it.
func HighConfidence(c Correlation) bool {
	return c.Confidence >= 0.8
}

// FamilyForRule maps a rule ID back to its response family (best-effort).
func FamilyForRule(ruleID string) ResponseFamily {
	switch {
	case ruleID == RuleSalesforceBulkQueryLocator:
		return FamilyHeader
	case ruleID == RuleSalesforceAuraTokenCookie:
		return FamilyCookie
	case strings.HasPrefix(ruleID, "json."):
		return FamilyJSON
	case strings.HasPrefix(ruleID, "html."):
		return FamilyHTML
	case strings.HasPrefix(ruleID, "xml."):
		return FamilyXML
	case strings.HasPrefix(ruleID, "cookie."):
		return FamilyCookie
	case strings.HasPrefix(ruleID, "header."):
		return FamilyHeader
	case strings.HasPrefix(ruleID, "messaging."):
		return FamilyMessage
	case strings.HasPrefix(ruleID, "salesforce."):
		return FamilyForm
	default:
		return ""
	}
}
