package storage

import (
	"path/filepath"
	"testing"
	"time"
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
		ScriptID:        "script-1",
		RefKey:          "S-7",
		Owner:           "octocat",
		Repo:            "scripts",
		Branch:          "main",
		Path:            "apexload/scripts/order-flow.json",
		LastCommitSHA:   "blob1",
		RemoteBlobSHA:   "blob1",
		RemoteCommitSHA: "commit1",
		ContentHash:     "hash1",
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
	if got == nil || got.Owner != "octocat" || got.LastCommitSHA != "blob1" || got.RemoteBlobSHA != "blob1" || got.RemoteCommitSHA != "commit1" || got.ContentHash != "hash1" {
		t.Fatalf("unexpected link: %#v", got)
	}

	// Upsert updates in place.
	link.LastCommitSHA = "blob2"
	link.RemoteBlobSHA = "blob2"
	link.RemoteCommitSHA = "commit2"
	link.ContentHash = "hash2"
	link.Branch = "dev"
	if err := store.SaveGitHubScriptLink(link); err != nil {
		t.Fatalf("re-SaveGitHubScriptLink: %v", err)
	}
	got, _ = store.GetGitHubScriptLink("script-1")
	if got.LastCommitSHA != "blob2" || got.RemoteBlobSHA != "blob2" || got.RemoteCommitSHA != "commit2" || got.ContentHash != "hash2" || got.Branch != "dev" {
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

func TestGitHubEnterpriseStoreCRUD(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	suspended := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if err := store.SaveGitHubAppInstallation(&GitHubAppInstallation{
		InstallationID:      42,
		AccountLogin:        "octo-org",
		AccountType:         "Organization",
		RepositorySelection: "selected",
		PermissionsJSON:     `{"contents":"write"}`,
		EventsJSON:          `["push"]`,
		HTMLURL:             "https://ghe/octo-org/settings/installations/42",
		SuspendedAt:         &suspended,
	}); err != nil {
		t.Fatalf("SaveGitHubAppInstallation: %v", err)
	}
	installations, err := store.ListGitHubAppInstallations()
	if err != nil || len(installations) != 1 || installations[0].InstallationID != 42 {
		t.Fatalf("ListGitHubAppInstallations = (%#v, %v)", installations, err)
	}

	completed := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveGitHubSyncJob(&GitHubSyncJob{
		ID: "job-1", ProjectID: "project-a", Kind: "import", Status: "complete",
		Owner: "octo", Repo: "scripts", Branch: "main", Path: "apexload/scripts",
		Total: 2, Processed: 2, Created: 1, Updated: 1, SummaryJSON: `{"ok":true}`, CompletedAt: &completed,
	}); err != nil {
		t.Fatalf("SaveGitHubSyncJob: %v", err)
	}
	jobs, err := store.ListGitHubSyncJobs("project-a", 10)
	if err != nil || len(jobs) != 1 || jobs[0].Processed != 2 {
		t.Fatalf("ListGitHubSyncJobs = (%#v, %v)", jobs, err)
	}

	if err := store.SaveGitHubWebhookDelivery(&GitHubWebhookDelivery{
		DeliveryID: "delivery-1", Event: "push", Owner: "octo", Repo: "scripts", Ref: "refs/heads/main", CommitSHA: "commit1", Status: "processed",
	}); err != nil {
		t.Fatalf("SaveGitHubWebhookDelivery: %v", err)
	}
	delivery, err := store.GetGitHubWebhookDelivery("delivery-1")
	if err != nil || delivery == nil || delivery.CommitSHA != "commit1" {
		t.Fatalf("GetGitHubWebhookDelivery = (%#v, %v)", delivery, err)
	}

	if err := store.SaveGitHubCheckLink(&GitHubCheckLink{
		ID: "check-1", TestID: "test-1", ScriptID: "script-1", Owner: "octo", Repo: "scripts", CommitSHA: "commit1",
		CheckRunID: 99, CheckRunURL: "https://ghe/octo/scripts/runs/99", Status: "completed", Conclusion: "success",
	}); err != nil {
		t.Fatalf("SaveGitHubCheckLink: %v", err)
	}
	checks, err := store.ListGitHubCheckLinks("test-1", "script-1", 10)
	if err != nil || len(checks) != 1 || checks[0].CheckRunID != 99 {
		t.Fatalf("ListGitHubCheckLinks = (%#v, %v)", checks, err)
	}
}
