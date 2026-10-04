package collectoragent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Emit only stable finite classifications. Provider descriptions, arbitrary
// wrapped errors, usernames, endpoints and credential material never appear.
func kafkaTransactionSetupErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	var protocol *kerr.Error
	if errors.As(err, &protocol) {
		return fmt.Sprintf("kafka_code_%d", protocol.Code)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, kgo.ErrClientClosed):
		return "client_closed"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection_eof"
	}
	var network net.Error
	if errors.As(err, &network) {
		if network.Timeout() {
			return "network_timeout"
		}
		return "network_error"
	}
	return "unclassified"
}

// This client exists only in a genuine-fixture test. Mutations use the separate
// anonymous SETUP superuser listener, never management SASL credentials or the
// audited collector listener. The production reader does not construct kgo.
type kafkaTransactionSetupDialer struct {
	address    netip.AddrPort
	mu         sync.Mutex
	sockets    map[*kafkaTransactionSetupConn]bool
	unapproved bool
	closing    bool
	dials      sync.WaitGroup
	connect    func(context.Context, string, netip.AddrPort) (net.Conn, error)
}
type kafkaTransactionSetupConn struct {
	net.Conn
	owner *kafkaTransactionSetupDialer
	once  sync.Once
	err   error
}

func (c *kafkaTransactionSetupConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		if c.err == nil {
			c.owner.mu.Lock()
			delete(c.owner.sockets, c)
			c.owner.mu.Unlock()
		}
	})
	return c.err
}
func (d *kafkaTransactionSetupDialer) dial(ctx context.Context, _, host string) (net.Conn, error) {
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		return nil, context.Canceled
	}
	// Reap fences future admission under the same lock before waiting; no
	// WaitGroup Add can race the ownership join, including in-flight connects
	// that kgo.Close has not yet registered as broker connections.
	d.dials.Add(1)
	d.mu.Unlock()
	defer d.dials.Done()
	// The disposable broker advertises localhost:9094 on SETUP while its host
	// port is the explicitly validated loopback fixture base port plus two.
	// Only these two exact aliases are admitted.
	// No broker-discovered hostname receives ambient DNS or automatic fallback.
	if host != d.address.String() && host != "localhost:9094" {
		d.mu.Lock()
		d.unapproved = true
		d.mu.Unlock()
		return nil, errors.New("fixture setup broker route was not approved")
	}
	var mu sync.Mutex
	attempted := false
	network := "tcp4"
	if d.address.Addr().Is6() {
		network = "tcp6"
	}
	dialer := net.Dialer{KeepAlive: -1, ControlContext: func(_ context.Context, actualNetwork, address string, _ syscall.RawConn) error {
		mu.Lock()
		defer mu.Unlock()
		if attempted || actualNetwork != network || address != d.address.String() {
			return errors.New("fixture setup socket route changed")
		}
		attempted = true
		return nil
	}}
	dialer.SetMultipathTCP(false)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	connect := d.connect
	if connect == nil {
		connect = func(ctx context.Context, network string, address netip.AddrPort) (net.Conn, error) {
			return dialer.DialTCP(ctx, network, netip.AddrPort{}, address)
		}
	}
	raw, err := connect(ctx, network, d.address)
	if err != nil {
		return nil, err
	}
	connection := &kafkaTransactionSetupConn{Conn: raw, owner: d}
	d.mu.Lock()
	d.sockets[connection] = true
	closing := d.closing
	d.mu.Unlock()
	if closing {
		// Socket acquisition raced shutdown. Close it while this actual dial
		// remains charged; unexplained close errors remain in the registry.
		_ = connection.Close()
		return nil, context.Canceled
	}
	return connection, nil
}
func (d *kafkaTransactionSetupDialer) reap(t *testing.T) {
	t.Helper()
	d.mu.Lock()
	d.closing = true
	d.mu.Unlock()
	d.dials.Wait()
	d.mu.Lock()
	sockets := make([]*kafkaTransactionSetupConn, 0, len(d.sockets))
	for connection := range d.sockets {
		sockets = append(sockets, connection)
	}
	unapproved := d.unapproved
	d.mu.Unlock()
	if unapproved {
		t.Error("independent setup attempted an unapproved broker identity")
	}
	for _, connection := range sockets {
		if err := connection.Close(); err != nil {
			t.Error("fixture setup physical socket close was not proven")
		}
	}
	d.mu.Lock()
	remaining := len(d.sockets)
	d.mu.Unlock()
	if remaining != 0 {
		t.Error("fixture setup physical socket ownership remained after reap")
	}
}

