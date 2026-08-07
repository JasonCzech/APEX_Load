package kubeops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Well-known node labels used to group nodes into pools and zones.
const (
	LabelZone          = "topology.kubernetes.io/zone"
	LabelAKSAgentPool  = "kubernetes.azure.com/agentpool"
	LabelEKSNodeGroup  = "eks.amazonaws.com/nodegroup"
	LabelKarpenterPool = "karpenter.sh/nodepool"
	LabelAgentPool     = "agentpool"
)

// agentPoolLabels is the precedence order for resolving a node's pool.
//
// AKS stays first because an AKS node carries both its vendor label and the
// generic one, and the vendor value is what an operator sees in the portal —
// so existing behavior is unchanged. EKS managed node groups come next, then
// Karpenter (whose nodes carry neither of the first two), then the generic
// label as a last resort.
var agentPoolLabels = []string{LabelAKSAgentPool, LabelEKSNodeGroup, LabelKarpenterPool, LabelAgentPool}

// Taint mirrors the node taint fields that affect generator scheduling.
type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

// NodeRef is the subset of a Node that capacity planning needs.
type NodeRef struct {
	Name          string            `json:"name"`
	Labels        map[string]string `json:"labels,omitempty"`
	Unschedulable bool              `json:"unschedulable,omitempty"`
	Taints        []Taint           `json:"taints,omitempty"`
	Allocatable   ResourceList      `json:"allocatable,omitempty"`
	Capacity      ResourceList      `json:"capacity,omitempty"`
}

// AgentPool returns the node's pool name.
func (n NodeRef) AgentPool() string {
	_, value := n.agentPool()
	return value
}

// AgentPoolLabel returns the label KEY that supplied AgentPool.
//
// Callers that build a nodeSelector need it, not just the value: pinning a pod
// to an EKS managed node group requires the eks.amazonaws.com/nodegroup key,
// and writing the generic "agentpool" key instead produces a selector that
// matches no node, so the pods stay Pending forever.
func (n NodeRef) AgentPoolLabel() string {
	label, _ := n.agentPool()
	return label
}

func (n NodeRef) agentPool() (label, value string) {
	for _, candidate := range agentPoolLabels {
		if pool := strings.TrimSpace(n.Labels[candidate]); pool != "" {
			return candidate, pool
		}
	}
	return "", ""
}

// Zone returns the node's topology zone, if labeled.
func (n NodeRef) Zone() string { return strings.TrimSpace(n.Labels[LabelZone]) }

// ResourceQuotaRef is a namespace ResourceQuota's hard limits and current use.
type ResourceQuotaRef struct {
	Name string       `json:"name"`
	Hard ResourceList `json:"hard,omitempty"`
	Used ResourceList `json:"used,omitempty"`
}

// ContainerRef is the subset of a pod container that allocation edits.
type ContainerRef struct {
	Name     string       `json:"name"`
	Requests ResourceList `json:"requests,omitempty"`
	Limits   ResourceList `json:"limits,omitempty"`
}

// StatefulSetRef describes a generator workload well enough to decide whether
// it is safe to patch and what the patch must change.
type StatefulSetRef struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Replicas        int               `json:"replicas"`
	ReadyReplicas   int               `json:"ready_replicas"`
	UpdateStrategy  string            `json:"update_strategy,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
	PodTemplateJSON map[string]string `json:"pod_template_labels,omitempty"`
	Containers      []ContainerRef    `json:"containers,omitempty"`
	NodeSelector    map[string]string `json:"node_selector,omitempty"`
}

// IsForbidden reports whether an error is a Kubernetes 403. Node listing is
// cluster-scoped and the controller's shipped Role is namespaced, so callers
// use this to degrade to "capacity unknown" rather than failing the request.
func IsForbidden(err error) bool {
	var apiErr *KubernetesAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusForbidden
	}
	return false
}

// ListNodes reads cluster nodes for pool/zone discovery and pod-fit checks.
// This requires CLUSTER-SCOPED "nodes list" RBAC, which is shipped as an
// opt-in ClusterRole — expect IsForbidden(err) on a default install.
func (c *Client) ListNodes(ctx context.Context, labelSelector string) ([]NodeRef, error) {
	if c == nil {
		return nil, fmt.Errorf("kubernetes client is not initialized")
	}
	path := "/api/v1/nodes"
	if selector := strings.TrimSpace(labelSelector); selector != "" {
		query := url.Values{}
		query.Set("labelSelector", selector)
		path += "?" + query.Encode()
	}
	payload, err := c.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Unschedulable bool    `json:"unschedulable"`
				Taints        []Taint `json:"taints"`
			} `json:"spec"`
			Status struct {
				Allocatable ResourceList `json:"allocatable"`
				Capacity    ResourceList `json:"capacity"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, fmt.Errorf("decode nodes response: %w", err)
	}
	out := make([]NodeRef, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, NodeRef{
			Name:          strings.TrimSpace(item.Metadata.Name),
			Labels:        item.Metadata.Labels,
			Unschedulable: item.Spec.Unschedulable,
			Taints:        item.Spec.Taints,
			Allocatable:   item.Status.Allocatable,
			Capacity:      item.Status.Capacity,
		})
	}
	return out, nil
}

