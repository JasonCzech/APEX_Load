package dynatrace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/netpolicy"
	"golang.org/x/sync/singleflight"
)

var ErrBudget = errors.New("Dynatrace collection budget exhausted")
var ErrUnreachable = errors.New("Dynatrace destination is unreachable")

type HTTPError struct {
	Status     int
	Kind       string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Dynatrace %s (HTTP %d)", e.Kind, e.Status) }

type Client struct {
	cfg                           Config
	credentials                   Credentials
	http                          *http.Client
	token                         *tokenState
	environmentRate, platformRate *rateState
	permit                        func(context.Context, bool) error
	scan                          func(context.Context, int64) error
	scanHook                      func(string, int64) error
	now                           func() time.Time
}
type tokenState struct {
	mu                sync.Mutex
	group             singleflight.Group
	value             string
	expires, lastUsed time.Time
}
type rateState struct {
	mu       sync.Mutex
	next     time.Time
	slots    chan struct{}
	lastUsed time.Time
}

var sharedStates = struct {
	sync.Mutex
	tokens map[string]*tokenState
	rates  map[string]*rateState
	// Uncached identities share a conservative limiter rather than receiving
	// a fresh budget on each client construction when the bounded cache fills.
	overflowRate *rateState
}{tokens: map[string]*tokenState{}, rates: map[string]*rateState{}, overflowRate: &rateState{slots: make(chan struct{}, 1)}}

func hashIdentity(v ...string) string {
	h := sha256.Sum256([]byte(strings.Join(v, "\x00")))
	return hex.EncodeToString(h[:])
}

// RequestQuotaIdentity groups consumers of the same upstream read principal.
// Credential version and unrelated endpoint credentials must not create fresh
// request budgets for an existing principal.
func RequestQuotaIdentity(cfg Config, creds Credentials, platform bool) string {
	if !platform {
		return hashIdentity("environment", quotaOrigin(cfg.EnvironmentURL), creds.EnvironmentToken)
	}
	if cfg.Defaults().PlatformAuthType == AuthToken {
		return hashIdentity("platform_token", quotaOrigin(cfg.PlatformURL), creds.PlatformToken)
	}
	return hashIdentity("oauth_client", quotaOrigin(cfg.PlatformURL), cfg.OAuthResource, creds.ClientID, creds.ClientSecret)
}

func quotaOrigin(raw string) string {
	endpoint, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.ToLower(endpoint.Scheme) + "://" + strings.ToLower(endpoint.Hostname())
}
func sharedToken(cfg Config, creds Credentials) *tokenState {
	key := hashIdentity(cfg.PlatformURL, cfg.OAuthResource, strings.Join(cfg.OAuthScopes, " "), creds.ClientID, creds.ClientSecret, creds.VersionIdentity)
	sharedStates.Lock()
	defer sharedStates.Unlock()
	if sharedStates.tokens[key] == nil {
		if len(sharedStates.tokens) >= 256 {
			for k, s := range sharedStates.tokens {
				s.mu.Lock()
				old := time.Since(s.lastUsed) > time.Hour
				s.mu.Unlock()
				if old {
					delete(sharedStates.tokens, k)
				}
			}
		}
		if len(sharedStates.tokens) >= 256 {
			return nil // An unshared fallback would lose refresh coalescing.
		}
		sharedStates.tokens[key] = &tokenState{lastUsed: time.Now()}
	}
	state := sharedStates.tokens[key]
	state.mu.Lock()
	state.lastUsed = time.Now()
	state.mu.Unlock()
	return state
}
func sharedRate(key string) *rateState {
	sharedStates.Lock()
	defer sharedStates.Unlock()
	if s := sharedStates.rates[key]; s != nil {
		s.mu.Lock()
		s.lastUsed = time.Now()
		s.mu.Unlock()
		return s
	}
	// Keep admitted mappings for process lifetime: evicting them could split an
	// existing client's semaphore or move an overflow client to a fresh limiter.
	// The fixed overflow limiter bounds memory for all remaining identities.
	if len(sharedStates.rates) >= 512 {
		return sharedStates.overflowRate
	}
	// Durable quota admission assigns at most two executors to one upstream
	// principal. Each executor gets one process-shared request slot, so concurrent
	// clients/jobs within that executor cannot multiply the global two requests.
	s := &rateState{slots: make(chan struct{}, 1), lastUsed: time.Now()}
	sharedStates.rates[key] = s
	return s
}
func validateCredentials(c Credentials) error {
	for _, v := range []string{c.EnvironmentToken, c.PlatformToken, c.ClientID, c.ClientSecret, c.VersionIdentity} {
		if len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid Dynatrace credential")
		}
	}
	return nil
}
func NewClient(cfg Config, creds Credentials) (*Client, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateCredentials(creds); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: time.Duration(cfg.TimeoutSeconds) * time.Second, MaxResponseHeaderBytes: 64 << 10}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialPublic(ctx, network, address, net.DefaultResolver.LookupNetIP, dialer.DialContext)
	}
	return newClient(cfg, creds, &http.Client{Transport: transport}, true)
}

