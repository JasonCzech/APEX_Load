package api

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

// errClusterConnectionInactive stops a suspended connection from being used.
var errClusterConnectionInactive = errors.New("cluster connection is not active")

// clusterClientCache builds and reuses one Kubernetes client per registered
// connection. The cache key includes UpdatedAt so editing a connection
// invalidates both the cached HTTP client (new CA or endpoint) and the token
// source's cached Azure AD token (new credential) — without it, a rotated
// client secret would keep failing until the controller restarted.
type clusterClientCache struct {
	dialGuard func(netip.Addr) error
	wrapper   keywrap.KeyWrapper

	mu      sync.Mutex
	entries map[string]clusterClientCacheEntry
}

type clusterClientCacheEntry struct {
	updatedAt time.Time
	client    *kubeops.Client
}

func newClusterClientCache(wrapper keywrap.KeyWrapper, dialGuard func(netip.Addr) error) *clusterClientCache {
	return &clusterClientCache{
		dialGuard: dialGuard,
		wrapper:   wrapper,
		entries:   make(map[string]clusterClientCacheEntry),
	}
}

// ClientFor returns a client for the connection, building one if the cached
// entry is missing or stale.
func (c *clusterClientCache) ClientFor(ctx context.Context, conn *storage.ClusterConnection) (*kubeops.Client, error) {
	if c == nil {
		return nil, errors.New("cluster client cache is not initialized")
	}
	if conn == nil {
		return nil, errors.New("cluster connection is required")
	}
	if strings.TrimSpace(conn.Status) != "" && conn.Status != "active" {
		return nil, fmt.Errorf("%w: %s", errClusterConnectionInactive, conn.Status)
	}

	c.mu.Lock()
	if entry, ok := c.entries[conn.ID]; ok && entry.updatedAt.Equal(conn.UpdatedAt) {
		c.mu.Unlock()
		return entry.client, nil
	}
	c.mu.Unlock()

	client, err := c.build(ctx, conn)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[conn.ID] = clusterClientCacheEntry{updatedAt: conn.UpdatedAt, client: client}
	c.mu.Unlock()
	return client, nil
}

// Forget drops a connection's cached client, used after a delete.
func (c *clusterClientCache) Forget(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}

func (c *clusterClientCache) build(ctx context.Context, conn *storage.ClusterConnection) (*kubeops.Client, error) {
	base := kubeops.Config{
		Namespace:         conn.Namespace,
		GeneratorSelector: conn.GeneratorSelector,
	}

	// An in-cluster connection uses the controller's own service account and
	// must not carry an administrator-supplied endpoint or credential.
	if conn.Kind == storage.ClusterKindInCluster {
		return kubeops.NewInClusterClient(base)
	}

	tokenSource, err := c.tokenSourceFor(ctx, conn)
	if err != nil {
		return nil, err
	}
	base.APIServerURL = conn.APIServerURL
	base.CAPEM = conn.CACertificatePEM
	base.TLSMode = conn.TLSMode
	base.TokenSource = tokenSource
	base.DialGuard = c.dialGuard
	return kubeops.NewRemoteClient(base)
}

func (c *clusterClientCache) tokenSourceFor(ctx context.Context, conn *storage.ClusterConnection) (kubeops.TokenSource, error) {
	switch conn.AuthMode {
	case storage.ClusterAuthAzureServicePrincipal:
		secret, err := c.resolveCredential(ctx, conn, clusterCredentialAADClientSecret,
			conn.ClientSecretEnvVar, conn.ClientSecretCiphertext, conn.ClientSecretWrappedKey)
		if err != nil {
			return nil, err
		}
		return kubeops.NewAzureServicePrincipalTokenSource(kubeops.AzureServicePrincipalConfig{
			TenantID:     conn.AzureTenantID,
			ClientID:     conn.AzureClientID,
			ClientSecret: secret,
			Scope:        conn.AzureScope,
			Authority:    conn.AzureAuthority,
			// The AAD login endpoint is a public internet host, so it uses the
			// general SSRF-guarded client rather than the cluster dial policy.
			HTTPClient: newGuardedHTTPClient(15 * time.Second),
		})
	case storage.ClusterAuthStaticToken:
		token, err := c.resolveCredential(ctx, conn, clusterCredentialStaticToken,
			conn.ClientSecretEnvVar, conn.StaticTokenCiphertext, conn.StaticTokenWrappedKey)
		if err != nil {
			return nil, err
		}
		return kubeops.StaticTokenSource{Value: token}, nil
	case storage.ClusterAuthAWSIAM:
		return c.awsTokenSourceFor(ctx, conn)
	default:
		return nil, fmt.Errorf("unsupported cluster authentication mode %q", conn.AuthMode)
	}
}

