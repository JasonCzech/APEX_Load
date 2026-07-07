package enterprisearchive

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// ParsedFunction represents a single C/VUser source function call extracted from a script.
type ParsedFunction struct {
	Name         string            // e.g. "web_url", "web_submit_data", "lr_think_time"
	StepName     string            // The first string argument (step name)
	Args         map[string]string // Named key-value arguments extracted from ITEMDATA/LAST pairs
	RawArgs      []string          // Raw string arguments in order
	CallArgs     []ParsedArgument  // Structured top-level C call arguments in order
	Headers      map[string]string // Extracted header key-value pairs
	HeaderList   dsl.HeaderList    // Ordered header occurrences when the source preserves them
	Body         string            // Request body if present
	Method       string            // HTTP method (for web_custom_request)
	URL          string            // Target URL
	Action       string            // Action/target URL for form submissions
	Cookies      []ParsedCookie    // Cookies added through web_add_cookie
	Phase        string            // init, action, or teardown
	LineNumber   int               // Source line number for diagnostics
	Filename     string            // Source file containing the function
	EndLine      int               // Source line where the function call ends
	RawBlock     string            // Original source block for source review
	JavaOrigin   bool              // Body was decoded from Java source; skip C-string unescaping
	Conditions   []string          // Enclosing if/else conditions (raw C, outermost first; else pre-negated as !(...))
	ConditionIDs []string          // Stable per-construct identities (source positions) parallel to Conditions
	Loops        []string          // Enclosing loop headers (raw C, outermost first)
	LoopIDs      []string          // Stable per-construct identities (source positions) parallel to Loops
}

type ParsedCookie struct {
	Name   string
	Value  string
	Domain string
	Path   string
	Raw    string
}

// ParsedArgument represents a single top-level C function call argument.
type ParsedArgument struct {
	Raw        string `json:"raw,omitempty"`
	Value      string `json:"value,omitempty"`
	Kind       string `json:"kind,omitempty"` // string, key_value, number, identifier, macro, nested_call, expression
	Name       string `json:"name,omitempty"`
	NestedCall string `json:"nested_call,omitempty"`
}

// ParsedAction represents a parsed .c action file.
type ParsedAction struct {
	Filename  string
	Phase     string
	Functions []ParsedFunction
}

// SourceActionFile records a lifecycle/action source file declared by the .usr
// project, including language and source phase.
type SourceActionFile struct {
	Name     string `json:"name"`
	Phase    string `json:"phase,omitempty"`
	Language string `json:"language,omitempty"`
}

// ProjectConfig holds parsed .usr file settings.
type ProjectConfig struct {
	ScriptType        string // e.g. "Web"
	ActiveTypes       []string
	ScriptLanguage    string // e.g. "JAVA", "C"
	RunType           string // e.g. "Java-Interpreter"
	ActionFiles       []string
	SourceActionFiles []SourceActionFile
	RuntimeFile       string
	ParamFiles        []string
	ExtraFiles        []string
	BaseURL           string
	ThinkTimeMin      float64
	ThinkTimeMax      float64
}

// regex patterns for enterprise C/VUser source function parsing.
var (
	// Matches C/VUser source calls such as web_url, lrd_stmt, ftp_command, or sapgui_*.
	reFuncCall = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

	// Matches key=value patterns inside ITEMDATA sections
	reKeyValue = regexp.MustCompile(`"([^"]+)=([^"]*)"`)

	// Matches "Name=value" patterns for named arguments
	reNamedArg = regexp.MustCompile(`"(\w+)=(.+?)"`)

	// Matches simple string arguments like "StepName"
	reStringArg = regexp.MustCompile(`"([^"]*)"`)

	// Matches numeric arguments
	reNumArg = regexp.MustCompile(`(\d+)`)

	// URL pattern within arguments
	reURLArg = regexp.MustCompile(`"URL=(.+?)"`)

	// Action pattern
	reActionArg = regexp.MustCompile(`"Action=(.+?)"`)

	// Method pattern
	reMethodArg = regexp.MustCompile(`"Method=(.+?)"`)

	// Body pattern
	reBodyArg = regexp.MustCompile(`"Body=(.+?)"`)

	// RecContentType pattern
	reRecContentType = regexp.MustCompile(`"RecContentType=(.+?)"`)

	// .usr file section header
	reSectionHeader = regexp.MustCompile(`^\[(.+)\]$`)
)

