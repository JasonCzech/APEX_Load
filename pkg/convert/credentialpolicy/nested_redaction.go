package credentialpolicy

import (
	"bytes"
	"errors"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// Recursive decoding is disclosure-only. Executable projection modes retain
// their existing wire/template and target-locator semantics.
const maxRedactionJSONStringDepth = 3

type structuredRedactionBudget struct {
	remainingBytes  int
	remainingValues int
}

func newStructuredRedactionBudget(limits Limits) *structuredRedactionBudget {
	maxInt := int(^uint(0) >> 1)
	workBytes := maxInt
	if limits.MaxBytes <= maxInt/4 {
		workBytes = limits.MaxBytes * 4
	}
	return &structuredRedactionBudget{remainingBytes: workBytes, remainingValues: limits.MaxValues}
}

func reserveStructuredRedactionBytes(options Options, size int) bool {
	if options.Mode != Redact {
		return true
	}
	budget := options.structuredRedactionBudget
	if budget == nil || size < 0 || size > budget.remainingBytes {
		return false
	}
	budget.remainingBytes -= size
	return true
}

func reserveStructuredRedactionValues(options Options, count int) bool {
	if options.Mode != Redact {
		return true
	}
	budget := options.structuredRedactionBudget
	if budget == nil || count < 0 || count > budget.remainingValues {
		return false
	}
	budget.remainingValues -= count
	return true
}

func nestedJSONObjectOrArray(value string) bool {
	trimmed := bytes.TrimSpace([]byte(value))
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte(credential.Redacted)) || wholeStructuredRuntimeTemplate(string(trimmed)) {
		return false
	}
	return trimmed[0] == '{' || trimmed[0] == '['
}

// Check escaped output lengths before allocating the encoded representation.
func reserveFormEncodedRedaction(options Options, value []byte) bool {
	size := 0
	for _, ch := range value {
		increment := 3
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '_' || ch == '.' || ch == '~' || ch == ' ' {
			increment = 1
		}
		if increment > options.Limits.MaxBytes-size {
			return false
		}
		size += increment
	}
	return reserveStructuredRedactionBytes(options, size)
}

func reserveJSONEncodedRedaction(options Options, value []byte) bool {
	size := 2 // enclosing quotes
	if size > options.Limits.MaxBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		ch := value[index]
		increment := 1
		switch ch {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			increment = 2
		case '<', '>', '&':
			increment = 6 // encoding/json HTML escaping
		default:
			if ch < 0x20 {
				increment = 6
			} else if ch == 0xe2 && index+2 < len(value) && value[index+1] == 0x80 &&
				(value[index+2] == 0xa8 || value[index+2] == 0xa9) {
				increment = 6
				index += 2
			}
		}
		if increment > options.Limits.MaxBytes-size {
			return false
		}
		size += increment
	}
	return reserveStructuredRedactionBytes(options, size)
}

func structuredRedactionOutputWithinLimit(payload []byte, replacements []replacement, limit int) bool {
	size := len(payload)
	for _, item := range replacements {
		removed := item.end - item.start
		if removed < 0 || removed > size {
			return false
		}
		size -= removed
		if len(item.value) > limit-size {
			return false
		}
		size += len(item.value)
	}
	return size <= limit
}

func jsonUnicodeEscapeCode(digits []byte) uint16 {
	var code uint16
	for _, digit := range digits {
		code <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			code += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			code += uint16(digit - 'a' + 10)
		case digit >= 'A' && digit <= 'F':
			code += uint16(digit - 'A' + 10)
		}
	}
	return code
}

func validLowJSONSurrogate(digits []byte) bool {
	for _, digit := range digits {
		if !isHexDigit(digit) {
			return false
		}
	}
	code := jsonUnicodeEscapeCode(digits)
	return code >= 0xdc00 && code <= 0xdfff
}

// ErrQueryStructureWorkLimit identifies bounded query dispatch admission
// failure without exposing any source content.
var ErrQueryStructureWorkLimit = errors.New("query structure inspection exceeds work limit")

const maxQueryStructureWorkBytes = 64 << 20

