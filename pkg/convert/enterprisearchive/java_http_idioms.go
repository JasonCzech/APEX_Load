package enterprisearchive

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	javaWebCallRe             = regexp.MustCompile(`(?i)\bweb\.([A-Za-z_][A-Za-z0-9_]*)\s*\((.*)\)\s*;?\s*$`)
	javaHTTPBuilderURIRe      = regexp.MustCompile(`(?is)\.uri\s*\(\s*(?:URI\.create\s*\(\s*)?("(?:(?:\\.)|[^"\\])*")`)
	javaHTTPBodyPublisherRe   = regexp.MustCompile(`(?is)\bofString\s*\(\s*("(?:(?:\\.)|[^"\\])*")`)
	javaHTTPContentTypeHintRe = regexp.MustCompile(`(?is)\.header\s*\(\s*"Content-Type"\s*,\s*("(?:(?:\\.)|[^"\\])*")`)
	javaApacheRequestAssignRe = regexp.MustCompile(`(?is)^\s*(?:(?:final\s+)?[A-Za-z_$][A-Za-z0-9_$.<>\[\]]*\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*new\s+(?:[A-Za-z_$][A-Za-z0-9_$.]*\.)?Http(Get|Post|Put|Delete|Patch|Head|Options)\s*\(\s*("(?:(?:\\.)|[^"\\])*")`)
	javaApacheSetHeaderRe     = regexp.MustCompile(`(?is)^\s*([A-Za-z_][A-Za-z0-9_]*)\.setHeader\s*\(\s*("(?:(?:\\.)|[^"\\])*")\s*,\s*("(?:(?:\\.)|[^"\\])*")`)
	javaApacheSetEntityRe     = regexp.MustCompile(`(?is)^\s*([A-Za-z_][A-Za-z0-9_]*)\.setEntity\s*\(.*?\bStringEntity\s*\(\s*("(?:(?:\\.)|[^"\\])*")`)
	javaApacheExecuteRe       = regexp.MustCompile(`(?is)\.execute\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\b`)
	javaURLAssignRe           = regexp.MustCompile(`(?is)^\s*(?:(?:final\s+)?(?:java\.net\.)?URL\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*new\s+(?:java\.net\.)?URL\s*\(\s*("(?:(?:\\.)|[^"\\])*")`)
	javaURLOpenVarRe          = regexp.MustCompile(`(?is)^\s*(?:(?:final\s+)?[A-Za-z_$][A-Za-z0-9_$.<>\[\]]*\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:\([^)]*HttpURLConnection[^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\.openConnection\s*\(`)
	javaURLOpenInlineRe       = regexp.MustCompile(`(?is)^\s*(?:(?:final\s+)?[A-Za-z_$][A-Za-z0-9_$.<>\[\]]*\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:\([^)]*HttpURLConnection[^)]*\)\s*)?new\s+(?:java\.net\.)?URL\s*\(\s*("(?:(?:\\.)|[^"\\])*")\s*\)\.openConnection\s*\(`)
	javaURLSetMethodRe        = regexp.MustCompile(`(?is)^\s*([A-Za-z_][A-Za-z0-9_]*)\.setRequestMethod\s*\(\s*("(?:(?:\\.)|[^"\\])*")`)
	javaURLSetHeaderRe        = regexp.MustCompile(`(?is)^\s*([A-Za-z_][A-Za-z0-9_]*)\.(?:set|add)RequestProperty\s*\(\s*("(?:(?:\\.)|[^"\\])*")\s*,\s*("(?:(?:\\.)|[^"\\])*")`)
	javaURLOutputStreamRe     = regexp.MustCompile(`(?is)^\s*(?:(?:final\s+)?[A-Za-z_$][A-Za-z0-9_$.<>\[\]]*\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\.getOutputStream\s*\(`)
	javaURLStreamWriteRe      = regexp.MustCompile(`(?is)^\s*([A-Za-z_][A-Za-z0-9_]*)\.write\s*\(\s*("(?:(?:\\.)|[^"\\])*")`)
	javaURLDirectWriteRe      = regexp.MustCompile(`(?is)^\s*([A-Za-z_][A-Za-z0-9_]*)\.getOutputStream\s*\(\s*\)\.write\s*\(\s*("(?:(?:\\.)|[^"\\])*")`)
	javaURLSendRe             = regexp.MustCompile(`(?is)([A-Za-z_][A-Za-z0-9_]*)\.(?:getResponseCode|getInputStream)\s*\(`)
)

