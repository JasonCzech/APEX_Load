package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// errScopeCategoryUnresolvable is what the counting store returns for the
// category placement it is told to hide. Any error puts repositoryIDForProject
// on its domain-repository fallback, so the exact value does not matter.
var errScopeCategoryUnresolvable = errors.New("repository category not found")

// scopeReadCountingStore counts the per-binding scope resolution reads that
// canManageBinding performs. Those reads are the N+1 that a scoped-admin user
// listing used to pay once per binding per user (twice, in fact: once for the
// visibility decision and again for the binding projection).
//
// unresolvableCategoryID makes GetRepositoryCategory fail for one category, so
// that project resolves through repositoryIDForProject's domain-repository
// fallback (handlers_auth.go) instead of its category. That emulation is the
// only way to reach the fallback: SaveProject rejects a project whose category
// does not belong to its domain, and GetProject normalizes an empty category id
// to the seeded "uncategorized" category, so every project a SQLite-backed
// fixture can create resolves through the category branch. In production the
// fallback is reached the same way: repositoryIDForProject treats any
// GetRepositoryCategory error as "no category placement", so a row that went
// missing out of band or a transient read error both land on it.
type scopeReadCountingStore struct {
	Store                  storage.Store
	unresolvableCategoryID string
	mu                     sync.Mutex
	getProject             int
	getCategory            int
	getAppRepository       int
	listUsersCallCount     int
}

func (s *scopeReadCountingStore) GetProject(id string) (*storage.Project, error) {
	s.mu.Lock()
	s.getProject++
	s.mu.Unlock()
	return s.Store.GetProject(id)
}

func (s *scopeReadCountingStore) GetRepositoryCategory(id string) (*storage.RepositoryCategory, error) {
	s.mu.Lock()
	s.getCategory++
	unresolvable := s.unresolvableCategoryID != "" && id == s.unresolvableCategoryID
	s.mu.Unlock()
	if unresolvable {
		return nil, errScopeCategoryUnresolvable
	}
	return s.Store.GetRepositoryCategory(id)
}

func (s *scopeReadCountingStore) GetApplicationRepository(id string) (*storage.ApplicationRepository, error) {
	s.mu.Lock()
	s.getAppRepository++
	s.mu.Unlock()
	return s.Store.GetApplicationRepository(id)
}

func (s *scopeReadCountingStore) ListUsers(filter storage.UserFilter) ([]*storage.User, error) {
	s.mu.Lock()
	s.listUsersCallCount++
	s.mu.Unlock()
	return s.Store.ListUsers(filter)
}

func (s *scopeReadCountingStore) counts() (project, category, repository int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getProject, s.getCategory, s.getAppRepository
}

