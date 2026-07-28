package scriptdisclosure

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

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
					"pointer":     "/password",
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
	if !strings.Contains(string(payload), `"/password"`) ||
		strings.Contains(string(payload), credential.Redacted) {
		t.Fatalf("credential locators were projected as values: %s", payload)
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
