package storage

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Cluster connection kinds.
const (
	ClusterKindInCluster = "in_cluster"
	ClusterKindGeneric   = "generic"
	ClusterKindAKS       = "aks"
	ClusterKindEKS       = "eks"
)

// Authentication modes for a cluster connection.
const (
	ClusterAuthInCluster             = "in_cluster"
	ClusterAuthStaticToken           = "static_token"
	ClusterAuthAzureServicePrincipal = "azure_service_principal"
	ClusterAuthAWSIAM                = "aws_iam"
)

// AWS credential modes for ClusterAuthAWSIAM. These mirror kubeops but are
// duplicated as plain strings so the storage layer does not depend on the
// Kubernetes client package — the same reason validateClusterAPIServerURL
// duplicates kubeops.ValidateAPIServerURL.
const (
	// AWSCredentialModeStaticKeys uses an administrator-supplied access key.
	AWSCredentialModeStaticKeys = "static_keys"
	// AWSCredentialModeAssumeRole assumes a role, optionally chained from
	// static keys.
	AWSCredentialModeAssumeRole = "assume_role"
	// AWSCredentialModeAmbient uses the controller's own identity: IRSA or EKS
	// Pod Identity.
	AWSCredentialModeAmbient = "ambient"
)

// TLS trust modes. These mirror kubeops but are duplicated as plain strings so
// the storage layer does not depend on the Kubernetes client package.
const (
	ClusterTLSCustomCA    = "custom_ca"
	ClusterTLSSystemRoots = "system_roots"
)

// ErrClusterConnectionReferenced blocks deleting a connection that fleet pools
// still point at. Cascading instead of refusing would leave those pools falling
// back to the controller's own cluster — i.e. scaling generators somewhere the
// administrator never chose.
var ErrClusterConnectionReferenced = errors.New("cluster connection is referenced by fleet pools")

// ErrClusterConnectionKindImmutable prevents an edit from silently changing
// the provider semantics of every normalized execution target derived from the
// connection. Create a new connection and repoint dependents instead.
var ErrClusterConnectionKindImmutable = errors.New("cluster connection kind is immutable")

// ClusterConnection is an administrator-registered Kubernetes control plane the
// controller can drive. Credentials are never stored in plaintext: the AAD
// client secret and static service-account token are held as keywrap envelopes
// (ciphertext + wrapped DEK) and are excluded from JSON so they cannot leak
// through an API response.
type ClusterConnection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`

	APIServerURL         string `json:"api_server_url,omitempty"`
	Namespace            string `json:"namespace"`
	GeneratorSelector    string `json:"generator_selector,omitempty"`
	GeneratorStatefulSet string `json:"generator_stateful_set,omitempty"`
	// GeneratorContainer disambiguates which container an allocation patch
	// resizes. It matters because the shipped Helm chart runs a
	// java-vuser-runner sidecar next to the generator.
	GeneratorContainer string `json:"generator_container,omitempty"`

	CACertificatePEM string `json:"ca_certificate_pem,omitempty"`
	TLSMode          string `json:"tls_mode"`

	AuthMode       string `json:"auth_mode"`
	AzureTenantID  string `json:"azure_tenant_id,omitempty"`
	AzureClientID  string `json:"azure_client_id,omitempty"`
	AzureScope     string `json:"azure_scope,omitempty"`
	AzureAuthority string `json:"azure_authority,omitempty"`

	// AWS identifiers for an EKS control plane. AWSAccessKeyID is half of a
	// credential pair and is treated like AzureClientID: displayable, because
	// AWS itself displays it and it is useless without the secret.
	AWSRegion         string `json:"aws_region,omitempty"`
	AWSClusterName    string `json:"aws_cluster_name,omitempty"`
	AWSCredentialMode string `json:"aws_credential_mode,omitempty"`
	AWSAccessKeyID    string `json:"aws_access_key_id,omitempty"`
	AWSRoleARN        string `json:"aws_role_arn,omitempty"`
	AWSSTSEndpoint    string `json:"aws_sts_endpoint,omitempty"`

	// Envelope-encrypted credentials. json:"-" is the read-boundary guarantee:
	// no handler can accidentally serialize these.
	//
	// The AWS slots are separate from the AAD/static-token slots rather than
	// reused, for three reasons: the envelope AAD embeds the slot kind (so
	// reusing one would let a credential be unsealed as a different type), AWS
	// needs two secrets at once, and HasStoredCredential's meaning would become
	// unreadable.
	ClientSecretCiphertext string `json:"-"`
	ClientSecretWrappedKey string `json:"-"`
	StaticTokenCiphertext  string `json:"-"`
	StaticTokenWrappedKey  string `json:"-"`
	AWSSecretKeyCiphertext string `json:"-"`
	AWSSecretKeyWrappedKey string `json:"-"`
	// The external ID is enveloped rather than stored in the clear because its
	// entire purpose is defeating the confused-deputy attack on the role ARN
	// sitting next to it; plaintext would halve that protection.
	AWSExternalIDCiphertext string `json:"-"`
	AWSExternalIDWrappedKey string `json:"-"`
	// ClientSecretEnvVar names an environment variable holding the credential
	// instead of storing it. Mutually exclusive with the encrypted secret.
	ClientSecretEnvVar string `json:"client_secret_env_var,omitempty"`

	Status           string     `json:"status"`
	LastCheckedAt    *time.Time `json:"last_checked_at,omitempty"`
	LastCheckStatus  string     `json:"last_check_status,omitempty"`
	LastCheckMessage string     `json:"last_check_message,omitempty"`

	Labels    map[string]string `json:"labels,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// HasStoredCredential reports whether an encrypted credential is present, so a
