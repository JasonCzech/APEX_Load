package enterprisearchive

import (
	"bufio"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// ParsedFunction represents a single C/VUser source function call extracted from a script.
type ParsedFunction struct {
	Name            string            // e.g. "web_url", "web_submit_data", "lr_think_time"
	StepName        string            // The first string argument (step name)
	Args            map[string]string // Named key-value arguments extracted from ITEMDATA/LAST pairs
	RawArgs         []string          // Raw string arguments in order
	CallArgs        []ParsedArgument  // Structured top-level C call arguments in order
	Headers         map[string]string // Extracted header key-value pairs
	HeaderList      dsl.HeaderList    // Ordered header occurrences when the source preserves them
	Body            string            // Request body if present, C escapes decoded once
	RawBody         string            // Body with its source C escapes intact (code-page byte encoding); empty when unknown
	Method          string            // HTTP method (for web_custom_request)
	URL             string            // Target URL
	Action          string            // Action/target URL for form submissions
	Cookies         []ParsedCookie    // Cookies added through web_add_cookie
	Phase           string            // init, action, or teardown
	LineNumber      int               // Source line number for diagnostics
	Filename        string            // Source file containing the function
	EndLine         int               // Source line where the function call ends
	RawBlock        string            // Original source block for source review
	JavaOrigin      bool              // Body was decoded from Java source; skip C-string unescaping
	Conditions      []string          // Enclosing if/else conditions (raw C, outermost first; else pre-negated as !(...))
	ConditionIDs    []string          // Stable per-construct identities (source positions) parallel to Conditions
	Loops           []string          // Enclosing loop headers (raw C, outermost first)
	LoopIDs         []string          // Stable per-construct identities (source positions) parallel to Loops
	sourceColumn    int               // 1-based source column used to deduplicate embedded/fallback call discovery
	sourceOffset    int               // parser-local byte offset used only for stable evaluation ordering
	sourceEndOffset int               // exclusive parser-local byte offset
	sourceFunction  string            // enclosing C function definition; used to preserve lifecycle ownership
	// cLocals are the qualified int locals of the enclosing C function that
	// its flow conditions may read (see c_locals.go).
	cLocals                             map[string]bool
	containsUnsupportedTransactionCall  bool
	dependsOnUnsupportedTransactionFlow bool
	javaJDBCConnection                  *javaJDBCConnection
	itemDataReviewReason                string
	itemDataURLForm                     bool
	// itemDataMultipart holds the ITEMDATA items of a multipart/form-data
	// step; the converter rebuilds the body from them (request_multipart.go)
	// or keeps itemDataReviewReason's gate.
	itemDataMultipart []sourceMultipartItem
	// embeddedCall marks a call to a script-defined C function made inside
	// an expression rather than as its own statement (see
	// embeddedHelperCallKind). It runs only through helper inlining.
	embeddedCall string
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
	Filename      string
	Phase         string
	Functions     []ParsedFunction
	ParseFailures []CSourceParseFailure
	// vtsReferences preserves unsupported shared-data dependencies even when
	// they occur in assignments, predicates, macros or function aliases.
	vtsReferences []vtsSourceReference
	// SplicedCommentLines are code lines a backslash-continued // comment
	// swallows; they are reported, not imported.
	SplicedCommentLines []CSplicedCommentLine
	// embeddedCalls are calls made inside expressions (see
	// embeddedHelperCallKind), in source order. Only helper inlining reads
	// them.
	embeddedCalls []ParsedFunction
}

// CSourceParseFailure is value-free evidence that a C lifecycle/action file
// could not be analyzed completely. Raw parser diagnostics and source text are
// intentionally excluded because this record is later persisted in action
// metadata and import-review responses.
type CSourceParseFailure struct {
	Filename   string
	Phase      string
	LineNumber int
	Kind       string
}

// SourceActionFile records a lifecycle/action source file declared by the .usr
// project, including language and source phase.
type SourceActionFile struct {
	Name     string `json:"name"`
	Phase    string `json:"phase,omitempty"`
	Language string `json:"language,omitempty"`
	Function string `json:"function,omitempty"`
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
	DefaultCfg        string
	CfgProfiles       map[string]string
	RunLogicProfiles  map[string]string
	ParamFiles        []string
	ExtraFiles        []string
	BaseURL           string
	ThinkTimeMin      float64
	ThinkTimeMax      float64
	// ParamLeftBrace and ParamRightBrace are the .usr parameter delimiters
	// ("{" and "}" unless the script changed them, as Java scripts often do).
	ParamLeftBrace  string
	ParamRightBrace string
	// Transactions are the transaction names the project declares in
	// [TransactionsOrder]/[Transactions], in declared order. They let the
	// importer cross-check that every declared transaction survived
	// conversion.
	Transactions []string
	// runtimePointerConflicts retains conflicting case-insensitive .usr
	// cfg/run-logic pointer declarations for fail-closed profile resolution.
	runtimePointerConflicts []runtimeConfigCaseFoldConflict
}

// VuGen project files are line-oriented and normally contain short settings.
// Bound individual lines independently from the archive entry limit so an
// adversarial project file cannot force unbounded scanner growth, while still
// allowing large run-logic/action lists.
const maxUSRConfigLineBytes = 1 << 20

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
func ParseActionFile(filename string, content string) (parsed *ParsedAction, parseErr error) {
	defer func() {
		if parsed != nil {
			parsed.vtsReferences = scanVTSSourceReferences(content)
		}
	}()
	analysis := analyzeCTransactionSource(filename, content)
	spliced := findSplicedCommentLines(content)
	astParsed, astErr := parseActionFileASTWithAnalysis(filename, content, analysis)
	if astErr == nil && len(astParsed.Functions) > 0 {
		astParsed.SplicedCommentLines = spliced
		return astParsed, nil
	}

	legacyParsed, legacyErr := parseActionFileLegacy(filename, content, splicedCommentLineSet(spliced))
	legacyParsed.SplicedCommentLines = spliced
	if astParsed != nil {
		legacyParsed.ParseFailures = appendUniqueCSourceParseFailures(
			legacyParsed.ParseFailures,
			astParsed.ParseFailures...,
		)
	}
	if astErr != nil {
		legacyParsed.ParseFailures = append(legacyParsed.ParseFailures, CSourceParseFailure{
			Filename: filename,
			Kind:     "syntax_error",
		})
		legacyParsed.ParseFailures = appendUniqueCSourceParseFailures(
			legacyParsed.ParseFailures,
			analysis.failures...,
		)
		mergeRecoveredSourceTransactionCalls(
			legacyParsed,
			scanUnsupportedSourceTransactionCallsWithAnalysis(filename, content, analysis),
			analysis,
		)
	}
	if legacyErr == nil && len(legacyParsed.Functions) > 0 {
		return legacyParsed, nil
	}

	// Neither parser recovered any calls. Trust a clean AST parse of an
	// otherwise-empty file; otherwise surface the richer AST diagnostic so the
	// caller can flag the archive for review.
	if astErr == nil {
		astParsed.SplicedCommentLines = spliced
		return astParsed, nil
	}
	return legacyParsed, astErr
}

func appendUniqueCSourceParseFailures(
	failures []CSourceParseFailure,
	values ...CSourceParseFailure,
) []CSourceParseFailure {
	for _, value := range values {
		duplicate := false
		for _, existing := range failures {
			if existing.Filename == value.Filename &&
				existing.Phase == value.Phase &&
				existing.LineNumber == value.LineNumber &&
				existing.Kind == value.Kind {
				duplicate = true
				break
			}
		}
		if !duplicate {
			failures = append(failures, value)
		}
	}
	return failures
}

// parseActionFileLegacy scans content line by line. Lines in splicedComments
// belong to a backslash-continued // comment and are skipped, as the C parser
// skips them.
func parseActionFileLegacy(filename string, content string, splicedComments map[int]bool) (*ParsedAction, error) {
	parsed := &ParsedAction{
		Filename:  filename,
		Functions: make([]ParsedFunction, 0),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), maxEnterpriseArchiveEntryBytes)
	lineNum := 0
	var currentBlock strings.Builder
	var rawBlock strings.Builder
	inBlock := false
	inComment := false
	blockStartLine := 0
	blockStartColumn := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		parseLine := ""
		if !splicedComments[lineNum] {
			parseLine = stripCComments(line, &inComment)
		}
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
			match := reFuncCall.FindStringSubmatch(trimmed)
			blockStartColumn = 1
			if len(match) >= 2 {
				if column := strings.Index(parseLine, match[1]); column >= 0 {
					blockStartColumn = column + 1
				}
			}
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
			if containsCFunctionTerminator(trimmed) {
				blockStr := currentBlock.String()
				fn, err := parseFunctionBlock(blockStr, filename, blockStartLine, lineNum, rawBlock.String())
				if err == nil {
					fn.sourceColumn = blockStartColumn
					parsed.Functions = append(parsed.Functions, fn)
				}
				inBlock = false
				currentBlock.Reset()
				rawBlock.Reset()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan action source %s: %w", filename, err)
	}
	return parsed, nil
}

