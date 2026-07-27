package enterprisearchive

import (
	"fmt"
	"sort"
	"strings"
)

const (
	maxCTransactionDesignatorDepth = 64
	maxCTransactionFailureEvidence = 64
	maxCTransactionAliasTokens     = 2*maxCTransactionDesignatorDepth + 4
)

type cTransactionTokenKind uint8

const (
	cTransactionIdentifier cTransactionTokenKind = iota
	cTransactionLiteral
	cTransactionPunctuation
)

type cTransactionToken struct {
	text      string
	kind      cTransactionTokenKind
	start     int
	end       int
	line      int
	column    int
	endLine   int
	endColumn int
}

type cTransactionAliasAssignment struct {
	rhs             string
	invalid         bool
	directlyRelated bool
}

type cTransactionIdentity struct {
	target    string
	related   bool
	ambiguous bool
}

type cTransactionOccurrence struct {
	name       string
	sourceName string
	start      int
	end        int
	line       int
	endLine    int
	column     int
	argCount   int
	unresolved bool
	unmatched  bool
}

// cTransactionSourceAnalysis is built once per source file. The token stream,
// parenthesis index, alias graph, and call occurrence index are all bounded by
// the source size; no candidate rescans the remaining file.
type cTransactionSourceAnalysis struct {
	tokens       []cTransactionToken
	parenMatch   []int
	parenCommas  []int
	parenContent []bool
	identities   map[string]cTransactionIdentity
	occurrences  []cTransactionOccurrence
	directCalls  map[string]sourceASTDirectCall
	failures     []CSourceParseFailure
}

func analyzeCTransactionSource(filename string, content string) *cTransactionSourceAnalysis {
	tokens := lexCTransactionTokens(content, true)
	match, commas, contentFlags := indexCTransactionParentheses(tokens)
	assignments := collectCTransactionMacroAssignments(content)
	collectCTransactionVariableAssignments(tokens, match, assignments)
	identities := resolveCTransactionAliases(assignments)

	analysis := &cTransactionSourceAnalysis{
		tokens:       tokens,
		parenMatch:   match,
		parenCommas:  commas,
		parenContent: contentFlags,
		identities:   identities,
		directCalls:  make(map[string]sourceASTDirectCall),
	}
	analysis.indexCalls(filename)
	return analysis
}

func cTransactionCallLocationKey(line int, column int) string {
	return fmt.Sprintf("%d:%d", line, column)
}

func cTransactionDirectCallKey(name string, line int, column int) string {
	return strings.ToLower(strings.TrimSpace(name)) + "\x00" +
		cTransactionCallLocationKey(line, column)
}

func (analysis *cTransactionSourceAnalysis) addFailure(filename string, line int, kind string) {
	if analysis == nil || strings.TrimSpace(kind) == "" {
		return
	}
	for _, failure := range analysis.failures {
		if failure.Filename == filename && failure.LineNumber == line && failure.Kind == kind {
			return
		}
	}
	if len(analysis.failures) >= maxCTransactionFailureEvidence {
		for _, failure := range analysis.failures {
			if failure.Kind == "analysis_evidence_limit" {
				return
			}
		}
		analysis.failures = append(analysis.failures, CSourceParseFailure{
			Filename: filename,
			Kind:     "analysis_evidence_limit",
		})
		return
	}
	analysis.failures = append(analysis.failures, CSourceParseFailure{
		Filename:   filename,
		LineNumber: line,
		Kind:       kind,
	})
}

func (analysis *cTransactionSourceAnalysis) identityFor(name string) cTransactionIdentity {
	name = strings.TrimSpace(name)
	if nativeSourceTransactionReviewUnsupported(name) {
		return cTransactionIdentity{target: name, related: true}
	}
	if identity, ok := analysis.identities[name]; ok {
		return identity
	}
	if unresolvedNativeTransactionIdentity(name) {
		return cTransactionIdentity{related: true, ambiguous: true}
	}
	return cTransactionIdentity{}
}

