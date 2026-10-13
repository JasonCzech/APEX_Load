package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Administrator surface for registering Kubernetes and AKS control planes.
//
// Every route here is system-admin only. Even the redacted response carries the
// API server endpoint and Azure directory identifiers, which are not something
// a viewer or project role has any reason to see.

const clusterProbeTimeout = 10 * time.Second

// clusterProbeCooldown throttles the test endpoint so the dashboard cannot turn
// an administrator holding down a button into a burst of outbound connections.
const clusterProbeCooldown = 5 * time.Second

// clusterProbeThrottle bounds how often one connection may be probed. It is
// per-Server rather than package-global so two Server instances in the same
// process (tests, embedded desktop mode) cannot throttle each other.
type clusterProbeThrottle struct {
	mu      sync.Mutex
	lastRun map[string]time.Time
}

// claim reports whether a probe may run now, recording the attempt if so.
func (t *clusterProbeThrottle) claim(id string, now time.Time) bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastRun == nil {
		t.lastRun = make(map[string]time.Time)
	}
	if last, ok := t.lastRun[id]; ok && now.Sub(last) < clusterProbeCooldown {
		return false
	}
	t.lastRun[id] = now
	return true
}

// forget drops a deleted connection's entry so the map cannot grow without
// bound across a long-lived controller's connection churn.
func (t *clusterProbeThrottle) forget(id string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.lastRun, id)
}

// ClusterConnectionRequest is the write shape. Credentials are write-only: they
// appear here and never in a response.
type ClusterConnectionRequest struct {
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	Kind                 string            `json:"kind"`
	APIServerURL         string            `json:"api_server_url"`
	Namespace            string            `json:"namespace"`
	GeneratorSelector    string            `json:"generator_selector"`
	GeneratorStatefulSet string            `json:"generator_stateful_set"`
	GeneratorContainer   string            `json:"generator_container"`
	CACertificatePEM     string            `json:"ca_certificate_pem"`
	TLSMode              string            `json:"tls_mode"`
	AuthMode             string            `json:"auth_mode"`
	AzureTenantID        string            `json:"azure_tenant_id"`
	AzureClientID        string            `json:"azure_client_id"`
	AzureScope           string            `json:"azure_scope"`
	AzureAuthority       string            `json:"azure_authority"`
	AWSRegion            string            `json:"aws_region"`
	AWSClusterName       string            `json:"aws_cluster_name"`
	AWSCredentialMode    string            `json:"aws_credential_mode"`
	AWSAccessKeyID       string            `json:"aws_access_key_id"`
	AWSRoleARN           string            `json:"aws_role_arn"`
	AWSSTSEndpoint       string            `json:"aws_sts_endpoint"`
	Labels               map[string]string `json:"labels"`
	Status               string            `json:"status"`

	// ClientSecret and StaticToken are write-only. On update, an omitted value
	// means "keep the stored credential" — the handler carries the existing
	// envelope forward rather than clearing it.
	//
	// ClientSecretEnvVar follows the SAME presence-aware rule rather than the
	// plain replace-on-omit rule the other fields use. It is a credential
	// reference, so a client that omits it must not silently detach the
	// connection's credential; send an explicit empty string to clear it.
	ClientSecret       *string `json:"client_secret,omitempty"`
	StaticToken        *string `json:"static_token,omitempty"`
	ClientSecretEnvVar *string `json:"client_secret_env_var,omitempty"`
	// AWSSecretAccessKey and AWSExternalID follow the same omit-to-keep rule.
	AWSSecretAccessKey *string `json:"aws_secret_access_key,omitempty"`
	AWSExternalID      *string `json:"aws_external_id,omitempty"`

	// UpdatedAt is the optimistic-concurrency token, echoed from a prior read.
	UpdatedAt time.Time `json:"updated_at"`
}

// ClusterConnectionTestRequest probes a connection as its form describes it,
// before it is saved. ConnectionID names the stored connection an edit starts
// from: credentials the form leaves out are its stored ones, as on update.
type ClusterConnectionTestRequest struct {
	ConnectionID string                   `json:"connection_id,omitempty"`
	Connection   ClusterConnectionRequest `json:"connection"`
}

