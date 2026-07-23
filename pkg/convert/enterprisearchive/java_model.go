package enterprisearchive

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type javaProgram struct {
	Classes  []javaClass
	Methods  map[string]javaMethod
	Unparsed []javaStatement

	// selectedVuserClass* records the class selected from the .usr action-source
	// declaration. It is intentionally kept on the parsed program so every
	// downstream lifecycle, transaction, and review-evidence pass resolves the
	// same runner class.
	selectedVuserClassFile string
	selectedVuserClassName string
}

type javaStringEnv struct {
	Vars         map[string]string
	Destinations map[string]string
	Messages     map[string]javaMessageModel
	Producers    map[string]string
}

type javaMessageModel struct {
	Body       string
	Properties map[string]string
	// BodyUnresolved is true when the source supplied a body argument that could
	// not be resolved (vs. a legitimately empty no-arg createTextMessage()).
	BodyUnresolved bool
}

var (
	javaSimpleCallRe   = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*;?\s*$`)
	javaStringAssignRe = regexp.MustCompile(`^\s*(?:final\s+)?(?:String|CharSequence|Object)?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+);\s*$`)
	javaDestAssignRe   = regexp.MustCompile(`^\s*(?:Queue|Topic|Destination|javax\.jms\.Queue|javax\.jms\.Topic|javax\.jms\.Destination)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*.+?\.create(?:Queue|Topic)\s*\((.+)\)\s*;\s*$`)
	javaDestLookupRe   = regexp.MustCompile(`^\s*(?:Destination|Queue|Topic|javax\.jms\.Destination|javax\.jms\.Queue|javax\.jms\.Topic)?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:\(\s*(?:Destination|Queue|Topic|javax\.jms\.Destination|javax\.jms\.Queue|javax\.jms\.Topic)\s*\)\s*)?.+?\.lookup\s*\((.+)\)\s*;\s*$`)
	javaMsgAssignRe    = regexp.MustCompile(`^\s*(?:(?:TextMessage|Message|javax\.jms\.TextMessage|javax\.jms\.Message)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*.+?\.createTextMessage\s*\((.*)\)\s*;\s*$`)
	javaProducerRe     = regexp.MustCompile(`^\s*(?:(?:MessageProducer|javax\.jms\.MessageProducer)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*.+?\.createProducer\s*\((.+)\)\s*;\s*$`)
	javaReplaceAllRe   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\.replace(?:All)?\s*\((.+)\)\s*$`)
	javaFileReadCallRe = regexp.MustCompile(`(?i)(?:readFile|loadFile|fileToString|getFileContents|readText|convertXMLFileToString)\s*\((.+)\)`)
	javaLREvalStringRe = regexp.MustCompile(`(?i)^\s*lr\.eval_string\s*\(\s*"(\{[A-Za-z_][A-Za-z0-9_.-]*\})"\s*\)\s*$`)
	javaLREvalExprRe   = regexp.MustCompile(`(?i)^\s*lr\.eval_string\s*\((.+)\)\s*$`)
)

