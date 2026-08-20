package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
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
	"sync"
	"time"

	"github.com/google/uuid"
	brunoconvert "github.com/jasonczech/apexload/pkg/convert/bruno"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"github.com/jasonczech/apexload/pkg/keywrap"
	brunoprotocol "github.com/jasonczech/apexload/pkg/protocol/bruno"
	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	brunoImportSessionTTL         = 30 * time.Minute
	brunoArtifactCollectionGrace  = 5 * time.Minute
	brunoImportSessionLimit       = 128
	brunoImportSessionMaxBytes    = 64 << 20
	defaultBrunoUploadBytes       = 64 << 20
	defaultBrunoExpandedBytes     = 32 << 20
	defaultBrunoMaterializedBytes = 1 << 30
	defaultBrunoImportFileCount   = 100_000
)

var errBrunoImportSessionExpiredDuringCommit = errors.New("Bruno import session expired before it could be restored")

type brunoImportSession struct {
	ID                      string
	ProjectID               string
	Actor                   string
	Result                  *brunoconvert.Result
	RawArtifact             storage.BrunoSourceArtifact
	SanitizedArtifact       storage.BrunoSourceArtifact
	RevisionID              string
	SyncScriptID            string
	NativeCredentialTargets map[string][]brunoconvert.NativeCredentialTarget
	CreatedAt               time.Time
	ExpiresAt               time.Time
	Timer                   *time.Timer
}

type persistedBrunoImportSession struct {
	ID                      string                                           `json:"id"`
	ProjectID               string                                           `json:"project_id"`
	Actor                   string                                           `json:"actor"`
	Result                  *brunoconvert.Result                             `json:"result"`
	RawArtifact             storage.BrunoSourceArtifact                      `json:"raw_artifact"`
	SanitizedArtifact       storage.BrunoSourceArtifact                      `json:"sanitized_artifact"`
	RevisionID              string                                           `json:"revision_id"`
	SyncScriptID            string                                           `json:"sync_script_id,omitempty"`
	NativeCredentialTargets map[string][]brunoconvert.NativeCredentialTarget `json:"native_credential_targets,omitempty"`
	CreatedAt               time.Time                                        `json:"created_at"`
	ExpiresAt               time.Time                                        `json:"expires_at"`
	SecretValues            map[string]string                                `json:"secret_values,omitempty"`
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
	// SharedValues is populated for native imports only. Canonical mode hands
	// the collection to the Bruno runner, which resolves variables from the
	// selected environment inside the sealed bundle; offering a competing
	// override there would fight the runner rather than help the operator.
	SharedValues *sharedvalue.Result `json:"shared_values,omitempty"`
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
	// SharedValues maps a shared-value candidate ID to the operator's value.
	// Bruno artifacts never leave the server, so the values are applied here at
	// commit rather than through the generic shared-values apply endpoint.
	SharedValues           map[string]string `json:"shared_values,omitempty"`
	ConflictResolutions    map[string]string `json:"conflict_resolutions,omitempty"`
	CandidateIDs           []string          `json:"candidate_ids,omitempty"`
	NameOverrides          map[string]string `json:"name_overrides,omitempty"`
	AcknowledgedFindingIDs []string          `json:"acknowledged_finding_ids,omitempty"`
	candidateIDsOmitted    bool
	nameOverridesOmitted   bool
}

type brunoSecretMigrationPartialError struct {
	Cause          error
	Migrated       []secretstore.Metadata
	FailedName     string
	RemainingNames []string
}

type brunoSecretMigrationWindow struct {
	StartedAt time.Time
	ExpiresAt time.Time
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
	custody := s.brunoSourceRetentionKeyCustody()
	retention := map[string]any{
		"raw_source":        "encrypted and administrator-restricted",
		"execution_source":  "sanitized encrypted bundle",
		"content_addressed": true,
		"available":         custody != brunoKeyCustodyNone,
		"key_custody":       custody,
	}
	if custody != brunoKeyCustodyNone {
		if _, err := s.brunoArtifactStore(); err != nil {
			retention["available"] = false
			if errors.Is(err, importartifact.ErrUnsupportedFilesystem) {
				retention["unavailable_code"] = "protected_import_store_unsupported_filesystem"
				retention["unavailable_reason"] = "Protected imports on Windows require a local fixed NTFS or ReFS artifact directory."
			} else {
				retention["unavailable_code"] = "protected_import_store_unavailable"
				retention["unavailable_reason"] = "Protected import storage is unavailable; verify the controller artifact directory."
			}
		}
	}
	switch custody {
	case brunoKeyCustodyNone:
		retention["unavailable_reason"] = brunoSourceRetentionUnavailableMessage
	case brunoKeyCustodyLocal:
		retention["advisory"] = brunoLocalKeyAdvisory
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version":                brunoconvert.ImporterVersion,
		"compatibility_version":         brunoconvert.CompatibilityVersion,
		"import_enabled":                brunoFeatureEnabled("APEXLOAD_BRUNO_IMPORT_ENABLED", true),
		"safe_execution_enabled":        brunoFeatureEnabled("APEXLOAD_BRUNO_SAFE_EXECUTION_ENABLED", true),
		"developer_execution_enabled":   brunoFeatureEnabled("APEXLOAD_BRUNO_DEVELOPER_EXECUTION_ENABLED", false),
		"native_projection_enabled":     brunoFeatureEnabled("APEXLOAD_BRUNO_NATIVE_PROJECTION_ENABLED", false),
		"native_request_import_enabled": true,
		"source_formats":                []string{"classic .bru", "OpenCollection YAML", "standalone .bru/YAML", "ZIP", "TAR", "TAR.GZ", "directory upload"},
		"request_types":                 []string{"HTTP", "SOAP-over-HTTP", "GraphQL", "gRPC unary/client-stream/server-stream/bidirectional-stream", "WebSocket"},
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
			"OpenCollection YAML HTTP requests can import as independent native APEX scripts",
			"gRPC requests execute in collection runs",
			"WebSocket requests execute in collection runs",
			"bru.runRequest supports HTTP, GraphQL, gRPC, and WebSocket",
		},
		"retention": retention,
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
	// Refuse before the upload is parsed rather than after the operator has
	// filled in the whole import form. Inspection publishes encrypted source
	// artifacts, so with no envelope key it can only fail — and writeError
	// masks that failure into an opaque "internal server error", leaving the
	// import UI enabled with no way forward.
	if !s.brunoSourceRetentionAvailable() {
		writeBrunoSourceRetentionUnavailable(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, brunoImportMaxBodyBytes)
	input, syncScriptID, err := readBrunoInspectInput(r)
	if err != nil {
		writeError(w, importErrorStatus(err), err.Error())
		return
	}
	// Decode and bound the upload before occupying the scarce inspection slot.
	// Slow clients must not prevent an already-spooled request from progressing.
	select {
	case brunoInspectConcurrency <- struct{}{}:
		defer func() { <-brunoInspectConcurrency }()
	default:
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusTooManyRequests, "Bruno import inspection capacity is currently exhausted")
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
	// Availability for optional project-secret migration is intentionally
	// narrower than availability for exact destination replay. Preserve the
	// latter as a disclosure-safe boolean before the protected payload is
	// cleared from the in-memory lifecycle copy.
	retainedSecretValues := make(map[string]bool, len(result.SecretRequirements))
	normalizeBrunoImportResultCredentialValues(result)
	for index := range result.SecretRequirements {
		requirement := &result.SecretRequirements[index]
		if brunoImportCredentialValueUsable(requirement.Value) {
			retainedSecretValues[requirement.ID] = true
		}
		setBrunoDestinationPreservationEvidence(requirement, requirement.Value != "", true)
	}
	nativeMode := strings.EqualFold(strings.TrimSpace(input.Options.OutputMode), brunoconvert.OutputModeNativeRequests)
	if nativeMode && strings.TrimSpace(syncScriptID) != "" {
		writeError(w, http.StatusBadRequest, "native Bruno imports are create-only and cannot be synchronized")
		return
	}
	migrationAuthorized := secretstore.SupportsCreate(s.projectSecretStore) &&
		s.sourceSecretMigrationAuthorized(r, input.ProjectID)
	migrationValuesValid := validateBrunoMigrationSecretValues(
		result.SecretRequirements,
	) == nil
	for index := range result.SecretRequirements {
		// Source values are retained in the protected one-shot session so the
		// default commit can restore an exact runnable destination. Authorization
		// to externalize those values into the project secret store is checked
		// separately when (and only when) migration is explicitly requested.
		result.SecretRequirements[index].SourceValueAvailable =
			migrationAuthorized && migrationValuesValid &&
				brunoImportCredentialValueUsable(result.SecretRequirements[index].Value)
	}
	secretAvailability := make(map[string]bool, len(result.SecretRequirements))
	for _, requirement := range result.SecretRequirements {
		secretAvailability[requirement.ID] = requirement.SourceValueAvailable
	}
	nativeRequirementSeen := make(map[string]bool, len(result.SecretRequirements))
	nativeRequirementPreserved := make(map[string]bool, len(result.SecretRequirements))
	for candidateIndex := range result.Scripts {
		targetedRequirementIDs := make(map[string]bool, len(result.Scripts[candidateIndex].CredentialTargets))
		for _, target := range result.Scripts[candidateIndex].CredentialTargets {
			targetedRequirementIDs[target.RequirementID] = true
		}
		for requirementIndex := range result.Scripts[candidateIndex].SecretRequirements {
			requirement := &result.Scripts[candidateIndex].SecretRequirements[requirementIndex]
			requirement.SourceValueAvailable = secretAvailability[requirement.ID]
			setBrunoDestinationPreservationEvidence(
				requirement,
				retainedSecretValues[requirement.ID],
				targetedRequirementIDs[requirement.ID],
			)
			if !nativeRequirementSeen[requirement.ID] {
				nativeRequirementPreserved[requirement.ID] = requirement.DestinationValuePreserved
			} else {
				nativeRequirementPreserved[requirement.ID] =
					nativeRequirementPreserved[requirement.ID] && requirement.DestinationValuePreserved
			}
			nativeRequirementSeen[requirement.ID] = true
			requirement.Value = ""
		}
	}
	if nativeMode {
		for index := range result.SecretRequirements {
			requirement := &result.SecretRequirements[index]
			setBrunoDestinationPreservationEvidence(
				requirement,
				retainedSecretValues[requirement.ID],
				nativeRequirementSeen[requirement.ID] && nativeRequirementPreserved[requirement.ID],
			)
		}
	}
	boundedValues, err := boundedBrunoImportSecretValues(result.SecretRequirements)
	clearStringMap(boundedValues)
	if err != nil {
		// Dropping source values would turn an otherwise runnable import into a
		// script that asks the operator to re-enter credentials they already
		// supplied. Reject before publishing artifacts when the protected
		// session cannot retain the complete bounded set.
		clearBrunoImportSecretValues(result)
		writeError(w, http.StatusUnprocessableEntity, "Bruno source credentials exceed the protected import-session limit")
		return
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		if !s.brunoSourceRetentionAvailable() {
			writeBrunoSourceRetentionUnavailable(w)
			return
		}
		writeProtectedImportStoreError(w, err)
		return
	}
	unlockSourcePublication, err := objects.LockSourcePublication()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "reserve protected Bruno source publication: "+err.Error())
		return
	}
	sourcePublicationLocked := true
	sourcePublished := false
	publicationRecorded := false
	sourceArtifacts := make([]storage.BrunoSourceArtifact, 0, 2)
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	revisionID := uuid.NewString()
	releaseSourcePublication := func() {
		if sourcePublicationLocked {
			_ = unlockSourcePublication()
			sourcePublicationLocked = false
		}
	}
	defer func() {
		if !sourcePublished {
			cleanupErr := s.cleanupUnreferencedBrunoArtifactsLocked(objects, sourceArtifacts, "")
			if cleanupErr != nil {
				slog.Warn("clean interrupted Bruno source publication", "publication_id", sessionID, "error", cleanupErr)
			} else if publicationRecorded {
				if deleteErr := objects.DeletePublication(sessionID, input.ProjectID); deleteErr != nil {
					slog.Warn("remove cleaned Bruno source publication", "publication_id", sessionID, "error", deleteErr)
				}
			}
		}
		releaseSourcePublication()
	}()
	rawSource := result.OriginalUpload
	if len(rawSource) == 0 {
		rawSource = result.RawArchive
	}
	if len(rawSource) == 0 || len(result.SanitizedArchive) == 0 {
		writeError(w, http.StatusInternalServerError, "Bruno importer produced an empty source artifact")
		return
	}
	rawPlan, err := objects.PlanBytes(input.ProjectID, "raw", rawSource, int64(len(rawSource)))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "plan raw Bruno source: "+err.Error())
		return
	}
	sanitizedPlan, err := objects.PlanBytes(input.ProjectID, "sanitized", result.SanitizedArchive, int64(len(result.SanitizedArchive)))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "plan sanitized Bruno source: "+err.Error())
		return
	}
	if err := objects.SavePublication(importartifact.PublicationDescriptor{
		ID:        sessionID,
		ProjectID: input.ProjectID,
		ExpiresAt: now,
		Objects:   []importartifact.OpenRequest{rawPlan, sanitizedPlan},
	}); err != nil {
		writeError(w, http.StatusServiceUnavailable, "journal protected Bruno source publication: "+err.Error())
		return
	}
	publicationRecorded = true
	sourceArtifacts = append(sourceArtifacts,
		storage.BrunoSourceArtifact{
			BackendKey: rawPlan.BackendKey, ProjectID: input.ProjectID,
			ArtifactKind: "raw", SHA256: rawPlan.SHA256, SizeBytes: rawPlan.SizeBytes,
		},
		storage.BrunoSourceArtifact{
			BackendKey: sanitizedPlan.BackendKey, ProjectID: input.ProjectID,
			ArtifactKind: "sanitized", SHA256: sanitizedPlan.SHA256, SizeBytes: sanitizedPlan.SizeBytes,
		},
	)
	rawObject, err := objects.PutBytes(r.Context(), input.ProjectID, "raw", rawSource, int64(len(rawSource)))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "retain raw Bruno source: "+err.Error())
		return
	}
	sanitizedObject, err := objects.PutBytes(r.Context(), input.ProjectID, "sanitized", result.SanitizedArchive, int64(len(result.SanitizedArchive)))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "retain sanitized Bruno source: "+err.Error())
		return
	}
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
	var parameterization *paramize.Result
	var sharedValues *sharedvalue.Result
	responseResult := result
	if nativeMode {
		for index := range result.Scripts {
			if result.Scripts[index].Script == nil {
				continue
			}
			result.Scripts[index].Script.ProjectID = input.ProjectID
			result.Scripts[index].Script.ID = result.Scripts[index].PlannedScriptID
		}
		responseResult, err = publicBrunoNativeInspectResult(result)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Derived from the masked projection so the candidate IDs the browser
		// receives are the ones the commit path re-derives. Candidate IDs are
		// structural (name, path, field) and never carry a value, so masking a
		// credential cannot shift them.
		sharedValues = buildImportSharedValues(brunoNativeCandidateScripts(responseResult))
	} else {
		if result.Script == nil {
			writeError(w, http.StatusInternalServerError, "Bruno importer produced no canonical script")
			return
		}
		result.Script.Config.Bruno.BundleRef = "artifact://" + sanitizedArtifact.ID
		result.Script.Config.Bruno.BundleSHA256 = sanitizedArtifact.SHA256
		result.Script.Config.Bruno.SourceRevision = revisionID
		// Only advertise the server-replayable, disclosure-safe data suggestions
		// that the commit endpoint can derive again. Credential suggestions belong
		// exclusively to the governed Credentials card and never cross this wire.
		parameterization = buildTrustedImportParameterization(result.Script)
	}
	result.RawArchive = nil
	result.SanitizedArchive = nil
	result.OriginalUpload = nil
	_, actor, _ := s.resolveAuditActor(r)
	session := &brunoImportSession{
		ID:                      sessionID,
		ProjectID:               input.ProjectID,
		Actor:                   actor,
		Result:                  result,
		RawArtifact:             rawArtifact,
		SanitizedArtifact:       sanitizedArtifact,
		RevisionID:              revisionID,
		SyncScriptID:            strings.TrimSpace(syncScriptID),
		NativeCredentialTargets: collectBrunoNativeCredentialTargets(result),
		CreatedAt:               now,
		ExpiresAt:               now.Add(brunoImportSessionTTL),
	}
	if err := s.persistBrunoImportSession(r.Context(), session); err != nil {
		writeError(w, http.StatusServiceUnavailable, "persist Bruno import session: "+err.Error())
		return
	}
	if err := objects.DeletePublication(sessionID, input.ProjectID); err != nil {
		// The session descriptor now owns both objects. Retaining the journal is
		// safe; the janitor will observe those references and remove it later.
		slog.Warn("remove completed Bruno source publication", "publication_id", sessionID, "error", err)
	} else {
		publicationRecorded = false
	}
	sourcePublished = true
	releaseSourcePublication()
	// The encrypted, one-shot persisted session is authoritative. Do not retain
	// a second plaintext copy in the controller's in-memory lifecycle index.
	clearBrunoImportSecretValuePayloads(session.Result)
	s.storeBrunoImportSession(session)
	var syncPreview *brunoSyncPreview
	if !nativeMode && session.SyncScriptID != "" {
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
	auditDetails := map[string]any{
		"source_format":  result.SourceFormat,
		"files":          len(result.Manifest.Files),
		"source_bytes":   result.Manifest.TotalBytes,
		"output_mode":    input.Options.OutputMode,
		"native_scripts": len(result.Scripts),
		"request_types":  result.Capabilities.RequestTypes,
	}
	if result.Script != nil {
		auditDetails["sandbox"] = result.Script.Config.Bruno.Sandbox
	}
	s.recordAuditEvent(r, "script.import.bruno.inspect", "success", "bruno_import_session", session.ID, input.ProjectID, auditDetails)
	executionPreflight := map[string]any{}
	if nativeMode {
		executionPreflight = brunoNativeExecutionPreflight(result, retainedSecretValues)
	} else {
		executionPreflight = s.brunoExecutionPreflight(r.Context(), result.Script.Config.Bruno.Sandbox)
	}
	writeJSON(w, http.StatusOK, brunoInspectResponse{
		ImportSessionID: session.ID,
		ExpiresAt:       session.ExpiresAt,
		Result:          responseResult,
		Artifacts: []brunoArtifactSummary{
			brunoSanitizedArtifactPublicSummary(sanitizedArtifact),
		},
		SyncPreview:        syncPreview,
		ExecutionPreflight: executionPreflight,
		Parameterization:   parameterization,
		SharedValues:       sharedValues,
	})
}

