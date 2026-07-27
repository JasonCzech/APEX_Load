package storage

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestImportBatchSchemaMigratesLegacyTableInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-import-batch.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	_, err = legacy.Exec(`CREATE TABLE import_batches (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		request_sha256 TEXT NOT NULL,
		raw_artifact_id TEXT NOT NULL,
		sanitized_artifact_id TEXT NOT NULL,
		candidate_ids_json TEXT NOT NULL,
		script_ids_json TEXT NOT NULL,
		created_by TEXT,
		created_at DATETIME NOT NULL,
		UNIQUE(project_id, idempotency_key)
	)`)
	closeErr := legacy.Close()
	if err := errors.Join(err, closeErr); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("migrate legacy store: %v", err)
	}
	defer store.Close()
	_, err = store.db.Exec(`INSERT INTO import_batches (
		id, project_id, idempotency_key, request_sha256,
		raw_artifact_id, sanitized_artifact_id,
		candidate_ids_json, script_ids_json, created_by, created_at
	) VALUES (?, ?, ?, ?, ?, ?, '[]', '[]', ?, ?)`,
		"legacy-row", "legacy-project", "legacy-session", "legacy-sha",
		"legacy-raw", "legacy-sanitized", "legacy-actor", time.Now().UTC())
	if err != nil {
		t.Fatalf("insert through migrated legacy table: %v", err)
	}
	var profile, digestProfile, scriptsJSON string
	var receiptVersion int
	if err := store.db.QueryRow(`SELECT source_profile, receipt_version,
		request_digest_profile, scripts_json
		FROM import_batches WHERE id = ?`, "legacy-row").Scan(
		&profile, &receiptVersion, &digestProfile, &scriptsJSON,
	); err != nil {
		t.Fatalf("read migrated defaults: %v", err)
	}
	if profile != ImportBatchProfileBrunoNative ||
		receiptVersion != ImportBatchReceiptVersion1 ||
		digestProfile != ImportBatchDigestBrunoNativeCommitV1 ||
		scriptsJSON != "[]" {
		t.Fatalf(
			"migrated defaults = profile=%q version=%d digest=%q scripts=%q",
			profile, receiptVersion, digestProfile, scriptsJSON,
		)
	}
}

func TestGenericImportBatchAndBrunoCompatibilityReadsAreBidirectional(t *testing.T) {
	t.Run("generic commit is readable through Bruno API", func(t *testing.T) {
		store := newBrunoImportTestStore(t)
		commit := genericImportBatchFixture(
			"postman_native", "postman-native-commit-v1",
			"generic-session-1", "generic-request-sha-1",
		)
		// The generic transaction is protocol-neutral; the importer adapter
		// owns protocol eligibility.
		commit.Scripts[0].Protocol = "websocket"

		created, err := store.CommitImportBatch(commit)
		if err != nil {
			t.Fatalf("CommitImportBatch: %v", err)
		}
		if !created.Created || created.Receipt == nil ||
			created.Receipt.Profile != "postman_native" ||
			created.Receipt.RequestDigestProfile != "postman-native-commit-v1" {
			t.Fatalf("generic result = %+v", created)
		}
		if got := created.Receipt.CandidateIDs; len(got) != 2 ||
			got[0] != "candidate-1" || got[1] != "candidate-2" {
			t.Fatalf("candidate order = %#v", got)
		}
		if _, err := store.GetBrunoNativeImportByIdempotencyKey(
			"generic-session-1", "project-bruno",
		); err == nil {
			t.Fatal("Bruno compatibility lookup exposed another import profile")
		}
	})

	t.Run("Bruno commit is readable through generic API", func(t *testing.T) {
		store := newBrunoImportTestStore(t)
		created, err := store.CommitBrunoNativeImport(
			brunoNativeImportCommitFixture("native-session-generic-read", "native-request-generic-read"),
		)
		if err != nil {
			t.Fatalf("CommitBrunoNativeImport: %v", err)
		}
		generic, err := store.GetImportBatchByIdempotencyKey(
			"project-bruno", ImportBatchProfileBrunoNative, "native-session-generic-read",
		)
		if err != nil {
			t.Fatalf("GetImportBatchByIdempotencyKey: %v", err)
		}
		if generic.Receipt.ID != created.Batch.ID ||
			generic.Receipt.Profile != ImportBatchProfileBrunoNative ||
			generic.Receipt.ReceiptVersion != ImportBatchReceiptVersion1 ||
			generic.Receipt.RequestDigestProfile != ImportBatchDigestBrunoNativeCommitV1 {
			t.Fatalf("generic Bruno receipt = %+v", generic.Receipt)
		}
		if len(generic.Scripts) != len(created.Scripts) ||
			generic.Scripts[0].ID != created.Scripts[0].ID {
			t.Fatalf("generic scripts = %+v, Bruno scripts = %+v", generic.Scripts, created.Scripts)
		}
	})
}

