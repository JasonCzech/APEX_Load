package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/refkey"
)

// Per-script recorded baselines. The recorded exchanges are stored as a JSON
// blob in a dedicated table (not in the hot scripts table) so they are loaded
// only on demand, when diagnosing record-vs-replay correlation.

// maxRecordingObservations caps how many exchanges a baseline persists, to keep
// the blob bounded regardless of recording size.
const maxRecordingObservations = 500

const (
	maxLegacyRecordingObservationsJSONSize = 12 << 20
	// Current AES-GCM sidecars seal at most 12 MiB of JSON with a 12-byte
	// nonce and 16-byte tag, then use unpadded base64 plus the seven-byte
	// version prefix. Keep this exact storage bound alongside the plaintext
	// projection bound so recognized-prefix garbage cannot create an
	// arbitrarily large row or read-path allocation.
	maxEncryptedRecordingSidecarSize = len("enc:v3:") +
		((maxLegacyRecordingObservationsJSONSize+12+16)*4+2)/3
	minEncryptedRecordingPayloadSize = 12 + 16
)

// scriptRecordingObservationsProjectionVersion identifies the storage-safe,
// ID-only representation of observations_json. observations_projected_at is a
// per-row witness for the exact recording revision that received this
// projection. Legacy writers omit both fields: inserts retain version zero and
// updates advance updated_at without advancing the witness, so a later startup
// can find either case without relying on a process-global completion marker.
const scriptRecordingObservationsProjectionVersion = 1

// scriptRecordingCorrelationStatusProjectionVersion identifies the bounded
// count/envelope metadata derived from the two recording payload columns.
// correlation_status_projected_at is an exact-revision witness: rolling-upgrade
// writers advance updated_at without knowing these columns, so their metadata
// becomes stale and the polled status endpoint fails closed until repair.
const scriptRecordingCorrelationStatusProjectionVersion = 1

const (
	// legacyScriptRecordingBindingBatchRows bounds binding metadata retained
	// between database round trips. Current rows need no follow-up write, so
	// fixed pages avoid one PostgreSQL query per recording on every takeover.
	legacyScriptRecordingBindingBatchRows = 64
	// legacyScriptRecordingProjectionBatchRows bounds per-page bookkeeping while
	// keeping the startup/takeover repair deterministic by script_id.
	legacyScriptRecordingProjectionBatchRows = 64
	// legacyScriptRecordingProjectionBatchBytes bounds the retained source and
	// projected payloads for one page. A single legacy row may be larger than
	// this threshold (up to maxLegacyRecordingObservationsJSONSize), so it is
	// processed alone; peak retained payload memory remains deterministically
	// bounded by one maximum-sized row plus this budget.
	legacyScriptRecordingProjectionBatchBytes = 2 << 20
	// Canonical-envelope validation has to inspect ciphertext bytes, but doing
	// that with LIMIT 1 costs one PostgreSQL round trip per recording. A windowed
	// page transfers at most this aggregate budget (or one individually bounded
	// maximum-sized sidecar) while validating up to 64 small envelopes at once.
	legacyScriptRecordingSidecarValidationBatchRows  = 64
	legacyScriptRecordingSidecarValidationBatchBytes = 2 << 20
	legacyScriptRecordingStatusRepairBatchRows       = 64
	legacyScriptRecordingStatusRepairBatchBytes      = 2 << 20
	// A bounded convergence loop catches CAS misses and old-writer updates that
	// land while an acquire-time repair is paging. Leadership admission retries
	// the whole repair if contention persists beyond these local passes.
	legacyScriptRecordingProjectionConvergencePasses = 4
	// Binding and projection are repaired as one convergence unit. A legacy row
	// inserted by an old controller (or a database trigger in regression tests)
	// during projection must be bound before leadership admission opens.
	legacyScriptRecordingRepairConvergencePasses = 4
)

func scriptRecordingObservationJSONByteLengthSQL(dialect storeDialect, column string) string {
	if dialect == storeDialectPostgres {
		return fmt.Sprintf("OCTET_LENGTH(COALESCE(%s, ''))", column)
	}
	return fmt.Sprintf("LENGTH(CAST(COALESCE(%s, '') AS BLOB))", column)
}

func isEncryptedScriptRecordingSidecar(value string) bool {
	return strings.HasPrefix(value, "enc:v1:") || strings.HasPrefix(value, "enc:v2:") || strings.HasPrefix(value, "enc:v3:")
}

func rawStdBase64Value(value byte) (byte, bool) {
	switch {
	case value >= 'A' && value <= 'Z':
		return value - 'A', true
	case value >= 'a' && value <= 'z':
		return value - 'a' + 26, true
	case value >= '0' && value <= '9':
		return value - '0' + 52, true
	case value == '+':
		return 62, true
	case value == '/':
		return 63, true
	default:
		return 0, false
	}
}

// canonicalRawStdBase64DecodedLen validates unpadded standard base64 without
// decoding it. In addition to rejecting padding and non-alphabet bytes, the
// low unused bits in the final symbol must be zero; otherwise several textual
// encodings could represent the same ciphertext. Avoiding DecodeString here is
// important because status and migration paths only need envelope metadata and
// must not allocate another copy of a sidecar that can approach 16 MiB.
func canonicalRawStdBase64DecodedLen(encoded string) (int, bool) {
	remainder := len(encoded) % 4
	if len(encoded) == 0 || remainder == 1 {
		return 0, false
	}
	var finalValue byte
	for index := 0; index < len(encoded); index++ {
		value, ok := rawStdBase64Value(encoded[index])
		if !ok {
			return 0, false
		}
		finalValue = value
	}
	switch remainder {
	case 2:
		if finalValue&0x0f != 0 {
			return 0, false
		}
	case 3:
		if finalValue&0x03 != 0 {
			return 0, false
		}
	}
	decodedLen := len(encoded) / 4 * 3
	if remainder > 0 {
		decodedLen += remainder - 1
	}
	return decodedLen, true
}

// IsCanonicalEncryptedScriptRecordingSidecar verifies the part of the
// ciphertext envelope storage can check without the API's encryption key:
// recognized version, exact raw-base64 representation, bounded size, and room
// for the AES-GCM nonce and tag. This prevents an internal caller from
// disguising plaintext JSON or a trivially prefixed non-ciphertext string with
// an "enc:vN:" prefix. Authentication and row binding remain the API decrypt
// boundary's job.
func IsCanonicalEncryptedScriptRecordingSidecar(value string) bool {
	if len(value) > maxEncryptedRecordingSidecarSize {
		return false
	}
	var encoded string
	switch {
	case strings.HasPrefix(value, "enc:v1:"):
		encoded = strings.TrimPrefix(value, "enc:v1:")
	case strings.HasPrefix(value, "enc:v2:"):
		encoded = strings.TrimPrefix(value, "enc:v2:")
	case strings.HasPrefix(value, "enc:v3:"):
		encoded = strings.TrimPrefix(value, "enc:v3:")
	default:
		return false
	}
	decodedLen, ok := canonicalRawStdBase64DecodedLen(encoded)
	return ok && decodedLen >= minEncryptedRecordingPayloadSize
}

func scriptRecordingSidecarVersion(value string) int {
	if !IsCanonicalEncryptedScriptRecordingSidecar(value) {
		return ScriptRecordingSidecarNone
	}
	switch {
	case strings.HasPrefix(value, "enc:v1:"):
		return ScriptRecordingSidecarV1
	case strings.HasPrefix(value, "enc:v2:"):
		return ScriptRecordingSidecarV2
	case strings.HasPrefix(value, "enc:v3:"):
		return ScriptRecordingSidecarV3
	default:
		return ScriptRecordingSidecarNone
	}
}

var ErrScriptRecordingParentChanged = errors.New("script recording parent is missing or changed")

