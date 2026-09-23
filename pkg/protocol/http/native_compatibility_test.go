package http

import (
	"context"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNativeReadBufferAndMetaRefresh(t *testing.T) {
	payload := strings.Repeat("complete", 10000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/start":
			io.WriteString(w, `<meta http-equiv="refresh" content="0; url=final">`)
		case "/loop":
			io.WriteString(w, `<meta http-equiv="refresh" content="0">`)
		case "/delay":
			io.WriteString(w, `<meta http-equiv="refresh" content="10; url=final">`)
		default:
			io.WriteString(w, payload)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		path string
		ok   bool
	}{{"/start", true}, {"/loop", false}, {"/delay", false}} {
		t.Run(tc.path, func(t *testing.T) {
			h := initCompatibilityHTTP(t, map[string]any{"receive_buffer_bytes": 17, "max_meta_refresh": 2, "page_timeout_ms": 100})
			r, e := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL + tc.path, Assertions: []protocol.Assertion{{Type: "body_contains", Value: payload}}})
			if e != nil || r.Success != tc.ok {
				t.Fatalf("success=%v err=%v message=%s", r.Success, e, r.ErrorMessage)
			}
			if tc.ok && r.BytesReceived < int64(len(payload)) {
				t.Fatal("response truncated")
			}
		})
	}
}
func TestNativeContentCheckLimitDoesNotChangeFailure(t *testing.T) {
	for _, limit := range []int{0, 1, 2, 10} {
		r := &protocol.Result{Success: true}
		err := evaluateContentChecksLimited([]contentCheck{{LeftBoundary: "[", RightBoundary: "]", FailIfFound: true}}, r, "[a][b][c]", limit)
		if err == nil {
			t.Fatal("failure lost")
		}
		want := 3
		if limit > 0 {
			want = min(want, limit)
		}
		if r.ContentCheckErrorMatches != want || r.ContentCheckInfoMatches != 3-want {
			t.Fatalf("%+v", r)
		}
	}
	r := &protocol.Result{}
	if evaluateContentChecksLimited([]contentCheck{{LeftBoundary: "[", RightBoundary: "]", Text: "yes", FailIfFound: false}}, r, "[yes]", 1) != nil {
		t.Fatal("required boundary content not found")
	}
}
func TestPACEvaluatorBoundsAndFailClosed(t *testing.T) {
	u, _ := url.Parse("https://example.com/path")
	cases := []struct {
		name, source, want string
		fail               bool
	}{
		{"direct", `function FindProxyForURL(url,host){return "DIRECT"}`, "DIRECT", false},
		{"helper", `function FindProxyForURL(url,host){return shExpMatch(host,"*.com")?"PROXY proxy.example:8080; DIRECT":"DIRECT"}`, "PROXY proxy.example:8080; DIRECT", false},
		{"missing", `var a=1`, "", true},
		{"variables", `function FindProxyForURL(url,host){var local=isPlainHostName(host);return local?"DIRECT":"PROXY proxy.example:8080"}`, "PROXY proxy.example:8080", false},
		{"aliasedEval", `function FindProxyForURL(){var shExpMatch=eval;return shExpMatch("'DIRECT'")}`, "", true},
		{"aliasedRecursion", `function FindProxyForURL(){var dnsResolve=FindProxyForURL;return dnsResolve()}`, "", true},
		{"globalValue", `function FindProxyForURL(){var x=eval;return "DIRECT"}`, "", true},
		{"shadowedHelperParameter", `function FindProxyForURL(shExpMatch){return "DIRECT"}`, "", true},
		{"infinite", `function FindProxyForURL(){while(true){}}`, "", true},
		{"filesystem", `function FindProxyForURL(){return require("fs").readFileSync("/etc/passwd")}`, "", true},
		{"privateDNS", `function FindProxyForURL(){dnsResolve("127.0.0.1");return "DIRECT"}`, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			got, err := evaluatePAC(context.Background(), tc.source, u, nil)
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("got=%q err=%v", got, err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("unbounded evaluator")
			}
		})
	}
}
func TestPACFetchAndExplicitOrderedRoutes(t *testing.T) {
	var pac string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pac" {
			io.WriteString(w, pac)
		} else {
			io.WriteString(w, "ok")
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	allowed := []string{u.Hostname()}
	pac = `function FindProxyForURL(){return "PROXY 127.0.0.1:1; DIRECT"}`
	source, err := fetchPAC(context.Background(), server.URL+"/pac", allowed)
	if err != nil {
		t.Fatal(err)
	}
	rt := &pacRoundTripper{source: source, allowed: allowed, base: http.DefaultTransport.(*http.Transport)}
	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	rt.source = `function FindProxyForURL(){return "PROXY 127.0.0.1:1"}`
	if resp, err = rt.RoundTrip(req); err == nil {
		resp.Body.Close()
		t.Fatal("implicit DIRECT fallback")
	}
	if _, err = fetchPAC(context.Background(), server.URL+"/pac", nil); err == nil {
		t.Fatal("unguarded private PAC fetch")
	}
	for _, bad := range []string{"", "SOCKS host:1", "PROXY user:password@host:80", "DIRECT; BOGUS proxy:1"} {
		if _, err = parsePACRoutes(bad, nil); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestNativeWindows1252WireBodyAndHTTP2ReadBuffer(t *testing.T) {
	received := make(chan []byte, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- b
		if r.ProtoMajor != 2 {
			t.Error("HTTP/2 was not negotiated")
		}
		io.WriteString(w, strings.Repeat("response", 2000))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	h := initCompatibilityHTTP(t, map[string]any{"http_version": "2", "tls_insecure_skip_verify": true, "receive_buffer_bytes": 13})
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "POST", Target: server.URL, Body: []byte("café €"), BodyEncoding: "windows-1252"})
	if err != nil || !result.Success || result.BytesReceived != 16000 {
		t.Fatalf("HTTP/2 response: %+v %v", result, err)
	}
	if got := string(<-received); got != "caf\xe9 \x80" {
		t.Fatalf("wire bytes changed: %x", got)
	}
	result, err = h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "POST", Target: server.URL, Body: []byte("unrepresentable 😀"), BodyEncoding: "windows-1252"})
	if err != nil || result.Success {
		t.Fatal("lossy charset conversion accepted")
	}
}
func TestNativeMetaRefreshCrossOriginStripsAuthAndChecksEachResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Secret") != "" {
			t.Error("cross-origin refresh forwarded credentials")
		}
		io.WriteString(w, "done")
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `forbidden<meta http-equiv="refresh" content="0; url=`+target.URL+`">`)
	}))
	defer source.Close()
	for _, checks := range []bool{false, true} {
		cfg := map[string]any{"max_meta_refresh": 1, "http_default_headers": map[string]string{"Authorization": "Bearer private", "X-Secret": "private"}}
		if checks {
			cfg["web_content_checks"] = []map[string]any{{"text": "forbidden", "fail_if_found": true}}
		}
		h := initCompatibilityHTTP(t, cfg)
		r, e := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: source.URL, Headers: map[string]string{"Authorization": "Bearer private", "X-Secret": "private"}, ResolvedDefaultHeaders: true, DefaultHeaders: map[string]string{"X-Secret": "default-secret"}, Auth: &protocol.AuthConfig{Type: "basic", Username: "private", Password: "password"}})
		if e != nil || r.Success == checks {
			t.Fatalf("per-response check=%v result=%+v err=%v", checks, r, e)
		}
	}
}

func TestNativeCompatibilityBounds(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value any
	}{{"receive_buffer_bytes", -1}, {"receive_buffer_bytes", 1048577}, {"receive_buffer_bytes", 1.5}, {"max_meta_refresh", 51}, {"max_meta_refresh", -1}, {"content_check_error_limit", 1001}} {
		t.Run(fmt.Sprint(tc.key, tc.value), func(t *testing.T) {
			h := &HTTP{}
			if err := h.Init(context.Background(), map[string]any{tc.key: tc.value}); err == nil {
				h.Teardown(context.Background())
				t.Fatal("invalid native bound accepted")
			}
		})
	}
}
