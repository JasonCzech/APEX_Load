package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func waitBrowserReplayTerminal(t *testing.T, manager *BrowserReplayManager, id string, timeout time.Duration) *BrowserReplaySession {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		session, ok := manager.Get(id)
		if !ok {
			t.Fatal("replay session not found")
		}
		if replaySessionStatusTerminal(session.Status) {
			return session
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("replay %s did not finish", id)
	return nil
}

// A replay without a native recording still has a synthetic HAR built from
// its report's network entries; only editors download it, and only once the
// replay has ended.
func TestBrowserReplaySyntheticHARNeedsEditorsAndATerminalReplay(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, PageURL: "https://shop.example/", NetworkEntries: []protocol.NetworkEntry{{URL: "https://shop.example/?access_token=replayTokenQxZr", Method: "GET", StatusCode: 200, DurationMs: 14}}}, nil
	})
	script := &dsl.Script{Name: "Replay HAR", Protocol: protocolName, Version: "1.0.0", ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-synthetic"}); err != nil {
		t.Fatal(err)
	}
	waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-synthetic", 5*time.Second)
	base := "/api/v1/browser-replay/sessions/replay-har-synthetic"
	if reply := harDownloadRequest(t, server, base+"/har", "replay-viewer", harViewer); reply.Code != http.StatusForbidden {
		t.Fatalf("a viewer downloaded a replay HAR: %d", reply.Code)
	}
	if reply := harDownloadRequest(t, server, base+"/har", "replay-other", harOtherEditor); reply.Code != http.StatusForbidden && reply.Code != http.StatusNotFound {
		t.Fatalf("another project's editor downloaded a replay HAR: %d", reply.Code)
	}
	reply := harDownloadRequest(t, server, base+"/har", "replay-editor", harEditor)
	if reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != harsynth.Fidelity {
		t.Fatalf("replay HAR %d %v %s", reply.Code, reply.Header(), reply.Body.String())
	}
	syntheticHARSchemaValidate(t, reply.Body.Bytes())
	var doc syntheticHARDoc
	if err := json.Unmarshal(reply.Body.Bytes(), &doc); err != nil || len(doc.Log.Entries) != 1 || doc.Log.Apexload["capture_mode"] != harsynth.CaptureReplayNetworkEntries {
		t.Fatalf("replay HAR %+v %v", doc.Log.Apexload, err)
	}
	if strings.Contains(reply.Body.String(), "replayTokenQxZr") {
		t.Fatal("the replay HAR retains a token")
	}
	evidence := harDownloadRequest(t, server, base+"/evidence", "replay-editor", harEditor)
	if evidence.Code != http.StatusOK || evidence.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("replay evidence %d %v", evidence.Code, evidence.Header())
	}
	entries := harZipEntries(t, evidence.Body.Bytes())
	var metadata browserReplayHARMetadata
	if err := json.Unmarshal(entries["har.json"], &metadata); err != nil || metadata.Fidelity != harsynth.Fidelity || metadata.Entries == nil || *metadata.Entries != 1 || metadata.Bytes != int64(len(entries["replay.har"])) {
		t.Fatalf("har.json %+v %v", metadata, err)
	}
	if !strings.Contains(string(entries["report.json"]), `"replay-har-synthetic"`) || string(entries["replay.har"]) != reply.Body.String() {
		t.Fatal("the evidence ZIP lacks the report or its HAR")
	}
	if reply := harDownloadRequest(t, server, base+"/evidence", "replay-viewer", harViewer); reply.Code != http.StatusForbidden {
		t.Fatalf("a viewer downloaded replay evidence: %d", reply.Code)
	}
	// A running replay has no HAR yet.
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-running", StartPaused: true}); err != nil {
		t.Fatal(err)
	}
	if reply := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/replay-har-running/har", "replay-editor", harEditor); reply.Code != http.StatusConflict {
		t.Fatalf("a running replay's HAR: %d", reply.Code)
	}
	server.browserReplay.Stop("replay-har-running")
	// An unknown session is refused without saying whether it exists.
	if reply := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/missing/har", "replay-editor", harEditor); reply.Code != http.StatusNotFound && reply.Code != http.StatusForbidden {
		t.Fatalf("a missing replay's HAR: %d", reply.Code)
	}
}

// A browser replay records a native HAR, keeps a private copy, retires the
// HAR directory's original and drops the copy once the session is pruned.
func TestBrowserReplayActualNativeHAR(t *testing.T) {
	switch strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_TESTS")) {
	case "require":
	case "", "auto", "skip":
		t.Skip("native replay HAR qualification requires APEXLOAD_BROWSER_TESTS=require")
	default:
		t.Fatal("invalid APEXLOAD_BROWSER_TESTS value; use require, auto or skip")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>replayed</title><body>replayed page</body>`)
	}))
	t.Cleanup(fixture.Close)
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	harDir := t.TempDir()
	script := func(record bool) (*dsl.Script, BrowserReplayStartRequest) {
		return &dsl.Script{Name: "Native replay HAR", Protocol: "browser", Version: "1.0.0", ProjectID: storage.DefaultProjectID,
				Config:  dsl.ScriptConfig{BaseURL: fixture.URL, Browser: dsl.BrowserConfig{Engine: "chromium", Headless: ptr(true), HARArtifactDir: harDir}},
				Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}},
			BrowserReplayStartRequest{ReplaySessionID: fmt.Sprintf("replay-native-%v", record), RecordHAR: &record}
	}
	for _, record := range []bool{true, false} {
		s, req := script(record)
		if _, err := server.browserReplay.Start("", s, req); err != nil {
			t.Fatal(err)
		}
		session := waitBrowserReplayTerminal(t, server.browserReplay, req.ReplaySessionID, 90*time.Second)
		if session.Status != BrowserReplayCompleted {
			t.Fatalf("replay %s: %s %s", req.ReplaySessionID, session.Status, session.Error)
		}
		reply := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/"+req.ReplaySessionID+"/har", "replay-editor", harEditor)
		want := map[bool]string{true: "native", false: harsynth.Fidelity}[record]
		if reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != want || !strings.Contains(reply.Body.String(), fixture.URL) {
			t.Fatalf("record %v: %d %s %.300s", record, reply.Code, reply.Header().Get("X-APEXLoad-HAR-Fidelity"), reply.Body.String())
		}
	}
	// Nothing is left published in the HAR directory: the copy was taken and
	// the original retired.
	published, err := os.ReadDir(harDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range published {
		if strings.HasSuffix(entry.Name(), "_scrubbed.har") {
			t.Fatalf("the HAR directory kept %s", entry.Name())
		}
	}
	copied := server.browserReplay.har.path("replay-native-true")
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("no private copy: %v", err)
	}
	server.browserReplay.mu.Lock()
	delete(server.browserReplay.sessions, "replay-native-true")
	server.browserReplay.mu.Unlock()
	server.browserReplay.sweepReplayHARs()
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("a pruned replay's HAR copy stayed: %v", err)
	}
}
