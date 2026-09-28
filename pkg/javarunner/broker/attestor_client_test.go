package broker

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func externalAttestorFixture(t *testing.T, handler http.Handler) (*ExternalAttestor, *httptest.Server) {
	t.Helper()
	uri := "spiffe://apexload/java-boundary-attestor/qualified-runtime"
	ca, serverCert, brokerCert, _, roots := controlCertificatesForURI(t, uri)
	server := httptest.NewUnstartedServer(handler)
	var e error
	server.TLS, e = SupervisorTLS(serverCert, ca, "spiffe://apexload/java-broker")
	if e != nil {
		t.Fatal(e)
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	client, e := NewExternalAttestor(AttestorConfig{Endpoint: server.URL, ServerURI: uri, BrokerClientURI: "spiffe://apexload/java-broker", QualificationSHA256: strings.Repeat("a", 64), Timeout: time.Second}, &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{brokerCert}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	return client, server
}
func attestorBoundary() PodReceipt {
	return PodReceipt{Namespace: "execution", Name: "java-host", UID: "pod-uid", ContainerID: "containerd://exact", HostID: "host", BrokerEpoch: "epoch"}
}
func proofResponse(q AttestationRequest) AttestationResponse {
	now := time.Now()
	return AttestationResponse{Contract: q.Contract, RequestID: q.RequestID, QualificationSHA256: q.QualificationSHA256, Boundary: q.Boundary, Result: "terminated", Proof: &BoundaryProof{Kind: "runtime_boundary", Scope: "pod_container_descendants", EvidenceSHA256: strings.Repeat("b", 64), ObservedAt: now, ExpiresAt: now.Add(10 * time.Second)}}
}
func TestExternalAttestorRequiresExactFreshWholeBoundaryProof(t *testing.T) {
	for _, kind := range []string{"runtime_boundary", "infrastructure_fence", "unproven", "wrong_pod", "wrong_container", "wrong_epoch", "wrong_request", "wrong_profile", "only_jvm", "expired", "future", "old", "invalid_digest", "no_fence_id", "no_machine", "no_machine_uid", "node_name_only", "missing_proof", "unknown_kind", "redirect", "oversize", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			client, _ := externalAttestorFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/java-boundary/attest" || r.Header.Get("Authorization") != "" || r.Method != "POST" {
					t.Error("unexpected transport")
				}
				var q AttestationRequest
				if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
					t.Error(e)
					w.WriteHeader(400)
					return
				}
				out := proofResponse(q)
				switch kind {
				case "infrastructure_fence", "no_fence_id", "no_machine", "no_machine_uid", "node_name_only":
					out.Proof.Kind = "infrastructure_fence"
					out.Proof.NodeUID = "immutable-node-uid"
					out.Proof.MachineID = "/subscriptions/test/vmss/instance"
					out.Proof.MachineUID = "immutable-vm-uid"
					out.Proof.FenceID = "fence-proof-1"
					if kind == "no_fence_id" {
						out.Proof.FenceID = ""
					}
					if kind == "no_machine" {
						out.Proof.MachineID = ""
					}
					if kind == "no_machine_uid" {
						out.Proof.MachineUID = ""
					}
					if kind == "node_name_only" {
						out.Proof.NodeUID = ""
					}
				case "unproven":
					out.Result = "unproven"
					out.Proof = nil
				case "wrong_pod":
					out.Boundary.UID = "replacement"
				case "wrong_container":
					out.Boundary.ContainerID = "containerd://replacement"
				case "wrong_epoch":
					out.Boundary.BrokerEpoch = "other"
				case "wrong_request":
					out.RequestID = "replayed"
				case "wrong_profile":
					out.QualificationSHA256 = strings.Repeat("c", 64)
				case "only_jvm":
					out.Proof.Scope = "root_process"
				case "expired":
					out.Proof.ExpiresAt = time.Now().Add(-time.Second)
				case "future":
					out.Proof.ObservedAt = time.Now().Add(time.Minute)
				case "old":
					out.Proof.ObservedAt = time.Now().Add(-time.Minute)
				case "invalid_digest":
					out.Proof.EvidenceSHA256 = "invalid"
				case "missing_proof":
					out.Proof = nil
				case "unknown_kind":
					out.Proof.Kind = "pod_deleted"
				case "redirect":
					w.Header().Set("Location", "https://example.invalid/leak")
					w.WriteHeader(307)
					return
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat(" ", 65537)))
					return
				}
				_ = json.NewEncoder(w).Encode(out)
				if kind == "trailing" {
					_, _ = w.Write([]byte("{}"))
				}
			}))
			proven, e := client.BoundaryTerminated(t.Context(), attestorBoundary())
			want := kind == "runtime_boundary" || kind == "infrastructure_fence"
			if proven != want || want && e != nil || kind == "unproven" && e != nil || !want && kind != "unproven" && !errors.Is(e, ErrAttestation) {
				t.Fatal(proven, e)
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected retry", calls.Load())
			}
		})
	}
}
func TestExternalAttestorChallengeCannotBeReplayed(t *testing.T) {
	var saved *AttestationResponse
	client, _ := externalAttestorFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q AttestationRequest
		_ = json.NewDecoder(r.Body).Decode(&q)
		if saved == nil {
			out := proofResponse(q)
			saved = &out
		}
		_ = json.NewEncoder(w).Encode(saved)
	}))
	if proven, e := client.BoundaryTerminated(t.Context(), attestorBoundary()); !proven || e != nil {
		t.Fatal(proven, e)
	}
	if proven, e := client.BoundaryTerminated(t.Context(), attestorBoundary()); proven || !errors.Is(e, ErrAttestation) {
		t.Fatal("replayed evidence accepted", proven, e)
	}
}
func TestExternalAttestorRejectsExecutionHostIdentity(t *testing.T) {
	ca, serverCert, brokerCert, hostCert, roots := controlCertificates(t)
	cfg := AttestorConfig{Endpoint: "https://127.0.0.1:9443", ServerURI: "spiffe://apexload/java-host/host", BrokerClientURI: "spiffe://apexload/java-broker", QualificationSHA256: strings.Repeat("a", 64), Timeout: time.Second}
	tc := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{brokerCert}}
	if _, e := NewExternalAttestor(cfg, tc); !errors.Is(e, ErrState) {
		t.Fatal("host role configured as attestor", e)
	}
	cfg.ServerURI = "spiffe://apexload/java-boundary-attestor/runtime"
	tc.Certificates = []tls.Certificate{hostCert}
	if _, e := NewExternalAttestor(cfg, tc); !errors.Is(e, ErrState) {
		t.Fatal("host obtained broker attestation credentials", e)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("host certificate reached attestor HTTP handler") }))
	server.TLS, _ = SupervisorTLS(serverCert, ca, cfg.BrokerClientURI)
	server.StartTLS()
	defer server.Close()
	tc.Certificates = []tls.Certificate{brokerCert}
	cfg.Endpoint = server.URL
	client, e := NewExternalAttestor(cfg, tc)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	if ok, e := client.BoundaryTerminated(t.Context(), attestorBoundary()); ok || !errors.Is(e, ErrAttestation) {
		t.Fatal(ok, e)
	}
}

