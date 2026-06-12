package api

import (
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type AuthoringCapabilityResponse struct {
	ContractVersion string                       `json:"contract_version"`
	DraftLifecycle  []string                     `json:"draft_lifecycle"`
	HealthInputs    []string                     `json:"health_inputs"`
	Sources         []AuthoringSourceCapability  `json:"sources"`
	SourceMap       map[string]AuthoringFeatures `json:"source_map"`
}

type AuthoringSourceCapability struct {
	Source       string            `json:"source"`
	DisplayName  string            `json:"display_name"`
	Status       string            `json:"status"`
	Acquisition  []string          `json:"acquisition"`
	Features     AuthoringFeatures `json:"features"`
	HealthInputs []string          `json:"health_inputs"`
	Notes        []string          `json:"notes,omitempty"`
}

type AuthoringFeatures struct {
	DraftValidation       bool `json:"draft_validation"`
	StoredValidation      bool `json:"stored_validation"`
	Replay                bool `json:"replay"`
	BrowserReplay         bool `json:"browser_replay"`
	ImportReview          bool `json:"import_review"`
	RepairSuggestions     bool `json:"repair_suggestions"`
	SaveHandoff           bool `json:"save_handoff"`
	MultiScript           bool `json:"multi_script"`
	ScenarioDraft         bool `json:"scenario_draft"`
	Provenance            bool `json:"provenance"`
	ApprovalGateCandidate bool `json:"approval_gate_candidate"`
}

type AuthoringHealth struct {
	Score  int                   `json:"score"`
	Status string                `json:"status"`
	Inputs []AuthoringHealthItem `json:"inputs"`
}

type AuthoringHealthItem struct {
	Key     string `json:"key"`
	Status  string `json:"status"`
	Count   int    `json:"count,omitempty"`
	Summary string `json:"summary"`
}

func buildStoredScriptAuthoringHealth(script *dsl.Script) *AuthoringHealth {
	if script == nil {
		return nil
	}
	health := buildScriptValidationResponse(script.ID, script).AuthoringHealth
	return &health
}

func buildAuthoringHealth(issues []string, warnings []string, parityCounts map[string]int, dataSourceFit *DataSourceFitReport, aiGuardrails *AIGuardrailReport, scriptHasProvenance bool) AuthoringHealth {
	score := 100
	inputs := []AuthoringHealthItem{
		{Key: "validation_issues", Status: "pass", Count: len(issues), Summary: "Script validation passed."},
		{Key: "warnings", Status: "pass", Count: len(warnings), Summary: "No validation warnings."},
		{Key: "runtime_parity", Status: "pass", Summary: "No runtime parity blockers were detected."},
		{Key: "data_source_fit", Status: "pass", Summary: "Data sources match referenced parameters and load-model needs."},
		{Key: "ai_guardrails", Status: "pass", Summary: "No AI-generation guardrails apply to this draft."},
		{Key: "replay_readiness", Status: "pass", Summary: "Draft is structurally ready for replay."},
		{Key: "provenance", Status: "info", Summary: "No source provenance is required for manually authored drafts."},
	}
	if len(issues) > 0 {
		score -= 45
		inputs[0] = AuthoringHealthItem{Key: "validation_issues", Status: "blocker", Count: len(issues), Summary: "Script has validation issues that block save or replay."}
		inputs[5] = AuthoringHealthItem{Key: "replay_readiness", Status: "blocker", Summary: "Replay should wait until validation blockers are fixed."}
	}
	if len(warnings) > 0 {
		score -= minInt(15, len(warnings)*5)
		inputs[1] = AuthoringHealthItem{Key: "warnings", Status: "warning", Count: len(warnings), Summary: "Validation warnings need review before promotion."}
	}
	parityPenalty := runtimeParityHealthPenalty(parityCounts)
	if parityPenalty > 0 {
		score -= parityPenalty
		inputs[2] = AuthoringHealthItem{Key: "runtime_parity", Status: "warning", Count: parityPenalty, Summary: "Runtime parity has partial, compatibility-only, or blocked settings."}
	}
	if dataSourceFit != nil {
		blocked := dataSourceFit.StatusCounts["blocked"]
		needsReview := dataSourceFit.StatusCounts["needs_review"]
		if blocked > 0 {
			score -= minInt(35, blocked*15)
			inputs[3] = AuthoringHealthItem{Key: "data_source_fit", Status: "blocker", Count: blocked, Summary: "Data-source fit checks found missing columns, missing sources, or insufficient unique rows."}
		} else if needsReview > 0 {
			score -= minInt(15, needsReview*5)
			inputs[3] = AuthoringHealthItem{Key: "data_source_fit", Status: "warning", Count: needsReview, Summary: "Data-source fit checks need review before high-volume execution."}
		}
	}
	if aiGuardrails != nil {
		blocked := aiGuardrails.StatusCounts["blocked"]
		needsReview := aiGuardrails.StatusCounts["needs_review"]
		if blocked > 0 {
			score -= minInt(35, blocked*10)
			inputs[4] = AuthoringHealthItem{Key: "ai_guardrails", Status: "blocker", Count: blocked, Summary: "AI-generated drafts require provenance, diff review, validation, and replay evidence before promotion."}
		} else if needsReview > 0 {
			score -= minInt(10, needsReview*5)
			inputs[4] = AuthoringHealthItem{Key: "ai_guardrails", Status: "warning", Count: needsReview, Summary: "AI-generation evidence is present but should be reviewed before promotion."}
		}
	}
	if scriptHasProvenance {
		inputs[6] = AuthoringHealthItem{Key: "provenance", Status: "pass", Summary: "Source provenance is attached to the draft."}
	}
	if score < 0 {
		score = 0
	}
	status := "ready"
	if score < 60 || len(issues) > 0 || (dataSourceFit != nil && dataSourceFit.StatusCounts["blocked"] > 0) || (aiGuardrails != nil && aiGuardrails.StatusCounts["blocked"] > 0) {
		status = "blocked"
	} else if score < 85 || len(warnings) > 0 || parityPenalty > 0 || (dataSourceFit != nil && dataSourceFit.StatusCounts["needs_review"] > 0) || (aiGuardrails != nil && aiGuardrails.StatusCounts["needs_review"] > 0) {
		status = "needs_review"
	}
	return AuthoringHealth{Score: score, Status: status, Inputs: inputs}
}

func runtimeParityHealthPenalty(counts map[string]int) int {
	if len(counts) == 0 {
		return 0
	}
	penalty := 0
	penalty += counts[string(enterprisearchive.RuntimeSettingStatusBlocked)] * 20
	penalty += counts[string(enterprisearchive.RuntimeSettingStatusPartial)] * 5
	penalty += counts[string(enterprisearchive.RuntimeSettingStatusCompatibilityOnly)] * 8
	if penalty > 30 {
		return 30
	}
	return penalty
}

func authoringCapabilityResponse() AuthoringCapabilityResponse {
	sources := []AuthoringSourceCapability{
		{
			Source:      "enterprise_script_archive",
			DisplayName: "Enterprise Script Archive",
			Status:      "active",
			Acquisition: []string{"zip_upload", "validate_only"},
			Features: AuthoringFeatures{
				DraftValidation:       true,
				StoredValidation:      true,
				Replay:                true,
				BrowserReplay:         true,
				ImportReview:          true,
				RepairSuggestions:     true,
				SaveHandoff:           true,
				MultiScript:           false,
				ScenarioDraft:         false,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"validation_issues", "runtime_parity", "unsupported_constructs", "replay_readiness"},
			Notes:        []string{"Browser-profile archives use the Enterprise Script Archive acquisition surface with browser-specific review sections."},
		},
		{
			Source:      "trueclient_mobile_web",
			DisplayName: "TrueClient Mobile Web",
			Status:      "active",
			Acquisition: []string{"enterprise_archive_zip_upload", "validate_only"},
			Features: AuthoringFeatures{
				DraftValidation:       true,
				StoredValidation:      true,
				Replay:                true,
				BrowserReplay:         true,
				ImportReview:          true,
				RepairSuggestions:     true,
				SaveHandoff:           true,
				MultiScript:           false,
				ScenarioDraft:         false,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"validation_issues", "unsupported_steps", "browser_replay_readiness", "runtime_parity"},
		},
		{
			Source:      "trueclient2_web",
			DisplayName: "TruClient 2.0 - Web",
			Status:      "active",
			Acquisition: []string{"enterprise_archive_zip_upload", "validate_only", "starter_template"},
			Features: AuthoringFeatures{
				DraftValidation:       true,
				StoredValidation:      true,
				Replay:                true,
				BrowserReplay:         true,
				ImportReview:          true,
				RepairSuggestions:     true,
				SaveHandoff:           true,
				MultiScript:           false,
				ScenarioDraft:         false,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"web2_profile_summary", "object_identification", "unsupported_fragments", "browser_replay_readiness", "runtime_parity"},
			Notes:        []string{"Web 2.0 browser-profile archives produce executable browser drafts; replay evidence, JavaScript gates, runtime parity, and selector review govern promotion."},
		},
		{
			Source:      "web2_browser_profile",
			DisplayName: "Web 2.0 Browser Profile",
			Status:      "active",
			Acquisition: []string{"enterprise_archive_zip_upload", "validate_only", "starter_template", "browser_to_http_preview"},
			Features: AuthoringFeatures{
				DraftValidation:       true,
				StoredValidation:      true,
				Replay:                true,
				BrowserReplay:         true,
				ImportReview:          true,
				RepairSuggestions:     true,
				SaveHandoff:           true,
				MultiScript:           false,
				ScenarioDraft:         true,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"web2_profile_summary", "object_identification", "browser_replay_readiness", "runtime_parity", "http_conversion_readiness"},
			Notes:        []string{"Canonical APEX Load profile for executable TruClient 2.0 - Web imports and governed browser-to-HTTP conversion review."},
		},
		{
			Source:      "jmeter",
			DisplayName: "JMeter",
			Status:      "active",
			Acquisition: []string{"jmx_upload", "validate_only"},
			Features: AuthoringFeatures{
				DraftValidation:       true,
				StoredValidation:      true,
				Replay:                true,
				BrowserReplay:         false,
				ImportReview:          true,
				RepairSuggestions:     true,
				SaveHandoff:           true,
				MultiScript:           true,
				ScenarioDraft:         true,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"validation_issues", "unsupported_elements", "missing_data_sources", "scenario_draft_readiness"},
		},
		{
			Source:      "devweb",
			DisplayName: "DevWeb",
			Status:      "active",
			Acquisition: []string{"javascript_upload", "zip_upload", "json"},
			Features: AuthoringFeatures{
				DraftValidation:   true,
				StoredValidation:  true,
				Replay:            true,
				BrowserReplay:     false,
				ImportReview:      true,
				RepairSuggestions: true,
				SaveHandoff:       true,
				Provenance:        true,
			},
			HealthInputs: []string{"validation_issues", "unsupported_elements", "protocol_coverage"},
		},
		{
			Source:      "har",
			DisplayName: "HAR",
			Status:      "active",
			Acquisition: []string{"har_upload", "json"},
			Features: AuthoringFeatures{
				DraftValidation:   true,
				StoredValidation:  true,
				Replay:            true,
				BrowserReplay:     false,
				ImportReview:      true,
				RepairSuggestions: true,
				SaveHandoff:       true,
				Provenance:        true,
			},
			HealthInputs: []string{"validation_issues", "correlation_suggestions", "base_url_readiness"},
		},
		{
			Source:      "recorder",
			DisplayName: "Recorder",
			Status:      "active",
			Acquisition: []string{"recording_session", "preview_conversion"},
			Features: AuthoringFeatures{
				DraftValidation:   true,
				StoredValidation:  true,
				Replay:            true,
				BrowserReplay:     false,
				ImportReview:      true,
				RepairSuggestions: true,
				SaveHandoff:       true,
				Provenance:        true,
			},
			HealthInputs: []string{"validation_issues", "correlation_suggestions", "captured_request_count"},
		},
		{
			Source:      "sample",
			DisplayName: "Sample",
			Status:      "active",
			Acquisition: []string{"sample_template", "curl"},
			Features: AuthoringFeatures{
				DraftValidation:   true,
				StoredValidation:  true,
				Replay:            true,
				BrowserReplay:     false,
				ImportReview:      true,
				RepairSuggestions: true,
				SaveHandoff:       true,
				Provenance:        true,
			},
			HealthInputs: []string{"validation_issues", "template_completeness"},
		},
		{
			Source:      "manual",
			DisplayName: "Manual",
			Status:      "active",
			Acquisition: []string{"editor_draft"},
			Features: AuthoringFeatures{
				DraftValidation:  true,
				StoredValidation: true,
				Replay:           true,
				BrowserReplay:    true,
				SaveHandoff:      true,
				Provenance:       false,
			},
			HealthInputs: []string{"validation_issues", "replay_readiness", "missing_data_sources"},
		},
		{
			Source:      "ai_generated",
			DisplayName: "AI Generated",
			Status:      "planned",
			Acquisition: []string{"prompt", "jira_story", "openapi", "har"},
			Features: AuthoringFeatures{
				DraftValidation:       true,
				StoredValidation:      true,
				Replay:                true,
				BrowserReplay:         true,
				ImportReview:          true,
				RepairSuggestions:     true,
				SaveHandoff:           true,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"validation_issues", "provenance", "diff_review", "replay_readiness"},
			Notes:        []string{"Generation guardrails are not complete; planned sources must satisfy this contract before save."},
		},
	}
	sourceMap := make(map[string]AuthoringFeatures, len(sources))
	for _, source := range sources {
		sourceMap[source.Source] = source.Features
	}
	return AuthoringCapabilityResponse{
		ContractVersion: "authoring.v1",
		DraftLifecycle:  []string{"acquire", "review", "validate", "edit", "replay", "save", "reopen"},
		HealthInputs:    []string{"validation_issues", "runtime_parity", "unsupported_constructs", "missing_data_sources", "replay_readiness", "provenance"},
		Sources:         sources,
		SourceMap:       sourceMap,
	}
}
