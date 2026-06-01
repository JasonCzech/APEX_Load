package recorder

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type"`
	FoundInSeq  int    `json:"found_in_seq"`
	UsedInSeqs  []int  `json:"used_in_seqs"`
	SampleValue string `json:"sample_value"`
}

type ConvertResult struct {
	Script       *dsl.Script   `json:"script"`
	Correlations []Correlation `json:"correlations"`
	Warnings     []string      `json:"warnings,omitempty"`
}

// ConvertOptions toggles optional, opt-in conversion behaviors.
type ConvertOptions struct {
	// Parameterize promotes recorded input values (form fields / query params)
	// into a generated data source so the script is data-driven instead of
	// replaying hard-coded values.
	Parameterize bool
}

// ConvertSession converts a recording with default options.
func ConvertSession(session *Session, name string) *ConvertResult {
	return ConvertSessionWithOptions(session, name, ConvertOptions{})
}

// ConvertSessionWithOptions converts a recording, applying the supplied options.
func ConvertSessionWithOptions(session *Session, name string, opts ConvertOptions) *ConvertResult {
	if name == "" {
		name = session.Name
	}

	result := &ConvertResult{}

	correlations := detectCorrelations(session.Requests)
	result.Correlations = correlations

	corrMap := buildCorrelationMap(correlations)

	script := &dsl.Script{
		Name:        name,
		Version:     "1.0",
		Protocol:    "http",
		GeneratedBy: "recorder",
		Config: dsl.ScriptConfig{
			BaseURL: session.BaseURL,
			ThinkTime: dsl.ThinkTimeConfig{
				Type:   "recorded",
				MeanMs: 0,
			},
		},
		CreatedAt: time.Now(),
	}
	recordedResourceURLs := make([]string, 0)
	recordedResourceSeen := make(map[string]struct{})

	for i, req := range session.Requests {
		if req.IsStatic && !req.Excluded {
			rawURL := strings.TrimSpace(req.URL)
			if rawURL != "" {
				if _, ok := recordedResourceSeen[rawURL]; !ok {
					recordedResourceSeen[rawURL] = struct{}{}
					recordedResourceURLs = append(recordedResourceURLs, rawURL)
				}
			}
		}
		if req.IsStatic || req.Excluded {
			continue
		}

		target := req.Path
		if session.BaseURL != "" && strings.HasPrefix(req.URL, session.BaseURL) {
			target = strings.TrimPrefix(req.URL, session.BaseURL)
		}
		if target == "" {
			target = "/"
		}

		target = applyCorrelationVars(target, corrMap)

		actionName := fmt.Sprintf("%s %s", req.Method, simplifyPath(req.Path))
		if req.StepName != "" {
			actionName = req.StepName
		}
		if i < 10 {
			actionName = fmt.Sprintf("%02d - %s", i+1, actionName)
		} else {
			actionName = fmt.Sprintf("%d - %s", i+1, actionName)
		}

		action := dsl.Action{
			Name:   actionName,
			Type:   "request",
			Method: req.Method,
			Target: target,
		}

		// Carry the curated transaction name through to the engine, which groups
		// metrics by action metadata "source_transaction" (see
		// engine.applyProtocolResultSourceMetadata). Without this the transaction
		// names users assign in the recording studio are silently dropped.
		if txn := strings.TrimSpace(req.TransactionName); txn != "" {
			action.Metadata = map[string]any{"source_transaction": txn}
		}

		headers := filterRecordedHeaders(req.Headers)
		if len(headers) > 0 {
			action.Headers = headers
		}

		if req.Body != "" {
			body := req.Body
			if req.BodyEncoding == "" {
				body = applyCorrelationVars(body, corrMap)
			}
			action.Body = body
			action.BodyEncoding = req.BodyEncoding
		}

		for _, corr := range correlations {
			if corr.FoundInSeq == req.Seq {
				action.Extractors = append(action.Extractors, dsl.Extractor{
					Name:       corr.Name,
					Source:     corr.Source,
					Type:       corr.ExtractType,
					Expression: corr.Expression,
				})
			}
		}

		action.Assertions = append(action.Assertions, dsl.Assertion{
			Type:     "status_code",
			Expected: req.StatusCode,
		})

		if req.ThinkTimeMs > 500 {
			action.ThinkTimeAfter = true
			action.ThinkTime = dsl.ThinkTimeConfig{
				Type:   "fixed",
				MeanMs: float64(req.ThinkTimeMs),
			}
		}

		script.Actions = append(script.Actions, action)
	}

	if len(script.Actions) == 0 {
		result.Warnings = append(result.Warnings, "no non-static requests found in recording")
	}
	if opts.Parameterize {
		result.Warnings = append(result.Warnings, parameterizeInputs(script)...)
	}
	if len(recordedResourceURLs) > 0 {
		script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:           "recorder",
			SourceScriptName: session.Name,
			Preserved: map[string]any{
				"recorded_resource_urls_available": true,
				"recorded_resource_urls":           recordedResourceURLs,
			},
		}}
	}

	result.Script = script
	return result
}

