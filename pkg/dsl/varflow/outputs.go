// Package varflow is the one model of which variables a script step reads and
// publishes, and of whether each reference has a value when it executes.
// Server replay readiness (requestimport), import dependency review and the
// engine conformance test share it, so the producer set cannot drift between
// them. It imports only pkg/dsl and the messaging metadata rule pkg/dsl
// already depends on: requestimport imports convert/importreview, so neither
// of those may be imported here.
//
// dashboard/src/components/variableFlow.ts ports this analyzer for the
// editor's local preflight; the shared corpus pkg/dsl/varflow/testdata/
// readiness_cases.json holds both to the same expectations, so a change here
// updates the port and the corpus together.
//
// Every producer row cites the runtime code it mirrors. "x+lc" means the
// runtime saves the exact name and its lowercase alias
// (setResolvedSourceVariable, pkg/engine/vuser_source_actions.go).
package varflow

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Write is one variable a step publishes. Lower adds the lowercase alias the
// runtime saves beside the exact name. Array publishes NAME_1..NAME_n and
// NAME_count instead of NAME.
type Write struct {
	Name  string `json:"name"`
	Lower bool   `json:"lower,omitempty"`
	Array bool   `json:"array,omitempty"`
}

// Outputs describes one step's effect on runtime variables.
//
// When Known is set, ReadTexts are the only template expressions the runtime
// resolves for the step; otherwise every field of the action except its
// metadata is a potential read, plus the metadata a Kafka or JMS send
// delivers and any {{param.*}} in a protocol action's metadata outside its
// import-evidence keys (messaging.MetadataKeyIsEvidence), which replay keeps
// as recorded and never resolves or refuses. NoReads marks
// steps the runtime only logs or uses literally, so their arguments are
// evidence rather than reads. Removes are deleted (exact and lowercase), and
// each Prefixes entry publishes <prefix>.<field> values.
type Outputs struct {
	Writes    []Write  `json:"writes,omitempty"`
	Removes   []string `json:"removes,omitempty"`
	Prefixes  []string `json:"prefixes,omitempty"`
	NoReads   bool     `json:"no_reads,omitempty"`
	ReadTexts []string `json:"read_texts,omitempty"`
	Known     bool     `json:"known,omitempty"`
}

// StepOutputs returns what the runtime reads and publishes for one leaf
// action. Types are matched on their canonical name, so legacy loadrunner_*
// spellings behave like their source_* equivalents. Extractors are not
// included; Analyze registers them separately.
func StepOutputs(action dsl.Action) Outputs {
	switch dsl.CanonicalActionType(action.Type) {
	case "source_parameter":
		return sourceParameterOutputs(action)
	case "source_json":
		return sourceJSONOutputs(action)
	case "source_secret":
		return sourceSecretOutputs(action)
	case "source_message":
		// handleSourceMessageAction (vuser_source_actions.go): status_message
		// saves the rendered message as vuser_status, exact name only.
		if status, _ := paramBool(action.Params, "status_message"); status {
			return Outputs{Writes: []Write{{Name: "vuser_status"}}}
		}
	case "http_oauth2_token":
		// handleHTTPOAuth2TokenAction (vuser_source_actions.go): save_as,
		// default access_token, plus _token_type, _instance_url and
		// _expires_at, each x+lc. The handler interpolates every param.
		name, _ := paramString(action.Params, "save_as")
		if name == "" {
			name = "access_token"
		}
		return Outputs{Writes: []Write{
			{Name: name, Lower: true},
			{Name: name + "_token_type", Lower: true},
			{Name: name + "_instance_url", Lower: true},
			{Name: name + "_expires_at", Lower: true},
		}}
	case "browser_evaluate_js":
		// dispatchEvaluateJS (pkg/protocol/browser/action_dispatch.go) stores
		// the page result as the extraction "result", exact name only.
		return Outputs{Writes: []Write{{Name: "result"}}}
	case "native_javascript":
		// executeNativeJavaScript (pkg/engine/javascript.go) interpolates only
		// params.inputs; the source text never reaches the resolver. Output
		// fields are published as <result_prefix>.<key>.
		outputs := Outputs{Known: true}
		if inputs, ok := action.Params["inputs"]; ok && inputs != nil {
			if payload, err := json.Marshal(inputs); err == nil {
				outputs.ReadTexts = []string{string(payload)}
			}
		}
		if prefix, _ := action.Params["result_prefix"].(string); strings.TrimSpace(prefix) != "" {
			outputs.Prefixes = []string{strings.TrimSpace(prefix)}
		}
		return outputs
	case "live_table":
		// executeLiveTableAction publishes <result_prefix>.status,
		// .record_id, .values.<column> and so on.
		if prefix, _ := action.Params["result_prefix"].(string); strings.TrimSpace(prefix) != "" {
			return Outputs{Prefixes: []string{strings.TrimSpace(prefix)}}
		}
	case "source_review", "source_external", "custom_call", "source_database":
		// handleEngineAction routes these to logSourceReviewAction
		// (vuser_source_actions.go); source_database reaches it through the
		// default of handleSourceStateAction. Nothing is interpolated.
		return Outputs{NoReads: true}
	case "source_transaction", "source_timer", "source_metric", "source_runtime", "rendezvous":
		// handleSourceTransactionActionContext, handleSourceTimerAction,
		// handleSourceMetricAction, handleSourceRuntimeAction
		// (vuser_source_actions.go) and waitAtRendezvous (vuser_execute.go)
		// receive the raw action and use its name and args literally or only
		// log them. Nothing is interpolated.
		return Outputs{NoReads: true}
	}
	return Outputs{}
}