func javaHTTPParsedActions(program javaProgram) []*ParsedAction {
	apache := newJavaApacheHTTPState()
	urlConnection := newJavaURLConnectionHTTPState()
	return javaParsedActionsFor(program, func(statement javaStatement, phase string) (ParsedFunction, bool) {
		if fn, ok := javaWebToParsedFunction(statement, phase); ok {
			return fn, true
		}
		if fn, ok := apache.mapStatement(statement, phase); ok {
			return fn, true
		}
		if fn, ok := urlConnection.mapStatement(statement, phase); ok {
			return fn, true
		}
		if fn, ok := javaHTTPClientToParsedFunction(statement, phase); ok {
			return fn, true
		}
		if fn, ok := javaLRToParsedFunction(statement, phase); ok {
			if javaLRFunctionAllowedInExecutableLane(fn.Name) {
				return fn, true
			}
		}
		return ParsedFunction{}, false
	})
}

type javaApacheHTTPState struct {
	requests map[string]ParsedFunction
}

func newJavaApacheHTTPState() *javaApacheHTTPState {
	return &javaApacheHTTPState{requests: map[string]ParsedFunction{}}
}

func (state *javaApacheHTTPState) mapStatement(statement javaStatement, phase string) (ParsedFunction, bool) {
	text := javaNormalizeStatementText(statement.Text)
	if matches := javaApacheRequestAssignRe.FindStringSubmatch(text); len(matches) == 4 {
		url, _ := parseJavaStringLiteral(matches[3])
		if isAbsoluteHTTPURL(url) {
			state.requests[matches[1]] = javaHTTPClientParsedFunction(statement, phase, url, strings.ToUpper(matches[2]))
		}
		return ParsedFunction{}, false
	}
	if matches := javaApacheSetHeaderRe.FindStringSubmatch(text); len(matches) == 4 {
		if fn, ok := state.requests[matches[1]]; ok {
			key, _ := parseJavaStringLiteral(matches[2])
			value, _ := parseJavaStringLiteral(matches[3])
			if key != "" {
				fn.Headers[key] = value
				state.requests[matches[1]] = fn
			}
		}
		return ParsedFunction{}, false
	}
	if matches := javaApacheSetEntityRe.FindStringSubmatch(text); len(matches) == 3 {
		if fn, ok := state.requests[matches[1]]; ok {
			body, _ := parseJavaStringLiteral(matches[2])
			fn.Body = body
			fn.Args["Body"] = body
			state.requests[matches[1]] = fn
		}
		return ParsedFunction{}, false
	}
	if matches := javaApacheExecuteRe.FindStringSubmatch(text); len(matches) == 2 {
		if fn, ok := state.requests[matches[1]]; ok {
			fn.Filename = statement.File
			fn.LineNumber = statement.StartLine
			fn.EndLine = statement.EndLine
			fn.RawBlock = statement.Text
			return fn, true
		}
	}
	return ParsedFunction{}, false
}

type javaURLConnectionHTTPState struct {
	urls     map[string]string
	requests map[string]ParsedFunction
	streams  map[string]string
	emitted  map[string]bool
}

func newJavaURLConnectionHTTPState() *javaURLConnectionHTTPState {
	return &javaURLConnectionHTTPState{
		urls:     map[string]string{},
		requests: map[string]ParsedFunction{},
		streams:  map[string]string{},
		emitted:  map[string]bool{},
	}
}

