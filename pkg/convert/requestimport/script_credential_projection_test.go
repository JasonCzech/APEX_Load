package requestimport

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestProjectScriptCredentialsCoversExistingRequestSurfaces(t *testing.T) {
	t.Parallel()
	const (
		urlPassword   = "script-url-password-canary"
		queryToken    = "script-query-token-canary"
		headerToken   = "script-header-token-canary"
		formPassword  = "script-form-password-canary"
		authPassword  = "script-auth-password-canary"
		resourceToken = "script-resource-token-canary"
	)
	script := validBaseScript()
	script.GeneratedFrom = "test_import"
	script.Config.BaseURL = "https://api.example.test"
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "POST",
		Method: "POST",
		Target: "https://user:" + urlPassword + "@api.example.test/login?api_key=" + queryToken,
		HeaderList: dsl.HeaderList{
			{Name: "X-Trace", Value: "first"},
			{Name: "Authorization", Value: "Bearer " + headerToken},
			{Name: "x-trace", Value: "last"},
			{Name: "Content-Type", Value: "application/x-www-form-urlencoded"},
		},
		Headers: map[string]string{
			"X-Trace":       "first",
			"Authorization": "Bearer " + headerToken,
			"x-trace":       "last",
			"Content-Type":  "application/x-www-form-urlencoded",
		},
		Body: "username=demo&password=" + formPassword,
		Auth: &dsl.AuthConfig{
			Type:     "basic",
			Username: "demo",
			Password: authPassword,
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
		AdditionalURLs: []ScriptCredentialURL{{
			Value:       "https://cdn.example.test/app.js?token=" + resourceToken,
			TargetField: "metadata.import.preserved.recorded_resource_urls[0]",
			Element:     "recorded resource URL",
		}},
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	payload, err := json.Marshal(struct {
		Script *dsl.Script
		URLs   []string
	}{Script: script, URLs: result.AdditionalURLs})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{
		urlPassword,
		queryToken,
		headerToken,
		formPassword,
		authPassword,
		resourceToken,
	} {
		if !strings.Contains(string(payload), canary) {
			t.Fatalf("executable import lost credential %q: %s", canary, payload)
		}
	}
	if got := script.Actions[0].HeaderList; len(got) != 4 ||
		got[0].Name != "X-Trace" ||
		got[1].Name != "Authorization" ||
		got[2].Name != "x-trace" ||
		got[3].Name != "Content-Type" {
		t.Fatalf("header_list = %#v, want source order/casing/duplicates preserved", got)
	}
	if blocker := script.Actions[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("metadata = %#v, preserved credentials must not block execution", script.Actions[0].Metadata)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved credentials must not require reentry", result.Requirements)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
}

func TestProjectScriptCredentialsPreservesInvalidHeaderUntilRuntimeFidelityReview(t *testing.T) {
	t.Parallel()
	const canary = "invalid-header-value-canary"
	script := validBaseScript()
	script.GeneratedFrom = "test_import"
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "request",
		Method:     "GET",
		Target:     "/",
		HeaderList: dsl.HeaderList{{Name: "", Value: canary}},
	}}

	if _, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	}); err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if got := script.Actions[0].HeaderList; len(got) != 1 ||
		got[0].Name != "" || got[0].Value != canary {
		t.Fatalf("pre-fidelity header_list = %#v, want authoritative malformed occurrence preserved", got)
	}

	findings, _, err := ApplyRuntimeFidelityReview(
		script,
		RuntimeFidelityOptions{SourceProfile: "test_import"},
	)
	if err != nil {
		t.Fatalf("ApplyRuntimeFidelityReview: %v", err)
	}
	if len(script.Actions[0].HeaderList) != 0 ||
		script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("post-fidelity action = %#v, want sanitized blocked draft", script.Actions[0])
	}
	if len(findings) != 1 || findings[0].ReasonCode != "http_header_invalid_header_name" {
		t.Fatalf("runtime findings = %#v", findings)
	}
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("blocked public draft retained invalid header value: %s", payload)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsMalformedBase64IsValidBlockedDraft(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:         "request",
		Type:         "POST",
		Method:       "POST",
		Target:       "/upload",
		Body:         "invalid-base64%%",
		BodyEncoding: "base64",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "base64",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if result.FallbackDraft {
		t.Fatal("malformed request body should block its request, not replace the whole script")
	}
	action := script.Actions[0]
	decoded, decodeErr := base64.StdEncoding.DecodeString(action.Body)
	if decodeErr != nil || string(decoded) != "invalid-base64%%" {
		t.Fatalf("body = %q, want exact imported text in valid base64 envelope: decoded=%q err=%v",
			action.Body, decoded, decodeErr)
	}
	if action.Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, malformed encoding must block execution", action.Metadata)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsProjectsHTTPSequenceChildren(t *testing.T) {
	t.Parallel()
	const canary = "sequence-authorization-canary"
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "sequence",
		Type:   "http_sequence",
		Method: "SEQUENCE",
		Target: "/raw",
		Params: map[string]any{
			"requests": []map[string]any{{
				"name":   "child",
				"type":   "GET",
				"method": "GET",
				"target": "/orders",
				"headers": map[string]string{
					"Authorization": "Bearer " + canary,
				},
			}},
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "sequence",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(payload), canary) {
		t.Fatalf("sequence child lost executable credential: %s", payload)
	}
	if script.Actions[0].Target != "/orders" {
		t.Fatalf("sequence target = %q, want synchronized projected child target", script.Actions[0].Target)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved sequence credential must not require reentry", result.Requirements)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
}

// A malformed (non-string) sequence body cannot be inspected by any projector.
// It must still be retained byte-for-byte: overwriting it with the redaction
// sentinel destroyed an imported payload the operator already owned and did not
// make the shape any more valid. The gate, not a rewrite, is what keeps the
// draft from executing.
func TestProjectScriptCredentialsPreservesMalformedSequenceBodyShape(t *testing.T) {
	t.Parallel()
	const canary = "malformed-sequence-body-canary"
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "sequence",
		Type:   "http_sequence",
		Method: "SEQUENCE",
		Target: "/raw",
		Params: map[string]any{
			"requests": []map[string]any{{
				"name":   "child",
				"type":   "POST",
				"method": "POST",
				"target": "/upload",
				"body": map[string]any{
					"password": canary,
				},
			}},
		},
	}}

	_, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "malformed-sequence-body",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	payload, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if !strings.Contains(string(payload), canary) {
		t.Fatalf("malformed sequence body destroyed imported credential material: %s", payload)
	}
	if strings.Contains(string(payload), `"body":"[REDACTED]"`) {
		t.Fatalf("malformed sequence body was replaced by a redaction sentinel: %s", payload)
	}
	requests, _ := script.Actions[0].Params["requests"].([]map[string]any)
	if len(requests) != 1 {
		t.Fatalf("sequence requests = %#v, want the authored request retained", requests)
	}
	if body, _ := requests[0]["body"].(string); body != `{"password":"`+canary+`"}` {
		t.Fatalf("sequence request body = %#v, want the exact imported payload in a representable form", requests[0]["body"])
	}
	metadata, _ := requests[0]["metadata"].(map[string]any)
	if metadata["execution_blocker"] != "credential_request_body_shape_invalid" {
		t.Fatalf("metadata = %#v, malformed body shape must block execution", metadata)
	}
	targets, _ := metadata[dsl.ImportPreservedCredentialTargetsMetadataKey].([]credentialpolicy.TargetRef)
	if len(targets) != 1 ||
		targets[0].Field != "actions[0].params.requests[0].body.opaque" {
		t.Fatalf("preserved credential targets = %#v, want one opaque body locator", metadata[dsl.ImportPreservedCredentialTargetsMetadataKey])
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsRespectsSharedRegistry(t *testing.T) {
	t.Parallel()
	registry := credentialpolicy.NewNameRegistry()
	registry.Reserve("TEST_IMPORT_AUTHORIZATION")
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/",
		HeaderList: dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer token",
		}},
	}}
	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "registry",
		NamePrefix:    "TEST_IMPORT",
		Registry:      registry,
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved header must not require binding", result.Requirements)
	}
	if got := script.Actions[0].HeaderList[0].Value; got != "Bearer token" {
		t.Fatalf("Authorization = %q, want imported value", got)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
}

