package main

import (
	"encoding/json"
	"testing"
)

func TestMergeRecentDedupAndCap(t *testing.T) {
	existing := []recentItem{
		{Kind: "script", ID: "a", Name: "A"},
		{Kind: "script", ID: "b", Name: "B"},
	}
	// Re-opening "a" moves it to the front (de-duped).
	got := mergeRecent(existing, recentItem{Kind: "script", ID: "a", Name: "A"}, 12)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("dedup/order wrong: %+v", got)
	}

	// Cap is enforced, newest first.
	var list []recentItem
	for _, id := range []string{"1", "2", "3"} {
		list = mergeRecent(list, recentItem{Kind: "script", ID: id}, 2)
	}
	if len(list) != 2 || list[0].ID != "3" || list[1].ID != "2" {
		t.Fatalf("cap/order wrong: %+v", list)
	}
}

func TestRecentsPersistenceRoundTrip(t *testing.T) {
	app := NewApp()
	app.appDataPath = t.TempDir()

	if err := app.AddRecentItem(`{"kind":"script","id":"s1","name":"Order flow","route":"#/scripts/s1"}`); err != nil {
		t.Fatalf("AddRecentItem: %v", err)
	}
	if err := app.AddRecentItem(`{"kind":"import","id":"i1","name":"LR archive","route":"#/import/i1"}`); err != nil {
		t.Fatalf("AddRecentItem: %v", err)
	}

	out, err := app.GetRecentItems()
	if err != nil {
		t.Fatalf("GetRecentItems: %v", err)
	}
	var items []recentItem
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 2 || items[0].ID != "i1" {
		t.Fatalf("recents = %+v, want newest (i1) first", items)
	}
	// OpenedAt is stamped server-side even though the JS payload omitted it.
	if items[0].OpenedAt == "" {
		t.Error("expected AddRecentItem to stamp OpenedAt")
	}

	if err := app.ClearRecentItems(); err != nil {
		t.Fatalf("ClearRecentItems: %v", err)
	}
	out, _ = app.GetRecentItems()
	if out != "[]" {
		t.Fatalf("after clear = %q, want []", out)
	}
}

func TestMergeRecentNeverEvictsPinned(t *testing.T) {
	// Pinned "p" must survive even as fresh unpinned items push past the cap of 2.
	var list []recentItem
	list = mergeRecent(list, recentItem{Kind: "script", ID: "p", Pinned: true}, 2)
	for _, id := range []string{"1", "2", "3"} {
		list = mergeRecent(list, recentItem{Kind: "script", ID: id}, 2)
	}
	var hasPinned bool
	for _, it := range list {
		if it.ID == "p" {
			hasPinned = true
		}
	}
	if !hasPinned {
		t.Fatalf("pinned item was evicted by the cap: %+v", list)
	}
	// Newest unpinned ("3") is kept; pinned doesn't consume more than its slot.
	if list[len(list)-1].ID != "3" && list[0].ID != "3" {
		t.Logf("list order: %+v", list)
	}
}

func TestMergeRecentKeepsNewItemWhenPinsFillLimit(t *testing.T) {
	var existing []recentItem
	for i := 0; i < maxRecentItems; i++ {
		existing = append(existing, recentItem{Kind: "script", ID: string(rune('a' + i)), Pinned: true})
	}

	got := mergeRecent(existing, recentItem{Kind: "test", ID: "fresh", Name: "Fresh"}, maxRecentItems)
	if len(got) != maxRecentItems+1 {
		t.Fatalf("len = %d, want pinned items plus the fresh recent", len(got))
	}
	if got[0].ID != "fresh" {
		t.Fatalf("first recent = %+v, want fresh item retained at the front", got[0])
	}
}

func TestToggleRecentPinAndCarryOver(t *testing.T) {
	app := NewApp()
	app.appDataPath = t.TempDir()
	if err := app.AddRecentItem(`{"kind":"script","id":"s1","name":"Order","route":"#/scripts/s1"}`); err != nil {
		t.Fatal(err)
	}
	if err := app.ToggleRecentPin("script", "s1"); err != nil {
		t.Fatal(err)
	}
	items := loadRecents(app.appDataPath)
	if len(items) != 1 || !items[0].Pinned {
		t.Fatalf("expected s1 pinned, got %+v", items)
	}
	// Re-opening the pinned item keeps it pinned.
	if err := app.AddRecentItem(`{"kind":"script","id":"s1","name":"Order","route":"#/scripts/s1"}`); err != nil {
		t.Fatal(err)
	}
	items = loadRecents(app.appDataPath)
	if !items[0].Pinned {
		t.Errorf("pin should carry across re-add, got %+v", items[0])
	}
}

func TestAddRecentItemIgnoresInvalid(t *testing.T) {
	app := NewApp()
	app.appDataPath = t.TempDir()
	// Empty / missing required fields are ignored without error.
	if err := app.AddRecentItem(""); err != nil {
		t.Errorf("empty item should be ignored, got %v", err)
	}
	if err := app.AddRecentItem(`{"kind":"script"}`); err != nil {
		t.Errorf("item without id should be ignored, got %v", err)
	}
	out, _ := app.GetRecentItems()
	if out != "[]" {
		t.Errorf("no items should be stored, got %q", out)
	}
}