func parseJavaProgram(files map[string]string) (javaProgram, []string) {
	program := javaProgram{Methods: map[string]javaMethod{}}
	var warnings []string
	totalSourceBytes := 0
	analysisBudget := javaProgramAnalysisBudget{}
	analysisLimitReached := false
	names := sortedFileNames(files)
	appendClassUnchecked := func(class javaClass) {
		program.Classes = append(program.Classes, class)
		program.Unparsed = append(program.Unparsed, class.Unparsed...)
		for methodName, method := range class.Methods {
			if _, exists := program.Methods[methodName]; !exists {
				program.Methods[methodName] = method
			}
		}
	}
	appendClass := func(class javaClass) bool {
		if reason := analysisBudget.reserveClass(class); reason != "" {
			reason = class.File + ": " + reason
			warnings = append(warnings, reason)
			placeholder := javaAnalysisLimitClass(class.File, reason)
			placeholder.Name = class.Name
			placeholder.Package = class.Package
			placeholder.Kind = class.Kind
			appendClassUnchecked(placeholder)
			return false
		}
		appendClassUnchecked(class)
		return true
	}
	for _, name := range names {
		if !strings.HasSuffix(strings.ToLower(filepath.Base(name)), ".java") {
			continue
		}
		if analysisLimitReached {
			continue
		}
		content := files[name]
		nextSourceBytes, reason := reserveJavaAnalysisSource(
			name,
			len(content),
			totalSourceBytes,
		)
		if reason != "" {
			warnings = append(warnings, reason)
			if !appendClass(javaAnalysisLimitClass(name, reason)) {
				analysisLimitReached = true
			}
			continue
		}
		totalSourceBytes = nextSourceBytes
		context := newJavaParseContext(content)
		_, hasTopLevelType := selectJavaTopLevelTypeWithScan(
			name,
			content,
			context,
		)
		cls, err := parseJavaFileWithContext(name, content, context)
		skipUndeclaredFile := false
		if err != nil {
			warnings = append(warnings, err.Error())
			if len(cls.Methods) == 0 && len(cls.Unparsed) == 0 {
				if !hasTopLevelType {
					skipUndeclaredFile = true
				}
			}
		}
		fileClasses := make([]javaClass, 0, 1)
		// Preserve a declared Actions type even when it owns no methods. It may
		// inherit lifecycle methods that this source scanner cannot yet prove;
		// retaining the actual target lets source-integrity checks fail closed
		// instead of silently retargeting a bundled base/helper class.
		if !skipUndeclaredFile {
			fileClasses = append(fileClasses, cls)
			auxiliary, auxiliaryWarnings := parseJavaAuxiliaryClassesWithContext(
				name,
				content,
				context,
			)
			warnings = append(warnings, auxiliaryWarnings...)
			fileClasses = append(fileClasses, auxiliary...)
		}
		if reason := analysisBudget.reserveContext(context); reason != "" {
			reason = name + ": " + reason
			warnings = append(warnings, reason)
			appendClassUnchecked(javaAnalysisLimitClass(name, reason))
			analysisLimitReached = true
			continue
		}
		if skipUndeclaredFile {
			continue
		}
		for _, class := range fileClasses {
			if !appendClass(class) {
				analysisLimitReached = true
				break
			}
		}
	}
	return program, warnings
}

type javaProgramAnalysisBudget struct {
	types       int
	methods     int
	statements  int
	imports     int
	braceTokens int
	scanBytes   int
}

func (budget *javaProgramAnalysisBudget) reserveContext(context *javaParseContext) string {
	if context == nil {
		return ""
	}
	braceTokens := len(context.scan.braceOffsets)
	if len(context.imports) > maxJavaAnalysisTotalImports-budget.imports {
		return "Java sources exceed aggregate retained-import limit"
	}
	if braceTokens > maxJavaAnalysisTotalBraceTokens-budget.braceTokens {
		return "Java sources exceed aggregate brace-metadata limit"
	}
	if context.structuralScanBytes > maxJavaAnalysisTotalScanBytes-budget.scanBytes {
		return "Java sources exceed aggregate structural scan-work limit"
	}
	budget.imports += len(context.imports)
	budget.braceTokens += braceTokens
	budget.scanBytes += context.structuralScanBytes
	return ""
}

func (budget *javaProgramAnalysisBudget) reserveClass(class javaClass) string {
	methods := javaRetainedMethodCount(class)
	statements := javaRetainedStatementCount(class)
	switch {
	case budget.types >= maxJavaAnalysisTypeCount:
		return "Java sources exceed aggregate retained-type limit"
	case methods > maxJavaAnalysisMethodCount-budget.methods:
		return "Java sources exceed aggregate retained-method limit"
	case statements > maxJavaAnalysisStatementCount-budget.statements:
		return "Java sources exceed aggregate retained-statement limit"
	}
	budget.types++
	budget.methods += methods
	budget.statements += statements
	return ""
}

func javaRetainedMethodCount(class javaClass) int {
	count := len(class.Constructors)
	if len(class.MethodOverloads) > 0 {
		for _, overloads := range class.MethodOverloads {
			count += len(overloads)
		}
		return count
	}
	return count + len(class.Methods)
}

func javaRetainedStatementCount(class javaClass) int {
	count := javaStatementTreeCount(class.Fields) + javaStatementTreeCount(class.Unparsed)
	for _, constructor := range class.Constructors {
		count += javaStatementTreeCount(constructor.Statements)
	}
	if len(class.MethodOverloads) > 0 {
		for _, overloads := range class.MethodOverloads {
			for _, method := range overloads {
				count += javaStatementTreeCount(method.Statements)
			}
		}
		return count
	}
	for _, method := range class.Methods {
		count += javaStatementTreeCount(method.Statements)
	}
	return count
}