// InspectQueryValueStructure distinguishes JSON-like query string structure
// from anonymous GraphQL. It recognizes quoted object members throughout the
// value, outside GraphQL strings/block strings/comments. Malformed punctuation
// cannot silently redirect encoded JSON to the GraphQL scalar collector.
// The returned conservative work charge covers scanning and ignored lookahead;
// callers share it with their existing disclosure work budget.
func InspectQueryValueStructure(value string, maxWorkBytes int) (jsonLike bool, workBytes int, err error) {
	if maxWorkBytes > maxQueryStructureWorkBytes {
		maxWorkBytes = maxQueryStructureWorkBytes
	}
	if maxWorkBytes < 0 || len(value) > maxWorkBytes/2 {
		return false, 0, ErrQueryStructureWorkLimit
	}
	workBytes = len(value) * 2
	trimmed := strings.TrimSpace(value)
	if len(trimmed) == 0 || trimmed == credential.Redacted || wholeStructuredRuntimeTemplate(trimmed) {
		return false, workBytes, nil
	}
	if trimmed[0] == '[' {
		return true, workBytes, nil
	}
	if trimmed[0] == '{' {
		rest := strings.TrimSpace(trimmed[1:])
		if len(rest) == 0 || rest[0] == '{' || rest[0] == '[' || rest[0] == '}' {
			return true, workBytes, nil
		}
	}
	grammarContext := queryHasExecutableStructure(trimmed)
	parenthesisDepth, structureDepth := 0, 0
	var structure [128]byte
	var previous byte
	for index := 0; index < len(trimmed); {
		switch trimmed[index] {
		case ' ', '\t', '\n', '\r', ',':
			index++
		case '#':
			for index < len(trimmed) && trimmed[index] != '\n' && trimmed[index] != '\r' {
				index++
			}
		case '\'':
			containerContext := structureDepth > 0 && (structure[structureDepth-1] == '{' || structure[structureDepth-1] == '[')
			if grammarContext || containerContext {
				return true, workBytes, nil
			}
			end, complete, memberEvidence := queryNeutralQuotedEnd(trimmed, index)
			if !complete {
				// The helper consumed the entire suffix once. Its member
				// evidence decides admission without rescanning escaped quotes.
				return memberEvidence, workBytes, nil
			}
			index = end
		case '\\', '/', ';', '?', '<', '>', '%', '~', '^', '+':
			if grammarContext {
				return true, workBytes, nil
			}
			previous = trimmed[index]
			index++
		case '(', '[', '{':
			if structureDepth == len(structure) {
				return false, workBytes, ErrQueryStructureWorkLimit
			}
			structure[structureDepth] = trimmed[index]
			structureDepth++
			if trimmed[index] == '(' {
				parenthesisDepth++
			}
			previous = trimmed[index]
			index++
		case ')', ']', '}':
			opening := byte('(')
			if trimmed[index] == ']' {
				opening = '['
			} else if trimmed[index] == '}' {
				opening = '{'
			}
			if structureDepth == 0 || structure[structureDepth-1] != opening {
				if grammarContext {
					return true, workBytes, nil
				}
				previous = trimmed[index]
				index++
				continue
			}
			structureDepth--
			if trimmed[index] == ')' {
				parenthesisDepth--
			}
			previous = trimmed[index]
			index++
		case '"':
			// Executable GraphQL strings are argument/default/input values,
			// never selection-field names (including colonless quoted keys).
			insideValueArray := structureDepth > 0 && structure[structureDepth-1] == '['
			valueContext := previous == ':' || previous == '=' || insideValueArray && (previous == '[' || previous == '"' || previous == ']' || previous == '}' || queryValueNameByte(previous))
			objectContext := structureDepth > 0 && structure[structureDepth-1] == '{'
			if (grammarContext || objectContext) && (parenthesisDepth == 0 || !valueContext) {
				return true, workBytes, nil
			}
			end, complete := queryQuotedScalarEnd(trimmed, index)
			if !complete {
				return true, workBytes, nil
			}
			after := queryIgnoredEnd(trimmed, end)
			if after < len(trimmed) && trimmed[after] == ':' {
				return true, workBytes, nil
			}
			previous = '"'
			index = end
		default:
			if grammarContext && !queryValueNameByte(previous) && graphQLNumberStart(trimmed, index) {
				end, numberErr := graphQLNumberEnd(trimmed, index)
				if numberErr != nil {
					return true, workBytes, nil
				}
				previous = trimmed[end-1]
				index = end
				continue
			}
			if strings.HasPrefix(trimmed[index:], "\ufeff") {
				index += 3 // GraphQL permits ignored UTF-8 BOMs.
				continue
			}
			if trimmed[index] >= 0x80 && grammarContext {
				return true, workBytes, nil
			}
			previous = trimmed[index]
			index++
		}
	}
	return false, workBytes, nil
}

