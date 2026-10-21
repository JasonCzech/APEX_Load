package browser

import (
	"context"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
)

func TestBrowserAuthoredPageUnavailableWithoutNativeProof(t *testing.T) {
	source := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	for _, tc := range []struct {
		name, kind string
		step       *protocol.BrowserAuthoredStep
		reason     string
	}{
		{"historical", "browser_navigate", nil, "original_source_unavailable"},
		{"legacy input", "browser_navigate", protocol.NewBrowserAuthoredStep(source.ScriptSHA256, "actions[0]"), "original_source_unavailable"},
		{"same document", "browser_navigate", protocol.NewBrowserOriginalAuthoredStep(source, "actions[0]"), "navigation_response_unavailable"},
		{"history", "browser_back", protocol.NewBrowserOriginalAuthoredStep(source, "actions[0]"), "unsupported_navigation_binding"},
		{"new tab without response", "browser_open_window", protocol.NewBrowserOriginalAuthoredStep(source, "actions[0]"), "navigation_response_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &protocol.Result{}
			(&Browser{}).noteAuthoredPageResponse(context.Background(), &protocol.Action{Type: tc.kind, BrowserAuthoredStep: tc.step}, result, nil)
			raw, _ := json.Marshal(result.ProtocolEvidence[protocol.BrowserAuthoredPageEvidenceKey])
			receipt, err := protocol.DecodeBrowserAuthoredPageBinding(raw)
			if err != nil || receipt.State != "unavailable" || receipt.Reason != tc.reason || receipt.PageID != "" {
				t.Fatal(string(raw), err)
			}
		})
	}
}
