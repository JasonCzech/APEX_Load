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
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/dsl"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/selector"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newBrowserRecorderTestServer(t *testing.T, fake *fakeBrowserRecorderBackend) *Server {
	return newBrowserRecorderTestServerWithStore(t, fake, nil)
}

func newBrowserRecorderTestServerWithStore(t *testing.T, fake *fakeBrowserRecorderBackend, store storage.Store) *Server {
	t.Helper()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0") // satisfy the headed-display availability probe in CI
	}
	server := NewServer(ServerConfig{Store: store})
	fake.install(server.browserRecording)
	return server
}

func newHABrowserRecorderTestServerWithStore(t *testing.T, fake *fakeBrowserRecorderBackend, store storage.Store) *Server {
	t.Helper()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	server := NewServer(ServerConfig{Store: store, HAExecutionLeadership: true})
	fake.install(server.browserRecording)
	return server
}

type failOnceBrowserRecordingBaselineStore struct {
	*storage.SQLiteStore
	fail bool
}

type failOnceBrowserRecordingBaselineReadStore struct {
	*storage.SQLiteStore
	fail bool
}

type browserRecordingLocatorCountingStore struct {
	*storage.SQLiteStore
	listScripts int
	getScripts  int
}

type failOnceBrowserRecordingLocatorStore struct {
	*storage.SQLiteStore
	fail bool
}

type failOnceBrowserRecordingPublicationStore struct {
	*storage.SQLiteStore
	mu               sync.Mutex
	failComplete     bool
	failPublish      bool
	failClaimCleanup bool
}

type browserRecordingRollbackTakeoverStore struct {
	*storage.SQLiteStore
	renewStarted  chan struct{}
	renewContinue chan struct{}
	renewOnce     sync.Once
}

func (s *failOnceBrowserRecordingLocatorStore) CompareAndSwapSetting(key, expected, value string) (bool, error) {
	if s.fail && strings.HasPrefix(key, browserRecordingSaveLocatorKeyPrefix) && expected == "" {
		s.fail = false
		return false, errors.New("injected browser recording locator failure")
	}
	return s.SQLiteStore.CompareAndSwapSetting(key, expected, value)
}

func (s *failOnceBrowserRecordingPublicationStore) CompleteDirectImportReceipt(
	id, ownerToken string,
	scriptIDs []string,
	scenarioID string,
	now time.Time,
) (bool, error) {
	s.mu.Lock()
	if s.failComplete {
		s.failComplete = false
		s.mu.Unlock()
		return false, errors.New("injected browser receipt completion failure")
	}
	s.mu.Unlock()
	return s.SQLiteStore.CompleteDirectImportReceipt(id, ownerToken, scriptIDs, scenarioID, now)
}

func (s *browserRecordingRollbackTakeoverStore) RenewDirectImportReceipt(
	id, ownerToken string,
	now time.Time,
	lease time.Duration,
) (bool, error) {
	s.renewOnce.Do(func() { close(s.renewStarted) })
	<-s.renewContinue
	return s.SQLiteStore.RenewDirectImportReceipt(id, ownerToken, now, lease)
}

func (s *failOnceBrowserRecordingPublicationStore) CompareAndSwapSetting(key, expected, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.HasPrefix(key, browserRecordingSaveLocatorKeyPrefix) &&
		strings.Contains(expected, `"state":"staged"`) &&
		strings.Contains(value, `"state":"published"`) && s.failPublish {
		s.failPublish = false
		return false, errors.New("injected browser locator publication failure")
	}
	if key == libraryImportClaimsSettingKey && s.failClaimCleanup &&
		strings.Contains(expected, `"remote_id":"direct-import:browser-recording"`) &&
		strings.Contains(expected, `"committed":true`) &&
		!strings.Contains(value, `"remote_id":"direct-import:browser-recording"`) {
		s.failClaimCleanup = false
		return false, errors.New("injected browser claim cleanup failure")
	}
	return s.SQLiteStore.CompareAndSwapSetting(key, expected, value)
}

func (s *browserRecordingLocatorCountingStore) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	s.listScripts++
	return s.SQLiteStore.ListScripts(projectID, folderID)
}

func (s *browserRecordingLocatorCountingStore) GetScript(id string) (*dsl.Script, error) {
	s.getScripts++
	return s.SQLiteStore.GetScript(id)
}

func TestValidateBrowserRecordingHARBaselinePreparation(t *testing.T) {
	observation := []correlate.Observation{{ID: 1}}
	for _, test := range []struct {
		name       string
		durable    bool
		hasHTTP    bool
		baseline   []correlate.Observation
		deriveErr  error
		wantErrSub string
	}{
		{name: "durable baseline", durable: true, hasHTTP: true, baseline: observation},
		{name: "storage-free embedding", durable: false, hasHTTP: true, deriveErr: errors.New("unavailable")},
		{name: "no HTTP companion", durable: true, hasHTTP: false, deriveErr: errors.New("unavailable")},
		{name: "durable derivation failure", durable: true, hasHTTP: true, deriveErr: errors.New("bad HAR"), wantErrSub: "bad HAR"},
		{name: "durable empty baseline", durable: true, hasHTTP: true, wantErrSub: "no persistable HTTP exchanges"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateBrowserRecordingHARBaselinePreparation(test.durable, test.hasHTTP, test.baseline, test.deriveErr)
			if test.wantErrSub == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantErrSub != "" && (err == nil || !strings.Contains(err.Error(), test.wantErrSub)) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErrSub)
			}
		})
	}
}

func (s *failOnceBrowserRecordingBaselineStore) SaveScriptRecording(record *storage.ScriptRecordingRecord) error {
	if s.fail {
		s.fail = false
		return errors.New("injected browser recording baseline failure")
	}
	return s.SQLiteStore.SaveScriptRecording(record)
}

