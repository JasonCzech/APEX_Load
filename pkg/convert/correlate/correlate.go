package correlate

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type Observation struct {
	ID              int
	RequestURL      string
	RequestBody     string
	RequestHeaders  map[string]string
	ResponseBody    string
	ResponseHeaders map[string]string
}

type Correlation struct {
	Name        string
	Source      string
	Expression  string
	ExtractType string
	FoundInID   int
	UsedInIDs   []int
	Value       string
	SampleValue string
	// RuleID identifies which detection rule produced this correlation, so the
	// dashboard and migration review can explain *why* a value was flagged.
	RuleID string
	// Confidence is a 0..1 ranking combining the rule's base confidence with
	// runtime signals (reuse count, value entropy). Higher = more certain this
	// is a genuine dynamic value that must be correlated.
	Confidence float64
	// Reasons are human-readable factors that contributed to the confidence.
	Reasons []string
}

type Candidate struct {
	Name        string
	Value       string
	Source      string
	ExtractType string
	Expression  string
	// RuleID and BaseConfidence are assigned by the rule that emitted the
	// candidate; Reason is a short description of the matched pattern.
	RuleID         string
	BaseConfidence float64
	Reason         string
}

var (
	jsonStringTokenPattern = regexp.MustCompile(`(?i)"([A-Za-z0-9_]*(?:token|access_token|id_token|refresh_token|session_id|sessionid|sid|csrf|_token|xsrf|auth|id)[A-Za-z0-9_]*)"\s*:\s*"([^"]{6,})"`)
	jsonNumberIDPattern    = regexp.MustCompile(`(?i)"([A-Za-z0-9_]*(?:id)[A-Za-z0-9_]*)"\s*:\s*(\d{4,})`)
	htmlInputPatterns      = []struct {
		re         *regexp.Regexp
		nameGroup  int
		valueGroup int
	}{
		{regexp.MustCompile(`(?i)<input[^>]*\bname="([A-Za-z0-9_.\-\[\]]*(?:csrf|xsrf|token|nonce|state|viewstate|authenticity)[A-Za-z0-9_.\-\[\]]*)"[^>]*\bvalue="([^"]{4,})"`), 1, 2},
		{regexp.MustCompile(`(?i)<input[^>]*\bvalue="([^"]{4,})"[^>]*\bname="([A-Za-z0-9_.\-\[\]]*(?:csrf|xsrf|token|nonce|state|viewstate|authenticity)[A-Za-z0-9_.\-\[\]]*)"`), 2, 1},
	}
	nonWordCorrelationChars = regexp.MustCompile(`[^a-z0-9]+`)
	setCookieValuePattern   = regexp.MustCompile(`(?i)([A-Za-z0-9_.\-]+)=([^;\s,]{4,})`)
	// ASP.NET WebForms hidden state fields (__VIEWSTATE, __EVENTVALIDATION, …).
	aspNetHiddenPattern    = regexp.MustCompile(`(?i)<input[^>]*\bname="(__[A-Za-z0-9]+)"[^>]*\bvalue="([^"]*)"`)
	xmlElementTokenPattern = regexp.MustCompile(`(?is)<(?:[A-Za-z0-9_.-]+:)?([A-Za-z0-9_.-]*(?:token|session|csrf|xsrf|nonce|state|messageid|relatesto|correlationid|correlation|requestid|id)[A-Za-z0-9_.-]*)\b[^>]*>\s*([^<\s][^<]{5,})\s*</(?:[A-Za-z0-9_.-]+:)?[A-Za-z0-9_.-]+>`)
	xmlAttrTokenPattern    = regexp.MustCompile(`(?is)\b(?:[A-Za-z0-9_.-]+:)?([A-Za-z0-9_.-]*(?:token|session|csrf|xsrf|nonce|state|messageid|relatesto|correlationid|correlation|requestid|id)[A-Za-z0-9_.-]*)\s*=\s*"([^"]{6,})"`)
	auraTokenPattern       = regexp.MustCompile(`(?i)(?:^|[&{,"\s])aura\.token["']?\s*[:=]\s*["']?([^"&',}\s]{6,})`)
	auraFWUIDPattern       = regexp.MustCompile(`(?i)"fwuid"\s*:\s*"([^"]{6,})"`)
	auraAppPattern         = regexp.MustCompile(`(?i)"app"\s*:\s*"([^"]{4,})"`)
	auraLoadedPattern      = regexp.MustCompile(`(?i)"loaded"\s*:\s*(\{[^}]{6,}\})`)
	auraPageURIPattern     = regexp.MustCompile(`(?i)(?:"pageURI"\s*:\s*"([^"]{4,})"|aura\.pageURI\s*=\s*([^&\s]{4,}))`)
	visualforcePattern     = regexp.MustCompile(`(?i)<input[^>]*\bname="((?:com\.salesforce\.visualforce\.)?ViewState(?:MAC|Version|CSRF)?)"[^>]*\bvalue="([^"]{4,})"`)
	confirmationPattern    = regexp.MustCompile(`(?i)(?:^|[&{,"\s])_CONFIRMATIONTOKEN["']?\s*[:=]\s*["']?([^"&',}\s]{6,})`)
	frontdoorSIDPattern    = regexp.MustCompile(`(?i)(?:^|[?&{,"\s])sid(?:_Client(?:_id)?)?["']?\s*[:=]\s*["']?([A-Za-z0-9!._-]{8,})`)
)

