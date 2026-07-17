package secretstore

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/brunoartifact"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

type engineResolverStore struct {
	err   error
	delay time.Duration
}

type createOnlyNoRollbackStore struct {
	creates int
}

type falseRollbackCapabilityStore struct {
	createOnlyNoRollbackStore
}

func (*falseRollbackCapabilityStore) CreatedValueRollbackSupported() bool { return true }

func (*createOnlyNoRollbackStore) Get(context.Context, string, string) (string, Metadata, error) {
	return "", Metadata{}, ErrNotFound
}
func (s *createOnlyNoRollbackStore) Create(context.Context, string, string, string, string) (Metadata, error) {
	s.creates++
	return Metadata{}, nil
}
func (*createOnlyNoRollbackStore) Replace(context.Context, string, string, string, string) (Metadata, error) {
	return Metadata{}, ErrNotFound
}
func (*createOnlyNoRollbackStore) List(context.Context, string) ([]Metadata, error) { return nil, nil }
func (*createOnlyNoRollbackStore) Writable() bool                                   { return true }
func (*createOnlyNoRollbackStore) Provider() string                                 { return "create-no-rollback" }

func (s engineResolverStore) Get(ctx context.Context, _ string, _ string) (string, Metadata, error) {
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return "", Metadata{}, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	return "", Metadata{}, s.err
}

func (engineResolverStore) Create(context.Context, string, string, string, string) (Metadata, error) {
	return Metadata{}, ErrReadOnly
}

func (engineResolverStore) Replace(context.Context, string, string, string, string) (Metadata, error) {
	return Metadata{}, ErrReadOnly
}

func (engineResolverStore) List(context.Context, string) ([]Metadata, error) {
	return nil, nil
}

func (engineResolverStore) Writable() bool   { return false }
func (engineResolverStore) Provider() string { return "test" }

func TestEngineResolverFallsBackOnlyWhenManagedSecretIsNotFound(t *testing.T) {
	const (
		projectID = "project-fail-closed"
		name      = "api_token"
		value     = "environment-fallback"
	)
	t.Setenv(engine.ProjectSecretEnvironmentName(projectID, name), value)

	notFound := EngineResolver(engineResolverStore{err: ErrNotFound}, projectID)
	if got, ok := notFound.Resolve(name); !ok || got != value {
		t.Fatalf("not-found fallback = (%q, %t)", got, ok)
	}

	providerFailure := EngineResolver(engineResolverStore{err: errors.New("provider unavailable")}, projectID)
	if got, ok := providerFailure.Resolve(name); ok || got != "" {
		t.Fatalf("provider failure fell back to environment: (%q, %t)", got, ok)
	}
}

func TestEngineResolverPreservesCallerDeadlineAcrossManagedMissAndFallback(t *testing.T) {
	const (
		projectID = "project-budget"
		name      = "api_token"
	)
	boundName := engine.ProjectSecretBindingName(projectID, name)
	vaultStarted := make(chan struct{}, 1)
	vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vaultStarted <- struct{}{}
		<-r.Context().Done()
	}))
	defer vault.Close()

	previousTransport := http.DefaultTransport
	http.DefaultTransport = vault.Client().Transport
	defer func() { http.DefaultTransport = previousTransport }()
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", vault.URL)
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_AUTH", "token")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_TOKEN", "test-token")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS", boundName)

	resolver := EngineResolver(engineResolverStore{err: ErrNotFound, delay: 25 * time.Millisecond}, projectID)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	if value, ok := engine.ResolveSecret(ctx, resolver, name); ok || value != "" {
		t.Fatalf("cancelled resolution = (%q, %t), want missing", value, ok)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("resolution ignored shared caller deadline: %s", elapsed)
	}
	select {
	case <-vaultStarted:
	default:
		t.Fatal("fallback provider was not exercised")
	}
}

func TestNewConfiguredPrioritizesAtomicDatabaseForCreateAndResolution(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED", "true")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "https://unit-test.vault.azure.net")
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: "configured-project", Name: "Configured", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := brunoartifact.NewLocalAESKeyWrapper(brunoartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := NewConfigured(records, wrapper)
	if err != nil {
		t.Fatal(err)
	}
	chain, ok := configured.(Chain)
	if !ok || len(chain.Stores) < 3 {
		t.Fatalf("configured store = %#v, want database, Key Vault, environment chain", configured)
	}
	if _, ok := chain.Stores[0].(*DatabaseStore); !ok {
		t.Fatalf("first configured provider = %T, want atomic database", chain.Stores[0])
	}
	vaultStore, ok := chain.Stores[1].(*AzureKeyVaultStore)
	if !ok {
		t.Fatalf("second configured provider = %T, want Key Vault", chain.Stores[1])
	}
	vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(vault.Close)
	vaultStore.BaseURL = vault.URL
	vaultStore.Client = vault.Client()
	vaultStore.Credential = staticVaultCredential{}
	if !SupportsCreate(chain) {
		t.Fatal("database-backed configured chain did not advertise atomic create")
	}
	metadata, err := chain.Create(context.Background(), "configured-project", "LOGIN_TOKEN", "database-value", "admin")
	if err != nil || metadata.Provider != databaseProvider {
		t.Fatalf("configured Create = %#v, %v", metadata, err)
	}
	value, resolved, err := chain.Get(context.Background(), "configured-project", "LOGIN_TOKEN")
	if err != nil || value != "database-value" || resolved.Provider != databaseProvider {
		t.Fatalf("configured resolution = %q, %#v, %v", value, resolved, err)
	}
}

func TestChainDoesNotAdvertiseRollbackWhenWritableChildCannotCompensate(t *testing.T) {
	child := &createOnlyNoRollbackStore{}
	chain := Chain{Stores: []Store{child, EnvironmentStore{}}}
	if !SupportsCreate(chain) {
		t.Fatal("create-capable child did not advertise create")
	}
	if SupportsCreatedValueRollback(chain) {
		t.Fatal("chain advertised rollback through a child that cannot compensate")
	}
	if child.creates != 0 {
		t.Fatalf("capability probe performed %d writes", child.creates)
	}
}

func TestRollbackCapabilityRequiresTheAtomicRollbackOperation(t *testing.T) {
	store := &falseRollbackCapabilityStore{}
	if SupportsCreatedValueRollback(store) {
		t.Fatal("capability flag advertised rollback without the atomic rollback operation")
	}
}

func TestNewConfiguredKeyVaultOnlyFailsClosedForCreate(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED", "true")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "https://unit-test.vault.azure.net")
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	configured, err := NewConfigured(records, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain, ok := configured.(Chain)
	if !ok || len(chain.Stores) != 2 {
		t.Fatalf("configured store = %#v, want Key Vault and environment chain", configured)
	}
	if _, ok := chain.Stores[0].(*AzureKeyVaultStore); !ok {
		t.Fatalf("first configured provider = %T, want Key Vault", chain.Stores[0])
	}
	if SupportsCreate(chain) {
		t.Fatal("Key Vault-only chain advertised atomic create")
	}
	if _, err := chain.Create(context.Background(), "configured-project", "LOGIN_TOKEN", "must-not-write", "admin"); !errors.Is(err, ErrCreateUnsupported) {
		t.Fatalf("Key Vault-only Create error = %v, want create unsupported", err)
	}
}
