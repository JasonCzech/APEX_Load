package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func secretBearingScript(id, projectID string) *dsl.Script {
	return &dsl.Script{
		ID: id, Name: "Secret script", Version: "1", Protocol: "http", ProjectID: projectID,
		Config: dsl.ScriptConfig{
			BaseURL: "https://url-user:url-pass@example.test/orders?token=url-token&keep=yes",
			HTTP: dsl.HTTPConfig{
				Auth:           dsl.AuthConfig{Type: "bearer", Username: "global-user", Password: "global-password", Token: "global-token"},
				Proxy:          dsl.ProxyConfig{URL: "https://proxy-user:proxy-pass@proxy.test?api_key=proxy-key", Username: "proxy-user", Password: "proxy-password"},
				DefaultHeaders: map[string]string{"Authorization": "Bearer global-header-token", "X-API-Key": "header-api-key", "X-Custom-Secret": "short", "X-Safe": "safe"},
			},
			GRPC:      dsl.GRPCConfig{Metadata: map[string]string{"authorization": "grpc-token", "safe": "safe"}},
			SOAP:      dsl.SOAPConfig{WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "soap-user", Password: "soap-password"}}},
			Database:  dsl.DatabaseConfig{DSN: "postgres://db-user:db-password@db.test/main", DSNSecret: "{{ secret.database-dsn }}"},
			Network:   dsl.NetworkConfig{Address: "ldap://ldap-user:ldap-password@ldap.test?token=ldap-token", Username: "ldap-user", Password: "ldap-password"},
			SAPGUI:    dsl.SAPGUIConfig{ConnectionString: "sap-user:sap-password", Username: "sap-user", Password: "sap-password"},
			JMS:       dsl.JMSConfig{BrokerURL: "https://jms-user:jms-password@jms.test?token=jms-token", Username: "jms-user", Password: "jms-password", Properties: map[string]string{"client.secret": "jms-client-secret"}},
			Kafka:     dsl.KafkaConfig{SASLUsername: "kafka-user", SASLPassword: "kafka-password", SASLPasswordSecret: "{{secret.kafka_password}}", SchemaRegistryURL: "https://schema-user:schema-password@schema.test?api_key=schema-key", Properties: map[string]string{"sasl.password": "property-password"}},
			EventHubs: dsl.EventHubsConfig{ConnectionString: "Endpoint=sb://hub/;SharedAccessKey=eventhub-key", ConnectionStringSecret: "{{secret.eventhub_connection}}"},
			Browser: dsl.BrowserConfig{
				HTTPCredentials:    dsl.BrowserHTTPCredentials{Username: "browser-user", Password: "browser-password"},
				Proxy:              dsl.BrowserProxyConfig{Server: "https://browser-user:browser-password@proxy.test", Username: "browser-proxy-user", Password: "browser-proxy-password"},
				ClientCertificates: []dsl.BrowserClientCertificate{{Passphrase: "certificate-passphrase"}},
			},
		},
		Actions: []dsl.Action{{
			Name: "outer", Type: "control_if", Target: "/outer",
			Control: &dsl.ControlFlow{Steps: []dsl.Action{{
				Name: "nested", Type: "request", Method: http.MethodPost,
				Target:     "https://action-user:action-password@example.test?access_token=action-token&ref={{secret.query_token}}",
				Headers:    map[string]string{"Authorization": "Bearer action-token", "X-API-Key": "action-api-key", "X-Custom-Token": "tiny", "X-Secret-Ref": "Bearer {{secret.action_token}}"},
				HeaderList: dsl.HeaderList{{Name: "Cookie", Value: "session=action-cookie"}},
				Body:       `{"password":"body-password","safe":"visible","token":"{{secret.body_token}}"}`,
				Params: map[string]any{
					"client_secret": "param-secret",
					"signature":     "short-signature",
					"account_key":   "short-account-key",
					"csrf":          "short-csrf",
					"pwd":           "short-pwd",
					"nested":        map[string]any{"api_key": "nested-secret"},
				},
				Metadata: map[string]any{"authorization": "metadata-secret", "nested": map[string]any{"password": "nested-metadata-secret"}},
				Auth:     &dsl.AuthConfig{Username: "action-user", Password: "action-password", Token: "action-auth-token"},
			}, {
				Name: "fill password", Type: "input", Target: "password", Selector: "input[type=password]", Value: "short-action-password",
			}}},
		}},
	}
}

func TestDisclosureSafeScriptNameSurvivesLargeCredentialSource(t *testing.T) {
	script := secretBearingScript("large-name-projection", "project-a")
	script.Actions[0].Params = map[string]any{
		"ordinary_blob": strings.Repeat("x", 4<<20),
	}

	if got := disclosureSafeScriptName("acc_wls_reg_1", script); got != "acc_wls_reg_1" {
		t.Fatalf("disclosureSafeScriptName() = %q, want stored name", got)
	}
	if got := disclosureSafeScriptName("global-password", script); got != maskedScriptSecret {
		t.Fatalf("credential-equal script name = %q, want redacted", got)
	}
}

// seedLegacySecretScript models a pre-upgrade in-memory row. Current durable
// persistence APIs reject these plaintext credentials; projection/export tests
// still need to prove that an upgraded legacy row cannot be disclosed.
func seedLegacySecretScript(server *Server, script *dsl.Script) *StoredScript {
	script = cloneScriptDefinition(script)
	if script.RefKey == "" {
		script.RefKey = "LOC-SCR-9999-v1"
	}
	now := time.Now().UTC()
	stored := &StoredScript{
		ID: script.ID, RefKey: script.RefKey, Name: script.Name, Protocol: script.Protocol,
		Version: script.Version, ProjectID: script.ProjectID, FolderID: cloneStringPtr(script.FolderID),
		CreatedAt: now, AuthoringHealth: buildStoredScriptAuthoringHealth(script), Script: script,
	}
	server.runner.mu.Lock()
	server.runner.scripts[stored.ID] = stored
	server.runner.observeAndIndexRefKeyLocked(stored.RefKey, stored.ID)
	server.runner.mu.Unlock()
	return stored
}

