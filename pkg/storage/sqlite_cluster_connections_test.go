package storage

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newClusterTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func aksConnection(id, name string) *ClusterConnection {
	return &ClusterConnection{
		ID:                     id,
		Name:                   name,
		Kind:                   ClusterKindAKS,
		APIServerURL:           "https://apex-aks.hcp.eastus.azmk8s.io",
		Namespace:              "apex",
		GeneratorStatefulSet:   "apexload-generator",
		CACertificatePEM:       "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
		TLSMode:                ClusterTLSCustomCA,
		AuthMode:               ClusterAuthAzureServicePrincipal,
		AzureTenantID:          "tenant-1",
		AzureClientID:          "client-1",
		ClientSecretCiphertext: "cipher-blob",
		ClientSecretWrappedKey: `{"wrapped":"key"}`,
		Labels:                 map[string]string{"env": "perf"},
	}
}

func TestClusterConnectionRoundTrip(t *testing.T) {
	store := newClusterTestStore(t)
	conn := aksConnection("cluster-1", "eastus-perf")
	if err := store.CreateClusterConnection(conn); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}

	loaded, err := store.GetClusterConnection("cluster-1")
	if err != nil {
		t.Fatalf("GetClusterConnection: %v", err)
	}
	if loaded.Name != "eastus-perf" || loaded.Kind != ClusterKindAKS {
		t.Fatalf("loaded = %+v, want the stored AKS connection", loaded)
	}
	if loaded.Labels["env"] != "perf" {
		t.Errorf("Labels = %v, want the labels_json round-trip", loaded.Labels)
	}
	if loaded.ClientSecretCiphertext != "cipher-blob" || loaded.ClientSecretWrappedKey != `{"wrapped":"key"}` {
		t.Errorf("credential envelope did not round-trip: %+v", loaded)
	}
	if loaded.CredentialSource() != "inline_encrypted" {
		t.Errorf("CredentialSource() = %q, want inline_encrypted", loaded.CredentialSource())
	}

	// List must project exactly the same columns as Get.
	all, err := store.ListClusterConnections()
	if err != nil {
		t.Fatalf("ListClusterConnections: %v", err)
	}
	if len(all) != 1 || all[0].ClientSecretCiphertext != loaded.ClientSecretCiphertext ||
		all[0].APIServerURL != loaded.APIServerURL {
		t.Fatalf("List and Get projections drifted: %+v vs %+v", all, loaded)
	}
}

// TestClusterConnectionNeverSerializesCredentials is the read-boundary
// guarantee: no handler can leak the envelope by marshaling the row.
func TestClusterConnectionNeverSerializesCredentials(t *testing.T) {
	conn := aksConnection("cluster-1", "eastus-perf")
	conn.StaticTokenCiphertext = "static-cipher"
	conn.StaticTokenWrappedKey = "static-key"

	encoded, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, secret := range []string{"cipher-blob", `{"wrapped":"key"}`, "static-cipher", "static-key"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("serialized connection leaked %q: %s", secret, encoded)
		}
	}
}

func TestUpdateClusterConnectionCASRejectsStaleWriteWithoutMutating(t *testing.T) {
	store := newClusterTestStore(t)
	conn := aksConnection("cluster-1", "eastus-perf")
	if err := store.CreateClusterConnection(conn); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}
	stale := conn.UpdatedAt

	// A first writer wins.
	first, _ := store.GetClusterConnection("cluster-1")
	first.Description = "first writer"
	ok, err := store.UpdateClusterConnectionIfUnchanged(first, stale)
	if err != nil || !ok {
		t.Fatalf("first update = (%v, %v), want (true, nil)", ok, err)
	}

	// The second writer holds the now-stale timestamp.
	second, _ := store.GetClusterConnection("cluster-1")
	second.Description = "second writer"
	ok, err = store.UpdateClusterConnectionIfUnchanged(second, stale)
	if err != nil {
		t.Fatalf("stale update error = %v, want nil", err)
	}
	if ok {
		t.Fatal("stale update reported success; the CAS check must reject it")
	}
	after, _ := store.GetClusterConnection("cluster-1")
	if after.Description != "first writer" {
		t.Fatalf("Description = %q after a rejected CAS write; nothing should have been written", after.Description)
	}
}

