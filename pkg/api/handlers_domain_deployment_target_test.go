package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// These drive the four deployment-target endpoints through the real mux, which
// nothing else did: the resolver and storage tests cover the layers underneath,
// and the route catalogs cover the authorization spec, but the handlers
// themselves — status codes, response bodies, inheritance — were untested.

func newDeploymentTargetServer(t *testing.T) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	for _, domain := range []struct{ id, name string }{{"domain-1", "Payments"}, {"domain-2", "Search"}} {
		if err := store.SaveDomain(&storage.Domain{
			ID: domain.id, Name: domain.name, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("SaveDomain(%s): %v", domain.id, err)
		}
	}
	for _, conn := range []*storage.ClusterConnection{
		{
			ID: "cluster-eks", Name: "us-east-1-perf", Kind: storage.ClusterKindEKS,
			APIServerURL: "https://ABCDEF.gr7.us-east-1.eks.amazonaws.com", Namespace: "apex",
			TLSMode: storage.ClusterTLSSystemRoots, AuthMode: storage.ClusterAuthAWSIAM,
			AWSRegion: "us-east-1", AWSClusterName: "prod-perf",
			AWSCredentialMode: storage.AWSCredentialModeAmbient,
		},
		{
			ID: "cluster-aks", Name: "eastus-perf", Kind: storage.ClusterKindAKS,
			APIServerURL: "https://apex.hcp.eastus.azmk8s.io", Namespace: "apex",
			TLSMode: storage.ClusterTLSSystemRoots, AuthMode: storage.ClusterAuthStaticToken,
		},
	} {
		if err := store.CreateClusterConnection(conn); err != nil {
			t.Fatalf("CreateClusterConnection(%s): %v", conn.ID, err)
		}
	}
	return NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"}), store
}

func deploymentTargetRequest(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	return rec
}

func decodeTargetResponse(t *testing.T, rec *httptest.ResponseRecorder) DomainDeploymentTargetResponse {
	t.Helper()
	var response DomainDeploymentTargetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return response
}

// TestUnconfiguredDomainReturns200NotFound pins the deliberate choice: having no
// constraint is a normal state, and a 404 would make the dashboard render it as
// an error on every domain that has not been configured yet.
func TestUnconfiguredDomainReturns200NotFound(t *testing.T) {
	server, _ := newDeploymentTargetServer(t)

	rec := deploymentTargetRequest(t, server, http.MethodGet, "/api/v1/system/deployment-targets/domain-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET unconfigured domain = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	response := decodeTargetResponse(t, rec)
	if response.Configured {
		t.Error("configured = true for a domain with no target")
	}
	if response.Provider != "" {
		t.Errorf("provider = %q, want empty", response.Provider)
	}
	if response.DomainName != "Payments" {
		t.Errorf("domain_name = %q, want the domain's name", response.DomainName)
	}

	// A domain that genuinely does not exist is still a 404.
	rec = deploymentTargetRequest(t, server, http.MethodGet, "/api/v1/system/deployment-targets/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown domain = %d, want 404", rec.Code)
	}
}

func TestDeploymentTargetLifecycleOverHTTP(t *testing.T) {
	server, _ := newDeploymentTargetServer(t)

	// A first write must omit the concurrency token.
	rec := deploymentTargetRequest(t, server, http.MethodPut, "/api/v1/system/deployment-targets/domain-1",
		map[string]any{"provider": "aws_eks", "default_cluster_id": "cluster-eks"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	created := decodeTargetResponse(t, rec)
	if !created.Configured || created.Provider != storage.DeploymentProviderAWSEKS {
		t.Fatalf("created = %+v", created)
	}
	if created.DefaultClusterName != "us-east-1-perf" {
		t.Errorf("default_cluster_name = %q, want the cluster's name resolved for display", created.DefaultClusterName)
	}

	// A stale token is refused.
	rec = deploymentTargetRequest(t, server, http.MethodPut, "/api/v1/system/deployment-targets/domain-1",
		map[string]any{"provider": "kubernetes", "updated_at": created.UpdatedAt.Add(-time.Hour)})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale PUT = %d, want 409", rec.Code)
	}

	// The current token succeeds.
	rec = deploymentTargetRequest(t, server, http.MethodPut, "/api/v1/system/deployment-targets/domain-1",
		map[string]any{"provider": "kubernetes", "updated_at": created.UpdatedAt})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT with the current token = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// Listing includes it, with the selectable vocabulary the UI renders.
	rec = deploymentTargetRequest(t, server, http.MethodGet, "/api/v1/system/deployment-targets", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET list = %d, want 200", rec.Code)
	}
	var list DomainDeploymentTargetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Targets) != 1 {
		t.Fatalf("list = %+v, want exactly one target", list)
	}
	if len(list.SelectableProviders) != 5 {
		t.Errorf("selectable_providers = %v, want Kubernetes, AKS, EKS, Container Apps, and ECS Fargate", list.SelectableProviders)
	}

	// Clearing is unconditional and leaves the domain unconstrained.
	rec = deploymentTargetRequest(t, server, http.MethodDelete, "/api/v1/system/deployment-targets/domain-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if decodeTargetResponse(t, rec).Configured {
		t.Error("configured = true after clearing the constraint")
	}
}

