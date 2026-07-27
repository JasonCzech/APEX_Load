package scrub

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type quotedValueReplacement struct {
	start int
	end   int
	value string
}

// RedactQuotedValues removes quoted source scalars whose decoded value contains
// protected material. Import adapters use it after projecting source literals:
// replacing only the decoded spelling is insufficient when JSON, JavaScript,
// or YAML authored the same value with \u, \x, or quoted-scalar escapes.
//
// The complete scalar is replaced rather than attempting byte-level surgery
// inside an escape sequence. Physical newline count is retained so existing
// source-review mappings continue to refer to the same lines.
func RedactQuotedValues(source string, protected []string) (string, bool) {
	protected = normalizedProtectedValues(protected)
	if source == "" || len(protected) == 0 {
		return source, false
	}

	// Run two independent linear lexical passes. JavaScript treats a
	// backslash-quote as escaped, while YAML single-quoted scalars treat the
	// backslash literally and escape quotes only by doubling them. Precomputed
	// closing positions let us consider every quote as a possible opener
	// without repeatedly scanning attacker-controlled suffixes. The inspection
	// budget covers ambiguous tokens and protected-value comparisons; exhaustion
	// omits the complete public source rather than risking a missed spelling.
	budget := quotedValueInspectionBudget(len(source))
	replacements, exhausted := scanQuotedValueReplacements(source, protected, false, &budget)
	var yamlReplacements []quotedValueReplacement
	if !exhausted {
		yamlReplacements, exhausted = scanQuotedValueReplacements(source, protected, true, &budget)
		replacements = append(replacements, yamlReplacements...)
	}
	if exhausted {
		return `"` + Redacted + `"` +
			strings.Repeat("\n", strings.Count(source, "\n")), true
	}
	if len(replacements) == 0 {
		return source, false
	}
	replacements = nonOverlappingQuotedReplacements(source, replacements)

	var output strings.Builder
	output.Grow(len(source))
	cursor := 0
	for _, replacement := range replacements {
		output.WriteString(source[cursor:replacement.start])
		output.WriteString(replacement.value)
		cursor = replacement.end
	}
	output.WriteString(source[cursor:])
	return output.String(), true
}

func quotedValueInspectionBudget(sourceBytes int) int {
	const fixedAllowance = 1 << 20
	const sourceMultiplier = 16
	maxInt := int(^uint(0) >> 1)
	if sourceBytes > (maxInt-fixedAllowance)/sourceMultiplier {
		return maxInt
	}
	return fixedAllowance + sourceMultiplier*sourceBytes
}

func scanQuotedValueReplacements(
	source string,
	protected []string,
	yamlSingle bool,
	budget *int,
) ([]quotedValueReplacement, bool) {
	var replacements []quotedValueReplacement
	endings := backslashQuotedEndings(source)
	if yamlSingle {
		endings = yamlSingleQuotedEndings(source)
	}
	for index := 0; index < len(source); index++ {
		quote := source[index]
		if yamlSingle {
			if quote != '\'' {
				continue
			}
		} else if quote != '"' && quote != '\'' {
			continue
		}
		if !possibleQuotedValueStart(source, index) {
			continue
		}
		end := endings[index]
		if end < 0 {
			continue
		}
		token := source[index:end]
		contains, exhausted := quotedValueContainsProtected(token, protected, budget)
		if exhausted {
			return nil, true
		}
		if contains {
			replacements = append(replacements, quotedValueReplacement{
				start: index,
				end:   end,
			})
		}
	}
	return replacements, false
}

func possibleQuotedValueStart(source string, index int) bool {
	if index <= 0 {
		return true
	}
	previous := index - 1
	for previous >= 0 {
		switch source[previous] {
		case ' ', '\t', '\r', '\n':
			previous--
			continue
		}
		break
	}
	if previous < 0 {
		return true
	}
	// These delimiters cover JSON/YAML keys and values, JavaScript argument
	// and object literals, XML attributes, and top-level list scalars. An
	// apostrophe embedded in prose (for example "don't") is deliberately not
	// an opener, but the actual request quote after '(' is still inspected.
	return strings.ContainsRune(`[{(:,=;+-!?&|`, rune(source[previous]))
}

func backslashQuotedEndings(source string) []int {
	endings := make([]int, len(source))
	for index := range endings {
		endings[index] = -1
	}
	escaped := make([]bool, len(source))
	backslashes := 0
	for index := 0; index < len(source); index++ {
		if source[index] == '\\' {
			backslashes++
			continue
		}
		if source[index] == '\'' || source[index] == '"' {
			escaped[index] = backslashes%2 == 1
		}
		backslashes = 0
	}
	for _, quote := range []byte{'\'', '"'} {
		next := -1
		for index := len(source) - 1; index >= 0; index-- {
			if source[index] != quote {
				continue
			}
			if next >= 0 {
				endings[index] = next + 1
			}
			if !escaped[index] {
				next = index
			}
		}
	}
	return endings
}

