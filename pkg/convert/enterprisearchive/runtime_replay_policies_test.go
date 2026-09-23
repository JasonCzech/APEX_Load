package enterprisearchive

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
)

func TestReplayPolicyExplicitOffAndClassification(t *testing.T) {
	text := "[General]\nFailTransOnErrorMsg=0\nAutomaticTransactions=0\n[WEB]\nHttpErrorsAsWarnings=0\nFailNonCriticalItem=0\nResourcePageTimeoutIsWarning=0\nEnableChecks=0\nEnableIPCache=0\nSimulateNewUser=1\nClearCacheForSimulateNewUser=0\nAutoTransFileLine=1\nLoadChilEngine=1\n"
	rc := ParseRuntimeCfg(text)
	cfg := rc.ToScriptConfig()
	for _, v := range []*bool{cfg.HTTP.HTTPErrorsAsWarnings, cfg.HTTP.FailNonCriticalResources, cfg.HTTP.ResourceTimeoutAsWarning, cfg.HTTP.LegacyHTMLChecks, cfg.HTTP.DNSCache, cfg.ErrorHandling.FailTransactionOnErrorMessage, cfg.BrowserEmulation.Cache.ClearCacheForNewUser} {
		if v == nil || *v {
			t.Fatal("explicit false lost")
		}
	}
	dst := &dsl.ScriptConfig{}
	mergeScriptConfig(dst, cfg)
	if dst.BrowserEmulation.Cache.ClearCacheForNewUser == nil {
		t.Fatal("merge lost cache policy")
	}
	sections := ParseConfigSections(text)
	rows := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{"default.cfg": sections}, "http")["default.cfg"]["WEB"]
	for _, key := range []string{"HttpErrorsAsWarnings", "FailNonCriticalItem", "ResourcePageTimeoutIsWarning", "EnableChecks", "EnableIPCache", "ClearCacheForSimulateNewUser"} {
		if rows[key].Status != RuntimeSettingDispositionMapped {
			t.Fatalf("%s=%+v", key, rows[key])
		}
	}
	if rows["LoadChilEngine"].Status != RuntimeSettingDispositionCompatibilityOnly || rows["AutoTransFileLine"].Status != RuntimeSettingDispositionNonApplicable {
		t.Fatalf("incorrect contextual classification: %v", rows)
	}
}
func TestReplayPolicyConflictAndUnverifiedValues(t *testing.T) {
	for _, tc := range []struct{ source, id string }{
		{"EnableChecks=invalid", "http_legacy_checks"}, {"EnableIPCache=1\nenableipcache=0", "http_dns_cache"},
		{"Retry401ThinkTime=5", "http_auth_delay"}, {"LogEnableResponseLimit=1\nLogMaxResponseSize=10", "runtime_response_log_limit"},
	} {
		rc := ParseRuntimeCfg("[WEB]\n" + tc.source)
		found := false
		for _, issue := range rc.RuntimeIssues {
			found = found || issue.ID == tc.id
		}
		if !found {
			t.Fatalf("missing review for %s: %+v", tc.source, rc.RuntimeIssues)
		}
	}
}

func TestLegacyCheckProvenanceAndProtocolScope(t *testing.T) {
	r := convertRuntimeContextFixture(t, "[WEB]\nEnableChecks=0", "", `Action(){web_url("page","URL=https://example.test/",LAST);web_find("check","What=missing",LAST);web_reg_find("Text=registered",LAST);web_url("next","URL=https://example.test/next",LAST);web_image_check("image","Src=https://example.test/image.png",LAST);}`)
	if !r.Script.Actions[0].Assertions[0].LegacyHTMLCheck || r.Script.Actions[1].Assertions[0].LegacyHTMLCheck || r.Script.Actions[2].Params["legacy_html_check"] != true {
		t.Fatal("check provenance lost")
	}
	for _, proto := range []string{"http", "soap", "java_http", "browser", "java_vuser"} {
		entries := buildRTSParityReportForProtocol(map[string]map[string]map[string]string{"default.cfg": {"WEB": {"EnableChecks": "0"}}}, proto)
		entry := runtimeParityEntryByID(t, entries, "http_legacy_checks")
		if (entry.Status == RuntimeSettingStatusExecuted) != nativeHTTPCompatibilityProtocol(proto) {
			t.Fatalf("protocol %s: %+v", proto, entry)
		}
	}
}
func TestUnknownFalseSettingRequiresReview(t *testing.T) {
	rows := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{"default.cfg": {"WEB": {"UnknownBoolean": "0"}}}, "http")
	if rows["default.cfg"]["WEB"]["UnknownBoolean"].Status != RuntimeSettingDispositionCompatibilityOnly {
		t.Fatal("unproven zero classified as inactive")
	}
}

func TestImageChecksDoNotConsumeNextRequestRegistrations(t *testing.T) {
	r := convertRuntimeContextFixture(t, "[WEB]\nEnableChecks=0", "", `Action(){web_url("page","URL=https://example.test/",LAST);web_reg_find("Text=registered",LAST);web_add_header("X-Once","value");web_image_check("image","Src=https://example.test/image.png",LAST);web_find("legacy","What=previous page",LAST);web_url("next","URL=https://example.test/next",LAST);}`)
	if len(r.Script.Actions) != 3 || len(r.Script.Actions[1].Assertions) != 0 || r.Script.Actions[1].Headers["X-Once"] != "" {
		t.Fatal("image check consumed next-request registrations")
	}
	if len(r.Script.Actions[0].Assertions) != 1 || !r.Script.Actions[0].Assertions[0].LegacyHTMLCheck {
		t.Fatal("inline check lost preceding page")
	}
	if len(r.Script.Actions[2].Assertions) != 1 || r.Script.Actions[2].Assertions[0].LegacyHTMLCheck || r.Script.Actions[2].Headers["X-Once"] != "value" {
		t.Fatal("registered check/header lost")
	}
}
