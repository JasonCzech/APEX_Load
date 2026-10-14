package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestRetainedSchemaCredentialRunEvidenceFollowsOperatorSetting pins the origin
// rule at all four runtime entry points at once — engine.New, NewMultiGroup,
// ReplayScript, and shakeout — which is precisely where the four byte-identical
// copies of the credential chokepoint used to live.
//
// A pin retained from an import is script-sourced. It is never removed from the
// script, and whether it is scrubbed out of the run's OUTPUT is the operator's
// call through script.Config.Log.RedactRunEvidence (default: visible).
//
// A value the script merely REFERENCES through {{secret.NAME}} lives in the
// project secret store. The operator does not have it, so it is redacted in BOTH
// modes. Both halves are asserted against the same error string, in both modes,
// so a change that collapses the distinction in either direction fails here.
func TestRetainedSchemaCredentialRunEvidenceFollowsOperatorSetting(t *testing.T) {
	const (
		pin            = "7312049876"
		platformSecret = "vault-issued-bravo-8842"
	)
	// The canary must survive the origin-independent scrubbers, or every
	// assertion below is satisfied by the generic high-entropy matcher and the
	// test cannot detect the loss of platform-managed registration it exists to
	// guard. A 24+ character [A-Za-z0-9+/=_-] run trips scrub's bare-token rule.
	if scrubSensitiveTextWithConfig(platformSecret, nil) != platformSecret {
		t.Fatalf(
			"canary %q is removed by an origin-independent scrubber; this test cannot measure platform-managed registration",
			platformSecret,
		)
	}
	protocolName := registerReplayProtocolWithInitError(
		t,
		errors.New("remote rejected imported pin "+pin+" using store credential "+platformSecret),
	)
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		if name == "PLATFORM_TOKEN" {
			return platformSecret, true
		}
		return "", false
	})
	script := &dsl.Script{
		Name:     "retained-schema-credential",
		Protocol: protocolName,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "POST",
			Method: "POST",
			Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}, {
				Name:  "X-Platform",
				Value: "{{secret.PLATFORM_TOKEN}}",
			}},
			Body: `{"account":"42","pin":` + pin + `}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "openapi:verify",
						"field":       "body",
						"pointer":     "/pin",
						"index":       2,
						"occurrence":  2,
					},
				},
			},
		}},
	}

	// The disclosure surface reports the retained pin in BOTH modes: it exists
	// precisely so an operator can see what an import kept, and the setting
	// governs run output, never the script or its disclosure.
	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if !containsExactString(values, pin) {
		t.Fatalf("retained runtime values = %#v, want schema-annotated pin", values)
	}

	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			script := setRunEvidenceRedaction(script, mode.redact)
			assertOriginRule := func(label string, err error) {
				t.Helper()
				if err == nil {
					t.Fatalf("%s returned nil error", label)
				}
				assertScriptSourcedRunEvidence(t, label, err.Error(), pin, mode.redact)
				if strings.Contains(err.Error(), platformSecret) {
					t.Fatalf("%s error = %q, leaked platform-managed secret %q", label, err, platformSecret)
				}
				if !strings.Contains(err.Error(), redactedPayloadValue) {
					t.Fatalf("%s error = %q, want platform-managed secret replaced by %s", label, err, redactedPayloadValue)
				}
				// The durable script is never rewritten, whatever the setting says.
				if got := script.Actions[0].Body; got != `{"account":"42","pin":`+pin+`}` {
					t.Fatalf("%s rewrote the executable body to %q", label, got)
				}
			}

			_, err := ReplayScript(context.Background(), script, ReplayOptions{SecretResolver: resolver})
			assertOriginRule("replay", err)
			assertOriginRule(
				"shakeout",
				RunScriptShakeoutWithSecretResolver(context.Background(), "shakeout", script, resolver),
			)

			load, err := New(Config{Script: script, VUsers: 1, Duration: time.Second, SecretResolver: resolver})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = load.Run(context.Background())
			assertOriginRule("load", err)

			multi, err := NewMultiGroup(MultiGroupConfig{
				Duration: time.Second,
				Groups: []GroupConfig{{
					Name:           "group",
					Script:         script,
					VUsers:         1,
					RampUp:         0,
					RampDown:       0,
					SecretResolver: resolver,
				}},
			})
			if err != nil {
				t.Fatalf("NewMultiGroup: %v", err)
			}
			_, err = multi.Run(context.Background())
			assertOriginRule("multi-group load", err)
		})
	}
}

func TestImportedCredentialValuesForDisclosureFailsClosedBesideStaleLocator(t *testing.T) {
	const pin = "731"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "verify",
			Type:   "POST",
			Method: "POST",
			Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}},
			Body: `{"code":` + pin + `}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "body.json", "pointer": "/code"},
					map[string]any{"field": "body.json", "pointer": "/removed"},
				},
			},
		}},
	}

	if _, err := ImportedCredentialValues(script); err == nil {
		t.Fatal("strict runtime inspection accepted a stale locator")
	}
	if values, err := ImportedCredentialValuesForDisclosure(script); err == nil ||
		values != nil {
		t.Fatalf("ImportedCredentialValuesForDisclosure() = %#v, %v; want stale locator to fail closed", values, err)
	}
}

