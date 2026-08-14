package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type cloudCredentialKind string

const (
	cloudCredentialAzureClientSecret cloudCredentialKind = "azure-client-secret"
	cloudCredentialAWSSecretKey      cloudCredentialKind = "aws-secret-access-key"
	cloudCredentialAWSExternalID     cloudCredentialKind = "aws-role-external-id"
)

func cloudCredentialAAD(connectionID string, kind cloudCredentialKind) []byte {
	return []byte("apexload:cloud-connection:" + connectionID + ":" + string(kind))
}

// CloudConnectionRequest is the write-only credential shape. Nil credential
// pointers preserve the existing envelope; an explicit empty value clears it.
type CloudConnectionRequest struct {
	ID                  string            `json:"id,omitempty"`
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	Provider            string            `json:"provider"`
	AuthMode            string            `json:"auth_mode"`
	AzureCloud          string            `json:"azure_cloud"`
	AzureTenantID       string            `json:"azure_tenant_id"`
	AzureSubscriptionID string            `json:"azure_subscription_id"`
	AzureResourceGroup  string            `json:"azure_resource_group"`
	AzureClientID       string            `json:"azure_client_id"`
	AWSPartition        string            `json:"aws_partition"`
	AWSAccountID        string            `json:"aws_account_id"`
	AWSRegion           string            `json:"aws_region"`
	AWSAccessKeyID      string            `json:"aws_access_key_id"`
	AWSRoleARN          string            `json:"aws_role_arn"`
	CredentialEnvVar    *string           `json:"credential_env_var,omitempty"`
	ClientSecret        *string           `json:"client_secret,omitempty"`
	AWSSecretAccessKey  *string           `json:"aws_secret_access_key,omitempty"`
	AWSExternalID       *string           `json:"aws_external_id,omitempty"`
	Status              string            `json:"status"`
	Labels              map[string]string `json:"labels"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

type CloudConnectionResponse struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Description         string            `json:"description,omitempty"`
	Provider            string            `json:"provider"`
	AuthMode            string            `json:"auth_mode"`
	AzureCloud          string            `json:"azure_cloud,omitempty"`
	AzureTenantID       string            `json:"azure_tenant_id,omitempty"`
	AzureSubscriptionID string            `json:"azure_subscription_id,omitempty"`
	AzureResourceGroup  string            `json:"azure_resource_group,omitempty"`
	AzureClientID       string            `json:"azure_client_id,omitempty"`
	AWSPartition        string            `json:"aws_partition,omitempty"`
	AWSAccountID        string            `json:"aws_account_id,omitempty"`
	AWSRegion           string            `json:"aws_region,omitempty"`
	AWSAccessKeyID      string            `json:"aws_access_key_id,omitempty"`
	AWSRoleARN          string            `json:"aws_role_arn,omitempty"`
	CredentialEnvVar    string            `json:"credential_env_var,omitempty"`
	CredentialSource    string            `json:"credential_source"`
	HasClientSecret     bool              `json:"has_client_secret"`
	HasAWSExternalID    bool              `json:"has_aws_external_id"`
	Status              string            `json:"status"`
	LastCheckedAt       *time.Time        `json:"last_checked_at,omitempty"`
	LastCheckStatus     string            `json:"last_check_status,omitempty"`
	LastCheckMessage    string            `json:"last_check_message,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

func cloudConnectionResponse(connection *storage.CloudConnection) CloudConnectionResponse {
	return CloudConnectionResponse{
		ID: connection.ID, Name: connection.Name, Description: connection.Description,
		Provider: connection.Provider, AuthMode: connection.AuthMode,
		AzureCloud: connection.AzureCloud, AzureTenantID: connection.AzureTenantID,
		AzureSubscriptionID: connection.AzureSubscriptionID, AzureResourceGroup: connection.AzureResourceGroup,
		AzureClientID: connection.AzureClientID, AWSPartition: connection.AWSPartition,
		AWSAccountID: connection.AWSAccountID, AWSRegion: connection.AWSRegion,
		AWSAccessKeyID: connection.AWSAccessKeyID, AWSRoleARN: connection.AWSRoleARN,
		CredentialEnvVar: connection.CredentialEnvVar, CredentialSource: connection.CredentialSource(),
		HasClientSecret: connection.HasClientSecret() || connection.HasAWSSecretAccessKey(), HasAWSExternalID: connection.HasAWSExternalID(),
		Status: connection.Status, LastCheckedAt: connection.LastCheckedAt,
		LastCheckStatus: connection.LastCheckStatus, LastCheckMessage: connection.LastCheckMessage,
		Labels: connection.Labels, CreatedAt: connection.CreatedAt, UpdatedAt: connection.UpdatedAt,
	}
}

func (s *Server) requireCloudStore(w http.ResponseWriter) (storage.CloudConnectionStore, bool) {
	if s.cloudStore == nil {
		writeError(w, http.StatusServiceUnavailable, "this controller's storage backend does not support cloud connections")
		return nil, false
	}
	return s.cloudStore, true
}

func (s *Server) handleCreateCloudConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireCloudStore(w)
	if !ok {
		return
	}
	var req CloudConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cloud connection payload")
		return
	}
	connectionID, err := computeResourceIDForCreate(req.ID, "cloud connection")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	connection := &storage.CloudConnection{ID: connectionID}
	applyCloudConnectionRequest(connection, req)
	if err := s.sealSubmittedCloudCredentials(r.Context(), connection, req); err != nil {
		writeCloudCredentialError(w, err)
		return
	}
	if err := validateCloudConnectionCredential(connection); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.CreateCloudConnection(connection); err != nil {
		writeComputeConnectionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cloudConnectionResponse(connection))
}

