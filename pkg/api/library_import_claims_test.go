package api

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type commitBetweenPublishedScriptScansStore struct {
	Store   storage.Store
	once    sync.Once
	between func()
}

func (s *commitBetweenPublishedScriptScansStore) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	scripts, err := s.Store.ListScripts(projectID, folderID)
	if err == nil {
		s.once.Do(s.between)
	}
	return scripts, err
}

func TestListPublishedImportScriptsRejectsCommitBetweenScans(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "published-script-list-commit-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	server := NewServer(ServerConfig{Store: base})
	first := libraryTestScript("list-race-first", "First staged script", storage.DefaultProjectID, "/first")
	second := libraryTestScript("list-race-second", "Second staged script", storage.DefaultProjectID, "/second")
	transaction, err := newCreateImportTransaction(server, "list-commit-race", []*dsl.Script{first, second}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	if _, err := transaction.createScript(first); err != nil {
		t.Fatal(err)
	}

	var commitErr error
	raceStore := &commitBetweenPublishedScriptScansStore{
		Store: base,
		between: func() {
			if _, err := transaction.createScript(second); err != nil {
				commitErr = err
				return
			}
			commitErr = transaction.commit()
		},
	}
	listed, err := listPublishedImportScripts(storeOverlay(raceStore), storage.DefaultProjectID, nil)
	if commitErr != nil {
		t.Fatalf("commit between publication scans: %v", commitErr)
	}
	if err != nil {
		t.Fatalf("list published scripts: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("mixed pre-commit candidates with post-commit publication state: %#v", listed)
	}

	// A fresh read sees the complete committed set; the raced read only fails
	// closed for its inconsistent snapshot.
	listed, err = listPublishedImportScripts(base, storage.DefaultProjectID, nil)
	if err != nil {
		t.Fatalf("list committed scripts: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("committed script count = %d, want 2", len(listed))
	}
}

type publicationDispatchStore struct {
	SQLiteStore   *storage.SQLiteStore
	claimCAS      atomic.Int64
	legacyCAS     atomic.Int64
	snapshotReads atomic.Int64
	conflictOnce  atomic.Bool
}

func (s *publicationDispatchStore) GetLibraryImportPublicationSnapshot() (*storage.LibraryImportPublicationSnapshot, error) {
	s.snapshotReads.Add(1)
	return s.SQLiteStore.GetLibraryImportPublicationSnapshot()
}
func (s *publicationDispatchStore) CompareAndSwapLibraryImportClaims(expected, value string) (bool, error) {
	s.claimCAS.Add(1)
	if s.conflictOnce.CompareAndSwap(true, false) {
		if applied, err := s.SQLiteStore.CompareAndSwapLibraryImportClaims(expected, "[]"); err != nil || !applied {
			return applied, err
		}
		return false, nil
	}
	return s.SQLiteStore.CompareAndSwapLibraryImportClaims(expected, value)
}
func (s *publicationDispatchStore) CompareAndSwapSetting(key, expected, value string) (bool, error) {
	if key == libraryImportClaimsSettingKey {
		s.legacyCAS.Add(1)
		return false, errors.New("qualified claims writer bypassed publication fence")
	}
	return s.SQLiteStore.CompareAndSwapSetting(key, expected, value)
}

func TestLibraryImportClaimsUseQualifiedPublicationAndRereadConflict(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "qualified-claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &publicationDispatchStore{SQLiteStore: base}
	server := NewServer(ServerConfig{Store: sqliteOverlay(store)})
	script := libraryTestScript("qualified-existing", "Published source", storage.DefaultProjectID, "/published")
	if _, err := server.runner.CreateScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	before, err := base.GetScriptAuthoringSnapshot(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := base.GetLibraryImportPublicationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	store.conflictOnce.Store(true)
	transaction, err := newCreateImportTransaction(server, "qualified-existing", []*dsl.Script{script}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := transaction.commitUnmutatedClaims(); err != nil {
			t.Error(err)
		}
		transaction.closeWithoutAssetCompensation()
	}()
	prepared, err := base.GetLibraryImportPublicationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State == initial.State || !prepared.State.Exists {
		t.Fatal("prepare did not change publication evidence")
	}
	after, err := base.GetScriptAuthoringSnapshot(script.ID)
	if err != nil || after.ContentRevision != before.ContentRevision {
		t.Fatalf("claim preparation mutated source content: %v", err)
	}
	if _, err := getPublishedImportScript(sqliteOverlay(store), script.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("prepared source remained published: %v", err)
	}
	if transaction.mutationStarted {
		t.Fatal("prepared claim started an asset mutation")
	}
	if store.claimCAS.Load() < 3 || store.legacyCAS.Load() != 0 || store.snapshotReads.Load() < 3 {
		t.Fatalf("qualified calls: CAS=%d generic=%d reads=%d", store.claimCAS.Load(), store.legacyCAS.Load(), store.snapshotReads.Load())
	}
	if err := transaction.commitUnmutatedClaims(); err != nil {
		t.Fatal(err)
	}
	transaction.closeWithoutAssetCompensation()
	if _, err := getPublishedImportScript(sqliteOverlay(store), script.ID); err != nil {
		t.Fatalf("terminal no-op lost source: %v", err)
	}
}

func TestLibraryImportClaimsRejectInvalidUTF8(t *testing.T) {
	if _, err := decodeLibraryImportClaims("[{\"identity\":\"\xff\"}]"); err == nil {
		t.Fatal("claims decoder normalized invalid identity bytes")
	}
}
