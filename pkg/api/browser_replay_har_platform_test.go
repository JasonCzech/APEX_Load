package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
)

// A replay asked to record a native HAR where native HAR capture is not
// supported is refused with 422 before anything starts, through both the
// draft and the stored-script routes; one that does not ask still starts.
// Where the platform itself refuses native HAR nothing is replaced, so the
// test exercises the platform's real answer.
func TestBrowserReplayRecordHARRefusedWithoutNativeHAR(t *testing.T) {
	if browser.NativeHARSupported() {
		previous := browserNativeHARSupported
		browserNativeHARSupported = func() bool { return false }
		t.Cleanup(func() { browserNativeHARSupported = previous })
	}
	server := NewServer(ServerConfig{})
	t.Cleanup(server.browserReplay.Close)
	reservations := 0
	server.browserReplay.setSessionIDReserver(func(ctx context.Context, id, project string) (string, func(), *replaySessionLeaseSignal, error) {
		reservations++
		return server.reserveReplaySessionIDContext(ctx, id, project)
	})
	script := map[string]any{
		"name": "Replay HAR platform", "version": "1.0.0", "protocol": "browser",
		"config":  map[string]any{"browser": map[string]any{"engine": "chromium"}},
		"actions": []map[string]any{{"name": "capture", "type": "browser_screenshot"}},
	}
	stored := server.runner.StoreScript(&dsl.Script{
		Name: "Stored replay HAR platform", Version: "1.0.0", Protocol: "browser",
		Config:  dsl.ScriptConfig{Browser: dsl.BrowserConfig{Engine: "chromium"}},
		Actions: []dsl.Action{{Name: "capture", Type: "browser_screenshot"}},
	})
	routes := map[string]struct {
		path string
		body map[string]any
	}{
		"draft":  {"/api/v1/scripts/browser-replay/sessions", map[string]any{"project_id": "project-har-platform", "script": script}},
		"stored": {"/api/v1/scripts/" + stored.ID + "/browser-replay/sessions", map[string]any{}},
	}
	for name, route := range routes {
		t.Run(name, func(t *testing.T) {
			start := func(record *bool) *httptest.ResponseRecorder {
				t.Helper()
				request := map[string]any{"start_paused": true}
				for key, value := range route.body {
					request[key] = value
				}
				if record != nil {
					request["record_har"] = *record
				}
				body, _ := json.Marshal(request)
				req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				resp := httptest.NewRecorder()
				server.mux.ServeHTTP(resp, req)
				return resp
			}

			before := reservations
			refused := start(ptr(true))
			if refused.Code != http.StatusUnprocessableEntity {
				t.Fatalf("record_har: status %d body=%s, want 422", refused.Code, refused.Body.String())
			}
			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(refused.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Error, "record_har is not available") || !strings.Contains(payload.Error, runtime.GOOS) || !strings.Contains(payload.Error, "without record_har") {
				t.Fatalf("refusal message %q", payload.Error)
			}
			if after := reservations; after != before {
				t.Fatalf("a refused replay attempted a reservation; count changed from %d to %d", before, after)
			}

			for _, record := range []*bool{nil, ptr(false)} {
				resp := start(record)
				if resp.Code != http.StatusOK {
					t.Fatalf("record_har %v: status %d body=%s", record, resp.Code, resp.Body.String())
				}
				var started BrowserReplayStartResponse
				if err := json.Unmarshal(resp.Body.Bytes(), &started); err != nil {
					t.Fatal(err)
				}
				if _, ok := server.browserReplay.Stop(started.ReplaySessionID); !ok {
					t.Fatalf("record_har %v: no session %q to stop", record, started.ReplaySessionID)
				}
				// The stopped worker settles before the next start, so no
				// replay outlives the test.
				waitBrowserReplayTerminal(t, server.browserReplay, started.ReplaySessionID, 60*time.Second)
			}
		})
	}
}
