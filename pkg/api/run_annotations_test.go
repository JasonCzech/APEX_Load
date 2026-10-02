package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRunAnnotationLifecycleAndValidation(t *testing.T) {
	s, project := newGrafanaDashboardAPIServer(t)
	start := time.Now().UTC().Add(-time.Minute)
	end := start.Add(30 * time.Second)
	if err := s.store.SaveTest(&storage.TestRecord{ID: "run", ProjectID: project, Name: "run", Status: "COMPLETE", StartTime: start, StartedAt: &start, EndTime: &end}); err != nil {
		t.Fatal(err)
	}
	s.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: project, Status: "COMPLETE", StartedAt: &start, EndedAt: &end}
	path := "/api/v1/tests/run/analysis/annotations"
	req := runAnnotationRequest{Timestamp: start.Add(time.Second), Label: "Deploy", Text: "Version 2"}
	r := dashboardAPIRequest(t, s, "POST", path, req)
	if r.Code != 201 {
		t.Fatalf("create %d %s", r.Code, r.Body.String())
	}
	var set storage.RunAnnotationSet
	if err := json.Unmarshal(r.Body.Bytes(), &set); err != nil {
		t.Fatal(err)
	}
	if set.Revision != 1 || len(set.Annotations) != 1 || set.Annotations[0].CreatedAt.IsZero() {
		t.Fatalf("audit %+v", set)
	}
	if r := dashboardAPIRequest(t, s, "POST", path, req); r.Code != 409 {
		t.Fatalf("stale %d %s", r.Code, r.Body.String())
	}
	for _, at := range []time.Time{time.Time{}, start.Add(-time.Second), end.Add(time.Second)} {
		bad := req
		bad.ExpectedRevision = 1
		bad.Timestamp = at
		if r := dashboardAPIRequest(t, s, "POST", path, bad); r.Code != 400 {
			t.Fatalf("timestamp %d %s", r.Code, r.Body.String())
		}
	}
	deleted := dashboardAPIRequest(t, s, "DELETE", path+"/"+set.Annotations[0].ID+"?expected_revision=1", nil)
	if deleted.Code != 200 {
		t.Fatalf("delete %d %s", deleted.Code, deleted.Body.String())
	}
	get := dashboardAPIRequest(t, s, "GET", path, nil)
	json.Unmarshal(get.Body.Bytes(), &set)
	if set.Revision != 2 || set.Annotations[0].DeletedAt == nil || set.Annotations[0].Text != "Version 2" {
		t.Fatalf("audit lost %+v", set)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		url := path
		if method == http.MethodDelete {
			url += "/note"
		}
		access, ok := accessRequirementForRequest(httptest.NewRequest(method, url, nil))
		if !ok || access.ProjectLookup == nil {
			t.Fatalf("unprotected %s", method)
		}
		viewer := false
		for _, role := range access.ProjectRoles {
			viewer = viewer || role == RoleViewer
		}
		if viewer != (method == http.MethodGet) {
			t.Fatalf("viewer writes %s %+v", method, access)
		}
	}
}

func TestRunAnnotationLongAuditActorsRemainWritable(t *testing.T) {
	for _, tc := range []struct{ name, display, subject string }{
		{name: "multibyte display", display: strings.Repeat("界", 200), subject: "editor"},
		{name: "subject fallback", subject: strings.Repeat("x", 500)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, project := newGrafanaDashboardAPIServer(t)
			start := time.Now().UTC().Add(-time.Minute)
			end := start.Add(30 * time.Second)
			if err := s.store.SaveTest(&storage.TestRecord{ID: "run", ProjectID: project, Name: "run", Status: "COMPLETE", StartTime: start, StartedAt: &start, EndTime: &end}); err != nil {
				t.Fatal(err)
			}
			s.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: project, Status: "COMPLETE", StartedAt: &start, EndedAt: &end}
			actor := &AuthenticatedUser{Subject: tc.subject, DisplayName: tc.display}
			body, err := json.Marshal(runAnnotationRequest{Timestamp: start.Add(time.Second), Label: "Deployment"})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			r.SetPathValue("id", "run")
			r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, actor))
			w := httptest.NewRecorder()
			s.handleCreateRunAnnotation(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("create %d %s", w.Code, w.Body.String())
			}
			var set storage.RunAnnotationSet
			if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil {
				t.Fatal(err)
			}
			createdBy := set.Annotations[0].CreatedBy
			if createdBy == "" || len(createdBy) > 256 || !utf8.ValidString(createdBy) {
				t.Fatalf("invalid actor projection length=%d", len(createdBy))
			}
			r = httptest.NewRequest(http.MethodDelete, "/?expected_revision=1", nil)
			r.SetPathValue("id", "run")
			r.SetPathValue("annotation_id", set.Annotations[0].ID)
			r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, actor))
			w = httptest.NewRecorder()
			s.handleDeleteRunAnnotation(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("delete %d %s", w.Code, w.Body.String())
			}
			if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil {
				t.Fatal(err)
			}
			if set.Annotations[0].DeletedAt == nil || set.Annotations[0].DeletedBy != createdBy {
				t.Fatalf("invalid deletion actor: %+v", set.Annotations[0])
			}
		})
	}
}