func TestCommitImportBatchRollbackDoesNotMutateCallerAndSameObjectRetries(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"generic-session-refkey-retry",
		"generic-request-refkey-retry",
	)
	commit.Receipt.ID = ""
	commit.Receipt.ScriptIDs = nil
	commit.RefKeyEnv = "local"
	commit.RawArtifact.ID = ""
	commit.SanitizedArtifact.ID = ""
	for _, script := range commit.Scripts {
		script.RefKey = ""
	}
	missingFolder := "missing-folder"
	commit.Scripts[1].FolderID = &missingFolder

	beforeFailure, err := json.Marshal(commit)
	if err != nil {
		t.Fatalf("marshal commit before failure: %v", err)
	}
	if _, err := store.CommitImportBatch(commit); err == nil {
		t.Fatal("CommitImportBatch unexpectedly succeeded with a missing folder")
	}
	afterFailure, err := json.Marshal(commit)
	if err != nil {
		t.Fatalf("marshal commit after failure: %v", err)
	}
	if string(afterFailure) != string(beforeFailure) {
		t.Fatalf("failed commit mutated caller\nbefore: %s\nafter:  %s", beforeFailure, afterFailure)
	}
	if sequence, err := store.GetSetting("refseq:LOC:SCR"); err != nil || sequence != "" {
		t.Fatalf("rolled-back reference sequence = %q, err=%v; want empty", sequence, err)
	}

	validFolder := "native-folder"
	commit.Scripts[1].FolderID = &validFolder
	result, err := store.CommitImportBatch(commit)
	if err != nil {
		t.Fatalf("retry same CommitImportBatch object: %v", err)
	}
	if result == nil || !result.Created || result.Receipt == nil {
		t.Fatalf("retry result = %+v, want created receipt", result)
	}
	wantRefKeys := []string{"LOC-SCR-0001-v1", "LOC-SCR-0002-v1"}
	for index, want := range wantRefKeys {
		if got := result.Scripts[index].RefKey; got != want {
			t.Fatalf("result script %d reference key = %q, want %q", index+1, got, want)
		}
		if got := commit.Scripts[index].RefKey; got != want {
			t.Fatalf("caller script %d reference key = %q, want published %q", index+1, got, want)
		}
		if commit.Scripts[index].CreatedAt.IsZero() || commit.Scripts[index].UpdatedAt.IsZero() {
			t.Fatalf("caller script %d timestamps were not published after success", index+1)
		}
	}
	if commit.Receipt.ID != result.Receipt.ID ||
		commit.Receipt.RawArtifactID != result.Receipt.RawArtifactID ||
		commit.Receipt.SanitizedArtifactID != result.Receipt.SanitizedArtifactID ||
		len(commit.Receipt.ScriptIDs) != len(result.Receipt.ScriptIDs) {
		t.Fatalf("caller receipt = %+v, result receipt = %+v", commit.Receipt, result.Receipt)
	}
	if commit.RawArtifact.ID == "" || commit.SanitizedArtifact.ID == "" ||
		commit.RawArtifact.ProjectID != commit.Receipt.ProjectID ||
		commit.SanitizedArtifact.ProjectID != commit.Receipt.ProjectID {
		t.Fatalf(
			"successful artifact normalization was not published: raw=%+v sanitized=%+v",
			commit.RawArtifact,
			commit.SanitizedArtifact,
		)
	}
	if sequence, err := store.GetSetting("refseq:LOC:SCR"); err != nil || sequence != "2" {
		t.Fatalf("committed reference sequence = %q, err=%v; want 2", sequence, err)
	}
}

func TestGenericImportBatchReadsLegacySchemaDefaults(t *testing.T) {
	store := newBrunoImportTestStore(t)
	created, err := store.CommitBrunoNativeImport(
		brunoNativeImportCommitFixture("native-session-default-source", "native-request-default-source"),
	)
	if err != nil {
		t.Fatalf("seed Bruno batch: %v", err)
	}

	// Simulate an older controller that omits every generic receipt column.
	_, err = store.db.Exec(`INSERT INTO import_batches (
		id, project_id, idempotency_key, request_sha256, raw_artifact_id, sanitized_artifact_id,
		candidate_ids_json, script_ids_json, scripts_json, created_by, created_at
	)
	SELECT ?, project_id, ?, request_sha256, raw_artifact_id, sanitized_artifact_id,
		candidate_ids_json, script_ids_json, '[]', created_by, created_at
	FROM import_batches WHERE id = ?`,
		"legacy-default-batch", "legacy-default-session", created.Batch.ID)
	if err != nil {
		t.Fatalf("insert legacy-shaped receipt: %v", err)
	}

	legacy, err := store.GetImportBatchByIdempotencyKey(
		"project-bruno", ImportBatchProfileBrunoNative, "legacy-default-session",
	)
	if err != nil {
		t.Fatalf("read legacy-shaped receipt: %v", err)
	}
	if legacy.Receipt.ID != "legacy-default-batch" ||
		legacy.Receipt.Profile != ImportBatchProfileBrunoNative ||
		legacy.Receipt.ReceiptVersion != ImportBatchReceiptVersion1 ||
		legacy.Receipt.RequestDigestProfile != ImportBatchDigestBrunoNativeCommitV1 {
		t.Fatalf("legacy defaults = %+v", legacy.Receipt)
	}
	if len(legacy.Scripts) != 2 ||
		legacy.Scripts[0].ID != legacy.Receipt.ScriptIDs[0] ||
		legacy.Scripts[1].ID != legacy.Receipt.ScriptIDs[1] {
		t.Fatalf("legacy live-script fallback = %+v", legacy.Scripts)
	}
}

