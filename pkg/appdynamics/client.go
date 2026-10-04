package appdynamics

import (
	"bytes"
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

	"github.com/jasonczech/apexload/pkg/netpolicy"
)

type Client struct {
	cfg                           Config
	credentials                   Credentials
	http                          *http.Client
	authState                     *tokenState
	controllerRate, analyticsRate *rateState
	capMu                         sync.Mutex
	capabilities                  Capabilities
}

// HTTPError contains a safe classification; upstream response bodies and URLs
// never become error messages or live payloads.
type HTTPError struct {
	Status     int
	Kind       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("AppDynamics %s (HTTP %d)", e.Kind, e.Status) }

var ErrBudget = errors.New("AppDynamics collection request budget exhausted")
var ErrUnreachable = errors.New("AppDynamics destination is unreachable")

type pollBudget struct {
	mu        sync.Mutex
	remaining int
}
type budgetKey struct{}

func (b *pollBudget) take() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return ErrBudget
	}
	b.remaining--
	return nil
}

func NewClient(cfg Config, credentials Credentials) (*Client, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateCredentials(cfg, credentials); err != nil {
		return nil, err
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if cfg.CAPEM != "" && !pool.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
		return nil, errors.New("custom CA does not contain a certificate")
	}
	prefixes := []netip.Prefix{}
	for _, cidr := range cfg.ApprovedCIDRs {
		p, _ := netip.ParsePrefix(cidr)
		prefixes = append(prefixes, p)
	}
	dialer := &net.Dialer{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: time.Duration(cfg.TimeoutSeconds) * time.Second, MaxResponseHeaderBytes: 64 << 10}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid destination")
		}
		resolved, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(resolved) == 0 {
			return nil, errors.New("AppDynamics DNS lookup failed")
		}
		// Validate every answer before dialing a pinned address. Loopback, link
		// local, multicast and unspecified destinations are always denied.
		for _, addr := range resolved {
			if !allowedAddress(addr, prefixes) {
				return nil, errors.New("AppDynamics destination is outside approved network policy")
			}
		}
		for _, addr := range resolved {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("AppDynamics connection failed")
	}
	client := newClient(cfg, credentials, &http.Client{Transport: transport})
	client.controllerRate = sharedRateState("controller", strings.TrimRight(cfg.Endpoint, "/")+cfg.ContextPath, cfg.AccountName, "")
	client.analyticsRate = sharedRateState("analytics", cfg.EventsEndpoint, cfg.EventsAccountName, credentials.EventsAPIKey)
	if cfg.AuthType == AuthOAuth {
		client.authState = sharedTokenState(cfg, credentials)
	}
	return client, nil
}

func allowedAddress(addr netip.Addr, prefixes []netip.Prefix) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	if addr == netip.MustParseAddr("fd00:ec2::254") || addr == netip.MustParseAddr("168.63.129.16") {
		return false
	}
	if netpolicy.IsPublicAddr(addr) {
		return true
	}
	// Explicit approvals only extend to private administrative address space,
	// never documentation, metadata, benchmarking or reserved networks.
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

// NewClientWithHTTPClient injects a trusted transport for deterministic tests
// and embedding. Callers own its destination policy; API configuration never
// selects this constructor. URL validation, redirects and size limits remain.
func NewClientWithHTTPClient(cfg Config, credentials Credentials, trusted *http.Client) (*Client, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateCredentials(cfg, credentials); err != nil {
		return nil, err
	}
	if trusted == nil {
		return nil, errors.New("trusted HTTP client is required")
	}
	return newClient(cfg, credentials, trusted), nil
}
func newClient(cfg Config, credentials Credentials, h *http.Client) *Client {
	copyClient := *h
	copyClient.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	copyClient.Jar = nil
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{cfg: cfg, credentials: credentials, http: &copyClient, authState: &tokenState{}, controllerRate: &rateState{}, analyticsRate: &rateState{}}
}
func validateCredentials(cfg Config, c Credentials) error {
	for _, v := range []string{c.ClientID, c.ClientSecret, c.Username, c.Password, c.EventsAPIKey} {
		if len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid AppDynamics credential")
		}
	}
	if cfg.AuthType == AuthOAuth && (c.ClientID == "" || c.ClientSecret == "") {
		return errors.New("OAuth client ID and secret are required")
	}
	if cfg.AuthType == AuthBasic && (c.Username == "" || c.Password == "") {
		return errors.New("Basic username and password are required")
	}
	return nil
}