func containsCFunctionTerminator(line string) bool {
	inString := false
	quote := byte(0)
	escaped := false
	for index := 0; index < len(line); index++ {
		ch := line[index]
		if inString {
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
			continue
		}
		if ch == ')' && index+1 < len(line) && line[index+1] == ';' {
			return true
		}
	}
	return false
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
	case "lr_think_time", "lr_force_think_time":
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
		fn.Args[fmt.Sprintf("arg%d", index+1)] = cleaned
		if cleaned != "" && fn.Args["value"] == "" {
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
		case strings.EqualFold(cleaned, "LAST") || strings.EqualFold(cleaned, "ITEMDATA") || strings.EqualFold(cleaned, "ENDITEM") || strings.EqualFold(cleaned, "EXTRARES"):
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
// ("Text="/"Alt="/"Src="). Only an explicit "URL=" gives a usable target (and
// web_image_check's "Src=", which names the image it verifies); otherwise
// fn.URL is left empty and the converter gates the step instead of
// fabricating a request.
func parseWebLinkImage(fn *ParsedFunction, inner string) {
	if len(fn.RawArgs) > 0 {
		fn.StepName = fn.RawArgs[0]
	}
	fn.Method = "GET"
	extractNamedArgs(fn, inner)
	if v := fn.Args["URL"]; v != "" {
		fn.URL = v
	} else if v := fn.Args["Src"]; v != "" && fn.Name == "web_image_check" {
		// web_image_check verifies that an image is present, so its Src names
		// that image. web_image instead clicks the hyperlink wrapping the image:
		// its Src is a selector, never the request target.
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
	if method := strings.TrimSpace(fn.Args["Method"]); method != "" {
		fn.Method = strings.ToUpper(method)
	}
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
		fn.Body = rawSourceStringContent(m[1])
		fn.RawBody = m[1]
	}
	if fn.Args["Body"] != "" {
		fn.Body = fn.Args["Body"]
		fn.RawBody = rawSourceArgument(fn, "Body", fn.Body)
	}
	concatenateSourceBodySegments(fn)
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
		fn.RawBody = rawSourceArgument(fn, "Body", fn.Body)
	} else if m := reBodyArg.FindStringSubmatch(inner); len(m) > 1 {
		fn.Body = rawSourceStringContent(m[1])
		fn.RawBody = m[1]
	}
	concatenateSourceBodySegments(fn)
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
				fn.HeaderList = append(fn.HeaderList, dsl.HeaderEntry{
					Name:  currentName,
					Value: arg.Value,
				})
			}
		}
	}
	// HeaderList is the authoritative replay surface. Derive the compatibility
	// map only after every source occurrence has been collected so duplicate
	// names, casing, and source order are not destroyed during parsing.
	if len(fn.HeaderList) > 0 {
		fn.Headers = fn.HeaderList.ToMap()
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
		fn.Body = rawSourceStringContent(m[1])
		fn.RawBody = m[1]
	}
	if fn.Args["Body"] != "" {
		fn.Body = fn.Args["Body"]
		fn.RawBody = rawSourceArgument(fn, "Body", fn.Body)
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
		if arg.Kind == "macro" && (strings.EqualFold(arg.Value, "ITEMDATA") || strings.EqualFold(arg.Value, "EXTRARES")) {
			break
		}
		if arg.Kind == "key_value" && arg.Name != "" {
			fn.Args[arg.Name] = arg.Value
		}
	}
}

