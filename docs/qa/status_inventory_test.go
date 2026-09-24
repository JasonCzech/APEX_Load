// Package qa_test guards the test-program tracker against drift.
//
// The tracker under docs/qa is only useful if it cannot quietly disagree with
// reality, so it is enforced the same way this repository already enforces its
// other documentation-as-data contracts (see scripts/ and deployments/k8s/,
// which are likewise test-only packages asserting on non-Go files, and
// pkg/convert/enterprisearchive/optin_suite_inventory_test.go, which fails when
// a gated suite is missing from docs/qa/TEST_PROGRAM.md).
package qa_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var validStates = map[string]bool{
	"NOT_STARTED": true,
	"IN_PROGRESS": true,
	"BLOCKED":     true,
	"DONE":        true,
}

var validDefectStates = map[string]bool{
	"OPEN":        true,
	"IN_PROGRESS": true,
	"BLOCKED":     true,
	"FIXED":       true,
	"DEFERRED":    true,
}

var (
	milestoneHeading = regexp.MustCompile(`^###\s+(M\d+)\b`)
	milestoneRow     = regexp.MustCompile(`^\|\s*(M\d+)\s*\|`)
	defectRow        = regexp.MustCompile(`^\|\s*((?:DEF|QA)-\d+)\s*\|`)
	commitReference  = regexp.MustCompile(`^(?:verified@)?([0-9a-fA-F]{7,40})$`)
	deferredMetadata = regexp.MustCompile(`^owner=([^;]+);\s*review=(\d{4}-\d{2}-\d{2});\s*target=(M\d+);\s*rationale=(.+)$`)
)

type evidenceContract struct {
	SchemaVersion  int    `json:"schema_version"`
	ManifestSchema string `json:"manifest_schema"`
	Milestones     []struct {
		ID       string   `json:"id"`
		Required []string `json:"required"`
	} `json:"milestones"`
}

type evidenceManifest struct {
	SchemaVersion    int            `json:"schema_version"`
	Milestone        string         `json:"milestone"`
	RepositoryCommit string         `json:"repository_commit"`
	Environment      map[string]any `json:"environment"`
	Commands         []string       `json:"commands"`
	StartedAt        string         `json:"started_at"`
	FinishedAt       string         `json:"finished_at"`
	Result           string         `json:"result"`
	Limitations      []string       `json:"limitations"`
	Artifacts        []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"artifacts"`
}

// splitRow returns the cells of a Markdown table row, trimmed.
func splitRow(line string) []string {
	trimmed := strings.Trim(strings.TrimSpace(line), "|")
	cells := strings.Split(trimmed, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

func readLines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.Split(string(data), "\n")
}

func readEvidenceContract(t *testing.T) evidenceContract {
	t.Helper()
	data, err := os.ReadFile("evidence-contract.json")
	if err != nil {
		t.Fatalf("read evidence-contract.json: %v", err)
	}
	var contract evidenceContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("parse evidence-contract.json: %v", err)
	}
	if contract.SchemaVersion != 1 {
		t.Fatalf("evidence contract schema_version = %d, want 1", contract.SchemaVersion)
	}
	if contract.ManifestSchema != "evidence-manifest.schema.json" {
		t.Fatalf("evidence contract manifest_schema = %q, want evidence-manifest.schema.json", contract.ManifestSchema)
	}
	return contract
}

// programMilestones reads the scope of record rather than duplicating it in
// Go. Adding M9 to TEST_PROGRAM.md must immediately require M9 everywhere else.
func programMilestones(t *testing.T) []string {
	t.Helper()
	var milestones []string
	seen := make(map[string]bool)
	for _, line := range readLines(t, "TEST_PROGRAM.md") {
		match := milestoneHeading.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if seen[match[1]] {
			t.Fatalf("TEST_PROGRAM.md declares milestone %s more than once", match[1])
		}
		seen[match[1]] = true
		milestones = append(milestones, match[1])
	}
	if len(milestones) == 0 {
		t.Fatal("TEST_PROGRAM.md declares no milestones")
	}
	return milestones
}

func git(t *testing.T, args ...string) error {
	t.Helper()
	_, err := gitOutput(t, args...)
	return err
}

func gitOutput(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", "../.."}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, &gitError{err: err, output: strings.TrimSpace(string(output))}
	}
	return output, nil
}

type gitError struct {
	err    error
	output string
}

func (e *gitError) Error() string {
	if e.output == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.output
}

func isCommitted(t *testing.T, path string) bool {
	t.Helper()
	repositoryPath := filepath.ToSlash(filepath.Join("docs", "qa", path))
	output, err := gitOutput(t, "ls-tree", "HEAD", "--", repositoryPath)
	if err != nil {
		return false
	}
	fields := strings.Fields(string(output))
	return len(fields) >= 3 && (fields[0] == "100644" || fields[0] == "100755") && fields[1] == "blob"
}

func committedEvidence(t *testing.T, relativePath string) ([]byte, error) {
	t.Helper()
	if !isCommitted(t, relativePath) {
		return nil, fmt.Errorf("not a committed regular file in HEAD")
	}
	repositoryPath := filepath.ToSlash(filepath.Join("docs", "qa", relativePath))
	return gitOutput(t, "show", "HEAD:"+repositoryPath)
}

func cleanEvidencePath(value string) (string, bool) {
	if value == "" || path.IsAbs(value) || !regexp.MustCompile(`^[A-Za-z0-9._/-]+$`).MatchString(value) {
		return "", false
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func citedCommit(commit string) (string, bool) {
	match := commitReference.FindStringSubmatch(strings.Trim(strings.TrimSpace(commit), "`"))
	if match == nil {
		return "", false
	}
	return match[1], true
}

