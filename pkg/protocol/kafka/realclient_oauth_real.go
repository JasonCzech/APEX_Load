//go:build kafka_real

package kafka

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/jasonczech/apexload/pkg/identitypolicy"
)

// oauthTokenRefreshSkew refreshes a cached token this long before expiry so it
// never goes stale mid-handshake.
const oauthTokenRefreshSkew = 2 * time.Minute

// oauthTokenSource caches a Microsoft Entra ID bearer token for the SASL
// OAUTHBEARER mechanism (e.g. Azure Event Hubs). Credentials are taken from the
// Azure environment via DefaultAzureCredential (AKS workload identity, managed
// identity, environment variables, Azure CLI) — never from the script — matching
// the no-secrets-in-script posture used elsewhere (pkg/engine/azure_token.go).
type oauthTokenSource struct {
	credential azcore.TokenCredential
	scope      string

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func (c *franzClient) oauthTokenSource() (*oauthTokenSource, error) {
	if !strings.EqualFold(strings.TrimSpace(c.cfg.Profile), "eventhubs") {
		return nil, fmt.Errorf("kafka managed-identity oauthbearer is restricted to config.kafka.profile=eventhubs")
	}
	if c.cfg.TLSInsecureSkipVerify {
		return nil, fmt.Errorf("kafka managed-identity oauthbearer forbids tls_insecure_skip_verify")
	}
	if len(c.cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka managed-identity oauthbearer requires at least one broker")
	}
	for _, broker := range c.cfg.Brokers {
		if _, err := identitypolicy.ValidateAzureMessagingNamespace(broker); err != nil {
			return nil, fmt.Errorf("kafka oauthbearer broker: %w", err)
		}
	}
	scope, err := eventHubsOAuthScope(c.cfg.Brokers, c.cfg.OAuthScope)
	if err != nil {
		return nil, err
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("kafka oauthbearer credential: %w", err)
	}
	return &oauthTokenSource{credential: cred, scope: scope}, nil
}

func (s *oauthTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.expiry.Add(-oauthTokenRefreshSkew)) {
		return s.token, nil
	}
	tok, err := s.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{s.scope}})
	if err != nil {
		return "", err
	}
	s.token = tok.Token
	s.expiry = tok.ExpiresOn
	return s.token, nil
}

// eventHubsOAuthScope returns the Entra ID scope for the OAUTHBEARER token. An
// explicit config.kafka.oauth_scope wins; otherwise it is derived from the first
// bootstrap broker host (e.g. ns.servicebus.windows.net:9093 ->
// https://ns.servicebus.windows.net/.default), the Event Hubs convention.
func eventHubsOAuthScope(_ []string, explicit string) (string, error) {
	if scope := strings.TrimSpace(explicit); scope != "" && scope != identitypolicy.AzureEventHubsOAuthScope {
		return "", fmt.Errorf("kafka managed-identity oauth_scope must be %q", identitypolicy.AzureEventHubsOAuthScope)
	}
	return identitypolicy.AzureEventHubsOAuthScope, nil
}