func TestConcurrentGenericImportBatchCommitHasOneDurableWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generic-import-concurrency.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	defer second.Close()

	now := time.Now().UTC()
	if err := first.SaveProject(&Project{
		ID: "project-bruno", Name: "Generic imports", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := first.SaveFolder(&Folder{
		ID: "native-folder", ProjectID: "project-bruno", Name: "Imports",
		Category: "scripts", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save folder: %v", err)
	}

	stores := []*SQLiteStore{first, second}
	results := make(chan *ImportBatchCommitResult, len(stores))
	errs := make(chan error, len(stores))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, store := range stores {
		store := store
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, commitErr := store.CommitImportBatch(genericImportBatchFixture(
				"postman_native", "postman-native-commit-v1",
				"concurrent-generic-session", "concurrent-generic-sha",
			))
			results <- result
			errs <- commitErr
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errs)

	createdCount := 0
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent CommitImportBatch: %v", err)
		}
	}
	for result := range results {
		if result == nil || result.Receipt == nil || len(result.Scripts) != 2 {
			t.Fatalf("concurrent result = %+v", result)
		}
		if result.Created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created result count = %d, want 1", createdCount)
	}

	var receiptCount, scriptCount int
	if err := first.db.QueryRow(
		`SELECT COUNT(*) FROM import_batches WHERE project_id = ? AND idempotency_key = ?`,
		"project-bruno", "concurrent-generic-session",
	).Scan(&receiptCount); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if err := first.db.QueryRow(
		`SELECT COUNT(*) FROM scripts WHERE project_id = ? AND id IN (?, ?)`,
		"project-bruno", "native-script-1", "native-script-2",
	).Scan(&scriptCount); err != nil {
		t.Fatalf("count scripts: %v", err)
	}
	if receiptCount != 1 || scriptCount != 2 {
		t.Fatalf("durable receipt/scripts = %d/%d, want 1/2", receiptCount, scriptCount)
	}
}

func TestGenericImportBatchConflictDoesNotChangeBrunoSentinel(t *testing.T) {
	store := newBrunoImportTestStore(t)
	if _, err := store.CommitImportBatch(genericImportBatchFixture(
		"postman_native", "postman-native-commit-v1", "generic-conflict", "sha-1",
	)); err != nil {
		t.Fatalf("first generic commit: %v", err)
	}
	if _, err := store.CommitImportBatch(genericImportBatchFixture(
		"postman_native", "postman-native-commit-v1", "generic-conflict", "sha-2",
	)); !errors.Is(err, ErrImportBatchConflict) {
		t.Fatalf("generic conflict = %v, want ErrImportBatchConflict", err)
	}
}

func TestGenericImportBatchRejectsMismatchedReceiptAndArtifactScope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ImportBatchCommit)
	}{
		{
			name: "receipt script id",
			mutate: func(commit *ImportBatchCommit) {
				commit.Receipt.ScriptIDs = []string{"wrong-script-1", "native-script-2"}
			},
		},
		{
			name: "receipt script count",
			mutate: func(commit *ImportBatchCommit) {
				commit.Receipt.ScriptIDs = []string{"native-script-1"}
			},
		},
		{
			name: "raw artifact project",
			mutate: func(commit *ImportBatchCommit) {
				commit.RawArtifact.ProjectID = "another-project"
			},
		},
		{
			name: "sanitized artifact kind",
			mutate: func(commit *ImportBatchCommit) {
				commit.SanitizedArtifact.ArtifactKind = "raw"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native", "postman-native-commit-v1",
				"scope-"+strings.ReplaceAll(test.name, " ", "-"), "scope-sha",
			)
			test.mutate(commit)
			if _, err := store.CommitImportBatch(commit); err == nil {
				t.Fatal("CommitImportBatch unexpectedly accepted mismatched scope")
			}
			var receipts, scripts int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM import_batches`).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM scripts`).Scan(&scripts); err != nil {
				t.Fatal(err)
			}
			if receipts != 0 || scripts != 0 {
				t.Fatalf("failed validation persisted receipts/scripts = %d/%d", receipts, scripts)
			}
		})
	}
}