const unsupportedItemDataEncodingReason = "web_submit_data_itemdata_encoding_unsupported"

// extractItemData extracts the ordered ITEMDATA form occurrences. APEX can
// faithfully materialize URL-encoded Name/Value pairs here. Other encodings
// need a structured multipart/raw body representation, so retain the source
// for review and leave the executable body empty instead of emitting
// URL-encoded bytes under a contradictory Content-Type.
func extractItemData(fn *ParsedFunction, inner string) {
	type formField = sourceMultipartItem
	formData := make([]formField, 0)
	current := formField{}
	hasName := false
	hasValue := false
	hasEncryptedValue := false
	inItemData := false
	hasItemData := false
	hasUnsupportedField := false
	hasMalformedItem := false
	finished := false
	resetItem := func() {
		current = formField{}
		hasName = false
		hasValue = false
	}
	finishItem := func() {
		if !hasName && !hasValue {
			hasMalformedItem = true
			return
		}
		if !hasName || !hasValue {
			hasMalformedItem = true
			resetItem()
			return
		}
		formData = append(formData, current)
		resetItem()
	}
	for _, arg := range fn.CallArgs {
		if arg.Kind == "macro" && strings.EqualFold(arg.Value, "ITEMDATA") {
			if inItemData {
				hasMalformedItem = true
			}
			inItemData = true
			hasItemData = true
			continue
		}
		if !inItemData {
			continue
		}
		if arg.Kind == "macro" {
			switch {
			case strings.EqualFold(arg.Value, "ENDITEM"):
				finishItem()
			case strings.EqualFold(arg.Value, "LAST"), strings.EqualFold(arg.Value, "EXTRARES"):
				if hasName || hasValue {
					hasMalformedItem = true
					resetItem()
				}
				finished = true
				inItemData = false
			default:
				hasMalformedItem = true
			}
			if finished {
				break
			}
			continue
		}
		if arg.Kind != "key_value" {
			// Expressions such as "Value=" + helper() and lr_unmask(...)
			// cannot be reconstructed as one form component. Ignoring them would
			// leave a partial request runnable, so fail closed instead.
			hasMalformedItem = true
			continue
		}
		switch strings.ToLower(arg.Name) {
		case "name":
			if hasName || hasValue {
				hasMalformedItem = true
				continue
			}
			current.name = arg.Value
			hasName = true
		case "value":
			if !hasName || hasValue {
				hasMalformedItem = true
				continue
			}
			current.value = arg.Value
			hasValue = true
		case "file", "filepath", "contenttype", "contenttransferencoding":
			// Multipart attributes: serializable only as multipart/form-data.
			hasUnsupportedField = true
			switch strings.ToLower(arg.Name) {
			case "file":
				current.file = strings.EqualFold(strings.TrimSpace(arg.Value), "yes")
			case "filepath":
				current.filePath = arg.Value
			case "contenttype":
				current.contentType = arg.Value
			default:
				current.transferEncoding = arg.Value
			}
		case "encryptedvalue":
			hasUnsupportedField = true
			hasEncryptedValue = true
		default:
			// Unknown ITEMDATA attributes must not be discarded: they may alter
			// serialization or identify a source field shape APEX does not model.
			hasMalformedItem = true
		}
	}

	if !hasItemData {
		return
	}
	if !finished || hasName || hasValue {
		hasMalformedItem = true
	}
	contentType, supported := sourceItemDataFormContentType(fn.Args)
	if !supported || hasUnsupportedField || hasMalformedItem {
		fn.Body = ""
		delete(fn.Headers, "Content-Type")
		fn.itemDataReviewReason = unsupportedItemDataEncodingReason
		// File parts outside multipart/form-data, encrypted values and
		// malformed items keep the gate.
		if !hasMalformedItem && !hasEncryptedValue && sourceItemDataIsMultipart(fn.Args) {
			fn.itemDataMultipart = formData
		}
		return
	}

	pairs := make([]string, 0, len(formData))
	for _, field := range formData {
		pairs = append(pairs,
			encodeSourceFormComponent(field.name)+"="+encodeSourceFormComponent(field.value),
		)
	}
	fn.Body = strings.Join(pairs, "&")
	fn.itemDataURLForm = true
	if len(formData) > 0 {
		fn.Headers["Content-Type"] = contentType
	}
}

