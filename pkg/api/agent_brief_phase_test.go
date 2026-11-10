package api

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// agentFixtureAnatomy is a time anatomy with n transactions whose tails
// stated an attribution, the i-th +(420 - 40 i) ms over its median, mostly
// in server, and one transaction too thin to attribute.
func agentFixtureAnatomy(n int) *RunArchiveTimeAnatomy {
	anatomy := &RunArchiveTimeAnatomy{Schema: timeAnatomySchema, Coverage: RunArchiveTimeAnatomyCoverage{TransactionRows: 1000, JoinedTransactionRows: 950}}
	names := []string{"Checkout", "Browse"}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("Txn %02d", i)
		if i < len(names) {
			name = names[i]
		}
		total := 420 - 40*float64(i)
		anatomy.Transactions = append(anatomy.Transactions, RunArchiveTransactionAnatomy{Name: name, Instances: 400, Tail: &RunArchiveTailAttribution{
			Status: "ok", BandEstimate: "exact", Instances: 400,
			Tail: RunArchiveTailBand{Count: 20, Joined: 20, AvgDurationMs: 600 + total}, Median: RunArchiveTailBand{Count: 200, Joined: 200, AvgDurationMs: 600},
			Delta: RunArchiveTailDelta{TotalMs: total, Components: []RunArchiveTailComponent{
				{Component: "connect", DeltaMs: 45}, {Component: "tls", DeltaMs: 0.2}, {Component: "server", DeltaMs: total - 60}, {Component: "download", DeltaMs: 15},
			}},
		}})
	}
	anatomy.Transactions = append(anatomy.Transactions, RunArchiveTransactionAnatomy{Name: "Login", Instances: 12, Tail: &RunArchiveTailAttribution{Status: "insufficient_samples", Instances: 12}})
	return anatomy
}

func TestAgentBriefPhaseAttribution(t *testing.T) {
	report := agentFixtureReport()
	report.TimeAnatomy = agentFixtureAnatomy(8)
	report.TimeAnatomy.Truncation = RunArchiveTimeAnatomyTruncation{Truncated: true, Reasons: []string{"request_row_cap"}}
	fixture := agentFixture{name: "phase", report: report}

	standard := buildAgentFixtureBrief(t, fixture, agentDetailStandard)
	phase := standard.PhaseAttribution
	if phase == nil || phase.Basis != agentPhaseBasis || len(phase.Transactions) != 5 || phase.Eligible != 8 || phase.Ineligible["insufficient_samples"] != 1 ||
		!phase.Partial || phase.PartialReasons[0] != "request_row_cap" || phase.JoinCoveragePct == nil || *phase.JoinCoveragePct != 95 {
		t.Fatalf("%+v", phase)
	}
	top := phase.Transactions[0]
	if top.Transaction != "Checkout" || top.TailDeltaMs != 420 || top.MedianMs != 600 || top.TailMs != 1020 || top.TailSamples != 20 ||
		top.DominantPhase != "server" || top.DominantSharePct == nil || *top.DominantSharePct != 85.71 || top.PhasesMs["server"] != 360 || top.PhasesMs["connect"] != 45 {
		t.Fatalf("%+v", top)
	}
	// Components under half a millisecond are left out.
	if _, ok := top.PhasesMs["tls"]; ok {
		t.Fatalf("%+v", top.PhasesMs)
	}
	if standard.Truncation.Omitted["phase_attribution"] != 3 || !strings.Contains(standard.Truncation.HowToGetMore, "time_anatomy") {
		t.Fatalf("%+v", standard.Truncation)
	}
	markdown := renderAgentBriefMarkdown(standard)
	if !strings.Contains(markdown, "- Checkout: tail +420 ms vs median (20 tail samples): connect +45 ms, server +360 ms, download +15 ms") ||
		!strings.Contains(markdown, "partial anatomy: request_row_cap") {
		t.Fatal(markdown)
	}

	summary := buildAgentFixtureBrief(t, fixture, agentDetailSummary)
	if summary.PhaseAttribution == nil || len(summary.PhaseAttribution.Transactions) != 2 || summary.Truncation.Omitted["phase_attribution"] != 6 {
		t.Fatalf("%+v", summary.PhaseAttribution)
	}
	jsonSize, markdownSize := agentBriefSizes(summary)
	if budget := agentDetailBudgets[agentDetailSummary]; jsonSize > budget.JSONBytes || markdownSize > budget.MarkdownBytes {
		t.Fatalf("summary over budget: %d / %d", jsonSize, markdownSize)
	}
	full := buildAgentFixtureBrief(t, fixture, agentDetailFull)
	if len(full.PhaseAttribution.Transactions) != 8 || full.Truncation.Omitted["phase_attribution"] != 0 {
		t.Fatalf("%+v", full.PhaseAttribution)
	}
}

