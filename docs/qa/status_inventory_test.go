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
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// expectedMilestones is the full program from docs/qa/TEST_PROGRAM.md. A
// milestone may be renamed or rescheduled, but it may not silently vanish.
var expectedMilestones = []string{"M0", "M1", "M2", "M3", "M4", "M5", "M6", "M7", "M8"}

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
	milestoneRow = regexp.MustCompile(`^\|\s*(M\d)\s*\|`)
	defectRow    = regexp.MustCompile(`^\|\s*((?:DEF|QA)-\d+)\s*\|`)
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

// hasEvidence reports whether evidence/<milestone>/ holds anything beyond the
// .gitkeep placeholder. A milestone is not DONE until it has produced an
// artifact; that is the whole point of the program's evidence-over-assertion
// rule.
func hasEvidence(t *testing.T, milestone string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("evidence", milestone))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Name() != ".gitkeep" {
			return true
		}
	}
	return false
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

	for _, milestone := range expectedMilestones {
		state, ok := seen[milestone]
		if !ok {
			t.Errorf("STATUS.md is missing milestone %s; every milestone in TEST_PROGRAM.md must appear", milestone)
			continue
		}
		if !validStates[state] {
			t.Errorf("milestone %s has state %q, want one of NOT_STARTED/IN_PROGRESS/BLOCKED/DONE", milestone, state)
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
		if !hasEvidence(t, match[1]) {
			t.Errorf("milestone %s is marked DONE but docs/qa/evidence/%s/ holds no artifact; "+
				"either commit the evidence or move it back to IN_PROGRESS", match[1], match[1])
		}
	}
}

// TestEveryMilestoneHasAnEvidenceDir keeps the evidence tree in step with the
// milestone list, so closing a milestone never requires inventing a directory.
func TestEveryMilestoneHasAnEvidenceDir(t *testing.T) {
	for _, milestone := range expectedMilestones {
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
		if commit == "" || commit == "—" || commit == "-" || commit == "TBD" {
			t.Errorf("defect %s is marked FIXED with no commit reference (%q); "+
				"cite the commit that closed it", match[1], commit)
		}
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
