package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/selector"
)

func newBrowserRecorderTestServer(t *testing.T, fake *fakeBrowserRecorderBackend) *Server {
	t.Helper()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0") // satisfy the headed-display availability probe in CI
	}
	server := NewServer(ServerConfig{})
	fake.install(server.browserRecording)
	return server
}

func stubBrowserRecordingDependencyProbe(t *testing.T, err error) {
	t.Helper()
	previous := browserRecordingDependencyProbe
	browserRecordingDependencyProbe = func() error { return err }
	t.Cleanup(func() { browserRecordingDependencyProbe = previous })
}

func startBrowserRecordingViaAPI(t *testing.T, server *Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions",
		bytes.NewBufferString(`{"name":"Checkout","start_url":"https://shop.example/login","project_id":"proj-1"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("start status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Session BrowserRecordingSession `json:"session"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal start response: %v", err)
	}
	return payload.Session.ID
}

func stopBrowserRecordingViaAPI(t *testing.T, server *Server, id string) BrowserRecordingSession {
	t.Helper()
	stopReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/stop", nil)
	stopResp := httptest.NewRecorder()
	server.mux.ServeHTTP(stopResp, stopReq)
	if stopResp.Code != http.StatusOK {
		t.Fatalf("stop status = %d body=%s", stopResp.Code, stopResp.Body.String())
	}
	var stopPayload struct {
		Session BrowserRecordingSession `json:"session"`
	}
	if err := json.Unmarshal(stopResp.Body.Bytes(), &stopPayload); err != nil {
		t.Fatal(err)
	}
	// Graceful cleanup is asynchronous but the in-memory fake can finish before
	// the handler snapshots its response. Both states satisfy the API contract:
	// stopping means cleanup is in flight; stopped means it already completed.
	if (stopPayload.Session.Status != BrowserRecordingStopping && stopPayload.Session.Status != BrowserRecordingStopped) || stopPayload.Session.StopReason != browserRecordingStopRequested {
		t.Fatalf("initial stop response = %+v, want stopping-or-stopped/requested", &stopPayload.Session)
	}
	return *waitForRecordingStatus(t, server.browserRecording, id, BrowserRecordingStopped)
}

func TestHandleStartBrowserRecording(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)

	id := startBrowserRecordingViaAPI(t, server)
	if id == "" {
		t.Fatal("start response missing session id")
	}
	if !fake.started {
		t.Error("backend not started")
	}

	// Second start conflicts while the first is live.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions",
		bytes.NewBufferString(`{"name":"Second","start_url":"https://shop.example/","project_id":"proj-1"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Errorf("conflicting start status = %d, want 409", resp.Code)
	}
}

func TestHandleStartBrowserRecordingRejectsNonHTTPSchemes(t *testing.T) {
	server := newBrowserRecorderTestServer(t, newFakeBrowserRecorder(nil))
	for _, startURL := range []string{"file:///etc/passwd", "javascript:alert(1)", "chrome://settings", "not a url", ""} {
		body, _ := json.Marshal(map[string]string{"name": "X", "start_url": startURL, "project_id": "proj-1"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions", bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("start_url %q status = %d, want 400", startURL, resp.Code)
		}
	}
}

func TestHandleStartBrowserRecordingRejectsStorageStateTraversal(t *testing.T) {
	server := newBrowserRecorderTestServer(t, newFakeBrowserRecorder(nil))
	for _, path := range []string{"/etc/passwd", "../../../etc/passwd", "../secrets.json"} {
		body, _ := json.Marshal(map[string]any{
			"name": "X", "start_url": "https://shop.example/", "project_id": "proj-1", "storage_state_path": path,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions", bytes.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("storage_state_path %q status = %d, want 400", path, resp.Code)
		}
	}
}

func TestHandleStartBrowserRecordingUnavailableFailsClosed(t *testing.T) {
	t.Setenv("APEXLOAD_BROWSER_RECORDER", "off")
	server := newBrowserRecorderTestServer(t, newFakeBrowserRecorder(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions",
		bytes.NewBufferString(`{"name":"X","start_url":"https://shop.example/","project_id":"proj-1"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 body=%s", resp.Code, resp.Body.String())
	}
}

