package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/loadrunner"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const runtimeSettingsSupportSchemaVersion = "apexload.runtime_settings_support.v1"
const runtimeSettingsReportSchemaVersion = "apexload.runtime_settings_report.v1"

type RuntimeSettingsSupportResponse struct {
	SchemaVersion    string                         `json:"schema_version"`
	GeneratedAt      time.Time                      `json:"generated_at"`
	Protocols        []string                       `json:"protocols"`
	StatusVocabulary []RuntimeSettingsStatusVocab   `json:"status_vocabulary"`
	StatusCounts     map[string]int                 `json:"status_counts"`
	SupportMatrix    []RuntimeSettingsSupportEntry  `json:"support_matrix"`
	Presets          []RuntimeSettingsProfilePreset `json:"presets"`
	ReportExports    []RuntimeSettingsReportExport  `json:"report_exports"`
	Decisions        []RuntimeSettingsDecision      `json:"decisions"`
	Notes            []string                       `json:"notes,omitempty"`
}

type RuntimeSettingsStatusVocab struct {
	Status  string `json:"status"`
	Meaning string `json:"meaning"`
}

type RuntimeSettingsSupportEntry struct {
	ID                  string   `json:"id"`
	Protocols           []string `json:"protocols"`
	Source              string   `json:"source,omitempty"`
	Area                string   `json:"area"`
	Concept             string   `json:"concept"`
	Fields              []string `json:"fields,omitempty"`
	Status              string   `json:"status"`
	ExecutionScope      string   `json:"execution_scope,omitempty"`
	ImportSources       []string `json:"import_sources,omitempty"`
	Evidence            []string `json:"evidence,omitempty"`
	RecommendedWorkflow string   `json:"recommended_workflow,omitempty"`
	Notes               string   `json:"notes,omitempty"`
}

type RuntimeSettingsProfilePreset struct {
	ID                   string                   `json:"id"`
	Name                 string                   `json:"name"`
	Summary              string                   `json:"summary"`
	RecommendedProtocols []string                 `json:"recommended_protocols"`
	RuntimeSettings      *RuntimeSettingsOverride `json:"runtime_settings,omitempty"`
	ScriptConfigHints    map[string]any           `json:"script_config_hints,omitempty"`
	ScenarioHints        map[string]any           `json:"scenario_hints,omitempty"`
	Evidence             []string                 `json:"evidence,omitempty"`
}

type RuntimeSettingsReportExport struct {
	Scope     string   `json:"scope"`
	Endpoint  string   `json:"endpoint"`
	Formats   []string `json:"formats"`
	Evidence  []string `json:"evidence,omitempty"`
	UseCases  []string `json:"use_cases,omitempty"`
	Available bool     `json:"available"`
}

type RuntimeSettingsDecision struct {
	ID        string   `json:"id"`
	Decision  string   `json:"decision"`
	Status    string   `json:"status"`
	Rationale string   `json:"rationale"`
	Evidence  []string `json:"evidence,omitempty"`
}

func (s *Server) handleRuntimeSettingsSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildRuntimeSettingsSupportResponse(time.Now().UTC()))
}

