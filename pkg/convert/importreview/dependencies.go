package importreview

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

var variableReference = regexp.MustCompile(`\{\{\s*(var|param)\.([A-Za-z_][A-Za-z0-9_.]*)\s*\}\}`)

// AnalyzeDependencies examines executable values only. Source snapshots and
// comments are evidence, never producers. A nested producer is conservative
// evidence: its branch may not execute before a consumer.
func AnalyzeDependencies(script *dsl.Script) ([]Finding, error) {
	if script == nil {
		return nil, nil
	}
	reached := map[string]bool{}
	parameters := map[string]bool{}
	possible := map[string]bool{}
	arrayProducers := map[string]bool{}
	for _, p := range script.Parameters {
		parameters[strings.ToLower(p.Name)] = true
	}
	producers := func(a dsl.Action) []string {
		names := []string{}
		for _, e := range a.Extractors {
			names = append(names, e.Name)
			if e.Mode == "all" {
				names = append(names, e.Name+"_count")
			}
		}
		if a.Type == "source_parameter" || a.Type == "source_json" || a.Type == "source_xml" || a.Type == "set_variable" {
			for _, k := range []string{"save_param", "saveparam", "save_param_name", "parameter", "param", "name"} {
				if v, ok := a.Params[k].(string); ok && v != "" {
					// A save_all producer (lr_json_get_values SelectAll=Yes)
					// publishes NAME_1..NAME_n and NAME_count, never NAME itself.
					if all, _ := a.Params["save_all"].(bool); all {
						names = append(names, v+"_count")
					} else {
						names = append(names, v)
					}
					break
				}
			}
			var args []map[string]any
			b, _ := json.Marshal(a.Params["call_args"])
			_ = json.Unmarshal(b, &args)
			for _, arg := range args {
				k, _ := arg["name"].(string)
				if strings.EqualFold(k, "SaveParam") {
					if v, ok := arg["value"].(string); ok {
						names = append(names, v)
					}
				}
			}
		}
		// Source parameter actions publish both exact and lowercase names.
		// Extractor variables remain case sensitive.
		if strings.HasPrefix(a.Type, "source_") {
			for _, name := range append([]string(nil), names...) {
				names = append(names, strings.ToLower(name))
			}
		}
		return names
	}
	actionOf := func(ref dslwalk.SurfaceRef) dsl.Action {
		if ref.Action != nil {
			return *ref.Action
		}
		var a dsl.Action
		b, _ := json.Marshal(ref.Request)
		_ = json.Unmarshal(b, &a)
		return a
	}
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		for _, e := range actionOf(ref).Extractors {
			if e.Mode == "all" {
				arrayProducers[e.Name] = true
			}
		}
		if a := actionOf(ref); a.Type == "source_json" {
			if all, _ := a.Params["save_all"].(bool); all {
				if name, ok := a.Params["save_param"].(string); ok && name != "" {
					arrayProducers[name] = true
				}
			}
		}
		for _, name := range producers(actionOf(ref)) {
			possible[name] = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	var findings []Finding
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		a := actionOf(ref)
		// Children are walked separately. Exclude metadata and diagnostic source args.
		executable := map[string]any{"target": a.Target, "body": a.Body, "headers": a.Headers, "header_list": a.HeaderList, "value": a.Value, "auth": a.Auth}
		if !strings.HasPrefix(a.Type, "source_") || a.Type == "source_verification" {
			p := map[string]any{}
			for k, v := range a.Params {
				if k != "requests" {
					p[k] = v
				}
			}
			executable["params"] = p
		} else if a.Type == "source_parameter" {
			executable["source_string"] = a.Params["source_string"]
		} else if a.Type == "source_json" {
			executable["source_json"] = map[string]any{"buffer": a.Params["buffer"], "query": a.Params["query"]}
		}
		if a.Type == "http_sequence" {
			executable = map[string]any{}
		}
		b, _ := json.Marshal(executable)
		seen := map[string]bool{}
		for _, match := range variableReference.FindAllSubmatch(b, -1) {
			name := string(match[2])
			key := name
			parameterOnly := string(match[1]) == "param"
			if (!parameterOnly && reached[key]) || parameters[strings.ToLower(key)] || seen[string(match[1])+"."+key] {
				continue
			}
			seen[string(match[1])+"."+key] = true
			code, level, gate := "missing_producer:"+name, diag.Blocker, GateExecution
			detail := "No preceding producer supplies " + name + ". Bind a producer, parameter, or response extractor."
			arrayValue := false
			if idx := strings.LastIndex(key, "_"); idx > 0 && arrayProducers[key[:idx]] {
				n, err := strconv.Atoi(key[idx+1:])
				arrayValue = err == nil && n > 0
			}
			if !parameterOnly && (possible[key] || arrayValue) {
				code = "conditional_producer:" + name
				level = diag.Warning
				gate = GatePromotion
				detail = "Producer ordering or conditional execution for " + name + " requires review."
			}
			source := fmt.Sprintf("%v:%v", a.Metadata["source_file"], a.Metadata["source_line"])
			findings = append(findings, Finding{Code: code, ReasonCode: code, Severity: level, Gate: gate, Detail: detail, Element: name, ActionPath: ref.Path, Source: "enterprise_script_archive", SourceRef: source, Resolution: "bind_producer"})
		}
		for _, name := range producers(a) {
			if ref.Depth == 1 {
				reached[name] = true
			}
		}
		return nil
	})
	return NormalizeFindings(findings), err
}