// A converter may write a template expression where a JSON value goes (a
// refreshed client timestamp stays a number). The credential locator still
// reads the literal it points at.
func TestImportedCredentialValuesReadTemplatedJSONBodies(t *testing.T) {
	const session = "session-templated-4821"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "har",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:       "events",
			Type:       "POST",
			Method:     "POST",
			Target:     "/v1/events",
			HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: "application/json"}},
			Body:       `{"visitors":[{"session_id":"` + session + `","events":[{"t":{{fn.timestamp.unix_ms}}}]}]}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "body", "pointer": "/visitors/0/session_id"},
				},
			},
		}},
	}
	for name, read := range map[string]func(*dsl.Script) ([]string, error){
		"runtime":    ImportedCredentialValues,
		"disclosure": ImportedCredentialValuesForDisclosure,
	} {
		values, err := read(script)
		if err != nil || len(values) != 1 || values[0] != session {
			t.Fatalf("%s values = %#v, %v; want the session literal", name, values, err)
		}
	}
}

func TestImportedCredentialValuesAcceptsNativeHTTPSequenceCredentialSurfaces(t *testing.T) {
	const (
		cookieValue = "session-native-7319"
		password    = "basic-native-6248"
	)
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "sequence",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Params: map[string]any{
				"requests": []map[string]any{{
					"name":   "request",
					"type":   "GET",
					"method": "GET",
					"target": "/resource",
					"header_list": []map[string]any{{
						"name":  "Cookie",
						"value": "sid=" + cookieValue,
					}},
					"auth": &dsl.AuthConfig{
						Type:     "basic",
						Username: "operator",
						Password: password,
					},
					"metadata": map[string]any{
						dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
							map[string]any{
								"field": "actions[0].params.requests[0].header_list",
								"index": 0,
							},
							map[string]any{
								"field": "actions[0].params.requests[0].auth.password",
							},
						},
					},
				}},
			},
		}},
	}

	values, err := ImportedCredentialValuesForDisclosure(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValuesForDisclosure: %v", err)
	}
	for _, want := range []string{cookieValue, password} {
		if !containsExactString(values, want) {
			t.Fatalf("retained values = %#v, want native sequence credential %q", values, want)
		}
	}
	request := script.Actions[0].Params["requests"].([]map[string]any)[0]
	if got := request["header_list"].([]map[string]any)[0]["value"]; got != "sid="+cookieValue {
		t.Fatalf("native sequence header mutated to %#v", got)
	}
	if got := request["auth"].(*dsl.AuthConfig).Password; got != password {
		t.Fatalf("native sequence auth password mutated to %q", got)
	}
}

func TestImportedRequestHeaderListRejectsInvalidEnabledValue(t *testing.T) {
	tests := []struct {
		name string
		raw  any
	}{
		{
			name: "map slice",
			raw: []map[string]any{{
				"name":    "Authorization",
				"value":   "Bearer {{var.token}}",
				"enabled": "false",
			}},
		},
		{
			name: "any slice",
			raw: []any{map[string]any{
				"name":    "Cookie",
				"value":   "route={{secret.route}}",
				"enabled": 0,
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if headers, ok := importedRequestHeaderList(test.raw); ok || headers != nil {
				t.Fatalf("importedRequestHeaderList() = %#v, %v; want invalid enabled value rejected", headers, ok)
			}
		})
	}
}

func TestLegacyImportedRequestBodiesCollectTypedCredentialsWithoutLocators(t *testing.T) {
	const multipartBoundary = "legacy-credential-boundary"
	tests := []struct {
		name        string
		contentType string
		body        string
		want        []string
		notWant     []string
	}{
		{
			name:        "JSON",
			contentType: "application/json",
			body:        `{"password":"json-password-7319","ordinary":"visible-json-value"}`,
			want:        []string{"json-password-7319"},
			notWant:     []string{"visible-json-value"},
		},
		{
			name:        "XML element and attribute",
			contentType: "application/soap+xml",
			body: `<Envelope sessionToken="xml-attribute-token-7319">` +
				`<password>xml-password-6248</password>` +
				`<ordinary>visible-xml-value</ordinary></Envelope>`,
			want:    []string{"xml-attribute-token-7319", "xml-password-6248"},
			notWant: []string{"visible-xml-value"},
		},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			body:        "password=form-password-7319&ordinary=visible-form-value",
			want:        []string{"form-password-7319"},
			notWant:     []string{"visible-form-value"},
		},
		{
			name:        "multipart",
			contentType: "multipart/form-data; boundary=" + multipartBoundary,
			body: "--" + multipartBoundary + "\r\n" +
				"Content-Disposition: form-data; name=\"password\"\r\n\r\n" +
				"multipart-password-7319\r\n--" + multipartBoundary + "--\r\n",
			want: []string{"multipart-password-7319"},
		},
		{
			name:        "GraphQL envelope",
			contentType: "application/json",
			body: `{"query":"mutation($input: String!) { login(password: $input, token: \"inline-graphql-token-7319\") }",` +
				`"variables":{"input":"graphql-variable-password-6248","ordinary":"visible-variable"}}`,
			want:    []string{"inline-graphql-token-7319", "graphql-variable-password-6248"},
			notWant: []string{"visible-variable"},
		},
		{
			name:        "GraphQL request",
			contentType: "application/graphql",
			body:        `mutation { login(password: "graphql-password-7319") }`,
			want:        []string{"graphql-password-7319"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := dsl.Action{
				Name:   "legacy request",
				Type:   "POST",
				Method: "POST",
				Target: "/login",
				HeaderList: dsl.HeaderList{{
					Name:  "Content-Type",
					Value: test.contentType,
				}},
				Body: test.body,
			}
			script := &dsl.Script{
				Protocol: "http",
				Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
					Source: "postman",
					Preserved: map[string]any{
						dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
					},
				}},
				Actions: []dsl.Action{action},
			}
			originalBody := script.Actions[0].Body

			values, err := ImportedCredentialValues(script)
			if err != nil {
				t.Fatalf("ImportedCredentialValues: %v", err)
			}
			for _, want := range test.want {
				if !containsExactString(values, want) {
					t.Fatalf("retained body values = %#v, want %q", values, want)
				}
			}
			for _, notWant := range test.notWant {
				if containsExactString(values, notWant) {
					t.Fatalf("retained body values = %#v, included ordinary value %q", values, notWant)
				}
			}
			config, err := WithSecretValueRedactionChecked(nil, values)
			if err != nil {
				t.Fatalf("construct runtime matcher: %v", err)
			}
			for _, want := range test.want {
				if got := config.ScrubText("echo=" + want); strings.Contains(got, want) {
					t.Fatalf("credential echo %q was not redacted: %q", want, got)
				}
			}
			if script.Actions[0].Body != originalBody {
				t.Fatalf("credential inspection changed executable body bytes")
			}
		})
	}
}

func TestLegacyBodyCredentialFallbackIgnoresReviewOnlyActionBodies(t *testing.T) {
	const reviewOnlyCredential = "review-source-password-7319"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "review omitted source behavior",
			Type: "review_required",
			Body: `{"password":"` + reviewOnlyCredential + `"}`,
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if containsExactString(values, reviewOnlyCredential) {
		t.Fatalf("review-only body was treated as an executable request credential: %#v", values)
	}
}

func TestLegacyBodyCredentialFallbackFailsClosedForMalformedDeclaredStructure(t *testing.T) {
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "legacy request",
			Type:   "POST",
			Method: "POST",
			Target: "/login",
			HeaderList: dsl.HeaderList{{
				Name: "Content-Type", Value: "application/json",
			}},
			Body: `{"password":"unterminated`,
		}},
	}
	if _, err := ImportedCredentialValues(script); err == nil {
		t.Fatal("malformed declared JSON credential body did not fail closed")
	}
	script.Actions[0].Body = `{"ordinary":"intentionally malformed`
	if _, err := ImportedCredentialValues(script); err != nil {
		t.Fatalf("malformed noncredential load-test payload was unnecessarily blocked: %v", err)
	}
}

