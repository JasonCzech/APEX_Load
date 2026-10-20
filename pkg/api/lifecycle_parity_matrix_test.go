package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// The authoring lifecycle parity matrix (AUTH-2, A7). Four parts, one
// generated document (docs/qa/LIFECYCLE_PARITY_MATRIX.md):
//
//   - every validating entry point × a fixture corpus across protocols must
//     report the same static findings (contextual runtime/install/policy
//     differences are not static findings and are classified apart);
//   - the four batch, archive and JMeter saves validate their persisted copy
//     with the stored validator and report the same findings (the
//     single-script and Bruno commits validate differently and are not
//     covered);
//   - every POST /api/v1/scripts/import/* route is inventoried with its
//     lifecycle role, so a new launcher without a row fails the build;
//   - every AUTH-2 row names the Playwright tests that prove it, and each
//     runs in both e2e profiles.

const lifecycleParityMatrixPath = "../../docs/qa/LIFECYCLE_PARITY_MATRIX.md"

// lifecycleFixture is one script of the corpus; Clean fixtures have no
// static finding.
type lifecycleFixture struct {
	Name    string
	Class   string
	Browser bool
	Clean   bool
	// Unstorable content cannot be stored as an ordinary script: create and
	// update refuse it with a storage-safety message that leads with its
	// findings. A credential-bearing Java Vuser import is stored only through
	// the authoring-session approval of its import, which stores the copy
	// preparePersistentImportedLibraryScript prepares, so the stored routes
	// are covered by the import-save check: it prepares the copy the same way
	// and runs the stored validator those routes use.
	Unstorable bool
	// ImportTrust names a contextual difference: when a server-owned import
	// attests the content, its persisted copy has none of the findings.
	ImportTrust bool
	Script      func() *dsl.Script
}

func lifecycleFixtures() []lifecycleFixture {
	base := dsl.ScriptConfig{BaseURL: "https://lifecycle.example.test"}
	script := func(protocol string, config dsl.ScriptConfig, actions ...dsl.Action) func() *dsl.Script {
		return func() *dsl.Script {
			return &dsl.Script{Name: "Lifecycle " + protocol, Version: "1.0.0", Protocol: protocol, Config: config, Actions: actions}
		}
	}
	return []lifecycleFixture{
		{Name: "http_valid", Class: "HTTP valid", Clean: true, Script: script("http", base, dsl.Action{Name: "orders", Type: "request", Method: "GET", Target: "/orders"})},
		{Name: "http_missing_target", Class: "HTTP invalid", Script: script("http", dsl.ScriptConfig{}, dsl.Action{Name: "orders", Type: "request", Method: "GET"})},
		{Name: "browser_engine", Class: "Browser engine", Browser: true, Script: script("browser", dsl.ScriptConfig{Browser: dsl.BrowserConfig{Engine: "netscape"}}, dsl.Action{Name: "capture", Type: "browser_screenshot"})},
		{Name: "browser_profile", Class: "Browser profile", Browser: true, Script: script("browser", dsl.ScriptConfig{Browser: dsl.BrowserConfig{Engine: "webkit", DeviceProfile: "no-such-profile"}}, dsl.Action{Name: "capture", Type: "browser_screenshot"})},
		{Name: "browser_selector", Class: "Selectors", Browser: true, Script: script("browser", dsl.ScriptConfig{Browser: dsl.BrowserConfig{Engine: "chromium"}}, dsl.Action{Name: "click", Type: "browser_click"})},
		{Name: "grpc_unary_target", Class: "gRPC modes", Script: script("grpc", dsl.ScriptConfig{BaseURL: "lifecycle.example.test:443"}, dsl.Action{Name: "call", Type: "grpc_unary", Target: "service-only"})},
		{Name: "kafka_produce_topic", Class: "Kafka modes", Script: script("kafka", dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true}}, dsl.Action{Name: "produce", Type: "kafka_produce"})},
		{Name: "eventhubs_produce_hub", Class: "Event Hubs modes", Script: script("eventhubs", dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{Mock: true}}, dsl.Action{Name: "produce", Type: "eventhubs_produce"})},
		{Name: "eventhubs_consumer_mode", Class: "Event Hubs modes", Script: script("eventhubs", dsl.ScriptConfig{EventHubs: dsl.EventHubsConfig{Mock: true, EventHub: "orders", ConsumerMode: "teleport"}}, dsl.Action{Name: "consume", Type: "eventhubs_consume"})},
		{Name: "jms_solace", Class: "JMS Solace", Script: script("jms-solace", dsl.ScriptConfig{JMS: dsl.JMSConfig{Provider: "solace", Mock: true}}, dsl.Action{Name: "publish", Type: "jms_publish"})},
		{Name: "jms_ibmmq", Class: "JMS IBM MQ", Script: script("jms-ibmmq", dsl.ScriptConfig{JMS: dsl.JMSConfig{Provider: "ibmmq", Mock: true}}, dsl.Action{Name: "publish", Type: "jms_publish"})},
		{Name: "jms_servicebus", Class: "JMS Service Bus", Script: script("jms-servicebus", dsl.ScriptConfig{JMS: dsl.JMSConfig{Provider: "servicebus", Mock: true}}, dsl.Action{Name: "publish", Type: "jms_publish"})},
		{Name: "nested_control_flow", Class: "Nested control flow", Script: script("http", base, dsl.Action{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{
			{Name: "branch", Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Name: "orders", Type: "request", Method: "GET"}}}}}}})},
		{Name: "templates_secrets", Class: "Templates and secrets", Clean: true, Script: script("http", base, dsl.Action{Name: "orders", Type: "request", Method: "GET", Target: "/orders/{{param.order}}",
			Headers: map[string]string{"Authorization": "Bearer {{secret.api_token}}"}})},
		{Name: "java_credential_vm_argument", Class: "Java VM arguments", Unstorable: true, ImportTrust: true, Script: func() *dsl.Script {
			script := javaVMDraft([]string{"-Ddatabase.password=lifecycle-matrix"})
			script.Actions[0].Target = "Actions"
			return script
		}},
	}
}

