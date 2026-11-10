package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Round 3 #5: TTFB is the first leg's first byte, so DNS/connect/TLS must
// describe that same leg. A cleartext first leg that redirects to a TLS host
// reported the second leg's handshake next to the first leg's TTFB, so
// server = TTFB - setup subtracted a later leg's setup.
func TestHTTPRedirectSetupPhasesDescribeTheTTFBLeg(t *testing.T) {
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("landed"))
	}))
	defer secure.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL+"/landed", http.StatusFound)
	}))
	defer plain.Close()

	h := initHTTP(t, map[string]any{
		"base_url":                 plain.URL,
		"tls_insecure_skip_verify": true,
		"allow_private_target":     true,
	})
	defer h.Teardown(context.Background())
	res := executeTraceGET(t, h, "http-to-https", "/start")
	if !res.Success {
		t.Fatalf("redirect execute failed: %+v", res)
	}
	if res.TTFB <= 0 {
		t.Fatalf("no TTFB: %+v", res)
	}
	if res.TLSTime != 0 {
		t.Fatalf("TLS %v belongs to the redirect leg, not the cleartext leg TTFB %v measures", res.TLSTime, res.TTFB)
	}
	if setup := res.DNSTime + res.ConnectTime + res.TLSTime; setup > res.TTFB {
		t.Fatalf("setup %v exceeds TTFB %v", setup, res.TTFB)
	}
	if res.TTFB > res.Duration {
		t.Fatalf("TTFB %v exceeds duration %v", res.TTFB, res.Duration)
	}
}
