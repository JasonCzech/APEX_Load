package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	projectSecretMaxValueBytes       = 64 << 10
	projectSecretMaxBatchBytes       = 1 << 20
	projectSecretMaxBatchItems       = 256
	sourceSecretSessionTTL           = 30 * time.Minute
	sourceSecretSessionLimit         = 128
	sourceSecretSessionMaxTotalBytes = 1 << 20
	projectSecretMutationBudget      = 4 * time.Minute
)

type projectSecretInput struct {
	Name          string                      `json:"name"`
	Value         string                      `json:"value"`
	ExpectedUsage *projectSecretExpectedUsage `json:"expected_usage,omitempty"`
}

type projectSecretBatchRequest struct {
	Secrets []projectSecretInput `json:"secrets"`
}

type projectSecretStatusRequest struct {
	Names []string `json:"names"`
}

type projectSecretStatus struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Provider    string `json:"provider,omitempty"`
	Writable    bool   `json:"writable,omitempty"`
	SourceScope string `json:"source_scope,omitempty"`
}

type rotateProjectSecretRequest struct {
	Value                  string `json:"value"`
	ExpectedUsageRevision  string `json:"expected_usage_revision"`
	AcknowledgeSharedUsage bool   `json:"acknowledge_shared_usage,omitempty"`
}

type cloneScriptDraftRequest struct {
	ImportDetectedCredentialValues bool `json:"import_detected_credential_values,omitempty"`
}

type sourceSecretSession struct {
	ID         string
	ProjectID  string
	Actor      string
	Values     map[string]string
	ExpiresAt  time.Time
	FeatureIDs []string
	Timer      *time.Timer
}

type sourceSecretMigrationSelection struct {
	RequirementID string `json:"requirement_id"`
	SecretName    string `json:"secret_name"`
}

type sourceSecretMigrationRequest struct {
	ProjectID             string                           `json:"project_id"`
	SourceSecretSessionID string                           `json:"source_secret_session_id"`
	Secrets               []sourceSecretMigrationSelection `json:"secrets"`
}

type sourceSecretSessionDiscardRequest struct {
	ProjectID             string `json:"project_id"`
	SourceSecretSessionID string `json:"source_secret_session_id"`
}

func (s *Server) projectSecretCapabilities(_ string) map[string]any {
	provider := "unavailable"
	writable := false
	createSupported := false
	if s != nil && s.projectSecretStore != nil {
		if configured := strings.TrimSpace(s.projectSecretStore.Provider()); configured != "" {
			provider = configured
		}
		writable = s.projectSecretStore.Writable()
		createSupported = secretstore.SupportsCreate(s.projectSecretStore)
	}
	allowMigration := false
	if s != nil && s.runner != nil {
		allowMigration = s.runner.GetSystemSettings().ScriptImports.AllowSourceSecretMigration
	}
	return map[string]any{
		"provider":                               provider,
		"writable":                               writable,
		"create_supported":                       createSupported,
		"replace_supported":                      writable,
		"source_secret_migration_policy_enabled": allowMigration,
	}
}

func (s *Server) handleListProjectSecrets(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.PathValue("id"))
	if !s.ensureSecretProjectExists(w, projectID) {
		return
	}
	discoveryComplete := true
	var discoveryErr error
	discoveryComplete, discoveryErr = s.discoverProjectSecretMetadata(r.Context(), projectID)
	if discoveryErr != nil {
		discoveryComplete = false
		// Provider errors are deliberately not logged: an implementation may echo
		// response material. The project and value-free outcome are sufficient.
		slog.Warn("project secret metadata discovery incomplete; returning indexed metadata", "project_id", projectID, "reason", "provider_unavailable")
	}
	metadata := []secretstore.Metadata{}
	if s.projectSecretStore != nil {
		listed, err := s.projectSecretStore.List(r.Context(), projectID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "project secret metadata is unavailable")
			return
		}
		metadata = publicProjectSecretMetadata(listed)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":                  projectID,
		"capabilities":                s.projectSecretCapabilities(projectID),
		"secrets":                     metadata,
		"count":                       len(metadata),
		"metadata_discovery_complete": discoveryComplete,
	})
}

// publicProjectSecretMetadata removes provider-only rollback ownership tokens
// from API responses. The encrypted store must retain these subjects to make
// create compensation atomic, but they are not human creator identities and
// must not appear in Settings or other metadata consumers.
func publicProjectSecretMetadata(metadata []secretstore.Metadata) []secretstore.Metadata {
	result := append([]secretstore.Metadata(nil), metadata...)
	for index := range result {
		if internalRuntimeSecretRollbackOwner(result[index].CreatedBy) {
			result[index].CreatedBy = ""
		}
	}
	return result
}

func internalRuntimeSecretRollbackOwner(subject string) bool {
	subject = strings.TrimSpace(subject)
	for _, resourceType := range []string{"test", "scenario"} {
		prefix := "runtime-" + resourceType + ":"
		if !strings.HasPrefix(subject, prefix) {
			continue
		}
		resourceAndAttempt := strings.TrimPrefix(subject, prefix)
		resourceID, attemptID, found := strings.Cut(resourceAndAttempt, ":attempt:")
		return found && strings.TrimSpace(resourceID) != "" && strings.TrimSpace(attemptID) != ""
	}
	return false
}

