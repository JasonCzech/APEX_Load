package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConfluenceConnectionValidationAndSecretDisclosure(t *testing.T) {
	base := ConfluenceConnection{ID: "conn", ProjectID: "project", Name: "Wiki", Enabled: true, SiteURL: "https://example.atlassian.net/wiki", Email: "writer@example.com", SpaceID: "42", ParentPageID: "100", TokenSecretName: "wiki-token"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	if base.SiteURL != "https://example.atlassian.net" {
		t.Fatalf("normalized URL = %q", base.SiteURL)
	}
	encoded, _ := json.Marshal(base)
	if strings.Contains(string(encoded), "wiki-token") {
		t.Fatal("secret reference disclosed in JSON")
	}
	for _, site := range []string{"http://example.atlassian.net", "https://user:pass@example.atlassian.net", "https://example.atlassian.net/wiki?q=1", "https://example.atlassian.net/other"} {
		candidate := base
		candidate.SiteURL = site
		if err := candidate.Validate(); err == nil {
			t.Errorf("unsafe site %q accepted", site)
		}
	}
	incomplete := base
	incomplete.TokenSecretName = ""
	if err := incomplete.Validate(); err == nil {
		t.Fatal("enabled connection without token accepted")
	}
	incomplete.Enabled = false
	incomplete.AutoPublish = false
	if err := incomplete.Validate(); err != nil {
		t.Fatalf("incomplete draft: %v", err)
	}
	invalidID := base
	invalidID.ParentPageID = "abc"
	if err := invalidID.Validate(); err == nil {
		t.Fatal("non-numeric parent ID accepted")
	}
}

func TestConfluencePublicationLifecycleAndArchiveEnqueue(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/confluence.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	c := &ConfluenceConnection{ID: "conn-a", ProjectID: "project-a", Name: "Wiki", Enabled: true, SiteURL: "https://example.atlassian.net", Email: "writer@example.com", SpaceID: "42", ParentPageID: "100", TokenSecretName: "wiki-token", AutoPublish: true}
	if err := store.CreateConfluenceConnection(c); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateConfluenceConnectionHealth(c.ID, c.ProjectID, "connected", "Parent page verified", now, c.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if listed, err := store.ListConfluenceConnections("project-b"); err != nil || len(listed) != 0 {
		t.Fatalf("cross-project list: %v, %v", listed, err)
	}
	other := *c
	other.ID = "conn-b"
	if err := store.CreateConfluenceConnection(&other); err == nil {
		t.Fatal("second auto default accepted")
	}
	loaded, err := store.GetConfluenceConnection(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	prior := loaded.UpdatedAt
	loaded.Name = "New name"
	loaded.LastCheckedAt, loaded.LastCheckStatus, loaded.LastCheckMessage = nil, "", ""
	if changed, err := store.UpdateConfluenceConnectionIfUnchanged(loaded, prior); err != nil || !changed {
		t.Fatalf("update: %v, %v", changed, err)
	}
	if refreshed, err := store.GetConfluenceConnection(c.ID); err != nil || refreshed.LastCheckStatus != "" || refreshed.LastCheckedAt != nil {
		t.Fatalf("edited connection retained stale health: %v, %v", refreshed, err)
	}
	if err := store.UpdateConfluenceConnectionHealth(c.ID, c.ProjectID, "connected", "Stale test", now, prior); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale health update = %v, want sql.ErrNoRows", err)
	}
	if changed, err := store.UpdateConfluenceConnectionIfUnchanged(loaded, prior); err != nil || changed {
		t.Fatalf("stale update: %v, %v", changed, err)
	}
	if err := store.SaveTest(&TestRecord{ID: "run-a", ProjectID: "project-a", Name: "Run", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	record := &RunArchiveRecord{TestID: "run-a", ArchivePath: "/archive/run-a", ManifestPath: "/archive/run-a/manifest.json", CreatedAt: now, CompletedAt: now}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListPendingConfluencePublications(10)
	if err != nil || len(items) != 1 {
		t.Fatalf("queued: %v, %v", items, err)
	}
	claimed, ok, err := store.ClaimConfluencePublication("run-a", "project-a", c.ID, "automatic", true)
	if err != nil || !ok || claimed.Attempts != 1 {
		t.Fatalf("claim: %v, %v, %v", claimed, ok, err)
	}
	if _, ok, err := store.ClaimConfluencePublication("run-a", "project-a", c.ID, "automatic", true); err != nil || ok {
		t.Fatalf("double claim: %v, %v", ok, err)
	}
	for wantAttempt := 2; wantAttempt <= 3; wantAttempt++ {
		if err := store.FinishConfluencePublication("run-a", c.ID, "failed", "", "", "temporary error"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`UPDATE confluence_publications SET updated_at=? WHERE test_id=?`, now.Add(-2*time.Minute), "run-a"); err != nil {
			t.Fatal(err)
		}
		retried, ok, err := store.ClaimConfluencePublication("run-a", "project-a", c.ID, "automatic", true)
		if err != nil || !ok || retried.Attempts != wantAttempt {
			t.Fatalf("retry %d: %v, %v, %v", wantAttempt, retried, ok, err)
		}
	}
	if err := store.FinishConfluencePublication("run-a", c.ID, "failed", "", "", "final error"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimConfluencePublication("run-a", "project-a", c.ID, "automatic", true); err != nil || ok {
		t.Fatalf("attempt limit: %v, %v", ok, err)
	}
	if err := store.FinishConfluencePublication("run-a", c.ID, "complete", "200", "https://example.atlassian.net/wiki/spaces/42/pages/200", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimConfluencePublication("run-a", "project-a", c.ID, "automatic", true); err != nil || ok {
		t.Fatalf("automatic republish: %v, %v", ok, err)
	}
	if _, ok, err := store.ClaimConfluencePublication("run-a", "project-a", c.ID, "manual", false); err != nil || !ok {
		t.Fatalf("manual update: %v, %v", ok, err)
	}
	if err := store.FinishConfluencePublication("run-a", c.ID, "complete", "", "", ""); err != nil {
		t.Fatal(err)
	}
	manual, err := store.GetConfluencePublication("run-a", c.ID)
	if err != nil || !manual.Automatic || manual.RequestedBy != "manual" {
		t.Fatalf("manual update lost automatic origin or actor: %v, %v", manual, err)
	}
	if deleted, err := store.DeleteConfluenceConnection(c.ID, c.ProjectID); err != nil || !deleted {
		t.Fatalf("delete: %v, %v", deleted, err)
	}
	if publication, err := store.GetConfluencePublication("run-a", c.ID); err != nil || publication.PageID != "200" {
		t.Fatalf("history after connection deletion: %v, %v", publication, err)
	}
	if err := store.DeleteProject("project-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConfluencePublication("run-a", c.ID); err != sql.ErrNoRows {
		t.Fatalf("publication survived project deletion: %v", err)
	}
}

func TestConfluenceManualAttemptKeepsQueuedAutomaticRetry(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/confluence.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "project-a", Name: "Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: "run-a", ProjectID: "project-a", Name: "Run", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	c := &ConfluenceConnection{ID: "conn-a", ProjectID: "project-a", Name: "Wiki", Enabled: true, SiteURL: "https://example.atlassian.net", Email: "writer@example.com", SpaceID: "42", ParentPageID: "100", TokenSecretName: "wiki-token", AutoPublish: true}
	if err := store.CreateConfluenceConnection(c); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(&RunArchiveRecord{TestID: "run-a", ArchivePath: "/archive/run-a", ManifestPath: "/archive/run-a/manifest.json", CreatedAt: now, CompletedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimConfluencePublication("run-a", "project-a", c.ID, "editor", false)
	if err != nil || !ok || !claimed.Automatic {
		t.Fatalf("manual claim lost automatic intent: %v, %v, %v", claimed, ok, err)
	}
	if err := store.FinishConfluencePublication("run-a", c.ID, "failed", "", "", "temporary error"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE confluence_publications SET updated_at=? WHERE test_id=?`, now.Add(-2*time.Minute), "run-a"); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPendingConfluencePublications(10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("automatic retry missing after manual failure: %v, %v", pending, err)
	}
}