func TestMaskScriptSecretsProjectsEveryCredentialSurface(t *testing.T) {
	raw := secretBearingScript("projection", "project-a")
	masked := maskScriptSecrets(raw)
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{
		"url-pass", "global-password", "global-token", "proxy-password", "header-api-key", "grpc-token",
		"soap-password", "db-password", "ldap-password", "sap-password", "jms-password", "jms-client-secret",
		"kafka-password", "schema-password", "eventhub-key", "browser-password", "certificate-passphrase",
		"action-password", "action-token", "action-api-key", "action-cookie", "body-password", "param-secret",
		"short-signature", "short-account-key", "short-csrf", "short-pwd",
		"nested-secret", "metadata-secret", "nested-metadata-secret", "short-action-password", `"X-Custom-Secret":"short"`, `"X-Custom-Token":"tiny"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("masked script leaked %q: %s", forbidden, text)
		}
	}
	for _, reference := range []string{"{{ secret.database-dsn }}", "{{secret.kafka_password}}", "{{secret.eventhub_connection}}", "{{secret.query_token}}", "{{secret.action_token}}", "{{secret.body_token}}"} {
		if !strings.Contains(text, reference) {
			t.Fatalf("masked script lost secret reference %q: %s", reference, text)
		}
	}
	if !strings.Contains(text, maskedScriptSecret) || raw.Config.HTTP.Auth.Password != "global-password" {
		t.Fatal("projection did not redact deeply or mutated the runtime script")
	}
}

func TestMaskScriptSecretsUsesImportedCredentialParameterProvenance(t *testing.T) {
	const (
		listCredential = "tiny-list-credential"
		cellCredential = "tiny-cell-credential"
	)
	script := &dsl.Script{
		ID: "neutral-credential-parameters", Name: "Neutral parameters", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialParameterNamesKey: []string{"foo", "bar"},
			},
		}},
		Parameters: []dsl.Parameter{
			{Name: "foo", Type: "list", Values: []string{listCredential}},
			{Name: "bar", Type: "file", DataSource: "postman", Column: "bar"},
			{Name: "label", Type: "file", DataSource: "postman", Column: "label"},
		},
		DataSources: []dsl.DataSource{{
			Name: "postman", Type: "csv", Strategy: "sequential",
			FirstRowHeader: true, Columns: []string{"bar", "label"},
			InlineData: "bar,label\n" + cellCredential + ",ordinary\n",
		}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + listCredential}},
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(listCredential)) ||
		bytes.Contains(payload, []byte(cellCredential)) {
		t.Fatalf("credential source leaked through masked script: %s", payload)
	}
	if !bytes.Contains(payload, []byte("ordinary")) {
		t.Fatalf("nonsensitive source cell was lost: %s", payload)
	}
	if got := masked.Parameters[0].Values; len(got) != 1 ||
		got[0] != maskedScriptSecret {
		t.Fatalf("neutral credential list parameter = %#v", got)
	}
	if !strings.Contains(masked.DataSources[0].InlineData, maskedScriptSecret) ||
		strings.Contains(masked.DataSources[0].InlineData, cellCredential) {
		t.Fatalf("neutral credential data-source column = %q", masked.DataSources[0].InlineData)
	}
	if script.Parameters[0].Values[0] != listCredential ||
		!strings.Contains(script.DataSources[0].InlineData, cellCredential) {
		t.Fatal("projection mutated the durable execution copy")
	}
}

func TestMaskScriptSecretsTreatsDevWebGatedSourceAsOpaque(t *testing.T) {
	const canary = "devweb-opaque-source-canary-20d4"
	script := &dsl.Script{
		ID: "devweb-opaque", Name: "DevWeb opaque", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "devweb",
			Preserved: map[string]any{
				"devweb_gated_source": map[string]string{
					"main.js": `const signingMaterial = "` + canary + `";`,
				},
			},
		}},
		Actions: []dsl.Action{{
			Name: "Review", Type: "review_required",
			Value:    "Source behavior requires operator review before execution.",
			Metadata: map[string]any{"execution_blocker": "import_review_required"},
		}},
	}

	masked := maskScriptSecrets(script)
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(canary)) {
		t.Fatalf("masked script disclosed opaque DevWeb source: %s", payload)
	}
	if !bytes.Contains(payload, []byte(maskedScriptSecret)) {
		t.Fatalf("masked script omitted opaque-source marker: %s", payload)
	}
	rawSnapshot := script.Metadata.Import.Preserved["devweb_gated_source"].(map[string]string)
	if !strings.Contains(rawSnapshot["main.js"], canary) {
		t.Fatalf("projection mutated durable DevWeb source: %#v", rawSnapshot)
	}
}

func TestMaskScriptSecretsRedactsUnsafeJavaVMArgumentsWithoutChangingSafeSiblings(t *testing.T) {
	const canary = "q7W9pL2xN8vC4mR6tY1kH5sF3dJ0aB9uE2zG"
	script := &dsl.Script{
		ID: "legacy-java-vm-argument", Name: "Legacy Java", Version: "1", Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			AdditionalVMParameters: []string{
				"-Xmx512m",
				"-Dlabel=" + canary,
				"-Dendpoint=https://example.test/path#token=short-secret",
			},
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	got := masked.Config.JavaVuser.AdditionalVMParameters
	if len(got) != 3 || got[0] != "-Xmx512m" ||
		got[1] != maskedScriptSecret || got[2] != maskedScriptSecret {
		t.Fatalf("projected Java VM parameters = %#v", got)
	}
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), canary) ||
		strings.Contains(string(payload), "short-secret") {
		t.Fatalf("projected script leaked Java VM credential material: %s", payload)
	}
	if requirements := discoverSecretBindingRequirements(masked, 0); len(requirements) != 0 {
		t.Fatalf("Java VM argv produced forbidden secret-binding requirements: %#v", requirements)
	}
	if script.Config.JavaVuser.AdditionalVMParameters[1] != "-Dlabel="+canary {
		t.Fatal("projection mutated the stored Java VM parameter list")
	}
}

func TestMaskScriptSecretsRedactsJavaRunnerCommandCredentialWithoutChangingSafeCommand(t *testing.T) {
	const canary = "runner-command-password-93c1"
	script := &dsl.Script{
		ID: "legacy-java-runner-command", Name: "Legacy Java", Version: "1", Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			RunnerCommand: "/opt/apex/runner --password=" + canary,
			HarnessJar:    "/opt/apex/harness.jar",
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	if strings.Contains(masked.Config.JavaVuser.RunnerCommand, canary) ||
		!strings.Contains(masked.Config.JavaVuser.RunnerCommand, maskedScriptSecret) {
		t.Fatalf("projected Java runner command = %q", masked.Config.JavaVuser.RunnerCommand)
	}
	if masked.Config.JavaVuser.HarnessJar != "/opt/apex/harness.jar" {
		t.Fatalf("safe Java harness path changed: %q", masked.Config.JavaVuser.HarnessJar)
	}
	if script.Config.JavaVuser.RunnerCommand != "/opt/apex/runner --password="+canary {
		t.Fatal("projection mutated the stored Java runner command")
	}
}

func TestMaskScriptSecretsRedactsSOAPActionCredentialsWithoutChangingSafeFields(t *testing.T) {
	const canary = "soap-action-password-a813"
	script := &dsl.Script{
		ID: "legacy-soap-action", Name: "SOAP action", Version: "1", Protocol: "soap",
		Actions: []dsl.Action{{
			Name: "Call service", Type: "request", Method: http.MethodPost, Target: "/service",
			SOAP: &dsl.SOAPActionConfig{
				Action:    "https://operator:" + canary + "@service.test/action?token=" + canary + "&safe=yes",
				Operation: "GetAccount",
				BodyMode:  "raw_xml",
			},
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil || len(masked.Actions) != 1 || masked.Actions[0].SOAP == nil {
		t.Fatalf("masked script = %#v", masked)
	}
	soap := masked.Actions[0].SOAP
	if strings.Contains(soap.Action, canary) ||
		!strings.Contains(soap.Action, maskedScriptSecret) {
		t.Fatalf("projected SOAP action = %q", soap.Action)
	}
	if soap.Operation != "GetAccount" || soap.BodyMode != "raw_xml" {
		t.Fatalf("safe SOAP fields changed: %#v", soap)
	}
	if !strings.Contains(script.Actions[0].SOAP.Action, canary) {
		t.Fatal("projection mutated the stored SOAP action")
	}
}

func TestSecretBindingApplicationRejectsJavaVMTemplatesWithoutRequirements(t *testing.T) {
	script := &dsl.Script{
		Name: "Java VM template", Version: "1", Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			AdditionalVMParameters: []string{"-Dlabel={{secret.JAVA_VM_ARG}}"},
		}},
	}
	if requirements := discoverSecretBindingRequirements(maskScriptSecrets(script), 0); len(requirements) != 0 {
		t.Fatalf("Java VM template produced secret-binding requirements: %#v", requirements)
	}
	resolved, remaining, err := applySecretBindings(script, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "template_forbidden") {
		t.Fatalf("applySecretBindings = %#v, %#v, %v; want value-free template rejection", resolved, remaining, err)
	}
	if strings.Contains(err.Error(), "JAVA_VM_ARG") {
		t.Fatalf("Java VM template rejection leaked logical name: %v", err)
	}
}

func TestMaskScriptSecretsPreservesPortableVariableBindings(t *testing.T) {
	script := &dsl.Script{
		ID: "portable-binding", Name: "Portable binding", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/submit",
			Body: "aura.token={{var.corr_access_token}}&password=raw-secret&safe=yes",
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil || len(masked.Actions) != 1 {
		t.Fatalf("masked script = %#v", masked)
	}
	body := masked.Actions[0].Body
	if !strings.Contains(body, "aura.token={{var.corr_access_token}}") {
		t.Fatalf("portable variable binding was lost: %q", body)
	}
	if strings.Contains(body, "raw-secret") || !strings.Contains(body, maskedScriptSecret) {
		t.Fatalf("inline password was not redacted: %q", body)
	}
	if script.Actions[0].Body != "aura.token={{var.corr_access_token}}&password=raw-secret&safe=yes" {
		t.Fatalf("projection mutated runtime script: %q", script.Actions[0].Body)
	}
}

func TestMaskScriptSecretsGovernsCredentialNamedRawBody(t *testing.T) {
	const canary = "raw-body-credential-never-disclose"
	script := &dsl.Script{
		ID: "raw-body", Name: "Raw credential body", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name:    "submit",
			Type:    "request",
			Method:  http.MethodPost,
			Target:  "/submit",
			Headers: map[string]string{"Content-Type": "text/plain"},
			Body:    "password = " + canary + "\nordinary prose",
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil || len(masked.Actions) != 1 {
		t.Fatalf("masked script = %#v", masked)
	}
	if strings.Contains(masked.Actions[0].Body, canary) ||
		masked.Actions[0].Body != maskedScriptSecret {
		t.Fatalf("masked raw body = %q", masked.Actions[0].Body)
	}
	if !strings.Contains(script.Actions[0].Body, canary) {
		t.Fatal("projection mutated the stored script")
	}
}

func TestMaskScriptSecretsKeepsMalformedCredentialBodiesAsGatedDrafts(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		encoding    string
	}{
		{name: "json", contentType: "application/json", body: `{"password":"tiny"`},
		{name: "xml", contentType: "application/xml", body: `<root><password>tiny`},
		{name: "form", contentType: "application/x-www-form-urlencoded", body: `password=%ZZ`},
		{
			name:        "multipart",
			contentType: "multipart/form-data; boundary=boundary",
			body:        "--boundary\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\ntiny",
		},
		{name: "graphql", contentType: "application/graphql", body: `mutation Login { login(password: "tiny")`},
		{
			name:        "base64 json",
			contentType: "application/json",
			body:        base64.StdEncoding.EncodeToString([]byte(`{"password":"tiny"`)),
			encoding:    "base64",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{
				Name: "malformed " + test.name, Version: "1", Protocol: "http",
				Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
				Actions: []dsl.Action{{
					Name: "Submit", Type: "request", Method: http.MethodPost, Target: "/login",
					Headers:      map[string]string{"Content-Type": test.contentType},
					Body:         test.body,
					BodyEncoding: test.encoding,
				}},
			}
			projected := maskScriptSecrets(script)
			if projected == nil {
				t.Fatal("maskScriptSecrets returned nil")
			}
			gotBody := projected.Actions[0].Body
			if test.encoding == "base64" {
				decoded, err := base64.StdEncoding.DecodeString(gotBody)
				if err != nil {
					t.Fatalf("decode projected body: %v", err)
				}
				gotBody = string(decoded)
			}
			if gotBody != maskedScriptSecret {
				t.Fatalf("projected malformed body = %q, want whole-body sentinel", gotBody)
			}
			if !scriptHasExecutionBlockerCode(projected, "import_review_required") {
				t.Fatalf("projected malformed body omitted review gate: %#v", projected.Actions[0].Metadata)
			}
			if issues := dsl.ValidateScriptIssues(projected); len(issues) != 0 {
				t.Fatalf("projected malformed body validation issues = %#v", issues)
			}
			if script.Actions[0].Body != test.body || script.Actions[0].Metadata != nil {
				t.Fatalf("projection mutated stored source action: %#v", script.Actions[0])
			}
		})
	}
}

func TestMaskScriptSecretsGeneratesRedactedXMLMessageIDs(t *testing.T) {
	const recordedMessageID = "AbCdEf0123456789GhIjKlMnOpQrStUv"
	script := &dsl.Script{
		ID: "soap-message-id", Name: "SOAP message", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/soap",
			Body: `<Envelope><Header><mes:messageId>` + recordedMessageID + `</mes:messageId>` +
				`<mes:password>source-password</mes:password></Header></Envelope>`,
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil || len(masked.Actions) != 1 {
		t.Fatalf("masked script = %#v", masked)
	}
	body := masked.Actions[0].Body
	if strings.Contains(body, recordedMessageID) || !strings.Contains(body, `<mes:messageId>{{fn.uuid()}}</mes:messageId>`) {
		t.Fatalf("message id was not replaced with a generated UUID: %q", body)
	}
	if strings.Contains(body, "source-password") || !strings.Contains(body, `<mes:password>`+maskedScriptSecret+`</mes:password>`) {
		t.Fatalf("password was not redacted: %q", body)
	}
	requirements := discoverSecretBindingRequirements(masked, 0)
	if len(requirements) != 1 || !strings.Contains(requirements[0].Replacement, `<mes:password>{{secret.`) {
		t.Fatalf("secret requirements = %#v, want only the password", requirements)
	}
}

func TestMaskScriptSecretsPreservesGovernedXMLSecretReferences(t *testing.T) {
	script := &dsl.Script{
		ID: "soap-secret-refs", Name: "SOAP secret references", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/soap",
			Body: `<Envelope password="{{ secret.envelope_password }}"><mes:password>{{secret.soap_password}}</mes:password>` +
				`<mes:token>source-token</mes:token></Envelope>`,
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil || len(masked.Actions) != 1 {
		t.Fatalf("masked script = %#v", masked)
	}
	body := masked.Actions[0].Body
	for _, reference := range []string{"{{ secret.envelope_password }}", "{{secret.soap_password}}"} {
		if !strings.Contains(body, reference) {
			t.Fatalf("governed XML reference %q was lost: %q", reference, body)
		}
	}
	if strings.Contains(body, "source-token") || !strings.Contains(body, `<mes:token>`+maskedScriptSecret+`</mes:token>`) {
		t.Fatalf("inline XML credential was not redacted: %q", body)
	}
	if strings.Contains(script.Actions[0].Body, maskedScriptSecret) {
		t.Fatalf("projection mutated runtime script: %q", script.Actions[0].Body)
	}
}

func TestMaskScriptSecretsScrubsSiblingAttributeOnPortableXMLElement(t *testing.T) {
	script := &dsl.Script{
		ID: "xml-sibling-secret", Name: "XML sibling secret", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/soap",
			Body: `<Envelope password="plaintext-password"><value>{{param.safe_value}}</value></Envelope>`,
		}},
	}

	masked := maskScriptSecrets(script)
	body := masked.Actions[0].Body
	if strings.Contains(body, "plaintext-password") {
		t.Fatalf("whole-element template protection leaked sibling attribute: %q", body)
	}
	if !strings.Contains(body, `password="`+maskedScriptSecret+`"`) ||
		!strings.Contains(body, `<value>{{param.safe_value}}</value>`) {
		t.Fatalf("projection did not scrub sibling while preserving portable template: %q", body)
	}
}

func TestMaskScriptSecretsPreservesNativeSourceAndMessageIdentifiers(t *testing.T) {
	const findingID = "bruno-native-0123456789abcdef01234567"
	script := &dsl.Script{
		ID: "native-identifiers", Name: "Native identifiers", Version: "1", Protocol: "http",
		GeneratedBy: brunoNativeImporterIdentity,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "bruno",
			Preserved: map[string]any{
				"effective_auth": "basic",
				"diagnostic_ids": []string{findingID},
				"finding_ids":    []string{findingID},
				"effective_fields": map[string]any{
					"auth_mode": "basic",
				},
			},
		}},
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/soap",
			Headers: map[string]string{
				"idpctx-message-id": "02d90c91f908a3d31a80a7f48f805508",
				"idpctx-session-id": "source-session-credential",
			},
			HeaderList: dsl.HeaderList{
				{Name: "idpctx-message-id", Value: "02d90c91f908a3d31a80a7f48f805508"},
				{Name: "idpctx-session-id", Value: "source-session-credential"},
			},
			Metadata: map[string]any{
				"source_path":    "CTA/InquireSubscriptionEligibility_v120.yml",
				"source_step_id": "0a3d7bb4aa0ff8b2b1512082a253b7af",
				"source_profile": "bruno_native",
			},
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil || len(masked.Actions) != 1 {
		t.Fatalf("masked script = %#v", masked)
	}
	action := masked.Actions[0]
	if got := action.Headers["idpctx-message-id"]; got != "02d90c91f908a3d31a80a7f48f805508" {
		t.Fatalf("message identifier = %q", got)
	}
	if got := action.HeaderList[0].Value; got != "02d90c91f908a3d31a80a7f48f805508" {
		t.Fatalf("message identifier list value = %q", got)
	}
	if got := action.Metadata["source_path"]; got != "CTA/InquireSubscriptionEligibility_v120.yml" {
		t.Fatalf("source path = %#v", got)
	}
	if got := action.Metadata["source_step_id"]; got != "0a3d7bb4aa0ff8b2b1512082a253b7af" {
		t.Fatalf("source step id = %#v", got)
	}
	if got := action.Headers["idpctx-session-id"]; got != maskedScriptSecret {
		t.Fatalf("session credential = %q, want redaction", got)
	}
	preserved := masked.Metadata.Import.Preserved
	if preserved["effective_auth"] != "basic" {
		t.Fatalf("effective auth = %#v", preserved["effective_auth"])
	}
	if got, ok := preserved["diagnostic_ids"].([]string); !ok || len(got) != 1 || got[0] != findingID {
		t.Fatalf("diagnostic ids = %#v", preserved["diagnostic_ids"])
	}
	fields, ok := preserved["effective_fields"].(map[string]any)
	if !ok || fields["auth_mode"] != "basic" {
		t.Fatalf("effective fields = %#v", preserved["effective_fields"])
	}
	if unresolved := discoverSecretBindingRequirements(masked, 0); len(unresolved) != 2 {
		t.Fatalf("unresolved requirements = %#v, want only the duplicated session credential surfaces", unresolved)
	}
}

func TestMaskScriptSecretsPreservesOAuthTokenEndpointLocator(t *testing.T) {
	script := &dsl.Script{
		ID: "oauth-locator", Name: "OAuth locator", Version: "1", Protocol: "http",
		Init: []dsl.Action{{
			Name: "token", Type: "http_oauth2_token",
			Params: map[string]any{
				"token_url":     "https://login.example.test/oauth/token",
				"client_secret": "short-secret",
			},
		}},
	}
	masked := maskScriptSecrets(script)
	if got := masked.Init[0].Params["token_url"]; got != "https://login.example.test/oauth/token" {
		t.Fatalf("token_url locator = %#v", got)
	}
	if got := masked.Init[0].Params["client_secret"]; got != maskedScriptSecret {
		t.Fatalf("client_secret = %#v, want redaction", got)
	}
}

func TestMaskScriptSecretsRejectsTemplateShapedCredentialBypasses(t *testing.T) {
	script := &dsl.Script{
		ID: "template-bypass", Name: "Template bypass", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/submit",
			Body: "password={{raw-secret}}&token={{fn.not_a_builtin(raw-token)}}",
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil || len(masked.Actions) != 1 {
		t.Fatalf("masked script = %#v", masked)
	}
	body := masked.Actions[0].Body
	if strings.Contains(body, "raw-secret") || strings.Contains(body, "raw-token") {
		t.Fatalf("template-shaped inline credentials bypassed projection: %q", body)
	}
}

func TestReconcileMaskedScriptSecretsRestoresOnlyExactPriorProjection(t *testing.T) {
	prior := secretBearingScript("reconcile", "project-a")
	incoming := maskScriptSecrets(prior)
	incoming.Name = "renamed"
	if err := reconcileMaskedScriptSecrets(incoming, prior); err != nil {
		t.Fatalf("reconcile exact projection: %v", err)
	}
	if incoming.Config.HTTP.Auth.Password != prior.Config.HTTP.Auth.Password || incoming.Actions[0].Control.Steps[0].Body != prior.Actions[0].Control.Steps[0].Body {
		t.Fatal("reconciliation did not restore prior credentials")
	}
	if incoming.Name != "renamed" {
		t.Fatal("reconciliation discarded a non-secret edit")
	}

	orphan := secretBearingScript("orphan", "project-a")
	orphan.Config.HTTP.Auth.Password = maskedScriptSecret
	if err := reconcileMaskedScriptSecrets(orphan, nil); err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("orphan sentinel error = %v", err)
	}
	changed := maskScriptSecrets(prior)
	changed.Actions[0].Control.Steps[0].Body += " changed"
	if err := reconcileMaskedScriptSecrets(changed, prior); err == nil {
		t.Fatal("partially edited masked credential body was accepted")
	}
}

func TestReconcileMaskedScriptSecretsPreservesSentinelLikeImportedCredentials(t *testing.T) {
	credentialValue := "prefix" + maskedScriptSecret + "suffix"
	prior := secretBearingScript("sentinel-like", "project-a")
	prior.Config.HTTP.Auth.Password = credentialValue
	ensureCredentialPreservingImportMetadata(prior, "fixture")
	incoming := maskScriptSecrets(prior)

	if err := reconcileMaskedScriptSecrets(incoming, prior); err != nil {
		t.Fatalf("reconcile sentinel-like credential: %v", err)
	}
	if got := incoming.Config.HTTP.Auth.Password; got != credentialValue {
		t.Fatalf("reconciled password = %q, want exact prior value", got)
	}

	trustedCreate := secretBearingScript("trusted-sentinel-like", "project-a")
	trustedCreate.Config.HTTP.Auth.Password = credentialValue
	if err := reconcileMaskedScriptSecretsWithTrust(trustedCreate, nil, true); err != nil {
		t.Fatalf("trusted import create rejected exact credential: %v", err)
	}
	if got := trustedCreate.Config.HTTP.Auth.Password; got != credentialValue {
		t.Fatalf("trusted password = %q, want exact imported value", got)
	}

	trustedReplacePrior := secretBearingScript("trusted-replace-prior", "project-a")
	trustedReplacePrior.Config.HTTP.Auth.Password = "old-password"
	trustedReplace := secretBearingScript("trusted-replace", "project-a")
	trustedReplace.Config.HTTP.Auth.Password = maskedScriptSecret
	if err := reconcileMaskedScriptSecretsWithTrust(
		trustedReplace,
		trustedReplacePrior,
		true,
	); err != nil {
		t.Fatalf("trusted import replace rejected exact sentinel credential: %v", err)
	}
	if got := trustedReplace.Config.HTTP.Auth.Password; got != maskedScriptSecret {
		t.Fatalf("trusted replace password = %q, want exact imported sentinel", got)
	}

	orphan := secretBearingScript("ordinary-sentinel-like", "project-a")
	orphan.Config.HTTP.Auth.Password = credentialValue
	if err := reconcileMaskedScriptSecrets(orphan, nil); err == nil ||
		!strings.Contains(err.Error(), "orphan") {
		t.Fatalf("ordinary orphan sentinel-like credential error = %v", err)
	}
}

func TestPrepareTransientExecutableScriptPreservesSentinelLikeImportedCredential(t *testing.T) {
	imported := &dsl.Script{
		ID: "trusted-replay-sentinel", Name: "Trusted replay sentinel", Version: "1", Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type:     "basic",
				Username: "operator",
				Password: maskedScriptSecret,
			}},
		},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
		}},
	}
	ensureCredentialPreservingImportMetadata(imported, "fixture")

	response := httptest.NewRecorder()
	prepared, ok := prepareTransientExecutableScript(response, imported, true)
	if !ok {
		t.Fatalf("trusted replay preparation status = %d body=%s", response.Code, response.Body.String())
	}
	if got := prepared.Config.HTTP.Auth.Password; got != maskedScriptSecret {
		t.Fatalf("trusted replay password = %q, want exact imported value", got)
	}
}

func TestReconcileMaskedScriptSecretsFailsClosedWhenCredentialActionsReorder(t *testing.T) {
	prior := &dsl.Script{
		ID: "reordered-credentials", Name: "Credential actions", Version: "1", Protocol: "http",
		Actions: []dsl.Action{
			{Name: "first", Type: "request", Target: "/first", Headers: map[string]string{"Authorization": "Bearer first-token"}},
			{Name: "second", Type: "request", Target: "/second", Headers: map[string]string{"Authorization": "Bearer second-token"}},
		},
	}

	t.Run("unchanged array restores each prior credential", func(t *testing.T) {
		incoming := maskScriptSecrets(prior)
		if err := reconcileMaskedScriptSecrets(incoming, prior); err != nil {
			t.Fatalf("reconcile unchanged array: %v", err)
		}
		if got := incoming.Actions[0].Headers["Authorization"]; got != "Bearer first-token" {
			t.Fatalf("first Authorization = %q, want prior credential", got)
		}
		if got := incoming.Actions[1].Headers["Authorization"]; got != "Bearer second-token" {
			t.Fatalf("second Authorization = %q, want prior credential", got)
		}
	})

	t.Run("reordered array cannot positionally swap credentials", func(t *testing.T) {
		incoming := maskScriptSecrets(prior)
		incoming.Actions[0], incoming.Actions[1] = incoming.Actions[1], incoming.Actions[0]

		err := reconcileMaskedScriptSecrets(incoming, prior)
		if err == nil || !strings.Contains(err.Error(), "changed while containing redacted credentials") {
			t.Fatalf("reordered credential action error = %v", err)
		}
		for index, action := range incoming.Actions {
			got := action.Headers["Authorization"]
			if !strings.Contains(got, maskedScriptSecret) {
				t.Fatalf("action %d Authorization = %q, raw prior credential was restored", index, got)
			}
		}
	})

	t.Run("same-route reorder cannot positionally swap credentials", func(t *testing.T) {
		sameRoutePrior := &dsl.Script{
			ID: "same-route-credentials", Name: "Same route credentials", Version: "1", Protocol: "http",
			Actions: []dsl.Action{
				{
					Name: "Admin login", Type: "request", Method: http.MethodPost, Target: "/login",
					Headers:    map[string]string{"Authorization": "Bearer admin-token"},
					Extractors: []dsl.Extractor{{Name: "admin-session", Type: "jsonpath", Expression: "$.session"}},
				},
				{
					Name: "User login", Type: "request", Method: http.MethodPost, Target: "/login",
					Headers:    map[string]string{"Authorization": "Bearer user-token"},
					Extractors: []dsl.Extractor{{Name: "user-session", Type: "jsonpath", Expression: "$.session"}},
				},
			},
		}
		incoming := maskScriptSecrets(sameRoutePrior)
		incoming.Actions[0], incoming.Actions[1] = incoming.Actions[1], incoming.Actions[0]

		err := reconcileMaskedScriptSecrets(incoming, sameRoutePrior)
		if err == nil || !strings.Contains(err.Error(), "changed while containing redacted credentials") {
			t.Fatalf("same-route reordered credential action error = %v", err)
		}
		for index, action := range incoming.Actions {
			got := action.Headers["Authorization"]
			if !strings.Contains(got, maskedScriptSecret) {
				t.Fatalf("action %d Authorization = %q, raw prior credential was restored", index, got)
			}
		}
	})
}

// Viewer READS are a disclosure projection and still mask. A download or a
// private export is the runnable artifact — it has to be re-importable into a
// script that executes — so those carry the executable definition and only the
// filename stays a disclosure-safe label.
func TestStoredScriptViewerReadsMaskWhileDownloadAndExportStayRunnable(t *testing.T) {
	exportDir := t.TempDir()
	t.Setenv("APEXLOAD_EXPORT_DIR", exportDir)
	server := NewServer(ServerConfig{})
	stored := seedLegacySecretScript(server, secretBearingScript("viewer-secret", "project-a"))

	assertMasked := func(label string, response *httptest.ResponseRecorder) {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", label, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "global-password") || strings.Contains(response.Body.String(), "action-api-key") {
			t.Fatalf("%s leaked script secrets: %s", label, response.Body.String())
		}
	}
	assertRunnable := func(label string, data []byte) {
		t.Helper()
		if !strings.Contains(string(data), "global-password") ||
			!strings.Contains(string(data), "action-api-key") {
			t.Fatalf("%s dropped executable credentials and cannot be re-imported: %s", label, data)
		}
		var envelope struct {
			Script *dsl.Script `json:"script"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("%s is not a script envelope: %v", label, err)
		}
		if envelope.Script == nil {
			t.Fatalf("%s omitted the script: %s", label, data)
		}
		if got := envelope.Script.Config.HTTP.Auth.Password; got != "global-password" {
			t.Fatalf("%s auth password = %q, want the executable value", label, got)
		}
	}

	list := httptest.NewRecorder()
	server.handleListScripts(list, httptest.NewRequest(http.MethodGet, "/api/v1/scripts?project_id=project-a", nil))
	assertMasked("list", list)
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+stored.ID, nil)
	getReq.SetPathValue("id", stored.ID)
	get := httptest.NewRecorder()
	server.handleGetScript(get, getReq)
	assertMasked("get", get)
	downloadReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+stored.ID+"/download", nil)
	downloadReq.SetPathValue("id", stored.ID)
	download := httptest.NewRecorder()
	server.handleDownloadScript(download, downloadReq)
	if download.Code != http.StatusOK {
		t.Fatalf("download status = %d body=%s", download.Code, download.Body.String())
	}
	assertRunnable("download", download.Body.Bytes())
	if got := download.Header().Get("Content-Disposition"); strings.Contains(got, "global-password") {
		t.Fatalf("download filename leaked a credential: %q", got)
	}

	exportReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/export", strings.NewReader(`{"filename":"private.json"}`))
	exportReq.SetPathValue("id", stored.ID)
	exported := httptest.NewRecorder()
	server.handleExportScript(exported, exportReq)
	// The export RESPONSE reports only a path and size, so it stays masked.
	assertMasked("export response", exported)
	path := filepath.Join(exportDir, "private.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertRunnable("private export", data)

	exportBadReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/export", strings.NewReader(`{`))
	exportBadReq.SetPathValue("id", stored.ID)
	bad := httptest.NewRecorder()
	server.handleExportScript(bad, exportBadReq)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad export status = %d body=%s", bad.Code, bad.Body.String())
	}
	var badBody map[string]string
	if err := json.Unmarshal(bad.Body.Bytes(), &badBody); err != nil {
		t.Fatalf("bad export response was not JSON: %v", err)
	}
	if !strings.Contains(badBody["error"], "invalid request body") {
		t.Fatalf("bad export response = %#v, want invalid request body", badBody)
	}

	_, err = os.Stat(filepath.Join(exportDir, "private"))
	if !os.IsNotExist(err) {
		t.Fatalf("bad export should not create an export file without filename field")
	}
}

func TestStoredScriptResponseMasksDecodedBase64JSONWithoutMutatingStoredWire(t *testing.T) {
	const canary = "stored-base64-response-canary-8e45"
	const wireBody = "{\n  \"password\": \"" + canary + "\",\n  \"count\": 1.00e+2,\n  \"ok\": true\n}"
	encodedBody := base64.StdEncoding.EncodeToString([]byte(wireBody))
	server := NewServer(ServerConfig{})
	stored := seedLegacySecretScript(server, &dsl.Script{
		ID:        "base64-response-projection",
		Name:      "Base64 response projection",
		Version:   "1",
		Protocol:  "http",
		ProjectID: "project-a",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/records",
			Headers:      map[string]string{"Content-Type": "application/json"},
			Body:         encodedBody,
			BodyEncoding: "base64",
		}},
	})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+stored.ID, nil)
	getReq.SetPathValue("id", stored.ID)
	response := httptest.NewRecorder()
	server.handleGetScript(response, getReq)
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", response.Code, response.Body.String())
	}
	var projected StoredScript
	if err := json.Unmarshal(response.Body.Bytes(), &projected); err != nil {
		t.Fatal(err)
	}
	action := projected.Script.Actions[0]
	if action.BodyEncoding != "base64" {
		t.Fatalf("response body_encoding = %q, want base64", action.BodyEncoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(action.Body))
	if err != nil {
		t.Fatal(err)
	}
	wantWire := strings.Replace(wireBody, canary, maskedScriptSecret, 1)
	if string(decoded) != wantWire {
		t.Fatalf("masked wire bytes changed outside credential occurrence:\n got: %q\nwant: %q", decoded, wantWire)
	}
	current, ok := server.runner.GetScript(stored.ID)
	if !ok || current.Script.Actions[0].Body != encodedBody {
		t.Fatalf("response projection mutated stored base64 body: %#v", current)
	}
	storedWire, err := base64.StdEncoding.DecodeString(current.Script.Actions[0].Body)
	if err != nil || string(storedWire) != wireBody {
		t.Fatalf("stored wire body = %q, %v; want original", storedWire, err)
	}
}

