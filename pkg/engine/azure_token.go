package engine

import (
	"context"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// azureTokenSource supplies bearer tokens for Azure data-plane calls (Key
// Vault). Implementations: a static operator-provided token, or a cached
// azcore.TokenCredential (DefaultAzureCredential) that covers AKS workload
// identity, managed identity, environment credentials, and the Azure CLI.
type azureTokenSource interface {
	Token(ctx context.Context) (string, error)
}

type staticAzureTokenSource string

func (s staticAzureTokenSource) Token(context.Context) (string, error) {
	return string(s), nil
}

// azureTokenRefreshSkew refreshes cached tokens this long before expiry so a
// token never goes stale mid-request.
const azureTokenRefreshSkew = 2 * time.Minute

type azureCredentialTokenSource struct {
	credential azcore.TokenCredential
	scope      string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func newAzureCredentialTokenSource(credential azcore.TokenCredential, scope string) *azureCredentialTokenSource {
	return &azureCredentialTokenSource{credential: credential, scope: scope}
}

func (s *azureCredentialTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.expiry.Add(-azureTokenRefreshSkew)) {
		return s.token, nil
	}
	token, err := s.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{s.scope}})
	if err != nil {
		return "", err
	}
	s.token = token.Token
	s.expiry = token.ExpiresOn
	return s.token, nil
}
