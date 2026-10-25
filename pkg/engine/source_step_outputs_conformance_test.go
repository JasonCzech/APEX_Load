package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
)

// sourceStepConformanceCase runs one source step through handleEngineAction
// and compares what it does to vu.Variables with varflow.StepOutputs, the
// producer model that replay readiness and import review share. OAuth,
// browser, native JavaScript and live table steps need runtimes this test
// does not start, so they are not listed.
type sourceStepConformanceCase struct {
	name string
	// setup runs before the snapshot, for example lr_eval_json documents.
	setup  []dsl.Action
	seed   map[string]string
	action dsl.Action
	// elements is how many NAME_N entries an Array write publishes.
	elements int
	// rewrites are seeded keys the step changes in place without publishing
	// a new name (web_convert_date_param).
	rewrites []string
	// skipsWrite marks a path where the runtime leaves out a predicted write
	// that readiness still treats as provided (NotFound=Continue).
	skipsWrite bool
	// fails marks a step the runtime refuses. It must change no variable.
	fails bool
	// hostname marks writes that happen only when os.Hostname succeeds.
	hostname bool
}

func sourceStepConformanceCases() []sourceStepConformanceCase {
	with := func(action dsl.Action, key string, value any) dsl.Action {
		params := make(map[string]any, len(action.Params)+1)
		for k, v := range action.Params {
			params[k] = v
		}
		params[key] = value
		action.Params = params
		return action
	}
	typed := func(actionType, function string, params map[string]any) dsl.Action {
		all := map[string]any{"function": function}
		for k, v := range params {
			all[k] = v
		}
		return dsl.Action{Name: function, Type: actionType, Params: all}
	}
	sourceJSON := func(function string, params map[string]any) dsl.Action {
		return typed("source_json", function, params)
	}
	document := func(name, buffer string) []dsl.Action {
		return []dsl.Action{sourceJSON("lr_eval_json", map[string]any{"json_object": name, "buffer": buffer})}
	}
	ids := document("doc", `{"name":"Ada","ids":["a","b","c"]}`)
	queue := map[string]string{
		"Queue_1": "q1", "queue_1": "q1", "Queue_2": "q2", "queue_2": "q2", "Queue_3": "q3", "queue_3": "q3",
		"Queue_count": "3", "queue_count": "3",
	}

	return []sourceStepConformanceCase{
		// source_parameter: handleSourceParameterAction.
		{name: "lr_save_string", seed: map[string]string{"input": "abc"}, action: sourceParameterAction("lr_save_string", "{{var.input}}", "Copy")},
		{name: "lr_save_int", action: sourceParameterAction("lr_save_int", 1, "Result")},
		{name: "lr_save_string needs two args", action: sourceParameterAction("lr_save_string", "only")},
		{name: "lr_save_string random source array", seed: map[string]string{"ids_count": "2", "ids_1": "a", "ids_2": "b"}, action: with(sourceParameterAction("lr_save_string", "{{var.ids}}", "Picked"), "source_array", map[string]any{"name": " IDs ", "selection": "random"})},
		{name: "lr_save_string non-random source array", action: with(sourceParameterAction("lr_save_string", "{{var.ids}}", "Picked"), "source_array", map[string]any{"name": "ids", "selection": "sequential"})},
		{name: "lr_save_var", seed: map[string]string{"buffer": "abcdefgh"}, action: sourceParameterAction("lr_save_var", "{{var.buffer}}", "5", "0", "Part")},
		{name: "lr_save_var needs four args", action: sourceParameterAction("lr_save_var", "{{var.buffer}}", "5", "Part")},
		{name: "lr_param_sprintf", seed: map[string]string{"a": "1", "b": "2"}, action: sourceParameterAction("lr_param_sprintf", "Formatted", "%s-%s", "{{var.a}}", "{{var.b}}")},
		{name: "lr_param_sprintf needs two args", action: sourceParameterAction("lr_param_sprintf", "Formatted")},
		{name: "lr_param_increment", action: sourceParameterAction("lr_param_increment", "Counter", "2")},
		{name: "lr_param_increment of a saved value", seed: map[string]string{"counter": "4"}, action: sourceParameterAction("lr_param_increment", "Counter")},
		{name: "lr_param_increment without args", action: sourceParameterAction("lr_param_increment")},
		{name: "lr_free_parameter", seed: map[string]string{"Value": "x", "value": "x", "other": "y"}, action: sourceParameterAction("lr_free_parameter", "Value")},
		{name: "lr_save_timestamp default name", action: sourceParameterAction("lr_save_timestamp")},
		{name: "lr_save_timestamp first non-empty arg", action: sourceParameterAction("lr_save_timestamp", "  ", "Stamp", "DIGITS=10")},
		{name: "lr_save_timestamp scalar arg", action: dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_save_timestamp", "args": "Stamp"}}},
		{name: "lr_save_datetime one arg", action: sourceParameterAction("lr_save_datetime", "Today")},
		{name: "lr_save_datetime format and destination", action: sourceParameterAction("lr_save_datetime", "%Y-%m-%d", "DATE_NOW", "Out")},
		{name: "lr_save_datetime blank destination", action: sourceParameterAction("lr_save_datetime", "%Y", "")},
		{name: "lr_save_datetime default name", action: sourceParameterAction("lr_save_datetime")},
		{name: "lr_whoami", action: sourceParameterAction("lr_whoami")},
		{name: "lr_get_host_name default", action: sourceParameterAction("lr_get_host_name"), hostname: true},
		{name: "lr_get_master_host_name named", action: sourceParameterAction("lr_get_master_host_name", "Master"), hostname: true},
		{name: "lr_get_vuser_ip default", action: sourceParameterAction("lr_get_vuser_ip")},
		{name: "web_convert_param save_param and source_string", seed: map[string]string{"token": "a b"}, action: with(with(sourceParameterAction("web_convert_param", "Ignored", "SourceString={{var.other}}"), "save_param", " c_Token_URL "), "source_string", "{{var.token}}")},
		{name: "web_convert_param args fallback", seed: map[string]string{"token": "a b"}, action: sourceParameterAction("web_convert_param", "Encoded", "SourceString={{var.token}}", "TargetEncoding=URL")},
		{name: "web_convert_param blank name", action: sourceParameterAction("web_convert_param", " ", "SourceString={{var.token}}")},
		{name: "web_convert_param save_param without args", action: dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "web_convert_param", "save_param": "Saved"}}},
		{name: "web_save_timestamp_param", action: sourceParameterAction("web_save_timestamp_param", "Tstamp")},
		{name: "web_save_timestamp_param without args", action: sourceParameterAction("web_save_timestamp_param")},
		{name: "web_save_param_length", seed: map[string]string{"c_body": "abcdef"}, action: sourceParameterAction("web_save_param_length", " C_Body ", "Base=Hexadecimal")},
		{name: "web_save_param_length without args", action: sourceParameterAction("web_save_param_length")},
		{name: "web_convert_date_param rewrites in place", seed: map[string]string{"Date": "07/03/2026", "date": "07/03/2026"}, action: sourceParameterAction("web_convert_date_param", "Date", "SourceFormat=%d/%m/%Y", "TargetFormat=%Y-%m-%d"), rewrites: []string{"Date", "date"}},
		{name: "web_convert_date_param leaves an unparsed value", seed: map[string]string{"Date": "soon", "date": "soon"}, action: sourceParameterAction("web_convert_date_param", "Date", "SourceFormat=%d/%m/%Y", "TargetFormat=%Y")},
		{name: "named SaveParam fallback", seed: map[string]string{"input": "abc"}, action: with(sourceParameterAction("legacy_parameter", " ", "{{var.input}}"), "save_param", "Result")},
		{name: "blank named fallback key is skipped", action: with(with(sourceParameterAction("legacy_parameter", "ready"), "save_param", "  "), "parameter", "Result")},
		{name: "call_args SaveParam fallback", seed: map[string]string{"body": "text"}, action: with(sourceParameterAction("lr_save_param_regexp", "{{var.body}}"), "call_args", []any{map[string]any{"name": "SaveParam", "value": "Matched"}})},
		{name: "blank call_args SaveParam", action: with(sourceParameterAction("lr_save_param_regexp", "{{var.body}}"), "call_args", []map[string]any{{"name": "SaveParam", "value": " "}, {"name": "param", "value": "Later"}})},
		{name: "positional lr_save_searched_string publishes nothing", action: sourceParameterAction("lr_save_searched_string", "{{var.body}}", "LB=a", "Found")},
		{name: "function from metadata", action: dsl.Action{Type: "source_parameter", Params: map[string]any{"args": []string{"ready", "Copy"}}, Metadata: map[string]any{"source_function": "lr_save_string"}}},
		{name: "whitespace function", action: sourceParameterAction(" lr_save_string ", "ready", "Copy")},
		{name: "legacy loadrunner_parameter", action: typed("loadrunner_parameter", "lr_save_string", map[string]any{"args": []any{"ready", "Copy"}})},

		// source_json: handleSourceJSONAction.
		{name: "lr_eval_json creates a document, not a variable", seed: map[string]string{"body": `{"a":1}`}, action: sourceJSON("lr_eval_json", map[string]any{"json_object": "doc", "buffer": "{{var.body}}"})},
		{name: "lr_json_get_values SelectAll array", setup: ids, action: sourceJSON("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": " C_OfferIds ", "query": "$.ids[*]", "select_all": " yes "}), elements: 3},
		{name: "lr_json_get_values scalar", setup: ids, action: sourceJSON("lr_json_get_values", map[string]any{"json_object": "DOC", "value_param": "C_Name", "query": "$.name", "select_all": ""})},
		{name: "lr_json_get_values legacy args", setup: ids, action: sourceJSON("lr_json_get_values", map[string]any{"args": []any{"JsonObject=doc", "ValueParam=Picked", "QueryString=$.ids[*]", "SelectAll=Yes"}}), elements: 3},
		{name: "lr_json_get_values shorter array drops stale entries", setup: document("short", `{"ids":["x","y"]}`), seed: queue, action: sourceJSON("lr_json_get_values", map[string]any{"json_object": "short", "value_param": "Queue", "query": "$.ids[*]", "select_all": "yes"}), elements: 2},
		{name: "lr_json_get_values SelectAll without a match", setup: ids, action: sourceJSON("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "Missing", "query": "$.absent[*]", "select_all": "yes", "not_found": "continue"}), elements: 0},
		{name: "lr_json_get_values scalar without a match", setup: ids, action: sourceJSON("lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "Missing", "query": "$.absent", "not_found": "Continue"}), skipsWrite: true},
		{name: "lr_json_stringify", setup: ids, action: sourceJSON("lr_json_stringify", map[string]any{"json_object": "doc", "output_param": "Text"})},
		{name: "legacy loadrunner_json", setup: ids, action: typed("loadrunner_json", "lr_json_get_values", map[string]any{"json_object": "doc", "value_param": "C_Name", "query": "$.name"})},
		{name: "other JSON functions fail", setup: ids, action: sourceJSON("lr_json_find", map[string]any{"json_object": "doc"}), fails: true},

		// source_secret: handleSourceSecretAction.
		{name: "source_secret save param", action: typed("source_secret", "lr_get_secret", map[string]any{"save_param": "Token", "args": []any{"{{var.ignored}}", "Last"}})},
		{name: "source_secret last arg", action: typed("source_secret", "lr_get_secret", map[string]any{"args": []any{"vault", "Last"}})},
		{name: "legacy loadrunner_secret function name", action: typed("loadrunner_secret", "lr_unmask", nil)},

		// source_message: handleSourceMessageAction.
		{name: "source_message status", action: typed("source_message", "lr_vuser_status_message", map[string]any{"format": "done", "status_message": "true"})},
		{name: "source_message without status", action: typed("source_message", "lr_output_message", map[string]any{"format": "done", "status_message": false})},

		// Log-only steps: logSourceReviewAction.
		{name: "source_review", seed: map[string]string{"x": "1"}, action: typed("source_review", "lr_save_searched_string", map[string]any{"args": []any{"{{var.x}}", "Copy"}})},
		{name: "source_external", action: typed("source_external", "lr_load_dll", nil)},
		{name: "custom_call", action: typed("custom_call", "helper", map[string]any{"args": []any{"Copy"}})},
		{name: "legacy loadrunner_database", action: typed("loadrunner_database", "lr_db_connect", map[string]any{"save_param": "Copy"})},

		// source_runtime: handleSourceRuntimeAction keeps its state in "__" keys.
		{name: "lr_exit", action: typed("source_runtime", "lr_exit", map[string]any{"args": []any{"LR_EXIT_VUSER", "LR_PASS"}})},
		{name: "web_cache_cleanup", action: typed("source_runtime", "web_cache_cleanup", nil)},
		{name: "lr_disable_ip_spoofing", action: typed("source_runtime", "lr_disable_ip_spoofing", nil)},
		{name: "lr_enable_ip_spoofing", action: typed("source_runtime", "lr_enable_ip_spoofing", nil)},
		{name: "lr_load_dll", action: typed("source_runtime", "lr_load_dll", map[string]any{"args": []any{"helper.dll"}})},
		{name: "other runtime functions", action: typed("source_runtime", "lr_set_debug_message", nil)},
		{name: "lr_abort", action: typed("source_runtime", "lr_abort", nil), fails: true},
	}
}

// Replay readiness predicts each source step's variable effects with
// varflow.StepOutputs. The runtime must publish exactly the predicted names,
// remove the predicted names, and change nothing else, or readiness reports
// values as missing that a replay has (or the reverse).
func TestSourceStepOutputsMatchRuntime(t *testing.T) {
	_, hostErr := os.Hostname()
	for _, tc := range sourceStepConformanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			outputs := varflow.StepOutputs(tc.action)
			if len(outputs.Prefixes) > 0 {
				t.Fatalf("prefix outputs need their own runtime: %#v", outputs)
			}
			vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{}}
			for key, value := range tc.seed {
				vu.Variables[key] = value
			}
			for _, setup := range tc.setup {
				if handled, err := vu.handleEngineAction(context.Background(), setup); !handled || err != nil {
					t.Fatalf("setup %s: handled=%v err=%v", setup.Name, handled, err)
				}
			}
			before := visibleSourceVariables(vu.Variables)

			handled, err := vu.handleEngineAction(context.Background(), tc.action)
			if !handled {
				t.Fatalf("%s was not dispatched", tc.action.Type)
			}
			if tc.fails != (err != nil) {
				t.Fatalf("err = %v, want failure %v", err, tc.fails)
			}
			after := visibleSourceVariables(vu.Variables)

			writes := expandSourceStepWrites(outputs.Writes, tc.elements)
			if tc.fails || tc.skipsWrite || (tc.hostname && hostErr != nil) {
				if tc.fails && len(writes) > 0 {
					t.Fatalf("a refused step must not be modeled as a producer: %#v", outputs)
				}
				writes = map[string]bool{}
			}
			removes := map[string]bool{}
			for _, name := range outputs.Removes {
				removes[name] = true
				removes[strings.ToLower(name)] = true
			}
			rewrites := map[string]bool{}
			for _, key := range tc.rewrites {
				rewrites[key] = true
			}

			var problems []string
			for _, key := range sortedSourceStepKeys(writes) {
				if _, ok := after[key]; !ok {
					problems = append(problems, "predicted write missing: "+key)
				}
			}
			for _, key := range sortedSourceStepKeys(removes) {
				if _, seeded := before[key]; !seeded {
					t.Fatalf("seed %s so the removal is observable", key)
				}
				if _, ok := after[key]; ok {
					problems = append(problems, "predicted removal kept: "+key)
				}
			}
			for _, key := range sortedSourceStepKeys(after) {
				previous, existed := before[key]
				switch {
				case !existed && !writes[key]:
					problems = append(problems, "unpredicted write: "+key)
				case existed && previous != after[key] && !writes[key] && !rewrites[key]:
					problems = append(problems, "unpredicted change: "+key)
				case existed && rewrites[key] && previous == after[key]:
					problems = append(problems, "expected in-place rewrite did not happen: "+key)
				}
			}
			for _, key := range sortedSourceStepKeys(before) {
				if _, ok := after[key]; !ok && !removes[key] && !staleSourceStepArrayEntry(key, outputs.Writes, tc.elements) {
					problems = append(problems, "unpredicted removal: "+key)
				}
			}
			if len(problems) > 0 {
				t.Fatalf("runtime and varflow.StepOutputs disagree:\n  %s\noutputs %#v\nbefore  %v\nafter   %v",
					strings.Join(problems, "\n  "), outputs, before, after)
			}
			for _, write := range outputs.Writes {
				if write.Array && !tc.fails && !tc.skipsWrite {
					if got := after[strings.TrimSpace(write.Name)+"_count"]; got != strconv.Itoa(tc.elements) {
						t.Fatalf("%s_count = %q, want %d", write.Name, got, tc.elements)
					}
				}
			}
		})
	}
}

