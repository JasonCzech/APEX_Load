package credentialpolicy

import (
	"encoding/base64"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
)

// SourceCredentialValueStatus describes whether source credential material is
// safe to retain or migrate. A disclosure placeholder is evidence that a value
// existed, not the value itself, even when it is wrapped or encoded.
type SourceCredentialValueStatus string

const (
	SourceCredentialValueAvailable           SourceCredentialValueStatus = "available"
	SourceCredentialValueMissing             SourceCredentialValueStatus = "missing"
	SourceCredentialValueRedactedPlaceholder SourceCredentialValueStatus = "redacted_placeholder"
	SourceCredentialValueUnsafeEncoding      SourceCredentialValueStatus = "unsafe_encoding"
)

const sourceCredentialDecodeDepth = 32
const sourceCredentialCandidateLimit = 128

var encodedCredentialCandidatePattern = regexp.MustCompile(`[A-Za-z0-9+/_-]{12,}={0,2}`)

// Available reports whether the exact source bytes may be retained or offered
// for migration.
func (status SourceCredentialValueStatus) Available() bool {
	return status == SourceCredentialValueAvailable
}

// ClassifySourceCredentialValue recognizes the repository disclosure sentinel
// case-insensitively through common source encodings. Decoding is bounded and
// fail-closed: an encoding chain that keeps changing beyond the supported depth
// is not treated as a real credential.
func ClassifySourceCredentialValue(value string) SourceCredentialValueStatus {
	if strings.TrimSpace(value) == "" {
		return SourceCredentialValueMissing
	}
	candidates := []string{value}
	seen := map[string]struct{}{value: {}}
	for depth := 0; depth < sourceCredentialDecodeDepth; depth++ {
		next := make([]string, 0, len(candidates)*2)
		for _, candidate := range candidates {
			if containsDisclosureSentinel(candidate) {
				return SourceCredentialValueRedactedPlaceholder
			}
			for _, decoded := range sourceCredentialDecodings(candidate) {
				if decoded == candidate {
					continue
				}
				if containsDisclosureSentinel(decoded) {
					return SourceCredentialValueRedactedPlaceholder
				}
				if _, duplicate := seen[decoded]; duplicate {
					continue
				}
				if len(seen) >= sourceCredentialCandidateLimit {
					return SourceCredentialValueUnsafeEncoding
				}
				seen[decoded] = struct{}{}
				next = append(next, decoded)
			}
		}
		if len(next) == 0 {
			return SourceCredentialValueAvailable
		}
		candidates = next
	}
	// More decoding work after the fixed bound is ambiguous. Refuse to migrate
	// it rather than allowing an over-depth placeholder to become executable.
	for _, candidate := range candidates {
		if len(sourceCredentialDecodings(candidate)) > 0 {
			return SourceCredentialValueUnsafeEncoding
		}
	}
	return SourceCredentialValueAvailable
}

// RecoverableSourceCredentialValue returns the original bytes only when they
// represent an available credential. Callers must never substitute a decoded
// form because encoding can be part of the source protocol contract.
func RecoverableSourceCredentialValue(value string) (string, bool) {
	if !ClassifySourceCredentialValue(value).Available() {
		return "", false
	}
	return value, true
}

func containsDisclosureSentinel(value string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(credential.Redacted))
}

func sourceCredentialDecodings(value string) []string {
	decoded := make([]string, 0, 8)
	appendDecoded := func(candidate string) {
		if candidate == "" || candidate == value {
			return
		}
		for _, prior := range decoded {
			if prior == candidate {
				return
			}
		}
		decoded = append(decoded, candidate)
	}

	// Go returns the safely decoded prefix alongside malformed percent-escape
	// errors. Inspect that prefix too, so "%5BREDACTED%5D%ZZ" cannot pass as a
	// usable credential merely because its suffix is malformed.
	if candidate, _ := url.QueryUnescape(value); candidate != "" {
		appendDecoded(candidate)
	}
	if candidate, _ := url.PathUnescape(value); candidate != "" {
		appendDecoded(candidate)
	}
	appendDecoded(tolerantPercentDecode(value))
	appendDecoded(html.UnescapeString(value))
	appendDecoded(decodeCredentialBackslashEscapes(value))

	trimmed := strings.TrimSpace(value)
	appendCredentialBase64Decodings(trimmed, appendDecoded)
	for _, token := range encodedCredentialCandidatePattern.FindAllString(value, -1) {
		appendCredentialBase64Decodings(token, appendDecoded)
	}
	return decoded
}

func tolerantPercentDecode(value string) string {
	var out strings.Builder
	changed := false
	for offset := 0; offset < len(value); {
		if value[offset] == '%' && offset+2 < len(value) {
			decoded, err := strconv.ParseUint(value[offset+1:offset+3], 16, 8)
			if err == nil {
				out.WriteByte(byte(decoded))
				offset += 3
				changed = true
				continue
			}
		}
		out.WriteByte(value[offset])
		offset++
	}
	if !changed {
		return value
	}
	return out.String()
}

