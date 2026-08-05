package enterprisearchive

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Source scripts guard calls with C if/else statements and loops. The AST
// parser records each call's enclosing condition chain and loop headers; this
// file translates the recognizable shapes into DSL control_if / control_loop
// blocks so the imported script keeps its branching behavior instead of being
// flattened into a straight line.

// Metadata keys used to carry flow context from emission to the structuring
// pass. The keys prefixed with "__" are internal and removed from the final
// script; the display keys stay on wrappers (or, for untranslated flow, on the
// affected actions) for review. The *_id keys carry per-construct identities
// (source positions) so two distinct constructs with identical text never
// merge into one block.
const (
	sourceFlowConditionKey     = "source_condition"
	sourceFlowConditionExprKey = "__source_condition_expr"
	sourceFlowConditionIDKey   = "__source_condition_id"
	sourceFlowLoopKey          = "source_loop"
	sourceFlowLoopExprKey      = "__source_loop_expr"
	sourceFlowLoopCountKey     = "__source_loop_count"
	sourceFlowLoopIDKey        = "__source_loop_id"
)

// numexpr: atoi(lr_eval_string("{param}")), optionally parenthesized.
const sourceAtoiParamPattern = `\(*\s*atoi\s*\(\s*lr_eval_string\s*\(\s*"\{([A-Za-z_][A-Za-z0-9_]*)\}"\s*\)\s*\)\s*\)*`

var (
	sourceCondNumCmpRe     = regexp.MustCompile(`^` + sourceAtoiParamPattern + `\s*(==|!=|>=|<=|>|<)\s*\(*\s*(-?\d+)\s*\)*$`)
	sourceCondNumCmpRevRe  = regexp.MustCompile(`^\(*\s*(-?\d+)\s*\)*\s*(==|!=|>=|<=|>|<)\s*` + sourceAtoiParamPattern + `$`)
	sourceCondNumTruthyRe  = regexp.MustCompile(`^` + sourceAtoiParamPattern + `$`)
	sourceCondStrcmpRe     = regexp.MustCompile(`^strcmp\s*\(\s*lr_eval_string\s*\(\s*"\{([A-Za-z_][A-Za-z0-9_]*)\}"\s*\)\s*,\s*"((?:\\.|[^"\\])*)"\s*\)\s*(==|!=)\s*0$`)
	sourceCondStrcmpRevRe  = regexp.MustCompile(`^strcmp\s*\(\s*"((?:\\.|[^"\\])*)"\s*,\s*lr_eval_string\s*\(\s*"\{([A-Za-z_][A-Za-z0-9_]*)\}"\s*\)\s*\)\s*(==|!=)\s*0$`)
	sourceCondStrstrRe     = regexp.MustCompile(`^strstr\s*\(\s*lr_eval_string\s*\(\s*"\{([A-Za-z_][A-Za-z0-9_]*)\}"\s*\)\s*,\s*"((?:\\.|[^"\\])*)"\s*\)\s*(==|!=)\s*(?:NULL|0)$`)
	sourceForLoopHeaderRe  = regexp.MustCompile(`^for\s*\(\s*(?:int\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(-?\d+)\s*;\s*([A-Za-z_][A-Za-z0-9_]*)\s*(<=|<)\s*(.+?)\s*;\s*(?:([A-Za-z_][A-Za-z0-9_]*)\s*\+\+|\+\+\s*([A-Za-z_][A-Za-z0-9_]*))\s*\)$`)
	sourceForBoundParamRe  = regexp.MustCompile(`^` + sourceAtoiParamPattern + `$`)
	sourceForBoundNumberRe = regexp.MustCompile(`^\(*\s*(\d+)\s*\)*$`)
	sourceWhileHeaderRe    = regexp.MustCompile(`^while\s*\((.+)\)$`)
)

