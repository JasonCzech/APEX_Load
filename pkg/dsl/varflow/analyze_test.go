package varflow

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// readinessCase is one entry of testdata/readiness_cases.json. The dashboard's
// TypeScript port of Analyze reads the same file, so both sides are held to
// the same expectations.
type readinessCase struct {
	Name     string            `json:"name"`
	Script   dsl.Script        `json:"script"`
	Seeded   map[string]string `json:"seeded,omitempty"`
	Expected struct {
		Missing      []string `json:"missing"`
		Blocking     []string `json:"blocking"`
		LateProduced []string `json:"late_produced"`
		// ParamBlocking and ConfigBlocking are empty unless a case lists them.
		ParamBlocking  []string `json:"param_blocking,omitempty"`
		ConfigBlocking []string `json:"config_blocking,omitempty"`
	} `json:"expected"`
}

func loadReadinessCases(t *testing.T) []readinessCase {
	t.Helper()
	payload, err := os.ReadFile("testdata/readiness_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var cases []readinessCase
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("decode readiness corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("readiness corpus is empty")
	}
	return cases
}

func TestAnalyzeGolden(t *testing.T) {
	t.Parallel()
	names := make(map[string]bool)
	for _, test := range loadReadinessCases(t) {
		test := test
		if names[test.Name] {
			t.Fatalf("duplicate corpus case %q", test.Name)
		}
		names[test.Name] = true
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()
			got := Analyze(&test.Script, test.Seeded)
			for _, check := range []struct {
				field     string
				got, want []string
			}{
				{"missing", got.Missing, test.Expected.Missing},
				{"blocking", got.Blocking, test.Expected.Blocking},
				{"late_produced", got.LateProduced, test.Expected.LateProduced},
				{"param_blocking", got.ParamBlocking, test.Expected.ParamBlocking},
				{"config_blocking", got.ConfigBlocking, test.Expected.ConfigBlocking},
			} {
				if !reflect.DeepEqual(nonNil(check.got), nonNil(check.want)) {
					t.Errorf("%s = %#v, want %#v (by action %#v)", check.field, check.got, check.want, got.ByAction)
				}
			}
			union := make(map[string]struct{})
			for _, names := range got.ByAction {
				for _, name := range names {
					union[name] = struct{}{}
				}
			}
			if !reflect.DeepEqual(nonNil(sortedStringSet(union)), nonNil(got.Missing)) {
				t.Errorf("missing %#v is not the union of by action %#v", got.Missing, got.ByAction)
			}
			if fromMisses := missesByAction(got.Misses); !reflect.DeepEqual(fromMisses, nonNilByAction(got.ByAction)) {
				t.Errorf("misses %#v do not match by action %#v", got.Misses, got.ByAction)
			}
		})
	}
}

// Misses carry what ByAction folds away: the namespace of each use and
// whether that use, not just the name, refuses replay.
func TestAnalyzeMissesKeepNamespaceAndBlockingPerUse(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://{{var.host}}"}, Actions: []dsl.Action{
		{Type: "request", Method: "POST", Target: "https://example.test/", Body: "{{var.token}} {{param.token}}"},
		{Type: "source_json", Params: map[string]any{"function": "lr_eval_json", "json_object": "doc", "buffer": "{{var.payload}}"}},
		{Type: "request", Method: "GET", Target: "/{{var.payload}}", Extractors: []dsl.Extractor{{Name: "token"}}},
	}}
	got := Analyze(script, nil)
	want := []Miss{
		{Path: "actions[0]", Reference: Reference{Namespace: NamespaceParam, Name: "token"}, Blocking: true},
		{Path: "actions[0]", Reference: Reference{Namespace: NamespaceVar, Name: "token"}},
		{Path: "actions[1]", Reference: Reference{Namespace: NamespaceVar, Name: "payload"}, Blocking: true},
		{Path: "actions[2]", Reference: Reference{Namespace: NamespaceVar, Name: "payload"}},
		{Path: "actions[2]", Reference: Reference{Namespace: NamespaceVar, Name: "host"}, Blocking: true, Config: true},
	}
	if !reflect.DeepEqual(got.Misses, want) {
		t.Fatalf("misses = %#v\nwant %#v", got.Misses, want)
	}
	if !reflect.DeepEqual(got.Blocking, []string{"host", "payload", "token"}) || !reflect.DeepEqual(got.LateProduced, []string{"token"}) {
		t.Fatalf("blocking = %#v, late produced = %#v", got.Blocking, got.LateProduced)
	}
	// token is late produced through its {{var.*}} use, yet only a declared
	// parameter clears its {{param.*}} use.
	if !reflect.DeepEqual(got.ParamBlocking, []string{"token"}) || !reflect.DeepEqual(got.ConfigBlocking, []string{"host"}) {
		t.Fatalf("param blocking = %#v, config blocking = %#v", got.ParamBlocking, got.ConfigBlocking)
	}
}

