package enterprisearchive

import (
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"strings"
	"testing"
)

func TestWebPositionalParameterAssignmentsAreDependencyProducers(t *testing.T) {
	archive := buildSourceZip(t, map[string]string{
		"Script.usr": "[General]\nType=Web\n[Actions]\nAction=Action.c\n",
		"Action.c": `Action() {
 lr_save_timestamp("Tstamp", LAST);
 web_reg_save_param("IDs", "LB=id=", "RB=;", "ORD=ALL", LAST);
 web_url("List", "URL=https://example.test/{Tstamp}", LAST);
 lr_save_string(lr_paramarr_random("IDs"), "Picked");
 web_url("Detail", "URL=https://example.test/{Picked}", LAST);
 return 0;
}`,
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := importreview.AnalyzeDependencies(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if strings.HasPrefix(finding.Code, "missing_producer:") {
			t.Fatalf("false missing producer: %#v", finding)
		}
	}
	var timestamp, random bool
	for _, action := range result.Script.Actions {
		if action.Type != "source_parameter" {
			continue
		}
		switch action.Params["function"] {
		case "lr_save_timestamp":
			timestamp = action.Params["save_param"] == "Tstamp"
		case "lr_save_string":
			array, _ := action.Params["source_array"].(map[string]any)
			random = action.Params["save_param"] == "Picked" && array["name"] == "ids" && array["selection"] == "random" && action.Params["source_string"] == "{{var.ids_count}}"
		}
	}
	if !timestamp || !random {
		t.Fatalf("assignment metadata missing: timestamp=%v random=%v", timestamp, random)
	}
	if missing := requestimport.MissingVariables(result.Script, nil); len(missing) != 0 {
		t.Fatalf("API readiness lost executable parameter assignments: %v", missing)
	}
}

func TestWebAssignmentDependencyReadsStillFailWhenProducerIsAbsent(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action() { lr_save_string(lr_paramarr_random("Absent"),"Picked"); web_url("Detail", "URL=https://example.test/{Picked}", LAST); }`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := importreview.AnalyzeDependencies(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range findings {
		if finding.Code == "missing_producer:absent_count" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing input array did not gate replay: %#v", findings)
	}
	if missing := requestimport.MissingVariables(result.Script, nil); len(missing) != 1 || missing[0] != "absent_count" {
		t.Fatalf("API readiness did not retain the real array input dependency: %v", missing)
	}
}

func TestSourceRandomParameterArrayRequiresOneLiteralName(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`lr_paramarr_random("IDs")`, "ids"},
		{` lr_paramarr_random ( "IDs" ) `, "ids"},
		{`lr_paramarr_random("I" "Ds")`, "ids"},
		{"", ""},
		{`lr_paramarr_random()`, ""},
		{`lr_paramarr_random(variable)`, ""},
		{`lr_paramarr_random("IDs", 2)`, ""},
		{`lr_paramarr_random("")`, ""},
		{`lr_paramarr_random("IDs") + other()`, ""},
		{`lr_paramarr_random("IDs" ? "Other" : "Fallback")`, ""},
	} {
		got, ok := sourceRandomParameterArray(tc.input)
		if got != tc.want || ok != (tc.want != "") {
			t.Fatalf("sourceRandomParameterArray(%q) = %q, %v; want %q", tc.input, got, ok, tc.want)
		}
	}
}

func TestUnsupportedRandomArrayAssignmentRemainsSourceReview(t *testing.T) {
	for _, expression := range []string{
		`lr_paramarr_random("IDs" ? "Other" : "Fallback")`,
		`lr_paramarr_random("IDs") + other()`,
		`lr_paramarr_random(variable)`,
	} {
		parsed, err := ParseActionFile("Action.c", `Action() { lr_save_string(`+expression+`, "Picked"); }`)
		if err != nil {
			t.Fatal(err)
		}
		result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, action := range result.Script.Actions {
			if action.Params["function"] == "lr_save_string" {
				found = true
				if action.Type != "source_review" || action.Metadata["execution_blocker"] == nil || action.Params["source_array"] != nil {
					t.Fatalf("unsupported C array expression became executable: type=%q", action.Type)
				}
			}
		}
		if !found || result.Stats.ReviewOnlyFunctions != 1 {
			t.Fatalf("array assignment was lost or falsely counted as native: found=%v review=%d", found, result.Stats.ReviewOnlyFunctions)
		}
	}
	parsed, err := ParseActionFile("Action.c", `Action() { lr_save_string("lr_paramarr_random(variable)", "Literal"); lr_save_string("lr_paramarr_random(\"IDs\")", "QuotedLiteral"); lr_save_string(lr_paramarr_random("I" "Ds"), "Picked"); }`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil || result.Stats.ReviewOnlyFunctions != 0 {
		t.Fatalf("literal text or adjacent literals acquired a false review gate: err=%v", err)
	}
	for _, action := range result.Script.Actions {
		switch action.Params["save_param"] {
		case "Literal", "QuotedLiteral":
			if action.Params["source_array"] != nil || action.Params["source_string"] == "{{var.variable}}" {
				t.Fatal("authored function-looking literal became an executable array read")
			}
			args, _ := action.Params["args"].([]any)
			if len(args) == 0 || args[0] != action.Params["source_string"] {
				t.Fatal("authored literal readiness and runtime values disagree")
			}
			if action.Params["save_param"] == "QuotedLiteral" && args[0] != `lr_paramarr_random("IDs")` {
				t.Fatal("quoted function-looking literal lost its exact decoded source bytes")
			}
		case "Picked":
			array, _ := action.Params["source_array"].(map[string]any)
			if array["name"] != "ids" {
				t.Fatal("adjacent C literals lost their exact array name")
			}
		}
	}
}

func TestWebDefaultProxyParentAndCustomSelectorAreDistinct(t *testing.T) {
	for _, selector := range []string{"ProxyUseBrowser=1", "ProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://example.test/proxy.pac"} {
		rc := ParseRuntimeCfg("[WEB]\nProxyUseProxy=1\nProxyUseProxyServer=0\n" + selector)
		want := "system"
		if strings.Contains(selector, "AutoConfig") {
			want = "pac"
		}
		if rc.ProxyMode != want {
			t.Fatalf("proxy=%q want %q: %#v", rc.ProxyMode, want, rc.RuntimeIssues)
		}
		for _, issue := range rc.RuntimeIssues {
			if strings.Contains(issue.Notes, "Conflicting") {
				t.Fatalf("parent and custom mode falsely conflict: %#v", issue)
			}
		}
	}
}

func TestWebDisabledCustomProxyKeepsConflictingSavedHostsDormant(t *testing.T) {
	const hosts = "ProxyHTTPHost=a.example.test\nproxyhttphost=b.example.test\nProxyHTTPPort=3128\n"
	for _, tc := range []struct {
		name, selectors string
		disabled        bool
	}{
		{"custom disabled", "ProxyUseProxyServer=0\n", true},
		{"parent disabled", "ProxyUseProxy=0\n", true},
		{"both disabled", "ProxyUseProxy=0\nProxyUseProxyServer=0\n", true},
		{"malformed custom", "ProxyUseProxyServer=unknown\n", false},
		{"conflicting custom", "ProxyUseProxyServer=0\nproxyuseproxyserver=1\n", false},
		{"parent contradicts custom", "ProxyUseProxy=1\nProxyUseProxyServer=0\n", false},
		{"parent contradicts browser", "ProxyUseProxy=0\nProxyUseBrowser=1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := "[WEB]\n" + tc.selectors + hosts
			if got := runtimeCfgProxyExplicitlyDisabled(ParseConfigSections(cfg)); got != tc.disabled {
				t.Fatalf("explicitly disabled=%v want=%v", got, tc.disabled)
			}
			rc := ParseRuntimeCfg(cfg)
			if tc.disabled {
				if rc.ProxyMode != "none" || len(rc.RuntimeIssues) != 0 {
					t.Fatalf("dormant host conflicts changed disabled selection: mode=%q issues=%v", rc.ProxyMode, rc.RuntimeIssues)
				}
			} else if len(rc.RuntimeIssues) == 0 {
				t.Fatal("ambiguous or malformed selector lost its review issue")
			}
			if tc.name == "parent contradicts custom" {
				for _, issue := range rc.RuntimeIssues {
					if issue.sourceKey == "proxyhttphost" {
						t.Fatal("inactive custom host conflict obscured the active parent-selector migration issue")
					}
				}
			}
		})
	}
}

func TestWebPortableProxySelectionsKeepOnlyCustomEndpointsDormant(t *testing.T) {
	for _, tc := range []struct{ selector, mode string }{
		{"ProxyUseBrowser=1", "system"},
		{"ProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://example.test/proxy.pac", "pac"},
	} {
		rc := ParseRuntimeCfg("[WEB]\nProxyUseProxy=1\nProxyUseProxyServer=0\n" + tc.selector + "\nProxyHTTPHost=a.example.test\nproxyhttphost=b.example.test\nProxyHTTPPort=3128\n")
		if rc.ProxyMode != tc.mode {
			t.Fatalf("portable proxy mode=%q want=%q", rc.ProxyMode, tc.mode)
		}
		for _, issue := range rc.RuntimeIssues {
			if issue.sourceKey == "proxyhttphost" {
				t.Fatal("portable proxy activated a dormant custom host conflict")
			}
		}
		if tc.mode == "system" && len(rc.RuntimeIssues) == 0 {
			t.Fatal("Windows browser proxy migration lost its review requirement")
		}
	}
}

func TestWebRecordedScreenDimensionsDoNotClaimBrowserExecution(t *testing.T) {
	for _, tc := range []struct {
		name, config, protocol string
		status                 RuntimeSettingStatus
	}{
		{"http screen", "[WEB]\nScreenWidth=1920\nScreenHeight=1080", "http", RuntimeSettingStatusNonApplicable},
		{"http explicit device", "[Browser]\nDeviceProfile=pixel_7\nViewportWidth=412", "http", RuntimeSettingStatusCompatibilityOnly},
		{"browser explicit device", "[Browser]\nDeviceProfile=pixel_7\nViewportWidth=412", "browser", RuntimeSettingStatusExecuted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"default.cfg": tc.config}
			parity := buildRTSParityReportForProtocol(collectRuntimeSections(files), tc.protocol)
			row := runtimeParityEntryByID(t, parity, "browser_device_emulation")
			if row.Status != tc.status {
				t.Fatalf("status=%q want %q", row.Status, tc.status)
			}
			dispositions := classifyRuntimeSelectedSections(collectRuntimeSections(files), tc.protocol)
			refineRuntimeSettingDispositions(dispositions, ParseRuntimeCfg(tc.config), files, tc.protocol)
			for _, keys := range dispositions["default.cfg"] {
				for _, d := range keys {
					if d.ParityID != "browser_device_emulation" {
						continue
					}
					want := RuntimeSettingDispositionCompatibilityOnly
					if tc.status == RuntimeSettingStatusNonApplicable {
						want = RuntimeSettingDispositionNonApplicable
					}
					if tc.status == RuntimeSettingStatusExecuted {
						want = RuntimeSettingDispositionMapped
					}
					if d.Status != want {
						t.Fatalf("disposition=%#v want %q", d, want)
					}
				}
			}
		})
	}
}

func TestWebTimeParameterPreservesLiteralMillisecondsAndExplicitTimezone(t *testing.T) {
	for _, tc := range []struct{ extra, zone string }{{"", ""}, {"Timezone=UTC\n", "UTC"}} {
		parsed := ParseParameterFile("Script.prm", "[parameter:Clock]\nType=Time\nFormat=%Y-%m-%dT%H:%M:%S.000Z\nGenerateNewVal=EachOccurrence\nOffset=\nOffsetUI=0\nWorkingDaysOn=0\n"+tc.extra, nil)
		if len(parsed.Parameters) != 1 {
			t.Fatal(parsed)
		}
		p := parsed.Parameters[0]
		if p.Format != "%Y-%m-%dT%H:%M:%S.000Z" || p.FormatDialect != "loadrunner_strftime" || p.Timezone != tc.zone || p.Update != "each_occurrence" || p.OffsetSeconds != 0 {
			t.Fatalf("time parameter = %#v", p)
		}
	}
}

func TestWebCustomUserAgentMakesSavedBrowserIdentityDormant(t *testing.T) {
	files := map[string]string{"default.cfg": "[WEB]\nCustomUserAgent=Custom-UA\nUseCustomAgent=1\nBrowserType=Microsoft Internet Explorer 4.0\nUseBrowserAgent=0\n"}
	rc := ParseRuntimeCfg(files["default.cfg"])
	dispositions := classifyRuntimeSelectedSections(collectRuntimeSections(files), "http")
	refineRuntimeSettingDispositions(dispositions, rc, files, "http")
	for _, key := range []string{"BrowserType", "UseBrowserAgent"} {
		if row := dispositions["default.cfg"]["WEB"][key]; row.Status != RuntimeSettingDispositionNonApplicable {
			t.Fatalf("dormant %s = %#v", key, row)
		}
	}
}