// translateSourceConditionToCEL converts one raw C condition into a CEL
// expression over imported variables. ok is false when the shape is not
// recognized; the caller then keeps the linear import and warns.
func translateSourceConditionToCEL(condition string) (string, bool) {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return "", false
	}

	// Peel a negation wrapper: !(...) over the whole condition.
	if strings.HasPrefix(condition, "!") {
		inner := strings.TrimSpace(strings.TrimPrefix(condition, "!"))
		if strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") && balancedParens(inner[1:len(inner)-1]) {
			translated, ok := translateSourceConditionToCEL(inner[1 : len(inner)-1])
			if !ok {
				return "", false
			}
			return "!(" + translated + ")", true
		}
		return "", false
	}

	// Strip redundant levels of surrounding parentheses.
	for strings.HasPrefix(condition, "(") && strings.HasSuffix(condition, ")") && balancedParens(condition[1:len(condition)-1]) {
		condition = strings.TrimSpace(condition[1 : len(condition)-1])
	}

	if m := sourceCondNumCmpRe.FindStringSubmatch(condition); m != nil {
		return fmt.Sprintf("%s %s %s", celSafeAtoi(m[1]), m[2], m[3]), true
	}
	if m := sourceCondNumCmpRevRe.FindStringSubmatch(condition); m != nil {
		return fmt.Sprintf("%s %s %s", m[1], m[2], celSafeAtoi(m[3])), true
	}
	if m := sourceCondNumTruthyRe.FindStringSubmatch(condition); m != nil {
		return fmt.Sprintf("%s != 0", celSafeAtoi(m[1])), true
	}
	if m := sourceCondStrcmpRe.FindStringSubmatch(condition); m != nil {
		return fmt.Sprintf("%s %s %s", celSafeStr(m[1]), strcmpCELOp(m[3]), strconv.Quote(unescapeSourceCString(m[2]))), true
	}
	if m := sourceCondStrcmpRevRe.FindStringSubmatch(condition); m != nil {
		return fmt.Sprintf("%s %s %s", celSafeStr(m[2]), strcmpCELOp(m[3]), strconv.Quote(unescapeSourceCString(m[1]))), true
	}
	if m := sourceCondStrstrRe.FindStringSubmatch(condition); m != nil {
		expr := fmt.Sprintf("%s.contains(%s)", celSafeStr(m[1]), strconv.Quote(unescapeSourceCString(m[2])))
		if m[3] == "==" {
			expr = "!(" + expr + ")"
		}
		return expr, true
	}
	return "", false
}

func strcmpCELOp(op string) string {
	if op == "!=" {
		return "!="
	}
	return "=="
}

// celSafeAtoi renders a C atoi(lr_eval_string("{name}")) as CEL that matches
// atoi's forgiving semantics: an unset or non-numeric parameter evaluates to
// 0 instead of erroring (a CEL evaluation error would silently force the whole
// condition false, inverting negated guards).
func celSafeAtoi(rawName string) string {
	name := canonicalSourceParameterName(rawName)
	return fmt.Sprintf(`(has(vars.%s) && var.%s.matches("^-?[0-9]+$") ? int(var.%s) : 0)`, name, name, name)
}

// celSafeStr renders a C lr_eval_string("{name}") as CEL: an unset parameter
// evaluates to the literal "{name}" token, matching source-tool behavior.
func celSafeStr(rawName string) string {
	name := canonicalSourceParameterName(rawName)
	return fmt.Sprintf(`(has(vars.%s) ? var.%s : %s)`, name, name, strconv.Quote("{"+rawName+"}"))
}

// translateSourceConditionChain translates a chain of nested conditions
// (outermost first) into one CEL conjunction.
func translateSourceConditionChain(conditions []string) (string, bool) {
	if len(conditions) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		translated, ok := translateSourceConditionToCEL(condition)
		if !ok {
			return "", false
		}
		parts = append(parts, translated)
	}
	if len(parts) == 1 {
		return parts[0], true
	}
	for i, part := range parts {
		parts[i] = "(" + part + ")"
	}
	return strings.Join(parts, " && "), true
}