func (c *Client) waitRate(ctx context.Context, analytics bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state := c.controllerRate
	rate := c.cfg.RequestsPerMinute
	if analytics {
		state = c.analyticsRate
		rate = c.cfg.AnalyticsRequestsPerMinute
	}
	state.mu.Lock()
	at := time.Now()
	if state.next.After(at) {
		at = state.next
	}
	if deadline, ok := ctx.Deadline(); ok && at.After(deadline) {
		state.mu.Unlock()
		return context.DeadlineExceeded
	}
	state.next = at.Add(time.Minute / time.Duration(rate))
	state.used = time.Now()
	state.mu.Unlock()
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type rateState struct {
	mu         sync.Mutex
	next, used time.Time
	inflight   chan struct{}
}

func (c *Client) acquireRequest(ctx context.Context, analytics bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state := c.controllerRate
	if analytics {
		state = c.analyticsRate
	}
	state.mu.Lock()
	if state.inflight == nil {
		// Durable quota leases permit two executor identities per resource.
		// Each identity allows one request, keeping their combined ceiling two.
		state.inflight = make(chan struct{}, 1)
	}
	permit := state.inflight
	state.mu.Unlock()
	select {
	case permit <- struct{}{}:
		return func() { <-permit }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var rateRegistry = struct {
	sync.Mutex
	states   map[[32]byte]*rateState
	overflow *rateState
}{states: map[[32]byte]*rateState{}, overflow: &rateState{}}

func sharedRateState(kind, endpoint, account, identity string) *rateState {
	key := sha256.Sum256([]byte(kind + "\x00" + normalizedRateTarget(endpoint) + "\x00" + strings.ToLower(strings.TrimSpace(account)) + "\x00" + identity))
	rateRegistry.Lock()
	defer rateRegistry.Unlock()
	if state := rateRegistry.states[key]; state != nil {
		return state
	}
	if len(rateRegistry.states) >= 256 {
		return rateRegistry.overflow
	}
	state := &rateState{used: time.Now()}
	rateRegistry.states[key] = state
	return state
}

func ControllerQuotaKey(cfg Config) string {
	cfg = cfg.Defaults()
	return quotaKey("controller", strings.TrimRight(cfg.Endpoint, "/")+cfg.ContextPath, cfg.AccountName, "")
}
func AnalyticsQuotaKey(cfg Config, credentials Credentials) string {
	return quotaKey("analytics", cfg.EventsEndpoint, cfg.EventsAccountName, credentials.EventsAPIKey)
}
func quotaKey(kind, endpoint, account, identity string) string {
	key := sha256.Sum256([]byte(kind + "\x00" + normalizedRateTarget(endpoint) + "\x00" + strings.ToLower(strings.TrimSpace(account)) + "\x00" + identity))
	return fmt.Sprintf("%x", key)
}
func normalizedRateTarget(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && port != "443" {
		host += ":" + port
	}
	u.Host = host
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String()
}
func (c *Client) accessToken(ctx context.Context, rejected string) (string, error) {
	state := c.authState
	state.mu.Lock()
	defer state.mu.Unlock()
	state.used = time.Now()
	// A delayed 401 must not discard a newer token refreshed by another job.
	if state.token != "" && time.Now().Before(state.expires) && (rejected == "" || rejected != state.token) {
		return state.token, nil
	}
	id := c.credentials.ClientID
	if !strings.HasSuffix(id, "@"+c.cfg.AccountName) {
		id += "@" + c.cfg.AccountName
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {c.credentials.ClientSecret}}
	u := strings.TrimRight(c.cfg.Endpoint, "/") + c.cfg.ContextPath + "/api/oauth/access_token"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errors.New("cannot construct token request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	body, err := c.execute(ctx, request, false)
	if err != nil {
		return "", err
	}
	var response struct {
		Token   string `json:"access_token"`
		Expires int64  `json:"expires_in"`
	}
	if err := decodeJSON(body, &response); err != nil || response.Token == "" || len(response.Token) > 16384 || response.Expires <= 0 || response.Expires > 86400*365 {
		return "", errors.New("invalid OAuth token response")
	}
	state.token = response.Token
	lifetime := time.Duration(response.Expires) * time.Second
	state.expires = time.Now().Add(lifetime - min(30*time.Second, lifetime/10))
	return state.token, nil
}

func (c *Client) execute(ctx context.Context, req *http.Request, analytics bool) ([]byte, error) {
	if b, ok := ctx.Value(budgetKey{}).(*pollBudget); ok {
		if err := b.take(); err != nil {
			return nil, err
		}
	}
	release, err := c.acquireRequest(ctx, analytics)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := c.waitRate(ctx, analytics); err != nil {
		return nil, err
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnreachable
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(c.cfg.MaxResponseBytes)+1))
	if err != nil {
		return nil, errors.New("AppDynamics response read failed")
	}
	if len(data) > c.cfg.MaxResponseBytes {
		return nil, errors.New("AppDynamics response exceeds byte limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		kind := "upstream error"
		switch response.StatusCode {
		case 401:
			kind = "authentication failed"
		case 402:
			kind = "unlicensed"
		case 403:
			kind = "permission denied"
			if analytics && explicitlyUnlicensed(data) {
				kind = "unlicensed"
			}
		case 404, 405, 501:
			kind = "capability unavailable"
		case 429:
			kind = "rate limited"
			low := strings.ToLower(string(data))
			if strings.Contains(low, "point") && (strings.Contains(low, "limit") || strings.Contains(low, "budget")) {
				kind = "point budget exceeded"
			}
		}
		retry := time.Duration(0)
		if seconds, e := strconv.Atoi(response.Header.Get("Retry-After")); e == nil && seconds >= 0 && seconds <= 30 {
			retry = time.Duration(seconds) * time.Second
		}
		return nil, &HTTPError{Status: response.StatusCode, Kind: kind, RetryAfter: retry}
	}
	typeValue, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !(typeValue == "application/json" || strings.HasSuffix(typeValue, "+json")) {
		return nil, errors.New("AppDynamics response is not JSON")
	}
	return data, nil
}

// License classification reads only an explicit error marker; upstream narrative
// text is never returned to the controller or persisted as capability health.
func explicitlyUnlicensed(body []byte) bool {
	var fields map[string]any
	if json.Unmarshal(body, &fields) != nil {
		return false
	}
	for _, field := range []string{"code", "errorCode", "error", "message"} {
		text := strings.ToLower(scalar(fields[field]))
		if strings.Contains(text, "unlicensed") || strings.Contains(text, "license required") || strings.Contains(text, "not licensed") || strings.Contains(text, "license expired") {
			return true
		}
	}
	return false
}

func unsupportedMetricAPI(err error) bool {
	var status *HTTPError
	return errors.As(err, &status) && (status.Status == 404 || status.Status == 405 || status.Status == 501)
}

func decodeJSON(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid AppDynamics JSON response")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid AppDynamics JSON response")
	}
	return nil
}

func (c *Client) query(ctx context.Context, method, path string, params url.Values, body []byte, analytics bool, out any) error {
	origin := c.cfg.Endpoint
	prefix := c.cfg.ContextPath
	if analytics {
		origin = c.cfg.EventsEndpoint
		prefix = ""
		if origin == "" || c.cfg.EventsAccountName == "" || c.credentials.EventsAPIKey == "" {
			return errors.New("Analytics credentials and endpoint are required")
		}
	}
	u := strings.TrimRight(origin, "/") + prefix + path
	if params == nil {
		params = url.Values{}
	}
	if !analytics {
		params.Set("output", "JSON")
	}
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	rejectedToken := ""
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
		if err != nil {
			return errors.New("invalid AppDynamics request")
		}
		usedToken := ""
		if analytics {
			req.Header.Set("X-Events-API-AccountName", c.cfg.EventsAccountName)
			req.Header.Set("X-Events-API-Key", c.credentials.EventsAPIKey)
			req.Header.Set("Content-Type", "application/vnd.appd.events+text;v=2")
			req.Header.Set("Accept", "application/vnd.appd.events+json;v=2")
		} else if c.cfg.AuthType == AuthBasic {
			username := c.credentials.Username
			if !strings.HasSuffix(username, "@"+c.cfg.AccountName) {
				username += "@" + c.cfg.AccountName
			}
			req.SetBasicAuth(username, c.credentials.Password)
		} else {
			token, err := c.accessToken(ctx, rejectedToken)
			if err != nil {
				return err
			}
			usedToken = token
			req.Header.Set("Authorization", "Bearer "+token)
		}
		data, err := c.execute(ctx, req, analytics)
		if err == nil {
			return decodeJSON(data, out)
		}
		var status *HTTPError
		if !errors.As(err, &status) || attempt > 0 {
			return err
		}
		if status.Status == 401 && !analytics && c.cfg.AuthType == AuthOAuth {
			rejectedToken = usedToken
			continue
		}
		if status.Status == 429 && status.Kind == "rate limited" && status.RetryAfter > 0 {
			timer := time.NewTimer(status.RetryAfter)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		return err
	}
	return errors.New("AppDynamics retry exhausted")
}

func timeParams(from, to time.Time) url.Values {
	return url.Values{"time-range-type": {"BETWEEN_TIMES"}, "start-time": {strconv.FormatInt(from.UnixMilli(), 10)}, "end-time": {strconv.FormatInt(to.UnixMilli(), 10)}}
}

func (c *Client) Discover(ctx context.Context, applicationID, kind, path string) ([]Entity, error) {
	endpoint := "/rest/applications"
	if kind == "applications" && applicationID != "" {
		if !numericID.MatchString(applicationID) {
			return nil, errors.New("application ID must be numeric")
		}
		endpoint += "/" + applicationID
	}
	isMetrics := false
	if kind != "applications" {
		if !numericID.MatchString(applicationID) {
			return nil, errors.New("application ID must be numeric")
		}
		endpoint += "/" + applicationID
		switch kind {
		case "tiers", "nodes", "backends":
			endpoint += "/" + kind
		case "business_transactions", "business-transactions":
			endpoint += "/business-transactions"
		case "metrics":
			endpoint += "/metrics"
			isMetrics = true
		default:
			return nil, errors.New("unsupported discovery kind")
		}
	}
	params := url.Values{}
	if path != "" {
		if !isMetrics || len(path) > 4096 || strings.ContainsAny(path, "\r\n") {
			return nil, errors.New("invalid metric discovery path")
		}
		params.Set("metric-path", path)
	}
	var rows []map[string]any
	if err := c.query(ctx, "GET", endpoint, params, nil, false, &rows); err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, errors.New("AppDynamics discovery exceeds entity bound")
	}
	result := make([]Entity, 0, len(rows))
	for _, row := range rows {
		e := Entity{ID: scalar(row["id"]), Name: safeLabel(scalar(row["name"]), 256, c.credentials), Type: kind, ParentID: applicationID}
		if !isMetrics && !numericID.MatchString(e.ID) {
			continue
		}
		if isMetrics {
			e.Type = scalar(row["type"])
			if path == "" {
				e.Path = e.Name
			} else {
				e.Path = path + "|" + e.Name
			}
		}
		if e.Name != "" {
			result = append(result, e)
		}
	}
	return result, nil
}

func capabilityStatus(err error) string {
	if err == nil {
		return "available"
	}
	var status *HTTPError
	if errors.As(err, &status) {
		if status.Kind == "unlicensed" {
			return "unlicensed"
		}
		switch status.Status {
		case 401:
			return "unauthorized"
		case 403:
			return "forbidden"
		case 404, 405, 501:
			return "unsupported"
		case 429:
			return "rate_limited"
		}
	}
	if errors.Is(err, ErrUnreachable) {
		return "unreachable"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrBudget) {
		return "budget_exhausted"
	}
	return "error"
}
func cloneCapabilities(c Capabilities) Capabilities {
	if c.Status != nil {
		copyStatus := make(map[string]string, len(c.Status))
		for k, v := range c.Status {
			copyStatus[k] = v
		}
		c.Status = copyStatus
	}
	return c
}

type tokenState struct {
	mu            sync.Mutex
	token         string
	expires, used time.Time
}

var tokenRegistry = struct {
	sync.Mutex
	states map[[32]byte]*tokenState
}{states: map[[32]byte]*tokenState{}}

func sharedTokenState(cfg Config, credentials Credentials) *tokenState {
	id := credentials.ClientID
	if !strings.HasSuffix(id, "@"+cfg.AccountName) {
		id += "@" + cfg.AccountName
	}
	key := sha256.Sum256([]byte(normalizedRateTarget(strings.TrimRight(cfg.Endpoint, "/")+cfg.ContextPath) + "\x00" + cfg.AccountName + "\x00" + id + "\x00" + credentials.ClientSecret))
	tokenRegistry.Lock()
	defer tokenRegistry.Unlock()
	if state := tokenRegistry.states[key]; state != nil {
		return state
	}
	if len(tokenRegistry.states) >= 256 {
		for k, state := range tokenRegistry.states {
			if !state.mu.TryLock() {
				continue
			}
			expired := time.Since(state.used) > 10*time.Minute || !state.expires.After(time.Now())
			state.mu.Unlock()
			if expired {
				delete(tokenRegistry.states, k)
			}
		}
	}
	state := &tokenState{used: time.Now()}
	if len(tokenRegistry.states) < 256 {
		tokenRegistry.states[key] = state
	}
	return state
}
