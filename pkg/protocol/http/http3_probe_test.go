package http

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
)

func TestProbeHTTP3CompletesAHandshakeWithoutARequest(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, map[string]any{"http_version": "3"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome := h.ProbeHTTP3(ctx, fmt.Sprintf("https://127.0.0.1:%d", fixture.Port))
	if outcome.Err != nil || outcome.Address == "" || outcome.Duration <= 0 {
		t.Fatalf("probe = %+v", outcome)
	}
	if n := fixture.Ledger.Count(func(h3fixture.Attempt) bool { return true }); n != 0 {
		t.Fatalf("the probe sent %d requests", n)
	}
	// The probe's connection is closed; the session holds none open.
	if open := h.http3.openConnections(); open != 0 {
		t.Fatalf("probe left %d connections open", open)
	}
}

func TestProbeHTTP3ReportsAFailedHandshakeAsNotSent(t *testing.T) {
	// A UDP port with nothing listening.
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
	h := http3Session(t, map[string]any{"http_version": "3", quicHandshakeTimeoutKey: float64(1000)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome := h.ProbeHTTP3(ctx, fmt.Sprintf("https://127.0.0.1:%d", port))
	if outcome.Err == nil || outcome.ErrorClass == "" || HTTP3FailureCertainty(outcome.Err) != "not_sent" {
		t.Fatalf("probe = %+v (certainty %q)", outcome, HTTP3FailureCertainty(outcome.Err))
	}
	if bad := h.ProbeHTTP3(ctx, "http://127.0.0.1:443"); bad.Err == nil {
		t.Fatal("a cleartext origin cannot be probed")
	}
}

func TestProbeHTTP3SkipsProxiedRoutesButNotNoProxyHosts(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	h := http3Session(t, map[string]any{"http_version": "auto+h3", "proxy_mode": "custom", "proxy_url": "http://127.0.0.1:9", "no_proxy": "127.0.0.1"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if outcome := h.ProbeHTTP3(ctx, "https://proxied.example"); !outcome.Proxied || outcome.Err != nil || outcome.Address != "" {
		t.Fatalf("a proxied origin is not checked: %+v", outcome)
	}
	if outcome := h.ProbeHTTP3(ctx, fmt.Sprintf("https://127.0.0.1:%d", fixture.Port)); outcome.Proxied || outcome.Err != nil || outcome.Address == "" {
		t.Fatalf("a no_proxy origin keeps HTTP/3 and is checked: %+v", outcome)
	}
}

func TestProbeHTTP3NeedsHTTP3Execution(t *testing.T) {
	t.Setenv(EnvHTTP3, "false")
	h := versionHTTP(t, nil)
	if outcome := h.ProbeHTTP3(context.Background(), "https://example.test"); outcome.Err == nil {
		t.Fatal("probing needs HTTP/3 execution")
	}
}

func TestHTTP3PreflightPolicyDefaultsToReport(t *testing.T) {
	for raw, want := range map[string]string{"": "report", "REPORT": "report", "block": "block", " Off ": "off", "blok": "report"} {
		if got := HTTP3PreflightPolicy(func(string) string { return raw }); got != want {
			t.Fatalf("policy(%q) = %q, want %q", raw, got, want)
		}
	}
}
