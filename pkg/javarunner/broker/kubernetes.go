package broker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// TerminationAttestor must independently prove this exact container boundary
// stopped, including descendants, on the qualified cluster runtime. Kubernetes
// object deletion/status alone is deliberately not an implementation.
type TerminationAttestor interface {
	BoundaryTerminated(context.Context, PodReceipt) (bool, error)
}

type KubernetesBackend struct {
	origin, namespace string
	client            *http.Client
	token             func(context.Context) (string, error)
	attestor          TerminationAttestor
}

func NewKubernetesBackend(endpoint, namespace string, config *tls.Config, token func(context.Context) (string, error), attestor TerminationAttestor) (*KubernetesBackend, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || !namespaceName.MatchString(namespace) || config == nil || config.InsecureSkipVerify || token == nil || attestor == nil {
		return nil, ErrState
	}
	tc := config.Clone()
	tc.MinVersion = max(tc.MinVersion, tls.VersionTLS12)
	if tc.RootCAs != nil {
		tc.RootCAs = tc.RootCAs.Clone()
	}
	transport := &http.Transport{TLSClientConfig: tc, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxIdleConnsPerHost: 8, IdleConnTimeout: 30 * time.Second}
	return &KubernetesBackend{origin: strings.TrimRight(endpoint, "/"), namespace: namespace, token: token, attestor: attestor, client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (k *KubernetesBackend) Close() { k.client.CloseIdleConnections() }

func (k *KubernetesBackend) request(ctx context.Context, method, pod string, body []byte) ([]byte, int, error) {
	return k.resourceRequest(ctx, method, "pods", pod, body)
}

func (k *KubernetesBackend) resourceRequest(ctx context.Context, method, resource, object string, body []byte) ([]byte, int, error) {
	if resource != "pods" && resource != "secrets" || object != "" && !namespaceName.MatchString(object) {
		return nil, 0, ErrState
	}
	path := k.origin + "/api/v1/namespaces/" + k.namespace + "/" + resource
	if object != "" {
		path += "/" + object
	}
	token, err := k.token(ctx)
	if err != nil || token == "" || len(token) > 16384 || strings.ContainsAny(token, "\r\n") {
		return nil, 0, errors.New("java_broker_kubernetes_auth_failed")
	}
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, ErrState
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := k.client.Do(req)
	if err != nil {
		return nil, 0, errors.New("java_broker_kubernetes_transport_failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, response.StatusCode, errors.New("java_broker_kubernetes_response_invalid")
	}
	return data, response.StatusCode, nil
}

const hostAnnotation = "apexload.io/java-host"
const epochAnnotation = "apexload.io/java-broker-epoch"

type podResponse struct {
	Metadata struct {
		Name, Namespace, UID string
		Annotations          map[string]string
	}
	Spec struct {
		Containers                          []struct{ Name string }
		InitContainers, EphemeralContainers []json.RawMessage
	}
	Status struct {
		ContainerStatuses []struct{ Name, ContainerID string }
	}
}

func (k *KubernetesBackend) receipt(data []byte, expected string) (PodReceipt, error) {
	var pod podResponse
	if json.Unmarshal(data, &pod) != nil || pod.Metadata.Name != expected || pod.Metadata.Namespace != k.namespace || !name.MatchString(pod.Metadata.UID) || len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Name != "java-host" || len(pod.Spec.InitContainers) != 0 || len(pod.Spec.EphemeralContainers) != 0 || len(pod.Status.ContainerStatuses) > 1 {
		return PodReceipt{}, ErrCleanup
	}
	r := PodReceipt{Namespace: k.namespace, Name: expected, UID: pod.Metadata.UID, HostID: pod.Metadata.Annotations[hostAnnotation], BrokerEpoch: pod.Metadata.Annotations[epochAnnotation]}
	if !name.MatchString(r.HostID) || !name.MatchString(r.BrokerEpoch) {
		return PodReceipt{}, ErrCleanup
	}
	if len(pod.Status.ContainerStatuses) == 1 {
		s := pod.Status.ContainerStatuses[0]
		if s.Name != "java-host" || s.ContainerID != "" && !validContainer(s.ContainerID) {
			return PodReceipt{}, ErrCleanup
		}
		r.ContainerID = s.ContainerID
	}
	return r, nil
}

func (k *KubernetesBackend) Find(ctx context.Context, namespace, pod string) (PodReceipt, error) {
	if namespace != k.namespace {
		return PodReceipt{}, ErrState
	}
	data, status, err := k.request(ctx, http.MethodGet, pod, nil)
	if err != nil || status != http.StatusOK {
		return PodReceipt{}, ErrCleanup
	}
	return k.receipt(data, pod)
}
func (k *KubernetesBackend) Delete(ctx context.Context, pod PodReceipt) error {
	if pod.Namespace != k.namespace || !name.MatchString(pod.UID) || pod.Name == "" {
		return ErrState
	}
	body, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": pod.UID}, "propagationPolicy": "Foreground", "gracePeriodSeconds": int64(0)})
	_, status, err := k.request(ctx, http.MethodDelete, pod.Name, body)
	if err != nil || status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNotFound {
		return ErrCleanup
	}
	return nil
}
func (k *KubernetesBackend) BoundaryTerminated(ctx context.Context, pod PodReceipt) (bool, error) {
	if pod.Namespace != k.namespace || !name.MatchString(pod.UID) || !validContainer(pod.ContainerID) || !name.MatchString(pod.HostID) || !name.MatchString(pod.BrokerEpoch) || !namespaceName.MatchString(pod.Name) {
		return false, ErrState
	}
	return k.attestor.BoundaryTerminated(ctx, pod)
}

// ExecutionPodConfig is operator-owned and contains no generator credentials.
// Approved assets and the supervisor are baked into the immutable image. TLSSecret
// contains only host-scoped transport material; it must not authorize lease renewal.
// No arbitrary pod template, environment, command or volume input is accepted.
type ExecutionPodConfig struct {
	Image, TLSSecret, ServiceAccount string
	CPUmilli, ScratchBytes           int64
	NodeSelector                     map[string]string
	Context                          *javarunner.DedicatedHostContext
}

var selectorPart = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9_.-]{0,61}[A-Za-z0-9])?$`)

func validNodeSelector(key, value string) bool {
	parts := strings.Split(key, "/")
	if len(parts) > 2 || !selectorPart.MatchString(parts[len(parts)-1]) || value != "" && !selectorPart.MatchString(value) {
		return false
	}
	if len(parts) == 2 {
		if len(parts[0]) > 253 {
			return false
		}
		for _, part := range strings.Split(parts[0], ".") {
			if !namespaceName.MatchString(part) {
				return false
			}
		}
	}
	return true
}

var imageDigest = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./:_-]{0,254}@sha256:[a-f0-9]{64}$`)