func queryHasExecutableStructure(value string) bool {
	start := queryIgnoredEnd(value, 0)
	if start == len(value) {
		return false
	}
	value = value[start:]
	if value[0] == '{' || value[0] == '[' {
		return true
	}
	for _, keyword := range []string{"query", "mutation", "subscription", "fragment"} {
		if len(value) > len(keyword) && strings.EqualFold(value[:len(keyword)], keyword) && !queryValueNameByte(value[len(keyword)]) {
			return true
		}
	}
	return false
}

// Neutral query text can contain ordinary SQL/search quotes. Skipping a
// complete single-quoted literal avoids treating its content as GraphQL/JSON;
// unmatched text still leaves later member evidence available to the scan.
func queryNeutralQuotedEnd(value string, start int) (int, bool, bool) {
	inDoubleQuote, afterDoubleQuote, memberEvidence := false, false, false
	containerDepth := 0
	for index := start + 1; index < len(value); index++ {
		if value[index] == '\\' {
			index++
			continue
		}
		if value[index] == '\'' {
			if index+1 < len(value) && value[index+1] == '\'' {
				index++
				continue
			}
			return index + 1, true, false
		}
		if value[index] == '"' {
			if !inDoubleQuote && containerDepth > 0 {
				memberEvidence = true
			}
			inDoubleQuote = !inDoubleQuote
			afterDoubleQuote = !inDoubleQuote
			continue
		}
		if inDoubleQuote {
			continue
		}
		if value[index] == '{' || value[index] == '[' {
			// An incomplete neutral literal cannot prove container text is data.
			memberEvidence = true
			containerDepth++
		} else if (value[index] == '}' || value[index] == ']') && containerDepth > 0 {
			containerDepth--
		}
		if value[index] == ':' && afterDoubleQuote {
			memberEvidence = true
		} else if value[index] != ' ' && value[index] != '\t' && value[index] != '\n' && value[index] != '\r' {
			afterDoubleQuote = false
		}
	}
	return len(value), false, memberEvidence
}

func queryValueNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}

func queryQuotedScalarEnd(value string, start int) (int, bool) {
	if strings.HasPrefix(value[start:], `"""`) {
		for index := start + 3; index < len(value); index++ {
			if value[index] == '\\' && strings.HasPrefix(value[index+1:], `"""`) {
				index += 3
				continue
			}
			if strings.HasPrefix(value[index:], `"""`) {
				return index + 3, true
			}
		}
		return len(value), false
	}
	for index := start + 1; index < len(value); index++ {
		if value[index] == '\\' {
			index++
			continue
		}
		if value[index] == '"' {
			return index + 1, true
		}
	}
	return len(value), false
}

func queryIgnoredEnd(value string, start int) int {
	for start < len(value) {
		switch value[start] {
		case ' ', '\t', '\n', '\r', ',':
			start++
		case '#':
			for start < len(value) && value[start] != '\n' && value[start] != '\r' {
				start++
			}
		default:
			if strings.HasPrefix(value[start:], "\ufeff") {
				start += 3
				continue
			}
			return start
		}
	}
	return start
}

var structuredRuntimeReferenceExpression = regexp.MustCompile(`^(?:var|param|data|env|secret|file)\.[A-Za-z0-9_.:/@-]+$`)
var structuredRuntimeFunctionExpression = regexp.MustCompile(`^(?:fn|fake)\.[A-Za-z_][A-Za-z0-9_.-]*(?:\(\s*[A-Za-z0-9_.+\s,-]*\))?$`)

func wholeStructuredRuntimeTemplate(value string) bool {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 260 || !strings.HasPrefix(trimmed, "{{") || runtimeTemplatePattern.FindString(trimmed) != trimmed {
		return false
	}
	expression := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	return expression == "run.id" || structuredRuntimeReferenceExpression.MatchString(expression) || structuredRuntimeFunctionExpression.MatchString(expression)
}