func TestAgentBriefPhaseAttributionAbsentWithoutAnatomy(t *testing.T) {
	brief := buildAgentFixtureBrief(t, agentFixture{name: "pass", report: agentFixtureReport()}, agentDetailStandard)
	if brief.PhaseAttribution != nil {
		t.Fatalf("%+v", brief.PhaseAttribution)
	}
	// An anatomy whose tails were all too thin lists none, and says why.
	report := agentFixtureReport()
	report.TimeAnatomy = agentFixtureAnatomy(0)
	brief = buildAgentFixtureBrief(t, agentFixture{name: "thin", report: report}, agentDetailStandard)
	if phase := brief.PhaseAttribution; phase == nil || len(phase.Transactions) != 0 || phase.Eligible != 0 || phase.Ineligible["insufficient_samples"] != 1 {
		t.Fatalf("%+v", phase)
	}
	if strings.Contains(renderAgentBriefMarkdown(brief), "Where the P95 tail goes") {
		t.Fatal("an empty attribution rendered a heading")
	}
}

// Long transaction names are capped, and the byte budget trims phase rows
// before findings, counting what it cut.
func TestAgentBriefPhaseAttributionStaysWithinTheBudget(t *testing.T) {
	report := agentFixtureReport()
	report.TimeAnatomy = agentFixtureAnatomy(8)
	for i := range report.TimeAnatomy.Transactions {
		report.TimeAnatomy.Transactions[i].Name = fmt.Sprintf("%d %s", i, strings.Repeat("x", 1500))
	}
	for _, detail := range []string{agentDetailSummary, agentDetailStandard, agentDetailFull} {
		brief := buildAgentFixtureBrief(t, agentFixture{name: "long", report: report}, detail)
		budget := agentDetailBudgets[detail]
		jsonSize, markdownSize := agentBriefSizes(brief)
		if jsonSize > budget.JSONBytes || markdownSize > budget.MarkdownBytes {
			t.Fatalf("%s: %d / %d bytes over %d / %d", detail, jsonSize, markdownSize, budget.JSONBytes, budget.MarkdownBytes)
		}
		phase := brief.PhaseAttribution
		for _, tx := range phase.Transactions {
			if n := utf8.RuneCountInString(tx.Transaction); n > agentSafeNameMaxRunes {
				t.Fatalf("%s: %d-rune name", detail, n)
			}
		}
		if listed, cut := len(phase.Transactions), brief.Truncation.Omitted["phase_attribution"]; listed+cut != 8 {
			t.Fatalf("%s: %d listed + %d omitted", detail, listed, cut)
		}
	}
	// With room for every row, nothing is cut by the budget.
	full := buildAgentFixtureBrief(t, agentFixture{name: "phase", report: func() *RunArchiveReport { r := agentFixtureReport(); r.TimeAnatomy = agentFixtureAnatomy(8); return r }()}, agentDetailFull)
	if len(full.PhaseAttribution.Transactions) != 8 {
		t.Fatalf("%d", len(full.PhaseAttribution.Transactions))
	}
}
