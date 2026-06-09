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
	APEXLoadShape    string
	SupportedInputs  []string
	RequiredEvidence []string
	ReviewSections   []string
	Notes            string
}

type StepScope struct {
	CanonicalName string
	DisplayName   string
	Status        ScopeStatus
	APEXLoadShape string
	Notes         string
}

type ProfileCapabilityScope struct {
	CanonicalName    string
	DisplayName      string
	Status           ScopeStatus
	APEXLoadShape    string
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
		APEXLoadShape:    "browser script import",
		SupportedInputs:  []string{"LoadRunner ZIP", ".usr project metadata", "default.xml", "default.cfg", "parameter files"},
		RequiredEvidence: []string{"conversion warnings", "unsupported step summary", "browser replay validation"},
		ReviewSections:   []string{"preserved_metadata", "runtime_settings_parity", "browser_import_assumptions"},
		Notes:            "The MVP importer maps TruClient Mobile Web and TruClient 2.0 Mobile Web navigation, click, fill, select, wait, conservative verify assertions, simple JavaScript-derived fill or click, jQuery attr/trigger, and transaction flows into browser DSL actions.",
	},
	{
		CanonicalName:    "truclient2_web",
		DisplayName:      "TruClient 2.0 - Web",
		Status:           ScopeReviewOnly,
		APEXLoadShape:    "vugen_web2 browser profile",
		SupportedInputs:  []string{"LoadRunner ZIP", ".usr project metadata", "default.xml", "object repository files", "parameter files", "runtime settings"},
		RequiredEvidence: []string{"fixture-backed parser coverage", "object identification summary", "runtime settings parity", "browser replay evidence", "promotion review artifact", "browser-to-http conversion evidence"},
		ReviewSections:   []string{"web2_profile_summary", "object_identification", "runtime_settings_parity", "browser_replay_evidence", "promotion_review", "conversion_readiness"},
		Notes:            "Review-only VuGen Web 2.0 browser-profile import scope; supported step shapes produce a browser draft, but selector identity, waits, runtime parity, replay evidence, and the saved promotion review artifact must be reviewed before production promotion.",
	},
	{
		CanonicalName:    "truclient_web",
		DisplayName:      "TruClient - Web",
		Status:           ScopePlanned,
		APEXLoadShape:    "vugen_web2 browser profile",
		SupportedInputs:  []string{"LoadRunner ZIP", ".usr project metadata", "default.xml", "object repository files", "parameter files", "runtime settings"},
		RequiredEvidence: []string{"fixture-backed parser coverage", "object identification summary", "runtime settings parity", "browser replay evidence"},
		ReviewSections:   []string{"web2_profile_summary", "object_identification", "runtime_settings_parity", "browser_replay_evidence"},
		Notes:            "Planned via the same APEX Load Web 2.0 browser-profile path, with TruClient 2.0 evidence prioritized first.",
	},
	{
		CanonicalName:    "truclient_legacy_desktop",
		DisplayName:      "TruClient",
		Status:           ScopeOutOfScope,
		APEXLoadShape:    "manual guided review",
		SupportedInputs:  []string{"LoadRunner ZIP metadata only"},
		RequiredEvidence: []string{"manual migration decision"},
		ReviewSections:   []string{"unsupported_script_type"},
		Notes:            "Legacy desktop-style TruClient parity is out of scope for APEX Load browser import.",
	},
}

