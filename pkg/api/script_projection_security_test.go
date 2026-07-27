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
}

func TestStoredScriptViewerDownloadAndPrivateExportUseProjection(t *testing.T) {
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
	assertMasked("download", download)

	exportReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/export", strings.NewReader(`{"filename":"private.json"}`))
	exportReq.SetPathValue("id", stored.ID)
	exported := httptest.NewRecorder()
	server.handleExportScript(exported, exportReq)
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
	if strings.Contains(string(data), "global-password") || !strings.Contains(string(data), maskedScriptSecret) {
		t.Fatalf("private export was not projected: %s", data)
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
		wantWire := strings.Replace(wireBody, canary, maskedScriptSecret, 1)
		if string(decoded) != wantWire {
			t.Fatalf("%s masked wire bytes changed outside credential occurrence:\n got: %q\nwant: %q", label, decoded, wantWire)
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

func TestPortableHashesBundlesAndGitHubPayloadNeverContainSecrets(t *testing.T) {
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
	itemJSON, _ := json.Marshal(item)
	if strings.Contains(string(itemJSON), "global-password") {
		t.Fatalf("library item leaked secret: %s", itemJSON)
	}
	githubPayload, _, err := githubScriptEnvelope(first.Script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(githubPayload), "global-password") || !strings.Contains(string(githubPayload), maskedScriptSecret) {
		t.Fatalf("github envelope leaked secret: %s", githubPayload)
	}
}

func TestGitHubPushSendsProjectedPayload(t *testing.T) {
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
	if strings.Contains(string(payload), "global-password") || !strings.Contains(string(payload), maskedScriptSecret) {
		t.Fatalf("github PUT leaked secret: %s", payload)
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
