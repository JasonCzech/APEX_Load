package storage

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/refkey"
)

// CommitImportBatch creates an entire per-request import in one database
// transaction. The receipt is inserted before the scripts so the
// project/session idempotency key also serializes concurrent commits.
func (s *SQLiteStore) CommitImportBatch(commit *ImportBatchCommit) (*ImportBatchCommitResult, error) {
	if commit == nil {
		return nil, errors.New("import batch commit is required")
	}
	// Normalization, generated IDs, timestamps, artifact metadata, and
	// reference keys all belong to the transaction's working state. Keeping
	// them off the caller object makes a rolled-back commit safe to repair and
	// retry without carrying phantom allocations into the next transaction.
	callerCommit := commit
	workingCommit, err := cloneImportBatchCommit(commit)
	if err != nil {
		return nil, fmt.Errorf("clone import batch commit: %w", err)
	}
	commit = workingCommit
	batch := &commit.Receipt
	batch.ID = strings.TrimSpace(batch.ID)
	if batch.ID == "" {
		batch.ID = uuid.NewString()
	}
	batch.ProjectID = normalizeProjectID(strings.TrimSpace(batch.ProjectID))
	batch.Profile = strings.TrimSpace(batch.Profile)
	batch.RequestDigestProfile = strings.TrimSpace(batch.RequestDigestProfile)
	batch.IdempotencyKey = strings.TrimSpace(batch.IdempotencyKey)
	batch.RequestSHA256 = strings.ToLower(strings.TrimSpace(batch.RequestSHA256))
	if batch.Profile == "" || batch.ReceiptVersion <= 0 || batch.RequestDigestProfile == "" ||
		batch.IdempotencyKey == "" || batch.RequestSHA256 == "" {
		return nil, errors.New("import batch profile, version, idempotency key, and request digest are required")
	}
	if len(commit.Scripts) == 0 {
		return nil, errors.New("import batch requires at least one script")
	}
	if len(batch.CandidateIDs) != len(commit.Scripts) {
		return nil, errors.New("import batch candidate and script counts must match")
	}
	if len(commit.ExpectedUpdatedAt) != 0 && len(commit.ExpectedUpdatedAt) != len(commit.Scripts) {
		return nil, errors.New("import batch expected update times and script counts must match")
	}
	if batch.CreatedAt.IsZero() {
		batch.CreatedAt = time.Now().UTC()
	}

	seenCandidates := make(map[string]struct{}, len(batch.CandidateIDs))
	seenScripts := make(map[string]struct{}, len(commit.Scripts))
	seenRefKeys := make(map[string]struct{}, len(commit.Scripts))
	suppliedScriptIDs := append([]string(nil), batch.ScriptIDs...)
	if len(suppliedScriptIDs) != 0 && len(suppliedScriptIDs) != len(commit.Scripts) {
		return nil, errors.New("import batch receipt and script counts must match")
	}
	batch.ScriptIDs = make([]string, 0, len(commit.Scripts))
	for index, candidateID := range batch.CandidateIDs {
		candidateID = strings.TrimSpace(candidateID)
		if candidateID == "" {
			return nil, fmt.Errorf("import batch candidate %d has no id", index+1)
		}
		if _, duplicate := seenCandidates[candidateID]; duplicate {
			return nil, fmt.Errorf("import batch contains duplicate candidate id %q", candidateID)
		}
		seenCandidates[candidateID] = struct{}{}
		batch.CandidateIDs[index] = candidateID

		script := commit.Scripts[index]
		if script == nil {
			return nil, fmt.Errorf("import batch script %d is nil", index+1)
		}
		script.ID = strings.TrimSpace(script.ID)
		script.ProjectID = normalizeProjectID(strings.TrimSpace(script.ProjectID))
		script.RefKey = strings.TrimSpace(script.RefKey)
		if script.ID == "" || script.ProjectID != batch.ProjectID {
			return nil, fmt.Errorf("import batch script %d has an invalid id or project", index+1)
		}
		if strings.TrimSpace(script.Protocol) == "" {
			return nil, fmt.Errorf("import batch script %d requires a protocol", index+1)
		}
		// An updated script keeps its current placement, which may be the
		// project root. A created script needs a destination folder.
		if !importBatchRowUpdates(commit, index) &&
			(script.FolderID == nil || strings.TrimSpace(*script.FolderID) == "") {
			return nil, fmt.Errorf("import batch script %d requires a destination folder", index+1)
		}
		if _, duplicate := seenScripts[script.ID]; duplicate {
			return nil, fmt.Errorf("import batch contains duplicate script id %q", script.ID)
		}
		if len(suppliedScriptIDs) != 0 && strings.TrimSpace(suppliedScriptIDs[index]) != script.ID {
			return nil, fmt.Errorf("import batch receipt script id %d does not match its script", index+1)
		}
		seenScripts[script.ID] = struct{}{}
		if script.RefKey != "" {
			if _, duplicate := seenRefKeys[script.RefKey]; duplicate {
				return nil, fmt.Errorf("import batch contains duplicate script ref key %q", script.RefKey)
			}
			seenRefKeys[script.RefKey] = struct{}{}
		}
		if err := validateImportBatchCredentialSafety(
			script,
			commit.TrustPreservedExecutableCredentials,
		); err != nil {
			return nil, fmt.Errorf("validate import batch script %d: %w", index+1, err)
		}
		batch.ScriptIDs = append(batch.ScriptIDs, script.ID)
	}

	if existing, err := s.GetImportBatchByIdempotencyKey(batch.ProjectID, batch.Profile, batch.IdempotencyKey); err == nil {
		if existing.Receipt.RequestSHA256 != batch.RequestSHA256 ||
			existing.Receipt.RequestDigestProfile != batch.RequestDigestProfile ||
			existing.Receipt.ReceiptVersion != batch.ReceiptVersion {
			return nil, ErrImportBatchConflict
		}
		// A matching receipt is already durable, so publishing its immutable
		// state is safe and keeps retry callers aligned with the successful
		// create path without exposing any uncommitted working mutations.
		publishImportBatchCommitSuccess(callerCommit, commit, existing)
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if err := validateImportBatchArtifactScope(&commit.RawArtifact, batch.ProjectID, "raw"); err != nil {
		return nil, err
	}
	if err := normalizeBrunoArtifact(&commit.RawArtifact, batch.ProjectID, "raw"); err != nil {
		return nil, err
	}
	if err := validateImportBatchArtifactScope(&commit.SanitizedArtifact, batch.ProjectID, "sanitized"); err != nil {
		return nil, err
	}
	if err := normalizeBrunoArtifact(&commit.SanitizedArtifact, batch.ProjectID, "sanitized"); err != nil {
		return nil, err
	}

	candidateJSON, err := json.Marshal(batch.CandidateIDs)
	if err != nil {
		return nil, err
	}
	scriptJSON, err := json.Marshal(batch.ScriptIDs)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rawArtifact, err := saveBrunoArtifactTx(tx, &commit.RawArtifact)
	if err != nil {
		return nil, err
	}
	sanitizedArtifact, err := saveBrunoArtifactTx(tx, &commit.SanitizedArtifact)
	if err != nil {
		return nil, err
	}
	batch.RawArtifactID = rawArtifact.ID
	batch.SanitizedArtifactID = sanitizedArtifact.ID

	receiptResult, err := tx.Exec(`INSERT INTO bruno_native_import_batches (
		id, project_id, source_profile, receipt_version, idempotency_key,
		request_digest_profile, request_sha256, raw_artifact_id, sanitized_artifact_id,
		candidate_ids_json, script_ids_json, scripts_json, created_by, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?)
	ON CONFLICT(project_id, idempotency_key) DO NOTHING`,
		batch.ID, batch.ProjectID, batch.Profile, batch.ReceiptVersion, batch.IdempotencyKey,
		batch.RequestDigestProfile, batch.RequestSHA256,
		batch.RawArtifactID, batch.SanitizedArtifactID, string(candidateJSON), string(scriptJSON),
		nullableString(batch.CreatedBy), batch.CreatedAt)
	if err != nil {
		return nil, err
	}
	receiptRows, err := receiptResult.RowsAffected()
	if err != nil {
		return nil, err
	}
	if receiptRows != 1 {
		_ = tx.Rollback()
		existing, loadErr := s.getImportBatchByIdempotencyKeyAnyProfile(batch.ProjectID, batch.IdempotencyKey)
		if loadErr != nil {
			return nil, loadErr
		}
		if existing.Receipt.Profile != batch.Profile ||
			existing.Receipt.RequestSHA256 != batch.RequestSHA256 ||
			existing.Receipt.RequestDigestProfile != batch.RequestDigestProfile ||
			existing.Receipt.ReceiptVersion != batch.ReceiptVersion {
			return nil, ErrImportBatchConflict
		}
		return existing, nil
	}

	encodedScripts := make([]storedScriptJSON, len(commit.Scripts))
	for i, script := range commit.Scripts {
		if importBatchRowUpdates(commit, i) {
			refKey, refErr := nextImportBatchScriptVersionRefKeyTx(tx, script, commit.RefKeyEnv)
			if refErr != nil {
				return nil, fmt.Errorf("allocate import batch script %d version reference key: %w", i+1, refErr)
			}
			script.RefKey = refKey
		} else if strings.TrimSpace(script.RefKey) == "" {
			sequence, sequenceErr := nextImportBatchScriptSequenceTx(tx, commit.RefKeyEnv)
			if sequenceErr != nil {
				return nil, fmt.Errorf("allocate import batch script %d reference key: %w", i+1, sequenceErr)
			}
			script.RefKey = refkey.Script(commit.RefKeyEnv, sequence, 1)
		}
		encoded, encodeErr := marshalScriptForStorage(script)
		if encodeErr != nil {
			return nil, fmt.Errorf("marshal import batch script %d: %w", i+1, encodeErr)
		}
		encodedScripts[i] = encoded
	}

	for i, script := range commit.Scripts {
		if err := validateScriptFolderProjectTx(tx, batch.ProjectID, script.FolderID); err != nil {
			return nil, err
		}
		// A created script must land in a script folder. An updated script
		// keeps whatever placement it already has.
		if !importBatchRowUpdates(commit, i) && script.FolderID != nil && strings.TrimSpace(*script.FolderID) != "" {
			var folderCategory string
			if err := tx.QueryRow(`SELECT category FROM folders WHERE id = ?`, strings.TrimSpace(*script.FolderID)).Scan(&folderCategory); err != nil {
				return nil, err
			}
			if !strings.EqualFold(strings.TrimSpace(folderCategory), "scripts") {
				return nil, fmt.Errorf("import batch script %d destination must be a script folder", i+1)
			}
		}
		if err := validateScriptObjectRepositoryProjectsTx(tx, batch.ProjectID, script); err != nil {
			return nil, err
		}
		var expectedUpdatedAt *time.Time
		if importBatchRowUpdates(commit, i) {
			expectedUpdatedAt = commit.ExpectedUpdatedAt[i]
		}
		created, err := upsertBrunoImportScriptTx(tx, script, encodedScripts[i], expectedUpdatedAt)
		if errors.Is(err, ErrBrunoImportConcurrentChange) {
			return nil, ErrImportBatchConcurrentChange
		}
		if err != nil {
			return nil, err
		}
		if created == (expectedUpdatedAt != nil) {
			return nil, ErrImportBatchConcurrentChange
		}
	}
	receiptScripts, err := disclosureSafeImportBatchReceiptScripts(commit.Scripts)
	if err != nil {
		return nil, err
	}
	receiptScriptsJSON, err := json.Marshal(receiptScripts)
	if err != nil {
		return nil, fmt.Errorf("marshal import batch receipt scripts: %w", err)
	}
	result, err := tx.Exec(`UPDATE bruno_native_import_batches SET scripts_json = ? WHERE id = ? AND project_id = ?`,
		string(receiptScriptsJSON), batch.ID, batch.ProjectID)
	if err != nil {
		return nil, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		return nil, errors.New("import batch receipt disappeared during commit")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	commitResult := &ImportBatchCommitResult{
		Receipt: cloneImportBatchReceipt(batch),
		Scripts: cloneBrunoNativeScripts(commit.Scripts),
		Created: true,
	}
	publishImportBatchCommitSuccess(callerCommit, commit, commitResult)
	return commitResult, nil
}

// importBatchRowUpdates reports whether the script at index updates an
// existing script rather than creating one.
func importBatchRowUpdates(commit *ImportBatchCommit, index int) bool {
	return index < len(commit.ExpectedUpdatedAt) &&
		commit.ExpectedUpdatedAt[index] != nil &&
		!commit.ExpectedUpdatedAt[index].IsZero()
}

// nextImportBatchScriptVersionRefKeyTx returns the reference key for the next
// version of an existing script, matching an ordinary script update. An
// import update keeps the script's placement, so a different folder is
// refused rather than applied as a move.
func nextImportBatchScriptVersionRefKeyTx(tx *storeTx, script *dsl.Script, environment string) (string, error) {
	var current string
	var currentFolder sql.NullString
	err := tx.QueryRow(`SELECT COALESCE(ref_key, ''), folder_id FROM scripts WHERE id = ? AND project_id = ?`,
		script.ID, script.ProjectID).Scan(&current, &currentFolder)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrImportBatchConcurrentChange
	}
	if err != nil {
		return "", err
	}
	requestedFolder := ""
	if script.FolderID != nil {
		requestedFolder = strings.TrimSpace(*script.FolderID)
	}
	if requestedFolder != strings.TrimSpace(currentFolder.String) {
		// The API sends the folder it read, so a difference means the script
		// was moved after review.
		return "", fmt.Errorf("%w: an import update cannot move a script to another folder", ErrImportBatchConcurrentChange)
	}
	if parts, ok := refkey.Parse(strings.ToUpper(strings.TrimSpace(current))); ok && parts.Type == refkey.TypeScript {
		return refkey.Script(parts.Env, parts.Seq, parts.Version+1), nil
	}
	sequence, err := nextImportBatchScriptSequenceTx(tx, environment)
	if err != nil {
		return "", err
	}
	return refkey.Script(environment, sequence, 1), nil
}

func cloneImportBatchCommit(commit *ImportBatchCommit) (*ImportBatchCommit, error) {
	trustPreservedExecutableCredentials := commit.TrustPreservedExecutableCredentials
	payload, err := json.Marshal(commit)
	if err != nil {
		return nil, err
	}
	var clone ImportBatchCommit
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&clone); err != nil {
		return nil, err
	}
	clone.TrustPreservedExecutableCredentials = trustPreservedExecutableCredentials
	return &clone, nil
}