func validateDeferredMetadata(value string, program map[string]bool) error {
	metadata := strings.Trim(strings.TrimSpace(value), "`")
	match := deferredMetadata.FindStringSubmatch(metadata)
	if match == nil || strings.TrimSpace(match[1]) == "" || strings.TrimSpace(match[4]) == "" {
		return fmt.Errorf("missing owner, review date, target milestone, or rationale")
	}
	if _, err := time.Parse("2006-01-02", match[2]); err != nil {
		return fmt.Errorf("invalid review date %q", match[2])
	}
	if !program[match[3]] {
		return fmt.Errorf("target milestone %s is undefined", match[3])
	}
	return nil
}

func validateEvidenceManifest(t *testing.T, milestone string, required []string) {
	t.Helper()
	manifestPath := filepath.Join("evidence", milestone, "manifest.json")
	data, err := committedEvidence(t, manifestPath)
	if err != nil {
		t.Errorf("read committed %s: %v", manifestPath, err)
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Errorf("parse %s: %v", manifestPath, err)
		return
	}
	requiredFields := []string{"schema_version", "milestone", "repository_commit", "environment", "commands", "started_at", "finished_at", "result", "limitations", "artifacts"}
	allowedFields := make(map[string]bool, len(requiredFields))
	for _, field := range requiredFields {
		allowedFields[field] = true
		if _, ok := fields[field]; !ok {
			t.Errorf("%s is missing required field %q", manifestPath, field)
		}
	}
	for field := range fields {
		if !allowedFields[field] {
			t.Errorf("%s contains unsupported field %q", manifestPath, field)
		}
	}
	var manifest evidenceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Errorf("parse %s: %v", manifestPath, err)
		return
	}
	if manifest.SchemaVersion != 1 || manifest.Milestone != milestone || manifest.Result != "pass" {
		t.Errorf("%s must carry schema_version=1, milestone=%s, and result=pass", manifestPath, milestone)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(manifest.RepositoryCommit) {
		t.Errorf("%s repository_commit must be a full 40-digit lowercase Git hash", manifestPath)
	} else if err := git(t, "merge-base", "--is-ancestor", manifest.RepositoryCommit, "HEAD"); err != nil {
		t.Errorf("%s tested commit is not an ancestor of HEAD: %v", manifestPath, err)
	}
	if len(manifest.Environment) == 0 || len(manifest.Commands) == 0 {
		t.Errorf("%s must describe a non-empty environment and command list", manifestPath)
	}
	for _, command := range manifest.Commands {
		if strings.TrimSpace(command) == "" {
			t.Errorf("%s commands must not contain empty entries", manifestPath)
		}
	}
	for _, limitation := range manifest.Limitations {
		if strings.TrimSpace(limitation) == "" {
			t.Errorf("%s limitations must not contain empty entries", manifestPath)
		}
	}
	if manifest.Limitations == nil {
		t.Errorf("%s limitations must be a JSON array (use [] when there are none)", manifestPath)
	}
	if len(manifest.Artifacts) == 0 {
		t.Errorf("%s must hash at least one milestone report", manifestPath)
	}
	started, startErr := time.Parse(time.RFC3339, manifest.StartedAt)
	finished, finishErr := time.Parse(time.RFC3339, manifest.FinishedAt)
	if startErr != nil || finishErr != nil || finished.Before(started) {
		t.Errorf("%s started_at/finished_at must be ordered RFC3339 timestamps", manifestPath)
	}

	artifacts := make(map[string]string)
	for _, artifact := range manifest.Artifacts {
		cleaned, safe := cleanEvidencePath(artifact.Path)
		if !safe || cleaned == "manifest.json" || cleaned == "SUMMARY.md" {
			t.Errorf("%s contains unsafe or self-referential artifact path %q", manifestPath, artifact.Path)
			continue
		}
		if _, duplicate := artifacts[cleaned]; duplicate {
			t.Errorf("%s repeats artifact path %q", manifestPath, cleaned)
			continue
		}
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(artifact.SHA256) {
			t.Errorf("%s artifact %q has invalid SHA-256 %q", manifestPath, artifact.Path, artifact.SHA256)
			continue
		}
		artifacts[cleaned] = artifact.SHA256
		artifactPath := path.Join("evidence", milestone, cleaned)
		artifactData, err := committedEvidence(t, artifactPath)
		if err != nil {
			t.Errorf("%s hashes artifact %q that is not committed in HEAD: %v", manifestPath, cleaned, err)
			continue
		}
		got := fmt.Sprintf("%x", sha256.Sum256(artifactData))
		if got != artifact.SHA256 {
			t.Errorf("%s hash for %q = %s, want %s", manifestPath, cleaned, artifact.SHA256, got)
		}
	}
	for _, artifact := range required {
		if artifact == "manifest.json" || artifact == "SUMMARY.md" {
			continue
		}
		_, ok := artifacts[artifact]
		if !ok {
			t.Errorf("%s does not hash required artifact %q", manifestPath, artifact)
			continue
		}
	}
	summaryPath := filepath.Join("evidence", milestone, "SUMMARY.md")
	summary, err := committedEvidence(t, summaryPath)
	if err != nil {
		t.Errorf("read committed %s: %v", summaryPath, err)
		return
	}
	command := exec.Command("python3", "../../scripts/qa-generate-evidence-summary.py", "--manifest", "-")
	command.Stdin = bytes.NewReader(data)
	generated, err := command.CombinedOutput()
	if err != nil {
		t.Errorf("generate %s: %v\n%s", summaryPath, err, generated)
		return
	}
	if !bytes.Equal(summary, generated) {
		t.Errorf("%s is stale; regenerate it from the committed manifest", summaryPath)
	}
}

