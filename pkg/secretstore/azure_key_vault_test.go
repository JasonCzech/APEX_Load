package secretstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jasonczech/apexload/pkg/storage"
)

type staticVaultCredential struct{}

func (staticVaultCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type failingMetadataReplaceStore struct {
	storage.ProjectSecretMetadataStore
	applyBeforeError bool
}

func (s *failingMetadataReplaceStore) ReplaceProjectSecretMetadata(metadata *storage.ProjectSecretMetadata) (bool, error) {
	if s.applyBeforeError {
		replaced, err := s.ProjectSecretMetadataStore.ReplaceProjectSecretMetadata(metadata)
		if err != nil || !replaced {
			return replaced, err
		}
	}
	return false, errors.New("simulated metadata commit acknowledgement failure")
}

func TestAzureKeyVaultStoreCreateFailsClosedBecauseSetSecretIsUpsert(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: "vault-project", Name: "Vault", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// A real Set Secret PUT would add a version if this externally managed
		// name already existed. Create must never reach the data-plane API.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"value": "external-value"})
	}))
	t.Cleanup(vault.Close)
	store := &AzureKeyVaultStore{
		BaseURL:    vault.URL,
		APIVersion: "7.4",
		Credential: staticVaultCredential{},
		Client:     vault.Client(),
		Metadata:   records,
	}
	if SupportsCreate(store) {
		t.Fatal("Azure Key Vault advertised atomic create support")
	}
	if _, err := store.Create(context.Background(), "vault-project", "LOGIN_PASSWORD", "must-not-version-external-value", "admin"); !errors.Is(err, ErrCreateUnsupported) {
		t.Fatalf("Create error = %v, want create unsupported", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("Create made %d Key Vault request(s), want none", requests.Load())
	}
	if _, err := records.GetProjectSecretMetadata("vault-project", "LOGIN_PASSWORD"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsupported Create left a misleading metadata claim: %v", err)
	}
}

func TestAzureKeyVaultStoreFailedReplacePreservesCleanupMetadata(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: "vault-failure", Name: "Vault", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	created, err := records.CreateProjectSecretMetadata(&storage.ProjectSecretMetadata{
		ProjectID: "vault-failure", SecretName: "TOKEN", Provider: keyVaultProvider,
		CreatedAt: now, UpdatedAt: now, CreatedBy: "admin",
	})
	if err != nil || !created {
		t.Fatalf("seed cleanup metadata = %t, %v", created, err)
	}
	vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusBadGateway)
	}))
	t.Cleanup(vault.Close)
	store := &AzureKeyVaultStore{
		BaseURL: vault.URL, APIVersion: "7.4", Credential: staticVaultCredential{},
		Client: vault.Client(), Metadata: records,
	}
	if _, err := store.Replace(context.Background(), "vault-failure", "TOKEN", "replacement-value", "admin"); err == nil {
		t.Fatal("Replace unexpectedly succeeded")
	}
	listed, err := store.List(context.Background(), "vault-failure")
	if err != nil || len(listed) != 1 || listed[0].Name != "TOKEN" || listed[0].Provider != keyVaultProvider {
		t.Fatalf("failed/ambiguous write lost administrative cleanup metadata: %#v, %v", listed, err)
	}
}

func TestAzureKeyVaultStoreAdoptsExternallyProvisionedSecretForListAndRotation(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const projectID = "vault-adoption"
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: projectID, Name: "Vault adoption", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var gets atomic.Int32
	var puts atomic.Int32
	vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			gets.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"value": "externally-provisioned-value"})
		case http.MethodPut:
			puts.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"value": "rotated-value"})
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(vault.Close)
	store := &AzureKeyVaultStore{
		BaseURL: vault.URL, APIVersion: "7.4", Credential: staticVaultCredential{},
		Client: vault.Client(), Metadata: records,
	}
	value, metadata, err := store.Get(context.Background(), projectID, "EXTERNAL_TOKEN")
	if err != nil || value != "externally-provisioned-value" || metadata.Provider != keyVaultProvider || !metadata.Writable {
		t.Fatalf("external Get = value %q metadata %#v error %v", value, metadata, err)
	}
	listed, err := store.List(context.Background(), projectID)
	if err != nil || len(listed) != 1 || listed[0].Name != "EXTERNAL_TOKEN" {
		t.Fatalf("adopted metadata list = %#v error %v", listed, err)
	}
	rotated, err := store.Replace(context.Background(), projectID, "EXTERNAL_TOKEN", "rotated-value", "admin")
	if err != nil || rotated.Name != "EXTERNAL_TOKEN" {
		t.Fatalf("external Replace = %#v error %v", rotated, err)
	}
	if gets.Load() != 1 || puts.Load() != 1 {
		t.Fatalf("Key Vault operations = GET %d PUT %d, want 1/1", gets.Load(), puts.Load())
	}
}

