package storage

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
	ciCheckID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:test-1")).String()
	if err := store.SaveGitHubCheckLink(&GitHubCheckLink{
		ID: ciCheckID, Kind: "ci", TestID: "test-1", Owner: "octo", Repo: "scripts", CommitSHA: "commit2",
		CheckRunID: 100, CheckRunURL: "https://ghe/octo/scripts/runs/100", Status: "in_progress",
	}); err != nil {
		t.Fatalf("Save incomplete GitHubCheckLink: %v", err)
	}
	incomplete, err := store.ListIncompleteGitHubCheckLinks(10)
	if err != nil || len(incomplete) != 1 || incomplete[0].ID != ciCheckID {
		t.Fatalf("ListIncompleteGitHubCheckLinks = (%#v, %v)", incomplete, err)
	}
}

func TestGitHubCheckKindMigrationRecoversLegacyCIBacklog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	requireGitHubAnchors(t, store, "project-ci-kind", []string{"manual-script"}, []string{"ci-test", "manual-test"})
	if _, err := store.db.Exec(`UPDATE tests SET total_vusers = 1, duration_ns = 0, error = '', groups_json = '[]' WHERE id IN (?, ?)`, "ci-test", "manual-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE tests SET materialization_key = ? WHERE id = ?`, "ci:v1:legacy-metadata", "ci-test"); err != nil {
		t.Fatal(err)
	}
	legacyCIID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:ci-test")).String()
	for _, link := range []*GitHubCheckLink{
		{ID: legacyCIID, Kind: "ci", TestID: "ci-test", Owner: "octo", Repo: "apex", CheckRunID: 10, Status: "in_progress"},
		{ID: "legacy-manual", Kind: "manual", TestID: "manual-test", ScriptID: "manual-script", Owner: "octo", Repo: "apex", CheckRunID: 11, Status: "in_progress"},
		{ID: "legacy-manual-on-ci", Kind: "manual", TestID: "ci-test", Owner: "octo", Repo: "apex", CheckRunID: 12, Status: "in_progress"},
	} {
		if err := store.SaveGitHubCheckLink(link); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`UPDATE github_check_links SET kind = NULL`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ciLink, err := store.GetGitHubCheckLink(legacyCIID)
	if err != nil || ciLink == nil || ciLink.Kind != "ci" {
		t.Fatalf("legacy CI link kind = %#v, %v", ciLink, err)
	}
	manualLink, err := store.GetGitHubCheckLink("legacy-manual")
	if err != nil || manualLink == nil || manualLink.Kind != "manual" {
		t.Fatalf("legacy manual link kind = %#v, %v", manualLink, err)
	}
	ambiguousManual, err := store.GetGitHubCheckLink("legacy-manual-on-ci")
	if err != nil || ambiguousManual == nil || ambiguousManual.Kind != "manual" {
		t.Fatalf("legacy manual link on CI test kind = %#v, %v", ambiguousManual, err)
	}
	backlog, err := store.ListIncompleteGitHubCheckLinks(10)
	if err != nil || len(backlog) != 1 || backlog[0].ID != legacyCIID {
		t.Fatalf("legacy CI reconciliation backlog = %#v, %v", backlog, err)
	}
}

