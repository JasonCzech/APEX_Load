package engine

import (
	"fmt"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var conditionExprPrograms sync.Map

func conditionExprProgram(expr string) (cel.Program, error) {
	expr = dsl.NormalizeConditionExpression(expr)
	if cached, ok := conditionExprPrograms.Load(expr); ok {
		return cached.(cel.Program), nil
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
	actual, _ := conditionExprPrograms.LoadOrStore(expr, program)
	return actual.(cel.Program), nil
}

func (vu *VUser) evalConditionExpr(expr string) bool {
	program, err := conditionExprProgram(expr)
	if err != nil {
		return false
	}
	out, _, err := program.Eval(vu.conditionExprActivation())
	if err != nil || out == nil {
		return false
	}
	value, ok := out.Value().(bool)
	return ok && value
}

func (vu *VUser) conditionExprActivation() map[string]any {
	return map[string]any{
		"vars": vu.conditionExprVarMap(),
		"data": vu.conditionExprDataMap(),
		"env":  conditionExprEnvMap(),
	}
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
