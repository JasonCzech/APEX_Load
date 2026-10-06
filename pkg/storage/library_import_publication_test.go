package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func publicationSQLitePair(t *testing.T) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "publication.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close() })
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	return first, second
}

func requirePublication(t *testing.T, store *SQLiteStore) *LibraryImportPublicationSnapshot {
	t.Helper()
	snapshot, err := store.GetLibraryImportPublicationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(snapshot.Raw))
	if snapshot.State.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("publication hash does not describe exact raw bytes")
	}
	return snapshot
}

func TestLibraryImportPublicationExactStateAndCAS(t *testing.T) {
	first, second := publicationSQLitePair(t)
	absent := requirePublication(t, first)
	if absent.State.Exists || absent.Raw != "" {
		t.Fatal("missing row became present")
	}
	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	locked, err := readLibraryImportPublicationTx(tx)
	if err != nil || locked.State != absent.State {
		tx.Rollback()
		t.Fatalf("absent fence = %#v, %v", locked, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if requirePublication(t, second).State != absent.State {
		t.Fatal("fence fabricated a claims row")
	}
	if err := first.SaveSetting(LibraryImportClaimsSettingKey, ""); err != nil {
		t.Fatal(err)
	}
	empty := requirePublication(t, second)
	if !empty.State.Exists || empty.State.SHA256 != absent.State.SHA256 || empty.State == absent.State {
		t.Fatal("present-empty and absent authority was collapsed")
	}
	if applied, err := second.CompareAndSwapLibraryImportClaims("", "[]"); err != nil || applied {
		t.Fatalf("present-empty insert = %v, %v", applied, err)
	}
	if err := first.DeleteSetting(LibraryImportClaimsSettingKey); err != nil {
		t.Fatal(err)
	}
	if applied, err := first.CompareAndSwapLibraryImportClaims("", "[]"); err != nil || !applied {
		t.Fatalf("missing insert = %v, %v", applied, err)
	}
	before := requirePublication(t, second)
	if applied, err := second.CompareAndSwapLibraryImportClaims("[]", "[ ]"); err != nil || !applied {
		t.Fatalf("exact-byte update = %v, %v", applied, err)
	}
	after := requirePublication(t, first)
	if before.State.SHA256 == after.State.SHA256 || after.Raw != "[ ]" {
		t.Fatal("semantically equal JSON lost byte identity")
	}
	if applied, err := first.CompareAndSwapLibraryImportClaims("[]", "null"); err != nil || applied {
		t.Fatalf("stale raw CAS = %v, %v", applied, err)
	}
	if requirePublication(t, second).State != after.State {
		t.Fatal("losing CAS changed state")
	}
	if applied, err := first.CompareAndSwapSetting("unrelated", "", "ordinary"); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if applied, err := first.CompareAndSwapSetting("unrelated", "", "replacement"); err != nil || applied {
		t.Fatal("generic missing-only semantics changed")
	}
}

func TestLibraryImportPublicationTwoStoreSingleWinner(t *testing.T) {
	for _, expected := range []string{"", "[]"} {
		t.Run("expected_"+expected, func(t *testing.T) {
			first, second := publicationSQLitePair(t)
			if expected != "" {
				if err := first.SaveSetting(LibraryImportClaimsSettingKey, expected); err != nil {
					t.Fatal(err)
				}
			}
			start := make(chan struct{})
			results := make(chan bool, 2)
			failures := make(chan error, 2)
			var workers sync.WaitGroup
			for index, store := range []*SQLiteStore{first, second} {
				workers.Add(1)
				go func(store *SQLiteStore, value string) {
					defer workers.Done()
					<-start
					applied, err := store.CompareAndSwapLibraryImportClaims(expected, value)
					results <- applied
					failures <- err
				}(store, []string{"[ ]", "null"}[index])
			}
			close(start)
			workers.Wait()
			close(results)
			close(failures)
			winners := 0
			for applied := range results {
				if applied {
					winners++
				}
			}
			for err := range failures {
				if err != nil {
					t.Fatal(err)
				}
			}
			if winners != 1 {
				t.Fatalf("CAS winners = %d", winners)
			}
			if requirePublication(t, first).State != requirePublication(t, second).State {
				t.Fatal("stores disagree on committed state")
			}
		})
	}
}

func TestLibraryImportPublicationValidationAndBounds(t *testing.T) {
	for _, raw := range []string{"", " ", "null", "[]", "[{}]", "[" + strings.Repeat("{},", MaxLibraryImportClaims-1) + "{}]"} {
		if err := validateLibraryImportPublicationRaw(raw); err != nil {
			t.Fatalf("valid bounded input: %v", err)
		}
	}
	for _, raw := range []string{"{", "{}", "[}", "[", "[] []", "null x", "[\"\xff\"]", "[" + strings.Repeat("{},", MaxLibraryImportClaims) + "{}]"} {
		if err := validateLibraryImportPublicationRaw(raw); err == nil {
			t.Fatalf("accepted malformed/over-count input %q", raw[:min(len(raw), 20)])
		}
	}
	first, _ := publicationSQLitePair(t)
	for _, raw := range []string{"[", "{}", "[] []"} {
		if applied, err := first.CompareAndSwapLibraryImportClaims("", raw); err == nil || applied {
			t.Fatal("invalid candidate committed")
		}
		if requirePublication(t, first).State.Exists {
			t.Fatal("invalid candidate fabricated row")
		}
	}
	// Persist via the intentionally unchanged generic seam to emulate a legacy
	// oversized value. SQL byte length, not Unicode character count, is bounded.
	raw := `["` + strings.Repeat("é", MaxLibraryImportClaimsRawBytes/2) + `"]`
	if err := first.SaveSetting(LibraryImportClaimsSettingKey, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := first.GetLibraryImportPublicationSnapshot(); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized read = %v", err)
	}
	if applied, err := first.CompareAndSwapLibraryImportClaims("", "[]"); err == nil || applied {
		t.Fatal("oversized current state was replaced")
	}
	if err := first.DeleteSetting(LibraryImportClaimsSettingKey); err != nil {
		t.Fatal(err)
	}
	boundary := `["` + strings.Repeat("x", MaxLibraryImportClaimsRawBytes-4) + `"]`
	if err := validateLibraryImportPublicationRaw(boundary); err != nil {
		t.Fatalf("exact byte boundary: %v", err)
	}
	if err := validateLibraryImportPublicationRaw(boundary + " "); err == nil {
		t.Fatal("one-byte overflow accepted")
	}
}

func TestLibraryImportPublicationInvalidUTF8RetainsExactBytes(t *testing.T) {
	first, _ := publicationSQLitePair(t)
	raw := "[{\"kind\":\"script\",\"local_item_id\":\"\xff\"}]"
	if err := first.SaveSetting(LibraryImportClaimsSettingKey, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := first.GetLibraryImportPublicationSnapshot(); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("invalid UTF-8 read: %v", err)
	}
	if applied, err := first.CompareAndSwapLibraryImportClaims(raw, "[]"); err == nil || applied {
		t.Fatal("invalid UTF-8 was normalized or replaced")
	}
	stored, err := first.GetSetting(LibraryImportClaimsSettingKey)
	if err != nil || stored != raw {
		t.Fatal("refusal altered corrupt bytes", err)
	}
}

func TestLibraryImportPublicationSQLiteFenceBlocksClaimWriter(t *testing.T) {
	for _, expected := range []string{"", "[]"} {
		t.Run("expected_"+expected, func(t *testing.T) {
			first, second := publicationSQLitePair(t)
			if expected != "" {
				if err := first.SaveSetting(LibraryImportClaimsSettingKey, expected); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := first.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := acquireLibraryImportPublicationFenceTx(tx); err != nil {
				t.Fatal(err)
			}
			if _, err := lockProjectForOwnedMutation(tx, DefaultProjectID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				applied, err := second.CompareAndSwapLibraryImportClaims(expected, "[ ]")
				if err == nil && !applied {
					err = fmt.Errorf("waiting writer lost its unchanged CAS")
				}
				done <- err
			}()
			joined := false
			defer func() {
				tx.Rollback()
				if !joined {
					select {
					case err := <-done:
						if err != nil {
							t.Error(err)
						}
					case <-time.After(10 * time.Second):
						t.Error("publication writer did not join")
					}
				}
			}()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for second.db.db.Stats().InUse == 0 {
				select {
				case err := <-done:
					joined = true
					t.Fatalf("writer passed held publication fence: %v", err)
				case <-deadline.C:
					t.Fatal("writer never acquired a connection")
				case <-ticker.C:
				}
			}
			select {
			case err := <-done:
				joined = true
				t.Fatalf("writer passed held publication fence: %v", err)
			default:
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-deadline.C:
				t.Fatal("writer did not finish after fence release")
			}
			if got := requirePublication(t, first); got.Raw != "[ ]" || !got.State.Exists {
				t.Fatal("waiting writer did not publish", got)
			}
		})
	}
}
