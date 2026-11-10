package api

import (
	"fmt"
	"strings"
	"testing"
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