// A Java HTTP request keeps the placeholders the HTTP adapter sends, so only
// a use in its stored body, which is decoded as base64 first, blocks, and the
// miss says why. A name both uses share blocks through the body use alone;
// an engine step's body never reaches the adapter.
func TestAnalyzeMissesMarkJavaHTTPBodyUses(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Protocol: "java_http", Actions: []dsl.Action{
		{Type: "request", Method: "POST", Target: "/invoker/{{var.bean}}", Headers: map[string]string{"X-Session": "{{var.session}}"}, Body: "{{var.serialized}}"},
		{Type: "request", Method: "POST", Target: "/invoker/{{var.serialized}}", Body: "b3BhcXVlLWphdmEtb2JqZWN0"},
		{Type: "request", Method: "POST", Target: "/invoker", Body: "c2VyOnt7dmFyLm9yZGVyX3JlZn19", BodyEncoding: "base64"},
		{Type: "source_message", Params: map[string]any{"function": "lr_output_message"}, Body: "{{var.note}}"},
	}}
	got := Analyze(script, nil)
	want := []Miss{
		{Path: "actions[0]", Reference: Reference{Namespace: NamespaceVar, Name: "bean"}},
		{Path: "actions[0]", Reference: Reference{Namespace: NamespaceVar, Name: "serialized"}, Blocking: true, Base64Body: true},
		{Path: "actions[0]", Reference: Reference{Namespace: NamespaceVar, Name: "session"}},
		{Path: "actions[1]", Reference: Reference{Namespace: NamespaceVar, Name: "serialized"}},
		{Path: "actions[2]", Reference: Reference{Namespace: NamespaceVar, Name: "order_ref"}},
		{Path: "actions[3]", Reference: Reference{Namespace: NamespaceVar, Name: "note"}},
	}
	if !reflect.DeepEqual(got.Misses, want) {
		t.Fatalf("misses = %#v\nwant %#v", got.Misses, want)
	}
	if !reflect.DeepEqual(got.Blocking, []string{"serialized"}) {
		t.Fatalf("blocking = %#v", got.Blocking)
	}
}

func missesByAction(misses []Miss) map[string][]string {
	byAction := make(map[string][]string)
	for _, miss := range misses {
		byAction[miss.Path] = append(byAction[miss.Path], miss.Reference.Name)
	}
	for path, names := range byAction {
		byAction[path] = uniqueSortedStrings(names)
	}
	return byAction
}

func nonNilByAction(byAction map[string][]string) map[string][]string {
	if byAction == nil {
		return map[string][]string{}
	}
	return byAction
}

// Every branch of the walk needs a corpus case, because the corpus is the
// only guard that holds the TypeScript port to this implementation.
func TestReadinessCorpusCoversEveryWalkBranch(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	var visit func(actions []dsl.Action)
	visit = func(actions []dsl.Action) {
		for _, action := range actions {
			seen[dsl.CanonicalActionType(action.Type)] = true
			if len(action.Extractors) > 0 {
				seen["extractors"] = true
			}
			if action.BodyEncoding != "" {
				seen["body_encoding"] = true
			}
			if action.Control == nil {
				continue
			}
			visit(action.Control.Steps)
			visit(action.Control.Else)
			visit(action.Control.Default)
			for _, switchCase := range action.Control.Cases {
				visit(switchCase.Steps)
			}
			for _, branch := range action.Control.Branches {
				visit(branch.Steps)
			}
			if action.Control.ForEach != nil {
				seen["for_each"] = true
			}
			if action.Control.DoWhile {
				seen["do_while"] = true
			}
		}
	}
	for _, test := range loadReadinessCases(t) {
		visit(test.Script.Init)
		visit(test.Script.Actions)
		visit(test.Script.Teardown)
		if test.Script.Config.BaseURL != "" {
			seen["base_url"] = true
		}
		if len(test.Script.Parameters) > 0 {
			seen["parameters"] = true
		}
		if len(test.Seeded) > 0 {
			seen["seeded"] = true
		}
		if strings.Contains(mustJSON(t, test.Script), "{{param.") {
			seen["param_namespace"] = true
		}
		if len(test.Expected.LateProduced) > 0 {
			seen["late_produced"] = true
		}
		if len(test.Expected.ParamBlocking) > 0 {
			seen["param_blocking"] = true
		}
		if len(test.Expected.ConfigBlocking) > 0 {
			seen["config_blocking"] = true
		}
	}
	for _, required := range []string{
		"control_if", "control_switch", "control_loop", "control_parallel", "http_sequence",
		"source_parameter", "source_json", "source_message", "source_review", "source_external",
		"custom_call", "source_database", "http_oauth2_token", "browser_evaluate_js",
		"native_javascript", "live_table", "extractors", "body_encoding", "for_each",
		"do_while", "base_url", "parameters", "seeded", "param_namespace", "late_produced",
		"param_blocking", "config_blocking", "kafka_produce", "kafka_request_reply",
		"jms_publish", "jms_request_reply",
	} {
		if !seen[required] {
			t.Errorf("readiness corpus has no case for %s", required)
		}
	}
}

