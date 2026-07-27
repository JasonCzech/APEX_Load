package storage

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCommitBrunoNativeImportIsAtomicAndIdempotent(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := brunoNativeImportCommitFixture("native-session-1", "request-sha-1")

	first, err := store.CommitBrunoNativeImport(commit)
	if err != nil {
		t.Fatalf("CommitBrunoNativeImport: %v", err)
	}
	if !first.Created || first.Batch == nil || first.Batch.ID != "native-batch-1" || len(first.Scripts) != 2 {
		t.Fatalf("first result = %+v", first)
	}
	if first.Batch.RawArtifactID != "native-raw" || first.Batch.SanitizedArtifactID != "native-sanitized" {
		t.Fatalf("artifact ids = %q/%q", first.Batch.RawArtifactID, first.Batch.SanitizedArtifactID)
	}

	retry, err := store.CommitBrunoNativeImport(brunoNativeImportCommitFixture("native-session-1", "request-sha-1"))
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if retry.Created || retry.Batch.ID != first.Batch.ID || len(retry.Scripts) != 2 {
		t.Fatalf("retry result = %+v", retry)
	}
	originalName := retry.Scripts[0].Name
	if err := store.DeleteScript(retry.Scripts[0].ID); err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
	immutable, err := store.GetBrunoNativeImportByIdempotencyKey("native-session-1", "project-bruno")
	if err != nil {
		t.Fatalf("load immutable receipt after script deletion: %v", err)
	}
	if len(immutable.Scripts) != 2 || immutable.Scripts[0].Name != originalName {
		t.Fatalf("immutable receipt = %+v, want original script snapshots", immutable)
	}

	conflict := brunoNativeImportCommitFixture("native-session-1", "different-request-sha")
	if _, err := store.CommitBrunoNativeImport(conflict); !errors.Is(err, ErrBrunoNativeBatchConflict) {
		t.Fatalf("mismatched retry error = %v, want ErrBrunoNativeBatchConflict", err)
	}
}