func TestBrowserRecordingAvailabilityRules(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	server := NewServer(ServerConfig{})

	t.Run("default development mode is available", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "")
		t.Setenv("APEXLOAD_BROWSER_RECORDER", "")
		if got := server.browserRecordingAvailability(); !got.Available {
			t.Errorf("availability = %+v, want available", got)
		}
	})

	t.Run("production mode is off by default", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "production")
		t.Setenv("APEXLOAD_BROWSER_RECORDER", "")
		got := server.browserRecordingAvailability()
		if got.Available || got.Code != browserRecordingUnavailableProductionDisabled || !strings.Contains(got.Reason, "production") {
			t.Errorf("availability = %+v, want unavailable with production reason", got)
		}
	})

	t.Run("explicit opt-in overrides production", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "production")
		t.Setenv("APEXLOAD_BROWSER_RECORDER", "on")
		if got := server.browserRecordingAvailability(); !got.Available {
			t.Errorf("availability = %+v, want available", got)
		}
	})

	t.Run("desktop mode stays available in production posture", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "production")
		t.Setenv("APEXLOAD_BROWSER_RECORDER", "")
		desktop := NewServer(ServerConfig{DesktopMode: true})
		if got := desktop.browserRecordingAvailability(); !got.Available {
			t.Errorf("availability = %+v, want available in desktop mode", got)
		}
	})

	t.Run("kill switch wins everywhere", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "")
		t.Setenv("APEXLOAD_BROWSER_RECORDER", "off")
		if got := server.browserRecordingAvailability(); got.Available || got.Code != browserRecordingUnavailableDisabled {
			t.Errorf("availability = %+v, want unavailable", got)
		}
	})
}

func TestBrowserRecordingAvailabilityDetectsMissingChromium(t *testing.T) {
	stubBrowserRecordingDependencyProbe(t, errors.New("launch chromium: executable doesn't exist; please run: playwright install chromium"))
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	t.Setenv("APEXLOAD_ENV", "")
	t.Setenv("APEXLOAD_BROWSER_RECORDER", "")

	server := NewServer(ServerConfig{})
	got := server.browserRecordingAvailability()
	if got.Available || got.Code != browserRecordingUnavailableBrowsersNotInstalled {
		t.Fatalf("availability = %+v, want browsers_not_installed", got)
	}
}