// lifecycleEntryPoint is one validating route; Stored routes validate a
// stored copy of the fixture, BrowserOnly routes only take browser scripts.
type lifecycleEntryPoint struct {
	Name, Method, Path string
	Stored             bool
	BrowserOnly        bool
	// Refuses: the route rejects a script with findings (400); validation
	// routes report them (200).
	Refuses bool
	// Executes: the route would run a clean script, so only invalid
	// fixtures exercise it here.
	Executes bool
}

func lifecycleEntryPoints() []lifecycleEntryPoint {
	return []lifecycleEntryPoint{
		{Name: "create", Method: http.MethodPost, Path: "/api/v1/scripts", Refuses: true},
		{Name: "update", Method: http.MethodPut, Path: "/api/v1/scripts/{valid}", Refuses: true},
		{Name: "validate draft", Method: http.MethodPost, Path: "/api/v1/scripts/validate"},
		{Name: "validate stored", Method: http.MethodPost, Path: "/api/v1/scripts/{stored}/validate", Stored: true},
		{Name: "debug draft", Method: http.MethodPost, Path: "/api/v1/scripts/debug/sessions", Refuses: true, Executes: true},
		{Name: "debug stored", Method: http.MethodPost, Path: "/api/v1/scripts/{stored}/debug/sessions", Stored: true, Refuses: true, Executes: true},
		{Name: "replay session draft", Method: http.MethodPost, Path: "/api/v1/scripts/replay/sessions", Refuses: true, Executes: true},
		{Name: "replay session stored", Method: http.MethodPost, Path: "/api/v1/scripts/{stored}/replay/sessions", Stored: true, Refuses: true, Executes: true},
		{Name: "replay draft", Method: http.MethodPost, Path: "/api/v1/scripts/replay", Refuses: true, Executes: true},
		{Name: "replay stored", Method: http.MethodPost, Path: "/api/v1/scripts/{stored}/replay", Stored: true, Refuses: true, Executes: true},
		{Name: "browser replay draft", Method: http.MethodPost, Path: "/api/v1/scripts/browser-replay/sessions", BrowserOnly: true, Refuses: true, Executes: true},
		{Name: "browser replay stored", Method: http.MethodPost, Path: "/api/v1/scripts/{stored}/browser-replay/sessions", Stored: true, BrowserOnly: true, Refuses: true, Executes: true},
	}
}

func lifecycleApplies(entry lifecycleEntryPoint, fixture lifecycleFixture) bool {
	return (!entry.BrowserOnly || fixture.Browser) && !(entry.Executes && fixture.Clean) && !(entry.Stored && fixture.Unstorable)
}

// lifecycleImportSaves are the four batch, archive and JMeter saves (the
// single-script and Bruno commits validate differently and are not covered).
// Each validates the persisted copy (preparePersistentImportedLibraryScript,
// then the stored validator) and refuses findings with 400 (the batch saves
// prefix "script N: "; JMeter save does not); the handler file must keep both
// calls.
var lifecycleImportSaves = map[string]string{
	"/api/v1/scripts/import/commit-batch":                   "generic_import_batch_commit.go",
	"/api/v1/scripts/import/enterprise-script-archive/save": "handlers_import_enterprise_save.go",
	"/api/v1/scripts/import/asyncapi/commit-batch":          "asyncapi_import_batch_commit.go",
	"/api/v1/scripts/import/jmeter/save":                    "handlers_import_jmeter.go",
}

