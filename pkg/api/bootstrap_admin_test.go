package api

import (
	"testing"
)

func bootstrapUpstream(subject string, roles ...string) *AuthenticatedUser {
	global := map[string]struct{}{}
	for _, r := range roles {
		global[r] = struct{}{}
	}
	return &AuthenticatedUser{
		Subject:         subject,
		GlobalRoles:     global,
		ProjectRoles:    map[string]map[string]struct{}{},
		DomainRoles:     map[string]map[string]struct{}{},
		RepositoryRoles: map[string]map[string]struct{}{},
	}
}

// A5 — in non-production the empty-store bootstrap preserves the historical behavior
// (honor the asserted identity/roles) so local first-run and existing tests work.
func TestBootstrapNonProductionHonorsAsserted(t *testing.T) {
	store := auditEventStore(t)
	got, err := applyLocalUserBindings(bootstrapUpstream("dev-user", RoleViewer), store)
	if err != nil {
		t.Fatalf("non-production bootstrap should succeed, got %v", err)
	}
	if _, ok := got.GlobalRoles[RoleViewer]; !ok {
		t.Fatalf("non-production bootstrap should honor asserted viewer role, got %#v", got.GlobalRoles)
	}
}

// A5 — in production the empty-store bootstrap is OFF by default.
func TestBootstrapProductionDisabledByDefault(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	store := auditEventStore(t)
	if _, err := applyLocalUserBindings(bootstrapUpstream("attacker", RoleSystemAdmin), store); err == nil {
		t.Fatalf("production bootstrap must be disabled by default")
	}
}

// A5 — when explicitly enabled in production, bootstrap grants a fixed system_admin
// (ignoring asserted roles), persists + audits it, and the window then closes.
func TestBootstrapProductionEnabledGrantsFixedAdmin(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_ALLOW_BOOTSTRAP_ADMIN", "true")
	store := auditEventStore(t)

	// Asserts only viewer, but bootstrap grants a fixed system_admin.
	got, err := applyLocalUserBindings(bootstrapUpstream("first-admin", RoleViewer), store)
	if err != nil {
		t.Fatalf("enabled production bootstrap should succeed, got %v", err)
	}
	if _, ok := got.GlobalRoles[RoleSystemAdmin]; !ok {
		t.Fatalf("bootstrap should grant fixed system_admin, got %#v", got.GlobalRoles)
	}
	if _, ok := got.GlobalRoles[RoleViewer]; ok {
		t.Fatalf("bootstrap must NOT honor the asserted viewer role")
	}

	// Persisted: a user + a global system_admin binding now exist.
	user, err := store.GetUserBySubject("first-admin")
	if err != nil || user == nil || !userHasActiveGlobalSystemAdmin(user) {
		t.Fatalf("bootstrap should persist a system-admin user, got %#v (err=%v)", user, err)
	}
	// Audited.
	if got := countAuditEvents(t, store, "auth.bootstrap.admin_granted"); got != 1 {
		t.Fatalf("bootstrap admin grant events = %d, want 1", got)
	}
	// Window closed: a different unknown subject is now rejected (store no longer empty).
	if _, err := applyLocalUserBindings(bootstrapUpstream("second-comer", RoleSystemAdmin), store); err == nil {
		t.Fatalf("after bootstrap the window must close (store non-empty)")
	}
}

// A5 — a configured pinned subject is the only one that may bootstrap.
func TestBootstrapProductionPinnedSubject(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_ALLOW_BOOTSTRAP_ADMIN", "1")
	t.Setenv("APEXLOAD_BOOTSTRAP_SUBJECT", "ops@example.com")

	store := auditEventStore(t)
	if _, err := applyLocalUserBindings(bootstrapUpstream("someone-else", RoleSystemAdmin), store); err == nil {
		t.Fatalf("non-pinned subject must not bootstrap")
	}
	if _, err := applyLocalUserBindings(bootstrapUpstream("Ops@Example.com", RoleSystemAdmin), store); err == nil {
		t.Fatalf("case-mismatched pinned subject must not bootstrap")
	}

	got, err := applyLocalUserBindings(bootstrapUpstream("ops@example.com", RoleViewer), store)
	if err != nil {
		t.Fatalf("pinned subject should bootstrap, got %v", err)
	}
	if _, ok := got.GlobalRoles[RoleSystemAdmin]; !ok {
		t.Fatalf("pinned-subject bootstrap should grant system_admin, got %#v", got.GlobalRoles)
	}
}