func Detect(observations []Observation) []Correlation {
	seen := map[string]bool{}
	correlations := make([]Correlation, 0)
	for index, observation := range observations {
		if observation.ResponseBody == "" && len(observation.ResponseHeaders) == 0 {
			continue
		}
		for _, candidate := range Candidates(observation.ResponseBody, observation.ResponseHeaders) {
			if candidate.Value == "" || seen[candidate.Value] {
				continue
			}
			usedIn := make([]int, 0)
			for later := index + 1; later < len(observations); later++ {
				if ObservationContains(observations[later], candidate.Value) {
					usedIn = append(usedIn, observations[later].ID)
				}
			}
			if len(usedIn) == 0 {
				continue
			}
			seen[candidate.Value] = true
			confidence, reasons := scoreConfidence(candidate, len(usedIn))
			correlations = append(correlations, Correlation{
				Name:        candidate.Name,
				Source:      candidate.Source,
				Expression:  candidate.Expression,
				ExtractType: candidate.ExtractType,
				FoundInID:   observation.ID,
				UsedInIDs:   usedIn,
				Value:       candidate.Value,
				SampleValue: Truncate(candidate.Value, 80),
				RuleID:      candidate.RuleID,
				Confidence:  confidence,
				Reasons:     reasons,
			})
		}
	}
	// Rank by confidence (desc) so the most certain correlations surface first,
	// falling back to source order for stable, deterministic output.
	sort.SliceStable(correlations, func(i, j int) bool {
		if correlations[i].Confidence != correlations[j].Confidence {
			return correlations[i].Confidence > correlations[j].Confidence
		}
		if correlations[i].FoundInID == correlations[j].FoundInID {
			return correlations[i].Name < correlations[j].Name
		}
		return correlations[i].FoundInID < correlations[j].FoundInID
	})
	return correlations
}

func Candidates(responseBody string, responseHeaders map[string]string) []Candidate {
	candidates := make([]Candidate, 0)
	candidates = append(candidates, salesforceCandidates(responseBody, responseHeaders)...)
	candidates = append(candidates, formCandidates(responseBody)...)
	candidates = append(candidates, aspNetHiddenCandidates(responseBody)...)
	candidates = append(candidates, htmlInputCandidates(responseBody)...)
	candidates = append(candidates, jsonCandidates(responseBody)...)
	candidates = append(candidates, xmlCandidates(responseBody)...)
	candidates = append(candidates, headerCandidates(responseHeaders)...)
	return candidates
}

func ObservationContains(observation Observation, value string) bool {
	if strings.Contains(observation.RequestURL, value) || strings.Contains(observation.RequestBody, value) {
		return true
	}
	if decodedContains(observation.RequestURL, value) || decodedContains(observation.RequestBody, value) {
		return true
	}
	if formContainsValue(observation.RequestBody, value) {
		return true
	}
	for _, headerValue := range observation.RequestHeaders {
		if strings.Contains(headerValue, value) || decodedContains(headerValue, value) {
			return true
		}
	}
	return false
}

