package kubeops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newResourceTestClient wires a client to a handler that serves canned API
// responses, so the decoding contract can be pinned without a live cluster.
func newResourceTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewRemoteClient(Config{
		Namespace:    "apex",
		APIServerURL: server.URL,
		CAPEM:        serverCAPEM(t, server),
		TokenSource:  StaticTokenSource{Value: "token"},
	})
	if err != nil {
		t.Fatalf("NewRemoteClient: %v", err)
	}
	return client
}

func TestListNodesDecodesPoolZoneAndAllocatable(t *testing.T) {
	client := newResourceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes" {
			t.Errorf("path = %q, want /api/v1/nodes", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"aks-loadgen-0","labels":{"kubernetes.azure.com/agentpool":"loadgen","agentpool":"loadgen","topology.kubernetes.io/zone":"eastus-2"}},
			 "spec":{"unschedulable":false,"taints":[{"key":"apexload/loadgen","value":"true","effect":"NoSchedule"}]},
			 "status":{"allocatable":{"cpu":"3860m","memory":"12683016Ki"},"capacity":{"cpu":"4","memory":"16393612Ki"}}}
		]}`))
	})

	nodes, err := client.ListNodes(context.Background(), "")
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	node := nodes[0]
	if node.AgentPool() != "loadgen" {
		t.Errorf("AgentPool() = %q, want loadgen", node.AgentPool())
	}
	if node.Zone() != "eastus-2" {
		t.Errorf("Zone() = %q, want eastus-2", node.Zone())
	}
	if len(node.Taints) != 1 || node.Taints[0].Effect != "NoSchedule" {
		t.Errorf("Taints = %+v, want one NoSchedule taint", node.Taints)
	}
	millis, present, err := node.Allocatable.CPUMillis()
	if err != nil || !present || millis != 3860 {
		t.Errorf("allocatable CPU = (%d, %v, %v), want 3860 millicores", millis, present, err)
	}
	bytes, present, err := node.Allocatable.MemoryBytes()
	if err != nil || !present || bytes != 12683016*1024 {
		t.Errorf("allocatable memory = (%d, %v, %v), want 12683016Ki in bytes", bytes, present, err)
	}
}

// TestListNodesForbiddenIsDetectable pins the graceful-degradation contract:
// the controller's shipped Role is namespaced, so a default install gets a 403
// here and callers must be able to recognize it rather than fail the request.
func TestListNodesForbiddenIsDetectable(t *testing.T) {
	client := newResourceTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"nodes is forbidden"}`))
	})

	_, err := client.ListNodes(context.Background(), "")
	if err == nil {
		t.Fatal("ListNodes succeeded, want a 403")
	}
	if !IsForbidden(err) {
		t.Fatalf("IsForbidden(%v) = false, want true", err)
	}
}