// awsTokenSourceFor builds the EKS token source. Ambient mode reads no stored
// credential at all: the controller's own IRSA / Pod Identity role is used.
func (c *clusterClientCache) awsTokenSourceFor(ctx context.Context, conn *storage.ClusterConnection) (kubeops.TokenSource, error) {
	cfg := kubeops.AwsIAMConfig{
		Region:         conn.AWSRegion,
		ClusterName:    conn.AWSClusterName,
		CredentialMode: kubeops.AwsCredentialMode(conn.AWSCredentialMode),
		AccessKeyID:    conn.AWSAccessKeyID,
		RoleARN:        conn.AWSRoleARN,
		STSEndpoint:    conn.AWSSTSEndpoint,
		SessionName:    awsSessionNameFor(conn.ID),
		// STS is a public internet host, so this is the general SSRF-guarded
		// client rather than the cluster dial policy — the same reasoning as
		// the AAD login endpoint above. Presigning makes no request at all;
		// this client covers the assume-role and GetCallerIdentity calls.
		HTTPClient: newGuardedHTTPClient(15 * time.Second),
	}

	if conn.AWSCredentialMode != storage.AWSCredentialModeAmbient && strings.TrimSpace(conn.AWSAccessKeyID) != "" {
		secret, err := c.resolveCredential(ctx, conn, clusterCredentialAWSSecretKey,
			conn.ClientSecretEnvVar, conn.AWSSecretKeyCiphertext, conn.AWSSecretKeyWrappedKey)
		if err != nil {
			return nil, err
		}
		cfg.SecretAccessKey = secret
	}

	// The external ID has no env-var alternative: ClientSecretEnvVar names the
	// variable holding the ACCESS KEY, and applying it here would silently feed
	// the secret key in as the external ID.
	if strings.TrimSpace(conn.AWSExternalIDCiphertext) != "" {
		externalID, err := c.resolveCredential(ctx, conn, clusterCredentialAWSExternalID,
			"", conn.AWSExternalIDCiphertext, conn.AWSExternalIDWrappedKey)
		if err != nil {
			return nil, err
		}
		cfg.ExternalID = externalID
	}
	return kubeops.NewAwsIAMTokenSource(cfg)
}

// awsSessionNameFor derives a stable, identifiable role session name. It shows
// up in CloudTrail and in the assumed-role ARN an administrator must map, so it
// must be deterministic per connection rather than random.
func awsSessionNameFor(connectionID string) string {
	trimmed := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return -1
		}
	}, strings.TrimSpace(connectionID))
	if len(trimmed) > 16 {
		trimmed = trimmed[:16]
	}
	if trimmed == "" {
		return "apexload"
	}
	return "apexload-" + trimmed
}

// resolveCredential prefers the environment-variable reference when one is
// configured, so a GitOps deployment can keep credentials entirely out of the
// database.
//
// envVarName is passed per credential slot rather than read from the connection
// here. A connection has only one env-var reference but may hold several
// credentials; resolving every slot from that one variable would silently hand
// the same value back for each of them.
func (c *clusterClientCache) resolveCredential(ctx context.Context, conn *storage.ClusterConnection, kind clusterCredentialKind, envVarName, ciphertext, wrappedKey string) (string, error) {
	if name := strings.TrimSpace(envVarName); name != "" {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("environment variable %s referenced by cluster connection %q is empty", name, conn.Name)
		}
		return value, nil
	}
	return openClusterCredential(ctx, c.wrapper, conn.ID, kind, ciphertext, wrappedKey)
}