func TestImportedCredentialValuesIncludesRecordedResourceURLCredentials(t *testing.T) {
	const (
		password = "cdn-password-7319"
		token    = "cdn-token-6248"
	)
	script := &dsl.Script{
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "browser",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				"recorded_resource_urls": []any{
					"https://operator:" + password + "@cdn.example.test/app.js?token=" + token + "&variant=modern",
					42,
				},
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{password, token} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want %q", values, want)
		}
	}
	if containsExactString(values, "modern") {
		t.Fatalf("retained runtime values = %#v, nonsensitive query value was included", values)
	}
}

func TestImportedCredentialValuesDereferencesExecutableURLSurfaces(t *testing.T) {
	const (
		basePassword     = "base-url-password-63d1"
		tokenURLPassword = "token-url-password-82f4"
		provenanceOnly   = "original-url-password-29a6"
	)
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://base-user:" + basePassword + "@api.example.test",
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "fixture:base",
						"field":       "config.base_url.userinfo",
					},
				},
			},
		}},
		Init: []dsl.Action{{
			Name: "OAuth token",
			Type: "http_oauth2_token",
			Params: map[string]any{
				"token_url": "https://oauth-user:" + tokenURLPassword + "@auth.example.test/token",
			},
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "fixture:oauth",
						"field":       "params.token_url.userinfo",
					},
				},
			},
		}},
		Actions: []dsl.Action{{
			Name:   "Orders",
			Type:   "GET",
			Method: "GET",
			Target: "/orders",
			Metadata: map[string]any{
				// Provenance is deliberately not a runtime locator.
				"original_url": "https://source-user:" + provenanceOnly + "@source.example.test/orders",
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{basePassword, tokenURLPassword} {
		if !containsExactString(values, want) {
			t.Fatalf("retained values = %#v, want executable URL credential %q", values, want)
		}
	}
	if containsExactString(values, provenanceOnly) {
		t.Fatalf("retained values = %#v, provenance-only original URL was treated as executable", values)
	}
}

func TestImportedCredentialValuesTreatsOAuthTokenURLAsLocator(t *testing.T) {
	const (
		password = "oauth-url-password-8cf2"
		token    = "oauth-query-token-417a"
	)
	tokenURL := "https://oauth-user:" + password + "@auth.example.test/token?access_token=" + token + "&region=us"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "raml",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Init: []dsl.Action{{
			Name: "OAuth token", Type: "http_oauth2_token",
			Params: map[string]any{
				"token_url":     tokenURL,
				"client_secret": "{{secret.RAML_CLIENT_SECRET}}",
			},
		}},
	}

	values, err := ImportedCredentialValuesForDisclosure(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValuesForDisclosure: %v", err)
	}
	if containsExactString(values, tokenURL) {
		t.Fatalf("OAuth endpoint locator was classified as a credential: %#v", values)
	}
	for _, want := range []string{password, token} {
		if !containsExactString(values, want) {
			t.Fatalf("embedded OAuth URL credential %q missing from %#v", want, values)
		}
	}
}

func TestImportedCredentialValuesIncludesMarkedBrowserActionValues(t *testing.T) {
	const (
		fillSecret   = "captured-fill-secret-7319"
		selectSecret = "captured-select-secret-6248"
		secondSecret = "captured-select-secret-1957"
	)
	protocolName := registerReplayProtocolWithInitError(
		t,
		errors.New("browser retained "+fillSecret+" "+selectSecret+" "+secondSecret),
	)
	script := &dsl.Script{
		Name:     "retained-browser-credentials",
		Protocol: protocolName,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "browser",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{
			{
				Name:  "Fill password",
				Type:  "browser_fill",
				Value: fillSecret,
				Metadata: map[string]any{
					"source_marked_secret": true,
				},
			},
			{
				Name:  "Select credentials",
				Type:  "browser_select",
				Value: selectSecret,
				Params: map[string]any{
					// []any is the representation after a durable JSON
					// round-trip through storage.
					"values": []any{selectSecret, secondSecret, 42},
				},
				Metadata: map[string]any{
					"source_masked_secret": true,
				},
			},
			{
				Name:  "Unmarked value",
				Type:  "browser_fill",
				Value: "ordinary-browser-value",
			},
		},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{fillSecret, selectSecret, secondSecret} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want %q", values, want)
		}
	}
	if containsExactString(values, "ordinary-browser-value") {
		t.Fatalf("retained runtime values = %#v, unmarked browser value was included", values)
	}
	if script.Actions[0].Value != fillSecret ||
		script.Actions[1].Value != selectSecret ||
		script.Actions[1].Params["values"].([]any)[1] != secondSecret {
		t.Fatalf("credential collection mutated durable browser actions: %#v", script.Actions)
	}

	// Values a recorder marked as secret while capturing a browser flow are still
	// values written into the script the operator imported. They are enumerated by
	// the disclosure surface above in either mode; only whether they are scrubbed
	// back out of that same operator's run OUTPUT follows the setting.
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			setRunEvidenceRedaction(script, mode.redact)
			_, err := ReplayScript(context.Background(), script, ReplayOptions{})
			if err == nil {
				t.Fatal("ReplayScript returned nil error")
			}
			for _, value := range []string{fillSecret, selectSecret, secondSecret} {
				assertScriptSourcedRunEvidence(t, "browser replay", err.Error(), value, mode.redact)
			}
			if script.Actions[0].Value != fillSecret || script.Actions[1].Value != selectSecret {
				t.Fatalf("replay rewrote durable browser actions: %#v", script.Actions)
			}
		})
	}
}

