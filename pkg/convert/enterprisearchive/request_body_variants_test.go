package enterprisearchive

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRebuildSourceBinaryBodies(t *testing.T) {
	tests := []struct {
		name     string
		call     string
		encoding string
		body     string
		bytes    []byte
		reason   string
		cp1252   bool
	}{
		{
			name:     "fixed bytes become base64",
			call:     `web_custom_request("b", "URL=https://x.test/", "Method=POST", "BodyBinary=\\x00\\x01ABC\\xFF", LAST);`,
			encoding: "base64",
			bytes:    []byte{0, 1, 'A', 'B', 'C', 0xff},
		},
		{
			name:     "parameters keep the escaped text for the runtime",
			call:     `web_custom_request("b", "URL=https://x.test/", "Method=POST", "BodyBinary=\\x02{Token}\\x03", LAST);`,
			encoding: "c_escaped",
			body:     `\x02{{var.token}}\x03`,
		},
		{
			name:     "body segments are literal text joined in order",
			call:     `web_custom_request("b", "URL=https://x.test/", "Method=POST", "Body=C:\\dir", "BodyBinary=\\x00", LAST);`,
			encoding: "base64",
			bytes:    []byte("C:\\dir\x00"),
		},
		{
			name:     "code-page scripts send code-page bytes",
			call:     `web_custom_request("b", "URL=https://x.test/", "Method=POST", "Body=café", "BodyBinary=\\x00", LAST);`,
			encoding: "base64",
			bytes:    []byte("caf\xe9\x00"),
			cp1252:   true,
		},
		{
			name:     "code-page scripts send parameter values as code-page bytes",
			call:     `web_custom_request("b", "URL=https://x.test/", "Method=POST", "Body=café {Name}", "BodyBinary=\\x00\\xE8", LAST);`,
			encoding: "windows-1252",
			body:     "café {{var.name}}\x00è",
			cp1252:   true,
		},
		{
			name:     "code-page bytes without a character stay escaped",
			call:     `web_custom_request("b", "URL=https://x.test/", "Method=POST", "BodyBinary=\\x81{Name}", LAST);`,
			encoding: "c_escaped",
			body:     `\x81{{var.name}}`,
			cp1252:   true,
		},
		{
			name:   "an unknown escape keeps the gate",
			call:   `web_custom_request("b", "URL=https://x.test/", "Method=POST", "BodyBinary=\\q", LAST);`,
			reason: "escape the importer does not recognize",
		},
		{
			name:     "unicode bodies are sent as UTF-16LE after interpolation",
			call:     `web_custom_request("u", "URL=https://x.test/", "Method=POST", "BodyUnicode=hi {Name}", LAST);`,
			encoding: "utf-16le",
			body:     "hi {{var.name}}",
		},
		{
			name:   "unicode mixed with other segments keeps the gate",
			call:   `web_custom_request("u", "URL=https://x.test/", "Method=POST", "Body=a", "BodyUnicode=b", LAST);`,
			reason: "BodyUnicode is combined",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoding := ""
			if tt.cp1252 {
				encoding = "windows-1252"
			}
			action := &dsl.Action{Metadata: map[string]any{}}
			rebuild := rebuildSourceBodyVariants(action, parseSingleRequest(t, tt.call), nil, encoding)
			if tt.reason != "" {
				if len(rebuild.rebuilt) != 0 || !strings.Contains(rebuild.reason, tt.reason) {
					t.Fatalf("rebuild = %+v, want refusal mentioning %q", rebuild, tt.reason)
				}
				return
			}
			if len(rebuild.rebuilt) != 1 || rebuild.reason != "" {
				t.Fatalf("rebuild = %+v, want one rebuilt variant", rebuild)
			}
			if action.BodyEncoding != tt.encoding {
				t.Fatalf("body_encoding = %q, want %q", action.BodyEncoding, tt.encoding)
			}
			if tt.bytes != nil {
				got, err := base64.StdEncoding.DecodeString(action.Body)
				if err != nil || string(got) != string(tt.bytes) {
					t.Fatalf("body bytes = %q (%v), want %q", got, err, tt.bytes)
				}
			} else if action.Body != tt.body {
				t.Fatalf("body = %q, want %q", action.Body, tt.body)
			}
			if issues := dsl.ValidateScriptIssues(&dsl.Script{Name: "s", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://x.test"}, Actions: []dsl.Action{{Type: "http", Name: "a", Method: "POST", Target: "/", Body: action.Body, BodyEncoding: action.BodyEncoding}}}); len(issues) != 0 {
				t.Fatalf("rebuilt body does not validate: %v", issues)
			}
		})
	}
}

func TestRebuildSourceBodyFile(t *testing.T) {
	files := map[string]string{
		"payload.json":   `{"order":1}`,
		"data/blob.bin":  "\x00\x01\xff",
		"other/one.json": "1",
		"dupe/one.json":  "2",
	}
	tests := []struct {
		name     string
		path     string
		encoding string
		body     string
		file     string
		missing  bool
		reason   string
	}{
		{name: "relative text file", path: "payload.json", body: `{"order":1}`, file: "payload.json"},
		{name: "binary file becomes base64", path: `data\blob.bin`, encoding: "base64", body: base64.StdEncoding.EncodeToString([]byte("\x00\x01\xff")), file: "data/blob.bin"},
		{name: "absolute recording path falls back to the base name", path: `C:\recordings\shop\payload.json`, body: `{"order":1}`, file: "payload.json"},
		{name: "missing file is its own gate", path: "absent.json", missing: true},
		{name: "ambiguous base name is missing", path: `C:\x\one.json`, missing: true},
		{name: "parameterized path keeps the body gate", path: "{File}.json", reason: "parameterized path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := `web_custom_request("f", "URL=https://x.test/", "Method=POST", "BodyFilePath=` + strings.ReplaceAll(tt.path, `\`, `\\`) + `", LAST);`
			action := &dsl.Action{Metadata: map[string]any{}}
			rebuild := rebuildSourceBodyVariants(action, parseSingleRequest(t, call), files, "")
			switch {
			case tt.reason != "":
				if !strings.Contains(rebuild.reason, tt.reason) {
					t.Fatalf("rebuild = %+v, want refusal mentioning %q", rebuild, tt.reason)
				}
			case tt.missing:
				if len(rebuild.findings) != 1 || rebuild.findings[0].Code != requestBodyFileMissingReasonCode || !reflect.DeepEqual(rebuild.rebuilt, []string{"BodyFilePath"}) {
					t.Fatalf("rebuild = %+v, want the missing-file gate", rebuild)
				}
			default:
				if action.Body != tt.body || action.BodyEncoding != tt.encoding || action.Metadata["source_body_file"] != tt.file {
					t.Fatalf("body = %q (%q) from %v, want %q (%q) from %s", action.Body, action.BodyEncoding, action.Metadata["source_body_file"], tt.body, tt.encoding, tt.file)
				}
			}
		})
	}
	without := &dsl.Action{Metadata: map[string]any{}}
	if rebuild := rebuildSourceBodyVariants(without, parseSingleRequest(t, `web_custom_request("f", "URL=https://x.test/", "Method=POST", "BodyFilePath=payload.json", LAST);`), nil, ""); rebuild.reason == "" {
		t.Fatal("a conversion without archive files must keep the body gate")
	}
}
