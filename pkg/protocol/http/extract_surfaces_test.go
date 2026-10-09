package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestCookiesURLAndStatusSurfaces(t *testing.T) {
	response := responseSurfaces{
		headers: http.Header{"Set-Cookie": {"sid=S-1; Path=/", "pref=dark; HttpOnly"}, "X-Sid": {"sid=WRONG;"}},
		body:    []byte("sid=BODY;"),
		url:     "https://shop.test/callback?code=C-9&state=s",
		status:  302,
	}
	tests := []struct {
		name string
		ext  protocol.Extractor
		want string
		miss bool
	}{
		{"cookie boundary skips other headers and the body", protocol.Extractor{Source: "cookies", Type: "boundary", Expression: "sid=|||;"}, "S-1", false},
		{"cookie boundary with the header name", protocol.Extractor{Source: "cookies", Type: "boundary", Expression: "Set-Cookie: pref=|||;"}, "dark", false},
		{"cookie regex count", protocol.Extractor{Source: "cookies", Type: "regex_count", Expression: "Set-Cookie:"}, "2", false},
		{"whole url", protocol.Extractor{Source: "url"}, "https://shop.test/callback?code=C-9&state=s", false},
		{"url regex", protocol.Extractor{Source: "url", Type: "regex", Expression: `code=([^&]+)`}, "C-9", false},
		{"status", protocol.Extractor{Source: "status"}, "302", false},
		{"status regex", protocol.Extractor{Source: "status", Type: "regex", Expression: `^3\d\d$`}, "302", false},
		{"no cookies is a miss", protocol.Extractor{Source: "cookies", Type: "boundary", Expression: "nope=|||;"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractResponse(tt.ext, response)
			if tt.miss {
				if !errors.Is(err, protocol.ErrExtractionMiss) {
					t.Fatalf("got %q, %v; want a miss", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	all, err := extractAllResponse(protocol.Extractor{Source: "cookies", Type: "regex", Expression: `Set-Cookie: (\w+)=`, Mode: "all"}, response)
	if err != nil || !reflect.DeepEqual(all, []string{"sid", "pref"}) {
		t.Fatalf("save-all over cookies = %v, %v", all, err)
	}
	whole, err := extractAllResponse(protocol.Extractor{Source: "status", Mode: "all"}, response)
	if err != nil || !reflect.DeepEqual(whole, []string{"302"}) {
		t.Fatalf("save-all over status = %v, %v", whole, err)
	}
}

func TestURLAndStatusSourcesReadTheFinalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/done?code=FINAL", http.StatusFound)
			return
		}
		w.Header().Add("Set-Cookie", "sid=S-7; Path=/")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{})
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL + "/start", Extractors: []protocol.Extractor{
		{Name: "code", Source: "url", Type: "regex", Expression: `code=(\w+)`},
		{Name: "status", Source: "status"},
		{Name: "sid", Source: "cookies", Type: "boundary", Expression: "sid=|||;"},
	}})
	if err != nil || !result.Success {
		t.Fatalf("request: %+v %v", result, err)
	}
	want := map[string]string{"code": "FINAL", "status": "202", "sid": "S-7"}
	for name, value := range want {
		if result.Extractions[name] != value {
			t.Fatalf("extractions = %v, want %v", result.Extractions, want)
		}
	}
}

func TestExtractorDFEs(t *testing.T) {
	encoded := []byte("eyJpZCI6IkRFQy03In0=") // {"id":"DEC-7"}
	response := responseSurfaces{headers: http.Header{"Content-Type": {"text/plain"}}, body: encoded}
	got, err := extractResponse(protocol.Extractor{Source: "body", Type: "jsonpath", Expression: "$.id", DFEs: []string{"base64"}}, response)
	if err != nil || got != "DEC-7" {
		t.Fatalf("base64 then jsonpath = %q, %v", got, err)
	}
	got, err = extractResponse(protocol.Extractor{Source: "body", Type: "xpath", Expression: "//id", DFEs: []string{"base64", "json_to_xml"}}, response)
	if err != nil || got != "DEC-7" {
		t.Fatalf("base64, json_to_xml then xpath = %q, %v", got, err)
	}
	if _, err := extractResponse(protocol.Extractor{Source: "body", Type: "regex", Expression: "(x)", DFEs: []string{"base64"}}, responseSurfaces{body: []byte("not base64!")}); !errors.Is(err, protocol.ErrExtractionMiss) {
		t.Fatalf("a body the chain cannot read must be a miss, got %v", err)
	}
	if _, err := extractResponse(protocol.Extractor{Source: "body", Type: "regex", Expression: "(x)", DFEs: []string{"gwt"}}, response); err == nil || errors.Is(err, protocol.ErrExtractionMiss) {
		t.Fatalf("an unsupported transform must be an error, got %v", err)
	}
	values, err := extractAllResponse(protocol.Extractor{Source: "body", Type: "regex", Expression: "(x)", Mode: "all", DFEs: []string{"base64"}}, responseSurfaces{body: []byte("%%%")})
	if err != nil || len(values) != 0 {
		t.Fatalf("save-all over an unreadable body = %v, %v; want no matches", values, err)
	}
	// A source that does not read the body is not affected by the chain.
	got, err = extractResponse(protocol.Extractor{Source: "header", Expression: "X-Id", DFEs: []string{"base64"}}, responseSurfaces{headers: http.Header{"X-Id": {"H-1"}}, body: []byte("not base64!")})
	if err != nil || got != "H-1" {
		t.Fatalf("header extraction with a DFE chain = %q, %v", got, err)
	}
}
