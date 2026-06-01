package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