func (s *failOnceBrowserRecordingBaselineReadStore) GetScriptRecording(scriptID string) (*storage.ScriptRecordingRecord, error) {
	if s.fail {
		s.fail = false
		return nil, errors.New("injected browser recording baseline read failure")
	}
	return s.SQLiteStore.GetScriptRecording(scriptID)
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

func TestHandlePreviewBrowserRecordingConversionRejectsSaveOnlySecretBindings(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert/preview",
		bytes.NewBufferString(`{
			"name":"Checkout Preview",
			"secret_bindings":[{
				"requirement_id":"secret-binding-example",
				"secret_name":"LOGIN_PASSWORD",
				"script_index":0
			}]
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("preview persisted %d script(s), want none", got)
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

func TestHandleConvertBrowserRecordingRejectsChangedScriptCountBeforePersisting(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout Saved","expected_script_count":2}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("convert status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("count mismatch persisted %d script(s), want none", got)
	}
}

func TestHandleConvertBrowserRecordingPersistsEveryScriptInSelectedFolder(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-folder.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store})
	fake.install(server.browserRecording)
	if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{
		ID: "browser-scripts", ProjectID: "proj-1", Name: "Browser imports", Category: "scripts",
	}); err != nil {
		t.Fatal(err)
	}
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout Folder","folder_id":"browser-scripts"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d body=%s", resp.Code, resp.Body.String())
	}
	stored := server.runner.ListScripts()
	if len(stored) != 1 || stored[0] == nil || stored[0].Script == nil {
		t.Fatalf("stored scripts = %#v, want one script", stored)
	}
	if stored[0].Script.FolderID == nil || *stored[0].Script.FolderID != "browser-scripts" {
		t.Fatalf("stored folder = %#v, want browser-scripts", stored[0].Script.FolderID)
	}
}

func TestHandleConvertBrowserRecordingAppliesReviewedParameterizationAtomically(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.StartURL = "https://shop.example/login?user_id=12345"
	artifact.NavEvents[0].URL = artifact.StartURL
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	stoppedArtifact, info, ok := server.browserRecording.Artifact(id)
	if !ok || stoppedArtifact == nil || info == nil {
		t.Fatal("stopped browser artifact is unavailable")
	}
	preview := server.convertBrowserRecording(
		stoppedArtifact,
		info,
		browserRecordingConvertRequest{Name: "Parameterized Browser"},
	)
	var selectedID string
	for _, suggestion := range buildTrustedImportParameterization(preview.Script).Suggestions {
		if suggestion.VarName == "user_id" {
			selectedID = suggestion.ID
			break
		}
	}
	if selectedID == "" {
		t.Fatalf(
			"browser preview produced no trusted user_id parameterization: %#v",
			buildTrustedImportParameterization(preview.Script).Suggestions,
		)
	}

	requestBody, err := json.Marshal(map[string]any{
		"name":                  "Parameterized Browser",
		"expected_script_count": 1,
		"parameterization_selections": []map[string]any{{
			"script_index": 0,
			"selected_ids": []string{selectedID},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewReader(requestBody),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d body=%s", resp.Code, resp.Body.String())
	}
	stored := server.runner.ListScripts()
	if len(stored) != 1 || stored[0] == nil || stored[0].Script == nil {
		t.Fatalf("stored scripts = %#v, want one script", stored)
	}
	foundParameterizedTarget := false
	for _, action := range stored[0].Script.Actions {
		foundParameterizedTarget = foundParameterizedTarget ||
			strings.Contains(action.Target, "user_id={{var.user_id}}")
	}
	if !foundParameterizedTarget || len(stored[0].Script.Parameters) == 0 {
		t.Fatalf(
			"reviewed parameterization was not committed atomically: actions=%#v parameters=%#v",
			stored[0].Script.Actions,
			stored[0].Script.Parameters,
		)
	}
}

func TestHandleConvertBrowserRecordingRejectsStaleParameterizationBeforePersisting(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{
			"name":"Stale Parameterization",
			"expected_script_count":1,
			"parameterization_selections":[{
				"script_index":0,
				"selected_ids":["suggestion-from-an-old-preview"]
			}]
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("convert status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("stale parameterization persisted %d script(s), want none", got)
	}
}

func TestHandleConvertBrowserRecordingMasksCredentialRepeatedAsDetachedName(t *testing.T) {
	const credentialName = "browser-name-password-canary"
	artifact := recordedBrowserArtifact()
	artifact.Steps[0].Masked = false
	artifact.Steps[0].MarkSecret = true
	artifact.Steps[0].Value = credentialName

	server, store := newAuditTestServer(t)
	defer store.Close()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	if err := store.SaveProject(&storage.Project{
		ID:        "proj-1",
		Name:      "Browser recorder project",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	fake := newFakeBrowserRecorder(artifact)
	fake.install(server.browserRecording)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)
	drainHubBroadcasts(t, server.hub)

	body, err := json.Marshal(map[string]any{"name": credentialName})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), credentialName) {
		t.Fatalf("convert response did not preserve imported script name: %s", resp.Body.String())
	}

	var converted map[string]any
	for _, message := range drainHubBroadcasts(t, server.hub) {
		if message["type"] == "browser_recording_converted" {
			converted = message
			break
		}
	}
	if converted == nil {
		t.Fatal("missing browser_recording_converted broadcast")
	}
	if got, _ := converted["script_name"].(string); got == credentialName || !strings.Contains(got, scrub.Redacted) {
		t.Fatalf("converted broadcast script_name = %q, want disclosure-safe marker", got)
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{
		Action:    "browser_recording.convert",
		ProjectID: "proj-1",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("browser conversion audit events = %#v, want one", events)
	}
	if got, _ := events[0].Details["name"].(string); got == credentialName || !strings.Contains(got, scrub.Redacted) {
		t.Fatalf("browser conversion audit name = %q, want disclosure-safe marker", got)
	}

	stored := server.runner.ListScripts()
	if len(stored) != 1 || stored[0] == nil || stored[0].Script == nil {
		t.Fatalf("stored scripts = %#v, want one durable script", stored)
	}
	if stored[0].Name != credentialName || stored[0].Script.Name != credentialName {
		t.Fatalf("durable script name was altered: wrapper=%q script=%q", stored[0].Name, stored[0].Script.Name)
	}
}

func TestHandleConvertBrowserRecordingRejectsMalformedJSONBeforePersisting(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(`{"name":`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("convert status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("malformed browser conversion persisted %d scripts, want 0", got)
	}
}

func TestHandleConvertBrowserRecordingPreservesHARCredentialInDurableScript(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-05-12T20:00:00Z","time":12,"request":{"method":"GET","url":"https://shop.example/api/cart","httpVersion":"HTTP/1.1","headers":[{"name":"Authorization","value":"Bearer browser-recorded-token"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[{"name":"Content-Type","value":"application/json"}],"cookies":[],"content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},"cache":{},"timings":{"send":1,"wait":10,"receive":1}}]}}`)
	fake := newFakeBrowserRecorder(artifact)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-har-baseline.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: "proj-1", Name: "Browser recording", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server := newBrowserRecorderTestServerWithStore(t, fake, store)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Checkout Saved","also_create_http_script":true}`))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "browser-recorded-token") {
		t.Fatalf("convert response did not preserve recorded credential: %s", resp.Body.String())
	}
	server.importDraftAttestationMu.Lock()
	attestationCount := len(server.importDraftAttestations)
	server.importDraftAttestationMu.Unlock()
	if attestationCount != 0 {
		t.Fatalf("already-committed browser response retained %d unused draft attestation(s)", attestationCount)
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
	if !strings.Contains(string(storedJSON), "browser-recorded-token") {
		t.Fatalf("durable HTTP script discarded recorded credential: %s", storedJSON)
	}
	if strings.Contains(string(storedJSON), "{{secret.") || strings.Contains(string(storedJSON), scrub.Redacted) {
		t.Fatalf("durable HTTP script requires credential re-entry or contains a sentinel: %s", storedJSON)
	}
	if !isCredentialPreservingImport(httpScript.Script) {
		t.Fatalf("stored HTTP script lacks server-owned preservation policy: %#v", httpScript.Script.Metadata)
	}
	baseline, err := server.store.GetScriptRecording(httpScript.ID)
	if err != nil {
		t.Fatalf("load browser-recording HTTP baseline: %v", err)
	}
	if baseline == nil || baseline.Source != "browser_recording_har" || baseline.RequestCount != 1 {
		t.Fatalf("browser-recording HTTP baseline = %#v, want one HAR observation", baseline)
	}
	if len(baseline.Observations) != 1 || strings.Contains(
		baseline.Observations[0].RequestHeaders["Authorization"],
		"browser-recorded-token",
	) {
		t.Fatalf("browser-recording baseline did not retain a disclosure-safe structural observation: %#v", baseline.Observations)
	}
	readReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+httpScript.ID, nil)
	readResp := httptest.NewRecorder()
	server.mux.ServeHTTP(readResp, readReq)
	if readResp.Code != http.StatusOK {
		t.Fatalf("stored read status = %d body=%s", readResp.Code, readResp.Body.String())
	}
	if !strings.Contains(readResp.Body.String(), "browser-recorded-token") {
		t.Fatalf("ordinary stored-script read did not preserve HAR credential: %s", readResp.Body.String())
	}
}

func TestHandleConvertBrowserRecordingReplaysCompletedReceiptAfterRestart(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "browser-restart-baseline-key")
	artifact := recordedBrowserArtifact()
	const credential = "restart-safe-browser-token"
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-05-12T20:00:00Z","time":12,"request":{"method":"GET","url":"https://shop.example/api/cart","httpVersion":"HTTP/1.1","headers":[{"name":"Authorization","value":"Bearer ` + credential + `"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[{"name":"Set-Cookie","value":"session=response-secret"}],"cookies":[],"content":{"size":17,"mimeType":"application/json","text":"{\"token\":\"reply\"}"},"redirectURL":"","headersSize":-1,"bodySize":17},"cache":{},"timings":{"send":1,"wait":10,"receive":1}}]}}`)
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	now := time.Now().UTC()
	if err := base.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := base.SaveFolder(&storage.Folder{ID: "browser-restart-folder", ProjectID: "proj-1", Name: "Restart imports", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	first := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(artifact), base)
	id := startBrowserRecordingViaAPI(t, first)
	stopBrowserRecordingViaAPI(t, first, id)
	body := `{"name":"Restart-safe save","also_create_http_script":true,"folder_id":"browser-restart-folder","expected_script_count":2}`
	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	firstReq.Header.Set("Content-Type", "application/json")
	firstResp := httptest.NewRecorder()
	first.mux.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusCreated {
		t.Fatalf("first save status = %d body=%s", firstResp.Code, firstResp.Body.String())
	}
	if !strings.Contains(firstResp.Body.String(), credential) {
		t.Fatalf("first save response did not preserve the imported request credential: %s", firstResp.Body.String())
	}
	firstStored := first.runner.ListScripts()
	if len(firstStored) != 2 {
		t.Fatalf("first save scripts = %d, want 2", len(firstStored))
	}
	firstIDs := map[string]struct{}{}
	for _, stored := range firstStored {
		firstIDs[stored.ID] = struct{}{}
		if stored.Script.FolderID == nil || *stored.Script.FolderID != "browser-restart-folder" {
			t.Fatalf("first save folder = %#v, want browser-restart-folder", stored.Script.FolderID)
		}
		preserved := browserRecordingImportPreserved(stored.Script)
		if browserRecordingPreservedString(preserved, browserRecordingSaveSessionIDKey) != id {
			t.Fatalf("first save script lacks durable recorder provenance: %#v", preserved)
		}
	}

	// Simulate the response being lost and the controller restarting. The new
	// manager has no process-local recording session or raw HAR capability.
	restarted := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), base)
	resolverRequest := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", nil)
	resolverRequest.SetPathValue("id", id)
	resolvedProject, status, message := resolveBrowserRecordingSessionProjectID(restarted)(resolverRequest)
	if status != 0 || resolvedProject != "proj-1" {
		t.Fatalf("restart resolver = project %q status %d message %q, want proj-1", resolvedProject, status, message)
	}
	controlRequest := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/stop", nil)
	controlRequest.SetPathValue("id", id)
	if _, status, _ := resolveBrowserRecordingSessionProjectID(restarted)(controlRequest); status != http.StatusNotFound {
		t.Fatalf("restart resolver granted a non-convert control, status = %d", status)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	retryReq.Header.Set("Content-Type", "application/json")
	retryResp := httptest.NewRecorder()
	restarted.mux.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusCreated {
		t.Fatalf("restart retry status = %d body=%s", retryResp.Code, retryResp.Body.String())
	}
	if !strings.Contains(retryResp.Body.String(), credential) {
		t.Fatalf("restart retry response did not preserve the imported request credential: %s", retryResp.Body.String())
	}
	retriedStored := restarted.runner.ListScripts()
	if len(retriedStored) != 2 {
		t.Fatalf("restart retry scripts = %d, want the original 2", len(retriedStored))
	}
	var httpStored *StoredScript
	for _, stored := range retriedStored {
		if _, ok := firstIDs[stored.ID]; !ok {
			t.Fatalf("restart retry created a different script %q", stored.ID)
		}
		if stored.Script.FolderID == nil || *stored.Script.FolderID != "browser-restart-folder" {
			t.Fatalf("restart retry changed folder = %#v", stored.Script.FolderID)
		}
		if stored.Script.Protocol == "http" {
			httpStored = stored
		}
	}
	if httpStored == nil {
		t.Fatal("restart retry lost the HTTP companion")
	}
	exact, err := json.Marshal(httpStored.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exact), credential) {
		t.Fatalf("restart retry discarded exact imported credential: %s", exact)
	}
	baseline, err := base.GetScriptRecording(httpStored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if baseline == nil || baseline.RequestCount != 1 || baseline.Source != "browser_recording_har" {
		t.Fatalf("restart retry baseline = %#v, want one durable HAR exchange", baseline)
	}
}

