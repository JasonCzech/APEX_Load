package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/filelock"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"github.com/jasonczech/apexload/pkg/storage"
)

func seedActiveHARWorkingGenerationForCurrentProcess(t testing.TB, harDir, generation string) (string, func()) {
	t.Helper()
	workDir := filepath.Join(harDir, ".apexload-har-work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatalf("create HAR working root: %v", err)
	}
	genDir := filepath.Join(workDir, generation)
	if err := os.MkdirAll(genDir, 0o700); err != nil {
		t.Fatalf("create HAR generation dir: %v", err)
	}
	lockPath := filepath.Join(genDir, "owner.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open owner.lock: %v", err)
	}
	release, acquired, err := filelock.TryLock(lockFile)
	if err != nil || !acquired {
		_ = lockFile.Close()
		t.Fatalf("lock owner.lock: acquired=%v err=%v", acquired, err)
	}
	self, err := procidentity.Self()
	if err != nil {
		_ = release()
		_ = lockFile.Close()
		t.Fatalf("procidentity.Self: %v", err)
	}
	type manifestProc struct {
		Role string `json:"role"`
		procidentity.Identity
	}
	manifest := struct {
		Version    int            `json:"version"`
		Generation string         `json:"generation"`
		State      string         `json:"state"`
		Processes  []manifestProc `json:"processes"`
	}{
		Version:    3,
		Generation: generation,
		State:      "sealed",
		Processes:  []manifestProc{{Role: "generator", Identity: self}},
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		_ = release()
		_ = lockFile.Close()
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "manifest.json"), rawManifest, 0o600); err != nil {
		_ = release()
		_ = lockFile.Close()
		t.Fatalf("write manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "working.har"), []byte(`{"log":{"version":"1.2","entries":[]}}`), 0o600); err != nil {
		_ = release()
		_ = lockFile.Close()
		t.Fatalf("write working.har: %v", err)
	}
	return genDir, func() {
		_ = release()
		_ = lockFile.Close()
	}
}

func runReplaySIGKILLChild(t *testing.T, stage string) {
	dbPath := os.Getenv("APEXLOAD_TEST_REPLAY_DB")
	archiveDir := os.Getenv("APEXLOAD_ARCHIVE_DIR")
	harDir := os.Getenv("APEXLOAD_BROWSER_HAR_DIR")
	sessionID := os.Getenv("APEXLOAD_TEST_REPLAY_SESSION")
	gen := os.Getenv("APEXLOAD_TEST_REPLAY_HAR_GEN")

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("child open sqlite store: %v", err)
	}
	ctx := context.Background()

	// Hold an active native HAR working generation in harDir until SIGKILL.
	_, _ = seedActiveHARWorkingGenerationForCurrentProcess(t, harDir, gen)

	switch stage {
	case "pre-stage":
		acquired, err := store.TryAcquireReplaySessionLease(ctx, sessionID, "child-owner", storage.DefaultProjectID, time.Hour, 10, 10)
		if err != nil || acquired != storage.ReplaySessionLeaseAcquired {
			t.Fatalf("child acquire lease: %v %v", acquired, err)
		}
		retained, err := store.RetainReplaySessionLease(ctx, sessionID, "child-owner", storage.DefaultProjectID, time.Hour)
		if err != nil || !retained {
			t.Fatalf("child retain lease: %v %v", retained, err)
		}

	case "unjoined-stage":
		acquired, err := store.TryAcquireReplaySessionLease(ctx, sessionID, "child-owner", storage.DefaultProjectID, time.Hour, 10, 10)
		if err != nil || acquired != storage.ReplaySessionLeaseAcquired {
			t.Fatalf("child acquire lease: %v %v", acquired, err)
		}
		retained, err := store.RetainReplaySessionLease(ctx, sessionID, "child-owner", storage.DefaultProjectID, time.Hour)
		if err != nil || !retained {
			t.Fatalf("child retain lease: %v %v", retained, err)
		}
		backend, err := openReplayArtifactBackend(ctx, archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: archiveDir})
		if err != nil {
			t.Fatalf("child open replay backend: %v", err)
		}
		harBytes := []byte(`{"log":{"version":"1.2","pages":[{"id":"unjoined"}],"entries":[]}}`)
		reportBytes := []byte(`{"session":{"id":"` + sessionID + `","status":"completed"}}`)
		spec := storage.ReplayArtifactSpec{
			Version:      1,
			SessionID:    sessionID,
			ProjectID:    storage.DefaultProjectID,
			Generation:   strings.Repeat("d", 64),
			SourceSHA256: backend.sourceSHA256(),
			HARSHA256:    sha256BytesHex(harBytes),
			HARBytes:     int64(len(harBytes)),
			ReportSHA256: sha256BytesHex(reportBytes),
			ReportBytes:  int64(len(reportBytes)),
		}
		if err := store.BeginReplayArtifactReceipt(ctx, spec, "child-owner", time.Hour, storage.ReplayArtifactLimits{
			GlobalReceipts:  10,
			ProjectReceipts: 10,
			GlobalBytes:     1 << 20,
			ProjectBytes:    1 << 20,
		}); err != nil {
			t.Fatalf("child begin receipt: %v", err)
		}
		// Write staged bytes on disk without recording JoinReplayArtifactWriter or PublishReplayArtifactReceipt.
		if err := backend.Stage(ctx, spec, bytes.NewReader(harBytes), bytes.NewReader(reportBytes)); err != nil {
			t.Fatalf("child stage bytes: %v", err)
		}

	case "post-publish":
		server := NewServer(ServerConfig{Store: store})
		published := publishFakeReplayHAR(t, harDir, "sigkill-published", "sigkill-post-publish")
		protoName := registerReplayHARProtocol(t, harDir, func() []string { return []string{published} }, nil)
		script := &dsl.Script{
			Name:      "SIGKILL durable replay",
			Protocol:  protoName,
			Version:   "1.0.0",
			ProjectID: storage.DefaultProjectID,
			Actions:   []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}},
		}
		on := true
		if _, err := server.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: sessionID, RecordHAR: &on}); err != nil {
			t.Fatalf("child start replay: %v", err)
		}
		terminal := waitBrowserReplayTerminal(t, server.browserReplay, sessionID, 10*time.Second)
		if terminal.ArtifactDurability() != "durable" {
			t.Fatalf("child replay durability = %q, want durable", terminal.ArtifactDurability())
		}
	default:
		t.Fatalf("unknown SIGKILL stage %q", stage)
	}

	_, _ = fmt.Fprintln(os.Stdout, "READY")
	time.Sleep(time.Hour)
}

