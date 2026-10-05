package scriptdisclosure

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestProjectBudgetFallbackPreservesTypedTimestamps(t *testing.T) {
	script, secrets := largeCredentialBearingLabelSource()
	script.CreatedAt = time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	script.UpdatedAt = script.CreatedAt.Add(time.Minute)

	projected, err := ProjectWithCredentialSource(script, script)
	if err != nil {
		t.Fatalf("ProjectWithCredentialSource: %v", err)
	}
	if !projected.CreatedAt.Equal(script.CreatedAt) || !projected.UpdatedAt.Equal(script.UpdatedAt) {
		t.Fatalf(
			"projected timestamps = %s/%s, want %s/%s",
			projected.CreatedAt,
			projected.UpdatedAt,
			script.CreatedAt,
			script.UpdatedAt,
		)
	}
	if projected.Name != script.Name {
		t.Fatalf("projected script name = %q, want %q", projected.Name, script.Name)
	}
	if payload, err := json.Marshal(projected); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(payload), secrets[0]) {
		t.Fatal("budget fallback disclosed a known credential")
	}
}

func TestProjectPreservesRequestSemanticsAndMasksKnownCredentialEverywhere(t *testing.T) {
	const secret = "receipt-projector-canary-7f4c"
	script := &dsl.Script{
		ID: "script-1", Name: "Imported", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://base.example.test/api",
			HTTP: dsl.HTTPConfig{
				RequestTimeoutMs: 45_000,
				Auth:             dsl.AuthConfig{Type: "basic", Username: "operator", Password: secret},
			},
		},
		Parameters: []dsl.Parameter{
			{Name: "password", Type: "list", Values: []string{secret}},
			{Name: "region", Type: "list", Values: []string{"us-east-safe"}},
		},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "POST",
			Target:     "https://operator:" + secret + "@example.test/orders?access_token=" + secret,
			Selector:   secret,
			ObjectRef:  secret,
			Headers:    map[string]string{"Authorization": "Bearer " + secret, "X-Flow-ID": "flow-safe"},
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + secret}, {Name: "X-Flow-ID", Value: "flow-safe"}},
			Body:       `{"password":"` + secret + `","message":"keep-me"}`,
			Assertions: []dsl.Assertion{{Type: "status", Expected: float64(201), Value: "created"}},
			Extractors: []dsl.Extractor{{Name: "order_id", Source: "body", Type: "jsonpath", Expression: "$.id"}},
			Control: &dsl.ControlFlow{Steps: []dsl.Action{{
				Name: "nested", Type: "request", Method: "GET", Target: "/status",
			}}},
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal projected script: %v", err)
	}
	if strings.Contains(string(payload), secret) {
		t.Fatalf("projected script leaked credential: %s", payload)
	}
	if projected.Config.BaseURL != script.Config.BaseURL ||
		projected.Config.HTTP.RequestTimeoutMs != 45_000 ||
		projected.Parameters[1].Values[0] != "us-east-safe" {
		t.Fatalf("ordinary config/parameter changed: %+v", projected)
	}
	action := projected.Actions[0]
	if action.Headers["X-Flow-ID"] != "flow-safe" ||
		len(action.HeaderList) != 2 ||
		action.HeaderList[1].Name != "X-Flow-ID" ||
		len(action.Assertions) != 1 ||
		len(action.Extractors) != 1 ||
		action.Extractors[0].Expression != "$.id" ||
		action.Control == nil ||
		len(action.Control.Steps) != 1 ||
		action.Control.Steps[0].Target != "/status" {
		t.Fatalf("request semantics changed: %+v", action)
	}
	if action.Auth != nil {
		t.Fatalf("unexpected action auth: %+v", action.Auth)
	}
	if projected.Config.HTTP.Auth.Password != credential.Redacted ||
		projected.Config.HTTP.Auth.Type != "basic" ||
		projected.Config.HTTP.Auth.Username != "operator" ||
		projected.Parameters[0].Values[0] != credential.Redacted ||
		action.Selector != credential.Redacted ||
		action.ObjectRef != credential.Redacted ||
		!strings.Contains(action.Body, `"message":"keep-me"`) ||
		!strings.Contains(action.Body, credential.Redacted) {
		t.Fatalf("credential projection incomplete: %+v", projected)
	}

	again, err := Project(projected)
	if err != nil {
		t.Fatalf("Project(Project(script)): %v", err)
	}
	firstJSON, _ := json.Marshal(projected)
	secondJSON, _ := json.Marshal(again)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("projection is not idempotent\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
}

func TestProjectPreservesCredentialLocatorMetadataWithoutTreatingNamesAsValues(t *testing.T) {
	script := &dsl.Script{
		Name: "Locator", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "verify", Type: "POST", Method: "POST", Target: "/verify",
			Body: `{"pin":"runtime-secret"}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
					"request_key": "openapi:verify",
					"field":       "body",
					"pointer":     "/pin",
					"index":       1,
					"occurrence":  1,
				}},
			},
		}},
	}
	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	payload, _ := json.Marshal(projected.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey])
	if !strings.Contains(string(payload), `"/pin"`) ||
		strings.Contains(string(payload), credential.Redacted) {
		t.Fatalf("credential locators were projected as values: %s", payload)
	}
}

func TestProjectFailsClosedWhenNeighboringLocatorIsStale(t *testing.T) {
	const pin = "731"
	script := &dsl.Script{
		Name: "stale locator", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "verify", Type: "POST", Method: "POST", Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}},
			Body: `{"code":` + pin + `}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{
					{"field": "body.json", "pointer": "/code"},
					{"field": "body.json", "pointer": "/removed"},
				},
			},
		}},
	}

	if projected, err := Project(script); err == nil || projected != nil {
		t.Fatalf("Project() = %#v, %v; want neighboring stale locator to fail closed", projected, err)
	}
}