// ParseActionFile parses an enterprise C/VUser action file and extracts source
// function calls. It prefers the full C AST parser for fidelity but falls back
// to the lenient line scanner when the AST path cannot recover any calls.
//
// The strict C frontend (cc.Parse) rejects the entire translation unit on any
// syntax error — an LR-specific macro, a vendor pragma, or a single malformed
// helper line — which would otherwise discard every well-formed web_url /
// web_submit_data call in the file and silently demote the import to
// review-only. The legacy scanner extracts calls block-by-block regardless of
// surrounding C validity, so it recovers the valid steps the AST parser drops.
func ParseActionFile(filename string, content string) (*ParsedAction, error) {
	astParsed, astErr := ParseActionFileAST(filename, content)
	if astErr == nil && len(astParsed.Functions) > 0 {
		return astParsed, nil
	}

	legacyParsed, legacyErr := parseActionFileLegacy(filename, content)
	if legacyErr == nil && len(legacyParsed.Functions) > 0 {
		return legacyParsed, nil
	}

	// Neither parser recovered any calls. Trust a clean AST parse of an
	// otherwise-empty file; otherwise surface the richer AST diagnostic so the
	// caller can flag the archive for review.
	if astErr == nil {
		return astParsed, nil
	}
	return nil, astErr
}