// TestLifecycleParityStaticFindingsMatchAcrossEntryPoints checks that the
// parser, the disk parser and every validating route report exactly the
// static findings of each fixture.
func TestLifecycleParityStaticFindingsMatchAcrossEntryPoints(t *testing.T) {
	server := NewServer(ServerConfig{})
	valid := server.runner.StoreScript(lifecycleFixtures()[0].Script())
	for _, fixture := range lifecycleFixtures() {
		t.Run(fixture.Name, func(t *testing.T) {
			expected := append([]string{}, validateScript(fixture.Script())...)
			if fixture.Clean != (len(expected) == 0) {
				t.Fatalf("fixture findings %q do not match Clean=%t", expected, fixture.Clean)
			}
			body, err := json.Marshal(map[string]any{"script": fixture.Script()})
			if err != nil {
				t.Fatal(err)
			}
			// The parser and the disk parser apply the same validator.
			path := filepath.Join(t.TempDir(), "script.json")
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			for name, parse := range map[string]func() error{
				"parser":      func() error { _, err := dsl.Parse(body); return err },
				"disk parser": func() error { _, err := dsl.ParseFile(path); return err },
			} {
				err := parse()
				if fixture.Clean {
					if err != nil {
						t.Fatalf("%s refused a clean fixture: %v", name, err)
					}
					continue
				}
				var invalid *dsl.ValidationError
				if !errors.As(err, &invalid) || !reflect.DeepEqual(invalid.Issues, expected) {
					t.Fatalf("%s: %v, want exactly %q", name, err, expected)
				}
			}
			var stored *StoredScript
			if !fixture.Unstorable {
				stored = server.runner.StoreScript(fixture.Script())
			}
			for _, entry := range lifecycleEntryPoints() {
				if !lifecycleApplies(entry, fixture) {
					continue
				}
				replacer := strings.NewReplacer("{valid}", valid.ID)
				if stored != nil {
					replacer = strings.NewReplacer("{stored}", stored.ID, "{valid}", valid.ID)
				}
				route := replacer.Replace(entry.Path)
				var reader *bytes.Reader
				if entry.Stored {
					reader = bytes.NewReader(nil)
				} else {
					reader = bytes.NewReader(body)
				}
				request := httptest.NewRequest(entry.Method, route, reader)
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				server.mux.ServeHTTP(recorder, request)
				want := http.StatusOK
				if entry.Refuses && !fixture.Clean {
					want = http.StatusBadRequest
				} else if entry.Name == "create" {
					want = http.StatusCreated
				}
				if recorder.Code != want {
					t.Fatalf("%s: status %d, want %d: %s", entry.Name, recorder.Code, want, recorder.Body.String())
				}
				if !entry.Refuses || !fixture.Clean {
					issues := extractLifecycleIssues(t, recorder.Body.Bytes())
					// Write routes refuse unstorable content with a
					// storage-safety message that leads with its findings.
					if fixture.Unstorable && entry.Refuses && !entry.Executes {
						if !strings.HasPrefix(strings.Join(issues, "; "), strings.Join(expected, "; ")) {
							t.Fatalf("%s: issues %q, want a message leading with %q", entry.Name, issues, expected)
						}
						continue
					}
					if !reflect.DeepEqual(issues, expected) {
						t.Fatalf("%s: issues %q, want %q", entry.Name, issues, expected)
					}
				}
			}
		})
	}
}

// extractLifecycleIssues reads a response's static findings; a clean
// validation has none.
func extractLifecycleIssues(t *testing.T, body []byte) []string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if issues, ok := payload["issues"]; ok && issues != nil {
		return extractValidationIssues(t, body)
	}
	if _, ok := payload["error"]; ok {
		return extractValidationIssues(t, body)
	}
	return []string{}
}

// TestLifecycleParityImportSavesReportTheSameFindings checks the import
// saves: each persists a copy that the stored validator judges, and that copy
// has exactly the fixture's findings, except where a server-owned import
// attests credentials the fixture carries (the import_credential_trust
// contextual cause).
func TestLifecycleParityImportSavesReportTheSameFindings(t *testing.T) {
	for route, file := range lifecycleImportSaves {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), "preparePersistentImportedLibraryScript(") || !regexp.MustCompile(`validate(StoredExecution)?Script\((persistent|safeScript)\)`).Match(source) {
			t.Errorf("%s (%s) no longer validates its persisted copy with the stored validator", route, file)
		}
	}
	for _, fixture := range lifecycleFixtures() {
		t.Run(fixture.Name, func(t *testing.T) {
			expected := append([]string{}, validateScript(fixture.Script())...)
			if fixture.ImportTrust {
				expected = []string{}
			}
			persistent, err := preparePersistentImportedLibraryScript(fixture.Script(), nil, "har")
			if err != nil {
				t.Fatalf("import save refused to prepare: %v", err)
			}
			if issues := append([]string{}, validateStoredExecutionScript(persistent)...); !reflect.DeepEqual(issues, expected) {
				t.Fatalf("import save copy: issues %q, want %q", issues, expected)
			}
		})
	}
}

// lifecycleImportRoute is one import route's role in the lifecycle.
type lifecycleImportRoute struct {
	Role, Row string
	// Proof names the test files (pkg/api Go tests or dashboard e2e specs)
	// that exercise the route; each must mention the route.
	Proof []string
}

