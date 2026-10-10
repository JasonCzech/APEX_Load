package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// upgradeReceipt asserts the routing fields every auto+h3 receipt carries.
func upgradeReceipt(t *testing.T, label string, result *protocol.Result, negotiated, selection, fallback string, attempted bool) {
	t.Helper()
	evidence := result.ProtocolEvidence
	if result.HTTPVersionPolicy != VersionAutoH3 || result.HTTPVersionNegotiated != negotiated ||
		evidence["http_version_selection_reason"] != selection || evidence["http_version_fallback"] != fallback ||
		evidence["quic_attempted"] != attempted {
		t.Fatalf("%s: success=%v policy=%q negotiated=%q message=%q receipt=%v", label, result.Success, result.HTTPVersionPolicy,
			result.HTTPVersionNegotiated, result.ErrorMessage, evidence)
	}
}

func hostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

func TestAutoH3UsesTCPUntilTheOriginAdvertisesHTTP3(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	// The session policy applies to requests without their own.
	h := http3Session(t, map[string]any{"http_version": "auto+h3"})
	first := run(t, h, h3Get(t, fixture.URL("/echo"), ""))
	if !first.Success {
		t.Fatal(first.ErrorMessage)
	}
	upgradeReceipt(t, "discovery", first, VersionHTTP2, "not_advertised", "none", false)
	if first.ProtocolEvidence["alt_svc_h3_advertised"] != true || first.ProtocolEvidence["timing_availability"] != nil {
		t.Fatalf("a TCP hop's receipt: %v", first.ProtocolEvidence)
	}
	second := run(t, h, h3Get(t, fixture.URL("/echo"), ""))
	if !second.Success {
		t.Fatal(second.ErrorMessage)
	}
	upgradeReceipt(t, "upgrade", second, VersionHTTP3, "upgraded", "none", true)
	if second.ProtocolEvidence["quic_connection"] != "new" || second.ProtocolEvidence["timing_availability"] == nil {
		t.Fatalf("an upgraded hop's receipt: %v", second.ProtocolEvidence)
	}
	attempts := fixture.Ledger.Attempts()
	if len(attempts) != 2 || attempts[0].Listener != "tcp" || attempts[1].Listener != "quic" ||
		attempts[0].AltUsed != "" || attempts[1].AltUsed != hostPort(fixture.Host, fixture.Port) {
		t.Fatalf("ledger = %+v", attempts)
	}
	third := run(t, h, h3Get(t, fixture.URL("/echo"), ""))
	if third.ProtocolEvidence["quic_connection"] != "reused" {
		t.Fatalf("the upgraded connection was not reused: %v", third.ProtocolEvidence)
	}
}

func TestAutoH3UsesANon443AlternativePort(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{AlternatePort: true, Advertise: h3fixture.AdvertiseAltPort})
	h := http3Session(t, nil)
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	result := run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	upgradeReceipt(t, "alternate port", result, VersionHTTP3, "upgraded", "none", true)
	last := fixture.Ledger.Attempts()[1]
	if last.Listener != "quic-alt" || last.AltUsed != hostPort(fixture.Host, fixture.AltPort) {
		t.Fatalf("the alternative was not used: %+v", last)
	}
}

func TestAutoH3CoalescesConcurrentRequestsOntoOneDial(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	fixture.SetHandshakeDelay(100 * time.Millisecond)
	h := http3Session(t, map[string]any{quicHandshakeTimeoutKey: float64(5000)})
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	var wg sync.WaitGroup
	results := make([]*protocol.Result, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
		}()
	}
	wg.Wait()
	connections := map[any]int{}
	for i, result := range results {
		if !result.Success || result.HTTPVersionNegotiated != VersionHTTP3 {
			t.Fatalf("request %d: negotiated=%q %s", i, result.HTTPVersionNegotiated, result.ErrorMessage)
		}
		connections[result.ProtocolEvidence["quic_connection"]]++
	}
	if fixture.QUICConnections() != 1 || connections["new"] != 1 {
		t.Fatalf("concurrent upgrades opened %d connections; receipts %v", fixture.QUICConnections(), connections)
	}
}

