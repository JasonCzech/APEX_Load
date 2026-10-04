package collectoragent

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const serviceBusFixtureSecret = "SERVICEBUS_RESPONSE_KEY_CANARY_92731"
const serviceBusFixtureKey = "controlled-literal-key-not-base64"

type serviceBusHTTPFixtureOptions struct {
	status      int
	body        string
	chunked     bool
	holdBody    bool
	delayedPath string
	delay       time.Duration
}
type serviceBusHTTPFixture struct {
	options                    serviceBusHTTPFixtureOptions
	listener                   net.Listener
	caPath                     string
	mu                         sync.Mutex
	raw                        []net.Conn
	paths                      []string
	failures                   []string
	expires                    []int64
	lateStarted, lateCompleted time.Time
	wg                         sync.WaitGroup
	accepts, active, peak      atomic.Int32
	held                       chan struct{}
}

func newServiceBusHTTPFixture(t *testing.T, options serviceBusHTTPFixtureOptions) *serviceBusHTTPFixture {
	t.Helper()
	if options.status == 0 {
		options.status = 200
	}
	rootPublic, rootPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "controlled Service Bus root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
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
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "namespace.example.test"}, DNSNames: []string{"namespace.example.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	fixture := &serviceBusHTTPFixture{options: options, listener: listener, caPath: caPath, held: make(chan struct{}, 1)}
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
func (f *serviceBusHTTPFixture) fail(message string) {
	f.mu.Lock()
	f.failures = append(f.failures, message)
	f.mu.Unlock()
}
func (f *serviceBusHTTPFixture) serve(raw net.Conn, config *tls.Config) {
	defer f.wg.Done()
	defer func() { _ = raw.Close(); f.active.Add(-1) }()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	connection := tls.Server(raw, config)
	if err := connection.Handshake(); err != nil {
		return
	}
	if connection.ConnectionState().ServerName != "namespace.example.test" || connection.ConnectionState().NegotiatedProtocol != "http/1.1" {
		f.fail("approved HTTP/1.1 TLS identity not used")
		return
	}
	request, err := http.ReadRequest(bufio.NewReader(connection))
	if err != nil {
		f.fail("controlled GET request malformed")
		return
	}
	defer request.Body.Close()
	if request.Method != "GET" || request.Host != "namespace.example.test" || request.ContentLength != 0 || len(request.TransferEncoding) != 0 || !request.Close || request.Header.Get("Accept") != "application/atom+xml" {
		f.fail("reader left fixed read-only HTTP surface")
		return
	}
	if request.URL.RawQuery != "api-version=2021-05" || request.Header.Get("Accept-Encoding") != "" {
		f.fail("API revision or compression contract changed")
		return
	}
	f.mu.Lock()
	f.paths = append(f.paths, request.RequestURI)
	f.mu.Unlock()
	audience := strings.ToLower("https://namespace.example.test" + request.URL.EscapedPath())
	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "SharedAccessSignature ") {
		f.fail("purpose-bound SAS missing")
		return
	}
	fields, err := url.ParseQuery(strings.TrimPrefix(authorization, "SharedAccessSignature "))
	if err != nil || len(fields) != 4 || fields.Get("sr") != audience || fields.Get("skn") != "reader" {
		f.fail("SAS approval audience or principal changed")
		return
	}
	expiry, err := strconv.ParseInt(fields.Get("se"), 10, 64)
	if err != nil || expiry <= time.Now().Unix() || expiry > time.Now().Add(30*time.Second).Unix() {
		f.fail("SAS expiry did not stay short")
		return
	}
	mac := hmac.New(sha256.New, []byte(serviceBusFixtureKey))
	_, _ = mac.Write([]byte(strings.ToLower(url.QueryEscape(audience)) + "\n" + fields.Get("se")))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(fields.Get("sig"))) {
		f.fail("SAS did not sign approved entity with literal key")
		return
	}
	f.mu.Lock()
	f.expires = append(f.expires, expiry)
	f.mu.Unlock()
	body := f.options.body
	if body == "" {
		body = serviceBusFixtureDescription(request.URL.Path, false)
	}
	status := f.options.status
	if f.options.holdBody {
		_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n<")
		select {
		case f.held <- struct{}{}:
		default:
		}
		var b [1]byte
		_, _ = connection.Read(b[:])
		return
	}
	if request.URL.Path == f.options.delayedPath && f.options.delay > 0 {
		f.mu.Lock()
		f.lateStarted = time.Now()
		f.mu.Unlock()
		time.Sleep(f.options.delay)
		f.mu.Lock()
		f.lateCompleted = time.Now()
		f.mu.Unlock()
	}
	header := fmt.Sprintf("HTTP/1.1 %d fixture\r\nContent-Type: application/atom+xml;type=entry;charset=utf-8\r\nConnection: close\r\n", status)
	if status >= 300 && status < 400 {
		header += "Location: https://unapproved.example.test/private\r\n"
	}
	if f.options.chunked {
		header += "Transfer-Encoding: chunked\r\n\r\n"
		_, _ = io.WriteString(connection, header)
		_, _ = fmt.Fprintf(connection, "%x\r\n%s\r\n0\r\n\r\n", len(body), body)
	} else {
		header += fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
		_, _ = io.WriteString(connection, header+body)
	}
	// No second request can be silently retried or reuse this connection.
}
func serviceBusFixtureDescription(path string, subscription bool) string {
	kind := "QueueDescription"
	if subscription {
		kind = "SubscriptionDescription"
	}
	return `<entry xmlns="http://www.w3.org/2005/Atom"><id>https://namespace.example.test` + path + `</id><content type="application/xml"><` + kind + ` xmlns="http://schemas.microsoft.com/netservices/2010/10/servicebus/connect"><MessageCount>9007199254740993</MessageCount><SizeInBytes>9223372036854775807</SizeInBytes><CountDetails><ActiveMessageCount>0</ActiveMessageCount><ScheduledMessageCount>2</ScheduledMessageCount><DeadLetterMessageCount>1</DeadLetterMessageCount><TransferMessageCount>3</TransferMessageCount><TransferDeadLetterMessageCount>4</TransferDeadLetterMessageCount></CountDetails><UpdatedAt>2044-01-01T00:00:00Z</UpdatedAt><AuthorizationRules><Rule><PrimaryKey>` + serviceBusFixtureSecret + `</PrimaryKey></Rule></AuthorizationRules></` + kind + `></content></entry>`
}
func (f *serviceBusHTTPFixture) target(t *testing.T, entities []analysis.ManagementEntity) ManagementTarget {
	t.Helper()
	target := serviceBusConfigTestTarget(t)
	target.TLSCAFile = f.caPath
	if len(entities) > 0 {
		target.Scope.Entities = entities
	}
	if err := prepareServiceBusManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	return target
}
func (f *serviceBusHTTPFixture) dial(ctx context.Context, route serviceBusManagementRoute) (net.Conn, error) {
	if route.serverName != "namespace.example.test" || route.origin != "https://namespace.example.test" || route.address.String() != "127.0.0.1:443" {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	dialer := net.Dialer{KeepAlive: -1}
	dialer.SetMultipathTCP(false)
	return dialer.DialContext(ctx, "tcp4", f.listener.Addr().String()) // Explicit private real-TCP fixture seam; never production JSON.
}
func serviceBusFixturePoll(target ManagementTarget) *managementObservation {
	grant := managementTestGrant("instance")
	grant.Pack = "servicebus-runtime"
	grant.ScopeHash, _ = target.Scope.Hash()
	grant.IdentityHash, _ = target.Identity.Fingerprint()
	credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "shared_key", SharedKey: &ManagementSharedKeyCredentials{KeyName: "reader", Key: serviceBusFixtureKey}}
	return newManagementObservation(target, grant, credentials)
}
func (f *serviceBusHTTPFixture) audit(t *testing.T, poll *managementObservation) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for f.active.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("controlled physical server socket did not close")
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.failures) != 0 {
		t.Fatal("independent read-only HTTPS audit failed", f.failures)
	}
	requests, pages := poll.counts()
	if pages != 0 || requests != int(f.accepts.Load())*2+len(f.paths) {
		t.Fatal("TCP/TLS/GET admission counts do not reconcile")
	}
	if f.peak.Load() > 2 {
		t.Fatal("more than two active physical transports")
	}
	for _, expiry := range f.expires {
		if expiry > poll.grant.AuthorizationExpiresAt.Unix() || expiry > poll.grant.Deadline.Unix() {
			t.Fatal("SAS exceeded signed authorization lease")
		}
	}
}
func TestServiceBusManagementControlledHTTPSExactCountsAndNoDisclosure(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(map[bool]string{false: "length", true: "chunked"}[chunked], func(t *testing.T) {
			fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{chunked: chunked})
			target := fixture.target(t, nil)
			if fixture.accepts.Load() != 0 {
				t.Fatal("configuration contacted target")
			}
			poll := serviceBusFixturePoll(target)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			snapshot, err := (serviceBusManagementObserver{dial: fixture.dial}).observe(ctx, poll)
			poll.finish()
			if err != nil {
				t.Fatal(err)
			}
			if err := snapshot.ValidateFor(target.Scope, target.Identity); err != nil {
				t.Fatal(err)
			}
			if !snapshot.InventoryComplete || snapshot.Incomplete || len(snapshot.Rows) != 1 {
				t.Fatal("complete queue inventory lost")
			}
			row := snapshot.Rows[0]
			runtime := row.ServiceBusRuntime
			if *runtime.Total.Value != "9007199254740993" || *runtime.Active.Value != "0" || *runtime.SizeBytes.Value != "9223372036854775807" {
				t.Fatal("exact counts or observed zero changed")
			}
			if runtime.Deferred.Value != nil || runtime.Deferred.State != analysis.ManagementStateUnsupported {
				t.Fatal("deferred count inferred from overlapping counters")
			}
			if row.ObservedAt == nil || row.ObservedAt.Year() == 2044 || row.ObservationAgeMS == nil {
				t.Fatal("static provider timestamp became counter capture time")
			}
			encoded, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), serviceBusFixtureSecret) || strings.Contains(string(encoded), serviceBusFixtureKey) || strings.Contains(string(encoded), "SharedAccessSignature") {
				t.Fatal("provider/credential canary disclosed")
			}
			fixture.audit(t, poll)
		})
	}
}
func TestServiceBusManagementStatusesAreUnavailableWithoutRetryOrRedirect(t *testing.T) {
	for _, test := range []struct {
		status int
		reason string
	}{{302, "permission_denied"}, {401, "permission_denied"}, {403, "permission_denied"}, {404, "not_observed"}, {429, "throttled"}, {503, "not_observed"}} {
		t.Run(strconv.Itoa(test.status), func(t *testing.T) {
			fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{status: test.status})
			poll := serviceBusFixturePoll(fixture.target(t, nil))
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			snapshot, err := (serviceBusManagementObserver{dial: fixture.dial}).observe(ctx, poll)
			poll.finish()
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.InventoryComplete || !snapshot.Incomplete || snapshot.Rows[0].State == analysis.ManagementStateObserved || snapshot.Rows[0].Reason != test.reason || fixture.accepts.Load() != 1 {
				t.Fatal("status was treated as observed or retried")
			}
			fixture.audit(t, poll)
		})
	}
}
func TestServiceBusManagementSubscriptionFieldsRemainUnsupported(t *testing.T) {
	path := "/orders/Subscriptions/worker"
	fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{body: serviceBusFixtureDescription(strings.ToLower(path), true)})
	entities := []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Topic: "orders", Subscription: "worker"}}}
	poll := serviceBusFixturePoll(fixture.target(t, entities))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	snapshot, err := (serviceBusManagementObserver{dial: fixture.dial}).observe(ctx, poll)
	poll.finish()
	if err != nil {
		t.Fatal(err)
	}
	runtime := snapshot.Rows[0].ServiceBusRuntime
	if runtime.Scheduled.State != analysis.ManagementStateUnsupported || runtime.SizeBytes.State != analysis.ManagementStateUnsupported || runtime.Deferred.State != analysis.ManagementStateUnsupported {
		t.Fatal("unqualified subscription or independent deferred counts promoted")
	}
	fixture.audit(t, poll)
}
func TestServiceBusManagementLaterEntityCannotFreshenEarlierCapture(t *testing.T) {
	fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{delayedPath: "/second", delay: 60 * time.Millisecond})
	entities := []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "first"}}, {ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "second"}}}
	poll := serviceBusFixturePoll(fixture.target(t, entities))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	snapshot, err := (serviceBusManagementObserver{dial: fixture.dial}).observe(ctx, poll)
	poll.finish()
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	lateStarted, lateCompleted := fixture.lateStarted, fixture.lateCompleted
	fixture.mu.Unlock()
	first := snapshot.Rows[0]
	if lateStarted.IsZero() || first.ObservedAt == nil || !first.ObservedAt.Before(lateStarted.UTC()) || first.ObservationAgeMS == nil || *first.ObservationAgeMS < lateCompleted.Sub(lateStarted).Milliseconds() {
		t.Fatal("later entity refreshed earlier source evidence")
	}
	fixture.audit(t, poll)
}
func TestServiceBusManagementBudgetSkipsRemainingWithoutTraffic(t *testing.T) {
	fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{})
	poll := serviceBusFixturePoll(fixture.target(t, nil))
	poll.requests = 510
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	snapshot, err := (serviceBusManagementObserver{dial: fixture.dial}).observe(ctx, poll)
	poll.finish()
	if err != nil {
		t.Fatal(err)
	}
	if fixture.accepts.Load() != 0 || snapshot.Rows[0].Reason != "budget_exhausted" || snapshot.RequestCount != 510 {
		t.Fatal("remaining setup/GET budget exceeded")
	}
}
func TestServiceBusManagementCancellationRetainsUntilPhysicalClose(t *testing.T) {
	fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{holdBody: true})
	poll := serviceBusFixturePoll(fixture.target(t, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	closed := make(chan struct{})
	unblock := make(chan struct{})
	dial := func(ctx context.Context, route serviceBusManagementRoute) (net.Conn, error) {
		raw, err := fixture.dial(ctx, route)
		if err != nil {
			return nil, err
		}
		return &kafkaControlledCloseConn{Conn: raw, entered: closed, unblock: unblock}, nil
	}
	done := make(chan error, 1)
	joined := make(chan struct{})
	var unblockOnce sync.Once
	unblockClose := func() { unblockOnce.Do(func() { close(unblock) }) }
	// Failure cleanup is installed before launching the observer. It always
	// unblocks actual raw Close and joins retained ownership before fixture reap.
	defer func() {
		cancel()
		unblockClose()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("cancel fixture observer/physical ownership did not join")
		}
	}()
	go func() {
		defer close(joined)
		_, err := (serviceBusManagementObserver{dial: dial}).observe(ctx, poll)
		poll.finish()
		done <- err
	}()
	select {
	case <-fixture.held:
	case <-time.After(time.Second):
		t.Fatal("fixture did not enter held response body")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("owned cancellation did not enter raw Close")
	}
	select {
	case <-done:
		t.Fatal("cancelled observer returned before physical close")
	case <-time.After(30 * time.Millisecond):
	}
	unblockClose()
	select {
	case err := <-done:
		if kafkaManagementErrorReason(err) != "cancelled" {
			t.Fatal("cancel reason lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("physical close and actual callbacks did not join")
	}
	fixture.audit(t, poll)
}

func TestServiceBusManagementStaticAndFeedResponsesStayMissing(t *testing.T) {
	for _, body := range []string{
		`<entry xmlns="http://www.w3.org/2005/Atom"><content type="application/xml"><QueueDescription xmlns="http://schemas.microsoft.com/netservices/2010/10/servicebus/connect"><UpdatedAt>2044-01-01T00:00:00Z</UpdatedAt></QueueDescription></content></entry>`,
		`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>https://unapproved.example.test/discovered</id></entry></feed>`,
	} {
		fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{body: body})
		poll := serviceBusFixturePoll(fixture.target(t, nil))
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		snapshot, err := (serviceBusManagementObserver{dial: fixture.dial}).observe(ctx, poll)
		cancel()
		poll.finish()
		if err != nil {
			t.Fatal(err)
		}
		row := snapshot.Rows[0]
		if snapshot.InventoryComplete || !snapshot.Incomplete || row.ObservedAt != nil || row.State == analysis.ManagementStateObserved || row.ServiceBusRuntime.Active.Value != nil || fixture.accepts.Load() != 1 {
			t.Fatal("static metadata or feed manufactured observations/discovery")
		}
		fixture.audit(t, poll)
	}
}

func TestServiceBusManagementForgedEntityRouteNeverDials(t *testing.T) {
	fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{})
	target := fixture.target(t, nil)
	poll := serviceBusFixturePoll(target)
	route, err := serviceBusRouteForEntity(&target, target.Scope.Entities[0])
	if err != nil {
		t.Fatal(err)
	}
	route.requestPath = "/orders/messages/head?api-version=2021-05"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, _, _, err = (serviceBusManagementObserver{dial: fixture.dial}).readEntity(ctx, poll, route, serviceBusManagementBodyLimit)
	poll.finish()
	if kafkaManagementErrorReason(err) != "permission_denied" || fixture.accepts.Load() != 0 {
		t.Fatal("fixed entity allowlist bypass emitted target traffic")
	}
}
func TestServiceBusManagementFirstCloseOwnerAndUnprovenError(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(map[bool]string{false: "unproven", true: "acknowledged"}[acknowledged], func(t *testing.T) {
			poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
			release, err := poll.retainResource()
			if err != nil {
				t.Fatal(err)
			}
			raw := &kafkaControlledCloseConn{entered: make(chan struct{}), unblock: make(chan struct{}), closeError: io.ErrClosedPipe, acknowledged: acknowledged}
			connection := &serviceBusManagementConnection{raw: raw, release: release}
			first, second, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var unblockOnce sync.Once
			unblock := func() { unblockOnce.Do(func() { close(raw.unblock) }) }
			// Register failure cleanup before launching any owned goroutine.
			// The once-only controlled proof follows actual first Close return;
			// a failed second-close assertion cannot strand the first owner.
			defer func() {
				unblock()
				select {
				case <-first:
					release() // Explicit controlled process-reap acknowledgment.
				case <-time.After(time.Second):
					t.Error("first physical Close did not join during fixture reap")
					return
				}
				for _, done := range []chan struct{}{second, finished} {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("owned fixture close/completion did not join during reap")
					}
				}
			}()
			go func() { _ = connection.close(); close(first) }()
			go func() { _ = connection.close(); close(second) }()
			go func() { poll.finish(); close(finished) }()
			select {
			case <-raw.entered:
			case <-time.After(time.Second):
				t.Fatal("first close owner did not enter physical close")
			}
			select {
			case <-second:
				t.Fatal("second close bypassed first physical owner")
			case <-time.After(30 * time.Millisecond):
			}
			unblock()
			for _, done := range []chan struct{}{first, second} {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("concurrent close did not join first physical owner")
				}
			}
			if acknowledged {
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("physical acknowledgment did not release ownership")
				}
			} else {
				select {
				case <-finished:
					t.Fatal("unproven close error released source ownership")
				case <-time.After(30 * time.Millisecond):
				}
				release()
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("controlled process reap did not join completion")
				}
			}
			if raw.calls.Load() != 1 {
				t.Fatal("physical close repeated independently")
			}
		})
	}
}

