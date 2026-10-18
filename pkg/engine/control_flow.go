package engine

import (
	"log/slog"
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

// controlRunner is what the iteration loop (vuserRun) and the lifecycle
// runner (lifecycleRun) share to run control blocks. Each block type runs in
// its own small function, so every level of nesting adds little to the
// stack of a VUser parked in a nested think time.
type controlRunner interface {
	// controlBlockRuns reports whether a control block can run at depth.
	controlBlockRuns(action *dsl.Action, depth int) bool
	runActions(actions []dsl.Action, depth int) actionFlow
	// stopped reports that the runner's action boundary or action context
	// has ended.
	stopped() bool
	vuser() *VUser
	log() *slog.Logger
}

// The block functions return the flow and whether the block's own think
// time follows.

func runIfBlock(r controlRunner, action *dsl.Action, depth int) (actionFlow, bool) {
	if !r.controlBlockRuns(action, depth) {
		return flowNext, false
	}
	body := action.Control.Else
	if r.vuser().evalCondition(action.Control.Condition) {
		body = action.Control.Steps
	}
	return runBlockBody(r, body, depth)
}

func runSwitchBlock(r controlRunner, action *dsl.Action, depth int) (actionFlow, bool) {
	if !r.controlBlockRuns(action, depth) {
		return flowNext, false
	}
	body, _ := r.vuser().selectSwitchBranch(action.Control)
	return runBlockBody(r, body, depth)
}

// runBlockBody runs the body a control_if or control_switch selected. It
// propagates a break/continue from its body up to the enclosing loop, and a
// teardown aborts the phase; otherwise the block's think time follows.
func runBlockBody(r controlRunner, body []dsl.Action, depth int) (actionFlow, bool) {
	switch flow := r.runActions(body, depth+1); flow {
	case flowTeardown, flowBreak, flowContinue:
		return flow, false
	}
	return flowNext, true
}

func runLoopBlock(r controlRunner, action *dsl.Action, depth int) (actionFlow, bool) {
	if !r.controlBlockRuns(action, depth) {
		return flowNext, false
	}
	loop := action.Control
	cursor := newControlLoopCursor(r.vuser(), loop)
	if count := cursor.implicitTruncation(); count > 0 {
		warnLoopTruncation(r.log(), action.Name, count, cursor.maxPasses)
	}
passes:
	for cursor.pending() {
		if r.stopped() {
			cursor.restoreBindings()
			return flowTeardown, false
		}
		pass, ok := cursor.next()
		if !ok {
			if cursor.conditionCapReached {
				warnConditionLoopSafetyCap(r.log(), action.Name, cursor.maxPasses)
			}
			break
		}
		if pass.checkCondition && !r.vuser().evalCondition(pass.condition) {
			break
		}
		switch r.runActions(loop.Steps, depth+1) {
		case flowTeardown:
			cursor.restoreBindings()
			return flowTeardown, false
		case flowBreak:
			break passes
		}
		// flowContinue / flowNext: advance to the next pass.
	}
	cursor.restoreBindings()
	return flowNext, true
}

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
	Branch string `json:"branch,omitempty"` // then/else, case[n]/default, or empty for loops
}

