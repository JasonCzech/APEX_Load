package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func resourceAuthTestServer(t *testing.T, mode string) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "resource-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Only the authorizer's immutable settings input is needed. This avoids
	// unrelated recovery workers/listeners in a focused real-storage test.
	server := &Server{store: store, runner: &TestRunner{settings: SystemSettings{Auth: AuthSettings{Mode: mode}}}}
	return server, store
}

// Seed the actual durable project resolver that the registered middleware
// invokes. This fixture does not bypass catalog admission or invent its project.
func resourceAuthTestArchiveAuthority(t *testing.T, store *storage.SQLiteStore, id string) {
	t.Helper()
	now := time.Now().UTC()
	root := t.TempDir()
	manifest := filepath.Join(root, runArchiveManifestFileName)
	if err := os.WriteFile(manifest, []byte(`{"test_id":"`+id+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: "Owned resource authority fixture", ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: now, MaterializationKey: "owned-auth-incarnation"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{TestID: id, ArchiveBackend: "filesystem", ArchivePath: root, ManifestPath: manifest, CreatedAt: now, CompletedAt: now}); err != nil {
		t.Fatal(err)
	}
}
func resourceAuthTestUser(t *testing.T, store *storage.SQLiteStore, scopeType, scope string) (*storage.User, *storage.UserRoleBinding) {
	t.Helper()
	now := time.Now().UTC()
	user := &storage.User{ID: "resource-user", Subject: "resource-subject", Active: true, SessionEpoch: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	binding := &storage.UserRoleBinding{ID: "resource-binding", UserID: user.ID, ScopeType: scopeType, ScopeID: scope, Role: RoleViewer, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUserRoleBinding(binding); err != nil {
		t.Fatal(err)
	}
	return user, binding
}
func resourceAuthTestRequest(t *testing.T, store *storage.SQLiteStore, user *storage.User, local bool) *http.Request {
	t.Helper()
	stored, err := store.GetUserBySubject(user.Subject)
	if err != nil {
		t.Fatal(err)
	}
	identity := authenticatedUserFromStoredUser(stored)
	identity.LocalScopeBoundViewer = local
	identity.SessionScoped = true
	identity.SessionEpoch = stored.SessionEpoch
	identity.SessionExpiresAt = time.Now().UTC().Add(time.Hour)
	return withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-1/archive/browser-resources", nil), authResolution{User: identity})
}
func resourceAuthTestPair(t *testing.T, server *Server, request *http.Request, project string) (func() bool, func() bool) {
	t.Helper()
	ordinary := server.websocketAuthorizer(request, project)
	contextual, err := server.browserResourceAuthorizer(request, project)
	if err != nil {
		t.Fatal(err)
	}
	if !ordinary() || !contextual() {
		t.Fatal("current authorized identity was refused")
	}
	return ordinary, contextual
}
func TestBrowserResourceFinalAuthUserParityAndRevocation(t *testing.T) {
	for _, kind := range []string{"project_role", "session_epoch", "inactive_user", "session_expiry"} {
		t.Run(kind, func(t *testing.T) {
			server, store := resourceAuthTestServer(t, authModeTrustedHeaders)
			user, binding := resourceAuthTestUser(t, store, "project", storage.DefaultProjectID)
			request := resourceAuthTestRequest(t, store, user, false)
			ordinary, contextual := resourceAuthTestPair(t, server, request, storage.DefaultProjectID)
			switch kind {
			case "project_role":
				if err := store.DeleteUserRoleBinding(binding.ID); err != nil {
					t.Fatal(err)
				}
			case "session_epoch":
				user.SessionEpoch++
				if err := store.SaveUser(user); err != nil {
					t.Fatal(err)
				}
			case "inactive_user":
				user.Active = false
				if err := store.SaveUser(user); err != nil {
					t.Fatal(err)
				}
			case "session_expiry":
				identity, _ := AuthenticatedUserFromContext(request.Context())
				expired := *identity
				expired.SessionExpiresAt = time.Now().UTC().Add(-time.Second)
				request = withAuthenticatedContext(request, authResolution{User: &expired})
				ordinary = server.websocketAuthorizer(request, storage.DefaultProjectID)
				var err error
				contextual, err = server.browserResourceAuthorizer(request, storage.DefaultProjectID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if ordinary() || contextual() {
				t.Fatal("revoked user/session retained archive authority")
			}
		})
	}
}
func TestBrowserResourceFinalAuthServiceAccountParityAndRevocation(t *testing.T) {
	for _, kind := range []string{"key_rotation", "read_scope", "project_role", "inactive", "expiry"} {
		t.Run(kind, func(t *testing.T) {
			server, store := resourceAuthTestServer(t, authModeTrustedHeaders)
			now := time.Now().UTC()
			account := &storage.ServiceAccount{ID: "resource-account", Name: "Resource account", KeyHash: hashAPIKey("initial-resource-transport-key"), Active: true, ProjectRoles: map[string][]string{storage.DefaultProjectID: {RoleViewer}}, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now}
			if err := store.SaveServiceAccount(account); err != nil {
				t.Fatal(err)
			}
			request := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-1/archive/browser-resources", nil), authResolution{User: authenticatedUserFromServiceAccount(account), ServiceAccount: &AuthenticatedServiceAccount{ID: account.ID, Name: account.Name, Scopes: account.Scopes}})
			request.Header.Set("X-APEXLoad-API-Key", "initial-resource-transport-key")
			ordinary, contextual := resourceAuthTestPair(t, server, request, storage.DefaultProjectID)
			switch kind {
			case "key_rotation":
				if err := store.RotateServiceAccountKey(account.ID, "rotated-private-digest", now.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			default:
				switch kind {
				case "read_scope":
					account.Scopes = []string{"scripts:write"}
				case "project_role":
					account.ProjectRoles = map[string][]string{}
				case "inactive":
					account.Active = false
				case "expiry":
					expired := now.Add(-time.Second)
					account.ExpiresAt = &expired
				}
				account.UpdatedAt = now.Add(time.Minute)
				if updated, err := store.UpdateServiceAccountMetadata(account, now); err != nil || !updated {
					t.Fatalf("account metadata update: %v", err)
				}
			}
			if ordinary() || contextual() {
				t.Fatal("revoked service account retained archive authority")
			}
		})
	}
}
func TestBrowserResourceFinalAuthLocalSessionAndGenerationParity(t *testing.T) {
	server, store := resourceAuthTestServer(t, authModeLocal)
	user, _ := resourceAuthTestUser(t, store, "project", storage.DefaultProjectID)
	now := time.Now().UTC()
	generation := strings.Repeat("a", 64)
	server.localAuthGeneration = generation
	if _, err := store.ActivateLocalAuthGenerationContext(context.Background(), generation, now); err != nil {
		t.Fatal(err)
	}
	makePair := func(token string) (func() bool, func() bool) {
		session := &storage.LocalAuthSession{TokenHash: localSessionTokenHashForGeneration(generation, token), UserID: user.ID, SessionEpoch: user.SessionEpoch, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := store.SaveLocalAuthSessionForGeneration(generation, session); err != nil {
			t.Fatal(err)
		}
		request := resourceAuthTestRequest(t, store, user, true)
		request.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: token})
		return resourceAuthTestPair(t, server, request, storage.DefaultProjectID)
	}
	first, firstContext := makePair("first-owned-token")
	second, secondContext := makePair("second-owned-token")
	if err := store.DeleteLocalAuthSessionByTokenHash(localSessionTokenHashForGeneration(generation, "first-owned-token")); err != nil {
		t.Fatal(err)
	}
	if first() || firstContext() {
		t.Fatal("deleted exact session retained authority")
	}
	if !second() || !secondContext() {
		t.Fatal("one session deletion revoked a separate session")
	}
	if _, err := store.ActivateLocalAuthGenerationContext(context.Background(), strings.Repeat("b", 64), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if second() || secondContext() {
		t.Fatal("superseded controller generation retained authority")
	}
}
func TestBrowserResourceFinalAuthParentScopeAndGlobalViewerParity(t *testing.T) {
	for _, scope := range []string{"domain", "application_repository"} {
		t.Run(scope, func(t *testing.T) {
			server, store := resourceAuthTestServer(t, authModeTrustedHeaders)
			id := storage.DefaultDomainID
			if scope == "application_repository" {
				id = storage.DefaultApplicationRepositoryID
			}
			user, binding := resourceAuthTestUser(t, store, scope, id)
			request := resourceAuthTestRequest(t, store, user, false)
			ordinary := server.websocketAuthorizer(request, storage.DefaultProjectID)
			contextual, err := server.browserResourceAuthorizer(request, storage.DefaultProjectID)
			if err != nil {
				t.Fatal(err)
			}
			if ordinary() || contextual() {
				t.Fatal("parent inheritance expanded an established provider's scopes")
			}
			identity, _ := AuthenticatedUserFromContext(request.Context())
			local := *identity
			local.LocalScopeBoundViewer = true
			request = withAuthenticatedContext(request, authResolution{User: &local})
			ordinary, contextual = resourceAuthTestPair(t, server, request, storage.DefaultProjectID)
			if err := store.DeleteUserRoleBinding(binding.ID); err != nil {
				t.Fatal(err)
			}
			if ordinary() || contextual() {
				t.Fatal("removed parent role retained authority")
			}
		})
	}
	server, store := resourceAuthTestServer(t, authModeTrustedHeaders)
	user, _ := resourceAuthTestUser(t, store, "global", "")
	request := resourceAuthTestRequest(t, store, user, false)
	resourceAuthTestPair(t, server, request, "unbound-project")
	identity, _ := AuthenticatedUserFromContext(request.Context())
	local := *identity
	local.LocalScopeBoundViewer = true
	request = withAuthenticatedContext(request, authResolution{User: &local})
	ordinary := server.websocketAuthorizer(request, "unbound-project")
	contextual, err := server.browserResourceAuthorizer(request, "unbound-project")
	if err != nil {
		t.Fatal(err)
	}
	if ordinary() || contextual() {
		t.Fatal("local baseline global viewer became unrestricted")
	}
}
func TestBrowserResourceFinalAuthCancellationAndUnavailableStoreAreExplicit(t *testing.T) {
	server, store := resourceAuthTestServer(t, authModeTrustedHeaders)
	user, _ := resourceAuthTestUser(t, store, "project", storage.DefaultProjectID)
	request := resourceAuthTestRequest(t, store, user, false)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	_, contextual := resourceAuthTestPair(t, server, request, storage.DefaultProjectID)
	cancel()
	if contextual() {
		t.Fatal("cancelled page retained authority")
	}
	if _, err := server.browserResourceAuthorizer(request, storage.DefaultProjectID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel cause lost: %v", err)
	}
	bypass, _ := resourceAuthTestServer(t, authModeDev)
	ctx, cancel = context.WithCancel(context.Background())
	request = httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-1/archive/browser-resources", nil).WithContext(ctx)
	check, err := bypass.browserResourceAuthorizer(request, storage.DefaultProjectID)
	if err != nil || !check() {
		t.Fatalf("bypass setup: %v", err)
	}
	cancel()
	if check() {
		t.Fatal("bypass ignored canceled page")
	}
	// A Store wrapper that withholds only the new optional Context capability
	// must be refused instead of executing legacy uncancellable reads.
	unavailable := &Server{store: struct{ storage.Store }{store}}
	if _, err := unavailable.browserResourceAuthorizer(httptest.NewRequest(http.MethodGet, "/", nil), storage.DefaultProjectID); !errors.Is(err, errBrowserResourceUnavailable) {
		t.Fatalf("unsupported storage became success: %v", err)
	}
}

// This uses the actual authentication middleware and changes the durable key
// only after it admitted the old presented credential. Taking a fresh current
// hash as the revalidator's snapshot would wrongly accept this request.
func TestBrowserResourceFinalAuthPinsPresentedKeyAcrossMiddlewareRotation(t *testing.T) {
	server, store := resourceAuthTestServer(t, authModeTrustedHeaders)
	resourceAuthTestArchiveAuthority(t, store, "run-1")
	now := time.Now().UTC()
	oldKey := "resource-reader-old-transport-key"
	account := &storage.ServiceAccount{ID: "rotation-boundary", Name: "Rotation boundary", KeyHash: hashAPIKey(oldKey), Active: true, ProjectRoles: map[string][]string{storage.DefaultProjectID: {RoleViewer}}, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}
	entered := false
	protected := authMiddleware(func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }, store, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered = true
		identity, ok := AuthenticatedServiceAccountFromContext(r.Context())
		if !ok || identity == nil || identity.ID != account.ID {
			t.Fatal("genuine middleware did not admit account")
		}
		if err := store.RotateServiceAccountKey(account.ID, hashAPIKey("resource-reader-new-transport-key"), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		recheck, err := server.browserResourceAuthorizer(r, storage.DefaultProjectID)
		if err != nil {
			t.Fatal(err)
		}
		if recheck() {
			t.Fatal("revoked transport key rebound to current account credential")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-1/archive/browser-resources", nil)
	request.SetPathValue("id", "run-1")
	request.Header.Set("X-APEXLoad-API-Key", oldKey)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if !entered || response.Code != http.StatusForbidden {
		t.Fatalf("middleware boundary was not exercised: entered=%v status=%d", entered, response.Code)
	}
	// A manually injected account without a presented credential cannot mint a
	// new transport-key snapshot either.
	missing := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-1/archive/browser-resources", nil), authResolution{User: authenticatedUserFromServiceAccount(account), ServiceAccount: &AuthenticatedServiceAccount{ID: account.ID, Scopes: []string{"results:read"}}})
	recheck, err := server.browserResourceAuthorizer(missing, storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if recheck() {
		t.Fatal("account context without presented credential was accepted")
	}
}

func TestBrowserResourceFinalAuthFixedScopeMatchesCatalogAndActualMiddleware(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-1/archive/browser-resources", nil)
	request.SetPathValue("id", "run-1")
	request.Header.Set("X-APEXLoad-API-Key", "fixed-owned-resource-key")
	requirement, ok := accessRequirementForRequest(request)
	if !ok || requirement.ServiceAccountScopeLookup != nil || requirement.ServiceAccountScope != staticServiceAccountScopeForRoute(request.Method, request.URL.Path) || requirement.ServiceAccountScope != "results:read" {
		t.Fatal("resource final scope differs from runtime route admission")
	}
	for _, tc := range []struct {
		name   string
		scopes []string
		mutate bool
		status int
	}{
		{"results_read", []string{"results:read"}, false, http.StatusOK},
		{"broad_read", []string{"read"}, false, http.StatusForbidden},
		{"tests_run", []string{"tests:run"}, false, http.StatusForbidden},
		{"revoked_after_middleware", []string{"results:read"}, true, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store := resourceAuthTestServer(t, authModeTrustedHeaders)
			resourceAuthTestArchiveAuthority(t, store, "run-1")
			now := time.Now().UTC()
			account := &storage.ServiceAccount{ID: "fixed-scope-account", Name: "Fixed scope account", KeyHash: hashAPIKey("fixed-owned-resource-key"), Active: true, ProjectRoles: map[string][]string{storage.DefaultProjectID: {RoleViewer}}, Scopes: tc.scopes, CreatedAt: now, UpdatedAt: now}
			if err := store.SaveServiceAccount(account); err != nil {
				t.Fatal(err)
			}
			entered := false
			middleware := authMiddleware(func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }, store, nil)
			handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered = true
				if tc.mutate {
					updated := *account
					updated.Scopes = []string{"tests:run"}
					updated.UpdatedAt = now.Add(time.Minute)
					if won, err := store.UpdateServiceAccountMetadata(&updated, now); err != nil || !won {
						t.Fatalf("durable post-middleware scope CAS failed: %v", err)
					}
				}
				authorize, err := server.browserResourceAuthorizer(r, storage.DefaultProjectID)
				if err != nil || !authorize() {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request.Clone(context.Background()))
			if response.Code != tc.status || (tc.name == "tests_run" || tc.name == "broad_read") && entered {
				t.Fatalf("middleware/final scope parity changed: status%d entered%v", response.Code, entered)
			}
		})
	}
	for _, outside := range []*http.Request{httptest.NewRequest(http.MethodPost, "/api/v1/tests/run-1/archive/browser-resources", nil), httptest.NewRequest(http.MethodGet, "/api/v1/tests/run-1/archive/transactions", nil)} {
		if browserResourceSnapshotRevalidator(outside, storage.DefaultProjectID, AuthSettings{Mode: authModeDev}, nil, "")() {
			t.Fatal("fixed resource revalidator admitted unrelated method or route")
		}
	}
}