func detectCorrelations(requests []RecordedRequest) []Correlation {
	var correlations []Correlation
	seen := make(map[string]bool)

	for i, resp := range requests {
		if resp.ResponseBody == "" {
			continue
		}

		candidates := extractDynamicValues(resp)

		for _, cand := range candidates {
			if seen[cand.value] {
				continue
			}

			var usedIn []int
			for j := i + 1; j < len(requests); j++ {
				later := requests[j]
				if strings.Contains(later.URL, cand.value) ||
					strings.Contains(later.Body, cand.value) ||
					headerContains(later.Headers, cand.value) {
					usedIn = append(usedIn, later.Seq)
				}
			}

			if len(usedIn) > 0 {
				seen[cand.value] = true
				correlations = append(correlations, Correlation{
					Name:        cand.name,
					Source:      cand.source,
					Expression:  cand.expression,
					ExtractType: cand.extractType,
					FoundInSeq:  resp.Seq,
					UsedInSeqs:  usedIn,
					SampleValue: truncate(cand.value, 80),
				})
			}
		}
	}

	return correlations
}

type candidate struct {
	name        string
	value       string
	source      string
	extractType string
	expression  string
}

var (
	tokenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`"(?:token|access_token|id_token|refresh_token|session_id|sessionid|sid|csrf|_token|xsrf|auth)"\s*:\s*"([^"]{8,})"`),
		regexp.MustCompile(`"(?:id|Id|ID)"\s*:\s*"([0-9a-f-]{8,})"`),
		regexp.MustCompile(`"(?:id|Id|ID)"\s*:\s*(\d{4,})`),
	}
	headerTokenNames = []string{
		"x-csrf-token", "x-xsrf-token", "set-cookie", "location",
		"x-request-id", "x-correlation-id", "authorization",
	}
	// HTML hidden-field tokens (CSRF, ASP.NET __VIEWSTATE / __RequestVerificationToken,
	// OAuth state, etc.) are a staple of recorded form flows that the JSON patterns miss.
	// Group order differs between the two attribute orderings, tracked by nameGroup/valueGroup.
	htmlInputPatterns = []struct {
		re         *regexp.Regexp
		nameGroup  int
		valueGroup int
	}{
		{regexp.MustCompile(`(?i)<input[^>]*\bname="([A-Za-z0-9_.\-\[\]]*(?:csrf|xsrf|token|nonce|state|viewstate|authenticity)[A-Za-z0-9_.\-\[\]]*)"[^>]*\bvalue="([^"]{4,})"`), 1, 2},
		{regexp.MustCompile(`(?i)<input[^>]*\bvalue="([^"]{4,})"[^>]*\bname="([A-Za-z0-9_.\-\[\]]*(?:csrf|xsrf|token|nonce|state|viewstate|authenticity)[A-Za-z0-9_.\-\[\]]*)"`), 2, 1},
	}
)

