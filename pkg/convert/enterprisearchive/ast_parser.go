package enterprisearchive

import (
	"fmt"
	"reflect"
	"strings"

	"modernc.org/cc/v4"
)

const enterpriseArchiveCPrelude = `
int __predefined_declarator;
`

// ParseActionFileAST parses an enterprise C/VUser action file with a full C AST
// and adapts top-level expression-statement calls into the converter's source
// function model.
func ParseActionFileAST(filename string, content string) (*ParsedAction, error) {
	normalized := normalizeEnterpriseArchiveCSource(content)
	ast, err := cc.Parse(&cc.Config{}, []cc.Source{
		{Name: "<enterprise-archive-compat.h>", Value: enterpriseArchiveCPrelude},
		{Name: filename, Value: normalized},
	})
	if err != nil {
		return nil, fmt.Errorf("parse enterprise C archive action %s: %w", filename, err)
	}

	parsed := &ParsedAction{
		Filename:  filename,
		Functions: make([]ParsedFunction, 0),
	}
	originalLines := strings.Split(content, "\n")

	jumpLoops := make(map[string]struct{})
	walkCCNode(ast.TranslationUnit, func(node cc.Node, flow sourceFlowContext) {
		if jump, ok := node.(*cc.JumpStatement); ok {
			for _, loopID := range flow.jumpAffectedLoopIDs(jump.Case) {
				jumpLoops[loopID] = struct{}{}
			}
			return
		}
		stmt, ok := node.(*cc.ExpressionStatement)
		if !ok || stmt.ExpressionList == nil {
			return
		}
		block := strings.TrimSpace(cc.NodeSource(stmt.ExpressionList))
		if block == "" || !reFuncCall.MatchString(block) || isCControlCall(block) {
			return
		}
		if !strings.HasSuffix(block, ";") {
			block += ";"
		}
		startLine, endLine := nodeLineRange(stmt.ExpressionList)
		if startLine == 0 {
			startLine = stmt.Position().Line
		}
		if endLine == 0 {
			endLine = startLine
		}
		rawBlock := rawSourceBlock(originalLines, startLine, endLine)
		fn, parseErr := parseFunctionBlock(block, filename, startLine, endLine, rawBlock)
		if parseErr == nil {
			for _, frame := range flow.conditions {
				fn.Conditions = append(fn.Conditions, frame.Text)
				fn.ConditionIDs = append(fn.ConditionIDs, frame.ID)
			}
			for _, frame := range flow.loops {
				fn.Loops = append(fn.Loops, frame.Text)
				fn.LoopIDs = append(fn.LoopIDs, frame.ID)
			}
			parsed.Functions = append(parsed.Functions, fn)
		}
	})

	// A loop whose body jumps (break/continue/goto/return) cannot be
	// reproduced by a simple count or per-pass condition; defeat its header
	// translation so it imports linearly with a review warning.
	if len(jumpLoops) > 0 {
		for fi := range parsed.Functions {
			fn := &parsed.Functions[fi]
			for li, loopID := range fn.LoopIDs {
				if _, jumped := jumpLoops[loopID]; jumped && !strings.HasSuffix(fn.Loops[li], sourceLoopJumpMarker) {
					fn.Loops[li] += sourceLoopJumpMarker
				}
			}
		}
	}

	return parsed, nil
}

// sourceFlowFrame is one enclosing control construct: its raw C source text
// plus a stable identity (source position) so two distinct constructs with
// identical text are never conflated downstream.
type sourceFlowFrame struct {
	ID   string
	Text string
}

// sourceFlowContext describes the control-flow statements enclosing a call:
// the if/else condition chain (outermost first; else branches pre-negated as
// !(...)) and any enclosing loop headers (outermost first). breakables tracks
// the innermost-last stack of break targets ("loop" frames carry the loop ID,
// "switch" frames an empty ID) for jump attribution.
type sourceFlowContext struct {
	conditions []sourceFlowFrame
	loops      []sourceFlowFrame
	breakables []sourceFlowFrame // Text: "loop" or "switch"; ID: loop ID or ""
}

