package importcontract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/paritytest"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"github.com/jasonczech/apexload/pkg/recorder"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/recorder/marker"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

// normalizedExecutableScript is the Level 1 canonical executable projection of
// a converted dsl.Script. Presentation and provenance-only fields are stripped,
// while executable runtime config, parameters, data sources, goal config, HTTP
// semantics, header occurrence order, bodies, assertions, extractors, think
// times, transactions, review/execution gates, and variable producer->consumer
// bindings are preserved.
type normalizedExecutableScript struct {
	Protocol         string              `json:"protocol"`
	BaseURL          string              `json:"base_url,omitempty"`
	Config           dsl.ScriptConfig    `json:"config"`
	Metadata         *dsl.ScriptMetadata `json:"metadata,omitempty"`
	Parameters       []dsl.Parameter     `json:"parameters,omitempty"`
	DataSources      []dsl.DataSource    `json:"data_sources,omitempty"`
	GoalConfig       *dsl.GoalConfig     `json:"goal_config,omitempty"`
	Init             []dsl.Action        `json:"init,omitempty"`
	Actions          []dsl.Action        `json:"actions"`
	Teardown         []dsl.Action        `json:"teardown,omitempty"`
	VariableBindings []variableBinding   `json:"variable_bindings,omitempty"`
	UnboundVariables []string            `json:"unbound_variables,omitempty"`
}

// variableBinding records the structural producer site and ordered consumer
// sites of one runtime variable in a normalized script.
type variableBinding struct {
	CanonicalName string   `json:"canonical_name"`
	ProducerSite  string   `json:"producer_site"`
	ConsumerSites []string `json:"consumer_sites,omitempty"`
}

var varTemplateRegex = regexp.MustCompile(`\{\{\s*var\.([a-zA-Z0-9_.-]+)(\s*\|\s*[^}]+)?\s*\}\}`)

var allowedActionProvenanceMetadataKeys = map[string]bool{
	"source_file":               true,
	"source_line":               true,
	"source_end_line":           true,
	"source_column":             true,
	"source_function":           true,
	"source_step_name":          true,
	"source_step_id":            true,
	"source_entry_id":           true,
	"source_section":            true,
	"source_phase":              true,
	"source_action_name":        true,
	"original_url":              true,
	"http_version_capture":      true,
	"response_content_capture":  true,
	"source_transaction_status": true,
	"source_lifecycle_group":    true,
	"lr_c_helper":               true,
	"comment":                   true,
	"phase":                     true,
	"support_mode":              true,
}

// normalizeExecutableScript strips allowed presentation/provenance fields from
// script and canonicalizes variable producer/consumer bindings so structural
// equivalence can be asserted across HTTP authoring and import entrypoints.
func normalizeExecutableScript(script *dsl.Script) (*normalizedExecutableScript, error) {
	if script == nil {
		return nil, fmt.Errorf("script is nil")
	}
	raw, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var clone dsl.Script
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, err
	}

	var renameOrder []string
	renameMap := map[string]string{}
	producerByCanonical := map[string]string{}
	consumersByCanonical := map[string][]string{}
	var unbound []string

	registerProducer := func(rawName, site string) string {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return ""
		}
		canon, exists := renameMap[name]
		if !exists {
			canon = fmt.Sprintf("__var_%d", len(renameOrder)+1)
			renameMap[name] = canon
			renameOrder = append(renameOrder, canon)
			producerByCanonical[canon] = site
		}
		return canon
	}

	rewriteTemplates := func(text, site string) string {
		if text == "" {
			return ""
		}
		return varTemplateRegex.ReplaceAllStringFunc(text, func(match string) string {
			sub := varTemplateRegex.FindStringSubmatch(match)
			if len(sub) < 2 {
				return match
			}
			varName := strings.TrimSpace(sub[1])
			pipeSuffix := ""
			if len(sub) >= 3 && strings.TrimSpace(sub[2]) != "" {
				filterExpr := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sub[2]), "|"))
				if filterExpr != "" {
					pipeSuffix = " | " + filterExpr
				}
			}
			canon, ok := renameMap[varName]
			if !ok {
				unbound = append(unbound, fmt.Sprintf("%s->%s", site, varName))
				return "{{var." + varName + pipeSuffix + "}}"
			}
			consumersByCanonical[canon] = append(consumersByCanonical[canon], site)
			return "{{var." + canon + pipeSuffix + "}}"
		})
	}

	var rewriteAny func(value any, site string) any
	rewriteAny = func(value any, site string) any {
		switch typed := value.(type) {
		case string:
			return rewriteTemplates(typed, site)
		case []any:
			out := make([]any, len(typed))
			for i, item := range typed {
				out[i] = rewriteAny(item, fmt.Sprintf("%s[%d]", site, i))
			}
			return out
		case map[string]any:
			out := make(map[string]any, len(typed))
			keys := make([]string, 0, len(typed))
			for k := range typed {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				out[k] = rewriteAny(typed[k], site+"."+k)
			}
			return out
		default:
			return value
		}
	}

	var normalizeActions func(actions []dsl.Action, phasePrefix string) []dsl.Action
	normalizeActions = func(actions []dsl.Action, phasePrefix string) []dsl.Action {
		if len(actions) == 0 {
			return nil
		}
		out := make([]dsl.Action, len(actions))
		for i, act := range actions {
			path := fmt.Sprintf("%s[%d]", phasePrefix, i)
			norm := normalizeSingleAction(act, path, registerProducer, rewriteTemplates, rewriteAny, normalizeActions)
			out[i] = norm
		}
		return out
	}

	normInit := normalizeActions(clone.Init, "init")
	normMain := normalizeActions(clone.Actions, "actions")
	normTeardown := normalizeActions(clone.Teardown, "teardown")

	normCfg := normalizeScriptConfig(clone.Config)
	if err := rewriteStructTemplates(&normCfg, "config", rewriteAny); err != nil {
		return nil, err
	}
	normParams := clone.Parameters
	if len(normParams) > 0 {
		if err := rewriteStructTemplates(&normParams, "parameters", rewriteAny); err != nil {
			return nil, err
		}
	}
	normDataSources := clone.DataSources
	if len(normDataSources) > 0 {
		if err := rewriteStructTemplates(&normDataSources, "data_sources", rewriteAny); err != nil {
			return nil, err
		}
	}
	normGoalConfig := clone.GoalConfig
	if normGoalConfig != nil {
		if err := rewriteStructTemplates(&normGoalConfig, "goal_config", rewriteAny); err != nil {
			return nil, err
		}
	}

	bindings := make([]variableBinding, 0, len(renameOrder))
	for _, canon := range renameOrder {
		bindings = append(bindings, variableBinding{
			CanonicalName: canon,
			ProducerSite:  producerByCanonical[canon],
			ConsumerSites: consumersByCanonical[canon],
		})
	}

	return &normalizedExecutableScript{
		Protocol:         strings.ToLower(strings.TrimSpace(clone.Protocol)),
		BaseURL:          normCfg.BaseURL,
		Config:           normCfg,
		Metadata:         normalizeScriptMetadata(clone.Metadata),
		Parameters:       normParams,
		DataSources:      normDataSources,
		GoalConfig:       normGoalConfig,
		Init:             normInit,
		Actions:          normMain,
		Teardown:         normTeardown,
		VariableBindings: bindings,
		UnboundVariables: unbound,
	}, nil
}

func normalizeScriptConfig(cfg dsl.ScriptConfig) dsl.ScriptConfig {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	// Strip presentation/diagnostic log verbosity while keeping all executable
	// transport, browser, think-time, pacing, run-logic, and error-handling fields.
	cfg.Log = dsl.LogConfig{}
	if strings.EqualFold(strings.TrimSpace(cfg.Pacing.Type), "none") {
		cfg.Pacing.Type = ""
	}
	if strings.EqualFold(strings.TrimSpace(cfg.RunLogic.AutoTransactions), "none") {
		cfg.RunLogic.AutoTransactions = ""
	}
	if len(cfg.HTTP.DefaultHeaderList) == 0 && len(cfg.HTTP.DefaultHeaders) > 0 {
		cfg.HTTP.DefaultHeaderList = dsl.HeaderListFromMap(cfg.HTTP.DefaultHeaders)
	}
	cfg.HTTP.DefaultHeaders = nil
	if cfg.HTTP.Traceparent != nil && !*cfg.HTTP.Traceparent {
		cfg.HTTP.Traceparent = nil
	}
	return cfg
}

func rewriteStructTemplates(target any, site string, rewriteAny func(value any, site string) any) error {
	raw, err := json.Marshal(target)
	if err != nil {
		return err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return err
	}
	rewritten := rewriteAny(generic, site)
	rewrittenBytes, err := json.Marshal(rewritten)
	if err != nil {
		return err
	}
	return json.Unmarshal(rewrittenBytes, target)
}

