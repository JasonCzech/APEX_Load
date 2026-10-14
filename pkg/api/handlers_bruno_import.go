package api

import (
	"archive/tar"
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
	"regexp"
	"slices"
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
	maxBrunoSourceInspectBytes    = 4 << 20
)

var errBrunoImportSessionExpiredDuringCommit = errors.New("Bruno import session expired before it could be restored")

type brunoImportSession struct {
	ID                      string
	ProjectID               string
	Actor                   string
	Result                  *brunoconvert.Result
	RawArtifact             storage.ImportSourceArtifact
	SanitizedArtifact       storage.ImportSourceArtifact
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
	RawArtifact             storage.ImportSourceArtifact                     `json:"raw_artifact"`
	SanitizedArtifact       storage.ImportSourceArtifact                     `json:"sanitized_artifact"`
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
	Parameterization   *paramize.Result       `json:"parameterization,omitempty"`
	// SharedValues describes native request variables that can be reviewed
	// together before committing the per-request script batch.
	SharedValues *sharedvalue.Result `json:"shared_values,omitempty"`
	// PreviousInputs maps a shared-value candidate ID to how the scripts this
	// re-import updates supplied that input, to offer it again.
	PreviousInputs map[string]brunoPreviousInput `json:"previous_inputs,omitempty"`
}

// brunoPreviousInput is one input as the existing scripts supply it: a value
// for every user (Value), a data pool column, or a generated value.
type brunoPreviousInput struct {
	Mode      string `json:"mode"`
	Value     string `json:"value,omitempty"`
	Alias     string `json:"alias,omitempty"`
	Column    string `json:"column,omitempty"`
	Generator string `json:"generator,omitempty"`
	Digits    int    `json:"digits,omitempty"`
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
	SharedValues map[string]string `json:"shared_values,omitempty"`
	// ParameterizationSelections maps a native candidate ID to the parameter
	// suggestions (generated values, data parameters) from that candidate's
	// inspection to apply. IDs repeat across candidates, so they are scoped.
	ParameterizationSelections map[string][]string `json:"parameterization_selections,omitempty"`
	// InputBindings maps a shared-value candidate ID (a variable the source
	// left unset) to a data pool column or a generated value, for an input
	// that should not be one value for every virtual user.
	InputBindings          map[string]sharedvalue.Binding `json:"input_bindings,omitempty"`
	ConflictResolutions    map[string]string              `json:"conflict_resolutions,omitempty"`
	CandidateIDs           []string                       `json:"candidate_ids,omitempty"`
	NameOverrides          map[string]string              `json:"name_overrides,omitempty"`
	AcknowledgedFindingIDs []string                       `json:"acknowledged_finding_ids,omitempty"`
	// UpdateTargets maps a native candidate ID to the script an earlier import
	// created from the same request. That script is updated as a new version
	// instead of creating a duplicate.
	UpdateTargets map[string]brunoNativeUpdateTarget `json:"update_targets,omitempty"`
	// HeaderRepairs maps a bruno_invalid_header_value finding ID to
	// brunoconvert.HeaderRepairTrim or HeaderRepairRemove.
	HeaderRepairs        map[string]string `json:"header_repairs,omitempty"`
	candidateIDsOmitted  bool
	nameOverridesOmitted bool
}

// brunoNativeUpdateTarget names an existing script and the updated_at the
// operator reviewed. The update fails if the script changed since.
type brunoNativeUpdateTarget struct {
	ScriptID          string    `json:"script_id"`
	ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
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

func (s *Server) handleBrunoSupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "bruno")
}

func (s *Server) handleInspectBrunoImportSource(w http.ResponseWriter, r *http.Request) {
	projectID := normalizeProjectID(r.URL.Query().Get("project_id"))
	sourcePath := strings.TrimSpace(r.URL.Query().Get("path"))
	session, err := s.loadBrunoImportSession(r, r.PathValue("id"), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Bruno import session was not found or is no longer available")
		return
	}
	defer clearBrunoImportSession(session)
	if time.Now().UTC().After(session.ExpiresAt) {
		writeError(w, http.StatusGone, "Bruno import session has expired; inspect the source again")
		return
	}
	if sourcePath == "" || session.Result == nil {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	authorized := false
	for _, candidate := range session.Result.Scripts {
		if candidate.SourcePath == sourcePath {
			authorized = true
			break
		}
	}
	if !authorized {
		writeError(w, http.StatusNotFound, "source API is not part of this import inspection")
		return
	}
	archivePath := pathpkg.Join(session.Result.RootPath, sourcePath)
	if archivePath == "." || strings.HasPrefix(archivePath, "../") || pathpkg.IsAbs(archivePath) {
		writeError(w, http.StatusBadRequest, "source API path is invalid")
		return
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "import source decryption is unavailable: "+err.Error())
		return
	}
	reader, _, err := objects.Open(r.Context(), brunoArtifactOpenRequest(session.SanitizedArtifact))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open sanitized import source: "+err.Error())
		return
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			writeError(w, http.StatusNotFound, "source API was not found in the sanitized import bundle")
			return
		}
		if nextErr != nil {
			writeError(w, http.StatusInternalServerError, "read sanitized import source: "+nextErr.Error())
			return
		}
		if pathpkg.Clean(header.Name) != archivePath || header.FileInfo().IsDir() {
			continue
		}
		if header.Size < 0 || header.Size > maxBrunoSourceInspectBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "source API exceeds the inspection size limit")
			return
		}
		payload, readErr := io.ReadAll(io.LimitReader(tarReader, maxBrunoSourceInspectBytes+1))
		if readErr != nil {
			writeError(w, http.StatusInternalServerError, "read sanitized source API: "+readErr.Error())
			return
		}
		if len(payload) > maxBrunoSourceInspectBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "source API exceeds the inspection size limit")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
		clear(payload)
		return
	}
}

