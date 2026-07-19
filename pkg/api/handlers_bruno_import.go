package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/brunoartifact"
	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
	brunoprotocol "github.com/jasonczech/apexload/pkg/protocol/bruno"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	brunoImportSessionTTL         = 30 * time.Minute
	brunoImportSessionLimit       = 128
	brunoImportSessionMaxBytes    = 64 << 20
	defaultBrunoUploadBytes       = 64 << 20
	defaultBrunoExpandedBytes     = 32 << 20
	defaultBrunoMaterializedBytes = 1 << 30
	defaultBrunoImportFileCount   = 100_000
)

type brunoImportSession struct {
	ID                string
	ProjectID         string
	Actor             string
	Result            *brunoconvert.Result
	RawArtifact       storage.BrunoSourceArtifact
	SanitizedArtifact storage.BrunoSourceArtifact
	RevisionID        string
	SyncScriptID      string
	CreatedAt         time.Time
	ExpiresAt         time.Time
	Timer             *time.Timer
}

type persistedBrunoImportSession struct {
	ID                string                      `json:"id"`
	ProjectID         string                      `json:"project_id"`
	Actor             string                      `json:"actor"`
	Result            *brunoconvert.Result        `json:"result"`
	RawArtifact       storage.BrunoSourceArtifact `json:"raw_artifact"`
	SanitizedArtifact storage.BrunoSourceArtifact `json:"sanitized_artifact"`
	RevisionID        string                      `json:"revision_id"`
	SyncScriptID      string                      `json:"sync_script_id,omitempty"`
	CreatedAt         time.Time                   `json:"created_at"`
	ExpiresAt         time.Time                   `json:"expires_at"`
	SecretValues      map[string]string           `json:"secret_values,omitempty"`
}

type brunoArtifactSummary struct {
	ID             string `json:"id"`
	ArtifactKind   string `json:"artifact_kind"`
	SHA256         string `json:"sha256"`
	SizeBytes      int64  `json:"size_bytes"`
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
}

type brunoImportConflict struct {
	ID         string `json:"id"`
	Field      string `json:"field"`
	Summary    string `json:"summary"`
	Resolution string `json:"required_resolution"`
}

type brunoSyncPreview struct {
	TargetScriptID    string                `json:"target_script_id"`
	ExpectedUpdatedAt time.Time             `json:"expected_updated_at"`
	ActiveRevision    string                `json:"active_revision,omitempty"`
	Changes           map[string]any        `json:"changes"`
	Conflicts         []brunoImportConflict `json:"conflicts,omitempty"`
}

type brunoInspectResponse struct {
	ImportSessionID    string                 `json:"import_session_id"`
	ExpiresAt          time.Time              `json:"expires_at"`
	Result             *brunoconvert.Result   `json:"result"`
	Artifacts          []brunoArtifactSummary `json:"artifacts"`
	SyncPreview        *brunoSyncPreview      `json:"sync_preview,omitempty"`
	ExecutionPreflight map[string]any         `json:"execution_preflight"`
	Parameterization   *paramize.Result       `json:"parameterization"`
}

type brunoCommitRequest struct {
	ProjectID              string            `json:"project_id"`
	ImportSessionID        string            `json:"import_session_id"`
	Mode                   string            `json:"mode,omitempty"`
	ScriptID               string            `json:"script_id,omitempty"`
	SyncScriptID           string            `json:"sync_script_id,omitempty"`
	Name                   string            `json:"name,omitempty"`
	FolderID               *string           `json:"folder_id,omitempty"`
	ExpectedUpdatedAt      *time.Time        `json:"expected_updated_at,omitempty"`
	ExpectedActiveRevision string            `json:"expected_active_revision_id,omitempty"`
	SecretBindings         map[string]string `json:"secret_bindings,omitempty"`
	MigrateSecrets         *bool             `json:"migrate_secrets,omitempty"`
	ParameterizationIDs    []string          `json:"parameterization_selected_ids,omitempty"`
	ConflictResolutions    map[string]string `json:"conflict_resolutions,omitempty"`
}

type brunoSecretMigrationPartialError struct {
	Cause          error
	Migrated       []secretstore.Metadata
	FailedName     string
	RemainingNames []string
}

var brunoInspectConcurrency = make(chan struct{}, 1)

func (e *brunoSecretMigrationPartialError) Error() string {
	return "Bruno source credential migration partially completed"
}