func normalizeSingleAction(
	act dsl.Action,
	path string,
	registerProducer func(rawName, site string) string,
	rewriteTemplates func(text, site string) string,
	rewriteAny func(value any, site string) any,
	normalizeActions func(actions []dsl.Action, phasePrefix string) []dsl.Action,
) dsl.Action {
	// Canonicalize HTTP request action type/method representation.
	upperType := strings.ToUpper(strings.TrimSpace(act.Type))
	switch upperType {
	case "REQUEST", "HTTP_REQUEST", "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		method := strings.ToUpper(strings.TrimSpace(act.Method))
		if method == "" && upperType != "REQUEST" && upperType != "HTTP_REQUEST" {
			method = upperType
		}
		act.Type = "request"
		act.Method = method
	}

	// Preserve HeaderList occurrences as the single authoritative header list.
	if len(act.HeaderList) == 0 && len(act.Headers) > 0 {
		act.HeaderList = dsl.HeaderListFromMap(act.Headers)
	}
	act.Headers = nil

	// Strip presentation action name except where it carries transaction marker identity.
	if act.Type == "source_transaction" {
		act.Params = normalizeSourceTransactionAction(act)
		act.Name = ""
	} else {
		act.Name = ""
	}

	// Rewrite consumer templates in target, headers, body, multipart, value, assertions, and params
	// BEFORE registering this action's own response extractors, so a consumer on step N cannot
	// accidentally bind to a producer that only extracts from step N's response.
	act.Target = rewriteTemplates(act.Target, path+".target")
	for j := range act.HeaderList {
		act.HeaderList[j].Name = strings.TrimSpace(act.HeaderList[j].Name)
		act.HeaderList[j].Value = rewriteTemplates(act.HeaderList[j].Value, fmt.Sprintf("%s.header_list[%d](%s)", path, j, act.HeaderList[j].Name))
	}
	act.Body = rewriteTemplates(act.Body, path+".body")
	if act.BodyMultipart != nil {
		for j := range act.BodyMultipart.Parts {
			act.BodyMultipart.Parts[j].Value = rewriteTemplates(act.BodyMultipart.Parts[j].Value, fmt.Sprintf("%s.body_multipart.parts[%d].value", path, j))
		}
	}
	act.Value = rewriteTemplates(act.Value, path+".value")

	for j := range act.Assertions {
		act.Assertions[j].Value = rewriteTemplates(act.Assertions[j].Value, fmt.Sprintf("%s.assertions[%d].value", path, j))
	}

	if len(act.Params) > 0 {
		if act.Type == "http_sync" {
			if paramName, ok := act.Params["param"].(string); ok && paramName != "" {
				rewritten := rewriteTemplates("{{var."+paramName+"}}", path+".params.param")
				act.Params["param"] = strings.TrimSuffix(strings.TrimPrefix(rewritten, "{{var."), "}}")
			}
		}
		if act.Type == "set_var" {
			if varName, ok := act.Params["name"].(string); ok && strings.TrimSpace(varName) != "" {
				act.Params["name"] = registerProducer(varName, path+".params.name")
			}
		}
		var normalizedSeqReqs []dsl.Action
		hasSeqReqs := false
		if act.Type == "http_sequence" {
			if rawReqs, ok := act.Params["requests"]; ok && rawReqs != nil {
				if reqBytes, err := json.Marshal(rawReqs); err == nil {
					var childActions []dsl.Action
					if err := json.Unmarshal(reqBytes, &childActions); err == nil {
						normalizedSeqReqs = normalizeActions(childActions, path+".params.requests")
						hasSeqReqs = true
						delete(act.Params, "requests")
					}
				}
			}
		}
		rewritten, _ := rewriteAny(act.Params, path+".params").(map[string]any)
		if hasSeqReqs {
			if rewritten == nil {
				rewritten = make(map[string]any)
			}
			rewritten["requests"] = normalizedSeqReqs
		}
		act.Params = rewritten
	}

	for j := range act.Extractors {
		act.Extractors[j].Expression = rewriteTemplates(act.Extractors[j].Expression, fmt.Sprintf("%s.extractors[%d].expression", path, j))
		act.Extractors[j].Name = registerProducer(act.Extractors[j].Name, fmt.Sprintf("%s.extractors[%d]", path, j))
		// Normalize Scope when it only carries the default Redirects/Resources include.
		if scope := act.Extractors[j].Scope; scope != nil {
			if (scope.Redirects == "" || scope.Redirects == "include") &&
				(scope.Resources == "" || scope.Resources == "include") &&
				scope.RequestURL == "" && scope.ContentType == "" && len(scope.HeaderNames) == 0 {
				act.Extractors[j].Scope = nil
			}
		}
	}

	if act.Control != nil {
		ctrl := *act.Control
		if ctrl.Condition != nil {
			cond := *ctrl.Condition
			cond.Expr = rewriteTemplates(cond.Expr, path+".control.condition.expr")
			cond.Left = rewriteTemplates(cond.Left, path+".control.condition.left")
			cond.Right = rewriteTemplates(cond.Right, path+".control.condition.right")
			ctrl.Condition = &cond
		}
		if ctrl.ForEach != nil {
			fe := *ctrl.ForEach
			if strings.TrimSpace(fe.Var) != "" {
				fe.Var = registerProducer(fe.Var, path+".control.for_each.var")
			}
			ctrl.ForEach = &fe
		}
		ctrl.Steps = normalizeActions(ctrl.Steps, path+".control.steps")
		ctrl.Else = normalizeActions(ctrl.Else, path+".control.else")
		ctrl.Default = normalizeActions(ctrl.Default, path+".control.default")
		for idx := range ctrl.Cases {
			ctrl.Cases[idx].Steps = normalizeActions(ctrl.Cases[idx].Steps, fmt.Sprintf("%s.control.cases[%d].steps", path, idx))
		}
		for idx := range ctrl.Branches {
			ctrl.Branches[idx].Steps = normalizeActions(ctrl.Branches[idx].Steps, fmt.Sprintf("%s.control.branches[%d].steps", path, idx))
		}
		act.Control = &ctrl
	}

	act.Metadata = normalizeActionMetadata(act.Metadata)
	return act
}

func normalizeSourceTransactionAction(act dsl.Action) map[string]any {
	fn, _ := act.Params["function"].(string)
	fn = strings.ToLower(strings.TrimSpace(fn))
	var args []string
	if rawArgs, ok := act.Params["args"].([]any); ok {
		for _, item := range rawArgs {
			if s, ok := item.(string); ok {
				args = append(args, strings.TrimSpace(s))
			}
		}
	} else if strArgs, ok := act.Params["args"].([]string); ok {
		for _, s := range strArgs {
			args = append(args, strings.TrimSpace(s))
		}
	}
	txName := strings.TrimSpace(act.Name)
	if len(args) > 0 && args[0] != "" {
		txName = args[0]
	}
	out := map[string]any{
		"function":    fn,
		"transaction": txName,
	}
	if strings.HasPrefix(fn, "lr_end_") {
		status := "auto"
		if len(args) >= 2 && args[1] != "" {
			s := strings.ToLower(strings.TrimSpace(args[1]))
			s = strings.TrimPrefix(s, "lr.")
			s = strings.TrimPrefix(s, "lr_")
			status = s
		}
		out["status"] = status
	}
	return out
}

func normalizeScriptMetadata(meta *dsl.ScriptMetadata) *dsl.ScriptMetadata {
	if meta == nil {
		return nil
	}
	out := *meta
	if out.Import != nil {
		imp := *out.Import
		imp.Source = ""
		imp.SourceScriptName = ""
		imp.Preserved = nil
		if len(imp.Inputs) == 0 {
			out.Import = nil
		} else {
			out.Import = &imp
		}
	}
	if out.Import == nil && out.RuntimePolicy == nil {
		return nil
	}
	return &out
}