// A {{param.*}} in metadata is a read only where the runtime hands the step
// to its protocol adapter. The TypeScript port lists the engine steps itself,
// so every engine step type needs a corpus action whose metadata holds a
// {{param.*}}: the golden expectations then hold that list to the catalog.
func TestReadinessCorpusCoversEveryEngineStepMetadata(t *testing.T) {
	t.Parallel()
	covered := make(map[string]bool)
	var visit func(actions []dsl.Action)
	visit = func(actions []dsl.Action) {
		for _, action := range actions {
			if strings.Contains(mustJSON(t, action.Metadata), "{{param.") {
				covered[dsl.CanonicalActionType(action.Type)] = true
			}
			if action.Control == nil {
				continue
			}
			for _, branch := range action.Control.ActionBranches() {
				visit(branch.Actions)
			}
		}
	}
	for _, test := range loadReadinessCases(t) {
		visit(test.Script.Init)
		visit(test.Script.Actions)
		visit(test.Script.Teardown)
	}
	// handleEngineAction also runs the HTTP OAuth token step.
	required := []string{"http_oauth2_token"}
	for _, spec := range dsl.AllActionSpecs() {
		if spec.EngineAction() {
			required = append(required, spec.Type)
			required = append(required, spec.Aliases...)
		}
	}
	for _, actionType := range required {
		if !covered[actionType] {
			t.Errorf("readiness corpus has no %s step with a {{param.*}} in its metadata", actionType)
		}
	}
}

func TestAnalyzeReportsStableActionPaths(t *testing.T) {
	t.Parallel()
	use := func(ref string) dsl.Action {
		return dsl.Action{Type: "request", Method: "GET", Target: "/{{var." + ref + "}}"}
	}
	condition := &dsl.Condition{Left: "a", Op: "eq", Right: "a"}
	script := &dsl.Script{
		Protocol: "http",
		Init:     []dsl.Action{use("in_init")},
		Actions: []dsl.Action{
			{Type: "control_if", Control: &dsl.ControlFlow{Condition: condition, Steps: []dsl.Action{use("in_then")}, Else: []dsl.Action{use("in_else")}}},
			{Type: "control_switch", Control: &dsl.ControlFlow{Cases: []dsl.SwitchCase{{Condition: condition, Steps: []dsl.Action{use("in_case")}}}, Default: []dsl.Action{use("in_default")}}},
			{Type: "control_loop", Control: &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{use("in_loop")}}},
			{Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "a", Steps: []dsl.Action{use("in_branch")}}}}},
			{Type: "http_sequence", Method: "SEQUENCE", Target: "/", Params: map[string]any{"requests": []any{
				map[string]any{"type": "GET", "method": "GET", "target": "/{{var.in_child}}"},
			}}},
		},
		Teardown: []dsl.Action{use("in_teardown")},
	}
	got := Analyze(script, nil)
	want := map[string][]string{
		"init[0]":                                 {"in_init"},
		"actions[0].control.steps[0]":             {"in_then"},
		"actions[0].control.else[0]":              {"in_else"},
		"actions[1].control.cases[0].steps[0]":    {"in_case"},
		"actions[1].control.default[0]":           {"in_default"},
		"actions[2].control.steps[0]":             {"in_loop"},
		"actions[3].control.branches[0].steps[0]": {"in_branch"},
		"actions[4].params.requests[0]":           {"in_child"},
		"teardown[0]":                             {"in_teardown"},
	}
	if !reflect.DeepEqual(got.ByAction, want) {
		t.Fatalf("by action = %#v\nwant %#v", got.ByAction, want)
	}
}

