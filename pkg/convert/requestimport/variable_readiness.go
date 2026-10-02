package requestimport

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

var variableTemplatePattern = regexp.MustCompile(`\{\{\s*var\.([a-zA-Z0-9_.-]+)`)

// VariableReadiness is the source-neutral result of analyzing variable
// references in execution order. ByAction uses stable DSL paths and Missing is
// the sorted union used by API preflight and import review.
type VariableReadiness struct {
	Missing  []string            `json:"missing,omitempty"`
	ByAction map[string][]string `json:"by_action,omitempty"`
}

// AnalyzeVariableReadiness finds references that are not supplied by explicit
// replay seeds, script parameters, or an output that executes earlier on
// the same path. Save-all extractors publish name_N and name_count, not name.
func AnalyzeVariableReadiness(script *dsl.Script, seeded map[string]string) VariableReadiness {
	if script == nil {
		return VariableReadiness{}
	}
	state := newVariableState(script, seeded)
	configRefs := scriptConfigVariableReferences(script)
	result := VariableReadiness{ByAction: make(map[string][]string)}
	analyzeVariableActions(script.Init, "init", state, configRefs, &result)
	analyzeVariableActions(script.Actions, "actions", state, configRefs, &result)
	analyzeVariableActions(script.Teardown, "teardown", state, configRefs, &result)

	union := make(map[string]struct{})
	for path, names := range result.ByAction {
		names = uniqueSorted(names)
		if len(names) == 0 {
			delete(result.ByAction, path)
			continue
		}
		result.ByAction[path] = names
		for _, name := range names {
			union[name] = struct{}{}
		}
	}
	result.Missing = sortedStringSet(union)
	if len(result.ByAction) == 0 {
		result.ByAction = nil
	}
	return result
}

// MissingVariables is the compatibility projection used by replay preflight.
func MissingVariables(script *dsl.Script, seeded map[string]string) []string {
	return AnalyzeVariableReadiness(script, seeded).Missing
}

type variableState struct {
	available      map[string]struct{}
	saveAll        map[string]struct{}
	parameterNames map[string]struct{}
	rowPrefixes    map[string]struct{}
	dataSources    map[string]struct{}
}

type configVariableRefs struct {
	baseURL       []string
	httpTransport []string
	httpRequest   []string
	soap          []string
	javaHTTP      []string
	protocol      string
}

func newVariableState(script *dsl.Script, seeded map[string]string) *variableState {
	state := &variableState{
		available:      make(map[string]struct{}, len(seeded)),
		saveAll:        make(map[string]struct{}),
		parameterNames: make(map[string]struct{}, len(script.Parameters)),
		rowPrefixes:    make(map[string]struct{}),
		dataSources:    make(map[string]struct{}),
	}
	for _, source := range script.DataSources {
		state.dataSources[source.Name] = struct{}{}
	}
	for name := range seeded {
		if name = strings.TrimSpace(name); name != "" {
			state.available[name] = struct{}{}
		}
	}
	for _, parameter := range script.Parameters {
		if name := strings.TrimSpace(parameter.Name); name != "" {
			state.parameterNames[strings.ToLower(name)] = struct{}{}
		}
	}
	return state
}

func (state *variableState) clone() *variableState {
	cloned := &variableState{
		available:      make(map[string]struct{}, len(state.available)),
		saveAll:        make(map[string]struct{}, len(state.saveAll)),
		parameterNames: state.parameterNames,
		rowPrefixes:    make(map[string]struct{}, len(state.rowPrefixes)),
		dataSources:    state.dataSources,
	}
	for prefix := range state.rowPrefixes {
		cloned.rowPrefixes[prefix] = struct{}{}
	}
	for name := range state.available {
		cloned.available[name] = struct{}{}
	}
	for name := range state.saveAll {
		cloned.saveAll[name] = struct{}{}
	}
	return cloned
}

