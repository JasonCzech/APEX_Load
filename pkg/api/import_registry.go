package api

import (
	"net/http"
	"sort"

	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/brokerconfig"
	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/eventhubscapture"
	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	k6import "github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/raml"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
)

// importerRegistration is what the platform knows about one import format.
//
// Before this existed the format list lived in six places that nothing
// connected: the route table, a support-matrix handler per format, the
// batch-commit allowlist, the preflight coverage test, the frontend, and the
// OpenAPI spec. Registering a format is now one entry, and import_registry_test
// holds the route table and the allowlist to it.
//
// Route registration itself deliberately stays as literal apiRouteSpec/mux
// calls: the OpenAPI generator discovers routes by reading those literals, so a
// loop-built route would document nothing.
type importerRegistration struct {
	// Profile is the durable source_profile key. It is written to import
	// receipts and audit events, and it is the wire value the batch commit
	// endpoint accepts.
	Profile string
	// DisplayName names the format in operator-facing text.
	DisplayName string
	// Route is the path segment under /api/v1/scripts/import/. It is empty for
	// formats that have no route of their own — either because they ride
	// another importer's reader (salesforce arrives through the HAR route) or
	// because the drafts are produced internally (browser recordings) or by a
	// shared request importer (curl, raw_http, rest_json).
	Route string
	// ProfileAliases are additional wire spellings the batch commit endpoint
	// accepts for Profile, kept for clients written against older names.
	ProfileAliases []string
	// SupportMatrix builds the format's support-matrix payload. A nil value
	// means the format publishes no matrix; the registry test requires the
	// absence to be deliberate rather than forgotten.
	SupportMatrix func(*Server) map[string]any
	// BatchCommit reports whether /scripts/import/commit-batch accepts this
	// profile. That endpoint is the create-only persistence path for importers
	// with no format-specific replace semantics; formats with their own commit
	// route (openapi, postman, asyncapi, bruno) are false here.
	BatchCommit bool
}

