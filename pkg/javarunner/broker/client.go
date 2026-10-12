package broker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

var ErrBrokerTransport = errors.New("java_broker_transport_uncertain")

type BrokerFailure struct{ Code string }

func (e *BrokerFailure) Error() string { return e.Code }

type BrokerClientConfig struct {
	Endpoint, ServerURI, FacadeURI string
	InvocationTimeout              time.Duration
}

// BrokerClient owns one facade-process instance. It never retries an operation
// or automatically replaces an expired owner. Renew must be driven by genuine
// upstream authority, independently of invocation. Release/cleanup remain usable
// after uncertain transport; only positive cleanup is a termination receipt.
type BrokerClient struct {
	config                              BrokerClientConfig
	http                                *http.Client
	mu                                  sync.Mutex
	claimAttempted                      bool
	ownerEpoch, ownerToken, brokerEpoch string
	lease                               time.Duration
	waves                               map[string]*BrokerClientWave
	renewMu                             sync.Mutex
	renewSequence                       uint64
	terminal                            atomic.Bool
}
type BrokerClientWave struct {
	nativeData bool
	client     *BrokerClient
	id, digest string
	mu         sync.Mutex
	phase      string
	lease      time.Duration
	hosts      map[string]*brokerClientHost
}
type brokerClientHost struct {
	session        string
	sequence       uint64
	busy, terminal bool
	ending         bool
	renewSequence  uint64
	renewBusy      bool
}

