package http

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestHTTPNetworkTraceSocketPoolAndTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session_id", Value: "super-secret-cookie-val", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(`{"hello":"world","padding":"` + strings.Repeat("1234567890", 20) + `"}`))
		_ = gz.Close()
	}))
	defer server.Close()

	// Disabled by default: NetworkTrace must be nil.
	disabled := initHTTP(t, map[string]any{
		"base_url":                 server.URL,
		"tls_insecure_skip_verify": true,
		"allow_private_target":     true,
	})
	defer disabled.Teardown(context.Background())
	resDisabled, err := disabled.Execute(context.Background(), &protocol.Action{
		Name:   "disabled-trace",
		Type:   "request",
		Method: "GET",
		Target: "/test",
	})
	if err != nil || resDisabled == nil || !resDisabled.Success {
		t.Fatalf("disabled execute: %v, result=%+v", err, resDisabled)
	}
	if resDisabled.NetworkTrace != nil {
		t.Fatalf("expected nil NetworkTrace when disabled, got %+v", resDisabled.NetworkTrace)
	}
	if _, exists := resDisabled.ProtocolEvidence["network_trace"]; exists {
		t.Fatalf("unexpected network_trace in ProtocolEvidence: %+v", resDisabled.ProtocolEvidence)
	}

	// Enabled: first request performs fresh TLS handshake, second reuses idle connection.
	enabled := initHTTP(t, map[string]any{
		"base_url":                             server.URL,
		"tls_insecure_skip_verify":             true,
		"allow_private_target":                 true,
		protocol.ReplayNetworkCaptureConfigKey: true,
		protocol.ReplayNetworkTraceConfigKey:   true,
	})
	defer enabled.Teardown(context.Background())

	res1, err := enabled.Execute(context.Background(), &protocol.Action{
		Name:   "first-req",
		Type:   "request",
		Method: "POST",
		Target: "/test",
		Body:   []byte(`{"request":"payload"}`),
		Params: map[string]any{RequestContentEncodingParam: "gzip"},
	})
	if err != nil || res1 == nil || !res1.Success {
		t.Fatalf("first execute: %v, result=%+v", err, res1)
	}
	tr1 := res1.NetworkTrace
	if tr1 == nil {
		t.Fatal("expected NetworkTrace on first request")
	}
	if tr1.ConnectionReused == nil || *tr1.ConnectionReused {
		t.Fatalf("expected an observed fresh connection on first request, got reused=%v", tr1.ConnectionReused)
	}
	if reused, ok := tr1.EvidenceMap()["connection_reused"].(bool); !ok || reused {
		t.Fatalf("expected connection_reused=false emitted for a fresh socket, got %+v", tr1.EvidenceMap())
	}
	if tr1.RemoteAddr == "" || tr1.LocalAddr == "" {
		t.Fatalf("expected remote and local socket addresses, got remote=%q local=%q", tr1.RemoteAddr, tr1.LocalAddr)
	}
	if !strings.HasPrefix(tr1.TLSVersion, "TLS 1.") {
		t.Fatalf("expected TLS version, got %q", tr1.TLSVersion)
	}
	if tr1.TLSCipherSuite == "" {
		t.Fatal("expected non-empty TLS cipher suite")
	}
	if tr1.TLSCertSubject == "" && tr1.TLSCertIssuer == "" && tr1.TLSCertExpiry == "" {
		t.Fatalf("expected peer certificate metadata, got %+v", tr1)
	}
	if tr1.RequestCompression != "gzip" {
		t.Fatalf("expected request_compression=gzip, got %q", tr1.RequestCompression)
	}
	if tr1.ContentEncoding != "gzip" {
		t.Fatalf("expected content_encoding=gzip, got %q", tr1.ContentEncoding)
	}
	if tr1.EncodedBodyBytes <= 0 || tr1.DecodedBodyBytes <= tr1.EncodedBodyBytes {
		t.Fatalf("expected encoded (%d) < decoded (%d) bytes", tr1.EncodedBodyBytes, tr1.DecodedBodyBytes)
	}
	if tr1.SetCookiesReceivedCount != 1 || len(tr1.SetCookieNamesReceived) != 1 || tr1.SetCookieNamesReceived[0] != "session_id" {
		t.Fatalf("expected set_cookies=[session_id], got count=%d names=%v", tr1.SetCookiesReceivedCount, tr1.SetCookieNamesReceived)
	}
	for _, name := range tr1.SetCookieNamesReceived {
		if strings.Contains(name, "super-secret-cookie-val") {
			t.Fatal("cookie value leaked into cookie names")
		}
	}
	if fromEv := protocol.HTTPNetworkTraceFromEvidence(res1.ProtocolEvidence["network_trace"]); fromEv == nil || fromEv.TLSVersion != tr1.TLSVersion {
		t.Fatalf("expected network_trace in ProtocolEvidence, got %+v", res1.ProtocolEvidence["network_trace"])
	}
	if len(res1.NetworkEntries) != 1 || res1.NetworkEntries[0].Trace == nil {
		t.Fatalf("expected per-hop trace on NetworkEntries[0], got %+v", res1.NetworkEntries)
	}
	if res1.NetworkEntries[0].Trace.DecodedBodyBytes != tr1.DecodedBodyBytes {
		t.Fatalf("expected hop trace DecodedBodyBytes=%d, got %d", tr1.DecodedBodyBytes, res1.NetworkEntries[0].Trace.DecodedBodyBytes)
	}

	// Sleep briefly so idle pool duration is measurable on the second request.
	time.Sleep(15 * time.Millisecond)

	res2, err := enabled.Execute(context.Background(), &protocol.Action{
		Name:   "second-req",
		Type:   "request",
		Method: "GET",
		Target: "/test",
	})
	if err != nil || res2 == nil || !res2.Success {
		t.Fatalf("second execute: %v, result=%+v", err, res2)
	}
	tr2 := res2.NetworkTrace
	if tr2 == nil {
		t.Fatal("expected NetworkTrace on second request")
	}
	if tr2.ConnectionReused == nil || !*tr2.ConnectionReused || !tr2.WasIdle || tr2.IdleTimeMs <= 0 {
		t.Fatalf("expected reused idle connection on second request, got reused=%v wasIdle=%v idleMs=%v", tr2.ConnectionReused, tr2.WasIdle, tr2.IdleTimeMs)
	}
	if tr2.CookiesSentCount != 1 || len(tr2.CookieNamesSent) != 1 || tr2.CookieNamesSent[0] != "session_id" {
		t.Fatalf("expected jar cookie session_id sent on second request, got count=%d names=%v", tr2.CookiesSentCount, tr2.CookieNamesSent)
	}
}

