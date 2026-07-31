package enterprisearchive

// runtime_vugen24_golden_test.go anchors the importer against a real VuGen
// 24.3 Web HTTP/HTML runtime-settings surface (sanitized hosts, otherwise the
// exact section/key/value shape VuGen writes). The earlier golden fixture is
// written in APEX-native section names and cannot catch regressions in the
// real LoadRunner vocabulary; this one can. It also enforces the completeness
// guarantee: every key of the selected cfg/usp receives an explicit
// disposition — no source runtime setting is silently dropped.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoldenVuGen24RealRuntimeSettings(t *testing.T) {
	zipBytes := zipTestDataDir(t, "testdata/golden_web_http_html_vugen24")

	result, err := ConvertZip(zipBytes, ConvertOptions{
		IncludeAssertions:    true,
		PreserveTransactions: true,
	})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	script := result.Script
	if script == nil {
		t.Fatal("no script converted")
	}
	// The SOAP-envelope web_custom_request classifies this Web HTTP/HTML source
	// as a SOAP-over-HTTP script; every Web runtime setting still applies.
	if script.Protocol != "soap" {
		t.Fatalf("protocol = %q, want soap", script.Protocol)
	}
	config := script.Config

	// Run logic + pacing come from the selected default.usp (Const 5s), not the
	// stale cfg [Iterations] IterationASAP it supersedes.
	if config.Pacing.Type != "fixed_interval" || config.Pacing.IntervalMs != 5000 {
		t.Fatalf("pacing = %#v, want fixed_interval every 5000ms from default.usp", config.Pacing)
	}
	if config.RunLogic.Iterations != 1 || config.RunLogic.AutoTransactions != "per_action" {
		t.Fatalf("run logic = %#v, want 1 iteration with per-action auto transactions", config.RunLogic)
	}
	if config.RunLogic.BlockScheduling != "sequential" {
		t.Fatalf("block scheduling = %q, want sequential from RunLogicRunMode", config.RunLogic.BlockScheduling)
	}
	if strings.Join(config.RunLogic.ActionSequence, ",") != "vuser_init,action,vuser_end" {
		t.Fatalf("action sequence = %#v, want the [Profile Actions] lifecycle", config.RunLogic.ActionSequence)
	}

	if config.ThinkTime.Type != "recorded" || config.ThinkTime.Multiplier != 1 || config.ThinkTime.Ignore {
		t.Fatalf("think time = %#v, want replay-as-recorded", config.ThinkTime)
	}

	// [WEB] transport rows: 500s timeouts, keep-alive, EnableHTTP2 negotiation.
	if config.HTTP.ConnectTimeoutMs != 500000 || config.HTTP.RequestTimeoutMs != 500000 {
		t.Fatalf("timeouts = %#v, want 500s connect/receive", config.HTTP)
	}
	if config.HTTP.Version != "" {
		t.Fatalf("http version = %q, want auto-negotiation for EnableHTTP2=1", config.HTTP.Version)
	}
	if config.HTTP.AcceptEncoding != "gzip, deflate, br" {
		t.Fatalf("accept encoding = %q", config.HTTP.AcceptEncoding)
	}
	// TLS 1.3 comes from the web_set_sockets_option source call.
	if config.HTTP.TLS.MinVersion != "1.3" || config.HTTP.TLS.MaxVersion != "1.3" {
		t.Fatalf("tls = %#v, want exact TLS 1.3", config.HTTP.TLS)
	}
	// ProxyNoLocal=1 deliberately fails the proxy closed for review.
	if config.HTTP.ProxyMode != "none" || config.HTTP.Proxy.URL != "" {
		t.Fatalf("proxy = %#v/%#v, want fail-closed no-proxy pending review", config.HTTP.ProxyMode, config.HTTP.Proxy)
	}

	// [Log] LogOptions=LogExtended + MsgClassParameters=1; AutoLog=0 keeps
	// error-only logging off.
	if config.Log.Level != "extended" || config.Log.OnErrorOnly {
		t.Fatalf("log = %#v, want extended without error-only", config.Log)
	}
	if strings.Join(config.Log.ExtendedOptions, ",") != "parameter_substitution" {
		t.Fatalf("extended options = %#v", config.Log.ExtendedOptions)
	}

	if !config.ErrorHandling.ContinueOnError || !config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error handling = %#v, want continue and snapshot on error", config.ErrorHandling)
	}

	// [ModemSpeed] with both toggles off is LR "Use maximum bandwidth".
	if config.WebRuntime.NetworkSimulation.Mode != "max" || config.WebRuntime.NetworkSimulation.BandwidthKbps != 0 {
		t.Fatalf("network simulation = %#v, want max bandwidth from [ModemSpeed]", config.WebRuntime.NetworkSimulation)
	}
	// [FILTERS] with empty include/exclude lists configures no filtering.
	if config.WebRuntime.DownloadFilters.Mode != "" || len(config.WebRuntime.DownloadFilters.Hosts) != 0 {
		t.Fatalf("download filters = %#v, want none from empty [FILTERS] lists", config.WebRuntime.DownloadFilters)
	}

	emulation := config.BrowserEmulation
	if emulation == nil || emulation.Mode != "specific" || emulation.BrowserType != "ie" || emulation.BrowserVersion != "10.0" || emulation.Platform != "windows" {
		t.Fatalf("browser emulation = %#v, want specific IE 10 on Windows", emulation)
	}
	if emulation.Cache == nil ||
		emulation.Cache.SimulateCache == nil || !*emulation.Cache.SimulateCache ||
		emulation.Cache.CacheContentURLs == nil || !*emulation.Cache.CacheContentURLs ||
		emulation.Cache.CheckNewerVersions == nil || !*emulation.Cache.CheckNewerVersions ||
		emulation.Cache.DownloadNonHTML == nil || !*emulation.Cache.DownloadNonHTML ||
		emulation.Cache.SimulatePrefetch == nil || !*emulation.Cache.SimulatePrefetch {
		t.Fatalf("browser cache = %#v, want every cache toggle on", emulation.Cache)
	}

	if script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatal("import metadata missing")
	}
	preserved := script.Metadata.Import.Preserved

	parity, ok := preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
	if !ok {
		t.Fatalf("runtime_settings_parity = %#v", preserved["runtime_settings_parity"])
	}
	byID := make(map[string]RuntimeSettingParityEntry, len(parity))
	for _, entry := range parity {
		byID[entry.ID] = entry
	}
	expectStatus := func(id string, status RuntimeSettingStatus) RuntimeSettingParityEntry {
		t.Helper()
		entry, ok := byID[id]
		if !ok {
			t.Fatalf("parity entry %q missing from %v", id, parityIDs(parity))
		}
		if entry.Status != status {
			t.Fatalf("parity %q status = %q, want %q (notes: %s)", id, entry.Status, status, entry.Notes)
		}
		return entry
	}

	blockScheduling := expectStatus("run_logic_block_scheduling", RuntimeSettingStatusCompatibilityOnly)
	if !containsFold(blockScheduling.RuntimeFiles, "default.usp") {
		t.Fatalf("block scheduling provenance = %#v, want default.usp", blockScheduling.RuntimeFiles)
	}
	expectStatus("run_logic_action_sequence", RuntimeSettingStatusCompatibilityOnly)
	networkEntry := expectStatus("network_simulation", RuntimeSettingStatusExecuted)
	if !containsFold(networkEntry.RuntimeFiles, "default.cfg") {
		t.Fatalf("network simulation provenance = %#v, want default.cfg", networkEntry.RuntimeFiles)
	}
	expectStatus("logging", RuntimeSettingStatusExecuted)
	expectStatus("pacing", RuntimeSettingStatusExecuted)
	expectStatus("run_logic_iterations", RuntimeSettingStatusExecuted)
	expectStatus("proxy", RuntimeSettingStatusPartial)

	// The Accept-Language spill rows carry a divergent value that must surface
	// for review instead of vanishing behind the base "None".
	headers := expectStatus("request_headers", RuntimeSettingStatusPartial)
	if !strings.Contains(headers.Notes, "BrowserAcceptLanguage_extra_arg_2") {
		t.Fatalf("request_headers notes = %q, want the spill-row review evidence", headers.Notes)
	}

	// The stock error-handler scaffold has no matching action file: recorded as
	// inert, not as a behavior gap.
	handler := expectStatus("run_logic_error_handler", RuntimeSettingStatusNonApplicable)
	if !strings.Contains(handler.Notes, "vuser_errorhandler") {
		t.Fatalf("error handler notes = %q", handler.Notes)
	}

	unmapped := expectStatus("runtime_unmapped", RuntimeSettingStatusCompatibilityOnly)
	for _, evidence := range []string{"[WEB] HttpErrorsAsWarnings", "[WEB] EnableChecks", "[Streaming] StreamTimeout", "[General] automatic_nested_transactions"} {
		if !strings.Contains(unmapped.Notes, evidence) {
			t.Fatalf("runtime_unmapped notes missing %q: %s", evidence, unmapped.Notes)
		}
	}
	expectStatus("runtime_non_applicable", RuntimeSettingStatusNonApplicable)

	// Declared-transaction conservation: [TransactionsOrder] ACME_RCI is started
	// by Action.c, so no transactions finding may appear.
	if _, exists := byID["transactions"]; exists {
		t.Fatalf("unexpected transactions conservation finding: %#v", byID["transactions"])
	}

	assertVuGen24DispositionCompleteness(t, preserved)
}