// ScriptIncarnation returns a stable, non-secret identity for one durable
// script row. A delete/recreate with the same script ID receives a new
// incarnation because the parent row's creation time and stable reference-key
// identity change. Only the version suffix is excluded because ordinary edits
// advance it; those edits are fenced separately by ScriptUpdatedAt.
func ScriptIncarnation(scriptID, projectID, refKey string, createdAt time.Time) string {
	refIdentity := strings.TrimSpace(refKey)
	if parts, ok := refkey.Parse(refIdentity); ok && parts.Type == refkey.TypeScript {
		refIdentity = fmt.Sprintf("%s-%s-%d", parts.Env, parts.Type, parts.Seq)
	}
	payload := strings.Join([]string{
		"apexload.script-incarnation.v1",
		strings.TrimSpace(scriptID),
		normalizeProjectID(strings.TrimSpace(projectID)),
		refIdentity,
		createdAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func scriptRecordingParentTx(tx *storeTx, dialect storeDialect, scriptID string) (projectID, incarnation string, updatedAt time.Time, err error) {
	query := `SELECT COALESCE(project_id, ''), COALESCE(ref_key, ''), created_at, updated_at FROM scripts WHERE id = ?`
	if dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var refKey string
	var createdAt time.Time
	if err := tx.QueryRow(query, scriptID).Scan(&projectID, &refKey, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", time.Time{}, ErrScriptRecordingParentChanged
		}
		return "", "", time.Time{}, err
	}
	projectID = normalizeProjectID(projectID)
	return projectID, ScriptIncarnation(scriptID, projectID, refKey, createdAt), updatedAt, nil
}

func structuralScriptRecordingObservations(observations []ScriptRecordingObservation) []ScriptRecordingObservation {
	if len(observations) > maxRecordingObservations {
		observations = observations[:maxRecordingObservations]
	}
	projected := make([]ScriptRecordingObservation, len(observations))
	for index := range observations {
		projected[index].ID = observations[index].ID
	}
	return projected
}

// decodeStructuralScriptRecordingObservations streams a legacy JSON array and
// retains only the first maxRecordingObservations IDs. Every later element is
// still decoded into the legacy observation shape so malformed JSON or field
// type mismatches fail closed, without materializing a potentially
// million-element slice before truncation.
func decodeStructuralScriptRecordingObservations(payload string) []ScriptRecordingObservation {
	projected := make([]ScriptRecordingObservation, 0, maxRecordingObservations)
	decoder := json.NewDecoder(strings.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return projected
	}
	open, ok := token.(json.Delim)
	if !ok || open != '[' {
		return projected
	}
	index := 0
	var observation ScriptRecordingObservation
	for decoder.More() {
		observation = ScriptRecordingObservation{}
		if err := decoder.Decode(&observation); err != nil {
			return projected[:0]
		}
		if index < maxRecordingObservations {
			projected = append(projected, ScriptRecordingObservation{ID: observation.ID})
		}
		index++
	}
	token, err = decoder.Token()
	if err != nil {
		return projected[:0]
	}
	close, ok := token.(json.Delim)
	if !ok || close != ']' {
		return projected[:0]
	}
	// Decode once more to require EOF (apart from whitespace). Decoding into an
	// empty struct avoids retaining an arbitrary trailing value.
	var trailing struct{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return projected[:0]
	}
	return projected
}

// SaveScriptRecording upserts the recorded baseline for a script.
func (s *SQLiteStore) SaveScriptRecording(record *ScriptRecordingRecord) error {
	if record == nil {
		return fmt.Errorf("script recording is required")
	}
	record.ScriptID = strings.TrimSpace(record.ScriptID)
	if record.ScriptID == "" {
		return fmt.Errorf("script recording script_id is required")
	}
	if record.SchemaVersion == "" {
		record.SchemaVersion = scriptRecordingSchemaVersion
	}
	record.ProjectID = normalizeProjectID(strings.TrimSpace(record.ProjectID))
	record.ScriptIncarnation = strings.TrimSpace(record.ScriptIncarnation)
	if record.ScriptIncarnation == "" {
		return fmt.Errorf("%w: script incarnation is required", ErrScriptRecordingParentChanged)
	}
	if record.ScriptUpdatedAt.IsZero() {
		return fmt.Errorf("%w: expected script updated_at is required", ErrScriptRecordingParentChanged)
	}
	// RequestCount is the authoritative original-capture count, not the number
	// retained after storage projection. Preserve evidence of truncation even
	// for legacy/internal callers that omit the count or supply plaintext
	// CorrelationObservations (which are discarded below).
	originalObservationCount := len(record.Observations)
	originalCorrelationObservationCount := len(record.CorrelationObservations)
	if record.RequestCount < originalObservationCount {
		record.RequestCount = originalObservationCount
	}
	if record.RequestCount < originalCorrelationObservationCount {
		record.RequestCount = originalCorrelationObservationCount
	}
	if len(record.Observations) > maxRecordingObservations {
		record.Observations = record.Observations[:maxRecordingObservations]
	}
	if record.RequestCount <= 0 {
		record.RequestCount = len(record.Observations)
	}
	// Raw correlation observations must be encrypted by the API layer. Silently
	// discard a plaintext sidecar supplied by an older/internal caller so this
	// persistence boundary can never write recorded credentials in cleartext.
	record.CorrelationObservations = nil
	// The public observation column is structural metadata only. Enforce this at
	// the persistence boundary even when an internal caller bypasses the API
	// projection so arbitrary URLs, headers, and bodies never reach plaintext
	// storage.
	record.Observations = structuralScriptRecordingObservations(record.Observations)
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now

	observationsJSON, err := json.Marshal(record.Observations)
	if err != nil {
		return fmt.Errorf("marshal recording observations: %w", err)
	}
	correlationObservationsJSON := record.EncryptedCorrelationObservations
	if strings.TrimSpace(correlationObservationsJSON) == "" {
		correlationObservationsJSON = ""
	}
	if correlationObservationsJSON != "" && !isEncryptedScriptRecordingSidecar(correlationObservationsJSON) {
		return fmt.Errorf("script recording encrypted correlation observations use an unsupported format")
	}
	if len(correlationObservationsJSON) > maxEncryptedRecordingSidecarSize {
		return fmt.Errorf("script recording encrypted correlation observations exceed %d bytes", maxEncryptedRecordingSidecarSize)
	}
	if correlationObservationsJSON != "" && !IsCanonicalEncryptedScriptRecordingSidecar(correlationObservationsJSON) {
		return fmt.Errorf("script recording encrypted correlation observations are malformed")
	}
	sidecarVersion := scriptRecordingSidecarVersion(correlationObservationsJSON)
	correlationObservationCount := 0
	if sidecarVersion != ScriptRecordingSidecarNone {
		correlationObservationCount = len(record.Observations)
	}
	if correlationObservationsJSON == "" {
		correlationObservationsJSON = "[]"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	parentProjectID, parentIncarnation, parentUpdatedAt, err := scriptRecordingParentTx(tx, s.db.dialect, record.ScriptID)
	if err != nil {
		return err
	}
	if parentProjectID != record.ProjectID || parentIncarnation != record.ScriptIncarnation || !parentUpdatedAt.Equal(record.ScriptUpdatedAt) {
		return ErrScriptRecordingParentChanged
	}
	_, err = tx.Exec(`INSERT INTO script_recordings (script_id, project_id, script_incarnation, schema_version, action_graph_digest, source, sensitivity, request_count, observations_json, observations_projection_version, observations_projected_at, correlation_observations_json, correlation_status_version, correlation_observation_count, correlation_sidecar_version, correlation_status_projected_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(script_id) DO UPDATE SET
			project_id=excluded.project_id,
			script_incarnation=excluded.script_incarnation,
			schema_version=excluded.schema_version,
			action_graph_digest=excluded.action_graph_digest,
			source=excluded.source,
			sensitivity=excluded.sensitivity,
			request_count=excluded.request_count,
			observations_json=excluded.observations_json,
			observations_projection_version=excluded.observations_projection_version,
			observations_projected_at=excluded.observations_projected_at,
			correlation_observations_json=excluded.correlation_observations_json,
			correlation_status_version=excluded.correlation_status_version,
			correlation_observation_count=excluded.correlation_observation_count,
			correlation_sidecar_version=excluded.correlation_sidecar_version,
			correlation_status_projected_at=excluded.correlation_status_projected_at,
			updated_at=excluded.updated_at`,
		record.ScriptID,
		record.ProjectID,
		record.ScriptIncarnation,
		record.SchemaVersion,
		strings.TrimSpace(record.ActionGraphDigest),
		record.Source,
		record.Sensitivity,
		record.RequestCount,
		string(observationsJSON),
		scriptRecordingObservationsProjectionVersion,
		record.UpdatedAt,
		correlationObservationsJSON,
		scriptRecordingCorrelationStatusProjectionVersion,
		correlationObservationCount,
		sidecarVersion,
		record.UpdatedAt,
		record.CreatedAt,
		record.UpdatedAt,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetScriptRecording returns the recorded baseline for a script, or (nil, nil)
// when none exists.
func (s *SQLiteStore) GetScriptRecording(scriptID string) (*ScriptRecordingRecord, error) {
	scriptID = strings.TrimSpace(scriptID)
	if scriptID == "" {
		return nil, nil
	}
	var (
		record                      ScriptRecordingRecord
		observationsJSON            string
		correlationObservationsJSON string
		parentProjectID             string
		parentRefKey                string
		parentCreatedAt             time.Time
		parentUpdatedAt             time.Time
	)
	observationByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "r.observations_json")
	sidecarByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "r.correlation_observations_json")
	// Fail oversized legacy rows closed in SQL so a rolling-upgrade write cannot
	// force the driver to transfer/materialize the full blob in Go. Apply the
	// same rule to the encrypted sidecar before base64 decoding. The len check
	// below remains defense in depth for unexpected driver coercions.
	query := fmt.Sprintf(`SELECT r.script_id, COALESCE(r.project_id, ''), COALESCE(r.script_incarnation, ''), COALESCE(r.schema_version, ''), COALESCE(r.action_graph_digest, ''), COALESCE(r.source, ''), COALESCE(r.sensitivity, ''), COALESCE(r.request_count, 0),
		CASE WHEN %s > ? THEN '[]' ELSE COALESCE(r.observations_json, '') END,
		CASE WHEN %s > ? THEN '[]' ELSE COALESCE(r.correlation_observations_json, '') END,
		r.created_at, r.updated_at,
		COALESCE(s.project_id, ''), COALESCE(s.ref_key, ''), s.created_at, s.updated_at
		FROM script_recordings r JOIN scripts s ON s.id = r.script_id WHERE r.script_id = ?`, observationByteLengthSQL, sidecarByteLengthSQL)
	err := s.db.QueryRow(query, maxLegacyRecordingObservationsJSONSize, maxEncryptedRecordingSidecarSize, scriptID).Scan(
		&record.ScriptID,
		&record.ProjectID,
		&record.ScriptIncarnation,
		&record.SchemaVersion,
		&record.ActionGraphDigest,
		&record.Source,
		&record.Sensitivity,
		&record.RequestCount,
		&observationsJSON,
		&correlationObservationsJSON,
		&record.CreatedAt,
		&record.UpdatedAt,
		&parentProjectID,
		&parentRefKey,
		&parentCreatedAt,
		&parentUpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parentProjectID = normalizeProjectID(parentProjectID)
	if record.ProjectID != parentProjectID || record.ScriptIncarnation == "" || record.ScriptIncarnation != ScriptIncarnation(record.ScriptID, parentProjectID, parentRefKey, parentCreatedAt) {
		return nil, nil
	}
	record.ScriptUpdatedAt = parentUpdatedAt
	if observationsJSON != "" {
		if len(observationsJSON) > maxLegacyRecordingObservationsJSONSize {
			record.Observations = []ScriptRecordingObservation{}
		} else {
			record.Observations = decodeStructuralScriptRecordingObservations(observationsJSON)
		}
		// A legacy controller can write during a rolling upgrade after this
		// process has completed startup. Keep that transient at-rest row from
		// crossing the read boundary while the per-row witness ensures the next
		// startup repairs it durably.
	}
	if IsCanonicalEncryptedScriptRecordingSidecar(correlationObservationsJSON) {
		record.EncryptedCorrelationObservations = correlationObservationsJSON
	}
	// Deliberately ignore legacy plaintext correlation_observations_json. The
	// scrubbed Observations view remains available, but old credentials are never
	// re-exposed by current code.
	return &record, nil
}

const scriptRecordingStatusSelectQuery = `SELECT
	r.script_id,
	COALESCE(r.project_id, ''),
	COALESCE(r.script_incarnation, ''),
	COALESCE(r.schema_version, ''),
	COALESCE(r.action_graph_digest, ''),
	COALESCE(r.source, ''),
	COALESCE(r.sensitivity, ''),
	COALESCE(r.request_count, 0),
	COALESCE(r.correlation_status_version, 0),
	COALESCE(r.correlation_observation_count, 0),
	COALESCE(r.correlation_sidecar_version, 0),
	r.correlation_status_projected_at,
	COALESCE(r.observations_projection_version, 0),
	r.observations_projected_at,
	r.created_at,
	r.updated_at,
	COALESCE(s.project_id, ''),
	COALESCE(s.ref_key, ''),
	s.created_at
	FROM script_recordings r
	JOIN scripts s ON s.id = r.script_id
	WHERE r.script_id = ?`

// GetScriptRecordingStatus returns only bounded status metadata. In
// particular, the query never selects observations_json or
// correlation_observations_json, so a viewer poll cannot transfer or
// materialize a multi-megabyte recording payload.
func (s *SQLiteStore) GetScriptRecordingStatus(scriptID string) (*ScriptRecordingStatusRecord, error) {
	scriptID = strings.TrimSpace(scriptID)
	if scriptID == "" {
		return nil, nil
	}
	var (
		record                        ScriptRecordingStatusRecord
		statusProjectionVersion       int
		statusProjectedAt             sql.NullTime
		observationsProjectionVersion int
		observationsProjectedAt       sql.NullTime
		parentProjectID               string
		parentRefKey                  string
		parentCreatedAt               time.Time
	)
	err := s.db.QueryRow(scriptRecordingStatusSelectQuery, scriptID).Scan(
		&record.ScriptID,
		&record.ProjectID,
		&record.ScriptIncarnation,
		&record.SchemaVersion,
		&record.ActionGraphDigest,
		&record.Source,
		&record.Sensitivity,
		&record.RequestCount,
		&statusProjectionVersion,
		&record.CorrelationObservationCount,
		&record.CorrelationSidecarVersion,
		&statusProjectedAt,
		&observationsProjectionVersion,
		&observationsProjectedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
		&parentProjectID,
		&parentRefKey,
		&parentCreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parentProjectID = normalizeProjectID(parentProjectID)
	if record.ProjectID != parentProjectID ||
		record.ScriptIncarnation == "" ||
		record.ScriptIncarnation != ScriptIncarnation(record.ScriptID, parentProjectID, parentRefKey, parentCreatedAt) {
		return nil, nil
	}
	record.UpdatedAt = record.UpdatedAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	metadataShapeValid := record.CorrelationObservationCount >= 0 &&
		record.CorrelationObservationCount <= maxRecordingObservations &&
		record.CorrelationSidecarVersion >= ScriptRecordingSidecarNone &&
		record.CorrelationSidecarVersion <= ScriptRecordingSidecarV3 &&
		(record.CorrelationSidecarVersion != ScriptRecordingSidecarNone || record.CorrelationObservationCount == 0)
	record.CorrelationMetadataCurrent =
		metadataShapeValid &&
			statusProjectionVersion == scriptRecordingCorrelationStatusProjectionVersion &&
			statusProjectedAt.Valid &&
			statusProjectedAt.Time.Equal(record.UpdatedAt) &&
			observationsProjectionVersion == scriptRecordingObservationsProjectionVersion &&
			observationsProjectedAt.Valid &&
			observationsProjectedAt.Time.Equal(record.UpdatedAt)
	if !record.CorrelationMetadataCurrent {
		record.CorrelationObservationCount = 0
		record.CorrelationSidecarVersion = ScriptRecordingSidecarNone
	}
	return &record, nil
}

type scriptRecordingSidecarValidationSnapshot struct {
	scriptID     string
	payload      string
	updatedAtRaw string
	current      bool
}

// loadScriptRecordingSidecarValidationBatch returns a deterministic,
// aggregate-byte-bounded page of recognized ciphertext envelopes. The first
// row is always selected so a single sidecar larger than the aggregate budget
// still makes progress; its independent maximum-size predicate bounds that
// case. The metadata CTE chooses rows before the payload join, preventing a
// driver from buffering 64 maximum-sized ciphertexts.
func (s *SQLiteStore) loadScriptRecordingSidecarValidationBatch(
	afterScriptID string,
) ([]scriptRecordingSidecarValidationSnapshot, string, bool, error) {
	sidecarByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "correlation_observations_json")
	currentSidecarByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "r.correlation_observations_json")
	query := fmt.Sprintf(`WITH sidecar_page AS (
			SELECT script_id, %s AS payload_size
			FROM script_recordings
			WHERE script_id > ?
				AND %s <= ?
				AND COALESCE(correlation_status_version, 0) <= %d
				AND COALESCE(observations_projection_version, 0) <= %d
				AND (SUBSTR(COALESCE(correlation_observations_json, ''), 1, 7) = 'enc:v1:'
					OR SUBSTR(COALESCE(correlation_observations_json, ''), 1, 7) = 'enc:v2:'
					OR SUBSTR(COALESCE(correlation_observations_json, ''), 1, 7) = 'enc:v3:')
			ORDER BY script_id
			LIMIT ?
		), ranked_sidecars AS (
			SELECT script_id, payload_size,
				ROW_NUMBER() OVER (ORDER BY script_id) AS page_row,
				COUNT(*) OVER () AS page_count,
				SUM(payload_size) OVER (
					ORDER BY script_id
					ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
				) AS cumulative_size
			FROM sidecar_page
		), selected_sidecars AS (
			SELECT script_id, payload_size, page_count
			FROM ranked_sidecars
			WHERE page_row = 1 OR cumulative_size <= ?
		)
		SELECT selected_sidecars.script_id,
			CASE WHEN %s = selected_sidecars.payload_size
				THEN COALESCE(r.correlation_observations_json, '') END,
			CASE WHEN %s = selected_sidecars.payload_size
				THEN CAST(r.updated_at AS TEXT) END,
			selected_sidecars.page_count
		FROM selected_sidecars
		LEFT JOIN script_recordings r
			ON r.script_id = selected_sidecars.script_id
			AND COALESCE(r.correlation_status_version, 0) <= %d
			AND COALESCE(r.observations_projection_version, 0) <= %d
		ORDER BY selected_sidecars.script_id`,
		sidecarByteLengthSQL,
		sidecarByteLengthSQL,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
		currentSidecarByteLengthSQL,
		currentSidecarByteLengthSQL,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
	)
	rows, err := s.db.Query(
		query,
		afterScriptID,
		maxEncryptedRecordingSidecarSize,
		legacyScriptRecordingSidecarValidationBatchRows,
		legacyScriptRecordingSidecarValidationBatchBytes,
	)
	if err != nil {
		return nil, afterScriptID, false, fmt.Errorf("list encrypted script recording sidecars: %w", err)
	}
	snapshots := make([]scriptRecordingSidecarValidationSnapshot, 0, legacyScriptRecordingSidecarValidationBatchRows)
	pageCount := 0
	for rows.Next() {
		var (
			snapshot  scriptRecordingSidecarValidationSnapshot
			payload   sql.NullString
			updatedAt sql.NullString
		)
		if err := rows.Scan(&snapshot.scriptID, &payload, &updatedAt, &pageCount); err != nil {
			rows.Close()
			return nil, afterScriptID, false, fmt.Errorf("scan encrypted script recording sidecar: %w", err)
		}
		snapshot.current = payload.Valid && updatedAt.Valid
		if snapshot.current {
			snapshot.payload = payload.String
			snapshot.updatedAtRaw = updatedAt.String
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, afterScriptID, false, fmt.Errorf("list encrypted script recording sidecars: %w", err)
	}
	rows.Close()
	nextCursor := afterScriptID
	if len(snapshots) > 0 {
		nextCursor = snapshots[len(snapshots)-1].scriptID
	}
	done := len(snapshots) == 0 ||
		(len(snapshots) == pageCount && pageCount < legacyScriptRecordingSidecarValidationBatchRows)
	return snapshots, nextCursor, done, nil
}

// purgeLegacyPlaintextScriptRecordingSidecars irreversibly removes raw
// correlation observations written by schema versions that stored this column
// as JSON, plus malformed strings carrying a recognized ciphertext prefix.
// Empty slots and canonical ciphertext versions are preserved. Prefix-shaped
// candidates are fetched one at a time under the SQL byte ceiling and cleared
// with an exact payload/timestamp CAS, so repair memory stays bounded and a
// concurrent fresh capture always wins.
func (s *SQLiteStore) purgeLegacyPlaintextScriptRecordingSidecars() error {
	sidecarByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "correlation_observations_json")
	clearOversized := fmt.Sprintf(`UPDATE script_recordings
		SET correlation_observations_json = '[]',
			correlation_status_version = ?,
			correlation_observation_count = 0,
			correlation_sidecar_version = ?,
			correlation_status_projected_at = updated_at
		WHERE %s > ?
			AND COALESCE(correlation_status_version, 0) <= ?
			AND COALESCE(observations_projection_version, 0) <= ?`, sidecarByteLengthSQL)
	if _, err := s.db.Exec(
		clearOversized,
		scriptRecordingCorrelationStatusProjectionVersion,
		ScriptRecordingSidecarNone,
		maxEncryptedRecordingSidecarSize,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
	); err != nil {
		return fmt.Errorf("purge oversized script recording sidecars: %w", err)
	}
	clearPlaintext := fmt.Sprintf(`UPDATE script_recordings
		SET correlation_observations_json = '[]',
			correlation_status_version = ?,
			correlation_observation_count = 0,
			correlation_sidecar_version = ?,
			correlation_status_projected_at = updated_at
		WHERE %s <= ?
			AND COALESCE(correlation_observations_json, '') != ''
			AND COALESCE(correlation_observations_json, '') != '[]'
			AND SUBSTR(correlation_observations_json, 1, 7) != 'enc:v1:'
			AND SUBSTR(correlation_observations_json, 1, 7) != 'enc:v2:'
			AND SUBSTR(correlation_observations_json, 1, 7) != 'enc:v3:'
			AND COALESCE(correlation_status_version, 0) <= ?
			AND COALESCE(observations_projection_version, 0) <= ?`, sidecarByteLengthSQL)
	if _, err := s.db.Exec(
		clearPlaintext,
		scriptRecordingCorrelationStatusProjectionVersion,
		ScriptRecordingSidecarNone,
		maxEncryptedRecordingSidecarSize,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
	); err != nil {
		return fmt.Errorf("purge legacy plaintext script recording sidecars: %w", err)
	}

	cursor := ""
	for {
		snapshots, nextCursor, done, err := s.loadScriptRecordingSidecarValidationBatch(cursor)
		if err != nil {
			return err
		}
		if !done && nextCursor == cursor {
			return fmt.Errorf("encrypted script recording sidecar repair made no paging progress after %q", cursor)
		}
		for _, snapshot := range snapshots {
			if !snapshot.current || IsCanonicalEncryptedScriptRecordingSidecar(snapshot.payload) {
				continue
			}
			if _, err := s.clearMalformedScriptRecordingSidecarIfUnchanged(
				snapshot.scriptID,
				snapshot.payload,
				snapshot.updatedAtRaw,
			); err != nil {
				return fmt.Errorf("purge malformed encrypted script recording sidecar %s: %w", snapshot.scriptID, err)
			}
		}
		if done {
			return nil
		}
		cursor = nextCursor
	}
}

func (s *SQLiteStore) clearMalformedScriptRecordingSidecarIfUnchanged(scriptID, payload, updatedAtRaw string) (bool, error) {
	result, err := s.db.Exec(`UPDATE script_recordings
		SET correlation_observations_json = '[]',
			correlation_status_version = ?,
			correlation_observation_count = 0,
			correlation_sidecar_version = ?,
			correlation_status_projected_at = updated_at
		WHERE script_id = ?
			AND COALESCE(correlation_observations_json, '') = ?
			AND CAST(updated_at AS TEXT) = ?
			AND COALESCE(correlation_status_version, 0) <= ?
			AND COALESCE(observations_projection_version, 0) <= ?`,
		scriptRecordingCorrelationStatusProjectionVersion,
		ScriptRecordingSidecarNone,
		scriptID,
		payload,
		updatedAtRaw,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLiteStore) hasInvalidScriptRecordingSidecars() (bool, error) {
	sidecarByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "correlation_observations_json")
	obviousInvalidQuery := fmt.Sprintf(`SELECT 1
		FROM script_recordings
		WHERE COALESCE(correlation_status_version, 0) <= ?
			AND COALESCE(observations_projection_version, 0) <= ?
			AND (%s > ?
				OR (%s <= ?
					AND COALESCE(correlation_observations_json, '') != ''
					AND COALESCE(correlation_observations_json, '') != '[]'
					AND SUBSTR(COALESCE(correlation_observations_json, ''), 1, 7) != 'enc:v1:'
					AND SUBSTR(COALESCE(correlation_observations_json, ''), 1, 7) != 'enc:v2:'
					AND SUBSTR(COALESCE(correlation_observations_json, ''), 1, 7) != 'enc:v3:'))
		ORDER BY script_id
		LIMIT 1`, sidecarByteLengthSQL, sidecarByteLengthSQL)
	var one int
	err := s.db.QueryRow(
		obviousInvalidQuery,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
		maxEncryptedRecordingSidecarSize,
		maxEncryptedRecordingSidecarSize,
	).Scan(&one)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("check invalid script recording sidecars: %w", err)
	}

	cursor := ""
	for {
		snapshots, nextCursor, done, err := s.loadScriptRecordingSidecarValidationBatch(cursor)
		if err != nil {
			return false, err
		}
		if !done && nextCursor == cursor {
			return false, fmt.Errorf("encrypted script recording sidecar validation made no paging progress after %q", cursor)
		}
		for _, snapshot := range snapshots {
			if snapshot.current && !IsCanonicalEncryptedScriptRecordingSidecar(snapshot.payload) {
				return true, nil
			}
		}
		if done {
			return false, nil
		}
		cursor = nextCursor
	}
}

// RepairLegacyScriptRecordings performs the CAS-safe recording repairs needed
// both during store initialization and whenever an HA replica acquires
// execution leadership. Re-running it on takeover closes the rolling-upgrade
// window where an older controller can insert a legacy row after the standby's
// store was initialized but before that standby becomes active.
func (s *SQLiteStore) RepairLegacyScriptRecordings() error {
	for pass := 0; pass < legacyScriptRecordingRepairConvergencePasses; pass++ {
		if err := s.purgeInvalidScriptRecordings(); err != nil {
			return err
		}
		if err := s.migrateLegacyScriptRecordingObservations(); err != nil {
			return err
		}
		if err := s.purgeLegacyPlaintextScriptRecordingSidecars(); err != nil {
			return err
		}
		if err := s.repairScriptRecordingCorrelationStatusMetadata(); err != nil {
			return err
		}
		staleBinding, err := s.hasInvalidScriptRecordingBindings()
		if err != nil {
			return err
		}
		staleProjection, err := s.hasStaleScriptRecordingObservationProjection()
		if err != nil {
			return err
		}
		staleSidecar, err := s.hasInvalidScriptRecordingSidecars()
		if err != nil {
			return err
		}
		staleStatus, err := s.hasStaleScriptRecordingCorrelationStatusMetadata()
		if err != nil {
			return err
		}
		if !staleBinding && !staleProjection && !staleSidecar && !staleStatus {
			return nil
		}
	}
	return fmt.Errorf("legacy script recording binding/projection/sidecar/status repair did not converge after %d passes", legacyScriptRecordingRepairConvergencePasses)
}

func (s *SQLiteStore) hasInvalidScriptRecordingBindings() (bool, error) {
	rows, err := s.db.Query(`SELECT r.script_id, COALESCE(r.project_id, ''), COALESCE(r.script_incarnation, ''),
		COALESCE(s.project_id, ''), COALESCE(s.ref_key, ''), s.created_at
		FROM script_recordings r LEFT JOIN scripts s ON s.id = r.script_id
		ORDER BY r.script_id`)
	if err != nil {
		return false, fmt.Errorf("check script recording bindings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scriptID, recordingProjectID, recordingIncarnation, parentProjectID, parentRefKey string
		var parentCreatedAt sql.NullTime
		if err := rows.Scan(&scriptID, &recordingProjectID, &recordingIncarnation, &parentProjectID, &parentRefKey, &parentCreatedAt); err != nil {
			return false, fmt.Errorf("scan script recording binding check: %w", err)
		}
		if !parentCreatedAt.Valid {
			return true, nil
		}
		parentProjectID = normalizeProjectID(parentProjectID)
		if strings.TrimSpace(recordingProjectID) != parentProjectID ||
			strings.TrimSpace(recordingIncarnation) != ScriptIncarnation(scriptID, parentProjectID, parentRefKey, parentCreatedAt.Time) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("check script recording bindings: %w", err)
	}
	return false, nil
}

// purgeInvalidScriptRecordings removes stale rows that cannot prove which live
// script incarnation they belong to, after backfilling the binding columns for
// legitimate legacy rows. This is intentionally fail-closed: an old ciphertext
// must never become evidence for a newly-created script that happens to reuse
// the same ID.
//
// Legacy rows were written before the project_id and script_incarnation columns
// existed, so both are empty. When such a row still has a live parent script,
// the parent is the only identity the row could ever have referred to: bind the
// row to it instead of deleting captured evidence. The fail-closed threat (a
// ciphertext feeding a different script incarnation) is unaffected because a
// legacy sidecar is enc:v1/enc:v2, which the API layer never decrypts, and a
// v3 sidecar's AEAD binding is verified again at decrypt time.
func (s *SQLiteStore) purgeInvalidScriptRecordings() error {
	cursor := ""
	firstPage := true
	backfilled := 0
	purged := 0
	for {
		candidates, nextCursor, done, err := s.loadScriptRecordingBindingRepairBatch(cursor, firstPage)
		if err != nil {
			return err
		}
		if !firstPage && len(candidates) > 0 && nextCursor == cursor {
			return fmt.Errorf("script recording binding repair made no paging progress after %q", cursor)
		}
		for _, candidate := range candidates {
			if !candidate.parentExists {
				deleted, err := s.deleteInvalidScriptRecordingIfUnchanged(candidate.snapshot)
				if err != nil {
					return fmt.Errorf("purge invalid script recording %s: %w", candidate.snapshot.scriptID, err)
				}
				if deleted {
					purged++
				}
				continue
			}

			recordingProjectID := strings.TrimSpace(candidate.snapshot.projectID)
			recordingIncarnation := strings.TrimSpace(candidate.snapshot.incarnation)
			if recordingProjectID == "" && recordingIncarnation == "" {
				updated, err := s.backfillScriptRecordingBindingIfUnchanged(candidate)
				if err != nil {
					return fmt.Errorf("backfill script recording binding %s: %w", candidate.snapshot.scriptID, err)
				}
				if updated {
					backfilled++
				}
				continue
			}
			if recordingProjectID == candidate.projectID &&
				recordingIncarnation == candidate.expectedIncarnation {
				continue
			}
			deleted, err := s.deleteInvalidScriptRecordingIfUnchanged(candidate.snapshot)
			if err != nil {
				return fmt.Errorf("purge invalid script recording %s: %w", candidate.snapshot.scriptID, err)
			}
			if deleted {
				purged++
			}
		}
		if done {
			break
		}
		cursor = nextCursor
		firstPage = false
	}
	if backfilled > 0 {
		slog.Info("bound legacy script recording baselines to their live parent scripts", "backfilled", backfilled)
	}
	if purged > 0 {
		slog.Warn("purged script recording baselines that cannot prove their parent script binding", "purged", purged)
	}
	// Legacy enc:v1/enc:v2 sidecars predate the AAD-bound v3 format and are no
	// longer decryptable; the rows are retained so their structural metadata
	// stays visible and the API can report a precise recapture requirement.
	var legacyCiphertexts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM script_recordings
		WHERE SUBSTR(correlation_observations_json, 1, 7) = 'enc:v1:'
			OR SUBSTR(correlation_observations_json, 1, 7) = 'enc:v2:'`).Scan(&legacyCiphertexts); err != nil {
		return fmt.Errorf("count legacy encrypted script recording sidecars: %w", err)
	}
	if legacyCiphertexts > 0 {
		slog.Warn("script recording baselines use legacy enc:v1/enc:v2 encryption that current builds cannot decrypt; correlation diagnosis for these scripts requires capturing a new recording", "recordings", legacyCiphertexts)
	}
	return nil
}

type scriptRecordingBindingRepair struct {
	snapshot            invalidScriptRecordingSnapshot
	parentExists        bool
	parentProjectIDRaw  string
	parentRefKey        string
	parentCreatedAtRaw  string
	projectID           string
	expectedIncarnation string
}

// loadScriptRecordingBindingRepairBatch returns one fixed-size deterministic
// metadata page. It never selects either recording payload column. The caller
// closes/drains this page before any CAS writes, preserving SQLite's
// single-connection invariant while reducing PostgreSQL round trips from one
// per current recording to one per bounded page.
func (s *SQLiteStore) loadScriptRecordingBindingRepairBatch(
	afterScriptID string,
	firstPage bool,
) ([]scriptRecordingBindingRepair, string, bool, error) {
	query := `SELECT
			r.script_id,
			COALESCE(r.project_id, ''),
			COALESCE(r.script_incarnation, ''),
			CAST(r.updated_at AS TEXT),
			s.id,
			COALESCE(s.project_id, ''),
			COALESCE(s.ref_key, ''),
			s.created_at,
			CAST(s.created_at AS TEXT)
		FROM script_recordings r
		LEFT JOIN scripts s ON s.id = r.script_id`
	args := make([]any, 0, 2)
	if !firstPage {
		query += ` WHERE r.script_id > ?`
		args = append(args, afterScriptID)
	}
	query += `
		ORDER BY r.script_id
		LIMIT ?`
	args = append(args, legacyScriptRecordingBindingBatchRows)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, afterScriptID, false, fmt.Errorf("list script recording bindings: %w", err)
	}
	candidates := make([]scriptRecordingBindingRepair, 0, legacyScriptRecordingBindingBatchRows)
	for rows.Next() {
		var (
			candidate          scriptRecordingBindingRepair
			parentID           sql.NullString
			parentProjectID    string
			parentRefKey       string
			parentCreatedAt    sql.NullTime
			parentCreatedAtRaw sql.NullString
		)
		if err := rows.Scan(
			&candidate.snapshot.scriptID,
			&candidate.snapshot.projectID,
			&candidate.snapshot.incarnation,
			&candidate.snapshot.updatedAtRaw,
			&parentID,
			&parentProjectID,
			&parentRefKey,
			&parentCreatedAt,
			&parentCreatedAtRaw,
		); err != nil {
			rows.Close()
			return nil, afterScriptID, false, fmt.Errorf("scan script recording binding: %w", err)
		}
		candidate.parentExists = parentID.Valid && parentCreatedAt.Valid && parentCreatedAtRaw.Valid
		candidate.snapshot.parentSnapshotKnown = true
		candidate.snapshot.parentExists = candidate.parentExists
		if candidate.parentExists {
			candidate.parentProjectIDRaw = parentProjectID
			candidate.parentRefKey = parentRefKey
			candidate.parentCreatedAtRaw = parentCreatedAtRaw.String
			candidate.projectID = normalizeProjectID(parentProjectID)
			candidate.expectedIncarnation = ScriptIncarnation(
				candidate.snapshot.scriptID,
				candidate.projectID,
				parentRefKey,
				parentCreatedAt.Time,
			)
			candidate.snapshot.parentProjectIDRaw = candidate.parentProjectIDRaw
			candidate.snapshot.parentRefKey = candidate.parentRefKey
			candidate.snapshot.parentCreatedAtRaw = candidate.parentCreatedAtRaw
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, afterScriptID, false, fmt.Errorf("list script recording bindings: %w", err)
	}
	rows.Close()
	nextCursor := afterScriptID
	if len(candidates) > 0 {
		nextCursor = candidates[len(candidates)-1].snapshot.scriptID
	}
	return candidates, nextCursor, len(candidates) < legacyScriptRecordingBindingBatchRows, nil
}

func (s *SQLiteStore) backfillScriptRecordingBindingIfUnchanged(candidate scriptRecordingBindingRepair) (bool, error) {
	result, err := s.db.Exec(`UPDATE script_recordings
		SET project_id = ?, script_incarnation = ?
		WHERE script_id = ?
			AND COALESCE(project_id, '') = ?
			AND COALESCE(script_incarnation, '') = ?
			AND CAST(updated_at AS TEXT) = ?
			AND EXISTS (
				SELECT 1
				FROM scripts s
				WHERE s.id = script_recordings.script_id
					AND COALESCE(s.project_id, '') = ?
					AND COALESCE(s.ref_key, '') = ?
					AND CAST(s.created_at AS TEXT) = ?
			)`,
		candidate.projectID,
		candidate.expectedIncarnation,
		candidate.snapshot.scriptID,
		candidate.snapshot.projectID,
		candidate.snapshot.incarnation,
		candidate.snapshot.updatedAtRaw,
		candidate.parentProjectIDRaw,
		candidate.parentRefKey,
		candidate.parentCreatedAtRaw,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// invalidScriptRecordingSnapshot captures the row version whose parent binding
// was found invalid. Startup migration runs concurrently across rolling HA
// replicas, so cleanup must compare the exact scanned version before deleting.
type invalidScriptRecordingSnapshot struct {
	scriptID            string
	projectID           string
	incarnation         string
	updatedAtRaw        string
	parentSnapshotKnown bool
	parentExists        bool
	parentProjectIDRaw  string
	parentRefKey        string
	parentCreatedAtRaw  string
}

func (s *SQLiteStore) deleteInvalidScriptRecordingIfUnchanged(snapshot invalidScriptRecordingSnapshot) (bool, error) {
	query := `DELETE FROM script_recordings
		WHERE script_id = ?
			AND COALESCE(project_id, '') = ?
			AND COALESCE(script_incarnation, '') = ?
			AND CAST(updated_at AS TEXT) = ?`
	args := []any{snapshot.scriptID, snapshot.projectID, snapshot.incarnation, snapshot.updatedAtRaw}
	if snapshot.parentSnapshotKnown && snapshot.parentExists {
		query += ` AND EXISTS (
			SELECT 1
			FROM scripts s
			WHERE s.id = script_recordings.script_id
				AND COALESCE(s.project_id, '') = ?
				AND COALESCE(s.ref_key, '') = ?
				AND CAST(s.created_at AS TEXT) = ?
		)`
		args = append(args, snapshot.parentProjectIDRaw, snapshot.parentRefKey, snapshot.parentCreatedAtRaw)
	} else if snapshot.parentSnapshotKnown {
		query += ` AND NOT EXISTS (
			SELECT 1 FROM scripts s WHERE s.id = script_recordings.script_id
		)`
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// migrateLegacyScriptRecordingObservations rewrites the old scrubbed-but-still-
// identifying observation payloads into their ID-only structural projection.
// Invalid or pathologically large legacy JSON is cleared. The migration runs
// on every startup but selects only rows whose per-row projection version is
// stale or whose projection witness no longer matches updated_at. The witness
// is important during rolling upgrades: an older controller can overwrite an
// already-projected row without knowing about the new columns, but it will
// advance updated_at and therefore make that row eligible again.
func (s *SQLiteStore) migrateLegacyScriptRecordingObservations() error {
	observationByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "observations_json")
	for pass := 0; pass < legacyScriptRecordingProjectionConvergencePasses; pass++ {
		oversizedUpdate := fmt.Sprintf(`UPDATE script_recordings
			SET observations_json = '[]',
				observations_projection_version = ?,
				observations_projected_at = updated_at,
				correlation_status_version = 0,
				correlation_observation_count = 0,
				correlation_sidecar_version = 0,
				correlation_status_projected_at = NULL
			WHERE %s > ?
				AND COALESCE(observations_projection_version, 0) <= ?
				AND (COALESCE(observations_projection_version, 0) < ?
					OR observations_projected_at IS NULL
					OR observations_projected_at != updated_at)`, observationByteLengthSQL)
		if _, err := s.db.Exec(oversizedUpdate,
			scriptRecordingObservationsProjectionVersion,
			maxLegacyRecordingObservationsJSONSize,
			scriptRecordingObservationsProjectionVersion,
			scriptRecordingObservationsProjectionVersion); err != nil {
			return fmt.Errorf("clear oversized legacy script recording observations: %w", err)
		}

		cursor := ""
		for {
			updates, nextCursor, done, err := s.loadLegacyScriptRecordingObservationProjectionBatch(cursor)
			if err != nil {
				return err
			}
			for _, update := range updates {
				if _, err := s.rewriteLegacyScriptRecordingObservationsIfUnchanged(update.snapshot, update.payload); err != nil {
					return fmt.Errorf("rewrite legacy script recording observations for %s: %w", update.snapshot.scriptID, err)
				}
			}
			if done {
				break
			}
			if nextCursor == cursor {
				return fmt.Errorf("legacy script recording observation migration made no paging progress after %q", cursor)
			}
			cursor = nextCursor
		}

		stale, err := s.hasStaleScriptRecordingObservationProjection()
		if err != nil {
			return err
		}
		if !stale {
			return nil
		}
	}
	return fmt.Errorf("legacy script recording observation migration did not converge after %d passes", legacyScriptRecordingProjectionConvergencePasses)
}

type legacyScriptRecordingObservationSnapshot struct {
	scriptID          string
	payload           string
	updatedAtRaw      string
	projectionVersion int
	projectedAtRaw    sql.NullString
	current           bool
}

type legacyScriptRecordingObservationUpdate struct {
	snapshot legacyScriptRecordingObservationSnapshot
	payload  string
}

func (s *SQLiteStore) hasStaleScriptRecordingObservationProjection() (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM script_recordings
		WHERE COALESCE(observations_projection_version, 0) <= ?
			AND (COALESCE(observations_projection_version, 0) < ?
				OR observations_projected_at IS NULL
				OR observations_projected_at != updated_at)
		ORDER BY script_id
		LIMIT 1`,
		scriptRecordingObservationsProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check legacy script recording observation projection convergence: %w", err)
	}
	return true, nil
}

// loadLegacyScriptRecordingObservationProjectionBatch returns one deterministic
// aggregate-byte-bounded page. A metadata window selects up to 64 small payloads
// for one PostgreSQL read round trip; one individually capped large row is
// always allowed so paging cannot stall.
func (s *SQLiteStore) loadLegacyScriptRecordingObservationProjectionBatch(afterScriptID string) ([]legacyScriptRecordingObservationUpdate, string, bool, error) {
	observationByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "observations_json")
	currentObservationByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "r.observations_json")
	query := fmt.Sprintf(`WITH observation_page AS (
			SELECT script_id, %s AS payload_size
			FROM script_recordings
			WHERE script_id > ?
				AND %s <= ?
				AND COALESCE(observations_projection_version, 0) <= %d
				AND (COALESCE(observations_projection_version, 0) < %d
					OR observations_projected_at IS NULL
					OR observations_projected_at != updated_at)
			ORDER BY script_id
			LIMIT ?
		), ranked_observations AS (
			SELECT script_id, payload_size,
				ROW_NUMBER() OVER (ORDER BY script_id) AS page_row,
				COUNT(*) OVER () AS page_count,
				SUM(payload_size) OVER (
					ORDER BY script_id
					ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
				) AS cumulative_size
			FROM observation_page
		), selected_observations AS (
			SELECT script_id, payload_size, page_count
			FROM ranked_observations
			WHERE page_row = 1 OR cumulative_size <= ?
		)
		SELECT selected_observations.script_id,
			COALESCE(r.observations_json, ''),
			CAST(r.updated_at AS TEXT),
			COALESCE(r.observations_projection_version, 0),
			CAST(r.observations_projected_at AS TEXT),
			selected_observations.page_count,
			r.script_id
		FROM selected_observations
		LEFT JOIN script_recordings r
			ON r.script_id = selected_observations.script_id
			AND %s = selected_observations.payload_size
			AND COALESCE(r.observations_projection_version, 0) <= %d
			AND (COALESCE(r.observations_projection_version, 0) < %d
				OR r.observations_projected_at IS NULL
				OR r.observations_projected_at != r.updated_at)
		ORDER BY selected_observations.script_id`,
		observationByteLengthSQL,
		observationByteLengthSQL,
		scriptRecordingObservationsProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
		currentObservationByteLengthSQL,
		scriptRecordingObservationsProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
	)
	rows, err := s.db.Query(
		query,
		afterScriptID,
		maxLegacyRecordingObservationsJSONSize,
		legacyScriptRecordingProjectionBatchRows,
		legacyScriptRecordingProjectionBatchBytes,
	)
	if err != nil {
		return nil, afterScriptID, false, fmt.Errorf("list legacy script recording observations: %w", err)
	}
	snapshots := make([]legacyScriptRecordingObservationSnapshot, 0, legacyScriptRecordingProjectionBatchRows)
	pageCount := 0
	for rows.Next() {
		var (
			snapshot       legacyScriptRecordingObservationSnapshot
			payload        sql.NullString
			updatedAtRaw   sql.NullString
			projection     sql.NullInt64
			projectedAtRaw sql.NullString
			joinedScriptID sql.NullString
		)
		if err := rows.Scan(
			&snapshot.scriptID,
			&payload,
			&updatedAtRaw,
			&projection,
			&projectedAtRaw,
			&pageCount,
			&joinedScriptID,
		); err != nil {
			rows.Close()
			return nil, afterScriptID, false, fmt.Errorf("scan legacy script recording observations: %w", err)
		}
		snapshot.current = joinedScriptID.Valid &&
			payload.Valid &&
			updatedAtRaw.Valid &&
			projection.Valid
		if snapshot.current {
			snapshot.payload = payload.String
			snapshot.updatedAtRaw = updatedAtRaw.String
			snapshot.projectionVersion = int(projection.Int64)
			snapshot.projectedAtRaw = projectedAtRaw
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, afterScriptID, false, fmt.Errorf("list legacy script recording observations: %w", err)
	}
	rows.Close()

	updates := make([]legacyScriptRecordingObservationUpdate, 0, len(snapshots))
	nextCursor := afterScriptID
	for _, snapshot := range snapshots {
		nextCursor = snapshot.scriptID
		if !snapshot.current {
			continue
		}
		observations := decodeStructuralScriptRecordingObservations(snapshot.payload)
		projected, err := json.Marshal(observations)
		if err != nil {
			return nil, afterScriptID, false, fmt.Errorf("marshal structural script recording observations: %w", err)
		}
		updates = append(updates, legacyScriptRecordingObservationUpdate{snapshot: snapshot, payload: string(projected)})
	}
	done := len(snapshots) == 0 ||
		(len(snapshots) == pageCount && pageCount < legacyScriptRecordingProjectionBatchRows)
	return updates, nextCursor, done, nil
}

func (s *SQLiteStore) rewriteLegacyScriptRecordingObservationsIfUnchanged(snapshot legacyScriptRecordingObservationSnapshot, projectedPayload string) (bool, error) {
	query := `UPDATE script_recordings
		SET observations_json = ?,
			observations_projection_version = ?,
			observations_projected_at = updated_at,
			correlation_status_version = 0,
			correlation_observation_count = 0,
			correlation_sidecar_version = 0,
			correlation_status_projected_at = NULL
		WHERE script_id = ?
			AND COALESCE(observations_json, '') = ?
			AND CAST(updated_at AS TEXT) = ?
			AND COALESCE(observations_projection_version, 0) = ?`
	args := []any{
		projectedPayload,
		scriptRecordingObservationsProjectionVersion,
		snapshot.scriptID,
		snapshot.payload,
		snapshot.updatedAtRaw,
		snapshot.projectionVersion,
	}
	if snapshot.projectedAtRaw.Valid {
		query += ` AND CAST(observations_projected_at AS TEXT) = ?`
		args = append(args, snapshot.projectedAtRaw.String)
	} else {
		query += ` AND observations_projected_at IS NULL`
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

type scriptRecordingCorrelationStatusSnapshot struct {
	scriptID                      string
	observationsPayload           string
	sidecarPayload                string
	updatedAtRaw                  string
	statusProjectionVersion       int
	correlationObservationCount   int
	correlationSidecarVersion     int
	statusProjectedAtRaw          sql.NullString
	observationsProjectionVersion int
	observationsProjectedAtRaw    sql.NullString
	current                       bool
}

func (s *SQLiteStore) hasStaleScriptRecordingCorrelationStatusMetadata() (bool, error) {
	var marker int
	err := s.db.QueryRow(`SELECT 1
		FROM script_recordings
		WHERE COALESCE(correlation_status_version, 0) <= ?
			AND COALESCE(observations_projection_version, 0) <= ?
			AND (COALESCE(correlation_status_version, 0) < ?
				OR correlation_status_projected_at IS NULL
				OR correlation_status_projected_at != updated_at
				OR COALESCE(correlation_observation_count, 0) < 0
				OR COALESCE(correlation_observation_count, 0) > ?
				OR COALESCE(correlation_sidecar_version, 0) < ?
				OR COALESCE(correlation_sidecar_version, 0) > ?
				OR (COALESCE(correlation_sidecar_version, 0) = ?
					AND COALESCE(correlation_observation_count, 0) != 0))
		LIMIT 1`,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingObservationsProjectionVersion,
		scriptRecordingCorrelationStatusProjectionVersion,
		maxRecordingObservations,
		ScriptRecordingSidecarNone,
		ScriptRecordingSidecarV3,
		ScriptRecordingSidecarNone,
	).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check script recording correlation status metadata: %w", err)
	}
	return true, nil
}

// loadScriptRecordingCorrelationStatusRepairBatch returns a deterministic
// aggregate-byte-bounded page of stale status rows. Like sidecar validation,
// it uses a metadata window before joining payloads: many small recordings take
// one PostgreSQL read round trip, while a single maximum-sized row is still
// processed alone under the independent column ceilings.
func (s *SQLiteStore) loadScriptRecordingCorrelationStatusRepairBatch(
	afterScriptID string,
) ([]scriptRecordingCorrelationStatusSnapshot, string, bool, error) {
	observationByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "observations_json")
	sidecarByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "correlation_observations_json")
	currentObservationByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "r.observations_json")
	currentSidecarByteLengthSQL := scriptRecordingObservationJSONByteLengthSQL(s.db.dialect, "r.correlation_observations_json")
	stalePredicate := `(COALESCE(correlation_status_version, 0) < ?
				OR correlation_status_projected_at IS NULL
				OR correlation_status_projected_at != updated_at
				OR COALESCE(correlation_observation_count, 0) < 0
				OR COALESCE(correlation_observation_count, 0) > ?
				OR COALESCE(correlation_sidecar_version, 0) < ?
				OR COALESCE(correlation_sidecar_version, 0) > ?
				OR (COALESCE(correlation_sidecar_version, 0) = ?
					AND COALESCE(correlation_observation_count, 0) != 0))`
	query := fmt.Sprintf(`WITH status_page AS (
			SELECT script_id, (%s + %s) AS payload_size
			FROM script_recordings
			WHERE script_id > ?
				AND %s <= ?
				AND %s <= ?
				AND COALESCE(observations_projection_version, 0) = ?
				AND observations_projected_at IS NOT NULL
				AND observations_projected_at = updated_at
				AND COALESCE(correlation_status_version, 0) <= ?
				AND %s
			ORDER BY script_id
			LIMIT ?
		), ranked_status AS (
			SELECT script_id, payload_size,
				ROW_NUMBER() OVER (ORDER BY script_id) AS page_row,
				COUNT(*) OVER () AS page_count,
				SUM(payload_size) OVER (
					ORDER BY script_id
					ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
				) AS cumulative_size
			FROM status_page
		), selected_status AS (
			SELECT script_id, payload_size, page_count
			FROM ranked_status
			WHERE page_row = 1 OR cumulative_size <= ?
		)
		SELECT selected_status.script_id,
			COALESCE(r.observations_json, ''),
			COALESCE(r.correlation_observations_json, ''),
			CAST(r.updated_at AS TEXT),
			COALESCE(r.correlation_status_version, 0),
			COALESCE(r.correlation_observation_count, 0),
			COALESCE(r.correlation_sidecar_version, 0),
			CAST(r.correlation_status_projected_at AS TEXT),
			COALESCE(r.observations_projection_version, 0),
			CAST(r.observations_projected_at AS TEXT),
			selected_status.page_count,
			r.script_id
		FROM selected_status
		LEFT JOIN script_recordings r
			ON r.script_id = selected_status.script_id
			AND (%s + %s) = selected_status.payload_size
		ORDER BY selected_status.script_id`,
		observationByteLengthSQL,
		sidecarByteLengthSQL,
		observationByteLengthSQL,
		sidecarByteLengthSQL,
		stalePredicate,
		currentObservationByteLengthSQL,
		currentSidecarByteLengthSQL,
	)
	rows, err := s.db.Query(
		query,
		afterScriptID,
		maxLegacyRecordingObservationsJSONSize,
		maxEncryptedRecordingSidecarSize,
		scriptRecordingObservationsProjectionVersion,
		scriptRecordingCorrelationStatusProjectionVersion,
		scriptRecordingCorrelationStatusProjectionVersion,
		maxRecordingObservations,
		ScriptRecordingSidecarNone,
		ScriptRecordingSidecarV3,
		ScriptRecordingSidecarNone,
		legacyScriptRecordingStatusRepairBatchRows,
		legacyScriptRecordingStatusRepairBatchBytes,
	)
	if err != nil {
		return nil, afterScriptID, false, fmt.Errorf("load script recording correlation status metadata: %w", err)
	}
	snapshots := make([]scriptRecordingCorrelationStatusSnapshot, 0, legacyScriptRecordingStatusRepairBatchRows)
	pageCount := 0
	for rows.Next() {
		var (
			snapshot       scriptRecordingCorrelationStatusSnapshot
			joinedScriptID sql.NullString
			observations   sql.NullString
			sidecar        sql.NullString
			updatedAtRaw   sql.NullString
			statusVersion  sql.NullInt64
			count          sql.NullInt64
			sidecarVersion sql.NullInt64
			statusAt       sql.NullString
			projection     sql.NullInt64
			projectedAt    sql.NullString
		)
		if err := rows.Scan(
			&snapshot.scriptID,
			&observations,
			&sidecar,
			&updatedAtRaw,
			&statusVersion,
			&count,
			&sidecarVersion,
			&statusAt,
			&projection,
			&projectedAt,
			&pageCount,
			&joinedScriptID,
		); err != nil {
			rows.Close()
			return nil, afterScriptID, false, fmt.Errorf("scan script recording correlation status metadata: %w", err)
		}
		snapshot.current = joinedScriptID.Valid &&
			observations.Valid &&
			sidecar.Valid &&
			updatedAtRaw.Valid &&
			statusVersion.Valid &&
			count.Valid &&
			sidecarVersion.Valid &&
			projection.Valid
		if snapshot.current {
			snapshot.observationsPayload = observations.String
			snapshot.sidecarPayload = sidecar.String
			snapshot.updatedAtRaw = updatedAtRaw.String
			snapshot.statusProjectionVersion = int(statusVersion.Int64)
			snapshot.correlationObservationCount = int(count.Int64)
			snapshot.correlationSidecarVersion = int(sidecarVersion.Int64)
			snapshot.statusProjectedAtRaw = statusAt
			snapshot.observationsProjectionVersion = int(projection.Int64)
			snapshot.observationsProjectedAtRaw = projectedAt
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, afterScriptID, false, fmt.Errorf("load script recording correlation status metadata: %w", err)
	}
	rows.Close()
	nextCursor := afterScriptID
	if len(snapshots) > 0 {
		nextCursor = snapshots[len(snapshots)-1].scriptID
	}
	done := len(snapshots) == 0 ||
		(len(snapshots) == pageCount && pageCount < legacyScriptRecordingStatusRepairBatchRows)
	return snapshots, nextCursor, done, nil
}

