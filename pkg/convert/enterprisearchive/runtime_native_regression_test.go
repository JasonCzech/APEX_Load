package enterprisearchive

import (
	"strings"
	"testing"
)

func TestRuntimeHTTPVersionProjectionUsesNativeExactAndFallbackModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		content    string
		want       string
		wantStatus RuntimeSettingStatus
	}{
		{
			name:       "http 1.1 exact",
			content:    "[HTTP]\nHTTPVersion=1.1\n",
			want:       "1.1",
			wantStatus: RuntimeSettingStatusExecuted,
		},
		{
			name:       "explicit http 2 exact",
			content:    "[HTTP]\nHTTPVersion=HTTP/2\n",
			want:       "2",
			wantStatus: RuntimeSettingStatusExecuted,
		},
		{
			name:       "http 2 feature toggle retains h1 fallback",
			content:    "[HTTP]\nHTTPVersion=1.1\nEnableHTTP2=1\n",
			want:       "",
			wantStatus: RuntimeSettingStatusExecuted,
		},
		{
			name:       "http 1.0 is blocked and projected inert",
			content:    "[HTTP]\nHTTPVersion=HTTP/1.0\n",
			want:       "",
			wantStatus: RuntimeSettingStatusBlocked,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rc := ParseRuntimeCfg(test.content)
			if got := rc.ToHTTPConfig().Version; got != test.want {
				t.Fatalf("version = %q, want %q", got, test.want)
			}
			sections := collectRuntimeSections(map[string]string{"default.cfg": test.content})
			parity := applyRuntimeMigrationIssues(
				buildRTSParityReportForProtocol(sections, "http"),
				rc,
			)
			entry := runtimeParityEntryByID(t, parity, "http_transport")
			if entry.Status != test.wantStatus {
				t.Fatalf("http_transport = %#v, want %q", entry, test.wantStatus)
			}
		})
	}
}

func TestParseRuntimeCfgMapsNativeHTTPHeadersAndLogging(t *testing.T) {
	rc := ParseRuntimeCfg(`[HTTP]
HTTPVersion=1.1

[Browser Emulation]
UserAgent=RuntimeTest/1.0

[WEB]
EnableHTTP2=1
BrowserAcceptEncoding=gzip, deflate, br
BrowserAcceptLanguage=None

[Log]
LogOptions=LogExtended
MsgClassData=1
MsgClassParameters=1
MsgClassFull=1
`)
	config := rc.ToScriptConfig()
	if config.HTTP.Version != "" {
		t.Fatalf("http.version = %q, want automatic H2-preferred negotiation with HTTP/1.1 fallback", config.HTTP.Version)
	}
	if config.HTTP.AcceptEncoding != "gzip, deflate, br" {
		t.Fatalf("accept_encoding = %q, want full source encoding list", config.HTTP.AcceptEncoding)
	}
	if config.HTTP.AcceptLanguage != "" {
		t.Fatalf("accept_language = %q, want LoadRunner None sentinel omitted", config.HTTP.AcceptLanguage)
	}
	if config.Log.Level != "extended" {
		t.Fatalf("log level = %q, want extended", config.Log.Level)
	}
	if got := strings.Join(config.Log.ExtendedOptions, ","); got != "parameter_substitution,data_returned,advanced_trace" {
		t.Fatalf("extended options = %q, want all native MsgClass mappings", got)
	}
	if len(rc.RuntimeIssues) != 0 {
		t.Fatalf("runtime issues = %#v, want exact native mappings", rc.RuntimeIssues)
	}
}

