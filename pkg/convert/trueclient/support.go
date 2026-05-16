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
		Notes:         "The MVP importer maps common navigation, click, fill, wait, simple JavaScript-derived fill or click, and transaction flows into browser DSL actions.",
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
		Status:           ScopePlanned,
		StormrunnerShape: "action assertions",
		Notes:            "Verification steps are expected to map into existing assertion support instead of a new browser action type.",
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
		Status:           ScopeReviewOnly,
		StormrunnerShape: "browser_click plus browser_fill",
		Notes:            "Select-style controls may require importer review because element semantics are recorder-specific.",
	},
	{
		CanonicalName:    "scroll",
		DisplayName:      "Scroll",
		Status:           ScopeReviewOnly,
		StormrunnerShape: "browser_swipe or preserved review note",
		Notes:            "Scroll intent depends on viewport state and may need manual repair during import review.",
	},
	{
		CanonicalName:    "evaluate_javascript",
		DisplayName:      "Evaluate JavaScript",
		Status:           ScopeReviewOnly,
		StormrunnerShape: "browser_fill or browser_click when decomposable",
		Notes:            "Simple jQuery and document.querySelector fill/click statements are decomposed; other JavaScript remains a review warning because Stormrunner does not expose a public browser-eval action.",
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