func publishImportBatchCommitSuccess(
	destination *ImportBatchCommit,
	working *ImportBatchCommit,
	result *ImportBatchCommitResult,
) {
	// Preserve the established success-side API: callers observe normalized
	// receipt/artifact metadata and the script timestamps/reference keys that
	// were made durable. This is called only after tx.Commit succeeds.
	if destination == nil || working == nil || result == nil {
		return
	}
	if result.Receipt != nil {
		destination.Receipt = *cloneImportBatchReceipt(result.Receipt)
	}
	publishNormalizedImportArtifact(&destination.RawArtifact, &working.RawArtifact)
	publishNormalizedImportArtifact(&destination.SanitizedArtifact, &working.SanitizedArtifact)
	for index, script := range result.Scripts {
		if index >= len(destination.Scripts) || destination.Scripts[index] == nil || script == nil {
			continue
		}
		destination.Scripts[index].ID = script.ID
		destination.Scripts[index].ProjectID = script.ProjectID
		destination.Scripts[index].RefKey = script.RefKey
		destination.Scripts[index].CreatedAt = script.CreatedAt
		destination.Scripts[index].UpdatedAt = script.UpdatedAt
	}
}

func publishNormalizedImportArtifact(destination, working *ImportSourceArtifact) {
	if destination == nil || working == nil {
		return
	}
	destination.ID = working.ID
	destination.ProjectID = working.ProjectID
	destination.ArtifactKind = working.ArtifactKind
	destination.BackendKey = working.BackendKey
	destination.SHA256 = working.SHA256
	destination.LifecycleState = working.LifecycleState
	destination.CreatedAt = working.CreatedAt
}

