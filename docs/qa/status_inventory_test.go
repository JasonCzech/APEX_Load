// Package qa_test guards the test-program tracker against drift.
//
// The tracker under docs/qa is only useful if it cannot quietly disagree with
// reality, so it is enforced the same way this repository already enforces its
// other documentation-as-data contracts (see scripts/ and deployments/k8s/,
// which are likewise test-only packages asserting on non-Go files, and
// pkg/convert/enterprisearchive/optin_suite_inventory_test.go, which fails when
// a gated suite is missing from docs/OPTIN_TEST_SUITES.md).
package qa_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
)

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
	command := exec.Command("git", append([]string{"-C", "../.."}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		return &gitError{err: err, output: strings.TrimSpace(string(output))}
	}
	return nil
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

// hasCommittedEvidence reports whether HEAD contains a regular artifact below
// evidence/<milestone>. An untracked file, staged-only file, empty directory,
// or .gitkeep cannot close a milestone.
func hasCommittedEvidence(t *testing.T, milestone string) bool {
	t.Helper()
	root := filepath.Join("evidence", milestone)
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Name() == ".gitkeep" {
			return nil
		}
		repositoryPath := filepath.ToSlash(filepath.Join("docs", "qa", path))
		if git(t, "cat-file", "-e", "HEAD:"+repositoryPath) == nil {
			found = true
		}
		return nil
	})
	return found
}

func citedCommit(commit string) (string, bool) {
	match := commitReference.FindStringSubmatch(strings.Trim(strings.TrimSpace(commit), "`"))
	if match == nil {
		return "", false
	}
	return match[1], true
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

// TestDoneMilestonesHaveEvidence enforces the program's central rule: a
// milestone is closed by an artifact, never by an assertion.
func TestDoneMilestonesHaveEvidence(t *testing.T) {
	for _, line := range readLines(t, "STATUS.md") {
		match := milestoneRow.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		cells := splitRow(line)
		if len(cells) < 3 || cells[2] != "DONE" {
			continue
		}
		if !hasCommittedEvidence(t, match[1]) {
			t.Errorf("milestone %s is marked DONE but HEAD holds no artifact in docs/qa/evidence/%s/; "+
				"untracked or staged-only files do not count", match[1], match[1])
		}
	}
}

// TestEveryMilestoneHasAnEvidenceDir keeps the evidence tree in step with the
// milestone list, so closing a milestone never requires inventing a directory.
func TestEveryMilestoneHasAnEvidenceDir(t *testing.T) {
	for _, milestone := range programMilestones(t) {
		info, err := os.Stat(filepath.Join("evidence", milestone))
		if err != nil || !info.IsDir() {
			t.Errorf("docs/qa/evidence/%s/ is missing; every milestone needs an evidence directory", milestone)
		}
	}
}

// TestFixedDefectsCiteACommit stops a defect from being closed on a claim. The
// commit column must name something; an em dash placeholder is not a reference.
func TestFixedDefectsCiteACommit(t *testing.T) {
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
				"docs/reviews/WHOLE_APP_REVIEW_2026-08-02.md and is in this program's scope", id)
		}
	}
}

// TestTrackerFilesExist guards the tracker's own shape.
func TestTrackerFilesExist(t *testing.T) {
	for _, name := range []string{"STATUS.md", "DEFECTS.md", "RISK_REGISTER.md", "FLAKE_LOG.md", "TEST_PROGRAM.md"} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("docs/qa/%s is missing: %v", name, err)
		}
	}
}