func (s *Server) handleListCloudConnections(w http.ResponseWriter, _ *http.Request) {
	store, ok := s.requireCloudStore(w)
	if !ok {
		return
	}
	connections, err := store.ListCloudConnections()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list cloud connections")
		return
	}
	items := make([]CloudConnectionResponse, 0, len(connections))
	for _, connection := range connections {
		items = append(items, cloudConnectionResponse(connection))
	}
	writeJSON(w, http.StatusOK, map[string]any{"cloud_connections": items, "total": len(items)})
}

func (s *Server) handleGetCloudConnection(w http.ResponseWriter, r *http.Request) {
	connection, ok := s.loadCloudConnection(w, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, cloudConnectionResponse(connection))
}

func (s *Server) handleUpdateCloudConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireCloudStore(w)
	if !ok {
		return
	}
	connection, ok := s.loadCloudConnection(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req CloudConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cloud connection payload")
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "updated_at is required to update a cloud connection")
		return
	}
	applyCloudConnectionRequest(connection, req)
	if err := s.sealSubmittedCloudCredentials(r.Context(), connection, req); err != nil {
		writeCloudCredentialError(w, err)
		return
	}
	if err := validateCloudConnectionCredential(connection); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := store.UpdateCloudConnectionIfUnchanged(connection, req.UpdatedAt)
	if err != nil {
		writeComputeConnectionError(w, err)
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "cloud connection changed since it was loaded; reload and retry")
		return
	}
	writeJSON(w, http.StatusOK, cloudConnectionResponse(connection))
}

func (s *Server) handleDeleteCloudConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireCloudStore(w)
	if !ok {
		return
	}
	deleted, err := store.DeleteCloudConnectionIfUnreferenced(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "cloud connection not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete cloud connection")
		return
	}
	if !deleted {
		writeError(w, http.StatusConflict, "cloud connection is still used by one or more execution targets; repoint them first")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loadCloudConnection(w http.ResponseWriter, id string) (*storage.CloudConnection, bool) {
	store, ok := s.requireCloudStore(w)
	if !ok {
		return nil, false
	}
	connection, err := store.GetCloudConnection(strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "cloud connection not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load cloud connection")
		return nil, false
	}
	return connection, true
}

