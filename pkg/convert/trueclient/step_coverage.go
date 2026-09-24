package trueclient

// step_coverage.go — the published LoadRunner TruClient / TruClient 2.0 step
// toolbox coverage matrix. It joins the published step inventory with the
// resolved StepScope (family/status/shape/notes), backing both the generated
// evaluation doc (docs/LOADRUNNER_COVERAGE.md, via
// cmd/webcoverage) and the support-matrix endpoint.

import (
	"fmt"
	"sort"
	"strings"
)

// publishedTruClientStepNames is the complete published TruClient step toolbox
// (OpenText VuGen 25.1 "TruClient step arguments" + the flow/structural steps),
// in Web + Mobile Web scope. Native Mobile is out of scope. This slice is the
// authoritative inventory the coverage doc renders.
var publishedTruClientStepNames = []string{
	// object steps
	"Mouse Down", "Mouse Up", "Mouse Over", "Click", "Double Click", "Drag", "Drag To",
	"Get Property", "Scroll", "Upload", "Verify", "Wait for Property", "Press Key",
	"Type", "Set", "Select", "Multi Select", "Seek", "Set Day",
	// browser-role steps
	"Activate", "Activate Tab", "Close Tab", "Add Tab", "Navigate", "Go Back", "Go Forward",
	"Resize", "Dialog - Confirm", "Dialog Prompt", "Dialog - Authenticate", "Dialog - Prompt Password",
	// non-object steps
	"Evaluate JavaScript", "Evaluate C", "Call Function", "Catch Error", "For Loop",
	"Generic API Action", "If Block", "Rendezvous", "Verify PDF Content", "Wait", "Wait for File Download",
	// flow / structural
	"Start Transaction", "End Transaction", "Else", "Break", "Continue",
}

// StepCoverageRow is one row of the TruClient step coverage matrix.
type StepCoverageRow struct {
	Step          string `json:"step"`
	Family        string `json:"family"`
	Status        string `json:"status"`
	APEXLoadShape string `json:"apexload_shape"`
	Notes         string `json:"notes"`
}

// StepCoverage returns the full published TruClient step toolbox coverage matrix,
// sorted by step name.
func StepCoverage() []StepCoverageRow {
	rows := make([]StepCoverageRow, 0, len(publishedTruClientStepNames))
	for _, name := range publishedTruClientStepNames {
		scope, _ := LookupStepScope(name)
		rows = append(rows, StepCoverageRow{
			Step:          name,
			Family:        scope.CanonicalName,
			Status:        string(scope.Status),
			APEXLoadShape: scope.APEXLoadShape,
			Notes:         scope.Notes,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Step < rows[j].Step })
	return rows
}

// RenderStepCoverageMarkdown renders the committed evaluation document. The byte
// output is pinned by the freshness test.
func RenderStepCoverageMarkdown() string {
	rows := StepCoverage()

	counts := map[ScopeStatus]int{}
	for _, row := range rows {
		counts[ScopeStatus(row.Status)]++
	}

	var b strings.Builder
	b.WriteString("## TruClient step coverage\n\n")
	b.WriteString("Evaluation of the complete published OpenText/Micro Focus VuGen 25.1 **TruClient** step ")
	b.WriteString("toolbox against the APEX Load conversion engine (`pkg/convert/trueclient`). Source: VuGen ")
	b.WriteString("Function Reference, \"TruClient step arguments\".\n\n")
	b.WriteString("Scope = the **Web + Mobile Web** step toolbox (classic TruClient and TruClient 2.0). ")
	b.WriteString("TruClient **Native Mobile** is out of scope (external-runner, no browser DSL).\n\n")
	b.WriteString("> Two-level distinction: the step matrix below is shared across TruClient variants, but ")
	b.WriteString("**script-type executability differs** — TruClient 2.0 - Web and Mobile Web import as ")
	b.WriteString("executable browser profiles, while classic **TruClient - Web stays review-only/planned** ")
	b.WriteString("until it has fixture-backed replay validation. A `supported` step does not by itself make ")
	b.WriteString("classic TruClient - Web executable. Browser VUsers remain diagnostic canaries; HTTP/API ")
	b.WriteString("protocols carry density load after conversion.\n\n")

	fmt.Fprintf(&b, "**Total published steps:** %d\n\n", len(rows))
	b.WriteString("| Disposition | Count | Meaning |\n")
	b.WriteString("|---|---|---|\n")
	fmt.Fprintf(&b, "| `supported` | %d | Translated to an executable browser/engine DSL action. |\n", counts[ScopeSupported])
	fmt.Fprintf(&b, "| `review_only` | %d | Preserved as a review-gated placeholder; embeds arbitrary code or has no browser-replay equivalent, so it is not executed (never fabricated). |\n", counts[ScopeReviewOnly])
	fmt.Fprintf(&b, "| `planned` | %d | Recognized; conversion path planned. |\n", counts[ScopePlanned])
	fmt.Fprintf(&b, "| `out_of_scope` | %d | Outside the browser import scope. |\n\n", counts[ScopeOutOfScope])

	b.WriteString("Every published step resolves to one of the dispositions above — none is silently skipped. Enforced by `TestLookupStepScopeResolvesEveryPublishedStep` and `TestConvertReviewOnlyStepsPreservedNotSkipped`.\n\n")

	b.WriteString("| Step | Family | Status | Target | Notes |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s |\n",
			row.Step, row.Family, row.Status, row.APEXLoadShape, row.Notes)
	}
	return b.String()
}