func unresolvedNativeTransactionIdentity(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	if def, ok := lookupSourceFunction(name); ok && def.Family == "transaction" {
		return false
	}
	return strings.HasPrefix(name, "lr_get_transaction_") ||
		strings.HasPrefix(name, "lr_get_trans_instance_") ||
		strings.HasPrefix(name, "lr_start_transaction_instance") ||
		strings.HasPrefix(name, "lr_end_transaction_instance") ||
		strings.HasPrefix(name, "lr_stop_transaction_instance") ||
		strings.HasPrefix(name, "lr_resume_transaction_instance") ||
		strings.HasPrefix(name, "lr_set_transaction_instance_") ||
		strings.HasPrefix(name, "lr_start_cross_vuser_transaction") ||
		strings.HasPrefix(name, "lr_end_cross_vuser_transaction") ||
		name == "lr_wasted_time"
}

func (analysis *cTransactionSourceAnalysis) indexCalls(filename string) {
	if analysis == nil {
		return
	}
	for index, token := range analysis.tokens {
		if token.kind != cTransactionIdentifier ||
			cTransactionCallHasMemberReceiver(analysis.tokens, index) {
			continue
		}
		left, open, exceeded, ok := cTransactionCallOpen(
			analysis.tokens,
			analysis.parenMatch,
			index,
		)
		identity := analysis.identityFor(token.text)
		if exceeded {
			if identity.related {
				analysis.addFailure(filename, token.line, "transaction_designator_budget_exceeded")
			}
			continue
		}
		if !ok || cTransactionCallLooksLikeDeclaration(analysis.tokens, left) {
			continue
		}
		close := analysis.parenMatch[open]
		callName := token.text
		if identity.target != "" {
			callName = identity.target
		}
		if close >= 0 {
			span := sourceASTDirectCall{
				name:   callName,
				line:   analysis.tokens[left].line,
				column: analysis.tokens[left].column,
				start:  analysis.tokens[left].start,
				end:    analysis.tokens[close].end,
			}
			analysis.directCalls[cTransactionDirectCallKey(callName, span.line, span.column)] = span
			if callName != token.text {
				analysis.directCalls[cTransactionDirectCallKey(token.text, span.line, span.column)] = span
			}
		}
		if !identity.related {
			continue
		}
		occurrence := cTransactionOccurrence{
			name:       identity.target,
			sourceName: token.text,
			start:      analysis.tokens[left].start,
			line:       analysis.tokens[left].line,
			column:     analysis.tokens[left].column,
			unresolved: identity.ambiguous || identity.target == "",
		}
		if close < 0 {
			occurrence.unmatched = true
			analysis.addFailure(filename, occurrence.line, "unmatched_transaction_call")
			analysis.occurrences = append(analysis.occurrences, occurrence)
			continue
		}
		occurrence.end = analysis.tokens[close].end
		occurrence.endLine = analysis.tokens[close].endLine
		if analysis.parenContent[open] {
			occurrence.argCount = analysis.parenCommas[open] + 1
		}
		if occurrence.unresolved {
			analysis.addFailure(filename, occurrence.line, "unresolved_transaction_identity")
		}
		analysis.occurrences = append(analysis.occurrences, occurrence)
	}
}

func (analysis *cTransactionSourceAnalysis) directCall(
	name string,
	line int,
	column int,
) (sourceASTDirectCall, bool) {
	if analysis == nil {
		return sourceASTDirectCall{}, false
	}
	call, ok := analysis.directCalls[cTransactionDirectCallKey(name, line, column)]
	return call, ok
}

// scanUnsupportedSourceTransactionCalls is retained for focused parser tests.
// ParseActionFile uses the WithAnalysis form so tokenization never runs twice.
func scanUnsupportedSourceTransactionCalls(filename string, content string) []ParsedFunction {
	return scanUnsupportedSourceTransactionCallsWithAnalysis(
		filename,
		content,
		analyzeCTransactionSource(filename, content),
	)
}

