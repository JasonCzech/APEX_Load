package http

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/http/internal/h3transport"
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

func TestAutoH3FallsBackToTCPWhenQUICCannotConnect(t *testing.T) {
	for _, state := range []h3fixture.QUICState{h3fixture.QUICClosed, h3fixture.QUICBlackholed} {
		fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
		h := http3Session(t, nil)
		run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
		if err := fixture.SetQUICState(state); err != nil {
			t.Fatal(err)
		}
		fellBack := run(t, h, h3Get(t, fixture.URL("/effect?key=f"), "auto+h3"))
		if !fellBack.Success {
			t.Fatalf("state %v: %s", state, fellBack.ErrorMessage)
		}
		upgradeReceipt(t, "fallback", fellBack, VersionHTTP2, "upgraded", "quic_connect_failed", true)
		if fellBack.ProtocolEvidence["quic_connection"] != "dial_failed" || fellBack.ProtocolEvidence["attempt_count"] != 2 ||
			fellBack.ProtocolEvidence["http3_failure_certainty"] != nil {
			t.Fatalf("state %v: fallback receipt %v", state, fellBack.ProtocolEvidence)
		}
		if fixture.Ledger.Effects("f") != 1 {
			t.Fatalf("state %v: the fallen-back write took effect %d times", state, fixture.Ledger.Effects("f"))
		}
		// The origin is now known broken: no QUIC attempt until the TTL ends.
		next := run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
		upgradeReceipt(t, "broken", next, VersionHTTP2, "broken_cache", "none", false)
	}
}