func (s *Server) discoverProjectSecretMetadata(ctx context.Context, projectID string) (bool, error) {
	if s == nil || s.store == nil || s.projectSecretStore == nil ||
		!hasConfiguredExternalSecretProvider(secretStoreProvider(s.projectSecretStore)) {
		return true, nil
	}
	scripts, err := listPublishedImportScripts(s.store, projectID, nil)
	if err != nil {
		return false, err
	}
	byName := make(map[string]struct{})
	for _, script := range scripts {
		for _, name := range scriptSecretReferences(script) {
			byName[name] = struct{}{}
		}
	}
	if connections, ok := s.store.(storage.AppDynamicsConnectionStore); ok {
		profiles, err := connections.ListAppDynamicsConnections(projectID)
		if err != nil {
			return false, err
		}
		for _, profile := range profiles {
			for _, reference := range []string{profile.ClientIDSecretName, profile.ClientSecretSecretName, profile.UsernameSecretName, profile.PasswordSecretName, profile.EventsAPIKeySecretName} {
				if name, err := normalizeProjectSecretName(reference); err == nil {
					byName[name] = struct{}{}
				}
			}
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	probeCtx, cancel := engine.WithSecretResolutionBudget(ctx)
	defer cancel()
	indexed, err := s.projectSecretStore.List(probeCtx, projectID)
	if err != nil {
		return false, err
	}
	indexedNames := make(map[string]struct{}, len(indexed))
	for _, metadata := range indexed {
		name, normalizeErr := engine.NormalizeLogicalSecretName(metadata.Name)
		if normalizeErr == nil {
			indexedNames[name] = struct{}{}
		}
	}
	pending := names[:0]
	for _, name := range names {
		if _, alreadyIndexed := indexedNames[name]; !alreadyIndexed {
			pending = append(pending, name)
		}
	}
	batch, complete := projectSecretDiscoveryBatch(projectID, pending, time.Now().UTC())
	if err := secretstore.DiscoverManagedMetadata(probeCtx, s.projectSecretStore, projectID, batch); err != nil {
		return false, err
	}
	return complete, nil
}

func hasConfiguredExternalSecretProvider(provider string) bool {
	return strings.Contains(provider, "azure_key_vault") || strings.Contains(provider, "aws_secrets_manager")
}

const projectSecretDiscoveryRotationInterval = 5 * time.Second

// projectSecretDiscoveryBatch uses a stateless time-window rotation. Every HA
// replica selects the same bounded window for a project, while later windows
// advance even when all earlier provider probes returned not-found and produced
// no durable metadata. Indexed names are removed by the caller, so successful
// claims accelerate completion without making progression depend on memory.
func projectSecretDiscoveryBatch(projectID string, pending []string, now time.Time) ([]string, bool) {
	if len(pending) <= projectSecretMaxBatchItems {
		return pending, true
	}
	seed := uint64(14695981039346656037)
	for _, character := range []byte(normalizeProjectID(projectID)) {
		seed ^= uint64(character)
		seed *= 1099511628211
	}
	bucket := now.Unix() / int64(projectSecretDiscoveryRotationInterval/time.Second)
	if bucket < 0 {
		bucket = -bucket
	}
	start := int((seed + uint64(bucket)*projectSecretMaxBatchItems) % uint64(len(pending)))
	batch := make([]string, 0, projectSecretMaxBatchItems)
	for offset := 0; offset < projectSecretMaxBatchItems; offset++ {
		batch = append(batch, pending[(start+offset)%len(pending)])
	}
	return batch, false
}

func (s *Server) handleProjectSecretStatus(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.PathValue("id"))
	if !s.ensureSecretProjectExists(w, projectID) {
		return
	}
	var req projectSecretStatusRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	names, err := normalizeProjectSecretNames(req.Names)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	statuses, err := s.projectSecretStatuses(r, projectID, names)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "project secret status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id":   projectID,
		"capabilities": s.projectSecretCapabilities(projectID),
		"secrets":      statuses,
	})
}

func (s *Server) handleCreateProjectSecrets(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.PathValue("id"))
	if !s.ensureSecretProjectExists(w, projectID) || !s.requireHumanSecretWrite(w, r) {
		return
	}
	var req projectSecretBatchRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	inputs, err := validateProjectSecretInputs(req.Secrets)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.projectSecretStore == nil || !secretstore.SupportsCreate(s.projectSecretStore) {
		writeError(w, http.StatusServiceUnavailable, "no atomic create-only project secret provider is configured")
		return
	}
	releaseReferenceMutation, fenceErr := s.acquireSecretReferenceMutation(projectID)
	if fenceErr != nil {
		writeError(w, http.StatusServiceUnavailable, "project secret usage fence is unavailable")
		return
	}
	defer releaseReferenceMutation()
	mutationCtx, cancelMutation := context.WithTimeout(r.Context(), projectSecretMutationBudget)
	defer cancelMutation()
	for _, input := range inputs {
		if input.ExpectedUsage == nil {
			continue
		}
		usage, usageErr := s.projectSecretUsage(projectID, input.Name)
		if usageErr != nil {
			writeError(w, http.StatusServiceUnavailable, "project secret usage is unavailable")
			return
		}
		if err := validateExpectedProjectSecretUsage(usage, input.ExpectedUsage); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	actor := secretActorSubject(s, r)
	probeCtx, cancelProbe := engine.WithSecretResolutionBudget(mutationCtx)
	defer cancelProbe()
	for _, input := range inputs {
		if _, _, err := s.projectSecretStore.Get(probeCtx, projectID, input.Name); err == nil {
			if auditErr := s.persistProjectSecretOutcome(r, "project_secret.create", "blocked", "already_exists", input.Name, projectID, secretStoreProvider(s.projectSecretStore), "already_exists", nil); auditErr != nil {
				writeProjectSecretAuditUnavailable(w)
				return
			}
			writeError(w, http.StatusConflict, fmt.Sprintf("project secret %q already exists", input.Name))
			return
		} else if !errors.Is(err, secretstore.ErrNotFound) {
			if auditErr := s.persistProjectSecretOutcome(r, "project_secret.create", "failed", "provider_probe_failed", input.Name, projectID, secretStoreProvider(s.projectSecretStore), "provider_error", nil); auditErr != nil {
				writeProjectSecretAuditUnavailable(w)
				return
			}
			writeError(w, http.StatusServiceUnavailable, "project secret provider is unavailable")
			return
		}
	}
	created := make([]secretstore.Metadata, 0, len(inputs))
	for index, input := range inputs {
		if err := s.invalidateAppDynamicsSecretPreparations(mutationCtx, projectID, input.Name); err != nil {
			writeError(w, 503, "monitoring credential authorization invalidation failed")
			return
		}
		operationID, auditErr := s.beginProjectSecretOperation(r, "project_secret.create", input.Name, projectID, secretStoreProvider(s.projectSecretStore))
		if auditErr != nil {
			writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "project secret creation audit is unavailable", "created", created, input.Name, "audit_persistence_unavailable", projectSecretInputNames(inputs[index:]), false)
			return
		}
		metadata, outcome, err := createProjectSecretWithReconciliation(mutationCtx, s.projectSecretStore, projectID, input.Name, input.Value, actor)
		if err != nil {
			status := http.StatusServiceUnavailable
			message := "project secret could not be created"
			if errors.Is(err, secretstore.ErrAlreadyExists) || errors.Is(err, secretstore.ErrReservedBinding) {
				status = http.StatusConflict
				message = fmt.Sprintf("project secret %q already exists or is reserved by the read-only Key Vault allowlist", input.Name)
			}
			details := map[string]any{}
			if outcome == "provider_error_commit_ambiguous" {
				details["commit_state"] = "ambiguous"
			}
			if auditErr := s.completeProjectSecretOperation(r, operationID, "project_secret.create", "failed", outcome, input.Name, projectID, secretStoreProvider(s.projectSecretStore), projectSecretErrorReason(err), details); auditErr != nil {
				writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "project secret creation audit is unavailable", "created", created, input.Name, "audit_persistence_unavailable", projectSecretInputNames(inputs[index:]), false)
				return
			}
			if len(created) > 0 {
				writeProjectSecretPartialFailure(w, status, projectID, message, "created", created, input.Name, projectSecretErrorReason(err), projectSecretInputNames(inputs[index:]), false)
				return
			}
			writeError(w, status, message)
			return
		}
		if err := s.invalidateAppDynamicsSecretPreparations(mutationCtx, projectID, input.Name); err != nil {
			writeError(w, 503, "credential created; monitoring authorization invalidation must be retried")
			return
		}
		created = append(created, metadata)
		if auditErr := s.completeProjectSecretOperation(r, operationID, "project_secret.create", "success", outcome, input.Name, projectID, firstNonEmpty(strings.TrimSpace(metadata.Provider), secretStoreProvider(s.projectSecretStore)), "", nil); auditErr != nil {
			writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "project secret was created but its audit outcome could not be persisted; refresh secret status before retrying", "created", created, "", "audit_persistence_unavailable", projectSecretInputNames(inputs[index+1:]), false)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"project_id": projectID,
		"secrets":    created,
		"count":      len(created),
	})
}

