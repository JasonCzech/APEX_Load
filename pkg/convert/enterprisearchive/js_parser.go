package enterprisearchive

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
)

// VuGen's JavaScript web scripts (Action.js with web.url({...}) calls) are
// translated into the C a VuGen C script would hold for the same steps, and
// that C goes through the ordinary C pipeline, so every C capability applies
// to them. web.<fn>({key: value}) becomes web_<fn>("name", "Key=value", ...,
// LAST); lr.<fn>(...) becomes lr_<fn>(...); if, for and while become the C
// statements the control-flow translation understands. A statement the
// translation cannot express becomes a call to jsUnsupportedFunction, which
// imports as a review step on the statement's line.

// jsUnsupportedFunction names the review step for an untranslatable
// JavaScript statement.
const jsUnsupportedFunction = "js_unsupported_statement"

// jsStepFunctions take the step name as their first argument.
var jsStepFunctions = map[string]bool{
	"web_url": true, "web_submit_data": true, "web_submit_form": true, "web_custom_request": true,
	"web_link": true, "web_image": true, "web_image_check": true, "web_rest": true, "web_service_call": true,
}

// jsAttributeNames maps the JavaScript property names whose VuGen attribute
// is not the name with its first letter capitalized.
var jsAttributeNames = map[string]string{
	"url": "URL", "lb": "LB", "rb": "RB", "dfes": "DFEs", "requestUrl": "RequestUrl",
	"itemData": "ITEMDATA", "extraRes": "EXTRARES", "id": "ID", "uri": "URI",
}

// jsConstants maps lr.* and web.* constants to their C spelling.
var jsConstants = map[string]string{
	"lr.AUTO": "LR_AUTO", "lr.PASS": "LR_PASS", "lr.FAIL": "LR_FAIL", "lr.STOP": "LR_STOP",
	"lr.MSG_CLASS_DISABLE_LOG": "LR_MSG_CLASS_DISABLE_LOG", "lr.MSG_CLASS_EXTENDED_LOG": "LR_MSG_CLASS_EXTENDED_LOG",
}

type jsTranslator struct {
	source  string
	file    *file.File
	out     strings.Builder
	line    int
	locals  map[string]bool
	notes   []string
	failure string
}

// translateJavaScriptToC translates one JavaScript action file. It returns
// the C source and a note for each statement that became a review step.
func translateJavaScriptToC(name, source string) (string, []string, error) {
	fileSet := &file.FileSet{}
	program, err := parser.ParseFile(fileSet, name, source, 0)
	if err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", name, err)
	}
	t := &jsTranslator{source: source, file: fileSet.File(program.Idx0()), line: 1}
	for _, statement := range program.Body {
		function, ok := statement.(*ast.FunctionDeclaration)
		if !ok || function.Function == nil || function.Function.Name == nil || function.Function.Body == nil {
			if _, empty := statement.(*ast.EmptyStatement); !empty {
				t.unsupported(statement, "a top-level statement outside a function")
			}
			continue
		}
		t.function(function.Function)
	}
	return t.out.String(), t.notes, nil
}

func (t *jsTranslator) lineOf(idx file.Idx) int {
	if t.file == nil {
		return t.line
	}
	return t.file.Position(int(idx) - t.file.Base()).Line
}

// moveTo starts the next C text on the JavaScript statement's line, so C
// line numbers point at the JavaScript source.
func (t *jsTranslator) moveTo(idx file.Idx) {
	line := t.lineOf(idx)
	for t.line < line {
		t.out.WriteByte('\n')
		t.line++
	}
}

func (t *jsTranslator) write(text string) {
	t.out.WriteString(text)
	t.line += strings.Count(text, "\n")
}

func (t *jsTranslator) function(function *ast.FunctionLiteral) {
	t.locals = map[string]bool{}
	collectJSLoopLocals(function.Body, t.locals)
	t.moveTo(function.Function)
	t.write(string(function.Name.Name) + "()\n{")
	if len(t.locals) > 0 {
		names := make([]string, 0, len(t.locals))
		for local := range t.locals {
			names = append(names, local)
		}
		sort.Strings(names)
		t.write(" int " + strings.Join(names, ", ") + ";")
	}
	for _, statement := range function.Body.List {
		t.statement(statement)
	}
	t.moveTo(function.Body.RightBrace)
	t.write("}\n")
}