// brunoNativeCandidateScripts collects the generated scripts of a native
// inspection so cross-script passes can see the whole collection at once.
func brunoNativeCandidateScripts(result *brunoconvert.Result) []*dsl.Script {
	if result == nil {
		return nil
	}
	scripts := make([]*dsl.Script, 0, len(result.Scripts))
	for _, candidate := range result.Scripts {
		if candidate.Script != nil {
			scripts = append(scripts, candidate.Script)
		}
	}
	return scripts
}

func setBrunoDestinationPreservationEvidence(
	requirement *brunoconvert.SecretRequirement,
	sourceValueRetained bool,
	destinationMapped bool,
) {
	if requirement == nil {
		return
	}
	requirement.DestinationValuePreserved = false
	requirement.PreservationUnavailableReason = ""
	switch {
	case requirement.ExistingReference:
		requirement.PreservationUnavailableReason = brunoconvert.PreservationUnavailableReference
	case !sourceValueRetained:
		requirement.PreservationUnavailableReason = brunoconvert.PreservationUnavailableSourceValue
	case !destinationMapped:
		requirement.PreservationUnavailableReason = brunoconvert.PreservationUnavailableDestination
	default:
		requirement.DestinationValuePreserved = true
	}
}

func publicBrunoNativeInspectResult(result *brunoconvert.Result) (*brunoconvert.Result, error) {
	if result == nil {
		return nil, errors.New("native Bruno inspection result is required")
	}
	projected := *result
	projected.RawArchive = nil
	projected.SanitizedArchive = nil
	projected.OriginalUpload = nil
	projected.Scripts = make([]brunoconvert.NativeScriptCandidate, len(result.Scripts))
	for index, candidate := range result.Scripts {
		projectedCandidate := candidate
		if candidate.Script != nil {
			projectedCandidate.Script = maskScriptSecrets(candidate.Script)
			if projectedCandidate.Script == nil {
				return nil, fmt.Errorf("mask native Bruno candidate %q", candidate.CandidateID)
			}
			if len(projectedCandidate.Script.Actions) > 0 {
				projectedCandidate.Target = projectedCandidate.Script.Actions[0].Target
			} else {
				projectedCandidate.Target = projectedCandidate.Script.Config.BaseURL
			}
		}
		projected.Scripts[index] = projectedCandidate
	}
	return &projected, nil
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

func brunoNativeExecutionPreflight(
	result *brunoconvert.Result,
	retainedSecretValues map[string]bool,
) map[string]any {
	type candidateStatus struct {
		CandidateID string   `json:"candidate_id"`
		Ready       bool     `json:"ready"`
		Blockers    []string `json:"blockers,omitempty"`
	}
	statuses := make([]candidateStatus, 0, len(result.Scripts))
	totalBlockers := 0
	for _, candidate := range result.Scripts {
		var blockers []string
		if len(candidate.MissingVariables) > 0 {
			blockers = append(blockers, "missing_variables")
		}
		if brunoNativeCandidateNeedsCredentialInput(candidate.SecretRequirements, retainedSecretValues) {
			blockers = append(blockers, "secret_bindings_required")
		}
		if scriptHasPromotionBlocker(candidate.Script) {
			blockers = append(blockers, "review_required")
		}
		totalBlockers += len(blockers)
		statuses = append(statuses, candidateStatus{
			CandidateID: candidate.CandidateID,
			Ready:       len(blockers) == 0,
			Blockers:    blockers,
		})
	}
	return map[string]any{
		"ready":      totalBlockers == 0 && len(statuses) > 0,
		"blockers":   totalBlockers,
		"candidates": statuses,
	}
}

func brunoNativeCandidateNeedsCredentialInput(
	requirements []brunoconvert.SecretRequirement,
	retainedSecretValues map[string]bool,
) bool {
	for _, requirement := range requirements {
		if !requirement.SourceValueAvailable && !retainedSecretValues[requirement.ID] {
			return true
		}
	}
	return false
}

func scriptHasPromotionBlocker(script *dsl.Script) bool {
	if script == nil {
		return true
	}
	var visit func([]dsl.Action) bool
	visit = func(actions []dsl.Action) bool {
		for _, action := range actions {
			if metadataBool(action.Metadata, "review_required") || metadataBool(action.Metadata, "promotion_blocker") {
				return true
			}
			if action.Control != nil && (visit(action.Control.Steps) || visit(action.Control.Else)) {
				return true
			}
		}
		return false
	}
	return visit(script.Init) || visit(script.Actions) || visit(script.Teardown)
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
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	_, folderIDProvided := fields["folder_id"]
	request.ProjectID = normalizeProjectID(request.ProjectID)
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "create_native_batch" {
		s.handleCommitBrunoNativeImport(w, r, request, fields)
		return
	}
	session, err := s.loadBrunoImportSession(r, request.ImportSessionID, request.ProjectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "Bruno import session was already claimed, consumed, or expired; inspect the source again unless an earlier response reported session_retained=true",
				"code":  "bruno_import_session_consumed",
			})
			return
		}
		if errors.Is(err, importartifact.ErrSessionClaimed) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "Bruno import session is already claimed by another commit",
				"code":  "bruno_import_session_claimed",
			})
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	defer clearBrunoImportSecretValues(session.Result)
	if session.Result != nil && strings.EqualFold(strings.TrimSpace(session.Result.OutputMode), brunoconvert.OutputModeNativeRequests) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "native Bruno import sessions require mode create_native_batch",
			"code":  "bruno_native_commit_mode_required",
		})
		return
	}
	migrateSecrets := request.MigrateSecrets != nil && *request.MigrateSecrets
	if !s.authorizeBrunoSourceSecretMigration(w, r, request.ProjectID, migrateSecrets) {
		return
	}
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno revision storage is not configured")
		return
	}
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
		existing, err = getPublishedImportScript(s.store, targetID)
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
	markCredentialPreservingImportScript(script)
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
		// Parameterization changes the executable script and must retain every
		// unrelated imported credential byte. Disclosure masking belongs only
		// on response/read projections after the exact durable script is saved.
		script = parameterized
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
		if folderIDProvided {
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
		if folderIDProvided {
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
	preserveSourceCredentials, err := brunoCanonicalPreservesSourceCredentials(
		session.Result.SecretRequirements,
		request.SecretBindings,
		existing,
		migrateSecrets,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	runtimeArtifactKind := "sanitized"
	if preserveSourceCredentials {
		runtimeArtifactKind = "raw"
	}
	migrationNeeded := migrateSecrets && brunoRequirementsHaveSourceValues(session.Result.SecretRequirements)
	if migrationNeeded && (s.projectSecretStore == nil || !secretstore.SupportsCreate(s.projectSecretStore)) {
		writeError(w, http.StatusServiceUnavailable, "no atomic create-only project secret provider is configured")
		return
	}
	if migrationNeeded && !secretstore.SupportsCreatedValueRollback(s.projectSecretStore) {
		writeError(w, http.StatusServiceUnavailable, "the project secret provider cannot safely roll back a failed import; configure the encrypted project secret store or preserve the imported values")
		return
	}
	// Claim the encrypted one-shot session before any source credential write or
	// durable identifier allocation. Concurrent requests may both complete the
	// read-only checks above, but only one is allowed to cross this boundary.
	claimedSession, artifactObjects, claimActor, claimToken, err := s.claimBrunoImportSession(
		r, request.ImportSessionID, request.ProjectID,
	)
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
	claimLease := startBrunoImportSessionClaimLease(
		artifactObjects, session.ID, request.ProjectID, claimActor, claimToken,
	)
	defer claimLease.stop()
	restoreClaimedSession := func() (bool, error) {
		if !claimActive {
			return true, nil
		}
		claimLease.stop()
		var restoreErr error
		for attempt := 0; attempt < 3; attempt++ {
			var retained bool
			restoreNow := time.Now().UTC()
			retained, restoreErr = artifactObjects.RestoreSessionClaim(
				context.Background(), session.ID, request.ProjectID, claimActor, claimToken, restoreNow,
			)
			sessionExpired := !session.ExpiresAt.After(restoreNow)
			if restoreErr == nil || sessionExpired {
				claimActive = false
				clearBrunoImportSecretValuePayloads(session.Result)
				if sessionExpired || !retained {
					s.cleanupBrunoImportSessionArtifacts(session)
					return false, errors.Join(errBrunoImportSessionExpiredDuringCommit, restoreErr)
				}
				s.storeBrunoImportSession(session)
				return true, nil
			}
		}
		// Keep claimActive set so the deferred restoration below makes another
		// bounded attempt after the response path has finished.
		return false, restoreErr
	}
	completeClaimedSession := func() error {
		if !claimActive {
			return nil
		}
		claimLease.stop()
		if completeErr := artifactObjects.CompleteSessionClaim(
			context.Background(), session.ID, request.ProjectID, claimActor, claimToken,
		); completeErr != nil {
			if errors.Is(completeErr, os.ErrNotExist) {
				// The durable commit succeeded and another cleanup path already
				// removed the claim, so there is no credential payload to restore.
				claimActive = false
				return nil
			}
			return completeErr
		}
		claimActive = false
		return nil
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
	migrationActor := importSecretMigrationActor("bruno", session.ID)
	if err := claimLease.assertOwned(); err != nil {
		_, restoreErr := restoreClaimedSession()
		writeError(w, http.StatusConflict, errors.Join(err, restoreErr).Error())
		return
	}
	secretBindings, missing, migrations, migrationConflicts, err := s.bindOrMigrateBrunoImportSecrets(
		r,
		session.Result.SecretRequirements,
		request.SecretBindings,
		existing,
		request.ProjectID,
		migrationActor,
		migrateSecrets,
		brunoSecretMigrationWindow{StartedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt},
		claimLease.assertOwned,
	)
	if err != nil {
		var partial *brunoSecretMigrationPartialError
		if errors.As(err, &partial) {
			attemptedMigrations := append([]secretstore.Metadata(nil), partial.Migrated...)
			retainedMigrations, rollbackErr := s.rollbackCreatedImportSecrets(
				r, request.ProjectID, migrationActor, partial.Migrated, "migration_failed",
			)
			partial.RemainingNames = remainingImportSecretMigrationNames(
				partial.RemainingNames, attemptedMigrations, retainedMigrations,
			)
			partial.Migrated = retainedMigrations
			if rollbackErr != nil {
				partial.Cause = errors.Join(partial.Cause, rollbackErr)
			}
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
				session.ID,
				partial.Migrated,
				partial.RemainingNames,
				partial.FailedName,
				reason,
				len(partial.RemainingNames) > 0,
				sessionRetained,
				"Any value still listed under migrated was not safely rolled back. Correct the provider failure or bind the remaining names, then retry this import session.",
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
	rollbackMigrations := func(trigger string) error {
		retained, rollbackErr := s.rollbackCreatedImportSecrets(
			r, request.ProjectID, migrationActor, migrations, trigger,
		)
		migrations = retained
		return rollbackErr
	}
	script.Config.Bruno.SecretBindings = secretBindings
	// Bruno commits intentionally combine the library row, encrypted source
	// artifacts, and revision record in one storage transaction, so they cannot
	// use TestRunner's ordinary script write. Apply the same final persistence
	// boundary before handing the canonical script to that transactional store.
	safeScript, err := preparePersistentImportedLibraryScript(script, existing, "bruno")
	if err != nil {
		rollbackErr := rollbackMigrations("script_validation_failed")
		err = errors.Join(err, rollbackErr)
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
				session.ID,
				migrations,
				nil,
				"",
				"secret_bindings_required",
				false,
				sessionRetained,
				"Any value still listed under migrated was not safely rolled back. Correct the validation issue and retry this import session.",
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
		UseRawArtifact:           preserveSourceCredentials,
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
				"runtime_artifact":  runtimeArtifactKind,
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
	if err := claimLease.assertOwned(); err != nil {
		rollbackErr := rollbackMigrations("session_claim_lost")
		err = errors.Join(err, rollbackErr)
		sessionRetained, restoreErr := restoreClaimedSession()
		if len(migrations) > 0 {
			writeBrunoSecretPartialFailure(
				w, http.StatusConflict, "bruno_import_claim_lost_after_secret_migration",
				"Bruno import session claim was lost after source credentials were migrated",
				request.ProjectID, session.ID, migrations, nil, "", "session_claim_lost",
				false, sessionRetained,
				"Any value still listed under migrated was not safely rolled back. Retry only when session_retained=true.",
			)
			return
		}
		writeError(w, http.StatusConflict, errors.Join(err, restoreErr).Error())
		return
	}
	commitResult, err := brunoStore.CommitBrunoImport(commit)
	if err != nil {
		// A database/provider can lose the commit acknowledgement after making the
		// transaction durable. Reconcile by the immutable revision ID before any
		// compensating secret deletion. On an unavailable lookup the only safe
		// action is to retain both the secrets and retryable session.
		reconciledRevision, reconcileErr := brunoStore.GetBrunoSourceRevision(session.RevisionID, request.ProjectID)
		switch {
		case reconcileErr == nil && reconciledRevision != nil &&
			reconciledRevision.ScriptID == script.ID && reconciledRevision.CanonicalScript != nil:
			commitResult = &storage.BrunoImportCommitResult{
				Script: reconciledRevision.CanonicalScript, Revision: reconciledRevision, Created: mode == "create",
			}
			err = nil
		case !errors.Is(reconcileErr, sql.ErrNoRows):
			sessionRetained, restoreErr := restoreClaimedSession()
			committedNames := make([]string, 0, len(migrations))
			for _, metadata := range migrations {
				committedNames = append(committedNames, metadata.Name)
			}
			sort.Strings(committedNames)
			s.recordAuditEvent(r, "script.import.bruno."+mode, "failure", "bruno_import_session", session.ID, request.ProjectID, map[string]any{
				"stage": "script_commit", "reason": "commit_state_unknown", "commit_state": "unknown",
				"secrets_migrated": len(migrations), "session_retained": sessionRetained,
			})
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "Bruno script commit state could not be confirmed. Newly migrated secrets were retained to avoid deleting credentials that a committed script may reference.",
				"code":  "bruno_import_commit_state_unknown", "commit_state": "unknown", "script_committed": "unknown",
				"session_retained": sessionRetained, "manual_retry_safe": sessionRetained,
				"idempotency_session_id": session.ID, "committed_names": committedNames,
				"secret_migrations": migrations, "source_values_retained": sessionRetained,
				"source_values_disclosed": false, "session_restore_failed": restoreErr != nil,
			})
			return
		}
	}
	if err != nil {
		rollbackErr := rollbackMigrations("script_commit_failed")
		err = errors.Join(err, rollbackErr)
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
				session.ID,
				migrations,
				nil,
				"",
				reason,
				false,
				sessionRetained,
				"Any value still listed under migrated was not safely rolled back. Retry this retained import session after correcting the commit failure.",
			)
			return
		}
		reason := brunoCommitErrorReason(err)
		s.recordAuditEvent(r, "script.import.bruno."+mode, "failure", "bruno_import_session", session.ID, request.ProjectID, map[string]any{
			"stage":                         "script_commit",
			"reason":                        reason,
			"secrets_migrated":              0,
			"secret_migrations_rolled_back": true,
		})
		switch {
		case errors.Is(err, storage.ErrBrunoImportConcurrentChange):
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "Bruno script changed before the import could be committed; newly migrated secrets were rolled back",
				"code":  "bruno_import_concurrent_change", "reason": reason, "session_retained": sessionRetained,
			})
		case errors.Is(err, storage.ErrBrunoRevisionNotFound):
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "The selected Bruno source revision is no longer available; newly migrated secrets were rolled back",
				"code":  "bruno_import_revision_not_found", "reason": reason, "session_retained": sessionRetained,
			})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "Bruno script commit failed; newly migrated secrets were rolled back",
				"code":  "bruno_import_commit_failed", "reason": reason, "session_retained": sessionRetained,
			})
		}
		return
	}
	if completeErr := completeClaimedSession(); completeErr != nil {
		slog.Warn("complete canonical Bruno import session claim", "session_id", session.ID, "error", completeErr)
	}
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
		"script":                   maskScriptSecrets(commitResult.Script),
		"revision":                 publicBrunoRevision(commitResult.Revision),
		"created":                  commitResult.Created,
		"change_summary":           changeSummary,
		"secret_migrations":        migrations,
		"parameterization_applied": parameterizationApplied,
	})
}