// repairScriptRecordingCorrelationStatusMetadata derives the small metadata
// needed by status polls after binding/projection/sidecar repair.
func (s *SQLiteStore) repairScriptRecordingCorrelationStatusMetadata() error {
	cursor := ""
	for {
		snapshots, nextCursor, done, err := s.loadScriptRecordingCorrelationStatusRepairBatch(cursor)
		if err != nil {
			return err
		}
		if !done && nextCursor == cursor {
			return fmt.Errorf("script recording correlation status repair made no paging progress after %q", cursor)
		}
		for _, snapshot := range snapshots {
			if !snapshot.current {
				continue
			}
			sidecarVersion := ScriptRecordingSidecarNone
			switch snapshot.sidecarPayload {
			case "", "[]":
			default:
				sidecarVersion = scriptRecordingSidecarVersion(snapshot.sidecarPayload)
				if sidecarVersion == ScriptRecordingSidecarNone {
					// A concurrent malformed/oversized write is handled by the
					// sidecar repair in the next outer convergence pass.
					continue
				}
			}
			observationCount := 0
			if sidecarVersion != ScriptRecordingSidecarNone {
				observationCount = len(decodeStructuralScriptRecordingObservations(snapshot.observationsPayload))
			}
			if _, err := s.rewriteScriptRecordingCorrelationStatusIfUnchanged(
				snapshot,
				observationCount,
				sidecarVersion,
			); err != nil {
				return fmt.Errorf("rewrite script recording correlation status for %s: %w", snapshot.scriptID, err)
			}
		}
		if done {
			return nil
		}
		cursor = nextCursor
	}
}