// TestStatusListsEveryMilestone fails when a milestone is dropped from the
// tracker, or carries a state outside the documented vocabulary.
func TestStatusListsEveryMilestone(t *testing.T) {
	seen := make(map[string]string)
	for _, line := range readLines(t, "STATUS.md") {
		match := milestoneRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		cells := splitRow(line)
		if len(cells) < 5 {
			t.Errorf("STATUS.md milestone row for %s has %d cells, want at least 5: %q",
				match[1], len(cells), line)
			continue
		}
		if _, dup := seen[match[1]]; dup {
			t.Errorf("STATUS.md lists milestone %s more than once", match[1])
			continue
		}
		seen[match[1]] = cells[2]
	}

	expected := make(map[string]bool)
	for _, milestone := range programMilestones(t) {
		expected[milestone] = true
		state, ok := seen[milestone]
		if !ok {
			t.Errorf("STATUS.md is missing milestone %s; every milestone in TEST_PROGRAM.md must appear", milestone)
			continue
		}
		if !validStates[state] {
			t.Errorf("milestone %s has state %q, want one of NOT_STARTED/IN_PROGRESS/BLOCKED/DONE", milestone, state)
		}
	}
	for milestone := range seen {
		if !expected[milestone] {
			t.Errorf("STATUS.md lists milestone %s, but TEST_PROGRAM.md does not define it", milestone)
		}
	}
}

