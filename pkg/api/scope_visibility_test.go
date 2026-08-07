package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// scopeVisibilityServer builds a two-domain fixture with authentication in a
// non-bypass mode, so the scope filters actually engage.
//
//	domain-a -> proj-a1 (app-a1), proj-a2
//	domain-b -> proj-b1 (app-b1)
//	default  -> app-orphan (no project_id)
func scopeVisibilityServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	categories := make(map[string]string, 2)
	for _, domain := range []string{"domain-a", "domain-b"} {
		// A project's category must belong to its domain, so each domain needs
		// its own repository + category hierarchy.
		repository := &storage.ApplicationRepository{
			ID: domain + "-repo", DomainID: domain, Name: domain + " repo", CreatedAt: now, UpdatedAt: now,
		}
		category := &storage.RepositoryCategory{
			ID: domain + "-cat", DomainID: domain, RepositoryID: repository.ID,
			Name: "Uncategorized", CreatedAt: now, UpdatedAt: now,
		}
		if err := createDomainHierarchy(store,
			&storage.Domain{ID: domain, Name: domain, CreatedAt: now, UpdatedAt: now}, repository, category); err != nil {
			t.Fatalf("createDomainHierarchy(%s): %v", domain, err)
		}
		categories[domain] = category.ID
	}
	for _, project := range []struct{ id, domain string }{
		{"proj-a1", "domain-a"}, {"proj-a2", "domain-a"}, {"proj-b1", "domain-b"},
	} {
		if err := store.SaveProject(&storage.Project{
			ID: project.id, DomainID: project.domain, CategoryID: categories[project.domain],
			Name: project.id, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("SaveProject(%s): %v", project.id, err)
		}
	}
	for _, app := range []struct{ id, project string }{
		{"app-a1", "proj-a1"}, {"app-b1", "proj-b1"}, {"app-orphan", ""},
	} {
		if err := store.SaveApplication(&storage.Application{
			ID: app.id, ProjectID: app.project, Name: app.id, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("SaveApplication(%s): %v", app.id, err)
		}
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)
	return server
}

func roleSet(roles ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		out[role] = struct{}{}
	}
	return out
}