// Validate the fixture's three distinct physical listeners. The CLI may choose
// another base port; discovery still cannot change the explicitly pinned route.
func kafkaTransactionSetupAddress(settings kafkaManagementBrokerFixture) (netip.AddrPort, error) {
	endpoint, err := url.Parse(settings.Endpoint)
	if err != nil || endpoint.Scheme != "tls" || endpoint.Hostname() != "localhost" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return netip.AddrPort{}, errors.New("invalid fixture management identity")
	}
	port, err := strconv.ParseUint(endpoint.Port(), 10, 16)
	if err != nil || port == 0 || port > 65533 {
		return netip.AddrPort{}, errors.New("invalid fixture listener port")
	}
	proxy, proxyErr := netip.ParseAddrPort(settings.DialAddress)
	broker, brokerErr := netip.ParseAddrPort(settings.BrokerAddress)
	setup, setupErr := netip.ParseAddrPort(settings.SetupAddress)
	if proxyErr != nil || brokerErr != nil || setupErr != nil || !proxy.Addr().IsLoopback() || proxy.Port() != uint16(port) || broker.Addr() != proxy.Addr() || broker.Port() != uint16(port+1) || setup.Addr() != proxy.Addr() || setup.Port() != uint16(port+2) {
		return netip.AddrPort{}, errors.New("fixture SETUP listener does not match approved listener allocation")
	}
	return setup, nil
}