func (e *brunoSecretMigrationPartialError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type brunoInspectJSONRequest struct {
	ProjectID     string                      `json:"project_id"`
	Name          string                      `json:"name,omitempty"`
	Filename      string                      `json:"filename,omitempty"`
	Content       string                      `json:"content,omitempty"`
	ContentBase64 string                      `json:"content_base64,omitempty"`
	Files         []brunoInspectJSONFile      `json:"files,omitempty"`
	Sandbox       string                      `json:"sandbox,omitempty"`
	RootPath      string                      `json:"root_path,omitempty"`
	Options       brunoconvert.InspectOptions `json:"options,omitempty"`
	SyncScriptID  string                      `json:"sync_script_id,omitempty"`
}

type brunoInspectJSONFile struct {
	Path          string `json:"path"`
	Content       string `json:"content,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

func (s *Server) handleBrunoSupportMatrix(w http.ResponseWriter, _ *http.Request) {
	secretCapabilities := s.projectSecretCapabilities("")
	secretCapabilities["fallback"] = "binding_only"
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version":              brunoconvert.ImporterVersion,
		"compatibility_version":       brunoconvert.CompatibilityVersion,
		"import_enabled":              brunoFeatureEnabled("APEXLOAD_BRUNO_IMPORT_ENABLED", true),
		"safe_execution_enabled":      brunoFeatureEnabled("APEXLOAD_BRUNO_SAFE_EXECUTION_ENABLED", true),
		"developer_execution_enabled": brunoFeatureEnabled("APEXLOAD_BRUNO_DEVELOPER_EXECUTION_ENABLED", false),
		"native_projection_enabled":   brunoFeatureEnabled("APEXLOAD_BRUNO_NATIVE_PROJECTION_ENABLED", false),
		"source_formats":              []string{"classic .bru", "OpenCollection YAML", "standalone .bru/YAML", "ZIP", "TAR", "TAR.GZ", "directory upload"},
		"request_types":               []string{"HTTP", "SOAP-over-HTTP", "GraphQL", "gRPC unary/client-stream/server-stream/bidirectional-stream", "WebSocket"},
		"sandbox_modes": map[string]any{
			"safe": map[string]any{
				"default":       true,
				"filesystem":    false,
				"child_process": false,
				"arbitrary_npm": false,
			},
			"developer": map[string]any{
				"operator_enabled":         brunoFeatureEnabled("APEXLOAD_BRUNO_DEVELOPER_EXECUTION_ENABLED", false),
				"isolated_worker_required": true,
			},
		},
		"intentional_extensions": []string{
			"gRPC requests execute in collection runs",
			"WebSocket requests execute in collection runs",
			"bru.runRequest supports HTTP, GraphQL, gRPC, and WebSocket",
		},
		"retention": map[string]any{
			"raw_source":        "encrypted and administrator-restricted",
			"execution_source":  "sanitized encrypted bundle",
			"content_addressed": true,
		},
		"execution_governance": map[string]any{
			"egress_broker_required":  true,
			"egress_configured_here":  brunoprotocol.GovernedEgressConfigured(),
			"runner_transport":        "loopback_only",
			"unresolved_secrets_fail": "before runner session creation",
		},
		"secret_store": secretCapabilities,
	})
}

func (s *Server) handleInspectBrunoImport(w http.ResponseWriter, r *http.Request) {
	if !brunoFeatureEnabled("APEXLOAD_BRUNO_IMPORT_ENABLED", true) {
		writeError(w, http.StatusServiceUnavailable, "Bruno importing is disabled by operator policy")
		return
	}
	select {
	case brunoInspectConcurrency <- struct{}{}:
		defer func() { <-brunoInspectConcurrency }()
	case <-r.Context().Done():
		writeError(w, http.StatusRequestTimeout, "Bruno import upload was canceled")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, brunoImportMaxBodyBytes)
	input, syncScriptID, err := readBrunoInspectInput(r)
	if err != nil {
		writeError(w, importErrorStatus(err), err.Error())
		return
	}
	projectID, status, message := reconcileImportProjectID(r.URL.Query().Get("project_id"), input.ProjectID, "request body")
	if status != 0 {
		writeError(w, status, message)
		return
	}
	input.ProjectID = projectID
	result, err := brunoconvert.Inspect(input)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	migrationAuthorized := secretstore.SupportsCreate(s.projectSecretStore) && s.sourceSecretMigrationAuthorized(r, input.ProjectID)
	for index := range result.SecretRequirements {
		available := migrationAuthorized && result.SecretRequirements[index].Value != ""
		result.SecretRequirements[index].SourceValueAvailable = available
		if !available {
			result.SecretRequirements[index].Value = ""
		}
	}
	boundedValues, err := boundedBrunoImportSecretValues(result.SecretRequirements)
	clearStringMap(boundedValues)
	if err != nil {
		// Secret migration is optional. Keep the import non-blocking by dropping
		// every source value when the bounded migration session cannot retain the
		// full set safely; callers can configure the generated bindings later.
		clearBrunoImportSecretValues(result)
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "protected Bruno source retention is unavailable: "+err.Error())
		return
	}
	rawSource := result.OriginalUpload
	if len(rawSource) == 0 {
		rawSource = result.RawArchive
	}
	if len(rawSource) == 0 || len(result.SanitizedArchive) == 0 {
		writeError(w, http.StatusInternalServerError, "Bruno importer produced an empty source artifact")
		return
	}
	rawObject, err := objects.PutBytes(r.Context(), input.ProjectID, "raw", rawSource, int64(len(rawSource)))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "retain raw Bruno source: "+err.Error())
		return
	}
	sanitizedObject, err := objects.PutBytes(r.Context(), input.ProjectID, "sanitized", result.SanitizedArchive, int64(len(result.SanitizedArchive)))
	if err != nil {
		s.cleanupUnreferencedBrunoArtifacts([]storage.BrunoSourceArtifact{{BackendKey: rawObject.BackendKey, ProjectID: input.ProjectID, ArtifactKind: "raw", SHA256: rawObject.SHA256, SizeBytes: rawObject.SizeBytes}})
		writeError(w, http.StatusUnprocessableEntity, "retain sanitized Bruno source: "+err.Error())
		return
	}
	now := time.Now().UTC()
	revisionID := uuid.NewString()
	rawArtifact := storage.BrunoSourceArtifact{
		ID:                 uuid.NewString(),
		ProjectID:          input.ProjectID,
		ArtifactKind:       "raw",
		BackendKey:         rawObject.BackendKey,
		SHA256:             rawObject.SHA256,
		SizeBytes:          rawObject.SizeBytes,
		ManifestSHA256:     result.Manifest.SHA256,
		SourceFormat:       result.SourceFormat,
		EncryptionMetadata: rawObject.EncryptionMetadata,
		LifecycleState:     "active",
		CreatedAt:          now,
	}
	sanitizedArtifact := storage.BrunoSourceArtifact{
		ID:                 uuid.NewString(),
		ProjectID:          input.ProjectID,
		ArtifactKind:       "sanitized",
		BackendKey:         sanitizedObject.BackendKey,
		SHA256:             sanitizedObject.SHA256,
		SizeBytes:          sanitizedObject.SizeBytes,
		ManifestSHA256:     result.Manifest.SHA256,
		SourceFormat:       result.SourceFormat,
		EncryptionMetadata: sanitizedObject.EncryptionMetadata,
		LifecycleState:     "active",
		CreatedAt:          now,
	}
	result.Script.Config.Bruno.BundleRef = "artifact://" + sanitizedArtifact.ID
	result.Script.Config.Bruno.BundleSHA256 = sanitizedArtifact.SHA256
	result.Script.Config.Bruno.SourceRevision = revisionID
	// Only advertise the server-replayable, disclosure-safe data suggestions
	// that the commit endpoint can derive again. Credential suggestions belong
	// exclusively to the governed Credentials card and never cross this wire.
	parameterization := buildTrustedImportParameterization(result.Script)
	result.RawArchive = nil
	result.SanitizedArchive = nil
	result.OriginalUpload = nil
	_, actor, _ := s.resolveAuditActor(r)
	session := &brunoImportSession{
		ID:                uuid.NewString(),
		ProjectID:         input.ProjectID,
		Actor:             actor,
		Result:            result,
		RawArtifact:       rawArtifact,
		SanitizedArtifact: sanitizedArtifact,
		RevisionID:        revisionID,
		SyncScriptID:      strings.TrimSpace(syncScriptID),
		CreatedAt:         now,
		ExpiresAt:         now.Add(brunoImportSessionTTL),
	}
	if err := s.persistBrunoImportSession(r.Context(), session); err != nil {
		s.cleanupBrunoImportSessionArtifacts(session)
		writeError(w, http.StatusServiceUnavailable, "persist Bruno import session: "+err.Error())
		return
	}
	// The encrypted, one-shot persisted session is authoritative. Do not retain
	// a second plaintext copy in the controller's in-memory lifecycle index.
	clearBrunoImportSecretValuePayloads(session.Result)
	s.storeBrunoImportSession(session)
	var syncPreview *brunoSyncPreview
	if session.SyncScriptID != "" {
		syncPreview, err = s.previewBrunoSynchronization(session.SyncScriptID, input.ProjectID, result)
		if err != nil {
			s.deleteBrunoImportSession(session.ID)
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "Bruno synchronization target script not found")
			} else {
				writeError(w, http.StatusConflict, err.Error())
			}
			return
		}
	}
	s.recordAuditEvent(r, "script.import.bruno.inspect", "success", "bruno_import_session", session.ID, input.ProjectID, map[string]any{
		"source_format": result.SourceFormat,
		"files":         len(result.Manifest.Files),
		"source_bytes":  result.Manifest.TotalBytes,
		"sandbox":       result.Script.Config.Bruno.Sandbox,
		"request_types": result.Capabilities.RequestTypes,
	})
	writeJSON(w, http.StatusOK, brunoInspectResponse{
		ImportSessionID: session.ID,
		ExpiresAt:       session.ExpiresAt,
		Result:          result,
		Artifacts: []brunoArtifactSummary{
			brunoSanitizedArtifactPublicSummary(sanitizedArtifact),
		},
		SyncPreview:        syncPreview,
		ExecutionPreflight: s.brunoExecutionPreflight(r.Context(), result.Script.Config.Bruno.Sandbox),
		Parameterization:   parameterization,
	})
}

func (s *Server) brunoExecutionPreflight(ctx context.Context, sandbox string) map[string]any {
	var blockers []string
	if strings.EqualFold(strings.TrimSpace(sandbox), "developer") {
		if !brunoFeatureEnabled("APEXLOAD_BRUNO_DEVELOPER_EXECUTION_ENABLED", false) {
			blockers = append(blockers, "developer_execution_disabled")
		}
	} else if !brunoFeatureEnabled("APEXLOAD_BRUNO_SAFE_EXECUTION_ENABLED", true) {
		blockers = append(blockers, "safe_execution_disabled")
	}
	if !brunoprotocol.GovernedEgressConfigured() {
		blockers = append(blockers, "governed_egress_unavailable")
	}
	runnerReady := false
	if s != nil && s.runner != nil {
		inventory := s.runner.cachedGeneratorInventory()
		for _, entry := range inventory {
			if generatorInventoryEntryReachable(entry) && entry.DrainState == "active" &&
				(stringSliceContainsEqualFold(entry.SupportedProtocols, "bruno") ||
					stringSliceContainsEqualFold(entry.CompatibleFeatures, "bruno_runner_"+brunoconvert.CompatibilityVersion)) {
				runnerReady = true
				break
			}
		}
		if len(inventory) == 0 {
			runnerReady = brunoprotocol.RunnerHealthy(ctx)
		}
	}
	if !runnerReady {
		blockers = append(blockers, "compatible_bruno_runner_unavailable")
	}
	return map[string]any{"ready": len(blockers) == 0, "blockers": blockers}
}

func (s *Server) handleCommitBrunoImport(w http.ResponseWriter, r *http.Request) {
	if !brunoFeatureEnabled("APEXLOAD_BRUNO_IMPORT_ENABLED", true) {
		writeError(w, http.StatusServiceUnavailable, "Bruno importing is disabled by operator policy")
		return
	}
	var request brunoCommitRequest
	if err := decodeRequestBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	request.ProjectID = normalizeProjectID(request.ProjectID)
	session, err := s.loadBrunoImportSession(r, request.ImportSessionID, request.ProjectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "Bruno import session was already claimed, consumed, or expired; inspect the source again unless an earlier response reported session_retained=true",
				"code":  "bruno_import_session_consumed",
			})
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer clearBrunoImportSecretValues(session.Result)
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno revision storage is not configured")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	targetID := strings.TrimSpace(firstNonEmpty(request.SyncScriptID, session.SyncScriptID))
	if mode == "" {
		if targetID != "" {
			mode = "sync"
		} else {
			mode = "create"
		}
	}
	if mode != "create" && mode != "sync" {
		writeError(w, http.StatusBadRequest, "mode must be create or sync")
		return
	}
	if mode == "sync" && targetID == "" {
		writeError(w, http.StatusBadRequest, "sync_script_id is required for Bruno synchronization")
		return
	}
	var existing *dsl.Script
	var activeRevision *storage.BrunoSourceRevision
	if mode == "sync" {
		existing, err = s.store.GetScript(targetID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "Bruno synchronization target script not found")
			} else {
				writeError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		if normalizeProjectID(existing.ProjectID) != request.ProjectID {
			writeError(w, http.StatusForbidden, "Bruno synchronization target belongs to another project")
			return
		}
		activeRevision, err = brunoStore.GetActiveBrunoSourceRevision(existing.ID, request.ProjectID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		conflicts := detectBrunoCanonicalConflicts(existing, activeRevision)
		if unresolved := unresolvedBrunoConflicts(conflicts, request.ConflictResolutions); len(unresolved) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":     "Bruno synchronization has unresolved canonical-script conflicts",
				"code":      "bruno_import_conflicts",
				"conflicts": unresolved,
			})
			return
		}
		if request.ExpectedUpdatedAt == nil || request.ExpectedUpdatedAt.IsZero() {
			writeError(w, http.StatusBadRequest, "expected_updated_at is required for Bruno synchronization")
			return
		}
	}
	script := cloneScriptDefinition(session.Result.Script)
	if script == nil {
		writeError(w, http.StatusInternalServerError, "Bruno import session has no canonical script")
		return
	}
	parameterizationApplied := 0
	if len(request.ParameterizationIDs) > 0 {
		trusted := buildTrustedImportParameterization(script)
		trustedByID := make(map[string]bool, len(trusted.Suggestions))
		for _, suggestion := range trusted.Suggestions {
			trustedByID[suggestion.ID] = true
		}
		selected := make(map[string]bool, len(request.ParameterizationIDs))
		for _, id := range request.ParameterizationIDs {
			if !trustedByID[id] {
				writeError(w, http.StatusConflict, "selected Bruno parameterization suggestions are stale or unavailable; inspect the source again")
				return
			}
			selected[id] = true
		}
		parameterized, diffs, applyErr := paramize.ApplySuggestions(script, trusted.Suggestions, selected)
		if applyErr != nil {
			writeError(w, http.StatusUnprocessableEntity, "failed to apply Bruno parameterization: "+applyErr.Error())
			return
		}
		projected := maskScriptSecrets(parameterized)
		if projected == nil {
			writeError(w, http.StatusInternalServerError, "failed to project parameterized Bruno script")
			return
		}
		script = projected
		appliedIDs := make(map[string]bool, len(diffs))
		for _, diff := range diffs {
			if diff.SuggestionID != "" {
				appliedIDs[diff.SuggestionID] = true
			}
		}
		parameterizationApplied = len(appliedIDs)
	}
	if mode == "create" {
		script.ID = strings.TrimSpace(request.ScriptID)
		if script.ID == "" {
			script.ID = uuid.NewString()
		}
		if request.FolderID != nil {
			script.FolderID = cloneStringPtr(request.FolderID)
		}
		if strings.TrimSpace(request.Name) != "" {
			script.Name = strings.TrimSpace(request.Name)
		}
	} else {
		script = mergeBrunoSynchronization(existing, script)
		script.ID = existing.ID
		script.RefKey = existing.RefKey
		script.CreatedAt = existing.CreatedAt
		if request.FolderID != nil {
			script.FolderID = cloneStringPtr(request.FolderID)
		}
		if strings.TrimSpace(request.Name) != "" {
			script.Name = strings.TrimSpace(request.Name)
		}
	}
	script.ProjectID = request.ProjectID
	if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "Bruno canonical script validation failed",
			"code":   "bruno_script_validation_failed",
			"issues": issues,
		})
		return
	}
	migrateSecrets := request.MigrateSecrets != nil && *request.MigrateSecrets
	if migrateSecrets && !s.sourceSecretMigrationAuthorized(r, request.ProjectID) {
		writeError(w, http.StatusForbidden, "source secret migration is not allowed")
		return
	}
	if migrateSecrets && (s.projectSecretStore == nil || !secretstore.SupportsCreate(s.projectSecretStore)) {
		writeError(w, http.StatusServiceUnavailable, "no atomic create-only project secret provider is configured")
		return
	}
	// Claim the encrypted one-shot session before any source credential write or
	// durable identifier allocation. Concurrent requests may both complete the
	// read-only checks above, but only one is allowed to cross this boundary.
	claimedSession, err := s.consumeBrunoImportSession(r, request.ImportSessionID, request.ProjectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "Bruno import session was already consumed or expired; inspect the source again unless an earlier response reported session_retained=true",
				"code":  "bruno_import_session_consumed",
			})
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "Bruno import session is already claimed or unavailable: " + err.Error(),
			"code":  "bruno_import_session_claimed",
		})
		return
	}
	session = claimedSession
	defer clearBrunoImportSecretValues(session.Result)
	claimActive := true
	restoreClaimedSession := func() (bool, error) {
		if !claimActive {
			return true, nil
		}
		claimActive = false
		if restoreErr := s.restoreClaimedBrunoImportSession(session); restoreErr != nil {
			return false, restoreErr
		}
		return true, nil
	}
	defer func() {
		if claimActive {
			_, _ = restoreClaimedSession()
		}
	}()
	if mode == "create" && script.RefKey == "" {
		script.RefKey, err = s.allocateScriptRefKey()
		if err != nil {
			_, restoreErr := restoreClaimedSession()
			writeError(w, http.StatusInternalServerError, errors.Join(fmt.Errorf("allocate script reference: %w", err), restoreErr).Error())
			return
		}
	}
	_, actorSubject, _ := s.resolveAuditActor(r)
	secretBindings, missing, migrations, migrationConflicts, err := s.bindOrMigrateBrunoImportSecrets(
		r,
		session.Result.SecretRequirements,
		request.SecretBindings,
		existing,
		request.ProjectID,
		actorSubject,
		migrateSecrets,
	)
	if err != nil {
		var partial *brunoSecretMigrationPartialError
		if errors.As(err, &partial) {
			sessionRetained, restoreErr := restoreClaimedSession()
			reason := projectSecretErrorReason(partial.Cause)
			if restoreErr != nil {
				reason = firstNonEmpty(reason, "session_restore_failed")
			}
			s.recordAuditEvent(r, "script.import.bruno."+mode, "failure", "bruno_import_session", session.ID, request.ProjectID, map[string]any{
				"stage":            "secret_migration",
				"reason":           reason,
				"secrets_migrated": len(partial.Migrated),
			})
			writeBrunoSecretPartialFailure(
				w,
				brunoSecretMigrationFailureStatus(partial.Cause),
				"bruno_secret_migration_partial",
				"Bruno source credential migration partially completed",
				request.ProjectID,
				partial.Migrated,
				partial.RemainingNames,
				partial.FailedName,
				reason,
				len(partial.RemainingNames) > 0,
				sessionRetained,
				"The committed names remain configured. Correct the provider failure or bind the remaining names, then retry this import session.",
			)
			return
		}
		if retained, restoreErr := restoreClaimedSession(); !retained {
			writeError(w, http.StatusInternalServerError, errors.Join(err, fmt.Errorf("restore Bruno import session: %w", restoreErr)).Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(migrationConflicts) > 0 {
		if retained, restoreErr := restoreClaimedSession(); !retained {
			writeError(w, http.StatusInternalServerError, "restore Bruno import session: "+restoreErr.Error())
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        "Bruno secret migration found existing values under automatically selected names",
			"code":         "bruno_secret_migration_conflict",
			"requirements": migrationConflicts,
			"resolution":   "Choose an explicit existing binding name or rename the imported binding.",
		})
		return
	}
	if len(missing) > 0 {
		if retained, restoreErr := restoreClaimedSession(); !retained {
			writeError(w, http.StatusInternalServerError, "restore Bruno import session: "+restoreErr.Error())
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":        "Bruno secret bindings are required before commit",
			"code":         secretBindingsRequiredCode,
			"requirements": missing,
		})
		return
	}
	script.Config.Bruno.SecretBindings = secretBindings
	// Bruno commits intentionally combine the library row, encrypted source
	// artifacts, and revision record in one storage transaction, so they cannot
	// use TestRunner's ordinary script write. Apply the same final persistence
	// boundary before handing the canonical script to that transactional store.
	safeScript, err := preparePersistentLibraryScript(script, existing)
	if err != nil {
		sessionRetained, restoreErr := restoreClaimedSession()
		if len(migrations) > 0 {
			s.recordAuditEvent(r, "script.import.bruno."+mode, "failure", "bruno_import_session", session.ID, request.ProjectID, map[string]any{
				"stage":            "script_validation",
				"reason":           "secret_bindings_required",
				"secrets_migrated": len(migrations),
			})
			writeBrunoSecretPartialFailure(
				w,
				http.StatusUnprocessableEntity,
				"bruno_import_validation_failed_after_secret_migration",
				"Bruno script validation failed after source credentials were migrated",
				request.ProjectID,
				migrations,
				nil,
				"",
				"secret_bindings_required",
				false,
				sessionRetained,
				"Correct the validation issue and retry this import session; already migrated logical names remain configured.",
			)
			return
		}
		if restoreErr != nil {
			writeError(w, http.StatusInternalServerError, errors.Join(err, fmt.Errorf("restore Bruno import session: %w", restoreErr)).Error())
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "Bruno import contains executable credentials that require logical secret bindings",
			"code":  secretBindingsRequiredCode,
		})
		return
	}
	script = safeScript
	script.Config.Bruno.SourceRevision = session.RevisionID
	newIdentity := brunoCollectionIdentityMap(session.Result.Collection)
	changeSummary := summarizeBrunoIdentityChanges(nil, newIdentity)
	parentRevisionID := ""
	expectedActive := strings.TrimSpace(request.ExpectedActiveRevision)
	if activeRevision != nil {
		parentRevisionID = activeRevision.ID
		if expectedActive == "" {
			expectedActive = activeRevision.ID
		}
		changeSummary = summarizeBrunoIdentityChanges(activeRevision.IdentityMap, newIdentity)
	}
	commit := &storage.BrunoImportCommit{
		Script:                   script,
		ExpectedScriptUpdatedAt:  request.ExpectedUpdatedAt,
		ExpectedActiveRevisionID: expectedActive,
		RawArtifact:              session.RawArtifact,
		SanitizedArtifact:        session.SanitizedArtifact,
		Revision: storage.BrunoSourceRevision{
			ID:                    session.RevisionID,
			ParentRevisionID:      parentRevisionID,
			SourceFormat:          session.Result.SourceFormat,
			CompatibilityVersion:  brunoconvert.CompatibilityVersion,
			IdentityMap:           newIdentity,
			ImportOptions:         brunoImportOptionsFromScript(script),
			DependencySnapshotRef: script.Config.Bruno.DependencySnapshotRef,
			SecurityProfile: map[string]any{
				"sandbox":           script.Config.Bruno.Sandbox,
				"runner_profile":    script.Config.Bruno.RunnerProfile,
				"capabilities":      session.Result.Capabilities,
				"security_findings": session.Result.SecurityFindings,
				"native_projection": session.Result.NativeProjection,
				"secret_store": map[string]any{
					"provider":         secretStoreProvider(s.projectSecretStore),
					"migrated":         len(migrations),
					"writable":         s.projectSecretStore != nil && s.projectSecretStore.Writable(),
					"create_supported": secretstore.SupportsCreate(s.projectSecretStore),
					"requested":        migrateSecrets,
				},
			},
			ChangeSummary:  changeSummary,
			SecretBindings: logicalBrunoSecretBindings(secretBindings),
			CreatedBy:      actorSubject,
		},
	}
	commitResult, err := brunoStore.CommitBrunoImport(commit)
	if err != nil {
		// Validation has completed, but the transactional commit failed. Restore
		// the encrypted one-shot session so optimistic conflicts and transient DB
		// failures can be corrected without a full source re-upload.
		sessionRetained, restoreErr := restoreClaimedSession()
		if restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore Bruno import session: %w", restoreErr))
		}
		if len(migrations) > 0 {
			reason := brunoCommitErrorReason(err)
			s.recordAuditEvent(r, "script.import.bruno."+mode, "failure", "bruno_import_session", session.ID, request.ProjectID, map[string]any{
				"stage":            "script_commit",
				"reason":           reason,
				"secrets_migrated": len(migrations),
			})
			writeBrunoSecretPartialFailure(
				w,
				brunoCommitErrorStatus(err),
				"bruno_import_commit_failed_after_secret_migration",
				"Bruno script commit failed after source credentials were migrated",
				request.ProjectID,
				migrations,
				nil,
				"",
				reason,
				false,
				sessionRetained,
				"Retry this import session; already migrated logical names remain configured.",
			)
			return
		}
		switch {
		case errors.Is(err, storage.ErrBrunoImportConcurrentChange):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrBrunoRevisionNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	claimActive = false
	s.cleanupBrunoImportSessionArtifacts(session)
	if _, _, refreshErr := s.runner.GetScriptDurable(commitResult.Script.ID); refreshErr != nil {
		slogWarnBrunoCacheRefresh(refreshErr)
	}
	action := "script.import.bruno.create"
	if mode == "sync" {
		action = "script.import.bruno.sync"
	}
	s.recordAuditEvent(r, action, "success", "script", commitResult.Script.ID, request.ProjectID, map[string]any{
		"revision_id":              commitResult.Revision.ID,
		"parent_revision":          commitResult.Revision.ParentRevisionID,
		"source_format":            commitResult.Revision.SourceFormat,
		"change_summary":           changeSummary,
		"secrets_migrated":         len(migrations),
		"parameterization_applied": parameterizationApplied,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"script":                   commitResult.Script,
		"revision":                 publicBrunoRevision(commitResult.Revision),
		"created":                  commitResult.Created,
		"change_summary":           changeSummary,
		"secret_migrations":        migrations,
		"parameterization_applied": parameterizationApplied,
	})
}

func writeBrunoSecretPartialFailure(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	projectID string,
	migrated []secretstore.Metadata,
	remainingNames []string,
	failedName string,
	reason string,
	manualConfigurationRequired bool,
	sessionRetained bool,
	resolution string,
) {
	committedNames := make([]string, 0, len(migrated))
	for _, metadata := range migrated {
		committedNames = append(committedNames, metadata.Name)
	}
	sort.Strings(committedNames)
	remaining := append([]string{}, remainingNames...)
	sort.Strings(remaining)
	payload := map[string]any{
		"error":                         message,
		"code":                          code,
		"partial":                       true,
		"project_id":                    projectID,
		"script_committed":              false,
		"migrated":                      append([]secretstore.Metadata(nil), migrated...),
		"migrated_count":                len(migrated),
		"committed_names":               committedNames,
		"remaining_names":               remaining,
		"reason":                        reason,
		"import_session_consumed":       !sessionRetained,
		"source_values_cleared":         !sessionRetained,
		"manual_configuration_required": manualConfigurationRequired,
		"committed_secrets_retained":    len(migrated) > 0,
		"resolution":                    resolution,
	}
	if failedName != "" {
		payload["failed_name"] = failedName
	}
	writeJSON(w, status, payload)
}

func brunoSecretMigrationFailureStatus(err error) int {
	if errors.Is(err, secretstore.ErrAlreadyExists) {
		return http.StatusConflict
	}
	return http.StatusServiceUnavailable
}

func brunoCommitErrorStatus(err error) int {
	switch {
	case errors.Is(err, storage.ErrBrunoImportConcurrentChange):
		return http.StatusConflict
	case errors.Is(err, storage.ErrBrunoRevisionNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func brunoCommitErrorReason(err error) string {
	switch {
	case errors.Is(err, storage.ErrBrunoImportConcurrentChange):
		return "concurrent_change"
	case errors.Is(err, storage.ErrBrunoRevisionNotFound):
		return "revision_not_found"
	default:
		return "storage_error"
	}
}

func (s *Server) handleGetBrunoImportSource(w http.ResponseWriter, r *http.Request) {
	extendArtifactWriteDeadline(w)
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno revision storage is not configured")
		return
	}
	script, err := s.store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if script.Protocol != "bruno" {
		writeError(w, http.StatusConflict, "script is not a canonical Bruno import")
		return
	}
	projectID := normalizeProjectID(script.ProjectID)
	raw := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("raw")), "true")
	if raw && !s.canReadRawBrunoSource(r, projectID) {
		s.recordAuditEvent(r, "script.import.bruno.source.raw", "denied", "script", script.ID, projectID, map[string]any{"reason": "insufficient_role"})
		writeError(w, http.StatusForbidden, "raw Bruno source access requires project_admin or system_admin permission")
		return
	}
	revision, err := brunoStore.GetActiveBrunoSourceRevision(script.ID, projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "active Bruno source revision not found")
		return
	}
	privateKeyFiles := brunoRevisionHasPrivateKeyFiles(revision)
	if !raw && privateKeyFiles && !s.canReadRawBrunoSource(r, projectID) {
		s.recordAuditEvent(r, "script.import.bruno.source.sanitized", "denied", "script", script.ID, projectID, map[string]any{"reason": "private_key_material"})
		writeError(w, http.StatusForbidden, "this Bruno source bundle contains private-key files and requires project_admin or system_admin permission")
		return
	}
	artifactID := revision.SanitizedArtifactID
	if raw {
		artifactID = revision.RawArtifactID
	}
	artifact, err := brunoStore.GetBrunoSourceArtifact(artifactID, projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Bruno source artifact not found")
		return
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno source decryption is unavailable: "+err.Error())
		return
	}
	reader, size, err := objects.Open(r.Context(), brunoartifact.OpenRequest{
		BackendKey: artifact.BackendKey,
		ProjectID:  projectID,
		Kind:       artifact.ArtifactKind,
		SHA256:     artifact.SHA256,
		SizeBytes:  artifact.SizeBytes,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open Bruno source artifact: "+err.Error())
		return
	}
	defer reader.Close()
	filename := sanitizeDownloadName(script.Name)
	if filename == "" {
		filename = "bruno-source"
	}
	contentType := "application/x-tar"
	extension := ".sanitized.tar"
	sensitivity := "sanitized"
	if privateKeyFiles {
		sensitivity = "restricted-sanitized"
	}
	if raw {
		contentType = "application/octet-stream"
		extension = ".raw"
		sensitivity = "raw"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s%s"`, filename, extension))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-APEXLoad-Sensitivity", sensitivity)
	w.Header().Set("X-APEXLoad-Source-Revision", revision.ID)
	w.Header().Set("ETag", `"`+artifact.SHA256+`"`)
	if raw || privateKeyFiles {
		action := "script.import.bruno.source.sanitized_restricted"
		if raw {
			action = "script.import.bruno.source.raw"
		}
		s.recordAuditEvent(r, action, "success", "script", script.ID, projectID, map[string]any{
			"revision_id": revision.ID,
			"artifact_id": artifact.ID,
		})
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, reader); err != nil {
		return
	}
}

