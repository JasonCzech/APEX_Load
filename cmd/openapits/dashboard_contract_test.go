package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	"gopkg.in/yaml.v3"
)

type routeKey struct {
	Method string
	Path   string
}

type dashboardCall struct {
	File     string
	Line     int
	Callee   string
	Argument string
	Method   string
	Path     string
}

type dynamicCallRule struct {
	File           string
	Callee         string
	ArgumentPrefix string
	Routes         []routeKey
	Reason         string
}

// These are the only controller calls in dashboard API hooks whose route cannot
// be recovered directly from the first argument. Generic transport helpers are
// allowed only when their concrete wrapper call sites are scanned separately.
// Finite dynamic routes list every OpenAPI operation they can dispatch to.
var dynamicDashboardCallRules = []dynamicCallRule{
	{
		File:           "useApplicationApi.ts",
		Callee:         "apiFetch",
		ArgumentPrefix: "path",
		Reason:         "generic blob helper; every fetchBlob call site is scanned",
	},
	{
		File:           "useTestApi.ts",
		Callee:         "apiFetch",
		ArgumentPrefix: "path",
		Reason:         "generic blob helper; every fetchBlob call site is scanned",
	},
	{
		File:           "useSystemApi.ts",
		Callee:         "apiFetch",
		ArgumentPrefix: "path",
		Reason:         "generic blob helper; every fetchBlob call site is scanned",
	},
	{
		File:           "useLibraryApi.ts",
		Callee:         "apiFetch",
		ArgumentPrefix: "path",
		Reason:         "generic transfer helper; every transferRequest call site is scanned",
	},
	{
		File:           "useLibraryApi.ts",
		Callee:         "request",
		ArgumentPrefix: "catalogPath(",
		Routes:         []routeKey{{Method: "GET", Path: "/api/v1/library/catalog"}},
		Reason:         "helper adds query parameters to one fixed controller route",
	},
	{
		File:           "useLibraryApi.ts",
		Callee:         "request",
		ArgumentPrefix: "remoteCatalogPath(",
		Routes:         []routeKey{{Method: "GET", Path: "/api/v1/library/remotes/{param}/catalog"}},
		Reason:         "helper adds query parameters to one parameterized controller route",
	},
	{
		File:           "useRecorderApi.ts",
		Callee:         "request",
		ArgumentPrefix: "projectId?",
		Routes:         []routeKey{{Method: "GET", Path: "/api/v1/recorder/status"}},
		Reason:         "conditional expression changes only the query string",
	},
	{
		File:           "useBrowserRecorderApi.ts",
		Callee:         "request",
		ArgumentPrefix: "path",
		Reason:         "controller/record-agent dispatcher; every recorderRequest call site is scanned",
	},
	{
		File:           "useBrowserRecorderApi.ts",
		Callee:         "apiFetch",
		ArgumentPrefix: "path",
		Routes:         []routeKey{{Method: "GET", Path: "/api/v1/browser-recorder/sessions/{param}/export"}},
		Reason:         "export path is assembled immediately before controller/record-agent dispatch",
	},
	{
		File:           "useScriptApi.ts",
		Callee:         "apiFetch",
		ArgumentPrefix: "${endpoint}",
		Routes: []routeKey{
			{Method: "POST", Path: "/api/v1/scripts/import/postman"},
			{Method: "POST", Path: "/api/v1/scripts/import/postman/validate"},
		},
		Reason: "endpoint is a TypeScript union restricted to the Postman import and validation routes",
	},
	{
		File:           "useTestApi.ts",
		Callee:         "request",
		ArgumentPrefix: "/remotes/${encodeURIComponent(remoteId)}/runs/${encodeURIComponent(remoteTestId)}/${action}",
		Routes: []routeKey{
			{Method: "POST", Path: "/api/v1/remotes/{param}/runs/{param}/start"},
			{Method: "POST", Path: "/api/v1/remotes/{param}/runs/{param}/stop"},
			{Method: "POST", Path: "/api/v1/remotes/{param}/runs/{param}/abort"},
			{Method: "POST", Path: "/api/v1/remotes/{param}/runs/{param}/pause"},
			{Method: "POST", Path: "/api/v1/remotes/{param}/runs/{param}/resume"},
		},
		Reason: "action is a TypeScript union restricted to the five remote-run control routes",
	},
}