func (s *Server) handleCommitBrunoNativeImport(
	w http.ResponseWriter,
	r *http.Request,
	request brunoCommitRequest,
	fields map[string]json.RawMessage,
) {
	nativeStore, ok := s.store.(storage.BrunoNativeImportStore)
	if !ok || nativeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "native Bruno batch storage is not configured")
		return
	}
	allowedFields := map[string]struct{}{
		"project_id": {}, "import_session_id": {}, "mode": {}, "folder_id": {},
		"candidate_ids": {}, "name_overrides": {}, "secret_bindings": {},
		"migrate_secrets": {}, "acknowledged_finding_ids": {}, "shared_values": {},
	}
	var incompatible []string
	for field := range fields {
		if _, allowed := allowedFields[field]; !allowed {
			incompatible = append(incompatible, field)
		}
	}
	if len(incompatible) > 0 {
		sort.Strings(incompatible)
		writeError(w, http.StatusBadRequest, "native Bruno batch mode does not accept fields: "+strings.Join(incompatible, ", "))
		return
	}
	if strings.TrimSpace(request.ScriptID) != "" || strings.TrimSpace(request.SyncScriptID) != "" ||
		strings.TrimSpace(request.Name) != "" || request.ExpectedUpdatedAt != nil ||
		strings.TrimSpace(request.ExpectedActiveRevision) != "" || len(request.ParameterizationIDs) > 0 ||
		len(request.ConflictResolutions) > 0 {
		writeError(w, http.StatusBadRequest, "native Bruno batch mode does not accept canonical script, sync, revision, or parameterization fields")
		return
	}
	if err := normalizeBrunoNativeCommitRequest(&request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if request.FolderID == nil || strings.TrimSpace(*request.FolderID) == "" {
		writeError(w, http.StatusBadRequest, "folder_id is required for native Bruno batch imports")
		return
	}
	migrateSecrets := request.MigrateSecrets != nil && *request.MigrateSecrets

	_, actorSubject, _ := s.resolveAuditActor(r)
	if receipt, receiptErr := nativeStore.GetBrunoNativeImportByIdempotencyKey(request.ImportSessionID, request.ProjectID); receiptErr == nil {
		if receipt.Batch.CreatedBy != "" && receipt.Batch.CreatedBy != actorSubject {
			writeError(w, http.StatusForbidden, "native Bruno import receipt belongs to another actor")
			return
		}
		if !s.authorizeBrunoSourceSecretMigration(w, r, request.ProjectID, migrateSecrets) {
			return
		}
		replayRequest, canonicalizeErr := canonicalizeBrunoNativeReceiptRequest(request, receipt)
		if canonicalizeErr != nil {
			writeError(w, http.StatusConflict, storage.ErrBrunoNativeBatchConflict.Error())
			return
		}
		receiptProfile := receipt.Batch.RequestDigestProfile
		requestSHA, digestErr := brunoNativeCommitRequestSHAForProfile(receiptProfile, replayRequest)
		if digestErr != nil {
			writeError(w, http.StatusBadRequest, digestErr.Error())
			return
		}
		digestMatches := receipt.Batch.RequestSHA256 == requestSHA
		candidateOmissionEquivalent := !request.candidateIDsOmitted &&
			equalBrunoNativeCandidateSelection(request.CandidateIDs, receipt.Batch.CandidateIDs)
		nameOmissionEquivalent := !request.nameOverridesOmitted &&
			brunoNativeOverridesMatchReceipt(request.NameOverrides, receipt)
		for _, omissionVariant := range []struct {
			candidates bool
			names      bool
		}{
			{candidates: candidateOmissionEquivalent},
			{names: nameOmissionEquivalent},
			{candidates: candidateOmissionEquivalent, names: nameOmissionEquivalent},
		} {
			if digestMatches || (!omissionVariant.candidates && !omissionVariant.names) {
				continue
			}
			variant := replayRequest
			if omissionVariant.candidates {
				variant.candidateIDsOmitted = true
			}
			if omissionVariant.names {
				variant.nameOverridesOmitted = true
			}
			variantSHA, variantErr := brunoNativeCommitRequestSHAForProfile(receiptProfile, variant)
			if variantErr != nil {
				writeError(w, http.StatusBadRequest, variantErr.Error())
				return
			}
			digestMatches = receipt.Batch.RequestSHA256 == variantSHA
		}
		if !digestMatches {
			writeError(w, http.StatusConflict, storage.ErrBrunoNativeBatchConflict.Error())
			return
		}
		s.finalizeCommittedBrunoImportSession(
			request.ImportSessionID, request.ProjectID, actorSubject,
		)
		for _, script := range receipt.Scripts {
			if _, _, refreshErr := s.runner.GetScriptDurable(script.ID); refreshErr != nil {
				slogWarnBrunoCacheRefresh(refreshErr)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"batch_id":          receipt.Batch.ID,
			"scripts":           maskBrunoNativeCommitScripts(receipt.Scripts),
			"count":             len(receipt.Scripts),
			"created":           false,
			"idempotent_replay": true,
		})
		return
	} else if !errors.Is(receiptErr, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, receiptErr.Error())
		return
	}

	// A committed session no longer has an inspection record, so receipt replay
	// must remain first. For every uncommitted request, however, validate the
	// actor/project-bound inspection session before resolving the destination.
	// Otherwise a fabricated or stale session ID can be used to distinguish a
	// foreign folder from a nonexistent one.
	session, err := s.loadBrunoImportSession(r, request.ImportSessionID, request.ProjectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "Bruno import session was already claimed, consumed, or expired",
				"code":  "bruno_import_session_consumed",
			})
		} else if errors.Is(err, importartifact.ErrSessionClaimed) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "Bruno import session is already claimed by another commit",
				"code":  "bruno_import_session_claimed",
			})
		} else {
			writeError(w, http.StatusConflict, err.Error())
		}
		return
	}
	defer clearBrunoImportSecretValues(session.Result)
	if !s.authorizeBrunoSourceSecretMigration(w, r, request.ProjectID, migrateSecrets) {
		return
	}

	folder, folderErr := s.store.GetFolder(*request.FolderID)
	if folderErr != nil {
		if errors.Is(folderErr, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "native Bruno destination folder not found")
		} else {
			writeError(w, http.StatusInternalServerError, folderErr.Error())
		}
		return
	}
	if folder == nil {
		writeError(w, http.StatusNotFound, "native Bruno destination folder not found")
		return
	}
	if normalizeProjectID(folder.ProjectID) != request.ProjectID {
		writeError(w, http.StatusForbidden, "native Bruno destination folder belongs to another project")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(folder.Category), "scripts") {
		writeError(w, http.StatusConflict, "native Bruno destination must be a script folder")
		return
	}

	if session.Result == nil || len(session.Result.Scripts) == 0 {
		writeError(w, http.StatusConflict, "Bruno import session does not contain native request candidates")
		return
	}

	selected, err := selectBrunoNativeCandidates(session.Result.Scripts, request.CandidateIDs)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := validateBrunoNativeNamesAndAcknowledgements(selected, request.NameOverrides, request.AcknowledgedFindingIDs); err != nil {
		var reviewErr *brunoNativeReviewRequiredError
		if errors.As(err, &reviewErr) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":    reviewErr.Error(),
				"code":     "bruno_native_review_acknowledgement_required",
				"findings": reviewErr.FindingIDs,
			})
		} else {
			writeError(w, http.StatusConflict, err.Error())
		}
		return
	}
	requirements, err := selectedBrunoNativeSecretRequirements(session.Result.SecretRequirements, selected)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := validateBrunoNativeSecretBindingKeys(requirements, request.SecretBindings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	scripts := make([]*dsl.Script, 0, len(selected))
	candidateIDs := make([]string, 0, len(selected))
	plannedIDs := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		script := cloneScriptDefinition(candidate.Script)
		if script == nil || strings.TrimSpace(candidate.PlannedScriptID) == "" {
			writeError(w, http.StatusConflict, "native Bruno candidate has no server-owned script")
			return
		}
		script.ID = strings.TrimSpace(candidate.PlannedScriptID)
		if _, parseErr := uuid.Parse(script.ID); parseErr != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("native Bruno candidate %q has an invalid planned script id", candidate.CandidateID))
			return
		}
		if _, duplicate := plannedIDs[script.ID]; duplicate {
			writeError(w, http.StatusConflict, fmt.Sprintf("native Bruno candidates contain duplicate planned script id %q", script.ID))
			return
		}
		plannedIDs[script.ID] = struct{}{}
		if strings.TrimSpace(script.Protocol) != "http" {
			writeError(w, http.StatusConflict, fmt.Sprintf("native Bruno candidate %q protocol must be exactly http", candidate.CandidateID))
			return
		}
		if _, existingErr := s.store.GetScript(script.ID); existingErr == nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("planned native Bruno script id %q already exists", script.ID))
			return
		} else if !errors.Is(existingErr, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, existingErr.Error())
			return
		}
		script.ProjectID = request.ProjectID
		script.FolderID = cloneStringPtr(request.FolderID)
		if override, exists := request.NameOverrides[candidate.CandidateID]; exists {
			script.Name = strings.TrimSpace(override)
		}
		if script.Metadata == nil {
			script.Metadata = &dsl.ScriptMetadata{}
		}
		if script.Metadata.Import == nil {
			script.Metadata.Import = &dsl.ImportMetadata{Source: "bruno"}
		}
		if script.Metadata.Import.Preserved == nil {
			script.Metadata.Import.Preserved = make(map[string]any)
		}
		markCredentialPreservingImportScript(script)
		script.Metadata.Import.Preserved["native_batch_id"] = session.RevisionID
		script.Metadata.Import.Preserved["candidate_id"] = candidate.CandidateID
		script.Metadata.Import.Preserved["sanitized_source_sha256"] = session.SanitizedArtifact.SHA256
		if len(candidate.MissingVariables) > 0 && len(script.Actions) > 0 {
			missingVariables := append([]string(nil), candidate.MissingVariables...)
			sort.Strings(missingVariables)
			if script.Actions[0].Metadata == nil {
				script.Actions[0].Metadata = make(map[string]any)
			}
			script.Actions[0].Metadata["execution_blocker"] = "bruno_missing_variables"
			script.Actions[0].Metadata["missing_variables"] = missingVariables
		}
		// Native inspection retains authored credential literals in the encrypted,
		// authenticated source session so the imported request remains runnable.
		// Do not apply the governed-reference-only XML rule here: that rule is for
		// ordinary, non-imported scripts and conflicts with the import-preservation
		// contract. The converter already parsed and bounded the XML before the
		// protected session was published, and the persistence boundary below masks
		// the browser response without rewriting the durable executable body.
		if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":        "native Bruno script validation failed",
				"code":         "bruno_native_script_validation_failed",
				"candidate_id": candidate.CandidateID,
				"issues":       issues,
			})
			return
		}
		scripts = append(scripts, script)
		candidateIDs = append(candidateIDs, candidate.CandidateID)
	}

	// Collection-wide values are applied across the whole selected batch before
	// anything is canonicalized or persisted: one value can touch every script,
	// so a per-script pass would leave the batch inconsistent.
	if len(request.SharedValues) > 0 {
		trusted := trustedSharedValueCandidates(scripts)
		known := make(map[string]bool, len(trusted))
		for _, candidate := range trusted {
			known[candidate.ID] = true
		}
		for id := range request.SharedValues {
			if !known[id] {
				writeError(w, http.StatusConflict,
					"selected Bruno shared values are stale or unavailable; inspect the source again")
				return
			}
		}
		if _, applyErr := sharedvalue.Apply(scripts, trusted, request.SharedValues); applyErr != nil {
			if errors.Is(applyErr, sharedvalue.ErrCredentialShaped) {
				writeError(w, http.StatusUnprocessableEntity,
					"credential-shaped shared values must use guided credential bindings")
				return
			}
			writeError(w, http.StatusUnprocessableEntity, "failed to apply Bruno shared values: "+applyErr.Error())
			return
		}
		for index, script := range scripts {
			if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":        "native Bruno script validation failed after applying shared values",
					"code":         "bruno_native_script_validation_failed",
					"candidate_id": candidateIDs[index],
					"issues":       issues,
				})
				return
			}
		}
	}

	request = canonicalizeBrunoNativeSelectedRequest(request, selected, scripts)
	requestSHA, err := brunoNativeCommitRequestSHA(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plannedSecretBindings, plannedCredentialValues, err := planBrunoNativeSecretBindings(
		requirements,
		request.SecretBindings,
		migrateSecrets,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	for index, script := range scripts {
		bound, bindErr := applyBrunoNativeCredentialTargets(
			script,
			session.NativeCredentialTargets[candidateIDs[index]],
			selected[index].SecretRequirements,
			plannedCredentialValues,
		)
		if bindErr != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":        "native Bruno secret binding validation failed",
				"code":         "bruno_native_binding_failed",
				"candidate_id": candidateIDs[index],
			})
			return
		}
		safeScript, persistErr := preparePersistentBrunoNativeScript(bound)
		if persistErr != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":        "native Bruno persistence validation failed",
				"code":         "bruno_native_persistence_validation_failed",
				"candidate_id": candidateIDs[index],
			})
			return
		}
		if err := validatePersistentLibraryScript(safeScript); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":        "native Bruno library validation failed",
				"code":         "bruno_native_library_validation_failed",
				"candidate_id": candidateIDs[index],
			})
			return
		}
		scripts[index] = safeScript
	}

	migrationNeeded := migrateSecrets && brunoRequirementsHaveSourceValues(requirements)
	if migrationNeeded && (s.projectSecretStore == nil || !secretstore.SupportsCreate(s.projectSecretStore)) {
		writeError(w, http.StatusServiceUnavailable, "no atomic create-only project secret provider is configured")
		return
	}
	if migrationNeeded && !secretstore.SupportsCreatedValueRollback(s.projectSecretStore) {
		writeError(w, http.StatusServiceUnavailable, "the project secret provider cannot safely roll back a failed import; configure the encrypted project secret store or preserve the imported values")
		return
	}

	claimedSession, artifactObjects, claimActor, claimToken, err := s.claimBrunoImportSession(
		r, request.ImportSessionID, request.ProjectID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "Bruno import session was already consumed or expired",
				"code":  "bruno_import_session_consumed",
			})
		} else {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "Bruno import session is already claimed or unavailable: " + err.Error(),
				"code":  "bruno_import_session_claimed",
			})
		}
		return
	}
	session = claimedSession
	defer clearBrunoImportSecretValues(session.Result)
	claimActive := true
	claimLease := startBrunoImportSessionClaimLease(
		artifactObjects, session.ID, request.ProjectID, claimActor, claimToken,
	)
	defer claimLease.stop()
	restoreClaimedSession := func() (bool, error) {
		if !claimActive {
			return true, nil
		}
		claimLease.stop()
		var restoreErr error
		for attempt := 0; attempt < 3; attempt++ {
			var retained bool
			restoreNow := time.Now().UTC()
			retained, restoreErr = artifactObjects.RestoreSessionClaim(
				context.Background(), session.ID, request.ProjectID, claimActor, claimToken, restoreNow,
			)
			sessionExpired := !session.ExpiresAt.After(restoreNow)
			if restoreErr == nil || sessionExpired {
				claimActive = false
				clearBrunoImportSecretValuePayloads(session.Result)
				if sessionExpired || !retained {
					s.cleanupBrunoImportSessionArtifacts(session)
					return false, errors.Join(errBrunoImportSessionExpiredDuringCommit, restoreErr)
				}
				s.storeBrunoImportSession(session)
				return true, nil
			}
		}
		// Keep claimActive set so the deferred restoration below makes another
		// bounded attempt after the response path has finished.
		return false, restoreErr
	}
	completeClaimedSession := func() error {
		if !claimActive {
			return nil
		}
		claimLease.stop()
		if completeErr := artifactObjects.CompleteSessionClaim(
			context.Background(), session.ID, request.ProjectID, claimActor, claimToken,
		); completeErr != nil {
			if errors.Is(completeErr, os.ErrNotExist) {
				// A matching durable receipt retry may have finalized this claim
				// after the database commit but before this response resumed.
				claimActive = false
				return nil
			}
			return completeErr
		}
		claimActive = false
		return nil
	}
	defer func() {
		if claimActive {
			_, _ = restoreClaimedSession()
		}
	}()

	if err := claimLease.assertOwned(); err != nil {
		_, restoreErr := restoreClaimedSession()
		writeError(w, http.StatusConflict, errors.Join(err, restoreErr).Error())
		return
	}
	migrationActor := importSecretMigrationActor("bruno-native", session.ID)
	secretBindings, missing, migrations, migrationConflicts, err := s.bindOrMigrateBrunoImportSecrets(
		r, requirements, request.SecretBindings, nil, request.ProjectID, migrationActor, migrateSecrets,
		brunoSecretMigrationWindow{StartedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt},
		claimLease.assertOwned,
	)
	if err != nil {
		var partial *brunoSecretMigrationPartialError
		if errors.As(err, &partial) {
			attemptedMigrations := append([]secretstore.Metadata(nil), partial.Migrated...)
			retainedMigrations, rollbackErr := s.rollbackCreatedImportSecrets(
				r, request.ProjectID, migrationActor, partial.Migrated, "migration_failed",
			)
			partial.RemainingNames = remainingImportSecretMigrationNames(
				partial.RemainingNames, attemptedMigrations, retainedMigrations,
			)
			partial.Migrated = retainedMigrations
			if rollbackErr != nil {
				partial.Cause = errors.Join(partial.Cause, rollbackErr)
			}
			sessionRetained, restoreErr := restoreClaimedSession()
			reason := projectSecretErrorReason(partial.Cause)
			if restoreErr != nil {
				reason = firstNonEmpty(reason, "session_restore_failed")
			}
			writeBrunoSecretPartialFailure(
				w, brunoSecretMigrationFailureStatus(partial.Cause), "bruno_secret_migration_partial",
				"Bruno source credential migration partially completed", request.ProjectID, session.ID,
				partial.Migrated, partial.RemainingNames, partial.FailedName, reason,
				len(partial.RemainingNames) > 0, sessionRetained,
				"Any value still listed under migrated was not safely rolled back. Correct the provider failure or bind the remaining names, then retry this import session.",
			)
			return
		}
		if retained, restoreErr := restoreClaimedSession(); !retained {
			err = errors.Join(err, fmt.Errorf("restore Bruno import session: %w", restoreErr))
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(migrationConflicts) > 0 || len(missing) > 0 {
		if retained, restoreErr := restoreClaimedSession(); !retained {
			writeError(w, http.StatusInternalServerError, "restore Bruno import session: "+restoreErr.Error())
			return
		}
		code := secretBindingsRequiredCode
		message := "Bruno secret bindings are required before commit"
		requirementPayload := missing
		if len(migrationConflicts) > 0 {
			code = "bruno_secret_migration_conflict"
			message = "Bruno secret migration found existing values under automatically selected names"
			requirementPayload = migrationConflicts
		}
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": message, "code": code, "requirements": requirementPayload,
		})
		return
	}
	rollbackMigrations := func(trigger string) error {
		retained, rollbackErr := s.rollbackCreatedImportSecrets(
			r, request.ProjectID, migrationActor, migrations, trigger,
		)
		migrations = retained
		return rollbackErr
	}

	failAfterSecretMigration := func(status int, code string, message string, cause error, reason string) {
		cause = errors.Join(cause, rollbackMigrations(reason))
		sessionRetained, restoreErr := restoreClaimedSession()
		if restoreErr != nil {
			cause = errors.Join(cause, fmt.Errorf("restore Bruno import session: %w", restoreErr))
		}
		if len(migrations) > 0 {
			writeBrunoSecretPartialFailure(
				w, status, code, message, request.ProjectID, session.ID, migrations, nil, "", reason,
				false, sessionRetained,
				"Any value still listed under migrated was not safely rolled back. Correct the failure and retry this retained import session.",
			)
			return
		}
		slog.Warn(
			"native Bruno batch commit failed",
			"session_id", session.ID,
			"project_id", request.ProjectID,
			"code", code,
			"reason", reason,
			"error", cause,
		)
		writeJSON(w, status, map[string]any{
			"error":            message,
			"code":             code,
			"reason":           reason,
			"session_retained": sessionRetained,
		})
	}

	if !equalBrunoNativeSecretBindings(secretBindings, plannedSecretBindings) {
		failAfterSecretMigration(
			http.StatusInternalServerError,
			"bruno_native_binding_plan_changed",
			"Native Bruno secret bindings changed after preflight validation",
			errors.New("native Bruno binding plan changed"),
			"secret_binding",
		)
		return
	}
	if err := claimLease.assertOwned(); err != nil {
		failAfterSecretMigration(
			http.StatusConflict,
			"bruno_native_claim_lost",
			"Native Bruno import session claim was lost before batch commit",
			err,
			"session_claim_lost",
		)
		return
	}

	releaseReferenceMutation, fenceErr := s.acquireSecretReferenceMutation(request.ProjectID)
	if fenceErr != nil {
		failAfterSecretMigration(
			http.StatusServiceUnavailable,
			"bruno_native_reference_fence_unavailable",
			"Native Bruno script references are busy; retry the import",
			fenceErr,
			"reference_fence",
		)
		return
	}
	commitResult, err := nativeStore.CommitBrunoNativeImport(&storage.BrunoNativeImportCommit{
		Batch: storage.BrunoNativeImportBatch{
			ID:             session.RevisionID,
			ProjectID:      request.ProjectID,
			IdempotencyKey: session.ID,
			// Record the profile alongside the digest so a later replay can
			// verify with the same hash this commit used.
			RequestDigestProfile: brunoNativeCommitCurrentDigestProfile,
			RequestSHA256:        requestSHA,
			CandidateIDs:         candidateIDs,
			CreatedBy:            actorSubject,
		},
		Scripts:           scripts,
		RawArtifact:       session.RawArtifact,
		SanitizedArtifact: session.SanitizedArtifact,
		RefKeyEnv:         s.runner.envCode,
	})
	releaseReferenceMutation()
	if err != nil {
		reconciled, reconcileErr := nativeStore.GetBrunoNativeImportByIdempotencyKey(session.ID, request.ProjectID)
		switch {
		case reconcileErr == nil:
			if reconciled.Batch.CreatedBy != "" && reconciled.Batch.CreatedBy != actorSubject {
				_ = completeClaimedSession()
				s.cleanupBrunoImportSessionArtifacts(session)
				writeError(w, http.StatusForbidden, "native Bruno import receipt belongs to another actor")
				return
			}
			if reconciled.Batch.RequestSHA256 != requestSHA {
				_ = completeClaimedSession()
				s.cleanupBrunoImportSessionArtifacts(session)
				writeError(w, http.StatusConflict, storage.ErrBrunoNativeBatchConflict.Error())
				return
			}
			// A commit acknowledgement can be lost even though the transaction
			// became durable. Treat the immutable matching receipt as success.
			commitResult = reconciled
		case errors.Is(reconcileErr, sql.ErrNoRows):
			status := brunoCommitErrorStatus(err)
			code := "bruno_native_commit_failed"
			message := "Native Bruno batch commit failed"
			if len(migrations) > 0 {
				code = "bruno_native_commit_failed_after_secret_migration"
				message = "Native Bruno batch commit failed after source credentials were migrated"
			}
			failAfterSecretMigration(status, code, message, err, brunoCommitErrorReason(err))
			return
		default:
			sessionRetained, restoreErr := restoreClaimedSession()
			if restoreErr != nil {
				reconcileErr = errors.Join(reconcileErr, fmt.Errorf("restore Bruno import session: %w", restoreErr))
			}
			committedNames := make([]string, 0, len(migrations))
			for _, metadata := range migrations {
				committedNames = append(committedNames, metadata.Name)
			}
			sort.Strings(committedNames)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":                   "Native Bruno batch commit state could not be reconciled: " + reconcileErr.Error(),
				"code":                    "bruno_native_commit_state_unknown",
				"commit_state":            "unknown",
				"script_committed":        "unknown",
				"session_retained":        sessionRetained,
				"committed_names":         committedNames,
				"secret_migrations":       migrations,
				"manual_retry_safe":       sessionRetained,
				"idempotency_session_id":  session.ID,
				"source_values_retained":  sessionRetained,
				"source_values_disclosed": false,
			})
			return
		}
	}
	if completeErr := completeClaimedSession(); completeErr != nil {
		slog.Warn("complete native Bruno import session claim", "session_id", session.ID, "error", completeErr)
	}
	s.cleanupBrunoImportSessionArtifacts(session)
	for _, script := range commitResult.Scripts {
		if _, _, refreshErr := s.runner.GetScriptDurable(script.ID); refreshErr != nil {
			slogWarnBrunoCacheRefresh(refreshErr)
		}
	}
	s.recordAuditEvent(r, "script.import.bruno.native.create", "success", "bruno_import_batch", commitResult.Batch.ID, request.ProjectID, map[string]any{
		"source_format":    session.Result.SourceFormat,
		"scripts_created":  len(commitResult.Scripts),
		"secrets_migrated": len(migrations),
	})
	status := http.StatusCreated
	if !commitResult.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"batch_id":          commitResult.Batch.ID,
		"scripts":           maskBrunoNativeCommitScripts(commitResult.Scripts),
		"count":             len(commitResult.Scripts),
		"created":           commitResult.Created,
		"idempotent_replay": !commitResult.Created,
		"secret_migrations": migrations,
	})
}

