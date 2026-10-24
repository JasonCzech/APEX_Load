package requestimport

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// VariableReadiness is the source-neutral result of analyzing variable
// references in execution order. ByAction uses stable DSL paths and Missing is
// the sorted union used by API preflight and import review.
//
// Blocking is the subset of Missing that replay cannot carry as placeholder
// text. Like LoadRunner, which uses an undefined {param} literally and warns,
// HTTP replay keeps an unresolved {{var.*}} reference verbatim, and the
// request that sends it warns (unresolved_variable_sent). SOAP and Java HTTP
// requests run through the same HTTP adapter and keep placeholders too, except
// in a Java HTTP request body, which is decoded as base64 before it is sent.
// Protocol configuration is never interpolated, a JSON helper fails its step
// on an unresolved buffer, an undeclared {{param.*}} always blocks, and other
// protocols have no placeholder fallback.
//
// LateProduced lists the missing names that a step of the script does write,
// only not on every path before their use. ParamBlocking and ConfigBlocking
// are the blocking names that an undeclared {{param.*}} use or a protocol
// configuration reference blocks, which no step can supply, so a client words
// their fix apart from producer order.
type VariableReadiness struct {
	Missing        []string            `json:"missing,omitempty"`
	ByAction       map[string][]string `json:"by_action,omitempty"`
	Blocking       []string            `json:"blocking,omitempty"`
	LateProduced   []string            `json:"late_produced,omitempty"`
	ParamBlocking  []string            `json:"param_blocking,omitempty"`
	ConfigBlocking []string            `json:"config_blocking,omitempty"`
}

// AnalyzeVariableReadiness finds references that are not supplied by explicit
// replay seeds, script parameters, or an output that executes earlier on
// the same path. The walk and the producer model live in pkg/dsl/varflow so
// every readiness consumer applies the same runtime semantics.
func AnalyzeVariableReadiness(script *dsl.Script, seeded map[string]string) VariableReadiness {
	result := varflow.Analyze(script, seeded)
	return VariableReadiness{
		Missing:        result.Missing,
		ByAction:       result.ByAction,
		Blocking:       result.Blocking,
		LateProduced:   result.LateProduced,
		ParamBlocking:  result.ParamBlocking,
		ConfigBlocking: result.ConfigBlocking,
	}
}

// MissingVariables is the compatibility projection used by replay preflight.
func MissingVariables(script *dsl.Script, seeded map[string]string) []string {
	return AnalyzeVariableReadiness(script, seeded).Missing
}

// actionVariableReferences returns the {{var.*}} names the runtime reads for
// one action, using the same read model as readiness: a source step's
// snapshots and call_args are evidence, and log-only steps read nothing.
func actionVariableReferences(action dsl.Action) []string {
	var names []string
	for _, ref := range varflow.ActionReferences(action) {
		if ref.Namespace == varflow.NamespaceVar {
			names = append(names, ref.Name)
		}
	}
	return names
}

func collectVariableReferences(value string, set map[string]struct{}) {
	for _, ref := range varflow.References(value) {
		if ref.Namespace == varflow.NamespaceVar {
			set[ref.Name] = struct{}{}
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

func absoluteHTTPActionTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
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