func BuildVarMap(correlations []Correlation) map[string]string {
	m := make(map[string]string, len(correlations))
	for _, corr := range correlations {
		value := corr.Value
		if value == "" {
			value = corr.SampleValue
		}
		m[value] = "{{var." + corr.Name + "}}"
	}
	return m
}

func ApplyVars(value string, vars map[string]string) string {
	values := make([]string, 0, len(vars))
	for raw := range vars {
		if raw != "" {
			values = append(values, raw)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, raw := range values {
		value = strings.ReplaceAll(value, raw, vars[raw])
	}
	return value
}

func ApplyVarsToHeaders(headers map[string]string, vars map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	for key, value := range headers {
		headers[key] = ApplyVars(value, vars)
	}
	return headers
}

func Slug(value string) string {
	slug := nonWordCorrelationChars.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		return "value"
	}
	return slug
}

func Truncate(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "..."
}

func salesforceCandidates(responseBody string, responseHeaders map[string]string) []Candidate {
	decoded := urlDecoded(responseBody)
	candidates := make([]Candidate, 0)
	candidates = append(candidates, salesforceRegexCandidates(decoded)...)
	for key, value := range responseHeaders {
		header := key + ": " + value
		candidates = append(candidates, salesforceRegexCandidates(header)...)
	}
	return candidates
}

func salesforceRegexCandidates(body string) []Candidate {
	candidates := make([]Candidate, 0)
	addMatches := func(pattern *regexp.Regexp, name, ruleID, reason string, confidence float64) {
		for _, match := range pattern.FindAllStringSubmatch(body, -1) {
			value := lastNonEmptyMatch(match)
			if value == "" {
				continue
			}
			candidates = append(candidates, Candidate{
				Name:           "corr_" + Slug(name),
				Value:          value,
				Source:         "body",
				ExtractType:    "regex",
				Expression:     pattern.String(),
				RuleID:         ruleID,
				BaseConfidence: confidence,
				Reason:         reason,
			})
		}
	}
	addMatches(auraTokenPattern, "salesforce_aura_token", RuleSalesforceAuraToken, "Salesforce aura.token value", 0.95)
	addMatches(auraFWUIDPattern, "salesforce_aura_fwuid", RuleSalesforceAuraContextFWUID, "Salesforce Aura context fwuid", 0.94)
	addMatches(auraAppPattern, "salesforce_aura_app", RuleSalesforceAuraAppDescriptor, "Salesforce Aura app descriptor", 0.9)
	addMatches(auraLoadedPattern, "salesforce_aura_loaded", RuleSalesforceAuraLoaded, "Salesforce Aura loaded map", 0.88)
	addMatches(auraPageURIPattern, "salesforce_aura_page_uri", RuleSalesforceAuraPageURI, "Salesforce Aura page URI", 0.86)
	addMatches(visualforcePattern, "salesforce_viewstate", RuleSalesforceViewState, "Salesforce Visualforce ViewState field", 0.94)
	addMatches(confirmationPattern, "salesforce_confirmation_token", RuleSalesforceConfirmationToken, "Salesforce confirmation token", 0.93)
	addMatches(frontdoorSIDPattern, "salesforce_frontdoor_sid", RuleSalesforceFrontdoorSID, "Salesforce frontdoor sid/session value", 0.85)
	return candidates
}

func formCandidates(body string) []Candidate {
	values, err := url.ParseQuery(body)
	if err != nil || len(values) == 0 {
		return nil
	}
	candidates := make([]Candidate, 0)
	add := func(name, value, ruleID string, confidence float64, reason string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    "boundary",
			Expression:     name + "=|||&",
			RuleID:         ruleID,
			BaseConfidence: confidence,
			Reason:         reason,
		})
	}
	for key, fieldValues := range values {
		for _, value := range fieldValues {
			switch strings.ToLower(key) {
			case "aura.token":
				add("salesforce_aura_token", value, RuleSalesforceAuraToken, 0.95, "Salesforce aura.token form field")
			case "aura.context":
				for _, candidate := range salesforceRegexCandidates(value) {
					candidates = append(candidates, candidate)
				}
			case "aura.pageuri":
				add("salesforce_aura_page_uri", value, RuleSalesforceAuraPageURI, 0.86, "Salesforce aura.pageURI form field")
			case "com.salesforce.visualforce.viewstate", "viewstatemac", "viewstateversion", "viewstatecsrf":
				add("salesforce_viewstate", value, RuleSalesforceViewState, 0.94, "Salesforce Visualforce ViewState form field")
			case "_confirmationtoken":
				add("salesforce_confirmation_token", value, RuleSalesforceConfirmationToken, 0.93, "Salesforce confirmation token form field")
			case "sid", "sid_client", "sid_client_id":
				add("salesforce_frontdoor_sid", value, RuleSalesforceFrontdoorSID, 0.85, "Salesforce frontdoor sid form field")
			}
		}
	}
	return candidates
}

