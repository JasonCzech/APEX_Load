package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/clientids"
	"github.com/jasonczech/apexload/pkg/convert/clienttimestamps"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func ApplyClientGeneratedUUIDs(script *dsl.Script, harData []byte, result *har.ConvertResult) {
	if script == nil || result == nil {
		return
	}
	var server map[string]bool
	var judged bool
	var err error
	if result.Document.Matches(harData) {
		server, judged, err = result.Document.ServerUUIDs()
		if err != nil {
			server, judged, err = clientids.ServerUUIDs(harData)
		}
	} else {
		server, judged, err = clientids.ServerUUIDs(harData)
	}
	if err != nil {
		return
	}
	var note string
	if judged {
		note = clientids.Apply(script, server).Warning()
	} else if clientids.HasRequestUUIDs(script) {
		note = "Client-generated UUIDs were not detected: too few recorded responses kept their bodies to tell which UUIDs the server sent, so request UUIDs keep their recorded values."
	}
	if note == "" {
		return
	}
	result.Warnings = append(result.Warnings, note)
	result.Unsupported = append(result.Unsupported, diag.Diagnostic{Code: "client_generated_uuids", Element: "Client-generated UUIDs", Location: "HAR requests", Detail: note, Severity: diag.Warning})
}

func ApplyClientTimestamps(script *dsl.Script, harData []byte, result *har.ConvertResult) {
	if script == nil || result == nil {
		return
	}
	var evidence clienttimestamps.Evidence
	var err error
	if result.Document.Matches(harData) {
		evidence, err = result.Document.ClientTimestamps()
		if err != nil {
			evidence, err = clienttimestamps.FromHAR(harData)
		}
	} else {
		evidence, err = clienttimestamps.FromHAR(harData)
	}
	if err != nil {
		return
	}
	note := clienttimestamps.Apply(script, evidence).Warning()
	if note == "" {
		return
	}
	result.Warnings = append(result.Warnings, note)
	result.Unsupported = append(result.Unsupported, diag.Diagnostic{Code: sample.ClientTimestampsDiagnosticCode, Element: "Client-generated timestamps", Location: "HAR requests", Detail: note, Severity: diag.Warning})
}

type TrueClient2CompatReport struct {
	SourceScriptType        string                                  `json:"source_script_type,omitempty"`
	SourceProfile           string                                  `json:"source_profile,omitempty"`
	ImportMode              string                                  `json:"import_mode,omitempty"`
	PromotionStatus         string                                  `json:"promotion_status"`
	PromotionAllowed        bool                                    `json:"promotion_allowed"`
	ImportedSteps           int                                     `json:"imported_steps"`
	ConvertedActions        int                                     `json:"converted_actions"`
	SkippedReviewSteps      int                                     `json:"skipped_review_steps"`
	SupportedStepFamilies   int                                     `json:"supported_step_families"`
	ReviewOnlyStepFamilies  int                                     `json:"review_only_step_families"`
	UnsupportedStepFamilies int                                     `json:"unsupported_step_families"`
	SelectorConfidencePct   int                                     `json:"selector_confidence_pct"`
	SelectorCandidateCount  int                                     `json:"selector_candidate_count"`
	RuntimeParityStatus     string                                  `json:"runtime_parity_status"`
	ReplayStatus            string                                  `json:"replay_status"`
	JavaScriptReviewGate    string                                  `json:"javascript_review_gate"`
	JavaScriptReview        *TrueClientJavaScriptReviewPolicyReport `json:"javascript_review,omitempty"`
	FrameWindowBlockers     int                                     `json:"frame_window_blockers"`
	HTTPConversionReadiness string                                  `json:"http_conversion_readiness"`
	FixtureContractStatus   map[string]string                       `json:"fixture_contract_status,omitempty"`
	Findings                []string                                `json:"findings,omitempty"`
	NextActions             []string                                `json:"next_actions,omitempty"`
}

type TrueClientJavaScriptReviewPolicyReport struct {
	Status                 string                             `json:"status"`
	Policy                 string                             `json:"policy"`
	ApprovalRequired       bool                               `json:"approval_required"`
	ReplayEvidenceRequired bool                               `json:"replay_evidence_required"`
	RequiredActions        int                                `json:"required_actions"`
	ApprovedActions        int                                `json:"approved_actions"`
	PendingActions         int                                `json:"pending_actions"`
	RejectedActions        int                                `json:"rejected_actions"`
	SourceOnlyFragments    int                                `json:"source_only_fragments,omitempty"`
	ActionReviews          []TrueClientJavaScriptActionReview `json:"action_reviews,omitempty"`
	Findings               []string                           `json:"findings,omitempty"`
	NextActions            []string                           `json:"next_actions,omitempty"`
}

type TrueClientJavaScriptActionReview struct {
	Index           int    `json:"index"`
	Name            string `json:"name,omitempty"`
	Type            string `json:"type,omitempty"`
	Status          string `json:"status"`
	Gate            string `json:"gate,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Reviewer        string `json:"reviewer,omitempty"`
	ApprovedAt      string `json:"approved_at,omitempty"`
	Source          string `json:"source,omitempty"`
	SourceStepID    string `json:"source_step_id,omitempty"`
	SourceStepIndex string `json:"source_step_index,omitempty"`
	ScriptPreview   string `json:"script_preview,omitempty"`
}

type ProtocolCompatReport struct {
	SourceFormat         string         `json:"source_format"`
	CanonicalProtocol    string         `json:"canonical_protocol"`
	ImportMode           string         `json:"import_mode"`
	MappedSteps          int            `json:"mapped_steps"`
	ReviewGatedSteps     int            `json:"review_gated_steps"`
	UnsupportedFragments []string       `json:"unsupported_fragments,omitempty"`
	RuntimeParity        map[string]int `json:"runtime_parity,omitempty"`
	DataSources          int            `json:"data_sources"`
	CorrelationCoverage  string         `json:"correlation_coverage"`
	PromotionStatus      string         `json:"promotion_status"`
	NextActions          []string       `json:"next_actions,omitempty"`
}