func (s *Server) handleRotateProjectSecret(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.PathValue("id"))
	if !s.ensureSecretProjectExists(w, projectID) || !s.requireHumanSecretWrite(w, r) {
		return
	}
	name, err := normalizeProjectSecretName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req rotateProjectSecretRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if err := validateProjectSecretValue(req.Value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	releaseReferenceMutation, fenceErr := s.acquireSecretReferenceMutation(projectID)
	if fenceErr != nil {
		writeError(w, http.StatusServiceUnavailable, "project secret usage fence is unavailable")
		return
	}
	defer releaseReferenceMutation()
	mutationCtx, cancelMutation := context.WithTimeout(r.Context(), projectSecretMutationBudget)
	defer cancelMutation()
	usage, usageErr := s.projectSecretUsage(projectID, name)
	if usageErr != nil {
		writeError(w, http.StatusServiceUnavailable, "project secret usage is unavailable")
		return
	}
	if strings.TrimSpace(req.ExpectedUsageRevision) != "" && req.ExpectedUsageRevision != usage.UsageRevision {
		writeError(w, http.StatusConflict, "secret consumers changed; refresh usage before rotating the value")
		return
	}
	if usage.ConsumerCount > 1 {
		if strings.TrimSpace(req.ExpectedUsageRevision) == "" {
			writeError(w, http.StatusConflict, "shared secret rotation requires a current usage revision")
			return
		}
		if !req.AcknowledgeSharedUsage {
			writeError(w, http.StatusConflict, "shared secret rotation requires acknowledgement of all consuming scripts")
			return
		}
	}
	if s.projectSecretStore == nil || !s.projectSecretStore.Writable() {
		writeError(w, http.StatusServiceUnavailable, "no writable project secret provider is configured")
		return
	}
	if err := s.invalidateAppDynamicsSecretPreparations(mutationCtx, projectID, name); err != nil {
		writeError(w, 503, "monitoring credential authorization invalidation failed")
		return
	}
	operationID, auditErr := s.beginProjectSecretOperation(r, "project_secret.rotate", name, projectID, secretStoreProvider(s.projectSecretStore))
	if auditErr != nil {
		writeProjectSecretAuditUnavailable(w)
		return
	}
	metadata, outcome, err := replaceProjectSecretWithReconciliation(mutationCtx, s.projectSecretStore, projectID, name, req.Value, secretActorSubject(s, r))
	if err != nil {
		status := http.StatusServiceUnavailable
		message := "project secret could not be rotated"
		if errors.Is(err, secretstore.ErrNotFound) {
			status = http.StatusNotFound
			message = "project secret not found"
		} else if errors.Is(err, secretstore.ErrReadOnly) {
			status = http.StatusConflict
			message = "project secret is owned by a read-only provider"
		}
		details := map[string]any{}
		if outcome == "provider_error_commit_ambiguous" {
			details["commit_state"] = "ambiguous"
		}
		if auditErr := s.completeProjectSecretOperation(r, operationID, "project_secret.rotate", "failed", outcome, name, projectID, secretStoreProvider(s.projectSecretStore), projectSecretErrorReason(err), details); auditErr != nil {
			writeProjectSecretAuditUnavailable(w)
			return
		}
		writeError(w, status, message)
		return
	}
	if err := s.invalidateAppDynamicsSecretPreparations(mutationCtx, projectID, name); err != nil {
		writeError(w, 503, "credential rotated; monitoring authorization invalidation must be retried")
		return
	}
	if auditErr := s.completeProjectSecretOperation(r, operationID, "project_secret.rotate", "success", outcome, name, projectID, firstNonEmpty(strings.TrimSpace(metadata.Provider), secretStoreProvider(s.projectSecretStore)), "", nil); auditErr != nil {
		writeError(w, http.StatusServiceUnavailable, "project secret was rotated but its audit outcome could not be persisted; refresh secret status before retrying")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project_id": projectID, "secret": metadata, "usage": usage})
}

func (s *Server) handleScriptSecretStatus(w http.ResponseWriter, r *http.Request) {
	stored, ok := s.runner.GetScript(r.PathValue("id"))
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	required := scriptSecretReferences(stored.Script)
	statuses, applicationID, err := s.scriptSecretStatuses(r, stored.Script, required)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "script secret status is unavailable")
		return
	}
	configured := make([]string, 0, len(statuses))
	missing := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Configured {
			configured = append(configured, status.Name)
		} else {
			missing = append(missing, status.Name)
		}
	}
	response := map[string]any{
		"script_id":    stored.ID,
		"project_id":   stored.ProjectID,
		"required":     required,
		"configured":   configured,
		"missing":      missing,
		"secrets":      statuses,
		"capabilities": s.projectSecretCapabilities(stored.ProjectID),
	}
	if applicationID != "" {
		response["application_id"] = applicationID
		response["capabilities"] = s.applicationSecretCapabilities()
	}
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("details")), "bindings") {
		response["bindings"] = scriptSecretBindingDetails(stored.Script, statuses)
	}
	// Report the REAL store state even when placeholder substitution is enabled.
	// Claiming every secret is configured hid the fact that runs were sending a
	// fake credential; surface the substitution as its own flag instead.
	if secretBindingEnforcementDisabled() {
		response["placeholder_substitution"] = true
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleCloneScriptDraft(w http.ResponseWriter, r *http.Request) {
	stored, ok := s.runner.GetScript(r.PathValue("id"))
	if !ok || stored == nil || stored.Script == nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	var req cloneScriptDraftRequest
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	rawDraft := cloneScriptDefinition(stored.Script)
	rawDraft.ID = ""
	rawDraft.RefKey = ""
	rawDraft.CreatedAt = time.Time{}
	rawDraft.UpdatedAt = time.Time{}
	rawDraft.Name = "Copy of " + strings.TrimSpace(rawDraft.Name)
	masked := maskScriptSecrets(rawDraft)
	if masked == nil {
		writeError(w, http.StatusInternalServerError, "failed to create safe script draft")
		return
	}
	requirements := discoverSecretBindingRequirements(masked, 0)
	sessionID := ""
	sourceAvailable := []string{}
	if req.ImportDetectedCredentialValues && len(requirements) > 0 {
		values := sourceValuesForMaskedRequirements(rawDraft, masked, requirements)
		for requirementID := range values {
			sourceAvailable = append(sourceAvailable, requirementID)
		}
		sort.Strings(sourceAvailable)
		var err error
		sessionID, err = s.registerSourceSecretSession(r, stored.ProjectID, values, "script_builder.existing_scripts")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if sessionID == "" {
			sourceAvailable = nil
		} else {
			available := make(map[string]struct{}, len(sourceAvailable))
			for _, requirementID := range sourceAvailable {
				available[requirementID] = struct{}{}
			}
			for index := range requirements {
				_, requirements[index].SourceValueAvailable = available[requirements[index].ID]
			}
		}
	}
	bindings := make([]secretBindingSelection, 0, len(requirements))
	for _, requirement := range requirements {
		bindings = append(bindings, secretBindingSelection{
			RequirementID: requirement.ID,
			SecretName:    requirement.SuggestedName,
		})
	}
	editable, remaining, err := applySecretBindings(masked, 0, bindings)
	if err != nil || len(remaining) != 0 {
		writeError(w, http.StatusInternalServerError, "failed to apply safe secret references to cloned script")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"script":                       editable,
		"secret_binding_requirements":  requirements,
		"source_secret_session_id":     sessionID,
		"source_value_requirement_ids": sourceAvailable,
		"secret_provider_capabilities": s.projectSecretCapabilities(stored.ProjectID),
	})
}