func scanUnsupportedSourceTransactionCallsWithAnalysis(
	filename string,
	content string,
	analysis *cTransactionSourceAnalysis,
) []ParsedFunction {
	if analysis == nil {
		return nil
	}
	lines := strings.Split(content, "\n")
	out := make([]ParsedFunction, 0, len(analysis.occurrences))
	for _, occurrence := range analysis.occurrences {
		if occurrence.unresolved || occurrence.unmatched || occurrence.name == "" {
			continue
		}
		fn := newReviewOnlyTransactionParsedFunction(
			occurrence.name,
			filename,
			occurrence.line,
			occurrence.endLine,
			occurrence.column,
			occurrence.start,
			occurrence.end,
			occurrence.argCount,
			rawSourceBlock(lines, occurrence.line, occurrence.endLine),
		)
		out = append(out, fn)
	}
	return orderAndMarkSourceFunctions(out)
}

func newReviewOnlyTransactionParsedFunction(
	name string,
	filename string,
	startLine int,
	endLine int,
	column int,
	startOffset int,
	endOffset int,
	argCount int,
	rawBlock string,
) ParsedFunction {
	if endLine <= 0 {
		endLine = startLine
	}
	fn := ParsedFunction{
		Name:            name,
		Filename:        filename,
		LineNumber:      startLine,
		EndLine:         endLine,
		RawBlock:        rawBlock,
		Args:            make(map[string]string, argCount),
		CallArgs:        make([]ParsedArgument, argCount),
		RawArgs:         make([]string, argCount),
		sourceColumn:    column,
		sourceOffset:    startOffset,
		sourceEndOffset: endOffset,
	}
	for index := 0; index < argCount; index++ {
		fn.Args[fmt.Sprintf("arg%d", index+1)] = "[REDACTED]"
		fn.RawArgs[index] = "[REDACTED]"
		fn.CallArgs[index] = ParsedArgument{
			Kind:  "expression",
			Value: "[REDACTED]",
		}
	}
	return fn
}

func mergeRecoveredSourceTransactionCalls(
	parsed *ParsedAction,
	recovered []ParsedFunction,
	analysis *cTransactionSourceAnalysis,
) {
	if parsed == nil {
		return
	}
	if analysis != nil {
		applyAnalyzedSourceCallSpans(parsed.Functions, analysis)
	}
	recoveredSpans := make(map[string]struct{}, len(recovered))
	for _, fn := range recovered {
		if fn.sourceEndOffset > fn.sourceOffset {
			recoveredSpans[fmt.Sprintf("%d:%d", fn.sourceOffset, fn.sourceEndOffset)] = struct{}{}
		}
	}
	filtered := parsed.Functions[:0]
	for _, fn := range parsed.Functions {
		if _, replaced := recoveredSpans[fmt.Sprintf("%d:%d", fn.sourceOffset, fn.sourceEndOffset)]; replaced &&
			!nativeSourceTransactionReviewUnsupported(fn.Name) {
			continue
		}
		filtered = append(filtered, fn)
	}
	parsed.Functions = filtered

	seen := make(map[string]struct{}, len(parsed.Functions)+len(recovered))
	for _, fn := range parsed.Functions {
		seen[parsedSourceCallOccurrenceKey(fn)] = struct{}{}
	}
	for _, fn := range recovered {
		key := parsedSourceCallOccurrenceKey(fn)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		parsed.Functions = append(parsed.Functions, fn)
	}
	parsed.Functions = orderAndMarkSourceFunctions(parsed.Functions)
	if len(recovered) > 0 || len(parsed.ParseFailures) > 0 {
		// Without a complete AST, exact branch/loop ownership cannot be proven.
		// Fail closed for every other call in the malformed source file.
		for index := range parsed.Functions {
			if !nativeSourceTransactionReviewUnsupported(parsed.Functions[index].Name) {
				parsed.Functions[index].dependsOnUnsupportedTransactionFlow = true
			}
		}
	}
}

func applyAnalyzedSourceCallSpans(functions []ParsedFunction, analysis *cTransactionSourceAnalysis) {
	if analysis == nil {
		return
	}
	for index := range functions {
		fn := &functions[index]
		if call, ok := analysis.directCall(fn.Name, fn.LineNumber, fn.sourceColumn); ok {
			fn.sourceOffset = call.start
			fn.sourceEndOffset = call.end
		}
	}
}