func (s *SQLiteStore) rewriteScriptRecordingCorrelationStatusIfUnchanged(
	snapshot scriptRecordingCorrelationStatusSnapshot,
	observationCount int,
	sidecarVersion int,
) (bool, error) {
	query := `UPDATE script_recordings
		SET correlation_status_version = ?,
			correlation_observation_count = ?,
			correlation_sidecar_version = ?,
			correlation_status_projected_at = updated_at
		WHERE script_id = ?
			AND COALESCE(observations_json, '') = ?
			AND COALESCE(correlation_observations_json, '') = ?
			AND CAST(updated_at AS TEXT) = ?
			AND COALESCE(correlation_status_version, 0) = ?
			AND COALESCE(correlation_observation_count, 0) = ?
			AND COALESCE(correlation_sidecar_version, 0) = ?
			AND COALESCE(observations_projection_version, 0) = ?`
	args := []any{
		scriptRecordingCorrelationStatusProjectionVersion,
		observationCount,
		sidecarVersion,
		snapshot.scriptID,
		snapshot.observationsPayload,
		snapshot.sidecarPayload,
		snapshot.updatedAtRaw,
		snapshot.statusProjectionVersion,
		snapshot.correlationObservationCount,
		snapshot.correlationSidecarVersion,
		snapshot.observationsProjectionVersion,
	}
	if snapshot.statusProjectedAtRaw.Valid {
		query += ` AND CAST(correlation_status_projected_at AS TEXT) = ?`
		args = append(args, snapshot.statusProjectedAtRaw.String)
	} else {
		query += ` AND correlation_status_projected_at IS NULL`
	}
	if snapshot.observationsProjectedAtRaw.Valid {
		query += ` AND CAST(observations_projected_at AS TEXT) = ?`
		args = append(args, snapshot.observationsProjectedAtRaw.String)
	} else {
		query += ` AND observations_projected_at IS NULL`
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// DeleteScriptRecording removes the recorded baseline for a script.
func (s *SQLiteStore) DeleteScriptRecording(scriptID string) error {
	scriptID = strings.TrimSpace(scriptID)
	if scriptID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM script_recordings WHERE script_id = ?`, scriptID)
	return err
}
