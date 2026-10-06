package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQARecordAcceptanceHashesEvidenceLocally(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "browser-matrix.json")
	if err := os.WriteFile(artifact, []byte(`{"passed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/system/acceptance-records" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"recorded"}`))
	}))
	defer server.Close()
	var out bytes.Buffer
	err := executeQACommand(context.Background(), []string{"record-acceptance", "--api", server.URL, "--requirement", "BE-4", "--feature", "browser-har",
		"--platform", "linux-amd64", "--commit", "ABCDEF1234567", "--suite", "browser-acceptance", "--result", "passed", "--artifact", artifact}, &out)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(`{"passed":true}`))
	artifacts, _ := received["artifacts"].([]any)
	if len(artifacts) != 1 || received["commit_sha"] != "abcdef1234567" || received["requirement_id"] != "BE-4" {
		t.Fatalf("request: %#v", received)
	}
	first, _ := artifacts[0].(map[string]any)
	if first["name"] != "browser-matrix.json" || first["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("artifact hash: %#v", first)
	}
	if !strings.Contains(out.String(), "recorded") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestQACommandRejectsUnknownActions(t *testing.T) {
	if err := executeQACommand(context.Background(), []string{"delete"}, io.Discard); err == nil {
		t.Fatal("unknown action accepted")
	}
}