func extractDynamicValues(req RecordedRequest) []candidate {
	var candidates []candidate

	for _, pat := range tokenPatterns {
		matches := pat.FindAllStringSubmatch(req.ResponseBody, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			keyMatch := regexp.MustCompile(`"(\w+)"`).FindStringSubmatch(m[0])
			name := "corr_value"
			if len(keyMatch) >= 2 {
				name = "corr_" + strings.ToLower(keyMatch[1])
			}

			jsonPath := fmt.Sprintf("$.%s", strings.ToLower(keyMatch[1]))
			candidates = append(candidates, candidate{
				name:        name,
				value:       m[1],
				source:      "body",
				extractType: "jsonpath",
				expression:  jsonPath,
			})
		}
	}

	for _, hdr := range headerTokenNames {
		if val, ok := req.ResponseHeaders[hdr]; ok && len(val) > 4 {
			candidates = append(candidates, candidate{
				name:        "corr_" + strings.ReplaceAll(hdr, "-", "_"),
				value:       val,
				source:      "header",
				extractType: "header",
				expression:  hdr,
			})
		}
		titleCase := strings.ReplaceAll(strings.Title(strings.ReplaceAll(hdr, "-", " ")), " ", "-")
		if val, ok := req.ResponseHeaders[titleCase]; ok && len(val) > 4 {
			candidates = append(candidates, candidate{
				name:        "corr_" + strings.ReplaceAll(hdr, "-", "_"),
				value:       val,
				source:      "header",
				extractType: "header",
				expression:  titleCase,
			})
		}
	}

	// HTML hidden form tokens. Emit a regex extractor (robust to attribute
	// spacing/ordering) that the HTTP protocol's body extractor understands.
	for _, pat := range htmlInputPatterns {
		matches := pat.re.FindAllStringSubmatch(req.ResponseBody, -1)
		for _, m := range matches {
			if len(m) <= pat.valueGroup || len(m) <= pat.nameGroup {
				continue
			}
			field := m[pat.nameGroup]
			value := m[pat.valueGroup]
			if value == "" {
				continue
			}
			var expression string
			if pat.nameGroup < pat.valueGroup {
				expression = fmt.Sprintf(`name="%s"[^>]*value="([^"]+)"`, regexp.QuoteMeta(field))
			} else {
				expression = fmt.Sprintf(`value="([^"]+)"[^>]*name="%s"`, regexp.QuoteMeta(field))
			}
			candidates = append(candidates, candidate{
				name:        "corr_" + slugifyCorrelationName(field),
				value:       value,
				source:      "body",
				extractType: "regex",
				expression:  expression,
			})
		}
	}

	return candidates
}

func slugifyCorrelationName(field string) string {
	slug := nonWordCorrelationChars.ReplaceAllString(strings.ToLower(field), "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		return "value"
	}
	return slug
}

var nonWordCorrelationChars = regexp.MustCompile(`[^a-z0-9]+`)

func buildCorrelationMap(correlations []Correlation) map[string]string {
	m := make(map[string]string)
	for _, c := range correlations {
		m[c.SampleValue] = "{{var." + c.Name + "}}"
	}
	return m
}

