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

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDatabaseStoreEncryptsAndResolvesProjectSecret(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x31}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &DatabaseStore{Records: records, Wrapper: wrapper}
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_URL", "https://vault.example.test")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_ALLOWED_SECRETS", engine.ProjectSecretBindingName("project-a", "RESERVED"))
	if _, err := store.Create(context.Background(), "project-a", "reserved", "must-not-shadow", "tester"); !errors.Is(err, ErrReservedBinding) {
		t.Fatalf("allowlisted direct database create error = %v, want reserved binding", err)
	}
	if _, err := records.GetProjectSecretCipher("project-a", "RESERVED"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("allowlisted direct database create persisted a value: %v", err)
	}
	for _, sentinel := range []string{"[REDACTED]", " [redacted] ", "\t[ReDaCtEd]\n"} {
		if _, err := store.Create(context.Background(), "project-a", "SENTINEL", sentinel, "tester"); err == nil {
			t.Fatalf("Create accepted redaction sentinel %q", sentinel)
		}
	}
	metadata, err := store.Create(context.Background(), "project-a", "login.password", "secret-value", "tester")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Provider != databaseProvider || !metadata.Writable || metadata.Name != "LOGIN_PASSWORD" {
		t.Fatalf("metadata = %#v", metadata)
	}
	record, err := records.GetProjectSecretCipher("project-a", "LOGIN_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(record.Ciphertext, "secret-value") || strings.Contains(record.WrappedKeyJSON, "secret-value") {
		t.Fatal("encrypted storage contains plaintext")
	}
	value, resolved, err := store.Get(context.Background(), "project-a", "login-password")
	if err != nil {
		t.Fatal(err)
	}
	if value != "secret-value" || resolved.Name != "LOGIN_PASSWORD" {
		t.Fatalf("resolved = value:%q metadata:%#v", value, resolved)
	}
	if _, _, err := store.Get(context.Background(), "project-b", "LOGIN_PASSWORD"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project read error = %v", err)
	}
	if _, err := store.Create(context.Background(), "project-a", "LOGIN-PASSWORD", "must-not-overwrite", "other"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Create error = %v, want ErrAlreadyExists", err)
	}
	if removed, err := store.RollbackCreated(context.Background(), "project-a", "LOGIN_PASSWORD", "other"); err != nil || removed {
		t.Fatalf("differently-owned RollbackCreated = %t, %v", removed, err)
	}
	value, _, err = store.Get(context.Background(), "project-a", "LOGIN_PASSWORD")
	if err != nil || value != "secret-value" {
		t.Fatalf("duplicate create changed value = %q, %v", value, err)
	}
	replaced, err := store.Replace(context.Background(), "project-a", "LOGIN_PASSWORD", "rotated-value", "admin")
	if err != nil || replaced.CreatedBy != "admin" {
		t.Fatalf("Replace metadata = %#v, %v", replaced, err)
	}
	value, _, err = store.Get(context.Background(), "project-a", "LOGIN_PASSWORD")
	if err != nil || value != "rotated-value" {
		t.Fatalf("rotated value = %q, %v", value, err)
	}
	for _, sentinel := range []string{"[REDACTED]", " [redacted] ", "\t[ReDaCtEd]\n"} {
		if _, err := store.Replace(context.Background(), "project-a", "LOGIN_PASSWORD", sentinel, "admin"); err == nil {
			t.Fatalf("Replace accepted redaction sentinel %q", sentinel)
		}
		value, _, err = store.Get(context.Background(), "project-a", "LOGIN_PASSWORD")
		if err != nil || value != "rotated-value" {
			t.Fatalf("rejected sentinel changed stored value = %q, %v", value, err)
		}
	}
	if _, err := store.Replace(context.Background(), "project-a", "MISSING", "value", "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Replace error = %v, want ErrNotFound", err)
	}
	listed, err := store.List(context.Background(), "project-a")
	if err != nil || len(listed) != 1 || listed[0].Name != "LOGIN_PASSWORD" {
		t.Fatalf("listed metadata = %#v err=%v", listed, err)
	}
}

func TestEnvironmentStoreIsReadOnlyAndProjectScoped(t *testing.T) {
	t.Setenv(engine.ProjectSecretEnvironmentName("project-a", "LOGIN_PASSWORD"), "environment-value")
	store := EnvironmentStore{}
	if store.Writable() {
		t.Fatal("environment secret store is writable")
	}
	if _, err := store.Create(context.Background(), "project-a", "LOGIN_PASSWORD", "value", "tester"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := store.Replace(context.Background(), "project-a", "LOGIN_PASSWORD", "value", "tester"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Replace error = %v", err)
	}
	value, _, err := store.Get(context.Background(), "project-a", "LOGIN_PASSWORD")
	if err != nil || value != "environment-value" {
		t.Fatalf("project secret = %q, %v", value, err)
	}
	if _, _, err := store.Get(context.Background(), "project-b", "LOGIN_PASSWORD"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project environment read error = %v", err)
	}
}

// The dashboard keeps the metadata a create or rotation returns and compares
// it with the listed metadata. PostgreSQL keeps microseconds and Go's clock
// has nanoseconds on Linux, so the returned times must be the stored ones.
func TestDatabaseStoreReturnsTheStoredMetadataTimes(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x31}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	store := &DatabaseStore{Records: records, Wrapper: wrapper}
	ctx := context.Background()
	for _, write := range []struct {
		name string
		do   func() (Metadata, error)
	}{
		{"create", func() (Metadata, error) { return store.Create(ctx, "project-a", "API_TOKEN", "first", "tester") }},
		{"rotate", func() (Metadata, error) { return store.Replace(ctx, "project-a", "API_TOKEN", "second", "tester") }},
	} {
		returned, err := write.do()
		if err != nil {
			t.Fatalf("%s: %v", write.name, err)
		}
		listed, err := store.List(ctx, "project-a")
		if err != nil || len(listed) != 1 {
			t.Fatalf("list after %s = %+v, %v", write.name, listed, err)
		}
		for field, times := range map[string][2]time.Time{
			"created_at": {returned.CreatedAt, listed[0].CreatedAt},
			"updated_at": {returned.UpdatedAt, listed[0].UpdatedAt},
		} {
			if !times[0].Equal(times[1]) || !times[0].Equal(times[0].Truncate(time.Microsecond)) {
				t.Fatalf("%s returned %s %s, listed %s: want the stored value, at microseconds", write.name, field, times[0].Format(time.RFC3339Nano), times[1].Format(time.RFC3339Nano))
			}
		}
	}
}
