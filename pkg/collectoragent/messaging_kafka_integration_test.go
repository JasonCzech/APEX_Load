package collectoragent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type kafkaManagementBrokerFixture struct {
	Endpoint                  string   `json:"endpoint"`
	DialAddress               string   `json:"dial_address"`
	CAFile                    string   `json:"ca_file"`
	ClusterID                 string   `json:"cluster_id"`
	Username                  string   `json:"username"`
	Password                  string   `json:"password"`
	Mechanism                 string   `json:"mechanism,omitempty"`
	Mechanisms                []string `json:"mechanisms,omitempty"`
	SetupAddress              string   `json:"setup_address,omitempty"`
	DeniedGroup               string   `json:"denied_group,omitempty"`
	Topic                     string   `json:"topic"`
	Group                     string   `json:"group"`
	ExpectedCommittedNext     string   `json:"expected_committed_next"`
	ExpectedHighWatermarkNext string   `json:"expected_high_watermark_next"`
	ExpectedLastStableNext    string   `json:"expected_last_stable_next"`
	BrokerAddress             string   `json:"broker_address"`
	ProxyCertFile             string   `json:"proxy_cert_file"`
	ProxyKeyFile              string   `json:"proxy_key_file"`
}
type kafkaManagementAuditedFrame struct {
	key, version int16
	correlation  int32
	bytes        int
}
type kafkaManagementAuditProxy struct {
	listener              net.Listener
	settings              kafkaManagementBrokerFixture
	tlsConfig             *tls.Config
	upstreamTLS           *tls.Config
	mu                    sync.Mutex
	sockets               []net.Conn
	frames                []kafkaManagementAuditedFrame
	accepts, active, peak int
	failures              []string
	wg                    sync.WaitGroup
}

