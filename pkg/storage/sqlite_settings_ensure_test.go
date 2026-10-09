package storage

import (
	"path/filepath"
	"testing"
)

func TestEnsureSettingKeepsTheFirstValue(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "ensure-setting.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testEnsureSettingKeepsTheFirstValue(t, store)
}

func testEnsureSettingKeepsTheFirstValue(t *testing.T, store *SQLiteStore) {
	t.Helper()
	first, err := store.EnsureSetting("controller.capacity_owner_id", "owner-a")
	if err != nil || first != "owner-a" {
		t.Fatalf("first EnsureSetting = (%q, %v), want owner-a", first, err)
	}
	second, err := store.EnsureSetting("controller.capacity_owner_id", "owner-b")
	if err != nil || second != "owner-a" {
		t.Fatalf("second EnsureSetting = (%q, %v), want the first writer's owner-a", second, err)
	}
	if stored, err := store.GetSetting("controller.capacity_owner_id"); err != nil || stored != "owner-a" {
		t.Fatalf("stored setting = (%q, %v)", stored, err)
	}
}
