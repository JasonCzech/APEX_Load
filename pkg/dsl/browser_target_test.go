package dsl

import (
	"strings"
	"testing"
)

// The kinds follow the catalog's own description of each browser action's
// target, so a new action cannot have its target read the wrong way.
func TestBrowserActionTargetKindFollowsTheCatalog(t *testing.T) {
	for _, spec := range browserActionSpecs() {
		for _, field := range spec.Fields {
			if field.Name != "target" {
				continue
			}
			want := BrowserTargetLocator
			switch {
			case strings.HasPrefix(field.Doc, "Destination URL"), strings.HasPrefix(field.Doc, "URL substring"):
				want = BrowserTargetURL
			case strings.HasPrefix(field.Doc, "JavaScript source"):
				want = BrowserTargetScript
			}
			if got := BrowserActionTargetKind(spec.Type); got != want {
				t.Errorf("BrowserActionTargetKind(%q) = %d, want %d for target %q", spec.Type, got, want, field.Doc)
			}
		}
	}
	if got := BrowserActionTargetKind("request"); got != BrowserTargetNotBrowser {
		t.Errorf("BrowserActionTargetKind(request) = %d, want BrowserTargetNotBrowser", got)
	}
	// A window switch to a URL is URL-handled; one to a title is not.
	if got := BrowserTargetValueKind("browser_switch_window", "https://app.example.test/cb?code=x"); got != BrowserTargetURL {
		t.Errorf("switch_window URL kind = %d, want BrowserTargetURL", got)
	}
	if got := BrowserTargetValueKind("browser_switch_window", "Checkout #2"); got != BrowserTargetLocator {
		t.Errorf("switch_window title kind = %d, want BrowserTargetLocator", got)
	}
}