func TestGenericImportBatchRejectsUnmanagedCredentialsBeforePersistence(t *testing.T) {
	tests := []struct {
		name   string
		canary string
		mutate func(*dsl.Script, string)
	}{
		{
			name:   "ordered authorization header",
			canary: "batch-header-canary-9d4f",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].HeaderList = dsl.HeaderList{{
					Name:  "Authorization",
					Value: "Bearer " + canary,
				}}
			},
		},
		{
			name:   "legacy authorization header map",
			canary: "batch-header-map-canary-7c2a",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].Headers = map[string]string{
					"Authorization": "Bearer " + canary,
				}
			},
		},
		{
			name:   "typed auth password",
			canary: "batch-auth-password-canary-5e81",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].Auth = &dsl.AuthConfig{
					Type:     "basic",
					Username: "import-user",
					Password: canary,
				}
			},
		},
		{
			name:   "structured JSON body password",
			canary: "batch-body-canary-18bf",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].Headers = map[string]string{
					"Content-Type": "application/json",
				}
				script.Actions[0].Body = `{"username":"import-user","password":"` + canary + `"}`
			},
		},
		{
			name:   "wire-visible action metadata",
			canary: "batch-action-metadata-canary-4fd3",
			mutate: func(script *dsl.Script, canary string) {
				script.Protocol = "jms"
				script.Actions[0].Metadata = map[string]any{
					"authorization": "Bearer " + canary,
				}
			},
		},
		{
			name:   "semantic metadata name value pair",
			canary: "batch-action-metadata-pair-canary-b71a",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].Metadata = map[string]any{
					"name":  "password",
					"value": canary,
				}
			},
		},
		{
			name:   "nested metadata credential container",
			canary: "batch-action-metadata-container-canary-593e",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].Metadata = map[string]any{
					"credentials": []any{canary},
				}
			},
		},
		{
			name:   "credential assignment under neutral metadata key",
			canary: "batch-action-metadata-assignment-canary-124e",
			mutate: func(script *dsl.Script, canary string) {
				script.Protocol = "kafka"
				script.Actions[0].Metadata = map[string]any{
					"config": "password=" + canary,
				}
			},
		},
		{
			name:   "auth scheme under neutral metadata key",
			canary: "batch-action-metadata-bearer-canary-6da3",
			mutate: func(script *dsl.Script, canary string) {
				script.Protocol = "jms"
				script.Actions[0].Metadata = map[string]any{
					"config": "Bearer " + canary,
				}
			},
		},
		{
			name:   "credential assignment under neutral params key",
			canary: "batch-action-params-assignment-canary-b8c1",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].Params = map[string]any{
					"config": "password=" + canary,
				}
			},
		},
		{
			name:   "auth scheme under neutral params key",
			canary: "batch-action-params-bearer-canary-87c0",
			mutate: func(script *dsl.Script, canary string) {
				script.Actions[0].Params = map[string]any{
					"config": "Bearer " + canary,
				}
			},
		},
		{
			name:   "script review metadata",
			canary: "batch-script-metadata-canary-e052",
			mutate: func(script *dsl.Script, canary string) {
				script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
					Source: "fixture",
					Preserved: map[string]any{
						"source_review": map[string]any{
							"authorization": "Bearer " + canary,
						},
					},
				}}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"credential-"+strings.ReplaceAll(test.name, " ", "-"),
				"credential-boundary-sha",
			)
			test.mutate(commit.Scripts[0], test.canary)

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			if strings.Contains(err.Error(), test.canary) {
				t.Fatalf("credential boundary disclosed rejected value in error: %v", err)
			}

			for _, table := range []string{
				"import_batches",
				"scripts",
				"import_source_artifacts",
			} {
				var count int
				if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
					t.Fatalf("count %s: %v", table, err)
				}
				if count != 0 {
					t.Fatalf("failed credential validation persisted %d row(s) in %s", count, table)
				}
			}
		})
	}
}

func TestGenericImportBatchAcceptsDisclosureSafeCredentialMetadata(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"safe-credential-metadata-session",
		"safe-credential-metadata-sha",
	)
	commit.Scripts[0].Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source: "fixture",
		Preserved: map[string]any{
			"source_review": map[string]any{
				"authorization": "Bearer [REDACTED]",
			},
		},
	}}
	commit.Scripts[0].Actions[0].Metadata = map[string]any{
		"source_secret_ref":    "lr_unmask_login",
		"body_gated_by_secret": true,
		"config":               "password={{secret.CONFIG_PASSWORD}}",
		"authorization_config": "Bearer {{secret.API_TOKEN}}",
		"source_review": map[string]any{
			"name":  "password",
			"value": "[REDACTED]",
		},
	}
	commit.Scripts[0].Actions[0].Params = map[string]any{
		"config": "password={{secret.CONFIG_PASSWORD}}",
	}

	if _, err := store.CommitImportBatch(commit); err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
}

