package recorder

import (
	"fmt"
	"regexp"
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

func ConvertSession(session *Session, name string) *ConvertResult {
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

		headers := filterRecordedHeaders(req.Headers)
		if len(headers) > 0 {
			action.Headers = headers
		}

		if req.Body != "" {
			body := applyCorrelationVars(req.Body, corrMap)
			action.Body = body
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

	return candidates
}

func buildCorrelationMap(correlations []Correlation) map[string]string {
	m := make(map[string]string)
	for _, c := range correlations {
		m[c.SampleValue] = "{{var." + c.Name + "}}"
	}
	return m
}

func applyCorrelationVars(s string, corrMap map[string]string) string {
	for value, varRef := range corrMap {
		s = strings.ReplaceAll(s, value, varRef)
	}
	return s
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