// lifecycleImportRoutes inventories every POST /api/v1/scripts/import/*
// route. Adding a route without a row here fails the build.
var lifecycleImportRoutes = map[string]lifecycleImportRoute{
	"/api/v1/scripts/import/asyncapi":                           {"launcher", "Every import launcher", []string{"handlers_web_api_imports_test.go"}},
	"/api/v1/scripts/import/broker-config":                      {"launcher", "Every import launcher", []string{"handlers_web_api_imports_test.go"}},
	"/api/v1/scripts/import/bruno/inspect":                      {"launcher", "Every import launcher", []string{"handlers_bruno_import_test.go", "tier0-bruno-import.spec.ts"}},
	"/api/v1/scripts/import/cloudtest":                          {"launcher", "Every import launcher", []string{"handlers_import_cloudtest_test.go"}},
	"/api/v1/scripts/import/devweb":                             {"launcher", "Every import launcher", []string{"handlers_scripts_test.go"}},
	"/api/v1/scripts/import/enterprise-script-archive":          {"launcher", "Every import launcher", []string{"java_vuser_import_launch_flow_test.go", "handlers_import_enterprise_decode_test.go"}},
	"/api/v1/scripts/import/eventhubs-capture":                  {"launcher", "Every import launcher", []string{"handlers_eventhubs_capture_test.go"}},
	"/api/v1/scripts/import/github":                             {"launcher", "Every import launcher", []string{"github_handlers_test.go"}},
	"/api/v1/scripts/import/har":                                {"launcher", "Every import launcher", []string{"handlers_import_sample_har_protected_credentials_test.go", "workflow-import-source.spec.ts"}},
	"/api/v1/scripts/import/jmeter":                             {"launcher", "Every import launcher", []string{"jmeter_review_evidence_test.go", "workflow-jmeter-fidelity.spec.ts"}},
	"/api/v1/scripts/import/k6":                                 {"launcher", "Every import launcher", []string{"handlers_k6_test.go"}},
	"/api/v1/scripts/import/openapi":                            {"launcher", "Every import launcher", []string{"handlers_web_api_imports_test.go", "tier0-script-import.spec.ts"}},
	"/api/v1/scripts/import/oracle-ebusiness/review":            {"launcher", "Every import launcher", []string{"oracle_ebusiness_review_test.go"}},
	"/api/v1/scripts/import/postman":                            {"launcher", "Every import launcher", []string{"handlers_web_api_imports_test.go", "workflow-import-source.spec.ts"}},
	"/api/v1/scripts/import/raml":                               {"launcher", "Every import launcher", []string{"handlers_import_raml_fidelity_test.go"}},
	"/api/v1/scripts/import/recorder":                           {"launcher", "Recording", []string{"handlers_recorder_import_test.go"}},
	"/api/v1/scripts/import/salesforce":                         {"launcher", "Every import launcher", []string{"handlers_import_sample_har_protected_credentials_test.go"}},
	"/api/v1/scripts/import/sample":                             {"launcher", "Every import launcher", []string{"handlers_import_sample_har_protected_credentials_test.go"}},
	"/api/v1/scripts/import/wsdl":                               {"launcher", "Every import launcher", []string{"handlers_web_api_imports_test.go"}},
	"/api/v1/scripts/import/asyncapi/commit":                    {"commit", "Every import launcher", []string{"import_commit_service_test.go"}},
	"/api/v1/scripts/import/asyncapi/commit-batch":              {"commit", "Multi-script imports", []string{"asyncapi_import_batch_commit_test.go"}},
	"/api/v1/scripts/import/bruno/commit":                       {"commit", "Multi-script imports", []string{"handlers_bruno_import_test.go", "tier0-bruno-import.spec.ts"}},
	"/api/v1/scripts/import/commit-batch":                       {"commit", "Multi-script imports", []string{"generic_import_batch_commit_test.go", "workflow-aura-outcomes.spec.ts"}},
	"/api/v1/scripts/import/enterprise-script-archive/save":     {"commit", "Multi-script imports", []string{"handlers_import_enterprise_save_test.go", "workflow-loadrunner-review.spec.ts"}},
	"/api/v1/scripts/import/jmeter/save":                        {"commit", "Every import launcher", []string{"jmeter_review_evidence_test.go", "workflow-jmeter-fidelity.spec.ts"}},
	"/api/v1/scripts/import/openapi/commit":                     {"commit", "Every import launcher", []string{"import_commit_service_test.go", "tier0-script-import.spec.ts"}},
	"/api/v1/scripts/import/postman/save":                       {"commit", "Every import launcher", []string{"import_source_lifecycle_test.go"}},
	"/api/v1/scripts/import/enterprise-script-archive/validate": {"validate", "Every import launcher", []string{"openapi_enterprise_archive_collection_test.go", "workflow-loadrunner-review.spec.ts"}},
	"/api/v1/scripts/import/jmeter/validate":                    {"validate", "Every import launcher", []string{"handlers_scripts_test.go"}},
	"/api/v1/scripts/import/postman/validate":                   {"validate", "Every import launcher", []string{"handlers_web_api_imports_test.go"}},
	"/api/v1/scripts/import/recorder/validate":                  {"validate", "Recording", []string{"handlers_recorder_import_test.go"}},
	"/api/v1/scripts/import/recorder/replay":                    {"validate", "Recording", []string{"handlers_recorder_import_test.go"}},
	"/api/v1/scripts/import/parameterize/apply":                 {"shared review", "Every import launcher", []string{"handlers_import_paramize_test.go", "workflow-aura-outcomes.spec.ts"}},
	"/api/v1/scripts/import/transport/apply":                    {"shared review", "Every import launcher", []string{"handlers_import_transport_test.go", "workflow-import-transport.spec.ts"}},
	"/api/v1/scripts/import/repairs/apply":                      {"shared review", "Every import launcher", []string{"workflow-jmeter-fidelity.spec.ts"}},
	"/api/v1/scripts/import/repairs/preview":                    {"shared review", "Every import launcher", []string{"import_repairs_test.go", "workflow-jmeter-fidelity.spec.ts"}},
	"/api/v1/scripts/import/shared-values/apply":                {"shared review", "Multi-script imports", []string{"handlers_import_shared_values_test.go"}},
	"/api/v1/scripts/import/secrets/migrate":                    {"shared review", "Template/component/clone", []string{"import_candidate_credentials_test.go"}},
	"/api/v1/scripts/import/secrets/discard":                    {"shared review", "Template/component/clone", []string{"handlers_project_secrets_test.go"}},
	"/api/v1/scripts/import/settings":                           {"shared review", "Every import launcher", []string{"handlers_import_settings_test.go", "workflow-import-source.spec.ts"}},
	"/api/v1/scripts/import/browser-recorder/attest":            {"assets", "Recording", []string{"browser_recorder_bridge_import_test.go"}},
	"/api/v1/scripts/import/{id}/assets/upload":                 {"assets", "Every import launcher", []string{"handlers_import_assets_test.go"}},
	"/api/v1/scripts/import/{id}/java-classpath/upload":         {"assets", "Every import launcher", []string{"workflow-loadrunner-review.spec.ts"}},
	"/api/v1/scripts/import/{id}/java-compile":                  {"assets", "Every import launcher", []string{"handlers_import_java_compile_test.go"}},
}

