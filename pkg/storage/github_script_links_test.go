package storage

import (
	"path/filepath"
	"testing"
)

func TestGitHubScriptLinkCRUD(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	// Absent link returns (nil, nil).
	got, err := store.GetGitHubScriptLink("script-1")
	if err != nil || got != nil {
		t.Fatalf("expected (nil,nil) for absent link, got (%v, %v)", got, err)
	}

	link := &GitHubScriptLink{
		ScriptID:      "script-1",
		RefKey:        "S-7",
		Owner:         "octocat",
		Repo:          "scripts",
		Branch:        "main",
		Path:          "apexload/scripts/order-flow.json",
		LastCommitSHA: "blob1",
		ContentHash:   "hash1",
	}
	if err := store.SaveGitHubScriptLink(link); err != nil {
		t.Fatalf("SaveGitHubScriptLink: %v", err)
	}
	if link.SyncedAt.IsZero() || link.UpdatedAt.IsZero() {
		t.Fatal("Save should stamp synced_at/updated_at")
	}

	got, err = store.GetGitHubScriptLink("script-1")
	if err != nil {
		t.Fatalf("GetGitHubScriptLink: %v", err)
	}
	if got == nil || got.Owner != "octocat" || got.LastCommitSHA != "blob1" || got.ContentHash != "hash1" {
		t.Fatalf("unexpected link: %#v", got)
	}

	// Upsert updates in place.
	link.LastCommitSHA = "blob2"
	link.ContentHash = "hash2"
	link.Branch = "dev"
	if err := store.SaveGitHubScriptLink(link); err != nil {
		t.Fatalf("re-SaveGitHubScriptLink: %v", err)
	}
	got, _ = store.GetGitHubScriptLink("script-1")
	if got.LastCommitSHA != "blob2" || got.ContentHash != "hash2" || got.Branch != "dev" {
		t.Fatalf("upsert did not update: %#v", got)
	}

	if err := store.DeleteGitHubScriptLink("script-1"); err != nil {
		t.Fatalf("DeleteGitHubScriptLink: %v", err)
	}
	got, _ = store.GetGitHubScriptLink("script-1")
	if got != nil {
		t.Fatalf("expected nil after delete, got %#v", got)
	}
}
