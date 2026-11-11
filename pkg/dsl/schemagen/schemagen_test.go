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

// TestGeneratedReferenceTablesKeepTheirColumns fails when a field name or
// description writes an unescaped pipe, which splits its row into extra cells.
func TestGeneratedReferenceTablesKeepTheirColumns(t *testing.T) {
	for number, line := range strings.Split(string(GenerateReference()), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		row := strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
		if cells := strings.Count(row, "|") - strings.Count(row, "\\|") + 1; cells != 4 {
			t.Errorf("line %d has %d cells, want 4: %s", number+1, cells, line)
		}
	}
}

// TestGeneratedSchemaCompiles asserts the emitted document is a valid JSON
// Schema 2020-12.
func TestGeneratedSchemaCompiles(t *testing.T) {
	if _, err := compileGeneratedSchema(t); err != nil {
		t.Fatalf("generated schema does not compile: %v", err)
	}
}

func TestRetiredBrunoConfigurationIsNotAdvertised(t *testing.T) {
	schema, err := GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{`"BrunoConfig"`, `"BrunoRuntimeConfig"`, `"BrunoRuntimeLimits"`, `"bruno"`, `"bruno_run"`} {
		if bytes.Contains(schema, []byte(retired)) {
			t.Fatalf("generated schema still advertises %s", retired)
		}
	}
	var legacy dsl.Script
	if err := json.Unmarshal([]byte(`{"protocol":"bruno","config":{"bruno":{"bundle_ref":"artifact://retained","sandbox":"safe"}}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Config.Bruno.BundleRef != "artifact://retained" || legacy.Config.Bruno.Sandbox != "safe" {
		t.Fatal("retired configuration no longer decodes from saved records")
	}
}

func TestRetiredBrunoSchemaAcceptsSerializedNativeScript(t *testing.T) {
	schema, err := compileGeneratedSchema(t)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dsl.Script{
		Name: "Native request", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Health", Type: "GET", Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatalf("retired configuration omission broke normal script serialization: %v", err)
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
		{name: "upgrade to HTTP 3", version: stringPointer("auto+h3")},
		{name: "required HTTP 3", version: stringPointer("3")},
		{name: "unknown version is unsupported", version: stringPointer("h3-29"), wantInvalid: true},
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

func TestLLMRequestDialectAliasesAndStreamProfileSchemaParity(t *testing.T) {
	schema, err := compileGeneratedSchema(t)
	if err != nil {
		t.Fatalf("compileGeneratedSchema: %v", err)
	}

	allDialects := append([]string{}, dsl.LLMDialectNames...)
	allDialects = append(allDialects, dsl.LLMDialectAliases...)

	for _, dialect := range allDialects {
		doc := map[string]any{
			"name":     "llm-alias-" + dialect,
			"protocol": "http",
			"config":   map[string]any{"base_url": "https://api.example.com"},
			"actions": []map[string]any{
				{
					"name":   "infer",
					"type":   "llm_request",
					"target": "/v1/chat/completions",
					"params": map[string]any{
						"dialect": dialect,
						"prompt":  "Hello",
					},
				},
			},
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal %s: %v", dialect, err)
		}
		var script dsl.Script
		if err := json.Unmarshal(raw, &script); err != nil {
			t.Fatalf("unmarshal %s: %v", dialect, err)
		}
		if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
			t.Errorf("dialect %q failed ValidateScriptIssues: %v", dialect, issues)
		}
		instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("UnmarshalJSON %s: %v", dialect, err)
		}
		if err := schema.Validate(instance); err != nil {
			t.Errorf("dialect %q failed JSON Schema validation: %v", dialect, err)
		}
	}

	// Custom stream_profile object + llm_request inside http_sequence.
	customDoc := map[string]any{
		"name":     "llm-custom-stream-profile-and-sequence",
		"protocol": "http",
		"config":   map[string]any{"base_url": "https://api.example.com"},
		"actions": []map[string]any{
			{
				"name":   "custom_stream",
				"type":   "llm_request",
				"target": "/v1/stream",
				"params": map[string]any{
					"dialect": "custom",
					"prompt":  "Hello",
					"stream_profile": map[string]any{
						"preset":      "openai_chat",
						"delta_path":  "$.choices[0].delta.content",
						"finish_path": "$.choices[0].finish_reason",
						"usage_path":  "$.usage",
					},
				},
			},
			{
				"name": "rag_sequence",
				"type": "http_sequence",
				"params": map[string]any{
					"requests": []map[string]any{
						{
							"name":   "hop1_embed",
							"type":   "llm_request",
							"target": "/v1/chat/completions",
							"params": map[string]any{
								"dialect": "azure_openai_chat",
								"prompt":  "Summarize context",
							},
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(customDoc)
	if err != nil {
		t.Fatal(err)
	}
	var script dsl.Script
	if err := json.Unmarshal(raw, &script); err != nil {
		t.Fatal(err)
	}
	if issues := dsl.ValidateScriptIssues(&script); len(issues) > 0 {
		t.Fatalf("custom stream_profile / http_sequence llm_request failed ValidateScriptIssues: %v", issues)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatalf("custom stream_profile / http_sequence llm_request failed JSON Schema validation: %v", err)
	}

	// Verify conversation object/bool, stream_mode, and extraction aliases pass both Go and JSON Schema validation.
	convDoc := map[string]any{
		"name":     "llm-conversation-and-aliases",
		"protocol": "http",
		"config":   map[string]any{"base_url": "https://api.example.com"},
		"actions": []map[string]any{
			{
				"name":   "conv_turn",
				"type":   "llm_request",
				"target": "/v1/chat/completions",
				"params": map[string]any{
					"stream_mode":           "sse",
					"system_prompt":         "You are helpful.",
					"ignore_eos":            true,
					"golden_prompt_id":      "gp-1",
					"save_as":               "reply_text",
					"save_finish_reason_as": "fin_reason",
					"save_tool_calls_as":    "tool_calls",
					"conversation": map[string]any{
						"history_var":         "chat_hist",
						"max_turns":           4,
						"max_context_tokens":  2048,
						"max_history_bytes":   16384,
						"truncation_strategy": "sliding_window",
					},
				},
			},
			{
				"name":   "conv_bool_turn",
				"type":   "llm_request",
				"target": "/v1/chat/completions",
				"params": map[string]any{
					"conversation": true,
				},
			},
		},
	}
	convRaw, err := json.Marshal(convDoc)
	if err != nil {
		t.Fatal(err)
	}
	var convScript dsl.Script
	if err := json.Unmarshal(convRaw, &convScript); err != nil {
		t.Fatal(err)
	}
	if issues := dsl.ValidateScriptIssues(&convScript); len(issues) > 0 {
		t.Fatalf("conversation llm_request failed ValidateScriptIssues: %v", issues)
	}
	convInstance, err := jsonschema.UnmarshalJSON(bytes.NewReader(convRaw))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(convInstance); err != nil {
		t.Fatalf("conversation llm_request failed JSON Schema validation: %v", err)
	}

	// Verify invalid conversation config is rejected by ValidateScriptIssues.
	badConvScript := dsl.Script{
		Name:     "bad-conv",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{
			{
				Name:   "bad_turn",
				Type:   "llm_request",
				Target: "/v1/chat/completions",
				Params: map[string]any{
					"prompt": "hi",
					"conversation": map[string]any{
						"max_turns":           -1,
						"max_history_bytes":   999999,
						"truncation_strategy": "invalid_strategy",
					},
				},
			},
		},
	}
	badIssues := dsl.ValidateScriptIssues(&badConvScript)
	if len(badIssues) < 3 {
		t.Fatalf("expected >= 3 validation issues for invalid conversation config, got %v", badIssues)
	}
}

func TestTransactionSLARuleSchemaMirrorsContract(t *testing.T) {
	raw, err := GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	defs, _ := doc["$defs"].(map[string]any)
	rule, ok := defs["TransactionSLARule"].(map[string]any)
	if !ok {
		t.Fatal("missing TransactionSLARule in $defs")
	}

	gotRequired := toStringSlice(t, rule["required"])
	wantRequired := []string{"transaction", "metric", "comparator", "threshold"}
	if strings.Join(gotRequired, ",") != strings.Join(wantRequired, ",") {
		t.Fatalf("TransactionSLARule.required = %v, want %v", gotRequired, wantRequired)
	}

	properties, ok := rule["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing TransactionSLARule.properties")
	}
	wantEnums := map[string][]string{
		"phase":      {"dns", "connect", "tls", "server", "download"},
		"metric":     {"average_duration_ms", "bytes_received", "bytes_sent", "error_count", "error_rate_pct", "goodput_pct", "max_acknowledgement_ms", "max_consumer_lag_ms", "max_consumer_offset_lag", "max_duration_ms", "min_duration_ms", "minimum_count", "output_tokens_per_sec", "p50_ttft_ms", "p90_duration_ms", "p90_tpot_ms", "p90_ttft_ms", "p95_duration_ms", "p95_itl_ms", "p95_tpot_ms", "p95_ttft_ms", "p99_duration_ms", "p99_itl_ms", "p99_tpot_ms", "p99_ttft_ms", "throttle_rate_pct", "tps", "truncation_rate_pct"},
		"comparator": {"lt", "lte", "gt", "gte", "eq", "neq"},
		"population": {"transaction", "action"},
		"severity":   {"fail", "warning"},
		"iterations": {"warm", "all", "cold"},
	}
	for field, want := range wantEnums {
		prop, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("missing property %q on TransactionSLARule", field)
		}
		got := toStringSlice(t, prop["enum"])
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("TransactionSLARule.properties.%s.enum = %v, want %v", field, got, want)
		}
	}

	schema, err := compileGeneratedSchema(t)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		rule        map[string]any
		wantInvalid bool
	}{
		{
			name: "valid full rule",
			rule: map[string]any{
				"transaction": "Checkout", "phase": "connect", "metric": "p95_duration_ms",
				"comparator": "lte", "threshold": 250, "severity": "fail",
				"population": "transaction", "iterations": "warm",
			},
		},
		{
			name:        "missing required comparator",
			rule:        map[string]any{"transaction": "Checkout", "metric": "p95_duration_ms", "threshold": 250},
			wantInvalid: true,
		},
		{
			name:        "invalid phase enum",
			rule:        map[string]any{"transaction": "Checkout", "phase": "ttfb", "metric": "p95_duration_ms", "comparator": "lte", "threshold": 250},
			wantInvalid: true,
		},
		{
			name:        "invalid metric enum",
			rule:        map[string]any{"transaction": "Checkout", "metric": "median_ms", "comparator": "lte", "threshold": 250},
			wantInvalid: true,
		},
		{
			name:        "invalid comparator enum",
			rule:        map[string]any{"transaction": "Checkout", "metric": "p95_duration_ms", "comparator": "<=", "threshold": 250},
			wantInvalid: true,
		},
		{
			name:        "invalid population enum",
			rule:        map[string]any{"transaction": "Checkout", "metric": "p95_duration_ms", "comparator": "lte", "threshold": 250, "population": "step"},
			wantInvalid: true,
		},
		{
			name:        "invalid severity enum",
			rule:        map[string]any{"transaction": "Checkout", "metric": "p95_duration_ms", "comparator": "lte", "threshold": 250, "severity": "critical"},
			wantInvalid: true,
		},
		{
			name:        "invalid iterations enum",
			rule:        map[string]any{"transaction": "Checkout", "metric": "p95_duration_ms", "comparator": "lte", "threshold": 250, "iterations": "first"},
			wantInvalid: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scriptDoc := map[string]any{
				"name":        "sla-rule-test",
				"protocol":    "http",
				"goal_config": map[string]any{"transaction_rules": []any{tc.rule}},
				"actions":     []any{map[string]any{"name": "req", "type": "GET", "target": "https://example.test"}},
			}
			encoded, err := json.Marshal(scriptDoc)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(instance)
			if tc.wantInvalid && err == nil {
				t.Fatalf("expected schema rejection for %v", tc.rule)
			}
			if !tc.wantInvalid && err != nil {
				t.Fatalf("unexpected schema rejection: %v", err)
			}
		})
	}
}

func toStringSlice(t *testing.T, raw any) []string {
	t.Helper()
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T (%#v)", raw, raw)
	}
	out := make([]string, len(items))
	for i, item := range items {
		str, ok := item.(string)
		if !ok {
			t.Fatalf("item %d: expected string, got %T", i, item)
		}
		out[i] = str
	}
	return out
}