func lastNonEmptyMatch(match []string) string {
	for i := len(match) - 1; i >= 1; i-- {
		if strings.TrimSpace(match[i]) != "" {
			return strings.TrimSpace(match[i])
		}
	}
	return ""
}

func decodedContains(raw, value string) bool {
	decoded := urlDecoded(raw)
	return decoded != raw && strings.Contains(decoded, value)
}

func formContainsValue(body, value string) bool {
	values, err := url.ParseQuery(body)
	if err != nil || len(values) == 0 {
		return false
	}
	for key, vals := range values {
		if strings.Contains(key, value) {
			return true
		}
		for _, val := range vals {
			if strings.Contains(val, value) {
				return true
			}
		}
	}
	return false
}

func urlDecoded(raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}

func jsonCandidates(body string) []Candidate {
	candidates := make([]Candidate, 0)
	for _, match := range jsonStringTokenPattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 3 {
			continue
		}
		key := match[1]
		ruleID, base := classifyJSONKey(key)
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          match[2],
			Source:         "body",
			ExtractType:    "jsonpath",
			Expression:     "$." + key,
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "JSON field " + key + " looks like a dynamic token/identifier",
		})
	}
	for _, match := range jsonNumberIDPattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 3 {
			continue
		}
		key := match[1]
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          match[2],
			Source:         "body",
			ExtractType:    "jsonpath",
			Expression:     "$." + key,
			RuleID:         RuleJSONNumericID,
			BaseConfidence: 0.5,
			Reason:         "JSON numeric id field " + key,
		})
	}
	return candidates
}

// classifyJSONKey picks the most specific rule (and base confidence) for a JSON
// key, so CSRF/auth tokens rank above generic ids.
func classifyJSONKey(key string) (string, float64) {
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "jmscorrelationid"), strings.Contains(lower, "jmsmessageid"),
		strings.Contains(lower, "jms_correlation"), strings.Contains(lower, "jms_message"):
		return RuleJMSCorrelationID, 0.88
	case strings.Contains(lower, "websocket"), strings.Contains(lower, "subscriptionid"),
		strings.Contains(lower, "operationid"), strings.Contains(lower, "messageid"),
		strings.Contains(lower, "correlationid"), strings.Contains(lower, "correlation_id"):
		return RuleWebSocketID, 0.84
	case strings.Contains(lower, "csrf"), strings.Contains(lower, "xsrf"):
		return RuleJSONCSRFToken, 0.9
	case strings.Contains(lower, "access_token"), strings.Contains(lower, "id_token"),
		strings.Contains(lower, "refresh_token"), strings.Contains(lower, "token"):
		return RuleJSONAuthToken, 0.85
	case strings.Contains(lower, "session"), strings.Contains(lower, "sid"):
		return RuleJSONSessionID, 0.85
	default:
		return RuleJSONDynamicID, 0.6
	}
}

func htmlInputCandidates(body string) []Candidate {
	candidates := make([]Candidate, 0)
	for _, pattern := range htmlInputPatterns {
		for _, match := range pattern.re.FindAllStringSubmatch(body, -1) {
			if len(match) <= pattern.valueGroup || len(match) <= pattern.nameGroup {
				continue
			}
			field := match[pattern.nameGroup]
			value := match[pattern.valueGroup]
			if value == "" {
				continue
			}
			expression := fmt.Sprintf(`name="%s"[^>]*value="([^"]+)"`, regexp.QuoteMeta(field))
			if pattern.nameGroup > pattern.valueGroup {
				expression = fmt.Sprintf(`value="([^"]+)"[^>]*name="%s"`, regexp.QuoteMeta(field))
			}
			candidates = append(candidates, Candidate{
				Name:           "corr_" + Slug(field),
				Value:          value,
				Source:         "body",
				ExtractType:    "regex",
				Expression:     expression,
				RuleID:         RuleHTMLHiddenToken,
				BaseConfidence: 0.9,
				Reason:         "Hidden form field " + field + " (anti-CSRF/state token)",
			})
		}
	}
	return candidates
}