func lexCTransactionTokens(content string, skipPreprocessor bool) []cTransactionToken {
	tokens := make([]cTransactionToken, 0, len(content)/4)
	offset := 0
	line := 1
	column := 1
	lineWhitespaceOnly := true
	advance := func() {
		if content[offset] == '\n' {
			line++
			column = 1
			lineWhitespaceOnly = true
		} else {
			if content[offset] != ' ' && content[offset] != '\t' && content[offset] != '\r' {
				lineWhitespaceOnly = false
			}
			column++
		}
		offset++
	}
	advanceUntil := func(end int) {
		for offset < end {
			advance()
		}
	}

	for offset < len(content) {
		ch := content[offset]
		if skipPreprocessor && lineWhitespaceOnly && ch == '#' {
			for {
				end := offset
				for end < len(content) && content[end] != '\n' {
					end++
				}
				last := end - 1
				for last >= offset && (content[last] == ' ' || content[last] == '\t' || content[last] == '\r') {
					last--
				}
				continued := last >= offset && content[last] == '\\'
				if end < len(content) {
					end++
				}
				advanceUntil(end)
				if !continued || offset >= len(content) {
					break
				}
			}
			continue
		}
		if ch == '/' && offset+1 < len(content) && content[offset+1] == '/' {
			for {
				end := offset
				for end < len(content) && content[end] != '\n' {
					end++
				}
				last := end - 1
				for last >= offset && (content[last] == ' ' || content[last] == '\t' || content[last] == '\r') {
					last--
				}
				continued := last >= offset && content[last] == '\\'
				if end < len(content) {
					end++
				}
				advanceUntil(end)
				if !continued || offset >= len(content) {
					break
				}
			}
			continue
		}
		if ch == '/' && offset+1 < len(content) && content[offset+1] == '*' {
			end := offset + 2
			for end+1 < len(content) && !(content[end] == '*' && content[end+1] == '/') {
				end++
			}
			if end+1 < len(content) {
				end += 2
			} else {
				end = len(content)
			}
			advanceUntil(end)
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			advance()
			continue
		}

		start, startLine, startColumn := offset, line, column
		if ch == '"' || ch == '\'' {
			quote := ch
			advance()
			escaped := false
			for offset < len(content) {
				current := content[offset]
				advance()
				if escaped {
					escaped = false
					continue
				}
				if current == '\\' {
					escaped = true
					continue
				}
				if current == quote {
					break
				}
			}
			tokens = append(tokens, cTransactionToken{
				text:      "<literal>",
				kind:      cTransactionLiteral,
				start:     start,
				end:       offset,
				line:      startLine,
				column:    startColumn,
				endLine:   line,
				endColumn: column,
			})
			continue
		}
		if isCIdentifierStart(ch) {
			advance()
			for offset < len(content) && isCIdentifierContinue(content[offset]) {
				advance()
			}
			tokens = append(tokens, cTransactionToken{
				text:      content[start:offset],
				kind:      cTransactionIdentifier,
				start:     start,
				end:       offset,
				line:      startLine,
				column:    startColumn,
				endLine:   line,
				endColumn: column,
			})
			continue
		}
		if ch >= '0' && ch <= '9' {
			advance()
			for offset < len(content) {
				current := content[offset]
				if !isCIdentifierContinue(current) && current != '.' {
					break
				}
				advance()
			}
			tokens = append(tokens, cTransactionToken{
				text:      "<literal>",
				kind:      cTransactionLiteral,
				start:     start,
				end:       offset,
				line:      startLine,
				column:    startColumn,
				endLine:   line,
				endColumn: column,
			})
			continue
		}
		advance()
		tokens = append(tokens, cTransactionToken{
			text:      content[start:offset],
			kind:      cTransactionPunctuation,
			start:     start,
			end:       offset,
			line:      startLine,
			column:    startColumn,
			endLine:   line,
			endColumn: column,
		})
	}
	return tokens
}