// ClusterConnectionResponse is the read shape: everything an administrator
// needs, and nothing that could reconstruct a credential.
type ClusterConnectionResponse struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	Kind                 string `json:"kind"`
	APIServerURL         string `json:"api_server_url,omitempty"`
	Namespace            string `json:"namespace"`
	GeneratorSelector    string `json:"generator_selector,omitempty"`
	GeneratorStatefulSet string `json:"generator_stateful_set,omitempty"`
	GeneratorContainer   string `json:"generator_container,omitempty"`
	CACertificatePEM     string `json:"ca_certificate_pem,omitempty"`
	TLSMode              string `json:"tls_mode"`
	AuthMode             string `json:"auth_mode"`
	AzureTenantID        string `json:"azure_tenant_id,omitempty"`
	AzureClientID        string `json:"azure_client_id,omitempty"`
	// The AWS identifiers are returned for the same reason AzureClientID is:
	// they are half a credential pair at most, an edit form must round-trip
	// them, and AWS itself displays the access key ID. The secret access key
	// and the role external ID are never returned.
	AWSRegion         string            `json:"aws_region,omitempty"`
	AWSClusterName    string            `json:"aws_cluster_name,omitempty"`
	AWSCredentialMode string            `json:"aws_credential_mode,omitempty"`
	AWSAccessKeyID    string            `json:"aws_access_key_id,omitempty"`
	AWSRoleARN        string            `json:"aws_role_arn,omitempty"`
	AWSSTSEndpoint    string            `json:"aws_sts_endpoint,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Status            string            `json:"status"`

	// HasAWSExternalID reports that an external ID is stored without revealing
	// it, so the edit form can render "leave blank to keep".
	HasAWSExternalID bool `json:"has_aws_external_id"`

	// HasClientSecret reports that a credential is stored without revealing
	// anything about it, so the UI can render "leave blank to keep".
	HasClientSecret bool `json:"has_client_secret"`
	// ClientSecretEnvVar is the NAME of an environment variable, never its
	// value, so it is safe to return — and it must be, or an edit form cannot
	// round-trip it and would clear the reference on save.
	ClientSecretEnvVar string     `json:"client_secret_env_var,omitempty"`
	CredentialSource   string     `json:"credential_source"`
	AuthDescription    string     `json:"auth_description,omitempty"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	LastCheckStatus    string     `json:"last_check_status,omitempty"`
	LastCheckMessage   string     `json:"last_check_message,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ClusterProbe is one capability check in a connection test.
type ClusterProbe struct {
	Probe      string `json:"probe"`
	OK         bool   `json:"ok"`
	Optional   bool   `json:"optional,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	Message    string `json:"message,omitempty"`
}

// ClusterTestResponse aggregates the probe results.
type ClusterTestResponse struct {
	Status     string         `json:"status"` // ok, degraded, failed
	Summary    string         `json:"summary"`
	Probes     []ClusterProbe `json:"probes"`
	CheckedAt  time.Time      `json:"checked_at"`
	ServerInfo string         `json:"server_version,omitempty"`
}

// ClusterNodePool aggregates nodes that share a pool and zone.
type ClusterNodePool struct {
	Name string `json:"name"`
	// Label is the node label KEY that supplied Name. The allocation editor
	// must echo it back as PoolAllocation.NodePoolLabel: pinning to an EKS
	// managed node group needs eks.amazonaws.com/nodegroup, and the generic
	// "agentpool" key would match no node at all.
	Label                  string   `json:"label,omitempty"`
	Zone                   string   `json:"zone,omitempty"`
	NodeCount              int      `json:"node_count"`
	SchedulableNodes       int      `json:"schedulable_nodes"`
	AllocatableCPUMillis   int64    `json:"allocatable_cpu_millis"`
	AllocatableMemoryBytes int64    `json:"allocatable_memory_bytes"`
	Taints                 []string `json:"taints,omitempty"`
}

// ClusterNodeCapacity describes node-level capacity, or why it is unknown.
type ClusterNodeCapacity struct {
	Available         bool                `json:"available"`
	UnavailableReason string              `json:"unavailable_reason,omitempty"`
	NodePools         []ClusterNodePool   `json:"node_pools,omitempty"`
	LabelValues       map[string][]string `json:"label_values,omitempty"`
}

// ClusterQuota is a namespace ResourceQuota's limits and usage.
type ClusterQuota struct {
	Name string            `json:"name"`
	Hard map[string]string `json:"hard,omitempty"`
	Used map[string]string `json:"used,omitempty"`
}

// ClusterWorkload describes the generator StatefulSet's live state.
type ClusterWorkload struct {
	Name            string            `json:"name"`
	Replicas        int               `json:"replicas"`
	ReadyReplicas   int               `json:"ready_replicas"`
	Container       string            `json:"container,omitempty"`
	ContainerNames  []string          `json:"container_names,omitempty"`
	UpdateStrategy  string            `json:"update_strategy,omitempty"`
	CurrentRequests map[string]string `json:"current_requests,omitempty"`
	CurrentLimits   map[string]string `json:"current_limits,omitempty"`
	NodeSelector    map[string]string `json:"node_selector,omitempty"`
	Warnings        []string          `json:"warnings,omitempty"`
}

// ClusterCapacityResponse is the capacity view behind the allocation editor.
type ClusterCapacityResponse struct {
	ClusterID    string              `json:"cluster_id"`
	Namespace    string              `json:"namespace"`
	NodeCapacity ClusterNodeCapacity `json:"node_capacity"`
	Quotas       []ClusterQuota      `json:"quotas"`
	Workload     *ClusterWorkload    `json:"generator_workload,omitempty"`
}