// An http_sequence wrapper sends no request of its own, so neither the target
// an LR transaction copies from its first request nor the HTTP configuration
// is charged to it: a fix to the child clears every use.
func TestAnalyzeChargesSequenceRequestsToTheChildOnly(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://{{var.host}}", HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{"X-Tenant": "{{var.tenant}}"}}},
		Actions: []dsl.Action{{Type: "http_sequence", Method: "SEQUENCE", Target: "/orders/{{var.order}}", Params: map[string]any{"requests": []any{
			map[string]any{"type": "GET", "method": "GET", "target": "/orders/{{var.order}}"},
		}}}},
	}
	got := Analyze(script, nil)
	want := map[string][]string{"actions[0].params.requests[0]": {"host", "order", "tenant"}}
	if !reflect.DeepEqual(got.ByAction, want) {
		t.Fatalf("by action = %#v\nwant %#v", got.ByAction, want)
	}
}

func TestAnalyzeNilScript(t *testing.T) {
	t.Parallel()
	if got := Analyze(nil, map[string]string{"x": "y"}); !reflect.DeepEqual(got, Result{}) {
		t.Fatalf("Analyze(nil) = %#v", got)
	}
}

func TestReferencesParsesBothNamespaces(t *testing.T) {
	t.Parallel()
	got := References(`{{var.a}} {{ param.B }} {{var.a}} {{env.c}} {{secret.d}} {{param.var.e}} {{var.f.g-h}}`)
	want := []Reference{
		{Namespace: NamespaceVar, Name: "a"},
		{Namespace: NamespaceParam, Name: "B"},
		{Namespace: NamespaceParam, Name: "var.e"},
		{Namespace: NamespaceVar, Name: "f.g-h"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("References() = %#v\nwant %#v", got, want)
	}
}

func TestActionReferencesFollowStepReads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action dsl.Action
		want   []Reference
	}{
		{
			name: "source parameter reads only its inputs",
			action: dsl.Action{Type: "source_parameter", Params: map[string]any{
				"function":  "lr_save_string",
				"args":      []string{"{{param.region}}-{{var.zone}}", "Copy"},
				"call_args": []any{map[string]any{"name": "source_snapshot", "value": "{{var.evidence}}"}},
			}, Metadata: map[string]any{"source_snippet": "{{var.evidence}}"}},
			want: []Reference{{Namespace: NamespaceParam, Name: "region"}, {Namespace: NamespaceVar, Name: "zone"}},
		},
		{
			name:   "log-only steps read nothing",
			action: dsl.Action{Type: "source_review", Params: map[string]any{"args": []string{"{{var.x}}"}}},
		},
		{
			name: "unknown steps read every field but metadata, and the decoded body",
			action: dsl.Action{
				Type: "request", Method: "POST", Target: "/{{var.target}}",
				Body: "eyJyZXF1ZXN0X2lkIjoie3t2YXIucmVxdWVzdF9pZH19In0=", BodyEncoding: "base64",
				Metadata: map[string]any{"note": "{{var.meta}}"},
			},
			want: []Reference{
				{Namespace: NamespaceVar, Name: "request_id"},
				{Namespace: NamespaceVar, Name: "target"},
			},
		},
		{
			name: "a messaging send reads the metadata it delivers, and a protocol step any param there",
			action: dsl.Action{Type: "kafka_produce", Target: "orders", Metadata: map[string]any{
				"trace_id":   "{{var.trace_id}}",
				"source_uid": "{{var.provenance}}",
				"schema":     map[string]any{"subject": "{{var.nested}} {{param.region}}"},
			}},
			want: []Reference{
				{Namespace: NamespaceParam, Name: "region"},
				{Namespace: NamespaceVar, Name: "trace_id"},
			},
		},
		{
			name: "a protocol step's import evidence keeps its params out of the read",
			action: dsl.Action{Type: "POST", Target: "/orders", Body: "{{param.bound}}", Metadata: map[string]any{
				"source_evidence": "{{param.replaced}}",
				"Original_URL":    "/orders/{{param.recorded}}",
				"note":            "{{param.kept}}",
			}},
			want: []Reference{{Namespace: NamespaceParam, Name: "bound"}, {Namespace: NamespaceParam, Name: "kept"}},
		},
		{
			name: "an engine step's metadata stays evidence",
			action: dsl.Action{Type: "source_message", Params: map[string]any{"format": "{{var.shown}}"}, Metadata: map[string]any{
				"raw_source": "{{var.evidence}} {{param.evidence}}",
			}},
			want: []Reference{{Namespace: NamespaceVar, Name: "shown"}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ActionReferences(test.action); !reflect.DeepEqual(nonNilRefs(got), nonNilRefs(test.want)) {
				t.Fatalf("ActionReferences() = %#v\nwant %#v", got, test.want)
			}
		})
	}
}

