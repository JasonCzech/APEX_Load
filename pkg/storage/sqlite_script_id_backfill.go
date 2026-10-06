package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	librarySyncLinksSettingKeyForScriptIDBackfill    = "library.sync_links.v1"
	libraryImportClaimsSettingKeyForScriptIDBackfill = "library.import_claims.v1"
)

// backfillBlankScriptIDs repairs script rows written by older create paths
// before an ID was reserved. SQLite accepts the empty string as a TEXT primary
// key, so the row is durable but cannot be addressed through a REST path.
//
// Updating the parent key in place is unsafe because the declared foreign keys
// do not use ON UPDATE CASCADE. Instead, one transaction inserts an identical
// parent under a deterministic UUID, remaps every relational and known
// serialized reference, then deletes the unreachable parent. The same sequence
// works with immediate SQLite constraints and PostgreSQL's non-deferrable
// compatibility constraints.
func (s *SQLiteStore) backfillBlankScriptIDs() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.db.dialect == storeDialectPostgres {
		// Serialize this parent-key rewrite across HA controller startups.
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(73194721018462)`); err != nil {
			return fmt.Errorf("lock blank script id repair: %w", err)
		}
	}

	// Publication admission takes this fence before project/source locks. Keep
	// repair in the same order, even when the claims row is currently absent.
	if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
		return fmt.Errorf("lock blank script publication repair: %w", err)
	}

	// Do the blank classification in Go. SQL TRIM differs by dialect and only
	// recognizes ASCII space by default, while every script write boundary uses
	// strings.TrimSpace and therefore rejects tabs and Unicode whitespace too.
	rows, err := tx.Query(`SELECT id FROM scripts ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list scripts with blank ids: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	var oldIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan script with blank id: %w", err)
		}
		if strings.TrimSpace(id) == "" {
			oldIDs = append(oldIDs, id)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close blank script id scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list scripts with blank ids: %w", err)
	}

	for _, oldID := range oldIDs {
		if _, err := backfillOneBlankScriptID(tx, oldID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func backfillOneBlankScriptID(tx *storeTx, oldID string) (string, error) {
	// A sync worker keeps using the script identifier it claimed. Re-keying that
	// identifier underneath an unexpired worker would strand finalization after a
	// possible remote mutation, so defer this startup repair until the claim has
	// expired or been released.
	if err := rejectActiveGitHubScriptSyncClaimTx(tx, oldID); err != nil {
		return "", err
	}
	script, err := loadImportSourceRetentionScriptTx(tx, oldID, false)
	if err != nil {
		return "", fmt.Errorf("load script with blank id: %w", err)
	}
	newID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
		"apexload:legacy-blank-script-id:v1",
		oldID,
		normalizeProjectID(script.ProjectID),
		strings.TrimSpace(script.RefKey),
		strings.TrimSpace(script.Name),
		script.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}, "\x00"))).String()
	var collision int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scripts WHERE id = ?`, newID).Scan(&collision); err != nil {
		return "", fmt.Errorf("check repaired script id collision: %w", err)
	}
	if collision != 0 {
		return "", fmt.Errorf("cannot repair blank script id: deterministic replacement %s already exists", newID)
	}

	script.ID = newID
	if err := refreshProtectedSourceFenceFingerprint(script); err != nil {
		return "", fmt.Errorf("refresh repaired script source fence: %w", err)
	}
	encoded, err := marshalScriptForStorage(script)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO scripts (
		id, ref_key, name, protocol, version, project_id, folder_id,
		generated_by, generated_from, metadata_json, goal_config_json, config, data_sources,
		parameters, init, actions, teardown, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		// The old parent still owns the globally unique ref_key until the end of
		// this transaction. Reserve the repaired parent with an empty key, then
		// restore the exact key immediately after deleting the old parent.
		script.ID, "", script.Name, script.Protocol, script.Version,
		normalizeProjectID(script.ProjectID), script.FolderID,
		script.GeneratedBy, script.GeneratedFrom,
		encoded.metadata, encoded.goals, encoded.config, encoded.dataSources, encoded.parameters,
		encoded.init, encoded.actions, encoded.teardown,
		script.CreatedAt, script.UpdatedAt,
	); err != nil {
		return "", fmt.Errorf("insert repaired script %s: %w", newID, err)
	}

	for _, update := range []struct {
		label string
		query string
		args  []any
	}{
		{"scenario script references", `UPDATE scenario_scripts SET script_id = ? WHERE script_id = ?`, []any{newID, oldID}},
		{"source revision references", `UPDATE bruno_source_revisions SET script_id = ? WHERE script_id = ?`, []any{newID, oldID}},
		{"GitHub script sync target claims", `UPDATE github_script_sync_target_claims SET script_id = ? WHERE script_id = ?`, []any{newID, oldID}},
		{"GitHub script sync claims", `UPDATE github_script_sync_claims SET script_id = ? WHERE script_id = ?`, []any{newID, oldID}},
		{"GitHub script sync target owners", `UPDATE github_script_sync_target_owners SET script_id = ? WHERE script_id = ?`, []any{newID, oldID}},
		{"GitHub script links", `UPDATE github_script_links SET script_id = ? WHERE script_id = ?`, []any{newID, oldID}},
		{"GitHub script checks", `UPDATE github_check_links SET script_id = ? WHERE script_id = ? AND LOWER(TRIM(COALESCE(kind, ''))) = 'script'`, []any{newID, oldID}},
		{"script recording references", `UPDATE script_recordings
			SET script_id = ?, project_id = ?, script_incarnation = ?
			WHERE script_id = ?`, []any{
			newID,
			normalizeProjectID(script.ProjectID),
			ScriptIncarnation(newID, normalizeProjectID(script.ProjectID), script.RefKey, script.CreatedAt),
			oldID,
		}},
		{"script audit references", `UPDATE audit_events SET resource_id = ?
			WHERE resource_id = ? AND LOWER(TRIM(COALESCE(resource_type, ''))) = 'script'`, []any{newID, oldID}},
	} {
		if _, err := tx.Exec(update.query, update.args...); err != nil {
			return "", fmt.Errorf("remap %s for repaired script %s: %w", update.label, newID, err)
		}
	}

	if err := rewriteBlankScriptRevisionSnapshots(tx, oldID, newID); err != nil {
		return "", err
	}
	if err := rewriteBlankScriptBatchReceipts(tx, oldID, newID); err != nil {
		return "", err
	}
	if err := rewriteBlankScriptDirectImportReceipts(tx, oldID, newID); err != nil {
		return "", err
	}
	if err := rewriteBlankScriptObjectRepositoryRefs(tx, oldID, newID); err != nil {
		return "", err
	}
	if err := rewriteBlankScriptTestGroups(tx, oldID, newID); err != nil {
		return "", err
	}
	if err := rewriteBlankScriptSettingsRefs(tx, oldID, newID); err != nil {
		return "", err
	}

	if _, err := tx.Exec(`INSERT INTO script_library_assignments(item_id,application_id,revision) SELECT ?,application_id,revision FROM script_library_assignments WHERE item_id=?`, newID, oldID); err != nil {
		return "", fmt.Errorf("copy repaired script placement: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO script_library_assignments_releases(item_id,release_id) SELECT ?,release_id FROM script_library_assignments_releases WHERE item_id=?`, newID, oldID); err != nil {
		return "", fmt.Errorf("copy repaired script releases: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM script_library_assignments WHERE item_id=?`, oldID); err != nil {
		return "", err
	}
	result, err := tx.Exec(`DELETE FROM scripts WHERE id = ?`, oldID)
	if err != nil {
		return "", fmt.Errorf("delete blank-id script after remap: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return "", errors.New("blank-id script disappeared during repair")
	}
	if _, err := tx.Exec(`UPDATE scripts SET ref_key = ? WHERE id = ?`, script.RefKey, newID); err != nil {
		return "", fmt.Errorf("restore repaired script reference key: %w", err)
	}
	return newID, nil
}

func refreshProtectedSourceFenceFingerprint(script *dsl.Script) error {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return nil
	}
	raw, exists := script.Metadata.Import.Preserved[ImportSourceCommitFenceMetadataKey]
	if !exists {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var fence map[string]any
	if err := json.Unmarshal(payload, &fence); err != nil {
		return err
	}
	fingerprint, err := ImportSourceScriptFingerprint(script)
	if err != nil {
		return err
	}
	fence["script_fingerprint"] = fingerprint
	script.Metadata.Import.Preserved[ImportSourceCommitFenceMetadataKey] = fence
	return nil
}

func rewriteBlankScriptRevisionSnapshots(tx *storeTx, oldID, newID string) error {
	rows, err := tx.Query(`SELECT id, canonical_script_json
		FROM bruno_source_revisions WHERE script_id = ?`, newID)
	if err != nil {
		return fmt.Errorf("list repaired script source revisions: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	type revisionSnapshot struct {
		id      string
		payload string
	}
	var snapshots []revisionSnapshot
	for rows.Next() {
		var snapshot revisionSnapshot
		if err := rows.Scan(&snapshot.id, &snapshot.payload); err != nil {
			rows.Close()
			return err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		var canonical dsl.Script
		if err := json.Unmarshal([]byte(snapshot.payload), &canonical); err != nil {
			return fmt.Errorf("decode repaired source revision %s: %w", snapshot.id, err)
		}
		if strings.TrimSpace(canonical.ID) == strings.TrimSpace(oldID) {
			canonical.ID = newID
		}
		if err := refreshProtectedSourceFenceFingerprint(&canonical); err != nil {
			return fmt.Errorf("refresh repaired source revision %s fence: %w", snapshot.id, err)
		}
		payload, err := json.Marshal(&canonical)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE bruno_source_revisions SET canonical_script_json = ? WHERE id = ?`,
			string(payload), snapshot.id); err != nil {
			return fmt.Errorf("update repaired source revision %s: %w", snapshot.id, err)
		}
	}
	return nil
}

func rewriteBlankScriptBatchReceipts(tx *storeTx, oldID, newID string) error {
	rows, err := tx.Query(`SELECT id, script_ids_json, scripts_json FROM bruno_native_import_batches`)
	if err != nil {
		return fmt.Errorf("list import receipts for blank script id repair: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	type receipt struct {
		id        string
		scriptIDs string
		scripts   string
	}
	var receipts []receipt
	for rows.Next() {
		var item receipt
		if err := rows.Scan(&item.id, &item.scriptIDs, &item.scripts); err != nil {
			rows.Close()
			return err
		}
		receipts = append(receipts, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range receipts {
		var ids []string
		if err := json.Unmarshal([]byte(item.scriptIDs), &ids); err != nil {
			return fmt.Errorf("decode import receipt %s script ids: %w", item.id, err)
		}
		changed := false
		for index := range ids {
			if ids[index] == oldID {
				ids[index] = newID
				changed = true
			}
		}
		scripts := make([]*dsl.Script, 0)
		if strings.TrimSpace(item.scripts) != "" && strings.TrimSpace(item.scripts) != "[]" {
			if err := json.Unmarshal([]byte(item.scripts), &scripts); err != nil {
				return fmt.Errorf("decode import receipt %s scripts: %w", item.id, err)
			}
			for _, script := range scripts {
				if script != nil && script.ID == oldID {
					script.ID = newID
					if err := refreshProtectedSourceFenceFingerprint(script); err != nil {
						return fmt.Errorf("refresh import receipt %s script fence: %w", item.id, err)
					}
					changed = true
				}
			}
		}
		if !changed {
			continue
		}
		encodedIDs, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		encodedScripts, err := json.Marshal(scripts)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE bruno_native_import_batches
			SET script_ids_json = ?, scripts_json = ? WHERE id = ?`,
			string(encodedIDs), string(encodedScripts), item.id); err != nil {
			return fmt.Errorf("update import receipt %s for repaired script: %w", item.id, err)
		}
	}
	return nil
}

func rewriteBlankScriptDirectImportReceipts(tx *storeTx, oldID, newID string) error {
	rows, err := tx.Query(`SELECT id, script_ids_json FROM direct_import_receipts`)
	if err != nil {
		return fmt.Errorf("list direct import receipts for blank script id repair: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	type receipt struct {
		id        string
		scriptIDs string
	}
	var receipts []receipt
	for rows.Next() {
		var item receipt
		if err := rows.Scan(&item.id, &item.scriptIDs); err != nil {
			rows.Close()
			return err
		}
		receipts = append(receipts, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range receipts {
		var ids []string
		if err := json.Unmarshal([]byte(item.scriptIDs), &ids); err != nil {
			return fmt.Errorf("decode direct import receipt %s script ids: %w", item.id, err)
		}
		changed := false
		for index := range ids {
			if ids[index] == oldID {
				ids[index] = newID
				changed = true
			}
		}
		if !changed {
			continue
		}
		payload, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE direct_import_receipts SET script_ids_json = ? WHERE id = ?`,
			string(payload),
			item.id,
		); err != nil {
			return fmt.Errorf("update direct import receipt %s for repaired script: %w", item.id, err)
		}
	}
	return nil
}

func rewriteBlankScriptObjectRepositoryRefs(tx *storeTx, oldID, newID string) error {
	rows, err := tx.Query(`SELECT id, objects_json FROM object_repositories`)
	if err != nil {
		return fmt.Errorf("list object repositories for blank script id repair: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	type repository struct {
		id      string
		objects string
	}
	var repositories []repository
	for rows.Next() {
		var item repository
		if err := rows.Scan(&item.id, &item.objects); err != nil {
			rows.Close()
			return err
		}
		repositories = append(repositories, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range repositories {
		var objects []map[string]any
		if err := json.Unmarshal([]byte(item.objects), &objects); err != nil {
			return fmt.Errorf("decode object repository %s: %w", item.id, err)
		}
		changed := false
		for _, object := range objects {
			if sourceID, exists := object["source_script_id"].(string); exists && sourceID == oldID {
				object["source_script_id"] = newID
				changed = true
			}
		}
		if !changed {
			continue
		}
		payload, err := json.Marshal(objects)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE object_repositories SET objects_json = ? WHERE id = ?`,
			string(payload), item.id); err != nil {
			return fmt.Errorf("update object repository %s repaired script reference: %w", item.id, err)
		}
	}
	return nil
}

func rewriteBlankScriptTestGroups(tx *storeTx, oldID, newID string) error {
	rows, err := tx.Query(`SELECT id, COALESCE(groups_json, '') FROM tests`)
	if err != nil {
		return fmt.Errorf("list test snapshots for blank script id repair: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	type testGroups struct {
		id     string
		groups string
	}
	var tests []testGroups
	for rows.Next() {
		var item testGroups
		if err := rows.Scan(&item.id, &item.groups); err != nil {
			rows.Close()
			return err
		}
		tests = append(tests, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range tests {
		if strings.TrimSpace(item.groups) == "" {
			continue
		}
		var groups []map[string]any
		if err := json.Unmarshal([]byte(item.groups), &groups); err != nil {
			return fmt.Errorf("decode test %s groups for blank script id repair: %w", item.id, err)
		}
		changed := false
		for _, group := range groups {
			if replaceExactStringField(group, "script_id", oldID, newID) {
				changed = true
			}
		}
		if !changed {
			continue
		}
		payload, err := json.Marshal(groups)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tests SET groups_json = ? WHERE id = ?`, string(payload), item.id); err != nil {
			return fmt.Errorf("update test %s repaired script reference: %w", item.id, err)
		}
	}
	return nil
}

func rewriteBlankScriptSettingsRefs(tx *storeTx, oldID, newID string) error {
	for _, key := range []string{
		librarySyncLinksSettingKeyForScriptIDBackfill,
		libraryImportClaimsSettingKeyForScriptIDBackfill,
	} {
		var raw string
		if key == libraryImportClaimsSettingKeyForScriptIDBackfill {
			snapshot, err := readLibraryImportPublicationTx(tx)
			if err != nil {
				return fmt.Errorf("load %s for blank script id repair: %w", key, err)
			}
			raw = snapshot.Raw
		} else {
			err := tx.QueryRow(`SELECT COALESCE(value, '') FROM app_settings WHERE key = ?`, key).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("load %s for blank script id repair: %w", key, err)
			}
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if !utf8.ValidString(raw) {
			return fmt.Errorf("decode %s for blank script id repair: invalid UTF-8", key)
		}
		var entries []map[string]any
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return fmt.Errorf("decode %s for blank script id repair: %w", key, err)
		}
		changed := false
		for _, entry := range entries {
			switch key {
			case librarySyncLinksSettingKeyForScriptIDBackfill:
				kind, _ := entry["Kind"].(string)
				if strings.EqualFold(strings.TrimSpace(kind), "script") &&
					replaceExactStringField(entry, "LocalItemID", oldID, newID) {
					changed = true
				}
			case libraryImportClaimsSettingKeyForScriptIDBackfill:
				kind, _ := entry["kind"].(string)
				if strings.EqualFold(strings.TrimSpace(kind), "script") &&
					replaceExactStringField(entry, "local_item_id", oldID, newID) {
					changed = true
				}
				if beforeLink, ok := entry["before_link"].(map[string]any); ok {
					beforeKind, _ := beforeLink["Kind"].(string)
					if strings.EqualFold(strings.TrimSpace(beforeKind), "script") &&
						replaceExactStringField(beforeLink, "LocalItemID", oldID, newID) {
						changed = true
					}
				}
				if beforeScript, ok := entry["before_script"].(map[string]any); ok {
					if replaceExactStringField(beforeScript, "id", oldID, newID) {
						changed = true
					}
					if script, ok := beforeScript["script"].(map[string]any); ok &&
						replaceExactStringField(script, "id", oldID, newID) {
						changed = true
					}
				}
				if beforeScenario, ok := entry["before_scenario"].(map[string]any); ok {
					if scripts, ok := beforeScenario["scripts"].([]any); ok {
						for _, rawScript := range scripts {
							if script, ok := rawScript.(map[string]any); ok &&
								replaceExactStringField(script, "script_id", oldID, newID) {
								changed = true
							}
						}
					}
				}
			}
		}
		if !changed {
			continue
		}
		payload, err := json.Marshal(entries)
		if err != nil {
			return err
		}
		if key == libraryImportClaimsSettingKeyForScriptIDBackfill {
			if err := validateLibraryImportPublicationRaw(string(payload)); err != nil {
				return fmt.Errorf("validate repaired import claims: %w", err)
			}
		}
		if _, err := tx.Exec(`UPDATE app_settings SET value = ? WHERE key = ?`, string(payload), key); err != nil {
			return fmt.Errorf("update %s repaired script references: %w", key, err)
		}
	}
	return nil
}

func replaceExactStringField(object map[string]any, field, oldValue, newValue string) bool {
	if object == nil {
		return false
	}
	value, exists := object[field].(string)
	if !exists || value != oldValue {
		return false
	}
	object[field] = newValue
	return true
}
