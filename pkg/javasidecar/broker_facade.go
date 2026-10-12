package javasidecar

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

const BrokerFacadeContract = "apexload.java-facade/v1"
const BrokerFacadePath = "/v1/java-facade"
const maxFacadeRecords = 10000

var ErrFacadeState = errors.New("java_facade_state_invalid")
var ErrFacadeOwner = errors.New("java_facade_owner_fenced")
var ErrFacadeLease = errors.New("java_facade_lease_fenced")

type BrokerFacadeRequest struct {
	Contract            string                            `json:"contract"`
	RequestID           string                            `json:"request_id"`
	Operation           string                            `json:"operation"`
	OwnerToken          string                            `json:"owner_token,omitempty"`
	RunID               string                            `json:"run_id,omitempty"`
	RunToken            string                            `json:"run_token,omitempty"`
	SessionID           string                            `json:"session_id,omitempty"`
	SessionToken        string                            `json:"session_token,omitempty"`
	Sequence            uint64                            `json:"sequence,omitempty"`
	Profile             string                            `json:"profile,omitempty"`
	ConfigurationSHA256 string                            `json:"configuration_sha256,omitempty"`
	PolicySHA256        string                            `json:"policy_sha256,omitempty"`
	ExecutionID         string                            `json:"execution_id,omitempty"`
	GroupID             string                            `json:"group_id,omitempty"`
	RunDurationMS       int64                             `json:"run_duration_ms,omitempty"`
	Sessions            []javarunner.DedicatedHostContext `json:"sessions,omitempty"`
	Method              string                            `json:"method,omitempty"`
	Parameters          map[string]string                 `json:"parameters,omitempty"`
}
type BrokerFacadeResponse struct {
	NativeData   bool                       `json:"native_data,omitempty"`
	Contract     string                     `json:"contract"`
	RequestID    string                     `json:"request_id"`
	Failure      string                     `json:"failure,omitempty"`
	OwnerToken   string                     `json:"owner_token,omitempty"`
	RunID        string                     `json:"run_id,omitempty"`
	RunToken     string                     `json:"run_token,omitempty"`
	SessionID    string                     `json:"session_id,omitempty"`
	SessionToken string                     `json:"session_token,omitempty"`
	HostID       string                     `json:"host_id,omitempty"`
	Sequence     uint64                     `json:"sequence,omitempty"`
	State        string                     `json:"state,omitempty"`
	LeaseMS      int64                      `json:"lease_ms,omitempty"`
	Capabilities *broker.BrokerCapabilities `json:"capabilities,omitempty"`
	Result       *ExecutionResponse         `json:"result,omitempty"`
}

type facadeRun struct {
	wave                                   *broker.BrokerClientWave
	token                                  string
	phase                                  string
	leaseUntil, prepareUntil, executeUntil time.Time
	duration, lease                        time.Duration
	renewSequence                          uint64
	renewBusy                              bool
	sessions                               map[string]*facadeRuntimeLease
	retire                                 sync.Once
}
type facadeRuntimeLease struct {
	session                         *DedicatedBrokerSession
	token                           string
	leaseUntil                      time.Time
	active, closed, busy, renewBusy bool
	sequence, renewSequence         uint64
	retire                          sync.Once
	ending                          bool
}

// BrokerFacade is a credential-minimized loopback control plane. The broker
// client holds transport credentials; the generator sees only local bearer and
// scoped lease tokens. Preparation, run authority and individual Vuser authority
// are distinct. Nothing here renews authority in the absence of an upstream call.
// A process owns one generator epoch and never adopts/replays Java on restart.
type BrokerFacade struct {
	closing                               bool
	calls                                 sync.WaitGroup
	client                                *broker.BrokerClient
	token                                 string
	caps                                  broker.BrokerCapabilities
	mu                                    sync.Mutex
	claimed, claiming, retired, renewBusy bool
	ownerToken                            string
	ownerUntil                            time.Time
	ownerSequence                         uint64
	runs                                  map[string]*facadeRun
	ctx                                   context.Context
	cancel                                context.CancelFunc
	done                                  chan struct{}
	cleanup                               sync.WaitGroup
	ownerRetire                           sync.Once
}

