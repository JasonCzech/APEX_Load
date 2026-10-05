package collectoragent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func kafkaConfigTestCA(t *testing.T, serial int64) ([]byte, *x509.Certificate) {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "controlled CA " + big.NewInt(serial).String()}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), cert
}

func kafkaConfigTestTarget(t *testing.T) ManagementTarget {
	t.Helper()
	body, _ := kafkaConfigTestCA(t, 1)
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return ManagementTarget{
		Scope:     analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindKafka, Provider: "approved-cluster", Isolation: "read_committed", Entities: []analysis.ManagementEntity{{Kafka: &analysis.KafkaManagementEntity{Topic: "orders", Partition: 0, Group: "workers"}}}},
		Identity:  analysis.ManagementIdentity{Mode: "sasl_plain", Principal: "reader", PolicyRevision: "policy-1", CredentialRef: "management-reader", CredentialVersion: "approved-version"},
		Endpoints: []string{"tls://broker.example.test:9093"}, TLSCAFile: path,
		Kafka: &KafkaManagementSettings{ClusterID: "approved-cluster", DialAddresses: map[string]string{"tls://broker.example.test:9093": "127.0.0.1:9093"}},
	}
}

func kafkaConfigTestAgent(t *testing.T, target *ManagementTarget, packs []string) (*Agent, error) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private := make([]byte, 32)
	if _, err := rand.Read(private); err != nil {
		t.Fatal(err)
	}
	return New(Config{ControllerURL: "http://localhost:8080", CollectorID: "collector", Token: "token", ControllerPublicKey: pub, CredentialPrivateKey: private, SpoolDirectory: t.TempDir(), Packs: packs, AdapterTargets: []AdapterTarget{{Pack: "kafka-offsets", Destination: "management://orders", Management: target}}})
}

func TestKafkaManagementTargetRejectsInvalidExecutableProfiles(t *testing.T) {
	changes := map[string]func(*ManagementTarget){
		"missing Kafka":              func(t *ManagementTarget) { t.Kafka = nil },
		"wrong cluster":              func(t *ManagementTarget) { t.Kafka.ClusterID = "another-cluster" },
		"anonymous":                  func(t *ManagementTarget) { t.Identity.Mode = "anonymous" },
		"workload identity":          func(t *ManagementTarget) { t.Identity.Mode = "workload_identity" },
		"missing credential ref":     func(t *ManagementTarget) { t.Identity.CredentialRef = ""; t.Identity.CredentialVersion = "" },
		"missing credential version": func(t *ManagementTarget) { t.Identity.CredentialVersion = "" },
		"missing trust":              func(t *ManagementTarget) { t.TLSCAFile = "" },
		"relative trust":             func(t *ManagementTarget) { t.TLSCAFile = "ca.pem" },
		"missing file":               func(t *ManagementTarget) { t.TLSCAFile = filepath.Join(filepath.Dir(t.TLSCAFile), "absent.pem") },
		"HTTP endpoint":              func(t *ManagementTarget) { t.Endpoints[0] = "https://broker.example.test:9093" },
		"missing map":                func(t *ManagementTarget) { t.Kafka.DialAddresses = nil },
		"extra map":                  func(t *ManagementTarget) { t.Kafka.DialAddresses["tls://other.test:9093"] = "127.0.0.1:9093" },
		"unapproved map": func(t *ManagementTarget) {
			t.Kafka.DialAddresses = map[string]string{"tls://other.test:9093": "127.0.0.1:9093"}
		},
		"DNS dial":       func(t *ManagementTarget) { t.Kafka.DialAddresses[t.Endpoints[0]] = "broker.example.test:9093" },
		"different port": func(t *ManagementTarget) { t.Kafka.DialAddresses[t.Endpoints[0]] = "127.0.0.1:9094" },
		"zero port":      func(t *ManagementTarget) { t.Kafka.DialAddresses[t.Endpoints[0]] = "127.0.0.1:0" },
		"unspecified":    func(t *ManagementTarget) { t.Kafka.DialAddresses[t.Endpoints[0]] = "0.0.0.0:9093" },
		"multicast":      func(t *ManagementTarget) { t.Kafka.DialAddresses[t.Endpoints[0]] = "224.0.0.1:9093" },
		"broadcast":      func(t *ManagementTarget) { t.Kafka.DialAddresses[t.Endpoints[0]] = "255.255.255.255:9093" },
		"IPv6 zone":      func(t *ManagementTarget) { t.Kafka.DialAddresses[t.Endpoints[0]] = "[fe80::1%en0]:9093" },
		"short cadence":  func(t *ManagementTarget) { t.MinIntervalSeconds = 9 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			target := kafkaConfigTestTarget(t)
			change(&target)
			if agent, err := kafkaConfigTestAgent(t, &target, []string{"kafka-offsets"}); err == nil || agent != nil {
				t.Fatal("invalid genuine Kafka profile constructed", name)
			}
		})
	}
}

