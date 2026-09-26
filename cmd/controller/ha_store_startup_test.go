package main

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestValidateHAStoreStartup(t *testing.T) {
	cases := []struct {
		name    string
		ha      bool
		backend string
		wantErr bool
	}{
		{name: "single replica sqlite", ha: false, backend: storage.StoreBackendSQLite},
		{name: "ha postgres", ha: true, backend: storage.StoreBackendPostgres},
		{name: "ha postgresql alias", ha: true, backend: " PostgreSQL "},
		{name: "ha sqlite", ha: true, backend: storage.StoreBackendSQLite, wantErr: true},
		{name: "ha unset backend defaults to sqlite", ha: true, backend: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHAStoreStartup(tc.ha, storage.StoreConfig{Backend: tc.backend, DatabaseURL: "postgres://db"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateHAStoreStartup() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "APEXLOAD_STORE=postgres") {
				t.Fatalf("error should name the fix, got %q", err)
			}
		})
	}
}

func TestIgnoredDatabaseURLWarning(t *testing.T) {
	if got := ignoredDatabaseURLWarning(storage.StoreConfig{Backend: storage.StoreBackendSQLite, DatabaseURL: "postgres://db"}); !strings.Contains(got, "ignored") {
		t.Fatalf("sqlite + URL should warn, got %q", got)
	}
	if got := ignoredDatabaseURLWarning(storage.StoreConfig{Backend: storage.StoreBackendPostgres, DatabaseURL: "postgres://db"}); got != "" {
		t.Fatalf("postgres + URL should not warn, got %q", got)
	}
	if got := ignoredDatabaseURLWarning(storage.StoreConfig{Backend: storage.StoreBackendSQLite}); got != "" {
		t.Fatalf("sqlite without URL should not warn, got %q", got)
	}
}
