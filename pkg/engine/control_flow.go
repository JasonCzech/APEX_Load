package engine

import (
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// actionFlow signals whether the per-action executor should continue to the next
// action or abort the current iteration and proceed to teardown.
type actionFlow int

const (
	flowNext actionFlow = iota
	flowTeardown
)

// defaultControlLoopMaxIterations bounds condition-driven control_loop blocks so a
// non-terminating predicate can never hang a VUser goroutine.
const defaultControlLoopMaxIterations = 1000

// maxControlDepth bounds control-flow nesting to guard against pathological or
// cyclic imported scripts.
const maxControlDepth = 32

// evalCondition deterministically evaluates a DSL condition against the VUser's
// current variable/parameter state. A nil condition evaluates to true so that a
// control_loop with only a Count, or a control_if used as an unconditional block,
// behaves intuitively.
func (vu *VUser) evalCondition(cond *dsl.Condition) bool {
	if cond == nil {
		return true
	}
	left := vu.interpolate(cond.Left)
	right := vu.interpolate(cond.Right)
	switch strings.ToLower(strings.TrimSpace(cond.Op)) {
	case "", "eq", "==", "equals":
		return left == right
	case "ne", "!=", "not_equals":
		return left != right
	case "contains":
		return strings.Contains(left, right)
	case "not_contains":
		return !strings.Contains(left, right)
	case "exists", "truthy":
		return conditionTruthy(left)
	case "not_exists", "falsy", "empty":
		return !conditionTruthy(left)
	case "gt", "lt", "gte", "lte", ">", "<", ">=", "<=":
		return compareNumeric(left, right, cond.Op)
	default:
		return false
	}
}

// conditionTruthy reports whether a template-expanded string should be treated as
// true for exists/truthy checks. Empty and common false-y tokens are false.
func conditionTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off", "null", "nil":
		return false
	default:
		return true
	}
}

// compareNumeric compares two numeric operands; non-numeric operands compare false.
func compareNumeric(left, right, op string) bool {
	leftNum, leftErr := strconv.ParseFloat(strings.TrimSpace(left), 64)
	rightNum, rightErr := strconv.ParseFloat(strings.TrimSpace(right), 64)
	if leftErr != nil || rightErr != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "gt", ">":
		return leftNum > rightNum
	case "lt", "<":
		return leftNum < rightNum
	case "gte", ">=":
		return leftNum >= rightNum
	case "lte", "<=":
		return leftNum <= rightNum
	default:
		return false
	}
}

// controlLoopMaxPasses returns the effective pass cap for a control_loop block.
func controlLoopMaxPasses(control *dsl.ControlFlow) int {
	if control != nil && control.MaxIterations > 0 {
		return control.MaxIterations
	}
	return defaultControlLoopMaxIterations
}