func applyCloudConnectionRequest(connection *storage.CloudConnection, req CloudConnectionRequest) {
	connection.Name = strings.TrimSpace(req.Name)
	connection.Description = strings.TrimSpace(req.Description)
	connection.Provider = strings.TrimSpace(req.Provider)
	connection.AuthMode = strings.TrimSpace(req.AuthMode)
	connection.AzureCloud = strings.TrimSpace(req.AzureCloud)
	connection.AzureTenantID = strings.TrimSpace(req.AzureTenantID)
	connection.AzureSubscriptionID = strings.TrimSpace(req.AzureSubscriptionID)
	connection.AzureResourceGroup = strings.TrimSpace(req.AzureResourceGroup)
	connection.AzureClientID = strings.TrimSpace(req.AzureClientID)
	connection.AWSPartition = strings.TrimSpace(req.AWSPartition)
	connection.AWSAccountID = strings.TrimSpace(req.AWSAccountID)
	connection.AWSRegion = strings.TrimSpace(req.AWSRegion)
	connection.AWSAccessKeyID = strings.TrimSpace(req.AWSAccessKeyID)
	connection.AWSRoleARN = strings.TrimSpace(req.AWSRoleARN)
	if req.CredentialEnvVar != nil {
		connection.CredentialEnvVar = strings.TrimSpace(*req.CredentialEnvVar)
	}
	connection.Status = firstNonEmpty(strings.TrimSpace(req.Status), "active")
	connection.Labels = req.Labels
}

func (s *Server) sealSubmittedCloudCredentials(ctx context.Context, connection *storage.CloudConnection, req CloudConnectionRequest) error {
	for _, item := range []struct {
		value **string
		kind  cloudCredentialKind
		set   func(string, string)
	}{
		{&req.ClientSecret, cloudCredentialAzureClientSecret, func(ciphertext, wrapped string) {
			connection.ClientSecretCiphertext, connection.ClientSecretWrappedKey = ciphertext, wrapped
		}},
		{&req.AWSSecretAccessKey, cloudCredentialAWSSecretKey, func(ciphertext, wrapped string) {
			connection.AWSSecretKeyCiphertext, connection.AWSSecretKeyWrappedKey = ciphertext, wrapped
		}},
		{&req.AWSExternalID, cloudCredentialAWSExternalID, func(ciphertext, wrapped string) {
			connection.AWSExternalIDCiphertext, connection.AWSExternalIDWrappedKey = ciphertext, wrapped
		}},
	} {
		if *item.value == nil {
			continue
		}
		value := **item.value
		if strings.TrimSpace(value) == "" {
			item.set("", "")
			continue
		}
		ciphertext, wrapped, err := sealCredentialEnvelope(ctx, s.clusterSecretWrapper,
			cloudCredentialAAD(connection.ID, item.kind), "cloud credential", value)
		if err != nil {
			return err
		}
		item.set(ciphertext, wrapped)
	}
	return nil
}

func validateCloudConnectionCredential(connection *storage.CloudConnection) error {
	if err := connection.Validate(); err != nil {
		return err
	}
	envConfigured := strings.TrimSpace(connection.CredentialEnvVar) != ""
	switch connection.AuthMode {
	case storage.CloudAuthAzureServicePrincipal:
		if !connection.HasClientSecret() && !envConfigured {
			return errors.New("azure service-principal authentication requires an encrypted client secret or credential_env_var")
		}
	case storage.CloudAuthAWSStaticKeys:
		if !connection.HasAWSSecretAccessKey() && !envConfigured {
			return errors.New("aws static-key authentication requires an encrypted secret access key or credential_env_var")
		}
	case storage.CloudAuthAWSAssumeRole:
		if envConfigured {
			return errors.New("credential_env_var is not supported for assume-role authentication; the source identity is ambient and the optional external ID is stored separately")
		}
	}
	return nil
}