func TestImportResponseMasksEmbeddedBase64BodyWithoutCorruptingWire(t *testing.T) {
	const canary = "import-base64-response-canary-a81f"
	const wireBody = "{\n  \"password\": \"" + canary + "\",\n  \"count\": 1.00e+2,\n  \"ok\": true\n}"
	encodedBody := base64.StdEncoding.EncodeToString([]byte(wireBody))
	script := &dsl.Script{
		ID:        "base64-import-response",
		Name:      "Base64 import response",
		Version:   "1",
		Protocol:  "http",
		ProjectID: "project-a",
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "submit", Type: "request", Method: http.MethodPost, Target: "/records",
			Headers:      map[string]string{"Content-Type": "application/json"},
			Body:         encodedBody,
			BodyEncoding: "base64",
		}},
	}
	response := newScriptImportResponse(scriptImportResponseOptions{
		Script: script,
		Result: struct {
			Script *dsl.Script `json:"script"`
		}{Script: script},
	})
	scripts, ok := response["scripts"].([]any)
	if !ok || len(scripts) != 1 {
		t.Fatalf("response scripts = %#v, want one projected script", response["scripts"])
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("response result = %#v, want object", response["result"])
	}
	resultScript, ok := result["script"]
	if !ok {
		t.Fatalf("response result has no embedded script: %#v", result)
	}

	for label, value := range map[string]any{
		"script":        response["script"],
		"scripts":       scripts[0],
		"result.script": resultScript,
	} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s marshal: %v", label, err)
		}
		var projected dsl.Script
		if err := json.Unmarshal(payload, &projected); err != nil {
			t.Fatalf("%s unmarshal: %v", label, err)
		}
		action := projected.Actions[0]
		if action.BodyEncoding != "base64" {
			t.Fatalf("%s body_encoding = %q, want base64", label, action.BodyEncoding)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(action.Body))
		if err != nil {
			t.Fatalf("%s body is not valid base64: %v (%q)", label, err, action.Body)
		}
		wantWire := wireBody
		if label == "result.script" {
			wantWire = strings.Replace(wireBody, canary, maskedScriptSecret, 1)
		}
		if string(decoded) != wantWire {
			t.Fatalf("%s wire bytes changed outside the expected disclosure boundary:\n got: %q\nwant: %q", label, decoded, wantWire)
		}
	}
	if script.Actions[0].Body != encodedBody {
		t.Fatalf("import response projection mutated original base64 body: %q", script.Actions[0].Body)
	}
}