func TestImportedCredentialValuesIncludesSensitiveJavaVMArguments(t *testing.T) {
	const (
		password = "java-vm-password-7319"
		token    = "java-vm-token-6248"
	)
	passwordArgument := "-Dservice.password=" + password
	urlValue := "https://service.example.test/connect?api_key=" + token
	urlArgument := "-Dservice.endpoint=" + urlValue
	protocolName := registerReplayProtocolWithInitError(
		t,
		errors.New("runner rejected "+passwordArgument+" and "+urlArgument),
	)
	script := &dsl.Script{
		Name:     "retained-java-vm-credentials",
		Protocol: protocolName,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			AdditionalVMParameters: []string{
				passwordArgument,
				urlArgument,
				"-Dservice.label=ordinary",
			},
		}},
		Actions: []dsl.Action{{Name: "noop", Type: "noop"}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{
		passwordArgument,
		password,
		urlArgument,
		urlValue,
	} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want exact JVM value %q", values, want)
		}
	}
	if containsExactString(values, "-Dservice.label=ordinary") ||
		containsExactString(values, "ordinary") {
		t.Fatalf("retained runtime values = %#v, nonsensitive JVM argument was included", values)
	}
	if got := script.Config.JavaVuser.AdditionalVMParameters; len(got) != 3 ||
		got[0] != passwordArgument || got[1] != urlArgument {
		t.Fatalf("credential collection mutated durable JVM arguments: %#v", got)
	}

	_, err = ReplayScript(context.Background(), script, ReplayOptions{})
	if err == nil {
		t.Fatal("ReplayScript returned nil error")
	}
	for _, secret := range []string{passwordArgument, password, urlArgument, urlValue, token} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("ReplayScript error leaked retained JVM credential %q: %q", secret, err)
		}
	}
	if !strings.Contains(err.Error(), redactedPayloadValue) {
		t.Fatalf("ReplayScript error = %q, want runtime redaction marker", err)
	}
}