// collectJSLoopLocals lists the variables for-loops declare, which C must
// declare at the top of the function.
func collectJSLoopLocals(statement ast.Statement, locals map[string]bool) {
	switch s := statement.(type) {
	case *ast.BlockStatement:
		for _, child := range s.List {
			collectJSLoopLocals(child, locals)
		}
	case *ast.IfStatement:
		collectJSLoopLocals(s.Consequent, locals)
		if s.Alternate != nil {
			collectJSLoopLocals(s.Alternate, locals)
		}
	case *ast.WhileStatement:
		collectJSLoopLocals(s.Body, locals)
	case *ast.DoWhileStatement:
		collectJSLoopLocals(s.Body, locals)
	case *ast.ForStatement:
		switch init := s.Initializer.(type) {
		case *ast.ForLoopInitializerVarDeclList:
			for _, binding := range init.List {
				if identifier, ok := binding.Target.(*ast.Identifier); ok {
					locals[string(identifier.Name)] = true
				}
			}
		case *ast.ForLoopInitializerLexicalDecl:
			for _, binding := range init.LexicalDeclaration.List {
				if identifier, ok := binding.Target.(*ast.Identifier); ok {
					locals[string(identifier.Name)] = true
				}
			}
		}
		collectJSLoopLocals(s.Body, locals)
	}
}

func (t *jsTranslator) statement(statement ast.Statement) {
	switch s := statement.(type) {
	case *ast.EmptyStatement:
	case *ast.BlockStatement:
		t.moveTo(s.LeftBrace)
		t.write("{")
		for _, child := range s.List {
			t.statement(child)
		}
		t.moveTo(s.RightBrace)
		t.write("}")
	case *ast.ExpressionStatement:
		call, ok := s.Expression.(*ast.CallExpression)
		if !ok {
			t.unsupported(statement, "an expression that is not a call")
			return
		}
		text, reason := t.call(call)
		if reason != "" {
			t.unsupported(statement, reason)
			return
		}
		t.moveTo(statement.Idx0())
		t.write(text + ";")
	case *ast.ReturnStatement:
		t.moveTo(s.Return)
		t.write("return 0;")
	case *ast.IfStatement:
		condition, reason := t.expression(s.Test)
		if reason != "" {
			t.unsupported(statement, "its condition uses "+reason)
			return
		}
		t.moveTo(statementIdx(s, s.Test))
		t.write("if (" + condition + ") ")
		t.body(s.Consequent)
		if s.Alternate != nil {
			t.write(" else ")
			t.body(s.Alternate)
		}
	case *ast.WhileStatement:
		condition, reason := t.expression(s.Test)
		if reason != "" {
			t.unsupported(statement, "its condition uses "+reason)
			return
		}
		t.moveTo(statementIdx(s, s.Test))
		t.write("while (" + condition + ") ")
		t.body(s.Body)
	case *ast.DoWhileStatement:
		condition, reason := t.expression(s.Test)
		if reason != "" {
			t.unsupported(statement, "its condition uses "+reason)
			return
		}
		t.moveTo(statementIdx(s, s.Body))
		t.write("do ")
		t.body(s.Body)
		t.write(" while (" + condition + ");")
	case *ast.ForStatement:
		header, reason := t.forHeader(s)
		if reason != "" {
			t.unsupported(statement, reason)
			return
		}
		t.moveTo(statementIdx(s, s.Body))
		t.write("for (" + header + ") ")
		t.body(s.Body)
	default:
		t.unsupported(statement, "a statement the JavaScript import does not translate")
	}
}

// statementIdx is a statement's position; goja leaves some keyword
// positions unset, so the position of a part on its first line stands in.
func statementIdx(statement ast.Statement, part ast.Node) file.Idx {
	if idx := statement.Idx0(); idx > 0 {
		return idx
	}
	return part.Idx0()
}

// body writes a branch or loop body as a C block.
func (t *jsTranslator) body(statement ast.Statement) {
	if block, ok := statement.(*ast.BlockStatement); ok {
		t.statement(block)
		return
	}
	t.write("{")
	t.statement(statement)
	t.write("}")
}

