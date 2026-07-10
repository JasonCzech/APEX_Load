package engine

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var conditionExprPrograms sync.Map

type conditionExprProgramCache struct {
	program   cel.Program
	needsVars bool
	needsData bool
	needsEnv  bool
}

func conditionExprProgram(expr string) (*conditionExprProgramCache, error) {
	expr = dsl.NormalizeConditionExpression(expr)
	if cached, ok := conditionExprPrograms.Load(expr); ok {
		return cached.(*conditionExprProgramCache), nil
	}
	env, err := dsl.NewConditionCELEnv()
	if err != nil {
		return nil, err
	}
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	if ast.OutputType() == nil || !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, fmt.Errorf("condition expression must return bool")
	}
	program, err := env.Program(ast, cel.CostLimit(10_000))
	if err != nil {
		return nil, err
	}
	entry := &conditionExprProgramCache{
		program:   program,
		needsVars: conditionExprUsesNamespace(expr, "vars"),
		needsData: conditionExprUsesNamespace(expr, "data"),
		needsEnv:  conditionExprUsesNamespace(expr, "env"),
	}
	actual, _ := conditionExprPrograms.LoadOrStore(expr, entry)
	return actual.(*conditionExprProgramCache), nil
}

// conditionNamespacePatterns matches a namespace referenced as a whole
// identifier token, so a bare reference — `'promo' in vars`, `size(data) > 0`,
// `'REGION' in env`, or a whitespaced `vars ["key"]` — is detected, not just
// `vars.`/`vars[`. Missing a binding makes cel-go error at eval time ("no such
// attribute"), which evalConditionExpr swallows to false; over-detecting only
// binds an unused namespace (harmless), so err toward inclusion.
var conditionNamespacePatterns = map[string]*regexp.Regexp{
	"vars": regexp.MustCompile(`\bvars\b`),
	"data": regexp.MustCompile(`\bdata\b`),
	"env":  regexp.MustCompile(`\benv\b`),
}

func conditionExprUsesNamespace(expr string, namespace string) bool {
	pattern, ok := conditionNamespacePatterns[namespace]
	if !ok {
		return true // unknown namespace: bind it to be safe
	}
	return pattern.MatchString(expr)
}

func (vu *VUser) evalConditionExpr(expr string) bool {
	compiled, err := conditionExprProgram(expr)
	if err != nil {
		return false
	}
	out, _, err := compiled.program.Eval(vu.conditionExprActivation(compiled))
	if err != nil || out == nil {
		return false
	}
	value, ok := out.Value().(bool)
	return ok && value
}

func (vu *VUser) conditionExprActivation(compiled *conditionExprProgramCache) map[string]any {
	if compiled == nil {
		return map[string]any{
			"vars": vu.conditionExprVarMap(),
			"data": vu.conditionExprDataMap(),
			"env":  conditionExprEnvMap(),
		}
	}
	activation := make(map[string]any, 3)
	if compiled.needsVars {
		activation["vars"] = vu.conditionExprVarMap()
	}
	if compiled.needsData {
		activation["data"] = vu.conditionExprDataMap()
	}
	if compiled.needsEnv {
		activation["env"] = conditionExprEnvMap()
	}
	return activation
}

func (vu *VUser) conditionExprVarMap() map[string]any {
	out := map[string]any{}
	if vu == nil {
		return out
	}
	put := func(key, value string) {
		// Dotted keys (e.g. foreach bindings "row.email") are exposed as nested
		// maps so a CEL condition can traverse them as var.row.email, mirroring the
		// data namespace; plain keys stay flat (var.loop_index).
		if source, column, ok := strings.Cut(key, "."); ok && strings.TrimSpace(source) != "" && strings.TrimSpace(column) != "" {
			nested, _ := out[source].(map[string]any)
			if nested == nil {
				nested = map[string]any{}
				out[source] = nested
			}
			nested[column] = value
			return
		}
		// Plain key. If a dotted key already populated a nested map under this name
		// (e.g. a foreach binding "row.email" vs a plain var "row"), keep the nested
		// map — the dotted binding wins. This makes the result deterministic
		// regardless of Go's randomized map iteration order, so a CEL condition over
		// var.<name> evaluates the same across identical runs/replays.
		if _, isNested := out[key].(map[string]any); isNested {
			return
		}
		out[key] = value
	}
	for key, value := range vu.Variables {
		put(key, value)
	}
	if vu.Parameters != nil && vu.Script != nil {
		for _, param := range vu.Script.Parameters {
			name := strings.TrimSpace(param.Name)
			if name == "" {
				continue
			}
			if value, ok := vu.Parameters.ResolveNamed(name); ok {
				put(name, value)
			}
		}
	}
	return out
}

func (vu *VUser) conditionExprDataMap() map[string]any {
	out := map[string]any{}
	if vu == nil {
		return out
	}
	for key, value := range vu.Variables {
		source, column, ok := strings.Cut(key, ".")
		if !ok || strings.TrimSpace(source) == "" || strings.TrimSpace(column) == "" {
			// Plain key: a dotted binding under the same name wins (deterministic
			// regardless of map iteration order), mirroring conditionExprVarMap.
			if _, isNested := out[key].(map[string]any); isNested {
				continue
			}
			out[key] = value
			continue
		}
		nested, _ := out[source].(map[string]any)
		if nested == nil {
			nested = map[string]any{}
			out[source] = nested
		}
		nested[column] = value
	}
	return out
}

func conditionExprEnvMap() map[string]any {
	// Only expose env values the script is permitted to read. When an operator
	// sets APEXLOAD_SCRIPT_ENV_ALLOWLIST this restricts the CEL `env` binding to
	// the listed keys; the engine secret namespace is always excluded so a
	// condition expression cannot enumerate or read resolved credentials.
	return scriptVisibleEnvMap()
}
