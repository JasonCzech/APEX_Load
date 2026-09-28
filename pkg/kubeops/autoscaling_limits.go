package kubeops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// StatefulSetAutoscalingLimit is an observation of a KEDA/HPA upper bound, not
// proof that nodes, quotas, or compatible spare pods can supply that capacity.
// Callers must revalidate placement and reserve capacity before making a promise.
type StatefulSetAutoscalingLimit struct {
	Namespace, Workload                  string
	ScaledObjectUID, ScaledObjectVersion string
	HPAUID, HPAVersion                   string
	MaximumReplicas                      int
	ObservedAt                           time.Time
}

type autoscalingObject struct {
	Metadata struct {
		Name, Namespace, UID, ResourceVersion string
		DeletionTimestamp                     *string
		Annotations                           map[string]string
		OwnerReferences                       []struct {
			APIVersion, Kind, Name, UID string
			Controller                  bool
		}
	}
	Spec struct {
		ScaleTargetRef                                             struct{ APIVersion, Kind, Name string }
		MinReplicaCount, MaxReplicaCount, MinReplicas, MaxReplicas *int
	}
}

type autoscalingObjectList struct {
	Metadata struct{ Continue string }
	Items    []autoscalingObject
}

// StatefulSetAutoscalingLimit reads both controllers in one bounded attempt.
// An absent CRD, missing permission, paused object, conflicting controller, or
// truncated list leaves capacity unknown. Neither a missing HPA nor the number
// of discovered pods is interpreted as an unlimited replica allowance.
func (c *Client) StatefulSetAutoscalingLimit(ctx context.Context, namespace, workload string) (StatefulSetAutoscalingLimit, error) {
	var unknown StatefulSetAutoscalingLimit
	if c == nil || strings.TrimSpace(namespace) == "" || strings.TrimSpace(workload) == "" || namespace != strings.TrimSpace(namespace) || workload != strings.TrimSpace(workload) {
		return unknown, fmt.Errorf("namespace and StatefulSet are required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	read := func(path string) ([]autoscalingObject, error) {
		payload, err := c.do(ctx, http.MethodGet, path+"?limit=256", "", nil)
		if err != nil {
			return nil, err
		}
		var list autoscalingObjectList
		if err = json.Unmarshal(payload, &list); err != nil {
			return nil, fmt.Errorf("decode autoscaling limits: %w", err)
		}
		if list.Metadata.Continue != "" || len(list.Items) > 256 {
			return nil, fmt.Errorf("autoscaling inventory exceeds bounded snapshot")
		}
		return list.Items, nil
	}
	objects, err := read("/apis/keda.sh/v1alpha1/namespaces/" + pathEscape(namespace) + "/scaledobjects")
	if err != nil {
		return unknown, err
	}
	hpas, err := read("/apis/autoscaling/v2/namespaces/" + pathEscape(namespace) + "/horizontalpodautoscalers")
	if err != nil {
		return unknown, err
	}
	selectTarget := func(items []autoscalingObject) (*autoscalingObject, error) {
		var match *autoscalingObject
		for i := range items {
			item := &items[i]
			ref := item.Spec.ScaleTargetRef
			if ref.Name != workload || ref.Kind != "StatefulSet" {
				continue
			}
			if ref.APIVersion != "apps/v1" {
				return nil, fmt.Errorf("StatefulSet autoscaler uses an unsupported API version")
			}
			if match != nil {
				return nil, fmt.Errorf("multiple autoscaling controllers target StatefulSet")
			}
			if item.Metadata.Namespace != namespace || item.Metadata.Name == "" || item.Metadata.UID == "" || item.Metadata.ResourceVersion == "" || item.Metadata.DeletionTimestamp != nil {
				return nil, fmt.Errorf("autoscaling controller identity is unavailable or terminating")
			}
			match = item
		}
		if match == nil {
			return nil, fmt.Errorf("no explicit autoscaling controller for StatefulSet")
		}
		return match, nil
	}
	scaled, err := selectTarget(objects)
	if err != nil {
		return unknown, err
	}
	hpa, err := selectTarget(hpas)
	if err != nil {
		return unknown, err
	}
	for key, value := range scaled.Metadata.Annotations {
		if strings.HasPrefix(key, "autoscaling.keda.sh/paused") && (key == "autoscaling.keda.sh/paused-replicas" || value != "false") {
			return unknown, fmt.Errorf("KEDA scaling is paused")
		}
	}
	owners := 0
	for _, owner := range hpa.Metadata.OwnerReferences {
		if !owner.Controller {
			continue
		}
		if owner.APIVersion != "keda.sh/v1alpha1" || owner.Kind != "ScaledObject" || owner.UID != scaled.Metadata.UID || owner.Name != scaled.Metadata.Name {
			return unknown, fmt.Errorf("HPA does not belong to the observed KEDA controller")
		}
		owners++
	}
	if owners != 1 {
		return unknown, fmt.Errorf("HPA controller ownership is ambiguous")
	}
	validBounds := func(low, high *int) bool {
		return high != nil && *high > 0 && (low == nil || (*low >= 0 && *low <= *high))
	}
	if !validBounds(scaled.Spec.MinReplicaCount, scaled.Spec.MaxReplicaCount) || !validBounds(hpa.Spec.MinReplicas, hpa.Spec.MaxReplicas) {
		return unknown, fmt.Errorf("explicit valid autoscaling replica bounds are required")
	}
	// During reconciliation either object can still carry the lower maximum.
	// Never promise capacity based on only the newer/higher half of that pair.
	return StatefulSetAutoscalingLimit{Namespace: namespace, Workload: workload, ScaledObjectUID: scaled.Metadata.UID, ScaledObjectVersion: scaled.Metadata.ResourceVersion, HPAUID: hpa.Metadata.UID, HPAVersion: hpa.Metadata.ResourceVersion, MaximumReplicas: min(*scaled.Spec.MaxReplicaCount, *hpa.Spec.MaxReplicas), ObservedAt: time.Now().UTC()}, nil
}