// aspNetHiddenCandidates handles ASP.NET WebForms state fields, which are the
// classic correlation case for .NET apps and a frequent LoadRunner migration
// pain point. They are large opaque hidden inputs that change every request.
func aspNetHiddenCandidates(body string) []Candidate {
	candidates := make([]Candidate, 0)
	for _, match := range aspNetHiddenPattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 3 {
			continue
		}
		field := match[1]
		value := match[2]
		if value == "" {
			continue
		}
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(field),
			Value:          value,
			Source:         "body",
			ExtractType:    "regex",
			Expression:     fmt.Sprintf(`name="%s"[^>]*value="([^"]+)"`, regexp.QuoteMeta(field)),
			RuleID:         RuleASPNetState,
			BaseConfidence: 0.95,
			Reason:         "ASP.NET state field " + field,
		})
	}
	return candidates
}

func xmlCandidates(body string) []Candidate {
	candidates := make([]Candidate, 0)
	for _, match := range xmlElementTokenPattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 3 {
			continue
		}
		name := match[1]
		value := strings.TrimSpace(match[2])
		if value == "" {
			continue
		}
		ruleID, base := classifyXMLName(name)
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    "xpath",
			Expression:     "//*[local-name()='" + localXMLName(name) + "']/text()",
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "XML/SOAP element " + name + " looks dynamic",
		})
	}
	for _, match := range xmlAttrTokenPattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 3 {
			continue
		}
		name := match[1]
		value := strings.TrimSpace(match[2])
		if value == "" {
			continue
		}
		ruleID, base := classifyXMLName(name)
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    "xpath",
			Expression:     "//*[@*[local-name()='" + localXMLName(name) + "']]",
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "XML/SOAP attribute " + name + " looks dynamic",
		})
	}
	return candidates
}

func classifyXMLName(name string) (string, float64) {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "jmscorrelationid"), strings.Contains(lower, "jmsmessageid"):
		return RuleJMSCorrelationID, 0.88
	case strings.Contains(lower, "messageid"), strings.Contains(lower, "relatesto"),
		strings.Contains(lower, "correlationid"), strings.Contains(lower, "correlation"):
		return RuleXMLSOAPToken, 0.88
	case strings.Contains(lower, "token"), strings.Contains(lower, "session"),
		strings.Contains(lower, "csrf"), strings.Contains(lower, "xsrf"),
		strings.Contains(lower, "nonce"), strings.Contains(lower, "state"):
		return RuleXMLSOAPToken, 0.88
	default:
		return RuleXMLDynamicID, 0.6
	}
}

func localXMLName(name string) string {
	if _, after, ok := strings.Cut(name, ":"); ok {
		return after
	}
	return name
}

func headerCandidates(headers map[string]string) []Candidate {
	candidates := make([]Candidate, 0)
	// Deterministic key order so equal-confidence output is stable.
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := headers[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		lowerKey := strings.ToLower(key)
		if lowerKey == "set-cookie" {
			candidates = append(candidates, setCookieCandidates(key, value)...)
			continue
		}
		if lowerKey == "location" {
			locationMatches := locationCandidates(key, value)
			candidates = append(candidates, locationMatches...)
			if len(locationMatches) == 0 {
				// A redirect target frequently carries a one-time code/state that the
				// next request must echo back. Capture the whole value for review.
				candidates = append(candidates, Candidate{
					Name:           "corr_location",
					Value:          value,
					Source:         "header",
					ExtractType:    "header",
					Expression:     key,
					RuleID:         RuleLocationRedirect,
					BaseConfidence: 0.6,
					Reason:         "Location redirect target may carry a one-time code/state",
				})
			}
			continue
		}
		if isJMSHeaderName(lowerKey) {
			candidates = append(candidates, Candidate{
				Name:           "corr_" + Slug(lowerKey),
				Value:          value,
				Source:         "header",
				ExtractType:    "header",
				Expression:     key,
				RuleID:         RuleJMSCorrelationID,
				BaseConfidence: 0.88,
				Reason:         "JMS correlation/message header " + key,
			})
			continue
		}
		if isWebSocketHeaderName(lowerKey) {
			candidates = append(candidates, Candidate{
				Name:           "corr_" + Slug(lowerKey),
				Value:          value,
				Source:         "header",
				ExtractType:    "header",
				Expression:     key,
				RuleID:         RuleWebSocketID,
				BaseConfidence: 0.84,
				Reason:         "WebSocket correlation header " + key,
			})
			continue
		}
		if !dynamicHeaderName(lowerKey) || len(value) < 4 {
			continue
		}
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(lowerKey),
			Value:          value,
			Source:         "header",
			ExtractType:    "header",
			Expression:     key,
			RuleID:         RuleDynamicHeader,
			BaseConfidence: 0.8,
			Reason:         "Response header " + key + " looks dynamic (token/auth/id)",
		})
	}
	return candidates
}

