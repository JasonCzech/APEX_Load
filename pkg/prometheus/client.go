package prometheus

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/jasonczech/apexload/pkg/netpolicy"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type Client struct {
	cfg         Config
	credentials Credentials
	http        *http.Client
	azure       azcore.TokenCredential
	authState   *tokenState
	rate        *rateState
}
type HTTPError struct {
	Status     int
	Kind       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Prometheus %s (HTTP %d)", e.Kind, e.Status) }

var ErrBudget = errors.New("Prometheus collection request budget exhausted")
var ErrUnreachable = errors.New("Prometheus destination is unreachable")
var ErrAuthentication = errors.New("Prometheus authentication failed")

type pollBudget struct{ remaining int }
type budgetKey struct{}

func (c *Client) boundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.MaxPollSeconds)*time.Second)
	if _, exists := ctx.Value(budgetKey{}).(*pollBudget); exists {
		return ctx, cancel
	}
	return context.WithValue(ctx, budgetKey{}, &pollBudget{c.cfg.MaxRequestsPerPoll}), cancel
}
func NewClient(cfg Config, cred Credentials) (*Client, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateCredentials(cfg, cred); err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if cfg.CAPEM != "" && !pool.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
		return nil, errors.New("custom CA does not contain a certificate")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	if cfg.MTLS {
		cert, err := tls.X509KeyPair([]byte(cred.ClientCertificate), []byte(cred.ClientKey))
		if err != nil {
			return nil, errors.New("invalid client certificate or private key")
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	prefixes := []netip.Prefix{}
	for _, v := range cfg.ApprovedCIDRs {
		p, _ := netip.ParsePrefix(v)
		prefixes = append(prefixes, p)
	}
	dialer := &net.Dialer{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, KeepAlive: 30 * time.Second}
	u, _ := url.Parse(cfg.Endpoint)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: time.Duration(cfg.TimeoutSeconds) * time.Second, MaxResponseHeaderBytes: 64 << 10}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrUnreachable
		}
		resolved, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(resolved) == 0 {
			return nil, ErrUnreachable
		}
		for _, a := range resolved {
			if !allowedAddress(a, prefixes, u.Scheme == "http") {
				return nil, errors.New("Prometheus destination is outside approved network policy")
			}
		}
		for _, a := range resolved {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, ErrUnreachable
	}
	c, err := newClient(cfg, cred, &http.Client{Transport: transport})
	if err != nil {
		return nil, err
	}
	c.rate = sharedRate(cfg, cred)
	if cfg.AuthType == AuthAzure {
		c.authState = sharedTokenState(cfg, cred)
	}
	return c, nil
}

// The trusted-transport seam is for deterministic fixtures. Production callers
// always use NewClient, whose DNS answers are validated and pinned at dial time.
func NewClientWithHTTPClient(cfg Config, cred Credentials, trusted *http.Client) (*Client, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateCredentials(cfg, cred); err != nil {
		return nil, err
	}
	if trusted == nil {
		return nil, errors.New("trusted HTTP client is required")
	}
	return newClient(cfg, cred, trusted)
}
func newClient(cfg Config, cred Credentials, h *http.Client) (*Client, error) {
	hc := *h
	hc.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	hc.Jar = nil
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Client{cfg: cfg, credentials: cred, http: &hc, rate: &rateState{inflight: make(chan struct{}, 1)}, authState: &tokenState{}}
	if cfg.AuthType == AuthAzure {
		var err error
		switch cfg.AzureCredentialMode {
		case "workload_identity":
			c.azure, err = azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{ClientOptions: azcore.ClientOptions{Cloud: cloud.AzurePublic}, ClientID: cfg.ClientID, TenantID: cfg.AzureTenantID})
		case "managed_identity":
			opts := &azidentity.ManagedIdentityCredentialOptions{}
			if cfg.ClientID != "" {
				opts.ID = azidentity.ClientID(cfg.ClientID)
			}
			c.azure, err = azidentity.NewManagedIdentityCredential(opts)
		case "service_principal":
			c.azure, err = azidentity.NewClientSecretCredential(cfg.AzureTenantID, cfg.ClientID, cred.ClientSecret, &azidentity.ClientSecretCredentialOptions{ClientOptions: azcore.ClientOptions{Cloud: cloud.AzurePublic}})
		}
		if err != nil {
			return nil, errors.New("Azure credential initialization failed")
		}
	}
	return c, nil
}
func validateCredentials(cfg Config, c Credentials) error {
	if strings.HasPrefix(cfg.Endpoint, "http://") && (c.Token != "" || c.Username != "" || c.Password != "" || c.ClientSecret != "" || c.ClientCertificate != "" || c.ClientKey != "") {
		return errors.New("private HTTP cannot transmit credential material")
	}
	for _, v := range []string{c.Token, c.Username, c.Password, c.ClientSecret} {
		if len(v) > 16384 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid Prometheus credential")
		}
	}
	if len(c.ClientCertificate) > 65536 || len(c.ClientKey) > 65536 {
		return errors.New("client certificate material exceeds bounds")
	}
	if cfg.AuthType == AuthBasic && (c.Username == "" || c.Password == "") {
		return errors.New("Basic username and password are required")
	}
	if cfg.AuthType == AuthBearer && c.Token == "" {
		return errors.New("bearer token is required")
	}
	if cfg.AuthType == AuthAzure && cfg.AzureCredentialMode == "service_principal" && c.ClientSecret == "" {
		return errors.New("Azure service principal secret is required")
	}
	if cfg.MTLS && (c.ClientCertificate == "" || c.ClientKey == "") {
		return errors.New("mTLS certificate and key are required")
	}
	return nil
}
func allowedAddress(addr netip.Addr, prefixes []netip.Prefix, privateOnly bool) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || addr == netip.MustParseAddr("fd00:ec2::254") || addr == netip.MustParseAddr("168.63.129.16") {
		return false
	}
	if !privateOnly && netpolicy.IsPublicAddr(addr) {
		return true
	}
	if !addr.IsPrivate() {
		return false
	}
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

