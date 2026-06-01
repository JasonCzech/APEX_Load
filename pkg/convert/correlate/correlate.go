package correlate

import (
	"fmt"
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
	aspNetHiddenPattern = regexp.MustCompile(`(?i)<input[^>]*\bname="(__[A-Za-z0-9]+)"[^>]*\bvalue="([^"]*)"`)
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
	candidates = append(candidates, aspNetHiddenCandidates(responseBody)...)
	candidates = append(candidates, htmlInputCandidates(responseBody)...)
	candidates = append(candidates, jsonCandidates(responseBody)...)
	candidates = append(candidates, headerCandidates(responseHeaders)...)
	return candidates
}

func ObservationContains(observation Observation, value string) bool {
	if strings.Contains(observation.RequestURL, value) || strings.Contains(observation.RequestBody, value) {
		return true
	}
	for _, headerValue := range observation.RequestHeaders {
		if strings.Contains(headerValue, value) {
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