func (ctx sourceFlowContext) withCondition(id, condition string) sourceFlowContext {
	out := ctx
	out.conditions = append(append([]sourceFlowFrame(nil), ctx.conditions...), sourceFlowFrame{ID: id, Text: condition})
	return out
}

func (ctx sourceFlowContext) withLoop(id, header string) sourceFlowContext {
	out := ctx
	out.loops = append(append([]sourceFlowFrame(nil), ctx.loops...), sourceFlowFrame{ID: id, Text: header})
	out.breakables = append(append([]sourceFlowFrame(nil), ctx.breakables...), sourceFlowFrame{ID: id, Text: "loop"})
	return out
}

func (ctx sourceFlowContext) withSwitch() sourceFlowContext {
	out := ctx
	out.breakables = append(append([]sourceFlowFrame(nil), ctx.breakables...), sourceFlowFrame{Text: "switch"})
	return out
}

// jumpAffectedLoopIDs reports which enclosing loops a jump statement can exit
// or short-circuit: continue and (non-switch) break affect the innermost loop;
// goto and return escape every enclosing loop.
func (ctx sourceFlowContext) jumpAffectedLoopIDs(jumpCase cc.JumpStatementCase) []string {
	switch jumpCase {
	case cc.JumpStatementContinue:
		if len(ctx.loops) > 0 {
			return []string{ctx.loops[len(ctx.loops)-1].ID}
		}
	case cc.JumpStatementBreak:
		if len(ctx.breakables) > 0 {
			innermost := ctx.breakables[len(ctx.breakables)-1]
			if innermost.Text == "loop" {
				return []string{innermost.ID}
			}
		}
	case cc.JumpStatementGoto, cc.JumpStatementGotoExpr, cc.JumpStatementReturn:
		ids := make([]string, 0, len(ctx.loops))
		for _, loop := range ctx.loops {
			ids = append(ids, loop.ID)
		}
		return ids
	}
	return nil
}

func sourceFlowNodeID(node cc.Node) string {
	pos := node.Position()
	return fmt.Sprintf("%d:%d", pos.Line, pos.Column)
}

// sourceLoopJumpMarker is appended to a loop header's display text when the
// loop body contains flow-altering jumps; it deliberately defeats header
// translation so the loop imports linearly with a review warning instead of
// running a wrong number of passes.
const sourceLoopJumpMarker = " [body contains break/continue/goto/return]"

func sourceNodeText(node cc.Node) string {
	if node == nil {
		return ""
	}
	if value := reflect.ValueOf(node); !value.IsValid() || (value.Kind() == reflect.Pointer && value.IsNil()) {
		return ""
	}
	return strings.TrimSpace(cc.NodeSource(node))
}

// sourceLoopHeaderText reconstructs a loop statement's header (without body)
// for downstream translation and review display.
func sourceLoopHeaderText(loop *cc.IterationStatement) string {
	switch loop.Case {
	case cc.IterationStatementWhile:
		return fmt.Sprintf("while (%s)", sourceNodeText(loop.ExpressionList))
	case cc.IterationStatementDo:
		return fmt.Sprintf("do-while (%s)", sourceNodeText(loop.ExpressionList))
	case cc.IterationStatementFor:
		return fmt.Sprintf("for (%s; %s; %s)", sourceNodeText(loop.ExpressionList), sourceNodeText(loop.ExpressionList2), sourceNodeText(loop.ExpressionList3))
	case cc.IterationStatementForDecl:
		// "for" '(' Declaration ExpressionList ';' ExpressionList ')' — the
		// declaration carries the init (and its own semicolon), shifting the
		// condition/post expressions down one slot.
		init := strings.TrimSuffix(strings.TrimSpace(sourceNodeText(loop.Declaration)), ";")
		return fmt.Sprintf("for (%s; %s; %s)", init, sourceNodeText(loop.ExpressionList), sourceNodeText(loop.ExpressionList2))
	default:
		return sourceNodeText(loop)
	}
}