func appendCredentialBase64Decodings(value string, appendDecoded func(string)) {
	if len(value) < 8 {
		return
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err != nil {
			continue
		}
		if utf8.Valid(decoded) && mostlyText(decoded) {
			appendDecoded(string(decoded))
		}
		for _, decodedUTF16 := range decodeCredentialUTF16(decoded) {
			appendDecoded(decodedUTF16)
		}
	}
}

// decodeCredentialUTF16 accepts only structurally valid UTF-16 byte streams.
// This catches encoded disclosure placeholders without treating arbitrary
// NUL-separated bytes as text or weakening binary credential handling.
func decodeCredentialUTF16(value []byte) []string {
	if len(value) < 2 || len(value)%2 != 0 {
		return nil
	}
	type byteOrder struct {
		little bool
		skip   int
	}
	orders := make([]byteOrder, 0, 2)
	switch {
	case value[0] == 0xff && value[1] == 0xfe:
		orders = append(orders, byteOrder{little: true, skip: 2})
	case value[0] == 0xfe && value[1] == 0xff:
		orders = append(orders, byteOrder{little: false, skip: 2})
	default:
		// Without a BOM, require a consistent ASCII zero-byte lane. This is
		// positive UTF-16 structure, not heuristic NUL stripping.
		littleEvidence, bigEvidence := true, true
		for offset := 0; offset < len(value); offset += 2 {
			littleEvidence = littleEvidence && value[offset+1] == 0
			bigEvidence = bigEvidence && value[offset] == 0
		}
		if littleEvidence {
			orders = append(orders, byteOrder{little: true})
		}
		if bigEvidence {
			orders = append(orders, byteOrder{little: false})
		}
	}
	result := make([]string, 0, len(orders))
	for _, order := range orders {
		bytes := value[order.skip:]
		if len(bytes) == 0 || len(bytes)%2 != 0 {
			continue
		}
		units := make([]uint16, len(bytes)/2)
		valid := true
		for index := range units {
			offset := index * 2
			if order.little {
				units[index] = uint16(bytes[offset]) | uint16(bytes[offset+1])<<8
			} else {
				units[index] = uint16(bytes[offset])<<8 | uint16(bytes[offset+1])
			}
		}
		runes := make([]rune, 0, len(units))
		for index := 0; index < len(units); index++ {
			unit := rune(units[index])
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if index+1 >= len(units) {
					valid = false
					break
				}
				low := rune(units[index+1])
				if low < 0xdc00 || low > 0xdfff {
					valid = false
					break
				}
				runes = append(runes, utf16.DecodeRune(unit, low))
				index++
			case unit >= 0xdc00 && unit <= 0xdfff:
				valid = false
			default:
				runes = append(runes, unit)
			}
			if !valid {
				break
			}
		}
		if valid {
			result = append(result, string(runes))
		}
	}
	return result
}

func mostlyText(value []byte) bool {
	for _, current := range string(value) {
		if current == '\n' || current == '\r' || current == '\t' || current >= ' ' {
			continue
		}
		return false
	}
	return true
}

func decodeCredentialBackslashEscapes(value string) string {
	var out strings.Builder
	changed := false
	for offset := 0; offset < len(value); {
		if value[offset] != '\\' || offset+1 >= len(value) {
			out.WriteByte(value[offset])
			offset++
			continue
		}
		switch value[offset+1] {
		case 'u':
			if offset+6 <= len(value) {
				first, err := strconv.ParseUint(value[offset+2:offset+6], 16, 16)
				if err == nil {
					r := rune(first)
					consumed := 6
					if utf16.IsSurrogate(r) && offset+12 <= len(value) && value[offset+6:offset+8] == `\u` {
						second, secondErr := strconv.ParseUint(value[offset+8:offset+12], 16, 16)
						if secondErr == nil {
							if combined := utf16.DecodeRune(r, rune(second)); combined != utf8.RuneError {
								r = combined
								consumed = 12
							}
						}
					}
					out.WriteRune(r)
					offset += consumed
					changed = true
					continue
				}
			}
		case 'x':
			if offset+4 <= len(value) {
				decoded, err := strconv.ParseUint(value[offset+2:offset+4], 16, 8)
				if err == nil {
					out.WriteByte(byte(decoded))
					offset += 4
					changed = true
					continue
				}
			}
		case '\\', '/', '"':
			out.WriteByte(value[offset+1])
			offset += 2
			changed = true
			continue
		case 'n':
			out.WriteByte('\n')
			offset += 2
			changed = true
			continue
		case 'r':
			out.WriteByte('\r')
			offset += 2
			changed = true
			continue
		case 't':
			out.WriteByte('\t')
			offset += 2
			changed = true
			continue
		}
		out.WriteByte(value[offset])
		offset++
	}
	if !changed {
		return value
	}
	return out.String()
}