func sourceItemDataFormContentType(args map[string]string) (string, bool) {
	raw, present := sourceNamedArg(args, "EncType")
	if !present {
		return "application/x-www-form-urlencoded", true
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "raw") {
		return "", false
	}
	mediaType, params, err := mime.ParseMediaType(raw)
	if err != nil || !strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		return "", false
	}
	for name, value := range params {
		if !strings.EqualFold(name, "charset") ||
			(!strings.EqualFold(value, "utf-8") && !strings.EqualFold(value, "utf8")) {
			return "", false
		}
	}
	return raw, true
}

func sourceNamedArg(args map[string]string, key string) (string, bool) {
	if value, ok := args[key]; ok {
		return value, true
	}
	for name, value := range args {
		if strings.EqualFold(name, key) {
			return value, true
		}
	}
	return "", false
}

// encodeSourceFormComponent applies HTML form escaping to literal spans while
// retaining source parameter templates for replay-time interpolation.
func encodeSourceFormComponent(value string) string {
	value = convertParamReferences(value)
	var encoded strings.Builder
	for len(value) > 0 {
		open := strings.Index(value, "{{")
		if open < 0 {
			encoded.WriteString(url.QueryEscape(value))
			break
		}
		encoded.WriteString(url.QueryEscape(value[:open]))
		closeOffset := strings.Index(value[open+2:], "}}")
		if closeOffset < 0 {
			encoded.WriteString(url.QueryEscape(value[open:]))
			break
		}
		closeIndex := open + 2 + closeOffset + 2
		encoded.WriteString(value[open:closeIndex])
		value = value[closeIndex:]
	}
	return encoded.String()
}

