package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func testResponseSet() (responseSurfaces, *responseSetCollector) {
	final := responseSurfaces{headers: http.Header{"Content-Type": {"text/html"}, "X-Id": {"id=FINAL;"}}, body: []byte("id=BODY;"), url: "https://shop.test/app/home", status: 200}
	set := &responseSetCollector{wantHops: true, wantResources: true}
	set.hops = []responseSurfaces{{headers: http.Header{"Location": {"https://shop.test/cb?id=HOP&x=1"}}, url: "https://shop.test/start", status: 302}}
	set.resources = []responseSurfaces{
		{headers: http.Header{"Content-Type": {"application/javascript"}}, body: []byte("id=RES;"), url: "https://cdn.test/token.js", status: 200},
		{headers: http.Header{"Content-Type": {"text/css"}}, body: []byte("id=CSS;"), url: "https://cdn.test/site.css", status: 200},
	}
	return final, set
}

func TestScopedCandidatesOrderAndFilters(t *testing.T) {
	final, set := testResponseSet()
	urls := func(scope *protocol.ExtractorScope) []string {
		var out []string
		for _, candidate := range scopedCandidates(protocol.Extractor{Scope: scope}, final, set) {
			out = append(out, candidate.url)
		}
		return out
	}
	if got := urls(nil); !reflect.DeepEqual(got, []string{"https://shop.test/app/home"}) {
		t.Fatalf("no scope = %v, want the final response only", got)
	}
	if got := urls(&protocol.ExtractorScope{Redirects: "include", Resources: "include"}); !reflect.DeepEqual(got, []string{"https://shop.test/start", "https://shop.test/app/home", "https://cdn.test/token.js", "https://cdn.test/site.css"}) {
		t.Fatalf("full scope = %v, want hops, final, then resources", got)
	}
	if got := urls(&protocol.ExtractorScope{Resources: "include", RequestURL: "*token*"}); !reflect.DeepEqual(got, []string{"https://cdn.test/token.js"}) {
		t.Fatalf("request_url filter = %v", got)
	}
	if got := urls(&protocol.ExtractorScope{Redirects: "include", Resources: "include", ContentType: "text/*"}); !reflect.DeepEqual(got, []string{"https://shop.test/app/home", "https://cdn.test/site.css"}) {
		t.Fatalf("content_type filter = %v", got)
	}
}

func TestScopedExtractionFirstMatchWinsAndHeaderNamesNarrow(t *testing.T) {
	final, set := testResponseSet()
	full := &protocol.ExtractorScope{Redirects: "include", Resources: "include"}
	tests := []struct {
		name string
		ext  protocol.Extractor
		want string
	}{
		{"hop header before the final response", protocol.Extractor{Source: "header", Type: "boundary", Expression: "id=|||&", Scope: full}, "HOP"},
		{"final body before resources", protocol.Extractor{Source: "body", Type: "boundary", Expression: "id=|||;", Scope: full}, "BODY"},
		{"resource only through its URL filter", protocol.Extractor{Source: "body", Type: "boundary", Expression: "id=|||;", Scope: &protocol.ExtractorScope{Resources: "include", RequestURL: "*.css"}}, "CSS"},
		{"header_names skips other headers", protocol.Extractor{Source: "header", Type: "boundary", Expression: "id=|||;", Scope: &protocol.ExtractorScope{Redirects: "include", HeaderNames: []string{"x-id"}}}, "FINAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractFromCandidates(tt.ext, scopedCandidates(tt.ext, final, set))
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	all, err := extractAllFromCandidates(protocol.Extractor{Source: "body", Type: "regex", Expression: `id=(\w+);`, Mode: "all", Scope: full}, scopedCandidates(protocol.Extractor{Scope: full}, final, set))
	if err != nil || !reflect.DeepEqual(all, []string{"BODY", "RES", "CSS"}) {
		t.Fatalf("save-all across responses = %v, %v", all, err)
	}
	if _, err := extractFromCandidates(protocol.Extractor{Source: "body", Type: "regex", Expression: "id=(", Scope: full}, scopedCandidates(protocol.Extractor{Scope: full}, final, set)); err == nil || errors.Is(err, protocol.ErrExtractionMiss) {
		t.Fatalf("a bad expression must stop the search with its error, got %v", err)
	}
	if _, err := extractFromCandidates(protocol.Extractor{Source: "body", Type: "regex", Expression: "id=(x)"}, nil); !errors.Is(err, protocol.ErrExtractionMiss) {
		t.Fatalf("no candidate is a miss, got %v", err)
	}
}

func TestResponseSetCollectorBounds(t *testing.T) {
	if newResponseSetCollector([]protocol.Extractor{{Source: "body"}}) != nil {
		t.Fatal("unscoped extractors must not collect anything")
	}
	set := newResponseSetCollector([]protocol.Extractor{{Scope: &protocol.ExtractorScope{Redirects: "include"}}})
	if set == nil || !set.wantHops || set.wantResources {
		t.Fatalf("collector = %+v", set)
	}
	for i := 0; i < maxResponseSetEntries+5; i++ {
		set.addHop(&http.Response{StatusCode: 302, Header: http.Header{"Location": {"/next"}}})
	}
	if len(set.hops) != maxResponseSetEntries {
		t.Fatalf("hops = %d, want the %d cap", len(set.hops), maxResponseSetEntries)
	}
	var nilSet *responseSetCollector
	nilSet.addHop(&http.Response{})
}

func TestScopeGlob(t *testing.T) {
	for _, tt := range []struct {
		pattern, value string
		want           bool
	}{
		{"*token*", "https://cdn.test/static/TOKEN.js", true},
		{"https://shop.test/*", "https://shop.test/a/b", true},
		{"text/*", "text/html; charset=utf-8", true},
		{"orders", "https://shop.test/app/orders?x=1", true},
		{"*.css", "https://cdn.test/site.js", false},
		{"*items[0]*", "https://shop.test/api?items[0]=a", true},
		{"*items[0]*", "https://shop.test/api?items0=a", false},
		{"*a\\b?", "C:\\a\\bc", true},
		{"[unclosed*", "[unclosed.js", true},
	} {
		if got := MatchScopeGlob(tt.pattern, tt.value); got != tt.want {
			t.Errorf("MatchScopeGlob(%q, %q) = %v", tt.pattern, tt.value, got)
		}
	}
}

// When extraction stops on an output limit, the extractors waiting for the
// page's resources do not run either.
func TestDeferredResourceExtractorsStopWithExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset.js" {
			_, _ = io.WriteString(w, "id=RES;")
			return
		}
		_, _ = io.WriteString(w, "big="+strings.Repeat("x", protocol.MaxExtractionValueBytes+1)+";")
	}))
	defer server.Close()
	h := initHTTP(t, nil)
	result, err := h.Execute(context.Background(), &protocol.Action{
		Type: "http_request", Method: "GET", Target: server.URL,
		Params: map[string]any{"resources": []any{map[string]any{"url": "/asset.js"}}},
		Extractors: []protocol.Extractor{
			{Name: "fromResource", Source: "body", Type: "boundary", Expression: "id=|||;", Scope: &protocol.ExtractorScope{Resources: "include"}},
			{Name: "tooBig", Source: "body", Type: "boundary", Expression: "big=|||;"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !result.ExtractionsIncomplete || len(result.Extractions) != 0 {
		t.Fatalf("success = %v, incomplete = %v, extractions = %v; want a stopped extraction with nothing stored", result.Success, result.ExtractionsIncomplete, result.Extractions)
	}
}
