package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func zipFilesForTest(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestParseRuntimeCfgBrowserEmulationRealLRKeys(t *testing.T) {
	rc := ParseRuntimeCfg(`[WEB]
BrowserType=Microsoft Internet Explorer 4.0
BrowserPlatform=Windows
BrowserLanguageCode=en-GB
SimulateCache=Yes
CacheHtmlPages=1
CacheAlwaysCheckForNewerPages=0
SimulateNewUser=No
ClearCacheForSimulateNewUser=1
SearchForImages=Yes
SimulatePrefetchPrerender=1
`)

	if rc.BEBrowserType != "ie" || rc.BEBrowserTypeRaw != "Microsoft Internet Explorer 4.0" {
		t.Fatalf("browser type = %q (raw %q), want ie", rc.BEBrowserType, rc.BEBrowserTypeRaw)
	}
	if rc.BEBrowserVersion != "4.0" {
		t.Fatalf("browser version = %q, want embedded 4.0", rc.BEBrowserVersion)
	}
	if rc.BEPlatform != "windows" || rc.BELanguage != "en-GB" {
		t.Fatalf("platform/language = %q/%q, want windows/en-GB", rc.BEPlatform, rc.BELanguage)
	}
	assertBoolPtr := func(name string, got *bool, want bool) {
		t.Helper()
		if got == nil || *got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertBoolPtr("SimulateCache", rc.BESimulateCache, true)
	assertBoolPtr("CacheHtmlPages", rc.BECacheContentURLs, true)
	assertBoolPtr("CacheAlwaysCheckForNewerPages", rc.BECheckNewerVersions, false)
	assertBoolPtr("SimulateNewUser", rc.BENewUserEachIteration, false)
	assertBoolPtr("SearchForImages", rc.BEDownloadNonHTML, true)
	assertBoolPtr("SimulatePrefetchPrerender", rc.BESimulatePrefetch, true)
	if rc.BEClearCacheEachIterationRaw != "1" {
		t.Fatalf("clear-cache raw = %q, want preserved 1", rc.BEClearCacheEachIterationRaw)
	}

	emu := rc.ToBrowserEmulationConfig()
	if emu == nil || emu.Mode != "specific" || emu.BrowserType != "ie" || emu.BrowserVersion != "4.0" || emu.Platform != "windows" {
		t.Fatalf("emulation = %#v, want specific ie 4.0 windows", emu)
	}
	// SimulateNewUser=No: ClearCacheForSimulateNewUser alone must not enable
	// per-iteration clearing (stock cfgs carry it unconditionally).
	if emu.Cache == nil || emu.Cache.NewUserEachIteration == nil || *emu.Cache.NewUserEachIteration {
		t.Fatalf("cache = %#v, want new_user_each_iteration=false", emu.Cache)
	}
}

func TestParseRuntimeCfgBrowserEmulationInventedAliasesStillWork(t *testing.T) {
	rc := ParseRuntimeCfg(`[Browser Emulation]
UserAgent=APEXAgent/1.0
CacheEnabled=1
CacheContentURLs=1
CheckNewerVersions=1
NewUserPerIteration=1
`)
	emu := rc.ToBrowserEmulationConfig()
	if emu == nil || emu.Mode != "custom" || emu.CustomUserAgent != "APEXAgent/1.0" {
		t.Fatalf("emulation = %#v, want custom APEXAgent/1.0 (legacy UA import behavior)", emu)
	}
	for name, ptr := range map[string]*bool{
		"simulate_cache":          emu.Cache.SimulateCache,
		"cache_content_urls":      emu.Cache.CacheContentURLs,
		"check_newer_versions":    emu.Cache.CheckNewerVersions,
		"new_user_each_iteration": emu.Cache.NewUserEachIteration,
	} {
		if ptr == nil || !*ptr {
			t.Fatalf("%s = %v, want true", name, ptr)
		}
	}
}

func TestParseRuntimeCfgBrowserEmulationUseCustomAgentSuppression(t *testing.T) {
	rc := ParseRuntimeCfg(`[WEB]
UseCustomAgent=0
CustomUserAgent=Ignored/1.0
BrowserType=Google Chrome
BrowserVer=120
`)
	emu := rc.ToBrowserEmulationConfig()
	if emu == nil || emu.Mode != "specific" || emu.BrowserType != "chrome" || emu.BrowserVersion != "120" {
		t.Fatalf("emulation = %#v, want specific chrome 120 (UseCustomAgent=0 suppresses custom)", emu)
	}
	if emu.CustomUserAgent != "" {
		t.Fatalf("custom UA = %q, want empty", emu.CustomUserAgent)
	}

	httpCfg := rc.ToHTTPConfig()
	if httpCfg.UserAgent != "" {
		t.Fatalf("http.user_agent = %q, want empty (no double-set when emulation mode resolves)", httpCfg.UserAgent)
	}
}

func TestParseRuntimeCfgBrowserEmulationInvalidBoolsPreserveRaw(t *testing.T) {
	rc := ParseRuntimeCfg(`[WEB]
SimulateCache=maybe
SearchForImages=sometimes
BrowserType=NetPositive
`)
	if rc.BESimulateCache != nil || rc.BESimulateCacheParseError == "" || rc.BESimulateCacheRaw != "maybe" {
		t.Fatalf("simulate cache = %v err=%q raw=%q, want unset with parse error", rc.BESimulateCache, rc.BESimulateCacheParseError, rc.BESimulateCacheRaw)
	}
	if rc.BEDownloadNonHTML != nil || rc.BEDownloadNonHTMLParseError == "" {
		t.Fatalf("download non-HTML = %v err=%q, want unset with parse error", rc.BEDownloadNonHTML, rc.BEDownloadNonHTMLParseError)
	}
	if rc.BEBrowserType != "" || rc.BEBrowserTypeRaw != "NetPositive" {
		t.Fatalf("browser type = %q raw=%q, want unmapped with raw preserved", rc.BEBrowserType, rc.BEBrowserTypeRaw)
	}
	if emu := rc.ToBrowserEmulationConfig(); emu != nil && emu.Mode != "" {
		t.Fatalf("emulation mode = %q, want none for unmapped browser", emu.Mode)
	}
}

func TestParseRuntimeCfgBrowserEmulationDownloadResourcesEnumUnaffected(t *testing.T) {
	rc := ParseRuntimeCfg(`[Browser Emulation]
DownloadNonHtmlResources=recorded
`)
	if rc.DownloadResources != "recorded" {
		t.Fatalf("download resources enum = %q, want recorded", rc.DownloadResources)
	}
	if rc.BEDownloadNonHTML != nil || rc.BEDownloadNonHTMLParseError != "" {
		t.Fatalf("bool form = %v err=%q, want untouched for enum value", rc.BEDownloadNonHTML, rc.BEDownloadNonHTMLParseError)
	}
}

func TestImportSanitizesValidationBlockingBrowserEmulation(t *testing.T) {
	// Stock VuGen emits cache child keys unconditionally; an unchecked "Simulate
	// browser cache" arrives as SimulateCache=0 alongside CacheHtmlPages=1 etc.
	t.Run("cache-off drops contradictory children", func(t *testing.T) {
		rc := ParseRuntimeCfg("[WEB]\nSimulateCache=0\nCacheHtmlPages=1\nCacheAlwaysCheckForNewerPages=1\n")
		script := browserEmulationScriptFromRC(t, rc)
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("validation issues after import: %v", issues)
		}
		cache := script.Config.BrowserEmulation.Cache
		if cache.SimulateCache == nil || *cache.SimulateCache {
			t.Fatalf("simulate_cache = %v, want false", cache.SimulateCache)
		}
		if cache.CacheContentURLs != nil || cache.CheckNewerVersions != nil {
			t.Fatalf("contradictory children survived: content=%v newer=%v", cache.CacheContentURLs, cache.CheckNewerVersions)
		}
		// Raw source values are still preserved for review.
		if rc.BECacheContentURLsRaw != "1" || rc.BECheckNewerVersionsRaw != "1" {
			t.Fatalf("raw values not preserved: %q %q", rc.BECacheContentURLsRaw, rc.BECheckNewerVersionsRaw)
		}
	})

	t.Run("incompatible platform dropped", func(t *testing.T) {
		rc := ParseRuntimeCfg("[WEB]\nBrowserType=Apple Safari\nBrowserPlatform=Windows\n")
		script := browserEmulationScriptFromRC(t, rc)
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("validation issues after import: %v", issues)
		}
		emu := script.Config.BrowserEmulation
		if emu.BrowserType != "safari" || emu.Platform != "" {
			t.Fatalf("emulation = %#v, want safari with dropped platform", emu)
		}
		if rc.BEPlatformRaw != "Windows" {
			t.Fatalf("raw platform not preserved: %q", rc.BEPlatformRaw)
		}
	})

	t.Run("non-numeric version dropped", func(t *testing.T) {
		rc := ParseRuntimeCfg("[WEB]\nBrowserType=Google Chrome\nBrowserVer=latest\n")
		script := browserEmulationScriptFromRC(t, rc)
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("validation issues after import: %v", issues)
		}
		if script.Config.BrowserEmulation.BrowserVersion != "" {
			t.Fatalf("browser_version = %q, want dropped", script.Config.BrowserEmulation.BrowserVersion)
		}
	})
}