func TestHandleConvertBrowserRecordingRetriesAfterBaselinePersistenceFailure(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "browser-baseline-retry-key")
	artifact := recordedBrowserArtifact()
	const credential = "baseline-retry-browser-token"
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-05-12T20:00:00Z","time":12,"request":{"method":"GET","url":"https://shop.example/api/cart","httpVersion":"HTTP/1.1","headers":[{"name":"Authorization","value":"Bearer ` + credential + `"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],"cookies":[],"content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},"cache":{},"timings":{"send":1,"wait":10,"receive":1}}]}}`)
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-baseline-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failOnceBrowserRecordingBaselineStore{SQLiteStore: base, fail: true}
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{ID: "browser-baseline-folder", ProjectID: "proj-1", Name: "Baseline imports", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(artifact), store)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)
	body := `{"name":"Baseline retry","also_create_http_script":true,"folder_id":"browser-baseline-folder","expected_script_count":2}`

	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	firstReq.Header.Set("Content-Type", "application/json")
	firstResp := httptest.NewRecorder()
	server.mux.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusInternalServerError {
		t.Fatalf("failed baseline save status = %d, want 500 body=%s", firstResp.Code, firstResp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("failed baseline save left %d committed script(s), want rollback", got)
	}
	_, actorSubject, _ := server.resolveAuditActor(firstReq)
	operationID := strings.TrimSpace(actorSubject) + "\x00session:" + id
	probeAttempt, probeReplay, err := server.claimDirectImportAttempt(
		"proj-1",
		"browser_recorder",
		operationID,
		"different-after-baseline-failure",
	)
	if err != nil {
		t.Fatalf("baseline failure left a conflicting receipt: %v", err)
	}
	if probeReplay || probeAttempt == nil || !probeAttempt.created {
		t.Fatalf("baseline failure left a completed/pending receipt: attempt=%#v replay=%v", probeAttempt, probeReplay)
	}
	if err := probeAttempt.rollback(nil); err != nil {
		t.Fatalf("release receipt absence probe: %v", err)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	retryReq.Header.Set("Content-Type", "application/json")
	retryResp := httptest.NewRecorder()
	server.mux.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusCreated {
		t.Fatalf("baseline retry status = %d body=%s", retryResp.Code, retryResp.Body.String())
	}
	if !strings.Contains(retryResp.Body.String(), credential) {
		t.Fatalf("baseline retry response did not preserve the HAR credential: %s", retryResp.Body.String())
	}
	stored := server.runner.ListScripts()
	if len(stored) != 2 {
		t.Fatalf("baseline retry scripts = %d, want 2", len(stored))
	}
	var httpStored *StoredScript
	for _, item := range stored {
		if item.Script.FolderID == nil || *item.Script.FolderID != "browser-baseline-folder" {
			t.Fatalf("baseline retry folder = %#v", item.Script.FolderID)
		}
		if item.Script.Protocol == "http" {
			httpStored = item
		}
	}
	if httpStored == nil {
		t.Fatal("baseline retry lost the HTTP companion")
	}
	baseline, err := base.GetScriptRecording(httpStored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if baseline == nil || baseline.RequestCount != 1 || baseline.Source != "browser_recording_har" {
		t.Fatalf("baseline retry record = %#v, want one durable HAR exchange", baseline)
	}
}

func TestHandleConvertBrowserRecordingRejectsUnknownLostCapabilityWithoutWrites(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-unknown-capability.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	now := time.Now().UTC()
	if err := base.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	store := &browserRecordingLocatorCountingStore{SQLiteStore: base}
	server := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), store)
	// Ignore startup hydration. The assertion below covers only the bogus-id
	// resolver/handler path and proves it does not scan the script catalog.
	store.listScripts = 0
	store.getScripts = 0
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/lost-session/convert",
		bytes.NewBufferString(`{"name":"Should not save"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("unknown lost capability status = %d, want 404 body=%s", resp.Code, resp.Body.String())
	}
	if store.listScripts != 0 || store.getScripts != 0 {
		t.Fatalf("bogus lost id performed script reads: ListScripts=%d GetScript=%d", store.listScripts, store.getScripts)
	}
	// Read the backing store directly after the no-scan assertion. The runner's
	// user-facing ListScripts path intentionally performs a publication-fence
	// revalidation against durable storage, so using it here would count the
	// test's own verification as a bogus-id handler scan.
	persisted, err := base.ListScripts("proj-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(persisted); got != 0 {
		t.Fatalf("unknown lost capability persisted %d script(s), want none", got)
	}
}

func TestHandleConvertBrowserRecordingRetriesAfterLocatorPersistenceFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-locator-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failOnceBrowserRecordingLocatorStore{SQLiteStore: base, fail: true}
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(recordedBrowserArtifact()), store)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)
	body := `{"name":"Locator retry","expected_script_count":1}`

	firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	firstReq.Header.Set("Content-Type", "application/json")
	firstResp := httptest.NewRecorder()
	server.mux.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusInternalServerError {
		t.Fatalf("locator failure status = %d, want 500 body=%s", firstResp.Code, firstResp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("locator failure published %d script(s), want none", got)
	}
	locatorKey, err := server.browserRecordingSaveLocatorKey(id)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := base.GetSetting(locatorKey); err != nil || raw != "" {
		t.Fatalf("locator failure left locator %q err=%v", raw, err)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	retryReq.Header.Set("Content-Type", "application/json")
	retryResp := httptest.NewRecorder()
	server.mux.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusCreated {
		t.Fatalf("locator retry status = %d body=%s", retryResp.Code, retryResp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("locator retry scripts = %d, want 1", got)
	}
	if raw, err := base.GetSetting(locatorKey); err != nil || raw == "" {
		t.Fatalf("locator retry did not persist locator: %q err=%v", raw, err)
	}
}

func TestRemoveBrowserRecordingSaveLocatorClearsReceiptBearingStagedValue(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-locator-cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	server := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), base)
	attempt := &directImportAttempt{receipt: &storage.DirectImportReceipt{
		ID:               "receipt-1",
		OperationKeyHash: "operation-hash",
		RequestSHA256:    "request-digest",
	}}
	stored := []*StoredScript{{ID: "script-1"}}
	if err := server.persistBrowserRecordingSaveLocator("recording-1", "proj-1", stored, attempt); err != nil {
		t.Fatal(err)
	}
	locatorKey, err := server.browserRecordingSaveLocatorKey("recording-1")
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := base.GetSetting(locatorKey); err != nil || raw == "" {
		t.Fatalf("staged receipt-bearing locator = %q err=%v", raw, err)
	}
	if err := server.removeBrowserRecordingSaveLocator("recording-1", "proj-1", stored, attempt); err != nil {
		t.Fatal(err)
	}
	if raw, err := base.GetSetting(locatorKey); err != nil || raw != "" {
		t.Fatalf("compensated locator = %q err=%v, want empty", raw, err)
	}
	if err := server.publishBrowserRecordingSaveLocator("recording-1", "proj-1", stored, attempt); err == nil {
		t.Fatal("publication recreated an absent staged locator")
	}
}

func TestRollbackBrowserRecordingSavePreservesLocatorWhenReceiptTakeoverWins(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-rollback-takeover.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &browserRecordingRollbackTakeoverStore{
		SQLiteStore:   base,
		renewStarted:  make(chan struct{}),
		renewContinue: make(chan struct{}),
	}
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), store)
	const recordingID = "rollback-takeover-recording"
	req := browserRecordingConvertRequest{Name: "Rollback takeover", ExpectedScriptCount: 1}
	script, err := preparePersistentImportedLibraryScript(&dsl.Script{
		Name:      req.Name,
		Version:   "1",
		Protocol:  "browser",
		ProjectID: "proj-1",
		Actions: []dsl.Action{{
			Name: "Open", Type: "browser_navigate", Target: "https://example.test",
		}},
	}, nil, "browser_recorder")
	if err != nil {
		t.Fatal(err)
	}
	_, actorSubject, _ := server.resolveAuditActor(httptest.NewRequest(http.MethodPost, "/", nil))
	operationID := strings.TrimSpace(actorSubject) + "\x00session:" + recordingID
	script.ID = deterministicDirectImportAssetID(
		directImportOperationHash("proj-1", "browser_recorder", operationID),
		"script",
		0,
	)
	script.RefKey = ""
	actorHash, err := server.browserRecordingSaveActorFingerprint(recordingID, actorSubject)
	if err != nil {
		t.Fatal(err)
	}
	stampBrowserRecordingSaveProvenance(
		script, recordingID, 0, 1, actorHash, browserRecordingSaveRoleBrowser, false,
	)
	proof, err := server.browserRecordingSaveProvenanceProof(script)
	if err != nil {
		t.Fatal(err)
	}
	script.Metadata.Import.Preserved[browserRecordingSaveProofKey] = proof
	scripts := []*dsl.Script{script}
	requestDigest, err := server.directImportScriptsRequestDigest(scripts, req)
	if err != nil {
		t.Fatal(err)
	}
	attempt, replay, err := server.claimDirectImportAttempt(
		"proj-1", "browser_recorder", operationID, requestDigest,
	)
	if err != nil || replay || attempt == nil || !attempt.created {
		t.Fatalf("claim attempt=%#v replay=%v err=%v", attempt, replay, err)
	}
	transaction, err := newCreateImportTransaction(server, "browser-recording", scripts, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := transaction.createImportedScript(script, "browser_recorder")
	if err != nil {
		t.Fatal(err)
	}
	allStored := []*StoredScript{stored}
	if err := server.persistBrowserRecordingSaveLocator(recordingID, "proj-1", allStored, attempt); err != nil {
		t.Fatal(err)
	}

	rollbackResult := make(chan error, 1)
	go func() {
		rollbackResult <- server.rollbackBrowserRecordingSave(
			attempt, transaction, recordingID, "proj-1", allStored, true,
		)
	}()
	<-store.renewStarted
	takeover, takeoverErr := base.ClaimDirectImportReceipt(
		"proj-1",
		"browser_recorder",
		attempt.receipt.OperationKeyHash,
		attempt.receipt.RequestSHA256,
		"winning-healer",
		time.Now().UTC().Add(3*directImportReceiptLease()),
		directImportReceiptLease(),
	)
	close(store.renewContinue)
	if takeoverErr != nil || takeover == nil || takeover.Created || takeover.Replay {
		t.Fatalf("receipt takeover=%#v err=%v", takeover, takeoverErr)
	}
	rollbackErr := <-rollbackResult
	if rollbackErr == nil || !strings.Contains(rollbackErr.Error(), "ownership moved") {
		t.Fatalf("losing rollback error=%v, want ownership-moved result", rollbackErr)
	}
	locatorKey, err := server.browserRecordingSaveLocatorKey(recordingID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base.GetSetting(locatorKey)
	if err != nil || raw == "" {
		t.Fatalf("losing rollback removed staged locator: raw=%q err=%v", raw, err)
	}
	locator, err := server.decodeBrowserRecordingSaveLocator(raw)
	if err != nil || locator.State != browserRecordingSaveLocatorStaged {
		t.Fatalf("staged locator after takeover=%#v err=%v", locator, err)
	}

	// The exact healer can now authenticate and publish the save which won the
	// receipt fence. This proves preserving the locator is recovery-enabling,
	// not merely avoiding a destructive write.
	if err := server.recoverStagedBrowserRecordingConversionsAtStartup(); err != nil {
		t.Fatal(err)
	}
	raw, err = base.GetSetting(locatorKey)
	if err != nil {
		t.Fatal(err)
	}
	locator, err = server.decodeBrowserRecordingSaveLocator(raw)
	if err != nil || locator.State != browserRecordingSaveLocatorPublished {
		t.Fatalf("healed locator=%#v err=%v", locator, err)
	}
	if recovery, err := server.loadDurableBrowserRecordingConversion(recordingID); err != nil || recovery == nil || len(recovery.Scripts) != 1 {
		t.Fatalf("healed conversion=%#v err=%v", recovery, err)
	}
}

func TestValidatePersistedBrowserRecordingHARBaselineForAdoptionPreservesReadErrors(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-baseline-read.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failOnceBrowserRecordingBaselineReadStore{SQLiteStore: base, fail: true}
	server := &Server{store: store}
	script := &dsl.Script{
		ID: "http-script",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
			browserRecordingSaveRoleKey:          browserRecordingSaveRoleHTTP,
			browserRecordingHARBaselineNeededKey: true,
		}}},
	}
	stored := []*StoredScript{{ID: script.ID, Script: script}}
	valid, err := server.validatePersistedBrowserRecordingHARBaselineForAdoption(stored)
	if err == nil || valid {
		t.Fatalf("baseline read result valid=%v err=%v, want preserved storage error", valid, err)
	}
	valid, err = server.validatePersistedBrowserRecordingHARBaselineForAdoption(stored)
	if err != nil || valid {
		t.Fatalf("missing baseline result valid=%v err=%v, want incomplete without storage error", valid, err)
	}
}

func TestHandleConvertBrowserRecordingHARetryFinishesEveryPublicationFailure(t *testing.T) {
	for _, test := range []struct {
		name             string
		failComplete     bool
		failPublish      bool
		failClaimCleanup bool
	}{
		{name: "receipt completion", failComplete: true},
		{name: "locator publication", failPublish: true},
		{name: "retained claim cleanup", failClaimCleanup: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-ha-publication.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = base.Close() })
			store := &failOnceBrowserRecordingPublicationStore{
				SQLiteStore:      base,
				failComplete:     test.failComplete,
				failPublish:      test.failPublish,
				failClaimCleanup: test.failClaimCleanup,
			}
			now := time.Now().UTC()
			if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			server := newHABrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(recordedBrowserArtifact()), store)
			id := startBrowserRecordingViaAPI(t, server)
			stopBrowserRecordingViaAPI(t, server, id)
			body := `{"name":"HA publication retry","expected_script_count":1}`

			firstReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
			firstReq.Header.Set("Content-Type", "application/json")
			firstResp := httptest.NewRecorder()
			server.mux.ServeHTTP(firstResp, firstReq)
			if firstResp.Code != http.StatusInternalServerError {
				t.Fatalf("interrupted publication status=%d body=%s", firstResp.Code, firstResp.Body.String())
			}
			if got := len(server.runner.ListScripts()); got != 1 {
				t.Fatalf("interrupted publication scripts=%d, want one retained exact asset", got)
			}

			retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
			retryReq.Header.Set("Content-Type", "application/json")
			retryResp := httptest.NewRecorder()
			server.mux.ServeHTTP(retryResp, retryReq)
			if retryResp.Code != http.StatusCreated {
				t.Fatalf("exact HA retry status=%d body=%s", retryResp.Code, retryResp.Body.String())
			}
			if got := len(server.runner.ListScripts()); got != 1 {
				t.Fatalf("exact HA retry scripts=%d, want no duplicate", got)
			}
			claims, err := server.loadLibraryImportClaims()
			if err != nil {
				t.Fatal(err)
			}
			for _, claim := range claims {
				if claim.RemoteID == "direct-import:browser-recording" {
					t.Fatalf("exact HA retry retained claim %#v", claim)
				}
			}
			locatorKey, err := server.browserRecordingSaveLocatorKey(id)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := base.GetSetting(locatorKey)
			if err != nil {
				t.Fatal(err)
			}
			locator, err := server.decodeBrowserRecordingSaveLocator(raw)
			if err != nil || locator.State != browserRecordingSaveLocatorPublished {
				t.Fatalf("exact HA retry locator=%#v err=%v", locator, err)
			}
		})
	}
}

func TestHandleConvertBrowserRecordingRestartAdoptsStagedLocatorBeforeClaimCommitAndRetriesExactSave(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "browser-staged-locator-restart-key")
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	artifact := recordedBrowserArtifact()
	const credential = "staged-locator-retry-token"
	artifact.HAR = []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-05-12T20:00:00Z","time":12,"request":{"method":"GET","url":"https://shop.example/api/cart","httpVersion":"HTTP/1.1","headers":[{"name":"Authorization","value":"Bearer ` + credential + `"}],"queryString":[],"cookies":[],"headersSize":-1,"bodySize":0},"response":{"status":200,"statusText":"OK","httpVersion":"HTTP/1.1","headers":[],"cookies":[],"content":{"size":2,"mimeType":"application/json","text":"{}"},"redirectURL":"","headersSize":-1,"bodySize":2},"cache":{},"timings":{"send":1,"wait":10,"receive":1}}]}}`)
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-staged-locator-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	now := time.Now().UTC()
	if err := base.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := base.SaveFolder(&storage.Folder{ID: "browser-crash-folder", ProjectID: "proj-1", Name: "Crash imports", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	first := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(artifact), base)
	id := startBrowserRecordingViaAPI(t, first)
	stopBrowserRecordingViaAPI(t, first, id)
	folderID := "browser-crash-folder"
	req := browserRecordingConvertRequest{
		Name:                 "Crash-window retry",
		FolderID:             &folderID,
		AlsoCreateHTTPScript: true,
		ExpectedScriptCount:  2,
	}
	info, ok := first.browserRecording.Get(id)
	if !ok {
		t.Fatal("stopped recording disappeared")
	}
	result := first.convertBrowserRecording(artifact, info, req)
	if result == nil || len(result.Scripts) != 1 || result.Scripts[0] == nil {
		t.Fatalf("browser conversion = %#v, want one script", result)
	}
	preparedBrowser, err := preparePersistentImportedLibraryScript(result.Scripts[0], nil, "browser_recorder")
	if err != nil {
		t.Fatal(err)
	}
	httpScript, _, _, err := first.createHTTPScriptFromHAR(artifact, info, req.Name)
	if err != nil {
		t.Fatal(err)
	}
	preparedHTTP, err := preparePersistentImportedLibraryScript(httpScript, nil, "browser_recording_har")
	if err != nil {
		t.Fatal(err)
	}
	scripts := []*dsl.Script{preparedBrowser, preparedHTTP}
	for _, script := range scripts {
		if err := bindDraftScriptProject(script, "proj-1"); err != nil {
			t.Fatal(err)
		}
		if status, err := first.applyProtectedImportFolderOverride(script, req.FolderID); err != nil || status != 0 {
			t.Fatalf("apply folder status=%d err=%v", status, err)
		}
	}
	_, actorSubject, _ := first.resolveAuditActor(httptest.NewRequest(http.MethodPost, "/", nil))
	operationID := strings.TrimSpace(actorSubject) + "\x00session:" + id
	assetIdentitySeed := directImportOperationHash("proj-1", "browser_recorder", operationID)
	for index, script := range scripts {
		script.ID = deterministicDirectImportAssetID(assetIdentitySeed, "script", index)
		script.RefKey = ""
	}
	actorHash, err := first.browserRecordingSaveActorFingerprint(id, actorSubject)
	if err != nil {
		t.Fatal(err)
	}
	for index, script := range scripts {
		role := browserRecordingSaveRoleBrowser
		baselineRequired := false
		if index == len(scripts)-1 {
			role = browserRecordingSaveRoleHTTP
			baselineRequired = true
		}
		stampBrowserRecordingSaveProvenance(script, id, index, len(scripts), actorHash, role, baselineRequired)
		proof, err := first.browserRecordingSaveProvenanceProof(script)
		if err != nil {
			t.Fatal(err)
		}
		script.Metadata.Import.Preserved[browserRecordingSaveProofKey] = proof
	}
	requestDigest, err := first.directImportScriptsRequestDigest(scripts, req)
	if err != nil {
		t.Fatal(err)
	}
	attempt, replay, err := first.claimDirectImportAttempt("proj-1", "browser_recorder", operationID, requestDigest)
	if err != nil || replay || attempt == nil || !attempt.created {
		t.Fatalf("claim attempt=%#v replay=%v err=%v", attempt, replay, err)
	}
	transaction, err := newCreateImportTransaction(first, "browser-recording", scripts, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	stored := make([]*StoredScript, 0, len(scripts))
	for index, script := range scripts {
		profile := "browser_recorder"
		if index == len(scripts)-1 {
			profile = "browser_recording_har"
		}
		item, err := transaction.createImportedScript(script, profile)
		if err != nil {
			t.Fatal(err)
		}
		stored = append(stored, item)
	}
	baseline, err := har.CorrelationBaseline(artifact.HAR, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.finalizeBrowserRecordingHARBaseline(stored, baseline); err != nil {
		t.Fatal(err)
	}
	if err := first.persistBrowserRecordingSaveLocator(id, "proj-1", stored, attempt); err != nil {
		t.Fatal(err)
	}
	locatorKey, err := first.browserRecordingSaveLocatorKey(id)
	if err != nil {
		t.Fatal(err)
	}
	stagedRaw, err := base.GetSetting(locatorKey)
	if err != nil || stagedRaw == "" {
		t.Fatalf("staged locator = %q err=%v", stagedRaw, err)
	}
	locator, err := first.decodeBrowserRecordingSaveLocator(stagedRaw)
	if err != nil || locator.State != browserRecordingSaveLocatorStaged {
		t.Fatalf("staged locator = %#v err=%v", locator, err)
	}

	// Model process loss after the staged locator CAS and before the claim commit,
	// receipt completion, or locator publication. A fresh server must
	// authenticate and adopt the complete staged set;
	// it has no process-local recording session or raw stopped capability.
	transaction.stopClaimHeartbeat()
	activeLibraryImportOwners.Delete(transaction.claims.owner)
	restarted := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), base)
	if got := len(restarted.runner.ListScripts()); got != 2 {
		t.Fatalf("startup recovery published %d script(s), want exact staged pair", got)
	}
	resolverRequest := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", nil)
	resolverRequest.SetPathValue("id", id)
	if projectID, status, message := resolveBrowserRecordingSessionProjectID(restarted)(resolverRequest); status != 0 || projectID != "proj-1" {
		t.Fatalf("recovered locator resolver project=%q status=%d message=%q", projectID, status, message)
	}

	// Retry through only the durable locator/receipt; no live session is recreated.
	body := `{"name":"Crash-window retry","folder_id":"browser-crash-folder","also_create_http_script":true,"expected_script_count":2}`
	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	retryReq.Header.Set("Content-Type", "application/json")
	retryResp := httptest.NewRecorder()
	restarted.mux.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusCreated {
		t.Fatalf("exact retry status=%d body=%s", retryResp.Code, retryResp.Body.String())
	}
	if !strings.Contains(retryResp.Body.String(), credential) {
		t.Fatalf("exact retry response did not preserve credential: %s", retryResp.Body.String())
	}
	retried := restarted.runner.ListScripts()
	if len(retried) != 2 {
		t.Fatalf("exact retry scripts=%d, want 2", len(retried))
	}
	for index, script := range retried {
		if script.ID != scripts[index].ID && script.ID != scripts[1-index].ID {
			t.Fatalf("exact retry created unexpected script id %q", script.ID)
		}
	}
	finalRaw, err := base.GetSetting(locatorKey)
	if err != nil {
		t.Fatal(err)
	}
	finalLocator, err := restarted.decodeBrowserRecordingSaveLocator(finalRaw)
	if err != nil || finalLocator.State != browserRecordingSaveLocatorPublished {
		t.Fatalf("published locator = %#v err=%v", finalLocator, err)
	}
}

func TestHandleConvertBrowserRecordingHAAdoptsExpiredPreCommitStageAndRetriesExactSave(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-ha-staged-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base}
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	artifact := recordedBrowserArtifact()
	first := newHABrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(artifact), store)
	id := startBrowserRecordingViaAPI(t, first)
	stopBrowserRecordingViaAPI(t, first, id)
	req := browserRecordingConvertRequest{Name: "HA crash-window retry", ExpectedScriptCount: 1}
	info, ok := first.browserRecording.Get(id)
	if !ok {
		t.Fatal("stopped recording disappeared")
	}
	result := first.convertBrowserRecording(artifact, info, req)
	if result == nil || len(result.Scripts) != 1 || result.Scripts[0] == nil {
		t.Fatalf("browser conversion = %#v, want one script", result)
	}
	script, err := preparePersistentImportedLibraryScript(result.Scripts[0], nil, "browser_recorder")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(script.Version) == "" {
		script.Version = "1"
	}
	if err := bindDraftScriptProject(script, "proj-1"); err != nil {
		t.Fatal(err)
	}
	_, actorSubject, _ := first.resolveAuditActor(httptest.NewRequest(http.MethodPost, "/", nil))
	operationID := strings.TrimSpace(actorSubject) + "\x00session:" + id
	assetIdentitySeed := directImportOperationHash("proj-1", "browser_recorder", operationID)
	script.ID = deterministicDirectImportAssetID(assetIdentitySeed, "script", 0)
	script.RefKey = ""
	actorHash, err := first.browserRecordingSaveActorFingerprint(id, actorSubject)
	if err != nil {
		t.Fatal(err)
	}
	stampBrowserRecordingSaveProvenance(script, id, 0, 1, actorHash, browserRecordingSaveRoleBrowser, false)
	proof, err := first.browserRecordingSaveProvenanceProof(script)
	if err != nil {
		t.Fatal(err)
	}
	script.Metadata.Import.Preserved[browserRecordingSaveProofKey] = proof
	scripts := []*dsl.Script{script}
	requestDigest, err := first.directImportScriptsRequestDigest(scripts, req)
	if err != nil {
		t.Fatal(err)
	}
	attempt, replay, err := first.claimDirectImportAttempt("proj-1", "browser_recorder", operationID, requestDigest)
	if err != nil || replay || attempt == nil || !attempt.created {
		t.Fatalf("claim attempt=%#v replay=%v err=%v", attempt, replay, err)
	}
	transaction, err := newCreateImportTransaction(first, "browser-recording", scripts, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := transaction.createImportedScript(script, "browser_recorder")
	if err != nil {
		t.Fatal(err)
	}
	allStored := []*StoredScript{stored}
	if err := first.persistBrowserRecordingSaveLocator(id, "proj-1", allStored, attempt); err != nil {
		t.Fatal(err)
	}

	// Model a dead HA owner while its durable leases are still unexpired. Startup
	// must leave the staged save fenced rather than stealing live-looking work.
	transaction.stopClaimHeartbeat()
	owner := transaction.claims.owner
	activeLibraryImportOwners.Delete(owner)
	restarted := newHABrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), store)
	if _, err := restarted.loadDurableBrowserRecordingConversion(id); !errors.Is(err, errBrowserRecordingSaveFinalizing) {
		t.Fatalf("unexpired HA staged lookup error=%v, want finalizing conflict", err)
	}

	// Any later import acquisition invokes generic expired-claim recovery. Once
	// the browser claim expires, that shared path must give the recorder-specific
	// staged evidence first refusal instead of deleting the stopped recording.
	if err := restarted.mutateLibraryImportClaims(func(claims *[]libraryImportClaim) (bool, error) {
		changed := false
		for index := range *claims {
			if (*claims)[index].OwnerID == owner {
				(*claims)[index].ExpiresAt = time.Now().UTC().Add(-time.Minute)
				changed = true
			}
		}
		return changed, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("unrelated import recovery did not adopt staged browser save: %v", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewReader(body))
	retryReq.Header.Set("Content-Type", "application/json")
	retryResp := httptest.NewRecorder()
	restarted.mux.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusCreated {
		t.Fatalf("exact HA crash retry status=%d body=%s", retryResp.Code, retryResp.Body.String())
	}
	if got := len(restarted.runner.ListScripts()); got != 1 {
		t.Fatalf("exact HA crash retry scripts=%d, want no duplicate", got)
	}
}

func TestHandleConvertBrowserRecordingRestartAdoptsAfterReceiptCompletionFailure(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-receipt-completion-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base, failComplete: true}
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-1", Name: "Browser project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	first := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(recordedBrowserArtifact()), store)
	id := startBrowserRecordingViaAPI(t, first)
	stopBrowserRecordingViaAPI(t, first, id)
	body := `{"name":"Receipt completion restart","expected_script_count":1}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	first.mux.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("interrupted save status=%d body=%s", response.Code, response.Body.String())
	}
	if got := len(first.runner.ListScripts()); got != 1 {
		t.Fatalf("interrupted save published %d script(s), want committed script retained", got)
	}

	// No live/stopped recorder capability survives. Startup uses the signed
	// locator plus committed claim set to finish the exact receipt and publish it.
	restarted := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), base)
	if got := len(restarted.runner.ListScripts()); got != 1 {
		t.Fatalf("startup adoption loaded %d script(s), want 1", got)
	}
	retry := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/convert", bytes.NewBufferString(body))
	retry.Header.Set("Content-Type", "application/json")
	retryResponse := httptest.NewRecorder()
	restarted.mux.ServeHTTP(retryResponse, retry)
	if retryResponse.Code != http.StatusCreated {
		t.Fatalf("restart retry status=%d body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	if got := len(restarted.runner.ListScripts()); got != 1 {
		t.Fatalf("restart retry duplicated assets: %d scripts", got)
	}
	claims, err := restarted.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range claims {
		if claim.RemoteID == "direct-import:browser-recording" {
			t.Fatalf("restart adoption retained browser claim: %#v", claim)
		}
	}
}

func TestLoadDurableBrowserRecordingConversionRejectsTamperedLocator(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "browser-tampered-locator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	server := newBrowserRecorderTestServerWithStore(t, newFakeBrowserRecorder(nil), base)
	key, err := server.browserRecordingSaveLocatorKey("tampered-recording")
	if err != nil {
		t.Fatal(err)
	}
	if err := base.SaveSetting(key, `{"version":1,"recording_id":"tampered-recording","project_id":"proj-1","script_ids":["attacker-script"],"proof":"forged"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := server.loadDurableBrowserRecordingConversion("tampered-recording"); err == nil || !strings.Contains(err.Error(), "proof does not match") {
		t.Fatalf("tampered locator error = %v, want proof mismatch", err)
	}
}