func buildRuntimeSettingsSupportResponse(now time.Time) RuntimeSettingsSupportResponse {
	matrix := runtimeSettingsSupportMatrix()
	return RuntimeSettingsSupportResponse{
		SchemaVersion:    runtimeSettingsSupportSchemaVersion,
		GeneratedAt:      now,
		Protocols:        runtimeSettingsSupportedProtocols(matrix),
		StatusVocabulary: runtimeSettingsStatusVocabulary(),
		StatusCounts:     runtimeSettingsStatusCounts(matrix),
		SupportMatrix:    matrix,
		Presets:          runtimeSettingsProfilePresets(),
		ReportExports: []RuntimeSettingsReportExport{
			{
				Scope:     "script",
				Endpoint:  "/api/v1/scripts/{id}/runtime-settings/report?format=json|csv",
				Formats:   []string{"json", "csv"},
				Evidence:  []string{"pkg/api/runtime_settings_support_test.go::TestRuntimeSettingsReportEndpointsExportScriptAndScenarioEvidence"},
				UseCases:  []string{"migration review", "authoring approval", "support handoff"},
				Available: true,
			},
			{
				Scope:     "scenario",
				Endpoint:  "/api/v1/scenarios/{id}/runtime-settings/report?format=json|csv",
				Formats:   []string{"json", "csv"},
				Evidence:  []string{"pkg/api/runtime_settings_support_test.go::TestRuntimeSettingsReportEndpointsExportScriptAndScenarioEvidence"},
				UseCases:  []string{"pre-run approval", "release gate evidence", "operator review"},
				Available: true,
			},
		},
		Decisions: []RuntimeSettingsDecision{
			{
				ID:        "streaming-media-runtime",
				Decision:  "executed",
				Status:    "decided",
				Rationale: "APEX Load now maps LoadRunner-style streaming settings into HTTP runtime pacing for media-like responses, including startup buffer delay, fixed bitrate shaping, and adaptive bitrate estimation.",
				Evidence:  []string{"docs/LOADRUNNER_RTS_REFERENCE.md", "docs/RUNTIME_SETTINGS_PARITY.md", "/api/v1/runtime-settings/support-matrix"},
			},
		},
		Notes: []string{
			"The unified matrix extends the LoadRunner Web HTTP/HTML RTS matrix with native browser, SOAP, Java-over-HTTP, WebSocket, gRPC, JMS, DevWeb, HAR, and JMeter-derived coverage.",
			"Scenario validation and queued-run creation use the same runtime review contract so compatibility-only and blocking settings are visible before execution.",
			"Browser VUsers remain diagnostic canaries; real-browser density claims are governed by the browser benchmark evidence endpoint.",
		},
	}
}

func runtimeSettingsSupportMatrix() []RuntimeSettingsSupportEntry {
	matrix := make([]RuntimeSettingsSupportEntry, 0, 64)
	for _, entry := range loadrunner.GetRTSSupportMatrix() {
		matrix = append(matrix, RuntimeSettingsSupportEntry{
			ID:                  "loadrunner_web_" + entry.ID,
			Protocols:           []string{"http", "browser", "loadrunner_web_http_html"},
			Source:              "loadrunner_web_http_html",
			Area:                entry.Area,
			Concept:             entry.Concept,
			Fields:              cloneStringSlice(entry.Fields),
			Status:              string(entry.Status),
			ExecutionScope:      "imported Web HTTP/HTML and TruClient Mobile Web runtime settings mapped into APEX Load DSL/runtime fields",
			ImportSources:       []string{"LoadRunner Web HTTP/HTML", "TruClient Mobile Web"},
			Evidence:            []string{"pkg/convert/loadrunner/rts_support.go", "docs/LOADRUNNER_RTS_REFERENCE.md"},
			RecommendedWorkflow: "Import, validate runtime parity, replay, then approve or export the RTS report.",
			Notes:               entry.Notes,
		})
	}
	matrix = append(matrix, vugenWeb2RuntimeSupportEntries()...)
	matrix = append(matrix, nativeRuntimeSupportEntries()...)
	sort.SliceStable(matrix, func(i, j int) bool {
		if strings.Join(matrix[i].Protocols, ",") == strings.Join(matrix[j].Protocols, ",") {
			return matrix[i].ID < matrix[j].ID
		}
		return strings.Join(matrix[i].Protocols, ",") < strings.Join(matrix[j].Protocols, ",")
	})
	return matrix
}

