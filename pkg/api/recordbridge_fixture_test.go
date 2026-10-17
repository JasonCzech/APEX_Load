package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/recordbridge"
)

// replaceReplayFixtureManager retains the real API reservation and completion
// adapters while injecting deterministic lifecycle budgets into the shared owner.
func replaceReplayFixtureManager(t *testing.T, server *Server, options recordbridge.ReplayManagerOptions) {
	t.Helper()
	server.browserReplay.Close()
	// Route authorization retains the API wrapper's identity. Replace its
	// unused underlying owner, preserving that wrapper for all route closures.
	server.browserReplay.BrowserReplayManager = NewBrowserReplayManager(server.hub, options).BrowserReplayManager
	server.browserReplay.setSessionIDReserver(server.reserveReplaySessionIDContext)
	server.browserReplay.setCompletionHook(server.persistSelfHealToRepository)
	t.Cleanup(server.browserReplay.Close)
}

func waitForBrowserReplayFixture(t *testing.T, manager *BrowserReplayManager, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := manager.Wait(ctx, id); err != nil {
		t.Fatalf("wait for browser replay %q: %v", id, err)
	}
}

func startPausedBrowserReplayFixture(t *testing.T, server *Server, id, projectID string) *BrowserReplaySession {
	t.Helper()
	session, err := server.browserReplay.Start("", &dsl.Script{Name: "Paused replay fixture", Protocol: "browser", ProjectID: projectID}, BrowserReplayStartRequest{ReplaySessionID: id, StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.browserReplay.Stop(session.ID)
		waitForBrowserReplayFixture(t, server.browserReplay, session.ID)
	})
	return session
}

// completeBrowserReplayFixture obtains retained network evidence through the
// real replay engine and manager instead of installing an internal map entry.
func completeBrowserReplayFixture(t *testing.T, server *Server, id string, success bool, entries []protocol.NetworkEntry) {
	t.Helper()
	name := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		result := &protocol.Result{ActionName: action.Name, Success: success, StatusCode: http.StatusOK, NetworkEntries: entries}
		if !success {
			result.ErrorMessage = "assertion failed"
		}
		return result, nil
	})
	session, err := server.browserReplay.Start("", &dsl.Script{Name: "Network evidence fixture", Protocol: name, Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}}, BrowserReplayStartRequest{ReplaySessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	waitForBrowserReplayFixture(t, server.browserReplay, session.ID)
	t.Cleanup(server.browserReplay.Close)
	retained, found := server.browserReplay.Get(session.ID)
	if !found || retained.FinalReport == nil || len(retained.FinalReport.Events) == 0 || len(retained.Events) != 0 {
		t.Fatalf("completed replay must retain evidence in its final report: %#v, found=%t", retained, found)
	}
}