func validateImportBatchArtifactScope(artifact *ImportSourceArtifact, projectID, kind string) error {
	if artifact == nil {
		return errors.New("import source artifact is required")
	}
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	if suppliedProjectID := strings.TrimSpace(artifact.ProjectID); suppliedProjectID != "" &&
		normalizeProjectID(suppliedProjectID) != projectID {
		return errors.New("import source artifact belongs to another project")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if suppliedKind := strings.ToLower(strings.TrimSpace(artifact.ArtifactKind)); suppliedKind != "" &&
		suppliedKind != kind {
		return fmt.Errorf("import source artifact kind %q does not match %q", suppliedKind, kind)
	}
	return nil
}

func nextImportBatchScriptSequenceTx(tx *storeTx, environment string) (int, error) {
	settingKey := fmt.Sprintf("refseq:%s:%s", refkey.EnvCode(environment), refkey.TypeScript)
	var raw string
	err := tx.QueryRow(`INSERT INTO app_settings (key, value, updated_at) VALUES (?, '1', ?)
		ON CONFLICT(key) DO UPDATE SET value=CAST(app_settings.value AS INTEGER)+1, updated_at=excluded.updated_at
		RETURNING value`, settingKey, time.Now().UTC()).Scan(&raw)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid script reference-key sequence %q", raw)
	}
	return value, nil
}