func clusterConnectionResponse(conn *storage.ClusterConnection) ClusterConnectionResponse {
	return ClusterConnectionResponse{
		ID:                   conn.ID,
		Name:                 conn.Name,
		Description:          conn.Description,
		Kind:                 conn.Kind,
		APIServerURL:         conn.APIServerURL,
		Namespace:            conn.Namespace,
		GeneratorSelector:    conn.GeneratorSelector,
		GeneratorStatefulSet: conn.GeneratorStatefulSet,
		GeneratorContainer:   conn.GeneratorContainer,
		CACertificatePEM:     conn.CACertificatePEM,
		TLSMode:              conn.TLSMode,
		AuthMode:             conn.AuthMode,
		AzureTenantID:        conn.AzureTenantID,
		AzureClientID:        conn.AzureClientID,
		AWSRegion:            conn.AWSRegion,
		AWSClusterName:       conn.AWSClusterName,
		AWSCredentialMode:    conn.AWSCredentialMode,
		AWSAccessKeyID:       conn.AWSAccessKeyID,
		AWSRoleARN:           conn.AWSRoleARN,
		AWSSTSEndpoint:       conn.AWSSTSEndpoint,
		HasAWSExternalID:     strings.TrimSpace(conn.AWSExternalIDCiphertext) != "",
		Labels:               conn.Labels,
		Status:               conn.Status,
		HasClientSecret:      conn.HasStoredCredential(),
		ClientSecretEnvVar:   conn.ClientSecretEnvVar,
		CredentialSource:     conn.CredentialSource(),
		AuthDescription:      describeClusterAuth(conn),
		LastCheckedAt:        conn.LastCheckedAt,
		LastCheckStatus:      conn.LastCheckStatus,
		LastCheckMessage:     conn.LastCheckMessage,
		CreatedAt:            conn.CreatedAt,
		UpdatedAt:            conn.UpdatedAt,
	}
}

// describeClusterAuth renders a non-secret summary of how the connection
// authenticates.
func describeClusterAuth(conn *storage.ClusterConnection) string {
	switch conn.AuthMode {
	case storage.ClusterAuthInCluster:
		return "controller service account (in-cluster)"
	case storage.ClusterAuthAzureServicePrincipal:
		return fmt.Sprintf("azure service principal (tenant %s, client %s)", conn.AzureTenantID, conn.AzureClientID)
	case storage.ClusterAuthStaticToken:
		return "static service-account token"
	case storage.ClusterAuthAWSIAM:
		// The role ARN is not secret and is the most actionable diagnostic on
		// this path. The external ID is a shared secret and never appears.
		detail := "unknown credentials"
		switch conn.AWSCredentialMode {
		case storage.AWSCredentialModeStaticKeys:
			detail = "static access key"
		case storage.AWSCredentialModeAssumeRole:
			detail = "assume role " + conn.AWSRoleARN
		case storage.AWSCredentialModeAmbient:
			detail = "workload identity (IRSA or EKS Pod Identity)"
		}
		return fmt.Sprintf("aws iam (eks cluster %s, region %s, %s)", conn.AWSClusterName, conn.AWSRegion, detail)
	default:
		return conn.AuthMode
	}
}

// requireClusterStore returns the cluster store or writes the error response.
func (s *Server) requireClusterStore(w http.ResponseWriter) (storage.ClusterConnectionStore, bool) {
	if s.clusterStore == nil {
		writeError(w, http.StatusServiceUnavailable, "this controller's storage backend does not support cluster connections")
		return nil, false
	}
	return s.clusterStore, true
}

func (s *Server) handleCreateClusterConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireClusterStore(w)
	if !ok {
		return
	}
	var req ClusterConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cluster connection payload")
		return
	}
	conn := &storage.ClusterConnection{ID: uuid.New().String()}
	applyClusterConnectionRequest(conn, req)
	if err := s.sealSubmittedClusterCredentials(r.Context(), conn, req); err != nil {
		writeClusterCredentialError(w, err)
		return
	}
	if err := conn.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.CreateClusterConnection(conn); err != nil {
		writeClusterConnectionWriteError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, clusterConnectionResponse(conn))
}

func (s *Server) handleListClusterConnections(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireClusterStore(w)
	if !ok {
		return
	}
	connections, err := store.ListClusterConnections()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list cluster connections")
		return
	}
	items := make([]ClusterConnectionResponse, 0, len(connections))
	for _, conn := range connections {
		items = append(items, clusterConnectionResponse(conn))
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": items, "total": len(items)})
}

func (s *Server) handleGetClusterConnection(w http.ResponseWriter, r *http.Request) {
	conn, ok := s.loadClusterConnection(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, clusterConnectionResponse(conn))
}

