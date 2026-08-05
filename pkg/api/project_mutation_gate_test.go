package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func projectMutationTestStore(projectIDs ...string) *metricsStoreSpy {
	store := &metricsStoreSpy{projectsByID: make(map[string]*storage.Project, len(projectIDs))}
	for _, projectID := range projectIDs {
		store.projectsByID[projectID] = &storage.Project{ID: projectID}
	}
	return store
}

func TestProjectMutationAdmissionBlocksOnlySameProjectDeletion(t *testing.T) {
	server := &Server{store: projectMutationTestStore("project-a")}
	mutationEntered := make(chan struct{})
	releaseMutation := make(chan struct{})
	mutationDone := make(chan struct{})
	handler := server.projectMutationAdmission(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(mutationEntered)
		<-releaseMutation
		close(mutationDone)
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/applications", nil)
	bindAuthorizedProjectToRequest(request, "project-a")
	go handler.ServeHTTP(httptest.NewRecorder(), request)
	select {
	case <-mutationEntered:
	case <-time.After(time.Second):
		t.Fatal("mutation did not enter admission gate")
	}

	deleteAdmitted := make(chan struct{})
	deleteDone := make(chan struct{})
	go func() {
		release := server.projectMutations.acquire("project-a", true)
		close(deleteAdmitted)
		release()
		close(deleteDone)
	}()
	select {
	case <-deleteAdmitted:
		t.Fatal("project deletion acquired the gate while a mutation was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	releaseOther := server.projectMutations.acquire("project-b", true)
	releaseOther()
	close(releaseMutation)
	select {
	case <-mutationDone:
	case <-time.After(time.Second):
		t.Fatal("mutation did not complete")
	}
	select {
	case <-deleteAdmitted:
	case <-time.After(time.Second):
		t.Fatal("project deletion was not admitted after mutation completion")
	}
	<-deleteDone
	server.projectMutations.mu.Lock()
	remaining := len(server.projectMutations.entries)
	server.projectMutations.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("coordinator retained %d idle project lock(s)", remaining)
	}
}

func TestProjectMutationAdmissionLeavesProjectDeleteForExclusiveHandler(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	called := false
	server.mux.Handle(deleteProjectRoutePattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	handler := server.projectMutationAdmission(server.mux)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/api/v1/projects/project-1", nil))
	if !called {
		t.Fatal("project delete request did not reach its exclusive handler")
	}
}

func TestProjectMutationCoordinatedAuthBindsGlobalAdminProject(t *testing.T) {
	server := &Server{store: projectMutationTestStore("project-ci")}
	globalAdminAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resolution := authResolution{User: &AuthenticatedUser{
				ID:          "global-admin",
				GlobalRoles: map[string]struct{}{RoleSystemAdmin: {}},
			}}
			requirement := accessRequirement{
				GlobalRoles: []string{RoleSystemAdmin},
				ProjectLookup: func(*http.Request, storage.Store) (string, int, string) {
					return "project-ci", 0, ""
				},
			}
			if authorizeAccessRequirement(w, r, &metricsStoreSpy{}, resolution, requirement) {
				next.ServeHTTP(w, r)
			}
		})
	}

	mutationEntered := make(chan struct{})
	releaseMutation := make(chan struct{})
	handler := server.projectMutationCoordinatedAuth(globalAdminAuth)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(mutationEntered)
		<-releaseMutation
	}))
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", nil))
		close(done)
	}()
	select {
	case <-mutationEntered:
	case <-time.After(time.Second):
		t.Fatal("global-admin mutation did not enter the coordinated handler")
	}

	deleteAdmitted := make(chan struct{})
	go func() {
		release := server.projectMutations.acquire("project-ci", true)
		close(deleteAdmitted)
		release()
	}()
	select {
	case <-deleteAdmitted:
		t.Fatal("global-admin mutation was not bound to its project gate")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseMutation)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("global-admin mutation did not finish")
	}
	select {
	case <-deleteAdmitted:
	case <-time.After(time.Second):
		t.Fatal("project deletion did not proceed after global-admin mutation")
	}
}

func TestProjectMutationAdmissionRejectsDeletedProjectTombstone(t *testing.T) {
	server := &Server{store: projectMutationTestStore()}
	server.projectMutations.markDeleted("project-deleted")
	called := false
	handler := server.projectMutationAdmission(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/applications", nil)
	bindAuthorizedProjectToRequest(request, "project-deleted")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
	if called {
		t.Fatal("mutation handler ran after project deletion")
	}
}

func TestProjectMutationAdmissionPreservesUnmaterializedProjectSemantics(t *testing.T) {
	server := &Server{store: projectMutationTestStore()}
	called := false
	handler := server.projectMutationAdmission(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/applications", nil)
	bindAuthorizedProjectToRequest(request, "lightweight-project")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("unmaterialized project status = %d, called = %t; want downstream status %d", response.Code, called, http.StatusNoContent)
	}
}
