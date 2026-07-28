package engine

import (
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// actionFlow signals how the per-action executor should proceed after running
// one action: continue to the next action, abort the iteration to teardown, or
// (inside a loop body) break out of / continue the enclosing control_loop.
type actionFlow int

const (
	flowNext actionFlow = iota
	flowTeardown
	// flowBreak/flowContinue are emitted by control_break/control_continue
	// actions. They bubble up through control_if bodies until the nearest
	// enclosing control_loop consumes them; escaping to the phase level is a
	// no-op (logged as a warning).
	flowBreak
	flowContinue
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
	if expr := strings.TrimSpace(cond.Expr); expr != "" {
		return vu.evalConditionExpr(expr)
	}
	return evalConditionOp(vu.interpolate(cond.Left), cond.Op, vu.interpolate(cond.Right))
}

// evalConditionOp applies a comparison operator to two already-interpolated
// operands. An empty operator means equality so that a bare {Left,Right} pair
// behaves intuitively.
func evalConditionOp(left, op, right string) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
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
		return compareNumeric(left, right, op)
	default:
		return false
	}
}

// ConditionEvidence captures the evaluated operands and outcome of a single
// condition evaluation so the debugger can show *why* a branch was taken or a
// loop pass ran. It carries no secret-bearing values beyond what the operands
// already expand to (the same redaction applies as for action targets).
type ConditionEvidence struct {
	Expr   string `json:"expr,omitempty"`   // CEL expression evaluated (when set)
	Left   string `json:"left,omitempty"`   // interpolated left operand
	Op     string `json:"op,omitempty"`     // normalized operator
	Right  string `json:"right,omitempty"`  // interpolated right operand
	Result bool   `json:"result"`           // evaluation outcome
	Branch string `json:"branch,omitempty"` // "then"/"else" for control_if; "" otherwise
}

// evalConditionEvidence mirrors evalCondition but additionally returns the
// evaluated operands so the script debugger can surface them. A nil condition
// evaluates to true with empty operand evidence.
func (vu *VUser) evalConditionEvidence(cond *dsl.Condition) (bool, ConditionEvidence) {
	if cond == nil {
		return true, ConditionEvidence{Result: true}
	}
	if expr := strings.TrimSpace(cond.Expr); expr != "" {
		result := vu.evalConditionExpr(expr)
		return result, ConditionEvidence{
			Expr:   scrubSensitiveTextWithConfig(expr, vu.RedactionConfig),
			Result: result,
		}
	}
	left := vu.interpolate(cond.Left)
	right := vu.interpolate(cond.Right)
	op := strings.ToLower(strings.TrimSpace(cond.Op))
	if op == "" {
		op = "eq"
	}
	result := evalConditionOp(left, op, right)
	return result, ConditionEvidence{
		Left:   vu.conditionEvidenceOperand(cond.Left, left),
		Op:     op,
		Right:  vu.conditionEvidenceOperand(cond.Right, right),
		Result: result,
	}
}

// conditionEvidenceOperand keeps debugger predicates useful without returning
// resolved credential material. Direct project-secret references (and
// sensitive variable/data/parameter references such as {{var.access_token}})
// remain as their author-facing template text. Other operands retain their
// evaluated value, with the run's exact-value redaction applied as a final
// boundary in case secret material arrived through an indirect variable.
func (vu *VUser) conditionEvidenceOperand(raw, resolved string) string {
	if vu.runtimeCredentialRedactionError() != nil {
		return redactedPayloadValue
	}
	if conditionOperandContainsSensitiveReference(raw) ||
		vu.conditionOperandContainsImportedCredentialReference(raw) {
		return scrubSensitiveTextWithConfig(raw, vu.RedactionConfig)
	}
	return scrubSensitiveTextWithConfig(resolved, vu.RedactionConfig)
}

func (vu *VUser) conditionOperandContainsImportedCredentialReference(value string) bool {
	for _, match := range scriptTemplateReferencePattern.FindAllStringSubmatch(value, -1) {
		if len(match) >= 2 && vu.importedCredentialTemplateExpression(match[1]) {
			return true
		}
	}
	return false
}

func conditionOperandContainsSensitiveReference(value string) bool {
	for _, match := range scriptTemplateReferencePattern.FindAllStringSubmatch(value, -1) {
		if len(match) < 2 {
			continue
		}
		expr := strings.TrimSpace(match[1])
		namespace, name, ok := strings.Cut(expr, ".")
		if !ok {
			continue
		}
		switch namespace {
		case "secret":
			return true
		case "var", "param", "data", "env":
			if scrub.SensitiveName(name) {
				return true
			}
		}
	}
	return false
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
		return min(control.MaxIterations, dsl.MaxControlLoopIterations)
	}
	return defaultControlLoopMaxIterations
}

// foreachRows resolves the rows a foreach control_loop iterates over. The rows
// are read-only column→value maps (raw column names) that never advance the
// per-VUser data cursor, so foreach is independent of the per-iteration
// {{data.src.col}} binding. A nil DataProvider or unknown source yields nil.
func (vu *VUser) foreachRows(fe *dsl.ForEachLoop) dataRowView {
	if fe == nil || vu == nil || vu.DataProvider == nil {
		return dataRowView{}
	}
	return vu.DataProvider.rowView(strings.TrimSpace(fe.Source))
}

// bindForEachRow binds one foreach row into the var namespace as
// {{var.<varName>.<col>}} for every column in the row.
func (vu *VUser) bindForEachRow(varName string, row map[string]string) {
	varName = strings.TrimSpace(varName)
	if varName == "" {
		return
	}
	for col, val := range row {
		vu.Variables[varName+"."+col] = val
	}
}

// captureForEachRestore snapshots the current values of every var-namespace key
// a foreach loop will overwrite (varName.<col> across all rows) and returns a
// closure that restores them, mirroring the loop_index save/restore so nested
// foreach blocks compose without clobbering an outer binding of the same name.
func (vu *VUser) captureForEachRestore(varName string, rows dataRowView) func() {
	varName = strings.TrimSpace(varName)
	if varName == "" {
		return func() {}
	}
	type savedVar struct {
		value string
		had   bool
	}
	prior := map[string]savedVar{}
	for _, col := range rows.Columns() {
		key := varName + "." + col
		v, ok := vu.Variables[key]
		prior[key] = savedVar{value: v, had: ok}
	}
	return func() {
		for key, s := range prior {
			if s.had {
				vu.Variables[key] = s.value
			} else {
				delete(vu.Variables, key)
			}
		}
	}
}
