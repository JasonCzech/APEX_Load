package broker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

type brokerServiceFixture struct {
	service  *BrokerService
	store    *Store
	epoch    string
	client   *http.Client
	url      string
	calls    *atomic.Int32
	sequence atomic.Int32
}

func newBrokerServiceFixture(t *testing.T, factory func(*Store, string, *atomic.Int32) func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error)) *brokerServiceFixture {
	t.Helper()
	s, epoch, _ := newStore(t)
	calls := new(atomic.Int32)
	makePreparer := func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
		return hostPrepareFunc(func(ctx context.Context, r Record) (*SupervisorClient, error) {
			if e := s.BeginPrepare(ctx, epoch, r.ID); e != nil {
				return nil, e
			}
			if e := s.BindPod(ctx, epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID); e != nil {
				return nil, e
			}
			cfg := controlConfig()
			cfg.HostID = r.ID
			cfg.BrokerEpoch = epoch
			cfg.PreparationTimeout = 10 * time.Second
			control, e := NewSupervisorControl(t.Context(), cfg, &countedWaveRuntime{calls: calls})
			if e != nil {
				return nil, e
			}
			t.Cleanup(control.Close)
			client, _ := supervisorClientFixture(t, cfg, control)
			return client, nil
		}), nil
	}
	if factory != nil {
		makePreparer = factory(s, epoch, calls)
	}
	service, e := NewBrokerService(t.Context(), s, epoch, 2*time.Second, []BrokerPrincipal{{URI: "spiffe://apexload/java-facade/generator", OwnerID: "generator", Profiles: []string{"approved"}}}, map[string]BrokerProfile{"approved": {PolicySHA256: binding().ConfigurationSHA256, TransportLifetime: time.Hour, InvocationTimeout: time.Second, ConfigurationSHA256: binding().ConfigurationSHA256, MemoryBytes: 100, HostLeaseTTL: time.Second, PreparationTimeout: 10 * time.Second, NewPreparer: makePreparer}})
	if e != nil {
		t.Fatal(e)
	}
	_, facade, serverCert, _, roots := controlCertificatesForURI(t, "spiffe://apexload/java-facade/generator")
	server := httptest.NewUnstartedServer(service)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	server.StartTLS()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{facade}}}}
	t.Cleanup(func() { client.CloseIdleConnections(); server.Close(); service.Close() })
	return &brokerServiceFixture{service: service, store: s, epoch: epoch, client: client, url: server.URL, calls: calls}
}
func (f *brokerServiceFixture) request(t *testing.T, q BrokerRequest) (BrokerResponse, int) {
	t.Helper()
	q.Contract = BrokerContract
	if q.RequestID == "" {
		id, e := randomID()
		if e != nil {
			t.Fatal(e)
		}
		q.RequestID = id
	}
	data, e := json.Marshal(q)
	if e != nil {
		t.Fatal(e)
	}
	req, e := http.NewRequestWithContext(t.Context(), "POST", f.url+"/v1/java-broker", bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	response, e := f.client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	var out BrokerResponse
	if response.StatusCode == 200 || response.StatusCode == 409 {
		if json.NewDecoder(response.Body).Decode(&out) != nil {
			t.Fatal("invalid broker response")
		}
		if out.Contract != BrokerContract || out.RequestID != q.RequestID || out.BrokerEpoch != f.epoch {
			t.Fatal("unbound response")
		}
	}
	return out, response.StatusCode
}
func (f *brokerServiceFixture) claim(t *testing.T) BrokerRequest {
	t.Helper()
	out, status := f.request(t, BrokerRequest{Operation: "claim", InstanceID: "instance-1"})
	if status != 200 || out.OwnerToken == "" || out.OwnerEpoch == "" || out.LeaseTTLMS != 2000 {
		t.Fatal(status, out.Failure)
	}
	return BrokerRequest{OwnerEpoch: out.OwnerEpoch, OwnerToken: out.OwnerToken}
}
func (f *brokerServiceFixture) wave(t *testing.T, owner BrokerRequest) BrokerRequest {
	t.Helper()
	q := owner
	q.Operation = "reserve"
	q.ExecutionID = "run"
	q.GroupID = "group"
	q.Profile = "approved"
	q.ConfigurationSHA256 = binding().ConfigurationSHA256
	q.PolicySHA256 = binding().ConfigurationSHA256
	q.RunDurationMS = 60000
	q.Sessions = []javarunner.DedicatedHostContext{managedContext()}
	out, status := f.request(t, q)
	if status != 200 || len(out.Hosts) != 1 || out.State != "preparing" {
		t.Fatal(status, out.Failure)
	}
	q = owner
	q.Operation = "ready"
	q.WaveID = out.WaveID
	deadline := time.Now().Add(time.Second)
	for {
		out, status = f.request(t, q)
		if status != 200 {
			t.Fatal(status, out.Failure)
		}
		if out.State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preparation did not become ready", out.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
	q.HostID = out.Hosts[0].HostID
	return q
}

func TestBrokerServiceAuthenticatedPrepareCommitAndNoReplay(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	owner := f.claim(t)
	w := f.wave(t, owner)
	if f.calls.Load() != 0 {
		t.Fatal("preparation initialized user code")
	}
	q := owner
	q.Operation = "invoke"
	q.WaveID = w.WaveID
	q.HostID = w.HostID
	q.Sequence = 1
	q.Method = "init"
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("precommit invoke accepted")
	}
	// The precommit rejection consumed neither invocation sequence.
	commit := owner
	commit.Operation = "commit"
	commit.WaveID = w.WaveID
	if out, status := f.request(t, commit); status != 200 || out.State != "committed" {
		t.Fatal(status, out.Failure)
	}
	q.Sequence = 1
	if out, status := f.request(t, q); status != 200 || out.Result == nil || f.calls.Load() != 1 {
		t.Fatal(status, out.Failure)
	}
	if _, status := f.request(t, q); status != 409 || f.calls.Load() != 1 {
		t.Fatal("invocation replayed")
	}
	q.Sequence = 2
	q.Method = "action"
	if out, status := f.request(t, q); status != 200 || out.Result == nil || f.calls.Load() != 2 {
		t.Fatal(status, out.Failure)
	}
	q.HostID = "foreign"
	q.Sequence = 3
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("foreign host accepted")
	}
}