func TestGenericImportBatchAcceptsGovernedSecretTemplates(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"governed-credential-session",
		"governed-credential-sha",
	)
	action := &commit.Scripts[0].Actions[0]
	action.HeaderList = dsl.HeaderList{{
		Name:  "Authorization",
		Value: "Bearer {{secret.API_TOKEN}}",
	}}
	action.Headers = map[string]string{
		"Authorization": "Bearer {{secret.API_TOKEN}}",
		"Content-Type":  "application/json",
	}
	action.Auth = &dsl.AuthConfig{
		Type:     "basic",
		Username: "import-user",
		Password: "{{secret.LOGIN_PASSWORD}}",
	}
	action.Body = `{"username":"import-user","password":"{{secret.LOGIN_PASSWORD}}"}`

	result, err := store.CommitImportBatch(commit)
	if err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	if result == nil || !result.Created {
		t.Fatalf("CommitImportBatch result = %+v, want created", result)
	}
	stored, err := store.GetScript(commit.Scripts[0].ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	if got := stored.Actions[0].HeaderList[0].Value; got != "Bearer {{secret.API_TOKEN}}" {
		t.Fatalf("stored Authorization = %q", got)
	}
	if got := stored.Actions[0].Auth.Password; got != "{{secret.LOGIN_PASSWORD}}" {
		t.Fatalf("stored auth password = %q", got)
	}
	if got := stored.Actions[0].Body; got != `{"username":"import-user","password":"{{secret.LOGIN_PASSWORD}}"}` {
		t.Fatalf("stored body = %q", got)
	}
}

func TestGenericImportBatchRejectsCredentialNestedInNeutralMultipartPart(t *testing.T) {
	const canary = "batch-neutral-multipart-canary-f3a8"
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"neutral-multipart-session",
		"neutral-multipart-sha",
	)
	action := &commit.Scripts[0].Actions[0]
	action.Headers = map[string]string{
		"Content-Type": "multipart/form-data; boundary=neutral-boundary",
	}
	action.Body = "--neutral-boundary\r\n" +
		"Content-Disposition: form-data; name=\"payload\"\r\n" +
		"Content-Type: application/json\r\n\r\n" +
		`{"password":"` + canary + `"}` + "\r\n" +
		"--neutral-boundary--\r\n"

	_, err := store.CommitImportBatch(commit)
	if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
		t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("credential boundary disclosed nested multipart value: %v", err)
	}
	assertGenericImportBatchTablesEmpty(t, store)
}

func TestGenericImportBatchRejectsBase64StructuredCredentialBodiesBeforeWrites(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		canary      string
		wireBody    func(string) string
	}{
		{
			name:   "JSON with omitted media type",
			canary: "batch-base64-json-canary-1fc8",
			wireBody: func(canary string) string {
				return `{"username":"import-user","password":"` + canary + `"}`
			},
		},
		{
			name:        "XML with misleading media type",
			contentType: "text/plain",
			canary:      "batch-base64-xml-canary-2ad7",
			wireBody: func(canary string) string {
				return `<request><password>` + canary + `</password></request>`
			},
		},
		{
			name:        "form with misleading media type",
			contentType: "application/octet-stream",
			canary:      "batch-base64-form-canary-3be6",
			wireBody: func(canary string) string {
				return `username=import-user&password=` + canary
			},
		},
		{
			name:        "multipart with misleading media type",
			contentType: "application/json",
			canary:      "batch-base64-multipart-canary-4cd5",
			wireBody: func(canary string) string {
				return "--base64-boundary\r\n" +
					"Content-Disposition: form-data; name=\"password\"\r\n\r\n" +
					canary + "\r\n--base64-boundary--\r\n"
			},
		},
		{
			name:        "GraphQL with misleading media type",
			contentType: "text/plain",
			canary:      "batch-base64-graphql-canary-5dc4",
			wireBody: func(canary string) string {
				return `mutation Login { login(password: "` + canary + `") { id } }`
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"base64-"+strings.ReplaceAll(strings.ToLower(test.name), " ", "-"),
				"base64-structured-credential-sha",
			)
			action := &commit.Scripts[0].Actions[0]
			wireBody := test.wireBody(test.canary)
			action.Body = base64.StdEncoding.EncodeToString([]byte(wireBody))
			action.BodyEncoding = "base64"
			if test.contentType != "" {
				action.HeaderList = dsl.HeaderList{{
					Name: "Content-Type", Value: test.contentType,
				}}
			}

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			if strings.Contains(err.Error(), test.canary) {
				t.Fatalf("credential boundary disclosed base64 wire value: %v", err)
			}
			assertGenericImportBatchTablesEmpty(t, store)
		})
	}
}

