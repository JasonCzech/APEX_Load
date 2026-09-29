package broker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const AttestorContract = "apexload.java-boundary-attestor/v1"
const NoStartAttestorContract = "apexload.java-no-start-attestor/v1"

var ErrAttestation = errors.New("java_boundary_attestation_unproven")

// AttestorConfig is operator-owned. QualificationSHA256 identifies the reviewed
// external runtime/fencing implementation and its exact deployment boundary.
// A client cannot establish that qualification just by connecting successfully.
type AttestorConfig struct {
	Endpoint, ServerURI, BrokerClientURI, QualificationSHA256 string
	Timeout                                                   time.Duration
}

type AttestationRequest struct {
	Contract            string     `json:"contract"`
	RequestID           string     `json:"request_id"`
	QualificationSHA256 string     `json:"qualification_sha256"`
	Boundary            PodReceipt `json:"boundary"`
}

type BoundaryProof struct {
	// Either a qualified runtime proves the whole container boundary empty, or
	// infrastructure fencing proves its exact immutable machine is stopped.
	Kind           string    `json:"kind"`
	Scope          string    `json:"scope"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
	ObservedAt     time.Time `json:"observed_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	// Fencing must identify an immutable node and backing machine, not a reused
	// node name. The external service owns durable boundary-to-machine provenance.
	NodeUID    string `json:"node_uid,omitempty"`
	MachineID  string `json:"machine_id,omitempty"`
	MachineUID string `json:"machine_uid,omitempty"`
	FenceID    string `json:"fence_id,omitempty"`
	// CreationFenceID identifies the attestor's durable barrier against any
	// delayed admission for an unstarted creation intent.
	CreationFenceID string `json:"creation_fence_id,omitempty"`
}

type AttestationResponse struct {
	Contract            string         `json:"contract"`
	RequestID           string         `json:"request_id"`
	QualificationSHA256 string         `json:"qualification_sha256"`
	Boundary            PodReceipt     `json:"boundary"`
	Result              string         `json:"result"` // terminated | unproven
	Proof               *BoundaryProof `json:"proof,omitempty"`
}

type NoStartAttestationRequest struct {
	Contract            string       `json:"contract"`
	RequestID           string       `json:"request_id"`
	QualificationSHA256 string       `json:"qualification_sha256"`
	Intent              UnstartedPod `json:"intent"`
}

type NoStartAttestationResponse struct {
	Contract            string         `json:"contract"`
	RequestID           string         `json:"request_id"`
	QualificationSHA256 string         `json:"qualification_sha256"`
	Intent              UnstartedPod   `json:"intent"`
	Result              string         `json:"result"` // never_started | unproven
	Proof               *BoundaryProof `json:"proof,omitempty"`
}

// ExternalAttestor consumes positive evidence over a separate pinned mTLS
// channel. The attestor receives no Kubernetes token or execution credentials.
// Neither execution pods nor facade callers may provide attestations directly.
// Each call uses a new challenge and never treats absence, timeout, deletion or
// an unqualified node-status observation as proof. This client never fences a
// machine itself; that authority belongs to the external attestor service.
type ExternalAttestor struct {
	config AttestorConfig
	http   *http.Client
	now    func() time.Time
}