func (state *javaURLConnectionHTTPState) mapStatement(statement javaStatement, phase string) (ParsedFunction, bool) {
	text := javaNormalizeStatementText(statement.Text)
	if matches := javaURLAssignRe.FindStringSubmatch(text); len(matches) == 3 {
		if url, ok := parseJavaStringLiteral(matches[2]); ok && isAbsoluteHTTPURL(url) {
			state.urls[matches[1]] = url
		}
		return ParsedFunction{}, false
	}
	if matches := javaURLOpenInlineRe.FindStringSubmatch(text); len(matches) == 3 {
		if url, ok := parseJavaStringLiteral(matches[2]); ok && isAbsoluteHTTPURL(url) {
			state.requests[matches[1]] = javaHTTPClientParsedFunction(statement, phase, url, "GET")
		}
		return ParsedFunction{}, false
	}
	if matches := javaURLOpenVarRe.FindStringSubmatch(text); len(matches) == 3 {
		if url := state.urls[matches[2]]; isAbsoluteHTTPURL(url) {
			state.requests[matches[1]] = javaHTTPClientParsedFunction(statement, phase, url, "GET")
		}
		return ParsedFunction{}, false
	}
	if matches := javaURLSetMethodRe.FindStringSubmatch(text); len(matches) == 3 {
		if fn, ok := state.requests[matches[1]]; ok {
			if method, ok := parseJavaStringLiteral(matches[2]); ok {
				fn.Method = strings.ToUpper(method)
				state.requests[matches[1]] = fn
			}
		}
		return ParsedFunction{}, false
	}
	if matches := javaURLSetHeaderRe.FindStringSubmatch(text); len(matches) == 4 {
		if fn, ok := state.requests[matches[1]]; ok {
			key, _ := parseJavaStringLiteral(matches[2])
			value, _ := parseJavaStringLiteral(matches[3])
			if key != "" {
				fn.Headers[key] = value
				state.requests[matches[1]] = fn
			}
		}
		return ParsedFunction{}, false
	}
	if matches := javaURLOutputStreamRe.FindStringSubmatch(text); len(matches) == 3 {
		if _, ok := state.requests[matches[2]]; ok {
			state.streams[matches[1]] = matches[2]
		}
		return ParsedFunction{}, false
	}
	if matches := javaURLStreamWriteRe.FindStringSubmatch(text); len(matches) == 3 {
		if requestVar := state.streams[matches[1]]; requestVar != "" {
			state.setBody(requestVar, matches[2])
		}
		return ParsedFunction{}, false
	}
	if matches := javaURLDirectWriteRe.FindStringSubmatch(text); len(matches) == 3 {
		state.setBody(matches[1], matches[2])
		return ParsedFunction{}, false
	}
	if matches := javaURLSendRe.FindStringSubmatch(text); len(matches) == 2 {
		requestVar := matches[1]
		if state.emitted[requestVar] {
			return ParsedFunction{}, false
		}
		if fn, ok := state.requests[requestVar]; ok {
			state.emitted[requestVar] = true
			fn.Filename = statement.File
			fn.LineNumber = statement.StartLine
			fn.EndLine = statement.EndLine
			fn.RawBlock = statement.Text
			return fn, true
		}
	}
	return ParsedFunction{}, false
}

func (state *javaURLConnectionHTTPState) setBody(requestVar string, rawLiteral string) {
	fn, ok := state.requests[requestVar]
	if !ok {
		return
	}
	body, _ := parseJavaStringLiteral(rawLiteral)
	fn.Body = body
	fn.Args["Body"] = body
	state.requests[requestVar] = fn
}

func javaURLConnectionStatementLooksOwned(text string) bool {
	normalized := javaNormalizeStatementText(text)
	return javaURLAssignRe.MatchString(normalized) ||
		javaURLOpenInlineRe.MatchString(normalized) ||
		javaURLOpenVarRe.MatchString(normalized) ||
		javaURLSetMethodRe.MatchString(normalized) ||
		javaURLSetHeaderRe.MatchString(normalized) ||
		javaURLOutputStreamRe.MatchString(normalized) ||
		javaURLStreamWriteRe.MatchString(normalized) ||
		javaURLDirectWriteRe.MatchString(normalized) ||
		javaURLSendRe.MatchString(normalized)
}