func TestIncompleteGitHubCheckBacklogIsTerminalOnlyAndRotatesRetries(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "project-check-queue", []string{"manual-script"}, []string{"terminal-a", "terminal-b", "running", "manual"})
	if _, err := store.db.Exec(`UPDATE tests SET status = 'RUNNING' WHERE id = ?`, "running"); err != nil {
		t.Fatal(err)
	}
	linkA := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:terminal-a")).String()
	linkB := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:terminal-b")).String()
	linkRunning := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:running")).String()
	for _, link := range []*GitHubCheckLink{
		{ID: linkA, Kind: "ci", TestID: "terminal-a", Owner: "octo", Repo: "apex", CheckRunID: 1, Status: "in_progress"},
		{ID: linkB, Kind: "ci", TestID: "terminal-b", Owner: "octo", Repo: "apex", CheckRunID: 2, Status: "in_progress"},
		{ID: linkRunning, Kind: "ci", TestID: "running", Owner: "octo", Repo: "apex", CheckRunID: 3, Status: "in_progress"},
		{ID: "link-manual", Kind: "manual", TestID: "manual", ScriptID: "manual-script", Owner: "octo", Repo: "apex", CheckRunID: 4, Status: "in_progress"},
	} {
		if err := store.SaveGitHubCheckLink(link); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i, id := range []string{linkA, linkB, linkRunning, "link-manual"} {
		if _, err := store.db.Exec(`UPDATE github_check_links SET updated_at = ? WHERE id = ?`, base.Add(time.Duration(i)*time.Minute), id); err != nil {
			t.Fatal(err)
		}
	}
	backlog, err := store.ListIncompleteGitHubCheckLinks(1)
	if err != nil || len(backlog) != 1 || backlog[0].ID != linkA {
		t.Fatalf("initial terminal Check backlog = %#v, %v", backlog, err)
	}
	rotatedAt := time.Now().UTC()
	if err := store.TouchGitHubCheckLink(linkA, rotatedAt); err != nil {
		t.Fatal(err)
	}
	backlog, err = store.ListIncompleteGitHubCheckLinks(1)
	if err != nil || len(backlog) != 1 || backlog[0].ID != linkB {
		t.Fatalf("rotated terminal Check backlog = %#v, %v", backlog, err)
	}
	rotated, err := store.GetGitHubCheckLink(linkA)
	if err != nil || rotated == nil || rotated.Status != "in_progress" || rotated.Conclusion != "" || !rotated.UpdatedAt.Equal(rotatedAt) {
		t.Fatalf("rotated Check state = %#v, %v", rotated, err)
	}
}