func parityIDs(entries []RuntimeSettingParityEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// assertVuGen24DispositionCompleteness is the no-silent-drop gate: the union of
// keys parsed from the selected cfg and usp must equal the union of keys that
// received a disposition, and every disposition must carry a known status.
func assertVuGen24DispositionCompleteness(t *testing.T, preserved map[string]any) {
	t.Helper()
	dispositions, ok := preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)
	if !ok {
		t.Fatalf("runtime_setting_dispositions = %#v", preserved["runtime_setting_dispositions"])
	}

	expected := make(map[string]struct{})
	for _, fixture := range []string{"default.cfg", "default.usp"} {
		content, err := os.ReadFile(filepath.Join("testdata/golden_web_http_html_vugen24", fixture))
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture, err)
		}
		for sectionName, keys := range ParseConfigSections(string(content)) {
			for key := range keys {
				expected[fmt.Sprintf("%s\x00%s\x00%s", fixture, sectionName, key)] = struct{}{}
			}
		}
	}
	if len(expected) == 0 {
		t.Fatal("fixture parsed to zero keys")
	}

	classified := make(map[string]struct{})
	for fileName, sections := range dispositions {
		for sectionName, keys := range sections {
			for key, disposition := range keys {
				switch disposition.Status {
				case RuntimeSettingDispositionMapped, RuntimeSettingDispositionCompatibilityOnly, RuntimeSettingDispositionNonApplicable:
				default:
					t.Fatalf("unclassified disposition for [%s] %s in %s: %#v", sectionName, key, fileName, disposition)
				}
				classified[fmt.Sprintf("%s\x00%s\x00%s", fileName, sectionName, key)] = struct{}{}
			}
		}
	}

	for identity := range expected {
		if _, ok := classified[identity]; !ok {
			parts := strings.SplitN(identity, "\x00", 3)
			t.Errorf("source runtime setting silently dropped: file=%s section=[%s] key=%s", parts[0], parts[1], parts[2])
		}
	}
	for identity := range classified {
		if _, ok := expected[identity]; !ok {
			parts := strings.SplitN(identity, "\x00", 3)
			t.Errorf("disposition for a key the fixture does not declare: file=%s section=[%s] key=%s", parts[0], parts[1], parts[2])
		}
	}
}
