package jsonextract

// Exact-number JMESPath shared by every protocol that extracts JSON values:
// HTTP, WebSocket, gRPC, database and network extraction all evaluate through
// EvaluateJMESPath, so a selected number keeps its source token everywhere and
// a comparison never silently rounds a large integer.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/jmespath/go-jmespath"

	"github.com/jasonczech/apexload/pkg/internal/boundedcache"
)

// JMESPathErrorKind classifies why an exact-number JMESPath evaluation failed,
// so each protocol can keep its own error wording.
type JMESPathErrorKind int

const (
	JMESPathInvalidJSON JMESPathErrorKind = iota + 1
	JMESPathInvalidExpression
	JMESPathEvaluation
)

// JMESPathError carries the failure class and its cause. Its text is the
// cause's text alone.
type JMESPathError struct {
	Kind JMESPathErrorKind
	Err  error
}

func (e *JMESPathError) Error() string { return e.Err.Error() }

func (e *JMESPathError) Unwrap() error { return e.Err }

var errUnsafeJMESPathComparison = errors.New("numeric comparison is unsafe for values outside the exact float64 integer range")

// Sized like HTTP's compiled-extractor caches, since HTTP extraction compiles
// through this one: an expression up to HTTP's 64 KiB limit stays cached.
var jmesPathCompileCache = boundedcache.NewStringCache[*jmespath.JMESPath](1024, 64<<10, 4<<20)

func compileJMESPathCached(expr string) (*jmespath.JMESPath, error) {
	if cached, ok := jmesPathCompileCache.Load(expr); ok {
		return cached, nil
	}
	compiled, err := jmespath.Compile(expr)
	if err != nil {
		return nil, err
	}
	actual, _ := jmesPathCompileCache.LoadOrStore(expr, compiled)
	return actual, nil
}

// JMESPathValue decodes one JSON document with DecodeExact and evaluates expr
// against it (see EvaluateJMESPath). A nil value means the expression matched
// nothing.
func JMESPathValue(expr string, body []byte) (any, error) {
	var current any
	if err := DecodeExact(body, &current); err != nil {
		return nil, &JMESPathError{Kind: JMESPathInvalidJSON, Err: err}
	}
	return EvaluateJMESPath(expr, current)
}

// EvaluateJMESPath evaluates expr against a document decoded with json.Number
// values, for a protocol that applies its own decoding limits. A selection
// keeps each number's source token (json.Number). Only an expression that can
// observe number representation (a comparison, function call or literal) is
// re-evaluated with float64 values inside the exact integer range, and it is
// refused when a comparison would read a larger integer. A nil value means the
// expression matched nothing.
func EvaluateJMESPath(expr string, current any) (any, error) {
	compiled, err := compileJMESPathCached(expr)
	if err != nil {
		return nil, &JMESPathError{Kind: JMESPathInvalidExpression, Err: err}
	}
	originalResult, originalErr := compiled.Search(current)
	result := originalResult
	// Extraction runs on every response, so the normalized re-evaluation (a full
	// tree copy plus a second Search) is reserved for expressions that can
	// actually observe number representation.
	if jmesPathNumberRepresentationSensitive(expr) {
		normalizedCurrent, hasUnsafeNumber := normalizeJMESPathNumbers(current)
		normalizedResult, normalizedErr := compiled.Search(normalizedCurrent)
		if hasUnsafeNumber && jmesPathComparisonTouchesUnsafeNumber(expr, collectUnsafeJMESPathNumbers(current)) {
			return nil, &JMESPathError{Kind: JMESPathEvaluation, Err: errUnsafeJMESPathComparison}
		}
		if hasUnsafeNumber {
			lossyResult, lossyErr := compiled.Search(normalizeJMESPathNumbersLossy(current))
			if normalizedErr != nil || lossyErr != nil || !equivalentLossyJMESPathResult(normalizedResult, lossyResult) {
				return nil, &JMESPathError{Kind: JMESPathEvaluation, Err: errUnsafeJMESPathComparison}
			}
		}
		switch {
		case normalizedErr == nil && (originalErr != nil || !equivalentJMESPathResult(originalResult, normalizedResult)):
			// Numeric predicates can partially succeed against nonnumeric branches
			// while silently omitting json.Number branches. Compare the complete
			// result, not only empty/boolean shapes, before retaining exact numbers.
			result = normalizedResult
			originalErr = nil
		case originalErr != nil:
			originalErr = normalizedErr
		}
	}
	if originalErr != nil {
		return nil, &JMESPathError{Kind: JMESPathEvaluation, Err: originalErr}
	}
	return result, nil
}

