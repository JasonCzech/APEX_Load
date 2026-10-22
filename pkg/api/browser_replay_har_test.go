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
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/recordbridge"
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
		// Terminal and settled: the worker has recorded its final state, so
		// the session's HAR is final (a Stop is terminal before that).
		if replaySessionStatusTerminal(session.Status) && session.WorkerSettled() {
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

// A browser replay asked to record (record_har) records a native HAR, keeps a
// private copy, retires the HAR directory's original and drops the copy once
// the session is pruned; one not asked serves the synthetic HAR.
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
	advanceClock := installReplayHARFixtureClock(t, server)
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
	copied := retainedReplayHARFixturePath(t, server, "replay-native-true")
	waitForBrowserReplayFixture(t, server.browserReplay, "replay-native-true")
	advanceClock(48 * time.Hour)
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
	onInit    func(map[string]any) error
}

func (p *replayHARProtocol) Init(_ context.Context, config map[string]any) error {
	if p.onInit != nil {
		return p.onInit(config)
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

func registerReplayHARProtocol(t *testing.T, harDir string, publish func() []string, onInit func(map[string]any) error) string {
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
// reusing the ID never serves it, removing the record removes the local copy, the
// evidence ZIP streams it with its digest, and a replay that fails before
// reporting retires its recording instead of leaving it published.
func TestBrowserReplayHARCopiesBelongToTheirRecord(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	advanceClock := installReplayHARFixtureClock(t, server)
	harDir := t.TempDir()
	published := ""
	var captureMu sync.Mutex
	captures := map[string]any{}
	protocolName := registerReplayHARProtocol(t, harDir, func() []string {
		if published == "" {
			return nil
		}
		return []string{published}
	}, func(config map[string]any) error {
		captureMu.Lock()
		defer captureMu.Unlock()
		captures["browser_har_capture"], captures["browser_har_capture_policy"] = config["browser_har_capture"], config["browser_har_capture_policy"]
		return nil
	})
	script := &dsl.Script{Name: "Replay HAR copy", Protocol: protocolName, Version: "1.0.0", ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	published = publishFakeReplayHAR(t, harDir, "first", "first-attempt")
	on := true
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-reuse", RecordHAR: &on}); err != nil {
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
	first := retainedReplayHARFixturePath(t, server, "replay-har-reuse")
	// Expiring the local record removes its copy, but the durable receipt still
	// owns the ID and native bytes until database-clock expiry and exact cleanup.
	waitForBrowserReplayFixture(t, server.browserReplay, "replay-har-reuse")
	advanceClock(48 * time.Hour)
	server.browserReplay.PruneExpired()
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("a retired record's copy stayed: %v", err)
	}
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-reuse"}); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatal("cache eviction released a retained artifact namespace", err)
	}
	if reply := harDownloadRequest(t, server, base+"/har", "replay-editor", harEditor); reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" || !strings.Contains(reply.Body.String(), "first-attempt") {
		t.Fatalf("cache eviction lost durable native evidence: %d", reply.Code)
	}
	base = "/api/v1/browser-replay/sessions/replay-har-next"
	published = ""
	captureMu.Lock()
	captures = map[string]any{}
	captureMu.Unlock()
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-next"}); err != nil {
		t.Fatal(err)
	}
	waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-next", 5*time.Second)
	captureMu.Lock()
	if captures["browser_har_capture"] != nil || captures["browser_har_capture_policy"] != nil {
		t.Fatalf("an unrequested recording forced the capture policy: %+v", captures)
	}
	captureMu.Unlock()
	if reply := harDownloadRequest(t, server, base+"/har", "replay-editor", harEditor); reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != harsynth.Fidelity || strings.Contains(reply.Body.String(), "first-attempt") {
		t.Fatalf("a new replay served the earlier record's HAR: %d %s", reply.Code, reply.Header().Get("X-APEXLoad-HAR-Fidelity"))
	}

	// A replay that fails before reporting (a data source that cannot load)
	// retires its recording.
	published = publishFakeReplayHAR(t, harDir, "failed", "failed-attempt")
	broken := *script
	broken.DataSources = []dsl.DataSource{{Name: "rows", Type: "csv", Path: filepath.Join(t.TempDir(), "missing.csv")}}
	if _, err := server.browserReplay.Start("", &broken, BrowserReplayStartRequest{ReplaySessionID: "replay-har-early", RecordHAR: &on}); err != nil {
		t.Fatal(err)
	}
	if session := waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-early", 5*time.Second); session.Status != BrowserReplayFailed {
		t.Fatalf("broken replay %s", session.Status)
	}
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("an unreported recording stayed published: %v", err)
	}
}

