package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func policyPod(t *testing.T) ([]byte, map[string]any) {
	t.Helper()
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	cfg := podConfig()
	cfg.CPUmilli = 1000
	data, e := ExecutionPod(testConfig.Namespace, r, cfg)
	if e != nil {
		t.Fatal(e)
	}
	var pod map[string]any
	if e = json.Unmarshal(data, &pod); e != nil {
		t.Fatal(e)
	}
	return data, pod
}
func encodedPod(t *testing.T, pod map[string]any) []byte {
	t.Helper()
	data, e := json.Marshal(pod)
	if e != nil {
		t.Fatal(e)
	}
	return data
}
func TestAdmittedPodAllowsOnlyKnownDefaultsAndExactResourceAmounts(t *testing.T) {
	expected, pod := policyPod(t)
	spec := pod["spec"].(map[string]any)
	container := spec["containers"].([]any)[0].(map[string]any)
	for k, v := range map[string]any{"dnsPolicy": "ClusterFirst", "schedulerName": "default-scheduler", "priority": 0, "preemptionPolicy": "PreemptLowerPriority", "serviceAccount": spec["serviceAccountName"]} {
		spec[k] = v
	}
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC", "shareProcessNamespace"} {
		delete(spec, key)
	}
	container["terminationMessagePath"] = "/dev/termination-log"
	container["terminationMessagePolicy"] = "File"
	container["stdin"] = false
	spec["imagePullSecrets"] = []any{map[string]any{"name": "registry-credential"}}
	spec["tolerations"] = []any{map[string]any{"key": "node.kubernetes.io/not-ready", "operator": "Exists", "effect": "NoExecute", "tolerationSeconds": 300}}
	for _, kind := range []string{"requests", "limits"} {
		resource := container["resources"].(map[string]any)[kind].(map[string]any)
		resource["cpu"] = "1"
		resource["ephemeral-storage"] = "1Mi"
	}
	spec["volumes"].([]any)[0].(map[string]any)["emptyDir"].(map[string]any)["sizeLimit"] = "1Mi"
	if e := verifyAdmittedPod(expected, encodedPod(t, pod), false); e != nil {
		t.Fatal(e)
	}
	spec["nodeName"] = "node-1"
	if e := verifyAdmittedPod(expected, encodedPod(t, pod), false); e == nil {
		t.Fatal("preassigned node accepted at create")
	}
	if e := verifyAdmittedPod(expected, encodedPod(t, pod), true); e != nil {
		t.Fatal(e)
	}
}
func TestAdmittedPodRejectsSecurityAndIdentityMutations(t *testing.T) {
	for _, field := range []string{"hostPID", "token", "image", "command", "resources", "capabilities", "environment", "volume", "scratch", "sidecar", "init", "ephemeral", "placement", "label", "annotation", "owner", "finalizer", "deleting", "unknown"} {
		t.Run(field, func(t *testing.T) {
			expected, pod := policyPod(t)
			spec := pod["spec"].(map[string]any)
			c := spec["containers"].([]any)[0].(map[string]any)
			metadata := pod["metadata"].(map[string]any)
			switch field {
			case "hostPID":
				spec["hostPID"] = true
			case "token":
				spec["automountServiceAccountToken"] = true
			case "image":
				c["image"] = "registry/other@sha256:" + strings.Repeat("b", 64)
			case "command":
				c["command"] = []string{"/bin/sh"}
			case "resources":
				c["resources"].(map[string]any)["limits"].(map[string]any)["memory"] = "99"
			case "capabilities":
				c["securityContext"].(map[string]any)["privileged"] = true
			case "environment":
				c["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "generator"}}}
			case "volume":
				spec["volumes"] = append(spec["volumes"].([]any), map[string]any{"name": "host", "hostPath": map[string]any{"path": "/"}})
			case "scratch":
				spec["volumes"].([]any)[0].(map[string]any)["emptyDir"].(map[string]any)["sizeLimit"] = "2Mi"
			case "sidecar":
				spec["containers"] = append(spec["containers"].([]any), map[string]any{"name": "injected"})
			case "init":
				spec["initContainers"] = []any{map[string]any{"name": "injected"}}
			case "ephemeral":
				spec["ephemeralContainers"] = []any{map[string]any{"name": "injected"}}
			case "placement":
				spec["nodeSelector"].(map[string]any)["zone"] = "other"
			case "label":
				metadata["labels"].(map[string]any)["egress"] = "unrestricted"
			case "annotation":
				metadata["annotations"].(map[string]any)["cloud-identity"] = "injected"
			case "owner":
				metadata["ownerReferences"] = []any{map[string]any{"kind": "ReplicaSet"}}
			case "finalizer":
				metadata["finalizers"] = []any{"retain-forever"}
			case "deleting":
				metadata["deletionTimestamp"] = "2026-01-01T00:00:00Z"
			case "unknown":
				spec["futureUnsafeField"] = true
			}
			if e := verifyAdmittedPod(expected, encodedPod(t, pod), true); e == nil {
				t.Fatal("admission mutation accepted")
			}
		})
	}
}
func TestPodQuantityComparisonIsExact(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{{"1000m", "1", true}, {"1048576", "1Mi", true}, {"1000000", "1M", true}, {"1000", "1k", true}, {"1m", "1", false}, {"9007199254740992", "9007199254740993", false}, {"1Mi", "1M", false}} {
		a, ok := canonicalPodQuantity(tc.a)
		if !ok {
			t.Fatal(tc)
		}
		b, ok := canonicalPodQuantity(tc.b)
		if !ok || (a == b) != tc.same {
			t.Fatal(tc, a, b)
		}
	}
	for _, bad := range []string{"", "-1", "NaN", "0.5", "9999999999999999999999", "1mi", "1Mi extra"} {
		if _, ok := canonicalPodQuantity(bad); ok {
			t.Fatal(bad)
		}
	}
}
func TestRejectedAdmissionPersistsUIDAndQuarantine(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		var pod map[string]any
		if e := json.NewDecoder(req.Body).Decode(&pod); e != nil {
			t.Fatal(e)
		}
		pod["metadata"].(map[string]any)["uid"] = "rejected-uid"
		pod["spec"].(map[string]any)["hostPID"] = true
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(pod)
	}, &testAttestor{})
	receipt, e := k.Provision(t.Context(), s, epoch, r.ID, podConfig())
	if !errors.Is(e, ErrCleanup) || receipt.UID != "rejected-uid" {
		t.Fatal(receipt, e)
	}
	got := record(t, s, epoch, r.ID)
	if got.State != javarunner.HostQuarantined || got.PodUID != "rejected-uid" || got.ContainerID != "" || got.PodPolicySHA256 == "" {
		t.Fatal(got)
	}
}
func TestPreparedPodObservationRevalidatesPolicyBeforeBindingContainer(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "changed"}[mutate], func(t *testing.T) {
			s, epoch, _ := newStore(t)
			r := reserve(t, s, epoch)
			var body map[string]any
			k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
				if req.Method == "POST" {
					_ = json.NewDecoder(req.Body).Decode(&body)
					body["metadata"].(map[string]any)["uid"] = "uid"
					w.WriteHeader(201)
				} else {
					body["spec"].(map[string]any)["nodeName"] = "node"
					body["status"] = map[string]any{"containerStatuses": []any{map[string]any{"name": "java-host", "containerID": "containerd://id"}}}
					if mutate {
						body["spec"].(map[string]any)["hostNetwork"] = true
					}
				}
				_ = json.NewEncoder(w).Encode(body)
			}, &testAttestor{})
			if _, e := k.Provision(t.Context(), s, epoch, r.ID, podConfig()); e != nil {
				t.Fatal(e)
			}
			pod, e := k.ObservePrepared(t.Context(), s, epoch, r.ID, podConfig())
			got := record(t, s, epoch, r.ID)
			if mutate {
				if !errors.Is(e, ErrCleanup) || got.State != javarunner.HostQuarantined || got.ContainerID != "" {
					t.Fatal(got, e)
				}
			} else if e != nil || pod.ContainerID != "containerd://id" || got.State != javarunner.HostPreparing {
				t.Fatal(got, pod, e)
			}
		})
	}
}