func TestAutoH3MisdirectedRequestKeepsTheOriginOnTCP(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	h := http3Session(t, nil)
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	fixture.MisdirectNextQUICRequests(1)
	result := run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	if !result.Success || result.StatusCode != http.StatusOK {
		t.Fatalf("421: status=%d %s", result.StatusCode, result.ErrorMessage)
	}
	upgradeReceipt(t, "421", result, VersionHTTP2, "upgraded", "misdirected_request", true)
	upgradeReceipt(t, "after 421", run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3")), VersionHTTP2, "broken_cache", "none", false)
}

func TestAutoH3AltSvcClearAndPerIterationCache(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	h := http3Session(t, nil)
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	fixture.SetAltSvc("clear")
	upgradeReceipt(t, "cleared on QUIC", run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3")), VersionHTTP3, "upgraded", "none", true)
	upgradeReceipt(t, "after clear", run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3")), VersionHTTP2, "not_advertised", "none", false)

	fixture.SetAltSvc(fixture.AltSvcFor(fixture.Port, 0))
	for _, tc := range []struct {
		cache string
		want  string
	}{{QUICAltSvcCachePerVUser, "upgraded"}, {QUICAltSvcCachePerIteration, "not_advertised"}} {
		session := http3Session(t, map[string]any{quicAltSvcCacheKey: tc.cache})
		run(t, session, h3Get(t, fixture.URL("/echo"), "auto+h3"))
		if err := session.BeginIteration(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := run(t, session, h3Get(t, fixture.URL("/echo"), "auto+h3")).ProtocolEvidence["http_version_selection_reason"]; got != tc.want {
			t.Fatalf("alt_svc_cache %s after an iteration: %v", tc.cache, got)
		}
	}
}

func TestAutoH3SelectionReasonsKeepRequestsOnTCP(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	tls12 := http3Session(t, map[string]any{"tls_max_version": "1.2"})
	for range 2 {
		result := run(t, tls12, h3Get(t, fixture.URL("/echo"), "auto+h3"))
		if !result.Success {
			t.Fatal(result.ErrorMessage)
		}
		upgradeReceipt(t, "TLS 1.2", result, VersionHTTP2, "tls_policy", "none", false)
	}
	ntlm := http3Session(t, map[string]any{"auth_type": "ntlm", "auth_username": "u", "auth_password": "p"})
	for range 2 {
		upgradeReceipt(t, "integrated auth", run(t, ntlm, h3Get(t, fixture.URL("/echo"), "auto+h3")), VersionHTTP2, "integrated_auth", "none", false)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(proxy.Close)
	proxied := http3Session(t, map[string]any{"proxy_mode": "custom", "proxy_url": proxy.URL})
	routed := run(t, proxied, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	if routed.ProtocolEvidence["http_version_selection_reason"] != "proxy_route" || routed.ProtocolEvidence["quic_attempted"] != false {
		t.Fatalf("proxy route: %v", routed.ProtocolEvidence)
	}
	if fixture.QUICConnections() != 0 {
		t.Fatalf("a TCP-only selection opened %d QUIC connections", fixture.QUICConnections())
	}
}

func TestAutoH3IsRefusedWithoutTheFlag(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	h := versionHTTP(t, nil)
	t.Setenv(EnvHTTP3, "")
	result := run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	if result.Success || !errors.Is(HTTP3UpgradeExecutionError(), ErrHTTP3NotEnabled) || len(fixture.Ledger.Attempts()) != 0 {
		t.Fatalf("flag off: success=%v message=%q attempts=%d", result.Success, result.ErrorMessage, len(fixture.Ledger.Attempts()))
	}
}

func TestAltSvcCacheEntries(t *testing.T) {
	now := time.Unix(1_000, 0)
	cache := newAltSvcCache(func() time.Time { return now })
	origin, _ := url.Parse("https://Example.test:8443/a")
	key, _, _, _ := originKey(origin)
	if key != "https://example.test:8443" {
		t.Fatalf("origin key %q", key)
	}
	for _, tc := range []struct {
		values []string
		port   int
		reason string
	}{
		{[]string{`h3=":9443"; ma=60`}, 9443, ""},
		{[]string{`h2=":443"`}, 9443, ""},                        // no h3: entry kept
		{[]string{`h3="other.test:443"`}, 9443, ""},              // another host: ignored
		{[]string{`h3-29=":7443", h3=":7443"; ma=60`}, 7443, ""}, // replaced by the final h3
		{[]string{`h3=":6443"`, `clear`}, 7443, ""},              // clear mixed with alternatives is malformed
		{[]string{`clear`}, 0, "not_advertised"},                 // clear forgets it
		{[]string{`h3=":6443"; ma=60`}, 6443, ""},                // re-advertised
		{[]string{`h3=":6443"; ma=0`}, 0, "not_advertised"},      // ma=0 withdraws it
		{[]string{`h3=":5443"; ma=60`}, 5443, ""},
		{nil, 5443, ""}, // no field: kept
	} {
		cache.observe(origin, tc.values)
		port, reason := cache.alternative(key)
		if port != tc.port || reason != tc.reason {
			t.Fatalf("after %q: port=%d reason=%q, want %d %q", tc.values, port, reason, tc.port, tc.reason)
		}
	}
	now = now.Add(61 * time.Second)
	if _, reason := cache.alternative(key); reason != "not_advertised" {
		t.Fatalf("expired entry: %q", reason)
	}
	cleartext, _ := url.Parse("http://example.test/")
	cache.observe(cleartext, []string{`h3=":443"`})
	if len(cache.entries) != 0 {
		t.Fatal("a cleartext origin was cached")
	}
	for i := range maxAltSvcOrigins + 10 {
		u, _ := url.Parse("https://host" + strconv.Itoa(i) + ".test/")
		cache.observe(u, []string{`h3=":443"`})
		k, _, _, _ := originKey(u)
		cache.markBroken(k, time.Minute)
	}
	if len(cache.entries) > maxAltSvcOrigins || len(cache.broken) > maxAltSvcOrigins {
		t.Fatalf("cache grew to %d entries and %d broken origins", len(cache.entries), len(cache.broken))
	}
	cache.markBroken("https://zero.test:443", 0)
	if _, broken := cache.broken["https://zero.test:443"]; broken {
		t.Fatal("broken_ttl_ms 0 recorded a broken origin")
	}
}

// FuzzAltSvcCacheObserve feeds untrusted Alt-Svc fields to the router's cache:
// it never panics, keeps only usable same-host ports and stays bounded.
func FuzzAltSvcCacheObserve(f *testing.F) {
	for _, seed := range []string{`h3=":443"`, `h3="example.test:8443"; ma=60`, `clear`, `h3=":0"`, `h3=":443"; ma=-1, clear`, `h3-29=":1", h3=":65535"; persist=1`} {
		f.Add("https://example.test", seed)
	}
	f.Fuzz(func(t *testing.T, rawURL, field string) {
		u, err := url.Parse(rawURL)
		if err != nil {
			return
		}
		cache := newAltSvcCache(time.Now)
		cache.observe(u, []string{field})
		key, _, _, ok := originKey(u)
		if !ok {
			if len(cache.entries) != 0 {
				t.Fatalf("a non-https origin %q was cached", rawURL)
			}
			return
		}
		port, reason := cache.alternative(key)
		if (reason == "") != (port >= 1 && port <= 65535) || len(cache.entries) > 1 {
			t.Fatalf("field %q: port=%d reason=%q entries=%d", field, port, reason, len(cache.entries))
		}
	})
}
