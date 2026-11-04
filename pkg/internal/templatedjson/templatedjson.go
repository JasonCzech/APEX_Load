// Package templatedjson reads request bodies that are JSON with template
// expressions where a value goes. Converters write an expression unquoted to
// keep a JSON number a number at runtime ({{fn.timestamp.unix_ms}} for a
// refreshed client timestamp), so the stored body is not JSON until rendered.
package templatedjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// OccurrenceContext describes the lexical context of a {{...}} template
// occurrence inside a JSON body template.
type OccurrenceContext struct {
	InString bool
}

// ClassifyOccurrences scans payload for each [start, end] byte range in matches
// and reports whether each template occurrence sits inside a JSON string literal
// ("...") or in an unquoted JSON value position.
func ClassifyOccurrences(payload string, matches [][]int) ([]OccurrenceContext, error) {
	if len(matches) == 0 {
		return nil, nil
	}
	contexts := make([]OccurrenceContext, len(matches))
	inString, escaped := false, false
	matchIdx := 0
	for i := 0; i < len(payload) && matchIdx < len(matches); i++ {
		if i == matches[matchIdx][0] {
			if escaped {
				return nil, fmt.Errorf("template occurrence %d follows an incomplete JSON string escape", matchIdx)
			}
			contexts[matchIdx] = OccurrenceContext{InString: inString}
			// Advance past the template token itself without changing string state.
			i = matches[matchIdx][1] - 1
			matchIdx++
			continue
		}
		c := payload[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
		}
	}
	return contexts, nil
}

// EscapeStringContent escapes s so it can be safely spliced inside a JSON
// string literal (without adding outer quotes).
func EscapeStringContent(s string) string {
	needsEscape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' || c < 0x20 {
			needsEscape = true
			break
		}
	}
	if !needsEscape && utf8.ValidString(s) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
}

// ValidateUnquotedValue verifies that an unquoted template replacement is a
// single valid JSON value (number, boolean, null, object, array, or quoted string).
func ValidateUnquotedValue(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("unquoted JSON template resolved to invalid JSON value")
	}
	return nil
}

// NullExpressions returns payload with each template expression that stands
// outside a JSON string replaced by null, so the body parses as JSON.
// Expressions inside strings are left as they are. allow, when not nil, is
// asked about each such expression (braces included); ok is false when one is
// not allowed or not closed, so a caller can refuse rather than read a body
// whose expressions it cannot vouch for. A payload without "{{" is returned
// as it is.
func NullExpressions(payload []byte, allow func(expression string) bool) ([]byte, bool) {
	if !bytes.Contains(payload, []byte("{{")) {
		return payload, true
	}
	out := make([]byte, 0, len(payload))
	inString, escaped := false, false
	for i := 0; i < len(payload); i++ {
		c := payload[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
		} else if c == '{' && i+1 < len(payload) && payload[i+1] == '{' {
			end := bytes.Index(payload[i+2:], []byte("}}"))
			if end < 0 {
				return nil, false
			}
			expression := payload[i : i+2+end+2]
			if allow != nil && !allow(string(expression)) {
				return nil, false
			}
			out = append(out, "null"...)
			i += len(expression) - 1
			continue
		}
		out = append(out, c)
	}
	return out, true
}