func maskBrunoNativeCommitScripts(scripts []*dsl.Script) []*dsl.Script {
	if scripts == nil {
		return nil
	}
	masked := make([]*dsl.Script, len(scripts))
	for index, script := range scripts {
		masked[index] = maskScriptSecrets(script)
		if brunoNativeResponseHasProjectionGate(masked[index]) {
			masked[index] = nil
		}
		if masked[index] == nil && script != nil {
			// A legacy receipt can fall back to a subsequently edited script
			// whose body is no longer safely projectable. Preserve only the
			// server-owned receipt identity so clients can reconcile the batch
			// without returning the unprojectable executable payload.
			masked[index] = &dsl.Script{
				ID:        script.ID,
				Name:      maskedScriptSecret,
				RefKey:    script.RefKey,
				Version:   script.Version,
				Protocol:  script.Protocol,
				ProjectID: script.ProjectID,
				FolderID:  cloneStringPtr(script.FolderID),
				CreatedAt: script.CreatedAt,
				UpdatedAt: script.UpdatedAt,
			}
		}
	}
	return masked
}

func brunoNativeResponseHasProjectionGate(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	var visit func([]dsl.Action) bool
	visit = func(actions []dsl.Action) bool {
		for _, action := range actions {
			review, _ := action.Metadata["import_review"].(map[string]any)
			source, _ := review["source"].(string)
			if scriptProjectionResponseGateMarker(action.Metadata) ||
				strings.EqualFold(strings.TrimSpace(source), "script_projection") {
				for _, blocker := range executionReviewBlockerCodes(action.Metadata) {
					if blocker == "import_review_required" {
						return true
					}
				}
			}
			if action.Control != nil &&
				(visit(action.Control.Steps) || visit(action.Control.Else)) {
				return true
			}
		}
		return false
	}
	return visit(script.Init) || visit(script.Actions) || visit(script.Teardown)
}

