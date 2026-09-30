package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func writeApplicationSecretPartialFailure(w http.ResponseWriter, code int, app *storage.Application, message, failedName string, created []secretstore.Metadata) {
	writeJSON(w, code, map[string]any{"error": message, "application_id": app.ID, "project_id": app.ProjectID,
		"failed_name": failedName, "created": applicationPublicSecretMetadata(created, app)})
}

func (s *Server) scriptSecretStatuses(r *http.Request, script *dsl.Script, names []string) ([]projectSecretStatus, string, error) {
	if s.applicationSecretStore == nil || s.runner == nil || s.runner.libraryAssignmentStore == nil {
		statuses, err := s.projectSecretStatuses(r, script.ProjectID, names)
		return statuses, "", err
	}
	assignments, err := s.runner.libraryAssignmentStore.ListLibraryAssignments("script", []string{script.ID})
	if err != nil {
		return nil, "", err
	}
	assignment := assignments[script.ID]
	if assignment == nil || assignment.Application == nil {
		statuses, err := s.projectSecretStatuses(r, script.ProjectID, names)
		return statuses, "", err
	}
	app, err := s.store.GetApplication(assignment.Application.ID)
	if err != nil || app == nil || normalizeProjectID(app.ProjectID) != normalizeProjectID(script.ProjectID) {
		return nil, "", errors.New("script application assignment is unavailable")
	}
	resolved, err := s.applicationSecretStatuses(r.Context(), app, names)
	if err != nil {
		return nil, "", err
	}
	statuses := make([]projectSecretStatus, 0, len(resolved))
	for _, item := range resolved {
		status := projectSecretStatus{Name: item["name"].(string), Configured: item["configured"].(bool)}
		status.Provider, _ = item["provider"].(string)
		status.Writable, _ = item["writable"].(bool)
		status.SourceScope, _ = item["source_scope"].(string)
		statuses = append(statuses, status)
	}
	return statuses, app.ID, nil
}

func (s *Server) secretApplication(w http.ResponseWriter, r *http.Request) (*storage.Application, bool) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is unavailable")
		return nil, false
	}
	app, err := s.store.GetApplication(strings.TrimSpace(r.PathValue("id")))
	if err != nil || app == nil {
		writeError(w, http.StatusNotFound, "application not found")
		return nil, false
	}
	if !requireAuthorizedProjectMatch(w, r, app.ProjectID) {
		return nil, false
	}
	return app, true
}

func (s *Server) applicationSecretCapabilities() map[string]any {
	provider := "unavailable"
	writable, create := false, false
	if s.applicationSecretStore != nil {
		provider = s.applicationSecretStore.Provider()
		writable = s.applicationSecretStore.Writable()
		create = secretstore.SupportsCreate(s.applicationSecretStore)
	}
	return map[string]any{"provider": provider, "writable": writable,
		"create_supported": create, "replace_supported": writable,
		"source_secret_migration_policy_enabled": false}
}

func applicationPublicSecretMetadata(records []secretstore.Metadata, app *storage.Application) []secretstore.Metadata {
	result := publicProjectSecretMetadata(records)
	for index := range result {
		result[index].ProjectID = app.ProjectID
	}
	return result
}

func (s *Server) handleListApplicationSecrets(w http.ResponseWriter, r *http.Request) {
	app, ok := s.secretApplication(w, r)
	if !ok {
		return
	}
	metadata := []secretstore.Metadata{}
	if s.applicationSecretStore != nil {
		listed, err := s.applicationSecretStore.List(r.Context(), storage.ApplicationSecretScopeID(app.ID))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "application secret metadata is unavailable")
			return
		}
		metadata = applicationPublicSecretMetadata(listed, app)
	}
	writeJSON(w, http.StatusOK, map[string]any{"application_id": app.ID, "project_id": app.ProjectID,
		"capabilities": s.applicationSecretCapabilities(), "secrets": metadata, "count": len(metadata)})
}