func newKafkaManagementAuditProxy(t *testing.T, settings kafkaManagementBrokerFixture) *kafkaManagementAuditProxy {
	t.Helper()
	endpoint, err := url.Parse(settings.Endpoint)
	if err != nil {
		t.Fatal("fixture endpoint invalid")
	}
	address, err := netip.ParseAddrPort(settings.DialAddress)
	if err != nil || !address.Addr().IsLoopback() {
		t.Fatal("fixture proxy requires literal loopback")
	}
	broker, err := netip.ParseAddrPort(settings.BrokerAddress)
	if err != nil || !broker.Addr().IsLoopback() {
		t.Fatal("fixture broker requires literal loopback")
	}
	certificate, err := tls.LoadX509KeyPair(settings.ProxyCertFile, settings.ProxyKeyFile)
	if err != nil {
		t.Fatal("fixture proxy certificate unavailable")
	}
	roots, _, _, err := loadKafkaManagementTrust(settings.CAFile)
	if err != nil {
		t.Fatal("fixture trust unavailable")
	}
	listener, err := net.Listen("tcp", address.String())
	if err != nil {
		t.Fatal("fixture proxy could not bind")
	}
	proxy := &kafkaManagementAuditProxy{listener: listener, settings: settings, tlsConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, upstreamTLS: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: endpoint.Hostname()}}
	proxy.wg.Add(1)
	go func() {
		defer proxy.wg.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			proxy.mu.Lock()
			proxy.accepts++
			// Charge before dispatch: joined cannot miss an accepted handler
			// whose goroutine has not yet been scheduled.
			proxy.active++
			proxy.peak = max(proxy.peak, proxy.active)
			proxy.sockets = append(proxy.sockets, raw)
			proxy.mu.Unlock()
			proxy.wg.Add(1)
			go proxy.forward(raw, broker)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		proxy.mu.Lock()
		for _, raw := range proxy.sockets {
			_ = raw.Close()
		}
		proxy.mu.Unlock()
		proxy.wg.Wait()
	})
	return proxy
}
func (p *kafkaManagementAuditProxy) fail(message string) {
	p.mu.Lock()
	p.failures = append(p.failures, message)
	p.mu.Unlock()
}
func (p *kafkaManagementAuditProxy) forward(raw net.Conn, broker netip.AddrPort) {
	defer p.wg.Done()
	// The upstream Close, installed below, runs first. Discharge only after
	// both real socket closes have returned, never before raw cleanup.
	defer func() { _ = raw.Close(); p.mu.Lock(); p.active--; p.mu.Unlock() }()
	deadline := time.Now().Add(15 * time.Second)
	_ = raw.SetDeadline(deadline)
	client := tls.Server(raw, p.tlsConfig)
	if err := client.Handshake(); err != nil {
		return
	} // Invalid-client CA cases never forward credentials.
	dialer := net.Dialer{KeepAlive: -1}
	dialer.SetMultipathTCP(false)
	network := "tcp4"
	if broker.Addr().Is6() {
		network = "tcp6"
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	upstreamRaw, err := dialer.DialTCP(ctx, network, netip.AddrPort{}, broker)
	if err != nil {
		p.fail("fixture backend connection failed")
		return
	}
	p.mu.Lock()
	p.sockets = append(p.sockets, upstreamRaw)
	p.mu.Unlock()
	defer upstreamRaw.Close()
	_ = upstreamRaw.SetDeadline(deadline)
	upstream := tls.Client(upstreamRaw, p.upstreamTLS.Clone())
	if err := upstream.HandshakeContext(ctx); err != nil {
		p.fail("fixture backend TLS failed")
		return
	}
	for {
		body, err := kafkaManagementProxyReadFrame(client)
		if err != nil {
			return
		}
		if len(body) < 8 {
			clear(body)
			p.fail("fixture request header truncated")
			return
		}
		key := int16(binary.BigEndian.Uint16(body[:2]))
		version := int16(binary.BigEndian.Uint16(body[2:4]))
		correlation := int32(binary.BigEndian.Uint32(body[4:8]))
		permitted := false
		switch key {
		case 18:
			permitted = version == 0
		case 17, 36:
			permitted = version == 1
		case 3:
			permitted = version == 8 || version == 12
		case 10:
			permitted = version == 2
		case 9:
			permitted = version == 7
		case 2:
			permitted = version == 5
		}
		if !permitted {
			clear(body)
			p.fail("fixture observed nonapproved API key or version")
			return
		}
		p.mu.Lock()
		p.frames = append(p.frames, kafkaManagementAuditedFrame{key: key, version: version, correlation: correlation, bytes: len(body) + 4})
		p.mu.Unlock()
		err = kafkaManagementProxyWriteFrame(upstream, body)
		clear(body)
		if err != nil {
			p.fail("fixture backend write failed")
			return
		}
		response, err := kafkaManagementProxyReadFrame(upstream)
		if err != nil {
			p.fail("fixture backend response failed")
			return
		}
		if len(response) < 4 || int32(binary.BigEndian.Uint32(response[:4])) != correlation {
			clear(response)
			p.fail("fixture backend correlation failed")
			return
		}
		err = kafkaManagementProxyWriteFrame(client, response)
		clear(response)
		if err != nil {
			return
		}
	}
}
func kafkaManagementProxyReadFrame(reader io.Reader) ([]byte, error) {
	var length [4]byte
	if _, err := io.ReadFull(reader, length[:]); err != nil {
		return nil, err
	}
	size := int64(int32(binary.BigEndian.Uint32(length[:])))
	if size < 4 || size > kafkaManagementFrameLimit {
		return nil, errors.New("fixture frame exceeds bound")
	}
	body := make([]byte, int(size))
	if _, err := io.ReadFull(reader, body); err != nil {
		clear(body)
		return nil, err
	}
	return body, nil
}
func kafkaManagementProxyWriteFrame(writer io.Writer, body []byte) error {
	header := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	for _, part := range [][]byte{header, body} {
		for len(part) > 0 {
			n, err := writer.Write(part)
			if n < 0 || n > len(part) {
				return io.ErrShortWrite
			}
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}
func (p *kafkaManagementAuditProxy) joined(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		active := p.active
		p.mu.Unlock()
		if active == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("real broker proxy connection remained live after collection")
		}
		time.Sleep(time.Millisecond)
	}
}
func (p *kafkaManagementAuditProxy) audit(t *testing.T, poll *managementObservation, startFrames, startAccepts int) {
	t.Helper()
	p.joined(t)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.failures) != 0 {
		t.Fatal("genuine broker independent wire audit failed", p.failures)
	}
	requests, pages := poll.counts()
	if pages != 0 || requests != len(p.frames)-startFrames+2*(p.accepts-startAccepts) {
		t.Fatal("genuine broker frame and TCP/TLS operation counts do not reconcile")
	}
	if p.peak > 2 {
		t.Fatal("genuine broker live transport limit exceeded")
	}
	for _, frame := range p.frames[startFrames:] {
		t.Logf("audited read-only frame key=%d version=%d correlation=%d bytes=%d", frame.key, frame.version, frame.correlation, frame.bytes)
	}
}

