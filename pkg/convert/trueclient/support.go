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
	CanonicalName    string
	DisplayName      string
	Status           ScopeStatus
	StormrunnerShape string
	SupportedInputs  []string
	RequiredEvidence []string
	ReviewSections   []string
	Notes            string
}

type StepScope struct {
	CanonicalName    string
	DisplayName      string
	Status           ScopeStatus
	StormrunnerShape string
	Notes            string
}

type ProfileCapabilityScope struct {
	CanonicalName    string
	DisplayName      string
	Status           ScopeStatus
	StormrunnerShape string
	RequiredEvidence []string
	Notes            string
}

type FixtureContract struct {
	ID               string
	DisplayName      string
	Status           ScopeStatus
	Required         bool
	Covers           []string
	RequiredEvidence []string
	Notes            string
}

type PlannedImportError struct {
	Scope      ScriptTypeScope
	Inspection *ArchiveInspection
}

func (e *PlannedImportError) Error() string {
	if e == nil {
		return ""
	}
	message := fmt.Sprintf("LoadRunner script type %q is recognized but not implemented yet. Current planned scope is %s", e.Scope.DisplayName, e.Scope.Notes)
	if e.Inspection == nil {
		return message
	}
	parts := make([]string, 0)
	if e.Inspection.DefaultXML != nil {
		parts = append(parts, fmt.Sprintf("default.xml with %d executable steps", e.Inspection.StepInventory.Total))
	}
	if count := len(e.Inspection.ObjectRepositories); count > 0 {
		parts = append(parts, fmt.Sprintf("%d object repository file(s)", count))
	}
	if count := len(e.Inspection.ParameterFiles); count > 0 {
		parts = append(parts, fmt.Sprintf("%d parameter file(s)", count))
	}
	if count := len(e.Inspection.RuntimeFiles); count > 0 {
		parts = append(parts, fmt.Sprintf("%d runtime file(s)", count))
	}
	if len(parts) > 0 {
		message += " Archive inspection found " + strings.Join(parts, ", ") + "."
	}
	return message
}

var scriptTypeScopes = []ScriptTypeScope{
	{
		CanonicalName:    "truclient_mobile_web",
		DisplayName:      "TruClient - Mobile Web",
		Status:           ScopeSupported,
		StormrunnerShape: "browser script import",
		SupportedInputs:  []string{"LoadRunner ZIP", ".usr project metadata", "default.xml", "default.cfg", "parameter files"},
		RequiredEvidence: []string{"conversion warnings", "unsupported step summary", "browser replay validation"},
		ReviewSections:   []string{"preserved_metadata", "runtime_settings_parity", "browser_import_assumptions"},
		Notes:            "The MVP importer maps TruClient Mobile Web and TruClient 2.0 Mobile Web navigation, click, fill, select, wait, conservative verify assertions, simple JavaScript-derived fill or click, jQuery attr/trigger, and transaction flows into browser DSL actions.",
	},
	{
		CanonicalName:    "truclient2_web",
		DisplayName:      "TruClient 2.0 - Web",
		Status:           ScopePlanned,
		StormrunnerShape: "vugen_web2 browser profile",
		SupportedInputs:  []string{"LoadRunner ZIP", ".usr project metadata", "default.xml", "object repository files", "parameter files", "runtime settings"},
		RequiredEvidence: []string{"fixture-backed parser coverage", "object identification summary", "runtime settings parity", "browser replay evidence", "browser-to-http conversion evidence"},
		ReviewSections:   []string{"web2_profile_summary", "object_identification", "runtime_settings_parity", "browser_replay_evidence", "conversion_readiness"},
		Notes:            "Planned VuGen Web 2.0 browser-profile import and creation scope; see docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md.",
	},
	{
		CanonicalName:    "truclient_web",
		DisplayName:      "TruClient - Web",
		Status:           ScopePlanned,
		StormrunnerShape: "vugen_web2 browser profile",
		SupportedInputs:  []string{"LoadRunner ZIP", ".usr project metadata", "default.xml", "object repository files", "parameter files", "runtime settings"},
		RequiredEvidence: []string{"fixture-backed parser coverage", "object identification summary", "runtime settings parity", "browser replay evidence"},
		ReviewSections:   []string{"web2_profile_summary", "object_identification", "runtime_settings_parity", "browser_replay_evidence"},
		Notes:            "Planned via the same Stormrunner Web 2.0 browser-profile path, with TruClient 2.0 evidence prioritized first.",
	},
	{
		CanonicalName:    "truclient_legacy_desktop",
		DisplayName:      "TruClient",
		Status:           ScopeOutOfScope,
		StormrunnerShape: "manual guided review",
		SupportedInputs:  []string{"LoadRunner ZIP metadata only"},
		RequiredEvidence: []string{"manual migration decision"},
		ReviewSections:   []string{"unsupported_script_type"},
		Notes:            "Legacy desktop-style TruClient parity is out of scope for Stormrunner browser import.",
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
		Notes:            "Simple jQuery .val(...), .attr('value', ...), .click(), and .trigger('click'), document.querySelector, first-match document.querySelectorAll, document.getElementById, first-match document.getElementsByName, first-match document.getElementsByClassName with safe class tokens, first-match document.getElementsByTagName with safe tag tokens, and document.forms[].elements[] fill/click statements plus window scroll and location navigation statements are decomposed. Collection first-match access supports both [0] and item(0); unsupported shapes stay in guided review with warnings, blocker repair suggestions, and preserved unsupported-step reporting because Stormrunner does not expose a public browser-eval action.",
	},
}

