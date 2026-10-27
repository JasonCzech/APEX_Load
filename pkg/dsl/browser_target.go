package dsl

import "strings"

// BrowserTargetKind is what a browser action's target field holds.
type BrowserTargetKind int

const (
	// BrowserTargetNotBrowser marks an action that is not a browser action.
	BrowserTargetNotBrowser BrowserTargetKind = iota
	// BrowserTargetURL is a destination URL or a URL substring to match.
	BrowserTargetURL
	// BrowserTargetScript is JavaScript source.
	BrowserTargetScript
	// BrowserTargetLocator is an element or frame locator, or a window title.
	// It is never a URL: a CSS "#id" is not a fragment.
	BrowserTargetLocator
)

// BrowserActionTargetKind says what the target of an action of this type
// holds, as the browser catalog documents it.
func BrowserActionTargetKind(actionType string) BrowserTargetKind {
	actionType = strings.ToLower(strings.TrimSpace(actionType))
	if !strings.HasPrefix(actionType, "browser_") {
		return BrowserTargetNotBrowser
	}
	switch actionType {
	case "browser_navigate", "browser_open_window", "browser_wait_response":
		return BrowserTargetURL
	case "browser_evaluate_js":
		return BrowserTargetScript
	default:
		return BrowserTargetLocator
	}
}

// BrowserTargetValueKind is BrowserActionTargetKind for one target value: a
// window switch names its window by title, index or URL, and a URL there is
// read as one.
func BrowserTargetValueKind(actionType, target string) BrowserTargetKind {
	trimmed := strings.TrimSpace(actionType)
	kind := BrowserActionTargetKind(trimmed)
	if kind == BrowserTargetLocator &&
		(strings.EqualFold(trimmed, "browser_switch_window") || strings.EqualFold(trimmed, "browser_close_window")) &&
		strings.Contains(target, "://") {
		return BrowserTargetURL
	}
	return kind
}
