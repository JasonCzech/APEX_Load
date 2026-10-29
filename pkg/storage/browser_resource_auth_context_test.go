package storage

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHasUsersContext(t *testing.T) {
	store := resourceIdentityStore(t)
	if exists, err := store.HasUsersContext(context.Background()); err != nil || exists {
		t.Fatalf("empty store = %v, %v", exists, err)
	}
	now := time.Now().UTC()
	if err := store.SaveUser(&User{ID: "bootstrap-user", Subject: "bootstrap-user", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.HasUsersContext(context.Background()); err != nil || !exists {
		t.Fatalf("populated store = %v, %v", exists, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.HasUsersContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read returned %v", err)
	}
}

func TestBrowserResourceAuthContextMatchesEstablishedSQLProjection(t *testing.T) {
	checkBrowserResourceAuthSQLProjection(t, resourceIdentityStore(t))
}
func checkBrowserResourceAuthSQLProjection(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	user := &User{ID: "resource-user", Subject: "resource-subject", Active: true, SessionEpoch: 3, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	binding := &UserRoleBinding{ID: "resource-binding", UserID: user.ID, ScopeType: "project", ScopeID: DefaultProjectID, Role: "viewer", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUserRoleBinding(binding); err != nil {
		t.Fatal(err)
	}
	account := &ServiceAccount{ID: "resource-account", Name: "Resource account", KeyHash: "private-key-digest", Active: true, ProjectRoles: map[string][]string{DefaultProjectID: {"viewer"}}, DomainRoles: map[string][]string{DefaultDomainID: {"viewer"}}, Scopes: []string{"read"}, ExpiresAt: timePointerForResourceTest(now.Add(time.Hour)), CreatedAt: now, UpdatedAt: now}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}
	generation := strings.Repeat("a", 64)
	if _, err := store.ActivateLocalAuthGenerationContext(context.Background(), generation, now); err != nil {
		t.Fatal(err)
	}
	session := &LocalAuthSession{TokenHash: strings.Repeat("b", 64), UserID: user.ID, SessionEpoch: 3, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.SaveLocalAuthSessionForGeneration(generation, session); err != nil {
		t.Fatal(err)
	}
	project, err := store.GetProject(DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		ordinary   func() (any, error)
		contextual func(context.Context) (any, error)
	}{
		{"has_users", func() (any, error) {
			users, err := store.ListUsers(UserFilter{Limit: 1})
			return len(users) > 0, err
		}, func(ctx context.Context) (any, error) { return store.HasUsersContext(ctx) }},
		{"user_and_bindings", func() (any, error) { return store.GetUserBySubject(" " + user.Subject + " ") }, func(ctx context.Context) (any, error) {
			return store.GetUserBySubjectContext(ctx, " "+user.Subject+" ")
		}},
		{"service_account", func() (any, error) { return store.GetServiceAccount(account.ID) }, func(ctx context.Context) (any, error) { return store.GetServiceAccountContext(ctx, account.ID) }},
		{"project", func() (any, error) { return store.GetProject(project.ID) }, func(ctx context.Context) (any, error) { return store.GetProjectContext(ctx, project.ID) }},
		{"category", func() (any, error) { return store.GetRepositoryCategory(project.CategoryID) }, func(ctx context.Context) (any, error) {
			return store.GetRepositoryCategoryContext(ctx, project.CategoryID)
		}},
		{"repository", func() (any, error) { return store.GetApplicationRepository(project.DomainID) }, func(ctx context.Context) (any, error) {
			return store.GetApplicationRepositoryContext(ctx, project.DomainID)
		}},
		{"generation", func() (any, error) { return store.GetLocalAuthGeneration() }, func(ctx context.Context) (any, error) { return store.GetLocalAuthGenerationContext(ctx) }},
		{"exact_session", func() (any, error) {
			return store.GetLocalAuthSessionByTokenHashForGeneration(generation, session.TokenHash)
		}, func(ctx context.Context) (any, error) {
			return store.GetLocalAuthSessionByTokenHashForGenerationContext(ctx, generation, session.TokenHash)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ordinary, err := tc.ordinary()
			if err != nil {
				t.Fatal(err)
			}
			contextual, err := tc.contextual(context.Background())
			if err != nil || !reflect.DeepEqual(contextual, ordinary) {
				t.Fatalf("contextual projection changed: %v", err)
			}
			// The sole database connection is genuinely occupied. QueryRowContext and
			// QueryContext must abandon acquisition while the transaction stays open.
			tx, err := store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			began := time.Now()
			_, err = tc.contextual(ctx)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("occupied SQL ignored deadline: %v", err)
			}
			if time.Since(began) > time.Second {
				t.Fatal("cancellation waited for occupied transaction")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func timePointerForResourceTest(value time.Time) *time.Time { return &value }

func TestBrowserResourceAuthContextBindingOverflowCannotInventRole(t *testing.T) {
	checkBrowserResourceAuthBindingOverflow(t, resourceIdentityStore(t))
}
func checkBrowserResourceAuthBindingOverflow(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	user := &User{ID: "overflow-user", Subject: "overflow-subject", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i <= maxBrowserResourceAuthBindings; i++ {
		// Distinct scope keys permit a real stored population beyond the reader cap.
		if _, err := tx.Exec(`INSERT INTO user_role_bindings(id,user_id,scope_type,scope_id,role,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, fmtResourceBindingID(i), user.ID, "project", fmtResourceBindingID(i), "viewer", now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetUserBySubjectContext(context.Background(), user.Subject); err == nil {
		t.Fatal("oversized role population silently truncated")
	}
}
func fmtResourceBindingID(value int) string { return fmt.Sprintf("resource-scope-%d", value) }