func brunoRevisionHasPrivateKeyFiles(revision *storage.BrunoSourceRevision) bool {
	if revision == nil || revision.SecurityProfile == nil {
		return false
	}
	capabilities, ok := revision.SecurityProfile["capabilities"].(map[string]any)
	if !ok {
		return false
	}
	value, _ := capabilities["has_private_key_files"].(bool)
	return value
}

func (s *Server) handleListBrunoImportRevisions(w http.ResponseWriter, r *http.Request) {
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno revision storage is not configured")
		return
	}
	script, err := s.store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	revisions, err := brunoStore.ListBrunoSourceRevisions(script.ID, normalizeProjectID(script.ProjectID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	public := make([]*storage.BrunoSourceRevision, 0, len(revisions))
	for _, revision := range revisions {
		public = append(public, publicBrunoRevision(revision))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": public, "count": len(public)})
}

func (s *Server) handleRollbackBrunoImport(w http.ResponseWriter, r *http.Request) {
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno revision storage is not configured")
		return
	}
	script, err := s.store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	var request struct {
		RevisionID             string `json:"revision_id"`
		ExpectedActiveRevision string `json:"expected_active_revision_id,omitempty"`
	}
	if err := decodeRequestBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	projectID := normalizeProjectID(script.ProjectID)
	targetRevisionID := strings.TrimSpace(request.RevisionID)
	targetRevision, err := brunoStore.GetBrunoSourceRevision(targetRevisionID, projectID)
	if err != nil {
		if errors.Is(err, storage.ErrBrunoRevisionNotFound) || errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, storage.ErrBrunoRevisionNotFound.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if targetRevision.CanonicalScript == nil {
		writeError(w, http.StatusUnprocessableEntity, "Bruno revision has no canonical script snapshot")
		return
	}
	// Activation updates the script config directly inside the revision
	// transaction. Validate the immutable canonical snapshot at the same final
	// boundary as current imports before allowing a pre-upgrade revision to
	// become executable. A successful projection cannot differ in executable
	// config: any credential that projection would redact is rejected as an
	// unresolved binding before activation.
	if _, err := preparePersistentLibraryScript(targetRevision.CanonicalScript, script); err != nil {
		s.recordAuditEvent(r, "script.import.bruno.rollback", "blocked", "script", script.ID, script.ProjectID, map[string]any{
			"revision_id": targetRevisionID,
			"reason":      secretBindingsRequiredCode,
		})
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "Bruno revision contains executable credentials that require logical secret bindings",
			"code":  secretBindingsRequiredCode,
		})
		return
	}
	revision, err := brunoStore.ActivateBrunoSourceRevision(
		script.ID,
		projectID,
		targetRevisionID,
		strings.TrimSpace(request.ExpectedActiveRevision),
	)
	if err != nil {
		if errors.Is(err, storage.ErrBrunoImportConcurrentChange) {
			writeError(w, http.StatusConflict, err.Error())
		} else if errors.Is(err, storage.ErrBrunoRevisionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if _, _, refreshErr := s.runner.GetScriptDurable(script.ID); refreshErr != nil {
		slogWarnBrunoCacheRefresh(refreshErr)
	}
	s.recordAuditEvent(r, "script.import.bruno.rollback", "success", "script", script.ID, script.ProjectID, map[string]any{
		"revision_id": revision.ID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"revision": publicBrunoRevision(revision)})
}

func (s *Server) handlePreviewBrunoProjection(w http.ResponseWriter, r *http.Request) {
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno revision storage is not configured")
		return
	}
	script, err := s.store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	revision, err := brunoStore.GetActiveBrunoSourceRevision(script.ID, normalizeProjectID(script.ProjectID))
	if err != nil {
		writeError(w, http.StatusNotFound, "active Bruno source revision not found")
		return
	}
	report := revision.SecurityProfile["native_projection"]
	writeJSON(w, http.StatusOK, map[string]any{
		"script_id":          script.ID,
		"source_revision_id": revision.ID,
		"projection":         report,
		"proof_status":       "not_run",
		"eligible":           false,
		"required_evidence": []string{
			"deterministic canonical-versus-native mock execution",
			"wire-level request order/method/URL/header/body equality",
			"variable mutation and assertion-result equality",
		},
	})
}

func (s *Server) handleCommitBrunoProjection(w http.ResponseWriter, r *http.Request) {
	if !brunoFeatureEnabled("APEXLOAD_BRUNO_NATIVE_PROJECTION_ENABLED", false) {
		writeError(w, http.StatusServiceUnavailable, "Bruno native projection is disabled until equivalence proof is enabled by operator policy")
		return
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": "No current Bruno revision has a completed differential-equivalence proof; canonical Bruno execution remains authoritative",
		"code":  "bruno_projection_proof_required",
	})
}

