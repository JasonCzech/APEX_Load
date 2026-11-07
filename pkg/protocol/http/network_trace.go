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
}

func newHTTPTraceCollector() *httpTraceCollector {
	return &httpTraceCollector{}
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
	c.trace.ConnectionReused = info.Reused
	c.trace.WasIdle = info.WasIdle
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

func (c *httpTraceCollector) observeRedirect(req *http.Request, via []*http.Request, jar http.CookieJar) {
	if c == nil || req == nil || req.URL == nil || len(via) == 0 {
		return
	}
	prev := via[len(via)-1]
	if prev == nil || prev.URL == nil || sameRefreshOrigin(prev.URL, req.URL) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trace.CrossOriginRedirect = true
	for _, headerName := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Www-Authenticate"} {
		hadHeader := requestHeaderKeyPresent(prev.Header, headerName) || requestHeaderKeyPresent(via[0].Header, headerName)
		if !hadHeader && headerName == "Cookie" && jar != nil && prev.URL != nil && len(jar.Cookies(prev.URL)) > 0 {
			hadHeader = true
		}
		if hadHeader && !requestHeaderKeyPresent(req.Header, headerName) {
			c.trace.StrippedRedirectHeaders = appendUniqueString(c.trace.StrippedRedirectHeaders, headerName)
		}
	}
}

func (c *httpTraceCollector) observeExchange(
	req *http.Request,
	resp *http.Response,
	jar http.CookieJar,
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
	}
	if requestCompression != "" && !strings.EqualFold(requestCompression, "none") {
		c.trace.RequestCompression = requestCompression
	} else if effectiveReq != nil {
		if ce := strings.TrimSpace(effectiveReq.Header.Get("Content-Encoding")); ce != "" && !strings.EqualFold(ce, "identity") {
			c.trace.RequestCompression = ce
		}
	}

	if effectiveReq != nil {
		sentCount, sentNames := extractCookieNamesSent(effectiveReq, jar, resp != nil)
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

func extractCookieNamesSent(req *http.Request, jar http.CookieJar, jarAlreadyMerged bool) (int, []string) {
	if req == nil {
		return 0, nil
	}
	cookies := req.Cookies()
	if !jarAlreadyMerged && jar != nil && req.URL != nil {
		for _, jc := range jar.Cookies(req.URL) {
			if jc != nil {
				cookies = append(cookies, jc)
			}
		}
	}
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