func TestProjectFailsClosedWhenNeutralCredentialFieldWasRenamed(t *testing.T) {
	const pin = "renamed-neutral-pin-4d72"
	script := &dsl.Script{
		Name: "renamed neutral field", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "verify", Type: "POST", Method: "POST", Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}},
			Body: `{"renamed":"` + pin + `"}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
					"field": "body.json", "pointer": "/code",
				}},
			},
		}},
	}

	if projected, err := Project(script); err == nil || projected != nil {
		t.Fatalf("Project() = %#v, %v; want renamed neutral credential field to fail closed", projected, err)
	}
}

func TestProjectFailsClosedForMalformedNeutralCredentialLocators(t *testing.T) {
	const pin = "neutral-malformed-pin-82d4"
	script := &dsl.Script{
		Name: "malformed locator", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "verify", Type: "POST", Method: "POST", Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}},
			Body: `{"code":"` + pin + `"}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "body.json", "pointer": "/code"},
					"malformed-locator",
				},
			},
		}},
	}

	if projected, err := Project(script); err == nil || projected != nil {
		t.Fatalf("Project() = %#v, %v; want malformed locator metadata to fail closed", projected, err)
	}
}

func TestProjectMasksProtectedOpaqueImportSourceEnvelopes(t *testing.T) {
	const (
		proxyPassword = "receipt-enterprise-proxy-password-7c42"
		jdbcPassword  = "receipt-jmeter-jdbc-password-9e63"
	)
	script := &dsl.Script{
		Name: "Opaque sources", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "import",
			Preserved: map[string]any{
				dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey: map[string]any{
					"version":         1,
					"password_base64": base64.StdEncoding.EncodeToString([]byte(proxyPassword)),
				},
				dsl.ImportPreservedJMeterJDBCSourcesKey: []map[string]string{{
					"password":              jdbcPassword,
					"connection_properties": "sslpassword=" + jdbcPassword,
				}},
			},
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET",
			Target: "https://example.test/",
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		proxyPassword,
		jdbcPassword,
		base64.StdEncoding.EncodeToString([]byte(proxyPassword)),
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("opaque import source leaked %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(string(payload), credential.Redacted) {
		t.Fatalf("opaque import source was removed instead of masked: %s", payload)
	}
	if got := script.Metadata.Import.Preserved[dsl.ImportPreservedJMeterJDBCSourcesKey].([]map[string]string)[0]["password"]; got != jdbcPassword {
		t.Fatalf("projection mutated durable JDBC source: %q", got)
	}
}

func TestProjectDereferencesEnterpriseConfigLocatorsForNeutralDuplicates(t *testing.T) {
	const (
		proxyToken  = "disclosure-proxy-neutral-query-7d31"
		dsnPassword = "disclosure-dsn-password-2a64"
		dsnOption   = "disclosure-dsn-neutral-option-8f05"
		jvmToken    = "disclosure-jvm-query-token-4c92"
	)
	script := &dsl.Script{
		Name: "Enterprise config", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{
					{"field": "config.http.proxy.url.query", "index": 0},
					{"field": "config.database.dsn.opaque"},
					{"field": "config.java_vuser.additional_vm_parameters.opaque", "index": 0},
				},
				"neutral_proxy_duplicate":  proxyToken,
				"neutral_dsn_duplicate":    dsnPassword,
				"neutral_option_duplicate": dsnOption,
				"neutral_jvm_duplicate":    jvmToken,
			},
		}},
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{Proxy: dsl.ProxyConfig{
				URL: "http://proxy.example.test:8080?display=" + proxyToken,
			}},
			Database: dsl.DatabaseConfig{
				DSN: "postgres://db-user:" + dsnPassword +
					"@db.example.test/orders?display=" + dsnOption,
			},
			JavaVuser: dsl.JavaVuserConfig{AdditionalVMParameters: []string{
				"-Dservice.endpoint=https://service.example.test/connect?api_key=" + jvmToken,
			}},
		},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{proxyToken, dsnPassword, dsnOption, jvmToken} {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("config locator credential %q escaped disclosure projection: %s", canary, payload)
		}
	}
	if script.Config.HTTP.Proxy.URL !=
		"http://proxy.example.test:8080?display="+proxyToken {
		t.Fatalf("disclosure projection mutated durable proxy config: %#v", script.Config.HTTP.Proxy)
	}
}