func TestBrokerServiceOwnerRenewalReplayExpiryAndRestart(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	owner := f.claim(t)
	q := owner
	q.Operation = "renew"
	q.RenewSequence = 1
	if _, status := f.request(t, q); status != 200 {
		t.Fatal(status)
	}
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("renewal replay extended lease")
	}
	if _, status := f.request(t, BrokerRequest{Operation: "claim", InstanceID: "other-instance"}); status != 409 {
		t.Fatal("active owner replaced")
	}
	f.service.mu.Lock()
	o := f.service.owners["generator"]
	f.service.mu.Unlock()
	o.mu.Lock()
	o.deadline = time.Now().Add(-time.Second)
	o.mu.Unlock()
	if _, status := f.request(t, BrokerRequest{Operation: "claim", InstanceID: "instance-1"}); status != 409 {
		t.Fatal("expired instance revived")
	}
	if out, status := f.request(t, BrokerRequest{Operation: "claim", InstanceID: "other-instance"}); status != 200 || out.OwnerEpoch == owner.OwnerEpoch {
		t.Fatal("new instance not fenced")
	}
	q.RenewSequence = 2
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("old owner renewed")
	}
	if _, e := f.store.StartEpoch(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, status := f.request(t, BrokerRequest{Operation: "claim", InstanceID: "third-instance"}); status != 409 {
		t.Fatal("old broker issued authority")
	}
}

func TestBrokerServiceReleaseCancelsBlockedPreparation(t *testing.T) {
	entered := make(chan struct{})
	stopped := make(chan struct{})
	f := newBrokerServiceFixture(t, func(s *Store, epoch string, _ *atomic.Int32) func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
		return func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
			return hostPrepareFunc(func(ctx context.Context, r Record) (*SupervisorClient, error) {
				close(entered)
				<-ctx.Done()
				close(stopped)
				return nil, ctx.Err()
			}), nil
		}
	})
	owner := f.claim(t)
	q := owner
	q.Operation = "reserve"
	q.ExecutionID = "run"
	q.GroupID = "group"
	q.Profile = "approved"
	q.ConfigurationSHA256 = binding().ConfigurationSHA256
	q.PolicySHA256 = binding().ConfigurationSHA256
	q.RunDurationMS = 60000
	q.Sessions = []javarunner.DedicatedHostContext{managedContext()}
	out, status := f.request(t, q)
	if status != 200 {
		t.Fatal(out.Failure)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no preparation")
	}
	q = owner
	q.Operation = "release"
	if out, status = f.request(t, q); status != 200 || out.State != "retiring" {
		t.Fatal(status, out.Failure)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("owner release queued behind preparation")
	}
	if f.calls.Load() != 0 {
		t.Fatal("user code during preparation")
	}
}

