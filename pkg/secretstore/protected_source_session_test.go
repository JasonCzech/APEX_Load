package secretstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

type microsecondSessionStore struct {
	storage.ProtectedSourceSecretSessionStore
}

func (s microsecondSessionStore) ClaimProtectedSourceSecretSession(id, projectID, actor, claimToken string, now, claimExpiresAt time.Time) (*storage.ProtectedSourceSecretSessionCipher, error) {
	record, err := s.ProtectedSourceSecretSessionStore.ClaimProtectedSourceSecretSession(id, projectID, actor, claimToken, now, claimExpiresAt)
	if record != nil {
		record.ExpiresAt = record.ExpiresAt.UTC().Truncate(time.Microsecond)
		record.CreatedAt = record.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	return record, err
}

type transientUnwrapWrapper struct {
	keywrap.KeyWrapper
	fail bool
}

func (w *transientUnwrapWrapper) UnwrapKey(ctx context.Context, wrapped keywrap.WrappedKey, aad []byte) ([]byte, error) {
	if w.fail {
		return nil, errors.New("transient key service outage")
	}
	return w.KeyWrapper.UnwrapKey(ctx, wrapped, aad)
}

func TestProtectedSourceSessionSharedAtomicConsumeSurvivesDatabaseTimePrecision(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const projectID = "protected-session-project"
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: projectID, Name: "Protected", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	durable := microsecondSessionStore{ProtectedSourceSecretSessionStore: records}
	creator := &ProtectedSourceSessionStore{Records: durable, Wrapper: wrapper}
	consumer := &ProtectedSourceSessionStore{Records: durable, Wrapper: wrapper}
	expiresAt := time.Unix(2_000_000_000, 123_456_789).UTC()
	if err := creator.Create(context.Background(), ProtectedSourceSession{
		ID: "shared-session", ProjectID: projectID, Actor: "admin",
		Values: map[string]string{"opaque-requirement": "source-value-never-on-wire"}, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := consumer.Consume(context.Background(), "shared-session", projectID, "other-admin"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-actor consume error = %v, want not found", err)
	}
	session, err := consumer.Consume(context.Background(), "shared-session", projectID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Values["opaque-requirement"]; got != "source-value-never-on-wire" {
		t.Fatalf("decrypted source value = %q", got)
	}
	if session.ExpiresAt.Nanosecond()%1_000 != 0 {
		t.Fatalf("expiry precision = %s, want database-stable microseconds", session.ExpiresAt)
	}
	clearStringMap(session.Values)
	if _, err := creator.Consume(context.Background(), "shared-session", projectID, "admin"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second consume error = %v, want single-use not found", err)
	}
}

func TestProtectedSourceSessionTransientDecryptFailureReleasesDurableClaim(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const projectID = "protected-session-retry-project"
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: projectID, Name: "Protected retry", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x6b}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	creator := &ProtectedSourceSessionStore{Records: records, Wrapper: wrapper}
	if err := creator.Create(context.Background(), ProtectedSourceSession{
		ID: "retry-session", ProjectID: projectID, Actor: "admin",
		Values: map[string]string{"requirement": "retry-secret"}, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	flaky := &transientUnwrapWrapper{KeyWrapper: wrapper, fail: true}
	consumer := &ProtectedSourceSessionStore{Records: records, Wrapper: flaky}
	if _, err := consumer.Consume(context.Background(), "retry-session", projectID, "admin"); err == nil || !strings.Contains(err.Error(), "unwrap") {
		t.Fatalf("transient consume error = %v", err)
	}
	flaky.fail = false
	session, err := consumer.Consume(context.Background(), "retry-session", projectID, "admin")
	if err != nil {
		t.Fatalf("retry consume: %v", err)
	}
	if got := session.Values["requirement"]; got != "retry-secret" {
		t.Fatalf("retry value = %q", got)
	}
	clearStringMap(session.Values)
	if _, err := consumer.Consume(context.Background(), "retry-session", projectID, "admin"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalized session replay error = %v, want not found", err)
	}
}

func TestProtectedSourceSessionDiscardIsScopedAtomicAndIdempotent(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	const projectID = "protected-session-discard-project"
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: projectID, Name: "Protected discard", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	store := &ProtectedSourceSessionStore{Records: records, Wrapper: wrapper}
	if err := store.Create(context.Background(), ProtectedSourceSession{
		ID: "discard-session", ProjectID: projectID, Actor: "owner",
		Values: map[string]string{"requirement": "discard-without-decrypting"}, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	if discarded, err := store.Discard(context.Background(), "discard-session", projectID, "other-actor"); err != nil || discarded {
		t.Fatalf("cross-actor discard = %t, %v; want scoped no-op", discarded, err)
	}
	if discarded, err := store.Discard(context.Background(), "discard-session", "other-project", "owner"); err != nil || discarded {
		t.Fatalf("cross-project discard = %t, %v; want scoped no-op", discarded, err)
	}
	if discarded, err := store.Discard(context.Background(), "discard-session", projectID, "owner"); err != nil || !discarded {
		t.Fatalf("owner discard = %t, %v; want deletion", discarded, err)
	}
	if discarded, err := store.Discard(context.Background(), "discard-session", projectID, "owner"); err != nil || discarded {
		t.Fatalf("second discard = %t, %v; want idempotent no-op", discarded, err)
	}
	if _, err := store.Consume(context.Background(), "discard-session", projectID, "owner"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("discarded session consume error = %v, want not found", err)
	}
}
