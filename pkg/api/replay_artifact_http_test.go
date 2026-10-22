package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestReplayArtifactHTTPRecoveryAfterManagerShutdown(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	firstStore, secondStore := openReplaySessionHAStores(t)
	first := NewServer(ServerConfig{Store: firstStore})
	closeBatchTestServer(t, first)
	harDir := t.TempDir()
	path := publishFakeReplayHAR(t, harDir, "recovered", "retained-native")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name := registerReplayHARProtocol(t, harDir, func() []string { return []string{path} }, nil)
	script := &dsl.Script{Name: "Durable replay", Protocol: name, Version: "1.0.0", ProjectID: storage.DefaultProjectID, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	on := true
	if _, err = first.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "durable-http", RecordHAR: &on}); err != nil {
		t.Fatal(err)
	}
	terminal := waitBrowserReplayTerminal(t, first.browserReplay, "durable-http", 10*time.Second)
	if terminal.ArtifactDurability() != "durable" {
		t.Fatalf("controller did not publish native evidence: %s", terminal.ArtifactDurability())
	}
	local := retainedReplayHARFixturePath(t, first, "durable-http")
	// A durable live cache entry cannot bypass current backend verification.
	originalBackend := first.replayArtifacts.backend
	first.replayArtifacts.backend.LocalRoot = t.TempDir()
	for _, suffix := range []string{"", "/har"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/browser-replay/sessions/durable-http"+suffix, nil)
		response := httptest.NewRecorder()
		first.mux.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("live cache bypassed source verification: %d %s", response.Code, response.Body.String())
		}
	}
	first.replayArtifacts.backend = originalBackend
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("local native copy survived manager shutdown", err)
	}
	second := NewServer(ServerConfig{Store: secondStore})
	closeBatchTestServer(t, second)
	if _, ok := second.browserReplay.Get("durable-http"); ok {
		t.Fatal("recovery invented a live replay")
	}
	base := "/api/v1/browser-replay/sessions/durable-http"
	// Auth bypass/global callers still receive an exact bound project for reads.
	request := httptest.NewRequest(http.MethodGet, base+"/har", nil)
	reply := httptest.NewRecorder()
	second.mux.ServeHTTP(reply, request)
	if reply.Code != http.StatusOK || !bytes.Equal(reply.Body.Bytes(), original) || reply.Header().Get("X-APEXLoad-Replay-Durability") != "durable" {
		t.Fatalf("bypass recovery %d: %s", reply.Code, reply.Body.String())
	}
	enableTrustedHeaderAuth(t, second)
	status := harDownloadRequest(t, second, base, "viewer", harViewer)
	if status.Code != http.StatusOK || status.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer status %d: %s", status.Code, status.Body.String())
	}
	var envelope BrowserReplaySessionResponse
	if err = json.Unmarshal(status.Body.Bytes(), &envelope); err != nil || envelope.Session == nil || envelope.Session.ID != "durable-http" {
		t.Fatal("lost terminal identity", err)
	}
	for _, suffix := range []string{"/har", "/evidence"} {
		if got := harDownloadRequest(t, second, base+suffix, "viewer", harViewer); got.Code != http.StatusForbidden {
			t.Fatalf("viewer downloaded %s: %d", suffix, got.Code)
		}
		if got := harDownloadRequest(t, second, base+suffix, "foreign", harOtherEditor); got.Code != http.StatusForbidden && got.Code != http.StatusNotFound {
			t.Fatalf("foreign project downloaded %s: %d", suffix, got.Code)
		}
	}
	download := harDownloadRequest(t, second, base+"/har", "editor", harEditor)
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), original) || download.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" {
		t.Fatalf("native recovery %d: %s", download.Code, download.Body.String())
	}
	evidence := harDownloadRequest(t, second, base+"/evidence", "editor", harEditor)
	if evidence.Code != http.StatusOK {
		t.Fatalf("evidence recovery %d: %s", evidence.Code, evidence.Body.String())
	}
	entries := harZipEntries(t, evidence.Body.Bytes())
	if !bytes.Equal(entries["replay.har"], original) {
		t.Fatal("evidence ZIP changed native bytes")
	}
	if _, err = secondStore.GetTest("durable-http"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("recovery fabricated a managed test", err)
	}
	receipt, err := secondStore.GetReplayArtifactReceipt(context.Background(), "durable-http", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, archiveReplayDirectory, "v1", receipt.Spec.Generation, "replay.har")); err != nil {
		t.Fatal(err)
	}
	if got := harDownloadRequest(t, second, base, "viewer", harViewer); got.Code != http.StatusOK {
		t.Fatal("bounded status unnecessarily requires HAR", got.Code)
	}
	if got := harDownloadRequest(t, second, base+"/har", "editor", harEditor); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing native evidence synthesized: %d %s", got.Code, got.Body.String())
	}
	if len(second.replayArtifacts.reads) != 0 {
		t.Fatal("HTTP download leaked a private read slot")
	}
}