// brunoSupportMatrixPayload reports Bruno coverage together with the live
// secret-capability and source-retention posture, which depend on how this
// controller is configured rather than on the converter alone.
func brunoSupportMatrixPayload(s *Server) map[string]any {
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
	return map[string]any{
		"schema_version":                brunoconvert.ImporterVersion,
		"compatibility_version":         "native_requests",
		"import_enabled":                brunoFeatureEnabled("APEXLOAD_BRUNO_IMPORT_ENABLED", true),
		"safe_execution_enabled":        false,
		"developer_execution_enabled":   false,
		"native_projection_enabled":     false,
		"native_request_import_enabled": true,
		"source_formats":                []string{"OpenCollection YAML HTTP", "standalone YAML HTTP", "ZIP", "TAR", "TAR.GZ", "directory upload"},
		"request_types":                 []string{"HTTP", "SOAP-over-HTTP", "GraphQL-over-HTTP"},
		"runtime":                       "native_http",
		"runner_retired":                true,
		"retention":                     retention,
		"secret_store":                  secretCapabilities,
	}
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
	if strings.TrimSpace(syncScriptID) != "" || strings.TrimSpace(input.Sandbox) != "" || strings.EqualFold(strings.TrimSpace(input.Options.OutputMode), brunoconvert.OutputModeCanonical) {
		writeBrunoRunnerRetired(w)
		return
	}
	if strings.TrimSpace(input.Options.OutputMode) == "" {
		input.Options.OutputMode = brunoconvert.OutputModeNativeRequests
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
	nativeMode := true
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
	if nativeMode {
		s.attachBrunoNativeExistingScripts(input.ProjectID, result)
		if err := attachBrunoNativeHeaderRepairs(result); err != nil {
			clearBrunoImportSecretValues(result)
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
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
	sourceArtifacts := make([]storage.ImportSourceArtifact, 0, 2)
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
		storage.ImportSourceArtifact{
			BackendKey: rawPlan.BackendKey, ProjectID: input.ProjectID,
			ArtifactKind: "raw", SHA256: rawPlan.SHA256, SizeBytes: rawPlan.SizeBytes,
		},
		storage.ImportSourceArtifact{
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
	rawArtifact := storage.ImportSourceArtifact{
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
	sanitizedArtifact := storage.ImportSourceArtifact{
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
		// From the same masked projection, so the suggestion IDs are the ones
		// the commit re-derives.
		for index := range responseResult.Scripts {
			responseResult.Scripts[index].Parameterization = brunoNativeCandidateParameterization(responseResult.Scripts[index].Script)
		}
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
	auditDetails := map[string]any{
		"source_format":  result.SourceFormat,
		"files":          len(result.Manifest.Files),
		"source_bytes":   result.Manifest.TotalBytes,
		"output_mode":    input.Options.OutputMode,
		"native_scripts": len(result.Scripts),
		"request_types":  result.Capabilities.RequestTypes,
	}
	s.recordAuditEvent(r, "script.import.bruno.inspect", "success", "bruno_import_session", session.ID, input.ProjectID, auditDetails)
	executionPreflight := brunoNativeExecutionPreflight(result, retainedSecretValues)
	writeJSON(w, http.StatusOK, brunoInspectResponse{
		ImportSessionID: session.ID,
		ExpiresAt:       session.ExpiresAt,
		Result:          responseResult,
		Artifacts: []brunoArtifactSummary{
			brunoSanitizedArtifactPublicSummary(sanitizedArtifact),
		},
		ExecutionPreflight: executionPreflight,
		SharedValues:       sharedValues,
		PreviousInputs:     s.brunoNativePreviousInputs(input.ProjectID, responseResult, sharedValues),
	})
}

// brunoNativePreviousInputs reads, from the scripts a re-import updates, how
// each input the source leaves unset was supplied last time: the choice an
// earlier import recorded, or a value-for-every-user parameter of the same
// name. The first script that supplies an input decides it.
func (s *Server) brunoNativePreviousInputs(projectID string, result *brunoconvert.Result, shared *sharedvalue.Result) map[string]brunoPreviousInput {
	if result == nil || shared == nil {
		return nil
	}
	existing := map[string]*dsl.Script{}
	load := func(scriptID string) *dsl.Script {
		if script, loaded := existing[scriptID]; loaded {
			return script
		}
		stored, err := s.store.GetScript(scriptID)
		if err != nil || stored == nil || normalizeProjectID(stored.ProjectID) != normalizeProjectID(projectID) {
			stored = nil
		}
		existing[scriptID] = stored
		return stored
	}
	previous := map[string]brunoPreviousInput{}
	for _, candidate := range shared.Candidates {
		// A credential-shaped input never becomes a parameter at import, so a
		// value under its name was put there by hand and stays out of the response.
		if candidate.Kind != sharedvalue.KindSourceVariable || candidate.CredentialShaped {
			continue
		}
		for _, index := range candidate.ScriptIndexes {
			if index < 0 || index >= len(result.Scripts) || result.Scripts[index].ExistingScript == nil {
				continue
			}
			script := load(result.Scripts[index].ExistingScript.ScriptID)
			if script == nil {
				continue
			}
			if input, ok := brunoPreviousInputFrom(script, candidate.Name); ok {
				previous[candidate.ID] = input
				break
			}
		}
	}
	if len(previous) == 0 {
		return nil
	}
	return previous
}

func brunoPreviousInputFrom(script *dsl.Script, name string) (brunoPreviousInput, bool) {
	var recorded dsl.ImportInput
	if script.Metadata != nil && script.Metadata.Import != nil {
		recorded = script.Metadata.Import.Inputs[name]
	}
	switch recorded.Mode {
	case sharedvalue.BindingData, sharedvalue.BindingGenerated:
		return brunoPreviousInput{Mode: recorded.Mode, Alias: recorded.Alias, Column: recorded.Column, Generator: recorded.Generator, Digits: recorded.Digits}, true
	}
	// A value for every user lives in the parameter, which the script's
	// author may have edited since.
	for _, param := range script.Parameters {
		if param.Name == name && dsl.CanonicalParameterType(param.Type) == "list" && len(param.Values) == 1 && strings.TrimSpace(param.Values[0]) != "" {
			return brunoPreviousInput{Mode: "value", Value: param.Values[0]}, true
		}
	}
	return brunoPreviousInput{}, false
}

// brunoImportedScriptLister is the storage needed to match a re-import with
// the scripts an earlier import created.
type brunoImportedScriptLister interface {
	ListImportedScriptIdentities(projectID, source string) ([]storage.ImportedScriptIdentity, error)
	ImportBatchSavedAt(projectID string, scriptIDs []string) (map[string]time.Time, error)
}

// attachBrunoNativeExistingScripts marks each candidate whose source request
// an earlier import already saved in this project, so a re-import can update
// that script as a new version instead of duplicating it. Matching is a
// convenience: when it cannot run, inspection continues without it.
//
// A candidate ID hashes the request's UID and path. Collection UIDs that
// differ never match. A match is the same collection only when the UIDs are
// equal or, with no UIDs, the collection names are equal; other matches are
// offered but default to a copy.
func (s *Server) attachBrunoNativeExistingScripts(projectID string, result *brunoconvert.Result) {
	if s == nil || result == nil || len(result.Scripts) == 0 {
		return
	}
	lister, ok := s.store.(brunoImportedScriptLister)
	if !ok {
		return
	}
	identities, err := lister.ListImportedScriptIdentities(projectID, "bruno")
	if err != nil {
		slog.Warn("match previously imported Bruno scripts", "project_id", projectID, "error", err)
		return
	}
	byCandidate := make(map[string][]storage.ImportedScriptIdentity)
	for _, identity := range identities {
		if identity.ArchivedAt != nil {
			continue
		}
		if candidateID, _, _, ok := brunoNativeImportIdentity(identity.Import); ok && candidateID != "" {
			byCandidate[candidateID] = append(byCandidate[candidateID], identity)
		}
	}
	type match struct {
		identity       storage.ImportedScriptIdentity
		matches        int
		sameCollection bool
	}
	chosen := make(map[int]match)
	var chosenIDs []string
	for index := range result.Scripts {
		candidate := &result.Scripts[index]
		if candidate.Script == nil {
			continue
		}
		var candidateImport *dsl.ImportMetadata
		if candidate.Script.Metadata != nil {
			candidateImport = candidate.Script.Metadata.Import
		}
		_, collectionUID, collectionName, _ := brunoNativeImportIdentity(candidateImport)
		// A name taken from a folder, a file or the default can coincide
		// across unrelated uploads, so only a declared name can match.
		declared := false
		if candidateImport != nil {
			declared, _ = candidateImport.Preserved["collection_name_declared"].(bool)
		}
		if !declared {
			collectionName = ""
		}
		best := match{}
		found := false
		count := 0
		for _, existing := range byCandidate[candidate.CandidateID] {
			_, existingUID, existingName, _ := brunoNativeImportIdentity(existing.Import)
			// Scripts saved before the flag existed have no record of where
			// their name came from; an explicit false is never an identity.
			if existingDeclared, recorded := existing.Import.Preserved["collection_name_declared"].(bool); recorded && !existingDeclared {
				existingName = ""
			}
			if collectionUID != "" && existingUID != "" && collectionUID != existingUID {
				continue
			}
			count++
			sameCollection := (collectionUID != "" && collectionUID == existingUID) ||
				(collectionUID == "" && existingUID == "" && collectionName != "" &&
					strings.EqualFold(collectionName, existingName))
			// Prefer a script from the same collection, then the newest.
			if !found || (sameCollection && !best.sameCollection) ||
				(sameCollection == best.sameCollection && existing.UpdatedAt.After(best.identity.UpdatedAt)) {
				best = match{identity: existing, sameCollection: sameCollection}
				found = true
			}
		}
		if !found {
			continue
		}
		best.matches = count
		chosen[index] = best
		chosenIDs = append(chosenIDs, best.identity.ID)
	}
	if len(chosen) == 0 {
		return
	}
	savedAt, err := lister.ImportBatchSavedAt(projectID, chosenIDs)
	if err != nil {
		slog.Warn("read Bruno import receipts for edit detection", "project_id", projectID, "error", err)
		savedAt = nil
	}
	for index, best := range chosen {
		folderID := ""
		if best.identity.FolderID != nil {
			folderID = strings.TrimSpace(*best.identity.FolderID)
		}
		saved, known := savedAt[best.identity.ID]
		// Postgres keeps microseconds while the receipt keeps nanoseconds. A
		// script with no receipt is treated as edited, which only adds a
		// warning.
		edited := !known || !saved.Truncate(time.Microsecond).Equal(best.identity.UpdatedAt.Truncate(time.Microsecond))
		result.Scripts[index].ExistingScript = &brunoconvert.NativeExistingScript{
			ScriptID:          best.identity.ID,
			Name:              best.identity.Name,
			RefKey:            best.identity.RefKey,
			FolderID:          folderID,
			UpdatedAt:         best.identity.UpdatedAt.UTC(),
			Matches:           best.matches,
			SameCollection:    best.sameCollection,
			EditedSinceImport: edited,
		}
	}
}

// brunoNativeImportIdentity returns the source identity a native Bruno import
// records in a script's import metadata: the candidate ID (request UID and
// path), which commit stamps and an inspection candidate lacks, the
// collection UID, which may be empty, and the collection name.
func brunoNativeImportIdentity(metadata *dsl.ImportMetadata) (string, string, string, bool) {
	if metadata == nil || !strings.EqualFold(strings.TrimSpace(metadata.Source), "bruno") {
		return "", "", "", false
	}
	preserved := metadata.Preserved
	if preserved == nil || preserved["native_importer"] == nil {
		return "", "", "", false
	}
	candidateID, _ := preserved["candidate_id"].(string)
	collectionUID, _ := preserved["collection_uid"].(string)
	collectionName, _ := preserved["collection_name"].(string)
	return strings.TrimSpace(candidateID), strings.TrimSpace(collectionUID), strings.TrimSpace(collectionName), true
}

// attachBrunoNativeHeaderRepairs adds a review finding, with a fix, for each
// request header HTTP cannot carry. Headers are checked as a default commit
// would save them, with imported credential values restored.
func attachBrunoNativeHeaderRepairs(result *brunoconvert.Result) error {
	if result == nil {
		return nil
	}
	_, replacements, err := planBrunoNativeSecretBindings(result.SecretRequirements, nil, false)
	if err != nil {
		return err
	}
	defer clearStringMap(replacements)
	for index := range result.Scripts {
		candidate := &result.Scripts[index]
		if candidate.Script == nil {
			continue
		}
		bound, bindErr := applyBrunoNativeCredentialTargets(
			cloneScriptDefinition(candidate.Script),
			candidate.CredentialTargets,
			candidate.SecretRequirements,
			replacements,
		)
		if bindErr != nil {
			return fmt.Errorf("check native Bruno request headers for %q: %w", candidate.SourcePath, bindErr)
		}
		diagnostics := brunoconvert.NativeHeaderRepairDiagnostics(candidate.CandidateID, candidate.SourcePath, bound)
		if len(diagnostics) == 0 {
			continue
		}
		candidate.Diagnostics = append(candidate.Diagnostics, diagnostics...)
		candidate.Fidelity = "review_required"
	}
	return nil
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

// brunoNativeCandidateParameterization is the review copy of one request's
// parameter suggestions, derived from its masked script as the commit
// re-derives them. Nil when there is nothing to suggest.
func brunoNativeCandidateParameterization(script *dsl.Script) *paramize.Result {
	if script == nil {
		return nil
	}
	result := sanitizeImportParameterization(buildTrustedImportParameterization(cloneScriptDefinition(script)))
	if len(result.Suggestions) == 0 {
		return nil
	}
	return result
}

// applyBrunoNativeInputBindings binds this script's inputs that are not one
// value for every user, then records how each of its inputs was supplied so
// a later import of the same requests can offer the same choices.
func applyBrunoNativeInputBindings(
	script *dsl.Script,
	scriptIndex int,
	candidates []sharedvalue.Candidate,
	values map[string]string,
	bindings map[string]sharedvalue.Binding,
) error {
	if script == nil || (len(values) == 0 && len(bindings) == 0) {
		return nil
	}
	inputs := map[string]dsl.ImportInput{}
	for _, candidate := range candidates {
		if candidate.Kind != sharedvalue.KindSourceVariable || !slices.Contains(candidate.ScriptIndexes, scriptIndex) {
			continue
		}
		if binding, bound := bindings[candidate.ID]; bound {
			if _, err := sharedvalue.Bind(script, candidate, binding); err != nil {
				return err
			}
			inputs[candidate.Name] = dsl.ImportInput{Mode: binding.Mode, Alias: binding.Alias, Column: binding.Column, Generator: binding.Generator, Digits: binding.Digits}
			continue
		}
		if value := strings.TrimSpace(values[candidate.ID]); value != "" {
			inputs[candidate.Name] = dsl.ImportInput{Mode: "value"}
		}
	}
	if len(inputs) == 0 {
		return nil
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	script.Metadata.Import.Inputs = inputs
	if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
		return fmt.Errorf("the script is not valid with these inputs: %s", strings.Join(issues, "; "))
	}
	return nil
}

var errBrunoNativeParameterizationStale = errors.New("native Bruno parameter suggestions are stale")

// brunoNativeParameterizationPlan is one request's selected suggestions,
// derived again from its masked inspection script.
type brunoNativeParameterizationPlan struct {
	suggestions []paramize.Suggestion
	selected    map[string]bool
}

func planBrunoNativeParameterization(projected *dsl.Script, ids []string) (brunoNativeParameterizationPlan, error) {
	if projected == nil {
		return brunoNativeParameterizationPlan{}, errBrunoNativeParameterizationStale
	}
	trusted := buildTrustedImportParameterization(cloneScriptDefinition(projected))
	available := make(map[string]bool, len(trusted.Suggestions))
	for _, suggestion := range trusted.Suggestions {
		available[suggestion.ID] = true
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !available[id] {
			return brunoNativeParameterizationPlan{}, errBrunoNativeParameterizationStale
		}
		selected[id] = true
	}
	return brunoNativeParameterizationPlan{suggestions: trusted.Suggestions, selected: selected}, nil
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
		projectedCandidate.Diagnostics = publicBrunoNativeDiagnostics(candidate.Diagnostics)
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

// publicBrunoNativeDiagnostics drops the header text a left-out header's trim
// would put back. The browser shows only character counts, and the response
// masks script header values, so the finding should not carry them either.
func publicBrunoNativeDiagnostics(diagnostics []brunoconvert.NativeDiagnostic) []brunoconvert.NativeDiagnostic {
	if diagnostics == nil {
		return nil
	}
	public := make([]brunoconvert.NativeDiagnostic, len(diagnostics))
	for index, diagnostic := range diagnostics {
		if diagnostic.HeaderRepair != nil && diagnostic.HeaderRepair.TrimmedValue != "" {
			repair := *diagnostic.HeaderRepair
			repair.TrimmedValue = ""
			diagnostic.HeaderRepair = &repair
		}
		public[index] = diagnostic
	}
	return public
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
		for _, diagnostic := range candidate.Diagnostics {
			if diagnostic.HeaderRepair != nil {
				blockers = append(blockers, "invalid_header")
				break
			}
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
			if action.Control != nil {
				for _, branch := range action.Control.ActionBranches() {
					if visit(branch.Actions) {
						return true
					}
				}
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
	request.ProjectID = normalizeProjectID(request.ProjectID)
	if strings.ToLower(strings.TrimSpace(request.Mode)) != "create_native_batch" {
		writeBrunoRunnerRetired(w)
		return
	}
	s.handleCommitBrunoNativeImport(w, r, request, fields)
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
		"update_targets": {}, "header_repairs": {}, "parameterization_selections": {}, "input_bindings": {},
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
	if request.FolderID != nil && *request.FolderID == "" {
		request.FolderID = nil
	}
	if request.FolderID == nil && len(request.UpdateTargets) == 0 {
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
		response := map[string]any{
			"batch_id":           receipt.Batch.ID,
			"scripts":            maskBrunoNativeCommitScripts(receipt.Scripts),
			"count":              len(receipt.Scripts),
			"created":            false,
			"idempotent_replay":  true,
			"updated_script_ids": brunoNativeReceiptUpdatedScriptIDs(receipt, replayRequest),
		}
		s.attachBrunoNativeUnconfiguredSecretNames(r, request.ProjectID, receipt.Scripts, response)
		writeJSON(w, http.StatusOK, response)
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
	if session.Result == nil || !strings.EqualFold(strings.TrimSpace(session.Result.OutputMode), brunoconvert.OutputModeNativeRequests) || session.Result.Script != nil {
		writeBrunoRunnerRetired(w)
		return
	}
	if !s.authorizeBrunoSourceSecretMigration(w, r, request.ProjectID, migrateSecrets) {
		return
	}

	if request.FolderID != nil {
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
	headerRepairs, err := selectedBrunoNativeHeaderRepairs(selected, request.HeaderRepairs)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	selectedIDs := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		selectedIDs[candidate.CandidateID] = struct{}{}
	}
	for candidateID := range request.UpdateTargets {
		if _, ok := selectedIDs[candidateID]; !ok {
			writeError(w, http.StatusConflict, fmt.Sprintf("update target candidate %q is not selected", candidateID))
			return
		}
	}
	if request.FolderID == nil && len(request.UpdateTargets) < len(selected) {
		writeError(w, http.StatusBadRequest, "folder_id is required when a native Bruno batch creates scripts")
		return
	}
	// Repairing a finding resolves it, so it needs no acknowledgement.
	resolvedFindingIDs := append([]string(nil), request.AcknowledgedFindingIDs...)
	for findingID := range request.HeaderRepairs {
		if !slices.Contains(resolvedFindingIDs, findingID) {
			resolvedFindingIDs = append(resolvedFindingIDs, findingID)
		}
	}
	if err := validateBrunoNativeNamesAndAcknowledgements(selected, request.NameOverrides, resolvedFindingIDs); err != nil {
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
	expectedUpdatedAt := make([]*time.Time, 0, len(selected))
	updatedScriptIDs := make([]string, 0, len(request.UpdateTargets))
	plannedIDs := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		script := cloneScriptDefinition(candidate.Script)
		if script == nil || strings.TrimSpace(candidate.PlannedScriptID) == "" {
			writeError(w, http.StatusConflict, "native Bruno candidate has no server-owned script")
			return
		}
		if strings.TrimSpace(script.Protocol) != "http" {
			writeError(w, http.StatusConflict, fmt.Sprintf("native Bruno candidate %q protocol must be exactly http", candidate.CandidateID))
			return
		}
		var expected *time.Time
		if target, updating := request.UpdateTargets[candidate.CandidateID]; updating {
			existing, targetErr := s.brunoNativeUpdateTargetScript(request.ProjectID, candidate.CandidateID, target)
			if targetErr != nil {
				if !errors.Is(targetErr, errBrunoNativeUpdateTargetStale) {
					ref := logServerError(w, http.StatusInternalServerError, targetErr, "code", "bruno_native_update_target_unavailable")
					writeJSON(w, http.StatusInternalServerError, map[string]any{
						"error":        "the script to update could not be read",
						"ref":          ref,
						"code":         "bruno_native_update_target_unavailable",
						"candidate_id": candidate.CandidateID,
					})
					return
				}
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":        targetErr.Error(),
					"code":         "bruno_native_update_target_stale",
					"candidate_id": candidate.CandidateID,
				})
				return
			}
			script.ID = existing.ID
			script.FolderID = cloneStringPtr(existing.FolderID)
			script.Name = existing.Name
			script.Version = existing.Version
			script.CreatedAt = existing.CreatedAt
			expectedTime := target.ExpectedUpdatedAt
			expected = &expectedTime
			updatedScriptIDs = append(updatedScriptIDs, existing.ID)
		} else {
			script.ID = strings.TrimSpace(candidate.PlannedScriptID)
			if _, parseErr := uuid.Parse(script.ID); parseErr != nil {
				writeError(w, http.StatusConflict, fmt.Sprintf("native Bruno candidate %q has an invalid planned script id", candidate.CandidateID))
				return
			}
			if _, existingErr := s.store.GetScript(script.ID); existingErr == nil {
				writeError(w, http.StatusConflict, fmt.Sprintf("planned native Bruno script id %q already exists", script.ID))
				return
			} else if !errors.Is(existingErr, sql.ErrNoRows) {
				writeError(w, http.StatusInternalServerError, existingErr.Error())
				return
			}
			script.FolderID = cloneStringPtr(request.FolderID)
		}
		if _, duplicate := plannedIDs[script.ID]; duplicate {
			writeError(w, http.StatusConflict, fmt.Sprintf("native Bruno candidates contain duplicate planned script id %q", script.ID))
			return
		}
		plannedIDs[script.ID] = struct{}{}
		script.ProjectID = request.ProjectID
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
		expectedUpdatedAt = append(expectedUpdatedAt, expected)
	}

	// The masked inspection the browser reviewed. Shared values and parameter
	// suggestions re-derive their IDs from it.
	var inspectionProjection *brunoconvert.Result
	projectInspection := func() (*brunoconvert.Result, error) {
		if inspectionProjection == nil {
			projection, err := publicBrunoNativeInspectResult(session.Result)
			if err != nil {
				return nil, err
			}
			inspectionProjection = projection
		}
		return inspectionProjection, nil
	}
	// Collection-wide values are applied across the whole selected batch before
	// anything is canonicalized or persisted: one value can touch every script,
	// so a per-script pass would leave the batch inconsistent.
	var trustedShared []sharedvalue.Candidate
	if len(request.SharedValues) > 0 || len(request.InputBindings) > 0 {
		// Bind the submitted IDs to the immutable inspection, then rebase its
		// server-owned locations onto the selected batch. Re-detecting only the
		// subset changes location-based IDs and can discard repeated literals
		// whose other occurrences were deselected.
		projection, projectionErr := projectInspection()
		if projectionErr != nil {
			writeError(w, http.StatusInternalServerError, "unable to project inspected Bruno values")
			return
		}
		selectedIndexes := make(map[int]int, len(selected))
		selectedByID := make(map[string]int, len(selected))
		for index, candidate := range selected {
			selectedByID[candidate.CandidateID] = index
		}
		for originalIndex, original := range session.Result.Scripts {
			if selectedIndex, ok := selectedByID[original.CandidateID]; ok {
				selectedIndexes[originalIndex] = selectedIndex
			}
		}
		trusted := rebaseBrunoSharedValues(trustedSharedValueCandidates(brunoNativeCandidateScripts(projection)), selectedIndexes)
		trustedShared = trusted
		known := make(map[string]sharedvalue.Kind, len(trusted))
		for _, candidate := range trusted {
			known[candidate.ID] = candidate.Kind
		}
		for id := range request.SharedValues {
			if _, ok := known[id]; !ok {
				writeError(w, http.StatusConflict,
					"selected Bruno shared values are stale or unavailable; inspect the source again")
				return
			}
		}
		for id := range request.InputBindings {
			kind, ok := known[id]
			if !ok {
				writeError(w, http.StatusConflict,
					"selected Bruno input bindings are stale or unavailable; inspect the source again")
				return
			}
			if kind != sharedvalue.KindSourceVariable {
				writeError(w, http.StatusBadRequest, "input_bindings can only bind a variable the source left unset")
				return
			}
		}
	}
	if len(request.SharedValues) > 0 {
		trusted := trustedShared
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

	// Parameter selections are checked here and applied after credentials are
	// bound: a credential target names exact character positions in the
	// inspected script, which a replacement earlier in the field would move.
	parameterizationPlans := make(map[int]brunoNativeParameterizationPlan, len(request.ParameterizationSelections))
	if len(request.ParameterizationSelections) > 0 {
		projection, projectionErr := projectInspection()
		if projectionErr != nil {
			writeError(w, http.StatusInternalServerError, "unable to project inspected Bruno requests")
			return
		}
		projected := make(map[string]*dsl.Script, len(projection.Scripts))
		for _, candidate := range projection.Scripts {
			projected[candidate.CandidateID] = candidate.Script
		}
		selectedIndex := make(map[string]int, len(candidateIDs))
		for index, candidateID := range candidateIDs {
			selectedIndex[candidateID] = index
		}
		selectionCandidates := make([]string, 0, len(request.ParameterizationSelections))
		for candidateID := range request.ParameterizationSelections {
			selectionCandidates = append(selectionCandidates, candidateID)
		}
		sort.Strings(selectionCandidates)
		for _, candidateID := range selectionCandidates {
			index, ok := selectedIndex[candidateID]
			if !ok {
				writeError(w, http.StatusBadRequest, "parameterization_selections names a request that is not being saved")
				return
			}
			plan, planErr := planBrunoNativeParameterization(projected[candidateID], request.ParameterizationSelections[candidateID])
			if planErr != nil {
				writeError(w, http.StatusConflict, "selected Bruno parameter suggestions are stale or unavailable; inspect the source again")
				return
			}
			parameterizationPlans[index] = plan
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
		for _, planned := range headerRepairs[candidateIDs[index]] {
			applied, repairErr := brunoconvert.ApplyNativeHeaderRepair(bound, planned.repair, planned.choice)
			if repairErr == nil && !applied && migrateSecrets {
				// The header now holds a project-secret reference, so the fix
				// cannot reach the value. When that secret does not exist yet,
				// migration creates it from the invalid source value.
				migrated, statusErr := s.brunoHeaderSecretsAwaitValue(r, request.ProjectID,
					brunoconvert.NativeHeaderRepairTargetValue(bound, planned.repair))
				switch {
				case statusErr != nil:
					repairErr = errors.New("the header's project secrets could not be checked; retry the save")
				case migrated:
					repairErr = errors.New("the header's credential is being moved into project secrets, so its value cannot be fixed here; save without migrating it, or correct the value in Project secrets")
				}
			}
			if repairErr != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":        "native Bruno header repair failed: " + repairErr.Error(),
					"code":         "bruno_native_header_repair_failed",
					"candidate_id": candidateIDs[index],
				})
				return
			}
		}
		// Like parameter selections, a data binding rewrites fields a
		// credential target pins, so it waits until credentials are bound.
		if bindErr := applyBrunoNativeInputBindings(bound, index, trustedShared, request.SharedValues, request.InputBindings); bindErr != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"error":        "native Bruno input binding failed: " + bindErr.Error(),
				"code":         "bruno_native_input_binding_failed",
				"candidate_id": candidateIDs[index],
			})
			return
		}
		if plan, planned := parameterizationPlans[index]; planned {
			applied, _, applyErr := paramize.ApplySuggestions(bound, plan.suggestions, plan.selected)
			if applyErr != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":        "failed to apply Bruno parameter suggestions: " + applyErr.Error(),
					"code":         "bruno_native_parameterization_failed",
					"candidate_id": candidateIDs[index],
				})
				return
			}
			if issues := dsl.ValidateScriptIssues(applied); len(issues) > 0 {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"error":        "native Bruno script validation failed after applying parameter suggestions",
					"code":         "bruno_native_script_validation_failed",
					"candidate_id": candidateIDs[index],
					"issues":       issues,
				})
				return
			}
			bound = applied
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
		ExpectedUpdatedAt: expectedUpdatedAt,
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
			ref := logServerError(w, http.StatusServiceUnavailable, reconcileErr, "code", "bruno_native_commit_state_unknown", "session_id", session.ID)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":                   "Native Bruno batch commit state could not be reconciled",
				"ref":                     ref,
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
		"scripts_created":  len(commitResult.Scripts) - len(updatedScriptIDs),
		"scripts_updated":  len(updatedScriptIDs),
		"secrets_migrated": len(migrations),
	})
	// Each overwritten script gets its own entry, so its history shows the
	// re-import that replaced its content.
	for _, scriptID := range updatedScriptIDs {
		s.recordAuditEvent(r, "script.import.bruno.native.update", "success", "script", scriptID, request.ProjectID, map[string]any{
			"batch_id":      commitResult.Batch.ID,
			"source_format": session.Result.SourceFormat,
		})
	}
	status := http.StatusCreated
	if !commitResult.Created {
		status = http.StatusOK
	}
	response := map[string]any{
		"batch_id":           commitResult.Batch.ID,
		"scripts":            maskBrunoNativeCommitScripts(commitResult.Scripts),
		"count":              len(commitResult.Scripts),
		"created":            commitResult.Created,
		"idempotent_replay":  !commitResult.Created,
		"secret_migrations":  migrations,
		"updated_script_ids": updatedScriptIDs,
	}
	s.attachBrunoNativeUnconfiguredSecretNames(r, request.ProjectID, commitResult.Scripts, response)
	writeJSON(w, status, response)
}