// ParseConfigSections parses INI-style section/key=value content into nested maps.
func ParseConfigSections(content string) map[string]map[string]string {
	sections := make(map[string]map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	maxTokenSize := len(content)
	if maxTokenSize < 64*1024 {
		maxTokenSize = 64 * 1024
	} else if maxTokenSize < int(^uint(0)>>1) {
		maxTokenSize++
	}
	// Runtime cfg entries are already bounded by archive-ingest limits. Let a
	// single line consume that bounded input instead of silently stopping at
	// bufio.Scanner's 64 KiB default and treating later settings as absent.
	scanner.Buffer(make([]byte, 64*1024), maxTokenSize)
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
		key := strings.TrimSpace(parts[0])
		sections[section][key] = cleanINIConfigValue(section, key, parts[1])
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
		CfgProfiles:       make(map[string]string),
		RunLogicProfiles:  make(map[string]string),
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), maxUSRConfigLineBytes)
	section := ""
	transactionNames := make(map[string]struct{})

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
		rawValue := parts[1]
		value := cleanConfigValue(rawValue)
		pathValue := cleanRuntimePathValue(rawValue)
		if strings.HasSuffix(strings.ToLower(pathValue), ".prm") {
			cfg.ParamFiles = append(cfg.ParamFiles, pathValue)
		}

		normalizedSection := strings.ToLower(strings.TrimSpace(section))
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		switch normalizedSection {
		case "general":
			switch normalizedKey {
			case "type":
				cfg.ScriptType = value
			case "activetypes", "additionaltypes", "generatetypes":
				cfg.ActiveTypes = appendUnique(cfg.ActiveTypes, splitConfigList(value)...)
			case "scriptlanguage":
				cfg.ScriptLanguage = value
			case "runtype":
				cfg.RunType = value
			case "paramleftbrace":
				cfg.ParamLeftBrace = value
			case "paramrightbrace":
				cfg.ParamRightBrace = value
			case "defaultcfg":
				cfg.DefaultCfg = pathValue
				cfg.RuntimeFile = pathValue
			case "runtimesettings", "runtimefile":
				cfg.RuntimeFile = pathValue
			}
		case "cfgfiles":
			setCaseInsensitiveProfile(cfg.CfgProfiles, key, pathValue)
		case "actions":
			if isSupportedLifecycleSourceFile(value) {
				cfg.SourceActionFiles = append(cfg.SourceActionFiles, SourceActionFile{
					Name:     value,
					Phase:    sourceActionPhaseFromUSRKey(key, value),
					Language: sourceActionLanguage(value),
					Function: strings.TrimSpace(key),
				})
			}
			if strings.HasSuffix(strings.ToLower(value), ".c") || strings.HasSuffix(strings.ToLower(value), ".java") {
				cfg.ActionFiles = append(cfg.ActionFiles, value)
			}
		case "runlogicfiles":
			if strings.HasSuffix(strings.ToLower(pathValue), ".usp") {
				setCaseInsensitiveProfile(cfg.RunLogicProfiles, key, pathValue)
			}
			if normalizedKey == "runaction" || normalizedKey == "actions" {
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
		case "manuallyextrafiles", "extrafiles":
			if strings.TrimSpace(value) != "" {
				cfg.ExtraFiles = append(cfg.ExtraFiles, value)
			}
		case "transactionsorder":
			if normalizedKey == "order" {
				names, err := splitProjectTransactionOrder(rawValue)
				if err != nil {
					return nil, fmt.Errorf("parse [TransactionsOrder] Order: %w", err)
				}
				for _, name := range names {
					appendProjectTransaction(cfg, transactionNames, name)
				}
			}
		case "transactions":
			// [Transactions] lists one transaction name per key; values are
			// normally empty annotations. VuGen quotes keys containing spaces;
			// those delimiters are not part of the runtime transaction name.
			appendProjectTransaction(cfg, transactionNames, cleanConfigValue(key))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan LoadRunner project config: %w", err)
	}
	for _, conflict := range runtimeConfigCaseFoldConflicts(content) {
		if usrRuntimePointerConflict(conflict) {
			cfg.runtimePointerConflicts = append(cfg.runtimePointerConflicts, conflict)
		}
	}
	if strings.TrimSpace(cfg.RuntimeFile) == "" && len(cfg.CfgProfiles) == 1 {
		for _, candidate := range cfg.CfgProfiles {
			cfg.RuntimeFile = candidate
		}
	}

	// Ensure standard action files if none found explicitly
	if len(cfg.ActionFiles) == 0 {
		cfg.ActionFiles = []string{"Action.c"}
	}
	if len(cfg.SourceActionFiles) == 0 {
		cfg.SourceActionFiles = []SourceActionFile{{Name: "Action.c", Phase: "action", Language: "c", Function: "Action"}}
	}

	return cfg, nil
}