// response can say "a secret is set" without revealing anything about it.
func (c *ClusterConnection) HasStoredCredential() bool {
	if c == nil {
		return false
	}
	// The external ID is deliberately not counted: an ambient assume-role
	// connection carrying only an external ID would otherwise render as
	// "a secret is set", which misdescribes how it authenticates.
	return strings.TrimSpace(c.ClientSecretCiphertext) != "" ||
		strings.TrimSpace(c.StaticTokenCiphertext) != "" ||
		strings.TrimSpace(c.AWSSecretKeyCiphertext) != ""
}

// CredentialSource describes where the credential comes from, for display.
func (c *ClusterConnection) CredentialSource() string {
	switch {
	case c == nil:
		return ""
	case c.AuthMode == ClusterAuthInCluster:
		return "in_cluster"
	// An ambient AWS connection has no stored credential and no env var, but
	// "unset" would read as a misconfiguration and "in_cluster" means the
	// Kubernetes service account rather than the AWS role. IRSA and EKS Pod
	// Identity are both workload identity.
	case c.AuthMode == ClusterAuthAWSIAM && c.AWSCredentialMode == AWSCredentialModeAmbient:
		return "workload_identity"
	case strings.TrimSpace(c.ClientSecretEnvVar) != "":
		return "env_var"
	case c.HasStoredCredential():
		return "inline_encrypted"
	default:
		return "unset"
	}
}

// Validate enforces the invariants that make a connection safe to use. It runs
// before persistence so a malformed row can never reach the Kubernetes client.
func (c *ClusterConnection) Validate() error {
	if c == nil {
		return errors.New("cluster connection is required")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("cluster connection name is required")
	}
	if strings.TrimSpace(c.Namespace) == "" {
		return errors.New("cluster connection namespace is required")
	}
	switch c.Kind {
	case ClusterKindInCluster, ClusterKindGeneric, ClusterKindAKS, ClusterKindEKS:
	default:
		return fmt.Errorf("unsupported cluster kind %q", c.Kind)
	}
	switch c.TLSMode {
	case ClusterTLSCustomCA, ClusterTLSSystemRoots:
	default:
		return fmt.Errorf("unsupported TLS mode %q", c.TLSMode)
	}

	if c.Kind == ClusterKindInCluster {
		if c.AuthMode != ClusterAuthInCluster {
			return errors.New("an in-cluster connection must use in-cluster authentication")
		}
		return nil
	}

	// Remote connections must fully specify how to reach and trust the cluster;
	// there is no fallback to the controller's own environment.
	if err := validateClusterAPIServerURL(c.APIServerURL); err != nil {
		return err
	}
	if c.TLSMode == ClusterTLSCustomCA && strings.TrimSpace(c.CACertificatePEM) == "" {
		return errors.New("a CA certificate is required unless the TLS mode is system_roots")
	}
	if pem := strings.TrimSpace(c.CACertificatePEM); pem != "" && !strings.Contains(pem, "-----BEGIN CERTIFICATE-----") {
		return errors.New("the CA certificate must be PEM-encoded")
	}

	switch c.AuthMode {
	case ClusterAuthAzureServicePrincipal:
		if strings.TrimSpace(c.AzureTenantID) == "" || strings.TrimSpace(c.AzureClientID) == "" {
			return errors.New("azure tenant ID and client ID are required for service-principal authentication")
		}
	case ClusterAuthAWSIAM:
		if err := c.validateAWSIAM(); err != nil {
			return err
		}
	case ClusterAuthStaticToken:
	case ClusterAuthInCluster:
		return errors.New("in-cluster authentication is only valid for an in-cluster connection")
	default:
		return fmt.Errorf("unsupported authentication mode %q", c.AuthMode)
	}
	if strings.TrimSpace(c.ClientSecretEnvVar) != "" && c.HasStoredCredential() {
		return errors.New("a connection cannot use both a stored credential and an environment variable")
	}
	return nil
}

var (
	awsRegionPattern      = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	awsEKSClusterPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)
	awsRoleARNPattern     = regexp.MustCompile(`^arn:aws[a-z-]*:iam::\d{12}:role/.+$`)
	awsSTSHostPattern     = regexp.MustCompile(`^sts(-fips)?(\.[a-z0-9-]{1,32})?\.amazonaws\.com(\.cn)?$`)
	errAWSKeysNeedBothHal = errors.New("an aws access key ID and secret access key must be supplied together")
)

