package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

type interruptedArchiveRecordUpdateStore struct{ *storage.SQLiteStore }

func (s *interruptedArchiveRecordUpdateStore) UpdateArchiveFinalizationRecordIfToken(string, string, *storage.RunArchiveRecord) (bool, error) {
	return false, errors.New("publication interrupted after adjunct write")
}
func TestArchivePublicationRecoversInterruptedAdjunctWrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &interruptedArchiveRecordUpdateStore{sqlite}
	test, record := createArchiveFinalizationTest(t, store, "interrupted-adjunct", root)
	if err := NewTestRunner(1, NewHub(), store).finalizeRunArchiveDurably(context.Background(), test, record, true); err == nil {
		t.Fatal("interruption ignored")
	}
	job, err := sqlite.GetArchiveFinalizationJob(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewTestRunner(1, NewHub(), sqlite).processArchiveFinalizationJob(context.Background(), job); err != nil {
		t.Fatalf("interrupted adjunct retry: %v", err)
	}
}