func TestMaskScriptSecretsProjectsDecodedBase64HTTPSequenceRequest(t *testing.T) {
	const canary = "sequence-base64-canary-d250"
	const wireBody = `{"password":"` + canary + `","safe":"visible"}`
	encodedBody := base64.StdEncoding.EncodeToString([]byte(wireBody))
	script := &dsl.Script{
		ID: "base64-sequence", Name: "Base64 sequence", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "sequence", Type: "http_sequence", Target: "/sequence",
			Params: map[string]any{"requests": []any{map[string]any{
				"name": "nested", "method": http.MethodPost, "target": "/login",
				"headers":       map[string]any{"Content-Type": "application/json"},
				"body":          encodedBody,
				"body_encoding": "base64",
			}}},
		}},
	}

	masked := maskScriptSecrets(script)
	if masked == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	requests, _ := masked.Actions[0].Params["requests"].([]any)
	if len(requests) != 1 {
		t.Fatalf("masked sequence requests = %#v", masked.Actions[0].Params["requests"])
	}
	request, _ := requests[0].(map[string]any)
	projectedBody, _ := request["body"].(string)
	decoded, err := base64.StdEncoding.DecodeString(projectedBody)
	if err != nil {
		t.Fatal(err)
	}
	wantWire := strings.Replace(wireBody, canary, maskedScriptSecret, 1)
	if string(decoded) != wantWire || request["body_encoding"] != "base64" {
		t.Fatalf("masked nested wire = %q (%#v), want %q base64", decoded, request["body_encoding"], wantWire)
	}
	originalRequests, _ := script.Actions[0].Params["requests"].([]any)
	originalRequest, _ := originalRequests[0].(map[string]any)
	if originalRequest["body"] != encodedBody {
		t.Fatalf("projection mutated original nested body: %#v", originalRequest["body"])
	}
}