func (state *variableState) satisfied(name string) bool {
	for prefix := range state.rowPrefixes {
		if strings.HasPrefix(name, prefix+".") {
			return true
		}
	}
	if _, ok := state.available[name]; ok {
		return true
	}
	if _, ok := state.parameterNames[strings.ToLower(strings.TrimSpace(name))]; ok {
		return true
	}
	for prefix := range state.saveAll {
		if name == prefix+"_count" {
			return true
		}
		if ordinal, ok := strings.CutPrefix(name, prefix+"_"); ok &&
			ordinal != "" && strings.Trim(ordinal, "0123456789") == "" {
			return true
		}
	}
	return false
}

func (state *variableState) register(extractors []dsl.Extractor) {
	for _, extractor := range extractors {
		name := strings.TrimSpace(extractor.Name)
		if name == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(extractor.Mode), "all") {
			state.saveAll[name] = struct{}{}
			delete(state.available, name)
			continue
		}
		state.available[name] = struct{}{}
		delete(state.saveAll, name)
	}
}

func analyzeVariableActions(actions []dsl.Action, prefix string, state *variableState, configRefs configVariableRefs, result *VariableReadiness) {
	for index := range actions {
		path := fmt.Sprintf("%s[%d]", prefix, index)
		action := &actions[index]
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			shell := *action
			shell.Control = cloneControlShell(action.Control)
			shell.Extractors = nil
			recordMissingVariableRefs(shell, path, state, configVariableRefs{}, result)
			if action.Control == nil {
				continue
			}
			thenState := state.clone()
			analyzeVariableActions(action.Control.Steps, path+".control.steps", thenState, configRefs, result)
			if len(action.Control.Else) == 0 {
				// The branch may not run, so only values available before the
				// branch remain guaranteed afterward.
				continue
			}
			elseState := state.clone()
			analyzeVariableActions(action.Control.Else, path+".control.else", elseState, configRefs, result)
			intersectVariableStates(state, thenState, elseState)
		case "control_switch":
			shell := *action
			shell.Control = cloneControlShell(action.Control)
			shell.Extractors = nil
			recordMissingVariableRefs(shell, path, state, configVariableRefs{}, result)
			if action.Control == nil {
				continue
			}
			branchStates := make([]*variableState, 0, len(action.Control.Cases)+1)
			for caseIndex, switchCase := range action.Control.Cases {
				caseState := state.clone()
				analyzeVariableActions(switchCase.Steps, fmt.Sprintf("%s.control.cases[%d].steps", path, caseIndex), caseState, configRefs, result)
				branchStates = append(branchStates, caseState)
			}
			if len(action.Control.Default) > 0 {
				defaultState := state.clone()
				analyzeVariableActions(action.Control.Default, path+".control.default", defaultState, configRefs, result)
				branchStates = append(branchStates, defaultState)
			} else {
				// No case is guaranteed to match.
				branchStates = append(branchStates, state.clone())
			}
			if len(branchStates) > 0 {
				merged := branchStates[0]
				for _, branchState := range branchStates[1:] {
					intersectVariableStates(merged, merged, branchState)
				}
				replaceVariableState(state, merged)
			}
		case "control_loop":
			bodyState := state.clone()
			bodyState.available["loop_index"] = struct{}{}
			if action.Control != nil && action.Control.ForEach != nil {
				fe := action.Control.ForEach
				if _, exists := state.dataSources[strings.TrimSpace(fe.Source)]; exists && strings.TrimSpace(fe.Var) != "" {
					bodyState.rowPrefixes[strings.TrimSpace(fe.Var)] = struct{}{}
				}
			}
			shell := *action
			shell.Control = cloneControlShell(action.Control)
			shell.Extractors = nil
			recordMissingVariableRefs(shell, path, bodyState, configVariableRefs{}, result)
			if action.Control == nil {
				continue
			}
			analyzeVariableActions(action.Control.Steps, path+".control.steps", bodyState, configRefs, result)
			if loopRunsAtLeastOnce(action.Control) {
				// Runtime restores loop locals on exit, including nested loops.
				if _, existed := state.available["loop_index"]; !existed {
					delete(bodyState.available, "loop_index")
				}
				replaceVariableState(state, bodyState)
			}
		case "control_parallel":
			if action.Control == nil {
				continue
			}
			parallelSeed := state.clone()
			for branchIndex, branch := range action.Control.Branches {
				branchState := parallelSeed.clone()
				analyzeVariableActions(branch.Steps, fmt.Sprintf("%s.control.branches[%d].steps", path, branchIndex), branchState, configRefs, result)
				for name := range branchState.available {
					if _, seeded := parallelSeed.available[name]; seeded {
						continue
					}
					state.available["parallel."+branch.Name+"."+name] = struct{}{}
				}
			}
		case "http_sequence":
			shell := *action
			shell.Params = cloneParamsWithoutRequests(action.Params)
			shell.Extractors = nil
			recordMissingVariableRefs(shell, path, state, configRefs, result)
			children := sequenceChildActions(action.Params)
			for childIndex := range children {
				childPath := fmt.Sprintf("%s.params.requests[%d]", path, childIndex)
				recordMissingVariableRefs(children[childIndex], childPath, state, configRefs, result)
				state.register(children[childIndex].Extractors)
			}
		default:
			recordMissingVariableRefs(*action, path, state, configRefs, result)
			state.register(action.Extractors)
			state.registerActionOutputs(*action)
		}
	}
}

