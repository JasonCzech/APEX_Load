package collectoragent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type kafkaControlledCloseConn struct {
	net.Conn
	entered      chan struct{}
	unblock      chan struct{}
	closeError   error
	acknowledged bool
	calls        atomic.Int32
}

func (c *kafkaControlledCloseConn) Close() error {
	c.calls.Add(1)
	if c.entered != nil {
		close(c.entered)
	}
	if c.unblock != nil {
		<-c.unblock
	}
	if c.Conn != nil {
		_ = c.Conn.Close()
	}
	return c.closeError
}
func (c *kafkaControlledCloseConn) physicalClosed() bool { return c.acknowledged }

func TestKafkaManagementCloseJoinsFirstOwnerAndRetainsPoll(t *testing.T) {
	poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	release, err := poll.retainResource()
	if err != nil {
		t.Fatal(err)
	}
	raw := &kafkaControlledCloseConn{entered: make(chan struct{}), unblock: make(chan struct{})}
	connection := &kafkaManagementConnection{raw: raw, release: release}
	first := make(chan struct{})
	second := make(chan struct{})
	finished := make(chan struct{})
	go func() { _ = connection.close(); close(first) }()
	<-raw.entered
	go func() { _ = connection.close(); close(second) }()
	go func() { poll.finish(); close(finished) }()
	for _, done := range []chan struct{}{first, second, finished} {
		select {
		case <-done:
			t.Fatal("blocked first Close was discharged")
		case <-time.After(30 * time.Millisecond):
		}
	}
	close(raw.unblock)
	for _, done := range []chan struct{}{first, second, finished} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("proven close did not join")
		}
	}
	if raw.calls.Load() != 1 || !connection.provenClosed() {
		t.Fatal("first Close ownership was not preserved")
	}
}
func TestKafkaManagementCloseErrorRequiresPhysicalAcknowledgement(t *testing.T) {
	for _, ack := range []bool{false, true} {
		t.Run(map[bool]string{false: "unproven", true: "acknowledged"}[ack], func(t *testing.T) {
			poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
			release, err := poll.retainResource()
			if err != nil {
				t.Fatal(err)
			}
			raw := &kafkaControlledCloseConn{closeError: errors.New("controlled close error"), acknowledged: ack}
			connection := &kafkaManagementConnection{raw: raw, release: release}
			if connection.close() == nil {
				t.Fatal("close error discarded")
			}
			done := make(chan struct{})
			go func() { poll.finish(); close(done) }()
			if ack {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("acknowledged physical close retained")
				}
			} else {
				select {
				case <-done:
					t.Fatal("unproven close released source charge")
				case <-time.After(30 * time.Millisecond):
				}
				// Test cleanup simulates independently verified process destruction.
				release()
				<-done
			}
			if connection.provenClosed() != ack {
				t.Fatal("close proof fabricated")
			}
		})
	}
}
func TestKafkaManagementCancellationJoinsBlockedPhysicalClose(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	raw := &kafkaControlledCloseConn{Conn: client, entered: make(chan struct{}), unblock: make(chan struct{})}
	connection := &kafkaManagementConnection{raw: raw}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	operationStarted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- connection.withIO(ctx, func(context.Context) error {
			close(operationStarted)
			var b [1]byte
			_, err := io.ReadFull(client, b[:])
			return err
		})
	}()
	<-operationStarted
	cancel()
	<-raw.entered
	select {
	case <-done:
		t.Fatal("callback returned before real close")
	case <-time.After(30 * time.Millisecond):
	}
	close(raw.unblock)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancel reason lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled operation/watcher did not join")
	}
	if raw.calls.Load() != 1 {
		t.Fatal("cancellation and defer closed independently")
	}
}
func TestKafkaManagementOperationPanicClosesOwnedConnection(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	raw := &kafkaControlledCloseConn{Conn: client}
	connection := &kafkaManagementConnection{raw: raw}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("test panic swallowed")
			}
		}()
		_ = connection.withIO(ctx, func(context.Context) error { panic("controlled") })
	}()
	if raw.calls.Load() != 1 || !connection.provenClosed() {
		t.Fatal("panic escaped physical cleanup")
	}
}
func TestKafkaManagementTypedDialGuardRejectsSecondAndUnapprovedAttempt(t *testing.T) {
	route := kafkaManagementRoute{network: "tcp4", address: netip.MustParseAddrPort("127.0.0.1:9093")}
	guard := kafkaManagementDialGuard{route: route}
	if guard.control(context.Background(), "tcp6", "[::1]:9093", nil) == nil {
		t.Fatal("wrong family admitted")
	}
	if guard.control(context.Background(), "tcp4", "127.0.0.2:9093", nil) == nil {
		t.Fatal("different literal admitted")
	}
	if err := guard.control(context.Background(), "tcp4", "127.0.0.1:9093", nil); err != nil {
		t.Fatal(err)
	}
	if guard.control(context.Background(), "tcp4", "127.0.0.1:9093", nil) == nil {
		t.Fatal("hidden second socket attempt admitted")
	}
}
func TestKafkaManagementMaximumActualOperationsIsTwo(t *testing.T) {
	poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	var active, peak atomic.Int32
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = poll.request(context.Background(), poll.target.Endpoints[0], false, func(context.Context) error {
				now := active.Add(1)
				for {
					previous := peak.Load()
					if now <= previous || peak.CompareAndSwap(previous, now) {
						break
					}
				}
				entered <- struct{}{}
				<-release
				active.Add(-1)
				return nil
			})
		}()
	}
	<-entered
	<-entered
	select {
	case <-entered:
		t.Fatal("third actual callback entered")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	poll.finish()
	if peak.Load() != 2 {
		t.Fatal("operation concurrency did not remain bounded")
	}
}