// NewClientWithHTTPClient is for trusted fixture transports. Runtime API
// configuration cannot select it; origin, redirect and response bounds remain.
func NewClientWithHTTPClient(cfg Config, creds Credentials, h *http.Client) (*Client, error) {
	cfg = cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateCredentials(creds); err != nil {
		return nil, err
	}
	if h == nil {
		return nil, errors.New("trusted fixture HTTP client required")
	}
	return newClient(cfg, creds, h, false)
}
func newClient(cfg Config, creds Credentials, h *http.Client, shared bool) (*Client, error) {
	cfg.OAuthScopes = append([]string(nil), cfg.OAuthScopes...)
	copy := *h
	copy.Timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &Client{cfg: cfg, credentials: creds, http: &copy, token: &tokenState{}, now: time.Now}
	if shared {
		// Only selected OAuth credentials consume refresh-cache admission. Token-
		// only and Classic-only clients cannot crowd out active OAuth principals.
		oauth := cfg.PlatformURL != "" && cfg.PlatformAuthType == AuthOAuth && creds.ClientID != "" && creds.ClientSecret != ""
		if oauth {
			c.token = sharedToken(cfg, creds)
			if c.token == nil {
				return nil, ErrBudget
			}
		}
		if cfg.EnvironmentURL != "" && creds.EnvironmentToken != "" {
			c.environmentRate = sharedRate(RequestQuotaIdentity(cfg, creds, false))
		}
		if cfg.PlatformURL != "" && (oauth || cfg.PlatformAuthType == AuthToken && creds.PlatformToken != "") {
			c.platformRate = sharedRate(RequestQuotaIdentity(cfg, creds, true))
		}
	}
	return c, nil
}

// SetBudgetHooks installs executor leases. Hooks are private runtime state and
// never serialized. The client still enforces its local request/response limits.
func (c *Client) SetBudgetHooks(request func(context.Context, bool) error, scan func(context.Context, int64) error) {
	c.permit = request
	c.scan = scan
}
func (c *Client) SetScanHook(hook func(string, int64) error) { c.scanHook = hook }

// queryPrincipal binds private continuation to its originating credential
// revision without including plaintext credentials in the cursor.
func (c *Client) queryPrincipal() string {
	return hashIdentity(c.cfg.PlatformURL, c.cfg.PlatformAuthType, c.cfg.OAuthResource, strings.Join(c.cfg.OAuthScopes, " "), c.credentials.PlatformToken, c.credentials.ClientID, c.credentials.ClientSecret, c.credentials.VersionIdentity)
}

type lookupFunc func(context.Context, string, string) ([]netip.Addr, error)
type dialFunc func(context.Context, string, string) (net.Conn, error)

func dialPublic(ctx context.Context, network, address string, lookup lookupFunc, dial dialFunc) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrUnreachable
	}
	addresses, err := lookup(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrUnreachable
	}
	for _, ip := range addresses {
		if !netpolicy.IsPublicAddr(ip.Unmap()) || ip.Unmap() == netip.MustParseAddr("168.63.129.16") || ip.Unmap() == netip.MustParseAddr("fd00:ec2::254") {
			return nil, errors.New("Dynatrace destination is not a public address")
		}
	}
	for _, ip := range addresses {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, ErrUnreachable
}

type pollBudget struct {
	mu        sync.Mutex
	remaining int
}
type budgetKey struct{}

