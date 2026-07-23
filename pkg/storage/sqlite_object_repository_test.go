package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestObjectRepositoryRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "proj-1")

	// Absent repo returns sql.ErrNoRows from Get.
	if _, err := store.GetObjectRepository("missing"); err == nil {
		t.Fatal("expected error for absent repository")
	}

	repo := &ObjectRepository{
		ProjectID: "proj-1",
		Name:      "Checkout objects",
		Source:    "script:s1",
		Objects: []RepositoryObject{
			{LogicalName: "search-box", CSS: "[data-testid=search]", BestConfidence: 95,
				Candidates: []RepositoryObjectCandidate{{Kind: "css", Value: "[data-testid=search]", Confidence: 95}}},
			{LogicalName: "submit", Role: "button", Attributes: map[string]string{"aria-label": "Go"}},
		},
	}
	repo.ID = "object-repo-1"
	if err := store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("SaveObjectRepository: %v", err)
	}
	if repo.CreatedAt.IsZero() || repo.UpdatedAt.IsZero() {
		t.Error("timestamps not stamped")
	}

	loaded, err := store.GetObjectRepository("object-repo-1")
	if err != nil {
		t.Fatalf("GetObjectRepository: %v", err)
	}
	if loaded == nil || len(loaded.Objects) != 2 {
		t.Fatalf("expected 2 objects, got %#v", loaded)
	}
	if loaded.Objects[0].LogicalName != "search-box" || loaded.Objects[0].Candidates[0].Confidence != 95 {
		t.Errorf("object not round-tripped: %#v", loaded.Objects[0])
	}
	if loaded.Objects[1].Attributes["aria-label"] != "Go" {
		t.Errorf("attributes not round-tripped: %#v", loaded.Objects[1])
	}

	// List is project-scoped.
	repos, err := store.ListObjectRepositories("proj-1", 100, 0)
	if err != nil || len(repos) != 1 {
		t.Fatalf("ListObjectRepositories proj-1 = %d (%v)", len(repos), err)
	}
	if other, _ := store.ListObjectRepositories("proj-2", 100, 0); len(other) != 0 {
		t.Errorf("expected no repos for proj-2, got %d", len(other))
	}

	// Upsert replaces objects.
	repo.Objects = repo.Objects[:1]
	if err := store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	loaded, _ = store.GetObjectRepository("object-repo-1")
	if len(loaded.Objects) != 1 {
		t.Errorf("upsert did not replace objects, got %d", len(loaded.Objects))
	}

	if err := store.DeleteObjectRepository("object-repo-1"); err != nil {
		t.Fatalf("DeleteObjectRepository: %v", err)
	}
	if repos, _ := store.ListObjectRepositories("proj-1", 100, 0); len(repos) != 0 {
		t.Errorf("expected 0 repos after delete, got %d", len(repos))
	}
}

func TestObjectRepositoryDeleteAndScriptBindingsPreserveReferences(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"object-ref-project-a", "object-ref-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	for _, repo := range []*ObjectRepository{
		{ID: "object-repo-ref-a", ProjectID: "object-ref-project-a", Name: "A", Objects: []RepositoryObject{{LogicalName: "login", CSS: "#login"}}},
		{ID: "object-repo-ref-b", ProjectID: "object-ref-project-b", Name: "B", Objects: []RepositoryObject{{LogicalName: "login", CSS: "#login"}}},
	} {
		if err := store.SaveObjectRepository(repo); err != nil {
			t.Fatalf("SaveObjectRepository(%s): %v", repo.ID, err)
		}
	}
	script := &dsl.Script{
		ID: "object-ref-script-a", Name: "A", Protocol: "browser", ProjectID: "object-ref-project-a", CreatedAt: now,
		Actions: []dsl.Action{{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-ref-a:login"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript owned binding: %v", err)
	}
	if err := store.DeleteObjectRepository("object-repo-ref-a"); err == nil {
		t.Fatal("DeleteObjectRepository removed a referenced repository")
	}
	if _, err := store.GetObjectRepository("object-repo-ref-a"); err != nil {
		t.Fatalf("referenced repository disappeared: %v", err)
	}
	forged := *script
	forged.Actions = []dsl.Action{{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-ref-b:login"}}
	if err := store.SaveScript(&forged); err == nil {
		t.Fatal("SaveScript accepted a foreign-project object repository")
	}
	missing := *script
	missing.ID = "object-ref-script-missing"
	missing.Actions = []dsl.Action{{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-ref-missing:login"}}
	if err := store.SaveScript(&missing); err == nil {
		t.Fatal("SaveScript accepted a missing qualified object repository")
	}
	script.Actions[0].ObjectRef = ""
	script.Actions[0].Selector = "#login"
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript remove binding: %v", err)
	}
	if err := store.DeleteObjectRepository("object-repo-ref-a"); err != nil {
		t.Fatalf("DeleteObjectRepository after unbind: %v", err)
	}
}

func TestObjectRepositoryOptimisticUpdateRejectsStaleAndOwnershipChanges(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a", "project-b")

	repo := &ObjectRepository{ID: "object-repo-cas", ProjectID: "project-a", Name: "original"}
	if err := store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("SaveObjectRepository: %v", err)
	}
	first, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository first: %v", err)
	}
	stale, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository stale: %v", err)
	}

	first.Name = "first writer"
	if updated, err := store.UpdateObjectRepositoryIfUnchanged(first, first.UpdatedAt); err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Name = "stale writer"
	if updated, err := store.UpdateObjectRepositoryIfUnchanged(stale, stale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	fresh, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository fresh: %v", err)
	}
	if fresh.Name != "first writer" || fresh.ProjectID != "project-a" {
		t.Fatalf("fresh repository = %#v, want first writer in project-a", fresh)
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	fresh.Name = "canceled writer"
	if updated, err := store.UpdateObjectRepositoryIfUnchangedContext(canceledContext, fresh, fresh.UpdatedAt); !errors.Is(err, context.Canceled) || updated {
		t.Fatalf("canceled update = (%v, %v), want context.Canceled without mutation", updated, err)
	}
	afterCanceled, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository after canceled update: %v", err)
	}
	if afterCanceled.Name != "first writer" {
		t.Fatalf("canceled update persisted name %q", afterCanceled.Name)
	}
	fresh = afterCanceled

	expected := fresh.UpdatedAt
	fresh.ProjectID = "project-b"
	if updated, err := store.UpdateObjectRepositoryIfUnchanged(fresh, expected); err != nil || updated {
		t.Fatalf("ownership-changing update = (%v, %v), want rejection", updated, err)
	}
	// Even the legacy upsert primitive must not mutate an existing row through a
	// different project scope.
	if err := store.SaveObjectRepository(&ObjectRepository{ID: repo.ID, ProjectID: "project-b", Name: "legacy update"}); err == nil {
		t.Fatal("SaveObjectRepository accepted a cross-project update")
	}
	fresh, err = store.GetObjectRepository(repo.ID)
	if err != nil || fresh.ProjectID != "project-a" || fresh.Name != "first writer" {
		t.Fatalf("repository ownership after upsert = %#v, err=%v", fresh, err)
	}
	if err := store.SaveObjectRepository(&ObjectRepository{ID: "missing-owner", ProjectID: "missing-project", Name: "bad"}); err == nil {
		t.Fatal("SaveObjectRepository accepted a missing owner project")
	}
}

