package http

import (
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	maxTraceDNSResolvedIPs  = 8
	maxTraceCookieNames     = 32
	maxTraceCookieNameBytes = 128
	maxTraceCertFieldBytes  = 256
)

type httpTraceCollector struct {
	mu           sync.Mutex
	trace        protocol.HTTPNetworkTrace
	wait100Start time.Time
	// lastHop is the most recent redirect request. net/http adds jar cookies
	// to it in place when it sends the hop, so after Client.Do returns it
	// holds the Cookie header that hop actually carried.
	lastHop *http.Request
	// lastHopRoot is via[0] of the redirect chain lastHop belongs to: the
	// exact request passed to Client.Do. net/http forks requests (deadline,
	// wrapping transports), so Response.Request chains never lead back to it.
	lastHopRoot *http.Request
	// hopWroteHeaders records whether the current hop's request headers
	// reached a connection; a hop that never wrote them sent no cookies.
	hopWroteHeaders bool
}

func newHTTPTraceCollector() *httpTraceCollector {
	return &httpTraceCollector{}
}

// onGetConn marks a hop (or a transport retry) boundary: the aggregate trace
// describes the connection of the final attempt, so per-connection fields
// observed for an earlier hop must not leak into it.
func (c *httpTraceCollector) onGetConn() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.resetHopLocked()
	c.mu.Unlock()
}

// resetHopLocked clears per-connection fields. Request-level receipts
// (CrossOriginRedirect, StrippedRedirectHeaders, RequestCompression) and the
// response-derived fields observeExchange sets at the end are kept.
func (c *httpTraceCollector) resetHopLocked() {
	c.trace.DNSResolvedIPs = nil
	c.trace.DNSCoalesced = false
	c.trace.RemoteAddr = ""
	c.trace.LocalAddr = ""
	c.trace.ConnectionReused = nil
	c.trace.WasIdle = false
	c.trace.IdleTimeMs = 0
	c.trace.Got100Continue = false
	c.trace.Wait100ContinueMs = 0
	c.wait100Start = time.Time{}
	c.trace.TLSVersion = ""
	c.trace.TLSCipherSuite = ""
	c.trace.TLSNegotiatedProto = ""
	c.trace.TLSServerName = ""
	c.trace.TLSDidResume = false
	c.trace.TLSCertSubject = ""
	c.trace.TLSCertIssuer = ""
	c.trace.TLSCertExpiry = ""
	c.hopWroteHeaders = false
}

func (c *httpTraceCollector) onWroteHeaders() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.hopWroteHeaders = true
	c.mu.Unlock()
}

func (c *httpTraceCollector) onDNSDone(info httptrace.DNSDoneInfo) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trace.DNSCoalesced = info.Coalesced
	if len(info.Addrs) > 0 {
		ips := make([]string, 0, min(len(info.Addrs), maxTraceDNSResolvedIPs))
		for i := 0; i < len(info.Addrs) && len(ips) < maxTraceDNSResolvedIPs; i++ {
			if ip := info.Addrs[i].IP.String(); ip != "" {
				ips = append(ips, ip)
			}
		}
		if len(ips) > 0 {
			c.trace.DNSResolvedIPs = ips
		}
	}
}

func (c *httpTraceCollector) onConnectDone(_, addr string, err error) {
	if c == nil || err != nil || addr == "" {
		return
	}
	c.mu.Lock()
	if c.trace.RemoteAddr == "" {
		c.trace.RemoteAddr = addr
	}
	c.mu.Unlock()
}

func (c *httpTraceCollector) onTLSHandshakeDone(cs tls.ConnectionState, err error) {
	if c == nil || err != nil {
		return
	}
	c.mu.Lock()
	applyTLSConnectionState(&c.trace, cs)
	c.mu.Unlock()
}

func (c *httpTraceCollector) onGotConn(info httptrace.GotConnInfo) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	reused := info.Reused
	c.trace.ConnectionReused = &reused
	c.trace.WasIdle = info.WasIdle
	c.trace.IdleTimeMs = 0
	if info.WasIdle && info.IdleTime > 0 {
		c.trace.IdleTimeMs = float64(info.IdleTime) / float64(time.Millisecond)
	}
	if info.Conn != nil {
		if ra := info.Conn.RemoteAddr(); ra != nil {
			c.trace.RemoteAddr = ra.String()
		}
		if la := info.Conn.LocalAddr(); la != nil {
			c.trace.LocalAddr = la.String()
		}
	}
}

func (c *httpTraceCollector) onWait100Continue() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.wait100Start = time.Now()
	c.mu.Unlock()
}

func (c *httpTraceCollector) onGot100Continue() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.trace.Got100Continue = true
	if !c.wait100Start.IsZero() {
		c.trace.Wait100ContinueMs = float64(time.Since(c.wait100Start)) / float64(time.Millisecond)
	}
	c.mu.Unlock()
}

