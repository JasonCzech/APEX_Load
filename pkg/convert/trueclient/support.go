package trueclient

import (
	"fmt"
	"strings"
)

type ScopeStatus string

const (
	ScopeSupported  ScopeStatus = "supported"
	ScopePlanned    ScopeStatus = "planned"
	ScopeReviewOnly ScopeStatus = "review_only"
	ScopeOutOfScope ScopeStatus = "out_of_scope"
)

type ScriptTypeScope struct {
	CanonicalName string
	DisplayName   string
	Status        ScopeStatus
	Notes         string
}

type StepScope struct {
	CanonicalName    string
	DisplayName      string
	Status           ScopeStatus
	StormrunnerShape string
	Notes            string
}

var scriptTypeScopes = []ScriptTypeScope{
	{
		CanonicalName: "truclient_mobile_web",
		DisplayName:   "TruClient - Mobile Web",
		Status:        ScopeSupported,
		Notes:         "The MVP importer maps navigation, click, fill, select, wait, conservative verify assertions, simple JavaScript-derived fill or click, and transaction flows into browser DSL actions.",
	},
	{
		CanonicalName: "truclient_web",
		DisplayName:   "TruClient",
		Status:        ScopeOutOfScope,
		Notes:         "Legacy desktop-style TruClient parity is out of scope for Stormrunner browser import.",
	},
}

var stepScopes = []StepScope{
	{
		CanonicalName:    "navigate",
		DisplayName:      "Navigate",
		Status:           ScopeSupported,
		StormrunnerShape: "browser_navigate",
		Notes:            "URL-driven page transitions map cleanly to browser navigation.",
	},
	{
		CanonicalName:    "click",
		DisplayName:      "Click",
		Status:           ScopeSupported,
		StormrunnerShape: "browser_click",
		Notes:            "Direct object clicks map to browser click actions with the captured selector.",
	},
	{
		CanonicalName:    "set_type",
		DisplayName:      "Set / Type",
		Status:           ScopeSupported,
		StormrunnerShape: "browser_fill",
		Notes:            "Text entry is in scope for the MVP action mapping.",
	},
	{
		CanonicalName:    "wait",
		DisplayName:      "Wait",
		Status:           ScopeSupported,
		StormrunnerShape: "browser_wait",
		Notes:            "Wait conditions remain in scope when they can be normalized to existing browser wait semantics.",
	},
	{
		CanonicalName:    "verify",
		DisplayName:      "Verify",
		Status:           ScopeSupported,
		StormrunnerShape: "action assertions",
		Notes:            "Verification steps map into browser action assertions instead of a separate browser action type.",
	},
	{
		CanonicalName:    "start_transaction",
		DisplayName:      "Start Transaction",
		Status:           ScopeSupported,
		StormrunnerShape: "transaction metadata",
		Notes:            "Transaction boundaries should reuse the existing import preservation model instead of inventing a second representation.",
	},
	{
		CanonicalName:    "end_transaction",
		DisplayName:      "End Transaction",
		Status:           ScopeSupported,
		StormrunnerShape: "transaction metadata",
		Notes:            "Transaction close events remain in scope with the same preservation model as start events.",
	},
	{
		CanonicalName:    "select",
		DisplayName:      "Select",
		Status:           ScopeSupported,
		StormrunnerShape: "browser_select",
		Notes:            "Select inputs map to a native browser_select action that chooses an option by value, text, or label.",
	},
	{
		CanonicalName:    "scroll",
		DisplayName:      "Scroll",
		Status:           ScopeSupported,
		StormrunnerShape: "browser_swipe",
		Notes:            "Direct Scroll steps and simple JavaScript scrollTo/scrollBy statements map to browser_swipe; viewport-specific behavior should still be replay-validated.",
	},
	{
		CanonicalName:    "evaluate_javascript",
		DisplayName:      "Evaluate JavaScript",
		Status:           ScopeReviewOnly,
		StormrunnerShape: "browser_fill, browser_click, browser_swipe, or browser_navigate when decomposable",
		Notes:            "Simple jQuery/document.querySelector fill/click, window scroll, and location navigation statements are decomposed; unsupported shapes stay in guided review with warnings, blocker repair suggestions, and preserved unsupported-step reporting because Stormrunner does not expose a public browser-eval action.",
	},
}

func ScriptTypeScopes() []ScriptTypeScope {
	return append([]ScriptTypeScope(nil), scriptTypeScopes...)
}

func StepScopes() []StepScope {
	return append([]StepScope(nil), stepScopes...)
}

func LookupScriptTypeScope(scriptType string) (ScriptTypeScope, bool) {
	normalized := normalizeScriptType(scriptType)
	for _, scope := range scriptTypeScopes {
		if scope.CanonicalName == normalized {
			return scope, true
		}
	}
	return ScriptTypeScope{}, false
}

func LookupStepScope(stepName string) (StepScope, bool) {
	normalized := normalizeStepName(stepName)
	for _, scope := range stepScopes {
		if scope.CanonicalName == normalized {
			return scope, true
		}
	}
	return StepScope{}, false
}

func ImportScopeError(scriptType string) error {
	scope, ok := LookupScriptTypeScope(scriptType)
	if !ok {
		return nil
	}

	switch scope.Status {
	case ScopeSupported:
		return nil
	case ScopePlanned:
		return fmt.Errorf("LoadRunner script type %q is recognized but not implemented yet. Current planned scope is %s", scope.DisplayName, scope.Notes)
	case ScopeOutOfScope:
		return fmt.Errorf("LoadRunner script type %q is out of scope. %s", scope.DisplayName, scope.Notes)
	default:
		return fmt.Errorf("LoadRunner script type %q requires guided import review. %s", scope.DisplayName, scope.Notes)
	}
}

func normalizeScriptType(scriptType string) string {
	value := strings.ToLower(strings.TrimSpace(scriptType))
	value = strings.NewReplacer("_", " ", "-", " ").Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	compact := strings.ReplaceAll(value, " ", "")

	switch compact {
	case "truclientmobileweb":
		return "truclient_mobile_web"
	case "truclient", "truclientweb":
		return "truclient_web"
	}

	switch value {
	case "truclient mobile web":
		return "truclient_mobile_web"
	case "truclient", "truclient web":
		return "truclient_web"
	default:
		return ""
	}
}

func normalizeStepName(stepName string) string {
	value := strings.ToLower(strings.TrimSpace(stepName))
	replacer := strings.NewReplacer("/", " ", "-", " ", "_", " ")
	value = replacer.Replace(value)
	value = strings.Join(strings.Fields(value), "_")
	return value
}
