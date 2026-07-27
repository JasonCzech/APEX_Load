package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func TestParseRuntimeCfgHonorsCaseInsensitiveSupportedKeys(t *testing.T) {
	const content = `[http]
connecttimeout=11
receivetimeout=12
stepdownloadtimeout=13
httpversion=2
keepalive=0
maxredirectiondepth=7
maxconnectionsperhost=8

[browser emulation]
acceptlanguage=en-US
acceptencoding=gzip, br
cacheenabled=1
cachecontenturls=1
checknewerversions=1
newuserperiteration=1
engine=chromium
deviceprofile=desktop
touchmode=1
orientation=landscape
clientmetrics=1
slowmoms=3
viewportwidth=1280
viewportheight=720
downloadresources=all

[security]
tlsversion=TLS1.2

[authentication]
username=alice
password=source-password

[runlogic]
iterations=3
autotransactions=per_action
blockscheduling=sequential
actionsequence=Action

[logging]
level=extended
onerroronly=1
extendedoptions=parameter_substitution

[miscellaneous]
continueonerror=1
snapshotonerror=1

[downloadfilters]
mode=include
hosts=example.test

[contentcheck]
text=needle
failiffound=0
scope=body

[networksimulation]
mode=custom
preset=lan
bandwidthkbps=100
latencyms=5

[streaming]
mode=fixed
buffersizeseconds=4
fixedbitratekbps=5
bandwidthestimation=adaptive

[dataformatting]
autodetect=1
chain=base64,gzip
`

	rc := ParseRuntimeCfg(content)
	if rc.ConnectTimeoutSec != 11 || rc.ReceiveTimeoutSec != 12 || rc.StepTimeoutSec != 13 ||
		rc.HTTPVersion != "2" || rc.KeepAlive || rc.MaxRedirects != 7 || rc.MaxConnsPerHost != 8 {
		t.Fatalf("case-insensitive HTTP projection = %#v", rc)
	}
	if rc.AcceptLanguage != "en-US" || rc.AcceptEncoding != "gzip, br" ||
		!rc.CacheEnabled || !rc.CacheContentURLs || !rc.CheckNewerVersions || !rc.NewUserPerIteration ||
		rc.BrowserEngine != "chromium" || rc.BrowserDeviceProfile != "desktop" ||
		!rc.BrowserTouchMode || rc.BrowserOrientation != "landscape" || !rc.BrowserClientMetrics ||
		rc.BrowserSlowMoMs != 3 || rc.BrowserViewportWidth != 1280 || rc.BrowserViewportHeight != 720 ||
		rc.DownloadResources != "all" {
		t.Fatalf("case-insensitive browser projection = %#v", rc)
	}
	if rc.SSLVersion != "TLS1.2" || rc.AuthUser != "alice" || rc.AuthPass != "source-password" ||
		rc.Iterations != 3 || rc.AutoTransactions != "per_action" ||
		rc.BlockScheduling != "sequential" || strings.Join(rc.ActionSequence, ",") != "action" {
		t.Fatalf("case-insensitive security/run-logic projection = %#v", rc)
	}
	if rc.LogLevel != "extended" || !rc.OnErrorOnly ||
		!containsString(rc.LogExtendedOptions, "parameter_substitution") ||
		!rc.ContinueOnError || !rc.SnapshotOnError {
		t.Fatalf("case-insensitive logging/error projection = %#v", rc)
	}
	if rc.DownloadFilterMode != "include" || strings.Join(rc.DownloadFilterHosts, ",") != "example.test" ||
		len(rc.ContentChecks) != 1 || rc.ContentChecks[0].Text != "needle" ||
		rc.ContentChecks[0].FailIfFound == nil || *rc.ContentChecks[0].FailIfFound {
		t.Fatalf("case-insensitive filters/content-check projection = %#v", rc)
	}
	if rc.NetworkSimulationMode != "custom" || rc.NetworkSimulationPreset != "lan" ||
		rc.NetworkBandwidthKbps != 100 || rc.NetworkLatencyMs != 5 ||
		rc.StreamingMode != "fixed" || rc.StreamingBufferSizeSeconds != 4 ||
		rc.StreamingFixedBitrateKbps != 5 || rc.StreamingBandwidthEstimation != "adaptive" ||
		!rc.DataFormattingAutoDetect || len(rc.DataFormattingChain) != 2 {
		t.Fatalf("case-insensitive extended runtime projection = %#v", rc)
	}

	parity := BuildRTSParityReport(map[string]map[string]map[string]string{
		"lower.cfg": ParseConfigSections(content),
	})
	for _, id := range []string{
		"http_transport", "request_headers", "browser_device_emulation", "tls",
		"authentication", "run_logic_iterations", "run_logic_auto_transactions",
		"run_logic_block_scheduling", "run_logic_action_sequence", "logging",
		"continue_on_error", "snapshot_on_error", "download_filters", "content_checks",
		"network_simulation", "streaming", "data_formatting_auto_detect", "data_formatting_chain",
	} {
		entry, ok := runtimeSecurityParityEntry(parity, id)
		if !ok || entry.Status != RuntimeSettingStatusExecuted {
			t.Fatalf("parity %q = %#v, want executed", id, entry)
		}
	}
}

