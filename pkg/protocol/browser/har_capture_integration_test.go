package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestNormalizeHARCapturePolicy(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: HARCaptureOff},
		{input: "off", want: HARCaptureOff},
		{input: "ON_ERROR", want: HARCaptureOnError},
		{input: "on_sla_breach", want: HARCaptureOnSLABreach},
		{input: "manual", want: HARCaptureManual},
		{input: " always ", want: HARCaptureAlways},
		{input: "bogus", want: HARCaptureOff},
	}
	for _, tt := range tests {
		if got := normalizeHARCapturePolicy(tt.input); got != tt.want {
			t.Errorf("normalizeHARCapturePolicy(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func harCaptureFixtureServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Set-Cookie", "sid=super-secret-cookie")
		_, _ = w.Write([]byte(`<!doctype html><html><body><p id="ready">ok</p></body></html>`))
	}))
}

func runHARCaptureSession(t *testing.T, policy string, harDir string, failAction bool) {
	t.Helper()
	server := harCaptureFixtureServer()
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":            server.URL,
		"browser_headless":    true,
		"browser_har_capture": policy,
		"browser_har_dir":     harDir,
	}); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}

	result, err := b.Execute(context.Background(), &protocol.Action{
		Name: "open", Type: "browser_navigate", Target: "/",
	})
	if err != nil || result.ErrorMessage != "" {
		t.Fatalf("navigate failed: err=%v msg=%s", err, result.ErrorMessage)
	}
	if failAction {
		failed, err := b.Execute(context.Background(), &protocol.Action{
			Name: "click missing", Type: "browser_click", Selector: "#does-not-exist",
			WaitFor: &protocol.WaitCondition{TimeoutMs: 800},
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if failed.ErrorMessage == "" {
			t.Fatal("expected the click on a missing selector to fail")
		}
	}
	if err := b.Teardown(context.Background()); err != nil {
		t.Fatalf("teardown: %v", err)
	}
}

func harArtifacts(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read har dir: %v", err)
	}
	artifacts := make([]string, 0)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".har") {
			artifacts = append(artifacts, filepath.Join(dir, entry.Name()))
		}
	}
	return artifacts
}

func TestHARCaptureAlwaysPersistsScrubbedArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	dir := t.TempDir()
	runHARCaptureSession(t, "always", dir, false)

	artifacts := harArtifacts(t, dir)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %v, want exactly one", artifacts)
	}
	if !strings.Contains(artifacts[0], scrub.SensitivityScrubbed) {
		t.Errorf("artifact name should carry sensitivity label: %s", artifacts[0])
	}
	data, err := os.ReadFile(artifacts[0])
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if strings.Contains(string(data), "super-secret-cookie") {
		t.Error("scrub-on-write failed: cookie value persisted")
	}
	if scrub.HARSensitivity(data) != scrub.SensitivityScrubbed {
		t.Error("artifact missing scrubbed sensitivity annotation")
	}
	if !strings.Contains(string(data), "entries") {
		t.Error("artifact does not look like a HAR document")
	}
}

func TestHARCaptureOnErrorDiscardsCleanSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	dir := t.TempDir()
	runHARCaptureSession(t, "on_error", dir, false)
	if artifacts := harArtifacts(t, dir); len(artifacts) != 0 {
		t.Errorf("clean on_error session should keep no artifact, got %v", artifacts)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(leftovers) != 0 {
		t.Errorf("working files not cleaned up: %v", leftovers)
	}
}

func TestHARCaptureOnErrorKeepsFailingSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	dir := t.TempDir()
	runHARCaptureSession(t, "on_error", dir, true)
	artifacts := harArtifacts(t, dir)
	if len(artifacts) != 1 {
		t.Fatalf("failing on_error session should keep one artifact, got %v", artifacts)
	}
	data, err := os.ReadFile(artifacts[0])
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if strings.Contains(string(data), "super-secret-cookie") {
		t.Error("scrub-on-write failed for on_error artifact")
	}
}

func TestHARCaptureOffKeepsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	dir := t.TempDir()
	runHARCaptureSession(t, "off", dir, false)
	if artifacts := harArtifacts(t, dir); len(artifacts) != 0 {
		t.Errorf("off policy should keep no artifact, got %v", artifacts)
	}
}
