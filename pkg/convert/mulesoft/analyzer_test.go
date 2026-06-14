package mulesoft

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAnalyzeDetectsAnypointHostsPoliciesAndAuth(t *testing.T) {
	script := &dsl.Script{
		Name:     "Mule API",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://accounts-api.us-e2.cloudhub.io/api"},
		Init: []dsl.Action{{
			Name: "Token",
			Type: "http_oauth2_token",
			Params: map[string]any{
				"grant_type": "client_credentials",
				"token_url":  "https://login.example.test/oauth/token",
			},
		}},
		Actions: []dsl.Action{{
			Name:   "List Accounts",
			Type:   "GET",
			Method: "GET",
			Target: "/accounts",
			Headers: map[string]string{
				"Authorization": "Bearer {{var.access_token}}",
				"client_id":     "{{env.MULE_CLIENT_ID}}",
				"client_secret": "{{secret.MULE_CLIENT_SECRET}}",
			},
		}},
	}

	review := Analyze(script)
	if !review.Detected {
		t.Fatal("MuleSoft traffic not detected")
	}
	if len(review.Domains) != 1 || review.Domains[0].Kind != "cloudhub" {
		t.Fatalf("domains = %#v, want one cloudhub host", review.Domains)
	}

	policies := map[string]bool{}
	for _, finding := range review.PolicyFindings {
		policies[finding.Policy] = true
	}
	if !policies["client_id_enforcement"] {
		t.Fatalf("policy findings = %#v, want client_id_enforcement", review.PolicyFindings)
	}

	auths := map[string]bool{}
	for _, finding := range review.AuthFindings {
		auths[finding.Type] = true
	}
	if !auths["oauth2_client_credentials"] || !auths["bearer"] {
		t.Fatalf("auth findings = %#v, want oauth2 client-credentials + bearer", review.AuthFindings)
	}
	if len(review.Blockers) == 0 {
		t.Fatalf("blockers = %#v, want client-credential blocker", review.Blockers)
	}
}

func TestAnalyzeFlagsBearerWithoutTokenAcquisition(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.anypoint.mulesoft.com/v1"},
		Actions: []dsl.Action{{
			Name:    "Call",
			Type:    "GET",
			Method:  "GET",
			Target:  "/resource",
			Headers: map[string]string{"Authorization": "Bearer abc"},
		}},
	}
	review := Analyze(script)
	if !review.Detected {
		t.Fatal("anypoint host not detected")
	}
	foundBlocker := false
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "http_oauth2_token") {
			foundBlocker = true
		}
	}
	if !foundBlocker {
		t.Fatalf("blockers = %#v, want missing-token-acquisition blocker", review.Blockers)
	}
}

func TestAnalyzeDoesNotFlagMissingTokenWhenRefreshTokenGrantPresent(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.anypoint.mulesoft.com/v1"},
		Init:   []dsl.Action{{Name: "Token", Type: "http_oauth2_token", Params: map[string]any{"grant_type": "refresh_token"}}},
		Actions: []dsl.Action{{
			Name: "Call", Type: "GET", Method: "GET", Target: "/resource",
			Headers: map[string]string{"Authorization": "Bearer {{var.access_token}}"},
		}},
	}
	review := Analyze(script)
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "http_oauth2_token") {
			t.Fatalf("blockers = %#v, should not claim missing token when refresh_token init present", review.Blockers)
		}
	}
}

func TestAnalyzeDoesNotDetectGenericApiKeyServices(t *testing.T) {
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.com/v1"},
		Actions: []dsl.Action{{
			Name: "Call", Type: "GET", Method: "GET", Target: "/resource",
			Headers: map[string]string{"x-api-key": "{{env.API_KEY}}"},
		}},
	}
	if review := Analyze(script); review.Detected {
		t.Fatalf("review = %#v, want a generic api-key service NOT detected as MuleSoft", review)
	}
}

func TestAnalyzeDoesNotDetectNonAnypointMulesoftSubstringHosts(t *testing.T) {
	script := &dsl.Script{
		Config:  dsl.ScriptConfig{BaseURL: "https://mulesoft-proxy.acme.com/api"},
		Actions: []dsl.Action{{Type: "GET", Method: "GET", Target: "/resource"}},
	}
	if review := Analyze(script); review.Detected {
		t.Fatalf("review = %#v, want non-Anypoint host containing 'mulesoft' NOT detected", review)
	}
}

func TestAnalyzeIgnoresNonMuleScripts(t *testing.T) {
	script := &dsl.Script{
		Config:  dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions: []dsl.Action{{Type: "GET", Method: "GET", Target: "/health"}},
	}
	review := Analyze(script)
	if review.Detected {
		t.Fatalf("review = %#v, want not detected for non-Mule script", review)
	}
}