type rateState struct {
	mu        sync.Mutex
	next      time.Time
	throttles int
	inflight  chan struct{}
	skipWait  bool // Trusted fixture seam only; production constructors leave false.
}

var rates = struct {
	sync.Mutex
	entries  map[string]*rateState
	overflow *rateState
}{entries: map[string]*rateState{}, overflow: &rateState{inflight: make(chan struct{}, 1)}}

// QuotaKey scopes quotas to endpoint and credential identity without publishing
// credential material. Executors additionally coordinate through durable leases.
func QuotaKey(cfg Config, cred Credentials) string {
	cfg = cfg.Defaults()
	u, _ := url.Parse(cfg.Endpoint)
	if u != nil {
		port := u.Port()
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		u.Host = host
		if strings.Contains(host, ":") {
			u.Host = "[" + host + "]"
		}
		if port != "" && !(u.Scheme == "https" && port == "443") && !(u.Scheme == "http" && port == "80") {
			u.Host = net.JoinHostPort(host, port)
		}
		u.Path = ""
	}
	target := cfg.Endpoint
	if u != nil {
		target = u.String()
	}
	sum := sha256.Sum256([]byte(target + cfg.ContextPath + "\x00" + cfg.AuthType + "\x00" + cfg.ClientID + "\x00" + cfg.AzureTenantID + "\x00" + cfg.AzureCredentialMode + "\x00" + cred.Username + "\x00" + cred.Token))
	return fmt.Sprintf("%x", sum)
}
func sharedRate(cfg Config, cred Credentials) *rateState {
	k := QuotaKey(cfg, cred)
	rates.Lock()
	defer rates.Unlock()
	if r := rates.entries[k]; r != nil {
		return r
	}
	if len(rates.entries) >= 256 {
		return rates.overflow
	}
	r := &rateState{inflight: make(chan struct{}, 1)}
	rates.entries[k] = r
	return r
}