func TestHandleConvertBrowserRecordingSplitPreservesMarkedCapturedSecret(t *testing.T) {
	artifact := recordedBrowserArtifact()
	const captured = "split-captured-password"
	artifact.Steps[0].Masked = false
	artifact.Steps[0].MarkSecret = true
	artifact.Steps[0].Value = captured
	artifact.Markers = []recbrowser.Marker{
		{Kind: recbrowser.MarkerTransactionStart, Name: "Login", Timestamp: artifact.StartedAt.Add(500 * time.Millisecond)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Login", Timestamp: artifact.StartedAt.Add(1500 * time.Millisecond)},
		{Kind: recbrowser.MarkerTransactionStart, Name: "Checkout", Timestamp: artifact.StartedAt.Add(1750 * time.Millisecond)},
		{Kind: recbrowser.MarkerTransactionEnd, Name: "Checkout", Timestamp: artifact.StartedAt.Add(2500 * time.Millisecond)},
	}
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{"name":"Split Secret","split_at_transactions":true}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("convert status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), captured) {
		t.Fatalf("saved split-import response did not preserve captured secret: %s", resp.Body.String())
	}

	storedScripts := server.runner.ListScripts()
	if len(storedScripts) != 2 {
		t.Fatalf("stored split scripts = %d, want 2", len(storedScripts))
	}
	foundCaptured := false
	foundCapturedRead := false
	for _, stored := range storedScripts {
		if stored == nil || stored.Script == nil {
			continue
		}
		if !isCredentialPreservingImport(stored.Script) {
			t.Fatalf("split script lacks server-owned preservation policy: %#v", stored.Script.Metadata)
		}
		scriptHasCaptured := false
		for _, action := range stored.Script.Actions {
			if action.Type != "browser_fill" {
				continue
			}
			scriptHasCaptured = scriptHasCaptured || action.Value == captured
			if strings.HasPrefix(action.Value, "{{secret.") {
				t.Fatalf("split browser fill requires credential re-entry: %#v", action)
			}
		}
		foundCaptured = foundCaptured || scriptHasCaptured
		readReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+stored.ID, nil)
		readResp := httptest.NewRecorder()
		server.mux.ServeHTTP(readResp, readReq)
		if readResp.Code != http.StatusOK {
			t.Fatalf("stored read status = %d body=%s", readResp.Code, readResp.Body.String())
		}
		foundCapturedRead = foundCapturedRead || strings.Contains(readResp.Body.String(), captured)
		if scriptHasCaptured && !strings.Contains(readResp.Body.String(), captured) {
			t.Fatalf("credential-bearing split-script read did not preserve captured secret: %s", readResp.Body.String())
		}
	}
	if !foundCaptured || !foundCapturedRead {
		t.Fatalf("no durable split script and read retained %q", captured)
	}
}