func javaStatementTreeCount(statements []javaStatement) int {
	count := 0
	stack := append([]javaStatement(nil), statements...)
	for len(stack) > 0 {
		statement := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		stack = append(stack, statement.Children...)
	}
	return count
}

func sortedFileNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func javaLifecycleStatements(program javaProgram, lifecycle string) []javaStatement {
	method, ok := javaVuserLifecycleMethod(program, lifecycle)
	if !ok {
		return nil
	}
	class, ok := javaVuserClass(program)
	if !ok {
		return nil
	}
	return inlineJavaHelperStatements(method.Statements, class.Methods, map[string]bool{method.Name: true}, 0)
}

func javaLifecycleMethodNames(lifecycle string) []string {
	switch lifecycle {
	case "init":
		return []string{"init", "vuser_init", "initialize"}
	case "teardown":
		return []string{"end", "vuser_end", "teardown"}
	default:
		return []string{"action", "Action", "run"}
	}
}

func inlineJavaHelperStatements(statements []javaStatement, methods map[string]javaMethod, visited map[string]bool, depth int) []javaStatement {
	if depth >= 6 {
		return append([]javaStatement(nil), statements...)
	}
	out := make([]javaStatement, 0, len(statements))
	for _, statement := range statements {
		if statement.Kind == "block" {
			copy := statement
			copy.Children = inlineJavaHelperStatements(statement.Children, methods, visited, depth+1)
			out = append(out, copy)
			continue
		}
		if name := simpleJavaHelperCall(statement.Text); name != "" {
			if method, ok := methods[name]; ok && !visited[name] {
				visited[name] = true
				out = append(out, inlineJavaHelperStatements(method.Statements, methods, visited, depth+1)...)
				delete(visited, name)
				continue
			}
		}
		out = append(out, statement)
	}
	return out
}

func simpleJavaHelperCall(text string) string {
	text = javaNormalizeStatementText(text)
	matches := javaSimpleCallRe.FindStringSubmatch(text)
	if len(matches) != 2 {
		return ""
	}
	name := matches[1]
	switch name {
	case "action", "init", "end", "vuser_init", "vuser_end":
		return ""
	default:
		return name
	}
}

func javaNormalizeStatementText(text string) string {
	text = strings.TrimSpace(text)
	for {
		switch {
		case strings.HasPrefix(text, "//"):
			if idx := strings.IndexByte(text, '\n'); idx >= 0 {
				text = strings.TrimSpace(text[idx+1:])
				continue
			}
			return ""
		case strings.HasPrefix(text, "/*"):
			if idx := strings.Index(text, "*/"); idx >= 0 {
				text = strings.TrimSpace(text[idx+2:])
				continue
			}
			return ""
		default:
			return text
		}
	}
}

func newJavaStringEnv() javaStringEnv {
	return javaStringEnv{
		Vars:         map[string]string{},
		Destinations: map[string]string{},
		Messages:     map[string]javaMessageModel{},
		Producers:    map[string]string{},
	}
}

func (env javaStringEnv) eval(expr string, files map[string]string) (string, bool) {
	expr = strings.TrimSpace(strings.TrimSuffix(expr, ";"))
	if expr == "" {
		return "", false
	}
	if value, ok := parseJavaStringLiteral(expr); ok {
		return convertParamReferences(value), true
	}
	if value, ok := env.Vars[expr]; ok {
		return value, true
	}
	if strings.HasPrefix(expr, "SupportedProperty.") {
		return strings.TrimPrefix(expr, "SupportedProperty."), true
	}
	if strings.Contains(expr, "+") {
		parts := splitJavaConcat(expr)
		if len(parts) > 1 {
			var b strings.Builder
			for _, part := range parts {
				value, ok := env.eval(part, files)
				if !ok {
					return "", false
				}
				b.WriteString(value)
			}
			return b.String(), true
		}
	}
	if match := javaReplaceAllRe.FindStringSubmatch(expr); len(match) == 3 {
		base, ok := env.Vars[match[1]]
		if !ok {
			return "", false
		}
		args := splitJavaArgs(match[2])
		if len(args) != 2 {
			return "", false
		}
		oldValue, ok1 := env.eval(args[0], files)
		newValue, ok2 := env.eval(args[1], files)
		if !ok1 || !ok2 {
			return "", false
		}
		return strings.ReplaceAll(base, oldValue, newValue), true
	}
	if match := javaLREvalStringRe.FindStringSubmatch(expr); len(match) == 2 {
		return convertParamReferences(match[1]), true
	}
	if match := javaLREvalExprRe.FindStringSubmatch(expr); len(match) == 2 {
		return env.eval(match[1], files)
	}
	if match := javaFileReadCallRe.FindStringSubmatch(expr); len(match) == 2 {
		if name, ok := env.eval(match[1], files); ok {
			if content, found := findFile(files, name); found {
				return convertParamReferences(content), true
			}
		}
	}
	return "", false
}

