package api

import (
	"strings"
	"testing"
	"time"
)

func TestControllerFailoverReportSectionRendersAllFormats(t *testing.T) {
	renderer, ok := reportSectionRegistry["controller_failover"]
	if !ok {
		t.Fatal("controller_failover section is not registered")
	}
	var engineer RunArchiveReportProfile
	for _, profile := range buildRunArchiveReportProfiles() {
		if profile.ID == "engineer" {
			engineer = profile
		}
	}
	render := func(continuity *RunContinuityStatus) (string, string, map[string]string) {
		t.Helper()
		report := consistencyMatrixReport()
		report.Manifest.Continuity = continuity
		data := buildRunArchiveReportRenderData(report, engineer, nil, consistencyMatrixManifest())
		html, err := renderer.HTML(&data)
		if err != nil {
			t.Fatalf("HTML: %v", err)
		}
		var lines []string
		renderer.PDF(&data, &lines)
		rows := map[string]string{}
		if err := renderer.CSV(&data, func(section, key, metric, value string) error {
			rows[section+"/"+key+"/"+metric] = value
			return nil
		}); err != nil {
			t.Fatalf("CSV: %v", err)
		}
		return string(html), strings.Join(lines, "\n"), rows
	}

	adopted := time.Date(2026, 9, 25, 22, 47, 22, 0, time.UTC)
	html, pdf, csv := render(&RunContinuityStatus{Policy: "continue", Failovers: []RunFailover{
		{PriorGeneration: 1, Generation: 2, AdoptedAt: adopted, GapSeconds: 36.4, AdoptedGenerators: 2},
		{PriorGeneration: 2, Generation: 3, AdoptedAt: adopted.Add(time.Minute), GapSeconds: 31, AdoptedGenerators: 1, CompletedGenerators: 1},
	}})
	for _, want := range []string{"Controller failover", "survived 2 HA controller failover", "1 → 2", "2 → 3", "complete"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q:\n%s", want, html)
		}
	}
	for _, want := range []string{"Recorded 2 controller failovers", "Unobserved controller time: 67.4 s", "generation 1 -> 2", "1 finished during the gap"} {
		if !strings.Contains(pdf, want) {
			t.Errorf("PDF missing %q:\n%s", want, pdf)
		}
	}
	if strings.Contains(html, "is complete") || strings.Contains(pdf, "results are complete") || !strings.Contains(html, "missing sources are evaluated separately") {
		t.Fatal("failover narration implied complete archive evidence")
	}
	if csv["controller_failover//failovers"] != "2" || csv["controller_failover/2/completed_generators"] != "1" || csv["controller_failover/1/generation"] != "2" {
		t.Errorf("CSV rows = %v", csv)
	}

	// A failover that restarted a missing evacuation replacement says so in
	// every format.
	html, pdf, csv = render(&RunContinuityStatus{Policy: "continue", Failovers: []RunFailover{
		{PriorGeneration: 1, Generation: 2, AdoptedAt: adopted, GapSeconds: 12, AdoptedGenerators: 1, RestartedVUsers: 5, RestartedGroups: []string{"Store_Browse", "Store_Cart"}},
	}})
	const restarted = "5 VUsers of Store_Browse, Store_Cart lost their evacuation replacement and were restarted fresh on a new generator"
	if !strings.Contains(html, restarted) || !strings.Contains(pdf, restarted) {
		t.Errorf("restarted share missing: html=%q pdf=%q", html, pdf)
	}
	if csv["controller_failover/1/restarted_vusers"] != "5" || csv["controller_failover/1/restarted_groups"] != "Store_Browse; Store_Cart" {
		t.Errorf("restarted share CSV rows = %v", csv)
	}
	if got := controllerFailoverComparisonWarning(&RunContinuityStatus{Failovers: []RunFailover{{RestartedVUsers: 5, RestartedGroups: []string{"Store_Browse"}}}}); !strings.Contains(got, "5 VUsers of Store_Browse lost their evacuation replacement") {
		t.Errorf("comparison warning = %q", got)
	}

	html, pdf, csv = render(nil)
	if !strings.Contains(html, "No controller failover occurred") || !strings.Contains(pdf, "No controller failover occurred") || csv["controller_failover//failovers"] != "0" {
		t.Errorf("no-failover rendering: html=%q pdf=%q csv=%v", html, pdf, csv)
	}
}

func TestArchivedRunContinuityOnlyForRunsThatFailedOver(t *testing.T) {
	if got := archivedRunContinuity(&ManagedTest{Status: "COMPLETE", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}); got != nil {
		t.Fatalf("a run that never failed over archived continuity %+v", got)
	}
	checkpoint := `{"version":1,"execution_generation":3,"failovers":[{"prior_generation":2,"generation":3,"adopted_at":"2026-09-25T22:47:22Z","adopted_generators":2}]}`
	test := &ManagedTest{Status: "RUNNING", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}
	setContinuityCheckpointLocked(test, checkpoint)
	got := archivedRunContinuity(test)
	if got == nil || len(got.Failovers) != 1 || got.Armed {
		t.Fatalf("archived continuity = %+v, want one failover and not armed", got)
	}
}

func TestControllerFailoverComparisonWarning(t *testing.T) {
	if got := controllerFailoverComparisonWarning(nil); got != "" {
		t.Fatalf("warning for a run without failover: %q", got)
	}
	got := controllerFailoverComparisonWarning(&RunContinuityStatus{Failovers: []RunFailover{{GapSeconds: 30}, {GapSeconds: 12}}})
	if strings.Contains(got, "results are complete") || !strings.Contains(got, "separate verification") {
		t.Fatal("failover warning overstates completeness")
	}
	if !strings.Contains(got, "2 controller failover(s)") || !strings.Contains(got, "42 s unobserved") {
		t.Fatalf("comparison warning = %q", got)
	}
}

// A failover recorded in memory survives a re-arm that could not be persisted:
// reloading the older checkpoint must not erase it.
func TestContinuityHistorySurvivesUnpersistedRearm(t *testing.T) {
	test := &ManagedTest{Status: "RUNNING", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}
	setContinuityCheckpointLocked(test, `{"version":1,"execution_generation":1}`)
	test.continuityFailovers = append(test.continuityFailovers, RunFailover{PriorGeneration: 1, Generation: 2})
	setContinuityCheckpointLocked(test, `{"version":1,"execution_generation":1}`)
	status := runContinuityStatusLocked(test)
	if status == nil || len(status.Failovers) != 1 || !status.Armed {
		t.Fatalf("status after an unpersisted re-arm = %+v, want the in-memory failover kept", status)
	}
}

func TestStoredSnapshotKeepsLongerInMemoryFailoverHistory(t *testing.T) {
	current := &ManagedTest{Status: "COMPLETE", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}
	setContinuityCheckpointLocked(current, `{"version":1,"execution_generation":1}`)
	current.continuityFailovers = []RunFailover{{PriorGeneration: 1, Generation: 2}}
	stored := &ManagedTest{Status: "COMPLETE"}
	setContinuityCheckpointLocked(stored, `{"version":1,"execution_generation":1}`)
	applyStoredManagedTestSnapshot(current, stored)
	if len(current.continuityFailovers) != 1 {
		t.Fatalf("reload discarded the in-memory failover: %+v", current.continuityFailovers)
	}
}