func TestExternalAttestorReleasesMissingPodOnlyWithInfrastructureProof(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	for _, e := range []error{s.BeginPrepare(t.Context(), epoch, r.ID), s.BindPod(t.Context(), epoch, r.ID, "original-pod", "containerd://original"), s.Abort(t.Context(), epoch, r.ID, "owner_lost")} {
		if e != nil {
			t.Fatal(e)
		}
	}
	var proven atomic.Bool
	attestor, _ := externalAttestorFixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var q AttestationRequest
		if e := json.NewDecoder(req.Body).Decode(&q); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		out := proofResponse(q)
		if !proven.Load() {
			out.Result = "unproven"
			out.Proof = nil
		} else {
			out.Proof.Kind = "infrastructure_fence"
			out.Proof.NodeUID = "original-node-uid"
			out.Proof.MachineID = "/provider/vm-instance"
			out.Proof.MachineUID = "original-machine-uid"
			out.Proof.FenceID = "fence-receipt"
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	var deletes atomic.Int32
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "DELETE" {
			var body struct{ Preconditions struct{ UID string } }
			if e := json.NewDecoder(req.Body).Decode(&body); e != nil || body.Preconditions.UID != "original-pod" {
				t.Error("deletion lost exact UID")
			}
			deletes.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}, &testAttestor{})
	k.attestor = attestor
	reconciler, e := NewReconciler(s, epoch, k, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = reconciler.Step(t.Context()); !errors.Is(e, ErrCleanup) {
		t.Fatal("missing pod counted as proof", e)
	}
	_, usage, e := s.Snapshot(t.Context(), epoch)
	if e != nil || usage.Hosts != 1 || usage.MemoryBytes != 100 {
		t.Fatal(usage, e)
	}
	proven.Store(true)
	if e = reconciler.Step(t.Context()); e != nil {
		t.Fatal(e)
	}
	_, usage, e = s.Snapshot(t.Context(), epoch)
	if e != nil || usage.Hosts != 0 || usage.MemoryBytes != 0 || deletes.Load() != 2 {
		t.Fatal(usage, deletes.Load(), e)
	}
}

func TestExternalAttestorBoundsAndTimeout(t *testing.T) {
	client, _ := externalAttestorFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	config := client.http.Transport.(*http.Transport).TLSClientConfig
	for _, mutate := range []func(*AttestorConfig){func(c *AttestorConfig) { c.Timeout = 0 }, func(c *AttestorConfig) { c.Timeout = 31 * time.Second }, func(c *AttestorConfig) { c.Endpoint = "http://localhost" }, func(c *AttestorConfig) { c.Endpoint += "?token=secret" }, func(c *AttestorConfig) { c.QualificationSHA256 = "invalid" }, func(c *AttestorConfig) { c.ServerURI = c.BrokerClientURI }} {
		bad := client.config
		mutate(&bad)
		if _, e := NewExternalAttestor(bad, config); !errors.Is(e, ErrState) {
			t.Fatal("invalid attestor settings accepted", e)
		}
	}
	cfg := client.config
	cfg.Timeout = 30 * time.Millisecond
	bounded, e := NewExternalAttestor(cfg, config)
	if e != nil {
		t.Fatal(e)
	}
	defer bounded.Close()
	if ok, e := bounded.BoundaryTerminated(t.Context(), attestorBoundary()); ok || !errors.Is(e, ErrAttestation) {
		t.Fatal("timeout considered proof", ok, e)
	}
	badTLS := config.Clone()
	badTLS.RootCAs = nil
	if _, e := NewExternalAttestor(cfg, badTLS); !errors.Is(e, ErrState) {
		t.Fatal("no operator trust roots", e)
	}
	bad := attestorBoundary()
	bad.ContainerID = ""
	if ok, e := client.BoundaryTerminated(t.Context(), bad); ok || !errors.Is(e, ErrAttestation) {
		t.Fatal("unknown container considered proof", ok, e)
	}
}