var stepScopes = []StepScope{
	{
		CanonicalName: "navigate",
		DisplayName:   "Navigate",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_navigate",
		Notes:         "URL-driven page transitions map cleanly to browser navigation.",
	},
	{
		CanonicalName: "click",
		DisplayName:   "Click / Submit / Check",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_click",
		Notes:         "Direct object clicks, submit actions, checkbox/radio selection actions, and double-click metadata map to browser click actions with the captured selector.",
	},
	{
		CanonicalName: "set_type",
		DisplayName:   "Set / Type",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_fill",
		Notes:         "Text entry is in scope for the MVP action mapping.",
	},
	{
		CanonicalName: "wait",
		DisplayName:   "Wait",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_wait",
		Notes:         "Wait conditions remain in scope when they can be normalized to existing browser wait semantics.",
	},
	{
		CanonicalName: "verify",
		DisplayName:   "Verify",
		Status:        ScopeSupported,
		APEXLoadShape: "action assertions",
		Notes:         "Verification steps map into browser action assertions instead of a separate browser action type.",
	},
	{
		CanonicalName: "start_transaction",
		DisplayName:   "Start Transaction",
		Status:        ScopeSupported,
		APEXLoadShape: "transaction metadata",
		Notes:         "Transaction boundaries should reuse the existing import preservation model instead of inventing a second representation.",
	},
	{
		CanonicalName: "end_transaction",
		DisplayName:   "End Transaction",
		Status:        ScopeSupported,
		APEXLoadShape: "transaction metadata",
		Notes:         "Transaction close events remain in scope with the same preservation model as start events.",
	},
	{
		CanonicalName: "select",
		DisplayName:   "Select",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_select",
		Notes:         "Select inputs map to a native browser_select action that chooses an option by value, text, or label.",
	},
	{
		CanonicalName: "scroll",
		DisplayName:   "Scroll",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_swipe",
		Notes:         "Direct Scroll steps and simple JavaScript scrollTo/scrollBy statements map to browser_swipe; viewport-specific behavior should still be replay-validated.",
	},
	{
		CanonicalName: "evaluate_javascript",
		DisplayName:   "Evaluate JavaScript",
		Status:        ScopeReviewOnly,
		APEXLoadShape: "browser_fill, browser_click, browser_swipe, or browser_navigate when decomposable",
		Notes:         "Simple jQuery .val(...), .attr('value', ...), .click(), and .trigger('click'), document.querySelector, first-match document.querySelectorAll, document.getElementById, first-match document.getElementsByName, first-match document.getElementsByClassName with safe class tokens, first-match document.getElementsByTagName with safe tag tokens, and document.forms[].elements[] fill/click statements plus window scroll and location navigation statements are decomposed into native browser actions. Collection first-match access supports both [0] and item(0). Statements that cannot be decomposed are imported as executable browser_evaluate_js actions but remain review-gated so recorded JavaScript is validated before promotion.",
	},
	{
		CanonicalName: "hover",
		DisplayName:   "Hover",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_hover",
		Notes:         "Hover and mouse-over steps map to the native browser_hover action using the captured selector or highest-confidence object-repository candidate.",
	},
	{
		CanonicalName: "press_key",
		DisplayName:   "Press Key",
		Status:        ScopeSupported,
		APEXLoadShape: "browser_press_key",
		Notes:         "Keyboard steps map to selector-scoped browser_press_key actions when object identification is available, with focused-page keypress replay as a fallback.",
	},
}

