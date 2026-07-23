package storage

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func newRecordingTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func recordingTestCiphertext(prefix string) string {
	return prefix + base64.RawStdEncoding.EncodeToString(make([]byte, minEncryptedRecordingPayloadSize))
}

func TestScriptRecordingRoundTrip(t *testing.T) {
	store := newRecordingTestStore(t)

	// Absent baseline returns (nil, nil).
	got, err := store.GetScriptRecording("script-1")
	if err != nil || got != nil {
		t.Fatalf("expected (nil,nil) for absent recording, got (%v, %v)", got, err)
	}

	record := storeRecording("script-1")
	serialized, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal in-memory recording: %v", err)
	}
	if strings.Contains(string(serialized), `"correlation_observations"`) ||
		strings.Contains(string(serialized), `"token":"raw"`) {
		t.Fatalf("generic recording JSON exposed raw correlation observations: %s", serialized)
	}
	bindRecordingToLiveScript(t, store, record)
	record.ActionGraphDigest = "sha256:recording-action-graph"
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}
	if record.SchemaVersion != scriptRecordingSchemaVersion {
		t.Errorf("schema version not stamped, got %q", record.SchemaVersion)
	}
	if record.RequestCount != 2 {
		t.Errorf("request count = %d, want 2", record.RequestCount)
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		t.Error("created_at/updated_at not stamped")
	}

	loaded, err := store.GetScriptRecording("script-1")
	if err != nil {
		t.Fatalf("GetScriptRecording: %v", err)
	}
	if loaded == nil || len(loaded.Observations) != 2 {
		t.Fatalf("expected 2 observations, got %#v", loaded)
	}
	if loaded.ActionGraphDigest != record.ActionGraphDigest {
		t.Fatalf("action graph digest = %q, want %q", loaded.ActionGraphDigest, record.ActionGraphDigest)
	}
	for _, observation := range loaded.Observations {
		if observation.RequestURL != "" || observation.RequestBody != "" || observation.ResponseBody != "" || observation.RequestHeaders != nil || observation.ResponseHeaders != nil {
			t.Errorf("plaintext observation detail crossed storage boundary: %#v", observation)
		}
	}
	if len(loaded.CorrelationObservations) != 0 {
		t.Errorf("plaintext correlation observations were persisted: %#v", loaded.CorrelationObservations)
	}
	var persistedSidecar string
	if err := store.db.QueryRow(`SELECT correlation_observations_json FROM script_recordings WHERE script_id = ?`, "script-1").Scan(&persistedSidecar); err != nil {
		t.Fatal(err)
	}
	if persistedSidecar != "[]" {
		t.Fatalf("plaintext correlation sidecar = %q, want empty encrypted slot", persistedSidecar)
	}
	var projectionVersion int
	var projectedAt, recordingUpdatedAt time.Time
	if err := store.db.QueryRow(`SELECT observations_projection_version, observations_projected_at, updated_at
		FROM script_recordings WHERE script_id = ?`, "script-1").Scan(&projectionVersion, &projectedAt, &recordingUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if projectionVersion != scriptRecordingObservationsProjectionVersion || !projectedAt.Equal(recordingUpdatedAt) {
		t.Fatalf("saved projection metadata = (version %d, projected %s, updated %s)", projectionVersion, projectedAt, recordingUpdatedAt)
	}

	// Upsert replaces observations.
	record.Observations = record.Observations[:1]
	record.CorrelationObservations = []ScriptRecordingObservation{{ID: 0, ResponseBody: `{"token":"still-raw"}`}}
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	loaded, _ = store.GetScriptRecording("script-1")
	if len(loaded.Observations) != 1 || len(loaded.CorrelationObservations) != 0 {
		t.Errorf("upsert did not replace observations, got obs=%d corr=%d", len(loaded.Observations), len(loaded.CorrelationObservations))
	}
}