func TestCommitBrunoNativeImportRollsBackEveryScriptAndReceipt(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := brunoNativeImportCommitFixture("native-session-rollback", "request-sha-rollback")
	missingFolder := "missing-folder"
	commit.Scripts[1].FolderID = &missingFolder

	if _, err := store.CommitBrunoNativeImport(commit); err == nil {
		t.Fatal("CommitBrunoNativeImport unexpectedly succeeded")
	}
	for _, id := range []string{"native-script-1", "native-script-2"} {
		if _, err := store.GetScript(id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("script %s lookup error = %v, want sql.ErrNoRows", id, err)
		}
	}
	if _, err := store.GetBrunoNativeImportByIdempotencyKey("native-session-rollback", "project-bruno"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("receipt lookup error = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.GetBrunoSourceArtifact("native-raw", "project-bruno"); !errors.Is(err, ErrBrunoRevisionNotFound) && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("raw artifact lookup error = %v, want not found", err)
	}
}

func TestCommitBrunoNativeImportAllocatesRefKeysInsideTransaction(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := brunoNativeImportCommitFixture("native-session-refkeys-fail", "request-sha-refkeys-fail")
	commit.RefKeyEnv = "local"
	for _, script := range commit.Scripts {
		script.RefKey = ""
	}
	missingFolder := "missing-folder"
	commit.Scripts[1].FolderID = &missingFolder

	if _, err := store.CommitBrunoNativeImport(commit); err == nil {
		t.Fatal("CommitBrunoNativeImport unexpectedly succeeded")
	}
	if sequence, err := store.GetSetting("refseq:LOC:SCR"); err != nil || sequence != "" {
		t.Fatalf("rolled-back reference sequence = %q, err=%v; want empty", sequence, err)
	}

	retry := brunoNativeImportCommitFixture("native-session-refkeys-ok", "request-sha-refkeys-ok")
	retry.Batch.ID = "native-batch-refkeys-ok"
	retry.RefKeyEnv = "local"
	for _, script := range retry.Scripts {
		script.RefKey = ""
	}
	result, err := store.CommitBrunoNativeImport(retry)
	if err != nil {
		t.Fatalf("CommitBrunoNativeImport retry: %v", err)
	}
	if got := []string{result.Scripts[0].RefKey, result.Scripts[1].RefKey}; got[0] != "LOC-SCR-0001-v1" || got[1] != "LOC-SCR-0002-v1" {
		t.Fatalf("transactional reference keys = %#v", got)
	}
	if sequence, err := store.GetSetting("refseq:LOC:SCR"); err != nil || sequence != "2" {
		t.Fatalf("committed reference sequence = %q, err=%v; want 2", sequence, err)
	}
}

func TestCommitBrunoNativeImportSharesContentAddressedArtifactsAcrossBatches(t *testing.T) {
	store := newBrunoImportTestStore(t)
	first := brunoNativeImportCommitFixture("native-session-dedupe-1", "request-sha-dedupe-1")
	if _, err := store.CommitBrunoNativeImport(first); err != nil {
		t.Fatalf("first CommitBrunoNativeImport: %v", err)
	}

	second := brunoNativeImportCommitFixture("native-session-dedupe-2", "request-sha-dedupe-2")
	second.Batch.ID = "native-batch-dedupe-2"
	second.Batch.CandidateIDs = []string{"candidate-3", "candidate-4"}
	second.Scripts[0].ID = "native-script-3"
	second.Scripts[0].RefKey = "SCR-TEST-3"
	second.Scripts[1].ID = "native-script-4"
	second.Scripts[1].RefKey = "SCR-TEST-4"
	second.RawArtifact.ID = "native-raw-duplicate-id"
	second.SanitizedArtifact.ID = "native-sanitized-duplicate-id"
	result, err := store.CommitBrunoNativeImport(second)
	if err != nil {
		t.Fatalf("second CommitBrunoNativeImport: %v", err)
	}
	if result.Batch.RawArtifactID != "native-raw" || result.Batch.SanitizedArtifactID != "native-sanitized" {
		t.Fatalf("deduplicated artifact ids = %q/%q", result.Batch.RawArtifactID, result.Batch.SanitizedArtifactID)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM bruno_source_artifacts WHERE project_id = ?`, "project-bruno").Scan(&count); err != nil {
		t.Fatalf("count Bruno artifacts: %v", err)
	}
	if count != 2 {
		t.Fatalf("shared artifact row count = %d, want raw + sanitized only", count)
	}
}

func brunoNativeImportCommitFixture(idempotencyKey, requestSHA string) *BrunoNativeImportCommit {
	folderID := "native-folder"
	scripts := []*dsl.Script{
		{
			ID: "native-script-1", RefKey: "SCR-TEST-1", Name: "Request one", Version: "1",
			Protocol: "http", ProjectID: "project-bruno", GeneratedBy: "apexload-bruno-native-importer",
			FolderID: &folderID,
			Actions:  []dsl.Action{{Name: "Request one", Type: "http_request", Method: "POST", Target: "https://example.test/one"}},
		},
		{
			ID: "native-script-2", RefKey: "SCR-TEST-2", Name: "Request two", Version: "1",
			Protocol: "http", ProjectID: "project-bruno", GeneratedBy: "apexload-bruno-native-importer",
			FolderID: &folderID,
			Actions:  []dsl.Action{{Name: "Request two", Type: "http_request", Method: "POST", Target: "https://example.test/two"}},
		},
	}
	return &BrunoNativeImportCommit{
		Batch: BrunoNativeImportBatch{
			ID:             "native-batch-1",
			ProjectID:      "project-bruno",
			IdempotencyKey: idempotencyKey,
			RequestSHA256:  requestSHA,
			CandidateIDs:   []string{"candidate-1", "candidate-2"},
			CreatedBy:      "test",
		},
		Scripts: scripts,
		RawArtifact: BrunoSourceArtifact{
			ID: "native-raw", BackendKey: "objects/native-raw", SHA256: "native-raw-sha",
			SizeBytes: 10, ManifestSHA256: "native-manifest", SourceFormat: "open_collection_yaml",
			EncryptionMetadata: map[string]any{"algorithm": "AES-256-GCM"},
		},
		SanitizedArtifact: BrunoSourceArtifact{
			ID: "native-sanitized", BackendKey: "objects/native-sanitized", SHA256: "native-sanitized-sha",
			SizeBytes: 9, ManifestSHA256: "native-manifest", SourceFormat: "open_collection_yaml",
			EncryptionMetadata: map[string]any{"algorithm": "AES-256-GCM"},
		},
	}
}
