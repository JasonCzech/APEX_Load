package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type browserResourceContextAuthStore interface {
	GetUserBySubjectContext(context.Context, string) (*storage.User, error)
	GetServiceAccountContext(context.Context, string) (*storage.ServiceAccount, error)
	GetProjectContext(context.Context, string) (*storage.Project, error)
	GetRepositoryCategoryContext(context.Context, string) (*storage.RepositoryCategory, error)
	GetApplicationRepositoryContext(context.Context, string) (*storage.ApplicationRepository, error)
	GetLocalAuthGenerationContext(context.Context) (string, error)
	GetLocalAuthSessionByTokenHashForGenerationContext(context.Context, string, string) (*storage.LocalAuthSession, error)
}

// This view forwards the established Store surface and only substitutes the
// authorizer's reads. No helper goroutine can outlive a timed-out resource page.
type browserResourceFinalAuthStore struct {
	storage.Store
	localAuthStore
	ctx       context.Context
	authority browserResourceContextAuthStore
}

func (v *browserResourceFinalAuthStore) GetUserBySubject(subject string) (*storage.User, error) {
	return v.authority.GetUserBySubjectContext(v.ctx, subject)
}
func (v *browserResourceFinalAuthStore) GetServiceAccount(id string) (*storage.ServiceAccount, error) {
	return v.authority.GetServiceAccountContext(v.ctx, id)
}
func (v *browserResourceFinalAuthStore) GetProject(id string) (*storage.Project, error) {
	return v.authority.GetProjectContext(v.ctx, id)
}
func (v *browserResourceFinalAuthStore) GetRepositoryCategory(id string) (*storage.RepositoryCategory, error) {
	return v.authority.GetRepositoryCategoryContext(v.ctx, id)
}
func (v *browserResourceFinalAuthStore) GetApplicationRepository(id string) (*storage.ApplicationRepository, error) {
	return v.authority.GetApplicationRepositoryContext(v.ctx, id)
}
func (v *browserResourceFinalAuthStore) GetLocalAuthGeneration() (string, error) {
	return v.authority.GetLocalAuthGenerationContext(v.ctx)
}
func (v *browserResourceFinalAuthStore) GetLocalAuthSessionByTokenHashForGeneration(generation, hash string) (*storage.LocalAuthSession, error) {
	return v.authority.GetLocalAuthSessionByTokenHashForGenerationContext(v.ctx, generation, hash)
}

func (s *Server) browserResourceAuthorizer(r *http.Request, project string) (func() bool, error) {
	if s == nil || r == nil || s.store == nil {
		return nil, errBrowserResourceUnavailable
	}
	ctx := r.Context()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contextual, ok := s.store.(browserResourceContextAuthStore)
	if !ok {
		return nil, errBrowserResourceUnavailable
	}
	local, ok := s.store.(localAuthStore)
	if !ok {
		return nil, errBrowserResourceUnavailable
	}
	settings := normalizeAuthSettings(AuthSettings{})
	if s.runner != nil {
		// Do not let the existing legacy settings fallback issue a contextless read.
		if s.runner.settingsStore != nil {
			if _, ok := s.runner.settingsStore.(contextualSettingsPersistence); !ok {
				return nil, errBrowserResourceUnavailable
			}
		}
		if err := s.runner.refreshSystemSettingsFromStoreContext(ctx, false); err != nil {
			return nil, browserResourceContextOr(ctx, errBrowserResourceUnavailable)
		}
		s.runner.mu.RLock()
		settings = s.runner.settings.Auth
		s.runner.mu.RUnlock()
		settings = s.authSettingsForRuntime(settings)
	}
	view := &browserResourceFinalAuthStore{Store: s.store, localAuthStore: local, ctx: ctx, authority: contextual}
	return browserResourceSnapshotRevalidator(r, project, settings, view, s.activeLocalAuthGeneration()), nil
}

