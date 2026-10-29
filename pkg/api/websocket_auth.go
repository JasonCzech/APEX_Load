package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// websocketAuthorizer snapshots the authenticated transport identity and
// returns a cheap periodic revalidation closure. Long-lived sockets otherwise
// outlive user disablement, session epoch changes, project-role removal, API-key
// rotation, and credential expiry.
func (s *Server) websocketAuthorizer(r *http.Request, projectID string) func() bool {
	authorize := s.websocketScopedAuthorizer(r)
	return func() bool { return authorize(projectID, false) }
}

// websocketScopedAuthorizer revalidates the transport identity against the
// event's project. Debugger events carry raw session data and require editor
// access even on a global viewer socket.
func (s *Server) websocketScopedAuthorizer(r *http.Request) func(string, bool) bool {
	authorize := s.websocketBoundedScopedAuthorizer(r)
	return func(projectID string, requireEditor bool) bool {
		return authorize(context.Background(), projectID, requireEditor, requireEditor)
	}
}

// websocketBoundedScopedAuthorizer also supports a bounded viewer-level check
// before the HTTP upgrade, when the original request can still be canceled.
func (s *Server) websocketBoundedScopedAuthorizer(r *http.Request) func(context.Context, string, bool, bool) bool {
	if s == nil || r == nil {
		return func(context.Context, string, bool, bool) bool { return false }
	}
	settings, admitted := r.Context().Value(websocketAuthSettingsContextKey{}).(AuthSettings)
	if !admitted {
		settings = s.authSettingsForRequest()
	}
	// A connection is authenticated under the settings active at upgrade. A
	// live provider change must not let that old identity (or an old bypass
	// socket) receive credentials under the replacement provider. Once a change
	// has been observed, the old socket stays revoked even if settings revert.
	var settingsRevoked atomic.Bool
	settingsCurrent := func(ctx context.Context, bounded bool) bool {
		if settingsRevoked.Load() {
			return false
		}
		var current AuthSettings
		if bounded {
			var err error
			current, err = s.websocketDurableAuthSettingsContext(ctx)
			if err != nil {
				// Do not disclose raw debugger data while the authority cannot be
				// read. A transient failure may recover without revoking the socket.
				return false
			}
		} else {
			current = s.authSettingsForRequest()
		}
		if !reflect.DeepEqual(settings, current) {
			settingsRevoked.Store(true)
			return false
		}
		return true
	}
	if resolution, _, ok := devPreviewResolutionForRequest(settings, r); ok {
		return func(parent context.Context, projectID string, requireEditor, bounded bool) bool {
			ctx, cancel := websocketDeliveryAuthContext(parent, bounded)
			defer cancel()
			if !settingsCurrent(ctx, bounded) {
				return false
			}
			if requireEditor {
				return s.websocketUserCanEditProject(resolution.User, projectID) && ctx.Err() == nil
			}
			return s.websocketUserCanViewProject(resolution.User, projectID) && ctx.Err() == nil
		}
	}
	if isAuthBypassMode(settings.Mode) {
		return func(parent context.Context, _ string, _, bounded bool) bool {
			ctx, cancel := websocketDeliveryAuthContext(parent, bounded)
			defer cancel()
			return settingsCurrent(ctx, bounded) && ctx.Err() == nil
		}
	}
	user, _ := AuthenticatedUserFromContext(r.Context())
	account, _ := AuthenticatedServiceAccountFromContext(r.Context())
	if user == nil {
		return func(context.Context, string, bool, bool) bool { return false }
	}

	snapshot := *user
	initialKeyHash := ""
	localSessionHash := ""
	localSessionGeneration := ""
	if account != nil && s.store != nil {
		var stored *storage.ServiceAccount
		var err, ctxErr error
		if store, ok := s.store.(browserResourceContextAuthStore); ok {
			ctx, cancel := context.WithTimeout(r.Context(), defaultCacheWriteTimeout)
			stored, err = store.GetServiceAccountContext(ctx, account.ID)
			ctxErr = ctx.Err()
			cancel()
		} else {
			// Compatibility for legacy stores on ordinary streams. Raw debugger
			// delivery independently requires the contextual authority surface.
			stored, err = s.store.GetServiceAccount(account.ID)
		}
		if err != nil || ctxErr != nil || stored == nil {
			return func(context.Context, string, bool, bool) bool { return false }
		}
		// Bind this socket to the credential that authenticated its transport.
		// Rotation can occur between middleware admission and this snapshot.
		presented := extractAPIKey(r)
		if strings.TrimSpace(presented) == "" {
			return func(context.Context, string, bool, bool) bool { return false }
		}
		initialKeyHash = hashAPIKey(presented)
		if subtle.ConstantTimeCompare([]byte(stored.KeyHash), []byte(initialKeyHash)) != 1 {
			return func(context.Context, string, bool, bool) bool { return false }
		}
	} else if settings.Mode == authModeLocal && snapshot.SessionScoped {
		cookie, err := r.Cookie(localSessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			return func(context.Context, string, bool, bool) bool { return false }
		}
		var generation string
		var ctxErr error
		if localStore, ok := s.store.(browserResourceContextAuthStore); ok {
			ctx, cancel := context.WithTimeout(r.Context(), defaultCacheWriteTimeout)
			generation, err = localStore.GetLocalAuthGenerationContext(ctx)
			ctxErr = ctx.Err()
			cancel()
		} else if localStore, ok := s.store.(localAuthStore); ok {
			generation, err = localStore.GetLocalAuthGeneration()
		} else {
			return func(context.Context, string, bool, bool) bool { return false }
		}
		expectedGeneration := s.activeLocalAuthGeneration()
		if err != nil || ctxErr != nil || strings.TrimSpace(generation) == "" || strings.TrimSpace(expectedGeneration) == "" ||
			subtle.ConstantTimeCompare([]byte(generation), []byte(expectedGeneration)) != 1 {
			return func(context.Context, string, bool, bool) bool { return false }
		}
		localSessionGeneration = expectedGeneration
		localSessionHash = localSessionTokenHashForGeneration(generation, cookie.Value)
	}

	return func(parent context.Context, projectID string, requireEditor, bounded bool) bool {
		ctx, cancel := websocketDeliveryAuthContext(parent, bounded)
		defer cancel()
		if !settingsCurrent(ctx, bounded) {
			return false
		}
		store := s.store
		if bounded && store != nil {
			authority, ok := store.(browserResourceContextAuthStore)
			if !ok {
				return false
			}
			local, _ := store.(localAuthStore)
			store = &browserResourceFinalAuthStore{Store: store, localAuthStore: local, ctx: ctx, authority: authority}
		}
		now := time.Now().UTC()
		if snapshot.SessionScoped && !snapshot.SessionExpiresAt.IsZero() && !now.Before(snapshot.SessionExpiresAt) {
			return false
		}

		current := &snapshot
		var accountExpiresAt *time.Time
		var localSessionExpiresAt time.Time
		if store != nil && account != nil {
			stored, err := store.GetServiceAccount(account.ID)
			if err != nil || stored == nil || !stored.Active || (stored.ExpiresAt != nil && !now.Before(stored.ExpiresAt.UTC())) {
				return false
			}
			if initialKeyHash == "" || subtle.ConstantTimeCompare([]byte(stored.KeyHash), []byte(initialKeyHash)) != 1 {
				return false
			}
			accountExpiresAt = stored.ExpiresAt
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
		} else if store != nil && strings.TrimSpace(snapshot.Subject) != "" && strings.TrimSpace(snapshot.ID) == "" && !productionMode() &&
			((settings.Mode == authModeTrustedHeaders && !snapshot.SessionScoped) ||
				(settings.Mode == authModeOIDC && snapshot.SessionScoped && !snapshot.SessionExpiresAt.IsZero())) {
			// Development bootstrap permits an upstream identity only while the
			// local user store is empty. A signed OIDC session also needs a real
			// expiry, checked above. Recheck the empty store on every delivery so
			// onboarding closes the bootstrap window immediately.
			if bounded {
				presence, ok := s.store.(interface {
					HasUsersContext(context.Context) (bool, error)
				})
				if !ok {
					return false
				}
				hasUsers, err := presence.HasUsersContext(ctx)
				if err != nil || hasUsers {
					return false
				}
			} else {
				users, err := store.ListUsers(storage.UserFilter{Limit: 1})
				if err != nil || len(users) != 0 {
					return false
				}
			}
		} else if store != nil && strings.TrimSpace(snapshot.Subject) != "" {
			stored, err := store.GetUserBySubject(snapshot.Subject)
			if err != nil || stored == nil || !stored.Active {
				return false
			}
			// Subjects can be renamed and reused. An existing connection belongs
			// to the original durable identity, never its subject's replacement.
			if strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(stored.ID) != strings.TrimSpace(snapshot.ID) {
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
			localSessionExpiresAt = session.ExpiresAt
		}

		allowed := false
		if requireEditor {
			allowed = websocketUserCanEditProject(store, current, projectID)
		} else {
			allowed = websocketUserCanViewProject(store, current, projectID)
		}
		// Store and parent-scope lookups may have taken most of the deadline.
		// Do not authorize on an expiry observed before those reads completed.
		now = time.Now().UTC()
		if (snapshot.SessionScoped && !snapshot.SessionExpiresAt.IsZero() && !now.Before(snapshot.SessionExpiresAt)) ||
			(accountExpiresAt != nil && !now.Before(accountExpiresAt.UTC())) ||
			(!localSessionExpiresAt.IsZero() && !now.Before(localSessionExpiresAt.UTC())) {
			return false
		}
		return allowed && ctx.Err() == nil
	}
}

// Legacy stores can retain their established ordinary WebSocket behavior, but
// cannot pass the separate bounded checks for raw debugger events.
func (s *Server) websocketSupportsBoundedAuth() bool {
	if s.runner != nil && s.runner.settingsStore != nil {
		if _, ok := s.runner.settingsStore.(contextualSettingsPersistence); !ok {
			return false
		}
	}
	if s.store != nil {
		if _, ok := s.store.(browserResourceContextAuthStore); !ok {
			return false
		}
		if _, ok := s.store.(interface {
			HasUsersContext(context.Context) (bool, error)
		}); !ok {
			return false
		}
	}
	return true
}

func websocketDeliveryAuthContext(parent context.Context, bounded bool) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if bounded {
		return context.WithTimeout(parent, defaultCacheWriteTimeout)
	}
	return parent, func() {}
}

