package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// RunArchiveHTTPVersionMix is the run's HTTP version mix: counts per
// negotiated version (plus unknown and cache), per routing reason and per
// fallback, the two published ratios with their denominators, and the
// Protocol Mix finding when a ratio exceeds its expectation.
type RunArchiveHTTPVersionMix struct {
	protocol.HTTPVersionMix
	FallbackRatio    protocol.Ratio `json:"fallback_ratio"`
	QUICFailureRatio protocol.Ratio `json:"quic_failure_ratio"`
	// MinSamples is the denominator a ratio needs before the finding judges
	// it; set when the run used HTTP/3.
	MinSamples int                           `json:"min_samples,omitempty"`
	Finding    *RunArchiveProtocolMixFinding `json:"finding,omitempty"`
}

// RunArchiveProtocolMixFinding reports the ratios above their expectation and
// the most frequent causes.
type RunArchiveProtocolMixFinding struct {
	Breaches   []RunArchiveProtocolMixBreach `json:"breaches"`
	TopReasons []RunArchiveProtocolMixReason `json:"top_reasons,omitempty"`
	Summary    string                        `json:"summary"`
}

type RunArchiveProtocolMixBreach struct {
	// Ratio is fallback or quic_failure.
	Ratio      string         `json:"ratio"`
	Observed   protocol.Ratio `json:"observed"`
	Expected   float64        `json:"expected"`
	MinSamples int            `json:"min_samples"`
}

type RunArchiveProtocolMixReason struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// carryRemoteHTTPVersionMixLocked keeps a target's last run-to-date mix when a
// batch carries none: a state-only or terminal publication, or an older
// generator. The mix is cumulative, so a batch without one never means zero.
// The caller holds the runner lock.
func carryRemoteHTTPVersionMixLocked(test *ManagedTest, target string, batch *generatorrpc.MetricsBatch) {
	if batch == nil || batch.HTTPVersionMix != nil {
		return
	}
	if prior, ok := test.remoteMetricsBatchesByTarget[target]; ok {
		batch.HTTPVersionMix = prior.HTTPVersionMix
	}
}

// remoteHTTPVersionMix sums each generator's latest run-to-date HTTP version
// mix. The caller holds the runner lock.
func remoteHTTPVersionMix(test *ManagedTest) *protocol.HTTPVersionMix {
	if test == nil {
		return nil
	}
	var total protocol.HTTPVersionMix
	for _, batch := range test.remoteMetricsBatchesByTarget {
		total.Merge(batch.HTTPVersionMix)
	}
	if total.Actions == 0 {
		return nil
	}
	return &total
}

func cloneRunArchiveHTTPVersionMix(mix *RunArchiveHTTPVersionMix) *RunArchiveHTTPVersionMix {
	if mix == nil {
		return nil
	}
	out := *mix
	out.HTTPVersionMix = *mix.HTTPVersionMix.Clone()
	if mix.Finding != nil {
		finding := *mix.Finding
		finding.Breaches = append([]RunArchiveProtocolMixBreach(nil), mix.Finding.Breaches...)
		finding.TopReasons = append([]RunArchiveProtocolMixReason(nil), mix.Finding.TopReasons...)
		out.Finding = &finding
	}
	return &out
}

// archiveTransactionHTTPVersionFacts reads an archived event's version facts:
// the receipt in protocol_evidence, else the typed fields older writers lack
// but newer ones always set.
func archiveTransactionHTTPVersionFacts(tx TransactionEvent) (protocol.HTTPVersionFacts, bool) {
	if facts, ok := protocol.HTTPVersionFactsFromEvidence(tx.ProtocolEvidence); ok {
		return facts, true
	}
	if tx.HTTPVersion == "" && tx.HTTPVersionFallback == "" {
		return protocol.HTTPVersionFacts{}, false
	}
	facts := protocol.HTTPVersionFacts{Negotiated: tx.HTTPVersion, Fallback: tx.HTTPVersionFallback, Attempts: tx.AttemptCount}
	if tx.HTTPVersion != "" {
		facts.Source = "wire"
	}
	return facts, true
}