func (s *Server) applicationSecretStatuses(ctx context.Context, app *storage.Application, names []string) ([]map[string]any, error) {
	ctx, cancel := engine.WithSecretResolutionBudget(ctx)
	defer cancel()
	var legacyProjectResolver engine.SecretResolver
	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		status := map[string]any{"name": name, "configured": false}
		if s.applicationSecretStore != nil {
			value, metadata, err := s.applicationSecretStore.Get(ctx, storage.ApplicationSecretScopeID(app.ID), name)
			if err == nil && engine.IsUsableResolvedSecretValue(value) {
				status["configured"], status["provider"], status["writable"], status["source_scope"] =
					true, metadata.Provider, metadata.Writable, "application"
			} else if err != nil && !errors.Is(err, secretstore.ErrNotFound) {
				return nil, err
			}
		}
		if status["configured"] == false {
			if s.projectSecretStore != nil {
				value, metadata, err := s.projectSecretStore.Get(ctx, app.ProjectID, name)
				if err == nil && engine.IsUsableResolvedSecretValue(value) {
					status["configured"], status["provider"], status["writable"], status["source_scope"] =
						true, metadata.Provider, metadata.Writable, "project"
				} else if err != nil && !errors.Is(err, secretstore.ErrNotFound) {
					return nil, err
				}
			}
			if status["configured"] == false && engine.ProjectScriptSecretEnvAvailable(app.ProjectID, name) {
				status["configured"], status["provider"], status["source_scope"] = true, "environment_read_only", "project"
			}
			if status["configured"] == false && engine.ProjectKeyVaultBindingAllowlisted(app.ProjectID, name) {
				if legacyProjectResolver == nil {
					legacyProjectResolver = engine.NewProjectSecretResolver(app.ProjectID)
				}
				if _, found := engine.ResolveSecret(ctx, legacyProjectResolver, name); found {
					status["configured"], status["provider"], status["source_scope"] = true, "azure_key_vault", "project"
				}
			}
		}
		result = append(result, status)
	}
	return result, nil
}

func (s *Server) handleApplicationSecretStatus(w http.ResponseWriter, r *http.Request) {
	app, ok := s.secretApplication(w, r)
	if !ok {
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
	statuses, err := s.applicationSecretStatuses(r.Context(), app, names)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "application secret status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"application_id": app.ID, "project_id": app.ProjectID,
		"capabilities": s.applicationSecretCapabilities(), "secrets": statuses})
}

func (s *Server) handleApplicationSecretUsage(w http.ResponseWriter, r *http.Request) {
	app, ok := s.secretApplication(w, r)
	if !ok {
		return
	}
	name, err := normalizeProjectSecretName(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, offset, err := parseSecretUsagePagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	usage, err := s.applicationSecretUsage(app, name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "application secret usage is unavailable")
		return
	}
	start := min(offset, len(usage.Consumers))
	end := min(start+limit, len(usage.Consumers))
	writeJSON(w, http.StatusOK, map[string]any{"application_id": app.ID, "project_id": app.ProjectID,
		"name": name, "usage_revision": usage.UsageRevision, "consumer_count": usage.ConsumerCount,
		"reference_count": usage.ReferenceCount, "consumers": usage.Consumers[start:end],
		"limit": limit, "offset": offset, "has_more": end < len(usage.Consumers),
		"consumer_scope": "assigned_application_scripts"})
}

func (s *Server) applicationSecretUsage(app *storage.Application, name string) (projectSecretUsage, error) {
	usage, err := s.projectSecretUsage(app.ProjectID, name)
	if err != nil {
		return usage, err
	}
	if s.runner == nil || s.runner.libraryAssignmentStore == nil {
		return usage, errors.New("script application assignments are unavailable")
	}
	ids := make([]string, 0, len(usage.Consumers))
	for _, consumer := range usage.Consumers {
		ids = append(ids, consumer.ScriptID)
	}
	assignments, err := s.runner.libraryAssignmentStore.ListLibraryAssignments("script", ids)
	if err != nil {
		return usage, err
	}
	consumers := make([]projectSecretConsumer, 0, len(usage.Consumers))
	usage.ReferenceCount = 0
	for _, consumer := range usage.Consumers {
		assignment := assignments[consumer.ScriptID]
		if assignment == nil || assignment.Application == nil || assignment.Application.ID != app.ID {
			continue
		}
		consumers = append(consumers, consumer)
		usage.ReferenceCount += consumer.ReferenceCount
	}
	usage.Consumers = consumers
	usage.ConsumerCount = len(consumers)
	usage.UsageRevision = projectSecretUsageRevision(storage.ApplicationSecretScopeID(app.ID), name, consumers)
	return usage, nil
}

