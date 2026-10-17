package httpcontract

import (
	"path"
	"strings"
)

func MatchScopeGlob(pattern, value string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	value = strings.ToLower(strings.TrimSpace(value))
	if pattern == "" {
		return true
	}
	if !strings.ContainsAny(pattern, "*?") {
		return strings.Contains(value, pattern)
	}
	// path.Match treats "/" as a separator for "*" (LoadRunner's "*" spans
	// it) and "[", "]" and "\" as syntax (LoadRunner's are literal).
	var literal strings.Builder
	for _, r := range pattern {
		switch r {
		case '/':
			literal.WriteByte(0)
			continue
		case '[', ']', '\\':
			literal.WriteByte('\\')
		}
		literal.WriteRune(r)
	}
	matched, err := path.Match(literal.String(), strings.ReplaceAll(value, "/", "\x00"))
	return err == nil && matched
}
