package sample

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func TestHARBinaryResourceNeverImplicitlyWireDecoded(t *testing.T) {
	body := []byte(`{"csrf_token":"compressed-export-abcdef123456","label":"日本語"}`)
	writers := map[string]func(io.Writer) io.WriteCloser{
		"gzip":    func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) },
		"deflate": func(w io.Writer) io.WriteCloser { return zlib.NewWriter(w) },
		"raw deflate": func(w io.Writer) io.WriteCloser {
			writer, err := flate.NewWriter(w, flate.DefaultCompression)
			if err != nil {
				t.Fatal(err)
			}
			return writer
		},
		"br": func(w io.Writer) io.WriteCloser { return brotli.NewWriter(w) },
	}
	for name, newWriter := range writers {
		t.Run(name, func(t *testing.T) {
			var encoded bytes.Buffer
			writer := newWriter(&encoded)
			if _, err := writer.Write(body); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			wireEncoding := name
			if name == "raw deflate" {
				wireEncoding = "deflate"
			}
			var diagnostics diag.Collector
			got := decodeHARText(base64.StdEncoding.EncodeToString(encoded.Bytes()), "base64", map[string]string{"Content-Encoding": wireEncoding}, &diagnostics, "HAR entry 1", "response body")
			if got != encoded.String() {
				t.Fatal("HAR storage bytes changed because of a wire encoding header")
			}
			for _, d := range diagnostics.Items() {
				if d.Severity == diag.Warning {
					t.Fatalf("unexpected compression warning: %+v", d)
				}
			}
		})
	}
}

func TestHARCompressedResourceDoesNotExpandFromWireHeader(t *testing.T) {
	encoded := gzipBase64(t, []byte(strings.Repeat("x", maxHARDecodedContentBytes+1)))
	var diagnostics diag.Collector
	got := decodeHARText(encoded, "base64", map[string]string{"Content-Encoding": "gzip"}, &diagnostics, "HAR entry 1", "response body")
	stored, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(stored) {
		t.Fatal("HAR resource bytes were expanded implicitly")
	}
	items := diagnostics.Items()
	if len(items) != 0 {
		t.Fatalf("HAR storage was mistaken for encoded wire content: %+v", items)
	}
}