// Run only after all authentication baseline subtests: abort markers advance
// positions, so the original four-record oracle must have been checked first.
func testKafkaManagementActualBrokerOpenTransaction(t *testing.T, settings kafkaManagementBrokerFixture) {
	if settings.SetupAddress == "" {
		t.Skip("genuine independent SETUP transaction listener is not configured")
	}
	setupAddress, err := kafkaTransactionSetupAddress(settings)
	if err != nil {
		t.Fatal("transaction fixture requires its exact static loopback SETUP route")
	}
	if settings.Mechanism != "plain" && settings.Mechanism != "scram_sha_256" && settings.Mechanism != "scram_sha_512" {
		t.Fatal("transaction fixture requires an explicitly validated authentication profile")
	}
	proxy := newKafkaManagementAuditProxy(t, settings)
	target := ManagementTarget{Scope: analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindKafka, Provider: settings.ClusterID, Isolation: "read_committed", Entities: []analysis.ManagementEntity{{Kafka: &analysis.KafkaManagementEntity{Topic: settings.Topic, Partition: 0, Group: settings.Group}}}}, Identity: analysis.ManagementIdentity{Mode: "sasl_plain", Principal: settings.Username, PolicyRevision: "fixture-policy-v1", CredentialRef: "fixture-management", CredentialVersion: "fixture-credentials-v1"}, Endpoints: []string{settings.Endpoint}, TLSCAFile: settings.CAFile, Kafka: &KafkaManagementSettings{ClusterID: settings.ClusterID, DialAddresses: map[string]string{settings.Endpoint: settings.DialAddress}}}
	if settings.Mechanism != "plain" {
		target.Identity.Mode = "sasl_scram"
	}
	if err := prepareKafkaManagementTarget(&target); err != nil {
		t.Fatal("transaction fixture management approval invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	read := func() (hwm, lso, committed int64) {
		proxy.mu.Lock()
		frames, accepts := len(proxy.frames), proxy.accepts
		proxy.mu.Unlock()
		grant := managementTestGrant("instance")
		grant.ScopeHash, _ = target.Scope.Hash()
		grant.IdentityHash, _ = target.Identity.Fingerprint()
		credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "sasl", SASL: &ManagementSASLCredentials{Mechanism: settings.Mechanism, Username: settings.Username, Password: settings.Password}}
		poll := newManagementObservation(target, grant, credentials)
		pollCtx, pollCancel := context.WithTimeout(ctx, 8*time.Second)
		snapshot, err := (kafkaManagementObserver{}).observe(pollCtx, poll)
		poll.finish()
		pollCancel()
		if err != nil || snapshot == nil || snapshot.Incomplete || len(snapshot.Rows) != 1 {
			t.Fatalf("genuine transaction-boundary read unavailable: %s", kafkaManagementErrorReason(err))
		}
		offsets := snapshot.Rows[0].KafkaOffsets
		parse := func(value analysis.ManagementInteger) int64 {
			if value.State != analysis.ManagementStateObserved || value.Value == nil {
				t.Fatal("genuine transaction boundary became unavailable")
			}
			n, err := analysis.ParseManagementInteger(*value.Value)
			if err != nil {
				t.Fatal("genuine transaction boundary was not exact")
			}
			return n
		}
		hwm, lso, committed = parse(offsets.HighWatermarkNext), parse(offsets.LastStableNext), parse(offsets.CommittedNext)
		proxy.audit(t, poll, frames, accepts)
		return
	}
	preHWM, preLSO, preCommitted := read()
	if preHWM != preLSO {
		t.Fatal("transaction fixture started with an unrelated unstable boundary")
	}
	dialer := &kafkaTransactionSetupDialer{address: setupAddress, sockets: map[*kafkaTransactionSetupConn]bool{}}
	client, err := kgo.NewClient(kgo.WithContext(ctx), kgo.SeedBrokers(setupAddress.String()), kgo.Dialer(dialer.dial), kgo.TransactionalID("apex-management-offset-fixture"), kgo.TransactionTimeout(30*time.Second), kgo.RecordPartitioner(kgo.ManualPartitioner()), kgo.MaxBufferedRecords(4), kgo.MaxBufferedBytes(64<<10), kgo.ProducerBatchMaxBytes(64<<10), kgo.BrokerMaxWriteBytes(128<<10), kgo.BrokerMaxReadBytes(256<<10), kgo.FetchMaxBytes(128<<10), kgo.ProduceRequestTimeout(3*time.Second), kgo.RecordRetries(0), kgo.UnknownTopicRetries(0), kgo.RequestRetries(3), kgo.RetryTimeout(3*time.Second), kgo.RequestTimeoutOverhead(time.Second), kgo.RecordDeliveryTimeout(8*time.Second))
	if err != nil {
		t.Fatal("independent SETUP transaction client could not initialize")
	}
	begun := false
	defer func() {
		if begun {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 8*time.Second)
			if err := client.AbortBufferedRecords(cleanupCtx); err != nil {
				t.Errorf("independent transaction buffered-record cleanup failed (class=%s)", kafkaTransactionSetupErrorClass(err))
			}
			if err := client.EndTransaction(cleanupCtx, kgo.TryAbort); err != nil {
				t.Errorf("independent fixture transaction abort was not confirmed (class=%s)", kafkaTransactionSetupErrorClass(err))
			}
			cleanupCancel()
		}
		client.Close() // Join the independently owned client before physical reap.
		dialer.reap(t)
	}()
	if err := client.BeginTransaction(); err != nil {
		t.Fatalf("independent SETUP transaction did not begin (class=%s)", kafkaTransactionSetupErrorClass(err))
	}
	begun = true
	produceCtx, produceCancel := context.WithTimeout(ctx, 8*time.Second)
	record := &kgo.Record{Topic: settings.Topic, Partition: 0, Value: []byte("bounded-open-transaction-fixture")}
	produceErr := client.ProduceSync(produceCtx, record).FirstErr()
	produceCancel()
	if produceErr != nil {
		t.Fatalf("independent SETUP transaction produce was not acknowledged (class=%s)", kafkaTransactionSetupErrorClass(produceErr))
	}
	openHWM, openLSO, openCommitted := read()
	if openHWM <= preHWM || openHWM <= record.Offset || openLSO != preLSO || openHWM <= openLSO || openCommitted != preCommitted {
		t.Fatal("genuine open transaction failed to preserve independent HWM, LSO and committed-next positions")
	}
	abortCtx, abortCancel := context.WithTimeout(ctx, 8*time.Second)
	abortErr := client.EndTransaction(abortCtx, kgo.TryAbort)
	abortCancel()
	if abortErr != nil {
		t.Fatalf("independent SETUP transaction abort did not acknowledge (class=%s)", kafkaTransactionSetupErrorClass(abortErr))
	}
	begun = false
	var postHWM, postLSO, postCommitted int64
	stable := false
	for attempt := 0; attempt < 3; attempt++ {
		postHWM, postLSO, postCommitted = read()
		if postHWM == postLSO && postLSO >= openHWM {
			stable = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("genuine abort-boundary observation deadline exceeded")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !stable || postCommitted != preCommitted {
		t.Fatal("genuine abort did not close the stable boundary without changing committed-next")
	}
	// Positions include record/control offsets; no record-count-plus-one oracle.
	// This is producer transaction visibility, not consume-transform EOS proof.
	t.Logf("genuine transaction boundaries pre HWM=%d LSO=%d committed=%d; open HWM=%d LSO=%d committed=%d; aborted HWM=%d LSO=%d committed=%d; setup record offset=%d", preHWM, preLSO, preCommitted, openHWM, openLSO, openCommitted, postHWM, postLSO, postCommitted, record.Offset)
}

func TestKafkaTransactionSetupReapJoinsLateActualAcquisition(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address, err := netip.ParseAddrPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	entered, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	allow := func() { once.Do(func() { close(unblock) }) }
	defer allow()
	owner := &kafkaTransactionSetupDialer{address: address, sockets: map[*kafkaTransactionSetupConn]bool{}, connect: func(ctx context.Context, network string, address netip.AddrPort) (net.Conn, error) {
		close(entered)
		<-unblock
		dialer := net.Dialer{KeepAlive: -1}
		dialer.SetMultipathTCP(false)
		return dialer.DialTCP(ctx, network, netip.AddrPort{}, address)
	}}
	returned := make(chan error, 1)
	go func() { _, err := owner.dial(context.Background(), "tcp", address.String()); returned <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("controlled setup dial did not enter actual acquisition boundary")
	}
	joined := make(chan struct{})
	go func() { owner.reap(t); close(joined) }()
	deadline := time.Now().Add(time.Second)
	for {
		owner.mu.Lock()
		closing := owner.closing
		owner.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture reap did not fence further dial admission")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-joined:
		t.Fatal("fixture claimed cleanup before in-flight acquisition joined")
	default:
	}
	if _, err := owner.dial(context.Background(), "tcp", address.String()); !errors.Is(err, context.Canceled) {
		t.Fatal("late setup dial bypassed shutdown fence")
	}
	allow()
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, err := listener.Accept()
	if err != nil {
		t.Fatal("late actual TCP socket was not independently accepted")
	}
	defer raw.Close()
	_ = raw.SetReadDeadline(time.Now().Add(time.Second))
	var body [1]byte
	if _, err := raw.Read(body[:]); err != io.EOF {
		t.Fatal("late acquired physical setup socket did not close before reap")
	}
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("late acquisition did not reject borrowed socket")
		}
	case <-time.After(time.Second):
		t.Fatal("actual setup acquisition did not join")
	}
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("fixture reap did not finish after actual socket close")
	}
}