func (s *SQLiteStore) GetImportBatchByIdempotencyKey(projectID, profile, idempotencyKey string) (*ImportBatchCommitResult, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	profile = strings.TrimSpace(profile)
	return s.getImportBatchByIdempotencyKey(projectID, profile, idempotencyKey, true)
}

func (s *SQLiteStore) getImportBatchByIdempotencyKeyAnyProfile(projectID, idempotencyKey string) (*ImportBatchCommitResult, error) {
	return s.getImportBatchByIdempotencyKey(
		normalizeProjectID(strings.TrimSpace(projectID)),
		"",
		strings.TrimSpace(idempotencyKey),
		false,
	)
}

func (s *SQLiteStore) getImportBatchByIdempotencyKey(
	projectID, profile, idempotencyKey string,
	filterProfile bool,
) (*ImportBatchCommitResult, error) {
	var batch ImportBatchReceipt
	var candidateJSON, scriptIDJSON, scriptsJSON string
	query := `SELECT id, project_id, source_profile, receipt_version, idempotency_key,
		request_digest_profile, request_sha256,
		raw_artifact_id, sanitized_artifact_id, candidate_ids_json, script_ids_json, scripts_json,
		COALESCE(created_by, ''), created_at
		FROM bruno_native_import_batches WHERE project_id = ? AND idempotency_key = ?`
	args := []any{projectID, idempotencyKey}
	if filterProfile {
		query += ` AND source_profile = ?`
		args = append(args, profile)
	}
	if err := s.db.QueryRow(query, args...).Scan(
		&batch.ID, &batch.ProjectID, &batch.Profile, &batch.ReceiptVersion,
		&batch.IdempotencyKey, &batch.RequestDigestProfile, &batch.RequestSHA256,
		&batch.RawArtifactID, &batch.SanitizedArtifactID, &candidateJSON, &scriptIDJSON, &scriptsJSON,
		&batch.CreatedBy, &batch.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(candidateJSON), &batch.CandidateIDs); err != nil {
		return nil, fmt.Errorf("decode import batch candidate ids: %w", err)
	}
	if err := json.Unmarshal([]byte(scriptIDJSON), &batch.ScriptIDs); err != nil {
		return nil, fmt.Errorf("decode import batch script ids: %w", err)
	}
	if len(batch.CandidateIDs) != len(batch.ScriptIDs) {
		return nil, errors.New("import batch receipt candidate and script counts do not match")
	}
	seenCandidates := make(map[string]struct{}, len(batch.CandidateIDs))
	seenScripts := make(map[string]struct{}, len(batch.ScriptIDs))
	for index := range batch.CandidateIDs {
		candidateID := strings.TrimSpace(batch.CandidateIDs[index])
		scriptID := strings.TrimSpace(batch.ScriptIDs[index])
		if candidateID == "" || scriptID == "" {
			return nil, errors.New("import batch receipt contains an empty candidate or script id")
		}
		if _, duplicate := seenCandidates[candidateID]; duplicate {
			return nil, errors.New("import batch receipt contains duplicate candidate ids")
		}
		if _, duplicate := seenScripts[scriptID]; duplicate {
			return nil, errors.New("import batch receipt contains duplicate script ids")
		}
		seenCandidates[candidateID] = struct{}{}
		seenScripts[scriptID] = struct{}{}
		batch.CandidateIDs[index] = candidateID
		batch.ScriptIDs[index] = scriptID
	}
	var scripts []*dsl.Script
	if strings.TrimSpace(scriptsJSON) != "" && strings.TrimSpace(scriptsJSON) != "[]" {
		if err := json.Unmarshal([]byte(scriptsJSON), &scripts); err != nil {
			return nil, fmt.Errorf("decode import batch receipt scripts: %w", err)
		}
		if len(scripts) != len(batch.ScriptIDs) {
			return nil, errors.New("import batch receipt script count does not match its script ids")
		}
		for index, script := range scripts {
			if script == nil || script.ID != batch.ScriptIDs[index] || normalizeProjectID(script.ProjectID) != projectID {
				return nil, errors.New("import batch receipt contains a mismatched script snapshot")
			}
		}
	} else {
		// Compatibility fallback for a batch row created before immutable
		// snapshots were introduced. New commits always populate scripts_json
		// in the same transaction as their script rows.
		scripts = make([]*dsl.Script, 0, len(batch.ScriptIDs))
		for _, scriptID := range batch.ScriptIDs {
			script, err := s.GetScript(scriptID)
			if err != nil {
				return nil, fmt.Errorf("load import batch script %s: %w", scriptID, err)
			}
			if normalizeProjectID(script.ProjectID) != projectID {
				return nil, errors.New("import batch script belongs to another project")
			}
			scripts = append(scripts, script)
		}
	}
	return &ImportBatchCommitResult{
		Receipt: cloneImportBatchReceipt(&batch),
		Scripts: cloneBrunoNativeScripts(scripts),
		Created: false,
	}, nil
}