// selectSwitchBranch evaluates ordered cases and returns the first matching
// body. A nil condition never matches: validation rejects it, and the runtime
// stays fail-closed if an older or externally constructed script bypasses
// admission.
func (vu *VUser) selectSwitchBranch(control *dsl.ControlFlow) ([]dsl.Action, int) {
	if control == nil {
		return nil, -1
	}
	for index, switchCase := range control.Cases {
		if switchCase.Condition != nil && vu.evalCondition(switchCase.Condition) {
			return switchCase.Steps, index
		}
	}
	return control.Default, -1
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
		publicExpr := scrubSensitiveTextWithConfig(expr, vu.resultPublicationRedactionConfig())
		if vu.resultPublicationUnavailable() {
			publicExpr = ""
		}
		return result, ConditionEvidence{
			Expr:   publicExpr,
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
// platform-managed credential material. Direct project-secret references (and
// sensitive variable/data/parameter references such as {{var.access_token}})
// remain as their author-facing template text. Other operands retain their
// evaluated value, with the run's exact-value redaction applied as a final
// boundary in case secret material arrived through an indirect variable.
//
// An expression is no longer suppressed merely because an import placed it in a
// credential-governed field. That is script-sourced provenance, and blanking the
// evaluated operand made the debugger unable to explain why a predicate failed
// for a value the operator can read in the script anyway.
func (vu *VUser) conditionEvidenceOperand(raw, resolved string) string {
	if vu.runtimeCredentialRedactionError() != nil {
		return redactedPayloadValue
	}
	if conditionOperandContainsSensitiveReference(raw) {
		return scrubSensitiveTextWithConfig(raw, vu.RedactionConfig)
	}
	return scrubSensitiveTextWithConfig(resolved, vu.RedactionConfig)
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
	if control != nil && (control.Count > 0 || control.ForEach != nil) {
		return dsl.MaxControlLoopIterations
	}
	return defaultControlLoopMaxIterations
}

// controlLoopCursor owns the loop language's pass order and temporary bindings.
// Execution adapters retain their own cancellation, flow signals, observation,
// and think-time ordering. In particular, replay observes predicates before it
// dispatches a body, while live execution does not publish debugger evidence.
type controlLoopCursor struct {
	vu                  *VUser
	control             *dsl.ControlFlow
	rows                dataRowView
	maxPasses           int
	pass                int
	restore             func()
	conditionCapReached bool
}

type controlLoopPass struct {
	index          int
	condition      *dsl.Condition
	checkCondition bool
	rowReceipt     dataRowReceipt
}

func newControlLoopCursor(vu *VUser, control *dsl.ControlFlow) *controlLoopCursor {
	outerIndex, hadIndex := vu.Variables["loop_index"]
	restoreRows := func() {}
	cursor := &controlLoopCursor{vu: vu, control: control, maxPasses: controlLoopMaxPasses(control)}
	if control.ForEach != nil {
		cursor.rows = vu.foreachRows(control.ForEach)
		restoreRows = vu.captureForEachRestore(control.ForEach.Var, cursor.rows)
	}
	cursor.restore = func() {
		restoreRows()
		if hadIndex {
			vu.Variables["loop_index"] = outerIndex
		} else {
			delete(vu.Variables, "loop_index")
		}
	}
	return cursor
}

// pending preserves the foreach boundary: an empty/exhausted row view has no
// next pass. Count/condition loops perform their final context/bounds probe in
// the adapter, as they did before sharing this cursor.
func (cursor *controlLoopCursor) pending() bool {
	return cursor.control.ForEach == nil || cursor.pass < min(cursor.rows.Len(), cursor.maxPasses)
}

func (cursor *controlLoopCursor) next() (controlLoopPass, bool) {
	pass := cursor.pass
	cursor.pass++
	current := controlLoopPass{index: pass + 1}
	cursor.vu.Variables["loop_index"] = strconv.Itoa(current.index)
	switch {
	case cursor.control.ForEach != nil:
		if pass >= min(cursor.rows.Len(), cursor.maxPasses) {
			return current, false
		}
		row, receipt := cursor.rows.rowWithReceipt(pass)
		cursor.vu.bindForEachRow(cursor.control.ForEach.Var, row)
		current.rowReceipt = receipt
		current.condition = cursor.control.ForEach.Condition
		current.checkCondition = current.condition != nil
	case cursor.control.Count > 0:
		if pass >= min(cursor.control.Count, cursor.maxPasses) {
			return current, false
		}
	default:
		if pass >= cursor.maxPasses {
			cursor.conditionCapReached = cursor.control.MaxIterations <= 0
			return current, false
		}
		current.condition = cursor.control.Condition
		current.checkCondition = !(cursor.control.DoWhile && pass == 0)
	}
	return current, true
}

func (cursor *controlLoopCursor) implicitTruncation() int {
	if cursor.control.MaxIterations > 0 {
		return 0
	}
	count := cursor.control.Count
	if cursor.control.ForEach != nil {
		count = cursor.rows.Len()
	}
	if count > cursor.maxPasses {
		return count
	}
	return 0
}

func (cursor *controlLoopCursor) restoreBindings() {
	if cursor.restore != nil {
		cursor.restore()
		cursor.restore = nil
	}
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
	rowKey := ""
	var priorRow ReplayRuntimeRow
	hadRow := false
	if vu.replayRuntime != nil {
		rowKey = "foreach\x00" + varName
		priorRow, hadRow = vu.replayRuntime.rows[rowKey]
	}
	prior := map[string]savedVar{}
	for _, col := range rows.Columns() {
		key := varName + "." + col
		v, ok := vu.Variables[key]
		prior[key] = savedVar{value: v, had: ok}
	}
	return func() {
		if vu.replayRuntime != nil {
			if hadRow {
				vu.replayRuntime.rows[rowKey] = priorRow
			} else {
				delete(vu.replayRuntime.rows, rowKey)
			}
		}
		for key, s := range prior {
			if s.had {
				vu.Variables[key] = s.value
			} else {
				delete(vu.Variables, key)
			}
		}
	}
}