// buildRunArchiveHTTPVersionMix publishes the mix, or nil for a run without
// HTTP actions. The finding is evaluated only when the run used HTTP/3.
func buildRunArchiveHTTPVersionMix(mix *protocol.HTTPVersionMix, expectation *dsl.ProtocolMixExpectation) *RunArchiveHTTPVersionMix {
	if mix == nil || mix.Actions == 0 {
		return nil
	}
	out := &RunArchiveHTTPVersionMix{HTTPVersionMix: *mix.Clone(), FallbackRatio: mix.FallbackRatio(), QUICFailureRatio: mix.QUICFailureRatio()}
	if !mix.UsedHTTP3() {
		return out
	}
	maxFallback, maxQUICFailure, minSamples := expectation.Effective()
	out.MinSamples = minSamples
	var breaches []RunArchiveProtocolMixBreach
	for _, candidate := range []struct {
		name     string
		observed protocol.Ratio
		expected float64
	}{{"fallback", out.FallbackRatio, maxFallback}, {"quic_failure", out.QUICFailureRatio, maxQUICFailure}} {
		if candidate.observed.Denominator >= int64(minSamples) && candidate.observed.Value > candidate.expected {
			breaches = append(breaches, RunArchiveProtocolMixBreach{Ratio: candidate.name, Observed: candidate.observed, Expected: candidate.expected, MinSamples: minSamples})
		}
	}
	if len(breaches) == 0 {
		return out
	}
	reasons := topProtocolMixReasons(mix, 3)
	parts := make([]string, 0, len(breaches))
	for _, breach := range breaches {
		label := "of upgrade-eligible auto+h3 actions fell back to TCP"
		if breach.Ratio == "quic_failure" {
			label = "of QUIC attempts failed"
		}
		parts = append(parts, fmt.Sprintf("%s %s (%d of %d; expected at most %s)", reportRatioPercent(breach.Observed.Value), label, breach.Observed.Numerator, breach.Observed.Denominator, reportRatioPercent(breach.Expected)))
	}
	summary := strings.Join(parts, "; ") + "."
	if len(reasons) > 0 {
		names := make([]string, 0, len(reasons))
		for _, reason := range reasons {
			names = append(names, fmt.Sprintf("%s (%d)", protocolMixReasonLabel(reason.Reason), reason.Count))
		}
		summary += " Top reasons: " + strings.Join(names, ", ") + "."
	}
	out.Finding = &RunArchiveProtocolMixFinding{Breaches: breaches, TopReasons: reasons, Summary: summary}
	return out
}

// protocolMixReasonLabels are reader labels for the bounded routing, fallback
// and QUIC error tokens; the dashboard receipt uses the same wording.
var protocolMixReasonLabels = map[string]string{
	"upgraded":            "Upgraded to HTTP/3",
	"not_advertised":      "TCP: HTTP/3 not yet advertised",
	"broken_cache":        "TCP: HTTP/3 failed recently",
	"tls_policy":          "TCP: TLS maximum below 1.3",
	"proxy_route":         "Proxy route",
	"integrated_auth":     "Integrated authentication",
	"exact_policy":        "Exact HTTP/3 policy",
	"quic_connect_failed": "QUIC could not connect",
	"h3_version_fallback": "Server asked for an earlier HTTP version",
	"misdirected_request": "421 Misdirected Request",
	"timeout":             "QUIC timeout",
	"handshake":           "QUIC handshake failed",
	"version_negotiation": "QUIC version negotiation failed",
	"connection_closed":   "QUIC connection closed",
	"request_rejected":    "Request rejected unprocessed",
	"possibly_sent":       "Possibly sent before failing",
	"version_fallback":    "HTTP/3 version fallback",
}

func protocolMixReasonLabel(token string) string {
	if label, ok := protocolMixReasonLabels[token]; ok {
		return label
	}
	return reportTokenLabel(token)
}

// protocolMixFindingTitle names the ratios a finding breaches.
func protocolMixFindingTitle(finding *RunArchiveProtocolMixFinding) string {
	fallback, quic := false, false
	for _, breach := range finding.Breaches {
		fallback = fallback || breach.Ratio == "fallback"
		quic = quic || breach.Ratio == "quic_failure"
	}
	switch {
	case fallback && quic:
		return "Protocol Mix: HTTP/3 fallback and QUIC failures above expectation"
	case quic:
		return "Protocol Mix: QUIC failures above expectation"
	}
	return "Protocol Mix: HTTP/3 fallback above expectation"
}

// topProtocolMixReasons ranks fallback kinds and QUIC error classes.
func topProtocolMixReasons(mix *protocol.HTTPVersionMix, limit int) []RunArchiveProtocolMixReason {
	var reasons []RunArchiveProtocolMixReason
	for reason, count := range mix.ByFallback {
		reasons = append(reasons, RunArchiveProtocolMixReason{Reason: reason, Count: count})
	}
	for reason, count := range mix.ByQUICError {
		reasons = append(reasons, RunArchiveProtocolMixReason{Reason: reason, Count: count})
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].Count != reasons[j].Count {
			return reasons[i].Count > reasons[j].Count
		}
		return reasons[i].Reason < reasons[j].Reason
	})
	if len(reasons) > limit {
		reasons = reasons[:limit]
	}
	return reasons
}