func TestHTTPNetworkTraceCrossOriginRedirectAnd100Continue(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Authorization header should have been stripped on cross-origin redirect")
		}
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer dest.Close()
	destCrossDomain := strings.Replace(dest.URL, "127.0.0.1", "localhost", 1)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destCrossDomain+"/landed", http.StatusFound)
	}))
	defer origin.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                             origin.URL,
		"allow_private_target":                 true,
		protocol.ReplayNetworkCaptureConfigKey: true,
		protocol.ReplayNetworkTraceConfigKey:   true,
	})
	defer h.Teardown(context.Background())

	res, err := h.Execute(context.Background(), &protocol.Action{
		Name:   "redirect-step",
		Type:   "request",
		Method: "GET",
		Target: "/start",
		Headers: map[string]string{
			"Authorization": "Bearer secret-token",
			"Cookie":        "auth_sid=secret-cookie",
		},
	})
	if err != nil || res == nil || !res.Success {
		t.Fatalf("redirect execute: %v, result=%+v", err, res)
	}
	if res.NetworkTrace == nil || !res.NetworkTrace.CrossOriginRedirect {
		t.Fatalf("expected CrossOriginRedirect=true, got %+v", res.NetworkTrace)
	}
	if got := res.NetworkTrace.StrippedRedirectHeaders; !slices.Equal(got, []string{"Authorization", "Cookie"}) {
		t.Fatalf("expected exactly [Authorization Cookie] in StrippedRedirectHeaders, got %v", got)
	}
	if len(res.NetworkEntries) != 2 {
		t.Fatalf("expected 2 hop entries across redirect, got %d", len(res.NetworkEntries))
	}
	if res.NetworkEntries[0].StatusCode != http.StatusFound || res.NetworkEntries[1].StatusCode != http.StatusOK {
		t.Fatalf("unexpected hop statuses: %+v", res.NetworkEntries)
	}

	// Test 100-Continue trace telemetry
	contServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer contServer.Close()

	hCont := initHTTP(t, map[string]any{
		"base_url":                           contServer.URL,
		"allow_private_target":               true,
		protocol.ReplayNetworkTraceConfigKey: true,
	})
	defer hCont.Teardown(context.Background())

	resCont, err := hCont.Execute(context.Background(), &protocol.Action{
		Name:    "continue-step",
		Type:    "request",
		Method:  "POST",
		Target:  "/upload",
		Headers: map[string]string{"Expect": "100-continue"},
		Body:    []byte("payload-after-continue"),
	})
	if err != nil || resCont == nil || !resCont.Success {
		t.Fatalf("100-continue execute: %v, result=%+v", err, resCont)
	}
	if resCont.NetworkTrace == nil || !resCont.NetworkTrace.Got100Continue {
		t.Fatalf("expected Got100Continue=true, got %+v", resCont.NetworkTrace)
	}
}

