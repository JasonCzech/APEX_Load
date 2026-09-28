package broker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const SupervisorContract = "apexload.java-host/v1"

// IPC v2 allows three independently bounded capture streams and JSON escaping.
const MaxSupervisorPayload = 5 << 20
const MaxSupervisorResponse = 7 << 20

// PreparedRuntime is the process adapter inside one disposable execution pod.
// Ready must verify assets/start the harness WITHOUT initializing user classes.
// Invoke transports one bounded, authenticated dedicated IPC v2 exchange.
// Its arguments are a host-local unique invocation ID, method, and parameters.
// A valid lifecycle failure belongs in the payload; error means transport/state
// is uncertain and permanently retires the pod. Return an owned payload buffer.
// The PID 1 owner MUST exit when Control.Done closes, independently of an
// uncooperative Ready/Invoke. Control never claims whole-container termination.
type PreparedRuntime interface {
	Ready(context.Context) error
	Invoke(context.Context, string, string, map[string]string) ([]byte, error)
}

type SupervisorConfig struct {
	HostID, BrokerEpoch, ConfigurationSHA256, BrokerClientURI string
	LeaseTTL, PreparationTimeout, InvocationTimeout           time.Duration
}

type SupervisorRequest struct {
	Contract            string            `json:"contract"`
	HostID              string            `json:"host_id"`
	BrokerEpoch         string            `json:"broker_epoch"`
	ConfigurationSHA256 string            `json:"configuration_sha256"`
	RequestID           string            `json:"request_id"`
	Operation           string            `json:"operation"`
	Sequence            uint64            `json:"sequence,omitempty"`
	Method              string            `json:"method,omitempty"`
	Parameters          map[string]string `json:"parameters,omitempty"`
}

type SupervisorResponse struct {
	Contract             string `json:"contract"`
	HostID               string `json:"host_id"`
	BrokerEpoch          string `json:"broker_epoch"`
	ConfigurationSHA256  string `json:"configuration_sha256"`
	RequestID            string `json:"request_id"`
	State                string `json:"state"`
	Failure              string `json:"failure,omitempty"`
	Payload              []byte `json:"payload,omitempty"`
	LeaseTTLMS           int64  `json:"lease_ttl_ms"`
	PreparationTimeoutMS int64  `json:"preparation_timeout_ms"`
	InvocationTimeoutMS  int64  `json:"invocation_timeout_ms"`
	InvocationID         string `json:"invocation_id,omitempty"`
}

type SupervisorControl struct {
	config                   SupervisorConfig
	runtime                  PreparedRuntime
	mu                       sync.Mutex
	state, failure           string
	leaseUntil, prepareUntil time.Time
	sequence                 uint64
	initialized, ended, busy bool
	ctx                      context.Context
	cancel                   context.CancelFunc
	wake                     chan struct{}
}