func TestPortableHashesStayStableWhileLibraryAndGitHubPayloadsStayRunnable(t *testing.T) {
	server := NewServer(ServerConfig{})
	first := seedLegacySecretScript(server, secretBearingScript("portable", "project-a"))
	second := secretBearingScript("portable-2", "project-a")
	second.Config.HTTP.Auth.Password = "different-password"
	if hashLibraryScript(first.Script) != hashLibraryScript(second) {
		t.Fatal("portable script hash changed when only an inline secret changed")
	}
	item, err := server.libraryScriptBundleItem(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A library bundle crosses an instance boundary and the receiver imports it,
	// so like a download/export it must carry the runnable script. Masking here
	// meant an instance-to-instance transfer silently produced a script that
	// could not execute.
	itemJSON, _ := json.Marshal(item)
	if !strings.Contains(string(itemJSON), "global-password") {
		t.Fatalf("library item dropped executable credentials: %s", itemJSON)
	}
	// The hash is still the masked, portable one (hashLibraryScript masks
	// internally), so shipping the executable body does not change link identity.
	if item.ContentHash != hashLibraryScript(second) {
		t.Fatalf("library item hash = %q, want the portable masked hash %q", item.ContentHash, hashLibraryScript(second))
	}
	// The GitHub envelope is the same artifact as a download/export: it must be
	// importable back into a runnable script, and drift detection hashes these
	// exact bytes against the remote copy.
	githubPayload, _, err := githubScriptEnvelope(first.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(githubPayload), "global-password") {
		t.Fatalf("github envelope dropped executable credentials: %s", githubPayload)
	}
}

func TestGitHubPushSendsExecutablePayload(t *testing.T) {
	stub := newGitHubWriteStub(t)
	server := newGitHubTestServer(t, stub.server.URL)
	seedGitHubConnection(t, server)
	legacy := secretBearingScript("github-secret", "team-a")
	if err := server.store.SaveScript(legacy); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}
	stored, ok := server.runner.GetScript(legacy.ID)
	if !ok {
		t.Fatal("legacy GitHub script was not loaded")
	}
	body, _ := json.Marshal(githubPushRequest{Owner: "octocat", Repo: "scripts", Branch: "main", Path: "apexload/scripts/x.json", Strategy: "commit"})
	postGitHubJSON(t, server, "/api/v1/scripts/"+stored.ID+"/github/push", body, http.StatusOK)
	stub.mu.Lock()
	encoded, _ := stub.lastPut["content"].(string)
	stub.mu.Unlock()
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "global-password") {
		t.Fatalf("github PUT dropped executable credentials and is not re-importable: %s", payload)
	}
	var envelope struct {
		Script *dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("github PUT body is not a script envelope: %v", err)
	}
	if envelope.Script == nil || envelope.Script.Config.HTTP.Auth.Password != "global-password" {
		t.Fatalf("github PUT body is not the executable script: %s", payload)
	}
}

