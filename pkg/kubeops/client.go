package kubeops

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	DefaultCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	maxKubernetesAPIResponseBytes int64 = 8 << 20
	maxServiceAccountTokenBytes   int64 = 1 << 20
	maxServiceAccountCABytes      int64 = 4 << 20
)

var (
	ErrPodNotFound        = errors.New("pod not found")
	ErrPodMultipleMatches = errors.New("multiple pods matched")
)

type Config struct {
	Namespace         string
	GeneratorSelector string

	// Test seams. Empty values use the standard in-cluster environment and
	// service-account file paths.
	APIServerURL string
	TokenFile    string
	CAFile       string
	HTTPClient   *http.Client
}

type PodRef struct {
	Name        string
	Namespace   string
	IP          string
	Phase       string
	Ready       bool
	Terminating bool
}

type Client struct {
	cfg Config

	httpMu     sync.Mutex
	httpClient *http.Client
}

func NewInClusterClient(cfg Config) (*Client, error) {
	cfg.Namespace = strings.TrimSpace(cfg.Namespace)
	cfg.GeneratorSelector = strings.TrimSpace(cfg.GeneratorSelector)
	cfg.APIServerURL = strings.TrimRight(strings.TrimSpace(cfg.APIServerURL), "/")
	cfg.TokenFile = firstNonEmpty(strings.TrimSpace(cfg.TokenFile), DefaultTokenFile)
	cfg.CAFile = firstNonEmpty(strings.TrimSpace(cfg.CAFile), DefaultCAFile)

	if cfg.APIServerURL == "" {
		host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
		port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
		if host == "" || port == "" {
			return nil, fmt.Errorf("KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT are required")
		}
		cfg.APIServerURL = "https://" + net.JoinHostPort(host, port)
	}
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	return &Client{cfg: cfg}, nil
}

func (c *Client) DeletePod(ctx context.Context, namespace, name string) error {
	if c == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	path := "/api/v1/namespaces/" + pathEscape(namespace) + "/pods/" + pathEscape(name)
	_, err := c.do(ctx, http.MethodDelete, path, "", nil)
	return err
}

func (c *Client) RolloutRestart(ctx context.Context, kind, namespace, name string) error {
	if c == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	plural, err := workloadPlural(kind)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339Nano),
					},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	path := "/apis/apps/v1/namespaces/" + pathEscape(namespace) + "/" + plural + "/" + pathEscape(name)
	_, err = c.do(ctx, http.MethodPatch, path, "application/strategic-merge-patch+json", body)
	return err
}

// ScaleStatefulSet sets the desired replica count on a generator StatefulSet via a
// merge patch. It reuses the same in-cluster client + RBAC (statefulsets patch) as
// RolloutRestart, so no additional permissions are required.
func (c *Client) ScaleStatefulSet(ctx context.Context, namespace, name string, replicas int) error {
	if c == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}
	if replicas < 0 {
		return fmt.Errorf("replicas must be >= 0")
	}
	payload := map[string]any{
		"spec": map[string]any{
			"replicas": replicas,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	path := "/apis/apps/v1/namespaces/" + pathEscape(namespace) + "/statefulsets/" + pathEscape(name)
	_, err = c.do(ctx, http.MethodPatch, path, "application/merge-patch+json", body)
	return err
}

func (c *Client) PodByIP(ctx context.Context, namespace, selector, ip string) (PodRef, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return PodRef{}, fmt.Errorf("pod IP is required")
	}
	pods, err := c.ListPods(ctx, namespace, selector)
	if err != nil {
		return PodRef{}, err
	}
	matches := make([]PodRef, 0, 1)
	for _, pod := range pods {
		if pod.IP == ip {
			matches = append(matches, pod)
		}
	}
	switch len(matches) {
	case 0:
		return PodRef{}, ErrPodNotFound
	case 1:
		return matches[0], nil
	default:
		return PodRef{}, ErrPodMultipleMatches
	}
}