func TestScriptRecordingStatusProjectionIsPayloadFreeAndRevisionBound(t *testing.T) {
	if strings.Contains(scriptRecordingStatusSelectQuery, "observations_json") ||
		strings.Contains(scriptRecordingStatusSelectQuery, "correlation_observations_json") {
		t.Fatalf("status query selects a recording payload column:\n%s", scriptRecordingStatusSelectQuery)
	}

	store := newRecordingTestStore(t)
	record := storeRecording("status-projection")
	bindRecordingToLiveScript(t, store, record)
	record.ActionGraphDigest = "sha256:status-projection"
	record.EncryptedCorrelationObservations = recordingTestCiphertext("enc:v3:")
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatal(err)
	}

	status, err := store.GetScriptRecordingStatus(record.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if status == nil ||
		!status.CorrelationMetadataCurrent ||
		status.CorrelationObservationCount != len(record.Observations) ||
		status.CorrelationSidecarVersion != ScriptRecordingSidecarV3 {
		t.Fatalf("current status projection = %#v", status)
	}

	// A rolling old writer advances updated_at without knowing the bounded
	// metadata columns. The read remains payload-free and fails closed until
	// takeover/startup repair derives witnesses for that exact revision.
	nextUpdatedAt := record.UpdatedAt.Add(time.Minute)
	legacyObservations := `[{"id":7,"request_url":"https://legacy.example/private"}]`
	legacySidecar := recordingTestCiphertext("enc:v1:")
	if _, err := store.db.Exec(`UPDATE script_recordings
		SET observations_json = ?,
			correlation_observations_json = ?,
			updated_at = ?
		WHERE script_id = ?`,
		legacyObservations,
		legacySidecar,
		nextUpdatedAt,
		record.ScriptID,
	); err != nil {
		t.Fatal(err)
	}
	status, err = store.GetScriptRecordingStatus(record.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if status == nil || status.CorrelationMetadataCurrent ||
		status.CorrelationObservationCount != 0 ||
		status.CorrelationSidecarVersion != ScriptRecordingSidecarNone {
		t.Fatalf("stale status projection did not fail closed: %#v", status)
	}

	if err := store.RepairLegacyScriptRecordings(); err != nil {
		t.Fatal(err)
	}
	status, err = store.GetScriptRecordingStatus(record.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if status == nil ||
		!status.CorrelationMetadataCurrent ||
		status.CorrelationObservationCount != 1 ||
		status.CorrelationSidecarVersion != ScriptRecordingSidecarV1 {
		t.Fatalf("repaired status projection = %#v", status)
	}

	if _, err := store.db.Exec(`UPDATE script_recordings
		SET correlation_observation_count = ?, correlation_sidecar_version = ?
		WHERE script_id = ?`,
		maxRecordingObservations+1,
		99,
		record.ScriptID,
	); err != nil {
		t.Fatal(err)
	}
	status, err = store.GetScriptRecordingStatus(record.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if status == nil || status.CorrelationMetadataCurrent {
		t.Fatalf("invalid bounded metadata did not fail closed: %#v", status)
	}
	if err := store.RepairLegacyScriptRecordings(); err != nil {
		t.Fatal(err)
	}
	status, err = store.GetScriptRecordingStatus(record.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if status == nil ||
		!status.CorrelationMetadataCurrent ||
		status.CorrelationObservationCount != 1 ||
		status.CorrelationSidecarVersion != ScriptRecordingSidecarV1 {
		t.Fatalf("invalid status metadata did not converge: %#v", status)
	}

	// Unknown future projection versions are not assumed backward-compatible.
	// An older binary must fail the status poll closed instead of interpreting
	// metadata whose semantics may have changed.
	const futureSidecar = "enc:v4:future-ciphertext"
	if _, err := store.db.Exec(`UPDATE script_recordings
		SET correlation_status_version = ?,
			correlation_sidecar_version = ?,
			correlation_observations_json = ?,
			correlation_status_projected_at = NULL
		WHERE script_id = ?`,
		scriptRecordingCorrelationStatusProjectionVersion+1,
		ScriptRecordingSidecarV3+1,
		futureSidecar,
		record.ScriptID,
	); err != nil {
		t.Fatal(err)
	}
	status, err = store.GetScriptRecordingStatus(record.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if status == nil || status.CorrelationMetadataCurrent ||
		status.CorrelationObservationCount != 0 ||
		status.CorrelationSidecarVersion != ScriptRecordingSidecarNone {
		t.Fatalf("future status projection did not fail closed: %#v", status)
	}
	if err := store.RepairLegacyScriptRecordings(); err != nil {
		t.Fatalf("older repair rejected a future status projection: %v", err)
	}
	var (
		futureVersion        int
		persistedSidecar     string
		futureSidecarVersion int
	)
	if err := store.db.QueryRow(`SELECT correlation_status_version,
			correlation_observations_json, correlation_sidecar_version
		FROM script_recordings WHERE script_id = ?`, record.ScriptID).Scan(
		&futureVersion,
		&persistedSidecar,
		&futureSidecarVersion,
	); err != nil {
		t.Fatal(err)
	}
	if futureVersion != scriptRecordingCorrelationStatusProjectionVersion+1 {
		t.Fatalf("older repair rewrote future status projection version %d", futureVersion)
	}
	if persistedSidecar != futureSidecar || futureSidecarVersion != ScriptRecordingSidecarV3+1 {
		t.Fatalf(
			"older repair rewrote future sidecar = (%q, version %d)",
			persistedSidecar,
			futureSidecarVersion,
		)
	}
}

func TestScriptRecordingStatusRepairUsesBoundedDeterministicPages(t *testing.T) {
	store := newRecordingTestStore(t)
	now := time.Now().UTC()
	rowCount := legacyScriptRecordingStatusRepairBatchRows*2 + 7
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < rowCount; index++ {
		scriptID := fmt.Sprintf("status-page-%04d", index)
		if _, err := tx.Exec(`INSERT INTO script_recordings
			(script_id, schema_version, request_count, observations_json,
				observations_projection_version, observations_projected_at,
				correlation_observations_json, correlation_status_version,
				correlation_observation_count, correlation_sidecar_version,
				created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			scriptID,
			scriptRecordingSchemaVersion,
			1,
			`[{"id":0}]`,
			scriptRecordingObservationsProjectionVersion,
			now,
			recordingTestCiphertext("enc:v3:"),
			0,
			0,
			ScriptRecordingSidecarNone,
			now,
			now,
		); err != nil {
			tx.Rollback()
			t.Fatalf("seed stale status %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	firstPage, nextCursor, done, err := store.loadScriptRecordingCorrelationStatusRepairBatch("")
	if err != nil {
		t.Fatal(err)
	}
	if done ||
		len(firstPage) != legacyScriptRecordingStatusRepairBatchRows ||
		nextCursor != fmt.Sprintf("status-page-%04d", legacyScriptRecordingStatusRepairBatchRows-1) {
		t.Fatalf("first status page = rows:%d cursor:%q done:%t", len(firstPage), nextCursor, done)
	}
	if err := store.repairScriptRecordingCorrelationStatusMetadata(); err != nil {
		t.Fatal(err)
	}
	var repaired int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings
		WHERE correlation_status_version = ?
			AND correlation_observation_count = 1
			AND correlation_sidecar_version = ?
			AND correlation_status_projected_at = updated_at`,
		scriptRecordingCorrelationStatusProjectionVersion,
		ScriptRecordingSidecarV3,
	).Scan(&repaired); err != nil {
		t.Fatal(err)
	}
	if repaired != rowCount {
		t.Fatalf("repaired status rows = %d, want %d across bounded pages", repaired, rowCount)
	}
}

func TestScriptRecordingCascadeOnDeleteScript(t *testing.T) {
	store := newRecordingTestStore(t)

	if err := store.SaveScript(&dsl.Script{ID: "script-1", Name: "demo", Protocol: "http"}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	record := storeRecording("script-1")
	bindRecordingToLiveScript(t, store, record)
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}

	if err := store.DeleteScript("script-1"); err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
	got, err := store.GetScriptRecording("script-1")
	if err != nil {
		t.Fatalf("GetScriptRecording after delete: %v", err)
	}
	if got != nil {
		t.Errorf("recording should be cascade-deleted with its script, got %#v", got)
	}
}

func TestScriptRecordingConditionalDeleteHonorsVersionFence(t *testing.T) {
	store := newRecordingTestStore(t)

	if err := store.SaveScript(&dsl.Script{ID: "script-conditional-delete", Name: "initial", Protocol: "http"}); err != nil {
		t.Fatalf("SaveScript: %v", err)
	}
	observed, err := store.GetScript("script-conditional-delete")
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	record := storeRecording(observed.ID)
	bindRecordingToLiveScript(t, store, record)
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}

	updated := *observed
	updated.Name = "concurrent edit"
	changed, err := store.UpdateScriptIfUnchanged(&updated, observed.UpdatedAt)
	if err != nil || !changed {
		t.Fatalf("UpdateScriptIfUnchanged changed=%t error=%v", changed, err)
	}
	current, err := store.GetScript(observed.ID)
	if err != nil {
		t.Fatalf("GetScript after update: %v", err)
	}

	deleted, err := store.DeleteScriptIfUnchanged(observed.ID, observed.UpdatedAt)
	if err != nil {
		t.Fatalf("stale DeleteScriptIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("stale DeleteScriptIfUnchanged unexpectedly deleted the script")
	}
	if record, err := store.GetScriptRecording(observed.ID); err != nil || record == nil {
		t.Fatalf("recording after stale delete = %#v, %v; want retained", record, err)
	}

	deleted, err = store.DeleteScriptIfUnchanged(observed.ID, current.UpdatedAt)
	if err != nil || !deleted {
		t.Fatalf("current DeleteScriptIfUnchanged deleted=%t error=%v", deleted, err)
	}
	if record, err := store.GetScriptRecording(observed.ID); err != nil || record != nil {
		t.Fatalf("recording after successful delete = %#v, %v; want removed", record, err)
	}
}

func TestScriptRecordingCapsObservations(t *testing.T) {
	store := newRecordingTestStore(t)
	record := &ScriptRecordingRecord{ScriptID: "big", Source: "har"}
	bindRecordingToLiveScript(t, store, record)
	for i := 0; i < maxRecordingObservations+50; i++ {
		record.Observations = append(record.Observations, storage_obs(i))
		record.CorrelationObservations = append(record.CorrelationObservations, storage_obs(i))
	}
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}
	if record.RequestCount != maxRecordingObservations+50 {
		t.Errorf("request count = %d, want original %d", record.RequestCount, maxRecordingObservations+50)
	}
	if len(record.Observations) != maxRecordingObservations || len(record.CorrelationObservations) != 0 {
		t.Errorf("observation caps = obs %d corr %d, want %d", len(record.Observations), len(record.CorrelationObservations), maxRecordingObservations)
	}
	loaded, err := store.GetScriptRecording(record.ScriptID)
	if err != nil {
		t.Fatalf("GetScriptRecording: %v", err)
	}
	if loaded.RequestCount != maxRecordingObservations+50 || len(loaded.Observations) != maxRecordingObservations {
		t.Fatalf("durable truncation witness = count:%d retained:%d", loaded.RequestCount, len(loaded.Observations))
	}
}

func TestScriptRecordingRecognizesV1AndV2EncryptedSidecars(t *testing.T) {
	store := newRecordingTestStore(t)
	for _, tc := range []struct {
		id         string
		ciphertext string
	}{
		{id: "legacy-v1", ciphertext: recordingTestCiphertext("enc:v1:")},
		{id: "bound-v2", ciphertext: recordingTestCiphertext("enc:v2:")},
	} {
		record := storeRecording(tc.id)
		bindRecordingToLiveScript(t, store, record)
		record.EncryptedCorrelationObservations = tc.ciphertext
		if err := store.SaveScriptRecording(record); err != nil {
			t.Fatalf("SaveScriptRecording(%s): %v", tc.id, err)
		}
		loaded, err := store.GetScriptRecording(tc.id)
		if err != nil {
			t.Fatalf("GetScriptRecording(%s): %v", tc.id, err)
		}
		if loaded.EncryptedCorrelationObservations != tc.ciphertext {
			t.Fatalf("sidecar(%s) = %q, want %q", tc.id, loaded.EncryptedCorrelationObservations, tc.ciphertext)
		}
	}

	record := storeRecording("plaintext-disguised-as-encrypted")
	bindRecordingToLiveScript(t, store, record)
	record.EncryptedCorrelationObservations = `[{"authorization":"Bearer secret"}]`
	if err := store.SaveScriptRecording(record); err == nil {
		t.Fatal("SaveScriptRecording accepted a non-encrypted sidecar")
	}
	for _, malformed := range []string{
		"enc:v3:plaintext-secret",
		"enc:v3:" + base64.RawStdEncoding.EncodeToString([]byte("too short")),
		recordingTestCiphertext("enc:v3:")[:12] + "\n" + recordingTestCiphertext("enc:v3:")[12:],
		strings.TrimSuffix(recordingTestCiphertext("enc:v3:"), "A") + "B",
	} {
		record.EncryptedCorrelationObservations = malformed
		if err := store.SaveScriptRecording(record); err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("SaveScriptRecording(%q) error = %v, want malformed sidecar rejection", malformed, err)
		}
	}
}

func TestScriptRecordingEncryptedSidecarIsByteBoundedOnWriteReadAndRepair(t *testing.T) {
	store := newRecordingTestStore(t)
	record := storeRecording("oversized-encrypted-sidecar")
	bindRecordingToLiveScript(t, store, record)
	oversized := "enc:v3:" + strings.Repeat("A", maxEncryptedRecordingSidecarSize-len("enc:v3:")+1)
	record.EncryptedCorrelationObservations = oversized
	if err := store.SaveScriptRecording(record); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("SaveScriptRecording oversized error = %v", err)
	}

	record.EncryptedCorrelationObservations = recordingTestCiphertext("enc:v3:")
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording bounded: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE script_recordings SET correlation_observations_json = ? WHERE script_id = ?`, oversized, record.ScriptID); err != nil {
		t.Fatalf("seed oversized sidecar: %v", err)
	}
	loaded, err := store.GetScriptRecording(record.ScriptID)
	if err != nil {
		t.Fatalf("GetScriptRecording: %v", err)
	}
	if loaded == nil || loaded.EncryptedCorrelationObservations != "" {
		t.Fatalf("oversized sidecar crossed read boundary: %#v", loaded)
	}
	if err := store.purgeLegacyPlaintextScriptRecordingSidecars(); err != nil {
		t.Fatalf("purgeLegacyPlaintextScriptRecordingSidecars: %v", err)
	}
	var persisted string
	if err := store.db.QueryRow(`SELECT correlation_observations_json FROM script_recordings WHERE script_id = ?`, record.ScriptID).Scan(&persisted); err != nil {
		t.Fatalf("read repaired sidecar: %v", err)
	}
	if persisted != "[]" {
		t.Fatalf("repaired sidecar = %q, want []", persisted)
	}
}

func TestScriptRecordingMalformedPrefixedSidecarFailsClosedAndIsRepaired(t *testing.T) {
	store := newRecordingTestStore(t)
	record := storeRecording("malformed-prefixed-sidecar")
	bindRecordingToLiveScript(t, store, record)
	record.EncryptedCorrelationObservations = recordingTestCiphertext("enc:v3:")
	if err := store.SaveScriptRecording(record); err != nil {
		t.Fatalf("SaveScriptRecording: %v", err)
	}
	const malformed = "enc:v3:plaintext-secret"
	if _, err := store.db.Exec(`UPDATE script_recordings
		SET correlation_observations_json = ?
		WHERE script_id = ?`, malformed, record.ScriptID); err != nil {
		t.Fatalf("seed malformed sidecar: %v", err)
	}

	loaded, err := store.GetScriptRecording(record.ScriptID)
	if err != nil {
		t.Fatalf("GetScriptRecording: %v", err)
	}
	if loaded == nil || loaded.EncryptedCorrelationObservations != "" {
		t.Fatalf("malformed prefixed sidecar crossed the read boundary: %#v", loaded)
	}
	if invalid, err := store.hasInvalidScriptRecordingSidecars(); err != nil || !invalid {
		t.Fatalf("hasInvalidScriptRecordingSidecars = (%t, %v), want true", invalid, err)
	}
	if err := store.RepairLegacyScriptRecordings(); err != nil {
		t.Fatalf("RepairLegacyScriptRecordings: %v", err)
	}
	var persisted string
	if err := store.db.QueryRow(`SELECT correlation_observations_json
		FROM script_recordings WHERE script_id = ?`, record.ScriptID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "[]" {
		t.Fatalf("repaired malformed sidecar = %q, want []", persisted)
	}
	if invalid, err := store.hasInvalidScriptRecordingSidecars(); err != nil || invalid {
		t.Fatalf("post-repair invalid sidecar = (%t, %v), want false", invalid, err)
	}
}

func TestScriptRecordingSidecarValidationUsesBoundedDeterministicPages(t *testing.T) {
	store := newRecordingTestStore(t)
	now := time.Now().UTC()
	rowCount := legacyScriptRecordingSidecarValidationBatchRows*2 + 7
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < rowCount; index++ {
		scriptID := fmt.Sprintf("sidecar-page-%04d", index)
		if _, err := tx.Exec(`INSERT INTO script_recordings
			(script_id, schema_version, request_count, observations_json,
				correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			scriptID,
			scriptRecordingSchemaVersion,
			1,
			`[{"id":0}]`,
			recordingTestCiphertext("enc:v3:"),
			now,
			now,
		); err != nil {
			tx.Rollback()
			t.Fatalf("seed sidecar %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	firstPage, nextCursor, done, err := store.loadScriptRecordingSidecarValidationBatch("")
	if err != nil {
		t.Fatal(err)
	}
	if done ||
		len(firstPage) != legacyScriptRecordingSidecarValidationBatchRows ||
		nextCursor != fmt.Sprintf("sidecar-page-%04d", legacyScriptRecordingSidecarValidationBatchRows-1) {
		t.Fatalf("first sidecar page = rows:%d cursor:%q done:%t", len(firstPage), nextCursor, done)
	}
	for _, snapshot := range firstPage {
		if !snapshot.current || !IsCanonicalEncryptedScriptRecordingSidecar(snapshot.payload) {
			t.Fatalf("sidecar page returned invalid snapshot: %#v", snapshot)
		}
	}
}

func TestScriptRecordingSidecarValidationPageHonorsAggregateByteBudget(t *testing.T) {
	store := newRecordingTestStore(t)
	now := time.Now().UTC()
	decodedSize := legacyScriptRecordingSidecarValidationBatchBytes * 3 / 4
	largeSidecar := "enc:v3:" + base64.RawStdEncoding.EncodeToString(make([]byte, decodedSize))
	if len(largeSidecar) >= maxEncryptedRecordingSidecarSize {
		t.Fatalf("large sidecar fixture = %d bytes, exceeds per-row cap", len(largeSidecar))
	}
	for index := 0; index < 3; index++ {
		if _, err := store.db.Exec(`INSERT INTO script_recordings
			(script_id, schema_version, request_count, observations_json,
				correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("large-sidecar-%02d", index),
			scriptRecordingSchemaVersion,
			1,
			`[{"id":0}]`,
			largeSidecar,
			now,
			now,
		); err != nil {
			t.Fatal(err)
		}
	}

	page, nextCursor, done, err := store.loadScriptRecordingSidecarValidationBatch("")
	if err != nil {
		t.Fatal(err)
	}
	if done || len(page) != 1 || nextCursor != "large-sidecar-00" {
		t.Fatalf("byte-bounded sidecar page = rows:%d cursor:%q done:%t", len(page), nextCursor, done)
	}
	if !page[0].current || page[0].payload != largeSidecar {
		t.Fatalf("byte-bounded sidecar snapshot = %#v", page[0])
	}
}

func TestScriptRecordingMigrationPurgesLegacyPlaintextSidecars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	legacyV1 := recordingTestCiphertext("enc:v1:")
	legacyV2 := recordingTestCiphertext("enc:v2:")
	fixtures := map[string]string{
		"empty":        "",
		"empty-json":   "[]",
		"legacy-v1":    legacyV1,
		"bound-v2":     legacyV2,
		"malformed-v3": "enc:v3:opaque",
		"plaintext":    `[{"response_body":"session-token"}]`,
		"uppercase":    "ENC:V1:opaque",
		"padded":       " " + legacyV2 + " ",
	}
	for id, sidecar := range fixtures {
		if err := store.SaveScript(&dsl.Script{ID: id, Name: id, Protocol: "http"}); err != nil {
			store.Close()
			t.Fatalf("seed parent %s: %v", id, err)
		}
		parent, err := store.GetScript(id)
		if err != nil {
			store.Close()
			t.Fatalf("load parent %s: %v", id, err)
		}
		if _, err := store.db.Exec(`INSERT INTO script_recordings
			(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, parent.ProjectID, ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt), scriptRecordingSchemaVersion, "test", "scrubbed", 1, `[{"id":0,"request_url":"https://user@example.test/private?id=42","request_headers":{"Authorization":"Bearer secret"},"response_body":"customer-name"}]`, sidecar, now, now); err != nil {
			store.Close()
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := store.SaveScript(&dsl.Script{ID: "malformed-observations", Name: "malformed", Protocol: "http"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	malformedParent, err := store.GetScript("malformed-observations")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "malformed-observations", malformedParent.ProjectID, ScriptIncarnation(malformedParent.ID, malformedParent.ProjectID, malformedParent.RefKey, malformedParent.CreatedAt), scriptRecordingSchemaVersion, "test", "scrubbed", 1, `{"not":"an array"}`, "[]", now, now); err != nil {
		store.Close()
		t.Fatalf("seed malformed observations: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "orphan", DefaultProjectID, "sha256:orphan", scriptRecordingSchemaVersion, "test", "scrubbed", 1, `[{"id":0}]`, "enc:v3:opaque", now, now); err != nil {
		store.Close()
		t.Fatalf("seed orphan: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fixtures["plaintext"] = "[]"
	fixtures["uppercase"] = "[]"
	fixtures["padded"] = "[]"
	fixtures["malformed-v3"] = "[]"
	for id, want := range fixtures {
		var got, observations string
		if err := store.db.QueryRow(`SELECT COALESCE(correlation_observations_json, ''), COALESCE(observations_json, '') FROM script_recordings WHERE script_id = ?`, id).Scan(&got, &observations); err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if got != want {
			t.Fatalf("sidecar(%s) = %q, want %q", id, got, want)
		}
		if observations != `[{"id":0}]` {
			t.Fatalf("observations(%s) = %q, want structural projection", id, observations)
		}
	}
	var malformed string
	if err := store.db.QueryRow(`SELECT observations_json FROM script_recordings WHERE script_id = 'malformed-observations'`).Scan(&malformed); err != nil {
		t.Fatal(err)
	}
	if malformed != "[]" {
		t.Fatalf("malformed observations were not cleared: %q", malformed)
	}
	var orphanCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings WHERE script_id = 'orphan'`).Scan(&orphanCount); err != nil {
		t.Fatal(err)
	}
	if orphanCount != 0 {
		t.Fatalf("orphan recording count = %d, want 0", orphanCount)
	}
	if err := store.migrateLegacyScriptRecordingObservations(); err != nil {
		t.Fatalf("second observation migration run: %v", err)
	}
	if err := store.purgeLegacyPlaintextScriptRecordingSidecars(); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
	var got string
	if err := store.db.QueryRow(`SELECT correlation_observations_json FROM script_recordings WHERE script_id = 'plaintext'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "[]" {
		t.Fatalf("idempotent migration restored plaintext: %q", got)
	}
}

// TestScriptRecordingMigrationBackfillsLegacyBindings proves that upgrading a
// database whose script_recordings rows predate the project_id and
// script_incarnation columns binds those rows to their live parent scripts
// instead of deleting them, while genuinely mismatched rows are still purged.
func TestScriptRecordingMigrationBackfillsLegacyBindings(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "proj-a", Name: "proj-a", CreatedAt: now, UpdatedAt: now}); err != nil {
		store.Close()
		t.Fatalf("SaveProject: %v", err)
	}
	legacyCiphertext := recordingTestCiphertext("enc:v1:")
	seedRecording := func(scriptID, projectID, incarnation string) {
		t.Helper()
		if _, err := store.db.Exec(`INSERT INTO script_recordings
			(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			scriptID, projectID, incarnation, scriptRecordingSchemaVersion, "browser_recording", "encrypted_contains_secrets", 1, `[{"id":0}]`, legacyCiphertext, now, now); err != nil {
			store.Close()
			t.Fatalf("seed recording %s: %v", scriptID, err)
		}
	}
	for _, parent := range []*dsl.Script{
		{ID: "legacy-default", Name: "legacy-default", Protocol: "http"},
		{ID: "legacy-projected", Name: "legacy-projected", Protocol: "http", ProjectID: "proj-a"},
		{ID: "stale-incarnation", Name: "stale-incarnation", Protocol: "http"},
		{ID: "project-mismatch", Name: "project-mismatch", Protocol: "http"},
	} {
		if err := store.SaveScript(parent); err != nil {
			store.Close()
			t.Fatalf("SaveScript(%s): %v", parent.ID, err)
		}
	}
	// Legacy rows: both binding columns are empty, exactly as an upgraded
	// pre-existing database presents them.
	seedRecording("legacy-default", "", "")
	seedRecording("legacy-projected", "", "")
	// A non-empty incarnation that does not match the live parent (script ID
	// reuse) must still be purged.
	seedRecording("stale-incarnation", "", "sha256:stale")
	// A non-empty project that contradicts the parent's project must be purged.
	seedRecording("project-mismatch", "proj-a", "")
	// A recording without a parent script must be purged.
	seedRecording("orphan", "", "")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, scriptID := range []string{"legacy-default", "legacy-projected"} {
		parent, err := store.GetScript(scriptID)
		if err != nil {
			t.Fatalf("GetScript(%s): %v", scriptID, err)
		}
		var projectID, incarnation, sidecar string
		if err := store.db.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(script_incarnation, ''), COALESCE(correlation_observations_json, '')
			FROM script_recordings WHERE script_id = ?`, scriptID).Scan(&projectID, &incarnation, &sidecar); err != nil {
			t.Fatalf("legacy recording %s did not survive migration: %v", scriptID, err)
		}
		wantProject := normalizeProjectID(parent.ProjectID)
		wantIncarnation := ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt)
		if projectID != wantProject || incarnation != wantIncarnation {
			t.Fatalf("backfilled binding(%s) = (%q, %q), want (%q, %q)", scriptID, projectID, incarnation, wantProject, wantIncarnation)
		}
		if sidecar != legacyCiphertext {
			t.Fatalf("legacy sidecar(%s) = %q, want retained ciphertext", scriptID, sidecar)
		}
		loaded, err := store.GetScriptRecording(scriptID)
		if err != nil || loaded == nil {
			t.Fatalf("GetScriptRecording(%s) after backfill = %#v, %v; want live record", scriptID, loaded, err)
		}
		if loaded.EncryptedCorrelationObservations != legacyCiphertext {
			t.Fatalf("loaded sidecar(%s) = %q, want retained ciphertext", scriptID, loaded.EncryptedCorrelationObservations)
		}
	}
	for _, scriptID := range []string{"stale-incarnation", "project-mismatch", "orphan"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings WHERE script_id = ?`, scriptID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("mismatched recording %s survived migration", scriptID)
		}
	}

	// A second pass is idempotent: the backfilled rows remain valid and intact.
	if err := store.purgeInvalidScriptRecordings(); err != nil {
		t.Fatalf("second purge run: %v", err)
	}
	var survivors int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings`).Scan(&survivors); err != nil {
		t.Fatal(err)
	}
	if survivors != 2 {
		t.Fatalf("recordings after idempotent rerun = %d, want 2", survivors)
	}
}

func TestScriptRecordingBindingRepairIsBoundedAcrossHighCardinality(t *testing.T) {
	store := newRecordingTestStore(t)
	const rowCount = legacyScriptRecordingBindingBatchRows*4 + 9
	parents := make([]*dsl.Script, 0, rowCount)
	for index := 0; index < rowCount; index++ {
		scriptID := fmt.Sprintf("binding-page-%04d", index)
		if err := store.SaveScript(&dsl.Script{ID: scriptID, Name: scriptID, Protocol: "http"}); err != nil {
			t.Fatalf("SaveScript(%d): %v", index, err)
		}
		parent, err := store.GetScript(scriptID)
		if err != nil {
			t.Fatalf("GetScript(%d): %v", index, err)
		}
		parents = append(parents, parent)
	}
	now := time.Now().UTC()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range parents {
		if _, err := tx.Exec(`INSERT INTO script_recordings
			(script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			parent.ID,
			"apexload.recording.legacy",
			"legacy-controller",
			"scrubbed",
			1,
			`[{"id":0}]`,
			recordingTestCiphertext("enc:v1:"),
			now,
			now,
		); err != nil {
			tx.Rollback()
			t.Fatalf("seed recording %s: %v", parent.ID, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO script_recordings
		(script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"binding-page-orphan",
		"apexload.recording.legacy",
		"legacy-controller",
		"scrubbed",
		1,
		`[{"id":0}]`,
		"[]",
		now,
		now,
	); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	firstBatch, nextCursor, done, err := store.loadScriptRecordingBindingRepairBatch("", true)
	if err != nil {
		t.Fatal(err)
	}
	if done || len(firstBatch) != legacyScriptRecordingBindingBatchRows ||
		nextCursor != "binding-page-0063" {
		t.Fatalf("first binding page = rows:%d cursor:%q done:%t", len(firstBatch), nextCursor, done)
	}

	// This cardinality crosses several fixed pages and exercises deterministic
	// cursor progress without retaining O(total rows) snapshots or issuing one
	// metadata query per current recording.
	if err := store.purgeInvalidScriptRecordings(); err != nil {
		t.Fatal(err)
	}
	var survivors, unbound int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings`).Scan(&survivors); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings
		WHERE TRIM(COALESCE(project_id, '')) = ''
			OR TRIM(COALESCE(script_incarnation, '')) = ''`).Scan(&unbound); err != nil {
		t.Fatal(err)
	}
	if survivors != rowCount || unbound != 0 {
		t.Fatalf("bounded binding repair survivors=%d unbound=%d, want %d/0", survivors, unbound, rowCount)
	}
}

func TestScriptRecordingBindingRepairParentCASConverges(t *testing.T) {
	t.Run("backfill", func(t *testing.T) {
		store := newRecordingTestStore(t)
		if err := store.SaveScript(&dsl.Script{ID: "binding-cas-backfill", Name: "binding-cas-backfill", Protocol: "http"}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, err := store.db.Exec(`INSERT INTO script_recordings
			(script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"binding-cas-backfill",
			"apexload.recording.legacy",
			"legacy-controller",
			"scrubbed",
			1,
			`[{"id":0}]`,
			"[]",
			now,
			now,
		); err != nil {
			t.Fatal(err)
		}
		candidates, _, _, err := store.loadScriptRecordingBindingRepairBatch("", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 1 {
			t.Fatalf("binding repair candidates = %d, want 1", len(candidates))
		}
		candidate := candidates[0]
		if _, err := store.db.Exec(`UPDATE scripts SET ref_key = ? WHERE id = ?`,
			"changed-script-binding-cas-backfill-v1",
			"binding-cas-backfill",
		); err != nil {
			t.Fatal(err)
		}
		updated, err := store.backfillScriptRecordingBindingIfUnchanged(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if updated {
			t.Fatal("stale parent snapshot backfilled a recording")
		}
		var projectID, incarnation string
		if err := store.db.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(script_incarnation, '')
			FROM script_recordings WHERE script_id = ?`, "binding-cas-backfill").Scan(&projectID, &incarnation); err != nil {
			t.Fatal(err)
		}
		if projectID != "" || incarnation != "" {
			t.Fatalf("stale backfill mutated binding to (%q, %q)", projectID, incarnation)
		}

		if err := store.purgeInvalidScriptRecordings(); err != nil {
			t.Fatal(err)
		}
		parent, err := store.GetScript("binding-cas-backfill")
		if err != nil {
			t.Fatal(err)
		}
		wantIncarnation := ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt)
		if err := store.db.QueryRow(`SELECT project_id, script_incarnation
			FROM script_recordings WHERE script_id = ?`, parent.ID).Scan(&projectID, &incarnation); err != nil {
			t.Fatal(err)
		}
		if projectID != parent.ProjectID || incarnation != wantIncarnation {
			t.Fatalf("converged backfill = (%q, %q), want (%q, %q)", projectID, incarnation, parent.ProjectID, wantIncarnation)
		}
	})

	t.Run("delete", func(t *testing.T) {
		store := newRecordingTestStore(t)
		if err := store.SaveScript(&dsl.Script{ID: "binding-cas-delete", Name: "binding-cas-delete", Protocol: "http"}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, err := store.db.Exec(`INSERT INTO script_recordings
			(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"binding-cas-delete",
			"wrong-project",
			"sha256:wrong",
			"apexload.recording.legacy",
			"legacy-controller",
			"scrubbed",
			1,
			`[{"id":0}]`,
			"[]",
			now,
			now,
		); err != nil {
			t.Fatal(err)
		}
		candidates, _, _, err := store.loadScriptRecordingBindingRepairBatch("", true)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 1 {
			t.Fatalf("binding repair candidates = %d, want 1", len(candidates))
		}
		candidate := candidates[0]
		if _, err := store.db.Exec(`UPDATE scripts SET ref_key = ? WHERE id = ?`,
			"changed-script-binding-cas-delete-v1",
			"binding-cas-delete",
		); err != nil {
			t.Fatal(err)
		}
		deleted, err := store.deleteInvalidScriptRecordingIfUnchanged(candidate.snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if deleted {
			t.Fatal("stale parent snapshot deleted a recording")
		}
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings WHERE script_id = ?`,
			"binding-cas-delete",
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("stale delete left %d rows, want 1", count)
		}
		if err := store.purgeInvalidScriptRecordings(); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings WHERE script_id = ?`,
			"binding-cas-delete",
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("recomputed invalid binding left %d rows, want 0", count)
		}
	})
}

// TestScriptRecordingObservationMigrationRepairsRollingUpgradeWrites proves a
// legacy controller remains detectable after another controller has already
// projected the row. Legacy inserts leave the per-row version at zero; legacy
// updates leave the projected-at witness behind updated_at.
func TestScriptRecordingObservationMigrationRepairsRollingUpgradeWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "rolling-upgrade", Name: "rolling-upgrade", Protocol: "http"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	parent, err := store.GetScript("rolling-upgrade")
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	firstLegacyWrite := time.Now().UTC().Add(-time.Minute)
	rawObservations := `[{"id":0,"request_url":"https://example.test/private"}]`
	// This is the column list used by a pre-projection controller. The new
	// columns take their defaults and make the row eligible on the next startup.
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"rolling-upgrade", parent.ProjectID, ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt), scriptRecordingSchemaVersion, "test", "scrubbed", 1, rawObservations, "[]", firstLegacyWrite, firstLegacyWrite); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var observations string
	var projectionVersion int
	var projectedAt, updatedAt time.Time
	if err := store.db.QueryRow(`SELECT observations_json, observations_projection_version, observations_projected_at, updated_at
		FROM script_recordings WHERE script_id = 'rolling-upgrade'`).Scan(&observations, &projectionVersion, &projectedAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if observations != `[{"id":0}]` {
		t.Fatalf("legacy insert observations = %q, want structural projection", observations)
	}
	if projectionVersion != scriptRecordingObservationsProjectionVersion || !projectedAt.Equal(updatedAt) {
		t.Fatalf("legacy insert projection metadata = (version %d, projected %s, updated %s)", projectionVersion, projectedAt, updatedAt)
	}

	// A rolling old replica can subsequently overwrite this same row while
	// omitting the new projection columns. updated_at advances but the witness
	// does not, so the following startup must project it again even though the
	// numeric version still says it was projected once.
	secondLegacyWrite := updatedAt.Add(time.Minute)
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(script_id) DO UPDATE SET
			schema_version=excluded.schema_version,
			source=excluded.source,
			sensitivity=excluded.sensitivity,
			request_count=excluded.request_count,
			observations_json=excluded.observations_json,
			correlation_observations_json=excluded.correlation_observations_json,
			updated_at=excluded.updated_at`,
		parent.ID, parent.ProjectID, ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt),
		scriptRecordingSchemaVersion, "legacy-controller", "scrubbed", 1, rawObservations, "[]", secondLegacyWrite, secondLegacyWrite); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetScriptRecording(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Observations) != 1 || loaded.Observations[0].RequestURL != "" {
		t.Fatalf("legacy write crossed read boundary before restart: %#v", loaded)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.db.QueryRow(`SELECT observations_json, observations_projection_version, observations_projected_at, updated_at
		FROM script_recordings WHERE script_id = 'rolling-upgrade'`).Scan(&observations, &projectionVersion, &projectedAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if observations != `[{"id":0}]` {
		t.Fatalf("legacy update observations = %q, want structural projection", observations)
	}
	if projectionVersion != scriptRecordingObservationsProjectionVersion || !projectedAt.Equal(secondLegacyWrite) || !projectedAt.Equal(updatedAt) {
		t.Fatalf("legacy update projection metadata = (version %d, projected %s, updated %s), want version %d at %s",
			projectionVersion, projectedAt, updatedAt, scriptRecordingObservationsProjectionVersion, secondLegacyWrite)
	}
}

func TestScriptRecordingObservationMigrationUsesBoundedDeterministicBatches(t *testing.T) {
	store := newRecordingTestStore(t)
	now := time.Now().UTC()
	rowCount := legacyScriptRecordingProjectionBatchRows*2 + 7
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < rowCount; index++ {
		scriptID := fmt.Sprintf("batch-%04d", index)
		if _, err := tx.Exec(`INSERT INTO script_recordings
			(script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			scriptID, "apexload.recording.legacy", "legacy-controller", "scrubbed", 1,
			`[{"id":0,"request_url":"https://legacy.example/private"}]`, "[]", now, now); err != nil {
			tx.Rollback()
			t.Fatalf("seed legacy recording %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	firstBatch, nextCursor, done, err := store.loadLegacyScriptRecordingObservationProjectionBatch("")
	if err != nil {
		t.Fatal(err)
	}
	if done || len(firstBatch) != legacyScriptRecordingProjectionBatchRows || nextCursor != "batch-0063" {
		t.Fatalf("first batch = rows %d cursor %q done %t", len(firstBatch), nextCursor, done)
	}
	if err := store.migrateLegacyScriptRecordingObservations(); err != nil {
		t.Fatal(err)
	}
	var projected int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings
		WHERE observations_json = '[{"id":0}]'
			AND observations_projection_version = ?
			AND observations_projected_at = updated_at`, scriptRecordingObservationsProjectionVersion).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != rowCount {
		t.Fatalf("projected rows = %d, want %d across multiple batches", projected, rowCount)
	}
}

func TestScriptRecordingObservationMigrationBatchHonorsAggregateByteBudget(t *testing.T) {
	store := newRecordingTestStore(t)
	now := time.Now().UTC()
	body := strings.Repeat("x", legacyScriptRecordingProjectionBatchBytes*3/4)
	payload, err := json.Marshal([]ScriptRecordingObservation{{ID: 0, ResponseBody: body}})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if _, err := store.db.Exec(`INSERT INTO script_recordings
			(script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("large-%02d", index), "apexload.recording.legacy", "legacy-controller", "scrubbed", 1,
			string(payload), "[]", now, now); err != nil {
			t.Fatal(err)
		}
	}

	batch, nextCursor, done, err := store.loadLegacyScriptRecordingObservationProjectionBatch("")
	if err != nil {
		t.Fatal(err)
	}
	if done || len(batch) != 1 || nextCursor != "large-00" {
		t.Fatalf("byte-bounded batch = rows %d cursor %q done %t", len(batch), nextCursor, done)
	}
	if err := store.migrateLegacyScriptRecordingObservations(); err != nil {
		t.Fatal(err)
	}
	var projected int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM script_recordings
		WHERE observations_json = '[{"id":0}]' AND observations_projection_version = ?`,
		scriptRecordingObservationsProjectionVersion).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != 3 {
		t.Fatalf("byte-paged projected rows = %d, want 3", projected)
	}
}

func TestScriptRecordingObservationMigrationEnforcesUTF8ByteCap(t *testing.T) {
	store := newRecordingTestStore(t)
	if err := store.SaveScript(&dsl.Script{ID: "multibyte-over-byte-cap", Name: "Multibyte", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.GetScript("multibyte-over-byte-cap")
	if err != nil {
		t.Fatal(err)
	}
	payload := `[{"id":0,"response_body":"` +
		strings.Repeat("界", maxLegacyRecordingObservationsJSONSize/3+1) +
		`"}]`
	if len(payload) <= maxLegacyRecordingObservationsJSONSize {
		t.Fatalf("multibyte fixture = %d bytes, want over %d", len(payload), maxLegacyRecordingObservationsJSONSize)
	}
	if utf8.RuneCountInString(payload) >= maxLegacyRecordingObservationsJSONSize {
		t.Fatalf("multibyte fixture = %d characters, must demonstrate character-length bypass", utf8.RuneCountInString(payload))
	}
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		parent.ID,
		parent.ProjectID,
		ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt),
		"apexload.recording.legacy", "legacy-controller", "scrubbed", 1,
		payload, "[]", now, now); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetScriptRecording(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Observations) != 0 {
		t.Fatalf("oversized multibyte read did not fail closed: %#v", loaded)
	}
	var persistedBeforeMigration string
	if err := store.db.QueryRow(`SELECT observations_json FROM script_recordings WHERE script_id = ?`, parent.ID).Scan(&persistedBeforeMigration); err != nil {
		t.Fatal(err)
	}
	if persistedBeforeMigration != payload {
		t.Fatal("bounded read unexpectedly mutated the stored oversized payload")
	}
	if err := store.migrateLegacyScriptRecordingObservations(); err != nil {
		t.Fatal(err)
	}
	var observations string
	var projectionVersion int
	if err := store.db.QueryRow(`SELECT observations_json, observations_projection_version
		FROM script_recordings WHERE script_id = 'multibyte-over-byte-cap'`).Scan(&observations, &projectionVersion); err != nil {
		t.Fatal(err)
	}
	if observations != "[]" || projectionVersion != scriptRecordingObservationsProjectionVersion {
		t.Fatalf("multibyte oversized projection = %q version %d, want [] version %d",
			observations, projectionVersion, scriptRecordingObservationsProjectionVersion)
	}
}

func TestDecodeStructuralScriptRecordingObservationsStreamsAdversarialArray(t *testing.T) {
	const observationCount = 1_000_000
	var payload strings.Builder
	payload.Grow(observationCount*9 + 2)
	payload.WriteByte('[')
	for index := 0; index < observationCount; index++ {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"id":7}`)
	}
	payload.WriteByte(']')
	if payload.Len() >= maxLegacyRecordingObservationsJSONSize {
		t.Fatalf("adversarial fixture = %d bytes, must remain below migration cap", payload.Len())
	}

	var projected []ScriptRecordingObservation
	allocations := testing.AllocsPerRun(1, func() {
		projected = decodeStructuralScriptRecordingObservations(payload.String())
	})
	if len(projected) != maxRecordingObservations {
		t.Fatalf("projected observations = %d, want %d", len(projected), maxRecordingObservations)
	}
	for index, observation := range projected {
		if observation.ID != 7 {
			t.Fatalf("projected observation %d id = %d, want 7", index, observation.ID)
		}
	}
	// The streaming decoder may allocate its bounded input buffer and retained
	// 500-row result, but must not allocate one retained object per source row.
	if allocations > 10_000 {
		t.Fatalf("streaming projection allocations = %.0f, want <= 10000 for %d source rows", allocations, observationCount)
	}
}

func TestDecodeStructuralScriptRecordingObservationsFailsClosedAfterRetainedPrefix(t *testing.T) {
	var malformed strings.Builder
	malformed.WriteByte('[')
	for index := 0; index < maxRecordingObservations+25; index++ {
		if index > 0 {
			malformed.WriteByte(',')
		}
		malformed.WriteString(`{"id":1}`)
	}
	malformed.WriteString(`,{"id":"wrong-type"}]`)

	for name, payload := range map[string]string{
		"late type mismatch": malformed.String(),
		"trailing value":     `[{"id":1}] {"id":2}`,
		"non-array":          `{"id":1}`,
		"truncated":          `[{"id":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if projected := decodeStructuralScriptRecordingObservations(payload); len(projected) != 0 {
				t.Fatalf("malformed payload projected %#v, want []", projected)
			}
		})
	}
}

func TestGetScriptRecordingStreamsAdversarialLegacyObservations(t *testing.T) {
	store := newRecordingTestStore(t)
	if err := store.SaveScript(&dsl.Script{ID: "legacy-adversarial-read", Name: "Legacy read", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.GetScript("legacy-adversarial-read")
	if err != nil {
		t.Fatal(err)
	}
	const observationCount = 1_000_000
	var payload strings.Builder
	payload.Grow(observationCount*9 + 2)
	payload.WriteByte('[')
	for index := 0; index < observationCount; index++ {
		if index > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"id":7}`)
	}
	payload.WriteByte(']')
	if payload.Len() >= maxLegacyRecordingObservationsJSONSize {
		t.Fatalf("adversarial read fixture = %d bytes, must remain below migration cap", payload.Len())
	}
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		parent.ID,
		parent.ProjectID,
		ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt),
		"apexload.recording.legacy",
		"legacy-controller",
		"scrubbed",
		observationCount,
		payload.String(),
		"[]",
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.GetScriptRecording(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Observations) != maxRecordingObservations {
		t.Fatalf("adversarial legacy read observations = %#v", loaded)
	}
	for index, observation := range loaded.Observations {
		if observation.ID != 7 || observation.RequestURL != "" || observation.ResponseBody != "" {
			t.Fatalf("adversarial legacy read observation %d = %#v", index, observation)
		}
	}

	var malformed strings.Builder
	malformed.WriteByte('[')
	for index := 0; index < maxRecordingObservations+25; index++ {
		if index > 0 {
			malformed.WriteByte(',')
		}
		malformed.WriteString(`{"id":1}`)
	}
	malformed.WriteString(`,{"id":"wrong-type"}]`)
	if _, err := store.db.Exec(`UPDATE script_recordings SET observations_json = ?, updated_at = ?
		WHERE script_id = ?`, malformed.String(), now.Add(time.Second), parent.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetScriptRecording(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Observations) != 0 {
		t.Fatalf("malformed legacy read did not fail closed: %#v", loaded)
	}
}

func TestRepairLegacyScriptRecordingsConvergesBindingInsertedDuringProjection(t *testing.T) {
	store := newRecordingTestStore(t)
	for _, scriptID := range []string{"a-late", "z-seed"} {
		if err := store.SaveScript(&dsl.Script{ID: scriptID, Name: scriptID, Protocol: "http"}); err != nil {
			t.Fatal(err)
		}
	}
	lateParent, err := store.GetScript("a-late")
	if err != nil {
		t.Fatal(err)
	}
	seedParent, err := store.GetScript("z-seed")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		seedParent.ID,
		seedParent.ProjectID,
		ScriptIncarnation(seedParent.ID, seedParent.ProjectID, seedParent.RefKey, seedParent.CreatedAt),
		"apexload.recording.legacy",
		"legacy-controller",
		"scrubbed",
		1,
		`[{"id":9,"request_url":"https://legacy.example/seed"}]`,
		"[]",
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER insert_late_legacy_recording
		AFTER UPDATE OF observations_json ON script_recordings
		WHEN NEW.script_id = 'z-seed'
			AND NOT EXISTS (SELECT 1 FROM script_recordings WHERE script_id = 'a-late')
		BEGIN
			INSERT INTO script_recordings
				(script_id, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
			VALUES
				('a-late', 'apexload.recording.legacy', 'legacy-controller', 'scrubbed', 1,
				 '[{"id":7,"request_url":"https://legacy.example/private"}]', '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		END`); err != nil {
		t.Fatal(err)
	}

	if err := store.RepairLegacyScriptRecordings(); err != nil {
		t.Fatal(err)
	}

	var projectID, incarnation, observations string
	var projectionVersion int
	var projectedAt, updatedAt time.Time
	if err := store.db.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(script_incarnation, ''),
			COALESCE(observations_json, ''), COALESCE(observations_projection_version, 0),
			observations_projected_at, updated_at
		FROM script_recordings WHERE script_id = 'a-late'`).Scan(
		&projectID,
		&incarnation,
		&observations,
		&projectionVersion,
		&projectedAt,
		&updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	wantIncarnation := ScriptIncarnation(lateParent.ID, lateParent.ProjectID, lateParent.RefKey, lateParent.CreatedAt)
	if projectID != lateParent.ProjectID || incarnation != wantIncarnation {
		t.Fatalf("late binding = (%q, %q), want (%q, %q)", projectID, incarnation, lateParent.ProjectID, wantIncarnation)
	}
	if observations != `[{"id":7}]` {
		t.Fatalf("late observations = %q, want structural projection", observations)
	}
	if projectionVersion != scriptRecordingObservationsProjectionVersion || !projectedAt.Equal(updatedAt) {
		t.Fatalf("late projection metadata = (version %d, projected %s, updated %s)", projectionVersion, projectedAt, updatedAt)
	}
	loaded, err := store.GetScriptRecording("a-late")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Observations) != 1 || loaded.Observations[0].ID != 7 {
		t.Fatalf("late recording is not readable after repair: %#v", loaded)
	}
}

func TestScriptRecordingStartupMigrationCASPreservesRefreshedRow(t *testing.T) {
	store := newRecordingTestStore(t)
	if err := store.SaveScript(&dsl.Script{ID: "migration-cas", Name: "migration-cas", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.GetScript("migration-cas")
	if err != nil {
		t.Fatal(err)
	}
	staleUpdatedAt := time.Now().UTC().Add(-time.Hour)
	staleSidecar := "enc:v3:stale"
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		parent.ID, "wrong-project", "sha256:stale", scriptRecordingSchemaVersion, "test", "scrubbed", 1,
		`[{"id":0,"request_url":"https://legacy.example/private"}]`, staleSidecar, staleUpdatedAt, staleUpdatedAt); err != nil {
		t.Fatal(err)
	}

	invalidSnapshot := invalidScriptRecordingSnapshot{
		scriptID:     parent.ID,
		projectID:    "wrong-project",
		incarnation:  "sha256:stale",
		updatedAtRaw: staleUpdatedAt.String(),
	}
	legacyPayload := `[{"id":0,"request_url":"https://legacy.example/private"}]`
	freshPayload := `[{"id":0}]`
	freshUpdatedAt := staleUpdatedAt.Add(30 * time.Minute)
	freshIncarnation := ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt)
	freshSidecar := recordingTestCiphertext("enc:v3:")
	if _, err := store.db.Exec(`UPDATE script_recordings SET
		project_id = ?, script_incarnation = ?, observations_json = ?, correlation_observations_json = ?, updated_at = ?
		WHERE script_id = ?`, parent.ProjectID, freshIncarnation, freshPayload, freshSidecar, freshUpdatedAt, parent.ID); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.deleteInvalidScriptRecordingIfUnchanged(invalidSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("stale cleanup snapshot deleted a concurrently refreshed recording")
	}
	rewritten, err := store.rewriteLegacyScriptRecordingObservationsIfUnchanged(legacyScriptRecordingObservationSnapshot{
		scriptID:          parent.ID,
		payload:           legacyPayload,
		updatedAtRaw:      staleUpdatedAt.String(),
		projectionVersion: 0,
	}, `[{"id":99}]`)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten {
		t.Fatal("stale observation snapshot overwrote a concurrently refreshed recording")
	}
	cleared, err := store.clearMalformedScriptRecordingSidecarIfUnchanged(parent.ID, staleSidecar, staleUpdatedAt.String())
	if err != nil {
		t.Fatal(err)
	}
	if cleared {
		t.Fatal("stale sidecar cleanup snapshot erased a concurrently refreshed recording")
	}

	var projectID, incarnation, observations, sidecar string
	if err := store.db.QueryRow(`SELECT project_id, script_incarnation, observations_json, correlation_observations_json
		FROM script_recordings WHERE script_id = ?`, parent.ID).Scan(&projectID, &incarnation, &observations, &sidecar); err != nil {
		t.Fatal(err)
	}
	if projectID != parent.ProjectID || incarnation != freshIncarnation || observations != freshPayload || sidecar != freshSidecar {
		t.Fatalf("refreshed recording was mutated: project=%q incarnation=%q observations=%q sidecar=%q", projectID, incarnation, observations, sidecar)
	}
}

func TestScriptRecordingRejectsStaleOrReusedParent(t *testing.T) {
	store := newRecordingTestStore(t)
	record := storeRecording("reused-script")
	record.ProjectID = DefaultProjectID
	record.ScriptIncarnation = "sha256:not-a-live-parent"
	record.ScriptUpdatedAt = time.Now().UTC()
	if err := store.SaveScriptRecording(record); !errors.Is(err, ErrScriptRecordingParentChanged) {
		t.Fatalf("missing parent error = %v, want ErrScriptRecordingParentChanged", err)
	}

	first := &dsl.Script{ID: "reused-script", Name: "first", Protocol: "http", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := store.SaveScript(first); err != nil {
		t.Fatal(err)
	}
	bindRecordingToLiveScript(t, store, record)
	staleIncarnation := record.ScriptIncarnation
	first.Name = "concurrent edit"
	if err := store.SaveScript(first); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScriptRecording(record); !errors.Is(err, ErrScriptRecordingParentChanged) {
		t.Fatalf("stale parent revision error = %v, want ErrScriptRecordingParentChanged", err)
	}
	if err := store.DeleteScript(first.ID); err != nil {
		t.Fatal(err)
	}
	second := &dsl.Script{ID: first.ID, Name: "second", Protocol: "http", CreatedAt: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)}
	if err := store.SaveScript(second); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScriptRecording(record); !errors.Is(err, ErrScriptRecordingParentChanged) {
		t.Fatalf("reused parent error = %v, want ErrScriptRecordingParentChanged", err)
	}

	current, err := store.GetScript(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source, sensitivity, request_count, observations_json, correlation_observations_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, current.ID, current.ProjectID, staleIncarnation, scriptRecordingSchemaVersion, "test", "scrubbed", 1, `[{"id":0}]`, "enc:v3:opaque", time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetScriptRecording(current.ID)
	if err != nil || loaded != nil {
		t.Fatalf("stale recording read = %#v, %v; want nil", loaded, err)
	}
	if err := store.purgeInvalidScriptRecordings(); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT script_id FROM script_recordings WHERE script_id = ?`, current.ID).Scan(new(string)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale recording purge error = %v, want no row", err)
	}
}

// TestScriptRecordingMigrationSQLRewritesForPostgres guards the runtime SQL
// introduced by the recording-binding backfill, legacy-ciphertext count, and
// conditional-delete parent lock: there is no separate Postgres schema, so
// every statement must survive rewriteSQL translation.
func TestScriptRecordingMigrationSQLRewritesForPostgres(t *testing.T) {
	backfill := rewriteSQL(`UPDATE script_recordings
		SET project_id = ?, script_incarnation = ?
		WHERE script_id = ?
			AND COALESCE(project_id, '') = ?
			AND COALESCE(script_incarnation, '') = ?
			AND CAST(updated_at AS TEXT) = ?
			AND EXISTS (
				SELECT 1 FROM scripts s
				WHERE s.id = script_recordings.script_id
					AND COALESCE(s.project_id, '') = ?
					AND COALESCE(s.ref_key, '') = ?
					AND CAST(s.created_at AS TEXT) = ?
			)`, storeDialectPostgres)
	if !strings.Contains(backfill, "$9") || strings.Contains(backfill, "?") {
		t.Fatalf("backfill placeholders not rewritten: %s", backfill)
	}
	if !strings.Contains(backfill, "EXISTS") || !strings.Contains(backfill, "CAST(s.created_at AS TEXT)") {
		t.Fatalf("backfill parent CAS mangled by rewrite: %s", backfill)
	}
	bindingPage := rewriteSQL(`SELECT r.script_id, COALESCE(r.project_id, '')
		FROM script_recordings r
		LEFT JOIN scripts s ON s.id = r.script_id
		WHERE r.script_id > ?
		ORDER BY r.script_id
		LIMIT ?`, storeDialectPostgres)
	if !strings.Contains(bindingPage, "$2") || strings.Contains(bindingPage, "?") ||
		!strings.Contains(bindingPage, "ORDER BY r.script_id") {
		t.Fatalf("binding metadata page query not rewritten deterministically: %s", bindingPage)
	}
	conditionalDelete := rewriteSQL(`DELETE FROM script_recordings
		WHERE script_id = ? AND COALESCE(project_id, '') = ?
			AND COALESCE(script_incarnation, '') = ? AND CAST(updated_at AS TEXT) = ?
			AND EXISTS (
				SELECT 1 FROM scripts s
				WHERE s.id = script_recordings.script_id
					AND COALESCE(s.project_id, '') = ?
					AND COALESCE(s.ref_key, '') = ?
					AND CAST(s.created_at AS TEXT) = ?
			)`, storeDialectPostgres)
	if !strings.Contains(conditionalDelete, "$7") || strings.Contains(conditionalDelete, "?") {
		t.Fatalf("conditional delete placeholders not rewritten: %s", conditionalDelete)
	}
	conditionalObservationRewrite := rewriteSQL(`UPDATE script_recordings
		SET observations_json = ?, observations_projection_version = ?, observations_projected_at = updated_at
		WHERE script_id = ? AND COALESCE(observations_json, '') = ?
			AND CAST(updated_at AS TEXT) = ? AND COALESCE(observations_projection_version, 0) = ?
			AND CAST(observations_projected_at AS TEXT) = ?`, storeDialectPostgres)
	if !strings.Contains(conditionalObservationRewrite, "$7") || strings.Contains(conditionalObservationRewrite, "?") {
		t.Fatalf("conditional observation rewrite placeholders not rewritten: %s", conditionalObservationRewrite)
	}
	if !strings.Contains(conditionalObservationRewrite, "observations_projected_at = updated_at") {
		t.Fatalf("conditional observation rewrite lost per-row witness: %s", conditionalObservationRewrite)
	}
	conditionalSidecarClear := rewriteSQL(`UPDATE script_recordings
		SET correlation_observations_json = '[]',
			correlation_status_version = ?,
			correlation_observation_count = 0,
			correlation_sidecar_version = ?,
			correlation_status_projected_at = updated_at
		WHERE script_id = ?
			AND COALESCE(correlation_observations_json, '') = ?
			AND CAST(updated_at AS TEXT) = ?`, storeDialectPostgres)
	if !strings.Contains(conditionalSidecarClear, "$5") || strings.Contains(conditionalSidecarClear, "?") {
		t.Fatalf("conditional sidecar clear placeholders not rewritten: %s", conditionalSidecarClear)
	}
	sqliteByteLength := scriptRecordingObservationJSONByteLengthSQL(storeDialectSQLite, "observations_json")
	if sqliteByteLength != "LENGTH(CAST(COALESCE(observations_json, '') AS BLOB))" {
		t.Fatalf("SQLite byte-length expression = %s", sqliteByteLength)
	}
	postgresByteLength := scriptRecordingObservationJSONByteLengthSQL(storeDialectPostgres, "observations_json")
	if postgresByteLength != "OCTET_LENGTH(COALESCE(observations_json, ''))" {
		t.Fatalf("PostgreSQL byte-length expression = %s", postgresByteLength)
	}
	projectionPage := rewriteSQL(fmt.Sprintf(`SELECT script_id, %s
		FROM script_recordings
		WHERE script_id > ? AND %s <= ?
			AND COALESCE(observations_projection_version, 0) < ?
		ORDER BY script_id LIMIT ?`, postgresByteLength, postgresByteLength), storeDialectPostgres)
	if !strings.Contains(projectionPage, "$4") || strings.Contains(projectionPage, "?") || !strings.Contains(projectionPage, "ORDER BY script_id") {
		t.Fatalf("projection page query not rewritten deterministically: %s", projectionPage)
	}
	if !strings.Contains(projectionPage, "OCTET_LENGTH") {
		t.Fatalf("projection page query lost byte-accurate bound: %s", projectionPage)
	}

	legacyCount := rewriteSQL(`SELECT COUNT(*) FROM script_recordings
		WHERE SUBSTR(correlation_observations_json, 1, 7) = 'enc:v1:'
			OR SUBSTR(correlation_observations_json, 1, 7) = 'enc:v2:'`, storeDialectPostgres)
	if !strings.Contains(legacyCount, "'enc:v1:'") || !strings.Contains(legacyCount, "'enc:v2:'") {
		t.Fatalf("legacy ciphertext literals mangled by rewrite: %s", legacyCount)
	}

	parentLock := rewriteSQL(`SELECT CAST(updated_at AS TEXT) FROM scripts WHERE id = ? FOR UPDATE`, storeDialectPostgres)
	if !strings.Contains(parentLock, "$1") || !strings.Contains(parentLock, "FOR UPDATE") {
		t.Fatalf("parent lock query not rewritten: %s", parentLock)
	}

	statusQuery := rewriteSQL(scriptRecordingStatusSelectQuery, storeDialectPostgres)
	if !strings.Contains(statusQuery, "$1") || strings.Contains(statusQuery, "?") {
		t.Fatalf("status query placeholders not rewritten: %s", statusQuery)
	}
	if strings.Contains(statusQuery, "observations_json") ||
		strings.Contains(statusQuery, "correlation_observations_json") {
		t.Fatalf("PostgreSQL status query selects payload data: %s", statusQuery)
	}
	statusCAS := rewriteSQL(`UPDATE script_recordings
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
			AND COALESCE(observations_projection_version, 0) = ?
			AND CAST(correlation_status_projected_at AS TEXT) = ?
			AND CAST(observations_projected_at AS TEXT) = ?`, storeDialectPostgres)
	if !strings.Contains(statusCAS, "$13") || strings.Contains(statusCAS, "?") ||
		!strings.Contains(statusCAS, "correlation_status_projected_at = updated_at") {
		t.Fatalf("PostgreSQL status witness CAS = %s", statusCAS)
	}
}

// storeRecording builds a small two-observation baseline for tests.
func storeRecording(scriptID string) *ScriptRecordingRecord {
	return &ScriptRecordingRecord{
		ScriptID: scriptID,
		Source:   "browser_recording",
		Observations: []ScriptRecordingObservation{
			{ID: 0, RequestURL: "/login", ResponseBody: `{"token":"abc"}`, ResponseHeaders: map[string]string{"Content-Type": "application/json"}},
			{ID: 1, RequestURL: "/orders", RequestHeaders: map[string]string{"X-Token": "abc"}},
		},
		CorrelationObservations: []ScriptRecordingObservation{
			{ID: 0, RequestURL: "/login", ResponseBody: `{"token":"raw"}`, ResponseHeaders: map[string]string{"Content-Type": "application/json"}},
			{ID: 1, RequestURL: "/orders", RequestHeaders: map[string]string{"X-Token": "raw"}},
		},
	}
}

func bindRecordingToLiveScript(t *testing.T, store *SQLiteStore, record *ScriptRecordingRecord) {
	t.Helper()
	parent, err := store.GetScript(record.ScriptID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := store.SaveScript(&dsl.Script{ID: record.ScriptID, Name: record.ScriptID, Protocol: "http"}); err != nil {
			t.Fatalf("SaveScript(%s): %v", record.ScriptID, err)
		}
		parent, err = store.GetScript(record.ScriptID)
	}
	if err != nil {
		t.Fatalf("GetScript(%s): %v", record.ScriptID, err)
	}
	record.ProjectID = parent.ProjectID
	record.ScriptIncarnation = ScriptIncarnation(parent.ID, parent.ProjectID, parent.RefKey, parent.CreatedAt)
	record.ScriptUpdatedAt = parent.UpdatedAt
}

func storage_obs(i int) ScriptRecordingObservation {
	return ScriptRecordingObservation{ID: i, RequestURL: "/step"}
}
