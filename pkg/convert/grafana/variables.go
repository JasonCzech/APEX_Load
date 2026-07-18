package grafana

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

// variablePolicy is the per-variable decision the converter records so target
// expression checks can look up how a referenced variable is handled.
type variablePolicy struct {
	kind   string
	reason string
}

// classifyVariables applies the template-variable policy to a dashboard's
// templating.list, returning the reviewable bindings and a name→policy lookup. It does
// NOT emit blockers for a rejected variable's declaration — a rejected variable only
// blocks the import when a target actually references it (see checkVariableReference),
// so an unused legacy variable never blocks an otherwise-importable dashboard.
func classifyVariables(list []rawVariable, col *diag.Collector) ([]VariableBinding, map[string]variablePolicy) {
	bindings := []VariableBinding{}
	policy := map[string]variablePolicy{}
	for _, v := range list {
		name := strings.TrimSpace(v.Name)
		if name == "" {
			continue
		}
		if len(name) > 128 {
			col.Add(diag.Blocker, "variable", "templating", "variable name exceeds the limit of 128 bytes")
			continue
		}
		varType := strings.ToLower(strings.TrimSpace(v.Type))
		if len(varType) > MaxTypeBytes || len(strings.TrimSpace(v.Label)) > MaxTitleBytes {
			col.Add(diag.Blocker, "variable", name, "variable type or label exceeds its size limit")
			continue
		}
		// Reserved namespace: Grafana's built-in macros all start with "__"; a
		// user variable must not shadow them.
		if strings.HasPrefix(name, "__") {
			col.Add(diag.Warning, "variable", name, "variable name starts with \"__\", which is reserved for Grafana built-ins; it was ignored")
			continue
		}
		if IsReservedBindingName(name) {
			binding := VariableBinding{
				Name: name, Kind: BindingAuto, GrafanaType: strings.ToLower(strings.TrimSpace(v.Type)),
				Label: strings.TrimSpace(v.Label), Reason: "reserved run identifier is supplied by APEX at query time",
			}
			policy[name] = variablePolicy{kind: BindingAuto}
			bindings = append(bindings, binding)
			continue
		}
		label := strings.TrimSpace(v.Label)
		defaultValue, defaultOversized := boundedVariableValue(v.Current.Value, name, "current value", col)

		binding := VariableBinding{Name: name, GrafanaType: varType, Label: label, Default: defaultValue}

		if v.Multi || v.IncludeAll {
			binding.Kind = BindingRejected
			binding.Reason = "multi-value/include-all variables expand to a regex alternation and rewrite the matcher operator (= to =~), which cannot be reproduced without a PromQL parser"
			policy[name] = variablePolicy{kind: BindingRejected, reason: binding.Reason}
			bindings = append(bindings, binding)
			continue
		}

		switch varType {
		case "constant":
			binding.Kind = BindingAuto
			resolved := defaultValue
			oversized := defaultOversized
			if resolved == "" && !oversized {
				resolved, oversized = boundedVariableValue(v.Query, name, "query value", col)
			}
			if oversized {
				binding.Kind = BindingRejected
				binding.Reason = "constant value exceeds the import size limit"
				policy[name] = variablePolicy{kind: BindingRejected, reason: binding.Reason}
				break
			}
			binding.ResolvedTo = resolved
			binding.Reason = "constant variable resolved to its stored value"
			policy[name] = variablePolicy{kind: BindingAuto}
		case "interval":
			if defaultOversized {
				binding.Kind = BindingRejected
				binding.Reason = "interval value exceeds the import size limit"
				policy[name] = variablePolicy{kind: BindingRejected, reason: binding.Reason}
				break
			}
			binding.Kind = BindingAuto
			// An interval variable resolves to the interval the dashboard had
			// selected (its current value). This is stored and folded into the
			// query bindings at commit so a query like rate(x[$win]) resolves.
			binding.ResolvedTo = defaultValue
			binding.Reason = "interval variable resolved to its selected value"
			policy[name] = variablePolicy{kind: BindingAuto}
		case "datasource":
			binding.Kind = BindingAuto
			binding.ResolvedTo = "APEX Prometheus"
			binding.Reason = "datasource variable is bound to the APEX-configured Prometheus"
			col.Add(diag.Warning, "variable", name, "datasource variable is bound to the APEX-configured Prometheus")
			policy[name] = variablePolicy{kind: BindingAuto}
		case "custom", "query", "textbox":
			binding.Kind = BindingRequired
			binding.Options = variableOptions(v, name, col)
			if varType == "query" {
				binding.Query = variableQueryString(v.Query)
				binding.DatasourceUID = datasourceUIDForStorage(parseDatasource(v.Datasource))
				if len(binding.Query) > MaxExprBytes {
					binding.Query = ""
					col.Add(diag.Warning, "variable", name, "label_values query exceeds the expression size limit and cannot be resolved")
				}
			}
			binding.Reason = requiredBindingReason(varType)
			policy[name] = variablePolicy{kind: BindingRequired}
		case "adhoc":
			binding.Kind = BindingRejected
			binding.Reason = "ad-hoc filters mutate the query selector at render time and are not representable in a stored panel"
			policy[name] = variablePolicy{kind: BindingRejected, reason: binding.Reason}
		case "system":
			binding.Kind = BindingRejected
			binding.Reason = "system variables are Grafana-runtime only"
			policy[name] = variablePolicy{kind: BindingRejected, reason: binding.Reason}
		default:
			binding.Kind = BindingRejected
			binding.Reason = fmt.Sprintf("variable type %q is not supported", varType)
			policy[name] = variablePolicy{kind: BindingRejected, reason: binding.Reason}
		}
		bindings = append(bindings, binding)
	}
	return bindings, policy
}