// ListResourceQuotas reads the namespace's quotas. This is namespaced RBAC and
// is required for quota preflight.
func (c *Client) ListResourceQuotas(ctx context.Context, namespace string) ([]ResourceQuotaRef, error) {
	if c == nil {
		return nil, fmt.Errorf("kubernetes client is not initialized")
	}
	path := "/api/v1/namespaces/" + pathEscape(namespace) + "/resourcequotas"
	payload, err := c.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Hard ResourceList `json:"hard"`
				Used ResourceList `json:"used"`
			} `json:"status"`
			Spec struct {
				Hard ResourceList `json:"hard"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, fmt.Errorf("decode resource quotas response: %w", err)
	}
	out := make([]ResourceQuotaRef, 0, len(list.Items))
	for _, item := range list.Items {
		// status.hard is what the quota controller has actually admitted;
		// spec.hard is only a fallback for a quota not yet reconciled.
		hard := item.Status.Hard
		if len(hard) == 0 {
			hard = item.Spec.Hard
		}
		out = append(out, ResourceQuotaRef{
			Name: strings.TrimSpace(item.Metadata.Name),
			Hard: hard,
			Used: item.Status.Used,
		})
	}
	return out, nil
}

// GetStatefulSet reads a generator workload. Every allocation patch depends on
// this call: the container to patch and the currently-set nodeSelector keys
// cannot be determined from the desired state alone.
func (c *Client) GetStatefulSet(ctx context.Context, namespace, name string) (StatefulSetRef, error) {
	if c == nil {
		return StatefulSetRef{}, fmt.Errorf("kubernetes client is not initialized")
	}
	path := "/apis/apps/v1/namespaces/" + pathEscape(namespace) + "/statefulsets/" + pathEscape(name)
	payload, err := c.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return StatefulSetRef{}, err
	}
	var decoded struct {
		Metadata struct {
			Name        string            `json:"name"`
			Namespace   string            `json:"namespace"`
			Annotations map[string]string `json:"annotations"`
			Labels      map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Replicas       *int `json:"replicas"`
			UpdateStrategy struct {
				Type string `json:"type"`
			} `json:"updateStrategy"`
			Template struct {
				Metadata struct {
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
				Spec struct {
					NodeSelector map[string]string `json:"nodeSelector"`
					Containers   []struct {
						Name      string `json:"name"`
						Resources struct {
							Requests ResourceList `json:"requests"`
							Limits   ResourceList `json:"limits"`
						} `json:"resources"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas int `json:"readyReplicas"`
		} `json:"status"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return StatefulSetRef{}, fmt.Errorf("decode statefulset response: %w", err)
	}
	ref := StatefulSetRef{
		Name:            firstNonEmpty(decoded.Metadata.Name, name),
		Namespace:       firstNonEmpty(decoded.Metadata.Namespace, namespace),
		ReadyReplicas:   decoded.Status.ReadyReplicas,
		UpdateStrategy:  strings.TrimSpace(decoded.Spec.UpdateStrategy.Type),
		Annotations:     decoded.Metadata.Annotations,
		PodTemplateJSON: decoded.Spec.Template.Metadata.Labels,
		NodeSelector:    decoded.Spec.Template.Spec.NodeSelector,
	}
	// A StatefulSet with no explicit replicas defaults to 1.
	if decoded.Spec.Replicas != nil {
		ref.Replicas = *decoded.Spec.Replicas
	} else {
		ref.Replicas = 1
	}
	for _, container := range decoded.Spec.Template.Spec.Containers {
		ref.Containers = append(ref.Containers, ContainerRef{
			Name:     strings.TrimSpace(container.Name),
			Requests: container.Resources.Requests,
			Limits:   container.Resources.Limits,
		})
	}
	return ref, nil
}

// PatchStatefulSetPodTemplate applies a strategic-merge patch to a workload.
// Strategic merge is required (rather than the plain merge patch used by
// ScaleStatefulSet) because it merges into the container list by name instead
// of replacing it, which is what leaves sidecars untouched.
func (c *Client) PatchStatefulSetPodTemplate(ctx context.Context, namespace, name string, patch map[string]any) error {
	if c == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	path := "/apis/apps/v1/namespaces/" + pathEscape(namespace) + "/statefulsets/" + pathEscape(name)
	_, err = c.do(ctx, http.MethodPatch, path, "application/strategic-merge-patch+json", body)
	return err
}

// ServerVersion is the cheapest authenticated probe: it proves the endpoint,
// TLS trust anchor, and bearer token all work before any RBAC-specific call.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("kubernetes client is not initialized")
	}
	payload, err := c.do(ctx, http.MethodGet, "/version", "", nil)
	if err != nil {
		return "", err
	}
	var decoded struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", fmt.Errorf("decode version response: %w", err)
	}
	return strings.TrimSpace(decoded.GitVersion), nil
}
