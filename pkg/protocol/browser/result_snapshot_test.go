package browser

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserErrorSnapshotClonesResultState(t *testing.T) {
	result := &protocol.Result{
		PageURL:          "https://example.test",
		PageTitle:        "Example",
		HTML:             "<main>failed</main>",
		ScreenshotBase64: "encoded",
		ConsoleEntries:   []protocol.ConsoleEntry{{Text: "console"}},
		NetworkEntries:   []protocol.NetworkEntry{{URL: "/asset.js"}},
		DOMSummary: &protocol.DOMSummary{
			Title:       "Example",
			ReadyState:  "complete",
			VisibleText: []string{"first"},
		},
	}

	snapshot := browserErrorSnapshot(result)
	if snapshot.PageURL != result.PageURL || snapshot.PageTitle != result.PageTitle || snapshot.HTML != result.HTML || snapshot.ScreenshotBase64 != result.ScreenshotBase64 {
		t.Fatalf("snapshot scalar fields = %#v, want copied from result", snapshot)
	}
	snapshot.ConsoleEntries[0].Text = "changed"
	snapshot.NetworkEntries[0].URL = "/changed.js"
	snapshot.DOMSummary.VisibleText[0] = "changed"
	if result.ConsoleEntries[0].Text != "console" {
		t.Fatal("ConsoleEntries were aliased")
	}
	if result.NetworkEntries[0].URL != "/asset.js" {
		t.Fatal("NetworkEntries were aliased")
	}
	if result.DOMSummary.VisibleText[0] != "first" {
		t.Fatal("DOMSummary visible text was aliased")
	}
}
