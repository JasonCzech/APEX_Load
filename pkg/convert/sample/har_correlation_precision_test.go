package sample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func correlationHAREntry(method, url string, requestBody string, responseHeaders map[string]string, responseBody string) map[string]any {
	request := map[string]any{"method": method, "url": url, "headers": []any{}}
	if requestBody != "" {
		request["postData"] = map[string]any{"mimeType": "application/json", "text": requestBody}
	}
	headers := make([]any, 0, len(responseHeaders))
	for name, value := range responseHeaders {
		headers = append(headers, map[string]any{"name": name, "value": value})
	}
	return map[string]any{
		"request":  request,
		"response": map[string]any{"status": 200, "headers": headers, "content": map[string]any{"mimeType": "application/json", "text": responseBody}},
	}
}

func convertSyntheticHAR(t *testing.T, entries ...map[string]any) *ConvertResult {
	t.Helper()
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": entries}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// A correlation is rewritten only where its value stands on its own and never
// in a URL's host; a dictionary-word cookie is not a correlation at all.
func TestConvertHARRewritesStandaloneValuesOutsideHosts(t *testing.T) {
	const token = "tk4815162342ab"
	result := convertSyntheticHAR(t,
		correlationHAREntry("GET", "https://www.example.test/", "", map[string]string{"Set-Cookie": "agent_cohort=qualified; Path=/"}, `{"pageToken":"`+token+`"}`),
		correlationHAREntry("GET", "https://"+token+".tenant.example.test/w/1?token="+token, "", nil, `{}`),
		correlationHAREntry("POST", "https://www.example.test/api", `{"ref":"x`+token+`","encoded":"%22`+token+`%22","plain":"`+token+`"}`, nil, `{}`),
		correlationHAREntry("GET", "https://app.qualified.example.test/w?cohort=qualified", "", nil, `{}`),
	)
	if len(result.Correlations) != 1 || result.Correlations[0].Value != token || !result.Correlations[0].PreApplied {
		t.Fatalf("correlations = %+v, want only the page token, applied", result.Correlations)
	}
	name := result.Correlations[0].Name
	actions := result.Script.Actions
	if want := "https://" + token + ".tenant.example.test/w/1?token={{var." + name + "}}"; actions[1].Target != want {
		t.Fatalf("target = %q, want the host kept and the query rewritten: %q", actions[1].Target, want)
	}
	if want := `{"ref":"x` + token + `","encoded":"%22{{var.` + name + `}}%22","plain":"{{var.` + name + `}}"}`; actions[2].Body != want {
		t.Fatalf("body = %q, want only the standalone occurrences rewritten: %q", actions[2].Body, want)
	}
	if actions[3].Target != "https://app.qualified.example.test/w?cohort=qualified" {
		t.Fatalf("target = %q, want the word cookie left alone", actions[3].Target)
	}
}

// A high-confidence dynamic value fails the step when missing; a weaker one
// falls back to the value it replaced, so static content that a page no
// longer carries does not fail replay.
func TestConvertHARFailsOnlyHighConfidenceCorrelationMisses(t *testing.T) {
	const token = "tk4815162342ab"
	const visitor = "v7Kq2xLm9Pz4"
	result := convertSyntheticHAR(t,
		correlationHAREntry("GET", "https://www.example.test/", "", map[string]string{"Set-Cookie": "visitor=" + visitor + "; Path=/"}, `{"pageToken":"`+token+`"}`),
		correlationHAREntry("GET", "https://www.example.test/next?token="+token+"&v="+visitor, "", nil, `{}`),
	)
	extractors := map[string]string{}
	defaults := map[string]string{}
	for _, extractor := range result.Script.Actions[0].Extractors {
		extractors[extractor.Expression] = extractor.OnMiss
		if extractor.DefaultValue != nil {
			defaults[extractor.Expression] = *extractor.DefaultValue
		}
	}
	if len(extractors) != 2 {
		t.Fatalf("extractors = %+v, want the token and the visitor cookie", result.Script.Actions[0].Extractors)
	}
	for expression, onMiss := range extractors {
		switch {
		case strings.Contains(expression, "pageToken"):
			if onMiss != "fail" || defaults[expression] != "" {
				t.Fatalf("token extractor on_miss = %q default %q, want fail", onMiss, defaults[expression])
			}
		case strings.Contains(expression, "visitor"):
			if onMiss != "default" || defaults[expression] != visitor {
				t.Fatalf("cookie extractor on_miss = %q default %q, want default to the recorded value", onMiss, defaults[expression])
			}
		default:
			t.Fatalf("unexpected extractor %q", expression)
		}
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("script with miss defaults does not validate: %v", issues)
	}
}
