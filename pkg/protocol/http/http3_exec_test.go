package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// http3Session starts a session with HTTP/3 enabled; quick handshake timeouts
// keep failing dials fast.
func http3Session(t *testing.T, config map[string]any) *HTTP {
	t.Helper()
	t.Setenv(EnvHTTP3, "true")
	if config == nil {
		config = map[string]any{}
	}
	if _, ok := config[quicHandshakeTimeoutKey]; !ok {
		config[quicHandshakeTimeoutKey] = float64(1000)
	}
	return versionHTTP(t, config)
}

func run(t *testing.T, h *HTTP, action *protocol.Action) *protocol.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := h.Execute(ctx, action)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func h3Get(t *testing.T, target, policy string) *protocol.Action {
	t.Helper()
	action := &protocol.Action{Name: "h3", Type: "GET", Target: target}
	if policy != "" {
		action.HTTPVersion = versionOverride(t, policy)
	}
	return action
}

func TestExactHTTP3RunsThroughTheSession(t *testing.T) {
	// The fixture advertises HTTP/3; an HTTP/3 response has already used it.
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	for _, tc := range []struct{ config, leaf string }{{"3", ""}, {"", "3"}, {"2", "3"}} {
		config := map[string]any{}
		if tc.config != "" {
			config["http_version"] = tc.config
		}
		h := http3Session(t, config)
		result := run(t, h, h3Get(t, fixture.URL("/echo"), tc.leaf))
		if !result.Success || result.HTTPVersionPolicy != VersionHTTP3 || result.HTTPVersionNegotiated != VersionHTTP3 {
			t.Fatalf("config=%q leaf=%q: success=%v policy=%q negotiated=%q message=%q", tc.config, tc.leaf, result.Success, result.HTTPVersionPolicy, result.HTTPVersionNegotiated, result.ErrorMessage)
		}
		if result.ProtocolEvidence["http_version_negotiated"] != "3" || result.ProtocolEvidence["alt_svc_h3_advertised"] != nil {
			t.Fatalf("evidence = %v", result.ProtocolEvidence)
		}
	}
	if n := fixture.Ledger.Count(func(a h3fixture.Attempt) bool { return a.Listener != "quic" }); n != 0 {
		t.Fatalf("exact 3 sent %d requests over TCP", n)
	}
}

func TestExactHTTP3SharesCookiesAndPartitionsTheCache(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, map[string]any{"http_cache_enabled": true})
	// A TCP response from the same host sets a cookie the HTTP/3 pool sends.
	cookieServer := versionTLS(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "shared", Path: "/"})
	}))
	if r := run(t, h, h3Get(t, cookieServer.URL, "2")); !r.Success {
		t.Fatalf("cookie request: %s", r.ErrorMessage)
	}
	echo := run(t, h, h3Get(t, fixture.URL("/echo"), "3"))
	if !echo.Success {
		t.Fatalf("echo: %s", echo.ErrorMessage)
	}
	var body struct {
		Headers map[string][]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(echo.ResponseBody), &body); err != nil || !strings.Contains(strings.Join(body.Headers["cookie"], ";"), "session=shared") {
		t.Fatalf("cookie not sent over HTTP/3: %v %s", err, echo.ResponseBody)
	}
	// A TCP-filled cache entry never satisfies exact 3.
	tcp := run(t, h, h3Get(t, fixture.URL("/cacheable"), "2"))
	first := run(t, h, h3Get(t, fixture.URL("/cacheable"), "3"))
	second := run(t, h, h3Get(t, fixture.URL("/cacheable"), "3"))
	if tcp.HTTPVersionSource != "wire" || first.HTTPVersionSource != "wire" || first.HTTPVersionNegotiated != "3" || second.HTTPVersionSource != "cache" {
		t.Fatalf("cache sources tcp=%s first=%s/%s second=%s", tcp.HTTPVersionSource, first.HTTPVersionSource, first.HTTPVersionNegotiated, second.HTTPVersionSource)
	}
}

