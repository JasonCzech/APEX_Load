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
// replay seeds, script parameters, or an extractor that executes earlier on
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
		case "control_loop":
			shell := *action
			shell.Control = cloneControlShell(action.Control)
			shell.Extractors = nil
			recordMissingVariableRefs(shell, path, state, configVariableRefs{}, result)
			if action.Control == nil {
				continue
			}
			bodyState := state.clone()
			analyzeVariableActions(action.Control.Steps, path+".control.steps", bodyState, configRefs, result)
			if loopRunsAtLeastOnce(action.Control) {
				replaceVariableState(state, bodyState)
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
