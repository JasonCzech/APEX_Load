package importartifact

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/jasonczech/apexload/pkg/keywrap"
)

// Legacy names remain as aliases so existing artifact-store integrations keep
// compiling while all wrapping implementation and configuration is neutral.
const (
	MasterKeyEnvVar       = keywrap.MasterKeyEnvVar
	KeyVaultKeyIDEnvVar   = keywrap.KeyVaultKeyIDEnvVar
	keyVaultWrapAlgorithm = "RSA-OAEP-256"
)

type WrappedKey = keywrap.WrappedKey
type KeyWrapper = keywrap.KeyWrapper
type LocalKeyOptions = keywrap.LocalKeyOptions
type LocalAESKeyWrapper = keywrap.LocalAESKeyWrapper
type AzureKeyVaultKeyWrapper = keywrap.AzureKeyVaultKeyWrapper

func NewLocalAESKeyWrapper(options LocalKeyOptions) (*LocalAESKeyWrapper, error) {
	return keywrap.NewLocalAESKeyWrapper(options)
}

func NewAzureKeyVaultKeyWrapper(keyID string, credential azcore.TokenCredential, client *http.Client) (*AzureKeyVaultKeyWrapper, error) {
	return keywrap.NewAzureKeyVaultKeyWrapper(keyID, credential, client)
}

func NewConfiguredKeyWrapper(allowKeyring bool) (KeyWrapper, error) {
	return keywrap.NewConfiguredKeyWrapper(allowKeyring)
}