func applyCorrelationVars(s string, corrMap map[string]string) string {
	// Replace longest values first so a short correlation value (e.g. a numeric
	// id) can't clobber a substring of a longer one that shares a prefix.
	values := make([]string, 0, len(corrMap))
	for value := range corrMap {
		if value == "" {
			continue
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		s = strings.ReplaceAll(s, value, corrMap[value])
	}
	return s
}

const parameterizedDataSourceName = "recorded_inputs"

// parameterizableField matches form/query field names that typically hold
// user-supplied input worth turning into data-driven parameters.
var parameterizableField = regexp.MustCompile(`(?i)\b(user(name)?|email|login|pass(word|wd)?|search|query|q|first[_-]?name|last[_-]?name|full[_-]?name|phone|mobile|zip|postal[_-]?code|city|address|account|customer)\b`)

// keyValuePair captures `key=value` segments in query strings and
// form-urlencoded bodies, preserving the leading delimiter so it can be
// re-emitted unchanged.
var keyValuePair = regexp.MustCompile(`([?&]|^)([A-Za-z0-9_.\-\[\]]+)=([^&#\s"']+)`)

// parameterizeInputs promotes recorded input values into a generated data
// source and rewrites the occurrences to {{data.recorded_inputs.<col>}}. It runs
// after correlation replacement so values already turned into {{var.*}} are left
// alone. Returns warnings describing what was parameterized.
func parameterizeInputs(script *dsl.Script) []string {
	columns := make([]string, 0)
	sampleValues := make(map[string]string)
	seen := make(map[string]bool)

	rewrite := func(s string) string {
		return keyValuePair.ReplaceAllStringFunc(s, func(segment string) string {
			m := keyValuePair.FindStringSubmatch(segment)
			if m == nil {
				return segment
			}
			prefix, key, value := m[1], m[2], m[3]
			if strings.Contains(value, "{{") || !parameterizableField.MatchString(key) {
				return segment
			}
			col := slugifyCorrelationName(key)
			if col == "" {
				return segment
			}
			if !seen[col] {
				seen[col] = true
				columns = append(columns, col)
				decoded, err := url.QueryUnescape(value)
				if err != nil {
					decoded = value
				}
				sampleValues[col] = decoded
			}
			return prefix + key + "={{data." + parameterizedDataSourceName + "." + col + "}}"
		})
	}

	for i := range script.Actions {
		script.Actions[i].Target = rewrite(script.Actions[i].Target)
		if script.Actions[i].Body != "" {
			script.Actions[i].Body = rewrite(script.Actions[i].Body)
		}
	}

	if len(columns) == 0 {
		return nil
	}

	header := strings.Join(columns, ",")
	row := make([]string, len(columns))
	for i, col := range columns {
		row[i] = csvEscapeField(sampleValues[col])
	}
	script.DataSources = append(script.DataSources, dsl.DataSource{
		Name:           parameterizedDataSourceName,
		Type:           "csv",
		Strategy:       "sequential",
		FirstRowHeader: true,
		InlineData:     header + "\n" + strings.Join(row, ","),
	})

	return []string{fmt.Sprintf(
		"Parameterized %d recorded input field(s) into data source %q (%s). Review the sample values and add rows before running at load.",
		len(columns), parameterizedDataSourceName, strings.Join(columns, ", "),
	)}
}

func csvEscapeField(value string) string {
	if strings.ContainsAny(value, ",\"\n\r") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}

func filterRecordedHeaders(headers map[string]string) map[string]string {
	skip := map[string]bool{
		"host": true, "content-length": true, "accept-encoding": true,
		"connection": true, "proxy-connection": true, "user-agent": true,
		"accept": true, "accept-language": true, "cache-control": true,
		"pragma": true, "sec-ch-ua": true, "sec-ch-ua-mobile": true,
		"sec-ch-ua-platform": true, "sec-fetch-dest": true,
		"sec-fetch-mode": true, "sec-fetch-site": true,
		"sec-fetch-user": true, "upgrade-insecure-requests": true,
		"cookie": true,
	}

	filtered := make(map[string]string)
	for k, v := range headers {
		if !skip[strings.ToLower(k)] {
			filtered[k] = v
		}
	}
	return filtered
}

func simplifyPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	result := "/" + strings.Join(parts, "/")
	if len(result) > 40 {
		result = result[:40]
	}
	return result
}

func headerContains(headers map[string]string, value string) bool {
	for _, v := range headers {
		if strings.Contains(v, value) {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