var (
	controllerCallNamePattern = regexp.MustCompile(`\b(request|apiFetch|fetchBlob|recorderRequest|transferRequest)\b`)
	methodOptionPattern       = regexp.MustCompile(`(?i)\bmethod\s*:\s*['\"]([a-z]+)['\"]`)
	constantPathPattern       = regexp.MustCompile(`(?m)^\s*const\s+([A-Z][A-Z0-9_]*)\s*=\s*['\"](/[^'\"\r\n]*)['\"]`)
	pathParameterPattern      = regexp.MustCompile(`\{[^}/]+\}`)
)

func TestDashboardHookRoutesHaveGeneratedOpenAPIBindings(t *testing.T) {
	root := repositoryRoot(t)
	document := readOpenAPIDocument(t, root)
	operations, err := collectOperations(&document)
	if err != nil {
		t.Fatalf("collect OpenAPI operations: %v", err)
	}
	openAPIRoutes := make(map[routeKey]locatedOperation, len(operations))
	for _, located := range operations {
		key := routeKey{Method: strings.ToUpper(located.Method), Path: canonicalRoutePath(located.Path)}
		if previous, exists := openAPIRoutes[key]; exists {
			t.Fatalf("OpenAPI routes %s and %s collapse to the same dashboard route key %s %s", previous.Path, located.Path, key.Method, key.Path)
		}
		openAPIRoutes[key] = located
	}
	generatedContract := string(mustReadFile(t, filepath.Join(root, "dashboard", "src", "types", "generated", "openapi.ts")))

	hookFiles, err := filepath.Glob(filepath.Join(root, "dashboard", "src", "hooks", "api", "*.ts"))
	if err != nil {
		t.Fatalf("list dashboard API hooks: %v", err)
	}
	sort.Strings(hookFiles)
	var calls []dashboardCall
	for _, hookFile := range hookFiles {
		if strings.HasSuffix(hookFile, ".test.ts") {
			continue
		}
		source, err := os.ReadFile(hookFile)
		if err != nil {
			t.Fatalf("read %s: %v", hookFile, err)
		}
		calls = append(calls, scanDashboardControllerCalls(filepath.Base(hookFile), string(source))...)
	}

	webSocketPath := filepath.Join(root, "dashboard", "src", "hooks", "useWebSocket.ts")
	webSocketSource, err := os.ReadFile(webSocketPath)
	if err != nil {
		t.Fatalf("read websocket hook: %v", err)
	}
	calls = append(calls, scanDirectAPIV1Literals(filepath.Base(webSocketPath), string(webSocketSource))...)

	if len(calls) < 300 {
		t.Fatalf("dashboard route scanner found only %d calls; expected the API hook inventory", len(calls))
	}
	usedRules := make(map[int]bool)
	for _, call := range calls {
		if call.Path != "" {
			key := routeKey{Method: call.Method, Path: call.Path}
			if located, exists := openAPIRoutes[key]; exists {
				assertGeneratedPathBinding(t, generatedContract, located)
				continue
			}
		}

		ruleIndex, rule := matchingDynamicCallRule(call)
		if rule == nil {
			t.Errorf("%s:%d: %s(%s) resolves to %s %s, which has no OpenAPI operation binding", call.File, call.Line, call.Callee, call.Argument, call.Method, call.Path)
			continue
		}
		usedRules[ruleIndex] = true
		for _, route := range rule.Routes {
			route.Method = strings.ToUpper(route.Method)
			route.Path = canonicalRoutePath(route.Path)
			located, exists := openAPIRoutes[route]
			if !exists {
				t.Errorf("dynamic route rule for %s:%d (%s) references missing OpenAPI operation %s %s", call.File, call.Line, rule.Reason, route.Method, route.Path)
				continue
			}
			assertGeneratedPathBinding(t, generatedContract, located)
		}
	}
	for index, rule := range dynamicDashboardCallRules {
		if !usedRules[index] {
			t.Errorf("stale dynamic dashboard call rule for %s %s(%s): %s", rule.File, rule.Callee, rule.ArgumentPrefix, rule.Reason)
		}
	}
}

