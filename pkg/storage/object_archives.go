package storage

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrObjectArchived = errors.New("object is archived; restore it before editing or executing")

// PostgreSQL's timestamp text depends on the connection time zone. Compare the
// stored incarnation as an instant there, while preserving SQLite's exact text.
func archiveIncarnationMatch(dialect storeDialect, incarnation, createdAt string) string {
	if dialect == storeDialectPostgres {
		return "CAST(" + incarnation + " AS TIMESTAMPTZ)=" + createdAt
	}
	return incarnation + "=CAST(" + createdAt + " AS TEXT)"
}

func archiveObjectTable(kind string) (string, error) {
	switch kind {
	case "script":
		return "scripts", nil
	case "scenario":
		return "scenarios", nil
	}
	return "", errors.New("unsupported archive resource type")
}

// Lock the source row before inspecting metadata so a concurrent restore/archive
// and content edit cannot both commit against the same object version.
func ensureObjectActiveTx(tx *storeTx, kind, id string) error {
	table, err := archiveObjectTable(kind)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE `+table+` SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return err
	}
	var count int
	err = tx.QueryRow(`SELECT COUNT(*) FROM object_archive_states a JOIN `+table+` o ON o.id=a.resource_id AND `+archiveIncarnationMatch(tx.dialect, "a.incarnation", "o.created_at")+` WHERE a.resource_type=? AND a.resource_id=? AND a.archived_at IS NOT NULL`, kind, id).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%s %s: %w", kind, id, ErrObjectArchived)
	}
	return nil
}

// SetObjectArchived changes only server-owned lifecycle metadata. Source version
// checks protect stale browser requests and fence simultaneous content writers.
func (s *SQLiteStore) SetObjectArchived(kind, id string, expected time.Time, archived bool) (bool, error) {
	table, err := archiveObjectTable(kind)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE `+table+` SET updated_at=updated_at WHERE id=?`, id); err != nil {
		return false, err
	}
	var at time.Time
	var raw, incarnation string
	if err = tx.QueryRow(`SELECT updated_at,CAST(updated_at AS TEXT),CAST(created_at AS TEXT) FROM `+table+` WHERE id=?`, id).Scan(&at, &raw, &incarnation); err != nil {
		return false, err
	}
	var current, previousRestored sql.NullTime
	err = tx.QueryRow(`SELECT a.archived_at,a.restored_at FROM object_archive_states a JOIN `+table+` o ON o.id=a.resource_id AND `+archiveIncarnationMatch(tx.dialect, "a.incarnation", "o.created_at")+` WHERE a.resource_type=? AND a.resource_id=?`, kind, id).Scan(&current, &previousRestored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if current.Valid == archived {
		return false, nil
	}
	if expected.IsZero() || !at.Equal(expected) {
		return false, errors.New("object changed concurrently; reload before archiving or restoring")
	}
	now := time.Now().UTC()
	var archivedAt any
	var restoredAt any
	if archived {
		archivedAt = now
		if previousRestored.Valid {
			restoredAt = previousRestored.Time
		}
	} else {
		restoredAt = now
	}
	if _, err = tx.Exec(`INSERT INTO object_archive_states(resource_type,resource_id,incarnation,archived_at,restored_at) VALUES(?,?,?,?,?) ON CONFLICT(resource_type,resource_id) DO UPDATE SET incarnation=excluded.incarnation,archived_at=excluded.archived_at,restored_at=excluded.restored_at`, kind, id, incarnation, archivedAt, restoredAt); err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE `+table+` SET updated_at=? WHERE id=? AND CAST(updated_at AS TEXT)=?`, now, id, raw)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, errors.New("object changed concurrently")
	}
	return true, tx.Commit()
}

// ObjectRestoredAfter prevents missed schedule occurrences from being replayed
// after restoration, including when no scheduler observed the archived state.
// The boundary belongs to the source incarnation and survives controller restart.
func (s *SQLiteStore) ObjectRestoredAfter(kind, id string, plannedAt time.Time) (bool, error) {
	table, err := archiveObjectTable(kind)
	if err != nil {
		return false, err
	}
	var count int
	err = s.db.QueryRow(`SELECT COUNT(*) FROM object_archive_states a JOIN `+table+` o ON o.id=a.resource_id AND `+archiveIncarnationMatch(s.db.dialect, "a.incarnation", "o.created_at")+` WHERE a.resource_type=? AND a.resource_id=? AND a.restored_at>=?`, kind, id, plannedAt.UTC()).Scan(&count)
	return count > 0, err
}

func ensureTestSourcesActiveTx(tx *storeTx, test *TestRecord) error {
	if test.ScenarioID != "" {
		if err := ensureObjectActiveTx(tx, "scenario", test.ScenarioID); err != nil {
			return err
		}
	}
	var groups []struct {
		ScriptID string `json:"script_id"`
	}
	raw, err := json.Marshal(test.Groups)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &groups); err != nil {
		return err
	}
	ids := []string{}
	for _, g := range groups {
		if g.ScriptID != "" {
			ids = append(ids, g.ScriptID)
		}
	}
	ids = append(ids, ExecutionSourceScriptIDs(test.MaterializationKey)...)
	sort.Strings(ids)
	for _, id := range ids {
		if err := ensureObjectActiveTx(tx, "script", id); err != nil {
			return err
		}
	}
	return nil
}

// ExecutionSourceScriptIDs recovers the stable library identity retained in a CI
// execution snapshot. Inline CI scripts have no durable library dependency.
func ExecutionSourceScriptIDs(materialization string) []string {
	const prefix = "ci:v1:"
	if !strings.HasPrefix(materialization, prefix) {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(materialization, prefix))
	if err != nil {
		return nil
	}
	var metadata struct {
		ScriptIdentity string `json:"script_identity"`
	}
	if json.Unmarshal(raw, &metadata) != nil || !strings.HasPrefix(metadata.ScriptIdentity, "script_id:") {
		return nil
	}
	id := strings.TrimPrefix(metadata.ScriptIdentity, "script_id:")
	if id == "" {
		return nil
	}
	return []string{id}
}
