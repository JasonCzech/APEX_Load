package elastic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/netpolicy"
)

var ErrBudget = errors.New("search request budget exhausted")
var ErrUnreachable = errors.New("search destination unreachable")

type HTTPError struct {
	Status     int
	Kind       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("search %s (HTTP %d)", e.Kind, e.Status) }

type Client struct {
	cfg                Config
	credentials        Credentials
	http               *http.Client
	componentHTTP      map[string]*http.Client
	rate               *rateState
	mu                 sync.Mutex
	capabilities       Capabilities
	verificationPolicy VerificationPolicy
	pendingCleanup     []*Continuation
}

// VerificationPolicy is an executor-private ceiling for capability probes.
// Collector-local approvals cannot be expanded by a controller job or preview.
type VerificationPolicy struct {
	ESQL   bool
	Health bool
}

func (c *Client) SetVerificationPolicy(policy VerificationPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verificationPolicy = policy
	c.capabilities = applyVerificationPolicy(c.capabilities, policy)
}

func (c *Client) verificationPolicySnapshot() VerificationPolicy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.verificationPolicy
}

func applyVerificationPolicy(cap Capabilities, policy VerificationPolicy) Capabilities {
	if cap.Status == nil {
		cap.Status = map[string]string{}
	}
	if !policy.ESQL {
		cap.ESQL, cap.AsyncESQL = false, false
		cap.Status["esql"], cap.Status["async_esql"] = "locally_disabled", "locally_disabled"
	}
	if !policy.Health {
		cap.Health, cap.KibanaHealth, cap.LogstashHealth = false, false, false
		cap.Status["health"], cap.Status["kibana"], cap.Status["logstash"] = "locally_disabled", "locally_disabled", "locally_disabled"
	}
	return cap
}

type rateState struct {
	mu       sync.Mutex
	next     time.Time
	inflight chan struct{}
}

var rateRegistry = struct {
	sync.Mutex
	states   map[[32]byte]*rateState
	overflow *rateState
}{states: map[[32]byte]*rateState{}, overflow: &rateState{inflight: make(chan struct{}, 1)}}

func QuotaKey(c Config) string {
	h := sha256.Sum256([]byte(c.Product + "\x00" + strings.TrimRight(c.Endpoint, "/")))
	return "elastic:" + hex.EncodeToString(h[:])
}
func sharedRate(c Config, creds Credentials) *rateState {
	h := sha256.Sum256([]byte(QuotaKey(c) + "\x00" + c.AuthType + "\x00" + creds.APIKey + "\x00" + creds.Username + "\x00" + creds.Password + "\x00" + creds.BearerToken + "\x00" + creds.ClientCertPEM))
	rateRegistry.Lock()
	defer rateRegistry.Unlock()
	if r := rateRegistry.states[h]; r != nil {
		return r
	}
	if len(rateRegistry.states) >= 256 {
		return rateRegistry.overflow
	}
	r := &rateState{inflight: make(chan struct{}, 2)}
	rateRegistry.states[h] = r
	return r
}