func TestEvidenceContractMatchesProgram(t *testing.T) {
	program := programMilestones(t)
	contract := readEvidenceContract(t)
	if len(contract.Milestones) != len(program) {
		t.Fatalf("evidence contract has %d milestones, TEST_PROGRAM.md has %d", len(contract.Milestones), len(program))
	}
	for index, milestone := range program {
		entry := contract.Milestones[index]
		if entry.ID != milestone {
			t.Errorf("evidence contract milestone %d = %q, want %q", index, entry.ID, milestone)
		}
		seen := make(map[string]bool)
		for _, required := range entry.Required {
			if _, safe := cleanEvidencePath(required); !safe {
				t.Errorf("%s evidence path %q must stay inside its milestone directory", milestone, required)
			}
			if seen[required] {
				t.Errorf("%s evidence contract repeats %q", milestone, required)
			}
			seen[required] = true
		}
		for _, universal := range []string{"manifest.json", "SUMMARY.md"} {
			if !seen[universal] {
				t.Errorf("%s evidence contract must require %s", milestone, universal)
			}
		}
	}
}

// TestDoneMilestonesHaveEvidence enforces the program's central rule: a
// milestone is closed by an artifact, never by an assertion.

func TestDoneMilestonesHaveEvidence(t *testing.T) {
	required := make(map[string][]string)
	for _, milestone := range readEvidenceContract(t).Milestones {
		required[milestone.ID] = milestone.Required
	}
	for _, line := range readLines(t, "STATUS.md") {
		match := milestoneRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		cells := splitRow(line)
		if len(cells) < 3 || cells[2] != "DONE" {
			continue
		}
		for _, artifact := range required[match[1]] {
			path := filepath.Join("evidence", match[1], artifact)
			if !isCommitted(t, path) {
				t.Errorf("milestone %s is DONE but required evidence %s is not committed in HEAD", match[1], path)
			}
		}
		validateEvidenceManifest(t, match[1], required[match[1]])
	}
}

// TestEveryMilestoneHasAnEvidenceDir keeps the evidence tree in step with the
// milestone list, so closing a milestone never requires inventing a directory.
func TestEveryMilestoneHasAnEvidenceDir(t *testing.T) {
	for _, milestone := range programMilestones(t) {
		placeholder := filepath.Join("evidence", milestone, ".gitkeep")
		if !isCommitted(t, placeholder) {
			t.Errorf("docs/qa/%s is not committed; every milestone needs a committed evidence directory placeholder", placeholder)
		}
	}
}

func TestCleanEvidencePath(t *testing.T) {
	tests := []struct {
		value string
		ok    bool
	}{
		{value: "report.json", ok: true},
		{value: "soak/summary.json", ok: true},
		{value: "../report.json"},
		{value: "soak/../report.json"},
		{value: "./report.json"},
		{value: "/tmp/report.json"},
		{value: `soak\report.json`},
		{value: "report|forged.md"},
		{value: ""},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			_, ok := cleanEvidencePath(test.value)
			if ok != test.ok {
				t.Fatalf("cleanEvidencePath(%q) safe = %v, want %v", test.value, ok, test.ok)
			}
		})
	}
}

