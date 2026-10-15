package api

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// lightningReloadWarning warns about browser_navigate steps that load a
// Salesforce Lightning page once the iteration's journey has started, at its
// first browser step that is not a navigation. Each reloads the page and
// restarts Lightning, so the step's time includes the framework's startup,
// which a user moving through the app never waits for again. Older recordings
// and hand edits leave them.
func lightningReloadWarning(script *dsl.Script) string {
	if script == nil || script.Protocol != "browser" {
		return ""
	}
	var reloads []string
	started := false
	var walk func([]dsl.Action)
	walk = func(actions []dsl.Action) {
		for _, action := range actions {
			switch {
			case action.Type == "browser_navigate":
				// Navigations before the journey starts are where it starts.
				if started && lightningAppURL(script.Config.BaseURL, action.Target) {
					reloads = append(reloads, action.Name)
				}
			case strings.HasPrefix(action.Type, "browser_"):
				started = true
			}
			for _, branch := range action.Control.ActionBranches() {
				walk(branch.Actions)
			}
		}
	}
	walk(script.Actions)
	if len(reloads) == 0 {
		return ""
	}
	which := fmt.Sprintf("Step %q loads", reloads[0])
	if len(reloads) > 1 {
		which = fmt.Sprintf("%d steps (the first, %q) load", len(reloads), reloads[0])
	}
	return which + " a Salesforce Lightning page with browser_navigate mid-journey: that reloads the page and restarts Lightning, adding its startup to the step's time. Click through the app to the page instead, as a user does."
}

// lightningAppURL reports whether target, resolved against baseURL, is a
// Lightning Experience page: /lightning/ on a Lightning or My Domain host.
func lightningAppURL(baseURL, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" || strings.Contains(target, "{{") && !strings.HasPrefix(target, "/") {
		return false
	}
	resolved, err := url.Parse(target)
	if err != nil {
		return false
	}
	if !resolved.IsAbs() {
		base, err := url.Parse(strings.TrimSpace(baseURL))
		if err != nil || !base.IsAbs() {
			return false
		}
		resolved = base.ResolveReference(resolved)
	}
	host := strings.ToLower(resolved.Hostname())
	if !strings.HasSuffix(host, ".lightning.force.com") && !strings.HasSuffix(host, ".my.salesforce.com") {
		return false
	}
	return resolved.Path == "/lightning" || strings.HasPrefix(resolved.Path, "/lightning/")
}