func javaWebToParsedFunction(statement javaStatement, phase string) (ParsedFunction, bool) {
	matches := javaWebCallRe.FindStringSubmatch(javaNormalizeStatementText(statement.Text))
	if len(matches) != 3 {
		return ParsedFunction{}, false
	}
	name := "web_" + javaLRFunctionName(matches[1])
	switch name {
	case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_rest", "web_add_header",
		"web_add_auto_header", "web_cleanup_auto_headers", "web_remove_auto_header", "web_revert_auto_header",
		"web_reg_save_param", "web_reg_save_param_ex", "web_reg_save_param_json", "web_reg_save_param_regexp",
		"web_reg_save_param_xpath", "web_reg_find", "web_add_cookie", "web_add_cookie_ex", "web_reg_add_cookie",
		"web_cleanup_cookies", "web_cache_cleanup", "web_set_timeout", "web_set_sockets_option", "web_set_option":
	default:
		return ParsedFunction{}, false
	}
	fn := javaParsedFunctionBase(name, statement, phase, matches[2])
	inner := strings.Join(fn.RawArgs, ", ")
	switch name {
	case "web_url":
		parseWebURL(&fn, inner)
	case "web_submit_data", "web_submit_form":
		parseWebSubmitData(&fn, inner)
	case "web_custom_request":
		parseWebCustomRequest(&fn, inner)
	case "web_rest":
		parseWebREST(&fn, inner)
	case "web_add_header", "web_add_auto_header", "web_remove_auto_header", "web_revert_auto_header":
		parseWebAddHeader(&fn, inner)
	case "web_reg_save_param", "web_reg_save_param_ex", "web_reg_save_param_json", "web_reg_save_param_regexp", "web_reg_save_param_xpath":
		parseWebRegSaveParam(&fn, inner)
	case "web_reg_find":
		parseWebRegFind(&fn, inner)
	case "web_add_cookie", "web_add_cookie_ex", "web_reg_add_cookie":
		parseWebAddCookie(&fn, inner)
	default:
		extractNamedArgs(&fn, inner)
	}
	return fn, true
}

func javaHTTPClientToParsedFunction(statement javaStatement, phase string) (ParsedFunction, bool) {
	text := javaNormalizeStatementText(statement.Text)
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "http") {
		return ParsedFunction{}, false
	}
	if url := javaHTTPBuilderURL(text); url != "" {
		fn := javaHTTPClientParsedFunction(statement, phase, url, javaHTTPMethodFromText(text))
		if body := javaHTTPBodyPublisherString(text); body != "" {
			fn.Body = body
			fn.Args["Body"] = body
		}
		if contentType := javaHTTPContentTypeHint(text); contentType != "" {
			fn.Headers["Content-Type"] = contentType
		}
		return fn, true
	}
	if !strings.Contains(lower, ".send(") && !strings.Contains(lower, ".execute(") && !strings.Contains(lower, ".openconnection(") {
		return ParsedFunction{}, false
	}
	if url := firstQuotedURL(text); url != "" {
		return javaHTTPClientParsedFunction(statement, phase, url, javaHTTPMethodFromText(text)), true
	}
	return ParsedFunction{}, false
}

func javaHTTPClientParsedFunction(statement javaStatement, phase string, url string, method string) ParsedFunction {
	if method == "" {
		method = "GET"
	}
	return ParsedFunction{
		Name:       "web_custom_request",
		StepName:   "Java HTTP Client",
		Phase:      phase,
		Filename:   statement.File,
		LineNumber: statement.StartLine,
		EndLine:    statement.EndLine,
		RawBlock:   statement.Text,
		Args:       map[string]string{},
		Headers:    map[string]string{},
		URL:        url,
		Method:     method,
	}
}

func javaParsedFunctionBase(name string, statement javaStatement, phase string, args string) ParsedFunction {
	fn := ParsedFunction{
		Name:       name,
		Phase:      phase,
		Filename:   statement.File,
		LineNumber: statement.StartLine,
		EndLine:    statement.EndLine,
		RawBlock:   statement.Text,
		Args:       map[string]string{},
		Headers:    map[string]string{},
	}
	rawArgs := splitJavaArgs(args)
	for _, raw := range rawArgs {
		arg := javaParsedArgument(raw)
		fn.CallArgs = append(fn.CallArgs, arg)
		switch arg.Kind {
		case "string", "key_value":
			fn.RawArgs = append(fn.RawArgs, arg.Raw)
		default:
			fn.RawArgs = append(fn.RawArgs, strings.TrimSpace(raw))
		}
	}
	return fn
}

