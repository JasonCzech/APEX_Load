package collectoragent

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/analysis"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const solaceFixtureSecret = "SOLACE_CREDENTIAL_RESPONSE_CANARY_52764"
const solaceFixtureCookie = "SOLACE_SESSION_CANARY_83597"

type solaceHTTPFixtureOptions struct {
	holdAfter     int
	password      string
	missingCookie bool
	failLogout    bool
	holdBody      bool
	logoutDelay   time.Duration
}
type solaceHTTPFixture struct {
	options                        solaceHTTPFixtureOptions
	listener                       net.Listener
	caPath                         string
	mu                             sync.Mutex
	raw                            []net.Conn
	paths, failures                []string
	basic, cookie, logout          int
	sessions                       map[string]bool
	logoutStarted, logoutCompleted time.Time
	wg                             sync.WaitGroup
	accepts, active, peak          atomic.Int32
	held                           chan struct{}
}

func newSolaceHTTPFixture(t *testing.T, options solaceHTTPFixtureOptions) *solaceHTTPFixture {
	t.Helper()
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "controlled Solace root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPublic, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "broker.example.test"}, DNSNames: []string{"broker.example.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, public, rootPrivate)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &solaceHTTPFixture{options: options, listener: listener, caPath: caPath, held: make(chan struct{}, 1), sessions: map[string]bool{}}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: private}}}
	fixture.wg.Add(1)
	go func() {
		defer fixture.wg.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			fixture.mu.Lock()
			fixture.raw = append(fixture.raw, raw)
			fixture.mu.Unlock()
			fixture.accepts.Add(1)
			active := fixture.active.Add(1)
			for {
				peak := fixture.peak.Load()
				if active <= peak || fixture.peak.CompareAndSwap(peak, active) {
					break
				}
			}
			fixture.wg.Add(1)
			go fixture.serve(raw, tlsConfig)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		fixture.mu.Lock()
		for _, raw := range fixture.raw {
			_ = raw.Close()
		}
		fixture.mu.Unlock()
		fixture.wg.Wait()
	})
	return fixture
}
func (f *solaceHTTPFixture) password() string {
	if f.options.password != "" {
		return f.options.password
	}
	return solaceFixtureSecret
}
func (f *solaceHTTPFixture) fail(message string) {
	f.mu.Lock()
	f.failures = append(f.failures, message)
	f.mu.Unlock()
}
func (f *solaceHTTPFixture) serve(raw net.Conn, config *tls.Config) {
	defer f.wg.Done()
	defer func() { _ = raw.Close(); f.active.Add(-1) }()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	connection := tls.Server(raw, config)
	if connection.Handshake() != nil {
		return
	}
	if connection.ConnectionState().ServerName != "broker.example.test" || connection.ConnectionState().NegotiatedProtocol != "http/1.1" {
		f.fail("TLS identity or protocol changed")
		return
	}
	request, err := http.ReadRequest(bufio.NewReader(connection))
	if err != nil {
		f.fail("HTTP request malformed")
		return
	}
	defer request.Body.Close()
	if request.Host != "broker.example.test:943" || !request.Close || request.Header.Get("Accept") != "application/json" || len(request.TransferEncoding) != 0 || request.Header.Get("Accept-Encoding") != "" {
		f.fail("fixed HTTP/1.1 surface changed")
		return
	}
	f.mu.Lock()
	f.paths = append(f.paths, request.RequestURI)
	f.mu.Unlock()
	logout := request.Method == "PUT"
	if logout {
		if request.RequestURI != "/SEMP/v2/action/about/user/logout" || request.Header.Get("Cookie") != "Session="+solaceFixtureCookie || request.Header.Get("Authorization") != "" || request.ContentLength != 2 || request.Header.Get("Content-Type") != "application/json" {
			f.fail("logout left approved own-session action")
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, 3))
		defer clear(body)
		if err != nil || string(body) != "{}" {
			f.fail("logout had arbitrary action input")
			return
		}
		f.mu.Lock()
		f.logout++
		exists := f.sessions[solaceFixtureCookie]
		f.logoutStarted = time.Now()
		f.mu.Unlock()
		if !exists {
			f.fail("logout lacked owned provider session")
			return
		}
		if f.options.logoutDelay > 0 {
			time.Sleep(f.options.logoutDelay)
		}
		status, bodyText := 200, `{"meta":{"responseCode":200,"request":{"method":"PUT"}}}`
		if f.options.failLogout {
			status = 503
			bodyText = `{"meta":{"error":{"code":14,"description":"` + solaceFixtureSecret + `"}}}`
		} else {
			f.mu.Lock()
			delete(f.sessions, solaceFixtureCookie)
			f.mu.Unlock()
		}
		f.mu.Lock()
		f.logoutCompleted = time.Now()
		f.mu.Unlock()
		f.write(connection, status, bodyText, "Session=; Max-Age=-1; Path=/; Secure; HttpOnly")
		return
	}
	if request.Method != "GET" || !strings.HasPrefix(request.URL.Path, "/SEMP/v2/monitor/msgVpns/orders/") || request.ContentLength != 0 || request.Header.Get("Content-Type") != "" {
		f.fail("management mutation or unapproved path")
		return
	}
	setCookie := ""
	authorization := request.Header.Get("Authorization")
	if authorization != "" {
		encoded := strings.TrimPrefix(authorization, "Basic ")
		cleartext, err := base64.StdEncoding.DecodeString(encoded)
		defer clear(cleartext)
		if !strings.HasPrefix(authorization, "Basic ") || err != nil || string(cleartext) != "reader:"+f.password() || request.Header.Get("Cookie") != "" {
			f.fail("Basic identity changed")
			return
		}
		f.mu.Lock()
		f.basic++
		f.sessions[solaceFixtureCookie] = true
		f.mu.Unlock()
		if !f.options.missingCookie {
			setCookie = "Session=" + solaceFixtureCookie + "; Path=/; Secure; HttpOnly"
		}
	} else {
		f.mu.Lock()
		f.cookie++
		exists := f.sessions[solaceFixtureCookie]
		f.mu.Unlock()
		if request.Header.Get("Cookie") != "Session="+solaceFixtureCookie || !exists {
			f.fail("cookie session ownership changed")
			return
		}
	}
	f.mu.Lock()
	getCount := f.basic + f.cookie
	f.mu.Unlock()
	if f.options.holdBody && getCount >= max(1, f.options.holdAfter) {
		header := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n"
		if setCookie != "" {
			header += "Set-Cookie: " + setCookie + "\r\n"
		}
		_, _ = io.WriteString(connection, header+"Content-Length: 1000\r\n\r\n{")
		select {
		case f.held <- struct{}{}:
		default:
		}
		var b [1]byte
		_, _ = connection.Read(b[:])
		return
	}
	name := request.URL.Path[strings.LastIndexByte(request.URL.Path, '/')+1:]
	field, count := "queueName", "0"
	if strings.Contains(request.URL.Path, "/topicEndpoints/") {
		field, count = "topicEndpointName", "9223372036854775807"
	} else if name == "pending" {
		count = "9007199254740993"
	}
	expectedSelect := "msgVpnName," + field + ",deadMsgQueue,msgs.count"
	if request.URL.Query().Get("select") != expectedSelect || len(request.URL.Query()) != 1 {
		f.fail("projection or query left fixed allowlist")
		return
	}
	body := fmt.Sprintf(`{"meta":{"responseCode":200,"request":{"method":"GET"}},"data":{"msgVpnName":"orders","%s":%q,"deadMsgQueue":"dmq","description":%q},"collections":{"msgs":{"count":%s}}}`, field, name, solaceFixtureSecret, count)
	f.write(connection, 200, body, setCookie)
}
func (f *solaceHTTPFixture) write(writer io.Writer, status int, body, cookie string) {
	header := fmt.Sprintf("HTTP/1.1 %d fixture\r\nContent-Type: application/json;charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n", status, len(body))
	if cookie != "" {
		header += "Set-Cookie: " + cookie + "\r\n"
	}
	_, _ = io.WriteString(writer, header+"\r\n"+body)
}
func (f *solaceHTTPFixture) target(t *testing.T) ManagementTarget {
	t.Helper()
	target := solaceConfigTestTarget(t)
	target.TLSCAFile = f.caPath
	target.MinIntervalSeconds = 30
	if err := prepareSolaceManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	return target
}
func (f *solaceHTTPFixture) dial(ctx context.Context, route solaceManagementRoute) (net.Conn, error) {
	if route.origin != "https://broker.example.test:943" || route.serverName != "broker.example.test" || route.address.String() != "127.0.0.1:943" || route.network != "tcp4" {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	address, err := netip.ParseAddrPort(f.listener.Addr().String())
	if err != nil {
		return nil, err
	}
	testRoute := route
	testRoute.address = address
	return dialSolaceManagementTCP(ctx, testRoute)
}
func solaceFixturePoll(t *testing.T, target ManagementTarget) (*managementObservation, *solaceManagementQuarantine, *solaceManagementOriginState) {
	t.Helper()
	grant := managementTestGrant("instance")
	grant.Pack = "solace-semp"
	grant.ScopeHash, _ = target.Scope.Hash()
	grant.IdentityHash, _ = target.Identity.Fingerprint()
	credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "basic", Basic: &ManagementBasicCredentials{Username: "reader", Password: solaceFixtureSecret}}
	poll := newManagementObservation(target, grant, credentials)
	poll.origins = newManagementOriginGate([]AdapterTarget{{Management: &target}})
	q, err := openSolaceManagementQuarantine(filepath.Join(t.TempDir(), solaceManagementQuarantineFile), newCollectorSpoolBudget())
	if err != nil {
		t.Fatal(err)
	}
	if err = poll.origins.bindSolaceQuarantine(q); err != nil {
		t.Fatal(err)
	}
	state, err := solaceManagementState(poll, target.Endpoints[0])
	if err != nil {
		t.Fatal(err)
	}
	return poll, q, state
}
func (f *solaceHTTPFixture) providerRelease(t *testing.T, state *solaceManagementOriginState) {
	t.Helper()
	state.mu.Lock()
	var intent *solaceManagementSessionIntent
	if state.intent != nil {
		copy := *state.intent
		intent = &copy
	}
	state.mu.Unlock()
	if intent == nil {
		return
	}
	// Independent controlled provider fixture ends its actual session before
	// supplying audit authority. Destroying local sockets alone is insufficient.
	f.mu.Lock()
	clear(f.sessions)
	f.mu.Unlock()
	if err := state.auditedProviderSessionRelease(*intent); err != nil {
		t.Error("controlled provider release failed", err)
	}
}
func (f *solaceHTTPFixture) audit(t *testing.T, poll *managementObservation) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for f.active.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("server physical sockets remained active")
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.failures) != 0 {
		t.Fatal("independent HTTPS fixture rejected reader", f.failures)
	}
	requests, pages := poll.counts()
	if pages != 0 || requests != int(f.accepts.Load())*2+len(f.paths) || f.peak.Load() > 2 {
		t.Fatal("actual TCP/TLS/HTTP operation accounting did not reconcile")
	}
}
func TestSolaceManagementControlledTLSUsesOneSessionThenOwnLogout(t *testing.T) {
	fixture := newSolaceHTTPFixture(t, solaceHTTPFixtureOptions{logoutDelay: 200 * time.Millisecond})
	target := fixture.target(t)
	if fixture.accepts.Load() != 0 {
		t.Fatal("preparation contacted broker")
	}
	poll, q, state := solaceFixturePoll(t, target)
	defer fixture.providerRelease(t, state)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	snapshot, err := (solaceManagementObserver{dial: fixture.dial}).observe(ctx, poll)
	if err != nil {
		t.Fatal(err)
	}
	poll.finish()
	fixture.audit(t, poll)
	if snapshot.ValidateFor(target.Scope, target.Identity) != nil || snapshot.Incomplete || !snapshot.InventoryComplete {
		t.Fatal("complete valid individual evidence lost")
	}
	for _, row := range snapshot.Rows {
		expected := "0"
		if row.Entity.Solace.Queue == "pending" {
			expected = "9007199254740993"
		} else if row.Entity.Solace.TopicEndpoint != "" {
			expected = "9223372036854775807"
		}
		if row.SolaceSEMP.Depth.Value == nil || *row.SolaceSEMP.Depth.Value != expected || row.SolaceSEMP.DeadMessageQueue != "dmq" {
			t.Fatal("exact depth or approved DMQ changed")
		}
		fixture.mu.Lock()
		boundary := fixture.logoutStarted
		fixture.mu.Unlock()
		if row.ObservedAt == nil || !row.ObservedAt.Before(boundary) || row.ObservationAgeMS == nil || *row.ObservationAgeMS < 200 {
			t.Fatal("logout refreshed old entity capture time")
		}
	}
	fixture.mu.Lock()
	basic, cookies, logout, remaining := fixture.basic, fixture.cookie, fixture.logout, len(fixture.sessions)
	fixture.mu.Unlock()
	if basic != 1 || cookies != 2 || logout != 1 || remaining != 0 || q.blocked(target.Endpoints[0]) {
		t.Fatal("one-session reuse or provider logout proof changed")
	}
	wire, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), solaceFixtureSecret) || strings.Contains(string(wire), solaceFixtureCookie) {
		t.Fatal("credential/session/provider body leaked into evidence")
	}
}
func TestSolaceManagementMissingCookieAndFailedLogoutPersistAcrossRestart(t *testing.T) {
	for _, options := range []solaceHTTPFixtureOptions{{missingCookie: true}, {failLogout: true}} {
		t.Run(fmt.Sprintf("missing=%v", options.missingCookie), func(t *testing.T) {
			fixture := newSolaceHTTPFixture(t, options)
			target := fixture.target(t)
			poll, q, state := solaceFixturePoll(t, target)
			defer fixture.providerRelease(t, state)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			snapshot, err := (solaceManagementObserver{dial: fixture.dial}).observe(ctx, poll)
			if err == nil || snapshot != nil || !q.blocked(target.Endpoints[0]) {
				t.Fatal("unresolved remote session became completed evidence")
			}
			body, err := os.ReadFile(q.path)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(body)
			if strings.Contains(string(body), solaceFixtureSecret) || strings.Contains(string(body), solaceFixtureCookie) {
				t.Fatal("cookie or credentials entered durable intent")
			}
			restarted, err := openSolaceManagementQuarantine(q.path, newCollectorSpoolBudget())
			if err != nil {
				t.Fatal(err)
			}
			next := newManagementObservation(target, managementTestGrant("fresh-instance"), poll.credentials)
			next.grant.Pack = "solace-semp"
			next.grant.IdentityHash = poll.grant.IdentityHash
			next.grant.PollID = "fresh-poll"
			next.origins = newManagementOriginGate([]AdapterTarget{{Management: &target}})
			if err = next.origins.bindSolaceQuarantine(restarted); err != nil {
				t.Fatal(err)
			}
			before := fixture.accepts.Load()
			if got, err := (solaceManagementObserver{dial: fixture.dial}).observe(ctx, next); err == nil || got != nil || fixture.accepts.Load() != before {
				t.Fatal("fresh observer/store silently authenticated past quarantine")
			}
			next.finish()
			finished := make(chan struct{})
			go func() { poll.finish(); close(finished) }()
			defer func() {
				fixture.providerRelease(t, state)
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Error("audited provider fixture cleanup did not join")
				}
			}()
			select {
			case <-finished:
				t.Fatal("socket closure alone released remote provider ownership")
			case <-time.After(20 * time.Millisecond):
			}
			fixture.providerRelease(t, state)
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("provider session release did not drain ownership")
			}
			fixture.audit(t, poll)
		})
	}
}
func TestSolaceManagementSignedAuthorityPreflightEmitsNoTraffic(t *testing.T) {
	for _, ttl := range []time.Duration{-time.Second, 500 * time.Millisecond, 3 * time.Second} {
		t.Run(ttl.String(), func(t *testing.T) {
			fixture := newSolaceHTTPFixture(t, solaceHTTPFixtureOptions{})
			target := fixture.target(t)
			poll, q, _ := solaceFixturePoll(t, target)
			poll.grant.AuthorizationExpiresAt = time.Now().Add(ttl)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			got, err := (solaceManagementObserver{dial: fixture.dial}).observe(ctx, poll)
			poll.finish()
			requests, _ := poll.counts()
			if err == nil || got != nil || requests != 0 || fixture.accepts.Load() != 0 || q.blocked(target.Endpoints[0]) {
				t.Fatal("short signed authority created intent or contacted target")
			}
		})
	}
}
func TestSolaceManagementCancelledReadLogsOutOnlyWithinAuthority(t *testing.T) {
	// Establish the owned cookie before holding a later body. Cancellation
	// before the first cookie is received cannot prove a logout identity.
	fixture := newSolaceHTTPFixture(t, solaceHTTPFixtureOptions{holdBody: true, holdAfter: 2})
	target := fixture.target(t)
	poll, q, state := solaceFixturePoll(t, target)
	defer fixture.providerRelease(t, state)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := (solaceManagementObserver{dial: fixture.dial}).observe(ctx, poll); done <- err }()
	select {
	case <-fixture.held:
	case <-time.After(time.Second):
		t.Fatal("controlled response did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled read not classified", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled read/owned logout did not join")
	}
	poll.finish()
	if q.blocked(target.Endpoints[0]) {
		t.Fatal("same-authority successful logout left quarantine")
	}
	fixture.mu.Lock()
	logout := fixture.logout
	fixture.mu.Unlock()
	if logout != 1 {
		t.Fatal("cancellation lost owned bounded logout")
	}
	fixture.audit(t, poll)
}