// sourceLoopTranslation is the DSL form of a recognized loop header: either a
// fixed pass count or a per-pass CEL condition over the engine-provided
// 1-based {{var.loop_index}}.
type sourceLoopTranslation struct {
	Count         int
	ConditionExpr string
}

// translateSourceLoopHeader recognizes count-style for-loops
// (for (i = N0; i <= bound; i++)) and condition while-loops. A
// atoi(lr_eval_string("{param}")) bound is re-evaluated every pass through the
// loop condition — matching C semantics.
func translateSourceLoopHeader(header string) (sourceLoopTranslation, bool) {
	header = strings.TrimSpace(header)

	if m := sourceWhileHeaderRe.FindStringSubmatch(header); m != nil {
		expr, ok := translateSourceConditionToCEL(m[1])
		if !ok {
			return sourceLoopTranslation{}, false
		}
		return sourceLoopTranslation{ConditionExpr: expr}, true
	}

	m := sourceForLoopHeaderRe.FindStringSubmatch(header)
	if m == nil {
		return sourceLoopTranslation{}, false
	}
	loopVar, start, condVar, op, bound := m[1], m[2], m[3], m[4], strings.TrimSpace(m[5])
	postVar := convertutil.FirstNonEmpty(m[6], m[7])
	if loopVar != condVar || loopVar != postVar {
		return sourceLoopTranslation{}, false
	}
	startValue, err := strconv.Atoi(start)
	if err != nil {
		return sourceLoopTranslation{}, false
	}
	// Passes = bound + (op == "<=" ? 1 : 0) - start; loop_index runs 1..Passes.
	adjust := -startValue
	if op == "<=" {
		adjust++
	}

	if bm := sourceForBoundNumberRe.FindStringSubmatch(bound); bm != nil {
		boundValue, err := strconv.Atoi(bm[1])
		if err != nil {
			return sourceLoopTranslation{}, false
		}
		passes := boundValue + adjust
		if passes < 0 {
			passes = 0
		}
		return sourceLoopTranslation{Count: passes}, true
	}
	if bm := sourceForBoundParamRe.FindStringSubmatch(bound); bm != nil {
		expr := fmt.Sprintf("int(var.loop_index) <= %s", celSafeAtoi(bm[1]))
		switch {
		case adjust > 0:
			expr += fmt.Sprintf(" + %d", adjust)
		case adjust < 0:
			expr += fmt.Sprintf(" - %d", -adjust)
		}
		return sourceLoopTranslation{ConditionExpr: expr}, true
	}
	return sourceLoopTranslation{}, false
}