func TestProjectPreservesPortableBindingsAndRedactsCredentialDataSourceCells(t *testing.T) {
	script := &dsl.Script{
		Name: "Bindings", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
			dsl.ImportPreservedCredentialParameterNamesKey: []string{"auth_value"},
		}}},
		DataSources: []dsl.DataSource{{
			Name: "inputs", Type: "csv", FirstRowHeader: true,
			InlineData: "region,credential\nwest,short\n",
			Strategy:   "sequential",
		}},
		Parameters: []dsl.Parameter{{
			Name: "auth_value", Type: "file", DataSource: "inputs", Column: "credential",
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET",
			Target:     "https://safe.example.test/orders?authorization={{secret.API_TOKEN}}",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer {{secret.API_TOKEN}}"}},
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if projected.Actions[0].Target != script.Actions[0].Target ||
		projected.Actions[0].HeaderList[0].Value != "Bearer {{secret.API_TOKEN}}" {
		t.Fatalf("portable binding changed: %+v", projected.Actions[0])
	}
	if strings.Contains(projected.DataSources[0].InlineData, "short") ||
		!strings.Contains(projected.DataSources[0].InlineData, credential.Redacted) ||
		!strings.Contains(projected.DataSources[0].InlineData, "west") {
		t.Fatalf("credential data source projection = %q", projected.DataSources[0].InlineData)
	}
}

func TestProjectWithCredentialSourceMasksLiteralAfterPrimaryFieldWasProjected(t *testing.T) {
	const secret = "source-equivalence-canary-19af"
	source := &dsl.Script{
		Name: "Source", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Target: "/",
			Auth:     &dsl.AuthConfig{Type: "basic", Password: secret},
			Selector: secret,
		}},
	}
	alreadyMasked := *source
	alreadyMasked.Actions = append([]dsl.Action(nil), source.Actions...)
	auth := *source.Actions[0].Auth
	auth.Password = credential.Redacted
	alreadyMasked.Actions[0].Auth = &auth

	projected, err := ProjectWithCredentialSource(&alreadyMasked, source)
	if err != nil {
		t.Fatalf("ProjectWithCredentialSource: %v", err)
	}
	if projected.Actions[0].Selector != credential.Redacted {
		t.Fatalf("equivalent neutral-field credential = %q", projected.Actions[0].Selector)
	}
}

func TestProjectShortImportedCredentialDoesNotCorruptUnrelatedScalars(t *testing.T) {
	for _, secret := range []string{"t", "tp", "ttp"} {
		t.Run(secret, func(t *testing.T) {
			script := &dsl.Script{
				Name:        "status",
				Protocol:    "http",
				GeneratedBy: "apexload-postman-converter",
				Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
					Source: "postman",
					Preserved: map[string]any{
						dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
					},
				}},
				Actions: []dsl.Action{{
					Name:   "status",
					Type:   "request",
					Method: "POST",
					Target: "/status",
					HeaderList: dsl.HeaderList{{
						Name:  "Content-Type",
						Value: "application/json",
					}},
					Body: `{"pin":"` + secret + `"}`,
					Params: map[string]any{
						"ordinary_copy": "credential=" + secret,
					},
					Metadata: map[string]any{
						dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
							"field":   "body.json",
							"pointer": "/pin",
						}},
					},
				}},
			}

			projected, err := Project(script)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			if projected.Name != script.Name ||
				projected.Protocol != script.Protocol ||
				projected.Actions[0].Name != script.Actions[0].Name ||
				projected.Actions[0].Type != script.Actions[0].Type ||
				projected.Actions[0].Method != script.Actions[0].Method ||
				projected.Actions[0].Target != script.Actions[0].Target {
				t.Fatalf("short credential corrupted ordinary structure: %#v", projected)
			}
			if strings.Contains(projected.Actions[0].Body, secret) ||
				!strings.Contains(projected.Actions[0].Body, credential.Redacted) {
				t.Fatalf("body projection = %q, want short credential masked", projected.Actions[0].Body)
			}
			if got := projected.Actions[0].Params["ordinary_copy"]; got != "credential="+credential.Redacted {
				t.Fatalf("bounded credential copy = %#v", got)
			}
			if script.Actions[0].Body != `{"pin":"`+secret+`"}` {
				t.Fatalf("projection mutated executable body to %q", script.Actions[0].Body)
			}
		})
	}
}

func TestProjectShortPunctuationCredentialMasksBoundedCopies(t *testing.T) {
	for _, secret := range []string{"!", "/", ":", "$$"} {
		t.Run(secret, func(t *testing.T) {
			script := &dsl.Script{
				Name: "punctuation credential", Version: "1", Protocol: "http",
				Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
					Source: "postman",
					Preserved: map[string]any{
						dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
					},
				}},
				Actions: []dsl.Action{{
					Name: "submit", Type: "request", Method: "POST", Target: "/status",
					HeaderList: dsl.HeaderList{{
						Name: "Content-Type", Value: "application/json",
					}},
					Body: `{"pin":"` + secret + `"}`,
					Params: map[string]any{
						"ordinary_copy": "credential=" + secret,
					},
					Metadata: map[string]any{
						dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
							"field": "body.json", "pointer": "/pin",
						}},
					},
				}},
			}

			projected, err := Project(script)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			if got := projected.Actions[0].Body; got != `{"pin":"`+credential.Redacted+`"}` {
				t.Fatalf("body projection = %q", got)
			}
			if got := projected.Actions[0].Params["ordinary_copy"]; got != "credential="+credential.Redacted {
				t.Fatalf("bounded punctuation copy = %#v", got)
			}
			if got := ProjectLabel("credential="+secret, script); got != "credential="+credential.Redacted {
				t.Fatalf("ProjectLabel = %q", got)
			}
			if projected.Actions[0].Target != "/status" ||
				script.Actions[0].Body != `{"pin":"`+secret+`"}` {
				t.Fatalf("projection changed ordinary or executable structure: %#v", projected.Actions[0])
			}
		})
	}
}

