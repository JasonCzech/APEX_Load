package correlate

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type Observation struct {
	ID              int
	RequestURL      string
	RequestBody     string
	RequestHeaders  map[string]string
	ResponseBody    string
	ResponseHeaders map[string]string
	// ResponseStatus is the replay HTTP status (0 if unknown). It lets the
	// diagnosis flag a step the server rejected (>=400) even when no dynamic
	// value visibly rotated — e.g. a custom anti-replay token re-sent verbatim.
	ResponseStatus int
}

type Correlation struct {
	Name        string
	Source      string
	Expression  string
	ExtractType string
	Convert     string
	SaveOffset  int
	SaveLength  int
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
	Convert     string
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
	// auraAppPattern only matches values shaped like a Salesforce Aura app
	// descriptor — a markup:// reference or a "namespace:name" descriptor — so a
	// generic "app":"SomeName" field in unrelated JSON (analytics, feature flags)
	// is not mis-detected as an Aura correlation. See TestAuraAppPatternRequiresDescriptorShape.
	auraAppPattern         = regexp.MustCompile(`(?i)"app"\s*:\s*"(markup://[^"]+|[A-Za-z][\w$.-]*:[A-Za-z][\w$./-]*)"`)
	auraLoadedPattern      = regexp.MustCompile(`(?i)"loaded"\s*:\s*(\{[^}]{6,}\})`)
	auraPageURIPattern     = regexp.MustCompile(`(?i)(?:"pageURI"\s*:\s*"([^"]{4,})"|aura\.pageURI\s*=\s*([^&\s]{4,}))`)
	auraPageScopeIDPattern = regexp.MustCompile(`(?i)"pageScopeId"\s*:\s*"([^"]{6,})"`)
	visualforcePattern     = regexp.MustCompile(`(?i)<input[^>]*\bname="((?:com\.salesforce\.visualforce\.)?ViewState(?:MAC|Version|CSRF)?)"[^>]*\bvalue="([^"]{4,})"`)
	confirmationPattern    = regexp.MustCompile(`(?i)(?:^|[&{,"\s])_CONFIRMATIONTOKEN["']?\s*[:=]\s*["']?([^"&',}\s]{6,})`)
	frontdoorSIDPattern    = regexp.MustCompile(`(?i)(?:^|[?&{,"\s])sid(?:_Client(?:_id)?)?["']?\s*[:=]\s*["']?([A-Za-z0-9!._-]{8,})`)
	// salesforceNextRecordsURLPattern matches the SOQL REST Query API pagination
	// cursor: the response carries {"done":false,"nextRecordsUrl":"/services/data/
	// vXX/query/01g...-2000",...} and the client must GET that relative URL to page.
	// It is extracted via JSONPath and reused verbatim as the next request's target.
	salesforceNextRecordsURLPattern = regexp.MustCompile(`(?i)"nextRecordsUrl"\s*:\s*"([^"]+)"`)
	// salesforceBulkContentURLPattern matches the Bulk API 2.0 ingest job-create
	// response field {"id":"750...","contentUrl":"services/data/vXX/jobs/ingest/
	// 750.../batches",...}. contentUrl is the relative path the client PUTs the CSV
	// batch to, and it must be reused as the upload request target. It is keyed
	// narrowly on the exact field name so generic url fields are not over-matched.
	salesforceBulkContentURLPattern = regexp.MustCompile(`(?i)"contentUrl"\s*:\s*"([^"]+)"`)
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
				Convert:     candidate.Convert,
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
	uniquifyCorrelationNames(correlations)
	return correlations
}

