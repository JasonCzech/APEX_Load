package collectoragent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func solaceConfigTestTarget(t *testing.T) ManagementTarget {
	t.Helper()
	target := kafkaConfigTestTarget(t)
	target.Kafka = nil
	target.Scope = analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindSolace, Provider: "broker.example.test", Entities: []analysis.ManagementEntity{
		{Solace: &analysis.SolaceManagementEntity{VPN: "orders", Queue: "pending"}},
		{Solace: &analysis.SolaceManagementEntity{VPN: "orders", Queue: "dmq"}},
		{Solace: &analysis.SolaceManagementEntity{VPN: "orders", TopicEndpoint: "events"}},
	}}
	target.Identity = analysis.ManagementIdentity{Mode: "basic", Principal: "reader", PolicyRevision: "policy-1", CredentialRef: "management-reader", CredentialVersion: "approved-version"}
	target.Endpoints = []string{"https://broker.example.test:943"}
	target.Solace = &SolaceManagementSettings{Profile: "semp_v2", DialAddress: "127.0.0.1:943", AllowOwnSessionLogout: true}
	return target
}
func solaceConfigTestAgent(t *testing.T, target *ManagementTarget, packs []string) (*Agent, error) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private := make([]byte, 32)
	if _, err := rand.Read(private); err != nil {
		t.Fatal(err)
	}
	agent, err := New(Config{ControllerURL: "http://localhost:8080", CollectorID: "collector", Token: "token", ControllerPublicKey: pub, CredentialPrivateKey: private, SpoolDirectory: t.TempDir(), Packs: packs, AdapterTargets: []AdapterTarget{{Pack: "solace-semp", Destination: "management://orders", Management: target}}})
	if err == nil && agent.management != nil {
		t.Cleanup(agent.management.close)
	}
	return agent, err
}
func TestSolaceManagementConfigRejectsUnapprovedProfiles(t *testing.T) {
	for name, change := range map[string]func(*ManagementTarget){
		"missing settings":    func(t *ManagementTarget) { t.Solace = nil },
		"legacy profile":      func(t *ManagementTarget) { t.Solace.Profile = "semp_v1" },
		"implicit profile":    func(t *ManagementTarget) { t.Solace.Profile = "" },
		"no logout approval":  func(t *ManagementTarget) { t.Solace.AllowOwnSessionLogout = false },
		"provider mismatch":   func(t *ManagementTarget) { t.Scope.Provider = "other.example.test" },
		"principal separator": func(t *ManagementTarget) { t.Identity.Principal = "reader:admin" },
		"shared key":          func(t *ManagementTarget) { t.Identity.Mode = "shared_key" },
		"no ref":              func(t *ManagementTarget) { t.Identity.CredentialRef = ""; t.Identity.CredentialVersion = "" },
		"no version":          func(t *ManagementTarget) { t.Identity.CredentialVersion = "" },
		"no CA":               func(t *ManagementTarget) { t.TLSCAFile = "" },
		"relative CA":         func(t *ManagementTarget) { t.TLSCAFile = "ca.pem" },
		"invalid CA":          func(t *ManagementTarget) { _ = os.WriteFile(t.TLSCAFile, []byte("not a certificate"), 0600) },
		"too fast":            func(t *ManagementTarget) { t.MinIntervalSeconds = 29 },
		"multiple roots":      func(t *ManagementTarget) { t.Endpoints = append(t.Endpoints, "https://other.example.test:943") },
		"HTTP":                func(t *ManagementTarget) { t.Endpoints[0] = "http://broker.example.test:943" },
		"IP TLS identity":     func(t *ManagementTarget) { t.Endpoints[0] = "https://127.0.0.1:943" },
		"route port mismatch": func(t *ManagementTarget) { t.Solace.DialAddress = "127.0.0.1:944" },
		"DNS route":           func(t *ManagementTarget) { t.Solace.DialAddress = "broker.example.test:943" },
		"zone route":          func(t *ManagementTarget) { t.Solace.DialAddress = "[fe80::1%en0]:943" },
		"multicast route":     func(t *ManagementTarget) { t.Solace.DialAddress = "224.0.0.1:943" },
		"unspecified route":   func(t *ManagementTarget) { t.Solace.DialAddress = "0.0.0.0:943" },
		"path":                func(t *ManagementTarget) { t.Endpoints[0] += "/SEMP/v2/monitor" },
		"query":               func(t *ManagementTarget) { t.Endpoints[0] += "?select=other" },
		"empty query":         func(t *ManagementTarget) { t.Endpoints[0] += "?" },
		"fragment":            func(t *ManagementTarget) { t.Endpoints[0] += "#" },
		"user info":           func(t *ManagementTarget) { t.Endpoints[0] = "https://admin@broker.example.test:943" },
		"bracketed DNS":       func(t *ManagementTarget) { t.Endpoints[0] = "https://[broker.example.test]:943" },
		"mixed provider":      func(t *ManagementTarget) { t.Kafka = &KafkaManagementSettings{} },
	} {
		t.Run(name, func(t *testing.T) {
			target := solaceConfigTestTarget(t)
			change(&target)
			if err := prepareSolaceManagementTarget(&target); err == nil || target.solaceApproval != nil {
				t.Fatal("invalid executable profile retained approval")
			}
		})
	}
}
func TestSolaceManagementConfigRoutesPreserveSegmentIdentities(t *testing.T) {
	for _, name := range []string{"pending", "a/b", "a%2fb", "a,b+?x=1", "π/雪", ".", "..", "a\\b"} {
		t.Run(name, func(t *testing.T) {
			target := solaceConfigTestTarget(t)
			target.Scope.Entities = []analysis.ManagementEntity{{Solace: &analysis.SolaceManagementEntity{VPN: "v/p?%+", Queue: name}}}
			if err := prepareSolaceManagementTarget(&target); err != nil {
				t.Fatal(err)
			}
			route, err := solaceRouteForEntity(&target, target.Scope.Entities[0])
			if err != nil {
				t.Fatal(err)
			}
			if route.entity.Solace.Queue != name || route.entity.Solace.VPN != "v/p?%+" || strings.Contains(route.requestPath, "/v/p?") {
				t.Fatal("path identity was normalized or escaped into another route")
			}
			if name == "a/b" && !strings.Contains(route.requestPath, "a%2Fb?") {
				t.Fatal("literal slash escaped out of one identity segment")
			}
			if name == "." && !strings.Contains(route.requestPath, "/%2E?") || name == ".." && !strings.Contains(route.requestPath, "/%2E%2E?") {
				t.Fatal("dot segment was not escaped")
			}
			if !strings.Contains(route.requestPath, "select=msgVpnName%2CqueueName%2CdeadMsgQueue%2Cmsgs.count") {
				t.Fatal("fixed selection changed")
			}
		})
	}
	target := solaceConfigTestTarget(t)
	if err := prepareSolaceManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	logout, err := solaceLogoutRoute(&target)
	if err != nil || logout.requestPath != "/SEMP/v2/action/about/user/logout" || logout.entity.Kind() != "" {
		t.Fatal("logout approval widened or selected entity", err)
	}
	unapproved := analysis.ManagementEntity{Solace: &analysis.SolaceManagementEntity{VPN: "orders", Queue: "other"}}
	if _, err := solaceRouteForEntity(&target, unapproved); err == nil {
		t.Fatal("unapproved entity got executable route")
	}
}
func TestSolaceManagementConfigImmutableTrustAndClones(t *testing.T) {
	target := solaceConfigTestTarget(t)
	if err := prepareSolaceManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	originalDigest := target.solaceApproval.trustDigest
	cloned := target.clone()
	cloned.Solace.DialAddress = "127.0.0.2:943"
	if target.Solace.DialAddress != "127.0.0.1:943" || validateSolaceManagementTarget(&cloned) == nil {
		t.Fatal("mutable settings alias or stale approval")
	}
	cloned = target.clone()
	key, _ := target.Scope.Entities[0].ID()
	route := cloned.solaceApproval.routes[key]
	route.entity.Solace.Queue = "other"
	cloned.solaceApproval.routes[key] = route
	if target.solaceApproval.routes[key].entity.Solace.Queue != "pending" || validateSolaceManagementTarget(&cloned) == nil {
		t.Fatal("mutable route identity alias or stale approval")
	}
	body, _ := kafkaConfigTestCA(t, 2)
	if err := os.WriteFile(target.TLSCAFile, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateSolaceManagementTarget(&target); err != nil || target.solaceApproval.trustDigest != originalDigest {
		t.Fatal("runtime reread changed immutable trust", err)
	}
	fresh := target.clone()
	if err := prepareSolaceManagementTarget(&fresh); err != nil || fresh.solaceApproval.trustDigest == originalDigest {
		t.Fatal("fresh preparation did not load changed trust", err)
	}
	if err := os.Remove(target.TLSCAFile); err != nil {
		t.Fatal(err)
	}
	if err := validateSolaceManagementTarget(&target); err != nil {
		t.Fatal("readiness/runtime touched CA path", err)
	}
	if err := prepareSolaceManagementTarget(&fresh); err == nil || fresh.solaceApproval != nil {
		t.Fatal("missing trust retained fresh approval")
	}
}
func TestSolaceManagementConfigCanonicalOriginsAndStaticRoutes(t *testing.T) {
	for _, test := range []struct {
		input, origin string
		port          uint16
	}{
		{"https://BROKER.EXAMPLE.TEST:443/", "https://broker.example.test", 443},
		{"https://broker.example.test:65535", "https://broker.example.test:65535", 65535},
	} {
		origin, host, port, err := canonicalSolaceOrigin(test.input)
		if err != nil || origin != test.origin || host != "broker.example.test" || port != test.port {
			t.Fatal("finite origin not canonicalized", err)
		}
	}
	for _, input := range []string{"https://broker.example.test:0443", "https://broker.example.test:0", "https://broker.example.test:65536", "https://broker.example.test:", "https://broker.example.test./", "https://one-label", "https://broker.example.test/%2F", "https://broker.example.test\\@other.example.test"} {
		if _, _, _, err := canonicalSolaceOrigin(input); err == nil {
			t.Fatal("ambiguous origin accepted", input)
		}
	}
	address, network, err := solaceStaticAddress("[::ffff:127.0.0.1]:943", 943)
	if err != nil || address != netip.MustParseAddrPort("127.0.0.1:943") || network != "tcp4" {
		t.Fatal("literal mapped address not frozen", err)
	}
	for _, body := range []string{`{"profile":"semp_v2","dial_address":"127.0.0.1:943","allow_own_session_logout":true,"unknown":1}`, `{"profile":"semp_v2","profile":"semp_v2","dial_address":"127.0.0.1:943","allow_own_session_logout":true}`, `{"profile":"semp_v1","dial_address":"127.0.0.1:943","allow_own_session_logout":true}`} {
		var settings SolaceManagementSettings
		if json.Unmarshal([]byte(body), &settings) == nil {
			t.Fatal("invalid local settings JSON accepted")
		}
	}
}
func TestSolaceManagementNewAndReadinessAreTrafficFree(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepts atomic.Int64
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = connection.Close()
		}
	}()
	defer func() { _ = listener.Close(); <-joined }()
	target := solaceConfigTestTarget(t)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	target.Endpoints = []string{"https://broker.example.test:" + port}
	target.Solace.DialAddress = listener.Addr().String()
	agent, err := solaceConfigTestAgent(t, &target, []string{"solace-semp"})
	if err != nil {
		t.Fatal(err)
	}
	if target.solaceApproval != nil {
		t.Fatal("New compiled caller-owned target")
	}
	readiness := agent.management.readiness()
	expectedScope, _ := target.Scope.Hash()
	expectedIdentity, _ := target.Identity.Fingerprint()
	if len(readiness) != 1 || readiness[0].ScopeHash != expectedScope || readiness[0].IdentityHash != expectedIdentity {
		t.Fatal("actual prepared profile did not advertise readiness")
	}
	select {
	case <-time.After(20 * time.Millisecond):
	}
	if accepts.Load() != 0 {
		t.Fatal("New/readiness contacted target")
	}
	// Dormant generic approvals remain compatible and never advertise readiness.
	dormant := solaceConfigTestTarget(t)
	dormant.Solace = nil
	agent, err = solaceConfigTestAgent(t, &dormant, []string{"go-runtime"})
	if err != nil || len(agent.management.readiness()) != 0 {
		t.Fatal("dormant target acquired genuine readiness", err)
	}
}