func TestKafkaManagementCanonicalStaticRoutesDoNotExpandApproval(t *testing.T) {
	target := kafkaConfigTestTarget(t)
	target.Endpoints[0] = "tls://BROKER.Example.Test:9093"
	target.Kafka.DialAddresses = map[string]string{target.Endpoints[0]: "[::ffff:127.0.0.1]:9093"}
	if err := prepareKafkaManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	route, err := kafkaRouteForBroker(&target, "broker.example.test", 9093)
	if err != nil || route.endpoint != "tls://broker.example.test:9093" || route.serverName != "broker.example.test" || route.address.String() != "127.0.0.1:9093" || route.network != "tcp4" {
		t.Fatal("static route normalization failed", route, err)
	}
	for _, host := range []string{"same-ip-unapproved.test", "broker.example.test.", "broker.example.test:9093", "user@broker.example.test", "broker.example.test/path", "broker.example.test?token=hidden"} {
		if _, err := kafkaRouteForBroker(&target, host, 9093); err == nil {
			t.Fatal("unapproved broker label admitted", host)
		}
	}
	if _, err := kafkaRouteForBroker(&target, "broker.example.test", 9094); err == nil {
		t.Fatal("unapproved broker port admitted")
	}
	if _, err := kafkaRouteForEndpoint(&target, "tls://BROKER.EXAMPLE.TEST:9093"); err == nil {
		t.Fatal("noncanonical direct route admitted")
	}
}

func TestKafkaManagementEndpointsAndMapRejectAmbiguity(t *testing.T) {
	for name, edit := range map[string]func(*ManagementTarget){
		"canonical endpoint duplicate": func(t *ManagementTarget) {
			t.Endpoints = append(t.Endpoints, "tls://BROKER.EXAMPLE.TEST:9093")
			t.Kafka.DialAddresses["tls://BROKER.EXAMPLE.TEST:9093"] = "127.0.0.1:9093"
		},
		"canonical map duplicate": func(t *ManagementTarget) {
			t.Endpoints = append(t.Endpoints, "tls://second.test:9093")
			t.Kafka.DialAddresses["tls://BROKER.EXAMPLE.TEST:9093"] = "127.0.0.1:9093"
		},
		"literal identity mismatch": func(t *ManagementTarget) {
			t.Endpoints = []string{"tls://127.0.0.2:9093"}
			t.Kafka.DialAddresses = map[string]string{t.Endpoints[0]: "127.0.0.1:9093"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			target := kafkaConfigTestTarget(t)
			edit(&target)
			if err := prepareKafkaManagementTarget(&target); err == nil {
				t.Fatal("ambiguous transport profile admitted")
			}
		})
	}
	for _, endpoint := range []string{"tls://host:0", "tls://host:65536", "tls://host:09093", "tls://host:", "tls://host:9093?", "tls://host:9093#", "tls://host:9093/path", "tls://user@host:9093", "tls://[host]:9093", "tls://høst:9093", "tls://host.:9093", "tls://[::%en0]:9093"} {
		if _, _, _, err := canonicalKafkaEndpoint(endpoint); err == nil {
			t.Fatal("malformed endpoint admitted", endpoint)
		}
	}
}

