package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"time"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

type AuthoringCapabilityResponse struct {
	ContractVersion string                       `json:"contract_version"`
	DraftLifecycle  []string                     `json:"draft_lifecycle"`
	HealthInputs    []string                     `json:"health_inputs"`
	Sources         []AuthoringSourceCapability  `json:"sources"`
	SourceMap       map[string]AuthoringFeatures `json:"source_map"`
	ImportSurfaces  []AuthoringImportSurface     `json:"import_surfaces"`
	FeaturePolicy   *domainFeaturePolicyResponse `json:"feature_policy,omitempty"`
	HTTPRuntime     AuthoringHTTPRuntime         `json:"http_runtime"`
}

// AuthoringHTTPRuntime says which HTTP versions this controller can execute.
// Editors use it for capability messages that never block saving; a run's
// generators are checked again when it is admitted.
type AuthoringHTTPRuntime struct {
	// Versions are the explicit versions; empty (automatic) always runs.
	Versions []string                 `json:"versions"`
	HTTP3    AuthoringHTTP3Capability `json:"http3"`
}

type AuthoringHTTP3Capability struct {
	Exact   bool   `json:"exact"`   // policy 3
	Upgrade bool   `json:"upgrade"` // policy auto+h3
	Reason  string `json:"reason,omitempty"`
	// Scope is "controller": generators advertise their own capability.
	Scope string `json:"scope"`
}

func authoringHTTPRuntime() AuthoringHTTPRuntime {
	runtime := AuthoringHTTPRuntime{Versions: httpprotocol.SupportedVersions(), HTTP3: AuthoringHTTP3Capability{Scope: "controller"}}
	exactErr, upgradeErr := httpprotocol.HTTP3ExecutionError(), httpprotocol.HTTP3UpgradeExecutionError()
	runtime.HTTP3.Exact, runtime.HTTP3.Upgrade = exactErr == nil, upgradeErr == nil
	switch {
	case exactErr != nil:
		runtime.HTTP3.Reason = exactErr.Error()
	case upgradeErr != nil:
		runtime.HTTP3.Reason = upgradeErr.Error()
	}
	return runtime
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

type AuthoringImportSurface struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Badge       string                `json:"badge"`
	Category    string                `json:"category"`
	Order       int                   `json:"order"`
	Launcher    string                `json:"launcher"`
	Visible     bool                  `json:"visible"`
	Modes       []AuthoringImportMode `json:"modes,omitempty"`
	// Protocols names the protocols a collection format carries when its
	// title does not, so a search for "soap" finds Bruno and Postman.
	Protocols []string `json:"protocols,omitempty"`
}

type AuthoringImportMode struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
}

type AuthoringHealth = importreview.AuthoringHealth

type AuthoringHealthItem = importreview.AuthoringHealthItem

var buildStoredScriptAuthoringHealth = importreview.BuildStoredScriptAuthoringHealth

var buildAuthoringHealth = importreview.BuildAuthoringHealth

var runtimeParityHealthPenalty = importreview.RuntimeParityHealthPenalty

var runtimeParityIssueCount = importreview.RuntimeParityIssueCount

// collectionImportProtocols lists what a request collection's import turns
// into runnable scripts. GraphQL is left out: those requests are held for
// translation review. A new slice each call, so no caller can change another's.
func collectionImportProtocols() []string {
	return []string{"HTTP", "REST", "SOAP"}
}