func (s *Server) handleUpdateClusterConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireClusterStore(w)
	if !ok {
		return
	}
	conn, ok := s.loadClusterConnection(w, r)
	if !ok {
		return
	}
	var req ClusterConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cluster connection payload")
		return
	}
	if req.UpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "updated_at is required to update a cluster connection")
		return
	}
	applyClusterConnectionRequest(conn, req)
	// An omitted credential keeps the stored envelope: applyClusterConnection
	// Request does not touch the ciphertext fields, so they survive untouched.
	if err := s.sealSubmittedClusterCredentials(r.Context(), conn, req); err != nil {
		writeClusterCredentialError(w, err)
		return
	}
	if err := conn.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := store.UpdateClusterConnectionIfUnchanged(conn, req.UpdatedAt)
	if err != nil {
		writeClusterConnectionWriteError(w, err)
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "cluster connection changed since it was loaded; reload and retry")
		return
	}
	// The cached client keys on UpdatedAt, so this edit already invalidates it;
	// forgetting explicitly also drops any cached Azure AD token immediately.
	s.clusterClients.Forget(conn.ID)
	writeJSON(w, http.StatusOK, clusterConnectionResponse(conn))
}

func (s *Server) handleDeleteClusterConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireClusterStore(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	deleted, err := store.DeleteClusterConnectionIfUnreferenced(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "cluster connection not found")
		return
	}
	if !deleted {
		// Refusing rather than cascading: an orphaned pool would silently fall
		// back to the controller's own cluster on its next scale.
		pools, listErr := store.ListFleetPoolsForCluster(id)
		names := make([]string, 0, len(pools))
		if listErr == nil {
			for _, pool := range pools {
				names = append(names, pool.Name)
			}
		}
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"cluster connection is still used by %d fleet pool(s): %s; repoint them first",
			len(names), strings.Join(names, ", ")))
		return
	}
	s.clusterClients.Forget(id)
	s.clusterProbes.forget(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestClusterConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireClusterStore(w)
	if !ok {
		return
	}
	conn, ok := s.loadClusterConnection(w, r)
	if !ok {
		return
	}
	if !s.clusterProbes.claim(conn.ID, time.Now()) {
		writeError(w, http.StatusTooManyRequests, "a connection test for this cluster ran moments ago; wait a few seconds and retry")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), clusterProbeTimeout)
	defer cancel()
	result := s.probeClusterConnection(ctx, conn)

	// Health is provisioner-owned state, so it is written outside the CAS
	// window: a probe must never restore stale administrator fields.
	if err := store.UpdateClusterConnectionHealth(conn.ID, result.Status, result.Summary, result.CheckedAt); err != nil {
		// A failed health write must not mask the probe result the admin asked for.
		writeJSON(w, http.StatusOK, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleTestClusterConnectionDraft probes a connection before it is saved, so
// a cluster is tested before it is registered or an edit is kept. Nothing is
// stored: not the connection, its credentials, or a health result.
func (s *Server) handleTestClusterConnectionDraft(w http.ResponseWriter, r *http.Request) {
	store, ok := s.requireClusterStore(w)
	if !ok {
		return
	}
	var req ClusterConnectionTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cluster connection payload")
		return
	}
	conn := &storage.ClusterConnection{ID: uuid.New().String()}
	// New drafts are throttled per administrator: one admin's test must not
	// refuse another's, as a shared key would.
	throttleKey := "draft"
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		throttleKey = "draft:" + firstNonEmpty(user.ID, user.Subject)
	}
	if id := strings.TrimSpace(req.ConnectionID); id != "" {
		stored, err := store.GetClusterConnection(id)
		if err != nil || stored == nil {
			writeError(w, http.StatusNotFound, "cluster connection not found")
			return
		}
		// A copy: the edit is probed over the stored connection without
		// changing it. Its ID keeps the stored credentials decryptable.
		draft := *stored
		conn = &draft
		throttleKey = stored.ID
	}
	applyClusterConnectionRequest(conn, req.Connection)
	// The probe reads credentials the way a stored connection does, sealed;
	// without a key wrapper the connection could not be saved either.
	if err := s.sealSubmittedClusterCredentials(r.Context(), conn, req.Connection); err != nil {
		if errors.Is(err, errClusterCredentialEncryptionUnavailable) {
			writeError(w, http.StatusServiceUnavailable,
				"cluster credential encryption is not configured on this controller, so an entered credential can be neither tested nor saved; configure a key wrapper, or read the credential from an environment variable")
			return
		}
		writeClusterCredentialError(w, err)
		return
	}
	if err := conn.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.clusterProbes.claim(throttleKey, time.Now()) {
		writeError(w, http.StatusTooManyRequests, "a connection test for this cluster ran moments ago; wait a few seconds and retry")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), clusterProbeTimeout)
	defer cancel()
	// Never the cached client: an edit shares the stored connection's ID and
	// timestamp, so the cache would answer with the stored configuration.
	writeJSON(w, http.StatusOK, s.probeClusterConnectionWith(ctx, conn, s.clusterClients.BuildUncached))
}