func TestImportBrowserEmulationSectionSourcing(t *testing.T) {
	// F2: a [WEB] custom UA must survive even when an APEX-native [Browser
	// Emulation] section is present (the UA shares the toggles' section fallback).
	t.Run("WEB custom UA not lost under native section", func(t *testing.T) {
		rc := ParseRuntimeCfg("[Browser Emulation]\nLanguage=en-US\n\n[WEB]\nUseCustomAgent=1\nCustomUserAgent=RealCustom/2.0\nBrowserType=Google Chrome\n")
		emu := rc.ToBrowserEmulationConfig()
		if emu == nil || emu.Mode != "custom" || emu.CustomUserAgent != "RealCustom/2.0" {
			t.Fatalf("emulation = %#v, want custom RealCustom/2.0", emu)
		}
	})

	// F3: real SimulateCache=0 wins over an invented [Browser] CacheEnabled=1.
	t.Run("real SimulateCache wins over CacheEnabled", func(t *testing.T) {
		rc := ParseRuntimeCfg("[Browser]\nCacheEnabled=1\n\n[WEB]\nSimulateCache=0\n")
		if rc.BESimulateCache == nil || *rc.BESimulateCache {
			t.Fatalf("simulate_cache = %v, want false (real key wins)", rc.BESimulateCache)
		}
	})

	// F4: UseBrowserAgent must not suppress a custom UA (it is not a custom-UA toggle).
	t.Run("UseBrowserAgent does not suppress custom UA", func(t *testing.T) {
		rc := ParseRuntimeCfg("[WEB]\nUseBrowserAgent=0\nCustomUserAgent=MyAgent/1.0\nBrowserType=Google Chrome\n")
		if rc.BEUseCustomUA != nil {
			t.Fatalf("BEUseCustomUA = %v, want nil (UseBrowserAgent ignored as toggle)", rc.BEUseCustomUA)
		}
		emu := rc.ToBrowserEmulationConfig()
		if emu == nil || emu.Mode != "custom" || emu.CustomUserAgent != "MyAgent/1.0" {
			t.Fatalf("emulation = %#v, want custom MyAgent/1.0", emu)
		}
	})
}