// This test qualifies only the pinned genuine broker fixture actually supplied.
// Absence is an explicit skip, not evidence of hosted-provider acceptance.
func TestKafkaManagementActualBroker(t *testing.T) {
	path := os.Getenv("APEXLOAD_KAFKA_MANAGEMENT_FIXTURE")
	if path == "" {
		t.Skip("genuine Kafka fixture is not configured")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("genuine Kafka fixture settings unavailable")
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		t.Fatal("genuine Kafka fixture settings exceed bounds")
	}
	defer clear(body)
	var settings kafkaManagementBrokerFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		t.Fatal("genuine Kafka fixture settings invalid")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		t.Fatal("genuine Kafka fixture settings contain extra JSON")
	}
	for _, position := range []string{settings.ExpectedCommittedNext, settings.ExpectedHighWatermarkNext, settings.ExpectedLastStableNext} {
		if _, err := analysis.ParseManagementInteger(position); err != nil {
			t.Fatal("genuine Kafka fixture expected positions invalid")
		}
	}
	mechanisms := settings.Mechanisms
	if len(mechanisms) == 0 {
		mechanisms = []string{settings.Mechanism}
	}
	if len(mechanisms) > 3 {
		t.Fatal("fixture authentication inventory exceeds bounds")
	}
	seen := map[string]bool{}
	baselinesPassed := true
	for _, mechanism := range mechanisms {
		if seen[mechanism] || mechanism != "plain" && mechanism != "scram_sha_256" && mechanism != "scram_sha_512" {
			t.Fatal("fixture authentication inventory invalid")
		}
		seen[mechanism] = true
		passed := t.Run(mechanism, func(t *testing.T) {
			profile := settings
			profile.Mechanism = mechanism
			testKafkaManagementActualBrokerProfile(t, profile)
		})
		baselinesPassed = baselinesPassed && passed
	}
	if baselinesPassed {
		profile := settings
		profile.Mechanism = mechanisms[0]
		t.Run("open_transaction_read_boundaries", func(t *testing.T) { testKafkaManagementActualBrokerOpenTransaction(t, profile) })
	}
}
func testKafkaManagementActualBrokerProfile(t *testing.T, settings kafkaManagementBrokerFixture) {
	proxy := newKafkaManagementAuditProxy(t, settings)
	target := ManagementTarget{Scope: analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindKafka, Provider: settings.ClusterID, Isolation: "read_committed", Entities: []analysis.ManagementEntity{{Kafka: &analysis.KafkaManagementEntity{Topic: settings.Topic, Partition: 0, Group: settings.Group}}}}, Identity: analysis.ManagementIdentity{Mode: "sasl_plain", Principal: settings.Username, PolicyRevision: "fixture-policy-v1", CredentialRef: "fixture-management", CredentialVersion: "fixture-credentials-v1"}, Endpoints: []string{settings.Endpoint}, TLSCAFile: settings.CAFile, Kafka: &KafkaManagementSettings{ClusterID: settings.ClusterID, DialAddresses: map[string]string{settings.Endpoint: settings.DialAddress}}}
	if settings.Mechanism != "plain" {
		target.Identity.Mode = "sasl_scram"
	}
	agent, err := kafkaConfigTestAgent(t, &target, []string{"kafka-offsets"})
	if err != nil {
		t.Fatal("genuine Kafka local approval construction failed")
	}
	target = agent.cfg.AdapterTargets[0].Management.clone()
	if len(agent.management.readiness()) != 1 {
		t.Fatal("genuine Kafka local profile did not advertise exact readiness")
	}
	proxy.mu.Lock()
	before := proxy.accepts
	proxy.mu.Unlock()
	if before != 0 {
		t.Fatal("construction or readiness contacted the real broker")
	}
	run := func(target ManagementTarget) (*analysis.MessagingManagementSnapshot, error, *managementObservation) {
		grant := managementTestGrant("instance")
		grant.ScopeHash, _ = target.Scope.Hash()
		grant.IdentityHash, _ = target.Identity.Fingerprint()
		credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "sasl", SASL: &ManagementSASLCredentials{Mechanism: settings.Mechanism, Username: settings.Username, Password: settings.Password}}
		poll := newManagementObservation(target, grant, credentials)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
		poll.finish()
		return snapshot, err, poll
	}
	snapshot, err, poll := run(target)
	if err != nil {
		t.Fatalf("genuine Kafka observation failed with reason %s", kafkaManagementErrorReason(err))
	}
	if snapshot == nil || len(snapshot.Rows) != 1 || snapshot.Incomplete {
		t.Fatal("genuine Kafka fixture did not return complete exact positions")
	}
	offsets := snapshot.Rows[0].KafkaOffsets
	for _, pair := range []struct {
		actual   analysis.ManagementInteger
		expected string
	}{{offsets.CommittedNext, settings.ExpectedCommittedNext}, {offsets.HighWatermarkNext, settings.ExpectedHighWatermarkNext}, {offsets.LastStableNext, settings.ExpectedLastStableNext}} {
		if pair.actual.Value == nil || *pair.actual.Value != pair.expected {
			t.Fatal("genuine Kafka expected exact boundary mismatch")
		}
	}
	proxy.audit(t, poll, 0, 0)
	proxy.mu.Lock()
	frames, accepts := len(proxy.frames), proxy.accepts
	proxy.mu.Unlock()
	if settings.DeniedGroup != "" {
		deniedTarget := target.clone()
		deniedTarget.Scope.Entities[0].Kafka.Group = settings.DeniedGroup
		if err := prepareKafkaManagementTarget(&deniedTarget); err != nil {
			t.Fatal("denied-group local fixture approval failed")
		}
		denied, deniedErr, deniedPoll := run(deniedTarget)
		if deniedErr != nil || denied == nil || len(denied.Rows) != 1 || !denied.Incomplete || denied.Rows[0].KafkaOffsets.CommittedNext.Reason != "permission_denied" || denied.Rows[0].KafkaOffsets.HighWatermarkNext.Value == nil {
			t.Fatal("genuine broker permission denial erased independent boundary or manufactured commit")
		}
		proxy.audit(t, deniedPoll, frames, accepts)
		proxy.mu.Lock()
		frames, accepts = len(proxy.frames), proxy.accepts
		proxy.mu.Unlock()
	}
	wrongCluster := target.clone()
	wrongCluster.Scope.Provider = "unapproved-fixture-cluster"
	wrongCluster.Kafka.ClusterID = wrongCluster.Scope.Provider
	if err := prepareKafkaManagementTarget(&wrongCluster); err != nil {
		t.Fatal("wrong-cluster local test approval failed")
	}
	rejected, err, wrongPoll := run(wrongCluster)
	if rejected != nil || kafkaManagementErrorReason(err) != "identity_mismatch" {
		t.Fatal("genuine broker cluster pin was not enforced")
	}
	proxy.audit(t, wrongPoll, frames, accepts)
	proxy.mu.Lock()
	for _, frame := range proxy.frames[frames:] {
		if frame.key == 2 || frame.key == 9 || frame.key == 10 {
			proxy.mu.Unlock()
			t.Fatal("genuine broker data read preceded cluster proof")
		}
	}
	frames, accepts = len(proxy.frames), proxy.accepts
	proxy.mu.Unlock()
	otherCA, _ := kafkaConfigTestCA(t, 777)
	wrongTrust := target.clone()
	wrongTrust.TLSCAFile = filepath.Join(t.TempDir(), "wrong-ca.pem")
	if err := os.WriteFile(wrongTrust.TLSCAFile, otherCA, 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareKafkaManagementTarget(&wrongTrust); err != nil {
		t.Fatal(err)
	}
	rejected, err, wrongPoll = run(wrongTrust)
	if rejected != nil || err == nil {
		t.Fatal("genuine broker invalid CA accepted")
	}
	proxy.audit(t, wrongPoll, frames, accepts)
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	if len(proxy.frames) != frames {
		t.Fatal("genuine broker credentials sent after invalid TLS trust")
	}
}
