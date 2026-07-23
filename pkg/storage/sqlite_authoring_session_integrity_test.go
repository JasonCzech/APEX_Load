package storage

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAuthoringSessionProjectOwnershipAndPagination(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, projectID := range []string{"authoring-project-a", "authoring-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	if err := store.SaveAuthoringSession(&AuthoringSession{
		ID: "missing-project-session", ProjectID: "missing-project", Source: "manual", Name: "missing", Status: "draft",
	}); err == nil {
		t.Fatal("SaveAuthoringSession accepted a missing project")
	}
	for i, id := range []string{"authoring-session-1", "authoring-session-2", "authoring-session-3"} {
		session := &AuthoringSession{
			ID: id, ProjectID: "authoring-project-a", Source: "manual", Name: id, Status: "draft",
			Draft: &dsl.Script{ID: "draft-" + id, ProjectID: "authoring-project-a", Name: id, Protocol: "http"},
		}
		if err := store.SaveAuthoringSession(session); err != nil {
			t.Fatalf("SaveAuthoringSession(%s): %v", id, err)
		}
		if i < 2 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	page, err := store.ListAuthoringSessionsPage("authoring-project-a", 1, 1)
	if err != nil {
		t.Fatalf("ListAuthoringSessionsPage: %v", err)
	}
	if len(page) != 1 || page[0].ID != "authoring-session-2" {
		t.Fatalf("page = %#v, want authoring-session-2", page)
	}

	owned, err := store.GetAuthoringSession("authoring-session-1")
	if err != nil {
		t.Fatalf("GetAuthoringSession: %v", err)
	}
	forged := *owned
	forged.ProjectID = "authoring-project-b"
	forged.Draft = &dsl.Script{ID: "draft-forged", ProjectID: "authoring-project-b", Name: "forged", Protocol: "http"}
	if err := store.SaveAuthoringSession(&forged); err == nil {
		t.Fatal("SaveAuthoringSession allowed project ownership rewrite")
	}
	if updated, err := store.CompareAndSwapAuthoringSession(&forged, owned.UpdatedAt, owned.Status); err != nil || updated {
		t.Fatalf("cross-project CAS = (%v, %v), want false, nil", updated, err)
	}
	mismatchedDraft := *owned
	mismatchedDraft.Draft = &dsl.Script{ID: "draft-mismatch", ProjectID: "authoring-project-b", Name: "mismatch", Protocol: "http"}
	if err := store.SaveAuthoringSession(&mismatchedDraft); err == nil {
		t.Fatal("SaveAuthoringSession accepted a foreign-project draft")
	}
	got, err := store.GetAuthoringSession(owned.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession after rejected writes: %v", err)
	}
	if got.ProjectID != "authoring-project-a" || got.Name != owned.Name {
		t.Fatalf("rejected writes changed session: %#v", got)
	}
}

func TestConcurrentAuthoringSessionCreateAndProjectDeleteCannotLeaveOrphan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authoring-project-race.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	now := time.Now().UTC()
	if err := first.SaveProject(&Project{ID: "authoring-race-project", Name: "Race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- first.SaveAuthoringSession(&AuthoringSession{
			ID: "authoring-race-session", ProjectID: "authoring-race-project", Source: "manual", Name: "Race", Status: "draft",
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- second.DeleteProject("authoring-race-project")
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			continue
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "not found") && !strings.Contains(message, "locked") && !strings.Contains(message, "busy") {
			t.Fatalf("concurrent project/authoring-session mutation error: %v", err)
		}
	}

	session, sessionErr := first.GetAuthoringSession("authoring-race-session")
	project, projectErr := first.GetProject("authoring-race-project")
	if sessionErr == nil && session != nil && (projectErr != nil || project == nil) {
		t.Fatalf("orphan authoring session survived concurrent create/delete: session=%#v project=%#v projectErr=%v", session, project, projectErr)
	}
}

func TestConcurrentCreateAuthoringSessionConvergesWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authoring-idempotency-race.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	now := time.Now().UTC()
	const projectID = "authoring-idempotency-project"
	if err := first.SaveProject(&Project{ID: projectID, Name: "Idempotency", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	start := make(chan struct{})
	type result struct {
		created bool
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i, store := range []*SQLiteStore{first, second} {
		wg.Add(1)
		go func(index int, replica *SQLiteStore) {
			defer wg.Done()
			<-start
			created, err := replica.CreateAuthoringSession(&AuthoringSession{
				ID:        "stable-idempotent-session",
				ProjectID: projectID,
				Source:    "source_review_artifact",
				Name:      fmt.Sprintf("Replica %d", index),
				Status:    "reviewing",
				Draft:     &dsl.Script{ProjectID: projectID, Name: fmt.Sprintf("Draft %d", index), Protocol: "java_vuser"},
			})
			results <- result{created: created, err: err}
		}(i, store)
	}
	close(start)
	wg.Wait()
	close(results)

	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("CreateAuthoringSession: %v", result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want exactly one winning insert", createdCount)
	}
	session, err := first.GetAuthoringSession("stable-idempotent-session")
	if err != nil {
		t.Fatalf("GetAuthoringSession: %v", err)
	}
	if session == nil || (session.Name != "Replica 0" && session.Name != "Replica 1") {
		t.Fatalf("persisted session = %#v, want one complete winning replica", session)
	}
}
