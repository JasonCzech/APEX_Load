package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
)

// A credential-preserving import pins the proxy and TLS settings its kept
// credentials are sent with, unless a system admin allows those edits.
func TestRetainedCredentialTransportEditsRequireAdminSetting(t *testing.T) {
	server, records, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source-secret migration: %v", err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5d}, 32),
		KeyID: "transport-edit-session",
	})
	if err != nil {
		t.Fatalf("create wrapper: %v", err)
	}
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{Records: records, Wrapper: wrapper}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "ACME.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(buildSanitizedACMEArchive(t)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID, &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d; body=%s", response.Code, response.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var imported dsl.Script
	if err := json.Unmarshal(envelope["script"], &imported); err != nil {
		t.Fatal(err)
	}
	imported.ID = ""
	imported.ProjectID = projectID
	created := postSecretBindingJSON(t, server, "/api/v1/scripts", map[string]any{"project_id": projectID, "script": &imported})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil || createdBody.ID == "" {
		t.Fatalf("created script id: %v %s", err, created.Body.String())
	}

	save := func(edit func(*dsl.Script)) *httptest.ResponseRecorder {
		t.Helper()
		get := httptest.NewRecorder()
		server.mux.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+createdBody.ID, nil))
		if get.Code != http.StatusOK {
			t.Fatalf("get status = %d; body=%s", get.Code, get.Body.String())
		}
		var view struct {
			Script dsl.Script `json:"script"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		edit(&view.Script)
		payload, err := json.Marshal(map[string]any{"script": &view.Script})
		if err != nil {
			t.Fatal(err)
		}
		put := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+createdBody.ID, bytes.NewReader(payload))
		put.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, put)
		return recorder
	}
	customProxy := func(script *dsl.Script) {
		script.Config.HTTP.ProxyMode = "custom"
		script.Config.HTTP.Proxy.URL = "http://proxy.example.test:8888"
	}
	insecureTLS := func(script *dsl.Script) { script.Config.HTTP.TLS.InsecureSkipVerify = true }

	for name, edit := range map[string]func(*dsl.Script){"custom proxy": customProxy, "TLS verification": insecureTLS} {
		if got := save(edit); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "retained imported credentials") {
			t.Fatalf("%s with the setting off: status = %d; body=%s", name, got.Code, got.Body.String())
		}
	}
	if got := save(func(script *dsl.Script) { script.Config.HTTP.ProxyMode = "none" }); got.Code != http.StatusOK {
		t.Fatalf("No Proxy status = %d; body=%s", got.Code, got.Body.String())
	}

	settings = server.runner.GetSystemSettings()
	settings.ScriptImports.AllowRetainedCredentialTransportEdits = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable transport edits: %v", err)
	}
	for name, edit := range map[string]func(*dsl.Script){"custom proxy": customProxy, "TLS verification": insecureTLS} {
		if got := save(edit); got.Code != http.StatusOK {
			t.Fatalf("%s with the setting on: status = %d; body=%s", name, got.Code, got.Body.String())
		}
	}
	// The setting widens only the transport route: a request header added to a
	// kept-credential request is still refused.
	if got := save(func(script *dsl.Script) { script.Config.HTTP.DefaultHeaders = map[string]string{"X-Debug": "1"} }); got.Code != http.StatusBadRequest {
		t.Fatalf("default header with the setting on: status = %d; body=%s", got.Code, got.Body.String())
	}
}
