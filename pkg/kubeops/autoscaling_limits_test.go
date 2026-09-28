package kubeops

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func autoscalingLimitFixtures() (autoscalingObjectList, autoscalingObjectList) {
	var scaled, hpa autoscalingObjectList
	_ = json.Unmarshal([]byte(`{"items":[{"metadata":{"name":"scale","namespace":"apex","uid":"scale-uid","resourceVersion":"10"},"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"StatefulSet","name":"gen"},"minReplicaCount":2,"maxReplicaCount":4}}]}`), &scaled)
	_ = json.Unmarshal([]byte(`{"items":[{"metadata":{"name":"hpa","namespace":"apex","uid":"hpa-uid","resourceVersion":"11","ownerReferences":[{"apiVersion":"keda.sh/v1alpha1","kind":"ScaledObject","name":"scale","uid":"scale-uid","controller":true}]},"spec":{"scaleTargetRef":{"apiVersion":"apps/v1","kind":"StatefulSet","name":"gen"},"minReplicas":2,"maxReplicas":4}}]}`), &hpa)
	return scaled, hpa
}

func TestStatefulSetAutoscalingLimit(t *testing.T) {
	for _, mode := range []string{"valid", "higher KEDA", "higher HPA", "missing KEDA", "missing HPA", "duplicate KEDA", "duplicate HPA", "foreign namespace", "foreign owner", "missing owner", "duplicate owner", "terminating", "paused", "paused zero", "pause false", "unset maximum", "negative maximum", "invalid minimum", "zero minimum", "truncated KEDA", "truncated HPA", "too many", "missing uid", "missing version", "other workload", "ambiguous API version", "forbidden", "invalid json"} {
		t.Run(mode, func(t *testing.T) {
			so, hpa := autoscalingLimitFixtures()
			good := mode == "valid" || mode == "higher KEDA" || mode == "higher HPA" || mode == "pause false" || mode == "zero minimum"
			intp := func(n int) *int { return &n }
			switch mode {
			case "higher KEDA":
				so.Items[0].Spec.MaxReplicaCount = intp(8)
			case "higher HPA":
				hpa.Items[0].Spec.MaxReplicas = intp(8)
			case "missing KEDA":
				so.Items = nil
			case "missing HPA":
				hpa.Items = nil
			case "duplicate KEDA":
				so.Items = append(so.Items, so.Items[0])
			case "duplicate HPA":
				hpa.Items = append(hpa.Items, hpa.Items[0])
			case "foreign namespace":
				so.Items[0].Metadata.Namespace = "other"
			case "foreign owner":
				hpa.Items[0].Metadata.OwnerReferences[0].UID = "replaced"
			case "missing owner":
				hpa.Items[0].Metadata.OwnerReferences = nil
			case "duplicate owner":
				hpa.Items[0].Metadata.OwnerReferences = append(hpa.Items[0].Metadata.OwnerReferences, hpa.Items[0].Metadata.OwnerReferences[0])
			case "terminating":
				s := "now"
				hpa.Items[0].Metadata.DeletionTimestamp = &s
			case "paused":
				so.Items[0].Metadata.Annotations = map[string]string{"autoscaling.keda.sh/paused": "true"}
			case "paused zero":
				so.Items[0].Metadata.Annotations = map[string]string{"autoscaling.keda.sh/paused-replicas": "0"}
			case "pause false":
				so.Items[0].Metadata.Annotations = map[string]string{"autoscaling.keda.sh/paused": "false"}
			case "unset maximum":
				so.Items[0].Spec.MaxReplicaCount = nil
			case "negative maximum":
				hpa.Items[0].Spec.MaxReplicas = intp(-1)
			case "invalid minimum":
				hpa.Items[0].Spec.MinReplicas = intp(5)
			case "zero minimum":
				so.Items[0].Spec.MinReplicaCount = intp(0)
			case "truncated KEDA":
				so.Metadata.Continue = "more"
			case "truncated HPA":
				hpa.Metadata.Continue = "more"
			case "too many":
				so.Items = make([]autoscalingObject, 257)
			case "missing uid":
				hpa.Items[0].Metadata.UID = ""
			case "missing version":
				so.Items[0].Metadata.ResourceVersion = ""
			case "other workload":
				hpa.Items[0].Spec.ScaleTargetRef.Name = "other"
			case "ambiguous API version":
				hpa.Items = append(hpa.Items, hpa.Items[0])
				hpa.Items[1].Spec.ScaleTargetRef.APIVersion = "apps/v1beta1"
			}
			calls := 0
			client := newResourceTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Query().Get("limit") != "256" || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				if mode == "forbidden" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if mode == "invalid json" {
					_, _ = w.Write([]byte(`{`))
					return
				}
				if r.URL.Path == "/apis/keda.sh/v1alpha1/namespaces/apex/scaledobjects" {
					_ = json.NewEncoder(w).Encode(so)
				} else if r.URL.Path == "/apis/autoscaling/v2/namespaces/apex/horizontalpodautoscalers" {
					_ = json.NewEncoder(w).Encode(hpa)
				} else {
					t.Errorf("unexpected path %s", r.URL)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			before := time.Now()
			got, err := client.StatefulSetAutoscalingLimit(context.Background(), "apex", "gen")
			if good {
				if err != nil || got.MaximumReplicas != 4 || got.ScaledObjectUID != "scale-uid" || got.HPAUID != "hpa-uid" || got.ObservedAt.Before(before) || calls != 2 {
					t.Fatalf("limit=%+v err=%v calls=%d", got, err, calls)
				}
			} else if err == nil || got.MaximumReplicas != 0 {
				t.Fatalf("unproven capacity was accepted: %+v %v", got, err)
			}
		})
	}
}

func TestStatefulSetAutoscalingLimitCancellationAndInput(t *testing.T) {
	client := newResourceTestClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := client.StatefulSetAutoscalingLimit(ctx, "apex", "gen"); err == nil || time.Since(start) > time.Second {
		t.Fatalf("request ignored deadline: %v", err)
	}
	for _, pair := range [][2]string{{"", "gen"}, {"apex", ""}, {" apex", "gen"}} {
		if _, err := client.StatefulSetAutoscalingLimit(context.Background(), pair[0], pair[1]); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	var empty *Client
	if _, err := empty.StatefulSetAutoscalingLimit(context.Background(), "apex", "gen"); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatal(err)
	}
}
