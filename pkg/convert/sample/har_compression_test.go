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

func TestHARCompressedExporterCompatibility(t *testing.T) {
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
			if got != string(body) {
				t.Fatalf("compressed resource was not decoded: %q", got)
			}
			for _, d := range diagnostics.Items() {
				if d.Severity == diag.Warning {
					t.Fatalf("unexpected compression warning: %+v", d)
				}
			}
		})
	}
}

func TestHARCompressedResponseKeepsExpansionLimit(t *testing.T) {
	encoded := gzipBase64(t, []byte(strings.Repeat("x", maxHARDecodedContentBytes+1)))
	var diagnostics diag.Collector
	got := decodeHARText(encoded, "base64", map[string]string{"Content-Encoding": "gzip"}, &diagnostics, "HAR entry 1", "response body")
	if got != encoded {
		t.Fatal("oversized decoded response escaped the expansion limit")
	}
	items := diagnostics.Items()
	if len(items) != 1 || items[0].Severity != diag.Warning || items[0].Element != "HAR content-encoding" {
		t.Fatalf("limit warning missing: %+v", items)
	}
}