func authoringImportSurfaces() []AuthoringImportSurface {
	return []AuthoringImportSurface{
		{
			ID:          "openapi",
			Title:       "OpenAPI / Swagger",
			Description: "Generate grouped REST request flows with detected auth schemes, examples, variables, and candidate data pools.",
			Badge:       "OAS",
			Category:    "Contracts & collections",
			Order:       10,
			Launcher:    "openapi",
			Visible:     true,
		},
		{
			ID:          "bruno",
			Title:       "Bruno Collection",
			Description: "Convert supported OpenCollection YAML HTTP requests into native APEX scripts with retained source and credential review.",
			Badge:       "BRU",
			Category:    "Contracts & collections",
			Order:       15,
			Launcher:    "bruno",
			Visible:     true,
			Protocols:   collectionImportProtocols(),
		},
		{
			ID:          "postman",
			Title:       "Postman Collection",
			Description: "Convert collection folders, requests, variables, and auth placeholders into a reviewable HTTP script.",
			Badge:       "PM",
			Category:    "Contracts & collections",
			Order:       20,
			Launcher:    "postman",
			Visible:     true,
			Protocols:   collectionImportProtocols(),
		},
		{
			ID:          "wsdl",
			Title:       "WSDL / SOAP",
			Description: "Generate SOAP operation actions with SOAPAction, namespaces, sample envelopes, and fault-review guidance.",
			Badge:       "WSDL",
			Category:    "Contracts & collections",
			Order:       30,
			Launcher:    "wsdl",
			Visible:     true,
		},
		{
			ID:          "asyncapi",
			Title:       "AsyncAPI / Kafka",
			Description: "Convert AsyncAPI 2.x/3.x channels into Kafka produce, consume, and request-reply actions with brokers, topics, and sample payloads.",
			Badge:       "ASYNC",
			Category:    "Contracts & collections",
			Order:       40,
			Launcher:    "asyncapi",
			Visible:     true,
		},
		{
			ID:          "raml",
			Title:       "RAML / Anypoint",
			Description: "Convert RAML resources, methods, examples, and OAuth2 security schemes into reviewable HTTP actions.",
			Badge:       "RAML",
			Category:    "Contracts & collections",
			Order:       50,
			Launcher:    "raml",
			Visible:     true,
		},
		{
			ID:          "traffic_har",
			Title:       "HAR Traffic",
			Description: "Convert captured browser/API traffic into HTTP actions; auto-detects Salesforce, Lightning, and Agentforce and generates a native, correlation-rich draft.",
			Badge:       "HAR",
			Category:    "Traffic & samples",
			Order:       100,
			Launcher:    "traffic_har",
			Visible:     true,
			Modes: []AuthoringImportMode{
				{ID: "auto_detect", Label: "Auto-detect", Description: "Detect Salesforce traffic and emit the native Salesforce draft when found.", Default: true},
				{ID: "force_generic", Label: "Force Generic HAR", Description: "Return the raw HAR-derived HTTP script while still attaching detected Salesforce review.", Default: false},
			},
		},
		{
			ID:          "recorder_export",
			Title:       "Recorder Export / Playwright Codegen",
			Description: "Convert a Chrome DevTools Recorder export or Playwright codegen script into a browser script with ranked selector candidates.",
			Badge:       "REC",
			Category:    "Traffic & samples",
			Order:       110,
			Launcher:    "recorder_export",
			Visible:     true,
		},
		{
			ID:          "sample",
			Title:       "Request / Response Sample",
			Description: "Build an API draft from raw HTTP, cURL, HAR, or payload examples when formal contracts are unavailable.",
			Badge:       "API",
			Category:    "Traffic & samples",
			Order:       120,
			Launcher:    "sample",
			Visible:     true,
			Modes: []AuthoringImportMode{
				{ID: "standard_api", Label: "Standard API", Description: "Infer HTTP actions from raw HTTP, cURL, HAR, or structured request/response samples.", Default: true},
				{ID: "java_http", Label: "Java over HTTP", Description: "Preserve opaque base64-encoded Java serialized payloads and promote the draft to java_http.", Default: false},
			},
		},
		{
			ID:          "enterprise_archive",
			Title:       "LoadRunner Script Archive",
			Description: "Import VuGen ZIP or USZ projects, including a package of several projects, and review each script, its settings, and migration requirements.",
			Badge:       "ESA",
			Category:    "Legacy & tool exports",
			Order:       200,
			Launcher:    "enterprise_archive",
			Visible:     true,
		},
		{
			ID:          "jmeter",
			Title:       "JMeter Test Plan",
			Description: "Convert a JMeter .jmx plan into HTTP scripts with warnings, unsupported elements, and scenario draft context.",
			Badge:       "JMX",
			Category:    "Legacy & tool exports",
			Order:       210,
			Launcher:    "jmeter",
			Visible:     true,
		},
		{
			ID:          "devweb",
			Title:       "DevWeb Source",
			Description: "Convert DevWeb JavaScript assets into HTTP, WebSocket, or gRPC script drafts for review.",
			Badge:       "DEV",
			Category:    "Legacy & tool exports",
			Order:       220,
			Launcher:    "devweb",
			Visible:     true,
		},
		{
			ID:          "k6",
			Title:       "k6 Script",
			Description: "Convert supported k6 HTTP flows into APEX Load actions and preserve unsupported JavaScript for review.",
			Badge:       "k6",
			Category:    "Legacy & tool exports",
			Order:       230,
			Launcher:    "k6",
			Visible:     true,
		},
		{
			ID:          "cloudtest",
			Title:       "Akamai CloudTest Export",
			Description: "Inspect dependency-complete CloudTest XML/ZIP exports into fixture-gated Clip scripts, Composition scenarios, and preserved review artifacts.",
			Badge:       "CT",
			Category:    "Legacy & tool exports",
			Order:       240,
			Launcher:    "cloudtest",
			Visible:     true,
		},
		{
			ID:          "eventhubs_capture",
			Title:       "Event Hubs Capture",
			Description: "Seed native Event Hubs producer actions from Azure Event Hubs Capture files or any Avro data file.",
			Badge:       "EH",
			Category:    "Messaging & cloud artifacts",
			Order:       300,
			Launcher:    "eventhubs_capture",
			Visible:     true,
		},
		{
			ID:          "broker_config",
			Title:       "Broker Config",
			Description: "Import IBM MQ MQSC or JMS JNDI broker configuration into a JMS topology draft.",
			Badge:       "JMS",
			Category:    "Messaging & cloud artifacts",
			Order:       310,
			Launcher:    "broker_config",
			Visible:     true,
		},
		{
			ID:          "oracle_ebusiness_review",
			Title:       "Oracle E-Business / Siebel Review",
			Description: "Review Oracle E-Business, Oracle NCA, and Siebel assets for package signals, correlation packs, and execution posture.",
			Badge:       "EBS",
			Category:    "Reviews",
			Order:       400,
			Launcher:    "oracle_ebusiness_review",
			Visible:     true,
		},
	}
}