type brunoNativeReviewRequiredError struct {
	FindingIDs []string
}

func (e *brunoNativeReviewRequiredError) Error() string {
	return "native Bruno import has review-gated findings that must be acknowledged before saving"
}

func selectBrunoNativeCandidates(candidates []brunoconvert.NativeScriptCandidate, requested []string) ([]brunoconvert.NativeScriptCandidate, error) {
	byID := make(map[string]brunoconvert.NativeScriptCandidate, len(candidates))
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.CandidateID)
		if id == "" {
			return nil, errors.New("native Bruno inspection contains a candidate without an id")
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, fmt.Errorf("native Bruno inspection contains duplicate candidate id %q", id)
		}
		byID[id] = candidate
	}
	if requested == nil {
		return append([]brunoconvert.NativeScriptCandidate(nil), candidates...), nil
	}
	if len(requested) == 0 {
		return nil, errors.New("candidate_ids must be omitted to select every candidate or contain at least one id")
	}
	selectedIDs := make(map[string]struct{}, len(requested))
	for _, rawID := range requested {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return nil, errors.New("candidate_ids cannot contain an empty id")
		}
		if _, duplicate := selectedIDs[id]; duplicate {
			return nil, fmt.Errorf("candidate id %q is duplicated", id)
		}
		if _, exists := byID[id]; !exists {
			return nil, fmt.Errorf("candidate id %q is stale or unavailable; inspect the source again", id)
		}
		selectedIDs[id] = struct{}{}
	}
	selected := make([]brunoconvert.NativeScriptCandidate, 0, len(selectedIDs))
	for _, candidate := range candidates {
		if _, ok := selectedIDs[candidate.CandidateID]; ok {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("native Bruno import requires at least one selected candidate")
	}
	return selected, nil
}

func validateBrunoNativeNamesAndAcknowledgements(
	candidates []brunoconvert.NativeScriptCandidate,
	nameOverrides map[string]string,
	acknowledgedFindingIDs []string,
) error {
	selected := make(map[string]struct{}, len(candidates))
	knownFindings := make(map[string]struct{})
	requiredFindings := make(map[string]struct{})
	names := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		selected[candidate.CandidateID] = struct{}{}
		candidateHasRequiredFinding := false
		name := ""
		if candidate.Script != nil {
			name = strings.TrimSpace(candidate.Script.Name)
		}
		if override, exists := nameOverrides[candidate.CandidateID]; exists {
			name = strings.TrimSpace(override)
			if name == "" {
				return fmt.Errorf("name override for candidate %q cannot be empty", candidate.CandidateID)
			}
		}
		if name == "" {
			return fmt.Errorf("native Bruno candidate %q has no script name", candidate.CandidateID)
		}
		nameKey := strings.ToLower(name)
		if previous, duplicate := names[nameKey]; duplicate {
			return fmt.Errorf("native Bruno script name %q is duplicated by candidates %q and %q", name, previous, candidate.CandidateID)
		}
		names[nameKey] = candidate.CandidateID
		for _, diagnostic := range candidate.Diagnostics {
			id := strings.TrimSpace(diagnostic.ID)
			if id == "" {
				continue
			}
			knownFindings[id] = struct{}{}
			if diagnostic.AcknowledgementRequired {
				requiredFindings[id] = struct{}{}
				candidateHasRequiredFinding = true
			}
		}
		if scriptHasPromotionBlocker(candidate.Script) && !candidateHasRequiredFinding {
			return fmt.Errorf("native Bruno candidate %q has a promotion blocker without an acknowledgement finding", candidate.CandidateID)
		}
	}
	for candidateID := range nameOverrides {
		if _, ok := selected[strings.TrimSpace(candidateID)]; !ok {
			return fmt.Errorf("name override candidate %q is not selected", candidateID)
		}
	}
	acknowledged := make(map[string]struct{}, len(acknowledgedFindingIDs))
	for _, rawID := range acknowledgedFindingIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return errors.New("acknowledged_finding_ids cannot contain an empty id")
		}
		if _, known := knownFindings[id]; !known {
			return fmt.Errorf("acknowledged finding %q is stale or unavailable; inspect the source again", id)
		}
		acknowledged[id] = struct{}{}
	}
	var missing []string
	for id := range requiredFindings {
		if _, ok := acknowledged[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &brunoNativeReviewRequiredError{FindingIDs: missing}
	}
	return nil
}

func selectedBrunoNativeSecretRequirements(
	all []brunoconvert.SecretRequirement,
	candidates []brunoconvert.NativeScriptCandidate,
) ([]brunoconvert.SecretRequirement, error) {
	byID := make(map[string]brunoconvert.SecretRequirement, len(all))
	for _, requirement := range all {
		byID[requirement.ID] = requirement
	}
	seen := make(map[string]struct{})
	selected := make([]brunoconvert.SecretRequirement, 0)
	for _, candidate := range candidates {
		for _, publicRequirement := range candidate.SecretRequirements {
			if _, duplicate := seen[publicRequirement.ID]; duplicate {
				continue
			}
			requirement, exists := byID[publicRequirement.ID]
			if !exists {
				return nil, fmt.Errorf("native Bruno candidate %q has a stale secret requirement; inspect the source again", candidate.CandidateID)
			}
			seen[publicRequirement.ID] = struct{}{}
			selected = append(selected, requirement)
		}
	}
	return selected, nil
}

func applyBrunoNativeCredentialTargets(
	script *dsl.Script,
	targets []brunoconvert.NativeCredentialTarget,
	requirements []brunoconvert.SecretRequirement,
	replacements map[string]string,
) (*dsl.Script, error) {
	if script == nil {
		return nil, errors.New("native Bruno script is required")
	}
	requirementsByID := make(map[string]brunoconvert.SecretRequirement, len(requirements))
	for _, requirement := range requirements {
		requirementID := strings.TrimSpace(requirement.ID)
		if requirementID == "" {
			return nil, errors.New("native Bruno credential requirement has no id")
		}
		requirementsByID[requirementID] = requirement
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, err
	}
	type rewrite struct {
		start       int
		end         int
		replacement string
	}
	rewritesByPointer := make(map[string][]rewrite)
	targetCounts := make(map[string]int, len(requirements))
	for _, target := range targets {
		requirement, exists := requirementsByID[target.RequirementID]
		if !exists {
			return nil, errors.New("native Bruno credential target references a stale requirement")
		}
		value, ok := jsonPointerStringValue(document, target.JSONPointer)
		if !ok {
			return nil, errors.New("native Bruno credential target no longer references a string")
		}
		sum := sha256.Sum256([]byte(value))
		if fmt.Sprintf("%x", sum[:]) != target.SanitizedSHA256 {
			return nil, errors.New("native Bruno credential target no longer matches the inspected script")
		}
		start, end, ok := brunoNativeStringOccurrenceSpan(
			value,
			requirement.Replacement,
			target.Occurrence,
		)
		if !ok {
			return nil, errors.New("native Bruno credential target occurrence is stale")
		}
		targetCounts[target.RequirementID]++
		replacement, planned := replacements[target.RequirementID]
		if !planned || replacement == requirement.Replacement {
			continue
		}
		rewritesByPointer[target.JSONPointer] = append(
			rewritesByPointer[target.JSONPointer],
			rewrite{start: start, end: end, replacement: replacement},
		)
	}
	for requirementID, replacement := range replacements {
		requirement, appliesToCandidate := requirementsByID[requirementID]
		if !appliesToCandidate || replacement == requirement.Replacement {
			continue
		}
		if targetCounts[requirementID] == 0 {
			return nil, errors.New("native Bruno credential target is unavailable; inspect the source again")
		}
	}
	pointers := make([]string, 0, len(rewritesByPointer))
	for pointer := range rewritesByPointer {
		pointers = append(pointers, pointer)
	}
	sort.Strings(pointers)
	for _, pointer := range pointers {
		value, ok := jsonPointerStringValue(document, pointer)
		if !ok {
			return nil, errors.New("native Bruno credential target no longer references a string")
		}
		rewrites := rewritesByPointer[pointer]
		sort.Slice(rewrites, func(i, j int) bool {
			if rewrites[i].start == rewrites[j].start {
				return rewrites[i].end > rewrites[j].end
			}
			return rewrites[i].start > rewrites[j].start
		})
		previousStart := len(value)
		for _, rewrite := range rewrites {
			if rewrite.start < 0 || rewrite.end < rewrite.start ||
				rewrite.end > previousStart {
				return nil, errors.New("native Bruno credential targets overlap")
			}
			value = value[:rewrite.start] + rewrite.replacement + value[rewrite.end:]
			previousStart = rewrite.start
		}
		if !setJSONPointerStringValue(document, pointer, value) {
			return nil, errors.New("native Bruno credential target could not be updated")
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var bound dsl.Script
	if err := json.Unmarshal(encoded, &bound); err != nil {
		return nil, err
	}
	return &bound, nil
}

func brunoNativeStringOccurrenceSpan(
	value string,
	token string,
	occurrence int,
) (int, int, bool) {
	if token == "" || occurrence < 0 {
		return 0, 0, false
	}
	cursor := 0
	for current := 0; ; current++ {
		index := strings.Index(value[cursor:], token)
		if index < 0 {
			return 0, 0, false
		}
		index += cursor
		if current == occurrence {
			return index, index + len(token), true
		}
		cursor = index + len(token)
	}
}

func setJSONPointerStringValue(root any, pointer string, value string) bool {
	tokens, err := splitJSONPointer(pointer)
	if err != nil || len(tokens) == 0 {
		return false
	}
	current := root
	for _, token := range tokens[:len(tokens)-1] {
		switch typed := current.(type) {
		case map[string]any:
			child, exists := typed[token]
			if !exists {
				return false
			}
			current = child
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return false
			}
			current = typed[index]
		default:
			return false
		}
	}
	last := tokens[len(tokens)-1]
	switch typed := current.(type) {
	case map[string]any:
		if _, ok := typed[last].(string); !ok {
			return false
		}
		typed[last] = value
		return true
	case []any:
		index, err := strconv.Atoi(last)
		if err != nil || index < 0 || index >= len(typed) {
			return false
		}
		if _, ok := typed[index].(string); !ok {
			return false
		}
		typed[index] = value
		return true
	default:
		return false
	}
}

func normalizeBrunoNativeCommitRequest(request *brunoCommitRequest) error {
	if request == nil {
		return errors.New("native Bruno commit request is required")
	}
	request.ProjectID = normalizeProjectID(request.ProjectID)
	request.ImportSessionID = strings.TrimSpace(request.ImportSessionID)
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.ImportSessionID == "" {
		return errors.New("import_session_id is required")
	}
	if request.Mode != "create_native_batch" {
		return errors.New("native Bruno commit mode must be create_native_batch")
	}
	request.candidateIDsOmitted = request.CandidateIDs == nil
	request.nameOverridesOmitted = request.NameOverrides == nil
	if request.FolderID != nil {
		folderID := strings.TrimSpace(*request.FolderID)
		request.FolderID = &folderID
	}
	if request.CandidateIDs != nil {
		if len(request.CandidateIDs) == 0 {
			return errors.New("candidate_ids must be omitted to select every candidate or contain at least one id")
		}
		for index := range request.CandidateIDs {
			request.CandidateIDs[index] = strings.TrimSpace(request.CandidateIDs[index])
		}
	}
	acknowledged := make(map[string]struct{}, len(request.AcknowledgedFindingIDs))
	for index := range request.AcknowledgedFindingIDs {
		id := strings.TrimSpace(request.AcknowledgedFindingIDs[index])
		if id == "" {
			return errors.New("acknowledged_finding_ids cannot contain an empty id")
		}
		if _, duplicate := acknowledged[id]; duplicate {
			return fmt.Errorf("acknowledged_finding_ids contains duplicate id %q", id)
		}
		acknowledged[id] = struct{}{}
		request.AcknowledgedFindingIDs[index] = id
	}
	var err error
	request.NameOverrides, err = normalizeBrunoNativeStringMap("name_overrides", request.NameOverrides)
	if err != nil {
		return err
	}
	request.SecretBindings, err = normalizeBrunoNativeStringMap("secret_bindings", request.SecretBindings)
	if err != nil {
		return err
	}
	for requirementID, rawName := range request.SecretBindings {
		name, normalizeErr := engine.NormalizeLogicalSecretName(rawName)
		if normalizeErr != nil || !logicalSecretNamePattern.MatchString(name) {
			return fmt.Errorf("secret binding for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", requirementID)
		}
		request.SecretBindings[requirementID] = name
	}
	return nil
}

func normalizeBrunoNativeStringMap(field string, values map[string]string) (map[string]string, error) {
	if values == nil {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(values))
	for rawKey, rawValue := range values {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return nil, fmt.Errorf("%s cannot contain an empty key", field)
		}
		if _, duplicate := out[key]; duplicate {
			return nil, fmt.Errorf("%s contains keys that normalize to the same value %q", field, key)
		}
		out[key] = strings.TrimSpace(rawValue)
	}
	return out, nil
}

func validateBrunoNativeSecretBindingKeys(requirements []brunoconvert.SecretRequirement, bindings map[string]string) error {
	if len(bindings) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(requirements))
	for _, requirement := range requirements {
		id := strings.TrimSpace(requirement.ID)
		if id == "" {
			return errors.New("native Bruno inspection contains a secret requirement without an id")
		}
		if _, duplicate := known[id]; duplicate {
			return fmt.Errorf("native Bruno inspection contains duplicate secret requirement id %q", id)
		}
		known[id] = struct{}{}
	}
	for id, name := range bindings {
		if _, exists := known[id]; !exists {
			return fmt.Errorf("secret binding requirement %q is stale, unknown, or not selected; inspect the source again", id)
		}
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("secret binding for %q cannot be empty", id)
		}
	}
	return nil
}

func canonicalizeBrunoNativeSelectedRequest(
	request brunoCommitRequest,
	selected []brunoconvert.NativeScriptCandidate,
	scripts []*dsl.Script,
) brunoCommitRequest {
	request.CandidateIDs = make([]string, 0, len(selected))
	request.NameOverrides = make(map[string]string, len(selected))
	for index, candidate := range selected {
		candidateID := strings.TrimSpace(candidate.CandidateID)
		request.CandidateIDs = append(request.CandidateIDs, candidateID)
		if index < len(scripts) && scripts[index] != nil {
			request.NameOverrides[candidateID] = strings.TrimSpace(scripts[index].Name)
		}
	}
	return request
}

