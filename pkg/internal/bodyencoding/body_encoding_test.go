package bodyencoding

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestDecodeRepresentationsAndLimits(t *testing.T) {
	binary := []byte{0, 0xff, '{', '}'}
	for _, tc := range []struct {
		name, body, encoding          string
		limit                         int
		want                          []byte
		transformed, invalid, limited bool
	}{
		{name: "binary", body: base64.StdEncoding.EncodeToString(binary), encoding: " BASE64 ", limit: 4, want: binary, transformed: true},
		{name: "empty", encoding: "base64", limit: 1, want: []byte{}, transformed: true},
		{name: "wrapped", body: "  YW\r\nJj\nZA==  ", encoding: "base64", limit: 4, want: []byte("abcd"), transformed: true},
		{name: "unbounded", body: "YWJjZA==", encoding: "base64", want: []byte("abcd"), transformed: true},
		{name: "text", body: "not base64", encoding: "text", limit: 1},
		{name: "unknown representation", body: "not base64", encoding: "windows-1252", limit: 1},
		{name: "invalid", body: "?", encoding: "base64", limit: 4, transformed: true, invalid: true},
		{name: "decoded ceiling", body: "YWJjZA==", encoding: "base64", limit: 3, transformed: true, limited: true},
		{name: "encoded ceiling", body: "YWJjZA==", encoding: "base64", limit: 1, transformed: true, limited: true},
		{name: "wrapped inspection ceiling", body: "Y" + strings.Repeat("\n", 9) + "Q==", encoding: "base64", limit: 1, transformed: true, limited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, transformed, err := Decode(tc.body, tc.encoding, tc.limit)
			if transformed != tc.transformed || (err != nil) != (tc.invalid || tc.limited) || errors.Is(err, ErrLimit) != tc.limited || !bytes.Equal(got, tc.want) {
				t.Fatalf("Decode() = %v, %t, %v; want %v, %t (invalid=%t limited=%t)", got, transformed, err, tc.want, tc.transformed, tc.invalid, tc.limited)
			}
		})
	}
}
