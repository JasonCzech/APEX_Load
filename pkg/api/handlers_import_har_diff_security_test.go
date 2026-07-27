package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleImportHARPairNeverDisclosesCredentialDiffValues(t *testing.T) {
	const (
		baselineToken   = "api-har-baseline-secret-token-123456"
		comparisonToken = "api-har-comparison-secret-token-654321"
	)
	server := NewServer(ServerConfig{})
	payload, err := json.Marshal(map[string]any{
		"name":              "Credential Pair",
		"content":           apiCredentialCorrelationHAR(baselineToken),
		"secondary_content": apiCredentialCorrelationHAR(comparisonToken),
		"options": map[string]any{
			"redact_sensitive":   false,
			"sanitize_sensitive": false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	server.mux.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, token := range []string{baselineToken, comparisonToken} {
		if bytes.Contains(response.Body.Bytes(), []byte(token)) {
			t.Fatalf("HAR pair response disclosed credential diff value %q", token)
		}
	}
	var decoded struct {
		Summary struct {
			HARDiff struct {
				DynamicCandidates int `json:"dynamic_candidates"`
				Classifications   []struct {
					BaselineValue   string `json:"baseline_value"`
					ComparisonValue string `json:"comparison_value"`
				} `json:"classifications"`
			} `json:"har_diff"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.HARDiff.DynamicCandidates != 1 ||
		len(decoded.Summary.HARDiff.Classifications) != 1 {
		t.Fatalf("HAR diff = %#v, want one dynamic credential candidate", decoded.Summary.HARDiff)
	}
	classification := decoded.Summary.HARDiff.Classifications[0]
	if classification.BaselineValue != "[REDACTED]" ||
		classification.ComparisonValue != "[REDACTED]" {
		t.Fatalf("classification values = %#v, want disclosure-safe placeholders", classification)
	}
}

func apiCredentialCorrelationHAR(token string) string {
	return `{
	  "log": {
	    "entries": [
	      {
	        "request": {"method": "POST", "url": "https://api.example.test/login", "headers": []},
	        "response": {
	          "status": 200,
	          "headers": [{"name": "Content-Type", "value": "application/json"}],
	          "content": {"text": "{\"access_token\":\"` + token + `\"}"}
	        }
	      },
	      {
	        "request": {
	          "method": "GET",
	          "url": "https://api.example.test/orders?access_token=` + token + `",
	          "headers": []
	        },
	        "response": {"status": 200, "content": {"text": "{\"ok\":true}"}}
	      }
	    ]
	  }
	}`
}