// attachBrunoNativeUnconfiguredSecretNames adds the project secrets the saved
// scripts reference that still need a value before a run. Saving does not
// require them, so a failed status check leaves the field out rather than
// failing a committed import.
func (s *Server) attachBrunoNativeUnconfiguredSecretNames(r *http.Request, projectID string, scripts []*dsl.Script, response map[string]any) {
	unconfigured, err := s.unconfiguredBrunoNativeSecretNames(r, projectID, scripts)
	if err != nil {
		slog.Warn("check native Bruno secret configuration", "project_id", projectID, "error", err)
		return
	}
	response["unconfigured_secret_names"] = unconfigured
}

// unconfiguredBrunoNativeSecretNames lists the project secrets scripts
// reference that have no usable value, using the same status check as the
// project secrets API.
func (s *Server) unconfiguredBrunoNativeSecretNames(r *http.Request, projectID string, scripts []*dsl.Script) ([]string, error) {
	seen := make(map[string]struct{})
	for _, script := range scripts {
		for _, name := range scriptSecretReferences(script) {
			seen[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	statuses, err := s.projectSecretStatuses(r, projectID, names)
	if err != nil {
		return nil, err
	}
	unconfigured := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if !status.Configured {
			unconfigured = append(unconfigured, status.Name)
		}
	}
	return unconfigured, nil
}

var brunoSecretReferencePattern = regexp.MustCompile(`\{\{\s*secret\.([^{}\s]+)\s*\}\}`)

// brunoHeaderSecretsAwaitValue reports whether value references a project
// secret that has no usable value yet. During migration that is a secret the
// commit will create from the source credential.
func (s *Server) brunoHeaderSecretsAwaitValue(r *http.Request, projectID, value string) (bool, error) {
	var names []string
	for _, match := range brunoSecretReferencePattern.FindAllStringSubmatch(value, -1) {
		if name, err := engine.NormalizeLogicalSecretName(match[1]); err == nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return false, nil
	}
	statuses, err := s.projectSecretStatuses(r, projectID, names)
	if err != nil {
		return false, err
	}
	for _, status := range statuses {
		if !status.Configured {
			return true, nil
		}
	}
	return false, nil
}

var errBrunoNativeUpdateTargetStale = errors.New("native Bruno update target is stale")

// brunoNativeReceiptUpdatedScriptIDs returns, in receipt order, the scripts a
// replayed batch updated rather than created. A V3 or later receipt's digest
// covers update_targets, so a matching replay names exactly the batch's
// updates. Earlier profiles could not update scripts.
func brunoNativeReceiptUpdatedScriptIDs(receipt *storage.BrunoNativeImportCommitResult, replay brunoCommitRequest) []string {
	updated := []string{}
	if receipt == nil || receipt.Batch == nil {
		return updated
	}
	switch strings.TrimSpace(receipt.Batch.RequestDigestProfile) {
	case storage.ImportBatchDigestBrunoNativeCommitV3, storage.ImportBatchDigestBrunoNativeCommitV4:
	default:
		return updated
	}
	targets := make(map[string]struct{}, len(replay.UpdateTargets))
	for _, target := range replay.UpdateTargets {
		targets[strings.TrimSpace(target.ScriptID)] = struct{}{}
	}
	for _, scriptID := range receipt.Batch.ScriptIDs {
		if _, ok := targets[strings.TrimSpace(scriptID)]; ok {
			updated = append(updated, scriptID)
		}
	}
	return updated
}

// brunoNativeUpdateTargetScript loads the script a re-import updates and
// checks it is still the active script an earlier import of this request
// created, unchanged since review.
func (s *Server) brunoNativeUpdateTargetScript(
	projectID string,
	candidateID string,
	target brunoNativeUpdateTarget,
) (*dsl.Script, error) {
	stale := func(reason string) error {
		return fmt.Errorf("%w: %s; inspect the source again", errBrunoNativeUpdateTargetStale, reason)
	}
	existing, err := s.store.GetScript(target.ScriptID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && existing == nil) {
		return nil, stale("the script to update no longer exists")
	}
	if err != nil {
		return nil, err
	}
	if normalizeProjectID(existing.ProjectID) != projectID {
		return nil, stale("the script to update no longer exists")
	}
	if existing.ArchivedAt != nil {
		return nil, stale("the script to update is archived")
	}
	var existingImport *dsl.ImportMetadata
	if existing.Metadata != nil {
		existingImport = existing.Metadata.Import
	}
	if existingCandidateID, _, _, ok := brunoNativeImportIdentity(existingImport); !ok || existingCandidateID != candidateID {
		return nil, stale("the script to update was not imported from this request")
	}
	if !existing.UpdatedAt.Equal(target.ExpectedUpdatedAt) {
		return nil, stale("the script to update changed after review")
	}
	return existing, nil
}

type brunoNativePlannedHeaderRepair struct {
	repair brunoconvert.NativeHeaderRepair
	choice string
}

// selectedBrunoNativeHeaderRepairs resolves each requested header repair to
// its finding on a selected candidate. Repairs are ordered last header first
// so removing or putting back one header does not shift the next repair's
// position.
func selectedBrunoNativeHeaderRepairs(
	selected []brunoconvert.NativeScriptCandidate,
	requested map[string]string,
) (map[string][]brunoNativePlannedHeaderRepair, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	planned := make(map[string][]brunoNativePlannedHeaderRepair)
	found := make(map[string]struct{}, len(requested))
	for _, candidate := range selected {
		for _, diagnostic := range candidate.Diagnostics {
			choice, wanted := requested[strings.TrimSpace(diagnostic.ID)]
			if !wanted || diagnostic.HeaderRepair == nil {
				continue
			}
			if !slices.Contains(diagnostic.HeaderRepair.Options, choice) {
				return nil, fmt.Errorf("header repair %q is not available for finding %q", choice, diagnostic.ID)
			}
			found[strings.TrimSpace(diagnostic.ID)] = struct{}{}
			planned[candidate.CandidateID] = append(planned[candidate.CandidateID], brunoNativePlannedHeaderRepair{
				repair: *diagnostic.HeaderRepair,
				choice: choice,
			})
		}
	}
	for findingID := range requested {
		if _, ok := found[findingID]; !ok {
			return nil, fmt.Errorf("header repair finding %q is stale or not selected; inspect the source again", findingID)
		}
	}
	for candidateID := range planned {
		repairs := planned[candidateID]
		// Diagnostics list headers in source order. Reversed and then sorted
		// stably, a fix in place (including a left-out Content-Type replacing
		// the body default) runs before a left-out header goes back at the
		// same position, and left-out headers sharing a position go back in
		// source order.
		inPlace := func(repair brunoconvert.NativeHeaderRepair) bool {
			return !repair.Omitted || repair.ReplacesBodyDefault
		}
		slices.Reverse(repairs)
		sort.SliceStable(repairs, func(i, j int) bool {
			left, right := repairs[i].repair, repairs[j].repair
			if left.ActionIndex != right.ActionIndex {
				return left.ActionIndex > right.ActionIndex
			}
			if left.HeaderIndex != right.HeaderIndex {
				return left.HeaderIndex > right.HeaderIndex
			}
			return inPlace(left) && !inPlace(right)
		})
	}
	return planned, nil
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
			if action.Control != nil {
				for _, branch := range action.Control.ActionBranches() {
					if visit(branch.Actions) {
						return true
					}
				}
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
	request.HeaderRepairs, err = normalizeBrunoNativeStringMap("header_repairs", request.HeaderRepairs)
	if err != nil {
		return err
	}
	for findingID, rawChoice := range request.HeaderRepairs {
		choice := strings.ToLower(rawChoice)
		if choice != brunoconvert.HeaderRepairTrim && choice != brunoconvert.HeaderRepairRemove {
			return fmt.Errorf("header repair for %q must be %q or %q", findingID, brunoconvert.HeaderRepairTrim, brunoconvert.HeaderRepairRemove)
		}
		request.HeaderRepairs[findingID] = choice
	}
	targets := make(map[string]brunoNativeUpdateTarget, len(request.UpdateTargets))
	scriptTargets := make(map[string]string, len(request.UpdateTargets))
	for rawCandidateID, target := range request.UpdateTargets {
		candidateID := strings.TrimSpace(rawCandidateID)
		if candidateID == "" {
			return errors.New("update_targets cannot contain an empty candidate id")
		}
		if _, duplicate := targets[candidateID]; duplicate {
			return fmt.Errorf("update_targets contains keys that normalize to the same value %q", candidateID)
		}
		target.ScriptID = strings.TrimSpace(target.ScriptID)
		if target.ScriptID == "" || target.ExpectedUpdatedAt.IsZero() {
			return fmt.Errorf("update target for %q requires script_id and expected_updated_at", candidateID)
		}
		if previous, reused := scriptTargets[target.ScriptID]; reused {
			return fmt.Errorf("update targets for %q and %q name the same script", previous, candidateID)
		}
		scriptTargets[target.ScriptID] = candidateID
		target.ExpectedUpdatedAt = target.ExpectedUpdatedAt.UTC()
		targets[candidateID] = target
	}
	request.UpdateTargets = targets
	selections := make(map[string][]string, len(request.ParameterizationSelections))
	for rawCandidateID, rawIDs := range request.ParameterizationSelections {
		candidateID := strings.TrimSpace(rawCandidateID)
		if candidateID == "" {
			return errors.New("parameterization_selections cannot contain an empty candidate id")
		}
		if _, duplicate := selections[candidateID]; duplicate {
			return fmt.Errorf("parameterization_selections contains keys that normalize to the same value %q", candidateID)
		}
		ids := make([]string, 0, len(rawIDs))
		seen := make(map[string]struct{}, len(rawIDs))
		for _, rawID := range rawIDs {
			id := strings.TrimSpace(rawID)
			if id == "" {
				return fmt.Errorf("parameterization selections for %q contain an empty id", candidateID)
			}
			if _, duplicate := seen[id]; duplicate {
				return fmt.Errorf("parameterization selections for %q contain duplicate id %q", candidateID, id)
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		// A candidate with nothing selected is the same request as one left out.
		if len(ids) > 0 {
			sort.Strings(ids)
			selections[candidateID] = ids
		}
	}
	request.ParameterizationSelections = selections
	bindings := make(map[string]sharedvalue.Binding, len(request.InputBindings))
	for rawID, binding := range request.InputBindings {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return errors.New("input_bindings cannot contain an empty id")
		}
		if _, duplicate := bindings[id]; duplicate {
			return fmt.Errorf("input_bindings contains keys that normalize to the same value %q", id)
		}
		binding.Mode = strings.ToLower(strings.TrimSpace(binding.Mode))
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.Column = strings.TrimSpace(binding.Column)
		binding.Generator = strings.ToLower(strings.TrimSpace(binding.Generator))
		if binding.Mode != sharedvalue.BindingGenerated || binding.Generator != sharedvalue.GeneratorDigits {
			binding.Digits = 0
		}
		if binding.Mode != sharedvalue.BindingData {
			binding.Alias, binding.Column = "", ""
		}
		if binding.Mode != sharedvalue.BindingGenerated {
			binding.Generator = ""
		}
		if err := binding.Validate(); err != nil {
			return fmt.Errorf("input binding for %q: %w", id, err)
		}
		if _, valued := request.SharedValues[id]; valued {
			return fmt.Errorf("input %q has both a value for every user and a binding", id)
		}
		bindings[id] = binding
	}
	request.InputBindings = bindings
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
		if name == "" && !brunoImportCredentialValueUsable(requirement.Value) {
			// An unresolved source expression keeps its generated reference.
			// bindOrMigrateBrunoImportSecrets binds it to the suggested name,
			// so preflight must plan the same binding.
			name = suggested
		}
		if name == "" {
			// The source value is preserved in the destination by default.
			replacements[requirementID] = requirement.Value
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
const brunoNativeCommitCurrentDigestProfile = storage.ImportBatchDigestBrunoNativeCommitV4

func brunoNativeCommitRequestSHA(request brunoCommitRequest) (string, error) {
	return brunoNativeCommitRequestSHAForProfile(brunoNativeCommitCurrentDigestProfile, request)
}

func brunoNativeCommitRequestSHAForProfile(profile string, request brunoCommitRequest) (string, error) {
	switch strings.TrimSpace(profile) {
	case storage.ImportBatchDigestBrunoNativeCommitV1:
		return brunoNativeCommitRequestSHAV1(request)
	case storage.ImportBatchDigestBrunoNativeCommitV2:
		return brunoNativeCommitRequestSHAV2(request)
	case storage.ImportBatchDigestBrunoNativeCommitV3:
		return brunoNativeCommitRequestSHAV3(request)
	case storage.ImportBatchDigestBrunoNativeCommitV4:
		return brunoNativeCommitRequestSHAV4(request)
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

// brunoNativeCommitRequestSHAV3 extends V2 with update_targets and
// header_repairs. Both change what is saved, so a retry that changes either
// must not replay an earlier batch.
//
// Like V1 and V2, this implementation is frozen once receipts exist under it.
func brunoNativeCommitRequestSHAV3(request brunoCommitRequest) (string, error) {
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
	type updateTarget struct {
		ScriptID          string `json:"script_id"`
		ExpectedUpdatedAt string `json:"expected_updated_at"`
	}
	updateTargets := make(map[string]updateTarget, len(request.UpdateTargets))
	for candidateID, target := range request.UpdateTargets {
		updateTargets[strings.TrimSpace(candidateID)] = updateTarget{
			ScriptID:          strings.TrimSpace(target.ScriptID),
			ExpectedUpdatedAt: target.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	headerRepairs := request.HeaderRepairs
	if headerRepairs == nil {
		headerRepairs = map[string]string{}
	}
	payload, err := json.Marshal(struct {
		ProjectID              string                  `json:"project_id"`
		ImportSessionID        string                  `json:"import_session_id"`
		Mode                   string                  `json:"mode"`
		FolderID               string                  `json:"folder_id"`
		CandidateIDs           []string                `json:"candidate_ids"`
		NameOverrides          map[string]string       `json:"name_overrides"`
		SecretBindings         map[string]string       `json:"secret_bindings"`
		SharedValues           map[string]string       `json:"shared_values"`
		UpdateTargets          map[string]updateTarget `json:"update_targets"`
		HeaderRepairs          map[string]string       `json:"header_repairs"`
		MigrateSecrets         bool                    `json:"migrate_secrets"`
		AcknowledgedFindingIDs []string                `json:"acknowledged_finding_ids"`
		CandidateIDsOmitted    bool                    `json:"candidate_ids_omitted"`
		NameOverridesOmitted   bool                    `json:"name_overrides_omitted"`
	}{
		ProjectID:              normalizeProjectID(request.ProjectID),
		ImportSessionID:        strings.TrimSpace(request.ImportSessionID),
		Mode:                   "create_native_batch",
		FolderID:               folderID,
		CandidateIDs:           candidateIDs,
		NameOverrides:          request.NameOverrides,
		SecretBindings:         request.SecretBindings,
		SharedValues:           request.SharedValues,
		UpdateTargets:          updateTargets,
		HeaderRepairs:          headerRepairs,
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

// brunoNativeCommitRequestSHAV4 is immutable once receipts exist. It hashes
// the V3 digest together with parameterization_selections, so V3 stays as
// frozen and the selections are covered.
func brunoNativeCommitRequestSHAV4(request brunoCommitRequest) (string, error) {
	v3, err := brunoNativeCommitRequestSHAV3(request)
	if err != nil {
		return "", err
	}
	selections := make(map[string][]string, len(request.ParameterizationSelections))
	for candidateID, ids := range request.ParameterizationSelections {
		if len(ids) == 0 {
			continue
		}
		sorted := append([]string(nil), ids...)
		sort.Strings(sorted)
		selections[strings.TrimSpace(candidateID)] = sorted
	}
	payload, err := json.Marshal(struct {
		Profile                    string                         `json:"profile"`
		V3RequestSHA256            string                         `json:"v3_request_sha256"`
		ParameterizationSelections map[string][]string            `json:"parameterization_selections"`
		InputBindings              map[string]sharedvalue.Binding `json:"input_bindings,omitempty"`
	}{
		Profile:                    storage.ImportBatchDigestBrunoNativeCommitV4,
		V3RequestSHA256:            v3,
		ParameterizationSelections: selections,
		InputBindings:              request.InputBindings,
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
	case errors.Is(err, storage.ErrObjectArchived):
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
	case errors.Is(err, storage.ErrObjectArchived):
		return "script_archived"
	case errors.Is(err, storage.ErrBrunoRevisionNotFound):
		return "revision_not_found"
	default:
		return "storage_error"
	}
}

// handleGetImportSource serves the raw or sanitized artifact an import retained.
// It is not Bruno-specific: any import routed through a protected session can
// retain its upload, and the active-revision lookup is the real gate — a script
// with nothing retained 404s regardless of which importer produced it.
func (s *Server) handleGetImportSource(w http.ResponseWriter, r *http.Request) {
	extendArtifactWriteDeadline(w)
	brunoStore, ok := s.store.(storage.ImportSourceStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "import source retention is not configured")
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
	if raw && !s.canReadRawImportSource(r, projectID) {
		s.recordAuditEvent(r, "script.import.bruno.source.raw", "denied", "script", script.ID, projectID, map[string]any{"reason": "insufficient_role"})
		writeError(w, http.StatusForbidden, "raw import source access requires project_admin or system_admin permission")
		return
	}
	revision, err := brunoStore.GetActiveImportSourceRevision(script.ID, projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no retained import source revision is active for this script")
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
	if !raw && privateKeyFiles && !s.canReadRawImportSource(r, projectID) {
		s.recordAuditEvent(r, "script.import.bruno.source.sanitized", "denied", "script", script.ID, projectID, map[string]any{"reason": "private_key_material"})
		writeError(w, http.StatusForbidden, "this import source bundle contains private-key files and requires project_admin or system_admin permission")
		return
	}
	artifactID := revision.SanitizedArtifactID
	if raw {
		artifactID = revision.RawArtifactID
	}
	artifact, err := brunoStore.GetImportSourceArtifact(artifactID, projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "retained import source artifact not found")
		return
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "import source decryption is unavailable: "+err.Error())
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
		writeError(w, http.StatusInternalServerError, "open retained import source artifact: "+err.Error())
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

func brunoRevisionHasPrivateKeyFiles(revision *storage.ImportSourceRevision) bool {
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

func (s *Server) handleListImportSourceRevisions(w http.ResponseWriter, r *http.Request) {
	brunoStore, ok := s.store.(storage.ImportSourceStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "import source retention is not configured")
		return
	}
	script, err := getPublishedImportScript(s.store, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	revisions, err := brunoStore.ListImportSourceRevisions(script.ID, normalizeProjectID(script.ProjectID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	public := make([]*storage.ImportSourceRevision, 0, len(revisions))
	for _, revision := range revisions {
		public = append(public, publicBrunoRevision(revision))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": public, "count": len(public)})
}

func (s *Server) handleRollbackImportSource(w http.ResponseWriter, r *http.Request) {
	brunoStore, ok := s.store.(storage.ImportSourceStore)
	if !ok || brunoStore == nil {
		writeError(w, http.StatusServiceUnavailable, "import source retention is not configured")
		return
	}
	script, err := getPublishedImportScript(s.store, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	var request struct {
		RevisionID string `json:"revision_id"`
	}
	if err := decodeRequestBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	revision, err := brunoStore.GetImportSourceRevision(strings.TrimSpace(request.RevisionID), normalizeProjectID(script.ProjectID))
	if err != nil || revision == nil || revision.ScriptID != script.ID {
		writeError(w, http.StatusNotFound, "retained import source revision not found")
		return
	}
	if isBrunoRollbackRevision(revision) {
		writeBrunoRunnerRetired(w)
		return
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": "rollback is unavailable for this retained import source; import the source again to create a native script",
		"code":  "import_rollback_unsupported_source",
	})
}

func isBrunoRollbackRevision(revision *storage.ImportSourceRevision) bool {
	if revision == nil || revision.CanonicalScript == nil {
		return false
	}
	err := engine.RetiredBrunoRuntimeError(revision.CanonicalScript)
	return err != nil && strings.Contains(err.Error(), dsl.BrunoRunnerRetiredMessage)
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

func brunoArtifactOpenRequest(artifact storage.ImportSourceArtifact) importartifact.OpenRequest {
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
	if cleanupErr := s.cleanupUnreferencedBrunoArtifactsLocked(objects, []storage.ImportSourceArtifact{
		session.RawArtifact,
		session.SanitizedArtifact,
	}, session.ID); cleanupErr != nil {
		slog.Warn("clean Bruno import session artifacts", "session_id", session.ID, "error", cleanupErr)
	}
}

func (s *Server) cleanupUnreferencedBrunoArtifacts(artifacts []storage.ImportSourceArtifact) {
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
	artifacts []storage.ImportSourceArtifact,
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
	// receipt), preserving the import flow's anti-enumeration ordering. It
	// names only the deployment policy or caller role that blocked migration.
	s.writeSourceSecretMigrationDenied(w, r, projectID)
	return false
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

func (s *Server) canReadRawImportSource(r *http.Request, projectID string) bool {
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

func publicBrunoRevision(revision *storage.ImportSourceRevision) *storage.ImportSourceRevision {
	if revision == nil {
		return nil
	}
	payload, _ := json.Marshal(revision)
	var clone storage.ImportSourceRevision
	_ = json.Unmarshal(payload, &clone)
	clone.CanonicalScript = nil
	clone.RawArtifactID = ""
	return &clone
}

func brunoSanitizedArtifactPublicSummary(artifact storage.ImportSourceArtifact) brunoArtifactSummary {
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
