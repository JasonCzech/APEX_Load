package collectoragent

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

const kafkaManagementFrameLimit = 256 << 10

// Substitutions exist only in package-local controlled fixtures. Production
// always dials one immutable literal IP through the typed TCP entry point.
type kafkaManagementDialFunc func(context.Context, kafkaManagementRoute) (net.Conn, error)

func dialKafkaManagementTCP(ctx context.Context, route kafkaManagementRoute) (net.Conn, error) {
	guard := kafkaManagementDialGuard{route: route}
	dialer := net.Dialer{KeepAlive: -1, ControlContext: guard.control}
	dialer.SetMultipathTCP(false)
	return dialer.DialTCP(ctx, route.network, netip.AddrPort{}, route.address)
}

type kafkaManagementDialGuard struct {
	mu        sync.Mutex
	route     kafkaManagementRoute
	attempted bool
}

func (g *kafkaManagementDialGuard) control(_ context.Context, network, address string, _ syscall.RawConn) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.attempted || network != g.route.network || address != g.route.address.String() {
		return &managementObservationError{reason: "permission_denied"}
	}
	g.attempted = true
	return nil
}

// A first Close result is the sole shutdown authority. A second Close cannot
// discharge a blocked first close. Error acknowledgements require a separate
// physical-close proof in controlled transports; elapsed time is never proof.
type kafkaManagementPhysicalClose interface{ physicalClosed() bool }
type kafkaManagementConnection struct {
	route           kafkaManagementRoute
	raw             net.Conn
	tls             *tls.Conn
	release         func()
	ioMu            sync.Mutex
	closeMu         sync.Mutex
	closeStarted    bool
	closeDone       chan struct{}
	closeErr        error
	closeProven     bool
	correlation     int32
	capabilities    map[int16]kafkaAPIVersion
	metadataVersion int16
	authExpires     time.Time
}

func (c *kafkaManagementConnection) close() error {
	c.closeMu.Lock()
	if c.closeStarted {
		done := c.closeDone
		c.closeMu.Unlock()
		<-done
		c.closeMu.Lock()
		err := c.closeErr
		c.closeMu.Unlock()
		return err
	}
	c.closeStarted = true
	c.closeDone = make(chan struct{})
	done := c.closeDone
	c.closeMu.Unlock()
	err := c.raw.Close() // Deliberately avoid TLS close-notify network traffic.
	proven := err == nil
	if proof, ok := c.raw.(kafkaManagementPhysicalClose); ok {
		proven = proven || proof.physicalClosed()
	}
	c.closeMu.Lock()
	c.closeErr, c.closeProven = err, proven
	c.closeMu.Unlock()
	if proven && c.release != nil {
		c.release()
	}
	close(done)
	return err
}
func (c *kafkaManagementConnection) closed() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closeStarted
}
func (c *kafkaManagementConnection) provenClosed() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closeProven
}

// withIO joins the cancellation watcher and the real synchronous operation.
// WithoutCancel prevents TLS from creating its own unjoined close callback.
func (c *kafkaManagementConnection) withIO(ctx context.Context, call func(context.Context) error) (err error) {
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	if c.closed() {
		return &managementObservationError{reason: "cancelled"}
	}
	if err = managementContextError(ctx); err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return &managementObservationError{reason: "invalid"}
	}
	if err = c.raw.SetDeadline(deadline); err != nil {
		_ = c.close()
		return err
	}
	stopped := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			_ = c.close()
		case <-stopped:
		}
	}()
	defer func() {
		close(stopped)
		<-joined
		if recovered := recover(); recovered != nil {
			_ = c.close()
			panic(recovered)
		}
		if contextErr := managementContextError(ctx); contextErr != nil {
			err = contextErr
		}
		if err != nil {
			_ = c.close()
		}
	}()
	return call(context.WithoutCancel(ctx))
}