func browserEmulationScriptFromRC(t *testing.T, rc *RuntimeConfig) *dsl.Script {
	t.Helper()
	cfg := rc.ToScriptConfig()
	return &dsl.Script{
		Name:     "import-probe",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test", BrowserEmulation: cfg.BrowserEmulation},
		Actions:  []dsl.Action{{Name: "GET /", Type: "http_request", Method: "GET", Target: "/"}},
	}
}

func TestMapSourceBrowserTypeTable(t *testing.T) {
	tests := []struct {
		raw         string
		wantType    string
		wantVersion string
	}{
		{"Microsoft Internet Explorer 4.0", "ie", "4.0"},
		{"Microsoft Internet Explorer", "ie", ""},
		{"MSIE", "ie", ""},
		{"Google Chrome 99", "chrome", "99"},
		{"Chrome", "chrome", ""},
		{"Chromium", "chrome", ""},
		{"Mozilla Firefox", "firefox", ""},
		{"Microsoft Edge", "edge", ""},
		{"Apple Safari 17", "safari", "17"},
		{"NetPositive", "", ""},
	}
	for _, tt := range tests {
		gotType, gotVersion := mapSourceBrowserType(tt.raw)
		if gotType != tt.wantType || gotVersion != tt.wantVersion {
			t.Errorf("mapSourceBrowserType(%q) = (%q, %q), want (%q, %q)", tt.raw, gotType, gotVersion, tt.wantType, tt.wantVersion)
		}
	}
}

func TestMergeScriptConfigDeepClonesBrowserEmulation(t *testing.T) {
	on := true
	src := &dsl.ScriptConfig{BrowserEmulation: &dsl.BrowserEmulationConfig{
		Mode:        "specific",
		BrowserType: "chrome",
		Cache:       &dsl.BrowserCacheSimulationConfig{SimulateCache: &on},
	}}
	dst := &dsl.ScriptConfig{}
	mergeScriptConfig(dst, src)

	if dst.BrowserEmulation == nil || dst.BrowserEmulation.BrowserType != "chrome" {
		t.Fatalf("merged = %#v, want chrome emulation", dst.BrowserEmulation)
	}
	*src.BrowserEmulation.Cache.SimulateCache = false
	src.BrowserEmulation.BrowserType = "edge"
	if !*dst.BrowserEmulation.Cache.SimulateCache || dst.BrowserEmulation.BrowserType != "chrome" {
		t.Fatalf("merge is not a deep clone: %#v", dst.BrowserEmulation)
	}
}