func normalizeActionMetadata(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any)
	for k, v := range meta {
		if allowedActionProvenanceMetadataKeys[k] || strings.HasPrefix(k, "har.") {
			continue
		}
		if k == "import_review" {
			out[k] = normalizeImportReviewMetadata(v)
			continue
		}
		if k == "import_credential_targets" {
			out[k] = normalizeImportCredentialTargets(v)
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var actionPathPrefixRegex = regexp.MustCompile(`^(?:init|actions|teardown)\[\d+\]\.`)

func normalizeImportCredentialTargets(raw any) any {
	list, ok := raw.([]any)
	if !ok {
		return raw
	}
	cleaned := make([]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			cleaned = append(cleaned, item)
			continue
		}
		cm := make(map[string]any, len(m))
		for k, v := range m {
			if k == "request_key" {
				continue
			}
			if k == "field" {
				if s, ok := v.(string); ok {
					v = actionPathPrefixRegex.ReplaceAllString(s, "")
				}
			}
			cm[k] = v
		}
		cleaned = append(cleaned, cm)
	}
	return cleaned
}

func normalizeImportReviewMetadata(raw any) any {
	mapped, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	out := make(map[string]any, len(mapped))
	for k, v := range mapped {
		switch k {
		case "source", "source_ref":
			continue
		case "findings":
			if list, ok := v.([]any); ok {
				cleaned := make([]any, 0, len(list))
				for _, item := range list {
					if fm, ok := item.(map[string]any); ok {
						fc := make(map[string]any, len(fm))
						for fk, fv := range fm {
							if fk == "source" || fk == "source_ref" || fk == "location" {
								continue
							}
							fc[fk] = fv
						}
						cleaned = append(cleaned, fc)
					} else {
						cleaned = append(cleaned, item)
					}
				}
				out[k] = cleaned
				continue
			}
		}
		out[k] = v
	}
	return out
}

// compareExecutableScripts compares two dsl.Scripts under the Level 1
// normalized executable contract and returns human-readable differences.
func compareExecutableScripts(leftLabel string, left *dsl.Script, rightLabel string, right *dsl.Script) []string {
	normLeft, err := normalizeExecutableScript(left)
	if err != nil {
		return []string{fmt.Sprintf("normalize %s: %v", leftLabel, err)}
	}
	normRight, err := normalizeExecutableScript(right)
	if err != nil {
		return []string{fmt.Sprintf("normalize %s: %v", rightLabel, err)}
	}
	var diffs []string
	if len(normLeft.UnboundVariables) > 0 {
		diffs = append(diffs, fmt.Sprintf("%s has unbound variable reference(s): %v", leftLabel, normLeft.UnboundVariables))
	}
	if len(normRight.UnboundVariables) > 0 {
		diffs = append(diffs, fmt.Sprintf("%s has unbound variable reference(s): %v", rightLabel, normRight.UnboundVariables))
	}
	if normLeft.Protocol != normRight.Protocol {
		diffs = append(diffs, fmt.Sprintf("protocol: %s=%q vs %s=%q", leftLabel, normLeft.Protocol, rightLabel, normRight.Protocol))
	}
	if normLeft.BaseURL != normRight.BaseURL {
		diffs = append(diffs, fmt.Sprintf("base_url: %s=%q vs %s=%q", leftLabel, normLeft.BaseURL, rightLabel, normRight.BaseURL))
	}
	if !reflect.DeepEqual(normLeft.Config, normRight.Config) {
		diffs = append(diffs, fmt.Sprintf("config: %s=%s vs %s=%s", leftLabel, mustJSON(normLeft.Config), rightLabel, mustJSON(normRight.Config)))
	}
	if !reflect.DeepEqual(normLeft.Metadata, normRight.Metadata) {
		diffs = append(diffs, fmt.Sprintf("script metadata: %s=%s vs %s=%s", leftLabel, mustJSON(normLeft.Metadata), rightLabel, mustJSON(normRight.Metadata)))
	}
	if !reflect.DeepEqual(normLeft.Parameters, normRight.Parameters) {
		diffs = append(diffs, fmt.Sprintf("parameters: %s=%s vs %s=%s", leftLabel, mustJSON(normLeft.Parameters), rightLabel, mustJSON(normRight.Parameters)))
	}
	if !reflect.DeepEqual(normLeft.DataSources, normRight.DataSources) {
		diffs = append(diffs, fmt.Sprintf("data_sources: %s=%s vs %s=%s", leftLabel, mustJSON(normLeft.DataSources), rightLabel, mustJSON(normRight.DataSources)))
	}
	if !reflect.DeepEqual(normLeft.GoalConfig, normRight.GoalConfig) {
		diffs = append(diffs, fmt.Sprintf("goal_config: %s=%s vs %s=%s", leftLabel, mustJSON(normLeft.GoalConfig), rightLabel, mustJSON(normRight.GoalConfig)))
	}
	if !reflect.DeepEqual(normLeft.VariableBindings, normRight.VariableBindings) {
		diffs = append(diffs, fmt.Sprintf("variable bindings: %s=%s vs %s=%s", leftLabel, mustJSON(normLeft.VariableBindings), rightLabel, mustJSON(normRight.VariableBindings)))
	}
	diffs = append(diffs, compareActionSlices("init", leftLabel, normLeft.Init, rightLabel, normRight.Init)...)
	diffs = append(diffs, compareActionSlices("actions", leftLabel, normLeft.Actions, rightLabel, normRight.Actions)...)
	diffs = append(diffs, compareActionSlices("teardown", leftLabel, normLeft.Teardown, rightLabel, normRight.Teardown)...)
	return diffs
}

func compareActionSlices(phase, leftLabel string, left []dsl.Action, rightLabel string, right []dsl.Action) []string {
	var diffs []string
	if len(left) != len(right) {
		diffs = append(diffs, fmt.Sprintf("%s count: %s=%d vs %s=%d", phase, leftLabel, len(left), rightLabel, len(right)))
		return diffs
	}
	for i := range left {
		lJSON := mustJSON(left[i])
		rJSON := mustJSON(right[i])
		if lJSON != rJSON {
			diffs = append(diffs, fmt.Sprintf("%s[%d] mismatch:\n  %s: %s\n  %s: %s", phase, i, leftLabel, lJSON, rightLabel, rJSON))
		}
	}
	return diffs
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// Paired Synthetic Workflow Fixtures (3 source representations: proxy
// recorder.Session, HAR 1.2 document + browser recording session companion,
// and LoadRunner URL-level C archive)
// ---------------------------------------------------------------------------

type pairedWorkflowInputs struct {
	recorderSession *recorder.Session
	fullHAR         string
	browserSession  *recbrowser.Session
	lrArchiveZip    []byte
}

func convertBrowserCompanionHTTPScript(t *testing.T, session *recbrowser.Session, opts recordbridge.BrowserHTTPScriptOptions) recordbridge.BrowserHTTPScript {
	t.Helper()
	if opts.DisableSalesforce == nil {
		// recorder.ConvertSessionWithOptions, har.Convert, and sample.Convert operate
		// below recordbridge's Salesforce profile augmentation wrapper; disable that
		// wrapper here while still exercising CreateHTTPScriptFromHARAtLevel's marker,
		//EntryPointBrowserHTTP policy resolution, client-UUID, and client-timestamp pipeline.
		disable := true
		opts.DisableSalesforce = &disable
	}
	svc := &recordbridge.Service{
		Conversion: recordbridge.ConversionPorts{
			ProjectRecordingProfile: func(projectID, profileID string) (*recordingprofile.Profile, error) {
				return nil, nil
			},
		},
	}
	info := &recordbridge.BrowserRecordingSession{
		ID:        session.ID,
		Name:      "Paired Checkout Recording",
		ProjectID: "default",
	}
	res, err := svc.CreateHTTPScriptFromHARAtLevel(session, info, info.Name, recbrowser.HTTPScriptLevelURL, opts)
	if err != nil {
		t.Fatalf("CreateHTTPScriptFromHARAtLevel: %v", err)
	}
	return res
}

func buildPairedWorkflowInputs(t *testing.T, origin string) pairedWorkflowInputs {
	t.Helper()
	t0 := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(100 * time.Millisecond)
	t2 := t0.Add(200 * time.Millisecond)
	t2Note := t0.Add(250 * time.Millisecond)
	t3 := t0.Add(300 * time.Millisecond)
	t4 := t0.Add(400 * time.Millisecond)

	recLoginCode := "RecLoginCode1111"
	recSessionCookie := "RecSessionCookie2222"
	recXsrfToken := "RecXsrfToken3333"
	recCsrfFormToken := "RecCsrfFormToken4444"

	callbackURL := origin + "/login/callback?login_code=" + recLoginCode + "&mode=sso"
	callbackHTML := `<html><head><title>Order Portal</title></head><body><form action="/orders/submit" method="POST"><input type="hidden" name="csrf_token" value="` + recCsrfFormToken + `"></form></body></html>`
	submitBody := "csrf_token=" + recCsrfFormToken + "&item=widget&qty=2"
	confirmedHTML := `<html><head><title>Order Confirmed</title></head><body>order_status=CONFIRMED</body></html>`

	// 1. Private proxy recorder.Session
	recSession := &recorder.Session{
		ID:        "sess-paired-1",
		Name:      "Paired Checkout Recording",
		StartedAt: t0,
		StoppedAt: &t4,
		BaseURL:   origin,
		Markers: []marker.Marker{
			{Kind: marker.KindTransactionStart, Name: "checkout_flow", Timestamp: t0.Add(50 * time.Millisecond)},
			{Kind: marker.KindNote, Text: "submit order after login", Timestamp: t2Note},
			{Kind: marker.KindTransactionEnd, Name: "checkout_flow", Timestamp: t4},
		},
		Requests: []recorder.RecordedRequest{
			{
				Seq:                 1,
				Timestamp:           t1,
				Method:              "GET",
				RequestHTTPVersion:  "HTTP/1.1",
				ResponseHTTPVersion: "HTTP/1.1",
				URL:                 origin + "/login/start",
				Headers:             map[string]string{"X-Client-Flow": "checkout"},
				HeadersMulti:        map[string][]string{"X-Client-Flow": {"checkout"}},
				StatusCode:          302,
				ResponseTimeMs:      12,
				ResponseHeaders: map[string]string{
					"Location":     callbackURL,
					"Content-Type": "text/html; charset=utf-8",
					"Set-Cookie":   "xsrf_token=" + recXsrfToken + "; Path=/",
				},
				ResponseHeadersMulti: map[string][]string{
					"Location":     {callbackURL},
					"Set-Cookie":   {"session_id=" + recSessionCookie + "; Path=/; HttpOnly", "xsrf_token=" + recXsrfToken + "; Path=/"},
					"Content-Type": {"text/html; charset=utf-8"},
				},
				ResponseBody:    "Redirecting",
				ContentType:     "text/html; charset=utf-8",
				TransactionName: "checkout_flow",
			},
			{
				Seq:                 2,
				Timestamp:           t2,
				Method:              "GET",
				RequestHTTPVersion:  "HTTP/1.1",
				ResponseHTTPVersion: "HTTP/1.1",
				URL:                 callbackURL,
				Headers: map[string]string{
					"Cookie": "session_id=" + recSessionCookie + "; xsrf_token=" + recXsrfToken,
				},
				HeadersMulti: map[string][]string{
					"Cookie": {"session_id=" + recSessionCookie + "; xsrf_token=" + recXsrfToken},
				},
				StatusCode:     200,
				ResponseTimeMs: 15,
				ResponseHeaders: map[string]string{
					"Content-Type": "text/html; charset=utf-8",
				},
				ResponseHeadersMulti: map[string][]string{
					"Content-Type": {"text/html; charset=utf-8"},
				},
				ResponseBody:    callbackHTML,
				ContentType:     "text/html; charset=utf-8",
				TransactionName: "checkout_flow",
			},
			{
				Seq:                 3,
				Timestamp:           t3,
				Method:              "POST",
				RequestHTTPVersion:  "HTTP/1.1",
				ResponseHTTPVersion: "HTTP/1.1",
				URL:                 origin + "/orders/submit?login_code=" + recLoginCode,
				Headers: map[string]string{
					"Content-Type":    "application/x-www-form-urlencoded",
					"Cookie":          "session_id=" + recSessionCookie + "; xsrf_token=" + recXsrfToken,
					"X-CSRF-Token":    recXsrfToken,
					"X-Duplicate-Tag": "alpha, beta",
				},
				HeadersMulti: map[string][]string{
					"Content-Type":    {"application/x-www-form-urlencoded"},
					"Cookie":          {"session_id=" + recSessionCookie + "; xsrf_token=" + recXsrfToken},
					"X-CSRF-Token":    {recXsrfToken},
					"X-Duplicate-Tag": {"alpha", "beta"},
				},
				Body:           submitBody,
				ContentType:    "application/x-www-form-urlencoded",
				StatusCode:     200,
				ResponseTimeMs: 18,
				ResponseHeaders: map[string]string{
					"Content-Type": "text/html; charset=utf-8",
				},
				ResponseHeadersMulti: map[string][]string{
					"Content-Type": {"text/html; charset=utf-8"},
				},
				ResponseBody:    confirmedHTML,
				TransactionName: "checkout_flow",
			},
		},
	}

	// 2. Equivalent full HAR 1.2 document
	harMap := map[string]any{
		"log": map[string]any{
			"version": "1.2",
			"creator": map[string]any{"name": "Paired HAR Fixture", "version": "1.0"},
			"entries": []map[string]any{
				{
					"startedDateTime": t1.Format(time.RFC3339Nano),
					"time":            12,
					"request": map[string]any{
						"method":      "GET",
						"url":         origin + "/login/start",
						"httpVersion": "HTTP/1.1",
						"headers": []map[string]any{
							{"name": "X-Client-Flow", "value": "checkout"},
						},
					},
					"response": map[string]any{
						"status":      302,
						"httpVersion": "HTTP/1.1",
						"headers": []map[string]any{
							{"name": "Location", "value": callbackURL},
							{"name": "Set-Cookie", "value": "session_id=" + recSessionCookie + "; Path=/; HttpOnly"},
							{"name": "Set-Cookie", "value": "xsrf_token=" + recXsrfToken + "; Path=/"},
							{"name": "Content-Type", "value": "text/html; charset=utf-8"},
						},
						"content": map[string]any{
							"size":     len("Redirecting"),
							"mimeType": "text/html; charset=utf-8",
							"text":     "Redirecting",
						},
					},
				},
				{
					"startedDateTime": t2.Format(time.RFC3339Nano),
					"time":            15,
					"request": map[string]any{
						"method":      "GET",
						"url":         callbackURL,
						"httpVersion": "HTTP/1.1",
						"headers": []map[string]any{
							{"name": "Cookie", "value": "session_id=" + recSessionCookie + "; xsrf_token=" + recXsrfToken},
						},
					},
					"response": map[string]any{
						"status":      200,
						"httpVersion": "HTTP/1.1",
						"headers": []map[string]any{
							{"name": "Content-Type", "value": "text/html; charset=utf-8"},
						},
						"content": map[string]any{
							"size":     len(callbackHTML),
							"mimeType": "text/html; charset=utf-8",
							"text":     callbackHTML,
						},
					},
				},
				{
					"startedDateTime": t3.Format(time.RFC3339Nano),
					"time":            18,
					"request": map[string]any{
						"method":      "POST",
						"url":         origin + "/orders/submit?login_code=" + recLoginCode,
						"httpVersion": "HTTP/1.1",
						"headers": []map[string]any{
							{"name": "Content-Type", "value": "application/x-www-form-urlencoded"},
							{"name": "Cookie", "value": "session_id=" + recSessionCookie + "; xsrf_token=" + recXsrfToken},
							{"name": "X-CSRF-Token", "value": recXsrfToken},
							{"name": "X-Duplicate-Tag", "value": "alpha"},
							{"name": "X-Duplicate-Tag", "value": "beta"},
						},
						"postData": map[string]any{
							"mimeType": "application/x-www-form-urlencoded",
							"text":     submitBody,
						},
					},
					"response": map[string]any{
						"status":      200,
						"httpVersion": "HTTP/1.1",
						"headers": []map[string]any{
							{"name": "Content-Type", "value": "text/html; charset=utf-8"},
						},
						"content": map[string]any{
							"size":     len(confirmedHTML),
							"mimeType": "text/html; charset=utf-8",
							"text":     confirmedHTML,
						},
					},
				},
			},
		},
	}
	harBytes, err := json.Marshal(harMap)
	if err != nil {
		t.Fatal(err)
	}
	fullHAR := string(harBytes)

	// 2b. Browser recorder session with embedded side-car HAR for recordbridge companion HTTP conversion
	browserSession := &recbrowser.Session{
		ID:        "browser-sess-paired-1",
		StartURL:  origin + "/login/start",
		StartedAt: t0,
		StoppedAt: t4,
		HAR:       append([]byte(nil), harBytes...),
		Markers: []recbrowser.Marker{
			{Kind: marker.KindTransactionStart, Name: "checkout_flow", Timestamp: t0.Add(50 * time.Millisecond)},
			{Kind: marker.KindNote, Text: "submit order after login", Timestamp: t2Note},
			{Kind: marker.KindTransactionEnd, Name: "checkout_flow", Timestamp: t4},
		},
		Steps: []recbrowser.Step{
			{Seq: 1, Kind: recbrowser.StepNavigate, URL: origin + "/login/start", Timestamp: t1},
			{Seq: 2, Kind: recbrowser.StepClick, URL: callbackURL, Timestamp: t3},
		},
	}

	// 3. LoadRunner URL-level web script archive (Action.c + Parity.usr + default.cfg)
	actionC := fmt.Sprintf(`Action()
{
	lr_start_transaction("checkout_flow");

	web_reg_save_param_ex("ParamName=LoginCode",
		"LB=login_code=",
		"RB=&",
		SEARCH_FILTERS,
		"Scope=Headers",
		LAST);

	web_reg_save_param_ex("ParamName=XsrfToken",
		"LB=xsrf_token=",
		"RB=;",
		SEARCH_FILTERS,
		"Scope=Headers",
		LAST);

	web_reg_save_param_ex("ParamName=CsrfToken",
		"LB=name=\"csrf_token\" value=\"",
		"RB=\"",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_reg_find("Text=Order Portal", LAST);

	web_add_header("X-Client-Flow", "checkout");

	web_url("login_start",
		"URL=%s/login/start",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	web_reg_find("Text=Order Confirmed", LAST);

	web_rest("submit_order",
		"URL=%s/orders/submit?login_code={LoginCode}",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded",
		"Body=csrf_token={CsrfToken}&item=widget&qty=2",
		HEADERS,
		"Name=X-CSRF-Token", "Value={XsrfToken}", ENDHEADER,
		"Name=X-Duplicate-Tag", "Value=alpha", ENDHEADER,
		"Name=X-Duplicate-Tag", "Value=beta", ENDHEADER,
		LAST);

	lr_end_transaction("checkout_flow", LR_AUTO);

	return 0;
}
`, origin, origin)

	lrZip := buildLRTestZip(t, map[string]string{
		"Parity.usr":  "[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction1=Action.c\n",
		"default.cfg": "[General]\n",
		"Action.c":    actionC,
	})

	return pairedWorkflowInputs{
		recorderSession: recSession,
		fullHAR:         fullHAR,
		browserSession:  browserSession,
		lrArchiveZip:    lrZip,
	}
}

func buildLRTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------
// Level 1 Tests: Normalized Executable Structure + Negative Detectors
// ---------------------------------------------------------------------------

func toSampleTransactions(markers []marker.Marker) []sample.TransactionWindow {
	windows := marker.Windows(markers)
	out := make([]sample.TransactionWindow, 0, len(windows))
	for _, w := range windows {
		out = append(out, sample.TransactionWindow(w))
	}
	return out
}

func toSampleNotes(markers []marker.Marker) []sample.TimedNote {
	notes := marker.Notes(markers)
	out := make([]sample.TimedNote, 0, len(notes))
	for _, n := range notes {
		out = append(out, sample.TimedNote(n))
	}
	return out
}

func TestLevel1_NormalizedExecutableEquivalenceAcrossEntrypoints(t *testing.T) {
	inputs := buildPairedWorkflowInputs(t, paritytest.Origin)

	// 1. Private proxy recorder session -> recorder.ConvertSessionWithOptions
	recConverted := recorder.ConvertSessionWithOptions(inputs.recorderSession, "Paired Checkout Recording", recorder.ConvertOptions{})

	// 2. Full HAR -> har.Convert (and sample.Convert under its un-overridden legacy default)
	txWindows := toSampleTransactions(inputs.recorderSession.Markers)
	timedNotes := toSampleNotes(inputs.recorderSession.Markers)
	harConverted, err := har.Convert([]byte(inputs.fullHAR), har.ConvertOptions{
		Transactions: txWindows,
		Notes:        timedNotes,
	})
	if err != nil {
		t.Fatalf("har.Convert: %v", err)
	}

	// Under legacy_v1, EntryPointImportSample defaults to CorrelationModeAll
	// (HighConfidenceOnly=false) while EntryPointRecorderProxy defaults to
	// CorrelationModeHighConfidence (HighConfidenceOnly=true). Because every
	// correlation in the paired checkout fixture is high-confidence, the
	// un-overridden legacy sample.Convert call still produces an equivalent
	// executable script while reporting its distinct legacy EffectiveSettings.
	sampleConverted, err := sample.Convert(sample.ImportRequest{
		Name:    "Paired Checkout Recording",
		Format:  "har",
		Content: inputs.fullHAR,
		Options: sample.ConvertOptions{
			Transactions: txWindows,
			Notes:        timedNotes,
		},
	})
	if err != nil {
		t.Fatalf("sample.Convert: %v", err)
	}
	if sampleConverted.EffectiveSettings == nil || sampleConverted.EffectiveSettings.HighConfidenceOnly {
		t.Fatalf("legacy sample.Convert EffectiveSettings = %#v, want HighConfidenceOnly=false", sampleConverted.EffectiveSettings)
	}

	// 3. Browser recorder companion HTTP conversion via recordbridge.Service.CreateHTTPScriptFromHARAtLevel
	browserConverted := convertBrowserCompanionHTTPScript(t, inputs.browserSession, recordbridge.BrowserHTTPScriptOptions{})

	if diffs := compareExecutableScripts("recorder.Session", recConverted.Script, "har.Convert", harConverted.Script); len(diffs) != 0 {
		t.Fatalf("Level 1 mismatch between recorder.Session and har.Convert:\n%s", strings.Join(diffs, "\n"))
	}
	if diffs := compareExecutableScripts("recorder.Session", recConverted.Script, "sample.Convert(legacy_default)", sampleConverted.Script); len(diffs) != 0 {
		t.Fatalf("Level 1 mismatch between recorder.Session and sample.Convert:\n%s", strings.Join(diffs, "\n"))
	}
	if diffs := compareExecutableScripts("recorder.Session", recConverted.Script, "recordbridge.CreateHTTPScriptFromHARAtLevel", browserConverted.Script); len(diffs) != 0 {
		t.Fatalf("Level 1 mismatch between recorder.Session and recordbridge.CreateHTTPScriptFromHARAtLevel:\n%s", strings.Join(diffs, "\n"))
	}

	t.Run("UnifiedV1ConversionPolicyEquivalence", func(t *testing.T) {
		recUnified := recorder.ConvertSessionWithOptions(inputs.recorderSession, "Paired Checkout Recording", recorder.ConvertOptions{
			ConversionPolicy: sample.ConversionPolicyUnifiedV1,
		})
		harUnified, err := har.Convert([]byte(inputs.fullHAR), har.ConvertOptions{
			ConversionPolicy: sample.ConversionPolicyUnifiedV1,
			Transactions:     txWindows,
			Notes:            timedNotes,
		})
		if err != nil {
			t.Fatalf("har.Convert unified_v1: %v", err)
		}
		sampleUnified, err := sample.Convert(sample.ImportRequest{
			Name:    "Paired Checkout Recording",
			Format:  "har",
			Content: inputs.fullHAR,
			Options: sample.ConvertOptions{
				ConversionPolicy: sample.ConversionPolicyUnifiedV1,
				Transactions:     txWindows,
				Notes:            timedNotes,
			},
		})
		if err != nil {
			t.Fatalf("sample.Convert unified_v1: %v", err)
		}
		browserUnified := convertBrowserCompanionHTTPScript(t, inputs.browserSession, recordbridge.BrowserHTTPScriptOptions{
			ConversionPolicy: sample.ConversionPolicyUnifiedV1,
		})

		for label, eff := range map[string]*sample.EffectiveSettings{
			"recorder.Session": recUnified.EffectiveSettings,
			"har.Convert":      harUnified.EffectiveSettings,
			"sample.Convert":   sampleUnified.EffectiveSettings,
			"recbrowser+HAR":   browserUnified.EffectiveSettings,
		} {
			if eff == nil || eff.ConversionPolicy != sample.ConversionPolicyUnifiedV1 {
				t.Fatalf("%s EffectiveSettings = %#v, want ConversionPolicy=%q", label, eff, sample.ConversionPolicyUnifiedV1)
			}
			if !eff.HighConfidenceOnly || eff.CorrelationMode != sample.CorrelationModeHighConfidence {
				t.Fatalf("%s EffectiveSettings = %#v, want HighConfidenceOnly=true and CorrelationMode=%q", label, eff, sample.CorrelationModeHighConfidence)
			}
		}

		if diffs := compareExecutableScripts("recorder.Session(unified_v1)", recUnified.Script, "har.Convert(unified_v1)", harUnified.Script); len(diffs) != 0 {
			t.Fatalf("Level 1 unified_v1 mismatch between recorder.Session and har.Convert:\n%s", strings.Join(diffs, "\n"))
		}
		if diffs := compareExecutableScripts("recorder.Session(unified_v1)", recUnified.Script, "sample.Convert(unified_v1)", sampleUnified.Script); len(diffs) != 0 {
			t.Fatalf("Level 1 unified_v1 mismatch between recorder.Session and sample.Convert:\n%s", strings.Join(diffs, "\n"))
		}
		if diffs := compareExecutableScripts("recorder.Session(unified_v1)", recUnified.Script, "recordbridge.CreateHTTPScriptFromHARAtLevel(unified_v1)", browserUnified.Script); len(diffs) != 0 {
			t.Fatalf("Level 1 unified_v1 mismatch between recorder.Session and recordbridge.CreateHTTPScriptFromHARAtLevel:\n%s", strings.Join(diffs, "\n"))
		}
	})

	// Also verify Level 1 structural equivalence between recorded-traffic conversion
	// and LoadRunner URL-level C conversion (enterprisearchive.ConvertZip) when
	// representing the same explicit 2-step portal + POST submission flow.
	t.Run("TwoWayExplicitStepsHARAndLoadRunnerArchive", func(t *testing.T) {
		t0 := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
		t1 := t0.Add(100 * time.Millisecond)
		t2 := t0.Add(200 * time.Millisecond)
		t3 := t0.Add(300 * time.Millisecond)
		portalHTML := `<html><head><title>Order Portal</title></head><body><form action="/orders/submit" method="POST"><input type="hidden" name="csrf_token" value="RecCsrfFormToken4444"></form></body></html>`
		confirmedHTML := `<html><head><title>Order Confirmed</title></head><body>order_status=CONFIRMED</body></html>`

		includeAssertions := false
		twoStepHARMap := map[string]any{
			"log": map[string]any{
				"version": "1.2",
				"creator": map[string]any{"name": "Paired 2-Step HAR", "version": "1.0"},
				"entries": []map[string]any{
					{
						"startedDateTime": t1.Format(time.RFC3339Nano),
						"time":            10,
						"request": map[string]any{
							"method":      "GET",
							"url":         paritytest.Origin + "/portal/form",
							"httpVersion": "HTTP/1.1",
							"headers": []map[string]any{
								{"name": "X-Client-Flow", "value": "checkout"},
							},
						},
						"response": map[string]any{
							"status":      200,
							"httpVersion": "HTTP/1.1",
							"headers": []map[string]any{
								{"name": "Set-Cookie", "value": "xsrf_token=RecXsrfToken3333; Path=/"},
								{"name": "Content-Type", "value": "text/html; charset=utf-8"},
							},
							"content": map[string]any{
								"size":     len(portalHTML),
								"mimeType": "text/html; charset=utf-8",
								"text":     portalHTML,
							},
						},
					},
					{
						"startedDateTime": t2.Format(time.RFC3339Nano),
						"time":            12,
						"request": map[string]any{
							"method":      "POST",
							"url":         paritytest.Origin + "/orders/submit",
							"httpVersion": "HTTP/1.1",
							"headers": []map[string]any{
								{"name": "Content-Type", "value": "application/x-www-form-urlencoded"},
								{"name": "X-CSRF-Token", "value": "RecXsrfToken3333"},
								{"name": "X-Duplicate-Tag", "value": "alpha"},
								{"name": "X-Duplicate-Tag", "value": "beta"},
							},
							"postData": map[string]any{
								"mimeType": "application/x-www-form-urlencoded",
								"text":     "csrf_token=RecCsrfFormToken4444&item=widget&qty=2",
							},
						},
						"response": map[string]any{
							"status":      200,
							"httpVersion": "HTTP/1.1",
							"headers": []map[string]any{
								{"name": "Content-Type", "value": "text/html; charset=utf-8"},
							},
							"content": map[string]any{
								"size":     len(confirmedHTML),
								"mimeType": "text/html; charset=utf-8",
								"text":     confirmedHTML,
							},
						},
					},
				},
			},
		}
		rawHAR, err := json.Marshal(twoStepHARMap)
		if err != nil {
			t.Fatal(err)
		}
		harRes, err := har.Convert(rawHAR, har.ConvertOptions{
			IncludeAssertions: &includeAssertions,
			HTTPVersionMode:   "inherit",
			Transactions: toSampleTransactions([]marker.Marker{
				{Kind: marker.KindTransactionStart, Name: "portal_open", Timestamp: t0.Add(50 * time.Millisecond)},
				{Kind: marker.KindTransactionEnd, Name: "portal_open", Timestamp: t0.Add(150 * time.Millisecond)},
				{Kind: marker.KindTransactionStart, Name: "order_submit", Timestamp: t0.Add(160 * time.Millisecond)},
				{Kind: marker.KindTransactionEnd, Name: "order_submit", Timestamp: t3},
			}),
		})
		if err != nil {
			t.Fatalf("har.Convert: %v", err)
		}

		lrActionC := fmt.Sprintf(`Action()
{
	lr_start_transaction("portal_open");

	web_reg_save_param_regexp("ParamName=CsrfToken",
		"RegExp=(?i)<input[^>]*\\bname=\"csrf_token\"[^>]*\\bvalue=\"([^\"]{4,})\"",
		"Group=1",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_reg_save_param_regexp("ParamName=XsrfToken",
		"RegExp=(?m)^(?i:Set-Cookie):[ \\t]*xsrf_token=[ \\t]*\"?([^\";\\s,]+)\"?(?:[ \\t]*;|[ \\t]*$)",
		"Group=1",
		SEARCH_FILTERS,
		"Scope=Headers",
		LAST);

	web_add_header("X-Client-Flow", "checkout");

	web_url("portal_form",
		"URL=%s/portal/form",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	lr_end_transaction("portal_open", LR_AUTO);

	lr_start_transaction("order_submit");

	web_rest("submit_order",
		"URL=%s/orders/submit",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded",
		"Body=csrf_token={CsrfToken}&item=widget&qty=2",
		HEADERS,
		"Name=Content-Type", "Value=application/x-www-form-urlencoded", ENDHEADER,
		"Name=X-CSRF-Token", "Value={XsrfToken}", ENDHEADER,
		"Name=X-Duplicate-Tag", "Value=alpha", ENDHEADER,
		"Name=X-Duplicate-Tag", "Value=beta", ENDHEADER,
		LAST);

	lr_end_transaction("order_submit", LR_AUTO);

	return 0;
}
`, paritytest.Origin, paritytest.Origin)

		lrZip := buildLRTestZip(t, map[string]string{
			"Action.c": lrActionC,
		})
		lrRes, err := enterprisearchive.ConvertZip(lrZip, enterprisearchive.ConvertOptions{
			PreserveTransactions: true,
			DefaultThinkTimeMs:   1000,
		})
		if err != nil {
			t.Fatalf("ConvertZip: %v", err)
		}

		if diffs := compareExecutableScripts("har.Convert", harRes.Script, "enterprisearchive.ConvertZip", lrRes.Script); len(diffs) != 0 {
			t.Fatalf("Level 1 mismatch between har.Convert and enterprisearchive.ConvertZip:\n%s", strings.Join(diffs, "\n"))
		}
	})
}

func TestLevel1_ComparatorNegativeDetectors(t *testing.T) {
	inputs := buildPairedWorkflowInputs(t, paritytest.Origin)
	baseRes := recorder.ConvertSessionWithOptions(inputs.recorderSession, "Paired Checkout Recording", recorder.ConvertOptions{})
	cloneScript := func() *dsl.Script {
		raw, err := json.Marshal(baseRes.Script)
		if err != nil {
			t.Fatal(err)
		}
		var out dsl.Script
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return &out
	}

	t.Run("DetectsAlteredExecutionBlockerGate", func(t *testing.T) {
		mutated := cloneScript()
		for i := range mutated.Actions {
			if mutated.Actions[i].Method == "POST" {
				if mutated.Actions[i].Metadata == nil {
					mutated.Actions[i].Metadata = map[string]any{}
				}
				mutated.Actions[i].Metadata["execution_blocker"] = "restored_body_required"
				break
			}
		}
		diffs := compareExecutableScripts("baseline", baseRes.Script, "gated", mutated)
		if len(diffs) == 0 || !strings.Contains(strings.Join(diffs, "\n"), "execution_blocker") {
			t.Fatalf("expected comparator to fail on execution_blocker change, got: %v", diffs)
		}
	})

	t.Run("DetectsAlteredHeaderOccurrenceOrderAndCommaCollapse", func(t *testing.T) {
		swapped := cloneScript()
		for i := range swapped.Actions {
			if swapped.Actions[i].Method == "POST" {
				// Swap the two X-Duplicate-Tag occurrences ("alpha", "beta" -> "beta", "alpha").
				for j := 0; j+1 < len(swapped.Actions[i].HeaderList); j++ {
					if swapped.Actions[i].HeaderList[j].Name == "X-Duplicate-Tag" &&
						swapped.Actions[i].HeaderList[j+1].Name == "X-Duplicate-Tag" {
						swapped.Actions[i].HeaderList[j], swapped.Actions[i].HeaderList[j+1] =
							swapped.Actions[i].HeaderList[j+1], swapped.Actions[i].HeaderList[j]
						break
					}
				}
			}
		}
		diffs := compareExecutableScripts("baseline", baseRes.Script, "swapped_headers", swapped)
		if len(diffs) == 0 || !strings.Contains(strings.Join(diffs, "\n"), "X-Duplicate-Tag") {
			t.Fatalf("expected comparator to fail when duplicate header occurrence order is swapped, got: %v", diffs)
		}

		collapsed := cloneScript()
		for i := range collapsed.Actions {
			if collapsed.Actions[i].Method == "POST" {
				var kept dsl.HeaderList
				replaced := false
				for _, h := range collapsed.Actions[i].HeaderList {
					if h.Name == "X-Duplicate-Tag" {
						if !replaced {
							kept = append(kept, dsl.HeaderEntry{Name: "X-Duplicate-Tag", Value: "alpha, beta"})
							replaced = true
						}
						continue
					}
					kept = append(kept, h)
				}
				collapsed.Actions[i].HeaderList = kept
			}
		}
		diffsCollapsed := compareExecutableScripts("baseline", baseRes.Script, "collapsed_headers", collapsed)
		if len(diffsCollapsed) == 0 || !strings.Contains(strings.Join(diffsCollapsed, "\n"), "X-Duplicate-Tag") {
			t.Fatalf("expected comparator to fail when duplicate headers are collapsed into one comma-joined header, got: %v", diffsCollapsed)
		}
	})

	t.Run("DetectsAlteredBodyBytesAndEncoding", func(t *testing.T) {
		bodyMutated := cloneScript()
		for i := range bodyMutated.Actions {
			if bodyMutated.Actions[i].Method == "POST" {
				bodyMutated.Actions[i].Body = strings.Replace(bodyMutated.Actions[i].Body, "qty=2", "qty=3", 1)
			}
		}
		if diffs := compareExecutableScripts("baseline", baseRes.Script, "body_bytes", bodyMutated); len(diffs) == 0 {
			t.Fatal("expected comparator to fail when request body bytes are altered")
		}

		encMutated := cloneScript()
		for i := range encMutated.Actions {
			if encMutated.Actions[i].Method == "POST" {
				encMutated.Actions[i].BodyEncoding = "base64"
			}
		}
		if diffs := compareExecutableScripts("baseline", baseRes.Script, "body_encoding", encMutated); len(diffs) == 0 {
			t.Fatal("expected comparator to fail when body_encoding is altered")
		}
	})

	t.Run("DetectsAlteredTransactionPlacement", func(t *testing.T) {
		txMutated := cloneScript()
		// Find the POST request action and the immediately following source_transaction end marker.
		postIdx := -1
		endTxIdx := -1
		for i, act := range txMutated.Actions {
			if act.Method == "POST" {
				postIdx = i
			} else if postIdx >= 0 && act.Type == "source_transaction" {
				endTxIdx = i
				break
			}
		}
		if postIdx < 0 || endTxIdx <= postIdx {
			t.Fatalf("expected POST action followed by source_transaction end marker, got postIdx=%d endTxIdx=%d", postIdx, endTxIdx)
		}
		txMutated.Actions[postIdx], txMutated.Actions[endTxIdx] = txMutated.Actions[endTxIdx], txMutated.Actions[postIdx]
		if diffs := compareExecutableScripts("baseline", baseRes.Script, "tx_moved", txMutated); len(diffs) == 0 {
			t.Fatal("expected comparator to fail when transaction boundary placement is altered")
		}
	})

	t.Run("DetectsAlteredVariableProducerConsumerBinding", func(t *testing.T) {
		rebound := cloneScript()
		// Discover an existing extractor variable name produced on an earlier GET action
		// so the mutation rebinds to a genuinely produced variable rather than an unbound name.
		var altVar string
		postIdx := -1
		for i, act := range rebound.Actions {
			if act.Method == "GET" && len(act.Extractors) > 0 && altVar == "" {
				altVar = act.Extractors[0].Name
			}
			if act.Method == "POST" {
				postIdx = i
				break
			}
		}
		if altVar == "" || postIdx < 0 {
			t.Fatalf("expected earlier GET extractor and POST action, got altVar=%q postIdx=%d", altVar, postIdx)
		}
		if strings.Contains(rebound.Actions[postIdx].Body, "{{var."+altVar+"}}") {
			t.Fatalf("expected POST body not to already bind csrf_token to %q, body=%q", altVar, rebound.Actions[postIdx].Body)
		}
		rebound.Actions[postIdx].Body = "csrf_token={{var." + altVar + "}}&item=widget&qty=2"

		normRebound, err := normalizeExecutableScript(rebound)
		if err != nil {
			t.Fatalf("normalizeExecutableScript(rebound): %v", err)
		}
		if len(normRebound.UnboundVariables) != 0 {
			t.Fatalf("expected rebound script to have 0 unbound variables, got %v", normRebound.UnboundVariables)
		}

		diffs := compareExecutableScripts("baseline", baseRes.Script, "rebound_var", rebound)
		joinedDiffs := strings.Join(diffs, "\n")
		if len(diffs) == 0 || !strings.Contains(joinedDiffs, "variable bindings") || !strings.Contains(joinedDiffs, fmt.Sprintf("actions[%d].body", postIdx)) {
			t.Fatalf("expected comparator to report variable bindings diff at actions[%d].body, got: %v", postIdx, diffs)
		}

		// Also verify pipelined variable templates ({{var.name | urlencode}}) bind to the producer.
		pipelined := cloneScript()
		pipelined.Actions[postIdx].Body = "csrf_token={{var." + altVar + " | urlencode}}&item=widget&qty=2"
		normPipelined, err := normalizeExecutableScript(pipelined)
		if err != nil {
			t.Fatalf("normalizeExecutableScript(pipelined): %v", err)
		}
		if len(normPipelined.UnboundVariables) != 0 {
			t.Fatalf("expected pipelined variable reference to bind to producer, got unbound: %v", normPipelined.UnboundVariables)
		}
		if !strings.Contains(normPipelined.Actions[postIdx].Body, "| urlencode}}") {
			t.Fatalf("expected normalized body to preserve pipeline filter, got %q", normPipelined.Actions[postIdx].Body)
		}

		unbound := cloneScript()
		unbound.Actions[postIdx].Body = "csrf_token={{var.nonexistent_token}}&item=widget&qty=2"
		diffsUnbound := compareExecutableScripts("baseline", baseRes.Script, "unbound_var", unbound)
		if len(diffsUnbound) == 0 || !strings.Contains(strings.Join(diffsUnbound, "\n"), "unbound variable") {
			t.Fatalf("expected comparator to fail on unbound variable reference, got: %v", diffsUnbound)
		}
	})

	t.Run("DetectsAlteredConfigDataSourcesAndRunLogicActionMetadata", func(t *testing.T) {
		cfgMutated := cloneScript()
		cfgMutated.Config.ThinkTime.MeanMs = 2500
		diffsCfg := compareExecutableScripts("baseline", baseRes.Script, "cfg_mutated", cfgMutated)
		if len(diffsCfg) == 0 || !strings.Contains(strings.Join(diffsCfg, "\n"), "config:") {
			t.Fatalf("expected comparator to fail on Config change, got: %v", diffsCfg)
		}

		dsMutated := cloneScript()
		dsMutated.DataSources = append(dsMutated.DataSources, dsl.DataSource{
			Name: "users_csv",
			Type: "csv",
			Path: "users.csv",
		})
		diffsDS := compareExecutableScripts("baseline", baseRes.Script, "ds_mutated", dsMutated)
		if len(diffsDS) == 0 || !strings.Contains(strings.Join(diffsDS, "\n"), "data_sources:") {
			t.Fatalf("expected comparator to fail on DataSources change, got: %v", diffsDS)
		}

		rlMutated := cloneScript()
		for i := range rlMutated.Actions {
			if rlMutated.Actions[i].Method == "POST" {
				if rlMutated.Actions[i].Metadata == nil {
					rlMutated.Actions[i].Metadata = map[string]any{}
				}
				rlMutated.Actions[i].Metadata[dsl.RunLogicActionMetadataKey] = "Action2"
				break
			}
		}
		diffsRL := compareExecutableScripts("baseline", baseRes.Script, "run_logic_action_mutated", rlMutated)
		if len(diffsRL) == 0 || !strings.Contains(strings.Join(diffsRL, "\n"), dsl.RunLogicActionMetadataKey) {
			t.Fatalf("expected comparator to fail on run_logic_action metadata change, got: %v", diffsRL)
		}
	})
}

// ---------------------------------------------------------------------------
// Level 2 Tests: Wire HTTP Behavior Equivalence via paritytest.Server
// ---------------------------------------------------------------------------

func TestLevel2_PairedWorkflowWireEquivalenceAndFailureDetection(t *testing.T) {
	// Fresh runtime tokens served by paritytest.Server — all distinct from the
	// recorded literals (RecLoginCode1111, RecSessionCookie2222, RecXsrfToken3333,
	// RecCsrfFormToken4444) so any un-correlated literal replay fails on the wire.
	liveLoginCode := "LiveLoginCode7777"
	liveSessionCookie := "LiveSession8888"
	liveXsrfToken := "LiveXsrf9999"
	liveCsrfFormToken := "LiveCsrf6666"

	scriptedResponses := []paritytest.Response{
		{
			Method: "GET",
			Path:   "/login/start",
			Status: 302,
			HeaderList: dsl.HeaderList{
				{Name: "Location", Value: paritytest.Origin + "/login/callback?login_code=" + liveLoginCode + "&mode=sso"},
				{Name: "Set-Cookie", Value: "session_id=" + liveSessionCookie + "; Path=/; HttpOnly"},
				{Name: "Set-Cookie", Value: "xsrf_token=" + liveXsrfToken + "; Path=/"},
				{Name: "Content-Type", Value: "text/html; charset=utf-8"},
			},
			Body: "Redirecting",
		},
		{
			Method: "GET",
			Path:   "/login/callback",
			Status: 200,
			Headers: map[string]string{
				"Content-Type": "text/html; charset=utf-8",
			},
			Body: `<html><head><title>Order Portal</title></head><body><form action="/orders/submit" method="POST"><input type="hidden" name="csrf_token" value="` + liveCsrfFormToken + `"></form></body></html>`,
		},
		{
			Method: "POST",
			Path:   "/orders/submit",
			Status: 200,
			Headers: map[string]string{
				"Content-Type": "text/html; charset=utf-8",
			},
			Body: `<html><head><title>Order Confirmed</title></head><body>order_status=CONFIRMED</body></html>`,
		},
	}

	expectedWireRequests := []paritytest.ExpectedRequest{
		{
			Method: "GET",
			Path:   "/login/start",
			Query:  strPtr(""),
			HeaderOccurrences: map[string][]string{
				"X-Client-Flow": {"checkout"},
			},
		},
		{
			Method: "GET",
			Path:   "/login/callback",
			Query:  strPtr("login_code=" + liveLoginCode + "&mode=sso"),
			HeaderCounts: map[string]int{
				"Cookie:session_id": 1,
				"Cookie:xsrf_token": 1,
			},
			HeaderContains: map[string]string{
				"Cookie": "session_id=" + liveSessionCookie,
			},
		},
		{
			Method: "POST",
			Path:   "/orders/submit",
			Query:  strPtr("login_code=" + liveLoginCode),
			HeaderOccurrences: map[string][]string{
				"Content-Type":    {"application/x-www-form-urlencoded"},
				"X-Csrf-Token":    {liveXsrfToken},
				"X-Duplicate-Tag": {"alpha", "beta"},
			},
			HeaderCounts: map[string]int{
				"Cookie:session_id": 1,
				"Cookie:xsrf_token": 1,
			},
			HeaderContains: map[string]string{
				"Cookie": "xsrf_token=" + liveXsrfToken,
			},
			Body: strPtr("csrf_token=" + liveCsrfFormToken + "&item=widget&qty=2"),
		},
	}

	dependencies := []paritytest.DependencyRule{
		{
			Prerequisite: "GET /login/start",
			Dependents:   []string{"GET /login/callback", "POST /orders/submit"},
		},
		{
			Prerequisite: "GET /login/callback",
			Dependents:   []string{"POST /orders/submit"},
		},
	}

	type entrypointCase struct {
		name    string
		convert func(t *testing.T, serverOrigin string) (*dsl.Script, []string)
	}

	cases := []entrypointCase{
		{
			name: "PrivateProxyRecorderSession",
			convert: func(t *testing.T, serverOrigin string) (*dsl.Script, []string) {
				in := buildPairedWorkflowInputs(t, serverOrigin)
				res := recorder.ConvertSessionWithOptions(in.recorderSession, "Paired Checkout Recording", recorder.ConvertOptions{})
				return res.Script, res.Warnings
			},
		},
		{
			name: "FullHARConvert",
			convert: func(t *testing.T, serverOrigin string) (*dsl.Script, []string) {
				in := buildPairedWorkflowInputs(t, serverOrigin)
				res, err := har.Convert([]byte(in.fullHAR), har.ConvertOptions{
					Transactions: toSampleTransactions(in.recorderSession.Markers),
					Notes:        toSampleNotes(in.recorderSession.Markers),
				})
				if err != nil {
					t.Fatalf("har.Convert: %v", err)
				}
				return res.Script, res.Warnings
			},
		},
		{
			name: "BrowserRecorderCompanionHAR",
			convert: func(t *testing.T, serverOrigin string) (*dsl.Script, []string) {
				in := buildPairedWorkflowInputs(t, serverOrigin)
				res := convertBrowserCompanionHTTPScript(t, in.browserSession, recordbridge.BrowserHTTPScriptOptions{})
				return res.Script, res.Warnings
			},
		},
		{
			name: "PrivateProxyRecorderSession_UnifiedV1",
			convert: func(t *testing.T, serverOrigin string) (*dsl.Script, []string) {
				in := buildPairedWorkflowInputs(t, serverOrigin)
				res := recorder.ConvertSessionWithOptions(in.recorderSession, "Paired Checkout Recording", recorder.ConvertOptions{
					ConversionPolicy: sample.ConversionPolicyUnifiedV1,
				})
				return res.Script, res.Warnings
			},
		},
		{
			name: "FullHARConvert_UnifiedV1",
			convert: func(t *testing.T, serverOrigin string) (*dsl.Script, []string) {
				in := buildPairedWorkflowInputs(t, serverOrigin)
				res, err := har.Convert([]byte(in.fullHAR), har.ConvertOptions{
					ConversionPolicy: sample.ConversionPolicyUnifiedV1,
					Transactions:     toSampleTransactions(in.recorderSession.Markers),
					Notes:            toSampleNotes(in.recorderSession.Markers),
				})
				if err != nil {
					t.Fatalf("har.Convert unified_v1: %v", err)
				}
				return res.Script, res.Warnings
			},
		},
		{
			name: "BrowserRecorderCompanionHAR_UnifiedV1",
			convert: func(t *testing.T, serverOrigin string) (*dsl.Script, []string) {
				in := buildPairedWorkflowInputs(t, serverOrigin)
				res := convertBrowserCompanionHTTPScript(t, in.browserSession, recordbridge.BrowserHTTPScriptOptions{
					ConversionPolicy: sample.ConversionPolicyUnifiedV1,
				})
				return res.Script, res.Warnings
			},
		},
		{
			name: "LoadRunnerURLModeWebArchive",
			convert: func(t *testing.T, serverOrigin string) (*dsl.Script, []string) {
				in := buildPairedWorkflowInputs(t, serverOrigin)
				res, err := enterprisearchive.ConvertZip(in.lrArchiveZip, enterprisearchive.ConvertOptions{
					PreserveTransactions: true,
				})
				if err != nil {
					t.Fatalf("enterprisearchive.ConvertZip: %v", err)
				}
				return res.Script, res.Warnings
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Happy path with fresh dynamic server tokens: proves correlation,
			// multi-Set-Cookie jar + header extraction, and duplicate header occurrences.
			server := paritytest.NewServer(scriptedResponses)
			defer server.Close()

			script, warnings := tc.convert(t, server.URL)
			exp := &paritytest.Expectation{
				Description:  "Paired HTTP workflow equivalence",
				Source:       tc.name,
				Outcome:      paritytest.OutcomeReplay,
				Requests:     expectedWireRequests,
				Dependencies: dependencies,
			}
			if problems := paritytest.CheckOutcome(context.Background(), server, exp, script, warnings); len(problems) != 0 {
				t.Fatalf("%s happy-path wire parity failed:\n%s", tc.name, strings.Join(problems, "\n"))
			}

			// 2. Broken final page content: proves page/text assertion failure is caught.
			brokenPageResponses := append([]paritytest.Response(nil), scriptedResponses...)
			brokenPageResponses[2] = paritytest.Response{
				Method:  "POST",
				Path:    "/orders/submit",
				Status:  200,
				Headers: map[string]string{"Content-Type": "text/html; charset=utf-8"},
				Body:    `<html><head><title>Payment Declined</title></head><body>order_status=REJECTED</body></html>`,
			}
			brokenPageServer := paritytest.NewServer(brokenPageResponses)
			defer brokenPageServer.Close()

			scriptForBrokenPage, _ := tc.convert(t, brokenPageServer.URL)
			report, err := paritytest.ReplayOnce(context.Background(), scriptForBrokenPage, nil, nil)
			if err != nil {
				t.Fatalf("%s broken-page replay error: %v", tc.name, err)
			}
			if report == nil || report.Success {
				t.Fatalf("%s replay unexpectedly succeeded against broken page response", tc.name)
			}

			// 3. Missing dynamic CSRF token on /login/callback: proves failed extraction
			// aborts before sending a bogus literal or empty token to /orders/submit.
			missingTokenResponses := append([]paritytest.Response(nil), scriptedResponses...)
			missingTokenResponses[1] = paritytest.Response{
				Method:  "GET",
				Path:    "/login/callback",
				Status:  200,
				Headers: map[string]string{"Content-Type": "text/html; charset=utf-8"},
				Body:    `<html><head><title>Order Portal</title></head><body><form action="/orders/submit" method="POST"></form></body></html>`,
			}
			missingTokenServer := paritytest.NewServer(missingTokenResponses)
			defer missingTokenServer.Close()

			scriptForMissingToken, _ := tc.convert(t, missingTokenServer.URL)
			missingReport, err := paritytest.ReplayOnce(context.Background(), scriptForMissingToken, nil, nil)
			if err != nil {
				t.Fatalf("%s missing-token replay error: %v", tc.name, err)
			}
			if missingReport == nil || missingReport.Success {
				t.Fatalf("%s replay unexpectedly succeeded when csrf_token was missing from server response", tc.name)
			}
			for _, rec := range missingTokenServer.Recorded() {
				if rec.Path == "/orders/submit" {
					t.Fatalf("%s sent POST /orders/submit despite failed csrf_token extraction", tc.name)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Level 3 Tests: Multi-Iteration Lifecycle & Concurrency Proof Lane
// ---------------------------------------------------------------------------

func TestLevel3_LifecycleConcurrencyAndTransactionProofLane(t *testing.T) {
	server := paritytest.NewServer([]paritytest.Response{
		{
			Method:  "GET",
			Path:    "/lifecycle/init",
			Status:  200,
			Headers: map[string]string{"Content-Type": "text/plain"},
			HeaderList: dsl.HeaderList{
				{Name: "Set-Cookie", Value: "boot_session=BootSess42; Path=/"},
			},
			Body: "boot_token=BootTok99;",
		},
		{
			Method:  "GET",
			Path:    "/lifecycle/page",
			Status:  200,
			Headers: map[string]string{"Content-Type": "text/html"},
			Body:    `<html><head><title>Workspace</title></head><body>page_nonce=IterNonce1;</body></html>`,
		},
		{
			Method:  "GET",
			Path:    "/lifecycle/page",
			Status:  200,
			Headers: map[string]string{"Content-Type": "text/html"},
			Body:    `<html><head><title>Workspace</title></head><body>page_nonce=IterNonce2;</body></html>`,
		},
		{
			Method:       "GET",
			Path:         "/lifecycle/asset/a.css",
			Status:       200,
			Barrier:      "concurrent_assets",
			BarrierCount: 2,
			Headers:      map[string]string{"Content-Type": "text/css"},
			Body:         "body { color: #111; }",
		},
		{
			Method:       "GET",
			Path:         "/lifecycle/asset/b.js",
			Status:       200,
			Barrier:      "concurrent_assets",
			BarrierCount: 2,
			Headers:      map[string]string{"Content-Type": "application/javascript"},
			Body:         "console.log('ready');",
		},
		{
			Method:  "POST",
			Path:    "/lifecycle/commit",
			Status:  200,
			Headers: map[string]string{"Content-Type": "text/plain"},
			Body:    "committed=ok",
		},
		{
			Method:  "POST",
			Path:    "/lifecycle/logout",
			Status:  200,
			Headers: map[string]string{"Content-Type": "text/plain"},
			Body:    "logged_out=ok",
		},
	})
	defer server.Close()

	// Build a multi-file LoadRunner archive with vuser_init.c, Action.c (explicit
	// transaction + prerequisite page + web_concurrent_start/end asset wave +
	// dependent commit), vuser_end.c, and RunLogic Iterations=2.
	vuserInitC := fmt.Sprintf(`vuser_init()
{
	web_reg_save_param_ex("ParamName=BootToken",
		"LB=boot_token=",
		"RB=;",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_url("bootstrap",
		"URL=%s/lifecycle/init",
		"Resource=0",
		"Mode=HTTP",
		LAST);

	return 0;
}
`, server.URL)

	actionC := fmt.Sprintf(`Action()
{
	lr_start_transaction("IterationCheckout");

	web_reg_save_param_ex("ParamName=PageNonce",
		"LB=page_nonce=",
		"RB=;",
		SEARCH_FILTERS,
		"Scope=Body",
		LAST);

	web_url("main_page",
		"URL=%s/lifecycle/page",
		"Resource=0",
		"Mode=HTTP",
		LAST);

	web_concurrent_start(NULL);

	web_url("asset_css",
		"URL=%s/lifecycle/asset/a.css",
		"Resource=1",
		"Mode=HTTP",
		LAST);

	web_url("asset_js",
		"URL=%s/lifecycle/asset/b.js",
		"Resource=1",
		"Mode=HTTP",
		LAST);

	web_concurrent_end(NULL);

	web_custom_request("commit_step",
		"URL=%s/lifecycle/commit",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded",
		"Body=boot={BootToken}&nonce={PageNonce}",
		LAST);

	lr_end_transaction("IterationCheckout", LR_AUTO);

	return 0;
}
`, server.URL, server.URL, server.URL, server.URL)

	vuserEndC := fmt.Sprintf(`vuser_end()
{
	web_custom_request("logout",
		"URL=%s/lifecycle/logout",
		"Method=POST",
		"EncType=application/x-www-form-urlencoded",
		"Body=boot={BootToken}",
		LAST);

	return 0;
}
`, server.URL)

	defaultCfg := `[General]
[RunLogic]
NumberOfIterations=2
`
	usrFile := `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
vuser_init=vuser_init.c
Action=Action.c
vuser_end=vuser_end.c
`

	lrZip := buildLRTestZip(t, map[string]string{
		"Lifecycle.usr": usrFile,
		"default.cfg":   defaultCfg,
		"vuser_init.c":  vuserInitC,
		"Action.c":      actionC,
		"vuser_end.c":   vuserEndC,
	})

	convRes, err := enterprisearchive.ConvertZip(lrZip, enterprisearchive.ConvertOptions{
		PreserveTransactions: true,
	})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	script := convRes.Script
	if script.Config.RunLogic.Iterations != 2 {
		t.Fatalf("converted script iterations = %d, want 2", script.Config.RunLogic.Iterations)
	}
	if len(script.Init) != 1 || len(script.Teardown) != 1 {
		t.Fatalf("converted Init/Teardown counts = %d/%d, want 1/1", len(script.Init), len(script.Teardown))
	}

	eng, err := engine.New(engine.Config{
		Script:   script,
		VUsers:   1,
		Duration: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runResult, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("eng.Run: %v", err)
	}
	if runResult.CompletedIterations != 2 || runResult.TotalTransactions != 2 || runResult.TotalErrors != 0 {
		t.Fatalf("completed_iterations=%d total_transactions=%d total_errors=%d, want 2/2/0",
			runResult.CompletedIterations, runResult.TotalTransactions, runResult.TotalErrors)
	}

	// Verify dependency ordering and concurrent overlap across both iterations.
	depProblems := server.CheckDependencies(
		paritytest.DependencyRule{
			Prerequisite: "GET /lifecycle/init",
			Dependents:   []string{"GET /lifecycle/page"},
		},
		paritytest.DependencyRule{
			Prerequisite:   "GET /lifecycle/page",
			Dependents:     []string{"GET /lifecycle/asset/a.css", "GET /lifecycle/asset/b.js"},
			OverlapGroup:   []string{"GET /lifecycle/asset/a.css", "GET /lifecycle/asset/b.js"},
			MinMaxInFlight: 2,
		},
		paritytest.DependencyRule{
			Prerequisite: "GET /lifecycle/asset/a.css",
			Dependents:   []string{"POST /lifecycle/commit"},
		},
		paritytest.DependencyRule{
			Prerequisite: "GET /lifecycle/asset/b.js",
			Dependents:   []string{"POST /lifecycle/commit"},
		},
	)
	if len(depProblems) != 0 {
		t.Fatalf("Level 3 dependency/concurrency checks failed:\n%s", strings.Join(depProblems, "\n"))
	}

	recorded := server.Recorded()
	// Expected total requests: 1 init + 2 * (1 page + 2 assets + 1 commit) + 1 teardown = 10
	if len(recorded) != 10 {
		t.Fatalf("recorded %d requests, want 10", len(recorded))
	}
	if recorded[0].Path != "/lifecycle/init" {
		t.Fatalf("first request = %s %s, want GET /lifecycle/init", recorded[0].Method, recorded[0].Path)
	}
	if recorded[len(recorded)-1].Path != "/lifecycle/logout" || string(recorded[len(recorded)-1].Body) != "boot=BootTok99" {
		t.Fatalf("last request = %s %s (%q), want POST /lifecycle/logout (boot=BootTok99)",
			recorded[len(recorded)-1].Method, recorded[len(recorded)-1].Path, string(recorded[len(recorded)-1].Body))
	}
	// Verify that each iteration's commit consumed that iteration's extracted PageNonce
	// plus the Init phase's BootToken, and that both commits completed before logout arrived.
	var commits []paritytest.Recorded
	for _, rec := range recorded {
		if rec.Path == "/lifecycle/commit" {
			commits = append(commits, rec)
		}
	}
	if len(commits) != 2 {
		t.Fatalf("recorded %d commits, want 2", len(commits))
	}
	if string(commits[0].Body) != "boot=BootTok99&nonce=IterNonce1" {
		t.Fatalf("iteration 1 commit body = %q, want boot=BootTok99&nonce=IterNonce1", string(commits[0].Body))
	}
	if string(commits[1].Body) != "boot=BootTok99&nonce=IterNonce2" {
		t.Fatalf("iteration 2 commit body = %q, want boot=BootTok99&nonce=IterNonce2", string(commits[1].Body))
	}
	logout := recorded[len(recorded)-1]
	if commits[1].CompletionSeq >= logout.ArrivalSeq {
		t.Fatalf("second iteration commit (completed @%d) did not complete before teardown logout (arrived @%d)",
			commits[1].CompletionSeq, logout.ArrivalSeq)
	}
}

func strPtr(s string) *string { return &s }
