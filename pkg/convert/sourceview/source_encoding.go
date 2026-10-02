package sourceview

import (
	"encoding/binary"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"strings"
	"unicode/utf16"
)

func decodeReviewSourceText(name, content string) (string, bool) {
	if decoded, ok := decodeBOMSourceText(content); ok {
		return decoded, true
	}
	// VuGen TruClient XML also arrives without a BOM. The fixed UTF-16
	// root signature is sufficient for this display-only conversion; arbitrary
	// NUL-bearing source and other file kinds remain untouched.
	if strings.EqualFold(archiveingest.LogicalExt(name), ".xml") {
		if strings.HasPrefix(content, "<\x00t\x00r\x00u\x00S\x00c\x00r\x00i\x00p\x00t\x00") {
			return decodeUTF16SourcePrefix(content, binary.LittleEndian, 0)
		}
		if strings.HasPrefix(content, "\x00<\x00t\x00r\x00u\x00S\x00c\x00r\x00i\x00p\x00t") {
			return decodeUTF16SourcePrefix(content, binary.BigEndian, 0)
		}
	}
	return content, false
}

// Decode only an explicit UTF-16 BOM. This is a display projection; callers
// retain the untouched source bundle and OriginalBytes counts independently.
func decodeBOMSourceText(content string) (string, bool) {
	original := content
	if len(content) < 2 {
		return content, false
	}
	var order binary.ByteOrder
	switch {
	case content[0] == 0xff && content[1] == 0xfe:
		order = binary.LittleEndian
	case content[0] == 0xfe && content[1] == 0xff:
		order = binary.BigEndian
	default:
		return content, false
	}
	return decodeUTF16SourcePrefix(original, order, 2)
}

func decodeUTF16SourcePrefix(original string, order binary.ByteOrder, bomBytes int) (string, bool) {
	content := original[bomBytes:]
	if len(content)%2 != 0 {
		return original, false
	}
	limit := min(len(content)/2, MaxFileChars+2)
	words := make([]uint16, 0, limit+1)
	for i := 0; i < limit; i++ {
		words = append(words, order.Uint16([]byte(content[i*2:i*2+2])))
	}
	if len(words) > 0 && words[len(words)-1] >= 0xd800 && words[len(words)-1] <= 0xdbff && limit < len(content)/2 {
		words = append(words, order.Uint16([]byte(content[limit*2:limit*2+2])))
	}
	for i := 0; i < len(words); i++ {
		word := words[i]
		if word >= 0xd800 && word <= 0xdbff {
			if i+1 >= len(words) || words[i+1] < 0xdc00 || words[i+1] > 0xdfff {
				return original, false
			}
			i++
		} else if word >= 0xdc00 && word <= 0xdfff {
			return original, false
		}
	}
	return string(utf16.Decode(words)), true
}
