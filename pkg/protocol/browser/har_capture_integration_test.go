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
	playwright "github.com/playwright-community/playwright-go"
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
	runHARCaptureSessionWithManualRequest(t, policy, harDir, failAction, false)
}

func runHARCaptureSessionWithManualRequest(t *testing.T, policy string, harDir string, failAction bool, manualRequest bool) {
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
	if manualRequest {
		manual, err := b.Execute(context.Background(), &protocol.Action{
			Name:   "mark manual har",
			Type:   "browser_wait",
			Params: map[string]any{"capture_har": true},
			WaitFor: &protocol.WaitCondition{
				Type:  "duration",
				Value: "25ms",
			},
		})
		if err != nil || manual.ErrorMessage != "" {
			t.Fatalf("manual marker failed: err=%v msg=%s", err, manual.ErrorMessage)
		}
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

func TestFinalizeHARCaptureRedactsLowEntropyRuntimeSecrets(t *testing.T) {
	const secret = "tiny"

	dir := t.TempDir()
	workingPath := filepath.Join(dir, "apexload_browser_runtime.har.tmp")
	fixture := `{
		"log": {
			"version": "1.2",
			"creator": {"name": "test", "version": "1"},
			"entries": [{
				"request": {
					"url": "https://example.test/path/` + secret + `",
					"headers": [],
					"cookies": [],
					"queryString": [{"name": "ordinary", "value": "` + secret + `"}]
				},
				"response": {
					"headers": [],
					"cookies": [],
					"content": {"text": "prefix ` + secret + ` suffix"}
				}
			}]
		}
	}`
	if err := os.WriteFile(workingPath, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write HAR fixture: %v", err)
	}

	b := &Browser{
		harCapturePolicy: HARCaptureAlways,
		harWorkingPath:   workingPath,
	}
	b.SetRuntimeTextRedactor(func(value string) string {
		return strings.ReplaceAll(value, secret, scrub.Redacted)
	})

	artifactPath := b.finalizeHARCapture()
	if artifactPath == "" {
		t.Fatal("expected scrubbed HAR artifact")
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read scrubbed HAR artifact: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("runtime secret persisted in HAR artifact: %s", data)
	}
	if !strings.Contains(string(data), scrub.Redacted) {
		t.Fatalf("HAR artifact missing redaction marker: %s", data)
	}
	if got := scrub.HARSensitivity(data); got != scrub.SensitivityScrubbed {
		t.Fatalf("HAR sensitivity = %q, want %q", got, scrub.SensitivityScrubbed)
	}
}

func TestFinalizeHARCaptureRedactsPercentEncodedRuntimeSecrets(t *testing.T) {
	const secret = "a&b"
	dir := t.TempDir()
	workingPath := filepath.Join(dir, "apexload_browser_encoded.har.tmp")
	fixture := `{"log":{"version":"1.2","creator":{"name":"test","version":"1"},"entries":[{"request":{"url":"https://example.test/?q=a%26b","headers":[],"cookies":[],"queryString":[{"name":"q","value":"a%26b"}]},"response":{"headers":[],"cookies":[],"content":{"text":"YSZi"}}}]}}`
	if err := os.WriteFile(workingPath, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &Browser{harCapturePolicy: HARCaptureAlways, harWorkingPath: workingPath}
	variants := map[string]struct{}{secret: {}, "a%26b": {}, "YSZi": {}}
	b.SetRuntimeTextRedactor(func(value string) string {
		for variant := range variants {
			value = strings.ReplaceAll(value, variant, scrub.Redacted)
		}
		return value
	})
	artifact := b.finalizeHARCapture()
	if artifact == "" {
		t.Fatal("expected scrubbed HAR artifact")
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	for variant := range variants {
		if strings.Contains(string(data), variant) {
			t.Fatalf("encoded runtime secret %q survived HAR scrubbing: %s", variant, data)
		}
	}
}

func TestApplyHARCaptureOptionsPurgesOrphansButPreservesActiveWorkingFiles(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, "apexload_browser_orphan.har.tmp")
	active := filepath.Join(dir, "apexload_browser_active.har.tmp")
	if err := os.WriteFile(orphan, []byte("raw"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	registerRuntimeHARWorkingPath(active)
	t.Cleanup(func() { unregisterRuntimeHARWorkingPath(active) })
	b := &Browser{harCapturePolicy: HARCaptureAlways, harDir: dir}
	options := &playwright.BrowserNewContextOptions{}
	b.applyHARCaptureOptions(options)
	t.Cleanup(func() { unregisterRuntimeHARWorkingPath(b.harWorkingPath) })
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan raw HAR survived new-session purge: %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active HAR was purged: %v", err)
	}
	if !RuntimeHARWorkingPathActive(b.harWorkingPath) {
		t.Fatal("new HAR working path was not registered active")
	}
}

func TestApplyHARCaptureOptionsDisablesRawCaptureForRuntimeSecrets(t *testing.T) {
	dir := t.TempDir()
	b := &Browser{harCapturePolicy: HARCaptureAlways, harDir: dir}
	b.SetRuntimeSecretActivity(func() bool { return true })
	options := &playwright.BrowserNewContextOptions{}
	b.applyHARCaptureOptions(options)
	if options.RecordHarPath != nil || b.harWorkingPath != "" {
		t.Fatalf("runtime secrets enabled raw HAR capture: options=%#v path=%q", options, b.harWorkingPath)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("runtime secret gate created HAR artifacts: %#v", entries)
	}
}

func TestDiscardHARWorkingFileUnregistersAndRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload_browser_init_failure.har.tmp")
	if err := os.WriteFile(path, []byte(`{"log":{"entries":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registerRuntimeHARWorkingPath(path)
	b := &Browser{harWorkingPath: path}
	b.discardHARWorkingFile()
	if RuntimeHARWorkingPathActive(path) {
		t.Fatal("discarded HAR working file is still registered as active")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("discarded HAR working file still exists: %v", err)
	}
	if b.harWorkingPath != "" {
		t.Fatalf("browser working path = %q, want empty", b.harWorkingPath)
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

func TestHARCaptureManualKeepsOnlyRequestedSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	dir := t.TempDir()
	runHARCaptureSession(t, "manual", dir, false)
	if artifacts := harArtifacts(t, dir); len(artifacts) != 0 {
		t.Fatalf("manual HAR without request produced artifacts: %v", artifacts)
	}

	runHARCaptureSessionWithManualRequest(t, "manual", dir, false, true)
	if artifacts := harArtifacts(t, dir); len(artifacts) != 1 {
		t.Fatalf("manual HAR with request artifacts = %v, want one", artifacts)
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
