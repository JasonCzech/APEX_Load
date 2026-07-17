package kubeops

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func TestActiveEndpointPublisherPublishesLeaderWithResourceVersionFence(t *testing.T) {
	var calls atomic.Int32
	client, cleanup := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r, "token-a")
		switch calls.Add(1) {
		case 1:
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			_ = json.NewEncoder(w).Encode(activeEndpointSlicePayload("17", "", ""))
		case 2:
			if r.Method != http.MethodPatch {
				t.Fatalf("method = %s, want PATCH", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json-patch+json" {
				t.Fatalf("Content-Type = %q", got)
			}
			var operations []jsonPatchOperation
			if err := json.NewDecoder(r.Body).Decode(&operations); err != nil {
				t.Fatalf("decode JSON patch: %v", err)
			}
			if len(operations) != 4 || operations[0].Op != "test" || operations[0].Path != "/metadata/resourceVersion" || operations[0].Value != "17" {
				t.Fatalf("resourceVersion patch = %#v", operations)
			}
			annotations, ok := operations[1].Value.(map[string]any)
			if !ok ||
				annotations[activeEndpointOwnerAnnotation] != "controller-a/1" ||
				annotations[activeEndpointGenerationAnnotation] != "1" {
				t.Fatalf("lease annotations = %#v", operations[1].Value)
			}
			endpoints, err := json.Marshal(operations[2].Value)
			if err != nil {
				t.Fatal(err)
			}
			var decoded []endpointSliceEndpoint
			if err := json.Unmarshal(endpoints, &decoded); err != nil {
				t.Fatalf("decode endpoint replacement: %v", err)
			}
			if len(decoded) != 1 || len(decoded[0].Addresses) != 1 || decoded[0].Addresses[0] != "10.0.0.12" ||
				decoded[0].TargetRef == nil || decoded[0].TargetRef.Name != "controller-1" {
				t.Fatalf("published endpoints = %#v", decoded)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request %d", calls.Load())
		}
	}))
	defer cleanup()

	publisher, err := NewActiveEndpointPublisher(client, ActiveEndpointConfig{
		Namespace: "apex", EndpointSliceName: "apexload-controller-active", ServiceName: "apexload-controller-active",
		PodName: "controller-1", PodIP: "10.0.0.12", PortName: "http", Port: 8080,
	})
	if err != nil {
		t.Fatalf("NewActiveEndpointPublisher: %v", err)
	}
	if err := publisher.Publish(context.Background(), "controller-a/1", 1); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestActiveEndpointPublisherDoesNotClearSuccessor(t *testing.T) {
	var calls atomic.Int32
	client, cleanup := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r, "token-a")
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET only", r.Method)
		}
		_ = json.NewEncoder(w).Encode(activeEndpointSlicePayloadWithTerm("21", "controller-2", "10.0.0.13", "controller-b/1", 2))
	}))
	defer cleanup()

	publisher, err := NewActiveEndpointPublisher(client, ActiveEndpointConfig{
		Namespace: "apex", EndpointSliceName: "apexload-controller-active", ServiceName: "apexload-controller-active",
		PodName: "controller-1", PodIP: "10.0.0.12", PortName: "http", Port: 8080,
	})
	if err != nil {
		t.Fatalf("NewActiveEndpointPublisher: %v", err)
	}
	if err := publisher.Clear(context.Background(), "controller-a/1", 1); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want one GET and no PATCH", calls.Load())
	}
}

func TestActiveEndpointPublisherClearConflictRereadsBeforeRemoving(t *testing.T) {
	var calls atomic.Int32
	client, cleanup := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r, "token-a")
		switch calls.Add(1) {
		case 1:
			_ = json.NewEncoder(w).Encode(activeEndpointSlicePayloadWithTerm("30", "controller-1", "10.0.0.12", "controller-a/1", 1))
		case 2:
			if r.Method != http.MethodPatch {
				t.Fatalf("method = %s, want PATCH", r.Method)
			}
			http.Error(w, "resourceVersion changed", http.StatusConflict)
		case 3:
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			_ = json.NewEncoder(w).Encode(activeEndpointSlicePayloadWithTerm("31", "controller-2", "10.0.0.13", "controller-b/1", 2))
		default:
			t.Fatalf("unexpected request %d", calls.Load())
		}
	}))
	defer cleanup()

	publisher, err := NewActiveEndpointPublisher(client, ActiveEndpointConfig{
		Namespace: "apex", EndpointSliceName: "apexload-controller-active", ServiceName: "apexload-controller-active",
		PodName: "controller-1", PodIP: "10.0.0.12", PortName: "http", Port: 8080,
	})
	if err != nil {
		t.Fatalf("NewActiveEndpointPublisher: %v", err)
	}
	if err := publisher.Clear(context.Background(), "controller-a/1", 1); err != nil {
		t.Fatalf("Clear after successor publish: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want GET/PATCH-conflict/GET", calls.Load())
	}
}