// FormatValue renders a selected JSON value as variable text: a string as-is,
// a json.Number as its exact source token, any other number in plain decimal
// form (never Go's exponent form), and a composite as compact JSON.
func FormatValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case []any, map[string]any:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(encoded)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// normalizeJMESPathNumbers converts encoding/json's lossless json.Number
// values into the float64 representation expected by go-jmespath. Values
// outside JavaScript's safe-integer magnitude remain json.Number: converting
// them would let distinct identifiers collapse onto the same float64. It
// returns a copy so scalar selections can retain exact decimal strings.
func normalizeJMESPathNumbers(value any) (normalized any, unsafeNumber bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil || parsed > maxSafeJMESPathInteger || parsed < -maxSafeJMESPathInteger {
			return typed, true
		}
		return parsed, false
	case []any:
		normalizedItems := make([]any, len(typed))
		for index, item := range typed {
			normalizedItem, itemUnsafe := normalizeJMESPathNumbers(item)
			normalizedItems[index] = normalizedItem
			unsafeNumber = unsafeNumber || itemUnsafe
		}
		return normalizedItems, unsafeNumber
	case map[string]any:
		normalizedItems := make(map[string]any, len(typed))
		for key, item := range typed {
			normalizedItem, itemUnsafe := normalizeJMESPathNumbers(item)
			normalizedItems[key] = normalizedItem
			unsafeNumber = unsafeNumber || itemUnsafe
		}
		return normalizedItems, unsafeNumber
	default:
		return value, false
	}
}

// normalizeJMESPathNumbersLossy mirrors the representation go-jmespath would
// see if every finite JSON number were decoded through float64. It is used only
// as a predicate-safety witness; selected values still come from the lossless
// tree above. Comparing the two evaluations lets an unrelated large identifier
// coexist with a safe numeric predicate without silently allowing that
// identifier itself to participate in a rounded comparison.
func normalizeJMESPathNumbersLossy(value any) any {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil {
			return typed
		}
		return parsed
	case []any:
		normalizedItems := make([]any, len(typed))
		for index, item := range typed {
			normalizedItems[index] = normalizeJMESPathNumbersLossy(item)
		}
		return normalizedItems
	case map[string]any:
		normalizedItems := make(map[string]any, len(typed))
		for key, item := range typed {
			normalizedItems[key] = normalizeJMESPathNumbersLossy(item)
		}
		return normalizedItems
	default:
		return value
	}
}

// jmesPathNumberRepresentationSensitive reports whether evaluating expr can
// depend on document numbers being json.Number rather than float64. Only
// comparisons, function calls, and literals inspect a value numerically; a pure
// path/projection/multiselect selects the same nodes under either
// representation, and go-jmespath's truthiness treats both as non-empty. The
// scan is deliberately conservative: a quoted identifier that merely contains
// one of these bytes costs the fast path, never correctness.
func jmesPathNumberRepresentationSensitive(expr string) bool {
	return strings.ContainsAny(expr, "<>=!(`")
}

func equivalentLossyJMESPathResult(left, right any) bool {
	return reflect.DeepEqual(
		normalizeJMESPathNumbersLossy(left),
		normalizeJMESPathNumbersLossy(right),
	)
}

func equivalentJMESPathResult(original, normalized any) bool {
	normalizedOriginal, _ := normalizeJMESPathNumbers(original)
	return reflect.DeepEqual(normalizedOriginal, normalized)
}

