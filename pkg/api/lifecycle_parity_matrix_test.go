package api

import (
	"bytes"
	"encoding/json"
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

// The authoring lifecycle parity matrix (AUTH-2, A7). Three tables, one
// generated document (docs/qa/LIFECYCLE_PARITY_MATRIX.md):
//
//   - every validating entry point × a fixture corpus across protocols must
//     report the same static findings (contextual runtime/install/policy
//     differences are not static findings and are classified apart);
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
	Script  func() *dsl.Script
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
	return (!entry.BrowserOnly || fixture.Browser) && !(entry.Executes && fixture.Clean)
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
				if err == nil {
					t.Fatalf("%s accepted %s", name, fixture.Name)
				}
				for _, issue := range expected {
					if !strings.Contains(err.Error(), issue) {
						t.Fatalf("%s lost %q: %v", name, issue, err)
					}
				}
			}
			stored := server.runner.StoreScript(fixture.Script())
			for _, entry := range lifecycleEntryPoints() {
				if !lifecycleApplies(entry, fixture) {
					continue
				}
				route := strings.NewReplacer("{stored}", stored.ID, "{valid}", valid.ID).Replace(entry.Path)
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
					if issues := extractLifecycleIssues(t, recorder.Body.Bytes()); !reflect.DeepEqual(issues, expected) {
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

// lifecycleImportRoute is one import route's role in the lifecycle.
type lifecycleImportRoute struct {
	Role, Row, Proof string
}

// lifecycleImportRoutes inventories every POST /api/v1/scripts/import/*
// route. Adding a route without a row here fails the build.
var lifecycleImportRoutes = map[string]lifecycleImportRoute{
	"/api/v1/scripts/import/asyncapi":                           {"launcher", "Every import launcher", "AsyncAPI import tests (asyncapi_*_test.go)"},
	"/api/v1/scripts/import/broker-config":                      {"launcher", "Every import launcher", "broker configuration import tests"},
	"/api/v1/scripts/import/bruno/inspect":                      {"launcher", "Every import launcher", "tier0-bruno-import.spec.ts"},
	"/api/v1/scripts/import/cloudtest":                          {"launcher", "Every import launcher", "CloudTest import tests"},
	"/api/v1/scripts/import/devweb":                             {"launcher", "Every import launcher", "DevWeb import tests"},
	"/api/v1/scripts/import/enterprise-script-archive":          {"launcher", "Every import launcher", "workflow-loadrunner-review.spec.ts"},
	"/api/v1/scripts/import/eventhubs-capture":                  {"launcher", "Every import launcher", "Event Hubs Capture import tests"},
	"/api/v1/scripts/import/github":                             {"launcher", "Every import launcher", "GitHub script sync tests"},
	"/api/v1/scripts/import/har":                                {"launcher", "Every import launcher", "workflow-import-source.spec.ts"},
	"/api/v1/scripts/import/jmeter":                             {"launcher", "Every import launcher", "workflow-jmeter-fidelity.spec.ts"},
	"/api/v1/scripts/import/k6":                                 {"launcher", "Every import launcher", "k6 import tests"},
	"/api/v1/scripts/import/openapi":                            {"launcher", "Every import launcher", "workflow-authoring.spec.ts"},
	"/api/v1/scripts/import/oracle-ebusiness/review":            {"launcher", "Every import launcher", "workflow-import-source.spec.ts"},
	"/api/v1/scripts/import/postman":                            {"launcher", "Every import launcher", "workflow-import-source.spec.ts"},
	"/api/v1/scripts/import/raml":                               {"launcher", "Every import launcher", "RAML import tests"},
	"/api/v1/scripts/import/recorder":                           {"launcher", "Recording", "workflow-recording-review.spec.ts"},
	"/api/v1/scripts/import/salesforce":                         {"launcher", "Every import launcher", "Salesforce import tests"},
	"/api/v1/scripts/import/sample":                             {"launcher", "Every import launcher", "sample/Java HTTP import tests"},
	"/api/v1/scripts/import/wsdl":                               {"launcher", "Every import launcher", "WSDL import tests"},
	"/api/v1/scripts/import/asyncapi/commit":                    {"commit", "Every import launcher", "AsyncAPI commit tests"},
	"/api/v1/scripts/import/asyncapi/commit-batch":              {"commit", "Multi-script imports", "AsyncAPI batch commit tests"},
	"/api/v1/scripts/import/bruno/commit":                       {"commit", "Multi-script imports", "tier0-bruno-import.spec.ts"},
	"/api/v1/scripts/import/commit-batch":                       {"commit", "Multi-script imports", "workflow-loadrunner-collection.spec.ts"},
	"/api/v1/scripts/import/enterprise-script-archive/save":     {"commit", "Multi-script imports", "workflow-loadrunner-review.spec.ts"},
	"/api/v1/scripts/import/jmeter/save":                        {"commit", "Every import launcher", "workflow-jmeter-fidelity.spec.ts"},
	"/api/v1/scripts/import/openapi/commit":                     {"commit", "Every import launcher", "workflow-authoring.spec.ts"},
	"/api/v1/scripts/import/postman/save":                       {"commit", "Every import launcher", "Postman import tests"},
	"/api/v1/scripts/import/enterprise-script-archive/validate": {"validate", "Every import launcher", "LoadRunner import validation tests"},
	"/api/v1/scripts/import/jmeter/validate":                    {"validate", "Every import launcher", "JMeter import validation tests"},
	"/api/v1/scripts/import/postman/validate":                   {"validate", "Every import launcher", "Postman import validation tests"},
	"/api/v1/scripts/import/recorder/validate":                  {"validate", "Recording", "workflow-recording-review.spec.ts"},
	"/api/v1/scripts/import/recorder/replay":                    {"validate", "Recording", "recorder replay tests"},
	"/api/v1/scripts/import/parameterize/apply":                 {"shared review", "Every import launcher", "import parameterization tests"},
	"/api/v1/scripts/import/repairs/apply":                      {"shared review", "Every import launcher", "workflow-jmeter-fidelity.spec.ts"},
	"/api/v1/scripts/import/repairs/preview":                    {"shared review", "Every import launcher", "workflow-jmeter-fidelity.spec.ts"},
	"/api/v1/scripts/import/shared-values/apply":                {"shared review", "Multi-script imports", "shared import values tests"},
	"/api/v1/scripts/import/secrets/migrate":                    {"shared review", "Template/component/clone", "protected credential migration tests"},
	"/api/v1/scripts/import/secrets/discard":                    {"shared review", "Template/component/clone", "protected credential migration tests"},
	"/api/v1/scripts/import/settings":                           {"shared review", "Every import launcher", "import runtime settings tests"},
	"/api/v1/scripts/import/browser-recorder/attest":            {"assets", "Recording", "browser recorder attestation tests"},
	"/api/v1/scripts/import/{id}/assets/upload":                 {"assets", "Every import launcher", "import asset upload tests"},
	"/api/v1/scripts/import/{id}/java-classpath/upload":         {"assets", "Every import launcher", "Java classpath upload tests"},
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
			"workflow-loadrunner-collection.spec.ts": {"reviews and saves nested VuGen projects independently, retaining failed and Java reviews"},
			"workflow-loadrunner-review.spec.ts":     {"LoadRunner batch saves ready scripts, stays on import, and accepts another ZIP"},
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

// TestLifecycleParityRowsRunInBothProfiles checks every AUTH-2 row against
// the spec table, the named Playwright tests and the local-auth profile.
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
	config, err := os.ReadFile("../../dashboard/playwright.config.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range lifecycleAuth2Rows() {
		if len(row.E2E) == 0 {
			t.Errorf("AUTH-2 row %q has no Playwright proof", row.Entry)
		}
		for file, titles := range row.E2E {
			source, err := os.ReadFile(filepath.Join("../../dashboard/e2e", file))
			if err != nil {
				t.Errorf("AUTH-2 row %q names %s: %v", row.Entry, file, err)
				continue
			}
			if !strings.Contains(string(config), "'**/"+file+"'") {
				t.Errorf("%s (AUTH-2 row %q) does not run in the local-auth profile", file, row.Entry)
			}
			for _, title := range titles {
				if !strings.Contains(string(source), "test('"+title+"'") {
					t.Errorf("%s has no test %q (AUTH-2 row %q)", file, title, row.Entry)
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
	out.WriteString("Every entry point reports exactly the fixture's static findings (`validateScript`). Routes that refuse a script answer `400` with them, and validation routes answer `200` with them. Routes that would execute a clean script are exercised with invalid fixtures only. Contextual runtime, install, policy and approval outcomes are not static findings and are classified apart.\n\n")
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
	out.WriteString("\n## Import routes\n\nEvery script import route that accepts a POST has a row; a route without one fails `TestLifecycleParityImportLaunchersAreInventoried`.\n\n")
	out.WriteString("| Route | Role | AUTH-2 row | Proof |\n| --- | --- | --- | --- |\n")
	paths := make([]string, 0, len(lifecycleImportRoutes))
	for path := range lifecycleImportRoutes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		route := lifecycleImportRoutes[path]
		out.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n", path, route.Role, route.Row, route.Proof))
	}
	out.WriteString("\n## AUTH-2 rows\n\nEach row's Playwright tests run in both e2e profiles (default and local-auth).\n\n| AUTH-2 row | Playwright | Go |\n| --- | --- | --- |\n")
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