func indexCTransactionParentheses(tokens []cTransactionToken) (match []int, commas []int, content []bool) {
	match = make([]int, len(tokens))
	commas = make([]int, len(tokens))
	content = make([]bool, len(tokens))
	for index := range match {
		match[index] = -1
	}
	stack := make([]int, 0)
	for index, token := range tokens {
		switch token.text {
		case "(":
			if len(stack) > 0 {
				content[stack[len(stack)-1]] = true
			}
			stack = append(stack, index)
		case ")":
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			match[open] = index
			match[index] = open
		case ",":
			if len(stack) > 0 {
				commas[stack[len(stack)-1]]++
			}
		default:
			if len(stack) > 0 {
				content[stack[len(stack)-1]] = true
			}
		}
	}
	return match, commas, content
}

func cTransactionCallOpen(
	tokens []cTransactionToken,
	match []int,
	identifier int,
) (left int, open int, exceeded bool, ok bool) {
	left, right := identifier, identifier
	for steps := 0; ; steps++ {
		if steps >= maxCTransactionDesignatorDepth {
			return left, 0, true, false
		}
		changed := false
		for left > 0 && (tokens[left-1].text == "&" || tokens[left-1].text == "*") {
			left--
			changed = true
		}
		if left > 0 && right+1 < len(tokens) &&
			tokens[left-1].text == "(" &&
			tokens[right+1].text == ")" &&
			match[left-1] == right+1 {
			left--
			right++
			changed = true
		}
		if !changed {
			break
		}
	}
	if right+1 >= len(tokens) || tokens[right+1].text != "(" {
		return left, 0, false, false
	}
	return left, right + 1, false, true
}

func unwrapCTransactionDesignator(
	tokens []cTransactionToken,
	match []int,
	start int,
	end int,
) (symbol string, exceeded bool, ok bool) {
	for steps := 0; start < end; steps++ {
		if steps >= maxCTransactionDesignatorDepth {
			return "", true, false
		}
		changed := false
		for start < end && (tokens[start].text == "&" || tokens[start].text == "*") {
			start++
			changed = true
		}
		if end-start >= 2 && tokens[start].text == "(" &&
			tokens[end-1].text == ")" && match[start] == end-1 {
			start++
			end--
			changed = true
		}
		if !changed {
			break
		}
	}
	if end-start != 1 || tokens[start].kind != cTransactionIdentifier {
		return "", false, false
	}
	return tokens[start].text, false, true
}

func cTransactionCallHasMemberReceiver(tokens []cTransactionToken, identifier int) bool {
	return identifier > 0 && (tokens[identifier-1].text == "." ||
		(tokens[identifier-1].text == ">" && identifier > 1 && tokens[identifier-2].text == "-"))
}

func cTransactionCallLooksLikeDeclaration(tokens []cTransactionToken, left int) bool {
	if left <= 0 {
		return false
	}
	previous := tokens[left-1]
	if previous.kind != cTransactionIdentifier {
		return false
	}
	switch strings.ToLower(previous.text) {
	case "return", "if", "while", "switch", "sizeof":
		return false
	default:
		return true
	}
}

func collectCTransactionMacroAssignments(content string) map[string][]cTransactionAliasAssignment {
	assignments := make(map[string][]cTransactionAliasAssignment)
	for offset := 0; offset < len(content); {
		lineStart := offset
		lineEnd := offset
		for lineEnd < len(content) && content[lineEnd] != '\n' {
			lineEnd++
		}
		first := lineStart
		for first < lineEnd && (content[first] == ' ' || content[first] == '\t' || content[first] == '\r') {
			first++
		}
		if first >= lineEnd || content[first] != '#' {
			offset = lineEnd
			if offset < len(content) {
				offset++
			}
			continue
		}
		logicalEnd := lineEnd
		for {
			last := logicalEnd - 1
			for last >= first && (content[last] == ' ' || content[last] == '\t' || content[last] == '\r') {
				last--
			}
			if last < first || content[last] != '\\' || logicalEnd >= len(content) {
				break
			}
			nextEnd := logicalEnd + 1
			for nextEnd < len(content) && content[nextEnd] != '\n' {
				nextEnd++
			}
			logicalEnd = nextEnd
		}
		fragment := content[first+1 : logicalEnd]
		tokens := lexCTransactionTokens(fragment, false)
		parseCTransactionMacroDirective(tokens, assignments)
		offset = logicalEnd
		if offset < len(content) {
			offset++
		}
	}
	return assignments
}