// importerRegistrations is the single list of import formats. Keep it sorted by
// profile so the file reads as an inventory.
var importerRegistrations = []importerRegistration{
	{
		Profile:     "asyncapi",
		DisplayName: "AsyncAPI",
		Route:       "asyncapi",
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix": asyncapi.GetSupportMatrix(),
				"notes":          asyncapi.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:     "broker_config",
		DisplayName: "Broker config",
		Route:       "broker-config",
		BatchCommit: true,
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix": brokerconfig.GetSupportMatrix(),
				"notes":          brokerconfig.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:       "browser_recorder",
		DisplayName:   "Browser recorder",
		BatchCommit:   true,
		SupportMatrix: nil, // Recording capability is reported by the recorder routes.
	},
	{
		Profile:       "browser_recording_har",
		DisplayName:   "Browser recording (HTTP from HAR)",
		BatchCommit:   true,
		SupportMatrix: nil, // Shares the HAR importer's coverage.
	},
	{
		Profile:     "bruno",
		DisplayName: "Bruno",
		Route:       "bruno",
		// Bruno commits through its own route, which also persists the native
		// collection and its retained source revision.
		SupportMatrix: brunoSupportMatrixPayload,
	},
	{
		Profile:       "curl",
		DisplayName:   "cURL command",
		BatchCommit:   true,
		SupportMatrix: nil, // Shares the sample importer's request coverage.
	},
	{
		Profile:     "devweb",
		DisplayName: "DevWeb",
		Route:       "devweb",
		BatchCommit: true,
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix":  devweb.GetSupportMatrix(),
				"notes":           devweb.GetSupportMatrixNotes(),
				"tested_examples": devweb.GetSupportMatrixExamples(),
			}
		},
	},
	{
		Profile:       "enterprise_script_archive",
		DisplayName:   "Enterprise script archive",
		Route:         "enterprise-script-archive",
		SupportMatrix: enterpriseArchiveSupportMatrixPayload,
	},
	{
		Profile:     "eventhubs_capture",
		DisplayName: "Event Hubs Capture",
		Route:       "eventhubs-capture",
		BatchCommit: true,
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix": eventhubscapture.GetSupportMatrix(),
				"notes":          eventhubscapture.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:       "github",
		DisplayName:   "GitHub",
		BatchCommit:   true,
		SupportMatrix: nil, // The source is an APEX script already; nothing to map.
	},
	{
		Profile:     "grafana",
		DisplayName: "Grafana dashboard",
		// Dashboards import under /monitoring, not /scripts/import, and commit
		// through their own route.
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"entries": grafana.GetSupportMatrix(),
				"notes":   grafana.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:       "har",
		DisplayName:   "HAR",
		Route:         "har",
		BatchCommit:   true,
		SupportMatrix: nil, // Coverage is the sample importer's request surface.
	},
	{
		Profile:     "jmeter",
		DisplayName: "JMeter",
		Route:       "jmeter",
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				// Flat arrays retained for backward compatibility with existing clients.
				"support_matrix": jmeter.GetSupportMatrix(),
				// Structured entries (mirrors the AsyncAPI/k6/DevWeb importers) carry
				// per-construct status, execution_status, and review flags.
				"support_matrix_entries": jmeter.GetSupportMatrixEntries(),
				"notes":                  jmeter.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:     "k6",
		DisplayName: "k6",
		Route:       "k6",
		BatchCommit: true,
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix": k6import.GetSupportMatrix(),
				"notes":          k6import.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:     "openapi",
		DisplayName: "OpenAPI",
		Route:       "openapi",
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix": openapi.GetSupportMatrix(),
				"notes":          openapi.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:     "postman",
		DisplayName: "Postman",
		Route:       "postman",
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix": postman.GetSupportMatrix(),
				"notes":          postman.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:     "raml",
		DisplayName: "RAML",
		Route:       "raml",
		BatchCommit: true,
		SupportMatrix: func(*Server) map[string]any {
			return map[string]any{
				"support_matrix": raml.GetSupportMatrix(),
				"notes":          raml.GetSupportMatrixNotes(),
			}
		},
	},
	{
		Profile:       "raw_http",
		DisplayName:   "Raw HTTP request",
		BatchCommit:   true,
		SupportMatrix: nil, // Shares the sample importer's request coverage.
	},
	{
		Profile:       "recorder",
		DisplayName:   "Recorder",
		Route:         "recorder",
		BatchCommit:   true,
		SupportMatrix: nil, // Step coverage is reported per conversion, not as a matrix.
	},
	{
		Profile:        "recorder_export",
		DisplayName:    "Recorder export",
		ProfileAliases: []string{"recorder-export"},
		BatchCommit:    true,
		SupportMatrix:  nil, // Same conversion path as recorder.
	},
	{
		Profile:       "rest_json",
		DisplayName:   "REST JSON request",
		BatchCommit:   true,
		SupportMatrix: nil, // Shares the sample importer's request coverage.
	},
	{
		Profile:       "salesforce",
		DisplayName:   "Salesforce",
		Route:         "salesforce",
		BatchCommit:   true,
		SupportMatrix: nil, // Detection is reported per conversion as a review section.
	},
	{
		Profile:        "sample",
		DisplayName:    "Sample request",
		Route:          "sample",
		ProfileAliases: []string{"sample-import"},
		BatchCommit:    true,
		SupportMatrix:  nil, // The request surface is the DSL's own.
	},
	{
		Profile:     "truclient",
		DisplayName: "TruClient",
		BatchCommit: true,
		// TruClient scripts arrive inside enterprise archives, and that format's
		// matrix already publishes the step surface, script types, and Web2
		// capability scopes. A second copy here would have no endpoint serving it.
		SupportMatrix: nil,
	},
	{
		Profile:       "wsdl",
		DisplayName:   "WSDL / SOAP",
		Route:         "wsdl",
		BatchCommit:   true,
		SupportMatrix: nil, // No published matrix yet; the converter reports per-operation.
	},
}

// importerRegistrationByProfile looks a format up by its durable profile key.
func importerRegistrationByProfile(profile string) (importerRegistration, bool) {
	for _, registration := range importerRegistrations {
		if registration.Profile == profile {
			return registration, true
		}
	}
	return importerRegistration{}, false
}

