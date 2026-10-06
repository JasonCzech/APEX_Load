package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestBlankScriptIDBackfillRemapsDurableReferencesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blank-script.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	now := time.Date(2026, time.July, 28, 12, 30, 0, 123000000, time.UTC)
	legacy := &dsl.Script{
		ID:        "",
		RefKey:    "script/dev/000001/v1",
		Name:      "Legacy blank ID",
		Version:   "1.0",
		Protocol:  "http",
		ProjectID: DefaultProjectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "postman",
			Preserved: map[string]any{
				ImportSourceCommitFenceMetadataKey: map[string]any{
					"script_fingerprint": "legacy-fingerprint",
				},
			},
		}},
		Config:    dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:   []dsl.Action{{Type: "http", Name: "request", Method: "GET", Target: "/health"}},
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}
	encoded, err := marshalScriptForStorage(legacy)
	if err != nil {
		t.Fatalf("encode legacy script: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO scripts (
		id, ref_key, name, protocol, version, project_id, folder_id,
		generated_by, generated_from, metadata_json, config, data_sources,
		parameters, init, actions, teardown, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		legacy.ID, legacy.RefKey, legacy.Name, legacy.Protocol, legacy.Version,
		legacy.ProjectID, legacy.FolderID, legacy.GeneratedBy, legacy.GeneratedFrom,
		encoded.metadata, encoded.config, encoded.dataSources, encoded.parameters,
		encoded.init, encoded.actions, encoded.teardown, legacy.CreatedAt, legacy.UpdatedAt,
	); err != nil {
		t.Fatalf("seed blank script: %v", err)
	}

	if _, err := store.db.Exec(`INSERT INTO scenarios
		(id, ref_key, name, duration, created_at, updated_at)
		VALUES ('scenario-blank-script', 'scenario/dev/000001/v1', 'Scenario', '1m', ?, ?)`,
		now, now); err != nil {
		t.Fatalf("seed scenario: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO scenario_scripts
		(scenario_id, sort_order, name, script_id)
		VALUES ('scenario-blank-script', 0, 'Legacy', '')`); err != nil {
		t.Fatalf("seed scenario script reference: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO tests
		(id, name, status, project_id, total_vusers, duration_ns, start_time, groups_json)
		VALUES ('test-blank-script', 'Queued legacy run', 'QUEUED', ?, 1, ?, ?, ?)`,
		DefaultProjectID, int64(time.Minute), now,
		`[{"name":"Legacy","script_id":"","script_name":"Legacy blank ID","protocol":"http","vusers":1}]`,
	); err != nil {
		t.Fatalf("seed test group snapshot: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO bruno_source_artifacts
		(id, project_id, artifact_kind, backend_key, sha256, size_bytes,
		 source_format, encryption_json, lifecycle_state, created_at)
		VALUES ('blank-raw', ?, 'raw', 'raw/blank', 'blank-raw-sha', 12,
		 'postman', '{}', 'active', ?)`,
		DefaultProjectID, now); err != nil {
		t.Fatalf("seed import artifact: %v", err)
	}
	canonicalJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("encode canonical script: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO bruno_source_revisions
		(id, script_id, project_id, active_flag, raw_artifact_id, sanitized_artifact_id,
		 sanitized_available, source_format, compatibility_version, identity_map_json,
		 import_options_json, security_profile_json, change_summary_json,
		 secret_bindings_json, canonical_script_json, created_at)
		VALUES ('blank-revision', '', ?, 1, 'blank-raw', 'blank-raw', 0,
		 'postman', 'v1', '{}', '{}', '{}', '{}', '{}', ?, ?)`,
		DefaultProjectID, string(canonicalJSON), now); err != nil {
		t.Fatalf("seed source revision: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO bruno_native_import_batches
		(id, project_id, source_profile, receipt_version, idempotency_key,
		 request_digest_profile, request_sha256, raw_artifact_id, sanitized_artifact_id,
		 candidate_ids_json, script_ids_json, scripts_json, created_at)
		VALUES ('blank-batch', ?, 'postman', 1, 'blank-session',
		 'postman-v1', 'blank-request-sha', 'blank-raw', 'blank-raw',
		 '["candidate-1"]', '[""]', ?, ?)`,
		DefaultProjectID, string(mustJSON(t, []*dsl.Script{legacy})), now); err != nil {
		t.Fatalf("seed import receipt: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO direct_import_receipts
		(id, project_id, source_profile, operation_key_hash, request_sha256,
		 state, owner_token, script_ids_json, created_at, updated_at)
		VALUES ('blank-direct-receipt', ?, 'jmeter', 'operation-hash',
		 'request-hash', 'complete', '', '[""]', ?, ?)`,
		DefaultProjectID, now, now); err != nil {
		t.Fatalf("seed direct import receipt: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO github_script_links
		(script_id, ref_key, owner, repo, branch, path, content_hash,
		 remote_blob_sha, remote_commit_sha, synced_at, updated_at)
		VALUES ('', ?, 'owner', 'repo', 'main', 'script.json', 'sha256:local',
		 'remote-blob', 'remote-commit', ?, ?)`,
		legacy.RefKey, now, now); err != nil {
		t.Fatalf("seed GitHub script link: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO github_check_links
		(id, kind, script_id, owner, repo, commit_sha, published_at, updated_at)
		VALUES ('blank-check', 'script', '', 'owner', 'repo', 'sha', ?, ?)`,
		now, now); err != nil {
		t.Fatalf("seed GitHub check link: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO script_recordings
		(script_id, project_id, script_incarnation, schema_version, source,
		 sensitivity, request_count, observations_json, created_at, updated_at)
		VALUES ('', ?, 'legacy-incarnation', ?, 'browser', 'scrubbed', 0, '[]', ?, ?)`,
		DefaultProjectID, scriptRecordingSchemaVersion, now, now); err != nil {
		t.Fatalf("seed recording: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO audit_events
		(id, action, actor_type, actor_subject, project_id, resource_type,
		 resource_id, status, created_at)
		VALUES ('blank-audit', 'script.imported', 'user', 'operator', ?,
		 'script', '', 'success', ?)`,
		DefaultProjectID, now); err != nil {
		t.Fatalf("seed audit reference: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO object_repositories
		(id, project_id, name, objects_json, created_at, updated_at)
		VALUES ('blank-objects', ?, 'Objects',
		 '[{"logical_name":"submit","source_script_id":""}]', ?, ?)`,
		DefaultProjectID, now, now); err != nil {
		t.Fatalf("seed object repository: %v", err)
	}
	links := `[{"RemoteID":"direct","ProjectID":"unassigned","Kind":"script","SourceInstanceID":"source","SourceItemID":"remote-script","LocalItemID":"","RemoteItemID":"remote-script","LocalContentHash":"sha256:local","RemoteContentHash":"sha256:remote"}]`
	claimIdentity := strings.Join(
		[]string{"direct", DefaultProjectID, "script", "source", "remote-script"},
		"\x00",
	)
	claims := string(mustJSON(t, []map[string]any{{
		"identity":           claimIdentity,
		"owner_id":           "claim-owner",
		"sequence":           0,
		"remote_id":          "direct",
		"project_id":         DefaultProjectID,
		"kind":               "script",
		"source_instance_id": "source",
		"source_item_id":     "remote-script",
		"mutation_id":        "mutation-1",
		"local_item_id":      "",
		"intended_hash":      "sha256:intended",
		"before_link": map[string]any{
			"Kind":             "script",
			"LocalItemID":      "",
			"LocalContentHash": "sha256:before",
		},
		"before_script": map[string]any{
			"id":     "",
			"script": legacy,
		},
		"prepared":   true,
		"applied":    true,
		"created_at": now,
		"expires_at": now.Add(5 * time.Minute),
	}}))
	if _, err := store.db.Exec(`INSERT INTO app_settings (key, value, updated_at)
		VALUES (?, ?, ?), (?, ?, ?)`,
		librarySyncLinksSettingKeyForScriptIDBackfill, links, now,
		libraryImportClaimsSettingKeyForScriptIDBackfill, claims, now,
	); err != nil {
		t.Fatalf("seed serialized script references: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close seeded store: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen store with blank script: %v", err)
	}
	defer reopened.Close()

	var repairedID string
	if err := reopened.db.QueryRow(
		`SELECT id FROM scripts WHERE ref_key = ?`,
		legacy.RefKey,
	).Scan(&repairedID); err != nil {
		t.Fatalf("load repaired script id: %v", err)
	}
	if strings.TrimSpace(repairedID) == "" {
		t.Fatal("blank script id was not repaired")
	}
	repaired, err := reopened.GetScript(repairedID)
	if err != nil {
		t.Fatalf("load repaired script: %v", err)
	}
	if repaired.Name != legacy.Name || repaired.Config.BaseURL != legacy.Config.BaseURL ||
		len(repaired.Actions) != 1 || repaired.Actions[0].Target != legacy.Actions[0].Target {
		t.Fatalf("repaired script content changed: %#v", repaired)
	}
	assertScriptSourceFenceMatches(t, repaired)

	for _, reference := range []struct {
		name  string
		query string
		args  []any
	}{
		{"scenario", `SELECT script_id FROM scenario_scripts WHERE scenario_id = ?`, []any{"scenario-blank-script"}},
		{"revision", `SELECT script_id FROM bruno_source_revisions WHERE id = ?`, []any{"blank-revision"}},
		{"GitHub link", `SELECT script_id FROM github_script_links WHERE path = ?`, []any{"script.json"}},
		{"GitHub check", `SELECT script_id FROM github_check_links WHERE id = ?`, []any{"blank-check"}},
		{"recording", `SELECT script_id FROM script_recordings WHERE source = ?`, []any{"browser"}},
		{"audit", `SELECT resource_id FROM audit_events WHERE id = ?`, []any{"blank-audit"}},
	} {
		var got string
		if err := reopened.db.QueryRow(reference.query, reference.args...).Scan(&got); err != nil {
			t.Fatalf("load %s reference: %v", reference.name, err)
		}
		if got != repairedID {
			t.Fatalf("%s reference = %q, want %q", reference.name, got, repairedID)
		}
	}
	var testGroupsJSON string
	if err := reopened.db.QueryRow(
		`SELECT groups_json FROM tests WHERE id = 'test-blank-script'`,
	).Scan(&testGroupsJSON); err != nil {
		t.Fatalf("load repaired test groups: %v", err)
	}
	var testGroups []map[string]any
	if err := json.Unmarshal([]byte(testGroupsJSON), &testGroups); err != nil {
		t.Fatalf("decode repaired test groups: %v", err)
	}
	if len(testGroups) != 1 || testGroups[0]["script_id"] != repairedID {
		t.Fatalf("test group snapshot did not receive repaired id: %#v", testGroups)
	}
	var incarnation string
	if err := reopened.db.QueryRow(
		`SELECT script_incarnation FROM script_recordings WHERE script_id = ?`,
		repairedID,
	).Scan(&incarnation); err != nil {
		t.Fatalf("load repaired recording incarnation: %v", err)
	}
	wantIncarnation := ScriptIncarnation(repairedID, DefaultProjectID, legacy.RefKey, legacy.CreatedAt)
	if incarnation != wantIncarnation {
		t.Fatalf("recording incarnation = %q, want %q", incarnation, wantIncarnation)
	}

	var revisionPayload string
	if err := reopened.db.QueryRow(
		`SELECT canonical_script_json FROM bruno_source_revisions WHERE id = 'blank-revision'`,
	).Scan(&revisionPayload); err != nil {
		t.Fatalf("load canonical source snapshot: %v", err)
	}
	var revisionScript dsl.Script
	if err := json.Unmarshal([]byte(revisionPayload), &revisionScript); err != nil {
		t.Fatalf("decode canonical source snapshot: %v", err)
	}
	if revisionScript.ID != repairedID {
		t.Fatalf("canonical source snapshot id = %q, want %q", revisionScript.ID, repairedID)
	}
	assertScriptSourceFenceMatches(t, &revisionScript)

	var receiptIDsJSON, receiptScriptsJSON string
	if err := reopened.db.QueryRow(
		`SELECT script_ids_json, scripts_json FROM bruno_native_import_batches WHERE id = 'blank-batch'`,
	).Scan(&receiptIDsJSON, &receiptScriptsJSON); err != nil {
		t.Fatalf("load repaired import receipt: %v", err)
	}
	var receiptIDs []string
	if err := json.Unmarshal([]byte(receiptIDsJSON), &receiptIDs); err != nil {
		t.Fatalf("decode repaired receipt ids: %v", err)
	}
	if len(receiptIDs) != 1 || receiptIDs[0] != repairedID {
		t.Fatalf("repaired receipt ids = %#v, want [%q]", receiptIDs, repairedID)
	}
	var receiptScripts []*dsl.Script
	if err := json.Unmarshal([]byte(receiptScriptsJSON), &receiptScripts); err != nil {
		t.Fatalf("decode repaired receipt scripts: %v", err)
	}
	if len(receiptScripts) != 1 || receiptScripts[0] == nil || receiptScripts[0].ID != repairedID {
		t.Fatalf("repaired receipt scripts = %#v", receiptScripts)
	}
	assertScriptSourceFenceMatches(t, receiptScripts[0])

	var directReceiptIDsJSON string
	if err := reopened.db.QueryRow(
		`SELECT script_ids_json FROM direct_import_receipts WHERE id = 'blank-direct-receipt'`,
	).Scan(&directReceiptIDsJSON); err != nil {
		t.Fatalf("load repaired direct import receipt: %v", err)
	}
	var directReceiptIDs []string
	if err := json.Unmarshal([]byte(directReceiptIDsJSON), &directReceiptIDs); err != nil {
		t.Fatalf("decode repaired direct import receipt ids: %v", err)
	}
	if len(directReceiptIDs) != 1 || directReceiptIDs[0] != repairedID {
		t.Fatalf("repaired direct import receipt ids = %#v, want [%q]", directReceiptIDs, repairedID)
	}

	var objectsJSON string
	if err := reopened.db.QueryRow(
		`SELECT objects_json FROM object_repositories WHERE id = 'blank-objects'`,
	).Scan(&objectsJSON); err != nil {
		t.Fatalf("load repaired object repository: %v", err)
	}
	if !strings.Contains(objectsJSON, `"source_script_id":"`+repairedID+`"`) {
		t.Fatalf("object repository did not receive repaired id: %s", objectsJSON)
	}
	for _, setting := range []struct {
		key   string
		field string
	}{
		{librarySyncLinksSettingKeyForScriptIDBackfill, "LocalItemID"},
		{libraryImportClaimsSettingKeyForScriptIDBackfill, "local_item_id"},
	} {
		var value string
		if err := reopened.db.QueryRow(
			`SELECT value FROM app_settings WHERE key = ?`,
			setting.key,
		).Scan(&value); err != nil {
			t.Fatalf("load repaired setting %s: %v", setting.key, err)
		}
		var entries []map[string]any
		if err := json.Unmarshal([]byte(value), &entries); err != nil {
			t.Fatalf("decode repaired setting %s: %v", setting.key, err)
		}
		if len(entries) != 1 || entries[0][setting.field] != repairedID {
			t.Fatalf("setting %s did not receive repaired id: %#v", setting.key, entries)
		}
		switch setting.key {
		case librarySyncLinksSettingKeyForScriptIDBackfill:
			if entries[0]["LocalContentHash"] != "sha256:local" ||
				entries[0]["RemoteContentHash"] != "sha256:remote" {
				t.Fatalf("library link hashes changed during id repair: %#v", entries[0])
			}
		case libraryImportClaimsSettingKeyForScriptIDBackfill:
			if entries[0]["intended_hash"] != "sha256:intended" {
				t.Fatalf("library claim hash changed during id repair: %#v", entries[0])
			}
			beforeLink, _ := entries[0]["before_link"].(map[string]any)
			if beforeLink["LocalItemID"] != repairedID ||
				beforeLink["LocalContentHash"] != "sha256:before" {
				t.Fatalf("library claim before-link was not safely repaired: %#v", beforeLink)
			}
			beforeScript, _ := entries[0]["before_script"].(map[string]any)
			if beforeScript["id"] != repairedID {
				t.Fatalf("library claim before-script id was not repaired: %#v", beforeScript)
			}
			scriptSnapshot, _ := beforeScript["script"].(map[string]any)
			if scriptSnapshot["id"] != repairedID {
				t.Fatalf("library claim nested script id was not repaired: %#v", scriptSnapshot)
			}
		}
	}

	var blankRows int
	if err := reopened.db.QueryRow(
		`SELECT COUNT(*) FROM scripts WHERE TRIM(COALESCE(id, '')) = ''`,
	).Scan(&blankRows); err != nil {
		t.Fatalf("count blank scripts: %v", err)
	}
	if blankRows != 0 {
		t.Fatalf("blank script rows after repair = %d", blankRows)
	}
	var foreignKeyFailures int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyFailures); err != nil {
		t.Fatalf("check repaired foreign keys: %v", err)
	}
	if foreignKeyFailures != 0 {
		t.Fatalf("blank id repair left %d foreign-key failures", foreignKeyFailures)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return payload
}

func assertScriptSourceFenceMatches(t *testing.T, script *dsl.Script) {
	t.Helper()
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatalf("script has no source fence metadata: %#v", script)
	}
	rawFence, ok := script.Metadata.Import.Preserved[ImportSourceCommitFenceMetadataKey]
	if !ok {
		t.Fatalf("script has no %s: %#v", ImportSourceCommitFenceMetadataKey, script.Metadata)
	}
	payload := mustJSON(t, rawFence)
	var fence map[string]any
	if err := json.Unmarshal(payload, &fence); err != nil {
		t.Fatalf("decode source fence: %v", err)
	}
	got, _ := fence["script_fingerprint"].(string)
	want, err := ImportSourceScriptFingerprint(script)
	if err != nil {
		t.Fatalf("fingerprint repaired script: %v", err)
	}
	if got != want {
		t.Fatalf("source fence fingerprint = %q, want %q", got, want)
	}
}
