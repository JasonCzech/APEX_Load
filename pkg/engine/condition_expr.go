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
	for key, value := range vu.Variables {
		out[key] = value
	}
	if vu.Parameters != nil && vu.Script != nil {
		for _, param := range vu.Script.Parameters {
			name := strings.TrimSpace(param.Name)
			if name == "" {
				continue
			}
			if value, ok := vu.Parameters.ResolveNamed(name); ok {
				out[name] = value
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
