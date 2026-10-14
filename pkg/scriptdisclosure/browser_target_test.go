package scriptdisclosure

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// A browser step's target is a URL only where the step navigates or matches
// responses. Elsewhere it is an element locator, window title or script, and
// a URL reading would turn a CSS "#id" into a fragment to disclose.
func TestProjectKeepsBrowserLocatorTargetsOutOfURLHandling(t *testing.T) {
	const secret = "browser-target-canary-91c2"
	locators := map[string]string{
		"hover":  "#react-external > div.hero > h2",
		"click":  `[aria-label="Salesforce, the #1 CRM"]`,
		"frame":  "iframe#checkout?step=2",
		"window": "Checkout #2",
	}
	script := &dsl.Script{
		Name: "Recorded", Version: "1", Protocol: "browser",
		Actions: []dsl.Action{
			{Name: "go", Type: "browser_navigate", Target: "https://user:" + secret + "@shop.example.test/start?access_token=" + secret},
			{Name: "hover", Type: "browser_hover", Target: locators["hover"]},
			{Name: "click", Type: "browser_click", Target: locators["click"]},
			{Name: "frame", Type: "browser_switch_frame", Target: locators["frame"]},
			{Name: "window", Type: "browser_switch_window", Target: locators["window"]},
			{Name: "js", Type: "browser_evaluate_js", Target: "localStorage.setItem('k', '" + secret + "')"},
			{Name: "window url", Type: "browser_switch_window", Target: "https://user:" + secret + "@shop.example.test/callback"},
		},
	}
	projected, err := ProjectWithCredentialSource(script, script)
	if err != nil {
		t.Fatalf("ProjectWithCredentialSource: %v", err)
	}
	for _, action := range projected.Actions {
		if want, ok := locators[action.Name]; ok && action.Target != want {
			t.Errorf("%s target = %q, want the locator unchanged %q", action.Name, action.Target, want)
		}
	}
	navigate := projected.Actions[0].Target
	if strings.Contains(navigate, secret) || !strings.Contains(navigate, credential.Redacted) {
		t.Errorf("navigate target = %q, want its credentials redacted as a URL", navigate)
	}
	if strings.Contains(projected.Actions[5].Target, secret) {
		t.Errorf("evaluate_js target = %q, want the known credential masked", projected.Actions[5].Target)
	}
	// A window switch to a URL is still read as a URL.
	if window := projected.Actions[6].Target; strings.Contains(window, secret) || !strings.Contains(window, credential.Redacted) {
		t.Errorf("switch_window URL target = %q, want its userinfo redacted", window)
	}
}