// Retry-After is advisory and bounded by the complete poll budget. A server
// cannot keep a source asleep indefinitely with an excessive or malformed value.
func retryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" || len(header) > 128 {
		return 0
	}
	digits := true
	for _, ch := range header {
		if ch < '0' || ch > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(header, 10, 64)
		if err != nil || seconds > 30 {
			return 30 * time.Second
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil && at.After(now) {
		return min(at.Sub(now), 30*time.Second)
	}
	return 0
}

// All clients sharing an endpoint and credential identity observe the same
// throttle deadline. Query/sample-budget rejection does not enter this backoff.
func (r *rateState) deferResponse(status int, header string, now time.Time) time.Duration {
	retry := retryAfter(header, now)
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		return retry
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delay := retry
	if status == http.StatusTooManyRequests {
		r.throttles = min(r.throttles+1, 6)
		delay = max(delay, min(time.Second*time.Duration(1<<(r.throttles-1)), 30*time.Second))
	}
	if at := now.Add(delay); at.After(r.next) {
		r.next = at
	}
	return retry
}
func (r *rateState) resetThrottling() {
	r.mu.Lock()
	r.throttles = 0
	r.mu.Unlock()
}

func (c *Client) permit(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b, ok := ctx.Value(budgetKey{}).(*pollBudget); ok {
		if b.remaining <= 0 {
			return nil, ErrBudget
		}
		b.remaining--
	}
	select {
	case c.rate.inflight <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-c.rate.inflight }
	if c.rate.skipWait {
		return release, nil
	}
	c.rate.mu.Lock()
	at := time.Now()
	if c.rate.next.After(at) {
		at = c.rate.next
	}
	if deadline, ok := ctx.Deadline(); ok && at.After(deadline) {
		c.rate.mu.Unlock()
		release()
		return nil, context.DeadlineExceeded
	}
	c.rate.next = at.Add(time.Minute / time.Duration(c.cfg.RequestsPerMinute))
	c.rate.mu.Unlock()
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-timer.C:
		return release, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.authState.mu.Lock()
	defer c.authState.mu.Unlock()
	if c.authState.token.Token != "" && time.Now().Add(30*time.Second).Before(c.authState.token.ExpiresOn) {
		return c.authState.token.Token, nil
	}
	if c.azure == nil {
		return "", errors.New("Azure credential is unavailable")
	}
	token, err := c.azure.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{AzureScope}})
	if err != nil {
		return "", ErrAuthentication
	}
	if token.Token == "" || len(token.Token) > 16384 || strings.ContainsAny(token.Token, "\r\n\x00") || !token.ExpiresOn.After(time.Now()) {
		return "", errors.New("invalid Azure access token")
	}
	c.authState.token = token
	return token.Token, nil
}

type envelope struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType"`
	Error     string          `json:"error"`
	Warnings  []string        `json:"warnings"`
	Infos     []string        `json:"infos"`
}

func decodeJSON(body []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid Prometheus JSON response")
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return errors.New("invalid Prometheus JSON response")
	}
	return nil
}

