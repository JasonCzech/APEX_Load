package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A browser_navigate to a Lightning page after the journey starts reloads the
// app; the first navigation, other hosts and other paths do not warn.
func TestLightningReloadWarning(t *testing.T) {
	script := &dsl.Script{
		Name: "journey", Protocol: "browser",
		Config: dsl.ScriptConfig{BaseURL: "https://acme.lightning.force.com"},
		Actions: []dsl.Action{
			{Name: "Open home", Type: "browser_navigate", Target: "/lightning/page/home"},
			{Name: "Accounts", Type: "browser_click", Target: "a[title=Accounts]"},
			{Name: "Account", Type: "browser_navigate", Target: "https://acme.my.salesforce.com/lightning/r/Account/001/view"},
			{Name: "Help", Type: "browser_navigate", Target: "https://help.salesforce.com/s/"},
			{Name: "Classic", Type: "browser_navigate", Target: "/001/o"},
			{Name: "Loop", Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{
				{Name: "Opportunity", Type: "browser_navigate", Target: "/lightning/o/Opportunity/list"},
			}}},
		},
	}
	got := lightningReloadWarning(script)
	if !strings.Contains(got, `2 steps (the first, "Account") load a Salesforce Lightning page`) {
		t.Fatalf("warning = %q", got)
	}
	script.Actions = script.Actions[:2]
	if got := lightningReloadWarning(script); got != "" {
		t.Fatalf("the journey's first navigation warned: %q", got)
	}
	if got := validationWarnings(&dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "browser_navigate", Target: "/lightning/x"}}}); len(got) != 0 {
		t.Fatalf("an HTTP script warned: %v", got)
	}
}