func cloneImportBatchReceipt(batch *ImportBatchReceipt) *ImportBatchReceipt {
	if batch == nil {
		return nil
	}
	clone := *batch
	clone.CandidateIDs = append([]string(nil), batch.CandidateIDs...)
	clone.ScriptIDs = append([]string(nil), batch.ScriptIDs...)
	return &clone
}

func cloneBrunoNativeScripts(scripts []*dsl.Script) []*dsl.Script {
	out := make([]*dsl.Script, 0, len(scripts))
	for _, script := range scripts {
		out = append(out, cloneBrunoScript(script))
	}
	return out
}

// CommitBrunoNativeImport preserves the established Bruno storage API while
// delegating its transaction to the importer-neutral batch implementation.
func (s *SQLiteStore) CommitBrunoNativeImport(commit *BrunoNativeImportCommit) (*BrunoNativeImportCommitResult, error) {
	if commit == nil {
		return nil, errors.New("Bruno native import commit is required")
	}
	for index, script := range commit.Scripts {
		if script != nil && !strings.EqualFold(strings.TrimSpace(script.Protocol), "http") {
			return nil, fmt.Errorf("Bruno native import script %d protocol must be http", index+1)
		}
	}
	generic := &ImportBatchCommit{
		Receipt: ImportBatchReceipt{
			ID:                   commit.Batch.ID,
			ProjectID:            commit.Batch.ProjectID,
			Profile:              ImportBatchProfileBrunoNative,
			ReceiptVersion:       ImportBatchReceiptVersion1,
			IdempotencyKey:       commit.Batch.IdempotencyKey,
			RequestDigestProfile: digestProfileOrDefault(commit.Batch.RequestDigestProfile),
			RequestSHA256:        commit.Batch.RequestSHA256,
			RawArtifactID:        commit.Batch.RawArtifactID,
			SanitizedArtifactID:  commit.Batch.SanitizedArtifactID,
			CandidateIDs:         append([]string(nil), commit.Batch.CandidateIDs...),
			ScriptIDs:            append([]string(nil), commit.Batch.ScriptIDs...),
			CreatedBy:            commit.Batch.CreatedBy,
			CreatedAt:            commit.Batch.CreatedAt,
		},
		Scripts:           commit.Scripts,
		RawArtifact:       commit.RawArtifact,
		SanitizedArtifact: commit.SanitizedArtifact,
		// The Bruno adapter is a trusted server-side source adapter. The generic
		// storage boundary still requires each script's durable preservation
		// marker before it will retain executable credential literals.
		TrustPreservedExecutableCredentials: true,
		RefKeyEnv:                           commit.RefKeyEnv,
		ExpectedUpdatedAt:                   commit.ExpectedUpdatedAt,
	}
	result, err := s.CommitImportBatch(generic)
	if err != nil {
		switch {
		case errors.Is(err, ErrImportBatchConflict):
			return nil, ErrBrunoNativeBatchConflict
		case errors.Is(err, ErrImportBatchConcurrentChange):
			return nil, ErrBrunoImportConcurrentChange
		}
		return nil, err
	}
	commit.RawArtifact = generic.RawArtifact
	commit.SanitizedArtifact = generic.SanitizedArtifact
	commit.Batch = brunoNativeBatchFromImportReceipt(generic.Receipt)
	return brunoNativeResultFromImportBatch(result), nil
}