func normalizeEnterpriseArchiveCSource(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#include") {
			lines[i] = ""
			continue
		}
		for _, name := range []string{"Action", "vuser_init", "vuser_end"} {
			if strings.TrimSpace(line) == name+"()" {
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				lines[i] = indent + "int " + name + "()"
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

func nodeLineRange(node cc.Node) (int, int) {
	tokens := cc.NodeTokens(node)
	startLine := 0
	endLine := 0
	for _, token := range tokens {
		pos := token.Position()
		if !pos.IsValid() || pos.Line == 0 {
			continue
		}
		if startLine == 0 || pos.Line < startLine {
			startLine = pos.Line
		}
		if pos.Line > endLine {
			endLine = pos.Line
		}
	}
	return startLine, endLine
}

func rawSourceBlock(lines []string, startLine int, endLine int) string {
	if startLine <= 0 || len(lines) == 0 {
		return ""
	}
	if endLine < startLine {
		endLine = startLine
	}
	start := startLine - 1
	if start >= len(lines) {
		return ""
	}
	end := endLine
	if end > len(lines) {
		end = len(lines)
	}
	return strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n")
}

func walkCCNode(node cc.Node, visit func(cc.Node, sourceFlowContext)) {
	seen := make(map[uintptr]struct{})
	var walkValue func(reflect.Value, sourceFlowContext)
	walkValue = func(value reflect.Value, flow sourceFlowContext) {
		if !value.IsValid() {
			return
		}
		if value.Kind() == reflect.Interface {
			if value.IsNil() {
				return
			}
			value = value.Elem()
		}
		if value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return
			}
			ptr := value.Pointer()
			if ptr != 0 {
				if _, ok := seen[ptr]; ok {
					return
				}
				seen[ptr] = struct{}{}
			}
		}
		if value.CanInterface() {
			if _, ok := value.Interface().(cc.Token); ok {
				return
			}
			if node, ok := value.Interface().(cc.Node); ok {
				visit(node, flow)

				// Branching and looping statements thread their condition into
				// the flow context of their bodies instead of being flattened
				// silently.
				switch typed := node.(type) {
				case *cc.SelectionStatement:
					id := sourceFlowNodeID(typed)
					switch typed.Case {
					case cc.SelectionStatementIf, cc.SelectionStatementIfElse:
						condition := sourceNodeText(typed.ExpressionList)
						walkValue(reflect.ValueOf(typed.ExpressionList), flow)
						walkValue(reflect.ValueOf(typed.Statement), flow.withCondition(id, condition))
						if typed.Statement2 != nil {
							walkValue(reflect.ValueOf(typed.Statement2), flow.withCondition(id, "!("+condition+")"))
						}
						return
					case cc.SelectionStatementSwitch:
						condition := "switch (" + sourceNodeText(typed.ExpressionList) + ")"
						walkValue(reflect.ValueOf(typed.ExpressionList), flow)
						walkValue(reflect.ValueOf(typed.Statement), flow.withCondition(id, condition).withSwitch())
						return
					}
				case *cc.IterationStatement:
					header := sourceLoopHeaderText(typed)
					bodyFlow := flow.withLoop(sourceFlowNodeID(typed), header)
					walkValue(reflect.ValueOf(typed.Declaration), flow)
					walkValue(reflect.ValueOf(typed.ExpressionList), flow)
					walkValue(reflect.ValueOf(typed.ExpressionList2), flow)
					walkValue(reflect.ValueOf(typed.ExpressionList3), flow)
					walkValue(reflect.ValueOf(typed.Statement), bodyFlow)
					return
				}
			}
		}
		if value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Struct:
			valueType := value.Type()
			for i := 0; i < value.NumField(); i++ {
				field := valueType.Field(i)
				if field.PkgPath != "" {
					continue
				}
				walkValue(value.Field(i), flow)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				walkValue(value.Index(i), flow)
			}
		}
	}
	walkValue(reflect.ValueOf(node), sourceFlowContext{})
}