func ExecutionPod(namespace string, r Record, cfg ExecutionPodConfig) ([]byte, error) {
	digest, digestErr := hex.DecodeString(r.Binding.ConfigurationSHA256)
	if digestErr != nil || len(digest) != 32 || !namespaceName.MatchString(namespace) || !namespaceName.MatchString(r.PodName) || !name.MatchString(r.ID) || !name.MatchString(r.BrokerEpoch) || r.MemoryBytes <= 0 || !imageDigest.MatchString(cfg.Image) || !namespaceName.MatchString(cfg.TLSSecret) || !namespaceName.MatchString(cfg.ServiceAccount) || cfg.CPUmilli < 1 || cfg.CPUmilli > 1000000 || cfg.ScratchBytes < 1 || cfg.ScratchBytes > 1<<40 || len(cfg.NodeSelector) > 32 {
		return nil, ErrState
	}
	selector := map[string]string{"kubernetes.io/os": "linux"}
	for key, value := range cfg.NodeSelector {
		if !validNodeSelector(key, value) || key == "kubernetes.io/os" && value != "linux" {
			return nil, ErrState
		}
		selector[key] = value
	}
	args := []string{"--host", r.ID, "--broker-epoch", r.BrokerEpoch, "--configuration-sha256", r.Binding.ConfigurationSHA256, "--tls-dir", "/transport", "--work-dir", "/scratch"}
	if cfg.Context != nil {
		encoded, e := javarunner.EncodeDedicatedHostContext(*cfg.Context)
		if e != nil || r.SessionID == "" || cfg.Context.SessionID != r.SessionID {
			return nil, ErrState
		}
		args = append(args, "--context", encoded)
	} else if r.SessionID != "" {
		return nil, ErrState
	}
	resources := map[string]string{"cpu": strconv.FormatInt(cfg.CPUmilli, 10) + "m", "memory": strconv.FormatInt(r.MemoryBytes, 10), "ephemeral-storage": strconv.FormatInt(cfg.ScratchBytes, 10)}
	return json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"namespace": namespace, "name": r.PodName, "labels": map[string]string{"app.kubernetes.io/name": "apexload-java-execution"}, "annotations": map[string]string{hostAnnotation: r.ID, epochAnnotation: r.BrokerEpoch}},
		"spec": map[string]any{
			"restartPolicy": "Never", "automountServiceAccountToken": false, "serviceAccountName": cfg.ServiceAccount, "enableServiceLinks": false,
			"hostNetwork": false, "hostPID": false, "hostIPC": false, "shareProcessNamespace": false, "terminationGracePeriodSeconds": int64(5), "nodeSelector": selector,
			"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": int64(10001), "runAsGroup": int64(10001), "fsGroup": int64(10001), "seccompProfile": map[string]string{"type": "RuntimeDefault"}},
			"containers": []any{map[string]any{"name": "java-host", "image": cfg.Image, "imagePullPolicy": "IfNotPresent", "command": []string{"/apexload-java-host-supervisor"}, "args": args, "workingDir": "/scratch",
				"securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}},
				"resources":       map[string]any{"requests": resources, "limits": resources},
				"volumeMounts":    []any{map[string]any{"name": "scratch", "mountPath": "/scratch"}, map[string]any{"name": "scratch", "mountPath": "/tmp"}, map[string]any{"name": "transport", "mountPath": "/transport", "readOnly": true}}}},
			"volumes": []any{map[string]any{"name": "scratch", "emptyDir": map[string]string{"sizeLimit": strconv.FormatInt(cfg.ScratchBytes, 10)}}, map[string]any{"name": "transport", "secret": map[string]any{"secretName": cfg.TLSSecret, "defaultMode": int64(0440)}}},
		},
	})
}

