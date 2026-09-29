package broker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type controlRuntime struct {
	ready  func(context.Context) error
	invoke func(context.Context, string, string, map[string]string) ([]byte, error)
	calls  atomic.Int32
}

func (r *controlRuntime) Ready(ctx context.Context) error {
	if r.ready != nil {
		return r.ready(ctx)
	}
	return nil
}
func (r *controlRuntime) Invoke(ctx context.Context, id, m string, p map[string]string) ([]byte, error) {
	r.calls.Add(1)
	if r.invoke != nil {
		return r.invoke(ctx, id, m, p)
	}
	return []byte("authenticated IPC fixture"), nil
}
func controlConfig() SupervisorConfig {
	return SupervisorConfig{HostID: "host", BrokerEpoch: "epoch", ConfigurationSHA256: strings.Repeat("a", 64), BrokerClientURI: "spiffe://apexload/java-broker", LeaseTTL: time.Second, PreparationTimeout: time.Second, InvocationTimeout: time.Second}
}
func newControl(t *testing.T, cfg SupervisorConfig, r *controlRuntime) *SupervisorControl {
	t.Helper()
	c, e := NewSupervisorControl(t.Context(), cfg, r)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	return c
}
func waitReady(t *testing.T, c *SupervisorControl) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		if state == "ready" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime not ready", state)
		}
		time.Sleep(time.Millisecond)
	}
}
func controlRequest(c *SupervisorControl, op string) SupervisorRequest {
	return SupervisorRequest{Contract: SupervisorContract, HostID: c.config.HostID, BrokerEpoch: c.config.BrokerEpoch, ConfigurationSHA256: c.config.ConfigurationSHA256, RequestID: "request", Operation: op}
}
func invokeRequest(c *SupervisorControl, seq uint64, method string) SupervisorRequest {
	q := controlRequest(c, "invoke")
	q.Sequence, q.Method = seq, method
	return q
}

func TestSupervisorCommitBarrierAndPermanentInvocationIdentity(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	r := &controlRuntime{ready: func(context.Context) error { <-release; return nil }}
	c := newControl(t, controlConfig(), r)
	if _, failure := c.handle(t.Context(), controlRequest(c, "commit")); failure != "supervisor_not_ready" {
		t.Fatal(failure)
	}
	if _, failure := c.handle(t.Context(), invokeRequest(c, 1, "init")); failure == "" || r.calls.Load() != 0 {
		t.Fatal("precommit user initialization")
	}
	// A separate fixture avoids closing the channel twice while keeping the
	// deliberately uncooperative initial preparation bounded by its lifetime.
	r2 := &controlRuntime{}
	c2 := newControl(t, controlConfig(), r2)
	waitReady(t, c2)
	if _, failure := c2.handle(t.Context(), controlRequest(c2, "commit")); failure != "" {
		t.Fatal(failure)
	}
	if _, failure := c2.handle(t.Context(), invokeRequest(c2, 1, "action")); failure == "" {
		t.Fatal("action before init")
	}
	for i, method := range []string{"init", "action", "end"} {
		if _, failure := c2.handle(t.Context(), invokeRequest(c2, uint64(i+1), method)); failure != "" {
			t.Fatal(failure)
		}
	}
	for _, q := range []SupervisorRequest{invokeRequest(c2, 2, "action"), invokeRequest(c2, 4, "init"), invokeRequest(c2, 4, "action")} {
		if _, failure := c2.handle(t.Context(), q); failure == "" {
			t.Fatal("closed/replayed identity admitted")
		}
	}
	if r2.calls.Load() != 3 {
		t.Fatal("runtime replayed", r2.calls.Load())
	}
}

func TestSupervisorLeaseExpiryIndependentOfBlockedRuntime(t *testing.T) {
	for _, phase := range []string{"preparation", "invocation"} {
		t.Run(phase, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			r := &controlRuntime{}
			cfg := controlConfig()
			cfg.LeaseTTL = 100 * time.Millisecond
			if phase == "preparation" {
				r.ready = func(context.Context) error { close(entered); <-release; return nil }
			} else {
				r.invoke = func(context.Context, string, string, map[string]string) ([]byte, error) {
					close(entered)
					<-release
					return nil, nil
				}
			}
			c := newControl(t, cfg, r)
			if phase == "invocation" {
				waitReady(t, c)
				c.handle(t.Context(), controlRequest(c, "commit"))
				go c.handle(t.Context(), invokeRequest(c, 1, "init"))
			}
			<-entered
			select {
			case <-c.Done():
			case <-time.After(time.Second):
				t.Fatal("blocked runtime prevented lease expiry")
			}
			if _, failure := c.handle(t.Context(), controlRequest(c, "renew")); failure == "" {
				t.Fatal("expired host revived")
			}
			c.mu.Lock()
			failure := c.failure
			c.mu.Unlock()
			if failure != "supervisor_lease_expired" {
				t.Fatal(failure)
			}
		})
	}
}