func TestAutoH3BrokenOriginRetriesQUICAfterItsTTL(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	h := http3Session(t, nil)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	h.altSvc.now = func() time.Time { return time.Unix(0, now.Load()) }
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	if err := fixture.SetQUICState(h3fixture.QUICClosed); err != nil {
		t.Fatal(err)
	}
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	if err := fixture.SetQUICState(h3fixture.QUICServing); err != nil {
		t.Fatal(err)
	}
	upgradeReceipt(t, "within TTL", run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3")), VersionHTTP2, "broken_cache", "none", false)
	now.Add(int64(QUICBrokenTTLDefaultMS*time.Millisecond) + 1)
	upgradeReceipt(t, "after TTL", run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3")), VersionHTTP3, "upgraded", "none", true)
}

func TestAutoH3NeverRepeatsARequestTheTargetMayHaveSeen(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	h := http3Session(t, nil)
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	for _, tc := range []struct{ key, query, certainty string }{
		// Dropped before any response header: the target may or may not
		// have processed it.
		{"p", "headers=0", "possibly_sent"},
		// Dropped mid-body: the response had started, so the request was
		// sent and the router had already returned it.
		{"q", "", ""},
	} {
		target := fixture.URL("/partial?key=" + tc.key)
		if tc.query != "" {
			target += "&" + tc.query
		}
		partial := &protocol.Action{Name: "write", Type: "POST", Target: target, Body: []byte("payload"), HTTPVersion: versionOverride(t, "auto+h3")}
		failed := run(t, h, partial)
		certainty, _ := failed.ProtocolEvidence["http3_failure_certainty"].(string)
		if failed.Success || certainty != tc.certainty || failed.ProtocolEvidence["http_version_fallback"] != "none" || fixture.Ledger.Effects(tc.key) != 1 {
			t.Fatalf("a drop after the effect (%s): success=%v effects=%d receipt=%v", tc.key, failed.Success, fixture.Ledger.Effects(tc.key), failed.ProtocolEvidence)
		}
		if fixture.Ledger.Count(func(a h3fixture.Attempt) bool { return a.Key == tc.key && a.Listener == "tcp" }) != 0 {
			t.Fatalf("the write (%s) was repeated on TCP", tc.key)
		}
	}
	// Both rejections are absorbed by quic-go's one retry and then fail:
	// the target refused it unprocessed, but the action's attempts are spent.
	fixture.RejectNextQUICRequests(2)
	rejected := run(t, h, h3Get(t, fixture.URL("/effect?key=r"), "auto+h3"))
	if rejected.Success || rejected.ProtocolEvidence["quic_error_class"] != "request_rejected" ||
		rejected.ProtocolEvidence["http_version_fallback"] != "none" || fixture.Ledger.Effects("r") != 0 {
		t.Fatalf("rejected twice: %s %v", rejected.ErrorMessage, rejected.ProtocolEvidence)
	}
}

func TestAutoH3VersionFallbackReplaysOnlyReplaySafeRequests(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{Advertise: h3fixture.AdvertiseSamePort})
	h := http3Session(t, nil)
	run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	fixture.VersionFallbackNextQUICRequests(1)
	get := run(t, h, h3Get(t, fixture.URL("/echo"), "auto+h3"))
	if !get.Success {
		t.Fatal(get.ErrorMessage)
	}
	upgradeReceipt(t, "GET", get, VersionHTTP11, "upgraded", "h3_version_fallback", true)
	fixture.VersionFallbackNextQUICRequests(1)
	post := run(t, h, &protocol.Action{Name: "write", Type: "POST", Target: fixture.URL("/effect?key=v"), Body: []byte("x"), HTTPVersion: versionOverride(t, "auto+h3")})
	if post.Success || post.ProtocolEvidence["http3_failure_certainty"] != "possibly_sent" || fixture.Ledger.Effects("v") != 0 ||
		!strings.HasPrefix(post.ErrorMessage, "auto+h3 HTTP/3 attempt failed: possibly_sent") {
		t.Fatalf("POST after H3_VERSION_FALLBACK: success=%v effects=%d receipt=%v", post.Success, fixture.Ledger.Effects("v"), post.ProtocolEvidence)
	}
	// Exact 3 never falls back; the receipt names the refusal.
	fixture.VersionFallbackNextQUICRequests(1)
	exact := run(t, h, h3Get(t, fixture.URL("/echo"), "3"))
	if exact.Success || exact.ProtocolEvidence["quic_error_class"] != "version_fallback" || exact.ProtocolEvidence["http_version_fallback"] != nil {
		t.Fatalf("exact 3 after H3_VERSION_FALLBACK: %s %v", exact.ErrorMessage, exact.ProtocolEvidence)
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

// failingQUIC fails every request with a fixed HTTP/3 error, consuming the
// body as quic-go does.
type failingQUIC struct{ err error }

func (f failingQUIC) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return nil, f.err
}

type countingTCP struct{ calls atomic.Int64 }

func (c *countingTCP) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}, Request: req}, nil
}

func TestUpgradeRouterNeverReplaysABodyItCannotRebuild(t *testing.T) {
	origin, _ := url.Parse("https://example.test")
	for _, failure := range []*h3transport.Error{
		{Certainty: h3transport.NotSent, Class: h3transport.ClassTimeout, Err: errors.New("handshake timed out")},
		{Certainty: h3transport.PossiblySent, Class: h3transport.ClassVersionFallback, Err: errors.New("H3_VERSION_FALLBACK")},
	} {
		tcp := &countingTCP{}
		cache := newAltSvcCache(time.Now)
		cache.observe(origin, []string{`h3=":443"`})
		router := &upgradeRoundTripper{tcp: tcp, http11: tcp, h3: failingQUIC{err: failure}, cache: cache, brokenTTL: time.Minute}
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			req, _ := http.NewRequest(method, origin.String()+"/write", io.NopCloser(strings.NewReader("streamed")))
			req.GetBody = nil
			if _, err := router.RoundTrip(req); err == nil {
				t.Fatalf("%s %s: a body without GetBody was replayed", failure.Certainty, method)
			}
			cache.broken = map[string]time.Time{}
		}
		if tcp.calls.Load() != 0 {
			t.Fatalf("%s: TCP ran %d times", failure.Certainty, tcp.calls.Load())
		}
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
