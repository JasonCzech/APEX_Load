package enterprisearchive

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// sourceCallbackTranslation is one C callback an async conversation or a
// WebSocket connection names: the steps it becomes, or why it does not run.
type sourceCallbackTranslation struct {
	attribute, name string
	// branch is where the steps run; empty when APEX has no callback point.
	branch string
	steps  []dsl.Action
	// trivial is set when the body does nothing APEX reproduces: VuGen's
	// generated stub, or only logging.
	trivial bool
	// droppedLogs counts logging calls that read callback arguments APEX
	// has no value for; they are not reproduced.
	droppedLogs int
	reason      string
}

var sourceCallbackCallStatement = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*\(.*\)$`)

// sourceCallbackLogging are the logging calls a callback may make with
// arguments APEX has no value for; they are dropped instead of blocking the
// callback.
var sourceCallbackLogging = map[string]bool{
	"lr_output_message": true, "lr_log_message": true, "lr_message": true,
	"lr_debug_message": true, "lr_error_message": true, "lr_vuser_status_message": true,
}

// translateSourceCallback finds a callback in the archive's C files and
// translates its body through the C pipeline. arguments maps an argument
// position to the C expression that stands for it in APEX (for example the
// received message). A body made of plain calls that all convert to
// executable steps becomes the callback's steps; logging that reads other
// arguments is dropped. Anything else (C control flow or variables, reading
// an argument APEX has no value for, returning an abort code) does not run,
// and reason says why.
func translateSourceCallback(files map[string]string, attribute, name, branch string, arguments map[int]string) sourceCallbackTranslation {
	translation := sourceCallbackTranslation{attribute: attribute, name: name, branch: branch}
	file, params, body, line, found := locateSourceCallback(files, name)
	if !found {
		translation.reason = "its definition was not found in the archive"
		return translation
	}
	translation.steps, translation.trivial, translation.droppedLogs, translation.reason = translateSourceCallbackBody(file, params, body, line, arguments)
	if translation.reason == "" && !translation.trivial && branch == "" {
		translation.reason = "APEX has no " + attribute + " callback point"
	}
	if translation.reason != "" {
		translation.steps = nil
	}
	return translation
}

// locateSourceCallback finds a C function definition in the archive's .c
// and .h files.
func locateSourceCallback(files map[string]string, name string) (file string, params []string, body string, line int, found bool) {
	header := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*\(([^()]*)\)\s*\{`)
	names := make([]string, 0, len(files))
	for candidate := range files {
		lower := strings.ToLower(candidate)
		if strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".h") {
			names = append(names, candidate)
		}
	}
	sort.Strings(names)
	for _, candidate := range names {
		code := blankCComments(files[candidate])
		for _, match := range header.FindAllStringSubmatchIndex(code, -1) {
			if cBraceDepthAt(code, match[0]) != 0 {
				continue
			}
			end := cMatchingBrace(code, match[1]-1)
			if end < 0 {
				continue
			}
			for _, param := range strings.Split(code[match[2]:match[3]], ",") {
				tokens := cIdentifierTokenRe.FindAllString(param, -1)
				if len(tokens) > 0 && tokens[len(tokens)-1] != "void" {
					params = append(params, tokens[len(tokens)-1])
				}
			}
			return candidate, params, code[match[1]:end], 1 + strings.Count(code[:match[1]], "\n"), true
		}
	}
	return "", nil, "", 0, false
}

// translateSourceCallbackBody converts a callback body, reporting whether it
// does nothing APEX reproduces or why it cannot run.
func translateSourceCallbackBody(file string, params []string, body string, line int, arguments map[int]string) ([]dsl.Action, bool, int, string) {
	statements, reason := splitSourceCallbackStatements(body)
	if reason != "" {
		return nil, false, 0, reason
	}
	substitutions := map[string]string{}
	for index, param := range params {
		if expression, ok := arguments[index]; ok {
			substitutions[param] = expression
		}
	}
	var calls []string
	dropped := 0
	for _, statement := range statements {
		switch {
		case statement == "return WEB_ASYNC_CB_RC_OK" || statement == "return 0" || statement == "return":
			continue
		case strings.HasPrefix(statement, "return ") || strings.HasPrefix(statement, "return("):
			return nil, false, 0, "it returns " + strings.TrimSpace(strings.TrimPrefix(statement, "return")) + ", which decides the conversation in C"
		case !sourceCallbackCallStatement.MatchString(statement):
			return nil, false, 0, "it uses C variables or expressions"
		}
		call := replaceCIdentifiers(statement, substitutions)
		if unmapped := firstCIdentifier(call, params); unmapped != "" {
			callee := strings.TrimSpace(call[:strings.Index(call, "(")])
			if sourceCallbackLogging[callee] {
				dropped++
				continue
			}
			return nil, false, 0, "it reads the callback argument " + unmapped
		}
		calls = append(calls, call)
	}
	if len(calls) == 0 {
		return nil, true, dropped, ""
	}
	source := "SourceCallback()\n{\n" + strings.Repeat("\n", max(0, line-2)) + strings.Join(calls, ";\n") + ";\n}\n"
	parsed, err := ParseActionFile(file, source)
	if err != nil || len(parsed.Functions) != len(calls) {
		return nil, false, 0, "its calls could not be parsed"
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil || result.Script == nil {
		return nil, false, 0, "its calls could not be converted"
	}
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.SupportMode != string(SourceFunctionSupportExecutable) && mapping.SupportMode != string(SourceFunctionSupportNativeNoop) {
			return nil, false, 0, mapping.Function + " in it does not convert to an executable step"
		}
	}
	steps := append(append([]dsl.Action(nil), result.Script.Init...), result.Script.Actions...)
	if len(steps) == 0 || len(steps) > dsl.MaxHTTPAsyncCallbackSteps {
		return nil, false, 0, "it does not convert to between 1 and 64 steps"
	}
	return steps, false, dropped, ""
}