var web2ProfileCapabilityScopes = []ProfileCapabilityScope{
	{
		CanonicalName:    "archive_detection",
		DisplayName:      "Archive Detection",
		Status:           ScopePlanned,
		StormrunnerShape: "LoadRunner ZIP classifier with Web 2.0 profile metadata",
		RequiredEvidence: []string{".usr type detection", "default.xml discovery", "object repository discovery"},
		Notes:            "The importer must classify TruClient - Web and TruClient 2.0 - Web without routing them through Mobile Web or HTTP C-script conversion.",
	},
	{
		CanonicalName:    "step_mapping",
		DisplayName:      "Step Mapping",
		Status:           ScopePlanned,
		StormrunnerShape: "browser DSL actions plus review-only unsupported steps",
		RequiredEvidence: []string{"fixture step coverage", "unsupported step warnings", "transaction preservation"},
		Notes:            "Navigation, object interaction, waits, verification, transactions, and parameterized input should map into auditable browser actions.",
	},
	{
		CanonicalName:    "object_identification",
		DisplayName:      "Object Identification",
		Status:           ScopePlanned,
		StormrunnerShape: "selector candidates with stability scoring and fallback reasons",
		RequiredEvidence: []string{"object repository parse summary", "selector ranking explanation", "replay validation"},
		Notes:            "Stormrunner must preserve object identity evidence instead of flattening imported steps into opaque selectors.",
	},
	{
		CanonicalName:    "runtime_settings",
		DisplayName:      "Runtime Settings",
		Status:           ScopePlanned,
		StormrunnerShape: "browser runtime settings parity summary",
		RequiredEvidence: []string{"browser runtime setting matrix", "unsupported setting warnings", "replay profile summary"},
		Notes:            "Browser, pacing, think time, iteration, proxy, credential, and replay behavior need explicit parity status.",
	},
	{
		CanonicalName:    "browser_to_http_conversion",
		DisplayName:      "Browser-To-HTTP Conversion",
		Status:           ScopePlanned,
		StormrunnerShape: "conversion candidate report and generated HTTP script draft",
		RequiredEvidence: []string{"request capture summary", "correlation candidate list", "conversion blockers"},
		Notes:            "Conversion should be treated as a governed draft flow, not an automatic promotion to high-density load.",
	},
	{
		CanonicalName:    "native_desktop_automation",
		DisplayName:      "Native Desktop Automation",
		Status:           ScopeOutOfScope,
		StormrunnerShape: "manual migration guidance",
		RequiredEvidence: []string{"unsupported capability warning"},
		Notes:            "Stormrunner browser parity targets web browser flows; native desktop automation remains outside the Web 2.0 profile.",
	},
	{
		CanonicalName:    "high_density_browser_load",
		DisplayName:      "High-Density Browser Load",
		Status:           ScopeOutOfScope,
		StormrunnerShape: "browser canary plus converted HTTP load where suitable",
		RequiredEvidence: []string{"density warning", "scenario validation"},
		Notes:            "Browser VUsers remain diagnostic canaries; HTTP/API protocols carry density load after conversion and validation.",
	},
}