func (c *kafkaManagementConnection) supports(key, version int16) bool {
	cap, ok := c.capabilities[key]
	return ok && cap.Min <= version && cap.Max >= version
}
func (c *kafkaManagementConnection) exchange(ctx context.Context, poll *managementObservation, request kafkaManagementRequest) (out kafkaManagementResponse, err error) {
	if request.Operation != kafkaManagementAPIVersions && !c.supports(int16(request.Operation), request.Version) {
		return out, &managementObservationError{reason: "unsupported"}
	}
	err = poll.request(ctx, c.route.endpoint, false, func(opCtx context.Context) error {
		if !c.authExpires.IsZero() {
			if !time.Now().Before(c.authExpires) {
				return &managementObservationError{reason: "permission_denied"}
			}
			var cancel context.CancelFunc
			opCtx, cancel = context.WithDeadline(opCtx, c.authExpires)
			defer cancel()
		}
		return c.withIO(opCtx, func(context.Context) error {
			if c.correlation == math.MaxInt32 {
				return &managementObservationError{reason: "budget_exhausted"}
			}
			c.correlation++
			frame, encodeErr := encodeKafkaManagementRequest(request, c.correlation)
			if encodeErr != nil {
				return encodeErr
			}
			defer clear(frame)
			for len(frame) > 0 {
				n, writeErr := c.tls.Write(frame)
				if n < 0 || n > len(frame) {
					return io.ErrShortWrite
				}
				if writeErr != nil {
					return writeErr
				}
				if n == 0 {
					return io.ErrShortWrite
				}
				frame = frame[n:]
			}
			var size [4]byte
			if _, readErr := io.ReadFull(c.tls, size[:]); readErr != nil {
				return readErr
			}
			n := int64(int32(binary.BigEndian.Uint32(size[:])))
			if n < 4 || n > kafkaManagementFrameLimit {
				return &managementObservationError{reason: "budget_exhausted"}
			}
			body := make([]byte, int(n))
			defer clear(body)
			if _, readErr := io.ReadFull(c.tls, body); readErr != nil {
				return readErr
			}
			var decodeErr error
			out, decodeErr = decodeKafkaManagementResponse(body, request, c.correlation)
			return decodeErr
		})
	})
	return out, err
}

type kafkaManagementTransport struct {
	poll        *managementObservation
	dial        kafkaManagementDialFunc
	connections map[string]*kafkaManagementConnection
	failed      map[string]error
	order       []string
	topics      []kafkaManagementTopicRequest
	metadata    *kafkaManagementMetadata
	metadataAt  time.Time
}