func parseActionFileLegacy(filename string, content string) (*ParsedAction, error) {
	parsed := &ParsedAction{
		Filename:  filename,
		Functions: make([]ParsedFunction, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	var currentBlock strings.Builder
	var rawBlock strings.Builder
	inBlock := false
	inComment := false
	blockStartLine := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		parseLine := stripCComments(line, &inComment)
		trimmed := strings.TrimSpace(parseLine)

		// Skip empty lines, comments
		if !inBlock {
			if trimmed == "" {
				continue
			}
			// Skip standard includes and function declarations
			if strings.HasPrefix(trimmed, "#include") || strings.HasPrefix(trimmed, "return ") ||
				trimmed == "{" || trimmed == "}" ||
				strings.HasPrefix(trimmed, "Action()") || strings.HasPrefix(trimmed, "vuser_init()") ||
				strings.HasPrefix(trimmed, "vuser_end()") || strings.HasPrefix(trimmed, "int ") {
				continue
			}
		}

		// Check if this line starts a recognized C/VUser source function call.
		if !inBlock && reFuncCall.MatchString(trimmed) && !isCControlCall(trimmed) {
			inBlock = true
			blockStartLine = lineNum
			currentBlock.Reset()
			rawBlock.Reset()
		}

		if inBlock {
			if trimmed == "" {
				continue
			}
			currentBlock.WriteString(trimmed)
			currentBlock.WriteString(" ")
			rawBlock.WriteString(line)
			rawBlock.WriteString("\n")

			// Lines are concatenated with separators, so the terminator cannot
			// straddle line boundaries; checking the current line avoids an
			// accumulated-block rescan for large multiline bodies.
			if strings.Contains(trimmed, ");") {
				blockStr := currentBlock.String()
				fn, err := parseFunctionBlock(blockStr, filename, blockStartLine, lineNum, rawBlock.String())
				if err == nil {
					parsed.Functions = append(parsed.Functions, fn)
				}
				inBlock = false
				currentBlock.Reset()
				rawBlock.Reset()
			}
		}
	}

	return parsed, nil
}

func stripCComments(line string, inComment *bool) string {
	var out strings.Builder
	inString := false
	var quote byte
	escaped := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if inComment != nil && *inComment {
			if ch == '*' && i+1 < len(line) && line[i+1] == '/' {
				*inComment = false
				i++
			}
			continue
		}
		if inString {
			out.WriteByte(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				inString = false
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inString = true
			quote = ch
			out.WriteByte(ch)
			continue
		}
		if ch == '/' && i+1 < len(line) {
			switch line[i+1] {
			case '/':
				return out.String()
			case '*':
				if inComment != nil {
					*inComment = true
				}
				i++
				continue
			}
		}
		out.WriteByte(ch)
	}

	return out.String()
}

func isCControlCall(line string) bool {
	match := reFuncCall.FindStringSubmatch(line)
	if len(match) < 2 {
		return false
	}
	switch strings.ToLower(match[1]) {
	case "if", "for", "while", "switch", "return", "sizeof":
		return true
	default:
		return false
	}
}

// parseFunctionBlock parses a complete function call block.
func parseFunctionBlock(block string, filename string, lineNum int, endLine int, rawBlock string) (ParsedFunction, error) {
	fn := ParsedFunction{
		Args:       make(map[string]string),
		Headers:    make(map[string]string),
		LineNumber: lineNum,
		Filename:   filename,
		EndLine:    endLine,
		RawBlock:   strings.TrimRight(rawBlock, "\n"),
	}

	// Extract function name
	idx := strings.Index(block, "(")
	if idx < 0 {
		return fn, fmt.Errorf("no opening parenthesis found")
	}
	fn.Name = strings.TrimSpace(block[:idx])

	// Extract everything between outer parentheses
	inner := block[idx+1:]
	// Find matching closing paren + semicolon
	endIdx := strings.LastIndex(inner, ");")
	if endIdx >= 0 {
		inner = inner[:endIdx]
	}

	fn.CallArgs = parseCFunctionArguments(inner)
	for _, arg := range fn.CallArgs {
		if arg.Kind == "string" || arg.Kind == "key_value" {
			fn.RawArgs = append(fn.RawArgs, arg.Value)
		}
	}

	switch fn.Name {
	case "web_url":
		parseWebURL(&fn, inner)
	case "web_submit_data", "web_submit_form":
		parseWebSubmitData(&fn, inner)
	case "web_custom_request":
		parseWebCustomRequest(&fn, inner)
	case "web_rest":
		parseWebREST(&fn, inner)
	case "web_service_call":
		parseWebServiceCall(&fn, inner)
	case "web_reg_save_param", "web_reg_save_param_json", "web_reg_save_param_xpath", "web_reg_save_param_ex", "web_reg_save_param_attrib":
		parseWebRegSaveParam(&fn, inner)
	case "web_reg_save_param_regexp":
		parseWebRegSaveParam(&fn, inner)
	case "web_reg_find", "web_global_verification", "web_find":
		parseWebRegFind(&fn, inner)
	case "web_link", "web_image", "web_image_check":
		parseWebLinkImage(&fn, inner)
	case "lr_think_time":
		parseLRThinkTime(&fn, inner)
	case "lr_start_transaction", "lr_end_transaction":
		if len(fn.RawArgs) > 0 {
			fn.StepName = fn.RawArgs[0]
		}
	case "lr_continue_on_error", "lr_error_message", "lr_exit",
		"web_cache_cleanup", "web_cleanup_cookies", "web_set_sockets_option", "web_set_timeout",
		"web_convert_param", "web_revert_auto_header", "web_set_user",
		// Web (HTTP/HTML) runtime/correlation controls translated from the published library.
		"web_remove_auto_header", "web_cleanup_auto_headers", "web_remove_cookie", "web_save_header",
		"web_set_option", "web_set_max_retries", "web_disable_keep_alive", "web_enable_keep_alive",
		"web_set_certificate", "web_set_certificate_ex",
		"web_set_proxy", "web_set_secure_proxy", "web_set_proxy_bypass", "web_set_proxy_bypass_local", "web_set_pac",
		"web_add_filter", "web_add_auto_filter", "web_remove_auto_filter", "web_load_cache", "web_dump_cache",
		"web_concurrent_start", "web_concurrent_end",
		"web_convert_date_param", "web_convert_from_formatted", "web_convert_to_formatted", "web_sjis_to_euc_param",
		"web_save_param_length", "web_save_timestamp_param", "web_get_int_property",
		"web_util_set_request_body", "web_util_set_request_url", "web_report_data_point":
		parseSourceRuntimeControl(&fn, inner)
	case "web_add_header", "web_add_auto_header":
		parseWebAddHeader(&fn, inner)
	case "web_add_cookie", "web_add_cookie_ex", "web_reg_add_cookie":
		parseWebAddCookie(&fn, inner)
	default:
		if len(fn.RawArgs) > 0 {
			fn.StepName = fn.RawArgs[0]
		}
	}

	return fn, nil
}

func parseSourceRuntimeControl(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	extractNamedArgs(fn, inner)

	for index, arg := range splitCFunctionArgs(inner) {
		cleaned := cleanCArgument(arg)
		if cleaned == "" {
			continue
		}
		fn.Args[fmt.Sprintf("arg%d", index+1)] = cleaned
		if fn.Args["value"] == "" {
			fn.Args["value"] = cleaned
		}
	}
}

func parseCFunctionArguments(inner string) []ParsedArgument {
	rawArgs := splitCFunctionArgs(inner)
	args := make([]ParsedArgument, 0, len(rawArgs))
	for _, raw := range rawArgs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		arg := ParsedArgument{Raw: trimmed}
		cleaned := cleanConfigValue(trimmed)
		arg.Value = cleaned
		switch {
		case isQuotedCString(trimmed):
			arg.Kind = "string"
			if key, value, ok := splitSourceKeyValue(cleaned); ok {
				arg.Kind = "key_value"
				arg.Name = key
				arg.Value = value
			}
		case strings.EqualFold(cleaned, "LAST") || strings.EqualFold(cleaned, "ITEMDATA") || strings.EqualFold(cleaned, "ENDITEM"):
			arg.Kind = "macro"
		case looksLikeNestedCCall(cleaned):
			arg.Kind = "nested_call"
			arg.NestedCall = cleaned[:strings.Index(cleaned, "(")]
		case isNumericCArgument(cleaned):
			arg.Kind = "number"
		case isIdentifierCArgument(cleaned):
			arg.Kind = "identifier"
		default:
			arg.Kind = "expression"
		}
		args = append(args, arg)
	}
	return args
}