func TestSupervisorPreparationDeadlineCannotBeRenewed(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	cfg := controlConfig()
	cfg.PreparationTimeout = 60 * time.Millisecond
	c := newControl(t, cfg, &controlRuntime{ready: func(context.Context) error { <-release; return nil }})
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(time.Second)
	for {
		select {
		case <-c.Done():
			c.mu.Lock()
			reason := c.failure
			c.mu.Unlock()
			if reason != "supervisor_preparation_expired" {
				t.Fatal(reason)
			}
			return
		case <-ticker.C:
			c.handle(t.Context(), controlRequest(c, "renew"))
		case <-deadline:
			t.Fatal("renew extended preparation")
		}
	}
}

func TestSupervisorCallerCancellationRetiresWithoutReplay(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	c := newControl(t, controlConfig(), &controlRuntime{invoke: func(context.Context, string, string, map[string]string) ([]byte, error) {
		close(entered)
		<-release
		return nil, errors.New("private runtime detail")
	}})
	waitReady(t, c)
	c.handle(t.Context(), controlRequest(c, "commit"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan string, 1)
	go func() { _, failure := c.handle(ctx, invokeRequest(c, 1, "init")); result <- failure }()
	<-entered
	cancel()
	select {
	case failure := <-result:
		if failure != "supervisor_invocation_failed" {
			t.Fatal(failure)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked on Java")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("uncertain invocation did not retire host")
	}
}

func controlCertificates(t *testing.T, hostIDs ...string) ([]byte, tls.Certificate, tls.Certificate, tls.Certificate, *x509.CertPool) {
	t.Helper()
	hostID := "host"
	if len(hostIDs) > 0 {
		hostID = hostIDs[0]
	}
	return controlCertificatesForURI(t, "spiffe://apexload/java-host/"+hostID)
}
func controlCertificatesForURI(t *testing.T, serverURI string) ([]byte, tls.Certificate, tls.Certificate, tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	// Parse the generated CA so its public key and identifiers match validation.
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots = x509.NewCertPool()
	roots.AddCert(parsed)
	leaf := func(serial int64, uri string) tls.Certificate {
		private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(uri)
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{u}}
		cert, e := x509.CreateCertificate(rand.Reader, template, parsed, &private.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		return tls.Certificate{Certificate: [][]byte{cert}, PrivateKey: private}
	}
	return caPEM, leaf(2, serverURI), leaf(3, "spiffe://apexload/java-broker"), leaf(4, "spiffe://apexload/java-host/other"), roots
}
func TestSupervisorMutualTLSSeparatesBrokerFromHostCredentials(t *testing.T) {
	ca, serverCert, brokerCert, hostCert, roots := controlCertificates(t)
	c := newControl(t, controlConfig(), &controlRuntime{})
	waitReady(t, c)
	server := httptest.NewUnstartedServer(c)
	var e error
	server.TLS, e = SupervisorTLS(serverCert, ca, c.config.BrokerClientURI)
	if e != nil {
		t.Fatal(e)
	}
	server.StartTLS()
	defer server.Close()
	for _, test := range []struct {
		name   string
		cert   *tls.Certificate
		status int
	}{{"broker", &brokerCert, 200}, {"host cannot renew itself", &hostCert, 401}, {"no client certificate", nil, 0}} {
		t.Run(test.name, func(t *testing.T) {
			cfg := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}
			if test.cert != nil {
				cfg.Certificates = []tls.Certificate{*test.cert}
			}
			transport := &http.Transport{TLSClientConfig: cfg}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			body, _ := json.Marshal(controlRequest(c, "renew"))
			response, err := client.Post(server.URL+"/v1/java-host", "application/json", bytes.NewReader(body))
			if test.status == 0 {
				if err == nil {
					response.Body.Close()
					t.Fatal("missing certificate accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatal(response.StatusCode)
			}
		})
	}
}

func TestSupervisorUniqueIPCIdentityAndPayloadOwnership(t *testing.T) {
	var identities []string
	buffer := []byte("original")
	c := newControl(t, controlConfig(), &controlRuntime{invoke: func(_ context.Context, id, method string, _ map[string]string) ([]byte, error) {
		identities = append(identities, id)
		return buffer, nil
	}})
	waitReady(t, c)
	c.handle(t.Context(), controlRequest(c, "commit"))
	payload, failure := c.handle(t.Context(), invokeRequest(c, 1, "init"))
	if failure != "" {
		t.Fatal(failure)
	}
	copy(buffer, []byte("modified"))
	if string(payload) != "original" {
		t.Fatal("response aliased runtime buffer")
	}
	if _, failure = c.handle(t.Context(), invokeRequest(c, 2, "action")); failure != "" {
		t.Fatal(failure)
	}
	if len(identities) != 2 || identities[0] == identities[1] || identities[1] != SupervisorInvocationID(2) {
		t.Fatal(identities)
	}
}
func TestSupervisorRejectsCancelledAndInvalidInvocationBeforeAdmission(t *testing.T) {
	r := &controlRuntime{}
	c := newControl(t, controlConfig(), r)
	waitReady(t, c)
	c.handle(t.Context(), controlRequest(c, "commit"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, failure := c.handle(ctx, invokeRequest(c, 1, "init")); failure != "supervisor_request_cancelled" {
		t.Fatal(failure)
	}
	bad := invokeRequest(c, 1, "init")
	bad.Operation = "unknown"
	if _, failure := c.handle(t.Context(), bad); failure != "supervisor_request_invalid" {
		t.Fatal(failure)
	}
	if r.calls.Load() != 0 {
		t.Fatal("invalid call reached runtime")
	}
	if _, failure := c.handle(t.Context(), invokeRequest(c, 1, "init")); failure != "" {
		t.Fatal("invalid requests consumed identity", failure)
	}
}
func TestSupervisorRuntimeFailureAndOversizedPayloadRetire(t *testing.T) {
	for _, kind := range []string{"panic", "error", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			c := newControl(t, controlConfig(), &controlRuntime{invoke: func(context.Context, string, string, map[string]string) ([]byte, error) {
				switch kind {
				case "panic":
					panic("private data")
				case "error":
					return nil, errors.New("private data")
				default:
					return make([]byte, MaxSupervisorPayload+1), nil
				}
			}})
			waitReady(t, c)
			c.handle(t.Context(), controlRequest(c, "commit"))
			if _, failure := c.handle(t.Context(), invokeRequest(c, 1, "init")); failure != "supervisor_invocation_failed" {
				t.Fatal(failure)
			}
			select {
			case <-c.Done():
			default:
				t.Fatal("runtime uncertainty remained live")
			}
		})
	}
}

func TestSupervisorExpiredCommitAndHealthCannotRevive(t *testing.T) {
	c := newControl(t, controlConfig(), &controlRuntime{})
	waitReady(t, c)
	c.mu.Lock()
	c.prepareUntil = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if _, failure := c.handle(t.Context(), controlRequest(c, "commit")); failure != "supervisor_retiring" {
		t.Fatal(failure)
	}
	for _, op := range []string{"ready", "renew", "commit"} {
		if _, failure := c.handle(t.Context(), controlRequest(c, op)); failure != "supervisor_retiring" {
			t.Fatal(op, failure)
		}
	}
}

func TestSupervisorTLSRejectsHostLeaseRenewalIdentity(t *testing.T) {
	ca, serverCert, brokerCert, _, _ := controlCertificates(t)
	for _, tc := range []struct {
		certificate tls.Certificate
		brokerURI   string
	}{{serverCert, "spiffe://apexload/java-host/host"}, {brokerCert, "spiffe://apexload/java-broker"}, {serverCert, "not-a-spiffe-uri"}} {
		if _, e := SupervisorTLS(tc.certificate, ca, tc.brokerURI); !errors.Is(e, ErrState) {
			t.Fatal("host credential could renew own lease", e)
		}
	}
}

func TestSupervisorRejectsMixedRoleClientIdentity(t *testing.T) {
	c := newControl(t, controlConfig(), &controlRuntime{})
	brokerURI, _ := url.Parse(controlConfig().BrokerClientURI)
	hostURI, _ := url.Parse("spiffe://apexload/java-host/other")
	for _, uris := range [][]*url.URL{nil, {hostURI}, {brokerURI, hostURI}, {brokerURI, brokerURI}} {
		request := httptest.NewRequest("POST", "/v1/java-host", nil)
		request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: uris}}}}
		if c.authorized(request) {
			t.Fatal("mixed/missing broker role authorized")
		}
	}
	request := httptest.NewRequest("POST", "/v1/java-host", nil)
	request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{brokerURI}}}}}
	if !c.authorized(request) {
		t.Fatal("single broker role rejected")
	}
}
