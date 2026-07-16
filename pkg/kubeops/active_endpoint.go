package kubeops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

const (
	activeEndpointPublishAttempts      = 3
	activeEndpointOwnerAnnotation      = "apexload.io/execution-lease-owner"
	activeEndpointGenerationAnnotation = "apexload.io/execution-lease-generation"
	activeEndpointManagedBy            = "apexload-controller"
)

// ActiveEndpointConfig identifies the one pre-created EndpointSlice used by the
// controller's leader-only Service. The publisher never creates or deletes
// Kubernetes resources; scoped RBAC grants get/update only for this exact slice.
type ActiveEndpointConfig struct {
	Namespace         string
	EndpointSliceName string
	ServiceName       string
	PodName           string
	PodIP             string
	PortName          string
	Port              int32
}

// ActiveEndpointPublisher atomically publishes or clears this controller pod in
// a selectorless Service's EndpointSlice.
type ActiveEndpointPublisher struct {
	client      *Client
	cfg         ActiveEndpointConfig
	addressType string
}

func NewActiveEndpointPublisher(client *Client, cfg ActiveEndpointConfig) (*ActiveEndpointPublisher, error) {
	if client == nil {
		return nil, fmt.Errorf("Kubernetes client is required")
	}
	cfg.Namespace = strings.TrimSpace(cfg.Namespace)
	cfg.EndpointSliceName = strings.TrimSpace(cfg.EndpointSliceName)
	cfg.ServiceName = strings.TrimSpace(cfg.ServiceName)
	cfg.PodName = strings.TrimSpace(cfg.PodName)
	cfg.PodIP = strings.TrimSpace(cfg.PodIP)
	cfg.PortName = strings.TrimSpace(cfg.PortName)
	if cfg.Namespace == "" || cfg.EndpointSliceName == "" || cfg.ServiceName == "" || cfg.PodName == "" || cfg.PodIP == "" {
		return nil, fmt.Errorf("namespace, EndpointSlice name, Service name, pod name, and pod IP are required")
	}
	if cfg.PortName == "" {
		cfg.PortName = "http"
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("endpoint port must be between 1 and 65535")
	}
	ip := net.ParseIP(cfg.PodIP)
	if ip == nil {
		return nil, fmt.Errorf("pod IP is invalid")
	}
	addressType := "IPv6"
	if ip.To4() != nil {
		addressType = "IPv4"
		cfg.PodIP = ip.To4().String()
	} else {
		cfg.PodIP = ip.String()
	}
	return &ActiveEndpointPublisher{client: client, cfg: cfg, addressType: addressType}, nil
}

// Publish makes this admitted execution leader the slice's sole ready endpoint.
// The durable database lease generation is persisted on the EndpointSlice and
// compared before every patch. Together with the resourceVersion JSON-patch
// test, this prevents an old leader from overwriting a successor even when its
// Kubernetes request completes after leadership has changed.
func (p *ActiveEndpointPublisher) Publish(ctx context.Context, ownerID string, generation uint64) error {
	return p.reconcile(ctx, ownerID, generation, true)
}

// Clear removes the endpoint only when the slice still identifies this exact
// durable lease term. If a successor already published, Clear is deliberately
// a no-op.
func (p *ActiveEndpointPublisher) Clear(ctx context.Context, ownerID string, generation uint64) error {
	return p.reconcile(ctx, ownerID, generation, false)
}