func vugenWeb2RuntimeSupportEntries() []RuntimeSettingsSupportEntry {
	loadRunnerEntries := loadrunner.GetRTSSupportMatrix()
	entries := make([]RuntimeSettingsSupportEntry, 0, len(loadRunnerEntries)+2)
	for _, entry := range loadRunnerEntries {
		entries = append(entries, RuntimeSettingsSupportEntry{
			ID:                  "vugen_web2_" + entry.ID,
			Protocols:           []string{"browser", "vugen_web2", "trueclient2_web"},
			Source:              "vugen_web2",
			Area:                entry.Area,
			Concept:             entry.Concept,
			Fields:              cloneStringSlice(entry.Fields),
			Status:              string(entry.Status),
			ExecutionScope:      "TruClient 2.0 - Web browser-profile runtime settings mapped into APEX Load browser, web runtime, logging, pacing, think-time, proxy, and capture config where executable.",
			ImportSources:       []string{"TruClient 2.0 - Web", "VuGen Web 2.0"},
			Evidence:            []string{"pkg/convert/loadrunner/converter_test.go::TestConvertZipProducesReviewDraftForTruClientWeb2Archive", "docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md"},
			RecommendedWorkflow: "Import through LoadRunner ZIP acquisition, inspect the Web 2.0 review sections, run browser replay, then use the runtime report before promotion.",
			Notes:               entry.Notes,
		})
	}
	entries = append(entries,
		RuntimeSettingsSupportEntry{
			ID:                  "vugen_web2_browser_replay_evidence",
			Protocols:           []string{"browser", "vugen_web2", "trueclient2_web"},
			Source:              "vugen_web2",
			Area:                "Browser Replay Evidence",
			Concept:             "Screenshots, DOM summary, console entries, network entries, Core Web Vitals, and object-match diagnostics for Web 2.0 migration approval",
			Fields:              []string{"browser_replay.events", "browser_replay.report", "actions[].metadata.source_*"},
			Status:              "executed",
			ExecutionScope:      "Replay/session evidence exported from the browser replay manager and used by authoring review flows.",
			ImportSources:       []string{"TruClient 2.0 - Web", "VuGen Web 2.0"},
			Evidence:            []string{"pkg/api/handlers_browser_replay_test.go", "dashboard/src/components/ScriptEditor.test.tsx"},
			RecommendedWorkflow: "Capture browser replay evidence before approving or converting the Web 2.0 draft.",
		},
		RuntimeSettingsSupportEntry{
			ID:                  "vugen_web2_http_conversion_readiness",
			Protocols:           []string{"browser", "http", "vugen_web2", "trueclient2_web"},
			Source:              "vugen_web2",
			Area:                "Browser-To-HTTP Conversion",
			Concept:             "Network capture, request eligibility, correlation/data-source repair, and canary-plus-protocol-load scenario guidance",
			Fields:              []string{"browser_replay.events[].network_entries", "generated_http_script", "scenario_template"},
			Status:              "executed",
			ExecutionScope:      "Conversion is a governed review workflow; generated HTTP drafts still require validation, replay, and approval.",
			ImportSources:       []string{"TruClient 2.0 - Web replay evidence"},
			Evidence:            []string{"pkg/api/browser_to_http_conversion_test.go", "docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md"},
			RecommendedWorkflow: "Keep the browser script as a canary, generate an HTTP draft from captured replay traffic, repair correlation/data issues, then assemble a paired scenario.",
		},
	)
	return entries
}

