package http

import (
	"errors"
	"io"
	"net"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/http/internal/h3transport"
)

// maxAltSvcOrigins bounds the per-VUser Alt-Svc and broken-origin caches.
const maxAltSvcOrigins = 256

// altSvcCache remembers, per origin, the same-host final HTTP/3 alternative
// the origin advertised, and which origins recently failed over QUIC. It is
// per VUser, never shared.
type altSvcCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]altSvcAlternative
	broken  map[string]time.Time
}

type altSvcAlternative struct {
	port    int
	expires time.Time
}

func newAltSvcCache(now func() time.Time) *altSvcCache {
	return &altSvcCache{now: now, entries: map[string]altSvcAlternative{}, broken: map[string]time.Time{}}
}

// originKey normalizes an https URL to scheme://host:port; other schemes
// have no HTTP/3 origin.
func originKey(u *url.URL) (key, host string, port int, ok bool) {
	if u == nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return "", "", 0, false
	}
	host = strings.ToLower(u.Hostname())
	port = 443
	if raw := u.Port(); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return "", "", 0, false
		}
		port = parsed
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(port)), host, port, true
}

// observe applies one response's Alt-Svc field to the origin's entry:
// "clear" removes it, a same-host final h3 alternative replaces it, and an
// advertisement for another host is ignored (cross-host alternatives are an
// extension). A field without h3 leaves the entry alone.
func (c *altSvcCache) observe(u *url.URL, values []string) {
	key, host, _, ok := originKey(u)
	if !ok || len(values) == 0 {
		return
	}
	parsed := ParseAltSvc(values)
	c.mu.Lock()
	defer c.mu.Unlock()
	if parsed.Clear {
		delete(c.entries, key)
		return
	}
	for _, entry := range parsed.FinalH3() {
		if entry.Host != "" && !strings.EqualFold(entry.Host, host) {
			continue
		}
		if entry.MaxAge <= 0 {
			delete(c.entries, key)
			return
		}
		if _, known := c.entries[key]; !known && len(c.entries) >= maxAltSvcOrigins {
			c.evictLocked(c.entries)
		}
		c.entries[key] = altSvcAlternative{port: entry.Port, expires: c.now().Add(entry.MaxAge)}
		return
	}
}

// evictLocked drops expired entries, then one arbitrary entry if still full.
func (c *altSvcCache) evictLocked(entries map[string]altSvcAlternative) {
	now := c.now()
	for key, entry := range entries {
		if !now.Before(entry.expires) {
			delete(entries, key)
		}
	}
	for key := range entries {
		if len(entries) < maxAltSvcOrigins {
			return
		}
		delete(entries, key)
	}
}

// alternative returns the origin's live alternative port, or why there is
// none: "not_advertised" or "broken_cache".
func (c *altSvcCache) alternative(key string) (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if until, broken := c.broken[key]; broken {
		if now.Before(until) {
			return 0, "broken_cache"
		}
		delete(c.broken, key)
	}
	entry, ok := c.entries[key]
	if !ok {
		return 0, "not_advertised"
	}
	if !now.Before(entry.expires) {
		delete(c.entries, key)
		return 0, "not_advertised"
	}
	return entry.port, ""
}

// markBroken keeps the origin on TCP for ttl after a QUIC connection failure.
// Only transport failures are recorded, never cancellations or HTTP errors.
func (c *altSvcCache) markBroken(key string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, known := c.broken[key]; !known && len(c.broken) >= maxAltSvcOrigins {
		now := c.now()
		for origin, until := range c.broken {
			if !now.Before(until) || len(c.broken) >= maxAltSvcOrigins {
				delete(c.broken, origin)
			}
		}
	}
	c.broken[key] = c.now().Add(ttl)
}