const maxSafeJMESPathInteger = float64(1<<53 - 1)

type jmesPathSafetyToken struct {
	kind  byte
	value string
}

const (
	jmesPathIdentifierToken byte = 'i'
	jmesPathComparatorToken byte = 'c'
	jmesPathBoundaryToken   byte = 'b'
	jmesPathDotToken        byte = 'd'
	jmesPathProjectionToken byte = 'p'
)

type unsafeJMESPathNumbers struct {
	paths    map[string]struct{}
	suffixes map[string]struct{}
	fields   map[string]struct{}
}

// collectUnsafeJMESPathNumbers records both the full object path and leaf field
// for each number that cannot be compared through go-jmespath's float64 runtime
// without rounding. Full paths prevent an unsafe archive.id from poisoning an
// unrelated comparison of current.id; leaf names remain a conservative fallback
// for relative fields inside filter projections.
func collectUnsafeJMESPathNumbers(value any) unsafeJMESPathNumbers {
	unsafe := unsafeJMESPathNumbers{
		paths:    make(map[string]struct{}),
		suffixes: make(map[string]struct{}),
		fields:   make(map[string]struct{}),
	}
	var visit func(any, []string)
	visit = func(current any, path []string) {
		switch typed := current.(type) {
		case json.Number:
			parsed, err := strconv.ParseFloat(typed.String(), 64)
			if err != nil || parsed > maxSafeJMESPathInteger || parsed < -maxSafeJMESPathInteger {
				if len(path) == 0 {
					unsafe.paths[jmesPathSafetyPathKey([]string{"@"})] = struct{}{}
					unsafe.fields["@"] = struct{}{}
					return
				}
				unsafe.paths[jmesPathSafetyPathKey(path)] = struct{}{}
				for start := 0; start+1 < len(path); start++ {
					unsafe.suffixes[jmesPathSafetyPathKey(path[start:])] = struct{}{}
				}
				unsafe.fields[path[len(path)-1]] = struct{}{}
			}
		case []any:
			for _, item := range typed {
				visit(item, path)
			}
		case map[string]any:
			for key, item := range typed {
				childPath := append(append([]string(nil), path...), key)
				visit(item, childPath)
			}
		}
	}
	visit(value, nil)
	return unsafe
}

func jmesPathSafetyPathKey(path []string) string {
	var key strings.Builder
	for _, segment := range path {
		key.WriteString(strconv.Itoa(len(segment)))
		key.WriteByte(':')
		key.WriteString(segment)
	}
	return key.String()
}

func isJMESPathIdentifierStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isJMESPathIdentifierContinue(value byte) bool {
	return isJMESPathIdentifierStart(value) || value >= '0' && value <= '9'
}

