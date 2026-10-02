package sourceview

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func sourceUTF16(content string, order binary.ByteOrder) string {
	units := utf16.Encode([]rune(content))
	bytes := make([]byte, len(units)*2+2)
	order.PutUint16(bytes, 0xfeff)
	for i, unit := range units {
		order.PutUint16(bytes[2+i*2:], unit)
	}
	return string(bytes)
}

func TestSourceReviewDecodesBOMBeforeApplyingDisplayLimit(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		text := `<truScript><step action="Type" value="review ✓ 😀"/></truScript>`
		raw := sourceUTF16(text, order)
		review := Build(map[string]string{"default.xml": raw, "default.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=1\n"}, []Mapping{{SourceFile: "default.xml", TargetPath: "actions[0]"}})
		if len(review.SourceFiles) != 2 {
			t.Fatalf("source files=%d want readableXML+USP", len(review.SourceFiles))
		}
		for _, file := range review.SourceFiles {
			if file.Path == "default.xml" {
				if file.Content != text || file.OriginalBytes != len(raw) || strings.ContainsRune(file.Content, '\x00') {
					t.Fatalf("XML display lost original byte accounting or Unicode")
				}
			}
			if file.Path == "default.usp" && file.Language != "ini" {
				t.Fatal("run-logic source not navigable as INI")
			}
		}
		bomless := raw[2:]
		bomlessReview := Build(map[string]string{"default.xml": bomless}, nil).SourceFiles[0]
		if bomlessReview.Content != text || bomlessReview.OriginalBytes != len(bomless) {
			t.Fatal("recognized BOM-less TruClient source remains unreadable")
		}
		other, decoded := decodeReviewSourceText("payload.dat", bomless)
		if decoded || other != bomless {
			t.Fatal("binary parameter asset guessed to be TruClient XML")
		}
		if Build(map[string]string{"payload.dat": bomless}, nil) != nil {
			t.Fatal("unrecognized binary parameter asset became source preview")
		}
		large := sourceUTF16(strings.Repeat("a", MaxFileChars+10), order)
		projected := Build(map[string]string{"default.xml": large}, nil).SourceFiles[0]
		if !projected.Truncated || projected.OriginalBytes != len(large) || !strings.HasPrefix(projected.Content, strings.Repeat("a", MaxFileChars)) {
			t.Fatal("UTF16 display cap applied before decoding")
		}
	}
}

func TestSourceReviewDoesNotGuessMissingOrMalformedUTF16Encoding(t *testing.T) {
	for _, raw := range []string{"", "plain text", "a\x00b\x00", "\xff\xfe\x61", "\xff\xfe\x00\xd8", "\xff\xfe\x00\xdc"} {
		value, decoded := decodeBOMSourceText(raw)
		if decoded || value != raw {
			t.Fatal("unmarked or malformed text silently transcoded")
		}
	}
}
