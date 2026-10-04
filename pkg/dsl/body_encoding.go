package dsl

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

// ErrBodyEncodingLimit means decoding a body would exceed the caller's
// bounded inspection budget.
var ErrBodyEncodingLimit = bodyencoding.ErrLimit

// DecodeBodyEncoding returns the wire bytes represented by a DSL body. The
// boolean is false when encoding is not base64, in which case callers should
// retain their existing text-body behavior.
//
// maxBytes bounds decoded output before DecodeString allocates it. A value of
// zero preserves the runtime's historical unbounded decode semantics; import
// and persistence boundaries must supply their own conservative limit.
func DecodeBodyEncoding(body, encoding string, maxBytes int) ([]byte, bool, error) {
	return bodyencoding.Decode(body, encoding, maxBytes)
}

// IsTrustedFullBodyBase64Template reports whether body is the legacy deferred
// base64 form accepted by DSL validation. In this form the template resolves
// to encoded data at runtime, so import-time consumers cannot inspect decoded
// wire bytes and must leave the whole governed value intact.
func IsTrustedFullBodyBase64Template(body string) bool {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "{{") || !strings.HasSuffix(body, "}}") {
		return false
	}
	expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body, "{{"), "}}"))
	// Reject composite templates like {{env.X}}<arbitrary>{{secret.Y}}: stripping
	// only the outer braces leaves an inner expression that still starts with
	// env./secret but smuggles additional untrusted content.
	if strings.Contains(expr, "{{") || strings.Contains(expr, "}}") {
		return false
	}
	return strings.HasPrefix(expr, "env.") || strings.HasPrefix(expr, "secret.")
}
