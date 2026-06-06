package browser

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserCloseClearsEphemeralState(t *testing.T) {
	b := &Browser{
		currentURL:         "https://example.test",
		slowMo:             50 * time.Millisecond,
		userAgent:          "CustomUA",
		captureScreenshots: "per_action",
		consoleEntries:     []protocol.ConsoleEntry{{Text: "console"}},
		networkEntries:     []protocol.NetworkEntry{{URL: "/asset.js"}},
	}

	b.close()

	if b.currentURL != "" || b.slowMo != 0 || b.userAgent != "" || b.captureScreenshots != "" {
		t.Fatalf("close left scalar state: url=%q slowMo=%v ua=%q screenshots=%q", b.currentURL, b.slowMo, b.userAgent, b.captureScreenshots)
	}
	if b.consoleEntries != nil || b.networkEntries != nil {
		t.Fatalf("close left event state: console=%v network=%v", b.consoleEntries, b.networkEntries)
	}
}