func newTraceHTTP(t *testing.T, baseURL string) *HTTP {
	t.Helper()
	h := initHTTP(t, map[string]any{
		"base_url":                             baseURL,
		"allow_private_target":                 true,
		protocol.ReplayNetworkCaptureConfigKey: true,
		protocol.ReplayNetworkTraceConfigKey:   true,
	})
	t.Cleanup(func() { _ = h.Teardown(context.Background()) })
	return h
}

func executeTraceGET(t *testing.T, h *HTTP, name, target string) *protocol.Result {
	t.Helper()
	res, err := h.Execute(context.Background(), &protocol.Action{Name: name, Type: "request", Method: "GET", Target: target})
	if err != nil || res == nil {
		t.Fatalf("%s execute: %v, result=%+v", name, err, res)
	}
	return res
}

// The redirect trace compares the cookie names the previous hop sent with the
// names the destination will carry (copied header plus jar), not header
// presence: net/http adds jar cookies only after CheckRedirect returns.
func TestHTTPNetworkTraceRedirectCookieStripUsesJarAndNames(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer dest.Close()
	destOtherHost := strings.Replace(dest.URL, "127.0.0.1", "localhost", 1)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "secret-session", Path: "/"})
			_, _ = w.Write([]byte("logged in"))
		case "/to-other-host":
			http.Redirect(w, r, destOtherHost+"/landed", http.StatusFound)
		case "/to-other-port":
			// Same host, different port: cross-origin, but the host-only
			// cookie still matches, so the jar re-sends it.
			http.Redirect(w, r, dest.URL+"/landed", http.StatusFound)
		case "/set-and-redirect":
			http.SetCookie(w, &http.Cookie{Name: "fresh", Value: "set-by-3xx", Path: "/"})
			http.Redirect(w, r, destOtherHost+"/landed", http.StatusFound)
		}
	}))
	defer origin.Close()

	t.Run("jar cookie dropped on another host is reported", func(t *testing.T) {
		h := newTraceHTTP(t, origin.URL)
		executeTraceGET(t, h, "login", "/login")
		res := executeTraceGET(t, h, "redirect", "/to-other-host")
		if res.NetworkTrace == nil || !res.NetworkTrace.CrossOriginRedirect {
			t.Fatalf("expected a cross-origin redirect trace, got %+v", res.NetworkTrace)
		}
		if got := res.NetworkTrace.StrippedRedirectHeaders; !slices.Equal(got, []string{"Cookie"}) {
			t.Fatalf("expected [Cookie] stripped, got %v", got)
		}
	})

	t.Run("jar cookie re-sent to the destination is not reported", func(t *testing.T) {
		h := newTraceHTTP(t, origin.URL)
		executeTraceGET(t, h, "login", "/login")
		res := executeTraceGET(t, h, "redirect", "/to-other-port")
		if res.NetworkTrace == nil || !res.NetworkTrace.CrossOriginRedirect {
			t.Fatalf("expected a cross-origin redirect trace, got %+v", res.NetworkTrace)
		}
		if got := res.NetworkTrace.StrippedRedirectHeaders; len(got) != 0 {
			t.Fatalf("expected no stripped headers when the jar re-sends the cookie, got %v", got)
		}
		if !slices.Contains(res.NetworkTrace.CookieNamesSent, "sid") {
			t.Fatalf("expected the final hop to send sid, got %v", res.NetworkTrace.CookieNamesSent)
		}
	})

	t.Run("cookie set by the redirect response is not reported", func(t *testing.T) {
		h := newTraceHTTP(t, origin.URL)
		res := executeTraceGET(t, h, "redirect", "/set-and-redirect")
		if res.NetworkTrace == nil || !res.NetworkTrace.CrossOriginRedirect {
			t.Fatalf("expected a cross-origin redirect trace, got %+v", res.NetworkTrace)
		}
		if got := res.NetworkTrace.StrippedRedirectHeaders; len(got) != 0 {
			t.Fatalf("expected no stripped headers when the first hop sent no cookie, got %v", got)
		}
	})
}

