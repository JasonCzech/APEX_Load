package broker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

var ErrSupervisorTransport = errors.New("java_supervisor_transport_uncertain")

type SupervisorFailure struct{ Code, State string }

func (e *SupervisorFailure) Error() string { return e.Code }

// SupervisorClient is bound to one immutable host and server URI SAN. It never
// retries an invocation: any ambiguous exchange permanently closes invocation
// admission. Control requests have a separate lane, so renewal/close can proceed
// while Java blocks. A successful Close is not a termination receipt.
type SupervisorClient struct {
	config        SupervisorConfig
	url           string
	http          *http.Client
	invokeMu      sync.Mutex
	sequence      uint64
	terminal      atomic.Bool
	nativeCapable atomic.Bool
}

func NewSupervisorClient(endpoint, serverURI string, cfg SupervisorConfig, config *tls.Config) (*SupervisorClient, error) {
	u, e := url.Parse(endpoint)
	identity, identityErr := url.Parse(serverURI)
	validator := SupervisorControl{config: cfg}
	q := SupervisorRequest{Contract: SupervisorContract, HostID: cfg.HostID, BrokerEpoch: cfg.BrokerEpoch, ConfigurationSHA256: cfg.ConfigurationSHA256, RequestID: "validate", Operation: "ready"}
	if identityErr != nil || identity.Path != "/java-host/"+cfg.HostID || !validSupervisorConfig(cfg) || e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" || !name.MatchString(cfg.HostID) || !name.MatchString(cfg.BrokerEpoch) || !validator.validRequest(q) || !validBrokerURI(serverURI) || config == nil || config.RootCAs == nil || config.InsecureSkipVerify || len(config.Certificates) != 1 || cfg.InvocationTimeout < time.Millisecond || cfg.InvocationTimeout > time.Hour {
		return nil, ErrState
	}
	if len(config.Certificates[0].Certificate) == 0 {
		return nil, ErrState
	}
	leaf, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if err != nil {
		return nil, ErrState
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != cfg.BrokerClientURI {
		return nil, ErrState
	}
	tc := config.Clone()
	tc.RootCAs = config.RootCAs.Clone()
	tc.MinVersion = max(tc.MinVersion, tls.VersionTLS13)
	if tc.RootCAs != nil {
		tc.RootCAs = tc.RootCAs.Clone()
	}
	verify := tc.VerifyConnection
	tc.VerifyConnection = func(s tls.ConnectionState) error {
		if len(s.VerifiedChains) == 0 || len(s.VerifiedChains[0]) == 0 {
			return ErrSupervisorTransport
		}
		uris := s.VerifiedChains[0][0].URIs
		if len(uris) != 1 || uris[0].String() != serverURI {
			return ErrSupervisorTransport
		}
		if verify != nil {
			return verify(s)
		}
		return nil
	}
	transport := &http.Transport{TLSClientConfig: tc, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: cfg.InvocationTimeout + 5*time.Second, MaxIdleConnsPerHost: 4, IdleConnTimeout: 10 * time.Second, MaxResponseHeaderBytes: 8 << 10}
	return &SupervisorClient{config: cfg, url: u.Scheme + "://" + u.Host + "/v1/java-host", http: &http.Client{Transport: transport, Timeout: cfg.InvocationTimeout + 10*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// AwaitTransport waits for the pinned TLS identity without issuing any stateful
// HTTP control request. A pod TCP proxy may accept connections before PID1 is
// listening. Retrying this handshake cannot replay init, commit or invocation.
// The preparation deadline still bounds all attempts; failure closes admission.
func (c *SupervisorClient) AwaitTransport(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, c.config.PreparationTimeout)
	defer cancel()
	u, _ := url.Parse(c.url)
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	transport := c.http.Transport.(*http.Transport)
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 500 * time.Millisecond}, Config: transport.TLSClientConfig}
	for {
		if c.terminal.Load() {
			return ErrSupervisorTransport
		}
		attempt, stop := context.WithTimeout(ctx, 500*time.Millisecond)
		conn, e := dialer.DialContext(attempt, "tcp", net.JoinHostPort(host, port))
		stop()
		if e == nil {
			_ = conn.Close()
			if ctx.Err() == nil && !c.terminal.Load() {
				return nil
			}
		}
		if e = waitPreparation(ctx, 50*time.Millisecond); e != nil {
			c.Close()
			return ErrSupervisorTransport
		}
	}
}
func (c *SupervisorClient) Close() { c.terminal.Store(true); c.http.CloseIdleConnections() }
func (c *SupervisorClient) Control(ctx context.Context, operation string) (SupervisorResponse, error) {
	if c.terminal.Load() && operation != "close" {
		return SupervisorResponse{}, ErrSupervisorTransport
	}
	if operation != "ready" && operation != "commit" && operation != "renew" && operation != "close" {
		return SupervisorResponse{}, ErrState
	}
	// Bound control separately from the potentially long invocation timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, e := c.exchange(ctx, SupervisorRequest{Operation: operation})
	if e == nil && operation == "ready" && out.State == "ready" {
		c.nativeCapable.Store(out.NativeData)
	}
	if e != nil || operation == "close" {
		c.terminal.Store(true)
	}
	return out, e
}
func (c *SupervisorClient) Invoke(ctx context.Context, method string, params map[string]string) (SupervisorResponse, error) {
	// Never queue invocations. A busy slot must remain occupied.
	if !c.invokeMu.TryLock() {
		return SupervisorResponse{}, ErrState
	}
	defer c.invokeMu.Unlock()
	if c.terminal.Load() || c.sequence == ^uint64(0) || ctx.Err() != nil || (javarunner.NativeDataCallbackFrom(ctx) != nil && !c.nativeCapable.Load()) {
		return SupervisorResponse{}, ErrState
	}
	q := SupervisorRequest{Operation: "invoke", Sequence: c.sequence + 1, Method: method, Parameters: params}
	// Invalid local input does not consume the host sequence.
	c.bind(&q, "validate")
	if !(&SupervisorControl{config: c.config}).validRequest(q) {
		return SupervisorResponse{}, ErrState
	}
	c.sequence++
	out, e := c.exchange(ctx, q)
	if e != nil {
		c.terminal.Store(true)
	}
	return out, e
}
func (c *SupervisorClient) bind(q *SupervisorRequest, id string) {
	q.Contract = SupervisorContract
	q.HostID = c.config.HostID
	q.BrokerEpoch = c.config.BrokerEpoch
	q.ConfigurationSHA256 = c.config.ConfigurationSHA256
	q.RequestID = id
}
func (c *SupervisorClient) exchange(ctx context.Context, q SupervisorRequest) (SupervisorResponse, error) {
	var out SupervisorResponse
	id, e := randomID()
	if e != nil {
		return out, ErrSupervisorTransport
	}
	c.bind(&q, id)
	data, e := json.Marshal(q)
	if e != nil || len(data) > 512<<10 {
		return out, ErrState
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(data))
	if e != nil {
		return out, ErrState
	}
	req.Header.Set("Content-Type", "application/json")
	response, e := javarunner.DoNativeHTTP(c.http, req, MaxSupervisorResponse)
	if e != nil {
		return out, ErrSupervisorTransport
	}
	defer response.Body.Close()
	data, e = io.ReadAll(io.LimitReader(response.Body, MaxSupervisorResponse+1))
	if e != nil || len(data) > MaxSupervisorResponse || response.StatusCode != http.StatusOK && response.StatusCode != http.StatusConflict {
		return out, ErrSupervisorTransport
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || out.Contract != q.Contract || out.HostID != q.HostID || out.BrokerEpoch != q.BrokerEpoch || out.ConfigurationSHA256 != q.ConfigurationSHA256 || out.RequestID != q.RequestID || len(out.Payload) > MaxSupervisorPayload {
		return SupervisorResponse{}, ErrSupervisorTransport
	}
	if out.LeaseTTLMS != c.config.LeaseTTL.Milliseconds() || out.PreparationTimeoutMS != c.config.PreparationTimeout.Milliseconds() || out.InvocationTimeoutMS != c.config.InvocationTimeout.Milliseconds() {
		return SupervisorResponse{}, ErrSupervisorTransport
	}
	if q.Operation == "invoke" && out.InvocationID != SupervisorInvocationID(q.Sequence) || q.Operation != "invoke" && out.InvocationID != "" {
		return SupervisorResponse{}, ErrSupervisorTransport
	}
	switch out.State {
	case "preparing", "ready", "committed", "retiring":
	default:
		return SupervisorResponse{}, ErrSupervisorTransport
	}
	if out.Failure != "" {
		if response.StatusCode != http.StatusConflict || !supervisorFailureCode(out.Failure) {
			return SupervisorResponse{}, ErrSupervisorTransport
		}
		return out, &SupervisorFailure{Code: out.Failure, State: out.State}
	}
	if response.StatusCode != http.StatusOK || q.Operation == "invoke" && (out.State != "committed" || len(out.Payload) == 0) || q.Operation != "invoke" && len(out.Payload) != 0 || q.Operation == "commit" && out.State != "committed" || q.Operation == "close" && out.State != "retiring" {
		return SupervisorResponse{}, ErrSupervisorTransport
	}
	return out, nil
}
func supervisorFailureCode(code string) bool {
	switch code {
	case "supervisor_closed", "supervisor_cancelled", "supervisor_lease_expired", "supervisor_preparation_expired", "supervisor_preparation_failed", "supervisor_retiring", "supervisor_not_ready", "supervisor_request_cancelled", "supervisor_request_invalid", "supervisor_invocation_rejected", "supervisor_invocation_failed":
		return true
	}
	return false
}