func TestLifecycleParityImportLaunchersAreInventoried(t *testing.T) {
	seen := map[string]bool{}
	for _, route := range registeredServerRoutes(t) {
		if route.Method != http.MethodPost || !strings.HasPrefix(route.Path, "/api/v1/scripts/import/") {
			continue
		}
		seen[route.Path] = true
		if _, ok := lifecycleImportRoutes[route.Path]; !ok {
			t.Errorf("POST %s has no lifecycle parity row: add it to lifecycleImportRoutes with its role and proof", route.Path)
		}
	}
	for path := range lifecycleImportRoutes {
		if !seen[path] {
			t.Errorf("lifecycle parity row %s names no registered POST route", path)
		}
	}
	rows := map[string]bool{}
	for _, row := range lifecycleAuth2Rows() {
		rows[row.Entry] = true
	}
	for path, route := range lifecycleImportRoutes {
		if !rows[route.Row] {
			t.Errorf("%s names AUTH-2 row %q, which is not in the spec table", path, route.Row)
		}
		if len(route.Proof) == 0 {
			t.Errorf("%s names no proof", path)
		}
		// A proof mentions the route, so it exercises it rather than merely
		// naming a feature; a route with a path parameter, built at run time,
		// is mentioned by what follows the parameter.
		tail := "import/" + strings.TrimPrefix(path, "/api/v1/scripts/import/")
		if _, after, ok := strings.Cut(path, "{id}"); ok {
			tail = after
		}
		mention := regexp.MustCompile(regexp.QuoteMeta(tail) + `(?:[^\w/-]|$)`)
		for _, proof := range route.Proof {
			file := proof
			if strings.HasSuffix(proof, ".spec.ts") {
				file = filepath.Join("../../dashboard/e2e", proof)
			}
			source, err := os.ReadFile(file)
			if err != nil {
				t.Errorf("%s names proof %s: %v", path, proof, err)
				continue
			}
			if !mention.Match(source) {
				t.Errorf("%s names proof %s, which never mentions the route", path, proof)
			}
		}
	}
}

// lifecycleAuth2Row is one AUTH-2 table entry and the tests that prove it.
type lifecycleAuth2Row struct {
	Entry string
	// E2E maps a spec file to the test titles that cover the row.
	E2E map[string][]string
	Go  []string
}

func lifecycleAuth2Rows() []lifecycleAuth2Row {
	return []lifecycleAuth2Row{
		{Entry: "Direct JSON and structured editor", Go: []string{"TestLifecycleParityStaticFindingsMatchAcrossEntryPoints"}, E2E: map[string][]string{
			"workflow-script-editor.spec.ts":     {"authored script goals survive an unrelated editor save and canonical reopen", "two editors preserve stale drafts and require explicit revision review before reapply"},
			"workflow-author-experience.spec.ts": {"Author creates, reverses, validates, edits source and saves without losing context"},
		}},
		{Entry: "Template/component/clone", E2E: map[string][]string{
			"workflow-lifecycle-template.spec.ts": {"a starter template opens an independent draft that saves, reopens and leaves the template unchanged"},
		}},
		{Entry: "Every import launcher", Go: []string{"TestLifecycleParityImportLaunchersAreInventoried"}, E2E: map[string][]string{
			"workflow-import-source.spec.ts":   {"source catalog search and importer guidance stay consistent across formats", "JMeter review uses the shared six tabs and retains project scope during upload"},
			"workflow-jmeter-fidelity.spec.ts": {"JMeter fidelity review retains blockers and regenerates explicit timestamp and name repairs"},
		}},
		{Entry: "Multi-script imports", E2E: map[string][]string{
			"workflow-archive-approval-handoff.spec.ts": {"Legacy archive editor returns a durable approval receipt", "Legacy archive editor retains later edits while queueing approval"},
			"workflow-loadrunner-collection.spec.ts":    {"reviews and saves nested VuGen projects independently, retaining failed and Java reviews"},
			"workflow-loadrunner-review.spec.ts":        {"LoadRunner batch saves ready scripts, stays on import, and accepts another ZIP"},
		}},
		{Entry: "Recording", E2E: map[string][]string{
			"workflow-recording-review.spec.ts": {"recording review scopes verification to outputs and invalidates changed conversions"},
		}},
		{Entry: "Classic replay/debug/browser replay", Go: []string{"TestLifecycleParityStaticFindingsMatchAcrossEntryPoints", "TestBrowserCompatibilityMatrixParityAcrossValidationAndReplayEndpoints"}, E2E: map[string][]string{
			"workflow-debugger-experience.spec.ts": {"debugger captures successful evidence, diagnoses runtime failure, repairs a draft and retains protocol-correct history"},
			"workflow-script-session.spec.ts":      {"a validation that returns after an edit is ignored and the next one binds to the edited draft"},
		}},
		{Entry: "Scenario library wizard and compact modal", E2E: map[string][]string{
			"workflow-scenario-composition.spec.ts": {"scenario rename and reopen retain Results, WebSocket, named shape and group timing intent"},
			"workflow-scenario-data.spec.ts":        nil,
		}},
		{Entry: "New run", E2E: map[string][]string{
			"workflow-new-test.spec.ts": {"New Test saves drafts without execution, retains both tabs and validates recurring triggers"},
		}},
		{Entry: "Queued revision and execution overview", E2E: map[string][]string{
			"workflow-new-test.spec.ts": {"actual held-run serialization and an API-seeded queued copy preserve complete authored intent", "queued-copy preserves source capture IDs and durably clears the last authored goal"},
		}},
	}
}

