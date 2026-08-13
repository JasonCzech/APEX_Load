package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func localAuthFixtureForTest(now time.Time) LocalAuthFixture {
	return localAuthFixtureNamedForTest(now, "viewer", "local-test-viewer", "local:viewer")
}

func localAuthFixtureNamedForTest(now time.Time, username, userID, subject string) LocalAuthFixture {
	hash, _ := bcrypt.GenerateFromPassword([]byte(username), bcrypt.MinCost)
	return LocalAuthFixture{
		Credential: LocalAuthCredential{Username: " " + username + " ", UserID: userID, PasswordHash: string(hash), CreatedAt: now, UpdatedAt: now},
		User:       User{ID: userID, Subject: subject, DisplayName: username, Active: true, CreatedAt: now, UpdatedAt: now},
		Bindings: []*UserRoleBinding{
			{ID: userID + "-global", UserID: userID, ScopeType: "global", Role: "viewer", CreatedAt: now, UpdatedAt: now},
			{ID: userID + "-project", UserID: userID, ScopeType: "project", ScopeID: DefaultProjectID, Role: "viewer", CreatedAt: now, UpdatedAt: now},
		},
	}
}

func localAuthTokenHashForTest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestLocalAuthFixturesAreHashedIdempotentAndPreserveEdits(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	fixture := localAuthFixtureForTest(now)
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	credential, err := store.GetLocalAuthCredential("VIEWER")
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if credential.PasswordHash == "viewer" || !strings.HasPrefix(credential.PasswordHash, "$2") {
		t.Fatalf("credential was not stored as bcrypt: %q", credential.PasswordHash)
	}
	bindings, err := store.ListUserRoleBindings(fixture.User.ID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("initial bindings = %v (err=%v), want 2", bindings, err)
	}

	if err := store.UpdateUserActive(fixture.User.ID, false, now.Add(time.Minute)); err != nil {
		t.Fatalf("disable fixture: %v", err)
	}
	if err := store.DeleteUserRoleBinding("local-test-viewer-project"); err != nil {
		t.Fatalf("edit bindings: %v", err)
	}
	changed := fixture
	replacementHash, err := bcrypt.GenerateFromPassword([]byte("replacement-must-not-win"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	changed.Credential.PasswordHash = string(replacementHash)
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{changed}); err != nil {
		t.Fatalf("repeat seed: %v", err)
	}
	user, _ := store.GetUser(fixture.User.ID)
	bindings, _ = store.ListUserRoleBindings(fixture.User.ID)
	credential, _ = store.GetLocalAuthCredential("viewer")
	if user.Active || len(bindings) != 1 || credential.PasswordHash == changed.Credential.PasswordHash {
		t.Fatalf("repeat seed overwrote Access edits or credential: active=%v bindings=%d hash=%q", user.Active, len(bindings), credential.PasswordHash)
	}
}

func TestLocalAuthFixtureBatchRollsBackAtomically(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	first := localAuthFixtureNamedForTest(now, "first", "local-test-first", "local:first")
	conflicting := localAuthFixtureNamedForTest(now, "second", "local-test-second", "local:second")
	if err := store.SaveUser(&User{ID: conflicting.User.ID, Subject: "reserved-by-someone-else", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("save reserved-id collision: %v", err)
	}

	err = store.EnsureLocalAuthFixtures([]LocalAuthFixture{first, conflicting})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("batch conflict error = %v", err)
	}
	if _, err := store.GetUser(first.User.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("first fixture survived failed batch: %v", err)
	}
	if _, err := store.GetLocalAuthCredential("first"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("first credential survived failed batch: %v", err)
	}
}

func TestLocalAuthFixtureSeedIsConcurrentAndIdempotent(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	fixture := localAuthFixtureForTest(time.Now().UTC().Truncate(time.Second))
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent seed: %v", err)
		}
	}
	bindings, err := store.ListUserRoleBindings(fixture.User.ID)
	if err != nil || len(bindings) != len(fixture.Bindings) {
		t.Fatalf("bindings after concurrent seed = %v (err=%v), want %d", bindings, err, len(fixture.Bindings))
	}
}

func TestLocalAuthFixtureSeedUsesPostgresTransactionLock(t *testing.T) {
	if got := localAuthFixtureSeedLockQuery(storeDialectSQLite); got != "" {
		t.Fatalf("SQLite lock query = %q, want none", got)
	}
	query := localAuthFixtureSeedLockQuery(storeDialectPostgres)
	if query != localAuthFixturePostgresAdvisoryLock || !strings.Contains(query, "pg_advisory_xact_lock") {
		t.Fatalf("Postgres lock query = %q", query)
	}
	if got := rewriteSQL(query, storeDialectPostgres); got != query {
		t.Fatalf("Postgres lock query rewrite = %q, want %q", got, query)
	}
}

