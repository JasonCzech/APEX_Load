package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQABrowserQualificationPreservesPinsAndStrictInput(t *testing.T) {
	var received json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received = body["qualification"]
		w.WriteHeader(201)
		io.WriteString(w, `{"id":"qualified"}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "cohort.json")
	raw := `{"version":1,"dependencies":[{"record_id":"be","record_sha256":"` + strings.Repeat("a", 64) + `"}],"metric":"lcp","metric":"inp"}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeQACommand(context.Background(), []string{"record-acceptance", "--api", server.URL, "--qualification", path}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if string(received) != raw {
		t.Fatal("qualification pins or duplicate-key evidence changed", string(received))
	}
	// The HTTP fixture captures raw input; the real API refuses the duplicate.
	for _, body := range []string{"", "null", "[]", "{} junk", `{"metric":"` + strings.Repeat("x", 16<<10) + `"}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		received = nil
		if err := executeQACommand(context.Background(), []string{"record-acceptance", "--api", server.URL, "--qualification", path}, io.Discard); err == nil || received != nil {
			t.Fatal("invalid local input reached server", err)
		}
	}
	if _, err := readBrowserAcceptanceQualification(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := readBrowserAcceptanceQualification(path + "-missing"); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := executeQACommand(context.Background(), []string{"acceptance", "--api", server.URL, "--qualification", path}, io.Discard); err == nil {
		t.Fatal("status ignored write-only qualification flag")
	}
}

func TestQAAcceptanceFiltersExactBrowserCohort(t *testing.T) {
	digest := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("cohort_sha256") != digest {
			t.Error("cohort filter changed", r.URL.String())
		}
		io.WriteString(w, `{"statuses":[{"supported":true}]}`)
	}))
	defer server.Close()
	if err := executeQACommand(context.Background(), []string{"acceptance", "--api", server.URL, "--cohort", digest, "--require-supported"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := executeQACommand(context.Background(), []string{"record-acceptance", "--cohort", digest}, io.Discard); err == nil {
		t.Fatal("write action accepted a query filter")
	}
}