func TestKafkaManagementConnectionExpiryPreventsFurtherFrames(t *testing.T) {
	poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	connection := &kafkaManagementConnection{route: kafkaManagementRoute{endpoint: poll.target.Endpoints[0]}, capabilities: map[int16]kafkaAPIVersion{3: {Min: 8, Max: 12}}, authExpires: time.Now().Add(-time.Second)}
	_, err := connection.exchange(context.Background(), poll, kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: 8, Topics: []kafkaManagementTopicRequest{{Name: "orders"}}})
	poll.finish()
	if kafkaManagementErrorReason(err) != "permission_denied" || connection.correlation != 0 {
		t.Fatal("expired authentication emitted another frame")
	}
}
func TestKafkaManagementTransportRejectsUnapprovedEndpointBeforeDial(t *testing.T) {
	target := kafkaConfigTestTarget(t)
	if err := prepareKafkaManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	poll := kafkaWirePoll(target, "plain")
	var dialled atomic.Int32
	transport := newKafkaManagementTransport(poll, func(context.Context, kafkaManagementRoute) (net.Conn, error) {
		dialled.Add(1)
		return nil, errors.New("must not dial")
	}, kafkaManagementTopics(target.Scope.Entities, false))
	_, err := transport.connection(context.Background(), "tls://unapproved.example.test:9093")
	poll.finish()
	if dialled.Load() != 0 || kafkaManagementErrorReason(err) != "permission_denied" {
		t.Fatal("unapproved endpoint acquired socket")
	}
}
func TestKafkaManagementTransportClosesBeforeThirdLiveConnection(t *testing.T) {
	target := kafkaConfigTestTarget(t)
	target.Endpoints = []string{"tls://one.test:9091", "tls://two.test:9092", "tls://three.test:9093"}
	target.Kafka.DialAddresses = map[string]string{target.Endpoints[0]: "127.0.0.1:9091", target.Endpoints[1]: "127.0.0.1:9092", target.Endpoints[2]: "127.0.0.1:9093"}
	if err := prepareKafkaManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	poll := kafkaWirePoll(target, "plain")
	first := &kafkaControlledCloseConn{}
	second := &kafkaControlledCloseConn{}
	transport := newKafkaManagementTransport(poll, func(context.Context, kafkaManagementRoute) (net.Conn, error) {
		if first.calls.Load() != 1 || second.calls.Load() != 0 {
			t.Fatal("third dial started before oldest physical close")
		}
		return nil, errors.New("controlled dial rejection")
	}, kafkaManagementTopics(target.Scope.Entities, false))
	transport.connections[target.Endpoints[0]] = &kafkaManagementConnection{raw: first}
	transport.connections[target.Endpoints[1]] = &kafkaManagementConnection{raw: second}
	transport.order = append([]string(nil), target.Endpoints[:2]...)
	_, err := transport.connection(context.Background(), target.Endpoints[2])
	if err == nil {
		t.Fatal("controlled dial rejection lost")
	}
	_ = transport.close()
	poll.finish()
	if first.calls.Load() != 1 || second.calls.Load() != 1 {
		t.Fatal("transport registry cleanup not joined")
	}
}
func TestKafkaManagementReconcilesEveryEndpointClusterRoutingIdentity(t *testing.T) {
	target := managementTestTarget()
	initial := func() *kafkaManagementMetadata {
		return &kafkaManagementMetadata{ClusterID: target.Scope.Provider, Brokers: map[int32]kafkaManagementBroker{1: {ID: 1, Host: "one.test", Port: 9093}}, Topics: map[string]kafkaManagementMetadataTopic{"orders": {TopicID: "original", Partitions: map[int32]kafkaManagementMetadataPartition{0: {LeaderID: 1, LeaderEpoch: 7}}}}}
	}
	for _, test := range []struct {
		name   string
		change func(*kafkaManagementMetadata)
		reason string
	}{
		{"same identity", func(*kafkaManagementMetadata) {}, ""},
		{"conflicting node route", func(m *kafkaManagementMetadata) {
			m.Brokers[1] = kafkaManagementBroker{ID: 1, Host: "other.test", Port: 9093}
		}, "identity_mismatch"},
		{"topic recreation", func(m *kafkaManagementMetadata) {
			topic := m.Topics["orders"]
			topic.TopicID = "recreated"
			m.Topics["orders"] = topic
		}, "identity_mismatch"},
		{"leader change", func(m *kafkaManagementMetadata) {
			m.Topics["orders"].Partitions[0] = kafkaManagementMetadataPartition{LeaderID: 2, LeaderEpoch: 8}
		}, "fenced"},
	} {
		t.Run(test.name, func(t *testing.T) {
			poll := newManagementObservation(target, managementTestGrant("instance"), ManagementCredentials{})
			transport := newKafkaManagementTransport(poll, nil, nil)
			transport.metadata = initial()
			next := initial()
			test.change(next)
			if reason := kafkaManagementErrorReason(transport.acceptMetadata(next)); reason != test.reason {
				t.Fatalf("identity reconciliation %q, expected %q", reason, test.reason)
			}
			poll.finish()
		})
	}
}