func javaParsedArgument(raw string) ParsedArgument {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "web.")
	if value, ok := parseJavaStringLiteral(trimmed); ok {
		arg := ParsedArgument{Raw: `"` + value + `"`, Value: value, Kind: "string"}
		if key, val, found := strings.Cut(value, "="); found {
			arg.Kind = "key_value"
			arg.Name = key
			arg.Value = val
		}
		return arg
	}
	upper := strings.ToUpper(strings.TrimPrefix(trimmed, "web."))
	switch upper {
	case "LAST", "ITEMDATA", "ENDITEM", "HEADERS", "ENDHEADER":
		return ParsedArgument{Raw: upper, Value: upper, Kind: "macro"}
	}
	if strings.Contains(trimmed, "(") {
		return ParsedArgument{Raw: trimmed, Value: trimmed, Kind: "nested_call"}
	}
	return ParsedArgument{Raw: trimmed, Value: trimmed, Kind: "identifier"}
}

func javaParsedActionsFor(program javaProgram, convert func(javaStatement, string) (ParsedFunction, bool)) []*ParsedAction {
	var actions []*ParsedAction
	for _, phase := range []string{"init", "action", "teardown"} {
		functions := make([]ParsedFunction, 0)
		for _, statement := range javaLifecycleStatements(program, phase) {
			functions = append(functions, collectJavaParsedFunctions(statement, phase, convert)...)
		}
		if len(functions) > 0 {
			actions = append(actions, &ParsedAction{
				Filename:  fmt.Sprintf("java_%s", phase),
				Phase:     phase,
				Functions: functions,
			})
		}
	}
	return actions
}

func collectJavaParsedFunctions(statement javaStatement, phase string, convert func(javaStatement, string) (ParsedFunction, bool)) []ParsedFunction {
	if statement.Kind == "block" {
		var out []ParsedFunction
		for _, child := range statement.Children {
			out = append(out, collectJavaParsedFunctions(child, phase, convert)...)
		}
		return out
	}
	if fn, ok := convert(statement, phase); ok {
		return []ParsedFunction{fn}
	}
	return nil
}

func firstQuotedURL(text string) string {
	for _, part := range splitJavaArgs(text) {
		if value, ok := parseJavaStringLiteral(part); ok {
			lower := strings.ToLower(value)
			if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
				return value
			}
		}
	}
	start := strings.Index(text, `"http`)
	if start < 0 {
		return ""
	}
	end := strings.Index(text[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return text[start+1 : start+1+end]
}

func javaHTTPMethodFromText(text string) string {
	upper := strings.ToUpper(text)
	for _, method := range []string{"DELETE", "PATCH", "POST", "PUT", "GET"} {
		if strings.Contains(upper, `"`+method+`"`) || strings.Contains(upper, "."+method+"(") {
			return method
		}
	}
	return "GET"
}

func javaHTTPBuilderURL(text string) string {
	match := javaHTTPBuilderURIRe.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	if value, ok := parseJavaStringLiteral(match[1]); ok && isAbsoluteHTTPURL(value) {
		return value
	}
	return ""
}

func javaHTTPBodyPublisherString(text string) string {
	match := javaHTTPBodyPublisherRe.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	value, _ := parseJavaStringLiteral(match[1])
	return value
}

func javaHTTPContentTypeHint(text string) string {
	match := javaHTTPContentTypeHintRe.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	value, _ := parseJavaStringLiteral(match[1])
	return value
}

func javaEnsureHeaderLists(script *dsl.Script) {
	if script == nil {
		return
	}
	var walk func([]dsl.Action)
	walk = func(actions []dsl.Action) {
		for i := range actions {
			if len(actions[i].HeaderList) == 0 && len(actions[i].Headers) > 0 {
				actions[i].HeaderList = dsl.HeaderListFromMap(actions[i].Headers)
			}
			if actions[i].Control != nil {
				walk(actions[i].Control.Steps)
				walk(actions[i].Control.Else)
			}
		}
	}
	walk(script.Init)
	walk(script.Actions)
	walk(script.Teardown)
}
