package enterprisearchive

import (
	"strings"
)

func javaSOAPParsedActions(program javaProgram, files map[string]string) []*ParsedAction {
	actions := make([]*ParsedAction, 0)
	for _, phase := range []string{"init", "action", "teardown"} {
		statements := javaLifecycleStatements(program, phase)
		if len(statements) == 0 {
			continue
		}
		env := newJavaStringEnv()
		functions := make([]ParsedFunction, 0)
		for _, statement := range statements {
			functions = append(functions, collectJavaParsedFunctions(statement, phase, func(statement javaStatement, phase string) (ParsedFunction, bool) {
				text := javaNormalizeStatementText(statement.Text)
				if env.assignString(text, files) {
					return ParsedFunction{}, false
				}
				return javaSOAPCallToParsedFunction(env, files, statement, phase)
			})...)
		}
		if len(functions) == 0 {
			continue
		}
		actions = append(actions, &ParsedAction{
			Filename:  functions[0].Filename,
			Phase:     phase,
			Functions: functions,
		})
	}
	return actions
}

func javaSOAPCallToParsedFunction(env javaStringEnv, files map[string]string, statement javaStatement, phase string) (ParsedFunction, bool) {
	text := strings.TrimSpace(statement.Text)
	if !strings.Contains(strings.ToLower(text), ".call(") {
		return ParsedFunction{}, false
	}
	// Require the .call(...) receiver to look like a SOAP connection, so a generic
	// httpClient.call(...) / someRpc.call(...) inside a SOAP-importing program is
	// not misread as a SOAP send.
	if r := strings.ToLower(javaCallReceiver(text)); !strings.Contains(r, "soap") && !strings.Contains(r, "conn") {
		return ParsedFunction{}, false
	}
	open := strings.Index(text, "(")
	close := strings.LastIndex(text, ")")
	if open < 0 || close <= open {
		return ParsedFunction{}, false
	}
	args := splitJavaArgs(text[open+1 : close])
	if len(args) < 2 {
		return ParsedFunction{}, false
	}
	endpoint, ok := env.eval(args[1], files)
	if !ok || !isAbsoluteHTTPURL(endpoint) {
		return ParsedFunction{}, false
	}
	body := ""
	if value, ok := env.eval(args[0], files); ok && strings.Contains(value, "<") {
		body = value
	}
	if body == "" {
		body = javaBestSOAPEnvelope(env)
	}
	if !strings.Contains(strings.ToLower(body), "<") {
		return ParsedFunction{}, false
	}

	fn := javaParsedFunctionBase("web_custom_request", statement, phase, "")
	fn.StepName = convertutilFirstNonEmptySOAPName(statement)
	fn.JavaOrigin = true
	fn.Args["URL"] = endpoint
	fn.Args["Method"] = "POST"
	fn.Args["Body"] = body
	fn.URL = endpoint
	fn.Method = "POST"
	// Body is already Java-decoded + parameter-converted by env.eval; leave it for
	// convertAction to template once (JavaOrigin skips the C-string unescaper).
	fn.Body = body
	fn.Headers["Content-Type"] = "text/xml"
	return fn, true
}

// javaCallReceiver returns the identifier immediately before the ".call(" token.
func javaCallReceiver(text string) string {
	idx := strings.Index(strings.ToLower(text), ".call(")
	if idx <= 0 {
		return ""
	}
	prefix := text[:idx]
	i := len(prefix)
	for i > 0 {
		c := prefix[i-1]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i--
			continue
		}
		break
	}
	return prefix[i:]
}

func javaBestSOAPEnvelope(env javaStringEnv) string {
	for name, value := range env.Vars {
		lowerName := strings.ToLower(name)
		lowerValue := strings.ToLower(value)
		if strings.Contains(value, "<") && (strings.Contains(lowerName, "soap") || strings.Contains(lowerName, "envelope") || strings.Contains(lowerValue, "envelope")) {
			return value
		}
	}
	return ""
}

func convertutilFirstNonEmptySOAPName(statement javaStatement) string {
	name := statementName(statement)
	if strings.TrimSpace(name) == "" {
		return "SOAP Request"
	}
	return name
}