func (t *jsTranslator) forHeader(s *ast.ForStatement) (string, string) {
	var init string
	switch initializer := s.Initializer.(type) {
	case nil:
	case *ast.ForLoopInitializerVarDeclList:
		parts, reason := t.bindings(initializer.List)
		if reason != "" {
			return "", reason
		}
		init = parts
	case *ast.ForLoopInitializerLexicalDecl:
		parts, reason := t.bindings(initializer.LexicalDeclaration.List)
		if reason != "" {
			return "", reason
		}
		init = parts
	case *ast.ForLoopInitializerExpression:
		text, reason := t.expression(initializer.Expression)
		if reason != "" {
			return "", "its initializer uses " + reason
		}
		init = text
	default:
		return "", "a for loop form the JavaScript import does not translate"
	}
	var test, update string
	if s.Test != nil {
		var reason string
		if test, reason = t.expression(s.Test); reason != "" {
			return "", "its condition uses " + reason
		}
	}
	if s.Update != nil {
		var reason string
		if update, reason = t.expression(s.Update); reason != "" {
			return "", "its update uses " + reason
		}
	}
	return init + "; " + test + "; " + update, ""
}

func (t *jsTranslator) bindings(list []*ast.Binding) (string, string) {
	parts := make([]string, 0, len(list))
	for _, binding := range list {
		identifier, ok := binding.Target.(*ast.Identifier)
		if !ok || binding.Initializer == nil {
			return "", "a loop variable without an initializer"
		}
		value, reason := t.expression(binding.Initializer)
		if reason != "" {
			return "", "its initializer uses " + reason
		}
		parts = append(parts, string(identifier.Name)+" = "+value)
	}
	return strings.Join(parts, ", "), ""
}

// unsupported writes a review step for a statement, naming its line.
func (t *jsTranslator) unsupported(statement ast.Statement, reason string) {
	idx := statement.Idx0()
	if ifStatement, ok := statement.(*ast.IfStatement); ok {
		idx = statementIdx(statement, ifStatement.Test)
	}
	line := t.lineOf(idx)
	t.moveTo(idx)
	t.write(fmt.Sprintf("%s(%s);", jsUnsupportedFunction, cStringLiteral(fmt.Sprintf("line %d: %s", line, reason))))
	t.notes = append(t.notes, fmt.Sprintf("line %d: %s", line, reason))
}

// call translates lr.* and web.* calls and the few JavaScript built-ins C
// has an equivalent for.
func (t *jsTranslator) call(call *ast.CallExpression) (string, string) {
	object, method, ok := jsMember(call.Callee)
	if !ok {
		name, isIdentifier := call.Callee.(*ast.Identifier)
		if isIdentifier && len(call.ArgumentList) == 1 {
			switch string(name.Name) {
			case "parseInt", "Number":
				argument, reason := t.expression(call.ArgumentList[0])
				if reason != "" {
					return "", reason
				}
				return "atoi(" + argument + ")", ""
			}
		}
		if isIdentifier && len(call.ArgumentList) == 0 {
			// A call to a function the script defines (a helper).
			return string(name.Name) + "()", ""
		}
		return "", "a call to " + t.text(call.Callee)
	}
	if object != "lr" && object != "web" {
		return "", "a call to " + object + "." + method
	}
	function := object + "_" + snakeCase(method)
	if object == "web" && len(call.ArgumentList) == 1 {
		if literal, isObject := call.ArgumentList[0].(*ast.ObjectLiteral); isObject {
			return t.webCall(function, literal)
		}
	}
	args := make([]string, 0, len(call.ArgumentList))
	for _, argument := range call.ArgumentList {
		text, reason := t.expression(argument)
		if reason != "" {
			return "", reason
		}
		args = append(args, text)
	}
	return function + "(" + strings.Join(args, ", ") + ")", ""
}