func isQuotedCString(value string) bool {
	if len(value) < 2 {
		return false
	}
	first := value[0]
	last := value[len(value)-1]
	return (first == '"' && last == '"') || (first == '\'' && last == '\'')
}

func splitSourceKeyValue(value string) (string, string, bool) {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.TrimSpace(parts[0])
	if key == "" || strings.ContainsAny(key, " \t\r\n") {
		return "", "", false
	}
	return key, strings.TrimSpace(parts[1]), true
}

func looksLikeNestedCCall(value string) bool {
	open := strings.Index(value, "(")
	close := strings.LastIndex(value, ")")
	if open <= 0 || close <= open {
		return false
	}
	return isIdentifierCArgument(strings.TrimSpace(value[:open]))
}

func isNumericCArgument(value string) bool {
	if value == "" {
		return false
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return true
	}
	return false
}

func isIdentifierCArgument(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func splitCFunctionArgs(inner string) []string {
	args := make([]string, 0)
	var current strings.Builder
	inString := false
	escaped := false
	depth := 0

	for _, r := range inner {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			continue
		case inString && r == '\\':
			current.WriteRune(r)
			escaped = true
			continue
		case r == '"':
			inString = !inString
			current.WriteRune(r)
			continue
		case !inString && (r == '(' || r == '[' || r == '{'):
			depth++
		case !inString && (r == ')' || r == ']' || r == '}') && depth > 0:
			depth--
		case !inString && depth == 0 && r == ',':
			args = append(args, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}

	if strings.TrimSpace(current.String()) != "" {
		args = append(args, current.String())
	}
	return args
}

func cleanCArgument(value string) string {
	cleaned := cleanConfigValue(value)
	if strings.EqualFold(cleaned, "LAST") {
		return ""
	}
	return cleaned
}

func parseWebURL(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	fn.Method = "GET"

	if m := reURLArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	}

	// Extract named args
	extractNamedArgs(fn, inner)
}

// parseWebLinkImage parses the HTML-mode navigation steps web_link/web_image/
// web_image_check. In HTML mode the absolute target is resolved from page context
// at record time, so the static .c usually carries only the element label
// ("Text="/"Alt="). An explicit "URL=" (URL-mode recording) or an image "Src="
// gives a usable target; otherwise fn.URL is left empty and the converter
// preserves the step for review instead of fabricating a request.
func parseWebLinkImage(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	fn.Method = "GET"
	extractNamedArgs(fn, inner)
	if v := fn.Args["URL"]; v != "" {
		fn.URL = v
	} else if v := fn.Args["Src"]; v != "" {
		fn.URL = v
	}
}

func parseWebSubmitData(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	fn.Method = "POST"

	if m := reActionArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	}

	// Collect ITEMDATA fields
	extractNamedArgs(fn, inner)
	extractItemData(fn, inner)
}

