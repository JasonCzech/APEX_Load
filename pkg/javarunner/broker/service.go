package broker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

const BrokerContract = "apexload.java-broker/v1"

var errBrokerOwner = errors.New("java_broker_owner_fenced")

// BrokerPrincipal binds one authenticated facade to one generator owner and a
// finite set of operator profiles. Neither an execution pod nor a facade may
// choose images, certificates, resources or Kubernetes credentials through HTTP.
type BrokerPrincipal struct {
	URI, OwnerID string
	Profiles     []string
}

// BrokerProfile is trusted operator code. Its factory may bind the authorized
// session contexts to a ManagedKubernetesPreparer, never execute user code.
type BrokerProfile struct {
	ConfigurationSHA256 string
	PolicySHA256        string
	TransportLifetime   time.Duration
	InvocationTimeout   time.Duration
	MemoryBytes         int64
	HostLeaseTTL        time.Duration
	PreparationTimeout  time.Duration
	NewPreparer         func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error)
}

type BrokerRequest struct {
	Contract            string                            `json:"contract"`
	RequestID           string                            `json:"request_id"`
	Operation           string                            `json:"operation"`
	InstanceID          string                            `json:"instance_id,omitempty"`
	OwnerEpoch          string                            `json:"owner_epoch,omitempty"`
	OwnerToken          string                            `json:"owner_token,omitempty"`
	RenewSequence       uint64                            `json:"renew_sequence,omitempty"`
	ExecutionID         string                            `json:"execution_id,omitempty"`
	GroupID             string                            `json:"group_id,omitempty"`
	Profile             string                            `json:"profile,omitempty"`
	ConfigurationSHA256 string                            `json:"configuration_sha256,omitempty"`
	PolicySHA256        string                            `json:"policy_sha256,omitempty"`
	RunDurationMS       int64                             `json:"run_duration_ms,omitempty"`
	Sessions            []javarunner.DedicatedHostContext `json:"sessions,omitempty"`
	WaveID              string                            `json:"wave_id,omitempty"`
	HostID              string                            `json:"host_id,omitempty"`
	Sequence            uint64                            `json:"sequence,omitempty"`
	Method              string                            `json:"method,omitempty"`
	Parameters          map[string]string                 `json:"parameters,omitempty"`
}

type BrokerResponse struct {
	Capabilities *BrokerCapabilities `json:"capabilities,omitempty"`
	Contract     string              `json:"contract"`
	RequestID    string              `json:"request_id"`
	BrokerEpoch  string              `json:"broker_epoch"`
	Failure      string              `json:"failure,omitempty"`
	OwnerEpoch   string              `json:"owner_epoch,omitempty"`
	OwnerToken   string              `json:"owner_token,omitempty"`
	LeaseTTLMS   int64               `json:"lease_ttl_ms,omitempty"`
	WaveID       string              `json:"wave_id,omitempty"`
	State        string              `json:"state,omitempty"`
	Hosts        []BrokerHost        `json:"hosts,omitempty"`
	Result       *SupervisorResponse `json:"result,omitempty"`
}

type BrokerHost struct {
	HostID    string `json:"host_id"`
	SessionID string `json:"session_id"`
}

type brokerOwner struct {
	mu                     sync.Mutex
	instance, epoch, token string
	deadline               time.Time
	renewSequence          uint64
	retired                bool
	waves                  map[string]*brokerWave
	ctx                    context.Context
	cancel                 context.CancelFunc
}
type brokerWave struct {
	wave           *DedicatedWave
	mu             sync.Mutex
	sequences      map[string]uint64
	busy           map[string]bool
	renewSequences map[string]uint64
	renewBusy      map[string]bool
	lease          time.Duration
	retire         sync.Once
	runDuration    time.Duration
	deadline       time.Time
}

// BrokerService is the authenticated dedicated-pod control plane. The server
// must use TLS RequireAndVerifyClientCert; handlers additionally require an
// exact configured facade URI from a verified certificate. StartEpoch must have
// completed before construction. Reconciliation runs independently of requests.
// No owner/run state is adopted after restart. This is opt-in and has no effect
// on the existing dedicated IPC v2/loopback contracts.
type BrokerService struct {
	store      *Store
	epoch      string
	lease      time.Duration
	principals map[string]BrokerPrincipal
	profiles   map[string]BrokerProfile
	mu         sync.Mutex
	owners     map[string]*brokerOwner
	instances  map[string]bool
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	cleanup    sync.WaitGroup
	preparing  sync.WaitGroup
}