// The conformance cases must reach every branch the runtime dispatches on, so
// a new runtime producer cannot land without a matching StepOutputs row.
func TestSourceStepOutputsConformanceCoversRuntimeBranches(t *testing.T) {
	branches := map[string][]string{
		"source_parameter": sourceStepSwitchCases(t, "vuser_source_actions.go", "handleSourceParameterAction"),
		"source_json":      sourceStepSwitchCases(t, "vuser_source_json.go", "handleSourceJSONAction"),
		"source_runtime":   sourceStepSwitchCases(t, "vuser_source_actions.go", "handleSourceRuntimeAction"),
	}
	types := sourceStepSwitchCases(t, "vuser_source_actions.go", "handleSourceStateAction")
	if len(types) == 0 || len(branches["source_parameter"]) == 0 || len(branches["source_json"]) == 0 || len(branches["source_runtime"]) == 0 {
		t.Fatalf("runtime switches not found: types=%v branches=%v", types, branches)
	}

	covered := map[string]map[string]bool{}
	coveredTypes := map[string]bool{}
	for _, tc := range sourceStepConformanceCases() {
		actionType := dsl.CanonicalActionType(tc.action.Type)
		coveredTypes[actionType] = true
		if covered[actionType] == nil {
			covered[actionType] = map[string]bool{}
		}
		covered[actionType][strings.TrimSpace(sourceActionFunction(tc.action))] = true
	}
	for _, actionType := range append(types, "source_database", "source_review", "source_external", "custom_call", "source_message") {
		if !coveredTypes[actionType] {
			t.Errorf("no conformance case for %s", actionType)
		}
	}
	for actionType, functions := range branches {
		known := map[string]bool{}
		for _, function := range functions {
			known[function] = true
			if !covered[actionType][function] {
				t.Errorf("no conformance case for %s %s", actionType, function)
			}
		}
		fallback := false
		for function := range covered[actionType] {
			fallback = fallback || !known[function]
		}
		if !fallback {
			t.Errorf("no conformance case for the %s default branch", actionType)
		}
	}
}

