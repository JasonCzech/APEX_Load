package storage

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCreateExternalResultImportIfAbsentIsImmutableAcrossReplicas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external-result-cas.db")
	firstStore, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer firstStore.Close()
	secondStore, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()

	base := ExternalResultImport{
		ID:        "deterministic-import",
		Provider:  "apexload",
		Name:      "immutable evidence",
		ProjectID: DefaultProjectID,
		Source:    "library://source/result",
		Summary:   map[string]any{"status": "passed"},
		CreatedAt: time.Now().UTC(),
	}
	left := base
	left.RawResult = map[string]any{"content_hash": "sha256:left"}
	right := base
	right.RawResult = map[string]any{"content_hash": "sha256:right"}

	type outcome struct {
		created bool
		err     error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	write := func(store *SQLiteStore, result *ExternalResultImport) {
		defer ready.Done()
		<-start
		created, err := store.CreateExternalResultImportIfAbsent(result)
		outcomes <- outcome{created: created, err: err}
	}
	go write(firstStore, &left)
	go write(secondStore, &right)
	close(start)
	ready.Wait()
	close(outcomes)

	createdCount := 0
	for got := range outcomes {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
	stored, err := firstStore.GetExternalResultImport(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := stored.RawResult["content_hash"].(string)
	if hash != "sha256:left" && hash != "sha256:right" {
		t.Fatalf("stored raw evidence = %#v", stored.RawResult)
	}

	losingRaw := right.RawResult
	if hash == "sha256:right" {
		losingRaw = left.RawResult
	}
	deleted, err := secondStore.DeleteExternalResultImportIfRawResult(base.ID, losingRaw)
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("conditional delete accepted the losing evidence payload")
	}
	if _, err := firstStore.GetExternalResultImport(base.ID); err != nil {
		t.Fatalf("winning evidence disappeared: %v", err)
	}
}

func TestDeleteExternalResultImportIfRawResultUsesVersionFence(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "external-result-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result := &ExternalResultImport{
		ID: "import", Provider: "apexload", Name: "result", ProjectID: DefaultProjectID,
		RawResult: map[string]any{"content_hash": "sha256:winner"}, CreatedAt: time.Now().UTC(),
	}
	if created, err := store.CreateExternalResultImportIfAbsent(result); err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	if deleted, err := store.DeleteExternalResultImportIfRawResult(result.ID, map[string]any{"content_hash": "sha256:stale"}); err != nil || deleted {
		t.Fatalf("stale delete = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteExternalResultImportIfRawResult(result.ID, result.RawResult); err != nil || !deleted {
		t.Fatalf("matching delete = %v, %v", deleted, err)
	}
}

func TestDeleteExternalResultImportIfUnchangedFencesEveryField(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "external-result-full-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result := &ExternalResultImport{
		ID: "import", Provider: "apexload", Name: "result", ProjectID: DefaultProjectID,
		Summary: map[string]any{"status": "passed"},
		RawResult: map[string]any{
			"test": struct {
				ID     string `json:"id"`
				VUsers int    `json:"vusers"`
			}{ID: "run", VUsers: 5},
		},
		CreatedAt: time.Now().UTC(),
	}
	if created, err := store.CreateExternalResultImportIfAbsent(result); err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	stored, err := store.GetExternalResultImport(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := *stored
	stale.Name = "changed metadata"
	if deleted, err := store.DeleteExternalResultImportIfUnchanged(&stale); err != nil || deleted {
		t.Fatalf("metadata-mismatched delete = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteExternalResultImportIfUnchanged(stored); err != nil || !deleted {
		t.Fatalf("matching full-row delete = %v, %v", deleted, err)
	}
}
