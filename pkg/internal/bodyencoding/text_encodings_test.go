package bodyencoding

import (
	"bytes"
	"errors"
	"testing"
)

func TestDecodeCEscapes(t *testing.T) {
	tests := []struct {
		text string
		want []byte
		bad  bool
	}{
		{text: `plain`, want: []byte("plain")},
		{text: `\x00\x01ABC\xFF`, want: []byte{0, 1, 'A', 'B', 'C', 0xff}},
		{text: `\xfg`, want: []byte{0x0f, 'g'}},
		{text: `\101\0\177`, want: []byte{'A', 0, 0x7f}},
		{text: `a\\b\"\r\n\t`, want: []byte("a\\b\"\r\n\t")},
		{text: `\400`, bad: true},
		{text: `\x`, bad: true},
		{text: `\d`, bad: true},
		{text: `tail\`, bad: true},
	}
	for _, tt := range tests {
		got, err := DecodeCEscapes(tt.text)
		if tt.bad {
			if !errors.Is(err, ErrInvalidEscape) {
				t.Errorf("DecodeCEscapes(%q) error = %v, want ErrInvalidEscape", tt.text, err)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, tt.want) {
			t.Errorf("DecodeCEscapes(%q) = %v, %v; want %v", tt.text, got, err, tt.want)
		}
	}
}

func TestEscapeCTextRoundTrips(t *testing.T) {
	for _, value := range []string{`C:\data\x41`, `\\`, "no escapes", ""} {
		got, err := DecodeCEscapes(EscapeCText(value))
		if err != nil || string(got) != value {
			t.Errorf("round trip of %q = %q, %v", value, got, err)
		}
	}
}

func TestEncodeUTF16LE(t *testing.T) {
	got, err := EncodeUTF16LE("hé😀")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'h', 0, 0xe9, 0, 0x3d, 0xd8, 0x00, 0xde}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeUTF16LE = % x, want % x", got, want)
	}
	if _, err := EncodeUTF16LE("\xff"); err == nil {
		t.Fatal("invalid UTF-8 must be refused")
	}
}