func TestGitHubCheckIntentAndTerminalClaimSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-check-outbox.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	requireProjectFixtures(t, store, "project-checks")
	intent := &GitHubCheckIntent{
		TestID: "future-test", ProjectID: "project-checks", InstallationID: 42,
		Owner: "octo", Repo: "apex", CommitSHA: "0123456789abcdef0123456789abcdef01234567",
	}
	if err := store.SaveGitHubCheckIntent(intent); err != nil {
		t.Fatalf("SaveGitHubCheckIntent before test creation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recovered, err := store.GetGitHubCheckIntent(intent.TestID)
	if err != nil || recovered == nil || recovered.ProjectID != intent.ProjectID || recovered.InstallationID != 42 {
		t.Fatalf("reopened GitHub Check intent = %#v, %v", recovered, err)
	}
	forged := *recovered
	forged.CommitSHA = "ffffffffffffffffffffffffffffffffffffffff"
	if err := store.SaveGitHubCheckIntent(&forged); err == nil {
		t.Fatal("SaveGitHubCheckIntent changed immutable CI metadata")
	}
	const claimant = "controller-a"
	creationClaimed, err := store.ClaimGitHubCheckCreation(intent.TestID, claimant, time.Minute)
	if err != nil || !creationClaimed {
		t.Fatalf("first creation claim = %t, %v", creationClaimed, err)
	}
	creationClaimed, err = store.ClaimGitHubCheckCreation(intent.TestID, "controller-b", time.Minute)
	if err != nil || creationClaimed {
		t.Fatalf("competing creation claim = %t, %v", creationClaimed, err)
	}
	if err := store.ReleaseGitHubCheckCreation(intent.TestID, "controller-b"); err != nil {
		t.Fatal(err)
	}
	creationClaimed, err = store.ClaimGitHubCheckCreation(intent.TestID, "controller-b", time.Minute)
	if err != nil || creationClaimed {
		t.Fatalf("foreign release cleared creation claim: claimed=%t err=%v", creationClaimed, err)
	}
	if err := store.ReleaseGitHubCheckCreation(intent.TestID, claimant); err != nil {
		t.Fatal(err)
	}
	creationClaimed, err = store.ClaimGitHubCheckCreation(intent.TestID, "controller-b", time.Minute)
	if err != nil || !creationClaimed {
		t.Fatalf("claim after owner release = %t, %v", creationClaimed, err)
	}
	if err := store.ReleaseGitHubCheckCreation(intent.TestID, "controller-b"); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO tests (id, name, status, project_id, start_time) VALUES (?, ?, ?, ?, ?)`, intent.TestID, "future-test", "RUNNING", intent.ProjectID, now); err != nil {
		t.Fatal(err)
	}
	link := &GitHubCheckLink{
		ID: "durable-check", TestID: intent.TestID, Owner: intent.Owner, Repo: intent.Repo, CommitSHA: intent.CommitSHA,
		CheckRunID: 99, CheckRunURL: "https://github.example/octo/apex/runs/99", Status: "in_progress",
	}
	if err := store.SaveGitHubCheckLink(link); err != nil {
		t.Fatal(err)
	}

	// Competing controller replicas may derive different outcomes. The first
	// durable claim wins and every caller observes the same conclusion.
	conclusions := []string{"success", "failure", "cancelled", "failure"}
	claimed := make([]string, len(conclusions))
	errs := make([]error, len(conclusions))
	var wg sync.WaitGroup
	for i, conclusion := range conclusions {
		wg.Add(1)
		go func(i int, conclusion string) {
			defer wg.Done()
			result, claimErr := store.ClaimGitHubCheckConclusion(link.ID, conclusion)
			errs[i] = claimErr
			if result != nil {
				claimed[i] = result.PendingConclusion
			}
		}(i, conclusion)
	}
	wg.Wait()
	winner := claimed[0]
	if winner == "" {
		t.Fatalf("first claim result = %q, err=%v", claimed[0], errs[0])
	}
	for i := range claimed {
		if errs[i] != nil || claimed[i] != winner {
			t.Fatalf("terminal claims = %#v, errors=%#v", claimed, errs)
		}
	}

	claimedLink, err := store.GetGitHubCheckLink(link.ID)
	if err != nil || claimedLink.PendingConclusion != winner {
		t.Fatalf("durable terminal claim = %#v, %v", claimedLink, err)
	}
	claimedLink.Status = "completed"
	claimedLink.Conclusion = winner
	claimedLink.PendingConclusion = ""
	if err := store.SaveGitHubCheckLink(claimedLink); err != nil {
		t.Fatalf("complete claimed Check: %v", err)
	}
	conflict := *claimedLink
	conflict.Status = "in_progress"
	conflict.Conclusion = ""
	if err := store.SaveGitHubCheckLink(&conflict); err == nil {
		t.Fatal("SaveGitHubCheckLink reopened an immutable terminal Check")
	}
	conflict = *claimedLink
	if winner == "success" {
		conflict.Conclusion = "failure"
	} else {
		conflict.Conclusion = "success"
	}
	if err := store.SaveGitHubCheckLink(&conflict); err == nil {
		t.Fatal("SaveGitHubCheckLink changed an immutable terminal conclusion")
	}
	final, err := store.GetGitHubCheckLink(link.ID)
	if err != nil || final.Status != "completed" || final.Conclusion != winner {
		t.Fatalf("final immutable Check = %#v, %v", final, err)
	}
	if err := store.DeleteTest(intent.TestID); err != nil {
		t.Fatal(err)
	}
	if remaining, err := store.GetGitHubCheckIntent(intent.TestID); err != nil || remaining != nil {
		t.Fatalf("test deletion left GitHub Check intent = %#v, %v", remaining, err)
	}
	if err := store.TouchGitHubCheckIntent(intent.TestID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if remaining, err := store.GetGitHubCheckIntent(intent.TestID); err != nil || remaining != nil {
		t.Fatalf("retry rotation recreated a deleted GitHub Check intent = %#v, %v", remaining, err)
	}
}

func TestGitHubCheckCreationClaimRenewalAndOwnedCommit(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-check-renewal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-checks")

	intent := &GitHubCheckIntent{
		TestID: "renewed-test", ProjectID: "project-checks", InstallationID: 42,
		Owner: "octo", Repo: "apex", CommitSHA: "0123456789abcdef0123456789abcdef01234567",
	}
	if err := store.SaveGitHubCheckIntent(intent); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO tests (id, name, status, project_id, start_time) VALUES (?, ?, ?, ?, ?)`, intent.TestID, "renewed-test", "QUEUED", intent.ProjectID, now); err != nil {
		t.Fatal(err)
	}

	const claimant = "controller-a"
	lease := time.Minute
	if claimed, err := store.ClaimGitHubCheckCreation(intent.TestID, claimant, lease); err != nil || !claimed {
		t.Fatalf("initial creation claim = %t, %v", claimed, err)
	}
	if renewed, err := store.RenewGitHubCheckCreation(intent.TestID, "controller-b", lease); err != nil || renewed {
		t.Fatalf("foreign renewal = %t, %v", renewed, err)
	}
	if renewed, err := store.RenewGitHubCheckCreation(intent.TestID, claimant, lease); err != nil || !renewed {
		t.Fatalf("owner renewal = %t, %v", renewed, err)
	}
	if claimed, err := store.ClaimGitHubCheckCreation(intent.TestID, "controller-b", lease); err != nil || claimed {
		t.Fatalf("competitor stole renewed claim = %t, %v", claimed, err)
	}

	link := &GitHubCheckLink{
		ID:   uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+intent.TestID)).String(),
		Kind: "ci", TestID: intent.TestID, Owner: intent.Owner, Repo: intent.Repo, CommitSHA: intent.CommitSHA,
		CheckRunID: 99, CheckRunURL: "https://github.example/octo/apex/runs/99", Status: "in_progress",
	}
	if saved, err := store.SaveGitHubCheckLinkIfCreationClaimHeld(link, "controller-b"); err != nil || saved {
		t.Fatalf("foreign claimed commit = %t, %v", saved, err)
	}
	if stored, err := store.GetGitHubCheckLink(link.ID); err != nil || stored != nil {
		t.Fatalf("foreign claimant persisted Check = %#v, %v", stored, err)
	}
	if saved, err := store.SaveGitHubCheckLinkIfCreationClaimHeld(link, claimant); err != nil || !saved {
		t.Fatalf("owner claimed commit = %t, %v", saved, err)
	}

	expired := &GitHubCheckIntent{
		TestID: "expired-test", ProjectID: "project-checks", InstallationID: 42,
		Owner: "octo", Repo: "apex", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := store.SaveGitHubCheckIntent(expired); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO tests (id, name, status, project_id, start_time) VALUES (?, ?, ?, ?, ?)`, expired.TestID, "expired-test", "QUEUED", expired.ProjectID, now); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimGitHubCheckCreation(expired.TestID, claimant, lease); err != nil || !claimed {
		t.Fatalf("expiring claim = %t, %v", claimed, err)
	}
	if _, err := store.db.Exec(`UPDATE github_check_intents SET creation_claim_expires_at = ? WHERE test_id = ?`, time.Unix(0, 0).UTC(), expired.TestID); err != nil {
		t.Fatal(err)
	}
	expiredLink := &GitHubCheckLink{
		ID:   uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+expired.TestID)).String(),
		Kind: "ci", TestID: expired.TestID, Owner: expired.Owner, Repo: expired.Repo, CommitSHA: expired.CommitSHA,
		CheckRunID: 100, CheckRunURL: "https://github.example/octo/apex/runs/100", Status: "in_progress",
	}
	if saved, err := store.SaveGitHubCheckLinkIfCreationClaimHeld(expiredLink, claimant); err != nil || saved {
		t.Fatalf("expired claimed commit = %t, %v", saved, err)
	}
	if renewed, err := store.RenewGitHubCheckCreation(expired.TestID, claimant, time.Minute); err != nil || renewed {
		t.Fatalf("expired renewal = %t, %v", renewed, err)
	}
	if claimed, err := store.ClaimGitHubCheckCreation(expired.TestID, "controller-b", time.Minute); err != nil || !claimed {
		t.Fatalf("claim after expiry = %t, %v", claimed, err)
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
	if err := store.SaveGitHubCheckLink(&GitHubCheckLink{ID: "noncanonical-ci-check", Kind: "ci", TestID: "test-a"}); err == nil {
		t.Fatal("SaveGitHubCheckLink accepted a noncanonical controller CI link")
	}
	if err := store.SaveGitHubCheckLink(&GitHubCheckLink{ID: "unknown-check-kind", Kind: "unknown", TestID: "test-a"}); err == nil {
		t.Fatal("SaveGitHubCheckLink accepted an unknown kind")
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