func TestServiceBusManagementSignedAuthorityPreflightEmitsNoTraffic(t *testing.T) {
	for _, lifetime := range []time.Duration{-time.Second, 500 * time.Millisecond} {
		t.Run(map[bool]string{true: "expired", false: "too short"}[lifetime < 0], func(t *testing.T) {
			fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{})
			poll := serviceBusFixturePoll(fixture.target(t, nil))
			poll.grant.AuthorizationExpiresAt = time.Now().Add(lifetime)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snapshot, err := (serviceBusManagementObserver{dial: fixture.dial}).observe(ctx, poll)
			poll.finish()
			if err != nil || snapshot.Rows[0].Reason != "timeout" || fixture.accepts.Load() != 0 {
				t.Fatal("insufficient signed authorization contacted target before rejection", err)
			}
			fixture.mu.Lock()
			paths := len(fixture.paths)
			fixture.mu.Unlock()
			requests, _ := poll.counts()
			if requests != 0 || paths != 0 {
				t.Fatal("signed authorization preflight admitted target setup or GET")
			}
		})
	}
}

func TestServiceBusManagementSignedExpiryCancelsHeldResponse(t *testing.T) {
	fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{holdBody: true})
	poll := serviceBusFixturePoll(fixture.target(t, nil))
	// The outer context deliberately remains valid beyond signed authority.
	poll.grant.AuthorizationExpiresAt = time.Now().Add(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	target := poll.target
	route, err := serviceBusRouteForEntity(&target, target.Scope.Entities[0])
	if err != nil {
		t.Fatal(err)
	}
	returned := make(chan error, 1)
	go func() {
		_, _, _, err := (serviceBusManagementObserver{dial: fixture.dial}).readEntity(ctx, poll, route, serviceBusManagementBodyLimit)
		poll.finish()
		returned <- err
	}()
	select {
	case <-fixture.held:
	case <-time.After(time.Second):
		t.Fatal("authorized reader did not enter held response")
	}
	select {
	case err := <-returned:
		if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || time.Now().Before(poll.grant.AuthorizationExpiresAt) {
			t.Fatal("signed authorization expiry did not cancel owned response read", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("response read continued beyond signed authority")
	}
	fixture.audit(t, poll)
}

type serviceBusUnprovenFixtureConn struct {
	net.Conn
	acknowledged bool
}

func (c *serviceBusUnprovenFixtureConn) Close() error {
	if c.acknowledged {
		_ = c.Conn.Close()
	}
	return io.ErrClosedPipe
}
func (c *serviceBusUnprovenFixtureConn) physicalClosed() bool { return c.acknowledged }

func TestServiceBusManagementUnprovenCloseStopsLaterAdmissionAndCompletion(t *testing.T) {
	for _, status := range []int{200, 403} {
		for _, acknowledged := range []bool{false, true} {
			t.Run(strconv.Itoa(status)+"/"+map[bool]string{false: "unproven", true: "proven"}[acknowledged], func(t *testing.T) {
				fixture := newServiceBusHTTPFixture(t, serviceBusHTTPFixtureOptions{status: status})
				entities := []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "first"}}, {ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "second"}}}
				poll := serviceBusFixturePoll(fixture.target(t, entities))
				var retained []net.Conn
				dial := func(ctx context.Context, route serviceBusManagementRoute) (net.Conn, error) {
					raw, err := fixture.dial(ctx, route)
					if err != nil {
						return nil, err
					}
					retained = append(retained, raw)
					return &serviceBusUnprovenFixtureConn{Conn: raw, acknowledged: acknowledged}, nil
				}
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				snapshot, err := (serviceBusManagementObserver{dial: dial}).observe(ctx, poll)
				var reapOnce sync.Once
				reap := func() {
					reapOnce.Do(func() {
						if !acknowledged {
							// Explicit controlled process-reap simulation. Each
							// first failed Close retained exactly one resource.
							for _, raw := range retained {
								_ = raw.Close()
								poll.calls.Done()
							}
						}
					})
				}
				defer reap()
				if acknowledged {
					poll.finish()
					if err != nil || snapshot == nil || fixture.accepts.Load() != 2 {
						t.Fatal("proven error-after-close incorrectly retained or stopped admission", err)
					}
					fixture.audit(t, poll)
					return
				}
				var unproven *serviceBusManagementUnprovenCloseError
				if !errors.As(err, &unproven) || snapshot != nil || fixture.accepts.Load() != 1 {
					t.Fatal("unproven cleanup was hidden or followed by another entity", err)
				}
				finished := make(chan struct{})
				go func() { poll.finish(); close(finished) }()
				select {
				case <-finished:
					t.Fatal("unproven destruction confirmed poll completion")
				case <-time.After(20 * time.Millisecond):
				}
				// The controlled test independently destroys its physical socket,
				// modeling process reap. No production Close retry fabricates proof.
				reap()
				<-finished
				fixture.audit(t, poll)
			})
		}
	}
}