func TestReplayArtifactHTTPCleanupLifecycle(t *testing.T) {
	p, store, terminal, har := replayPublisherFixture(t)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", p.backend.backend.LocalRoot)
	stageReplayRuntimeCleanupFixture(t, p, store, terminal, har, true)
	server := NewServer(ServerConfig{Store: store})
	closeBatchTestServer(t, server)
	server.startReplayArtifactCleanup()
	done := server.replayArtifactCleanupDone
	server.startReplayArtifactCleanup()
	if server.replayArtifactCleanupDone != done {
		t.Fatal("cleanup loop started twice")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := store.LookupReplayArtifactScope(context.Background(), p.session)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatal("owned cleanup did not finish", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	default:
		t.Fatal("cleanup worker survived shutdown")
	}
}

func TestReplayArtifactHTTPProjectDeletionAndNamespaceCleanup(t *testing.T) {
	p, store, terminal, har := replayPublisherFixture(t)
	p.session = "deleted-replay"
	p.project = "deleted-project"
	terminal.ID = p.session
	terminal.ProjectID = p.project
	terminal.FinalReport.ReplaySessionID = p.session
	if err := store.SaveProject(&storage.Project{ID: p.project, Name: "Deleted project"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if acquired, err := store.TryAcquireReplaySessionLease(ctx, p.session, p.owner, p.project, time.Hour, 10, 10); err != nil || acquired != storage.ReplaySessionLeaseAcquired {
		t.Fatal(acquired, err)
	}
	if ok, err := store.RetainReplaySessionLease(ctx, p.session, p.owner, p.project, time.Hour); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := p.Publish(ctx, terminal, bytes.NewReader(har), int64(len(har))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", p.backend.backend.LocalRoot)
	server := NewServer(ServerConfig{Store: store})
	closeBatchTestServer(t, server)
	enableTrustedHeaderAuth(t, server)
	base := "/api/v1/browser-replay/sessions/" + p.session
	roles := p.project + "=" + RoleEditor
	if got := harDownloadRequest(t, server, base+"/har", "editor", roles); got.Code != http.StatusOK {
		t.Fatal("published fixture unavailable", got.Code, got.Body.String())
	}
	if err := store.DeleteProject(p.project); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/har", "/evidence"} {
		got := harDownloadRequest(t, server, base+suffix, "editor", roles)
		if got.Code != http.StatusForbidden && got.Code != http.StatusNotFound {
			t.Fatalf("deleted project read %s: %d %s", suffix, got.Code, got.Body.String())
		}
	}
	if err := server.replayArtifacts.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProject(&storage.Project{ID: p.project, Name: "Replacement project"}); err != nil {
		t.Fatal(err)
	}
	_, release, signal, err := server.reserveReplaySessionID(p.session, p.project)
	if err != nil {
		t.Fatal("exact cleanup failed to release namespace", err)
	}
	defer release()
	defer signal.releaseAll()
	if got := harDownloadRequest(t, server, base+"/har", "editor", roles); got.Code == http.StatusOK {
		t.Fatal("successor reservation adopted predecessor bytes")
	}
}