// TestListVisibleUsersResolvesEachBindingScopeOnce pins the O7 remediation. A
// repository-scoped project_admin listing users must resolve each distinct
// binding scope once, not once per binding per user per pass.
//
// Without the memoization this fixture pays 4 GetProject reads per user (one
// from userVisibleToRequest's first manageable binding plus three from the
// binding projection), i.e. 32 for 8 users over 3 distinct projects, alongside
// 32 GetRepositoryCategory and 8 GetApplicationRepository reads. Afterwards each
// counter is bounded by the number of distinct project scopes that reach it.
//
// The fixture mixes both shapes repositoryIDForProject can take: two projects
// resolve their repository through GetRepositoryCategory, and one resolves
// through the GetApplicationRepository fallback. All three counters are
// therefore load-bearing — with a category-only fixture the repository counter
// stays 0 and its bound holds no matter how badly the fallback regresses.
func TestListVisibleUsersResolvesEachBindingScopeOnce(t *testing.T) {
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "user-visibility-scale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()

	now := time.Now().UTC()
	domain := &storage.Domain{ID: "vis-domain", Name: "Visibility Domain", CreatedAt: now}
	repository := &storage.ApplicationRepository{ID: "vis-repository", DomainID: domain.ID, Name: "Visibility Repository", CreatedAt: now}
	category := &storage.RepositoryCategory{ID: "vis-category", DomainID: domain.ID, RepositoryID: repository.ID, Name: "Visibility Category", CreatedAt: now}
	if err := sqlite.CreateDomainHierarchy(domain, repository, category); err != nil {
		t.Fatal(err)
	}
	// legacyCategory is the placement the counting store hides below, which puts
	// its project on repositoryIDForProject's domain-repository fallback. It
	// still points at the same repository, so the caller administers all three
	// projects either way and only the read counters differ.
	legacyCategory := &storage.RepositoryCategory{ID: "vis-category-legacy", DomainID: domain.ID, RepositoryID: repository.ID, Name: "Legacy Category", CreatedAt: now}
	if err := sqlite.SaveRepositoryCategory(legacyCategory); err != nil {
		t.Fatal(err)
	}
	const legacyProjectID = "vis-project-legacy"
	projectIDs := []string{"vis-project-a", "vis-project-b", legacyProjectID}
	for _, projectID := range projectIDs {
		categoryID := category.ID
		if projectID == legacyProjectID {
			categoryID = legacyCategory.ID
		}
		project := &storage.Project{ID: projectID, DomainID: domain.ID, CategoryID: categoryID, Name: projectID, CreatedAt: now, UpdatedAt: now}
		if err := sqlite.SaveProject(project); err != nil {
			t.Fatal(err)
		}
	}

	const userCount = 8
	for index := 0; index < userCount; index++ {
		userID := fmt.Sprintf("vis-user-%d", index)
		user := &storage.User{
			ID: userID, Subject: userID, DisplayName: userID, Active: true,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := sqlite.SaveUser(user); err != nil {
			t.Fatal(err)
		}
		for _, projectID := range projectIDs {
			binding := &storage.UserRoleBinding{
				ID: userID + "-" + projectID, UserID: userID,
				ScopeType: "project", ScopeID: projectID, Role: RoleEditor,
				CreatedAt: now, UpdatedAt: now,
			}
			if err := sqlite.SaveUserRoleBinding(binding); err != nil {
				t.Fatal(err)
			}
		}
	}

	counting := &scopeReadCountingStore{Store: sqlite, unresolvableCategoryID: legacyCategory.ID}
	server := NewServer(ServerConfig{Store: storeOverlay(counting)})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
	caller := &AuthenticatedUser{
		ID:              "repo-admin",
		Subject:         "repo-admin",
		GlobalRoles:     map[string]struct{}{},
		RepositoryRoles: map[string]map[string]struct{}{repository.ID: {RoleProjectAdmin: {}}},
	}
	req = req.WithContext(context.WithValue(req.Context(), authUserContextKey{}, caller))

	// Reset counters so only the listing's reads are measured.
	counting.mu.Lock()
	counting.getProject, counting.getCategory, counting.getAppRepository = 0, 0, 0
	counting.mu.Unlock()

	users, total, err := server.listVisibleUsersForRequest(req, storage.UserFilter{Limit: 50})
	if err != nil {
		t.Fatalf("listVisibleUsersForRequest returned error: %v", err)
	}

	// Correctness first: the scoped admin still sees every user and every
	// binding it administers.
	if total != userCount {
		t.Fatalf("total = %d, want %d", total, userCount)
	}
	if len(users) != userCount {
		t.Fatalf("visible users = %d, want %d", len(users), userCount)
	}
	for _, user := range users {
		if len(user.Bindings) != len(projectIDs) {
			t.Fatalf("user %s exposed %d bindings, want %d", user.ID, len(user.Bindings), len(projectIDs))
		}
	}

	projectReads, categoryReads, repositoryReads := counting.counts()
	t.Logf("scope resolution reads: project=%d category=%d repository=%d (%d distinct project scopes, %d users)",
		projectReads, categoryReads, repositoryReads, len(projectIDs), userCount)

	// Upper bounds: each distinct project scope costs one resolution no matter
	// how many users and passes reference it. Reported with Errorf, not Fatalf,
	// so a regression shows which of the three paths lost its memoization.
	if projectReads > len(projectIDs) {
		t.Errorf("GetProject called %d times for %d distinct project scopes across %d users; scope resolution is not memoized", projectReads, len(projectIDs), userCount)
	}
	if categoryReads > len(projectIDs) {
		t.Errorf("GetRepositoryCategory called %d times for %d distinct project scopes", categoryReads, len(projectIDs))
	}
	// Exactly one project (legacyProjectID) reaches the domain-repository
	// fallback, so one read covers every user and both passes.
	const fallbackProjects = 1
	if repositoryReads > fallbackProjects {
		t.Errorf("GetApplicationRepository called %d times for %d project scope resolved through the domain-repository fallback across %d users; the fallback path is not memoized", repositoryReads, fallbackProjects, userCount)
	}

	// Lower bounds: a counter that never moves makes its bound above vacuous,
	// because the fixture has stopped executing that resolution path at all.
	if projectReads == 0 || categoryReads == 0 || repositoryReads == 0 {
		t.Errorf("fixture no longer exercises every scope-resolution path (project=%d category=%d repository=%d); the bounds above are vacuous for any path reading 0",
			projectReads, categoryReads, repositoryReads)
	}
}

// TestUserVisibilityResolverMatchesPerRequestHelpers guards the memoization
// against drift: the cached resolver must agree with the uncached per-request
// helpers for every binding shape, including the ones it must deny.
//
// The fixture is built so that dropping any one of userBindingScopeKey's three
// fields (handlers_auth.go) makes the resolver disagree with the oracle:
//   - role: binding project/project-a/system_admin collides with
//     project/project-a/editor, which the caller may manage.
//   - scope_id: project-a (manageable) collides with project-b (not).
//   - scope_type: domain/domain-a and application_repository/repo-a (both
//     manageable) collide with project bindings carrying those same scope IDs,
//     which are not manageable. Scope IDs are only unique within their own
//     table — validateUserBindingScope checks project IDs against projects,
//     domain IDs against domains and repository IDs against application
//     repositories — so a project may legitimately share an ID string with a
//     domain or a repository, and canManageBinding gives them different
//     answers. Order matters: the manageable domain/repository bindings come
//     first so the true decision is what a scope_type-less key would wrongly
//     serve back to the project bindings below them.
func TestUserVisibilityResolverMatchesPerRequestHelpers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/users", nil)
	caller := &AuthenticatedUser{
		GlobalRoles:     map[string]struct{}{},
		ProjectRoles:    map[string]map[string]struct{}{"project-a": {RoleProjectAdmin: {}}},
		DomainRoles:     map[string]map[string]struct{}{"domain-a": {RoleProjectAdmin: {}}},
		RepositoryRoles: map[string]map[string]struct{}{"repo-a": {RoleProjectAdmin: {}}},
	}
	req = req.WithContext(context.WithValue(req.Context(), authUserContextKey{}, caller))

	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "resolver-equivalence.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	server := NewServer(ServerConfig{Store: sqlite})
	settings := server.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	bindings := []*storage.UserRoleBinding{
		{ScopeType: "project", ScopeID: "project-a", Role: RoleEditor},
		{ScopeType: "project", ScopeID: "project-a", Role: RoleViewer},
		{ScopeType: "project", ScopeID: "project-b", Role: RoleEditor},
		{ScopeType: "domain", ScopeID: "domain-a", Role: RoleViewer},
		{ScopeType: "domain", ScopeID: "domain-b", Role: RoleViewer},
		{ScopeType: "application_repository", ScopeID: "repo-a", Role: RoleViewer},
		// Cross-scope-type collisions. No project named "domain-a" or "repo-a"
		// exists in the store, so canManageBinding denies both, while the
		// same-scope-ID domain and repository bindings above are allowed.
		{ScopeType: "project", ScopeID: "domain-a", Role: RoleViewer},
		{ScopeType: "project", ScopeID: "repo-a", Role: RoleViewer},
		{ScopeType: "global", Role: RoleViewer},
		{ScopeType: "project", ScopeID: "project-a", Role: RoleSystemAdmin},
	}

	resolver := server.newUserVisibilityResolver(req)
	for _, binding := range bindings {
		want := canManageBinding(req, server.store, binding)
		// Call twice so the memoized answer is exercised too.
		for attempt := 0; attempt < 2; attempt++ {
			if got := resolver.canManageBinding(binding); got != want {
				t.Fatalf("resolver.canManageBinding(%s/%s/%s) attempt %d = %v, want %v",
					binding.ScopeType, binding.ScopeID, binding.Role, attempt, got, want)
			}
		}
	}

	user := &storage.User{ID: "u1", Subject: "u1", Active: true, Bindings: bindings}
	got := resolver.visibleUser(user)
	if got == nil {
		t.Fatal("visibleUser returned nil")
	}
	wantVisible := 0
	for _, binding := range bindings {
		if canManageBinding(req, server.store, binding) {
			wantVisible++
		}
	}
	if len(got.Bindings) != wantVisible {
		t.Fatalf("visibleUser exposed %d bindings, want %d", len(got.Bindings), wantVisible)
	}
	if !resolver.userVisible(user) {
		t.Fatal("userVisible = false for a user with a manageable binding")
	}
	if resolver.userVisible(&storage.User{ID: "u2", Bindings: []*storage.UserRoleBinding{{ScopeType: "global", Role: RoleViewer}}}) {
		t.Fatal("userVisible = true for a user whose only binding is global")
	}
}