// listIDs invokes a list handler as the given user and returns the ids under key.
func listIDs(t *testing.T, server *Server, handler http.HandlerFunc, path, key string, user *AuthenticatedUser) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if user != nil {
		req = withAuthenticatedContext(req, authResolution{User: user})
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d, want 200 (%s)", path, rec.Code, rec.Body.String())
	}
	// The response also carries a numeric "count", so decode into RawMessage
	// and pull only the requested collection.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	var items []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload[key], &items); err != nil {
		t.Fatalf("decode %s[%s]: %v", path, key, err)
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func assertIDs(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	seen := make(map[string]struct{}, len(got))
	for _, id := range got {
		seen[id] = struct{}{}
	}
	for _, id := range want {
		if _, ok := seen[id]; !ok {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

// TestSystemAdminSeesEveryScope is the backward-compatibility guarantee: the
// superuser surface must not change.
func TestSystemAdminSeesEveryScope(t *testing.T) {
	server := scopeVisibilityServer(t)
	admin := &AuthenticatedUser{Subject: "admin", GlobalRoles: roleSet(RoleSystemAdmin)}

	assertIDs(t, "domains", listIDs(t, server, server.handleListDomains, "/api/v1/domains", "domains", admin),
		storage.DefaultDomainID, "domain-a", "domain-b")
	assertIDs(t, "projects", listIDs(t, server, server.handleListProjects, "/api/v1/projects", "projects", admin),
		storage.DefaultProjectID, "proj-a1", "proj-a2", "proj-b1")
	// "legacy" is seeded by the default hierarchy on the default project.
	assertIDs(t, "applications", listIDs(t, server, server.handleListApplications, "/api/v1/applications", "applications", admin),
		"legacy", "app-a1", "app-b1", "app-orphan")
}

// TestDomainBoundUserSeesOnlyTheirDomain covers the direct domain binding: a
// domain administrator gets that domain's whole project catalog without needing
// a binding on each project.
func TestDomainBoundUserSeesOnlyTheirDomain(t *testing.T) {
	server := scopeVisibilityServer(t)
	user := &AuthenticatedUser{
		Subject:     "domain-admin",
		GlobalRoles: roleSet(RoleViewer),
		DomainRoles: map[string]map[string]struct{}{"domain-a": roleSet(RoleProjectAdmin)},
	}

	assertIDs(t, "domains", listIDs(t, server, server.handleListDomains, "/api/v1/domains", "domains", user), "domain-a")
	assertIDs(t, "projects", listIDs(t, server, server.handleListProjects, "/api/v1/projects", "projects", user),
		"proj-a1", "proj-a2")
	assertIDs(t, "applications", listIDs(t, server, server.handleListApplications, "/api/v1/applications", "applications", user),
		"app-a1")
}

// TestProjectBoundUserSeesTheirProjectsDomain covers the other grant shape: a
// user with only a project binding must still see that project's domain, or the
// administrator surfaces render empty for them.
func TestProjectBoundUserSeesTheirProjectsDomain(t *testing.T) {
	server := scopeVisibilityServer(t)
	user := &AuthenticatedUser{
		Subject:      "project-admin",
		GlobalRoles:  roleSet(RoleViewer),
		ProjectRoles: map[string]map[string]struct{}{"proj-b1": roleSet(RoleProjectAdmin)},
	}

	assertIDs(t, "domains", listIDs(t, server, server.handleListDomains, "/api/v1/domains", "domains", user), "domain-b")
	// proj-b1 only: the binding is on the project, not on domain-b, so it does
	// not widen to that domain's other projects.
	assertIDs(t, "projects", listIDs(t, server, server.handleListProjects, "/api/v1/projects", "projects", user), "proj-b1")
	assertIDs(t, "applications", listIDs(t, server, server.handleListApplications, "/api/v1/applications", "applications", user),
		"app-b1")
}

// TestRepositoryBoundUserSeesTheirDomain covers the third grant shape the Access
// UI can create. application_repository is a first-class binding scope, and a
// user holding only that binding must still see the domain it belongs to.
func TestRepositoryBoundUserSeesTheirDomain(t *testing.T) {
	server := scopeVisibilityServer(t)
	user := &AuthenticatedUser{
		Subject:         "repo-admin",
		GlobalRoles:     roleSet(RoleViewer),
		RepositoryRoles: map[string]map[string]struct{}{"domain-b-repo": roleSet(RoleProjectAdmin)},
	}

	assertIDs(t, "domains", listIDs(t, server, server.handleListDomains, "/api/v1/domains", "domains", user), "domain-b")
	// The repository binding scopes the domain, and that domain's projects follow.
	assertIDs(t, "projects", listIDs(t, server, server.handleListProjects, "/api/v1/projects", "projects", user), "proj-b1")
}

// TestDomainIDQueryParamCannotEscapeScope proves the scope filter runs before
// the query-param filter, so ?domain_id= is a narrowing tool and never a way
// into a domain the caller holds no binding on.
func TestDomainIDQueryParamCannotEscapeScope(t *testing.T) {
	server := scopeVisibilityServer(t)
	user := &AuthenticatedUser{
		Subject:     "domain-admin",
		GlobalRoles: roleSet(RoleViewer),
		DomainRoles: map[string]map[string]struct{}{"domain-a": roleSet(RoleProjectAdmin)},
	}

	got := listIDs(t, server, server.handleListProjects, "/api/v1/projects?domain_id=domain-b", "projects", user)
	if len(got) != 0 {
		t.Fatalf("projects?domain_id=domain-b = %v, want none", got)
	}
}

// TestBareGlobalViewerSeesNothing pins the deliberate behavior change: a global
// viewer with no domain or project binding used to receive every row.
func TestBareGlobalViewerSeesNothing(t *testing.T) {
	server := scopeVisibilityServer(t)
	user := &AuthenticatedUser{Subject: "viewer", GlobalRoles: roleSet(RoleViewer)}

	if got := listIDs(t, server, server.handleListDomains, "/api/v1/domains", "domains", user); len(got) != 0 {
		t.Fatalf("domains = %v, want none", got)
	}
	if got := listIDs(t, server, server.handleListProjects, "/api/v1/projects", "projects", user); len(got) != 0 {
		t.Fatalf("projects = %v, want none", got)
	}
}

// TestUnresolvedIdentityFailsClosed guards the nil-user path: with auth enabled
// and no identity in context, the lists must be empty rather than falling back
// to returning everything.
func TestUnresolvedIdentityFailsClosed(t *testing.T) {
	server := scopeVisibilityServer(t)
	if got := listIDs(t, server, server.handleListDomains, "/api/v1/domains", "domains", nil); len(got) != 0 {
		t.Fatalf("domains = %v, want none", got)
	}
}

// TestAuthBypassModeSkipsScopeFiltering keeps local/dev deployments working:
// with auth disabled there is no identity to scope by, so nothing is filtered.
func TestAuthBypassModeSkipsScopeFiltering(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveDomain(&storage.Domain{ID: "domain-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveDomain: %v", err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	assertIDs(t, "domains", listIDs(t, server, server.handleListDomains, "/api/v1/domains", "domains", nil),
		storage.DefaultDomainID, "domain-a")
}

// TestOrphanApplicationFollowsTheDefaultProject documents the chosen rule for
// Application.ProjectID being optional: it normalizes to the default project
// rather than becoming invisible to every non-superuser.
func TestOrphanApplicationFollowsTheDefaultProject(t *testing.T) {
	server := scopeVisibilityServer(t)
	user := &AuthenticatedUser{
		Subject:      "default-project-admin",
		GlobalRoles:  roleSet(RoleViewer),
		ProjectRoles: map[string]map[string]struct{}{storage.DefaultProjectID: roleSet(RoleProjectAdmin)},
	}

	// Both the seeded "legacy" application and the orphan resolve to the
	// default project, so a binding on it surfaces exactly these two.
	assertIDs(t, "applications", listIDs(t, server, server.handleListApplications, "/api/v1/applications", "applications", user),
		"legacy", "app-orphan")
}