func TestProjectScriptCredentialsUsesPathGuaranteedExtractorProvenance(t *testing.T) {
	t.Parallel()
	const variable = "{{var.branch_token}}"
	extractor := dsl.Extractor{
		Name:       "branch_token",
		Source:     "body",
		Type:       "regex",
		Expression: "(.*)",
	}
	credentialRequest := func(name string) dsl.Action {
		return dsl.Action{
			Name:   name,
			Type:   "GET",
			Method: "GET",
			Target: "/" + name,
			HeaderList: dsl.HeaderList{{
				Name:  "Authorization",
				Value: variable,
			}},
		}
	}
	extractorRequest := func(name string) dsl.Action {
		return dsl.Action{
			Name:       name,
			Type:       "GET",
			Method:     "GET",
			Target:     "/" + name,
			Extractors: []dsl.Extractor{extractor},
		}
	}

	tests := []struct {
		name        string
		actions     []dsl.Action
		value       func(*dsl.Script) string
		wantTrusted bool
	}{
		{
			name: "then extractor is not available in else",
			actions: []dsl.Action{{
				Name: "branch",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
					Steps:     []dsl.Action{extractorRequest("then")},
					Else:      []dsl.Action{credentialRequest("else")},
				},
			}},
			value: func(script *dsl.Script) string {
				return script.Actions[0].Control.Else[0].HeaderList[0].Value
			},
		},
		{
			name: "one-sided branch extractor is not guaranteed afterward",
			actions: []dsl.Action{
				{
					Name: "branch",
					Type: "control_if",
					Control: &dsl.ControlFlow{
						Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
						Steps:     []dsl.Action{extractorRequest("then")},
					},
				},
				credentialRequest("after"),
			},
			value: func(script *dsl.Script) string {
				return script.Actions[1].HeaderList[0].Value
			},
		},
		{
			name: "zero-iteration loop extractor is not guaranteed afterward",
			actions: []dsl.Action{
				{
					Name: "loop",
					Type: "control_loop",
					Control: &dsl.ControlFlow{
						Condition: &dsl.Condition{Left: "no", Op: "eq", Right: "yes"},
						Steps:     []dsl.Action{extractorRequest("inside")},
					},
				},
				credentialRequest("after"),
			},
			value: func(script *dsl.Script) string {
				return script.Actions[1].HeaderList[0].Value
			},
		},
		{
			name: "unconditional prior extractor is trusted",
			actions: []dsl.Action{
				extractorRequest("before"),
				credentialRequest("after"),
			},
			value: func(script *dsl.Script) string {
				return script.Actions[1].HeaderList[0].Value
			},
			wantTrusted: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			script := validBaseScript()
			script.Actions = test.actions
			result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
				SourceProfile: "test_import",
				CandidateKey:  "provenance",
			})
			if err != nil {
				t.Fatalf("ProjectScriptCredentials: %v", err)
			}
			got := test.value(script)
			if got != variable {
				t.Fatalf("Authorization = %q, want imported runtime expression unchanged", got)
			}
			if len(result.Requirements) != 0 {
				t.Fatalf("requirements = %#v, preserved expression must not require reentry", result.Requirements)
			}
			if !test.wantTrusted {
				assertCredentialPreservationFindingsNoGate(t, result.Findings)
			}
		})
	}
}