func (s *SQLiteStore) GetBrunoNativeImportByIdempotencyKey(
	idempotencyKey string,
	projectID string,
) (*BrunoNativeImportCommitResult, error) {
	result, err := s.GetImportBatchByIdempotencyKey(
		projectID, ImportBatchProfileBrunoNative, idempotencyKey,
	)
	if err != nil {
		return nil, err
	}
	return brunoNativeResultFromImportBatch(result), nil
}

func brunoNativeResultFromImportBatch(result *ImportBatchCommitResult) *BrunoNativeImportCommitResult {
	if result == nil {
		return nil
	}
	return &BrunoNativeImportCommitResult{
		Batch:   brunoNativeBatchPtrFromImportReceipt(result.Receipt),
		Scripts: cloneBrunoNativeScripts(result.Scripts),
		Created: result.Created,
	}
}

func brunoNativeBatchPtrFromImportReceipt(receipt *ImportBatchReceipt) *BrunoNativeImportBatch {
	if receipt == nil {
		return nil
	}
	batch := brunoNativeBatchFromImportReceipt(*receipt)
	return &batch
}

func brunoNativeBatchFromImportReceipt(receipt ImportBatchReceipt) BrunoNativeImportBatch {
	return BrunoNativeImportBatch{
		ID:                   receipt.ID,
		ProjectID:            receipt.ProjectID,
		IdempotencyKey:       receipt.IdempotencyKey,
		RequestDigestProfile: digestProfileOrDefault(receipt.RequestDigestProfile),
		RequestSHA256:        receipt.RequestSHA256,
		RawArtifactID:        receipt.RawArtifactID,
		SanitizedArtifactID:  receipt.SanitizedArtifactID,
		CandidateIDs:         append([]string(nil), receipt.CandidateIDs...),
		ScriptIDs:            append([]string(nil), receipt.ScriptIDs...),
		CreatedBy:            receipt.CreatedBy,
		CreatedAt:            receipt.CreatedAt,
	}
}

// digestProfileOrDefault treats a blank profile as V1. Receipts written before
// the profile was plumbed through carry no value, and they were all produced by
// the V1 hash.
func digestProfileOrDefault(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return ImportBatchDigestBrunoNativeCommitV1
	}
	return strings.TrimSpace(profile)
}