func parseWebCustomRequest(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	extractNamedArgs(fn, inner)

	if m := reURLArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	}
	if fn.Args["URL"] != "" {
		fn.URL = fn.Args["URL"]
	}
	if m := reMethodArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Method = m[1]
	}
	if fn.Args["Method"] != "" {
		fn.Method = fn.Args["Method"]
	}
	if m := reBodyArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Body = m[1]
	}
	if fn.Args["Body"] != "" {
		fn.Body = fn.Args["Body"]
	}
	applyMaskedBodyReference(fn)
}

func parseWebREST(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	fn.Method = "GET"
	extractNamedArgs(fn, inner)

	if fn.Args["URL"] != "" {
		fn.URL = fn.Args["URL"]
	} else if m := reURLArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	}
	if fn.Args["Method"] != "" {
		fn.Method = fn.Args["Method"]
	} else if m := reMethodArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Method = m[1]
	}
	if fn.Args["Body"] != "" {
		fn.Body = fn.Args["Body"]
	} else if m := reBodyArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Body = m[1]
	}
	applyMaskedBodyReference(fn)
	extractWebRESTHeaders(fn)
}

func extractWebRESTHeaders(fn *ParsedFunction) {
	inHeaders := false
	currentName := ""
	for _, arg := range fn.CallArgs {
		if sourceArgTokenEquals(arg, "HEADERS") {
			inHeaders = true
			currentName = ""
			continue
		}
		if !inHeaders {
			continue
		}
		if sourceArgTokenEquals(arg, "ENDHEADER") {
			currentName = ""
			continue
		}
		if sourceArgTokenEquals(arg, "LAST") {
			break
		}
		if arg.Kind != "key_value" {
			continue
		}
		switch strings.ToLower(arg.Name) {
		case "name":
			currentName = arg.Value
		case "value":
			if currentName != "" {
				fn.Headers[currentName] = arg.Value
			}
		}
	}
}

func sourceArgTokenEquals(arg ParsedArgument, token string) bool {
	return strings.EqualFold(strings.TrimSpace(arg.Value), token) || strings.EqualFold(strings.TrimSpace(arg.Raw), token)
}

func parseWebServiceCall(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	fn.Method = "POST"
	extractNamedArgs(fn, inner)

	if m := reURLArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	} else if m := reActionArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	}
	if fn.Args["URL"] != "" {
		fn.URL = fn.Args["URL"]
	} else if fn.Args["Action"] != "" {
		fn.URL = fn.Args["Action"]
	}
	if m := reBodyArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Body = m[1]
	}
	if fn.Args["Body"] != "" {
		fn.Body = fn.Args["Body"]
	}

	if soapAction, ok := fn.Args["SOAPAction"]; ok && soapAction != "" {
		fn.Headers["SOAPAction"] = soapAction
	}
}

func parseWebRegSaveParam(fn *ParsedFunction, inner string) {
	// web_reg_save_param("ParamName", "LB=...", "RB=...", ..., LAST)
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	extractNamedArgs(fn, inner)
}

func parseWebRegFind(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	extractNamedArgs(fn, inner)
}

func parseLRThinkTime(fn *ParsedFunction, inner string) {
	fn.StepName = "think_time"
	if m := reNumArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Args["seconds"] = m[1]
	}
}

func parseWebAddHeader(fn *ParsedFunction, inner string) {
	// web_add_header("Header-Name", "Header-Value")
	stringArgs := positionalStringArguments(fn.CallArgs)
	if len(stringArgs) >= 2 {
		fn.StepName = stringArgs[0]
		fn.Headers[stringArgs[0]] = stringArgs[1]
	}
}

func parseWebAddCookie(fn *ParsedFunction, inner string) {
	stringArgs := positionalStringArguments(fn.CallArgs)
	if len(stringArgs) == 0 {
		return
	}
	rawCookie := stringArgs[0]
	fn.StepName = rawCookie
	fn.Cookies = append(fn.Cookies, parseSourceCookie(rawCookie))
	extractNamedArgs(fn, inner)
}

func positionalStringArguments(args []ParsedArgument) []string {
	values := make([]string, 0, len(args))
	for _, arg := range args {
		if arg.Kind == "macro" {
			continue
		}
		if arg.Kind == "string" || arg.Kind == "key_value" {
			values = append(values, cleanConfigValue(arg.Raw))
		}
	}
	return values
}