// probeClusterConnection runs the capability checks in increasing order of
// required privilege so the administrator sees exactly where it stops working.
func (s *Server) probeClusterConnection(ctx context.Context, conn *storage.ClusterConnection) ClusterTestResponse {
	return s.probeClusterConnectionWith(ctx, conn, s.clusterClients.ClientFor)
}

// probeClusterConnectionWith probes with the given client source: the cache
// for a stored connection, a fresh client for one not yet saved.
func (s *Server) probeClusterConnectionWith(ctx context.Context, conn *storage.ClusterConnection, clientFor func(context.Context, *storage.ClusterConnection) (*kubeops.Client, error)) ClusterTestResponse {
	response := ClusterTestResponse{CheckedAt: time.Now().UTC(), Probes: make([]ClusterProbe, 0, 5)}

	client, err := clientFor(ctx, conn)
	if err != nil {
		response.Probes = append(response.Probes, ClusterProbe{
			Probe: "credential", OK: false, Message: scrubClusterError(err),
		})
		response.Status = "failed"
		response.Summary = "the cluster credential could not be prepared"
		return response
	}

	// For EKS, resolve the caller identity BEFORE touching the API server. The
	// dominant EKS failure is a perfectly valid token whose ARN has no
	// Kubernetes RBAC, which otherwise surfaces as a bare 401 that tells an
	// administrator nothing.
	stsOK := s.appendAWSIdentityProbe(ctx, conn, &response)

	version, err := client.ServerVersion(ctx)
	apiProbe := clusterProbe("api_server", err, "reached the Kubernetes API server")
	if err != nil && stsOK && (apiProbe.StatusCode == http.StatusUnauthorized || apiProbe.StatusCode == http.StatusForbidden) {
		// STS vouched for the credential, so this is an authorization gap in
		// the cluster, not a bad key. Rewriting the existing probe's message
		// (rather than adding another probe) keeps the probe list stable, and
		// the dashboard renders probe messages verbatim.
		apiProbe.Message = "STS accepted this credential but the Kubernetes API server did not authorize it — the caller ARN has no EKS access entry or aws-auth ConfigMap mapping. " +
			"(A 401 here can also mean the controller's clock is more than 15 minutes out of sync with AWS.)"
	}
	response.Probes = append(response.Probes, apiProbe)
	if err != nil {
		response.Status = "failed"
		response.Summary = "the Kubernetes API server could not be reached or rejected the credential"
		return response
	}
	response.ServerInfo = version

	workload := firstNonEmpty(conn.GeneratorStatefulSet, "apexload-generator")
	_, err = client.GetStatefulSet(ctx, conn.Namespace, workload)
	response.Probes = append(response.Probes, clusterProbe("generator_workload", err,
		fmt.Sprintf("found the generator StatefulSet %q", workload)))

	_, quotaErr := client.ListResourceQuotas(ctx, conn.Namespace)
	response.Probes = append(response.Probes, clusterProbe("resource_quotas", quotaErr,
		"read namespace resource quotas"))

	// Node listing needs a cluster-scoped ClusterRole that is shipped opt-in,
	// so a 403 here is a capability gap to report, not a failure.
	_, nodeErr := client.ListNodes(ctx, "")
	nodeProbe := clusterProbe("nodes", nodeErr, "read cluster nodes")
	nodeProbe.Optional = true
	if nodeErr != nil && kubeops.IsForbidden(nodeErr) {
		nodeProbe.Message = "node read permission is not granted; node pool discovery and pod-fit checks are unavailable (apply the optional ClusterRole to enable them)"
	}
	response.Probes = append(response.Probes, nodeProbe)

	response.Status, response.Summary = summarizeClusterProbes(response.Probes)
	return response
}

// appendAWSIdentityProbe resolves and reports the AWS principal the controller
// authenticates as. It reports whether the probe ran AND succeeded, so the
// api_server probe can distinguish "bad credential" from "unmapped ARN".
//
// It is a no-op for every non-EKS connection, so the probe list is unchanged
// for existing AKS and generic clusters.
func (s *Server) appendAWSIdentityProbe(ctx context.Context, conn *storage.ClusterConnection, response *ClusterTestResponse) bool {
	if conn.AuthMode != storage.ClusterAuthAWSIAM {
		return false
	}
	source, err := s.clusterClients.awsTokenSourceFor(ctx, conn)
	if err != nil {
		response.Probes = append(response.Probes, ClusterProbe{
			Probe: "sts_identity", OK: false, Message: scrubClusterError(err),
		})
		return false
	}
	awsSource, ok := source.(*kubeops.AwsIAMTokenSource)
	if !ok {
		return false
	}
	identity, err := awsSource.CallerIdentity(ctx)
	if err != nil {
		response.Probes = append(response.Probes, ClusterProbe{
			Probe: "sts_identity", OK: false, Message: describeAWSIdentityError(err),
		})
		return false
	}
	// The ARN is echoed verbatim because it is precisely the string an
	// administrator must paste into an EKS access entry. This route is
	// system-admin only and the response already carries the API server URL and
	// Azure directory identifiers, so it is in band.
	response.Probes = append(response.Probes, ClusterProbe{
		Probe: "sts_identity", OK: true,
		Message: fmt.Sprintf("AWS credential resolves to %s (account %s). Map this principal in the cluster's EKS access entries or aws-auth ConfigMap.",
			identity.ARN, identity.Account),
	})
	return true
}