func TestGeneratedOperationsRepresentEveryDeclaredContentSchema(t *testing.T) {
	root := repositoryRoot(t)
	document := readOpenAPIDocument(t, root)
	operations, err := collectOperations(&document)
	if err != nil {
		t.Fatalf("collect OpenAPI operations: %v", err)
	}
	generated, err := generateTransportTypes(mustReadFile(t, filepath.Join(root, "docs", "swagger.yaml")))
	if err != nil {
		t.Fatalf("generate transport types: %v", err)
	}
	blocks := generatedOperationBlocks(t, string(generated), operations)

	for _, located := range operations {
		operation := located.Operation
		block := blocks[operation.OperationID]
		if operation.RequestBody == nil {
			if !strings.Contains(block, "    requestBody?: never;\n") {
				t.Errorf("%s omits its absent request body marker", operation.OperationID)
			}
		} else if operation.RequestBody.Ref != "" && len(operation.RequestBody.Content) == 0 {
			if !strings.Contains(block, "    requestBody") || !strings.Contains(block, ": unknown;\n") {
				t.Errorf("%s omits its referenced request body", operation.OperationID)
			}
		} else {
			if !strings.Contains(block, "    requestBody") {
				t.Errorf("%s omits its request body", operation.OperationID)
			}
			assertMediaSchemasRendered(t, operation.OperationID+" request", block, operation.RequestBody.Content, 4)
		}

		expectedResponseSchemaLines := make(map[string]int)
		expectedNoContentResponses := 0
		for _, status := range sortedKeys(operation.Responses) {
			response := operation.Responses[status]
			statusPrefix := "      " + quote(status) + ": "
			if !strings.Contains(block, statusPrefix) {
				t.Errorf("%s omits response status %s", operation.OperationID, status)
				continue
			}
			if response == nil || (response.Ref != "" && len(response.Content) == 0 && len(response.Headers) == 0) {
				if !strings.Contains(block, statusPrefix+"unknown;") {
					t.Errorf("%s response %s omits its dynamic representation", operation.OperationID, status)
				}
				continue
			}
			if len(response.Content) == 0 {
				expectedNoContentResponses++
				continue
			}
			for line, count := range expectedMediaSchemaLines(response.Content, 5) {
				expectedResponseSchemaLines[line] += count
			}
		}
		for line, expectedCount := range expectedResponseSchemaLines {
			if actualCount := strings.Count(block, line); actualCount < expectedCount {
				t.Errorf("%s response content represents a declared media schema %d time(s), want at least %d: %s", operation.OperationID, actualCount, expectedCount, strings.TrimSpace(line))
			}
		}
		noContentLine := "        content?: never;\n"
		if actualCount := strings.Count(block, noContentLine); actualCount < expectedNoContentResponses {
			t.Errorf("%s represents %d no-content responses, want at least %d", operation.OperationID, actualCount, expectedNoContentResponses)
		}
	}
}

func TestDashboardDomainTypesDoNotImportCompatibilityBarrel(t *testing.T) {
	root := repositoryRoot(t)
	typesDir := filepath.Join(root, "dashboard", "src", "types")
	entries, err := os.ReadDir(typesDir)
	if err != nil {
		t.Fatalf("read dashboard type modules: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".test.ts") {
			continue
		}
		source := string(mustReadFile(t, filepath.Join(typesDir, name)))
		if strings.Contains(source, "from '../types'") || strings.Contains(source, `from "../types"`) {
			t.Errorf("dashboard domain type module %s imports the compatibility barrel; import its owning domain module instead", name)
		}
	}
}