func validAttestorURI(raw string) bool {
	if !validBrokerURI(raw) {
		return false
	}
	u, _ := url.Parse(raw)
	return strings.HasPrefix(u.Path, "/java-boundary-attestor/") && name.MatchString(strings.TrimPrefix(u.Path, "/java-boundary-attestor/"))
}
func NewExternalAttestor(cfg AttestorConfig, config *tls.Config) (*ExternalAttestor, error) {
	u, e := url.Parse(cfg.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" || !validAttestorURI(cfg.ServerURI) || !validBrokerURI(cfg.BrokerClientURI) || cfg.ServerURI == cfg.BrokerClientURI || !validPolicyDigest(cfg.QualificationSHA256) || cfg.Timeout < time.Millisecond || cfg.Timeout > 30*time.Second || config == nil || config.RootCAs == nil || config.InsecureSkipVerify || len(config.Certificates) != 1 || len(config.Certificates[0].Certificate) == 0 {
		return nil, ErrState
	}
	leaf, e := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if e != nil {
		return nil, ErrState
	}
	broker := false
	for _, uri := range leaf.URIs {
		if uri.String() == cfg.ServerURI || strings.Contains(uri.Path, "/java-host/") {
			return nil, ErrState
		}
		if uri.String() == cfg.BrokerClientURI {
			broker = true
		}
	}
	if !broker {
		return nil, ErrState
	}
	tc := config.Clone()
	tc.MinVersion = max(tc.MinVersion, tls.VersionTLS13)
	if tc.RootCAs != nil {
		tc.RootCAs = tc.RootCAs.Clone()
	}
	verify := tc.VerifyConnection
	tc.VerifyConnection = func(s tls.ConnectionState) error {
		if len(s.VerifiedChains) == 0 || len(s.VerifiedChains[0]) == 0 {
			return ErrAttestation
		}
		found := false
		for _, uri := range s.VerifiedChains[0][0].URIs {
			if uri.String() == cfg.BrokerClientURI || strings.HasPrefix(uri.Path, "/java-host/") {
				return ErrAttestation
			}
			if uri.String() == cfg.ServerURI {
				found = true
			}
		}
		if !found {
			return ErrAttestation
		}
		if verify != nil {
			return verify(s)
		}
		return nil
	}
	transport := &http.Transport{TLSClientConfig: tc, TLSHandshakeTimeout: min(cfg.Timeout, 3*time.Second), ResponseHeaderTimeout: cfg.Timeout, IdleConnTimeout: 10 * time.Second, MaxIdleConnsPerHost: 8, MaxResponseHeaderBytes: 8 << 10}
	return &ExternalAttestor{config: cfg, http: &http.Client{Transport: transport, Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now}, nil
}
func (a *ExternalAttestor) Close() { a.http.CloseIdleConnections() }
func validBoundary(p PodReceipt) bool {
	return namespaceName.MatchString(p.Namespace) && namespaceName.MatchString(p.Name) && name.MatchString(p.UID) && validContainer(p.ContainerID) && name.MatchString(p.HostID) && name.MatchString(p.BrokerEpoch)
}
func validUnstartedPod(p UnstartedPod) bool {
	return namespaceName.MatchString(p.Namespace) && namespaceName.MatchString(p.Name) && (p.UID == "" || name.MatchString(p.UID)) && name.MatchString(p.HostID) && name.MatchString(p.BrokerEpoch) && validPolicyDigest(p.PodPolicySHA256)
}
func (a *ExternalAttestor) BoundaryTerminated(ctx context.Context, p PodReceipt) (bool, error) {
	if !validBoundary(p) {
		return false, ErrAttestation
	}
	id, e := randomID()
	if e != nil {
		return false, ErrAttestation
	}
	q := AttestationRequest{Contract: AttestorContract, RequestID: id, QualificationSHA256: a.config.QualificationSHA256, Boundary: p}
	data, _ := json.Marshal(q)
	data, e = a.request(ctx, "/v1/java-boundary/attest", data)
	if e != nil {
		return false, e
	}
	var out AttestationResponse
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || out.Contract != q.Contract || out.RequestID != q.RequestID || out.QualificationSHA256 != q.QualificationSHA256 || out.Boundary != q.Boundary {
		return false, ErrAttestation
	}
	if out.Result == "unproven" && out.Proof == nil {
		return false, nil
	}
	if out.Result != "terminated" || !validBoundaryProof(out.Proof, a.now()) || ctx.Err() != nil {
		return false, ErrAttestation
	}
	return true, nil
}

// NeverStarted is a distinct proof operation. It requires the attestor to
// close admission for the immutable creation intent, establish from complete
// runtime history that no container or descendant ever started, and prove the
// exact pod object has been removed. No UID means the attestor must discover
// and clean up any object before returning positive proof.
func (a *ExternalAttestor) NeverStarted(ctx context.Context, intent UnstartedPod) (bool, error) {
	if !validUnstartedPod(intent) {
		return false, ErrAttestation
	}
	id, e := randomID()
	if e != nil {
		return false, ErrAttestation
	}
	q := NoStartAttestationRequest{Contract: NoStartAttestorContract, RequestID: id, QualificationSHA256: a.config.QualificationSHA256, Intent: intent}
	data, _ := json.Marshal(q)
	data, e = a.request(ctx, "/v1/java-boundary/attest-never-started", data)
	if e != nil {
		return false, e
	}
	var out NoStartAttestationResponse
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || out.Contract != q.Contract || out.RequestID != q.RequestID || out.QualificationSHA256 != q.QualificationSHA256 || out.Intent != q.Intent {
		return false, ErrAttestation
	}
	if out.Result == "unproven" && out.Proof == nil {
		return false, nil
	}
	if out.Result != "never_started" || !validNoStartProof(out.Proof, a.now()) || ctx.Err() != nil {
		return false, ErrAttestation
	}
	return true, nil
}

func (a *ExternalAttestor) request(ctx context.Context, path string, data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, a.config.Timeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.config.Endpoint, "/")+path, bytes.NewReader(data))
	if e != nil {
		return nil, ErrAttestation
	}
	req.Header.Set("Content-Type", "application/json")
	response, e := a.http.Do(req)
	if e != nil {
		return nil, ErrAttestation
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrAttestation
	}
	data, e = io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if e != nil || len(data) > 64<<10 {
		return nil, ErrAttestation
	}
	if ctx.Err() != nil {
		return nil, ErrAttestation
	}
	return data, nil
}
func validBoundaryProof(p *BoundaryProof, now time.Time) bool {
	if !validFreshProof(p, now) || p.Scope != "pod_container_descendants" || p.CreationFenceID != "" {
		return false
	}
	switch p.Kind {
	case "runtime_boundary":
		return p.NodeUID == "" && p.MachineID == "" && p.MachineUID == "" && p.FenceID == ""
	case "infrastructure_fence":
		if !name.MatchString(p.NodeUID) || !name.MatchString(p.FenceID) || !name.MatchString(p.MachineUID) || p.MachineID == "" || len(p.MachineID) > 2048 {
			return false
		}
		// Provider resource IDs may contain slashes, but never whitespace/control
		// characters or credentials encoded as a URL userinfo/query/fragment.
		for _, r := range p.MachineID {
			if r < 33 || r > 126 {
				return false
			}
		}
		return !strings.ContainsAny(p.MachineID, "?#@")
	}
	return false
}

func validNoStartProof(p *BoundaryProof, now time.Time) bool {
	return validFreshProof(p, now) && p.Kind == "creation_fence" && p.Scope == "pod_never_started_object_removed" && name.MatchString(p.CreationFenceID) && p.NodeUID == "" && p.MachineID == "" && p.MachineUID == "" && p.FenceID == ""
}

func validFreshProof(p *BoundaryProof, now time.Time) bool {
	return p != nil && validPolicyDigest(p.EvidenceSHA256) && !p.ObservedAt.IsZero() && !p.ObservedAt.After(now.Add(2*time.Second)) && !p.ObservedAt.Before(now.Add(-30*time.Second)) && p.ExpiresAt.After(now) && !p.ExpiresAt.After(p.ObservedAt.Add(30*time.Second)) && p.ExpiresAt.After(p.ObservedAt)
}
