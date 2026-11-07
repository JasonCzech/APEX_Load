package http

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
		"base_url":                            server.URL,
		"tls_insecure_skip_verify":            true,
		"allow_private_target":                true,
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
	if tr1.ConnectionReused {
		t.Fatalf("expected fresh connection on first request, got reused=true")
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
	if !tr2.ConnectionReused || !tr2.WasIdle || tr2.IdleTimeMs <= 0 {
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
		"base_url":                            origin.URL,
		"allow_private_target":                true,
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
	if len(res.NetworkTrace.StrippedRedirectHeaders) < 2 {
		t.Fatalf("expected Authorization and Cookie in StrippedRedirectHeaders, got %v", res.NetworkTrace.StrippedRedirectHeaders)
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
		"base_url":                          contServer.URL,
		"allow_private_target":              true,
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
