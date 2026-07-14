package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProjectMutationAdmissionBlocksDeletionUntilMutationCompletes(t *testing.T) {
	server := &Server{}
	mutationEntered := make(chan struct{})
	releaseMutation := make(chan struct{})
	mutationDone := make(chan struct{})
	handler := server.projectMutationAdmission(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(mutationEntered)
		<-releaseMutation
		close(mutationDone)
	}))

	go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/applications", nil))
	select {
	case <-mutationEntered:
	case <-time.After(time.Second):
		t.Fatal("mutation did not enter admission gate")
	}

	deleteAdmitted := make(chan struct{})
	go func() {
		server.projectMutationMu.Lock()
		close(deleteAdmitted)
		server.projectMutationMu.Unlock()
	}()
	select {
	case <-deleteAdmitted:
		t.Fatal("project deletion acquired the gate while a mutation was in flight")
	case <-time.After(50 * time.Millisecond):
	}
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