func writeCloudCredentialError(w http.ResponseWriter, err error) {
	if errors.Is(err, errClusterCredentialEncryptionUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "cloud credential encryption is not configured on this controller; configure a key wrapper before storing cloud credentials")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// ExecutionTargetRequest excludes health and capabilities, which are derived
// by the controller and cannot be asserted by a registration client.
type ExecutionTargetRequest struct {
	ID                       string                                `json:"id,omitempty"`
	Name                     string                                `json:"name"`
	Description              string                                `json:"description"`
	Kind                     string                                `json:"kind"`
	CloudConnectionID        string                                `json:"cloud_connection_id"`
	ClusterConnectionID      string                                `json:"cluster_connection_id"`
	Region                   string                                `json:"region"`
	ConnectivityMode         string                                `json:"connectivity_mode"`
	StandingOwner            string                                `json:"standing_owner"`
	BurstOwner               string                                `json:"burst_owner"`
	ArchiveMode              string                                `json:"archive_mode"`
	EnrollmentToken          *string                               `json:"enrollment_token,omitempty"`
	EnrollmentTokenExpiresAt *time.Time                            `json:"enrollment_token_expires_at,omitempty"`
	AzureContainerApps       *storage.AzureContainerAppsTargetSpec `json:"azure_container_apps,omitempty"`
	AWSECSFargate            *storage.AWSECSFargateTargetSpec      `json:"aws_ecs_fargate,omitempty"`
	Static                   *storage.StaticExecutionTargetSpec    `json:"static,omitempty"`
	RuntimeProfiles          []storage.RuntimeProfile              `json:"runtime_profiles,omitempty"`
	Status                   string                                `json:"status"`
	Labels                   map[string]string                     `json:"labels"`
	UpdatedAt                time.Time                             `json:"updated_at"`
}

type ExecutionTargetTestResponse struct {
	Status       string                     `json:"status"`
	Summary      string                     `json:"summary"`
	Probes       []ClusterProbe             `json:"probes"`
	Capabilities storage.TargetCapabilities `json:"capabilities"`
	CheckedAt    time.Time                  `json:"checked_at"`
}

func (s *Server) requireExecutionTargetStore(w http.ResponseWriter) (storage.ExecutionTargetStore, bool) {
	if s.executionTargetStore == nil {
		writeError(w, http.StatusServiceUnavailable, "this controller's storage backend does not support execution targets")
		return nil, false
	}
	return s.executionTargetStore, true
}

func (s *Server) handleCreateExecutionTarget(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireExecutionTargetStore(w)
	if !ok {
		return
	}
	var req ExecutionTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid execution target payload")
		return
	}
	targetID, err := executionTargetIDForCreate(req.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target := &storage.ExecutionTarget{ID: targetID}
	applyExecutionTargetRequest(target, req)
	if target.ConnectivityMode == storage.ConnectivityReverse && target.EnrollmentToken == "" {
		target.EnrollmentToken = uuid.NewString() + uuid.NewString()
	}
	if target.EnrollmentToken != "" {
		expiresAt := time.Time{}
		if req.EnrollmentTokenExpiresAt != nil {
			expiresAt = req.EnrollmentTokenExpiresAt.UTC()
		}
		if err := storage.RotateExecutionTargetEnrollmentToken(target, target.EnrollmentToken, expiresAt, time.Now().UTC()); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	target.Capabilities = configuredTargetCapabilities(target)
	if err := target.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		writeComputeConnectionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, target)
}

var executionTargetIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func executionTargetIDForCreate(requested string) (string, error) {
	return computeResourceIDForCreate(requested, "execution target")
}

func computeResourceIDForCreate(requested, resource string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return uuid.NewString(), nil
	}
	if !executionTargetIDPattern.MatchString(requested) {
		return "", fmt.Errorf("%s id must be 1-128 characters using letters, numbers, period, underscore, colon, or hyphen", resource)
	}
	return requested, nil
}

func (s *Server) handleListExecutionTargets(w http.ResponseWriter, _ *http.Request) {
	store, ok := s.requireExecutionTargetStore(w)
	if !ok {
		return
	}
	targets, err := store.ListExecutionTargets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list execution targets")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"execution_targets": targets, "total": len(targets)})
}