func TestKafkaManagementLocalJSONRejectsDuplicateAndUnboundedRouting(t *testing.T) {
	for _, document := range []string{
		`null`, `{}`, `{"cluster_id":"c","cluster_id":"other","dial_addresses":{"tls://host:9093":"127.0.0.1:9093"}}`,
		`{"cluster_id":"c","dial_addresses":{"tls://host:9093":"127.0.0.1:9093","tls://host:9093":"127.0.0.2:9093"}}`,
		`{"Cluster_ID":"c","dial_addresses":{"tls://host:9093":"127.0.0.1:9093"}}`,
		`{"cluster_id":"c","dial_addresses":null}`, `{"cluster_id":"c","dial_addresses":{}}`,
		`{"cluster_id":"c","dial_addresses":{"tls://host:9093":"127.0.0.1:9093"},"insecure_skip_verify":true}`,
	} {
		var config KafkaManagementSettings
		if json.Unmarshal([]byte(document), &config) == nil {
			t.Fatal("invalid local Kafka settings decoded", document)
		}
	}
	var config KafkaManagementSettings
	if err := json.Unmarshal([]byte(`{"cluster_id":"c","dial_addresses":{"tls://host:9093":"127.0.0.1:9093"}}`), &config); err != nil {
		t.Fatal(err)
	}
	bounded := KafkaManagementSettings{ClusterID: "c", DialAddresses: make(map[string]string)}
	for i := int64(1); i <= 16; i++ {
		bounded.DialAddresses["tls://host"+big.NewInt(i).String()+":9093"] = "127.0.0.1:9093"
	}
	body, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal("exact routing limit rejected", err)
	}
	bounded.DialAddresses["tls://overflow:9093"] = "127.0.0.1:9093"
	body, err = json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(body, &config) == nil {
		t.Fatal("routing overflow accepted")
	}
}