func TestProjectPunctuationCredentialDoesNotCorruptStructuredBodySyntax(t *testing.T) {
	const secret = ":"
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{
			name:        "json",
			contentType: "application/json",
			body:        `{"note":":","safe":"https://example.test/a:b"}`,
			want:        `{"note":"[REDACTED]","safe":"https://example.test/a:b"}`,
		},
		{
			name:        "xml",
			contentType: "application/xml",
			body:        `<root><note>:</note><safe>https://example.test/a:b</safe></root>`,
			want:        `<root><note>[REDACTED]</note><safe>https://example.test/a:b</safe></root>`,
		},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			body:        `note=%3A&safe=https%3A%2F%2Fexample.test%2Fa%3Ab`,
			want:        `note=[REDACTED]&safe=https%3A%2F%2Fexample.test%2Fa%3Ab`,
		},
		{
			name:        "graphql",
			contentType: "application/graphql",
			body:        `mutation { send(note: ":", safe: "https://example.test/a:b") }`,
			want:        `mutation { send(note: "[REDACTED]", safe: "https://example.test/a:b") }`,
		},
		{
			name:        "GraphQL JSON envelope",
			contentType: "application/json",
			body:        `{"query":"query { send(note: \":\", safe: \"https://example.test/a:b\") }"}`,
			want:        `{"query":"query { send(note: \"[REDACTED]\", safe: \"https://example.test/a:b\") }"}`,
		},
		{
			name:        "multipart",
			contentType: `multipart/form-data; boundary=punctuation-boundary`,
			body: "--punctuation-boundary\r\n" +
				"Content-Disposition: form-data; name=\"note\"\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				":\r\n--punctuation-boundary--\r\n",
			want: "--punctuation-boundary\r\n" +
				"Content-Disposition: form-data; name=\"note\"\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				"[REDACTED]\r\n--punctuation-boundary--\r\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{
				Name: "structured punctuation", Version: "1", Protocol: "http",
				Actions: []dsl.Action{{
					Name: "submit", Type: "request", Method: "POST", Target: "/",
					Auth: &dsl.AuthConfig{
						Type: "basic", Username: "operator", Password: secret,
					},
					HeaderList: dsl.HeaderList{{
						Name: "Content-Type", Value: test.contentType,
					}},
					Body: test.body,
				}},
			}

			projected, err := Project(script)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			if got := projected.Actions[0].Body; got != test.want {
				t.Fatalf("projected body = %q, want %q", got, test.want)
			}
			if got := projected.Actions[0].Auth.Password; got != credential.Redacted {
				t.Fatalf("projected auth password = %q", got)
			}
			if script.Actions[0].Body != test.body ||
				script.Actions[0].Auth.Password != secret {
				t.Fatalf("disclosure projection mutated executable request: %+v", script.Actions[0])
			}
		})
	}
}

