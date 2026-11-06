package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// JMeter's JSON Extractor stores its default value when the response is not
// JSON (an HTML error page, say) and never fails the sample.
func TestHTTPJSONPathDefaultCoversUnreadableBody(t *testing.T) {
	fallback := "NOT_FOUND"
	for _, tc := range []struct {
		name, contentType, body string
	}{
		{"html error page", "text/html", "<html><body>Bad gateway</body></html>"},
		{"truncated json", "application/json", `{"id":`},
		{"empty body", "application/json", ""},
		{"multiple documents", "application/json", `{"id":"a"}{"id":"b"}`},
		{"invalid utf8", "application/json", "{\"id\":\"\xff\"}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			h := initHTTP(t, map[string]any{"base_url": server.URL})
			defer h.Teardown(context.Background())
			result, err := h.Execute(context.Background(), &protocol.Action{Name: "sample", Type: "POST", Method: "POST", Target: "/", Extractors: []protocol.Extractor{{Name: "idpTraceId", Type: "jsonpath", Expression: "$.idpTraceId", OnMiss: "default", DefaultValue: &fallback}}})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Success || result.ErrorMessage != "" {
				t.Fatalf("unreadable body failed the request: %q", result.ErrorMessage)
			}
			if got := result.Extractions["idpTraceId"]; got != fallback {
				t.Fatalf("idpTraceId = %q, want the default", got)
			}
			if !slices.Contains(result.WarningCodes, "extractor_default_unreadable_body") {
				t.Fatalf("warning codes = %v, want the unreadable-body warning", result.WarningCodes)
			}
			if names, _ := result.ProtocolEvidence["extractor_defaults_unreadable_body"].([]string); !slices.Equal(names, []string{"idpTraceId"}) {
				t.Fatalf("evidence names = %v", names)
			}
		})
	}
}

