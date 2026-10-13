package api

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// recordingClusterStore is a stubClusterStore that remembers health writes.
type recordingClusterStore struct {
	stubClusterStore
	healthWrites int
}

func (s *recordingClusterStore) UpdateClusterConnectionHealth(string, string, string, time.Time) error {
	s.healthWrites++
	return nil
}

func draftTestServer(store storage.ClusterConnectionStore) *Server {
	return &Server{
		clusterStore:   store,
		clusterClients: newClusterClientCache(nil, nil),
		clusterProbes:  &clusterProbeThrottle{lastRun: make(map[string]time.Time)},
	}
}

func postClusterDraftTest(t *testing.T, s *Server, body ClusterConnectionTestRequest) (*httptest.ResponseRecorder, ClusterTestResponse) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	s.handleTestClusterConnectionDraft(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/system/clusters/test", bytes.NewReader(payload)))
	var result ClusterTestResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return recorder, result
}

func probeByName(result ClusterTestResponse, name string) (ClusterProbe, bool) {
	for _, probe := range result.Probes {
		if probe.Probe == name {
			return probe, true
		}
	}
	return ClusterProbe{}, false
}

// TestClusterDraftTestProbesTheFormNotTheStoredConnection: an edit is tested
// as the form describes it, before it is saved. The stored connection points
// nowhere; the edit points at a reachable API server, and the probe must
// reach that one. Nothing is stored, not even the health result.
func TestClusterDraftTestProbesTheFormNotTheStoredConnection(t *testing.T) {
	const tokenEnv = "APEXLOAD_TEST_DRAFT_KUBE_TOKEN"
	t.Setenv(tokenEnv, "draft-token")
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer draft-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"gitVersion":"v1.30.2"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer api.Close()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}))

	stored := &storage.ClusterConnection{
		ID: "cluster-lab", Name: "Lab cluster", Kind: storage.ClusterKindGeneric, Namespace: "apex",
		APIServerURL: "https://127.0.0.1:1", TLSMode: storage.ClusterTLSCustomCA, CACertificatePEM: caPEM,
		AuthMode: storage.ClusterAuthStaticToken, ClientSecretEnvVar: tokenEnv, Status: "active",
	}
	store := &recordingClusterStore{stubClusterStore: stubClusterStore{connections: map[string]*storage.ClusterConnection{stored.ID: stored}}}
	s := draftTestServer(store)

	envVar := tokenEnv
	request := ClusterConnectionRequest{
		Name: "Lab cluster", Kind: storage.ClusterKindGeneric, Namespace: "apex",
		APIServerURL: api.URL, TLSMode: storage.ClusterTLSCustomCA, CACertificatePEM: caPEM,
		AuthMode: storage.ClusterAuthStaticToken, ClientSecretEnvVar: &envVar,
	}
	recorder, result := postClusterDraftTest(t, s, ClusterConnectionTestRequest{ConnectionID: stored.ID, Connection: request})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	probe, ok := probeByName(result, "api_server")
	if !ok || !probe.OK || result.ServerInfo != "v1.30.2" {
		t.Fatalf("api_server probe = %+v (server %q); the draft's URL was not the one probed: %+v", probe, result.ServerInfo, result)
	}
	if stored.APIServerURL != "https://127.0.0.1:1" {
		t.Fatalf("the stored connection was changed to %q", stored.APIServerURL)
	}
	if store.healthWrites != 0 {
		t.Fatalf("a draft test wrote %d health results", store.healthWrites)
	}
	// The stored test still probes the stored URL, from a cache the draft did not fill.
	storedResult := s.probeClusterConnection(t.Context(), stored)
	if probe, ok := probeByName(storedResult, "api_server"); !ok || probe.OK || storedResult.ServerInfo != "" {
		t.Fatalf("stored connection probe = %+v; it reached a server, so it used the draft's client", storedResult)
	}

	// A new connection is tested on its own, and an invalid one is refused.
	s.clusterProbes = &clusterProbeThrottle{lastRun: make(map[string]time.Time)}
	if recorder, result := postClusterDraftTest(t, s, ClusterConnectionTestRequest{Connection: request}); recorder.Code != http.StatusOK || result.ServerInfo != "v1.30.2" {
		t.Fatalf("new draft = %d %+v", recorder.Code, result)
	}
	incomplete := request
	incomplete.Namespace = ""
	s.clusterProbes = &clusterProbeThrottle{lastRun: make(map[string]time.Time)}
	if recorder, _ := postClusterDraftTest(t, s, ClusterConnectionTestRequest{Connection: incomplete}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("incomplete draft = %d, want 400", recorder.Code)
	}
	if recorder, _ := postClusterDraftTest(t, s, ClusterConnectionTestRequest{ConnectionID: "missing", Connection: request}); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown connection = %d, want 404", recorder.Code)
	}
}

// TestClusterDraftTestKeepsTheStoredCredentialWhenTheFormLeavesItBlank: an
// edit form never shows the stored secret, so a blank credential means "the
// stored one". The probe must authenticate with that sealed credential.
func TestClusterDraftTestKeepsTheStoredCredentialWhenTheFormLeavesItBlank(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stored-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"gitVersion":"v1.31.0"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer api.Close()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}))

	wrapper := testKeyWrapper(t)
	stored := &storage.ClusterConnection{
		ID: "cluster-sealed", Name: "Sealed", Kind: storage.ClusterKindGeneric, Namespace: "apex",
		APIServerURL: "https://127.0.0.1:1", TLSMode: storage.ClusterTLSCustomCA, CACertificatePEM: caPEM,
		AuthMode: storage.ClusterAuthStaticToken, Status: "active",
	}
	ciphertext, wrapped, err := sealClusterCredential(t.Context(), wrapper, stored.ID, clusterCredentialStaticToken, "stored-token")
	if err != nil {
		t.Fatal(err)
	}
	stored.StaticTokenCiphertext, stored.StaticTokenWrappedKey = ciphertext, wrapped
	s := draftTestServer(&recordingClusterStore{stubClusterStore: stubClusterStore{connections: map[string]*storage.ClusterConnection{stored.ID: stored}}})
	s.clusterClients = newClusterClientCache(wrapper, nil)
	s.clusterSecretWrapper = wrapper

	// The form changes the URL and leaves the token blank (omitted).
	request := ClusterConnectionRequest{
		Name: "Sealed", Kind: storage.ClusterKindGeneric, Namespace: "apex",
		APIServerURL: api.URL, TLSMode: storage.ClusterTLSCustomCA, CACertificatePEM: caPEM,
		AuthMode: storage.ClusterAuthStaticToken,
	}
	recorder, result := postClusterDraftTest(t, s, ClusterConnectionTestRequest{ConnectionID: stored.ID, Connection: request})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if probe, ok := probeByName(result, "api_server"); !ok || !probe.OK || result.ServerInfo != "v1.31.0" {
		t.Fatalf("probe = %+v; the stored credential was not used for the blank form field", result)
	}
}