func (state *variableState) registerActionOutputs(action dsl.Action) {
	if state != nil && dsl.CanonicalActionType(action.Type) == "source_parameter" {
		_, outputs, removed := sourceParameterReadiness(action)
		for _, name := range removed {
			delete(state.available, name)
			delete(state.available, strings.ToLower(name))
		}
		for _, name := range outputs {
			state.available[name] = struct{}{}
			state.available[strings.ToLower(name)] = struct{}{}
		}
		return
	}
	if state != nil && dsl.CanonicalActionType(action.Type) == "browser_evaluate_js" {
		state.available["result"] = struct{}{}
		return
	}
	if state == nil || dsl.CanonicalActionType(action.Type) != "http_oauth2_token" {
		return
	}
	name, _ := action.Params["save_as"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "access_token"
	}
	state.available[name] = struct{}{}
	delete(state.saveAll, name)
}

func recordMissingVariableRefs(action dsl.Action, path string, state *variableState, configRefs configVariableRefs, result *VariableReadiness) {
	references := actionVariableReferences(action)
	for _, name := range uniqueSorted(references) {
		if !state.satisfied(name) {
			result.ByAction[path] = append(result.ByAction[path], name)
		}
	}
	// Protocol configuration is flattened and handed to the protocol before
	// init/actions run. Unlike action fields, {{var.*}} references in these
	// values are not interpolated from parameters, replay seeds, or prior
	// extractors. Always report relevant config references as unresolved.
	result.ByAction[path] = append(
		result.ByAction[path],
		relevantHTTPConfigVariableReferences(action, configRefs)...,
	)
}

func scriptConfigVariableReferences(script *dsl.Script) configVariableRefs {
	if script == nil {
		return configVariableRefs{}
	}
	baseSet := make(map[string]struct{})
	collectVariableReferences(script.Config.BaseURL, baseSet)

	transportHTTP := script.Config.HTTP
	transportHTTP.Auth = dsl.AuthConfig{}
	transportHTTP.DefaultHeaders = nil
	transportHTTP.UserAgent = ""
	transportHTTP.AcceptEncoding = ""
	transportHTTP.AcceptLanguage = ""
	requestHTTP := struct {
		Auth             dsl.AuthConfig              `json:"auth,omitempty"`
		DefaultHeaders   map[string]string           `json:"default_headers,omitempty"`
		UserAgent        string                      `json:"user_agent,omitempty"`
		AcceptEncoding   string                      `json:"accept_encoding,omitempty"`
		AcceptLanguage   string                      `json:"accept_language,omitempty"`
		WebRuntime       dsl.WebRuntimeConfig        `json:"web_runtime,omitempty"`
		BrowserEmulation *dsl.BrowserEmulationConfig `json:"browser_emulation,omitempty"`
	}{
		Auth:             script.Config.HTTP.Auth,
		DefaultHeaders:   script.Config.HTTP.DefaultHeaders,
		UserAgent:        script.Config.HTTP.UserAgent,
		AcceptEncoding:   script.Config.HTTP.AcceptEncoding,
		AcceptLanguage:   script.Config.HTTP.AcceptLanguage,
		WebRuntime:       script.Config.WebRuntime,
		BrowserEmulation: script.Config.BrowserEmulation,
	}
	// AdditionalAttributes is import evidence and is not flattened into the
	// HTTP runtime configuration.
	requestHTTP.WebRuntime.AdditionalAttributes = nil

	return configVariableRefs{
		baseURL:       sortedStringSet(baseSet),
		httpTransport: configValueVariableReferences(transportHTTP),
		httpRequest:   configValueVariableReferences(requestHTTP),
		soap:          configValueVariableReferences(script.Config.SOAP),
		javaHTTP:      configValueVariableReferences(script.Config.JavaHTTP),
		protocol:      strings.ToLower(strings.TrimSpace(script.Protocol)),
	}
}

