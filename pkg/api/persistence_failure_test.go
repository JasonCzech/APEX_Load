package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type failingScriptPersistence struct {
	err error
}

func (s failingScriptPersistence) SaveScript(*dsl.Script) error { return s.err }
func (s failingScriptPersistence) DeleteScript(string) error    { return s.err }
func (s failingScriptPersistence) ListScripts(string, *string) ([]*dsl.Script, error) {
	return nil, nil
}
func (s failingScriptPersistence) MoveScript(string, *string) error { return s.err }

func TestDurableScriptCRUDDoesNotAcknowledgePersistenceFailure(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	stored := runner.StoreScript(&dsl.Script{ID: "script-1", Name: "Before", Protocol: "http", ProjectID: "project-a"})
	wantErr := errors.New("database unavailable")
	runner.scriptStore = failingScriptPersistence{err: wantErr}

	if created, err := runner.StoreScriptDurable(&dsl.Script{ID: "script-2", Name: "Create", Protocol: "http", ProjectID: "project-a"}); !errors.Is(err, wantErr) || created != nil {
		t.Fatalf("create = %#v, %v; want nil persistence error", created, err)
	}
	if _, ok := runner.GetScript("script-2"); ok {
		t.Fatal("failed create was added to memory")
	}
	if updated, ok, err := runner.UpdateScriptDurable(stored.ID, &dsl.Script{Name: "After", Protocol: "http"}); !errors.Is(err, wantErr) || ok || updated != nil {
		t.Fatalf("update = %#v, %v, %v; want failure", updated, ok, err)
	}
	if current, _ := runner.GetScript(stored.ID); current.Name != "Before" {
		t.Fatalf("failed update changed memory to %q", current.Name)
	}
	if deleted, err := runner.DeleteScriptDurable(stored.ID); !errors.Is(err, wantErr) || deleted {
		t.Fatalf("delete = %v, %v; want failure", deleted, err)
	}
	if _, ok := runner.GetScript(stored.ID); !ok {
		t.Fatal("failed delete removed script from memory")
	}
	if moved, err := runner.MoveScriptDurable(stored.ID, ptrString("folder")); !errors.Is(err, wantErr) || moved {
		t.Fatalf("move = %v, %v; want failure", moved, err)
	}
	if current, _ := runner.GetScript(stored.ID); current.FolderID != nil {
		t.Fatalf("failed move changed folder to %#v", current.FolderID)
	}
}

func TestCreateScriptDurableFailsClosedWithoutAtomicCreateOnlyStore(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.scriptStore = failingScriptPersistence{err: errors.New("ordinary UPSERT must not be called")}
	created, err := runner.CreateScriptDurable(&dsl.Script{ID: "approval-script", Name: "Approval", Protocol: "http", ProjectID: "project-a"})
	if err == nil || created != nil {
		t.Fatalf("CreateScriptDurable = %#v, %v; want unsupported create-only failure", created, err)
	}
	if !strings.Contains(err.Error(), "atomic create-only") {
		t.Fatalf("CreateScriptDurable error = %v", err)
	}
	if _, ok := runner.GetScript("approval-script"); ok {
		t.Fatal("unsupported durable store still populated the in-memory script cache")
	}
}

type failingTestPersistence struct{ err error }

func (s failingTestPersistence) SaveTest(*storage.TestRecord) error { return s.err }
func (s failingTestPersistence) GetTest(string) (*storage.TestRecord, error) {
	return nil, s.err
}
func (s failingTestPersistence) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, s.err
}
func (s failingTestPersistence) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, s.err
}
func (s failingTestPersistence) UpdateTestStatus(string, string, *time.Time, string) error {
	return s.err
}
func (s failingTestPersistence) DeleteTest(string) error        { return s.err }
func (s failingTestPersistence) MoveTest(string, *string) error { return s.err }

func TestLegacyDeleteTestPreservesArchiveWhenDurableDeleteFails(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	archivePath := filepath.Join(archiveRoot, "durable-delete-failure")
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("database unavailable")
	runner := NewTestRunner(1, NewHub(), nil)
	record := &storage.RunArchiveRecord{TestID: "durable-delete-failure", ArchiveBackend: archiveBackendFilesystem, ArchivePath: archivePath}
	runner.mu.Lock()
	runner.tests[record.TestID] = &ManagedTest{ID: record.TestID, Name: "delete", Status: "COMPLETE", Archive: record}
	runner.archives[record.TestID] = record
	runner.mu.Unlock()
	runner.testStore = failingTestPersistence{err: wantErr}

	deleted, err := runner.DeleteTestWithError(record.TestID)
	if deleted || !errors.Is(err, wantErr) {
		t.Fatalf("DeleteTestWithError = (%v, %v), want false and persistence error", deleted, err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive was destroyed before durable delete committed: %v", err)
	}
	if _, ok := runner.GetTest(record.TestID); !ok {
		t.Fatal("failed durable delete removed the in-memory test")
	}
}

func ptrString(value string) *string { return &value }
