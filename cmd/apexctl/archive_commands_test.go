package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/api"
)

func TestVerifyArchiveReturnsReport(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RunArchiveIntegrityReport{
			TestID:   "test-7",
			Status:   "verified",
			Verified: true,
			Summary:  "All 3 artifacts verified against their recorded SHA-256.",
			Total:    3,
			Matched:  3,
		})
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	report, err := client.VerifyArchive("test-7")
	if err != nil {
		t.Fatalf("VerifyArchive returned error: %v", err)
	}
	if gotPath != "/api/v1/tests/test-7/archive/verify" {
		t.Fatalf("requested path = %q, want archive verify path", gotPath)
	}
	if !report.Verified || report.Status != "verified" || report.Matched != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestVerifyArchiveTamperedReportNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.RunArchiveIntegrityReport{
			TestID:     "test-8",
			Status:     "tampered",
			Verified:   false,
			Summary:    "1 of 3 artifacts failed integrity verification.",
			Total:      3,
			Mismatched: 1,
			Matched:    2,
		})
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	report, err := client.VerifyArchive("test-8")
	if err != nil {
		t.Fatalf("VerifyArchive returned error for tampered archive: %v", err)
	}
	if report.Verified || report.Status != "tampered" || report.Mismatched != 1 {
		t.Fatalf("expected tampered verdict, got %+v", report)
	}
}

func TestVerifyArchiveMissingArchiveErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"archive not found"}`))
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	if _, err := client.VerifyArchive("missing"); err == nil {
		t.Fatal("expected error for missing archive")
	} else if !strings.Contains(err.Error(), "archive not found") {
		t.Fatalf("err = %v, want archive-not-found detail", err)
	}
}

func TestParseArchiveVerifyArgsPositionalAndFlags(t *testing.T) {
	// Exercise the real production parser, not a re-implementation.
	t.Setenv("APEXLOAD_API", "")
	t.Setenv("APEXLOAD_API_KEY", "")
	t.Setenv("APEXLOAD_BEARER_TOKEN", "")

	c := parseArchiveVerifyArgs([]string{
		"my-test-id",
		"--api", "http://ctrl:8080",
		"--api-key", "key-1",
		"--bearer-token", "tok-1",
		"--format", "json",
	})
	if c.test != "my-test-id" {
		t.Fatalf("positional test id = %q, want my-test-id", c.test)
	}
	if c.api != "http://ctrl:8080" {
		t.Fatalf("api = %q, want flag override", c.api)
	}
	if c.apiKey != "key-1" || c.bearerToken != "tok-1" {
		t.Fatalf("creds = (%q, %q), want key-1/tok-1", c.apiKey, c.bearerToken)
	}
	if c.format != "json" {
		t.Fatalf("format = %q, want json", c.format)
	}
}

func TestParseArchiveVerifyArgsTestFlagAlias(t *testing.T) {
	t.Setenv("APEXLOAD_API", "")
	c := parseArchiveVerifyArgs([]string{"--test", "id-via-flag"})
	if c.test != "id-via-flag" {
		t.Fatalf("--test id = %q, want id-via-flag", c.test)
	}
	// With no APEXLOAD_API and no --api, the controller URL defaults to localhost
	// for parity with the github/record subcommands.
	if c.api != "http://localhost:8080" {
		t.Fatalf("default api = %q, want http://localhost:8080", c.api)
	}
}

func TestParseArchiveVerifyArgsHonorsEnvDefaults(t *testing.T) {
	t.Setenv("APEXLOAD_API", "http://env-ctrl:9090")
	t.Setenv("APEXLOAD_API_KEY", "env-key")
	t.Setenv("APEXLOAD_BEARER_TOKEN", "env-tok")

	c := parseArchiveVerifyArgs([]string{"the-test"})
	if c.api != "http://env-ctrl:9090" {
		t.Fatalf("api = %q, want APEXLOAD_API default", c.api)
	}
	if c.apiKey != "env-key" || c.bearerToken != "env-tok" {
		t.Fatalf("creds = (%q, %q), want env defaults", c.apiKey, c.bearerToken)
	}
}