func (env javaStringEnv) assignString(text string, files map[string]string) bool {
	matches := javaStringAssignRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 3 {
		return false
	}
	if value, ok := env.eval(matches[2], files); ok {
		env.Vars[matches[1]] = value
		return true
	}
	return false
}

func (env javaStringEnv) assignDestination(text string, files map[string]string) bool {
	matches := javaDestAssignRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 3 {
		return false
	}
	if value, ok := env.eval(matches[2], files); ok {
		env.Destinations[matches[1]] = value
		return true
	}
	return false
}

func (env javaStringEnv) assignLookupDestination(text string, files map[string]string) bool {
	trimmed := strings.TrimSpace(text)
	matches := javaDestLookupRe.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return false
	}
	lowerText := strings.ToLower(trimmed)
	lowerName := strings.ToLower(matches[1])
	if !strings.Contains(lowerText, "destination") && !strings.Contains(lowerText, "queue") && !strings.Contains(lowerText, "topic") &&
		!strings.Contains(lowerName, "dest") && !strings.Contains(lowerName, "queue") && !strings.Contains(lowerName, "topic") {
		return false
	}
	if value, ok := env.eval(matches[2], files); ok {
		env.Destinations[matches[1]] = value
		return true
	}
	return false
}

func (env javaStringEnv) assignProducer(text string, files map[string]string) bool {
	matches := javaProducerRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 3 {
		return false
	}
	if target, ok := env.eval(matches[2], files); ok {
		env.Producers[matches[1]] = target
		return true
	}
	if target := env.Destinations[strings.TrimSpace(matches[2])]; target != "" {
		env.Producers[matches[1]] = target
		return true
	}
	return false
}

func (env javaStringEnv) assignMessage(text string, files map[string]string) bool {
	matches := javaMsgAssignRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 3 {
		return false
	}
	body := ""
	unresolved := false
	if strings.TrimSpace(matches[2]) != "" {
		var ok bool
		body, ok = env.eval(matches[2], files)
		unresolved = !ok
	}
	env.Messages[matches[1]] = javaMessageModel{Body: body, Properties: map[string]string{}, BodyUnresolved: unresolved}
	return true
}

func parseJavaStringLiteral(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if len(expr) < 2 || expr[0] != '"' || expr[len(expr)-1] != '"' {
		return "", false
	}
	inner := expr[1 : len(expr)-1]
	replacer := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n", `\r`, "\r", `\t`, "\t")
	return replacer.Replace(inner), true
}

func splitJavaConcat(expr string) []string {
	return splitJavaTopLevel(expr, '+')
}

func splitJavaArgs(expr string) []string {
	return splitJavaTopLevel(expr, ',')
}

func splitJavaTopLevel(expr string, sep rune) []string {
	parts := make([]string, 0)
	start := 0
	depth := 0
	angleDepth := 0
	inString := false
	escaped := false
	for i, r := range expr {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '<':
			if depth == 0 && findJavaGenericAngleEnd(expr, i) > i {
				angleDepth++
			}
		case '>':
			if angleDepth > 0 {
				angleDepth--
			}
		default:
			if r == sep && depth == 0 && angleDepth == 0 {
				parts = append(parts, strings.TrimSpace(expr[start:i]))
				start = i + len(string(r))
			}
		}
	}
	parts = append(parts, strings.TrimSpace(expr[start:]))
	return parts
}

func findJavaGenericAngleEnd(expr string, open int) int {
	if open < 0 || open >= len(expr) || expr[open] != '<' {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for i := open; i < len(expr); i++ {
		ch := expr[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return i
			}
		case ',':
			if depth == 1 {
				continue
			}
		}
	}
	return -1
}
