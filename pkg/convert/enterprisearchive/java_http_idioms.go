package enterprisearchive

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var javaWebCallRe = regexp.MustCompile(`(?i)\bweb\.([A-Za-z_][A-Za-z0-9_]*)\s*\((.*)\)\s*;?\s*$`)

func javaHTTPParsedActions(program javaProgram) []*ParsedAction {
	return javaParsedActionsFor(program, func(statement javaStatement, phase string) (ParsedFunction, bool) {
		if fn, ok := javaWebToParsedFunction(statement, phase); ok {
			return fn, true
		}
		if fn, ok := javaHTTPClientToParsedFunction(statement, phase); ok {
			return fn, true
		}
		if fn, ok := javaLRToParsedFunction(statement, phase); ok {
			switch fn.Name {
			case "lr_start_transaction", "lr_end_transaction", "lr_start_sub_transaction", "lr_end_sub_transaction", "lr_think_time",
				"lr_error_message", "lr_message", "lr_log_message", "lr_output_message", "lr_rendezvous", "lr_rendezvous_ex":
				return fn, true
			}
		}
		return ParsedFunction{}, false
	})
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
	if !strings.Contains(lower, ".send(") && !strings.Contains(lower, ".execute(") && !strings.Contains(lower, ".openconnection(") {
		return ParsedFunction{}, false
	}
	fn := ParsedFunction{
		Name:       "java_http_client",
		StepName:   "Java HTTP Client",
		Phase:      phase,
		Filename:   statement.File,
		LineNumber: statement.StartLine,
		EndLine:    statement.EndLine,
		RawBlock:   statement.Text,
		Args:       map[string]string{},
		Headers:    map[string]string{},
		Method:     "GET",
	}
	if url := firstQuotedURL(text); url != "" {
		fn.Name = "web_custom_request"
		fn.StepName = "Java HTTP Client"
		fn.URL = url
		fn.Method = javaHTTPMethodFromText(text)
		return fn, true
	}
	return ParsedFunction{}, false
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