func (s *Server) brunoArtifactStore() (*brunoartifact.EncryptedStore, error) {
	s.brunoArtifactMu.Lock()
	defer s.brunoArtifactMu.Unlock()
	if s.brunoArtifacts != nil {
		return s.brunoArtifacts, nil
	}
	root := strings.TrimSpace(s.brunoArtifactDir)
	if root == "" {
		root = filepath.Join(s.importAssetDir, ".bruno-artifacts")
	}
	wrapper := s.brunoArtifactWrapper
	if wrapper == nil {
		var err error
		wrapper, err = keywrap.NewConfiguredKeyWrapper(s.desktopMode)
		if err != nil {
			return nil, err
		}
	}
	objects, err := brunoartifact.NewEncryptedStore(brunoartifact.StoreOptions{
		RootDir:         root,
		Wrapper:         wrapper,
		SessionCapacity: brunoImportSessionLimit,
	})
	if err != nil {
		return nil, err
	}
	s.brunoArtifacts = objects
	if metadata, ok := s.store.(brunoartifact.ArtifactMetadataReader); ok && metadata != nil {
		brunoartifact.SetResolver(&brunoartifact.StoreResolver{Objects: s.brunoArtifacts, Metadata: metadata})
	}
	return s.brunoArtifacts, nil
}

func readBrunoInspectInput(r *http.Request) (brunoconvert.Input, string, error) {
	limit := configuredBrunoImportBytes("APEXLOAD_BRUNO_IMPORT_MAX_UPLOAD_BYTES", defaultBrunoUploadBytes, brunoImportMaxBodyBytes)
	limits := brunoconvert.DefaultLimits()
	limits.MaxFileBytes = limit
	limits.MaxExpandedBytes = configuredBrunoImportBytes("APEXLOAD_BRUNO_IMPORT_MAX_EXPANDED_BYTES", defaultBrunoExpandedBytes, defaultBrunoExpandedBytes)
	limits.MaxMaterializedBytes = configuredBrunoImportBytes("APEXLOAD_BRUNO_IMPORT_MAX_MATERIALIZED_BYTES", defaultBrunoMaterializedBytes, defaultBrunoMaterializedBytes)
	limits.MaxFiles = configuredBrunoImportInt("APEXLOAD_BRUNO_IMPORT_MAX_FILES", defaultBrunoImportFileCount, 1_000_000)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var request brunoInspectJSONRequest
		if err := decodeRequestBody(r, &request); err != nil {
			return brunoconvert.Input{}, "", fmt.Errorf("invalid request body: %w", err)
		}
		input := brunoconvert.Input{
			Name:      request.Name,
			ProjectID: jsonImportProjectID(r, request.ProjectID),
			Filename:  request.Filename,
			Sandbox:   request.Sandbox,
			RootPath:  request.RootPath,
			Options:   request.Options,
			Limits:    limits,
		}
		if len(request.Files) > 0 {
			input.Files = make(map[string][]byte, len(request.Files))
			var total int64
			for _, file := range request.Files {
				data, err := decodeBrunoJSONContent(file.Content, file.ContentBase64)
				if err != nil {
					return brunoconvert.Input{}, "", fmt.Errorf("decode Bruno file %q: %w", file.Path, err)
				}
				total += int64(len(data))
				if total > limit {
					return brunoconvert.Input{}, "", fmt.Errorf("Bruno upload exceeds %d bytes", limit)
				}
				if _, duplicate := input.Files[file.Path]; duplicate {
					return brunoconvert.Input{}, "", fmt.Errorf("Bruno file path %q is duplicated", file.Path)
				}
				input.Files[file.Path] = data
			}
		} else {
			data, err := decodeBrunoJSONContent(request.Content, request.ContentBase64)
			if err != nil {
				return brunoconvert.Input{}, "", err
			}
			if int64(len(data)) > limit {
				return brunoconvert.Input{}, "", fmt.Errorf("Bruno upload exceeds %d bytes", limit)
			}
			input.Content = data
		}
		return input, request.SyncScriptID, nil
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return readBrunoMultipartInput(r, limits, limit)
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return brunoconvert.Input{}, "", fmt.Errorf("read Bruno upload: %w", err)
	}
	if int64(len(data)) > limit {
		return brunoconvert.Input{}, "", fmt.Errorf("Bruno upload exceeds %d bytes", limit)
	}
	filename := strings.TrimSpace(r.URL.Query().Get("filename"))
	if filename == "" {
		filename = filenameFromContentDisposition(r.Header.Get("Content-Disposition"))
	}
	return brunoconvert.Input{
		Name:      strings.TrimSpace(r.URL.Query().Get("name")),
		ProjectID: strings.TrimSpace(r.URL.Query().Get("project_id")),
		Filename:  filename,
		Content:   data,
		Sandbox:   strings.TrimSpace(r.URL.Query().Get("sandbox")),
		RootPath:  strings.TrimSpace(r.URL.Query().Get("root_path")),
		Limits:    limits,
	}, strings.TrimSpace(r.URL.Query().Get("sync_script_id")), nil
}