// splitSourceCallbackStatements splits a callback body into its top-level
// statements; a nested block (if, loop) is C control flow the callback
// translation does not take on.
func splitSourceCallbackStatements(body string) ([]string, string) {
	var statements []string
	var current strings.Builder
	inString, escaped := false, false
	var quote byte
	depth := 0
	for index := 0; index < len(body); index++ {
		ch := body[index]
		switch {
		case inString && escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case inString && ch == quote:
			inString = false
		case !inString && (ch == '"' || ch == '\''):
			inString, quote = true, ch
		case !inString && (ch == '{' || ch == '}'):
			return nil, "it uses C control flow"
		case !inString && ch == '(':
			depth++
		case !inString && ch == ')':
			depth--
		case !inString && ch == ';' && depth == 0:
			if statement := strings.Join(strings.Fields(current.String()), " "); statement != "" {
				statements = append(statements, statement)
			}
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	if strings.TrimSpace(current.String()) != "" {
		return nil, "its body ends without a semicolon"
	}
	return statements, ""
}

// scanCIdentifiers calls visit for each identifier outside string and
// character literals, with its byte range.
func scanCIdentifiers(code string, visit func(name string, start, end int)) {
	inString, escaped := false, false
	var quote byte
	for index := 0; index < len(code); index++ {
		ch := code[index]
		switch {
		case inString && escaped:
			escaped = false
		case inString && ch == '\\':
			escaped = true
		case inString && ch == quote:
			inString = false
		case inString:
		case ch == '"' || ch == '\'':
			inString, quote = true, ch
		case ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z':
			start := index
			for index+1 < len(code) && (code[index+1] == '_' || code[index+1] >= 'A' && code[index+1] <= 'Z' || code[index+1] >= 'a' && code[index+1] <= 'z' || code[index+1] >= '0' && code[index+1] <= '9') {
				index++
			}
			visit(code[start:index+1], start, index+1)
		case ch >= '0' && ch <= '9':
			for index+1 < len(code) && (code[index+1] == '_' || code[index+1] == '.' || code[index+1] >= 'A' && code[index+1] <= 'Z' || code[index+1] >= 'a' && code[index+1] <= 'z' || code[index+1] >= '0' && code[index+1] <= '9') {
				index++
			}
		}
	}
}

// replaceCIdentifiers replaces identifiers outside literals.
func replaceCIdentifiers(code string, substitutions map[string]string) string {
	if len(substitutions) == 0 {
		return code
	}
	var out strings.Builder
	last := 0
	scanCIdentifiers(code, func(name string, start, end int) {
		if replacement, ok := substitutions[name]; ok {
			out.WriteString(code[last:start])
			out.WriteString(replacement)
			last = end
		}
	})
	out.WriteString(code[last:])
	return out.String()
}

// firstCIdentifier returns the first of names used outside literals.
func firstCIdentifier(code string, names []string) string {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	found := ""
	scanCIdentifiers(code, func(name string, _, _ int) {
		if found == "" && wanted[name] {
			found = name
		}
	})
	return found
}

// droppedCallbackLogsWarnings reports callback logging that is not
// reproduced.
func droppedCallbackLogsWarnings(fn ParsedFunction, callbacks []sourceCallbackTranslation) []string {
	var warnings []string
	for _, callback := range callbacks {
		if callback.droppedLogs > 0 {
			warnings = append(warnings, fmt.Sprintf("%s:%d %s %s: %d logging call(s) that read callback arguments are not reproduced", fn.Filename, fn.LineNumber, callback.attribute, callback.name, callback.droppedLogs))
		}
	}
	return warnings
}