func TestSolaceManagementMaxValidBasicBundleDoesNotQuarantine(t *testing.T) {
	password := strings.Repeat("p", 8192)
	fixture := newSolaceHTTPFixture(t, solaceHTTPFixtureOptions{password: password})
	target := fixture.target(t)
	poll, q, state := solaceFixturePoll(t, target)
	defer fixture.providerRelease(t, state)
	poll.credentials.Basic.Password = password
	header, err := solaceBasicAuthorization(poll.credentials, target.Identity)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(header)
	if len(header) <= 8<<10 {
		t.Fatal("regression did not exercise previously rejected valid Basic size")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	snapshot, err := (solaceManagementObserver{dial: fixture.dial}).observe(ctx, poll)
	if err != nil || snapshot == nil || q.blocked(target.Endpoints[0]) {
		t.Fatal("valid maximum credential was falsely quarantined", err)
	}
	poll.finish()
	fixture.audit(t, poll)
}

func TestSolaceManagementReservedWorkCutoffPublishesQualifiedPartialRows(t *testing.T) {
	fixture := newSolaceHTTPFixture(t, solaceHTTPFixtureOptions{holdBody: true, holdAfter: 2})
	target := fixture.target(t)
	poll, q, state := solaceFixturePoll(t, target)
	defer fixture.providerRelease(t, state)
	// Signed authority is independent of the parent and leaves the fixed
	// two-second cleanup reserve after the shorter work budget elapses.
	poll.grant.AuthorizationExpiresAt = time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	snapshot, err := (solaceManagementObserver{dial: fixture.dial}).observe(ctx, poll)
	if err != nil || snapshot == nil {
		t.Fatal("internal work cutoff discarded prior successful evidence", err)
	}
	poll.finish()
	fixture.audit(t, poll)
	observed, budget := 0, 0
	for _, row := range snapshot.Rows {
		if row.State == analysis.ManagementStateObserved {
			observed++
			if row.ObservedAt == nil || row.SolaceSEMP.Depth.Value == nil {
				t.Fatal("qualified earlier row lost its exact capture")
			}
		} else if row.Reason == "budget_exhausted" {
			budget++
		}
	}
	fixture.mu.Lock()
	logout, remaining := fixture.logout, len(fixture.sessions)
	fixture.mu.Unlock()
	if observed != 1 || budget != 2 || !snapshot.Incomplete || snapshot.InventoryComplete || logout != 1 || remaining != 0 || q.blocked(target.Endpoints[0]) {
		t.Fatal("partial coverage or independently proven logout changed")
	}
	if ctx.Err() != nil || !poll.grant.AuthorizationExpiresAt.After(snapshot.CompletedAt) {
		t.Fatal("fixture did not distinguish internal work cutoff from actual authority expiry")
	}
}

// The raw descriptor closes successfully, but its sole local proof is delayed.
// This delay must remain in evidence age, rather than refresh the depth sample.
type solaceDelayedCaptureCloseConn struct {
	net.Conn
	onClose func(time.Time)
}

func (c *solaceDelayedCaptureCloseConn) Close() error {
	c.onClose(time.Now())
	err := c.Conn.Close()
	time.Sleep(120 * time.Millisecond)
	return err
}
func TestSolaceManagementCapturePrecedesPhysicalGETClose(t *testing.T) {
	fixture := newSolaceHTTPFixture(t, solaceHTTPFixtureOptions{})
	target := fixture.target(t)
	poll, _, state := solaceFixturePoll(t, target)
	defer fixture.providerRelease(t, state)
	var mu sync.Mutex
	closed := make(map[string]time.Time)
	dial := func(ctx context.Context, route solaceManagementRoute) (net.Conn, error) {
		conn, err := fixture.dial(ctx, route)
		if err != nil {
			return conn, err
		}
		key, _ := route.entity.ID()
		return &solaceDelayedCaptureCloseConn{Conn: conn, onClose: func(at time.Time) {
			mu.Lock()
			defer mu.Unlock()
			if _, exists := closed[key]; exists {
				t.Error("descriptor had more than one physical closer")
			}
			closed[key] = at
		}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	snapshot, err := (solaceManagementObserver{dial: dial}).observe(ctx, poll)
	if err != nil {
		t.Fatal(err)
	}
	poll.finish()
	fixture.audit(t, poll)
	if snapshot.Incomplete || !snapshot.InventoryComplete {
		t.Fatal("controlled depth evidence became incomplete")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, row := range snapshot.Rows {
		key, _ := row.Entity.ID()
		boundary, ok := closed[key]
		if !ok || row.ObservedAt == nil || !row.ObservedAt.Before(boundary) {
			t.Fatal("successful GET physical cleanup refreshed the capture timestamp")
		}
		if row.ObservationAgeMS == nil || *row.ObservationAgeMS < 120 {
			t.Fatal("successful GET cleanup was omitted from source observation age")
		}
	}
}
