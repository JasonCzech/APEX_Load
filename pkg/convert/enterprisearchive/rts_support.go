package enterprisearchive

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
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
	// keyPredicate matches keys that no fixed alias can express, such as the
	// numbered Java_VM_Params_extra_arg_N history rows VuGen keeps for a
	// drop-down field. It is consulted after keyAliases.
	keyPredicate func(string) bool
}

type runtimeSettingSupportDefinition struct {
	entry    RuntimeSettingSupportEntry
	matchers []runtimeSettingMatcher
}

var sourceRTSSupportDefinitions = append(append(nativeArchiveRuntimeDefinitions(), replayPolicyDefinitions()...), []runtimeSettingSupportDefinition{
	{entry: RuntimeSettingSupportEntry{ID: "http_page_timeout", Area: "HTTP", Concept: "Complete page deadline", Fields: []string{"config.http.page_timeout_ms"}, Status: RuntimeSettingStatusExecuted, Notes: "Applies one deadline to the main response and all embedded-resource waves; seconds are converted to milliseconds."}, matchers: []runtimeSettingMatcher{matcher([]string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web"}, []string{"PageDownloadTimeout"})}},
	{entry: RuntimeSettingSupportEntry{ID: "http_ip_version", Area: "HTTP", Concept: "Outgoing IP family", Fields: []string{"config.http.ip_version"}, Status: RuntimeSettingStatusExecuted, Notes: "IPv4, IPv6, or automatic selection applies to outgoing TCP connections, including proxy connections; unverified source encodings require an explicit choice."}, matchers: []runtimeSettingMatcher{matcher([]string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web"}, []string{"IPVersionPolicy"})}},
	{entry: RuntimeSettingSupportEntry{ID: "http_cache_eviction", Area: "HTTP", Concept: "Unused cache entry lifetime", Fields: []string{"config.http.cache_max_unused_iterations"}, Status: RuntimeSettingStatusExecuted, Notes: "Evicts entries after the configured number of unreferenced main iterations; zero disables aging. Memory bounds remain enforced."}, matchers: []runtimeSettingMatcher{matcher([]string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web"}, []string{"DeleteCacheUnreferencedIterations"})}},
	{entry: RuntimeSettingSupportEntry{ID: "http_html_parse", Area: "HTTP", Concept: "HTML response parsing policy", Fields: []string{"config.http.html_parse_mode"}, Status: RuntimeSettingStatusExecuted, Notes: "Controls HTML resource discovery: HTML, text except text/xml, or any content type."}, matchers: []runtimeSettingMatcher{matcher([]string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web"}, []string{"ParseHtmlContentType"})}},
	{
		entry: RuntimeSettingSupportEntry{
			ID: "http_idle_timeout", Area: "HTTP", Concept: "Keep-alive idle timeout",
			Fields: []string{"config.http.idle_timeout_ms"}, Status: RuntimeSettingStatusExecuted,
			Notes: "KeepAliveTimeout seconds map to HTTP idle_timeout_ms for positive whole values up to 3600 seconds. Inactive values are preserved; unsupported values require review without clamping.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web"}, []string{"KeepAliveTimeout"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "http_transport",
			Area:    "HTTP",
			Concept: "Timeouts, redirects, and connection reuse",
			Fields:  []string{"config.http.version", "config.http.connect_timeout_ms", "config.http.request_timeout_ms", "config.http.max_redirects", "config.http.keep_alive", "config.http.max_conns_per_host"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "APEX Load maps core Web HTTP/HTML transport settings. HTTP/1.1 and explicit HTTP/2 are exact; EnableHTTP2 uses automatic H2-preferred negotiation with HTTP/1.1 fallback. HTTP/1.0 is retained only as a blocked review draft.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web"}, []string{"ConnectTimeout", "ReceiveTimeout", "StepDownloadTimeout", "HTTPVersion", "HTTPVer", "HttpVer", "EnableHTTP2", "EnableHttp2", "HTTP2Enabled", "KeepAlive", "MaxConnectionsPerHost", "MaxConnections", "MaxConnectionsPerHostHTTP", "MaxRedirections", "MaxRedirectionDepth"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "proxy",
			Area:    "Proxy",
			Concept: "Proxy mode and custom proxy",
			Fields:  []string{"config.http.proxy_mode", "config.http.proxy"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Supports none, environment-system, custom and bounded PAC proxy modes. Desktop browser inheritance requires an explicit portable export.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Proxy", "WEB", "Web"}, []string{"Mode", "ProxyMode", "Type", "ProxyServer", "ProxyURL", "Server", "ProxyHTTPHost", "ProxyHTTPPort", "ProxyHTTPSHost", "ProxyHTTPSPort", "ProxyUseBrowser", "ProxyUseAutoConfigScript", "ProxyAutoConfigScriptURL", "ProxyPasswordIsEncrypted", "ProxyUseProxy", "ProxyUseProxyServer", "ProxyUseSame", "ProxyBypass", "ProxyNoLocal", "ProxyUserName", "UserName", "ProxyUser", "ProxyPassword", "Password"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "request_headers",
			Area:    "Browser Emulation",
			Concept: "User agent and default accept headers",
			Fields:  []string{"config.browser_emulation.custom_user_agent", "config.browser_emulation.language", "config.http.accept_language", "config.http.accept_encoding"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Imported user agent and accept headers execute on HTTP replay via config.browser_emulation and the HTTP default headers.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"UserAgent", "CustomUserAgent", "BrowserUserAgent", "UseCustomAgent", "UseCustomUserAgent", "AcceptLanguage", "BrowserAcceptLanguage", "BrowserLanguageCode", "BrowserLanguage", "LanguageCode", "NavigatorBrowserLanguage", "NavigatorUserLanguage", "AcceptEncoding", "BrowserAcceptEncoding"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_selection",
			Area:    "Browser Emulation",
			Concept: "Specific browser selection (type, version, platform)",
			Fields:  []string{"config.browser_emulation.browser_type", "config.browser_emulation.browser_version", "config.browser_emulation.platform"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "The selected browser resolves to a catalog User-Agent that HTTP replay presents on every request.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"BrowserType", "Browser_Type", "UserBrowserType", "BrowserVer", "BrowserVersion", "UserBrowserVersion", "BrowserPlatform", "Platform"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "download_non_html_resources",
			Area:    "Browser Emulation",
			Concept: "Download non-HTML resources",
			Fields:  []string{"config.browser_emulation.cache.download_non_html_resources"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP replay parses HTML responses and fetches embedded resources with browser-like bounded concurrency, feeding the simulated cache.",
		},
		matchers: []runtimeSettingMatcher{
			matcherWithValues([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"SearchForImages", "DownloadNonHtmlResources"}, []string{"1", "0", "yes", "no", "true", "false", "on", "off", "enabled", "disabled"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "simulate_prefetch_prerender",
			Area:    "Browser Emulation",
			Concept: "Simulate browser prefetch and prerender",
			Fields:  []string{"config.browser_emulation.cache.simulate_prefetch"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP replay fetches link rel=prefetch/prerender/preload targets after page resources.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"SimulatePrefetchPrerender", "SimulatePrefetch", "Prefetch"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "tls",
			Area:    "Security",
			Concept: "TLS or SSL version",
			Fields:  []string{"config.http.tls.min_version", "config.http.tls.max_version"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "APEX Load maps recognized TLS versions exactly; legacy SSL selections are replaced with TLS 1.3 with a migration warning. Other unsupported values retain APEX defaults and require review.",
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
			Notes:   "Imported username and password values map into APEX Load basic auth configuration.",
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
			Notes:   "Applied by the engine iteration loop. Like LoadRunner Controller, a duration-driven scenario schedule takes precedence: the compiled run drops a script-only per-VUser cap so groups keep loading for the whole schedule, and the imported value applies when the scenario or group sets iterations explicitly.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Run Logic", "RunLogic", "Iterations"}, []string{"Iterations", "NumberOfIterations", "NumOfIterations"}),
			matcher([]string{"RunLogicRunRoot"}, []string{"RunLogicNumOfIterations"}),
		},
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
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Run Logic", "RunLogic"}, []string{"AutoTransactions"}),
			matcher([]string{"General"}, []string{"AutoTransactions", "AutomaticTransactions"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "run_logic_block_scheduling",
			Area:    "Run Logic",
			Concept: "Block scheduling",
			Fields:  []string{"config.run_logic.block_scheduling"},
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes:   "Preserved for migration evidence, but the current engine does not consume block_scheduling and executes the imported action list in its fixed order. Include compatibility_only in the migration block policy until source block scheduling has been reviewed or replaced.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Run Logic", "RunLogic"}, []string{"BlockScheduling"}),
			matcher([]string{"RunLogicRunRoot"}, []string{"RunLogicRunMode"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "run_logic_action_sequence",
			Area:    "Run Logic",
			Concept: "Action sequence",
			Fields:  []string{"config.run_logic.action_sequence"},
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes:   "Preserved for migration evidence, but the current engine does not consume action_sequence: it runs init, the imported action list, and teardown in its fixed lifecycle. Review source omissions, reordering, or custom action order before execution.",
		},
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Run Logic", "RunLogic"}, []string{"ActionSequence", "Sequence"}),
			matcher([]string{"RunLogicRunRoot"}, []string{"RunLogicActionOrder"}),
			matcher([]string{"Profile Actions"}, []string{"Profile Actions name"}),
		},
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
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Pacing"}, []string{"Mode", "PacingMode", "DelayMs", "Delay", "MinDelayMs", "MinDelay", "MaxDelayMs", "MaxDelay", "IntervalMs", "Interval", "MinIntervalMs", "MinInterval", "MaxIntervalMs", "MaxInterval"}),
			matcher([]string{"Iterations"}, []string{"IterationPace", "StartEvery", "RandomMin", "RandomMax"}),
			matcher([]string{"RunLogicRunRoot"}, []string{"RunLogicPaceType", "RunLogicPaceConstTime", "RunLogicRandomPaceMin", "RunLogicRandomPaceMax", "RunLogicPaceConstAfterTime", "RunLogicAfterPaceMin", "RunLogicAfterPaceMax"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "think_time",
			Area:    "Think Time",
			Concept: "Ignore, recorded percentage range, multiplier, and limit",
			Fields:  []string{"config.think_time"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Recorded delays, random percentages of each recorded delay, multiplier, cap, and ignore execute after actions.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Think Time", "ThinkTime"}, []string{"Options", "Factor", "Limit", "LimitFlag", "ThinkTimeRandomLow", "ThinkTimeRandomHigh", "Ignore", "IgnoreThinkTime", "Multiplier", "MultiplyRecordedThinkTimeBy", "LimitMs", "LimitThinkTimeMs"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "logging",
			Area:    "Logging",
			Concept: "Log level, error-only, and extended logging options",
			Fields:  []string{"config.log"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "APEX Load executes imported log level, error-only, parameter-substitution, data-returned, and advanced-trace controls for its supported trace envelope.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Log", "Logging"}, []string{"Level", "LogLevel", "LogOptions", "OnErrorOnly", "LogOnErrorOnly", "AutoLog", "ExtendedOptions", "ExtendedLogOptions", "ParameterSubstitution", "DataReturned", "AdvancedTrace", "MsgClassParameters", "MsgClassData", "MsgClassFull"})},
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
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Misc", "Miscellaneous"}, []string{"ContinueOnError", "ErrorContinue"}),
			matcher([]string{"General"}, []string{"ContinueOnError"}),
		},
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
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Misc", "Miscellaneous"}, []string{"SnapshotOnError", "GenerateSnapshotOnError"}),
			matcher([]string{"WEB", "Web"}, []string{"SnapshotOnErrorActive"}),
		},
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
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"Engine", "BrowserEngine", "DeviceProfile", "MobileProfile", "EmulatedDevice", "TouchMode", "TouchEnabled", "Orientation", "ClientMetrics", "CollectClientMetrics", "SlowMoMs", "SlowMotionMs", "ViewportWidth", "ScreenWidth", "ViewportHeight", "ScreenHeight"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_cache_enabled",
			Area:    "Browser Emulation",
			Concept: "Simulate browser cache",
			Fields:  []string{"config.browser_emulation.cache.simulate_cache", "config.browser.cache_enabled"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP replay toggles the simulated response cache; the browser runtime toggles its own cache usage.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"CacheEnabled", "SimulateCache"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_cache_content_urls",
			Area:    "Browser Emulation",
			Concept: "Cache URLs requiring content",
			Fields:  []string{"config.browser_emulation.cache.cache_content_urls", "config.browser.cache_content_urls"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP replay restricts cache storage to content-bearing (HTML) responses when enabled.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"CacheContentURLs", "CacheURLsRequiringContent", "CacheHtmlPages"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_check_newer_versions",
			Area:    "Browser Emulation",
			Concept: "Check for newer versions on every visit",
			Fields:  []string{"config.browser_emulation.cache.check_newer_versions", "config.browser.check_newer_versions"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP replay revalidates fresh cache entries with conditional requests on every visit; the browser runtime injects revalidation headers.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"CheckNewerVersions", "CheckForNewerPages", "CheckNewerVersion", "CacheAlwaysCheckForNewerPages"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_new_user_per_iteration",
			Area:    "Browser Emulation",
			Concept: "Simulate a new user on each iteration",
			Fields:  []string{"config.browser_emulation.cache.new_user_each_iteration", "config.browser.new_user_per_iteration"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP replay clears the simulated cache and cookie jar at iteration start; the browser protocol is reset between iterations.",
		},
		// ClearCacheForSimulateNewUser is deliberately NOT a matcher: stock VuGen
		// cfgs carry it unconditionally, and the importer never applies it on its
		// own (raw-preserved only), so matching it would overclaim "executed".
		matchers: []runtimeSettingMatcher{matcher([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"NewUserPerIteration", "NewUserEachIteration", "SimulateNewUser"})},
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
		matchers: []runtimeSettingMatcher{matcherWithValues([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"DownloadResources", "DownloadNonHtmlResources"}, []string{"all"})},
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
		matchers: []runtimeSettingMatcher{matcherWithValues([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"DownloadResources", "DownloadNonHtmlResources"}, []string{"none"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "browser_download_resources_recorded",
			Area:    "Browser Emulation",
			Concept: "Download resources = recorded",
			Fields:  []string{"config.browser.download_resources"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Recorded-only assets execute for imported or recorded browser flows that carry the required recorded-resource URL metadata.",
		},
		matchers: []runtimeSettingMatcher{matcherWithValues([]string{"Browser Emulation", "Browser", "WEB", "Web"}, []string{"DownloadResources", "DownloadNonHtmlResources"}, []string{"recorded"})},
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
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Download Filters", "DownloadFilters"}, []string{"Mode", "FilterMode", "Hosts", "IncludeHosts", "ExcludeHosts"}),
			matcher([]string{"FILTERS"}, []string{"IncludeFiltersInList", "ExcludeFiltersInList"}),
		},
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
		matchers: []runtimeSettingMatcher{matcher([]string{"Content Check", "ContentCheck"}, []string{"Text", "SearchText", "LeftBoundary", "RightBoundary", "Scope", "FailIfFound"})},
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
		matchers: []runtimeSettingMatcher{
			matcher([]string{"Network Speed Simulation", "NetworkSimulation"}, []string{"Mode", "SimulationMode", "Preset", "BandwidthKbps", "Bandwidth", "LatencyMs"}),
			matcher([]string{"ModemSpeed"}, []string{"EnableModemSpeed", "EnableCustomModemSpeed", "ModemSpeed", "CustomModemSpeed"}),
		},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "streaming",
			Area:    "Streaming",
			Concept: "Adaptive or fixed playback settings",
			Fields:  []string{"config.web_runtime.streaming"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP runtime applies streaming buffer and bitrate pacing to media-like responses.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Streaming"}, []string{"Mode", "BufferSizeSeconds", "BufferSize", "FixedBitrateKbps", "FixedBitrate", "BandwidthEstimation"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "data_formatting_auto_detect",
			Area:    "Data Formatting",
			Concept: "Auto-detect DFEs",
			Fields:  []string{"config.web_runtime.data_formatting.auto_detect"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP runtime auto-detects the supported DFE envelope: obvious base64 text and gzip-compressed text payloads before extraction and assertions.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Data Formatting", "DataFormatting"}, []string{"AutoDetect", "DFEAutoDetect"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "data_formatting_chain",
			Area:    "Data Formatting",
			Concept: "DFE transform chain",
			Fields:  []string{"config.web_runtime.data_formatting.chain"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "HTTP runtime executes the supported DFE transform chain: base64, gzip, JSON/XML, URL, and HTML encode/decode transforms.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"Data Formatting", "DataFormatting"}, []string{"Chain", "DFEChain"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "sapgui_runtime",
			Area:    "SAP GUI",
			Concept: "SAP GUI runtime and session options",
			Fields:  []string{"config.sapgui"},
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes:   "SAP GUI runtime settings are preserved in the DSL draft; execution requires the Windows SAP GUI runner bridge.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"SAPGUI", "SAP GUI"}, []string{"ConnectionString", "Client", "Language", "SessionBehavior", "SyncTimeoutMs", "ScriptingEnabled"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "sapgui_auto_logon",
			Area:    "SAP GUI",
			Concept: "Auto logon credentials",
			Fields:  []string{"config.sapgui.auto_logon", "config.sapgui.username", "config.sapgui.password"},
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes:   "SAP GUI credential settings are preserved with password secret templates; runner-side secret provisioning is required.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"SAPGUI", "SAP GUI", "AutoLogon"}, []string{"AutoLogon", "User", "Username", "Password", "Client", "Language"})},
	},
}...)

var javaVuserRTSSupportDefinitions = []runtimeSettingSupportDefinition{
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "java_vm_jdk",
			Area:    "Java VM",
			Concept: "Internal or specified JDK selection",
			Fields:  []string{"config.java_vuser.jdk_selection", "config.java_vuser.jdk_home"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "The governed Java Vuser runner resolves an internal JDK or validates the specified JDK home on each assigned runner; specified homes do not silently fall back.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options", "Java"}, []string{"use_jdk", "UseJdk", "jdk_selection", "UseSpecifiedJdk", "UseExternalJVM", "jdk_home", "JdkPath", "JavaVmJdkPath", "UserJdkPath", "JdkHome", "ExternalJREPath"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "java_vm_parameters",
			Area:    "Java VM",
			Concept: "Additional JVM parameters",
			Fields:  []string{"config.java_vuser.additional_vm_parameters"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "The active Java_VM_Params value is imported with quote-aware tokenization and executes as exact argv tokens after structural safety validation; no shell is involved.",
		},
		matchers: []runtimeSettingMatcher{matcher(
			[]string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"},
			javaVMParameterKeyAliases,
		)},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "java_vm_parameter_history",
			Area:    "Java VM",
			Concept: "Earlier Additional VM Parameters values",
			Status:  RuntimeSettingStatusNonApplicable,
			Notes:   "VuGen keeps the field's earlier values as numbered Java_VM_Params_extra_arg_N rows for its drop-down; LoadRunner never adds them to the command line, so they are not applied.",
		},
		matchers: []runtimeSettingMatcher{matcherWithKeyPredicate(
			[]string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"},
			nil,
			func(key string) bool {
				_, ok := javaVMParameterExtraArgIndex(key)
				return ok
			},
		)},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "java_vm_classpath",
			Area:    "Java VM",
			Concept: "Precompiled Java classpath",
			Fields:  []string{"config.java_vuser.classpath"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Imported entries preserve source order and reference precompiled classes or JARs that must already exist on every governed runner.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"}, []string{"Java_Env_ClassPath", "java_env_class_paths"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "java_vm_dedicated_class_loader",
			Area:    "Java VM",
			Concept: "Dedicated class loader",
			Fields:  []string{"config.java_vuser.dedicated_class_loader"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "Local and sidecar execution create one child URLClassLoader and Actions object per VUser session, retain them across init, action iterations, and end, then close them at teardown.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"}, []string{"enable_CL_per_vuser", "enable_classloader_per_vuser", "DedicatedClassLoader", "ClassLoaderPerVuser"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "java_vm_xbootclasspath",
			Area:    "Java VM",
			Concept: "Use -Xbootclasspath parameters",
			Fields:  []string{"config.java_vuser.use_xbootclasspath"},
			Status:  RuntimeSettingStatusCompatibilityOnly,
			Notes:   "The setting is preserved for migration evidence; supported modern JDKs execute the same entries through the ordinary classpath and emit an explicit fallback warning rather than an Xboot flag.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"}, []string{"Use_Xboot", "use_xboot", "UseXbootClasspath"})},
	},
	{
		entry: RuntimeSettingSupportEntry{
			ID:      "java_vm_skip_parameter_evaluation",
			Area:    "Java VM",
			Concept: "Skip automatic parameter evaluation",
			Fields:  []string{"config.java_vuser.skip_parameter_evaluation"},
			Status:  RuntimeSettingStatusExecuted,
			Notes:   "When enabled, the engine suppresses the automatic parameter snapshot (data-source rows and named parameters) before dispatch; only explicitly authored lr_parameters reach the JVM and the per-invocation each_occurrence cursor does not advance. Per-iteration data-row refresh still runs but those values are not forwarded.",
		},
		matchers: []runtimeSettingMatcher{matcher([]string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"}, []string{"skip_param_eval", "Skip_Param_Eval", "SkipParamEval", "skip_automatic_parameter_evaluation", "SkipAutomaticParameterEvaluation", "skip_parameter_evaluation"})},
	},
}

var sourceRTSSupportMatrixNotes = []string{
	"download_resources = recorded is executed for import/recording flows that preserve recorded-resource URL metadata; manually authored scripts should supply equivalent metadata before selecting that mode.",
	"Streaming settings execute in the HTTP runtime for media-like responses through buffer and bitrate pacing.",
	"DFE parity is complete for the supported HTTP response transform envelope: base64, gzip, JSON/XML, URL, and HTML transforms plus conservative base64/gzip auto-detection.",
	"Extended logging parity is complete for APEX Load's trace envelope: log level, error-only logging, parameter substitution, returned data, and advanced trace metadata.",
}

var sourceRTSSupportMatrixExamples = []RuntimeSettingSupportExample{
	{
		Title:    "Extended Web HTTP/HTML runtime import",
		Coverage: "Parses transport, proxy, pacing, think time, logging, browser emulation, download filters, content checks, network shaping, streaming, and DFE settings into APEX Load config.",
		Test:     "pkg/convert/enterprisearchive/converter_test.go::TestParseRuntimeCfgToScriptConfigMapsExtendedRuntimeSettings",
	},
	{
		Title:    "ZIP import parity metadata",
		Coverage: "Preserves runtime files and emits per-import runtime setting parity classifications with status counts for review workflows.",
		Test:     "pkg/convert/enterprisearchive/converter_test.go::TestConvertZipMapsExtendedRuntimeSettings",
	},
	{
		Title:    "Import envelope parity review",
		Coverage: "Surfaces runtime setting parity in the shared enterprise script archive import response, including summary counts and review sections.",
		Test:     "pkg/api/handlers_scripts_test.go::TestHandleImportSourceSummarizesRuntimeSettingParity",
	},
	{
		Title:    "Golden Web HTTP/HTML fixture",
		Coverage: "Converts a representative Web HTTP/HTML enterprise script archive with transport, proxy, auth, browser emulation, recorded resources, logging, streaming, DFE, and network settings, then asserts side-by-side runtime support statuses.",
		Test:     "pkg/convert/enterprisearchive/converter_test.go::TestGoldenWebHTTPHTMLFixtureRuntimeParity",
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
	entries := make([]RuntimeSettingSupportEntry, 0, len(sourceRTSSupportDefinitions))
	for _, definition := range sourceRTSSupportDefinitions {
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

// GetJavaVuserRTSSupportMatrix is intentionally separate from the Web
// HTTP/HTML source matrix so API clients never advertise Java-only settings as
// Web runtime capabilities.
func GetJavaVuserRTSSupportMatrix() []RuntimeSettingSupportEntry {
	entries := make([]RuntimeSettingSupportEntry, 0, len(javaVuserRTSSupportDefinitions))
	for _, definition := range javaVuserRTSSupportDefinitions {
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
	return cloneStrings(sourceRTSSupportMatrixNotes)
}

func GetRTSSupportMatrixExamples() []RuntimeSettingSupportExample {
	if len(sourceRTSSupportMatrixExamples) == 0 {
		return nil
	}
	examples := make([]RuntimeSettingSupportExample, 0, len(sourceRTSSupportMatrixExamples))
	for _, example := range sourceRTSSupportMatrixExamples {
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
	return buildRTSParityReport(runtimeSections, sourceRTSSupportDefinitions)
}

func buildRTSParityReportForProtocol(runtimeSections map[string]map[string]map[string]string, protocol string) []RuntimeSettingParityEntry {
	definitions := sourceRTSSupportDefinitions
	if strings.EqualFold(strings.TrimSpace(protocol), "java_vuser") {
		definitions = allImportedRuntimeSettingSupportDefinitions()
	}
	entries := scopeHTTPCompatibilityParity(buildRTSParityReport(runtimeSections, definitions), protocol)
	if nativeHTTPCompatibilityProtocol(protocol) && runtimeDeviceRowsAreRecordedScreens(runtimeSections) {
		for i := range entries {
			if entries[i].ID == "browser_device_emulation" {
				entries[i].Status = RuntimeSettingStatusNonApplicable
				entries[i].Notes = "Only recorded ScreenWidth/ScreenHeight rows are present. Native HTTP replay has no browser viewport."
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(protocol), "java_vuser") {
		for _, sections := range runtimeSections {
			think := findMergedSection(sections, "Think Time", "ThinkTime")
			if strings.EqualFold(strings.TrimSpace(runtimeSectionValue(think, "Options")), "RANDOM") {
				entries = externalRunnerPercentageParity(entries)
			}
		}
	}
	return entries
}

func runtimeDeviceRowsAreRecordedScreens(runtimeSections map[string]map[string]map[string]string) bool {
	found := false
	for _, sections := range runtimeSections {
		for section, keys := range sections {
			for key := range keys {
				for _, definition := range sourceRTSSupportDefinitions {
					if definition.entry.ID != "browser_device_emulation" {
						continue
					}
					matches := false
					for _, matcher := range definition.matchers {
						matches = matches || (matcher.matchesSection(section) && matcher.matchesKey(key) && matcher.matchesValue(keys[key]))
					}
					if !matches {
						continue
					}
					if runtimeConfigCanonicalSection(section) != "http" || (!strings.EqualFold(key, "ScreenWidth") && !strings.EqualFold(key, "ScreenHeight")) {
						return false
					}
					found = true
				}
			}
		}
	}
	return found
}

func buildRTSParityReport(runtimeSections map[string]map[string]map[string]string, definitions []runtimeSettingSupportDefinition) []RuntimeSettingParityEntry {
	if len(runtimeSections) == 0 {
		return nil
	}

	entries := make([]RuntimeSettingParityEntry, 0)
	for _, definition := range definitions {
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
	definitions := sourceRTSSupportDefinitions
	if strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
		definitions = allImportedRuntimeSettingSupportDefinitions()
	}
	for _, definition := range definitions {
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

	entries = scopeHTTPCompatibilityParity(entries, script.Protocol)
	if currentSequentialProof(script) {
		promoteSequentialParity(entries)
	}
	if strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") && strings.EqualFold(strings.TrimSpace(script.Config.ThinkTime.Type), "random_percentage") {
		return externalRunnerPercentageParity(entries)
	}
	return entries
}

// The external JVM owns its internal lr.think_time calls. Native DSL wait
// support must not claim that the runner applies this new percentage policy.
func externalRunnerPercentageParity(entries []RuntimeSettingParityEntry) []RuntimeSettingParityEntry {
	for i := range entries {
		if entries[i].ID == "think_time" {
			entries[i].Status = RuntimeSettingStatusCompatibilityOnly
			entries[i].Notes = "Random percentages execute for native DSL waits; the external Java Vuser runner does not apply percentage bounds to its internal think-time calls. Review or replace those waits before replay."
		}
	}
	return entries
}

func allImportedRuntimeSettingSupportDefinitions() []runtimeSettingSupportDefinition {
	definitions := make([]runtimeSettingSupportDefinition, 0, len(sourceRTSSupportDefinitions)+len(javaVuserRTSSupportDefinitions))
	definitions = append(definitions, sourceRTSSupportDefinitions...)
	definitions = append(definitions, javaVuserRTSSupportDefinitions...)
	return definitions
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
			if normalized := normalizeRuntimeArchivePath(fileName); normalized != "" {
				files = append(files, normalized)
			}
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
		if len(matcher.keyAliases) == 0 && matcher.keyPredicate == nil {
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
	if len(matcher.keyAliases) == 0 && matcher.keyPredicate == nil {
		return true
	}
	for _, alias := range matcher.keyAliases {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(alias)) {
			return true
		}
	}
	return matcher.keyPredicate != nil && matcher.keyPredicate(strings.TrimSpace(key))
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

func matcherWithKeyPredicate(sectionAliases []string, keyAliases []string, keyPredicate func(string) bool) runtimeSettingMatcher {
	return runtimeSettingMatcher{sectionAliases: sectionAliases, keyAliases: keyAliases, keyPredicate: keyPredicate}
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
		return config.ThinkTime.Ignore || config.ThinkTime.Multiplier != 0 || config.ThinkTime.LimitMs != 0 || strings.EqualFold(config.ThinkTime.Type, "random_percentage")
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
		return hasCompatibilityOnlyAdditionalAttributes(config)
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

func hasCompatibilityOnlyAdditionalAttributes(config dsl.ScriptConfig) bool {
	for key, value := range config.WebRuntime.AdditionalAttributes {
		if runtimeAdditionalAttributeIsNativelyMapped(config, key, value) {
			continue
		}
		return true
	}
	return false
}

func runtimeAdditionalAttributeIsNativelyMapped(config dsl.ScriptConfig, key, value string) bool {
	switch normalizeRuntimeAttributeKey(key) {
	case "web_set_sockets_option.ssl_version", "web_set_sockets_option.tls_version":
		mapped, exact := mapSSLVersionExact(value)
		return exact && tlsVersionsEquivalent(config.HTTP.TLS.MinVersion, mapped) &&
			tlsVersionsEquivalent(config.HTTP.TLS.MaxVersion, mapped)
	case "web_set_sockets_option.close_keepalive_connections", "web_set_sockets_option.close_keep_alive_connections":
		closeConnections, ok := parseSourceBool(value)
		return ok && closeConnections && config.HTTP.KeepAlive != nil && !*config.HTTP.KeepAlive
	case "web_set_sockets_option.max_connections_per_host", "web_set_sockets_option.max_connections":
		maxConnections := parseInt(value)
		return maxConnections > 0 && config.HTTP.MaxConnsPerHost == maxConnections
	case "web_set_timeout.connect":
		timeoutMs, ok := parseSourceTimeoutMs(value)
		return ok && config.HTTP.ConnectTimeoutMs == timeoutMs
	case "web_set_timeout.receive", "web_set_timeout.response", "web_set_timeout.download", "web_set_timeout.step", "web_set_timeout.step_download", "web_set_timeout.stepdownload":
		timeoutMs, ok := parseSourceTimeoutMs(value)
		return ok && config.HTTP.RequestTimeoutMs == timeoutMs
	case "web_set_timeout.ssl", "web_set_timeout.tls", "web_set_timeout.secure":
		timeoutMs, ok := parseSourceTimeoutMs(value)
		return ok && config.HTTP.TLSTimeoutMs == timeoutMs
	case "web_cache_cleanup":
		// The importer emits a positional source_runtime action which dispatches
		// through protocol.CacheController. The compatibility attribute is kept
		// only as source provenance.
		return true
	case "web_set_max_html_param_len":
		// APEX Load imposes no captured-parameter length cap (full-value capture,
		// a superset of the source limit), so this control has no executable
		// equivalent and is treated as a native no-op rather than a parity gap.
		return true
	default:
		return false
	}
}

func normalizeRuntimeAttributeKey(key string) string {
	segments := strings.Split(strings.TrimSpace(key), ".")
	for index, segment := range segments {
		segments[index] = normalizeEnum(segment)
	}
	return strings.Join(segments, ".")
}

func tlsVersionsEquivalent(left, right string) bool {
	left = normalizeTLSVersion(left)
	right = normalizeTLSVersion(right)
	return left != "" && right != "" && left == right
}

func normalizeTLSVersion(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.TrimPrefix(normalized, "tls")
	switch normalized {
	case "1.0", "10":
		return "1.0"
	case "1.1", "11":
		return "1.1"
	case "1.2", "12":
		return "1.2"
	case "1.3", "13":
		return "1.3"
	default:
		return ""
	}
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