func TestGenericImportBatchAcceptsGovernedAndNonsensitiveBase64Bodies(t *testing.T) {
	t.Run("governed JSON", func(t *testing.T) {
		store := newBrunoImportTestStore(t)
		commit := genericImportBatchFixture(
			"postman_native",
			"postman-native-commit-v1",
			"governed-base64-body-session",
			"governed-base64-body-sha",
		)
		wireBody := `{"password":"{{secret.LOGIN_PASSWORD}}","note":"unchanged"}`
		encoded := base64.StdEncoding.EncodeToString([]byte(wireBody))
		commit.Scripts[0].Actions[0].Body = encoded
		commit.Scripts[0].Actions[0].BodyEncoding = "base64"

		if _, err := store.CommitImportBatch(commit); err != nil {
			t.Fatalf("CommitImportBatch: %v", err)
		}
		stored, err := store.GetScript(commit.Scripts[0].ID)
		if err != nil {
			t.Fatalf("GetScript: %v", err)
		}
		if stored.Actions[0].Body != encoded || stored.Actions[0].BodyEncoding != "base64" {
			t.Fatalf("stored body = %q (%q), want exact base64 representation", stored.Actions[0].Body, stored.Actions[0].BodyEncoding)
		}
	})

	t.Run("nonsensitive binary", func(t *testing.T) {
		store := newBrunoImportTestStore(t)
		commit := genericImportBatchFixture(
			"postman_native",
			"postman-native-commit-v1",
			"nonsensitive-base64-body-session",
			"nonsensitive-base64-body-sha",
		)
		wireBody := []byte{0x00, 0xff, 0x80, 'A', 'P', 'E', 'X'}
		encoded := base64.StdEncoding.EncodeToString(wireBody)
		commit.Scripts[0].Actions[0].Body = encoded
		commit.Scripts[0].Actions[0].BodyEncoding = "base64"

		if _, err := store.CommitImportBatch(commit); err != nil {
			t.Fatalf("CommitImportBatch: %v", err)
		}
		stored, err := store.GetScript(commit.Scripts[0].ID)
		if err != nil {
			t.Fatalf("GetScript: %v", err)
		}
		if stored.Actions[0].Body != encoded || stored.Actions[0].BodyEncoding != "base64" {
			t.Fatalf("stored binary body changed: %q (%q)", stored.Actions[0].Body, stored.Actions[0].BodyEncoding)
		}
	})
}

func TestGenericImportBatchRejectsInvalidBase64BodyBeforeWrites(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid syntax", body: "not base64!"},
		{
			name: "decoded inspection limit",
			body: strings.Repeat(
				"A",
				base64.StdEncoding.EncodedLen(credentialpolicy.DefaultLimits().MaxBytes)+4,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"invalid-base64-"+strings.ReplaceAll(test.name, " ", "-"),
				"invalid-base64-body-sha",
			)
			commit.Scripts[0].Actions[0].Body = test.body
			commit.Scripts[0].Actions[0].BodyEncoding = "base64"

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			assertGenericImportBatchTablesEmpty(t, store)
		})
	}
}

func TestGenericImportBatchRejectsStaticCredentialBindingsAndRootCanaries(t *testing.T) {
	t.Run("innocuous parameter backed by inline CSV", func(t *testing.T) {
		const canary = "batch-static-data-canary-4a2d"
		store := newBrunoImportTestStore(t)
		commit := genericImportBatchFixture(
			"postman_native",
			"postman-native-commit-v1",
			"static-parameter-credential-session",
			"static-parameter-credential-sha",
		)
		script := commit.Scripts[0]
		script.Parameters = []dsl.Parameter{{
			Name:       "authValue",
			Type:       "file",
			DataSource: "rows",
			Column:     "value",
		}}
		script.DataSources = []dsl.DataSource{{
			Name:           "rows",
			Type:           "csv",
			InlineData:     "value\n" + canary + "\n",
			FirstRowHeader: true,
			Strategy:       "sequential",
		}}
		script.Actions[0].HeaderList = dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer {{var.authValue}}",
		}}

		_, err := store.CommitImportBatch(commit)
		if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
			t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
		}
		if strings.Contains(err.Error(), canary) {
			t.Fatalf("credential boundary disclosed inline source value: %v", err)
		}
		assertGenericImportBatchTablesEmpty(t, store)
	})

	t.Run("sensitive inline CSV column", func(t *testing.T) {
		const canary = "batch-inline-password-canary-82fc"
		store := newBrunoImportTestStore(t)
		commit := genericImportBatchFixture(
			"postman_native",
			"postman-native-commit-v1",
			"sensitive-inline-column-session",
			"sensitive-inline-column-sha",
		)
		commit.Scripts[0].Parameters = []dsl.Parameter{{
			Name:   "region",
			Type:   "list",
			Values: []string{"east"},
		}}
		commit.Scripts[0].DataSources = []dsl.DataSource{{
			Name:           "ordinary_rows",
			Type:           "csv",
			InlineData:     "region,password\neast," + canary + "\n",
			FirstRowHeader: true,
			Strategy:       "sequential",
		}}

		_, err := store.CommitImportBatch(commit)
		if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
			t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
		}
		if strings.Contains(err.Error(), canary) {
			t.Fatalf("credential boundary disclosed inline source value: %v", err)
		}
		assertGenericImportBatchTablesEmpty(t, store)
	})

	for _, test := range []struct {
		name      string
		reference string
	}{
		{name: "parameter namespace", reference: "{{param.authValue}}"},
		{name: "data namespace", reference: "{{data.rows.value}}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const canary = "batch-static-namespace-canary-27a1"
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"static-"+strings.ReplaceAll(test.name, " ", "-"),
				"static-namespace-credential-sha",
			)
			commit.Scripts[0].Parameters = []dsl.Parameter{{
				Name:   "authValue",
				Type:   "list",
				Values: []string{canary},
			}}
			commit.Scripts[0].DataSources = []dsl.DataSource{{
				Name:           "rows",
				Type:           "csv",
				InlineData:     "value\n" + canary + "\n",
				FirstRowHeader: true,
				Strategy:       "sequential",
			}}
			commit.Scripts[0].Actions[0].HeaderList = dsl.HeaderList{{
				Name:  "Authorization",
				Value: "Bearer " + test.reference,
			}}

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			if strings.Contains(err.Error(), canary) {
				t.Fatalf("credential boundary disclosed static source value: %v", err)
			}
			assertGenericImportBatchTablesEmpty(t, store)
		})
	}
}