func TestHTTPJSONPathUnreadableBodyOutcomeByOnMiss(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	defer server.Close()
	h := initHTTP(t, map[string]any{"base_url": server.URL})
	defer h.Teardown(context.Background())
	fallback := "NOT_FOUND"
	run := func(ext protocol.Extractor) *protocol.Result {
		t.Helper()
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "sample", Type: "GET", Method: "GET", Target: "/", Extractors: []protocol.Extractor{ext}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	// fail still fails, and says why.
	failed := run(protocol.Extractor{Name: "id", Type: "jsonpath", Expression: "$.id", OnMiss: "fail"})
	if failed.Success || !strings.Contains(failed.ErrorMessage, `extractor "id" found no match: invalid JSON body`) {
		t.Fatalf("on_miss fail: success=%t message=%q", failed.Success, failed.ErrorMessage)
	}
	// Without a default nothing is stored and no default warning is recorded.
	ignored := run(protocol.Extractor{Name: "id", Type: "jsonpath", Expression: "$.id"})
	if !ignored.Success {
		t.Fatalf("lenient miss failed: %q", ignored.ErrorMessage)
	}
	if _, ok := ignored.Extractions["id"]; ok || slices.Contains(ignored.WarningCodes, "extractor_default_unreadable_body") {
		t.Fatalf("lenient miss stored %v / warned %v", ignored.Extractions, ignored.WarningCodes)
	}
	// mode all stores the default and a zero count.
	all := run(protocol.Extractor{Name: "id", Type: "jsonpath", Expression: "$.id", Mode: "all", OnMiss: "default", DefaultValue: &fallback})
	if !all.Success || all.Extractions["id"] != fallback || all.Extractions["id_count"] != "0" {
		t.Fatalf("mode all: success=%t extractions=%v message=%q", all.Success, all.Extractions, all.ErrorMessage)
	}
	// A malformed expression is still a configuration error, not a default.
	bad := run(protocol.Extractor{Name: "id", Type: "jsonpath", Expression: "$.id[", OnMiss: "default", DefaultValue: &fallback})
	if bad.Success || bad.Extractions["id"] == fallback {
		t.Fatalf("malformed expression was defaulted: %+v", bad.Extractions)
	}
}

func TestHTTPJSONPathResourceLimitIsNotUnreadableBody(t *testing.T) {
	body := []byte(strings.Repeat("[", maxExtractorStructuredDepth+1) + strings.Repeat("]", maxExtractorStructuredDepth+1))
	_, err := extractJSONPathValue("$.id", body)
	if err == nil || !strings.Contains(err.Error(), "exceeds depth") {
		t.Fatalf("err = %v, want the depth limit", err)
	}
	if isMiss := errorsIsMiss(err); isMiss {
		t.Fatalf("resource limit classified as a miss: %v", err)
	}
}

func errorsIsMiss(err error) bool {
	return err != nil && (errors.Is(err, protocol.ErrExtractionMiss) || errors.Is(err, errExtractorUnreadableBody))
}

// A scoped search moves past a response that is not JSON (a redirect hop has
// no body) and warns only when no searched response was JSON.
func TestHTTPJSONPathScopedSearchPastUnreadableBodies(t *testing.T) {
	finalBody := `{"token":"from-final"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(finalBody))
	}))
	defer server.Close()
	h := initHTTP(t, map[string]any{"base_url": server.URL})
	defer h.Teardown(context.Background())
	fallback := "NOT_FOUND"
	run := func(mode string) *protocol.Result {
		t.Helper()
		ext := protocol.Extractor{Name: "token", Type: "jsonpath", Expression: "$.token", Mode: mode, OnMiss: "default", DefaultValue: &fallback, Scope: &protocol.ExtractorScope{Redirects: "include"}}
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "sample", Type: "GET", Method: "GET", Target: "/", Extractors: []protocol.Extractor{ext}})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Success {
			t.Fatalf("mode %q failed: %q", mode, result.ErrorMessage)
		}
		return result
	}

	if got := run("").Extractions["token"]; got != "from-final" {
		t.Fatalf("single: token = %q, want the final response's value", got)
	}
	if got := run("all").Extractions["token_1"]; got != "from-final" {
		t.Fatalf("all: token_1 = %q", got)
	}

	// The final response is JSON without the field: a plain miss, no warning.
	finalBody = `{"other":1}`
	for _, mode := range []string{"", "all"} {
		result := run(mode)
		if result.Extractions["token"] != fallback || slices.Contains(result.WarningCodes, "extractor_default_unreadable_body") {
			t.Fatalf("mode %q readable miss: extractions=%v warnings=%v", mode, result.Extractions, result.WarningCodes)
		}
	}

	// No searched response is JSON: the default carries the warning.
	finalBody = `<html>error</html>`
	for _, mode := range []string{"", "all"} {
		result := run(mode)
		if result.Extractions["token"] != fallback || !slices.Contains(result.WarningCodes, "extractor_default_unreadable_body") {
			t.Fatalf("mode %q unreadable: extractions=%v warnings=%v", mode, result.Extractions, result.WarningCodes)
		}
	}
}

// Order-independent attribution: a readable miss anywhere in the search wins
// over unreadable bodies before or after it.
func TestHTTPJSONPathCandidateMissAttribution(t *testing.T) {
	ext := protocol.Extractor{Name: "token", Type: "jsonpath", Expression: "$.token"}
	html := responseSurfaces{body: []byte("<html></html>")}
	json := responseSurfaces{body: []byte(`{"other":1}`), isFinal: true}
	for _, order := range [][]responseSurfaces{{html, json, html}, {json, html}, {html, json}} {
		_, err := extractFromCandidates(ext, order)
		if !errors.Is(err, protocol.ErrExtractionMiss) || errors.Is(err, errExtractorUnreadableBody) {
			t.Fatalf("single: err = %v, want a readable miss", err)
		}
		values, err := extractAllFromCandidates(ext, order)
		if len(values) != 0 || !errors.Is(err, protocol.ErrExtractionMiss) || errors.Is(err, errExtractorUnreadableBody) {
			t.Fatalf("all: values=%v err = %v, want a readable miss", values, err)
		}
	}
	_, err := extractFromCandidates(ext, []responseSurfaces{html, html})
	if !errors.Is(err, errExtractorUnreadableBody) {
		t.Fatalf("all unreadable: err = %v", err)
	}
	if _, err := extractAllFromCandidates(ext, []responseSurfaces{html, html}); !errors.Is(err, errExtractorUnreadableBody) {
		t.Fatalf("all mode, all unreadable: err = %v", err)
	}
}