// registerSourceSecretSession stores raw source values in a bounded,
// envelope-encrypted shared session when durable storage is available. The
// in-memory fallback is retained for single-controller/test configurations;
// HA fails closed rather than creating a replica-local migration handle.
// Policy or caller authority denial intentionally returns an empty ID.
func (s *Server) registerSourceSecretSession(r *http.Request, projectID string, values map[string]string, featureIDs ...string) (string, error) {
	projectID = normalizeProjectID(projectID)
	if len(values) == 0 || !secretstore.SupportsCreate(s.projectSecretStore) || !s.sourceSecretMigrationAuthorized(r, projectID) {
		return "", nil
	}
	copied := make(map[string]string, len(values))
	total := 0
	for rawID, value := range values {
		id := strings.TrimSpace(rawID)
		if id == "" || value == "" {
			continue
		}
		if _, recoverable := credentialpolicy.RecoverableSourceCredentialValue(value); !recoverable {
			return "", errors.New("source credential is unavailable or contains a disclosure placeholder")
		}
		if _, exists := copied[id]; !exists && len(copied) >= projectSecretMaxBatchItems {
			return "", fmt.Errorf("source credentials exceed %d-entry session limit", projectSecretMaxBatchItems)
		}
		if len(value) > projectSecretMaxValueBytes {
			return "", fmt.Errorf("source credential exceeds %d-byte limit", projectSecretMaxValueBytes)
		}
		if total > sourceSecretSessionMaxTotalBytes-len(value) {
			return "", fmt.Errorf("source credentials exceed %d-byte session limit", sourceSecretSessionMaxTotalBytes)
		}
		total += len(value)
		copied[id] = value
	}
	if len(copied) == 0 {
		return "", nil
	}
	_, actor, _ := s.resolveAuditActor(r)
	now := time.Now().UTC()
	lifetime := s.sourceSecretSessionLifetime
	if lifetime <= 0 {
		lifetime = sourceSecretSessionTTL
	}
	session := &sourceSecretSession{
		ID:        uuid.New().String(),
		ProjectID: projectID,
		Actor:     actor,
		Values:    copied,
		ExpiresAt: now.Add(lifetime),
	}
	if len(featureIDs) > 0 {
		seenFeatures := make(map[string]struct{}, len(featureIDs))
		for _, featureID := range featureIDs {
			featureID = strings.TrimSpace(featureID)
			if featureID == "" {
				continue
			}
			if _, duplicate := seenFeatures[featureID]; duplicate {
				continue
			}
			seenFeatures[featureID] = struct{}{}
			session.FeatureIDs = append(session.FeatureIDs, featureID)
		}
		sort.Strings(session.FeatureIDs)
	}
	if s.protectedSourceSessions != nil && s.protectedSourceSessions.Available() {
		defer clearSourceSecretSession(session)
		if err := s.protectedSourceSessions.Create(r.Context(), secretstore.ProtectedSourceSession{
			ID:         session.ID,
			ProjectID:  session.ProjectID,
			Actor:      session.Actor,
			Values:     session.Values,
			ExpiresAt:  session.ExpiresAt,
			FeatureIDs: session.FeatureIDs,
		}); err != nil {
			return "", err
		}
		return session.ID, nil
	}
	if s.haExecution {
		clearSourceSecretSession(session)
		return "", errors.New("durable encrypted source secret sessions are unavailable for controller HA")
	}
	s.sourceSecretMu.Lock()
	if s.sourceSecretSessions == nil {
		s.sourceSecretSessions = make(map[string]*sourceSecretSession)
	}
	for id, existing := range s.sourceSecretSessions {
		if existing == nil || !existing.ExpiresAt.After(now) {
			clearSourceSecretSession(existing)
			delete(s.sourceSecretSessions, id)
		}
	}
	if len(s.sourceSecretSessions) >= sourceSecretSessionLimit {
		s.sourceSecretMu.Unlock()
		clearSourceSecretSession(session)
		return "", errors.New("too many active source secret sessions; retry after an existing session expires or is consumed")
	}
	s.sourceSecretSessions[session.ID] = session
	session.Timer = time.AfterFunc(lifetime, func() {
		s.expireSourceSecretSession(session.ID, session)
	})
	s.sourceSecretMu.Unlock()
	return session.ID, nil
}

