package scrub

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

const maxBodyAssignmentJSONDepth = 4

var (
	bodyDoubleAssignment        = regexp.MustCompile(`(["']?([A-Za-z_][A-Za-z0-9_.-]*)["']?\s*[:=]\s*")((?:\\.|[^"\\\r\n])*)(")`)
	bodySingleAssignment        = regexp.MustCompile(`(["']?([A-Za-z_][A-Za-z0-9_.-]*)["']?\s*[:=]\s*')((?:\\.|[^'\\\r\n])*)(')`)
	bodyEscapedDoubleAssignment = regexp.MustCompile(`((?:\\"|\\'|["'])?([A-Za-z_][A-Za-z0-9_.-]*)(?:\\"|\\'|["'])?\s*[:=]\s*\\")((?:\\\\|\\[^"\\]|[^"\\\r\n])*)(\\")`)
	bodyEscapedSingleAssignment = regexp.MustCompile(`((?:\\"|\\'|["'])?([A-Za-z_][A-Za-z0-9_.-]*)(?:\\"|\\'|["'])?\s*[:=]\s*\\')((?:\\\\|\\[^'\\]|[^'\\\r\n])*)(\\')`)
	trailingAssignmentKey       = regexp.MustCompile(`((?:\\"|\\'|["'])?([A-Za-z_][A-Za-z0-9_.-]*)(?:\\"|\\'|["'])?\s*[:=]\s*)$`)
	jsonEncodedStringLiteral    = regexp.MustCompile(`"(?:\\.|[^"\\\r\n])*"`)
)

func countUnescapedQuoteToLineEnd(value string, pos int, quoteSeq string) int {
	count := 0
	if len(quoteSeq) == 1 {
		q := quoteSeq[0]
		for i := pos; i < len(value); i++ {
			c := value[i]
			if c == '\r' || c == '\n' {
				break
			}
			if c == '\\' {
				i++
				continue
			}
			if c == q {
				count++
			}
		}
		return count
	}
	if len(quoteSeq) == 2 && quoteSeq[0] == '\\' {
		q := quoteSeq[1]
		for i := pos; i < len(value); i++ {
			c := value[i]
			if c == '\r' || c == '\n' {
				break
			}
			if c == '\\' && i+1 < len(value) {
				next := value[i+1]
				if next == '\\' {
					i++
					continue
				}
				if next == q {
					count++
					i++
					continue
				}
			}
		}
	}
	return count
}

func isTokenCharAfterClosingQuote(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') ||
		b == '_' || b == '-' || b == '@' || b == '#' || b == '$'
}

func nextUnescapedQuoteOnLine(value string, pos int, quoteSeq string) (int, int, bool) {
	if len(quoteSeq) == 1 {
		q := quoteSeq[0]
		for i := pos; i < len(value); i++ {
			c := value[i]
			if c == '\r' || c == '\n' {
				return 0, 0, false
			}
			if c == '\\' {
				i++
				continue
			}
			if c == q {
				return i, i + 1, true
			}
		}
		return 0, 0, false
	}
	if len(quoteSeq) == 2 && quoteSeq[0] == '\\' {
		q := quoteSeq[1]
		for i := pos; i < len(value); i++ {
			c := value[i]
			if c == '\r' || c == '\n' {
				return 0, 0, false
			}
			if c == '\\' && i+1 < len(value) {
				next := value[i+1]
				if next == '\\' {
					i++
					continue
				}
				if next == q {
					return i, i + 2, true
				}
			}
		}
	}
	return 0, 0, false
}

func closedLiteralDelimiterEnd(value string, pos int) (int, bool) {
	i := pos
	for i < len(value) && (value[i] == ' ' || value[i] == '\t') {
		i++
	}
	if i >= len(value) {
		return i, true
	}
	switch value[i] {
	case ';', ',', ')', '}', ']', '>':
		return i + 1, true
	case '\r', '\n':
		return i, true
	default:
		return i, false
	}
}

func interiorEndsWithSwallowedSensitiveAssignment(interior, value string, closeEnd int, quoteSeq string) bool {
	if !strings.ContainsAny(interior, ":=") {
		return false
	}
	m := trailingAssignmentKey.FindStringSubmatchIndex(interior)
	if len(m) < 6 || !SensitiveName(interior[m[4]:m[5]]) {
		return false
	}
	nextQuoteStart, nextQuoteEnd, hasNextQuote := nextUnescapedQuoteOnLine(value, closeEnd, quoteSeq)
	if !hasNextQuote {
		return false
	}
	delimEnd, closedAtFirst := closedLiteralDelimiterEnd(value, closeEnd)
	if !closedAtFirst {
		return true
	}
	if _, closedAtSecond := closedLiteralDelimiterEnd(value, nextQuoteEnd); !closedAtSecond {
		return false
	}
	if delimEnd > nextQuoteStart {
		return false
	}
	between := strings.TrimSpace(value[delimEnd:nextQuoteStart])
	return !trailingAssignmentKey.MatchString(between)
}

