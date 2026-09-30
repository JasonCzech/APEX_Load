package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestApplicationSecretStorageIsolationAndDeletion(t *testing.T) {
	db, err := NewSQLiteStore(filepath.Join(t.TempDir(), "applications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if err := db.SaveProject(&Project{ID: "project", Name: "Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"app-a", "app-b"} {
		if err := db.SaveApplication(&Application{ID: id, ProjectID: "project", Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	store := ApplicationSecretStorage{SQLiteStore: db}
	if _, err := store.CreateProjectSecretCipher(&ProjectSecretCipher{ProjectID: "project", SecretName: "TOKEN", Provider: "encrypted_database", Ciphertext: "encrypted", WrappedKeyJSON: "{}"}); err == nil {
		t.Fatal("non-application scope accepted")
	}
	scope := ApplicationSecretScopeID("app-a")
	created, err := store.CreateProjectSecretCipher(&ProjectSecretCipher{ProjectID: scope, SecretName: "TOKEN", Provider: "encrypted_database", Ciphertext: "encrypted", WrappedKeyJSON: "{}", CreatedBy: "operator"})
	if err != nil || !created {
		t.Fatalf("create encrypted record: %v %t", err, created)
	}
	if _, err := store.GetProjectSecretCipher(ApplicationSecretScopeID("app-b"), "TOKEN"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-application read: %v", err)
	}
	metadata := &ProjectSecretMetadata{ProjectID: scope, SecretName: "EXTERNAL", Provider: "azure_key_vault"}
	created, err = store.CreateProjectSecretMetadata(metadata)
	if err != nil || !created {
		t.Fatalf("create external metadata: %v %t", err, created)
	}
	listed, err := db.ListExternalApplicationSecretMetadata("app-a")
	if err != nil || len(listed) != 1 || listed[0].SecretName != "EXTERNAL" || listed[0].Provider != "azure_key_vault" {
		t.Fatalf("external cleanup metadata: %+v, %v", listed, err)
	}
	if other, err := db.ListExternalApplicationSecretMetadata("app-b"); err != nil || len(other) != 0 {
		t.Fatalf("cross-application cleanup metadata: %+v, %v", other, err)
	}
	if err := db.DeleteApplication("app-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectSecretCipher(scope, "TOKEN"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("encrypted record survived deletion: %v", err)
	}
	if _, err := store.GetProjectSecretMetadata(scope, "EXTERNAL"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("external metadata survived deletion: %v", err)
	}
}
