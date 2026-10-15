package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserVersionParsing(t *testing.T) {
	for version, want := range map[string]int{"149.0.7827.55": 149, "26.0": 26, "152": 152} {
		if major, ok := BrowserMajorVersion(version); !ok || major != want {
			t.Errorf("BrowserMajorVersion(%q) = %d, %v", version, major, ok)
		}
	}
	for _, version := range []string{"", "HeadlessChrome/149", "149.0.0.0.0", "149.x", " 149.0"} {
		if ValidBrowserVersion(version) {
			t.Errorf("ValidBrowserVersion(%q) = true", version)
		}
	}
}

func writeDriverManifest(t *testing.T, chromium string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "package"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"comment":"x","browsers":[{"name":"chromium","revision":"1228","browserVersion":%q},{"name":"webkit","revision":"2311","browserVersion":"26.4"},{"name":"chromium-tip-of-tree","browserVersion":"151.0.1"}]}`, chromium)
	if err := os.WriteFile(filepath.Join(dir, "package", "browsers.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The driver's tested browser comes from its browsers.json, found the way
// playwright-go finds the driver.
func TestDriverBrowserVersionReadsTheDriverManifest(t *testing.T) {
	dir := writeDriverManifest(t, "149.0.7827.55")
	t.Setenv("PLAYWRIGHT_CLI_PATH", "")
	t.Setenv("PLAYWRIGHT_DRIVER_PATH", dir)
	if got := driverBrowserVersion("chromium"); got != "149.0.7827.55" {
		t.Fatalf("chromium = %q", got)
	}
	if got := driverBrowserVersion("chrome"); got != "149.0.7827.55" {
		t.Fatalf("chrome = %q", got)
	}
	if got := driverBrowserVersion("webkit"); got != "26.4" {
		t.Fatalf("webkit = %q", got)
	}
	other := writeDriverManifest(t, "150.0.1")
	t.Setenv("PLAYWRIGHT_DRIVER_PATH", "")
	t.Setenv("PLAYWRIGHT_CLI_PATH", filepath.Join(other, "package", "cli.js"))
	if got := driverBrowserVersion("chromium"); got != "150.0.1" {
		t.Fatalf("next to cli.js = %q", got)
	}
	t.Setenv("PLAYWRIGHT_CLI_PATH", filepath.Join(t.TempDir(), "cli.js"))
	if got := driverBrowserVersion("chromium"); got != "" {
		t.Fatalf("missing manifest = %q", got)
	}
}

// A browser of another major version than the driver's or the recording's is
// reported once, in the session's first action; with
// APEXLOAD_BROWSER_VERSION_SKEW=fail a driver mismatch fails the launch.
func TestCheckBrowserVersionReportsSkewOnce(t *testing.T) {
	t.Setenv("PLAYWRIGHT_CLI_PATH", "")
	t.Setenv("PLAYWRIGHT_DRIVER_PATH", writeDriverManifest(t, "149.0.7827.55"))
	t.Setenv(browserVersionSkewEnv, "")
	b := &Browser{engine: "chromium", recordedBrowserVersion: "148.0.7000.2"}
	if err := b.checkBrowserVersion("152.0.7900.1"); err != nil {
		t.Fatal(err)
	}
	first := &protocol.Result{}
	b.applyBrowserVersionSkew(first)
	if strings.Join(first.WarningCodes, ",") != "browser_version_skew,browser_recording_version_skew" {
		t.Fatalf("warnings = %v", first.WarningCodes)
	}
	want := `[map[actual:152.0.7900.1 expected:149.0.7827.55 source:driver] map[actual:152.0.7900.1 expected:148.0.7000.2 source:recording]]`
	if got := fmt.Sprint(first.ProtocolEvidence[BrowserVersionSkewEvidenceKey]); got != want {
		t.Fatalf("evidence = %s", got)
	}
	second := &protocol.Result{}
	b.applyBrowserVersionSkew(second)
	if err := b.checkBrowserVersion("152.0.7900.1"); err != nil {
		t.Fatal(err)
	}
	b.applyBrowserVersionSkew(second)
	if len(second.WarningCodes) != 0 || second.ProtocolEvidence != nil {
		t.Fatalf("reported again: %+v", second)
	}

	matching := &Browser{engine: "chromium"}
	if err := matching.checkBrowserVersion("149.0.7827.90"); err != nil {
		t.Fatal(err)
	}
	none := &protocol.Result{}
	matching.applyBrowserVersionSkew(none)
	if len(none.WarningCodes) != 0 {
		t.Fatalf("same major reported: %v", none.WarningCodes)
	}

	t.Setenv(browserVersionSkewEnv, "fail")
	strict := &Browser{engine: "chromium"}
	err := strict.checkBrowserVersion("152.0.7900.1")
	if err == nil || !strings.Contains(err.Error(), "152.0.7900.1") || !strings.Contains(err.Error(), "149.0.7827.55") {
		t.Fatalf("strict driver skew = %v", err)
	}
	if err := strict.checkBrowserVersion("152.0.7900.1"); err == nil {
		t.Fatal("a second launch of a strict session passed")
	}
	recordingOnly := &Browser{engine: "chromium", recordedBrowserVersion: "148.0.1"}
	if err := recordingOnly.checkBrowserVersion("149.0.7827.55"); err != nil {
		t.Fatalf("a recording skew failed a strict launch: %v", err)
	}
}

// A real session replaying a script recorded in another major version reports
// it in its first step only.
func TestBrowserSessionReportsRecordingVersionSkew(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>App</title><h1>App</h1>`)
	}))
	defer server.Close()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":                 server.URL,
		"browser_headless":         true,
		"browser_recorded_version": "100.0.4896.0",
	}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	defer b.Teardown(context.Background())
	run := func(name string) *protocol.Result {
		t.Helper()
		result, err := b.Execute(context.Background(), &protocol.Action{Name: name, Type: "browser_navigate", Target: "/"})
		if err != nil || !result.Success {
			t.Fatalf("%s: err %v, result %+v", name, err, result)
		}
		return result
	}
	first := run("Open")
	records, _ := first.ProtocolEvidence[BrowserVersionSkewEvidenceKey].([]any)
	if len(records) != 1 || !strings.Contains(strings.Join(first.WarningCodes, ","), "browser_recording_version_skew") {
		t.Fatalf("first step: warnings %v, evidence %v", first.WarningCodes, first.ProtocolEvidence[BrowserVersionSkewEvidenceKey])
	}
	if warnings, _ := first.ProtocolEvidence["warnings"].([]string); strings.Join(warnings, ",") != strings.Join(first.WarningCodes, ",") {
		t.Fatalf("evidence warnings = %v, result warnings %v", first.ProtocolEvidence["warnings"], first.WarningCodes)
	}
	record := records[0].(map[string]any)
	if record["source"] != BrowserVersionSkewRecording || record["expected"] != "100.0.4896.0" || !ValidBrowserVersion(fmt.Sprint(record["actual"])) {
		t.Fatalf("record = %v", record)
	}
	if second := run("Again"); second.ProtocolEvidence[BrowserVersionSkewEvidenceKey] != nil {
		t.Fatalf("second step reported again: %v", second.ProtocolEvidence)
	}
}

// A step whose evidence the browser recorded itself keeps the warnings that
// replay would otherwise build its evidence from.
func TestBrowserEvidenceKeepsStepWarnings(t *testing.T) {
	b := &Browser{harUnavailableReason: "har_budget_exhausted"}
	result := &protocol.Result{ProtocolEvidence: map[string]any{"aura_actions": map[string]any{"requests": 1}}}
	b.applyEvidenceOutcome(result)
	if warnings, _ := result.ProtocolEvidence["warnings"].([]string); strings.Join(warnings, ",") != "browser_har_unavailable_har_budget_exhausted" {
		t.Fatalf("evidence = %v", result.ProtocolEvidence)
	}
	plain := &protocol.Result{}
	(&Browser{harUnavailableReason: "har_budget_exhausted"}).applyEvidenceOutcome(plain)
	if plain.ProtocolEvidence != nil {
		t.Fatalf("a step without its own evidence got some: %v", plain.ProtocolEvidence)
	}
}