// The aggregate trace describes the final hop: a hop that resolved DNS must
// not leave its addresses on an IP-literal final hop, and per-connection
// fields from an earlier hop are cleared at each hop boundary.
func TestHTTPNetworkTraceAggregateDescribesFinalHop(t *testing.T) {
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secure"))
	}))
	defer secure.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL+"/landed", http.StatusFound)
	}))
	defer plain.Close()
	// localhost forces a DNS lookup on the first hop; the final hop is an IP.
	plainByName := strings.Replace(plain.URL, "127.0.0.1", "localhost", 1)

	h := initHTTP(t, map[string]any{
		"base_url":                             plainByName,
		"tls_insecure_skip_verify":             true,
		"allow_private_target":                 true,
		protocol.ReplayNetworkCaptureConfigKey: true,
		protocol.ReplayNetworkTraceConfigKey:   true,
	})
	defer h.Teardown(context.Background())
	res := executeTraceGET(t, h, "http-to-https", "/start")
	if !res.Success {
		t.Fatalf("redirect execute failed: %+v", res)
	}
	tr := res.NetworkTrace
	if tr == nil || !tr.CrossOriginRedirect {
		t.Fatalf("expected cross-origin redirect trace, got %+v", tr)
	}
	if len(res.NetworkEntries) != 2 || res.NetworkEntries[0].Trace == nil || len(res.NetworkEntries[0].Trace.DNSResolvedIPs) == 0 {
		t.Fatalf("expected the first hop's own trace to keep its DNS answer, got %+v", res.NetworkEntries)
	}
	if len(tr.DNSResolvedIPs) != 0 {
		t.Fatalf("expected no DNS IPs from the first hop on the IP-literal final hop, got %v", tr.DNSResolvedIPs)
	}
	if tr.RemoteAddr != strings.TrimPrefix(secure.URL, "https://") {
		t.Fatalf("expected remote_addr of the final hop %s, got %q", secure.URL, tr.RemoteAddr)
	}
	if tr.TLSVersion == "" {
		t.Fatalf("expected the final HTTPS hop's TLS state, got %+v", tr)
	}

	// A hop boundary clears TLS, 100-continue and idle state from the hop
	// before it (an HTTPS hop redirecting to plain HTTP would otherwise keep
	// them), while the redirect receipts accumulate.
	c := newHTTPTraceCollector()
	c.onTLSHandshakeDone(tls.ConnectionState{Version: tls.VersionTLS13, CipherSuite: tls.TLS_AES_128_GCM_SHA256, ServerName: "a.example", DidResume: true}, nil)
	c.onWait100Continue()
	c.onGot100Continue()
	c.trace.WasIdle, c.trace.IdleTimeMs = true, 12
	c.trace.CrossOriginRedirect = true
	c.trace.StrippedRedirectHeaders = []string{"Authorization"}
	c.onGetConn()
	got := c.snapshot()
	if got.TLSVersion != "" || got.TLSCipherSuite != "" || got.TLSServerName != "" || got.TLSDidResume || got.Got100Continue || got.WasIdle || got.IdleTimeMs != 0 {
		t.Fatalf("expected per-connection fields cleared at the hop boundary, got %+v", got)
	}
	if !got.CrossOriginRedirect || !slices.Equal(got.StrippedRedirectHeaders, []string{"Authorization"}) {
		t.Fatalf("expected redirect receipts kept across hops, got %+v", got)
	}
}