func TestProjectGraphQLBlockStringUsesSharedEscapeAwareScanner(t *testing.T) {
	const secret = "block-string-canary"
	body := `query { field(note: """prefix \""" block-string-canary suffix""") }`
	script := &dsl.Script{
		Name: "GraphQL block", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{{
			Name: "query", Type: "request", Method: "POST", Target: "/graphql",
			HeaderList: dsl.HeaderList{{
				Name: "Content-Type", Value: "application/graphql",
			}},
			Body: body,
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got, want := projected.Actions[0].Body,
		`query { field(note: "[REDACTED]") }`; got != want {
		t.Fatalf("GraphQL block projection = %q, want %q", got, want)
	}
	if script.Actions[0].Body != body {
		t.Fatalf("projection mutated executable GraphQL: %q", script.Actions[0].Body)
	}
}

func TestProjectXMLCredentialSplitAcrossCharacterNodesFailsClosed(t *testing.T) {
	const secret = "secret"
	body := `<root>se<![CDATA[cr]]>et</root>`
	script := &dsl.Script{
		Name: "XML split", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: "POST", Target: "/",
			HeaderList: dsl.HeaderList{{
				Name: "Content-Type", Value: "application/xml",
			}},
			Body: body,
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Actions[0].Body; got != credential.Redacted {
		t.Fatalf("split XML disclosure = %q, want fail-closed redaction", got)
	}
	if script.Actions[0].Body != body {
		t.Fatalf("projection mutated executable XML: %q", script.Actions[0].Body)
	}
}

func TestProjectKnownCredentialMatcherPreservesStructuredSchema(t *testing.T) {
	tests := []struct {
		name        string
		secret      string
		contentType string
		body        string
		wantType    string
		wantBody    string
	}{
		{
			name:        "JSON member name",
			secret:      "code",
			contentType: "application/json",
			body:        `{"code":"keep"}`,
		},
		{
			name:        "XML declaration",
			secret:      "1.0",
			contentType: "application/xml",
			body:        `<?xml version="1.0" encoding="UTF-8"?><root>keep</root>`,
		},
		{
			name:        "XML namespace",
			secret:      "urn:test",
			contentType: "application/xml",
			body:        `<x:root xmlns:x="urn:test"><x:value>keep</x:value></x:root>`,
		},
		{
			name:        "multipart boundary and MIME type",
			secret:      "inner-boundary",
			contentType: `multipart/mixed; boundary=inner-boundary`,
			body: "--inner-boundary\r\n" +
				"Content-Type: application/json\r\n\r\n" +
				"{\"value\":\"keep\"}\r\n--inner-boundary--\r\n",
			wantType: `multipart/mixed; boundary=apex-redacted-boundary`,
			wantBody: "--apex-redacted-boundary\r\n" +
				"Content-Type: application/json\r\n\r\n" +
				"{\"value\":\"keep\"}\r\n--apex-redacted-boundary--\r\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{
				Name: "schema", Version: "1", Protocol: "http",
				Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
					Auth: dsl.AuthConfig{
						Type: "basic", Username: "operator", Password: test.secret,
					},
				}},
				Actions: []dsl.Action{{
					Name: "submit", Type: "request", Method: "POST", Target: "/",
					HeaderList: dsl.HeaderList{{
						Name: "Content-Type", Value: test.contentType,
					}},
					Body: test.body,
				}},
			}

			projected, err := Project(script)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			wantBody := test.wantBody
			if wantBody == "" {
				wantBody = test.body
			}
			if got := projected.Actions[0].Body; got != wantBody {
				t.Fatalf("structured body projection:\n got %q\nwant %q", got, wantBody)
			}
			wantType := test.wantType
			if wantType == "" {
				wantType = test.contentType
			}
			if got := projected.Actions[0].HeaderList[0].Value; got != wantType {
				t.Fatalf("Content-Type projection = %q, want %q", got, wantType)
			}
			if script.Actions[0].Body != test.body {
				t.Fatalf("projection mutated executable body: %q", script.Actions[0].Body)
			}
		})
	}
}