func TestBrokerServiceRejectsUnauthorizedProfilesAndFrames(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	owner := f.claim(t)
	q := owner
	q.Operation = "reserve"
	q.ExecutionID = "run"
	q.GroupID = "group"
	q.Profile = "unapproved"
	q.ConfigurationSHA256 = binding().ConfigurationSHA256
	q.PolicySHA256 = binding().ConfigurationSHA256
	q.RunDurationMS = 60000
	q.Sessions = []javarunner.DedicatedHostContext{managedContext()}
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("unapproved profile")
	}
	q.Profile = "approved"
	q.ConfigurationSHA256 = binding().ConfigurationSHA256
	q.PolicySHA256 = binding().ConfigurationSHA256
	q.RunDurationMS = 60000
	q.OwnerToken = "wrong"
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("forged token")
	}
	q = owner
	q.Operation = "renew"
	q.RenewSequence = 1
	q.Parameters = map[string]string{"unused": "injected"}
	if _, status := f.request(t, q); status != 400 {
		t.Fatal("ambiguous operation")
	}
	data := []byte(`{"contract":"apexload.java-broker/v1","request_id":"id","operation":"claim","instance_id":"new","image":"attacker"}`)
	r, e := f.client.Post(f.url+"/v1/java-broker", "application/json", bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("arbitrary profile accepted")
	}
	_, _, _, hostCert, _ := controlCertificates(t)
	// A host certificate from a different root also fails mTLS; separately
	// check handler authorization using a verified host leaf from that root.
	leaf, e := x509Parse(hostCert)
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "https://broker/v1/java-broker", bytes.NewReader(data))
	req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
	recorder := httptest.NewRecorder()
	f.service.ServeHTTP(recorder, req)
	if recorder.Code != 401 {
		t.Fatal("host used facade role")
	}
}

func x509Parse(c tls.Certificate) (*x509.Certificate, error) {
	return x509.ParseCertificate(c.Certificate[0])
}

func TestBrokerServiceCleanupRequiresPositiveDurableEvidence(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	owner := f.claim(t)
	wave := f.wave(t, owner)
	cleanup := BrokerRequest{Operation: "cleanup", OwnerEpoch: owner.OwnerEpoch}
	if out, status := f.request(t, cleanup); status != 200 || out.State != "active" {
		t.Fatal(status, out.State)
	}
	release := owner
	release.Operation = "release"
	if _, status := f.request(t, release); status != 200 {
		t.Fatal(status)
	}
	if out, status := f.request(t, cleanup); status != 200 || out.State != "retiring" {
		t.Fatal("unproven cleanup released", status, out.State)
	}
	deadline := time.Now().Add(time.Second)
	for {
		r := record(t, f.store, f.epoch, wave.HostID)
		if r.State == javarunner.HostQuarantined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("host not retired")
		}
		time.Sleep(time.Millisecond)
	}
	r := record(t, f.store, f.epoch, wave.HostID)
	if e := f.store.ConfirmTerminated(t.Context(), f.epoch, r.ID, r.PodUID, r.ContainerID); e != nil {
		t.Fatal(e)
	}
	if out, status := f.request(t, cleanup); status != 200 || out.State != "reaped" || out.OwnerEpoch != owner.OwnerEpoch || len(out.Hosts) != 1 {
		t.Fatal(status, out.State)
	}
	cleanup.OwnerEpoch = "unknown"
	if _, status := f.request(t, cleanup); status != 409 {
		t.Fatal("unknown epoch reported reaped")
	}
}

func TestBrokerServiceOwnerExpiryInterruptsBlockedInvoke(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	f := newBrokerServiceFixture(t, func(s *Store, epoch string, calls *atomic.Int32) func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
		return func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
			return hostPrepareFunc(func(ctx context.Context, r Record) (*SupervisorClient, error) {
				if e := s.BeginPrepare(ctx, epoch, r.ID); e != nil {
					return nil, e
				}
				if e := s.BindPod(ctx, epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID); e != nil {
					return nil, e
				}
				cfg := controlConfig()
				cfg.HostID = r.ID
				cfg.BrokerEpoch = epoch
				cfg.LeaseTTL = time.Second
				cfg.PreparationTimeout = 10 * time.Second
				cfg.InvocationTimeout = 10 * time.Second
				control := newControl(t, cfg, &controlRuntime{invoke: func(ctx context.Context, _, _ string, _ map[string]string) ([]byte, error) {
					calls.Add(1)
					close(entered)
					<-ctx.Done()
					close(exited)
					return nil, ctx.Err()
				}})
				client, _ := supervisorClientFixture(t, cfg, control)
				return client, nil
			}), nil
		}
	})
	owner := f.claim(t)
	wave := f.wave(t, owner)
	q := owner
	q.Operation = "commit"
	q.WaveID = wave.WaveID
	if _, status := f.request(t, q); status != 200 {
		t.Fatal(status)
	}
	q.Operation = "invoke"
	q.HostID = wave.HostID
	q.Sequence = 1
	q.Method = "init"
	completed := make(chan int, 1)
	go func() { _, status := f.request(t, q); completed <- status }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("invoke not entered")
	}
	renew := owner
	renew.Operation = "renew"
	renew.RenewSequence = 1
	if _, status := f.request(t, renew); status != 200 {
		t.Fatal("renew blocked by invocation", status)
	}
	f.service.mu.Lock()
	o := f.service.owners["generator"]
	f.service.mu.Unlock()
	o.mu.Lock()
	o.deadline = time.Now().Add(-time.Second)
	o.mu.Unlock()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("owner expiry blocked behind invocation")
	}
	select {
	case status := <-completed:
		if status != 409 {
			t.Fatal("expired invoke succeeded", status)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP invocation stuck")
	}
}