func variableQueryString(raw json.RawMessage) string {
	if value := strings.TrimSpace(decodeStringValue(raw)); value != "" {
		return value
	}
	var object struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	return strings.TrimSpace(object.Query)
}

func requiredBindingReason(varType string) string {
	if varType == "query" {
		return "query variable requires an explicit binding; an administrator may opt in to bounded, run-scoped label_values() option resolution"
	}
	return "user-input variable requires an explicit value before the dashboard can be committed"
}

func variableOptions(v rawVariable, variableName string, col *diag.Collector) []string {
	seen := map[string]struct{}{}
	out := []string{}
	options := v.Options
	if len(options) > MaxOptionsPerVar {
		col.Add(diag.Warning, "variable", variableName, fmt.Sprintf("variable declares %d options; only the first %d are shown", len(options), MaxOptionsPerVar))
		options = options[:MaxOptionsPerVar]
	}
	for _, opt := range options {
		if len(opt.Value) > MaxVariableValueBytes*4 {
			col.Add(diag.Warning, "variable", variableName, "oversized variable option omitted from the review")
			continue
		}
		for _, value := range decodeStringValues(opt.Value) {
			value = strings.TrimSpace(value)
			if value == "" || value == "$__all" {
				continue
			}
			if len(value) > MaxVariableValueBytes {
				col.Add(diag.Warning, "variable", variableName, "oversized variable option omitted from the review")
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
			if len(out) >= MaxOptionsPerVar {
				return out
			}
		}
	}
	return out
}

func boundedVariableValue(raw json.RawMessage, variableName, field string, col *diag.Collector) (string, bool) {
	// A scalar JSON string adds only small quoting overhead. Refuse a large raw value
	// before unmarshalling so one array-valued current selection cannot amplify into
	// an unbounded temporary []string.
	if len(raw) > MaxVariableValueBytes*4 {
		col.Add(diag.Warning, "variable", variableName, "oversized "+field+" omitted from the review")
		return "", true
	}
	value := decodeStringValue(raw)
	if len(value) > MaxVariableValueBytes {
		col.Add(diag.Warning, "variable", variableName, "oversized "+field+" omitted from the review")
		return "", true
	}
	return value, false
}

// Binding kinds describe how a Grafana template variable is handled by APEX.
const (
	// BindingAuto is resolved by APEX with no user input: built-in time macros
	// ($__interval/$__range) at query time, or a stored constant at import time.
	BindingAuto = "auto"
	// BindingRequired must be supplied by the user at commit; there is no safe
	// default, so an unbound required variable blocks the commit.
	BindingRequired = "required"
	// BindingRejected cannot be represented and turns into a diag.Blocker.
	BindingRejected = "rejected"
)

// Reserved user-variable names auto-bind to the live run's identifiers at query time.
// This is what lets a generic dashboard's "{{test_id}}" legend become run-scoped.
const (
	reservedTestID    = "test_id"
	reservedProjectID = "project_id"
)

// BindingValuePattern constrains user-supplied binding values that are spliced into
// PromQL. It excludes quotes, braces, parentheses, brackets, pipes, commas,
// backslashes, whitespace and control characters — every character a value would need
// to escape a label matcher or inject a second selector. Validated at commit AND
// again at query time (defense in depth).
var BindingValuePattern = regexp.MustCompile(`^[A-Za-z0-9_.:/=+-]{1,128}$`)
var BindingNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidateBindingValue reports whether v is a safe PromQL binding value.
func ValidateBindingValue(v string) bool {
	return BindingValuePattern.MatchString(v)
}

// ValidateBindingName reports whether name is a syntactically valid user-variable
// name. Authorization to set it is checked separately against RequiredBindingNames.
func ValidateBindingName(name string) bool {
	return BindingNamePattern.MatchString(strings.TrimSpace(name))
}

// IsReservedBindingName reports whether a client binding would shadow an APEX-owned
// runtime identifier or Grafana built-in macro.
func IsReservedBindingName(name string) bool {
	name = strings.TrimSpace(name)
	return name == reservedTestID || name == reservedProjectID || strings.HasPrefix(name, "__")
}

// builtinTimeMacros are the Grafana global time macros APEX resolves from the query
// window. Values are computed per-request in QueryResolution.values.
var builtinTimeMacros = map[string]struct{}{
	"__interval": {}, "__interval_ms": {}, "__rate_interval": {},
	"__range": {}, "__range_s": {}, "__range_ms": {},
}

// rejectedGlobals are Grafana runtime-only macros with no APEX equivalent, or SQL/Flux
// macros that prove the target is not really a Prometheus query.
var rejectedGlobals = map[string]string{
	"__dashboard":       "Grafana dashboard-runtime macro with no APEX equivalent.",
	"__org":             "Grafana org-runtime macro with no APEX equivalent.",
	"__user":            "Grafana user-runtime macro with no APEX equivalent.",
	"__name":            "Grafana dashboard-runtime macro with no APEX equivalent.",
	"__from":            "Grafana uses $__from/$__to as epoch bounds; APEX resolves the window from the run instead.",
	"__to":              "Grafana uses $__from/$__to as epoch bounds; APEX resolves the window from the run instead.",
	"timeFilter":        "SQL/Flux time macro; the target is not a Prometheus query.",
	"__timeFilter":      "SQL/Flux time macro; the target is not a Prometheus query.",
	"__timeGroup":       "SQL/Flux time macro; the target is not a Prometheus query.",
	"__unixEpochFilter": "SQL/Flux time macro; the target is not a Prometheus query.",
	"__timeGroupAlias":  "SQL/Flux time macro; the target is not a Prometheus query.",
}

// exprVarRef is one template-variable reference found inside a PromQL expression.
type exprVarRef struct {
	Name     string
	Modifier string // the ${var:modifier} format suffix, empty when absent
}

var (
	exprBraceToken   = regexp.MustCompile(`\$\{([^}]*)\}`)
	exprBracketToken = regexp.MustCompile(`\[\[([^\]]*)\]\]`)
	exprDollarToken  = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
	// leftoverToken detects any unresolved template syntax after substitution.
	leftoverToken = regexp.MustCompile(`\$\{[^}]*\}|\[\[[^\]]*\]\]|\$[A-Za-z_][A-Za-z0-9_]*`)
)

// scanExprVariables returns every template-variable reference in an expression, in
// first-seen order, including the format modifier when one is present.
func scanExprVariables(expr string) []exprVarRef {
	seen := map[string]struct{}{}
	refs := []exprVarRef{}
	add := func(inner string) {
		inner = strings.TrimSpace(inner)
		if inner == "" {
			return
		}
		name := inner
		modifier := ""
		if idx := strings.IndexByte(inner, ':'); idx >= 0 {
			name = strings.TrimSpace(inner[:idx])
			modifier = strings.TrimSpace(inner[idx+1:])
		}
		if name == "" {
			return
		}
		key := name + "\x00" + modifier
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, exprVarRef{Name: name, Modifier: modifier})
	}
	for _, m := range exprBraceToken.FindAllStringSubmatch(expr, -1) {
		add(m[1])
	}
	for _, m := range exprBracketToken.FindAllStringSubmatch(expr, -1) {
		add(m[1])
	}
	for _, m := range exprDollarToken.FindAllStringSubmatch(expr, -1) {
		add(m[1])
	}
	return refs
}