func TestProjectScriptCredentialsHandlesDeferredBase64Templates(t *testing.T) {
	t.Parallel()
	t.Run("governed full-body secret is a valid deferred encoding", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Actions = []dsl.Action{{
			Name:         "request",
			Type:         "POST",
			Method:       "POST",
			Target:       "/upload",
			Body:         "{{secret.ENCODED_BODY}}",
			BodyEncoding: "base64",
		}}

		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base64-secret",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if result.FallbackDraft || len(result.Requirements) != 0 {
			t.Fatalf("projection = %#v, governed deferred body should remain executable", result)
		}
		if script.Actions[0].Body != "{{secret.ENCODED_BODY}}" {
			t.Fatalf("body = %q, want governed deferred template unchanged", script.Actions[0].Body)
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("DSL validation issues = %#v", issues)
		}
	})

	t.Run("unproven variable is not a trusted deferred encoding", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Actions = []dsl.Action{{
			Name:         "request",
			Type:         "POST",
			Method:       "POST",
			Target:       "/upload",
			Body:         "{{var.encoded_body}}",
			BodyEncoding: "base64",
		}}

		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base64-variable",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if result.FallbackDraft {
			t.Fatal("invalid encoded body should block its request, not replace the whole script")
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(script.Actions[0].Body)
		if decodeErr != nil || string(decoded) != "{{var.encoded_body}}" {
			t.Fatalf("body = %q, want exact imported expression in valid base64 envelope: decoded=%q err=%v",
				script.Actions[0].Body, decoded, decodeErr)
		}
		if script.Actions[0].Metadata["execution_blocker"] == nil {
			t.Fatalf("metadata = %#v, invalid encoded body must block execution", script.Actions[0].Metadata)
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("DSL validation issues = %#v", issues)
		}
	})
}

func TestProjectScriptCredentialsProjectsMirroredOAuthTokenURLOnce(t *testing.T) {
	t.Parallel()
	const canary = "oauth-token-url-canary"
	raw := "https://oauth-user:" + canary + "@auth.example.test/token"
	script := validBaseScript()
	script.Init = []dsl.Action{{
		Name:   "OAuth token",
		Type:   "http_oauth2_token",
		Target: raw,
		Params: map[string]any{
			"token_url":     raw,
			"grant_type":    "client_credentials",
			"client_id":     "{{env.CLIENT_ID}}",
			"client_secret": "{{secret.CLIENT_SECRET}}",
			"save_as":       "access_token",
		},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "oauth",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	tokenURL, _ := script.Init[0].Params["token_url"].(string)
	if script.Init[0].Target != raw || tokenURL != raw {
		t.Fatalf("OAuth URL mirrors lost imported bytes: %#v", script.Init[0])
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved OAuth URL must not require reentry", result.Requirements)
	}
	if blocker := script.Init[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("metadata = %#v, preserved OAuth URL must not block execution", script.Init[0].Metadata)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

// TestProjectScriptCredentialsGovernsBaseURLRuntimeVariables pins that a
// credential-shaped {{var.*}} reference in config.base_url is always governed,
// no matter how well an extractor's execution path is guaranteed.
// buildProtoConfigWithSecretResolver (pkg/engine/config.go) flattens base_url
// once at protocol init and resolves only {{secret.*}} there; HTTP.ResolveURL
// then concatenates the stored string verbatim per request. A preserved
// {{var.*}} would therefore go on the wire literally, so extractor provenance
// cannot make it executable — only a secret binding can.
func TestProjectScriptCredentialsGovernsBaseURLRuntimeVariables(t *testing.T) {
	t.Parallel()
	const rawBaseURL = "https://api.example.test?access_token={{var.access_token}}"
	extractorRequest := dsl.Action{
		Name:   "bootstrap",
		Type:   "GET",
		Method: "GET",
		Target: "https://auth.example.test/bootstrap",
		Extractors: []dsl.Extractor{{
			Name:       "access_token",
			Source:     "body",
			Type:       "jsonpath",
			Expression: "$.access_token",
		}},
	}
	relativeRequest := dsl.Action{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
	}

	t.Run("unconditional prior extractor", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Config.BaseURL = rawBaseURL
		script.Init = []dsl.Action{extractorRequest}
		script.Actions = []dsl.Action{relativeRequest}
		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base-proven",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if script.Config.BaseURL != rawBaseURL {
			t.Fatalf("base URL = %q, want imported expression %q", script.Config.BaseURL, rawBaseURL)
		}
		if len(result.Requirements) != 0 {
			t.Fatalf("requirements = %#v, preserved base expression must not require reentry", result.Requirements)
		}
		assertCredentialPreservationFindingsNoGate(t, result.Findings)
		if blocker := script.Init[0].Metadata["execution_blocker"]; blocker != nil {
			t.Fatalf("absolute bootstrap received unrelated base URL blocker %v: %#v", blocker, script.Init[0].Metadata)
		}
		assertBaseURLRuntimeTemplateGate(t, script.Actions[0])
		assertBaseURLRuntimeTemplateFinding(t, result.Findings)
	})

	t.Run("optional branch extractor", func(t *testing.T) {
		t.Parallel()
		script := validBaseScript()
		script.Config.BaseURL = rawBaseURL
		script.Actions = []dsl.Action{
			{
				Name: "branch",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
					Steps:     []dsl.Action{extractorRequest},
				},
			},
			relativeRequest,
		}
		result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
			SourceProfile: "test_import",
			CandidateKey:  "base-optional",
		})
		if err != nil {
			t.Fatalf("ProjectScriptCredentials: %v", err)
		}
		if script.Config.BaseURL != rawBaseURL {
			t.Fatalf("base URL = %q, want imported expression %q", script.Config.BaseURL, rawBaseURL)
		}
		if len(result.Requirements) != 0 {
			t.Fatalf("requirements = %#v, preserved base expression must not require reentry", result.Requirements)
		}
		assertCredentialPreservationFindingsNoGate(t, result.Findings)
		if blocker := script.Actions[0].Metadata["execution_blocker"]; blocker != nil {
			t.Fatalf("control shell received unrelated base URL blocker %v: %#v", blocker, script.Actions[0].Metadata)
		}
		assertBaseURLRuntimeTemplateGate(t, script.Actions[1])
		assertBaseURLRuntimeTemplateFinding(t, result.Findings)
	})
}