func uniquifyCorrelationNames(correlations []Correlation) {
	used := map[string]struct{}{}
	nextSuffix := map[string]int{}
	for i := range correlations {
		base := strings.TrimSpace(correlations[i].Name)
		if base == "" {
			base = "corr_value"
		}
		name := base
		for {
			if _, exists := used[name]; !exists {
				break
			}
			nextSuffix[base]++
			if nextSuffix[base] < 2 {
				nextSuffix[base] = 2
			}
			name = fmt.Sprintf("%s_%d", base, nextSuffix[base])
		}
		if nextSuffix[base] == 0 {
			nextSuffix[base] = 1
		}
		correlations[i].Name = name
		used[name] = struct{}{}
	}
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

func ApplyVarsWithEncodedValues(value string, vars map[string]string, encodedValues map[string]bool) string {
	values := make([]string, 0, len(vars))
	for raw := range vars {
		if raw != "" {
			values = append(values, raw)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, raw := range values {
		replacement := vars[raw]
		value = strings.ReplaceAll(value, raw, replacement)
		if encodedValues[raw] {
			for _, encoded := range EncodedFormsOf(raw) {
				value = strings.ReplaceAll(value, encoded, replacement)
			}
		}
	}
	return value
}

func EncodedFormsOf(value string) []string {
	forms := make([]string, 0, 4)
	for _, encoded := range []string{
		url.QueryEscape(value),
		url.PathEscape(value),
		strings.ReplaceAll(url.QueryEscape(value), "+", "%20"),
		strings.ReplaceAll(url.QueryEscape(value), "%2B", "+"),
	} {
		if encoded == "" || encoded == value {
			continue
		}
		seen := false
		for _, existing := range forms {
			if existing == encoded {
				seen = true
				break
			}
		}
		if !seen {
			forms = append(forms, encoded)
		}
	}
	return forms
}

func DetectEncodedValueUsage(value string, surfaces []string) bool {
	encodedForms := EncodedFormsOf(value)
	if len(encodedForms) == 0 {
		return false
	}
	for _, surface := range surfaces {
		for _, encoded := range encodedForms {
			if strings.Contains(surface, encoded) {
				return true
			}
		}
	}
	return false
}

func ApplyVarsToHeadersWithEncodedValues(headers map[string]string, vars map[string]string, encodedValues map[string]bool) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	for key, value := range headers {
		headers[key] = ApplyVarsWithEncodedValues(value, vars, encodedValues)
	}
	return headers
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
	candidates := make([]Candidate, 0)
	if !looksLikeFormEncodedBody(responseBody) {
		candidates = append(candidates, salesforceRegexCandidates(responseBody)...)
	}
	for key, value := range responseHeaders {
		header := key + ": " + value
		candidates = append(candidates, salesforceRegexCandidates(header)...)
	}
	return candidates
}

func looksLikeFormEncodedBody(body string) bool {
	if !strings.Contains(body, "=") {
		return false
	}
	values, err := url.ParseQuery(body)
	return err == nil && len(values) > 0
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
	addMatches(auraPageScopeIDPattern, "salesforce_page_scope_id", RuleSalesforcePageScopeID, "Salesforce pageScopeId value", 0.85)
	addMatches(visualforcePattern, "salesforce_viewstate", RuleSalesforceViewState, "Salesforce Visualforce ViewState field", 0.94)
	addMatches(confirmationPattern, "salesforce_confirmation_token", RuleSalesforceConfirmationToken, "Salesforce confirmation token", 0.93)
	addMatches(frontdoorSIDPattern, "salesforce_frontdoor_sid", RuleSalesforceFrontdoorSID, "Salesforce frontdoor sid/session value", 0.85)
	// addJSONPath emits a clean JSONPath extractor (not a brittle regex one) for
	// values that live at a well-known top-level JSON field, so the generated
	// extractor reads $.<field> at replay rather than re-running the match regex.
	addJSONPath := func(pattern *regexp.Regexp, name, ruleID, expression, reason string, confidence float64) {
		for _, match := range pattern.FindAllStringSubmatch(body, -1) {
			value := lastNonEmptyMatch(match)
			if value == "" {
				continue
			}
			candidates = append(candidates, Candidate{
				Name:           "corr_" + Slug(name),
				Value:          value,
				Source:         "body",
				ExtractType:    "jsonpath",
				Expression:     expression,
				RuleID:         ruleID,
				BaseConfidence: confidence,
				Reason:         reason,
			})
		}
	}
	addJSONPath(salesforceNextRecordsURLPattern, "salesforce_soql_next_records_url", RuleSalesforceNextRecordsURL, "$.nextRecordsUrl", "Salesforce SOQL pagination nextRecordsUrl cursor", 0.9)
	addJSONPath(salesforceBulkContentURLPattern, "salesforce_bulk_content_url", RuleSalesforceBulkContentURL, "$.contentUrl", "Salesforce Bulk API ingest contentUrl upload target", 0.9)
	return candidates
}

func formFieldRegex(fieldName string) string {
	return fmt.Sprintf(`(?i)(?:^|&)%s=([^&]*)`, regexp.QuoteMeta(fieldName))
}

func formEncodedJSONStringFieldRegex(formField, jsonField string) string {
	return fmt.Sprintf(`(?i)(?:^|&)%s=[^&]*(?:%%22|")%s(?:%%22|")\s*(?:%%3A|:)\s*(?:%%22|")((?:%%[0-1][0-9A-Fa-f]|%%2[013-9A-Fa-f]|%%[3-9A-Fa-f][0-9A-Fa-f]|[^&%%"'])*)(?:%%22|")`,
		regexp.QuoteMeta(formField), regexp.QuoteMeta(jsonField))
}

func salesforceFormContextCandidates(fieldName, decodedValue string) []Candidate {
	candidates := make([]Candidate, 0)
	addStringField := func(pattern *regexp.Regexp, jsonField, name, ruleID, reason string, confidence float64) {
		for _, match := range pattern.FindAllStringSubmatch(decodedValue, -1) {
			value := lastNonEmptyMatch(match)
			if value == "" {
				continue
			}
			candidates = append(candidates, Candidate{
				Name:           "corr_" + Slug(name),
				Value:          value,
				Source:         "body",
				ExtractType:    "regex",
				Expression:     formEncodedJSONStringFieldRegex(fieldName, jsonField),
				Convert:        "url_decode",
				RuleID:         ruleID,
				BaseConfidence: confidence,
				Reason:         reason,
			})
		}
	}
	addStringField(auraFWUIDPattern, "fwuid", "salesforce_aura_fwuid", RuleSalesforceAuraContextFWUID, "Salesforce Aura context fwuid", 0.94)
	addStringField(auraAppPattern, "app", "salesforce_aura_app", RuleSalesforceAuraAppDescriptor, "Salesforce Aura app descriptor", 0.9)
	addStringField(auraPageScopeIDPattern, "pageScopeId", "salesforce_page_scope_id", RuleSalesforcePageScopeID, "Salesforce pageScopeId value", 0.85)
	addStringField(auraPageURIPattern, "pageURI", "salesforce_aura_page_uri", RuleSalesforceAuraPageURI, "Salesforce Aura page URI", 0.86)
	return candidates
}

func formCandidates(body string) []Candidate {
	values, err := url.ParseQuery(body)
	if err != nil || len(values) == 0 {
		return nil
	}
	candidates := make([]Candidate, 0)
	add := func(fieldName, name, value, ruleID string, confidence float64, reason string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		candidates = append(candidates, Candidate{
			Name:           "corr_" + Slug(name),
			Value:          value,
			Source:         "body",
			ExtractType:    "regex",
			Expression:     formFieldRegex(fieldName),
			Convert:        "url_decode",
			RuleID:         ruleID,
			BaseConfidence: confidence,
			Reason:         reason,
		})
	}
	for key, fieldValues := range values {
		for _, value := range fieldValues {
			switch strings.ToLower(key) {
			case "aura.token":
				add(key, "salesforce_aura_token", value, RuleSalesforceAuraToken, 0.95, "Salesforce aura.token form field")
			case "aura.context":
				for _, candidate := range salesforceFormContextCandidates(key, value) {
					candidates = append(candidates, candidate)
				}
			case "aura.pageuri":
				add(key, "salesforce_aura_page_uri", value, RuleSalesforceAuraPageURI, 0.86, "Salesforce aura.pageURI form field")
			case "com.salesforce.visualforce.viewstate", "viewstatemac", "viewstateversion", "viewstatecsrf":
				add(key, "salesforce_viewstate", value, RuleSalesforceViewState, 0.94, "Salesforce Visualforce ViewState form field")
			case "_confirmationtoken":
				add(key, "salesforce_confirmation_token", value, RuleSalesforceConfirmationToken, 0.93, "Salesforce confirmation token form field")
			case "sid", "sid_client", "sid_client_id":
				add(key, "salesforce_frontdoor_sid", value, RuleSalesforceFrontdoorSID, 0.85, "Salesforce frontdoor sid form field")
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
	if parsed, ok := jsonCandidatesFromParsedBody(body); ok {
		return parsed
	}
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

func jsonCandidatesFromParsedBody(body string) ([]Candidate, bool) {
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return nil, false
	}
	candidates := make([]Candidate, 0)
	walkJSONCandidates(value, "$", true, &candidates)
	return candidates, true
}

// walkJSONCandidates descends the parsed body collecting correlation candidates.
// The `evaluable` flag tracks whether every object-key segment of `path` so far
// is one the replay-side lite JSONPath extractor (extractJSONPathValue) can
// resolve — i.e. a dot-identifier or an array index. A non-identifier key
// (hyphenated/dotted/etc.) is emitted by jsonPathChild as a bracket-quoted
// segment (`$["x-csrf-token"]`) that the extractor cannot evaluate, so any
// candidate under such a path would silently error at replay. We keep descending
// (a deeper subtree might still be useful for review) but do not EMIT candidates
// once the path is no longer evaluable, rather than emitting broken extractors.
func walkJSONCandidates(value any, path string, evaluable bool, candidates *[]Candidate) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childEvaluable := evaluable && jsonPathIdentifier.MatchString(key)
			walkJSONCandidates(typed[key], jsonPathChild(path, key), childEvaluable, candidates)
		}
	case []any:
		for index, item := range typed {
			walkJSONCandidates(item, fmt.Sprintf("%s[%d]", path, index), evaluable, candidates)
		}
	case string:
		key := jsonPathLastKey(path)
		if key == "" || len(typed) < 6 || !dynamicJSONKey(key) || !evaluable {
			return
		}
		ruleID, base := classifyJSONKey(key)
		*candidates = append(*candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          typed,
			Source:         "body",
			ExtractType:    "jsonpath",
			Expression:     path,
			RuleID:         ruleID,
			BaseConfidence: base,
			Reason:         "JSON field " + key + " looks like a dynamic token/identifier",
		})
	case float64:
		key := jsonPathLastKey(path)
		if key == "" || !dynamicJSONKey(key) || !evaluable {
			return
		}
		number := fmt.Sprintf("%.0f", typed)
		if typed != float64(int64(typed)) {
			number = fmt.Sprint(typed)
		}
		*candidates = append(*candidates, Candidate{
			Name:           "corr_" + Slug(key),
			Value:          number,
			Source:         "body",
			ExtractType:    "jsonpath",
			Expression:     path,
			RuleID:         RuleJSONNumericID,
			BaseConfidence: 0.5,
			Reason:         "JSON numeric id field " + key,
		})
	}
}

func dynamicJSONKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	tokens := correlationKeyTokens(key)
	compact := strings.Join(tokens, "")
	switch compact {
	case "accesstoken", "idtoken", "refreshtoken", "sessionid", "jmscorrelationid", "jmsmessageid", "messageid", "operationid", "subscriptionid", "correlationid":
		return true
	}
	for _, token := range tokens {
		switch token {
		case "token", "csrf", "xsrf", "nonce", "state", "session", "sid", "auth", "authorization", "bearer", "websocket", "correlation":
			return true
		case "id":
			return true
		}
	}
	return lower == "id" ||
		strings.HasSuffix(key, "Id") ||
		strings.HasSuffix(key, "ID") ||
		strings.HasSuffix(lower, "_id") ||
		strings.HasSuffix(lower, "-id") ||
		strings.HasSuffix(lower, ".id")
}