// websocketDurableAuthSettings bypasses the regular settings refresh throttle
// for raw debugger messages. A failed or uncancellable shared-store read must
// not authorize disclosure using a stale replica's settings cache.
func (s *Server) websocketDurableAuthSettings(parent context.Context) (AuthSettings, error) {
	ctx, cancel := context.WithTimeout(parent, defaultCacheWriteTimeout)
	defer cancel()
	return s.websocketDurableAuthSettingsContext(ctx)
}

func (s *Server) websocketDurableAuthSettingsContext(ctx context.Context) (AuthSettings, error) {
	if s.runner == nil {
		return normalizeAuthSettings(AuthSettings{}), nil
	}
	if s.runner.settingsStore == nil {
		// An in-memory runner has no durable read; avoid an unbounded lock wait
		// if a concurrent settings update is stalled.
		for !s.runner.mu.TryRLock() {
			timer := time.NewTimer(time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return AuthSettings{}, ctx.Err()
			case <-timer.C:
			}
		}
		settings := normalizeAuthSettings(s.runner.settings.Auth)
		s.runner.mu.RUnlock()
		return s.authSettingsForRuntime(settings), nil
	}
	store, ok := s.runner.settingsStore.(contextualSettingsPersistence)
	if !ok {
		return AuthSettings{}, errors.New("settings store cannot perform a bounded read")
	}
	raw, err := store.GetSettingContext(ctx, systemSettingsStoreKey)
	if err != nil || ctx.Err() != nil {
		if err == nil {
			err = ctx.Err()
		}
		return AuthSettings{}, err
	}
	persisted := defaultSystemSettings(s.runner.defaultMaxTests)
	if strings.TrimSpace(raw) != "" {
		var err error
		persisted, err = systemSettingsFromStoredValue(raw, persisted)
		if err != nil {
			return AuthSettings{}, err
		}
	}
	if err := validateSystemSettings(persisted); err != nil {
		return AuthSettings{}, err
	}
	if err := ctx.Err(); err != nil {
		return AuthSettings{}, err
	}
	return s.authSettingsForRuntime(persisted.Auth), nil
}

func (s *Server) websocketUserCanEditProject(user *AuthenticatedUser, projectID string) bool {
	return websocketUserCanEditProject(s.store, user, projectID)
}

func websocketUserCanEditProject(store storage.Store, user *AuthenticatedUser, projectID string) bool {
	if user == nil {
		return false
	}
	if user.hasGlobalRole(RoleEditor, RoleProjectAdmin, RoleSystemAdmin) {
		return true
	}
	if strings.TrimSpace(projectID) == "" {
		return false
	}
	return userHasProjectOrParentRole(store, user, projectID, RoleEditor, RoleProjectAdmin)
}

func (s *Server) websocketUserCanViewProject(user *AuthenticatedUser, projectID string) bool {
	return websocketUserCanViewProject(s.store, user, projectID)
}

func websocketUserCanViewProject(store storage.Store, user *AuthenticatedUser, projectID string) bool {
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

// Compile-time documentation of the store surface used by websocket
// revalidation. storage.Store already supplies these methods; keeping the
// assertion close to the feature makes accidental interface erosion obvious.
var _ interface {
	GetUserBySubject(string) (*storage.User, error)
	GetServiceAccount(string) (*storage.ServiceAccount, error)
} = (storage.Store)(nil)