func parseSourceCookie(raw string) ParsedCookie {
	cookie := ParsedCookie{Raw: raw, Path: "/"}
	segments := strings.Split(raw, ";")
	for index, segment := range segments {
		key, value, found := strings.Cut(strings.TrimSpace(segment), "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch {
		case index == 0:
			cookie.Name = key
			cookie.Value = value
		case strings.EqualFold(key, "domain"):
			cookie.Domain = value
		case strings.EqualFold(key, "path"):
			cookie.Path = value
		}
	}
	return cookie
}

// extractNamedArgs pulls out "Key=Value" patterns from the inner string.
func extractNamedArgs(fn *ParsedFunction, inner string) {
	for _, arg := range fn.CallArgs {
		if arg.Kind == "key_value" && arg.Name != "" {
			fn.Args[arg.Name] = arg.Value
		}
	}
	matches := reNamedArg.FindAllStringSubmatch(inner, -1)
	for _, m := range matches {
		key := m[1]
		value := m[2]
		if _, exists := fn.Args[key]; !exists {
			fn.Args[key] = value
		}
	}
}

// extractItemData attempts to extract ITEMDATA form field pairs.
func extractItemData(fn *ParsedFunction, inner string) {
	formData := make(map[string]string)
	currentName := ""
	inItemData := false
	for _, arg := range fn.CallArgs {
		if arg.Kind == "macro" && strings.EqualFold(arg.Value, "ITEMDATA") {
			inItemData = true
			continue
		}
		if !inItemData {
			continue
		}
		if arg.Kind == "macro" && strings.EqualFold(arg.Value, "ENDITEM") {
			currentName = ""
			continue
		}
		if arg.Kind != "key_value" {
			continue
		}
		switch strings.ToLower(arg.Name) {
		case "name":
			currentName = arg.Value
		case "value":
			if currentName != "" {
				formData[currentName] = arg.Value
			}
		}
	}

	if len(formData) > 0 {
		// Build URL-encoded body
		pairs := make([]string, 0, len(formData))
		for k, v := range formData {
			pairs = append(pairs, k+"="+v)
		}
		fn.Body = strings.Join(pairs, "&")
		fn.Headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
}

// ParseConfigSections parses INI-style section/key=value content into nested maps.
func ParseConfigSections(content string) map[string]map[string]string {
	sections := make(map[string]map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	section := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}

		if m := reSectionHeader.FindStringSubmatch(line); len(m) > 1 {
			section = m[1]
			if _, ok := sections[section]; !ok {
				sections[section] = make(map[string]string)
			}
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		if _, ok := sections[section]; !ok {
			sections[section] = make(map[string]string)
		}
		sections[section][strings.TrimSpace(parts[0])] = cleanConfigValue(parts[1])
	}

	if len(sections[""]) == 0 {
		delete(sections, "")
	}

	return sections
}

// ParseUSRFile parses a source .usr project configuration file.
func ParseUSRFile(content string) (*ProjectConfig, error) {
	cfg := &ProjectConfig{
		ActionFiles:       make([]string, 0),
		SourceActionFiles: make([]SourceActionFile, 0),
		ParamFiles:        make([]string, 0),
		ExtraFiles:        make([]string, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	section := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}

		if m := reSectionHeader.FindStringSubmatch(line); len(m) > 1 {
			section = m[1]
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := cleanConfigValue(parts[1])
		if cfg.RuntimeFile == "" && strings.HasSuffix(strings.ToLower(value), ".cfg") {
			cfg.RuntimeFile = value
		}
		if strings.HasSuffix(strings.ToLower(value), ".prm") {
			cfg.ParamFiles = append(cfg.ParamFiles, value)
		}

		switch section {
		case "General":
			switch key {
			case "Type":
				cfg.ScriptType = value
			case "ActiveTypes", "AdditionalTypes", "GenerateTypes":
				cfg.ActiveTypes = appendUnique(cfg.ActiveTypes, splitConfigList(value)...)
			case "ScriptLanguage":
				cfg.ScriptLanguage = value
			case "RunType":
				cfg.RunType = value
			}
		case "Actions":
			if isSupportedLifecycleSourceFile(value) {
				cfg.SourceActionFiles = append(cfg.SourceActionFiles, SourceActionFile{
					Name:     value,
					Phase:    sourceActionPhaseFromUSRKey(key, value),
					Language: sourceActionLanguage(value),
				})
			}
			if strings.HasSuffix(strings.ToLower(value), ".c") || strings.HasSuffix(strings.ToLower(value), ".java") {
				cfg.ActionFiles = append(cfg.ActionFiles, value)
			}
		case "RunLogicFiles":
			if key == "RunAction" || key == "Actions" {
				for _, f := range strings.Split(value, ",") {
					f = strings.TrimSpace(f)
					if f != "" && isSupportedLifecycleSourceFile(f) {
						cfg.SourceActionFiles = append(cfg.SourceActionFiles, SourceActionFile{
							Name:     f,
							Phase:    sourceActionPhaseFromUSRKey(key, f),
							Language: sourceActionLanguage(f),
						})
					}
					lower := strings.ToLower(f)
					if f != "" && (strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".java")) {
						cfg.ActionFiles = append(cfg.ActionFiles, f)
					}
				}
			}
		case "ManuallyExtraFiles", "ExtraFiles":
			if strings.TrimSpace(value) != "" {
				cfg.ExtraFiles = append(cfg.ExtraFiles, value)
			}
		}
	}

	// Ensure standard action files if none found explicitly
	if len(cfg.ActionFiles) == 0 {
		cfg.ActionFiles = []string{"Action.c"}
	}
	if len(cfg.SourceActionFiles) == 0 {
		cfg.SourceActionFiles = []SourceActionFile{{Name: "Action.c", Phase: "action", Language: "c"}}
	}

	return cfg, nil
}

func isSupportedLifecycleSourceFile(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".java")
}