func (p *ActiveEndpointPublisher) reconcile(ctx context.Context, ownerID string, generation uint64, publish bool) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("active endpoint publisher is not initialized")
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" || generation == 0 {
		return fmt.Errorf("active endpoint lease owner and generation are required")
	}
	var lastErr error
	for attempt := 0; attempt < activeEndpointPublishAttempts; attempt++ {
		slice, err := p.get(ctx)
		if err != nil {
			return err
		}
		if err := p.validateSlice(slice); err != nil {
			return err
		}
		currentOwner, currentGeneration, err := activeEndpointTerm(slice)
		if err != nil {
			return err
		}
		if currentGeneration > generation {
			// A newer durable lease term is already published. Both an old
			// Publish and an old Clear must become no-ops.
			return nil
		}
		if currentGeneration == generation && currentOwner != "" && currentOwner != ownerID {
			return fmt.Errorf("active controller EndpointSlice generation %d is owned by a different lease", generation)
		}
		if publish {
			if currentGeneration == generation && currentOwner == ownerID && p.sliceTargetsThisPod(slice) {
				return nil
			}
			slice.Endpoints = []endpointSliceEndpoint{p.ownEndpoint()}
			slice.Ports = []endpointSlicePort{p.ownPort()}
		} else {
			if currentGeneration != generation || currentOwner != ownerID || len(slice.Endpoints) == 0 {
				return nil
			}
			slice.Endpoints = []endpointSliceEndpoint{}
		}
		if slice.Metadata.Annotations == nil {
			slice.Metadata.Annotations = make(map[string]string)
		}
		slice.Metadata.Annotations[activeEndpointOwnerAnnotation] = ownerID
		slice.Metadata.Annotations[activeEndpointGenerationAnnotation] = strconv.FormatUint(generation, 10)
		if err := p.patch(ctx, slice); err != nil {
			lastErr = err
			var apiErr *KubernetesAPIError
			if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusConflict || apiErr.StatusCode == http.StatusUnprocessableEntity) {
				continue
			}
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("EndpointSlice update conflicted")
	}
	return lastErr
}

func (p *ActiveEndpointPublisher) get(ctx context.Context) (*endpointSlice, error) {
	path := "/apis/discovery.k8s.io/v1/namespaces/" + pathEscape(p.cfg.Namespace) + "/endpointslices/" + pathEscape(p.cfg.EndpointSliceName)
	payload, err := p.client.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return nil, err
	}
	var slice endpointSlice
	if err := json.Unmarshal(payload, &slice); err != nil {
		return nil, fmt.Errorf("decode active controller EndpointSlice: %w", err)
	}
	return &slice, nil
}

func (p *ActiveEndpointPublisher) validateSlice(slice *endpointSlice) error {
	if slice == nil {
		return fmt.Errorf("active controller EndpointSlice is unavailable")
	}
	if strings.TrimSpace(slice.Metadata.Name) != p.cfg.EndpointSliceName ||
		strings.TrimSpace(slice.Metadata.Namespace) != p.cfg.Namespace {
		return fmt.Errorf("active controller EndpointSlice identity does not match configuration")
	}
	if strings.TrimSpace(slice.Metadata.ResourceVersion) == "" {
		return fmt.Errorf("active controller EndpointSlice has no resourceVersion")
	}
	if strings.TrimSpace(slice.Metadata.Labels["kubernetes.io/service-name"]) != p.cfg.ServiceName {
		return fmt.Errorf("active controller EndpointSlice is not bound to the configured Service")
	}
	if strings.TrimSpace(slice.Metadata.Labels["endpointslice.kubernetes.io/managed-by"]) != activeEndpointManagedBy {
		return fmt.Errorf("active controller EndpointSlice is not managed by %q", activeEndpointManagedBy)
	}
	if !strings.EqualFold(strings.TrimSpace(slice.AddressType), p.addressType) {
		return fmt.Errorf("active controller EndpointSlice address type %q does not match pod IP family %q", slice.AddressType, p.addressType)
	}
	return nil
}

func activeEndpointTerm(slice *endpointSlice) (string, uint64, error) {
	if slice == nil {
		return "", 0, fmt.Errorf("active controller EndpointSlice is unavailable")
	}
	rawOwnerID, ownerPresent := slice.Metadata.Annotations[activeEndpointOwnerAnnotation]
	rawGeneration, generationPresent := slice.Metadata.Annotations[activeEndpointGenerationAnnotation]
	if !ownerPresent || !generationPresent {
		return "", 0, fmt.Errorf("active controller EndpointSlice has an incomplete lease fence")
	}
	ownerID := strings.TrimSpace(rawOwnerID)
	rawGeneration = strings.TrimSpace(rawGeneration)
	if ownerID == "" && rawGeneration == "0" {
		if len(slice.Endpoints) != 0 {
			return "", 0, fmt.Errorf("active controller EndpointSlice has endpoints without a lease fence")
		}
		return "", 0, nil
	}
	if ownerID == "" || rawGeneration == "" {
		return "", 0, fmt.Errorf("active controller EndpointSlice has an incomplete lease fence")
	}
	generation, err := strconv.ParseUint(rawGeneration, 10, 64)
	if err != nil || generation == 0 {
		return "", 0, fmt.Errorf("active controller EndpointSlice has invalid lease generation %q", rawGeneration)
	}
	return ownerID, generation, nil
}