func TestHandleConvertBrowserRecordingAtomicallyAppliesExplicitSecretBinding(t *testing.T) {
	artifact := recordedBrowserArtifact()
	const captured = "operator-selected-browser-password"
	artifact.Steps[0].Masked = false
	artifact.Steps[0].MarkSecret = true
	artifact.Steps[0].Value = captured
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	previewReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert/preview",
		bytes.NewBufferString(`{"name":"Bound Browser Secret"}`),
	)
	previewReq.Header.Set("Content-Type", "application/json")
	previewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview struct {
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	var selected SecretBindingRequirement
	for _, requirement := range preview.Requirements {
		if requirement.DestinationValuePreserved && requirement.Path == "/actions/2/value" {
			selected = requirement
			break
		}
	}
	if selected.ID == "" {
		t.Fatalf("preview requirements = %#v, want preserved browser-fill value", preview.Requirements)
	}

	requestBody, err := json.Marshal(map[string]any{
		"name": "Bound Browser Secret",
		"secret_bindings": []map[string]any{{
			"requirement_id": selected.ID,
			"secret_name":    selected.SuggestedName,
			"script_index":   selected.ScriptIndex,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	saveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewReader(requestBody),
	)
	saveReq.Header.Set("Content-Type", "application/json")
	saveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(saveResp, saveReq)
	if saveResp.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%s", saveResp.Code, saveResp.Body.String())
	}
	if strings.Contains(saveResp.Body.String(), captured) {
		t.Fatalf("save response disclosed captured credential: %s", saveResp.Body.String())
	}

	stored := server.runner.ListScripts()
	if len(stored) != 1 || stored[0] == nil || stored[0].Script == nil {
		t.Fatalf("stored scripts = %#v, want one script", stored)
	}
	wantReference := "{{secret." + selected.SuggestedName + "}}"
	foundReference := false
	for _, action := range stored[0].Script.Actions {
		if action.Type != "browser_fill" {
			continue
		}
		if action.Value == captured {
			t.Fatalf("explicit project-secret choice was ignored in durable script: %#v", action)
		}
		foundReference = foundReference || action.Value == wantReference
	}
	if !foundReference {
		t.Fatalf("durable script does not contain selected reference %q: %#v", wantReference, stored[0].Script.Actions)
	}
}

func TestHandleConvertBrowserRecordingRejectsStaleSecretBindingBeforePersisting(t *testing.T) {
	artifact := recordedBrowserArtifact()
	artifact.Steps[0].Masked = false
	artifact.Steps[0].MarkSecret = true
	artifact.Steps[0].Value = "stale-binding-browser-password"
	fake := newFakeBrowserRecorder(artifact)
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/convert",
		bytes.NewBufferString(`{
			"name":"Stale Browser Binding",
			"secret_bindings":[{
				"requirement_id":"secret-binding-from-an-old-preview",
				"secret_name":"LOGIN_PASSWORD",
				"script_index":0
			}]
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("save status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 0 {
		t.Fatalf("stale secret binding persisted %d script(s), want none", got)
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

	malformed := postBrowserRecorderControl(t, server, id, "notes", `{"text":"missing brace"`)
	if malformed.Code != http.StatusBadRequest || !strings.Contains(malformed.Body.String(), "invalid request body") {
		t.Fatalf("malformed marker status = %d body=%s, want JSON syntax error", malformed.Code, malformed.Body.String())
	}

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

func TestHandleVerifyBrowserRecordingUsesClientReplaySessionID(t *testing.T) {
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	server := newBrowserRecorderTestServer(t, fake)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	const replaySessionID = "8f28ef09-cf20-4c28-9b85-ff9b0b6f6440"
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+id+"/verify",
		bytes.NewBufferString(`{"replay_session_id":"`+replaySessionID+`"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("verify status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var payload struct {
		ReplaySessionID string `json:"replay_session_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if payload.ReplaySessionID != replaySessionID {
		t.Fatalf("replay_session_id = %q, want %q", payload.ReplaySessionID, replaySessionID)
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
		t.Errorf("active = %+v, want the live recording summary; body=%s", payload.Browser.Active, resp.Body.String())
	}
}

func TestVerifyBrowserRecordingUsesSelectedConversion(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"selected name", `{"conversion":{"name":"Reviewed journey","parameterize":true},"script_index":0}`, http.StatusCreated},
		{"negative output", `{"script_index":-1}`, http.StatusConflict},
		{"missing output", `{"script_index":9}`, http.StatusConflict},
		{"changed output count", `{"conversion":{"expected_script_count":9}}`, http.StatusConflict},
		{"stale parameter choice", `{"conversion":{"parameterization_selections":[{"script_index":0,"selected_ids":["missing-choice"]}]}}`, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newBrowserRecorderTestServer(t, newFakeBrowserRecorder(recordedBrowserArtifact()))
			id := startBrowserRecordingViaAPI(t, server)
			stopBrowserRecordingViaAPI(t, server, id)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+id+"/verify", strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == http.StatusCreated && !strings.Contains(response.Body.String(), "Reviewed journey") {
				t.Fatalf("selected conversion name missing: %s", response.Body.String())
			}
		})
	}
}
