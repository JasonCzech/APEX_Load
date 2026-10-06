package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func resourceIdentityStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "resource-identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
func resourceIdentitySeed(t *testing.T, store *SQLiteStore, id, alias string, withArchive bool) *RunArchiveRecord {
	t.Helper()
	now := time.Unix(1700000000, 123456789).UTC()
	if err := store.SaveTest(&TestRecord{ID: id, RefKey: alias, Name: id, ProjectID: DefaultProjectID, Status: "COMPLETE", StartTime: now, MaterializationKey: "incarnation-" + id}); err != nil {
		t.Fatal(err)
	}
	archive := &RunArchiveRecord{TestID: id, RefKey: alias, ArchiveBackend: "filesystem", ArchivePath: "/archives/" + id, ManifestPath: "/archives/" + id + "/manifest.json", ManifestSHA256: "sha-" + id, CreatedAt: now, CompletedAt: now}
	if withArchive {
		if err := store.SaveRunArchive(archive); err != nil {
			t.Fatal(err)
		}
	}
	return archive
}
func TestBrowserResourceArchiveIdentityExactOwnerPrecedesAliasAndTombstone(t *testing.T) {
	checkBrowserResourceArchiveExactOwner(t, resourceIdentityStore(t))
}
func checkBrowserResourceArchiveExactOwner(t *testing.T, store *SQLiteStore) {
	t.Helper()
	_ = resourceIdentitySeed(t, store, "first", "collision", true)
	second := resourceIdentitySeed(t, store, "collision", "second", false)
	if _, err := store.ReadBrowserResourceArchiveIdentityContext(context.Background(), "collision"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unarchived exact owner fell through to foreign alias: %v", err)
	}
	if err := store.SaveRunArchive(second); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadBrowserResourceArchiveIdentityContext(context.Background(), "collision")
	if err != nil || got.TestID != "collision" || got.ProjectID != DefaultProjectID || got.MaterializationKey != "incarnation-collision" || got.Archive.ManifestSHA256 != second.ManifestSHA256 {
		t.Fatalf("wrong exact publication: %+v %v", got, err)
	}
	alias, err := store.ReadBrowserResourceArchiveIdentityContext(context.Background(), "second")
	if err != nil || !reflect.DeepEqual(alias, got) {
		t.Fatalf("authorized display alias did not resolve to same owner: %+v %v", alias, err)
	}
	if err := store.SaveArchiveCleanupJob(&ArchiveCleanupJob{TestID: second.TestID, ProjectID: DefaultProjectID, ArchiveRecord: *second, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadBrowserResourceArchiveIdentityContext(context.Background(), "collision"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleanup-owned archive was admitted: %v", err)
	}
}
func TestBrowserResourceArchiveIdentitySeesFreshPublicationAndCancellation(t *testing.T) {
	checkBrowserResourceArchiveFreshnessAndCancellation(t, resourceIdentityStore(t))
}
func checkBrowserResourceArchiveFreshnessAndCancellation(t *testing.T, store *SQLiteStore) {
	t.Helper()
	archive := resourceIdentitySeed(t, store, "run-1", "alias", true)
	first, err := store.ReadBrowserResourceArchiveIdentityContext(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	archive.ManifestSHA256 = "replacement-sha"
	archive.ArchivePath = "/archives/replacement"
	archive.ManifestPath = archive.ArchivePath + "/manifest.json"
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	current, err := store.ReadBrowserResourceArchiveIdentityContext(context.Background(), "run-1")
	if err != nil || current.Archive.ManifestSHA256 == first.Archive.ManifestSHA256 || first.Archive.ManifestSHA256 != "sha-run-1" {
		t.Fatalf("publication cached or alias mutated: %+v %v", current, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ReadBrowserResourceArchiveIdentityContext(ctx, "run-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("identity ignored cancellation: %v", err)
	}
	if won, err := store.CompareAndSwapSettingContext(ctx, "private.resource.key", "", "value"); won || !errors.Is(err, context.Canceled) {
		t.Fatalf("setting write ignored cancellation: %v %v", won, err)
	}
}
func TestBrowserResourceCursorSettingCASHasOneDurableWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resource-key.db")
	one, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	checkBrowserResourceSettingWinner(t, one, two)
}
func checkBrowserResourceSettingWinner(t *testing.T, one, two *SQLiteStore) {
	t.Helper()
	stores := []*SQLiteStore{one, two}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	failures := make(chan error, 2)
	for i, store := range stores {
		wg.Add(1)
		go func(i int, store *SQLiteStore) {
			defer wg.Done()
			won, err := store.CompareAndSwapSettingContext(context.Background(), "private.resource.key", "", []string{"first", "second"}[i])
			results <- won
			failures <- err
		}(i, store)
	}
	wg.Wait()
	close(results)
	close(failures)
	count := 0
	for won := range results {
		if won {
			count++
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("durable cursor keys have%d winners", count)
	}
	a, err := one.GetSettingContext(context.Background(), "private.resource.key")
	if err != nil {
		t.Fatal(err)
	}
	b, err := two.GetSettingContext(context.Background(), "private.resource.key")
	if err != nil || a != b || a == "" {
		t.Fatalf("replicas disagree on key: %q %q %v", a, b, err)
	}
	if won, err := two.CompareAndSwapSettingContext(context.Background(), "private.resource.key", "wrong", "replacement"); err != nil || won {
		t.Fatalf("stale expected key overwritten: %v %v", won, err)
	}
}