func locationCandidates(headerName string, value string) []Candidate {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil
	}
	params := parsed.Query()
	if len(params) == 0 {
		return nil
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	candidates := make([]Candidate, 0, len(keys))
	for _, key := range keys {
		values := params[key]
		if len(values) == 0 || !dynamicLocationParamName(key) || len(values[0]) < 4 {
			continue
		}
		candidates = append(candidates, Candidate{
			Name:           "corr_location_" + Slug(key),
			Value:          values[0],
			Source:         "header",
			ExtractType:    "header_query",
			Expression:     headerName + "?" + key,
			RuleID:         RuleLocationParam,
			BaseConfidence: 0.78,
			Reason:         "Location query parameter " + key + " looks dynamic",
		})
	}
	return candidates
}

func setCookieCandidates(headerName string, value string) []Candidate {
	candidates := make([]Candidate, 0)
	for _, match := range setCookieValuePattern.FindAllStringSubmatch(value, -1) {
		if len(match) < 3 {
			continue
		}
		cookieName := match[1]
		cookieValue := match[2]
		ruleID, base := RuleSetCookieOther, 0.55
		if isSessionCookieName(cookieName) {
			ruleID, base = RuleSetCookieSession, 0.8
		}
		candidates = append(candidates, Candidate{
			Name:           "corr_cookie_" + Slug(cookieName),
			Value:          cookieValue,
			Source:         "header",
			ExtractType:    "regex",
			Expression:     fmt.Sprintf(`(?i)%s:\s*%s=([^;\s,]+)`, regexp.QuoteMeta(headerName), regexp.QuoteMeta(cookieName)),
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "Set-Cookie " + cookieName,
		})
	}
	return candidates
}

func isSessionCookieName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "session") || strings.Contains(lower, "sid") ||
		strings.Contains(lower, "auth") || strings.Contains(lower, "csrf") ||
		strings.Contains(lower, "xsrf") || strings.Contains(lower, "token")
}

func dynamicHeaderName(lowerKey string) bool {
	return strings.Contains(lowerKey, "token") ||
		strings.Contains(lowerKey, "cookie") ||
		strings.Contains(lowerKey, "location") ||
		strings.Contains(lowerKey, "id") ||
		strings.Contains(lowerKey, "auth") ||
		strings.Contains(lowerKey, "csrf") ||
		strings.Contains(lowerKey, "xsrf")
}

func dynamicLocationParamName(key string) bool {
	lower := strings.ToLower(key)
	return strings.Contains(lower, "code") ||
		strings.Contains(lower, "state") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "session") ||
		strings.Contains(lower, "csrf") ||
		strings.Contains(lower, "xsrf") ||
		strings.Contains(lower, "nonce") ||
		strings.Contains(lower, "id")
}

func isJMSHeaderName(lowerKey string) bool {
	return strings.Contains(lowerKey, "jmscorrelationid") ||
		strings.Contains(lowerKey, "jms-correlation-id") ||
		strings.Contains(lowerKey, "jmsmessageid") ||
		strings.Contains(lowerKey, "jms-message-id")
}

func isWebSocketHeaderName(lowerKey string) bool {
	return strings.Contains(lowerKey, "websocket-correlation") ||
		strings.Contains(lowerKey, "websocket-message") ||
		strings.Contains(lowerKey, "ws-correlation") ||
		strings.Contains(lowerKey, "ws-message")
}