func redactBodyAssignmentsWithPattern(value string, pattern *regexp.Regexp, quoteSeq string, otherQuote byte) string {
	var out strings.Builder
	cursor := 0
	searchPos := 0
	changed := false
	for searchPos < len(value) {
		rel := pattern.FindStringSubmatchIndex(value[searchPos:])
		if len(rel) < 10 {
			break
		}
		matchEnd := searchPos + rel[1]
		prefixStart := searchPos + rel[2]
		prefixEnd := searchPos + rel[3]
		key := value[searchPos+rel[4] : searchPos+rel[5]]
		interior := value[searchPos+rel[6] : searchPos+rel[7]]
		closeStart := searchPos + rel[8]
		closeEnd := searchPos + rel[9]
		if !SensitiveName(key) {
			hasBoundary := strings.ContainsAny(interior, string(otherQuote)+"<>`") || strings.Contains(interior, "*/")
			unmatchedLineQuote := len(quoteSeq) > 0 && countUnescapedQuoteToLineEnd(value, prefixEnd, quoteSeq)%2 == 0
			tokenFollowsClose := closeEnd < len(value) && isTokenCharAfterClosingQuote(value[closeEnd])
			swallowedSensitive := interiorEndsWithSwallowedSensitiveAssignment(interior, value, closeEnd, quoteSeq)
			if hasBoundary || unmatchedLineQuote || tokenFollowsClose || swallowedSensitive {
				searchPos = prefixEnd
			} else {
				searchPos = matchEnd
			}
			continue
		}
		out.WriteString(value[cursor:prefixStart])
		out.WriteString(value[prefixStart:prefixEnd])
		out.WriteString(Redacted)
		out.WriteString(value[closeStart:closeEnd])
		cursor = matchEnd
		searchPos = matchEnd
		changed = true
	}
	if !changed {
		return value
	}
	out.WriteString(value[cursor:])
	return out.String()
}

func redactPlainBodyAssignments(value string) string {
	value = redactBodyAssignmentsWithPattern(value, bodyDoubleAssignment, `"`, '\'')
	return redactBodyAssignmentsWithPattern(value, bodySingleAssignment, `'`, '"')
}

func quoteJSONStringWithoutHTMLEscape(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func mayContainJSONAssignmentOperator(s string) bool {
	if strings.ContainsAny(s, ":=") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, `\u003d`) || strings.Contains(lower, `\u003a`) ||
		strings.Contains(lower, `\u0000003d`) || strings.Contains(lower, `\u0000003a`) ||
		strings.Contains(lower, `\x3d`) || strings.Contains(lower, `\x3a`)
}

func redactJSONEmbeddedBodyAssignmentsDepth(value string, depth int) string {
	if depth >= maxBodyAssignmentJSONDepth || !strings.Contains(value, `\`) || !mayContainJSONAssignmentOperator(value) {
		return value
	}
	matches := jsonEncodedStringLiteral.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var out strings.Builder
	cursor := 0
	changed := false
	for _, span := range matches {
		token := value[span[0]:span[1]]
		if !strings.Contains(token, `\`) || !mayContainJSONAssignmentOperator(token) {
			continue
		}
		var unquoted string
		if err := json.Unmarshal([]byte(token), &unquoted); err != nil {
			var unquoteErr error
			unquoted, unquoteErr = strconv.Unquote(token)
			if unquoteErr != nil {
				continue
			}
		}
		redacted := redactBodyAssignmentsDepth(unquoted, depth+1)
		if redacted == unquoted {
			continue
		}
		out.WriteString(value[cursor:span[0]])
		out.WriteString(quoteJSONStringWithoutHTMLEscape(redacted))
		cursor = span[1]
		changed = true
	}
	if !changed {
		return value
	}
	out.WriteString(value[cursor:])
	return out.String()
}

func redactBodyAssignmentsDepth(value string, depth int) string {
	value = redactJSONEmbeddedBodyAssignmentsDepth(value, depth)
	value = redactPlainBodyAssignments(value)
	value = redactBodyAssignmentsWithPattern(value, bodyEscapedDoubleAssignment, `\"`, '\'')
	return redactBodyAssignmentsWithPattern(value, bodyEscapedSingleAssignment, `\'`, '"')
}

func redactBodyAssignments(value string) string {
	return redactBodyAssignmentsDepth(value, 0)
}

// BodyText applies credential detection to retained text, including quoted
// credentials in inline JavaScript. Exact runtime values remain the caller's
// responsibility, as with ValueLabel.
func BodyText(value string) string { return ValueLabel(redactBodyAssignments(value), Policy{}) }