func TestKafkaManagementTrustRejectsMalformedAndExcessiveBundles(t *testing.T) {
	valid, _ := kafkaConfigTestCA(t, 1)
	cases := map[string][]byte{
		"empty": nil, "garbage": []byte("invalid"), "leading garbage": append([]byte("invalid\n"), valid...),
		"trailing garbage":      append(append([]byte(nil), valid...), []byte("invalid")...),
		"invalid DER":           pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}),
		"other block":           append(append([]byte(nil), valid...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("private")})...),
		"duplicate certificate": append(append([]byte(nil), valid...), valid...),
		"oversize":              []byte(strings.Repeat(" ", kafkaManagementMaxTrustBytes+1)),
	}
	cases["malformed first block"] = append([]byte("-----BEGIN CERTIFICATE-----\n!!!\n-----END CERTIFICATE-----\n"), valid...)
	cases["unterminated first block"] = append([]byte("-----BEGIN CERTIFICATE-----\n!!!\n"), valid...)
	tooMany := []byte{}
	for i := int64(1); i <= kafkaManagementMaxTrustCertificates+1; i++ {
		body, _ := kafkaConfigTestCA(t, i)
		tooMany = append(tooMany, body...)
	}
	cases["too many certificates"] = tooMany
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := loadKafkaManagementTrust(path); err == nil {
				t.Fatal("invalid trust bundle accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if roots, digest, count, err := loadKafkaManagementTrust(path); err != nil || roots == nil || len(digest) != 64 || count != 1 {
		t.Fatal("valid trust rejected", err)
	}
	// Exact bounds are valid; overflow and malformed content above remain
	// rejected independently of certificate parsing success.
	atByteLimit := append(append([]byte(nil), valid...), []byte(strings.Repeat(" ", kafkaManagementMaxTrustBytes-len(valid)))...)
	if err := os.WriteFile(path, atByteLimit, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, count, err := loadKafkaManagementTrust(path); err != nil || count != 1 {
		t.Fatal("exact trust byte limit rejected", err)
	}
	countBound := []byte{}
	for i := int64(1); i <= kafkaManagementMaxTrustCertificates; i++ {
		body, _ := kafkaConfigTestCA(t, i)
		countBound = append(countBound, body...)
	}
	if err := os.WriteFile(path, countBound, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, count, err := loadKafkaManagementTrust(path); err != nil || count != kafkaManagementMaxTrustCertificates {
		t.Fatal("exact trust certificate limit rejected", count, err)
	}

	if _, _, _, err := loadKafkaManagementTrust(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted as trust")
	}
	link := filepath.Join(filepath.Dir(path), "link.pem")
	if err := os.Symlink(path, link); err == nil {
		if _, _, _, err := loadKafkaManagementTrust(link); err == nil {
			t.Fatal("symlink accepted as trust")
		}
	}
}

func TestKafkaManagementTrustIsImmutableAndReconstructedExplicitly(t *testing.T) {
	target := kafkaConfigTestTarget(t)
	original, err := os.ReadFile(target.TLSCAFile)
	if err != nil {
		t.Fatal(err)
	}
	originalBlock, _ := pem.Decode(original)
	first, err := x509.ParseCertificate(originalBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := kafkaConfigTestAgent(t, &target, []string{"kafka-offsets"})
	if err != nil {
		t.Fatal(err)
	}
	loaded := agent.cfg.AdapterTargets[0].Management
	secondPEM, second := kafkaConfigTestCA(t, 2)
	if err := os.WriteFile(target.TLSCAFile, secondPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateKafkaManagementTarget(loaded); err != nil {
		t.Fatal("file rewrite changed frozen validation", err)
	}
	if _, err := first.Verify(x509.VerifyOptions{Roots: loaded.kafkaApproval.roots}); err != nil {
		t.Fatal("loaded CA was replaced", err)
	}
	if _, err := second.Verify(x509.VerifyOptions{Roots: loaded.kafkaApproval.roots}); err == nil {
		t.Fatal("rewritten CA silently acquired approval")
	}
	// Pass a previously compiled target back to New: inherited trust must not
	// bypass reconstruction of the changed local CA approval.
	restarted, err := kafkaConfigTestAgent(t, loaded, []string{"kafka-offsets"})
	if err != nil {
		t.Fatal(err)
	}
	fresh := restarted.cfg.AdapterTargets[0].Management
	if _, err := second.Verify(x509.VerifyOptions{Roots: fresh.kafkaApproval.roots}); err != nil {
		t.Fatal("New did not reload local approval", err)
	}
	if _, err := first.Verify(x509.VerifyOptions{Roots: fresh.kafkaApproval.roots}); err == nil {
		t.Fatal("New reused old trust")
	}
	if err := os.Remove(target.TLSCAFile); err != nil {
		t.Fatal(err)
	}
	if len(agent.management.readiness()) != 1 || len(agent.approvedDestinations()) != 1 {
		t.Fatal("heartbeat reread deleted trust file")
	}
	if _, err := kafkaConfigTestAgent(t, &target, []string{"kafka-offsets"}); err == nil {
		t.Fatal("missing trust accepted by new agent")
	}
}

func TestKafkaManagementOwnedApprovalRejectsCallerMutation(t *testing.T) {
	target := kafkaConfigTestTarget(t)
	agent, err := kafkaConfigTestAgent(t, &target, []string{"kafka-offsets"})
	if err != nil {
		t.Fatal(err)
	}
	owned := agent.cfg.AdapterTargets[0].Management
	target.Kafka.ClusterID = "another"
	target.Kafka.DialAddresses["tls://broker.example.test:9093"] = "127.0.0.2:9093"
	target.Endpoints[0] = "tls://foreign.test:9093"
	if err := validateKafkaManagementTarget(owned); err != nil {
		t.Fatal("caller changed owned approval", err)
	}
	copy := owned.clone()
	copy.Kafka.DialAddresses[copy.Endpoints[0]] = "127.0.0.2:9093"
	copy.kafkaApproval.routes[copy.Endpoints[0]] = kafkaManagementRoute{}
	if err := validateKafkaManagementTarget(owned); err != nil {
		t.Fatal("clone changed owned approval", err)
	}
	owned.Kafka.ClusterID = "changed"
	if len(agent.management.readiness()) != 0 || len(agent.approvedDestinations()) != 0 {
		t.Fatal("altered profile advertised readiness")
	}
}

func TestKafkaManagementDormantApprovalDoesNotBecomeExecutable(t *testing.T) {
	target := managementTestTarget()
	agent, err := kafkaConfigTestAgent(t, &target, []string{"go-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	agent.cfg.Packs = append(agent.cfg.Packs, "kafka-offsets")
	if len(agent.management.readiness()) != 0 || len(agent.approvedDestinations()) != 0 {
		t.Fatal("unprepared dormant target became executable")
	}
	now := time.Now().UTC()
	scope, _ := target.Scope.Hash()
	identity, _ := target.Identity.Fingerprint()
	job := RunJob{ID: "job", ProjectID: "project", RunID: "run", CollectorID: "collector", SourceID: "source", SourceRevision: 1, Pack: "kafka-offsets", Destination: "management://orders", IntervalSeconds: 10, StartedAt: now, ExpiresAt: now.Add(time.Hour), LeaseExpiresAt: now.Add(time.Minute), Management: &ManagementJob{Version: 1, ScopeHash: scope, IdentityHash: identity}}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	agent.cfg.ControllerPublicKey = key.Public().(ed25519.PublicKey)
	signed, err := SignRunJob(job, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.management.candidate(signed, now); err == nil {
		t.Fatal("runtime admitted generic target as genuine Kafka")
	}
}

func TestKafkaManagementNewValidatesEverySelectedTarget(t *testing.T) {
	target := kafkaConfigTestTarget(t)
	agent, err := kafkaConfigTestAgent(t, &target, []string{"kafka-offsets"})
	if err != nil {
		t.Fatal(err)
	}
	invalid := target.clone()
	invalid.Kafka.ClusterID = "another-cluster"
	config := agent.cfg
	config.AdapterTargets = append(append([]AdapterTarget(nil), config.AdapterTargets...), AdapterTarget{Pack: "kafka-offsets", Destination: "management://other", Management: &invalid})
	if constructed, err := New(config); err == nil || constructed != nil {
		t.Fatal("valid first target hid invalid second approval")
	}
}

func TestKafkaManagementConstructionAndReadinessMakeNoTargetConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	target := kafkaConfigTestTarget(t)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	target.Endpoints = []string{"tls://approved.example.test:" + port}
	target.Kafka.DialAddresses = map[string]string{target.Endpoints[0]: listener.Addr().String()}
	agent, err := kafkaConfigTestAgent(t, &target, []string{"kafka-offsets"})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.management.readiness()) != 1 || len(agent.approvedDestinations()) != 1 {
		t.Fatal("valid local profile lacks readiness")
	}
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if conn, err := listener.Accept(); err == nil {
		conn.Close()
		t.Fatal("construction/readiness contacted target")
	}
}

func TestManagementRetainedResourceJoinsProofAndRejectsLateRegistration(t *testing.T) {
	poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	proof, err := poll.retainResource()
	if err != nil {
		t.Fatal(err)
	}
	defer proof()
	done := make(chan struct{})
	go func() { poll.finish(); close(done) }()
	// Wait until finish has fenced new registration rather than assuming a
	// goroutine has reached its entry point after a timing delay.
	deadline := time.Now().Add(time.Second)
	for {
		poll.mu.Lock()
		closed := poll.closed
		poll.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finish did not fence admission")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := poll.retainResource(); err == nil {
		t.Fatal("late resource escaped completed admission")
	}
	select {
	case <-done:
		t.Fatal("resource ownership released before proof")
	default:
	}
	proof()
	proof()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("physical proof failed to join")
	}
}

func TestManagementUnprovenResourceKeepsSourceChargedThroughCancellation(t *testing.T) {
	entered := make(chan func(), 1)
	var observations atomic.Int64
	var resourceMu sync.Mutex
	var testFinished bool
	var retainedProof func()
	fixture := newManagementFixture(t, managementObserverFunc(func(ctx context.Context, poll *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		observations.Add(1)
		proof, err := poll.retainResource()
		if err != nil {
			return nil, err
		}
		resourceMu.Lock()
		if testFinished {
			resourceMu.Unlock()
			proof()
			return nil, errors.New("test completed before observer entered")
		}
		retainedProof = proof
		resourceMu.Unlock()
		entered <- proof
		return nil, errors.New("injected unexplained close failure")
	}))
	// Release only this test's retained proof before the fixture joins its
	// scheduler, including an assertion failure or late observer entry.
	t.Cleanup(func() {
		resourceMu.Lock()
		testFinished = true
		proof := retainedProof
		resourceMu.Unlock()
		if proof != nil {
			proof()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signed, err := SignRunJob(fixture.jobs[0], fixture.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.agent.management.update(ctx, []SignedRunJob{signed}); err != nil {
		t.Fatal(err)
	}
	var proof func()
	select {
	case proof = <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("observer did not enter")
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(proof) }
	defer release()
	cancel()
	if err := fixture.agent.pushHeartbeat(context.Background(), heartbeat{Health: "healthy"}); err != nil {
		t.Fatal("unproven physical resource blocked heartbeat", err)
	}
	scheduler := fixture.agent.management
	scheduler.mu.Lock()
	task := scheduler.active[managementSourceKey("project", "source")]
	scheduler.mu.Unlock()
	if task == nil || observations.Load() != 1 {
		t.Fatal("unproven resource cleared source charge")
	}
	select {
	case <-task.done:
		t.Fatal("cancellation asserted physical completion")
	default:
	}
	if fixture.releases.Load() != 0 {
		t.Fatal("unproven close sent completion confirmation")
	}
	release()
	select {
	case <-task.done:
	case <-time.After(3 * time.Second):
		t.Fatal("confirmed resource did not join")
	}
}

func TestManagementRequestBudgetUsesBoundedTypedReason(t *testing.T) {
	poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	poll.requests = 512
	called := false
	err := poll.request(context.Background(), poll.target.Endpoints[0], false, func(context.Context) error { called = true; return nil })
	var budget *managementBudgetError
	if !errors.As(err, &budget) || called {
		t.Fatal("exhausted budget was not typed before provider operation", err)
	}
	poll.finish()
}