// tokenizeJMESPathComparisons extracts only the identifiers and boundaries
// needed to determine which fields participate in comparison expressions. It
// deliberately ignores literals; the real compiler remains the authority for
// expression validity.
func tokenizeJMESPathComparisons(expr string) []jmesPathSafetyToken {
	tokens := make([]jmesPathSafetyToken, 0, len(expr)/3)
	bracketFilters := make([]bool, 0, 4)
	for index := 0; index < len(expr); {
		current := expr[index]
		switch {
		case current == '\'' || current == '`':
			quote := current
			index++
			for index < len(expr) {
				if expr[index] == '\\' && index+1 < len(expr) {
					index += 2
					continue
				}
				if expr[index] == quote {
					index++
					break
				}
				index++
			}
		case current == '"':
			start := index
			index++
			for index < len(expr) {
				if expr[index] == '\\' && index+1 < len(expr) {
					index += 2
					continue
				}
				if expr[index] == '"' {
					index++
					break
				}
				index++
			}
			var identifier string
			if err := json.Unmarshal([]byte(expr[start:index]), &identifier); err == nil {
				tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathIdentifierToken, value: identifier})
			}
		case isJMESPathIdentifierStart(current):
			start := index
			for index++; index < len(expr) && isJMESPathIdentifierContinue(expr[index]); index++ {
			}
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathIdentifierToken, value: expr[start:index]})
		case current == '@':
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathIdentifierToken, value: "@"})
			index++
		case current == '.':
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathDotToken, value: "."})
			index++
		case current == '[':
			filter := index+1 < len(expr) && expr[index+1] == '?'
			bracketFilters = append(bracketFilters, filter)
			if !filter {
				tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathProjectionToken})
			}
			index++
		case current == ']':
			filter := false
			if last := len(bracketFilters) - 1; last >= 0 {
				filter = bracketFilters[last]
				bracketFilters = bracketFilters[:last]
			}
			if filter {
				tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathBoundaryToken, value: "]"})
			} else {
				tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathProjectionToken})
			}
			index++
		case (current == '=' || current == '!') && index+1 < len(expr) && expr[index+1] == '=':
			index++
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathComparatorToken})
			index++
		case current == '<' || current == '>':
			if index+1 < len(expr) && expr[index+1] == '=' {
				index++
			}
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathComparatorToken})
			index++
		case current == '|' || current == '&':
			value := string(current)
			if index+1 < len(expr) && expr[index+1] == current {
				value += string(current)
				index++
			}
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathBoundaryToken, value: value})
			index++
		case current == ',' || current == '?':
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathBoundaryToken, value: string(current)})
			index++
		case current == '(' || current == ')':
			tokens = append(tokens, jmesPathSafetyToken{kind: jmesPathBoundaryToken, value: string(current)})
			index++
		default:
			index++
		}
	}
	return tokens
}

func jmesPathComparisonTouchesUnsafeNumber(expr string, unsafe unsafeJMESPathNumbers) bool {
	if len(unsafe.paths) == 0 {
		return false
	}
	tokens := tokenizeJMESPathComparisons(expr)
	for comparator := range tokens {
		if tokens[comparator].kind != jmesPathComparatorToken {
			continue
		}
		filterContext, relativeToFilter := jmesPathComparisonFilterContext(tokens, comparator)
		pipeContext, relativeToPipe := jmesPathComparisonPipeContext(tokens, comparator)
		context := append(append([]string(nil), pipeContext...), filterContext...)
		relative := relativeToFilter || relativeToPipe
		left := jmesPathComparisonOperandIdentifiers(tokens, comparator-1, -1)
		if jmesPathOperandTouchesUnsafeNumber(left, context, relative, unsafe) {
			return true
		}
		right := jmesPathComparisonOperandIdentifiers(tokens, comparator+1, 1)
		if jmesPathOperandTouchesUnsafeNumber(right, context, relative, unsafe) {
			return true
		}
	}
	return false
}

func jmesPathComparisonOperandIdentifiers(tokens []jmesPathSafetyToken, start, step int) []string {
	identifiers := make([]string, 0, 3)
	expectIdentifier := true

scan:
	for index := start; index >= 0 && index < len(tokens); index += step {
		if tokens[index].kind == jmesPathBoundaryToken || tokens[index].kind == jmesPathComparatorToken {
			break
		}
		switch tokens[index].kind {
		case jmesPathIdentifierToken:
			if !expectIdentifier {
				break scan
			}
			identifiers = append(identifiers, tokens[index].value)
			expectIdentifier = false
		case jmesPathDotToken:
			if len(identifiers) == 0 {
				break scan
			}
			expectIdentifier = true
		case jmesPathProjectionToken:
			expectIdentifier = true
		}
	}
	if step < 0 {
		slices.Reverse(identifiers)
	}
	return identifiers
}