func TestProjectKnownTypedJSONCredentialRedactsWholeDisclosureBody(t *testing.T) {
	const secret = "731"
	script := &dsl.Script{
		Name: "typed credential", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: "POST", Target: "/",
			HeaderList: dsl.HeaderList{{
				Name: "Content-Type", Value: "application/json",
			}},
			Body: `{"code":731,"ordinary":1}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
					"field": "body.json", "pointer": "/code",
				}},
			},
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Actions[0].Body; got != credential.Redacted {
		t.Fatalf("typed credential body = %q, want fail-closed redaction", got)
	}
	if script.Actions[0].Body != `{"code":731,"ordinary":1}` {
		t.Fatalf("projection mutated executable body: %q", script.Actions[0].Body)
	}
}

func TestProjectMalformedStructuredBodiesFailClosedOnKnownCredential(t *testing.T) {
	const secret = "C4naryX9Qw7Z"
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "partially percent encoded malformed form",
			contentType: "application/x-www-form-urlencoded",
			body:        `copy=%43%34%6e%61%72%79%58%39%51%77%37%5a%ZZ`,
		},
		{
			name:        "folded multipart header",
			contentType: "multipart/form-data; boundary=folded",
			body: "--folded\r\n" +
				"Content-Disposition: form-data;\r\n" +
				" filename=\"" + secret + "\"\r\n\r\n" +
				"safe\r\n--folded--\r\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{
				Name: "malformed", Version: "1", Protocol: "http",
				Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
					Auth: dsl.AuthConfig{
						Type: "basic", Username: "operator", Password: secret,
					},
				}},
				Actions: []dsl.Action{{
					Name: "submit", Type: "request", Method: "POST", Target: "/",
					HeaderList: dsl.HeaderList{{
						Name: "Content-Type", Value: test.contentType,
					}},
					Body: test.body,
				}},
			}

			projected, err := Project(script)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			if got := projected.Actions[0].Body; got != credential.Redacted {
				t.Fatalf("malformed disclosure body = %q, want fail-closed redaction", got)
			}
			if script.Actions[0].Body != test.body {
				t.Fatalf("projection mutated executable body: %q", script.Actions[0].Body)
			}
		})
	}
}

func TestProjectMasksKnownCredentialInMultipartFilename(t *testing.T) {
	const secret = "multipart-filename-canary"
	body := "--upload\r\n" +
		"Content-Disposition: form-data; name=\"upload\"; filename=\"" + secret + "\"\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"safe file\r\n--upload--\r\n"
	script := &dsl.Script{
		Name: "multipart filename", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{{
			Name: "upload", Type: "request", Method: "POST", Target: "/",
			HeaderList: dsl.HeaderList{{
				Name: "Content-Type", Value: "multipart/form-data; boundary=upload",
			}},
			Body: body,
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Actions[0].Body; strings.Contains(got, secret) ||
		!strings.Contains(got, `filename="[REDACTED]"`) ||
		!strings.Contains(got, "--upload") {
		t.Fatalf("multipart filename projection = %q", got)
	}
	if script.Actions[0].Body != body {
		t.Fatalf("projection mutated executable multipart: %q", script.Actions[0].Body)
	}
}

func TestProjectMasksKnownCredentialInMultipartPreambleAndEpilogue(t *testing.T) {
	const secret = "multipart-envelope-canary"
	body := "preamble " + secret + "\r\n" +
		"--envelope\r\n" +
		"Content-Disposition: form-data; name=\"note\"\r\n\r\n" +
		"safe\r\n--envelope--\r\n" +
		"epilogue " + secret
	script := &dsl.Script{
		Name: "multipart envelope", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: "POST", Target: "/",
			HeaderList: dsl.HeaderList{{
				Name: "Content-Type", Value: "multipart/form-data; boundary=envelope",
			}},
			Body: body,
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Actions[0].Body; strings.Contains(got, secret) ||
		!strings.HasPrefix(got, credential.Redacted+"\r\n--envelope") ||
		!strings.HasSuffix(got, credential.Redacted) {
		t.Fatalf("multipart envelope projection = %q", got)
	}
	if script.Actions[0].Body != body {
		t.Fatalf("projection mutated executable multipart: %q", script.Actions[0].Body)
	}
}

func TestProjectCredentialEqualityDoesNotCorruptSchemaDiscriminators(t *testing.T) {
	for _, secret := range []string{"GET", "1", "http", "request", "base64"} {
		t.Run(secret, func(t *testing.T) {
			script := &dsl.Script{
				Name: "schema collision", Version: "1", Protocol: "http",
				Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
					Source: "postman",
					Preserved: map[string]any{
						dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
					},
				}},
				Actions: []dsl.Action{{
					Name: "submit", Type: "request", Method: "GET", Target: "/status",
					HeaderList: dsl.HeaderList{{
						Name: "Content-Type", Value: "application/json",
					}},
					Body:         `{"pin":"` + secret + `"}`,
					BodyEncoding: "base64",
					Params: map[string]any{
						"type":   secret,
						"method": secret,
						"target": "/status",
					},
					Metadata: map[string]any{
						dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
							"field": "body.json", "pointer": "/pin",
						}},
					},
				}},
			}
			// The body is plain JSON for locator coverage; clear the encoding
			// unless that discriminator is the value under test.
			if secret != "base64" {
				script.Actions[0].BodyEncoding = ""
			} else {
				script.Actions[0].Body = base64.StdEncoding.EncodeToString(
					[]byte(`{"pin":"` + secret + `"}`),
				)
			}

			projected, err := Project(script)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			if projected.Version != script.Version ||
				projected.Protocol != script.Protocol ||
				projected.Actions[0].Type != script.Actions[0].Type ||
				projected.Actions[0].Method != script.Actions[0].Method ||
				projected.Actions[0].BodyEncoding != script.Actions[0].BodyEncoding {
				t.Fatalf("credential collision corrupted schema: %#v", projected)
			}
			if projected.Actions[0].Body == script.Actions[0].Body {
				t.Fatalf("credential body was not projected for %q", secret)
			}
			if got := projected.Actions[0].Params["type"]; got != credential.Redacted {
				t.Fatalf("untyped params copy = %#v, want redacted", got)
			}
			if got := projected.Actions[0].Params["method"]; got != credential.Redacted {
				t.Fatalf("spoofed request-shaped params copy = %#v, want redacted", got)
			}
		})
	}
}

func TestProjectPreservesHTTPSequenceChildSchemaDiscriminators(t *testing.T) {
	const secret = "GET"
	script := &dsl.Script{
		Name: "sequence schema", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{{
			Name: "sequence", Type: "http_sequence",
			Params: map[string]any{
				"requests": []any{map[string]any{
					"name": "child", "type": "request",
					"method": secret, "target": "/status",
					"params": map[string]any{
						"method": secret,
					},
				}},
			},
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	requests := projected.Actions[0].Params["requests"].([]any)
	child := requests[0].(map[string]any)
	if got := child["method"]; got != secret {
		t.Fatalf("HTTP sequence child method = %#v, want executable schema", got)
	}
	childParams := child["params"].(map[string]any)
	if got := childParams["method"]; got != credential.Redacted {
		t.Fatalf("HTTP sequence child params method = %#v, want redacted", got)
	}
}

func TestProjectPreservesTypedImportSourceWhenCredentialTextMatches(t *testing.T) {
	const secret = "postman"
	script := &dsl.Script{
		Name: "import source", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:           secret,
			SourceScriptName: "collection",
			Preserved: map[string]any{
				"source": secret,
			},
		}},
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Metadata.Import.Source; got != secret {
		t.Fatalf("typed import source = %q, want %q", got, secret)
	}
	if got := projected.Metadata.Import.Preserved["source"]; got != credential.Redacted {
		t.Fatalf("untyped preserved source = %#v, want redacted", got)
	}
}

func TestProjectTreatsSourceAsSchemaOnlyInTypedContexts(t *testing.T) {
	const secret = "body"
	script := &dsl.Script{
		Name: "source contexts", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: secret,
		}},
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{
			{
				Name: "request", Type: "request", Method: "GET", Target: "/",
				Extractors: []dsl.Extractor{{
					Name: "id", Source: secret, Type: "jsonpath", Expression: "$.id",
				}},
			},
			{
				Name: "loop", Type: "control_loop",
				Control: &dsl.ControlFlow{
					ForEach: &dsl.ForEachLoop{Source: secret, Var: secret},
					Steps: []dsl.Action{{
						Name: "child", Type: "request", Method: "GET", Target: "/",
					}},
				},
			},
		},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Metadata.Import.Source; got != secret {
		t.Fatalf("typed import source = %q, want %q", got, secret)
	}
	if got := projected.Actions[0].Extractors[0].Source; got != secret {
		t.Fatalf("typed extractor source = %q, want %q", got, secret)
	}
	if got := projected.Actions[1].Control.ForEach.Source; got != credential.Redacted {
		t.Fatalf("runtime foreach source = %q, want redacted", got)
	}
	if got := projected.Actions[1].Control.ForEach.Var; got != credential.Redacted {
		t.Fatalf("runtime foreach variable = %q, want redacted", got)
	}
	if script.Actions[1].Control.ForEach.Source != secret {
		t.Fatal("disclosure projection mutated the durable foreach source")
	}
}

func TestProjectMasksKnownCredentialsInMalformedMIMEHeaders(t *testing.T) {
	const secret = "Giraffe7!"
	script := &dsl.Script{
		Name: "mime disclosure", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{
			Auth: dsl.AuthConfig{Type: "basic", Password: secret},
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "POST", Target: "/",
			HeaderList: dsl.HeaderList{
				{Name: "Content-Transfer-Encoding", Value: "7bit " + secret},
				{Name: "MIME-Version", Value: "1.0 " + secret},
				{
					Name:  "Content-Type",
					Value: `application/json; x-note="` + secret + `"`,
				},
			},
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	for index, header := range projected.Actions[0].HeaderList {
		if strings.Contains(header.Value, secret) {
			t.Fatalf("projected MIME header %d leaked credential: %#v", index, header)
		}
	}
	if script.Actions[0].HeaderList[0].Value != "7bit "+secret ||
		script.Actions[0].HeaderList[1].Value != "1.0 "+secret {
		t.Fatal("disclosure projection mutated durable MIME headers")
	}
}

func TestKnownCredentialComparisonBudgetFailsClosedDeterministically(t *testing.T) {
	p := &projector{knownSecrets: []string{strings.Repeat("x", maxCredentialMatcherNodes+1)}}
	p.configureKnownMatching(int(maxKnownCredentialComparisonWork))
	if !p.maskAllKnownValues {
		t.Fatal("expected comparison work estimate to enable fail-closed masking")
	}
	if got := p.replaceKnown("ordinary value"); got != credential.Redacted {
		t.Fatalf("budget-exhausted scalar = %q", got)
	}
	if got := p.projectString("GET", projectionContext{
		key: "method", schemaScalar: true,
	}); got != "GET" {
		t.Fatalf("budget fallback corrupted schema discriminator: %q", got)
	}
	p.knownSecrets = []string{"credential-name"}
	if got := p.projectString("ordinary-name", projectionContext{
		key: "name", scriptName: true,
	}); got != "ordinary-name" {
		t.Fatalf("budget fallback corrupted ordinary script name: %q", got)
	}
	if got := p.projectString("credential-name", projectionContext{
		key: "name", scriptName: true,
	}); got != credential.Redacted {
		t.Fatalf("budget fallback disclosed credential-equal script name: %q", got)
	}
}

func TestKnownCredentialComparisonBudgetChargesEveryCandidateSecret(t *testing.T) {
	p := &projector{
		knownSecrets: []string{
			"credential-one",
			"credential-two",
			"credential-three",
			"credential-four",
		},
	}
	p.configureKnownMatching(1)
	p.knownComparisonRemaining = 100

	value := strings.Repeat("ordinary", 4)
	if got := p.replaceKnown(value); got != credential.Redacted {
		t.Fatalf("budget-exhausted scalar = %q, want redacted", got)
	}
	if !p.maskAllKnownValues || p.knownComparisonRemaining != 0 {
		t.Fatalf(
			"budget state = maskAll:%v remaining:%d",
			p.maskAllKnownValues,
			p.knownComparisonRemaining,
		)
	}
}

func TestProjectMasksStructuredBodyCredentialRepeatedInNeutralName(t *testing.T) {
	const secret = "structured-equivalence-canary-69d1"
	tests := []struct {
		name        string
		contentType string
		body        string
		encoding    string
	}{
		{
			name:        "xml mixed content",
			contentType: "application/xml",
			body:        `<root><password>prefix<token>` + secret + `</token></password></root>`,
		},
		{
			name:        "multipart",
			contentType: `multipart/form-data; boundary=apex-boundary`,
			body: "--apex-boundary\r\n" +
				`Content-Disposition: form-data; name="password"` + "\r\n\r\n" +
				secret + "\r\n--apex-boundary--\r\n",
		},
		{
			name:        "graphql",
			contentType: "application/graphql",
			body:        `mutation { login(password: "` + secret + `") { ok } }`,
		},
		{
			name:        "base64 xml",
			contentType: "application/xml",
			body: base64.StdEncoding.EncodeToString(
				[]byte(`<root token="` + secret + `"/>`),
			),
			encoding: "base64",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{
				Name: secret, Version: "1", Protocol: "http",
				Actions: []dsl.Action{{
					Name:         secret,
					Type:         "request",
					Method:       "POST",
					Target:       "/",
					HeaderList:   dsl.HeaderList{{Name: "Content-Type", Value: test.contentType}},
					Body:         test.body,
					BodyEncoding: test.encoding,
				}},
			}
			projected, err := Project(script)
			if err != nil {
				t.Fatalf("Project: %v", err)
			}
			payload, err := json.Marshal(projected)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(payload), secret) {
				t.Fatalf("structured credential leaked through a neutral field: %s", payload)
			}
			if projected.Name != credential.Redacted ||
				projected.Actions[0].Name != credential.Redacted {
				t.Fatalf("neutral duplicate was not masked: %+v", projected)
			}
		})
	}
}

func TestProjectMasksCredentialsThatResembleDisclosureSentinels(t *testing.T) {
	markerCredential := "prefix" + credential.Redacted + "suffix"
	whitespaceCredential := "    "
	script := &dsl.Script{
		Name:     "sentinel credential",
		Version:  "1",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{
					Type:     "basic",
					Password: markerCredential,
				},
				Proxy: dsl.ProxyConfig{Password: whitespaceCredential},
			},
		},
		DataSources: []dsl.DataSource{{
			Name:           "credential rows",
			Type:           "csv",
			FirstRowHeader: true,
			InlineData: "username,password\n" +
				"marker," + markerCredential + "\n" +
				"spaces,\"" + whitespaceCredential + "\"\n",
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: "GET",
			Target: "https://example.test",
			Params: map[string]any{
				"marker_copy":     markerCredential,
				"whitespace_copy": whitespaceCredential,
				"ordinary_text":   "left" + whitespaceCredential + "right",
			},
		}},
	}

	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if projected.Config.HTTP.Auth.Password != credential.Redacted {
		t.Fatalf("auth password = %q, want redacted", projected.Config.HTTP.Auth.Password)
	}
	if projected.Config.HTTP.Proxy.Password != credential.Redacted {
		t.Fatalf("proxy password = %q, want redacted", projected.Config.HTTP.Proxy.Password)
	}
	if got := projected.Actions[0].Params["marker_copy"]; got != credential.Redacted {
		t.Fatalf("marker copy = %#v, want redacted", got)
	}
	if got := projected.Actions[0].Params["whitespace_copy"]; got != credential.Redacted {
		t.Fatalf("whitespace copy = %#v, want redacted", got)
	}
	if got := projected.Actions[0].Params["ordinary_text"]; got != "left"+whitespaceCredential+"right" {
		t.Fatalf("ordinary text = %#v, want formatting preserved", got)
	}
	if strings.Contains(projected.DataSources[0].InlineData, markerCredential) ||
		strings.Contains(projected.DataSources[0].InlineData, "\""+whitespaceCredential+"\"") {
		t.Fatalf("data source retained a sentinel-like credential: %q", projected.DataSources[0].InlineData)
	}
	if got := strings.Count(projected.DataSources[0].InlineData, credential.Redacted); got != 2 {
		t.Fatalf("data source redaction count = %d, want 2: %q", got, projected.DataSources[0].InlineData)
	}
}

// A short value under a credential-like query name (a cookie=1 flag) is not
// credential material, so it never marks a DNT header or an HTTP version.
func TestProjectShortNamedQueryValueDoesNotMarkOrdinaryTokens(t *testing.T) {
	script := &dsl.Script{
		Name: "flags", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "har",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "page", Type: "request", Method: "GET", Target: "https://example.test/x?cookie=1",
			HeaderList: dsl.HeaderList{{Name: "DNT", Value: "1"}},
			Metadata: map[string]any{
				"http_version": "HTTP/1.1",
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{map[string]any{"field": "url.query", "index": 0}},
			},
		}},
	}
	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := projected.Actions[0]; got.HeaderList[0].Value != "1" || got.Metadata["http_version"] != "HTTP/1.1" {
		t.Fatalf("short named value marked ordinary tokens: %#v", got)
	}
}

// Short values under credential-like header and field names are not credential
// material either, unless the name is password-like.
func TestProjectShortNamedHeaderAndFieldValues(t *testing.T) {
	script := &dsl.Script{
		Name: "fields", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "https://example.test",
			HeaderList: dsl.HeaderList{{Name: "X-Auth-Token", Value: "2"}},
			Params: map[string]any{
				"session_token": "1",
				"api_token":     "abcd1234",
				"password":      "12",
				"count":         "1",
				"level":         "2",
				"token_echo":    "abcd1234",
				"password_echo": "12",
			},
		}},
	}
	projected, err := Project(script)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	params := projected.Actions[0].Params
	if params["count"] != "1" || params["level"] != "2" {
		t.Fatalf("short named values marked ordinary values: %#v", params)
	}
	if params["token_echo"] != credential.Redacted || params["password_echo"] != credential.Redacted {
		t.Fatalf("long and password-like values were not matched: %#v", params)
	}
}