// A pruned record's copy is removed by the janitor's prune; a replay whose
// protocol fails to start retires its recording; downloads are bounded and
// release their slot.
func TestBrowserReplayHARPruneInitFailureAndDownloadSlots(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	advanceClock := installReplayHARFixtureClock(t, server)
	harDir := t.TempDir()
	published := ""
	failInit := false
	protocolName := registerReplayHARProtocol(t, harDir, func() []string {
		if published == "" {
			return nil
		}
		return []string{published}
	}, func(map[string]any) error {
		if failInit {
			return errors.New("browser did not start")
		}
		return nil
	})
	script := &dsl.Script{Name: "Replay HAR prune", Protocol: protocolName, Version: "1.0.0", ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	published = publishFakeReplayHAR(t, harDir, "prune", "pruned")
	on := true
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-prune", RecordHAR: &on}); err != nil {
		t.Fatal(err)
	}
	waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-prune", 5*time.Second)
	base := "/api/v1/browser-replay/sessions/replay-har-prune"

	// Every download slot is taken: the next download waits its turn.
	releaseSlots := acquireAllReplayHARFixtureSlots(t, server.browserReplay)
	busy := harDownloadRequest(t, server, base+"/har", "replay-editor", harEditor)
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("Retry-After") == "" {
		t.Fatalf("a download beyond the slots: %d %v", busy.Code, busy.Header())
	}
	releaseSlots()
	for path, want := range map[string]int{base + "/har": http.StatusOK, base + "/evidence": http.StatusOK} {
		if got := harDownloadRequest(t, server, path, "replay-editor", harEditor); got.Code != want {
			t.Fatalf("%s: %d, want %d", path, got.Code, want)
		}
	}
	// A refused request (an unknown session) holds no slot either.
	if got := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/missing/har", "replay-editor", harEditor); got.Code == http.StatusOK {
		t.Fatal("an unknown session's HAR was served")
	}
	acquireAllReplayHARFixtureSlots(t, server.browserReplay)()

	copied := retainedReplayHARFixturePath(t, server, "replay-har-prune")
	waitForBrowserReplayFixture(t, server.browserReplay, "replay-har-prune")
	advanceClock(48 * time.Hour)
	if removed := server.browserReplay.PruneExpired(); removed != 1 {
		t.Fatalf("pruned %d, want 1", removed)
	}
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Fatalf("a pruned record's copy stayed: %v", err)
	}

	// The protocol fails to start after its recording opened.
	failInit = true
	published = publishFakeReplayHAR(t, harDir, "initfail", "init-failed")
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-initfail", RecordHAR: &on}); err != nil {
		t.Fatal(err)
	}
	if session := waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-initfail", 5*time.Second); session.Status != BrowserReplayFailed {
		t.Fatalf("init failure %s", session.Status)
	}
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("a recording from a failed start stayed published: %v", err)
	}
}

// Right after Stop the session is terminal while its worker is still closing
// the browser and retaining the HAR: a download is a conflict to retry, not
// "no HAR", until the worker has recorded its final state.
func TestBrowserReplayHARWaitsForTheWorker(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	protocolName := registerReplayHARProtocol(t, t.TempDir(), func() []string { return nil }, func(map[string]any) error {
		close(entered)
		<-release
		return errors.New("fixture initialization failed after cancellation")
	})
	session, err := server.browserReplay.Start("", &dsl.Script{Name: "Closing replay", Protocol: protocolName, ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "request", Target: "/"}}}, BrowserReplayStartRequest{ReplaySessionID: "replay-har-closing"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not enter initialization")
	}
	server.browserReplay.Stop(session.ID)
	for _, suffix := range []string{"/har", "/evidence"} {
		reply := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/replay-har-closing"+suffix, "replay-editor", harEditor)
		if reply.Code != http.StatusConflict || reply.Header().Get("Retry-After") == "" {
			t.Fatalf("%s while the worker closes: %d %s", suffix, reply.Code, reply.Body)
		}
	}
	// Let the real worker complete its failed initialization. Until this
	// point the HTTP route must distinguish pending artifacts from absence.
	unblock()
	waitForBrowserReplayFixture(t, server.browserReplay, session.ID)
	if reply := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/replay-har-closing/har", "replay-editor", harEditor); reply.Code != http.StatusNotFound {
		t.Fatalf("a settled replay without a report or recording: %d %s", reply.Code, reply.Body)
	}
}

// A completed replay's HAR is final when the worker records the terminal
// state: a download during its completion hook (the self-heal work, which
// runs before the worker finishes) is served, not refused as still closing.
func TestBrowserReplayHARServedDuringTheCompletionHook(t *testing.T) {
	server, _ := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	harDir := t.TempDir()
	published := publishFakeReplayHAR(t, harDir, "hooked", "hooked-recording")
	protocolName := registerReplayHARProtocol(t, harDir, func() []string { return []string{published} }, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	server.browserReplay.setCompletionHook(func(context.Context, string, *dsl.Script, *engine.ReplayReport) {
		close(entered)
		<-release
	})
	script := &dsl.Script{Name: "Replay HAR hook", Protocol: protocolName, Version: "1.0.0", ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	on := true
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-hooked", RecordHAR: &on}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the completion hook never ran")
	}
	reply := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/replay-har-hooked/har", "replay-editor", harEditor)
	if reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" || !strings.Contains(reply.Body.String(), "hooked-recording") {
		t.Fatalf("download during the completion hook: %d %s", reply.Code, reply.Body)
	}
	close(release)
}

