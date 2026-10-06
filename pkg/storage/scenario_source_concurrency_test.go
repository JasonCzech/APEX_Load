package storage

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestScenarioSourceTwoStoreCreateSingleWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-concurrency.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	old := seedScenarioRevision(t, first)
	evidence := requireScenarioSources(t, first, old.Scripts).Admission
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	type outcome struct {
		result *ScenarioAuthoringMutationResult
		err    error
	}
	done := make(chan outcome, 2)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, store := range []*SQLiteStore{first, second} {
		workers.Add(1)
		go func(store *SQLiteStore) {
			defer workers.Done()
			<-start
			result, err := trySourceMutation(store, old, true, evidence)
			done <- outcome{result, err}
		}(store)
	}
	close(start)
	workers.Wait()
	close(done)
	winners, losers := 0, 0
	for o := range done {
		if o.err == nil && o.result != nil && o.result.Applied {
			winners++
		} else if errors.Is(o.err, ErrScenarioSourceAdmissionConflict) && o.result == nil {
			losers++
		} else {
			t.Fatalf("unexpected outcome %#v/%v", o.result, o.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatal("creation did not yield one winner and one safe conflict")
	}
	if !reflect.DeepEqual(requireScenarioAuthoring(t, first, "source-new"), requireScenarioAuthoring(t, second, "source-new")) {
		t.Fatal("stores disagree on winner")
	}
}

func TestScenarioSourceTwoStorePublicationTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source-publication.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	old := seedScenarioRevision(t, first)
	evidence := requireScenarioSources(t, first, old.Scripts).Admission
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	hold, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback()
	if err := acquireLibraryImportPublicationFenceTx(hold); err != nil {
		t.Fatal(err)
	}
	raw := `[{"kind":"script","prepared":true,"local_item_id":"scn-script","remote_id":"direct-import:barrier"}]`
	if _, err := hold.Exec(`INSERT INTO app_settings(key,value) VALUES(?,?)`, LibraryImportClaimsSettingKey, raw); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(); err != nil {
		t.Fatal(err)
	}
	assertSourceMutationRawRefused(t, second, old, evidence)
	if snapshot, err := second.GetScenarioSourceAuthoringSnapshot(old.Scripts); snapshot != nil || !errors.Is(err, ErrScenarioSourceAdmissionConflict) {
		t.Fatal("initial second-store source remained published", err)
	}
}
