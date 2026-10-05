package collectoragent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/twmb/franz-go/pkg/kmsg"
	"golang.org/x/crypto/pbkdf2"
)

type kafkaWireFixtureOptions struct {
	finalBoundaryDelay  time.Duration
	authLifetimeMS      int64
	authDelay           time.Duration
	cluster             string
	metadataVersion     int16
	mechanism           string
	committed, hwm, lso int64
	commitError         int16
	omitCommitted       bool
	coordinatorHost     string
	wrongCertificate    bool
	corrupt             func(int16, []byte) []byte
	holdKey             int16
}
type kafkaWireFixture struct {
	finalBoundaryStarted, finalBoundaryCompleted time.Time
	options                                      kafkaWireFixtureOptions
	listener                                     net.Listener
	caPath, host                                 string
	port                                         int32
	mu                                           sync.Mutex
	sockets                                      []net.Conn
	keys                                         []int16
	errors                                       []string
	wg                                           sync.WaitGroup
	accepts                                      atomic.Int32
	active                                       atomic.Int32
	peak                                         atomic.Int32
	held                                         chan struct{}
}

func newKafkaWireFixture(t *testing.T, options kafkaWireFixtureOptions) *kafkaWireFixture {
	t.Helper()
	if options.cluster == "" {
		options.cluster = "approved-cluster"
	}
	if options.metadataVersion == 0 {
		options.metadataVersion = 12
	}
	if options.mechanism == "" {
		options.mechanism = "plain"
	}
	rootPub, rootKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "controlled management root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, rootPub, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dnsName := "broker.example.test"
	if options.wrongCertificate {
		dnsName = "unapproved.example.test"
	}
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: dnsName}, DNSNames: []string{dnsName}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, pub, rootKey)
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
	fixture := &kafkaWireFixture{options: options, listener: listener, caPath: caPath, host: "broker.example.test", port: int32(listener.Addr().(*net.TCPAddr).Port), held: make(chan struct{}, 1)}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: key}}}
	fixture.wg.Add(1)
	go func() {
		defer fixture.wg.Done()
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			fixture.accepts.Add(1)
			fixture.mu.Lock()
			fixture.sockets = append(fixture.sockets, raw)
			fixture.mu.Unlock()
			fixture.wg.Add(1)
			go fixture.serve(tls.Server(raw, tlsConfig))
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		fixture.mu.Lock()
		for _, raw := range fixture.sockets {
			_ = raw.Close()
		}
		fixture.mu.Unlock()
		fixture.wg.Wait()
	})
	return fixture
}
func (f *kafkaWireFixture) fail(message string) {
	f.mu.Lock()
	f.errors = append(f.errors, message)
	f.mu.Unlock()
}
func (f *kafkaWireFixture) serve(connection *tls.Conn) {
	defer f.wg.Done()
	defer connection.NetConn().Close()
	current := f.active.Add(1)
	for {
		before := f.peak.Load()
		if current <= before || f.peak.CompareAndSwap(before, current) {
			break
		}
	}
	defer f.active.Add(-1)
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	if err := connection.Handshake(); err != nil {
		return
	}
	if connection.ConnectionState().ServerName != f.host {
		f.fail("TLS SNI did not use approved hostname")
		return
	}
	var clientFirst, serverFirst, expectedClientProof, serverProof []byte
	authStep := 0
	for {
		var length [4]byte
		if _, err := io.ReadFull(connection, length[:]); err != nil {
			return
		}
		n := int(int32(binary.BigEndian.Uint32(length[:])))
		if n < 10 || n > kafkaManagementFrameLimit {
			f.fail("request length outside bound")
			return
		}
		frame := make([]byte, n)
		if _, err := io.ReadFull(connection, frame); err != nil {
			return
		}
		key := int16(binary.BigEndian.Uint16(frame[:2]))
		version := int16(binary.BigEndian.Uint16(frame[2:4]))
		correlation := int32(binary.BigEndian.Uint32(frame[4:8]))
		clientLen := int(int16(binary.BigEndian.Uint16(frame[8:10])))
		if clientLen < 0 || 10+clientLen > len(frame) {
			f.fail("invalid client identifier")
			return
		}
		bodyAt := 10 + clientLen
		if version == 12 && key == 3 || version == 7 && key == 9 {
			if bodyAt >= len(frame) || frame[bodyAt] != 0 {
				f.fail("unexpected request header tags")
				return
			}
			bodyAt++
		}
		request := kmsg.RequestForKey(key)
		if request == nil {
			f.fail("unknown request key")
			return
		}
		request.SetVersion(version)
		if err := request.ReadFrom(frame[bodyAt:]); err != nil {
			f.fail("invalid request shape")
			return
		}
		f.mu.Lock()
		f.keys = append(f.keys, key)
		f.mu.Unlock()
		if key == f.options.holdKey {
			select {
			case f.held <- struct{}{}:
			default:
			}
			var b [1]byte
			_, _ = connection.Read(b[:])
			return
		}
		var response kmsg.Response
		switch request := request.(type) {
		case *kmsg.ApiVersionsRequest:
			if version != 0 {
				f.fail("ApiVersions version changed")
				return
			}
			response = &kmsg.ApiVersionsResponse{Version: 0, ApiKeys: []kmsg.ApiVersionsResponseApiKey{{ApiKey: 18, MinVersion: 0, MaxVersion: 0}, {ApiKey: 17, MinVersion: 1, MaxVersion: 1}, {ApiKey: 36, MinVersion: 1, MaxVersion: 1}, {ApiKey: 3, MinVersion: 8, MaxVersion: f.options.metadataVersion}, {ApiKey: 10, MinVersion: 2, MaxVersion: 2}, {ApiKey: 9, MinVersion: 7, MaxVersion: 7}, {ApiKey: 2, MinVersion: 5, MaxVersion: 5}}}
		case *kmsg.SASLHandshakeRequest:
			selected := "PLAIN"
			if f.options.mechanism == "scram_sha_256" {
				selected = "SCRAM-SHA-256"
			}
			if f.options.mechanism == "scram_sha_512" {
				selected = "SCRAM-SHA-512"
			}
			if version != 1 || request.Mechanism != selected {
				f.fail("unapproved authentication mechanism")
				return
			}
			response = &kmsg.SASLHandshakeResponse{Version: 1, SupportedMechanisms: []string{selected}}
		case *kmsg.SASLAuthenticateRequest:
			if version != 1 {
				f.fail("SASLAuthenticate version changed")
				return
			}
			authResponse := &kmsg.SASLAuthenticateResponse{Version: 1, SessionLifetimeMillis: f.options.authLifetimeMS}
			if f.options.mechanism == "plain" {
				if !bytes.Equal(request.SASLAuthBytes, []byte("\x00reader\x00controlled-password")) {
					f.fail("unapproved principal or password")
					return
				}
			} else if authStep == 0 {
				clientFirst = append([]byte(nil), request.SASLAuthBytes...)
				nonce := kafkaManagementClientNonce(clientFirst)
				serverFirst = append([]byte("r="), nonce...)
				serverFirst = append(serverFirst, []byte("-server,s=c2FsdA==,i=4096")...)
				authResponse.SASLAuthBytes = serverFirst
				expectedClientProof, serverProof = kafkaFixtureSCRAMProof(clientFirst, serverFirst, f.options.mechanism)
			} else {
				if !bytes.Equal(request.SASLAuthBytes, expectedClientProof) {
					f.fail("SCRAM client proof mismatch")
					return
				}
				authResponse.SASLAuthBytes = serverProof
			}
			authStep++
			response = authResponse
		case *kmsg.MetadataRequest:
			if version != f.options.metadataVersion || request.AllowAutoTopicCreation || len(request.Topics) != 1 || request.Topics[0].Topic == nil || *request.Topics[0].Topic != "orders" {
				f.fail("metadata expanded named inventory or enabled creation")
				return
			}
			topicName := "orders"
			cluster := f.options.cluster
			response = &kmsg.MetadataResponse{Version: version, ClusterID: &cluster, ControllerID: 1, Brokers: []kmsg.MetadataResponseBroker{{NodeID: 1, Host: f.host, Port: f.port}}, Topics: []kmsg.MetadataResponseTopic{{Topic: &topicName, TopicID: [16]byte{1}, Partitions: []kmsg.MetadataResponseTopicPartition{{Partition: 0, Leader: 1, LeaderEpoch: 7, Replicas: []int32{1}, ISR: []int32{1}}}}}}
		case *kmsg.FindCoordinatorRequest:
			if version != 2 || request.CoordinatorKey != "workers" || request.CoordinatorType != 0 {
				f.fail("coordinator expanded group scope")
				return
			}
			host := f.host
			if f.options.coordinatorHost != "" {
				host = f.options.coordinatorHost
			}
			response = &kmsg.FindCoordinatorResponse{Version: 2, NodeID: 1, Host: host, Port: f.port}
		case *kmsg.OffsetFetchRequest:
			if version != 7 || request.Group != "workers" || !request.RequireStable || len(request.Topics) != 1 || request.Topics[0].Topic != "orders" || len(request.Topics[0].Partitions) != 1 || request.Topics[0].Partitions[0] != 0 {
				f.fail("OffsetFetch expanded inventory or stability weakened")
				return
			}
			offsets := []kmsg.OffsetFetchResponseTopic{}
			if !f.options.omitCommitted {
				offsets = []kmsg.OffsetFetchResponseTopic{{Topic: "orders", Partitions: []kmsg.OffsetFetchResponseTopicPartition{{Partition: 0, Offset: f.options.committed, LeaderEpoch: 3, ErrorCode: f.options.commitError}}}}
			}
			response = &kmsg.OffsetFetchResponse{Version: 7, Topics: offsets}
		case *kmsg.ListOffsetsRequest:
			if version != 5 || request.ReplicaID != -1 || len(request.Topics) != 1 || request.Topics[0].Topic != "orders" || len(request.Topics[0].Partitions) != 1 || request.Topics[0].Partitions[0].Partition != 0 || request.Topics[0].Partitions[0].Timestamp != -1 || request.Topics[0].Partitions[0].CurrentLeaderEpoch != 7 {
				f.fail("ListOffsets expanded inventory or boundary semantics changed")
				return
			}
			position := f.options.hwm
			if request.IsolationLevel == 1 {
				if f.options.finalBoundaryDelay > 0 {
					f.mu.Lock()
					f.finalBoundaryStarted = time.Now()
					f.mu.Unlock()
					time.Sleep(f.options.finalBoundaryDelay)
					f.mu.Lock()
					f.finalBoundaryCompleted = time.Now()
					f.mu.Unlock()
				}
				position = f.options.lso
			} else if request.IsolationLevel != 0 {
				f.fail("unknown isolation")
				return
			}
			response = &kmsg.ListOffsetsResponse{Version: 5, Topics: []kmsg.ListOffsetsResponseTopic{{Topic: "orders", Partitions: []kmsg.ListOffsetsResponseTopicPartition{{Partition: 0, Offset: position, LeaderEpoch: 2, Timestamp: -1}}}}}
		default:
			f.fail("mutating, membership, record or telemetry request observed")
			return
		}
		body := binary.BigEndian.AppendUint32(nil, uint32(correlation))
		if response.IsFlexible() {
			body = append(body, 0)
		}
		body = response.AppendTo(body)
		if f.options.corrupt != nil {
			body = f.options.corrupt(key, body)
		}
		encoded := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
		encoded = append(encoded, body...)
		if key == 36 && f.options.authDelay > 0 {
			time.Sleep(f.options.authDelay)
		}
		if _, err := connection.Write(encoded); err != nil {
			return
		}
	}
}
func kafkaFixtureSCRAMProof(clientFirst, serverFirst []byte, mechanism string) ([]byte, []byte) {
	newHash := sha256.New
	if mechanism == "scram_sha_512" {
		newHash = sha512.New
	}
	nonce := bytes.Split(serverFirst, []byte(","))[0][2:]
	finalWithoutProof := append([]byte("c=biws,r="), nonce...)
	message := append([]byte(nil), clientFirst[3:]...)
	message = append(message, ',')
	message = append(message, serverFirst...)
	message = append(message, ',')
	message = append(message, finalWithoutProof...)
	salted := pbkdf2.Key([]byte("controlled-password"), []byte("salt"), 4096, newHash().Size(), newHash)
	mac := func(key, value []byte) []byte { h := hmac.New(newHash, key); _, _ = h.Write(value); return h.Sum(nil) }
	clientKey := mac(salted, []byte("Client Key"))
	hashed := newHash()
	_, _ = hashed.Write(clientKey)
	signature := mac(hashed.Sum(nil), message)
	for i := range signature {
		signature[i] ^= clientKey[i]
	}
	clientProof := append(finalWithoutProof, []byte(",p="+base64.StdEncoding.EncodeToString(signature))...)
	serverProof := []byte("v=" + base64.StdEncoding.EncodeToString(mac(mac(salted, []byte("Server Key")), message)))
	return clientProof, serverProof
}
func (f *kafkaWireFixture) target(t *testing.T) ManagementTarget {
	t.Helper()
	target := kafkaConfigTestTarget(t)
	target.TLSCAFile = f.caPath
	endpoint := "tls://" + net.JoinHostPort(f.host, strconv.Itoa(int(f.port)))
	target.Endpoints = []string{endpoint}
	target.Kafka.DialAddresses = map[string]string{endpoint: f.listener.Addr().String()}
	if f.options.mechanism != "plain" {
		target.Identity.Mode = "sasl_scram"
	}
	if err := prepareKafkaManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	return target
}
func kafkaWirePoll(target ManagementTarget, mechanism string) *managementObservation {
	grant := managementTestGrant("instance")
	grant.ScopeHash, _ = target.Scope.Hash()
	grant.IdentityHash, _ = target.Identity.Fingerprint()
	credentials := ManagementCredentials{Version: 2, Purpose: ManagementCredentialPurpose, Mode: "sasl", SASL: &ManagementSASLCredentials{Mechanism: mechanism, Username: "reader", Password: "controlled-password"}}
	return newManagementObservation(target, grant, credentials)
}
func (f *kafkaWireFixture) assertAudit(t *testing.T, poll *managementObservation) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errors) != 0 {
		t.Fatal("independent wire audit failed", f.errors)
	}
	requests, pages := poll.counts()
	if pages != 0 || requests != len(f.keys)+2*int(f.accepts.Load()) {
		t.Fatalf("admission count %d does not reconcile with %d frames and %d TCP/TLS setups", requests, len(f.keys), f.accepts.Load())
	}
	if f.peak.Load() > 2 {
		t.Fatal("more than two live transports")
	}
}
func TestKafkaManagementControlledTLSOffsetsAndFrameAudit(t *testing.T) {
	for _, version := range []int16{8, 12} {
		for _, mechanism := range []string{"plain", "scram_sha_256", "scram_sha_512"} {
			t.Run(fmt.Sprintf("metadata%d/%s", version, mechanism), func(t *testing.T) {
				fixture := newKafkaWireFixture(t, kafkaWireFixtureOptions{metadataVersion: version, mechanism: mechanism, committed: 9007199254740993, hwm: 9007199254741093, lso: 9007199254741043})
				target := fixture.target(t)
				poll := kafkaWirePoll(target, mechanism)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
				poll.finish()
				if err != nil {
					t.Fatal(err)
				}
				if err := snapshot.ValidateFor(target.Scope, target.Identity); err != nil {
					t.Fatal(err)
				}
				if len(snapshot.Rows) != 1 || !snapshot.InventoryComplete || snapshot.Incomplete {
					t.Fatal("complete exact inventory lost")
				}
				row := snapshot.Rows[0]
				offsets := row.KafkaOffsets
				if *offsets.CommittedNext.Value != "9007199254740993" || *offsets.HighWatermarkNext.Value != "9007199254741093" || *offsets.LastStableNext.Value != "9007199254741043" || *offsets.LeaderEpoch.Value != "7" || *offsets.CommittedLeaderEpoch.Value != "3" {
					t.Fatal("position or independent epoch semantics changed")
				}
				if offsets.LogEndNext.Value != nil || offsets.LogEndNext.State != analysis.ManagementStateUnsupported {
					t.Fatal("physical log end invented")
				}
				lag, err := offsets.Lag("read_committed")
				if err != nil || lag.Value == nil || *lag.Value != "50" {
					t.Fatal("next-offset lag has extra increment or wrong isolation")
				}
				if row.ObservationAgeMS == nil || row.ObservedAt == nil || row.ObservedAt.After(snapshot.CompletedAt) {
					t.Fatal("source capture age missing")
				}
				if version == 8 && offsets.TopicID != "" || version == 12 && offsets.TopicID == "" {
					t.Fatal("topic identity availability invented or lost")
				}
				fixture.assertAudit(t, poll)
			})
		}
	}
}
func TestKafkaManagementPartialCoverageKeepsIndependentBoundaries(t *testing.T) {
	for _, test := range []struct {
		name    string
		options kafkaWireFixtureOptions
		reason  string
	}{{"denied", kafkaWireFixtureOptions{commitError: 30, hwm: 10, lso: 7}, "permission_denied"}, {"missing", kafkaWireFixtureOptions{omitCommitted: true, hwm: 10, lso: 7}, "not_observed"}, {"unstable", kafkaWireFixtureOptions{commitError: 88, hwm: 10, lso: 7}, "unqualified"}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKafkaWireFixture(t, test.options)
			poll := kafkaWirePoll(fixture.target(t), "plain")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
			poll.finish()
			if err != nil {
				t.Fatal(err)
			}
			row := snapshot.Rows[0]
			if !snapshot.Incomplete || row.State != analysis.ManagementStateObserved || row.KafkaOffsets.CommittedNext.Value != nil || row.KafkaOffsets.CommittedNext.Reason != test.reason || *row.KafkaOffsets.HighWatermarkNext.Value != "10" || *row.KafkaOffsets.LastStableNext.Value != "7" {
				t.Fatal("partial coverage filled absent commit or erased independent boundaries")
			}
			fixture.assertAudit(t, poll)
		})
	}
}
func TestKafkaManagementWrongClusterAndCertificateNeverReadData(t *testing.T) {
	for _, test := range []struct {
		name    string
		options kafkaWireFixtureOptions
		reason  string
	}{{"cluster", kafkaWireFixtureOptions{cluster: "unapproved-cluster"}, "identity_mismatch"}, {"certificate", kafkaWireFixtureOptions{wrongCertificate: true}, "invalid"}} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKafkaWireFixture(t, test.options)
			poll := kafkaWirePoll(fixture.target(t), "plain")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
			poll.finish()
			if snapshot != nil || err == nil || kafkaManagementErrorReason(err) != test.reason {
				t.Fatal("wrong remote identity produced evidence", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			for _, key := range fixture.keys {
				if key == 10 || key == 9 || key == 2 {
					t.Fatal("data route used before cluster identity proof")
				}
				if test.options.wrongCertificate && (key == 17 || key == 36) {
					t.Fatal("credentials sent before TLS identity proof")
				}
			}
		})
	}
}
func TestKafkaManagementUnapprovedCoordinatorNeverDialled(t *testing.T) {
	fixture := newKafkaWireFixture(t, kafkaWireFixtureOptions{coordinatorHost: "unapproved-alias.test", hwm: 10, lso: 7})
	poll := kafkaWirePoll(fixture.target(t), "plain")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
	poll.finish()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Rows[0].KafkaOffsets.CommittedNext.Value != nil || fixture.accepts.Load() != 1 {
		t.Fatal("unapproved coordinator expanded network authority")
	}
	fixture.mu.Lock()
	for _, key := range fixture.keys {
		if key == 9 {
			t.Fatal("offset request followed unapproved coordinator")
		}
	}
	fixture.mu.Unlock()
}
func TestKafkaManagementCorruptFrameAndCancellationJoin(t *testing.T) {
	t.Run("correlation", func(t *testing.T) {
		fixture := newKafkaWireFixture(t, kafkaWireFixtureOptions{corrupt: func(key int16, body []byte) []byte {
			if key == 18 {
				binary.BigEndian.PutUint32(body[:4], 999)
			}
			return body
		}})
		poll := kafkaWirePoll(fixture.target(t), "plain")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
		poll.finish()
		if snapshot != nil || err == nil {
			t.Fatal("corrupt correlation accepted")
		}
	})
	t.Run("cancelled read", func(t *testing.T) {
		fixture := newKafkaWireFixture(t, kafkaWireFixtureOptions{holdKey: 18})
		poll := kafkaWirePoll(fixture.target(t), "plain")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := (kafkaManagementObserver{}).observe(ctx, poll); poll.finish(); done <- err }()
		select {
		case <-fixture.held:
		case <-time.After(time.Second):
			t.Fatal("fixture did not enter controlled read")
		}
		cancel()
		select {
		case err := <-done:
			if kafkaManagementErrorReason(err) != "cancelled" {
				t.Fatal("cancellation reason lost", err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled operation did not physically join")
		}
	})
}

func TestKafkaManagementAuthenticationLifetimeDoesNotIncludeResponseLatency(t *testing.T) {
	for _, test := range []struct {
		name     string
		lifetime int64
		delay    time.Duration
		expired  bool
	}{
		{"already expired response", 1, 20 * time.Millisecond, true},
		{"bounded lifetime", 1000, 0, false},
		{"maximum wire lifetime", 9223372036854775807, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKafkaWireFixture(t, kafkaWireFixtureOptions{authLifetimeMS: test.lifetime, authDelay: test.delay, hwm: 10, lso: 10})
			poll := kafkaWirePoll(fixture.target(t), "plain")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
			poll.finish()
			if test.expired {
				if snapshot != nil || kafkaManagementErrorReason(err) != "permission_denied" {
					t.Fatal("delayed response extended broker authentication lifetime")
				}
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				for _, key := range fixture.keys {
					if key == 3 || key == 10 || key == 9 || key == 2 {
						t.Fatal("data frame sent after remote authentication had expired")
					}
				}
			} else {
				if err != nil || snapshot == nil {
					t.Fatal("valid authentication lifetime rejected", err)
				}
				fixture.assertAudit(t, poll)
			}
		})
	}
}