func validateCredentials(auth string, c ComponentCredentials) error {
	for _, s := range []string{c.APIKey, c.Username, c.Password, c.BearerToken} {
		if len(s) > 8192 || strings.ContainsAny(s, "\r\n\x00") {
			return errors.New("invalid search credential")
		}
	}
	if strings.Contains(c.Username, ":") || len(c.ClientCertPEM) > 65536 || len(c.ClientKeyPEM) > 65536 {
		return errors.New("invalid search credential")
	}
	if (c.ClientCertPEM == "") != (c.ClientKeyPEM == "") {
		return errors.New("client certificate and key must be supplied together")
	}
	switch auth {
	case AuthAPIKey:
		if c.APIKey == "" {
			return errors.New("API key required")
		}
	case AuthBasic:
		if c.Username == "" || c.Password == "" {
			return errors.New("Basic username and password required")
		}
	case AuthBearer:
		if c.BearerToken == "" {
			return errors.New("Bearer token required")
		}
	case AuthMTLS:
		if c.ClientCertPEM == "" {
			return errors.New("client certificate required")
		}
	default:
		return errors.New("unsupported search authentication")
	}
	return nil
}
func (c Credentials) primary(auth string) ComponentCredentials {
	return ComponentCredentials{AuthType: auth, APIKey: c.APIKey, Username: c.Username, Password: c.Password, BearerToken: c.BearerToken, ClientCertPEM: c.ClientCertPEM, ClientKeyPEM: c.ClientKeyPEM}
}
func NewClient(cfg Config, credentials Credentials) (*Client, error) {
	cfg = cfg.Defaults()
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	if e := validateCredentials(cfg.AuthType, credentials.primary(cfg.AuthType)); e != nil {
		return nil, e
	}
	h, e := policyHTTP(cfg, credentials.primary(cfg.AuthType))
	if e != nil {
		return nil, e
	}
	c := newClient(cfg, credentials, h)
	for name, cc := range map[string]ComponentCredentials{"kibana": credentials.Kibana, "logstash": credentials.Logstash} {
		component := cfg
		component.Endpoint = cfg.KibanaEndpoint
		component.CAPEM = cfg.KibanaCAPEM
		component.ApprovedCIDRs = cfg.KibanaApprovedCIDRs
		if name == "logstash" {
			component.Endpoint = cfg.LogstashEndpoint
			component.CAPEM = cfg.LogstashCAPEM
			component.ApprovedCIDRs = cfg.LogstashApprovedCIDRs
		}
		if component.Endpoint == "" {
			continue
		}
		if e := validateCredentials(cc.AuthType, cc); e != nil {
			continue
		}
		ch, e := policyHTTP(component, cc)
		if e != nil {
			continue
		}
		c.componentHTTP[name] = ch
	}
	return c, nil
}
func allowedAddress(a netip.Addr, prefixes []netip.Prefix) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a == netip.MustParseAddr("168.63.129.16") || a == netip.MustParseAddr("fd00:ec2::254") {
		return false
	}
	if netpolicy.IsPublicAddr(a) {
		return true
	}
	if !a.IsPrivate() {
		return false
	}
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
func policyHTTP(cfg Config, cc ComponentCredentials) (*http.Client, error) {
	pool, e := x509.SystemCertPool()
	if e != nil {
		pool = x509.NewCertPool()
	}
	if cfg.CAPEM != "" && !pool.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
		return nil, errors.New("custom CA does not contain a certificate")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	if cc.ClientCertPEM != "" {
		cert, e := tls.X509KeyPair([]byte(cc.ClientCertPEM), []byte(cc.ClientKeyPEM))
		if e != nil {
			return nil, errors.New("invalid client certificate/key pair")
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	ps := []netip.Prefix{}
	for _, p := range cfg.ApprovedCIDRs {
		x, _ := netip.ParsePrefix(p)
		ps = append(ps, x)
	}
	d := &net.Dialer{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: tc, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: time.Duration(cfg.TimeoutSeconds) * time.Second, MaxResponseHeaderBytes: 64 << 10}
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, ErrUnreachable
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, ErrUnreachable
		}
		for _, ip := range ips {
			if !allowedAddress(ip, ps) {
				return nil, errors.New("search destination outside approved network policy")
			}
		}
		for _, ip := range ips {
			conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, ErrUnreachable
	}
	return securedHTTP(&http.Client{Transport: tr}, cfg), nil
}
func securedHTTP(h *http.Client, cfg Config) *http.Client {
	copy := *h
	copy.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

// NewClientWithHTTPClient is a trusted embedding/test seam. Callers own egress
// policy; public configuration never selects this constructor.
func NewClientWithHTTPClient(cfg Config, credentials Credentials, h *http.Client) (*Client, error) {
	cfg = cfg.Defaults()
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	if e := validateCredentials(cfg.AuthType, credentials.primary(cfg.AuthType)); e != nil {
		return nil, e
	}
	if h == nil {
		return nil, errors.New("trusted HTTP client required")
	}
	c := newClient(cfg, credentials, h)
	if cfg.KibanaEndpoint != "" {
		c.componentHTTP["kibana"] = c.http
	}
	if cfg.LogstashEndpoint != "" {
		c.componentHTTP["logstash"] = c.http
	}
	return c, nil
}
func newClient(cfg Config, creds Credentials, h *http.Client) *Client {
	return &Client{cfg: cfg, credentials: creds, http: securedHTTP(h, cfg), rate: sharedRate(cfg, creds), componentHTTP: map[string]*http.Client{}, capabilities: Capabilities{Product: cfg.Product, Status: map[string]string{}}, verificationPolicy: VerificationPolicy{ESQL: true, Health: true}}
}
func (c *Client) Close() {
	c.http.CloseIdleConnections()
	for _, h := range c.componentHTTP {
		h.CloseIdleConnections()
	}
}

type budget struct{ remaining int }
type budgetKey struct{}

func (c *Client) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Value(budgetKey{}) != nil {
		return ctx, func() {}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.MaxPollSeconds)*time.Second)
	return context.WithValue(ctx, budgetKey{}, &budget{remaining: c.cfg.MaxRequestsPerPoll}), cancel
}
func (c *Client) wait(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := c.rate
	select {
	case r.inflight <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-r.inflight }
	r.mu.Lock()
	at := time.Now()
	if r.next.After(at) {
		at = r.next
	}
	if deadline, ok := ctx.Deadline(); ok && at.After(deadline) {
		r.mu.Unlock()
		release()
		return nil, context.DeadlineExceeded
	}
	r.next = at.Add(time.Minute / time.Duration(c.cfg.RequestsPerMinute))
	r.mu.Unlock()
	timer := time.NewTimer(max(time.Duration(0), time.Until(at)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	}
}
func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	return c.requestAt(ctx, "", method, path, body, out, false)
}
func (c *Client) requestAt(ctx context.Context, component, method, path string, body any, out any, cleanup bool) error {
	endpoint, h, cc := c.cfg.Endpoint, c.http, c.credentials.primary(c.cfg.AuthType)
	if component == "kibana" {
		endpoint = c.cfg.KibanaEndpoint
		cc = c.credentials.Kibana
		h = c.componentHTTP[component]
	} else if component == "logstash" {
		endpoint = c.cfg.LogstashEndpoint
		cc = c.credentials.Logstash
		h = c.componentHTTP[component]
	}
	if endpoint == "" || h == nil {
		return errors.New("component endpoint unavailable")
	}
	encoded := []byte(nil)
	if body != nil {
		var e error
		encoded, e = json.Marshal(body)
		if e != nil || len(encoded) > 128<<10 {
			return errors.New("search request exceeds bounds")
		}
	}
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		if !cleanup {
			if b, _ := ctx.Value(budgetKey{}).(*budget); b != nil {
				if b.remaining <= 0 {
					return ErrBudget
				}
				b.remaining--
			}
		}
		release, e := c.wait(ctx)
		if e != nil {
			return e
		}
		req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(encoded))
		if e != nil {
			release()
			return errors.New("invalid search request")
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Opaque-Id", "apexload-observability")
		switch cc.AuthType {
		case AuthAPIKey:
			req.Header.Set("Authorization", "ApiKey "+cc.APIKey)
		case AuthBasic:
			req.SetBasicAuth(cc.Username, cc.Password)
		case AuthBearer:
			req.Header.Set("Authorization", "Bearer "+cc.BearerToken)
		}
		pitAttempt, _ := ctx.Value(pitSearchAttemptKey{}).(*pitSearchAttempt)
		if pitAttempt != nil {
			pitAttempt.dispatched = true
		}
		resp, e := h.Do(req)
		if e != nil {
			release()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrUnreachable
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(c.cfg.MaxResponseBytes)+1))
		resp.Body.Close()
		release()
		if pitAttempt != nil && len(data) <= c.cfg.MaxResponseBytes {
			// Decode only private ownership before status/page validation. A
			// complete bounded body may still accompany a transport read error.
			pitAttempt.observeResponse(data)
		}
		if readErr != nil {
			return errors.New("search response read failed")
		}
		if len(data) > c.cfg.MaxResponseBytes {
			return errors.New("search response exceeds byte budget")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			kind := "upstream_error"
			switch resp.StatusCode {
			case 401:
				kind = "unauthorized"
			case 403:
				kind = "forbidden"
			case 404:
				kind = "unsupported_or_missing"
			case 429:
				kind = "rate_limited"
			}
			retry := parseRetryAfter(resp.Header.Get("Retry-After"))
			he := &HTTPError{Status: resp.StatusCode, Kind: kind, RetryAfter: retry}
			if !cleanup && attempt < c.cfg.MaxRetries && (resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504) {
				delay := max(100*time.Millisecond, retry)
				if delay > 5*time.Second {
					delay = 5 * time.Second
				}
				t := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					t.Stop()
					return ctx.Err()
				case <-t.C:
					continue
				}
			}
			return he
		}
		if out == nil {
			return nil
		}
		if typ := resp.Header.Get("Content-Type"); typ != "" {
			media, _, e := mime.ParseMediaType(typ)
			if e != nil || media != "application/json" && !strings.HasSuffix(media, "+json") {
				return errors.New("search response is not JSON")
			}
		}
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		if e := d.Decode(out); e != nil {
			return errors.New("invalid search JSON response")
		}
		if e := d.Decode(new(any)); e != io.EOF {
			return errors.New("multiple search response documents rejected")
		}
		return nil
	}
	return errors.New("search retry budget exhausted")
}
func parseRetryAfter(v string) time.Duration {
	if n, e := strconv.Atoi(v); e == nil && n >= 0 {
		return time.Duration(min(n, 300)) * time.Second
	}
	if at, e := http.ParseTime(v); e == nil {
		return max(time.Duration(0), min(5*time.Minute, time.Until(at)))
	}
	return 0
}

// PendingCleanupContinuations returns privately owned failed probe handles.
// Only an acknowledged close or a known elapsed upstream expiry releases them.
func (c *Client) PendingCleanupContinuations() []*Continuation {
	c.mu.Lock()
	defer c.mu.Unlock()
	retained := c.pendingCleanup[:0]
	out := []*Continuation{}
	for _, owned := range c.pendingCleanup {
		if !owned.KeepAliveUntil.IsZero() && !owned.KeepAliveUntil.After(time.Now().UTC()) {
			continue
		}
		retained = append(retained, owned)
		copy := *owned
		out = append(out, &copy)
	}
	c.pendingCleanup = retained
	return out
}