func parseCTransactionMacroDirective(
	tokens []cTransactionToken,
	assignments map[string][]cTransactionAliasAssignment,
) {
	if len(tokens) < 2 || tokens[0].kind != cTransactionIdentifier ||
		tokens[1].kind != cTransactionIdentifier {
		return
	}
	directive := strings.ToLower(tokens[0].text)
	name := tokens[1].text
	if directive == "undef" {
		assignments[name] = append(assignments[name], cTransactionAliasAssignment{invalid: true})
		return
	}
	if directive != "define" {
		return
	}
	replacementStart := 2
	parameters := make(map[string]struct{})
	if replacementStart < len(tokens) && tokens[replacementStart].text == "(" &&
		tokens[replacementStart].start == tokens[1].end {
		match, _, _ := indexCTransactionParentheses(tokens)
		close := match[replacementStart]
		if close < 0 {
			assignments[name] = append(assignments[name], cTransactionAliasAssignment{
				invalid:         true,
				directlyRelated: true,
			})
			return
		}
		for index := replacementStart + 1; index < close; index++ {
			if tokens[index].kind == cTransactionIdentifier {
				parameters[tokens[index].text] = struct{}{}
			}
		}
		replacementStart = close + 1
	}
	replacementMatch, _, _ := indexCTransactionParentheses(tokens[replacementStart:])
	replacementBalanced := true
	for index, token := range tokens[replacementStart:] {
		if (token.text == "(" || token.text == ")") && replacementMatch[index] < 0 {
			replacementBalanced = false
			break
		}
	}
	targets := make(map[string]struct{})
	candidates := make(map[string]struct{})
	for index := replacementStart; index < len(tokens); index++ {
		token := tokens[index]
		if token.kind != cTransactionIdentifier {
			continue
		}
		if nativeSourceTransactionReviewUnsupported(token.text) {
			targets[token.text] = struct{}{}
			continue
		}
		if _, parameter := parameters[token.text]; !parameter {
			candidates[token.text] = struct{}{}
		}
	}
	if !replacementBalanced && (len(targets) > 0 || len(candidates) > 0) {
		rhs := ""
		if len(candidates) == 1 {
			for candidate := range candidates {
				rhs = candidate
			}
		}
		assignments[name] = append(assignments[name], cTransactionAliasAssignment{
			rhs:             rhs,
			invalid:         true,
			directlyRelated: len(targets) > 0,
		})
		return
	}
	switch len(targets) {
	case 1:
		for target := range targets {
			assignments[name] = append(assignments[name], cTransactionAliasAssignment{rhs: target})
		}
	case 0:
		if len(candidates) == 1 {
			for candidate := range candidates {
				assignments[name] = append(assignments[name], cTransactionAliasAssignment{rhs: candidate})
			}
		}
	default:
		assignments[name] = append(assignments[name], cTransactionAliasAssignment{
			invalid:         true,
			directlyRelated: true,
		})
	}
}