func (c *Client) ListPods(ctx context.Context, namespace, selector string) ([]PodRef, error) {
	if c == nil {
		return nil, fmt.Errorf("kubernetes client is not initialized")
	}
	query := url.Values{}
	if strings.TrimSpace(selector) != "" {
		query.Set("labelSelector", strings.TrimSpace(selector))
	}
	path := "/api/v1/namespaces/" + pathEscape(namespace) + "/pods"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	payload, err := c.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return nil, err
	}
	var list podList
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, fmt.Errorf("decode pods response: %w", err)
	}
	out := make([]PodRef, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, PodRef{
			Name:        strings.TrimSpace(item.Metadata.Name),
			Namespace:   firstNonEmpty(strings.TrimSpace(item.Metadata.Namespace), strings.TrimSpace(namespace)),
			IP:          strings.TrimSpace(item.Status.PodIP),
			Phase:       strings.TrimSpace(item.Status.Phase),
			Ready:       podReady(item.Status.Conditions),
			Terminating: strings.TrimSpace(item.Metadata.DeletionTimestamp) != "",
		})
	}
	return out, nil
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	payload, status, err := c.doOnce(ctx, method, path, contentType, body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		payload, status, err = c.doOnce(ctx, method, path, contentType, body)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("kubernetes API %s %s returned %d: %s", method, path, status, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func (c *Client) doOnce(ctx context.Context, method, path, contentType string, body []byte) ([]byte, int, error) {
	token, err := c.readToken()
	if err != nil {
		return nil, 0, err
	}
	client, err := c.resolveHTTPClient()
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.APIServerURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > maxKubernetesAPIResponseBytes {
		return nil, 0, fmt.Errorf("kubernetes API response exceeds %d bytes", maxKubernetesAPIResponseBytes)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxKubernetesAPIResponseBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(payload)) > maxKubernetesAPIResponseBytes {
		return nil, 0, fmt.Errorf("kubernetes API response exceeds %d bytes", maxKubernetesAPIResponseBytes)
	}
	return payload, resp.StatusCode, nil
}

func (c *Client) readToken() (string, error) {
	raw, err := readBoundedFile(c.cfg.TokenFile, maxServiceAccountTokenBytes)
	if err != nil {
		return "", fmt.Errorf("read Kubernetes service-account token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("Kubernetes service-account token is empty")
	}
	return token, nil
}

// resolveHTTPClient returns the shared HTTPS client, building it (and reading the
// cluster CA) at most once and reusing it across calls. ListPods runs on every
// dashboard-polled system-inventory request; rebuilding the client per call re-read
// ca.crt from disk and dialed a fresh connection each time. The service-account
// token is intentionally NOT cached — readToken re-reads it per call because it
// rotates. On a build error the client is not cached, so a later call retries
// (preserving the original lazy semantics: construction succeeds, the CA error
// surfaces on first use).
func (c *Client) resolveHTTPClient() (*http.Client, error) {
	if c.cfg.HTTPClient != nil {
		return c.cfg.HTTPClient, nil
	}
	c.httpMu.Lock()
	defer c.httpMu.Unlock()
	if c.httpClient != nil {
		return c.httpClient, nil
	}
	client, err := buildInClusterHTTPClient(c.cfg.CAFile)
	if err != nil {
		return nil, err
	}
	c.httpClient = client
	return c.httpClient, nil
}

func buildInClusterHTTPClient(caFile string) (*http.Client, error) {
	roots := x509.NewCertPool()
	ca, err := readBoundedFile(caFile, maxServiceAccountCABytes)
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes service-account CA: %w", err)
	}
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("Kubernetes service-account CA did not contain a PEM certificate")
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		},
	}, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, statErr := file.Stat(); statErr != nil {
		return nil, statErr
	} else if info.Size() > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func workloadPlural(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "deployment", "deployments":
		return "deployments", nil
	case "statefulset", "statefulsets":
		return "statefulsets", nil
	default:
		return "", fmt.Errorf("unsupported Kubernetes workload kind %q", kind)
	}
}

func pathEscape(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type podList struct {
	Items []struct {
		Metadata struct {
			Name              string `json:"name"`
			Namespace         string `json:"namespace"`
			DeletionTimestamp string `json:"deletionTimestamp"`
		} `json:"metadata"`
		Status struct {
			PodIP      string         `json:"podIP"`
			Phase      string         `json:"phase"`
			Conditions []podCondition `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type podCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

func podReady(conditions []podCondition) bool {
	for _, condition := range conditions {
		if strings.EqualFold(strings.TrimSpace(condition.Type), "Ready") {
			return strings.EqualFold(strings.TrimSpace(condition.Status), "True")
		}
	}
	return false
}