func balancedParens(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// tagSourceFlowMetadata stamps an emitted action with its source control-flow
// context plus pre-translated DSL forms. Translation warnings are emitted once
// per distinct condition/loop text.
func tagSourceFlowMetadata(result *ConvertResult, fn ParsedFunction, action *dsl.Action) {
	if len(fn.Conditions) == 0 && len(fn.Loops) == 0 {
		return
	}
	if result.flowWarned == nil {
		result.flowWarned = make(map[string]bool)
	}
	warned := result.flowWarned
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}

	if len(fn.Loops) > 0 {
		display := strings.Join(fn.Loops, " >> ")
		action.Metadata[sourceFlowLoopKey] = display
		action.Metadata[sourceFlowLoopIDKey] = strings.Join(fn.LoopIDs, ">>")
		if len(fn.Loops) == 1 {
			if loop, ok := translateSourceLoopHeader(fn.Loops[0]); ok {
				if loop.ConditionExpr != "" {
					if dsl.ValidateConditionExpression(loop.ConditionExpr) == nil {
						action.Metadata[sourceFlowLoopExprKey] = loop.ConditionExpr
					}
				} else if loop.Count <= 0 {
					// Faithful to a zero-pass C loop: the body never runs, so
					// the structuring pass drops these actions entirely.
					action.Metadata[sourceFlowLoopCountKey] = "0"
					if !warned["loop0:"+display] {
						warned["loop0:"+display] = true
						result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: loop %q never executes (zero passes); the enclosed calls were dropped from the import", fn.LineNumber, display))
					}
				} else {
					action.Metadata[sourceFlowLoopCountKey] = strconv.Itoa(loop.Count)
				}
			}
		}
		_, hasExpr := action.Metadata[sourceFlowLoopExprKey]
		_, hasCount := action.Metadata[sourceFlowLoopCountKey]
		if !hasExpr && !hasCount && !warned["loop:"+display] {
			warned["loop:"+display] = true
			result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: loop %q is not a recognized count or condition loop; the enclosed calls run once, in order — review metadata.%s", fn.LineNumber, display, sourceFlowLoopKey))
		}
	}

	if len(fn.Conditions) > 0 {
		display := strings.Join(fn.Conditions, " && ")
		action.Metadata[sourceFlowConditionKey] = display
		action.Metadata[sourceFlowConditionIDKey] = strings.Join(fn.ConditionIDs, "&&")
		if expr, ok := translateSourceConditionChain(fn.Conditions); ok && dsl.ValidateConditionExpression(expr) == nil {
			action.Metadata[sourceFlowConditionExprKey] = expr
		} else if !warned["cond:"+display] {
			warned["cond:"+display] = true
			result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: condition %q could not be translated to a DSL condition; the enclosed calls run unconditionally — review metadata.%s", fn.LineNumber, display, sourceFlowConditionKey))
		}
	}

	// A condition wrapping a loop imports as loop-with-per-pass condition (the
	// model nests conditions inside loops); the divergence only matters when
	// the loop body changes the condition's parameter, but surface it.
	if len(fn.Conditions) > 0 && len(fn.Loops) > 0 {
		if condLine, loopLine, ok := sourceFlowFrameLines(fn.ConditionIDs[0], fn.LoopIDs[0]); ok && condLine < loopLine {
			key := "nest:" + fn.ConditionIDs[0] + "|" + fn.LoopIDs[0]
			if !warned[key] {
				warned[key] = true
				result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: condition %q encloses loop %q in the source; the import evaluates the condition on every loop pass instead of once — review if the loop body changes the condition's parameter", fn.LineNumber, strings.Join(fn.Conditions, " && "), strings.Join(fn.Loops, " >> ")))
			}
		}
	}
}

// sourceFlowFrameLines parses the line numbers out of two construct IDs
// ("line:column").
func sourceFlowFrameLines(condID, loopID string) (int, int, bool) {
	parse := func(id string) (int, bool) {
		head, _, _ := strings.Cut(id, ":")
		line, err := strconv.Atoi(head)
		return line, err == nil
	}
	condLine, ok1 := parse(condID)
	loopLine, ok2 := parse(loopID)
	return condLine, loopLine, ok1 && ok2
}

// structureSourceControlFlow rebuilds recognized control flow: consecutive
// actions tagged with the same translated loop become a control_loop, and
// consecutive actions tagged with the same translated condition become a
// control_if (an adjacent negated group folds into its else branch). It runs
// before request compaction so sequences never straddle a branch boundary.
func structureSourceControlFlow(result *ConvertResult) {
	if result == nil || result.Script == nil {
		return
	}
	counters := &sourceFlowCounters{}
	pathMap := make(map[string]string)
	structure := func(actions []dsl.Action, phase string) []dsl.Action {
		return structureFlowActions(actions,
			func(i int) string { return sourceReviewActionPath(phase, i) },
			func(j int) string { return sourceReviewActionPath(phase, j) },
			counters, pathMap, true)
	}
	result.Script.Init = structure(result.Script.Init, "init")
	result.Script.Actions = structure(result.Script.Actions, "action")
	result.Script.Teardown = structure(result.Script.Teardown, "teardown")
	remapSourceReviewTargets(result, pathMap)

	if counters.ifBlocks > 0 || counters.loopBlocks > 0 {
		preserved := ensureEnterpriseArchiveImportPreserved(result.Script)
		preserved["source_control_flow"] = map[string]any{
			"condition_blocks": counters.ifBlocks,
			"loop_blocks":      counters.loopBlocks,
		}
	}
}