func NewBrokerFacade(ctx context.Context, c *broker.BrokerClient, token string) (*BrokerFacade, error) {
	if ctx == nil || c == nil || len(token) < 32 || len(token) > 4096 || strings.TrimSpace(token) != token {
		return nil, ErrFacadeState
	}
	caps, e := c.Capabilities(ctx)
	if e != nil {
		return nil, e
	}
	for _, p := range caps.Profiles {
		if p.HostLeaseMS < 300 {
			return nil, ErrFacadeState
		}
	}
	lifetime, cancel := context.WithCancel(ctx)
	f := &BrokerFacade{client: c, token: token, caps: caps, runs: map[string]*facadeRun{}, ctx: lifetime, cancel: cancel, done: make(chan struct{})}
	go f.watch()
	return f, nil
}
func facadeID() (string, error) {
	var nonce [24]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(nonce[:]), nil
}
func tokenEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (f *BrokerFacade) Close() {
	f.mu.Lock()
	f.closing = true
	f.mu.Unlock()
	f.cancel()
	<-f.done
	f.calls.Wait()
	f.cleanup.Wait()
	f.client.Close()
}

func (f *BrokerFacade) watch() {
	defer close(f.done)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-f.ctx.Done():
			f.expire(true)
			return
		case <-ticker.C:
			f.expire(false)
		}
	}
}
func (f *BrokerFacade) expire(all bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if all || f.claimed && !now.Before(f.ownerUntil) {
		f.retireOwnerLocked()
	}
	for _, r := range f.runs {
		if f.retired || !now.Before(r.leaseUntil) || r.phase != "committed" && !now.Before(r.prepareUntil) || r.phase == "committed" && !now.Before(r.executeUntil) {
			f.retireRunLocked(r)
			continue
		}
		for _, s := range r.sessions {
			if s.active && !s.closed && !now.Before(s.leaseUntil) {
				f.retireSessionLocked(s)
			}
		}
	}
}
func (f *BrokerFacade) retireOwnerLocked() {
	f.retired = true
	if f.claimed {
		f.ownerRetire.Do(func() {
			f.cleanup.Add(1)
			go func() {
				defer f.cleanup.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = f.client.Release(ctx)
			}()
		})
	}
}
func (f *BrokerFacade) retireRunLocked(r *facadeRun) {
	r.phase = "retiring"
	if r.wave == nil {
		return
	}
	r.retire.Do(func() {
		f.cleanup.Add(1)
		go func() {
			defer f.cleanup.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = r.wave.Abort(ctx)
		}()
	})
}
func (f *BrokerFacade) retireSessionLocked(s *facadeRuntimeLease) {
	s.closed = true
	s.retire.Do(func() {
		f.cleanup.Add(1)
		go func() {
			defer f.cleanup.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.session.Close(ctx)
		}()
	})
}
func (f *BrokerFacade) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if f.closing {
		f.mu.Unlock()
		http.Error(w, "java_facade_closed", 503)
		return
	}
	f.calls.Add(1)
	f.mu.Unlock()
	defer f.calls.Done()
	w.Header().Set("Content-Type", "application/json")
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || r.Header.Get("Origin") != "" || !tokenEqual(r.Header.Get("Authorization"), "Bearer "+f.token) {
		http.Error(w, "java_facade_unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path != BrokerFacadePath || r.URL.RawQuery != "" || r.Method != http.MethodPost {
		http.Error(w, "java_facade_request_invalid", http.StatusNotFound)
		return
	}

	if javarunner.ServeNativeHTTP(w, r, broker.MaxSupervisorResponse, f.ServeHTTP) {
		return
	}
	var q BrokerFacadeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&q) != nil || dec.Decode(new(any)) != io.EOF || !validFacadeRequest(q) {
		http.Error(w, "java_facade_request_invalid", http.StatusBadRequest)
		return
	}
	out := BrokerFacadeResponse{Contract: BrokerFacadeContract, RequestID: q.RequestID}
	e = f.dispatch(r.Context(), q, &out)
	status := http.StatusOK
	if e != nil {
		code := "java_facade_operation_failed"
		if errors.Is(e, ErrFacadeState) {
			code = "java_facade_state_invalid"
		}
		if errors.Is(e, ErrFacadeOwner) {
			code = "java_facade_owner_fenced"
		}
		if errors.Is(e, ErrFacadeLease) {
			code = "java_facade_lease_fenced"
		}
		var failure *broker.BrokerFailure
		if errors.As(e, &failure) && failure.Code == "java_broker_capacity_unavailable" {
			code = "java_facade_capacity_unavailable"
		}
		out = BrokerFacadeResponse{Contract: BrokerFacadeContract, RequestID: q.RequestID, Failure: code}
		status = http.StatusConflict
	}
	data, e := json.Marshal(out)
	if e != nil || len(data) > broker.MaxSupervisorResponse {
		http.Error(w, "java_facade_response_invalid", 500)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
func validFacadeRequest(q BrokerFacadeRequest) bool {
	if q.Contract != BrokerFacadeContract || !executionIDPattern.MatchString(q.RequestID) {
		return false
	}
	base := BrokerFacadeRequest{Contract: q.Contract, RequestID: q.RequestID, Operation: q.Operation}
	if q.Operation != "claim" && q.Operation != "capabilities" {
		if !executionIDPattern.MatchString(q.OwnerToken) {
			return false
		}
		base.OwnerToken = q.OwnerToken
	}
	switch q.Operation {
	case "claim", "capabilities", "release", "cleanup":
	case "renew_owner":
		base.Sequence = q.Sequence
		if q.Sequence == 0 {
			return false
		}
	case "reserve":
		base.Profile = q.Profile
		base.ConfigurationSHA256 = q.ConfigurationSHA256
		base.PolicySHA256 = q.PolicySHA256
		base.ExecutionID = q.ExecutionID
		base.GroupID = q.GroupID
		base.RunDurationMS = q.RunDurationMS
		base.Sessions = q.Sessions
		if !executionIDPattern.MatchString(q.Profile) || !executionIDPattern.MatchString(q.ExecutionID) || !executionIDPattern.MatchString(q.GroupID) || q.RunDurationMS < 1 || q.RunDurationMS > 86400000 || len(q.Sessions) < 1 || len(q.Sessions) > maxFacadeRecords {
			return false
		}
		seen := map[string]bool{}
		for _, s := range q.Sessions {
			if s.Validate() != nil || seen[s.SessionID] {
				return false
			}
			seen[s.SessionID] = true
		}
	case "ready", "commit", "abort", "renew_run", "activate", "renew_session", "invoke", "close_session":
		base.RunID = q.RunID
		base.RunToken = q.RunToken
		if !executionIDPattern.MatchString(q.RunID) || !executionIDPattern.MatchString(q.RunToken) {
			return false
		}
		if q.Operation == "renew_run" {
			base.Sequence = q.Sequence
			if q.Sequence == 0 {
				return false
			}
		}
		if q.Operation == "activate" || q.Operation == "renew_session" || q.Operation == "invoke" || q.Operation == "close_session" {
			base.SessionID = q.SessionID
			if !executionIDPattern.MatchString(q.SessionID) {
				return false
			}
			if q.Operation != "activate" {
				base.SessionToken = q.SessionToken
				if !executionIDPattern.MatchString(q.SessionToken) {
					return false
				}
			}
			if q.Operation == "renew_session" || q.Operation == "invoke" {
				base.Sequence = q.Sequence
				if q.Sequence == 0 {
					return false
				}
			}
			if q.Operation == "invoke" {
				base.Method = q.Method
				base.Parameters = q.Parameters
				if !validFacadeParameters(q.Parameters) || q.Method != "init" && q.Method != "action" && q.Method != "end" {
					return false
				}
			}
		}
	default:
		return false
	}
	a, _ := json.Marshal(q)
	b, _ := json.Marshal(base)
	return bytes.Equal(a, b)
}
func (f *BrokerFacade) ownerLocked(q BrokerFacadeRequest) error {
	if !f.claimed || f.retired || f.ctx.Err() != nil || !time.Now().Before(f.ownerUntil) || !tokenEqual(q.OwnerToken, f.ownerToken) {
		return ErrFacadeOwner
	}
	return nil
}
func (f *BrokerFacade) runLocked(q BrokerFacadeRequest) (*facadeRun, error) {
	if e := f.ownerLocked(q); e != nil {
		return nil, e
	}
	r := f.runs[q.RunID]
	if r == nil || !tokenEqual(q.RunToken, r.token) {
		return nil, ErrFacadeLease
	}
	now := time.Now()
	if r.phase == "retiring" || !now.Before(r.leaseUntil) || r.phase != "committed" && !now.Before(r.prepareUntil) || r.phase == "committed" && !now.Before(r.executeUntil) {
		f.retireRunLocked(r)
		return nil, ErrFacadeLease
	}
	return r, nil
}
func (f *BrokerFacade) dispatch(ctx context.Context, q BrokerFacadeRequest, out *BrokerFacadeResponse) error {
	if q.Operation == "capabilities" {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.retired {
			return ErrFacadeOwner
		}
		caps := f.caps
		out.Capabilities = &caps
		return nil
	}
	if q.Operation == "claim" {
		f.mu.Lock()
		if f.claimed || f.claiming || f.retired || f.ctx.Err() != nil {
			f.mu.Unlock()
			return ErrFacadeOwner
		}
		f.claiming = true
		f.mu.Unlock()
		token, e := facadeID()
		if e == nil {
			e = f.client.Claim(ctx)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.claiming = false
		if e != nil {
			f.retired = true
			return e
		}
		f.claimed = true
		f.ownerToken = token
		if f.retired || f.ctx.Err() != nil {
			f.retireOwnerLocked()
			return ErrFacadeOwner
		}
		f.ownerUntil = time.Now().Add(f.client.LeaseInterval())
		out.OwnerToken = token
		out.LeaseMS = f.client.LeaseInterval().Milliseconds()
		return nil
	}
	f.mu.Lock()
	if q.Operation == "cleanup" {
		valid := f.claimed && tokenEqual(q.OwnerToken, f.ownerToken)
		f.mu.Unlock()
		if !valid {
			return ErrFacadeOwner
		}
		done, e := f.client.Cleanup(ctx)
		if e != nil {
			return e
		}
		out.State = "retiring"
		if done {
			out.State = "reaped"
		}
		return nil
	}
	if e := f.ownerLocked(q); e != nil {
		f.mu.Unlock()
		return e
	}
	if q.Operation == "release" {
		f.retireOwnerLocked()
		for _, r := range f.runs {
			f.retireRunLocked(r)
		}
		f.mu.Unlock()
		out.State = "retiring"
		return nil
	}
	if q.Operation == "renew_owner" {
		if f.renewBusy || f.ownerSequence == ^uint64(0) || q.Sequence != f.ownerSequence+1 {
			f.mu.Unlock()
			return ErrFacadeState
		}
		f.ownerSequence = q.Sequence
		f.renewBusy = true
		f.mu.Unlock()
		e := f.client.Renew(ctx)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.renewBusy = false
		if e != nil || f.ownerLocked(q) != nil {
			f.retireOwnerLocked()
			return ErrFacadeOwner
		}
		f.ownerUntil = time.Now().Add(f.client.LeaseInterval())
		out.Sequence = q.Sequence
		out.LeaseMS = f.client.LeaseInterval().Milliseconds()
		return nil
	}
	if q.Operation == "reserve" {
		f.mu.Unlock()
		return f.reserve(ctx, q, out)
	}
	r, e := f.runLocked(q)
	if e != nil {
		f.mu.Unlock()
		return e
	}
	out.RunID = q.RunID
	switch q.Operation {
	case "abort":
		f.retireRunLocked(r)
		f.mu.Unlock()
		out.State = "retiring"
		return nil
	case "ready":
		if r.phase != "preparing" {
			f.mu.Unlock()
			return ErrFacadeState
		}
		f.mu.Unlock()
		ready, e := r.wave.Ready(ctx)
		if e != nil {
			return e
		}
		out.State = "preparing"
		if ready {
			out.State = "ready"
			out.NativeData = r.wave.NativeDataAvailable()
		}
		return nil
	case "commit":
		if r.phase != "preparing" {
			f.mu.Unlock()
			return ErrFacadeState
		}
		r.phase = "committing"
		f.mu.Unlock()
		e := r.wave.Commit(ctx)
		f.mu.Lock()
		defer f.mu.Unlock()
		if e != nil {
			f.retireRunLocked(r)
			return e
		}
		if _, e := f.runLocked(q); e != nil {
			return e
		}
		r.phase = "committed"
		r.executeUntil = time.Now().Add(r.duration)
		out.State = "committed"
		return nil
	case "renew_run":
		if r.renewBusy || r.renewSequence == ^uint64(0) || q.Sequence != r.renewSequence+1 {
			f.mu.Unlock()
			return ErrFacadeState
		}
		r.renewSequence = q.Sequence
		r.renewBusy = true
		pending := []*facadeRuntimeLease{}
		for _, s := range r.sessions {
			if !s.active && !s.closed {
				pending = append(pending, s)
			}
		}
		f.mu.Unlock()
		e := renewPendingSessions(ctx, pending)
		f.mu.Lock()
		defer f.mu.Unlock()
		r.renewBusy = false
		if e != nil {
			f.retireRunLocked(r)
			return e
		}
		if _, e = f.runLocked(q); e != nil {
			return e
		}
		r.leaseUntil = time.Now().Add(r.lease)
		out.LeaseMS = r.lease.Milliseconds()
		out.Sequence = q.Sequence
		return nil
	}
	s := r.sessions[q.SessionID]
	if r.phase != "committed" || s == nil {
		f.mu.Unlock()
		return ErrFacadeLease
	}
	out.SessionID = q.SessionID
	out.HostID = s.session.HostID()
	if q.Operation == "close_session" && s.active && tokenEqual(q.SessionToken, s.token) {
		f.retireSessionLocked(s)
		f.mu.Unlock()
		out.State = "retiring"
		return nil
	}
	if s.closed {
		f.mu.Unlock()
		return ErrFacadeLease
	}

	if q.Operation == "activate" {
		if s.active || r.renewBusy {
			f.mu.Unlock()
			return ErrFacadeState
		}
		token, e := facadeID()
		if e != nil {
			f.mu.Unlock()
			return e
		}
		s.active = true
		s.token = token
		s.leaseUntil = time.Now().Add(r.lease)
		f.mu.Unlock()
		if e := s.session.Renew(ctx); e != nil {
			f.mu.Lock()
			f.retireSessionLocked(s)
			f.mu.Unlock()
			return e
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, e := f.runLocked(q); e != nil {
			f.retireSessionLocked(s)
			return e
		}
		if s.closed || !time.Now().Before(s.leaseUntil) {
			f.retireSessionLocked(s)
			return ErrFacadeLease
		}
		out.SessionToken = token
		out.LeaseMS = r.lease.Milliseconds()
		return nil
	}
	if !s.active || !time.Now().Before(s.leaseUntil) || !tokenEqual(q.SessionToken, s.token) {
		f.mu.Unlock()
		return ErrFacadeLease
	}
	switch q.Operation {
	case "close_session":
		f.retireSessionLocked(s)
		f.mu.Unlock()
		out.State = "retiring"
		return nil
	case "renew_session":
		if s.renewBusy || s.renewSequence == ^uint64(0) || q.Sequence != s.renewSequence+1 {
			f.mu.Unlock()
			return ErrFacadeState
		}
		s.renewBusy = true
		s.renewSequence = q.Sequence
		f.mu.Unlock()
		e := s.session.Renew(ctx)
		f.mu.Lock()
		defer f.mu.Unlock()
		s.renewBusy = false
		if e != nil && s.ending && !s.closed && time.Now().Before(s.leaseUntil) {
			return ErrFacadeLease
		}
		if e != nil || s.closed || !time.Now().Before(s.leaseUntil) {
			f.retireSessionLocked(s)
			return ErrFacadeLease
		}
		if _, e = f.runLocked(q); e != nil {
			return e
		}
		s.leaseUntil = time.Now().Add(r.lease)
		out.LeaseMS = r.lease.Milliseconds()
		out.Sequence = q.Sequence
		return nil
	case "invoke":
		if s.busy || s.sequence == ^uint64(0) || q.Sequence != s.sequence+1 {
			f.mu.Unlock()
			return ErrFacadeState
		}
		s.busy = true
		s.ending = q.Method == "end"
		s.sequence = q.Sequence
		f.mu.Unlock()
		result, e := s.session.Execute(ctx, q.Method, q.Parameters)
		f.mu.Lock()
		defer f.mu.Unlock()
		s.busy = false
		if e != nil {
			f.retireSessionLocked(s)
			return e
		}
		if s.closed || !time.Now().Before(s.leaseUntil) {
			f.retireSessionLocked(s)
			return ErrFacadeLease
		}
		if _, e = f.runLocked(q); e != nil {
			return e
		}
		if q.Method == "end" {
			s.closed = true
		}
		out.Result = &result
		out.Sequence = q.Sequence
		return nil
	}
	f.mu.Unlock()
	return ErrFacadeState
}
func (f *BrokerFacade) reserve(ctx context.Context, q BrokerFacadeRequest, out *BrokerFacadeResponse) error {
	var profile broker.BrokerProfileCapability
	for _, p := range f.caps.Profiles {
		if p.ID == q.Profile {
			profile = p
		}
	}
	if profile.ID == "" || profile.ConfigurationSHA256 != q.ConfigurationSHA256 || profile.PolicySHA256 != q.PolicySHA256 || q.RunDurationMS > profile.MaxRunMS {
		return ErrFacadeState
	}
	id, e := facadeID()
	if e != nil {
		return e
	}
	token, e := facadeID()
	if e != nil {
		return e
	}
	// Reserve the local inventory entry before the remote operation so concurrent
	// requests cannot race past its bound. A failed/ambiguous reservation remains
	// a consumed admission attempt; broker cleanup still owns unknown outcomes.
	f.mu.Lock()
	if e = f.ownerLocked(q); e != nil {
		f.mu.Unlock()
		return e
	}
	if len(f.runs) >= maxFacadeRecords {
		f.mu.Unlock()
		return ErrFacadeState
	}
	f.runs[id] = &facadeRun{phase: "reserving", token: token, leaseUntil: time.Now().Add(time.Duration(profile.PreparationMS) * time.Millisecond), prepareUntil: time.Now().Add(time.Duration(profile.PreparationMS) * time.Millisecond), sessions: map[string]*facadeRuntimeLease{}}
	f.mu.Unlock()
	wave, e := f.client.ReserveFor(ctx, q.ExecutionID, q.GroupID, profile, time.Duration(q.RunDurationMS)*time.Millisecond, q.Sessions)
	f.mu.Lock()
	r := f.runs[id]
	if e != nil {
		r.phase = "retiring"
		if errors.Is(e, broker.ErrBrokerTransport) || errors.Is(e, broker.ErrFence) {
			f.retireOwnerLocked()
		}
		f.mu.Unlock()
		return e
	}
	wasRetired := r.phase == "retiring" || !time.Now().Before(r.prepareUntil)
	r.wave = wave
	r.lease = wave.HostLeaseInterval()
	r.duration = time.Duration(q.RunDurationMS) * time.Millisecond
	for _, h := range wave.Hosts() {
		session, se := NewDedicatedBrokerSession(wave, h.HostID)
		if se != nil {
			e = se
			break
		}
		r.sessions[h.SessionID] = &facadeRuntimeLease{session: session}
	}
	r.phase = "preparing"
	r.leaseUntil = time.Now().Add(r.lease)
	if e == nil {
		e = f.ownerLocked(q)
	}
	if e != nil || wasRetired || f.ctx.Err() != nil {
		f.retireRunLocked(r)
		f.mu.Unlock()
		if e != nil {
			return e
		}
		return ErrFacadeOwner
	}
	f.mu.Unlock()
	out.RunID = id
	out.RunToken = token
	out.LeaseMS = r.lease.Milliseconds()
	out.State = "preparing"
	return nil
}
func renewPendingSessions(ctx context.Context, sessions []*facadeRuntimeLease) error {
	jobs := make(chan *facadeRuntimeLease, len(sessions))
	for _, s := range sessions {
		jobs <- s
	}
	close(jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failure error
	for range min(8, len(sessions)) {
		wg.Go(func() {
			for s := range jobs {
				if e := s.session.Renew(ctx); e != nil {
					mu.Lock()
					failure = errors.Join(failure, e)
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	return failure
}
