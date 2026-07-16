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
			Database:  dsl.DatabaseConfig{DSN: "postgres://db-user:db-password@db.test/main", DSNSecret: "{{secret.database_dsn}}"},
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
	for _, reference := range []string{"{{secret.database_dsn}}", "{{secret.kafka_password}}", "{{secret.eventhub_connection}}", "{{secret.query_token}}", "{{secret.action_token}}", "{{secret.body_token}}"} {
		if !strings.Contains(text, reference) {
			t.Fatalf("masked script lost secret reference %q: %s", reference, text)
		}
	}
	if !strings.Contains(text, maskedScriptSecret) || raw.Config.HTTP.Auth.Password != "global-password" {
		t.Fatal("projection did not redact deeply or mutated the runtime script")
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
	stored, err := server.runner.StoreScriptDurable(secretBearingScript("viewer-secret", "project-a"))
	if err != nil {
		t.Fatal(err)
	}

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

func TestPortableHashesBundlesAndGitHubPayloadNeverContainSecrets(t *testing.T) {
	server := NewServer(ServerConfig{})
	first, err := server.runner.StoreScriptDurable(secretBearingScript("portable", "project-a"))
	if err != nil {
		t.Fatal(err)
	}
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
	stored, err := server.runner.StoreScriptDurable(secretBearingScript("github-secret", "team-a"))
	if err != nil {
		t.Fatal(err)
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
