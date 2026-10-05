package enterprisearchive

import (
	"strings"
	"unicode/utf8"
)

// decodeCStringContent decodes the escape sequences of one C string literal
// (the text between its quotes). It accepts every standard C escape, including
// \' and \? that Go's strconv.Unquote rejects, so the parser can decode each
// source literal exactly once. \x reads up to two hex digits and octal up to
// three, producing raw bytes as a C compiler does.
//
// Decoding is all or nothing: an unknown escape (\d, \p, a bare \x) or a
// trailing backslash returns ok=false and the caller keeps the text verbatim.
// Configuration values such as Table="C:\data\file.dat" pass through the same
// literal cleaner, and decoding their valid-looking \f while keeping \d would
// silently corrupt the path.
func decodeCStringContent(content string) (string, bool) {
	if !strings.Contains(content, `\`) {
		return content, true
	}
	var out strings.Builder
	out.Grow(len(content))
	for index := 0; index < len(content); index++ {
		c := content[index]
		if c != '\\' {
			out.WriteByte(c)
			continue
		}
		index++
		if index >= len(content) {
			return content, false
		}
		escape := content[index]
		switch escape {
		case '\\', '"', '\'', '?':
			out.WriteByte(escape)
		case 'a':
			out.WriteByte('\a')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'v':
			out.WriteByte('\v')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			value := 0
			digits := 0
			for digits < 3 && index < len(content) && content[index] >= '0' && content[index] <= '7' {
				value = value*8 + int(content[index]-'0')
				index++
				digits++
			}
			index--
			out.WriteByte(byte(value))
		case 'x':
			value := 0
			digits := 0
			for digits < 2 && index+1 < len(content) && isHexDigit(content[index+1]) {
				index++
				value = value*16 + hexDigitValue(content[index])
				digits++
			}
			if digits == 0 {
				return content, false
			}
			out.WriteByte(byte(value))
		case 'u', 'U':
			width := 4
			if escape == 'U' {
				width = 8
			}
			if index+width < len(content) && allHexDigits(content[index+1:index+1+width]) {
				value := 0
				for _, digit := range []byte(content[index+1 : index+1+width]) {
					value = value*16 + hexDigitValue(digit)
				}
				if utf8.ValidRune(rune(value)) {
					out.WriteRune(rune(value))
					index += width
					continue
				}
			}
			return content, false
		default:
			return content, false
		}
	}
	return out.String(), true
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexDigitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}

func allHexDigits(value string) bool {
	for index := 0; index < len(value); index++ {
		if !isHexDigit(value[index]) {
			return false
		}
	}
	return value != ""
}

// rawSourceArgument returns the source spelling (escapes intact, quotes and
// key removed) of the named key=value call argument whose decoded value is
// decoded. It returns "" when the argument is absent or its raw form does not
// decode back to exactly decoded, so callers never act on a mismatched pair.
func rawSourceArgument(fn *ParsedFunction, key, decoded string) string {
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" || !strings.EqualFold(arg.Name, key) {
			continue
		}
		content, ok := adjacentCStringContent(arg.Raw)
		if !ok {
			return ""
		}
		name, value, found := strings.Cut(content, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), key) {
			return ""
		}
		value = strings.TrimSpace(value)
		if roundTrip, ok := decodeCStringContent(value); !ok || roundTrip != decoded {
			return ""
		}
		return value
	}
	return ""
}

// adjacentCStringContent concatenates the contents of adjacent C string
// literals ("a" "b") without decoding their escapes.
func adjacentCStringContent(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	var out strings.Builder
	found := false
	for index := 0; index < len(trimmed); {
		for index < len(trimmed) && isASCIIWhitespace(trimmed[index]) {
			index++
		}
		if index >= len(trimmed) {
			break
		}
		if trimmed[index] != '"' {
			return "", false
		}
		start := index + 1
		index++
		closed := false
		for index < len(trimmed) {
			if trimmed[index] == '\\' {
				index += 2
				continue
			}
			if trimmed[index] == '"' {
				closed = true
				break
			}
			index++
		}
		if !closed {
			return "", false
		}
		out.WriteString(trimmed[start:index])
		index++
		found = true
	}
	return out.String(), found
}