func TestLibraryBuildRejectsEveryCrossProjectAssetAndDependency(t *testing.T) {
	server := NewServer(ServerConfig{})
	local := server.runner.StoreScript(libraryTestScript("local-project-script", "Local", "project-a", "/local"))
	foreign := server.runner.StoreScript(libraryTestScript("foreign-project-script", "Foreign", "project-b", "/foreign"))
	if _, err := server.buildLibraryBundle("script", foreign.ID, "project-a", false); err == nil {
		t.Fatal("cross-project script export was accepted")
	}
	server.runner.mu.Lock()
	server.runner.scenarios["corrupt-scenario"] = &StoredScenario{
		ID: "corrupt-scenario", Name: "Corrupt", ProjectID: "project-a", Duration: "1m",
		Scripts: []ScenarioScriptRef{{ScriptID: local.ID, VUsers: 1}, {ScriptID: foreign.ID, VUsers: 1}},
	}
	server.runner.tests["foreign-result"] = &ManagedTest{ID: "foreign-result", Name: "Foreign result", ProjectID: "project-b", Status: "COMPLETE"}
	server.runner.mu.Unlock()
	if _, err := server.buildLibraryBundle("scenario", "corrupt-scenario", "project-a", true); err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatalf("cross-project dependency error = %v", err)
	}
	if _, err := server.buildLibraryBundle("result", "foreign-result", "project-a", false); err == nil {
		t.Fatal("cross-project result export was accepted")
	}
	_ = local
}

func TestLibraryImportPreflightPreventsPartialWrites(t *testing.T) {
	server := NewServer(ServerConfig{})
	valid := libraryTestScript("preflight-valid", "Valid", "source", "/valid")
	invalid := libraryTestScript("preflight-invalid", "Invalid", "source", "/invalid")
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "remote", ProjectID: "source"}, Items: []LibraryBundleItem{
		{Kind: "script", SourceInstanceID: "remote", SourceItemID: valid.ID, ProjectID: "source", Script: valid, ContentHash: hashLibraryScript(valid)},
		{Kind: "script", SourceInstanceID: "remote", SourceItemID: invalid.ID, ProjectID: "source", Script: invalid, ContentHash: "sha256:tampered"},
	}}
	response, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: "destination", Bundle: bundle}, "remote-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Conflicts) == 0 || len(response.Items) != 0 {
		t.Fatalf("preflight response = %#v", response)
	}
	validItem := bundle.Items[0]
	localID, err := deterministicLibraryImportID("remote-id", "destination", validItem)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := server.runner.GetScript(localID); ok {
		t.Fatal("valid prefix item was persisted before a later conflict")
	}
}