// NewSupervisorControl starts lease/preparation enforcement before calling the
// runtime. A stuck JVM cannot serialize renewals or expiry behind invocation.
func NewSupervisorControl(parent context.Context, cfg SupervisorConfig, runtime PreparedRuntime) (*SupervisorControl, error) {
	if parent == nil || runtime == nil || !validSupervisorConfig(cfg) {
		return nil, ErrState
	}
	ctx, cancel := context.WithCancel(parent)
	now := time.Now()
	c := &SupervisorControl{config: cfg, runtime: runtime, state: "preparing", leaseUntil: now.Add(cfg.LeaseTTL), prepareUntil: now.Add(cfg.PreparationTimeout), ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
	go c.watch()
	go c.prepare()
	return c, nil
}
func (c *SupervisorControl) Done() <-chan struct{} { return c.ctx.Done() }
func (c *SupervisorControl) Close()                { c.retire("supervisor_closed") }
func (c *SupervisorControl) retire(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retireLocked(reason)
}
func (c *SupervisorControl) retireLocked(reason string) {
	if c.state != "retiring" {
		c.state = "retiring"
		c.failure = reason
	}
	c.cancel()
}
func (c *SupervisorControl) expiredLocked(now time.Time) bool {
	if c.ctx.Err() != nil {
		c.retireLocked("supervisor_cancelled")
		return true
	}
	if !now.Before(c.leaseUntil) {
		c.retireLocked("supervisor_lease_expired")
		return true
	}
	if c.state != "committed" && c.state != "retiring" && !now.Before(c.prepareUntil) {
		c.retireLocked("supervisor_preparation_expired")
		return true
	}
	return c.state == "retiring"
}
func (c *SupervisorControl) watch() {
	for {
		c.mu.Lock()
		if c.expiredLocked(time.Now()) {
			c.mu.Unlock()
			return
		}
		deadline := c.leaseUntil
		if c.state != "committed" && c.prepareUntil.Before(deadline) {
			deadline = c.prepareUntil
		}
		c.mu.Unlock()
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-c.ctx.Done():
			timer.Stop()
			c.retire("supervisor_cancelled")
			return
		case <-c.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (c *SupervisorControl) prepare() {
	ctx, cancel := context.WithDeadline(c.ctx, c.prepareUntil)
	defer cancel()
	err := func() (err error) {
		defer func() {
			if recover() != nil {
				err = errors.New("runtime preparation panic")
			}
		}()
		return c.runtime.Ready(ctx)
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.expiredLocked(time.Now()) {
		return
	}
	if err != nil {
		c.retireLocked("supervisor_preparation_failed")
		return
	}
	c.state = "ready"
}

func validSupervisorConfig(cfg SupervisorConfig) bool {
	digest, e := hex.DecodeString(cfg.ConfigurationSHA256)
	return name.MatchString(cfg.HostID) && name.MatchString(cfg.BrokerEpoch) && e == nil && len(digest) == 32 && validBrokerURI(cfg.BrokerClientURI) && cfg.LeaseTTL >= time.Millisecond && cfg.LeaseTTL <= time.Minute && cfg.PreparationTimeout >= time.Millisecond && cfg.PreparationTimeout <= time.Hour && cfg.InvocationTimeout >= time.Millisecond && cfg.InvocationTimeout <= time.Hour
}

func validBrokerURI(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "spiffe" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path != "" && u.RawPath == ""
}

// SupervisorTLS verifies client certificates at TLS handshake; the handler also
// requires the exact broker URI SAN. An execution host's own transport cert is
// not authority to renew its lease, even when signed by the same trust root.
func SupervisorTLS(certificate tls.Certificate, clientCAs []byte, brokerURI string) (*tls.Config, error) {
	roots := x509.NewCertPool()
	if !validBrokerURI(brokerURI) || len(certificate.Certificate) == 0 || certificate.PrivateKey == nil || !roots.AppendCertsFromPEM(clientCAs) {
		return nil, ErrState
	}
	// The host can read its mounted server credential. It must never also
	// authorize broker lease renewal, even if a CA issued a dual-role leaf.
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, ErrState
	}
	for _, uri := range leaf.URIs {
		if uri.String() == brokerURI {
			return nil, ErrState
		}
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, nil
}
func (c *SupervisorControl) authorized(r *http.Request) bool {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
		return false
	}
	leaf := r.TLS.VerifiedChains[0][0]
	return len(leaf.URIs) == 1 && leaf.URIs[0].String() == c.config.BrokerClientURI
}
func (c *SupervisorControl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/v1/java-host" || r.Method != http.MethodPost {
		http.Error(w, "host_request_invalid", http.StatusNotFound)
		return
	}
	if !c.authorized(r) {
		http.Error(w, "host_unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	var q SupervisorRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&q) != nil {
		http.Error(w, "host_request_invalid", http.StatusBadRequest)
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !c.validRequest(q) {
		http.Error(w, "host_request_invalid", http.StatusBadRequest)
		return
	}
	payload, failure := c.handle(r.Context(), q)
	c.mu.Lock()
	response := SupervisorResponse{Contract: SupervisorContract, HostID: c.config.HostID, BrokerEpoch: c.config.BrokerEpoch, ConfigurationSHA256: c.config.ConfigurationSHA256, RequestID: q.RequestID, State: c.state, Failure: failure, Payload: payload}
	response.LeaseTTLMS = c.config.LeaseTTL.Milliseconds()
	response.PreparationTimeoutMS = c.config.PreparationTimeout.Milliseconds()
	response.InvocationTimeoutMS = c.config.InvocationTimeout.Milliseconds()
	if q.Operation == "invoke" {
		response.InvocationID = SupervisorInvocationID(q.Sequence)
	}
	if c.failure != "" {
		response.Failure = c.failure
	}
	c.mu.Unlock()
	if q.Operation == "close" && c.closedNormally() {
		response.Failure = ""
	}
	if response.Failure != "" {
		w.WriteHeader(http.StatusConflict)
	}
	_ = json.NewEncoder(w).Encode(response)
}
func (c *SupervisorControl) closedNormally() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failure == "supervisor_closed"
}

func (c *SupervisorControl) validRequest(q SupervisorRequest) bool {
	if q.Contract != SupervisorContract || q.HostID != c.config.HostID || q.BrokerEpoch != c.config.BrokerEpoch || q.ConfigurationSHA256 != c.config.ConfigurationSHA256 || !name.MatchString(q.RequestID) {
		return false
	}
	if q.Operation == "invoke" {
		if q.Sequence == 0 || q.Method != "init" && q.Method != "action" && q.Method != "end" || len(q.Parameters) > 256 {
			return false
		}
		total := 0
		for k, v := range q.Parameters {
			if len(k) == 0 || len(k) > 126 || len(v) > 65536 {
				return false
			}
			total += len(k) + len(v)
		}
		return total <= 256<<10
	}
	if q.Sequence != 0 || q.Method != "" || len(q.Parameters) != 0 {
		return false
	}
	return q.Operation == "ready" || q.Operation == "commit" || q.Operation == "renew" || q.Operation == "close"
}

// SupervisorInvocationID cannot repeat within a host: sequence admission is
// monotonic and the host never restarts or adopts a previous JVM.
func SupervisorInvocationID(sequence uint64) string {
	return "host-call-" + strconv.FormatUint(sequence, 10)
}

func (c *SupervisorControl) handle(ctx context.Context, q SupervisorRequest) ([]byte, string) {
	if !c.validRequest(q) {
		return nil, "supervisor_request_invalid"
	}
	c.mu.Lock()
	if c.expiredLocked(time.Now()) {
		c.mu.Unlock()
		return nil, "supervisor_retiring"
	}
	switch q.Operation {
	case "ready":
		c.mu.Unlock()
		return nil, ""
	case "renew":
		c.leaseUntil = time.Now().Add(c.config.LeaseTTL)
		c.mu.Unlock()
		select {
		case c.wake <- struct{}{}:
		default:
		}
		return nil, ""
	case "close":
		c.retireLocked("supervisor_closed")
		c.mu.Unlock()
		return nil, ""
	case "commit":
		if c.state != "ready" && c.state != "committed" {
			c.mu.Unlock()
			return nil, "supervisor_not_ready"
		}
		c.state = "committed"
		c.mu.Unlock()
		select {
		case c.wake <- struct{}{}:
		default:
		}
		return nil, ""
	}
	if ctx.Err() != nil {
		c.mu.Unlock()
		return nil, "supervisor_request_cancelled"
	}
	if c.state != "committed" || c.busy || c.ended || q.Sequence != c.sequence+1 || c.sequence == ^uint64(0) || !c.initialized && q.Method != "init" || c.initialized && q.Method == "init" {
		c.mu.Unlock()
		return nil, "supervisor_invocation_rejected"
	}
	c.sequence = q.Sequence
	c.busy = true
	c.mu.Unlock()
	invokeCtx, cancel := context.WithTimeout(c.ctx, c.config.InvocationTimeout)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	type result struct {
		payload []byte
		err     error
	}
	done := make(chan result, 1)
	go func() {
		var out result
		defer func() {
			if recover() != nil {
				out.err = errors.New("runtime invocation panic")
			}
			done <- out
		}()
		out.payload, out.err = c.runtime.Invoke(invokeCtx, SupervisorInvocationID(q.Sequence), q.Method, q.Parameters)
		if len(out.payload) > MaxSupervisorPayload {
			out.payload = nil
			out.err = errors.New("runtime response exceeded bound")
		} else {
			out.payload = append([]byte(nil), out.payload...)
		}
	}()
	var out result
	select {
	case out = <-done:
	case <-invokeCtx.Done():
		out.err = invokeCtx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy = false
	if c.expiredLocked(time.Now()) {
		return nil, "supervisor_retiring"
	}
	if out.err != nil || len(out.payload) > MaxSupervisorPayload || invokeCtx.Err() != nil {
		c.retireLocked("supervisor_invocation_failed")
		return nil, "supervisor_invocation_failed"
	}
	if q.Method == "init" {
		c.initialized = true
	}
	if q.Method == "end" {
		c.ended = true
	}
	return out.payload, ""
}
