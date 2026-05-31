package loadrunner

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParsedFunction represents a single LoadRunner C function call extracted from a script.
type ParsedFunction struct {
	Name       string            // e.g. "web_url", "web_submit_data", "lr_think_time"
	StepName   string            // The first string argument (step name)
	Args       map[string]string // Named key-value arguments extracted from ITEMDATA/LAST pairs
	RawArgs    []string          // Raw string arguments in order
	CallArgs   []ParsedArgument  // Structured top-level C call arguments in order
	Headers    map[string]string // Extracted header key-value pairs
	Body       string            // Request body if present
	Method     string            // HTTP method (for web_custom_request)
	URL        string            // Target URL
	Action     string            // Action/target URL for form submissions
	LineNumber int               // Source line number for diagnostics
	Filename   string            // Source file containing the function
	EndLine    int               // Source line where the function call ends
	RawBlock   string            // Original source block for source review
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
	Functions []ParsedFunction
}

// ProjectConfig holds parsed .usr file settings.
type ProjectConfig struct {
	ScriptType   string // e.g. "Web"
	ActionFiles  []string
	RuntimeFile  string
	ParamFiles   []string
	BaseURL      string
	ThinkTimeMin float64
	ThinkTimeMax float64
}

// regex patterns for LoadRunner C function parsing
var (
	// Matches VuGen C function calls such as web_url, lrd_stmt, ftp_command, or sapgui_*.
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

// ParseActionFile parses a LoadRunner .c action file and extracts function calls.
func ParseActionFile(filename string, content string) (*ParsedAction, error) {
	parsed := &ParsedAction{
		Filename:  filename,
		Functions: make([]ParsedFunction, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	var currentBlock strings.Builder
	var rawBlock strings.Builder
	inBlock := false
	blockStartLine := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip empty lines, comments
		if !inBlock {
			if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
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

		// Check if this line starts a recognized VuGen function call.
		if !inBlock && reFuncCall.MatchString(trimmed) && !isCControlCall(trimmed) {
			inBlock = true
			blockStartLine = lineNum
			currentBlock.Reset()
			rawBlock.Reset()
		}

		if inBlock {
			currentBlock.WriteString(trimmed)
			currentBlock.WriteString(" ")
			rawBlock.WriteString(line)
			rawBlock.WriteString("\n")

			// Check if the block is complete (ends with ");")
			blockStr := currentBlock.String()
			if strings.Contains(blockStr, ");") {
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
	case "web_service_call":
		parseWebServiceCall(&fn, inner)
	case "web_reg_save_param":
		parseWebRegSaveParam(&fn, inner)
	case "web_reg_find":
		parseWebRegFind(&fn, inner)
	case "lr_think_time":
		parseLRThinkTime(&fn, inner)
	case "lr_start_transaction", "lr_end_transaction":
		if len(fn.RawArgs) > 0 {
			fn.StepName = fn.RawArgs[0]
		}
	case "lr_continue_on_error", "lr_error_message", "lr_exit", "web_cache_cleanup", "web_cleanup_cookies", "web_set_sockets_option", "web_set_timeout":
		parseLoadRunnerRuntimeControl(&fn, inner)
	case "web_add_header", "web_add_auto_header":
		parseWebAddHeader(&fn, inner)
	default:
		if len(fn.RawArgs) > 0 {
			fn.StepName = fn.RawArgs[0]
		}
	}

	return fn, nil
}

func parseLoadRunnerRuntimeControl(fn *ParsedFunction, inner string) {
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
			if key, value, ok := splitLoadRunnerKeyValue(cleaned); ok {
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

func splitLoadRunnerKeyValue(value string) (string, string, bool) {
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

	if m := reURLArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	}
	if m := reMethodArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Method = m[1]
	}
	if m := reBodyArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Body = m[1]
	}
	if m := reRecContentType.FindStringSubmatch(inner); len(m) > 1 {
		fn.Headers["Content-Type"] = m[1]
	}

	extractNamedArgs(fn, inner)
}

func parseWebServiceCall(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	fn.Method = "POST"

	if m := reURLArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	} else if m := reActionArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.URL = m[1]
	}
	if m := reBodyArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Body = m[1]
	}
	if m := reRecContentType.FindStringSubmatch(inner); len(m) > 1 {
		fn.Headers["Content-Type"] = m[1]
	}

	extractNamedArgs(fn, inner)
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
	if len(fn.RawArgs) >= 2 {
		fn.StepName = fn.RawArgs[0]
		fn.Headers[fn.RawArgs[0]] = fn.RawArgs[1]
	}
}

// extractNamedArgs pulls out "Key=Value" patterns from the inner string.
func extractNamedArgs(fn *ParsedFunction, inner string) {
	matches := reNamedArg.FindAllStringSubmatch(inner, -1)
	for _, m := range matches {
		key := m[1]
		value := m[2]
		fn.Args[key] = value
	}
}

// extractItemData attempts to extract ITEMDATA form field pairs.
func extractItemData(fn *ParsedFunction, inner string) {
	// Look for patterns like "Name=fieldname", "Value=fieldvalue" pairs
	// These come after ITEMDATA marker in web_submit_data calls
	parts := strings.Split(inner, "ITEMDATA")
	if len(parts) < 2 {
		return
	}

	itemSection := parts[1]
	// Extract Name/Value pairs
	nameMatches := regexp.MustCompile(`"Name=([^"]+)"`).FindAllStringSubmatch(itemSection, -1)
	valueMatches := regexp.MustCompile(`"Value=([^"]*?)"`).FindAllStringSubmatch(itemSection, -1)

	formData := make(map[string]string)
	for i, nm := range nameMatches {
		val := ""
		if i < len(valueMatches) {
			val = valueMatches[i][1]
		}
		formData[nm[1]] = val
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

// ParseUSRFile parses a LoadRunner .usr project configuration file.
func ParseUSRFile(content string) (*ProjectConfig, error) {
	cfg := &ProjectConfig{
		ActionFiles: make([]string, 0),
		ParamFiles:  make([]string, 0),
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
			}
		case "Actions":
			if strings.HasSuffix(value, ".c") {
				cfg.ActionFiles = append(cfg.ActionFiles, value)
			}
		case "RunLogicFiles":
			if key == "RunAction" || key == "Actions" {
				for _, f := range strings.Split(value, ",") {
					f = strings.TrimSpace(f)
					if f != "" && strings.HasSuffix(f, ".c") {
						cfg.ActionFiles = append(cfg.ActionFiles, f)
					}
				}
			}
		}
	}

	// Ensure standard action files if none found explicitly
	if len(cfg.ActionFiles) == 0 {
		cfg.ActionFiles = []string{"Action.c"}
	}

	return cfg, nil
}

func cleanConfigValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 2 {
		first := trimmed[0]
		last := trimmed[len(trimmed)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			trimmed = trimmed[1 : len(trimmed)-1]
		}
	}
	return strings.TrimSpace(trimmed)
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