func authoringCapabilityResponse(projectIDs ...string) AuthoringCapabilityResponse {
	projectID := ""
	if len(projectIDs) > 0 {
		projectID = normalizeProjectID(projectIDs[0])
	}
	aiPolicy := buildAIPolicyResponse(projectID, time.Now().UTC())
	aiStatus := "disabled"
	aiNotes := []string{"AI authoring is disabled by the current global, project, or provider policy."}
	if aiPolicy.Enabled && stringSliceContainsFold(aiPolicy.AllowedProviders, defaultAIProvider) {
		aiStatus = "active"
		aiNotes = []string{"Deterministic-local authoring is available; external providers remain policy-gated."}
	}
	sources := []AuthoringSourceCapability{
		{
			Source:      "enterprise_script_archive",
			DisplayName: "LoadRunner Script Archive",
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
				MultiScript:           true,
				ScenarioDraft:         false,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"validation_issues", "runtime_parity", "unsupported_constructs", "replay_readiness"},
			Notes:        []string{"ZIP/USZ projects and transport collections retain independent script review results.", "Browser-profile archives use the Enterprise Script Archive acquisition surface with browser-specific review sections."},
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
			Source:      "cloudtest",
			DisplayName: "Akamai CloudTest",
			Status:      "preview",
			Acquisition: []string{"xml_upload", "zip_upload"},
			Features: AuthoringFeatures{
				DraftValidation:       true,
				StoredValidation:      true,
				Replay:                false,
				BrowserReplay:         false,
				ImportReview:          true,
				RepairSuggestions:     false,
				SaveHandoff:           true,
				MultiScript:           true,
				ScenarioDraft:         true,
				Provenance:            true,
				ApprovalGateCandidate: true,
			},
			HealthInputs: []string{"fixture_gate", "unclassified_elements", "unsupported_elements", "scenario_draft_readiness"},
			Notes:        []string{"All converted actions remain execution-blocked until sanitized dependency-complete golden fixtures prove the source wire shape."},
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
			Status:      aiStatus,
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
			Notes:        aiNotes,
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
		ImportSurfaces:  authoringImportSurfaces(),
		HTTPRuntime:     authoringHTTPRuntime(),
	}
}
