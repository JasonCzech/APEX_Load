package sample

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// A compressed upload the proxy recorder stored as its wire bytes (the Mobile
// SDK's analytics upload) becomes the readable body, compressed again at
// replay; anything that does not decode completely stays the recorded bytes.
func TestHARCompressedRequestBodyBecomesReadable(t *testing.T) {
	const events = `{"events":[{"name":"screen_view","sessionId":"00D000000000001"}]}`
	compress := func(encoding string, body []byte) []byte {
		var buf bytes.Buffer
		var writer interface {
			Write([]byte) (int, error)
			Close() error
		}
		switch encoding {
		case "gzip":
			writer = gzip.NewWriter(&buf)
		case "zlib":
			writer = zlib.NewWriter(&buf)
		case "raw":
			writer, _ = flate.NewWriter(&buf, flate.DefaultCompression)
		}
		_, _ = writer.Write(body)
		_ = writer.Close()
		return buf.Bytes()
	}
	gzipped := compress("gzip", []byte(events))
	binary := compress("gzip", []byte{0x00, 0x01, 0xff, 0xfe})
	tests := []struct {
		name       string
		coding     string
		body       []byte
		length     bool
		wantBody   string
		wantParam  string
		wantHeader bool
		wantDiag   string
	}{
		{name: "gzip with its length", coding: "gzip", body: gzipped, length: true, wantBody: events, wantParam: "gzip", wantDiag: "captured compressed"},
		{name: "x-gzip without a length", coding: "x-gzip", body: gzipped, wantBody: events, wantParam: "gzip", wantDiag: "captured compressed"},
		{name: "zlib deflate", coding: "deflate", body: compress("zlib", []byte(events)), wantBody: events, wantParam: "deflate", wantDiag: "captured compressed"},
		{name: "raw deflate stays as recorded", coding: "deflate", body: compress("raw", []byte(events)), wantHeader: true},
		{name: "a truncated stream stays as recorded", coding: "gzip", body: gzipped[:len(gzipped)-6], wantHeader: true},
		{name: "a binary payload stays as recorded", coding: "gzip", body: binary, wantHeader: true},
		{name: "brotli stays as recorded", coding: "br", body: gzipped, wantHeader: true},
		{name: "two codings stay as recorded", coding: "gzip, gzip", body: gzipped, wantHeader: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := []map[string]any{header("Content-Type", "application/json"), header("Content-Encoding", tt.coding)}
			if tt.length {
				headers = append(headers, header("Content-Length", itoa(len(tt.body))))
			}
			encoded := base64.StdEncoding.EncodeToString(tt.body)
			result, err := Convert(ImportRequest{Format: "har", Content: harLengthDocument(t, headers, map[string]any{"mimeType": "application/json", "text": encoded, "encoding": "base64"})})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Script.Actions[0]
			wantBody, wantEncoding := tt.wantBody, ""
			if wantBody == "" {
				wantBody, wantEncoding = encoded, "base64"
			}
			if action.Body != wantBody || action.BodyEncoding != wantEncoding {
				t.Fatalf("body = %q (%q), want %q (%q)", action.Body, action.BodyEncoding, wantBody, wantEncoding)
			}
			if got, _ := action.Params[httpprotocol.RequestContentEncodingParam].(string); got != tt.wantParam {
				t.Fatalf("content_encoding = %q, want %q", got, tt.wantParam)
			}
			if got := harActionHasHeader(action, "Content-Encoding"); got != tt.wantHeader {
				t.Fatalf("Content-Encoding header present = %v, want %v", got, tt.wantHeader)
			}
			if tt.wantDiag != "" && !harHasDiagnostic(result.Unsupported, tt.wantDiag, diag.Info) {
				t.Fatalf("missing diagnostic %q: %+v", tt.wantDiag, result.Unsupported)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("invalid draft: %v", issues)
			}
		})
	}
}