func TestImportedCredentialValuesResolvesCompositionalMultipartAndXMLLocators(t *testing.T) {
	const (
		outerBoundary  = "outer-runtime-locator"
		innerBoundary  = "inner-runtime-locator"
		partHeader     = "part-header-secret-7319"
		firstPassword  = "first-password-decoy-6248"
		secondPassword = "second-password-secret-1957"
		branchPassword = "branch-password-secret-4802"
		formPassword   = "nested-form-secret-9173"
	)
	inner := "--" + innerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"payload\"\r\n" +
		"Content-Type: application/x-www-form-urlencoded\r\n\r\n" +
		"ordinary=keep&password=" + formPassword + "\r\n" +
		"--" + innerBoundary + "--\r\n"
	body := "--" + outerBoundary + "\r\n" +
		"Authorization: Bearer " + partHeader + "\r\n" +
		"Content-Disposition: form-data; name=\"headers\"\r\n\r\n" +
		"ordinary\r\n" +
		"--" + outerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"xml\"\r\n" +
		"Content-Type: application/xml\r\n\r\n" +
		"<root><group><password>" + firstPassword + "</password>" +
		"<password>" + secondPassword + "</password></group>" +
		"<other><password>" + branchPassword + "</password></other></root>\r\n" +
		"--" + outerBoundary + "\r\n" +
		"Content-Disposition: form-data; name=\"nested\"\r\n" +
		"Content-Type: multipart/mixed; boundary=" + innerBoundary + "\r\n\r\n" +
		inner +
		"--" + outerBoundary + "--\r\n"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "fixture",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "multipart",
			Type:   "POST",
			Method: "POST",
			Target: "/upload",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "multipart/form-data; boundary=" + outerBoundary,
			}},
			Body: body,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"field": "body.multipart.parts[0].headers",
						"index": 0,
					},
					map[string]any{
						"field":      "body.multipart.parts[1].body.xml.text",
						"pointer":    "/root/group/password",
						"occurrence": 1,
					},
					map[string]any{
						"field":   "body.multipart.parts[1].body.xml.text",
						"pointer": "/root/other/password",
					},
					map[string]any{
						"field": "body.multipart.parts[2].body.multipart.parts[0].body.form",
						"index": 1,
					},
				},
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{
		"Bearer " + partHeader,
		secondPassword,
		branchPassword,
		formPassword,
	} {
		if !containsExactString(values, want) {
			t.Fatalf("retained runtime values = %#v, want %q", values, want)
		}
	}
	for _, unwanted := range []string{
		firstPassword,
		"ordinary",
		"keep",
	} {
		if containsExactString(values, unwanted) {
			t.Fatalf("retained runtime values = %#v, unexpectedly included %q", values, unwanted)
		}
	}
}

