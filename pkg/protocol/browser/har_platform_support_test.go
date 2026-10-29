package browser

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// withNativeHARUnsupported runs a test on the unsupported platform's path.
// Where the platform itself refuses native HAR nothing is replaced, so the
// test exercises the platform's real answer.
func withNativeHARUnsupported(t *testing.T) {
	t.Helper()
	if !NativeHARSupported() {
		return
	}
	previous := nativeHARSupported
	nativeHARSupported = func() bool { return false }
	t.Cleanup(func() { nativeHARSupported = previous })
}

// Native HAR ownership is qualified on darwin, linux and windows only.
func TestNativeHARSupportedMatchesPlatform(t *testing.T) {
	want := runtime.GOOS == "darwin" || runtime.GOOS == "linux" || runtime.GOOS == "windows"
	if got := NativeHARSupported(); got != want {
		t.Fatalf("NativeHARSupported() on %s = %v, want %v", runtime.GOOS, got, want)
	}
	if got := (&Browser{}).SupportsBrowserNativeHAR(); got != want {
		t.Fatalf("SupportsBrowserNativeHAR() on %s = %v, want %v", runtime.GOOS, got, want)
	}
	withNativeHARUnsupported(t)
	if (&Browser{}).SupportsBrowserNativeHAR() {
		t.Fatal("SupportsBrowserNativeHAR() remained true on an unsupported platform")
	}
}

// Without native HAR support a HAR-recording session runs without a HAR,
// reserves no writer allowance, creates no staging and says why once.
func TestUnsupportedPlatformHARCaptureRunsWithoutRecordingAndSaysSoOnce(t *testing.T) {
	withNativeHARUnsupported(t)
	budget := useTestEvidenceBudget(t, 64<<30, noUsage, 0)
	dir := t.TempDir()
	b := &Browser{harCapturePolicy: HARCaptureAlways, harDir: dir}
	var options playwright.BrowserNewContextOptions
	if err := b.applyHARCaptureOptions(&options); err != nil {
		t.Fatalf("an unsupported platform failed the session: %v", err)
	}
	if options.RecordHarPath != nil || b.harOwner != nil || b.harWorkingPath != "" {
		t.Fatal("an unsupported platform still recorded a HAR")
	}
	budget.mu.Lock()
	_, reserved := budget.writers[b]
	budget.mu.Unlock()
	if reserved || budget.counters.harAdmitted.Load() != 0 || budget.counters.harRefused.Load() != 0 {
		t.Fatal("an unsupported platform reserved or refused a HAR writer allowance")
	}
	assertHARDirEmpty(t, dir)
	first, second := &protocol.Result{Success: true}, &protocol.Result{Success: true}
	b.applyEvidenceOutcome(first)
	b.applyEvidenceOutcome(second)
	if !first.Success || !slices.Equal(first.WarningCodes, []string{"browser_har_unavailable_har_platform_unsupported"}) || len(second.WarningCodes) != 0 {
		t.Fatalf("warnings %v / %v", first.WarningCodes, second.WarningCodes)
	}
}

