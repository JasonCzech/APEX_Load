package kafka

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/protocol/kafkacontract"
)

func hasExplicitOAuth(c config) bool {
	return c.OAuthMode != "" || c.OAuthTokenURL != "" || c.OAuthClientID != "" || c.OAuthClientSecret != "" || c.OAuthClientScope != "" || c.OAuthAuthenticationMethod != ""
}

func validateExplicitOAuth(c config) error {
	if c.OAuthMode != "client_credentials" {
		return fmt.Errorf("unsupported Kafka OAuth mode")
	}
	if !strings.EqualFold(c.SASLMechanism, "oauthbearer") || !strings.EqualFold(c.SecurityProtocol, "sasl_ssl") || c.TLSInsecureSkipVerify {
		return fmt.Errorf("Kafka client credentials require oauthbearer over verified sasl_ssl")
	}
	if c.Profile != "" {
		return fmt.Errorf("explicit Kafka client credentials cannot use the managed-identity Event Hubs profile")
	}
	if c.OAuthClientID == "" || c.OAuthClientSecret == "" || strings.Contains(c.OAuthClientID, "***") || strings.Contains(c.OAuthClientSecret, "***") || strings.Contains(c.OAuthClientSecret, "{{") || strings.Contains(c.OAuthClientID, "{{") || strings.Contains(c.OAuthClientSecret, "${") || strings.Contains(c.OAuthClientID, "${") {
		return fmt.Errorf("Kafka OAuth client credentials are unresolved")
	}
	if len(c.OAuthTokenURL) > 8192 || len(c.OAuthClientID) > 4096 || len(c.OAuthClientSecret) > 65536 || len(c.OAuthClientScope) > 4096 {
		return fmt.Errorf("Kafka OAuth configuration exceeds size limits")
	}
	if err := ValidateOAuthTokenEndpoint(c.OAuthTokenURL); err != nil {
		return err
	}
	if c.OAuthAuthenticationMethod != "" && c.OAuthAuthenticationMethod != "client_secret_post" && c.OAuthAuthenticationMethod != "client_secret_basic" && c.OAuthAuthenticationMethod != "kafka_3_3_1_basic" {
		return fmt.Errorf("unsupported Kafka OAuth endpoint authentication")
	}
	return protocol.ValidateTargetURL(c.OAuthTokenURL, c.AllowedTargetHosts)
}

func explicitOAuthToken(ctx context.Context, c config) (string, error) {
	if err := validateExplicitOAuth(c); err != nil {
		return "", err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.Proxy = protocol.NewTargetPolicyProxy(transport.Proxy, c.AllowedTargetHosts)
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 30 * time.Second, Transport: protocol.NewTargetPolicyRoundTripper(transport, c.AllowedTargetHosts)}
	return explicitOAuthTokenWithClient(ctx, c, client)
}

func explicitOAuthTokenWithClient(ctx context.Context, c config, client *http.Client) (string, error) {
	if err := validateExplicitOAuth(c); err != nil {
		return "", err
	}
	token, err := httpprotocol.FetchOAuth2Token(ctx, explicitOAuthTokenRequest(c, client))
	if err != nil {
		return "", fmt.Errorf("Kafka OAuth token acquisition failed: %w", err)
	}
	return token.AccessToken, nil
}

// Constructing the request in one place keeps the debug disclosure flag on
// the same singleflight boundary as the ordinary credential request.
func explicitOAuthTokenRequest(c config, client *http.Client) httpprotocol.OAuthTokenRequest {
	return httpprotocol.OAuthTokenRequest{TokenURL: c.OAuthTokenURL, GrantType: httpprotocol.OAuthGrantClientCredentials, ClientID: c.OAuthClientID, ClientSecret: c.OAuthClientSecret, Scope: c.OAuthClientScope, ClientAuthentication: c.OAuthAuthenticationMethod, HTTPClient: client, UnredactedDebugErrors: c.UnredactedDebugErrors}
}

// ValidateOAuthTokenEndpoint checks the structural contract without resolving
// credentials or performing network access.
func ValidateOAuthTokenEndpoint(raw string) error {
	return kafkacontract.ValidateOAuthTokenEndpoint(raw)
}