func newKafkaManagementTransport(poll *managementObservation, dial kafkaManagementDialFunc, topics []kafkaManagementTopicRequest) *kafkaManagementTransport {
	if dial == nil {
		dial = dialKafkaManagementTCP
	}
	return &kafkaManagementTransport{poll: poll, dial: dial, connections: map[string]*kafkaManagementConnection{}, failed: map[string]error{}, topics: topics}
}
func (t *kafkaManagementTransport) close() error {
	var failures []error
	for _, endpoint := range t.order {
		if connection := t.connections[endpoint]; connection != nil {
			failures = append(failures, connection.close())
		}
	}
	return errors.Join(failures...)
}
func (t *kafkaManagementTransport) connection(ctx context.Context, endpoint string) (result *kafkaManagementConnection, err error) {
	if failed := t.failed[endpoint]; failed != nil {
		return nil, failed
	}
	if current := t.connections[endpoint]; current != nil {
		if current.closed() {
			t.failed[endpoint] = &managementObservationError{reason: "fenced"}
			return nil, t.failed[endpoint]
		}
		return current, nil
	}
	defer func() {
		if err != nil {
			t.failed[endpoint] = err
		}
	}()
	route, err := kafkaRouteForEndpoint(&t.poll.target, endpoint)
	if err != nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	if len(t.connections) >= 2 {
		oldest := t.order[0]
		current := t.connections[oldest]
		closeErr := current.close()
		if !current.provenClosed() {
			return nil, &managementObservationError{reason: "invalid"}
		}
		delete(t.connections, oldest)
		t.order = t.order[1:]
		if closeErr != nil {
			return nil, closeErr
		}
	}
	var connection *kafkaManagementConnection
	err = t.poll.request(ctx, endpoint, false, func(opCtx context.Context) error {
		// Retain ownership before acquiring a socket, so finish racing a dial
		// cannot make an unproved close escape its already admitted callback.
		release, retainErr := t.poll.retainResource()
		if retainErr != nil {
			return retainErr
		}
		raw, dialErr := t.dial(opCtx, route)
		if raw == nil {
			release()
		}
		if raw != nil {
			connection = &kafkaManagementConnection{route: route, raw: raw, release: release}
			// Registration happens before cancellation/error can return the callback.
			t.connections[endpoint] = connection
			t.order = append(t.order, endpoint)
		}
		if dialErr != nil {
			return dialErr
		}
		if raw == nil {
			return &managementObservationError{reason: "invalid"}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	connection.tls = tls.Client(connection.raw, &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: route.serverName,
		RootCAs:    t.poll.target.kafkaApproval.roots.Clone(),
	})
	err = t.poll.request(ctx, endpoint, false, func(opCtx context.Context) error {
		return connection.withIO(opCtx, func(detached context.Context) error { return connection.tls.HandshakeContext(detached) })
	})
	if err != nil {
		return nil, err
	}
	response, err := connection.exchange(ctx, t.poll, kafkaManagementRequest{Operation: kafkaManagementAPIVersions, Version: 0})
	if err != nil {
		return nil, err
	}
	if response.ErrorCode != 0 {
		return nil, kafkaManagementProviderError(response.ErrorCode)
	}
	connection.capabilities = response.APIVersions
	if err = authenticateKafkaManagement(ctx, t.poll, connection); err != nil {
		return nil, err
	}
	switch {
	case connection.supports(int16(kafkaManagementMetadataOperation), 12):
		connection.metadataVersion = 12
	case connection.supports(int16(kafkaManagementMetadataOperation), 8):
		connection.metadataVersion = 8
	default:
		return nil, &managementObservationError{reason: "unsupported"}
	}
	// Every authenticated endpoint independently proves the pinned cluster before
	// any coordinator or offset request; routing returned by a seed is not trust.
	response, err = connection.exchange(ctx, t.poll, kafkaManagementRequest{Operation: kafkaManagementMetadataOperation, Version: connection.metadataVersion, Topics: t.topics})
	if err != nil {
		return nil, err
	}
	if response.Metadata == nil || response.Metadata.ClusterID != t.poll.target.Kafka.ClusterID {
		return nil, &managementObservationError{reason: "identity_mismatch"}
	}
	if err = t.acceptMetadata(response.Metadata); err != nil {
		return nil, err
	}
	if t.metadata == nil {
		t.metadata, t.metadataAt = response.Metadata, time.Now()
	}
	return connection, nil
}
func (t *kafkaManagementTransport) acceptMetadata(next *kafkaManagementMetadata) error {
	if t.metadata == nil {
		return nil
	}
	for id, broker := range next.Brokers {
		if initial, exists := t.metadata.Brokers[id]; exists && (initial.Host != broker.Host || initial.Port != broker.Port) {
			return &managementObservationError{reason: "identity_mismatch"}
		}
	}
	for _, entity := range t.poll.target.Scope.Entities {
		initialTopic, haveInitial := t.metadata.Topics[entity.Kafka.Topic]
		nextTopic, haveNext := next.Topics[entity.Kafka.Topic]
		if !haveInitial || !haveNext || initialTopic.ErrorCode != 0 || nextTopic.ErrorCode != 0 {
			continue
		}
		if initialTopic.TopicID != "" && nextTopic.TopicID != "" && initialTopic.TopicID != nextTopic.TopicID {
			return &managementObservationError{reason: "identity_mismatch"}
		}
		initial, haveInitial := initialTopic.Partitions[entity.Kafka.Partition]
		later, haveLater := nextTopic.Partitions[entity.Kafka.Partition]
		if haveInitial && haveLater && initial.ErrorCode == 0 && later.ErrorCode == 0 && (initial.LeaderID != later.LeaderID || initial.LeaderEpoch != later.LeaderEpoch) {
			return &managementObservationError{reason: "fenced"}
		}
	}
	return nil
}
