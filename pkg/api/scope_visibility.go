package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Scope visibility for list endpoints.
//
// Per-scope authorization on the by-ID routes is enforced by
// authorizeAccessRequirement, but the collection routes (`GET /domains`,
// `/projects`, `/applications`) historically returned every row to anyone who
// cleared the route's global-role bar. That leaked the names of domains,
// projects, and applications a caller has no binding on.
//
// These helpers narrow the collection responses to the caller's own scopes.
// They govern visibility only — a caller who can see a domain still needs the
// route's own requirement to act on it.

// requestHasUnrestrictedScopeVisibility reports whether the caller sees every
// domain, project, and application regardless of bindings.
//
// This deliberately does NOT treat a bare global `viewer` as unrestricted, even
// though authorizeAccessRequirement short-circuits on any matching global role.
// Scoping the administrator surfaces is the entire point: an operator who hands
// out global viewer alongside a domain binding expects the domain binding to be
// what bounds the list.
func (s *Server) requestHasUnrestrictedScopeVisibility(r *http.Request) bool {
	if isAuthBypassMode(normalizeAuthSettings(s.runner.GetSystemSettings().Auth).Mode) {
		return true
	}
	user, ok := AuthenticatedUserFromContext(r.Context())
	if !ok || user == nil {
		// Auth is enabled and no identity resolved. Fail closed rather than
		// falling back to the pre-scoping behavior of returning everything.
		return false
	}
	return user.hasGlobalRole(RoleSystemAdmin)
}

// visibleDomainIDsForUser is the union of the domains a user holds a direct
// binding on and the domains owning the projects they hold a binding on.
//
// The project half matters because project bindings are the common grant: a
// user given project_admin on one project must still see that project's domain
// in the picker, or the administrator surfaces render empty for them.
func visibleDomainIDsForUser(user *AuthenticatedUser, projects []*storage.Project) map[string]struct{} {
	visible := make(map[string]struct{})
	if user == nil {
		return visible
	}
	for domainID := range user.DomainRoles {
		visible[normalizeDomainID(domainID)] = struct{}{}
	}
	for _, project := range projects {
		if project == nil {
			continue
		}
		if _, ok := user.ProjectRoles[normalizeProjectID(project.ID)]; ok {
			visible[normalizeDomainID(project.DomainID)] = struct{}{}
		}
	}
	return visible
}

// addRepositoryBoundDomains adds the domains whose application repository the
// user holds a binding on.
//
// application_repository is a first-class binding scope the Access UI can grant,
// and a repository belongs to exactly one domain. Without this a user granted
// only a repository role would see an empty domain list and the repository page
// they DO have access to would have nothing to select.
//
// The lookup is keyed by domain, so this walks the domain list — it runs only
// for the users who actually carry repository bindings, and domain counts are
// administrator-scale. Failures are skipped rather than fatal: a missing
// repository must not break listing.
func (s *Server) addRepositoryBoundDomains(user *AuthenticatedUser, domains map[string]struct{}) {
	if user == nil || len(user.RepositoryRoles) == 0 {
		return
	}
	allDomains, err := s.store.ListDomains()
	if err != nil {
		return
	}
	for _, domain := range allDomains {
		if domain == nil {
			continue
		}
		if _, already := domains[normalizeDomainID(domain.ID)]; already {
			continue
		}
		repository, err := s.store.GetApplicationRepository(domain.ID)
		if err != nil || repository == nil {
			continue
		}
		if _, ok := user.RepositoryRoles[repository.ID]; ok {
			domains[normalizeDomainID(domain.ID)] = struct{}{}
		}
	}
}

// visibleScopeForRequest resolves the caller's visible domain and project sets
// in one pass. Returns unrestricted=true when no filtering should be applied.
//
// A project is visible when the caller holds a binding on it OR on its domain,
// so a domain-scoped administrator sees that domain's whole project catalog
// without needing a binding per project.
//
// knownProjects lets a caller that has already read the project catalog pass it
// in rather than paying for a second read of the same table on every request.
// Pass nil to have it loaded here.
func (s *Server) visibleScopeForRequest(r *http.Request, knownProjects []*storage.Project) (domains map[string]struct{}, projects map[string]struct{}, unrestricted bool, err error) {
	if s.requestHasUnrestrictedScopeVisibility(r) {
		return nil, nil, true, nil
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	allProjects := knownProjects
	if allProjects == nil {
		if allProjects, err = s.store.ListProjects(); err != nil {
			return nil, nil, false, err
		}
	}
	domains = visibleDomainIDsForUser(user, allProjects)
	s.addRepositoryBoundDomains(user, domains)
	projects = make(map[string]struct{})
	if user == nil {
		return domains, projects, false, nil
	}
	for _, project := range allProjects {
		if project == nil {
			continue
		}
		projectID := normalizeProjectID(project.ID)
		if _, ok := user.ProjectRoles[projectID]; ok {
			projects[projectID] = struct{}{}
			continue
		}
		if _, ok := domains[normalizeDomainID(project.DomainID)]; ok {
			projects[projectID] = struct{}{}
		}
	}
	return domains, projects, false, nil
}