// lifecycleE2EProfiles reads which specs each e2e profile runs: local-auth
// runs only its testMatch list, and the default profile runs every spec but
// its testIgnore patterns.
func lifecycleE2EProfiles(t *testing.T) (map[string]bool, []string) {
	t.Helper()
	raw, err := os.ReadFile("../../dashboard/playwright.config.ts")
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)
	list := func(expression string) []string {
		match := regexp.MustCompile(expression).FindStringSubmatch(config)
		if match == nil {
			t.Fatalf("playwright.config.ts has no %s", expression)
		}
		var out []string
		for _, entry := range regexp.MustCompile(`'\*\*/([^']+)'`).FindAllStringSubmatch(match[1], -1) {
			out = append(out, entry[1])
		}
		return out
	}
	localAuth := map[string]bool{}
	for _, file := range list(`testMatch: process\.env\.APEXLOAD_E2E_PROFILE === 'local-auth'\s*\?\s*\[([^\]]*)\]`) {
		localAuth[file] = true
	}
	return localAuth, list(`testIgnore: process\.env\.APEXLOAD_E2E_PROFILE === 'local-auth' \? \[\] : \[([^\]]*)\]`)
}

// lifecycleProfileSkip reports whether a spec's test (or the whole file)
// skips by e2e profile; with no title, whether any test in the file does.
func lifecycleProfileSkip(source, title string) bool {
	skip := regexp.MustCompile(`test\.(skip|fixme)\([^\n]*APEXLOAD_E2E_PROFILE`)
	if title == "" {
		return skip.MatchString(source)
	}
	start := strings.Index(source, "test('"+title+"'")
	if start < 0 {
		return false
	}
	body := source[start:]
	if end := strings.Index(body[1:], "\ntest("); end > 0 {
		body = body[:end+1]
	}
	// A file-level skip precedes the first test.
	head := source
	if first := strings.Index(source, "\ntest("); first > 0 {
		head = source[:first]
	}
	return skip.MatchString(body) || skip.MatchString(head)
}

// TestLifecycleParityRowsRunInBothProfiles checks every AUTH-2 row against
// the spec table, the named Go tests, and the named Playwright tests: each
// exists, runs in the local-auth profile and is not ignored by the default
// one, and neither skips by profile.
func TestLifecycleParityRowsRunInBothProfiles(t *testing.T) {
	spec, err := os.ReadFile("../../docs/FEATURE_COMPLETION_SPEC.md")
	if err != nil {
		t.Fatal(err)
	}
	table := string(spec)[strings.Index(string(spec), "### AUTH-2"):]
	table = table[:strings.Index(table, "### AUTH-3")]
	var specRows []string
	for _, match := range regexp.MustCompile(`(?m)^\| ([^|]+?) \| [^|]+ \|$`).FindAllStringSubmatch(table, -1) {
		if match[1] != "Entry point" && !strings.HasPrefix(match[1], "---") {
			specRows = append(specRows, match[1])
		}
	}
	var rows []string
	for _, row := range lifecycleAuth2Rows() {
		rows = append(rows, row.Entry)
	}
	if !reflect.DeepEqual(rows, specRows) {
		t.Fatalf("AUTH-2 rows %q, spec table %q", rows, specRows)
	}
	localAuth, defaultIgnore := lifecycleE2EProfiles(t)
	goTests := map[string]bool{}
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range regexp.MustCompile(`(?m)^func (Test\w+)\(t \*testing\.T\)`).FindAllStringSubmatch(string(source), -1) {
			goTests[match[1]] = true
		}
	}
	for _, row := range lifecycleAuth2Rows() {
		if len(row.E2E) == 0 {
			t.Errorf("AUTH-2 row %q has no Playwright proof", row.Entry)
		}
		for _, name := range row.Go {
			if !goTests[name] {
				t.Errorf("AUTH-2 row %q names Go test %s, which does not exist in pkg/api", row.Entry, name)
			}
		}
		for file, titles := range row.E2E {
			raw, err := os.ReadFile(filepath.Join("../../dashboard/e2e", file))
			if err != nil {
				t.Errorf("AUTH-2 row %q names %s: %v", row.Entry, file, err)
				continue
			}
			source := string(raw)
			if !localAuth[file] {
				t.Errorf("%s (AUTH-2 row %q) does not run in the local-auth profile", file, row.Entry)
			}
			for _, pattern := range defaultIgnore {
				if matched, _ := filepath.Match(pattern, file); matched {
					t.Errorf("%s (AUTH-2 row %q) is ignored by the default profile (%s)", file, row.Entry, pattern)
				}
			}
			if len(titles) == 0 && lifecycleProfileSkip(source, "") {
				t.Errorf("%s (AUTH-2 row %q) skips by profile", file, row.Entry)
			}
			for _, title := range titles {
				if !strings.Contains(source, "test('"+title+"'") {
					t.Errorf("%s has no test %q (AUTH-2 row %q)", file, title, row.Entry)
				}
				if lifecycleProfileSkip(source, title) {
					t.Errorf("%s test %q (AUTH-2 row %q) skips by profile, so it does not run in both", file, title, row.Entry)
				}
			}
		}
	}
}