func TestLibraryCatalogPaginationIsBoundedAndStable(t *testing.T) {
	server := NewServer(ServerConfig{})
	for i := 19; i >= 0; i-- {
		id := "catalog-" + string(rune('a'+i))
		server.runner.StoreScript(libraryTestScript(id, id, "project-a", "/"+id))
	}
	page, total := server.libraryCatalogPage("project-a", "script", 5, 4)
	if total != 20 || len(page) != 4 {
		t.Fatalf("catalog total/page = %d/%d", total, len(page))
	}
	for i := 1; i < len(page); i++ {
		if page[i-1].Name >= page[i].Name {
			t.Fatalf("catalog page is not stable: %#v", page)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/library/catalog?offset=10001", nil)
	response := httptest.NewRecorder()
	server.handleLibraryCatalog(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized offset status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestLibraryTransferResolverRejectsForgedProjectForPushAndDiff(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "resolver.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "project-b", Name: "Project B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	stored, err := server.runner.StoreScriptDurable(libraryTestScript("resolver-project", "Resolver", "project-b", "/"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/library/export", "/api/v1/library/remotes/r/push", "/api/v1/library/remotes/r/diff"} {
		requestBody, _ := json.Marshal(LibraryTransferRequest{Kind: "script", ID: stored.ID, LocalID: stored.ID, ProjectID: "project-a"})
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(requestBody))
		if projectID, status, _ := resolveLibraryTransferProjectID(req, store); projectID != "" || status != http.StatusBadRequest {
			t.Fatalf("%s resolver = %q/%d, want project mismatch", path, projectID, status)
		}
	}
}

// End-to-end guard for the sentinel-free projection case. scrubScriptText
// rewrites a redacted <messageId> into {{fn.uuid()}}, which carries NO
// [REDACTED] sentinel, so this must be driven through reconcileMaskedScriptSecrets
// (the real entry point) rather than reconcileProjectedJSON: an earlier version of
// this test called the inner helper directly and passed while the production path
// still destroyed the stored value.
func TestReconcileMaskedScriptSecretsRestoresSentinelFreeProjection(t *testing.T) {
	const rawBody = `<Envelope><Header><mes:messageId>AbCdEf0123456789GhIjKlMnOpQrStUv</mes:messageId></Header><Body><Order>123</Order></Body></Envelope>`
	stored := &dsl.Script{
		Name: "soap-order", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Submit", Type: "request", Method: "POST", Target: "/soap", Body: rawBody}},
	}

	projected := maskScriptSecrets(stored)
	projectedBody := projected.Actions[0].Body
	if strings.Contains(projectedBody, maskedScriptSecret) {
		t.Fatalf("projected body = %q, this test requires a SENTINEL-FREE projection", projectedBody)
	}
	if projectedBody == rawBody {
		t.Fatalf("projected body was not rewritten (%q); the messageId masking this guards is gone", projectedBody)
	}

	// Simulate GET -> unchanged -> PUT.
	incoming := maskScriptSecrets(stored)
	if err := reconcileMaskedScriptSecrets(incoming, stored); err != nil {
		t.Fatalf("reconcileMaskedScriptSecrets returned error: %v", err)
	}
	if incoming.Actions[0].Body != rawBody {
		t.Fatalf("reconciled body = %q, want the stored body %q restored", incoming.Actions[0].Body, rawBody)
	}
}

// A client edit must still win over the stored value.
func TestReconcileMaskedScriptSecretsKeepsClientEditOfProjectedField(t *testing.T) {
	const rawBody = `<Envelope><Header><mes:messageId>AbCdEf0123456789GhIjKlMnOpQrStUv</mes:messageId></Header><Body><Order>123</Order></Body></Envelope>`
	stored := &dsl.Script{
		Name: "soap-order", Version: "1.0.0", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Submit", Type: "request", Method: "POST", Target: "/soap", Body: rawBody}},
	}
	incoming := maskScriptSecrets(stored)
	incoming.Actions[0].Body = `<Envelope><Body><Order>999</Order></Body></Envelope>`

	if err := reconcileMaskedScriptSecrets(incoming, stored); err != nil {
		t.Fatalf("reconcileMaskedScriptSecrets returned error: %v", err)
	}
	if !strings.Contains(incoming.Actions[0].Body, "999") {
		t.Fatalf("reconciled body = %q, want the client edit preserved", incoming.Actions[0].Body)
	}
}

func TestReconcileMaskedScriptSecretsRestoresTypedSensitiveJSONProjection(t *testing.T) {
	const rawBody = `{"password":7,"account":"ordinary"}`
	stored := &dsl.Script{
		Name: "typed-login", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "Login", Type: "request", Method: "POST", Target: "/login",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    rawBody,
		}},
	}

	incoming := maskScriptSecrets(stored)
	if incoming == nil || incoming.Actions[0].Body != maskedScriptSecret ||
		incoming.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("typed body projection = %#v", incoming)
	}
	incoming.Actions[0].Name = "Renamed login"
	incomingJSON, _ := scriptAsJSONValue(incoming)
	projectedJSON, _ := scriptAsJSONValue(maskScriptSecrets(stored))
	rawJSON, _ := scriptAsJSONValue(stored)
	incomingAction := incomingJSON.(map[string]any)["actions"].([]any)[0].(map[string]any)
	projectedAction := projectedJSON.(map[string]any)["actions"].([]any)[0].(map[string]any)
	rawAction := rawJSON.(map[string]any)["actions"].([]any)[0].(map[string]any)
	if !isTransientScriptProjectionGate(incomingAction, projectedAction, rawAction) {
		t.Fatalf(
			"server projection gate was not recognized: incoming=%#v projected=%#v raw=%#v",
			incomingAction["metadata"],
			projectedAction["metadata"],
			rawAction["metadata"],
		)
	}
	if err := reconcileMaskedScriptSecrets(incoming, stored); err != nil {
		t.Fatalf("reconcileMaskedScriptSecrets returned error: %v", err)
	}
	if incoming.Actions[0].Body != rawBody {
		t.Fatalf("reconciled body = %q, want stored typed body %q", incoming.Actions[0].Body, rawBody)
	}
	if incoming.Actions[0].Name != "Renamed login" {
		t.Fatalf("non-wire edit was lost: %#v", incoming.Actions[0])
	}
	if _, exists := incoming.Actions[0].Metadata["execution_blocker"]; exists {
		t.Fatalf("response-only review gate became durable: %#v", incoming.Actions[0].Metadata)
	}
	if _, exists := incoming.Actions[0].Metadata["import_review"]; exists {
		t.Fatalf("response-only review evidence became durable: %#v", incoming.Actions[0].Metadata)
	}
}

func TestReconcileMaskedScriptSecretsAllowsGatedRedactedRuntimeData(t *testing.T) {
	script := &dsl.Script{
		Name:     "imported parameter table",
		Version:  "1.0.0",
		Protocol: "http",
		Parameters: []dsl.Parameter{{
			Name:   "ClientSecret",
			Type:   "list",
			Values: []string{maskedScriptSecret},
		}},
		DataSources: []dsl.DataSource{{
			Name:       "passwords",
			Type:       "file",
			InlineData: "Password\n[REDACTED]\n",
			Strategy:   "sequential",
		}},
		Actions: []dsl.Action{{
			Name:   "Submit",
			Type:   "request",
			Method: "POST",
			Target: "/login",
			Metadata: map[string]any{
				"execution_blocker": credentialParameterSourceRedactedBlocker,
			},
		}},
	}

	for _, path := range []string{"/parameters/0/values/0", "/data_sources/0/inline_data"} {
		if !isLegacyRedactedRuntimeSourceTraversalPath(path) {
			t.Fatalf("legacy runtime source path %q was not recognized", path)
		}
	}
	rawBeforeCreate, err := scriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	if jsonValueContainsExecutableMaskedSecretWithOptions(rawBeforeCreate, "", true) {
		t.Fatalf("gate-aware sentinel detection treated a legacy runtime source as orphan: %#v", rawBeforeCreate)
	}
	if err := reconcileMaskedScriptSecrets(script, nil); err != nil {
		t.Fatalf("create reconciliation rejected gated redacted runtime data: %v", err)
	}
	raw, err := scriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonValueContainsExecutableMaskedSecret(raw, "") {
		t.Fatal("generic sentinel detection incorrectly exempted runtime data without gate context")
	}
	if jsonValueContainsExecutableMaskedSecretWithOptions(raw, "", true) {
		t.Fatal("gate-aware sentinel detection treated redacted runtime data as an orphan")
	}
	incoming := maskScriptSecrets(script)
	if incoming == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	if err := reconcileMaskedScriptSecrets(incoming, script); err != nil {
		t.Fatalf("update reconciliation rejected gated redacted runtime data: %v", err)
	}
	if got := incoming.DataSources[0].InlineData; got != "Password\n[REDACTED]\n" {
		t.Fatalf("reconciled inline data = %q, want disclosure-safe table shape preserved", got)
	}
	if got := incoming.Parameters[0].Values; len(got) != 1 || got[0] != maskedScriptSecret {
		t.Fatalf("reconciled legacy list values = %#v, want gated sentinel preserved", got)
	}
}

func TestReconcileMaskedScriptSecretsRestoresSensitiveListParameterValues(t *testing.T) {
	const rawPassword = "preserved-list-password-canary"
	stored := &dsl.Script{
		Name:     "runtime list parameter",
		Version:  "1.0.0",
		Protocol: "http",
		Parameters: []dsl.Parameter{{
			Name:   "ClientSecret",
			Type:   "list",
			Values: []string{rawPassword},
		}},
		Actions: []dsl.Action{{
			Name: "Submit", Type: "request", Method: "POST", Target: "/login",
		}},
	}

	projected := maskScriptSecrets(stored)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	if got := projected.Parameters[0].Values; len(got) != 1 || got[0] != maskedScriptSecret {
		t.Fatalf("projected sensitive list values = %#v, want masked response", got)
	}
	if err := reconcileMaskedScriptSecrets(projected, stored); err != nil {
		t.Fatalf("reconcileMaskedScriptSecrets returned error: %v", err)
	}
	if got := projected.Parameters[0].Values; len(got) != 1 || got[0] != rawPassword {
		t.Fatalf("reconciled sensitive list values = %#v, want stored executable value", got)
	}

	directCreate := *stored
	directCreate.Parameters = append([]dsl.Parameter(nil), stored.Parameters...)
	directCreate.Parameters[0].Values = append([]string(nil), stored.Parameters[0].Values...)
	if err := reconcileMaskedScriptSecrets(&directCreate, nil); err != nil {
		t.Fatalf("direct create rejected executable sensitive list value: %v", err)
	}
	if got := directCreate.Parameters[0].Values[0]; got != rawPassword {
		t.Fatalf("direct create changed executable list value to %q", got)
	}
}

func TestMaskScriptSecretsRedactsCredentialBearingInlineDataCells(t *testing.T) {
	script := &dsl.Script{
		Name: "credential tables", Version: "1.0.0", Protocol: "http",
		DataSources: []dsl.DataSource{
			{
				Name:           "users",
				Type:           "csv",
				InlineData:     "username,password,region\nalice,tiny,west\n",
				Strategy:       "sequential",
				FirstRowHeader: true,
			},
			{
				Name:       "aliased",
				Type:       "csv",
				InlineData: "alice,short\n",
				Columns:    []string{"identity", "opaque"},
				Strategy:   "sequential",
			},
			{
				Name:           "ordinary",
				Type:           "csv",
				InlineData:     "region,status\nwest,ready\n",
				Strategy:       "sequential",
				FirstRowHeader: true,
			},
		},
		Parameters: []dsl.Parameter{{
			Name:       "ClientSecret",
			Type:       "file",
			DataSource: "aliased",
			Column:     "opaque",
		}},
		Actions: []dsl.Action{{
			Name: "Submit", Type: "request", Method: "POST", Target: "/login",
		}},
	}

	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	if got := projected.DataSources[0].InlineData; got != "username,password,region\nalice,"+maskedScriptSecret+",west\n" {
		t.Fatalf("header-classified credential table = %q", got)
	}
	if got := projected.DataSources[1].InlineData; got != "alice,"+maskedScriptSecret+"\n" {
		t.Fatalf("parameter-classified credential table = %q", got)
	}
	if got := projected.DataSources[2].InlineData; got != script.DataSources[2].InlineData {
		t.Fatalf("ordinary table changed from %q to %q", script.DataSources[2].InlineData, got)
	}
	if got := script.DataSources[0].InlineData; got != "username,password,region\nalice,tiny,west\n" {
		t.Fatalf("projection mutated runtime table: %q", got)
	}
}

func TestReconcileMaskedScriptSecretsRejectsUngatedRedactedRuntimeData(t *testing.T) {
	script := &dsl.Script{
		Name:     "unsafe parameter table",
		Version:  "1.0.0",
		Protocol: "http",
		DataSources: []dsl.DataSource{{
			Name:       "passwords",
			Type:       "file",
			InlineData: "Password\n[REDACTED]\n",
			Strategy:   "sequential",
		}},
		Actions: []dsl.Action{{
			Name:   "Submit",
			Type:   "request",
			Method: "POST",
			Target: "/login",
		}},
	}

	err := reconcileMaskedScriptSecrets(script, nil)
	if err == nil || !strings.Contains(err.Error(), credentialParameterSourceRedactedBlocker) {
		t.Fatalf("ungated redacted runtime data error = %v, want required execution gate", err)
	}
}

func TestReconcileMaskedScriptSecretsRestoresProjectedRuntimeDataWithoutGate(t *testing.T) {
	const rawTable = "Password\npassword=do-not-disclose-1234567890\n"
	prior := &dsl.Script{
		Name:     "resolved parameter table",
		Version:  "1.0.0",
		Protocol: "http",
		DataSources: []dsl.DataSource{{
			Name:       "passwords",
			Type:       "file",
			InlineData: rawTable,
			Strategy:   "sequential",
		}},
		Actions: []dsl.Action{{
			Name: "Submit", Type: "request", Method: "POST", Target: "/login",
		}},
	}
	incoming := maskScriptSecrets(prior)
	if incoming == nil || !strings.Contains(incoming.DataSources[0].InlineData, maskedScriptSecret) {
		t.Fatalf("projected runtime table = %#v, want disclosure-safe sentinel", incoming)
	}
	if err := reconcileMaskedScriptSecrets(incoming, prior); err != nil {
		t.Fatalf("update reconciliation rejected a projected stored runtime table: %v", err)
	}
	if got := incoming.DataSources[0].InlineData; got != rawTable {
		t.Fatalf("reconciled inline data = %q, want stored value restored", got)
	}
}

func TestReconcileMaskedScriptSecretsDoesNotOverwriteResolvedTableWhenAnotherTableRemainsGated(t *testing.T) {
	const rawTable = "Password\npassword=do-not-disclose-1234567890\n"
	prior := &dsl.Script{
		Name:     "mixed parameter tables",
		Version:  "1.0.0",
		Protocol: "http",
		DataSources: []dsl.DataSource{
			{Name: "resolved", Type: "file", InlineData: rawTable, Strategy: "sequential"},
			{Name: "still_redacted", Type: "file", InlineData: "Password\n[REDACTED]\n", Strategy: "sequential"},
		},
		Actions: []dsl.Action{{
			Name: "Submit", Type: "request", Method: "POST", Target: "/login",
			Metadata: map[string]any{"execution_blocker": credentialParameterSourceRedactedBlocker},
		}},
	}
	incoming := maskScriptSecrets(prior)
	if incoming == nil || !strings.Contains(incoming.DataSources[0].InlineData, maskedScriptSecret) {
		t.Fatalf("projected mixed runtime tables = %#v, want resolved table masked", incoming)
	}
	if err := reconcileMaskedScriptSecrets(incoming, prior); err != nil {
		t.Fatalf("update reconciliation rejected mixed gated runtime data: %v", err)
	}
	if got := incoming.DataSources[0].InlineData; got != rawTable {
		t.Fatalf("resolved table was overwritten by its projection: %q", got)
	}
	if got := incoming.DataSources[1].InlineData; got != "Password\n[REDACTED]\n" {
		t.Fatalf("durable redacted table = %q, want sentinel preserved", got)
	}
}

func TestReconcileMaskedScriptSecretsCanResolveGatedTableBesideMaskedSibling(t *testing.T) {
	const rawTable = "Password\npassword=do-not-disclose-1234567890\n"
	prior := &dsl.Script{
		Name:     "mixed parameter tables",
		Version:  "1.0.0",
		Protocol: "http",
		DataSources: []dsl.DataSource{
			{Name: "resolved", Type: "file", InlineData: rawTable, Strategy: "sequential"},
			{Name: "still_redacted", Type: "file", InlineData: "Password\n[REDACTED]\n", Strategy: "sequential"},
		},
		Actions: []dsl.Action{{
			Name: "Submit", Type: "request", Method: "POST", Target: "/login",
			Metadata: map[string]any{"execution_blocker": credentialParameterSourceRedactedBlocker},
		}},
	}
	incoming := maskScriptSecrets(prior)
	if incoming == nil || !strings.Contains(incoming.DataSources[0].InlineData, maskedScriptSecret) {
		t.Fatalf("projected mixed runtime tables = %#v, want resolved table masked", incoming)
	}
	incoming.DataSources[1].InlineData = "Password\napproved-value\n"
	incoming.Actions[0].Metadata = nil
	if err := reconcileMaskedScriptSecrets(incoming, prior); err != nil {
		t.Fatalf("resolving one gated table beside a masked sibling failed: %v", err)
	}
	if got := incoming.DataSources[0].InlineData; got != rawTable {
		t.Fatalf("resolved sibling table = %q, want stored value restored", got)
	}
	if got := incoming.DataSources[1].InlineData; got != "Password\napproved-value\n" {
		t.Fatalf("replacement table = %q, want client-approved value", got)
	}
}

func TestReconcileMaskedScriptSecretsRejectsGatedRuntimeTableReordering(t *testing.T) {
	prior := &dsl.Script{
		Name: "ordered parameter tables", Version: "1.0.0", Protocol: "http",
		DataSources: []dsl.DataSource{
			{Name: "first", Type: "file", InlineData: "Password\npassword=first-secret-1234567890\n"},
			{Name: "second", Type: "file", InlineData: "Password\n[REDACTED]\n"},
		},
		Actions: []dsl.Action{{
			Name: "Submit", Type: "request", Method: "POST", Target: "/login",
			Metadata: map[string]any{"execution_blocker": credentialParameterSourceRedactedBlocker},
		}},
	}
	incoming := maskScriptSecrets(prior)
	if incoming == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	incoming.DataSources[0], incoming.DataSources[1] = incoming.DataSources[1], incoming.DataSources[0]
	err := reconcileMaskedScriptSecrets(incoming, prior)
	if err == nil || !strings.Contains(err.Error(), "changed while containing redacted credentials") {
		t.Fatalf("reordered gated runtime tables error = %v, want fail-closed identity check", err)
	}
}

// A genuinely edited value must still be taken from the client.
func TestReconcileKeepsClientEditsOverStoredValue(t *testing.T) {
	got, err := reconcileProjectedJSON("edited by user", "projected", "stored raw", "script.actions.0.body")
	if err != nil {
		t.Fatalf("reconcileProjectedJSON returned error: %v", err)
	}
	if got != "edited by user" {
		t.Fatalf("reconciled = %q, want the client edit", got)
	}
}

func TestACMEShapedSOAPURLsDoNotCreateBodySecretRequirements(t *testing.T) {
	body := `<!-- endpoint http://p100csg8c1.vci.att.com:18000/Services/ACME_ISBA_WEB.jws -->` +
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<request><user>{{param.username}}</user><password>{{param.password}}</password></request>` +
		`</soap:Envelope>`
	script := &dsl.Script{
		Name: "ACME_ISBA_WEB", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{Name: "ACME_ISBA_WEB_CUSTOM_REQUEST", Type: "request", Method: "POST", Target: "/soap", Body: body}},
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	for _, value := range []string{
		"http://p100csg8c1.vci.att.com:18000/Services/ACME_ISBA_WEB.jws",
		"http://schemas.xmlsoap.org/soap/envelope/",
		"http://www.w3.org/2001/XMLSchema-instance",
		"{{param.username}}", "{{param.password}}",
	} {
		if !strings.Contains(projected.Actions[0].Body, value) {
			t.Fatalf("projected body dropped %q: %s", value, projected.Actions[0].Body)
		}
	}
	if requirements := discoverSecretBindingRequirements(projected, 0); len(requirements) != 0 {
		t.Fatalf("safe SOAP body created secret requirements: %#v", requirements)
	}
}

func TestMaskScriptSecretsMasksRetainedUnsupportedAuthSourceFields(t *testing.T) {
	const password = "retained-unsupported-auth-password-canary"
	script := &dsl.Script{
		Name: "retained auth", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "custom", Type: "request", Method: "GET", Target: "/custom",
			Auth: &dsl.AuthConfig{Type: "customscheme"},
			Metadata: map[string]any{
				"execution_blocker": "import_review_required",
				"postman_auth_source_fields": []map[string]any{{
					"type":  "source_credential",
					"name":  "password",
					"value": password,
				}},
			},
		}},
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), password) {
		t.Fatalf("ordinary script projection disclosed retained auth field: %s", payload)
	}
	if !strings.Contains(string(payload), maskedScriptSecret) {
		t.Fatalf("ordinary script projection did not mark retained auth field: %s", payload)
	}
}