func TestProjectScriptCredentialsPreservesTemplatedOriginalURLUserinfo(t *testing.T) {
	t.Parallel()
	const sourceValue = "user:{{var.static_password}}"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name:   "static_password",
		Type:   "list",
		Values: []string{"source-password"},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "/orders",
		Metadata: map[string]any{
			"original_url": "https://" + sourceValue + "@api.example.test/orders",
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "templated-original-url",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	originalURL, _ := script.Actions[0].Metadata["original_url"].(string)
	if originalURL != "https://"+sourceValue+"@api.example.test/orders" {
		t.Fatalf("original URL = %q, want exact imported provenance", originalURL)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved original URL must not require reentry", result.Requirements)
	}
	if blocker := script.Actions[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("metadata = %#v, preserved original URL must not block execution", script.Actions[0].Metadata)
	}
	payload, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if !strings.Contains(string(payload), "source-password") ||
		!strings.Contains(originalURL, "{{var.static_password}}") {
		t.Fatalf("script lost imported parameter or provenance: %s", payload)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
	if targets := preservedCredentialTargetsFromMetadata(script.Actions[0].Metadata); len(targets) != 0 {
		t.Fatalf("provenance-only original_url produced executable locators: %#v", targets)
	}
}

func TestProjectScriptCredentialsRetainsMalformedBase64BodyAsBlockedDraft(t *testing.T) {
	t.Parallel()

	const sourceBody = "password=invalid-base64-canary-93a7%%%"
	script := validBaseScript()
	script.Actions = []dsl.Action{{
		Name:         "request",
		Type:         "POST",
		Method:       "POST",
		Target:       "/login",
		Body:         sourceBody,
		BodyEncoding: "base64",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "fixture",
		CandidateKey:  "invalid-base64",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	action := script.Actions[0]
	decoded, err := base64.StdEncoding.DecodeString(action.Body)
	if err != nil {
		t.Fatalf("decode preserved body: %v", err)
	}
	if string(decoded) != sourceBody || action.BodyEncoding != "base64" {
		t.Fatalf("body = %q (%q), want exact imported text in valid base64 envelope",
			decoded, action.BodyEncoding)
	}
	if action.Metadata["execution_blocker"] == nil {
		t.Fatalf("metadata = %#v, want execution gate", action.Metadata)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, retained body must not require reentry", result.Requirements)
	}
	targets := preservedCredentialTargetsFromMetadata(action.Metadata)
	found := false
	for _, target := range targets {
		found = found || strings.HasSuffix(target.Field, ".body.opaque")
	}
	if !found {
		t.Fatalf("credential locators = %#v, want opaque body locator", targets)
	}
}

func TestProjectScriptCredentialsRemovesAllSourceVariablesForDeduplicatedRequirement(t *testing.T) {
	t.Parallel()
	const sourcePassword = "same-source-password"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{
		{Name: "metadata_password", Type: "list", Values: []string{sourcePassword}},
		{Name: "target_password", Type: "list", Values: []string{sourcePassword}},
	}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "https://user:{{var.target_password}}@api.example.test/orders",
		Metadata: map[string]any{
			"original_url": "https://user:{{var.metadata_password}}@api.example.test/orders",
		},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "deduplicated-source-variables",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved source variables must not require reentry", result.Requirements)
	}
	originalURL, _ := script.Actions[0].Metadata["original_url"].(string)
	if originalURL != "https://user:{{var.metadata_password}}@api.example.test/orders" {
		t.Fatalf("original URL = %q, want exact imported provenance", originalURL)
	}
	payload, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if !strings.Contains(string(payload), sourcePassword) ||
		!strings.Contains(string(payload), "{{var.target_password}}") ||
		!strings.Contains(originalURL, "{{var.metadata_password}}") ||
		len(script.Parameters) != 2 {
		t.Fatalf("executable script lost deduplicated credential source: %s", payload)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
}

func TestProjectScriptCredentialsRemovesMixedCaseRuntimeParameterCredential(t *testing.T) {
	t.Parallel()
	const sourceToken = "mixed-case-source-token-canary"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name:   "Token",
		Type:   "list",
		Values: []string{sourceToken},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "GET",
		Method: "GET",
		Target: "https://api.example.test/orders?access_token={{var.token}}",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "mixed-case-source-parameter",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved mixed-case parameter must not require reentry", result.Requirements)
	}
	publicResult, marshalErr := json.Marshal(struct {
		Script       *dsl.Script
		Requirements []credentialpolicy.Requirement
	}{
		Script:       script,
		Requirements: result.Requirements,
	})
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if !strings.Contains(string(publicResult), sourceToken) ||
		!strings.Contains(string(publicResult), "{{var.token}}") ||
		len(script.Parameters) != 1 {
		t.Fatalf("executable result lost mixed-case credential source: %s", publicResult)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
}

func TestProjectScriptCredentialsExternalizesConstantDataSourceParameter(t *testing.T) {
	t.Parallel()
	const sourcePassword = "data-source-password-canary"
	script := validBaseScript()
	script.Protocol = "soap"
	script.DataSources = []dsl.DataSource{
		{
			Name:           "credentials",
			Type:           "csv",
			InlineData:     "UserName,Password\nsynthetic-user," + sourcePassword + "\n",
			Strategy:       "sequential",
			FirstRowHeader: true,
			Columns:        []string{"UserName", "Password"},
		},
	}
	script.Parameters = []dsl.Parameter{
		{
			Name:       "UserName",
			Type:       "file",
			DataSource: "credentials",
			Column:     "UserName",
		},
		{
			Name:       "Password",
			Type:       "file",
			DataSource: "credentials",
			Column:     "Password",
		},
	}
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "POST",
		Method:     "POST",
		Target:     "https://api.example.test/login",
		HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: "application/xml"}},
		Headers:    map[string]string{"Content-Type": "application/xml"},
		Body:       "<request><user>{{param.username}}</user><password>{{var.password}}</password></request>",
	}}
	sourceVariables := literalScriptSourceVariables(script, "test_import")
	if sourceVariable, ok := sourceVariables["Password"]; !ok ||
		sourceVariable.Value != sourcePassword {
		t.Fatalf("source variables = %#v, want constant Password data-source value", sourceVariables)
	}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "data-source-parameter",
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, preserved data-source password must not require reentry", result.Requirements)
	}
	publicResult, marshalErr := json.Marshal(struct {
		Script       *dsl.Script
		Requirements []credentialpolicy.Requirement
	}{
		Script:       script,
		Requirements: result.Requirements,
	})
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if !strings.Contains(string(publicResult), sourcePassword) ||
		!strings.Contains(string(publicResult), "{{var.password}}") {
		t.Fatalf("executable result lost data-source credential: %s", publicResult)
	}
	if len(script.Parameters) != 2 {
		t.Fatalf("parameters = %#v, want both executable source bindings", script.Parameters)
	}
	if len(script.DataSources) != 1 ||
		!strings.Contains(script.DataSources[0].InlineData, "Password") ||
		!strings.Contains(script.DataSources[0].InlineData, sourcePassword) ||
		!strings.Contains(script.DataSources[0].InlineData, "synthetic-user") {
		t.Fatalf("preserved data sources = %#v", script.DataSources)
	}
	assertCredentialPreservationFindingsNoGate(t, result.Findings)
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestProjectScriptCredentialsPreservesGovernedRuntimeParameters(t *testing.T) {
	t.Parallel()
	const (
		firstPassword  = "rotating-password-one-canary"
		secondPassword = "rotating-password-two-canary"
		unusedSecret   = "unused-client-secret-canary"
	)
	script := validBaseScript()
	script.Protocol = "soap"
	script.DataSources = []dsl.DataSource{{
		Name:           "credentials",
		Type:           "dat",
		InlineData:     "UserName,Password\nalice," + firstPassword + "\nbob," + secondPassword + "\n",
		Strategy:       "sequential",
		FirstRowHeader: true,
		Columns:        []string{"UserName", "Password"},
	}}
	script.Parameters = []dsl.Parameter{
		{
			Name:       "UserName",
			Type:       "file",
			DataSource: "credentials",
			Column:     "UserName",
		},
		{
			Name:       "Password",
			Type:       "file",
			DataSource: "credentials",
			Column:     "Password",
		},
		{
			Name:   "ClientSecret",
			Type:   "list",
			Values: []string{unusedSecret},
		},
	}
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "POST",
		Method:     "POST",
		Target:     "https://api.example.test/login?label={{param.password}}",
		HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: "application/xml"}},
		Headers:    map[string]string{"Content-Type": "application/xml"},
		Body:       "<request><user>{{param.username}}</user><password>{{param.password}}</password></request>",
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile:          "enterprise_script_archive",
		CandidateKey:           "governed-runtime-parameters",
		RuntimeParameterPolicy: RuntimeParametersPreserveGoverned,
	})
	if err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	if len(result.Requirements) != 0 {
		t.Fatalf("requirements = %#v, want no project-secret externalization", result.Requirements)
	}
	if len(script.Parameters) != 3 ||
		!strings.Contains(script.Actions[0].Body, "{{param.password}}") ||
		!strings.Contains(script.Actions[0].Target, "{{param.password}}") {
		t.Fatalf("runtime parameters were deleted or rewritten: %#v / %#v", script.Parameters, script.Actions[0])
	}
	payload, marshalErr := json.Marshal(script)
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	for _, canary := range []string{firstPassword, secondPassword, unusedSecret} {
		if !strings.Contains(string(payload), canary) {
			t.Fatalf("governed runtime draft lost executable value %q: %s", canary, payload)
		}
	}
	if got, want := script.DataSources[0].InlineData,
		"UserName,Password\nalice,"+firstPassword+"\nbob,"+secondPassword+"\n"; got != want {
		t.Fatalf("rotating credential table = %q, want source bytes %q", got, want)
	}
	if len(script.Parameters[2].Values) != 1 ||
		script.Parameters[2].Values[0] != unusedSecret {
		t.Fatalf("unused credential parameter lost its executable value: %#v", script.Parameters[2])
	}
	findings, err := importreview.FindingsFromScript(script)
	if err != nil {
		t.Fatalf("FindingsFromScript: %v", err)
	}
	for _, finding := range findings {
		if finding.Code == "credential_parameter_source_redacted" {
			t.Fatalf("preserved executable runtime values gained obsolete redaction gate: %#v", findings)
		}
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("DSL validation issues = %#v", issues)
	}
}

