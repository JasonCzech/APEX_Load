package sample

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

// Browser/VuGen HAR exports retain wire Content-Encoding headers even though
// content.text already contains the decompressed resource (HAR 1.2).
func TestHARAlreadyDecodedResponseCorrelates(t *testing.T) {
	for _, encoding := range []string{"", "Raw", "base64"} {
		for _, wireEncoding := range []string{"gzip", "deflate", "br", "zstd"} {
			t.Run(encoding+"/"+wireEncoding, func(t *testing.T) {
				body := `{"csrf_token":"har-decoded-token-abcdef123456"}`
				text := body
				if encoding == "base64" {
					text = base64.StdEncoding.EncodeToString([]byte(body))
				}
				entries := []any{
					map[string]any{"request": map[string]any{"method": "GET", "url": "https://example.invalid/form", "postData": nil}, "response": map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Encoding", "value": wireEncoding}}, "content": map[string]any{"text": text, "encoding": encoding}}},
					map[string]any{"request": map[string]any{"method": "POST", "url": "https://example.invalid/form", "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "csrf_token=har-decoded-token-abcdef123456"}}, "response": map[string]any{"status": 200, "content": map[string]any{"text": "{}"}}},
				}
				data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": entries}})
				if err != nil {
					t.Fatal(err)
				}
				baseline, err := HARCorrelationBaseline(string(data), false)
				if err != nil {
					t.Fatal(err)
				}
				if baseline[0].ResponseBody != body {
					t.Fatalf("baseline response = %q, want decoded resource", baseline[0].ResponseBody)
				}
				result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
				if err != nil {
					t.Fatal(err)
				}
				if result.Stats.CorrelationsDetected != 1 || !strings.Contains(result.Script.Actions[1].Body, "{{var.corr_csrf_token}}") {
					t.Fatalf("decoded response did not correlate: stats=%+v body=%q", result.Stats, result.Script.Actions[1].Body)
				}
				for _, d := range result.Unsupported {
					if d.Element == "HAR content-encoding" && d.Severity == diag.Warning {
						t.Fatalf("decoded content produced a false compression warning: %+v", d)
					}
				}
			})
		}
	}
}

func TestHARDecodedResponsePreservesUTF8Whitespace(t *testing.T) {
	body := "\t\r\n<html>Résumé 日本語</html>\n"
	var diagnostics diag.Collector
	got := decodeHARText(base64.StdEncoding.EncodeToString([]byte(body)), "base64", map[string]string{"Content-Encoding": "gzip"}, &diagnostics, "HAR entry 1", "response body")
	if got != body {
		t.Fatalf("decoded body = %q, want %q", got, body)
	}
	if len(diagnostics.Items()) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics.Items())
	}
}

func TestHARCorruptCompressedResponseStillWarns(t *testing.T) {
	var diagnostics diag.Collector
	encoded := base64.StdEncoding.EncodeToString([]byte{0x1f, 0x8b, 0x08, 0x00, 0xff})
	got := decodeHARText(encoded, "base64", map[string]string{"Content-Encoding": "gzip"}, &diagnostics, "HAR entry 1", "response body")
	if got != encoded {
		t.Fatalf("corrupt compressed body changed: %q", got)
	}
	items := diagnostics.Items()
	if len(items) != 1 || items[0].Element != "HAR content-encoding" || items[0].Severity != diag.Warning {
		t.Fatalf("diagnostics = %+v, want compression warning", items)
	}
}

func TestHARUnicodeControlsAreAlreadyDecoded(t *testing.T) {
	// VuGen JavaScript resources can contain UTF-8 C1 characters inside
	// literals, whether the exporter uses Raw text or HAR base64 encoding.
	body := "const label = \"\u009d\u0090\u0081\";"
	for _, encoding := range []string{"", "Raw", "base64"} {
		for _, wireEncoding := range []string{"gzip", "deflate", "br", "zstd"} {
			t.Run(encoding+"/"+wireEncoding, func(t *testing.T) {
				text := body
				if encoding == "base64" {
					text = base64.StdEncoding.EncodeToString([]byte(body))
				}
				var diagnostics diag.Collector
				got := decodeHARText(text, encoding, map[string]string{"Content-Encoding": wireEncoding}, &diagnostics, "HAR entry 1", "response body")
				if got != body || len(diagnostics.Items()) != 0 {
					t.Fatalf("decoded Unicode resource changed or warned: %q %+v", got, diagnostics.Items())
				}
			})
		}
	}
}