func (c *Client) pollContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.MaxPollSeconds)*time.Second)
	return context.WithValue(ctx, budgetKey{}, &pollBudget{remaining: c.cfg.MaxRequestsPerPoll}), cancel
}
func (c *Client) acquire(ctx context.Context, platform bool) (func(), error) {
	if b, _ := ctx.Value(budgetKey{}).(*pollBudget); b != nil {
		b.mu.Lock()
		if b.remaining <= 0 {
			b.mu.Unlock()
			return nil, ErrBudget
		}
		b.remaining--
		b.mu.Unlock()
	}
	if c.permit != nil {
		if err := c.permit(ctx, platform); err != nil {
			return nil, err
		}
	}
	s, rate := c.environmentRate, c.cfg.RequestsPerMinute
	if platform {
		s, rate = c.platformRate, c.cfg.PlatformRequestsPerMinute
	}
	if s == nil {
		return func() {}, nil
	}
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-s.slots }
	s.mu.Lock()
	at := c.now()
	if s.next.After(at) {
		at = s.next
	}
	if deadline, ok := ctx.Deadline(); ok && at.After(deadline) {
		s.mu.Unlock()
		release()
		return nil, ErrBudget
	}
	s.next = at.Add(time.Minute / time.Duration(rate))
	s.lastUsed = c.now()
	s.mu.Unlock()
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	case <-timer.C:
		return release, nil
	}
}
func (c *Client) request(ctx context.Context, platform bool, method, path string, params url.Values, payload any) ([]byte, error) {
	base := c.cfg.EnvironmentURL
	if platform {
		base = c.cfg.PlatformURL
	}
	if base == "" {
		return nil, errors.New("selected Dynatrace endpoint is not configured")
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("invalid query payload")
		}
		body = bytes.NewReader(raw)
	}
	endpoint := strings.TrimRight(base, "/") + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("invalid Dynatrace request")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if platform {
		token, err := c.platformToken(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		if c.credentials.EnvironmentToken == "" {
			return nil, errors.New("Dynatrace environment credentials unavailable")
		}
		req.Header.Set("Authorization", "Api-Token "+c.credentials.EnvironmentToken)
	}
	release, err := c.acquire(ctx, platform)
	if err != nil {
		return nil, err
	}
	defer release()
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnreachable
	}
	return c.readResponse(resp)
}
func (c *Client) readResponse(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, ErrUnreachable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.cfg.MaxResponseBytes)+1))
	if err != nil || len(data) > c.cfg.MaxResponseBytes {
		return nil, errors.New("Dynatrace response exceeds readable byte limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := "upstream_error"
		switch resp.StatusCode {
		case 401:
			kind = "authentication_denied"
		case 403:
			kind = "permission_denied"
		case 404, 405, 501:
			kind = "unsupported"
		case 429:
			kind = "throttled"
		case 400:
			kind = "query_rejected"
			if strings.Contains(strings.ToLower(string(data)), "data point") || strings.Contains(strings.ToLower(string(data)), "datapoint") {
				kind = "query_budget"
			}
		}
		retry := time.Duration(0)
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds >= 0 && seconds <= 300 {
			retry = time.Duration(seconds) * time.Second
		} else if at, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
			retry = at.Sub(c.now())
			if retry < 0 {
				retry = 0
			}
			if retry > 300*time.Second {
				retry = 300 * time.Second
			}
		}
		return nil, &HTTPError{Status: resp.StatusCode, Kind: kind, RetryAfter: retry}
	}
	return data, nil
}
func (c *Client) platformToken(ctx context.Context) (string, error) {
	if c.cfg.PlatformAuthType == AuthToken {
		if c.credentials.PlatformToken == "" {
			return "", errors.New("Dynatrace Platform credentials unavailable")
		}
		return c.credentials.PlatformToken, nil
	}
	if c.credentials.ClientID == "" || c.credentials.ClientSecret == "" || c.cfg.OAuthResource == "" || len(c.cfg.OAuthScopes) == 0 {
		return "", errors.New("Dynatrace OAuth credentials unavailable")
	}
	state := c.token
	result := state.group.DoChan("token", func() (any, error) {
		state.mu.Lock()
		state.lastUsed = c.now()
		if state.value != "" && c.now().Before(state.expires.Add(-30*time.Second)) {
			v := state.value
			state.mu.Unlock()
			return v, nil
		}
		state.mu.Unlock()
		refreshCtx, cancel := context.WithTimeout(context.Background(), time.Duration(c.cfg.TimeoutSeconds)*time.Second)
		defer cancel()
		form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.credentials.ClientID}, "client_secret": {c.credentials.ClientSecret}, "resource": {c.cfg.OAuthResource}, "scope": {strings.Join(c.cfg.OAuthScopes, " ")}}
		req, _ := http.NewRequestWithContext(refreshCtx, http.MethodPost, "https://sso.dynatrace.com/sso/oauth2/token", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		release, err := c.acquire(refreshCtx, true)
		if err != nil {
			return nil, err
		}
		defer release()
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, ErrUnreachable
		}
		data, err := c.readResponse(resp)
		if err != nil {
			return nil, err
		}
		var token struct {
			Value   string `json:"access_token"`
			Expires int64  `json:"expires_in"`
			Type    string `json:"token_type"`
		}
		if json.Unmarshal(data, &token) != nil || token.Value == "" || len(token.Value) > 8192 || strings.ContainsAny(token.Value, "\r\n\x00") || token.Expires <= 0 || token.Expires > 86400 || token.Type != "" && !strings.EqualFold(token.Type, "Bearer") {
			return nil, errors.New("invalid Dynatrace OAuth token response")
		}
		state.mu.Lock()
		state.value = token.Value
		state.expires = c.now().Add(time.Duration(token.Expires) * time.Second)
		state.mu.Unlock()
		return token.Value, nil
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return "", r.Err
		}
		return r.Val.(string), nil
	}
}
func capabilityStatus(err error) string {
	if err == nil {
		return "available"
	}
	var h *HTTPError
	if errors.As(err, &h) {
		return h.Kind
	}
	if errors.Is(err, ErrBudget) {
		return "budget_exhausted"
	}
	if errors.Is(err, errUnsafeDimensions) {
		return "query_rejected"
	}
	if errors.Is(err, ErrPrincipalChanged) {
		return "authentication_denied"
	}
	return "unreachable"
}
