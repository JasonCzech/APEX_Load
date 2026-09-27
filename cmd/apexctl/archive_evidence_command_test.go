package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveEvidenceDispatch(t *testing.T) {
	if os.Getenv("APEXLOAD_TEST_ARCHIVE_EVIDENCE_DISPATCH") == "1" {
		os.Args = []string{"apexctl", "archive", "evidence", "--help"}
		archiveCmd()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestArchiveEvidenceDispatch$")
	cmd.Env = append(os.Environ(), "APEXLOAD_TEST_ARCHIVE_EVIDENCE_DISPATCH=1")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "--created-at") {
		t.Fatalf("dispatch: %v, %s", err, output)
	}
}

func evidenceArguments(expected archiveEvidenceExpectation, output string) []string {
	return []string{"--test", expected.TestID, "--project", expected.ProjectID, "--created-at", expected.CreatedAt.Format(time.RFC3339Nano), "--output", output}
}

func TestArchiveEvidenceCommandWritesPrivateProjection(t *testing.T) {
	expected, data := archiveEvidenceFixture()
	client, _ := evidenceServer(t, data, nil)
	t.Setenv("APEXLOAD_API", client.BaseURL)
	t.Setenv("APEXLOAD_BEARER_TOKEN", "credential")
	dir := filepath.Join(t.TempDir(), "evidence")
	var stdout, stderr bytes.Buffer
	code := runArchiveEvidenceCommand(evidenceArguments(expected, dir), &stdout, &stderr)
	if code != exitCodeSuccess {
		t.Fatalf("exit=%d, stderr=%s, report=%s", code, &stderr, &stdout)
	}
	path := filepath.Join(dir, "archive-evidence.json")
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, stdout.Bytes()) {
		t.Fatalf("saved report differs: %v", err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("mode %s: %v %v", path, info, err)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if runArchiveEvidenceCommand(evidenceArguments(expected, dir), &stdout, &stderr) != exitCodeUsageOrConfiguration {
		t.Fatal("reused output directory")
	}
	again, _ := os.ReadFile(path)
	if !bytes.Equal(saved, again) {
		t.Fatal("overwrote prior evidence")
	}
}

func TestArchiveEvidenceCommandRetainsFailureWithoutSecrets(t *testing.T) {
	expected, data := archiveEvidenceFixture()
	delete(data["run"].(map[string]any), "archive")
	data["run"].(map[string]any)["archive_failure"] = map[string]any{"reason": "remote_collection_unavailable", "detail": "SECRET"}
	client, _ := evidenceServer(t, data, nil)
	t.Setenv("APEXLOAD_API", client.BaseURL)
	t.Setenv("APEXLOAD_BEARER_TOKEN", "credential")
	dir := filepath.Join(t.TempDir(), "evidence")
	var stdout, stderr bytes.Buffer
	if runArchiveEvidenceCommand(evidenceArguments(expected, dir), &stdout, &stderr) != exitCodeTestFailure {
		t.Fatal("missing archive passed")
	}
	var report archiveEvidenceReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ArchiveFailure != "remote_collection_unavailable" || strings.Contains(stdout.String()+stderr.String(), "SECRET") {
		t.Fatalf("report: %s, %s", &stdout, &stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive-evidence.json")); err != nil {
		t.Fatal(err)
	}
}

func TestParseArchiveEvidenceArgs(t *testing.T) {
	expected, _ := archiveEvidenceFixture()
	args := evidenceArguments(expected, filepath.Join(t.TempDir(), "evidence"))
	t.Setenv("APEXLOAD_API", "https://controller.example")
	t.Setenv("APEXLOAD_API_KEY", "env-key")
	t.Setenv("APEXLOAD_BEARER_TOKEN", "env-token")
	parsed, err := parseArchiveEvidenceArgs(append(args, "--metadata-backend", "postgres", "--status", "FAILED", "--timeout", "10s"))
	if err != nil || parsed.expected.MetadataBackend != "postgres" || parsed.expected.Status != "FAILED" || parsed.timeout != 10*time.Second || parsed.apiKey != "env-key" || parsed.bearerToken != "env-token" {
		t.Fatalf("parse error %v", err)
	}
	for _, suffix := range [][]string{{"--timeout", "0s"}, {"--timeout", "31m"}, {"--metadata-backend", "other"}, {"--status", "RUNNING"}, {"--created-at", "bad"}, {"--output", "relative"}, {"--test", ""}, {"--project", ""}, {"--api", ""}, {"unexpected"}} {
		if _, err := parseArchiveEvidenceArgs(append(append([]string{}, args...), suffix...)); err == nil {
			t.Fatalf("accepted %v", suffix)
		}
	}
	var stdout, stderr bytes.Buffer
	if runArchiveEvidenceCommand([]string{"--help"}, &stdout, &stderr) != exitCodeSuccess || !strings.Contains(stdout.String(), "--created-at") {
		t.Fatal("missing help")
	}
}
