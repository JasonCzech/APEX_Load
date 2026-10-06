package browser

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestApplyCapturedPageState(t *testing.T) {
	b := &Browser{
		consoleEntries: []protocol.ConsoleEntry{{Text: "old"}, {Text: "new"}},
		networkEntries: []protocol.NetworkEntry{
			{URL: "/old"},
			{URL: "/new"},
		},
	}
	result := &protocol.Result{}
	pageState := capturedBrowserPageState{
		pageURL:    "https://example.test/next",
		pageTitle:  "Next",
		domSummary: &protocol.DOMSummary{Title: "Next", VisibleText: []string{"ready"}},
		html:       "<main>ready</main>",
		screenshot: "encoded",
	}

	b.applyCapturedPageState(result, pageState, 1, 1)

	if b.currentURL != pageState.pageURL || result.PageURL != pageState.pageURL {
		t.Fatalf("current/result URL = %q/%q, want %q", b.currentURL, result.PageURL, pageState.pageURL)
	}
	if result.PageTitle != "Next" || result.DOMSummary == nil || result.HTML != pageState.html || result.ScreenshotBase64 != pageState.screenshot {
		t.Fatalf("result page fields = %#v", result)
	}
	if len(result.ConsoleEntries) != 1 || result.ConsoleEntries[0].Text != "new" {
		t.Fatalf("ConsoleEntries = %#v, want new entry only", result.ConsoleEntries)
	}
	if len(result.NetworkEntries) != 1 || result.NetworkEntries[0].URL != "/new" {
		t.Fatalf("NetworkEntries = %#v, want new entry only", result.NetworkEntries)
	}
	result.ConsoleEntries[0].Text = "changed"
	result.NetworkEntries[0].URL = "/changed"
	if b.consoleEntries[1].Text != "new" || b.networkEntries[1].URL != "/new" {
		t.Fatal("event entries were aliased")
	}
}

func TestApplyCapturedPageStateLeavesCurrentURLEmptyWhenPageURLMissing(t *testing.T) {
	b := &Browser{currentURL: "https://example.test/current"}
	result := &protocol.Result{PageURL: "https://example.test/previous"}

	b.applyCapturedPageState(result, capturedBrowserPageState{pageTitle: "Untitled"}, 0, 0)

	if b.currentURL != "https://example.test/current" {
		t.Fatalf("currentURL = %q, want unchanged", b.currentURL)
	}
	if result.PageURL != "https://example.test/previous" {
		t.Fatalf("result.PageURL = %q, want unchanged", result.PageURL)
	}
	if result.PageTitle != "Untitled" {
		t.Fatalf("PageTitle = %q, want Untitled", result.PageTitle)
	}
}

func TestApplyCapturedPageStateSuppressesScreenshotWhenRuntimeSecretsAreActive(t *testing.T) {
	b := &Browser{}
	b.SetRuntimeSecretActivity(func() bool { return true })
	result := &protocol.Result{}
	b.applyCapturedPageState(result, capturedBrowserPageState{screenshot: "opaque-image"}, 0, 0)
	if result.ScreenshotBase64 != "" {
		t.Fatalf("ScreenshotBase64 = %q, want suppressed", result.ScreenshotBase64)
	}
	if screenshot, err := b.captureScreenshot(context.Background(), nil); err != nil || screenshot != "" {
		t.Fatalf("captureScreenshot with active secrets = %q, %v", screenshot, err)
	}
}

func TestApplyCapturedPageStateUsesOwnedNetworkSnapshotWithoutRecapture(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.networkEntries = []protocol.NetworkEntry{{URL: "/owned", RequestHeaders: map[string]string{"x-owned": "yes"}}}
	_, _, owner := b.actionEvidenceOffsets()
	entries, reason := b.acquireActionNetworkEntries(context.Background(), owner, 0)
	if reason != "" {
		t.Fatalf("owned snapshot reason = %q", reason)
	}
	b.mu.Lock()
	b.networkEntries = []protocol.NetworkEntry{{URL: "/later"}}
	b.mu.Unlock()
	result := &protocol.Result{}
	b.applyCapturedPageStateWithNetwork(result, capturedBrowserPageState{pageTitle: "Owned"}, 0, entries)
	if len(result.NetworkEntries) != 1 || result.NetworkEntries[0].URL != "/owned" || result.PageTitle != "Owned" {
		t.Fatalf("page finalization recaptured a newer network buffer: %#v", result)
	}
}