func readBrunoMultipartInput(r *http.Request, limits brunoconvert.Limits, maximum int64) (brunoconvert.Input, string, error) {
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
		return brunoconvert.Input{}, "", errors.New("invalid multipart form")
	}
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	fields := map[string]string{}
	files := map[string][]byte{}
	var singleFilename string
	var singleContent []byte
	directoryMode := false
	var total int64
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return brunoconvert.Input{}, "", fmt.Errorf("read multipart upload: %w", err)
		}
		formName := strings.TrimSpace(part.FormName())
		filename := part.FileName()
		if filename == "" && !strings.HasPrefix(formName, "file:") {
			value, err := io.ReadAll(io.LimitReader(part, (1<<20)+1))
			part.Close()
			if err != nil {
				return brunoconvert.Input{}, "", err
			}
			if len(value) > 1<<20 {
				return brunoconvert.Input{}, "", fmt.Errorf("Bruno multipart field %q is too large", formName)
			}
			fields[formName] = strings.TrimSpace(string(value))
			continue
		}
		remaining := maximum - total
		if remaining < 0 {
			part.Close()
			return brunoconvert.Input{}, "", fmt.Errorf("Bruno upload exceeds %d bytes", maximum)
		}
		data, err := io.ReadAll(io.LimitReader(part, remaining+1))
		part.Close()
		if err != nil {
			return brunoconvert.Input{}, "", err
		}
		total += int64(len(data))
		if total > maximum {
			return brunoconvert.Input{}, "", fmt.Errorf("Bruno upload exceeds %d bytes", maximum)
		}
		relative := strings.TrimSpace(part.Header.Get("X-APEXLoad-Relative-Path"))
		if strings.HasPrefix(formName, "file:") {
			decoded, decodeErr := url.QueryUnescape(strings.TrimPrefix(formName, "file:"))
			if decodeErr != nil {
				return brunoconvert.Input{}, "", fmt.Errorf("invalid encoded Bruno relative path: %w", decodeErr)
			}
			relative = decoded
			directoryMode = true
		}
		if relative != "" {
			directoryMode = true
			filename = relative
		}
		if filename == "" {
			filename = "request.yml"
		}
		if _, duplicate := files[filename]; duplicate {
			return brunoconvert.Input{}, "", fmt.Errorf("Bruno file path %q is duplicated", filename)
		}
		files[filename] = data
		singleFilename, singleContent = filename, data
	}
	var options brunoconvert.InspectOptions
	if raw := strings.TrimSpace(fields["options"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &options); err != nil {
			return brunoconvert.Input{}, "", fmt.Errorf("invalid Bruno options JSON: %w", err)
		}
	}
	input := brunoconvert.Input{
		Name:      fields["name"],
		ProjectID: firstNonEmpty(fields["project_id"], r.URL.Query().Get("project_id")),
		Sandbox:   fields["sandbox"],
		RootPath:  fields["root_path"],
		Options:   options,
		Limits:    limits,
	}
	if len(files) == 0 {
		return brunoconvert.Input{}, "", errors.New("at least one Bruno file is required")
	}
	if len(files) == 1 && !directoryMode {
		input.Filename = singleFilename
		input.Content = singleContent
	} else {
		input.Files = files
	}
	return input, firstNonEmpty(fields["sync_script_id"], r.URL.Query().Get("sync_script_id")), nil
}

func decodeBrunoJSONContent(content, encoded string) ([]byte, error) {
	if strings.TrimSpace(encoded) != "" {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, fmt.Errorf("invalid content_base64: %w", err)
		}
		return data, nil
	}
	if content == "" {
		return nil, errors.New("Bruno content or content_base64 is required")
	}
	return []byte(content), nil
}

func (s *Server) storeBrunoImportSession(session *brunoImportSession) {
	s.brunoImportMu.Lock()
	now := time.Now().UTC()
	var expired []*brunoImportSession
	for id, existing := range s.brunoImportSessions {
		if existing == nil || now.After(existing.ExpiresAt) {
			expired = append(expired, existing)
			delete(s.brunoImportSessions, id)
		}
	}
	s.brunoImportSessions[session.ID] = session
	delay := time.Until(session.ExpiresAt)
	if delay <= 0 {
		delay = time.Nanosecond
	}
	session.Timer = time.AfterFunc(delay, func() {
		s.expireBrunoImportSession(session.ID, session)
	})
	s.brunoImportMu.Unlock()
	for _, existing := range expired {
		s.releaseBrunoImportSession(existing, true, true)
	}
}

func (s *Server) consumeBrunoImportSession(r *http.Request, id, projectID string) (*brunoImportSession, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("import_session_id is required")
	}
	projectID = normalizeProjectID(projectID)
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return nil, err
	}
	_, actor, _ := s.resolveAuditActor(r)
	payload, _, consumeErr := objects.ConsumeSession(r.Context(), id, projectID, actor, brunoImportSessionMaxBytes)
	if consumeErr != nil {
		if errors.Is(consumeErr, os.ErrNotExist) {
			return nil, sql.ErrNoRows
		}
		return nil, consumeErr
	}
	s.brunoImportMu.Lock()
	existing := s.brunoImportSessions[id]
	delete(s.brunoImportSessions, id)
	s.brunoImportMu.Unlock()
	s.releaseBrunoImportSession(existing, false, false)

	session, decodeErr := decodePersistedBrunoImportSession(id, payload)
	clear(payload)
	if decodeErr != nil {
		if session != nil {
			s.releaseBrunoImportSession(session, false, true)
		}
		return nil, decodeErr
	}
	if normalizeProjectID(session.ProjectID) != projectID {
		s.releaseBrunoImportSession(session, false, true)
		return nil, errors.New("Bruno import session belongs to another project")
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		s.releaseBrunoImportSession(session, false, true)
		return nil, errors.New("Bruno import session expired")
	}
	if session.Actor != actor {
		s.releaseBrunoImportSession(session, false, true)
		return nil, errors.New("Bruno import session belongs to another actor")
	}
	return session, nil
}

func (s *Server) restoreClaimedBrunoImportSession(session *brunoImportSession) error {
	if session == nil {
		return errors.New("Bruno import session is required")
	}
	// Restoration must survive a client disconnect that occurs after the
	// one-shot claim. Keep it bounded so a failed backing store cannot pin the
	// handler indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.persistBrunoImportSession(ctx, session); err != nil {
		return err
	}
	clearBrunoImportSecretValuePayloads(session.Result)
	s.storeBrunoImportSession(session)
	return nil
}

func (s *Server) loadBrunoImportSession(r *http.Request, id, projectID string) (*brunoImportSession, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("import_session_id is required")
	}
	projectID = normalizeProjectID(projectID)
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return nil, err
	}
	_, actor, _ := s.resolveAuditActor(r)
	reader, _, err := objects.OpenSession(r.Context(), id, projectID, actor)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(reader, brunoImportSessionMaxBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		clear(payload)
		return nil, errors.Join(readErr, closeErr)
	}
	if int64(len(payload)) > brunoImportSessionMaxBytes {
		clear(payload)
		return nil, errors.New("Bruno import session exceeds its maximum size")
	}
	session, err := decodePersistedBrunoImportSession(id, payload)
	clear(payload)
	if err != nil {
		return nil, err
	}
	if normalizeProjectID(session.ProjectID) != projectID {
		clearBrunoImportSession(session)
		return nil, errors.New("Bruno import session belongs to another project")
	}
	if session.Actor != actor {
		clearBrunoImportSession(session)
		return nil, errors.New("Bruno import session belongs to another actor")
	}
	return session, nil
}

func (s *Server) deleteBrunoImportSession(id string) {
	s.brunoImportMu.Lock()
	session := s.brunoImportSessions[id]
	delete(s.brunoImportSessions, id)
	s.brunoImportMu.Unlock()
	s.releaseBrunoImportSession(session, true, true)
}

func (s *Server) releaseBrunoImportSession(session *brunoImportSession, deletePersisted, cleanupArtifacts bool) {
	if session == nil {
		return
	}
	if deletePersisted {
		s.deletePersistedBrunoImportSession(session)
	}
	if cleanupArtifacts {
		s.cleanupBrunoImportSessionArtifacts(session)
	}
	clearBrunoImportSession(session)
}

func (s *Server) clearProjectBrunoImportSessions(projectID string) error {
	if s == nil {
		return nil
	}
	projectID = normalizeProjectID(projectID)
	s.brunoImportMu.Lock()
	for id, session := range s.brunoImportSessions {
		if session == nil || normalizeProjectID(session.ProjectID) != projectID {
			continue
		}
		clearBrunoImportSession(session)
		delete(s.brunoImportSessions, id)
	}
	s.brunoImportMu.Unlock()
	present, err := s.brunoArtifactStorePresent()
	if err != nil || !present {
		return err
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return err
	}
	return objects.DeleteProject(context.Background(), projectID)
}

func (s *Server) brunoArtifactStorePresent() (bool, error) {
	if s == nil {
		return false, nil
	}
	if s.brunoArtifacts != nil || s.brunoArtifactWrapper != nil || strings.TrimSpace(s.brunoArtifactDir) != "" || s.desktopMode {
		return true, nil
	}
	if strings.TrimSpace(os.Getenv(keywrap.MasterKeyEnvVar)) != "" || strings.TrimSpace(os.Getenv(keywrap.KeyVaultKeyIDEnvVar)) != "" {
		return true, nil
	}
	defaultRoot := filepath.Join(s.importAssetDir, ".bruno-artifacts")
	if _, err := os.Stat(defaultRoot); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect Bruno artifact root: %w", err)
	}
	return true, nil
}