// The decision rules intentionally match websocketAuthorizer: current user
// activity/roles, session epoch/exact local session, account key/scope/expiry and
// Local Test Login's existing parent-scope inheritance are revalidated. The
// only resource-specific additions are contextual reads and cancellation.
func browserResourceSnapshotRevalidator(r *http.Request, projectID string, settings AuthSettings, store storage.Store, expectedLocalGeneration string) func() bool {
	ctx := r.Context()
	// This reader serves one fixed GET route. Derive the same pure static scope
	// that the catalog binds, without recursively consulting the catalog from
	// its own handler initialization dependency. Unrelated routes fail closed.
	if r.Method != http.MethodGet || !routePathTemplateMatches("/api/v1/tests/{id}/archive/browser-resources", r.URL.Path) {
		return func() bool { return false }
	}
	requiredScope := staticServiceAccountScopeForRoute(r.Method, r.URL.Path)
	if isAuthBypassMode(settings.Mode) {
		return func() bool { return ctx.Err() == nil }
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	account, _ := AuthenticatedServiceAccountFromContext(r.Context())
	if user == nil {
		return func() bool { return false }
	}

	projectID = strings.TrimSpace(projectID)
	snapshot := *user
	initialKeyHash := ""
	localSessionHash := ""
	localSessionGeneration := ""
	if account != nil && store != nil {
		stored, err := store.GetServiceAccount(account.ID)
		if err != nil || stored == nil {
			return func() bool { return false }
		}
		// Pin the credential that authenticated this request, not whichever
		// key is current after middleware. Rotation can occur in this gap.
		presented := extractAPIKey(r)
		if strings.TrimSpace(presented) == "" {
			return func() bool { return false }
		}
		initialKeyHash = hashAPIKey(presented)
		if subtle.ConstantTimeCompare([]byte(stored.KeyHash), []byte(initialKeyHash)) != 1 {
			return func() bool { return false }
		}
	} else if settings.Mode == authModeLocal && snapshot.SessionScoped {
		cookie, err := r.Cookie(localSessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			return func() bool { return false }
		}
		localStore, ok := store.(localAuthStore)
		if !ok {
			return func() bool { return false }
		}
		generation, err := localStore.GetLocalAuthGeneration()
		expectedGeneration := expectedLocalGeneration
		if err != nil || strings.TrimSpace(generation) == "" || strings.TrimSpace(expectedGeneration) == "" ||
			subtle.ConstantTimeCompare([]byte(generation), []byte(expectedGeneration)) != 1 {
			return func() bool { return false }
		}
		localSessionGeneration = expectedGeneration
		localSessionHash = localSessionTokenHashForGeneration(generation, cookie.Value)
	}

	return func() bool {
		if ctx.Err() != nil {
			return false
		}
		now := time.Now().UTC()
		if snapshot.SessionScoped && !snapshot.SessionExpiresAt.IsZero() && !now.Before(snapshot.SessionExpiresAt) {
			return false
		}

		current := &snapshot
		if store != nil && account != nil {
			stored, err := store.GetServiceAccount(account.ID)
			if err != nil || stored == nil || !stored.Active || (stored.ExpiresAt != nil && !now.Before(stored.ExpiresAt.UTC())) {
				return false
			}
			if initialKeyHash == "" || subtle.ConstantTimeCompare([]byte(stored.KeyHash), []byte(initialKeyHash)) != 1 {
				return false
			}
			currentAccount := &AuthenticatedServiceAccount{
				ID:          stored.ID,
				Name:        stored.Name,
				Description: stored.Description,
				Scopes:      normalizeAuthenticatedServiceAccountScopes(stored.Scopes),
			}
			if !serviceAccountAllowsScopes(currentAccount, []string{requiredScope}) {
				return false
			}
			current = authenticatedUserFromServiceAccount(stored)
		} else if store != nil && strings.TrimSpace(snapshot.Subject) != "" {
			stored, err := store.GetUserBySubject(snapshot.Subject)
			if err != nil || stored == nil || !stored.Active {
				return false
			}
			if snapshot.SessionScoped && snapshot.SessionEpoch < stored.SessionEpoch {
				return false
			}
			current = authenticatedUserFromStoredUser(stored)
			current.LocalScopeBoundViewer = snapshot.LocalScopeBoundViewer
		}
		if localSessionHash != "" {
			localStore := store.(localAuthStore)
			session, err := localStore.GetLocalAuthSessionByTokenHashForGeneration(localSessionGeneration, localSessionHash)
			if err != nil || session == nil || !now.Before(session.ExpiresAt.UTC()) {
				return false
			}
			if strings.TrimSpace(session.UserID) != strings.TrimSpace(snapshot.ID) || session.SessionEpoch != snapshot.SessionEpoch {
				return false
			}
		}

		return ctx.Err() == nil && browserResourceUserCanViewProject(store, current, projectID)
	}
}

func browserResourceUserCanViewProject(store storage.Store, user *AuthenticatedUser, projectID string) bool {
	if user == nil {
		return false
	}
	// Local Test Login's baseline viewer is scope-bound, just as it is for HTTP
	// resource routes. Established providers retain genuinely global viewers.
	if user.hasGlobalRole(RoleEditor, RoleProjectAdmin, RoleSystemAdmin) || (user.hasGlobalRole(RoleViewer) && !user.LocalScopeBoundViewer) {
		return true
	}
	if strings.TrimSpace(projectID) == "" {
		return false
	}
	return userHasProjectOrParentRole(store, user, projectID, RoleViewer, RoleEditor, RoleProjectAdmin)
}
