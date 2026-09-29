package broker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

type testAttestor struct {
	calls  atomic.Int32
	proven bool
	intent UnstartedPod
}

func (a *testAttestor) BoundaryTerminated(_ context.Context, p PodReceipt) (bool, error) {
	a.calls.Add(1)
	return a.proven, nil
}
func (a *testAttestor) NeverStarted(_ context.Context, intent UnstartedPod) (bool, error) {
	a.calls.Add(1)
	a.intent = intent
	return a.proven, nil
}
func podConfig() ExecutionPodConfig {
	return ExecutionPodConfig{Image: "registry.example/java@sha256:" + strings.Repeat("a", 64), TLSSecret: "host-transport", ServiceAccount: "java-execution", CPUmilli: 500, ScratchBytes: 1 << 20}
}
func kubeFixture(t *testing.T, handler http.HandlerFunc, a *testAttestor) *KubernetesBackend {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport)
	k, err := NewKubernetesBackend(server.URL, testConfig.Namespace, transport.TLSClientConfig, func(context.Context) (string, error) { return "test-token", nil }, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	return k
}
func TestExecutionPodRestrictedAndImmutable(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	data, err := ExecutionPod(testConfig.Namespace, r, podConfig())
	if err != nil {
		t.Fatal(err)
	}
	var pod map[string]any
	if err = json.Unmarshal(data, &pod); err != nil {
		t.Fatal(err)
	}
	spec := pod["spec"].(map[string]any)
	for _, key := range []string{"hostPID", "hostIPC", "hostNetwork", "shareProcessNamespace", "automountServiceAccountToken", "enableServiceLinks"} {
		if spec[key] != false {
			t.Fatal("unsafe pod flag", key)
		}
	}
	if spec["restartPolicy"] != "Never" {
		t.Fatal("restarts enabled")
	}
	containers := spec["containers"].([]any)
	if len(containers) != 1 {
		t.Fatal("extra containers")
	}
	container := containers[0].(map[string]any)
	security := container["securityContext"].(map[string]any)
	if security["allowPrivilegeEscalation"] != false || security["readOnlyRootFilesystem"] != true || len(spec["volumes"].([]any)) != 2 {
		t.Fatal("unsafe container", spec)
	}
	mounts := container["volumeMounts"].([]any)
	scratchPaths := map[string]bool{}
	for _, raw := range mounts {
		mount := raw.(map[string]any)
		if mount["name"] == "scratch" {
			scratchPaths[mount["mountPath"].(string)] = true
		}
	}
	if len(scratchPaths) != 2 || !scratchPaths["/scratch"] || !scratchPaths["/tmp"] {
		t.Fatal("Java default temporary directory is not on bounded scratch", mounts)
	}
	for _, key := range []string{"env", "envFrom", "lifecycle"} {
		if _, exists := container[key]; exists {
			t.Fatal("unexpected credentials or hooks", key)
		}
	}
	for _, mutate := range []func(*ExecutionPodConfig){func(c *ExecutionPodConfig) { c.Image = "registry/java:latest" }, func(c *ExecutionPodConfig) { c.NodeSelector = map[string]string{"kubernetes.io/os": "windows"} }, func(c *ExecutionPodConfig) { c.ScratchBytes = 0 }, func(c *ExecutionPodConfig) { c.ServiceAccount = "../other" }} {
		c := podConfig()
		mutate(&c)
		if _, err := ExecutionPod(testConfig.Namespace, r, c); err == nil {
			t.Fatal("invalid pod configuration admitted")
		}
	}
}
func TestKubernetesProvisionDurableIntentAndNoReplay(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	calls := 0
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Header.Get("Authorization") != "Bearer test-token" || req.Method != "POST" || req.URL.Path != "/api/v1/namespaces/java-test/pods" {
			t.Error("unexpected request")
		}
		if got := record(t, s, epoch, r.ID); got.State != javarunner.HostPreparing {
			t.Error("pod create preceded durable intent")
		}
		var pod map[string]any
		if err := json.NewDecoder(req.Body).Decode(&pod); err != nil {
			t.Error(err)
		}
		pod["metadata"].(map[string]any)["uid"] = "uid-1"
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(pod)
	}, &testAttestor{})
	receipt, err := k.Provision(t.Context(), s, epoch, r.ID, podConfig())
	if err != nil || receipt.UID != "uid-1" {
		t.Fatal(receipt, err)
	}
	got := record(t, s, epoch, r.ID)
	if got.State != javarunner.HostPreparing || got.PodUID != "uid-1" || got.ContainerID != "" {
		t.Fatal("create incorrectly implied harness ready", got)
	}
	if _, err := k.Provision(t.Context(), s, epoch, r.ID, podConfig()); err == nil || calls != 1 {
		t.Fatal("pod creation replayed", calls, err)
	}
}
func TestKubernetesAmbiguousCreateRetainsReservation(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	k := kubeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("private server detail"))
	}, &testAttestor{})
	_, err := k.Provision(t.Context(), s, epoch, r.ID, podConfig())
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	got := record(t, s, epoch, r.ID)
	if got.State != javarunner.HostQuarantined || got.MemoryBytes != 100 {
		t.Fatal(got)
	}
}
func TestRejectedCreateNeedsExternalNoStartFenceAcrossRestart(t *testing.T) {
	s, old, path := newStore(t)
	host := reserve(t, s, old)
	a := &testAttestor{}
	var creates atomic.Int32
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodPost:
			creates.Add(1)
			w.WriteHeader(http.StatusForbidden)
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Error("unexpected request", req.Method)
		}
	}, a)
	if _, e := k.Provision(t.Context(), s, old, host.ID, podConfig()); !errors.Is(e, ErrCleanup) {
		t.Fatal(e)
	}
	prepared := record(t, s, old, host.ID)
	if prepared.State != javarunner.HostQuarantined || prepared.PodUID != "" || !validPolicyDigest(prepared.PodPolicySHA256) || creates.Load() != 1 {
		t.Fatal("create failure lost the durable intent", prepared, creates.Load())
	}
	restarted, e := Open(t.Context(), path, testConfig, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	epoch, e := restarted.StartEpoch(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r, e := NewReconciler(restarted, epoch, k, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Step(t.Context()); !errors.Is(e, ErrCleanup) || record(t, restarted, epoch, host.ID).State != javarunner.HostQuarantined {
		t.Fatal("HTTP rejection or NotFound released capacity", e)
	}
	if a.intent.UID != "" || a.intent.Name != host.PodName || a.intent.HostID != host.ID || a.intent.BrokerEpoch != old || a.intent.PodPolicySHA256 != prepared.PodPolicySHA256 {
		t.Fatal("attestation lost immutable creation intent", a.intent)
	}
	a.proven = true
	if e = r.Step(t.Context()); e != nil || record(t, restarted, epoch, host.ID).State != javarunner.HostReaped || creates.Load() != 1 {
		t.Fatal("positive no-start proof failed or create replayed", e, creates.Load())
	}
}
func TestKubernetesDeleteUIDAndIndependentAttestation(t *testing.T) {
	a := &testAttestor{}
	var deletes, gets atomic.Int32
	k := kubeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deletes.Add(1)
			var options struct {
				Preconditions      struct{ UID string }
				GracePeriodSeconds int64
			}
			if json.NewDecoder(r.Body).Decode(&options) != nil || options.Preconditions.UID != "exact-uid" || options.GracePeriodSeconds != 0 {
				t.Error("missing exact deletion fence")
			}
		case http.MethodGet:
			gets.Add(1)
		default:
			t.Error("unexpected method", r.Method)
		}
		w.WriteHeader(404)
	}, a)
	pod := PodReceipt{Namespace: testConfig.Namespace, Name: "pod", UID: "exact-uid", ContainerID: "containerd://exact", HostID: "host", BrokerEpoch: "epoch"}
	if err := k.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 1 || gets.Load() != 1 {
		t.Fatal("deletion did not confirm object absence", deletes.Load(), gets.Load())
	}
	if ok, err := k.BoundaryTerminated(t.Context(), pod); err != nil || ok || a.calls.Load() != 1 {
		t.Fatal("NotFound incorrectly proved termination", ok, err)
	}
	pod.Namespace = "other"
	if err := k.Delete(t.Context(), pod); err == nil {
		t.Fatal("cross-namespace deletion admitted")
	}
}

