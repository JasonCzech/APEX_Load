package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBrowserManagedHARCaptureFailureIsRetainedAsOmission(t *testing.T) {
	if os.Getenv("APEXLOAD_BROWSER_TESTS") != "require" {
		t.Skip("requires installed native browsers")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><link rel="icon" href="data:,"><h1>Controlled capture failure</h1>`)
	}))
	t.Cleanup(target.Close)
	server, store := baselineTestServer(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	dir := t.TempDir()
	for n := 0; n < 1100; n++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("unrelated-%04d", n)), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	settings := server.runner.GetSystemSettings()
	settings.ArchiveRetention.HARArtifactDir = dir
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any, want int, out any) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		server.mux.ServeHTTP(reply, request)
		if reply.Code != want {
			t.Fatalf("%s %s status=%d body=%.1000s", method, path, reply.Code, reply.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(reply.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
	}
	on := true
	one := 1
	script := &dsl.Script{Name: "Capture failure", Protocol: "browser", Version: "1.0.0", ProjectID: storage.DefaultProjectID, Config: dsl.ScriptConfig{BaseURL: target.URL, Browser: dsl.BrowserConfig{Engine: "chromium", Headless: &on, HARCapture: "always"}}, Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	var authored StoredScript
	call("POST", "/api/v1/scripts", map[string]any{"script": script}, 201, &authored)
	var run ManagedTest
	call("POST", "/api/v1/tests", CreateTestRequest{Name: "Capture failure", ProjectID: storage.DefaultProjectID, TerminationMode: "iterations", Groups: []CreateGroupEntry{{Name: "browser", ScriptID: authored.ID, VUsers: 1, Iterations: &one}}}, 201, &run)
	call("POST", "/api/v1/tests/"+run.ID+"/start", map[string]any{}, 200, nil)
	waitForTestStatusWithin(t, server.runner, run.ID, 60*time.Second, "COMPLETE")
	var manifest *RunArchiveManifest
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		record, err := store.GetRunArchive(run.ID)
		if err != nil || record == nil || record.ManifestSHA256 == "" {
			continue
		}
		manifest, err = loadRunArchiveManifestFromRecord(record)
		if err == nil {
			break
		}
	}
	if manifest == nil || !manifest.BrowserHARIncomplete || manifest.BrowserHAROmitted[protocol.BrowserHAROmittedCaptureFailed] != 1 || len(manifest.BrowserHARArtifacts) != 0 {
		t.Fatalf("native capture loss was not retained: manifest_present=%t", manifest != nil)
	}
	var list BrowserHARArchiveList
	call("GET", "/api/v1/tests/"+run.ID+"/archive/browser/har", nil, 200, &list)
	if !list.Incomplete || list.Omitted[protocol.BrowserHAROmittedCaptureFailed] != 1 {
		t.Fatalf("HTTP listing hides capture failure: %+v", list)
	}
	for _, artifact := range list.Artifacts {
		if artifact.Fidelity == "native" {
			t.Fatal("failed capture advertised a native artifact")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("unexpected target traffic: %d", hits.Load())
	}
	for n := 0; n < 1100; n++ {
		raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("unrelated-%04d", n)))
		if err != nil || string(raw) != "preserve" {
			t.Fatal("unrelated capture directory file changed", n, err)
		}
	}
	t.Log("managed run completed; native capture failure retained in finalized manifest and authorized HAR list; unrelated files preserved")
}