func TestLatePreparedReceiptSurvivesCallerCancellation(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	if e := s.BeginPreparePod(t.Context(), epoch, r.ID, strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e := bindPreparedReceipt(ctx, s, epoch, r.ID, PodReceipt{UID: "late-uid", ContainerID: "containerd://late"})
	got := record(t, s, epoch, r.ID)
	if !errors.Is(e, context.Canceled) || got.PodUID != "late-uid" || got.ContainerID != "containerd://late" || got.State != javarunner.HostQuarantined {
		t.Fatal(got, e)
	}
}
func TestPreparationPolicyIdentityIsImmutable(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	for _, bad := range []string{"", strings.Repeat("x", 64), strings.Repeat("A", 64)} {
		if e := s.BeginPreparePod(t.Context(), epoch, r.ID, bad); !errors.Is(e, ErrState) {
			t.Fatal(e)
		}
	}
	if got := record(t, s, epoch, r.ID); got.State != javarunner.HostReserved {
		t.Fatal("invalid identity changed reservation", got)
	}
	digest := strings.Repeat("a", 64)
	if e := s.BeginPreparePod(t.Context(), epoch, r.ID, digest); e != nil {
		t.Fatal(e)
	}
	if e := s.BeginPreparePod(t.Context(), epoch, r.ID, strings.Repeat("b", 64)); !errors.Is(e, ErrState) {
		t.Fatal("policy changed after admission", e)
	}
	if got := record(t, s, epoch, r.ID); got.PodPolicySHA256 != digest {
		t.Fatal(got)
	}
}