func nativeRuntimeSupportEntries() []RuntimeSettingsSupportEntry {
	return []RuntimeSettingsSupportEntry{
		{
			ID:                  "browser_canary_runtime",
			Protocols:           []string{"browser"},
			Source:              "native",
			Area:                "Browser Runtime",
			Concept:             "Headless/headed execution, device profiles, touch, orientation, viewport, client metrics, screenshots, cache, and network/resource controls",
			Fields:              []string{"config.browser", "config.web_runtime.network_simulation", "config.web_runtime.download_filters"},
			Status:              "executed",
			ExecutionScope:      "Browser replay and low-density canary execution",
			ImportSources:       []string{"JSON DSL", "recorder", "TruClient Mobile Web", "sample templates"},
			Evidence:            []string{"pkg/protocol/browser/browser_test.go", "pkg/api/browser_benchmark_evidence.go"},
			RecommendedWorkflow: "Use browser canary presets beside protocol load and export replay evidence for UX review.",
		},
		{
			ID:                  "browser_recorded_resources_metadata",
			Protocols:           []string{"browser"},
			Source:              "native",
			Area:                "Browser Runtime",
			Concept:             "Recorded-only resource mode and missing recorded-resource metadata guard",
			Fields:              []string{"config.browser.download_resources", "metadata.import.preserved.recorded_resource_urls"},
			Status:              "executed",
			ExecutionScope:      "Runtime blocks recorded-only mode when metadata is unavailable to avoid false parity claims.",
			ImportSources:       []string{"LoadRunner Web HTTP/HTML", "TruClient Mobile Web", "recorder"},
			Evidence:            []string{"pkg/protocol/browser/browser_test.go::TestBrowserProtocolDownloadResourcesRecordedRequiresMetadata", "pkg/api/runtime_settings_support_test.go::TestScenarioRuntimeSettingsValidationSurfacesCompatibilityAndBlockers"},
			RecommendedWorkflow: "Use all/none for manually authored browser scripts unless recorded-resource URLs are preserved by import or recording.",
		},
		{
			ID:                  "soap_fault_snapshot",
			Protocols:           []string{"soap"},
			Source:              "native",
			Area:                "SOAP Runtime",
			Concept:             "SOAP 1.1/1.2 fault handling, WS-Security surface, request/response snapshots, and W3C trace propagation",
			Fields:              []string{"config.soap", "config.error_handling.snapshot_on_error", "config.observability"},
			Status:              "executed",
			ExecutionScope:      "SOAP protocol execution and WSDL-generated review scripts",
			ImportSources:       []string{"JSON DSL", "WSDL import"},
			Evidence:            []string{"pkg/protocol/soap/soap_test.go::TestSOAPExecuteFailsOnFault", "pkg/protocol/soap/soap_test.go::TestSOAPExecuteFailsOnSOAP12Fault"},
			RecommendedWorkflow: "Import WSDL, validate SOAP envelope/fault settings, replay, and keep snapshots enabled for release gates.",
		},
		{
			ID:                  "java_http_codec_snapshot",
			Protocols:           []string{"java_http"},
			Source:              "native",
			Area:                "Java-over-HTTP Runtime",
			Concept:             "Codec/helper mode, content negotiation, request/response mapping, binary result preservation, and decode-failure snapshots",
			Fields:              []string{"config.java_http", "config.error_handling.snapshot_on_error"},
			Status:              "executed",
			ExecutionScope:      "Java-over-HTTP protocol execution",
			ImportSources:       []string{"JSON DSL", "sample templates"},
			Evidence:            []string{"pkg/protocol/java_http/java_http_test.go::TestJavaHTTPExecuteRejectsInvalidBase64"},
			RecommendedWorkflow: "Attach codec mappings and keep debug snapshots for codec onboarding.",
		},
		{
			ID:                  "websocket_runtime_controls",
			Protocols:           []string{"websocket"},
			Source:              "native",
			Area:                "WebSocket Runtime",
			Concept:             "Connect, send, receive, close, ping, timeouts, max message size, subprotocol, origin, compression, reconnect, and error snapshots",
			Fields:              []string{"config.websocket", "config.error_handling.snapshot_on_error"},
			Status:              "executed",
			ExecutionScope:      "WebSocket protocol execution and DevWeb-derived WebSocket scripts",
			ImportSources:       []string{"JSON DSL", "DevWeb import"},
			Evidence:            []string{"pkg/protocol/websocket/websocket_test.go::TestWebSocketReceiveMatchFailure"},
			RecommendedWorkflow: "Replay subscription flows with receive assertions and inspect close/error frames before promotion.",
		},
		{
			ID:                  "grpc_status_metadata",
			Protocols:           []string{"grpc"},
			Source:              "native",
			Area:                "gRPC Runtime",
			Concept:             "Reflection/proto files, metadata, authority, plaintext/TLS, max receive size, timeout, streaming modes, status code reporting, and snapshots",
			Fields:              []string{"config.grpc", "config.error_handling.snapshot_on_error"},
			Status:              "executed",
			ExecutionScope:      "gRPC unary and streaming protocol execution",
			ImportSources:       []string{"JSON DSL", "DevWeb import"},
			Evidence:            []string{"pkg/protocol/grpc/grpc_test.go::TestGRPCFailurePopulatesSnapshot"},
			RecommendedWorkflow: "Attach proto/reflection evidence, validate method names, replay, then export status evidence.",
		},
		{
			ID:                  "jms_broker_reliability",
			Protocols:           []string{"jms-solace", "jms"},
			Source:              "native",
			Area:                "JMS Runtime",
			Concept:             "Broker/destination defaults, request/reply, acknowledgement, delivery mode, timeout, properties, broker errors, and dead-letter diagnostics",
			Fields:              []string{"config.jms", "config.error_handling.snapshot_on_error"},
			Status:              "executed",
			ExecutionScope:      "JMS Solace execution with local mock mode and broker-backed runs",
			ImportSources:       []string{"JSON DSL", "sample templates"},
			Evidence:            []string{"pkg/protocol/jms/jms_test.go::TestJMSExecuteWithoutBrokerReturnsActionError", "pkg/protocol/jms/jms_test.go::TestJMSDeadLetterDiagnostics"},
			RecommendedWorkflow: "Validate with mock mode locally, then attach broker credentials and export broker-error diagnostics for controlled runs.",
		},
		{
			ID:                  "devweb_runtime_mapping",
			Protocols:           []string{"devweb", "http", "websocket", "grpc"},
			Source:              "import",
			Area:                "DevWeb Import",
			Concept:             "Runtime mapping for WebRequest, WebSocket, unary gRPC, think time, transactions, and dynamic JavaScript review",
			Fields:              []string{"script.actions", "config.think_time", "metadata.import.preserved.devweb_constructs"},
			Status:              "partial",
			ExecutionScope:      "Supported constructs convert to executable DSL; dynamic JavaScript remains review-only.",
			ImportSources:       []string{"DevWeb JavaScript", "DevWeb zip"},
			Evidence:            []string{"pkg/convert/devweb/support_test.go", "pkg/api/handlers_scripts_test.go::TestDevWebSupportMatrixEndpoint"},
			RecommendedWorkflow: "Import DevWeb, resolve unsupported construct findings, validate, replay, then approve.",
		},
		{
			ID:                  "har_http_runtime_mapping",
			Protocols:           []string{"har", "http"},
			Source:              "import",
			Area:                "HAR Import",
			Concept:             "HAR request ordering, headers, cookies, query/body preservation, and HTTP runtime defaults",
			Fields:              []string{"script.actions", "config.http", "config.data_sources"},
			Status:              "executed",
			ExecutionScope:      "HAR imports produce executable HTTP DSL after auth/data review.",
			ImportSources:       []string{"HAR 1.2"},
			Evidence:            []string{"pkg/convert/har", "pkg/api/handlers_scripts_test.go"},
			RecommendedWorkflow: "Import HAR, parameterize secrets/data, validate, replay, then export RTS evidence.",
		},
		{
			ID:                  "jmeter_runtime_mapping",
			Protocols:           []string{"jmeter", "http"},
			Source:              "import",
			Area:                "JMeter Import",
			Concept:             "Thread group, HTTP sampler defaults, headers, assertions, timers, variables, and multi-script scenario draft mapping",
			Fields:              []string{"scenario.groups", "config.http", "config.think_time", "config.pacing"},
			Status:              "partial",
			ExecutionScope:      "Supported HTTP sampler/timer/assertion constructs convert to executable DSL; plugins and arbitrary JSR223 logic remain review-only.",
			ImportSources:       []string{"JMeter JMX"},
			Evidence:            []string{"pkg/convert/jmeter", "pkg/api/handlers_scripts_test.go::TestHandleValidateJMeterImport"},
			RecommendedWorkflow: "Import JMX as scenario draft, review unsupported elements, validate load model and runtime settings, then approve.",
		},
	}
}