func TestLiteralScriptSourceVariablesReadsSingleColumnDAT(t *testing.T) {
	t.Parallel()
	script := validBaseScript()
	script.DataSources = []dsl.DataSource{{
		Name:           "password",
		Type:           "dat",
		InlineData:     "Password\ndata-source-password-canary\n",
		Strategy:       "sequential",
		FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{{
		Name:       "password",
		Type:       "file",
		DataSource: "password",
		Column:     "Password",
	}}
	variables := literalScriptSourceVariables(script, "test_import")
	if variable, ok := variables["password"]; !ok ||
		variable.Value != "data-source-password-canary" {
		t.Fatalf("source variables = %#v", variables)
	}
}

func TestReconcileProtectedScriptCredentialSourcesAfterParameterAttachment(t *testing.T) {
	t.Parallel()
	const sourcePassword = "late-data-source-password-canary"

	// The first pass occurs before an archive adapter has attached its .prm
	// table, so it can only protect the unproven runtime template.
	script := validBaseScript()
	script.Protocol = "soap"
	script.Actions = []dsl.Action{{
		Name:       "request",
		Type:       "POST",
		Method:     "POST",
		Target:     "https://api.example.test/login",
		HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: "application/xml"}},
		Headers:    map[string]string{"Content-Type": "application/xml"},
		Body:       "<request><password>{{var.password}}</password></request>",
	}}
	first, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	})
	if err != nil {
		t.Fatalf("first ProjectScriptCredentials: %v", err)
	}
	if len(first.Requirements) != 0 {
		t.Fatalf("first requirements = %#v, preserved expression must not require binding", first.Requirements)
	}

	// ZIP finalization attaches the source table after the first projection.
	script.DataSources = []dsl.DataSource{{
		Name:           "password",
		Type:           "dat",
		InlineData:     "Password\n" + sourcePassword + "\n",
		Strategy:       "sequential",
		FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{{
		Name:       "password",
		Type:       "file",
		DataSource: "password",
		Column:     "Password",
	}}
	reconciled := ReconcileProtectedScriptCredentialSources(
		script,
		first.Requirements,
		"test_import",
		"candidate",
	)
	if len(reconciled) != 0 {
		t.Fatalf("reconciled requirements = %#v, no protected binding is needed", reconciled)
	}
	publicResult, marshalErr := json.Marshal(struct {
		Script       *dsl.Script
		Requirements []credentialpolicy.Requirement
	}{
		Script:       script,
		Requirements: reconciled,
	})
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	if !strings.Contains(string(publicResult), sourcePassword) ||
		!strings.Contains(string(publicResult), "{{var.password}}") ||
		len(script.Parameters) != 1 ||
		len(script.DataSources) != 1 {
		t.Fatalf("reconciled executable result lost credential source: %s", publicResult)
	}
}

func TestReconcileProtectedScriptCredentialSourcesRetainsResidualParameterReference(t *testing.T) {
	t.Parallel()
	const sourcePassword = "shared-parameter-password-canary"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name:   "password",
		Type:   "list",
		Values: []string{sourcePassword},
	}}
	script.Actions = []dsl.Action{{
		Name:   "request",
		Type:   "POST",
		Method: "POST",
		Target: "https://api.example.test/login",
		Body:   `{"note":"prefix-{{param.password}}-suffix"}`,
	}}
	requirements := []credentialpolicy.Requirement{{
		ID:                       "password-requirement",
		Value:                    "{{param.password}}",
		DeferredSourceExpression: "{{param.password}}",
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: "candidate",
			Field:      "auth.password",
		}},
	}}

	reconciled := ReconcileProtectedScriptCredentialSources(
		script,
		requirements,
		"test_import",
		"candidate",
	)
	if len(reconciled) != 1 || reconciled[0].Value != sourcePassword {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	if len(script.Parameters) != 1 ||
		len(script.Parameters[0].Values) != 1 ||
		script.Parameters[0].Values[0] != sourcePassword {
		t.Fatalf("residually referenced parameter = %#v", script.Parameters)
	}
	if !strings.Contains(script.Actions[0].Body, "{{param.password}}") {
		t.Fatalf("residual parameter reference was changed: %q", script.Actions[0].Body)
	}
}

