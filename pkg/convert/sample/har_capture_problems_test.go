package sample

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
)

func harEntryJSON(method, rawURL string, status int, requestHeaders, responseHeaders []map[string]any, postData map[string]any) map[string]any {
	request := map[string]any{"method": method, "url": rawURL, "httpVersion": "http/2.0", "headers": headerItems(requestHeaders), "cookies": []any{}}
	if postData != nil {
		request["postData"] = postData
	}
	return map[string]any{
		"request":  request,
		"response": map[string]any{"status": status, "httpVersion": "http/2.0", "headers": headerItems(responseHeaders), "cookies": []any{}, "content": map[string]any{"text": "{}"}},
	}
}

func headerItems(headers []map[string]any) []any {
	items := make([]any, 0, len(headers))
	for _, h := range headers {
		items = append(items, h)
	}
	return items
}

func harJSON(t *testing.T, entries ...map[string]any) string {
	t.Helper()
	list := make([]any, 0, len(entries))
	for _, entry := range entries {
		list = append(list, entry)
	}
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": list}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A browser export can drop a large request body while keeping the recorded
// Content-Length. The step stays gated exactly as before (one blocker, one
// execution gate), but the reason names the missing body instead of the
// framing header.
func TestHARRequestBodyNotExportedIsGatedForTheMissingBody(t *testing.T) {
	form := header("Content-Type", "application/x-www-form-urlencoded")
	tests := []struct {
		name        string
		method      string
		length      string
		postData    map[string]any
		wantMissing bool
	}{
		{name: "no postData", method: "POST", length: "93994", wantMissing: true},
		{name: "empty postData", method: "POST", length: "268276", postData: map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": ""}, wantMissing: true},
		{name: "PUT without postData", method: "PUT", length: "12", wantMissing: true},
		// Genuine framing problems keep the existing framing review.
		{name: "GET with a length", method: "GET", length: "12"},
		{name: "captured body of another length", method: "POST", length: "99", postData: map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "a=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := harJSON(t, harEntryJSON(tt.method, "https://example.invalid/aura?r=1", 200, []map[string]any{form, header("Content-Length", tt.length)}, nil, tt.postData))
			result, err := Convert(ImportRequest{Format: "har", Content: content})
			if err != nil {
				t.Fatal(err)
			}
			var blockers, bodyBlockers, lengthBlockers int
			for _, d := range result.Unsupported {
				if d.Severity != diag.Blocker {
					continue
				}
				blockers++
				switch d.Element {
				case "HAR request body":
					bodyBlockers++
					want := fmt.Sprintf("The HAR export did not include this request's body (recorded Content-Length %s bytes)", tt.length)
					if !strings.Contains(d.Detail, want) || !strings.Contains(d.Detail, "remove this step") || d.Location != "HAR entry 1" {
						t.Fatalf("body blocker = %+v", d)
					}
				case "Content-Length":
					lengthBlockers++
				}
			}
			findings, err := importreview.FindingsFromScript(result.Script)
			if err != nil {
				t.Fatal(err)
			}
			var gates, bodyGates int
			for _, finding := range findings {
				if finding.Gate == importreview.GateExecution {
					gates++
				}
				if finding.ReasonCode == HARRequestBodyNotExportedCode && finding.Resolution == "recapture_body" {
					bodyGates++
				}
			}
			if blockers != 1 || gates != 1 {
				t.Fatalf("gating changed: %d blocker(s), %d execution gate(s); diagnostics %+v findings %+v", blockers, gates, result.Unsupported, findings)
			}
			if tt.wantMissing != (bodyBlockers == 1 && bodyGates == 1 && lengthBlockers == 0) {
				t.Fatalf("missing-body reason=%v (body %d/%d, framing %d), want %v", !tt.wantMissing, bodyBlockers, bodyGates, lengthBlockers, tt.wantMissing)
			}
			action := result.Script.Actions[0]
			for _, h := range action.HeaderList {
				if strings.EqualFold(h.Name, "Content-Length") {
					t.Fatal("recorded Content-Length reached the replayed header list")
				}
			}
			if tt.wantMissing && (action.Body != "" || fmt.Sprint(action.Metadata["har.request.recorded_content_length"]) != tt.length) {
				t.Fatalf("body=%q recorded length=%v", action.Body, action.Metadata["har.request.recorded_content_length"])
			}
		})
	}
}

// A sanitized browser export has no cookie or Authorization evidence at all.
// A session capture like that gets one capture-level warning; any cookie or
// credential evidence, or a capture too small to be a session, gets none.
func TestHARSanitizedExportWarning(t *testing.T) {
	login := func(requestHeaders, responseHeaders []map[string]any) []map[string]any {
		return []map[string]any{
			harEntryJSON("POST", "https://login.example.invalid/login", 302, requestHeaders, append([]map[string]any{header("Location", "https://app.example.invalid/home")}, responseHeaders...), map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "un=a"}),
			harEntryJSON("GET", "https://app.example.invalid/home", 200, nil, nil, nil),
			harEntryJSON("GET", "https://app.example.invalid/api/data", 200, nil, nil, nil),
		}
	}
	plain := func(count int) []map[string]any {
		entries := make([]map[string]any, count)
		for index := range entries {
			entries[index] = harEntryJSON("GET", fmt.Sprintf("https://api.example.invalid/items/%d", index), 200, nil, nil, nil)
		}
		return entries
	}
	single := func(method, rawURL string, status int, responseHeaders ...map[string]any) []map[string]any {
		return []map[string]any{harEntryJSON(method, rawURL, status, nil, responseHeaders, nil)}
	}
	revalidated := plain(3)
	revalidated[1]["response"].(map[string]any)["status"] = 304
	withRequestCookies := login(nil, nil)
	withRequestCookies[1]["request"].(map[string]any)["cookies"] = []any{map[string]any{"name": "sid", "value": "x"}}
	withResponseCookies := login(nil, nil)
	withResponseCookies[0]["response"].(map[string]any)["cookies"] = []any{map[string]any{"name": "sid", "value": "x"}}
	tests := []struct {
		name      string
		entries   []map[string]any
		want      bool
		sanitized bool
	}{
		{name: "sign-in flow without cookies", entries: login(nil, nil), want: true, sanitized: true},
		{name: "twenty entries without cookies", entries: plain(20), want: true, sanitized: true},
		{name: "small capture without a session", entries: plain(3), sanitized: true},
		{name: "Set-Cookie header", entries: login(nil, []map[string]any{header("Set-Cookie", "sid=x; Path=/")})},
		{name: "Cookie header", entries: login([]map[string]any{header("Cookie", "sid=x")}, nil)},
		{name: "request cookies[]", entries: withRequestCookies},
		{name: "response cookies[]", entries: withResponseCookies},
		{name: "Authorization header", entries: login([]map[string]any{header("Authorization", "Bearer x")}, nil)},
		{name: "304 revalidation is not a redirect", entries: revalidated, sanitized: true},
		{name: "accessories path is not sso", entries: single("GET", "https://shop.example.invalid/accessories/list", 200), sanitized: true},
		{name: "lessons path is not sso", entries: single("GET", "https://learn.example.invalid/lessons", 200), sanitized: true},
		{name: "bloginfo path is not login", entries: single("GET", "https://blog.example.invalid/bloginfo", 200), sanitized: true},
		{name: "sso segment", entries: single("GET", "https://idp.example.invalid/sso/start", 200), want: true, sanitized: true},
		{name: "hyphenated sign-in segment", entries: single("POST", "https://app.example.invalid/sign-in", 200), want: true, sanitized: true},
		{name: "login page file", entries: single("GET", "https://app.example.invalid/login.jsp", 200), want: true, sanitized: true},
		{name: "307 redirect", entries: single("GET", "https://app.example.invalid/start", 307), want: true, sanitized: true},
		{name: "Location header redirect", entries: single("GET", "https://app.example.invalid/start", 200, header("Location", "https://app.example.invalid/next")), want: true, sanitized: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := harJSON(t, tt.entries...)
			result, err := Convert(ImportRequest{Format: "har", Content: content})
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics, warnings int
			for _, d := range result.Unsupported {
				if d.Code == HARSanitizedExportCode {
					diagnostics++
					if d.Severity != diag.Warning || !strings.Contains(d.Detail, fmt.Sprintf("capture's %d entries", len(tt.entries))) ||
						!strings.Contains(d.Detail, "sanitized export") || !strings.Contains(d.Detail, `"Export HAR (with sensitive data)"`) || !strings.Contains(d.Detail, "stale values") {
						t.Fatalf("sanitized-export diagnostic = %+v", d)
					}
				}
			}
			for _, warning := range result.Warnings {
				if strings.Contains(warning, "sanitized export") {
					warnings++
				}
			}
			want := 0
			if tt.want {
				want = 1
			}
			if diagnostics != want || warnings != want {
				t.Fatalf("sanitized-export diagnostics=%d warnings=%d, want %d each: %+v %v", diagnostics, warnings, want, result.Unsupported, result.Warnings)
			}
			document, err := ParseHARDocument(content)
			if err != nil {
				t.Fatal(err)
			}
			if document.LooksSanitized() != tt.sanitized {
				t.Fatalf("LooksSanitized=%v, want %v", document.LooksSanitized(), tt.sanitized)
			}
		})
	}
	var nilDocument *HARDocument
	if nilDocument.LooksSanitized() {
		t.Fatal("a missing capture is not a cookie-free capture")
	}
}