func TestBrokerServiceTakeoverCannotReplaySessionsOrEraseQuarantine(t *testing.T) {
	f := newBrokerServiceFixture(t, nil)
	owner := f.claim(t)
	_ = f.wave(t, owner)
	release := owner
	release.Operation = "release"
	if _, status := f.request(t, release); status != 200 {
		t.Fatal(status)
	}
	out, status := f.request(t, BrokerRequest{Operation: "claim", InstanceID: "replacement"})
	if status != 200 {
		t.Fatal(status)
	}
	q := BrokerRequest{Operation: "reserve", OwnerEpoch: out.OwnerEpoch, OwnerToken: out.OwnerToken, ExecutionID: "run", GroupID: "group", Profile: "approved", PolicySHA256: binding().ConfigurationSHA256, RunDurationMS: 60000, ConfigurationSHA256: binding().ConfigurationSHA256, Sessions: []javarunner.DedicatedHostContext{managedContext()}}
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("session replayed across owner takeover")
	}
	q.Sessions[0].SessionID = "replacement-session"
	if _, status := f.request(t, q); status != 409 {
		t.Fatal("replacement admitted before cleanup")
	}
}

func TestBrokerServiceHostClosureInterruptsBlockedInvoke(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	f := newBrokerServiceFixture(t, func(s *Store, epoch string, calls *atomic.Int32) func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
		return func([]javarunner.DedicatedHostContext) (DedicatedHostPreparer, error) {
			return hostPrepareFunc(func(ctx context.Context, r Record) (*SupervisorClient, error) {
				if e := s.BeginPrepare(ctx, epoch, r.ID); e != nil {
					return nil, e
				}
				if e := s.BindPod(ctx, epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID); e != nil {
					return nil, e
				}
				cfg := controlConfig()
				cfg.HostID = r.ID
				cfg.BrokerEpoch = epoch
				cfg.LeaseTTL = time.Second
				cfg.PreparationTimeout = 10 * time.Second
				cfg.InvocationTimeout = 10 * time.Second
				control := newControl(t, cfg, &controlRuntime{invoke: func(ctx context.Context, _, _ string, _ map[string]string) ([]byte, error) {
					calls.Add(1)
					close(entered)
					<-ctx.Done()
					close(exited)
					return nil, ctx.Err()
				}})
				client, _ := supervisorClientFixture(t, cfg, control)
				return client, nil
			}), nil
		}
	})
	owner := f.claim(t)
	wave := f.wave(t, owner)
	q := owner
	q.Operation = "commit"
	q.WaveID = wave.WaveID
	if _, status := f.request(t, q); status != 200 {
		t.Fatal(status)
	}
	q.Operation = "invoke"
	q.HostID = wave.HostID
	q.Sequence = 1
	q.Method = "init"
	completed := make(chan int, 1)
	go func() { _, status := f.request(t, q); completed <- status }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("invoke not entered")
	}
	renew := owner
	renew.Operation = "renew"
	renew.RenewSequence = 1
	if _, status := f.request(t, renew); status != 200 {
		t.Fatal("renew blocked by invocation", status)
	}
	closeHost := owner
	closeHost.Operation = "close_host"
	closeHost.WaveID, closeHost.HostID = wave.WaveID, wave.HostID
	if out, status := f.request(t, closeHost); status != 200 || out.State != "retiring" {
		t.Fatal("host close failed", status, out.Failure)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("host closure blocked behind invocation")
	}
	select {
	case status := <-completed:
		if status != 409 {
			t.Fatal("closed invoke succeeded", status)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP invocation stuck")
	}
}