func NewBrokerClient(cfg BrokerClientConfig, config *tls.Config) (*BrokerClient, error) {
	u, e := url.Parse(cfg.Endpoint)
	identity, ie := url.Parse(cfg.ServerURI)
	facade, fe := url.Parse(cfg.FacadeURI)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || ie != nil || !validBrokerURI(cfg.ServerURI) || (identity.Path != "/java-broker" && (!strings.HasPrefix(identity.Path, "/java-broker/") || !name.MatchString(strings.TrimPrefix(identity.Path, "/java-broker/")))) || fe != nil || !validBrokerURI(cfg.FacadeURI) || !strings.HasPrefix(facade.Path, "/java-facade/") || !name.MatchString(strings.TrimPrefix(facade.Path, "/java-facade/")) || cfg.InvocationTimeout < time.Millisecond || cfg.InvocationTimeout > time.Hour || config == nil || config.RootCAs == nil || config.InsecureSkipVerify || len(config.Certificates) != 1 || len(config.Certificates[0].Certificate) == 0 {
		return nil, ErrState
	}
	leaf, e := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if e != nil || len(leaf.URIs) != 1 || leaf.URIs[0].String() != cfg.FacadeURI {
		return nil, ErrState
	}
	tc := config.Clone()
	tc.RootCAs = config.RootCAs.Clone()
	tc.MinVersion = max(tc.MinVersion, tls.VersionTLS13)
	verify := tc.VerifyConnection
	tc.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
			return ErrBrokerTransport
		}
		uris := state.VerifiedChains[0][0].URIs
		if len(uris) != 1 || uris[0].String() != cfg.ServerURI {
			return ErrBrokerTransport
		}
		if verify != nil {
			return verify(state)
		}
		return nil
	}
	transport := &http.Transport{TLSClientConfig: tc, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: cfg.InvocationTimeout + 10*time.Second, IdleConnTimeout: 10 * time.Second, MaxIdleConnsPerHost: 8, MaxResponseHeaderBytes: 8 << 10}
	return &BrokerClient{config: cfg, http: &http.Client{Transport: transport, Timeout: cfg.InvocationTimeout + 10*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, waves: map[string]*BrokerClientWave{}}, nil
}
func (c *BrokerClient) Close() { c.terminal.Store(true); c.http.CloseIdleConnections() }
func (c *BrokerClient) LeaseInterval() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lease
}
func (c *BrokerClient) Claim(ctx context.Context) error {
	c.mu.Lock()
	if c.claimAttempted || c.terminal.Load() {
		c.mu.Unlock()
		return ErrState
	}
	c.claimAttempted = true
	c.mu.Unlock()
	instance, e := randomID()
	if e != nil {
		return e
	}
	out, e := c.exchange(ctx, BrokerRequest{Operation: "claim", InstanceID: instance}, true)
	if e != nil {
		c.terminal.Store(true)
		return e
	}
	if !name.MatchString(out.OwnerEpoch) || !name.MatchString(out.OwnerToken) || out.LeaseTTLMS < 1000 || out.LeaseTTLMS > 60000 || out.Failure != "" || out.State != "" || out.WaveID != "" || len(out.Hosts) != 0 || out.Result != nil {
		c.terminal.Store(true)
		return ErrBrokerTransport
	}
	c.mu.Lock()
	c.ownerEpoch = out.OwnerEpoch
	c.ownerToken = out.OwnerToken
	c.brokerEpoch = out.BrokerEpoch
	c.lease = time.Duration(out.LeaseTTLMS) * time.Millisecond
	c.mu.Unlock()
	return nil
}
func (c *BrokerClient) authority(operation string, allowTerminal bool) (BrokerRequest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ownerEpoch == "" || (!allowTerminal && c.terminal.Load()) {
		return BrokerRequest{}, ErrFence
	}
	return BrokerRequest{Operation: operation, OwnerEpoch: c.ownerEpoch, OwnerToken: c.ownerToken}, nil
}
func (c *BrokerClient) Renew(ctx context.Context) error {
	if !c.renewMu.TryLock() {
		return ErrState
	}
	defer c.renewMu.Unlock()
	q, e := c.authority("renew", false)
	if e != nil {
		return e
	}
	if c.renewSequence == ^uint64(0) {
		return ErrState
	}
	c.renewSequence++
	q.RenewSequence = c.renewSequence
	out, e := c.exchange(ctx, q, false)
	if e != nil {
		c.terminal.Store(true)
		return e
	}
	if out.LeaseTTLMS != c.LeaseInterval().Milliseconds() || out.OwnerToken != "" || out.OwnerEpoch != "" || out.State != "" || out.WaveID != "" || len(out.Hosts) != 0 || out.Result != nil {
		c.terminal.Store(true)
		return ErrBrokerTransport
	}
	return nil
}
func (c *BrokerClient) Release(ctx context.Context) error {
	q, e := c.authority("release", true)
	if e != nil {
		return e
	}
	c.terminal.Store(true)
	out, e := c.exchange(ctx, q, false)
	if e != nil {
		return e
	}
	if out.State != "retiring" || out.Result != nil || len(out.Hosts) != 0 {
		return ErrBrokerTransport
	}
	return nil
}
func (c *BrokerClient) Cleanup(ctx context.Context) (bool, error) {
	q, e := c.authority("cleanup", true)
	if e != nil {
		return false, e
	}
	q.OwnerToken = ""
	out, e := c.exchange(ctx, q, true)
	if e != nil {
		return false, e
	}
	if out.OwnerEpoch != q.OwnerEpoch || out.OwnerToken != "" || out.WaveID != "" || out.Result != nil || (out.State != "active" && out.State != "retiring" && out.State != "reaped") {
		return false, ErrBrokerTransport
	}
	seen := map[string]string{}
	for _, h := range out.Hosts {
		if !name.MatchString(h.HostID) || !name.MatchString(h.SessionID) || seen[h.HostID] != "" {
			return false, ErrBrokerTransport
		}
		seen[h.HostID] = h.SessionID
	}
	if out.State == "reaped" {
		c.mu.Lock()
		waves := make([]*BrokerClientWave, 0, len(c.waves))
		for _, w := range c.waves {
			waves = append(waves, w)
		}
		c.mu.Unlock()
		for _, w := range waves {
			for _, h := range w.Hosts() {
				if seen[h.HostID] != h.SessionID {
					return false, ErrBrokerTransport
				}
			}
		}
	}
	return out.State == "reaped", nil
}
func (c *BrokerClient) reserve(ctx context.Context, execution, group, profile, digest, policy string, duration time.Duration, contexts []javarunner.DedicatedHostContext) (*BrokerClientWave, error) {
	q, e := c.authority("reserve", false)
	if e != nil {
		return nil, e
	}
	q.ExecutionID = execution
	q.GroupID = group
	q.Profile = profile
	q.ConfigurationSHA256 = digest
	q.PolicySHA256 = policy
	q.RunDurationMS = duration.Milliseconds()
	q.Sessions = append([]javarunner.DedicatedHostContext(nil), contexts...)
	if !validPolicyDigest(digest) {
		return nil, ErrState
	}
	out, e := c.exchange(ctx, q, false)
	if e != nil {
		return nil, e
	}
	if !name.MatchString(out.WaveID) || out.State != "preparing" || out.Result != nil || len(out.Hosts) != len(contexts) || out.LeaseTTLMS < 1 || out.LeaseTTLMS > c.LeaseInterval().Milliseconds()/2 {
		c.terminal.Store(true)
		return nil, ErrBrokerTransport
	}
	expected := map[string]bool{}
	for _, ctx := range contexts {
		expected[ctx.SessionID] = true
	}
	w := &BrokerClientWave{client: c, id: out.WaveID, digest: digest, phase: "preparing", hosts: map[string]*brokerClientHost{}, lease: time.Duration(out.LeaseTTLMS) * time.Millisecond}
	for _, h := range out.Hosts {
		if !name.MatchString(h.HostID) || !expected[h.SessionID] || w.hosts[h.HostID] != nil {
			c.terminal.Store(true)
			return nil, ErrBrokerTransport
		}
		delete(expected, h.SessionID)
		w.hosts[h.HostID] = &brokerClientHost{session: h.SessionID}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.waves) >= maxRecords || c.waves[w.id] != nil {
		c.terminal.Store(true)
		return nil, ErrBrokerTransport
	}
	c.waves[w.id] = w
	return w, nil
}
func (w *BrokerClientWave) Hosts() []BrokerHost {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]BrokerHost, 0, len(w.hosts))
	for id, h := range w.hosts {
		out = append(out, BrokerHost{HostID: id, SessionID: h.session})
	}
	return out
}

