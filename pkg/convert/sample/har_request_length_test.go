package sample

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type harLengthCase struct {
	name         string
	headers      []map[string]any
	postData     map[string]any
	wantRemoved  bool
	wantBody     string
	wantEncoding string
	wantHeader   map[string]bool // header name -> must still be present
	wantDiag     string          // substring of a non-blocking diagnostic
	wantSeverity diag.Severity
}

func harLengthDocument(t *testing.T, headers []map[string]any, postData map[string]any) string {
	t.Helper()
	list := make([]any, 0, len(headers))
	for _, header := range headers {
		list = append(list, header)
	}
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{map[string]any{
		"request":  map[string]any{"method": "POST", "url": "https://example.invalid/submit", "httpVersion": "HTTP/1.1", "headers": list, "postData": postData},
		"response": map[string]any{"status": 200, "httpVersion": "HTTP/1.1", "content": map[string]any{"text": "{}"}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// latin1 spells raw bytes the way Chrome stores a binary postData body: one
// character per byte.
func latin1(raw []byte) string {
	runes := make([]rune, len(raw))
	for index, b := range raw {
		runes[index] = rune(b)
	}
	return string(runes)
}

// Recorders store request bodies in forms whose UTF-8 length no longer equals
// the recorded Content-Length even though nothing was lost. Those lengths are
// normalized; a body whose bytes really were lost keeps the review gate.
func TestHARRecordedContentLengthExplainedByCaptureStorage(t *testing.T) {
	auraFields := []any{
		map[string]any{"name": "message", "value": `{"actions":[{"params":{"label":"Rep's (main) store!"}}]}`},
		map[string]any{"name": "aura.token", "value": "eyJub25jZSI6InRva2VuIn0"},
	}
	// encodeURIComponent leaves ' ( ) ! unescaped where Go writes %27 %28 %29 %21.
	auraLength := len("message=") + len(`%7B%22actions%22%3A%5B%7B%22params%22%3A%7B%22label%22%3A%22Rep's%20(main)%20store!%22%7D%7D%5D%7D`) + len("&aura.token=eyJub25jZSI6InRva2VuIn0")
	oauthFields := []any{
		map[string]any{"name": "redirect_uri", "value": "https://mobileauth.example.invalid/oauth/done"},
		map[string]any{"name": "code", "value": "aPrx.yPFu=="},
	}
	// The iOS query encoding leaves : and / unescaped and escapes =.
	oauthLength := len("redirect_uri=https://mobileauth.example.invalid/oauth/done&code=aPrx.yPFu%3D%3D")
	binary := []byte{0x78, 0x9c, 0xec, 0xbd, 0x00, 0xff, 0x0a, 0x41}
	decodedJSON := `{"logLines":[{"code":"ailtn","data":{"schemaType":"LightningInteraction"}}]}`

	tests := []harLengthCase{
		{
			name:        "form params under encodeURIComponent",
			headers:     []map[string]any{header("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8"), header("Content-Length", itoa(auraLength))},
			postData:    map[string]any{"mimeType": "application/x-www-form-urlencoded", "params": auraFields},
			wantRemoved: true,
			wantDiag:    "encodeURIComponent",
		},
		{
			name:        "form params under RFC 3986 query encoding",
			headers:     []map[string]any{header("Content-Type", "application/x-www-form-urlencoded"), header("Content-Length", itoa(oauthLength))},
			postData:    map[string]any{"mimeType": "application/x-www-form-urlencoded", "params": oauthFields},
			wantRemoved: true,
			wantDiag:    "RFC 3986 query component",
		},
		{
			name:     "form params matching no serializer",
			headers:  []map[string]any{header("Content-Type", "application/x-www-form-urlencoded"), header("Content-Length", itoa(oauthLength+7))},
			postData: map[string]any{"mimeType": "application/x-www-form-urlencoded", "params": oauthFields},
		},
		{
			name:     "form text is never reinterpreted",
			headers:  []map[string]any{header("Content-Type", "application/x-www-form-urlencoded"), header("Content-Length", itoa(oauthLength))},
			postData: map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "redirect_uri=https%3A%2F%2Fmobileauth.example.invalid%2Foauth%2Fdone&code=aPrx.yPFu%3D%3D", "params": oauthFields},
		},
		{
			name:         "binary body stored one byte per character",
			headers:      []map[string]any{header("Content-Type", "text/plain"), header("Content-Length", itoa(len(binary)))},
			postData:     map[string]any{"mimeType": "text/plain", "text": latin1(binary)},
			wantRemoved:  true,
			wantBody:     base64.StdEncoding.EncodeToString(binary),
			wantEncoding: "base64",
			wantDiag:     "one byte per character",
		},
		{
			name:        "Latin-1 text body stored one byte per character",
			headers:     []map[string]any{header("Content-Type", "text/plain"), header("Content-Length", "5")},
			postData:    map[string]any{"mimeType": "text/plain", "text": "café!"},
			wantRemoved: true,
			// 0xE9 alone is not UTF-8, so the exact bytes travel as base64.
			wantBody:     base64.StdEncoding.EncodeToString([]byte{'c', 'a', 'f', 0xe9, '!'}),
			wantEncoding: "base64",
			wantDiag:     "one byte per character",
		},
		{
			name:         "body decoded from a compressed upload",
			headers:      []map[string]any{header("Content-Type", "application/json"), header("Content-Encoding", "gzip"), header("Content-Length", "61")},
			postData:     map[string]any{"mimeType": "application/json", "text": decodedJSON},
			wantRemoved:  true,
			wantHeader:   map[string]bool{"Content-Encoding": false},
			wantDiag:     "replay compresses it with gzip again",
			wantSeverity: diag.Info,
		},
		{
			name:         "body decoded from a brotli upload",
			headers:      []map[string]any{header("Content-Type", "application/json"), header("Content-Encoding", "br"), header("Content-Length", "61")},
			postData:     map[string]any{"mimeType": "application/json", "text": decodedJSON},
			wantRemoved:  true,
			wantHeader:   map[string]bool{"Content-Encoding": false},
			wantDiag:     "replay sends it uncompressed",
			wantSeverity: diag.Warning,
		},
		{
			name:       "unknown content coding keeps the gate",
			headers:    []map[string]any{header("Content-Type", "application/json"), header("Content-Encoding", "aes128gcm"), header("Content-Length", "61")},
			postData:   map[string]any{"mimeType": "application/json", "text": decodedJSON},
			wantHeader: map[string]bool{"Content-Encoding": true},
		},
		{
			name:         "binary bytes lost when the HAR was written",
			headers:      []map[string]any{header("Content-Type", "text/plain"), header("Content-Length", "6")},
			postData:     map[string]any{"mimeType": "text/plain", "text": "x^�V[o�"},
			wantDiag:     "binary bytes were lost",
			wantSeverity: diag.Warning,
		},
		{
			name:     "compressed upload with lost bytes keeps the gate",
			headers:  []map[string]any{header("Content-Type", "multipart/form-data; boundary=b"), header("Content-Encoding", "gzip"), header("Content-Length", "9")},
			postData: map[string]any{"mimeType": "multipart/form-data; boundary=b", "text": "--b\r\n\r\n��\r\n--b--\r\n"},
			// Content-Encoding is kept with the gate: the stored body is
			// neither the wire bytes nor recoverable text.
			wantHeader:   map[string]bool{"Content-Encoding": true},
			wantDiag:     "binary bytes were lost",
			wantSeverity: diag.Warning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Format: "har", Content: harLengthDocument(t, tt.headers, tt.postData)})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Script.Actions[0]
			lengthBlocker := false
			diagnosed := false
			for _, d := range result.Unsupported {
				if d.Element == "Content-Length" && d.Severity == diag.Blocker {
					lengthBlocker = true
				}
				if tt.wantDiag != "" && strings.Contains(d.Detail, tt.wantDiag) {
					if d.Severity == diag.Blocker {
						t.Fatalf("explanatory diagnostic must not add a blocker: %+v", d)
					}
					if tt.wantSeverity != "" && d.Severity != tt.wantSeverity {
						t.Fatalf("diagnostic severity=%s, want %s", d.Severity, tt.wantSeverity)
					}
					diagnosed = true
				}
			}
			if lengthBlocker == tt.wantRemoved {
				t.Fatalf("Content-Length blocker=%v, want %v (diagnostics %+v)", lengthBlocker, !tt.wantRemoved, result.Unsupported)
			}
			if tt.wantDiag != "" && !diagnosed {
				t.Fatalf("missing diagnostic containing %q: %+v", tt.wantDiag, result.Unsupported)
			}
			// A kept length is omitted from the action by the shared fidelity
			// pipeline, which records it and raises the blocker checked above.
			for _, h := range action.HeaderList {
				if strings.EqualFold(h.Name, "Content-Length") {
					t.Fatal("recorded Content-Length reached the replayed header list")
				}
			}
			for name, want := range tt.wantHeader {
				present := false
				for _, h := range action.HeaderList {
					if strings.EqualFold(h.Name, name) {
						present = true
					}
				}
				if present != want {
					t.Fatalf("%s present=%v, want %v", name, present, want)
				}
			}
			if tt.wantBody != "" && (action.Body != tt.wantBody || action.BodyEncoding != tt.wantEncoding) {
				t.Fatalf("body=%q encoding=%q, want %q %q", action.Body, action.BodyEncoding, tt.wantBody, tt.wantEncoding)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("invalid draft: %v", issues)
			}
		})
	}
}

func TestHARFormEncodingLengths(t *testing.T) {
	fields := [][2]string{{"a b", "x*y~z'(!)"}, {"k", "é"}}
	want := map[string]int64{
		// a+b=x*y%7Ez%27%28%21%29&k=%C3%A9
		"WHATWG form": int64(len("a+b=x*y%7Ez%27%28%21%29&k=%C3%A9")),
		// a%20b=x*y~z'(!)&k=%C3%A9
		"encodeURIComponent": int64(len("a%20b=x*y~z'(!)&k=%C3%A9")),
		// a%20b=x*y~z'(!)&k=%C3%A9
		"RFC 3986 query component": int64(len("a%20b=x*y~z'(!)&k=%C3%A9")),
	}
	for _, encoding := range harEquivalentFormEncodings {
		if got := encoding.length(fields); got != want[encoding.name] {
			t.Fatalf("%s length=%d, want %d", encoding.name, got, want[encoding.name])
		}
	}
}

func itoa(value int) string {
	data, _ := json.Marshal(value)
	return string(data)
}