func usrRuntimePointerConflict(conflict runtimeConfigCaseFoldConflict) bool {
	switch strings.ToLower(strings.TrimSpace(conflict.section)) {
	case "general":
		switch strings.ToLower(strings.TrimSpace(conflict.key)) {
		case "defaultcfg", "runtimesettings", "runtimefile":
			return true
		}
	case "cfgfiles":
		for _, occurrence := range conflict.occurrences {
			if strings.HasSuffix(strings.ToLower(strings.TrimSpace(occurrence.value)), ".cfg") {
				return true
			}
		}
	case "runlogicfiles":
		for _, occurrence := range conflict.occurrences {
			if strings.HasSuffix(strings.ToLower(strings.TrimSpace(occurrence.value)), ".usp") {
				return true
			}
		}
	}
	return false
}

func setCaseInsensitiveProfile(profiles map[string]string, name, value string) {
	for existing := range profiles {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(name)) {
			delete(profiles, existing)
		}
	}
	profiles[name] = value
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

// splitProjectTransactionOrder parses the quoted comma-separated transaction
// list VuGen writes in [TransactionsOrder]. Unlike the generic config-list
// helper, separators inside a transaction name remain part of that name.
func splitProjectTransactionOrder(value string) ([]string, error) {
	if strings.Contains(value, "__*delimiter*__") {
		value = strings.Trim(strings.TrimSpace(value), "\"")
		return strings.Split(value, "__*delimiter*__"), nil
	}
	var names []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		name := strings.TrimSpace(current.String())
		current.Reset()
		if name != "" {
			names = append(names, name)
		}
	}
	for _, character := range value {
		if escaped {
			if character != quote && character != '\\' {
				current.WriteRune('\\')
			}
			current.WriteRune(character)
			escaped = false
			continue
		}
		if quote != 0 {
			if character == '\\' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
				continue
			}
			current.WriteRune(character)
			continue
		}
		switch character {
		case '"', '\'':
			if strings.TrimSpace(current.String()) == "" {
				current.Reset()
				quote = character
			} else {
				current.WriteRune(character)
			}
		case ',', ';', '|':
			flush()
		default:
			current.WriteRune(character)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted transaction name")
	}
	flush()
	return names, nil
}

