package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func browserHARTestController(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tests/run-1/archive/browser/har":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":1,"test_id":"run-1","artifacts":[{"name":"browser_har_synthetic.har","fidelity":"synthetic","available":true}],"download_role":"editor","can_download":true}`))
		case "/api/v1/tests/run-1/archive/browser/har/browser_har_synthetic.har":
			w.Header().Set("X-APEXLoad-HAR-Fidelity", "synthetic")
			_, _ = w.Write([]byte(`{"log":{"version":"1.2","entries":[]}}`))
		case "/api/v1/browser-replay/sessions/replay-1/evidence":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK-evidence"))
		case "/api/v1/tests/run-1/archive/browser/har/forbidden.har":
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		case "/api/v1/tests/run-1/archive/browser/har/cut.har":
			// The connection drops mid-body.
			w.Header().Set("Content-Length", "100000")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"log":{"version":"1.2","entries":[`))
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestBrowserHARCommandListsAndDownloads(t *testing.T) {
	server := browserHARTestController(t)
	var out bytes.Buffer
	if err := executeBrowserHARCommand(context.Background(), []string{"list", "--api", server.URL, "--id", "run-1"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"fidelity": "synthetic"`) {
		t.Fatalf("list output %s", out.String())
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "run.har")
	out.Reset()
	if err := executeBrowserHARCommand(context.Background(), []string{"download", "--api", server.URL, "--id", "run-1", "--artifact", "browser_har_synthetic.har", "--out", target}, &out); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(target); err != nil || !strings.Contains(string(data), `"version":"1.2"`) || !strings.Contains(out.String(), "fidelity synthetic") {
		t.Fatalf("downloaded %q %v (%s)", data, err, out.String())
	}
	// An existing file is never overwritten.
	if err := executeBrowserHARCommand(context.Background(), []string{"download", "--api", server.URL, "--id", "run-1", "--artifact", "browser_har_synthetic.har", "--out", target}, &out); err == nil {
		t.Fatal("an existing file was overwritten")
	}
	// A refused download leaves no partial file.
	refused := filepath.Join(dir, "refused.har")
	if err := executeBrowserHARCommand(context.Background(), []string{"download", "--api", server.URL, "--id", "run-1", "--artifact", "forbidden.har", "--out", refused}, &out); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("refused download: %v", err)
	}
	if _, err := os.Stat(refused); !os.IsNotExist(err) {
		t.Fatal("a refused download left a file")
	}
	// A download cut mid-body leaves no partial file, so a retry can write.
	cut := filepath.Join(dir, "cut.har")
	if err := executeBrowserHARCommand(context.Background(), []string{"download", "--api", server.URL, "--id", "run-1", "--artifact", "cut.har", "--out", cut}, &out); err == nil {
		t.Fatal("a cut download succeeded")
	}
	if _, err := os.Stat(cut); !os.IsNotExist(err) {
		t.Fatalf("a cut download left a partial file: %v", err)
	}
	// A replay's evidence ZIP to standard output.
	out.Reset()
	if err := executeBrowserHARCommand(context.Background(), []string{"download", "--api", server.URL, "--replay", "replay-1", "--evidence", "--out", "-"}, &out); err != nil || out.String() != "PK-evidence" {
		t.Fatalf("evidence to stdout %q %v", out.String(), err)
	}
}

func TestBrowserHARCommandRefusesAmbiguousArguments(t *testing.T) {
	for _, args := range [][]string{
		{"list"},
		{"list", "--id", "run-1", "--artifact", "x.har"},
		{"download", "--id", "run-1", "--artifact", "x.har"},
		{"download", "--id", "run-1", "--out", "-"},
		{"download", "--id", "run-1", "--replay", "r", "--out", "-"},
		{"download", "--id", "run-1", "--artifact", "../manifest.json", "--out", "-"},
		{"download", "--id", "run-1", "--artifact", "x.har", "--evidence", "--out", "-"},
		{"remove", "--id", "run-1"},
		{"list", "--id", "run-1", "extra"},
	} {
		if err := executeBrowserHARCommand(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	var out bytes.Buffer
	if err := executeBrowserHARCommand(context.Background(), []string{"--help"}, &out); err != nil || !strings.Contains(out.String(), "browser-har download --replay") {
		t.Fatalf("help %q %v", out.String(), err)
	}
}

// The download's own deadline governs it, not the shared client's short
// request timeout.
func TestBrowserHARDownloadOutlivesTheClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte(`{"log":{"version":"1.2","entries":[]}}`))
	}))
	t.Cleanup(server.Close)
	client := &ApiClient{BaseURL: server.URL, Client: &http.Client{Timeout: 20 * time.Millisecond}}
	var out bytes.Buffer
	if err := client.downloadBrowserHAR(context.Background(), "/slow.har", "-", &out); err != nil || !strings.Contains(out.String(), `"1.2"`) {
		t.Fatalf("slow download %q %v", out.String(), err)
	}
	if client.Client.Timeout != 20*time.Millisecond {
		t.Fatal("the shared client was changed")
	}
}
