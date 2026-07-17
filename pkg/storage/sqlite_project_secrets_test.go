package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectSecretCipherCreateReplaceAndProjectDeletion(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	project := &Project{ID: "secret-project", Name: "Secrets", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	record := &ProjectSecretCipher{
		ProjectID:      project.ID,
		SecretName:     "LOGIN_PASSWORD",
		Provider:       "encrypted_database",
		Ciphertext:     "ciphertext-one",
		WrappedKeyJSON: `{"kind":"test"}`,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      "editor",
	}
	created, err := store.CreateProjectSecretCipher(record)
	if err != nil || !created {
		t.Fatalf("CreateProjectSecretCipher = %t, %v", created, err)
	}
	duplicate := *record
	duplicate.Ciphertext = "must-not-overwrite"
	created, err = store.CreateProjectSecretCipher(&duplicate)
	if err != nil || created {
		t.Fatalf("duplicate CreateProjectSecretCipher = %t, %v", created, err)
	}
	stored, err := store.GetProjectSecretCipher(project.ID, record.SecretName)
	if err != nil || stored.Ciphertext != record.Ciphertext {
		t.Fatalf("stored after duplicate create = %#v, %v", stored, err)
	}
	removed, err := store.DeleteProjectSecretCipherIfCreatedBy(project.ID, record.SecretName, "different-attempt")
	if err != nil || removed {
		t.Fatalf("differently-owned rollback = %t, %v", removed, err)
	}
	if _, err := store.GetProjectSecretCipher(project.ID, record.SecretName); err != nil {
		t.Fatalf("differently-owned rollback removed the value: %v", err)
	}
	replacement := *record
	replacement.Ciphertext = "ciphertext-two"
	replacement.UpdatedAt = now.Add(time.Minute)
	replacement.CreatedBy = "admin"
	replaced, err := store.ReplaceProjectSecretCipher(&replacement)
	if err != nil || !replaced {
		t.Fatalf("ReplaceProjectSecretCipher = %t, %v", replaced, err)
	}
	missing := replacement
	missing.SecretName = "MISSING"
	replaced, err = store.ReplaceProjectSecretCipher(&missing)
	if err != nil || replaced {
		t.Fatalf("missing ReplaceProjectSecretCipher = %t, %v", replaced, err)
	}
	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectSecretCipher(project.ID, record.SecretName); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("secret remained after project deletion: %v", err)
	}
}

func TestProjectSecretCipherRollbackDeletesOnlyMatchingCreateAttempt(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	project := &Project{ID: "rollback-project", Name: "Rollback", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	record := &ProjectSecretCipher{
		ProjectID: project.ID, SecretName: "TOKEN", Provider: "encrypted_database",
		Ciphertext: "ciphertext", WrappedKeyJSON: `{"kind":"test"}`,
		CreatedAt: now, UpdatedAt: now, CreatedBy: "attempt-one",
	}
	if created, err := store.CreateProjectSecretCipher(record); err != nil || !created {
		t.Fatalf("CreateProjectSecretCipher = %t, %v", created, err)
	}
	if removed, err := store.DeleteProjectSecretCipherIfCreatedBy(project.ID, record.SecretName, "attempt-two"); err != nil || removed {
		t.Fatalf("wrong-attempt rollback = %t, %v", removed, err)
	}
	if removed, err := store.DeleteProjectSecretCipherIfCreatedBy(project.ID, record.SecretName, "attempt-one"); err != nil || !removed {
		t.Fatalf("owned rollback = %t, %v", removed, err)
	}
	if _, err := store.GetProjectSecretCipher(project.ID, record.SecretName); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owned rollback left a value: %v", err)
	}
}

func TestProjectSecretProviderMetadataDeletedWithProject(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	project := &Project{ID: "external-secret-project", Name: "External", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProjectSecretMetadata(&ProjectSecretMetadata{
		ProjectID: project.ID, SecretName: "TOKEN", Provider: "azure_key_vault",
		CreatedAt: now, UpdatedAt: now, CreatedBy: "admin",
	})
	if err != nil || !created {
		t.Fatalf("CreateProjectSecretMetadata = %t, %v", created, err)
	}
	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectSecretMetadata(project.ID, "TOKEN"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("external metadata remained after project deletion: %v", err)
	}
}