// describeAWSIdentityError turns an STS failure into an actionable sentence.
// It matches on the error's text rather than surfacing a raw response body.
func describeAWSIdentityError(err error) string {
	message := err.Error()
	for _, mapping := range []struct{ code, advice string }{
		{"InvalidClientTokenId", "the AWS access key ID is not valid"},
		{"SignatureDoesNotMatch", "the AWS secret access key does not match the access key ID"},
		{"ExpiredToken", "the AWS credential has expired"},
		{"RequestTimeTooSkewed", "the controller's clock is out of sync with AWS; STS rejected the signature's timestamp"},
		{"AccessDenied", "the controller's AWS identity is not allowed to assume the configured role; check the role's trust policy and external ID"},
	} {
		if strings.Contains(message, mapping.code) {
			return mapping.advice
		}
	}
	return scrubClusterError(err)
}

func clusterProbe(name string, err error, okMessage string) ClusterProbe {
	if err == nil {
		return ClusterProbe{Probe: name, OK: true, Message: okMessage}
	}
	probe := ClusterProbe{Probe: name, OK: false, Message: scrubClusterError(err)}
	var apiErr *kubeops.KubernetesAPIError
	if errors.As(err, &apiErr) {
		probe.StatusCode = apiErr.StatusCode
	}
	return probe
}

func summarizeClusterProbes(probes []ClusterProbe) (status string, summary string) {
	failedRequired := make([]string, 0, len(probes))
	failedOptional := make([]string, 0, len(probes))
	for _, probe := range probes {
		if probe.OK {
			continue
		}
		if probe.Optional {
			failedOptional = append(failedOptional, probe.Probe)
		} else {
			failedRequired = append(failedRequired, probe.Probe)
		}
	}
	switch {
	case len(failedRequired) > 0:
		return "failed", "required checks failed: " + strings.Join(failedRequired, ", ")
	case len(failedOptional) > 0:
		return "degraded", "connected, with optional capabilities unavailable: " + strings.Join(failedOptional, ", ")
	default:
		return "ok", "all checks passed"
	}
}

// scrubClusterError keeps API server response bodies out of the administrator
// UI for auth failures, where the body can echo token material.
func scrubClusterError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *kubeops.KubernetesAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			return "the Kubernetes API server rejected the credential (401 Unauthorized)"
		case http.StatusForbidden:
			return "the credential is authenticated but not authorized for this operation (403 Forbidden)"
		case http.StatusNotFound:
			return "the requested Kubernetes object was not found (404)"
		default:
			return fmt.Sprintf("the Kubernetes API server returned %d", apiErr.StatusCode)
		}
	}
	return err.Error()
}

func (s *Server) handleGetClusterCapacity(w http.ResponseWriter, r *http.Request) {
	conn, ok := s.loadClusterConnection(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), clusterProbeTimeout)
	defer cancel()

	client, err := s.clusterClients.ClientFor(ctx, conn)
	if err != nil {
		writeError(w, http.StatusBadGateway, scrubClusterError(err))
		return
	}
	response := ClusterCapacityResponse{
		ClusterID: conn.ID,
		Namespace: conn.Namespace,
		Quotas:    make([]ClusterQuota, 0),
	}

	quotas, quotaErr := client.ListResourceQuotas(ctx, conn.Namespace)
	if quotaErr == nil {
		for _, quota := range quotas {
			response.Quotas = append(response.Quotas, ClusterQuota{
				Name: quota.Name, Hard: quota.Hard, Used: quota.Used,
			})
		}
	}

	nodes, nodeErr := client.ListNodes(ctx, "")
	response.NodeCapacity = summarizeNodeCapacity(nodes, nodeErr)

	workload := firstNonEmpty(conn.GeneratorStatefulSet, "apexload-generator")
	if set, setErr := client.GetStatefulSet(ctx, conn.Namespace, workload); setErr == nil {
		response.Workload = summarizeClusterWorkload(set, conn.GeneratorContainer)
	}
	writeJSON(w, http.StatusOK, response)
}