func TestAzureKeyVaultRotationMetadataCommitCanBeReconciledWithoutTrustingValueAlone(t *testing.T) {
	for _, test := range []struct {
		name              string
		applyMetadata     bool
		wantMetadataActor string
	}{
		{name: "metadata committed before acknowledgement loss", applyMetadata: true, wantMetadataActor: "new-admin"},
		{name: "metadata remained stale", applyMetadata: false, wantMetadataActor: "old-admin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = records.Close() })
			const projectID = "vault-metadata-reconciliation"
			now := time.Now().UTC().Add(-time.Minute)
			if err := records.SaveProject(&storage.Project{ID: projectID, Name: "Vault metadata", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			created, err := records.CreateProjectSecretMetadata(&storage.ProjectSecretMetadata{
				ProjectID: projectID, SecretName: "TOKEN", Provider: keyVaultProvider,
				CreatedAt: now, UpdatedAt: now, CreatedBy: "old-admin",
			})
			if err != nil || !created {
				t.Fatalf("seed metadata = %t, %v", created, err)
			}
			var current atomic.Value
			current.Store("old-value")
			vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPut:
					var payload struct {
						Value string `json:"value"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					current.Store(payload.Value)
					_ = json.NewEncoder(w).Encode(map[string]string{"value": payload.Value})
				case http.MethodGet:
					_ = json.NewEncoder(w).Encode(map[string]string{"value": current.Load().(string)})
				default:
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				}
			}))
			t.Cleanup(vault.Close)
			store := &AzureKeyVaultStore{
				BaseURL: vault.URL, APIVersion: "7.4", Credential: staticVaultCredential{}, Client: vault.Client(),
				Metadata: &failingMetadataReplaceStore{ProjectSecretMetadataStore: records, applyBeforeError: test.applyMetadata},
			}
			if _, err := store.Replace(context.Background(), projectID, "TOKEN", "new-value", "new-admin"); !errors.Is(err, ErrMetadataCommit) {
				t.Fatalf("Replace error = %v, want metadata commit ambiguity", err)
			}
			value, metadata, err := store.Get(context.Background(), projectID, "TOKEN")
			if err != nil || value != "new-value" || metadata.CreatedBy != test.wantMetadataActor || metadata.UpdatedAt.IsZero() {
				t.Fatalf("reconciliation probe = value %q metadata %#v error %v", value, metadata, err)
			}
		})
	}
}

func TestAzureKeyVaultManagedMetadataDiscoveryIsRequestAndDeadlineBounded(t *testing.T) {
	t.Run("request cap", func(t *testing.T) {
		var requests atomic.Int32
		vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			http.Error(w, "missing", http.StatusNotFound)
		}))
		t.Cleanup(vault.Close)
		store := &AzureKeyVaultStore{
			BaseURL: vault.URL, APIVersion: "7.4", Credential: staticVaultCredential{}, Client: vault.Client(),
			Metadata: &failingMetadataReplaceStore{},
		}
		names := make([]string, 0, keyVaultDiscoveryLimit+40)
		for index := 0; index < keyVaultDiscoveryLimit+40; index++ {
			names = append(names, fmt.Sprintf("TOKEN_%03d", index))
		}
		if err := store.DiscoverManagedMetadata(context.Background(), "project", names); err != nil {
			t.Fatal(err)
		}
		if got := int(requests.Load()); got != keyVaultDiscoveryLimit {
			t.Fatalf("discovery requests = %d, want cap %d", got, keyVaultDiscoveryLimit)
		}
	})

	t.Run("shared deadline", func(t *testing.T) {
		vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		t.Cleanup(vault.Close)
		store := &AzureKeyVaultStore{
			BaseURL: vault.URL, APIVersion: "7.4", Credential: staticVaultCredential{}, Client: vault.Client(),
			Metadata: &failingMetadataReplaceStore{},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		started := time.Now()
		err := store.DiscoverManagedMetadata(ctx, "project", []string{"ONE", "TWO"})
		if err == nil || time.Since(started) > time.Second {
			t.Fatalf("deadline-bound discovery error = %v elapsed=%s", err, time.Since(started))
		}
	})
}

// The dashboard keeps the metadata a rotation returns and compares it with
// the listed metadata, which the stored record serves. PostgreSQL keeps
// microseconds and Go's clock has nanoseconds on Linux.
func TestAzureKeyVaultStoreReplaceReturnsTheStoredMetadataTime(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: "vault-precision", Name: "Vault", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if created, err := records.CreateProjectSecretMetadata(&storage.ProjectSecretMetadata{
		ProjectID: "vault-precision", SecretName: "TOKEN", Provider: keyVaultProvider,
		CreatedAt: now, UpdatedAt: now, CreatedBy: "admin",
	}); err != nil || !created {
		t.Fatalf("seed metadata = %t, %v", created, err)
	}
	vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"https://vault.example.test/secrets/token/1"}`))
	}))
	t.Cleanup(vault.Close)
	store := &AzureKeyVaultStore{
		BaseURL: vault.URL, APIVersion: "7.4", Credential: staticVaultCredential{},
		Client: vault.Client(), Metadata: records,
	}
	returned, err := store.Replace(context.Background(), "vault-precision", "TOKEN", "replacement-value", "admin")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := records.GetProjectSecretMetadata("vault-precision", "TOKEN")
	if err != nil || stored == nil || !returned.UpdatedAt.Equal(stored.UpdatedAt) || !returned.UpdatedAt.Equal(returned.UpdatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("rotation returned updated_at %s, stored %+v (%v): want the stored value, at microseconds", returned.UpdatedAt.Format(time.RFC3339Nano), stored, err)
	}
}
