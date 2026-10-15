package sample

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestHARRecordedContentLength(t *testing.T) {
	tests := []struct {
		name, body, encoding string
		lengths              []string
		wantRemoved          bool
	}{
		{name: "matching form", body: "name=example", lengths: []string{"12"}, wantRemoved: true},
		{name: "UTF8 bytes", body: "日本", lengths: []string{"6"}, wantRemoved: true},
		{name: "empty body", lengths: []string{"0"}, wantRemoved: true},
		{name: "binary bytes", body: base64.StdEncoding.EncodeToString([]byte{0xff, 0, 0x01}), encoding: "base64", lengths: []string{"3"}, wantRemoved: true},
		{name: "identical duplicate lengths", body: "abc", lengths: []string{"3", "3"}, wantRemoved: true},
		{name: "mismatch", body: "abc", lengths: []string{"4"}},
		{name: "conflicting duplicates", body: "abc", lengths: []string{"3", "4"}},
		{name: "malformed", body: "abc", lengths: []string{"invalid"}},
		{name: "signed invalid length", body: "abc", lengths: []string{"+3"}},
		{name: "negative", body: "abc", lengths: []string{"-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := []any{map[string]any{"name": "X-Example", "value": "first"}}
			for _, length := range tt.lengths {
				headers = append(headers, map[string]any{"name": "content-length", "value": length})
			}
			headers = append(headers, map[string]any{"name": "X-Example", "value": "second"})
			data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{map[string]any{
				"request":  map[string]any{"method": "POST", "url": "https://example.invalid/form", "headers": headers, "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": tt.body, "encoding": tt.encoding}},
				"response": map[string]any{"status": 200, "cookies": []any{map[string]any{"name": "theme", "value": "light"}}, "content": map[string]any{"text": "{}"}},
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics diag.Collector
			exchanges, _, _, _, err := parseHAR(string(data), false, &diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			ex := exchanges[0]
			hasLength := false
			for _, h := range ex.HeaderList {
				if strings.EqualFold(h.Name, "Content-Length") {
					hasLength = true
				}
			}
			if hasLength == tt.wantRemoved {
				t.Fatalf("source length removed=%v, want %v", !hasLength, tt.wantRemoved)
			}
			if tt.wantRemoved {
				if headerLookup(ex.Headers, "Content-Length") != "" {
					t.Fatal("recorded length survived in header map")
				}
				if _, ok := ex.Metadata["har.request.content_length"]; !ok {
					t.Fatal("recorded framing evidence lost when response metadata was attached")
				}
			}
			result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
			if err != nil {
				t.Fatal(err)
			}
			lengthBlocker := false
			for _, d := range result.Unsupported {
				// The blocker names the header as the capture wrote it.
				if strings.EqualFold(d.Element, "Content-Length") && d.Severity == diag.Blocker {
					lengthBlocker = true
				}
			}
			if lengthBlocker == tt.wantRemoved {
				t.Fatalf("length blocker=%v, want %v", lengthBlocker, !tt.wantRemoved)
			}
			action := result.Script.Actions[0]
			if action.Body != tt.body || action.BodyEncoding != tt.encoding {
				t.Fatalf("request bytes changed: body=%q encoding=%q", action.Body, action.BodyEncoding)
			}
			var values []string
			for _, h := range action.HeaderList {
				if h.Name == "X-Example" {
					values = append(values, h.Value)
				}
			}
			if strings.Join(values, ",") != "first,second" {
				t.Fatalf("ordinary duplicate order changed: %v", values)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("invalid draft: %v", issues)
			}
		})
	}
}

func TestHARUnknownVersionIsUnavailableMetadata(t *testing.T) {
	for _, version := range []string{"unknown", " Unknown ", "HTTP/1.1", "HTTP/2", "HTTP/1.0", "HTTP/3"} {
		t.Run(version, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{map[string]any{"request": map[string]any{"method": "GET", "url": "https://example.invalid/page", "httpVersion": version}, "response": map[string]any{"status": 200, "httpVersion": version, "content": map[string]any{"text": "{}"}}}}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
			if err != nil {
				t.Fatal(err)
			}
			action := result.Script.Actions[0]
			if version != "HTTP/1.0" && action.Metadata["har.request.http_version"] != version {
				t.Fatalf("recorded version metadata lost: %+v", action.Metadata)
			}
			if version == "HTTP/1.0" && action.Metadata["har.request.http_version"] != nil {
				t.Fatal("unsupported source value copied instead of bounded capture state")
			}
			// Unknown and non-selectable versions are capture-quality warnings:
			// the leaf inherits the script policy and is not blocked.
			blocked, warned := false, false
			for _, d := range result.Unsupported {
				if d.Element == "HTTP version" && d.Severity == diag.Blocker {
					blocked = true
				}
				if d.Element == "HTTP version" && d.Severity == diag.Warning {
					warned = true
				}
			}
			wantWarned := version == "HTTP/1.0" || strings.EqualFold(strings.TrimSpace(version), "unknown")
			if blocked || warned != wantWarned {
				t.Fatalf("HTTP version blocker=%v warning=%v, want warning=%v and no blocker", blocked, warned, wantWarned)
			}
			if wantWarned {
				if result.Script.Config.HTTP.Version != "" || action.HTTPVersion.IsPresent() {
					t.Fatalf("unspecified version forced to %q / %v", result.Script.Config.HTTP.Version, action.HTTPVersion)
				}
				if _, ok := action.Metadata["import_http_version_fidelity"]; ok || action.Metadata["execution_blocker"] != nil {
					t.Fatal("capture-quality gap gated execution")
				}
			}
		})
	}
}
