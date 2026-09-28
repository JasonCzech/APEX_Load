package api

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/storage"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestArchiveCollectionPreservesUnconfirmedMergedSource(t *testing.T) {
	for _, mode := range []string{"complete", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, backend := collectionCacheFixture(t, "a")
			root := filepath.Join(resolveArchiveRoot(), "remote", run.ID)
			if mode == "complete" {
				record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "original outcome", time.Now(), true)
				if err != nil || record == nil {
					t.Fatalf("first collection: %+v %v", record, err)
				}
			} else {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "unconfirmed-evidence"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := func() map[string]string {
				t.Helper()
				result := make(map[string]string)
				err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.IsDir() {
						return nil
					}
					content, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					result[path] = string(content)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			before := snapshot()
			if _, err := store.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("fixture already has publication: %v", err)
			}
			record, err := backend.stageRemoteArchive(tr, run, "FAILED", "late changed outcome", time.Now().Add(time.Minute), true)
			if !errors.Is(err, errUnconfirmedArchiveCollectionSource) || record != nil {
				t.Fatalf("unconfirmed source replacement: %+v %v", record, err)
			}
			if !reflect.DeepEqual(snapshot(), before) {
				t.Fatal("unconfirmed merged source changed")
			}
			if _, err := store.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("collection invented publication: %v", err)
			}
		})
	}
}

func TestArchiveCollectionConcurrentControllersPreserveFirstSource(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a")
	row, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	peer := NewTestRunner(1, nil, nil)
	peer.testStore, peer.archiveStore = store, store
	peerRun := managedTestFromRecord(row)
	peerRun.remoteClientsByTarget = run.remoteClientsByTarget
	type outcome struct {
		record *storage.RunArchiveRecord
		err    error
	}
	results := make(chan outcome, 2)
	for i, runner := range []*TestRunner{tr, peer} {
		test := run
		if i == 1 {
			test = peerRun
		}
		go func() {
			record, err := backend.stageRemoteArchive(runner, test, "FAILED", fmt.Sprintf("collector-%d", i), time.Now(), true)
			results <- outcome{record, err}
		}()
	}
	var winner *storage.RunArchiveRecord
	refused := 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil && result.record != nil {
			if winner != nil {
				t.Fatal("both collectors replaced the source")
			}
			winner = result.record
		} else if errors.Is(result.err, errUnconfirmedArchiveCollectionSource) {
			refused++
		} else {
			t.Fatalf("unexpected collection: %+v", result)
		}
	}
	if winner == nil || refused != 1 {
		t.Fatal("collection fence did not select one source")
	}
	data, err := os.ReadFile(winner.ManifestPath)
	if err != nil || sha256BytesHex(data) != winner.ManifestSHA256 {
		t.Fatal("winning source changed after source lock release", err)
	}
}
