package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/api"
)

func TestStreamDesktopAPIResourceAttachesCapabilityAndStreamsToGrant(t *testing.T) {
	const capability = "desktop-capability-for-test"
	content := strings.Repeat("archive-chunk-", 128*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/api/v1/tests/run-1/archive?format=zip" {
			t.Errorf("request URI = %q", r.URL.RequestURI())
		}
		if got := r.Header.Get(api.DesktopCapabilityHeader); got != capability {
			t.Errorf("desktop capability = %q", got)
		}
		if got := r.Header.Get("X-APEXLoad-Dev-View"); got != "user" {
			t.Errorf("development preview mode = %q, want user", got)
		}
		if got := r.Header.Get("X-APEXLoad-Dev-Project-ID"); got != "project-b" {
			t.Errorf("development preview project = %q, want project-b", got)
		}
		w.Header().Set("Content-Type", "application/zip")
		for start := 0; start < len(content); start += 4096 {
			end := min(start+4096, len(content))
			if _, err := io.WriteString(w, content[start:end]); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "archive.zip")
	_, grant, err := newWritePathGrant(destination)
	if err != nil {
		t.Fatalf("newWritePathGrant: %v", err)
	}
	if err := streamDesktopAPIResource(
		context.Background(),
		newDesktopAPIHTTPClient(),
		server.URL,
		capability,
		"/api/v1/tests/run-1/archive?format=zip",
		mustDesktopPreviewContext(t, "user", "project-b"),
		grant,
	); err != nil {
		t.Fatalf("streamDesktopAPIResource: %v", err)
	}

	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read saved archive: %v", err)
	}
	if string(data) != content {
		t.Fatalf("saved archive length = %d, want %d", len(data), len(content))
	}
}

func TestStreamDesktopAPIResourceDoesNotPublishFailedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "archive is not ready", http.StatusConflict)
	}))
	defer server.Close()

	destination := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(destination, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, grant, err := newWritePathGrant(destination)
	if err != nil {
		t.Fatalf("newWritePathGrant: %v", err)
	}
	err = streamDesktopAPIResource(
		context.Background(),
		newDesktopAPIHTTPClient(),
		server.URL,
		"capability",
		"/api/v1/tests/run-1/archive",
		mustDesktopPreviewContext(t, "admin", ""),
		grant,
	)
	if err == nil || !strings.Contains(err.Error(), "409 Conflict") || !strings.Contains(err.Error(), "archive is not ready") {
		t.Fatalf("streamDesktopAPIResource error = %v", err)
	}
	data, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "original" {
		t.Fatalf("failed response replaced destination with %q", data)
	}
}

func TestStreamDesktopAPIResourceDoesNotPublishPartialBody(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(destination, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, grant, err := newWritePathGrant(destination)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(io.MultiReader(strings.NewReader("partial archive"), errorReader{})),
			Request:    req,
		}, nil
	})}

	err = streamDesktopAPIResource(
		context.Background(),
		client,
		"http://127.0.0.1:43123",
		"capability",
		"/api/v1/tests/run-1/archive",
		mustDesktopPreviewContext(t, "admin", ""),
		grant,
	)
	if err == nil || !strings.Contains(err.Error(), "connection interrupted") {
		t.Fatalf("partial response error = %v", err)
	}
	data, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "original" {
		t.Fatalf("partial response replaced destination with %q", data)
	}
}

func TestDesktopAPIClientRefusesRedirectWithoutLeakingCapability(t *testing.T) {
	var redirectedRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if got := r.Header.Get(api.DesktopCapabilityHeader); got != "" {
			t.Errorf("capability leaked to redirect target: %q", got)
		}
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/collect")
		w.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	destination := filepath.Join(t.TempDir(), "archive.zip")
	_, grant, err := newWritePathGrant(destination)
	if err != nil {
		t.Fatal(err)
	}
	err = streamDesktopAPIResource(
		context.Background(),
		newDesktopAPIHTTPClient(),
		source.URL,
		"secret-capability",
		"/api/v1/tests/run-1/archive",
		mustDesktopPreviewContext(t, "admin", ""),
		grant,
	)
	if err == nil || !strings.Contains(err.Error(), "302 Found") {
		t.Fatalf("redirect error = %v", err)
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests", got)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("redirect response published a destination: %v", statErr)
	}
}

func TestValidateDesktopAPIResourcePath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "archive", raw: "/api/v1/tests/run-1/archive", want: "/api/v1/tests/run-1/archive"},
		{name: "query", raw: "/api/v1/tests/run-1/archive/report?format=pdf", want: "/api/v1/tests/run-1/archive/report?format=pdf"},
		{name: "absolute URL", raw: "https://example.com/api/v1/archive"},
		{name: "scheme relative", raw: "//example.com/api/v1/archive"},
		{name: "outside API", raw: "/health"},
		{name: "dot segment", raw: "/api/v1/tests/../secrets"},
		{name: "encoded dot segment", raw: "/api/v1/tests/%2e%2e/secrets"},
		{name: "backslash", raw: "/api/v1/tests\\run/archive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateDesktopAPIResourcePath(tt.raw)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("validateDesktopAPIResourcePath(%q) = %q, want error", tt.raw, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("validateDesktopAPIResourcePath(%q) = %q, %v; want %q", tt.raw, got, err, tt.want)
			}
		})
	}
}

func TestSafeDesktopDownloadName(t *testing.T) {
	if got := safeDesktopDownloadName("../../Quarterly\\archive.zip"); got != "----Quarterly-archive.zip" {
		t.Fatalf("safeDesktopDownloadName = %q", got)
	}
	if got := safeDesktopDownloadName(" . "); got != "download" {
		t.Fatalf("empty safeDesktopDownloadName = %q", got)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func mustDesktopPreviewContext(t *testing.T, mode, projectID string) api.DesktopDevPreviewContext {
	t.Helper()
	preview, err := api.ParseDesktopDevPreviewContext(mode, projectID)
	if err != nil {
		t.Fatalf("ParseDesktopDevPreviewContext: %v", err)
	}
	return preview
}

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("connection interrupted")
}