func TestRuntimeParityReportsKnownActiveUnsupportedSettingsWithCfgProvenance(t *testing.T) {
	files := map[string]string{
		"default.cfg": `[WEB]
HTTPVer=1.1
EnableHTTP2=1
KeepAliveTimeout=6000
EnableSnapshotsInReplay=1

[Log]
LogOptions=LogBrief
`,
	}
	cfgName, rc := resolveRuntimeConfig(files, nil)
	sections := selectedRuntimeSections(collectRuntimeSections(files), cfgName, rc)
	dispositions := classifyRuntimeSelectedSections(sections, "http")
	parity := appendRuntimeDispositionParityEntries(
		applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(sections, "http"), rc),
		dispositions,
	)

	httpEntry := runtimeParityEntryByID(t, parity, "http_transport")
	if httpEntry.Status != RuntimeSettingStatusExecuted {
		t.Fatalf("http_transport = %#v, want executed", httpEntry)
	}
	logEntry := runtimeParityEntryByID(t, parity, "logging")
	if logEntry.Status != RuntimeSettingStatusExecuted {
		t.Fatalf("logging = %#v, want executed", logEntry)
	}
	unmapped := runtimeParityEntryByID(t, parity, "runtime_unmapped")
	if unmapped.Status != RuntimeSettingStatusCompatibilityOnly {
		t.Fatalf("runtime_unmapped = %#v, want compatibility_only", unmapped)
	}
	if len(unmapped.RuntimeFiles) != 1 || unmapped.RuntimeFiles[0] != "default.cfg" {
		t.Fatalf("runtime_unmapped files = %#v, want exact default.cfg provenance", unmapped.RuntimeFiles)
	}
	for _, evidence := range []string{`[WEB] KeepAliveTimeout`} {
		if !strings.Contains(unmapped.Notes, evidence) {
			t.Fatalf("runtime_unmapped notes = %q, want %s", unmapped.Notes, evidence)
		}
	}
	// EnableSnapshotsInReplay is a VuGen authoring aid: it is accounted for as
	// non-applicable rather than surfaced as a behavior gap.
	nonApplicable := runtimeParityEntryByID(t, parity, "runtime_non_applicable")
	if nonApplicable.Status != RuntimeSettingStatusNonApplicable {
		t.Fatalf("runtime_non_applicable = %#v, want non_applicable", nonApplicable)
	}
	if dispositions["default.cfg"]["WEB"]["EnableSnapshotsInReplay"].Status != RuntimeSettingDispositionNonApplicable {
		t.Fatalf("EnableSnapshotsInReplay disposition = %#v, want non_applicable", dispositions["default.cfg"]["WEB"]["EnableSnapshotsInReplay"])
	}
}

func TestRuntimeParityMarksDormantSpecificBrowserSelectionNonApplicable(t *testing.T) {
	files := map[string]string{
		"default.cfg": `[WEB]
Browser_Type=Microsoft Internet Explorer
BrowserVer=10.0
BrowserPlatform=Windows
CustomUserAgent=Custom/1.0
UseCustomAgent=1
`,
	}
	cfgName, rc := resolveRuntimeConfig(files, nil)
	sections := selectedRuntimeSections(collectRuntimeSections(files), cfgName, rc)
	parity := applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(sections, "http"), rc)
	entry := runtimeParityEntryByID(t, parity, "browser_selection")
	if entry.Status != RuntimeSettingStatusNonApplicable {
		t.Fatalf("browser_selection = %#v, want dormant/non-applicable under custom UA", entry)
	}
}

func TestParseRuntimeCfgRejectsUnknownTLSVersionWithPartialReview(t *testing.T) {
	files := map[string]string{
		"default.cfg": `[SSL]
SSLVersion=SSL3
`,
	}
	cfgName, rc := resolveRuntimeConfig(files, nil)
	config := rc.ToScriptConfig()
	if config.HTTP.TLS.MinVersion != "" || config.HTTP.TLS.MaxVersion != "" {
		t.Fatalf("TLS config = %#v, want APEX defaults for unsupported SSL3", config.HTTP.TLS)
	}

	sections := selectedRuntimeSections(collectRuntimeSections(files), cfgName, rc)
	parity := applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(sections, "http"), rc)
	entry := runtimeParityEntryByID(t, parity, "tls")
	if entry.Status != RuntimeSettingStatusPartial {
		t.Fatalf("tls parity = %#v, want partial", entry)
	}
	if !strings.Contains(entry.Notes, `SSL3`) || len(entry.RuntimeFiles) != 1 || entry.RuntimeFiles[0] != "default.cfg" {
		t.Fatalf("tls parity = %#v, want raw value and cfg provenance", entry)
	}
}

func runtimeParityEntryByID(t *testing.T, entries []RuntimeSettingParityEntry, id string) RuntimeSettingParityEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("parity entry %q not found in %#v", id, entries)
	return RuntimeSettingParityEntry{}
}
