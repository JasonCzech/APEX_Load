package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateDesktopAppDataPathMovesLegacyGeneration(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "local", "APEX Load")
	legacy := filepath.Join(root, "roaming", "APEX Load")
	if err := os.MkdirAll(filepath.Join(legacy, "recorder"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"apexload.db":                           "existing database",
		filepath.Join("recorder", "ca-key.pem"): "existing private key",
	} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := migrateDesktopAppDataPath(current, legacy, os.Rename)
	if err != nil {
		t.Fatal(err)
	}
	if got != current {
		t.Fatalf("migrated path = %q, want %q", got, current)
	}
	if _, err := os.Stat(filepath.Join(current, "apexload.db")); err != nil {
		t.Fatalf("migrated database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(current, "recorder", "ca-key.pem")); err != nil {
		t.Fatalf("migrated recorder CA key: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy directory survived successful move: %v", err)
	}
}

func TestMigrateDesktopAppDataPathFallsBackWithoutLosingLegacyState(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "local", "APEX Load")
	legacy := filepath.Join(root, "roaming", "APEX Load")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyDB := filepath.Join(legacy, "apexload.db")
	if err := os.WriteFile(legacyDB, []byte("existing database"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := migrateDesktopAppDataPath(current, legacy, func(string, string) error {
		return errors.New("cross-device link")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Fatalf("fallback path = %q, want intact legacy %q", got, legacy)
	}
	contents, err := os.ReadFile(legacyDB)
	if err != nil || string(contents) != "existing database" {
		t.Fatalf("legacy database was lost: %q, %v", contents, err)
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatalf("failed migration published an empty current directory: %v", err)
	}
}

func TestSelectInitialMenuAppDataPathReadsLegacyWithoutMigratingIt(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "local", "APEX Load")
	legacy := filepath.Join(root, "roaming", "APEX Load")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := selectInitialMenuAppDataPath(current, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got != legacy {
		t.Fatalf("initial menu path = %q, want existing legacy path %q", got, legacy)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("read-only menu lookup changed the legacy directory: %v", err)
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Fatalf("read-only menu lookup created the current directory: %v", err)
	}

	if err := os.MkdirAll(current, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err = selectInitialMenuAppDataPath(current, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got != current {
		t.Fatalf("initial menu path = %q, want current path %q", got, current)
	}
}