var web2FixtureContracts = []FixtureContract{
	{
		ID:               "web2_spa_checkout",
		DisplayName:      "SPA Checkout",
		Status:           ScopePlanned,
		Required:         true,
		Covers:           []string{"navigation", "click", "fill", "wait", "verification", "transactions", "parameterized data"},
		RequiredEvidence: []string{"converted browser actions", "selector stability summary", "successful replay artifact"},
		Notes:            "Baseline fixture for modern single-page application flows and transaction preservation.",
	},
	{
		ID:               "web2_iframe_window",
		DisplayName:      "Iframe And Window Context",
		Status:           ScopePlanned,
		Required:         true,
		Covers:           []string{"iframe targeting", "popup/window switch", "object repository context"},
		RequiredEvidence: []string{"context transition summary", "replay screenshots", "unsupported context warnings when needed"},
		Notes:            "Locks down context-aware object identification before Web 2.0 import is marked active.",
	},
	{
		ID:               "web2_custom_javascript_api",
		DisplayName:      "Custom JavaScript API",
		Status:           ScopePlanned,
		Required:         true,
		Covers:           []string{"evaluate javascript", "decomposition", "review-only fallbacks", "repair suggestions"},
		RequiredEvidence: []string{"decomposed action list", "unsupported JavaScript inventory", "guided repair suggestions"},
		Notes:            "Prevents silent loss of TruClient JavaScript helper behavior during conversion.",
	},
	{
		ID:               "web2_object_repository_variants",
		DisplayName:      "Object Repository Variants",
		Status:           ScopePlanned,
		Required:         true,
		Covers:           []string{"css selectors", "xpath selectors", "text locators", "attribute locators", "selector ranking"},
		RequiredEvidence: []string{"parsed repository summary", "ranked selector candidates", "fallback reason codes"},
		Notes:            "Ensures imported object identity remains explainable and replayable.",
	},
	{
		ID:               "web2_runtime_parameters",
		DisplayName:      "Runtime And Parameters",
		Status:           ScopePlanned,
		Required:         true,
		Covers:           []string{"runtime settings", "parameters", "data sources", "iteration behavior", "think time"},
		RequiredEvidence: []string{"runtime parity matrix", "data fit summary", "iteration goal summary"},
		Notes:            "Connects Web 2.0 authoring profile import with existing RTS and data-source parity work.",
	},
	{
		ID:               "web2_browser_to_http_conversion",
		DisplayName:      "Browser-To-HTTP Conversion",
		Status:           ScopePlanned,
		Required:         false,
		Covers:           []string{"network capture", "HTTP draft generation", "correlation candidates", "conversion blockers"},
		RequiredEvidence: []string{"request inventory", "correlation findings", "generated HTTP draft diff"},
		Notes:            "Optional fixture for promotion workflows that turn browser canaries into high-density HTTP scripts.",
	},
}

func ScriptTypeScopes() []ScriptTypeScope {
	return append([]ScriptTypeScope(nil), scriptTypeScopes...)
}

func StepScopes() []StepScope {
	return append([]StepScope(nil), stepScopes...)
}

func Web2ProfileCapabilityScopes() []ProfileCapabilityScope {
	return append([]ProfileCapabilityScope(nil), web2ProfileCapabilityScopes...)
}

func Web2FixtureContracts() []FixtureContract {
	return append([]FixtureContract(nil), web2FixtureContracts...)
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

func ImportScopeInspectionError(scriptType string, inspection *ArchiveInspection) error {
	scope, ok := LookupScriptTypeScope(scriptType)
	if !ok {
		return nil
	}
	if scope.Status == ScopePlanned {
		return &PlannedImportError{Scope: scope, Inspection: inspection}
	}
	return ImportScopeError(scriptType)
}

func normalizeScriptType(scriptType string) string {
	value := strings.ToLower(strings.TrimSpace(scriptType))
	value = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	compact := strings.ReplaceAll(value, " ", "")

	switch compact {
	case "truclientmobileweb", "truclient2mobileweb", "truclient20mobileweb":
		return "truclient_mobile_web"
	case "truclient2web", "truclient20web":
		return "truclient2_web"
	case "truclientweb":
		return "truclient_web"
	case "truclient", "truclient2", "truclient20":
		return "truclient_legacy_desktop"
	}

	switch value {
	case "truclient mobile web", "truclient 2 mobile web", "truclient 2 0 mobile web":
		return "truclient_mobile_web"
	case "truclient 2 web", "truclient 2 0 web":
		return "truclient2_web"
	case "truclient web":
		return "truclient_web"
	case "truclient", "truclient 2", "truclient 2 0":
		return "truclient_legacy_desktop"
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
