package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/recordbridge"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
)

func TestStandaloneSavedRecordingMatchesControllerMemoryContract(t *testing.T) {
	artifact := func() *recbrowser.Session {
		return &recbrowser.Session{ID: "source-recording", StartURL: "https://example.test", StartedAt: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), Steps: []recbrowser.Step{{Seq: 1, Kind: recbrowser.StepNavigate, URL: "https://example.test/orders"}}}
	}
	controller := newBrowserRecorderTestServer(t, newFakeBrowserRecorder(artifact()))
	// The original slim bridge built a nil-store runner directly and did not
	// configure the full controller's environment secret provider. Keep that
	// historical constructor state while exercising its actual save adapter.
	controller.projectSecretStore = nil
	bridge := newBrowserRecorderBridgeTestServer(t, newFakeBrowserRecorder(artifact()))
	pair := pairBridgeForTest(t, bridge, bridgeTestOrigin)
	invoke := func(server *Server, manager *recordbridge.BrowserRecordingManager, paired bool) map[string]any {
		t.Helper()
		session, err := manager.Start(recordbridge.BrowserRecordingStartRequest{Name: "Orders", StartURL: "https://example.test", ProjectID: "project-a"})
		if err != nil {
			t.Fatal(err)
		}
		manager.Stop(session.ID)
		waitForRecordingStatus(t, manager, session.ID, BrowserRecordingStopped)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/browser-recorder/sessions/"+session.ID+"/convert", strings.NewReader(`{"name":"Saved orders","expected_script_count":1}`))
		r.Header.Set("Content-Type", "application/json")
		if paired {
			r.Header.Set("Origin", bridgeTestOrigin)
			r.Header.Set(bridgeTokenHeader, bridgeTestToken)
			r.Header.Set(bridgeSessionHeader, pair)
		}
		w := httptest.NewRecorder()
		server.mux.ServeHTTP(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("save %t=%d %s", paired, w.Code, w.Body.String())
		}
		var response map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		normalizeRecordingParity(response)
		return response
	}
	old := invoke(controller, controller.browserRecording, false)
	shared := invoke(bridge, bridge.recordingRuntime.RecordingManager(), true)
	if !reflect.DeepEqual(old, shared) {
		left, _ := json.MarshalIndent(old, "", "  ")
		right, _ := json.MarshalIndent(shared, "", "  ")
		t.Fatalf("standalone saved conversion drift\ncontroller=%s\nbridge=%s", left, right)
	}
}

func normalizeRecordingParity(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			switch key {
			case "id", "created_at", "updated_at", "recorded_at", "browser_recorder_save_ref", "browser_recorder_save_actor_hash", "browser_recorder_save_proof":
				delete(v, key)
			default:
				normalizeRecordingParity(child)
			}
		}
	case []any:
		for _, child := range v {
			normalizeRecordingParity(child)
		}
	}
}

func TestBridgeServerCompatibilityFacadeOwnsLifecycle(t *testing.T) {
	server := newBrowserRecorderBridgeTestServer(t, nil)
	if server.runner != nil || server.hub != nil || server.recordingRuntime == nil {
		t.Fatal("compatibility facade recreated controller owners")
	}
	done := make(chan error, 1)
	go func() { done <- server.Start() }()
	deadline := time.After(2 * time.Second)
	for !server.BoundAddrReady() {
		select {
		case err := <-done:
			t.Fatalf("start: %v", err)
		case <-deadline:
			t.Fatal("facade never bound")
		case <-time.After(time.Millisecond):
		}
	}
	if server.BrowserURL() == "" || server.BridgePairingToken() != bridgeTestToken || server.BridgePairingExpiresAt().IsZero() {
		t.Fatal("facade lost discovery/pairing methods")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("facade did not join HTTP server")
	}
}

func TestBridgeFacadePreservesNilServerLifecycleQueries(t *testing.T) {
	var server *Server
	if server.BoundAddr() != "" || server.BoundAddrReady() || server.BrowserURL() != "http://" {
		t.Fatal("nil lifecycle query changed")
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
