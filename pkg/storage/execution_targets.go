package storage

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ExecutionTargetKubernetes         = "kubernetes"
	ExecutionTargetAzureContainerApps = "azure_container_apps"
	ExecutionTargetAWSECSFargate      = "aws_ecs_fargate"
	ExecutionTargetStatic             = "static"

	ConnectivityDirect  = "direct"
	ConnectivityReverse = "reverse"

	CapacityOwnerApex     = "apex"
	CapacityOwnerExternal = "external"
	CapacityOwnerDisabled = "disabled"

	ArchiveModeControllerPull = "controller_pull"
	ArchiveModeObjectDirect   = "object_direct"

	MinExecutionTargetEnrollmentTokenBytes = 32
	DefaultExecutionTargetEnrollmentTTL    = 90 * 24 * time.Hour
	ExecutionTargetTokenRotationOverlap    = 24 * time.Hour
)

var ErrExecutionTargetReferenced = errors.New("execution target is referenced")

type AzureContainerAppsTargetSpec struct {
	ResourceGroup       string `json:"resource_group"`
	EnvironmentName     string `json:"environment_name"`
	WarmContainerApp    string `json:"warm_container_app,omitempty"`
	BurstJob            string `json:"burst_job,omitempty"`
	ExpectedPrincipalID string `json:"expected_principal_id,omitempty"`
}

type AWSECSFargateTargetSpec struct {
	Cluster             string   `json:"cluster"`
	WarmService         string   `json:"warm_service,omitempty"`
	BurstTaskDefinition string   `json:"burst_task_definition,omitempty"`
	Subnets             []string `json:"subnets,omitempty"`
	SecurityGroups      []string `json:"security_groups,omitempty"`
	AssignPublicIP      string   `json:"assign_public_ip,omitempty"` // enabled | disabled
	ExpectedTaskRoleARN string   `json:"expected_task_role_arn,omitempty"`
}

type StaticExecutionTargetSpec struct {
	Targets []string `json:"targets"`
}

// RuntimeProfile is an immutable, discovered provider template. Apex chooses
// among profiles; Terraform or the platform owner changes their task/app shape.
type RuntimeProfile struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	CPUMillis           int      `json:"cpu_millis,omitempty"`
	MemoryMiB           int      `json:"memory_mib,omitempty"`
	EphemeralStorageGiB int      `json:"ephemeral_storage_gib,omitempty"`
	Architecture        string   `json:"architecture,omitempty"`
	ImageDigest         string   `json:"image_digest,omitempty"`
	TemplateRevision    string   `json:"template_revision,omitempty"`
	CapacityType        string   `json:"capacity_type,omitempty"`
	Sidecars            []string `json:"sidecars,omitempty"`
	Protocols           []string `json:"protocols,omitempty"`
}

func (p RuntimeProfile) Validate() error {
	if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Name) == "" {
		return errors.New("runtime profile id and name are required")
	}
	if p.CPUMillis < 0 || p.MemoryMiB < 0 || p.EphemeralStorageGiB < 0 {
		return errors.New("runtime profile resources must not be negative")
	}
	return nil
}

type TargetCapabilities struct {
	CanScale         bool              `json:"can_scale"`
	CanLaunch        bool              `json:"can_launch"`
	CanReachDirectly bool              `json:"can_reach_directly"`
	SupportsReverse  bool              `json:"supports_reverse"`
	SupportsSpot     bool              `json:"supports_spot"`
	ResourceShapes   []string          `json:"resource_shapes,omitempty"`
	Details          map[string]string `json:"details,omitempty"`
}