func TestKubernetesDeleteWaitsForAuthoritativePodAbsence(t *testing.T) {
	for _, deleteStatus := range []int{http.StatusOK, http.StatusAccepted, http.StatusNotFound} {
		t.Run(http.StatusText(deleteStatus), func(t *testing.T) {
			var present atomic.Bool
			present.Store(true)
			var deletes, gets atomic.Int32
			k := kubeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodDelete:
					deletes.Add(1)
					var options struct{ Preconditions struct{ UID string } }
					if e := json.NewDecoder(r.Body).Decode(&options); e != nil || options.Preconditions.UID != "exact-uid" {
						t.Error("missing exact UID precondition", e, options.Preconditions.UID)
					}
					w.WriteHeader(deleteStatus)
				case http.MethodGet:
					gets.Add(1)
					if present.Load() {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{"metadata":{"uid":"exact-uid"}}`))
					} else {
						w.WriteHeader(http.StatusNotFound)
					}
				default:
					t.Error("unexpected request", r.Method)
				}
			}, &testAttestor{})
			pod := PodReceipt{Namespace: testConfig.Namespace, Name: "pod", UID: "exact-uid"}
			if e := k.Delete(t.Context(), pod); !errors.Is(e, ErrCleanup) || deletes.Load() != 1 || gets.Load() != 1 {
				t.Fatal("delete acknowledgment released present pod", e, deletes.Load(), gets.Load())
			}
			present.Store(false)
			if e := k.Delete(t.Context(), pod); e != nil || deletes.Load() != 2 || gets.Load() != 2 {
				t.Fatal("absent pod did not complete cleanup", e, deletes.Load(), gets.Load())
			}
		})
	}
}

func TestKubernetesDeleteRetainsCleanupOnFailedAbsenceRead(t *testing.T) {
	k := kubeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusAccepted)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}, &testAttestor{})
	if e := k.Delete(t.Context(), PodReceipt{Namespace: testConfig.Namespace, Name: "pod", UID: "exact-uid"}); !errors.Is(e, ErrCleanup) {
		t.Fatal("failed verification released pod", e)
	}
}
func TestKubernetesRejectsRedirectAndUntrustedEndpoint(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer destination.Close()
	k := kubeFixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }, &testAttestor{})
	if _, err := k.Find(t.Context(), testConfig.Namespace, "pod"); err == nil || leaked.Load() {
		t.Fatal("credential redirect followed")
	}
	for _, endpoint := range []string{"http://localhost", "https://user:secret@example.com", "https://example.com/path", "https://example.com?token=x"} {
		if _, err := NewKubernetesBackend(endpoint, "java-test", &tls.Config{}, func(context.Context) (string, error) { return "token", nil }, &testAttestor{}); err == nil {
			t.Fatal("invalid endpoint accepted", endpoint)
		}
	}
	if _, err := NewKubernetesBackend("https://example.com", "java-test", &tls.Config{InsecureSkipVerify: true}, func(context.Context) (string, error) { return "token", nil }, &testAttestor{}); err == nil {
		t.Fatal("unverified TLS accepted")
	}
}

func TestNodeSelectorValidation(t *testing.T) {
	for _, test := range []struct {
		key, value string
		valid      bool
	}{
		{"kubernetes.io/os", "linux", true}, {"node-role.kubernetes.io/workload", "", true}, {"zone", "west_1", true}, {"a/b/c", "v", false}, {"a..b/key", "v", false}, {"/key", "v", false}, {"key", "bad/value", false}, {"Key/part", "v", false}, {"key", strings.Repeat("a", 64), false},
	} {
		if got := validNodeSelector(test.key, test.value); got != test.valid {
			t.Fatal(test, got)
		}
	}
}

func TestExecutionPodSessionContextIsRequiredAndPolicyBound(t *testing.T) {
	s, epoch, _ := newStore(t)
	rs, e := s.ReserveDedicatedWave(t.Context(), epoch, binding(), []string{"session-1"}, 100, time.Second, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	r := rs[0]
	cfg := podConfig()
	if _, e := ExecutionPod(testConfig.Namespace, r, cfg); e == nil {
		t.Fatal("session prepared without per-vuser context")
	}
	cfg.Context = &javarunner.DedicatedHostContext{Contract: javarunner.DedicatedHostContextContract, SessionID: r.SessionID, VUserID: 1, GroupName: "group", ScenarioID: "scenario"}
	first, e := ExecutionPod(testConfig.Namespace, r, cfg)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Context.VUserID = 2
	second, e := ExecutionPod(testConfig.Namespace, r, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if podPolicyDigest(first) == podPolicyDigest(second) {
		t.Fatal("context is not bound to pod policy")
	}
	cfg.Context.SessionID = "other"
	if _, e = ExecutionPod(testConfig.Namespace, r, cfg); e == nil {
		t.Fatal("wrong session accepted")
	}
}