func TestCommittedEvidenceRejectsWorktreeOnlyFile(t *testing.T) {
	file, err := os.CreateTemp(filepath.Join("evidence", "M0"), "worktree-only-*.json")
	if err != nil {
		t.Fatalf("create worktree-only evidence fixture: %v", err)
	}
	name := file.Name()
	if _, err := file.WriteString("{}\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		t.Fatalf("write worktree-only evidence fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		t.Fatalf("close worktree-only evidence fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(name) })
	relative, err := filepath.Rel(".", name)
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	if _, err := committedEvidence(t, relative); err == nil {
		t.Fatalf("worktree-only evidence %s was accepted as committed HEAD evidence", relative)
	}
}

// TestFixedDefectsCiteACommit stops a defect from being closed on a claim. The
// commit column must name something; an em dash placeholder is not a reference.
func TestFixedDefectsCiteACommit(t *testing.T) {
	program := make(map[string]bool)
	for _, milestone := range programMilestones(t) {
		program[milestone] = true
	}
	for _, line := range readLines(t, "DEFECTS.md") {
		match := defectRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		cells := splitRow(line)
		// State and commit are the last two columns in both defect tables, which
		// have different widths. Guard the length first: without this, a
		// truncated row such as "| DEF-999 |" indexes cells[-1] and panics,
		// taking down every other test in the package instead of reporting the
		// malformed row.
		if len(cells) < 3 {
			t.Errorf("defect %s row is malformed (%d cells, want at least 3): %q",
				match[1], len(cells), line)
			continue
		}
		state, commit := cells[len(cells)-2], cells[len(cells)-1]
		if !validDefectStates[state] {
			t.Errorf("defect %s has state %q, want one of OPEN/IN_PROGRESS/BLOCKED/FIXED/DEFERRED", match[1], state)
			continue
		}
		if state == "DEFERRED" {
			if err := validateDeferredMetadata(commit, program); err != nil {
				t.Errorf("defect %s has invalid DEFERRED metadata: %v", match[1], err)
			}
			continue
		}
		if state != "FIXED" {
			continue
		}
		hash, ok := citedCommit(commit)
		if !ok {
			t.Errorf("defect %s is marked FIXED with invalid commit reference %q; "+
				"use a 7–40 digit Git hash or verified@<hash>", match[1], commit)
			continue
		}
		if err := git(t, "cat-file", "-e", hash+"^{commit}"); err != nil {
			t.Errorf("defect %s cites commit %q, which does not resolve: %v", match[1], commit, err)
			continue
		}
		if err := git(t, "merge-base", "--is-ancestor", hash, "HEAD"); err != nil {
			t.Errorf("defect %s cites commit %q, which is not an ancestor of HEAD: %v", match[1], commit, err)
		}
	}
}

func TestDefectCommitReferenceSyntax(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain hash", value: "6938e544e", want: "6938e544e"},
		{name: "verified hash", value: "verified@1dc937e70", want: "1dc937e70"},
		{name: "markdown hash", value: "`6938e544e`", want: "6938e544e"},
		{name: "placeholder", value: "—"},
		{name: "arbitrary prose", value: "fixed in the auth patch"},
		{name: "too short", value: "123abc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := citedCommit(test.value)
			if got != test.want || ok != (test.want != "") {
				t.Fatalf("citedCommit(%q) = (%q, %v), want (%q, %v)", test.value, got, ok, test.want, test.want != "")
			}
		})
	}
}

func TestDeferredMetadataValidation(t *testing.T) {
	program := map[string]bool{"M0": true, "M1": true}
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "valid", value: "owner=QA; review=2026-09-01; target=M1; rationale=awaiting fixture", valid: true},
		{name: "invalid date", value: "owner=QA; review=2026-99-01; target=M1; rationale=awaiting fixture"},
		{name: "unknown milestone", value: "owner=QA; review=2026-09-01; target=M9; rationale=awaiting fixture"},
		{name: "missing owner", value: "owner=; review=2026-09-01; target=M1; rationale=awaiting fixture"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDeferredMetadata(test.value, program)
			if (err == nil) != test.valid {
				t.Fatalf("validateDeferredMetadata(%q) error = %v, valid = %v", test.value, err, test.valid)
			}
		})
	}
}