func TestDisclosureSafeNeutralConfigValuesScrubURLCredentials(t *testing.T) {
	t.Parallel()
	const (
		userCanary  = "neutral-url-user-canary"
		passCanary  = "neutral-url-password-canary"
		tokenCanary = "neutral-url-token-canary"
	)
	value := "documentation=https://" + userCanary + ":" + passCanary +
		"@safe.example.test:8443/reference/path?access_token=" + tokenCanary +
		"#client_secret=" + tokenCanary
	sections := disclosureSafeConfigSections(map[string]map[string]string{
		"General": {"Documentation": value},
	})
	safe := sections["General"]["Documentation"]
	for _, canary := range []string{userCanary, passCanary, tokenCanary} {
		if strings.Contains(safe, canary) {
			t.Fatalf("neutral preserved setting retained URL credential %q: %q", canary, safe)
		}
	}
	for _, want := range []string{"safe.example.test:8443", "/reference/path"} {
		if !strings.Contains(safe, want) {
			t.Fatalf("neutral preserved setting lost safe URL component %q: %q", want, safe)
		}
	}

	source := disclosureSafeEnterpriseINIContent(
		"[General]\nDocumentation=" + value + "\nNamespace=http://schemas.example.test/enterprise/v1\n",
	)
	for _, canary := range []string{userCanary, passCanary, tokenCanary} {
		if strings.Contains(source, canary) {
			t.Fatalf("source-review INI retained URL credential %q: %q", canary, source)
		}
	}
	for _, want := range []string{"safe.example.test:8443", "/reference/path", "http://schemas.example.test/enterprise/v1"} {
		if !strings.Contains(source, want) {
			t.Fatalf("source-review INI lost safe URL component %q: %q", want, source)
		}
	}
}

