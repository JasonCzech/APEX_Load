package bodyencoding

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Text body encodings applied after template interpolation.
const (
	// CEscaped is a text body whose C escapes (\xNN, octal, \\, \n and the
	// other standard escapes) name raw bytes. LoadRunner's BodyBinary uses this
	// form. Templates are resolved first and their values are sent verbatim:
	// a backslash inside a parameter value is never read as an escape.
	CEscaped = "c_escaped"
	// UTF16LE is a text body sent as UTF-16 little-endian without a byte-order
	// mark, as LoadRunner's BodyUnicode is.
	UTF16LE = "utf-16le"
)

// ErrInvalidEscape means a c_escaped body contains an escape outside the C
// grammar or ends in a lone backslash.
var ErrInvalidEscape = errors.New("invalid C escape in body")

// IsCEscaped and IsUTF16LE compare an authored encoding name.
func IsCEscaped(encoding string) bool {
	return strings.EqualFold(strings.TrimSpace(encoding), CEscaped)
}

func IsUTF16LE(encoding string) bool {
	return strings.EqualFold(strings.TrimSpace(encoding), UTF16LE)
}

// DecodeCEscapes returns the bytes a c_escaped body names. \x reads up to two
// hex digits and octal up to three, as a C compiler does.
func DecodeCEscapes(text string) ([]byte, error) {
	if !strings.Contains(text, `\`) {
		return []byte(text), nil
	}
	out := make([]byte, 0, len(text))
	for index := 0; index < len(text); index++ {
		c := text[index]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		index++
		if index >= len(text) {
			return nil, fmt.Errorf("%w: trailing backslash", ErrInvalidEscape)
		}
		switch escape := text[index]; escape {
		case '\\', '"', '\'', '?':
			out = append(out, escape)
		case 'a':
			out = append(out, '\a')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			value, digits := 0, 0
			for digits < 3 && index < len(text) && text[index] >= '0' && text[index] <= '7' {
				value = value*8 + int(text[index]-'0')
				index++
				digits++
			}
			index--
			if value > 0xff {
				return nil, fmt.Errorf("%w: octal escape out of range", ErrInvalidEscape)
			}
			out = append(out, byte(value))
		case 'x':
			value, digits := 0, 0
			for digits < 2 && index+1 < len(text) && isHexDigit(text[index+1]) {
				index++
				value = value*16 + hexValue(text[index])
				digits++
			}
			if digits == 0 {
				return nil, fmt.Errorf("%w: \\x without hex digits", ErrInvalidEscape)
			}
			out = append(out, byte(value))
		default:
			return nil, fmt.Errorf("%w: \\%c", ErrInvalidEscape, escape)
		}
	}
	return out, nil
}

// EscapeCText escapes a resolved template value so DecodeCEscapes returns it
// unchanged.
func EscapeCText(value string) string {
	return strings.ReplaceAll(value, `\`, `\\`)
}

// EncodeUTF16LE encodes a UTF-8 text body as UTF-16LE. Text that is not valid
// UTF-8 is refused rather than sent with replacement characters.
func EncodeUTF16LE(text string) ([]byte, error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("body is not valid UTF-8 text")
	}
	units := utf16.Encode([]rune(text))
	out := make([]byte, 0, 2*len(units))
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out, nil
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}
