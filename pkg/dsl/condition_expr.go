package dsl

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
)

// NewConditionCELEnv builds the CEL environment used for control-flow
// condition expressions. Names intentionally mirror template namespaces in
// source expressions: var.NAME, data.source.column, and env.KEY. CEL reserves
// "var", so expressions are normalized to the internal "vars" identifier
// before compile/eval.
func NewConditionCELEnv() (*cel.Env, error) {
	scopeType := cel.MapType(cel.StringType, cel.DynType)
	return cel.NewEnv(
		cel.Variable("vars", scopeType),
		cel.Variable("data", scopeType),
		cel.Variable("env", scopeType),
		cel.ParserExpressionSizeLimit(4096),
		cel.ParserRecursionLimit(32),
	)
}

func ValidateConditionExpression(expr string) error {
	if strings.TrimSpace(expr) == "" {
		return nil
	}
	expr = NormalizeConditionExpression(expr)
	env, err := NewConditionCELEnv()
	if err != nil {
		return err
	}
	ast, issues := env.Compile(expr)
	if issues != nil && issues.Err() != nil {
		return fmt.Errorf("%w", issues.Err())
	}
	if ast.OutputType() == nil || !ast.OutputType().IsExactType(cel.BoolType) {
		got := "unknown"
		if ast.OutputType() != nil {
			got = cel.FormatCELType(ast.OutputType())
		}
		return fmt.Errorf("expression must return bool, got %s", got)
	}
	return nil
}

// NormalizeConditionExpression preserves the public DSL namespace "var" while
// avoiding CEL's reserved identifier. It rewrites var.foo and var["foo"] to the
// internal vars namespace, skipping quoted string literals.
func NormalizeConditionExpression(expr string) string {
	var b strings.Builder
	b.Grow(len(expr))
	var quote byte
	escaped := false
	for i := 0; i < len(expr); {
		ch := expr[i]
		if quote != 0 {
			b.WriteByte(ch)
			i++
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			b.WriteByte(ch)
			i++
			continue
		}
		if strings.HasPrefix(expr[i:], "var") && conditionExprVarBoundary(expr, i) {
			b.WriteString("vars")
			i += len("var")
			continue
		}
		b.WriteByte(ch)
		i++
	}
	return b.String()
}

func conditionExprVarBoundary(expr string, index int) bool {
	if index > 0 && isConditionExprIdentByte(expr[index-1]) {
		return false
	}
	next := index + len("var")
	if next >= len(expr) {
		return false
	}
	return expr[next] == '.' || expr[next] == '['
}

func isConditionExprIdentByte(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}