// QueryResolution carries everything needed to turn a stored panel expression into a
// concrete PromQL query at request time.
type QueryResolution struct {
	StepSeconds  int
	RangeSeconds int
	TestID       string
	ProjectID    string
	// Bindings holds committed values for user variables plus import-resolved
	// constants, keyed by variable name. Values are re-validated here.
	Bindings map[string]string
}

// values assembles the full substitution table: built-in time macros, reserved run
// identifiers, then user/constant bindings. User binding values are validated; a bad
// value is a hard error rather than a silent splice.
func (q QueryResolution) values() (map[string]string, error) {
	step := q.StepSeconds
	if step < 1 {
		step = 1
	}
	rng := q.RangeSeconds
	if rng < 1 {
		rng = 1
	}
	rateInterval := 4 * step
	if rateInterval < 60 {
		rateInterval = 60
	}
	out := map[string]string{
		"__interval":      strconv.Itoa(step) + "s",
		"__interval_ms":   strconv.Itoa(step * 1000),
		"__rate_interval": strconv.Itoa(rateInterval) + "s",
		"__range":         strconv.Itoa(rng) + "s",
		"__range_s":       strconv.Itoa(rng),
		"__range_ms":      strconv.Itoa(rng * 1000),
	}
	// The reserved run identifiers are only spliced into PromQL when they are safe
	// values. test_id is request-controlled (a query parameter), so an unsafe one
	// must never reach a label matcher; project_id is server-supplied but validated
	// the same way for defense in depth. An unsafe value is simply not bound — a
	// panel that references it then fails the leftover-token check below (fail-soft,
	// per-panel) rather than injecting, and unrelated panels are unaffected.
	if testID := strings.TrimSpace(q.TestID); testID != "" && ValidateBindingValue(testID) {
		out[reservedTestID] = testID
	}
	if projectID := strings.TrimSpace(q.ProjectID); projectID != "" && ValidateBindingValue(projectID) {
		out[reservedProjectID] = projectID
	}
	for name, value := range q.Bindings {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !ValidateBindingName(name) {
			return nil, fmt.Errorf("binding name %q is not valid", name)
		}
		if IsReservedBindingName(name) {
			return nil, fmt.Errorf("binding name %q is reserved by APEX", name)
		}
		if !ValidateBindingValue(value) {
			return nil, fmt.Errorf("binding value for %q is not a valid PromQL value", name)
		}
		out[name] = value
	}
	return out, nil
}