func (s *Server) handleCreateApplicationSecrets(w http.ResponseWriter, r *http.Request) {
	app, ok := s.secretApplication(w, r)
	if !ok || !s.requireHumanSecretWrite(w, r) {
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
	if !secretstore.SupportsCreate(s.applicationSecretStore) {
		writeError(w, http.StatusServiceUnavailable, "no atomic application secret provider is configured")
		return
	}
	unlock, err := s.acquireSecretReferenceMutation(app.ProjectID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "secret usage fence is unavailable")
		return
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(r.Context(), projectSecretMutationBudget)
	defer cancel()
	scopeID := storage.ApplicationSecretScopeID(app.ID)
	created := make([]secretstore.Metadata, 0, len(inputs))
	for _, input := range inputs {
		if input.ExpectedUsage != nil {
			usage, err := s.applicationSecretUsage(app, input.Name)
			if err != nil {
				writeApplicationSecretPartialFailure(w, http.StatusServiceUnavailable, app,
					"secret usage is unavailable", input.Name, created)
				return
			}
			if err := validateExpectedProjectSecretUsage(usage, input.ExpectedUsage); err != nil {
				writeApplicationSecretPartialFailure(w, http.StatusConflict, app, err.Error(), input.Name, created)
				return
			}
		}
		operationID, err := s.beginRequiredAuditOperation(r, "application_secret.create", "application_secret",
			input.Name, app.ProjectID, map[string]any{"application_id": app.ID})
		if err != nil {
			writeApplicationSecretPartialFailure(w, http.StatusServiceUnavailable, app,
				"application secret audit is unavailable", input.Name, created)
			return
		}
		metadata, outcome, err := createProjectSecretWithReconciliation(ctx, s.applicationSecretStore,
			scopeID, input.Name, input.Value, secretActorSubject(s, r))
		status := "success"
		if err != nil {
			status = "failed"
		}
		auditErr := s.completeRequiredAuditOperation(r, operationID, "application_secret.create", status, outcome,
			"application_secret", input.Name, app.ProjectID, map[string]any{"application_id": app.ID, "provider": metadata.Provider})
		if auditErr != nil {
			if err == nil {
				created = append(created, metadata)
			}
			writeApplicationSecretPartialFailure(w, http.StatusServiceUnavailable, app,
				"application secret audit outcome is unavailable; refresh status before retrying", input.Name, created)
			return
		}
		if err != nil {
			code := http.StatusServiceUnavailable
			if errors.Is(err, secretstore.ErrAlreadyExists) {
				code = http.StatusConflict
			}
			if len(created) > 0 {
				writeApplicationSecretPartialFailure(w, code, app,
					"application secret could not be created; refresh status before retrying", input.Name, created)
			} else {
				writeError(w, code, "application secret could not be created; refresh status before retrying")
			}
			return
		}
		created = append(created, metadata)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"application_id": app.ID, "project_id": app.ProjectID,
		"secrets": applicationPublicSecretMetadata(created, app), "count": len(created)})
}

func (s *Server) handleRotateApplicationSecret(w http.ResponseWriter, r *http.Request) {
	app, ok := s.secretApplication(w, r)
	if !ok || !s.requireHumanSecretWrite(w, r) {
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
	unlock, err := s.acquireSecretReferenceMutation(app.ProjectID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "secret usage fence is unavailable")
		return
	}
	defer unlock()
	usage, err := s.applicationSecretUsage(app, name)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "secret usage is unavailable")
		return
	}
	if req.ExpectedUsageRevision != "" && req.ExpectedUsageRevision != usage.UsageRevision {
		writeError(w, http.StatusConflict, "secret consumers changed; refresh usage before rotating")
		return
	}
	if usage.ConsumerCount > 1 && (req.ExpectedUsageRevision == "" || !req.AcknowledgeSharedUsage) {
		writeError(w, http.StatusConflict, "shared secret rotation requires current usage and acknowledgement")
		return
	}
	if s.applicationSecretStore == nil || !s.applicationSecretStore.Writable() {
		writeError(w, http.StatusServiceUnavailable, "no writable application secret provider is configured")
		return
	}
	operationID, err := s.beginRequiredAuditOperation(r, "application_secret.rotate", "application_secret",
		name, app.ProjectID, map[string]any{"application_id": app.ID})
	if err != nil {
		writeProjectSecretAuditUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), projectSecretMutationBudget)
	defer cancel()
	metadata, outcome, err := replaceProjectSecretWithReconciliation(ctx, s.applicationSecretStore,
		storage.ApplicationSecretScopeID(app.ID), name, req.Value, secretActorSubject(s, r))
	status := "success"
	if err != nil {
		status = "failed"
	}
	auditErr := s.completeRequiredAuditOperation(r, operationID, "application_secret.rotate", status, outcome,
		"application_secret", name, app.ProjectID, map[string]any{"application_id": app.ID, "provider": metadata.Provider})
	if auditErr != nil {
		writeProjectSecretAuditUnavailable(w)
		return
	}
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, secretstore.ErrNotFound) {
			code = http.StatusNotFound
		}
		if errors.Is(err, secretstore.ErrReadOnly) {
			code = http.StatusConflict
		}
		writeError(w, code, "application secret could not be rotated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"application_id": app.ID, "project_id": app.ProjectID,
		"secret": applicationPublicSecretMetadata([]secretstore.Metadata{metadata}, app)[0], "usage": usage})
}
