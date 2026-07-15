package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func requireGitHubAnchors(t *testing.T, store *SQLiteStore, projectID string, scriptIDs, testIDs []string) {
	t.Helper()
	requireProjectFixtures(t, store, projectID)
	now := time.Now().UTC()
	for _, scriptID := range scriptIDs {
		if _, err := store.db.Exec(`INSERT INTO scripts (id, name, protocol, project_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, scriptID, scriptID, "http", projectID, now, now); err != nil {
			t.Fatalf("seed script %s: %v", scriptID, err)
		}
	}
	for _, testID := range testIDs {
		if _, err := store.db.Exec(`INSERT INTO tests (id, name, status, project_id, start_time) VALUES (?, ?, ?, ?, ?)`, testID, testID, "COMPLETE", projectID, now); err != nil {
			t.Fatalf("seed test %s: %v", testID, err)
		}
	}
}

func TestGitHubScriptLinkCRUD(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-1"}, nil)

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

func TestGitHubWebhookCommitUpdateDoesNotClobberConcurrentLinkChanges(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-race"}, nil)

	link := &GitHubScriptLink{
		ScriptID: "script-race", RefKey: "S-1", Owner: "Octo", Repo: "Scripts", Branch: "main",
		Path: "old.json", LastCommitSHA: "blob-old", RemoteBlobSHA: "blob-old",
		RemoteCommitSHA: "commit-old", ContentHash: "hash-old",
	}
	if err := store.SaveGitHubScriptLink(link); err != nil {
		t.Fatal(err)
	}

	// Simulate a push committing new link metadata after a webhook worker's
	// conceptual read. The targeted webhook update must only change the remote
	// branch head and preserve every field written by the push.
	link.Path = "new.json"
	link.LastCommitSHA = "blob-new"
	link.RemoteBlobSHA = "blob-new"
	link.RemoteCommitSHA = "commit-push"
	link.ContentHash = "hash-new"
	if err := store.SaveGitHubScriptLink(link); err != nil {
		t.Fatal(err)
	}

	updated, err := store.UpdateGitHubScriptLinksRemoteCommit("octo", "scripts", "main", "commit-webhook", time.Now().UTC())
	if err != nil || updated != 1 {
		t.Fatalf("UpdateGitHubScriptLinksRemoteCommit = %d, %v; want 1, nil", updated, err)
	}
	got, err := store.GetGitHubScriptLink(link.ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "new.json" || got.LastCommitSHA != "blob-new" || got.RemoteBlobSHA != "blob-new" || got.ContentHash != "hash-new" {
		t.Fatalf("targeted webhook update clobbered concurrent link metadata: %#v", got)
	}
	if got.RemoteCommitSHA != "commit-webhook" {
		t.Fatalf("remote commit = %q, want commit-webhook", got.RemoteCommitSHA)
	}
}

func TestGitHubEnterpriseStoreCRUD(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "project-a", []string{"script-1"}, []string{"test-1"})

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

func TestGitHubOwnedRowsRejectMissingOrRelocatedAnchors(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "project-a", []string{"script-a"}, []string{"test-a"})
	requireGitHubAnchors(t, store, "project-b", []string{"script-b"}, []string{"test-b"})

	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{ScriptID: "missing-script", Owner: "octo", Repo: "scripts"}); err == nil {
		t.Fatal("SaveGitHubScriptLink accepted a missing script")
	}
	job := &GitHubSyncJob{ID: "job-owned", ProjectID: "project-a", Kind: "push", Status: "running"}
	if err := store.SaveGitHubSyncJob(job); err != nil {
		t.Fatal(err)
	}
	job.ProjectID = "project-b"
	job.Status = "forged"
	if err := store.SaveGitHubSyncJob(job); err == nil {
		t.Fatal("SaveGitHubSyncJob relocated an existing job")
	}
	storedJob, err := store.GetGitHubSyncJob(job.ID)
	if err != nil || storedJob.ProjectID != "project-a" || storedJob.Status != "running" {
		t.Fatalf("sync job changed through foreign scope: %#v, err=%v", storedJob, err)
	}
	if err := store.SaveGitHubSyncJob(&GitHubSyncJob{ID: "missing-project-job", ProjectID: "missing", Kind: "push"}); err == nil {
		t.Fatal("SaveGitHubSyncJob accepted a missing project")
	}

	check := &GitHubCheckLink{ID: "check-owned", TestID: "test-a", ScriptID: "script-a", Owner: "octo", Repo: "scripts"}
	if err := store.SaveGitHubCheckLink(check); err != nil {
		t.Fatal(err)
	}
	check.TestID = "test-b"
	check.ScriptID = "script-b"
	if err := store.SaveGitHubCheckLink(check); err == nil {
		t.Fatal("SaveGitHubCheckLink relocated existing anchors")
	}
	if err := store.SaveGitHubCheckLink(&GitHubCheckLink{ID: "cross-project-check", TestID: "test-a", ScriptID: "script-b"}); err == nil {
		t.Fatal("SaveGitHubCheckLink accepted cross-project anchors")
	}
	if err := store.SaveGitHubCheckLink(&GitHubCheckLink{ID: "missing-check", TestID: "missing-test"}); err == nil {
		t.Fatal("SaveGitHubCheckLink accepted a missing test")
	}

	if err := store.DeleteTest("test-a"); err != nil {
		t.Fatal(err)
	}
	storedCheck, err := store.GetGitHubCheckLink(check.ID)
	if err != nil || storedCheck != nil {
		t.Fatalf("test deletion left github check link %#v, err=%v", storedCheck, err)
	}
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{ScriptID: "script-a", Owner: "octo", Repo: "scripts"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteScript("script-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{ScriptID: "script-a", Owner: "octo", Repo: "scripts"}); err == nil {
		t.Fatal("SaveGitHubScriptLink recreated a link after script deletion")
	}
	storedScriptLink, err := store.GetGitHubScriptLink("script-a")
	if err != nil || storedScriptLink != nil {
		t.Fatalf("script deletion left/recreated github link %#v, err=%v", storedScriptLink, err)
	}
}
