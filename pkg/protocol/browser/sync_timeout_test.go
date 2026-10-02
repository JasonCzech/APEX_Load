package browser

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserPostSyncUsesItsOwnTimeoutAndTriggerFrame(t *testing.T) {
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"browser_headless": true}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer b.Teardown(context.Background())
	if _, err := b.currentPage().Goto("about:blank"); err != nil {
		t.Fatal(err)
	}
	if err := b.currentPage().SetContent(`<iframe id="child" srcdoc="<div id='ready' style='display:none'>Ready</div><script>setTimeout(()=>document.querySelector('#ready').style.display='block',300)</script>"></iframe>`); err != nil {
		t.Fatal(err)
	}
	action := &protocol.Action{Name: "Trigger", Params: map[string]any{"frame_selector": "#child", "timeout_ms": 25}, Sync: &protocol.BrowserSyncPolicy{Type: "element_visible", Value: "#ready", TimeoutMs: 1000}}
	if err := b.runPostActionSync(context.Background(), action); err != nil {
		t.Fatalf("completion inherited short object timeout or lost frame: %v", err)
	}
	if action.Params["timeout_ms"] != 25 {
		t.Fatal("completion mutated trigger object budget")
	}
}