// Only these read-only query APIs are reachable. Discovery cannot call a path
// supplied by the upstream server, and redirects never receive credentials.
func allowedAPI(path string) bool {
	switch path {
	case "/api/v1/query", "/api/v1/query_range", "/api/v1/series", "/api/v1/labels", "/api/v1/metadata", "/api/v1/targets", "/api/v1/alerts", "/api/v1/status/buildinfo":
		return true
	}
	return strings.HasPrefix(path, "/api/v1/label/") && strings.HasSuffix(path, "/values") && labelRE.MatchString(strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/label/"), "/values"))
}
func (c *Client) query(ctx context.Context, path string, params url.Values) (envelope, error) {
	var e envelope
	if !allowedAPI(path) {
		return e, errors.New("unsupported read-only Prometheus API")
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("timeout", strconv.Itoa(c.cfg.TimeoutSeconds)+"s")
	method := http.MethodGet
	var body io.Reader
	u := strings.TrimRight(c.cfg.Endpoint, "/") + c.cfg.ContextPath + path
	if path == "/api/v1/query" || path == "/api/v1/query_range" {
		method = http.MethodPost
		body = strings.NewReader(params.Encode())
	} else {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return e, errors.New("cannot construct Prometheus query")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	switch c.cfg.AuthType {
	case AuthBasic:
		req.SetBasicAuth(c.credentials.Username, c.credentials.Password)
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+c.credentials.Token)
	case AuthAzure:
		t, err := c.accessToken(ctx)
		if err != nil {
			return e, err
		}
		req.Header.Set("Authorization", "Bearer "+t)
	}
	release, err := c.permit(ctx)
	if err != nil {
		return e, err
	}
	defer release()
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return e, ctx.Err()
		}
		return e, ErrUnreachable
	}
	defer resp.Body.Close()
	// Honor throttling even if the upstream error body later fails validation.
	retry := c.rate.deferResponse(resp.StatusCode, resp.Header.Get("Retry-After"), time.Now())
	var reader io.Reader = io.LimitReader(resp.Body, int64(c.cfg.MaxResponseBytes)+1)
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") && !resp.Uncompressed {
		r, err := gzip.NewReader(reader)
		if err != nil {
			return e, errors.New("invalid compressed Prometheus response")
		}
		defer r.Close()
		reader = r
	} else if enc := resp.Header.Get("Content-Encoding"); enc != "" && enc != "identity" && !resp.Uncompressed {
		return e, errors.New("unsupported Prometheus content encoding")
	}
	data, err := io.ReadAll(io.LimitReader(reader, int64(c.cfg.MaxResponseBytes)+1))
	if err != nil {
		return e, errors.New("Prometheus response read failed")
	}
	if len(data) > c.cfg.MaxResponseBytes {
		return e, errors.New("Prometheus response exceeds decompressed byte limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := "upstream error"
		switch resp.StatusCode {
		case 400:
			kind = "invalid query"
		case 401:
			kind = "authentication failed"
			if c.cfg.AuthType == AuthAzure {
				c.authState.mu.Lock()
				if req.Header.Get("Authorization") == "Bearer "+c.authState.token.Token {
					c.authState.token = azcore.AccessToken{}
				}
				c.authState.mu.Unlock()
			}
		case 403:
			kind = "permission denied"
		case 404, 405, 501:
			kind = "capability unavailable"
		case 422:
			kind = "query execution rejected"
		case 429:
			kind = "rate limited"
		case 503:
			kind = "query timeout or unavailable"
		}
		low := strings.ToLower(string(data))
		if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity) && strings.Contains(low, "sample") && (strings.Contains(low, "limit") || strings.Contains(low, "maximum")) {
			kind = "sample budget exceeded"
		}
		return e, &HTTPError{resp.StatusCode, kind, retry}
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || (mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
		return e, errors.New("Prometheus response is not JSON")
	}
	if err := decodeJSON(data, &e); err != nil {
		return e, err
	}
	if e.Status != "success" {
		return envelope{}, errors.New("Prometheus query returned an error")
	}
	c.rate.resetThrottling()
	// A concurrent refresh may replace the cache while this response is in
	// flight. Always redact the exact bearer used by this request as well.
	usedBearer := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if usedBearer != "" && strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
		for i, value := range e.Warnings {
			e.Warnings[i] = strings.ReplaceAll(value, usedBearer, scrub.Redacted)
		}
		for i, value := range e.Infos {
			e.Infos[i] = strings.ReplaceAll(value, usedBearer, scrub.Redacted)
		}
	}
	e.Warnings = c.safeMessages(e.Warnings)
	e.Infos = c.safeMessages(e.Infos)
	return e, nil
}
func (c *Client) safeText(value string, limit int) string {
	c.authState.mu.Lock()
	runtimeToken := c.authState.token.Token
	c.authState.mu.Unlock()
	for _, secret := range []string{runtimeToken, c.credentials.Token, c.credentials.Username, c.credentials.Password, c.credentials.ClientSecret, c.credentials.ClientCertificate, c.credentials.ClientKey} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, scrub.Redacted)
		}
	}
	value = scrub.ValueLabel(value, scrub.Policy{})
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}
func (c *Client) safeMessages(values []string) []string {
	out := make([]string, 0, min(len(values), 20))
	for _, v := range values[:min(len(values), 20)] {
		out = append(out, c.safeText(v, 256))
	}
	return out
}
func statusFor(err error) string {
	if errors.Is(err, ErrAuthentication) {
		return "authentication_failed"
	}
	if err == nil {
		return "ok"
	}
	var e *HTTPError
	if errors.As(err, &e) {
		switch e.Status {
		case 401:
			return "authentication_failed"
		case 403:
			return "permission_denied"
		case 404, 405, 501:
			return "unsupported"
		case 429:
			return "throttled"
		}
	}
	if errors.Is(err, ErrBudget) {
		return "budget_exhausted"
	}
	return "unreachable"
}

// Tokens are shared only across the same approved origin, principal and secret
// version. Overflow uses isolated state rather than ever sharing another
// principal's token. Azure credentials are still explicit SDK implementations.
type tokenState struct {
	mu    sync.Mutex
	token azcore.AccessToken
}

var tokens = struct {
	sync.Mutex
	entries map[[32]byte]*tokenState
}{entries: map[[32]byte]*tokenState{}}

func sharedTokenState(cfg Config, cred Credentials) *tokenState {
	key := sha256.Sum256([]byte(QuotaKey(cfg, cred) + "\x00" + cred.ClientSecret))
	tokens.Lock()
	defer tokens.Unlock()
	if state := tokens.entries[key]; state != nil {
		return state
	}
	state := &tokenState{}
	if len(tokens.entries) < 256 {
		tokens.entries[key] = state
	}
	return state
}
