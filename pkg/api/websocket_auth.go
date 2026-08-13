package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// websocketAuthorizer snapshots the authenticated transport identity and
// returns a cheap periodic revalidation closure. Long-lived sockets otherwise
// outlive user disablement, session epoch changes, project-role removal, API-key
// rotation, and credential expiry.
func (s *Server) websocketAuthorizer(r *http.Request, projectID string) func() bool {
	if s == nil || r == nil {
		return func() bool { return false }
	}
	settings := s.authSettingsForRequest()
	if isAuthBypassMode(settings.Mode) {
		return func() bool { return true }
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
	if account != nil && s.store != nil {
		stored, err := s.store.GetServiceAccount(account.ID)
		if err != nil || stored == nil {
			return func() bool { return false }
		}
		initialKeyHash = stored.KeyHash
	} else if settings.Mode == authModeLocal && snapshot.SessionScoped {
		cookie, err := r.Cookie(localSessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			return func() bool { return false }
		}
		localStore, ok := s.store.(localAuthStore)
		if !ok {
			return func() bool { return false }
		}
		generation, err := localStore.GetLocalAuthGeneration()
		expectedGeneration := s.activeLocalAuthGeneration()
		if err != nil || strings.TrimSpace(generation) == "" || strings.TrimSpace(expectedGeneration) == "" ||
			subtle.ConstantTimeCompare([]byte(generation), []byte(expectedGeneration)) != 1 {
			return func() bool { return false }
		}
		localSessionGeneration = expectedGeneration
		localSessionHash = localSessionTokenHashForGeneration(generation, cookie.Value)
	}

	return func() bool {
		now := time.Now().UTC()
		if snapshot.SessionScoped && !snapshot.SessionExpiresAt.IsZero() && !now.Before(snapshot.SessionExpiresAt) {
			return false
		}

		current := &snapshot
		if s.store != nil && account != nil {
			stored, err := s.store.GetServiceAccount(account.ID)
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
			if !serviceAccountAllowsRequest(currentAccount, r) {
				return false
			}
			current = authenticatedUserFromServiceAccount(stored)
		} else if s.store != nil && strings.TrimSpace(snapshot.Subject) != "" {
			stored, err := s.store.GetUserBySubject(snapshot.Subject)
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
			localStore := s.store.(localAuthStore)
			session, err := localStore.GetLocalAuthSessionByTokenHashForGeneration(localSessionGeneration, localSessionHash)
			if err != nil || session == nil || !now.Before(session.ExpiresAt.UTC()) {
				return false
			}
			if strings.TrimSpace(session.UserID) != strings.TrimSpace(snapshot.ID) || session.SessionEpoch != snapshot.SessionEpoch {
				return false
			}
		}

		return s.websocketUserCanViewProject(current, projectID)
	}
}

func (s *Server) websocketUserCanViewProject(user *AuthenticatedUser, projectID string) bool {
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
	return userHasProjectOrParentRole(s.store, user, projectID, RoleViewer, RoleEditor, RoleProjectAdmin)
}

// Compile-time documentation of the store surface used by websocket
// revalidation. storage.Store already supplies these methods; keeping the
// assertion close to the feature makes accidental interface erosion obvious.
var _ interface {
	GetUserBySubject(string) (*storage.User, error)
	GetServiceAccount(string) (*storage.ServiceAccount, error)
} = (storage.Store)(nil)
