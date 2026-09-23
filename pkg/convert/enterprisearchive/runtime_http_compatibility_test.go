package enterprisearchive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestHTTPCompatibilityImport(t *testing.T) {
	for _, tc := range []struct {
		settings, id string
		status       RuntimeSettingStatus
		check        func(dsl.HTTPConfig) bool
	}{
		{"PageDownloadTimeout=500", "http_page_timeout", RuntimeSettingStatusExecuted, func(c dsl.HTTPConfig) bool { return c.PageTimeoutMs == 500000 }},
		{"PageDownloadTimeout=3601", "http_page_timeout", RuntimeSettingStatusPartial, func(c dsl.HTTPConfig) bool { return c.PageTimeoutMs == 0 }},
		{"PageDownloadTimeout=0", "http_page_timeout", RuntimeSettingStatusPartial, func(c dsl.HTTPConfig) bool { return c.PageTimeoutMs == 0 }},
		{"PageDownloadTimeout=1\npagedownloadtimeout=2", "http_page_timeout", RuntimeSettingStatusPartial, func(c dsl.HTTPConfig) bool { return c.PageTimeoutMs == 0 }},
		{"IPVersionPolicy=IPv6", "http_ip_version", RuntimeSettingStatusExecuted, func(c dsl.HTTPConfig) bool { return c.IPVersion == "ipv6" }},
		{"IPVersionPolicy=2", "http_ip_version", RuntimeSettingStatusPartial, func(c dsl.HTTPConfig) bool { return c.IPVersion == "" }},
		{"IPVersionPolicy=IPv6\nipversionpolicy=IPv4", "http_ip_version", RuntimeSettingStatusPartial, func(c dsl.HTTPConfig) bool { return c.IPVersion == "" }},
		{"DeleteCacheUnreferencedIterations=1", "http_cache_eviction", RuntimeSettingStatusExecuted, func(c dsl.HTTPConfig) bool { return c.CacheMaxUnusedIterations == 1 }},
		{"DeleteCacheUnreferencedIterations=-1", "http_cache_eviction", RuntimeSettingStatusPartial, func(c dsl.HTTPConfig) bool { return c.CacheMaxUnusedIterations == 0 }},
		{"SimulateCache=0\nDeleteCacheUnreferencedIterations=1", "http_cache_eviction", RuntimeSettingStatusNonApplicable, func(c dsl.HTTPConfig) bool { return c.CacheMaxUnusedIterations == 1 }},
		{"ParseHtmlContentType=TEXT", "http_html_parse", RuntimeSettingStatusExecuted, func(c dsl.HTTPConfig) bool { return c.HTMLParseMode == "text" }},
		{"ParseHtmlContentType=unknown", "http_html_parse", RuntimeSettingStatusPartial, func(c dsl.HTTPConfig) bool { return c.HTMLParseMode == "" }},
	} {
		t.Run(tc.settings, func(t *testing.T) {
			r := convertRuntimeContextFixture(t, "[WEB]\n"+tc.settings, "", `Action(){web_url("page","URL=https://example.test/",LAST);}`)
			if !tc.check(r.Script.Config.HTTP) {
				t.Fatalf("HTTP=%+v", r.Script.Config.HTTP)
			}
			entry := runtimeParityEntryByID(t, r.Script.Metadata.Import.Preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry), tc.id)
			if entry.Status != tc.status {
				t.Fatalf("entry=%+v", entry)
			}
			if issues := dsl.ValidateScriptIssues(r.Script); len(issues) > 0 {
				t.Fatal(issues)
			}
		})
	}
	for _, protocol := range []string{"http", "soap", "java_http", "browser", "java_vuser"} {
		entries := buildRTSParityReportForProtocol(map[string]map[string]map[string]string{"default.cfg": {"WEB": {"PageDownloadTimeout": "500"}}}, protocol)
		entry := runtimeParityEntryByID(t, entries, "http_page_timeout")
		if (entry.Status == RuntimeSettingStatusExecuted) != nativeHTTPCompatibilityProtocol(protocol) {
			t.Fatalf("%s: %+v", protocol, entry)
		}
	}
}

func TestVerifiedSequentialProfileRequiresExactSourceTree(t *testing.T) {
	dir := "testdata/golden_web_http_html_vugen24"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files[entry.Name()] = string(b)
		}
	}
	original := files["default.usp"]
	if original == "" {
		t.Fatal("missing golden USP")
	}
	for _, tc := range []struct {
		name   string
		change func(string) string
		want   bool
	}{
		{"standard", func(s string) string { return s }, true},
		{"random", func(s string) string { return strings.ReplaceAll(s, "Sequential", "Random") }, false},
		{"leaf iterations", func(s string) string {
			return strings.Replace(s, "[RunLogicRunRoot:Action]", "[RunLogicRunRoot:Action]\nRunLogicNumOfIterations=2", 1)
		}, false},
		{"unknown group control", func(s string) string {
			return strings.Replace(s, "[RunLogicRunRoot]", "[RunLogicRunRoot]\nRunLogicCustomControl=1", 1)
		}, false},
		{"extra child", func(s string) string { return s + "\n[RunLogicRunRoot:Other]\nRunLogicObjectKind=Action\n" }, false},
		{"nested action", func(s string) string {
			return strings.ReplaceAll(s, `RunLogicObjectKind="Action"`, `RunLogicObjectKind="Group"`)
		}, false},
		{"init repeated", func(s string) string {
			return strings.Replace(s, `RunLogicNumOfIterations="1"`, `RunLogicNumOfIterations="2"`, 1)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files["default.usp"] = tc.change(original)
			rc := &RuntimeConfig{BlockScheduling: "sequential", ActionSequence: []string{"vuser_init", "action", "vuser_end"}, selectedRunLogicFile: "default.usp"}
			if got := verifiedNativeSequentialProfile(rc, nil, files, "http"); got != tc.want {
				t.Fatalf("verified=%v want %v", got, tc.want)
			}
		})
	}
}

func TestSequentialProofDoesNotSurviveChangedConfiguration(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{BlockScheduling: "sequential", ActionSequence: []string{"vuser_init", "action", "vuser_end"}}}, Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"native_sequential_run_logic": "vuser_init,action,vuser_end"}}}}
	check := func(want RuntimeSettingStatus) {
		t.Helper()
		for _, id := range []string{"run_logic_action_sequence", "run_logic_block_scheduling"} {
			entry := runtimeParityEntryByID(t, BuildRTSParityReportFromScript(script), id)
			if entry.Status != want {
				t.Fatalf("%+v", entry)
			}
		}
	}
	check(RuntimeSettingStatusExecuted)
	script.Config.RunLogic.ActionSequence = []string{"action", "vuser_init", "vuser_end"}
	check(RuntimeSettingStatusCompatibilityOnly)
	script.Config.RunLogic.ActionSequence = []string{"vuser_init", "action", "vuser_end"}
	script.Protocol = "java_vuser"
	check(RuntimeSettingStatusCompatibilityOnly)
}
