package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A body-less stored-script debug start names the revision the editor shows.
// When the stored script has moved on, the start is refused rather than
// running actions the editor's breakpoints and evidence do not describe.
func TestStartScriptDebugStoredScriptPinsExpectedContentRevision(t *testing.T) {
	var mu sync.Mutex
	var targets []string
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		mu.Lock()
		targets = append(targets, action.Target)
		mu.Unlock()
		return &protocol.Result{StatusCode: 200, Success: true}, nil
	})
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "debug-revision.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store})
	script := &dsl.Script{
		ID: "debug-revision-script", Name: "Debug revision", Version: "1.0.0", Protocol: protocolName,
		ProjectID: storage.DefaultProjectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://stored.example.test"},
		Actions:   []dsl.Action{{Name: "step-1", Type: "GET", Method: "GET", Target: "/r1"}},
	}
	first, err := server.runner.CreateScriptAuthoringDurable(script)
	if err != nil {
		t.Fatal(err)
	}
	revised := *first.Script
	revised.Actions = []dsl.Action{{Name: "step-0", Type: "GET", Method: "GET", Target: "/r2-inserted"}, {Name: "step-1", Type: "GET", Method: "GET", Target: "/r1"}}
	second, _, err := server.runner.UpdateScriptAuthoringDurable(first.ID, &revised, scriptAuthoringPrecondition(first))
	if err != nil {
		t.Fatal(err)
	}
	if second.ContentRevision == first.ContentRevision {
		t.Fatal("the update did not advance the content revision")
	}
	path := "/api/v1/scripts/" + first.ID + "/debug/sessions"

	stale := authoringAPIRequest(t, server, http.MethodPost, path, map[string]any{
		"replay_session_id": "66666666-6666-4666-8666-666666666661", "expected_content_revision": first.ContentRevision,
	}, "")
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), scriptDebugRevisionChangedCode) {
		t.Fatalf("a start at the superseded revision: %d %s", stale.Code, stale.Body.String())
	}
	if _, ok := server.scriptDebug.Get("66666666-6666-4666-8666-666666666661"); ok {
		t.Fatal("a refused start still created a debug session")
	}

	// The replay-session route takes the same request and the same pin.
	staleReplay := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+first.ID+"/replay/sessions", map[string]any{
		"replay_session_id": "66666666-6666-4666-8666-666666666662", "expected_content_revision": first.ContentRevision,
	}, "")
	if staleReplay.Code != http.StatusConflict {
		t.Fatalf("a replay session at the superseded revision: %d %s", staleReplay.Code, staleReplay.Body.String())
	}

	current := authoringAPIRequest(t, server, http.MethodPost, path, map[string]any{
		"replay_session_id": "66666666-6666-4666-8666-666666666663", "expected_content_revision": second.ContentRevision,
	}, "")
	if current.Code != http.StatusOK {
		t.Fatalf("a start at the current revision: %d %s", current.Code, current.Body.String())
	}
	waitForScriptDebugStatus(t, server.scriptDebug, "66666666-6666-4666-8666-666666666663", ScriptDebugCompleted)
	mu.Lock()
	got := strings.Join(targets, ",")
	mu.Unlock()
	if got != "/r2-inserted,/r1" {
		t.Fatalf("executed targets = %q, want the pinned revision's actions", got)
	}
}