type sourceFlowCounters struct {
	ifBlocks   int
	loopBlocks int
}

// structureFlowActions groups one action list. oldPath(i) is the current
// review path of input action i; newPath(j) is the review path of output
// position j. Moves are recorded in pathMap for source-review remapping.
func structureFlowActions(actions []dsl.Action, oldPath, newPath func(int) string, counters *sourceFlowCounters, pathMap map[string]string, allowLoops bool) []dsl.Action {
	out := make([]dsl.Action, 0, len(actions))
	record := func(old, new string) {
		if old != new {
			pathMap[old] = new
		}
	}

	i := 0
	for i < len(actions) {
		action := actions[i]

		if allowLoops {
			loopID := metadataString(action.Metadata, sourceFlowLoopIDKey)
			loopDisplay := metadataString(action.Metadata, sourceFlowLoopKey)
			loopExpr := metadataString(action.Metadata, sourceFlowLoopExprKey)
			loopCount := metadataString(action.Metadata, sourceFlowLoopCountKey)
			if loopID != "" && (loopExpr != "" || loopCount != "") {
				start := i
				var group []dsl.Action
				for i < len(actions) && sourceLoopPathWithin(metadataString(actions[i].Metadata, sourceFlowLoopIDKey), loopID) {
					group = append(group, actions[i])
					i++
				}

				if loopExpr == "" {
					if count, err := strconv.Atoi(loopCount); err == nil && count <= 0 {
						// A zero-pass C loop never runs its body: drop the
						// group, pointing review mappings at the preserved
						// import metadata (a tag-time warning explains it).
						for k := range group {
							record(oldPath(start+k), "metadata.import.preserved.dropped_zero_pass_loop")
						}
						continue
					}
				}

				counters.loopBlocks++
				wrapperPath := newPath(len(out))
				wrapper := dsl.Action{
					Name:     fmt.Sprintf("source_loop_%d", counters.loopBlocks),
					Type:     "control_loop",
					Metadata: map[string]any{sourceFlowLoopKey: loopDisplay},
					Control:  &dsl.ControlFlow{},
				}
				if loopExpr != "" {
					wrapper.Control.Condition = &dsl.Condition{Expr: loopExpr}
				} else if count, err := strconv.Atoi(loopCount); err == nil {
					wrapper.Control.Count = count
					// Keep the engine's runaway cap from silently truncating
					// imported loops with large fixed bounds.
					wrapper.Control.MaxIterations = count
				}
				wrapper.Control.Steps = structureFlowActions(stripFlowTags(group, true),
					func(k int) string { return oldPath(start + k) },
					func(k int) string { return fmt.Sprintf("%s.control.steps[%d]", wrapperPath, k) },
					counters, pathMap, false)
				out = append(out, wrapper)
				continue
			}
		}

		condID := metadataString(action.Metadata, sourceFlowConditionIDKey)
		condDisplay := metadataString(action.Metadata, sourceFlowConditionKey)
		condExpr := metadataString(action.Metadata, sourceFlowConditionExprKey)
		if condID != "" && condExpr != "" {
			// Group by construct identity within the same (possibly
			// untranslated) loop context only, so a branch never swallows
			// actions from a different if-statement or from outside its loop.
			runLoop := metadataString(action.Metadata, sourceFlowLoopIDKey)
			sameRun := func(candidate dsl.Action, id, display string) bool {
				return metadataString(candidate.Metadata, sourceFlowConditionIDKey) == id &&
					metadataString(candidate.Metadata, sourceFlowConditionKey) == display &&
					metadataString(candidate.Metadata, sourceFlowLoopIDKey) == runLoop
			}

			start := i
			var group []dsl.Action
			for i < len(actions) && sameRun(actions[i], condID, condDisplay) {
				group = append(group, actions[i])
				i++
			}
			counters.ifBlocks++
			wrapperPath := newPath(len(out))
			wrapper := dsl.Action{
				Name:     fmt.Sprintf("source_condition_%d", counters.ifBlocks),
				Type:     "control_if",
				Metadata: map[string]any{sourceFlowConditionKey: condDisplay},
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Expr: condExpr},
					Steps:     stripFlowTags(group, false),
				},
			}
			for k := range group {
				record(oldPath(start+k), fmt.Sprintf("%s.control.steps[%d]", wrapperPath, k))
			}

			// An else branch carries the SAME construct identity with the
			// negated condition text.
			negated := "!(" + condDisplay + ")"
			if i < len(actions) && sameRun(actions[i], condID, negated) {
				elseStart := i
				var elseGroup []dsl.Action
				for i < len(actions) && sameRun(actions[i], condID, negated) {
					elseGroup = append(elseGroup, actions[i])
					i++
				}
				wrapper.Control.Else = stripFlowTags(elseGroup, false)
				for k := range elseGroup {
					record(oldPath(elseStart+k), fmt.Sprintf("%s.control.else[%d]", wrapperPath, k))
				}
			}

			out = append(out, wrapper)
			continue
		}

		record(oldPath(i), newPath(len(out)))
		out = append(out, stripFlowTagsOne(action, false))
		i++
	}
	return out
}