func TestLocalAuthFixtureRejectsNonBcryptCredentialHashes(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	fixture := localAuthFixtureForTest(time.Now().UTC())
	fixture.Credential.PasswordHash = "viewer"
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err == nil || !strings.Contains(err.Error(), "must be bcrypt") {
		t.Fatalf("non-bcrypt fixture error = %v", err)
	}
	if _, err := store.GetUser(fixture.User.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid fixture persisted a user: %v", err)
	}
}

func TestLocalAuthFixtureRecreatedAfterDeletionAndRejectsReservedConflict(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	fixture := localAuthFixtureForTest(now)
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(fixture.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLocalAuthCredential("viewer"); err != sql.ErrNoRows {
		t.Fatalf("credential survived fixture deletion: %v", err)
	}
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatalf("recreate fixture: %v", err)
	}
	if err := store.DeleteUser(fixture.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUser(&User{ID: fixture.User.ID, Subject: "someone-else", Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("reserved conflict error = %v", err)
	}
}

func TestLocalAuthSessionLifecycle(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	fixture := localAuthFixtureForTest(now)
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatal(err)
	}
	expiredHash := localAuthTokenHashForTest("expired")
	activeHash := localAuthTokenHashForTest("active")
	for _, session := range []*LocalAuthSession{
		{TokenHash: expiredHash, UserID: fixture.User.ID, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute)},
		{TokenHash: activeHash, UserID: fixture.User.ID, SessionEpoch: 2, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := store.SaveLocalAuthSession(session); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := store.GetLocalAuthSessionByTokenHash(activeHash)
	if err != nil || loaded.SessionEpoch != 2 {
		t.Fatalf("active session = %#v, %v", loaded, err)
	}
	if err := store.DeleteExpiredLocalAuthSessions(now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLocalAuthSessionByTokenHash(expiredHash); err != sql.ErrNoRows {
		t.Fatalf("expired session lookup error = %v", err)
	}
	if err := store.DeleteLocalAuthSessionByTokenHash(activeHash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetLocalAuthSessionByTokenHash(activeHash); err != sql.ErrNoRows {
		t.Fatalf("deleted session lookup error = %v", err)
	}
}

func TestLocalAuthGenerationFencesSessionsAcrossControllerStarts(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth-generation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	fixture := localAuthFixtureForTest(now)
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatal(err)
	}
	firstGeneration := localAuthTokenHashForTest("first-controller-generation")
	secondGeneration := localAuthTokenHashForTest("second-controller-generation")
	if deleted, err := store.ActivateLocalAuthGeneration(firstGeneration, now); err != nil || deleted != 0 {
		t.Fatalf("activate first generation = (%d, %v), want (0, nil)", deleted, err)
	}
	if current, err := store.GetLocalAuthGeneration(); err != nil || current != firstGeneration {
		t.Fatalf("current generation = (%q, %v), want %q", current, err, firstGeneration)
	}

	firstSession := &LocalAuthSession{
		TokenHash: localAuthTokenHashForTest("first-generation-session"), UserID: fixture.User.ID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveLocalAuthSessionForGeneration(firstGeneration, firstSession); err != nil {
		t.Fatalf("save first-generation session: %v", err)
	}
	if _, err := store.GetLocalAuthSessionByTokenHashForGeneration(firstGeneration, firstSession.TokenHash); err != nil {
		t.Fatalf("lookup first-generation session: %v", err)
	}

	if deleted, err := store.ActivateLocalAuthGeneration(secondGeneration, now.Add(time.Minute)); err != nil || deleted != 1 {
		t.Fatalf("activate second generation = (%d, %v), want (1, nil)", deleted, err)
	}
	if _, err := store.GetLocalAuthSessionByTokenHashForGeneration(firstGeneration, firstSession.TokenHash); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old-generation lookup error = %v, want sql.ErrNoRows", err)
	}
	staleSession := &LocalAuthSession{
		TokenHash: localAuthTokenHashForTest("stale-controller-session"), UserID: fixture.User.ID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveLocalAuthSessionForGeneration(firstGeneration, staleSession); !errors.Is(err, ErrLocalAuthGenerationChanged) {
		t.Fatalf("stale-generation save error = %v, want ErrLocalAuthGenerationChanged", err)
	}
	currentSession := &LocalAuthSession{
		TokenHash: localAuthTokenHashForTest("current-controller-session"), UserID: fixture.User.ID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveLocalAuthSessionForGeneration(secondGeneration, currentSession); err != nil {
		t.Fatalf("save current-generation session: %v", err)
	}
	if _, err := store.GetLocalAuthSessionByTokenHashForGeneration(secondGeneration, currentSession.TokenHash); err != nil {
		t.Fatalf("lookup current-generation session: %v", err)
	}
}

func TestLocalAuthGenerationRejectsNonCanonicalValues(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth-generation-validation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	valid := localAuthTokenHashForTest("canonical-generation")
	for _, generation := range []string{"", "short", strings.Repeat("A", 64), strings.Repeat("z", 64), " " + valid, valid + " "} {
		if _, err := store.ActivateLocalAuthGeneration(generation, time.Now()); err == nil {
			t.Fatalf("ActivateLocalAuthGeneration(%q) succeeded", generation)
		}
	}
}

func TestLocalAuthGenerationActivationRollsBackOnSessionDeleteFailure(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth-generation-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	fixture := localAuthFixtureForTest(now)
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatal(err)
	}
	firstGeneration := localAuthTokenHashForTest("rollback-first-generation")
	secondGeneration := localAuthTokenHashForTest("rollback-second-generation")
	if _, err := store.ActivateLocalAuthGeneration(firstGeneration, now); err != nil {
		t.Fatal(err)
	}
	session := &LocalAuthSession{
		TokenHash: localAuthTokenHashForTest("rollback-session"), UserID: fixture.User.ID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SaveLocalAuthSessionForGeneration(firstGeneration, session); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_local_auth_session_delete
		BEFORE DELETE ON local_auth_sessions
		BEGIN SELECT RAISE(ABORT, 'injected local session delete failure'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.ActivateLocalAuthGeneration(secondGeneration, now.Add(time.Minute)); err == nil {
		t.Fatal("generation activation succeeded despite injected delete failure")
	}
	if generation, err := store.GetLocalAuthGeneration(); err != nil || generation != firstGeneration {
		t.Fatalf("generation after rollback = (%q, %v), want %q", generation, err, firstGeneration)
	}
	if _, err := store.GetLocalAuthSessionByTokenHashForGeneration(firstGeneration, session.TokenHash); err != nil {
		t.Fatalf("first-generation session was not rolled back: %v", err)
	}
}

func TestLocalAuthGenerationActivationHonorsCanceledContext(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth-generation-context.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	firstGeneration := localAuthTokenHashForTest("context-first-generation")
	secondGeneration := localAuthTokenHashForTest("context-second-generation")
	if _, err := store.ActivateLocalAuthGeneration(firstGeneration, now); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ActivateLocalAuthGenerationContext(ctx, secondGeneration, now.Add(time.Minute)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled generation activation error = %v, want context.Canceled", err)
	}
	if generation, err := store.GetLocalAuthGeneration(); err != nil || generation != firstGeneration {
		t.Fatalf("generation after canceled activation = (%q, %v), want %q", generation, err, firstGeneration)
	}
}

func TestLocalAuthSessionRejectsNonCanonicalTokenHashes(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "local-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	fixture := localAuthFixtureForTest(now)
	if err := store.EnsureLocalAuthFixtures([]LocalAuthFixture{fixture}); err != nil {
		t.Fatal(err)
	}
	valid := localAuthTokenHashForTest("valid")
	for _, tokenHash := range []string{"", "short", strings.Repeat("g", sha256.Size*2), strings.ToUpper(valid), " " + valid} {
		t.Run(tokenHash, func(t *testing.T) {
			session := &LocalAuthSession{TokenHash: tokenHash, UserID: fixture.User.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			if err := store.SaveLocalAuthSession(session); err == nil || !strings.Contains(err.Error(), "canonical SHA-256") {
				t.Fatalf("SaveLocalAuthSession(%q) error = %v", tokenHash, err)
			}
			if _, err := store.GetLocalAuthSessionByTokenHash(tokenHash); err == nil || !strings.Contains(err.Error(), "canonical SHA-256") {
				t.Fatalf("GetLocalAuthSessionByTokenHash(%q) error = %v", tokenHash, err)
			}
			if err := store.DeleteLocalAuthSessionByTokenHash(tokenHash); err == nil || !strings.Contains(err.Error(), "canonical SHA-256") {
				t.Fatalf("DeleteLocalAuthSessionByTokenHash(%q) error = %v", tokenHash, err)
			}
		})
	}
}
