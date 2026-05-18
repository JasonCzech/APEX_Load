package api

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

func authoringCapabilityResponse() AuthoringCapabilityResponse {
	sources := []AuthoringSourceCapability{
		{
			Source:      "loadrunner",
			DisplayName: "LoadRunner",
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
			Notes:        []string{"TrueClient Mobile Web archives use the LoadRunner acquisition surface with browser-specific review sections."},
		},
		{
			Source:      "trueclient_mobile_web",
			DisplayName: "TrueClient Mobile Web",
			Status:      "active",
			Acquisition: []string{"loadrunner_zip_upload", "validate_only"},
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