func runtimeSettingsStatusVocabulary() []RuntimeSettingsStatusVocab {
	return []RuntimeSettingsStatusVocab{
		{Status: "executed", Meaning: "APEX Load executes this setting in the runtime path named by the entry."},
		{Status: "partial", Meaning: "APEX Load executes a defined subset and preserves or warns on the rest."},
		{Status: "compatibility_only", Meaning: "APEX Load preserves the setting for migration/reporting but does not execute its runtime semantics."},
		{Status: "non_applicable", Meaning: "The setting has no equivalent meaning for the protocol/runtime scope."},
		{Status: "blocked", Meaning: "The setting prevents save/run until repaired or explicitly changed."},
		{Status: "planned", Meaning: "The setting is documented as future work and must not be claimed as executable."},
	}
}

func runtimeSettingsProfilePresets() []RuntimeSettingsProfilePreset {
	oneIteration := 1
	twoIterations := 2
	fiveIterations := 5
	headless := true
	touch := true
	return []RuntimeSettingsProfilePreset{
		{
			ID:                   "ci_smoke",
			Name:                 "CI Smoke",
			Summary:              "Fast deterministic validation for pull requests and import sanity checks.",
			RecommendedProtocols: []string{"http", "soap", "java_http", "websocket", "grpc", "jms-solace", "browser"},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations: &oneIteration,
				LogLevel:   "standard",
				ErrorHandling: &dsl.ErrorHandlingConfig{
					SnapshotOnError: true,
				},
			},
			ScenarioHints: map[string]any{"duration": "1m", "max_vusers_per_group": 5},
			Evidence:      []string{"/api/v1/runtime-settings/support-matrix"},
		},
		{
			ID:                   "release_gate",
			Name:                 "Release Gate",
			Summary:              "Balanced runtime profile for release qualification with snapshots and per-iteration transaction grouping.",
			RecommendedProtocols: []string{"http", "soap", "java_http", "websocket", "grpc", "jms-solace", "browser"},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations:       &twoIterations,
				LogLevel:         "standard",
				AutoTransactions: "per_iteration",
				ErrorHandling: &dsl.ErrorHandlingConfig{
					SnapshotOnError: true,
				},
			},
			ScenarioHints: map[string]any{"duration": "10m", "profile": "engineer"},
		},
		{
			ID:                   "migration_replay",
			Name:                 "Migration Replay",
			Summary:              "High-evidence replay profile for LoadRunner/JMeter/HAR/DevWeb migration review.",
			RecommendedProtocols: []string{"http", "browser", "jmeter", "har", "devweb"},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations:       &oneIteration,
				LogLevel:         "extended",
				AutoTransactions: "per_action",
				ErrorHandling: &dsl.ErrorHandlingConfig{
					ContinueOnError: true,
					SnapshotOnError: true,
				},
			},
			ScriptConfigHints: map[string]any{"config.log.extended_options": []string{"parameter_substitution", "data_returned", "advanced_trace"}},
			ScenarioHints:     map[string]any{"duration": "2m", "requires_runtime_report": true},
		},
		{
			ID:                   "vugen_web2_migration_review",
			Name:                 "VuGen Web 2.0 Migration Review",
			Summary:              "Replay-first profile for TruClient 2.0 - Web imports before approval or HTTP conversion.",
			RecommendedProtocols: []string{"browser", "vugen_web2", "trueclient2_web"},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations:       &oneIteration,
				LogLevel:         "extended",
				AutoTransactions: "per_action",
				Browser: &RuntimeBrowserOverride{
					Headless: &headless,
				},
				ErrorHandling: &dsl.ErrorHandlingConfig{
					ContinueOnError: true,
					SnapshotOnError: true,
				},
			},
			ScriptConfigHints: map[string]any{
				"config.browser.client_metrics":      true,
				"config.browser.capture_screenshots": "on_error",
				"config.log.extended_options":        []string{"parameter_substitution", "data_returned", "advanced_trace"},
			},
			ScenarioHints: map[string]any{
				"duration":                "2m",
				"requires_browser_replay": true,
				"requires_runtime_report": true,
				"claim_policy":            "diagnostic_canary_only",
			},
			Evidence: []string{"docs/VUGEN_WEB2_TRUCLIENT2_PARITY_PLAN.md"},
		},
		{
			ID:                   "browser_canary",
			Name:                 "Browser Canary",
			Summary:              "Low-density browser UX sentinel paired with protocol load.",
			RecommendedProtocols: []string{"browser"},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations: &oneIteration,
				Browser: &RuntimeBrowserOverride{
					Headless:      &headless,
					DeviceProfile: "pixel_8",
					TouchMode:     &touch,
					Orientation:   "portrait",
				},
				LogLevel: "standard",
				ErrorHandling: &dsl.ErrorHandlingConfig{
					SnapshotOnError: true,
				},
			},
			ScriptConfigHints: map[string]any{"config.browser.client_metrics": true, "config.browser.capture_screenshots": "on_error"},
			ScenarioHints:     map[string]any{"vusers_per_generator": "1-5", "claim_policy": "diagnostic_canary_only"},
			Evidence:          []string{"/api/v1/system/browser-benchmarks", "docs/BROWSER_BENCHMARK_ARTIFACTS.md"},
		},
		{
			ID:                   "debug_trace",
			Name:                 "Debug Trace",
			Summary:              "Verbose troubleshooting profile for protocol onboarding and failed migrations.",
			RecommendedProtocols: []string{"http", "soap", "java_http", "websocket", "grpc", "jms-solace", "browser"},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations:       &oneIteration,
				LogLevel:         "extended",
				AutoTransactions: "per_action",
				ErrorHandling: &dsl.ErrorHandlingConfig{
					ContinueOnError: true,
					SnapshotOnError: true,
					MaxRetries:      1,
					RetryDelayMs:    250,
					RetryBackoff:    "fixed",
				},
			},
			ScriptConfigHints: map[string]any{"config.log.extended_options": []string{"parameter_substitution", "data_returned", "advanced_trace"}},
			ScenarioHints:     map[string]any{"duration": "2m", "export": []string{"json", "csv"}},
		},
		{
			ID:                   "soak_stress",
			Name:                 "Soak / Stress",
			Summary:              "Longer-running profile with conservative pacing and standard logging for resource trend evidence.",
			RecommendedProtocols: []string{"http", "soap", "java_http", "websocket", "grpc", "jms-solace"},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations: &fiveIterations,
				LogLevel:   "standard",
				Pacing: &dsl.PacingConfig{
					Type:       "fixed_interval",
					IntervalMs: 1000,
				},
				ThinkTime: &dsl.ThinkTimeConfig{
					Type:    "recorded",
					LimitMs: 5000,
				},
				ErrorHandling: &dsl.ErrorHandlingConfig{
					SnapshotOnError: true,
				},
			},
			ScenarioHints: map[string]any{"duration": "1h", "resource_baseline": true, "trend_profile": "engineer"},
		},
	}
}

func runtimeSettingsSupportedProtocols(matrix []RuntimeSettingsSupportEntry) []string {
	set := make(map[string]struct{})
	for _, entry := range matrix {
		for _, protocol := range entry.Protocols {
			if protocol = strings.TrimSpace(protocol); protocol != "" {
				set[protocol] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for protocol := range set {
		out = append(out, protocol)
	}
	sort.Strings(out)
	return out
}

func runtimeSettingsStatusCounts(matrix []RuntimeSettingsSupportEntry) map[string]int {
	counts := make(map[string]int)
	for _, entry := range matrix {
		counts[entry.Status]++
	}
	return counts
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
