package openapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertPreservesCredentialEquivalentsInReviewSurfaces(t *testing.T) {
	const canary = "openapi-review-surface-password-8f31"
	result, err := Convert(ImportRequest{Content: `{
  "openapi":"3.0.3",
  "info":{
    "title":"` + canary + `",
    "version":"` + canary + `"
  },
  "servers":[{"url":"https://api.example.test"}],
  "paths":{
    "/accounts":{
      "post":{
        "operationId":"` + canary + `",
        "requestBody":{
          "content":{
            "application/json":{
              "example":{"password":"` + canary + `"}
            }
          }
        },
        "responses":{"204":{"description":"ok"}}
      }
    }
  }
}`})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Script == nil || len(result.Script.Actions) != 1 {
		t.Fatalf("script = %#v, want one executable action", result.Script)
	}
	action := result.Script.Actions[0]
	if action.Name != sanitizeName(canary) || !strings.Contains(action.Body, canary) {
		t.Fatalf(
			"executable action changed while projecting review text: name=%q body=%q",
			action.Name,
			action.Body,
		)
	}

	payload, err := json.Marshal(struct {
		Warnings     []string
		Unsupported  any
		Correlations any
		Stats        ConvertStats
		Summary      ReviewSummary
		SourceReview any
	}{
		Warnings:     result.Warnings,
		Unsupported:  result.Unsupported,
		Correlations: result.Correlations,
		Stats:        result.Stats,
		Summary:      result.Summary,
		SourceReview: result.SourceReview,
	})
	if err != nil {
		t.Fatalf("json.Marshal review surfaces: %v", err)
	}
	if !strings.Contains(string(payload), canary) {
		t.Fatalf("review surfaces lost retained credential: %s", payload)
	}
}

func TestSanitizeOpenAPIResultReviewTextCoversOperationAuthSummary(t *testing.T) {
	const credentialCanary = "operation-auth-summary-credential-canary"
	result := &ConvertResult{
		Script: &dsl.Script{
			Name:     "review",
			Protocol: "http",
			Actions: []dsl.Action{{
				Name:    "request",
				Type:    "request",
				Method:  "GET",
				Target:  "/",
				Headers: map[string]string{"Authorization": "Bearer " + credentialCanary},
			}},
		},
		Summary: ReviewSummary{
			OperationAuth: []string{"GET / — " + credentialCanary},
		},
	}

	sanitizeOpenAPIResultReviewText(result)

	if strings.Contains(result.Summary.OperationAuth[0], credentialCanary) {
		t.Fatalf("operation auth summary disclosed executable credential: %q", result.Summary.OperationAuth[0])
	}
}