// renderLifecycleParityMatrix is the generated document.
func renderLifecycleParityMatrix() string {
	var out strings.Builder
	out.WriteString("# Authoring lifecycle parity matrix\n\n")
	out.WriteString("Generated by `pkg/api/lifecycle_parity_matrix_test.go` (AUTH-2). Regenerate with `APEXLOAD_UPDATE_LIFECYCLE_PARITY_MATRIX=1 go test ./pkg/api -run TestLifecycleParityMatrixDocumentIsFresh`.\n\n")
	out.WriteString("## Static findings by entry point\n\n")
	out.WriteString("Every entry point, the parser and the disk parser report exactly the fixture's static findings (`validateScript`), in order. Routes that refuse a script answer `400` with them, and validation routes answer `200` with them. Debug and replay routes judge the copy a session executes, with the submitted JVM arguments in place of the sentinels that mask them there; a finding only that copy has (a recorded resource URL whose credential it masks cannot be executed) is contextual, classified apart and covered by `TestTransientReplayRefusesAMaskedRecordedResourceURL`. Routes that would execute a clean script are exercised with invalid fixtures only. Content that cannot be stored as an ordinary script (a credential-bearing JVM argument) is refused by create and update with a storage-safety message that leads with its findings. A credential-bearing Java Vuser import is stored only through the authoring-session approval of its import, which stores the copy `preparePersistentImportedLibraryScript` prepares, so the stored routes are covered for it by the import saves' check below, which prepares the copy the same way and runs the stored validator the stored routes use. Contextual runtime, install, policy and approval outcomes are not static findings and are classified apart.\n\n")
	entries := lifecycleEntryPoints()
	out.WriteString("| Fixture | Class | Parser | Disk parser |")
	for _, entry := range entries {
		out.WriteString(" " + entry.Name + " |")
	}
	out.WriteString("\n| --- | --- | --- | --- |")
	for range entries {
		out.WriteString(" --- |")
	}
	out.WriteString("\n")
	for _, fixture := range lifecycleFixtures() {
		out.WriteString(fmt.Sprintf("| `%s` | %s | ✓ | ✓ |", fixture.Name, fixture.Class))
		for _, entry := range entries {
			if lifecycleApplies(entry, fixture) {
				out.WriteString(" ✓ |")
			} else {
				out.WriteString(" — |")
			}
		}
		out.WriteString("\n")
	}
	out.WriteString("\n## Import saves\n\nThe four batch, archive and JMeter saves validate the persisted copy (`preparePersistentImportedLibraryScript`, then the stored validator) and refuse findings with `400` and the findings joined by `; ` (the three batch saves prefix `script N: `; JMeter save does not). The other import commits are not under this check: the single-script commits (`openapi/commit`, `postman/save`, `asyncapi/commit`) validate the finalized copy with the DSL validator (`400`), and `bruno/commit` validates with its native check (`422`). `TestLifecycleParityImportSavesReportTheSameFindings` checks that each handler keeps both calls and that the copy has exactly each fixture's findings, with one classified contextual cause:\n\n")
	out.WriteString("| Route | Handler |\n| --- | --- |\n")
	saves := make([]string, 0, len(lifecycleImportSaves))
	for route := range lifecycleImportSaves {
		saves = append(saves, route)
	}
	sort.Strings(saves)
	for _, route := range saves {
		out.WriteString(fmt.Sprintf("| `%s` | `%s` |\n", route, lifecycleImportSaves[route]))
	}
	out.WriteString("\n| Fixture | Contextual cause | Import save copy |\n| --- | --- | --- |\n")
	for _, fixture := range lifecycleFixtures() {
		if fixture.ImportTrust {
			out.WriteString(fmt.Sprintf("| `%s` | `import_credential_trust`: a server-owned import attests the credential; a draft's import marker never does | no findings |\n", fixture.Name))
		}
	}
	out.WriteString("\n## Import routes\n\nEvery script import route that accepts a POST has a row; a route without one fails `TestLifecycleParityImportLaunchersAreInventoried`. Each proof is a test file that mentions the route (a Go test in `pkg/api` or a spec in `dashboard/e2e`).\n\n")
	out.WriteString("| Route | Role | AUTH-2 row | Proof |\n| --- | --- | --- | --- |\n")
	paths := make([]string, 0, len(lifecycleImportRoutes))
	for path := range lifecycleImportRoutes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		route := lifecycleImportRoutes[path]
		out.WriteString(fmt.Sprintf("| `%s` | %s | %s | `%s` |\n", path, route.Role, route.Row, strings.Join(route.Proof, "`, `")))
	}
	out.WriteString("\n## AUTH-2 rows\n\nEach row's Playwright tests exist, run in the local-auth profile, are not ignored by the default profile, and do not skip by profile; each named Go test exists. The proofs show that each entry point opens, saves and reopens what it should; they do not each prove every AUTH-2 property (see the deviations in the spec).\n\n| AUTH-2 row | Playwright | Go |\n| --- | --- | --- |\n")
	for _, row := range lifecycleAuth2Rows() {
		files := make([]string, 0, len(row.E2E))
		for file := range row.E2E {
			files = append(files, file)
		}
		sort.Strings(files)
		var proofs []string
		for _, file := range files {
			if len(row.E2E[file]) == 0 {
				proofs = append(proofs, "`"+file+"` (all tests)")
			}
			for _, title := range row.E2E[file] {
				proofs = append(proofs, "`"+file+"`: "+title)
			}
		}
		goTests := "—"
		if len(row.Go) > 0 {
			goTests = "`" + strings.Join(row.Go, "`, `") + "`"
		}
		out.WriteString(fmt.Sprintf("| %s | %s | %s |\n", row.Entry, strings.Join(proofs, "<br>"), goTests))
	}
	return out.String()
}