// summarizeNodeCapacity groups nodes into pools. A forbidden node read is
// reported as unavailable rather than failing the whole capacity view, because
// the node ClusterRole is optional.
func summarizeNodeCapacity(nodes []kubeops.NodeRef, err error) ClusterNodeCapacity {
	if err != nil {
		reason := "unavailable"
		if kubeops.IsForbidden(err) {
			reason = "rbac_forbidden"
		}
		return ClusterNodeCapacity{Available: false, UnavailableReason: reason}
	}
	capacity := ClusterNodeCapacity{
		Available:   true,
		NodePools:   make([]ClusterNodePool, 0),
		LabelValues: map[string][]string{},
	}
	// The label KEY is part of the grouping identity, not just the value: a
	// cluster can run managed node groups and Karpenter side by side, and the
	// allocation editor has to echo back the key that actually matches.
	type poolKey struct{ label, name, zone string }
	grouped := map[poolKey]*ClusterNodePool{}
	labelValues := map[string]map[string]struct{}{}

	for _, node := range nodes {
		key := poolKey{label: node.AgentPoolLabel(), name: node.AgentPool(), zone: node.Zone()}
		entry, ok := grouped[key]
		if !ok {
			entry = &ClusterNodePool{Name: key.name, Label: key.label, Zone: key.zone}
			grouped[key] = entry
		}
		entry.NodeCount++
		if !node.Unschedulable {
			entry.SchedulableNodes++
		}
		cpu, _, _ := node.Allocatable.CPUMillis()
		memory, _, _ := node.Allocatable.MemoryBytes()
		entry.AllocatableCPUMillis += cpu
		entry.AllocatableMemoryBytes += memory
		for _, taint := range node.Taints {
			marker := taint.Key + "=" + taint.Value + ":" + taint.Effect
			if !containsTaintMarker(entry.Taints, marker) {
				entry.Taints = append(entry.Taints, marker)
			}
		}
		for label, value := range node.Labels {
			if labelValues[label] == nil {
				labelValues[label] = map[string]struct{}{}
			}
			labelValues[label][value] = struct{}{}
		}
	}
	for _, entry := range grouped {
		capacity.NodePools = append(capacity.NodePools, *entry)
	}
	for label, values := range labelValues {
		capacity.LabelValues[label] = sortedStringSet(values)
	}
	return capacity
}

func summarizeClusterWorkload(set kubeops.StatefulSetRef, configuredContainer string) *ClusterWorkload {
	workload := &ClusterWorkload{
		Name:           set.Name,
		Replicas:       set.Replicas,
		ReadyReplicas:  set.ReadyReplicas,
		UpdateStrategy: set.UpdateStrategy,
		NodeSelector:   set.NodeSelector,
		Warnings:       allocationTargetWarnings(set),
	}
	for _, container := range set.Containers {
		workload.ContainerNames = append(workload.ContainerNames, container.Name)
	}
	if name, err := resolveGeneratorContainer(set, configuredContainer); err == nil {
		workload.Container = name
		for _, container := range set.Containers {
			if container.Name == name {
				workload.CurrentRequests = container.Requests
				workload.CurrentLimits = container.Limits
			}
		}
	} else {
		workload.Warnings = append(workload.Warnings, err.Error())
	}
	return workload
}

