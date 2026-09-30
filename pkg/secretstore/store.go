// Package secretstore provides project-scoped secret migration and resolution.
// Implementations return plaintext only to the execution boundary; metadata
// APIs never include secret values.
package secretstore

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

var (
	ErrNotFound            = sql.ErrNoRows
	ErrReadOnly            = errors.New("secret store is read-only")
	ErrAlreadyExists       = errors.New("secret already exists")
	ErrReservedBinding     = errors.New("secret binding is reserved by the read-only Azure Key Vault allowlist")
	ErrCreateUnsupported   = errors.New("secret store does not support atomic create-only writes")
	ErrRollbackUnsupported = errors.New("secret store does not support safe created-value rollback")
	// ErrMetadataCommit means a provider value may have been committed, but the
	// governed value-free metadata index did not acknowledge the same operation.
	// Callers may reconcile by probing both the value and committed metadata;
	// matching the value alone is not sufficient to report success.
	ErrMetadataCommit = errors.New("secret metadata commit failed")
)

type Metadata struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	Writable  bool      `json:"writable"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	CreatedBy string    `json:"created_by,omitempty"`
}

type Store interface {
	Get(context.Context, string, string) (string, Metadata, error)
	// Create must be an atomic create-only operation: implementations may never
	// replace or version an existing provider value. Providers whose remote API
	// only offers upsert must return ErrCreateUnsupported and advertise that via
	// CreateSupported rather than approximating this contract with GET then PUT.
	Create(context.Context, string, string, string, string) (Metadata, error)
	Replace(context.Context, string, string, string, string) (Metadata, error)
	List(context.Context, string) ([]Metadata, error)
	Writable() bool
	Provider() string
}

type createCapability interface {
	CreateSupported() bool
}

// CreatedValueRollbackStore is an optional, narrowly scoped compensation
// capability. Implementations must atomically delete only when createdBy still
// owns the current value; they must return false without mutation for a
// preexisting, replaced, or differently-owned value.
type CreatedValueRollbackStore interface {
	RollbackCreated(context.Context, string, string, string) (bool, error)
}

type createdValueRollbackCapability interface {
	CreatedValueRollbackSupported() bool
}

type managedMetadataDiscoverer interface {
	DiscoverManagedMetadata(context.Context, string, []string) error
}

// DiscoverManagedMetadata asks external providers to claim value-free metadata
// for known logical names that already exist at their deterministic provider
// address. It never returns or serializes provider values and does not change
// create-only import semantics.
func DiscoverManagedMetadata(ctx context.Context, store Store, projectID string, names []string) error {
	if store == nil || len(names) == 0 {
		return nil
	}
	switch typed := store.(type) {
	case Chain:
		for _, child := range typed.Stores {
			if err := DiscoverManagedMetadata(ctx, child, projectID, names); err != nil {
				return err
			}
		}
		return nil
	case *Chain:
		if typed == nil {
			return nil
		}
		return DiscoverManagedMetadata(ctx, *typed, projectID, names)
	}
	if discoverer, ok := store.(managedMetadataDiscoverer); ok {
		return discoverer.DiscoverManagedMetadata(ctx, projectID, names)
	}
	return nil
}

// SupportsCreate reports whether the selected provider can honor Store.Create
// atomically. Existing implementations are assumed to satisfy the Store
// contract; providers with an upsert-only backend must explicitly opt out.
func SupportsCreate(store Store) bool {
	if store == nil || !store.Writable() {
		return false
	}
	if capability, ok := store.(createCapability); ok {
		return capability.CreateSupported()
	}
	return true
}

// SupportsCreatedValueRollback reports whether runtime provisioning can safely
// compensate a create after a later batch or test-persistence failure.
func SupportsCreatedValueRollback(store Store) bool {
	if store == nil || !SupportsCreate(store) {
		return false
	}
	if _, ok := store.(CreatedValueRollbackStore); !ok {
		return false
	}
	if capability, ok := store.(createdValueRollbackCapability); ok {
		return capability.CreatedValueRollbackSupported()
	}
	return true
}

// Chain resolves from the first provider containing a value and writes to the
// first writable provider. A common configuration is Key Vault or encrypted
// database storage followed by the read-only environment namespace.
type Chain struct {
	Stores []Store
}

func (c Chain) Get(ctx context.Context, projectID, name string) (string, Metadata, error) {
	for _, store := range c.Stores {
		if store == nil {
			continue
		}
		value, metadata, err := store.Get(ctx, projectID, name)
		if err == nil {
			if engine.IsUsableResolvedSecretValue(value) {
				return value, metadata, nil
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return "", Metadata{}, err
		}
	}
	return "", Metadata{}, ErrNotFound
}

func (c Chain) Create(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
	var target Store
	for _, store := range c.Stores {
		if store != nil && store.Writable() {
			target = store
			break
		}
	}
	if target == nil {
		return Metadata{}, ErrReadOnly
	}
	if !SupportsCreate(target) {
		return Metadata{}, ErrCreateUnsupported
	}
	for _, store := range c.Stores {
		if store == nil {
			continue
		}
		if _, _, err := store.Get(ctx, projectID, name); err == nil {
			return Metadata{}, ErrAlreadyExists
		} else if !errors.Is(err, ErrNotFound) {
			return Metadata{}, err
		}
	}
	if engine.ProjectKeyVaultBindingAllowlisted(projectID, name) {
		return Metadata{}, ErrReservedBinding
	}
	return target.Create(ctx, projectID, name, value, actor)
}

func (c Chain) RollbackCreated(ctx context.Context, projectID, name, createdBy string) (bool, error) {
	for _, store := range c.Stores {
		if store == nil || !store.Writable() {
			continue
		}
		rollback, ok := store.(CreatedValueRollbackStore)
		if !ok {
			return false, ErrRollbackUnsupported
		}
		return rollback.RollbackCreated(ctx, projectID, name, createdBy)
	}
	return false, ErrReadOnly
}

func (c Chain) Replace(ctx context.Context, projectID, name, value, actor string) (Metadata, error) {
	for _, store := range c.Stores {
		if store == nil {
			continue
		}
		if _, _, err := store.Get(ctx, projectID, name); err == nil {
			if !store.Writable() {
				return Metadata{}, ErrReadOnly
			}
			return store.Replace(ctx, projectID, name, value, actor)
		} else if !errors.Is(err, ErrNotFound) {
			return Metadata{}, err
		}
	}
	if engine.ProjectKeyVaultBindingAllowlisted(projectID, name) {
		return Metadata{}, ErrReadOnly
	}
	return Metadata{}, ErrNotFound
}

func (c Chain) List(ctx context.Context, projectID string) ([]Metadata, error) {
	byName := map[string]Metadata{}
	for _, store := range c.Stores {
		if store == nil {
			continue
		}
		items, err := store.List(ctx, projectID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := strings.ToUpper(strings.TrimSpace(item.Name))
			if _, exists := byName[key]; !exists {
				byName[key] = item
			}
		}
	}
	result := make([]Metadata, 0, len(byName))
	for _, item := range byName {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c Chain) Writable() bool {
	for _, store := range c.Stores {
		if store != nil && store.Writable() {
			return true
		}
	}
	return false
}

func (c Chain) CreateSupported() bool {
	for _, store := range c.Stores {
		if store != nil && store.Writable() {
			return SupportsCreate(store)
		}
	}
	return false
}

func (c Chain) CreatedValueRollbackSupported() bool {
	for _, store := range c.Stores {
		if store != nil && store.Writable() {
			return SupportsCreatedValueRollback(store)
		}
	}
	return false
}

func (c Chain) Provider() string {
	names := make([]string, 0, len(c.Stores))
	for _, store := range c.Stores {
		if store != nil {
			names = append(names, store.Provider())
		}
	}
	return strings.Join(names, ",")
}