func TestKafkaTransactionSetupAddressRequiresExactFixtureAllocation(t *testing.T) {
	fixture := kafkaManagementBrokerFixture{Endpoint: "tls://localhost:20392", DialAddress: "127.0.0.1:20392", BrokerAddress: "127.0.0.1:20393", SetupAddress: "127.0.0.1:20394"}
	address, err := kafkaTransactionSetupAddress(fixture)
	if err != nil || address.String() != fixture.SetupAddress {
		t.Fatal("valid custom fixture base port rejected", err)
	}
	for _, test := range []struct {
		name   string
		change func(*kafkaManagementBrokerFixture)
	}{
		{"unapproved setup port", func(f *kafkaManagementBrokerFixture) { f.SetupAddress = "127.0.0.1:19394" }},
		{"unapproved broker port", func(f *kafkaManagementBrokerFixture) { f.BrokerAddress = "127.0.0.1:20395" }},
		{"different loopback IP", func(f *kafkaManagementBrokerFixture) { f.SetupAddress = "127.0.0.2:20394" }},
		{"nonloopback setup", func(f *kafkaManagementBrokerFixture) { f.SetupAddress = "192.0.2.1:20394" }},
		{"hostname setup", func(f *kafkaManagementBrokerFixture) { f.SetupAddress = "localhost:20394" }},
		{"advertised management mismatch", func(f *kafkaManagementBrokerFixture) { f.Endpoint = "tls://localhost:20390" }},
		{"unknown management hostname", func(f *kafkaManagementBrokerFixture) { f.Endpoint = "tls://discovered.invalid:20392" }},
		{"port overflow", func(f *kafkaManagementBrokerFixture) { f.Endpoint = "tls://localhost:65534" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := fixture
			test.change(&bad)
			if _, err := kafkaTransactionSetupAddress(bad); err == nil {
				t.Fatal("unapproved fixture setup route accepted")
			}
		})
	}
}

func TestKafkaTransactionSetupErrorClassDoesNotDiscloseProviderText(t *testing.T) {
	canary := "FIXTURE_SECRET_ERROR_TEXT_DO_NOT_EMIT"
	for _, test := range []struct {
		err      error
		expected string
	}{
		{nil, "none"},
		{fmt.Errorf("%s: %w", canary, &kerr.Error{Code: 15, Message: canary, Description: canary}), "kafka_code_15"},
		{fmt.Errorf("%s: %w", canary, context.DeadlineExceeded), "deadline"},
		{fmt.Errorf("%s: %w", canary, context.Canceled), "cancelled"},
		{fmt.Errorf("%s: %w", canary, kgo.ErrClientClosed), "client_closed"},
		{fmt.Errorf("%s: %w", canary, io.EOF), "connection_eof"},
		{errors.New(canary), "unclassified"},
	} {
		if actual := kafkaTransactionSetupErrorClass(test.err); actual != test.expected {
			t.Fatalf("classification %s, expected %s", actual, test.expected)
		}
	}
}