func (s *Server) handleMigrateSourceSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireHumanSecretWrite(w, r) {
		return
	}
	var req sourceSecretMigrationRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	projectID := normalizeProjectID(req.ProjectID)
	if !s.ensureSecretProjectExists(w, projectID) {
		return
	}
	if !s.sourceSecretMigrationAuthorized(r, projectID) {
		writeError(w, http.StatusForbidden, "source secret migration is not allowed")
		return
	}
	if len(req.Secrets) == 0 || len(req.Secrets) > projectSecretMaxBatchItems {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("secrets must contain 1-%d selections", projectSecretMaxBatchItems))
		return
	}
	session, err := s.consumeSourceSecretSessionIf(r, projectID, req.SourceSecretSessionID, func(featureIDs []string) error {
		return s.validateDomainFeaturesForProject(projectID, featureIDs)
	})
	if err != nil {
		var disabled *domainFeatureDisabledError
		if errors.As(err, &disabled) {
			writeDomainFeatureDisabled(w, disabled.domainID, disabled.featureID)
			return
		}
		status := http.StatusConflict
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		} else if errors.Is(err, errDomainFeaturePolicyUnavailable) {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err.Error())
		return
	}
	defer clearSourceSecretSession(session)
	if s.projectSecretStore == nil || !secretstore.SupportsCreate(s.projectSecretStore) {
		writeConsumedSourceSecretFailure(w, http.StatusServiceUnavailable, projectID, "no atomic create-only project secret provider is configured", "create_unsupported", "", nil)
		return
	}
	inputs := make([]projectSecretInput, 0, len(req.Secrets))
	seenRequirements := make(map[string]struct{}, len(req.Secrets))
	for _, selection := range req.Secrets {
		requirementID := strings.TrimSpace(selection.RequirementID)
		if requirementID == "" {
			writeConsumedSourceSecretFailure(w, http.StatusBadRequest, projectID, "requirement_id is required", "invalid_selection", "", nil)
			return
		}
		if _, duplicate := seenRequirements[requirementID]; duplicate {
			writeConsumedSourceSecretFailure(w, http.StatusBadRequest, projectID, fmt.Sprintf("requirement %q was selected more than once", requirementID), "invalid_selection", "", nil)
			return
		}
		seenRequirements[requirementID] = struct{}{}
		value, ok := session.Values[requirementID]
		if !ok {
			writeConsumedSourceSecretFailure(w, http.StatusConflict, projectID, "source secret session does not contain every selected requirement", "session_requirement_missing", "", nil)
			return
		}
		name, err := normalizeProjectSecretName(selection.SecretName)
		if err != nil {
			writeConsumedSourceSecretFailure(w, http.StatusBadRequest, projectID, err.Error(), "invalid_secret_name", "", nil)
			return
		}
		inputs = append(inputs, projectSecretInput{Name: name, Value: value})
	}
	inputs, err = validateProjectSecretInputs(inputs)
	if err != nil {
		writeConsumedSourceSecretFailure(w, http.StatusBadRequest, projectID, err.Error(), "invalid_secret_batch", "", projectSecretInputNames(inputs))
		return
	}
	probeCtx, cancelProbe := engine.WithSecretResolutionBudget(r.Context())
	defer cancelProbe()
	for index, input := range inputs {
		if _, _, err := s.projectSecretStore.Get(probeCtx, projectID, input.Name); err == nil {
			if auditErr := s.persistProjectSecretOutcome(r, "script_import.secret.migrate", "blocked", "already_exists", input.Name, projectID, secretStoreProvider(s.projectSecretStore), "already_exists", nil); auditErr != nil {
				writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "source credential migration audit is unavailable", "migrated", nil, input.Name, "audit_persistence_unavailable", projectSecretInputNames(inputs[index:]), true)
				return
			}
			writeConsumedSourceSecretFailure(w, http.StatusConflict, projectID, fmt.Sprintf("project secret %q already exists", input.Name), "already_exists", input.Name, projectSecretInputNames(inputs[index:]))
			return
		} else if !errors.Is(err, secretstore.ErrNotFound) {
			if auditErr := s.persistProjectSecretOutcome(r, "script_import.secret.migrate", "failed", "provider_probe_failed", input.Name, projectID, secretStoreProvider(s.projectSecretStore), "provider_error", nil); auditErr != nil {
				writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "source credential migration audit is unavailable", "migrated", nil, input.Name, "audit_persistence_unavailable", projectSecretInputNames(inputs[index:]), true)
				return
			}
			writeConsumedSourceSecretFailure(w, http.StatusServiceUnavailable, projectID, "project secret provider is unavailable", "provider_error", input.Name, projectSecretInputNames(inputs[index:]))
			return
		}
	}
	actor := secretActorSubject(s, r)
	migrated := make([]secretstore.Metadata, 0, len(inputs))
	for index, input := range inputs {
		operationID, auditErr := s.beginProjectSecretOperation(r, "script_import.secret.migrate", input.Name, projectID, secretStoreProvider(s.projectSecretStore))
		if auditErr != nil {
			writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "source credential migration audit is unavailable", "migrated", migrated, input.Name, "audit_persistence_unavailable", projectSecretInputNames(inputs[index:]), true)
			return
		}
		metadata, outcome, err := createProjectSecretWithReconciliation(r.Context(), s.projectSecretStore, projectID, input.Name, input.Value, actor)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, secretstore.ErrAlreadyExists) || errors.Is(err, secretstore.ErrReservedBinding) {
				status = http.StatusConflict
			}
			details := map[string]any{}
			if outcome == "provider_error_commit_ambiguous" {
				details["commit_state"] = "ambiguous"
			}
			if auditErr := s.completeProjectSecretOperation(r, operationID, "script_import.secret.migrate", "failed", outcome, input.Name, projectID, secretStoreProvider(s.projectSecretStore), projectSecretErrorReason(err), details); auditErr != nil {
				writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "source credential migration audit is unavailable", "migrated", migrated, input.Name, "audit_persistence_unavailable", projectSecretInputNames(inputs[index:]), true)
				return
			}
			writeProjectSecretPartialFailure(w, status, projectID, "source credential migration failed", "migrated", migrated, input.Name, projectSecretErrorReason(err), projectSecretInputNames(inputs[index:]), true)
			return
		}
		migrated = append(migrated, metadata)
		if auditErr := s.completeProjectSecretOperation(r, operationID, "script_import.secret.migrate", "success", outcome, input.Name, projectID, firstNonEmpty(strings.TrimSpace(metadata.Provider), secretStoreProvider(s.projectSecretStore)), "", nil); auditErr != nil {
			writeProjectSecretPartialFailure(w, http.StatusServiceUnavailable, projectID, "source credential was migrated but its audit outcome could not be persisted; refresh secret status before retrying", "migrated", migrated, "", "audit_persistence_unavailable", projectSecretInputNames(inputs[index+1:]), true)
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project_id": projectID, "secrets": migrated, "count": len(migrated)})
}

func (s *Server) handleDiscardSourceSecretSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireHumanSecretWrite(w, r) {
		return
	}
	var req sourceSecretSessionDiscardRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	projectID := normalizeProjectID(req.ProjectID)
	if !s.ensureSecretProjectExists(w, projectID) {
		return
	}
	if strings.TrimSpace(req.SourceSecretSessionID) == "" {
		writeError(w, http.StatusBadRequest, "source_secret_session_id is required")
		return
	}
	if _, err := s.discardSourceSecretSession(r, projectID, req.SourceSecretSessionID); err != nil {
		writeError(w, http.StatusServiceUnavailable, "protected source-secret session could not be discarded")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeConsumedSourceSecretFailure makes the single-use boundary explicit on
// every failure after Consume. Clients must not retry the opaque session ID;
// they can safely transition to manual configuration without guessing whether
// protected source material is still retained.
func writeConsumedSourceSecretFailure(w http.ResponseWriter, status int, projectID, message, reason, failedName string, remainingNames []string) {
	payload := map[string]any{
		"error":                          message,
		"project_id":                     projectID,
		"reason":                         reason,
		"source_secret_session_consumed": true,
		"manual_configuration_required":  true,
		"migrated":                       []secretstore.Metadata{},
		"migrated_count":                 0,
	}
	if failedName = strings.TrimSpace(failedName); failedName != "" {
		payload["failed_name"] = failedName
	}
	if len(remainingNames) > 0 {
		payload["remaining_names"] = remainingNames
	}
	writeJSON(w, status, payload)
}

// writeProjectSecretPartialFailure reports the durable subset of a non-atomic
// provider batch without returning any write-only value. The HTTP status stays
// non-2xx so callers cannot mistake the batch for complete success. Migration
// sessions are single-use, so their response also directs the caller to
// configure every remaining logical name manually.
func writeProjectSecretPartialFailure(w http.ResponseWriter, status int, projectID, message, resultKey string, completed []secretstore.Metadata, failedName, reason string, remainingNames []string, sessionConsumed bool) {
	payload := map[string]any{
		"error":         message,
		"partial":       true,
		"project_id":    projectID,
		resultKey:       completed,
		"created_count": len(completed),
		"reason":        reason,
	}
	if failedName = strings.TrimSpace(failedName); failedName != "" {
		payload["failed_name"] = failedName
	}
	if resultKey == "migrated" {
		payload["migrated_count"] = len(completed)
	}
	if len(remainingNames) > 0 {
		payload["remaining_names"] = remainingNames
	}
	if sessionConsumed {
		payload["source_secret_session_consumed"] = true
		payload["manual_configuration_required"] = true
	}
	writeJSON(w, status, payload)
}

func projectSecretInputNames(inputs []projectSecretInput) []string {
	if len(inputs) == 0 {
		return nil
	}
	names := make([]string, 0, len(inputs))
	for _, input := range inputs {
		names = append(names, input.Name)
	}
	return names
}

func (s *Server) beginProjectSecretOperation(r *http.Request, action, name, projectID, provider string) (string, error) {
	return s.beginRequiredAuditOperation(r, action, "project_secret", name, projectID, projectSecretAuditDetails(provider, "", nil))
}

func (s *Server) completeProjectSecretOperation(r *http.Request, operationID, action, status, outcome, name, projectID, provider, reason string, extra map[string]any) error {
	return s.completeRequiredAuditOperation(r, operationID, action, status, outcome, "project_secret", name, projectID, projectSecretAuditDetails(provider, reason, extra))
}

func (s *Server) persistProjectSecretOutcome(r *http.Request, action, status, outcome, name, projectID, provider, reason string, extra map[string]any) error {
	operationID, err := s.beginProjectSecretOperation(r, action, name, projectID, provider)
	if err != nil {
		return err
	}
	return s.completeProjectSecretOperation(r, operationID, action, status, outcome, name, projectID, provider, reason, extra)
}

func projectSecretAuditDetails(provider, reason string, extra map[string]any) map[string]any {
	details := make(map[string]any, len(extra)+2)
	if provider = strings.TrimSpace(provider); provider != "" {
		details["provider"] = provider
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		details["reason"] = reason
	}
	for key, value := range extra {
		details[key] = value
	}
	return details
}

func createProjectSecretWithReconciliation(ctx context.Context, store secretstore.Store, projectID, name, value, actor string) (secretstore.Metadata, string, error) {
	mutationCtx, cancel := engine.WithSecretResolutionBudget(ctx)
	defer cancel()
	metadata, err := store.Create(mutationCtx, projectID, name, value, actor)
	if err == nil {
		return metadata, "created", nil
	}
	if projectSecretErrorReason(err) != "provider_error" {
		return secretstore.Metadata{}, projectSecretErrorReason(err), err
	}
	observedValue, observedMetadata, probeErr := store.Get(mutationCtx, projectID, name)
	if probeErr == nil && strings.TrimSpace(observedMetadata.CreatedBy) == strings.TrimSpace(actor) &&
		subtle.ConstantTimeCompare([]byte(observedValue), []byte(value)) == 1 {
		return observedMetadata, "created_reconciled", nil
	}
	if probeErr != nil && !errors.Is(probeErr, secretstore.ErrNotFound) {
		return secretstore.Metadata{}, "provider_error_commit_ambiguous", err
	}
	return secretstore.Metadata{}, "provider_error", err
}

func replaceProjectSecretWithReconciliation(ctx context.Context, store secretstore.Store, projectID, name, value, actor string) (secretstore.Metadata, string, error) {
	mutationCtx, cancel := engine.WithSecretResolutionBudget(ctx)
	defer cancel()
	attemptStarted := time.Now().UTC().Truncate(time.Microsecond)
	metadata, err := store.Replace(mutationCtx, projectID, name, value, actor)
	if err == nil {
		return metadata, "rotated", nil
	}
	if projectSecretErrorReason(err) != "provider_error" {
		return secretstore.Metadata{}, projectSecretErrorReason(err), err
	}
	observedValue, observedMetadata, probeErr := store.Get(mutationCtx, projectID, name)
	if probeErr == nil && projectSecretRotationMetadataCommitted(observedMetadata, projectID, name, actor, attemptStarted) &&
		subtle.ConstantTimeCompare([]byte(observedValue), []byte(value)) == 1 {
		return observedMetadata, "rotated_reconciled", nil
	}
	return secretstore.Metadata{}, "provider_error_commit_ambiguous", err
}

func projectSecretRotationMetadataCommitted(metadata secretstore.Metadata, projectID, name, actor string, attemptStarted time.Time) bool {
	return normalizeProjectID(metadata.ProjectID) == normalizeProjectID(projectID) &&
		strings.EqualFold(strings.TrimSpace(metadata.Name), strings.TrimSpace(name)) &&
		strings.TrimSpace(metadata.Provider) != "" && metadata.Writable &&
		strings.TrimSpace(metadata.CreatedBy) == strings.TrimSpace(actor) &&
		!metadata.UpdatedAt.IsZero() && !metadata.UpdatedAt.Before(attemptStarted)
}

func writeProjectSecretAuditUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "project secret operation audit is unavailable; no successful outcome can be acknowledged")
}

func (s *Server) consumeSourceSecretSession(r *http.Request, projectID, sessionID string) (*sourceSecretSession, error) {
	return s.consumeSourceSecretSessionIf(r, projectID, sessionID, nil)
}

