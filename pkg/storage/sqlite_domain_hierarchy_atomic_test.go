package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateDomainHierarchyRollsBackEveryRowOnConflict(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveRepositoryCategory(&RepositoryCategory{
		ID: DefaultRepositoryCategoryID, DomainID: DefaultDomainID, RepositoryID: DefaultApplicationRepositoryID,
		Name: "Existing", CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	domain := &Domain{ID: "atomic-domain", Name: "Atomic", CreatedAt: now}
	repository := &ApplicationRepository{ID: "atomic-repository", DomainID: domain.ID, Name: "Repository", CreatedAt: now}
	category := &RepositoryCategory{
		ID: DefaultRepositoryCategoryID, DomainID: domain.ID, RepositoryID: repository.ID,
		Name: "Conflicting category", CreatedAt: now,
	}
	if err := store.CreateDomainHierarchy(domain, repository, category); err == nil {
		t.Fatal("CreateDomainHierarchy unexpectedly accepted a duplicate category id")
	}
	if _, err := store.GetDomain(domain.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("partially-created domain lookup error = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.GetApplicationRepository(domain.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("partially-created repository lookup error = %v, want sql.ErrNoRows", err)
	}
}

func TestDomainHierarchyOptimisticUpdatesRejectStaleAndRelocation(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	domain := &Domain{ID: "cas-domain", Name: "Domain", CreatedAt: now}
	repository := &ApplicationRepository{ID: "cas-repository", DomainID: domain.ID, Name: "Repository", CreatedAt: now}
	category := &RepositoryCategory{ID: "cas-category", DomainID: domain.ID, RepositoryID: repository.ID, Name: "Category", CreatedAt: now}
	if err := store.CreateDomainHierarchy(domain, repository, category); err != nil {
		t.Fatalf("CreateDomainHierarchy: %v", err)
	}

	domainFirst, _ := store.GetDomain(domain.ID)
	domainStale, _ := store.GetDomain(domain.ID)
	domainFirst.Name = "first domain writer"
	if updated, err := store.UpdateDomainIfUnchanged(domainFirst, domainFirst.UpdatedAt); err != nil || !updated {
		t.Fatalf("first domain update = (%v, %v)", updated, err)
	}
	domainStale.Name = "stale domain writer"
	if updated, err := store.UpdateDomainIfUnchanged(domainStale, domainStale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale domain update = (%v, %v), want conflict", updated, err)
	}

	repositoryFirst, _ := store.GetApplicationRepository(domain.ID)
	repositoryStale, _ := store.GetApplicationRepository(domain.ID)
	repositoryFirst.Name = "first repository writer"
	if updated, err := store.UpdateApplicationRepositoryIfUnchanged(repositoryFirst, repositoryFirst.UpdatedAt); err != nil || !updated {
		t.Fatalf("first repository update = (%v, %v)", updated, err)
	}
	repositoryStale.Name = "stale repository writer"
	if updated, err := store.UpdateApplicationRepositoryIfUnchanged(repositoryStale, repositoryStale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale repository update = (%v, %v), want conflict", updated, err)
	}
	if err := store.SaveApplicationRepository(&ApplicationRepository{ID: repository.ID, DomainID: DefaultDomainID, Name: "relocation attempt"}); err == nil {
		t.Fatal("SaveApplicationRepository accepted an id owned by another domain")
	}
	repositoryFresh, err := store.GetApplicationRepository(domain.ID)
	if err != nil || repositoryFresh.ID != repository.ID || repositoryFresh.Name == "relocation attempt" {
		t.Fatalf("repository moved from domain: %#v, err=%v", repositoryFresh, err)
	}

	categoryFirst, _ := store.GetRepositoryCategory(category.ID)
	categoryStale, _ := store.GetRepositoryCategory(category.ID)
	categoryFirst.Name = "first category writer"
	if updated, err := store.UpdateRepositoryCategoryIfUnchanged(categoryFirst, categoryFirst.UpdatedAt); err != nil || !updated {
		t.Fatalf("first category update = (%v, %v)", updated, err)
	}
	categoryStale.Name = "stale category writer"
	if updated, err := store.UpdateRepositoryCategoryIfUnchanged(categoryStale, categoryStale.UpdatedAt); err != nil || updated {
		t.Fatalf("stale category update = (%v, %v), want conflict", updated, err)
	}
	if err := store.SaveRepositoryCategory(&RepositoryCategory{ID: category.ID, DomainID: DefaultDomainID, RepositoryID: DefaultApplicationRepositoryID, Name: "relocation attempt"}); err == nil {
		t.Fatal("SaveRepositoryCategory accepted an id owned by another hierarchy")
	}
	categoryFresh, err := store.GetRepositoryCategory(category.ID)
	if err != nil || categoryFresh.DomainID != domain.ID || categoryFresh.RepositoryID != repository.ID || categoryFresh.Name == "relocation attempt" {
		t.Fatalf("category placement changed: %#v, err=%v", categoryFresh, err)
	}
}

func TestHierarchyWritesRejectCrossOwnedPlacement(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, suffix := range []string{"a", "b"} {
		domain := &Domain{ID: "placement-domain-" + suffix, Name: "Domain " + suffix, CreatedAt: now}
		repository := &ApplicationRepository{ID: "placement-repository-" + suffix, DomainID: domain.ID, Name: "Repository " + suffix, CreatedAt: now}
		category := &RepositoryCategory{ID: "placement-category-" + suffix, DomainID: domain.ID, RepositoryID: repository.ID, Name: "Category " + suffix, CreatedAt: now}
		if err := store.CreateDomainHierarchy(domain, repository, category); err != nil {
			t.Fatalf("CreateDomainHierarchy(%s): %v", suffix, err)
		}
	}

	if err := store.SaveRepositoryCategory(&RepositoryCategory{
		ID: "cross-owned-category", DomainID: "placement-domain-a", RepositoryID: "placement-repository-b",
		Name: "Invalid", CreatedAt: now,
	}); err == nil {
		t.Fatal("SaveRepositoryCategory accepted a repository owned by another domain")
	}
	if _, err := store.GetRepositoryCategory("cross-owned-category"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid category was persisted: %v", err)
	}

	project := &Project{
		ID: "placement-project", DomainID: "placement-domain-a", CategoryID: "placement-category-b",
		Name: "Invalid", CreatedAt: now,
	}
	if err := store.SaveProject(project); err == nil {
		t.Fatal("SaveProject accepted a category owned by another domain")
	}
	if _, err := store.GetProject(project.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid project was persisted: %v", err)
	}

	project.DomainID = "placement-domain-a"
	project.CategoryID = "placement-category-a"
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject(valid placement): %v", err)
	}
	stored, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project.DomainID = "placement-domain-b"
	project.CategoryID = "placement-category-a"
	if updated, err := store.UpdateProjectIfUnchanged(project, stored.UpdatedAt); err == nil || updated {
		t.Fatalf("UpdateProjectIfUnchanged accepted cross-owned placement: updated=%t err=%v", updated, err)
	}
	stored, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DomainID != "placement-domain-a" || stored.CategoryID != "placement-category-a" {
		t.Fatalf("rejected project move changed placement: %#v", stored)
	}
}
