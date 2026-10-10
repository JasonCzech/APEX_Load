package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// archivedRoundTrip decodes an event the way a stored archive is read back,
// so evidence numbers arrive as float64.
func archivedRoundTrip(t *testing.T, tx TransactionEvent) TransactionEvent {
	t.Helper()
	encoded, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	var decoded TransactionEvent
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mixedHTTPVersionRun() []TransactionEvent {
	return []TransactionEvent{
		// auto+h3 upgraded and served on HTTP/3.
		{ActionName: "home", HTTPVersion: "3", AttemptCount: 1, ConnectMs: 12, TTFBMs: 30, ProtocolEvidence: map[string]any{
			"http_version_policy": "auto+h3", "http_version_negotiated": "3", "http_version_source": "wire",
			"http_version_selection_reason": "upgraded", "http_version_fallback": "none", "attempt_count": 1, "quic_attempted": true,
		}},
		// auto+h3 upgraded, the handshake failed and the action fell back.
		{ActionName: "home", HTTPVersion: "2", HTTPVersionFallback: "quic_connect_failed", AttemptCount: 2, ConnectMs: 40, TLSMs: 8, TTFBMs: 50, ProtocolEvidence: map[string]any{
			"http_version_policy": "auto+h3", "http_version_negotiated": "2", "http_version_source": "wire",
			"http_version_selection_reason": "upgraded", "http_version_fallback": "quic_connect_failed", "quic_error_class": "handshake",
			"attempt_count": 2, "quic_attempted": true,
		}},
		// auto+h3 before any advertisement: TCP, not upgrade-eligible.
		{ActionName: "home", HTTPVersion: "2", AttemptCount: 1, ConnectMs: 20, TLSMs: 10, TTFBMs: 40, ProtocolEvidence: map[string]any{
			"http_version_policy": "auto+h3", "http_version_negotiated": "2", "http_version_source": "wire",
			"http_version_selection_reason": "not_advertised", "http_version_fallback": "none", "attempt_count": 1, "quic_attempted": false,
		}},
		// A cache hit: counted, but not as a wire version.
		{ActionName: "home", ProtocolEvidence: map[string]any{"http_version_policy": "auto", "http_version_negotiated": "2", "http_version_source": "cache"}},
		// Exact HTTP/3 that failed before any response.
		{ActionName: "api", AttemptCount: 1, ProtocolEvidence: map[string]any{
			"http_version_policy": "3", "http_version_selection_reason": "exact_policy", "quic_error_class": "timeout",
			"attempt_count": 1, "quic_attempted": true,
		}},
		// An event with only the typed fields.
		{ActionName: "legacy", HTTPVersion: "1.1", ConnectMs: 5, TTFBMs: 20},
		// Not an HTTP action.
		{ActionName: "think", DurationMs: 1000},
	}
}

func TestRunArchiveVersionMixCountsLogicalActionsAndAttemptsOnAMixedRun(t *testing.T) {
	acc := newRunArchiveNetworkPhaseAccumulator()
	for _, tx := range mixedHTTPVersionRun() {
		acc.merge(archivedRoundTrip(t, tx))
	}
	summary := acc.finalize()
	mix := summary.VersionMix
	if mix == nil {
		t.Fatal("version mix missing")
	}
	if mix.Actions != 6 {
		t.Fatalf("actions = %d, want 6 HTTP actions (the think time is not one)", mix.Actions)
	}
	wantVersions := map[string]int64{"3": 1, "2": 2, "1.1": 1, "unknown": 1, "cache": 1}
	for version, count := range wantVersions {
		if mix.ByVersion[version] != count {
			t.Fatalf("by_version[%s] = %d, want %d (%v)", version, mix.ByVersion[version], count, mix.ByVersion)
		}
	}
	if len(mix.ByVersion) != len(wantVersions) {
		t.Fatalf("by_version = %v", mix.ByVersion)
	}
	if mix.Attempts != 5 {
		t.Fatalf("attempts = %d, want 5 (1 + 2 + 1 + 1 recorded by HTTP/3 actions)", mix.Attempts)
	}
	if mix.FallbackRatio != (protocol.Ratio{Numerator: 1, Denominator: 2, Value: 0.5}) {
		t.Fatalf("fallback ratio = %+v, want 1 of 2 upgrade-eligible actions", mix.FallbackRatio)
	}
	if mix.QUICFailureRatio.Numerator != 2 || mix.QUICFailureRatio.Denominator != 3 {
		t.Fatalf("QUIC failure ratio = %+v, want 2 of 3 attempts", mix.QUICFailureRatio)
	}
	if mix.BySelection["upgraded"] != 2 || mix.BySelection["not_advertised"] != 1 || mix.BySelection["exact_policy"] != 1 {
		t.Fatalf("by_selection = %v", mix.BySelection)
	}
	if mix.ByFallback["quic_connect_failed"] != 1 || len(mix.ByFallback) != 1 {
		t.Fatalf("by_fallback = %v", mix.ByFallback)
	}
	// Twenty samples are needed before the default expectation applies.
	if mix.Finding != nil {
		t.Fatalf("finding below the minimum sample count: %+v", mix.Finding)
	}

	// Phases split by the negotiated wire version; the cache hit and the
	// request without a response add none.
	if summary.ByHTTPVersion["3"].Count != 1 || summary.ByHTTPVersion["2"].Count != 2 || summary.ByHTTPVersion["1.1"].Count != 1 {
		t.Fatalf("by_http_version = %+v", summary.ByHTTPVersion)
	}
	if got := summary.ByHTTPVersion["2"].AvgConnectMs; got != 30 {
		t.Fatalf("HTTP/2 connect average = %v, want 30", got)
	}
	if _, ok := summary.ByHTTPVersion["cache"]; ok {
		t.Fatal("cache hits must not get a phase row")
	}

	if summary.VersionMixByAction["home"].Actions != 4 || summary.VersionMixByAction["api"].Actions != 1 || summary.VersionMixByAction["legacy"].Actions != 1 {
		t.Fatalf("version mix by action = %+v", summary.VersionMixByAction)
	}
	if _, ok := summary.VersionMixByAction["think"]; ok {
		t.Fatal("non-HTTP actions must not appear in the per-action mix")
	}
}

func TestRunArchiveProtocolMixFindingUsesTheDeclaredExpectation(t *testing.T) {
	acc := newRunArchiveNetworkPhaseAccumulator()
	for _, tx := range mixedHTTPVersionRun() {
		acc.merge(tx)
	}
	maxFallback := 0.4
	summary := acc.finalizeWithExpectation(&dsl.ProtocolMixExpectation{MaxFallbackRatio: &maxFallback, MinSamples: 2})
	finding := summary.VersionMix.Finding
	if finding == nil || len(finding.Breaches) != 2 {
		t.Fatalf("finding = %+v, want both ratios breached", finding)
	}
	if finding.Breaches[0].Ratio != "fallback" || finding.Breaches[0].Expected != 0.4 || finding.Breaches[1].Ratio != "quic_failure" || finding.Breaches[1].Expected != 0.05 {
		t.Fatalf("breaches = %+v", finding.Breaches)
	}
	if !strings.Contains(finding.Summary, "50.0% of upgrade-eligible auto+h3 actions fell back to TCP (1 of 2; expected at most 40.0%)") {
		t.Fatalf("summary = %q", finding.Summary)
	}
	if !strings.Contains(finding.Summary, "Top reasons: QUIC handshake failed (1), QUIC could not connect (1), QUIC timeout (1).") {
		t.Fatalf("summary reasons = %q", finding.Summary)
	}
	if title := protocolMixFindingTitle(finding); title != "Protocol Mix: HTTP/3 fallback and QUIC failures above expectation" {
		t.Fatalf("title = %q", title)
	}

	report := &RunArchiveReport{NetworkPhases: summary}
	var derived *ReportFinding
	for _, candidate := range deriveReportFindings(report) {
		if strings.HasPrefix(candidate.Title, "Protocol Mix") {
			derived = &candidate
		}
	}
	if derived == nil || derived.Severity != "minor" || derived.Observed != finding.Summary || !strings.Contains(derived.Evidence, "2 or more samples") {
		t.Fatalf("derived finding = %+v", derived)
	}
}

func TestRunArchiveProtocolMixIsNotEvaluatedWithoutHTTP3(t *testing.T) {
	acc := newRunArchiveNetworkPhaseAccumulator()
	for i := 0; i < 30; i++ {
		acc.merge(TransactionEvent{ActionName: "home", HTTPVersion: "2", TTFBMs: 10})
	}
	zero := 0.0
	mix := acc.finalizeWithExpectation(&dsl.ProtocolMixExpectation{MaxFallbackRatio: &zero, MaxQUICFailureRatio: &zero, MinSamples: 1}).VersionMix
	if mix == nil || mix.Actions != 30 || mix.UsedHTTP3() || mix.Finding != nil {
		t.Fatalf("TCP-only mix = %+v", mix)
	}
	if mix.FallbackRatio.Denominator != 0 || mix.QUICFailureRatio.Denominator != 0 {
		t.Fatalf("ratios without denominators = %+v / %+v", mix.FallbackRatio, mix.QUICFailureRatio)
	}
}

func TestRunArchiveVersionMixAbsentForArchivesWithoutVersionFacts(t *testing.T) {
	acc := newRunArchiveNetworkPhaseAccumulator()
	acc.merge(TransactionEvent{ActionName: "home", DNSMs: 1, ConnectMs: 2, TTFBMs: 20})
	summary := acc.finalize()
	if summary.VersionMix != nil || summary.ByHTTPVersion != nil || summary.VersionMixByAction != nil {
		t.Fatalf("an archive without version facts must not get a mix: %+v", summary)
	}
	if summary.Samples != 1 {
		t.Fatalf("phases still aggregate: %+v", summary)
	}

	// An archive from before the mix renders in every format.
	report := consistencyMatrixReport()
	report.NetworkPhases = summary
	data := buildRunArchiveReportRenderData(report, buildRunArchiveReportProfiles()[0], nil, consistencyMatrixManifest())
	renderer := reportSectionRegistry["network_phases"]
	html, err := renderer.HTML(&data)
	if err != nil || strings.Contains(string(html), "HTTP version mix") {
		t.Fatalf("legacy HTML = %v / %s", err, html)
	}
	var lines []string
	renderer.PDF(&data, &lines)
	if strings.Contains(strings.Join(lines, "\n"), "HTTP version mix") {
		t.Fatalf("legacy PDF lines = %v", lines)
	}
}

func TestNetworkPhasesSectionRendersTheVersionMixInEveryFormat(t *testing.T) {
	acc := newRunArchiveNetworkPhaseAccumulator()
	for _, tx := range mixedHTTPVersionRun() {
		acc.merge(tx)
	}
	maxFallback := 0.4
	report := consistencyMatrixReport()
	report.NetworkPhases = acc.finalizeWithExpectation(&dsl.ProtocolMixExpectation{MaxFallbackRatio: &maxFallback, MinSamples: 2})
	data := buildRunArchiveReportRenderData(report, buildRunArchiveReportProfiles()[0], nil, consistencyMatrixManifest())
	renderer := reportSectionRegistry["network_phases"]

	html, err := renderer.HTML(&data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Phases by negotiated HTTP version", "<td>HTTP/3</td><td class=\"num\">1</td>",
		"HTTP version mix", "<td>No response (version unknown)</td><td class=\"num\">1</td><td class=\"num\">16.67%</td>",
		"50.0% (1 of 2 upgrade-eligible auto&#43;h3 actions)", "66.7% (2 of 3 QUIC attempts)",
		"Routing: Upgraded to HTTP/3 (2), Exact HTTP/3 policy (1), TCP: HTTP/3 not yet advertised (1)", "HTTP attempts recorded by HTTP/3 actions: 5.",
		"Protocol Mix",
	} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("HTML missing %q:\n%s", want, html)
		}
	}

	var lines []string
	renderer.PDF(&data, &lines)
	pdf := strings.Join(lines, "\n")
	for _, want := range []string{"HTTP version mix (6 HTTP actions):", "- HTTP/3: 1 (16.7%)", "Fell back to TCP: 50.0% (1 of 2", "QUIC failures: 66.7% (2 of 3", "Protocol Mix finding: ", "HTTP/2 phases (2): DNS 0.0 ms, connect 30.0 ms"} {
		if !strings.Contains(pdf, want) {
			t.Fatalf("PDF missing %q:\n%s", want, pdf)
		}
	}

	csv := map[string]string{}
	if err := renderer.CSV(&data, func(section, key, metric, value string) error {
		csv[key+"|"+metric] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"http_version_mix|version:3": "1", "http_version_mix|fallback_ratio_denominator": "2", "http_version_mix|quic_failure_ratio_numerator": "2",
		"http_version_mix|selection:upgraded": "2", "http_version_mix|quic_error:timeout": "1", "http_version_mix|attempts": "5",
		"http_version:2|count": "2",
	} {
		if csv[key] != want {
			t.Fatalf("CSV %s = %q, want %q", key, csv[key], want)
		}
	}
	if !strings.Contains(csv["protocol_mix_finding|summary"], "fell back to TCP") {
		t.Fatalf("CSV finding = %q", csv["protocol_mix_finding|summary"])
	}
}

func TestDeclaredProtocolMixExpectationTakesTheStrictestGroupValue(t *testing.T) {
	loose, strict := 0.2, 0.01
	script := func(expectation *dsl.ProtocolMixExpectation) *dsl.Script {
		return &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{ProtocolMix: expectation}}}
	}
	test := &ManagedTest{groupSpecs: []engine.GroupConfig{
		{Script: script(&dsl.ProtocolMixExpectation{MaxFallbackRatio: &loose, MinSamples: 50})},
		{Script: script(nil)},
		{Script: script(&dsl.ProtocolMixExpectation{MaxFallbackRatio: &strict, MaxQUICFailureRatio: &loose, MinSamples: 10})},
	}}
	got := declaredProtocolMixExpectation(test)
	if got == nil || *got.MaxFallbackRatio != strict || *got.MaxQUICFailureRatio != loose || got.MinSamples != 10 {
		t.Fatalf("expectation = %+v", got)
	}
	if declaredProtocolMixExpectation(&ManagedTest{groupSpecs: []engine.GroupConfig{{Script: script(nil)}}}) != nil {
		t.Fatal("no declaration means the defaults (nil)")
	}
}