func canonicalizeBrunoNativeReceiptRequest(
	request brunoCommitRequest,
	receipt *storage.BrunoNativeImportCommitResult,
) (brunoCommitRequest, error) {
	if receipt == nil || receipt.Batch == nil ||
		len(receipt.Batch.CandidateIDs) == 0 ||
		len(receipt.Batch.CandidateIDs) != len(receipt.Scripts) {
		return brunoCommitRequest{}, errors.New("native Bruno receipt is incomplete")
	}
	namesByCandidate := make(map[string]string, len(receipt.Batch.CandidateIDs))
	for index, rawCandidateID := range receipt.Batch.CandidateIDs {
		candidateID := strings.TrimSpace(rawCandidateID)
		if candidateID == "" || receipt.Scripts[index] == nil {
			return brunoCommitRequest{}, errors.New("native Bruno receipt is incomplete")
		}
		if _, duplicate := namesByCandidate[candidateID]; duplicate {
			return brunoCommitRequest{}, errors.New("native Bruno receipt contains duplicate candidates")
		}
		namesByCandidate[candidateID] = strings.TrimSpace(receipt.Scripts[index].Name)
	}
	if request.CandidateIDs == nil {
		request.CandidateIDs = append([]string(nil), receipt.Batch.CandidateIDs...)
	}
	effectiveNames := make(map[string]string, len(request.CandidateIDs))
	seen := make(map[string]struct{}, len(request.CandidateIDs))
	for _, rawCandidateID := range request.CandidateIDs {
		candidateID := strings.TrimSpace(rawCandidateID)
		storedName, exists := namesByCandidate[candidateID]
		if !exists {
			return brunoCommitRequest{}, errors.New("native Bruno retry candidate does not belong to the receipt")
		}
		if _, duplicate := seen[candidateID]; duplicate {
			return brunoCommitRequest{}, errors.New("native Bruno retry contains duplicate candidates")
		}
		seen[candidateID] = struct{}{}
		if override, overridden := request.NameOverrides[candidateID]; overridden {
			effectiveNames[candidateID] = strings.TrimSpace(override)
		} else {
			effectiveNames[candidateID] = storedName
		}
	}
	for candidateID := range request.NameOverrides {
		if _, selected := seen[candidateID]; !selected {
			return brunoCommitRequest{}, errors.New("native Bruno retry contains an unselected name override")
		}
	}
	request.NameOverrides = effectiveNames
	return request, nil
}

func planBrunoNativeSecretBindings(
	requirements []brunoconvert.SecretRequirement,
	requested map[string]string,
	migrate bool,
) (map[string]string, map[string]string, error) {
	grouped, err := groupedBrunoRequestedBindings(requirements, requested)
	if err != nil {
		return nil, nil, err
	}
	bindings := make(map[string]string, len(requirements))
	replacements := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		requirementID := strings.TrimSpace(requirement.ID)
		if requirementID == "" {
			return nil, nil, errors.New("native Bruno secret requirement has no id")
		}
		suggested := strings.TrimSpace(requirement.SuggestedName)
		if suggested == "" {
			return nil, nil, fmt.Errorf("native Bruno secret requirement %q has no suggested name", requirement.ID)
		}
		name := grouped[suggested]
		if name == "" && migrate && brunoImportCredentialValueUsable(requirement.Value) {
			name = suggested
		}
		if name == "" && requirement.ExistingReference {
			name = suggested
		}
		if name == "" {
			if brunoImportCredentialValueUsable(requirement.Value) {
				replacements[requirementID] = requirement.Value
			}
			// Existing governed references and unresolved source expressions
			// already remain in the candidate. They require no rewrite here.
			continue
		}
		name, err = engine.NormalizeLogicalSecretName(name)
		if err != nil || !logicalSecretNamePattern.MatchString(name) {
			return nil, nil, fmt.Errorf("secret binding for %q must contain 1-249 letters, digits, underscores, dots, or hyphens", requirement.ID)
		}
		reference := "{{secret." + name + "}}"
		bindings[suggested] = reference
		replacements[requirementID] = reference
	}
	return bindings, replacements, nil
}

func equalBrunoNativeSecretBindings(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func equalBrunoNativeCandidateSelection(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	selected := make(map[string]struct{}, len(left))
	for _, rawID := range left {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return false
		}
		if _, duplicate := selected[id]; duplicate {
			return false
		}
		selected[id] = struct{}{}
	}
	for _, rawID := range right {
		id := strings.TrimSpace(rawID)
		if _, exists := selected[id]; !exists {
			return false
		}
	}
	return true
}

func brunoNativeOverridesMatchReceipt(
	overrides map[string]string,
	receipt *storage.BrunoNativeImportCommitResult,
) bool {
	if len(overrides) == 0 {
		return true
	}
	if receipt == nil || receipt.Batch == nil ||
		len(receipt.Batch.CandidateIDs) != len(receipt.Scripts) {
		return false
	}
	names := make(map[string]string, len(receipt.Batch.CandidateIDs))
	for index, rawID := range receipt.Batch.CandidateIDs {
		if receipt.Scripts[index] == nil {
			return false
		}
		names[strings.TrimSpace(rawID)] = strings.TrimSpace(receipt.Scripts[index].Name)
	}
	for rawID, rawName := range overrides {
		if names[strings.TrimSpace(rawID)] != strings.TrimSpace(rawName) {
			return false
		}
	}
	return true
}

// brunoNativeCommitCurrentDigestProfile is the profile new receipts are written
// under. Replays must use the profile recorded on the receipt instead.
const brunoNativeCommitCurrentDigestProfile = storage.ImportBatchDigestBrunoNativeCommitV2

func brunoNativeCommitRequestSHA(request brunoCommitRequest) (string, error) {
	return brunoNativeCommitRequestSHAForProfile(brunoNativeCommitCurrentDigestProfile, request)
}

func brunoNativeCommitRequestSHAForProfile(profile string, request brunoCommitRequest) (string, error) {
	switch strings.TrimSpace(profile) {
	case storage.ImportBatchDigestBrunoNativeCommitV1:
		return brunoNativeCommitRequestSHAV1(request)
	case storage.ImportBatchDigestBrunoNativeCommitV2:
		return brunoNativeCommitRequestSHAV2(request)
	default:
		return "", fmt.Errorf("unsupported Bruno native commit request digest profile %q", profile)
	}
}

// brunoNativeCommitRequestSHAV1 is immutable. Its exact JSON shape, field
// ordering, normalization, and omission-equivalence flags are frozen by a
// digest golden. Add a new profile instead of changing this implementation.
func brunoNativeCommitRequestSHAV1(request brunoCommitRequest) (string, error) {
	if strings.TrimSpace(request.ImportSessionID) == "" {
		return "", errors.New("import_session_id is required")
	}
	candidateIDs := append([]string(nil), request.CandidateIDs...)
	for index := range candidateIDs {
		candidateIDs[index] = strings.TrimSpace(candidateIDs[index])
	}
	sort.Strings(candidateIDs)
	acknowledged := append([]string(nil), request.AcknowledgedFindingIDs...)
	for index := range acknowledged {
		acknowledged[index] = strings.TrimSpace(acknowledged[index])
	}
	sort.Strings(acknowledged)
	folderID := ""
	if request.FolderID != nil {
		folderID = strings.TrimSpace(*request.FolderID)
	}
	migrate := request.MigrateSecrets != nil && *request.MigrateSecrets
	payload, err := json.Marshal(struct {
		ProjectID              string            `json:"project_id"`
		ImportSessionID        string            `json:"import_session_id"`
		Mode                   string            `json:"mode"`
		FolderID               string            `json:"folder_id"`
		CandidateIDs           []string          `json:"candidate_ids"`
		NameOverrides          map[string]string `json:"name_overrides"`
		SecretBindings         map[string]string `json:"secret_bindings"`
		MigrateSecrets         bool              `json:"migrate_secrets"`
		AcknowledgedFindingIDs []string          `json:"acknowledged_finding_ids"`
		CandidateIDsOmitted    bool              `json:"candidate_ids_omitted"`
		NameOverridesOmitted   bool              `json:"name_overrides_omitted"`
	}{
		ProjectID:              normalizeProjectID(request.ProjectID),
		ImportSessionID:        strings.TrimSpace(request.ImportSessionID),
		Mode:                   "create_native_batch",
		FolderID:               folderID,
		CandidateIDs:           candidateIDs,
		NameOverrides:          request.NameOverrides,
		SecretBindings:         request.SecretBindings,
		MigrateSecrets:         migrate,
		AcknowledgedFindingIDs: acknowledged,
		CandidateIDsOmitted:    request.candidateIDsOmitted,
		NameOverridesOmitted:   request.nameOverridesOmitted,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:]), nil
}