func TestDisclosureSafeParameterSectionsRedactSemanticCredentialValues(t *testing.T) {
	t.Parallel()
	const (
		passwordCanary = "preserved-prm-password-canary"
		originalCanary = "preserved-prm-original-canary"
	)
	safe := disclosureSafeParameterSections(map[string]map[string]map[string]string{
		"flow.prm": {
			"parameter:Password": {
				"ParamName":     "Password",
				"ColumnName":    "Password",
				"Table":         "credentials.dat",
				"ValueList":     passwordCanary,
				"OriginalValue": originalCanary,
			},
			"parameter:UserName": {
				"ParamName": "UserName",
				"ValueList": "synthetic-user",
			},
		},
	})
	password := safe["flow.prm"]["parameter:Password"]
	if password["ParamName"] != "Password" ||
		password["ColumnName"] != "Password" ||
		password["Table"] != "credentials.dat" ||
		password["ValueList"] != credential.Redacted ||
		password["OriginalValue"] != credential.Redacted {
		t.Fatalf("sensitive .prm section projection = %#v", password)
	}
	if got := safe["flow.prm"]["parameter:UserName"]["ValueList"]; got != "synthetic-user" {
		t.Fatalf("nonsensitive .prm value changed: %q", got)
	}

	source := disclosureSafeEnterprisePRMContent(
		"[parameter:Password]\nParamName=Password\nValueList=" + passwordCanary +
			"\nOriginalValue=" + originalCanary +
			"\n[parameter:UserName]\nParamName=UserName\nValueList=synthetic-user\n",
	)
	if strings.Contains(source, passwordCanary) ||
		strings.Contains(source, originalCanary) ||
		!strings.Contains(source, "ValueList=synthetic-user") {
		t.Fatalf("semantic .prm source projection was not disclosure-safe: %q", source)
	}
}

func TestParseRuntimeCfgMixedCaseKeysMatchCanonical(t *testing.T) {
	tests := []struct {
		name      string
		canonical string
		variant   string
		project   func(*RuntimeConfig) string
	}{
		{
			name:      "http transport",
			canonical: "[HTTP]\nConnectTimeout=11\nReceiveTimeout=12\nKeepAlive=0\nMaxConnectionsPerHost=8\n",
			variant:   "[hTtP]\ncOnNeCtTiMeOuT=11\nrEcEiVeTiMeOuT=12\nkEePaLiVe=0\nmAxCoNnEcTiOnSpErHoSt=8\n",
			project: func(rc *RuntimeConfig) string {
				return fmt.Sprintf("%d/%d/%t/%d", rc.ConnectTimeoutSec, rc.ReceiveTimeoutSec, rc.KeepAlive, rc.MaxConnsPerHost)
			},
		},
		{
			name:      "browser emulation",
			canonical: "[Browser Emulation]\nAcceptLanguage=en-US\nCacheEnabled=1\nTouchMode=1\nViewportWidth=1280\n",
			variant:   "[bRoWsEr EmUlAtIoN]\naCcEpTlAnGuAgE=en-US\ncAcHeEnAbLeD=1\ntOuChMoDe=1\nvIeWpOrTwIdTh=1280\n",
			project: func(rc *RuntimeConfig) string {
				return fmt.Sprintf("%s/%t/%t/%d", rc.AcceptLanguage, rc.CacheEnabled, rc.BrowserTouchMode, rc.BrowserViewportWidth)
			},
		},
		{
			name:      "run logic logging and errors",
			canonical: "[RunLogic]\nIterations=3\nAutoTransactions=per_action\n[Logging]\nLevel=extended\nOnErrorOnly=1\n[Miscellaneous]\nContinueOnError=1\n",
			variant:   "[rUnLoGiC]\niTeRaTiOnS=3\naUtOtRaNsAcTiOnS=per_action\n[lOgGiNg]\nlEvEl=extended\noNeRrOrOnLy=1\n[mIsCeLlAnEoUs]\ncOnTiNuEoNeRrOr=1\n",
			project: func(rc *RuntimeConfig) string {
				return fmt.Sprintf("%d/%s/%s/%t/%t", rc.Iterations, rc.AutoTransactions, rc.LogLevel, rc.OnErrorOnly, rc.ContinueOnError)
			},
		},
		{
			name:      "extended runtime sections",
			canonical: "[NetworkSimulation]\nBandwidthKbps=100\nLatencyMs=5\n[Streaming]\nBufferSizeSeconds=4\nFixedBitrateKbps=5\n[DataFormatting]\nAutoDetect=1\nChain=base64,gzip\n",
			variant:   "[nEtWoRkSiMuLaTiOn]\nbAnDwIdThKbPs=100\nlAtEnCyMs=5\n[sTrEaMiNg]\nbUfFeRsIzEsEcOnDs=4\nfIxEdBiTrAtEkBpS=5\n[dAtAfOrMaTtInG]\naUtOdEtEcT=1\ncHaIn=base64,gzip\n",
			project: func(rc *RuntimeConfig) string {
				return fmt.Sprintf("%d/%d/%d/%d/%t/%d", rc.NetworkBandwidthKbps, rc.NetworkLatencyMs, rc.StreamingBufferSizeSeconds, rc.StreamingFixedBitrateKbps, rc.DataFormattingAutoDetect, len(rc.DataFormattingChain))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := test.project(ParseRuntimeCfg(test.canonical))
			if got := test.project(ParseRuntimeCfg(test.variant)); got != want {
				t.Fatalf("mixed-case projection = %q, want canonical %q", got, want)
			}
		})
	}
}