// sourceParameterOutputs mirrors handleSourceParameterAction
// (vuser_source_actions.go). Names come from the trimmed args, and only the
// listed inputs are interpolated: source snapshots and call_args keep the
// imported expression as evidence.
func sourceParameterOutputs(action dsl.Action) Outputs {
	outputs := Outputs{Known: true}
	args := sourceArgs(action)
	write := func(name string) {
		if name = strings.TrimSpace(name); name != "" {
			outputs.Writes = append(outputs.Writes, Write{Name: name, Lower: true})
		}
	}
	read := func(texts ...string) {
		outputs.ReadTexts = append(outputs.ReadTexts, texts...)
	}
	switch function := sourceFunction(action); function {
	case "lr_save_string", "lr_save_int":
		// Needs two args and writes args[1]. A source_array reads the selected
		// lower(name)_count / _N slot instead of the value expression.
		if len(args) >= 2 {
			if array, ok := action.Params["source_array"].(map[string]any); ok {
				name, _ := array["name"].(string)
				selection, _ := array["selection"].(string)
				if name = strings.ToLower(strings.TrimSpace(name)); name != "" && selection == "random" {
					read("{{var." + name + "_count}}")
				}
			} else {
				read(args[0])
			}
			write(args[1])
		}
	case "lr_save_var":
		// Needs four args; reads the buffer and writes args[3].
		if len(args) >= 4 {
			read(args[0])
			write(args[3])
		}
	case "lr_param_sprintf":
		// Needs two args; reads the format and its values, writes args[0].
		if len(args) >= 2 {
			read(args[1:]...)
			write(args[0])
		}
	case "lr_param_increment":
		// Writes args[0]. Its current value is a soft read that defaults to 0.
		if len(args) >= 1 {
			write(args[0])
		}
	case "lr_free_parameter":
		// Deletes args[0] and its lowercase alias. A declared parameter still
		// resolves through ResolveNamed afterwards.
		if len(args) >= 1 {
			outputs.Removes = append(outputs.Removes, args[0])
		}
	case "lr_save_timestamp":
		write(firstValue("timestamp", args...))
	case "lr_save_datetime":
		// With more than one arg the last arg names the value and args[0] is
		// the format; otherwise the first non-empty arg, else "datetime".
		if len(args) > 1 {
			read(args[0])
			write(args[len(args)-1])
		} else {
			write(firstValue("datetime", args...))
		}
	case "lr_whoami":
		write("vuser_id")
		write("vuser_group")
	case "lr_get_host_name", "lr_get_master_host_name":
		// Written only when os.Hostname succeeds; treated as provided.
		write(firstValue("host_name", args...))
	case "lr_get_vuser_ip":
		write(firstValue("vuser_ip", args...))
	case "web_convert_param":
		// handleWebConvertParamAction: params.save_param, else args[0]. A
		// blank name returns before the source is read.
		name, ok := paramString(action.Params, "save_param")
		if !ok && len(args) > 0 {
			name = args[0]
		}
		if strings.TrimSpace(name) == "" {
			break
		}
		if source, ok := paramString(action.Params, "source_string"); ok {
			read(source)
		} else if len(args) > 1 {
			for _, arg := range args[1:] {
				key, value, found := strings.Cut(arg, "=")
				if found && strings.EqualFold(strings.TrimSpace(key), "SourceString") {
					read(value)
					break
				}
			}
		}
		write(name)
	case "web_save_timestamp_param":
		// Epoch milliseconds saved under args[0].
		if len(args) >= 1 {
			write(args[0])
		}
	case "web_save_param_length":
		// The length of args[0]'s value, saved as <args[0]>_Length. The
		// value lookup is a soft read: an absent parameter has length 0.
		if len(args) >= 1 {
			write(strings.TrimSpace(args[0]) + "_Length")
		}
	case "web_convert_date_param":
		// handleWebConvertDateParamAction rewrites args[0] in place; it
		// publishes no new name and only soft-reads the current value.
	default:
		// The native fallback writes only a named SaveParam, from the first
		// non-empty arg. Positional lr_save_searched_string,
		// lr_save_param_regexp, lr_param_unique, lr_convert_* and
		// lr_eval_string publish nothing at runtime.
		if name := saveParamName(action); strings.TrimSpace(name) != "" {
			if value := firstValue("", args...); value != "" {
				read(value)
			}
			write(name)
		}
	}
	return outputs
}