// Provision durably records intent before the single create attempt. An
// ambiguous response is quarantined; it is never retried as a second JVM.
// This is preparation only: readiness/commit require a separate supervisor
// protocol and must never be inferred from Kubernetes Running/Ready status.
func (k *KubernetesBackend) Provision(ctx context.Context, store *Store, epoch, id string, cfg ExecutionPodConfig) (PodReceipt, error) {
	if store == nil || store.config.Namespace != k.namespace {
		return PodReceipt{}, ErrState
	}
	records, _, err := store.Snapshot(ctx, epoch)
	if err != nil {
		return PodReceipt{}, err
	}
	var record Record
	for _, r := range records {
		if r.ID == id {
			record = r
			break
		}
	}
	body, err := ExecutionPod(k.namespace, record, cfg)
	if err != nil {
		return PodReceipt{}, err
	}
	if err = store.BeginPreparePod(ctx, epoch, id, podPolicyDigest(body)); err != nil {
		return PodReceipt{}, err
	}
	data, status, err := k.request(ctx, http.MethodPost, "", body)
	var receipt PodReceipt
	if err == nil && status == http.StatusCreated {
		receipt, err = k.receipt(data, record.PodName)
	} else {
		err = ErrCleanup
	}
	if err == nil {
		err = verifyAdmittedPod(body, data, false)
	}
	if err == nil && (receipt.HostID != id || receipt.BrokerEpoch != epoch) {
		err = ErrCleanup
	}
	// Persist late create results even when the caller's preparation deadline
	// expired. Abort and BindPod retain the exact boundary for reconciliation.
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		// A rejected admission is still a created pod. Save its exact UID from
		// this single create response so cleanup can delete it without trusting
		// mutated labels, a container identity, or a readiness declaration.
		if status == http.StatusCreated {
			if rejected, e := k.creationIdentity(data, record); e == nil {
				receipt = rejected
				_ = store.BindPod(cleanup, epoch, id, receipt.UID, "")
			}
		}
		_ = store.Abort(cleanup, epoch, id, "java_pod_preparation_failed")
		return receipt, ErrCleanup
	}
	if err = bindPreparedReceipt(ctx, store, epoch, id, receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func podPolicyDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func (k *KubernetesBackend) creationIdentity(data []byte, record Record) (PodReceipt, error) {
	var pod struct {
		Metadata struct{ Name, Namespace, UID string }
	}
	if json.Unmarshal(data, &pod) != nil || pod.Metadata.Name != record.PodName || pod.Metadata.Namespace != k.namespace || !name.MatchString(pod.Metadata.UID) {
		return PodReceipt{}, ErrCleanup
	}
	return PodReceipt{Namespace: k.namespace, Name: record.PodName, UID: pod.Metadata.UID, HostID: record.ID, BrokerEpoch: record.BrokerEpoch}, nil
}

// ObservePrepared rechecks the admitted policy and exact UID when a container
// identity becomes available. It never treats Pod Running as harness readiness.
// The returned bound record may then enter the independent TLS readiness gate.
func (k *KubernetesBackend) ObservePrepared(ctx context.Context, store *Store, epoch, id string, cfg ExecutionPodConfig) (PodReceipt, error) {
	if store == nil || store.config.Namespace != k.namespace {
		return PodReceipt{}, ErrState
	}
	records, _, e := store.Snapshot(ctx, epoch)
	if e != nil {
		return PodReceipt{}, e
	}
	var r Record
	for _, record := range records {
		if record.ID == id {
			r = record
			break
		}
	}
	if r.BrokerEpoch != epoch || r.State != javarunner.HostPreparing || r.PodUID == "" || r.PodPolicySHA256 == "" {
		return PodReceipt{}, ErrState
	}
	expected, e := ExecutionPod(k.namespace, r, cfg)
	if e != nil || podPolicyDigest(expected) != r.PodPolicySHA256 {
		return PodReceipt{}, ErrState
	}
	data, status, e := k.request(ctx, http.MethodGet, r.PodName, nil)
	if e != nil || status != http.StatusOK {
		return PodReceipt{}, ErrCleanup
	}
	pod, e := k.receipt(data, r.PodName)
	if e != nil || pod.UID != r.PodUID || pod.HostID != r.ID || pod.BrokerEpoch != epoch || verifyAdmittedPod(expected, data, true) != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = store.Abort(cleanup, epoch, id, "java_pod_policy_changed")
		return PodReceipt{}, ErrCleanup
	}
	if e = bindPreparedReceipt(ctx, store, epoch, id, pod); e != nil {
		return PodReceipt{}, e
	}
	return pod, nil
}

// Preserve a received container identity even when the caller cancels between
// observation and persistence. Cancellation retires it, never erases the receipt.
func bindPreparedReceipt(ctx context.Context, store *Store, epoch, id string, pod PodReceipt) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if e := store.BindPod(cleanup, epoch, id, pod.UID, pod.ContainerID); e != nil {
		return e
	}
	if ctx.Err() != nil {
		_ = store.Abort(cleanup, epoch, id, "java_pod_preparation_expired")
		return ctx.Err()
	}
	return nil
}