func TestActiveEndpointPublisherStalePublishCannotOverwriteSuccessor(t *testing.T) {
	var (
		mu              sync.Mutex
		resourceVersion = 1
		ownerID         string
		generation      uint64
		podName         string
		podIP           string
	)
	oldPatchStarted := make(chan struct{})
	successorPatched := make(chan struct{})
	client, cleanup := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r, "token-a")
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			payload := activeEndpointSlicePayloadWithTerm(
				strconv.Itoa(resourceVersion),
				podName,
				podIP,
				ownerID,
				generation,
			)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(payload)
		case http.MethodPatch:
			var operations []jsonPatchOperation
			if err := json.NewDecoder(r.Body).Decode(&operations); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			if len(operations) != 4 {
				t.Fatalf("patch operations = %#v", operations)
			}
			annotations, ok := operations[1].Value.(map[string]any)
			if !ok {
				t.Fatalf("annotations = %#v", operations[1].Value)
			}
			requestedOwner, _ := annotations[activeEndpointOwnerAnnotation].(string)
			requestedGeneration, err := strconv.ParseUint(annotations[activeEndpointGenerationAnnotation].(string), 10, 64)
			if err != nil {
				t.Fatalf("parse requested generation: %v", err)
			}
			if requestedGeneration == 1 {
				select {
				case <-oldPatchStarted:
				default:
					close(oldPatchStarted)
				}
				<-successorPatched
			}

			mu.Lock()
			defer mu.Unlock()
			expectedResourceVersion, _ := operations[0].Value.(string)
			if expectedResourceVersion != strconv.Itoa(resourceVersion) {
				http.Error(w, "resourceVersion changed", http.StatusConflict)
				return
			}
			endpointsJSON, err := json.Marshal(operations[2].Value)
			if err != nil {
				t.Fatal(err)
			}
			var endpoints []endpointSliceEndpoint
			if err := json.Unmarshal(endpointsJSON, &endpoints); err != nil {
				t.Fatalf("decode endpoints: %v", err)
			}
			ownerID = requestedOwner
			generation = requestedGeneration
			if len(endpoints) == 1 {
				podName = endpoints[0].TargetRef.Name
				podIP = endpoints[0].Addresses[0]
			} else {
				podName, podIP = "", ""
			}
			resourceVersion++
			w.WriteHeader(http.StatusOK)
			if requestedGeneration == 2 {
				close(successorPatched)
			}
		default:
			t.Fatalf("method = %s", r.Method)
		}
	}))
	defer cleanup()

	oldPublisher, err := NewActiveEndpointPublisher(client, ActiveEndpointConfig{
		Namespace: "apex", EndpointSliceName: "apexload-controller-active", ServiceName: "apexload-controller-active",
		PodName: "controller-old", PodIP: "10.0.0.12", PortName: "http", Port: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	successorPublisher, err := NewActiveEndpointPublisher(client, ActiveEndpointConfig{
		Namespace: "apex", EndpointSliceName: "apexload-controller-active", ServiceName: "apexload-controller-active",
		PodName: "controller-new", PodIP: "10.0.0.13", PortName: "http", Port: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}

	oldDone := make(chan error, 1)
	go func() {
		oldDone <- oldPublisher.Publish(context.Background(), "controller-old/1", 1)
	}()
	<-oldPatchStarted
	if err := successorPublisher.Publish(context.Background(), "controller-new/1", 2); err != nil {
		t.Fatalf("successor Publish: %v", err)
	}
	if err := <-oldDone; err != nil {
		t.Fatalf("stale Publish: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if ownerID != "controller-new/1" || generation != 2 || podName != "controller-new" || podIP != "10.0.0.13" {
		t.Fatalf("final active endpoint = owner %q generation %d pod %q ip %q", ownerID, generation, podName, podIP)
	}
}

func activeEndpointSlicePayload(resourceVersion, podName, podIP string) map[string]any {
	return activeEndpointSlicePayloadWithTerm(resourceVersion, podName, podIP, "", 0)
}

func activeEndpointSlicePayloadWithTerm(resourceVersion, podName, podIP, ownerID string, generation uint64) map[string]any {
	endpoints := []any{}
	if podName != "" {
		endpoints = append(endpoints, map[string]any{
			"addresses": []string{podIP},
			"conditions": map[string]bool{
				"ready": true, "serving": true, "terminating": false,
			},
			"targetRef": map[string]string{
				"kind": "Pod", "namespace": "apex", "name": podName,
			},
		})
	}
	return map[string]any{
		"apiVersion":  "discovery.k8s.io/v1",
		"kind":        "EndpointSlice",
		"addressType": "IPv4",
		"metadata": map[string]any{
			"name":            "apexload-controller-active",
			"namespace":       "apex",
			"resourceVersion": resourceVersion,
			"labels": map[string]string{
				"kubernetes.io/service-name":             "apexload-controller-active",
				"endpointslice.kubernetes.io/managed-by": activeEndpointManagedBy,
			},
			"annotations": map[string]string{
				activeEndpointOwnerAnnotation:      ownerID,
				activeEndpointGenerationAnnotation: strconv.FormatUint(generation, 10),
			},
		},
		"ports": []map[string]any{
			{"name": "http", "protocol": "TCP", "port": 8080},
		},
		"endpoints": endpoints,
	}
}