func TestGenericImportBatchAcceptsPriorDynamicCredentialProvenance(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"prior-dynamic-credential-session",
		"prior-dynamic-credential-sha",
	)
	commit.Scripts[0].Actions = []dsl.Action{
		{
			Name:   "Establish session",
			Type:   "http_request",
			Method: "POST",
			Target: "https://example.test/login",
			Extractors: []dsl.Extractor{
				{Name: "access_token", Source: "body", Type: "jsonpath", Expression: "$.access_token"},
				{Name: "session_id", Source: "header", Type: "regex", Expression: `session=([^;]+)`},
			},
		},
		{
			Name:   "Use session",
			Type:   "http_request",
			Method: "GET",
			Target: "https://example.test/profile",
			HeaderList: dsl.HeaderList{
				{Name: "Authorization", Value: "Bearer {{var.access_token}}"},
				{Name: "Cookie", Value: "session={{var.session_id}}"},
			},
			Auth: &dsl.AuthConfig{Type: "bearer", Token: "{{var.access_token}}"},
		},
	}

	result, err := store.CommitImportBatch(commit)
	if err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	if result == nil || !result.Created {
		t.Fatalf("CommitImportBatch result = %+v, want created", result)
	}
	stored, err := store.GetScript(commit.Scripts[0].ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	if got := stored.Actions[1].HeaderList[0].Value; got != "Bearer {{var.access_token}}" {
		t.Fatalf("stored Authorization = %q", got)
	}
	if got := stored.Actions[1].HeaderList[1].Value; got != "session={{var.session_id}}" {
		t.Fatalf("stored Cookie = %q", got)
	}
}

func TestGenericImportBatchAcceptsPriorOAuthCredentialProvenance(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"openapi_native",
		"openapi-native-commit-v1",
		"prior-oauth-credential-session",
		"prior-oauth-credential-sha",
	)
	commit.Scripts[0].Init = []dsl.Action{{
		Name:   "Acquire OAuth token",
		Type:   "http_oauth2_token",
		Method: "POST",
		Target: "https://auth.example.test/token",
		Params: map[string]any{
			"grant_type":    "client_credentials",
			"client_id":     "public-client-id",
			"client_secret": "{{secret.OAUTH_CLIENT_SECRET}}",
			"save_as":       "oauth_access_token",
		},
	}}
	commit.Scripts[0].Actions[0].HeaderList = dsl.HeaderList{{
		Name:  "Authorization",
		Value: "Bearer {{var.oauth_access_token}}",
	}}

	if _, err := store.CommitImportBatch(commit); err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
}