func containsTaintMarker(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// loadClusterConnection resolves the path id, writing the error response on
// failure.
func (s *Server) loadClusterConnection(w http.ResponseWriter, r *http.Request) (*storage.ClusterConnection, bool) {
	store, ok := s.requireClusterStore(w)
	if !ok {
		return nil, false
	}
	conn, err := store.GetClusterConnection(r.PathValue("id"))
	if err != nil || conn == nil {
		writeError(w, http.StatusNotFound, "cluster connection not found")
		return nil, false
	}
	return conn, true
}

// applyClusterConnectionRequest copies administrator-editable fields. It
// deliberately never touches the credential envelope columns, which is what
// makes "omit the secret to keep it" work.
func applyClusterConnectionRequest(conn *storage.ClusterConnection, req ClusterConnectionRequest) {
	conn.Name = strings.TrimSpace(req.Name)
	conn.Description = strings.TrimSpace(req.Description)
	conn.Kind = firstNonEmpty(strings.TrimSpace(req.Kind), storage.ClusterKindGeneric)
	conn.APIServerURL = strings.TrimRight(strings.TrimSpace(req.APIServerURL), "/")
	conn.Namespace = strings.TrimSpace(req.Namespace)
	conn.GeneratorSelector = strings.TrimSpace(req.GeneratorSelector)
	conn.GeneratorStatefulSet = strings.TrimSpace(req.GeneratorStatefulSet)
	conn.GeneratorContainer = strings.TrimSpace(req.GeneratorContainer)
	conn.CACertificatePEM = strings.TrimSpace(req.CACertificatePEM)
	conn.TLSMode = firstNonEmpty(strings.TrimSpace(req.TLSMode), storage.ClusterTLSCustomCA)
	conn.AuthMode = firstNonEmpty(strings.TrimSpace(req.AuthMode), defaultAuthModeForKind(conn.Kind))
	conn.AzureTenantID = strings.TrimSpace(req.AzureTenantID)
	conn.AzureClientID = strings.TrimSpace(req.AzureClientID)
	conn.AzureScope = strings.TrimSpace(req.AzureScope)
	conn.AzureAuthority = strings.TrimSpace(req.AzureAuthority)
	conn.AWSRegion = strings.TrimSpace(req.AWSRegion)
	conn.AWSClusterName = strings.TrimSpace(req.AWSClusterName)
	conn.AWSCredentialMode = strings.TrimSpace(req.AWSCredentialMode)
	conn.AWSAccessKeyID = strings.TrimSpace(req.AWSAccessKeyID)
	conn.AWSRoleARN = strings.TrimSpace(req.AWSRoleARN)
	conn.AWSSTSEndpoint = strings.TrimRight(strings.TrimSpace(req.AWSSTSEndpoint), "/")
	// Credential references are presence-aware; see ClusterConnectionRequest.
	if req.ClientSecretEnvVar != nil {
		conn.ClientSecretEnvVar = strings.TrimSpace(*req.ClientSecretEnvVar)
	}
	conn.Labels = req.Labels
	conn.Status = firstNonEmpty(strings.TrimSpace(req.Status), "active")
}

func defaultAuthModeForKind(kind string) string {
	switch kind {
	case storage.ClusterKindInCluster:
		return storage.ClusterAuthInCluster
	case storage.ClusterKindEKS:
		return storage.ClusterAuthAWSIAM
	default:
		return storage.ClusterAuthStaticToken
	}
}

// sealSubmittedClusterCredentials encrypts any credential present in the
// request. A nil pointer means the field was omitted, which preserves the
// stored envelope; an explicit empty string clears it.
func (s *Server) sealSubmittedClusterCredentials(ctx context.Context, conn *storage.ClusterConnection, req ClusterConnectionRequest) error {
	if req.ClientSecret != nil {
		if strings.TrimSpace(*req.ClientSecret) == "" {
			conn.ClientSecretCiphertext, conn.ClientSecretWrappedKey = "", ""
		} else {
			ciphertext, wrapped, err := sealClusterCredential(ctx, s.clusterSecretWrapper, conn.ID,
				clusterCredentialAADClientSecret, *req.ClientSecret)
			if err != nil {
				return err
			}
			conn.ClientSecretCiphertext, conn.ClientSecretWrappedKey = ciphertext, wrapped
		}
	}
	if req.StaticToken != nil {
		if strings.TrimSpace(*req.StaticToken) == "" {
			conn.StaticTokenCiphertext, conn.StaticTokenWrappedKey = "", ""
		} else {
			ciphertext, wrapped, err := sealClusterCredential(ctx, s.clusterSecretWrapper, conn.ID,
				clusterCredentialStaticToken, *req.StaticToken)
			if err != nil {
				return err
			}
			conn.StaticTokenCiphertext, conn.StaticTokenWrappedKey = ciphertext, wrapped
		}
	}
	if req.AWSSecretAccessKey != nil {
		if strings.TrimSpace(*req.AWSSecretAccessKey) == "" {
			conn.AWSSecretKeyCiphertext, conn.AWSSecretKeyWrappedKey = "", ""
		} else {
			ciphertext, wrapped, err := sealClusterCredential(ctx, s.clusterSecretWrapper, conn.ID,
				clusterCredentialAWSSecretKey, *req.AWSSecretAccessKey)
			if err != nil {
				return err
			}
			conn.AWSSecretKeyCiphertext, conn.AWSSecretKeyWrappedKey = ciphertext, wrapped
		}
	}
	if req.AWSExternalID != nil {
		if strings.TrimSpace(*req.AWSExternalID) == "" {
			conn.AWSExternalIDCiphertext, conn.AWSExternalIDWrappedKey = "", ""
		} else {
			ciphertext, wrapped, err := sealClusterCredential(ctx, s.clusterSecretWrapper, conn.ID,
				clusterCredentialAWSExternalID, *req.AWSExternalID)
			if err != nil {
				return err
			}
			conn.AWSExternalIDCiphertext, conn.AWSExternalIDWrappedKey = ciphertext, wrapped
		}
	}
	return nil
}

func writeClusterCredentialError(w http.ResponseWriter, err error) {
	if errors.Is(err, errClusterCredentialEncryptionUnavailable) {
		// Fail closed: storing an administrator's cluster credential in
		// plaintext is never an acceptable fallback.
		writeError(w, http.StatusServiceUnavailable,
			"cluster credential encryption is not configured on this controller; configure a key wrapper before storing cluster credentials")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func writeClusterConnectionWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrClusterConnectionKindImmutable) {
		writeError(w, http.StatusConflict, "cluster connection kind is immutable; create a new connection and repoint its execution targets")
		return
	}
	message := err.Error()
	if strings.Contains(strings.ToLower(message), "unique") {
		writeError(w, http.StatusConflict, "a cluster connection with that name already exists")
		return
	}
	writeError(w, http.StatusBadRequest, message)
}
