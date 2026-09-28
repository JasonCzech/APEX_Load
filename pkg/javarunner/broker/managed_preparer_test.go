package broker

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func managedProfile() ManagedPreparationConfig {
	pod := podConfig()
	pod.TLSSecret = ""
	return ManagedPreparationConfig{ConfigurationSHA256: binding().ConfigurationSHA256, Pod: pod, BrokerClientURI: controlConfig().BrokerClientURI, LeaseTTL: 10 * time.Second, PreparationTimeout: 10 * time.Second, InvocationTimeout: time.Second, TransportLifetime: time.Hour}
}
func managedContext() javarunner.DedicatedHostContext {
	return javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: "session", VUserID: 5, GroupName: "group", ScenarioID: "run"}
}

func TestManagedPreparationCreatesTransportPodAndTLSBarrier(t *testing.T) {
	s, epoch, _ := newStore(t)
	ca, key := credentialCA(t)
	brokerCA, _, brokerCertificate, _, _ := controlCertificates(t)
	issuer, e := NewHostCredentialIssuer(ca, key, brokerCA, "apexload", "hosts.internal")
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	tlsConfig := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{brokerCertificate}}
	var pod map[string]any
	var secret transportSecret
	var methods []string
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		methods = append(methods, req.Method+" "+req.URL.Path)
		switch {
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/secrets"):
			if json.NewDecoder(req.Body).Decode(&secret) != nil {
				t.Error("invalid secret")
				w.WriteHeader(400)
				return
			}
			secret.Metadata.UID = "transport-uid"
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(secret)
		case req.Method == "POST" && strings.HasSuffix(req.URL.Path, "/pods"):
			if secret.Metadata.UID == "" {
				t.Error("pod preceded transport")
			}
			if json.NewDecoder(req.Body).Decode(&pod) != nil {
				t.Error("bad pod")
				return
			}
			pod["metadata"].(map[string]any)["uid"] = "pod-uid"
			pod["status"] = map[string]any{"podIP": "10.20.30.40", "containerStatuses": []any{map[string]any{"name": "java-host", "containerID": "containerd://runtime"}}}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(pod)
		case req.Method == "GET" && strings.Contains(req.URL.Path, "/pods/"):
			_ = json.NewEncoder(w).Encode(pod)
		default:
			t.Error("unexpected call", req.Method, req.URL.Path)
			w.WriteHeader(500)
		}
	}, &testAttestor{})
	p, e := NewManagedKubernetesPreparer(s, k, epoch, issuer, tlsConfig, managedProfile(), []javarunner.DedicatedHostContext{managedContext()})
	if e != nil {
		t.Fatal(e)
	}
	var invocations atomic.Int32
	// Only redirect the otherwise fixed private pod address to a local TLS
	// listener. All generated keys, identity checks and controls remain real.
	p.newClient = func(endpoint, identity string, cfg SupervisorConfig, tc *tls.Config) (*SupervisorClient, error) {
		if endpoint != "https://10.20.30.40:9443" || identity != "spiffe://apexload/java-host/"+cfg.HostID {
			t.Error("unbound endpoint/identity")
		}
		pair, e := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
		if e != nil {
			return nil, e
		}
		control, e := NewSupervisorControl(t.Context(), cfg, &countedWaveRuntime{calls: &invocations})
		if e != nil {
			return nil, e
		}
		t.Cleanup(control.Close)
		server := httptest.NewUnstartedServer(control)
		server.TLS, e = SupervisorTLS(pair, secret.Data["ca.crt"], cfg.BrokerClientURI)
		if e != nil {
			return nil, e
		}
		server.StartTLS()
		t.Cleanup(server.Close)
		return NewSupervisorClient(server.URL, identity, cfg, tc)
	}
	w, e := NewDedicatedWave(t.Context(), s, epoch, binding(), []string{"session"}, 100, 10*time.Second, 10*time.Second, p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = w.Abort("test_complete") })
	if e = w.Prepare(t.Context()); e != nil {
		t.Fatal(e)
	}
	if invocations.Load() != 0 {
		t.Fatal("user initialized during preparation")
	}
	id := w.Records()[0].ID
	if _, e = w.Invoke(t.Context(), id, "init", nil); e == nil {
		t.Fatal("invoke before commit")
	}
	if e = w.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e = w.Invoke(t.Context(), id, "init", nil); e != nil {
		t.Fatal(e)
	}
	if invocations.Load() != 1 {
		t.Fatal("unexpected lifecycle count")
	}
	if len(methods) != 3 || methods[0] != "POST /api/v1/namespaces/java-test/secrets" || methods[1] != "POST /api/v1/namespaces/java-test/pods" {
		t.Fatal("unexpected mutation/replay", methods)
	}
	containers := pod["spec"].(map[string]any)["containers"].([]any)
	args := containers[0].(map[string]any)["args"].([]any)
	encoded := args[len(args)-1].(string)
	bound, e := javarunner.DecodeDedicatedHostContext(encoded)
	if e != nil || !reflect.DeepEqual(bound, managedContext()) {
		t.Fatal("context was not bound", e)
	}
}

