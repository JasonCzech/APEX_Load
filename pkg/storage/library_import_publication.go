package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	LibraryImportClaimsSettingKey  = "library.import_claims.v1"
	MaxLibraryImportClaims         = 10_000
	MaxLibraryImportClaimsRawBytes = 64 << 20
	// Reserved separately from legacy blank-ID repair and schema migration locks.
	libraryImportPublicationAdvisoryLock int64 = 73194721018463
)

// LibraryImportPublicationState describes exact settings bytes and row presence.
// Absent and present-empty states intentionally have different authority even
// though their hashes are both SHA-256 of the empty string. This is private
// admission evidence, not a public content revision or executable DSL field.
type LibraryImportPublicationState struct {
	Exists bool
	SHA256 string
}

type LibraryImportPublicationSnapshot struct {
	State LibraryImportPublicationState
	Raw   string
}

// LibraryImportPublicationStore is an optional durable publication capability.
// It is restricted to the import-claims key; unrelated settings keep their
// existing semantics. Expected empty creates only an absent row, including
// when a corrupt legacy row happens to contain an empty value.
type LibraryImportPublicationStore interface {
	GetLibraryImportPublicationSnapshot() (*LibraryImportPublicationSnapshot, error)
	CompareAndSwapLibraryImportClaims(expected, value string) (bool, error)
}

var _ LibraryImportPublicationStore = (*SQLiteStore)(nil)

// Acquire before project, scenario, source, or pool locks. Claims writers and
// blank-ID repair share this fence with future dependency-aware admission.
// SQLite must reserve its writer before any read, including an absent setting.
// The no-op UPDATE takes that writer without fabricating an empty setting row.
func acquireLibraryImportPublicationFenceTx(tx *storeTx) error {
	if tx.dialect == storeDialectPostgres {
		_, err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, libraryImportPublicationAdvisoryLock)
		return err
	}
	_, err := tx.Exec(`UPDATE app_settings SET value=value WHERE key=?`, LibraryImportClaimsSettingKey)
	return err
}

func readLibraryImportPublicationTx(tx *storeTx) (*LibraryImportPublicationSnapshot, error) {
	bytesExpression := `length(CAST(value AS BLOB))`
	if tx.dialect == storeDialectPostgres {
		bytesExpression = `octet_length(value)`
	}
	// The database rejects oversized values before returning their full bytes.
	// A separate length read would permit a concurrent value change in between.
	var raw sql.NullString
	var size int64
	err := tx.QueryRow(`SELECT CASE WHEN `+bytesExpression+` <= ? THEN value ELSE NULL END, `+bytesExpression+` FROM app_settings WHERE key=?`, MaxLibraryImportClaimsRawBytes, LibraryImportClaimsSettingKey).Scan(&raw, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return libraryImportPublicationSnapshot("", false), nil
	}
	if err != nil {
		return nil, err
	}
	if size > MaxLibraryImportClaimsRawBytes || !raw.Valid {
		return nil, fmt.Errorf("library import claims exceed the %d-byte limit", MaxLibraryImportClaimsRawBytes)
	}
	if err := validateLibraryImportPublicationRaw(raw.String); err != nil {
		return nil, err
	}
	return libraryImportPublicationSnapshot(raw.String, true), nil
}

func libraryImportPublicationSnapshot(raw string, exists bool) *LibraryImportPublicationSnapshot {
	hash := sha256.Sum256([]byte(raw))
	return &LibraryImportPublicationSnapshot{
		State: LibraryImportPublicationState{Exists: exists, SHA256: hex.EncodeToString(hash[:])},
		Raw:   raw,
	}
}

// Count entries without retaining another decoded copy of the whole bounded
// settings document. Rich identity/owner/lease validation remains in the API.
func validateLibraryImportPublicationRaw(raw string) error {
	if len(raw) > MaxLibraryImportClaimsRawBytes {
		return fmt.Errorf("library import claims exceed the %d-byte limit", MaxLibraryImportClaimsRawBytes)
	}
	if !utf8.ValidString(raw) {
		return fmt.Errorf("library import claims contain invalid UTF-8")
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode library import claims: %w", err)
	}
	if first != nil {
		if delimiter, ok := first.(json.Delim); !ok || delimiter != '[' {
			return fmt.Errorf("library import claims must be an array or null")
		}
		count := 0
		for decoder.More() {
			count++
			if count > MaxLibraryImportClaims {
				return fmt.Errorf("library import claims exceed the %d-entry limit", MaxLibraryImportClaims)
			}
			var entry json.RawMessage
			if err := decoder.Decode(&entry); err != nil {
				return fmt.Errorf("decode library import claims: %w", err)
			}
		}
		last, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode library import claims: %w", err)
		}
		if last != json.Delim(']') {
			return fmt.Errorf("library import claims have an invalid array terminator")
		}
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("library import claims contain trailing or malformed data")
	}
	return nil
}

func (s *SQLiteStore) GetLibraryImportPublicationSnapshot() (*LibraryImportPublicationSnapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
		return nil, err
	}
	snapshot, err := readLibraryImportPublicationTx(tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *SQLiteStore) CompareAndSwapLibraryImportClaims(expected, value string) (bool, error) {
	if err := validateLibraryImportPublicationRaw(expected); err != nil {
		return false, err
	}
	if err := validateLibraryImportPublicationRaw(value); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
		return false, err
	}
	current, err := readLibraryImportPublicationTx(tx)
	if err != nil {
		return false, err
	}
	if expected == "" {
		if current.State.Exists {
			return false, nil
		}
	} else if !current.State.Exists || current.Raw != expected {
		return false, nil
	}
	var result sql.Result
	if current.State.Exists {
		result, err = tx.Exec(`UPDATE app_settings SET value=?,updated_at=? WHERE key=? AND value=?`, value, time.Now().UTC(), LibraryImportClaimsSettingKey, expected)
	} else {
		result, err = tx.Exec(`INSERT INTO app_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT(key) DO NOTHING`, LibraryImportClaimsSettingKey, value, time.Now().UTC())
	}
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
