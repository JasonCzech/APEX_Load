package k6

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// k6Span is a byte range of the source that the importer reproduced faithfully
// in the generated DSL. Translated spans stop counting as residual JavaScript,
// which is what lifts the durable review gate off the affected requests.
type k6Span struct {
	Start int
	End   int
}

var (
	k6SleepCallPattern = regexp.MustCompile(`\bsleep\s*\(`)
	k6CheckCallPattern = regexp.MustCompile(`\bcheck\s*\(`)
	// The declaration may follow an opening brace or a previous statement on the
	// same line, which is how single-line iteration bodies are written.
	k6AssignedResponsePattern = regexp.MustCompile(`(?:^|[{};])\s*(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*$`)
	k6SleepSecondsPattern     = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
	k6StatusLiteralPattern    = regexp.MustCompile(`^[1-5][0-9]{2}$`)
	// The module specifier is deliberately left out of the pattern: it is a
	// string literal, and the masked copy this runs against blanks those.
	k6NamespaceImportPattern = regexp.MustCompile(`(?m)^[ \t]*import\s*\*\s*as\s+([A-Za-z_$][A-Za-z0-9_$]*)\s+from\b`)
)

// k6StaticTranslation is the result of the static sleep()/check() pass. Every
// call site this pass recognises — the bare `sleep(...)`/`check(...)` globals
// and the `import * as k6 from 'k6'` namespace spelling `k6.sleep(...)` /
// `k6.check(...)` — lands in exactly one bucket: translated into DSL material,
// or recorded as an untranslated source line that keeps its requests gated. A
// recognised construct is never dropped from both.
//
// Spellings this lexical pass does not recognise at all — renamed imports such
// as `import { check as verify } from 'k6'`, or a helper that wraps check()
// behind another name — are not counted in either bucket. They are not silently
// dropped either: the residual-source scan (detectK6ResidualSource) still sees
// their bytes, blocks the affected requests, and reports the offending source
// lines to the reviewer.
type k6StaticTranslation struct {
	thinkTimeMs        map[int]float64
	assertions         map[int][]dsl.Assertion
	translatedSpans    []k6Span
	untranslatedSleeps []int
	untranslatedChecks []int
	translatedSleeps   int
	translatedChecks   int
}

// k6ActionTranslation is the per-request slice of the translation that the
// shared request-import overlay consumes.
type k6ActionTranslation struct {
	Assertions     []dsl.Assertion
	ThinkTime      dsl.ThinkTimeConfig
	ThinkTimeAfter bool
}

func (translation k6StaticTranslation) forRequest(index int) k6ActionTranslation {
	out := k6ActionTranslation{Assertions: translation.assertions[index]}
	if milliseconds, ok := translation.thinkTimeMs[index]; ok {
		out.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: milliseconds}
		out.ThinkTimeAfter = true
	}
	return out
}

func (translation k6StaticTranslation) checks() int {
	return translation.translatedChecks + len(translation.untranslatedChecks)
}

func (translation k6StaticTranslation) sleeps() int {
	return translation.translatedSleeps + len(translation.untranslatedSleeps)
}