// A body the HAR stored decoded is compressed again even when no
// Content-Length confirms it (an HTTP/2 request).
func TestHARDecodedRequestBodyWithoutLengthIsCompressedAgain(t *testing.T) {
	const logLines = `{"logLines":[{"code":"ailtn"}]}`
	for coding, want := range map[string]string{"gzip": "gzip", "zstd": ""} {
		headers := []map[string]any{header("Content-Type", "application/json"), header("Content-Encoding", coding)}
		result, err := Convert(ImportRequest{Format: "har", Content: harLengthDocument(t, headers, map[string]any{"mimeType": "application/json", "text": logLines})})
		if err != nil {
			t.Fatal(err)
		}
		action := result.Script.Actions[0]
		if action.Body != logLines || harActionHasHeader(action, "Content-Encoding") {
			t.Fatalf("%s: body %q, headers %v", coding, action.Body, action.HeaderList)
		}
		if got, _ := action.Params[httpprotocol.RequestContentEncodingParam].(string); got != want {
			t.Fatalf("%s: content_encoding = %q, want %q", coding, got, want)
		}
		severity := diag.Info
		if want == "" {
			severity = diag.Warning
		}
		if !harHasDiagnostic(result.Unsupported, "captured after decoding (Content-Encoding: "+coding+")", severity) {
			t.Fatalf("%s: diagnostics %+v", coding, result.Unsupported)
		}
	}
}

func harActionHasHeader(action dsl.Action, name string) bool {
	for _, h := range action.HeaderList {
		if strings.EqualFold(h.Name, name) {
			return true
		}
	}
	for key := range action.Headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func harHasDiagnostic(diagnostics []diag.Diagnostic, fragment string, severity diag.Severity) bool {
	for _, d := range diagnostics {
		if strings.Contains(d.Detail, fragment) && d.Severity == severity {
			return true
		}
	}
	return false
}

// A HAR's compressed uploads share one decoding budget: every decoded byte
// counts against it, and a body past it stays as its recorded bytes.
func TestHARCompressedRequestBodiesShareADecodingBudget(t *testing.T) {
	compressed := func(body string) exchange {
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		_, _ = writer.Write([]byte(body))
		_ = writer.Close()
		return exchange{
			Body:         base64.StdEncoding.EncodeToString(buf.Bytes()),
			BodyEncoding: "base64",
			HeaderList:   dsl.HeaderList{{Name: "Content-Encoding", Value: "gzip"}},
		}
	}
	var diagnostics diag.Collector
	remaining := int64(30)
	first := compressed(`{"event":"one"}`)
	normalizeHARRequestContentEncoding(&first, &remaining, &diagnostics, "HAR entry 1")
	if first.Body != `{"event":"one"}` || first.ContentEncoding != "gzip" || remaining != 15 {
		t.Fatalf("first: body %q, encoding %q, remaining %d", first.Body, first.ContentEncoding, remaining)
	}
	// Binary bytes are not kept, but decoding them still spends the budget.
	binary := compressed("\x00\x01\x02\x03\x04")
	normalizeHARRequestContentEncoding(&binary, &remaining, &diagnostics, "HAR entry 2")
	if binary.BodyEncoding != "base64" || remaining != 10 {
		t.Fatalf("binary: encoding %q, remaining %d", binary.BodyEncoding, remaining)
	}
	second := compressed(`{"event":"two, longer"}`)
	normalizeHARRequestContentEncoding(&second, &remaining, &diagnostics, "HAR entry 3")
	if second.BodyEncoding != "base64" || second.ContentEncoding != "" || !harActionHasHeader(dsl.Action{HeaderList: second.HeaderList}, "Content-Encoding") {
		t.Fatalf("a body past the budget was decoded: %+v", second)
	}
	if !harHasDiagnostic(diagnostics.Items(), "exceed the 64 MiB it decodes in all", diag.Info) {
		t.Fatalf("diagnostics = %+v", diagnostics.Items())
	}
}