func TestKafkaManagementOldestCaptureAgeIncludesLaterBoundaryWait(t *testing.T) {
	fixture := newKafkaWireFixture(t, kafkaWireFixtureOptions{committed: 1, hwm: 10, lso: 7, finalBoundaryDelay: 60 * time.Millisecond})
	poll := kafkaWirePoll(fixture.target(t), "plain")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot, err := (kafkaManagementObserver{}).observe(ctx, poll)
	poll.finish()
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	lateStarted, lateCompleted := fixture.finalBoundaryStarted, fixture.finalBoundaryCompleted
	fixture.mu.Unlock()
	if lateStarted.IsZero() || lateCompleted.IsZero() || lateCompleted.Before(lateStarted) {
		t.Fatal("independent late-boundary fixture markers missing")
	}
	row := snapshot.Rows[0]
	if row.ObservedAt == nil || !row.ObservedAt.Before(lateStarted.UTC()) {
		t.Fatal("later successful boundary refreshed earlier captured evidence")
	}
	elapsed := lateCompleted.Sub(lateStarted).Milliseconds()
	if row.ObservationAgeMS == nil || *row.ObservationAgeMS < elapsed {
		t.Fatalf("observation age did not include actual later boundary wait: age %v, independent wait %d ms", row.ObservationAgeMS, elapsed)
	}
	fixture.assertAudit(t, poll)
}