func correlationKeyTokens(key string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	runes := []rune(strings.TrimSpace(key))
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && current.Len() > 0 {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || nextLower {
				flush()
			}
		}
		current.WriteRune(unicode.ToLower(r))
	}
	flush()
	return tokens
}

func jsonPathChild(parent string, key string) string {
	if jsonPathIdentifier.MatchString(key) {
		return parent + "." + key
	}
	return parent + `["` + strings.ReplaceAll(key, `"`, `\"`) + `"]`
}

var jsonPathIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func jsonPathLastKey(path string) string {
	if strings.HasSuffix(path, `"]`) {
		start := strings.LastIndex(path, `["`)
		if start >= 0 {
			return strings.ReplaceAll(path[start+2:len(path)-2], `\"`, `"`)
		}
	}
	lastDot := strings.LastIndex(path, ".")
	if lastDot >= 0 && lastDot+1 < len(path) {
		key := path[lastDot+1:]
		if bracket := strings.Index(key, "["); bracket >= 0 {
			key = key[:bracket]
		}
		return key
	}
	return ""
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
			ruleID, base, reason := htmlInputCorrelationRule(field)
			candidates = append(candidates, Candidate{
				Name:           "corr_" + Slug(field),
				Value:          value,
				Source:         "body",
				ExtractType:    "regex",
				Expression:     expression,
				RuleID:         ruleID,
				BaseConfidence: base,
				Reason:         reason,
			})
		}
	}
	return candidates
}

