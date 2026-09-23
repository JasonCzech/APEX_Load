package enterprisearchive

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
	"testing"
)

func TestImportRandomPercentageThinkTime(t *testing.T) {
	for _, tc := range []struct {
		low, high string
		valid     bool
	}{{"50", "150", true}, {"0", "0", true}, {"12.5", "10000", true}, {"", "150", false}, {"-1", "100", false}, {"150", "50", false}, {"NaN", "100", false}, {"0", "Inf", false}, {"0", "10001", false}} {
		t.Run(tc.low+"_"+tc.high, func(t *testing.T) {
			cfg := fmt.Sprintf("[ThinkTime]\nOptions=RANDOM\nThinkTimeRandomLow=%s\nThinkTimeRandomHigh=%s\nLimitFlag=1\nLimit=2", tc.low, tc.high)
			r, err := ConvertZip(buildSourceZip(t, map[string]string{"sample.usr": "[General]\nType=Web\n[Actions]\nAction=Action.c", "default.cfg": cfg, "Action.c": `Action(){ web_url("one", "URL=https://example.test", LAST); lr_think_time(4); return 0; }`}), ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			think := r.Script.Config.ThinkTime
			if tc.valid {
				if think.Type != "random_percentage" || think.LimitMs != 2000 {
					t.Fatalf("think = %#v", think)
				}
				if issues := dsl.ValidateScriptIssues(r.Script); len(issues) > 0 {
					t.Fatalf("validation: %v", issues)
				}
			} else {
				if think.Type != "recorded" {
					t.Fatalf("invalid range did not fall back: %#v", think)
				}
			}
			if got := runtimeConfigHasIssue(ParseRuntimeCfg(cfg), "think_time"); got == tc.valid {
				t.Fatalf("review issue=%v valid=%v", got, tc.valid)
			}
		})
	}
}
func TestInactiveThinkTimeSettingsAreNotClaimedAsMapped(t *testing.T) {
	sections := map[string]map[string]string{"ThinkTime": {"Options": "NOTHINK", "Factor": "1", "Limit": "1", "LimitFlag": "0", "ThinkTimeRandomLow": "50", "ThinkTimeRandomHigh": "150"}}
	rows := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{"default.cfg": sections}, "http")["default.cfg"]["ThinkTime"]
	for _, key := range []string{"Factor", "Limit", "ThinkTimeRandomLow", "ThinkTimeRandomHigh"} {
		if rows[key].Status != RuntimeSettingDispositionNonApplicable {
			t.Fatalf("%s: %#v", key, rows[key])
		}
	}
	if rows["Options"].Status != RuntimeSettingDispositionMapped {
		t.Fatal(rows["Options"])
	}
	cfg := ParseRuntimeCfg("[ThinkTime]\nOptions=RANDOM\nThinkTimeRandomLow=50\nthinktimerandomlow=100\nThinkTimeRandomHigh=150")
	if cfg.ThinkTimeType != "recorded" || cfg.ThinkTimeMinPercent != 0 || cfg.ThinkTimeMaxPercent != 0 || !runtimeConfigHasIssue(cfg, "think_time") {
		t.Fatalf("conflicting bounds: %#v", cfg)
	}
	if !strings.Contains(rows["ThinkTimeRandomLow"].Note, "inactive") {
		t.Fatal("missing inactive explanation")
	}
}

func TestRuntimeModesPreserveIndependentSourceWaits(t *testing.T) {
	for _, mode := range []string{"", "RECORDED", "NOTHINK", "MULTIPLY", "RANDOM"} {
		t.Run(mode, func(t *testing.T) {
			cfg := "[ThinkTime]\nOptions=" + mode + "\nFactor=2\nThinkTimeRandomLow=50\nThinkTimeRandomHigh=150\nLimitFlag=0"
			result := convertRuntimeContextFixture(t, cfg, "", `Action(){ lr_think_time(1); web_url("one", "URL=https://example.test/", LAST); lr_think_time(2); lr_think_time(3); }`)
			// The review editor and scenario overrides change only runtime config.
			// Recorded source waits must already be separable when this happens.
			result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{Type: "random_percentage", MinPercent: 100, MaxPercent: 100, LimitMs: 3000}
			var waits []float64
			for _, a := range result.Script.Actions {
				if a.ThinkTimeAfter {
					waits = append(waits, a.ThinkTime.MeanMs)
					if a.ThinkTime.Multiplier != 0 || a.ThinkTime.LimitMs != 0 || a.ThinkTime.Ignore {
						t.Fatalf("wait must inherit edited runtime policy: %#v", a.ThinkTime)
					}
				}
			}
			if fmt.Sprint(waits) != "[1000 2000 3000]" || result.Stats.ReviewOnlyFunctions != 0 {
				t.Fatalf("waits=%v stats=%#v", waits, result.Stats)
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("edited script validation: %v", issues)
			}
		})
	}
}

func TestGoldenPercentageRuntimeSettings(t *testing.T) {
	result, err := ConvertZip(zipTestDataDir(t, "testdata/golden_web_http_html_percentage"), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Script.Config
	if cfg.ThinkTime.Type != "random_percentage" || cfg.ThinkTime.MinPercent != 50 || cfg.ThinkTime.MaxPercent != 150 || cfg.ThinkTime.Ignore {
		t.Fatal(cfg.ThinkTime)
	}
	if cfg.Pacing.Type != "fixed_interval" || cfg.Pacing.IntervalMs != 5000 || cfg.HTTP.ConnectTimeoutMs != 500000 || cfg.HTTP.RequestTimeoutMs != 500000 {
		t.Fatal("transport/pacing regression")
	}
	if runtimeParityEntryByID(t, result.Script.Metadata.Import.Preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry), "think_time").Status != RuntimeSettingStatusExecuted {
		t.Fatal("percentage mode incorrectly marked partial")
	}
}

func TestPercentageParityDoesNotOverclaimExternalJavaRunner(t *testing.T) {
	sections := map[string]map[string]map[string]string{"default.cfg": {"ThinkTime": {"Options": "RANDOM", "ThinkTimeRandomLow": "50", "ThinkTimeRandomHigh": "150"}}}
	for _, entries := range [][]RuntimeSettingParityEntry{buildRTSParityReportForProtocol(sections, "java_vuser"), BuildRTSParityReportFromScript(&dsl.Script{Protocol: "java_vuser", Config: dsl.ScriptConfig{ThinkTime: dsl.ThinkTimeConfig{Type: "random_percentage", MinPercent: 50, MaxPercent: 150}}})} {
		if entry := runtimeParityEntryByID(t, entries, "think_time"); entry.Status != RuntimeSettingStatusCompatibilityOnly {
			t.Fatal(entry)
		}
	}
}