// The legacy loadrunner_* spellings used to fall through to the log-only
// default of handleSourceStateAction, so they saved nothing although replay
// readiness counted them as producers.
func TestSourceStepLegacyLoadRunnerTypesWrite(t *testing.T) {
	for _, tc := range []struct {
		legacy, canonical, function, saved string
		params                             map[string]any
	}{
		{"loadrunner_parameter", "source_parameter", "lr_save_string", "Copy", map[string]any{"args": []any{"{{var.input}}", "Copy"}}},
		{"loadrunner_secret", "source_secret", "lr_get_secret", "Token", map[string]any{"save_param": "Token"}},
	} {
		t.Run(tc.legacy, func(t *testing.T) {
			run := func(actionType string) map[string]string {
				params := map[string]any{"function": tc.function}
				for key, value := range tc.params {
					params[key] = value
				}
				vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{"input": "abc"}}
				if handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: tc.function, Type: actionType, Params: params}); !handled || err != nil {
					t.Fatalf("%s: handled=%v err=%v", actionType, handled, err)
				}
				return vu.Variables
			}
			legacy, canonical := run(tc.legacy), run(tc.canonical)
			value, ok := legacy[tc.saved]
			if !ok || value == "" || value != canonical[tc.saved] || legacy[strings.ToLower(tc.saved)] != value {
				t.Fatalf("%s saved %q (lowercase %q), want %q as %s saves it", tc.legacy, value, legacy[strings.ToLower(tc.saved)], canonical[tc.saved], tc.canonical)
			}
			if tc.legacy == "loadrunner_parameter" && value != "abc" {
				t.Fatalf("lr_save_string saved %q, want abc", value)
			}
		})
	}
}