// brunoNativeCommitRequestSHAV2 extends V1 with shared_values. V1 is immutable
// and frozen by a golden, so this is a new profile rather than an edit: without
// it, two commits differing only in the operator's collection-wide values hash
// identically, and the replay path returns the first commit's batch while
// reporting success for the second.
//
// Like V1, this implementation is frozen once receipts exist under it.
func brunoNativeCommitRequestSHAV2(request brunoCommitRequest) (string, error) {
	if strings.TrimSpace(request.ImportSessionID) == "" {
		return "", errors.New("import_session_id is required")
	}
	candidateIDs := append([]string(nil), request.CandidateIDs...)
	for index := range candidateIDs {
		candidateIDs[index] = strings.TrimSpace(candidateIDs[index])
	}
	sort.Strings(candidateIDs)
	acknowledged := append([]string(nil), request.AcknowledgedFindingIDs...)
	for index := range acknowledged {
		acknowledged[index] = strings.TrimSpace(acknowledged[index])
	}
	sort.Strings(acknowledged)
	folderID := ""
	if request.FolderID != nil {
		folderID = strings.TrimSpace(*request.FolderID)
	}
	migrate := request.MigrateSecrets != nil && *request.MigrateSecrets
	payload, err := json.Marshal(struct {
		ProjectID              string            `json:"project_id"`
		ImportSessionID        string            `json:"import_session_id"`
		Mode                   string            `json:"mode"`
		FolderID               string            `json:"folder_id"`
		CandidateIDs           []string          `json:"candidate_ids"`
		NameOverrides          map[string]string `json:"name_overrides"`
		SecretBindings         map[string]string `json:"secret_bindings"`
		SharedValues           map[string]string `json:"shared_values"`
		MigrateSecrets         bool              `json:"migrate_secrets"`
		AcknowledgedFindingIDs []string          `json:"acknowledged_finding_ids"`
		CandidateIDsOmitted    bool              `json:"candidate_ids_omitted"`
		NameOverridesOmitted   bool              `json:"name_overrides_omitted"`
	}{
		ProjectID:              normalizeProjectID(request.ProjectID),
		ImportSessionID:        strings.TrimSpace(request.ImportSessionID),
		Mode:                   "create_native_batch",
		FolderID:               folderID,
		CandidateIDs:           candidateIDs,
		NameOverrides:          request.NameOverrides,
		SecretBindings:         request.SecretBindings,
		SharedValues:           request.SharedValues,
		MigrateSecrets:         migrate,
		AcknowledgedFindingIDs: acknowledged,
		CandidateIDsOmitted:    request.candidateIDsOmitted,
		NameOverridesOmitted:   request.nameOverridesOmitted,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%x", sum[:]), nil
}

func writeBrunoSecretPartialFailure(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	projectID string,
	sessionID string,
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
		"session_retained":              sessionRetained,
		"manual_retry_safe":             sessionRetained,
		"source_values_cleared":         !sessionRetained,
		"manual_configuration_required": manualConfigurationRequired,
		"committed_secrets_retained":    len(migrated) > 0,
		"resolution":                    resolution,
	}
	if sessionRetained && strings.TrimSpace(sessionID) != "" {
		payload["idempotency_session_id"] = strings.TrimSpace(sessionID)
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
	case errors.Is(err, storage.ErrBrunoImportConcurrentChange), errors.Is(err, storage.ErrBrunoNativeBatchConflict):
		return http.StatusConflict
	case errors.Is(err, storage.ErrBrunoRevisionNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func brunoCommitErrorReason(err error) string {
	switch {
	case errors.Is(err, storage.ErrBrunoImportConcurrentChange), errors.Is(err, storage.ErrBrunoNativeBatchConflict):
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
	script, err := getPublishedImportScript(s.store, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	// No protocol gate. Retained source is no longer Bruno-only: any import
	// routed through a protected session can keep its raw upload, and refusing
	// by protocol here would make those revisions unreachable even though they
	// exist and are permission-checked below. The active-revision lookup is the
	// real gate -- a script with nothing retained still 404s a few lines down,
	// which is the same answer the protocol check used to give.
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
	if !raw && !revision.SanitizedAvailable {
		// The revision retains source raw-only: sanitized_artifact_id is a second
		// reference to the raw artifact, not a scrubbed copy. Serving it under the
		// sanitized gate would hand unredacted source to every editor, which is
		// precisely the disclosure this flag exists to prevent. Callers entitled
		// to the raw bundle can still ask for it explicitly and be role-checked
		// above.
		s.recordAuditEvent(r, "script.import.bruno.source.sanitized", "denied", "script", script.ID, projectID, map[string]any{"reason": "no_sanitized_artifact"})
		writeError(w, http.StatusConflict, "this import retains raw source only; request the raw bundle, which requires project_admin or system_admin permission")
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
	reader, size, err := objects.Open(r.Context(), importartifact.OpenRequest{
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
	script, err := getPublishedImportScript(s.store, r.PathValue("id"))
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
	script, err := getPublishedImportScript(s.store, r.PathValue("id"))
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
	if !isBrunoRollbackRevision(targetRevision) {
		s.recordAuditEvent(r, "script.import.rollback", "blocked", "script", script.ID, script.ProjectID, map[string]any{
			"revision_id": targetRevisionID,
			"reason":      "unsupported_source_format",
		})
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "rollback is currently supported only for Bruno source revisions",
			"code":  "import_rollback_unsupported_source",
		})
		return
	}
	// Activation updates the script config directly inside the revision
	// transaction. Validate the immutable canonical snapshot at the same final
	// boundary as current imports before allowing a pre-upgrade revision to
	// become executable. A successful projection cannot differ in executable
	// config: any credential that projection would redact is rejected as an
	// unresolved binding before activation.
	if _, err := preparePersistentImportedLibraryScript(
		targetRevision.CanonicalScript,
		script,
		"bruno",
	); err != nil {
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

func isBrunoRollbackRevision(revision *storage.BrunoSourceRevision) bool {
	if revision == nil || revision.CanonicalScript == nil {
		return false
	}
	script := revision.CanonicalScript
	if script.Metadata != nil && script.Metadata.Import != nil {
		source := strings.TrimSpace(script.Metadata.Import.Source)
		if source != "" {
			return strings.EqualFold(source, "bruno")
		}
	}
	// Canonical Bruno revisions predating import metadata used the Bruno
	// protocol as their durable source identity. Generic retained HTTP imports
	// never do, so this preserves legacy rollback without making their
	// format-neutral revisions eligible for the Bruno-only activation routine.
	return strings.EqualFold(strings.TrimSpace(script.Protocol), "bruno")
}

func (s *Server) handlePreviewBrunoProjection(w http.ResponseWriter, r *http.Request) {
	brunoStore, ok := s.store.(storage.BrunoImportStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Bruno revision storage is not configured")
		return
	}
	script, err := getPublishedImportScript(s.store, r.PathValue("id"))
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

func (s *Server) brunoArtifactStore() (*importartifact.EncryptedStore, error) {
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
	custody := brunoKeyCustodyOperator
	if wrapper == nil {
		var err error
		wrapper, custody, err = s.brunoArtifactKeyWrapper(root)
		if err != nil {
			return nil, markProtectedImportStoreUnavailable(err)
		}
	}
	objects, err := importartifact.NewEncryptedStore(importartifact.StoreOptions{
		RootDir:         root,
		Wrapper:         wrapper,
		SessionCapacity: brunoImportSessionLimit,
	})
	if err != nil {
		return nil, markProtectedImportStoreUnavailable(err)
	}
	s.brunoArtifacts = objects
	s.brunoArtifactKeyCustody = custody
	if metadata, ok := s.store.(importartifact.ArtifactMetadataReader); ok && metadata != nil {
		importartifact.SetResolver(&importartifact.StoreResolver{Objects: s.brunoArtifacts, Metadata: metadata})
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
		if err := mergeBrunoOutputModeQuery(&input.Options, r); err != nil {
			return brunoconvert.Input{}, "", err
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
	var options brunoconvert.InspectOptions
	if rawOptions := strings.TrimSpace(r.URL.Query().Get("options")); rawOptions != "" {
		if err := json.Unmarshal([]byte(rawOptions), &options); err != nil {
			return brunoconvert.Input{}, "", fmt.Errorf("invalid Bruno options JSON: %w", err)
		}
	}
	if err := mergeBrunoOutputModeQuery(&options, r); err != nil {
		return brunoconvert.Input{}, "", err
	}
	return brunoconvert.Input{
		Name:      strings.TrimSpace(r.URL.Query().Get("name")),
		ProjectID: strings.TrimSpace(r.URL.Query().Get("project_id")),
		Filename:  filename,
		Content:   data,
		Sandbox:   strings.TrimSpace(r.URL.Query().Get("sandbox")),
		RootPath:  strings.TrimSpace(r.URL.Query().Get("root_path")),
		Options:   options,
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
	if err := mergeBrunoOutputModeQuery(&options, r); err != nil {
		return brunoconvert.Input{}, "", err
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

func mergeBrunoOutputModeQuery(options *brunoconvert.InspectOptions, r *http.Request) error {
	if options == nil || r == nil {
		return nil
	}
	outputMode := strings.TrimSpace(r.URL.Query().Get("output_mode"))
	if outputMode == "" {
		return nil
	}
	if options.OutputMode != "" && !strings.EqualFold(strings.TrimSpace(options.OutputMode), outputMode) {
		return errors.New("Bruno output_mode conflicts with options.output_mode")
	}
	options.OutputMode = outputMode
	return nil
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

type brunoImportSessionClaimLease struct {
	objects   *importartifact.EncryptedStore
	sessionID string
	projectID string
	actor     string
	token     string
	done      chan struct{}
	stopOnce  sync.Once
	errMu     sync.Mutex
	lostErr   error
}

func startBrunoImportSessionClaimLease(
	objects *importartifact.EncryptedStore,
	sessionID, projectID, actor, token string,
) *brunoImportSessionClaimLease {
	lease := &brunoImportSessionClaimLease{
		objects: objects, sessionID: strings.TrimSpace(sessionID),
		projectID: normalizeProjectID(projectID), actor: strings.TrimSpace(actor),
		token: strings.TrimSpace(token), done: make(chan struct{}),
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-lease.done:
				return
			case now := <-ticker.C:
				if err := lease.touch(now); err != nil {
					return
				}
			}
		}
	}()
	return lease
}

func (l *brunoImportSessionClaimLease) touch(now time.Time) error {
	if l == nil || l.objects == nil {
		return errors.New("Bruno import session claim lease is unavailable")
	}
	l.errMu.Lock()
	if l.lostErr != nil {
		err := l.lostErr
		l.errMu.Unlock()
		return err
	}
	l.errMu.Unlock()
	if err := l.objects.TouchSessionClaim(
		l.sessionID, l.projectID, l.actor, l.token, now.UTC(),
	); err != nil {
		l.errMu.Lock()
		if l.lostErr == nil {
			l.lostErr = fmt.Errorf("Bruno import session claim lease lost: %w", err)
		}
		lostErr := l.lostErr
		l.errMu.Unlock()
		return lostErr
	}
	return nil
}

func (l *brunoImportSessionClaimLease) assertOwned() error {
	return l.touch(time.Now().UTC())
}

func (l *brunoImportSessionClaimLease) stop() {
	if l == nil {
		return
	}
	l.stopOnce.Do(func() { close(l.done) })
}

func (s *Server) claimBrunoImportSession(
	r *http.Request,
	id, projectID string,
) (*brunoImportSession, *importartifact.EncryptedStore, string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil, "", "", errors.New("import_session_id is required")
	}
	projectID = normalizeProjectID(projectID)
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return nil, nil, "", "", err
	}
	_, actor, _ := s.resolveAuditActor(r)
	payload, descriptor, claimErr := objects.ClaimSession(
		r.Context(), id, projectID, actor, brunoImportSessionMaxBytes,
	)
	if claimErr != nil {
		if errors.Is(claimErr, os.ErrNotExist) {
			return nil, objects, actor, "", sql.ErrNoRows
		}
		return nil, objects, actor, "", claimErr
	}
	claimToken := descriptor.ClaimToken
	releaseClaim := true
	defer func() {
		if releaseClaim {
			_ = objects.ReleaseSessionClaim(id, projectID, actor, claimToken)
		}
	}()

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
		return nil, objects, actor, claimToken, decodeErr
	}
	if normalizeProjectID(session.ProjectID) != projectID {
		s.releaseBrunoImportSession(session, false, true)
		return nil, objects, actor, claimToken, errors.New("Bruno import session belongs to another project")
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		s.releaseBrunoImportSession(session, false, true)
		return nil, objects, actor, claimToken, errors.New("Bruno import session expired")
	}
	if session.Actor != actor {
		s.releaseBrunoImportSession(session, false, true)
		return nil, objects, actor, claimToken, errors.New("Bruno import session belongs to another actor")
	}
	releaseClaim = false
	return session, objects, actor, claimToken, nil
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
	// Bruno's own store, including one opened with the local key, still owns
	// this project's retained source and must release it.
	present, err := s.brunoRetentionStorePresent()
	if err != nil || !present {
		return err
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return err
	}
	return objects.DeleteProject(context.Background(), projectID)
}

// brunoArtifactStorePresent reports whether the SHARED protected-import
// facility is available — the one every importer's candidate sessions,
// attestations, and source retention are gated on.
//
// It stays keyed on operator-managed custody. The Bruno-scoped local key
// (brunoSourceRetentionKeyCustody) exists so Bruno import works on an
// unconfigured controller; it must not silently switch other importers from
// their established client-submitted flow to the protected flow, which changes
// their capability and restart-recovery semantics.
func (s *Server) brunoArtifactStorePresent() (bool, error) {
	if s == nil {
		return false, nil
	}
	if s.brunoArtifactWrapper != nil || strings.TrimSpace(s.brunoArtifactDir) != "" || s.desktopMode {
		return true, nil
	}
	if strings.TrimSpace(os.Getenv(keywrap.MasterKeyEnvVar)) != "" || strings.TrimSpace(os.Getenv(keywrap.KeyVaultKeyIDEnvVar)) != "" {
		return true, nil
	}
	if s.brunoProspectiveKeyCustody() == brunoKeyCustodyLocal {
		// Only Bruno's own local key is available. An artifact directory left
		// by a Bruno import under that key is not an operator-managed
		// protected store, and treating it as one previously also meant the
		// shared paths opened a store they had no key for.
		return false, nil
	}
	if s.brunoArtifacts != nil {
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
	// Sessions and artifacts written under the local key still expire and must
	// still be swept, so this uses Bruno's own availability, not the shared
	// protected-import predicate.
	present, err := s.brunoRetentionStorePresent()
	if err != nil || !present {
		return err
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return err
	}
	unlockSourcePublication, err := objects.LockSourcePublication()
	if err != nil {
		return err
	}
	defer func() { _ = unlockSourcePublication() }()
	expired, sweepErr := objects.DeleteExpiredSessions(context.Background(), now.UTC())
	checker, canCheckDatabaseReferences := s.store.(storage.BrunoArtifactReferenceChecker)
	var cleanupErrors []error
	if sweepErr != nil {
		cleanupErrors = append(cleanupErrors, sweepErr)
	}
	publications, publicationErr := objects.ListExpiredPublications(now.UTC())
	if publicationErr != nil {
		cleanupErrors = append(cleanupErrors, publicationErr)
	}
	for _, publication := range publications {
		publicationCleanupOK := true
		for _, request := range publication.Objects {
			if strings.TrimSpace(request.ProjectID) != strings.TrimSpace(publication.ProjectID) ||
				(request.Kind != "raw" && request.Kind != "sanitized") {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("Bruno publication %q has invalid artifact metadata", publication.ID))
				publicationCleanupOK = false
				continue
			}
			sessionReferenced, sessionCheckErr := objects.IsSessionRelatedObjectReferenced(request.BackendKey, "")
			if sessionCheckErr != nil {
				cleanupErrors = append(cleanupErrors, sessionCheckErr)
				publicationCleanupOK = false
				continue
			}
			if sessionReferenced {
				continue
			}
			if !canCheckDatabaseReferences || checker == nil {
				publicationCleanupOK = false
				continue
			}
			referenced, checkErr := checker.IsBrunoArtifactBackendKeyReferenced(request.BackendKey)
			if checkErr != nil {
				cleanupErrors = append(cleanupErrors, checkErr)
				publicationCleanupOK = false
				continue
			}
			if referenced {
				continue
			}
			if deleteErr := objects.Delete(context.Background(), request); deleteErr != nil {
				cleanupErrors = append(cleanupErrors, deleteErr)
				publicationCleanupOK = false
			}
		}
		if publicationCleanupOK {
			if deleteErr := objects.DeletePublication(publication.ID, publication.ProjectID); deleteErr != nil {
				cleanupErrors = append(cleanupErrors, deleteErr)
			}
		}
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
			// Keep the descriptor as a durable reservation for a bounded period
			// after expiry. A concurrent identical inspection may have completed
			// its content-addressed Put but not yet published its own descriptor.
			if now.UTC().Before(descriptor.ExpiresAt.UTC().Add(brunoArtifactCollectionGrace)) {
				descriptorCleanupOK = false
				continue
			}
			if !canCheckDatabaseReferences || checker == nil {
				// A lightweight store can support imports without exposing the
				// optional reference checker. Unknown must fail closed: leaking
				// encrypted source is safer than deleting committed evidence.
				descriptorCleanupOK = false
				continue
			}
			referenced, checkErr := checker.IsBrunoArtifactBackendKeyReferenced(request.BackendKey)
			if checkErr != nil {
				cleanupErrors = append(cleanupErrors, checkErr)
				descriptorCleanupOK = false
				continue
			}
			if referenced {
				continue
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
	normalizeBrunoImportResultCredentialValues(session.Result)
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
		ID:                      session.ID,
		ProjectID:               session.ProjectID,
		Actor:                   session.Actor,
		Result:                  session.Result,
		RawArtifact:             session.RawArtifact,
		SanitizedArtifact:       session.SanitizedArtifact,
		RevisionID:              session.RevisionID,
		SyncScriptID:            session.SyncScriptID,
		NativeCredentialTargets: session.NativeCredentialTargets,
		CreatedAt:               session.CreatedAt,
		ExpiresAt:               session.ExpiresAt,
		SecretValues:            values,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	defer clear(data)
	return objects.SaveSessionBytes(ctx, importartifact.SessionDescriptor{
		ID:        session.ID,
		ProjectID: session.ProjectID,
		Actor:     session.Actor,
		ExpiresAt: session.ExpiresAt,
		RelatedObjects: []importartifact.OpenRequest{
			brunoArtifactOpenRequest(session.RawArtifact),
			brunoArtifactOpenRequest(session.SanitizedArtifact),
		},
	}, data, brunoImportSessionMaxBytes)
}

func brunoArtifactOpenRequest(artifact storage.BrunoSourceArtifact) importartifact.OpenRequest {
	return importartifact.OpenRequest{
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
		if !brunoImportCredentialValueUsable(requirement.Value) {
			continue
		}
		id := strings.TrimSpace(requirement.ID)
		if id == "" || id != requirement.ID {
			return nil, errors.New("Bruno source credential requirement has an invalid id")
		}
		if _, duplicate := values[id]; duplicate {
			return nil, fmt.Errorf("Bruno source credential requirement %q is duplicated", id)
		}
		entryBytes := len(id) + len(requirement.Value)
		if total > brunoImportSessionMaxBytes-entryBytes {
			return nil, fmt.Errorf("Bruno source credentials exceed %d-byte protected session limit", brunoImportSessionMaxBytes)
		}
		total += entryBytes
		values[id] = requirement.Value
	}
	return values, nil
}

func validatePersistedBrunoSecretValues(values map[string]string) error {
	total := 0
	for rawID, value := range values {
		id := strings.TrimSpace(rawID)
		if id == "" || id != rawID {
			return errors.New("persisted Bruno source credential has an invalid requirement id")
		}
		entryBytes := len(id) + len(value)
		if total > brunoImportSessionMaxBytes-entryBytes {
			return fmt.Errorf("persisted Bruno source credentials exceed %d-byte protected session limit", brunoImportSessionMaxBytes)
		}
		total += entryBytes
	}
	return nil
}

// Project-secret migration is optional and keeps the provider's tighter
// batching/value limits. Those limits must not govern the encrypted import
// session used to preserve exact runnable destination bytes.
func validateBrunoMigrationSecretValues(
	requirements []brunoconvert.SecretRequirement,
) error {
	values := make(map[string]string)
	total := 0
	for _, requirement := range requirements {
		if !brunoImportCredentialValueUsable(requirement.Value) {
			continue
		}
		id := strings.TrimSpace(requirement.ID)
		if id == "" || id != requirement.ID {
			return errors.New("Bruno source credential requirement has an invalid id")
		}
		if _, duplicate := values[id]; duplicate {
			return fmt.Errorf("Bruno source credential requirement %q is duplicated", id)
		}
		if len(values) >= projectSecretMaxBatchItems {
			return fmt.Errorf("Bruno source credentials exceed %d-entry migration limit", projectSecretMaxBatchItems)
		}
		if len(requirement.Value) > projectSecretMaxValueBytes {
			return fmt.Errorf("Bruno source credential exceeds %d-byte migration limit", projectSecretMaxValueBytes)
		}
		if total > sourceSecretSessionMaxTotalBytes-len(requirement.Value) {
			return fmt.Errorf("Bruno source credentials exceed %d-byte migration limit", sourceSecretSessionMaxTotalBytes)
		}
		total += len(requirement.Value)
		values[id] = requirement.Value
	}
	clearStringMap(values)
	return nil
}

func collectBrunoNativeCredentialTargets(
	result *brunoconvert.Result,
) map[string][]brunoconvert.NativeCredentialTarget {
	if result == nil || len(result.Scripts) == 0 {
		return nil
	}
	targets := make(map[string][]brunoconvert.NativeCredentialTarget)
	for _, candidate := range result.Scripts {
		if len(candidate.CredentialTargets) == 0 {
			continue
		}
		candidateID := strings.TrimSpace(candidate.CandidateID)
		targets[candidateID] = append(
			[]brunoconvert.NativeCredentialTarget(nil),
			candidate.CredentialTargets...,
		)
	}
	if len(targets) == 0 {
		return nil
	}
	return targets
}

func validatePersistedBrunoNativeCredentialTargets(
	result *brunoconvert.Result,
	targets map[string][]brunoconvert.NativeCredentialTarget,
) error {
	if len(targets) == 0 {
		return nil
	}
	if result == nil ||
		!strings.EqualFold(strings.TrimSpace(result.OutputMode), brunoconvert.OutputModeNativeRequests) {
		return errors.New("persisted Bruno credential targets require native request mode")
	}
	requirementsByCandidate := make(map[string]map[string]struct{}, len(result.Scripts))
	for _, candidate := range result.Scripts {
		candidateID := strings.TrimSpace(candidate.CandidateID)
		if candidateID == "" {
			return errors.New("persisted Bruno native candidate has no id")
		}
		allowed := make(map[string]struct{}, len(candidate.SecretRequirements))
		for _, requirement := range candidate.SecretRequirements {
			allowed[strings.TrimSpace(requirement.ID)] = struct{}{}
		}
		requirementsByCandidate[candidateID] = allowed
	}
	total := 0
	for rawCandidateID, candidateTargets := range targets {
		candidateID := strings.TrimSpace(rawCandidateID)
		allowed, exists := requirementsByCandidate[candidateID]
		if !exists || candidateID != rawCandidateID {
			return errors.New("persisted Bruno credential targets reference an unknown candidate")
		}
		seen := make(map[string]struct{}, len(candidateTargets))
		for _, target := range candidateTargets {
			total++
			if total > defaultBrunoImportFileCount {
				return errors.New("persisted Bruno credential targets exceed the session limit")
			}
			requirementID := strings.TrimSpace(target.RequirementID)
			if _, ok := allowed[requirementID]; !ok || requirementID != target.RequirementID {
				return errors.New("persisted Bruno credential target references an unknown requirement")
			}
			if target.JSONPointer == "" || len(target.JSONPointer) > 4096 {
				return errors.New("persisted Bruno credential target has an invalid JSON pointer")
			}
			tokens, err := splitJSONPointer(target.JSONPointer)
			if err != nil || !isAllowedBrunoNativeCredentialTargetPointer(tokens) {
				return errors.New("persisted Bruno credential target has an invalid JSON pointer")
			}
			if target.Occurrence < 0 || target.Occurrence > defaultBrunoImportFileCount {
				return errors.New("persisted Bruno credential target has an invalid occurrence")
			}
			if !isCanonicalLowerHexDigest(target.SanitizedSHA256, sha256.Size*2) {
				return errors.New("persisted Bruno credential target has an invalid digest")
			}
			identity := requirementID + "\x00" + target.JSONPointer + "\x00" +
				strconv.Itoa(target.Occurrence)
			if _, duplicate := seen[identity]; duplicate {
				return errors.New("persisted Bruno credential target is duplicated")
			}
			seen[identity] = struct{}{}
		}
	}
	return nil
}

func isAllowedBrunoNativeCredentialTargetPointer(tokens []string) bool {
	if len(tokens) == 2 && tokens[0] == "config" && tokens[1] == "base_url" {
		return true
	}
	if len(tokens) == 4 && tokens[0] == "config" && tokens[1] == "http" &&
		tokens[2] == "auth" {
		switch tokens[3] {
		case "username", "password", "token", "domain":
			return true
		}
	}
	if len(tokens) < 3 || tokens[0] != "actions" {
		return false
	}
	if _, err := strconv.Atoi(tokens[1]); err != nil {
		return false
	}
	switch {
	case len(tokens) == 3 && (tokens[2] == "target" || tokens[2] == "body"):
		return true
	case len(tokens) == 4 && tokens[2] == "headers" && tokens[3] != "":
		return true
	case len(tokens) == 5 && tokens[2] == "header_list" && tokens[4] == "value":
		_, err := strconv.Atoi(tokens[3])
		return err == nil
	case len(tokens) == 4 && tokens[2] == "auth":
		switch tokens[3] {
		case "username", "password", "token", "domain":
			return true
		}
	}
	return false
}

func decodePersistedBrunoImportSession(id string, payload []byte) (*brunoImportSession, error) {
	var envelope persistedBrunoImportSession
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	session := &brunoImportSession{
		ID:                      id,
		ProjectID:               envelope.ProjectID,
		Actor:                   envelope.Actor,
		Result:                  envelope.Result,
		RawArtifact:             envelope.RawArtifact,
		SanitizedArtifact:       envelope.SanitizedArtifact,
		RevisionID:              envelope.RevisionID,
		SyncScriptID:            envelope.SyncScriptID,
		NativeCredentialTargets: envelope.NativeCredentialTargets,
		CreatedAt:               envelope.CreatedAt,
		ExpiresAt:               envelope.ExpiresAt,
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
	if err := validatePersistedBrunoNativeCredentialTargets(
		envelope.Result,
		envelope.NativeCredentialTargets,
	); err != nil {
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
	normalizeBrunoImportResultCredentialValues(envelope.Result)
	return session, nil
}

func (s *Server) deletePersistedBrunoImportSession(session *brunoImportSession) {
	if session == nil || s.brunoArtifacts == nil {
		return
	}
	_ = s.brunoArtifacts.DeleteSession(context.Background(), session.ID, session.ProjectID)
}

func (s *Server) finalizeCommittedBrunoImportSession(id, projectID, actor string) {
	objects, err := s.brunoArtifactStore()
	if err != nil {
		slog.Warn("open Bruno artifact store for committed-session cleanup", "session_id", id, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := objects.FinalizeCommittedSession(ctx, id, projectID, actor); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		slog.Warn("finalize committed Bruno import session", "session_id", id, "error", err)
	}
	s.brunoImportMu.Lock()
	session := s.brunoImportSessions[id]
	delete(s.brunoImportSessions, id)
	s.brunoImportMu.Unlock()
	s.releaseBrunoImportSession(session, false, false)
}

func (s *Server) cleanupBrunoImportSessionArtifacts(session *brunoImportSession) {
	if session == nil {
		return
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		return
	}
	unlockSourcePublication, err := objects.LockSourcePublication()
	if err != nil {
		return
	}
	defer func() { _ = unlockSourcePublication() }()
	if cleanupErr := s.cleanupUnreferencedBrunoArtifactsLocked(objects, []storage.BrunoSourceArtifact{
		session.RawArtifact,
		session.SanitizedArtifact,
	}, session.ID); cleanupErr != nil {
		slog.Warn("clean Bruno import session artifacts", "session_id", session.ID, "error", cleanupErr)
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
	unlockSourcePublication, err := objects.LockSourcePublication()
	if err != nil {
		return
	}
	defer func() { _ = unlockSourcePublication() }()
	if cleanupErr := s.cleanupUnreferencedBrunoArtifactsLocked(objects, artifacts, ""); cleanupErr != nil {
		slog.Warn("clean unreferenced Bruno artifacts", "error", cleanupErr)
	}
}

func (s *Server) cleanupUnreferencedBrunoArtifactsLocked(
	objects *importartifact.EncryptedStore,
	artifacts []storage.BrunoSourceArtifact,
	excludedSessionID string,
) error {
	if objects == nil || len(artifacts) == 0 {
		return nil
	}
	checker, ok := s.store.(storage.BrunoArtifactReferenceChecker)
	if !ok || checker == nil {
		// Content-addressed objects may already belong to a committed revision.
		// Leak-safe cleanup is preferable to deleting shared protected source
		// when the configured store cannot prove the object is unreferenced.
		return errors.New("Bruno artifact reference checking is unavailable")
	}
	var cleanupErrors []error
	for _, artifact := range artifacts {
		referenced, checkErr := checker.IsBrunoArtifactBackendKeyReferenced(artifact.BackendKey)
		if checkErr != nil {
			cleanupErrors = append(cleanupErrors, checkErr)
			continue
		}
		if referenced {
			continue
		}
		referencedBySession, sessionErr := objects.IsSessionRelatedObjectReferenced(
			artifact.BackendKey, excludedSessionID,
		)
		if sessionErr != nil {
			cleanupErrors = append(cleanupErrors, sessionErr)
			continue
		}
		if referencedBySession {
			continue
		}
		if deleteErr := objects.Delete(context.Background(), brunoArtifactOpenRequest(artifact)); deleteErr != nil {
			cleanupErrors = append(cleanupErrors, deleteErr)
		}
	}
	return errors.Join(cleanupErrors...)
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
	existing, err := getPublishedImportScript(s.store, strings.TrimSpace(scriptID))
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

func brunoRequirementsHaveSourceValues(requirements []brunoconvert.SecretRequirement) bool {
	for _, requirement := range requirements {
		if brunoImportCredentialValueUsable(requirement.Value) {
			return true
		}
	}
	return false
}

func brunoImportCredentialValueUsable(value string) bool {
	_, usable := credentialpolicy.RecoverableSourceCredentialValue(value)
	return usable
}

func normalizeBrunoImportCredentialValues(requirements []brunoconvert.SecretRequirement) {
	for index := range requirements {
		requirement := &requirements[index]
		if requirement.Value == "" || brunoImportCredentialValueUsable(requirement.Value) {
			continue
		}
		requirement.Value = ""
		requirement.SourceValueAvailable = false
		requirement.DestinationValuePreserved = false
		requirement.PreservationUnavailableReason = brunoconvert.PreservationUnavailableSourceValue
	}
}

func normalizeBrunoImportResultCredentialValues(result *brunoconvert.Result) {
	if result == nil {
		return
	}
	unavailable := make(map[string]struct{})
	for _, requirement := range result.SecretRequirements {
		if requirement.Value != "" && !brunoImportCredentialValueUsable(requirement.Value) {
			unavailable[requirement.ID] = struct{}{}
		}
	}
	normalizeBrunoImportCredentialValues(result.SecretRequirements)
	for index := range result.Scripts {
		normalizeBrunoImportCredentialValues(result.Scripts[index].SecretRequirements)
		for requirementIndex := range result.Scripts[index].SecretRequirements {
			requirement := &result.Scripts[index].SecretRequirements[requirementIndex]
			if _, sourceUnavailable := unavailable[requirement.ID]; !sourceUnavailable {
				continue
			}
			requirement.SourceValueAvailable = false
			requirement.DestinationValuePreserved = false
			requirement.PreservationUnavailableReason = brunoconvert.PreservationUnavailableSourceValue
		}
	}
}

func (s *Server) authorizeBrunoSourceSecretMigration(
	w http.ResponseWriter,
	r *http.Request,
	projectID string,
	requested bool,
) bool {
	if !requested {
		return true
	}
	if s.sourceSecretMigrationAuthorized(r, projectID) {
		return true
	}
	// This response intentionally contains no requirement names, source paths,
	// candidate details, or values. Callers reach it only after proving
	// ownership of the actor/project-bound import session (or committed native
	// receipt), preserving the import flow's anti-enumeration ordering.
	writeError(w, http.StatusForbidden, "source secret migration is not allowed")
	return false
}

func brunoCanonicalPreservesSourceCredentials(
	requirements []brunoconvert.SecretRequirement,
	requested map[string]string,
	existing *dsl.Script,
	migrate bool,
) (bool, error) {
	if migrate {
		return false, nil
	}
	grouped, err := groupedBrunoRequestedBindings(requirements, requested)
	if err != nil {
		return false, err
	}
	existingLogical := map[string]string{}
	if existing != nil {
		existingLogical = logicalBrunoSecretBindings(existing.Config.Bruno.SecretBindings)
	}
	preserved := false
	for _, requirement := range requirements {
		if !brunoImportCredentialValueUsable(requirement.Value) {
			continue
		}
		suggested := strings.TrimSpace(requirement.SuggestedName)
		if grouped[suggested] != "" || strings.TrimSpace(existingLogical[suggested]) != "" {
			continue
		}
		preserved = true
	}
	return preserved, nil
}

func brunoSecretMatchesRetainedMigration(
	existingValue string,
	sourceValue string,
	metadata secretstore.Metadata,
	actor string,
	window brunoSecretMigrationWindow,
) bool {
	if !brunoImportCredentialValueUsable(sourceValue) ||
		strings.TrimSpace(metadata.CreatedBy) == "" ||
		strings.TrimSpace(metadata.CreatedBy) != strings.TrimSpace(actor) ||
		metadata.CreatedAt.IsZero() ||
		window.StartedAt.IsZero() ||
		window.ExpiresAt.IsZero() {
		return false
	}
	// Allow a small provider/controller timestamp skew while still tying the
	// automatic reconciliation to this one protected import session.
	const clockSkew = time.Minute
	createdAt := metadata.CreatedAt.UTC()
	if createdAt.Before(window.StartedAt.UTC().Add(-clockSkew)) ||
		createdAt.After(window.ExpiresAt.UTC().Add(clockSkew)) {
		return false
	}
	return len(existingValue) == len(sourceValue) &&
		subtle.ConstantTimeCompare([]byte(existingValue), []byte(sourceValue)) == 1
}

func (s *Server) bindOrMigrateBrunoImportSecrets(
	r *http.Request,
	requirements []brunoconvert.SecretRequirement,
	requested map[string]string,
	existing *dsl.Script,
	projectID string,
	actor string,
	migrate bool,
	migrationWindow brunoSecretMigrationWindow,
	assertMutationLease func() error,
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
		if !requestedExplicitly && !migrate && brunoImportCredentialValueUsable(requirement.Value) {
			// The protected source value is already part of the imported
			// destination. Do not silently bind it to an unrelated configured
			// secret with the same suggested name, and do not require the
			// operator to enter the value again.
			continue
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
		// Automatically selected existing names otherwise conflict unless
		// provider metadata proves this protected session created the equal value.
		explicitExistingBindingApproved := requestedExplicitly && explicitExistingBinding
		if canMigrate && brunoImportCredentialValueUsable(requirement.Value) && !explicitExistingBindingApproved {
			existingValue, existingMetadata, getErr := store.Get(probeCtx, projectID, name)
			switch {
			case getErr == nil:
				// An explicit equal-valued target makes migration retries
				// idempotent after a provider or script-commit failure. Automatic
				// name adoption remains a conflict, matching the shared migration
				// API and preventing an unnoticed source-value discard.
				if requestedExplicitly && existingValue == requirement.Value {
					break
				}
				// A retained import retry may encounter a suggested-name secret
				// created by its own earlier provider phase. Accept it only when
				// value-free provider metadata proves the same actor created the
				// equal value within this protected import session's lifetime.
				if !requestedExplicitly && brunoSecretMatchesRetainedMigration(
					existingValue, requirement.Value, existingMetadata, actor, migrationWindow,
				) {
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
		if assertMutationLease != nil {
			if leaseErr := assertMutationLease(); leaseErr != nil {
				return bindings, nil, migrations, nil, &brunoSecretMigrationPartialError{
					Cause:          leaseErr,
					Migrated:       append([]secretstore.Metadata(nil), migrations...),
					FailedName:     plan.name,
					RemainingNames: append([]string(nil), planNames[index:]...),
				}
			}
		}
		operationID, auditErr := s.beginProjectSecretOperation(r, "script_import.secret.migrate", plan.name, projectID, secretStoreProvider(store))
		if auditErr != nil {
			return bindings, nil, migrations, nil, &brunoSecretMigrationPartialError{
				Cause:          fmt.Errorf("source credential migration audit is unavailable: %w", auditErr),
				Migrated:       append([]secretstore.Metadata(nil), migrations...),
				FailedName:     plan.name,
				RemainingNames: append([]string(nil), planNames[index:]...),
			}
		}
		if assertMutationLease != nil {
			if leaseErr := assertMutationLease(); leaseErr != nil {
				if auditErr := s.completeProjectSecretOperation(
					r, operationID, "script_import.secret.migrate", "failed", "session_claim_lost",
					plan.name, projectID, secretStoreProvider(store), "session_claim_lost", nil,
				); auditErr != nil {
					leaseErr = errors.Join(leaseErr, fmt.Errorf("persist source credential migration audit outcome: %w", auditErr))
				}
				return bindings, nil, migrations, nil, &brunoSecretMigrationPartialError{
					Cause:          leaseErr,
					Migrated:       append([]secretstore.Metadata(nil), migrations...),
					FailedName:     plan.name,
					RemainingNames: append([]string(nil), planNames[index:]...),
				}
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
	settings := s.authSettingsForRequest()
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
	return archiveingest.LogicalBase(parameters["filename"])
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