func NewBrokerService(parent context.Context, store *Store, epoch string, lease time.Duration, principals []BrokerPrincipal, profiles map[string]BrokerProfile) (*BrokerService, error) {
	if parent == nil || store == nil || !name.MatchString(epoch) || lease < time.Second || lease > time.Minute || len(principals) == 0 || len(principals) > 256 || len(profiles) == 0 || len(profiles) > 256 {
		return nil, ErrState
	}
	if _, _, e := store.Snapshot(parent, epoch); e != nil {
		return nil, e
	}
	s := &BrokerService{store: store, epoch: epoch, lease: lease, principals: map[string]BrokerPrincipal{}, profiles: map[string]BrokerProfile{}, owners: map[string]*brokerOwner{}, instances: map[string]bool{}, done: make(chan struct{})}
	for id, p := range profiles {
		if !name.MatchString(id) || !validPolicyDigest(p.ConfigurationSHA256) || !validPolicyDigest(p.PolicySHA256) || p.TransportLifetime > 24*time.Hour || p.maxRun() < time.Millisecond || p.InvocationTimeout < time.Millisecond || p.InvocationTimeout > time.Hour || p.MemoryBytes < 1 || p.HostLeaseTTL < time.Millisecond || p.HostLeaseTTL > lease/2 || p.PreparationTimeout < time.Second || p.PreparationTimeout > time.Hour || p.NewPreparer == nil {
			return nil, ErrState
		}
		s.profiles[id] = p
	}
	ownerIDs := map[string]bool{}
	for _, p := range principals {
		u, e := url.Parse(p.URI)
		if e != nil || !validBrokerURI(p.URI) || !name.MatchString(p.OwnerID) || u.Path != "/java-facade/"+p.OwnerID || ownerIDs[p.OwnerID] || len(p.Profiles) == 0 || len(p.Profiles) > 256 {
			return nil, ErrState
		}
		seen := map[string]bool{}
		for _, id := range p.Profiles {
			if _, ok := s.profiles[id]; !ok || seen[id] {
				return nil, ErrState
			}
			seen[id] = true
		}
		p.Profiles = append([]string(nil), p.Profiles...)
		s.principals[p.URI] = p
		ownerIDs[p.OwnerID] = true
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	go s.watch()
	return s, nil
}

func (s *BrokerService) Close() { s.cancel(); <-s.done; s.cleanup.Wait(); s.preparing.Wait() }
func (s *BrokerService) watch() {
	defer close(s.done)
	ticker := time.NewTicker(min(s.lease/4, 250*time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.retireExpired(true)
			return
		case <-ticker.C:
			s.retireExpired(false)
		}
	}
}
func (s *BrokerService) retireExpired(all bool) {
	s.mu.Lock()
	owners := make([]*brokerOwner, 0, len(s.owners))
	for _, o := range s.owners {
		owners = append(owners, o)
	}
	s.mu.Unlock()
	for _, o := range owners {
		o.mu.Lock()
		expired := all || !time.Now().Before(o.deadline)
		if expired {
			o.retired = true
			o.cancel()
		}
		waves := make([]*brokerWave, 0, len(o.waves))
		for _, w := range o.waves {
			waves = append(waves, w)
		}
		o.mu.Unlock()
		if expired {
			s.retireOwner(o, "broker_owner_expired")
		} else {
			for _, w := range waves {
				w.mu.Lock()
				elapsed := !time.Now().Before(w.deadline)
				w.mu.Unlock()
				if elapsed {
					w.retire.Do(func() {
						s.cleanup.Add(1)
						go func() { defer s.cleanup.Done(); _ = w.wave.Abort("broker_run_expired") }()
					})
				}
			}
		}
	}
}

func (s *BrokerService) retireOwner(o *brokerOwner, reason string) {
	o.mu.Lock()
	o.retired = true
	o.cancel()
	waves := make([]*brokerWave, 0, len(o.waves))
	for _, w := range o.waves {
		waves = append(waves, w)
	}
	o.mu.Unlock()
	// Every wave owns at least one durable record, so total concurrent cleanup
	// is bounded by the store's inventory limit. No stalled network call can
	// serialize expiration/cancellation of other owners. Each Abort has a
	// separate five-second bound and retains unproven reservations durably.
	for _, w := range waves {
		w.retire.Do(func() { s.cleanup.Add(1); go func() { defer s.cleanup.Done(); _ = w.wave.Abort(reason) }() })
	}
}

func (s *BrokerService) principal(r *http.Request) (BrokerPrincipal, bool) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
		return BrokerPrincipal{}, false
	}
	leaf := r.TLS.VerifiedChains[0][0]
	// One role per leaf. A dual-role or multi-owner leaf is never facade authority.
	if len(leaf.URIs) != 1 {
		return BrokerPrincipal{}, false
	}
	p, ok := s.principals[leaf.URIs[0].String()]
	return p, ok
}