func TestRuntimeProxyDiagnosticsNeverEchoRawSettingValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		leaks   []string
	}{
		{
			name: "case-fold split endpoint conflict",
			content: `[Proxy]
ProxyHTTPHost=alice:alpha@proxy-one.test
proxyhttphost=bob:bravo@proxy-two.test
ProxyHTTPPort=3128
`,
			leaks: []string{"alice", "alpha", "bob", "bravo", "proxy-one.test", "proxy-two.test"},
		},
		{
			name: "unsupported mode",
			content: `[Proxy]
Mode=carol:charlie@proxy-mode.test
ProxyUseProxy=1
`,
			leaks: []string{"carol", "charlie", "proxy-mode.test"},
		},
		{
			name: "malformed enable flag",
			content: `[Proxy]
ProxyUseProxy=dana:delta@proxy-flag.test
`,
			leaks: []string{"dana", "delta", "proxy-flag.test"},
		},
		{
			name: "malformed split port",
			content: `[Proxy]
ProxyUseProxy=1
ProxyHTTPHost=proxy.test
ProxyHTTPPort=erin:echo@proxy-port.test
`,
			leaks: []string{"erin", "echo", "proxy-port.test"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg(test.content)
			diagnostics := fmt.Sprintf("%#v", rc.RuntimeIssues)
			if len(rc.RuntimeIssues) == 0 || !strings.Contains(diagnostics, credential.Redacted) {
				t.Fatalf("proxy diagnostics omitted redacted evidence: %s", diagnostics)
			}
			for _, leak := range test.leaks {
				if strings.Contains(diagnostics, leak) {
					t.Fatalf("proxy diagnostics leaked %q: %s", leak, diagnostics)
				}
			}
			if rc.ProxyMode != "none" || rc.ProxyURL != "" || rc.ProxyUser != "" || rc.ProxyPass != "" {
				t.Fatalf("unsafe proxy selection did not fail closed: %#v", rc)
			}
		})
	}
}

func TestRuntimeCfgCrossSectionAutoTransactionConflictsUseSemanticIdentity(t *testing.T) {
	t.Run("equivalent boolean and enum", func(t *testing.T) {
		rc := ParseRuntimeCfg(`[RunLogic]
AutoTransactions=per_action
[General]
AutomaticTransactions=TRUE
`)
		if rc.AutoTransactions != "per_action" {
			t.Fatalf("auto transactions = %q, want per_action", rc.AutoTransactions)
		}
		if _, conflict := runtimeConfigIssueByID(rc, "run_logic_auto_transactions"); conflict {
			t.Fatalf("equivalent declarations produced conflict: %#v", rc.RuntimeIssues)
		}
	})

	t.Run("contradictory declarations", func(t *testing.T) {
		rc := ParseRuntimeCfg(`[RunLogic]
AutoTransactions=per_iteration
[General]
AutoTransactions=1
`)
		if rc.AutoTransactions != "" {
			t.Fatalf("conflicting auto transactions = %q, want APEX default", rc.AutoTransactions)
		}
		issue, ok := runtimeConfigIssueByID(rc, "run_logic_auto_transactions")
		if !ok || !strings.Contains(issue.Notes, "Conflicting") {
			t.Fatalf("auto-transaction issue = %#v, want partial conflict", issue)
		}
	})
}