func sourceLoopPathWithin(candidate, parent string) bool {
	if candidate == parent {
		return true
	}
	return parent != "" && strings.HasPrefix(candidate, parent+">>")
}

// applyControlFlowThinkTime propagates pacing into structured control blocks:
// every step of a loop body paces (each pass mimics the source script's
// inter-request rhythm); within an if-branch all but the last step pace, the
// wrapper's own think_time_after covering the boundary to the next action.
func applyControlFlowThinkTime(action *dsl.Action) {
	if action.Control == nil {
		return
	}
	loop := action.Type == "control_loop"
	mark := func(steps []dsl.Action) {
		for i := range steps {
			if loop || i < len(steps)-1 {
				steps[i].ThinkTimeAfter = true
			}
			applyControlFlowThinkTime(&steps[i])
		}
	}
	mark(action.Control.Steps)
	mark(action.Control.Else)
}

// stripFlowTags removes internal flow metadata from grouped actions. When
// keepCondition is true the condition tags survive so nested condition
// grouping inside a loop body can still run.
func stripFlowTags(actions []dsl.Action, keepCondition bool) []dsl.Action {
	out := make([]dsl.Action, len(actions))
	for i, action := range actions {
		if keepCondition {
			out[i] = stripLoopTagsOne(action)
		} else {
			out[i] = stripFlowTagsOne(action, false)
		}
	}
	return out
}

func stripLoopTagsOne(action dsl.Action) dsl.Action {
	if len(action.Metadata) == 0 {
		return action
	}
	metadata := cloneSourceAnyMap(action.Metadata)
	delete(metadata, sourceFlowLoopKey)
	delete(metadata, sourceFlowLoopExprKey)
	delete(metadata, sourceFlowLoopCountKey)
	delete(metadata, sourceFlowLoopIDKey)
	if len(metadata) == 0 {
		metadata = nil
	}
	action.Metadata = metadata
	return action
}

func stripFlowTagsOne(action dsl.Action, keepCondition bool) dsl.Action {
	if len(action.Metadata) == 0 {
		return action
	}
	metadata := cloneSourceAnyMap(action.Metadata)
	// Internal keys never survive; an untranslated loop's display tag stays on
	// the action for review (translated loops carry it on the wrapper).
	delete(metadata, sourceFlowLoopExprKey)
	delete(metadata, sourceFlowLoopCountKey)
	delete(metadata, sourceFlowLoopIDKey)
	if !keepCondition {
		// A translated condition moves to the wrapper; an untranslated one
		// keeps its display tag for review.
		if metadataString(metadata, sourceFlowConditionExprKey) != "" {
			delete(metadata, sourceFlowConditionKey)
		}
		delete(metadata, sourceFlowConditionExprKey)
		delete(metadata, sourceFlowConditionIDKey)
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	action.Metadata = metadata
	return action
}
