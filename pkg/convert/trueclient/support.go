package trueclient

import (
	"fmt"
	"strings"
)

type ScopeStatus string

const (
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
		Status:        ScopePlanned,
		Notes:         "The planned MVP targets navigate, click or tap, fill, wait, verify or assertion, and transaction flows once representative fixture archives are available.",
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
		Status:           ScopePlanned,
		StormrunnerShape: "browser_navigate",
		Notes:            "URL-driven page transitions map cleanly to browser navigation.",
	},
	{
		CanonicalName:    "click",
		DisplayName:      "Click",
		Status:           ScopePlanned,
		StormrunnerShape: "browser_click or browser_tap",
		Notes:            "The importer can choose click versus tap from the resolved device profile.",
	},
	{
		CanonicalName:    "set_type",
		DisplayName:      "Set / Type",
		Status:           ScopePlanned,
		StormrunnerShape: "browser_fill",
		Notes:            "Text entry is in scope for the MVP action mapping.",
	},
	{
		CanonicalName:    "wait",
		DisplayName:      "Wait",
		Status:           ScopePlanned,
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
		Status:           ScopePlanned,
		StormrunnerShape: "transaction metadata",
		Notes:            "Transaction boundaries should reuse the existing import preservation model instead of inventing a second representation.",
	},
	{
		CanonicalName:    "end_transaction",
		DisplayName:      "End Transaction",
		Status:           ScopePlanned,
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
		Status:           ScopeOutOfScope,
		StormrunnerShape: "not supported",
		Notes:            "Stormrunner does not expose a supported browser-eval action in the current public DSL.",
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
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, " - ", " ")

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