func htmlInputCorrelationRule(field string) (string, float64, string) {
	lower := strings.ToLower(strings.TrimSpace(field))
	if strings.Contains(lower, "visualforce.viewstate") {
		return RuleSalesforceViewState, 0.94, "Salesforce Visualforce ViewState hidden field"
	}
	return RuleHTMLHiddenToken, 0.9, "Hidden form field " + field + " (anti-CSRF/state token)"
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
			Expression:     "//@*[local-name()='" + localXMLName(name) + "']",
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
			if len(locationMatches) == 0 && shouldCorrelateWholeLocation(value) {
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

func shouldCorrelateWholeLocation(value string) bool {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 12 {
		return false
	}
	candidate := trimmed
	if parsed, err := url.Parse(trimmed); err == nil {
		if parsed.RawQuery != "" {
			return true
		}
		candidate = lastPathSegment(parsed.Path)
		if len(candidate) < 12 {
			return false
		}
	}
	return looksDynamicLocationValue(candidate)
}

func lastPathSegment(pathValue string) string {
	pathValue = strings.Trim(pathValue, "/")
	if pathValue == "" {
		return ""
	}
	if idx := strings.LastIndex(pathValue, "/"); idx >= 0 {
		return pathValue[idx+1:]
	}
	return pathValue
}

func looksDynamicLocationValue(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(lower, "token") ||
		strings.Contains(lower, "code") ||
		strings.Contains(lower, "state") ||
		strings.Contains(lower, "session") ||
		strings.Contains(lower, "nonce") ||
		strings.Contains(lower, "csrf") ||
		strings.Contains(lower, "xsrf") {
		return true
	}
	hasLetter := false
	hasDigit := false
	for _, r := range lower {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
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
			ExtractType:    "regex",
			Expression:     locationHeaderQueryRegex(headerName, key),
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
		if setCookieAttributeName(cookieName) {
			continue
		}
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

func locationHeaderQueryRegex(headerName string, queryKey string) string {
	return fmt.Sprintf(`(?im)^%s:\s*[^\r\n]*[?&]%s=([^&#\s]+)`, regexp.QuoteMeta(headerName), regexp.QuoteMeta(queryKey))
}

func setCookieAttributeName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "path", "domain", "max-age", "expires", "samesite", "secure", "httponly", "partitioned", "priority":
		return true
	default:
		return false
	}
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
		strings.Contains(lowerKey, "api-key") ||
		strings.Contains(lowerKey, "apikey") ||
		strings.Contains(lowerKey, "secret") ||
		strings.HasSuffix(lowerKey, "-key") ||
		strings.HasSuffix(lowerKey, "_key") ||
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