func collectCTransactionVariableAssignments(
	tokens []cTransactionToken,
	match []int,
	assignments map[string][]cTransactionAliasAssignment,
) {
	for index, token := range tokens {
		if token.text != "=" || index == 0 || index+1 >= len(tokens) ||
			(index > 0 && (tokens[index-1].text == "=" || tokens[index-1].text == "!" ||
				tokens[index-1].text == "<" || tokens[index-1].text == ">")) ||
			(index+1 < len(tokens) && tokens[index+1].text == "=") {
			continue
		}
		lhsIndex, ok := cTransactionAssignmentLHS(tokens, index)
		if !ok {
			continue
		}
		lhs := tokens[lhsIndex].text
		end := index + 1
		exceededBudget := false
		for end < len(tokens) {
			if end-(index+1) >= maxCTransactionAliasTokens {
				exceededBudget = true
				break
			}
			if tokens[end].text == "(" && match[end] >= end {
				if match[end]-(index+1) >= maxCTransactionAliasTokens {
					exceededBudget = true
					break
				}
				end = match[end] + 1
				continue
			}
			if tokens[end].text == ";" || tokens[end].text == "," ||
				tokens[end].text == ")" || tokens[end].text == "}" {
				break
			}
			end++
		}
		rhs, exceeded, ok := unwrapCTransactionDesignator(tokens, match, index+1, end)
		if ok {
			assignments[lhs] = append(assignments[lhs], cTransactionAliasAssignment{rhs: rhs})
			continue
		}
		directlyRelated := false
		limit := end
		if limit > index+1+maxCTransactionDesignatorDepth {
			limit = index + 1 + maxCTransactionDesignatorDepth
		}
		for cursor := index + 1; cursor < limit; cursor++ {
			if tokens[cursor].kind == cTransactionIdentifier &&
				nativeSourceTransactionReviewUnsupported(tokens[cursor].text) {
				directlyRelated = true
				break
			}
		}
		assignments[lhs] = append(assignments[lhs], cTransactionAliasAssignment{
			invalid:         exceeded || exceededBudget || directlyRelated,
			directlyRelated: directlyRelated,
		})
	}
}

func cTransactionAssignmentLHS(tokens []cTransactionToken, assignment int) (int, bool) {
	if assignment <= 0 {
		return 0, false
	}
	if tokens[assignment-1].kind == cTransactionIdentifier {
		return assignment - 1, true
	}
	lowerBound := assignment - maxCTransactionDesignatorDepth
	if lowerBound < 0 {
		lowerBound = 0
	}
	for index := assignment - 1; index >= lowerBound; index-- {
		switch tokens[index].text {
		case ";", "{", "}":
			return 0, false
		}
		if tokens[index].kind == cTransactionIdentifier &&
			index > lowerBound && tokens[index-1].text == "*" {
			return index, true
		}
	}
	return 0, false
}

func resolveCTransactionAliases(
	assignments map[string][]cTransactionAliasAssignment,
) map[string]cTransactionIdentity {
	reverse := make(map[string][]string)
	related := make(map[string]bool)
	queue := make([]string, 0)
	for lhs, values := range assignments {
		for _, assignment := range values {
			if assignment.rhs != "" {
				reverse[assignment.rhs] = append(reverse[assignment.rhs], lhs)
			}
			if assignment.directlyRelated ||
				nativeSourceTransactionReviewUnsupported(assignment.rhs) {
				if !related[lhs] {
					related[lhs] = true
					queue = append(queue, lhs)
				}
			}
		}
	}
	for _, target := range []string{
		"lr_get_transaction_duration",
		"lr_get_transaction_status",
		"lr_get_transaction_think_time",
		"lr_get_transaction_wasted_time",
		"lr_wasted_time",
		"lr_start_cross_vuser_transaction",
		"lr_end_cross_vuser_transaction",
		"lr_start_transaction_instance",
		"lr_end_transaction_instance",
		"lr_stop_transaction_instance",
		"lr_resume_transaction_instance",
		"lr_set_transaction_instance_status",
		"lr_get_trans_instance_duration",
		"lr_get_trans_instance_status",
		"lr_get_trans_instance_think_time",
		"lr_get_trans_instance_wasted_time",
	} {
		queue = append(queue, target)
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, alias := range reverse[current] {
			if related[alias] {
				continue
			}
			related[alias] = true
			queue = append(queue, alias)
		}
	}

	resolved := make(map[string]cTransactionIdentity)
	var resolve func(string, int, map[string]bool) cTransactionIdentity
	resolve = func(name string, depth int, visiting map[string]bool) cTransactionIdentity {
		if nativeSourceTransactionReviewUnsupported(name) {
			return cTransactionIdentity{target: name, related: true}
		}
		if identity, ok := resolved[name]; ok {
			return identity
		}
		if !related[name] {
			return cTransactionIdentity{}
		}
		if depth >= maxCTransactionDesignatorDepth || visiting[name] {
			return cTransactionIdentity{related: true, ambiguous: true}
		}
		visiting[name] = true
		defer delete(visiting, name)
		values := assignments[name]
		if len(values) == 0 {
			return cTransactionIdentity{related: true, ambiguous: true}
		}
		target := ""
		ambiguous := false
		for _, assignment := range values {
			if assignment.invalid || assignment.rhs == "" {
				ambiguous = true
				continue
			}
			identity := resolve(assignment.rhs, depth+1, visiting)
			if !identity.related || identity.ambiguous || identity.target == "" {
				ambiguous = true
				continue
			}
			if target == "" {
				target = identity.target
			} else if target != identity.target {
				ambiguous = true
			}
		}
		identity := cTransactionIdentity{
			target:    target,
			related:   true,
			ambiguous: ambiguous || target == "",
		}
		resolved[name] = identity
		return identity
	}
	for name := range related {
		resolved[name] = resolve(name, 0, make(map[string]bool))
	}
	return resolved
}

func isCIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isCIdentifierContinue(ch byte) bool {
	return isCIdentifierStart(ch) || ch >= '0' && ch <= '9'
}

func orderAndMarkSourceFunctions(functions []ParsedFunction) []ParsedFunction {
	if len(functions) <= 1 {
		return functions
	}
	sort.SliceStable(functions, func(left, right int) bool {
		a, b := functions[left], functions[right]
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		aSpan := a.sourceEndOffset > a.sourceOffset
		bSpan := b.sourceEndOffset > b.sourceOffset
		if aSpan && bSpan {
			if a.sourceOffset != b.sourceOffset {
				return a.sourceOffset < b.sourceOffset
			}
			if a.sourceEndOffset != b.sourceEndOffset {
				return a.sourceEndOffset > b.sourceEndOffset
			}
		}
		if a.LineNumber != b.LineNumber {
			return a.LineNumber < b.LineNumber
		}
		if a.sourceColumn != b.sourceColumn {
			return a.sourceColumn < b.sourceColumn
		}
		return a.EndLine < b.EndLine
	})

	children := make([][]int, len(functions))
	roots := make([]int, 0, len(functions))
	stack := make([]int, 0)
	for index, fn := range functions {
		for len(stack) > 0 {
			parent := functions[stack[len(stack)-1]]
			if parent.Filename == fn.Filename &&
				parent.sourceEndOffset > parent.sourceOffset &&
				fn.sourceEndOffset > fn.sourceOffset &&
				parent.sourceOffset <= fn.sourceOffset &&
				parent.sourceEndOffset >= fn.sourceEndOffset &&
				(parent.sourceOffset != fn.sourceOffset ||
					parent.sourceEndOffset != fn.sourceEndOffset) {
				break
			}
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			children[parent] = append(children[parent], index)
		} else {
			roots = append(roots, index)
		}
		stack = append(stack, index)
	}

	out := make([]ParsedFunction, 0, len(functions))
	subtreeUnsupported := make([]bool, len(functions))
	type traversalFrame struct {
		index     int
		nextChild int
	}
	for _, root := range roots {
		traversal := []traversalFrame{{index: root}}
		for len(traversal) > 0 {
			frame := &traversal[len(traversal)-1]
			if frame.nextChild < len(children[frame.index]) {
				child := children[frame.index][frame.nextChild]
				frame.nextChild++
				traversal = append(traversal, traversalFrame{index: child})
				continue
			}
			index := frame.index
			unsupported := nativeSourceTransactionReviewUnsupported(functions[index].Name)
			for _, child := range children[index] {
				unsupported = unsupported || subtreeUnsupported[child]
			}
			subtreeUnsupported[index] = unsupported
			if !nativeSourceTransactionReviewUnsupported(functions[index].Name) && unsupported {
				functions[index].containsUnsupportedTransactionCall = true
			}
			out = append(out, functions[index])
			traversal = traversal[:len(traversal)-1]
		}
	}
	return out
}
