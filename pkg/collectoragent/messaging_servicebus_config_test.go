package collectoragent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func serviceBusConfigTestTarget(t *testing.T) ManagementTarget {
	t.Helper()
	pem, _ := kafkaConfigTestCA(t, 701)
	path := filepath.Join(t.TempDir(), "servicebus-ca.pem")
	if err := os.WriteFile(path, pem, 0600); err != nil {
		t.Fatal(err)
	}
	return ManagementTarget{Scope: analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindServiceBus, Provider: "namespace.example.test", Entities: []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "orders"}}}}, Identity: analysis.ManagementIdentity{Mode: "shared_key", Principal: "reader", PolicyRevision: "policy-v1", CredentialRef: "management-reader", CredentialVersion: "key-v1"}, Endpoints: []string{"https://namespace.example.test"}, TLSCAFile: path, ServiceBus: &ServiceBusManagementSettings{DialAddress: "127.0.0.1:443"}}
}

func TestServiceBusManagementConfigRejectsUnsafeExecutableProfiles(t *testing.T) {
	for name, change := range map[string]func(*ManagementTarget){
		"missing settings":   func(t *ManagementTarget) { t.ServiceBus = nil },
		"wrong kind":         func(t *ManagementTarget) { t.Scope.Kind = analysis.ManagementKindKafka },
		"wrong namespace":    func(t *ManagementTarget) { t.Scope.Provider = "another.example.test" },
		"mixed provider":     func(t *ManagementTarget) { t.Kafka = &KafkaManagementSettings{} },
		"anonymous":          func(t *ManagementTarget) { t.Identity.Mode = "anonymous" },
		"ambient identity":   func(t *ManagementTarget) { t.Identity.Mode = "managed_identity" },
		"missing credential": func(t *ManagementTarget) { t.Identity.CredentialRef = ""; t.Identity.CredentialVersion = "" },
		"missing version":    func(t *ManagementTarget) { t.Identity.CredentialVersion = "" },
		"policy too long":    func(t *ManagementTarget) { t.Identity.Principal = strings.Repeat("a", 51) },
		"policy unsafe":      func(t *ManagementTarget) { t.Identity.Principal = "reader&sig=hidden" },
		"missing trust":      func(t *ManagementTarget) { t.TLSCAFile = "" },
		"relative trust":     func(t *ManagementTarget) { t.TLSCAFile = "relative.pem" },
		"too fast":           func(t *ManagementTarget) { t.MinIntervalSeconds = 29 },
		"multiple origins":   func(t *ManagementTarget) { t.Endpoints = append(t.Endpoints, "https://another.example.test") },
		"missing route":      func(t *ManagementTarget) { t.ServiceBus.DialAddress = "" },
		"DNS route":          func(t *ManagementTarget) { t.ServiceBus.DialAddress = "namespace.example.test:443" },
		"wrong port":         func(t *ManagementTarget) { t.ServiceBus.DialAddress = "127.0.0.1:8443" },
		"unspecified":        func(t *ManagementTarget) { t.ServiceBus.DialAddress = "0.0.0.0:443" },
		"multicast":          func(t *ManagementTarget) { t.ServiceBus.DialAddress = "224.0.0.1:443" },
		"broadcast":          func(t *ManagementTarget) { t.ServiceBus.DialAddress = "255.255.255.255:443" },
		"zone":               func(t *ManagementTarget) { t.ServiceBus.DialAddress = "[fe80::1%en0]:443" },
	} {
		t.Run(name, func(t *testing.T) {
			target := serviceBusConfigTestTarget(t)
			change(&target)
			if prepareServiceBusManagementTarget(&target) == nil {
				t.Fatal("unsafe genuine profile approved")
			}
		})
	}
}
func TestServiceBusManagementCanonicalOriginAndExactRoutes(t *testing.T) {
	for _, origin := range []string{"https://NAMESPACE.EXAMPLE.TEST:443/", "https://namespace.example.test/", "https://namespace.example.test:443"} {
		target := serviceBusConfigTestTarget(t)
		target.Endpoints[0] = origin
		target.ServiceBus.DialAddress = "[::ffff:127.0.0.1]:443"
		target.Scope.Entities = []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "Orders/West.Coast"}}, {ServiceBus: &analysis.ServiceBusManagementEntity{Topic: "Events/West", Subscription: "Workers_1"}}}
		if err := prepareServiceBusManagementTarget(&target); err != nil {
			t.Fatal(err)
		}
		if target.Endpoints[0] != "https://namespace.example.test" || target.ServiceBus.DialAddress != "127.0.0.1:443" {
			t.Fatal("canonical approval changed identity")
		}
		for i, expected := range []string{"/orders/west.coast?api-version=2021-05", "/events/west/Subscriptions/workers_1?api-version=2021-05"} {
			route, err := serviceBusRouteForEntity(&target, target.Scope.Entities[i])
			if err != nil || route.requestPath != expected || route.requestURL != target.Endpoints[0]+expected || route.resourceURI != strings.Split(route.requestURL, "?")[0] || route.serverName != "namespace.example.test" || route.network != "tcp4" {
				t.Fatal("approved exact route lost", err)
			}
			if route.entity.ServiceBus.Queue != target.Scope.Entities[i].ServiceBus.Queue {
				t.Fatal("authored identity rewritten")
			}
		}
		if _, err := serviceBusRouteForEntity(&target, analysis.ManagementEntity{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "unapproved"}}); err == nil {
			t.Fatal("response expanded scope")
		}
	}
	for _, origin := range []string{"http://namespace.example.test", "https://namespace.example.test:0443", "https://namespace.example.test:8443", "https://namespace.example.test?", "https://namespace.example.test#", "https://user@namespace.example.test", "https://namespace.example.test/queue", "https://namespace.example.test/%2f", "https://[namespace.example.test]:443", "https://namespace.example.test.", "https://127.0.0.1", "https://localhost", "https://námé.example.test"} {
		if _, _, err := canonicalServiceBusOrigin(origin); err == nil {
			t.Fatal("ambiguous origin accepted", origin)
		}
	}
}
func TestServiceBusManagementPathBoundsAndCollision(t *testing.T) {
	for _, name := range []string{"", ".", "..", "orders//west", "/orders", "orders/", "orders/%2f", "orders\\west", "orders?x=1", "orders#secret", "orders/$deadletterqueue", "orders/Subscriptions/sub", "orders/messages", "rules", "orders/authorizationrules", "orders/_hidden", "orders/west-", "ordérs", strings.Repeat("a", 257)} {
		if _, err := serviceBusEntityPath(name, false); err == nil {
			t.Fatal("unsafe entity path accepted", name)
		}
	}
	for _, name := range []string{"x", "Orders/West_1", strings.Repeat("a", 256)} {
		if _, err := serviceBusEntityPath(name, false); err != nil {
			t.Fatal("valid bounded queue rejected", err)
		}
	}
	if _, err := serviceBusEntityPath(strings.Repeat("s", 50), true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{strings.Repeat("s", 51), "topic/sub"} {
		if _, err := serviceBusEntityPath(name, true); err == nil {
			t.Fatal("unsafe subscription accepted")
		}
	}
	target := serviceBusConfigTestTarget(t)
	target.Scope.Entities = append(target.Scope.Entities, analysis.ManagementEntity{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "ORDERS"}})
	if prepareServiceBusManagementTarget(&target) == nil {
		t.Fatal("casing alias creates duplicate requests")
	}
}
func TestServiceBusManagementSettingsJSONIsStrict(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"dial_address":"127.0.0.1:443","dial_address":"127.0.0.2:443"}`, `{"Dial_Address":"127.0.0.1:443"}`, `{"dial_address":"127.0.0.1:443","insecure_skip_verify":true}`, `{"dial_address":"hostname:443"}`} {
		var settings ServiceBusManagementSettings
		if json.Unmarshal([]byte(body), &settings) == nil {
			t.Fatal("unsafe settings decoded", body)
		}
	}
	var settings ServiceBusManagementSettings
	if err := json.Unmarshal([]byte(`{"dial_address":"[::1]:443"}`), &settings); err != nil {
		t.Fatal(err)
	}
}
func TestServiceBusManagementApprovalOwnsTrustAndMappings(t *testing.T) {
	target := serviceBusConfigTestTarget(t)
	first, err := os.ReadFile(target.TLSCAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots, _, _, err := loadKafkaManagementTrust(target.TLSCAFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareServiceBusManagementTarget(&target); err != nil {
		t.Fatal(err)
	}
	copy := target.clone()
	key, _ := copy.Scope.Entities[0].ID()
	route := copy.serviceBusApproval.routes[key]
	route.entity.ServiceBus.Queue = "changed"
	copy.serviceBusApproval.routes[key] = route
	copy.ServiceBus.DialAddress = "127.0.0.2:443"
	if err := validateServiceBusManagementTarget(&target); err != nil {
		t.Fatal("clone mutated approval", err)
	}
	if validateServiceBusManagementTarget(&copy) == nil {
		t.Fatal("altered approval remained executable")
	}
	second, cert := kafkaConfigTestCA(t, 702)
	if err := os.WriteFile(target.TLSCAFile, second, 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateServiceBusManagementTarget(&target); err != nil {
		t.Fatal("runtime reread changed trust", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: target.serviceBusApproval.roots}); err == nil {
		t.Fatal("changed file silently gained approval")
	}
	if !target.serviceBusApproval.roots.Equal(roots) {
		t.Fatal("loaded trust changed")
	}
	if err := os.Remove(target.TLSCAFile); err != nil {
		t.Fatal(err)
	}
	if err := validateServiceBusManagementTarget(&target); err != nil {
		t.Fatal("deleted file changed readiness", err)
	}
	if err := prepareServiceBusManagementTarget(&target); err == nil {
		t.Fatal("reconstruction ignored deleted trust")
	}
	if len(first) == 0 {
		t.Fatal("original fixture unexpectedly empty")
	}
}

func serviceBusConfigTestAgent(t *testing.T, target *ManagementTarget, packs []string) (*Agent, error) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private := make([]byte, 32)
	if _, err := rand.Read(private); err != nil {
		t.Fatal(err)
	}
	return New(Config{ControllerURL: "http://localhost:8080", CollectorID: "collector", Token: "token", ControllerPublicKey: pub, CredentialPrivateKey: private, SpoolDirectory: t.TempDir(), Packs: packs, AdapterTargets: []AdapterTarget{{Pack: "servicebus-runtime", Destination: "management://orders", Management: target}}})
}
func TestServiceBusManagementNewReloadsOnlySelectedGenuineApproval(t *testing.T) {
	target := serviceBusConfigTestTarget(t)
	agent, err := serviceBusConfigTestAgent(t, &target, []string{"servicebus-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	owned := agent.cfg.AdapterTargets[0].Management
	if len(agent.management.readiness()) != 1 || len(agent.approvedDestinations()) != 1 {
		t.Fatal("valid genuine profile missing readiness")
	}
	target.ServiceBus.DialAddress = "127.0.0.2:443"
	target.Scope.Entities[0].ServiceBus.Queue = "changed"
	if err := validateServiceBusManagementTarget(owned); err != nil {
		t.Fatal("caller mutated agent approval", err)
	}
	second, cert := kafkaConfigTestCA(t, 703)
	if err := os.WriteFile(owned.TLSCAFile, second, 0600); err != nil {
		t.Fatal(err)
	}
	if len(agent.management.readiness()) != 1 || len(agent.approvedDestinations()) != 1 {
		t.Fatal("heartbeat reloaded changed trust")
	}
	restarted, err := serviceBusConfigTestAgent(t, owned, []string{"servicebus-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	fresh := restarted.cfg.AdapterTargets[0].Management
	if _, err := cert.Verify(x509.VerifyOptions{Roots: fresh.serviceBusApproval.roots}); err != nil {
		t.Fatal("New inherited old compiled trust", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: owned.serviceBusApproval.roots}); err == nil {
		t.Fatal("path rewrite silently changed old trust")
	}
	if err := os.Remove(owned.TLSCAFile); err != nil {
		t.Fatal(err)
	}
	if len(agent.management.readiness()) != 1 {
		t.Fatal("runtime reread deleted trust")
	}
	if constructed, err := serviceBusConfigTestAgent(t, owned, []string{"servicebus-runtime"}); err == nil || constructed != nil {
		t.Fatal("New ignored missing trust")
	}
}
func TestServiceBusManagementDormantTargetNeverAdvertisesGenuineReadiness(t *testing.T) {
	target := serviceBusConfigTestTarget(t)
	target.ServiceBus = nil
	target.Identity = analysis.ManagementIdentity{Mode: "anonymous", Principal: "generic-local-approval", PolicyRevision: "revision"}
	target.TLSCAFile = ""
	agent, err := serviceBusConfigTestAgent(t, &target, []string{"go-runtime"})
	if err != nil {
		t.Fatal("dormant foundation compatibility lost", err)
	}
	agent.cfg.Packs = append(agent.cfg.Packs, "servicebus-runtime")
	if len(agent.management.readiness()) != 0 || len(agent.approvedDestinations()) != 0 {
		t.Fatal("dormant generic target became executable")
	}
	if constructed, err := serviceBusConfigTestAgent(t, &target, []string{"servicebus-runtime"}); err == nil || constructed != nil {
		t.Fatal("production New bypassed real validation")
	}
}

func TestKafkaManagementRejectsServiceBusTransportSettings(t *testing.T) {
	target := kafkaConfigTestTarget(t)
	target.ServiceBus = &ServiceBusManagementSettings{DialAddress: "127.0.0.1:443"}
	if err := prepareKafkaManagementTarget(&target); err == nil {
		t.Fatal("Kafka silently accepted another provider transport profile")
	}
}