func sourceActionLanguage(value string) string {
	switch strings.ToLower(strings.TrimPrefix(filepathExt(value), ".")) {
	case "java":
		return "java"
	case "c":
		return "c"
	default:
		return ""
	}
}

func sourceActionPhaseFromUSRKey(key, fileName string) string {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if strings.Contains(lowerKey, "init") {
		return "init"
	}
	if strings.Contains(lowerKey, "end") || strings.Contains(lowerKey, "teardown") {
		return "teardown"
	}
	base := strings.ToLower(strings.TrimSpace(fileName))
	if strings.Contains(base, "vuser_init") {
		return "init"
	}
	if strings.Contains(base, "vuser_end") {
		return "teardown"
	}
	return "action"
}

func filepathExt(value string) string {
	value = strings.TrimSpace(value)
	index := strings.LastIndex(value, ".")
	if index < 0 {
		return ""
	}
	return value[index:]
}

func splitConfigList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func cleanConfigValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if decoded, ok := decodeAdjacentCStringLiterals(trimmed); ok {
		return strings.TrimSpace(decoded)
	}
	if len(trimmed) >= 2 {
		first := trimmed[0]
		last := trimmed[len(trimmed)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			trimmed = trimmed[1 : len(trimmed)-1]
		}
	}
	return strings.TrimSpace(trimmed)
}

func decodeAdjacentCStringLiterals(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed[0] != '"' {
		return "", false
	}

	var out strings.Builder
	found := false
	for i := 0; i < len(trimmed); {
		for i < len(trimmed) && isASCIIWhitespace(trimmed[i]) {
			i++
		}
		if i >= len(trimmed) {
			break
		}
		if trimmed[i] != '"' {
			return "", false
		}

		start := i
		i++
		escaped := false
		closed := false
		for i < len(trimmed) {
			ch := trimmed[i]
			if escaped {
				escaped = false
				i++
				continue
			}
			if ch == '\\' {
				escaped = true
				i++
				continue
			}
			if ch == '"' {
				i++
				closed = true
				break
			}
			i++
		}
		if !closed {
			return "", false
		}

		decoded, err := strconv.Unquote(trimmed[start:i])
		if err != nil {
			return "", false
		}
		out.WriteString(decoded)
		found = true
	}

	return out.String(), found
}

func isASCIIWhitespace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\r', '\n', '\f', '\v':
		return true
	default:
		return false
	}
}

// extractThinkTime extracts think time value in seconds from an lr_think_time call.
func ExtractThinkTimeSeconds(fn ParsedFunction) (float64, bool) {
	if fn.Name != "lr_think_time" {
		return 0, false
	}
	secStr, ok := fn.Args["seconds"]
	if !ok {
		return 0, false
	}
	sec, err := strconv.ParseFloat(secStr, 64)
	if err != nil {
		return 0, false
	}
	return sec, true
}
