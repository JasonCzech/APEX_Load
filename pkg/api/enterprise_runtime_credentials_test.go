package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

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
			t.Fatalf("enterprise response disclosed %q: %s", canary, response.Body.String())
		}
	}

	var envelope struct {
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