func TestGenericImportBatchRejectsFutureDynamicCredentialProvenance(t *testing.T) {
	tests := []struct {
		name   string
		header dsl.HeaderEntry
	}{
		{
			name:   "authorization",
			header: dsl.HeaderEntry{Name: "Authorization", Value: "Bearer {{var.access_token}}"},
		},
		{
			name:   "composed cookie",
			header: dsl.HeaderEntry{Name: "Cookie", Value: "session={{var.session_id}}"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"future-dynamic-"+strings.ReplaceAll(test.name, " ", "-"),
				"future-dynamic-credential-sha",
			)
			commit.Scripts[0].Actions = []dsl.Action{
				{
					Name:       "Use future value",
					Type:       "http_request",
					Method:     "GET",
					Target:     "https://example.test/profile",
					HeaderList: dsl.HeaderList{test.header},
				},
				{
					Name:   "Extract too late",
					Type:   "http_request",
					Method: "POST",
					Target: "https://example.test/login",
					Extractors: []dsl.Extractor{
						{Name: "access_token", Source: "body", Type: "jsonpath", Expression: "$.access_token"},
						{Name: "session_id", Source: "header", Type: "regex", Expression: `session=([^;]+)`},
					},
				},
			}

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			if strings.Contains(err.Error(), "access_token") || strings.Contains(err.Error(), "session_id") {
				t.Fatalf("credential boundary disclosed binding identity: %v", err)
			}
			assertGenericImportBatchTablesEmpty(t, store)
		})
	}
}

func TestGenericImportBatchRejectsStaticComposedCookieBinding(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"static-cookie-binding-session",
		"static-cookie-binding-sha",
	)
	commit.Scripts[0].Parameters = []dsl.Parameter{{
		Name:   "cookieValue",
		Type:   "list",
		Values: []string{"static-session-value"},
	}}
	commit.Scripts[0].Actions[0].HeaderList = dsl.HeaderList{{
		Name:  "Cookie",
		Value: "session={{var.cookieValue}}",
	}}

	_, err := store.CommitImportBatch(commit)
	if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
		t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
	}
	if strings.Contains(err.Error(), "static-session-value") {
		t.Fatalf("credential boundary disclosed static parameter value: %v", err)
	}
	assertGenericImportBatchTablesEmpty(t, store)
}

func TestGenericImportBatchAllowsOrdinaryParameterAndDataColumns(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"ordinary-parameter-session",
		"ordinary-parameter-sha",
	)
	commit.Scripts[0].Parameters = []dsl.Parameter{
		{Name: "region", Type: "list", Values: []string{"east", "west"}},
		{Name: "key", Type: "list", Values: []string{"record-1", "record-2"}},
	}
	commit.Scripts[0].DataSources = []dsl.DataSource{{
		Name:           "regions",
		Type:           "csv",
		InlineData:     "region,key\neast,record-1\nwest,record-2\n",
		FirstRowHeader: true,
		Strategy:       "sequential",
	}}
	commit.Scripts[0].Actions[0].Headers = map[string]string{
		"X-Region": "{{var.region}}",
	}

	if _, err := store.CommitImportBatch(commit); err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
}

func assertGenericImportBatchTablesEmpty(t *testing.T, store *SQLiteStore) {
	t.Helper()
	for _, table := range []string{
		"import_batches",
		"scripts",
		"import_source_artifacts",
	} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("failed credential validation persisted %d row(s) in %s", count, table)
		}
	}
}

func TestGenericImportBatchRejectsCorruptReceiptCardinality(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native", "postman-native-commit-v1",
		"corrupt-cardinality", "corrupt-cardinality-sha",
	)
	if _, err := store.CommitImportBatch(commit); err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE import_batches
		SET candidate_ids_json = '["candidate-1"]'
		WHERE project_id = ? AND idempotency_key = ?`,
		commit.Receipt.ProjectID, commit.Receipt.IdempotencyKey); err != nil {
		t.Fatalf("corrupt receipt: %v", err)
	}
	if _, err := store.GetImportBatchByIdempotencyKey(
		commit.Receipt.ProjectID, commit.Receipt.Profile, commit.Receipt.IdempotencyKey,
	); err == nil || !strings.Contains(err.Error(), "counts do not match") {
		t.Fatalf("corrupt receipt error = %v, want cardinality failure", err)
	}
}

func genericImportBatchFixture(profile, digestProfile, idempotencyKey, requestSHA string) *ImportBatchCommit {
	legacy := brunoNativeImportCommitFixture(idempotencyKey, requestSHA)
	return &ImportBatchCommit{
		Receipt: ImportBatchReceipt{
			ID:                   legacy.Batch.ID,
			ProjectID:            legacy.Batch.ProjectID,
			Profile:              profile,
			ReceiptVersion:       ImportBatchReceiptVersion1,
			IdempotencyKey:       legacy.Batch.IdempotencyKey,
			RequestDigestProfile: digestProfile,
			RequestSHA256:        legacy.Batch.RequestSHA256,
			CandidateIDs:         append([]string(nil), legacy.Batch.CandidateIDs...),
			CreatedBy:            legacy.Batch.CreatedBy,
		},
		Scripts:           legacy.Scripts,
		RawArtifact:       legacy.RawArtifact,
		SanitizedArtifact: legacy.SanitizedArtifact,
		RefKeyEnv:         legacy.RefKeyEnv,
	}
}