// MetadataReads names the namespaces ActionReferences reads in one metadata
// entry, so a guided repair rebinds exactly the metadata replay reads.
func TestMetadataReadsMatchesActionReferences(t *testing.T) {
	t.Parallel()
	text := "{{var.v}} {{param.p}}"
	nested := map[string]any{"subject": text}
	tests := []struct {
		name         string
		action       dsl.Action
		key          string
		value        any
		vars, params bool
	}{
		{name: "kafka header", action: dsl.Action{Type: "kafka_produce", Target: "orders"}, key: "trace_id", value: text, vars: true, params: true},
		{name: "jms property", action: dsl.Action{Type: "jms_request_reply", Target: "orders"}, key: "tenant", value: text, vars: true, params: true},
		{name: "jms sends source_node_id", action: dsl.Action{Type: "jms_publish", Target: "orders"}, key: "source_node_id", value: text, vars: true, params: true},
		{name: "kafka keeps source_node_id off the wire", action: dsl.Action{Type: "kafka_produce", Target: "orders"}, key: "source_node_id", value: text, params: true},
		{name: "kafka sends only strings", action: dsl.Action{Type: "kafka_produce", Target: "orders"}, key: "schema", value: nested, params: true},
		{name: "kafka import evidence", action: dsl.Action{Type: "kafka_produce", Target: "orders"}, key: "source_evidence", value: text},
		{name: "jms import evidence", action: dsl.Action{Type: "jms_publish", Target: "orders"}, key: "asyncapi_channel", value: text},
		{name: "http entry", action: dsl.Action{Type: "POST", Target: "/orders"}, key: "note", value: text, params: true},
		{name: "http import evidence", action: dsl.Action{Type: "POST", Target: "/orders"}, key: "Original_URL", value: text},
		{name: "engine step", action: dsl.Action{Type: "source_message"}, key: "raw_source", value: text},
		{name: "known reads", action: dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"x", "Copy"}}}, key: "note", value: text},
		{name: "sequence wrapper", action: dsl.Action{Type: "http_sequence"}, key: "note", value: text},
		{name: "absent entry", action: dsl.Action{Type: "kafka_produce", Target: "orders"}, key: "missing"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			action := test.action
			if test.value != nil {
				action.Metadata = map[string]any{test.key: test.value}
			}
			vars, params := MetadataReads(action, test.key)
			if vars != test.vars || params != test.params {
				t.Fatalf("MetadataReads() = %v, %v; want %v, %v", vars, params, test.vars, test.params)
			}
			var read struct{ vars, params bool }
			for _, ref := range ActionReferences(action) {
				read.vars = read.vars || ref == Reference{Namespace: NamespaceVar, Name: "v"}
				read.params = read.params || ref == Reference{Namespace: NamespaceParam, Name: "p"}
			}
			if read.vars != vars || read.params != params {
				t.Fatalf("ActionReferences() reads var %v, param %v; MetadataReads() says %v, %v", read.vars, read.params, vars, params)
			}
		})
	}
}

// A scalar write after a save-all array, and an array write after a scalar,
// leave the other shape in place, as the runtime does.
func TestAnalyzeScalarAndArrayWritesCoexist(t *testing.T) {
	t.Parallel()
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{
		{Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"x", "Ids"}}},
		{Type: "source_json", Params: map[string]any{"function": "lr_eval_json", "json_object": "doc", "buffer": `{"ids":[1]}`}},
		{Type: "source_json", Params: map[string]any{"function": "lr_json_get_values", "json_object": "doc", "value_param": "Ids", "select_all": "Yes"}},
		{Type: "request", Method: "GET", Target: "/{{var.Ids}}/{{var.ids}}/{{var.ids_1}}/{{var.Ids_count}}"},
	}}
	if got := Analyze(script, nil); len(got.Missing) != 0 {
		t.Fatalf("missing = %#v", got.Missing)
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilRefs(values []Reference) []Reference {
	if values == nil {
		return []Reference{}
	}
	return values
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}