// sourceJSONOutputs mirrors handleSourceJSONAction
// (pkg/engine/vuser_source_json.go). Any other JSON function fails its step,
// so its reads stay unknown and every field is checked.
func sourceJSONOutputs(action dsl.Action) Outputs {
	params := sourceJSONParams(action)
	switch sourceFunction(action) {
	case "lr_eval_json":
		// evalSourceJSON reads Buffer and fails the step when a reference has
		// no value. The JsonObject it creates is a document, not a variable.
		return Outputs{Known: true, ReadTexts: []string{params["buffer"]}}
	case "lr_json_get_values":
		// getSourceJSONValues reads QueryString. SelectAll=Yes publishes an
		// array through setSourceJSONArray (NAME_1..n and NAME_count, x+lc);
		// otherwise the first match is saved as ValueParam, x+lc.
		outputs := Outputs{Known: true, ReadTexts: []string{params["query"]}}
		name := strings.TrimSpace(params["value_param"])
		if strings.TrimSpace(params["json_object"]) == "" || name == "" {
			return outputs
		}
		outputs.Writes = []Write{{
			Name:  name,
			Lower: true,
			Array: strings.EqualFold(strings.TrimSpace(params["select_all"]), "yes"),
		}}
		return outputs
	case "lr_json_stringify":
		// stringifySourceJSON saves the document text as OutputParam, x+lc.
		outputs := Outputs{Known: true}
		name := strings.TrimSpace(params["output_param"])
		if strings.TrimSpace(params["json_object"]) == "" || name == "" {
			return outputs
		}
		outputs.Writes = []Write{{Name: name, Lower: true}}
		return outputs
	}
	return Outputs{}
}