// validateAWSIAM mirrors kubeops.NewAwsIAMTokenSource's checks so a malformed
// EKS connection is refused at save time rather than at the first scale
// attempt. It is duplicated rather than imported for the same reason as
// validateClusterAPIServerURL: pkg/storage stays free of Kubernetes and cloud
// SDK dependencies. Both layers are covered by tests.
func (c *ClusterConnection) validateAWSIAM() error {
	// An EKS IAM token is only meaningful against an EKS control plane. The
	// converse is deliberately NOT enforced: reaching an EKS cluster with a
	// plain service-account token is a legitimate configuration.
	if c.Kind != ClusterKindEKS {
		return errors.New("aws IAM authentication is only valid for an eks connection")
	}
	if !awsRegionPattern.MatchString(strings.TrimSpace(c.AWSRegion)) {
		return fmt.Errorf("aws region %q is not a valid region name", c.AWSRegion)
	}
	// The cluster name is written into a signed HTTP header, so its charset is
	// a header-injection control, not cosmetics.
	if !awsEKSClusterPattern.MatchString(strings.TrimSpace(c.AWSClusterName)) {
		return fmt.Errorf("eks cluster name %q is not valid", c.AWSClusterName)
	}
	hasKeyID := strings.TrimSpace(c.AWSAccessKeyID) != ""
	hasSecret := strings.TrimSpace(c.AWSSecretKeyCiphertext) != "" || strings.TrimSpace(c.ClientSecretEnvVar) != ""
	switch c.AWSCredentialMode {
	case AWSCredentialModeStaticKeys:
		if !hasKeyID || !hasSecret {
			return errAWSKeysNeedBothHal
		}
	case AWSCredentialModeAssumeRole:
		if !awsRoleARNPattern.MatchString(strings.TrimSpace(c.AWSRoleARN)) {
			return fmt.Errorf("aws role ARN %q is not an IAM role ARN", c.AWSRoleARN)
		}
		if hasKeyID != hasSecret {
			return errAWSKeysNeedBothHal
		}
	case AWSCredentialModeAmbient:
		// An ambiguous configuration must fail loudly rather than silently
		// preferring one credential over the other.
		if hasKeyID || strings.TrimSpace(c.AWSSecretKeyCiphertext) != "" {
			return errors.New("ambient credentials cannot be combined with static aws keys")
		}
	default:
		return fmt.Errorf("unsupported aws credential mode %q", c.AWSCredentialMode)
	}
	return validateAWSSTSEndpoint(c.AWSSTSEndpoint, c.AWSRegion)
}

// validateAWSSTSEndpoint mirrors kubeops.ValidateSTSEndpoint. A presigned STS
// URL is a replayable bearer credential, so an endpoint that is not real STS
// would exfiltrate it.
func validateAWSSTSEndpoint(raw, region string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("aws STS endpoint is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return errors.New("aws STS endpoint must be an https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return errors.New("aws STS endpoint must be a bare https host")
	}
	host := strings.ToLower(parsed.Hostname())
	if !awsSTSHostPattern.MatchString(host) {
		return fmt.Errorf("aws STS endpoint host %q is not an allowed AWS STS endpoint", host)
	}
	if labels := strings.Split(strings.TrimSuffix(host, ".cn"), "."); len(labels) == 4 && labels[1] != strings.TrimSpace(region) {
		return fmt.Errorf("aws STS endpoint is in region %q but the connection is configured for %q", labels[1], region)
	}
	return nil
}

// validateClusterAPIServerURL mirrors kubeops.ValidateAPIServerURL. It is
// duplicated rather than imported so pkg/storage stays free of Kubernetes
// dependencies; both are covered by tests.
func validateClusterAPIServerURL(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return errors.New("API server URL is required for a remote cluster")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("API server URL is not a valid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return errors.New("API server URL must use https")
	}
	if parsed.Hostname() == "" {
		return errors.New("API server URL must include a host")
	}
	if parsed.User != nil {
		return errors.New("API server URL must not embed credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("API server URL must not include a query or fragment")
	}
	if strings.Trim(parsed.Path, "/") != "" {
		return errors.New("API server URL must not include a path")
	}
	return nil
}

// ClusterConnectionStore is an optional capability, type-asserted from Store so
// existing Store implementations and test doubles keep compiling (the same
// convention as GitHubConnectionStore).
type ClusterConnectionStore interface {
	CreateClusterConnection(conn *ClusterConnection) error
	UpdateClusterConnectionIfUnchanged(conn *ClusterConnection, expectedUpdatedAt time.Time) (bool, error)
	UpdateClusterConnectionHealth(id, status, message string, checkedAt time.Time) error
	GetClusterConnection(id string) (*ClusterConnection, error)
	ListClusterConnections() ([]*ClusterConnection, error)
	DeleteClusterConnectionIfUnreferenced(id string) (bool, error)
	ListFleetPoolsForCluster(clusterID string) ([]*FleetPool, error)
}