func (w *BrokerClientWave) HostLeaseInterval() time.Duration { return w.lease }

// RenewHost is driven by that Vuser's upstream runtime lease. During cold
// preparation, the admitted execution may explicitly maintain its reserved
// peak; an owner heartbeat never substitutes for this per-host authority.
func (w *BrokerClientWave) RenewHost(ctx context.Context, id string) error {
	q, e := w.client.authority("renew_host", false)
	if e != nil {
		return e
	}
	w.mu.Lock()
	h := w.hosts[id]
	if w.phase == "retiring" || h == nil || h.terminal || h.renewBusy || h.renewSequence == ^uint64(0) {
		w.mu.Unlock()
		return ErrState
	}
	h.renewBusy = true
	h.renewSequence++
	q.WaveID = w.id
	q.HostID = id
	q.RenewSequence = h.renewSequence
	w.mu.Unlock()
	out, e := w.client.exchange(ctx, q, false)
	w.mu.Lock()
	defer w.mu.Unlock()
	h.renewBusy = false
	if e != nil {
		// A rejected renewal after normal end must not invalidate its in-flight
		// result. Uncertain transport still fences the complete client.
		if !h.ending {
			h.terminal = true
		}
		return e
	}
	if out.WaveID != w.id || out.LeaseTTLMS != w.lease.Milliseconds() || out.State != "" || out.OwnerToken != "" || out.OwnerEpoch != "" || out.Result != nil || len(out.Hosts) != 0 {
		h.terminal = true
		w.client.terminal.Store(true)
		return ErrBrokerTransport
	}
	return nil
}
func (w *BrokerClientWave) Ready(ctx context.Context) (bool, error) {
	q, e := w.client.authority("ready", false)
	if e != nil {
		return false, e
	}
	q.WaveID = w.id
	out, e := w.client.exchange(ctx, q, false)
	if e != nil {
		return false, e
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	seen := map[string]bool{}
	valid := len(out.Hosts) == len(w.hosts)
	for _, h := range out.Hosts {
		known := w.hosts[h.HostID]
		if known == nil || known.session != h.SessionID || seen[h.HostID] {
			valid = false
		}
		seen[h.HostID] = true
	}
	if !valid || out.WaveID != w.id || out.Result != nil || (out.State != "preparing" && out.State != "ready" && out.State != "committing" && out.State != "committed" && out.State != "retiring") {
		w.client.terminal.Store(true)
		return false, ErrBrokerTransport
	}
	if out.State == "retiring" {
		w.phase = "retiring"
		return false, ErrFence
	}
	if w.phase == "preparing" && out.State == "ready" {
		w.phase = "ready"
		w.nativeData = out.NativeData
	}
	return w.phase == "ready", nil
}
func (w *BrokerClientWave) Commit(ctx context.Context) error {
	w.mu.Lock()
	if w.phase != "ready" {
		w.mu.Unlock()
		return ErrState
	}
	w.phase = "committing"
	w.mu.Unlock()
	q, e := w.client.authority("commit", false)
	if e != nil {
		return e
	}
	q.WaveID = w.id
	out, e := w.client.exchange(ctx, q, false)
	w.mu.Lock()
	defer w.mu.Unlock()
	if e != nil {
		w.phase = "retiring"
		return e
	}
	if out.State != "committed" || out.WaveID != w.id || out.Result != nil || len(out.Hosts) != 0 {
		w.client.terminal.Store(true)
		w.phase = "retiring"
		return ErrBrokerTransport
	}
	if w.phase != "committing" || w.client.terminal.Load() {
		return ErrFence
	}
	w.phase = "committed"
	return nil
}
func (w *BrokerClientWave) Abort(ctx context.Context) error {
	w.mu.Lock()
	w.phase = "retiring"
	w.mu.Unlock()
	q, e := w.client.authority("abort", true)
	if e != nil {
		return e
	}
	q.WaveID = w.id
	out, e := w.client.exchange(ctx, q, false)
	if e != nil {
		return e
	}
	if out.State != "retiring" || out.WaveID != w.id || out.Result != nil {
		return ErrBrokerTransport
	}
	return nil
}

// CloseHost permanently retires a committed dedicated Vuser without running
// user teardown or aborting peers. It does not release the durable reservation;
// only external termination evidence can do that. It remains available after
// an uncertain invocation and uses a lane independent of blocked Java work.
func (w *BrokerClientWave) CloseHost(ctx context.Context, id string) error {
	w.mu.Lock()
	h := w.hosts[id]
	if h == nil || w.phase != "committed" {
		w.mu.Unlock()
		return ErrState
	}
	h.terminal = true
	w.mu.Unlock()
	q, e := w.client.authority("close_host", true)
	if e != nil {
		return e
	}
	q.WaveID, q.HostID = w.id, id
	out, e := w.client.exchange(ctx, q, false)
	if e != nil {
		return e
	}
	if out.State != "retiring" || out.WaveID != w.id || out.Result != nil || len(out.Hosts) != 0 || out.OwnerEpoch != "" || out.OwnerToken != "" || out.LeaseTTLMS != 0 {
		return ErrBrokerTransport
	}
	return nil
}

func (w *BrokerClientWave) Invoke(ctx context.Context, host, method string, params map[string]string) (SupervisorResponse, error) {
	q, e := w.client.authority("invoke", false)
	if e != nil {
		return SupervisorResponse{}, e
	}
	// Freeze caller-owned parameters before allocating a sequence.
	owned := make(map[string]string, len(params))
	for k, v := range params {
		owned[k] = v
	}
	w.mu.Lock()
	h := w.hosts[host]
	if w.phase != "committed" || h == nil || h.busy || h.terminal || h.sequence == ^uint64(0) {
		w.mu.Unlock()
		return SupervisorResponse{}, ErrState
	}
	q.WaveID = w.id
	q.HostID = host
	q.Sequence = h.sequence + 1
	q.Method = method
	q.Parameters = owned
	probe := SupervisorRequest{Operation: "invoke", Sequence: q.Sequence, Method: method, Parameters: owned, Contract: SupervisorContract, HostID: host, BrokerEpoch: "validate", ConfigurationSHA256: w.digest, RequestID: "validate"}
	validator := SupervisorControl{config: SupervisorConfig{HostID: host, BrokerEpoch: "validate", ConfigurationSHA256: w.digest}}
	if !validator.validRequest(probe) {
		w.mu.Unlock()
		return SupervisorResponse{}, ErrState
	}
	h.sequence++
	h.busy = true
	h.ending = method == "end"
	w.mu.Unlock()
	out, e := w.client.exchange(ctx, q, false)
	w.mu.Lock()
	defer w.mu.Unlock()
	h.busy = false
	if e == nil && (h.terminal || w.phase != "committed" || w.client.terminal.Load()) {
		h.terminal = true
		return SupervisorResponse{}, ErrFence
	}
	if e != nil {
		h.terminal = true
		return SupervisorResponse{}, e
	}
	w.client.mu.Lock()
	epoch := w.client.brokerEpoch
	w.client.mu.Unlock()
	r := out.Result
	if out.WaveID != w.id || r == nil || r.Contract != SupervisorContract || r.HostID != host || r.BrokerEpoch != epoch || r.ConfigurationSHA256 != w.digest || !name.MatchString(r.RequestID) || r.InvocationID != SupervisorInvocationID(q.Sequence) || r.State != "committed" || r.Failure != "" || len(r.Payload) == 0 || len(r.Payload) > MaxSupervisorPayload {
		h.terminal = true
		w.client.terminal.Store(true)
		return SupervisorResponse{}, ErrBrokerTransport
	}
	if method == "end" {
		h.terminal = true
	}
	return *r, nil
}
func (c *BrokerClient) exchange(ctx context.Context, q BrokerRequest, allowEpochChange bool) (BrokerResponse, error) {
	var out BrokerResponse
	id, e := randomID()
	if e != nil {
		return out, e
	}
	q.Contract = BrokerContract
	q.RequestID = id
	if !validBrokerRequest(q) {
		return out, ErrState
	}
	data, e := json.Marshal(q)
	if e != nil || len(data) > 512<<10 {
		return out, ErrState
	}
	if q.Operation != "invoke" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.Endpoint, "/")+"/v1/java-broker", bytes.NewReader(data))
	if e != nil {
		return out, ErrState
	}
	req.Header.Set("Content-Type", "application/json")
	response, e := javarunner.DoNativeHTTP(c.http, req, MaxSupervisorResponse+(512<<10))
	if e != nil {
		// A known wave/host can be retired independently after caller
		// cancellation. Lost claims/reservations and unknown transport loss
		// continue to fence this owner; no invocation is retried.
		if ctx.Err() == nil || q.WaveID == "" {
			c.terminal.Store(true)
		}
		return out, ErrBrokerTransport
	}
	defer response.Body.Close()
	const limit = MaxSupervisorResponse + (512 << 10)
	data, e = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if e != nil || len(data) > limit {
		if e == nil || ctx.Err() == nil || q.WaveID == "" {
			c.terminal.Store(true)
		}
		return out, ErrBrokerTransport
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	c.mu.Lock()
	epoch := c.brokerEpoch
	c.mu.Unlock()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || (q.Operation != "capabilities" && out.Capabilities != nil) || (q.Operation != "ready" && out.NativeData) || out.Contract != BrokerContract || out.RequestID != id || !name.MatchString(out.BrokerEpoch) || (!allowEpochChange && out.BrokerEpoch != epoch) {
		c.terminal.Store(true)
		return BrokerResponse{}, ErrBrokerTransport
	}
	if response.StatusCode == http.StatusConflict && (out.Failure == "java_broker_operation_failed" || out.Failure == "java_broker_owner_fenced" || out.Failure == "java_broker_capacity_unavailable") {
		if out.OwnerEpoch != "" || out.OwnerToken != "" || out.LeaseTTLMS != 0 || out.WaveID != "" || out.State != "" || len(out.Hosts) != 0 || out.Result != nil || out.Capabilities != nil {
			c.terminal.Store(true)
			return BrokerResponse{}, ErrBrokerTransport
		}
		if out.Failure == "java_broker_owner_fenced" {
			c.terminal.Store(true)
		}
		return out, &BrokerFailure{Code: out.Failure}
	}
	if response.StatusCode != http.StatusOK || out.Failure != "" {
		c.terminal.Store(true)
		return BrokerResponse{}, ErrBrokerTransport
	}
	return out, nil
}

func (w *BrokerClientWave) NativeDataAvailable() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.phase == "ready" && w.nativeData && !w.client.terminal.Load()
}