// sourceSecretOutputs mirrors handleSourceSecretAction
// (vuser_source_actions.go): the named SaveParam, else the last arg, else the
// function without its lr_ prefix, x+lc. The saved value is a {{secret.*}}
// template, so no variable is read.
func sourceSecretOutputs(action dsl.Action) Outputs {
	args := sourceArgs(action)
	name := saveParamName(action)
	if name == "" && len(args) > 0 {
		name = args[len(args)-1]
	}
	if name == "" {
		name = strings.TrimPrefix(sourceFunction(action), "lr_")
	}
	outputs := Outputs{Known: true}
	if name = strings.TrimSpace(name); name != "" {
		outputs.Writes = []Write{{Name: name, Lower: true}}
	}
	return outputs
}

// sourceFunction mirrors sourceActionFunction (vuser_source_actions.go).
func sourceFunction(action dsl.Action) string {
	if value, ok := paramString(action.Params, "function"); ok {
		return value
	}
	if value, ok := action.Metadata["source_function"].(string); ok {
		return value
	}
	return action.Type
}

// sourceArgs mirrors sourceActionArgs (vuser_source_actions.go): a list or a
// single scalar, each value formatted with fmt.Sprint and trimmed.
func sourceArgs(action dsl.Action) []string {
	if action.Params == nil {
		return nil
	}
	raw, ok := action.Params["args"]
	if !ok {
		return nil
	}
	var values []any
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []string:
		for _, value := range typed {
			values = append(values, value)
		}
	case nil:
		return nil
	default:
		values = []any{typed}
	}
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, strings.TrimSpace(fmt.Sprint(value)))
	}
	return args
}

// saveParamName mirrors saveParamNameFromAction (vuser_source_actions.go): a
// named param key first, then a structured call_args entry. A call_args value
// is returned as written, so a blank one still ends the search.
func saveParamName(action dsl.Action) string {
	for _, key := range []string{"save_param", "saveparam", "save_param_name", "param", "parameter", "name"} {
		if value, ok := paramString(action.Params, key); ok {
			return value
		}
	}
	for _, entry := range callArgs(action.Params) {
		name, _ := entry["name"].(string)
		value, _ := entry["value"].(string)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "saveparam", "save_param", "param", "parameter", "name":
			return value
		}
	}
	return ""
}

// callArgs mirrors structuredCallArgs (vuser_source_actions.go).
func callArgs(params map[string]any) []map[string]any {
	switch typed := params["call_args"].(type) {
	case []map[string]any:
		return typed
	case []any:
		entries := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok {
				entries = append(entries, entry)
			}
		}
		return entries
	default:
		return nil
	}
}

// sourceJSONParams mirrors sourceJSONParams (vuser_source_json.go): the
// importer's structured keys when json_object is set, else the Key=Value
// arguments of older imports.
func sourceJSONParams(action dsl.Action) map[string]string {
	params := map[string]string{}
	for _, key := range []string{"json_object", "buffer", "value_param", "query", "select_all", "not_found", "format", "output_param"} {
		if value, ok := action.Params[key].(string); ok {
			params[key] = value
		}
	}
	if params["json_object"] != "" {
		return params
	}
	keys := map[string]string{"jsonobject": "json_object", "buffer": "buffer", "valueparam": "value_param", "querystring": "query", "selectall": "select_all", "notfound": "not_found", "format": "format", "outputparam": "output_param"}
	for _, arg := range sourceArgs(action) {
		name, value, found := strings.Cut(arg, "=")
		if key, known := keys[strings.ToLower(strings.TrimSpace(name))]; found && known {
			params[key] = value
		}
	}
	return params
}

// paramString mirrors actionParamString (pkg/engine/vuser_resolve.go): a
// non-blank string, trimmed.
func paramString(params map[string]any, key string) (string, bool) {
	value, ok := params[key].(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

// paramBool mirrors actionParamBool (pkg/engine/vuser_resolve.go).
func paramBool(params map[string]any, key string) (bool, bool) {
	switch typed := params[key].(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

// firstValue mirrors firstEngineValue (vuser_source_actions.go).
func firstValue(fallback string, values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}