func TestImportedCredentialValuesDereferencesEnterpriseConfigLocatorsAndComponents(t *testing.T) {
	const (
		proxyToken  = "enterprise-proxy-query-component-4d91"
		dsnPassword = "enterprise-dsn-password-component-8a27"
		dsnOption   = "enterprise-dsn-neutral-option-6c53"
		jvmToken    = "enterprise-jvm-url-token-component-2f84"
	)
	proxyURL := "http://proxy.example.test:8080?display=" + proxyToken
	dsn := "postgres://db-user:" + dsnPassword +
		"@db.example.test/orders?display=" + dsnOption
	jvmValue := "https://service.example.test/connect?api_key=" + jvmToken
	jvmArgument := "-Dservice.endpoint=" + jvmValue
	script := &dsl.Script{
		Name:     "enterprise-config-credentials",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "enterprise:proxy",
						"field":       "config.http.proxy.url.query",
						"index":       0,
					},
					map[string]any{
						"request_key": "enterprise:dsn",
						"field":       "config.database.dsn.opaque",
					},
					map[string]any{
						"request_key": "enterprise:jvm",
						"field":       "config.java_vuser.additional_vm_parameters.opaque",
						"index":       0,
					},
				},
			},
		}},
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{Proxy: dsl.ProxyConfig{URL: proxyURL}},
			Database: dsl.DatabaseConfig{
				DSN: dsn,
			},
			JavaVuser: dsl.JavaVuserConfig{
				AdditionalVMParameters: []string{jvmArgument},
			},
		},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{
		proxyToken,
		dsn,
		dsnPassword,
		dsnOption,
		jvmArgument,
		jvmValue,
		jvmToken,
	} {
		if !containsExactString(values, want) {
			t.Fatalf("retained config values = %#v, want %q", values, want)
		}
	}
	if script.Config.HTTP.Proxy.URL != proxyURL ||
		script.Config.Database.DSN != dsn ||
		script.Config.JavaVuser.AdditionalVMParameters[0] != jvmArgument {
		t.Fatalf("credential collection mutated durable config: %#v", script.Config)
	}
}

func TestImportedCredentialValuesKeepsLiteralDSNSiblingsWhenTemplateIsPresent(t *testing.T) {
	const literalPassword = "literal-dsn-sibling-password-3d71"
	dsn := "postgres://db-user:" + literalPassword +
		"@db.example.test/orders?sslpassword={{secret.DB_CERT_PASSWORD}}"
	script := &dsl.Script{
		Protocol: "database",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Config: dsl.ScriptConfig{
			Database: dsl.DatabaseConfig{DSN: dsn},
		},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if !containsExactString(values, literalPassword) {
		t.Fatalf("retained DSN values = %#v, want literal sibling password", values)
	}
	if containsExactString(values, dsn) ||
		containsExactString(values, "{{secret.DB_CERT_PASSWORD}}") {
		t.Fatalf("retained DSN values contain an unresolved template composite: %#v", values)
	}
}

func TestImportedCredentialValuesDereferencesAuthoredPathCredential(t *testing.T) {
	const (
		encoded = "Q7m%2FL4v"
		decoded = "Q7m/L4v"
	)
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "path credential",
			Type:   "request",
			Method: "GET",
			Target: "/accounts/prefix-" + encoded + "-suffix",
			Metadata: map[string]any{
				"openapi": map[string]any{
					"path": "/accounts/prefix-{password}-suffix",
				},
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{
						"request_key": "GET /accounts/prefix-{password}-suffix",
						"field":       "url.path",
						"index":       2,
						"pointer":     "password",
					},
				},
			},
		}},
	}

	values, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, want := range []string{encoded, decoded} {
		if !containsExactString(values, want) {
			t.Fatalf("retained path values = %#v, want %q", values, want)
		}
	}
}