func jmesPathComparisonFilterContext(tokens []jmesPathSafetyToken, comparator int) ([]string, bool) {
	contexts := make([][]string, 0, 2)
	cursor := comparator - 1
	for cursor >= 0 {
		closedFilters := 0
		filterStart := -1
		for index := cursor; index >= 0; index-- {
			if tokens[index].kind != jmesPathBoundaryToken {
				continue
			}
			switch tokens[index].value {
			case "]":
				closedFilters++
			case "?":
				if closedFilters > 0 {
					closedFilters--
					continue
				}
				filterStart = index
			}
			if filterStart >= 0 {
				break
			}
		}
		if filterStart < 0 {
			break
		}
		prefix := jmesPathComparisonOperandIdentifiers(tokens, filterStart-1, -1)
		contexts = append(contexts, prefix)
		cursor = filterStart - 1
	}
	if len(contexts) == 0 {
		return nil, false
	}
	slices.Reverse(contexts)
	var context []string
	for _, prefix := range contexts {
		context = append(context, prefix...)
	}
	return context, true
}

// jmesPathComparisonPipeContext qualifies operands evaluated on the right side
// of a JMESPath pipe. For example, `items[0] | id > ...` compares items.id, not
// a root-level id. Logical OR/AND and argument separators terminate a pipeline;
// chained pipes contribute each recoverable path segment in source order.
func jmesPathComparisonPipeContext(tokens []jmesPathSafetyToken, comparator int) ([]string, bool) {
	segments := make([][]string, 0, 2)
	cursor := comparator - 1
	foundPipe := false
	for cursor >= 0 {
		pipe := -1
		parenDepth := 0
		filterDepth := 0
		for index := cursor; index >= 0; index-- {
			if tokens[index].kind != jmesPathBoundaryToken {
				continue
			}
			switch tokens[index].value {
			case "|":
				if parenDepth == 0 && filterDepth == 0 {
					pipe = index
				}
			case ")":
				parenDepth++
			case "(":
				if parenDepth > 0 {
					parenDepth--
				}
			case "]":
				filterDepth++
			case "?":
				if filterDepth > 0 {
					filterDepth--
				}
			case ",":
				if parenDepth == 0 && filterDepth == 0 {
					index = -1
				}
			}
			if pipe >= 0 || index < 0 {
				break
			}
		}
		if pipe < 0 {
			break
		}
		foundPipe = true
		prefix := jmesPathComparisonOperandIdentifiers(tokens, pipe-1, -1)
		if len(prefix) == 0 {
			// The lightweight scanner could not reconstruct a complex left-hand
			// expression. Mark the operand as pipe-relative so the caller uses its
			// conservative unsafe-leaf fallback.
			return nil, true
		}
		segments = append(segments, prefix)
		cursor = pipe - 1
	}
	if !foundPipe {
		return nil, false
	}
	slices.Reverse(segments)
	var context []string
	for _, segment := range segments {
		context = append(context, segment...)
	}
	return context, true
}

func jmesPathOperandTouchesUnsafeNumber(identifiers, filterContext []string, relativeToFilter bool, unsafe unsafeJMESPathNumbers) bool {
	if len(identifiers) == 0 {
		return false
	}
	if relativeToFilter {
		for len(identifiers) > 0 && identifiers[0] == "@" {
			identifiers = identifiers[1:]
		}
		if len(identifiers) == 0 {
			identifiers = []string{"@"}
		}
		qualified := append(append([]string(nil), filterContext...), identifiers...)
		qualifiedKey := jmesPathSafetyPathKey(qualified)
		if _, found := unsafe.paths[qualifiedKey]; found {
			return true
		}
		// Nested projections can omit an outer collection segment from the
		// lightweight token context. Matching a multi-segment relative suffix is
		// conservative and prevents a rounded comparison from being accepted.
		if _, found := unsafe.suffixes[qualifiedKey]; found {
			return true
		}
		if len(filterContext) == 0 && len(identifiers) == 1 {
			_, found := unsafe.fields[identifiers[0]]
			return found
		}
		return false
	}
	if len(identifiers) == 1 {
		_, found := unsafe.paths[jmesPathSafetyPathKey(identifiers)]
		return found
	}
	path := identifiers
	if path[0] == "@" && len(path) > 1 {
		path = path[1:]
	}
	pathKey := jmesPathSafetyPathKey(path)
	if _, found := unsafe.paths[pathKey]; found {
		return true
	}
	return false
}