func TestHandleStartBrowserRecordingClassifiesMissingBrowserInstall(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	fake.startErr = errors.New("launch chromium: executable doesn't exist; please run: playwright install chromium")
	server := newBrowserRecorderTestServer(t, fake)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions",
		bytes.NewBufferString(`{"name":"Checkout","start_url":"https://shop.example/login","project_id":"proj-1"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("start status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Error        string                       `json:"error"`
		Availability browserRecordingAvailability `json:"availability"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Availability.Code != browserRecordingUnavailableBrowsersNotInstalled || payload.Availability.Available {
		t.Fatalf("availability = %+v, want browsers_not_installed", payload.Availability)
	}
	if payload.Error == "" || !strings.Contains(strings.ToLower(payload.Error), "chromium") {
		t.Fatalf("error = %q, want install guidance", payload.Error)
	}
}

func TestHandleBrowserRecorderInstallProductionGate(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_BROWSER_RECORDER", "")
	server := newBrowserRecorderTestServer(t, newFakeBrowserRecorder(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/install", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("install status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Availability browserRecordingAvailability `json:"availability"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Availability.Code != browserRecordingUnavailableProductionDisabled {
		t.Fatalf("availability = %+v, want production_disabled", payload.Availability)
	}
	if got := server.browserInstall.snapshot().Status; got != "idle" {
		t.Fatalf("install status = %q, want idle", got)
	}
}

func TestHandleGetAndStopBrowserRecording(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/"+id, nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	var getPayload struct {
		Session BrowserRecordingSession `json:"session"`
	}
	if err := json.Unmarshal(getResp.Body.Bytes(), &getPayload); err != nil {
		t.Fatal(err)
	}
	if len(getPayload.Session.Steps) != 2 || getPayload.Session.StepCount != 2 {
		t.Errorf("get session steps = %d count=%d, want 2", len(getPayload.Session.Steps), getPayload.Session.StepCount)
	}

	stopped := stopBrowserRecordingViaAPI(t, server, id)
	if stopped.Status != BrowserRecordingStopped || stopped.StopReason != browserRecordingStopRequested {
		t.Errorf("stop session = %+v", &stopped)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions?project_id=proj-1", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listPayload struct {
		Count    int                       `json:"count"`
		Sessions []BrowserRecordingSession `json:"sessions"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listPayload); err != nil {
		t.Fatal(err)
	}
	if listPayload.Count != 1 || len(listPayload.Sessions) != 1 || listPayload.Sessions[0].ID != id {
		t.Fatalf("list payload = %+v, want stopped session", listPayload)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/browser-recorder/sessions/"+id, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleteResp.Code, deleteResp.Body.String())
	}

	deletedGet := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/"+id, nil)
	deletedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deletedResp, deletedGet)
	if deletedResp.Code != http.StatusNotFound {
		t.Fatalf("deleted get status = %d, want 404", deletedResp.Code)
	}

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/nope", nil)
	missingResp := httptest.NewRecorder()
	server.mux.ServeHTTP(missingResp, missing)
	if missingResp.Code != http.StatusNotFound {
		t.Errorf("missing session status = %d, want 404", missingResp.Code)
	}
}

func TestHandleGetBrowserRecordingScrubsViewerAndGatesRawView(t *testing.T) {
	server := NewServer(ServerConfig{})
	now := time.Now().UTC()
	artifact := &recbrowser.Session{
		ID:       "browser-sensitive",
		StartURL: "https://start-user:start-password@app.example/login?access_token=start-token",
		Steps: []recbrowser.Step{{
			Seq:       1,
			Kind:      recbrowser.StepFill,
			Value:     "short-password",
			URL:       "https://app.example/form?session_id=step-token",
			Thumbnail: "data:image/jpeg;base64,sensitive-screen-pixels",
			Element:   &selector.ElementMetadata{Name: "password", Type: "password"},
		}},
		NavEvents:   []recbrowser.NavEvent{{URL: "https://app.example/callback?token=navigation-token"}},
		Annotations: []recbrowser.Annotation{{Text: "authorization=annotation-token", URL: "https://app.example/error?api_key=annotation-key"}},
	}
	info := &BrowserRecordingSession{
		ID:        artifact.ID,
		Name:      "Sensitive",
		ProjectID: "project-a",
		Status:    BrowserRecordingStopped,
		StartURL:  artifact.StartURL,
		StartedAt: now,
		final:     artifact,
	}
	server.browserRecording.mu.Lock()
	server.browserRecording.sessions[info.ID] = info
	server.browserRecording.mu.Unlock()

	viewer := &AuthenticatedUser{
		GlobalRoles:  map[string]struct{}{},
		ProjectRoles: map[string]map[string]struct{}{"project-a": {RoleViewer: {}}},
	}
	request := func(target string, user *AuthenticatedUser) *http.Request {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.SetPathValue("id", info.ID)
		return withAuthenticatedContext(req, authResolution{User: user})
	}

	resp := httptest.NewRecorder()
	server.handleGetBrowserRecording(resp, request("/api/v1/browser-recorder/sessions/"+info.ID, viewer))
	if resp.Code != http.StatusOK {
		t.Fatalf("viewer session status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("X-APEXLoad-Sensitivity"); got != scrub.SensitivityScrubbed {
		t.Fatalf("viewer sensitivity = %q, want %q", got, scrub.SensitivityScrubbed)
	}
	for _, leaked := range []string{"start-password", "start-token", "short-password", "step-token", "sensitive-screen-pixels", "navigation-token", "annotation-token", "annotation-key"} {
		if strings.Contains(resp.Body.String(), leaked) {
			t.Fatalf("viewer session leaked %q: %s", leaked, resp.Body.String())
		}
	}
	if !strings.Contains(resp.Body.String(), scrub.Redacted) {
		t.Fatalf("viewer response has no redaction marker: %s", resp.Body.String())
	}

	denied := httptest.NewRecorder()
	server.handleGetBrowserRecording(denied, request("/api/v1/browser-recorder/sessions/"+info.ID+"?raw=true", viewer))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer raw session status = %d, want 403 body=%s", denied.Code, denied.Body.String())
	}

	editor := &AuthenticatedUser{GlobalRoles: map[string]struct{}{RoleEditor: {}}, ProjectRoles: map[string]map[string]struct{}{}}
	raw := httptest.NewRecorder()
	server.handleGetBrowserRecording(raw, request("/api/v1/browser-recorder/sessions/"+info.ID+"?raw=true", editor))
	if raw.Code != http.StatusOK || !strings.Contains(raw.Body.String(), "short-password") {
		t.Fatalf("editor raw session status/body = %d %s", raw.Code, raw.Body.String())
	}
}

func TestHandlePreviewBrowserRecordingConversion(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert/preview",
		bytes.NewBufferString(`{"name":"Checkout Preview"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Script struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
			Actions  []struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"actions"`
		} `json:"script"`
		Warnings         []string `json:"warnings"`
		Parameterization *struct {
			Suggestions []map[string]any `json:"suggestions"`
		} `json:"parameterization"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Script.Name != "Checkout Preview" || payload.Script.Protocol != "browser" {
		t.Errorf("script = %+v", payload.Script)
	}
	if payload.Parameterization == nil {
		t.Error("preview response missing parameterization envelope")
	}

	// The masked password fill converts to an env placeholder, never a value.
	foundMaskedFill := false
	for _, action := range payload.Script.Actions {
		if action.Type == "browser_fill" && strings.HasPrefix(action.Value, "{{env.") {
			foundMaskedFill = true
		}
	}
	if !foundMaskedFill {
		t.Errorf("actions = %+v, want masked fill with env placeholder", payload.Script.Actions)
	}

	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("preview stored %d scripts, want 0", got)
	}
}

func TestHandlePreviewBrowserRecordingConversionReturnsAllScriptsWithoutStoring(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-05-12T20:00:00Z","time":12,"request":{"method":"GET","url":"https://shop.example/api/cart","httpVersion":"HTTP/1.1","headers":[],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"cookies":[],"content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},"cache":{},"timings":{"send":1,"wait":10,"receive":1}}]}}`)
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert/preview",
		bytes.NewBufferString(`{"name":"Checkout Preview","also_create_http_script":true}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Scripts []struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
		} `json:"scripts"`
		HTTPScript *struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
		} `json:"http_script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Scripts) != 2 {
		t.Fatalf("scripts = %+v, want browser + HTTP script", payload.Scripts)
	}
	if payload.Scripts[0].Protocol != "browser" || payload.Scripts[1].Protocol != "http" {
		t.Errorf("scripts = %+v, want browser then http", payload.Scripts)
	}
	if payload.HTTPScript == nil || payload.HTTPScript.Protocol != "http" {
		t.Errorf("http_script = %+v, want HTTP script preview", payload.HTTPScript)
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("preview stored %d scripts, want 0", got)
	}
}