func TestLifecycleParityMatrixDocumentIsFresh(t *testing.T) {
	want := renderLifecycleParityMatrix()
	if os.Getenv("APEXLOAD_UPDATE_LIFECYCLE_PARITY_MATRIX") == "1" {
		if err := os.WriteFile(lifecycleParityMatrixPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(lifecycleParityMatrixPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with APEXLOAD_UPDATE_LIFECYCLE_PARITY_MATRIX=1)", lifecycleParityMatrixPath, err)
	}
	if string(got) != want {
		t.Fatalf("%s is stale; regenerate with APEXLOAD_UPDATE_LIFECYCLE_PARITY_MATRIX=1", lifecycleParityMatrixPath)
	}
}

// Debug and replay judge what they would execute: a recorded resource URL
// whose credential the executed copy masks cannot be executed, although the
// submitted content alone has no finding.
func TestTransientReplayRefusesAMaskedRecordedResourceURL(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := &dsl.Script{Name: "Recorded resources", Version: "1.0.0", Protocol: "browser",
		Config:   dsl.ScriptConfig{BaseURL: "https://lifecycle.example.test", Browser: dsl.BrowserConfig{Engine: "chromium", DownloadResources: "recorded"}},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "browser_recorder", Preserved: map[string]any{"recorded_resource_urls_available": true, "recorded_resource_urls": []any{"https://user:pass@cdn.lifecycle.example.test/app.js"}}}},
		Actions:  []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}}}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("fixture: the submitted content has findings %q", issues)
	}
	body, err := json.Marshal(map[string]any{"script": script})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/scripts/browser-replay/sessions", "/api/v1/scripts/replay/sessions", "/api/v1/scripts/replay", "/api/v1/scripts/debug/sessions"} {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.mux.ServeHTTP(recorder, request)
		// The preflight refuses it, before any session is reserved: the
		// finding is among its issues, not a later start error.
		var refusal struct {
			Valid  *bool    `json:"valid"`
			Issues []string `json:"issues"`
		}
		if recorder.Code != http.StatusBadRequest || json.Unmarshal(recorder.Body.Bytes(), &refusal) != nil || refusal.Valid == nil || *refusal.Valid || !strings.Contains(strings.Join(refusal.Issues, "\n"), "recorded_resource_urls[0]") || !strings.Contains(strings.Join(refusal.Issues, "\n"), "cannot be executed") {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

// The profile detectors fire: a profile skip anywhere in a file named by
// "(all tests)", one inside a named test, and none where there is none; the
// default profile's ignore list is read.
func TestLifecycleParityProfileDetectors(t *testing.T) {
	source := "test('first', async () => {})\ntest('second', async () => {\n  test.skip(process.env.APEXLOAD_E2E_PROFILE === 'local-auth', 'default only')\n})\n"
	if !lifecycleProfileSkip(source, "") || !lifecycleProfileSkip(source, "second") || lifecycleProfileSkip(source, "first") {
		t.Fatal("profile skip detection")
	}
	raw, err := os.ReadFile("../../dashboard/e2e/workflow-browser-har.spec.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !lifecycleProfileSkip(string(raw), "viewers list HARs but cannot download them, and other projects are denied") || lifecycleProfileSkip(string(raw), "an archived run leads from its retained resources to HAR and archive downloads") {
		t.Fatal("profile skip detection on a real spec")
	}
	localAuth, defaultIgnore := lifecycleE2EProfiles(t)
	if !localAuth["workflow-browser-har.spec.ts"] || len(defaultIgnore) == 0 {
		t.Fatalf("profiles: local-auth %d specs, default ignore %q", len(localAuth), defaultIgnore)
	}
	if matched, _ := filepath.Match(defaultIgnore[0], "tier0-local-auth.spec.ts"); !matched {
		t.Fatalf("default ignore %q does not ignore a local-auth spec", defaultIgnore)
	}
}