func (s *Server) consumeSourceSecretSessionIf(r *http.Request, projectID, sessionID string, validate func([]string) error) (*sourceSecretSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("source_secret_session_id is required")
	}
	_, actor, _ := s.resolveAuditActor(r)
	if s.protectedSourceSessions != nil && s.protectedSourceSessions.Available() {
		protected, err := s.protectedSourceSessions.ConsumeIf(r.Context(), sessionID, projectID, actor, validate)
		if err != nil {
			return nil, err
		}
		session := &sourceSecretSession{
			ID:         protected.ID,
			ProjectID:  protected.ProjectID,
			Actor:      protected.Actor,
			Values:     protected.Values,
			ExpiresAt:  protected.ExpiresAt,
			FeatureIDs: protected.FeatureIDs,
		}
		if err := validateRecoverableSourceSecretSession(session); err != nil {
			clearSourceSecretSession(session)
			return nil, err
		}
		return session, nil
	}
	if s.haExecution {
		return nil, errors.New("durable encrypted source secret sessions are unavailable for controller HA")
	}
	now := time.Now().UTC()
	s.sourceSecretMu.Lock()
	defer s.sourceSecretMu.Unlock()
	session := s.sourceSecretSessions[sessionID]
	if session == nil {
		return nil, sql.ErrNoRows
	}
	if !session.ExpiresAt.After(now) {
		delete(s.sourceSecretSessions, sessionID)
		clearSourceSecretSession(session)
		return nil, errors.New("source secret session expired")
	}
	if session.ProjectID != projectID || session.Actor != actor {
		return nil, sql.ErrNoRows
	}
	if err := validateRecoverableSourceSecretSession(session); err != nil {
		delete(s.sourceSecretSessions, sessionID)
		clearSourceSecretSession(session)
		return nil, err
	}
	if validate != nil {
		if err := validate(append([]string(nil), session.FeatureIDs...)); err != nil {
			return nil, err
		}
	}
	delete(s.sourceSecretSessions, sessionID)
	if session.Timer != nil {
		session.Timer.Stop()
		session.Timer = nil
	}
	return session, nil
}

func validateRecoverableSourceSecretSession(session *sourceSecretSession) error {
	if session == nil {
		return errors.New("source secret session is invalid")
	}
	for _, value := range session.Values {
		if _, recoverable := credentialpolicy.RecoverableSourceCredentialValue(value); !recoverable {
			return errors.New("source secret session contains an unavailable credential value")
		}
	}
	return nil
}

func (s *Server) discardSourceSecretSession(r *http.Request, projectID, sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	projectID = normalizeProjectID(projectID)
	if sessionID == "" {
		return false, errors.New("source_secret_session_id is required")
	}
	_, actor, _ := s.resolveAuditActor(r)
	if s.protectedSourceSessions != nil && s.protectedSourceSessions.Available() {
		return s.protectedSourceSessions.Discard(r.Context(), sessionID, projectID, actor)
	}
	if s.haExecution {
		return false, errors.New("durable encrypted source secret sessions are unavailable for controller HA")
	}
	s.sourceSecretMu.Lock()
	defer s.sourceSecretMu.Unlock()
	session := s.sourceSecretSessions[sessionID]
	if session == nil || session.ProjectID != projectID || session.Actor != actor {
		return false, nil
	}
	delete(s.sourceSecretSessions, sessionID)
	clearSourceSecretSession(session)
	return true, nil
}

func (s *Server) startProtectedSourceSecretSessionJanitor() {
	if s == nil || s.protectedSourceSessions == nil || !s.protectedSourceSessions.Available() || s.sourceSecretCleanupCancel != nil {
		return
	}
	if _, err := s.protectedSourceSessions.DeleteExpired(time.Now().UTC()); err != nil {
		slog.Warn("failed to prune expired protected source secret sessions", "error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.sourceSecretCleanupCancel = cancel
	if !s.launchBackground(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if _, err := s.protectedSourceSessions.DeleteExpired(now.UTC()); err != nil {
					slog.Warn("failed to prune expired protected source secret sessions", "error", err)
				}
			}
		}
	}) {
		cancel()
		s.sourceSecretCleanupCancel = nil
	}
}

func clearSourceSecretSession(session *sourceSecretSession) {
	if session == nil {
		return
	}
	if session.Timer != nil {
		session.Timer.Stop()
		session.Timer = nil
	}
	for key := range session.Values {
		session.Values[key] = ""
		delete(session.Values, key)
	}
	session.Values = nil
}

func (s *Server) expireSourceSecretSession(id string, expected *sourceSecretSession) {
	if s == nil {
		return
	}
	s.sourceSecretMu.Lock()
	session := s.sourceSecretSessions[id]
	if session == expected {
		delete(s.sourceSecretSessions, id)
		clearSourceSecretSession(session)
	}
	s.sourceSecretMu.Unlock()
}

func (s *Server) clearAllSourceSecretSessions() {
	if s == nil {
		return
	}
	s.sourceSecretMu.Lock()
	for id, session := range s.sourceSecretSessions {
		clearSourceSecretSession(session)
		delete(s.sourceSecretSessions, id)
	}
	s.sourceSecretMu.Unlock()
}

func (s *Server) clearProjectSourceSecretSessions(projectID string) {
	if s == nil {
		return
	}
	projectID = normalizeProjectID(projectID)
	s.sourceSecretMu.Lock()
	for id, session := range s.sourceSecretSessions {
		if session == nil || session.ProjectID != projectID {
			continue
		}
		clearSourceSecretSession(session)
		delete(s.sourceSecretSessions, id)
	}
	s.sourceSecretMu.Unlock()
}

func (s *Server) projectSecretStatuses(r *http.Request, projectID string, names []string) ([]projectSecretStatus, error) {
	ctx, cancel := engine.WithSecretResolutionBudget(r.Context())
	defer cancel()
	var legacyResolver engine.SecretResolver
	result := make([]projectSecretStatus, 0, len(names))
	for _, name := range names {
		status := projectSecretStatus{Name: name}
		if s.projectSecretStore != nil {
			value, metadata, err := s.projectSecretStore.Get(ctx, projectID, name)
			if err == nil && engine.IsUsableResolvedSecretValue(value) {
				status.Configured = true
				status.Provider = metadata.Provider
				status.Writable = metadata.Writable
				status.SourceScope = "project"
			} else if !errors.Is(err, secretstore.ErrNotFound) {
				if err != nil {
					return nil, err
				}
			}
		}
		if !status.Configured && engine.ProjectScriptSecretEnvAvailable(projectID, name) {
			status.Configured = true
			status.Provider = "environment_read_only"
			status.SourceScope = "project"
		}
		if !status.Configured && engine.ProjectKeyVaultBindingAllowlisted(projectID, name) {
			if legacyResolver == nil {
				legacyResolver = engine.NewProjectSecretResolver(projectID)
			}
			if _, found := engine.ResolveSecret(ctx, legacyResolver, name); found {
				status.Configured = true
				status.Provider = "azure_key_vault"
				status.SourceScope = "project"
			}
		}
		result = append(result, status)
	}
	return result, nil
}

