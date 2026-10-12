package javasidecar

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

type facadePrepare func(context.Context, broker.Record) (*broker.SupervisorClient, error)

func (f facadePrepare) PrepareHost(ctx context.Context, r broker.Record) (*broker.SupervisorClient, error) {
	return f(ctx, r)
}

type facadeRuntime struct {
	calls   atomic.Int32
	block   bool
	entered chan struct{}
}

func (*facadeRuntime) Ready(context.Context) error { return nil }
func (r *facadeRuntime) Invoke(ctx context.Context, id, method string, params map[string]string) ([]byte, error) {
	r.calls.Add(1)
	if method == "action" && r.block {
		close(r.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	event := strings.ReplaceAll(brokerEvent(1, `,"transaction_id":"tx"`), "host-call-1", id)
	return json.Marshal(ExecutionResponse{Contract: ContractVersion, ExecutionID: id, Success: true, Stdout: params["value"], Events: event + "\n", DurationMS: 7})
}

// Real mTLS broker/client/supervisor and durable store; only the JVM and pod
// provisioning are replaced, so this test makes no container-containment claim.
func facadeBrokerFixture(t *testing.T, block bool) (*broker.BrokerClient, *facadeRuntime, *broker.Store, string) {
	return facadeBrokerFixtureWrapped(t, block, nil)
}
func facadeBrokerFixtureWrapped(t *testing.T, block bool, wrap func(http.Handler) http.Handler, prepared ...broker.PreparedRuntime) (*broker.BrokerClient, *facadeRuntime, *broker.Store, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	cert := func(uri string) tls.Certificate {
		t.Helper()
		leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(uri)
		serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
		if e != nil {
			t.Fatal(e)
		}
		leaf := &x509.Certificate{SerialNumber: serial, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{u}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		data, e := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		return tls.Certificate{Certificate: [][]byte{data}, PrivateKey: leafKey}
	}
	serve := func(h http.Handler, c tls.Certificate) *httptest.Server {
		server := httptest.NewUnstartedServer(h)
		server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{c}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
		server.StartTLS()
		t.Cleanup(server.Close)
		return server
	}
	store, e := broker.Open(t.Context(), filepath.Join(t.TempDir(), "authority.db"), broker.Config{Namespace: "execution", Budget: javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 2, MaxMemoryBytes: 1024}}, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.Close() })
	epoch, e := store.StartEpoch(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	runtime := &facadeRuntime{block: block, entered: make(chan struct{})}
	brokerURI := "spiffe://apexload/java-broker"
	facadeURI := "spiffe://apexload/java-facade/generator"
	controlCert := cert(brokerURI)
	digest := strings.Repeat("a", 64)
	service, e := broker.NewBrokerService(t.Context(), store, epoch, 10*time.Second, []broker.BrokerPrincipal{{URI: facadeURI, OwnerID: "generator", Profiles: []string{"approved"}}}, map[string]broker.BrokerProfile{"approved": {PolicySHA256: digest, TransportLifetime: time.Hour, InvocationTimeout: 10 * time.Second, ConfigurationSHA256: digest, MemoryBytes: 100, HostLeaseTTL: 5 * time.Second, PreparationTimeout: 10 * time.Second, NewPreparer: func([]javarunner.DedicatedHostContext) (broker.DedicatedHostPreparer, error) {
		return facadePrepare(func(ctx context.Context, r broker.Record) (*broker.SupervisorClient, error) {
			if e := store.BeginPrepare(ctx, epoch, r.ID); e != nil {
				return nil, e
			}
			if e := store.BindPod(ctx, epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID); e != nil {
				return nil, e
			}
			cfg := broker.SupervisorConfig{HostID: r.ID, BrokerEpoch: epoch, ConfigurationSHA256: digest, BrokerClientURI: brokerURI, LeaseTTL: 5 * time.Second, PreparationTimeout: 10 * time.Second, InvocationTimeout: 10 * time.Second}
			var guest broker.PreparedRuntime = runtime
			if len(prepared) > 0 {
				guest = prepared[0]
			}
			control, e := broker.NewSupervisorControl(t.Context(), cfg, guest)
			if e != nil {
				return nil, e
			}
			t.Cleanup(control.Close)
			hostURI := "spiffe://apexload/java-host/" + r.ID
			host := serve(control, cert(hostURI))
			return broker.NewSupervisorClient(host.URL, hostURI, cfg, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{controlCert}})
		}), nil
	}}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(service.Close)
	var handler http.Handler = service
	if wrap != nil {
		handler = wrap(handler)
	}
	server := serve(handler, controlCert)
	client, e := broker.NewBrokerClient(broker.BrokerClientConfig{Endpoint: server.URL, ServerURI: brokerURI, FacadeURI: facadeURI, InvocationTimeout: 10 * time.Second}, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{cert(facadeURI)}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	return client, runtime, store, epoch
}
func facadeSessionFixture(t *testing.T, block bool) (*DedicatedBrokerSession, *broker.BrokerClientWave, *facadeRuntime, *broker.Store, string) {
	t.Helper()
	client, runtime, store, epoch := facadeBrokerFixture(t, block)
	digest := strings.Repeat("a", 64)
	e := client.Claim(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	wave, e := client.Reserve(t.Context(), "run", "group", "approved", digest, []javarunner.DedicatedHostContext{{Contract: javarunner.DedicatedHostContextContract, SessionID: "session", VUserID: 1, GroupName: "group", ScenarioID: "run"}})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		ready, e := wave.Ready(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not ready")
		}
		time.Sleep(time.Millisecond)
	}
	session, e := NewDedicatedBrokerSession(wave, wave.Hosts()[0].HostID)
	if e != nil {
		t.Fatal(e)
	}
	return session, wave, runtime, store, epoch
}
func TestDedicatedBrokerSessionLifecycleAndAttribution(t *testing.T) {
	s, w, r, store, epoch := facadeSessionFixture(t, false)
	if r.calls.Load() != 0 {
		t.Fatal("preparation ran user code")
	}
	if e := s.Renew(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e := w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	var previous string
	for _, method := range []string{"init", "action", "end"} {
		out, e := s.Execute(t.Context(), method, map[string]string{"value": "captured"})
		if e != nil {
			t.Fatal(e)
		}
		if out.ExecutionID == previous || !strings.HasPrefix(out.ExecutionID, "broker-") || out.Stdout != "captured" || !strings.Contains(out.Events, s.SessionID()+":"+out.ExecutionID+":tx") {
			t.Fatal("unbound result", out)
		}
		previous = out.ExecutionID
	}
	if _, e := s.Execute(t.Context(), "init", nil); e == nil || r.calls.Load() != 3 {
		t.Fatal("closed Java state restarted")
	}
	records, _, e := store.Snapshot(t.Context(), epoch)
	if e != nil || len(records) != 1 || records[0].State != javarunner.HostQuarantined {
		t.Fatal("cleanup proof fabricated", e)
	}
}
func TestDedicatedBrokerSessionCloseInterruptsBlockedInvocation(t *testing.T) {
	s, w, r, _, _ := facadeSessionFixture(t, true)
	if e := w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Execute(t.Context(), "init", nil); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.Execute(t.Context(), "action", nil); done <- e }()
	select {
	case <-r.entered:
	case <-time.After(time.Second):
		t.Fatal("not entered")
	}
	if e := s.Renew(t.Context()); e != nil {
		t.Fatal("renew blocked", e)
	}
	if e := s.Close(t.Context()); e != nil {
		t.Fatal("close blocked", e)
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("closed invocation accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("invoke stuck")
	}
	if _, e := s.Execute(t.Context(), "action", nil); e == nil || r.calls.Load() != 2 {
		t.Fatal("closed invocation retried")
	}
}