// TestUpdateClusterConnectionPreservesCarriedForwardCiphertext pins the
// secret-preservation contract: an edit that does not resubmit the secret must
// leave the stored envelope byte-identical.
func TestUpdateClusterConnectionPreservesCarriedForwardCiphertext(t *testing.T) {
	store := newClusterTestStore(t)
	conn := aksConnection("cluster-1", "eastus-perf")
	if err := store.CreateClusterConnection(conn); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}

	// Simulate the handler's carry-forward: load the row, edit a non-secret
	// field, and write back without touching the envelope.
	loaded, _ := store.GetClusterConnection("cluster-1")
	loaded.Description = "renamed"
	ok, err := store.UpdateClusterConnectionIfUnchanged(loaded, loaded.UpdatedAt)
	if err != nil || !ok {
		t.Fatalf("update = (%v, %v), want (true, nil)", ok, err)
	}

	after, _ := store.GetClusterConnection("cluster-1")
	if after.ClientSecretCiphertext != "cipher-blob" || after.ClientSecretWrappedKey != `{"wrapped":"key"}` {
		t.Fatalf("credential envelope changed on a secret-less update: %+v", after)
	}
}

func TestDeleteClusterConnectionRefusesWhileReferenced(t *testing.T) {
	store := newClusterTestStore(t)
	if err := store.CreateClusterConnection(aksConnection("cluster-1", "eastus-perf")); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}
	pool := &FleetPool{ID: "pool-1", Name: "gen-pool", CloudProvider: "kubernetes", ClusterID: "cluster-1"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}

	// Refusing rather than cascading is what stops an orphaned pool from
	// falling back to the controller's own cluster on its next scale.
	deleted, err := store.DeleteClusterConnectionIfUnreferenced("cluster-1")
	if err != nil {
		t.Fatalf("delete error = %v, want nil", err)
	}
	if deleted {
		t.Fatal("delete succeeded while a fleet pool still referenced the connection")
	}
	referencing, err := store.ListFleetPoolsForCluster("cluster-1")
	if err != nil {
		t.Fatalf("ListFleetPoolsForCluster: %v", err)
	}
	if len(referencing) != 1 || referencing[0].Name != "gen-pool" {
		t.Fatalf("ListFleetPoolsForCluster = %+v, want the referencing pool", referencing)
	}

	// Repointing the pool releases the connection.
	pool.ClusterID = ""
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	deleted, err = store.DeleteClusterConnectionIfUnreferenced("cluster-1")
	if err != nil || !deleted {
		t.Fatalf("delete after repointing = (%v, %v), want (true, nil)", deleted, err)
	}
}

func TestUpdateClusterConnectionHealthTouchesOnlyProbeColumns(t *testing.T) {
	store := newClusterTestStore(t)
	conn := aksConnection("cluster-1", "eastus-perf")
	conn.Description = "administrator note"
	if err := store.CreateClusterConnection(conn); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}

	checkedAt := time.Now().UTC().Truncate(time.Second)
	if err := store.UpdateClusterConnectionHealth("cluster-1", "degraded", "nodes forbidden", checkedAt); err != nil {
		t.Fatalf("UpdateClusterConnectionHealth: %v", err)
	}
	after, _ := store.GetClusterConnection("cluster-1")
	if after.LastCheckStatus != "degraded" || after.LastCheckMessage != "nodes forbidden" {
		t.Fatalf("health fields = (%q, %q), want the probe result", after.LastCheckStatus, after.LastCheckMessage)
	}
	if after.LastCheckedAt == nil || !after.LastCheckedAt.Equal(checkedAt) {
		t.Fatalf("LastCheckedAt = %v, want %v", after.LastCheckedAt, checkedAt)
	}
	if after.Description != "administrator note" {
		t.Fatalf("Description = %q; a health write must not disturb administrator fields", after.Description)
	}
}

