package enterprisearchive

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A helper called with arguments, such as
//
//	void formatCTN(char* str) {
//	    char area[4] = "\0", number[11] = "\0";
//	    strncpy(area, str + 1, 3);
//	    sprintf(number, "%s-%s", area, str);
//	    lr_save_string(number, "FormattedCTN");
//	}
//
// reads C parameters and character-buffer locals that no LoadRunner parameter
// holds. translateHelperCall rewrites such a body into equivalent LoadRunner
// API calls on namespaced variables (clocal_<function>_<name>): parameters are
// bound with lr_save_string, strncpy becomes lr_save_var with an offset, strcpy
// lr_save_string, strcat and sprintf lr_param_sprintf, and every other call
// reads locals through lr_eval_string. The translated text is parsed like any
// source, so the converter needs nothing new. Anything else (control flow,
// assignments, non-string parameters) keeps the call for review.

var (
	cIdentifierPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	cIdentifierTokenRe     = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	cIntegerLiteralPattern = regexp.MustCompile(`^[0-9]+$`)
	cCallStatementPattern  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*\(([\s\S]*)\)$`)
	cDeclaratorPattern     = regexp.MustCompile(`^\*?\s*([A-Za-z_][A-Za-z0-9_]*)\s*(\[[^\]]*\])?\s*(?:=\s*([\s\S]+))?$`)
	cOffsetOperandPattern  = regexp.MustCompile(`^([\s\S]+?)\s*\+\s*([0-9]+)$`)
)

// helperSource is a helper definition located in its file's parsed text.
type helperSource struct {
	params  []string // parameter names, in order
	stmts   []cStatement
	invalid string // why the definition cannot be translated
}

type cStatement struct {
	text string
	line int
}

// translateHelperCall returns the calls a helper invocation with arguments
// performs, or a reason it cannot be translated.
func (table sourceHelperTable) translateHelperCall(call ParsedFunction, definition *sourceHelperDefinition) ([]ParsedFunction, string) {
	content, ok := table.contents[strings.ToLower(definition.file)]
	if !ok {
		return nil, "its source is unavailable"
	}
	source := table.helperSource(content, definition)
	if source.invalid != "" {
		return nil, source.invalid
	}
	args := helperCallArguments(call)
	if len(args) != len(source.params) {
		return nil, fmt.Sprintf("it is called with %d argument(s) but declares %d", len(args), len(source.params))
	}
	namespace := strings.ToLower(definition.name)
	locals := map[string]string{}
	for _, param := range source.params {
		locals[param] = helperLocalName(namespace, param)
	}
	// The synthetic source opens with two wrapper lines; lineOf maps each
	// later synthetic line back to the statement it came from (a statement
	// or argument may span lines).
	var lines []string
	lineOf := map[int]int{}
	nextLine := 3
	emit := func(text string, line int) {
		lines = append(lines, text)
		for offset := 0; offset <= strings.Count(text, "\n"); offset++ {
			lineOf[nextLine+offset] = line
		}
		nextLine += 1 + strings.Count(text, "\n")
	}
	for index, param := range source.params {
		value, ok := helperOperand(args[index], nil)
		if !ok {
			return nil, fmt.Sprintf("argument %d is not a string value", index+1)
		}
		emit(fmt.Sprintf("lr_save_string(%s, %q);", value, locals[param]), call.LineNumber)
	}
	known := func(name string) bool {
		if _, ok := lookupSourceFunction(name); ok || isSourceFileOutputFunction(name) {
			return true
		}
		return table.definesHelper(name)
	}
	for index, stmt := range source.stmts {
		translated, reason := translateHelperStatement(stmt.text, namespace, locals, index == len(source.stmts)-1, known)
		if reason != "" {
			return nil, fmt.Sprintf("line %d %s", stmt.line, reason)
		}
		for _, text := range translated {
			emit(text, stmt.line)
		}
	}
	if len(lines) == 0 {
		return nil, ""
	}
	parsed, err := ParseActionFile(definition.file, "__apexload_helper()\n{\n"+strings.Join(lines, "\n")+"\n}\n")
	if err != nil || parsed == nil {
		return nil, "its translation could not be parsed"
	}
	out := make([]ParsedFunction, 0, len(parsed.Functions))
	for _, function := range parsed.Functions {
		if line, ok := lineOf[function.LineNumber]; ok {
			function.LineNumber = line
			function.EndLine = line
		}
		function.Filename = definition.file
		function.sourceFunction = definition.name
		out = append(out, function)
	}
	return out, ""
}

func helperLocalName(namespace, name string) string {
	return canonicalSourceParameterName("clocal_" + namespace + "_" + name)
}

// helperCallArguments returns the call's arguments as C source text.
func helperCallArguments(call ParsedFunction) []string {
	if len(call.CallArgs) > 0 {
		args := make([]string, 0, len(call.CallArgs))
		for _, arg := range call.CallArgs {
			args = append(args, strings.TrimSpace(arg.Raw))
		}
		return args
	}
	return append([]string(nil), call.RawArgs...)
}

// helperOperand renders a string-valued C expression as LoadRunner source: a
// string literal, an lr_eval_string call, or a translated local.
func helperOperand(expression string, locals map[string]string) (string, bool) {
	expression = strings.TrimSpace(expression)
	if _, ok := decodeAdjacentCStringLiterals(expression); ok {
		return expression, true
	}
	if name, ok := locals[expression]; ok {
		return fmt.Sprintf("lr_eval_string(\"{%s}\")", name), true
	}
	if match := cCallStatementPattern.FindStringSubmatch(expression); match != nil && match[1] == "lr_eval_string" {
		if args := splitCFunctionArgs(match[2]); len(args) == 1 {
			if _, ok := decodeAdjacentCStringLiterals(strings.TrimSpace(args[0])); ok {
				return expression, true
			}
		}
	}
	return "", false
}

// translateHelperStatement translates one statement of a helper body.
func translateHelperStatement(stmt, namespace string, locals map[string]string, last bool, known func(string) bool) ([]string, string) {
	stmt = strings.TrimSpace(stmt)
	switch {
	case stmt == "":
		return nil, ""
	case stmt == "return" || strings.HasPrefix(stmt, "return ") || strings.HasPrefix(stmt, "return("):
		if last {
			return nil, ""
		}
		return nil, "returns early"
	}
	if declaration, ok := strings.CutPrefix(stmt, "char"); ok && (strings.HasPrefix(declaration, " ") || strings.HasPrefix(declaration, "*") || strings.HasPrefix(declaration, "\t")) {
		return translateHelperDeclaration(declaration, namespace, locals)
	}
	for _, prefix := range []string{"const char", "static char", "unsigned char"} {
		if declaration, ok := strings.CutPrefix(stmt, prefix); ok {
			return translateHelperDeclaration(declaration, namespace, locals)
		}
	}
	match := cCallStatementPattern.FindStringSubmatch(stmt)
	if match == nil {
		return nil, "uses a C statement other than a call or character declaration"
	}
	name, args := match[1], splitCFunctionArgs(match[2])
	for index := range args {
		args[index] = strings.TrimSpace(args[index])
	}
	destination := func() (string, bool) {
		if len(args) == 0 {
			return "", false
		}
		variable, ok := locals[args[0]]
		return variable, ok
	}
	switch name {
	case "if", "for", "while", "switch", "do":
		return nil, "uses C control flow"
	case "strncpy":
		target, ok := destination()
		if !ok || len(args) != 3 || !cIntegerLiteralPattern.MatchString(args[2]) {
			return nil, "uses strncpy outside the supported form strncpy(local, value[ + N], N)"
		}
		source, offset := args[1], ""
		if parts := cOffsetOperandPattern.FindStringSubmatch(source); parts != nil {
			source, offset = parts[1], " + "+parts[2]
		}
		value, ok := helperOperand(source, locals)
		if !ok {
			return nil, "copies from an unsupported value"
		}
		return []string{fmt.Sprintf("lr_save_var(%s%s, %s, 0, %q);", value, offset, args[2], target)}, ""
	case "strcpy":
		target, ok := destination()
		value, valueOK := "", false
		if len(args) == 2 {
			value, valueOK = helperOperand(args[1], locals)
		}
		if !ok || !valueOK {
			return nil, "uses strcpy outside the supported form strcpy(local, value)"
		}
		return []string{fmt.Sprintf("lr_save_string(%s, %q);", value, target)}, ""
	case "strcat":
		target, ok := destination()
		value, valueOK := "", false
		if len(args) == 2 {
			value, valueOK = helperOperand(args[1], locals)
		}
		if !ok || !valueOK {
			return nil, "uses strcat outside the supported form strcat(local, value)"
		}
		return []string{fmt.Sprintf("lr_param_sprintf(%q, \"%%s%%s\", lr_eval_string(\"{%s}\"), %s);", target, target, value)}, ""
	case "sprintf":
		target, ok := destination()
		if !ok || len(args) < 2 {
			return nil, "uses sprintf outside the supported form sprintf(local, format, values...)"
		}
		if _, literal := decodeAdjacentCStringLiterals(args[1]); !literal {
			return nil, "uses a sprintf format that is not a string literal"
		}
		rendered := []string{strconv.Quote(target), args[1]}
		for _, arg := range args[2:] {
			if cIntegerLiteralPattern.MatchString(arg) {
				rendered = append(rendered, arg)
				continue
			}
			value, ok := helperOperand(arg, locals)
			if !ok {
				return nil, "formats an unsupported value"
			}
			rendered = append(rendered, value)
		}
		return []string{"lr_param_sprintf(" + strings.Join(rendered, ", ") + ");"}, ""
	case "memset":
		target, ok := destination()
		if !ok || len(args) != 3 || args[1] != "0" {
			return nil, "uses memset outside the supported form memset(local, 0, N)"
		}
		return []string{fmt.Sprintf("lr_save_string(\"\", %q);", target)}, ""
	}
	// A LoadRunner API call or another helper reads locals through
	// lr_eval_string. Any other C function may write into a local buffer
	// (memcpy, itoa, sscanf), which cannot be represented, so it is refused
	// rather than silently dropping the write.
	if !known(name) {
		return nil, "calls " + name + "(), which has no LoadRunner equivalent"
	}
	rendered := make([]string, 0, len(args))
	for _, arg := range args {
		rendered = append(rendered, substituteHelperLocals(arg, locals))
	}
	return []string{name + "(" + strings.Join(rendered, ", ") + ");"}, ""
}

// translateHelperDeclaration initializes character locals.
func translateHelperDeclaration(declaration, namespace string, locals map[string]string) ([]string, string) {
	var out []string
	for _, declarator := range splitCFunctionArgs(strings.TrimSpace(declaration)) {
		match := cDeclaratorPattern.FindStringSubmatch(strings.TrimSpace(declarator))
		if match == nil {
			return nil, "declares a character local in an unsupported form"
		}
		name := match[1]
		variable := helperLocalName(namespace, name)
		value := `""`
		if initializer := strings.TrimSpace(match[3]); initializer != "" {
			if literal, ok := decodeAdjacentCStringLiterals(initializer); ok && strings.Trim(literal, "\x00") == "" {
				value = `""`
			} else if rendered, ok := helperOperand(initializer, locals); ok {
				value = rendered
			} else {
				return nil, "initializes a character local with an unsupported value"
			}
		}
		locals[name] = variable
		out = append(out, fmt.Sprintf("lr_save_string(%s, %q);", value, variable))
	}
	return out, ""
}

// substituteHelperLocals replaces local and parameter identifiers outside
// string literals with lr_eval_string reads of their variables.
func substituteHelperLocals(expression string, locals map[string]string) string {
	if strings.TrimSpace(expression) == "" {
		return expression
	}
	var out strings.Builder
	inString := false
	escaped := false
	start := 0
	flush := func(end int) {
		segment := expression[start:end]
		out.WriteString(cIdentifierTokenRe.ReplaceAllStringFunc(segment, func(token string) string {
			if name, ok := locals[token]; ok {
				return fmt.Sprintf("lr_eval_string(\"{%s}\")", name)
			}
			return token
		}))
	}
	for index := 0; index < len(expression); index++ {
		ch := expression[index]
		switch {
		case inString && escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case inString && ch == '"':
			inString = false
			out.WriteString(expression[start : index+1])
			start = index + 1
		case !inString && ch == '"':
			flush(index)
			inString = true
			start = index
		}
	}
	if inString {
		out.WriteString(expression[start:])
	} else {
		flush(len(expression))
	}
	return out.String()
}

// helperSource locates a helper's definition once per table.
func (table sourceHelperTable) helperSource(content string, definition *sourceHelperDefinition) helperSource {
	key := strings.ToLower(definition.file) + "\x00" + strings.ToLower(definition.name)
	if source, ok := table.sources[key]; ok {
		return source
	}
	fileKey := strings.ToLower(definition.file)
	code, ok := table.blanked[fileKey]
	if !ok {
		code = blankCComments(content)
		if table.blanked != nil {
			table.blanked[fileKey] = code
		}
	}
	source := locateHelperSource(code, definition.name)
	if table.sources != nil {
		table.sources[key] = source
	}
	return source
}

// locateHelperSource finds a function definition in comment-free source text
// (see blankCComments) and splits its body into top-level statements.
func locateHelperSource(code, name string) helperSource {
	header := regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(name) + `\s*\(([^()]*)\)\s*\{`)
	var match []int
	for _, candidate := range header.FindAllStringSubmatchIndex(code, -1) {
		if cBraceDepthAt(code, candidate[0]) == 0 {
			match = candidate
			break
		}
	}
	if match == nil {
		return helperSource{invalid: "its definition was not found"}
	}
	bodyStart := match[1]
	bodyEnd := cMatchingBrace(code, bodyStart-1)
	if bodyEnd < 0 {
		return helperSource{invalid: "its body is not terminated"}
	}
	params, reason := helperParameters(code[match[2]:match[3]])
	if reason != "" {
		return helperSource{invalid: reason}
	}
	source := helperSource{params: params}
	line := 1 + strings.Count(code[:bodyStart], "\n")
	var current strings.Builder
	currentLine := line
	inString := false
	var quote byte
	escaped := false
	depth := 0
	pendingSpace := false
	body := code[bodyStart:bodyEnd]
	for index := 0; index < len(body); index++ {
		ch := body[index]
		if ch == '\n' {
			line++
		}
		if !inString && isASCIIWhitespace(ch) {
			// Whitespace between tokens collapses to one space; text
			// inside string and character literals is kept exactly.
			pendingSpace = current.Len() > 0
			continue
		}
		if current.Len() == 0 {
			currentLine = line
		}
		switch {
		case inString && escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case inString && ch == quote:
			inString = false
		case !inString && (ch == '"' || ch == '\''):
			inString, quote = true, ch
		case !inString && (ch == '(' || ch == '['):
			depth++
		case !inString && (ch == ')' || ch == ']'):
			depth--
		case !inString && (ch == '{' || ch == '}'):
			return helperSource{invalid: "its body uses a nested block"}
		case !inString && ch == ';' && depth == 0:
			source.stmts = append(source.stmts, cStatement{text: current.String(), line: currentLine})
			current.Reset()
			pendingSpace = false
			continue
		}
		if pendingSpace {
			current.WriteByte(' ')
			pendingSpace = false
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		return helperSource{invalid: "its body ends without a semicolon"}
	}
	return source
}

// helperParameters accepts character-pointer and character-array parameters.
func helperParameters(list string) ([]string, string) {
	list = strings.TrimSpace(list)
	if list == "" || list == "void" {
		return nil, ""
	}
	var names []string
	for _, raw := range splitCFunctionArgs(list) {
		param := strings.TrimSpace(raw)
		if !strings.HasPrefix(param, "char") && !strings.HasPrefix(param, "const char") {
			return nil, "it takes a parameter that is not a string (" + param + ")"
		}
		tokens := cIdentifierTokenRe.FindAllString(param, -1)
		if len(tokens) == 0 {
			return nil, "it declares an unnamed parameter"
		}
		name := tokens[len(tokens)-1]
		if name == "char" || !cIdentifierPattern.MatchString(name) {
			return nil, "it declares an unnamed parameter"
		}
		names = append(names, name)
	}
	return names, ""
}

// blankCComments replaces comments with spaces, keeping newlines and string
// literals, so offsets and line numbers still match the source.
func blankCComments(content string) string {
	out := []byte(content)
	inString, inChar, inLine, inBlock, escaped := false, false, false, false, false
	for index := 0; index < len(out); index++ {
		ch := out[index]
		switch {
		case inLine:
			if ch == '\n' {
				inLine = false
			} else {
				out[index] = ' '
			}
		case inBlock:
			if ch == '*' && index+1 < len(out) && out[index+1] == '/' {
				out[index], out[index+1] = ' ', ' '
				index++
				inBlock = false
			} else if ch != '\n' {
				out[index] = ' '
			}
		case inString || inChar:
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case inString && ch == '"', inChar && ch == '\'':
				inString, inChar = false, false
			}
		case ch == '"':
			inString = true
		case ch == '\'':
			inChar = true
		case ch == '/' && index+1 < len(out) && out[index+1] == '/':
			out[index] = ' '
			inLine = true
		case ch == '/' && index+1 < len(out) && out[index+1] == '*':
			out[index], out[index+1] = ' ', ' '
			index++
			inBlock = true
		}
	}
	return string(out)
}

// cBraceDepthAt returns the brace depth at offset, ignoring string literals.
func cBraceDepthAt(code string, offset int) int {
	depth := 0
	inString, escaped := false, false
	for index := 0; index < offset && index < len(code); index++ {
		ch := code[index]
		switch {
		case inString && escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case inString && ch == '"':
			inString = false
		case !inString && ch == '"':
			inString = true
		case !inString && ch == '{':
			depth++
		case !inString && ch == '}':
			depth--
		}
	}
	return depth
}

// cMatchingBrace returns the offset of the brace closing the one at open.
func cMatchingBrace(code string, open int) int {
	depth := 0
	inString, escaped := false, false
	for index := open; index < len(code); index++ {
		ch := code[index]
		switch {
		case inString && escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case inString && ch == '"':
			inString = false
		case !inString && ch == '"':
			inString = true
		case !inString && ch == '{':
			depth++
		case !inString && ch == '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}