// expandSourceStepWrites lists the keys a StepOutputs write publishes: the
// name, plus its lowercase alias when Lower is set; an Array write publishes
// NAME_1..NAME_elements and NAME_count instead.
func expandSourceStepWrites(writes []varflow.Write, elements int) map[string]bool {
	keys := map[string]bool{}
	add := func(name string, lower bool) {
		keys[name] = true
		if lower {
			keys[strings.ToLower(name)] = true
		}
	}
	for _, write := range writes {
		name := strings.TrimSpace(write.Name)
		if !write.Array {
			add(name, write.Lower)
			continue
		}
		for index := 1; index <= elements; index++ {
			add(fmt.Sprintf("%s_%d", name, index), write.Lower)
		}
		add(name+"_count", write.Lower)
	}
	return keys
}

// staleSourceStepArrayEntry reports whether key is an entry an Array write
// removes because the new result is shorter (setSourceJSONArray). Readiness
// models the whole NAME_N family as one array, so this is not a removal it
// tracks.
func staleSourceStepArrayEntry(key string, writes []varflow.Write, elements int) bool {
	for _, write := range writes {
		if !write.Array {
			continue
		}
		name := strings.TrimSpace(write.Name)
		for _, prefix := range []string{name + "_", strings.ToLower(name) + "_"} {
			if ordinal, ok := strings.CutPrefix(key, prefix); ok {
				if index, err := strconv.Atoi(ordinal); err == nil && index > elements {
					return true
				}
			}
		}
	}
	return false
}

// visibleSourceVariables copies the variables templates can read. "__" keys
// hold runtime bookkeeping (transactions, timers, lr_exit) and are skipped.
func visibleSourceVariables(variables map[string]string) map[string]string {
	visible := make(map[string]string, len(variables))
	for key, value := range variables {
		if !strings.HasPrefix(key, "__") {
			visible[key] = value
		}
	}
	return visible
}

func sortedSourceStepKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sourceStepSwitchCases returns the string case labels of the first switch
// statement in the body of the named method.
func sourceStepSwitchCases(t *testing.T, file, method string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != method || fn.Body == nil {
			continue
		}
		for _, statement := range fn.Body.List {
			switchStatement, ok := statement.(*ast.SwitchStmt)
			if !ok {
				continue
			}
			var labels []string
			for _, clause := range switchStatement.Body.List {
				for _, expression := range clause.(*ast.CaseClause).List {
					literal, ok := expression.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					label, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatal(err)
					}
					labels = append(labels, label)
				}
			}
			return labels
		}
	}
	t.Fatalf("%s: no switch in %s", file, method)
	return nil
}
