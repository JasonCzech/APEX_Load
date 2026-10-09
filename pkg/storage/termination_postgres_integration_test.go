//go:build postgres_integration

package storage

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestTerminationModePostgresMigration(t *testing.T) {
	databaseURL := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("jmeter_termination_%d", time.Now().UnixNano())
	if _, err := db.Exec("CREATE SCHEMA " + quotePostgresIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DROP SCHEMA " + quotePostgresIdentifier(schema) + " CASCADE")
	scoped := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	s, err := NewPostgresStore(scoped, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveScenario(&Scenario{ID: "legacy", Name: "Legacy", Duration: "1m"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"scenarios", "tests"} {
		if _, err := s.db.Exec("ALTER TABLE " + table + " DROP COLUMN termination_mode"); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = NewPostgresStore(scoped, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	legacy, _, err := s.GetScenario("legacy")
	if err != nil || legacy.TerminationMode != "duration" {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
	if err := s.SaveScenario(&Scenario{ID: "finite", Name: "Finite", TerminationMode: "iterations"}, nil); err != nil {
		t.Fatal(err)
	}
	finite, _, err := s.GetScenario("finite")
	if err != nil || finite.TerminationMode != "iterations" || finite.Duration != "" {
		t.Fatalf("finite=%+v err=%v", finite, err)
	}
}