// translateK6StaticPatterns converts the two statically analysable k6 idioms
// that otherwise force manual review on every imported request: a literal
// sleep(<seconds>) becomes think time on the preceding request, and the
// standard check(res, {'...': r => r.status === 200}) form becomes a
// status_code assertion on the request that produced `res`. Dynamic or
// unrecognised spellings are reported as untranslated instead.
func translateK6StaticPatterns(content string, requests []parsedHTTPRequest) k6StaticTranslation {
	translation := k6StaticTranslation{
		thinkTimeMs: map[int]float64{},
		assertions:  map[int][]dsl.Assertion{},
	}
	lineIndex := sourceview.NewLineIndex(content)
	// Comments and string/template literals are masked for the call search for
	// the same reason http calls are: a check() written inside a comment is not
	// behavior and must not gate the import.
	code := maskK6NonCodeForCallSearch(content)
	namespaces := k6ModuleNamespaceAliases(code, content)
	sleepCalls := findK6GlobalCalls(code, content, k6SleepCallPattern, namespaces)
	checkCalls := findK6GlobalCalls(code, content, k6CheckCallPattern, namespaces)

	// A call nested below the iteration body executes conditionally or
	// repeatedly, and a call embedded in a larger expression (`guard &&
	// check(...)`, `guard ? check(...) : 0`) is conditional even though its
	// brace depth is unchanged. The flattened DSL cannot express either, so a
	// call is translated only when it sits at iteration-body brace depth *and*
	// begins its own statement; the rest stay review evidence.
	offsets := make([]int, 0, len(sleepCalls)+len(checkCalls))
	for _, call := range sleepCalls {
		offsets = append(offsets, call.start)
	}
	for _, call := range checkCalls {
		offsets = append(offsets, call.start)
	}
	// Depth alone cannot separate the iteration body from setup()/teardown()/
	// helper functions: statement level is depth 1 in all of them. The byte
	// range of the default export's body is what makes the scope check real.
	body := k6IterationBodyRange(code)
	baseline, depths := k6StatementDepths(code, offsets, body.Start)

	for _, call := range sleepCalls {
		seconds, ok := parseK6SleepSeconds(call.args)
		if depths[call.start] != baseline || !body.contains(call.start) || !k6CallStartsStatement(code, call.start) {
			ok = false
		}
		owner := -1
		if ok {
			owner = k6RequestBefore(requests, call.start, body)
		}
		total := math.MaxFloat64
		if ok && owner >= 0 {
			total = translation.thinkTimeMs[owner] + math.Round(seconds*1e6)/1e3
		}
		if !ok || owner < 0 || total > dsl.MaxConfiguredProtocolTimeoutMS {
			translation.untranslatedSleeps = append(translation.untranslatedSleeps, lineIndex.LineForOffset(call.start))
			continue
		}
		translation.thinkTimeMs[owner] = total
		translation.translatedSpans = append(translation.translatedSpans, k6Span{Start: call.start, End: call.end})
		translation.translatedSleeps++
	}

	for _, call := range checkCalls {
		variable, status, ok := parseK6StatusCheck(call.args)
		if depths[call.start] != baseline || !body.contains(call.start) || !k6CallStartsStatement(code, call.start) {
			ok = false
		}
		owner := -1
		if ok {
			owner = k6RequestForVariable(requests, variable, call.start, body)
		}
		if owner >= 0 && !k6BindingSurvivesTo(code, variable, requests[owner], call.start) {
			// Something the importer does not model reassigned the response
			// binding between the request and the check. Attaching the
			// assertion here would put it on the wrong action.
			owner = -1
		}
		if owner >= 0 && k6ConflictingStatusAssertion(translation.assertions[owner], status) {
			// The action already carries a different expected status. DSL
			// assertions are evaluated conjunctively, so a second status_code
			// could never hold at the same time as the first and every request
			// would fail. Keep this check as review evidence instead.
			owner = -1
		}
		if !ok || owner < 0 {
			translation.untranslatedChecks = append(translation.untranslatedChecks, lineIndex.LineForOffset(call.start))
			continue
		}
		if !k6HasStatusAssertion(translation.assertions[owner], status) {
			translation.assertions[owner] = append(translation.assertions[owner], dsl.Assertion{
				Type:     "status_code",
				Expected: status,
			})
		}
		translation.translatedSpans = append(translation.translatedSpans, k6Span{Start: call.start, End: call.end})
		translation.translatedChecks++
	}
	return translation
}