func configValueVariableReferences(value any) []string {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	set := make(map[string]struct{})
	collectVariableReferences(string(payload), set)
	return sortedStringSet(set)
}

func relevantHTTPConfigVariableReferences(action dsl.Action, refs configVariableRefs) []string {
	actionType := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(action.Type)))
	if actionType == "http_oauth2_token" {
		return append([]string(nil), refs.httpTransport...)
	}
	if !actionUsesHTTPRequestConfig(action) {
		return nil
	}
	references := append([]string(nil), refs.httpTransport...)
	references = append(references, refs.httpRequest...)
	if !absoluteHTTPActionTarget(action.Target) {
		references = append(references, refs.baseURL...)
	}
	family := strings.ToLower(strings.TrimSpace(dsl.ProtocolFamily(refs.protocol)))
	if family == "soap" || actionType == "soap_request" {
		references = append(references, refs.soap...)
	}
	if family == "java_http" || actionType == "java_http_request" {
		references = append(references, refs.javaHTTP...)
	}
	return uniqueSorted(references)
}

func actionUsesHTTPRequestConfig(action dsl.Action) bool {
	actionType := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(action.Type)))
	switch actionType {
	case "http_sequence", "request", "http_request", "soap_request", "java_http_request":
		return true
	}
	method := strings.ToUpper(strings.TrimSpace(action.Method))
	if method == "" {
		method = strings.ToUpper(strings.TrimSpace(action.Type))
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func absoluteHTTPActionTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

func actionVariableReferences(action dsl.Action) []string {
	if dsl.CanonicalActionType(action.Type) == "source_parameter" {
		inputs, _, _ := sourceParameterReadiness(action)
		// Source snapshots and call_args preserve the imported expression. Only
		// the values read by the native parameter handler are dependencies; for
		// example, random array selection reads name_count and name_N, not name.
		action = dsl.Action{Params: map[string]any{"inputs": inputs}}
	}
	payload, err := json.Marshal(action)
	if err != nil {
		return nil
	}
	set := make(map[string]struct{})
	collectVariableReferences(string(payload), set)
	if decoded, encoded, decodeErr := dsl.DecodeBodyEncoding(action.Body, action.BodyEncoding, 0); encoded && decodeErr == nil {
		// body_encoding describes storage, not the bytes delivered on the wire.
		// Decode before template analysis so an encoded {{var.*}} reference is
		// governed by the same execution-order readiness rules as a text body.
		collectVariableReferences(string(decoded), set)
	}
	return sortedStringSet(set)
}

// sourceParameterReadiness mirrors the native source-parameter handler's
// positional reads and writes. Output names are literal and publish exact and
// lowercase aliases; the input expressions are checked before those writes.
func sourceParameterReadiness(action dsl.Action) (inputs []string, outputs []string, removed []string) {
	function, _ := action.Params["function"].(string)
	function = strings.TrimSpace(function)
	if function == "" {
		function, _ = action.Metadata["source_function"].(string)
	}
	var args []string
	switch raw := action.Params["args"].(type) {
	case []string:
		for _, value := range raw {
			args = append(args, strings.TrimSpace(value))
		}
	case []any:
		for _, value := range raw {
			args = append(args, strings.TrimSpace(fmt.Sprint(value)))
		}
	default:
		if raw != nil {
			args = append(args, strings.TrimSpace(fmt.Sprint(raw)))
		}
	}
	outputAt := func(index int) {
		if index < len(args) && strings.TrimSpace(args[index]) != "" {
			outputs = append(outputs, strings.TrimSpace(args[index]))
		}
	}
	outputDefault := func(fallback string) {
		for _, arg := range args {
			if arg != "" {
				outputs = append(outputs, arg)
				return
			}
		}
		outputs = append(outputs, fallback)
	}
	switch function {
	case "lr_save_string", "lr_save_int":
		if len(args) >= 2 {
			if array, ok := action.Params["source_array"].(map[string]any); ok {
				name, _ := array["name"].(string)
				selection, _ := array["selection"].(string)
				if name = strings.ToLower(strings.TrimSpace(name)); name != "" && selection == "random" {
					inputs = append(inputs, "{{var."+name+"_count}}")
				}
			} else {
				inputs = append(inputs, args[0])
			}
			outputAt(1)
		}
	case "lr_save_var":
		if len(args) >= 4 {
			inputs = append(inputs, args[0])
			outputAt(3)
		}
	case "lr_param_sprintf":
		if len(args) >= 2 {
			inputs = append(inputs, args[1:]...)
			outputAt(0)
		}
	case "lr_param_increment":
		outputAt(0)
	case "lr_free_parameter":
		if len(args) > 0 {
			removed = append(removed, strings.TrimSpace(args[0]))
		}
	case "lr_save_timestamp", "lr_get_host_name", "lr_get_master_host_name", "lr_get_vuser_ip":
		defaults := map[string]string{"lr_save_timestamp": "timestamp", "lr_get_host_name": "host_name", "lr_get_master_host_name": "host_name", "lr_get_vuser_ip": "vuser_ip"}
		outputDefault(defaults[function])
	case "lr_save_datetime":
		if len(args) > 1 {
			inputs = append(inputs, args[0])
			outputAt(len(args) - 1)
		} else {
			outputDefault("datetime")
		}
	case "lr_whoami":
		outputs = append(outputs, "vuser_id", "vuser_group")
	case "web_convert_param":
		name, _ := action.Params["save_param"].(string)
		name = strings.TrimSpace(name)
		if name != "" {
			outputs = append(outputs, strings.TrimSpace(name))
		} else {
			outputAt(0)
		}
		if len(outputs) == 0 {
			break // The native handler returns before reading the source.
		}
		if source, ok := action.Params["source_string"].(string); ok && strings.TrimSpace(source) != "" {
			inputs = append(inputs, strings.TrimSpace(source))
		} else if len(args) > 1 {
			for _, arg := range args[1:] {
				key, value, ok := strings.Cut(arg, "=")
				if ok && strings.EqualFold(strings.TrimSpace(key), "SourceString") {
					inputs = append(inputs, value)
					break
				}
			}
		}
	default:
		// The native fallback only writes when a named SaveParam exists.
		if name := sourceParameterSaveName(action); name != "" {
			outputs = append(outputs, name)
			for _, arg := range args {
				if strings.TrimSpace(arg) != "" {
					inputs = append(inputs, arg)
					break
				}
			}
		}
	}
	return inputs, outputs, removed
}

func sourceParameterSaveName(action dsl.Action) string {
	for _, key := range []string{"save_param", "saveparam", "save_param_name", "param", "parameter", "name"} {
		if name, ok := action.Params[key].(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	payload, _ := json.Marshal(action.Params["call_args"])
	var entries []map[string]any
	_ = json.Unmarshal(payload, &entries)
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "saveparam", "save_param", "param", "parameter", "name":
			value, _ := entry["value"].(string)
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func collectVariableReferences(value string, set map[string]struct{}) {
	for _, match := range variableTemplatePattern.FindAllStringSubmatch(value, -1) {
		if len(match) == 2 {
			if name := strings.TrimSpace(match[1]); name != "" {
				set[name] = struct{}{}
			}
		}
	}
}

// provenVariableReferences returns the runtime variables referenced by value
// that shared execution-order readiness did not classify as missing at the
// current action. Credential projection uses this only to distinguish a prior
// response-derived value from an unproven source token with var-like syntax.
func provenVariableReferences(value string, missing []string) map[string]struct{} {
	referenced := make(map[string]struct{})
	collectVariableReferences(value, referenced)
	if len(referenced) == 0 {
		return nil
	}
	for _, name := range missing {
		delete(referenced, strings.TrimSpace(name))
	}
	if len(referenced) == 0 {
		return nil
	}
	return referenced
}

func cloneControlShell(control *dsl.ControlFlow) *dsl.ControlFlow {
	if control == nil {
		return nil
	}
	cloned := *control
	cloned.Steps = nil
	cloned.Else = nil
	cloned.Default = nil
	cloned.Cases = append([]dsl.SwitchCase(nil), control.Cases...)
	for index := range cloned.Cases {
		cloned.Cases[index].Steps = nil
	}
	return &cloned
}

func cloneParamsWithoutRequests(params map[string]any) map[string]any {
	if len(params) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		if key != "requests" {
			cloned[key] = value
		}
	}
	return cloned
}

func sequenceChildActions(params map[string]any) []dsl.Action {
	if params == nil || params["requests"] == nil {
		return nil
	}
	payload, err := json.Marshal(params["requests"])
	if err != nil {
		return nil
	}
	var children []dsl.Action
	if err := json.Unmarshal(payload, &children); err != nil {
		return nil
	}
	return children
}

func intersectVariableStates(target, left, right *variableState) {
	target.available = intersectStringSets(left.available, right.available)
	target.saveAll = intersectStringSets(left.saveAll, right.saveAll)
}

func replaceVariableState(target, source *variableState) {
	target.available = source.available
	target.saveAll = source.saveAll
}

func intersectStringSets(left, right map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, min(len(left), len(right)))
	for name := range left {
		if _, ok := right[name]; ok {
			out[name] = struct{}{}
		}
	}
	return out
}

func loopRunsAtLeastOnce(control *dsl.ControlFlow) bool {
	if control == nil || control.ForEach != nil {
		return false
	}
	return control.Count > 0 || control.DoWhile
}

func sortedStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func prepareMissingVariableBindings(script *dsl.Script) ([]importreview.Finding, error) {
	analysis := AnalyzeVariableReadiness(script, nil)
	if len(analysis.Missing) == 0 {
		return nil, nil
	}
	byContainer := make(map[string][]string)
	for path, names := range analysis.ByAction {
		container := sequenceContainerPath(path)
		byContainer[container] = append(byContainer[container], names...)
	}
	var findings []importreview.Finding
	if err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		missing := uniqueSorted(byContainer[ref.Path])
		if len(missing) == 0 {
			return nil
		}
		if hasImporterMissingVariableGate(ref.Action) {
			return nil
		}
		if ref.Action.Metadata == nil {
			ref.Action.Metadata = make(map[string]any)
		}
		ref.Action.Metadata["missing_variables"] = missing
		if _, exists := ref.Action.Metadata["import_review"]; !exists {
			ref.Action.Metadata["import_review"] = map[string]any{
				"version":      1,
				"source":       importSource(script),
				"reason_codes": []string{"missing_variable"},
				"source_refs":  []string{ref.Path},
				"resolution":   "declare_parameter_or_prior_extractor_and_remove_gate",
			}
		}
		finding := importreview.Finding{
			Code:       "import_missing_variables",
			Severity:   diag.Blocker,
			Element:    firstNonEmpty(ref.Action.Name, "request"),
			Detail:     fmt.Sprintf("%d variable(s) must be declared or extracted before execution", len(missing)),
			Location:   ref.Path,
			ActionPath: ref.Path,
			Gate:       importreview.GateExecution,
		}
		importreview.ApplyToAction(ref.Action, []importreview.Finding{finding})
		findings = append(findings, finding)
		return nil
	}); err != nil {
		return nil, err
	}
	return importreview.NormalizeFindings(findings), nil
}