func TestCreateClusterConnectionRejectsInvalidShapes(t *testing.T) {
	store := newClusterTestStore(t)
	cases := []struct {
		name    string
		mutate  func(*ClusterConnection)
		wantErr string
	}{
		{"plaintext endpoint", func(c *ClusterConnection) { c.APIServerURL = "http://cluster.example" }, "must use https"},
		{"missing CA", func(c *ClusterConnection) { c.CACertificatePEM = "" }, "CA certificate is required"},
		{"non-PEM CA", func(c *ClusterConnection) { c.CACertificatePEM = "not a certificate" }, "must be PEM-encoded"},
		{"missing tenant", func(c *ClusterConnection) { c.AzureTenantID = "" }, "tenant ID and client ID are required"},
		{"missing namespace", func(c *ClusterConnection) { c.Namespace = "" }, "namespace is required"},
		{"both credential sources", func(c *ClusterConnection) { c.ClientSecretEnvVar = "AKS_SECRET" }, "cannot use both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := aksConnection("cluster-invalid", "invalid")
			tc.mutate(conn)
			err := store.CreateClusterConnection(conn)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CreateClusterConnection error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestFleetPoolClusterAndAllocationRoundTrip(t *testing.T) {
	store := newClusterTestStore(t)
	if err := store.CreateClusterConnection(aksConnection("cluster-1", "eastus-perf")); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}
	allocation := &PoolAllocation{
		CPURequest:    "1",
		CPULimit:      "2",
		MemoryRequest: "2Gi",
		MemoryLimit:   "4Gi",
		NodePool:      "loadgen",
		Zone:          "eastus-2",
		NodeSelector:  map[string]string{"disktype": "ssd"},
		Tolerations:   []Toleration{{Key: "apexload/loadgen", Operator: "Equal", Value: "true", Effect: "NoSchedule"}},
	}
	pool := &FleetPool{
		ID: "pool-1", Name: "gen-pool", CloudProvider: "kubernetes",
		ClusterID: "cluster-1", Allocation: allocation,
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}

	loaded, err := store.GetFleetPool("pool-1")
	if err != nil {
		t.Fatalf("GetFleetPool: %v", err)
	}
	if loaded.ClusterID != "cluster-1" {
		t.Errorf("ClusterID = %q, want cluster-1", loaded.ClusterID)
	}
	if loaded.Allocation == nil || loaded.Allocation.CPULimit != "2" || loaded.Allocation.Zone != "eastus-2" {
		t.Fatalf("Allocation = %+v, want the stored allocation", loaded.Allocation)
	}
	if len(loaded.Allocation.Tolerations) != 1 || loaded.Allocation.Tolerations[0].Effect != "NoSchedule" {
		t.Errorf("Tolerations = %+v, want the stored toleration", loaded.Allocation.Tolerations)
	}

	// List must carry the same fields as Get.
	pools, err := store.ListFleetPools(0, 0)
	if err != nil {
		t.Fatalf("ListFleetPools: %v", err)
	}
	if len(pools) != 1 || pools[0].ClusterID != "cluster-1" || pools[0].Allocation == nil ||
		pools[0].Allocation.NodePool != "loadgen" {
		t.Fatalf("List and Get projections drifted: %+v", pools)
	}
}

// TestExistingFleetPoolsDefaultToNoClusterBinding is the backward-compatibility
// guarantee for the column migration: a pool created without cluster/allocation
// fields keeps the empty binding that means "use the env-configured
// provisioner".
func TestExistingFleetPoolsDefaultToNoClusterBinding(t *testing.T) {
	store := newClusterTestStore(t)
	if err := store.SaveFleetPool(&FleetPool{ID: "pool-legacy", Name: "legacy", CloudProvider: "static"}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	loaded, err := store.GetFleetPool("pool-legacy")
	if err != nil {
		t.Fatalf("GetFleetPool: %v", err)
	}
	if loaded.ClusterID != "" {
		t.Errorf("ClusterID = %q, want empty for a pool with no cluster binding", loaded.ClusterID)
	}
	if loaded.Allocation != nil {
		t.Errorf("Allocation = %+v, want nil for a pool with no allocation", loaded.Allocation)
	}
}

func TestPoolAllocationValidate(t *testing.T) {
	cases := []struct {
		name       string
		allocation *PoolAllocation
		wantErr    string
	}{
		{"empty is valid", &PoolAllocation{}, ""},
		{"nil is valid", nil, ""},
		{
			"reserved agentpool selector",
			&PoolAllocation{NodeSelector: map[string]string{NodeSelectorAgentPool: "loadgen"}},
			"use the node pool field",
		},
		{
			"reserved zone selector",
			&PoolAllocation{NodeSelector: map[string]string{NodeSelectorZone: "eastus-2"}},
			"use the zone field",
		},
		{
			"selector without a value",
			&PoolAllocation{NodeSelector: map[string]string{"disktype": ""}},
			"must have a value",
		},
		{
			"Exists toleration with a value",
			&PoolAllocation{Tolerations: []Toleration{{Key: "k", Operator: "Exists", Value: "v"}}},
			"must not set a value",
		},
		{
			"Equal toleration without a key",
			&PoolAllocation{Tolerations: []Toleration{{Operator: "Equal", Value: "v"}}},
			"requires a key",
		},
		{
			"unsupported effect",
			&PoolAllocation{Tolerations: []Toleration{{Key: "k", Operator: "Exists", Effect: "Evict"}}},
			"unsupported effect",
		},
		{
			"valid placement",
			&PoolAllocation{NodePool: "loadgen", Zone: "eastus-2",
				NodeSelector: map[string]string{"disktype": "ssd"},
				Tolerations:  []Toleration{{Key: "k", Operator: "Exists", Effect: "NoSchedule"}}},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.allocation.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestEffectiveNodeSelectorMergesDedicatedFields(t *testing.T) {
	allocation := &PoolAllocation{
		NodePool:     "loadgen",
		Zone:         "eastus-2",
		NodeSelector: map[string]string{"disktype": "ssd"},
	}
	selector := allocation.EffectiveNodeSelector()
	want := map[string]string{
		NodeSelectorAgentPool: "loadgen",
		NodeSelectorZone:      "eastus-2",
		"disktype":            "ssd",
	}
	for key, value := range want {
		if selector[key] != value {
			t.Errorf("selector[%q] = %q, want %q", key, selector[key], value)
		}
	}
	if got := allocation.SelectorKeys(); len(got) != 3 || got[0] != NodeSelectorAgentPool {
		t.Errorf("SelectorKeys() = %v, want a stable sorted key list", got)
	}
	if (&PoolAllocation{}).EffectiveNodeSelector() != nil {
		t.Error("an empty allocation must produce a nil selector, not an empty map")
	}
}

func TestMalformedAllocationJSONFailsSoft(t *testing.T) {
	// A corrupt blob must not break listing every pool.
	if got := unmarshalAllocation("{not json"); got != nil {
		t.Fatalf("unmarshalAllocation(malformed) = %+v, want nil", got)
	}
	if got := unmarshalAllocation(""); got != nil {
		t.Fatalf("unmarshalAllocation(empty) = %+v, want nil", got)
	}
	if !errors.Is(ErrClusterConnectionReferenced, ErrClusterConnectionReferenced) {
		t.Fatal("sentinel error identity broken")
	}
}
