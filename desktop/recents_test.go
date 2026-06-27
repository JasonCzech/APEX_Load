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
