package enterprisearchive

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
)

const (
	// Java archives are already bounded by the enterprise ZIP reader. These
	// tighter analysis limits keep a single (otherwise valid) source entry from
	// consuming unbounded CPU or parser stack while it is being reviewed.
	maxJavaAnalysisFileBytes        = 8 << 20
	maxJavaAnalysisTotalBytes       = 32 << 20
	maxJavaAnalysisTypeCount        = 4096
	maxJavaAnalysisMethodCount      = 16384
	maxJavaAnalysisStatementCount   = 65536
	maxJavaAnalysisImportCount      = 4096
	maxJavaAnalysisNesting          = 256
	maxJavaAnalysisBraceTokens      = maxJavaAnalysisStatementCount * 2
	maxJavaAnalysisScanBytes        = 96 << 20
	maxJavaAnalysisTotalImports     = maxJavaAnalysisImportCount * 4
	maxJavaAnalysisTotalBraceTokens = maxJavaAnalysisBraceTokens * 4
	maxJavaAnalysisTotalScanBytes   = maxJavaAnalysisScanBytes * 4
)

type javaSourceScan struct {
	code         string
	braceClose   map[int]int
	braceOffsets []int
	depthAfter   []int
	maxDepth     int
}

type javaParseBudget struct {
	statements int
	reason     string
}

type javaParseContext struct {
	scan                javaSourceScan
	budget              javaParseBudget
	lineIndex           sourceview.LineIndex
	metadataReady       bool
	imports             []string
	packageName         string
	declarationsReady   bool
	declarations        []javaTypeDeclaration
	structuralScanBytes int
}