// Without native HAR support nothing opens, creates or cleans a HAR
// directory's staging, and published-artifact reads say why.
func TestUnsupportedPlatformHARNamespaceCreatesNothing(t *testing.T) {
	withNativeHARUnsupported(t)
	dir := t.TempDir()
	called := false
	err := WithRuntimeHARArtifactNamespace(context.Background(), dir, func(*os.Root) error {
		called = true
		return nil
	})
	if !errors.Is(err, errHARPlatformUnsupported) || called {
		t.Fatalf("namespace on an unsupported platform: called=%v err=%v", called, err)
	}
	if plans, err := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, 8); err != nil || len(plans) != 0 {
		t.Fatalf("cleanup plan on an unsupported platform: %v %v", plans, err)
	}
	if reclaimed, err := ReclaimRuntimeHARWorking(context.Background(), dir, 8); err != nil || reclaimed != 0 {
		t.Fatalf("reclaim on an unsupported platform: %d %v", reclaimed, err)
	}
	published := filepath.Join(dir, "apexload_browser_"+strings.Repeat("a", 32)+"_scrubbed.har")
	if err := os.WriteFile(published, []byte(`{"log":{"entries":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRuntimeHARArtifact(context.Background(), published, func(io.Reader, int64) error { return nil }); !errors.Is(err, errHARPlatformUnsupported) {
		t.Fatalf("read on an unsupported platform: %v", err)
	}
	if err := TakeRuntimeHARArtifact(context.Background(), published, func(io.Reader, int64) error { return nil }); !errors.Is(err, errHARPlatformUnsupported) {
		t.Fatalf("take on an unsupported platform: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, runtimeHARWorkingDirectory)); !os.IsNotExist(err) {
		t.Fatalf("an unsupported platform created %s: %v", runtimeHARWorkingDirectory, err)
	}
	if _, err := os.Stat(published); err != nil {
		t.Fatalf("an unsupported platform touched a published file: %v", err)
	}
}

// A HAR-recording browser VUser on an unsupported platform runs its actions,
// reports har_platform_unsupported and leaves its HAR directory empty.
func TestUnsupportedPlatformBrowserVUserRunsWithoutHAR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	withNativeHARUnsupported(t)
	server := harCaptureFixtureServer()
	defer server.Close()
	dir := t.TempDir()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":            server.URL,
		"browser_headless":    true,
		"browser_har_capture": HARCaptureAlways,
		"browser_har_dir":     dir,
	}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	result, err := b.Execute(context.Background(), &protocol.Action{Name: "open", Type: "browser_navigate", Target: "/"})
	if err != nil || !result.Success || result.ErrorMessage != "" {
		t.Fatalf("navigate: err=%v result=%+v", err, result)
	}
	if !slices.Contains(result.WarningCodes, "browser_har_unavailable_har_platform_unsupported") {
		t.Fatalf("warnings %v", result.WarningCodes)
	}
	if err := b.Teardown(context.Background()); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	if artifacts := b.TakeBrowserHARArtifacts(); len(artifacts) != 0 {
		t.Fatalf("published HARs %v", artifacts)
	}
	assertHARDirEmpty(t, dir)
}

func assertHARDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("HAR directory holds %v", names)
	}
}

func TestRuntimeSecretHARSuppressionWarning(t *testing.T) {
	for _, policy := range []string{HARCaptureAlways, HARCaptureOnError, HARCaptureOff, ""} {
		t.Run(policy, func(t *testing.T) {
			b := &Browser{harCapturePolicy: policy, harDir: t.TempDir()}
			b.SetRuntimeSecretActivity(func() bool { return true })
			var options playwright.BrowserNewContextOptions
			if err := b.applyHARCaptureOptions(&options); err != nil {
				t.Fatal(err)
			}
			if options.RecordHarPath != nil || b.harOwner != nil || b.harWorkingPath != "" {
				t.Fatal("secret suppression opened capture")
			}
			assertHARDirEmpty(t, b.harDir)
			first, second := &protocol.Result{Success: true}, &protocol.Result{Success: true}
			b.applyEvidenceOutcome(first)
			b.applyEvidenceOutcome(second)
			var want []string
			if policy != HARCaptureOff && policy != "" {
				want = []string{"browser_har_unavailable_har_runtime_secrets"}
			}
			if !first.Success || !second.Success || !slices.Equal(first.WarningCodes, want) || len(second.WarningCodes) != 0 {
				t.Fatalf("warnings %v / %v", first.WarningCodes, second.WarningCodes)
			}
			if len(b.TakeBrowserHARCaptureOmissions()) != 0 {
				t.Fatal("policy suppression counted as capture failure")
			}
		})
	}
}

func TestRuntimeSecretHARRemainsSuppressedForDebugger(t *testing.T) {
	b := &Browser{unredactedDebug: true, harCapturePolicy: HARCaptureAlways, harDir: t.TempDir()}
	b.SetRuntimeSecretActivity(func() bool { return true })
	var options playwright.BrowserNewContextOptions
	if err := b.applyHARCaptureOptions(&options); err != nil {
		t.Fatal(err)
	}
	if options.RecordHarPath != nil || b.harOwner != nil || b.harWorkingPath != "" {
		t.Fatal("debugger enabled HAR capture with runtime secrets")
	}
	assertHARDirEmpty(t, b.harDir)
}
