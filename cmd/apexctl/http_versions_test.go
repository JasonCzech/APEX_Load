package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/runcontract"
)

func TestHTTPVersionSummaryShowsRatiosOnlyWhenHTTP3WasUsed(t *testing.T) {
	if lines := httpVersionSummaryLines(nil); lines != nil {
		t.Fatalf("no mix = %v", lines)
	}
	tcp := &protocol.HTTPVersionMix{Actions: 4, ByVersion: map[string]int64{"2": 3, "cache": 1}}
	if lines := httpVersionSummaryLines(tcp); len(lines) != 1 || lines[0] != "  HTTP versions: HTTP/2 3 (75.0%), cache 1 (25.0%)" {
		t.Fatalf("TCP-only lines = %q", lines)
	}
	mixed := &protocol.HTTPVersionMix{Actions: 10, ByVersion: map[string]int64{"2": 2, "3": 8}, HTTP3Policies: 10, UpgradeEligible: 8, FellBack: 1, QUICAttempted: 9, QUICFailed: 1}
	lines := strings.Join(httpVersionSummaryLines(mixed), "\n")
	for _, want := range []string{"HTTP/3 8 (80.0%)", "Fell back to TCP: 12.5% (1 of 8 upgrade-eligible auto+h3 actions)", "QUIC failures: 11.1% (1 of 9 QUIC attempts)"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("lines missing %q:\n%s", want, lines)
		}
	}
	summary := httpVersionSummaryJSON(mixed)
	if summary["actions"] != int64(10) || summary["fallback_ratio"] != (protocol.Ratio{Numerator: 1, Denominator: 8, Value: 0.125}) {
		t.Fatalf("json = %+v", summary)
	}
	live := &runcontract.LiveMetrics{HTTPVersions: &runcontract.HTTPVersionMix{HTTPVersionMix: *mixed}}
	if copied := httpVersionsFromLive(live); copied == nil || copied.Actions != 10 || copied == mixed {
		t.Fatalf("copied = %+v", copied)
	}
	if httpVersionsFromLive(&runcontract.LiveMetrics{}) != nil {
		t.Fatal("an older controller sends no mix")
	}
}

func TestScriptValidateNotesHTTP3Requirements(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	upgrade := validateScriptFile(write("h3.json", `{"name":"h3","version":"1","protocol":"http","config":{"base_url":"https://shop.example","http":{"version":"auto+h3"}},"actions":[{"name":"home","type":"GET","target":"/"}]}`))
	if !upgrade.Valid || !strings.Contains(upgrade.HTTP3, "APEXLOAD_HTTP3") {
		t.Fatalf("auto+h3 result = %+v", upgrade)
	}
	exact := validateScriptFile(write("h3-http.json", `{"name":"h3","version":"1","protocol":"http","config":{"base_url":"http://shop.example","http":{"version":"3"}},"actions":[{"name":"home","type":"GET","target":"/"}]}`))
	if exact.Valid || !strings.Contains(strings.Join(exact.Issues, "\n"), "https://") {
		t.Fatalf("exact 3 over http:// = %+v", exact)
	}
	tcp := validateScriptFile(write("h2.json", `{"name":"h2","version":"1","protocol":"http","config":{"base_url":"https://shop.example","http":{"version":"2"}},"actions":[{"name":"home","type":"GET","target":"/"}]}`))
	if tcp.HTTP3 != "" {
		t.Fatalf("TCP script note = %q", tcp.HTTP3)
	}
}

func TestCLIPercentNeverShowsAZeroShareForNonzeroCounts(t *testing.T) {
	for _, tc := range []struct {
		part, whole int64
		want        string
	}{{1, 5000, "<0.1%"}, {0, 5000, "0.0%"}, {5, 1000, "0.5%"}, {1, 0, "n/a"}, {3, 4, "75.0%"}} {
		if got := cliPercent(tc.part, tc.whole); got != tc.want {
			t.Fatalf("cliPercent(%d, %d) = %q, want %q", tc.part, tc.whole, got, tc.want)
		}
	}
}