func (s *Server) pruneExpiredBrunoImportSessions(now time.Time) error {
	present, err := s.brunoArtifactStorePresent()
	if err != nil || !present {
		return err
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return err
	}
	expired, sweepErr := objects.DeleteExpiredSessions(context.Background(), now.UTC())
	checker, _ := s.store.(storage.BrunoArtifactReferenceChecker)
	var cleanupErrors []error
	if sweepErr != nil {
		cleanupErrors = append(cleanupErrors, sweepErr)
	}
	seenSessions := make(map[string]struct{}, len(expired))
	for _, descriptor := range expired {
		descriptorCleanupOK := true
		if _, duplicate := seenSessions[descriptor.ID]; !duplicate {
			seenSessions[descriptor.ID] = struct{}{}
			s.brunoImportMu.Lock()
			session := s.brunoImportSessions[descriptor.ID]
			delete(s.brunoImportSessions, descriptor.ID)
			s.brunoImportMu.Unlock()
			s.releaseBrunoImportSession(session, false, false)
		}
		for _, request := range descriptor.RelatedObjects {
			if strings.TrimSpace(request.ProjectID) != strings.TrimSpace(descriptor.ProjectID) ||
				(request.Kind != "raw" && request.Kind != "sanitized") {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("expired Bruno import session %q has invalid related artifact metadata", descriptor.ID))
				descriptorCleanupOK = false
				continue
			}
			if checker != nil {
				referenced, checkErr := checker.IsBrunoArtifactBackendKeyReferenced(request.BackendKey)
				if checkErr != nil {
					cleanupErrors = append(cleanupErrors, checkErr)
					descriptorCleanupOK = false
					continue
				}
				if referenced {
					continue
				}
			}
			sessionReferenced, sessionCheckErr := objects.IsSessionRelatedObjectReferenced(request.BackendKey, descriptor.ID)
			if sessionCheckErr != nil {
				cleanupErrors = append(cleanupErrors, sessionCheckErr)
				descriptorCleanupOK = false
				continue
			}
			if sessionReferenced {
				continue
			}
			if deleteErr := objects.Delete(context.Background(), request); deleteErr != nil {
				cleanupErrors = append(cleanupErrors, deleteErr)
				descriptorCleanupOK = false
			}
		}
		if descriptorCleanupOK {
			if deleteErr := objects.DeleteSession(context.Background(), descriptor.ID, descriptor.ProjectID); deleteErr != nil {
				cleanupErrors = append(cleanupErrors, deleteErr)
			}
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *Server) startBrunoImportSessionJanitor() {
	if s == nil || s.brunoSessionCleanupCancel != nil {
		return
	}
	if err := s.pruneExpiredBrunoImportSessions(time.Now().UTC()); err != nil {
		slog.Warn("failed to prune expired Bruno import sessions", "error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.brunoSessionCleanupCancel = cancel
	if !s.launchBackground(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := s.pruneExpiredBrunoImportSessions(now.UTC()); err != nil {
					slog.Warn("failed to prune expired Bruno import sessions", "error", err)
				}
			}
		}
	}) {
		cancel()
		s.brunoSessionCleanupCancel = nil
	}
}

func (s *Server) expireBrunoImportSession(id string, expected *brunoImportSession) {
	s.brunoImportMu.Lock()
	if session := s.brunoImportSessions[id]; session != expected {
		s.brunoImportMu.Unlock()
		return
	}
	if expected != nil {
		expected.Timer = nil
	}
	s.brunoImportMu.Unlock()
	expiresAt := time.Now().UTC()
	if expected != nil && expected.ExpiresAt.After(expiresAt) {
		expiresAt = expected.ExpiresAt
	}
	if err := s.pruneExpiredBrunoImportSessions(expiresAt); err != nil {
		slog.Warn("failed to expire Bruno import session", "session_id", id, "error", err)
	}
}

