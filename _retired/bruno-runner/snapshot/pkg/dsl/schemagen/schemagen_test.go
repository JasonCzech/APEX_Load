package schemagen

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const repoRoot = "../../.."

// TestGeneratedSchemaIsFresh fails when docs/dsl.schema.json no longer
// matches the catalog — run `go run ./cmd/dslschema` to regenerate.
func TestGeneratedSchemaIsFresh(t *testing.T) {
	generated, err := GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	generated = append(generated, '\n')
	committed, err := os.ReadFile(filepath.Join(repoRoot, "docs", "dsl.schema.json"))
	if err != nil {
		t.Fatalf("read committed schema: %v (run `go run ./cmd/dslschema`)", err)
	}
	if !bytes.Equal(generated, committed) {
		t.Fatal("docs/dsl.schema.json is stale — run `go run ./cmd/dslschema`")
	}
}

// TestGeneratedReferenceIsFresh fails when docs/DSL_ACTION_REFERENCE.md no
// longer matches the catalog — run `go run ./cmd/dslschema` to regenerate.
func TestGeneratedReferenceIsFresh(t *testing.T) {
	generated := GenerateReference()
	committed, err := os.ReadFile(filepath.Join(repoRoot, "docs", "DSL_ACTION_REFERENCE.md"))
	if err != nil {
		t.Fatalf("read committed reference: %v (run `go run ./cmd/dslschema`)", err)
	}
	if !bytes.Equal(generated, committed) {
		t.Fatal("docs/DSL_ACTION_REFERENCE.md is stale — run `go run ./cmd/dslschema`")
	}
}

// TestGeneratedSchemaCompiles asserts the emitted document is a valid JSON
// Schema 2020-12.
func TestGeneratedSchemaCompiles(t *testing.T) {
	if _, err := compileGeneratedSchema(t); err != nil {
		t.Fatalf("generated schema does not compile: %v", err)
	}
}

func TestSAPGUIArtifactsDiscloseExternalRunnerRequirement(t *testing.T) {
	reference := string(GenerateReference())
	if !strings.Contains(reference, "Execution posture: `external_runner_required`") ||
		!strings.Contains(reference, "not executable in the bundled runtime") {
		t.Fatal("SAP GUI reference does not disclose its external runner requirement")
	}
	schema, err := GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(schema, []byte(`"x-execution-posture": "external_runner_required"`)) {
		t.Fatal("SAP GUI schema does not carry execution posture")
	}
}

func TestCatalogEnumChoicesAreSchemaExamplesNotUnenforcedConstraints(t *testing.T) {
	schema := fieldSchema(dsl.FieldSpec{Kind: dsl.FieldKindEnum, Enum: []string{"left", "right"}})
	if _, closed := schema["enum"]; closed {
		t.Fatalf("unenforced catalog choices were emitted as a closed enum: %#v", schema)
	}
	examples, ok := schema["examples"].([]any)
	if !ok || len(examples) != 2 {
		t.Fatalf("catalog choices were not retained as examples: %#v", schema)
	}
	closed := fieldSchema(dsl.FieldSpec{Kind: dsl.FieldKindEnum, Enum: []string{"accept", "dismiss"}, SchemaEnum: true})
	if values, ok := closed["enum"].([]any); !ok || len(values) != 2 {
		t.Fatalf("validator-enforced choices were not emitted as a closed enum: %#v", closed)
	}
}