func sequenceContainerPath(path string) string {
	if index := strings.Index(path, ".params.requests["); index >= 0 {
		return path[:index]
	}
	return path
}

func hasImporterMissingVariableGate(action *dsl.Action) bool {
	if action == nil || action.Metadata == nil {
		return false
	}
	if _, exists := action.Metadata["missing_variables"]; !exists {
		return false
	}
	switch blocker := action.Metadata["execution_blocker"].(type) {
	case string:
		return strings.TrimSpace(blocker) != ""
	}
	return false
}

// missingVariableGateCodes are the execution-blocker codes that exist solely
// because a variable had no value. Both spellings are in use: the shared
// importer gate emits import_missing_variables, and the Bruno native path
// stamps bruno_missing_variables directly on the first action.
var missingVariableGateCodes = map[string]bool{
	"import_missing_variables": true,
	"bruno_missing_variables":  true,
}

// ClearResolvedMissingVariableGates removes missing-variable execution gates
// that a later step has satisfied — typically by declaring the parameter the
// operator supplied a collection-wide value for.
//
// Gates are stamped at conversion time and are not self-healing, so without
// this a script would still refuse to run after the operator had supplied the
// very value the gate was asking for. Only the missing-variable contribution is
// removed: an action blocked for an unrelated reason stays blocked.
//
// It returns the action paths whose gate was cleared.
func ClearResolvedMissingVariableGates(script *dsl.Script) ([]string, error) {
	if script == nil {
		return nil, nil
	}
	analysis := AnalyzeVariableReadiness(script, nil)
	stillMissing := make(map[string]bool, len(analysis.ByAction))
	for path := range analysis.ByAction {
		stillMissing[sequenceContainerPath(path)] = true
	}

	var cleared []string
	err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if !hasImporterMissingVariableGate(ref.Action) || stillMissing[ref.Path] {
			return nil
		}
		if clearMissingVariableGate(ref.Action) {
			cleared = append(cleared, ref.Path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(cleared)
	return cleared, nil
}

func clearMissingVariableGate(action *dsl.Action) bool {
	if action == nil || action.Metadata == nil {
		return false
	}
	delete(action.Metadata, "missing_variables")

	remaining := make([]string, 0, 2)
	for _, code := range metadataStrings(action.Metadata, "execution_blockers") {
		if !missingVariableGateCodes[code] {
			remaining = append(remaining, code)
		}
	}
	if blocker, ok := action.Metadata["execution_blocker"].(string); ok {
		blocker = strings.TrimSpace(blocker)
		if blocker != "" && !missingVariableGateCodes[blocker] && !containsString(remaining, blocker) {
			remaining = append(remaining, blocker)
		}
	}
	if len(remaining) == 0 {
		delete(action.Metadata, "execution_blocker")
		delete(action.Metadata, "execution_blockers")
	} else {
		sort.Strings(remaining)
		action.Metadata["execution_blocker"] = remaining[0]
		action.Metadata["execution_blockers"] = remaining
	}

	clearMissingVariableImportReview(action)
	if len(action.Metadata) == 0 {
		action.Metadata = nil
	}
	return true
}

// clearMissingVariableImportReview drops the durable review record's
// missing-variable reason. The record is deleted only when nothing else
// justified it, so an unrelated gate keeps its provenance.
func clearMissingVariableImportReview(action *dsl.Action) {
	review, _ := action.Metadata["import_review"].(map[string]any)
	if review == nil {
		return
	}
	reasons := metadataStrings(review, "reason_codes")
	remaining := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason != "missing_variable" && !missingVariableGateCodes[reason] {
			remaining = append(remaining, reason)
		}
	}
	if len(remaining) == 0 {
		delete(action.Metadata, "import_review")
		return
	}
	review["reason_codes"] = remaining
}

func metadataStrings(metadata map[string]any, key string) []string {
	return dslwalk.MetadataStrings(metadata, key)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