func TestExactHTTP3AppliesToEveryRedirectHop(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, nil)
	same := run(t, h, h3Get(t, fixture.URL("/redirect?to="+url.QueryEscape(fixture.URL("/echo"))), "3"))
	if !same.Success || same.HTTPVersionNegotiated != "3" {
		t.Fatalf("same-origin redirect: %s", same.ErrorMessage)
	}
	tcpOnly := versionTLS(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	hop := run(t, h, h3Get(t, fixture.URL("/redirect?to="+url.QueryEscape(tcpOnly.URL+"/landing")), "3"))
	if hop.Success || !strings.HasPrefix(hop.ErrorMessage, "http version 3 is required: not_sent") {
		t.Fatalf("redirect to a TCP-only origin: success=%v message=%q", hop.Success, hop.ErrorMessage)
	}
	// The session's redirect policy refuses HTTPS-to-HTTP for every policy;
	// exact 3 could not carry the hop anyway.
	plain := run(t, h, h3Get(t, fixture.URL("/redirect?to="+url.QueryEscape("http://127.0.0.1:1/x")), "3"))
	if plain.Success || !strings.Contains(plain.ErrorMessage, "refused HTTPS to HTTP redirect") {
		t.Fatalf("redirect to http://: success=%v message=%q", plain.Success, plain.ErrorMessage)
	}
}

func TestExactHTTP3FailuresCarryTheirCertainty(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, nil)
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		fixture.CloseQUICConnections()
	}()
	dropped := run(t, h, h3Get(t, fixture.URL("/slow?ms=3000"), "3"))
	if dropped.Success || dropped.ProtocolEvidence["http3_failure_certainty"] != "possibly_sent" || !strings.Contains(dropped.ErrorMessage, "possibly_sent") {
		t.Fatalf("dropped: message=%q evidence=%v", dropped.ErrorMessage, dropped.ProtocolEvidence)
	}
	if err := fixture.SetQUICState(h3fixture.QUICBlackholed); err != nil {
		t.Fatal(err)
	}
	fresh := http3Session(t, nil)
	unreachable := run(t, fresh, h3Get(t, fixture.URL("/echo"), "3"))
	if unreachable.Success || unreachable.ProtocolEvidence["http3_failure_certainty"] != "not_sent" {
		t.Fatalf("blackholed: message=%q evidence=%v", unreachable.ErrorMessage, unreachable.ProtocolEvidence)
	}
}

func TestExactHTTP3RefusesWhatItCannotCarry(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	plainURL := strings.Replace(fixture.URL("/echo"), "https://", "http://", 1)
	h := http3Session(t, nil)
	if r := run(t, h, h3Get(t, plainURL, "3")); r.Success || !strings.Contains(r.ErrorMessage, "HTTP/3 requires an https:// URL") {
		t.Fatalf("http:// target: %q", r.ErrorMessage)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(proxy.Close)
	proxied := http3Session(t, map[string]any{"proxy_mode": "custom", "proxy_url": proxy.URL})
	if r := run(t, proxied, h3Get(t, fixture.URL("/echo"), "3")); r.Success || !strings.Contains(r.ErrorMessage, "http3_proxy_unsupported") {
		t.Fatalf("proxied route: %q", r.ErrorMessage)
	}
	ntlm := http3Session(t, map[string]any{"auth_type": "ntlm", "auth_username": "user", "auth_password": "pass"})
	if r := run(t, ntlm, h3Get(t, fixture.URL("/echo"), "3")); r.Success || r.ErrorMessage != ErrHTTP3IntegratedAuth.Error() {
		t.Fatalf("integrated auth: %q", r.ErrorMessage)
	}
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3")); r.Success || !strings.Contains(r.ErrorMessage, "auto+h3 is not available in this build") {
		t.Fatalf("auto+h3: %q", r.ErrorMessage)
	}
	if n := len(fixture.Ledger.Attempts()); n != 0 {
		t.Fatalf("refused requests reached the target %d times", n)
	}
	// The same requests on TCP policies are unaffected by the HTTP/3 rules.
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "2")); !r.Success {
		t.Fatalf("HTTP/2 on the same session: %q", r.ErrorMessage)
	}
}