var web2ProfileCapabilityScopes = []ProfileCapabilityScope{
	{
		CanonicalName:    "archive_detection",
		DisplayName:      "Archive Detection",
		Status:           ScopeSupported,
		APEXLoadShape:    "LoadRunner ZIP classifier with Web 2.0 profile metadata",
		RequiredEvidence: []string{".usr type detection", "default.xml discovery", "object repository discovery"},
		Notes:            "The importer classifies TruClient - Web and TruClient 2.0 - Web before C-script conversion and preserves default.xml, object repositories, parameters, and runtime files for review.",
	},
	{
		CanonicalName:    "step_mapping",
		DisplayName:      "Step Mapping",
		Status:           ScopeSupported,
		APEXLoadShape:    "browser DSL actions plus review-only unsupported steps",
		RequiredEvidence: []string{"fixture step coverage", "unsupported step warnings", "transaction preservation"},
		Notes:            "Navigation, object interaction, hover, waits, verification, JavaScript fallback review gates, transactions, and parameterized input map into auditable browser actions for covered fixtures.",
	},
	{
		CanonicalName:    "object_identification",
		DisplayName:      "Object Identification",
		Status:           ScopeReviewOnly,
		APEXLoadShape:    "object repository descriptors, selector candidates, context evidence, diagnostics, and guided repair",
		RequiredEvidence: []string{"object repository parse summary", "selector ranking explanation", "replay validation"},
		Notes:            "APEX Load preserves object identity evidence and ranks selectors, but approval still requires browser replay evidence.",
	},
	{
		CanonicalName:    "runtime_settings",
		DisplayName:      "Runtime Settings",
		Status:           ScopeReviewOnly,
		APEXLoadShape:    "browser runtime settings parity summary",
		RequiredEvidence: []string{"browser runtime setting matrix", "unsupported setting warnings", "replay profile summary"},
		Notes:            "Browser, pacing, think time, iteration, proxy, credential, and replay behavior are summarized for review; production promotion still requires replay evidence.",
	},
	{
		CanonicalName:    "browser_to_http_conversion",
		DisplayName:      "Browser-To-HTTP Conversion",
		Status:           ScopeReviewOnly,
		APEXLoadShape:    "conversion candidate report and generated HTTP script draft",
		RequiredEvidence: []string{"request capture summary", "correlation candidate list", "conversion blockers"},
		Notes:            "Conversion should be treated as a governed draft flow, not an automatic promotion to high-density load.",
	},
	{
		CanonicalName:    "promotion_review_artifacts",
		DisplayName:      "Promotion Review Artifacts",
		Status:           ScopeSupported,
		APEXLoadShape:    "metadata.import.preserved.trueclient2_promotion_review",
		RequiredEvidence: []string{"replay session id", "artifact evidence counts", "JavaScript/review gate summary", "selector fallback inventory"},
		Notes:            "Saved Web 2.0 browser drafts preserve compact promotion evidence in script metadata so replay, selector, JavaScript, and runtime gate decisions survive beyond the transient import session.",
	},
	{
		CanonicalName:    "native_desktop_automation",
		DisplayName:      "Native Desktop Automation",
		Status:           ScopeOutOfScope,
		APEXLoadShape:    "manual migration guidance",
		RequiredEvidence: []string{"unsupported capability warning"},
		Notes:            "APEX Load browser parity targets web browser flows; native desktop automation remains outside the Web 2.0 profile.",
	},
	{
		CanonicalName:    "high_density_browser_load",
		DisplayName:      "High-Density Browser Load",
		Status:           ScopeOutOfScope,
		APEXLoadShape:    "browser canary plus converted HTTP load where suitable",
		RequiredEvidence: []string{"density warning", "scenario validation"},
		Notes:            "Browser VUsers remain diagnostic canaries; HTTP/API protocols carry density load after conversion and validation.",
	},
}

var web2FixtureContracts = []FixtureContract{
	{
		ID:               "web2_spa_checkout",
		DisplayName:      "SPA Checkout",
		Status:           ScopeSupported,
		Required:         true,
		Covers:           []string{"navigation", "click", "fill", "wait", "verification", "transactions", "parameterized data"},
		RequiredEvidence: []string{"converted browser actions", "selector stability summary", "successful replay artifact"},
		Notes:            "Baseline fixture for modern single-page application flows and transaction preservation.",
	},
	{
		ID:               "web2_iframe_window",
		DisplayName:      "Iframe And Window Context",
		Status:           ScopeSupported,
		Required:         true,
		Covers:           []string{"iframe targeting", "popup/window switch", "object repository context"},
		RequiredEvidence: []string{"context transition summary", "frame-scoped browser replay", "popup/window switch replay", "unsupported context warnings when needed"},
		Notes:            "Locks down context-aware object identification and browser replay for iframe and popup/window flows.",
	},
	{
		ID:               "web2_custom_javascript_api",
		DisplayName:      "Custom JavaScript API",
		Status:           ScopeSupported,
		Required:         true,
		Covers:           []string{"evaluate javascript", "decomposition", "review-only fallbacks", "repair suggestions"},
		RequiredEvidence: []string{"decomposed action list", "unsupported JavaScript inventory", "guided repair suggestions"},
		Notes:            "Prevents silent loss of TruClient JavaScript helper behavior during conversion.",
	},
	{
		ID:               "web2_object_repository_variants",
		DisplayName:      "Object Repository Variants",
		Status:           ScopeSupported,
		Required:         true,
		Covers:           []string{"css selectors", "xpath selectors", "text locators", "attribute locators", "selector ranking"},
		RequiredEvidence: []string{"parsed repository summary", "ranked selector candidates", "fallback reason codes"},
		Notes:            "Ensures imported object identity remains explainable and replayable.",
	},
	{
		ID:               "web2_runtime_parameters",
		DisplayName:      "Runtime And Parameters",
		Status:           ScopeSupported,
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
	switch normalized {
	case "double_click", "doubleclick", "submit", "check", "uncheck", "select_radio", "select_checkbox", "radio", "checkbox":
		normalized = "click"
	case "set", "type":
		normalized = "set_type"
	}
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
	case "truclientmobileweb", "truclient2mobileweb", "truclient20mobileweb", "web2uimob":
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
