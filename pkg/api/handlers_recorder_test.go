package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/recorder"
	"github.com/jasonczech/stormrunner/pkg/storage"
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
		Scripts []json.RawMessage `json:"scripts"`
		Script  struct {
			Name    string `json:"name"`
			Actions []struct {
				Name   string `json:"name"`
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"script"`
		Warnings     []string          `json:"warnings"`
		Correlations []json.RawMessage `json:"correlations"`
		Stats        struct {
			ConvertedActions     int `json:"converted_actions"`
			CorrelationsDetected int `json:"correlations_detected"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts count = %d, want 1", len(result.Scripts))
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
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", result.Warnings)
	}
	if len(result.Correlations) != 0 {
		t.Fatalf("correlations = %v, want none", result.Correlations)
	}
	if result.Stats.ConvertedActions != 1 || result.Stats.CorrelationsDetected != 0 {
		t.Fatalf("stats = %#v, want 1 converted action and 0 correlations", result.Stats)
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("preview stored %d scripts, want 0", got)
	}
}

func TestHandleConvertRecordingReturnsStandardizedEnvelope(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, Addr: ":8080"})
	session, err := server.recorder.StartSession("rec-3", "Checkout Convert", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.ProjectID = "project-recorder-convert"
	session.BaseURL = "https://shop.example.com"
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq:        1,
		Method:     http.MethodGet,
		URL:        "https://shop.example.com/api/cart",
		Path:       "/api/cart",
		StatusCode: http.StatusOK,
		StepName:   "Load Cart",
	})

	body := bytes.NewBufferString(`{"name":"Checkout Convert"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/rec-3/convert", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}

	var result struct {
		Scripts []json.RawMessage `json:"scripts"`
		Script  struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"script"`
		Warnings []string `json:"warnings"`
		Stats    struct {
			ConvertedActions     int `json:"converted_actions"`
			CorrelationsDetected int `json:"correlations_detected"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts count = %d, want 1", len(result.Scripts))
	}
	if result.Script.ID == "" || result.Script.Name != "Checkout Convert" {
		t.Fatalf("script = %#v, want stored script with name", result.Script)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", result.Warnings)
	}
	if result.Stats.ConvertedActions != 1 || result.Stats.CorrelationsDetected != 0 {
		t.Fatalf("stats = %#v, want 1 converted action and 0 correlations", result.Stats)
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("stored scripts = %d, want 1", got)
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 10, ProjectID: "project-recorder-convert"})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 || events[0].Action != "recorder.session.convert" {
		t.Fatalf("audit events = %#v, want recorder.session.convert", events)
	}
}

func TestHandleRecordingLifecycleWritesAuditEvents(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, Addr: ":8080"})
	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/start", bytes.NewBufferString(`{"name":"Checkout Recording","project_id":"project-recorder"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startResp := httptest.NewRecorder()
	server.mux.ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusCreated {
		t.Fatalf("start status = %d, want %d body=%s", startResp.Code, http.StatusCreated, startResp.Body.String())
	}

	var startPayload struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("json.Unmarshal start returned error: %v", err)
	}
	if startPayload.Session.ID == "" {
		t.Fatal("expected session id in start payload")
	}

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/"+startPayload.Session.ID+"/pause", nil)
	pauseResp := httptest.NewRecorder()
	server.mux.ServeHTTP(pauseResp, pauseReq)
	if pauseResp.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want %d body=%s", pauseResp.Code, http.StatusOK, pauseResp.Body.String())
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/"+startPayload.Session.ID+"/resume", nil)
	resumeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resumeResp, resumeReq)
	if resumeResp.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want %d body=%s", resumeResp.Code, http.StatusOK, resumeResp.Body.String())
	}

	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/recorder/"+startPayload.Session.ID+"/stop", nil)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want %d body=%s", stopResp.Code, http.StatusOK, stopResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/recorder/"+startPayload.Session.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Limit: 10, ProjectID: "project-recorder"})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("audit event count = %d, want 5", len(events))
	}
	if events[0].Action != "recorder.session.delete" || events[1].Action != "recorder.session.stop" || events[2].Action != "recorder.session.resume" || events[3].Action != "recorder.session.pause" || events[4].Action != "recorder.session.start" {
		t.Fatalf("audit actions = [%s %s %s %s %s], want delete/stop/resume/pause/start", events[0].Action, events[1].Action, events[2].Action, events[3].Action, events[4].Action)
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

func TestHandleRecorderStatusFiltersActiveSessionByProject(t *testing.T) {
	server := NewServer(ServerConfig{})
	_, err := server.recorder.StartSessionForProject("rec-1", "Checkout Recording", "project-1", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatalf("StartSessionForProject returned error: %v", err)
	}

	matchingReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status?project_id=project-1", nil)
	matchingResp := httptest.NewRecorder()
	server.handleRecorderStatus(matchingResp, matchingReq)
	if matchingResp.Code != http.StatusOK {
		t.Fatalf("matching status = %d, want %d body=%s", matchingResp.Code, http.StatusOK, matchingResp.Body.String())
	}
	var matchingPayload struct {
		Active *struct {
			ID string `json:"id"`
		} `json:"active"`
	}
	if err := json.Unmarshal(matchingResp.Body.Bytes(), &matchingPayload); err != nil {
		t.Fatalf("json.Unmarshal matching payload returned error: %v", err)
	}
	if matchingPayload.Active == nil || matchingPayload.Active.ID != "rec-1" {
		t.Fatalf("matching active = %#v, want rec-1", matchingPayload.Active)
	}

	foreignReq := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status?project_id=project-2", nil)
	foreignResp := httptest.NewRecorder()
	server.handleRecorderStatus(foreignResp, foreignReq)
	if foreignResp.Code != http.StatusOK {
		t.Fatalf("foreign status = %d, want %d body=%s", foreignResp.Code, http.StatusOK, foreignResp.Body.String())
	}
	var foreignPayload struct {
		Active any `json:"active"`
	}
	if err := json.Unmarshal(foreignResp.Body.Bytes(), &foreignPayload); err != nil {
		t.Fatalf("json.Unmarshal foreign payload returned error: %v", err)
	}
	if foreignPayload.Active != nil {
		t.Fatalf("foreign active = %#v, want nil", foreignPayload.Active)
	}
}