func (s *BrokerService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/v1/java-broker" || r.URL.RawQuery != "" || r.Method != http.MethodPost {
		http.Error(w, "java_broker_request_invalid", http.StatusBadRequest)
		return
	}
	principal, ok := s.principal(r)
	if !ok {
		http.Error(w, "java_broker_unauthorized", http.StatusUnauthorized)
		return
	}
	var q BrokerRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&q) != nil || decoder.Decode(new(any)) != io.EOF || !validBrokerRequest(q) {
		http.Error(w, "java_broker_request_invalid", http.StatusBadRequest)
		return
	}
	out := BrokerResponse{Contract: BrokerContract, RequestID: q.RequestID, BrokerEpoch: s.epoch}
	e := s.dispatch(r.Context(), principal, q, &out)
	status := http.StatusOK
	if e != nil {
		status = http.StatusConflict
		out = BrokerResponse{Contract: BrokerContract, RequestID: q.RequestID, BrokerEpoch: s.epoch, Failure: "java_broker_operation_failed"}
		if errors.Is(e, errBrokerOwner) {
			out.Failure = "java_broker_owner_fenced"
		}
		if errors.Is(e, ErrCapacity) {
			out.Failure = "java_broker_capacity_unavailable"
		}
	}
	data, e := json.Marshal(out)
	if e != nil || len(data) > MaxSupervisorResponse+(512<<10) {
		http.Error(w, "java_broker_response_invalid", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func validBrokerRequest(q BrokerRequest) bool {
	if q.Contract != BrokerContract || !name.MatchString(q.RequestID) {
		return false
	}
	// Operation-specific canonical shape rejects ambiguous irrelevant fields.
	base := BrokerRequest{Contract: q.Contract, RequestID: q.RequestID, Operation: q.Operation}
	if q.Operation == "capabilities" {
	} else if q.Operation == "cleanup" {
		base.OwnerEpoch = q.OwnerEpoch
		if !name.MatchString(q.OwnerEpoch) {
			return false
		}
	} else if q.Operation == "claim" {
		base.InstanceID = q.InstanceID
		if !name.MatchString(q.InstanceID) {
			return false
		}
	} else {
		base.OwnerEpoch = q.OwnerEpoch
		base.OwnerToken = q.OwnerToken
		if !name.MatchString(q.OwnerEpoch) || !name.MatchString(q.OwnerToken) {
			return false
		}
		switch q.Operation {
		case "renew":
			base.RenewSequence = q.RenewSequence
			if q.RenewSequence == 0 {
				return false
			}
		case "release":
		case "reserve":
			base.ExecutionID = q.ExecutionID
			base.GroupID = q.GroupID
			base.Profile = q.Profile
			base.ConfigurationSHA256 = q.ConfigurationSHA256
			base.PolicySHA256 = q.PolicySHA256
			base.RunDurationMS = q.RunDurationMS
			base.Sessions = q.Sessions
			if !name.MatchString(q.ExecutionID) || !name.MatchString(q.GroupID) || !name.MatchString(q.Profile) || !validPolicyDigest(q.ConfigurationSHA256) || !validPolicyDigest(q.PolicySHA256) || q.RunDurationMS < 1 || q.RunDurationMS > 86400000 || len(q.Sessions) == 0 || len(q.Sessions) > maxRecords {
				return false
			}
			seen := map[string]bool{}
			for _, c := range q.Sessions {
				if c.Validate() != nil || seen[c.SessionID] {
					return false
				}
				seen[c.SessionID] = true
			}
		case "ready", "commit", "abort":
			base.WaveID = q.WaveID
			if !name.MatchString(q.WaveID) {
				return false
			}
		case "close_host":
			base.WaveID = q.WaveID
			base.HostID = q.HostID
			if !name.MatchString(q.WaveID) || !name.MatchString(q.HostID) {
				return false
			}
		case "renew_host":
			base.WaveID = q.WaveID
			base.HostID = q.HostID
			base.RenewSequence = q.RenewSequence
			if !name.MatchString(q.WaveID) || !name.MatchString(q.HostID) || q.RenewSequence == 0 {
				return false
			}
		case "invoke":
			base.WaveID = q.WaveID
			base.HostID = q.HostID
			base.Sequence = q.Sequence
			base.Method = q.Method
			base.Parameters = q.Parameters
			if !name.MatchString(q.WaveID) || !name.MatchString(q.HostID) || q.Sequence == 0 || (q.Method != "init" && q.Method != "action" && q.Method != "end") {
				return false
			}
		default:
			return false
		}
	}
	a, _ := json.Marshal(q)
	b, _ := json.Marshal(base)
	return string(a) == string(b)
}

func (s *BrokerService) claim(p BrokerPrincipal, instance string, out *BrokerResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return ErrFence
	}
	if o := s.owners[p.OwnerID]; o != nil {
		o.mu.Lock()
		if !o.retired && time.Now().Before(o.deadline) {
			if o.instance != instance {
				o.mu.Unlock()
				return ErrFence
			} // takeover waits for expiry/explicit release
			out.OwnerEpoch = o.epoch
			out.OwnerToken = o.token
			out.LeaseTTLMS = s.lease.Milliseconds()
			o.mu.Unlock()
			return nil
		}
		o.mu.Unlock()
		s.retireOwner(o, "broker_owner_expired")
	}
	key := p.OwnerID + "\x00" + instance
	if s.instances[key] || len(s.instances) >= maxRecords {
		return ErrFence
	}
	epoch, e := randomID()
	if e != nil {
		return e
	}
	token, e := randomID()
	if e != nil {
		return e
	}
	s.instances[key] = true
	ownerCtx, ownerCancel := context.WithCancel(s.ctx)
	s.owners[p.OwnerID] = &brokerOwner{instance: instance, epoch: epoch, token: token, deadline: time.Now().Add(s.lease), waves: map[string]*brokerWave{}, ctx: ownerCtx, cancel: ownerCancel}
	out.OwnerEpoch = epoch
	out.OwnerToken = token
	out.LeaseTTLMS = s.lease.Milliseconds()
	return nil
}

func (s *BrokerService) owner(p BrokerPrincipal, q BrokerRequest) (*brokerOwner, error) {
	s.mu.Lock()
	o := s.owners[p.OwnerID]
	s.mu.Unlock()
	if o == nil {
		return nil, ErrFence
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if s.ctx.Err() != nil || o.retired || !time.Now().Before(o.deadline) || o.epoch != q.OwnerEpoch || subtle.ConstantTimeCompare([]byte(o.token), []byte(q.OwnerToken)) != 1 {
		return nil, ErrFence
	}
	return o, nil
}

func (s *BrokerService) dispatch(ctx context.Context, p BrokerPrincipal, q BrokerRequest, out *BrokerResponse) error {
	if q.Operation == "capabilities" {
		if _, _, e := s.store.Snapshot(ctx, s.epoch); e != nil {
			return e
		}
		out.Capabilities = s.capabilities(p)
		return nil
	}
	if q.Operation == "cleanup" {
		return s.cleanupStatus(ctx, p, q.OwnerEpoch, out)
	}
	if q.Operation == "claim" || q.Operation == "renew" {
		if _, _, e := s.store.Snapshot(ctx, s.epoch); e != nil {
			if errors.Is(e, ErrFence) {
				return errBrokerOwner
			}
			return e
		}
	}
	if q.Operation == "claim" {
		e := s.claim(p, q.InstanceID, out)
		if errors.Is(e, ErrFence) {
			return errBrokerOwner
		}
		return e
	}
	o, e := s.owner(p, q)
	if e != nil {
		return errBrokerOwner
	}
	operationCtx, operationCancel := context.WithCancel(ctx)
	stop := context.AfterFunc(o.ctx, operationCancel)
	defer func() { stop(); operationCancel() }()
	ctx = operationCtx
	switch q.Operation {
	case "renew":
		o.mu.Lock()
		if o.retired || !time.Now().Before(o.deadline) || q.RenewSequence != o.renewSequence+1 || o.renewSequence == ^uint64(0) {
			o.mu.Unlock()
			return errBrokerOwner
		}
		o.renewSequence = q.RenewSequence
		o.deadline = time.Now().Add(s.lease)
		o.mu.Unlock()
		out.LeaseTTLMS = s.lease.Milliseconds()
		return nil
	case "release":
		s.retireOwner(o, "broker_owner_released")
		out.State = "retiring"
		return nil
	case "reserve":
		return s.reserve(ctx, p, o, q, out)
	}
	o.mu.Lock()
	wave := o.waves[q.WaveID]
	o.mu.Unlock()
	if wave == nil {
		return ErrFence
	}
	out.WaveID = q.WaveID
	wave.mu.Lock()
	deadline := wave.deadline
	expired := !time.Now().Before(deadline)
	wave.mu.Unlock()
	if expired && q.Operation != "abort" && q.Operation != "close_host" {
		return ErrFence
	}
	if q.Operation != "abort" && q.Operation != "close_host" {
		bounded, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		ctx = bounded
	}
	switch q.Operation {
	case "close_host":
		if e := wave.wave.CloseHost(q.HostID); e != nil {
			return e
		}
		out.State = "retiring"
		return nil
	case "renew_host":
		wave.mu.Lock()
		seq, known := wave.renewSequences[q.HostID]
		if !known || wave.renewBusy[q.HostID] || seq == ^uint64(0) || q.RenewSequence != seq+1 {
			wave.mu.Unlock()
			return ErrState
		}
		wave.renewSequences[q.HostID] = q.RenewSequence
		wave.renewBusy[q.HostID] = true
		wave.mu.Unlock()
		defer func() { wave.mu.Lock(); wave.renewBusy[q.HostID] = false; wave.mu.Unlock() }()
		if e := wave.wave.RenewHost(ctx, q.HostID, wave.lease); e != nil {
			return e
		}
		out.LeaseTTLMS = wave.lease.Milliseconds()
		return nil
	case "ready":
		wave.wave.mu.Lock()
		out.State = wave.wave.phase
		wave.wave.mu.Unlock()
		for _, r := range wave.wave.Records() {
			out.Hosts = append(out.Hosts, BrokerHost{HostID: r.ID, SessionID: r.SessionID})
		}
		return nil
	case "commit":
		if e := wave.wave.Commit(ctx); e != nil {
			return e
		}
		wave.mu.Lock()
		if !time.Now().Before(wave.deadline) {
			wave.mu.Unlock()
			_ = wave.wave.Abort("broker_run_expired")
			return ErrFence
		}
		wave.deadline = time.Now().Add(wave.runDuration)
		wave.mu.Unlock()
		out.State = "committed"
		return nil
	case "abort":
		out.State = "retiring"
		return wave.wave.Abort("broker_wave_aborted")
	case "invoke":
		wave.wave.mu.Lock()
		committed := wave.wave.phase == "committed"
		wave.wave.mu.Unlock()
		if !committed {
			return ErrState
		}
		wave.mu.Lock()
		seq, ok := wave.sequences[q.HostID]
		if !ok || wave.busy[q.HostID] || seq == ^uint64(0) || q.Sequence != seq+1 {
			wave.mu.Unlock()
			return ErrState
		}
		wave.sequences[q.HostID] = q.Sequence
		wave.busy[q.HostID] = true
		wave.mu.Unlock()
		result, e := wave.wave.Invoke(ctx, q.HostID, q.Method, q.Parameters)
		wave.mu.Lock()
		wave.busy[q.HostID] = false
		wave.mu.Unlock()
		if e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ErrFence
		}
		out.Result = &result
		return nil
	}
	return ErrState
}

func (s *BrokerService) reserve(ctx context.Context, p BrokerPrincipal, o *brokerOwner, q BrokerRequest, out *BrokerResponse) error {
	allowed := false
	for _, id := range p.Profiles {
		if id == q.Profile {
			allowed = true
		}
	}
	if !allowed {
		return ErrFence
	}
	profile := s.profiles[q.Profile]
	if q.ConfigurationSHA256 != profile.ConfigurationSHA256 || q.PolicySHA256 != profile.PolicySHA256 || time.Duration(q.RunDurationMS)*time.Millisecond > profile.maxRun() {
		return ErrState
	}
	preparer, e := profile.NewPreparer(append([]javarunner.DedicatedHostContext(nil), q.Sessions...))
	if e != nil {
		return ErrState
	}
	sessions := make([]string, len(q.Sessions))
	for i, c := range q.Sessions {
		sessions[i] = c.SessionID
	}
	id, e := randomID()
	if e != nil {
		return e
	}
	o.mu.Lock()
	if o.retired || !time.Now().Before(o.deadline) || len(o.waves) >= maxRecords {
		o.mu.Unlock()
		return ErrFence
	}
	binding := javarunner.HostBinding{OwnerID: p.OwnerID, OwnerEpoch: o.epoch, ExecutionID: q.ExecutionID, GroupID: q.GroupID, ConfigurationSHA256: profile.ConfigurationSHA256}
	wave, e := NewDedicatedWave(ctx, s.store, s.epoch, binding, sessions, profile.MemoryBytes, profile.HostLeaseTTL, profile.PreparationTimeout, leaseBoundPreparer{preparer, profile.HostLeaseTTL})
	if e != nil {
		o.mu.Unlock()
		return e
	}
	managed := &brokerWave{wave: wave, sequences: map[string]uint64{}, busy: map[string]bool{}, renewSequences: map[string]uint64{}, renewBusy: map[string]bool{}, lease: profile.HostLeaseTTL, runDuration: time.Duration(q.RunDurationMS) * time.Millisecond, deadline: time.Now().Add(profile.PreparationTimeout)}
	for _, r := range wave.Records() {
		managed.sequences[r.ID] = 0
		managed.renewSequences[r.ID] = 0
		out.Hosts = append(out.Hosts, BrokerHost{HostID: r.ID, SessionID: r.SessionID})
	}
	o.waves[id] = managed
	expired := s.ctx.Err() != nil || !time.Now().Before(o.deadline)
	if !expired {
		s.preparing.Add(1)
	}
	o.mu.Unlock()
	if expired {
		_ = wave.Abort("broker_owner_expired")
		return ErrFence
	}
	// Preparation is independent of HTTP request duration and is bounded by its
	// durable deadline and owner watchdog. It never invokes constructors/init.
	go func() { defer s.preparing.Done(); _ = wave.Prepare(o.ctx) }()
	out.WaveID = id
	out.State = "preparing"
	out.LeaseTTLMS = profile.HostLeaseTTL.Milliseconds()
	return nil
}

type leaseBoundPreparer struct {
	inner DedicatedHostPreparer
	lease time.Duration
}

func (p leaseBoundPreparer) PrepareHost(ctx context.Context, r Record) (*SupervisorClient, error) {
	c, e := p.inner.PrepareHost(ctx, r)
	if e == nil && c != nil && c.config.LeaseTTL != p.lease {
		return c, ErrState
	}
	return c, e
}

// Cleanup is read-only and authorized by the exact facade principal, rather
// than a live lease token: a stopped owner must still be able to observe proof.
// It cannot initiate fencing, release reservations, or adopt any Java state.
func (s *BrokerService) cleanupStatus(ctx context.Context, p BrokerPrincipal, ownerEpoch string, out *BrokerResponse) error {
	s.mu.Lock()
	owner := s.owners[p.OwnerID]
	s.mu.Unlock()
	known := false
	if owner != nil {
		owner.mu.Lock()
		if owner.epoch == ownerEpoch {
			known = true
			if !owner.retired {
				owner.mu.Unlock()
				out.State = "active"
				out.OwnerEpoch = ownerEpoch
				return nil
			}
		}
		owner.mu.Unlock()
	}
	records, _, e := s.store.Snapshot(ctx, s.epoch)
	if e != nil {
		return e
	}
	reaped := true
	for _, r := range records {
		if r.Binding.OwnerID == p.OwnerID && r.Binding.OwnerEpoch == ownerEpoch {
			known = true
			out.Hosts = append(out.Hosts, BrokerHost{HostID: r.ID, SessionID: r.SessionID})
			if r.State != javarunner.HostReaped {
				reaped = false
			}
		}
	}
	if !known {
		return ErrFence
	}
	out.OwnerEpoch = ownerEpoch
	out.State = "retiring"
	if reaped {
		out.State = "reaped"
	}
	return nil
}