// webCall turns web.<fn>({...}) into the C attribute list.
func (t *jsTranslator) webCall(function string, literal *ast.ObjectLiteral) (string, string) {
	var args []string
	stepName := ""
	for _, property := range literal.Value {
		keyed, ok := property.(*ast.PropertyKeyed)
		if !ok || keyed.Computed {
			return "", "a property the JavaScript import does not translate"
		}
		key := jsPropertyName(keyed.Key)
		if key == "" {
			return "", "a property the JavaScript import does not translate"
		}
		if key == "name" && jsStepFunctions[function] {
			value, reason := t.attributeValue(keyed.Value)
			if reason != "" {
				return "", reason
			}
			stepName = value
			continue
		}
		if key == "searchFilters" {
			// searchFilters: {scope: 'Body'} is C's SEARCH_FILTERS marker
			// followed by the filter attributes.
			filters, ok := keyed.Value.(*ast.ObjectLiteral)
			if !ok {
				return "", "searchFilters that is not an object"
			}
			args = append(args, "SEARCH_FILTERS")
			for _, filter := range filters.Value {
				filterKeyed, ok := filter.(*ast.PropertyKeyed)
				if !ok || filterKeyed.Computed {
					return "", "a search filter the JavaScript import does not translate"
				}
				value, reason := t.attributeValue(filterKeyed.Value)
				if reason != "" {
					return "", reason
				}
				args = append(args, cStringLiteral(jsAttributeName(jsPropertyName(filterKeyed.Key))+"="+value))
			}
			continue
		}
		attribute := jsAttributeName(key)
		if attribute == "ITEMDATA" || attribute == "EXTRARES" {
			items, reason := t.itemList(keyed.Value)
			if reason != "" {
				return "", reason
			}
			args = append(args, attribute)
			args = append(args, items...)
			continue
		}
		value, reason := t.attributeValue(keyed.Value)
		if reason != "" {
			return "", reason
		}
		args = append(args, cStringLiteral(attribute+"="+value))
	}
	if jsStepFunctions[function] {
		args = append([]string{cStringLiteral(stepName)}, args...)
	}
	args = append(args, "LAST")
	return function + "(" + strings.Join(args, ", ") + ")", ""
}

// itemList translates itemData / extraRes: a list of {name, value} (or
// {url, referer}) objects becomes "Name=x", "Value=y", ENDITEM groups.
func (t *jsTranslator) itemList(expression ast.Expression) ([]string, string) {
	array, ok := expression.(*ast.ArrayLiteral)
	if !ok {
		return nil, "an item list that is not an array"
	}
	var out []string
	for _, element := range array.Value {
		object, ok := element.(*ast.ObjectLiteral)
		if !ok {
			return nil, "an item that is not an object"
		}
		for _, property := range object.Value {
			keyed, ok := property.(*ast.PropertyKeyed)
			if !ok || keyed.Computed {
				return nil, "an item property the JavaScript import does not translate"
			}
			value, reason := t.attributeValue(keyed.Value)
			if reason != "" {
				return nil, reason
			}
			out = append(out, cStringLiteral(jsAttributeName(jsPropertyName(keyed.Key))+"="+value))
		}
		out = append(out, "ENDITEM")
	}
	return out, ""
}

// attributeValue is the text of an attribute value: a string, number or
// boolean literal.
func (t *jsTranslator) attributeValue(expression ast.Expression) (string, string) {
	switch value := expression.(type) {
	case *ast.StringLiteral:
		return string(value.Value), ""
	case *ast.NumberLiteral:
		return value.Literal, ""
	case *ast.BooleanLiteral:
		if value.Value {
			return "1", ""
		}
		return "0", ""
	case *ast.TemplateLiteral:
		if len(value.Expressions) == 0 && len(value.Elements) == 1 && value.Tag == nil {
			return string(value.Elements[0].Parsed), ""
		}
	}
	return "", "a computed attribute value (" + t.text(expression) + ")"
}