// ResolvePanelExpr substitutes every template variable in a stored panel expression
// and returns concrete PromQL. It errors if any user binding value is unsafe, if a
// format modifier survived to query time, or if any template token remains unresolved
// (an unbound or rejected variable) — a partially-resolved expression must never be
// sent to Prometheus.
func ResolvePanelExpr(expr string, q QueryResolution) (string, error) {
	values, err := q.values()
	if err != nil {
		return "", err
	}
	// Grafana creates one $__auto_interval_<variable> macro for interval variables
	// whose selected value is "auto". They have the same runtime meaning as the
	// computed $__interval; discover only macros actually present in this expression
	// so the substitution table remains bounded by the already-bounded query length.
	for _, ref := range scanExprVariables(expr) {
		if strings.HasPrefix(ref.Name, "__auto_interval_") {
			values[ref.Name] = values["__interval"]
		}
	}
	// Build replacer pairs for all three reference forms of each known variable.
	// Longer keys are matched first by strings.Replacer's longest-match rule, so
	// $__rate_interval is not shadowed by a shorter $__range.
	pairs := make([]string, 0, len(values)*6)
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		v := values[name]
		pairs = append(pairs,
			"${"+name+"}", v,
			"[["+name+"]]", v,
			"$"+name, v,
		)
	}
	resolved := strings.NewReplacer(pairs...).Replace(expr)
	if leftover := leftoverToken.FindAllString(resolved, -1); len(leftover) > 0 {
		return "", fmt.Errorf("unresolved template variable(s) in query: %s", strings.Join(dedupeStrings(leftover), ", "))
	}
	return resolved, nil
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