func TestExactHTTP3NeedsTheFlagAndReleasesSocketsAtTeardown(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	t.Setenv(EnvHTTP3, "")
	off := versionHTTP(t, nil)
	if r := run(t, off, h3Get(t, fixture.URL("/echo"), "3")); r.Success || !strings.Contains(r.ErrorMessage, EnvHTTP3) {
		t.Fatalf("flag off: %q", r.ErrorMessage)
	}
	if off.http3 != nil {
		t.Fatal("a session without the flag built an HTTP/3 transport")
	}
	h := http3Session(t, nil)
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	transport, err := h.http3.current()
	if err != nil || transport.OpenSockets() != 1 {
		t.Fatalf("sockets=%d err=%v", transport.OpenSockets(), err)
	}
	if err := h.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if transport.OpenSockets() != 0 {
		t.Fatalf("teardown left %d sockets open", transport.OpenSockets())
	}
	// A session that runs again after teardown gets a fresh transport.
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success {
		t.Fatalf("after teardown: %q", r.ErrorMessage)
	}
	if err := h.ValidateSharedSessionAction(h3Get(t, fixture.URL("/echo"), "3")); err != nil {
		t.Fatalf("concurrent validation of exact 3: %v", err)
	}
}

func TestExactHTTP3MethodBodyHeaderAndEncodingMatrix(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, nil)
	binary := "\x00\x01\xfe\xff binary é"
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		action := h3Get(t, fixture.URL("/echo?m="+method), "3")
		action.Type = method
		if method == "POST" || method == "PUT" || method == "PATCH" {
			action.Body = []byte(binary)
		}
		result := run(t, h, action)
		if !result.Success || result.HTTPVersionNegotiated != "3" {
			t.Fatalf("%s: success=%v negotiated=%q message=%q", method, result.Success, result.HTTPVersionNegotiated, result.ErrorMessage)
		}
	}
	sums := map[string]string{}
	for _, attempt := range fixture.Ledger.Attempts() {
		if attempt.Proto != "HTTP/3.0" {
			t.Fatalf("attempt over %s", attempt.Proto)
		}
		sums[attempt.Method] = attempt.BodySHA256
	}
	if len(sums) != 7 || sums["POST"] == "" || sums["POST"] != sums["PUT"] || sums["PUT"] != sums["PATCH"] {
		t.Fatalf("methods and body digests = %v", sums)
	}
	// Repeated header occurrences keep their order and count.
	repeated := h3Get(t, fixture.URL("/echo"), "3")
	repeated.HeaderList = protocol.HeaderList{{Name: "X-Multi", Value: "one"}, {Name: "X-Multi", Value: "two"}}
	result := run(t, h, repeated)
	var echo struct {
		Headers map[string][]string `json:"headers"`
	}
	if err := json.Unmarshal([]byte(result.ResponseBody), &echo); err != nil || strings.Join(echo.Headers["x-multi"], ",") != "one,two" {
		t.Fatalf("repeated header = %v (%v)", echo.Headers["x-multi"], err)
	}
	for _, coding := range []string{"gzip", "br", "zstd"} {
		action := h3Get(t, fixture.URL("/encoded?coding="+coding), "3")
		action.Headers = map[string]string{"Accept-Encoding": coding}
		result := run(t, h, action)
		if !result.Success || result.ResponseBody != h3fixture.EncodedBody {
			t.Fatalf("%s: success=%v body=%q message=%q", coding, result.Success, result.ResponseBody, result.ErrorMessage)
		}
	}
}

func TestExactHTTP3ManyIterationsLeakNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("10,000-request soak")
	}
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, nil)
	// Warm up so lazily started goroutines exist before the baseline.
	if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success {
		t.Fatal(r.ErrorMessage)
	}
	baseline := runtime.NumGoroutine()
	for i := range 10000 {
		if r := run(t, h, h3Get(t, fixture.URL("/echo"), "3")); !r.Success {
			t.Fatalf("iteration %d: %s", i, r.ErrorMessage)
		}
	}
	transport, _ := h.http3.current()
	if transport.OpenConnections() != 1 || transport.OpenSockets() != 1 {
		t.Fatalf("after 10,000 requests: connections=%d sockets=%d", transport.OpenConnections(), transport.OpenSockets())
	}
	if grown := runtime.NumGoroutine() - baseline; grown > 10 {
		t.Fatalf("goroutines grew by %d over 10,000 requests", grown)
	}
	if err := h.Teardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if transport.OpenSockets() != 0 || transport.OpenConnections() != 0 {
		t.Fatalf("teardown left connections=%d sockets=%d", transport.OpenConnections(), transport.OpenSockets())
	}
}
