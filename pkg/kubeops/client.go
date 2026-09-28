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
	"net/netip"
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

	defaultRequestTimeout = 15 * time.Second
)

var (
	ErrPodNotFound        = errors.New("pod not found")
	ErrPodMultipleMatches = errors.New("multiple pods matched")
)

// KubernetesAPIError preserves the response status for callers that need to
// distinguish optimistic-concurrency conflicts from permanent API failures.
// The response body is already bounded by maxKubernetesAPIResponseBytes.
type KubernetesAPIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *KubernetesAPIError) Error() string {
	if e == nil {
		return "Kubernetes API request failed"
	}
	return fmt.Sprintf("kubernetes API %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, strings.TrimSpace(e.Body))
}

// TLS trust modes for the API server connection.
const (
	// TLSModeCustomCA verifies the API server against CAPEM or CAFile. This is
	// the default; the zero value means custom CA.
	TLSModeCustomCA = "custom_ca"
	// TLSModeSystemRoots verifies against the host's root store, for clusters
	// fronted by a publicly-trusted certificate.
	TLSModeSystemRoots = "system_roots"
)

type Config struct {
	Namespace         string
	GeneratorSelector string

	// TokenSource authenticates every API request. Nil means the in-cluster
	// projected service-account token at TokenFile, preserving the original
	// read-per-request behavior for rotation.
	TokenSource TokenSource

	// CAPEM is an inline PEM bundle for the API server's CA. It takes
	// precedence over CAFile and is how registered remote clusters carry their
	// trust anchor without touching the filesystem.
	CAPEM string
	// TLSMode selects the trust anchor. Empty means TLSModeCustomCA.
	TLSMode string
	// RequestTimeout bounds a single API call. Zero means 15s.
	RequestTimeout time.Duration
	// DialGuard, when non-nil, vets every resolved API server IP before the
	// connection is made. It exists so pkg/api can apply its egress policy to
	// administrator-supplied endpoints without kubeops importing that package.
	DialGuard func(netip.Addr) error

	// Test seams. Empty values use the standard in-cluster environment and
	// service-account file paths.
	APIServerURL string
	TokenFile    string
	CAFile       string
	HTTPClient   *http.Client
}

type PodRef struct {
	StatefulSetUID  string
	StatefulSetName string
	UID             string
	NodeName        string
	Name            string
	Namespace       string
	IP              string
	Phase           string
	Ready           bool
	Terminating     bool
}

type Client struct {
	cfg Config

	httpMu     sync.Mutex
	httpClient *http.Client
}

// NewInClusterClient builds a client for the cluster the controller runs in:
// the API server comes from the standard environment variables and credentials
// from the projected service-account files. Its signature and semantics are
// unchanged by the pluggable-auth refactor — an unset TokenSource still means
// "re-read the token file on every request".
func NewInClusterClient(cfg Config) (*Client, error) {
	cfg.APIServerURL = strings.TrimRight(strings.TrimSpace(cfg.APIServerURL), "/")
	if cfg.APIServerURL == "" {
		host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
		port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
		if host == "" || port == "" {
			return nil, fmt.Errorf("KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT are required")
		}
		cfg.APIServerURL = "https://" + net.JoinHostPort(host, port)
	}
	cfg.CAFile = firstNonEmpty(strings.TrimSpace(cfg.CAFile), DefaultCAFile)
	return newClient(cfg)
}

// NewRemoteClient builds a client for a registered external cluster. Unlike
// NewInClusterClient it never consults the in-cluster environment or the
// service-account files: the endpoint, trust anchor, and credential must all
// come from the caller, so a misconfigured connection can never silently fall
// back to the controller's own cluster.
func NewRemoteClient(cfg Config) (*Client, error) {
	cfg.APIServerURL = strings.TrimRight(strings.TrimSpace(cfg.APIServerURL), "/")
	if cfg.APIServerURL == "" {
		return nil, fmt.Errorf("API server URL is required")
	}
	if err := ValidateAPIServerURL(cfg.APIServerURL); err != nil {
		return nil, err
	}
	if cfg.TokenSource == nil {
		return nil, fmt.Errorf("a token source is required for a remote cluster")
	}
	if strings.TrimSpace(cfg.CAPEM) == "" && normalizeTLSMode(cfg.TLSMode) != TLSModeSystemRoots {
		return nil, fmt.Errorf("a CA certificate is required unless TLS mode is %q", TLSModeSystemRoots)
	}
	// A remote client must not inherit the in-cluster file defaults.
	cfg.TokenFile = ""
	cfg.CAFile = ""
	return newClient(cfg)
}

// ValidateAPIServerURL enforces the transport-level shape of an API server
// endpoint. Network-level policy (which IPs may be dialed) is the caller's
// concern via Config.DialGuard.
func ValidateAPIServerURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("API server URL is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("API server URL must use https")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("API server URL must include a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("API server URL must not embed credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("API server URL must not include a query or fragment")
	}
	if strings.Trim(parsed.Path, "/") != "" {
		return fmt.Errorf("API server URL must not include a path")
	}
	return nil
}

// newClient applies the shared normalization and invariants for both
// constructors. It is the single place that defaults the token source, so no
// code path can end up with a nil credential.
func newClient(cfg Config) (*Client, error) {
	cfg.Namespace = strings.TrimSpace(cfg.Namespace)
	cfg.GeneratorSelector = strings.TrimSpace(cfg.GeneratorSelector)
	cfg.CAPEM = strings.TrimSpace(cfg.CAPEM)
	cfg.TLSMode = normalizeTLSMode(cfg.TLSMode)
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if cfg.TokenSource == nil {
		cfg.TokenSource = ServiceAccountFileTokenSource{Path: cfg.TokenFile}
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	return &Client{cfg: cfg}, nil
}

func normalizeTLSMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), TLSModeSystemRoots) {
		return TLSModeSystemRoots
	}
	return TLSModeCustomCA
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
		ownerUID, ownerName := "", ""
		controllers := 0
		for _, owner := range item.Metadata.OwnerReferences {
			if !owner.Controller {
				continue
			}
			controllers++
			if owner.APIVersion == "apps/v1" && owner.Kind == "StatefulSet" {
				ownerUID, ownerName = owner.UID, owner.Name
			}
		}
		if controllers != 1 {
			ownerUID, ownerName = "", ""
		}
		out = append(out, PodRef{
			StatefulSetUID: ownerUID, StatefulSetName: ownerName,
			UID:         strings.TrimSpace(item.Metadata.UID),
			NodeName:    strings.TrimSpace(item.Spec.NodeName),
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
		// Drop any cached credential before the retry. A file-backed source
		// re-reads per call and needs no hint, but a minted token (Azure AD)
		// would otherwise re-present the same rejected bearer token.
		if invalidating, ok := c.cfg.TokenSource.(invalidatingTokenSource); ok {
			invalidating.Invalidate()
		}
		payload, status, err = c.doOnce(ctx, method, path, contentType, body)
		if err != nil {
			return nil, err
		}
	}
	if status < 200 || status >= 300 {
		return nil, &KubernetesAPIError{
			Method:     method,
			Path:       path,
			StatusCode: status,
			Body:       string(payload),
		}
	}
	return payload, nil
}