func appendProjectTransaction(cfg *ProjectConfig, seen map[string]struct{}, name string) {
	if cfg == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	identity := strings.ToLower(name)
	if _, exists := seen[identity]; exists {
		return
	}
	seen[identity] = struct{}{}
	cfg.Transactions = append(cfg.Transactions, name)
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

func cleanINIConfigValue(section, key, value string) string {
	if strings.EqualFold(strings.TrimSpace(key), "DefaultCfg") ||
		strings.EqualFold(strings.TrimSpace(key), "DefaultRunLogic") ||
		strings.EqualFold(strings.TrimSpace(section), "CfgFiles") ||
		strings.EqualFold(strings.TrimSpace(section), "RunLogicFiles") {
		return cleanRuntimePathValue(value)
	}
	cleaned := cleanConfigValue(value)
	if cleaned == "" && strings.EqualFold(strings.TrimSpace(key), "Delimiter") {
		return whitespaceParameterDelimiter(value)
	}
	return cleaned
}

// cleanRuntimePathValue removes INI quoting without interpreting backslashes
// as C escapes. A quoted Windows path such as "profiles\active.cfg" must retain
// its separators until normalizeRuntimeArchivePath converts them to slashes.
func cleanRuntimePathValue(value string) string {
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

		decoded, ok := decodeCStringContent(trimmed[start+1 : i-1])
		if !ok {
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
	if fn.Name != "lr_think_time" && fn.Name != "lr_force_think_time" {
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