// A replay started without record_har whose script's own capture policy
// recorded a HAR: the replay serves it as its native HAR (the policy is not
// forced) and the HAR directory keeps nothing. A script debug session and a
// classic replay, which offer no HAR, retire it.
func TestReplayHARsRecordedByTheScriptsOwnPolicy(t *testing.T) {
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
	}, func(config map[string]any) error {
		captureMu.Lock()
		defer captureMu.Unlock()
		captures["browser_har_capture"] = config["browser_har_capture"]
		return nil
	})
	script := &dsl.Script{Name: "Script policy HAR", Protocol: protocolName, Version: "1.0.0", ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	published = publishFakeReplayHAR(t, harDir, "policy", "script-policy-recording")
	if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "replay-har-policy"}); err != nil {
		t.Fatal(err)
	}
	waitBrowserReplayTerminal(t, server.browserReplay, "replay-har-policy", 5*time.Second)
	captureMu.Lock()
	forced := captures["browser_har_capture"]
	captureMu.Unlock()
	if forced != nil {
		t.Fatalf("an unrequested recording forced the capture policy: %v", forced)
	}
	reply := harDownloadRequest(t, server, "/api/v1/browser-replay/sessions/replay-har-policy/har", "replay-editor", harEditor)
	if reply.Code != http.StatusOK || reply.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" || !strings.Contains(reply.Body.String(), "script-policy-recording") {
		t.Fatalf("a script-recorded replay HAR: %d %s", reply.Code, reply.Header().Get("X-APEXLoad-HAR-Fidelity"))
	}
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("the HAR directory kept the replay's original: %v", err)
	}

	published = publishFakeReplayHAR(t, harDir, "debug", "debug-recording")
	server.scriptDebug.Start("", script, ScriptDebugStartRequest{ReplaySessionID: "debug-har-policy"})
	waitForScriptDebugStatus(t, server.scriptDebug, "debug-har-policy", ScriptDebugCompleted)
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("the HAR directory kept a debug session's recording: %v", err)
	}

	// A classic replay offers no HAR either: it retires the recording.
	published = publishFakeReplayHAR(t, harDir, "classic", "classic-recording")
	classicScript := *script
	classicScript.Config.BaseURL = "https://shop.example.test"
	classic := httptest.NewRecorder()
	server.handleReplayRequestContext(context.Background(), classic, "", &classicScript, ReplayScriptRequest{ReplaySessionID: uuid.NewString()})
	if classic.Code != http.StatusOK {
		t.Fatalf("classic replay: %d %s", classic.Code, classic.Body)
	}
	if _, err := os.Stat(published); !os.IsNotExist(err) {
		t.Fatalf("the HAR directory kept a classic replay's recording: %v", err)
	}
}

func installReplayHARFixtureClock(t *testing.T, server *Server) func(time.Duration) {
	t.Helper()
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	retention := recordbridge.DefaultReplayRetentionConfig()
	retention.Now = func() time.Time { return time.Unix(0, now.Load()) }
	replaceReplayFixtureManager(t, server, recordbridge.ReplayManagerOptions{Retention: &retention})
	return func(delta time.Duration) { now.Add(int64(delta)) }
}

func retainedReplayHARFixturePath(t *testing.T, server *Server, id string) string {
	t.Helper()
	session, ok := server.browserReplay.Get(id)
	if !ok {
		t.Fatal("replay session disappeared")
	}
	har, ok, err := server.browserReplay.ReplayHARFor(session)
	if err != nil || !ok {
		t.Fatalf("retained HAR: %v, %v", ok, err)
	}
	defer har.Close()
	file, ok := har.Reader().(*os.File)
	if !ok {
		t.Fatal("native HAR is not backed by its retained file")
	}
	return file.Name()
}

func acquireAllReplayHARFixtureSlots(t *testing.T, manager *BrowserReplayManager) func() {
	t.Helper()
	releases := make([]func(), 0, 4)
	for i := 0; i < 4; i++ {
		release, ok := manager.AcquireHARDownload()
		if !ok {
			for _, release := range releases {
				release()
			}
			t.Fatalf("only %d download slots available", i)
		}
		releases = append(releases, release)
	}
	if release, ok := manager.AcquireHARDownload(); ok {
		release()
		for _, release := range releases {
			release()
		}
		t.Fatal("download bound was exceeded")
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			for _, release := range releases {
				release()
			}
		})
	}
	t.Cleanup(release)
	return release
}
