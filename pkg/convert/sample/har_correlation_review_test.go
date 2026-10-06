package sample

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestHARIncompleteCorrelationAnalysisAppliesCheckedCandidates(t *testing.T) {
	// A large response can exceed bounded discovery. The candidates that were
	// discovered finished every check, so they are applied; the gap in the
	// unanalyzed part of the response is reported for review.
	values := make([]map[string]string, 4200) // past correlate's 4,096 per-response cap
	for index := range values {
		values[index] = map[string]string{"token": fmt.Sprintf("har-token-%06d", index)}
	}
	response, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	requestBody := "token=har-token-000000"
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{"request": map[string]any{"method": "GET", "url": "https://example.invalid/tokens"}, "response": map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Encoding", "value": "gzip"}}, "content": map[string]any{"encoding": "base64", "text": base64.StdEncoding.EncodeToString(response)}}},
		map[string]any{"request": map[string]any{"method": "POST", "url": "https://example.invalid/submit", "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": requestBody}}, "response": map[string]any{"status": 200, "content": map[string]any{"text": "{}"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.CorrelationsDetected != 1 || len(result.Correlations) != 1 {
		t.Fatalf("checked candidate lost: %+v", result.Stats)
	}
	candidate := result.Correlations[0]
	if !candidate.PreApplied || candidate.ReviewAction != "" {
		t.Fatalf("checked candidate from an incomplete analysis was not applied: %+v", candidate)
	}
	if candidate.FoundInStep != 1 || len(candidate.UsedInSteps) != 1 || candidate.UsedInSteps[0] != 2 {
		t.Fatalf("candidate provenance changed: %+v", candidate)
	}
	if len(result.Script.Actions[0].Extractors) != 1 || !strings.Contains(result.Script.Actions[1].Body, "{{var.") {
		t.Fatalf("checked candidate not wired: extractors=%v body=%q", result.Script.Actions[0].Extractors, result.Script.Actions[1].Body)
	}
	if !containsSampleDiagnostic(result, "HAR correlation analysis incomplete") || !strings.Contains(strings.Join(result.Warnings, "\n"), "correlation coverage is incomplete") {
		t.Fatalf("incompleteness was not surfaced: %v", result.Warnings)
	}
}

// Under incomplete analysis a longer value containing an applied one may lie
// in the skipped part, where collision analysis cannot see it. A spelling that
// occurs inside a longer identifier is therefore held for review instead of
// being rewritten in place.
func TestHARIncompleteCorrelationAnalysisHoldsEmbeddedValues(t *testing.T) {
	values := make([]map[string]string, 4200) // past correlate's 4,096 per-response cap
	for index := range values {
		values[index] = map[string]string{"token": fmt.Sprintf("har-token-%06d", index)}
	}
	response, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	requestBody := "token=har-token-000000&id=har-token-000000X9"
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{"request": map[string]any{"method": "GET", "url": "https://example.invalid/tokens"}, "response": map[string]any{"status": 200, "content": map[string]any{"text": string(response)}}},
		map[string]any{"request": map[string]any{"method": "POST", "url": "https://example.invalid/submit", "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": requestBody}}, "response": map[string]any{"status": 200, "content": map[string]any{"text": "{}"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Correlations) != 1 {
		t.Fatalf("correlations = %+v", result.Correlations)
	}
	candidate := result.Correlations[0]
	if candidate.PreApplied || candidate.ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("embedded value was applied under incomplete analysis: %+v", candidate)
	}
	if result.Script.Actions[1].Body != requestBody {
		t.Fatalf("body rewritten to %q", result.Script.Actions[1].Body)
	}
}

func TestLiteralEmbedded(t *testing.T) {
	for _, tc := range []struct {
		text, literal string
		want          bool
	}{
		{"token=abc123&x", "abc123", false},
		{"id=abc123X9", "abc123", true},
		{"id=Zabc123", "abc123", true},
		{"a=abc123,b=abc1234", "abc123", true},
		{"path/-abc-/x", "-abc-", false},
		{"Xabc123 abc123", "abc123", true},
	} {
		if got := literalEmbedded(tc.text, tc.literal); got != tc.want {
			t.Errorf("literalEmbedded(%q, %q) = %v, want %v", tc.text, tc.literal, got, tc.want)
		}
	}
}