func TestHandleConvertBrowserRecordingRequiresStop(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("convert-while-recording status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
}

func TestHandleConvertBrowserRecordingStoresScript(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout Saved"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d body=%s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Script struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Script struct {
				ProjectID string `json:"project_id"`
			} `json:"script"`
		} `json:"script"`
		Parameterization any `json:"parameterization"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Script.ID == "" || payload.Script.Name != "Checkout Saved" {
		t.Errorf("stored script = %+v", payload.Script)
	}
	if payload.Script.Script.ProjectID != "proj-1" {
		t.Errorf("stored script project = %q, want proj-1", payload.Script.Script.ProjectID)
	}
	if payload.Parameterization == nil {
		t.Error("convert response missing parameterization envelope")
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("stored scripts = %d, want 1", got)
	}
}

func TestHandleConvertBrowserRecordingStoresOnlyGovernedSecretReferences(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-05-12T20:00:00Z","time":12,"request":{"method":"GET","url":"https://shop.example/api/cart","httpVersion":"HTTP/1.1","headers":[{"name":"Authorization","value":"Bearer browser-recorded-token"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"cookies":[],"content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},"cache":{},"timings":{"send":1,"wait":10,"receive":1}}]}}`)
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout Saved","also_create_http_script":true}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d body=%s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "browser-recorded-token") {
		t.Fatalf("convert response disclosed recorded credential: %s", resp.Body.String())
	}
	var payload struct {
		SecretBindingRequirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.SecretBindingRequirements) == 0 {
		t.Fatalf("convert requirements = %#v", payload.SecretBindingRequirements)
	}
	var httpScript *StoredScript
	for _, stored := range server.runner.ListScripts() {
		if stored != nil && stored.Script != nil && stored.Script.Protocol == "http" {
			httpScript = stored
			break
		}
	}
	if httpScript == nil {
		t.Fatal("converted HTTP script was not stored")
	}
	storedJSON, err := json.Marshal(httpScript.Script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedJSON), "browser-recorded-token") || strings.Contains(string(storedJSON), scrub.Redacted) {
		t.Fatalf("stored HTTP script contains credential material or sentinel: %s", storedJSON)
	}
	if !strings.Contains(string(storedJSON), "{{secret.") {
		t.Fatalf("stored HTTP script has no governed secret reference: %s", storedJSON)
	}
}

func TestHandleExportBrowserRecordingHAR(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/"+id+"/export?format=har", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("X-APEXLoad-Sensitivity"); got != scrub.SensitivityScrubbed {
		t.Errorf("default sensitivity = %q, want %q", got, scrub.SensitivityScrubbed)
	}
	if disposition := resp.Header().Get("Content-Disposition"); !strings.Contains(disposition, ".har") {
		t.Errorf("content-disposition = %q", disposition)
	}

	rawReq := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/"+id+"/export?raw=true", nil)
	rawResp := httptest.NewRecorder()
	server.mux.ServeHTTP(rawResp, rawReq)
	if rawResp.Code != http.StatusOK {
		t.Fatalf("raw export status = %d body=%s", rawResp.Code, rawResp.Body.String())
	}
	if got := rawResp.Header().Get("X-APEXLoad-Sensitivity"); got != scrub.SensitivityRaw {
		t.Errorf("raw sensitivity = %q, want %q", got, scrub.SensitivityRaw)
	}
}

