package secretstore

import (
	"context"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

func NewConfigured(records storage.ProjectSecretCipherStore, wrapper keywrap.KeyWrapper) (Store, error) {
	stores := make([]Store, 0, 4)
	keyVault, err := NewAzureKeyVaultStoreFromEnv()
	if err != nil {
		return nil, err
	}
	secretsManager, err := NewAwsSecretsManagerStoreFromEnv()
	if err != nil {
		return nil, err
	}
	if metadata, ok := records.(storage.ProjectSecretMetadataStore); ok {
		if keyVault != nil {
			keyVault.Metadata = metadata
		}
		if secretsManager != nil {
			secretsManager.Metadata = metadata
		}
	}
	// The encrypted database provider has a real atomic INSERT-if-absent and is
	// therefore authoritative for new managed values and resolution when it is
	// available. Key Vault remains a read/explicit-rotation provider for external
	// records, but its Set Secret API is upsert-only and cannot safely implement
	// import create semantics.
	if records != nil && wrapper != nil {
		stores = append(stores, &DatabaseStore{Records: records, Wrapper: wrapper})
	}
	// Both external providers may be configured at once. Cross-provider
	// clobbering is already prevented by the Provider mismatch check in each
	// Replace, but note that Get is first-wins if the same physical name exists
	// in both.
	if keyVault != nil {
		stores = append(stores, keyVault)
	}
	if secretsManager != nil {
		stores = append(stores, secretsManager)
	}
	stores = append(stores, EnvironmentStore{})
	return Chain{Stores: stores}, nil
}

func EngineResolver(store Store, projectID string) engine.SecretResolver {
	fallback := engine.NewProjectSecretResolver(projectID)
	if store == nil {
		return fallback
	}
	return engine.ContextSecretResolverFunc(func(parent context.Context, name string) (string, bool) {
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		value, _, err := store.Get(ctx, projectID, name)
		cancel()
		if err == nil {
			return value, true
		}
		if errors.Is(err, ErrNotFound) {
			// Preserve the caller's shared resolution budget across the managed
			// store and the environment/Key Vault fallback. Calling Resolve here
			// would replace parent with context.Background and allow every miss to
			// start a fresh provider timeout after the overall deadline expired.
			return engine.ResolveSecret(parent, fallback, name)
		}
		return "", false
	})
}
