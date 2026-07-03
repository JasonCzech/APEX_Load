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

	"github.com/jasonczech/apexload/pkg/recorder"
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
				Name   string `json:"name"`
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"script"`
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
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("preview stored %d scripts, want 0", got)
	}
}

func TestHandleConvertRecordingPersistsBaseline(t *testing.T) {
	server := recordingTestServer(t)
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
	if obs.ID != 0 || obs.RequestURL != "https://shop.example.com/api/login" {
		t.Fatalf("observation = %+v, want first included request at id 0", obs)
	}
	if strings.Contains(obs.RequestBody, "hunter2") || strings.Contains(obs.RequestHeaders["Authorization"], "recorded-secret") {
		t.Fatalf("request-side secrets were not redacted: body=%q headers=%v", obs.RequestBody, obs.RequestHeaders)
	}
	if !strings.Contains(obs.ResponseBody, "csrf-recorded") {
		t.Fatalf("response body = %q, want dynamic response values preserved", obs.ResponseBody)
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

func TestHandleExportRecordingHARScrubOption(t *testing.T) {
	server := NewServer(ServerConfig{})
	session, err := server.recorder.StartSession("rec-scrub", "Login HAR", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:    1,
		Method: http.MethodPost,
		URL:    "https://shop.example.com/login",
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
}