// ExecutionTarget binds a provider-neutral placement to an existing cloud or
// Kubernetes connection. Spec fields are typed at the public boundary and
// stored as a versioned JSON projection by the storage implementation.
type ExecutionTarget struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Description         string `json:"description,omitempty"`
	Kind                string `json:"kind"`
	CloudConnectionID   string `json:"cloud_connection_id,omitempty"`
	ClusterConnectionID string `json:"cluster_connection_id,omitempty"`
	Region              string `json:"region,omitempty"`
	ConnectivityMode    string `json:"connectivity_mode"`
	StandingOwner       string `json:"standing_owner"`
	BurstOwner          string `json:"burst_owner"`
	ArchiveMode         string `json:"archive_mode"`
	// EnrollmentToken is accepted only on create/rotation and returned once.
	// Only its digest is persisted; list/get responses expose the configured bit.
	EnrollmentToken                   string     `json:"enrollment_token,omitempty"`
	EnrollmentTokenHash               string     `json:"-"`
	EnrollmentTokenConfigured         bool       `json:"enrollment_token_configured"`
	EnrollmentTokenExpiresAt          *time.Time `json:"enrollment_token_expires_at,omitempty"`
	PreviousEnrollmentTokenHash       string     `json:"-"`
	PreviousEnrollmentTokenValidUntil *time.Time `json:"-"`

	AzureContainerApps *AzureContainerAppsTargetSpec `json:"azure_container_apps,omitempty"`
	AWSECSFargate      *AWSECSFargateTargetSpec      `json:"aws_ecs_fargate,omitempty"`
	Static             *StaticExecutionTargetSpec    `json:"static,omitempty"`
	RuntimeProfiles    []RuntimeProfile              `json:"runtime_profiles,omitempty"`
	Capabilities       TargetCapabilities            `json:"capabilities"`

	Status           string            `json:"status"`
	LastCheckedAt    *time.Time        `json:"last_checked_at,omitempty"`
	LastCheckStatus  string            `json:"last_check_status,omitempty"`
	LastCheckMessage string            `json:"last_check_message,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

func (t *ExecutionTarget) Validate() error {
	if t == nil {
		return errors.New("execution target is required")
	}
	if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.Name) == "" {
		return errors.New("execution target id and name are required")
	}
	if t.ConnectivityMode == "" {
		if t.Kind == ExecutionTargetKubernetes || t.Kind == ExecutionTargetStatic {
			t.ConnectivityMode = ConnectivityDirect
		} else {
			t.ConnectivityMode = ConnectivityReverse
		}
	}
	if t.ConnectivityMode != ConnectivityDirect && t.ConnectivityMode != ConnectivityReverse {
		return fmt.Errorf("unsupported connectivity mode %q", t.ConnectivityMode)
	}
	if t.EnrollmentToken != "" && len(t.EnrollmentToken) < MinExecutionTargetEnrollmentTokenBytes {
		return fmt.Errorf("reverse enrollment token must contain at least %d bytes", MinExecutionTargetEnrollmentTokenBytes)
	}
	if t.StandingOwner == "" {
		t.StandingOwner = CapacityOwnerApex
	}
	if t.BurstOwner == "" {
		t.BurstOwner = CapacityOwnerDisabled
	}
	if t.StandingOwner != CapacityOwnerApex && t.StandingOwner != CapacityOwnerExternal {
		return fmt.Errorf("unsupported standing capacity owner %q", t.StandingOwner)
	}
	if t.BurstOwner != CapacityOwnerApex && t.BurstOwner != CapacityOwnerDisabled {
		return fmt.Errorf("unsupported burst capacity owner %q", t.BurstOwner)
	}
	if t.ArchiveMode == "" {
		t.ArchiveMode = ArchiveModeControllerPull
	}
	if t.ArchiveMode == ArchiveModeObjectDirect {
		return errors.New("object-direct generator archives are not implemented; use controller_pull")
	}
	if t.ArchiveMode != ArchiveModeControllerPull {
		return fmt.Errorf("unsupported archive mode %q", t.ArchiveMode)
	}
	if t.Status == "" {
		t.Status = "active"
	}
	switch t.Status {
	case "active", "disabled", "error":
	default:
		return fmt.Errorf("unsupported execution target status %q", t.Status)
	}

	switch t.Kind {
	case ExecutionTargetKubernetes:
		if strings.TrimSpace(t.ClusterConnectionID) == "" || strings.TrimSpace(t.CloudConnectionID) != "" {
			return errors.New("kubernetes targets require exactly one cluster connection")
		}
		if t.AzureContainerApps != nil || t.AWSECSFargate != nil || t.Static != nil {
			return errors.New("kubernetes targets must not include a PaaS target specification")
		}
	case ExecutionTargetAzureContainerApps:
		if strings.TrimSpace(t.CloudConnectionID) == "" || strings.TrimSpace(t.ClusterConnectionID) != "" {
			return errors.New("azure container apps targets require exactly one cloud connection")
		}
		if t.AzureContainerApps == nil || t.AWSECSFargate != nil || t.Static != nil {
			return errors.New("azure container apps target specification is required")
		}
		spec := t.AzureContainerApps
		if strings.TrimSpace(spec.ResourceGroup) == "" || strings.TrimSpace(spec.EnvironmentName) == "" {
			return errors.New("azure resource group and container apps environment are required")
		}
		if strings.TrimSpace(spec.WarmContainerApp) == "" && strings.TrimSpace(spec.BurstJob) == "" {
			return errors.New("an azure warm container app or burst job is required")
		}
		if t.ConnectivityMode != ConnectivityReverse {
			return errors.New("azure container apps and jobs require reverse connectivity because replicas have no stable per-instance ingress")
		}
	case ExecutionTargetAWSECSFargate:
		if strings.TrimSpace(t.CloudConnectionID) == "" || strings.TrimSpace(t.ClusterConnectionID) != "" {
			return errors.New("aws ecs fargate targets require exactly one cloud connection")
		}
		if t.AWSECSFargate == nil || t.AzureContainerApps != nil || t.Static != nil {
			return errors.New("aws ecs fargate target specification is required")
		}
		spec := t.AWSECSFargate
		if strings.TrimSpace(spec.Cluster) == "" {
			return errors.New("aws ecs cluster is required")
		}
		if strings.TrimSpace(spec.WarmService) == "" && strings.TrimSpace(spec.BurstTaskDefinition) == "" {
			return errors.New("an aws warm service or burst task definition is required")
		}
		if strings.TrimSpace(spec.BurstTaskDefinition) != "" && (!containsNonEmptyString(spec.Subnets) || !containsNonEmptyString(spec.SecurityGroups)) {
			return errors.New("aws burst Fargate tasks require at least one subnet and security group")
		}
		if spec.AssignPublicIP != "" && spec.AssignPublicIP != "enabled" && spec.AssignPublicIP != "disabled" {
			return errors.New("aws assign_public_ip must be enabled or disabled")
		}
	case ExecutionTargetStatic:
		if strings.TrimSpace(t.CloudConnectionID) != "" || strings.TrimSpace(t.ClusterConnectionID) != "" {
			return errors.New("static targets must not reference a cloud or cluster connection")
		}
		if t.Static == nil || len(t.Static.Targets) == 0 || t.AzureContainerApps != nil || t.AWSECSFargate != nil {
			return errors.New("static targets require at least one address")
		}
	default:
		return fmt.Errorf("unsupported execution target kind %q", t.Kind)
	}

	seenProfiles := make(map[string]struct{}, len(t.RuntimeProfiles))
	for _, profile := range t.RuntimeProfiles {
		if err := profile.Validate(); err != nil {
			return err
		}
		if _, exists := seenProfiles[profile.ID]; exists {
			return fmt.Errorf("duplicate runtime profile id %q", profile.ID)
		}
		seenProfiles[profile.ID] = struct{}{}
	}
	return nil
}

func containsNonEmptyString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func SetExecutionTargetEnrollmentToken(target *ExecutionTarget, token string) error {
	return setExecutionTargetEnrollmentToken(target, token, time.Now().UTC().Add(DefaultExecutionTargetEnrollmentTTL))
}

func setExecutionTargetEnrollmentToken(target *ExecutionTarget, token string, expiresAt time.Time) error {
	if target == nil {
		return errors.New("execution target is required")
	}
	token = strings.TrimSpace(token)
	if len(token) < MinExecutionTargetEnrollmentTokenBytes {
		return fmt.Errorf("reverse enrollment token must contain at least %d bytes", MinExecutionTargetEnrollmentTokenBytes)
	}
	if expiresAt.IsZero() {
		return errors.New("reverse enrollment token expiry is required")
	}
	digest := sha256.Sum256([]byte(token))
	target.EnrollmentToken = token
	target.EnrollmentTokenHash = hex.EncodeToString(digest[:])
	target.EnrollmentTokenConfigured = true
	expiresAt = expiresAt.UTC()
	target.EnrollmentTokenExpiresAt = &expiresAt
	return nil
}

// RotateExecutionTargetEnrollmentToken replaces the verifier while retaining
// the previous token for a bounded overlap, allowing standing generators to
// reconnect during a rolling secret update without creating a permanent
// second credential.
func RotateExecutionTargetEnrollmentToken(target *ExecutionTarget, token string, expiresAt, now time.Time) error {
	if target == nil {
		return errors.New("execution target is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if expiresAt.IsZero() {
		expiresAt = now.Add(DefaultExecutionTargetEnrollmentTTL)
	}
	if !expiresAt.After(now) {
		return errors.New("reverse enrollment token expiry must be in the future")
	}
	previousHash := target.EnrollmentTokenHash
	previousValidUntil := now.Add(ExecutionTargetTokenRotationOverlap)
	if target.EnrollmentTokenExpiresAt != nil && target.EnrollmentTokenExpiresAt.Before(previousValidUntil) {
		previousValidUntil = target.EnrollmentTokenExpiresAt.UTC()
	}
	if err := setExecutionTargetEnrollmentToken(target, token, expiresAt); err != nil {
		return err
	}
	if previousHash != "" && previousValidUntil.After(now) {
		target.PreviousEnrollmentTokenHash = previousHash
		target.PreviousEnrollmentTokenValidUntil = &previousValidUntil
	} else {
		target.PreviousEnrollmentTokenHash = ""
		target.PreviousEnrollmentTokenValidUntil = nil
	}
	return nil
}

func VerifyExecutionTargetEnrollmentToken(target *ExecutionTarget, token string) bool {
	return VerifyExecutionTargetEnrollmentTokenAt(target, token, time.Now().UTC())
}

func VerifyExecutionTargetEnrollmentTokenAt(target *ExecutionTarget, token string, now time.Time) bool {
	if target == nil || token == "" {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	verifyHash := func(encoded string) bool {
		want, err := hex.DecodeString(encoded)
		return err == nil && len(want) == len(digest) && subtle.ConstantTimeCompare(want, digest[:]) == 1
	}
	currentValid := target.EnrollmentTokenExpiresAt != nil && now.Before(*target.EnrollmentTokenExpiresAt)
	previousValid := target.PreviousEnrollmentTokenValidUntil != nil && now.Before(*target.PreviousEnrollmentTokenValidUntil)
	currentMatch := verifyHash(target.EnrollmentTokenHash)
	previousMatch := verifyHash(target.PreviousEnrollmentTokenHash)
	return (currentValid && currentMatch) || (previousValid && previousMatch)
}

func ProviderForExecutionTargetKind(kind string) (string, bool) {
	switch strings.TrimSpace(kind) {
	case ExecutionTargetKubernetes:
		return DeploymentProviderKubernetes, true
	case ExecutionTargetAzureContainerApps:
		return DeploymentProviderAzureContainerApps, true
	case ExecutionTargetAWSECSFargate:
		return DeploymentProviderAWSECSFargate, true
	case ExecutionTargetStatic:
		return DeploymentProviderStatic, true
	default:
		return "", false
	}
}

type ExecutionTargetStore interface {
	CreateExecutionTarget(*ExecutionTarget) error
	UpdateExecutionTargetIfUnchanged(*ExecutionTarget, time.Time) (bool, error)
	UpdateExecutionTargetHealth(id, status, message string, checkedAt time.Time, capabilities TargetCapabilities) error
	GetExecutionTarget(id string) (*ExecutionTarget, error)
	ListExecutionTargets() ([]*ExecutionTarget, error)
	DeleteExecutionTargetIfUnreferenced(id string) (bool, error)
	ListFleetPoolsForExecutionTarget(id string) ([]*FleetPool, error)
}