func TestRuntimeCfgBrowserOccurrenceIdentityDoesNotClaimHTTPOnlyKey(t *testing.T) {
	rc := ParseRuntimeCfg(`[HTTP]
BrowserType=Chrome
[WEB]
BrowserType=Firefox
`)
	if rc.BEBrowserType != "firefox" {
		t.Fatalf("browser type = %q, want valid WEB Firefox setting", rc.BEBrowserType)
	}
	if _, conflict := runtimeConfigIssueByID(rc, "browser_selection"); conflict {
		t.Fatalf("ignored HTTP BrowserType conflicted with WEB setting: %#v", rc.RuntimeIssues)
	}
}

func TestRuntimeCfgDormantChildConflictsDoNotDefaultOrRequireReview(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		forbidden string
	}{
		{
			name: "disabled proxy endpoint",
			content: `[Proxy]
ProxyUseProxy=0
ProxyURL=http://one.example
proxyurl=http://two.example
`,
			forbidden: "proxy",
		},
		{
			name: "disabled cache child",
			content: `[WEB]
SimulateCache=0
CacheHtmlPages=1
cachehtmlpages=0
`,
			forbidden: "browser_cache_content_urls",
		},
		{
			name: "custom user agent browser child",
			content: `[WEB]
UseCustomAgent=1
CustomUserAgent=Custom/1.0
BrowserType=Chrome
browsertype=Firefox
`,
			forbidden: "browser_selection",
		},
		{
			name: "non extended log child",
			content: `[Log]
LogOptions=LogBrief
MsgClassData=1
msgclassdata=0
`,
			forbidden: "logging",
		},
		{
			name: "internal JDK home child",
			content: `[JavaVM:Options]
UseJdk=search_jdk
JdkHome=/opt/one
jdkhome=/opt/two
`,
			forbidden: "java_vm_jdk",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg(test.content)
			if issue, found := runtimeConfigIssueByID(rc, test.forbidden); found &&
				strings.Contains(issue.Notes, "Conflicting") {
				t.Fatalf("dormant child produced conflict issue: %#v", issue)
			}
		})
	}
}

func TestRuntimeParityRecognizesProxyUserAlias(t *testing.T) {
	parity := buildRTSParityReportForProtocol(
		map[string]map[string]map[string]string{
			"default.cfg": {
				"Proxy": {
					"Mode":      "custom",
					"ProxyURL":  "http://proxy.example",
					"ProxyUser": "runner",
				},
			},
		},
		"http",
	)
	entry, ok := runtimeSecurityParityEntry(parity, "proxy")
	if !ok || entry.Status != RuntimeSettingStatusExecuted {
		t.Fatalf("proxy parity = %#v, want ProxyUser alias executed", entry)
	}
}

func TestParseRuntimeCfgRejectsConflictingProxyAliasesAcrossSections(t *testing.T) {
	rc := ParseRuntimeCfg(`[Proxy]
ProxyUseProxy=1
Mode=custom
ProxyServer=http://one.example.test:8080

[WEB]
ProxyUseProxy=1
ProxyMode=custom
ProxyURL=http://two.example.test:8080
`)
	if rc.ProxyMode != "none" || rc.ProxyURL != "" {
		t.Fatalf("proxy = mode %q URL %q, want no-proxy default", rc.ProxyMode, rc.ProxyURL)
	}
	issue, ok := runtimeConfigIssueByID(rc, "proxy")
	if !ok || !strings.Contains(issue.Notes, "Conflicting") {
		t.Fatalf("proxy issues = %#v, want cross-section conflict", rc.RuntimeIssues)
	}
}

func runtimeSecurityParityEntry(entries []RuntimeSettingParityEntry, id string) (RuntimeSettingParityEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return RuntimeSettingParityEntry{}, false
}