func TestHandleExportBrowserRecordingWithoutHAR(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.HAR = nil
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/"+id+"/export", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Errorf("export-without-HAR status = %d, want 409", resp.Code)
	}
}

func postBrowserRecorderControl(t *testing.T, server *Server, id, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Buffer
	if body == "" {
		reader = bytes.NewBufferString("{}")
	} else {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/"+action, reader)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	return resp
}

func TestHandlePauseResumeBrowserRecording(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	pauseResp := postBrowserRecorderControl(t, server, id, "pause", "")
	if pauseResp.Code != http.StatusOK {
		t.Fatalf("pause status = %d body=%s", pauseResp.Code, pauseResp.Body.String())
	}
	var payload struct {
		Session BrowserRecordingSession `json:"session"`
	}
	if err := json.Unmarshal(pauseResp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session.Status != BrowserRecordingPaused {
		t.Errorf("status after pause = %q", payload.Session.Status)
	}

	resumeResp := postBrowserRecorderControl(t, server, id, "resume", "")
	if resumeResp.Code != http.StatusOK {
		t.Fatalf("resume status = %d body=%s", resumeResp.Code, resumeResp.Body.String())
	}
	if err := json.Unmarshal(resumeResp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session.Status != BrowserRecordingActive {
		t.Errorf("status after resume = %q", payload.Session.Status)
	}

	// Controls 409 once the session is stopped.
	postBrowserRecorderControl(t, server, id, "stop", "")
	conflict := postBrowserRecorderControl(t, server, id, "pause", "")
	if conflict.Code != http.StatusConflict {
		t.Errorf("pause-after-stop status = %d, want 409", conflict.Code)
	}
}

func TestHandleForceStopBrowserRecording(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.stopStarted = make(chan struct{})
	fake.stopRelease = make(chan struct{})
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	stopResp := postBrowserRecorderControl(t, server, id, "stop", "")
	if stopResp.Code != http.StatusOK {
		t.Fatalf("stop status = %d body=%s", stopResp.Code, stopResp.Body.String())
	}
	select {
	case <-fake.stopStarted:
	case <-time.After(time.Second):
		t.Fatal("backend Stop was not started")
	}

	forceResp := postBrowserRecorderControl(t, server, id, "force-stop", "")
	if forceResp.Code != http.StatusOK {
		t.Fatalf("force-stop status = %d body=%s", forceResp.Code, forceResp.Body.String())
	}
	var payload struct {
		Session BrowserRecordingSession `json:"session"`
	}
	if err := json.Unmarshal(forceResp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session.Status != BrowserRecordingStopped || payload.Session.StopReason != browserRecordingStopForced {
		t.Fatalf("force-stop session = %+v", &payload.Session)
	}
	if payload.Session.StepCount != 2 {
		t.Errorf("force-stop StepCount = %d, want captured steps preserved", payload.Session.StepCount)
	}
	if !strings.Contains(strings.Join(payload.Session.Warnings, "\n"), "force-finished") {
		t.Errorf("force-stop warnings = %v, want force-finished warning", payload.Session.Warnings)
	}
	close(fake.stopRelease)
}

func TestHandleForceStopBrowserRecordingRequiresStopping(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	resp := postBrowserRecorderControl(t, server, id, "force-stop", "")
	if resp.Code != http.StatusConflict {
		t.Fatalf("force-stop live session status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
}

func TestHandleNavigateBrowserRecording(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	good := postBrowserRecorderControl(t, server, id, "navigate", `{"url":"https://shop.example/cart"}`)
	if good.Code != http.StatusOK {
		t.Fatalf("navigate status = %d body=%s", good.Code, good.Body.String())
	}
	if len(fake.navigatedTo) != 1 || fake.navigatedTo[0] != "https://shop.example/cart" {
		t.Errorf("navigatedTo = %v", fake.navigatedTo)
	}

	for _, url := range []string{"file:///etc/passwd", "javascript:alert(1)", ""} {
		bad := postBrowserRecorderControl(t, server, id, "navigate", `{"url":"`+url+`"}`)
		if bad.Code != http.StatusBadRequest {
			t.Errorf("navigate %q status = %d, want 400", url, bad.Code)
		}
	}
	if len(fake.navigatedTo) != 1 {
		t.Errorf("blocked navigations reached the backend: %v", fake.navigatedTo)
	}
}

func TestHandleFocusBrowserRecording(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	resp := postBrowserRecorderControl(t, server, id, "focus", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("focus status = %d body=%s", resp.Code, resp.Body.String())
	}
	if fake.focusCalls != 1 {
		t.Errorf("focus calls = %d", fake.focusCalls)
	}

	missing := postBrowserRecorderControl(t, server, "nope", "focus", "")
	if missing.Code != http.StatusNotFound {
		t.Errorf("focus unknown session status = %d, want 404", missing.Code)
	}
}

func TestHandleOpenBrowserRecordingControlPanel(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	body := `{
		"url":"http://127.0.0.1:8080/#/recorder-panel?session=` + id + `",
		"bridge_connection":{
			"origin":"http://127.0.0.1:27777/",
			"token":"bridge-token",
			"session_id":"bridge-session",
			"paired_at":"2026-06-15T12:00:00Z"
		}
	}`
	resp := postBrowserRecorderControl(t, server, id, "control-panel", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("control-panel status = %d body=%s", resp.Code, resp.Body.String())
	}
	if len(fake.panelURLs) != 1 || !strings.Contains(fake.panelURLs[0], "#/recorder-panel?session="+id) {
		t.Fatalf("panelURLs = %v", fake.panelURLs)
	}
	key := recbrowser.BrowserRecorderBridgeStoragePrefix + "http://127.0.0.1:8080"
	seed := fake.panelStorage[key]
	if !strings.Contains(seed, `"origin":"http://127.0.0.1:27777"`) || !strings.Contains(seed, `"session_id":"bridge-session"`) {
		t.Errorf("bridge seed = %q", seed)
	}

	mismatchReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/control-panel",
		bytes.NewBufferString(`{"url":"http://127.0.0.1:8080/#/recorder-panel?session=`+id+`"}`))
	mismatchReq.Header.Set("Origin", "http://127.0.0.1:3000")
	mismatchResp := httptest.NewRecorder()
	server.mux.ServeHTTP(mismatchResp, mismatchReq)
	if mismatchResp.Code != http.StatusForbidden {
		t.Errorf("mismatched origin status = %d, want 403 body=%s", mismatchResp.Code, mismatchResp.Body.String())
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "non-panel route", body: `{"url":"http://127.0.0.1:8080/#/scripts"}`},
		{name: "non-http scheme", body: `{"url":"file:///tmp/panel.html#/recorder-panel"}`},
		{name: "bad bridge origin", body: `{"url":"http://127.0.0.1:8080/#/recorder-panel","bridge_connection":{"origin":"file:///tmp","token":"x","session_id":"y"}}`},
	} {
		bad := postBrowserRecorderControl(t, server, id, "control-panel", tc.body)
		if bad.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400 body=%s", tc.name, bad.Code, bad.Body.String())
		}
	}
	if len(fake.panelURLs) != 1 {
		t.Errorf("blocked control-panel requests reached backend: %v", fake.panelURLs)
	}
}

func TestHandleBrowserRecordingTransactionsAndNotes(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	start := postBrowserRecorderControl(t, server, id, "transactions/start", `{"name":"Checkout"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("transaction start status = %d body=%s", start.Code, start.Body.String())
	}
	var payload struct {
		Session BrowserRecordingSession `json:"session"`
		Marker  struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"marker"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Marker.Kind != "transaction_start" || payload.Marker.Name != "Checkout" {
		t.Errorf("marker = %+v", payload.Marker)
	}
	if len(payload.Session.OpenTransactions) != 1 {
		t.Errorf("open transactions = %v", payload.Session.OpenTransactions)
	}

	dup := postBrowserRecorderControl(t, server, id, "transactions/start", `{"name":"Checkout"}`)
	if dup.Code != http.StatusBadRequest {
		t.Errorf("duplicate transaction start status = %d, want 400", dup.Code)
	}

	note := postBrowserRecorderControl(t, server, id, "notes", `{"text":"watch the cart badge"}`)
	if note.Code != http.StatusOK {
		t.Fatalf("note status = %d body=%s", note.Code, note.Body.String())
	}
	emptyNote := postBrowserRecorderControl(t, server, id, "notes", `{"text":"  "}`)
	if emptyNote.Code != http.StatusBadRequest {
		t.Errorf("empty note status = %d, want 400", emptyNote.Code)
	}

	end := postBrowserRecorderControl(t, server, id, "transactions/end", `{}`)
	if end.Code != http.StatusOK {
		t.Fatalf("transaction end status = %d body=%s", end.Code, end.Body.String())
	}
	var endPayload struct {
		Session BrowserRecordingSession `json:"session"`
		Marker  struct {
			Name string `json:"name"`
		} `json:"marker"`
	}
	if err := json.Unmarshal(end.Body.Bytes(), &endPayload); err != nil {
		t.Fatal(err)
	}
	if endPayload.Marker.Name != "Checkout" {
		t.Errorf("default end closed %q, want Checkout", endPayload.Marker.Name)
	}
	if len(endPayload.Session.OpenTransactions) != 0 {
		t.Errorf("open transactions after end = %v", endPayload.Session.OpenTransactions)
	}

	ghostEnd := postBrowserRecorderControl(t, server, id, "transactions/end", `{"name":"Ghost"}`)
	if ghostEnd.Code != http.StatusBadRequest {
		t.Errorf("unmatched transaction end status = %d, want 400", ghostEnd.Code)
	}
}

func TestHandleSetBrowserRecordingSection(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	resp := postBrowserRecorderControl(t, server, id, "section", `{"section":"teardown"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("section status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Session BrowserRecordingSession `json:"session"`
	}
	json.Unmarshal(resp.Body.Bytes(), &payload)
	if payload.Session.Section != "teardown" {
		t.Errorf("section = %q, want teardown", payload.Session.Section)
	}
}

func TestHandleSetBrowserRecordingPickMode(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	resp := postBrowserRecorderControl(t, server, id, "pick-mode", `{"on":true}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("pick-mode status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !fake.assertMode {
		t.Error("assert mode not enabled on backend")
	}
}

func TestHandleBrowserRecordingStepMutations(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	// Exclude step 2 via PATCH.
	patch := func(seq, body string) int {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/browser-recorder/sessions/"+id+"/steps/"+seq, bytes.NewBufferString(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp.Code
	}
	if code := patch("2", `{"excluded":true}`); code != http.StatusOK {
		t.Errorf("exclude step status = %d", code)
	}
	if !fake.session.Steps[1].Excluded {
		t.Error("step 2 not excluded on backend")
	}
	// Mark step 1 (a fill) as a secret.
	if code := patch("1", `{"mark_secret":true}`); code != http.StatusOK {
		t.Errorf("mark step status = %d", code)
	}
	if !fake.session.Steps[0].MarkSecret {
		t.Error("step 1 not marked secret")
	}
	if code := patch("1", `{"mark_param":true}`); code != http.StatusOK {
		t.Errorf("mark-param step status = %d", code)
	}
	if !fake.session.Steps[0].MarkSecret || !fake.session.Steps[0].MarkParam {
		t.Errorf("partial mark patch clobbered step marks: secret=%v param=%v", fake.session.Steps[0].MarkSecret, fake.session.Steps[0].MarkParam)
	}
	// Delete last step.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/browser-recorder/sessions/"+id+"/steps/last", nil)
	delResp := httptest.NewRecorder()
	server.mux.ServeHTTP(delResp, delReq)
	if delResp.Code != http.StatusOK {
		t.Errorf("delete-last status = %d body=%s", delResp.Code, delResp.Body.String())
	}
}

func TestHandleSuggestBrowserRecordingTransactions(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.NavEvents = []recbrowser.NavEvent{{URL: "https://shop.example/cart"}}
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/"+id+"/transactions/suggestions", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("suggestions status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Suggestions []recbrowser.TransactionSuggestion `json:"suggestions"`
	}
	json.Unmarshal(resp.Body.Bytes(), &payload)
	if len(payload.Suggestions) == 0 || payload.Suggestions[0].Name != "Cart" {
		t.Errorf("suggestions = %+v", payload.Suggestions)
	}
}

func TestHandleListBrowserRecorderDevices(t *testing.T) {
	server := newBrowserRecorderTestServer(t, newFakeBrowserRecorder(nil))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/devices", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("devices status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Devices []map[string]any `json:"devices"`
	}
	json.Unmarshal(resp.Body.Bytes(), &payload)
	if len(payload.Devices) == 0 {
		t.Error("no devices returned")
	}
}

func TestHandleConvertBrowserRecordingDualHTTP(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-05-12T20:00:00Z","time":12,"request":{"method":"GET","url":"https://shop.example/api/cart","httpVersion":"HTTP/1.1","headers":[],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"cookies":[],"content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},"cache":{},"timings":{"send":1,"wait":10,"receive":1}}]}}`)
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout","also_create_http_script":true}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("dual convert status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		HTTPScript *struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		} `json:"http_script"`
	}
	json.Unmarshal(resp.Body.Bytes(), &payload)
	if payload.HTTPScript == nil || payload.HTTPScript.Protocol != "http" {
		t.Errorf("http_script = %+v, want an HTTP script", payload.HTTPScript)
	}
	if got := len(server.runner.ListScripts()); got != 2 {
		t.Errorf("stored scripts = %d, want 2 (browser + HTTP)", got)
	}
}

func TestHandleConvertBrowserRecordingStampsProvenance(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(`{"name":"Checkout"}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d", resp.Code)
	}
	stored := server.runner.ListScripts()
	if len(stored) != 1 {
		t.Fatalf("scripts = %d", len(stored))
	}
	meta := stored[0].Script.Metadata
	if meta == nil || meta.Import == nil || meta.Import.Source != "browser_recorder" {
		t.Fatalf("provenance metadata = %+v", meta)
	}
	if meta.Import.Preserved["recorder_session_id"] == nil {
		t.Errorf("missing recorder_session_id provenance: %+v", meta.Import.Preserved)
	}
}

func TestHandleVerifyBrowserRecordingRequiresStop(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	// While still recording, verify is a 409.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/verify", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Errorf("verify-while-recording status = %d, want 409", resp.Code)
	}
}

func TestHandleExportBrowserRecordingStorageState(t *testing.T) {
	exportDir := t.TempDir()
	t.Setenv("APEXLOAD_EXPORT_DIR", exportDir) // confine the persisted copy to a temp dir
	oldDir := filepath.Join(exportDir, browserStorageStateDir, "proj-1")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(oldDir, "old-state.json")
	if err := os.WriteFile(oldPath, []byte(`{"cookies":[{"value":"stale"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-defaultBrowserRecordingRetention - time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	artifact := recordedBrowserArtifact()
	artifact.StorageState = []byte(`{"cookies":[{"name":"sid","value":"cookie-secret","domain":"shop.example","path":"/"}],"origins":[{"origin":"https://shop.example","localStorage":[{"name":"access_token","value":"local-secret"},{"name":"theme","value":"dark"}]}]}`)
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browser-recorder/sessions/"+id+"/storage-state", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("storage-state status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("X-APEXLoad-Sensitivity"); got != scrub.SensitivityScrubbed {
		t.Errorf("sensitivity = %q, want %q", got, scrub.SensitivityScrubbed)
	}
	storagePath := filepath.ToSlash(resp.Header().Get("X-APEXLoad-StorageState-Path"))
	if !strings.HasPrefix(storagePath, browserStorageStateDir+"/proj-1/checkout-") || !strings.HasSuffix(storagePath, "-state.json") {
		t.Fatalf("storage state path = %q, want project/session scoped path", storagePath)
	}
	body := resp.Body.String()
	for _, leaked := range []string{"cookie-secret", "local-secret"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("storage state response leaked %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, scrub.Redacted) || !strings.Contains(body, `"name":"theme"`) || !strings.Contains(body, `"value":"dark"`) {
		t.Errorf("storage state body = %s, want redacted secrets and non-sensitive localStorage preserved", body)
	}
	persisted, err := os.ReadFile(filepath.Join(exportDir, filepath.FromSlash(storagePath)))
	if err != nil {
		t.Fatalf("read persisted storage state: %v", err)
	}
	// The confined reuse copy MUST retain the raw storage state so a later
	// recording can actually restore the session (login reuse) — seeding a
	// Playwright context with scrubbed [REDACTED] cookies silently breaks it.
	// This copy is read only server-side and is never served to a client; the
	// download response above is the scrubbed view.
	if !strings.Contains(string(persisted), "cookie-secret") || !strings.Contains(string(persisted), "local-secret") {
		t.Fatalf("persisted reuse copy must retain raw cookies for login reuse, got: %s", string(persisted))
	}
	info, err := os.Stat(filepath.Join(exportDir, filepath.FromSlash(storagePath)))
	if err != nil {
		t.Fatalf("stat persisted storage state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("persisted storage state mode = %o, want 600", got)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old storage state err = %v, want pruned", err)
	}
	confined, err := confineStorageStatePath(storagePath, "proj-1")
	if err != nil {
		t.Fatalf("exported storage path should be reusable within its own project: %v", err)
	}
	if confined != filepath.Join(exportDir, filepath.FromSlash(storagePath)) {
		t.Errorf("confined path = %q, want exported path", confined)
	}
	// A different project must not be able to reuse proj-1's raw storage state.
	if _, err := confineStorageStatePath(storagePath, "proj-2"); err == nil {
		t.Errorf("storage_state_path from another project must be rejected, got no error")
	}
}

func TestRecorderStatusIncludesBrowserAvailability(t *testing.T) {
	fake := newFakeBrowserRecorder(nil)
	server := newBrowserRecorderTestServer(t, fake)
	startBrowserRecordingViaAPI(t, server)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/status", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Browser struct {
			Availability browserRecordingAvailability `json:"availability"`
			Active       *BrowserRecordingSession     `json:"active"`
		} `json:"browser"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Browser.Availability.Available {
		t.Errorf("availability = %+v, want available", payload.Browser.Availability)
	}
	if payload.Browser.Active == nil || payload.Browser.Active.Name != "Checkout" {
		t.Errorf("active = %+v, want the live recording summary", payload.Browser.Active)
	}
}
