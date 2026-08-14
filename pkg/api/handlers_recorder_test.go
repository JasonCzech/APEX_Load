package api

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandlePreviewRecordingConversionDoesNotStoreScript(t *testing.T) {
	server := NewServer(ServerConfig{})
	session, err := server.recorder.StartSession("rec-1", "Checkout Recording", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.com"
	session.Requests = append(session.Requests,
		recorder.RecordedRequest{
			Seq:        1,
			Method:     http.MethodGet,
			URL:        "https://shop.example.com/api/cart",
			Path:       "/api/cart",
			Headers:    map[string]string{"Authorization": "Bearer preview-secret"},
			StatusCode: http.StatusOK,
			StepName:   "Load Cart",
		},
		recorder.RecordedRequest{
			Seq:        2,
			Method:     http.MethodGet,
			URL:        "https://shop.example.com/analytics.gif",
			Path:       "/analytics.gif",
			StatusCode: http.StatusOK,
			Excluded:   true,
		},
	)

	body := bytes.NewBufferString(`{"name":"Checkout Preview"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-1/convert/preview", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Script struct {
			Name    string `json:"name"`
			Actions []struct {
				Name    string            `json:"name"`
				Target  string            `json:"target"`
				Headers map[string]string `json:"headers"`
			} `json:"actions"`
		} `json:"script"`
		SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Name != "Checkout Preview" {
		t.Fatalf("script name = %q, want Checkout Preview", result.Script.Name)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("action count = %d, want 1", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Target != "/api/cart" {
		t.Fatalf("target = %q, want /api/cart", result.Script.Actions[0].Target)
	}
	if len(result.SecretBindingRequirements) == 0 {
		t.Fatalf("preview secret requirements = %#v", result.SecretBindingRequirements)
	}
	if strings.Contains(resp.Body.String(), "preview-secret") ||
		result.Script.Actions[0].Headers["Authorization"] != "Bearer "+maskedScriptSecret {
		t.Fatalf("recording preview disclosed or failed to represent its protected credential: %s", resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("preview stored %d scripts, want 0", got)
	}

	var previewEnvelope struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &previewEnvelope); err != nil {
		t.Fatal(err)
	}
	if !isCredentialPreservingImport(&previewEnvelope.Script) ||
		importDraftAttestationToken(&previewEnvelope.Script) == "" {
		t.Fatalf("preview lacks server-owned credential-preserving save proof: %#v", previewEnvelope.Script.Metadata)
	}
	savePayload, err := json.Marshal(map[string]any{"script": previewEnvelope.Script})
	if err != nil {
		t.Fatal(err)
	}
	saveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(savePayload))
	saveReq.Header.Set("Content-Type", "application/json")
	saveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(saveResp, saveReq)
	if saveResp.Code != http.StatusCreated {
		t.Fatalf("preview save status = %d body=%s", saveResp.Code, saveResp.Body.String())
	}
	if strings.Contains(saveResp.Body.String(), "preview-secret") {
		t.Fatalf("stored-script response disclosed preview credential: %s", saveResp.Body.String())
	}
	stored := server.runner.ListScripts()
	if len(stored) != 1 || stored[0] == nil || stored[0].Script == nil ||
		stored[0].Script.Actions[0].Headers["Authorization"] != "Bearer preview-secret" {
		t.Fatalf("preview save discarded captured credential: %#v", stored)
	}
}

func TestBroadcastRecorderSessionEventPreservesScrubbedCapWarningScope(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.hub.broadcast = make(chan hubBroadcast, 1)

	warning := "recording truncated: requests are being dropped after reaching the request-count retention limit; access_token=handler-secret"
	server.broadcastRecorderSessionEvent("project-1", recorder.SessionEvent{
		Type:      "recording_warning",
		SessionID: "recording-1",
		Warning:   warning,
		Session: &recorder.Session{
			ID:        "recording-1",
			ProjectID: "project-1",
			Warnings:  []string{warning},
		},
	})

	select {
	case message := <-server.hub.broadcast:
		if message.projectID != "project-1" || message.testID != "" || message.broadcastAll {
			t.Fatalf("broadcast scope = %#v, want project-1 only", message)
		}
		var payload struct {
			Type      string `json:"type"`
			ProjectID string `json:"project_id"`
			SessionID string `json:"session_id"`
			Warning   string `json:"warning"`
			Session   struct {
				Warnings []string `json:"warnings"`
			} `json:"session"`
		}
		if err := json.Unmarshal(message.payload, &payload); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if payload.Type != "recording_warning" || payload.ProjectID != "project-1" || payload.SessionID != "recording-1" {
			t.Fatalf("broadcast payload identity = %#v", payload)
		}
		if !strings.Contains(payload.Warning, "request-count retention limit") {
			t.Fatalf("warning = %q, want cap warning", payload.Warning)
		}
		if strings.Contains(payload.Warning, "handler-secret") || !strings.Contains(payload.Warning, scrub.Redacted) {
			t.Fatalf("warning was not scrubbed: %q", payload.Warning)
		}
		if len(payload.Session.Warnings) != 1 || payload.Session.Warnings[0] != payload.Warning {
			t.Fatalf("session warnings = %#v, want scrubbed live warning %q", payload.Session.Warnings, payload.Warning)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recorder warning broadcast")
	}
}

func TestHandleConvertRecordingPersistsBaseline(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recorder-handler-test-key")
	server := recordingTestServer(t)
	now := time.Now().UTC()
	if err := server.store.SaveProject(&storage.Project{ID: "project-1", Name: "Project 1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	session, err := server.recorder.StartSession("rec-baseline", "Checkout Recording", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.ProjectID = "project-1"
	session.BaseURL = "https://shop.example.com"
	session.Requests = append(session.Requests,
		recorder.RecordedRequest{
			Seq:             1,
			Method:          http.MethodPost,
			URL:             "https://shop.example.com/api/login",
			Path:            "/api/login",
			Headers:         map[string]string{"Authorization": "Bearer recorded-secret"},
			Body:            `{"password":"hunter2"}`,
			StatusCode:      http.StatusOK,
			ResponseBody:    `{"csrf":"csrf-recorded"}`,
			ResponseHeaders: map[string]string{"Set-Cookie": "sid=abc"},
		},
		recorder.RecordedRequest{
			Seq:        2,
			Method:     http.MethodGet,
			URL:        "https://shop.example.com/analytics.gif",
			Path:       "/analytics.gif",
			StatusCode: http.StatusOK,
			Excluded:   true,
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-baseline/convert", bytes.NewBufferString(`{"name":"Checkout Converted"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var payload struct {
		Script struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
			Script    struct {
				ProjectID string `json:"project_id"`
			} `json:"script"`
		} `json:"script"`
		SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.Script.ID == "" {
		t.Fatalf("stored script id was empty: %s", resp.Body.String())
	}
	if payload.Script.ProjectID != "project-1" || payload.Script.Script.ProjectID != "project-1" {
		t.Fatalf("stored project ids = wrapper %q script %q, want project-1", payload.Script.ProjectID, payload.Script.Script.ProjectID)
	}
	if len(payload.SecretBindingRequirements) < 2 {
		t.Fatalf("stored conversion requirements = %#v", payload.SecretBindingRequirements)
	}
	if strings.Contains(resp.Body.String(), "recorded-secret") || strings.Contains(resp.Body.String(), "hunter2") {
		t.Fatalf("stored conversion response disclosed source credentials: %s", resp.Body.String())
	}
	server.importDraftAttestationMu.Lock()
	attestationCount := len(server.importDraftAttestations)
	server.importDraftAttestationMu.Unlock()
	if attestationCount != 0 {
		t.Fatalf("already-committed recording response retained %d unused draft attestation(s)", attestationCount)
	}
	storedScript, ok := server.runner.GetScript(payload.Script.ID)
	if !ok || storedScript == nil || storedScript.Script == nil {
		t.Fatalf("stored script %q was unavailable", payload.Script.ID)
	}
	storedJSON, err := json.Marshal(storedScript.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(storedJSON), "recorded-secret") || !strings.Contains(string(storedJSON), "hunter2") {
		t.Fatalf("durable imported script discarded recorded credentials: %s", storedJSON)
	}
	if strings.Contains(string(storedJSON), "{{secret.") || strings.Contains(string(storedJSON), scrub.Redacted) {
		t.Fatalf("durable imported script requires credential re-entry or contains a sentinel: %s", storedJSON)
	}
	if !isCredentialPreservingImport(storedScript.Script) {
		t.Fatalf("stored script lacks server-owned import preservation policy: %#v", storedScript.Script.Metadata)
	}
	readReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+payload.Script.ID, nil)
	readResp := httptest.NewRecorder()
	server.mux.ServeHTTP(readResp, readReq)
	if readResp.Code != http.StatusOK {
		t.Fatalf("stored read status = %d body=%s", readResp.Code, readResp.Body.String())
	}
	if strings.Contains(readResp.Body.String(), "recorded-secret") || strings.Contains(readResp.Body.String(), "hunter2") {
		t.Fatalf("ordinary stored-script read disclosed recorded credentials: %s", readResp.Body.String())
	}
	record, err := server.store.GetScriptRecording(payload.Script.ID)
	if err != nil {
		t.Fatalf("GetScriptRecording returned error: %v", err)
	}
	if record == nil {
		t.Fatal("recording baseline was not persisted")
	}
	if record.Source != "proxy_recording" {
		t.Fatalf("baseline source = %q, want proxy_recording", record.Source)
	}
	if len(record.Observations) != 1 {
		t.Fatalf("observations = %d, want 1 included request", len(record.Observations))
	}
	obs := record.Observations[0]
	if obs.ID != 0 || obs.RequestURL != "" || obs.RequestBody != "" || obs.ResponseBody != "" || obs.RequestHeaders != nil || obs.ResponseHeaders != nil {
		t.Fatalf("observation = %+v, want structural-only first included request at id 0", obs)
	}
	// The public persistence view contains no caller-derived URL, body, or
	// header values; correlation uses the encrypted raw sidecar below.
	// The server-side encrypted correlation sidecar preserves the dynamic value
	// without persisting plaintext credentials.
	if len(record.CorrelationObservations) != 0 || record.EncryptedCorrelationObservations == "" {
		t.Fatalf("correlation sidecar = plaintext:%d encrypted:%t, want encrypted only", len(record.CorrelationObservations), record.EncryptedCorrelationObservations != "")
	}
	correlationObservations, err := baselineObservations(record)
	if err != nil {
		t.Fatalf("baselineObservations returned error: %v", err)
	}
	if len(correlationObservations) != 1 || !strings.Contains(correlationObservations[0].ResponseBody, "csrf-recorded") {
		t.Fatalf("decrypted correlation observations = %#v, want dynamic response value preserved for correlation", correlationObservations)
	}
}

func TestHandleConvertRecordingParameterizationPreservesCapturedPassword(t *testing.T) {
	server := recordingTestServer(t)
	session, err := server.recorder.StartSessionForProject(
		"rec-parameterized-secret",
		"Parameterized Login",
		"p1",
		recorder.SessionFilters{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.com"
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:         1,
		Method:      http.MethodPost,
		URL:         "https://shop.example.com/login",
		Path:        "/login",
		Body:        "username=demo&password=parameterized-captured-password",
		ContentType: "application/x-www-form-urlencoded",
		StatusCode:  http.StatusOK,
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/recorder/rec-parameterized-secret/convert",
		bytes.NewBufferString(`{"name":"Parameterized Login","parameterize":true}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "parameterized-captured-password") {
		t.Fatalf("saved-import response disclosed captured password: %s", resp.Body.String())
	}

	var payload struct {
		Script struct {
			ID string `json:"id"`
		} `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	stored, ok := server.runner.GetScript(payload.Script.ID)
	if !ok || stored == nil || stored.Script == nil || len(stored.Script.DataSources) != 1 {
		t.Fatalf("durable parameterized script = %#v", stored)
	}
	source := stored.Script.DataSources[0]
	if !strings.Contains(source.InlineData, "parameterized-captured-password") ||
		strings.Contains(source.InlineData, "__REPLACE_WITH_SECRET__") {
		t.Fatalf("durable parameterized data = %q, want captured password without re-entry placeholder", source.InlineData)
	}
	if !strings.Contains(stored.Script.Actions[0].Body, "{{data.recorded_inputs.password}}") {
		t.Fatalf("parameterized body = %q", stored.Script.Actions[0].Body)
	}

	readReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+payload.Script.ID, nil)
	readResp := httptest.NewRecorder()
	server.mux.ServeHTTP(readResp, readReq)
	if readResp.Code != http.StatusOK {
		t.Fatalf("stored read status = %d body=%s", readResp.Code, readResp.Body.String())
	}
	var read StoredScript
	if err := json.Unmarshal(readResp.Body.Bytes(), &read); err != nil {
		t.Fatal(err)
	}
	if read.Script == nil || len(read.Script.DataSources) != 1 ||
		!strings.Contains(read.Script.DataSources[0].InlineData, "parameterized-captured-password") {
		t.Fatalf("ordinary stored-script data-file view hid the captured password: %s", readResp.Body.String())
	}
}

func TestHandleConvertRecordingRejectsEmptyConversion(t *testing.T) {
	server := NewServer(ServerConfig{})
	session, err := server.recorder.StartSession("rec-empty", "Empty Recording", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.com"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-empty/convert", bytes.NewBufferString(`{"name":"Empty Converted"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusUnprocessableEntity, resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("empty conversion persisted %d scripts, want 0", got)
	}
}

func TestHandleConvertRecordingRejectsMalformedJSONBeforePersisting(t *testing.T) {
	server := NewServer(ServerConfig{})
	session, err := server.recorder.StartSession("rec-malformed", "Malformed Options", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.com"
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq: 1, Method: http.MethodGet, URL: "https://shop.example.com/cart", Path: "/cart", StatusCode: http.StatusOK,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-malformed/convert", bytes.NewBufferString(`{"name":`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("malformed conversion persisted %d scripts, want 0", got)
	}
}

func TestHandleStartRecordingThreadsCaptureCookies(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewBufferString(`{
		"name": "Cookie Capture",
		"capture_cookies": true
	}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var payload struct {
		Session recorder.Session `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !payload.Session.Filters.CaptureCookies {
		t.Fatalf("capture_cookies = false, want true: %#v", payload.Session.Filters)
	}
}

func TestHandleStartRecordingAllowsStaticCapture(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewBufferString(`{
		"name": "Static Capture",
		"exclude_static": false
	}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var payload struct {
		Session recorder.Session `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.Session.Filters.ExcludeStatic {
		t.Fatalf("exclude_static = true, want explicit false: %#v", payload.Session.Filters)
	}
}

func TestHandleRecordingSummariesAndDeltaRequests(t *testing.T) {
	server := NewServer(ServerConfig{})
	session, err := server.recorder.StartSessionForProject("rec-delta", "Delta Recording", "project-1", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests,
		recorder.RecordedRequest{Seq: 1, Method: http.MethodPost, URL: "https://shop.example.com/api/login", Path: "/api/login", Body: `{"password":"secret"}`, ResponseBody: `{"token":"abc"}`, StatusCode: http.StatusOK},
		recorder.RecordedRequest{Seq: 2, Method: http.MethodGet, URL: "https://shop.example.com/api/cart", Path: "/api/cart", ResponseBody: `{"items":[]}`, StatusCode: http.StatusOK},
	)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder?project_id=project-1", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var listPayload struct {
		Sessions []recorder.Session `json:"sessions"`
		Active   *recorder.Session  `json:"active"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listPayload); err != nil {
		t.Fatalf("json.Unmarshal list returned error: %v", err)
	}
	if len(listPayload.Sessions) != 1 || len(listPayload.Sessions[0].Requests) != 0 || listPayload.Sessions[0].RequestCount != 2 {
		t.Fatalf("list payload = %#v, want summary without requests", listPayload.Sessions)
	}
	if listPayload.Active == nil || len(listPayload.Active.Requests) != 0 || listPayload.Active.RequestCount != 2 {
		t.Fatalf("active payload = %#v, want active summary without requests", listPayload.Active)
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-delta", nil)
	summaryResp := httptest.NewRecorder()
	server.mux.ServeHTTP(summaryResp, summaryReq)
	if summaryResp.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want %d body=%s", summaryResp.Code, http.StatusOK, summaryResp.Body.String())
	}
	var summary recorder.Session
	if err := json.Unmarshal(summaryResp.Body.Bytes(), &summary); err != nil {
		t.Fatalf("json.Unmarshal summary returned error: %v", err)
	}
	if len(summary.Requests) != 0 || summary.RequestCount != 2 {
		t.Fatalf("summary = %#v, want no requests with full count", &summary)
	}

	deltaReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-delta?since=1", nil)
	deltaResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deltaResp, deltaReq)
	if deltaResp.Code != http.StatusOK {
		t.Fatalf("delta status = %d, want %d body=%s", deltaResp.Code, http.StatusOK, deltaResp.Body.String())
	}
	var delta recorder.Session
	if err := json.Unmarshal(deltaResp.Body.Bytes(), &delta); err != nil {
		t.Fatalf("json.Unmarshal delta returned error: %v", err)
	}
	if len(delta.Requests) != 1 || delta.Requests[0].Seq != 2 || delta.Requests[0].ResponseBody == "" {
		t.Fatalf("delta = %#v, want full request after sequence 1", &delta)
	}

	fullReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-delta?include=requests", nil)
	fullResp := httptest.NewRecorder()
	server.mux.ServeHTTP(fullResp, fullReq)
	if fullResp.Code != http.StatusOK {
		t.Fatalf("full status = %d, want %d body=%s", fullResp.Code, http.StatusOK, fullResp.Body.String())
	}
	var full recorder.Session
	if err := json.Unmarshal(fullResp.Body.Bytes(), &full); err != nil {
		t.Fatalf("json.Unmarshal full returned error: %v", err)
	}
	if len(full.Requests) != 2 || full.Requests[0].Body == "" {
		t.Fatalf("full = %#v, want include=requests to return stored bodies", &full)
	}
}

func TestHandleExportRecordingHAR(t *testing.T) {
	server := NewServer(ServerConfig{})
	session, err := server.recorder.StartSession("rec-2", "Checkout HAR", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests,
		recorder.RecordedRequest{
			Seq:                  1,
			Method:               http.MethodPost,
			URL:                  "https://shop.example.com/api/cart?debug=true",
			Path:                 "/api/cart",
			QueryString:          "debug=true",
			HeadersMulti:         map[string][]string{"Content-Type": {"application/json"}},
			Body:                 `{"sku":"123"}`,
			BodySizeBytes:        13,
			StatusCode:           http.StatusCreated,
			ResponseHeadersMulti: map[string][]string{"Content-Type": {"application/json"}},
			ResponseBody:         `{"id":"cart-1"}`,
		},
		recorder.RecordedRequest{
			Seq:        2,
			Method:     http.MethodGet,
			URL:        "https://shop.example.com/ignored",
			Path:       "/ignored",
			StatusCode: http.StatusOK,
			Excluded:   true,
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-2/export?format=har", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := resp.Header().Get("Content-Disposition"); got != `attachment; filename="checkout-har.har"` {
		t.Fatalf("content disposition = %q", got)
	}

	var payload struct {
		Log struct {
			Entries []json.RawMessage `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(payload.Log.Entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(payload.Log.Entries))
	}
}

func TestHandleRecorderCACertHonorsFormatAfterInitialization(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_CA_DIR", t.TempDir())
	server := NewServer(ServerConfig{})

	pemReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/ca.pem?format=pem", nil)
	pemResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pemResp, pemReq)

	if pemResp.Code != http.StatusOK {
		t.Fatalf("pem status = %d, want %d body=%s", pemResp.Code, http.StatusOK, pemResp.Body.String())
	}
	if ct := pemResp.Header().Get("Content-Type"); ct != "application/x-pem-file" {
		t.Fatalf("pem content type = %q, want application/x-pem-file", ct)
	}
	if block, _ := pem.Decode(pemResp.Body.Bytes()); block == nil {
		t.Fatal("expected PEM certificate body")
	}

	crtReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/ca.pem", nil)
	crtResp := httptest.NewRecorder()
	server.mux.ServeHTTP(crtResp, crtReq)

	if crtResp.Code != http.StatusOK {
		t.Fatalf("crt status = %d, want %d body=%s", crtResp.Code, http.StatusOK, crtResp.Body.String())
	}
	if ct := crtResp.Header().Get("Content-Type"); ct != "application/x-x509-ca-cert" {
		t.Fatalf("crt content type = %q, want application/x-x509-ca-cert", ct)
	}
	if cd := crtResp.Header().Get("Content-Disposition"); !strings.Contains(cd, "apexload-ca.crt") {
		t.Fatalf("crt content disposition = %q, want apexload-ca.crt", cd)
	}
	if _, err := x509.ParseCertificate(crtResp.Body.Bytes()); err != nil {
		t.Fatalf("expected DER certificate body: %v", err)
	}
}

func TestHandleRecorderStatusIncludesPersistentCAPath(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, nil)
	server := NewServer(ServerConfig{})
	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status", nil)
	statusResp := httptest.NewRecorder()

	server.mux.ServeHTTP(statusResp, statusReq)

	if statusResp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", statusResp.Code, http.StatusOK, statusResp.Body.String())
	}

	var payload struct {
		ProxyAddr  string `json:"proxy_addr"`
		CACertPath string `json:"ca_cert_path"`
	}
	if err := json.Unmarshal(statusResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.ProxyAddr == "" {
		t.Fatal("expected proxy address in recorder status")
	}
	if payload.CACertPath == "" {
		t.Fatal("expected persistent CA certificate path in recorder status")
	}
	if got, want := payload.CACertPath, filepath.Join("artifacts", "recorder", "ca-cert.pem"); got != want {
		t.Fatalf("ca_cert_path = %q, want %q", got, want)
	}
}

func TestHandleRecorderStatusFiltersActiveSessionByProject(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, nil)
	server := NewServer(ServerConfig{})
	if _, err := server.recorder.StartSessionForProject("rec-project-a", "Project A", "project-a", recorder.SessionFilters{}, nil); err != nil {
		t.Fatal(err)
	}

	status := func(projectID string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status?project_id="+projectID, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("status for %s = %d body=%s", projectID, resp.Code, resp.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}

	if active := status("project-a")["active"]; active == nil {
		t.Fatal("matching project did not receive its active recorder session")
	}
	if active := status("project-b")["active"]; active != nil {
		t.Fatalf("foreign project received active recorder session: %#v", active)
	}
}

func TestRecorderResponsesDoNotExposeProxyAuthToken(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, nil)
	t.Setenv("APEXLOAD_RECORDER_PROXY_TOKEN", "proxy-secret")
	server := NewServer(ServerConfig{})

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status", nil)
	statusResp := httptest.NewRecorder()
	server.mux.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("status response = %d, want %d body=%s", statusResp.Code, http.StatusOK, statusResp.Body.String())
	}
	var statusPayload map[string]any
	if err := json.Unmarshal(statusResp.Body.Bytes(), &statusPayload); err != nil {
		t.Fatalf("status json.Unmarshal returned error: %v", err)
	}
	if _, ok := statusPayload["proxy_auth_token"]; ok {
		t.Fatalf("recorder status exposed proxy_auth_token: %s", statusResp.Body.String())
	}

	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewBufferString(`{"name":"No Token Leak"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startResp := httptest.NewRecorder()
	server.mux.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusCreated {
		t.Fatalf("start response = %d, want %d body=%s", startResp.Code, http.StatusCreated, startResp.Body.String())
	}
	var startPayload map[string]any
	if err := json.Unmarshal(startResp.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("start json.Unmarshal returned error: %v", err)
	}
	if _, ok := startPayload["proxy_auth_token"]; ok {
		t.Fatalf("recorder start exposed proxy_auth_token: %s", startResp.Body.String())
	}
}

func TestDesktopRecorderStatusExposesOnlyRecorderProxyToken(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, nil)
	server := NewServer(ServerConfig{
		DesktopMode:       true,
		DesktopCapability: "desktop-api-capability",
		ProxyAuthToken:    "recorder-only-capability",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["proxy_auth_token"]; got != "recorder-only-capability" {
		t.Fatalf("proxy_auth_token = %v, want recorder-only capability", got)
	}
	if strings.Contains(resp.Body.String(), "desktop-api-capability") {
		t.Fatal("recorder response leaked the desktop API capability")
	}
	if got := resp.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestHandleExportRecordingHARScrubOption(t *testing.T) {
	server := NewServer(ServerConfig{})
	session, err := server.recorder.StartSession("rec-scrub", "Login HAR", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:    1,
		Method: http.MethodPost,
		URL:    "https://user:url-password@shop.example.com/login?access_token=query-secret",
		Path:   "/login",
		HeadersMulti: map[string][]string{
			"Authorization": {"Bearer top-secret-token"},
			"Content-Type":  {"application/json"},
		},
		Body:                 `{"password":"hunter2","user":"jason"}`,
		StatusCode:           http.StatusOK,
		ResponseHeadersMulti: map[string][]string{"Set-Cookie": {"sid=cookie-secret"}},
		ResponseBody:         `{"ok":true}`,
	})

	// Session/delta APIs are disclosure-safe by default too; they must not
	// provide an easier bypass around the scrubbed HAR export boundary.
	sessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-scrub?include=requests", nil)
	sessionResp := httptest.NewRecorder()
	server.mux.ServeHTTP(sessionResp, sessionReq)
	if sessionResp.Code != http.StatusOK {
		t.Fatalf("session status = %d body=%s", sessionResp.Code, sessionResp.Body.String())
	}
	if got := sessionResp.Header().Get("X-APEXLoad-Sensitivity"); got != scrub.SensitivityScrubbed {
		t.Fatalf("session sensitivity = %q, want %q", got, scrub.SensitivityScrubbed)
	}
	for _, secret := range []string{"url-password", "query-secret", "top-secret-token", "hunter2", "cookie-secret"} {
		if strings.Contains(sessionResp.Body.String(), secret) {
			t.Fatalf("scrubbed session still contains %q: %s", secret, sessionResp.Body.String())
		}
	}
	if !strings.Contains(sessionResp.Body.String(), scrub.Redacted) {
		t.Fatalf("scrubbed session has no redaction marker: %s", sessionResp.Body.String())
	}

	rawSessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-scrub?include=requests&raw=true", nil)
	rawSessionResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rawSessionResp, rawSessionReq)
	if rawSessionResp.Code != http.StatusOK || !strings.Contains(rawSessionResp.Body.String(), "hunter2") {
		t.Fatalf("explicit privileged raw session = %d body=%s", rawSessionResp.Code, rawSessionResp.Body.String())
	}
	if got := rawSessionResp.Header().Get("X-APEXLoad-Sensitivity"); got != scrub.SensitivityRaw {
		t.Fatalf("raw session sensitivity = %q, want %q", got, scrub.SensitivityRaw)
	}

	// Default export applies scrub-on-write for downloadable artifacts.
	scrubReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-scrub/export?format=har", nil)
	scrubResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scrubResp, scrubReq)
	if scrubResp.Code != http.StatusOK {
		t.Fatalf("scrub status = %d body=%s", scrubResp.Code, scrubResp.Body.String())
	}
	if got := scrubResp.Header().Get("X-APEXLoad-Sensitivity"); got != "scrubbed" {
		t.Errorf("scrubbed sensitivity header = %q", got)
	}
	body := scrubResp.Body.String()
	for _, secret := range []string{"top-secret-token", "hunter2", "cookie-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("scrubbed export still contains %q", secret)
		}
	}
	var payload struct {
		Log struct {
			Entries []json.RawMessage `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(scrubResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("scrubbed export is not valid JSON: %v", err)
	}
	if len(payload.Log.Entries) != 1 {
		t.Fatalf("scrubbed entry count = %d, want 1", len(payload.Log.Entries))
	}

	// Raw export is still available for privileged correlation diagnostics, but
	// must be explicit and labeled.
	rawReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-scrub/export?format=har&raw=true", nil)
	rawResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rawResp, rawReq)
	if rawResp.Code != http.StatusOK {
		t.Fatalf("raw status = %d body=%s", rawResp.Code, rawResp.Body.String())
	}
	if got := rawResp.Header().Get("X-APEXLoad-Sensitivity"); got != "raw_contains_secrets" {
		t.Errorf("raw sensitivity header = %q", got)
	}
	if !strings.Contains(rawResp.Body.String(), "top-secret-token") {
		t.Error("raw export should preserve values for correlation analysis")
	}
}

func TestHandleExportRecordingRawRequiresEditorRole(t *testing.T) {
	server := NewServer(ServerConfig{})
	enableTrustedHeaderAuth(t, server)
	session, err := server.recorder.StartSessionForProject("rec-rbac", "RBAC HAR", "proj-rbac", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:        1,
		Method:     http.MethodGet,
		URL:        "https://shop.example.com/account",
		Path:       "/account",
		StatusCode: http.StatusOK,
	})

	scrubbedReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-rbac/export", nil)
	scrubbedReq.Header.Set("X-Auth-Request-User", "viewer-user")
	scrubbedReq.Header.Set("X-APEXLoad-Project-Roles", "proj-rbac="+RoleViewer)
	scrubbedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(scrubbedResp, scrubbedReq)
	if scrubbedResp.Code != http.StatusOK {
		t.Fatalf("scrubbed viewer export status = %d, want 200 body=%s", scrubbedResp.Code, scrubbedResp.Body.String())
	}

	rawReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-rbac/export?raw=true", nil)
	rawReq.Header.Set("X-Auth-Request-User", "viewer-user")
	rawReq.Header.Set("X-APEXLoad-Project-Roles", "proj-rbac="+RoleViewer)
	rawResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rawResp, rawReq)
	if rawResp.Code != http.StatusForbidden {
		t.Fatalf("raw viewer export status = %d, want 403 body=%s", rawResp.Code, rawResp.Body.String())
	}

	rawSessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/rec-rbac?include=requests&raw=true", nil)
	rawSessionReq.Header.Set("X-Auth-Request-User", "viewer-user")
	rawSessionReq.Header.Set("X-APEXLoad-Project-Roles", "proj-rbac="+RoleViewer)
	rawSessionResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rawSessionResp, rawSessionReq)
	if rawSessionResp.Code != http.StatusForbidden {
		t.Fatalf("raw viewer session status = %d, want 403 body=%s", rawSessionResp.Code, rawSessionResp.Body.String())
	}
}
