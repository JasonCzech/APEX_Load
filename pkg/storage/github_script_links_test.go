package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
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

func githubSyncTestTargets(branch, path string) []GitHubScriptSyncTarget {
	return []GitHubScriptSyncTarget{{Owner: "octo", Repo: "scripts", Branch: branch, Path: path}}
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
		BaseBranch:      "main",
		BaseMissing:     true,
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
	if got == nil || got.Owner != "octocat" || got.LastCommitSHA != "blob1" || got.RemoteBlobSHA != "blob1" || got.RemoteCommitSHA != "commit1" ||
		got.BaseBranch != "main" || got.BaseBlobSHA != "" || !got.BaseMissing || got.ContentHash != "hash1" {
		t.Fatalf("unexpected link: %#v", got)
	}

	// Upsert updates in place.
	link.LastCommitSHA = "blob2"
	link.RemoteBlobSHA = "blob2"
	link.RemoteCommitSHA = "commit2"
	link.BaseBranch = "release"
	link.BaseBlobSHA = "base-blob2"
	link.BaseMissing = false
	link.ContentHash = "hash2"
	link.Branch = "dev"
	if err := store.SaveGitHubScriptLink(link); err != nil {
		t.Fatalf("re-SaveGitHubScriptLink: %v", err)
	}
	got, _ = store.GetGitHubScriptLink("script-1")
	if got.LastCommitSHA != "blob2" || got.RemoteBlobSHA != "blob2" || got.RemoteCommitSHA != "commit2" ||
		got.BaseBranch != "release" || got.BaseBlobSHA != "base-blob2" || got.BaseMissing ||
		got.ContentHash != "hash2" || got.Branch != "dev" {
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

func TestGitHubScriptLinkBaseWitnessColumnsMigrate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	requireGitHubAnchors(t, store, "github-project", []string{"script-legacy"}, nil)
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{
		ScriptID: "script-legacy",
		Owner:    "octocat",
		Repo:     "scripts",
		Branch:   "feature",
		Path:     "script.json",
	}); err != nil {
		t.Fatalf("seed legacy link: %v", err)
	}
	for _, column := range []string{"base_branch", "base_blob_sha", "base_missing"} {
		if _, err := store.db.Exec(`ALTER TABLE github_script_links DROP COLUMN ` + column); err != nil {
			_ = store.Close()
			t.Fatalf("drop %s to simulate legacy schema: %v", column, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetGitHubScriptLink("script-legacy")
	if err != nil {
		t.Fatalf("GetGitHubScriptLink after migration: %v", err)
	}
	if got == nil || got.BaseBranch != "" || got.BaseBlobSHA != "" || got.BaseMissing {
		t.Fatalf("migrated base witness defaults = %#v", got)
	}
}

func TestGitHubScriptSyncClaimLifecycleAndClaimedFinalize(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-sync-claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-claim"}, nil)

	initialExpiry := time.Now().UTC().Add(5 * time.Minute)
	targets := githubSyncTestTargets("main", "script.json")
	claimed, err := store.AcquireGitHubScriptSyncClaim("script-claim", "claim-a", targets, initialExpiry)
	if err != nil || !claimed {
		t.Fatalf("initial claim = %t, %v", claimed, err)
	}
	var persisted GitHubScriptSyncClaim
	if err := store.db.QueryRow(`SELECT script_id, project_id, claim_id, expires_at, created_at, updated_at
		FROM github_script_sync_claims WHERE script_id = ?`, "script-claim").Scan(
		&persisted.ScriptID, &persisted.ProjectID, &persisted.ClaimID, &persisted.ExpiresAt, &persisted.CreatedAt, &persisted.UpdatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if persisted.ProjectID != "github-project" || persisted.ClaimID != "claim-a" || persisted.CreatedAt.IsZero() || persisted.UpdatedAt.IsZero() {
		t.Fatalf("persisted claim = %#v", persisted)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-claim", "claim-a", targets, initialExpiry.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("active claim was reacquired by its token: claimed=%t err=%v", claimed, err)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-claim", "claim-b", targets, initialExpiry.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("active claim was stolen: claimed=%t err=%v", claimed, err)
	}
	if renewed, err := store.RenewGitHubScriptSyncClaim("script-claim", "claim-b", initialExpiry.Add(10*time.Minute)); err != nil || renewed {
		t.Fatalf("foreign renewal = %t, %v", renewed, err)
	}
	renewedExpiry := initialExpiry.Add(10 * time.Minute)
	if renewed, err := store.RenewGitHubScriptSyncClaim("script-claim", "claim-a", renewedExpiry); err != nil || !renewed {
		t.Fatalf("owner renewal = %t, %v", renewed, err)
	}
	var gotExpiry time.Time
	if err := store.db.QueryRow(`SELECT expires_at FROM github_script_sync_claims WHERE script_id = ?`, "script-claim").Scan(&gotExpiry); err != nil {
		t.Fatal(err)
	}
	if gotExpiry.Before(renewedExpiry.Add(-time.Millisecond)) {
		t.Fatalf("renewed expiry = %s, want at least %s", gotExpiry, renewedExpiry)
	}

	link := &GitHubScriptLink{
		ScriptID: "script-claim", RefKey: "S-claim", Owner: "octo", Repo: "scripts",
		Branch: "main", Path: "script.json", RemoteBlobSHA: "blob-a", RemoteCommitSHA: "commit-a", ContentHash: "hash-a",
	}
	bypassLink := *link
	if err := store.SaveGitHubScriptLink(&bypassLink); err == nil {
		t.Fatal("unclaimed link save bypassed an active sync claim")
	}
	if finalized, err := store.FinalizeGitHubScriptSyncClaim(link, "claim-b"); err != nil || finalized {
		t.Fatalf("foreign finalization = %t, %v", finalized, err)
	}
	if got, err := store.GetGitHubScriptLink("script-claim"); err != nil || got != nil {
		t.Fatalf("foreign finalization persisted link = %#v, %v", got, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-claim", "claim-b"); err != nil || released {
		t.Fatalf("foreign release = %t, %v", released, err)
	}
	if finalized, err := store.FinalizeGitHubScriptSyncClaim(link, "claim-a"); err != nil || !finalized {
		t.Fatalf("owner finalization = %t, %v", finalized, err)
	}
	got, err := store.GetGitHubScriptLink("script-claim")
	if err != nil || got == nil || got.Path != "script.json" || got.RemoteBlobSHA != "blob-a" || got.ContentHash != "hash-a" {
		t.Fatalf("finalized link = %#v, %v", got, err)
	}
	var remaining int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM github_script_sync_claims WHERE script_id = ?`, "script-claim").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("successful finalization left %d claims", remaining)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-claim", "claim-b", targets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("claim after finalization = %t, %v", claimed, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-claim", "claim-a"); err != nil || released {
		t.Fatalf("stale release removed replacement claim: released=%t err=%v", released, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-claim", "claim-b"); err != nil || !released {
		t.Fatalf("owner release = %t, %v", released, err)
	}
}

func TestGitHubScriptSyncClaimExpiryFencesStaleWorker(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-sync-expiry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-expiry"}, nil)

	oldTargets := githubSyncTestTargets("old", "old.json")
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-expiry", "claim-old", oldTargets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("initial claim = %t, %v", claimed, err)
	}
	if _, err := store.db.Exec(`UPDATE github_script_sync_claims SET expires_at = ? WHERE script_id = ?`, time.Unix(0, 0).UTC(), "script-expiry"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE github_script_sync_target_claims SET expires_at = ? WHERE script_id = ?`, time.Unix(0, 0).UTC(), "script-expiry"); err != nil {
		t.Fatal(err)
	}
	if renewed, err := store.RenewGitHubScriptSyncClaim("script-expiry", "claim-old", time.Now().UTC().Add(time.Minute)); err != nil || renewed {
		t.Fatalf("expired claim renewal = %t, %v", renewed, err)
	}
	newTargets := githubSyncTestTargets("new", "new.json")
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-expiry", "claim-new", newTargets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("expired-claim takeover = %t, %v", claimed, err)
	}
	staleLink := &GitHubScriptLink{ScriptID: "script-expiry", Owner: "octo", Repo: "scripts", Branch: "old", Path: "old.json", RemoteBlobSHA: "old"}
	if finalized, err := store.FinalizeGitHubScriptSyncClaim(staleLink, "claim-old"); err != nil || finalized {
		t.Fatalf("stale finalization = %t, %v", finalized, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-expiry", "claim-old"); err != nil || released {
		t.Fatalf("stale release = %t, %v", released, err)
	}
	winningLink := &GitHubScriptLink{ScriptID: "script-expiry", Owner: "octo", Repo: "scripts", Branch: "new", Path: "new.json", RemoteBlobSHA: "new"}
	if finalized, err := store.FinalizeGitHubScriptSyncClaim(winningLink, "claim-new"); err != nil || !finalized {
		t.Fatalf("replacement finalization = %t, %v", finalized, err)
	}
	got, err := store.GetGitHubScriptLink("script-expiry")
	if err != nil || got == nil || got.Branch != "new" || got.Path != "new.json" || got.RemoteBlobSHA != "new" {
		t.Fatalf("winning link = %#v, %v", got, err)
	}
}

func TestGitHubScriptSyncClaimConcurrentAcquireHasOneWinner(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-sync-concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-concurrent"}, nil)

	type result struct {
		claimID string
		claimed bool
		err     error
	}
	const contenders = 8
	results := make(chan result, contenders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		claimID := uuid.NewString()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := store.AcquireGitHubScriptSyncClaim("script-concurrent", claimID, githubSyncTestTargets("main", "concurrent.json"), time.Now().UTC().Add(time.Minute))
			results <- result{claimID: claimID, claimed: claimed, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := make([]string, 0, 1)
	for result := range results {
		if result.err != nil {
			t.Fatalf("claim %s: %v", result.claimID, result.err)
		}
		if result.claimed {
			winners = append(winners, result.claimID)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("claim winners = %v, want exactly one", winners)
	}
	var persistedClaimID string
	if err := store.db.QueryRow(`SELECT claim_id FROM github_script_sync_claims WHERE script_id = ?`, "script-concurrent").Scan(&persistedClaimID); err != nil {
		t.Fatal(err)
	}
	if persistedClaimID != winners[0] {
		t.Fatalf("persisted winner = %q, returned winner = %q", persistedClaimID, winners[0])
	}
}

func TestGitHubScriptSyncTargetConcurrentCrossScriptAcquireHasOneWinner(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-target-concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-target-a", "script-target-b"}, nil)

	type result struct {
		scriptID string
		claimID  string
		claimed  bool
		err      error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, scriptID := range []string{"script-target-a", "script-target-b"} {
		scriptID := scriptID
		claimID := "claim-" + scriptID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := store.AcquireGitHubScriptSyncClaim(scriptID, claimID,
				[]GitHubScriptSyncTarget{{Owner: " OCTO ", Repo: " Scripts ", Branch: "main", Path: "shared.json"}},
				time.Now().UTC().Add(time.Minute))
			results <- result{scriptID: scriptID, claimID: claimID, claimed: claimed, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := make([]result, 0, 1)
	for result := range results {
		if result.err != nil {
			t.Fatalf("cross-script claim for %s: %v", result.scriptID, result.err)
		}
		if result.claimed {
			winners = append(winners, result)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("cross-script target winners = %#v, want exactly one", winners)
	}
	var ownerNorm, repoNorm string
	if err := store.db.QueryRow(`SELECT owner_norm, repo_norm FROM github_script_sync_target_claims`).Scan(&ownerNorm, &repoNorm); err != nil {
		t.Fatal(err)
	}
	if ownerNorm != "octo" || repoNorm != "scripts" {
		t.Fatalf("normalized target = %s/%s, want octo/scripts", ownerNorm, repoNorm)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim(winners[0].scriptID, winners[0].claimID); err != nil || !released {
		t.Fatalf("release target winner = %t, %v", released, err)
	}
}

func TestGitHubScriptSyncTargetOwnershipSurvivesFinalizeUntilUnlink(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-target-ownership.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-owner-a", "script-owner-b"}, nil)
	targets := []GitHubScriptSyncTarget{{Owner: " Octo ", Repo: " SCRIPTS ", Branch: "main", Path: "shared.json"}}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-owner-a", "claim-owner-a", targets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("owner claim = %t, %v", claimed, err)
	}
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{
		ScriptID: "script-owner-b", Owner: "octo", Repo: "scripts", Branch: "main", Path: "shared.json",
	}); !IsGitHubScriptSyncBusy(err) {
		t.Fatalf("legacy save against active target error = %v, want busy", err)
	}
	if finalized, err := store.FinalizeGitHubScriptSyncClaim(&GitHubScriptLink{
		ScriptID: "script-owner-a", Owner: "OCTO", Repo: "Scripts", Branch: "main", Path: "shared.json",
	}, "claim-owner-a"); err != nil || !finalized {
		t.Fatalf("owner finalization = %t, %v", finalized, err)
	}
	var ownerScriptID, ownerNorm, repoNorm string
	if err := store.db.QueryRow(`SELECT script_id, owner_norm, repo_norm FROM github_script_sync_target_owners`).Scan(&ownerScriptID, &ownerNorm, &repoNorm); err != nil {
		t.Fatal(err)
	}
	if ownerScriptID != "script-owner-a" || ownerNorm != "octo" || repoNorm != "scripts" {
		t.Fatalf("persistent target owner = %s %s/%s", ownerScriptID, ownerNorm, repoNorm)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-owner-a", "claim-owner-a-unlink", targets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("owner unlink-fence claim = %t, %v", claimed, err)
	}
	if err := store.DeleteGitHubScriptLink("script-owner-a"); !IsGitHubScriptSyncBusy(err) {
		t.Fatalf("unlink during active sync error = %v, want busy", err)
	}
	if linked, err := store.GetGitHubScriptLink("script-owner-a"); err != nil || linked == nil {
		t.Fatalf("busy unlink changed persisted link = %#v, %v", linked, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-owner-a", "claim-owner-a-unlink"); err != nil || !released {
		t.Fatalf("release owner unlink-fence claim = %t, %v", released, err)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-owner-b", "claim-owner-b", targets, time.Now().UTC().Add(time.Minute)); err != nil || claimed {
		t.Fatalf("claim of persistently owned target = %t, %v", claimed, err)
	}
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{
		ScriptID: "script-owner-b", Owner: "octo", Repo: "scripts", Branch: "main", Path: "shared.json",
	}); err == nil {
		t.Fatal("legacy save replaced another script's persistent target ownership")
	}
	if err := store.DeleteGitHubScriptLink("script-owner-a"); err != nil {
		t.Fatalf("unlink target owner: %v", err)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-owner-b", "claim-owner-b", targets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("claim after owner unlink = %t, %v", claimed, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-owner-b", "claim-owner-b"); err != nil || !released {
		t.Fatalf("release post-unlink claim = %t, %v", released, err)
	}
}

func TestGitHubScriptSyncPRClaimOwnsBaseAndHeadTargets(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-target-pr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-pr-a", "script-pr-b", "script-pr-mismatch"}, nil)
	prTargets := []GitHubScriptSyncTarget{
		{Owner: "octo", Repo: "scripts", Branch: "main", Path: "pr.json"},
		{Owner: "octo", Repo: "scripts", Branch: "apexload/pr-1", Path: "pr.json"},
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-pr-a", "claim-pr-a", prTargets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("PR claim = %t, %v", claimed, err)
	}
	if finalized, err := store.FinalizeGitHubScriptSyncClaim(&GitHubScriptLink{
		ScriptID: "script-pr-a", Owner: "octo", Repo: "scripts", Branch: "apexload/pr-1", BaseBranch: "main", Path: "pr.json",
	}, "claim-pr-a"); err != nil || !finalized {
		t.Fatalf("PR finalization = %t, %v", finalized, err)
	}
	var ownerCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM github_script_sync_target_owners WHERE script_id = ?`, "script-pr-a").Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 2 {
		t.Fatalf("PR target owners = %d, want base and head", ownerCount)
	}
	for index, target := range prTargets {
		if claimed, err := store.AcquireGitHubScriptSyncClaim("script-pr-b", fmt.Sprintf("claim-pr-b-%d", index),
			[]GitHubScriptSyncTarget{target}, time.Now().UTC().Add(time.Minute)); err != nil || claimed {
			t.Fatalf("claim of PR target %d = %t, %v; want ownership conflict", index, claimed, err)
		}
	}
	mismatchTargets := []GitHubScriptSyncTarget{{Owner: "octo", Repo: "scripts", Branch: "head-only", Path: "mismatch.json"}}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-pr-mismatch", "claim-pr-mismatch", mismatchTargets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("mismatch setup claim = %t, %v", claimed, err)
	}
	if finalized, err := store.FinalizeGitHubScriptSyncClaim(&GitHubScriptLink{
		ScriptID: "script-pr-mismatch", Owner: "octo", Repo: "scripts", Branch: "head-only", BaseBranch: "main", Path: "mismatch.json",
	}, "claim-pr-mismatch"); err != nil || finalized {
		t.Fatalf("finalization with unreserved base = %t, %v", finalized, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-pr-mismatch", "claim-pr-mismatch"); err != nil || !released {
		t.Fatalf("release mismatched PR claim = %t, %v", released, err)
	}
}

func TestGitHubScriptSyncClaimBlocksConditionalScriptDeletionWithoutSideEffects(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-conditional-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireGitHubAnchors(t, store, "github-project", []string{"script-conditional-delete"}, nil)
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{
		ScriptID: "script-conditional-delete", Owner: "octo", Repo: "scripts", Branch: "main", Path: "delete.json",
	}); err != nil {
		t.Fatal(err)
	}
	var expectedUpdatedAt time.Time
	if err := store.db.QueryRow(`SELECT updated_at FROM scripts WHERE id = ?`, "script-conditional-delete").Scan(&expectedUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-conditional-delete", "claim-conditional-delete",
		githubSyncTestTargets("main", "delete.json"), time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("conditional-delete claim = %t, %v", claimed, err)
	}
	if deleted, err := store.DeleteScriptIfUnchanged("script-conditional-delete", expectedUpdatedAt); !IsGitHubScriptSyncBusy(err) || deleted {
		t.Fatalf("conditional delete with active claim = %t, %v; want busy", deleted, err)
	}
	var scripts, links, claims, targetClaims, owners int
	for _, probe := range []struct {
		query string
		dst   *int
	}{
		{`SELECT COUNT(*) FROM scripts WHERE id = ?`, &scripts},
		{`SELECT COUNT(*) FROM github_script_links WHERE script_id = ?`, &links},
		{`SELECT COUNT(*) FROM github_script_sync_claims WHERE script_id = ?`, &claims},
		{`SELECT COUNT(*) FROM github_script_sync_target_claims WHERE script_id = ?`, &targetClaims},
		{`SELECT COUNT(*) FROM github_script_sync_target_owners WHERE script_id = ?`, &owners},
	} {
		if err := store.db.QueryRow(probe.query, "script-conditional-delete").Scan(probe.dst); err != nil {
			t.Fatal(err)
		}
	}
	if scripts != 1 || links != 1 || claims != 1 || targetClaims != 1 || owners != 1 {
		t.Fatalf("busy conditional deletion changed rows: scripts=%d links=%d claims=%d target_claims=%d owners=%d",
			scripts, links, claims, targetClaims, owners)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-conditional-delete", "claim-conditional-delete"); err != nil || !released {
		t.Fatalf("release conditional-delete claim = %t, %v", released, err)
	}
	if deleted, err := store.DeleteScriptIfUnchanged("script-conditional-delete", expectedUpdatedAt); err != nil || !deleted {
		t.Fatalf("conditional delete after release = %t, %v", deleted, err)
	}
	for _, table := range []string{"github_script_links", "github_script_sync_claims", "github_script_sync_target_claims", "github_script_sync_target_owners"} {
		var remaining int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE script_id = ?`, "script-conditional-delete").Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("conditional script deletion left %d rows in %s", remaining, table)
		}
	}
}

func TestCommitGitHubScriptImportIsAtomicFencedAndIdempotent(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-import-atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "github-project")
	if err := store.SaveFolder(&Folder{ID: "local-folder", ProjectID: "github-project", Name: "Local", Category: "general"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	folderID := "local-folder"
	created := &dsl.Script{
		ID: "script-imported", RefKey: "S-LOCAL", Name: "Imported", Protocol: "http", Version: "1",
		ProjectID: "github-project", FolderID: &folderID, CreatedAt: now, UpdatedAt: now,
	}
	link := &GitHubScriptLink{
		ScriptID: "script-imported", Owner: "Octo", Repo: "Scripts", Branch: "main", Path: "imported.json",
		LastCommitSHA: "blob-1", RemoteBlobSHA: "blob-1", RemoteCommitSHA: "commit-1", ContentHash: "hash-1",
	}
	if outcome, err := store.CommitGitHubScriptImport(created, link, time.Time{}); err != nil || outcome != GitHubScriptImportCreated {
		t.Fatalf("atomic github import create = %q, %v", outcome, err)
	}
	persisted, err := store.GetScript("script-imported")
	if err != nil {
		t.Fatal(err)
	}
	firstUpdatedAt := persisted.UpdatedAt
	retry := *persisted
	retryLink := &GitHubScriptLink{
		ScriptID: retry.ID, Owner: "octo", Repo: "scripts", Branch: "main", Path: "imported.json",
		LastCommitSHA: "blob-1", RemoteBlobSHA: "blob-1", RemoteCommitSHA: "commit-1", ContentHash: "hash-1",
	}
	if outcome, err := store.CommitGitHubScriptImport(&retry, retryLink, firstUpdatedAt); err != nil || outcome != GitHubScriptImportUnchanged {
		t.Fatalf("idempotent github import retry = %q, %v", outcome, err)
	}
	if !retry.UpdatedAt.Equal(firstUpdatedAt) {
		t.Fatalf("idempotent retry changed script timestamp from %s to %s", firstUpdatedAt, retry.UpdatedAt)
	}

	maliciousFolder := "remote-folder"
	updated := *persisted
	updated.Name = "Imported v2"
	updated.RefKey = "REMOTE-KEY"
	updated.FolderID = &maliciousFolder
	updated.UpdatedAt = firstUpdatedAt.Add(time.Second)
	updatedLink := &GitHubScriptLink{
		ScriptID: updated.ID, Owner: "octo", Repo: "scripts", Branch: "main", Path: "imported.json",
		LastCommitSHA: "blob-2", RemoteBlobSHA: "blob-2", RemoteCommitSHA: "commit-2", ContentHash: "hash-2",
	}
	if outcome, err := store.CommitGitHubScriptImport(&updated, updatedLink, firstUpdatedAt); err != nil || outcome != GitHubScriptImportUpdated {
		t.Fatalf("atomic github import update = %q, %v", outcome, err)
	}
	persisted, err = store.GetScript("script-imported")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Name != "Imported v2" || persisted.RefKey != "S-LOCAL" || persisted.FolderID == nil || *persisted.FolderID != "local-folder" {
		t.Fatalf("updated import did not preserve local placement: %#v", persisted)
	}

	unlinked := &dsl.Script{ID: "script-unlinked", RefKey: "S-UNLINKED", Name: "Local only", Protocol: "http", Version: "1", ProjectID: "github-project"}
	if err := store.SaveScript(unlinked); err != nil {
		t.Fatal(err)
	}
	unlinkedUpdatedAt := unlinked.UpdatedAt
	forged := *unlinked
	forged.Name = "Remote overwrite"
	forged.UpdatedAt = unlinkedUpdatedAt.Add(time.Second)
	if outcome, err := store.CommitGitHubScriptImport(&forged, &GitHubScriptLink{
		ScriptID: forged.ID, Owner: "octo", Repo: "scripts", Branch: "main", Path: "unlinked.json", ContentHash: "remote",
	}, unlinkedUpdatedAt); err != nil || outcome != GitHubScriptImportConflict {
		t.Fatalf("unlinked supplied-id import = %q, %v; want conflict", outcome, err)
	}
	unchangedUnlinked, err := store.GetScript("script-unlinked")
	if err != nil || unchangedUnlinked.Name != "Local only" || !unchangedUnlinked.UpdatedAt.Equal(unlinkedUpdatedAt) {
		t.Fatalf("unlinked supplied-id conflict changed script = %#v, %v", unchangedUnlinked, err)
	}
	if got, err := store.GetGitHubScriptLink("script-unlinked"); err != nil || got != nil {
		t.Fatalf("unlinked supplied-id conflict created link = %#v, %v", got, err)
	}

	var beforeCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scripts`).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	colliding := &dsl.Script{ID: "script-target-collision", RefKey: "S-COLLIDE", Name: "Collision", Protocol: "http", Version: "1", ProjectID: "github-project", CreatedAt: now, UpdatedAt: now}
	if outcome, err := store.CommitGitHubScriptImport(colliding, &GitHubScriptLink{
		ScriptID: colliding.ID, Owner: "OCTO", Repo: "SCRIPTS", Branch: "main", Path: "imported.json", ContentHash: "collision",
	}, time.Time{}); err != nil || outcome != GitHubScriptImportConflict {
		t.Fatalf("target-colliding github import = %q, %v; want conflict", outcome, err)
	}
	var afterCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM scripts`).Scan(&afterCount); err != nil {
		t.Fatal(err)
	}
	if afterCount != beforeCount {
		t.Fatalf("target collision changed script count from %d to %d", beforeCount, afterCount)
	}
}

// The importing caller stamps the version. The store keeps it at microseconds,
// as PostgreSQL does, so the caller's copy stays equal to the stored row.
func TestCommitGitHubScriptImportStoresVersionAtMicroseconds(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "github-script-import-precision.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "github-project")
	stamped := time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.UTC)
	script := &dsl.Script{
		ID: "script-precise", RefKey: "S-PRECISE", Name: "Precise", Protocol: "http", Version: "1",
		ProjectID: "github-project", CreatedAt: stamped, UpdatedAt: stamped,
	}
	link := func(blob string) *GitHubScriptLink {
		return &GitHubScriptLink{
			ScriptID: "script-precise", Owner: "octo", Repo: "scripts", Branch: "main", Path: "precise.json",
			LastCommitSHA: blob, RemoteBlobSHA: blob, RemoteCommitSHA: "commit-" + blob, ContentHash: "hash-" + blob,
		}
	}
	if outcome, err := store.CommitGitHubScriptImport(script, link("1"), time.Time{}); err != nil || outcome != GitHubScriptImportCreated {
		t.Fatalf("github import create = %q, %v", outcome, err)
	}
	persisted, err := store.GetScript("script-precise")
	if err != nil {
		t.Fatal(err)
	}
	if want := stamped.Truncate(time.Microsecond); !script.UpdatedAt.Equal(want) || !persisted.UpdatedAt.Equal(want) {
		t.Fatalf("created version = %s (stored %s), want %s", script.UpdatedAt, persisted.UpdatedAt, want)
	}

	updated := *persisted
	updated.Name = "Precise v2"
	next := stamped.Add(time.Second + 789*time.Nanosecond)
	updated.UpdatedAt = next
	if outcome, err := store.CommitGitHubScriptImport(&updated, link("2"), persisted.UpdatedAt); err != nil || outcome != GitHubScriptImportUpdated {
		t.Fatalf("github import update = %q, %v", outcome, err)
	}
	persisted, err = store.GetScript("script-precise")
	if err != nil {
		t.Fatal(err)
	}
	if want := next.Truncate(time.Microsecond); !updated.UpdatedAt.Equal(want) || !persisted.UpdatedAt.Equal(want) {
		t.Fatalf("updated version = %s (stored %s), want %s", updated.UpdatedAt, persisted.UpdatedAt, want)
	}
}

func TestGitHubScriptSyncTargetTablesMigrateBackfillAndDeclareForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-script-sync-migration.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	requireGitHubAnchors(t, store, "github-project", []string{"script-legacy-link"}, nil)
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{
		ScriptID: "script-legacy-link", Owner: " Octo ", Repo: " Scripts ",
		Branch: "feature", BaseBranch: "main", Path: "legacy.json",
	}); err != nil {
		t.Fatalf("seed pre-migration github link: %v", err)
	}
	for _, table := range []string{
		"github_script_sync_target_claims",
		"github_script_sync_target_owners",
		"github_script_sync_targets",
		"github_script_sync_claims",
	} {
		if _, err := store.db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen legacy schema: %v", err)
	}
	defer reopened.Close()
	var ownerCount int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM github_script_sync_target_owners
		WHERE script_id = ? AND owner_norm = ? AND repo_norm = ?`, "script-legacy-link", "octo", "scripts").Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 2 {
		t.Fatalf("backfilled target owners = %d, want primary and base", ownerCount)
	}
	requireGitHubAnchors(t, reopened, "github-project", []string{"script-migrated-claim"}, nil)
	if claimed, err := reopened.AcquireGitHubScriptSyncClaim("script-migrated-claim", "claim-owned-target",
		githubSyncTestTargets("main", "legacy.json"), time.Now().UTC().Add(time.Minute)); err != nil || claimed {
		t.Fatalf("claim of backfilled base target = %t, %v; want ownership conflict", claimed, err)
	}
	if claimed, err := reopened.AcquireGitHubScriptSyncClaim("script-migrated-claim", "claim-migrated", githubSyncTestTargets("main", "migrated.json"), time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("claim after table migration = %t, %v", claimed, err)
	}
	const deletionLeaseOwner = "github-target-migration/1"
	if acquired, err := reopened.TryAcquireSchedulerLease(testExecutionLeaseName, deletionLeaseOwner, time.Now().UTC(), time.Minute); err != nil || !acquired {
		t.Fatalf("acquire project-deletion lease = %t, %v", acquired, err)
	}
	if result, err := reopened.DeleteProjectIfLeaseHeld("github-project", testExecutionLeaseName, deletionLeaseOwner); result != LeaseFencedDeleteFenced || !IsGitHubScriptSyncBusy(err) {
		t.Fatalf("lease-fenced project delete with active sync claim = %q, %v; want fenced busy", result, err)
	}
	if project, err := reopened.GetProject("github-project"); err != nil || project == nil {
		t.Fatalf("busy lease-fenced deletion removed project = %#v, %v", project, err)
	}
	var preservedClaimCount int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM github_script_sync_target_claims WHERE script_id = ?`, "script-migrated-claim").Scan(&preservedClaimCount); err != nil {
		t.Fatal(err)
	}
	if preservedClaimCount != 1 {
		t.Fatalf("busy lease-fenced deletion left %d target claims, want 1", preservedClaimCount)
	}
	if err := reopened.DeleteProject("github-project"); !IsGitHubScriptSyncBusy(err) {
		t.Fatalf("delete project with active sync claim error = %v, want busy", err)
	}
	if released, err := reopened.ReleaseGitHubScriptSyncClaim("script-migrated-claim", "claim-migrated"); err != nil || !released {
		t.Fatalf("release migrated claim = %t, %v", released, err)
	}
	if err := reopened.DeleteProject("github-project"); err != nil {
		t.Fatalf("delete project after releasing sync claim: %v", err)
	}
	for _, table := range []string{"github_script_sync_claims", "github_script_sync_target_claims", "github_script_sync_target_owners"} {
		var remaining int
		if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, "github-project").Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("project deletion left %d rows in %s", remaining, table)
		}
	}

	specs, err := postgresForeignKeySpecs()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"github_script_sync_claims.script_id":         "scripts",
		"github_script_sync_claims.project_id":        "projects",
		"github_script_sync_target_owners.target_key": "github_script_sync_targets",
		"github_script_sync_target_owners.script_id":  "scripts",
		"github_script_sync_target_owners.project_id": "projects",
		"github_script_sync_target_claims.target_key": "github_script_sync_targets",
		"github_script_sync_target_claims.script_id":  "scripts",
		"github_script_sync_target_claims.project_id": "projects",
	}
	for _, spec := range specs {
		key := spec.table + "." + spec.column
		referenced, ok := want[key]
		if !ok || spec.references != referenced || spec.onDelete != "CASCADE" {
			continue
		}
		if (spec.column == "target_key" && spec.refColumn == "target_key") || (spec.column != "target_key" && spec.refColumn == "id") {
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing PostgreSQL GitHub sync target foreign keys: %v", want)
	}
	foundTables := make(map[string]bool)
	for _, statement := range schemaCreateTableStatements() {
		for _, table := range []string{"github_script_sync_claims", "github_script_sync_targets", "github_script_sync_target_owners", "github_script_sync_target_claims"} {
			if !strings.Contains(statement, "CREATE TABLE IF NOT EXISTS "+table+" ") {
				continue
			}
			rewritten := rewriteSQL(statement, storeDialectPostgres)
			if strings.Contains(rewritten, "DATETIME") || !strings.Contains(rewritten, "TIMESTAMPTZ") {
				t.Fatalf("PostgreSQL %s rewrite = %s", table, rewritten)
			}
			foundTables[table] = true
		}
	}
	if len(foundTables) != 4 {
		t.Fatalf("github script sync schema tables = %v, want all four", foundTables)
	}
}

func TestGitHubScriptSyncTargetBackfillRepairsLegacyDuplicateLinksDeterministically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-script-sync-duplicate-migration.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	requireGitHubAnchors(t, store, "github-project", []string{"script-b-old", "script-z-old", "script-a-new"}, nil)
	oldest := time.Now().UTC().Add(-time.Hour)
	newer := oldest.Add(time.Minute)
	for _, legacy := range []struct {
		scriptID string
		owner    string
		repo     string
		syncedAt time.Time
	}{
		{scriptID: "script-z-old", owner: "Octo", repo: "Scripts", syncedAt: oldest},
		{scriptID: "script-b-old", owner: " OCTO ", repo: " scripts ", syncedAt: oldest},
		{scriptID: "script-a-new", owner: "octo", repo: "SCRIPTS", syncedAt: newer},
	} {
		if _, err := store.db.Exec(`INSERT INTO github_script_links
			(script_id, ref_key, owner, repo, branch, path, last_commit_sha, content_hash, synced_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, legacy.scriptID, "", legacy.owner, legacy.repo, "main", "duplicate.json", "", "", legacy.syncedAt, legacy.syncedAt); err != nil {
			t.Fatalf("seed duplicate legacy link %s: %v", legacy.scriptID, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen database with duplicate legacy links: %v", err)
	}
	defer reopened.Close()
	winner, err := reopened.GetGitHubScriptLink("script-b-old")
	if err != nil || winner == nil {
		t.Fatalf("deterministic oldest/lowest winner = %#v, %v", winner, err)
	}
	for _, loserID := range []string{"script-z-old", "script-a-new"} {
		if loser, err := reopened.GetGitHubScriptLink(loserID); err != nil || loser != nil {
			t.Fatalf("legacy duplicate loser %s link = %#v, %v; want removed", loserID, loser, err)
		}
	}
	var ownerScriptID string
	if err := reopened.db.QueryRow(`SELECT script_id FROM github_script_sync_target_owners
		WHERE owner_norm = ? AND repo_norm = ? AND branch = ? AND path = ?`, "octo", "scripts", "main", "duplicate.json").Scan(&ownerScriptID); err != nil {
		t.Fatal(err)
	}
	if ownerScriptID != "script-b-old" {
		t.Fatalf("duplicate target owner = %q, want oldest/lowest script-b-old", ownerScriptID)
	}
	var repairs int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = ? AND status = ? AND resource_id IN (?, ?)`,
		"github.script_link.target_conflict_repaired", "repaired", "script-z-old", "script-a-new").Scan(&repairs); err != nil {
		t.Fatal(err)
	}
	if repairs != 2 {
		t.Fatalf("duplicate-link repair audit events = %d, want 2", repairs)
	}
	targets := githubSyncTestTargets("main", "duplicate.json")
	for _, loserID := range []string{"script-z-old", "script-a-new"} {
		if claimed, err := reopened.AcquireGitHubScriptSyncClaim(loserID, "claim-"+loserID, targets, time.Now().UTC().Add(time.Minute)); err != nil || claimed {
			t.Fatalf("repaired loser %s target claim = %t, %v; want ownership conflict", loserID, claimed, err)
		}
	}
	if claimed, err := reopened.AcquireGitHubScriptSyncClaim("script-b-old", "claim-winner", targets, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("deterministic winner target claim = %t, %v", claimed, err)
	}
	if released, err := reopened.ReleaseGitHubScriptSyncClaim("script-b-old", "claim-winner"); err != nil || !released {
		t.Fatalf("release deterministic winner claim = %t, %v", released, err)
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
		RemoteCommitSHA: "commit-old", BaseBranch: "trunk", BaseBlobSHA: "base-old", ContentHash: "hash-old",
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
	link.BaseBranch = "release"
	link.BaseBlobSHA = "base-new"
	link.BaseMissing = true
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
	if got.Path != "new.json" || got.LastCommitSHA != "blob-new" || got.RemoteBlobSHA != "blob-new" ||
		got.BaseBranch != "release" || got.BaseBlobSHA != "base-new" || !got.BaseMissing || got.ContentHash != "hash-new" {
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
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{ScriptID: "script-a", Owner: "octo", Repo: "scripts", Branch: "main", Path: "script-a.json"}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.AcquireGitHubScriptSyncClaim("script-a", "delete-claim", githubSyncTestTargets("main", "script-a.json"), time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("claim before script deletion = %t, %v", claimed, err)
	}
	if err := store.DeleteScript("script-a"); !IsGitHubScriptSyncBusy(err) {
		t.Fatalf("delete script with active sync claim error = %v, want busy", err)
	}
	if storedScriptLink, err := store.GetGitHubScriptLink("script-a"); err != nil || storedScriptLink == nil {
		t.Fatalf("busy script deletion changed github link %#v, err=%v", storedScriptLink, err)
	}
	if released, err := store.ReleaseGitHubScriptSyncClaim("script-a", "delete-claim"); err != nil || !released {
		t.Fatalf("release claim before script deletion = %t, %v", released, err)
	}
	if err := store.DeleteScript("script-a"); err != nil {
		t.Fatalf("delete script after claim release: %v", err)
	}
	if err := store.SaveGitHubScriptLink(&GitHubScriptLink{ScriptID: "script-a", Owner: "octo", Repo: "scripts"}); err == nil {
		t.Fatal("SaveGitHubScriptLink recreated a link after script deletion")
	}
	storedScriptLink, err := store.GetGitHubScriptLink("script-a")
	if err != nil || storedScriptLink != nil {
		t.Fatalf("script deletion left/recreated github link %#v, err=%v", storedScriptLink, err)
	}
	var remainingClaims int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM github_script_sync_claims WHERE script_id = ?`, "script-a").Scan(&remainingClaims); err != nil {
		t.Fatal(err)
	}
	if remainingClaims != 0 {
		t.Fatalf("script deletion left %d github sync claims", remainingClaims)
	}
}

func TestScriptDeletionPreservesTestAnchoredGitHubCheckLinks(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		name := "direct"
		if conditional {
			name = "conditional"
		}
		t.Run(name, func(t *testing.T) {
			store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			requireGitHubAnchors(t, store, "project-a", []string{"script-a"}, []string{"test-a"})

			if err := store.SaveGitHubCheckLink(&GitHubCheckLink{
				ID: "test-and-script", Kind: "manual", TestID: "test-a", ScriptID: "script-a",
				Owner: "octo", Repo: "scripts", Status: "completed", Conclusion: "success",
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveGitHubCheckLink(&GitHubCheckLink{
				ID: "script-only", Kind: "manual", ScriptID: "script-a", Owner: "octo", Repo: "scripts",
			}); err != nil {
				t.Fatal(err)
			}

			if conditional {
				var updatedAt time.Time
				if err := store.db.QueryRow(`SELECT updated_at FROM scripts WHERE id = ?`, "script-a").Scan(&updatedAt); err != nil {
					t.Fatal(err)
				}
				deleted, err := store.DeleteScriptIfUnchanged("script-a", updatedAt)
				if err != nil || !deleted {
					t.Fatalf("DeleteScriptIfUnchanged = %t, %v", deleted, err)
				}
			} else if err := store.DeleteScript("script-a"); err != nil {
				t.Fatalf("DeleteScript: %v", err)
			}

			preserved, err := store.GetGitHubCheckLink("test-and-script")
			if err != nil || preserved == nil {
				t.Fatalf("test-anchored check after script deletion = %#v, %v", preserved, err)
			}
			if preserved.TestID != "test-a" || preserved.ScriptID != "" || preserved.Status != "completed" || preserved.Conclusion != "success" {
				t.Fatalf("test-anchored check changed beyond detaching script: %#v", preserved)
			}
			removed, err := store.GetGitHubCheckLink("script-only")
			if err != nil || removed != nil {
				t.Fatalf("script-only check after script deletion = %#v, %v; want nil", removed, err)
			}
		})
	}
}