// writeImporterSupportMatrix answers a format's support-matrix route from the
// registry. The per-format route handlers stay as thin named functions so the
// OpenAPI generator keeps documenting one endpoint per format.
//
// The request is threaded through even though the payload does not vary by it:
// the generator only follows a handler's delegate when the call passes r, and
// without it these routes lose their documented responses.
//
// Every route that reaches here is registered with a matrix — the registry test
// holds that — so a missing one is a wiring mistake and answers 500 rather than
// telling the operator the format publishes nothing.
func (s *Server) writeImporterSupportMatrix(w http.ResponseWriter, r *http.Request, profile string) {
	registration, ok := importerRegistrationByProfile(profile)
	if !ok || registration.SupportMatrix == nil {
		writeError(w, http.StatusInternalServerError, "support matrix is not configured for this import format")
		return
	}
	writeJSON(w, http.StatusOK, registration.SupportMatrix(s))
}

// importerProfileNormalization is the wire-level allowlist for the batch commit
// endpoint: every accepted spelling mapped to its durable profile. OpenAPI
// generation calls into this too, so SDK guidance cannot drift from server
// acceptance when a format is registered.
func importerProfileNormalization() map[string]string {
	normalization := make(map[string]string, len(importerRegistrations))
	for _, registration := range importerRegistrations {
		if !registration.BatchCommit {
			continue
		}
		normalization[registration.Profile] = registration.Profile
		for _, alias := range registration.ProfileAliases {
			normalization[alias] = registration.Profile
		}
	}
	return normalization
}

// importerProfileWireValues is the sorted set of accepted source_profile
// spellings, used to fence the batch commit request schema.
func importerProfileWireValues() []string {
	normalization := importerProfileNormalization()
	values := make([]string, 0, len(normalization))
	for value := range normalization {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// enterpriseArchiveSupportMatrixPayload reports archive coverage across the RTS
// mapping, the TruClient step surface, and the Java import surfaces.
func enterpriseArchiveSupportMatrixPayload(*Server) map[string]any {
	return map[string]any{
		"support_matrix":             enterprisearchive.GetRTSSupportMatrix(),
		"notes":                      enterprisearchive.GetRTSSupportMatrixNotes(),
		"tested_examples":            enterprisearchive.GetRTSSupportMatrixExamples(),
		"trueclient_script_types":    trueclient.ScriptTypeScopes(),
		"trueclient_step_matrix":     trueclient.StepScopes(),
		"trueclient_step_coverage":   trueclient.StepCoverage(),
		"web2_profile_capabilities":  trueclient.Web2ProfileCapabilityScopes(),
		"web2_fixture_contract":      trueclient.Web2FixtureContracts(),
		"protocol_support_matrix":    protocolBundleSupportMatrix(),
		"web_http_function_matrix":   enterprisearchive.WebHTTPFunctionCoverage(),
		"java_lr_function_matrix":    enterprisearchive.JavaLRFunctionCoverage(),
		"java_import_surface_matrix": enterprisearchive.JavaImportSurfaceCoverage(),
		"java_http_idiom_matrix":     enterprisearchive.JavaHTTPIdiomCoverage(),
		"java_jms_idiom_matrix":      enterprisearchive.JavaJMSIdiomCoverage(),
		"java_jdbc_idiom_matrix":     enterprisearchive.JavaJDBCIdiomCoverage(),
		"source_protocol_matrix": []map[string]any{
			{
				"canonical_source_protocol": "java_vuser",
				"display_name":              "LoadRunner Java Vuser",
				"apexload_protocol":         "java_vuser",
				"status":                    "external_runner_required",
				"promotion_gate":            "authoring approval plus an enabled governed java_vuser_jvm_runner advertising java_vuser_persistent_lifecycle_v1 and java_vuser_session_frame_token_v1 with the required classpath",
				"notes":                     "HTTP, JDBC, JMS, SOAP, LoadRunner utility calls, and custom Java remain together in one lifecycle-preserving Java Vuser draft.",
			},
		},
	}
}
