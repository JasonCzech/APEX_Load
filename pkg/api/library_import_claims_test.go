package api

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type commitBetweenPublishedScriptScansStore struct {
	storage.Store
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
	listed, err := listPublishedImportScripts(raceStore, storage.DefaultProjectID, nil)
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