func (c *httpTraceCollector) onWait100ContinueDone() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.wait100Start.IsZero() && c.trace.Wait100ContinueMs == 0 {
		c.trace.Wait100ContinueMs = float64(time.Since(c.wait100Start)) / float64(time.Millisecond)
	}
	c.mu.Unlock()
}

// observeRedirect runs inside Client.CheckRedirect, before net/http sends
// req. At that point req.Header holds only the headers the redirect copier
// kept, while each earlier request in via already carries the jar cookies
// net/http added when it sent that hop. The jar already holds any cookies the
// 3xx response set.
func (c *httpTraceCollector) observeRedirect(req *http.Request, via []*http.Request, jar http.CookieJar) {
	if c == nil || req == nil || req.URL == nil || len(via) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A new hop begins: nothing about its connection is known yet.
	c.lastHop = req
	c.lastHopRoot = via[0]
	c.resetHopLocked()
	prev := via[len(via)-1]
	if prev == nil || prev.URL == nil || sameRefreshOrigin(prev.URL, req.URL) {
		return
	}
	c.trace.CrossOriginRedirect = true
	for _, headerName := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Www-Authenticate"} {
		stripped := false
		if headerName == "Cookie" {
			stripped = redirectDroppedCookies(prev, req, jar)
		} else {
			// The copier copies from the initial request, so presence in
			// req.Header is authoritative for these headers.
			hadHeader := requestHeaderKeyPresent(prev.Header, headerName) || requestHeaderKeyPresent(via[0].Header, headerName)
			stripped = hadHeader && !requestHeaderKeyPresent(req.Header, headerName)
		}
		if stripped {
			c.trace.StrippedRedirectHeaders = appendUniqueString(c.trace.StrippedRedirectHeaders, headerName)
		}
	}
}

// redirectDroppedCookies reports whether a cookie the previous hop sent will
// not be sent to the redirect destination, either as a copied Cookie header
// or from the jar. Cookies are compared by name only.
func redirectDroppedCookies(prev, req *http.Request, jar http.CookieJar) bool {
	sent := prev.Cookies()
	if len(sent) == 0 {
		return false
	}
	destination := make(map[string]struct{})
	for _, ck := range req.Cookies() {
		destination[ck.Name] = struct{}{}
	}
	if jar != nil {
		// Mirror net/http's Client.send, which consults the jar with the
		// request Host when one is set.
		cookieURL := req.URL
		if req.Host != "" {
			clone := *req.URL
			clone.Host = req.Host
			cookieURL = &clone
		}
		for _, ck := range jar.Cookies(cookieURL) {
			if ck != nil {
				destination[ck.Name] = struct{}{}
			}
		}
	}
	for _, ck := range sent {
		if _, ok := destination[ck.Name]; !ok {
			return true
		}
	}
	return false
}

// observeExchange records request- and response-derived fields once the
// exchange finished. net/http has already merged the jar cookies into the
// Cookie header of every request it sent, so the request's own Cookie header
// is exactly what went out.
func (c *httpTraceCollector) observeExchange(
	req *http.Request,
	resp *http.Response,
	requestCompression string,
	encodedBytes int64,
	decodedBytes int64,
) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	effectiveReq := req
	if resp != nil && resp.Request != nil {
		effectiveReq = resp.Request
	} else if resp == nil && c.lastHop != nil && c.lastHopRoot == req {
		// A transport failure on a later redirect hop of req: describe that
		// hop rather than the first one.
		effectiveReq = c.lastHop
	}
	if requestCompression != "" && !strings.EqualFold(requestCompression, "none") {
		c.trace.RequestCompression = requestCompression
	} else if effectiveReq != nil {
		if ce := strings.TrimSpace(effectiveReq.Header.Get("Content-Encoding")); ce != "" && !strings.EqualFold(ce, "identity") {
			c.trace.RequestCompression = ce
		}
	}

	c.trace.CookiesSentCount = 0
	c.trace.CookieNamesSent = nil
	// Without a response, cookies count as sent only when the failing hop
	// wrote its headers; a refused dial sent none.
	if effectiveReq != nil && (resp != nil || c.hopWroteHeaders) {
		sentCount, sentNames := extractCookieNamesSent(effectiveReq)
		c.trace.CookiesSentCount = sentCount
		c.trace.CookieNamesSent = sentNames
	}

	if resp != nil {
		if resp.Proto != "" {
			c.trace.HTTPProtocol = resp.Proto
		}
		if resp.TLS != nil {
			applyTLSConnectionState(&c.trace, *resp.TLS)
		}
		if ce := strings.TrimSpace(resp.Header.Get("Content-Encoding")); ce != "" {
			c.trace.ContentEncoding = ce
		}
		if len(resp.TransferEncoding) > 0 {
			c.trace.TransferEncoding = append([]string(nil), resp.TransferEncoding...)
		}
		recvCount, recvNames := extractSetCookieNamesReceived(resp.Header)
		c.trace.SetCookiesReceivedCount = recvCount
		c.trace.SetCookieNamesReceived = recvNames
	}

	if encodedBytes > 0 {
		c.trace.EncodedBodyBytes = encodedBytes
	}
	if decodedBytes > 0 {
		c.trace.DecodedBodyBytes = decodedBytes
	}
}