// declaredProtocolMixExpectation is the run's expectation: per field, the
// strictest any group's effective script sets.
func declaredProtocolMixExpectation(test *ManagedTest) *dsl.ProtocolMixExpectation {
	if test == nil {
		return nil
	}
	var out *dsl.ProtocolMixExpectation
	for _, group := range test.groupSpecs {
		if group.Script == nil || group.Script.Config.Results.ProtocolMix == nil {
			continue
		}
		declared := group.Script.Config.Results.ProtocolMix
		if out == nil {
			out = &dsl.ProtocolMixExpectation{}
		}
		out.MaxFallbackRatio = stricterRatio(out.MaxFallbackRatio, declared.MaxFallbackRatio)
		out.MaxQUICFailureRatio = stricterRatio(out.MaxQUICFailureRatio, declared.MaxQUICFailureRatio)
		if declared.MinSamples > 0 && (out.MinSamples == 0 || declared.MinSamples < out.MinSamples) {
			out.MinSamples = declared.MinSamples
		}
	}
	return out
}

func stricterRatio(current, candidate *float64) *float64 {
	if candidate == nil {
		return current
	}
	if current == nil || *candidate < *current {
		value := *candidate
		return &value
	}
	return current
}

// reportHTTPVersionMixRow is one row of a report's version mix table.
type reportHTTPVersionMixRow struct {
	Key      string
	Label    string
	Count    int64
	SharePct float64
}

// reportHTTPVersionOrder lists the mix buckets in display order.
var reportHTTPVersionOrder = []struct{ key, label string }{
	{"1.1", "HTTP/1.1"},
	{"2", "HTTP/2"},
	{"3", "HTTP/3"},
	{"unknown", "No response (version unknown)"},
	{"cache", "Served from cache"},
}

func reportHTTPVersionLabel(key string) string {
	for _, bucket := range reportHTTPVersionOrder {
		if bucket.key == key {
			return bucket.label
		}
	}
	return key
}

// reportHTTPVersionMixRows returns the non-empty buckets in display order;
// shares are of every counted HTTP action.
func reportHTTPVersionMixRows(mix *RunArchiveHTTPVersionMix) []reportHTTPVersionMixRow {
	if mix == nil || mix.Actions == 0 {
		return nil
	}
	rows := make([]reportHTTPVersionMixRow, 0, len(reportHTTPVersionOrder))
	for _, bucket := range reportHTTPVersionOrder {
		count := mix.ByVersion[bucket.key]
		if count == 0 {
			continue
		}
		rows = append(rows, reportHTTPVersionMixRow{Key: bucket.key, Label: bucket.label, Count: count, SharePct: float64(count) * 100 / float64(mix.Actions)})
	}
	return rows
}

// reportHTTPVersionPhaseRow is one negotiated version's phase averages.
type reportHTTPVersionPhaseRow struct {
	Key    string
	Label  string
	Phases RunArchiveNetworkPhases
}

func reportHTTPVersionPhaseRows(summary RunArchiveNetworkPhaseSummary) []reportHTTPVersionPhaseRow {
	var rows []reportHTTPVersionPhaseRow
	for _, bucket := range reportHTTPVersionOrder {
		if phases, ok := summary.ByHTTPVersion[bucket.key]; ok && phases.Count > 0 {
			rows = append(rows, reportHTTPVersionPhaseRow{Key: bucket.key, Label: bucket.label, Phases: phases})
		}
	}
	return rows
}

// reportRatioText renders a ratio with its denominator, or says why it has
// none.
func reportRatioText(ratio protocol.Ratio, noun string) string {
	if ratio.Denominator == 0 {
		return "n/a (no " + noun + ")"
	}
	return fmt.Sprintf("%s (%d of %d %s)", reportRatioPercent(ratio.Value), ratio.Numerator, ratio.Denominator, noun)
}

// reportRatioPercent keeps a share that rounds to 0.0% but is not zero
// visible as "<0.1%".
func reportRatioPercent(value float64) string {
	if value > 0 && value < 0.0005 {
		return "<0.1%"
	}
	return fmt.Sprintf("%.1f%%", value*100)
}

// reportCountText renders a bounded count map of routing tokens as
// "label (n), …", largest first.
func reportCountText(counts map[string]int64) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s (%d)", protocolMixReasonLabel(key), counts[key]))
	}
	return strings.Join(parts, ", ")
}