func yamlSingleQuotedEndings(source string) []int {
	endings := make([]int, len(source))
	for index := range endings {
		endings[index] = -1
	}
	nextQuote := make([]int, len(source)+2)
	nextQuote[len(source)] = -1
	nextQuote[len(source)+1] = -1
	for index := len(source) - 1; index >= 0; index-- {
		nextQuote[index] = nextQuote[index+1]
		if source[index] == '\'' {
			nextQuote[index] = index
		}
	}
	closeFrom := make([]int, len(source)+2)
	closeFrom[len(source)] = -1
	closeFrom[len(source)+1] = -1
	for cursor := len(source) - 1; cursor >= 0; cursor-- {
		quote := nextQuote[cursor]
		switch {
		case quote < 0:
			closeFrom[cursor] = -1
		case quote+1 < len(source) && source[quote+1] == '\'':
			closeFrom[cursor] = closeFrom[quote+2]
		default:
			closeFrom[cursor] = quote + 1
		}
	}
	for index := 0; index < len(source); index++ {
		if source[index] == '\'' {
			endings[index] = closeFrom[index+1]
		}
	}
	return endings
}

func nonOverlappingQuotedReplacements(source string, input []quotedValueReplacement) []quotedValueReplacement {
	sort.SliceStable(input, func(left, right int) bool {
		if input[left].start == input[right].start {
			return input[left].end < input[right].end
		}
		return input[left].start < input[right].start
	})
	out := make([]quotedValueReplacement, 0, len(input))
	for _, candidate := range input {
		if len(out) == 0 || candidate.start >= out[len(out)-1].end {
			out = append(out, candidate)
			continue
		}
		// Merge the union, not the shorter candidate. Two grammar
		// interpretations can overlap while each protects a different secret;
		// retaining only one interval would disclose the other.
		if candidate.end > out[len(out)-1].end {
			out[len(out)-1].end = candidate.end
		}
	}
	for index := range out {
		token := source[out[index].start:out[index].end]
		out[index].value = `"` + Redacted + `"` +
			strings.Repeat("\n", strings.Count(token, "\n"))
	}
	return out
}

func normalizedProtectedValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.SliceStable(out, func(left, right int) bool {
		if len(out[left]) == len(out[right]) {
			return out[left] < out[right]
		}
		return len(out[left]) > len(out[right])
	})
	return out
}

func quotedValueContainsProtected(token string, protected []string, budget *int) (bool, bool) {
	if budget == nil || len(token) > *budget {
		return false, true
	}
	*budget -= len(token)
	candidates := []string{token[1 : len(token)-1]}

	var decoded string
	if err := json.Unmarshal([]byte(token), &decoded); err == nil {
		candidates = append(candidates, decoded)
	}
	if decoded, err := strconv.Unquote(token); err == nil {
		candidates = append(candidates, decoded)
	}
	if err := yaml.Unmarshal([]byte(token), &decoded); err == nil {
		candidates = append(candidates, decoded)
	}
	if token[0] == '\'' {
		if decoded, ok := decodeSingleQuotedJavaScript(token); ok {
			candidates = append(candidates, decoded)
		}
		candidates = append(candidates, strings.ReplaceAll(token[1:len(token)-1], "''", "'"))
	}

	for _, candidate := range candidates {
		for _, value := range protected {
			if len(candidate) > *budget {
				return false, true
			}
			*budget -= len(candidate)
			if strings.Contains(candidate, value) {
				return true, false
			}
		}
	}
	return false, false
}

func decodeSingleQuotedJavaScript(token string) (string, bool) {
	if len(token) < 2 || token[0] != '\'' || token[len(token)-1] != '\'' {
		return "", false
	}
	var encoded strings.Builder
	encoded.WriteByte('"')
	inner := token[1 : len(token)-1]
	for index := 0; index < len(inner); index++ {
		value := inner[index]
		if value < 0x20 {
			return "", false
		}
		switch value {
		case '"':
			encoded.WriteString(`\"`)
		case '\\':
			if index+1 >= len(inner) {
				return "", false
			}
			index++
			next := inner[index]
			switch next {
			case '\'':
				encoded.WriteByte('\'')
			case '"':
				encoded.WriteString(`\"`)
			case '\\', '/', 'b', 'f', 'n', 'r', 't':
				encoded.WriteByte('\\')
				encoded.WriteByte(next)
			case 'u':
				if index+4 >= len(inner) {
					return "", false
				}
				encoded.WriteString(`\u`)
				encoded.WriteString(inner[index+1 : index+5])
				index += 4
			case 'x':
				if index+2 >= len(inner) {
					return "", false
				}
				encoded.WriteString(`\u00`)
				encoded.WriteString(inner[index+1 : index+3])
				index += 2
			default:
				return "", false
			}
		default:
			encoded.WriteByte(value)
		}
	}
	encoded.WriteByte('"')
	var decoded string
	if err := json.Unmarshal([]byte(encoded.String()), &decoded); err != nil {
		return "", false
	}
	return decoded, true
}