func TestReplayArtifactSIGKILLMidReplayAndRestartReclamation(t *testing.T) {
	if stage := os.Getenv("APEXLOAD_TEST_REPLAY_SIGKILL_STAGE"); stage != "" {
		runReplaySIGKILLChild(t, stage)
		return
	}
	if !browser.NativeHARSupported() {
		t.Skip("native HAR working ownership is not supported on this platform")
	}

	stages := []struct {
		name       string
		generation string
	}{
		{name: "pre-stage", generation: strings.Repeat("1", 32)},
		{name: "unjoined-stage", generation: strings.Repeat("2", 32)},
		{name: "post-publish", generation: strings.Repeat("3", 32)},
	}

	for _, tc := range stages {
		t.Run(tc.name, func(t *testing.T) {
			tempRoot := t.TempDir()
			dbPath := filepath.Join(tempRoot, "replay-sigkill.db")
			archiveDir := filepath.Join(tempRoot, "archives")
			harDir := filepath.Join(tempRoot, "har-working")
			if err := os.MkdirAll(archiveDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(harDir, 0o700); err != nil {
				t.Fatal(err)
			}
			sessionID := "sigkill-" + tc.name

			cmd := exec.Command(os.Args[0], "-test.run=^TestReplayArtifactSIGKILLMidReplayAndRestartReclamation$")
			cmd.Env = append(os.Environ(),
				"APEXLOAD_TEST_REPLAY_SIGKILL_STAGE="+tc.name,
				"APEXLOAD_TEST_REPLAY_DB="+dbPath,
				"APEXLOAD_ARCHIVE_DIR="+archiveDir,
				"APEXLOAD_BROWSER_HAR_DIR="+harDir,
				"APEXLOAD_TEST_REPLAY_SESSION="+sessionID,
				"APEXLOAD_TEST_REPLAY_HAR_GEN="+tc.generation,
			)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}()

			readyCh := make(chan error, 1)
			go func() {
				buf := make([]byte, 64)
				var out strings.Builder
				for {
					n, readErr := stdout.Read(buf)
					if n > 0 {
						out.Write(buf[:n])
						if strings.Contains(out.String(), "READY") {
							readyCh <- nil
							return
						}
					}
					if readErr != nil {
						readyCh <- fmt.Errorf("read READY: %w (stderr=%s)", readErr, stderr.String())
						return
					}
				}
			}()

			select {
			case err := <-readyCh:
				if err != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal(err)
				}
			case <-time.After(15 * time.Second):
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("timed out waiting for child READY (stderr=%s)", stderr.String())
			}

			// Verify the active HAR working generation exists before SIGKILL.
			genDir := filepath.Join(harDir, ".apexload-har-work", tc.generation)
			if _, err := os.Stat(genDir); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("expected active HAR working generation before SIGKILL: %v", err)
			}

			if err := cmd.Process.Signal(os.Kill); err != nil {
				t.Fatalf("send SIGKILL: %v", err)
			}
			_ = cmd.Wait()

			// Start replacement controller against the same DB, archive root, and HAR working root.
			t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveDir)
			t.Setenv("APEXLOAD_BROWSER_HAR_DIR", harDir)
			store, err := storage.NewSQLiteStore(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })

			recovered := NewServer(ServerConfig{Store: store})
			closeBatchTestServer(t, recovered)
			recovered.startReplayArtifactCleanup()

			// Wait for startReplayArtifactCleanup() to reclaim the killed child's HAR working generation.
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(genDir); os.IsNotExist(err) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("killed child's native HAR working generation %s was not reclaimed on controller startup", genDir)
				}
				time.Sleep(20 * time.Millisecond)
			}

			base := "/api/v1/browser-replay/sessions/" + sessionID
			switch tc.name {
			case "pre-stage":
				for _, suffix := range []string{"", "/har"} {
					req := httptest.NewRequest(http.MethodGet, base+suffix, nil)
					rec := httptest.NewRecorder()
					recovered.mux.ServeHTTP(rec, req)
					if rec.Code != http.StatusNotFound {
						t.Fatalf("pre-stage %s status = %d, want 404: %s", suffix, rec.Code, rec.Body.String())
					}
				}
				if acquired, err := store.TryAcquireReplaySessionLease(context.Background(), sessionID, "successor-pre", storage.DefaultProjectID, time.Hour, 10, 10); err != nil || acquired == storage.ReplaySessionLeaseAcquired {
					t.Fatalf("pre-stage session ID allowed reuse while retained lease active: acquired=%v err=%v", acquired, err)
				}

			case "unjoined-stage":
				for _, suffix := range []string{"", "/har"} {
					req := httptest.NewRequest(http.MethodGet, base+suffix, nil)
					rec := httptest.NewRecorder()
					recovered.mux.ServeHTTP(rec, req)
					if rec.Code != http.StatusServiceUnavailable {
						t.Fatalf("unjoined-stage %s status = %d, want 503 (never synthetic fallback): %s", suffix, rec.Code, rec.Body.String())
					}
				}
				if err := recovered.replayArtifacts.Sweep(context.Background()); err != nil {
					t.Fatalf("Sweep on unjoined stage: %v", err)
				}
				scope, err := store.LookupReplayArtifactScope(context.Background(), sessionID)
				if err != nil || scope == nil || scope.WriterJoined {
					t.Fatalf("unjoined reservation lost or marked joined after SIGKILL: scope=%+v err=%v", scope, err)
				}
				if acquired, err := store.TryAcquireReplaySessionLease(context.Background(), sessionID, "successor-unjoined", storage.DefaultProjectID, time.Hour, 10, 10); err != nil || acquired == storage.ReplaySessionLeaseAcquired {
					t.Fatalf("unjoined-stage session ID allowed reuse after SIGKILL: acquired=%v err=%v", acquired, err)
				}

			case "post-publish":
				statusReq := httptest.NewRequest(http.MethodGet, base, nil)
				statusRec := httptest.NewRecorder()
				recovered.mux.ServeHTTP(statusRec, statusReq)
				if statusRec.Code != http.StatusOK || statusRec.Header().Get("X-APEXLoad-Replay-Durability") != "durable" {
					t.Fatalf("post-publish status = %d durability=%q: %s", statusRec.Code, statusRec.Header().Get("X-APEXLoad-Replay-Durability"), statusRec.Body.String())
				}
				harReq := httptest.NewRequest(http.MethodGet, base+"/har", nil)
				harRec := httptest.NewRecorder()
				recovered.mux.ServeHTTP(harRec, harReq)
				if harRec.Code != http.StatusOK || harRec.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" || !strings.Contains(harRec.Body.String(), "sigkill-post-publish") {
					t.Fatalf("post-publish HAR = %d fidelity=%q body=%s", harRec.Code, harRec.Header().Get("X-APEXLoad-HAR-Fidelity"), harRec.Body.String())
				}
			}
		})
	}
}