func TestConvertZipRealLRBrowserEmulationFixture(t *testing.T) {
	zipBytes := zipTestDataDir(t, "testdata/golden_web_http_html_real_rts")

	result, err := ConvertZip(zipBytes, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil {
		t.Fatal("no script converted")
	}

	emu := result.Script.Config.BrowserEmulation
	if emu == nil {
		t.Fatal("browser_emulation not populated from real LR [WEB] keys")
	}
	if emu.Mode != "specific" || emu.BrowserType != "chrome" || emu.BrowserVersion != "120.0" || emu.Platform != "windows" {
		t.Fatalf("emulation = %#v, want specific chrome 120.0 windows", emu)
	}
	if emu.Language != "de-DE" {
		t.Fatalf("language = %q, want de-DE", emu.Language)
	}
	if emu.CustomUserAgent != "" {
		t.Fatalf("custom UA = %q, want empty (UseCustomAgent=0)", emu.CustomUserAgent)
	}
	if result.Script.Config.HTTP.UserAgent != "" {
		t.Fatalf("http.user_agent = %q, want empty", result.Script.Config.HTTP.UserAgent)
	}
	cache := emu.Cache
	if cache == nil {
		t.Fatal("cache simulation not populated")
	}
	for name, want := range map[string]struct {
		ptr  *bool
		want bool
	}{
		"simulate_cache":              {cache.SimulateCache, true},
		"cache_content_urls":          {cache.CacheContentURLs, true},
		"check_newer_versions":        {cache.CheckNewerVersions, true},
		"new_user_each_iteration":     {cache.NewUserEachIteration, true},
		"download_non_html_resources": {cache.DownloadNonHTML, true},
		"simulate_prefetch":           {cache.SimulatePrefetch, true},
	} {
		if want.ptr == nil || *want.ptr != want.want {
			t.Fatalf("%s = %v, want %v", name, want.ptr, want.want)
		}
	}
	// SimulateNewUser also keeps the legacy cookie-clear mapping.
	if result.Script.Config.HTTP.ClearCookiesPerIter == nil || !*result.Script.Config.HTTP.ClearCookiesPerIter {
		t.Fatalf("clear_cookies_per_iteration = %v, want true", result.Script.Config.HTTP.ClearCookiesPerIter)
	}

	preserved := result.Script.Metadata.Import.Preserved
	beSettings, ok := preserved["browser_emulation_settings"].(map[string]any)
	if !ok {
		t.Fatalf("browser_emulation_settings = %#v, want preserved map", preserved["browser_emulation_settings"])
	}
	if beSettings["browser_type_raw"] != "Google Chrome" || beSettings["clear_cache_each_iteration_raw"] != "1" {
		t.Fatalf("preserved = %#v, want raw browser type and clear-cache child key", beSettings)
	}

	parity, ok := preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
	if !ok {
		t.Fatalf("runtime_settings_parity = %#v, want entries", preserved["runtime_settings_parity"])
	}
	for _, id := range []string{"browser_selection", "browser_cache_enabled", "browser_cache_content_urls", "browser_check_newer_versions", "browser_new_user_per_iteration", "download_non_html_resources", "simulate_prefetch_prerender", "request_headers"} {
		assertRuntimeSettingParityEntry(t, parity, id, RuntimeSettingStatusExecuted)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "Browser Emulation import") {
			t.Fatalf("unexpected browser-emulation warning: %q", warning)
		}
	}
}

func TestConvertZipUnmappedBrowserTypeWarns(t *testing.T) {
	files := map[string]string{
		"Weird.usr": "[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction1=Action.c\n",
		"Action.c":  "Action()\n{\n\tweb_url(\"Home\", \"URL=https://example.test/\", LAST);\n\treturn 0;\n}\n",
		"default.cfg": `[WEB]
BrowserType=NetPositive
SimulateCache=maybe
`,
	}
	zipBytes := zipFilesForTest(t, files)

	result, err := ConvertZip(zipBytes, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	var sawUnmapped, sawInvalidBool bool
	for _, warning := range result.Warnings {
		if strings.Contains(warning, `browser type "NetPositive"`) {
			sawUnmapped = true
		}
		if strings.Contains(warning, "simulate-cache boolean") {
			sawInvalidBool = true
		}
	}
	if !sawUnmapped || !sawInvalidBool {
		t.Fatalf("warnings = %#v, want unmapped-browser and invalid-bool warnings", result.Warnings)
	}
	if result.Script != nil && result.Script.Config.BrowserEmulation != nil && result.Script.Config.BrowserEmulation.Mode != "" {
		t.Fatalf("emulation mode = %q, want unset", result.Script.Config.BrowserEmulation.Mode)
	}
}