// reportProtocolMixLines are the PDF lines for the version mix.
func reportProtocolMixLines(summary RunArchiveNetworkPhaseSummary) []string {
	mix := summary.VersionMix
	if mix == nil {
		return nil
	}
	lines := []string{fmt.Sprintf("HTTP version mix (%d HTTP actions):", mix.Actions)}
	for _, row := range reportHTTPVersionMixRows(mix) {
		lines = append(lines, fmt.Sprintf("- %s: %d (%s)", row.Label, row.Count, reportRatioPercent(row.SharePct/100)))
	}
	if mix.UsedHTTP3() {
		lines = append(lines,
			"Fell back to TCP: "+reportRatioText(mix.FallbackRatio, "upgrade-eligible auto+h3 actions"),
			"QUIC failures: "+reportRatioText(mix.QUICFailureRatio, "QUIC attempts"))
	}
	if routing := reportCountText(mix.BySelection); routing != "" {
		lines = append(lines, "Routing: "+routing)
	}
	if mix.Attempts > 0 {
		lines = append(lines, fmt.Sprintf("HTTP attempts recorded by HTTP/3 actions: %d", mix.Attempts))
	}
	if mix.Finding != nil {
		lines = append(lines, "Protocol Mix finding: "+mix.Finding.Summary)
	}
	for _, row := range reportHTTPVersionPhaseRows(summary) {
		lines = append(lines, fmt.Sprintf("%s phases (%d): DNS %.1f ms, connect %.1f ms, TLS %.1f ms, TTFB %.1f ms (averages)", row.Label, row.Phases.Count, row.Phases.AvgDNSMs, row.Phases.AvgConnectMs, row.Phases.AvgTLSMs, row.Phases.AvgTTFBMs))
	}
	return lines
}

// writeProtocolMixCSVRows emits the version mix, the ratios with their
// denominators, the finding and the per-version phases.
func writeProtocolMixCSVRows(write reportCSVWriteFunc, summary RunArchiveNetworkPhaseSummary) error {
	mix := summary.VersionMix
	if mix == nil {
		return nil
	}
	rows := [][3]string{{"http_version_mix", "actions", formatCSVInt(mix.Actions)}}
	for _, row := range reportHTTPVersionMixRows(mix) {
		rows = append(rows, [3]string{"http_version_mix", "version:" + row.Key, formatCSVInt(row.Count)})
	}
	for _, key := range sortedInt64MapKeys(mix.BySelection) {
		rows = append(rows, [3]string{"http_version_mix", "selection:" + key, formatCSVInt(mix.BySelection[key])})
	}
	for _, key := range sortedInt64MapKeys(mix.ByFallback) {
		rows = append(rows, [3]string{"http_version_mix", "fallback:" + key, formatCSVInt(mix.ByFallback[key])})
	}
	for _, key := range sortedInt64MapKeys(mix.ByQUICError) {
		rows = append(rows, [3]string{"http_version_mix", "quic_error:" + key, formatCSVInt(mix.ByQUICError[key])})
	}
	rows = append(rows,
		[3]string{"http_version_mix", "attempts", formatCSVInt(mix.Attempts)},
		[3]string{"http_version_mix", "fallback_ratio", formatCSVFloat(mix.FallbackRatio.Value)},
		[3]string{"http_version_mix", "fallback_ratio_numerator", formatCSVInt(mix.FallbackRatio.Numerator)},
		[3]string{"http_version_mix", "fallback_ratio_denominator", formatCSVInt(mix.FallbackRatio.Denominator)},
		[3]string{"http_version_mix", "quic_failure_ratio", formatCSVFloat(mix.QUICFailureRatio.Value)},
		[3]string{"http_version_mix", "quic_failure_ratio_numerator", formatCSVInt(mix.QUICFailureRatio.Numerator)},
		[3]string{"http_version_mix", "quic_failure_ratio_denominator", formatCSVInt(mix.QUICFailureRatio.Denominator)})
	if mix.Finding != nil {
		rows = append(rows, [3]string{"protocol_mix_finding", "summary", mix.Finding.Summary})
	}
	for _, row := range rows {
		if err := write("network_phases", row[0], row[1], row[2]); err != nil {
			return err
		}
	}
	for _, row := range reportHTTPVersionPhaseRows(summary) {
		if err := writeNetworkPhaseCSVRows(write, "http_version:"+row.Key, row.Phases); err != nil {
			return err
		}
	}
	return nil
}

func sortedInt64MapKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