// TestInheritedReviewDefectsAreAllTracked pins the eleven defects the
// 2026-08-02 whole-application review left open. Dropping one from the tracker
// would quietly shrink the program's scope.
func TestInheritedReviewDefectsAreAllTracked(t *testing.T) {
	inherited := []string{
		"DEF-2", "DEF-9", "DEF-11", "DEF-21", "DEF-26", "DEF-35",
		"DEF-69", "DEF-156", "DEF-164", "DEF-297", "DEF-317",
	}
	tracked := make(map[string]bool)
	for _, line := range readLines(t, "DEFECTS.md") {
		if match := defectRow.FindStringSubmatch(line); match != nil {
			tracked[match[1]] = true
		}
	}
	for _, id := range inherited {
		if !tracked[id] {
			t.Errorf("%s is missing from DEFECTS.md; it is one of the eleven defects left open by "+
				"the 2026-08-02 whole-application review (see the Inherited section of DEFECTS.md) and is in this program's scope", id)
		}
	}
}

func TestQuarantinedFlakesHaveOwnersAndUnexpiredDates(t *testing.T) {
	for _, line := range readLines(t, "FLAKE_LOG.md") {
		cells := splitRow(line)
		if len(cells) != 8 || !strings.HasPrefix(cells[0], "`") || !strings.HasSuffix(cells[0], "`") {
			continue
		}
		owner, quarantined, deadline, state := cells[4], cells[5], cells[6], cells[7]
		if !strings.HasPrefix(strings.ToLower(quarantined), "yes") || strings.Contains(state, "FIXED") {
			continue
		}
		if owner == "" || owner == "—" {
			t.Errorf("quarantined flake %s has no owner", cells[0])
		}
		due, err := time.Parse("2006-01-02", deadline)
		if err != nil {
			t.Errorf("quarantined flake %s has invalid un-quarantine date %q", cells[0], deadline)
			continue
		}
		if !due.After(time.Now().UTC().Truncate(24 * time.Hour)) {
			t.Errorf("quarantined flake %s expired on %s; fix it or escalate it to DEFECTS.md", cells[0], deadline)
		}
	}
}

func TestOpenHighSeverityRisksHaveOwners(t *testing.T) {
	for _, line := range readLines(t, "TEST_PROGRAM.md") {
		cells := splitRow(line)
		if len(cells) != 7 || !strings.HasPrefix(cells[0], "R-") || cells[2] != "High" || cells[6] != "OPEN" {
			continue
		}
		if cells[4] == "" || cells[4] == "—" {
			t.Errorf("open high-severity risk %s has no owner", cells[0])
		}
	}
}

// TestTrackerFilesExist guards the tracker's own shape.
func TestTrackerFilesExist(t *testing.T) {
	for _, name := range []string{"STATUS.md", "DEFECTS.md", "FLAKE_LOG.md", "TEST_PROGRAM.md", "evidence-contract.json", "evidence-manifest.schema.json", "DASHBOARD.md", "HEADLINE_METRICS.md", "E2E_SUITES.md"} {
		if !isCommitted(t, name) {
			t.Errorf("docs/qa/%s is not a committed regular file in HEAD", name)
		}
	}
}

func TestGeneratedDashboardIsFresh(t *testing.T) {
	metrics := exec.Command("python3", "../../scripts/qa-generate-metrics.py",
		"--root", "../..", "--output", "docs/qa/HEADLINE_METRICS.md", "--check")
	if output, err := metrics.CombinedOutput(); err != nil {
		t.Fatalf("QA metrics freshness: %v\n%s", err, output)
	}
	command := exec.Command("python3", "../../scripts/qa-generate-dashboard.py",
		"--status", "STATUS.md", "--matrix", "E2E_SUITES.md", "--metrics", "HEADLINE_METRICS.md",
		"--api-contract", "evidence/M1/api-contract-matrix.json", "--output", "DASHBOARD.md", "--check")
	command.Dir = "."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("QA dashboard freshness: %v\n%s", err, output)
	}
}