func (c *Client) doOnce(ctx context.Context, method, path, contentType string, body []byte) ([]byte, int, error) {
	token, err := c.cfg.TokenSource.Token(ctx)
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

// resolveHTTPClient returns the shared HTTPS client, building it (and reading the
// cluster CA) at most once and reusing it across calls. ListPods runs on every
// dashboard-polled system-inventory request; rebuilding the client per call re-read
// ca.crt from disk and dialed a fresh connection each time. The bearer token is
// intentionally NOT cached here — that is the TokenSource's concern, and the
// in-cluster source re-reads per call because the projected token rotates. On a
// build error the client is not cached, so a later call retries (preserving the
// original lazy semantics: construction succeeds, the CA error surfaces on
// first use).
func (c *Client) resolveHTTPClient() (*http.Client, error) {
	if c.cfg.HTTPClient != nil {
		return c.cfg.HTTPClient, nil
	}
	c.httpMu.Lock()
	defer c.httpMu.Unlock()
	if c.httpClient != nil {
		return c.httpClient, nil
	}
	client, err := buildHTTPClient(c.cfg)
	if err != nil {
		return nil, err
	}
	c.httpClient = client
	return c.httpClient, nil
}

// buildHTTPClient assembles the TLS trust anchor and, for administrator-supplied
// endpoints, the per-IP dial guard. Nil RootCAs means the host root store, which
// is only reachable through an explicit TLSModeSystemRoots — there is deliberately
// no way to skip verification.
func buildHTTPClient(cfg Config) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case cfg.CAPEM != "":
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
			return nil, fmt.Errorf("cluster CA certificate did not contain a PEM certificate")
		}
		tlsConfig.RootCAs = roots
	case cfg.TLSMode == TLSModeSystemRoots:
		// Leave RootCAs nil so crypto/tls uses the host root store.
	default:
		roots := x509.NewCertPool()
		ca, err := readBoundedFile(cfg.CAFile, maxServiceAccountCABytes)
		if err != nil {
			return nil, fmt.Errorf("read Kubernetes service-account CA: %w", err)
		}
		if !roots.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("Kubernetes service-account CA did not contain a PEM certificate")
		}
		tlsConfig.RootCAs = roots
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}
	if cfg.DialGuard != nil {
		transport.Proxy = nil
		transport.DialContext = guardedDialContext(cfg.DialGuard)
	}
	return &http.Client{Timeout: cfg.RequestTimeout, Transport: transport}, nil
}

// guardedDialContext resolves the host itself, vets every candidate address, and
// dials the validated IP literal. Dialing the literal rather than the name is
// what closes the DNS-rebinding window between the policy check and the connect.
func guardedDialContext(guard func(netip.Addr) error) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, addr := range addrs {
			addr = addr.Unmap()
			if guardErr := guard(addr); guardErr != nil {
				lastErr = guardErr
				continue
			}
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no addresses resolved for %q", host)
		}
		return nil, lastErr
	}
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
			UID               string `json:"uid"`
			Name              string `json:"name"`
			Namespace         string `json:"namespace"`
			DeletionTimestamp string `json:"deletionTimestamp"`
			OwnerReferences   []struct {
				APIVersion, Kind, Name, UID string
				Controller                  bool
			} `json:"ownerReferences"`
		} `json:"metadata"`
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
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