func (p *ActiveEndpointPublisher) patch(ctx context.Context, slice *endpointSlice) error {
	operations := []jsonPatchOperation{
		{Op: "test", Path: "/metadata/resourceVersion", Value: slice.Metadata.ResourceVersion},
		{Op: "add", Path: "/metadata/annotations", Value: slice.Metadata.Annotations},
		{Op: "replace", Path: "/endpoints", Value: slice.Endpoints},
		{Op: "replace", Path: "/ports", Value: slice.Ports},
	}
	body, err := json.Marshal(operations)
	if err != nil {
		return err
	}
	path := "/apis/discovery.k8s.io/v1/namespaces/" + pathEscape(p.cfg.Namespace) + "/endpointslices/" + pathEscape(p.cfg.EndpointSliceName)
	_, err = p.client.do(ctx, http.MethodPatch, path, "application/json-patch+json", body)
	return err
}

func (p *ActiveEndpointPublisher) ownEndpoint() endpointSliceEndpoint {
	ready := true
	serving := true
	terminating := false
	return endpointSliceEndpoint{
		Addresses: []string{p.cfg.PodIP},
		Conditions: endpointSliceConditions{
			Ready:       &ready,
			Serving:     &serving,
			Terminating: &terminating,
		},
		TargetRef: &endpointSliceTargetRef{
			Kind:      "Pod",
			Namespace: p.cfg.Namespace,
			Name:      p.cfg.PodName,
		},
	}
}

func (p *ActiveEndpointPublisher) ownPort() endpointSlicePort {
	protocol := "TCP"
	port := p.cfg.Port
	return endpointSlicePort{Name: p.cfg.PortName, Protocol: protocol, Port: &port}
}

func (p *ActiveEndpointPublisher) sliceTargetsThisPod(slice *endpointSlice) bool {
	if slice == nil || len(slice.Endpoints) != 1 || len(slice.Ports) != 1 {
		return false
	}
	endpoint := slice.Endpoints[0]
	if len(endpoint.Addresses) != 1 || strings.TrimSpace(endpoint.Addresses[0]) != p.cfg.PodIP || endpoint.TargetRef == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(endpoint.TargetRef.Kind), "Pod") ||
		strings.TrimSpace(endpoint.TargetRef.Namespace) != p.cfg.Namespace ||
		strings.TrimSpace(endpoint.TargetRef.Name) != p.cfg.PodName {
		return false
	}
	if endpoint.Conditions.Ready == nil || !*endpoint.Conditions.Ready ||
		endpoint.Conditions.Serving == nil || !*endpoint.Conditions.Serving ||
		endpoint.Conditions.Terminating == nil || *endpoint.Conditions.Terminating {
		return false
	}
	port := slice.Ports[0]
	return strings.TrimSpace(port.Name) == p.cfg.PortName &&
		strings.EqualFold(strings.TrimSpace(port.Protocol), "TCP") &&
		port.Port != nil && *port.Port == p.cfg.Port
}

type jsonPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

type endpointSlice struct {
	Metadata struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		ResourceVersion string            `json:"resourceVersion"`
		Labels          map[string]string `json:"labels"`
		Annotations     map[string]string `json:"annotations"`
	} `json:"metadata"`
	AddressType string                  `json:"addressType"`
	Endpoints   []endpointSliceEndpoint `json:"endpoints"`
	Ports       []endpointSlicePort     `json:"ports"`
}

type endpointSliceEndpoint struct {
	Addresses  []string                `json:"addresses"`
	Conditions endpointSliceConditions `json:"conditions,omitempty"`
	TargetRef  *endpointSliceTargetRef `json:"targetRef,omitempty"`
}

type endpointSliceConditions struct {
	Ready       *bool `json:"ready,omitempty"`
	Serving     *bool `json:"serving,omitempty"`
	Terminating *bool `json:"terminating,omitempty"`
}

type endpointSliceTargetRef struct {
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

type endpointSlicePort struct {
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Port     *int32 `json:"port,omitempty"`
}