// k6StatementDepths returns the brace-nesting depth at baselineOffset (the
// iteration-body statement level) plus the depth at each requested offset,
// computed in one pass over the masked source.
func k6StatementDepths(code string, offsets []int, baselineOffset int) (int, map[int]int) {
	queries := append([]int(nil), offsets...)
	queries = append(queries, baselineOffset)
	sort.Ints(queries)

	depths := make(map[int]int, len(queries))
	depth := 0
	cursor := 0
	for offset := 0; offset <= len(code); offset++ {
		for cursor < len(queries) && queries[cursor] == offset {
			depths[offset] = depth
			cursor++
		}
		if offset == len(code) {
			break
		}
		switch code[offset] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return depths[baselineOffset], depths
}

// k6CallStartsStatement reports whether the call at start begins its own
// statement. Brace depth alone cannot see expression-level conditionality:
// `guard && check(res, ...)` and `guard ? check(res, ...) : 0` sit at
// iteration-body statement depth yet run only when the guard holds, so
// translating them would emit an assertion (or think time) that the source
// applies conditionally. Requiring the call to open a statement is what makes
// "unconditional" true rather than assumed.
func k6CallStartsStatement(code string, start int) bool {
	if start < 0 || start > len(code) {
		return false
	}
	prefix := strings.TrimRight(code[:start], " \t\r\n")
	if prefix == "" {
		// File scope, or the whole prefix was masked comments/whitespace.
		return true
	}
	switch prefix[len(prefix)-1] {
	case '{', '}', ';':
		return true
	case '=':
		// A plain assignment or declaration still runs the call exactly once
		// per iteration: `const ok = check(res, {...});` is unconditional.
		// Comparison, arrow and compound-assignment operators are not
		// statement starts — they mean the call is embedded in a larger
		// expression.
		if len(prefix) < 2 {
			return true
		}
		switch prefix[len(prefix)-2] {
		case '=', '!', '<', '>', '+', '-', '*', '/', '%', '&', '|', '^':
			return false
		}
		return true
	default:
		return false
	}
}

// k6ModuleNamespaceAliases collects the identifiers bound by
// `import * as <name> from 'k6'`. Those names carry the same check()/sleep()
// globals, so `k6.check(...)` must be recognised as the global rather than
// discarded as an unrelated member call. The structural match runs against the
// masked copy so a commented-out import cannot register an alias, while the
// module specifier is read from the real source because masking blanks string
// literals.
func k6ModuleNamespaceAliases(code, content string) map[string]bool {
	aliases := map[string]bool{}
	for _, match := range k6NamespaceImportPattern.FindAllStringSubmatchIndex(code, -1) {
		if match[1] > len(content) {
			continue
		}
		// The masked copy blanks the specifier, so the separator between
		// `from` and the quote has to be skipped explicitly rather than
		// consumed by the pattern.
		rest := strings.TrimLeft(content[match[1]:], " \t\r\n")
		if !strings.HasPrefix(rest, "'k6'") && !strings.HasPrefix(rest, `"k6"`) {
			continue
		}
		aliases[content[match[2]:match[3]]] = true
	}
	return aliases
}

// k6QualifierBefore returns the identifier that qualifies a member call whose
// '.' sits at dotOffset, plus where that identifier starts. It reports false
// when the receiver is not a bare identifier — a longer member chain such as
// `a.k6.check(...)`, or a call/index result — because only a bare binding can
// be the module namespace.
func k6QualifierBefore(code string, dotOffset int) (string, int, bool) {
	end := dotOffset
	for end > 0 && isK6SpaceByte(code[end-1]) {
		end--
	}
	start := end
	for start > 0 && isK6IdentifierByte(code[start-1]) {
		start--
	}
	if start == end || (code[start] >= '0' && code[start] <= '9') {
		return "", 0, false
	}
	if start > 0 {
		switch previous := code[start-1]; previous {
		case '.', ']', ')':
			return "", 0, false
		}
	}
	return code[start:end], start, true
}

func isK6IdentifierByte(value byte) bool {
	return value == '_' || value == '$' ||
		(value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z') ||
		(value >= '0' && value <= '9')
}

func isK6SpaceByte(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

// k6IterationBodyOffset locates the first statement position inside the default
// exported iteration function, falling back to file scope for the bare-call
// fragments the importer also accepts.
func k6IterationBodyOffset(code string) int {
	match := k6DefaultFunctionPattern.FindStringIndex(code)
	if match == nil {
		return 0
	}
	offset := match[1]
	if offset > match[0] && code[offset-1] == '{' {
		// The pattern already consumed the body brace.
		return offset
	}
	for offset < len(code) && (code[offset] == ' ' || code[offset] == '\t' || code[offset] == '\r' || code[offset] == '\n') {
		offset++
	}
	if offset < len(code) && code[offset] == '{' {
		offset++
	}
	return offset
}

// k6IterationBodyRange returns the byte range of the default exported iteration
// function's body. Statements outside it — setup(), teardown(), helper
// functions — run on a different schedule than the per-iteration flow the DSL
// models, so their sleep()/check() calls must not be attached to iteration
// requests even though their brace depth is identical. When there is no default
// export the whole file is the body, matching the bare-call fragments the
// importer also accepts.
func k6IterationBodyRange(code string) k6Span {
	start := k6IterationBodyOffset(code)
	if start <= 0 || start > len(code) {
		return k6Span{Start: 0, End: len(code)}
	}
	depth := 1
	for offset := start; offset < len(code); offset++ {
		switch code[offset] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return k6Span{Start: start, End: offset}
			}
		}
	}
	return k6Span{Start: start, End: len(code)}
}

func (span k6Span) contains(offset int) bool {
	return offset >= span.Start && offset < span.End
}

// k6Call is one occurrence of a k6 global helper call. end < 0 marks a call
// whose argument list could not be delimited: it carries no arguments, so it can
// never be translated, but it is still counted and gated as untranslated.
type k6Call struct {
	start int
	end   int
	args  []string
}

// findK6GlobalCalls locates calls to a k6 global helper. Offsets are searched in
// the masked copy so comments and literals cannot contribute calls, while the
// argument text is taken from the real source. namespaces holds the identifiers
// bound by `import * as <name> from 'k6'`; a member call through one of those is
// the same global and must be recognised, while any other receiver
// (`metrics.check(...)`) is not.
func findK6GlobalCalls(code, content string, pattern *regexp.Regexp, namespaces map[string]bool) []k6Call {
	matches := pattern.FindAllStringIndex(code, -1)
	out := make([]k6Call, 0, len(matches))
	for _, match := range matches {
		start := match[0]
		if prefix := strings.TrimRight(code[:match[0]], " \t\r\n"); strings.HasSuffix(prefix, ".") {
			qualifier, qualifierStart, ok := k6QualifierBefore(code, len(prefix)-1)
			if !ok || !namespaces[qualifier] {
				// A member call such as `metrics.check(...)` is not the k6
				// global.
				continue
			}
			// `k6.check(...)` under `import * as k6 from 'k6'`. The recorded
			// span has to start at the qualifier: a span that began at `check`
			// would leave the `k6.` bytes residual, and the request would keep
			// its review gate even though the call was fully translated.
			start = qualifierStart
		}
		open := match[1] - 1
		closeOffset := matchingParenOffset(content, open)
		if closeOffset < 0 {
			// The argument list could not be delimited — an unbalanced paren or
			// an apostrophe inside a comment defeats the raw-source scan, which
			// does not model comments. Record the occurrence with no arguments
			// so it lands in the untranslated bucket and keeps its review
			// evidence instead of disappearing from both buckets.
			out = append(out, k6Call{start: start, end: -1})
			continue
		}
		out = append(out, k6Call{
			start: start,
			end:   closeOffset + 1,
			args:  splitTopLevelArgs(content[open+1 : closeOffset]),
		})
	}
	return out
}

func parseK6SleepSeconds(args []string) (float64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	literal := strings.TrimSpace(args[0])
	if !k6SleepSecondsPattern.MatchString(literal) {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(literal, 64)
	if err != nil || seconds < 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return 0, false
	}
	return seconds, true
}

// parseK6StatusCheck recognises the standard status-code check idiom. Every
// property must be a recognised status predicate and every property must name
// the same status: a partially understood check stays untranslated so the
// reviewer still sees the behavior that was not reproduced, and a check that
// buckets several statuses stays untranslated because AND-ed status_code
// assertions could never all hold on one response.
func parseK6StatusCheck(args []string) (string, int, bool) {
	if len(args) != 2 {
		return "", 0, false
	}
	variable := strings.TrimSpace(args[0])
	if !k6IdentifierPattern.MatchString(variable) {
		return "", 0, false
	}
	object := strings.TrimSpace(args[1])
	if len(object) < 2 || object[0] != '{' || object[len(object)-1] != '}' {
		return "", 0, false
	}
	properties := splitTopLevelArgs(object[1 : len(object)-1])
	if len(properties) == 0 {
		return "", 0, false
	}
	expected := 0
	for index, property := range properties {
		keyExpression, valueExpression, ok := splitK6Property(property)
		if !ok {
			return "", 0, false
		}
		if _, ok := parseK6PropertyKey(keyExpression); !ok {
			return "", 0, false
		}
		status, ok := parseK6StatusPredicate(valueExpression)
		if !ok {
			return "", 0, false
		}
		if index == 0 {
			expected = status
			continue
		}
		if status != expected {
			return "", 0, false
		}
	}
	return variable, expected, true
}

// parseK6StatusPredicate accepts `r => r.status === 200` and `(r) => r.status
// == 200`. Anything else — response-body inspection, negation, ranges, helper
// calls, function-expression bodies — is left for review.
func parseK6StatusPredicate(expression string) (int, bool) {
	expression = strings.TrimSpace(expression)
	arrow := strings.Index(expression, "=>")
	if arrow < 0 {
		return 0, false
	}
	parameter := strings.TrimSpace(expression[:arrow])
	if strings.HasPrefix(parameter, "(") && strings.HasSuffix(parameter, ")") {
		parameter = strings.TrimSpace(parameter[1 : len(parameter)-1])
	}
	if !k6IdentifierPattern.MatchString(parameter) {
		return 0, false
	}
	body := strings.TrimSpace(expression[arrow+2:])
	prefix := parameter + ".status"
	if !strings.HasPrefix(body, prefix) {
		return 0, false
	}
	rest := strings.TrimSpace(body[len(prefix):])
	switch {
	case strings.HasPrefix(rest, "==="):
		rest = strings.TrimSpace(rest[3:])
	case strings.HasPrefix(rest, "=="):
		rest = strings.TrimSpace(rest[2:])
	default:
		return 0, false
	}
	if !k6StatusLiteralPattern.MatchString(rest) {
		return 0, false
	}
	status, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return status, true
}

func k6HasStatusAssertion(assertions []dsl.Assertion, status int) bool {
	for _, assertion := range assertions {
		if assertion.Type == "status_code" && assertion.Expected == status {
			return true
		}
	}
	return false
}

// k6ConflictingStatusAssertion reports whether the action already expects a
// different status. Assertions on one action are evaluated conjunctively, so
// two different expected statuses can never both be satisfied.
func k6ConflictingStatusAssertion(assertions []dsl.Assertion, status int) bool {
	for _, assertion := range assertions {
		if assertion.Type == "status_code" && assertion.Expected != status {
			return true
		}
	}
	return false
}

// k6RequestBefore returns the index of the last request that completes before
// offset. sleep() pauses the iteration after the preceding request, so that is
// the action the think time belongs to. Requests outside the iteration body run
// on a different schedule and can never own iteration think time.
func k6RequestBefore(requests []parsedHTTPRequest, offset int, body k6Span) int {
	owner := -1
	for index, request := range requests {
		if request.EndOffset > offset {
			break
		}
		if !k6RequestInBody(request, body) {
			continue
		}
		owner = index
	}
	return owner
}

// k6RequestForVariable resolves `check(res, ...)` to the nearest preceding
// request assigned to that variable, so a reassigned response name still binds
// to the right action.
func k6RequestForVariable(requests []parsedHTTPRequest, variable string, offset int, body k6Span) int {
	owner := -1
	for index, request := range requests {
		if request.EndOffset > offset {
			break
		}
		if !k6RequestInBody(request, body) {
			continue
		}
		if request.ResponseVar != "" && request.ResponseVar == variable {
			owner = index
		}
	}
	return owner
}

func k6RequestInBody(request parsedHTTPRequest, body k6Span) bool {
	return request.StartOffset >= body.Start && request.EndOffset <= body.End
}

// k6BindingSurvivesTo reports whether the owning request's declaration is still
// the most recent assignment to the response variable at the check offset.
func k6BindingSurvivesTo(code, variable string, owner parsedHTTPRequest, offset int) bool {
	assignment := k6LastAssignmentOffset(code, variable, offset)
	if assignment < 0 {
		return false
	}
	declarationStart := strings.LastIndex(code[:owner.StartOffset], "\n") + 1
	return assignment >= declarationStart && assignment < owner.StartOffset
}

// k6LastAssignmentOffset returns the offset of the last plain `name =`
// assignment before offset, or -1. Equality, arrow, and comparison operators are
// excluded so only real rebindings count.
func k6LastAssignmentOffset(code, variable string, offset int) int {
	if offset > len(code) {
		offset = len(code)
	}
	pattern, err := regexp.Compile(`(?:^|[^A-Za-z0-9_$.])` + regexp.QuoteMeta(variable) + `\s*=(?:[^=>]|$)`)
	if err != nil {
		return -1
	}
	last := -1
	for _, match := range pattern.FindAllStringIndex(code[:offset], -1) {
		equals := strings.IndexByte(code[match[0]:match[1]], '=')
		if equals < 0 {
			continue
		}
		last = match[0] + equals
	}
	return last
}

// k6AssignedResponseVariable reports the variable a request result is bound to,
// using the same declaration shape the residual scanner already treats as
// reproducible scaffolding.
func k6AssignedResponseVariable(content string, startOffset int) string {
	if startOffset <= 0 || startOffset > len(content) {
		return ""
	}
	lineStart := strings.LastIndex(content[:startOffset], "\n") + 1
	match := k6AssignedResponsePattern.FindStringSubmatch(content[lineStart:startOffset])
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
