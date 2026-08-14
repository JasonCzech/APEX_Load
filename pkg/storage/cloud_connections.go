package storage

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	CloudProviderAzure = "azure"
	CloudProviderAWS   = "aws"

	CloudAuthAzureIdentity         = "azure_identity"
	CloudAuthAzureServicePrincipal = "azure_service_principal"
	CloudAuthAWSAmbient            = "aws_ambient"
	CloudAuthAWSAssumeRole         = "aws_assume_role"
	CloudAuthAWSStaticKeys         = "aws_static_keys"

	AzureCloudPublic       = "public"
	AzureCloudUSGovernment = "us_government"
	AzureCloudChina        = "china"

	AWSPartitionCommercial = "aws"
	AWSPartitionGovCloud   = "aws-us-gov"
	AWSPartitionChina      = "aws-cn"
)

var ErrCloudConnectionReferenced = errors.New("cloud connection is referenced by execution targets")
var ErrCloudConnectionProviderImmutable = errors.New("cloud connection provider is immutable while execution targets reference it")
var ErrCloudConnectionScopeImmutable = errors.New("cloud connection provider scope is immutable while execution targets reference it")

// CloudConnection is a reusable, administrator-owned cloud control-plane
// identity. It deliberately does not name a compute workload: one connection
// may authorize several Container Apps environments or ECS clusters without
// duplicating its credential envelope for every placement.
type CloudConnection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Provider    string `json:"provider"`
	AuthMode    string `json:"auth_mode"`

	AzureCloud          string `json:"azure_cloud,omitempty"`
	AzureTenantID       string `json:"azure_tenant_id,omitempty"`
	AzureSubscriptionID string `json:"azure_subscription_id,omitempty"`
	AzureResourceGroup  string `json:"azure_resource_group,omitempty"`
	AzureClientID       string `json:"azure_client_id,omitempty"`

	AWSPartition   string `json:"aws_partition,omitempty"`
	AWSAccountID   string `json:"aws_account_id,omitempty"`
	AWSRegion      string `json:"aws_region,omitempty"`
	AWSAccessKeyID string `json:"aws_access_key_id,omitempty"`
	AWSRoleARN     string `json:"aws_role_arn,omitempty"`

	// Credential values are never serialized. The env-var reference contains a
	// name, not a value, and is safe to return to an administrator.
	ClientSecretCiphertext  string `json:"-"`
	ClientSecretWrappedKey  string `json:"-"`
	AWSSecretKeyCiphertext  string `json:"-"`
	AWSSecretKeyWrappedKey  string `json:"-"`
	AWSExternalIDCiphertext string `json:"-"`
	AWSExternalIDWrappedKey string `json:"-"`
	CredentialEnvVar        string `json:"credential_env_var,omitempty"`

	Status           string            `json:"status"`
	LastCheckedAt    *time.Time        `json:"last_checked_at,omitempty"`
	LastCheckStatus  string            `json:"last_check_status,omitempty"`
	LastCheckMessage string            `json:"last_check_message,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

func (c *CloudConnection) HasStoredCredential() bool {
	return c != nil && (c.HasClientSecret() || c.HasAWSSecretAccessKey())
}

func (c *CloudConnection) HasClientSecret() bool {
	return c != nil && strings.TrimSpace(c.ClientSecretCiphertext) != "" && strings.TrimSpace(c.ClientSecretWrappedKey) != ""
}

func (c *CloudConnection) HasAWSSecretAccessKey() bool {
	return c != nil && strings.TrimSpace(c.AWSSecretKeyCiphertext) != "" && strings.TrimSpace(c.AWSSecretKeyWrappedKey) != ""
}

func (c *CloudConnection) HasAWSExternalID() bool {
	return c != nil && strings.TrimSpace(c.AWSExternalIDCiphertext) != "" && strings.TrimSpace(c.AWSExternalIDWrappedKey) != ""
}

func (c *CloudConnection) CredentialSource() string {
	switch {
	case c == nil:
		return ""
	case c.AuthMode == CloudAuthAzureIdentity || c.AuthMode == CloudAuthAWSAmbient || c.AuthMode == CloudAuthAWSAssumeRole:
		return "workload_identity"
	case strings.TrimSpace(c.CredentialEnvVar) != "":
		return "env_var"
	case c.HasStoredCredential():
		return "inline_encrypted"
	default:
		return "unset"
	}
}

func (c *CloudConnection) Validate() error {
	if c == nil {
		return errors.New("cloud connection is required")
	}
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("cloud connection id is required")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("cloud connection name is required")
	}
	if c.Status == "" {
		c.Status = "active"
	}
	switch c.Status {
	case "active", "disabled", "error":
	default:
		return fmt.Errorf("unsupported cloud connection status %q", c.Status)
	}

	switch c.Provider {
	case CloudProviderAzure:
		if c.AzureCloud == "" {
			c.AzureCloud = AzureCloudPublic
		}
		switch c.AzureCloud {
		case AzureCloudPublic, AzureCloudUSGovernment, AzureCloudChina:
		default:
			return fmt.Errorf("unsupported Azure cloud %q", c.AzureCloud)
		}
		if strings.TrimSpace(c.AzureSubscriptionID) == "" {
			return errors.New("azure subscription id is required")
		}
		switch c.AuthMode {
		case CloudAuthAzureIdentity:
		case CloudAuthAzureServicePrincipal:
			if strings.TrimSpace(c.AzureTenantID) == "" || strings.TrimSpace(c.AzureClientID) == "" {
				return errors.New("azure tenant id and client id are required for service-principal authentication")
			}
		default:
			return fmt.Errorf("unsupported Azure authentication mode %q", c.AuthMode)
		}
	case CloudProviderAWS:
		if c.AWSPartition == "" {
			c.AWSPartition = AWSPartitionCommercial
		}
		switch c.AWSPartition {
		case AWSPartitionCommercial, AWSPartitionGovCloud, AWSPartitionChina:
		default:
			return fmt.Errorf("unsupported AWS partition %q", c.AWSPartition)
		}
		if strings.TrimSpace(c.AWSRegion) == "" {
			return errors.New("aws region is required")
		}
		region := strings.ToLower(strings.TrimSpace(c.AWSRegion))
		switch c.AWSPartition {
		case AWSPartitionChina:
			if !strings.HasPrefix(region, "cn-") {
				return fmt.Errorf("aws partition %q requires a cn-* region", c.AWSPartition)
			}
		case AWSPartitionGovCloud:
			if !strings.HasPrefix(region, "us-gov-") {
				return fmt.Errorf("aws partition %q requires a us-gov-* region", c.AWSPartition)
			}
		case AWSPartitionCommercial:
			if strings.HasPrefix(region, "cn-") || strings.HasPrefix(region, "us-gov-") {
				return fmt.Errorf("aws commercial partition cannot use sovereign region %q", c.AWSRegion)
			}
		}
		if accountID := strings.TrimSpace(c.AWSAccountID); accountID != "" && !isTwelveDigitAWSAccountID(accountID) {
			return errors.New("aws account id must contain exactly 12 digits")
		}
		switch c.AuthMode {
		case CloudAuthAWSAmbient:
		case CloudAuthAWSAssumeRole:
			if strings.TrimSpace(c.AWSRoleARN) == "" {
				return errors.New("aws role arn is required for assume-role authentication")
			}
			if !strings.HasPrefix(strings.TrimSpace(c.AWSRoleARN), "arn:"+c.AWSPartition+":iam::") {
				return fmt.Errorf("aws role arn partition must match %q", c.AWSPartition)
			}
		case CloudAuthAWSStaticKeys:
			if strings.TrimSpace(c.AWSAccessKeyID) == "" {
				return errors.New("aws access key id is required for static-key authentication")
			}
		default:
			return fmt.Errorf("unsupported AWS authentication mode %q", c.AuthMode)
		}
	default:
		return fmt.Errorf("unsupported cloud provider %q", c.Provider)
	}
	return nil
}

func isTwelveDigitAWSAccountID(value string) bool {
	if len(value) != 12 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// CloudConnectionStore is optional so existing storage test doubles do not
// need to grow methods unrelated to their tests.
type CloudConnectionStore interface {
	CreateCloudConnection(*CloudConnection) error
	UpdateCloudConnectionIfUnchanged(*CloudConnection, time.Time) (bool, error)
	UpdateCloudConnectionHealth(id, status, message string, checkedAt time.Time) error
	GetCloudConnection(id string) (*CloudConnection, error)
	ListCloudConnections() ([]*CloudConnection, error)
	DeleteCloudConnectionIfUnreferenced(id string) (bool, error)
}