func TestGeneratedSchemaPreservesOpenAliasesAndClosedRuntimeEnums(t *testing.T) {
	schema, err := compileGeneratedSchema(t)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		protocol    string
		config      map[string]any
		action      map[string]any
		wantInvalid bool
	}{
		{
			name:     "browser button compatibility alias remains accepted",
			protocol: "browser",
			action: map[string]any{
				"name": "click", "type": "browser_click", "selector": "#submit",
				"params": map[string]any{"button": "primary"},
			},
		},
		{
			name:     "blank browser button default remains accepted",
			protocol: "browser",
			action: map[string]any{
				"name": "click", "type": "browser_click", "selector": "#submit",
				"params": map[string]any{"button": " "},
			},
		},
		{
			name:     "blank navigation default remains accepted",
			protocol: "browser",
			action: map[string]any{
				"name": "navigate", "type": "browser_navigate", "target": "https://example.test",
				"params": map[string]any{"wait_until": " "},
			},
		},
		{
			name:     "normalized dialog choices remain accepted",
			protocol: "browser",
			action: map[string]any{
				"name": "dialog", "type": "browser_handle_dialog",
				"params": map[string]any{"action": " ACCEPT ", "match_mode": " Exact "},
			},
		},
		{
			name:     "blank dialog match mode remains accepted",
			protocol: "browser",
			action: map[string]any{
				"name": "dialog", "type": "browser_handle_dialog",
				"params": map[string]any{"action": "accept", "match_mode": " "},
			},
		},
		{
			name:     "blank Bruno defaults remain accepted",
			protocol: "bruno",
			config: map[string]any{
				"bruno": map[string]any{
					"bundle_ref": "asset://collection", "bundle_sha256": strings.Repeat("a", 64), "sandbox": "safe",
				},
			},
			action: map[string]any{
				"name": "run", "type": "bruno_run",
				"params": map[string]any{"selection": " ", "execution": ""},
			},
		},
		{
			name:     "dialog action is validator-enforced",
			protocol: "browser",
			action: map[string]any{
				"name": "dialog", "type": "browser_handle_dialog",
				"params": map[string]any{"action": "approve"},
			},
			wantInvalid: true,
		},
		{
			name:        "dialog action remains required",
			protocol:    "browser",
			action:      map[string]any{"name": "dialog", "type": "browser_handle_dialog", "params": map[string]any{}},
			wantInvalid: true,
		},
		{
			name:     "blank WebSocket message type uses its default",
			protocol: "websocket",
			config:   map[string]any{"base_url": "ws://example.test"},
			action: map[string]any{
				"name": "send", "type": "ws_send", "body": "payload",
				"params": map[string]any{"type": " "},
			},
		},
		{
			name:     "websocket message type is validator-enforced",
			protocol: "websocket",
			action: map[string]any{
				"name": "send", "type": "ws_send", "body": "payload",
				"params": map[string]any{"type": "bogus"},
			},
			wantInvalid: true,
		},
		{
			name:     "blank DNS record type uses its default",
			protocol: "network",
			action: map[string]any{
				"name": "lookup", "type": "dns_query", "target": "example.test",
				"params": map[string]any{"record_type": " "},
			},
		},
		{
			name:     "blank JMS message type uses its default",
			protocol: "jms-solace",
			config: map[string]any{
				"jms": map[string]any{
					"provider": "solace", "destination": "orders.in", "destination_type": "queue", "mock": true,
				},
			},
			action: map[string]any{
				"name": "publish", "type": "jms_publish", "target": "orders.in", "body": "payload",
				"params": map[string]any{"message_type": " "},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := map[string]any{
				"name": "enum schema contract", "protocol": test.protocol, "actions": []any{test.action},
			}
			if test.config != nil {
				document["config"] = test.config
			}
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var script dsl.Script
			if err := json.Unmarshal(raw, &script); err != nil {
				t.Fatal(err)
			}
			issues := dsl.ValidateScriptIssues(&script)
			if test.wantInvalid && len(issues) == 0 {
				t.Fatal("runtime validator accepted the deliberately invalid enum")
			}
			if !test.wantInvalid && len(issues) != 0 {
				t.Fatalf("runtime validator rejected a supported value: %v", issues)
			}
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(instance)
			if test.wantInvalid && err == nil {
				t.Fatal("schema accepted a value rejected by the runtime validator")
			}
			if !test.wantInvalid && err != nil {
				t.Fatalf("schema rejected a runtime-supported alias: %v", err)
			}
		})
	}
}

func TestGeneratedSchemaHTTPVersionMatchesRuntimeContract(t *testing.T) {
	schema, err := compileGeneratedSchema(t)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		version     *string
		wantInvalid bool
	}{
		{name: "omitted auto negotiation"},
		{name: "explicit empty auto negotiation", version: stringPointer("")},
		{name: "exact HTTP 1.1", version: stringPointer("1.1")},
		{name: "required HTTP 2", version: stringPointer("2")},
		{name: "HTTP 1.0 is unsupported", version: stringPointer("1.0"), wantInvalid: true},
		{name: "unknown version is unsupported", version: stringPointer("3"), wantInvalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpConfig := map[string]any{}
			if test.version != nil {
				httpConfig["version"] = *test.version
			}
			script := map[string]any{
				"name":     "HTTP version schema contract",
				"protocol": "http",
				"config": map[string]any{
					"http": httpConfig,
				},
				"actions": []any{
					map[string]any{
						"name":   "request",
						"type":   "GET",
						"target": "https://example.test",
					},
				},
			}
			raw, err := json.Marshal(script)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(instance)
			if test.wantInvalid && err == nil {
				t.Fatal("schema accepted an unsupported HTTP version")
			}
			if !test.wantInvalid && err != nil {
				t.Fatalf("schema rejected a supported HTTP version: %v", err)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

// TestExampleScriptsPassGoValidatorAndSchema is the dual-validation drift
// guard: every example script must satisfy both the Go validator and the
// generated JSON Schema. Because the schema is generated looser-or-equal to
// the validator, a schema rejection here means the catalog and validator
// have drifted apart.
func TestExampleScriptsPassGoValidatorAndSchema(t *testing.T) {
	schema, err := compileGeneratedSchema(t)
	if err != nil {
		t.Fatal(err)
	}

	pattern := filepath.Join(repoRoot, "examples", "*.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no example scripts matched %s", pattern)
	}

	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// Examples may wrap the script in a {"script": ...} envelope
			// (the POST /api/v1/scripts request shape).
			var envelope struct {
				Script json.RawMessage `json:"script"`
			}
			scriptJSON := raw
			if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Script) > 0 {
				scriptJSON = envelope.Script
			}

			// Some examples are test-run requests (groups, duration,
			// runtime_settings) rather than script documents — skip those.
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(scriptJSON, &probe); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if _, hasProtocol := probe["protocol"]; !hasProtocol {
				if _, hasActions := probe["actions"]; !hasActions {
					t.Skipf("%s is not a script document", filepath.Base(path))
				}
			}

			var script dsl.Script
			if err := json.Unmarshal(scriptJSON, &script); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
				t.Errorf("Go validator issues: %v", issues)
			}

			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(scriptJSON))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Errorf("schema validation: %v", err)
			}
		})
	}
}

func compileGeneratedSchema(t *testing.T) (*jsonschema.Schema, error) {
	t.Helper()
	generated, err := GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(generated))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("dsl.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	return compiler.Compile("dsl.schema.json")
}