func TestCredentialValuesFromComposedPathSegmentWithOtherParameters(t *testing.T) {
	tests := []struct {
		name       string
		template   string
		parameter  string
		executable string
		want       []string
	}{
		{
			name:       "target after ordinary parameter",
			template:   "prefix-{tenant}-{password}-suffix",
			parameter:  "password",
			executable: "prefix-acme-path-password-7319-suffix",
			want:       []string{"path-password-7319"},
		},
		{
			name:       "target before ordinary parameter",
			template:   "prefix-{password}-{tenant}-suffix",
			parameter:  "password",
			executable: "prefix-secret7319-acme-suffix",
			want:       []string{"secret7319"},
		},
		{
			name:       "repeated target",
			template:   "prefix-{password}-middle-{password}-suffix",
			parameter:  "password",
			executable: "prefix-first-password-middle-second-password-suffix",
			want:       []string{"first-password", "second-password"},
		},
		{
			name:       "encoded target",
			template:   "prefix-{tenant}-{password}-suffix",
			parameter:  "password",
			executable: "prefix-acme-Q7m%2FL4v-suffix",
			want:       []string{"Q7m%2FL4v"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := credentialValuesFromComposedPathSegment(
				test.template,
				test.parameter,
				test.executable,
			)
			if !ok {
				t.Fatalf(
					"credentialValuesFromComposedPathSegment(%q, %q, %q) did not match",
					test.template,
					test.parameter,
					test.executable,
				)
			}
			if len(got) != len(test.want) {
				t.Fatalf("values = %#v, want %#v", got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("values = %#v, want %#v", got, test.want)
				}
			}
		})
	}
}

func TestCredentialValuesFromComposedPathSegmentRejectsAmbiguousEmptyTarget(t *testing.T) {
	if values, ok := credentialValuesFromComposedPathSegment(
		"{password}{tenant}",
		"password",
		"acme-path-password-7319",
	); ok || values != nil {
		t.Fatalf("ambiguous adjacent placeholder values = %#v, %t; want fail closed", values, ok)
	}
}

func TestImportedCredentialValuesForDisclosureDecodesProtectedRepairSources(t *testing.T) {
	const (
		vmPassword   = "protected-vm-password-4f72"
		jdbcPassword = "protected-jdbc-password-8c31"
	)
	encode := func(value string) string {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	script := &dsl.Script{
		Protocol: "java_vuser",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedEnterpriseJavaVMSourceKey: map[string]any{
					"version": 1,
					"rows_base64": []string{
						encode("-javaagent:https://operator:" + vmPassword + "@agent.example.test/agent.jar"),
					},
				},
				dsl.ImportPreservedEnterpriseJavaJDBCSourceKey: map[string]any{
					"version": 1,
					"connections": []map[string]any{{
						"driver_base64": encode("postgres"),
						"dsn_base64": encode(
							"postgres://db-user:" + jdbcPassword + "@db.example.test/app",
						),
						"reason":     "unsupported",
						"source_ref": "Action.java line 4",
					}},
				},
			},
		}},
	}

	runtimeValues, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	for _, password := range []string{vmPassword, jdbcPassword} {
		if containsExactString(runtimeValues, password) {
			t.Fatalf("runtime values include inert repair-source password %q: %#v", password, runtimeValues)
		}
	}
	disclosureValues, err := ImportedCredentialValuesForDisclosure(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValuesForDisclosure: %v", err)
	}
	for _, password := range []string{vmPassword, jdbcPassword} {
		if !containsExactString(disclosureValues, password) {
			t.Fatalf("disclosure values = %#v, want decoded repair-source password %q", disclosureValues, password)
		}
	}
}