func TestObjectRepositoryOptimisticUpdateFencesExecutionLeaseTerm(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	requireProjectFixtures(t, store, "project-a")

	repo := &ObjectRepository{ID: "object-repo-lease-cas", ProjectID: "project-a", Name: "original"}
	if err := store.SaveObjectRepository(repo); err != nil {
		t.Fatalf("SaveObjectRepository: %v", err)
	}
	const (
		leaseName = "controller-execution-leader"
		oldOwner  = "controller-old/1"
		newOwner  = "controller-new/1"
	)
	acquired, oldTerm, err := store.TryAcquireSchedulerLeaseTerm(leaseName, oldOwner, time.Time{}, time.Minute)
	if err != nil || !acquired || oldTerm == nil {
		t.Fatalf("acquire old lease term = (%v, %#v, %v)", acquired, oldTerm, err)
	}

	expiredSnapshot, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository expired snapshot: %v", err)
	}
	expiredSnapshot.Name = "expired writer"
	if err := store.ReleaseSchedulerLease(leaseName, oldOwner); err != nil {
		t.Fatalf("ReleaseSchedulerLease old term: %v", err)
	}
	liveContext := context.Background()
	if updated, err := store.UpdateObjectRepositoryIfUnchangedContextAndLeaseTermHeld(
		liveContext,
		expiredSnapshot,
		expiredSnapshot.UpdatedAt,
		leaseName,
		oldOwner,
		oldTerm.Generation,
	); err != nil || updated {
		t.Fatalf("expired-term update = (%v, %v), want fenced without context cancellation", updated, err)
	}

	acquired, newTerm, err := store.TryAcquireSchedulerLeaseTerm(leaseName, newOwner, time.Time{}, time.Minute)
	if err != nil || !acquired || newTerm == nil {
		t.Fatalf("acquire replacement lease term = (%v, %#v, %v)", acquired, newTerm, err)
	}
	replacedSnapshot, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository replaced snapshot: %v", err)
	}
	replacedSnapshot.Name = "replaced writer"
	if updated, err := store.UpdateObjectRepositoryIfUnchangedContextAndLeaseTermHeld(
		liveContext,
		replacedSnapshot,
		replacedSnapshot.UpdatedAt,
		leaseName,
		oldOwner,
		oldTerm.Generation,
	); err != nil || updated {
		t.Fatalf("replaced-term update = (%v, %v), want fenced without context cancellation", updated, err)
	}

	currentSnapshot, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository current snapshot: %v", err)
	}
	currentSnapshot.Name = "current writer"
	if updated, err := store.UpdateObjectRepositoryIfUnchangedContextAndLeaseTermHeld(
		liveContext,
		currentSnapshot,
		currentSnapshot.UpdatedAt,
		leaseName,
		newOwner,
		newTerm.Generation,
	); err != nil || !updated {
		t.Fatalf("current-term update = (%v, %v), want success", updated, err)
	}
	persisted, err := store.GetObjectRepository(repo.ID)
	if err != nil {
		t.Fatalf("GetObjectRepository persisted: %v", err)
	}
	if persisted.Name != "current writer" {
		t.Fatalf("persisted repository name = %q, want current writer", persisted.Name)
	}
}
