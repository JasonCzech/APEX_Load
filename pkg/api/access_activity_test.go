package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type accessFailAuditStore struct {
	storage.Store
	calls int
}

func (s *accessFailAuditStore) ListAuditEvents(filter storage.AuditEventFilter) ([]*storage.AuditEvent, error) {
	s.calls++
	if s.calls > 1 {
		return nil, fmt.Errorf("fixture storage failure")
	}
	return s.Store.ListAuditEvents(filter)
}

func TestAccessActivityCompleteHistory(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	now := time.Now().UTC().Add(-time.Minute)
	for i := 0; i < 1005; i++ {
		if err := store.SaveAuditEvent(&storage.AuditEvent{ID: fmt.Sprintf("event-%04d", i), Action: "access.fixture", Status: "success", ActorSubject: "=danger", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		server.mux.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", path, r.Code, r.Body.String())
		}
		return r
	}
	seen := map[string]bool{}
	for offset := 0; offset < 1005; offset += 50 {
		var result AuthUserActivityResponse
		if err := json.Unmarshal(get(fmt.Sprintf("/api/v1/auth/user-activity?action=access.fixture&limit=50&offset=%d", offset)).Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.HasMore != (offset+50 < 1005) {
			t.Fatalf("incorrect has_more at %d", offset)
		}
		for _, event := range result.Events {
			if seen[event.ID] {
				t.Fatalf("duplicate event %s", event.ID)
			}
			seen[event.ID] = true
		}
	}
	if len(seen) != 1005 {
		t.Fatalf("saw %d events", len(seen))
	}
	// An exact final page must not offer another empty page.
	var last AuthUserActivityResponse
	json.Unmarshal(get("/api/v1/auth/user-activity?action=access.fixture&limit=5&offset=1000").Body.Bytes(), &last)
	if last.HasMore || last.Count != 5 {
		t.Fatalf("last page: %+v", last)
	}
	rows, err := csv.NewReader(strings.NewReader(get("/api/v1/auth/user-activity/export?action=access.fixture&all=true&limit=50&offset=900").Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1006 {
		t.Fatalf("export rows=%d", len(rows))
	}
	if rows[1][5] != "'=danger" {
		t.Fatalf("unsafe formula %q", rows[1][5])
	}
	for _, path := range []string{"/api/v1/auth/user-activity", "/api/v1/auth/user-activity/export?all=true"} {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		r := httptest.NewRecorder()
		server.mux.ServeHTTP(r, httptest.NewRequest("GET", path+separator+"created_after=2026-02-02T00:00:00Z&created_before=2026-01-01T00:00:00Z", nil))
		if r.Code != 400 {
			t.Fatalf("invalid range: %d", r.Code)
		}
	}
	server.store = &accessFailAuditStore{Store: store}
	failed := httptest.NewRecorder()
	server.mux.ServeHTTP(failed, httptest.NewRequest("GET", "/api/v1/auth/user-activity/export?all=true", nil))
	if failed.Code != 500 || strings.Contains(failed.Body.String(), "event-1004") || strings.Contains(failed.Header().Get("Content-Type"), "csv") {
		t.Fatalf("failed export must not return a successful partial CSV: %d %s", failed.Code, failed.Body.String())
	}

}

func TestAccessUsageAppliesScopeBeforeActorAndDates(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	now := time.Now().UTC()
	for _, id := range []string{"domain-a", "domain-b"} {
		if err := store.SaveDomain(&storage.Domain{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"domain-a", "domain-b"} {
		if err := store.SaveApplicationRepository(&storage.ApplicationRepository{ID: id + "-repo", DomainID: id, Name: id, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveRepositoryCategory(&storage.RepositoryCategory{ID: id + "-category", DomainID: id, RepositoryID: id + "-repo", Name: "Default", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, project := range []*storage.Project{{ID: "allowed", Name: "Allowed", DomainID: "domain-a"}, {ID: "other", Name: "Other", DomainID: "domain-b"}} {
		project.CategoryID = project.DomainID + "-category"
		project.CreatedAt = now
		project.UpdatedAt = now
		if err := store.SaveProject(project); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []*ManagedTest{
		{ID: "in", Name: "Included", ProjectID: "allowed", CreatedBySubject: "alice", CreatedAt: now, Status: "CREATED"},
		{ID: "out", ProjectID: "other", CreatedBySubject: "alice", CreatedAt: now},
		{ID: "old", ProjectID: "allowed", CreatedBySubject: "alice", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "bob", ProjectID: "allowed", CreatedBySubject: "bob", CreatedAt: now},
	} {
		server.runner.tests[run.ID] = run
	}
	after := now.Add(-time.Hour)
	for _, tc := range []struct {
		name   string
		filter storage.AuditEventFilter
		all    bool
		want   int
	}{
		{"actor cannot escape delegated scope", storage.AuditEventFilter{ActorSubject: "alice", CreatedAfter: &after}, false, 1},
		{"domain applies with actor", storage.AuditEventFilter{DomainID: "domain-a", ActorSubject: "alice", CreatedAfter: &after}, true, 1},
		{"system actor all projects", storage.AuditEventFilter{ActorSubject: "alice", CreatedAfter: &after}, true, 2},
		{"unauthorized project empty", storage.AuditEventFilter{ProjectID: "other", ActorSubject: "alice"}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs, err := server.accessUsageTests(tc.filter, map[string]struct{}{"allowed": {}}, tc.all)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != tc.want {
				t.Fatalf("runs=%d want %d", len(runs), tc.want)
			}
		})
	}
	r := httptest.NewRecorder()
	server.mux.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/auth/user-activity?report=usage&project_id=allowed&actor_subject=alice&limit=1", nil))
	var response AuthUserActivityResponse
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || response.Count != 1 || !response.HasMore || response.Usage.Totals.RunCount != 2 || response.UsageRuns[0].ID != "in" {
		t.Fatalf("usage response %d %s", r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "script") {
		t.Fatal("usage must not include execution payloads")
	}
	// The public endpoints must retain delegated authorization for both reports and exports.
	enableTrustedHeaderAuth(t, server)
	if err := store.SaveUser(&storage.User{ID: "delegated", Subject: "delegated", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: "delegated-binding", UserID: "delegated", ScopeType: "project", ScopeID: "allowed", Role: RoleProjectAdmin, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"allowed", "other"} {
		if err := store.SaveAuditEvent(&storage.AuditEvent{ID: project, ProjectID: project, ActorSubject: "alice", Action: "scope.fixture", CreatedAt: now, Status: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/api/v1/auth/user-activity?report=usage&actor_subject=alice", "/api/v1/auth/user-activity/export?all=true&actor_subject=alice"} {
		r := httptest.NewRecorder()
		server.mux.ServeHTTP(r, trustedHeaderJSONRequest("GET", path, "", "delegated"))
		if r.Code != 200 || strings.Contains(r.Body.String(), "other") {
			t.Fatalf("scope leak or failure: %d %s", r.Code, r.Body.String())
		}
		if strings.Contains(path, "export") {
			rows, err := csv.NewReader(strings.NewReader(r.Body.String())).ReadAll()
			if err != nil || len(rows) != 2 {
				t.Fatalf("scoped export: rows=%d error=%v", len(rows), err)
			}
		}
	}
	for _, path := range []string{"/api/v1/auth/user-activity?created_after=invalid", "/api/v1/auth/user-activity/export?all=true&created_before=invalid"} {
		r := httptest.NewRecorder()
		server.mux.ServeHTTP(r, trustedHeaderJSONRequest("GET", path, "", "delegated"))
		if r.Code != 400 {
			t.Fatalf("invalid date accepted: %d", r.Code)
		}
	}

}