func newJavaParseContext(content string) *javaParseContext {
	code := javaCodeOnly(content)
	scan := javaSourceScan{
		code:       code,
		braceClose: make(map[int]int),
	}
	stack := make([]int, 0, 16)
	depth := 0
	reason := ""
	braceTokens := 0
	for offset := 0; offset < len(code); offset++ {
		switch code[offset] {
		case '{':
			braceTokens++
			if braceTokens > maxJavaAnalysisBraceTokens {
				reason = fmt.Sprintf(
					"Java source exceeds analysis brace-metadata limit of %d tokens",
					maxJavaAnalysisBraceTokens,
				)
				break
			}
			stack = append(stack, offset)
			depth++
			if depth > scan.maxDepth {
				scan.maxDepth = depth
			}
			if depth > maxJavaAnalysisNesting {
				reason = fmt.Sprintf(
					"Java source exceeds analysis nesting limit of %d",
					maxJavaAnalysisNesting,
				)
				break
			}
			scan.braceOffsets = append(scan.braceOffsets, offset)
			scan.depthAfter = append(scan.depthAfter, depth)
		case '}':
			braceTokens++
			if braceTokens > maxJavaAnalysisBraceTokens {
				reason = fmt.Sprintf(
					"Java source exceeds analysis brace-metadata limit of %d tokens",
					maxJavaAnalysisBraceTokens,
				)
				break
			}
			if len(stack) == 0 {
				reason = "Java source contains an unmatched closing brace"
				break
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			scan.braceClose[open] = offset
			depth--
			scan.braceOffsets = append(scan.braceOffsets, offset)
			scan.depthAfter = append(scan.depthAfter, depth)
		}
		if reason != "" {
			break
		}
	}
	if reason == "" && len(stack) > 0 {
		reason = "Java source contains an unmatched opening brace"
	}
	context := &javaParseContext{
		scan:      scan,
		lineIndex: sourceview.NewLineIndex(content),
	}
	if reason != "" {
		context.fail(reason)
	}
	return context
}

func (context *javaParseContext) loadFileMetadata() {
	if context.metadataReady {
		return
	}
	context.metadataReady = true
	if !context.chargeStructuralScan(len(context.scan.code) * 2) {
		return
	}
	imports, exceeded := parseJavaImports(context.scan.code)
	if exceeded {
		context.fail(fmt.Sprintf(
			"Java source exceeds analysis import limit of %d",
			maxJavaAnalysisImportCount,
		))
		return
	}
	context.imports = imports
	if match := javaPackageDeclarationRe.FindStringSubmatch(context.scan.code); len(match) == 2 {
		context.packageName = strings.TrimSpace(match[1])
	}
}

func (context *javaParseContext) chargeStructuralScan(bytes int) bool {
	if bytes < 0 || context.structuralScanBytes > maxJavaAnalysisScanBytes-bytes {
		context.fail(fmt.Sprintf(
			"Java source exceeds structural scan-work limit of %d bytes",
			maxJavaAnalysisScanBytes,
		))
		return false
	}
	context.structuralScanBytes += bytes
	return true
}

func (context *javaParseContext) fail(reason string) {
	if context != nil && context.budget.reason == "" {
		context.budget.reason = reason
	}
}

func (scan javaSourceScan) depthAt(offset int) int {
	if offset <= 0 || len(scan.braceOffsets) == 0 {
		return 0
	}
	index := sort.SearchInts(scan.braceOffsets, offset) - 1
	if index < 0 {
		return 0
	}
	return scan.depthAfter[index]
}

func (scan javaSourceScan) depthBetween(start, end int) int {
	return scan.depthAt(end) - scan.depthAt(start)
}

func javaAnalysisLimitClass(filename, reason string) javaClass {
	return javaClass{
		File:            filename,
		Name:            strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Methods:         map[string]javaMethod{},
		MethodOverloads: map[string][]javaMethod{},
		Unparsed: []javaStatement{{
			Kind:      "unparsed",
			Text:      reason,
			Header:    reason,
			File:      filename,
			StartLine: 1,
			EndLine:   1,
		}},
	}
}

func reserveJavaAnalysisSource(
	filename string,
	sourceBytes int,
	totalSourceBytes int,
) (int, string) {
	if sourceBytes > maxJavaAnalysisFileBytes {
		return totalSourceBytes, fmt.Sprintf(
			"%s exceeds Java analysis file-size limit of %d bytes",
			filename,
			maxJavaAnalysisFileBytes,
		)
	}
	if totalSourceBytes > maxJavaAnalysisTotalBytes-sourceBytes {
		return totalSourceBytes, fmt.Sprintf(
			"Java sources exceed aggregate analysis limit of %d bytes at %s",
			maxJavaAnalysisTotalBytes,
			filename,
		)
	}
	return totalSourceBytes + sourceBytes, ""
}

type javaClass struct {
	File            string
	Name            string
	Package         string
	SuperClass      string
	Interfaces      []string
	Kind            string
	Public          bool
	Abstract        bool
	Source          string
	BodyStartLine   int
	Imports         []string
	Fields          []javaStatement
	Constructors    []javaMethod
	Methods         map[string]javaMethod
	MethodOverloads map[string][]javaMethod
	Unparsed        []javaStatement
	// Enum constants are immutable parser metadata. Retaining both ordered
	// names and a membership set prevents fields×source rescans in later
	// classification and constructor-preflight passes.
	EnumConstantNames  []string
	EnumConstantSet    map[string]bool
	EnumConstantsReady bool
}

type javaParameter struct {
	Type    string
	Name    string
	Varargs bool
	Array   bool
}

type javaMethod struct {
	Name               string
	File               string
	StartLine          int
	EndLine            int
	Visibility         string
	ReturnType         string
	Static             bool
	Abstract           bool
	Default            bool
	ParameterSignature string
	ParameterCount     int
	Parameters         []javaParameter
	Varargs            bool
	Body               string
	Statements         []javaStatement
}

type javaStatement struct {
	Kind      string
	Text      string
	Header    string
	File      string
	StartLine int
	EndLine   int
	Children  []javaStatement
	// BranchKey identifies the nearest enclosing mutually-exclusive branch
	// (if/else/switch/case) a leaf statement was flattened out of, so header
	// idioms can tell that two same-name headers came from branches that cannot
	// both run. Set transiently during traversal; empty outside conditionals.
	BranchKey string
}

var javaMethodHeaderRe = regexp.MustCompile(`(?m)(?:^|[\r\n{}])\s*(?:@[A-Za-z_][A-Za-z0-9_.]*(?:\s*\([^{};]*\))?\s*)*(?:public|private|protected)?\s*(?:(?:static|final|synchronized|native|strictfp|abstract|default)\s+)*(?:<[^{};()]+>\s*)?[A-Za-z_$][A-Za-z0-9_$.]*(?:\s*<[^{};()]+>)?(?:\s*\[\])*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*(?:throws\s+[^{;]+)?\s*\{`)

var javaBodylessMethodHeaderRe = regexp.MustCompile(`(?m)(?:^|[\r\n{}])\s*(?:@[A-Za-z_][A-Za-z0-9_.]*(?:\s*\([^{};]*\))?\s*)*(?:public|private|protected)?\s*(?:(?:static|final|synchronized|native|strictfp|abstract)\s+)*(?:<[^{};()]+>\s*)?[A-Za-z_$][A-Za-z0-9_$.]*(?:\s*<[^{};()]+>)?(?:\s*\[\])*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\([^;{}]*\)\s*(?:throws\s+[^;{}]+)?\s*;`)

var (
	javaPackageDeclarationRe = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)*)\s*;`)
	javaImportDeclarationRe  = regexp.MustCompile(`(?m)\bimport\s+((?:static\s+)?[A-Za-z_$][A-Za-z0-9_$]*(?:\s*\.\s*(?:[A-Za-z_$][A-Za-z0-9_$]*|\*))+)\s*;`)
	javaTypeDeclarationRe    = regexp.MustCompile(`(?m)\b(class|interface|enum|record)\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	javaMethodVisibilityRe   = regexp.MustCompile(`\b(public|protected|private)\b`)
	javaStaticModifierRe     = regexp.MustCompile(`\bstatic\b`)
	javaParameterNameRe      = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:\[\s*\])?\s*$`)
	javaPublicModifierRe     = regexp.MustCompile(`\bpublic\b`)
	javaAbstractModifierRe   = regexp.MustCompile(`\babstract\b`)
	javaDefaultModifierRe    = regexp.MustCompile(`\bdefault\b`)
)

func parseJavaFile(filename string, content string) (javaClass, error) {
	if _, reason := reserveJavaAnalysisSource(filename, len(content), 0); reason != "" {
		return javaAnalysisLimitClass(filename, reason), fmt.Errorf("%s", reason)
	}
	context := newJavaParseContext(content)
	return parseJavaFileWithContext(filename, content, context)
}

func parseJavaAuxiliaryClasses(filename string, content string) ([]javaClass, []string) {
	if len(content) > maxJavaAnalysisFileBytes {
		return nil, []string{fmt.Sprintf(
			"%s exceeds Java analysis file-size limit of %d bytes",
			filename,
			maxJavaAnalysisFileBytes,
		)}
	}
	return parseJavaAuxiliaryClassesWithContext(
		filename,
		content,
		newJavaParseContext(content),
	)
}

func parseJavaFileWithContext(
	filename string,
	content string,
	context *javaParseContext,
) (javaClass, error) {
	if context.budget.reason != "" {
		reason := filename + ": " + context.budget.reason
		return javaAnalysisLimitClass(filename, reason), fmt.Errorf("%s", reason)
	}
	declaration, hasDeclaration := selectJavaTopLevelTypeWithScan(
		filename,
		content,
		context,
	)
	if context.budget.reason != "" {
		reason := filename + ": " + context.budget.reason
		return javaAnalysisLimitClass(filename, reason), fmt.Errorf("%s", reason)
	}
	if !context.chargeStructuralScan(len(context.scan.code) * 2) {
		reason := filename + ": " + context.budget.reason
		return javaAnalysisLimitClass(filename, reason), fmt.Errorf("%s", reason)
	}
	methodHeaders := javaMethodHeaderRe.FindAllStringSubmatchIndex(
		context.scan.code,
		maxJavaAnalysisMethodCount+1,
	)
	bodylessHeaders := javaBodylessMethodHeaderRe.FindAllStringSubmatchIndex(
		context.scan.code,
		maxJavaAnalysisMethodCount+1,
	)
	if len(methodHeaders)+len(bodylessHeaders) > maxJavaAnalysisMethodCount {
		reason := fmt.Sprintf(
			"%s: Java source exceeds analysis method limit of %d",
			filename,
			maxJavaAnalysisMethodCount,
		)
		class := javaAnalysisLimitClass(filename, reason)
		if hasDeclaration {
			class.Name = declaration.name
			class.Kind = declaration.kind
			class.Package = ""
		}
		return class, fmt.Errorf("%s", reason)
	}
	return parseJavaClass(filename, content, context, declaration, hasDeclaration)
}

func parseJavaAuxiliaryClassesWithContext(
	filename string,
	content string,
	context *javaParseContext,
) ([]javaClass, []string) {
	if context.budget.reason != "" {
		return nil, nil
	}
	selected, hasSelected := selectJavaTopLevelTypeWithScan(filename, content, context)
	if !hasSelected || context.budget.reason != "" {
		return nil, nil
	}
	classes := make([]javaClass, 0)
	warnings := make([]string, 0)
	for _, declaration := range javaTopLevelTypeDeclarationsWithScan(content, context) {
		if declaration.open == selected.open {
			continue
		}
		class, err := parseJavaClass(filename, content, context, declaration, true)
		if err != nil && (len(class.Methods) > 0 || len(class.Unparsed) > 0) {
			warnings = append(warnings, err.Error())
		}
		// Keep constructor-only, initializer-only, and otherwise empty declared
		// types. They may still participate in an executable construction chain
		// (for example as an archived superclass or `new Helper()` target).
		classes = append(classes, class)
		if context.budget.reason != "" {
			break
		}
	}
	return classes, warnings
}

func parseJavaClass(filename string, content string, context *javaParseContext, declaration javaTypeDeclaration, hasDeclaration bool) (javaClass, error) {
	code := context.scan.code
	lineIndex := context.lineIndex
	context.loadFileMetadata()
	if context.budget.reason != "" {
		reason := filename + ": " + context.budget.reason
		class := javaAnalysisLimitClass(filename, reason)
		if hasDeclaration {
			class.Name = declaration.name
			class.Kind = declaration.kind
			class.Package = context.packageName
		}
		return class, fmt.Errorf("%s", reason)
	}
	cls := javaClass{
		File:    filename,
		Name:    strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Source:  content,
		Package: context.packageName,
		// Imports are immutable parser metadata shared by every top-level type
		// in this file. Cloning the complete slice per auxiliary class permits
		// imports×types allocation amplification without adding information.
		Imports:         context.imports,
		Methods:         map[string]javaMethod{},
		MethodOverloads: map[string][]javaMethod{},
	}
	classOpen, classClose := -1, -1
	if hasDeclaration {
		cls.Name = declaration.name
		cls.SuperClass = declaration.superClass
		cls.Interfaces = append([]string(nil), declaration.interfaces...)
		cls.Kind = declaration.kind
		cls.Public = declaration.public
		cls.Abstract = declaration.abstract
		classOpen = declaration.open
		classClose = declaration.close
		completeClassBody := classClose >= classOpen && classClose <= len(content)
		sourceEnd := classClose
		if sourceEnd < classOpen || sourceEnd > len(content) {
			sourceEnd = len(content)
		}
		if classOpen+1 <= sourceEnd {
			cls.Source = content[classOpen+1 : sourceEnd]
			cls.BodyStartLine = lineIndex.LineForOffset(classOpen + 1)
			if completeClassBody {
				if strings.EqualFold(strings.TrimSpace(cls.Kind), "enum") &&
					context.chargeStructuralScan(len(cls.Source)) {
					cls.EnumConstantNames = javaEnumConstantNamesFromSource(cls.Source)
					cls.EnumConstantSet = make(
						map[string]bool,
						len(cls.EnumConstantNames),
					)
					for _, name := range cls.EnumConstantNames {
						cls.EnumConstantSet[name] = true
					}
					cls.EnumConstantsReady = true
				}
				cls.Fields = parseJavaClassFieldsWithBudget(
					filename,
					cls.Source,
					cls.BodyStartLine,
					&context.budget,
				)
			}
		}
	}
	cls.Constructors = parseJavaConstructors(filename, content, context, cls.Name, classOpen, classClose, lineIndex)
	if context.budget.reason != "" {
		cls.Unparsed = append(cls.Unparsed, javaStatement{
			Kind:      "unparsed",
			Text:      context.budget.reason,
			Header:    context.budget.reason,
			File:      filename,
			StartLine: 1,
			EndLine:   1,
		})
		return cls, fmt.Errorf("%s: %s", filename, context.budget.reason)
	}
	scanStart, scanEnd := javaClassScanRange(classOpen, classClose, len(code))
	var matches [][]int
	if context.chargeStructuralScan((scanEnd - scanStart) * 2) {
		matches = javaRegexpMatchesInRange(
			javaMethodHeaderRe,
			code,
			scanStart,
			scanEnd,
			maxJavaAnalysisMethodCount+1,
		)
	}
	if len(matches) > maxJavaAnalysisMethodCount {
		context.fail(fmt.Sprintf(
			"Java source exceeds analysis method limit of %d",
			maxJavaAnalysisMethodCount,
		))
		matches = matches[:maxJavaAnalysisMethodCount]
	}
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		// Only methods owned directly by the selected top-level type belong on
		// its runner instance. A nested/local class may also declare action(),
		// but reflection against the outer Actions object cannot invoke it.
		if classOpen >= 0 && (match[2] <= classOpen || (classClose >= 0 && match[2] >= classClose) || context.scan.depthBetween(classOpen, match[2]) != 1) {
			continue
		}
		name := content[match[2]:match[3]]
		openBrace := strings.LastIndex(content[match[0]:match[1]], "{")
		if openBrace < 0 {
			continue
		}
		open := match[0] + openBrace
		close, ok := context.scan.braceClose[open]
		if !ok {
			close = -1
		}
		if close < 0 {
			cls.Unparsed = append(cls.Unparsed, javaStatement{
				Kind:      "unparsed",
				Text:      strings.TrimSpace(content[match[0]:]),
				Header:    strings.TrimSpace(content[match[0]:match[1]]),
				File:      filename,
				StartLine: lineIndex.LineForOffset(match[0]),
				EndLine:   lineIndex.LineForOffset(len(content)),
			})
			continue
		}
		body := content[open+1 : close]
		startLine := lineIndex.LineForOffset(match[0])
		endLine := lineIndex.LineForOffset(close)
		declarationHeader := content[match[0]:open]
		visibility, returnType, static, parameterSignature, parameters, parameterCount := parseJavaMethodDeclaration(
			declarationHeader,
			content[match[3]:open],
		)
		if cls.Kind == "interface" && visibility == "" {
			visibility = "public"
		}
		method := javaMethod{
			Name:               name,
			File:               filename,
			StartLine:          startLine,
			EndLine:            endLine,
			Visibility:         visibility,
			ReturnType:         returnType,
			Static:             static,
			Default:            javaDefaultModifierRe.MatchString(javaCodeOnly(declarationHeader)),
			ParameterSignature: parameterSignature,
			ParameterCount:     parameterCount,
			Parameters:         parameters,
			Varargs:            javaParametersAreVarargs(parameters),
			Body:               body,
		}
		method.Statements = parseJavaStatementsWithBudget(
			filename,
			body,
			startLine,
			&context.budget,
		)
		// The permissive method-header expression can backtrack and interpret
		// `public Actions()` as a return type named `public` plus a method named
		// `Actions`. Constructors were parsed explicitly above; exclude the same
		// source span here so constructor bodies are not counted or mapped twice.
		if javaMethodMatchesConstructor(method, cls.Constructors) {
			continue
		}
		cls.Methods[name] = method
		cls.MethodOverloads[name] = append(cls.MethodOverloads[name], method)
	}
	var bodylessMatches [][]int
	if context.budget.reason == "" {
		bodylessMatches = javaRegexpMatchesInRange(
			javaBodylessMethodHeaderRe,
			code,
			scanStart,
			scanEnd,
			maxJavaAnalysisMethodCount+1,
		)
	}
	if len(bodylessMatches) > maxJavaAnalysisMethodCount {
		context.fail(fmt.Sprintf(
			"Java source exceeds analysis method limit of %d",
			maxJavaAnalysisMethodCount,
		))
		bodylessMatches = bodylessMatches[:maxJavaAnalysisMethodCount]
	}
	for _, match := range bodylessMatches {
		if len(match) < 4 ||
			(classOpen >= 0 &&
				(match[2] <= classOpen ||
					(classClose >= 0 && match[2] >= classClose) ||
					context.scan.depthBetween(classOpen, match[2]) != 1)) {
			continue
		}
		name := strings.TrimSpace(content[match[2]:match[3]])
		declarationHeader := content[match[0]:match[1]]
		visibility, returnType, static, parameterSignature, parameters, parameterCount := parseJavaMethodDeclaration(
			declarationHeader,
			content[match[3]:match[1]],
		)
		if cls.Kind == "interface" && visibility == "" {
			visibility = "public"
		}
		method := javaMethod{
			Name:               name,
			File:               filename,
			StartLine:          lineIndex.LineForOffset(match[0]),
			EndLine:            lineIndex.LineForOffset(match[1]),
			Visibility:         visibility,
			ReturnType:         returnType,
			Static:             static,
			Abstract:           true,
			ParameterSignature: parameterSignature,
			ParameterCount:     parameterCount,
			Parameters:         parameters,
			Varargs:            javaParametersAreVarargs(parameters),
		}
		cls.MethodOverloads[name] = append(cls.MethodOverloads[name], method)
	}
	if context.budget.reason != "" {
		cls.Unparsed = append(cls.Unparsed, javaStatement{
			Kind:      "unparsed",
			Text:      context.budget.reason,
			Header:    context.budget.reason,
			File:      filename,
			StartLine: 1,
			EndLine:   1,
		})
	}
	if len(cls.Methods) == 0 && len(cls.Unparsed) == 0 {
		if cls.Kind == "interface" ||
			(cls.Abstract && len(cls.MethodOverloads) > 0) {
			return cls, nil
		}
		return cls, fmt.Errorf("no Java methods found in %s", filename)
	}
	if len(cls.Unparsed) > 0 {
		if context.budget.reason != "" {
			return cls, fmt.Errorf("%s: %s", filename, context.budget.reason)
		}
		return cls, fmt.Errorf("%s contains %d unparsed Java region(s) with unmatched braces", filename, len(cls.Unparsed))
	}
	return cls, nil
}

func javaClassScanRange(classOpen, classClose, sourceLength int) (int, int) {
	if classOpen >= 0 && classOpen < sourceLength &&
		classClose > classOpen && classClose <= sourceLength {
		return classOpen, classClose
	}
	return 0, sourceLength
}

func javaRegexpMatchesInRange(
	expression *regexp.Regexp,
	source string,
	start, end, limit int,
) [][]int {
	if start < 0 {
		start = 0
	}
	if end > len(source) {
		end = len(source)
	}
	if start >= end {
		return nil
	}
	matches := expression.FindAllStringSubmatchIndex(source[start:end], limit)
	for _, match := range matches {
		for index, offset := range match {
			if offset >= 0 {
				match[index] = start + offset
			}
		}
	}
	return matches
}

func parseJavaConstructors(
	filename string,
	content string,
	context *javaParseContext,
	className string,
	classOpen, classClose int,
	lineIndex sourceview.LineIndex,
) []javaMethod {
	className = strings.TrimSpace(className)
	if className == "" || classOpen < 0 {
		return nil
	}
	if context.budget.reason != "" {
		return nil
	}
	code := context.scan.code
	headerRe := regexp.MustCompile(`(?m)(?:^|[\r\n{}])\s*(?:@[A-Za-z_][A-Za-z0-9_.]*(?:\s*\([^{};]*\))?\s*)*(?:public|private|protected)?\s*(?:(?:final|synchronized|strictfp)\s+)*(?:<[^{};()]+>\s*)?(` + regexp.QuoteMeta(className) + `)\s*\([^;{}]*\)\s*(?:throws\s+[^{;]+)?\s*\{`)
	scanStart, scanEnd := javaClassScanRange(classOpen, classClose, len(code))
	if !context.chargeStructuralScan(scanEnd - scanStart) {
		return nil
	}
	matches := javaRegexpMatchesInRange(
		headerRe,
		code,
		scanStart,
		scanEnd,
		maxJavaAnalysisMethodCount+1,
	)
	if len(matches) > maxJavaAnalysisMethodCount {
		context.fail(fmt.Sprintf(
			"Java source exceeds analysis method limit of %d",
			maxJavaAnalysisMethodCount,
		))
		matches = matches[:maxJavaAnalysisMethodCount]
	}
	constructors := make([]javaMethod, 0, len(matches))
	for _, match := range matches {
		if len(match) < 4 || match[2] <= classOpen ||
			(classClose >= 0 && match[2] >= classClose) ||
			context.scan.depthBetween(classOpen, match[2]) != 1 {
			continue
		}
		openBrace := strings.LastIndex(content[match[0]:match[1]], "{")
		if openBrace < 0 {
			continue
		}
		open := match[0] + openBrace
		close, ok := context.scan.braceClose[open]
		if !ok {
			continue
		}
		visibility, returnType, static, parameterSignature, parameters, parameterCount := parseJavaMethodDeclaration(
			content[match[0]:open],
			content[match[3]:open],
		)
		constructor := javaMethod{
			Name:               className,
			File:               filename,
			StartLine:          lineIndex.LineForOffset(match[0]),
			EndLine:            lineIndex.LineForOffset(close),
			Visibility:         visibility,
			ReturnType:         returnType,
			Static:             static,
			ParameterSignature: parameterSignature,
			ParameterCount:     parameterCount,
			Parameters:         parameters,
			Varargs:            javaParametersAreVarargs(parameters),
			Body:               content[open+1 : close],
		}
		constructor.Statements = parseJavaStatementsWithBudget(
			filename,
			constructor.Body,
			constructor.StartLine,
			&context.budget,
		)
		constructors = append(constructors, constructor)
	}
	return constructors
}

func javaMethodMatchesConstructor(method javaMethod, constructors []javaMethod) bool {
	for _, constructor := range constructors {
		if method.File == constructor.File &&
			method.StartLine == constructor.StartLine &&
			method.EndLine == constructor.EndLine &&
			method.ParameterCount == constructor.ParameterCount &&
			method.ParameterSignature == constructor.ParameterSignature {
			return true
		}
	}
	return false
}

func parseJavaClassFields(filename, classBody string, baseLine int) []javaStatement {
	return parseJavaClassFieldsWithBudget(
		filename,
		classBody,
		baseLine,
		&javaParseBudget{},
	)
}

func parseJavaClassFieldsWithBudget(
	filename,
	classBody string,
	baseLine int,
	budget *javaParseBudget,
) []javaStatement {
	members := parseJavaStatementsWithBudget(filename, classBody, baseLine, budget)
	fields := make([]javaStatement, 0, len(members))
	for index := 0; index < len(members); index++ {
		member := members[index]
		// Methods, constructors, initializer blocks, and nested types are parsed
		// as blocks. A field with a brace initializer is also a block, but its
		// direct member header contains the assignment that precedes the brace.
		if member.Kind == "block" && javaClassMemberBlockIsField(member) {
			field := member
			field.Kind = "statement"
			field.Header = ""
			field.Children = nil
			// parseJavaStatements emits the terminator after a brace initializer
			// as its own direct statement. Reattach it and do not retain a bogus
			// field whose entire text is just ";".
			if index+1 < len(members) && strings.TrimSpace(members[index+1].Text) == ";" {
				field.Text = strings.TrimSpace(field.Text) + ";"
				field.EndLine = members[index+1].EndLine
				index++
			}
			fields = append(fields, field)
			continue
		}
		if member.Kind != "statement" {
			continue
		}
		text := strings.TrimSpace(member.Text)
		if text == ";" || !strings.HasSuffix(text, ";") {
			continue
		}
		fields = append(fields, member)
	}
	return fields
}

func javaClassMemberBlockIsField(member javaStatement) bool {
	if member.Kind != "block" {
		return false
	}
	// At direct class-body scope, a top-level assignment before the opening
	// brace distinguishes a field initializer from a method, nested type, or
	// static/instance initializer block. Comments and literals are masked so an
	// equals sign inside either cannot affect the classification.
	code := javaCodeOnly(member.Header)
	parenDepth := 0
	bracketDepth := 0
	for index := 0; index < len(code); index++ {
		switch code[index] {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '=':
			if parenDepth == 0 && bracketDepth == 0 {
				return true
			}
		}
	}
	return false
}

func parseJavaMethodDeclaration(header, afterName string) (string, string, bool, string, []javaParameter, int) {
	headerCode := javaCodeOnly(header)
	visibility := ""
	if match := javaMethodVisibilityRe.FindStringSubmatch(headerCode); len(match) == 2 {
		visibility = strings.ToLower(strings.TrimSpace(match[1]))
	}
	returnType := javaMethodReturnType(headerCode, afterName)
	static := javaStaticModifierRe.MatchString(headerCode)

	afterNameCode := javaCodeOnly(afterName)
	open := strings.IndexByte(afterNameCode, '(')
	if open < 0 {
		return visibility, returnType, static, "", nil, 0
	}
	close := matchingJavaParen(afterNameCode, open)
	if close < 0 {
		return visibility, returnType, static, "", nil, 0
	}
	signature := strings.TrimSpace(afterName[open+1 : close])
	parts := splitJavaParameters(signature)
	parameters := make([]javaParameter, 0, len(parts))
	for _, part := range parts {
		parameters = append(parameters, parseJavaParameter(part))
	}
	return visibility, returnType, static, strings.Join(strings.Fields(signature), " "), parameters, len(parts)
}

func javaMethodReturnType(headerCode, afterName string) string {
	nameEnd := len(headerCode) - len(afterName)
	if nameEnd <= 0 || nameEnd > len(headerCode) {
		return ""
	}
	nameStart := nameEnd
	for nameStart > 0 {
		ch := headerCode[nameStart-1]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '$' {
			nameStart--
			continue
		}
		break
	}
	fields := strings.Fields(headerCode[:nameStart])
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSpace(fields[len(fields)-1])
}

func matchingJavaParen(source string, open int) int {
	depth := 0
	for index := open; index < len(source); index++ {
		switch source[index] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func splitJavaParameters(signature string) []string {
	if strings.TrimSpace(signature) == "" {
		return nil
	}
	code := javaCodeOnly(signature)
	parts := make([]string, 0, 2)
	start := 0
	angleDepth := 0
	parenDepth := 0
	bracketDepth := 0
	for index := 0; index < len(code); index++ {
		switch code[index] {
		case '<':
			angleDepth++
		case '>':
			if angleDepth > 0 {
				angleDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ',':
			if angleDepth == 0 && parenDepth == 0 && bracketDepth == 0 {
				if part := strings.TrimSpace(signature[start:index]); part != "" {
					parts = append(parts, part)
				}
				start = index + 1
			}
		}
	}
	if part := strings.TrimSpace(signature[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func parseJavaParameter(source string) javaParameter {
	code := strings.TrimSpace(javaCodeOnly(source))
	code = stripLeadingJavaParameterDecorators(code)
	match := javaParameterNameRe.FindStringSubmatchIndex(code)
	if len(match) < 4 {
		return javaParameter{}
	}
	name := strings.TrimSpace(code[match[2]:match[3]])
	typeName := strings.TrimSpace(code[:match[0]])
	varargs := strings.HasSuffix(typeName, "...")
	typeName = strings.TrimSpace(strings.TrimSuffix(typeName, "..."))
	array := strings.Contains(typeName, "[") ||
		strings.Contains(code[match[3]:match[1]], "[")
	return javaParameter{
		Type:    javaSimpleTypeName(typeName),
		Name:    name,
		Varargs: varargs,
		Array:   array,
	}
}

func javaParametersAreVarargs(parameters []javaParameter) bool {
	return len(parameters) > 0 && parameters[len(parameters)-1].Varargs
}

func stripLeadingJavaParameterDecorators(value string) string {
	value = strings.TrimSpace(value)
	for value != "" {
		switch {
		case strings.HasPrefix(value, "final "):
			value = strings.TrimSpace(strings.TrimPrefix(value, "final "))
		case strings.HasPrefix(value, "@"):
			index := 1
			for index < len(value) && (isJavaIdentifierByte(value[index]) || value[index] == '.') {
				index++
			}
			if index < len(value) && value[index] == '(' {
				if close := matchingJavaParen(value, index); close >= 0 {
					index = close + 1
				}
			}
			value = strings.TrimSpace(value[index:])
		default:
			return value
		}
	}
	return value
}

type javaTypeDeclaration struct {
	name       string
	kind       string
	superClass string
	interfaces []string
	public     bool
	abstract   bool
	open       int
	close      int
}

func selectJavaTopLevelType(filename string, content string, code string) (javaTypeDeclaration, bool) {
	context := newJavaParseContext(content)
	// Keep compatibility with callers that already produced a masked view.
	if len(code) == len(content) {
		context.scan.code = code
	}
	return selectJavaTopLevelTypeWithScan(filename, content, context)
}

func selectJavaTopLevelTypeWithScan(
	filename string,
	content string,
	context *javaParseContext,
) (javaTypeDeclaration, bool) {
	declarations := javaTopLevelTypeDeclarationsWithScan(content, context)
	if len(declarations) == 0 {
		return javaTypeDeclaration{}, false
	}
	fileType := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	for _, declaration := range declarations {
		if strings.EqualFold(declaration.name, fileType) {
			return declaration, true
		}
	}
	for _, declaration := range declarations {
		if strings.EqualFold(declaration.name, "Actions") {
			return declaration, true
		}
	}
	return declarations[0], true
}

func javaTopLevelTypeDeclarations(content string, code string) []javaTypeDeclaration {
	context := newJavaParseContext(content)
	// Keep compatibility with callers that already produced a masked view.
	if len(code) == len(content) {
		context.scan.code = code
	}
	return javaTopLevelTypeDeclarationsWithScan(content, context)
}

func javaTopLevelTypeDeclarationsWithScan(
	content string,
	context *javaParseContext,
) []javaTypeDeclaration {
	if context.declarationsReady {
		return context.declarations
	}
	context.declarationsReady = true
	code := context.scan.code
	declarations := make([]javaTypeDeclaration, 0)
	if !context.chargeStructuralScan(len(code)) {
		context.declarations = declarations
		return declarations
	}
	matches := javaTypeDeclarationRe.FindAllStringSubmatchIndex(
		code,
		maxJavaAnalysisTypeCount+1,
	)
	if len(matches) > maxJavaAnalysisTypeCount {
		context.fail(fmt.Sprintf(
			"Java source exceeds analysis type-declaration limit of %d",
			maxJavaAnalysisTypeCount,
		))
		matches = matches[:maxJavaAnalysisTypeCount]
	}
	for _, match := range matches {
		if len(match) < 6 || context.scan.depthAt(match[4]) != 0 {
			continue
		}
		relativeOpen := strings.IndexByte(code[match[1]:], '{')
		if relativeOpen < 0 {
			continue
		}
		open := match[1] + relativeOpen
		headerStart := javaTypeDeclarationHeaderStart(code, match[0])
		header := code[headerStart:open]
		kind := strings.ToLower(strings.TrimSpace(content[match[2]:match[3]]))
		superClass := ""
		interfaces := make([]string, 0)
		extended := javaTypeDeclarationClause(header, "extends", "implements", "permits")
		if kind == "interface" {
			interfaces = append(interfaces, extended...)
		} else if len(extended) > 0 {
			superClass = extended[0]
		}
		interfaces = append(
			interfaces,
			javaTypeDeclarationClause(header, "implements", "permits")...,
		)
		close, ok := context.scan.braceClose[open]
		if !ok {
			close = -1
		}
		declarations = append(declarations, javaTypeDeclaration{
			name:       strings.TrimSpace(content[match[4]:match[5]]),
			kind:       kind,
			superClass: superClass,
			interfaces: interfaces,
			public:     javaPublicModifierRe.MatchString(header),
			abstract:   javaAbstractModifierRe.MatchString(header),
			open:       open,
			close:      close,
		})
	}
	context.declarations = declarations
	return declarations
}

func javaTypeDeclarationClause(
	header string,
	keyword string,
	stopKeywords ...string,
) []string {
	code := javaCodeOnly(header)
	start := javaTopLevelKeywordIndex(code, keyword, 0)
	if start < 0 {
		return nil
	}
	start += len(keyword)
	end := len(code)
	for _, stopKeyword := range stopKeywords {
		if stop := javaTopLevelKeywordIndex(code, stopKeyword, start); stop >= 0 && stop < end {
			end = stop
		}
	}
	types := make([]string, 0, 2)
	for _, typeName := range splitJavaParameters(code[start:end]) {
		typeName = strings.TrimSpace(javaSimpleTypeName(typeName))
		if typeName != "" {
			types = append(types, typeName)
		}
	}
	return types
}

func javaTopLevelKeywordIndex(source string, keyword string, offset int) int {
	angleDepth := 0
	if offset < 0 {
		offset = 0
	}
	for index := offset; index+len(keyword) <= len(source); index++ {
		switch source[index] {
		case '<':
			angleDepth++
			continue
		case '>':
			if angleDepth > 0 {
				angleDepth--
			}
			continue
		}
		if angleDepth != 0 ||
			!strings.EqualFold(source[index:index+len(keyword)], keyword) {
			continue
		}
		beforeIdentifier := index > 0 && isJavaIdentifierByte(source[index-1])
		after := index + len(keyword)
		afterIdentifier := after < len(source) && isJavaIdentifierByte(source[after])
		if !beforeIdentifier && !afterIdentifier {
			return index
		}
	}
	return -1
}

func javaTypeDeclarationHeaderStart(code string, declarationStart int) int {
	if declarationStart <= 0 {
		return 0
	}
	prefix := code[:declarationStart]
	start := -1
	for _, delimiter := range []byte{';', '{', '}'} {
		if index := strings.LastIndexByte(prefix, delimiter); index > start {
			start = index
		}
	}
	return start + 1
}

func javaBraceDepthAt(code string, start int, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(code) {
		end = len(code)
	}
	depth := 0
	for i := start; i < end; i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}

// javaCodeOnly replaces Java comments and literals with spaces while preserving
// byte offsets and newlines. Scanners can safely run regular expressions over
// the result and use the resulting offsets against the original source without
// treating JavaDoc prose, string data, or text blocks as declarations/calls.
func javaCodeOnly(source string) string {
	masked := []byte(source)
	const (
		javaLexCode = iota
		javaLexLineComment
		javaLexBlockComment
		javaLexString
		javaLexChar
		javaLexTextBlock
	)
	state := javaLexCode
	escaped := false
	mask := func(index int) {
		if masked[index] != '\n' && masked[index] != '\r' {
			masked[index] = ' '
		}
	}
	for i := 0; i < len(source); i++ {
		ch := source[i]
		next := byte(0)
		if i+1 < len(source) {
			next = source[i+1]
		}
		switch state {
		case javaLexLineComment:
			mask(i)
			if ch == '\n' || ch == '\r' {
				state = javaLexCode
			}
		case javaLexBlockComment:
			mask(i)
			if ch == '*' && next == '/' {
				mask(i + 1)
				i++
				state = javaLexCode
			}
		case javaLexString, javaLexChar:
			mask(i)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if (state == javaLexString && ch == '"') || (state == javaLexChar && ch == '\'') {
				state = javaLexCode
			}
		case javaLexTextBlock:
			mask(i)
			if ch == '"' && i+2 < len(source) && source[i+1] == '"' && source[i+2] == '"' && !javaByteIsEscaped(source, i) {
				mask(i + 1)
				mask(i + 2)
				i += 2
				state = javaLexCode
			}
		default:
			switch {
			case ch == '/' && next == '/':
				mask(i)
				mask(i + 1)
				i++
				state = javaLexLineComment
			case ch == '/' && next == '*':
				mask(i)
				mask(i + 1)
				i++
				state = javaLexBlockComment
			case ch == '"' && i+2 < len(source) && source[i+1] == '"' && source[i+2] == '"':
				mask(i)
				mask(i + 1)
				mask(i + 2)
				i += 2
				state = javaLexTextBlock
			case ch == '"':
				mask(i)
				state = javaLexString
			case ch == '\'':
				mask(i)
				state = javaLexChar
			}
		}
	}
	return string(masked)
}

func javaByteIsEscaped(source string, index int) bool {
	backslashes := 0
	for index--; index >= 0 && source[index] == '\\'; index-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func parseJavaImports(content string) ([]string, bool) {
	matches := javaImportDeclarationRe.FindAllStringSubmatch(
		content,
		maxJavaAnalysisImportCount+1,
	)
	if len(matches) > maxJavaAnalysisImportCount {
		return nil, true
	}
	imports := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		imported := strings.Join(strings.Fields(strings.TrimSpace(match[1])), " ")
		imported = strings.ReplaceAll(imported, " . ", ".")
		imported = strings.ReplaceAll(imported, ". ", ".")
		imported = strings.ReplaceAll(imported, " .", ".")
		if imported != "" {
			if _, exists := seen[imported]; exists {
				continue
			}
			seen[imported] = struct{}{}
			imports = append(imports, imported)
		}
	}
	return imports, false
}

func parseJavaStatements(filename string, body string, baseLine int) []javaStatement {
	budget := &javaParseBudget{}
	return parseJavaStatementsWithBudget(filename, body, baseLine, budget)
}

func parseJavaStatementsWithBudget(
	filename string,
	body string,
	baseLine int,
	budget *javaParseBudget,
) []javaStatement {
	if budget == nil {
		budget = &javaParseBudget{}
	}
	context := newJavaParseContext(body)
	if context.budget.reason != "" && budget.reason == "" {
		budget.reason = context.budget.reason
	}
	return parseJavaStatementRange(
		filename,
		body,
		context.scan,
		context.lineIndex,
		0,
		len(body),
		baseLine,
		0,
		budget,
	)
}

func parseJavaStatementRange(
	filename string,
	body string,
	scan javaSourceScan,
	lineIndex sourceview.LineIndex,
	rangeStart, rangeEnd int,
	baseLine, nesting int,
	budget *javaParseBudget,
) []javaStatement {
	statements := make([]javaStatement, 0)
	if budget.reason != "" {
		return statements
	}
	if nesting > maxJavaAnalysisNesting {
		budget.reason = fmt.Sprintf(
			"Java source exceeds analysis nesting limit of %d",
			maxJavaAnalysisNesting,
		)
		return statements
	}
	start := rangeStart
	parenDepth := 0
	bracketDepth := 0

	for i := rangeStart; i < rangeEnd; i++ {
		if budget.reason != "" {
			break
		}
		ch := scan.code[i]
		switch ch {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ';':
			if parenDepth == 0 && bracketDepth == 0 {
				appendJavaStatementWithBudget(
					&statements,
					filename,
					body,
					lineIndex,
					start,
					i+1,
					baseLine,
					"",
					nil,
					budget,
				)
				start = i + 1
			}
		case '{':
			if parenDepth == 0 && bracketDepth == 0 {
				close, ok := scan.braceClose[i]
				if !ok || close >= rangeEnd {
					continue
				}
				header := strings.TrimSpace(body[start:i])
				children := parseJavaStatementRange(
					filename,
					body,
					scan,
					lineIndex,
					i+1,
					close,
					baseLine,
					nesting+1,
					budget,
				)
				appendJavaStatementWithBudget(
					&statements,
					filename,
					body,
					lineIndex,
					start,
					close+1,
					baseLine,
					header,
					children,
					budget,
				)
				i = close
				start = close + 1
			}
		}
	}
	appendJavaStatementWithBudget(
		&statements,
		filename,
		body,
		lineIndex,
		start,
		rangeEnd,
		baseLine,
		"",
		nil,
		budget,
	)
	return statements
}

func appendJavaStatementWithBudget(
	statements *[]javaStatement,
	filename string,
	body string,
	lineIndex sourceview.LineIndex,
	start int,
	end int,
	baseLine int,
	header string,
	children []javaStatement,
	budget *javaParseBudget,
) {
	if budget.reason != "" {
		return
	}
	before := len(*statements)
	appendJavaStatement(
		statements,
		filename,
		body,
		lineIndex,
		start,
		end,
		baseLine,
		header,
		children,
	)
	if len(*statements) == before {
		return
	}
	budget.statements++
	if budget.statements > maxJavaAnalysisStatementCount {
		budget.reason = fmt.Sprintf(
			"Java source exceeds analysis statement limit of %d",
			maxJavaAnalysisStatementCount,
		)
	}
}

func appendJavaStatement(statements *[]javaStatement, filename string, body string, lineIndex sourceview.LineIndex, start int, end int, baseLine int, header string, children []javaStatement) {
	if start < 0 {
		start = 0
	}
	if end > len(body) {
		end = len(body)
	}
	raw := body[start:end]
	trimmedLeft := strings.TrimLeft(raw, " \t\r\n\f")
	start += len(raw) - len(trimmedLeft)
	raw = trimmedLeft
	trimmed := strings.TrimRight(raw, " \t\r\n\f")
	end = start + len(trimmed)
	text := strings.TrimSpace(trimmed)
	if text == "" {
		return
	}
	kind := "statement"
	if header != "" {
		kind = "block"
	}
	*statements = append(*statements, javaStatement{
		Kind:      kind,
		Text:      text,
		Header:    header,
		File:      filename,
		StartLine: baseLine + lineIndex.LineForOffset(start) - 1,
		EndLine:   baseLine + lineIndex.LineForOffset(end) - 1,
		Children:  children,
	})
}

func findMatchingJavaBrace(content string, open int) int {
	if open < 0 || open >= len(content) || content[open] != '{' {
		return -1
	}
	code := javaCodeOnly(content)
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
