package dsl

import (
	"encoding/base64"
	"errors"
	"strings"
)

// ErrBodyEncodingLimit means decoding a body would exceed the caller's
// bounded inspection budget.
var ErrBodyEncodingLimit = errors.New("decoded body exceeds inspection limit")

// DecodeBodyEncoding returns the wire bytes represented by a DSL body. The
// boolean is false when encoding is not base64, in which case callers should
// retain their existing text-body behavior.
//
// maxBytes bounds decoded output before DecodeString allocates it. A value of
// zero preserves the runtime's historical unbounded decode semantics; import
// and persistence boundaries must supply their own conservative limit.
func DecodeBodyEncoding(body, encoding string, maxBytes int) ([]byte, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		return nil, false, nil
	}
	encoded := strings.TrimSpace(body)
	if maxBytes > 0 {
		maxEncodedBytes := base64.StdEncoding.EncodedLen(maxBytes)
		// Permit conventional wrapped base64 while placing a hard ceiling on
		// whitespace-only work before the meaningful-byte count below.
		if len(encoded) > maxEncodedBytes*2 {
			return nil, true, ErrBodyEncodingLimit
		}
		// encoding/base64 ignores CR and LF. Count only meaningful input bytes
		// so wrapped-but-valid base64 receives the same bound as canonical text.
		encodedBytes := 0
		for index := 0; index < len(encoded); index++ {
			if encoded[index] != '\r' && encoded[index] != '\n' {
				encodedBytes++
			}
		}
		if encodedBytes > maxEncodedBytes {
			return nil, true, ErrBodyEncodingLimit
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, true, err
	}
	if maxBytes > 0 && len(decoded) > maxBytes {
		return nil, true, ErrBodyEncodingLimit
	}
	return decoded, true, nil
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