// expression translates the expressions conditions and arguments use.
func (t *jsTranslator) expression(expression ast.Expression) (string, string) {
	switch e := expression.(type) {
	case *ast.StringLiteral:
		return cStringLiteral(string(e.Value)), ""
	case *ast.NumberLiteral:
		return e.Literal, ""
	case *ast.BooleanLiteral:
		if e.Value {
			return "1", ""
		}
		return "0", ""
	case *ast.Identifier:
		return string(e.Name), ""
	case *ast.DotExpression:
		object, member, ok := jsMember(e)
		if ok {
			if constant, known := jsConstants[object+"."+member]; known {
				return constant, ""
			}
		}
		return "", t.text(e)
	case *ast.CallExpression:
		return t.call(e)
	case *ast.UnaryExpression:
		operand, reason := t.expression(e.Operand)
		if reason != "" {
			return "", reason
		}
		switch e.Operator {
		case token.NOT:
			return "!" + operand, ""
		case token.MINUS:
			return "-" + operand, ""
		case token.INCREMENT, token.DECREMENT:
			if e.Postfix {
				return operand + e.Operator.String(), ""
			}
			return e.Operator.String() + operand, ""
		}
		return "", t.text(e)
	case *ast.AssignExpression:
		left, reason := t.expression(e.Left)
		if reason != "" {
			return "", reason
		}
		right, reason := t.expression(e.Right)
		if reason != "" {
			return "", reason
		}
		operator := e.Operator.String()
		if e.Operator == token.ASSIGN {
			operator = "="
		} else {
			operator += "="
		}
		return left + " " + operator + " " + right, ""
	case *ast.BinaryExpression:
		return t.binary(e)
	}
	return "", t.text(expression)
}

func (t *jsTranslator) binary(e *ast.BinaryExpression) (string, string) {
	left, reason := t.expression(e.Left)
	if reason != "" {
		return "", reason
	}
	right, reason := t.expression(e.Right)
	if reason != "" {
		return "", reason
	}
	operator := e.Operator.String()
	switch e.Operator {
	case token.STRICT_EQUAL:
		operator = "=="
	case token.STRICT_NOT_EQUAL:
		operator = "!="
	}
	if operator == "==" || operator == "!=" {
		// C compares strings with strcmp.
		_, leftString := e.Left.(*ast.StringLiteral)
		_, rightString := e.Right.(*ast.StringLiteral)
		if leftString != rightString && (isJSEvalString(e.Left) || isJSEvalString(e.Right)) {
			return "strcmp(" + left + ", " + right + ") " + operator + " 0", ""
		}
	}
	switch e.Operator {
	case token.LOGICAL_AND, token.LOGICAL_OR:
		return "(" + left + ") " + operator + " (" + right + ")", ""
	}
	return left + " " + operator + " " + right, ""
}

func isJSEvalString(expression ast.Expression) bool {
	call, ok := expression.(*ast.CallExpression)
	if !ok {
		return false
	}
	object, member, ok := jsMember(call.Callee)
	return ok && object == "lr" && member == "evalString"
}

// jsMember splits a.b into its object and member names.
func jsMember(expression ast.Expression) (string, string, bool) {
	dot, ok := expression.(*ast.DotExpression)
	if !ok {
		return "", "", false
	}
	object, ok := dot.Left.(*ast.Identifier)
	if !ok {
		return "", "", false
	}
	return string(object.Name), string(dot.Identifier.Name), true
}

func jsPropertyName(key ast.Expression) string {
	switch k := key.(type) {
	case *ast.Identifier:
		return string(k.Name)
	case *ast.StringLiteral:
		return string(k.Value)
	}
	return ""
}

func jsAttributeName(key string) string {
	if name, ok := jsAttributeNames[key]; ok {
		return name
	}
	if key == "" {
		return key
	}
	runes := []rune(key)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// snakeCase turns submitData into submit_data and regSaveParamEx into
// reg_save_param_ex.
func snakeCase(name string) string {
	var out strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(unicode.ToLower(r))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// text is the JavaScript source of an expression, for notes.
func (t *jsTranslator) text(expression ast.Expression) string {
	from, to := int(expression.Idx0())-1, int(expression.Idx1())-1
	if t.file != nil {
		from, to = int(expression.Idx0())-t.file.Base(), int(expression.Idx1())-t.file.Base()
	}
	if from < 0 || to > len(t.source) || from >= to {
		return "an expression"
	}
	text := strings.Join(strings.Fields(t.source[from:to]), " ")
	if len(text) > 80 {
		text = text[:77] + "..."
	}
	return text
}

// cStringLiteral quotes text as a C string literal.
func cStringLiteral(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range text {
		switch r {
		case '\\':
			out.WriteString(`\\`)
		case '"':
			out.WriteString(`\"`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
