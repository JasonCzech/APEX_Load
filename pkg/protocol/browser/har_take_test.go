package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestTakeRuntimeHARArtifactRetiresOnlyAfterConsume(t *testing.T) {
	dir := t.TempDir()
	generation := "0123456789abcdef0123456789abcdef"
	name := "apexload_browser_" + generation + "_scrubbed.har"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(ownedHARFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("consumer failed")
	if err := TakeRuntimeHARArtifact(context.Background(), path, func(io.Reader, int64) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("consumer failure: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a failed consumer lost the artifact", err)
	}
	var got []byte
	var size int64
	if err := TakeRuntimeHARArtifact(context.Background(), path, func(r io.Reader, n int64) (err error) {
		got, err = io.ReadAll(r)
		size = n
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if string(got) != ownedHARFixture || size != int64(len(ownedHARFixture)) {
		t.Fatalf("consumed %q (%d bytes)", got, size)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a consumed artifact stayed in the HAR directory", err)
	}
	for _, bad := range []string{filepath.Join(dir, "other.har"), filepath.Join(dir, ".apexload-har-work", name)} {
		if err := TakeRuntimeHARArtifact(context.Background(), bad, func(io.Reader, int64) error { return nil }); err == nil {
			t.Fatalf("%s taken", bad)
		}
	}
}

func TestBrowserHandsOverEachPublishedHAROnce(t *testing.T) {
	b := &Browser{publishedHARArtifacts: []protocol.BrowserHARArtifact{{Path: "/tmp/a.har", Generation: "g"}}}
	if got := b.TakeBrowserHARArtifacts(); len(got) != 1 || got[0].Path != "/tmp/a.har" {
		t.Fatalf("taken %+v", got)
	}
	if got := b.TakeBrowserHARArtifacts(); len(got) != 0 {
		t.Fatalf("taken twice: %+v", got)
	}
	var _ protocol.BrowserHARArtifactProvider = b
}

func TestBrowserActualSessionPublishesOneTakeableHAR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<!doctype html><title>recorded</title><body>recorded session</body>`)
	}))
	defer server.Close()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := &Browser{}
	if err := b.Init(ctx, map[string]any{"base_url": server.URL, "browser_engine": "chromium", "browser_headless": true, "browser_har_capture": "always", "browser_har_dir": dir}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	documentActualExecute(t, b, ctx, &protocol.Action{Name: "navigate", Type: "browser_navigate", Target: "/"})
	if err := b.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	artifacts := b.TakeBrowserHARArtifacts()
	if len(artifacts) != 1 || !validHARGeneration(artifacts[0].Generation) || !strings.Contains(artifacts[0].Path, artifacts[0].Generation) {
		t.Fatalf("published %+v", artifacts)
	}
	var body []byte
	if err := TakeRuntimeHARArtifact(ctx, artifacts[0].Path, func(r io.Reader, _ int64) (err error) { body, err = io.ReadAll(r); return err }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"entries"`) || !strings.Contains(string(body), server.URL) {
		t.Fatalf("not a recorded HAR: %.200s", body)
	}
}
