package sample

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestHARIncompleteCorrelationAnalysisRetainsManualCandidates(t *testing.T) {
	// A large response can exceed bounded discovery while still supplying a
	// useful, exactly discovered candidate for the existing manual review flow.
	values := make([]map[string]string, 1100)
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
		t.Fatalf("manual candidates lost: %+v", result.Stats)
	}
	candidate := result.Correlations[0]
	if candidate.PreApplied || candidate.ReviewAction != manualCorrelationValueReplacement {
		t.Fatalf("incomplete analysis candidate presented as applied: %+v", candidate)
	}
	if candidate.FoundInStep != 1 || len(candidate.UsedInSteps) != 1 || candidate.UsedInSteps[0] != 2 {
		t.Fatalf("candidate provenance changed: %+v", candidate)
	}
	if result.Stats.Extractors != 0 || len(result.Script.Actions[0].Extractors) != 0 || result.Script.Actions[1].Body != requestBody {
		t.Fatal("incomplete analysis silently rewrote the script")
	}
	if !containsSampleDiagnostic(result, "HAR correlation analysis incomplete") || !strings.Contains(strings.Join(result.Warnings, "\n"), "manual review only") {
		t.Fatalf("incompleteness was not surfaced: %v", result.Warnings)
	}
}