func (c *httpTraceCollector) setBodyBytes(encodedBytes, decodedBytes int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if encodedBytes > 0 {
		c.trace.EncodedBodyBytes = encodedBytes
	}
	if decodedBytes > 0 {
		c.trace.DecodedBodyBytes = decodedBytes
	}
	c.mu.Unlock()
}

func (c *httpTraceCollector) snapshot() *protocol.HTTPNetworkTrace {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.trace.Clone()
}

func applyTLSConnectionState(target *protocol.HTTPNetworkTrace, cs tls.ConnectionState) {
	if target == nil {
		return
	}
	if v := formatTLSVersion(cs.Version); v != "" {
		target.TLSVersion = v
	}
	if cs.CipherSuite != 0 {
		target.TLSCipherSuite = tls.CipherSuiteName(cs.CipherSuite)
	}
	if cs.NegotiatedProtocol != "" {
		target.TLSNegotiatedProto = cs.NegotiatedProtocol
	}
	if cs.ServerName != "" {
		target.TLSServerName = boundedtext.TruncateUTF8(cs.ServerName, maxTraceCertFieldBytes)
	}
	if cs.DidResume {
		target.TLSDidResume = true
	}
	if len(cs.PeerCertificates) > 0 && cs.PeerCertificates[0] != nil {
		leaf := cs.PeerCertificates[0]
		if subj := strings.TrimSpace(leaf.Subject.String()); subj != "" {
			target.TLSCertSubject = boundedtext.TruncateUTF8(subj, maxTraceCertFieldBytes)
		}
		if issuer := strings.TrimSpace(leaf.Issuer.String()); issuer != "" {
			target.TLSCertIssuer = boundedtext.TruncateUTF8(issuer, maxTraceCertFieldBytes)
		}
		if !leaf.NotAfter.IsZero() {
			target.TLSCertExpiry = leaf.NotAfter.UTC().Format(time.RFC3339)
		}
	}
}

func formatTLSVersion(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		if v != 0 {
			return tls.VersionName(v)
		}
		return ""
	}
}

func extractCookieNamesSent(req *http.Request) (int, []string) {
	if req == nil {
		return 0, nil
	}
	cookies := req.Cookies()
	if len(cookies) == 0 {
		return 0, nil
	}
	names := make([]string, 0, min(len(cookies), maxTraceCookieNames))
	for _, ck := range cookies {
		if ck == nil {
			continue
		}
		name := boundedtext.TruncateUTF8(strings.TrimSpace(ck.Name), maxTraceCookieNameBytes)
		if name != "" && len(names) < maxTraceCookieNames {
			names = appendUniqueString(names, name)
		}
	}
	return len(cookies), names
}

func extractSetCookieNamesReceived(h http.Header) (int, []string) {
	if len(h) == 0 {
		return 0, nil
	}
	rawValues := h.Values("Set-Cookie")
	if len(rawValues) == 0 {
		return 0, nil
	}
	parsed := (&http.Response{Header: h}).Cookies()
	names := make([]string, 0, min(len(rawValues), maxTraceCookieNames))
	for _, ck := range parsed {
		if ck == nil {
			continue
		}
		name := boundedtext.TruncateUTF8(strings.TrimSpace(ck.Name), maxTraceCookieNameBytes)
		if name != "" && len(names) < maxTraceCookieNames {
			names = appendUniqueString(names, name)
		}
	}
	if len(names) == 0 {
		for _, raw := range rawValues {
			semi := strings.SplitN(raw, ";", 2)[0]
			if eq := strings.IndexByte(semi, '='); eq > 0 {
				name := boundedtext.TruncateUTF8(strings.TrimSpace(semi[:eq]), maxTraceCookieNameBytes)
				if name != "" && len(names) < maxTraceCookieNames {
					names = appendUniqueString(names, name)
				}
			}
		}
	}
	count := max(len(parsed), len(rawValues))
	return count, names
}

func appendUniqueString(slice []string, val string) []string {
	for _, existing := range slice {
		if existing == val {
			return slice
		}
	}
	return append(slice, val)
}