func readOpenAPIDocument(t *testing.T, root string) openAPIDocument {
	t.Helper()
	input := mustReadFile(t, filepath.Join(root, "docs", "swagger.yaml"))
	var document openAPIDocument
	if err := yaml.Unmarshal(input, &document); err != nil {
		t.Fatalf("parse OpenAPI document: %v", err)
	}
	return document
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return value
}

func assertGeneratedPathBinding(t *testing.T, generated string, located locatedOperation) {
	t.Helper()
	binding := quote(located.Method) + ": operations[" + quote(located.Operation.OperationID) + "];"
	if !strings.Contains(generated, binding) {
		t.Errorf("generated paths omit %s %s (%s)", strings.ToUpper(located.Method), located.Path, located.Operation.OperationID)
	}
}

func assertMediaSchemasRendered(t *testing.T, label, block string, content map[string]mediaType, indent int) {
	t.Helper()
	if len(content) == 0 {
		if !strings.Contains(block, strings.Repeat("  ", indent-1)+"content?: never;\n") {
			t.Errorf("%s omits its no-content representation", label)
		}
		return
	}
	expectedLines := expectedMediaSchemaLines(content, indent)
	for line, expectedCount := range expectedLines {
		if actualCount := strings.Count(block, line); actualCount < expectedCount {
			t.Errorf("%s represents a declared media schema %d time(s), want at least %d: %s", label, actualCount, expectedCount, strings.TrimSpace(line))
		}
	}
}

func expectedMediaSchemaLines(content map[string]mediaType, indent int) map[string]int {
	expectedLines := make(map[string]int, len(content))
	for _, name := range sortedKeys(content) {
		var rendered strings.Builder
		writeSchema(&rendered, content[name].Schema, indent)
		line := strings.Repeat("  ", indent) + quote(name) + ": " + rendered.String() + ";\n"
		expectedLines[line]++
	}
	return expectedLines
}

func generatedOperationBlocks(t *testing.T, output string, operations []locatedOperation) map[string]string {
	t.Helper()
	operationIDs := make([]string, 0, len(operations))
	for _, located := range operations {
		operationIDs = append(operationIDs, located.Operation.OperationID)
	}
	sort.Strings(operationIDs)
	blocks := make(map[string]string, len(operationIDs))
	for index, operationID := range operationIDs {
		marker := "  " + quote(operationID) + ": {\n"
		start := strings.Index(output, marker)
		if start < 0 {
			t.Fatalf("generated operations omit %s", operationID)
		}
		end := strings.Index(output, "\n}\n\nexport interface paths")
		if index+1 < len(operationIDs) {
			nextMarker := "  " + quote(operationIDs[index+1]) + ": {\n"
			relativeEnd := strings.Index(output[start+len(marker):], nextMarker)
			if relativeEnd < 0 {
				t.Fatalf("cannot locate generated operation boundary after %s", operationID)
			}
			end = start + len(marker) + relativeEnd
		}
		if end < start {
			t.Fatalf("cannot locate generated operation boundary for %s", operationID)
		}
		blocks[operationID] = output[start:end]
	}
	return blocks
}

