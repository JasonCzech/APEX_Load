package javasidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

var ErrFacadeTransport = errors.New("java_facade_transport_uncertain")

type FacadeFailure struct{ Code string }

func (e *FacadeFailure) Error() string { return e.Code }

type BrokerFacadeClient struct {
	endpoint, token string
	http            *http.Client
	mu              sync.Mutex
	owner           string
	attempted       bool
	lease           time.Duration
	renewMu         sync.Mutex
	sequence        uint64
	terminal        atomic.Bool
}
type BrokerFacadeRun struct {
	client        *BrokerFacadeClient
	id, token     string
	profile       broker.BrokerProfileCapability
	lease         time.Duration
	mu            sync.Mutex
	phase         string
	sessions      map[string]*BrokerFacadeSession
	renewMu       sync.Mutex
	renewSequence uint64
}
type BrokerFacadeSession struct {
	run                     *BrokerFacadeRun
	id, host, token         string
	lease                   time.Duration
	invokeMu, renewMu       sync.Mutex
	sequence, renewSequence uint64
	terminal                atomic.Bool
	ending                  atomic.Bool
}

func NewBrokerFacadeClient(endpoint, token string, timeout time.Duration) (*BrokerFacadeClient, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Port() == "" || len(token) < 32 || len(token) > 4096 || timeout < time.Millisecond || timeout > MaximumExecutionTimeout {
		return nil, ErrFacadeState
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 || strings.TrimSpace(token) != token {
		return nil, ErrFacadeState
	}
	transport := &http.Transport{MaxIdleConnsPerHost: 16, MaxResponseHeaderBytes: 8 << 10, ResponseHeaderTimeout: timeout + 10*time.Second, IdleConnTimeout: 10 * time.Second}
	return &BrokerFacadeClient{endpoint: strings.TrimRight(endpoint, "/") + BrokerFacadePath, token: token, http: &http.Client{Transport: transport, Timeout: timeout + 10*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *BrokerFacadeClient) Close() { c.terminal.Store(true); c.http.CloseIdleConnections() }
func (c *BrokerFacadeClient) LeaseInterval() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lease
}
func (c *BrokerFacadeClient) authority(op string, cleanup bool) (BrokerFacadeRequest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner == "" || !cleanup && c.terminal.Load() {
		return BrokerFacadeRequest{}, ErrFacadeOwner
	}
	return BrokerFacadeRequest{Operation: op, OwnerToken: c.owner}, nil
}
func (c *BrokerFacadeClient) Capabilities(ctx context.Context) (broker.BrokerCapabilities, error) {
	if c.terminal.Load() {
		return broker.BrokerCapabilities{}, ErrFacadeOwner
	}
	out, e := c.exchange(ctx, BrokerFacadeRequest{Operation: "capabilities"})
	if e != nil {
		return broker.BrokerCapabilities{}, e
	}
	if out.Capabilities == nil || out.Capabilities.Validate() != nil {
		c.terminal.Store(true)
		return broker.BrokerCapabilities{}, ErrFacadeTransport
	}
	return *out.Capabilities, nil
}
func (c *BrokerFacadeClient) Claim(ctx context.Context) error {
	c.mu.Lock()
	if c.attempted || c.terminal.Load() {
		c.mu.Unlock()
		return ErrFacadeOwner
	}
	c.attempted = true
	c.mu.Unlock()
	out, e := c.exchange(ctx, BrokerFacadeRequest{Operation: "claim"})
	if e != nil {
		c.terminal.Store(true)
		return e
	}
	if !executionIDPattern.MatchString(out.OwnerToken) || out.LeaseMS < 1000 || out.LeaseMS > 60000 {
		c.terminal.Store(true)
		return ErrFacadeTransport
	}
	c.mu.Lock()
	c.owner = out.OwnerToken
	c.lease = time.Duration(out.LeaseMS) * time.Millisecond
	c.mu.Unlock()
	return nil
}
func (c *BrokerFacadeClient) Renew(ctx context.Context) error {
	if !c.renewMu.TryLock() {
		return ErrFacadeState
	}
	defer c.renewMu.Unlock()
	q, e := c.authority("renew_owner", false)
	if e != nil {
		return e
	}
	if c.sequence == ^uint64(0) {
		return ErrFacadeState
	}
	c.sequence++
	q.Sequence = c.sequence
	out, e := c.exchange(ctx, q)
	if e != nil {
		c.terminal.Store(true)
		return e
	}
	if out.Sequence != q.Sequence || out.LeaseMS != c.LeaseInterval().Milliseconds() {
		c.terminal.Store(true)
		return ErrFacadeTransport
	}
	return nil
}
func (c *BrokerFacadeClient) Release(ctx context.Context) error {
	q, e := c.authority("release", true)
	if e != nil {
		return e
	}
	c.terminal.Store(true)
	out, e := c.exchange(ctx, q)
	if e != nil {
		return e
	}
	if out.State != "retiring" {
		return ErrFacadeTransport
	}
	return nil
}
func (c *BrokerFacadeClient) Cleanup(ctx context.Context) (bool, error) {
	q, e := c.authority("cleanup", true)
	if e != nil {
		return false, e
	}
	out, e := c.exchange(ctx, q)
	if e != nil {
		return false, e
	}
	if out.State != "retiring" && out.State != "reaped" {
		return false, ErrFacadeTransport
	}
	return out.State == "reaped", nil
}
func (c *BrokerFacadeClient) Reserve(ctx context.Context, execution, group string, profile broker.BrokerProfileCapability, duration time.Duration, sessions []javarunner.DedicatedHostContext) (*BrokerFacadeRun, error) {
	q, e := c.authority("reserve", false)
	if e != nil {
		return nil, e
	}
	if duration < time.Millisecond || duration.Milliseconds() > profile.MaxRunMS {
		return nil, ErrFacadeState
	}
	q.ExecutionID = execution
	q.GroupID = group
	q.Profile = profile.ID
	q.ConfigurationSHA256 = profile.ConfigurationSHA256
	q.PolicySHA256 = profile.PolicySHA256
	q.RunDurationMS = duration.Milliseconds()
	q.Sessions = append([]javarunner.DedicatedHostContext(nil), sessions...)
	out, e := c.exchange(ctx, q)
	if e != nil {
		return nil, e
	}
	if !executionIDPattern.MatchString(out.RunID) || !executionIDPattern.MatchString(out.RunToken) || out.State != "preparing" || out.LeaseMS != profile.HostLeaseMS {
		c.terminal.Store(true)
		return nil, ErrFacadeTransport
	}
	run := &BrokerFacadeRun{client: c, id: out.RunID, token: out.RunToken, profile: profile, phase: "preparing", lease: time.Duration(out.LeaseMS) * time.Millisecond, sessions: map[string]*BrokerFacadeSession{}}
	for _, s := range sessions {
		run.sessions[s.SessionID] = nil
	}
	return run, nil
}
func (r *BrokerFacadeRun) authority(op string, cleanup bool) (BrokerFacadeRequest, error) {
	q, e := r.client.authority(op, cleanup)
	q.RunID = r.id
	q.RunToken = r.token
	return q, e
}
func (r *BrokerFacadeRun) LeaseInterval() time.Duration            { return r.lease }
func (r *BrokerFacadeRun) Profile() broker.BrokerProfileCapability { return r.profile }
func (r *BrokerFacadeRun) Ready(ctx context.Context) (bool, error) {
	r.mu.Lock()
	allowed := r.phase == "preparing" || r.phase == "ready"
	r.mu.Unlock()
	if !allowed {
		return false, ErrFacadeState
	}
	q, e := r.authority("ready", false)
	if e != nil {
		return false, e
	}
	out, e := r.client.exchange(ctx, q)
	if e != nil {
		return false, e
	}
	if out.RunID != r.id || out.State != "ready" && out.State != "preparing" {
		r.client.terminal.Store(true)
		return false, ErrFacadeTransport
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != "preparing" && r.phase != "ready" {
		return false, ErrFacadeState
	}
	if out.State == "ready" {
		r.phase = "ready"
	}
	return r.phase == "ready", nil
}
func (r *BrokerFacadeRun) Commit(ctx context.Context) error {
	r.mu.Lock()
	if r.phase != "ready" {
		r.mu.Unlock()
		return ErrFacadeState
	}
	r.phase = "committing"
	r.mu.Unlock()
	q, e := r.authority("commit", false)
	if e != nil {
		return e
	}
	out, e := r.client.exchange(ctx, q)
	r.mu.Lock()
	defer r.mu.Unlock()
	if e != nil {
		r.phase = "retiring"
		return e
	}
	if out.RunID != r.id || out.State != "committed" {
		r.client.terminal.Store(true)
		r.phase = "retiring"
		return ErrFacadeTransport
	}
	if r.phase != "committing" || r.client.terminal.Load() {
		return ErrFacadeLease
	}
	r.phase = "committed"
	return nil
}
func (r *BrokerFacadeRun) Renew(ctx context.Context) error {
	if !r.renewMu.TryLock() {
		return ErrFacadeState
	}
	defer r.renewMu.Unlock()
	q, e := r.authority("renew_run", false)
	if e != nil {
		return e
	}
	if r.renewSequence == ^uint64(0) {
		return ErrFacadeState
	}
	r.renewSequence++
	q.Sequence = r.renewSequence
	out, e := r.client.exchange(ctx, q)
	if e != nil {
		return e
	}
	if out.RunID != r.id || out.Sequence != q.Sequence || out.LeaseMS != r.lease.Milliseconds() {
		r.client.terminal.Store(true)
		return ErrFacadeTransport
	}
	return nil
}
func (r *BrokerFacadeRun) Abort(ctx context.Context) error {
	r.mu.Lock()
	r.phase = "retiring"
	r.mu.Unlock()
	q, e := r.authority("abort", true)
	if e != nil {
		return e
	}
	out, e := r.client.exchange(ctx, q)
	if e != nil {
		return e
	}
	if out.RunID != r.id || out.State != "retiring" {
		return ErrFacadeTransport
	}
	return nil
}
func (r *BrokerFacadeRun) Activate(ctx context.Context, id string) (*BrokerFacadeSession, error) {
	// Activation belongs to staging, before the ramp clock starts. Complete the
	// pending-host renewal lane before transferring this host's lease authority.
	r.renewMu.Lock()
	defer r.renewMu.Unlock()

	r.mu.Lock()
	_, known := r.sessions[id]
	if !known || r.sessions[id] != nil || r.phase != "committed" {
		r.mu.Unlock()
		return nil, ErrFacadeState
	}
	// A placeholder makes activation single-attempt, including lost replies.
	session := &BrokerFacadeSession{run: r, id: id, lease: r.lease}
	session.terminal.Store(true)
	r.sessions[id] = session
	r.mu.Unlock()
	q, e := r.authority("activate", false)
	if e != nil {
		return nil, e
	}
	q.SessionID = id
	out, e := r.client.exchange(ctx, q)
	if e != nil {
		return nil, e
	}
	if out.RunID != r.id || out.SessionID != id || !executionIDPattern.MatchString(out.HostID) || !executionIDPattern.MatchString(out.SessionToken) || out.LeaseMS != r.lease.Milliseconds() {
		r.client.terminal.Store(true)
		return nil, ErrFacadeTransport
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phase != "committed" || r.client.terminal.Load() {
		return nil, ErrFacadeLease
	}
	session.host = out.HostID
	session.token = out.SessionToken
	session.terminal.Store(false)
	return session, nil
}

// RunID identifies the placement reservation without exposing its authority token.
func (s *BrokerFacadeSession) RunID() string                           { return s.run.id }
func (s *BrokerFacadeSession) Profile() broker.BrokerProfileCapability { return s.run.profile }
func (s *BrokerFacadeSession) HostID() string                          { return s.host }
func (s *BrokerFacadeSession) SessionID() string                       { return s.id }
func (s *BrokerFacadeSession) LeaseInterval() time.Duration            { return s.lease }
func (s *BrokerFacadeSession) Err() error {
	if s.terminal.Load() || s.run.client.terminal.Load() {
		return ErrFacadeLease
	}
	s.run.mu.Lock()
	active := s.run.phase == "committed"
	s.run.mu.Unlock()
	if !active {
		return ErrFacadeLease
	}
	return nil
}
func (s *BrokerFacadeSession) authority(op string, cleanup bool) (BrokerFacadeRequest, error) {
	q, e := s.run.authority(op, cleanup)
	q.SessionID = s.id
	q.SessionToken = s.token
	return q, e
}
func (s *BrokerFacadeSession) Renew(ctx context.Context) error {
	if !s.renewMu.TryLock() {
		return ErrFacadeState
	}
	defer s.renewMu.Unlock()
	if e := s.Err(); e != nil {
		return e
	}
	q, e := s.authority("renew_session", false)
	if e != nil {
		return e
	}
	if s.renewSequence == ^uint64(0) {
		return ErrFacadeState
	}
	s.renewSequence++
	q.Sequence = s.renewSequence
	out, e := s.run.client.exchange(ctx, q)
	if e != nil {
		if !s.ending.Load() {
			s.terminal.Store(true)
		}
		return e
	}
	if out.RunID != s.run.id || out.SessionID != s.id || out.HostID != s.host || out.Sequence != q.Sequence || out.LeaseMS != s.lease.Milliseconds() {
		s.terminal.Store(true)
		return ErrFacadeTransport
	}
	return nil
}
func (s *BrokerFacadeSession) Close(ctx context.Context) error {
	s.terminal.Store(true)
	q, e := s.authority("close_session", true)
	if e != nil {
		return e
	}
	out, e := s.run.client.exchange(ctx, q)
	if e != nil {
		return e
	}
	if out.RunID != s.run.id || out.SessionID != s.id || out.HostID != s.host || out.State != "retiring" {
		return ErrFacadeTransport
	}
	return nil
}
func (s *BrokerFacadeSession) Execute(ctx context.Context, method string, params map[string]string) (ExecutionResponse, error) {
	if !s.invokeMu.TryLock() {
		return ExecutionResponse{}, ErrFacadeState
	}
	defer s.invokeMu.Unlock()
	if e := s.Err(); e != nil {
		return ExecutionResponse{}, e
	}
	if !validFacadeParameters(params) || method != "init" && method != "action" && method != "end" || s.sequence == ^uint64(0) {
		return ExecutionResponse{}, ErrFacadeState
	}
	q, e := s.authority("invoke", false)
	if e != nil {
		return ExecutionResponse{}, e
	}
	if method == "end" {
		s.ending.Store(true)
	}
	s.sequence++
	q.Sequence = s.sequence
	q.Method = method
	q.Parameters = make(map[string]string, len(params))
	for k, v := range params {
		q.Parameters[k] = v
	}
	out, e := s.run.client.exchange(ctx, q)
	if e != nil {
		s.terminal.Store(true)
		return ExecutionResponse{}, e
	}
	result := out.Result
	if out.RunID != s.run.id || out.SessionID != s.id || out.HostID != s.host || out.Sequence != q.Sequence || result == nil || !validFacadeResult(result) || s.Err() != nil {
		s.terminal.Store(true)
		return ExecutionResponse{}, ErrFacadeTransport
	}
	if method == "end" {
		s.terminal.Store(true)
	}
	return *result, nil
}
func validFacadeParameters(params map[string]string) bool {
	if len(params) > 256 {
		return false
	}
	size := 0
	for k, v := range params {
		if len(k) < 1 || len(k) > 126 || len(v) > 65536 || !utf8.ValidString(k) || !utf8.ValidString(v) {
			return false
		}
		size += len(k) + len(v)
	}
	return size <= 256<<10
}
func (c *BrokerFacadeClient) exchange(ctx context.Context, q BrokerFacadeRequest) (BrokerFacadeResponse, error) {
	var out BrokerFacadeResponse
	id, e := facadeID()
	if e != nil {
		return out, e
	}
	q.Contract = BrokerFacadeContract
	q.RequestID = id
	if !validFacadeRequest(q) {
		return out, ErrFacadeState
	}
	data, e := json.Marshal(q)
	if e != nil || len(data) > 512<<10 {
		return out, ErrFacadeState
	}
	if q.Operation != "invoke" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if e != nil {
		return out, ErrFacadeState
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	response, e := c.http.Do(req)
	if e != nil {
		// A caller-cancelled request with a known reservation is contained by
		// that run/session. Claim/reserve have unknown authority on lost replies
		// and still fence the owner. Malformed responses below always fence it.
		if ctx.Err() == nil || q.RunID == "" {
			c.terminal.Store(true)
		}
		return out, ErrFacadeTransport
	}
	defer response.Body.Close()
	data, e = io.ReadAll(io.LimitReader(response.Body, broker.MaxSupervisorResponse+1))
	if e != nil || len(data) > broker.MaxSupervisorResponse {
		if e == nil || ctx.Err() == nil || q.RunID == "" {
			c.terminal.Store(true)
		}
		return out, ErrFacadeTransport
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || out.Contract != BrokerFacadeContract || out.RequestID != q.RequestID {
		c.terminal.Store(true)
		return BrokerFacadeResponse{}, ErrFacadeTransport
	}
	if out.Failure != "" {
		base := BrokerFacadeResponse{Contract: out.Contract, RequestID: out.RequestID, Failure: out.Failure}
		a, _ := json.Marshal(base)
		b, _ := json.Marshal(out)
		if response.StatusCode != http.StatusConflict || !bytes.Equal(a, b) {
			c.terminal.Store(true)
			return BrokerFacadeResponse{}, ErrFacadeTransport
		}
		switch out.Failure {
		case "java_facade_operation_failed", "java_facade_state_invalid", "java_facade_lease_fenced", "java_facade_capacity_unavailable":
		case "java_facade_owner_fenced":
			c.terminal.Store(true)
		default:
			c.terminal.Store(true)
			return BrokerFacadeResponse{}, ErrFacadeTransport
		}
		return out, &FacadeFailure{Code: out.Failure}
	}
	if response.StatusCode != http.StatusOK || !validFacadeResponseShape(q.Operation, out) {
		c.terminal.Store(true)
		return BrokerFacadeResponse{}, ErrFacadeTransport
	}
	return out, nil
}
func validFacadeResponseShape(op string, out BrokerFacadeResponse) bool {
	base := BrokerFacadeResponse{Contract: out.Contract, RequestID: out.RequestID}
	switch op {
	case "capabilities":
		base.Capabilities = out.Capabilities
	case "claim":
		base.OwnerToken = out.OwnerToken
		base.LeaseMS = out.LeaseMS
	case "renew_owner":
		base.Sequence = out.Sequence
		base.LeaseMS = out.LeaseMS
	case "release", "cleanup":
		base.State = out.State
	case "reserve":
		base.RunID = out.RunID
		base.RunToken = out.RunToken
		base.State = out.State
		base.LeaseMS = out.LeaseMS
	case "ready", "commit", "abort":
		base.RunID = out.RunID
		base.State = out.State
	case "renew_run":
		base.RunID = out.RunID
		base.Sequence = out.Sequence
		base.LeaseMS = out.LeaseMS
	case "activate", "renew_session", "invoke", "close_session":
		base.RunID = out.RunID
		base.SessionID = out.SessionID
		base.HostID = out.HostID
		if op == "activate" {
			base.SessionToken = out.SessionToken
			base.LeaseMS = out.LeaseMS
		}
		if op == "renew_session" {
			base.Sequence = out.Sequence
			base.LeaseMS = out.LeaseMS
		}
		if op == "invoke" {
			base.Sequence = out.Sequence
			base.Result = out.Result
		}
		if op == "close_session" {
			base.State = out.State
		}
	default:
		return false
	}
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(out)
	return bytes.Equal(a, b)
}

func validFacadeResult(out *ExecutionResponse) bool {
	if out == nil || out.Contract != ContractVersion || !executionIDPattern.MatchString(out.ExecutionID) || out.DurationMS < 0 || out.DurationMS > MaximumExecutionTimeout.Milliseconds()+10000 || len(out.WarningCodes) != 0 || (out.Success && (out.ErrorCode != "" || out.Error != "")) || (!out.Success && out.ErrorCode != "harness_reported_failure") {
		return false
	}
	for _, text := range []string{out.Stdout, out.Stderr, out.Events, out.Error} {
		if len(text) > MaxCaptureBytes || !utf8.ValidString(text) {
			return false
		}
	}
	return true
}
