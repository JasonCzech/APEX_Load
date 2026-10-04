package dsl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseContentFidelityRequiresDurableMatchingGateAfterReopen(t *testing.T) {
	reasons := []string{"response_content_truncated", "unsupported_response_content_encoding", "invalid_response_content_encoding", "response_content_decode_limit", "response_content_decode_canceled", "malformed_response_content_provenance", "response_content_provenance_unavailable", "invalid_har_content_storage", "unsupported_har_content_storage", "har_content_storage_limit"}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			action := Action{Name: "Captured request", Type: "GET", Target: "https://fixture.invalid/", Metadata: map[string]any{
				ImportResponseContentFidelityMetadataKey: map[string]any{"version": 1, "reason_code": reason},
				"execution_blocker":                      importReviewRequiredBlocker,
				"import_review":                          map[string]any{"version": 1, "reason_codes": []string{reason}},
			}}
			for _, nested := range []bool{false, true} {
				script := httpContractScript("", action)
				if nested {
					script.Actions = []Action{{Name: "Transaction", Type: "http_sequence", Params: map[string]any{"requests": []Action{action}}}}
				}
				payload, err := json.Marshal(script)
				if err != nil {
					t.Fatal(err)
				}
				var reopened Script
				if err = json.Unmarshal(payload, &reopened); err != nil {
					t.Fatal(err)
				}
				if got := ValidateScriptIssues(&reopened); len(got) != 0 {
					t.Fatalf("valid captured gate nested=%v: %v", nested, got)
				}
				metadata := reopened.Actions[0].Metadata
				if nested {
					metadata = reopened.Actions[0].Params["requests"].([]any)[0].(map[string]any)["metadata"].(map[string]any)
				}
				delete(metadata, "execution_blocker")
				joined := strings.Join(ValidateScriptIssues(&reopened), "\n")
				if !strings.Contains(joined, "requires the \"import_review_required\" execution blocker") {
					t.Fatalf("removed blocker nested=%v: %s", nested, joined)
				}
				metadata["execution_blockers"] = []string{importReviewRequiredBlocker}
				metadata["import_review"] = map[string]any{"version": 1, "reason_codes": []string{"http_version_unavailable"}}
				joined = strings.Join(ValidateScriptIssues(&reopened), "\n")
				if !strings.Contains(joined, "must include fidelity reason") {
					t.Fatalf("removed matching reason nested=%v: %s", nested, joined)
				}
			}
		})
	}
}

func TestResponseContentFidelityRejectsMalformedWithoutSourceValueDiagnostics(t *testing.T) {
	canary := "private-source-value-do-not-display"
	for _, marker := range []any{nil, canary, map[string]any{"version": 2, "reason_code": "response_content_truncated"}, map[string]any{"version": 1, "reason_code": canary}, map[string]any{"version": 1, "reason_code": " response_content_truncated "}} {
		script := httpContractScript("", Action{Type: "GET", Target: "https://fixture.invalid/", Metadata: map[string]any{ImportResponseContentFidelityMetadataKey: marker}})
		issues := strings.Join(ValidateScriptIssues(script), "\n")
		if issues == "" {
			t.Fatal("malformed marker passed")
		}
		if strings.Contains(issues, canary) {
			t.Fatal("source value reached a diagnostic")
		}
	}
}

func TestResponseContentFailureCaptureCannotLoseGateByRemovingMarker(t *testing.T) {
	action := Action{Type: "GET", Target: "https://fixture.invalid/", Metadata: map[string]any{
		"response_content_capture": map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "response_content_truncated"},
	}}
	script := httpContractScript("", action)
	if got := strings.Join(ValidateScriptIssues(script), "\n"); !strings.Contains(got, "requires the \"import_review_required\" execution blocker") {
		t.Fatalf("ungated capture: %s", got)
	}
	action.Metadata["execution_blocker"] = importReviewRequiredBlocker
	action.Metadata["import_review"] = map[string]any{"version": 1, "reason_codes": []string{"response_content_truncated"}}
	if got := ValidateScriptIssues(script); len(got) != 0 {
		t.Fatalf("valid capture gate: %v", got)
	}
	capture := action.Metadata["response_content_capture"].(map[string]any)
	capture["reason_code"] = "response_content_unavailable"
	if got := ValidateScriptIssues(script); len(got) == 0 {
		t.Fatal("wire capture cannot claim spec optional absence")
	}
	capture["captured"] = "har_decoded"
	delete(action.Metadata, "execution_blocker")
	delete(action.Metadata, "import_review")
	if got := ValidateScriptIssues(script); len(got) != 0 {
		t.Fatalf("normal optional HAR absence: %v", got)
	}
	capture["reason_code"] = "private-source-capture-reason"
	got := strings.Join(ValidateScriptIssues(script), "\n")
	if got == "" || strings.Contains(got, "private-source-capture-reason") {
		t.Fatal("unknown capture reason was accepted or disclosed")
	}
}

func TestUnavailableResponseContentCaptureRequiresFailureReason(t *testing.T) {
	for _, fields := range []map[string]any{
		{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable"},
		{"version": 1, "captured": "wire", "stored": "unavailable"},
		{"version": 1, "captured": "har_decoded", "state": "unavailable"},
	} {
		script := httpContractScript("", Action{Metadata: map[string]any{"response_content_capture": fields}})
		if got := strings.Join(ValidateScriptIssues(script), "\n"); !strings.Contains(got, "requires a supported reason_code") {
			t.Fatalf("failed capture lost disposition: %s", got)
		}
	}
}
