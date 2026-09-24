package http

import (
	"context"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPExtractorExplicitDefaults(t *testing.T) {
	for _, fallback := range []string{"NOT_FOUND", ""} {
		t.Run("fallback="+fallback, func(t *testing.T) {
			body := `{"id":"first"}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			h := initHTTP(t, map[string]any{"base_url": server.URL})
			defer h.Teardown(context.Background())
			a := &protocol.Action{Name: "probe", Method: "GET", Type: "GET", Target: "/", Extractors: []protocol.Extractor{{Name: "id", Type: "jsonpath", Expression: "$.id", OnMiss: "default", DefaultValue: &fallback}}}
			for i, want := range []string{"first", fallback, "last"} {
				if i == 1 {
					body = `{}`
				} else if i == 2 {
					body = `{"id":"last"}`
				}
				r, e := h.Execute(context.Background(), a)
				if e != nil {
					t.Fatal(e)
				}
				if got, ok := r.Extractions["id"]; !ok || got != want || !r.Success {
					t.Fatalf("got %q %t %+v", got, ok, r)
				}
			}
		})
	}
}

func TestHTTPExtractorDefaultDoesNotSuppressErrors(t *testing.T) {
	fallback := "NOT_FOUND"
	for _, tc := range []struct {
		name, body, expression string
		defaultValue           *string
	}{
		{"malformed JSON", `{`, "$.id", &fallback},
		{"invalid expression", `{}`, "broken", &fallback},
		{"missing ancestor invalid index", `{}`, "$.missing[", &fallback},
		{"missing ancestor invalid field", `{}`, "$.missing..id", &fallback},
		{"missing ancestor unsupported index", `{}`, "$.missing[unknown]", &fallback},
		{"wrong type invalid suffix", `[]`, "$.missing[", &fallback},
		{"missing default", `{}`, "$.id", nil},
		{"bounded expression", `{}`, strings.Repeat("x", MaxExtractorExpressionBytes+1), &fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			h := initHTTP(t, map[string]any{"base_url": server.URL})
			defer h.Teardown(context.Background())
			result, err := h.Execute(context.Background(), &protocol.Action{Name: "sample", Type: "GET", Method: "GET", Target: "/", Extractors: []protocol.Extractor{{Name: "id", Type: "jsonpath", Expression: tc.expression, OnMiss: "default", DefaultValue: tc.defaultValue}}})
			if err == nil && result.Success {
				t.Fatalf("invalid extraction succeeded: %+v", result)
			}
			if result != nil && result.Extractions["id"] == fallback {
				t.Fatal("error was converted to fallback")
			}
		})
	}
}

func TestHTTPFirstByteLatencyAcrossRedirectAndConnectionFailure(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		time.Sleep(120 * time.Millisecond)
		_, _ = w.Write([]byte("done"))
	}))
	h := initHTTP(t, map[string]any{"base_url": target.URL})
	defer h.Teardown(context.Background())
	result, err := h.Execute(context.Background(), &protocol.Action{Name: "sample", Type: "GET", Method: "GET", Target: "/"})
	if err != nil || !result.Success {
		t.Fatalf("%v %+v", err, result)
	}
	if result.Duration-result.TTFB < 100*time.Millisecond {
		t.Fatalf("latency used final response: duration=%s latency=%s", result.Duration, result.TTFB)
	}
	target.Close()
	result, err = h.Execute(context.Background(), &protocol.Action{Name: "sample", Type: "GET", Method: "GET", Target: "/"})
	if result != nil && (result.Success || result.TTFB != 0) {
		t.Fatalf("connection failure had first byte: %+v (%v)", result, err)
	}
}