func normalizeProjectSecretNames(raw []string) ([]string, error) {
	if len(raw) > projectSecretMaxBatchItems {
		return nil, fmt.Errorf("names must contain at most %d entries", projectSecretMaxBatchItems)
	}
	seen := make(map[string]struct{}, len(raw))
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		name, err := normalizeProjectSecretName(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeProjectSecretName(raw string) (string, error) {
	name, err := engine.NormalizeLogicalSecretName(raw)
	if err != nil || !logicalSecretNamePattern.MatchString(name) {
		return "", errors.New("secret name must contain 1-249 letters, digits, underscores, dots, or hyphens")
	}
	return name, nil
}

func validateProjectSecretValue(value string) error {
	if len(value) == 0 || len(value) > projectSecretMaxValueBytes {
		return fmt.Errorf("secret value must contain 1-%d bytes", projectSecretMaxValueBytes)
	}
	if strings.EqualFold(strings.TrimSpace(value), scrub.Redacted) {
		return errors.New("secret value cannot be the [REDACTED] sentinel; enter the actual value or configure it later")
	}
	return nil
}

func validateProjectSecretInputs(raw []projectSecretInput) ([]projectSecretInput, error) {
	if len(raw) == 0 || len(raw) > projectSecretMaxBatchItems {
		return nil, fmt.Errorf("secrets must contain 1-%d entries", projectSecretMaxBatchItems)
	}
	seen := make(map[string]string, len(raw))
	result := make([]projectSecretInput, 0, len(raw))
	total := 0
	for _, input := range raw {
		name, err := normalizeProjectSecretName(input.Name)
		if err != nil {
			return nil, err
		}
		if err := validateProjectSecretValue(input.Value); err != nil {
			return nil, err
		}
		if total > projectSecretMaxBatchBytes-len(input.Value) {
			return nil, fmt.Errorf("secret values exceed %d-byte batch limit", projectSecretMaxBatchBytes)
		}
		total += len(input.Value)
		if previous, exists := seen[name]; exists {
			if previous != input.Value {
				return nil, fmt.Errorf("secret %q has conflicting values in the same request", name)
			}
			continue
		}
		seen[name] = input.Value
		result = append(result, projectSecretInput{Name: name, Value: input.Value, ExpectedUsage: input.ExpectedUsage})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func scriptSecretReferences(script *dsl.Script) []string {
	if script == nil {
		return []string{}
	}
	referenced, err := engine.ReferencedSecretNames(script)
	if err != nil {
		return []string{}
	}
	found := make(map[string]struct{})
	for _, name := range referenced {
		if normalized, err := normalizeProjectSecretName(name); err == nil {
			found[normalized] = struct{}{}
		}
	}
	result := make([]string, 0, len(found))
	for name := range found {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func sourceValuesForMaskedRequirements(rawScript, maskedScript *dsl.Script, requirements []SecretBindingRequirement) map[string]string {
	rawValue, err := scriptAsJSONValue(rawScript)
	if err != nil {
		return nil
	}
	maskedValue, err := scriptAsJSONValue(maskedScript)
	if err != nil {
		return nil
	}
	segmentsByPath := make(map[string][]string)
	result := make(map[string]string)
	for _, requirement := range requirements {
		raw, rawOK := jsonPointerStringValue(rawValue, requirement.Path)
		masked, maskedOK := jsonPointerStringValue(maskedValue, requirement.Path)
		if !rawOK || !maskedOK {
			continue
		}
		segments, cached := segmentsByPath[requirement.Path]
		if !cached {
			segments, _ = extractProjectedRedactions(raw, masked)
			segmentsByPath[requirement.Path] = segments
		}
		if requirement.Occurrence < 0 || requirement.Occurrence >= len(segments) {
			continue
		}
		if value := segments[requirement.Occurrence]; value != "" && value != maskedScriptSecret {
			result[requirement.ID] = value
		}
	}
	return result
}

func jsonPointerStringValue(root any, path string) (string, bool) {
	tokens, err := splitJSONPointer(path)
	if err != nil {
		return "", false
	}
	current := root
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			current, _ = typed[token]
		case []any:
			index := -1
			if _, err := fmt.Sscanf(token, "%d", &index); err != nil || index < 0 || index >= len(typed) {
				return "", false
			}
			current = typed[index]
		default:
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok
}

func (s *Server) sourceSecretMigrationAuthorized(r *http.Request, projectID string) bool {
	if s == nil || s.runner == nil || !s.runner.GetSystemSettings().ScriptImports.AllowSourceSecretMigration {
		return false
	}
	if _, serviceAccount := AuthenticatedServiceAccountFromContext(r.Context()); serviceAccount {
		return false
	}
	if isAuthBypassMode(s.authSettingsForRequest().Mode) {
		return true
	}
	user, ok := AuthenticatedUserFromContext(r.Context())
	return ok && user != nil &&
		(user.hasGlobalRole(RoleProjectAdmin, RoleSystemAdmin) || userHasProjectOrParentRole(s.store, user, projectID, RoleProjectAdmin))
}

func (s *Server) requireHumanSecretWrite(w http.ResponseWriter, r *http.Request) bool {
	if _, serviceAccount := AuthenticatedServiceAccountFromContext(r.Context()); serviceAccount {
		writeError(w, http.StatusForbidden, "service accounts cannot write project secrets")
		return false
	}
	if s.runner != nil && isAuthBypassMode(s.authSettingsForRequest().Mode) {
		return true
	}
	if _, ok := AuthenticatedUserFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "human authentication is required")
		return false
	}
	return true
}

func (s *Server) ensureSecretProjectExists(w http.ResponseWriter, projectID string) bool {
	if strings.TrimSpace(projectID) == "" {
		writeError(w, http.StatusBadRequest, "project id is required")
		return false
	}
	if s.store == nil {
		return true
	}
	if _, err := s.store.GetProject(projectID); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return false
	}
	return true
}

func secretActorSubject(s *Server, r *http.Request) string {
	_, actor, _ := s.resolveAuditActor(r)
	return actor
}

func projectSecretErrorReason(err error) string {
	switch {
	case errors.Is(err, secretstore.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, secretstore.ErrReservedBinding):
		return "reserved_binding"
	case errors.Is(err, secretstore.ErrNotFound):
		return "not_found"
	case errors.Is(err, secretstore.ErrReadOnly):
		return "read_only"
	case errors.Is(err, secretstore.ErrCreateUnsupported):
		return "create_unsupported"
	default:
		return "provider_error"
	}
}
