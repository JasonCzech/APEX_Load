// Package templatedjson reads request bodies that are JSON with template
// expressions where a value goes. Converters write an expression unquoted to
// keep a JSON number a number at runtime ({{fn.timestamp.unix_ms}} for a
// refreshed client timestamp), so the stored body is not JSON until rendered.
package templatedjson

import "bytes"

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