func (s *Server) handleGetExecutionTarget(w http.ResponseWriter, r *http.Request) {
	target, ok := s.loadExecutionTarget(w, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func (s *Server) handleUpdateExecutionTarget(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireExecutionTargetStore(w)
	if !ok {
		return
	}
	target, ok := s.loadExecutionTarget(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req ExecutionTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid execution target payload")
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "updated_at is required to update an execution target")
		return
	}
	applyExecutionTargetRequest(target, req)
	if req.EnrollmentToken != nil {
		expiresAt := time.Time{}
		if req.EnrollmentTokenExpiresAt != nil {
			expiresAt = req.EnrollmentTokenExpiresAt.UTC()
		}
		if err := storage.RotateExecutionTargetEnrollmentToken(target, *req.EnrollmentToken, expiresAt, time.Now().UTC()); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if req.EnrollmentTokenExpiresAt != nil {
		writeError(w, http.StatusBadRequest, "enrollment_token_expires_at can change only when rotating enrollment_token")
		return
	}
	target.Capabilities = configuredTargetCapabilities(target)
	if err := target.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := store.UpdateExecutionTargetIfUnchanged(target, req.UpdatedAt)
	if err != nil {
		writeComputeConnectionError(w, err)
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "execution target changed since it was loaded; reload and retry")
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func (s *Server) handleDeleteExecutionTarget(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireExecutionTargetStore(w)
	if !ok {
		return
	}
	deleted, err := store.DeleteExecutionTargetIfUnreferenced(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "execution target not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete execution target")
		return
	}
	if !deleted {
		pools, _ := store.ListFleetPoolsForExecutionTarget(r.PathValue("id"))
		names := make([]string, 0, len(pools))
		for _, pool := range pools {
			names = append(names, pool.Name)
		}
		writeError(w, http.StatusConflict, fmt.Sprintf("execution target is still referenced; bound fleet pools: %s", strings.Join(names, ", ")))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loadExecutionTarget(w http.ResponseWriter, id string) (*storage.ExecutionTarget, bool) {
	store, ok := s.requireExecutionTargetStore(w)
	if !ok {
		return nil, false
	}
	target, err := store.GetExecutionTarget(strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "execution target not found")
		return nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load execution target")
		return nil, false
	}
	return target, true
}

func applyExecutionTargetRequest(target *storage.ExecutionTarget, req ExecutionTargetRequest) {
	target.Name = strings.TrimSpace(req.Name)
	target.Description = strings.TrimSpace(req.Description)
	target.Kind = strings.TrimSpace(req.Kind)
	target.CloudConnectionID = strings.TrimSpace(req.CloudConnectionID)
	target.ClusterConnectionID = strings.TrimSpace(req.ClusterConnectionID)
	target.Region = strings.TrimSpace(req.Region)
	target.ConnectivityMode = strings.TrimSpace(req.ConnectivityMode)
	target.StandingOwner = strings.TrimSpace(req.StandingOwner)
	target.BurstOwner = strings.TrimSpace(req.BurstOwner)
	target.ArchiveMode = strings.TrimSpace(req.ArchiveMode)
	if req.EnrollmentToken != nil {
		target.EnrollmentToken = strings.TrimSpace(*req.EnrollmentToken)
	}
	target.AzureContainerApps = req.AzureContainerApps
	target.AWSECSFargate = req.AWSECSFargate
	target.Static = req.Static
	target.RuntimeProfiles = req.RuntimeProfiles
	target.Status = firstNonEmpty(strings.TrimSpace(req.Status), "active")
	target.Labels = req.Labels
}

func configuredTargetCapabilities(target *storage.ExecutionTarget) storage.TargetCapabilities {
	capabilities := storage.TargetCapabilities{Details: map[string]string{}}
	switch target.Kind {
	case storage.ExecutionTargetKubernetes:
		capabilities.CanScale = target.StandingOwner == storage.CapacityOwnerApex
		capabilities.CanReachDirectly = true
		capabilities.Details["capacity_model"] = "statefulset"
	case storage.ExecutionTargetAzureContainerApps:
		capabilities.SupportsReverse = true
		if target.AzureContainerApps != nil {
			capabilities.CanScale = target.StandingOwner == storage.CapacityOwnerApex && target.AzureContainerApps.WarmContainerApp != ""
			capabilities.CanLaunch = target.BurstOwner == storage.CapacityOwnerApex && target.AzureContainerApps.BurstJob != ""
		}
		capabilities.Details["capacity_model"] = "container_app_and_job"
	case storage.ExecutionTargetAWSECSFargate:
		capabilities.CanReachDirectly = true
		capabilities.SupportsReverse = true
		if target.AWSECSFargate != nil {
			capabilities.CanScale = target.StandingOwner == storage.CapacityOwnerApex && target.AWSECSFargate.WarmService != ""
			capabilities.CanLaunch = target.BurstOwner == storage.CapacityOwnerApex && target.AWSECSFargate.BurstTaskDefinition != ""
		}
		capabilities.Details["capacity_model"] = "ecs_service_and_run_task"
	case storage.ExecutionTargetStatic:
		capabilities.CanReachDirectly = true
		capabilities.Details["capacity_model"] = "static"
	}
	for _, profile := range target.RuntimeProfiles {
		if profile.CPUMillis > 0 && profile.MemoryMiB > 0 {
			capabilities.ResourceShapes = append(capabilities.ResourceShapes, fmt.Sprintf("%dm/%dMi", profile.CPUMillis, profile.MemoryMiB))
		}
		if target.Kind == storage.ExecutionTargetAWSECSFargate && strings.EqualFold(strings.TrimSpace(profile.CapacityType), "spot") {
			capabilities.SupportsSpot = true
		}
	}
	sort.Strings(capabilities.ResourceShapes)
	return capabilities
}

func (s *Server) handleGetExecutionTargetCapabilities(w http.ResponseWriter, r *http.Request) {
	target, ok := s.loadExecutionTarget(w, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"execution_target_id": target.ID, "capabilities": target.Capabilities, "runtime_profiles": target.RuntimeProfiles})
}

func (s *Server) handleTestExecutionTarget(w http.ResponseWriter, r *http.Request) {
	target, ok := s.loadExecutionTarget(w, r.PathValue("id"))
	if !ok {
		return
	}
	result := s.probeExecutionTarget(r.Context(), target)
	if s.executionTargetStore != nil {
		_ = s.executionTargetStore.UpdateExecutionTargetHealth(target.ID, result.Status, result.Summary, result.CheckedAt, result.Capabilities)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) probeExecutionTarget(ctx context.Context, target *storage.ExecutionTarget) ExecutionTargetTestResponse {
	result := ExecutionTargetTestResponse{CheckedAt: time.Now().UTC(), Capabilities: configuredTargetCapabilities(target)}
	if err := target.Validate(); err != nil {
		result.Probes = []ClusterProbe{{Probe: "configuration", Message: err.Error()}}
		result.Status, result.Summary = summarizeClusterProbes(result.Probes)
		return result
	}
	result.Probes = append(result.Probes, ClusterProbe{Probe: "configuration", OK: true, Message: "target configuration is structurally valid"})
	if target.Kind == storage.ExecutionTargetKubernetes {
		if s.clusterStore == nil {
			result.Probes = append(result.Probes, ClusterProbe{Probe: "cluster_connection", Message: "cluster connections are unsupported by this storage backend"})
			result.Status, result.Summary = summarizeClusterProbes(result.Probes)
			return result
		}
		connection, err := s.clusterStore.GetClusterConnection(target.ClusterConnectionID)
		if err != nil {
			result.Probes = append(result.Probes, ClusterProbe{Probe: "cluster_connection", Message: "cluster connection is unavailable"})
		} else {
			clusterResult := s.probeClusterConnection(ctx, connection)
			result.Probes = append(result.Probes, clusterResult.Probes...)
		}
		result.Status, result.Summary = summarizeClusterProbes(result.Probes)
		return result
	}
	if target.Kind == storage.ExecutionTargetStatic {
		result.Probes = append(result.Probes, ClusterProbe{Probe: "connectivity", OK: true, Message: "static generator addresses are configured; runtime health is reported by fleet inventory"})
		result.Status, result.Summary = summarizeClusterProbes(result.Probes)
		return result
	}
	if s.cloudStore == nil {
		result.Probes = append(result.Probes, ClusterProbe{Probe: "cloud_connection", Message: "cloud connections are unsupported by this storage backend"})
		result.Status, result.Summary = summarizeClusterProbes(result.Probes)
		return result
	}
	connection, err := s.cloudStore.GetCloudConnection(target.CloudConnectionID)
	if err != nil {
		result.Probes = append(result.Probes, ClusterProbe{Probe: "cloud_connection", Message: "cloud connection is unavailable"})
	} else {
		credentialOK := validateCloudConnectionCredential(connection) == nil
		result.Probes = append(result.Probes, ClusterProbe{Probe: "identity", OK: credentialOK, Message: map[bool]string{true: "cloud identity configuration is present", false: "cloud identity configuration is incomplete"}[credentialOK]})
	}
	result.Probes = append(result.Probes, sidecarProbe(target), providerIdentityProbe(target))
	if resolver, ok := s.capacityBackends.(capacityBackendTargetResolver); ok {
		backend, err := resolver.BackendForTarget(ctx, target)
		if err != nil {
			result.Probes = append(result.Probes, ClusterProbe{Probe: "provider_api", Message: err.Error()})
		} else {
			for _, probe := range backend.Validate(ctx) {
				result.Probes = append(result.Probes, ClusterProbe{Probe: probe.Probe, OK: probe.OK, Optional: probe.Optional, Message: probe.Message})
			}
		}
	} else {
		result.Probes = append(result.Probes, ClusterProbe{Probe: "provider_api", Optional: true, Message: "live provider validation is unavailable on this controller"})
	}
	result.Status, result.Summary = summarizeClusterProbes(result.Probes)
	return result
}

func providerIdentityProbe(target *storage.ExecutionTarget) ClusterProbe {
	configured := false
	switch target.Kind {
	case storage.ExecutionTargetAzureContainerApps:
		configured = target.AzureContainerApps != nil && strings.TrimSpace(target.AzureContainerApps.ExpectedPrincipalID) != ""
	case storage.ExecutionTargetAWSECSFargate:
		configured = target.AWSECSFargate != nil && strings.TrimSpace(target.AWSECSFargate.ExpectedTaskRoleARN) != ""
	default:
		return ClusterProbe{Probe: "provider_identity", OK: true, Optional: true, Message: "provider identity attestation does not apply"}
	}
	if configured {
		return ClusterProbe{Probe: "provider_identity", OK: true, Message: "expected generator identity is pinned for provider attestation"}
	}
	return ClusterProbe{Probe: "provider_identity", Message: "expected generator identity is required so a fresh ephemeral launch cannot self-register under an untrusted identity"}
}

func sidecarProbe(target *storage.ExecutionTarget) ClusterProbe {
	missing := make(map[string]struct{})
	for _, profile := range target.RuntimeProfiles {
		available := make(map[string]struct{}, len(profile.Sidecars))
		for _, sidecar := range profile.Sidecars {
			available[strings.ToLower(strings.TrimSpace(sidecar))] = struct{}{}
		}
		for _, protocol := range profile.Protocols {
			var required string
			switch strings.ToLower(strings.TrimSpace(protocol)) {
			case "bruno":
				required = "bruno"
			case "java_vuser":
				required = "java-vuser-runner"
			}
			if required != "" {
				if _, ok := available[required]; !ok {
					missing[required] = struct{}{}
				}
			}
		}
	}
	if len(missing) == 0 {
		return ClusterProbe{Probe: "sidecars", OK: true, Message: "declared runtime protocols have their required loopback sidecars"}
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return ClusterProbe{Probe: "sidecars", Message: "runtime profile is missing required sidecars: " + strings.Join(names, ", ")}
}

func writeComputeConnectionError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrExecutionTargetReferenced) || errors.Is(err, storage.ErrCloudConnectionReferenced) || errors.Is(err, storage.ErrCloudConnectionProviderImmutable) || errors.Is(err, storage.ErrCloudConnectionScopeImmutable) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique") {
		writeError(w, http.StatusConflict, "a compute resource with that name already exists")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