func TestImportedCredentialValuesAcceptsEffectiveRequestBaseURLLocators(t *testing.T) {
	const (
		username = "effective-request-user-6c42"
		password = "effective-request-password-9d17"
		token    = "effective-request-token-4f83"
	)
	script := &dsl.Script{
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://" + username + ":" + password +
				"@api.example.test?access_token=" + token + "&region=east",
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{
					{"field": "base_url.userinfo"},
					{"field": "base_url.query", "index": 0},
				},
			},
		}},
	}

	values, err := ImportedCredentialValuesForDisclosure(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValuesForDisclosure: %v", err)
	}
	for _, expected := range []string{password, username + ":" + password, token} {
		if !containsExactString(values, expected) {
			t.Fatalf("values = %#v, want %q", values, expected)
		}
	}
	if containsExactString(values, username) {
		t.Fatalf("values = %#v, bare username was governed as a secret", values)
	}
	if containsExactString(values, "east") {
		t.Fatalf("values = %#v, nonsensitive query value was governed", values)
	}
}

func containsExactString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestImportedCookieCredentialsSkipShortPreferenceValues(t *testing.T) {
	const session = "session-cookie-value-4821"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name:   "sequence",
			Type:   "http_sequence",
			Method: "SEQUENCE",
			Params: map[string]any{
				"requests": []map[string]any{{
					"name":   "request",
					"type":   "GET",
					"method": "GET",
					"target": "/resource",
					"header_list": []map[string]any{{
						"name":  "Cookie",
						"value": "autocomplete=1; lang=en; consent=0:1; sid=ab12; store=" + session,
					}},
					"metadata": map[string]any{
						dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
							map[string]any{"field": "actions[0].params.requests[0].header_list", "index": 0},
						},
					},
				}},
			},
		}},
	}
	values, err := ImportedCredentialValuesForDisclosure(script)
	if err != nil {
		t.Fatal(err)
	}
	// A long value and a short value under a credential-like name are
	// credentials; short preference and flag values are not.
	for _, want := range []string{session, "ab12"} {
		if !containsExactString(values, want) {
			t.Fatalf("values = %#v, want cookie credential %q", values, want)
		}
	}
	for _, notWant := range []string{"1", "en", "0:1"} {
		if containsExactString(values, notWant) {
			t.Fatalf("values = %#v, short cookie preference %q treated as a credential", values, notWant)
		}
	}
}

func TestImportedNamedQueryCredentialsSkipShortValues(t *testing.T) {
	const token = "query-token-value-1234"
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "har",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "GET", Method: "GET",
			Target: "https://example.test/x?cookie=1&token=" + token + "&pin=12&sid=en",
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "url.query", "index": 0},
					map[string]any{"field": "url.query", "index": 1},
					map[string]any{"field": "url.query", "index": 2},
				},
			},
		}},
	}
	values, err := ImportedCredentialValuesForDisclosure(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{token, "12"} {
		if !containsExactString(values, want) {
			t.Fatalf("values = %#v, want %q", values, want)
		}
	}
	for _, notWant := range []string{"1", "en"} {
		if containsExactString(values, notWant) {
			t.Fatalf("values = %#v, short named value %q treated as a credential", values, notWant)
		}
	}
}

func TestImportedLegacyBodyCredentialsSkipShortNamedValues(t *testing.T) {
	const token = "body-token-value-1234"
	request := func(contentType, body string) dsl.Action {
		return dsl.Action{
			Name: "request", Type: "POST", Method: "POST", Target: "https://example.test/x",
			HeaderList: dsl.HeaderList{{Name: "Content-Type", Value: contentType}}, Body: body,
		}
	}
	script := &dsl.Script{
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "har",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{
			request("application/x-www-form-urlencoded", "token="+token+"&session=1&passcode=12"),
			request("application/json", `{"auth":{"token":"`+token+`","sid":"en","pin":"34"}}`),
			request("application/xml", `<login><token>2</token><password>56</password></login>`),
		},
	}
	values, err := ImportedCredentialValuesForDisclosure(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{token, "12", "34", "56"} {
		if !containsExactString(values, want) {
			t.Fatalf("values = %#v, want %q", values, want)
		}
	}
	for _, notWant := range []string{"1", "2", "en"} {
		if containsExactString(values, notWant) {
			t.Fatalf("values = %#v, short named value %q treated as a credential", values, notWant)
		}
	}
}