func TestReplayArtifactTwoControllersSharedFilesystemHandoff(t *testing.T) {
	sharedBase := t.TempDir()
	realArchiveDir := filepath.Join(sharedBase, "shared-archives")
	if err := os.MkdirAll(realArchiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Create a symlinked parent directory so controller 1 opens via symlinked parent
	// and controller 2 opens via canonical path, verifying EvalSymlinks normalization.
	symlinkParent := filepath.Join(sharedBase, "symlink-parent")
	if err := os.Symlink(sharedBase, symlinkParent); err != nil {
		t.Skipf("symlink creation unsupported: %v", err)
	}
	aliasArchiveDir := filepath.Join(symlinkParent, "shared-archives")

	aliasAdapter, err := openReplayArtifactBackend(context.Background(), archiveBackendRuntime{
		Backend:   archiveBackendFilesystem,
		LocalRoot: aliasArchiveDir,
	})
	if err != nil {
		t.Fatalf("openReplayArtifactBackend(aliasArchiveDir): %v", err)
	}
	defer aliasAdapter.Close()
	realAdapter, err := openReplayArtifactBackend(context.Background(), archiveBackendRuntime{
		Backend:   archiveBackendFilesystem,
		LocalRoot: realArchiveDir,
	})
	if err != nil {
		t.Fatalf("openReplayArtifactBackend(realArchiveDir): %v", err)
	}
	defer realAdapter.Close()
	if aliasAdapter.sourceSHA256() != realAdapter.sourceSHA256() {
		t.Fatalf("symlink alias sourceSHA256 = %q, canonical sourceSHA256 = %q", aliasAdapter.sourceSHA256(), realAdapter.sourceSHA256())
	}

	firstStore, secondStore := openReplaySessionHAStores(t)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", aliasArchiveDir)
	first := NewServer(ServerConfig{Store: firstStore, HAExecutionLeadership: true})
	closeBatchTestServer(t, first)

	second := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: false})
	closeBatchTestServer(t, second)

	harDir := t.TempDir()
	published := publishFakeReplayHAR(t, harDir, "shared-fs", "shared-fs-page")
	originalBytes, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	protoName := registerReplayHARProtocol(t, harDir, func() []string { return []string{published} }, nil)
	script := &dsl.Script{
		Name:      "Shared FS replay",
		Protocol:  protoName,
		Version:   "1.0.0",
		ProjectID: storage.DefaultProjectID,
		Actions:   []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}},
	}
	on := true
	if _, err := first.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: "shared-fs-replay", RecordHAR: &on}); err != nil {
		t.Fatal(err)
	}
	terminal := waitBrowserReplayTerminal(t, first.browserReplay, "shared-fs-replay", 10*time.Second)
	if terminal.ArtifactDurability() != "durable" {
		t.Fatalf("first controller durability = %q, want durable", terminal.ArtifactDurability())
	}

	// Switch APEXLOAD_ARCHIVE_DIR to the canonical path before Controller 2 serves recovered requests.
	t.Setenv("APEXLOAD_ARCHIVE_DIR", realArchiveDir)
	base := "/api/v1/browser-replay/sessions/shared-fs-replay"
	// Controller 2 (which never ran the session in memory) must serve both status and native HAR immediately.
	for _, srv := range []*Server{first, second} {
		statusReq := httptest.NewRequest(http.MethodGet, base, nil)
		statusRec := httptest.NewRecorder()
		srv.mux.ServeHTTP(statusRec, statusReq)
		if statusRec.Code != http.StatusOK || statusRec.Header().Get("X-APEXLoad-Replay-Durability") != "durable" {
			t.Fatalf("shared-FS status = %d: %s", statusRec.Code, statusRec.Body.String())
		}
		harReq := httptest.NewRequest(http.MethodGet, base+"/har", nil)
		harRec := httptest.NewRecorder()
		srv.mux.ServeHTTP(harRec, harReq)
		if harRec.Code != http.StatusOK || !bytes.Equal(harRec.Body.Bytes(), originalBytes) || harRec.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" {
			t.Fatalf("shared-FS HAR = %d: %s", harRec.Code, harRec.Body.String())
		}
	}

	// Controller 3 with a different filesystem root or mismatched source.v1 marker must fail closed with 503.
	foreignArchiveDir := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", foreignArchiveDir)
	third := NewServer(ServerConfig{Store: secondStore, HAExecutionLeadership: true})
	closeBatchTestServer(t, third)
	for _, suffix := range []string{"", "/har"} {
		req := httptest.NewRequest(http.MethodGet, base+suffix, nil)
		rec := httptest.NewRecorder()
		third.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("mismatched filesystem root %s status = %d, want 503: %s", suffix, rec.Code, rec.Body.String())
		}
	}
	if err := third.replayArtifacts.Sweep(context.Background()); err != nil {
		t.Fatalf("foreign Sweep returned error: %v", err)
	}
	// Verify Controller 2 still reads the intact artifact after Controller 3's Sweep.
	harReq := httptest.NewRequest(http.MethodGet, base+"/har", nil)
	harRec := httptest.NewRecorder()
	second.mux.ServeHTTP(harRec, harReq)
	if harRec.Code != http.StatusOK || !bytes.Equal(harRec.Body.Bytes(), originalBytes) {
		t.Fatalf("foreign Sweep corrupted shared-FS artifact: %d", harRec.Code)
	}
}

type inProcessObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newInProcessObjectStore() *inProcessObjectStore {
	return &inProcessObjectStore{objects: make(map[string][]byte)}
}

func (s *inProcessObjectStore) startS3Server(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/")
		bucket, key, hasKey := strings.Cut(trimmed, "/")
		if bucket == "" {
			http.Error(w, "missing bucket", http.StatusBadRequest)
			return
		}
		if !hasKey || key == "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		fullKey := bucket + "/" + key
		s.mu.Lock()
		defer s.mu.Unlock()

		switch r.Method {
		case http.MethodPut:
			if r.Header.Get("If-None-Match") == "*" {
				if _, exists := s.objects[fullKey]; exists {
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusPreconditionFailed)
					_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>PreconditionFailed</Code><Message>At least one of the pre-conditions you specified did not hold</Message></Error>`)
					return
				}
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			s.objects[fullKey] = body
			w.Header().Set("ETag", `"`+sha256BytesHex(body)+`"`)
			w.WriteHeader(http.StatusOK)

		case http.MethodHead, http.MethodGet:
			data, exists := s.objects[fullKey]
			if !exists {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotFound)
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`)
				}
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("ETag", `"`+sha256BytesHex(data)+`"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(data)
			}

		case http.MethodDelete:
			delete(s.objects, fullKey)
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (s *inProcessObjectStore) startAzuriteServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/")
		parts := strings.SplitN(trimmed, "/", 3)
		if len(parts) < 2 {
			http.Error(w, "invalid azblob path", http.StatusBadRequest)
			return
		}
		account, container := parts[0], parts[1]
		if len(parts) == 2 || r.URL.Query().Get("restype") == "container" {
			w.Header().Set("x-ms-request-id", "req-container")
			w.Header().Set("x-ms-version", "2020-10-02")
			w.WriteHeader(http.StatusCreated)
			return
		}
		key := parts[2]
		fullKey := account + "/" + container + "/" + key

		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("x-ms-request-id", "req-blob")
		w.Header().Set("x-ms-version", "2020-10-02")

		switch r.Method {
		case http.MethodPut:
			if r.Header.Get("If-None-Match") == "*" {
				if _, exists := s.objects[fullKey]; exists {
					w.Header().Set("x-ms-error-code", "BlobAlreadyExists")
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?><Error><Code>BlobAlreadyExists</Code><Message>The specified blob already exists.</Message></Error>`)
					return
				}
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			s.objects[fullKey] = body
			w.Header().Set("ETag", `"0x1"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusCreated)

		case http.MethodHead, http.MethodGet:
			data, exists := s.objects[fullKey]
			if !exists {
				w.Header().Set("x-ms-error-code", "BlobNotFound")
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotFound)
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?><Error><Code>BlobNotFound</Code><Message>The specified blob does not exist.</Message></Error>`)
				}
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("ETag", `"0x1"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.Header().Set("x-ms-blob-type", "BlockBlob")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(data)
			}

		case http.MethodDelete:
			if _, exists := s.objects[fullKey]; !exists {
				w.Header().Set("x-ms-error-code", "BlobNotFound")
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8"?><Error><Code>BlobNotFound</Code><Message>The specified blob does not exist.</Message></Error>`)
				return
			}
			delete(s.objects, fullKey)
			w.WriteHeader(http.StatusAccepted)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (s *inProcessObjectStore) startFakeGCSServer(t *testing.T) string {
	t.Helper()
	var srv *httptest.Server
	resumable := make(map[string]struct {
		bucket string
		name   string
		ifGen0 bool
	})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Upload endpoint: /upload/storage/v1/b/{bucket}/o
		if strings.HasPrefix(r.URL.Path, "/upload/storage/v1/b/") {
			rest := strings.TrimPrefix(r.URL.Path, "/upload/storage/v1/b/")
			bucket, _, _ := strings.Cut(rest, "/o")
			if uploadID := r.URL.Query().Get("upload_id"); uploadID != "" {
				meta, ok := resumable[uploadID]
				if !ok {
					http.Error(w, "unknown upload_id", http.StatusNotFound)
					return
				}
				body, _ := io.ReadAll(r.Body)
				fullKey := meta.bucket + "/" + meta.name
				if meta.ifGen0 {
					if _, exists := s.objects[fullKey]; exists {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusPreconditionFailed)
						_, _ = io.WriteString(w, `{"error":{"code":412,"message":"Precondition Failed"}}`)
						return
					}
				}
				s.objects[fullKey] = body
				delete(resumable, uploadID)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"kind":       "storage#object",
					"name":       meta.name,
					"bucket":     meta.bucket,
					"size":       strconv.Itoa(len(body)),
					"generation": "1",
					"updated":    time.Now().UTC().Format(time.RFC3339Nano),
				})
				return
			}

			ifGen0 := r.URL.Query().Get("ifGenerationMatch") == "0"
			uploadType := r.URL.Query().Get("uploadType")
			if uploadType == "resumable" {
				var meta struct {
					Name string `json:"name"`
				}
				_ = json.NewDecoder(r.Body).Decode(&meta)
				if meta.Name == "" {
					meta.Name = r.URL.Query().Get("name")
				}
				id := fmt.Sprintf("up-%d", len(resumable)+1)
				resumable[id] = struct {
					bucket string
					name   string
					ifGen0 bool
				}{bucket: bucket, name: meta.Name, ifGen0: ifGen0}
				w.Header().Set("Location", srv.URL+"/upload/storage/v1/b/"+bucket+"/o?uploadType=resumable&upload_id="+id)
				w.WriteHeader(http.StatusOK)
				return
			}

			var objName string
			var objData []byte
			mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err == nil && strings.HasPrefix(mediaType, "multipart/") {
				mr := multipart.NewReader(r.Body, params["boundary"])
				if part1, err := mr.NextPart(); err == nil {
					var meta struct {
						Name string `json:"name"`
					}
					_ = json.NewDecoder(part1).Decode(&meta)
					objName = meta.Name
				}
				if part2, err := mr.NextPart(); err == nil {
					objData, _ = io.ReadAll(part2)
				}
			} else {
				objName = r.URL.Query().Get("name")
				objData, _ = io.ReadAll(r.Body)
			}
			fullKey := bucket + "/" + objName
			if ifGen0 {
				if _, exists := s.objects[fullKey]; exists {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusPreconditionFailed)
					_, _ = io.WriteString(w, `{"error":{"code":412,"message":"Precondition Failed"}}`)
					return
				}
			}
			s.objects[fullKey] = objData
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":       "storage#object",
				"name":       objName,
				"bucket":     bucket,
				"size":       strconv.Itoa(len(objData)),
				"generation": "1",
				"updated":    time.Now().UTC().Format(time.RFC3339Nano),
			})
			return
		}

		// Download or metadata/delete path:
		// /storage/v1/b/{bucket}/o/{object} or /download/storage/v1/b/{bucket}/o/{object} or /{bucket}/{object}
		rawPath := r.URL.EscapedPath()
		isDownload := r.URL.Query().Get("alt") == "media"
		if strings.HasPrefix(rawPath, "/download/storage/v1/b/") {
			isDownload = true
			rawPath = "/storage/v1/b/" + strings.TrimPrefix(rawPath, "/download/storage/v1/b/")
		}
		var bucket, objName string
		if strings.HasPrefix(rawPath, "/storage/v1/b/") {
			rest := strings.TrimPrefix(rawPath, "/storage/v1/b/")
			if b, o, ok := strings.Cut(rest, "/o/"); ok {
				bucket = b
				objName, _ = url.PathUnescape(o)
			} else {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"kind":"storage#bucket"}`)
				return
			}
		} else {
			trimmed := strings.TrimPrefix(r.URL.Path, "/")
			bucket, objName, _ = strings.Cut(trimmed, "/")
			isDownload = true
		}

		fullKey := bucket + "/" + objName
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			data, exists := s.objects[fullKey]
			if !exists {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, `{"error":{"code":404,"message":"Not Found"}}`)
				}
				return
			}
			if isDownload {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("X-Goog-Generation", "1")
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				w.WriteHeader(http.StatusOK)
				if r.Method == http.MethodGet {
					_, _ = w.Write(data)
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind":        "storage#object",
				"name":        objName,
				"bucket":      bucket,
				"size":        strconv.Itoa(len(data)),
				"generation":  "1",
				"contentType": "application/octet-stream",
				"updated":     time.Now().UTC().Format(time.RFC3339Nano),
			})

		case http.MethodDelete:
			if _, exists := s.objects[fullKey]; !exists {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":{"code":404,"message":"Not Found"}}`)
				return
			}
			delete(s.objects, fullKey)
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestReplayArtifactCloudEmulatorsCrashAndRestart(t *testing.T) {
	storeMem := newInProcessObjectStore()

	minioEndpoint := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_MINIO_ENDPOINT"))
	if minioEndpoint == "" {
		minioEndpoint = storeMem.startS3Server(t)
	}
	azuriteEndpoint := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_AZURITE_ENDPOINT"))
	if azuriteEndpoint == "" {
		azuriteEndpoint = storeMem.startAzuriteServer(t)
	}
	gcsEndpoint := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_FAKE_GCS_ENDPOINT"))
	if gcsEndpoint == "" {
		gcsEndpoint = storeMem.startFakeGCSServer(t)
	}

	azuriteDefaultKey := base64.StdEncoding.EncodeToString([]byte("apexload-azurite-shared-key-32b!"))
	if os.Getenv("APEXLOAD_TEST_AZURITE_ENDPOINT") != "" {
		// Standard Azurite well-known key for devstoreaccount1
		azuriteDefaultKey = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
	}

	backends := []struct {
		name  string
		setup func(t *testing.T)
	}{
		{
			name: "s3-minio",
			setup: func(t *testing.T) {
				t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "s3")
				t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "replay-s3-bucket")
				t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
				t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", minioEndpoint)
				t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "tenant-a/replays")
				if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
					t.Setenv("AWS_ACCESS_KEY_ID", "minioadmin")
				}
				if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
					t.Setenv("AWS_SECRET_ACCESS_KEY", "minioadmin")
				}
			},
		},
		{
			name: "azure-blob-azurite",
			setup: func(t *testing.T) {
				t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "azure_blob")
				t.Setenv("APEXLOAD_ARCHIVE_CONTAINER", "replay-az-container")
				t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", azuriteEndpoint)
				t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "tenant-a/replays")
				t.Setenv("AZURE_STORAGE_ACCOUNT", "devstoreaccount1")
				t.Setenv("AZURE_STORAGE_KEY", azuriteDefaultKey)
			},
		},
		{
			name: "gcs-fake-gcs-server",
			setup: func(t *testing.T) {
				t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "gcs")
				t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "replay-gcs-bucket")
				t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", gcsEndpoint)
				t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "tenant-a/replays")
			},
		},
	}

	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())

			ctx := context.Background()
			runtimeCfg := currentArchiveBackend()
			adapter, err := openReplayArtifactBackend(ctx, runtimeCfg)
			if err != nil {
				t.Fatalf("openReplayArtifactBackend(%s): %v", tc.name, err)
			}
			defer adapter.Close()

			var s3Client *s3.Client
			if adapter.bucket != nil && adapter.bucket.As(&s3Client) {
				_, _ = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(runtimeCfg.Bucket)})
			}
			var azClient *container.Client
			if adapter.bucket != nil && adapter.bucket.As(&azClient) {
				_, _ = azClient.Create(ctx, nil)
			}
			if runtimeCfg.Backend == archiveBackendGCS && gcsEndpoint != "" {
				req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(gcsEndpoint, "/")+"/storage/v1/b?project=test", strings.NewReader(`{"name":"`+runtimeCfg.Bucket+`"}`))
				if reqErr == nil {
					req.Header.Set("Content-Type", "application/json")
					if resp, doErr := http.DefaultClient.Do(req); doErr == nil {
						_ = resp.Body.Close()
					}
				}
			}

			// 1. Verify IfNotExist conditional write enforcement on duplicate generation Stage.
			harBytes := []byte(`{"log":{"version":"1.2","pages":[{"id":"` + tc.name + `"}],"entries":[]}}`)
			reportBytes := []byte(`{"session":{"id":"dup-check","status":"completed"}}`)
			dupSpec := storage.ReplayArtifactSpec{
				Version:      1,
				SessionID:    "dup-check",
				ProjectID:    storage.DefaultProjectID,
				Generation:   strings.Repeat("a", 64),
				SourceSHA256: adapter.sourceSHA256(),
				HARSHA256:    sha256BytesHex(harBytes),
				HARBytes:     int64(len(harBytes)),
				ReportSHA256: sha256BytesHex(reportBytes),
				ReportBytes:  int64(len(reportBytes)),
			}
			if err := adapter.Stage(ctx, dupSpec, bytes.NewReader(harBytes), bytes.NewReader(reportBytes)); err != nil {
				t.Fatalf("%s initial Stage failed: %v", tc.name, err)
			}
			if err := adapter.Stage(ctx, dupSpec, bytes.NewReader(harBytes), bytes.NewReader(reportBytes)); err == nil {
				t.Fatalf("%s duplicate Stage overwrote existing generation without IfNotExist failure", tc.name)
			}

			// 2. End-to-end replay publication on Controller 1 -> crash/shutdown -> recovery on Controller 2 with fresh local staging dir.
			firstStore, secondStore := openReplaySessionHAStores(t)
			if err := firstStore.SaveProject(&storage.Project{ID: "cloud-proj", Name: "Cloud Project"}); err != nil {
				t.Fatal(err)
			}
			first := NewServer(ServerConfig{Store: firstStore})
			closeBatchTestServer(t, first)

			harDir := t.TempDir()
			published := publishFakeReplayHAR(t, harDir, tc.name, "cloud-"+tc.name)
			originalBytes, err := os.ReadFile(published)
			if err != nil {
				t.Fatal(err)
			}
			protoName := registerReplayHARProtocol(t, harDir, func() []string { return []string{published} }, nil)
			script := &dsl.Script{
				Name:      "Cloud emulator replay " + tc.name,
				Protocol:  protoName,
				Version:   "1.0.0",
				ProjectID: "cloud-proj",
				Actions:   []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}},
			}
			on := true
			sessionID := "cloud-replay-" + tc.name
			if _, err := first.browserReplay.Start("", script, BrowserReplayStartRequest{ReplaySessionID: sessionID, RecordHAR: &on}); err != nil {
				t.Fatalf("%s start replay: %v", tc.name, err)
			}
			terminal := waitBrowserReplayTerminal(t, first.browserReplay, sessionID, 10*time.Second)
			if terminal.ArtifactDurability() != "durable" {
				t.Fatalf("%s durability = %q, want durable", tc.name, terminal.ArtifactDurability())
			}

			// Also stage an unjoined session to verify 503 and non-deletion across restart.
			unjoinedID := "unjoined-" + tc.name
			if acquired, err := firstStore.TryAcquireReplaySessionLease(ctx, unjoinedID, "owner-u", "cloud-proj", time.Hour, 10, 10); err != nil || acquired != storage.ReplaySessionLeaseAcquired {
				t.Fatal(acquired, err)
			}
			if ok, err := firstStore.RetainReplaySessionLease(ctx, unjoinedID, "owner-u", "cloud-proj", time.Hour); err != nil || !ok {
				t.Fatal(ok, err)
			}
			now := time.Now()
			unjoinedSession := &recordbridge.BrowserReplaySession{
				ID:          unjoinedID,
				ProjectID:   "cloud-proj",
				Status:      recordbridge.BrowserReplayCompleted,
				CompletedAt: &now,
				FinalReport: &engine.ReplayReport{Success: true, ReplaySessionID: unjoinedID},
			}
			pub := &replayArtifactPublisher{
				store:   firstStore,
				backend: adapter,
				session: unjoinedID,
				project: "cloud-proj",
				owner:   "owner-u",
				ttl:     time.Hour,
				limits:  storage.ReplayArtifactLimits{GlobalReceipts: 10, ProjectReceipts: 10, GlobalBytes: 1 << 20, ProjectBytes: 1 << 20},
			}
			_ = stageReplayRuntimeCleanupFixture(t, pub, firstStore, unjoinedSession, harBytes, false)

			shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := first.Shutdown(shutdownCtx); err != nil {
				t.Fatal(err)
			}

			// Controller 2 uses a brand-new local staging directory and still resolves the same cloud authority.
			t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
			second := NewServer(ServerConfig{Store: secondStore})
			closeBatchTestServer(t, second)

			base := "/api/v1/browser-replay/sessions/" + sessionID
			statusReq := httptest.NewRequest(http.MethodGet, base, nil)
			statusRec := httptest.NewRecorder()
			second.mux.ServeHTTP(statusRec, statusReq)
			if statusRec.Code != http.StatusOK || statusRec.Header().Get("X-APEXLoad-Replay-Durability") != "durable" {
				t.Fatalf("%s recovered status = %d: %s", tc.name, statusRec.Code, statusRec.Body.String())
			}
			harReq := httptest.NewRequest(http.MethodGet, base+"/har", nil)
			harRec := httptest.NewRecorder()
			second.mux.ServeHTTP(harRec, harReq)
			if harRec.Code != http.StatusOK || !bytes.Equal(harRec.Body.Bytes(), originalBytes) || harRec.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" {
				t.Fatalf("%s recovered HAR = %d: %s", tc.name, harRec.Code, harRec.Body.String())
			}

			// Unjoined session must return 503 and remain retained after Sweep.
			unjoinedBase := "/api/v1/browser-replay/sessions/" + unjoinedID
			for _, suffix := range []string{"", "/har"} {
				req := httptest.NewRequest(http.MethodGet, unjoinedBase+suffix, nil)
				rec := httptest.NewRecorder()
				second.mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("%s unjoined %s = %d, want 503", tc.name, suffix, rec.Code)
				}
			}

			// Delete project and sweep: joined session is cleaned up, unjoined reservation stays retained.
			if err := secondStore.DeleteProject("cloud-proj"); err != nil {
				t.Fatal(err)
			}
			if err := second.replayArtifacts.Sweep(ctx); err != nil {
				t.Fatalf("%s Sweep failed: %v", tc.name, err)
			}
			if err := secondStore.SaveProject(&storage.Project{ID: "cloud-proj", Name: "Replacement Cloud Project"}); err != nil {
				t.Fatal(err)
			}
			if _, err := secondStore.LookupReplayArtifactScope(ctx, sessionID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("%s joined session receipt was not cleaned up: %v", tc.name, err)
			}
			if scope, err := secondStore.LookupReplayArtifactScope(ctx, unjoinedID); err != nil || scope == nil || scope.WriterJoined {
				t.Fatalf("%s unjoined session reservation was improperly released: scope=%+v err=%v", tc.name, scope, err)
			}
			if acquired, err := secondStore.TryAcquireReplaySessionLease(ctx, unjoinedID, "successor", "cloud-proj", time.Hour, 10, 10); err != nil || acquired == storage.ReplaySessionLeaseAcquired {
				t.Fatalf("%s unjoined session ID allowed reuse: acquired=%v err=%v", tc.name, acquired, err)
			}
		})
	}
}