func (c *altSvcCache) drop(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// resetAlternatives forgets advertised alternatives (alt_svc_cache:
// per_iteration). Broken origins keep their own TTL.
func (c *altSvcCache) resetAlternatives() {
	c.mu.Lock()
	c.entries = map[string]altSvcAlternative{}
	c.mu.Unlock()
}

// http3RouteObserver receives the router's decisions for the receipt.
type http3RouteObserver interface {
	routed(selection string, quic bool)
	fellBack(kind string)
}

type http3RouteObserverKey struct{}

// upgradeRoundTripper routes auto+h3 requests: TCP until the origin
// advertises a same-host HTTP/3 alternative, then QUIC to it. Fallback to
// TCP happens only when the request cannot have reached the target.
type upgradeRoundTripper struct {
	tcp       stdhttp.RoundTripper // automatic HTTP/2-or-HTTP/1.1 pool
	http11    stdhttp.RoundTripper // exact HTTP/1.1 pool, for H3_VERSION_FALLBACK
	h3        stdhttp.RoundTripper
	cache     *altSvcCache
	route     http3RouteRoundTripper // proxy and PAC route decision
	tlsBelow  bool                   // the session's TLS maximum is below 1.3
	brokenTTL time.Duration
}

func (rt *upgradeRoundTripper) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	observer, _ := req.Context().Value(http3RouteObserverKey{}).(http3RouteObserver)
	key, host, _, httpsOrigin := originKey(req.URL)
	selection := ""
	switch {
	case !httpsOrigin:
		selection = "not_advertised"
	case req.Context().Value(integratedAuthKey{}) != nil:
		selection = "integrated_auth"
	case rt.tlsBelow:
		selection = "tls_policy"
	default:
		if err := rt.route.directRoute(req); errors.Is(err, ErrHTTP3ProxyRoute) {
			selection = "proxy_route"
		} else if err != nil {
			// A PAC or target-policy failure: no route exists on either
			// transport, so nothing is sent.
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, err
		}
	}
	port := 0
	if selection == "" {
		port, selection = rt.cache.alternative(key)
	}
	if selection != "" {
		if observer != nil {
			observer.routed(selection, false)
		}
		return rt.tcpRoundTrip(req, key)
	}
	if observer != nil {
		observer.routed("upgraded", true)
	}
	attempt := req.Clone(h3transport.WithDialPort(req.Context(), port))
	attempt.Header.Set("Alt-Used", net.JoinHostPort(host, strconv.Itoa(port)))
	resp, err := rt.h3.RoundTrip(attempt)
	if err == nil {
		if resp.StatusCode == stdhttp.StatusMisdirectedRequest {
			// The alternative cannot serve this origin (RFC 9110 §15.5.20:
			// not processed, retryable on another connection). Forget it,
			// keep the origin on TCP for broken_ttl_ms so the TCP response's
			// own Alt-Svc does not send the next request straight back, and
			// run this request on TCP.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			rt.cache.drop(key)
			rt.cache.markBroken(key, rt.brokenTTL)
			return rt.fallback(req, key, "misdirected_request", rt.tcp, observer)
		}
		rt.cache.observe(req.URL, resp.Header.Values("Alt-Svc"))
		return resp, nil
	}
	if req.Context().Err() != nil {
		return nil, err // the caller's own cancellation: no fallback
	}
	var h3Err *h3transport.Error
	if !errors.As(err, &h3Err) {
		return nil, err
	}
	switch {
	case h3Err.Class == h3transport.ClassVersionFallback &&
		(h3Err.Certainty == h3transport.NotSent || idempotentReplayable(req)):
		return rt.fallback(req, key, "h3_version_fallback", rt.http11, observer)
	case h3Err.Certainty == h3transport.NotSent:
		// The QUIC path failed before the request was sent: keep the origin
		// on TCP for broken_ttl_ms and run this request once on TCP.
		rt.cache.markBroken(key, rt.brokenTTL)
		return rt.fallback(req, key, "quic_connect_failed", rt.tcp, observer)
	}
	// possibly_sent and rejected_unprocessed fail: the target may have
	// processed the request, or quic-go already used its single retry.
	return nil, err
}

func (rt *upgradeRoundTripper) tcpRoundTrip(req *stdhttp.Request, key string) (*stdhttp.Response, error) {
	resp, err := rt.tcp.RoundTrip(req)
	if err == nil && key != "" {
		rt.cache.observe(req.URL, resp.Header.Values("Alt-Svc"))
	}
	return resp, err
}

// fallback replays a request that never reached the target over TCP, once.
// It needs a body it can rebuild; quic-go has already closed the original.
func (rt *upgradeRoundTripper) fallback(req *stdhttp.Request, key, kind string, transport stdhttp.RoundTripper, observer http3RouteObserver) (*stdhttp.Response, error) {
	retry := req.Clone(req.Context())
	if req.Body != nil && req.Body != stdhttp.NoBody {
		if req.GetBody == nil {
			return nil, errors.New("HTTP/3 fallback needs a request body that can be sent again")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	if observer != nil {
		observer.fellBack(kind)
	}
	resp, err := transport.RoundTrip(retry)
	if err == nil && key != "" {
		rt.cache.observe(req.URL, resp.Header.Values("Alt-Svc"))
	}
	return resp, err
}

// idempotentReplayable reports whether a request may run again after the
// target might have seen it: an idempotent method with a rebuildable body
// (RFC 9110 §9.2.2).
func idempotentReplayable(req *stdhttp.Request) bool {
	switch req.Method {
	case stdhttp.MethodGet, stdhttp.MethodHead, stdhttp.MethodOptions, stdhttp.MethodPut, stdhttp.MethodDelete, stdhttp.MethodTrace:
	default:
		return false
	}
	return req.Body == nil || req.Body == stdhttp.NoBody || req.GetBody != nil
}
