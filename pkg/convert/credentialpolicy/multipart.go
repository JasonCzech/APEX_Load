package credentialpolicy

import (
	"fmt"
	"strings"
)

// SanitizeMultipartDispositionToken replaces bytes that could terminate a MIME
// header line with an inert percent-encoded representation. The returned
// boolean reports whether an unsafe control byte was replaced.
//
// This does not quote the token for Content-Disposition. Call
// EscapeMultipartDispositionToken exactly once when serializing it inside a
// quoted name or filename parameter.
func SanitizeMultipartDispositionToken(value string) (string, bool) {
	var out strings.Builder
	changed := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current < 0x20 || current == 0x7f {
			fmt.Fprintf(&out, "%%%02X", current)
			changed = true
			continue
		}
		out.WriteByte(current)
	}
	return out.String(), changed
}

// EscapeMultipartDispositionToken quotes backslash and double quote for one
// Content-Disposition quoted-string occurrence.
func EscapeMultipartDispositionToken(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

// AppendSourceCredentialValue appends a raw source credential to an
// importer-internal protected list without duplicates. Callers must keep the
// returned slice outside every public script, diagnostic, and JSON contract.
func AppendSourceCredentialValue(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