func TestReconcileProtectedScriptCredentialSourcesRetainsSharedDataBindingForResidualReference(t *testing.T) {
	t.Parallel()
	const sourcePassword = "shared-data-source-password-canary"
	script := validBaseScript()
	script.DataSources = []dsl.DataSource{{
		Name: "credentials", Type: "dat", InlineData: "Password\n" + sourcePassword + "\n",
		Strategy: "sequential", FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{
		{Name: "password", Type: "file", DataSource: "credentials", Column: "Password"},
		{Name: "password_copy", Type: "file", DataSource: "credentials", Column: "Password"},
	}
	script.Actions = []dsl.Action{{
		Name: "request", Type: "POST", Method: "POST", Target: "https://api.example.test/login",
		Body: `{"note":"prefix-{{param.password}}-suffix"}`,
	}}
	requirements := []credentialpolicy.Requirement{{
		ID: "password-requirement", Value: "{{param.password}}",
		DeferredSourceExpression: "{{param.password}}", SourceVariableNames: []string{"password_copy"},
		Targets: []credentialpolicy.TargetRef{{RequestKey: "candidate", Field: "auth.password"}},
	}}

	reconciled := ReconcileProtectedScriptCredentialSources(script, requirements, "test_import", "candidate")
	if len(reconciled) != 1 || reconciled[0].Value != sourcePassword {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	if len(script.Parameters) != 1 || script.Parameters[0].Name != "password" ||
		script.Parameters[0].DataSource != "credentials" || script.Parameters[0].Column != "Password" {
		t.Fatalf("retained parameters = %#v", script.Parameters)
	}
	if len(script.DataSources) != 1 || !strings.Contains(script.DataSources[0].InlineData, sourcePassword) {
		t.Fatalf("retained data source = %#v", script.DataSources)
	}
	if !strings.Contains(script.Actions[0].Body, "{{param.password}}") {
		t.Fatalf("residual parameter reference was changed: %q", script.Actions[0].Body)
	}
}

func TestReconcileProtectedScriptCredentialSourcesRetainsSharedBindingForUnconsumedAlias(t *testing.T) {
	t.Parallel()
	const sourcePassword = "unconsumed-alias-password-canary"
	script := validBaseScript()
	script.DataSources = []dsl.DataSource{{
		Name: "credentials", Type: "dat", InlineData: "Password\n" + sourcePassword + "\n",
		Strategy: "sequential", FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{
		{Name: "credential_password", Type: "file", DataSource: "credentials", Column: "Password"},
		{Name: "display_password", Type: "file", DataSource: "credentials", Column: "Password"},
	}
	script.Actions = []dsl.Action{{
		Name: "request", Type: "POST", Method: "POST", Target: "https://api.example.test/login",
		Body: `{"note":"prefix-{{param.display_password}}-suffix"}`,
	}}
	requirements := []credentialpolicy.Requirement{{
		ID: "password-requirement", Value: "{{param.credential_password}}",
		DeferredSourceExpression: "{{param.credential_password}}",
		Targets:                  []credentialpolicy.TargetRef{{RequestKey: "candidate", Field: "auth.password"}},
	}}

	reconciled := ReconcileProtectedScriptCredentialSources(script, requirements, "test_import", "candidate")
	if len(reconciled) != 1 || reconciled[0].Value != sourcePassword {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	if len(script.Parameters) != 1 || script.Parameters[0].Name != "display_password" ||
		script.Parameters[0].DataSource != "credentials" || script.Parameters[0].Column != "Password" {
		t.Fatalf("retained alias parameter = %#v", script.Parameters)
	}
	if len(script.DataSources) != 1 || !strings.Contains(script.DataSources[0].InlineData, sourcePassword) {
		t.Fatalf("shared alias data source = %#v", script.DataSources)
	}
	if !strings.Contains(script.Actions[0].Body, "{{param.display_password}}") {
		t.Fatalf("residual alias reference was changed: %q", script.Actions[0].Body)
	}
}

func TestReconcileProtectedScriptCredentialSourcesRetainsDirectDataBinding(t *testing.T) {
	t.Parallel()
	const sourcePassword = "direct-data-password-canary"
	script := validBaseScript()
	script.DataSources = []dsl.DataSource{{
		Name: "credentials", Type: "dat", InlineData: "Password\n" + sourcePassword + "\n",
		Strategy: "sequential", FirstRowHeader: true,
	}}
	script.Parameters = []dsl.Parameter{{
		Name: "credential_password", Type: "file", DataSource: "credentials", Column: "Password",
	}}
	script.Actions = []dsl.Action{{
		Name: "request", Type: "POST", Method: "POST", Target: "https://api.example.test/login",
		Body: `{"audit":"{{data.credentials.Password}}"}`,
	}}
	requirements := []credentialpolicy.Requirement{{
		ID: "password-requirement", Value: "{{param.credential_password}}",
		DeferredSourceExpression: "{{param.credential_password}}",
		Targets:                  []credentialpolicy.TargetRef{{RequestKey: "candidate", Field: "auth.password"}},
	}}

	reconciled := ReconcileProtectedScriptCredentialSources(script, requirements, "test_import", "candidate")
	if len(reconciled) != 1 || reconciled[0].Value != sourcePassword {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	if len(script.Parameters) != 0 {
		t.Fatalf("consumed credential parameter = %#v, want removed", script.Parameters)
	}
	if len(script.DataSources) != 1 || !strings.Contains(script.DataSources[0].InlineData, sourcePassword) {
		t.Fatalf("directly referenced data source = %#v, want binding retained", script.DataSources)
	}
	if !strings.Contains(script.Actions[0].Body, "{{data.credentials.Password}}") {
		t.Fatalf("direct data reference was changed: %q", script.Actions[0].Body)
	}
}

func TestReconcileProtectedScriptCredentialSourcesRetainsOpaqueSourceForDirectDataBinding(t *testing.T) {
	t.Parallel()
	const sourcePassword = "opaque-source-password-canary"
	script := validBaseScript()
	script.DataSources = []dsl.DataSource{{
		Name: "credentials", Type: "file", StorageRef: "asset://credentials.dat", Strategy: "sequential",
	}}
	// The list value lets reconciliation materialize the protected requirement;
	// the attached source binding models an importer that also retained the
	// original opaque source relationship.
	script.Parameters = []dsl.Parameter{{
		Name: "credential_password", Type: "list", Values: []string{sourcePassword},
		DataSource: "credentials", Column: "Password",
	}}
	script.Actions = []dsl.Action{{
		Name: "request", Type: "POST", Method: "POST", Target: "https://api.example.test/login",
		Body: `{"user":"{{data.credentials.Username}}"}`,
	}}
	requirements := []credentialpolicy.Requirement{{
		ID: "password-requirement", Value: "{{param.credential_password}}",
		DeferredSourceExpression: "{{param.credential_password}}",
		Targets:                  []credentialpolicy.TargetRef{{RequestKey: "candidate", Field: "auth.password"}},
	}}

	reconciled := ReconcileProtectedScriptCredentialSources(script, requirements, "test_import", "candidate")
	if len(reconciled) != 1 || reconciled[0].Value != sourcePassword {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	if len(script.Parameters) != 0 {
		t.Fatalf("consumed credential parameter = %#v, want removed", script.Parameters)
	}
	if len(script.DataSources) != 1 || script.DataSources[0].StorageRef != "asset://credentials.dat" {
		t.Fatalf("opaque directly referenced source = %#v, want whole source retained", script.DataSources)
	}
	if !strings.Contains(script.Actions[0].Body, "{{data.credentials.Username}}") {
		t.Fatalf("direct data reference was changed: %q", script.Actions[0].Body)
	}
}

func TestReconcileProtectedScriptCredentialSourcesRetainsSourcesWhenReferenceScanCannotMarshal(t *testing.T) {
	t.Parallel()
	const sourcePassword = "unmarshalable-metadata-password-canary"
	script := validBaseScript()
	script.Parameters = []dsl.Parameter{{
		Name: "password", Type: "list", Values: []string{sourcePassword},
	}}
	script.Actions = []dsl.Action{{
		Name: "request", Type: "POST", Method: "POST", Target: "https://api.example.test/login",
		Body:     `{"note":"prefix-{{param.password}}-suffix"}`,
		Metadata: map[string]any{"unsupported": func() {}},
	}}
	requirements := []credentialpolicy.Requirement{{
		ID: "password-requirement", Value: "{{param.password}}", DeferredSourceExpression: "{{param.password}}",
		Targets: []credentialpolicy.TargetRef{{RequestKey: "candidate", Field: "auth.password"}},
	}}

	reconciled := ReconcileProtectedScriptCredentialSources(script, requirements, "test_import", "candidate")
	if len(reconciled) != 1 || reconciled[0].Value != sourcePassword {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	if len(script.Parameters) != 1 || len(script.Parameters[0].Values) != 1 || script.Parameters[0].Values[0] != sourcePassword {
		t.Fatalf("parameter was removed or redacted after an incomplete reference scan: %#v", script.Parameters)
	}
	if !strings.Contains(script.Actions[0].Body, "{{param.password}}") {
		t.Fatalf("residual parameter reference was changed: %q", script.Actions[0].Body)
	}
}

func TestReconcileProtectedScriptCredentialSourcesIgnoresProvenanceOnlyParameterReferences(t *testing.T) {
	t.Parallel()
	const sourcePassword = "provenance-only-password-canary"
	script := validBaseScript()
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Preserved: map[string]any{"authored_expression": "{{param.password}}"},
	}}
	script.Parameters = []dsl.Parameter{{
		Name: "password", Type: "list", Values: []string{sourcePassword},
	}}
	script.Actions = []dsl.Action{{
		Name: "request", Type: "GET", Method: "GET", Target: "https://api.example.test/ready",
	}}
	requirements := []credentialpolicy.Requirement{{
		ID: "password-requirement", Value: "{{param.password}}", DeferredSourceExpression: "{{param.password}}",
		Targets: []credentialpolicy.TargetRef{{RequestKey: "candidate", Field: "auth.password"}},
	}}

	reconciled := ReconcileProtectedScriptCredentialSources(script, requirements, "test_import", "candidate")
	if len(reconciled) != 1 || reconciled[0].Value != sourcePassword {
		t.Fatalf("reconciled requirements = %#v", reconciled)
	}
	if len(script.Parameters) != 0 {
		t.Fatalf("provenance-only token retained a public credential parameter: %#v", script.Parameters)
	}
	if got := script.Metadata.Import.Preserved["authored_expression"]; got != "{{param.password}}" {
		t.Fatalf("import provenance changed: %#v", got)
	}
}

func TestProjectScriptCredentialsRetainsProtectedSnapshotOnTraversalFailure(t *testing.T) {
	t.Parallel()
	const credential = "traversal-fallback-password-canary"
	requests := make([]any, dsl.MaxScriptActionDefinitions)
	requests[0] = map[string]any{
		"name":   "login",
		"method": "POST",
		"target": "https://api.example.test/login",
		"body":   `{"password":"` + credential + `"}`,
	}
	script := validBaseScript()
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source:    "test_import",
		Preserved: map[string]any{},
	}}
	script.Actions = []dsl.Action{{
		Name:   "sequence",
		Type:   "http_sequence",
		Params: map[string]any{"requests": requests},
	}}

	result, err := ProjectScriptCredentials(script, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	})
	if err == nil || !result.FallbackDraft {
		t.Fatalf("ProjectScriptCredentials = %#v, %v; want fallback traversal error", result, err)
	}
	if len(script.Actions) != 1 || script.Actions[0].Type != "source_review" ||
		script.Actions[0].Metadata["execution_blocker"] != "credential_request_traversal_failed" {
		t.Fatalf("fallback actions = %#v, want one blocked review placeholder", script.Actions)
	}
	// The request tree exceeds dsl.MaxScriptActionDefinitions and can never be
	// stored as a valid script, which is why this one fallback still replaces
	// the actions. The base URL is not part of that limit, so it must survive:
	// substituting an .invalid host destroyed an executable value for no reason.
	if script.Config.BaseURL != "https://example.test" {
		t.Fatalf("fallback base URL = %q, want the authored base URL retained", script.Config.BaseURL)
	}
	encoded, _ := script.Metadata.Import.Preserved["credential_traversal_source_snapshot_base64"].(string)
	snapshot, decodeErr := base64.StdEncoding.DecodeString(encoded)
	if decodeErr != nil {
		t.Fatalf("decode fallback snapshot: %v", decodeErr)
	}
	if !strings.Contains(string(snapshot), credential) ||
		!strings.Contains(string(snapshot), `"type":"http_sequence"`) {
		t.Fatalf("fallback snapshot omitted original request/credential: %s", snapshot)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("fallback draft validation issues = %#v", issues)
	}
}

func assertCredentialPreservationFindingsNoGate(
	t *testing.T,
	findings []importreview.Finding,
) {
	t.Helper()
	found := false
	for _, finding := range findings {
		if finding.Code != "import_credential_preserved" {
			continue
		}
		found = true
		if finding.Gate != importreview.GateNone {
			t.Fatalf("credential preservation finding is executable blocker: %#v", finding)
		}
	}
	if !found {
		t.Fatalf("findings = %#v, want informational credential preservation review", findings)
	}
}
