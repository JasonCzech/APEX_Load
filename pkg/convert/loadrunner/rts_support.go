package loadrunner

import (
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

type RuntimeSettingStatus string

const (
	RuntimeSettingStatusExecuted          RuntimeSettingStatus = "executed"
	RuntimeSettingStatusPartial           RuntimeSettingStatus = "partial"
	RuntimeSettingStatusCompatibilityOnly RuntimeSettingStatus = "compatibility_only"
	RuntimeSettingStatusNonApplicable     RuntimeSettingStatus = "non_applicable"
	RuntimeSettingStatusBlocked           RuntimeSettingStatus = "blocked"
)

type RuntimeSettingSupportEntry struct {
	ID      string               `json:"id"`
	Area    string               `json:"area"`
	Concept string               `json:"concept"`
	Fields  []string             `json:"fields,omitempty"`
	Status  RuntimeSettingStatus `json:"status"`
	Notes   string               `json:"notes,omitempty"`
}

type RuntimeSettingSupportExample struct {
	Title    string `json:"title"`
	Coverage string `json:"coverage"`
	Test     string `json:"test"`
}

type RuntimeSettingMigrationPolicy struct {
	BlockStatuses []RuntimeSettingStatus `json:"block_statuses,omitempty"`
}

type RuntimeSettingMigrationReadiness struct {
	ScorePct         int                           `json:"score_pct"`
	Blocked          bool                          `json:"blocked"`
	Summary          string                        `json:"summary,omitempty"`
	BlockingStatuses []RuntimeSettingStatus        `json:"blocking_statuses,omitempty"`
	BlockingSettings []RuntimeSettingParityEntry   `json:"blocking_settings,omitempty"`
	Policy           RuntimeSettingMigrationPolicy `json:"policy"`
}

type RuntimeSettingParityEntry struct {
	ID           string               `json:"id"`
	Area         string               `json:"area"`
	Concept      string               `json:"concept"`
	Fields       []string             `json:"fields,omitempty"`
	Status       RuntimeSettingStatus `json:"status"`
	Notes        string               `json:"notes,omitempty"`
	RuntimeFiles []string             `json:"runtime_files,omitempty"`
}

type runtimeSettingMatcher struct {
	sectionAliases []string
	keyAliases     []string
	values         []string
}

type runtimeSettingSupportDefinition struct {
	entry    RuntimeSettingSupportEntry
	matchers []runtimeSettingMatcher
}

var loadRunnerRTSSupportDefinitions = []runtimeSettingSupportDefinition{
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "http_transport",
			Area:    "HTTP",
			Concept: "Timeouts, redirects, and connection reuse",
			Fields:  []string{"config.http.connect_timeout_ms", "config.http.request_timeout_ms", "config.http.max_redirects", "config.http.keep_alive", "config.http.max_conns_per_host"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Stormrunner maps core Web HTTP/HTML transport runtime settings into the HTTP runtime.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences"}, []string{"ConnectTimeout", "ReceiveTimeout", "StepDownloadTimeout", "HTTPVersion", "KeepAlive", "MaxConnectionsPerHost", "MaxConnections", "MaxConnectionsPerHostHTTP", "MaxRedirections"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "proxy",
			Area:    "Proxy",
			Concept: "Proxy mode and custom proxy",
			Fields:  []string{"config.http.proxy_mode", "config.http.proxy"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Supports none, system, and custom proxy execution paths.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Proxy"}, []string{"Mode", "ProxyMode", "Type", "ProxyServer", "ProxyURL", "Server", "ProxyUserName", "UserName", "ProxyPassword", "Password"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "request_headers",
			Area:    "Browser Emulation",
			Concept: "User agent and default accept headers",
			Fields:  []string{"config.browser.user_agent", "config.http.user_agent", "config.http.accept_language", "config.http.accept_encoding"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Imported user agent and accept headers execute in HTTP and browser runtimes.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Browser Emulation", "Browser"}, []string{"UserAgent", "AcceptLanguage", "AcceptEncoding"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "tls",
			Area:    "Security",
			Concept: "TLS or SSL version",
			Fields:  []string{"config.http.tls.min_version"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Stormrunner maps the configured SSL or TLS floor onto the HTTP client TLS settings.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"SSL", "Security"}, []string{"SSLVersion", "TLSVersion"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "authentication",
			Area:    "Authentication",
			Concept: "Basic authentication credentials",
			Fields:  []string{"config.http.auth"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Imported username and password values map into Stormrunner basic auth configuration.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Authentication"}, []string{"UserName", "Username", "Password"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "run_logic_iterations",
			Area:    "Run Logic",
			Concept: "Iterations",
			Fields:  []string{"config.run_logic.iterations"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Applied by the engine iteration loop.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Run Logic", "RunLogic"}, []string{"Iterations", "NumberOfIterations"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "run_logic_auto_transactions",
			Area:    "Run Logic",
			Concept: "Auto-transactions",
			Fields:  []string{"config.run_logic.auto_transactions"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Supports per-action and per-iteration wrapping.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Run Logic", "RunLogic"}, []string{"AutoTransactions"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "run_logic_block_scheduling",
			Area:    "Run Logic",
			Concept: "Block scheduling",
			Fields:  []string{"config.run_logic.block_scheduling"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Supports sequential, random, and percentage scheduling.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Run Logic", "RunLogic"}, []string{"BlockScheduling"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "run_logic_action_sequence",
			Area:    "Run Logic",
			Concept: "Action sequence",
			Fields:  []string{"config.run_logic.action_sequence"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Preserved and enforced by the engine action ordering.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Run Logic", "RunLogic"}, []string{"ActionSequence", "Sequence"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "pacing",
			Area:    "Pacing",
			Concept: "Delay mode and bounds",
			Fields:  []string{"config.pacing"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Fixed, random delay, and interval pacing settings execute in the engine.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Pacing"}, []string{"Mode", "PacingMode", "DelayMs", "Delay", "MinDelayMs", "MinDelay", "MaxDelayMs", "MaxDelay", "IntervalMs", "Interval", "MinIntervalMs", "MinInterval", "MaxIntervalMs", "MaxInterval"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "think_time",
			Area:    "Think Time",
			Concept: "Ignore, multiplier, and limit",
			Fields:  []string{"config.think_time"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Applied by the engine after action execution.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Think Time", "ThinkTime"}, []string{"Ignore", "IgnoreThinkTime", "Multiplier", "MultiplyRecordedThinkTimeBy", "LimitMs", "LimitThinkTimeMs"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "logging",
			Area:    "Logging",
			Concept: "Log level, error-only, and extended logging options",
			Fields:  []string{"config.log"},
			Status:  RuntimeSettingStatusPartial,
			Notes:   "Extended logging fidelity is narrower than full VuGen extended tracing.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Log", "Logging"}, []string{"Level", "LogLevel", "OnErrorOnly", "LogOnErrorOnly", "ExtendedOptions", "ExtendedLogOptions", "ParameterSubstitution", "DataReturned", "AdvancedTrace"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "continue_on_error",
			Area:    "Misc",
			Concept: "Continue on error",
			Fields:  []string{"config.error_handling.continue_on_error"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Supported through runtime error handling paths.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Misc", "Miscellaneous"}, []string{"ContinueOnError", "ErrorContinue"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "snapshot_on_error",
			Area:    "Misc",
			Concept: "Snapshot on error",
			Fields:  []string{"config.error_handling.snapshot_on_error"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP, browser, SOAP, Java-over-HTTP, WebSocket, and gRPC failure paths populate snapshots where request/response artifacts exist.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Misc", "Miscellaneous"}, []string{"SnapshotOnError", "GenerateSnapshotOnError"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_device_emulation",
			Area:    "Browser Emulation",
			Concept: "Engine, device profile, touch, orientation, and viewport",
			Fields:  []string{"config.browser.engine", "config.browser.device_profile", "config.browser.touch_mode", "config.browser.orientation", "config.browser.client_metrics", "config.browser.slow_mo_ms", "config.browser.viewport"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Scenario authoring and browser replay execute imported mobile-web emulation settings.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser"}, []string{"Engine", "BrowserEngine", "DeviceProfile", "MobileProfile", "EmulatedDevice", "TouchMode", "TouchEnabled", "Orientation", "ClientMetrics", "CollectClientMetrics", "SlowMoMs", "SlowMotionMs", "ViewportWidth", "ScreenWidth", "ViewportHeight", "ScreenHeight"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_cache_enabled",
			Area:    "Browser Emulation",
			Concept: "Cache enabled",
			Fields:  []string{"config.browser.cache_enabled"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Browser runtime toggles browser cache usage.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser"}, []string{"CacheEnabled"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_cache_content_urls",
			Area:    "Browser Emulation",
			Concept: "Cache URLs requiring content",
			Fields:  []string{"config.browser.cache_content_urls"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Browser runtime forces content revalidation when disabled.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser"}, []string{"CacheContentURLs", "CacheURLsRequiringContent"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_check_newer_versions",
			Area:    "Browser Emulation",
			Concept: "Check for newer versions",
			Fields:  []string{"config.browser.check_newer_versions"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Browser runtime injects revalidation headers.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser"}, []string{"CheckNewerVersions", "CheckForNewerPages"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_new_user_per_iteration",
			Area:    "Browser Emulation",
			Concept: "New user each iteration",
			Fields:  []string{"config.browser.new_user_per_iteration"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Browser protocol is reset between iterations.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser"}, []string{"NewUserPerIteration", "SimulateNewUser"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_download_resources_all",
			Area:    "Browser Emulation",
			Concept: "Download resources = all",
			Fields:  []string{"config.browser.download_resources"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Default browser behavior loads all resources.",
		},
		matchers: []runtimeSettingMatcher{matcherWithValues([]string{"Browser Emulation", "Browser"}, []string{"DownloadResources", "DownloadNonHtmlResources"}, []string{"all"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_download_resources_none",
			Area:    "Browser Emulation",
			Concept: "Download resources = none",
			Fields:  []string{"config.browser.download_resources"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Static asset URLs are blocked during browser replay.",
		},
		matchers: []runtimeSettingMatcher{matcherWithValues([]string{"Browser Emulation", "Browser"}, []string{"DownloadResources", "DownloadNonHtmlResources"}, []string{"none"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_download_resources_recorded",
			Area:    "Browser Emulation",
			Concept: "Download resources = recorded",
			Fields:  []string{"config.browser.download_resources"},
			Status:  RuntimeSettingStatusPartial,
			Notes:   "Recorded-only assets execute when preserved recorded-resource metadata is available.",
		},
		matchers: []runtimeSettingMatcher{matcherWithValues([]string{"Browser Emulation", "Browser"}, []string{"DownloadResources", "DownloadNonHtmlResources"}, []string{"recorded"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "additional_attributes",
			Area:    "Additional Attributes",
			Concept: "Arbitrary RTS name/value metadata",
			Fields:  []string{"config.web_runtime.additional_attributes"},
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes:   "Preserved for import and authoring parity, but not executed directly.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Additional Attributes", "Attributes"}, nil)},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "download_filters",
			Area:    "Download Filters",
			Concept: "Allow list and block list hosts",
			Fields:  []string{"config.web_runtime.download_filters"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Browser runtime enforces host-based asset filtering.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Download Filters", "DownloadFilters"}, []string{"Mode", "FilterMode", "Hosts", "IncludeHosts", "ExcludeHosts"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "content_checks",
			Area:    "Content Check",
			Concept: "Global required or forbidden text checks",
			Fields:  []string{"config.web_runtime.content_checks"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP runtime enforces content checks against main-document responses.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Content Check", "ContentCheck"}, []string{"Text", "SearchText", "Scope", "FailIfFound"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "network_simulation",
			Area:    "Network Speed Simulation",
			Concept: "Preset and custom bandwidth or latency",
			Fields:  []string{"config.web_runtime.network_simulation"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Browser runtime maps settings onto DevTools network throttling.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Network Speed Simulation", "NetworkSimulation"}, []string{"Mode", "SimulationMode", "Preset", "BandwidthKbps", "Bandwidth", "LatencyMs"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "streaming",
			Area:    "Streaming",
			Concept: "Adaptive or fixed playback settings",
			Fields:  []string{"config.web_runtime.streaming"},
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes:   "Streaming and media playback semantics are preserved but not executed.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Streaming"}, []string{"Mode", "BufferSizeSeconds", "BufferSize", "FixedBitrateKbps", "FixedBitrate", "BandwidthEstimation"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "data_formatting_auto_detect",
			Area:    "Data Formatting",
			Concept: "Auto-detect DFEs",
			Fields:  []string{"config.web_runtime.data_formatting.auto_detect"},
			Status:  RuntimeSettingStatusPartial,
			Notes:   "HTTP runtime auto-detects a narrow set of encoded payloads today.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Data Formatting", "DataFormatting"}, []string{"AutoDetect", "DFEAutoDetect"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "data_formatting_chain",
			Area:    "Data Formatting",
			Concept: "DFE transform chain",
			Fields:  []string{"config.web_runtime.data_formatting.chain"},
			Status:  RuntimeSettingStatusPartial,
			Notes:   "Stormrunner executes a bounded transform chain instead of the full DFE surface.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Data Formatting", "DataFormatting"}, []string{"Chain", "DFEChain"})},
	},
}

var loadRunnerRTSSupportMatrixNotes = []string{
	"download_resources = recorded still depends on preserved recorded-resource metadata; manually authored scripts without those URLs cannot execute that mode yet.",
	"Streaming and media playback settings remain compatibility-only and do not execute browser media semantics.",
	"DFE parity is intentionally bounded to the current HTTP response transform chain instead of the full LoadRunner surface.",
	"Extended logging fidelity is narrower than full VuGen tracing even when the settings are imported and preserved.",
}

var loadRunnerRTSSupportMatrixExamples = []RuntimeSettingSupportExample{
	{
		Title:    "Extended Web HTTP/HTML runtime import",
		Coverage: "Parses transport, proxy, pacing, think time, logging, browser emulation, download filters, content checks, network shaping, streaming, and DFE settings into Stormrunner config.",
		Test:     "pkg/convert/loadrunner/converter_test.go::TestParseRuntimeCfgToScriptConfigMapsExtendedRuntimeSettings",
	},
	{
		Title:    "ZIP import parity metadata",
		Coverage: "Preserves runtime files and emits per-import runtime setting parity classifications with status counts for review workflows.",
		Test:     "pkg/convert/loadrunner/converter_test.go::TestConvertZipMapsExtendedRuntimeSettings",
	},
	{
		Title:    "Import envelope parity review",
		Coverage: "Surfaces runtime setting parity in the shared LoadRunner import response, including summary counts and review sections.",
		Test:     "pkg/api/handlers_scripts_test.go::TestHandleImportLoadRunnerSummarizesRuntimeSettingParity",
	},
	{
		Title:    "Golden Web HTTP/HTML fixture",
		Coverage: "Converts a representative LoadRunner Web HTTP/HTML archive with transport, proxy, auth, browser emulation, recorded resources, logging, streaming, DFE, and network settings, then asserts side-by-side runtime support statuses.",
		Test:     "pkg/convert/loadrunner/converter_test.go::TestGoldenWebHTTPHTMLFixtureRuntimeParity",
	},
}

var runtimeSettingMigrationStatusWeights = map[RuntimeSettingStatus]int{
	RuntimeSettingStatusExecuted:          100,
	RuntimeSettingStatusPartial:           60,
	RuntimeSettingStatusCompatibilityOnly: 25,
	RuntimeSettingStatusNonApplicable:     10,
	RuntimeSettingStatusBlocked:           0,
}

func GetRTSSupportMatrix() []RuntimeSettingSupportEntry {
	entries := make([]RuntimeSettingSupportEntry, 0, len(loadRunnerRTSSupportDefinitions))
	for _, definition := range loadRunnerRTSSupportDefinitions {
		entries = append(entries, RuntimeSettingSupportEntry{
			ID:      definition.entry.ID,
			Area:    definition.entry.Area,
			Concept: definition.entry.Concept,
			Fields:  cloneStrings(definition.entry.Fields),
			Status:  definition.entry.Status,
			Notes:   definition.entry.Notes,
		})
	}
	return entries
}

func GetRTSSupportMatrixNotes() []string {
	return cloneStrings(loadRunnerRTSSupportMatrixNotes)
}

func GetRTSSupportMatrixExamples() []RuntimeSettingSupportExample {
	if len(loadRunnerRTSSupportMatrixExamples) == 0 {
		return nil
	}
	examples := make([]RuntimeSettingSupportExample, 0, len(loadRunnerRTSSupportMatrixExamples))
	for _, example := range loadRunnerRTSSupportMatrixExamples {
		examples = append(examples, example)
	}
	return examples
}

func DefaultRuntimeSettingMigrationPolicy() RuntimeSettingMigrationPolicy {
	return RuntimeSettingMigrationPolicy{
		BlockStatuses: []RuntimeSettingStatus{RuntimeSettingStatusBlocked},
	}
}

func ParseRuntimeSettingMigrationPolicy(statusesCSV string) RuntimeSettingMigrationPolicy {
	policy := DefaultRuntimeSettingMigrationPolicy()
	if strings.TrimSpace(statusesCSV) == "" {
		return policy
	}

	parsed := make([]RuntimeSettingStatus, 0)
	for _, raw := range strings.Split(statusesCSV, ",") {
		status := RuntimeSettingStatus(normalizeEnum(strings.TrimSpace(raw)))
		if !isRuntimeSettingStatus(status) {
			continue
		}
		parsed = append(parsed, status)
	}
	parsed = uniqueRuntimeSettingStatuses(parsed)
	if len(parsed) == 0 {
		return policy
	}
	policy.BlockStatuses = parsed
	return policy
}

func BuildRTSMigrationReadiness(entries []RuntimeSettingParityEntry, policy RuntimeSettingMigrationPolicy) *RuntimeSettingMigrationReadiness {
	if len(entries) == 0 {
		return nil
	}

	policy = normalizeRuntimeSettingMigrationPolicy(policy)
	points := 0
	blockingEntries := make([]RuntimeSettingParityEntry, 0)
	blockingStatusSet := make(map[RuntimeSettingStatus]struct{})
	for _, entry := range entries {
		points += runtimeSettingMigrationStatusWeights[entry.Status]
		if runtimeSettingStatusInList(entry.Status, policy.BlockStatuses) {
			blockingEntries = append(blockingEntries, cloneRuntimeSettingParityEntry(entry))
			blockingStatusSet[entry.Status] = struct{}{}
		}
	}

	scorePct := (points + len(entries)/2) / len(entries)
	blockingStatuses := make([]RuntimeSettingStatus, 0, len(blockingStatusSet))
	for _, status := range policy.BlockStatuses {
		if _, ok := blockingStatusSet[status]; ok {
			blockingStatuses = append(blockingStatuses, status)
		}
	}

	readiness := &RuntimeSettingMigrationReadiness{
		ScorePct:         scorePct,
		Blocked:          len(blockingEntries) > 0,
		BlockingStatuses: blockingStatuses,
		BlockingSettings: blockingEntries,
		Policy:           policy,
	}
	readiness.Summary = buildRuntimeSettingMigrationSummary(readiness)
	return readiness
}

func BuildRTSParityReport(runtimeSections map[string]map[string]map[string]string) []RuntimeSettingParityEntry {
	if len(runtimeSections) == 0 {
		return nil
	}

	entries := make([]RuntimeSettingParityEntry, 0)
	for _, definition := range loadRunnerRTSSupportDefinitions {
		files := runtimeSettingFilesForDefinition(definition, runtimeSections)
		if len(files) == 0 {
			continue
		}
		entries = append(entries, RuntimeSettingParityEntry{
			ID:           definition.entry.ID,
			Area:         definition.entry.Area,
			Concept:      definition.entry.Concept,
			Fields:       cloneStrings(definition.entry.Fields),
			Status:       definition.entry.Status,
			Notes:        definition.entry.Notes,
			RuntimeFiles: files,
		})
	}

	return entries
}

func BuildRTSParityReportFromScript(script *dsl.Script) []RuntimeSettingParityEntry {
	if script == nil {
		return nil
	}

	entries := make([]RuntimeSettingParityEntry, 0)
	for _, definition := range loadRunnerRTSSupportDefinitions {
		if !scriptConfigMatchesRuntimeSettingDefinition(script.Config, definition) {
			continue
		}
		entries = append(entries, RuntimeSettingParityEntry{
			ID:      definition.entry.ID,
			Area:    definition.entry.Area,
			Concept: definition.entry.Concept,
			Fields:  cloneStrings(definition.entry.Fields),
			Status:  definition.entry.Status,
			Notes:   definition.entry.Notes,
		})
	}

	return entries
}

func RuntimeSettingStatusCounts(entries []RuntimeSettingParityEntry) map[string]int {
	if len(entries) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, entry := range entries {
		counts[string(entry.Status)]++
	}
	return counts
}

func cloneRuntimeSettingParityEntry(entry RuntimeSettingParityEntry) RuntimeSettingParityEntry {
	return RuntimeSettingParityEntry{
		ID:           entry.ID,
		Area:         entry.Area,
		Concept:      entry.Concept,
		Fields:       cloneStrings(entry.Fields),
		Status:       entry.Status,
		Notes:        entry.Notes,
		RuntimeFiles: cloneStrings(entry.RuntimeFiles),
	}
}

func runtimeSettingFilesForDefinition(definition runtimeSettingSupportDefinition, runtimeSections map[string]map[string]map[string]string) []string {
	files := make([]string, 0)
	for fileName, sections := range runtimeSections {
		if runtimeSectionsMatchDefinition(sections, definition) {
			files = append(files, filepath.Base(fileName))
		}
	}
	if len(files) == 0 {
		return nil
	}
	files = uniqueStrings(files)
	sort.Strings(files)
	return files
}

func runtimeSectionsMatchDefinition(sections map[string]map[string]string, definition runtimeSettingSupportDefinition) bool {
	for _, matcher := range definition.matchers {
		if runtimeSectionsMatch(sections, matcher) {
			return true
		}
	}
	return false
}

func runtimeSectionsMatch(sections map[string]map[string]string, matcher runtimeSettingMatcher) bool {
	for sectionName, keys := range sections {
		if !matcher.matchesSection(sectionName) {
			continue
		}
		if len(matcher.keyAliases) == 0 {
			return len(keys) > 0
		}
		for key, value := range keys {
			if matcher.matchesKey(key) && matcher.matchesValue(value) {
				return true
			}
		}
	}
	return false
}

func (matcher runtimeSettingMatcher) matchesSection(sectionName string) bool {
	if len(matcher.sectionAliases) == 0 {
		return true
	}
	for _, alias := range matcher.sectionAliases {
		if strings.EqualFold(strings.TrimSpace(sectionName), strings.TrimSpace(alias)) {
			return true
		}
	}
	return false
}

func (matcher runtimeSettingMatcher) matchesKey(key string) bool {
	if len(matcher.keyAliases) == 0 {
		return true
	}
	for _, alias := range matcher.keyAliases {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(alias)) {
			return true
		}
	}
	return false
}

func (matcher runtimeSettingMatcher) matchesValue(value string) bool {
	if len(matcher.values) == 0 {
		return true
	}
	normalizedValue := normalizeEnum(strings.TrimSpace(value))
	for _, candidate := range matcher.values {
		if normalizedValue == normalizeEnum(candidate) {
			return true
		}
	}
	return false
}

func matcher(sectionAliases []string, keyAliases []string) runtimeSettingMatcher {
	return runtimeSettingMatcher{sectionAliases: sectionAliases, keyAliases: keyAliases}
}

func matcherWithValues(sectionAliases []string, keyAliases []string, values []string) runtimeSettingMatcher {
	return runtimeSettingMatcher{sectionAliases: sectionAliases, keyAliases: keyAliases, values: values}
}

func buildRuntimeSettingMigrationSummary(readiness *RuntimeSettingMigrationReadiness) string {
	if readiness == nil {
		return ""
	}
	policyStatuses := make([]string, 0, len(readiness.Policy.BlockStatuses))
	for _, status := range readiness.Policy.BlockStatuses {
		policyStatuses = append(policyStatuses, string(status))
	}
	if readiness.Blocked {
		return "Migration readiness score " + strconv.Itoa(readiness.ScorePct) + "%. Blocking runtime settings matched the selected block statuses: " + strings.Join(policyStatuses, ", ") + "."
	}
	return "Migration readiness score " + strconv.Itoa(readiness.ScorePct) + "%. No imported runtime settings matched the selected block statuses: " + strings.Join(policyStatuses, ", ") + "."
}

func isRuntimeSettingStatus(status RuntimeSettingStatus) bool {
	switch status {
	case RuntimeSettingStatusExecuted, RuntimeSettingStatusPartial, RuntimeSettingStatusCompatibilityOnly, RuntimeSettingStatusNonApplicable, RuntimeSettingStatusBlocked:
		return true
	default:
		return false
	}
}

func normalizeRuntimeSettingMigrationPolicy(policy RuntimeSettingMigrationPolicy) RuntimeSettingMigrationPolicy {
	policy.BlockStatuses = uniqueRuntimeSettingStatuses(policy.BlockStatuses)
	if len(policy.BlockStatuses) == 0 {
		return DefaultRuntimeSettingMigrationPolicy()
	}
	return policy
}

func runtimeSettingStatusInList(status RuntimeSettingStatus, statuses []RuntimeSettingStatus) bool {
	for _, candidate := range statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func uniqueRuntimeSettingStatuses(statuses []RuntimeSettingStatus) []RuntimeSettingStatus {
	if len(statuses) == 0 {
		return nil
	}
	seen := make(map[RuntimeSettingStatus]struct{}, len(statuses))
	result := make([]RuntimeSettingStatus, 0, len(statuses))
	for _, status := range statuses {
		if !isRuntimeSettingStatus(status) {
			continue
		}
		if _, ok := seen[status]; ok {
			continue
		}
		seen[status] = struct{}{}
		result = append(result, status)
	}
	return result
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func scriptConfigMatchesRuntimeSettingDefinition(config dsl.ScriptConfig, definition runtimeSettingSupportDefinition) bool {
	switch definition.entry.ID {
	case "proxy":
		return (config.HTTP.ProxyMode != "" && !strings.EqualFold(config.HTTP.ProxyMode, "none")) || hasConfiguredValue(reflect.ValueOf(config.HTTP.Proxy))
	case "think_time":
		return config.ThinkTime.Ignore || config.ThinkTime.Multiplier != 0 || config.ThinkTime.LimitMs != 0
	case "pacing":
		return (config.Pacing.Type != "" && !strings.EqualFold(config.Pacing.Type, "none")) ||
			config.Pacing.DelayMs != 0 ||
			config.Pacing.MinDelayMs != 0 ||
			config.Pacing.MaxDelayMs != 0 ||
			config.Pacing.IntervalMs != 0 ||
			config.Pacing.MinIntervalMs != 0 ||
			config.Pacing.MaxIntervalMs != 0 ||
			config.Pacing.TPS != 0
	case "run_logic_auto_transactions":
		return config.RunLogic.AutoTransactions != "" && !strings.EqualFold(config.RunLogic.AutoTransactions, "none")
	case "logging":
		return (config.Log.Level != "" && !strings.EqualFold(config.Log.Level, "standard")) || config.Log.OnErrorOnly || len(config.Log.ExtendedOptions) > 0
	case "browser_download_resources_all":
		return strings.EqualFold(config.Browser.DownloadResources, "all")
	case "browser_download_resources_none":
		return strings.EqualFold(config.Browser.DownloadResources, "none")
	case "browser_download_resources_recorded":
		return strings.EqualFold(config.Browser.DownloadResources, "recorded")
	case "additional_attributes":
		return len(config.WebRuntime.AdditionalAttributes) > 0
	case "data_formatting_auto_detect":
		return config.WebRuntime.DataFormatting.AutoDetect
	case "data_formatting_chain":
		return len(config.WebRuntime.DataFormatting.Chain) > 0
	}

	for _, fieldPath := range definition.entry.Fields {
		if scriptConfigHasConfiguredField(config, fieldPath) {
			return true
		}
	}
	return false
}

func scriptConfigHasConfiguredField(config dsl.ScriptConfig, fieldPath string) bool {
	segments := strings.Split(fieldPath, ".")
	if len(segments) < 2 || segments[0] != "config" {
		return false
	}

	current := reflect.ValueOf(config)
	for _, segment := range segments[1:] {
		for current.Kind() == reflect.Pointer {
			if current.IsNil() {
				return false
			}
			current = current.Elem()
		}
		field, ok := jsonFieldByName(current, segment)
		if !ok {
			return false
		}
		current = field
	}

	return hasConfiguredValue(current)
}

func jsonFieldByName(value reflect.Value, jsonName string) (reflect.Value, bool) {
	if value.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}

	typ := value.Type()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag != jsonName {
			continue
		}
		return value.Field(index), true
	}

	return reflect.Value{}, false
}

func hasConfiguredValue(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}

	switch value.Kind() {
	case reflect.Pointer:
		return !value.IsNil()
	case reflect.String:
		return strings.TrimSpace(value.String()) != ""
	case reflect.Bool:
		return value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return value.Float() != 0
	case reflect.Map, reflect.Slice, reflect.Array:
		return value.Len() > 0
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if hasConfiguredValue(value.Field(index)) {
				return true
			}
		}
		return false
	case reflect.Interface:
		if value.IsNil() {
			return false
		}
		return hasConfiguredValue(value.Elem())
	default:
		return !value.IsZero()
	}
}
