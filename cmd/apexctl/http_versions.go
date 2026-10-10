package main

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// httpVersionsFromLive copies a controller's run-to-date mix into the CLI's
// final snapshot.
func httpVersionsFromLive(live *api.LiveMetrics) *protocol.HTTPVersionMix {
	if live == nil || live.HTTPVersions == nil {
		return nil
	}
	return live.HTTPVersions.HTTPVersionMix.Clone()
}

var cliVersionBuckets = []struct{ key, label string }{
	{"1.1", "HTTP/1.1"}, {"2", "HTTP/2"}, {"3", "HTTP/3"}, {"unknown", "no response"}, {"cache", "cache"},
}

// httpVersionSummaryLines are the run summary's HTTP version lines: the
// version shares and, when HTTP/3 was in play, both ratios with their
// denominators.
func httpVersionSummaryLines(mix *protocol.HTTPVersionMix) []string {
	if mix == nil || mix.Actions == 0 {
		return nil
	}
	parts := make([]string, 0, len(cliVersionBuckets))
	for _, bucket := range cliVersionBuckets {
		if count := mix.ByVersion[bucket.key]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d (%.1f%%)", bucket.label, count, float64(count)*100/float64(mix.Actions)))
		}
	}
	lines := []string{"  HTTP versions: " + strings.Join(parts, ", ")}
	if !mix.UsedHTTP3() {
		return lines
	}
	ratio := func(name string, value protocol.Ratio, noun string) string {
		if value.Denominator == 0 {
			return fmt.Sprintf("    %s: n/a (no %s)", name, noun)
		}
		return fmt.Sprintf("    %s: %.1f%% (%d of %d %s)", name, value.Value*100, value.Numerator, value.Denominator, noun)
	}
	return append(lines,
		ratio("Fell back to TCP", mix.FallbackRatio(), "upgrade-eligible auto+h3 actions"),
		ratio("QUIC failures", mix.QUICFailureRatio(), "QUIC attempts"))
}

// httpVersionSummaryJSON is the same summary for --format json.
func httpVersionSummaryJSON(mix *protocol.HTTPVersionMix) map[string]any {
	if mix == nil || mix.Actions == 0 {
		return nil
	}
	return map[string]any{
		"actions":            mix.Actions,
		"by_version":         mix.ByVersion,
		"fallback_ratio":     mix.FallbackRatio(),
		"quic_failure_ratio": mix.QUICFailureRatio(),
	}
}

// scriptHTTP3Note says what a script's HTTP/3 policies need to run. Policies
// save and validate everywhere; runs need execution enabled.
func scriptHTTP3Note(script *dsl.Script) string {
	if script == nil || !dsl.ScriptUsesHTTP3(script) {
		return ""
	}
	return "uses HTTP/3 (auto+h3 or 3): runs need APEXLOAD_HTTP3=true on the controller and every generator"
}
