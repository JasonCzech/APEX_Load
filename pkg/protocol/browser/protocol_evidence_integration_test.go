package browser

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// trustFixtureForQUIC makes Chromium accept the fixture's throwaway
// certificate over TCP and QUIC for this test's browsers only. With force,
// Chromium also uses QUIC to the fixture from the first request: it never
// acts on an Alt-Svc advertisement from a certificate that does not chain to
// a publicly trusted root, so the loopback fixture cannot exercise discovery.
func trustFixtureForQUIC(t *testing.T, fixture *h3fixture.Server, force bool) {
	t.Helper()
	args := []string{"--ignore-certificate-errors-spki-list=" + fixture.Certs.LeafSPKISHA256}
	if force {
		args = append(args, "--origin-to-force-quic-on="+strings.TrimPrefix(fixture.URL(""), "https://"))
	}
	globalMutex.Lock()
	previous := testChromiumLaunchArgs
	testChromiumLaunchArgs = args
	globalMutex.Unlock()
	t.Cleanup(func() {
		globalMutex.Lock()
		testChromiumLaunchArgs = previous
		globalMutex.Unlock()
	})
}

func protocolSession(t *testing.T, fixture *h3fixture.Server, quic string) *Browser {
	t.Helper()
	b := &Browser{}
	config := map[string]any{"base_url": fixture.URL(""), "browser_headless": true, "browser_engine": "chromium", "browser_capture_document_metrics": true}
	if quic != "" {
		config["browser_quic"] = quic
	}
	if err := b.Init(context.Background(), config); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	t.Cleanup(func() { _ = b.Teardown(context.Background()) })
	return b
}

func documentEvidence(t *testing.T, entries []protocol.NetworkEntry, marker string) *protocol.BrowserResourceEvidence {
	t.Helper()
	for _, e := range entries {
		if strings.Contains(e.URL, marker) && e.BrowserEvidence != nil {
			return e.BrowserEvidence
		}
	}
	t.Fatalf("no evidence for %s among %d entries", marker, len(entries))
	return nil
}

func TestChromiumRecordsNegotiatedProtocolPerResource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	t.Run("tcp", func(t *testing.T) {
		fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort, AltSvcMaxAge: 600})
		trustFixtureForQUIC(t, fixture, false)
		b := protocolSession(t, fixture, "")
		v := documentEvidence(t, associationVisit(t, b, fixture.URL("/?tcp=1")), "/?tcp=1")
		if v.Version != protocol.BrowserResourceEvidenceVersion || v.Protocol != protocol.BrowserResourceProtocolHTTP2 || v.ProtocolUnavailableReason != "" {
			t.Fatalf("TCP document evidence version=%d protocol=%q reason=%q", v.Version, v.Protocol, v.ProtocolUnavailableReason)
		}
	})
	t.Run("quic", func(t *testing.T) {
		fixture := h3fixture.Start(t, h3fixture.Options{})
		trustFixtureForQUIC(t, fixture, true)
		b := protocolSession(t, fixture, "")
		for visit := range 2 {
			marker := "/?quic=" + strconv.Itoa(visit)
			v := documentEvidence(t, associationVisit(t, b, fixture.URL(marker)), marker)
			if v.Protocol != protocol.BrowserResourceProtocolHTTP3 {
				t.Fatalf("visit %d protocol %q (reason %q), want h3", visit, v.Protocol, v.ProtocolUnavailableReason)
			}
		}
		if fixture.Ledger.Count(func(a h3fixture.Attempt) bool { return a.Proto == "HTTP/3.0" }) == 0 {
			t.Fatal("evidence claimed h3 but the target saw no HTTP/3 request")
		}
	})
}

func TestChromiumQUICOffNeverUsesHTTP3(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort, AltSvcMaxAge: 600})
	// Force QUIC to the fixture, so only --disable-quic can keep the browser
	// on TCP; without force Chromium would stay on TCP regardless.
	trustFixtureForQUIC(t, fixture, true)
	b := protocolSession(t, fixture, browserQUICOff)
	for visit := range 4 {
		marker := "/?off=" + strconv.Itoa(visit)
		v := documentEvidence(t, associationVisit(t, b, fixture.URL(marker)), marker)
		if v.Protocol != protocol.BrowserResourceProtocolHTTP2 {
			t.Fatalf("visit %d protocol %q (reason %q), want h2 with QUIC off", visit, v.Protocol, v.ProtocolUnavailableReason)
		}
	}
	if n := fixture.Ledger.Count(func(a h3fixture.Attempt) bool { return a.Listener != "tcp" }); n != 0 {
		t.Fatalf("QUIC off still reached the QUIC listener %d times", n)
	}
}

func TestChromiumCachedResourceNeverClaimsAProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	fixture := h3fixture.Start(t, h3fixture.Options{})
	trustFixtureForQUIC(t, fixture, false)
	b := protocolSession(t, fixture, browserQUICOff)
	first := documentEvidence(t, associationVisit(t, b, fixture.URL("/cacheable")), "/cacheable")
	if first.Protocol == "" {
		t.Fatalf("network load reported no protocol: reason %q", first.ProtocolUnavailableReason)
	}
	second := documentEvidence(t, associationVisit(t, b, fixture.URL("/cacheable")), "/cacheable")
	if second.Cache == nil || !*second.Cache {
		t.Skipf("second load was not served from cache (cache=%v); nothing to assert", second.Cache)
	}
	if second.Protocol != "" || second.ProtocolUnavailableReason != "cached" {
		t.Fatalf("cached load protocol %q reason %q, want empty and cached", second.Protocol, second.ProtocolUnavailableReason)
	}
}
