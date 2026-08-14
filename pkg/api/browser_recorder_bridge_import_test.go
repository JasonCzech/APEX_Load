package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBridgedBrowserImportReceivesRemoteAttestationAndKeepsCredentialServerSide(t *testing.T) {
	const credentialValue = "bridged-browser-password-canary-9a42"
	server := NewServer(ServerConfig{})
	script := dsl.Script{
		Name:      "Bridged HAR",
		Version:   "1",
		Protocol:  "http",
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Preserved: map[string]any{
				importDraftAttestationMetadataKey: "local-agent-token-has-no-remote-authority",
			},
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: http.MethodGet,
			Target: "/private",
			HeaderList: dsl.HeaderList{{
				Name:  "Authorization",
				Value: "Bearer " + credentialValue,
			}},
		}},
	}
	secondScript := cloneScriptDefinition(&script)
	secondScript.ID = "bridged-har-second"
	secondScript.Name = "Bridged HAR second"
	secondScript.Actions[0].Name = "second request"
	body, err := json.Marshal(map[string]any{
		"project_id": storage.DefaultProjectID,
		"scripts":    []dsl.Script{script, *secondScript},
	})
	if err != nil {
		t.Fatal(err)
	}
	attest := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/browser-recorder/attest",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(attest, request)
	if attest.Code != http.StatusOK {
		t.Fatalf("attestation status = %d body=%s", attest.Code, attest.Body.String())
	}
	if got := attest.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var response struct {
		Scripts []dsl.Script `json:"scripts"`
	}
	if err := json.Unmarshal(attest.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Scripts) != 2 {
		t.Fatalf("attested scripts = %#v", response.Scripts)
	}
	for index := range response.Scripts {
		if token := importDraftAttestationToken(&response.Scripts[index]); token == "" ||
			token == "local-agent-token-has-no-remote-authority" {
			t.Fatalf("remote attestation token %d = %q", index, token)
		}
	}
	attested := &response.Scripts[0]
	token := importDraftAttestationToken(attested)
	if token == "" || token == "local-agent-token-has-no-remote-authority" {
		t.Fatalf("remote attestation token = %q", token)
	}
	if attested.Metadata == nil || attested.Metadata.Import == nil ||
		attested.Metadata.Import.Source != "browser_recording_har" {
		t.Fatalf("remote profile = %#v, want server-stamped browser_recording_har", attested.Metadata)
	}
	if strings.Contains(attest.Body.String(), credentialValue) ||
		!strings.Contains(attest.Body.String(), maskedScriptSecret) {
		t.Fatal("remote-attested draft disclosed or failed to represent the protected credential")
	}

	createBody, err := json.Marshal(map[string]any{
		"project_id": storage.DefaultProjectID,
		"script":     attested,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRecorder()
	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts",
		bytes.NewReader(createBody),
	)
	createRequest.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(create, createRequest)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	if strings.Contains(create.Body.String(), credentialValue) {
		t.Fatalf("stored-script response disclosed credential: %s", create.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	durable, ok := server.runner.GetScript(created.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("remote-attested script was not saved")
	}
	if got := durable.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+credentialValue {
		t.Fatalf("durable bridged credential = %q", got)
	}
}

func TestBridgedBrowserImportRejectsNonBrowserProtocol(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, err := json.Marshal(map[string]any{
		"project_id": storage.DefaultProjectID,
		"scripts": []dsl.Script{{
			Name: "Java", Version: "1", Protocol: "java_vuser",
			ProjectID: storage.DefaultProjectID,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/browser-recorder/attest",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("non-browser handoff status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBridgedBrowserImportUsesResolverProjectWhenWrapperOmitsProject(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, err := json.Marshal(map[string]any{
		"scripts": []dsl.Script{{
			Name:      "Project-scoped bridged HAR",
			Version:   "1",
			Protocol:  "http",
			ProjectID: "project-from-resolver",
			Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{{
				Name: "request", Type: "request", Method: http.MethodGet, Target: "/",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/browser-recorder/attest?project_id=project-from-resolver",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("resolver-scoped handoff status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBridgedBrowserImportDefaultsOmittedProjectForGlobalCaller(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, err := json.Marshal(map[string]any{
		"scripts": []dsl.Script{{
			Name:     "Default-project bridged HAR",
			Version:  "1",
			Protocol: "http",
			Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{{
				Name: "request", Type: "request", Method: http.MethodGet, Target: "/",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/browser-recorder/attest",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("default-project handoff status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Scripts []dsl.Script `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Scripts) != 1 || payload.Scripts[0].ProjectID != storage.DefaultProjectID {
		t.Fatalf("default-project scripts = %#v, want project_id %q", payload.Scripts, storage.DefaultProjectID)
	}
}