func scanDashboardControllerCalls(file, source string) []dashboardCall {
	constants := sourcePathConstants(source)
	matches := controllerCallNamePattern.FindAllStringSubmatchIndex(source, -1)
	calls := make([]dashboardCall, 0, len(matches))
	for _, match := range matches {
		nameStart, nameEnd := match[2], match[3]
		if isFunctionDeclaration(source, nameStart) {
			continue
		}
		cursor := skipSpace(source, nameEnd)
		if cursor < len(source) && source[cursor] == '<' {
			end, ok := scanAngleArguments(source, cursor)
			if !ok {
				continue
			}
			cursor = skipSpace(source, end)
		}
		if cursor >= len(source) || source[cursor] != '(' {
			continue
		}
		callEnd, ok := scanBalanced(source, cursor, '(', ')')
		if !ok {
			continue
		}
		argumentStart := skipSpace(source, cursor+1)
		if argumentStart >= callEnd {
			continue
		}
		argument, literal := readFirstArgument(source, argumentStart, callEnd)
		callee := source[nameStart:nameEnd]
		method := defaultMethod(callee)
		if methodMatch := methodOptionPattern.FindStringSubmatch(source[cursor:callEnd]); len(methodMatch) == 2 {
			method = strings.ToUpper(methodMatch[1])
		}
		call := dashboardCall{
			File:     file,
			Line:     1 + strings.Count(source[:nameStart], "\n"),
			Callee:   callee,
			Argument: compactExpression(argument),
			Method:   method,
		}
		if literal {
			call.Path = normalizeDashboardPath(argument, constants)
		}
		calls = append(calls, call)
	}
	return calls
}

func scanDirectAPIV1Literals(file, source string) []dashboardCall {
	var calls []dashboardCall
	for cursor := 0; cursor < len(source); cursor++ {
		if source[cursor] != '\'' && source[cursor] != '"' && source[cursor] != '`' {
			continue
		}
		end, ok := scanString(source, cursor)
		if !ok {
			break
		}
		raw := source[cursor+1 : end-1]
		if strings.HasPrefix(raw, "/api/v1/") {
			calls = append(calls, dashboardCall{
				File:     file,
				Line:     1 + strings.Count(source[:cursor], "\n"),
				Callee:   "websocketPath",
				Argument: compactExpression(raw),
				Method:   "GET",
				Path:     normalizeDashboardPath(raw, nil),
			})
		}
		cursor = end - 1
	}
	return calls
}

func sourcePathConstants(source string) map[string]string {
	constants := make(map[string]string)
	for _, match := range constantPathPattern.FindAllStringSubmatch(source, -1) {
		constants[match[1]] = match[2]
	}
	return constants
}

func normalizeDashboardPath(raw string, constants map[string]string) string {
	var normalized strings.Builder
	for cursor := 0; cursor < len(raw); {
		if raw[cursor] == '?' {
			break
		}
		if cursor+2 <= len(raw) && raw[cursor:cursor+2] == "${" {
			end, ok := scanBalanced(raw, cursor+1, '{', '}')
			if !ok {
				return ""
			}
			expression := strings.TrimSpace(raw[cursor+2 : end-1])
			if replacement, exists := constants[expression]; exists {
				normalized.WriteString(replacement)
			} else if isQuerySuffixExpression(expression) {
				break
			} else {
				normalized.WriteString("{param}")
			}
			cursor = end
			continue
		}
		normalized.WriteByte(raw[cursor])
		cursor++
	}
	path := normalized.String()
	if path == "" || path[0] != '/' {
		return ""
	}
	if !strings.HasPrefix(path, "/api/v1/") && path != "/api/v1" {
		path = "/api/v1" + path
	}
	return canonicalRoutePath(path)
}

func canonicalRoutePath(path string) string {
	if query := strings.IndexByte(path, '?'); query >= 0 {
		path = path[:query]
	}
	return pathParameterPattern.ReplaceAllString(path, "{param}")
}

func isQuerySuffixExpression(expression string) bool {
	compact := compactExpression(expression)
	lower := strings.ToLower(compact)
	return compact == "query" || compact == "suffix" ||
		strings.HasPrefix(lower, "buildquery(") || strings.HasPrefix(lower, "query(") ||
		strings.Contains(lower, "query(") || strings.Contains(compact, "'?") ||
		strings.Contains(compact, "\"?") || strings.Contains(compact, "`?")
}

