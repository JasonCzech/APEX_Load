package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type resourceDeadlineSQLIdentityStore struct {
	*storage.SQLiteStore
	pool     *sql.DB
	calls    int
	deadline time.Time
}

func (store *resourceDeadlineSQLIdentityStore) ReadBrowserResourceArchiveIdentityContext(ctx context.Context, id string) (*storage.BrowserResourceArchiveIdentity, error) {
	store.calls++
	store.deadline, _ = ctx.Deadline()
	var selected string
	// This real SQL pool's only connection remains occupied by an owned open
	// transaction. Acquisition must abandon the wait using the route context.
	if err := store.pool.QueryRowContext(ctx, "SELECT id FROM tests WHERE id = ?", id).Scan(&selected); err != nil {
		return nil, err
	}
	return store.SQLiteStore.ReadBrowserResourceArchiveIdentityContext(ctx, selected)
}
func TestBrowserResourceRouteDeadlineCoversPreHandlerOccupiedProjectSQL(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "occupied_pool_deadline", true: "already_canceled"}[alreadyCanceled], func(t *testing.T) {
			_, actual := resourceAuthTestServer(t, authModeTrustedHeaders)
			resourceAuthTestArchiveAuthority(t, actual, "deadline-run")
			now := time.Now().UTC()
			account := &storage.ServiceAccount{ID: "deadline-account", Name: "Deadline account", KeyHash: hashAPIKey("owned-deadline-key"), Active: true, ProjectRoles: map[string][]string{storage.DefaultProjectID: {RoleViewer}}, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now}
			if err := actual.SaveServiceAccount(account); err != nil {
				t.Fatal(err)
			}
			pool, err := sql.Open("sqlite", actual.BackendInfo().DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			pool.SetMaxOpenConns(1)
			transaction, err := pool.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.Rollback()
			store := &resourceDeadlineSQLIdentityStore{SQLiteStore: actual, pool: pool}
			server := &Server{store: store, mux: http.NewServeMux()}
			route := newBrowserResourceReadAPIRoute()
			entered := false
			route.Handler = func(*Server) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { entered = true })
			}
			passthrough := func(next http.Handler) http.Handler { return next }
			server.registerAPIRouteCatalog(apiRouteCatalog{Routes: []apiRouteSpec{route}}, apiRouteMiddlewareStack{cors: passthrough, log: passthrough, auth: authMiddleware(func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }, store, nil)})
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/deadline-run/archive/browser-resources", nil).WithContext(ctx)
			request.Header.Set("X-APEXLoad-API-Key", "owned-deadline-key")
			response := httptest.NewRecorder()
			began := time.Now()
			server.mux.ServeHTTP(response, request)
			if entered || store.calls != 1 || response.Code != http.StatusRequestTimeout || store.deadline.IsZero() || time.Since(began) > time.Second {
				t.Fatalf("pre-handler SQL deadline was not honored: entered=%v calls=%d status=%d", entered, store.calls, response.Code)
			}
			if err := transaction.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestBrowserResourceRouteAddsTenSecondDeadlineWithoutChangingOtherRoutes(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	route := newBrowserResourceReadAPIRoute()
	observed := map[string]bool{}
	route.Handler = func(*Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observed["resource_handler"] = true
			w.WriteHeader(http.StatusNoContent)
		})
	}
	other := newAuthenticatedAPIRoute(http.MethodGet, "/api/v1/resource-deadline-unrelated", "deadlineUnrelated", accessRequirement{}, func(*Server) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observed["other_handler"] = true
			w.WriteHeader(http.StatusNoContent)
		})
	})
	passthrough := func(next http.Handler) http.Handler { return next }
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deadline, ok := r.Context().Deadline()
			if r.URL.Path == "/api/v1/resource-deadline-unrelated" {
				if ok {
					t.Error("unrelated route acquired a new deadline")
				}
			} else {
				if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
					t.Error("resource authentication did not receive the early ten-second deadline")
				}
				observed["resource_auth"] = true
			}
			next.ServeHTTP(w, r)
		})
	}
	server.registerAPIRouteCatalog(apiRouteCatalog{Routes: []apiRouteSpec{route, other}}, apiRouteMiddlewareStack{cors: passthrough, log: passthrough, auth: auth})
	for _, path := range []string{"/api/v1/tests/run-1/archive/browser-resources", "/api/v1/resource-deadline-unrelated"} {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatal("registered route failed")
		}
	}
	if !observed["resource_auth"] || !observed["resource_handler"] || !observed["other_handler"] {
		t.Fatal("deadline route/ordinary route did not execute")
	}
}

func newBrowserResourceReadAPIRoute() apiRouteSpec {
	for _, route := range runArchiveAPIRouteCatalog.Routes {
		if route.Path == "/api/v1/tests/{id}/archive/browser-resources" {
			return route
		}
	}
	panic("resource route is not registered")
}