// net/http merges jar cookies into the request before every attempt, so a
// transport failure must neither count them twice nor claim a refused dial
// sent them.
func TestHTTPNetworkTraceCookiesSentOnTransportErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "secret-1", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "secret-2", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}
	}))
	defer server.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedURL := "http://" + listener.Addr().String() + "/refused"
	_ = listener.Close()

	h := newTraceHTTP(t, server.URL)
	executeTraceGET(t, h, "login", "/login")

	timedOut, err := h.Execute(context.Background(), &protocol.Action{Name: "slow", Type: "request", Method: "GET", Target: "/slow", TimeoutMs: 200})
	if err != nil || timedOut == nil || timedOut.Success {
		t.Fatalf("expected a timeout failure, got err=%v result=%+v", err, timedOut)
	}
	if tr := timedOut.NetworkTrace; tr == nil || tr.CookiesSentCount != 2 || len(tr.CookieNamesSent) != 2 {
		t.Fatalf("expected two cookies sent once on a response timeout, got %+v", tr)
	}

	// Host-only cookies ignore the port, so the jar matches the refused URL.
	refused, err := h.Execute(context.Background(), &protocol.Action{Name: "refused", Type: "request", Method: "GET", Target: refusedURL})
	if err != nil || refused == nil || refused.Success {
		t.Fatalf("expected a refused dial failure, got err=%v result=%+v", err, refused)
	}
	if tr := refused.NetworkTrace; tr == nil || tr.CookiesSentCount != 0 || len(tr.CookieNamesSent) != 0 {
		t.Fatalf("expected no cookies sent on a refused dial, got %+v", tr)
	}
	if tr := refused.NetworkTrace; tr.ConnectionReused != nil {
		t.Fatalf("expected connection_reused unknown on a refused dial, got %v", *tr.ConnectionReused)
	}
}

// A transport failure on a later redirect hop is described by that hop, on
// the production client path: Client.Timeout forks every request and the HAR
// recorder wraps the transport, so neither resp.Request nor a
// Response.Request chain leads back to the request passed to Client.Do.
func TestHTTPNetworkTraceLaterHopTransportFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "b1", Value: "secret-b", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case "/stall":
			// The headers arrived; the response never does.
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}
	}))
	defer dest.Close()
	// Another host, so the jar keeps each origin's host-only cookie apart.
	destOtherHost := strings.Replace(dest.URL, "127.0.0.1", "localhost", 1)
	refusedOtherHost := "http://localhost:" + strconv.Itoa(refusedPort)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "a1", Value: "secret-a", Path: "/"})
			_, _ = w.Write([]byte("ok"))
		case "/to-stall":
			http.Redirect(w, r, destOtherHost+"/stall", http.StatusFound)
		case "/to-refused":
			http.Redirect(w, r, refusedOtherHost+"/refused", http.StatusFound)
		}
	}))
	defer origin.Close()

	h := newTraceHTTP(t, origin.URL)
	executeTraceGET(t, h, "login-a", "/login")
	executeTraceGET(t, h, "login-b", destOtherHost+"/login")

	stalled, err := h.Execute(context.Background(), &protocol.Action{Name: "stall", Type: "request", Method: "GET", Target: "/to-stall", TimeoutMs: 300})
	if err != nil || stalled == nil || stalled.Success {
		t.Fatalf("expected a timeout on the redirect hop, got err=%v result=%+v", err, stalled)
	}
	if tr := stalled.NetworkTrace; tr == nil || tr.CookiesSentCount != 1 || !slices.Equal(tr.CookieNamesSent, []string{"b1"}) {
		t.Fatalf("expected the stalled hop's cookie b1, not the first hop's a1, got %+v", tr)
	}

	refused, err := h.Execute(context.Background(), &protocol.Action{Name: "refused", Type: "request", Method: "GET", Target: "/to-refused", TimeoutMs: 2000})
	if err != nil || refused == nil || refused.Success {
		t.Fatalf("expected a refused dial on the redirect hop, got err=%v result=%+v", err, refused)
	}
	if tr := refused.NetworkTrace; tr == nil || tr.CookiesSentCount != 0 || len(tr.CookieNamesSent) != 0 {
		t.Fatalf("expected no cookies sent by a refused redirect hop, got %+v", tr)
	}
}