func matchingDynamicCallRule(call dashboardCall) (int, *dynamicCallRule) {
	for index := range dynamicDashboardCallRules {
		rule := &dynamicDashboardCallRules[index]
		if call.File == rule.File && call.Callee == rule.Callee && strings.HasPrefix(call.Argument, rule.ArgumentPrefix) {
			return index, rule
		}
	}
	return -1, nil
}

func defaultMethod(callee string) string {
	if callee == "transferRequest" {
		return "POST"
	}
	return "GET"
}

func readFirstArgument(source string, start, callEnd int) (string, bool) {
	if source[start] == '\'' || source[start] == '"' || source[start] == '`' {
		end, ok := scanString(source, start)
		if !ok || end > callEnd {
			return "", false
		}
		return source[start+1 : end-1], true
	}
	end := scanExpressionEnd(source, start, callEnd)
	return strings.TrimSpace(source[start:end]), false
}

func scanExpressionEnd(source string, start, limit int) int {
	parenDepth, braceDepth, bracketDepth := 0, 0, 0
	for cursor := start; cursor < limit; cursor++ {
		switch source[cursor] {
		case '\'', '"', '`':
			end, ok := scanString(source, cursor)
			if !ok {
				return limit
			}
			cursor = end - 1
		case '(':
			parenDepth++
		case ')':
			if parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 {
				return cursor
			}
			parenDepth--
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		case ',':
			if parenDepth == 0 && braceDepth == 0 && bracketDepth == 0 {
				return cursor
			}
		}
	}
	return limit
}

func scanString(source string, start int) (int, bool) {
	quoteCharacter := source[start]
	for cursor := start + 1; cursor < len(source); cursor++ {
		if source[cursor] == '\\' {
			cursor++
			continue
		}
		if quoteCharacter == '`' && cursor+2 <= len(source) && source[cursor:cursor+2] == "${" {
			end, ok := scanBalanced(source, cursor+1, '{', '}')
			if !ok {
				return 0, false
			}
			cursor = end - 1
			continue
		}
		if source[cursor] == quoteCharacter {
			return cursor + 1, true
		}
	}
	return 0, false
}

func scanBalanced(source string, start int, opening, closing byte) (int, bool) {
	if start >= len(source) || source[start] != opening {
		return 0, false
	}
	depth := 1
	for cursor := start + 1; cursor < len(source); cursor++ {
		switch source[cursor] {
		case '\'', '"', '`':
			end, ok := scanString(source, cursor)
			if !ok {
				return 0, false
			}
			cursor = end - 1
		case '/':
			if cursor+1 < len(source) && source[cursor+1] == '/' {
				if newline := strings.IndexByte(source[cursor+2:], '\n'); newline >= 0 {
					cursor += newline + 2
				} else {
					return len(source), false
				}
			} else if cursor+1 < len(source) && source[cursor+1] == '*' {
				if end := strings.Index(source[cursor+2:], "*/"); end >= 0 {
					cursor += end + 3
				} else {
					return len(source), false
				}
			}
		default:
			if source[cursor] == opening {
				depth++
			} else if source[cursor] == closing {
				depth--
				if depth == 0 {
					return cursor + 1, true
				}
			}
		}
	}
	return 0, false
}

func scanAngleArguments(source string, start int) (int, bool) {
	depth := 0
	for cursor := start; cursor < len(source); cursor++ {
		switch source[cursor] {
		case '\'', '"', '`':
			end, ok := scanString(source, cursor)
			if !ok {
				return 0, false
			}
			cursor = end - 1
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return cursor + 1, true
			}
		}
	}
	return 0, false
}

func isFunctionDeclaration(source string, nameStart int) bool {
	start := nameStart - 32
	if start < 0 {
		start = 0
	}
	prefix := strings.TrimSpace(source[start:nameStart])
	return strings.HasSuffix(prefix, "function")
}

func skipSpace(source string, cursor int) int {
	for cursor < len(source) && unicode.IsSpace(rune(source[cursor])) {
		cursor++
	}
	return cursor
}

func compactExpression(value string) string {
	return strings.Join(strings.Fields(value), "")
}