func TestListResourceQuotasPrefersStatusHard(t *testing.T) {
	client := newResourceTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"apex-quota"},
			 "spec":{"hard":{"requests.cpu":"99"}},
			 "status":{"hard":{"requests.cpu":"8","requests.memory":"16Gi","pods":"20"},
			           "used":{"requests.cpu":"2","requests.memory":"4Gi","pods":"5"}}}
		]}`))
	})

	quotas, err := client.ListResourceQuotas(context.Background(), "apex")
	if err != nil {
		t.Fatalf("ListResourceQuotas: %v", err)
	}
	if len(quotas) != 1 {
		t.Fatalf("got %d quotas, want 1", len(quotas))
	}
	// status.hard is what the quota controller actually enforces; spec.hard is
	// only a fallback for a quota that has not been reconciled yet.
	if quotas[0].Hard["requests.cpu"] != "8" {
		t.Errorf("Hard[requests.cpu] = %q, want 8 (status.hard, not spec.hard)", quotas[0].Hard["requests.cpu"])
	}
	if quotas[0].Used["pods"] != "5" {
		t.Errorf("Used[pods] = %q, want 5", quotas[0].Used["pods"])
	}
}

func TestGetStatefulSetDecodesContainersAndDefaults(t *testing.T) {
	client := newResourceTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		// No spec.replicas: a StatefulSet without it defaults to 1, and
		// treating that as 0 would make every quota delta calculation wrong.
		_, _ = w.Write([]byte(`{"metadata":{"name":"apexload-generator","namespace":"apex",
			 "annotations":{"app.kubernetes.io/managed-by":"Helm"}},
			 "spec":{"updateStrategy":{"type":"OnDelete"},
			         "template":{"metadata":{"labels":{"app":"generator"}},
			                     "spec":{"nodeSelector":{"agentpool":"loadgen"},
			                             "containers":[
			                               {"name":"generator","resources":{"requests":{"cpu":"1","memory":"2Gi"},"limits":{"cpu":"2","memory":"4Gi"}}},
			                               {"name":"java-vuser-runner","resources":{}}]}}},
			 "status":{"readyReplicas":3}}`))
	})

	set, err := client.GetStatefulSet(context.Background(), "apex", "apexload-generator")
	if err != nil {
		t.Fatalf("GetStatefulSet: %v", err)
	}
	if set.Replicas != 1 {
		t.Errorf("Replicas = %d, want the Kubernetes default of 1", set.Replicas)
	}
	if set.ReadyReplicas != 3 {
		t.Errorf("ReadyReplicas = %d, want 3", set.ReadyReplicas)
	}
	if set.UpdateStrategy != "OnDelete" {
		t.Errorf("UpdateStrategy = %q, want OnDelete", set.UpdateStrategy)
	}
	if len(set.Containers) != 2 || set.Containers[1].Name != "java-vuser-runner" {
		t.Fatalf("Containers = %+v, want the generator and its sidecar", set.Containers)
	}
	if set.Containers[0].Requests["cpu"] != "1" {
		t.Errorf("generator CPU request = %q, want 1", set.Containers[0].Requests["cpu"])
	}
	if set.NodeSelector["agentpool"] != "loadgen" {
		t.Errorf("NodeSelector = %v, want the agentpool key", set.NodeSelector)
	}
	if set.Annotations["app.kubernetes.io/managed-by"] != "Helm" {
		t.Errorf("Annotations = %v, want the Helm management marker preserved", set.Annotations)
	}
}

func TestPatchStatefulSetPodTemplateUsesStrategicMerge(t *testing.T) {
	var gotContentType string
	var gotBody map[string]any
	client := newResourceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	})

	patch := map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"nodeSelector": map[string]any{"agentpool": "loadgen", "retired": nil},
	}}}}
	if err := client.PatchStatefulSetPodTemplate(context.Background(), "apex", "apexload-generator", patch); err != nil {
		t.Fatalf("PatchStatefulSetPodTemplate: %v", err)
	}
	// Strategic merge is what merges into the container list by name; a plain
	// merge patch would replace it and drop sidecars.
	if gotContentType != "application/strategic-merge-patch+json" {
		t.Errorf("Content-Type = %q, want strategic-merge-patch", gotContentType)
	}
	selector := gotBody["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["nodeSelector"].(map[string]any)
	value, present := selector["retired"]
	if !present || value != nil {
		t.Errorf("nodeSelector[retired] = (%v, present=%v), want an explicit null to remove the key", value, present)
	}
}

func TestServerVersionReturnsGitVersion(t *testing.T) {
	client := newResourceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Errorf("path = %q, want /version", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"gitVersion":"v1.29.4"}`))
	})

	version, err := client.ServerVersion(context.Background())
	if err != nil {
		t.Fatalf("ServerVersion: %v", err)
	}
	if version != "v1.29.4" {
		t.Fatalf("ServerVersion = %q, want v1.29.4", version)
	}
}

// TestNodeAgentPoolPrefersProviderLabels pins the label precedence and, just as
// importantly, the KEY each match came from: a nodeSelector built with the
// wrong key matches no node, so the generator pods would stay Pending forever.
func TestNodeAgentPoolPrefersProviderLabels(t *testing.T) {
	for _, tc := range []struct {
		name      string
		labels    map[string]string
		wantPool  string
		wantLabel string
	}{
		{
			name:      "aks carries both and the vendor label wins",
			labels:    map[string]string{LabelAKSAgentPool: "loadgen", LabelAgentPool: "loadgen"},
			wantPool:  "loadgen",
			wantLabel: LabelAKSAgentPool,
		},
		{
			name:      "eks managed node group",
			labels:    map[string]string{LabelEKSNodeGroup: "loadgen-ng"},
			wantPool:  "loadgen-ng",
			wantLabel: LabelEKSNodeGroup,
		},
		{
			name:      "karpenter node pool",
			labels:    map[string]string{LabelKarpenterPool: "loadgen-karpenter"},
			wantPool:  "loadgen-karpenter",
			wantLabel: LabelKarpenterPool,
		},
		{
			name:      "generic label is the last resort",
			labels:    map[string]string{LabelAgentPool: "loadgen"},
			wantPool:  "loadgen",
			wantLabel: LabelAgentPool,
		},
		{
			name:      "aks still outranks eks when a node somehow carries both",
			labels:    map[string]string{LabelAKSAgentPool: "aks-pool", LabelEKSNodeGroup: "eks-pool"},
			wantPool:  "aks-pool",
			wantLabel: LabelAKSAgentPool,
		},
		{
			name:      "an unlabeled node has no pool",
			labels:    map[string]string{"unrelated": "value"},
			wantPool:  "",
			wantLabel: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := NodeRef{Labels: tc.labels}
			if got := node.AgentPool(); got != tc.wantPool {
				t.Errorf("AgentPool() = %q, want %q", got, tc.wantPool)
			}
			if got := node.AgentPoolLabel(); got != tc.wantLabel {
				t.Errorf("AgentPoolLabel() = %q, want %q", got, tc.wantLabel)
			}
		})
	}
}