func (s *Server) persistBrunoImportSession(ctx context.Context, session *brunoImportSession) error {
	if session == nil || session.Result == nil {
		return errors.New("Bruno import session is required")
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return err
	}
	values, err := boundedBrunoImportSecretValues(session.Result.SecretRequirements)
	if err != nil {
		return err
	}
	defer clearStringMap(values)
	envelope := persistedBrunoImportSession{
		ID:                session.ID,
		ProjectID:         session.ProjectID,
		Actor:             session.Actor,
		Result:            session.Result,
		RawArtifact:       session.RawArtifact,
		SanitizedArtifact: session.SanitizedArtifact,
		RevisionID:        session.RevisionID,
		SyncScriptID:      session.SyncScriptID,
		CreatedAt:         session.CreatedAt,
		ExpiresAt:         session.ExpiresAt,
		SecretValues:      values,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	defer clear(data)
	return objects.SaveSessionBytes(ctx, brunoartifact.SessionDescriptor{
		ID:        session.ID,
		ProjectID: session.ProjectID,
		Actor:     session.Actor,
		ExpiresAt: session.ExpiresAt,
		RelatedObjects: []brunoartifact.OpenRequest{
			brunoArtifactOpenRequest(session.RawArtifact),
			brunoArtifactOpenRequest(session.SanitizedArtifact),
		},
	}, data, brunoImportSessionMaxBytes)
}

func brunoArtifactOpenRequest(artifact storage.BrunoSourceArtifact) brunoartifact.OpenRequest {
	return brunoartifact.OpenRequest{
		BackendKey: artifact.BackendKey,
		ProjectID:  artifact.ProjectID,
		Kind:       artifact.ArtifactKind,
		SHA256:     artifact.SHA256,
		SizeBytes:  artifact.SizeBytes,
	}
}

func boundedBrunoImportSecretValues(requirements []brunoconvert.SecretRequirement) (map[string]string, error) {
	values := make(map[string]string)
	total := 0
	for _, requirement := range requirements {
		if requirement.Value == "" {
			continue
		}
		id := strings.TrimSpace(requirement.ID)
		if id == "" || id != requirement.ID {
			return nil, errors.New("Bruno source credential requirement has an invalid id")
		}
		if _, duplicate := values[id]; duplicate {
			return nil, fmt.Errorf("Bruno source credential requirement %q is duplicated", id)
		}
		if len(values) >= projectSecretMaxBatchItems {
			return nil, fmt.Errorf("Bruno source credentials exceed %d-entry session limit", projectSecretMaxBatchItems)
		}
		if len(requirement.Value) > projectSecretMaxValueBytes {
			return nil, fmt.Errorf("Bruno source credential exceeds %d-byte limit", projectSecretMaxValueBytes)
		}
		if total > sourceSecretSessionMaxTotalBytes-len(requirement.Value) {
			return nil, fmt.Errorf("Bruno source credentials exceed %d-byte session limit", sourceSecretSessionMaxTotalBytes)
		}
		total += len(requirement.Value)
		values[id] = requirement.Value
	}
	return values, nil
}

func validatePersistedBrunoSecretValues(values map[string]string) error {
	if len(values) > projectSecretMaxBatchItems {
		return fmt.Errorf("persisted Bruno source credentials exceed %d-entry session limit", projectSecretMaxBatchItems)
	}
	total := 0
	for rawID, value := range values {
		id := strings.TrimSpace(rawID)
		if id == "" || id != rawID {
			return errors.New("persisted Bruno source credential has an invalid requirement id")
		}
		if len(value) > projectSecretMaxValueBytes {
			return fmt.Errorf("persisted Bruno source credential exceeds %d-byte limit", projectSecretMaxValueBytes)
		}
		if total > sourceSecretSessionMaxTotalBytes-len(value) {
			return fmt.Errorf("persisted Bruno source credentials exceed %d-byte session limit", sourceSecretSessionMaxTotalBytes)
		}
		total += len(value)
	}
	return nil
}

func decodePersistedBrunoImportSession(id string, payload []byte) (*brunoImportSession, error) {
	var envelope persistedBrunoImportSession
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	session := &brunoImportSession{
		ID:                id,
		ProjectID:         envelope.ProjectID,
		Actor:             envelope.Actor,
		Result:            envelope.Result,
		RawArtifact:       envelope.RawArtifact,
		SanitizedArtifact: envelope.SanitizedArtifact,
		RevisionID:        envelope.RevisionID,
		SyncScriptID:      envelope.SyncScriptID,
		CreatedAt:         envelope.CreatedAt,
		ExpiresAt:         envelope.ExpiresAt,
	}
	if envelope.Result == nil {
		return session, errors.New("persisted Bruno import session has no result")
	}
	if strings.TrimSpace(envelope.ID) != strings.TrimSpace(id) {
		return session, errors.New("persisted Bruno import session id does not match its descriptor")
	}
	if err := validatePersistedBrunoSecretValues(envelope.SecretValues); err != nil {
		return session, err
	}
	seen := make(map[string]struct{}, len(envelope.Result.SecretRequirements))
	for index := range envelope.Result.SecretRequirements {
		requirementID := envelope.Result.SecretRequirements[index].ID
		if _, duplicate := seen[requirementID]; duplicate {
			return session, fmt.Errorf("persisted Bruno source credential requirement %q is duplicated", requirementID)
		}
		seen[requirementID] = struct{}{}
		envelope.Result.SecretRequirements[index].Value = envelope.SecretValues[requirementID]
	}
	for requirementID := range envelope.SecretValues {
		if _, ok := seen[requirementID]; !ok {
			return session, fmt.Errorf("persisted Bruno source credential requirement %q is unknown", requirementID)
		}
	}
	return session, nil
}

func (s *Server) deletePersistedBrunoImportSession(session *brunoImportSession) {
	if session == nil || s.brunoArtifacts == nil {
		return
	}
	_ = s.brunoArtifacts.DeleteSession(context.Background(), session.ID, session.ProjectID)
}

func (s *Server) cleanupBrunoImportSessionArtifacts(session *brunoImportSession) {
	if session == nil || s.brunoArtifacts == nil {
		return
	}
	checker, _ := s.store.(storage.BrunoArtifactReferenceChecker)
	for _, artifact := range []storage.BrunoSourceArtifact{session.RawArtifact, session.SanitizedArtifact} {
		if strings.TrimSpace(artifact.BackendKey) == "" {
			continue
		}
		if checker != nil {
			referenced, err := checker.IsBrunoArtifactBackendKeyReferenced(artifact.BackendKey)
			if err != nil || referenced {
				continue
			}
		}
		referencedBySession, err := s.brunoArtifacts.IsSessionRelatedObjectReferenced(artifact.BackendKey, session.ID)
		if err != nil || referencedBySession {
			continue
		}
		_ = s.brunoArtifacts.Delete(context.Background(), brunoartifact.OpenRequest{
			BackendKey: artifact.BackendKey,
			ProjectID:  artifact.ProjectID,
			Kind:       artifact.ArtifactKind,
			SHA256:     artifact.SHA256,
			SizeBytes:  artifact.SizeBytes,
		})
	}
}

func (s *Server) cleanupUnreferencedBrunoArtifacts(artifacts []storage.BrunoSourceArtifact) {
	if len(artifacts) == 0 {
		return
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return
	}
	checker, ok := s.store.(storage.BrunoArtifactReferenceChecker)
	if !ok || checker == nil {
		// Content-addressed objects may already belong to a committed revision.
		// Leak-safe cleanup is preferable to deleting shared protected source
		// when the configured store cannot prove the object is unreferenced.
		return
	}
	for _, artifact := range artifacts {
		referenced, checkErr := checker.IsBrunoArtifactBackendKeyReferenced(artifact.BackendKey)
		if checkErr != nil || referenced {
			continue
		}
		_ = objects.Delete(context.Background(), brunoartifact.OpenRequest{
			BackendKey: artifact.BackendKey,
			ProjectID:  artifact.ProjectID,
			Kind:       artifact.ArtifactKind,
			SHA256:     artifact.SHA256,
			SizeBytes:  artifact.SizeBytes,
		})
	}
}

func clearBrunoImportSession(session *brunoImportSession) {
	if session == nil {
		return
	}
	if session.Timer != nil {
		session.Timer.Stop()
		session.Timer = nil
	}
	clearBrunoImportSecretValues(session.Result)
}

func clearBrunoImportSecretValues(result *brunoconvert.Result) {
	if result == nil {
		return
	}
	for index := range result.SecretRequirements {
		result.SecretRequirements[index].Value = ""
		result.SecretRequirements[index].SourceValueAvailable = false
	}
}

func clearBrunoImportSecretValuePayloads(result *brunoconvert.Result) {
	if result == nil {
		return
	}
	for index := range result.SecretRequirements {
		result.SecretRequirements[index].Value = ""
	}
}

func clearStringMap(values map[string]string) {
	for key := range values {
		values[key] = ""
		delete(values, key)
	}
}

func (s *Server) previewBrunoSynchronization(scriptID, projectID string, result *brunoconvert.Result) (*brunoSyncPreview, error) {
	existing, err := s.store.GetScript(strings.TrimSpace(scriptID))
	if err != nil {
		return nil, err
	}
	if normalizeProjectID(existing.ProjectID) != normalizeProjectID(projectID) {
		return nil, errors.New("Bruno synchronization target belongs to another project")
	}
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok {
		return nil, errors.New("Bruno revision storage is not configured")
	}
	active, err := brunoStore.GetActiveBrunoSourceRevision(existing.ID, normalizeProjectID(projectID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	preview := &brunoSyncPreview{
		TargetScriptID:    existing.ID,
		ExpectedUpdatedAt: existing.UpdatedAt,
		Changes:           summarizeBrunoIdentityChanges(nil, brunoCollectionIdentityMap(result.Collection)),
		Conflicts:         detectBrunoCanonicalConflicts(existing, active),
	}
	if active != nil {
		preview.ActiveRevision = active.ID
		preview.Changes = summarizeBrunoIdentityChanges(active.IdentityMap, brunoCollectionIdentityMap(result.Collection))
	}
	return preview, nil
}

func detectBrunoCanonicalConflicts(existing *dsl.Script, active *storage.BrunoSourceRevision) []brunoImportConflict {
	if existing == nil || active == nil || active.CanonicalScript == nil {
		return nil
	}
	var conflicts []brunoImportConflict
	if existing.Protocol != "bruno" {
		conflicts = append(conflicts, brunoImportConflict{
			ID: "protocol", Field: "protocol",
			Summary:    "The target script protocol was changed after its last Bruno import.",
			Resolution: "use_source",
		})
	}
	current, canonical := existing.Config.Bruno, active.CanonicalScript.Config.Bruno
	if current.BundleRef != canonical.BundleRef || current.BundleSHA256 != canonical.BundleSHA256 || current.SourceRevision != canonical.SourceRevision {
		conflicts = append(conflicts, brunoImportConflict{
			ID: "source_binding", Field: "config.bruno",
			Summary:    "The immutable Bruno source binding was edited locally.",
			Resolution: "use_source",
		})
	}
	if len(existing.Actions) == 0 || existing.Actions[0].Type != "bruno_run" {
		conflicts = append(conflicts, brunoImportConflict{
			ID: "canonical_action", Field: "actions[0].type",
			Summary:    "The canonical bruno_run action was removed or changed.",
			Resolution: "use_source",
		})
	}
	return conflicts
}

func unresolvedBrunoConflicts(conflicts []brunoImportConflict, resolutions map[string]string) []brunoImportConflict {
	var unresolved []brunoImportConflict
	for _, conflict := range conflicts {
		if strings.EqualFold(strings.TrimSpace(resolutions[conflict.ID]), conflict.Resolution) {
			continue
		}
		unresolved = append(unresolved, conflict)
	}
	return unresolved
}

func mergeBrunoSynchronization(existing, imported *dsl.Script) *dsl.Script {
	if existing == nil {
		return imported
	}
	merged := cloneScriptDefinition(imported)
	if merged == nil {
		return nil
	}
	sourceBruno := merged.Config.Bruno
	merged.ID = existing.ID
	merged.RefKey = existing.RefKey
	merged.Name = existing.Name
	merged.ProjectID = existing.ProjectID
	merged.FolderID = cloneStringPtr(existing.FolderID)
	merged.CreatedAt = existing.CreatedAt
	merged.GoalConfig = existing.GoalConfig
	merged.Config = existing.Config
	merged.Config.Bruno = sourceBruno
	merged.Config.Bruno.SecretBindings = existing.Config.Bruno.SecretBindings
	if len(existing.Actions) > 0 && existing.Actions[0].Type == "bruno_run" && len(merged.Actions) > 0 {
		merged.Actions[0].Name = existing.Actions[0].Name
	}
	if existing.Metadata != nil && existing.Metadata.RuntimePolicy != nil {
		if merged.Metadata == nil {
			merged.Metadata = &dsl.ScriptMetadata{}
		}
		policy := *existing.Metadata.RuntimePolicy
		policy.AllowedTargetHosts = append([]string(nil), existing.Metadata.RuntimePolicy.AllowedTargetHosts...)
		merged.Metadata.RuntimePolicy = &policy
	}
	return merged
}

func (s *Server) bindOrMigrateBrunoImportSecrets(
	r *http.Request,
	requirements []brunoconvert.SecretRequirement,
	requested map[string]string,
	existing *dsl.Script,
	projectID string,
	actor string,
	migrate bool,
) (map[string]string, []brunoconvert.SecretRequirement, []secretstore.Metadata, []brunoconvert.SecretRequirement, error) {
	existingLogical := map[string]string{}
	if existing != nil {
		existingLogical = logicalBrunoSecretBindings(existing.Config.Bruno.SecretBindings)
	}
	groupedRequested, err := groupedBrunoRequestedBindings(requirements, requested)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	store := s.projectSecretStore
	canMigrate := migrate && secretstore.SupportsCreate(store) && s.sourceSecretMigrationAuthorized(r, projectID)
	bindings := make(map[string]string, len(requirements))
	var missing []brunoconvert.SecretRequirement
	var conflicts []brunoconvert.SecretRequirement
	type migrationPlan struct {
		requirement brunoconvert.SecretRequirement
		name        string
		value       string
	}
	plansByName := make(map[string]migrationPlan)
	probeCtx, cancelProbe := engine.WithSecretResolutionBudget(r.Context())
	defer cancelProbe()
	for _, requirement := range requirements {
		requestedName := groupedRequested[requirement.SuggestedName]
		name := requestedName
		explicitExistingBinding := false
		requestedExplicitly := name != ""
		if name == "" {
			name = strings.TrimSpace(existingLogical[requirement.SuggestedName])
			if name != "" {
				requestedExplicitly = true
			}
		}
		if requestedExplicitly {
			if store != nil {
				if _, _, getErr := store.Get(probeCtx, projectID, name); getErr == nil {
					explicitExistingBinding = true
				} else if !errors.Is(getErr, secretstore.ErrNotFound) {
					return nil, nil, nil, nil, getErr
				}
			}
			if engine.ProjectScriptSecretEnvAvailable(projectID, name) {
				explicitExistingBinding = true
			}
		}
		if name == "" && store != nil {
			if _, _, getErr := store.Get(probeCtx, projectID, requirement.SuggestedName); getErr == nil {
				name = requirement.SuggestedName
				explicitExistingBinding = true
			} else if !errors.Is(getErr, secretstore.ErrNotFound) {
				return nil, nil, nil, nil, getErr
			}
		}
		if name == "" && engine.ProjectScriptSecretEnvAvailable(projectID, requirement.SuggestedName) {
			name = requirement.SuggestedName
			explicitExistingBinding = true
		}
		if name == "" {
			name = requirement.SuggestedName
		}
		var normalizeErr error
		name, normalizeErr = engine.NormalizeLogicalSecretName(name)
		if normalizeErr != nil || !logicalSecretNamePattern.MatchString(name) {
			return nil, nil, nil, nil, fmt.Errorf("secret binding for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", requirement.ID)
		}
		// An explicitly selected name that already exists is an instruction to
		// bind to that governed value, not to overwrite or re-migrate it. This is
		// also what makes a partially completed create-only migration retryable.
		// Automatically selected existing names still conflict because the user
		// has not approved binding this source requirement to their contents.
		explicitExistingBindingApproved := requestedExplicitly && explicitExistingBinding
		if canMigrate && requirement.Value != "" && !explicitExistingBindingApproved {
			existingValue, _, getErr := store.Get(probeCtx, projectID, name)
			switch {
			case getErr == nil:
				// An explicit equal-valued target makes migration retries
				// idempotent after a provider or script-commit failure. Automatic
				// name adoption remains a conflict, matching the shared migration
				// API and preventing an unnoticed source-value discard.
				if requestedExplicitly && existingValue == requirement.Value {
					break
				}
				if auditErr := s.persistProjectSecretOutcome(r, "script_import.secret.migrate", "blocked", "already_exists", name, projectID, secretStoreProvider(store), "already_exists", nil); auditErr != nil {
					return nil, nil, nil, nil, fmt.Errorf("persist source credential migration audit: %w", auditErr)
				}
				conflicts = append(conflicts, publicBrunoSecretRequirement(requirement))
				continue
			case errors.Is(getErr, secretstore.ErrNotFound):
				if planned, exists := plansByName[name]; exists && planned.value != requirement.Value {
					if auditErr := s.persistProjectSecretOutcome(r, "script_import.secret.migrate", "blocked", "conflicting_source_values", name, projectID, secretStoreProvider(store), "conflicting_source_values", nil); auditErr != nil {
						return nil, nil, nil, nil, fmt.Errorf("persist source credential migration audit: %w", auditErr)
					}
					conflicts = append(conflicts,
						publicBrunoSecretRequirement(planned.requirement),
						publicBrunoSecretRequirement(requirement),
					)
					continue
				}
				plansByName[name] = migrationPlan{requirement: requirement, name: name, value: requirement.Value}
			default:
				if auditErr := s.persistProjectSecretOutcome(r, "script_import.secret.migrate", "failed", "provider_probe_failed", name, projectID, secretStoreProvider(store), projectSecretErrorReason(getErr), nil); auditErr != nil {
					return nil, nil, nil, nil, fmt.Errorf("persist source credential migration audit: %w", auditErr)
				}
				return nil, nil, nil, nil, getErr
			}
		}
		bindings[requirement.SuggestedName] = "{{secret." + name + "}}"
	}
	if len(missing) > 0 || len(conflicts) > 0 {
		return bindings, missing, nil, conflicts, nil
	}
	planNames := make([]string, 0, len(plansByName))
	for name := range plansByName {
		planNames = append(planNames, name)
	}
	sort.Strings(planNames)
	migrations := make([]secretstore.Metadata, 0, len(planNames))
	for index, name := range planNames {
		plan := plansByName[name]
		operationID, auditErr := s.beginProjectSecretOperation(r, "script_import.secret.migrate", plan.name, projectID, secretStoreProvider(store))
		if auditErr != nil {
			return bindings, nil, migrations, nil, &brunoSecretMigrationPartialError{
				Cause:          fmt.Errorf("source credential migration audit is unavailable: %w", auditErr),
				Migrated:       append([]secretstore.Metadata(nil), migrations...),
				FailedName:     plan.name,
				RemainingNames: append([]string(nil), planNames[index:]...),
			}
		}
		metadata, outcome, putErr := createProjectSecretWithReconciliation(r.Context(), store, projectID, plan.name, plan.value, actor)
		if putErr != nil {
			status := "failed"
			if errors.Is(putErr, secretstore.ErrAlreadyExists) {
				status = "blocked"
			}
			details := map[string]any{}
			if outcome == "provider_error_commit_ambiguous" {
				details["commit_state"] = "ambiguous"
			}
			if auditErr := s.completeProjectSecretOperation(r, operationID, "script_import.secret.migrate", status, outcome, plan.name, projectID, secretStoreProvider(store), projectSecretErrorReason(putErr), details); auditErr != nil {
				putErr = errors.Join(putErr, fmt.Errorf("persist source credential migration audit outcome: %w", auditErr))
			}
			// Store.Create deliberately has no delete companion: ownership-safe
			// compensation cannot be proven after an external provider call. Keep
			// every successful create and return only value-free metadata/names so
			// the caller can configure the remaining values without risking an
			// overwrite of the committed subset.
			return bindings, nil, migrations, nil, &brunoSecretMigrationPartialError{
				Cause:          putErr,
				Migrated:       append([]secretstore.Metadata(nil), migrations...),
				FailedName:     plan.name,
				RemainingNames: append([]string(nil), planNames[index:]...),
			}
		}
		migrations = append(migrations, metadata)
		if auditErr := s.completeProjectSecretOperation(r, operationID, "script_import.secret.migrate", "success", outcome, plan.name, projectID, firstNonEmpty(strings.TrimSpace(metadata.Provider), secretStoreProvider(store)), "", nil); auditErr != nil {
			return bindings, nil, migrations, nil, &brunoSecretMigrationPartialError{
				Cause:          fmt.Errorf("source credential was migrated but its audit outcome could not be persisted: %w", auditErr),
				Migrated:       append([]secretstore.Metadata(nil), migrations...),
				RemainingNames: append([]string(nil), planNames[index+1:]...),
			}
		}
	}
	return bindings, missing, migrations, conflicts, nil
}

func publicBrunoSecretRequirement(requirement brunoconvert.SecretRequirement) brunoconvert.SecretRequirement {
	requirement.Value = ""
	requirement.SourceValueAvailable = false
	return requirement
}

func secretStoreProvider(store secretstore.Store) string {
	if store == nil || strings.TrimSpace(store.Provider()) == "" {
		return "binding_only"
	}
	return store.Provider()
}

func bindBrunoImportSecrets(requirements []brunoconvert.SecretRequirement, requested map[string]string, existing *dsl.Script, projectID string) (map[string]string, []brunoconvert.SecretRequirement, error) {
	existingLogical := map[string]string{}
	if existing != nil {
		existingLogical = logicalBrunoSecretBindings(existing.Config.Bruno.SecretBindings)
	}
	groupedRequested, err := groupedBrunoRequestedBindings(requirements, requested)
	if err != nil {
		return nil, nil, err
	}
	bindings := make(map[string]string, len(requirements))
	var missing []brunoconvert.SecretRequirement
	for _, requirement := range requirements {
		name := groupedRequested[requirement.SuggestedName]
		if name == "" {
			name = strings.TrimSpace(existingLogical[requirement.SuggestedName])
		}
		if name == "" && engine.ProjectScriptSecretEnvAvailable(projectID, requirement.SuggestedName) {
			name = requirement.SuggestedName
		}
		if name == "" && requirement.ExistingReference {
			name = requirement.SuggestedName
		}
		if name == "" {
			missing = append(missing, requirement)
			continue
		}
		var normalizeErr error
		name, normalizeErr = engine.NormalizeLogicalSecretName(name)
		if normalizeErr != nil || !logicalSecretNamePattern.MatchString(name) {
			return nil, nil, fmt.Errorf("secret binding for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", requirement.ID)
		}
		bindings[requirement.SuggestedName] = "{{secret." + name + "}}"
	}
	return bindings, missing, nil
}

func groupedBrunoRequestedBindings(requirements []brunoconvert.SecretRequirement, requested map[string]string) (map[string]string, error) {
	grouped := make(map[string]string)
	for _, requirement := range requirements {
		raw := strings.TrimSpace(requested[requirement.ID])
		if raw == "" {
			continue
		}
		name, err := engine.NormalizeLogicalSecretName(raw)
		if err != nil || !logicalSecretNamePattern.MatchString(name) {
			return nil, fmt.Errorf("secret binding for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", requirement.ID)
		}
		if prior := grouped[requirement.SuggestedName]; prior != "" && prior != name {
			return nil, fmt.Errorf("Bruno source binding %q cannot target multiple project secrets", requirement.SuggestedName)
		}
		grouped[requirement.SuggestedName] = name
	}
	return grouped, nil
}

func logicalBrunoSecretBindings(bindings map[string]string) map[string]string {
	logical := make(map[string]string, len(bindings))
	for key, value := range bindings {
		value = strings.TrimSpace(value)
		if match := scriptSecretTemplatePattern.FindStringSubmatch(value); len(match) >= 2 && match[0] == value {
			value = match[1]
		}
		logical[key] = value
	}
	return logical
}

func brunoCollectionIdentityMap(collection brunoconvert.Collection) map[string]string {
	identity := map[string]string{}
	var visit func([]brunoconvert.CollectionItem)
	visit = func(items []brunoconvert.CollectionItem) {
		for _, item := range items {
			if strings.TrimSpace(item.Path) != "" {
				identity[item.Path] = item.UID
			}
			visit(item.Children)
		}
	}
	visit(collection.Items)
	return identity
}

func summarizeBrunoIdentityChanges(previous, current map[string]string) map[string]any {
	if previous == nil {
		previous = map[string]string{}
	}
	if current == nil {
		current = map[string]string{}
	}
	added, changed, deleted := []string{}, []string{}, []string{}
	moved := []map[string]string{}
	renamed := []map[string]string{}
	previousByUID := map[string][]string{}
	currentByUID := map[string]int{}
	for path, uid := range previous {
		if uid != "" {
			previousByUID[uid] = append(previousByUID[uid], path)
		}
	}
	for _, uid := range current {
		if uid != "" {
			currentByUID[uid]++
		}
	}
	movedFrom := map[string]bool{}
	for path, uid := range current {
		oldUID, existed := previous[path]
		switch {
		case existed && oldUID != uid:
			changed = append(changed, path)
		case !existed && uid != "" && len(previousByUID[uid]) == 1 && currentByUID[uid] == 1:
			from := previousByUID[uid][0]
			change := map[string]string{"from": from, "to": path, "uid": uid}
			if pathpkg.Dir(from) != pathpkg.Dir(path) {
				moved = append(moved, change)
			}
			if pathpkg.Base(from) != pathpkg.Base(path) {
				renamed = append(renamed, change)
			}
			movedFrom[from] = true
		case !existed:
			added = append(added, path)
		}
	}
	for path := range previous {
		if _, exists := current[path]; !exists && !movedFrom[path] {
			deleted = append(deleted, path)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(deleted)
	sort.Slice(moved, func(i, j int) bool { return moved[i]["from"] < moved[j]["from"] })
	sort.Slice(renamed, func(i, j int) bool { return renamed[i]["from"] < renamed[j]["from"] })
	return map[string]any{
		"added":   added,
		"changed": changed,
		"moved":   moved,
		"renamed": renamed,
		"deleted": deleted,
		"counts": map[string]int{
			"added": len(added), "changed": len(changed), "moved": len(moved), "renamed": len(renamed), "deleted": len(deleted),
		},
	}
}

func brunoImportOptionsFromScript(script *dsl.Script) map[string]any {
	options := map[string]any{}
	if script == nil {
		return options
	}
	bruno := script.Config.Bruno
	options["default_environment"] = bruno.DefaultEnvironment
	options["default_global_environment"] = bruno.DefaultGlobalEnvironment
	options["sandbox"] = bruno.Sandbox
	options["runner_profile"] = bruno.RunnerProfile
	options["persist_session_across_iterations"] = bruno.PersistSessionAcrossIterations
	options["limits"] = bruno.Limits
	if len(script.Actions) > 0 {
		options["selection"] = script.Actions[0].Params
	}
	return options
}

func (s *Server) allocateScriptRefKey() (string, error) {
	s.runner.mu.Lock()
	defer s.runner.mu.Unlock()
	sequence, err := s.runner.nextScriptSeqLocked()
	if err != nil {
		return "", err
	}
	return refkey.Script(s.runner.envCode, sequence, 1), nil
}

func (s *Server) canReadRawBrunoSource(r *http.Request, projectID string) bool {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	if isAuthBypassMode(settings.Mode) {
		return true
	}
	user, ok := AuthenticatedUserFromContext(r.Context())
	if !ok || user == nil {
		return false
	}
	if user.hasGlobalRole(RoleProjectAdmin, RoleSystemAdmin) || user.hasProjectRole(projectID, RoleProjectAdmin) {
		return true
	}
	if s.store == nil {
		return false
	}
	project, err := s.store.GetProject(projectID)
	if err != nil || project == nil {
		return false
	}
	return user.hasDomainRole(project.DomainID, RoleProjectAdmin) ||
		user.hasRepositoryRole(repositoryIDForProject(s.store, project), RoleProjectAdmin)
}

func publicBrunoRevision(revision *storage.BrunoSourceRevision) *storage.BrunoSourceRevision {
	if revision == nil {
		return nil
	}
	payload, _ := json.Marshal(revision)
	var clone storage.BrunoSourceRevision
	_ = json.Unmarshal(payload, &clone)
	clone.CanonicalScript = nil
	clone.RawArtifactID = ""
	return &clone
}

func brunoSanitizedArtifactPublicSummary(artifact storage.BrunoSourceArtifact) brunoArtifactSummary {
	return brunoArtifactSummary{
		ID:             artifact.ID,
		ArtifactKind:   "sanitized",
		SHA256:         artifact.SHA256,
		SizeBytes:      artifact.SizeBytes,
		ManifestSHA256: artifact.ManifestSHA256,
	}
}

func configuredBrunoImportBytes(name string, fallback, maximum int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func configuredBrunoImportInt(name string, fallback, maximum int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func brunoFeatureEnabled(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return fallback
	}
}

func filenameFromContentDisposition(value string) string {
	_, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(parameters["filename"]))
}

func importErrorStatus(err error) int {
	if err == nil {
		return http.StatusBadRequest
	}
	if strings.Contains(strings.ToLower(err.Error()), "exceeds") {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func slogWarnBrunoCacheRefresh(err error) {
	if err != nil {
		// Keep the committed durable script authoritative. A subsequent HA-style
		// durable read refreshes the local cache even if this best-effort refresh
		// was interrupted.
		slog.Warn("Bruno script cache refresh failed", "error", err)
	}
}