func TestDeploymentTargetRejectsAnUnusableConfiguration(t *testing.T) {
	server, _ := newDeploymentTargetServer(t)

	for _, tc := range []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "an unselectable provider",
			// gcp_gke is recognized for legacy pool rows but no cluster kind
			// maps to it, so a domain constrained to it could admit no pool.
			body: map[string]any{"provider": "gcp_gke"},
			want: http.StatusBadRequest,
		},
		{name: "no provider", body: map[string]any{}, want: http.StatusBadRequest},
		{name: "nonsense provider", body: map[string]any{"provider": "nonsense"}, want: http.StatusBadRequest},
		{
			name: "a default cluster that contradicts the provider",
			// Otherwise every new pool inherits an immediately-invalid binding.
			body: map[string]any{"provider": "aws_eks", "default_cluster_id": "cluster-aks"},
			want: http.StatusBadRequest,
		},
		{
			name: "a default cluster that does not exist",
			body: map[string]any{"provider": "aws_eks", "default_cluster_id": "cluster-gone"},
			want: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := deploymentTargetRequest(t, server, http.MethodPut,
				"/api/v1/system/deployment-targets/domain-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("PUT = %d, want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// TestRetargetConflict409BodyMatchesWhatTheDashboardParses is the server half of
// the contract the dashboard's conflictingPoolsFromError depends on.
//
// writeJSONError emits {error, detail}, apiClient stores that whole body on
// ApiError.payload, and the card reads payload.detail.conflicting_pools. Nothing
// verified the SERVER end of that shape — a rename here would silently empty the
// conflict list in the UI with every test still green.
func TestRetargetConflict409BodyMatchesWhatTheDashboardParses(t *testing.T) {
	server, store := newDeploymentTargetServer(t)

	// A pool scoped to the domain and bound to an AKS cluster.
	if err := store.CreateFleetPoolWithMembers(&storage.FleetPool{
		ID: "pool-1", Name: "east-http", CloudProvider: "azure_aks", Status: "active",
		DomainID: "domain-1", ClusterID: "cluster-aks",
	}, nil); err != nil {
		t.Fatalf("CreateFleetPoolWithMembers: %v", err)
	}

	rec := deploymentTargetRequest(t, server, http.MethodPut,
		"/api/v1/system/deployment-targets/domain-1", map[string]any{"provider": "aws_eks"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("retarget with a conflicting pool = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}

	// Decode into UNTYPED maps, deliberately.
	//
	// Decoding into DomainDeploymentTargetConflictResponse would be tautological
	// for the field names: renaming a struct tag would rename both the producer
	// and this assertion, and the test would still pass while the dashboard —
	// which hardcodes these keys in TypeScript — silently broke. The literal
	// strings below are the contract.
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 409 body: %v (body=%s)", err, rec.Body.String())
	}
	if message, _ := body["error"].(string); message == "" {
		t.Errorf(`409 body has no "error" key; apiClient reads the message from it: %s`, rec.Body.String())
	}
	detail, ok := body["detail"].(map[string]any)
	if !ok {
		t.Fatalf(`409 body has no "detail" object; ApiError.payload.detail is where the card looks: %s`, rec.Body.String())
	}
	if provider, _ := detail["provider"].(string); provider != storage.DeploymentProviderAWSEKS {
		t.Errorf(`detail.provider = %q, want %q`, provider, storage.DeploymentProviderAWSEKS)
	}
	pools, ok := detail["conflicting_pools"].([]any)
	if !ok {
		t.Fatalf(`detail has no "conflicting_pools" array; deploymentTargetModel reads exactly that key: %s`, rec.Body.String())
	}
	if len(pools) != 1 {
		t.Fatalf("conflicting_pools = %v, want exactly the one conflicting pool", pools)
	}
	conflict, ok := pools[0].(map[string]any)
	if !ok {
		t.Fatalf("conflicting_pools[0] is not an object: %v", pools[0])
	}
	for key, want := range map[string]string{"pool_id": "pool-1", "pool_name": "east-http"} {
		if got, _ := conflict[key].(string); got != want {
			t.Errorf("conflicting_pools[0].%s = %q, want %q (the UI renders it)", key, got, want)
		}
	}
	if reason, _ := conflict["reason"].(string); reason == "" {
		t.Error(`conflicting_pools[0].reason is empty; the UI renders it verbatim`)
	}

	// The refusal must not have partially applied.
	rec = deploymentTargetRequest(t, server, http.MethodGet, "/api/v1/system/deployment-targets/domain-1", nil)
	if decodeTargetResponse(t, rec).Configured {
		t.Fatal("a refused retarget still wrote the target")
	}
}

// TestScopedPoolInheritsTheDomainDefaultCluster proves the inheritance half of
// "default and hard constraint" actually works through the API.
func TestScopedPoolInheritsTheDomainDefaultCluster(t *testing.T) {
	server, _ := newDeploymentTargetServer(t)

	rec := deploymentTargetRequest(t, server, http.MethodPut, "/api/v1/system/deployment-targets/domain-1",
		map[string]any{"provider": "aws_eks", "default_cluster_id": "cluster-eks"})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT target = %d (body=%s)", rec.Code, rec.Body.String())
	}

	// A pool that names no cluster and no provider.
	rec = deploymentTargetRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "inherits", "domain_id": "domain-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pool = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var pool storage.FleetPool
	if err := json.Unmarshal(rec.Body.Bytes(), &pool); err != nil {
		t.Fatalf("decode pool: %v", err)
	}
	if pool.ClusterID != "cluster-eks" {
		t.Errorf("cluster_id = %q, want the domain's default cluster to be inherited", pool.ClusterID)
	}
	if pool.CloudProvider != storage.DeploymentProviderAWSEKS {
		t.Errorf("cloud_provider = %q, want it derived from the domain target", pool.CloudProvider)
	}

	// And a pool bound to the wrong cloud is refused.
	rec = deploymentTargetRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "wrong-cloud", "domain_id": "domain-1", "cluster_id": "cluster-aks"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("create pool bound to another cloud = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestUnscopedPoolCreationIsUnaffected is the backward-compatibility guarantee
// at the HTTP layer: a pool with no domain keeps free-text cloud_provider and
// needs no cluster binding, exactly as before domain targets existed.
func TestUnscopedPoolCreationIsUnaffected(t *testing.T) {
	server, _ := newDeploymentTargetServer(t)

	// Constrain every domain, to prove an unscoped pool ignores all of it.
	for _, domain := range []string{"domain-1", "domain-2"} {
		rec := deploymentTargetRequest(t, server, http.MethodPut,
			"/api/v1/system/deployment-targets/"+domain,
			map[string]any{"provider": "aws_eks", "default_cluster_id": "cluster-eks"})
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d (body=%s)", domain, rec.Code, rec.Body.String())
		}
	}

	rec := deploymentTargetRequest(t, server, http.MethodPost, "/api/v1/fleet/pools",
		map[string]any{"name": "legacy", "cloud_provider": "static"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create unscoped pool = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var pool storage.FleetPool
	if err := json.Unmarshal(rec.Body.Bytes(), &pool); err != nil {
		t.Fatalf("decode pool: %v", err)
	}
	if pool.DomainID != "" {
		t.Errorf("domain_id = %q, want an unscoped pool", pool.DomainID)
	}
	if pool.CloudProvider != "static" {
		t.Errorf("cloud_provider = %q, want the free-text value preserved", pool.CloudProvider)
	}
	if pool.ClusterID != "" {
		t.Errorf("cluster_id = %q, want no binding inherited by an unscoped pool", pool.ClusterID)
	}
}

// TestDomainDeleteRefusesWhilePoolsAreScopedToIt pins the refusal that keeps a
// pool from being orphaned onto a domain that no longer exists.
func TestDomainDeleteRefusesWhilePoolsAreScopedToIt(t *testing.T) {
	server, store := newDeploymentTargetServer(t)
	if err := store.CreateFleetPoolWithMembers(&storage.FleetPool{
		ID: "pool-1", Name: "east-http", CloudProvider: "aws_eks", Status: "active",
		DomainID: "domain-1", ClusterID: "cluster-eks",
	}, nil); err != nil {
		t.Fatalf("CreateFleetPoolWithMembers: %v", err)
	}

	rec := deploymentTargetRequest(t, server, http.MethodDelete, "/api/v1/domains/domain-1", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE domain with scoped pools = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("east-http")) {
		t.Errorf("409 body does not name the blocking pool: %s", rec.Body.String())
	}
}
