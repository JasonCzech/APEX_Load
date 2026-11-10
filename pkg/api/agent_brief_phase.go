package api

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// agentPhaseBasis names what a brief's phase attribution compares: the mean
// instance above each transaction's P95 with the mean instance at or below
// its P50, as the report's tail attribution does.
const agentPhaseBasis = "p95_tail_vs_median"

// agentPhaseTransactionLimits bound the transactions a brief's phase
// attribution lists, per detail level: about 90 tokens each.
var agentPhaseTransactionLimits = map[string]int{agentDetailSummary: 2, agentDetailStandard: 5, agentDetailFull: 10}

// agentPhaseOf projects the report's time anatomy: the transactions with
// the largest tail deltas whose tail attribution the report could state,
// each with its per-component split and the component that carried most of
// it, plus how much of the run the anatomy covers. It is nil when the run
// has no anatomy.
func agentPhaseOf(report *RunArchiveReport, detail string) (*AgentPhaseAttribution, int) {
	anatomy := report.TimeAnatomy
	if anatomy == nil || len(anatomy.Transactions) == 0 {
		return nil, 0
	}
	out := &AgentPhaseAttribution{Basis: agentPhaseBasis, Transactions: []AgentPhaseTransaction{}, Partial: anatomy.Truncation.Truncated}
	if out.Partial {
		out.PartialReasons = slices.Clone(anatomy.Truncation.Reasons)
	}
	if rows := anatomy.Coverage.TransactionRows; rows > 0 {
		out.JoinCoveragePct = agentFloat(agentRound(float64(anatomy.Coverage.JoinedTransactionRows)/float64(rows)*100, "pct"))
	}
	for _, tx := range anatomy.Transactions {
		tail := tx.Tail
		switch {
		case tail == nil:
			continue
		case tail.Status != "ok":
			if out.Ineligible == nil {
				out.Ineligible = map[string]int{}
			}
			out.Ineligible[tail.Status]++
			continue
		}
		out.Eligible++
		if tail.Delta.TotalMs <= 0 {
			continue
		}
		entry := AgentPhaseTransaction{
			Transaction: agentCapRunes(tx.Name, agentSafeNameMaxRunes), TailDeltaMs: agentRound(tail.Delta.TotalMs, "ms"),
			MedianMs: agentRound(tail.Median.AvgDurationMs, "ms"), TailMs: agentRound(tail.Tail.AvgDurationMs, "ms"),
			TailSamples: tail.Tail.Joined, Sampled: tail.BandEstimate == "sampled", PhasesMs: map[string]float64{},
		}
		dominant := 0.0
		for _, component := range tail.Delta.Components {
			if value := agentRound(component.DeltaMs, "ms"); math.Abs(value) >= 0.5 {
				entry.PhasesMs[component.Component] = value
			}
			if component.DeltaMs > dominant {
				dominant, entry.DominantPhase = component.DeltaMs, component.Component
			}
		}
		if entry.DominantPhase != "" {
			entry.DominantSharePct = agentFloat(agentRound(dominant/tail.Delta.TotalMs*100, "pct"))
		}
		out.Transactions = append(out.Transactions, entry)
	}
	slices.SortFunc(out.Transactions, func(a, b AgentPhaseTransaction) int {
		return cmp.Or(cmp.Compare(b.TailDeltaMs, a.TailDeltaMs), strings.Compare(a.Transaction, b.Transaction))
	})
	omitted := 0
	if limit := agentPhaseTransactionLimits[detail]; len(out.Transactions) > limit {
		omitted = len(out.Transactions) - limit
		out.Transactions = out.Transactions[:limit]
	}
	return out, omitted
}

// agentPhaseWords names a tail component in a sentence.
var agentPhaseWords = map[string]string{"dns": "DNS", "connect": "connect", "tls": "TLS", "server": "server", "download": "download", "unsplit": "no TTFB split", "non_request": "outside requests", "overlap": "overlap"}

// agentPhaseOrder is the order a split is stated in.
var agentPhaseOrder = []string{"dns", "connect", "tls", "server", "download", "unsplit", "non_request", "overlap"}

// agentCapRunes keeps a name to at most n runes, marking a cut with "…".
func agentCapRunes(name string, n int) string {
	if utf8.RuneCountInString(name) <= n {
		return name
	}
	return string([]rune(name)[:n-1]) + "…"
}

// agentTrimPhaseAttribution drops a brief's phase rows from the smallest
// tail delta up while fits says the brief is over its caps: they are
// supplementary, so they give way before any finding or metric row.
// counted records each cut before fits is asked again, so every fit is
// measured with the truncation the cut adds.
func agentTrimPhaseAttribution(phase *AgentPhaseAttribution, fits func() bool, counted func()) {
	if phase == nil {
		return
	}
	for len(phase.Transactions) > 0 && !fits() {
		phase.Transactions = phase.Transactions[:len(phase.Transactions)-1]
		counted()
	}
}

// agentSignedMs renders a brief's rounded milliseconds with a sign.
func agentSignedMs(v float64) string {
	text := strconv.FormatFloat(v, 'f', -1, 64) + " ms"
	if v > 0 {
		return "+" + text
	}
	return text
}
