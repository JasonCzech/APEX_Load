// Package keywrap defines protocol-neutral envelope-key wrapping contracts.
package keywrap

import "context"

const (
	MasterKeyEnvVar     = "APEXLOAD_WRAPPING_MASTER_KEY"
	KeyVaultKeyIDEnvVar = "APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID"
	KMSKeyIDEnvVar      = "APEXLOAD_WRAPPING_KMS_KEY_ID"
)

type WrappedKey struct {
	Ciphertext string         `json:"ciphertext"`
	Metadata   map[string]any `json:"metadata"`
}

type KeyWrapper interface {
	WrapKey(context.Context, []byte, []byte) (WrappedKey, error)
	UnwrapKey(context.Context, WrappedKey, []byte) ([]byte, error)
}