func TestManagedPreparationRejectsUnboundInputsBeforeMutation(t *testing.T) {
	s, epoch, _ := newStore(t)
	k := kubeFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid preparation reached Kubernetes") }, &testAttestor{})
	ca, _, cert, _, roots := controlCertificates(t)
	issuer := credentialIssuer(t)
	issuer.brokerTrust = ca
	tc := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{cert}}
	for _, mutate := range []func(*ManagedPreparationConfig){
		func(c *ManagedPreparationConfig) { c.Pod.TLSSecret = "shared" },
		func(c *ManagedPreparationConfig) { v := managedContext(); c.Pod.Context = &v },
		func(c *ManagedPreparationConfig) { c.ConfigurationSHA256 = "bad" },
		func(c *ManagedPreparationConfig) { c.TransportLifetime = 25 * time.Hour },
		func(c *ManagedPreparationConfig) { c.TransportLifetime = c.PreparationTimeout },
		func(c *ManagedPreparationConfig) { c.LeaseTTL = 0 },
	} {
		cfg := managedProfile()
		mutate(&cfg)
		if _, e := NewManagedKubernetesPreparer(s, k, epoch, issuer, tc, cfg, []javarunner.DedicatedHostContext{managedContext()}); e == nil {
			t.Fatal("invalid configuration admitted")
		}
	}
	if _, e := NewManagedKubernetesPreparer(s, k, epoch, issuer, tc, managedProfile(), []javarunner.DedicatedHostContext{managedContext(), managedContext()}); e == nil {
		t.Fatal("duplicate context")
	}
	p, e := NewManagedKubernetesPreparer(s, k, epoch, issuer, tc, managedProfile(), []javarunner.DedicatedHostContext{managedContext()})
	if e != nil {
		t.Fatal(e)
	}
	rs, e := s.ReserveDedicatedWave(t.Context(), epoch, binding(), []string{"session"}, 100, 10*time.Second, 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Record){func(r *Record) { r.SessionID = "different" }, func(r *Record) { r.MemoryBytes++ }, func(r *Record) { r.Binding.OwnerEpoch = "old" }, func(r *Record) { r.PrepareUntil = r.PrepareUntil.Add(time.Second) }, func(r *Record) { r.BrokerEpoch = "old" }} {
		r := rs[0]
		mutate(&r)
		if _, e = p.PrepareHost(t.Context(), r); e == nil {
			t.Fatal("forged reservation")
		}
	}
}

func TestSupervisorEndpointRequiresExactBoundaryAndUnicastAddress(t *testing.T) {
	expected := PodReceipt{Namespace: "java-test", Name: "java-host", UID: "pod-uid", HostID: "host", BrokerEpoch: "epoch", ContainerID: "containerd://runtime"}
	for _, tt := range []struct{ address, change, want string }{
		{"10.2.3.4", "", "https://10.2.3.4:9443"}, {"fd00::1", "", "https://[fd00::1]:9443"},
		{"127.0.0.1", "", ""}, {"::1", "", ""}, {"169.254.169.254", "", ""}, {"0.0.0.0", "", ""}, {"example.com", "", ""}, {"fe80::1%eth0", "", ""},
		{"10.2.3.4", "uid", ""}, {"10.2.3.4", "container", ""}, {"10.2.3.4", "host", ""},
	} {
		t.Run(tt.address+tt.change, func(t *testing.T) {
			pod := map[string]any{"metadata": map[string]any{"name": expected.Name, "namespace": expected.Namespace, "uid": expected.UID, "annotations": map[string]string{hostAnnotation: expected.HostID, epochAnnotation: expected.BrokerEpoch}}, "spec": map[string]any{"containers": []any{map[string]string{"name": "java-host"}}}, "status": map[string]any{"podIP": tt.address, "containerStatuses": []any{map[string]string{"name": "java-host", "containerID": expected.ContainerID}}}}
			switch tt.change {
			case "uid":
				pod["metadata"].(map[string]any)["uid"] = "replacement"
			case "container":
				pod["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]string)["containerID"] = "containerd://replacement"
			case "host":
				pod["metadata"].(map[string]any)["annotations"].(map[string]string)[hostAnnotation] = "other"
			}
			k := kubeFixture(t, func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(pod) }, &testAttestor{})
			got, e := k.supervisorEndpoint(t.Context(), expected)
			if tt.want == "" {
				if e == nil {
					t.Fatal("unbound endpoint accepted", got)
				}
			} else if e != nil || got != tt.want {
				t.Fatal(got, e)
			}
		})
	}
}
