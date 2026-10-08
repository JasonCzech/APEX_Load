package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	server.browserReplay.mu.RLock()
	copied := server.browserReplay.sessions["replay-native-true"].harCopy
	server.browserReplay.mu.RUnlock()
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("no private copy: %v", err)
	}
	server.browserReplay.mu.Lock()
	delete(server.browserReplay.sessions, "replay-native-true")
	server.browserReplay.mu.Unlock()
	server.browserReplay.PruneExpired()
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("a pruned replay's HAR copy stayed: %v", err)
	}
}

// replayHARProtocol is a replay protocol that "records" a published HAR: it
// hands over fake published artifacts the way the browser does.
type replayHARProtocol struct {
	replayAPIProtocol
	mu        sync.Mutex
	artifacts []protocol.BrowserHARArtifact
	onInit    func(map[string]any)
}

func (p *replayHARProtocol) Init(_ context.Context, config map[string]any) error {
	if p.onInit != nil {
		p.onInit(config)
	}
	return nil
}

func (p *replayHARProtocol) TakeBrowserHARArtifacts() []protocol.BrowserHARArtifact {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.artifacts
	p.artifacts = nil
	return out
}

// publishFakeReplayHAR writes a published-looking scrubbed HAR into dir.
func publishFakeReplayHAR(t *testing.T, dir, name, page string) string {
	t.Helper()
	path := filepath.Join(dir, "apexload_browser_"+name+"_scrubbed.har")
	body := `{"log":{"version":"1.2","creator":{"name":"apexload","version":"test"},"pages":[{"id":"` + page + `"}],"entries":[]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func registerReplayHARProtocol(t *testing.T, harDir string, publish func() []string, onInit func(map[string]any)) string {
	t.Helper()
	name := "replay-har-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-") + "-" + strconv.FormatUint(replayAPIProtocolCounter.Add(1), 10)
	protocol.Register(name, func() protocol.Protocol {
		p := &replayHARProtocol{replayAPIProtocol: replayAPIProtocol{execute: func(action *protocol.Action) (*protocol.Result, error) {
			return &protocol.Result{ActionName: action.Name, Success: true}, nil
		}}, onInit: onInit}
		for _, path := range publish() {
			p.artifacts = append(p.artifacts, protocol.BrowserHARArtifact{Path: path})
		}
		return p
	})
	return name
}

// A replay's native HAR copy belongs to its session record: a later record
// reusing the ID never serves it, removing the record removes the copy, the
// evidence ZIP streams it with its digest, and a replay that fails before
// reporting retires its recording instead of leaving it published.
func TestBrowserReplayHARCopiesBelongToTheirRecord(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	harDir := t.TempDir()
	published := ""
	var captureMu sync.Mutex
	captures := map[string]any{}
	protocolName := registerReplayHARProtocol(t, harDir, func() []string {
		if published == "" {
			return nil
		}
		return []string{published}
	}, func(config map[string]any) {
		captureMu.Lock()
		defer captureMu.Unlock()
		captures["browser_har_capture"], captures["browser_har_capture_policy"] = config["browser_har_capture"], config["browser_har_capture_policy"]
	})
	script := &dsl.Script{Name: "Replay HAR copy", Protocol: protocolName, Version: "1.0.0", ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	published = publishFakeReplayHAR(t, harDir, "first", "first-attempt")
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-reuse"}); err != nil {
		t.Fatal(err)
	}
	waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-reuse", 5*time.Second)
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("the HAR directory kept the replay's original: %v", err)
	}
	// Recording sets both capture aliases, so a script's own on_error policy
	// cannot discard a successful replay's recording.
	captureMu.Lock()
	if captures["browser_har_capture"] != "always" || captures["browser_har_capture_policy"] != "always" {
		t.Fatalf("capture config %+v", captures)
	}
	captureMu.Unlock()
	base := "/api/v1/browser-replay/sessions/replay-har-reuse"
	reply := harDownloadRequest(t, server, base+"/har", "replay-editor", harEditor)
	if reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" || !strings.Contains(reply.Body.String(), "first-attempt") {
		t.Fatalf("native replay HAR %d %v %s", reply.Code, reply.Header(), reply.Body.String())
	}
	evidence := harDownloadRequest(t, server, base+"/evidence", "replay-editor", harEditor)
	entries := harZipEntries(t, evidence.Body.Bytes())
	var metadata browserReplayHARMetadata
	sum := sha256.Sum256(entries["replay.har"])
	if err := json.Unmarshal(entries["har.json"], &metadata); err != nil || metadata.Fidelity != "native" || metadata.SHA256 != hex.EncodeToString(sum[:]) || metadata.Bytes != int64(len(entries["replay.har"])) || string(entries["replay.har"]) != reply.Body.String() {
		t.Fatalf("streamed evidence %+v %v", metadata, err)
	}
	server.browserReplay.mu.RLock()
	record := server.browserReplay.sessions["replay-har-reuse"]
	server.browserReplay.mu.RUnlock()
	first := record.harCopy

	// The record is retired (its fence was lost), which removes its copy, and
	// a retry reuses the ID without recording: it has its own synthetic HAR.
	record.mu.Lock()
	record.terminalFenceEstablished = false
	record.mu.Unlock()
	server.browserReplay.retireUnfencedSession(record)
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("a retired record's copy stayed: %v", err)
	}
	published = ""
	off := false
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-reuse", RecordHAR: &off}); err != nil {
		t.Fatal(err)
	}
	waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-reuse", 5*time.Second)
	if reply := harDownloadRequest(t, server, base+"/har", "replay-editor", harEditor); reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != harsynth.Fidelity || strings.Contains(reply.Body.String(), "first-attempt") {
		t.Fatalf("a reused ID served the earlier record's HAR: %d %s", reply.Code, reply.Header().Get("X-APEXLoad-HAR-Fidelity"))
	}

	// A replay that fails before reporting (a data source that cannot load)
	// retires its recording.
	published = publishFakeReplayHAR(t, harDir, "failed", "failed-attempt")
	broken := *script
	broken.DataSources = []dsl.DataSource{{Name: "rows", Type: "csv", Path: filepath.Join(t.TempDir(), "missing.csv")}}
	if _, err := server.browserReplay.Start("", &broken, BrowserReplayStartRequest{ReplaySessionID: "replay-har-early"}); err != nil {
		t.Fatal(err)
	}
	if session := waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-early", 5*time.Second); session.Status != BrowserReplayFailed {
		t.Fatalf("broken replay %s", session.Status)
	}
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("an unreported recording stayed published: %v", err)
	}
}

// A copy is kept only when asked (a fenced, live record); the original is
// retired either way, and a closed store refuses writes.
func TestReplayHARStoreKeepsOnlyFencedCopies(t *testing.T) {
	manager := NewBrowserReplayManager(nil)
	harDir := t.TempDir()
	original := publishFakeReplayHAR(t, harDir, "unfenced", "unfenced")
	record := &BrowserReplaySession{ID: "unfenced"}
	if copied := manager.retainReplayHAR(record, []protocol.BrowserHARArtifact{{Path: original}}, false); copied != "" {
		t.Fatalf("an unfenced record kept %s", copied)
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatalf("the original was not retired: %v", err)
	}
	original = publishFakeReplayHAR(t, harDir, "kept", "kept")
	copied := manager.retainReplayHAR(record, []protocol.BrowserHARArtifact{{Path: original}}, true)
	if copied == "" || !strings.HasPrefix(copied, filepath.Join(os.TempDir(), replayHARRootName)) {
		t.Fatalf("copy %q outside the managed root", copied)
	}
	manager.Close()
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("close kept the copy: %v", err)
	}
	if _, err := manager.har.write(record, strings.NewReader("{}"), 2); !errors.Is(err, errReplayHARStoreClosed) {
		t.Fatalf("a closed store accepted a write: %v", err)
	}
}
