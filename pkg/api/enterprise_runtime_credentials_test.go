package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
)

func TestEnterpriseArchiveRuntimeCredentialsUseProtectedSourceSession(t *testing.T) {
	const (
		proxyUser = "enterprise-proxy-user-canary"
		proxyPass = "enterprise-proxy-pass-canary"
		authPass  = "enterprise-auth-pass-canary"
		apiKey    = "enterprise-attribute-key-canary"
	)
	archive := buildEnterpriseRuntimeCredentialArchive(t, map[string]string{
		"RuntimeCredentials.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[Proxy]
Mode=custom
ProxyServer=http://proxy.example.test:8080
ProxyUserName=` + proxyUser + `
ProxyPassword=` + proxyPass + `
[Authentication]
UserName=auth-user
Password=` + authPass + `
[Additional Attributes]
api_key=` + apiKey + `
`,
	})

	server, records, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source-secret migration: %v", err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x6d}, 32),
		KeyID: "enterprise-runtime-protected-source-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{
		Records: records,
		Wrapper: wrapper,
	}

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	part, err := writer.CreateFormFile("file", "RuntimeCredentials.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID,
		&requestBody,
	)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d; body=%s", response.Code, response.Body.String())
	}
	for _, canary := range []string{proxyUser, proxyPass, authPass, apiKey} {
		if bytes.Contains(response.Body.Bytes(), []byte(canary)) {
			t.Fatalf("enterprise import response disclosed %q: %s", canary, response.Body.Bytes())
		}
	}

	var envelope struct {
		Script                 dsl.Script                 `json:"script"`
		Requirements           []SecretBindingRequirement `json:"secret_binding_requirements"`
		SourceSecretSessionID  string                     `json:"source_secret_session_id"`
		SourceMigrationAllowed bool                       `json:"source_secret_migration_allowed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Requirements) != 4 {
		t.Fatalf("secret binding requirements = %#v, want four", envelope.Requirements)
	}
	for _, requirement := range envelope.Requirements {
		if !requirement.SourceValueAvailable {
			t.Errorf("requirement %q did not retain its protected source value", requirement.ID)
		}
	}
	if envelope.SourceSecretSessionID == "" || !envelope.SourceMigrationAllowed {
		t.Fatalf("protected source session missing: %#v", envelope)
	}
	if importDraftAttestationToken(&envelope.Script) == "" ||
		envelope.Script.Config.HTTP.Proxy.Username != maskedScriptSecret ||
		envelope.Script.Config.HTTP.Proxy.Password != maskedScriptSecret ||
		envelope.Script.Config.HTTP.Auth.Password != maskedScriptSecret ||
		envelope.Script.Config.WebRuntime.AdditionalAttributes["api_key"] != maskedScriptSecret {
		t.Fatalf("enterprise browser projection was not masked and attested: %#v", envelope.Script.Config)
	}

	created := createAttestedImportScript(t, server, &envelope.Script)
	if created.Code != http.StatusCreated {
		t.Fatalf("save enterprise draft status = %d body=%s", created.Code, created.Body.String())
	}
	for _, canary := range []string{proxyUser, proxyPass, authPass, apiKey} {
		if strings.Contains(created.Body.String(), canary) {
			t.Fatalf("enterprise save response disclosed %q: %s", canary, created.Body.String())
		}
	}
	var storedResponse StoredScript
	if err := json.Unmarshal(created.Body.Bytes(), &storedResponse); err != nil {
		t.Fatal(err)
	}
	durable, ok := server.runner.GetScript(storedResponse.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("attested enterprise draft was not persisted")
	}
	if durable.Script.Config.HTTP.Proxy.Username != proxyUser ||
		durable.Script.Config.HTTP.Proxy.Password != proxyPass ||
		durable.Script.Config.HTTP.Auth.Password != authPass ||
		durable.Script.Config.WebRuntime.AdditionalAttributes["api_key"] != apiKey {
		t.Fatalf("durable enterprise draft lost exact source credentials: %#v", durable.Script.Config)
	}
}

// The imported proxy credentials stay inert (proxy_mode "none") but durable and
// exact. Browser projections and ordinary reads mask them; the runnable export
// keeps them so importing that artifact again preserves execution behavior.
func TestEnterpriseArchiveUnsupportedProxyCredentialsPersistInertAndMaskBrowserSurfaces(t *testing.T) {
	const (
		proxyUser = "unsupported-enterprise-proxy-user-canary-47ad"
		proxyPass = "unsupported-enterprise-proxy-pass-canary-93e1"
	)
	archive := buildEnterpriseRuntimeCredentialArchive(t, map[string]string{
		"UnsupportedProxy.usr": `[General]
Type=Web
RuntimeSettings=default.cfg
[Actions]
Action1=Action.c
`,
		"Action.c": `Action()
{
	web_url("request", "URL=https://example.test/orders", LAST);
	return 0;
}
`,
		"default.cfg": `[Proxy]
Mode=automatic
ProxyUseProxy=1
ProxyServer=http://proxy.example.test:8080
ProxyUserName=` + proxyUser + `
ProxyPassword=` + proxyPass + `
`,
	})

	server := NewServer(ServerConfig{})
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	part, err := writer.CreateFormFile("file", "UnsupportedProxy.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	importRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/enterprise-script-archive",
		&requestBody,
	)
	importRequest.Header.Set("Content-Type", writer.FormDataContentType())
	importResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusOK {
		t.Fatalf(
			"unsupported proxy import status = %d body=%s",
			importResponse.Code,
			importResponse.Body.String(),
		)
	}
	var inspection struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(importResponse.Body.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if inspection.Script.Config.HTTP.ProxyMode != "none" ||
		inspection.Script.Config.HTTP.Proxy.URL != "" ||
		inspection.Script.Config.HTTP.Proxy.Username != maskedScriptSecret ||
		inspection.Script.Config.HTTP.Proxy.Password != maskedScriptSecret ||
		importDraftAttestationToken(&inspection.Script) == "" ||
		strings.Contains(importResponse.Body.String(), proxyUser) ||
		strings.Contains(importResponse.Body.String(), proxyPass) {
		t.Fatalf(
			"browser-visible disabled proxy record was not masked and attested = %#v",
			inspection.Script.Config.HTTP,
		)
	}

	createBody, err := json.Marshal(map[string]any{"script": inspection.Script})
	if err != nil {
		t.Fatal(err)
	}
	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts",
		bytes.NewReader(createBody),
	)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf(
			"save unsupported proxy draft status = %d body=%s",
			createResponse.Code,
			createResponse.Body.String(),
		)
	}
	if strings.Contains(createResponse.Body.String(), proxyUser) ||
		strings.Contains(createResponse.Body.String(), proxyPass) {
		t.Fatalf("save response disclosed proxy credentials: %s", createResponse.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatalf("save response omitted script identity: %s", createResponse.Body.String())
	}
	durable, ok := server.runner.GetScript(created.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("unsupported proxy draft was not saved")
	}
	if durable.Script.Config.HTTP.ProxyMode != "none" ||
		durable.Script.Config.HTTP.Proxy.URL != "" ||
		durable.Script.Config.HTTP.Proxy.Username != proxyUser ||
		durable.Script.Config.HTTP.Proxy.Password != proxyPass {
		t.Fatalf(
			"durable disabled proxy record lost source credentials: %#v",
			durable.Script.Config.HTTP,
		)
	}

	readRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/scripts/"+durable.ID,
		nil,
	)
	readResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read status = %d body=%s", readResponse.Code, readResponse.Body.String())
	}
	if strings.Contains(readResponse.Body.String(), proxyUser) ||
		strings.Contains(readResponse.Body.String(), proxyPass) ||
		!strings.Contains(readResponse.Body.String(), maskedScriptSecret) {
		t.Fatalf("ordinary read did not mask disabled proxy credentials: %s", readResponse.Body.String())
	}

	exportDir := t.TempDir()
	t.Setenv("APEXLOAD_EXPORT_DIR", exportDir)
	exportRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+durable.ID+"/export",
		strings.NewReader(`{"filename":"unsupported-proxy.json"}`),
	)
	exportRequest.Header.Set("Content-Type", "application/json")
	exportResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf(
			"export status = %d body=%s",
			exportResponse.Code,
			exportResponse.Body.String(),
		)
	}
	exported, err := os.ReadFile(filepath.Join(exportDir, "unsupported-proxy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(exported, []byte(proxyUser)) ||
		!bytes.Contains(exported, []byte(proxyPass)) {
		t.Fatalf("script export dropped the imported proxy credentials: %s", exported)
	}
	var exportedEnvelope struct {
		Script *dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(exported, &exportedEnvelope); err != nil {
		t.Fatalf("script export is not a script envelope: %v", err)
	}
	if exportedEnvelope.Script == nil ||
		exportedEnvelope.Script.Config.HTTP.ProxyMode != "none" ||
		exportedEnvelope.Script.Config.HTTP.Proxy.URL != "" ||
		exportedEnvelope.Script.Config.HTTP.Proxy.Username != proxyUser ||
		exportedEnvelope.Script.Config.HTTP.Proxy.Password != proxyPass {
		t.Fatalf("script export is not the durable inert proxy record: %s", exported)
	}
}

func buildEnterpriseRuntimeCredentialArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range files {
		entry, err := writer.Create("RuntimeCredentials/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}
