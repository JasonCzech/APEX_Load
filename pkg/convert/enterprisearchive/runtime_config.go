package enterprisearchive

// runtime_config.go — split from converter.go (same package, behavior-preserving).

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// RuntimeConfig holds parsed source runtime settings from a .cfg file.
type RuntimeConfig struct {
	ConnectTimeoutSec int
	ReceiveTimeoutSec int
	StepTimeoutSec    int
	HTTPVersion       string // source spelling; native projection supports exact "1.1" and "2"
	KeepAlive         bool
	MaxConnsPerHost   int
	ProxyMode         string
	ProxyURL          string
	ProxyUser         string
	ProxyPass         string
	// proxyReviewUser/proxyReviewPass retain source credentials only when an
	// enabled proxy selection cannot be represented safely. They are projected
	// into config.http.proxy beside proxy_mode=none, so the destination keeps
	// the operator-owned values without allowing the HTTP transport to use
	// them. They are cleared after final credential projection.
	proxyReviewUser                  string
	proxyReviewPass                  string
	ProxyNoProxy                     []string
	UserAgent                        string
	AcceptLanguage                   string
	AcceptEncoding                   string
	UseGzip                          bool
	EnableHTTP2                      *bool
	EnableHTTP2Raw                   string
	SSLVersion                       string
	AuthUser                         string
	AuthPass                         string
	MaxRedirects                     int
	Iterations                       int
	AutoTransactions                 string
	BlockScheduling                  string
	ActionSequence                   []string
	PacingType                       string
	DelayMs                          int
	MinDelayMs                       int
	MaxDelayMs                       int
	IntervalMs                       int
	MinIntervalMs                    int
	MaxIntervalMs                    int
	ThinkTimeIgnore                  bool
	ThinkTimeType                    string
	ThinkTimeMultiplier              float64
	ThinkTimeLimitMs                 float64
	RuntimeFiles                     []string
	RuntimeIssues                    []RuntimeMigrationIssue
	LogLevel                         string
	OnErrorOnly                      bool
	LogExtendedOptions               []string
	ContinueOnError                  bool
	SnapshotOnError                  bool
	CacheEnabled                     bool
	CacheContentURLs                 bool
	CheckNewerVersions               bool
	NewUserPerIteration              bool
	BrowserEngine                    string
	BrowserDeviceProfile             string
	BrowserTouchMode                 bool
	BrowserOrientation               string
	BrowserClientMetrics             bool
	BrowserSlowMoMs                  int
	BrowserViewportWidth             int
	BrowserViewportHeight            int
	DownloadResources                string
	AdditionalAttributes             map[string]string
	DownloadFilterMode               string
	DownloadFilterHosts              []string
	ContentChecks                    []dsl.ContentCheckConfig
	NetworkSimulationMode            string
	NetworkSimulationPreset          string
	NetworkBandwidthKbps             int
	NetworkLatencyMs                 int
	StreamingMode                    string
	StreamingBufferSizeSeconds       int
	StreamingFixedBitrateKbps        int
	StreamingBandwidthEstimation     string
	DataFormattingAutoDetect         bool
	DataFormattingChain              []dsl.DFETransform
	JavaVMJDKSelection               string
	JavaVMJDKSelectionRaw            string
	JavaVMJDKSelectionParseError     string
	JavaVMJDKHome                    string
	JavaVMAdditionalParameters       []string
	JavaVMParametersRaw              string
	JavaVMParameterRows              []string // verbatim source rows, in command-line order; see orderedJavaVMParameterRows
	JavaVMParametersParseError       string
	javaVMParameterSourceRows        []string
	javaVMParameterRejections        []javaVMParameterRejection
	javaVMParameterExposureReviews   []javaVMParameterRejection
	JavaVMUseXBootClasspath          *bool
	JavaVMUseXBootClasspathRaw       string
	JavaVMUseXBootParseError         string
	JavaVMDedicatedClassLoader       *bool
	JavaVMDedicatedClassLoaderRaw    string
	JavaVMDedicatedParseError        string
	JavaVMClasspath                  []string
	JavaVMClasspathRaw               string
	JavaVMClasspathParseError        string
	JavaVMSkipParameterEvaluation    *bool
	JavaVMSkipParameterEvaluationRaw string
	JavaVMSkipParamEvalParseError    string

	// Browser Emulation (config.browser_emulation) — parsed from real
	// LoadRunner [WEB] keys plus the APEX-native aliases. Raw values are
	// preserved for round-trip review; ParseError fields surface malformed
	// values instead of silently defaulting.
	BEBrowserType                string // catalog type (chrome|edge|firefox|safari|ie); "" when unmapped
	BEBrowserTypeRaw             string // source value, preserved even when unmapped
	BEBrowserVersion             string
	BEPlatform                   string
	BEPlatformRaw                string
	BELanguage                   string // explicit language code key only (BrowserLanguageCode)
	BEUseCustomUA                *bool
	BEUseCustomUARaw             string
	BEUseCustomUAParseError      string
	BESimulateCache              *bool
	BESimulateCacheRaw           string
	BESimulateCacheParseError    string
	BECacheContentURLs           *bool
	BECacheContentURLsRaw        string
	BECacheContentURLsParseError string
	BECheckNewerVersions         *bool
	BECheckNewerVersionsRaw      string
	BECheckNewerParseError       string
	BENewUserEachIteration       *bool
	BENewUserEachIterationRaw    string
	BENewUserParseError          string
	BEDownloadNonHTML            *bool
	BEDownloadNonHTMLRaw         string
	BEDownloadNonHTMLParseError  string
	BESimulatePrefetch           *bool
	BESimulatePrefetchRaw        string
	BESimulatePrefetchParseError string
	// BEClearCacheEachIterationRaw preserves LR's ClearCacheForSimulateNewUser
	// child key. Stock VuGen cfgs carry it unconditionally, so it is never
	// applied on its own — new_user_each_iteration comes from SimulateNewUser.
	BEClearCacheEachIterationRaw string
}

// ParseRuntimeCfg parses a source default.cfg / runtime settings .cfg file.
func ParseRuntimeCfg(content string) *RuntimeConfig {
	rc := &RuntimeConfig{
		KeepAlive:            true,
		HTTPVersion:          "1.1",
		MaxRedirects:         10,
		AdditionalAttributes: make(map[string]string),
	}

	sections := ParseConfigSections(content)
	httpSection := findMergedSection(sections, "HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web")
	webHTTPSection := findSection(sections, "WEB", "Web")
	rc.ConnectTimeoutSec = parseInt(runtimeSectionValue(httpSection, "ConnectTimeout"))
	rc.ReceiveTimeoutSec = parseInt(runtimeSectionValue(httpSection, "ReceiveTimeout"))
	rc.StepTimeoutSec = parseInt(runtimeSectionValue(httpSection, "StepDownloadTimeout"))
	if version := strings.TrimSpace(runtimeSectionFirstNonBlank(httpSection, "HTTPVersion", "HTTPVer", "HttpVer")); version != "" {
		rc.HTTPVersion = version
	}
	rawHTTP2, hasHTTP2 := sectionValueCaseInsensitive(httpSection, "EnableHTTP2", "EnableHttp2", "HTTP2Enabled")
	if !hasHTTP2 {
		rawHTTP2, hasHTTP2 = sectionValueCaseInsensitive(webHTTPSection, "EnableHTTP2", "EnableHttp2", "HTTP2Enabled")
	}
	if hasHTTP2 && strings.TrimSpace(rawHTTP2) != "" {
		rc.EnableHTTP2Raw = strings.TrimSpace(rawHTTP2)
		if enabled, valid := parseBoolExplicit(rawHTTP2); valid {
			rc.EnableHTTP2 = &enabled
		} else {
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "http_transport", Area: "HTTP",
				sourceKey: "enablehttp2",
				Notes:     fmt.Sprintf("Invalid EnableHTTP2=%q; retaining HTTP version %q and requiring review.", rawHTTP2, rc.HTTPVersion),
			})
		}
	}
	if !httpprotocol.VersionExecutable(rc.HTTPVersion) {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID:        "http_transport",
			Area:      "HTTP",
			Status:    RuntimeSettingStatusBlocked,
			sourceKey: "httpversion",
			Notes:     "The selected source HTTP version cannot be reproduced exactly by the native HTTP runtime; requests are retained as non-executable review drafts.",
		})
	}
	if keepAlive, ok := parseBoolExplicit(runtimeSectionValue(httpSection, "KeepAlive")); ok {
		rc.KeepAlive = keepAlive
	}
	if redirects := parseInt(runtimeSectionValue(httpSection, "MaxRedirections")); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	if redirects := parseInt(runtimeSectionValue(httpSection, "MaxRedirectionDepth")); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	rc.MaxConnsPerHost = firstNonZeroInt(
		runtimeSectionValue(httpSection, "MaxConnectionsPerHost"),
		runtimeSectionValue(httpSection, "MaxConnections"),
		runtimeSectionValue(httpSection, "MaxConnectionsPerHostHTTP"),
	)

	parseRuntimeProxySettings(rc, sections)

	browserSection := findMergedSection(sections, "Browser Emulation", "Browser")
	if len(browserSection) == 0 {
		browserSection = findSection(sections, "WEB", "Web")
	}
	browserWebSection := findSection(sections, "WEB", "Web")
	// rc.UserAgent is sourced inside parseBrowserEmulationRuntimeSettings so it
	// shares the same browser-section→[WEB] fallback as the browser-emulation
	// toggles; sourcing it here from browserSection alone would silently lose a
	// custom UA that lives in [WEB] when an APEX-native [Browser Emulation]
	// section is also present.
	rc.AcceptLanguage = normalizeRuntimeAcceptLanguage(firstNonBlank(
		runtimeSectionFirstNonBlank(browserSection, "AcceptLanguage", "BrowserAcceptLanguage", "NavigatorBrowserLanguage", "NavigatorUserLanguage"),
		runtimeSectionFirstNonBlank(browserWebSection, "AcceptLanguage", "BrowserAcceptLanguage", "NavigatorBrowserLanguage", "NavigatorUserLanguage"),
	))
	acceptEncoding := firstNonBlank(
		runtimeSectionFirstNonBlank(browserSection, "AcceptEncoding", "BrowserAcceptEncoding"),
		runtimeSectionFirstNonBlank(browserWebSection, "AcceptEncoding", "BrowserAcceptEncoding"),
	)
	rc.AcceptEncoding = normalizeRuntimeAcceptEncoding(acceptEncoding)
	rc.UseGzip = strings.Contains(strings.ToLower(acceptEncoding), "gzip")
	rc.CacheEnabled = parseBool(runtimeSectionValue(browserSection, "CacheEnabled"))
	rc.CacheContentURLs = parseBool(runtimeSectionFirstNonBlank(browserSection, "CacheContentURLs", "CacheURLsRequiringContent"))
	rc.CheckNewerVersions = parseBool(runtimeSectionFirstNonBlank(browserSection, "CheckNewerVersions", "CheckForNewerPages"))
	rc.NewUserPerIteration = parseBool(runtimeSectionFirstNonBlank(browserSection, "NewUserPerIteration", "SimulateNewUser"))
	rc.BrowserEngine = normalizeEnum(strings.TrimSpace(runtimeSectionFirstNonBlank(browserSection, "Engine", "BrowserEngine")))
	rc.BrowserDeviceProfile = normalizeEnum(strings.TrimSpace(runtimeSectionFirstNonBlank(browserSection, "DeviceProfile", "MobileProfile", "EmulatedDevice")))
	rc.BrowserTouchMode = parseBool(runtimeSectionFirstNonBlank(browserSection, "TouchMode", "TouchEnabled"))
	rc.BrowserOrientation = normalizeEnum(strings.TrimSpace(runtimeSectionValue(browserSection, "Orientation")))
	rc.BrowserClientMetrics = parseBool(runtimeSectionFirstNonBlank(browserSection, "ClientMetrics", "CollectClientMetrics"))
	rc.BrowserSlowMoMs = firstNonZeroInt(
		runtimeSectionValue(browserSection, "SlowMoMs"),
		runtimeSectionValue(browserSection, "SlowMotionMs"),
	)
	rc.BrowserViewportWidth = firstNonZeroInt(
		runtimeSectionValue(browserSection, "ViewportWidth"),
		runtimeSectionValue(browserSection, "ScreenWidth"),
	)
	rc.BrowserViewportHeight = firstNonZeroInt(
		runtimeSectionValue(browserSection, "ViewportHeight"),
		runtimeSectionValue(browserSection, "ScreenHeight"),
	)
	// DownloadNonHtmlResources in its bool form (1/0/yes/no) feeds the
	// browser_emulation cache flag, not this all/none/recorded enum — passing a
	// bool through here would yield an invalid config.browser.download_resources
	// value like "1".
	rc.DownloadResources = normalizeEnum(strings.TrimSpace(firstNonBlank(
		runtimeSectionValue(browserSection, "DownloadResources"),
		enumDownloadResourcesOnly(runtimeSectionValue(browserSection, "DownloadNonHtmlResources")),
	)))
	parseBrowserEmulationRuntimeSettings(rc, browserSection, browserWebSection)

	sslSection := findMergedSection(sections, "SSL", "Security")
	rc.SSLVersion = strings.TrimSpace(runtimeSectionFirstNonBlank(sslSection, "SSLVersion", "TLSVersion"))
	if rc.SSLVersion != "" {
		if _, ok := mapSSLVersionExact(rc.SSLVersion); !ok {
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "tls", Area: "Security",
				sourceKey: "tlsversion",
				Notes:     fmt.Sprintf("Unsupported SSL/TLS version %q; leaving APEX TLS defaults unchanged and requiring review.", rc.SSLVersion),
			})
		}
	}

	authSection := findSection(sections, "Authentication")
	rc.AuthUser = strings.TrimSpace(runtimeSectionFirstNonBlank(authSection, "UserName", "Username"))
	rc.AuthPass = strings.TrimSpace(runtimeSectionValue(authSection, "Password"))

	runLogicSection := findMergedSection(sections, "Run Logic", "RunLogic")
	generalSection := findSection(sections, "General")
	parseRuntimeCfgIterations(rc, sections)
	rc.AutoTransactions = normalizeEnum(runtimeSectionValue(runLogicSection, "AutoTransactions"))
	if rc.AutoTransactions == "" && parseBool(runtimeSectionFirstNonBlank(generalSection, "AutomaticTransactions", "AutoTransactions")) {
		rc.AutoTransactions = "per_action"
	}
	rc.BlockScheduling = normalizeEnum(runtimeSectionValue(runLogicSection, "BlockScheduling"))
	rc.ActionSequence = splitList(runtimeSectionFirstNonBlank(runLogicSection, "ActionSequence", "Sequence"))

	parseRuntimeCfgPacing(rc, sections)

	thinkTimeSection := findMergedSection(sections, "Think Time", "ThinkTime")
	thinkValue := func(aliases ...string) string {
		for _, alias := range aliases {
			if value, ok := sectionValueCaseInsensitive(thinkTimeSection, alias); ok &&
				strings.TrimSpace(value) != "" {
				return value
			}
		}
		return ""
	}
	optionsRaw := thinkValue("Options")
	thinkMode := strings.ToUpper(strings.TrimSpace(optionsRaw))
	ignoreRaw := strings.TrimSpace(thinkValue("Ignore", "IgnoreThinkTime"))
	legacyIgnore, legacyIgnoreValid := parseBoolExplicit(ignoreRaw)
	if thinkMode == "" && ignoreRaw != "" {
		if legacyIgnoreValid {
			rc.ThinkTimeIgnore = legacyIgnore
		} else {
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("Invalid legacy think-time Ignore=%q; using replay think time and requiring review.", ignoreRaw),
			})
		}
	}
	switch thinkMode {
	case "RECORDED":
		rc.ThinkTimeType = "recorded"
		rc.ThinkTimeIgnore = false
		rc.ThinkTimeMultiplier = 1
	case "MULTIPLY":
		rc.ThinkTimeType = "recorded"
		rc.ThinkTimeIgnore = false
		factorRaw := strings.TrimSpace(thinkValue("Factor"))
		factor, err := strconv.ParseFloat(factorRaw, 64)
		if err != nil || math.IsNaN(factor) || math.IsInf(factor, 0) || factor <= 0 || factor > dsl.MaxThinkTimeMultiplier {
			rc.ThinkTimeMultiplier = 1
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("Invalid MULTIPLY Factor=%q; using recorded think time at 100%%.", factorRaw),
			})
		} else {
			rc.ThinkTimeMultiplier = factor
		}
	case "NOTHINK":
		rc.ThinkTimeType = "recorded"
		rc.ThinkTimeIgnore = true
		rc.ThinkTimeMultiplier = 1
	case "RANDOM":
		rc.ThinkTimeType = "recorded"
		rc.ThinkTimeIgnore = false
		rc.ThinkTimeMultiplier = 1
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "think_time", Area: "Think Time",
			Notes: fmt.Sprintf("Unsupported LoadRunner RANDOM percentage think-time mode (low=%q, high=%q); using recorded think time at 100%% and requiring review.", thinkValue("ThinkTimeRandomLow"), thinkValue("ThinkTimeRandomHigh")),
		})
	default:
		if thinkMode != "" {
			rc.ThinkTimeType = "recorded"
			rc.ThinkTimeIgnore = false
			rc.ThinkTimeMultiplier = 1
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("Unsupported think-time Options=%q; using recorded think time at 100%% and requiring review.", optionsRaw),
			})
		}
	}
	if thinkMode != "" && ignoreRaw != "" {
		expectedIgnore := thinkMode == "NOTHINK"
		if !legacyIgnoreValid {
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("Invalid legacy think-time Ignore=%q conflicts with Options=%q; Options wins and review is required.", ignoreRaw, optionsRaw),
			})
		} else if legacyIgnore != expectedIgnore {
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("Legacy think-time Ignore=%q conflicts with Options=%q; Options wins and review is required.", ignoreRaw, optionsRaw),
			})
		}
	}
	parseLegacyRuntimeThinkTimeValues(rc, thinkTimeSection, thinkMode)
	if rawFlag := strings.TrimSpace(thinkValue("LimitFlag")); rawFlag != "" {
		enabled, validFlag := parseBoolExplicit(rawFlag)
		rc.ThinkTimeLimitMs = 0
		if !validFlag {
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("Invalid LimitFlag=%q; applying no think-time cap and requiring review.", rawFlag),
			})
		} else if enabled {
			rawLimit := strings.TrimSpace(thinkValue("Limit"))
			seconds, err := strconv.ParseFloat(rawLimit, 64)
			if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds*1000 > dsl.MaxConfiguredProtocolTimeoutMS {
				rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
					ID: "think_time", Area: "Think Time",
					Notes: fmt.Sprintf("Invalid think-time Limit=%q; applying no cap and requiring review.", rawLimit),
				})
			} else if seconds == 0 {
				// LoadRunner treats this as an enabled zero-second cap, while
				// APEX reserves limit_ms=0 to mean "no cap". Do not report the
				// numerically identical destination value as an exact mapping.
				// Retain the documented APEX default and require an operator to
				// choose the intended zero-delay policy explicitly.
				rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
					ID: "think_time", Area: "Think Time",
					Notes: fmt.Sprintf("LoadRunner LimitFlag=1 with Limit=%q enables a zero-second think-time cap, but APEX limit_ms=0 means no cap; using the APEX no-cap default and requiring review.", rawLimit),
				})
			} else {
				rc.ThinkTimeLimitMs = seconds * 1000
			}
		}
	}
	if math.IsNaN(rc.ThinkTimeLimitMs) || math.IsInf(rc.ThinkTimeLimitMs, 0) || rc.ThinkTimeLimitMs < 0 || rc.ThinkTimeLimitMs > dsl.MaxConfiguredProtocolTimeoutMS {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "think_time", Area: "Think Time",
			Notes: "Invalid native think-time limit; applying no cap and requiring review.",
		})
		rc.ThinkTimeLimitMs = 0
	}

	logSection := findMergedSection(sections, "Log", "Logging")
	nativeLogOptions, hasNativeLogOptions := sectionValueCaseInsensitive(logSection, "LogOptions")
	if hasNativeLogOptions && strings.TrimSpace(nativeLogOptions) != "" {
		switch normalizeEnum(nativeLogOptions) {
		case "logdisabled":
			rc.LogLevel = "disabled"
		case "logbrief":
			rc.LogLevel = "standard"
		case "logextended":
			rc.LogLevel = "extended"
		default:
			rc.LogLevel = "standard"
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "logging", Area: "Logging",
				sourceKey: "level",
				Notes:     fmt.Sprintf("Unsupported LogOptions=%q; using standard logging and requiring review.", nativeLogOptions),
			})
		}
	} else {
		rc.LogLevel = mapLogLevel(runtimeSectionFirstNonBlank(logSection, "Level", "LogLevel"))
	}
	rc.OnErrorOnly = parseBool(runtimeSectionFirstNonBlank(logSection, "OnErrorOnly", "LogOnErrorOnly"))
	rc.LogExtendedOptions = splitList(runtimeSectionFirstNonBlank(logSection, "ExtendedOptions", "ExtendedLogOptions"))
	if parseBool(runtimeSectionValue(logSection, "ParameterSubstitution")) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "parameter_substitution")
	}
	if parseBool(runtimeSectionValue(logSection, "DataReturned")) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "data_returned")
	}
	if parseBool(runtimeSectionValue(logSection, "AdvancedTrace")) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "advanced_trace")
	}
	if rc.LogLevel == "extended" && hasNativeLogOptions {
		appendNativeLogClassOption(rc, logSection, "MsgClassParameters", "parameter_substitution")
		appendNativeLogClassOption(rc, logSection, "MsgClassData", "data_returned")
		appendNativeLogClassOption(rc, logSection, "MsgClassFull", "advanced_trace")
	}

	miscSection := findMergedSection(sections, "Misc", "Miscellaneous")
	webSection := findSection(sections, "WEB", "Web")
	rc.ContinueOnError = parseBool(firstNonBlank(
		runtimeSectionFirstNonBlank(miscSection, "ContinueOnError", "ErrorContinue"),
		runtimeSectionValue(generalSection, "ContinueOnError"),
	))
	rc.SnapshotOnError = parseBool(firstNonBlank(
		runtimeSectionFirstNonBlank(miscSection, "SnapshotOnError", "GenerateSnapshotOnError"),
		runtimeSectionValue(webSection, "SnapshotOnErrorActive"),
	))

	attrsSection := findMergedSection(sections, "Additional Attributes", "Attributes")
	for key, value := range attrsSection {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		rc.AdditionalAttributes[trimmedKey] = strings.TrimSpace(value)
	}

	filtersSection := findMergedSection(sections, "Download Filters", "DownloadFilters")
	rc.DownloadFilterMode = normalizeEnum(runtimeSectionFirstNonBlank(filtersSection, "Mode", "FilterMode"))
	rc.DownloadFilterHosts = splitList(runtimeSectionFirstNonBlank(filtersSection, "Hosts", "IncludeHosts", "ExcludeHosts"))

	contentCheckSection := findMergedSection(sections, "Content Check", "ContentCheck")
	if text := strings.TrimSpace(runtimeSectionFirstNonBlank(contentCheckSection, "Text", "SearchText")); text != "" {
		failIfFound := !hasExplicitFalse(runtimeSectionValue(contentCheckSection, "FailIfFound"))
		rc.ContentChecks = append(rc.ContentChecks, dsl.ContentCheckConfig{
			Text:        text,
			Scope:       normalizeSourceContentCheckScope(firstNonBlank(runtimeSectionValue(contentCheckSection, "Scope"), "any_response")),
			FailIfFound: &failIfFound,
		})
	}

	networkSection := findMergedSection(sections, "Network Speed Simulation", "NetworkSimulation")
	rc.NetworkSimulationMode = normalizeEnum(runtimeSectionFirstNonBlank(networkSection, "Mode", "SimulationMode"))
	rc.NetworkSimulationPreset = normalizeEnum(runtimeSectionValue(networkSection, "Preset"))
	rc.NetworkBandwidthKbps = firstNonZeroInt(
		runtimeSectionValue(networkSection, "BandwidthKbps"),
		runtimeSectionValue(networkSection, "Bandwidth"),
	)
	rc.NetworkLatencyMs = parseInt(runtimeSectionValue(networkSection, "LatencyMs"))

	streamingSection := findSection(sections, "Streaming")
	rc.StreamingMode = normalizeEnum(runtimeSectionValue(streamingSection, "Mode"))
	rc.StreamingBufferSizeSeconds = firstNonZeroInt(
		runtimeSectionValue(streamingSection, "BufferSizeSeconds"),
		runtimeSectionValue(streamingSection, "BufferSize"),
	)
	rc.StreamingFixedBitrateKbps = firstNonZeroInt(
		runtimeSectionValue(streamingSection, "FixedBitrateKbps"),
		runtimeSectionValue(streamingSection, "FixedBitrate"),
	)
	rc.StreamingBandwidthEstimation = normalizeEnum(runtimeSectionValue(streamingSection, "BandwidthEstimation"))

	dataFormattingSection := findMergedSection(sections, "Data Formatting", "DataFormatting")
	rc.DataFormattingAutoDetect = parseBool(runtimeSectionFirstNonBlank(dataFormattingSection, "AutoDetect", "DFEAutoDetect"))
	for _, item := range splitDelimitedItems(runtimeSectionFirstNonBlank(dataFormattingSection, "Chain", "DFEChain")) {
		transform := parseDFETransform(item)
		if transform.Type == "" {
			continue
		}
		rc.DataFormattingChain = append(rc.DataFormattingChain, transform)
	}

	javaVMSectionAliases := []string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"}
	javaVMSection := findMergedSection(sections, javaVMSectionAliases...)
	// Parse executable list values from the original CFG text. The general
	// config parser decodes adjacent C string literals for source-code fields;
	// applying that normalization here would concatenate separately quoted JVM
	// arguments and could silently change the imported command line.
	rawJavaVMSection := findMergedSection(parseRawRuntimeConfigSections(content), javaVMSectionAliases...)
	javaVMJDKSelectionExplicit := false
	if selection, ok := sectionValueCaseInsensitive(javaVMSection, "use_jdk", "UseJdk", "jdk_selection", "UseSpecifiedJdk"); ok {
		javaVMJDKSelectionExplicit = true
		rc.JavaVMJDKSelectionRaw = selection
		rc.JavaVMJDKSelection = normalizeJavaVMJDKSelection(selection)
		if rc.JavaVMJDKSelection == "" {
			rc.JavaVMJDKSelectionParseError = "unsupported Java VM JDK selector"
		}
	}
	if home, ok := sectionValueCaseInsensitive(rawJavaVMSection, "jdk_home", "JdkPath", "JavaVmJdkPath", "UserJdkPath", "JdkHome"); ok {
		rc.JavaVMJDKHome = cleanRawRuntimeScalar(home)
	}
	// A home without an explicit selector is actionable source evidence. An
	// explicit search_jdk/internal selector wins while the stale home remains
	// preserved for round-trip review.
	if !javaVMJDKSelectionExplicit && rc.JavaVMJDKHome != "" {
		rc.JavaVMJDKSelection = "specified"
	}
	if rows := orderedJavaVMParameterRows(rawJavaVMSection); len(rows) > 0 {
		for index := range rows {
			rows[index].line = runtimeConfigSourceSettingLine(content, rows[index].key, rows[index].value)
		}
		// The base row remains JavaVMParametersRaw so the review-artifact
		// evidence still names one correctable source line; every row is kept
		// in order on JavaVMParameterRows.
		rc.JavaVMParametersRaw = disclosureSafeJavaVMParameterRow(rows[0].value)
		// Provenance is recorded for EVERY row before any parsing, because a
		// malformed row must not erase the evidence of the rows after it: the
		// argv list is discarded on a parse error, so these strings are the only
		// remaining record of what the archive configured.
		for _, row := range rows {
			rc.javaVMParameterSourceRows = append(
				rc.javaVMParameterSourceRows,
				row.value,
			)
			rc.JavaVMParameterRows = append(
				rc.JavaVMParameterRows,
				disclosureSafeJavaVMParameterRow(row.value),
			)
		}
		parameters := make([]string, 0, len(rows))
		provenance := make([]javaVMParameterTokenProvenance, 0, len(rows))
		for _, row := range rows {
			if strings.TrimSpace(row.value) == "" {
				continue
			}
			rowParameters, err := splitQuotedRuntimeArguments(row.value)
			if err != nil {
				// Fail closed across the whole set: a partial command line is
				// more dangerous than an unimported one, and the row name tells
				// the reviewer exactly which line to correct.
				rc.JavaVMParametersParseError = fmt.Sprintf("%s: %v", row.key, err)
				rc.javaVMParameterRejections = append(
					rc.javaVMParameterRejections,
					javaVMParameterRejection{
						sourceKey:  row.key,
						sourceLine: row.line,
						tokenIndex: -1,
						category:   "malformed_quoting",
					},
				)
				parameters = nil
				break
			}
			for _, parameter := range rowParameters {
				parameters = append(parameters, parameter)
				provenance = append(provenance, javaVMParameterTokenProvenance{
					sourceKey:  row.key,
					sourceLine: row.line,
				})
			}
		}
		if rc.JavaVMParametersParseError == "" {
			validationIssues := validateImportedJavaVMParameters(parameters)
			for _, issue := range javaVMParameterIssuesBySensitivity(validationIssues, true) {
				review := javaVMParameterRejection{
					tokenIndex: issue.Index,
					category:   issue.Category,
				}
				if issue.Index >= 0 && issue.Index < len(provenance) {
					review.sourceKey = provenance[issue.Index].sourceKey
					review.sourceLine = provenance[issue.Index].sourceLine
				} else if len(rows) > 0 {
					review.sourceKey = rows[0].key
					review.sourceLine = rows[0].line
				}
				rc.javaVMParameterExposureReviews = append(
					rc.javaVMParameterExposureReviews,
					review,
				)
			}
			if blockingIssues := javaVMParameterIssuesBySensitivity(validationIssues, false); len(blockingIssues) > 0 {
				for _, issue := range blockingIssues {
					rejection := javaVMParameterRejection{
						tokenIndex: issue.Index,
						category:   issue.Category,
					}
					if issue.Index >= 0 && issue.Index < len(provenance) {
						rejection.sourceKey = provenance[issue.Index].sourceKey
						rejection.sourceLine = provenance[issue.Index].sourceLine
					} else if len(rows) > 0 {
						rejection.sourceKey = rows[0].key
						rejection.sourceLine = rows[0].line
					}
					rc.javaVMParameterRejections = append(rc.javaVMParameterRejections, rejection)
				}
				// The draft remains execution-gated, so retain every structurally
				// valid sibling exactly for operator repair and omit only tokens
				// the runner can never accept. In particular, a credential-bearing
				// -D property must not disappear merely because a neighboring
				// process-control option was unsafe.
				parameters = javaVMParametersWithoutStructuralIssues(
					parameters,
					blockingIssues,
				)
			}
		}
		for _, rejection := range rc.javaVMParameterRejections {
			location := rejection.sourceKey
			if rejection.sourceLine > 0 {
				location = fmt.Sprintf("%s at line %d", rejection.sourceKey, rejection.sourceLine)
			}
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID:        "java_vm_parameters",
				Area:      "Java VM",
				sourceKey: "javavmparameters",
				Notes: fmt.Sprintf(
					"Java VM parameter %s (token index %d) was omitted (%s): unsafe JVM process-control options are not executable; execution remains blocked until the behavior is replaced and reviewed.",
					location,
					rejection.tokenIndex,
					rejection.category,
				),
			})
		}
		if rc.JavaVMParametersParseError == "" && len(parameters) > 0 {
			rc.JavaVMAdditionalParameters = parameters
		}
	}
	if value, ok := sectionValueCaseInsensitive(javaVMSection, "Use_Xboot", "use_xboot", "UseXbootClasspath"); ok {
		rc.JavaVMUseXBootClasspathRaw = value
		if parsed, valid := parseBoolExplicit(value); valid {
			rc.JavaVMUseXBootClasspath = &parsed
		} else {
			rc.JavaVMUseXBootParseError = "invalid Java VM Xboot boolean"
		}
	}
	if value, ok := sectionValueCaseInsensitive(javaVMSection, "enable_CL_per_vuser", "enable_classloader_per_vuser", "DedicatedClassLoader", "ClassLoaderPerVuser"); ok {
		rc.JavaVMDedicatedClassLoaderRaw = value
		if parsed, valid := parseBoolExplicit(value); valid {
			rc.JavaVMDedicatedClassLoader = &parsed
		} else {
			rc.JavaVMDedicatedParseError = "invalid Java VM dedicated-class-loader boolean"
		}
	}
	if value, ok := sectionValueCaseInsensitive(rawJavaVMSection, "Java_Env_ClassPath", "java_env_class_paths"); ok {
		rc.JavaVMClasspathRaw = value
		classpath, err := splitJavaVMClasspath(value)
		if err != nil {
			rc.JavaVMClasspathParseError = err.Error()
		} else {
			rc.JavaVMClasspath = classpath
		}
	}
	// Key aliases are documented best guesses pending a real LR Java default.cfg
	// sample containing the "Skip automatic parameter evaluation" performance
	// checkbox; the forgiving list mirrors the other Java VM options above.
	if value, ok := sectionValueCaseInsensitive(javaVMSection, "skip_param_eval", "Skip_Param_Eval", "SkipParamEval", "skip_automatic_parameter_evaluation", "SkipAutomaticParameterEvaluation", "skip_parameter_evaluation"); ok {
		rc.JavaVMSkipParameterEvaluationRaw = value
		if parsed, valid := parseBoolExplicit(value); valid {
			rc.JavaVMSkipParameterEvaluation = &parsed
		} else {
			rc.JavaVMSkipParamEvalParseError = "invalid Java VM skip-parameter-evaluation boolean"
		}
	}
	appendKnownUnsupportedRuntimeIssues(rc, sections)
	applyRuntimeCfgCaseFoldConflicts(rc, content)

	return rc
}

func (rc *RuntimeConfig) ToScriptConfig() *dsl.ScriptConfig {
	if rc == nil {
		return nil
	}
	config := &dsl.ScriptConfig{}
	if httpCfg := rc.ToHTTPConfig(); httpCfg != nil {
		config.HTTP = *httpCfg
	}
	config.BrowserEmulation = rc.ToBrowserEmulationConfig()
	if rc.Iterations > 0 || rc.AutoTransactions != "" || rc.BlockScheduling != "" || len(rc.ActionSequence) > 0 {
		config.RunLogic = dsl.RunLogicConfig{
			Iterations:       rc.Iterations,
			AutoTransactions: rc.AutoTransactions,
			BlockScheduling:  rc.BlockScheduling,
			ActionSequence:   append([]string(nil), rc.ActionSequence...),
		}
	}
	if rc.PacingType != "" {
		config.Pacing = dsl.PacingConfig{
			Type:          rc.PacingType,
			DelayMs:       rc.DelayMs,
			MinDelayMs:    rc.MinDelayMs,
			MaxDelayMs:    rc.MaxDelayMs,
			IntervalMs:    rc.IntervalMs,
			MinIntervalMs: rc.MinIntervalMs,
			MaxIntervalMs: rc.MaxIntervalMs,
		}
	}
	if rc.ThinkTimeType != "" || rc.ThinkTimeIgnore || rc.ThinkTimeMultiplier != 0 || rc.ThinkTimeLimitMs != 0 {
		config.ThinkTime = dsl.ThinkTimeConfig{
			Type:       rc.ThinkTimeType,
			Ignore:     rc.ThinkTimeIgnore,
			Multiplier: rc.ThinkTimeMultiplier,
			LimitMs:    rc.ThinkTimeLimitMs,
		}
	}
	if rc.LogLevel != "" || rc.OnErrorOnly || len(rc.LogExtendedOptions) > 0 {
		config.Log = dsl.LogConfig{
			Level:           rc.LogLevel,
			OnErrorOnly:     rc.OnErrorOnly,
			ExtendedOptions: append([]string(nil), rc.LogExtendedOptions...),
		}
	}
	if rc.ContinueOnError || rc.SnapshotOnError {
		config.ErrorHandling = dsl.ErrorHandlingConfig{
			ContinueOnError: rc.ContinueOnError,
			SnapshotOnError: rc.SnapshotOnError,
		}
	}
	if rc.CacheEnabled || rc.CacheContentURLs || rc.CheckNewerVersions || rc.NewUserPerIteration || rc.BrowserEngine != "" || rc.BrowserDeviceProfile != "" || rc.BrowserTouchMode || rc.BrowserOrientation != "" || rc.BrowserClientMetrics || rc.BrowserSlowMoMs != 0 || rc.BrowserViewportWidth != 0 || rc.BrowserViewportHeight != 0 || rc.DownloadResources != "" {
		cacheEnabled := rc.CacheEnabled
		cacheContentURLs := rc.CacheContentURLs
		checkNewerVersions := rc.CheckNewerVersions
		newUserPerIter := rc.NewUserPerIteration
		touchMode := rc.BrowserTouchMode
		clientMetrics := rc.BrowserClientMetrics
		resolvedProfile, profileEngine := resolveImportedBrowserProfile(rc.BrowserDeviceProfile)
		resolvedEngine := rc.BrowserEngine
		if profileEngine != "" {
			resolvedEngine = profileEngine
		}
		config.Browser = dsl.BrowserConfig{
			Engine:             resolvedEngine,
			SlowMoMs:           rc.BrowserSlowMoMs,
			DeviceProfile:      resolvedProfile,
			TouchMode:          &touchMode,
			Orientation:        rc.BrowserOrientation,
			ClientMetrics:      &clientMetrics,
			CacheEnabled:       &cacheEnabled,
			CacheContentURLs:   &cacheContentURLs,
			CheckNewerVersions: &checkNewerVersions,
			NewUserPerIter:     &newUserPerIter,
			DownloadResources:  rc.DownloadResources,
			Viewport: dsl.BrowserViewport{
				Width:  rc.BrowserViewportWidth,
				Height: rc.BrowserViewportHeight,
			},
		}
	}
	if len(rc.AdditionalAttributes) > 0 || rc.DownloadFilterMode != "" || len(rc.DownloadFilterHosts) > 0 || len(rc.ContentChecks) > 0 || rc.NetworkSimulationMode != "" || rc.NetworkSimulationPreset != "" || rc.NetworkBandwidthKbps != 0 || rc.NetworkLatencyMs != 0 || rc.StreamingMode != "" || rc.StreamingBufferSizeSeconds != 0 || rc.StreamingFixedBitrateKbps != 0 || rc.StreamingBandwidthEstimation != "" || rc.DataFormattingAutoDetect || len(rc.DataFormattingChain) > 0 {
		config.WebRuntime = dsl.WebRuntimeConfig{
			AdditionalAttributes: cloneStringMap(rc.AdditionalAttributes),
			DownloadFilters: dsl.DownloadFiltersConfig{
				Mode:  rc.DownloadFilterMode,
				Hosts: append([]string(nil), rc.DownloadFilterHosts...),
			},
			ContentChecks: append([]dsl.ContentCheckConfig(nil), rc.ContentChecks...),
			NetworkSimulation: dsl.NetworkSimulationConfig{
				Mode:          rc.NetworkSimulationMode,
				Preset:        rc.NetworkSimulationPreset,
				BandwidthKbps: rc.NetworkBandwidthKbps,
				LatencyMs:     rc.NetworkLatencyMs,
			},
			Streaming: dsl.StreamingConfig{
				Mode:                rc.StreamingMode,
				BufferSizeSeconds:   rc.StreamingBufferSizeSeconds,
				FixedBitrateKbps:    rc.StreamingFixedBitrateKbps,
				BandwidthEstimation: rc.StreamingBandwidthEstimation,
			},
			DataFormatting: dsl.DataFormattingConfig{
				AutoDetect: rc.DataFormattingAutoDetect,
				Chain:      append([]dsl.DFETransform(nil), rc.DataFormattingChain...),
			},
		}
	}
	if rc.JavaVMJDKSelection != "" || rc.JavaVMJDKHome != "" || len(rc.JavaVMAdditionalParameters) > 0 || rc.JavaVMUseXBootClasspath != nil || rc.JavaVMDedicatedClassLoader != nil || len(rc.JavaVMClasspath) > 0 || rc.JavaVMSkipParameterEvaluation != nil {
		config.JavaVuser.JDKSelection = rc.JavaVMJDKSelection
		config.JavaVuser.JDKHome = rc.JavaVMJDKHome
		config.JavaVuser.AdditionalVMParameters = append([]string(nil), rc.JavaVMAdditionalParameters...)
		config.JavaVuser.UseXBootClasspath = cloneRuntimeBoolPointer(rc.JavaVMUseXBootClasspath)
		config.JavaVuser.DedicatedClassLoader = cloneRuntimeBoolPointer(rc.JavaVMDedicatedClassLoader)
		config.JavaVuser.Classpath = append([]string(nil), rc.JavaVMClasspath...)
		config.JavaVuser.SkipParameterEvaluation = cloneRuntimeBoolPointer(rc.JavaVMSkipParameterEvaluation)
	}
	return config
}

var sourceBrowserDeviceAliases = map[string]string{
	"ipad_pro": "ipad_air",
}

func resolveImportedBrowserProfile(profile string) (string, string) {
	normalized := normalizeEnum(profile)
	if normalized == "" {
		return "", ""
	}
	if alias, ok := sourceBrowserDeviceAliases[normalized]; ok {
		normalized = alias
	}
	device, ok := browserprotocol.LookupDevice(normalized)
	if !ok {
		return normalized, ""
	}
	return normalized, device.BrowserFamily
}

// ToHTTPConfig converts parsed source runtime settings into an APEX Load HTTPConfig.
func (rc *RuntimeConfig) ToHTTPConfig() *dsl.HTTPConfig {
	cfg := &dsl.HTTPConfig{}

	if rc.ConnectTimeoutSec > 0 {
		cfg.ConnectTimeoutMs = rc.ConnectTimeoutSec * 1000
	}
	if rc.ReceiveTimeoutSec > 0 {
		cfg.RequestTimeoutMs = rc.ReceiveTimeoutSec * 1000
	}
	if rc.StepTimeoutSec > 0 && rc.ReceiveTimeoutSec == 0 {
		cfg.RequestTimeoutMs = rc.StepTimeoutSec * 1000
	}

	version := httpprotocol.NormalizeVersion(rc.HTTPVersion)
	if httpprotocol.VersionExecutable(version) {
		cfg.Version = version
	}
	if rc.EnableHTTP2 != nil && *rc.EnableHTTP2 {
		// LoadRunner's HTTP/2 toggle means H2 is preferred while HTTP/1.1
		// fallback remains available. Native "2" is an exact H2 requirement, so
		// the faithful projection is the empty automatic-negotiation mode.
		cfg.Version = ""
	}
	if rc.MaxConnsPerHost > 0 {
		cfg.MaxConnsPerHost = rc.MaxConnsPerHost
	}

	ka := rc.KeepAlive
	cfg.KeepAlive = &ka

	if rc.MaxRedirects != 10 {
		mr := rc.MaxRedirects
		cfg.MaxRedirects = &mr
	}

	if rc.ProxyURL != "" {
		proxyURL, proxyUsername, proxyPassword := splitEnterpriseProxyUserinfo(
			rc.ProxyURL,
			rc.ProxyUser,
			rc.ProxyPass,
		)
		if rc.ProxyMode == "" {
			cfg.ProxyMode = "custom"
		} else {
			cfg.ProxyMode = rc.ProxyMode
		}
		cfg.Proxy = dsl.ProxyConfig{
			URL:      proxyURL,
			Username: credential.DraftReference(proxyUsername, enterpriseProxyUsernameSecretName),
			Password: credential.DraftReference(proxyPassword, enterpriseProxyPasswordSecretName),
		}
	} else if rc.ProxyMode != "" {
		cfg.ProxyMode = rc.ProxyMode
	}
	if cfg.Proxy.Username == "" && rc.proxyReviewUser != "" {
		cfg.Proxy.Username = credential.DraftReference(
			rc.proxyReviewUser,
			enterpriseProxyUsernameSecretName,
		)
	}
	if cfg.Proxy.Password == "" && rc.proxyReviewPass != "" {
		cfg.Proxy.Password = credential.DraftReference(
			rc.proxyReviewPass,
			enterpriseProxyPasswordSecretName,
		)
	}
	if len(rc.ProxyNoProxy) > 0 {
		cfg.Proxy.NoProxy = append([]string(nil), rc.ProxyNoProxy...)
	}

	// When browser emulation resolves a mode, the User-Agent lives in
	// config.browser_emulation (authoritative at replay); duplicating it here
	// would leave two editable sources of the same header.
	//
	// An explicit UseCustomAgent=0 also suppresses the fallback: the source is
	// saying "do not present this custom agent". Without this guard, a cfg
	// carrying UseCustomAgent=0 alongside a CustomUserAgent value and no
	// recognized BrowserType resolves to mode "" and then applies the very agent
	// the source disabled.
	emulation := rc.ToBrowserEmulationConfig()
	customUASuppressed := rc.BEUseCustomUA != nil && !*rc.BEUseCustomUA
	if rc.UserAgent != "" && !customUASuppressed && (emulation == nil || emulation.Mode == "") {
		cfg.UserAgent = rc.UserAgent
	}
	if rc.AcceptLanguage != "" {
		cfg.AcceptLanguage = rc.AcceptLanguage
	}
	if rc.AcceptEncoding != "" {
		cfg.AcceptEncoding = rc.AcceptEncoding
	} else if rc.UseGzip {
		cfg.AcceptEncoding = "gzip, deflate"
	}
	if rc.NewUserPerIteration {
		clearCookies := true
		cfg.ClearCookiesPerIter = &clearCookies
	}

	if rc.SSLVersion != "" {
		version := mapSSLVersion(rc.SSLVersion)
		if version != "" {
			cfg.TLS = dsl.TLSConfig{
				MinVersion: version,
				MaxVersion: version,
			}
		}
	}

	if rc.AuthUser != "" {
		cfg.Auth = dsl.AuthConfig{
			Type:     "basic",
			Username: rc.AuthUser,
			Password: credential.DraftReference(rc.AuthPass, enterpriseHTTPAuthPasswordSecretName),
		}
	}

	return cfg
}

// enumDownloadResourcesOnly returns the value only when it is one of the
// browser download_resources enum members (all/none/recorded); bool forms such
// as "1"/"yes" are dropped so they don't become an invalid enum value.
func enumDownloadResourcesOnly(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all", "none", "recorded":
		return value
	}
	return ""
}

// firstNonBlankLookup returns the first alias whose lookup yields a non-blank
// value, preserving alias precedence.
func firstNonBlankLookup(lookup func(...string) (string, bool), aliases ...string) string {
	for _, alias := range aliases {
		if value, ok := lookup(alias); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// parseBrowserEmulationRuntimeSettings extracts config.browser_emulation
// inputs. Real LoadRunner cfgs keep these under [WEB]; the APEX-native aliases
// live under [Browser Emulation]. Each key is looked up in the browser section
// first, then the [WEB] section, case-insensitively.
func parseBrowserEmulationRuntimeSettings(rc *RuntimeConfig, browserSection, webSection map[string]string) {
	lookup := func(aliases ...string) (string, bool) {
		if value, ok := sectionValueCaseInsensitive(browserSection, aliases...); ok {
			return value, true
		}
		return sectionValueCaseInsensitive(webSection, aliases...)
	}
	// lookupPreferred resolves alias-first (each alias across both sections)
	// rather than section-first, so an authoritative real-LoadRunner key beats an
	// invented alias regardless of which section carries it — e.g. an explicit
	// [WEB] SimulateCache=0 is not shadowed by a [Browser] CacheEnabled=1.
	lookupPreferred := func(aliases ...string) (string, bool) {
		for _, alias := range aliases {
			if value, ok := lookup(alias); ok {
				return value, true
			}
		}
		return "", false
	}

	// UA string: same browser-section→[WEB] fallback as the toggles above.
	rc.UserAgent = strings.TrimSpace(firstNonBlankLookup(lookup, "CustomUserAgent", "UserAgent", "BrowserUserAgent"))
	parseTristate := func(target **bool, raw *string, parseError *string, errText string, aliases ...string) {
		value, ok := lookup(aliases...)
		if !ok || strings.TrimSpace(value) == "" {
			return
		}
		*raw = value
		if parsed, valid := parseBoolExplicit(value); valid {
			*target = &parsed
		} else {
			*parseError = errText
		}
	}

	if value, ok := lookup("BrowserType", "Browser_Type", "UserBrowserType"); ok && strings.TrimSpace(value) != "" {
		rc.BEBrowserTypeRaw = strings.TrimSpace(value)
		mappedType, embeddedVersion := mapSourceBrowserType(rc.BEBrowserTypeRaw)
		rc.BEBrowserType = mappedType
		if embeddedVersion != "" && rc.BEBrowserVersion == "" {
			rc.BEBrowserVersion = embeddedVersion
		}
	}
	if value, ok := lookup("BrowserVer", "BrowserVersion", "UserBrowserVersion"); ok && strings.TrimSpace(value) != "" {
		rc.BEBrowserVersion = strings.TrimSpace(value)
	}
	if value, ok := lookup("BrowserPlatform", "Platform"); ok && strings.TrimSpace(value) != "" {
		rc.BEPlatformRaw = strings.TrimSpace(value)
		rc.BEPlatform = mapSourceBrowserPlatform(rc.BEPlatformRaw)
	}
	if value, ok := lookup("BrowserLanguageCode", "BrowserLanguage", "LanguageCode"); ok && strings.TrimSpace(value) != "" {
		rc.BELanguage = strings.TrimSpace(value)
	}
	// UseBrowserAgent is intentionally NOT an alias here: in LoadRunner it means
	// "use the recorded browser's agent" — the negation of a custom UA — so
	// mapping it onto the custom-UA toggle would invert the semantics. Only the
	// genuine custom-UA toggle keys participate.
	parseTristate(&rc.BEUseCustomUA, &rc.BEUseCustomUARaw, &rc.BEUseCustomUAParseError,
		"invalid browser-emulation custom user-agent toggle", "UseCustomAgent", "UseCustomUserAgent")
	// SimulateCache (real LR) wins over the invented CacheEnabled alias across
	// sections; CacheEnabled still independently feeds config.browser.cache_enabled.
	if value, ok := lookupPreferred("SimulateCache", "CacheEnabled"); ok && strings.TrimSpace(value) != "" {
		rc.BESimulateCacheRaw = value
		if parsed, valid := parseBoolExplicit(value); valid {
			rc.BESimulateCache = &parsed
		} else {
			rc.BESimulateCacheParseError = "invalid browser-emulation simulate-cache boolean"
		}
	}
	parseTristate(&rc.BECacheContentURLs, &rc.BECacheContentURLsRaw, &rc.BECacheContentURLsParseError,
		"invalid browser-emulation cache-content-URLs boolean", "CacheHtmlPages", "CacheContentURLs", "CacheURLsRequiringContent")
	parseTristate(&rc.BECheckNewerVersions, &rc.BECheckNewerVersionsRaw, &rc.BECheckNewerParseError,
		"invalid browser-emulation check-newer-versions boolean", "CacheAlwaysCheckForNewerPages", "CheckNewerVersions", "CheckForNewerPages", "CheckNewerVersion")
	parseTristate(&rc.BENewUserEachIteration, &rc.BENewUserEachIterationRaw, &rc.BENewUserParseError,
		"invalid browser-emulation new-user-each-iteration boolean", "SimulateNewUser", "NewUserEachIteration", "NewUserPerIteration")
	parseTristate(&rc.BESimulatePrefetch, &rc.BESimulatePrefetchRaw, &rc.BESimulatePrefetchParseError,
		"invalid browser-emulation prefetch boolean", "SimulatePrefetchPrerender", "SimulatePrefetch", "Prefetch")
	// Bool form only: all/none/recorded values continue to feed the browser
	// protocol's download_resources enum path above.
	if value, ok := lookup("SearchForImages", "DownloadNonHtmlResources"); ok && strings.TrimSpace(value) != "" {
		if parsed, valid := parseBoolExplicit(value); valid {
			rc.BEDownloadNonHTMLRaw = value
			rc.BEDownloadNonHTML = &parsed
		} else if strings.EqualFold(strings.TrimSpace(value), "all") || strings.EqualFold(strings.TrimSpace(value), "none") || strings.EqualFold(strings.TrimSpace(value), "recorded") {
			// Enum form handled by DownloadResources; not an error.
		} else {
			rc.BEDownloadNonHTMLRaw = value
			rc.BEDownloadNonHTMLParseError = "invalid browser-emulation download-non-HTML boolean"
		}
	}
	if value, ok := lookup("ClearCacheForSimulateNewUser", "ClearCacheEachIteration", "ClearCacheOnEachIteration"); ok && strings.TrimSpace(value) != "" {
		rc.BEClearCacheEachIterationRaw = value
	}
}

// mapSourceBrowserType maps a LoadRunner browser name (e.g. "Microsoft
// Internet Explorer 4.0", "Google Chrome 99") to a catalog type plus any
// version embedded in the name. Unrecognized names return "".
func mapSourceBrowserType(raw string) (mapped, embeddedVersion string) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return "", ""
	}
	if trailing := trailingVersionToken(name); trailing != "" {
		embeddedVersion = trailing
		name = strings.TrimSpace(strings.TrimSuffix(name, trailing))
	}
	switch {
	case strings.Contains(name, "edge"):
		return "edge", embeddedVersion
	case strings.Contains(name, "chrome") || strings.Contains(name, "chromium"):
		return "chrome", embeddedVersion
	case strings.Contains(name, "firefox"):
		return "firefox", embeddedVersion
	case strings.Contains(name, "safari"):
		return "safari", embeddedVersion
	case strings.Contains(name, "internet explorer") || strings.Contains(name, "msie") || name == "ie":
		return "ie", embeddedVersion
	}
	return "", embeddedVersion
}

// trailingVersionToken returns a trailing numeric/dotted version token
// ("microsoft internet explorer 4.0" -> "4.0"), or "".
func trailingVersionToken(name string) string {
	fields := strings.Fields(name)
	if len(fields) < 2 {
		return ""
	}
	candidate := fields[len(fields)-1]
	for _, r := range candidate {
		if (r < '0' || r > '9') && r != '.' {
			return ""
		}
	}
	return candidate
}

// mapSourceBrowserPlatform maps LR platform names to catalog platform ids.
func mapSourceBrowserPlatform(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case name == "":
		return ""
	case strings.Contains(name, "windows") || strings.Contains(name, "win"):
		return "windows"
	case strings.Contains(name, "mac") || strings.Contains(name, "os x"):
		return "macos"
	case strings.Contains(name, "linux") || strings.Contains(name, "x11"):
		return "linux"
	case strings.Contains(name, "android"):
		return "android"
	case strings.Contains(name, "ios") || strings.Contains(name, "iphone") || strings.Contains(name, "ipad"):
		return "ios"
	}
	return ""
}

// ToBrowserEmulationConfig converts the parsed browser-emulation inputs into
// the config.browser_emulation section, or nil when nothing relevant was set.
// Mode resolution: an explicit UseCustomAgent=true (or a UA with no explicit
// toggle — the legacy import behavior) selects custom mode; an explicit
// UseCustomAgent=false suppresses custom mode even with a UA present, falling
// back to specific mode when a recognized browser type exists.
func (rc *RuntimeConfig) ToBrowserEmulationConfig() *dsl.BrowserEmulationConfig {
	if rc == nil {
		return nil
	}
	emu := &dsl.BrowserEmulationConfig{}

	customAllowed := rc.BEUseCustomUA == nil || *rc.BEUseCustomUA
	switch {
	case rc.UserAgent != "" && customAllowed:
		emu.Mode = "custom"
		emu.CustomUserAgent = rc.UserAgent
	case rc.BEBrowserType != "":
		emu.Mode = "specific"
		emu.BrowserType = rc.BEBrowserType
		emu.BrowserVersion = rc.BEBrowserVersion
		// Source browser type and platform/version are parsed independently, so
		// LR-era combinations the APEX catalog does not offer (e.g. Safari on
		// Windows, or a non-numeric version) must be dropped here — otherwise the
		// imported script fails dsl.ValidateScriptIssues and cannot be saved or
		// run. The raw source values remain in the preserved metadata for review,
		// and the engine's UA resolver degrades gracefully on the remaining type.
		if !dsl.BrowserEmulationPlatformValidFor(rc.BEBrowserType, rc.BEPlatform) {
			emu.Platform = ""
		} else {
			emu.Platform = rc.BEPlatform
		}
		if !browserEmulationVersionHasNumericMajor(emu.BrowserVersion) {
			emu.BrowserVersion = ""
		}
	}
	emu.Language = rc.BELanguage

	if rc.BESimulateCache != nil || rc.BECacheContentURLs != nil || rc.BECheckNewerVersions != nil ||
		rc.BENewUserEachIteration != nil || rc.BEDownloadNonHTML != nil || rc.BESimulatePrefetch != nil {
		cache := &dsl.BrowserCacheSimulationConfig{
			SimulateCache:        cloneRuntimeBoolPointer(rc.BESimulateCache),
			CacheContentURLs:     cloneRuntimeBoolPointer(rc.BECacheContentURLs),
			CheckNewerVersions:   cloneRuntimeBoolPointer(rc.BECheckNewerVersions),
			NewUserEachIteration: cloneRuntimeBoolPointer(rc.BENewUserEachIteration),
			DownloadNonHTML:      cloneRuntimeBoolPointer(rc.BEDownloadNonHTML),
			SimulatePrefetch:     cloneRuntimeBoolPointer(rc.BESimulatePrefetch),
		}
		// Stock VuGen cfgs emit the cache child keys unconditionally, so an
		// unchecked "Simulate browser cache" (SimulateCache=0) arrives alongside
		// CacheHtmlPages=1 / CacheAlwaysCheckForNewerPages=1. Those children are
		// inert while the cache is off, so normalize them away rather than import
		// settings that read as active but do nothing (raw values stay in
		// preserved metadata). The validator accepts either shape.
		if cache.SimulateCache != nil && !*cache.SimulateCache {
			cache.CacheContentURLs = nil
			cache.CheckNewerVersions = nil
		}
		emu.Cache = cache
	}

	if emu.Mode == "" && emu.Language == "" && emu.Cache == nil {
		return nil
	}
	return emu
}

// browserEmulationVersionHasNumericMajor reports whether a browser version
// string begins with a numeric major component (mirrors the validator's
// normalizeBrowserMajorVersion acceptance). Empty is treated as valid ("Latest").
func browserEmulationVersionHasNumericMajor(version string) bool {
	v := strings.TrimSpace(version)
	if v == "" {
		return true
	}
	major, _, _ := strings.Cut(v, ".")
	if _, err := strconv.Atoi(major); err != nil {
		return false
	}
	return true
}

func findSection(sections map[string]map[string]string, aliases ...string) map[string]string {
	for _, alias := range aliases {
		matchingNames := make([]string, 0, 2)
		for name := range sections {
			if strings.EqualFold(name, alias) {
				matchingNames = append(matchingNames, name)
			}
		}
		if len(matchingNames) == 0 {
			continue
		}
		if len(matchingNames) == 1 {
			return sections[matchingNames[0]]
		}

		// INI section names are case-insensitive. Preserve complementary keys
		// split across differently-cased declarations instead of silently
		// selecting one map. Merge deterministically, with the exact alias (when
		// present) applied last; differing declarations of the same semantic key
		// are independently detected and defaulted by the case-fold conflict
		// audit.
		sort.Slice(matchingNames, func(i, j int) bool {
			leftExact := matchingNames[i] == alias
			rightExact := matchingNames[j] == alias
			if leftExact != rightExact {
				return !leftExact
			}
			return matchingNames[i] < matchingNames[j]
		})
		merged := make(map[string]string)
		for _, name := range matchingNames {
			for key, value := range sections[name] {
				merged[key] = value
			}
		}
		return merged
	}
	return map[string]string{}
}

// findMergedSection treats every supplied alias as the same semantic INI
// section and merges complementary declarations across aliases and casing.
// Callers must not use it for documented precedence fallbacks such as
// Browser→WEB, where the first source remains authoritative.
func findMergedSection(sections map[string]map[string]string, aliases ...string) map[string]string {
	if len(sections) == 0 || len(aliases) == 0 {
		return map[string]string{}
	}
	matched := make(map[string]struct{})
	for name := range sections {
		for _, alias := range aliases {
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(alias)) {
				matched[name] = struct{}{}
				break
			}
		}
	}
	if len(matched) == 0 {
		return map[string]string{}
	}
	names := make([]string, 0, len(matched))
	for name := range matched {
		names = append(names, name)
	}
	sort.Strings(names)
	merged := make(map[string]string)
	for _, name := range names {
		for key, value := range sections[name] {
			merged[key] = value
		}
	}
	return merged
}

type runtimeConfigOccurrence struct {
	section string
	key     string
	value   string
	line    int
}

type runtimeConfigCaseFoldConflict struct {
	section     string
	key         string
	occurrences []runtimeConfigOccurrence
}

// runtimeConfigCaseFoldConflicts preserves the source occurrences that the
// ordinary INI map necessarily collapses or separates by spelling. Runtime
// settings are case-insensitive, so differently cased declarations belong to
// one semantic identity; differing values cannot be selected silently.
func runtimeConfigCaseFoldConflicts(content string) []runtimeConfigCaseFoldConflict {
	scanner := bufio.NewScanner(strings.NewReader(content))
	maxTokenSize := len(content)
	if maxTokenSize < 64*1024 {
		maxTokenSize = 64 * 1024
	} else if maxTokenSize < int(^uint(0)>>1) {
		maxTokenSize++
	}
	scanner.Buffer(make([]byte, 64*1024), maxTokenSize)

	section := ""
	grouped := make(map[string][]runtimeConfigOccurrence)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if match := reSectionHeader.FindStringSubmatch(line); len(match) > 1 {
			section = strings.TrimSpace(match[1])
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		occurrence := runtimeConfigOccurrence{
			section: section,
			key:     key,
			value:   cleanINIConfigValue(section, key, parts[1]),
			line:    lineNumber,
		}
		identity := runtimeConfigOccurrenceIdentity(section, key)
		grouped[identity] = append(grouped[identity], occurrence)
	}

	conflicts := make([]runtimeConfigCaseFoldConflict, 0)
	for _, occurrences := range grouped {
		if len(occurrences) < 2 || runtimeConfigOccurrencesAgree(occurrences) {
			continue
		}
		conflicts = append(conflicts, runtimeConfigCaseFoldConflict{
			section:     occurrences[0].section,
			key:         occurrences[0].key,
			occurrences: occurrences,
		})
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].occurrences[0].line < conflicts[j].occurrences[0].line
	})
	return conflicts
}

func runtimeConfigOccurrenceIdentity(section, key string) string {
	canonicalSection := runtimeConfigCanonicalSection(section)
	canonicalKey := runtimeConfigCanonicalKey(canonicalSection, key)
	rawSection := strings.ToLower(strings.TrimSpace(section))
	if canonicalKey == "cacheenabled" && compactRuntimeEnum(key) == "cacheenabled" {
		// SimulateCache is the authoritative LoadRunner field; CacheEnabled is
		// an APEX-native fallback. Keep duplicate fallback spellings grouped,
		// but do not turn the documented precedence into an ambiguity conflict.
		canonicalKey = "cacheenabledfallback"
	}
	if runtimeConfigBrowserSemanticKey(canonicalKey) &&
		(canonicalSection == "browser" || rawSection == "web") {
		// [WEB] carries both transport and browser-emulation settings. Group
		// only browser-domain keys with [Browser]/[Browser Emulation], while
		// transport keys continue to group with [HTTP]/[Preferences]. A stray
		// BrowserType under [HTTP] is unsupported source text, not an alias for
		// the valid [WEB] browser setting.
		canonicalSection = "browser"
	}
	if canonicalSection == "javavm" {
		if index, ok := javaVMParameterExtraArgIndex(key); ok {
			// Long Java VM argument lists are split over semantically numbered
			// rows. Different base aliases with the same row index name one
			// executable argv fragment and must conflict rather than concatenate.
			canonicalKey = fmt.Sprintf("javavmparameterextraarg:%09d", index)
		}
	}
	if canonicalKey == "iterations" &&
		(canonicalSection == "runlogic" || canonicalSection == "iterations") {
		// The parser treats [RunLogic] and [Iterations] counts as one
		// precedence-ordered setting. Contradictory declarations across those
		// sections are therefore ambiguous, not an authoritative value plus
		// harmless metadata.
		canonicalSection = "iterations"
	}
	if canonicalKey == "autotransactions" &&
		(canonicalSection == "runlogic" || canonicalSection == "general") {
		canonicalSection = "runlogic"
	}
	if rawSection == "web" && runtimeConfigProxySemanticKey(canonicalKey) {
		// The proxy parser treats [Proxy] as authoritative and [WEB] as its
		// compatibility fallback. Declarations on both surfaces still describe
		// one runtime setting: contradictory values are ambiguous source state,
		// not a safe precedence choice.
		canonicalSection = "proxy"
	}
	return canonicalSection + "\x00" + canonicalKey
}

func runtimeConfigProxySemanticKey(key string) bool {
	switch key {
	case "mode", "proxymode", "proxyserver", "proxyuser", "proxypassword",
		"proxyuseproxy", "proxyhttphost", "proxyhttpport",
		"proxyhttpshost", "proxyhttpsport", "proxyusesame",
		"proxybypass", "proxynolocal":
		return true
	default:
		return false
	}
}

func runtimeConfigBrowserSemanticKey(key string) bool {
	switch key {
	case "useragent", "usecustomagent", "acceptlanguage", "acceptencoding",
		"browsertype", "browserversion", "browserplatform",
		"engine", "deviceprofile", "touchmode", "clientmetrics",
		"slowmoms", "viewportwidth", "viewportheight",
		"cacheenabled", "cacheenabledfallback", "cachecontenturls", "checknewerversions",
		"newuserperiteration", "simulateprefetch", "downloadnonhtmlresources":
		return true
	default:
		return false
	}
}

func runtimeConfigCanonicalSection(section string) string {
	normalized := strings.ToLower(strings.TrimSpace(section))
	switch normalized {
	case "think time", "thinktime":
		return "thinktime"
	case "run logic", "runlogic":
		return "runlogic"
	case "http", "internet protocol", "preferences", "internet protocol preferences", "web":
		return "http"
	case "log", "logging":
		return "logging"
	case "browser emulation", "browser":
		return "browser"
	case "javavm:options", "javavm", "java vm", "java environment settings", "java:options":
		return "javavm"
	case "ssl", "security":
		return "security"
	case "misc", "miscellaneous":
		return "misc"
	case "download filters", "downloadfilters":
		return "downloadfilters"
	case "content check", "contentcheck":
		return "contentcheck"
	case "network speed simulation", "networksimulation":
		return "networksimulation"
	case "data formatting", "dataformatting":
		return "dataformatting"
	case "additional attributes", "attributes":
		return "additionalattributes"
	default:
		return normalized
	}
}

func runtimeConfigCanonicalKey(section, key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	compact := compactRuntimeEnum(normalized)
	switch section {
	case "thinktime":
		switch compact {
		case "ignore", "ignorethinktime":
			return "ignore"
		case "multiplier", "multiplyrecordedthinktimeby":
			return "multiplier"
		case "limitms", "limitthinktimems":
			return "limitms"
		}
	case "runlogic":
		switch compact {
		case "iterations", "numberofiterations", "numofiterations":
			return "iterations"
		case "actionsequence", "sequence":
			return "actionsequence"
		}
	case "iterations":
		switch compact {
		case "iterations", "numberofiterations", "numofiterations":
			return "iterations"
		}
	case "general":
		switch compact {
		case "automatictransactions", "autotransactions":
			return "autotransactions"
		}
	case "pacing":
		switch compact {
		case "mode", "pacingmode":
			return "mode"
		case "delayms", "delay":
			return "delay"
		case "mindelayms", "mindelay":
			return "mindelay"
		case "maxdelayms", "maxdelay":
			return "maxdelay"
		case "intervalms", "interval":
			return "interval"
		case "minintervalms", "mininterval":
			return "mininterval"
		case "maxintervalms", "maxinterval":
			return "maxinterval"
		}
	case "http":
		switch compact {
		case "httpversion", "httpver":
			return "httpversion"
		case "enablehttp2", "http2enabled":
			return "enablehttp2"
		case "maxredirections", "maxredirectiondepth":
			return "maxredirects"
		case "maxconnectionsperhost", "maxconnections", "maxconnectionsperhosthttp":
			return "maxconnectionsperhost"
		case "proxyserver", "proxyurl", "server":
			return "proxyserver"
		case "proxyusername", "username", "proxyuser":
			return "proxyuser"
		case "proxypassword", "password":
			return "proxypassword"
		case "customuseragent", "useragent", "browseruseragent":
			return "useragent"
		case "acceptlanguage", "browseracceptlanguage", "browserlanguagecode", "browserlanguage", "languagecode", "navigatorbrowserlanguage", "navigatoruserlanguage":
			return "acceptlanguage"
		case "acceptencoding", "browseracceptencoding":
			return "acceptencoding"
		case "mode", "proxymode", "type":
			return "proxymode"
		case "proxyuseproxy", "proxyuseproxyserver":
			return "proxyuseproxy"
		case "proxyhttphost":
			return "proxyhttphost"
		case "proxyhttpport":
			return "proxyhttpport"
		case "proxyhttpshost":
			return "proxyhttpshost"
		case "proxyhttpsport":
			return "proxyhttpsport"
		case "proxyusesame":
			return "proxyusesame"
		case "proxybypass":
			return "proxybypass"
		case "proxynolocal":
			return "proxynolocal"
		case "browsertype", "userbrowsertype":
			return "browsertype"
		case "browserver", "browserversion", "userbrowserversion":
			return "browserversion"
		case "browserplatform", "platform":
			return "browserplatform"
		case "usecustomagent", "usecustomuseragent":
			return "usecustomagent"
		case "engine", "browserengine":
			return "engine"
		case "deviceprofile", "mobileprofile", "emulateddevice":
			return "deviceprofile"
		case "touchmode", "touchenabled":
			return "touchmode"
		case "clientmetrics", "collectclientmetrics":
			return "clientmetrics"
		case "slowmoms", "slowmotionms":
			return "slowmoms"
		case "viewportwidth", "screenwidth":
			return "viewportwidth"
		case "viewportheight", "screenheight":
			return "viewportheight"
		case "cacheenabled", "simulatecache":
			return "cacheenabled"
		case "cachecontenturls", "cacheurlsrequiringcontent", "cachehtmlpages":
			return "cachecontenturls"
		case "checknewerversions", "checkfornewerpages", "checknewerversion", "cachealwayscheckfornewerpages":
			return "checknewerversions"
		case "newuserperiteration", "newusereachiteration", "simulatenewuser":
			return "newuserperiteration"
		case "simulateprefetchprerender", "simulateprefetch", "prefetch":
			return "simulateprefetch"
		case "searchforimages", "downloadnonhtmlresources":
			return "downloadnonhtmlresources"
		}
	case "authentication":
		switch compact {
		case "username":
			return "username"
		}
	case "proxy":
		switch compact {
		case "mode", "proxymode", "type":
			return "mode"
		case "proxyserver", "proxyurl", "server":
			return "proxyserver"
		case "proxyusername", "username", "proxyuser":
			return "proxyuser"
		case "proxypassword", "password":
			return "proxypassword"
		case "proxyuseproxy", "proxyuseproxyserver":
			return "proxyuseproxy"
		case "proxyhttphost":
			return "proxyhttphost"
		case "proxyhttpport":
			return "proxyhttpport"
		case "proxyhttpshost":
			return "proxyhttpshost"
		case "proxyhttpsport":
			return "proxyhttpsport"
		case "proxyusesame":
			return "proxyusesame"
		case "proxybypass":
			return "proxybypass"
		case "proxynolocal":
			return "proxynolocal"
		}
	case "browser":
		switch compact {
		case "customuseragent", "useragent", "browseruseragent":
			return "useragent"
		case "usecustomagent", "usecustomuseragent":
			return "usecustomagent"
		case "acceptlanguage", "browseracceptlanguage", "browserlanguagecode", "browserlanguage", "languagecode", "navigatorbrowserlanguage", "navigatoruserlanguage":
			return "acceptlanguage"
		case "acceptencoding", "browseracceptencoding":
			return "acceptencoding"
		case "engine", "browserengine":
			return "engine"
		case "deviceprofile", "mobileprofile", "emulateddevice":
			return "deviceprofile"
		case "touchmode", "touchenabled":
			return "touchmode"
		case "clientmetrics", "collectclientmetrics":
			return "clientmetrics"
		case "slowmoms", "slowmotionms":
			return "slowmoms"
		case "viewportwidth", "screenwidth":
			return "viewportwidth"
		case "viewportheight", "screenheight":
			return "viewportheight"
		case "browsertype", "userbrowsertype":
			return "browsertype"
		case "browserver", "browserversion", "userbrowserversion":
			return "browserversion"
		case "browserplatform", "platform":
			return "browserplatform"
		case "cacheenabled", "simulatecache":
			return "cacheenabled"
		case "cachecontenturls", "cacheurlsrequiringcontent", "cachehtmlpages":
			return "cachecontenturls"
		case "checknewerversions", "checkfornewerpages", "checknewerversion", "cachealwayscheckfornewerpages":
			return "checknewerversions"
		case "newuserperiteration", "newusereachiteration", "simulatenewuser":
			return "newuserperiteration"
		case "simulateprefetchprerender", "simulateprefetch", "prefetch":
			return "simulateprefetch"
		case "searchforimages", "downloadnonhtmlresources":
			return "downloadnonhtmlresources"
		}
	case "logging":
		switch compact {
		case "level", "loglevel", "logoptions":
			return "level"
		case "onerroronly", "logonerroronly":
			return "onerroronly"
		case "extendedoptions", "extendedlogoptions":
			return "extendedoptions"
		}
	case "networksimulation":
		switch compact {
		case "mode", "simulationmode":
			return "mode"
		case "bandwidthkbps", "bandwidth":
			return "bandwidth"
		}
	case "security":
		switch compact {
		case "sslversion", "tlsversion":
			return "tlsversion"
		}
	case "misc":
		switch compact {
		case "continueonerror", "errorcontinue":
			return "continueonerror"
		case "snapshotonerror", "generatesnapshotonerror":
			return "snapshotonerror"
		}
	case "downloadfilters":
		switch compact {
		case "mode", "filtermode":
			return "mode"
		case "hosts", "includehosts", "excludehosts":
			return "hosts"
		}
	case "contentcheck":
		switch compact {
		case "text", "searchtext":
			return "text"
		}
	case "streaming":
		switch compact {
		case "buffersizeseconds", "buffersize":
			return "buffersize"
		case "fixedbitratekbps", "fixedbitrate":
			return "fixedbitrate"
		}
	case "dataformatting":
		switch compact {
		case "autodetect", "dfeautodetect":
			return "autodetect"
		case "chain", "dfechain":
			return "chain"
		}
	case "javavm":
		switch compact {
		case "usejdk", "jdkselection", "usespecifiedjdk":
			return "jdkselection"
		case "jdkhome", "jdkpath", "javavmjdkpath", "userjdkpath":
			return "jdkhome"
		case "javavmparams", "javavmparameters", "additionalvmparams", "extravmparams", "vmparams":
			return "javavmparameters"
		case "javaenvclasspath", "javaenvclasspaths":
			return "javavmclasspath"
		case "enableclpervuser", "enableclassloaderpervuser", "dedicatedclassloader", "classloaderpervuser":
			return "dedicatedclassloader"
		case "usexboot", "usexbootclasspath":
			return "usexbootclasspath"
		case "skipparameval", "skipautomaticparameterevaluation", "skipparameterevaluation":
			return "skipparameterevaluation"
		}
	}
	return normalized
}

func runtimeConfigOccurrencesAgree(occurrences []runtimeConfigOccurrence) bool {
	if len(occurrences) < 2 {
		return true
	}
	want := runtimeConfigComparableOccurrenceValue(occurrences[0])
	for _, occurrence := range occurrences[1:] {
		if want != runtimeConfigComparableOccurrenceValue(occurrence) {
			return false
		}
	}
	return true
}

func runtimeConfigComparableOccurrenceValue(occurrence runtimeConfigOccurrence) string {
	rawKey := strings.ToLower(strings.TrimSpace(occurrence.key))
	section := runtimeConfigCanonicalSection(occurrence.section)
	key := runtimeConfigCanonicalKey(section, occurrence.key)
	value := strings.TrimSpace(occurrence.value)
	if section == "cfgfiles" || section == "runlogicfiles" ||
		(section == "general" &&
			(rawKey == "defaultcfg" || rawKey == "defaultrunlogic" ||
				rawKey == "runtimesettings" || rawKey == "runtimefile")) {
		// Runtime archive lookup is intentionally case-insensitive and slash
		// agnostic, unlike executable strings and credentials below.
		return "path:" + strings.ToLower(normalizeRuntimeArchivePath(value))
	}
	if key == "autotransactions" &&
		(section == "runlogic" || section == "general") {
		if parsed, ok := parseBoolExplicit(value); ok {
			if parsed {
				return "enum:per_action"
			}
			return "enum:none"
		}
		return "enum:" + normalizeEnum(value)
	}
	if runtimeConfigBooleanField(section, key) {
		if parsed, ok := parseBoolExplicit(value); ok {
			return "bool:" + strconv.FormatBool(parsed)
		}
		return "bool-invalid:" + strings.ToLower(value)
	}
	if numericKind := runtimeConfigNumericField(section, key); numericKind != "" {
		if numericKind == "int" {
			if parsed, err := strconv.Atoi(value); err == nil {
				return "number:" + strconv.Itoa(parsed)
			}
		} else if numericKind == "seconds_ms" {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil &&
				!math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
				return "number:" + strconv.Itoa(int(math.Round(parsed*1000)))
			}
		} else if parsed, err := strconv.ParseFloat(value, 64); err == nil &&
			!math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			return "number:" + strconv.FormatFloat(parsed, 'g', -1, 64)
		}
		return "number-invalid:" + value
	}
	if normalized, ok := runtimeConfigEnumValue(occurrence, section, key, value); ok {
		return "enum:" + normalized
	}
	// Usernames, passwords, URLs, content-check text, action names, JVM
	// arguments, classpaths, and other opaque executable strings are
	// case-sensitive. EqualFold here would silently select one of two genuinely
	// different credentials or commands.
	return "opaque:" + value
}

func runtimeConfigBooleanField(section, key string) bool {
	switch section {
	case "thinktime":
		return key == "ignore" || key == "limitflag"
	case "general":
		return key == "autotransactions" || key == "continueonerror"
	case "http", "browser":
		switch key {
		case "enablehttp2", "keepalive", "usecustomagent", "touchmode",
			"clientmetrics", "cacheenabled", "cachecontenturls",
			"checknewerversions", "newuserperiteration", "simulateprefetch",
			"downloadnonhtmlresources", "proxyuseproxy", "proxyusesame",
			"proxynolocal", "snapshotonerroractive":
			return true
		}
	case "proxy":
		return key == "proxyuseproxy" || key == "proxyusesame" || key == "proxynolocal"
	case "logging":
		switch key {
		case "onerroronly", "parametersubstitution", "datareturned",
			"advancedtrace", "msgclassparameters", "msgclassdata", "msgclassfull":
			return true
		}
	case "misc":
		return key == "continueonerror" || key == "snapshotonerror"
	case "contentcheck":
		return key == "failiffound"
	case "dataformatting":
		return key == "autodetect"
	case "javavm":
		return key == "dedicatedclassloader" ||
			key == "usexbootclasspath" ||
			key == "skipparameterevaluation"
	}
	return false
}

func runtimeConfigNumericField(section, key string) string {
	switch section {
	case "thinktime":
		switch key {
		case "factor", "limit", "multiplier", "limitms",
			"thinktimerandomlow", "thinktimerandomhigh":
			return "float"
		}
	case "runlogic":
		if key == "iterations" {
			return "int"
		}
	case "iterations":
		switch key {
		case "iterations":
			return "int"
		case "startevery", "randommin", "randommax":
			return "seconds_ms"
		}
	case "pacing":
		switch key {
		case "delay", "mindelay", "maxdelay", "interval", "mininterval", "maxinterval":
			return "int"
		}
	case "http":
		switch key {
		case "connecttimeout", "receivetimeout", "stepdownloadtimeout",
			"maxredirects", "maxconnectionsperhost", "slowmoms",
			"viewportwidth", "viewportheight":
			return "int"
		}
	case "browser":
		if key == "slowmoms" || key == "viewportwidth" || key == "viewportheight" {
			return "int"
		}
	case "networksimulation":
		if key == "bandwidth" || key == "latencyms" {
			return "int"
		}
	case "streaming":
		if key == "buffersize" || key == "fixedbitrate" {
			return "int"
		}
	case "runlogicrunroot":
		switch compactRuntimeEnum(key) {
		case "runlogicnumofiterations":
			return "int"
		case "runlogicpaceconsttime", "runlogicrandompacemin", "runlogicrandompacemax",
			"runlogicpaceconstaftertime", "runlogicafterpacemin", "runlogicafterpacemax":
			return "seconds_ms"
		}
	}
	return ""
}

func runtimeConfigEnumValue(occurrence runtimeConfigOccurrence, section, key, value string) (string, bool) {
	switch {
	case section == "thinktime" && key == "options":
		return normalizeEnum(value), true
	case section == "pacing" && key == "mode":
		return mapPacingType(value), true
	case section == "iterations" && key == "iterationpace":
		return compactRuntimeEnum(value), true
	case section == "http" && key == "proxymode",
		section == "proxy" && key == "mode":
		if mapped, ok := mapRuntimeProxyMode(value); ok {
			return mapped, true
		}
		return normalizeEnum(value), true
	case (section == "http" || section == "browser") && key == "browsertype":
		mapped, version := mapSourceBrowserType(value)
		if mapped != "" {
			return mapped + ":" + version, true
		}
		return normalizeEnum(value), true
	case (section == "http" || section == "browser") && key == "browserplatform":
		if mapped := mapSourceBrowserPlatform(value); mapped != "" {
			return mapped, true
		}
		return normalizeEnum(value), true
	case (section == "http" || section == "browser") && key == "acceptencoding":
		return normalizeRuntimeAcceptEncoding(value), true
	case section == "logging" && key == "level":
		if compactRuntimeEnum(occurrence.key) == "logoptions" {
			switch compactRuntimeEnum(value) {
			case "logdisabled":
				return "disabled", true
			case "logbrief":
				return "standard", true
			case "logextended":
				return "extended", true
			default:
				return normalizeEnum(value), true
			}
		}
		return mapLogLevel(value), true
	case section == "security" && key == "tlsversion":
		if mapped, ok := mapSSLVersionExact(value); ok {
			return mapped, true
		}
		return normalizeEnum(value), true
	case section == "javavm" && key == "jdkselection":
		if mapped := normalizeJavaVMJDKSelection(value); mapped != "" {
			return mapped, true
		}
		return normalizeEnum(value), true
	}
	switch section {
	case "http", "browser":
		switch key {
		case "httpversion", "engine", "deviceprofile", "orientation", "downloadresources":
			return normalizeEnum(value), true
		}
	case "runlogic":
		if key == "autotransactions" || key == "blockscheduling" {
			return normalizeEnum(value), true
		}
	case "logging":
		if key == "level" {
			return normalizeEnum(value), true
		}
	case "downloadfilters", "networksimulation":
		if key == "mode" || key == "preset" {
			return normalizeEnum(value), true
		}
	case "contentcheck":
		if key == "scope" {
			return normalizeEnum(value), true
		}
	case "streaming":
		if key == "mode" || key == "bandwidthestimation" {
			return normalizeEnum(value), true
		}
	case "runlogicrunroot":
		if compactRuntimeEnum(key) == "runlogicpacetype" {
			return normalizeEnum(value), true
		}
	}
	return "", false
}

func runtimeConfigConflictMatches(conflict runtimeConfigCaseFoldConflict, sections, keys []string) bool {
	sectionMatch := false
	for _, section := range sections {
		if strings.EqualFold(strings.TrimSpace(conflict.section), strings.TrimSpace(section)) {
			sectionMatch = true
			break
		}
	}
	if !sectionMatch {
		return false
	}
	for _, key := range keys {
		if strings.EqualFold(strings.TrimSpace(conflict.key), strings.TrimSpace(key)) {
			return true
		}
	}
	return false
}

func runtimeConfigConflictEvidence(conflict runtimeConfigCaseFoldConflict) string {
	evidence := make([]string, 0, len(conflict.occurrences))
	for _, occurrence := range conflict.occurrences {
		value := occurrence.value
		if runtimeConfigEvidenceValueMustBeRedacted(occurrence) {
			value = credential.Redacted
		}
		evidence = append(evidence, fmt.Sprintf(
			"line %d [%s] %s=%q",
			occurrence.line,
			occurrence.section,
			occurrence.key,
			value,
		))
	}
	return strings.Join(evidence, ", ")
}

func runtimeConfigEvidenceValueMustBeRedacted(occurrence runtimeConfigOccurrence) bool {
	if credential.SensitiveName(occurrence.key) {
		return true
	}
	section := runtimeConfigCanonicalSection(occurrence.section)
	key := runtimeConfigCanonicalKey(section, occurrence.key)
	// Proxy endpoints may carry scheme-less userinfo (user:pass@host), and
	// boolean/mode fields are attacker-controlled source text in diagnostics.
	// Preserve the source key and line for remediation, but never echo any raw
	// value from a proxy section or proxy-named key.
	if section == "proxy" ||
		strings.Contains(compactRuntimeEnum(key), "proxy") ||
		(section == "web" && (key == "mode" || key == "type")) {
		return true
	}
	// JVM option rows are arbitrary command-line text and commonly contain
	// -D...password/token/secret assignments whose sensitivity cannot be inferred
	// from the INI key. Keep line/key provenance, but redact the whole value.
	if section == "javavm" &&
		(key == "javavmparameters" || func() bool {
			_, ok := javaVMParameterExtraArgIndex(occurrence.key)
			return ok
		}()) {
		return true
	}
	// URL-valued settings may hide userinfo or credentials under otherwise
	// harmless-looking query names. Conflict evidence needs the source key and
	// line, not the raw endpoint value.
	return strings.Contains(occurrence.value, "://")
}

func applyRuntimeCfgCaseFoldConflicts(rc *RuntimeConfig, content string) {
	if rc == nil {
		return
	}
	sections := ParseConfigSections(content)
	thinkTimeSection := findMergedSection(sections, "Think Time", "ThinkTime")
	thinkMode, _ := sectionValueCaseInsensitive(thinkTimeSection, "Options")
	thinkMode = strings.ToUpper(strings.TrimSpace(thinkMode))
	_, hasFormalLimitFlag := sectionValueCaseInsensitive(thinkTimeSection, "LimitFlag")
	limitEnabled := false
	if rawLimitFlag, ok := sectionValueCaseInsensitive(thinkTimeSection, "LimitFlag"); ok {
		limitEnabled, _ = parseBoolExplicit(rawLimitFlag)
	}
	selectedPacingType := rc.PacingType
	activeState := runtimeCfgConflictActivation{
		proxyMode:               rc.ProxyMode,
		proxyExplicitlyDisabled: runtimeCfgProxyExplicitlyDisabled(sections),
		cacheDisabled:           rc.BESimulateCache != nil && !*rc.BESimulateCache,
		customUserAgent:         rc.UserAgent != "" && (rc.BEUseCustomUA == nil || *rc.BEUseCustomUA),
		logLevel:                rc.LogLevel,
		javaJDK:                 rc.JavaVMJDKSelection,
	}
	for _, conflict := range runtimeConfigCaseFoldConflicts(content) {
		switch {
		case runtimeConfigConflictMatches(
			conflict,
			[]string{"Think Time", "ThinkTime"},
			[]string{
				"Options", "Factor", "Limit", "LimitFlag",
				"Ignore", "IgnoreThinkTime",
				"Multiplier", "MultiplyRecordedThinkTimeBy",
				"LimitMs", "LimitThinkTimeMs",
				"ThinkTimeRandomLow", "ThinkTimeRandomHigh",
			},
		):
			if !runtimeCfgThinkTimeConflictIsActive(conflict, thinkMode, hasFormalLimitFlag, limitEnabled) {
				continue
			}
			resetRuntimeThinkTime(rc)
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf(
					"Conflicting case-insensitive think-time declarations (%s); using replay-as-recorded at 100%% with no cap and requiring review.",
					runtimeConfigConflictEvidence(conflict),
				),
			})
		case runtimeConfigConflictMatches(
			conflict,
			[]string{"Pacing"},
			[]string{
				"Mode", "PacingMode",
				"DelayMs", "Delay", "MinDelayMs", "MinDelay", "MaxDelayMs", "MaxDelay",
				"IntervalMs", "Interval", "MinIntervalMs", "MinInterval", "MaxIntervalMs", "MaxInterval",
			},
		), runtimeConfigConflictMatches(
			conflict,
			[]string{"Iterations"},
			[]string{"IterationPace", "StartEvery", "RandomMin", "RandomMax"},
		):
			if !runtimeCfgPacingConflictIsActive(conflict, selectedPacingType) {
				continue
			}
			resetRuntimePacing(rc)
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "pacing", Area: "Pacing",
				Notes: fmt.Sprintf(
					"Conflicting case-insensitive pacing declarations (%s); using no pacing and requiring review.",
					runtimeConfigConflictEvidence(conflict),
				),
			})
		case runtimeConfigConflictMatches(
			conflict,
			[]string{"Run Logic", "RunLogic", "Iterations"},
			[]string{"Iterations", "NumberOfIterations", "NumOfIterations"},
		):
			rc.Iterations = 1
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "run_logic_iterations", Area: "Run Logic",
				Notes: fmt.Sprintf(
					"Conflicting case-insensitive iteration declarations (%s); using 1 iteration and requiring review.",
					runtimeConfigConflictEvidence(conflict),
				),
			})
		default:
			entry, ok := runtimeSupportedConflictEntry(conflict)
			if !ok {
				continue
			}
			if !runtimeSupportedConflictIsActive(activeState, entry, conflict) {
				continue
			}
			discardRuntimeIssueForConflict(rc, entry.ID, conflict)
			resetRuntimeSupportedConflict(rc, entry.ID, conflict)
			conflictSection := runtimeConfigCanonicalSection(conflict.section)
			conflictKey := runtimeConfigCanonicalKey(conflictSection, conflict.key)
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: entry.ID, Area: entry.Area,
				sourceKey: conflictKey,
				Notes: fmt.Sprintf(
					"Conflicting case-insensitive or synonymous runtime declarations (%s); using APEX defaults for this setting and requiring review.",
					runtimeConfigConflictEvidence(conflict),
				),
			})
		}
	}
}

type runtimeCfgConflictActivation struct {
	proxyMode               string
	proxyExplicitlyDisabled bool
	cacheDisabled           bool
	customUserAgent         bool
	logLevel                string
	javaJDK                 string
}

func runtimeCfgProxyExplicitlyDisabled(sections map[string]map[string]string) bool {
	var flagTrue, flagFalse bool
	var modeActive, modeNone bool
	for sectionName, section := range sections {
		if !strings.EqualFold(strings.TrimSpace(sectionName), "Proxy") &&
			!strings.EqualFold(strings.TrimSpace(sectionName), "WEB") {
			continue
		}
		for key, raw := range section {
			canonicalSection := runtimeConfigCanonicalSection(sectionName)
			canonicalKey := runtimeConfigCanonicalKey(canonicalSection, key)
			switch canonicalKey {
			case "proxyuseproxy":
				if enabled, ok := parseBoolExplicit(raw); ok {
					if enabled {
						flagTrue = true
					} else {
						flagFalse = true
					}
				}
			case "mode", "proxymode":
				if mode, ok := mapRuntimeProxyMode(raw); ok {
					if mode == "none" {
						modeNone = true
					} else if mode == "custom" || mode == "system" {
						modeActive = true
					}
				}
			}
		}
	}
	// The explicit enable flag is authoritative when present. Contradictory
	// flags are not a disable; their own conflict remains active and reviewable.
	if flagTrue || flagFalse {
		return flagFalse && !flagTrue
	}
	return modeNone && !modeActive
}

func runtimeSupportedConflictIsActive(
	state runtimeCfgConflictActivation,
	entry RuntimeSettingSupportEntry,
	conflict runtimeConfigCaseFoldConflict,
) bool {
	section := runtimeConfigCanonicalSection(conflict.section)
	key := runtimeConfigCanonicalKey(section, conflict.key)
	switch entry.ID {
	case "proxy":
		// The enable/mode selector is always meaningful. Endpoint, credential,
		// and bypass rows are dormant when proxying is explicitly disabled.
		switch key {
		case "mode", "proxymode", "proxyuseproxy":
			return true
		default:
			if state.proxyExplicitlyDisabled {
				return false
			}
			// System proxying does not consume caller-supplied endpoint or
			// credential children.
			return state.proxyMode != "system"
		}
	case "browser_cache_content_urls", "browser_check_newer_versions":
		return !state.cacheDisabled
	case "browser_selection":
		return !state.customUserAgent
	case "logging":
		switch key {
		case "extendedoptions", "parametersubstitution", "datareturned",
			"advancedtrace", "msgclassparameters", "msgclassdata", "msgclassfull":
			return state.logLevel == "extended"
		default:
			return true
		}
	case "java_vm_jdk":
		if key == "jdkhome" {
			return state.javaJDK == "specified"
		}
	}
	return true
}

func runtimeCfgThinkTimeConflictIsActive(conflict runtimeConfigCaseFoldConflict, thinkMode string, hasFormalLimitFlag, limitEnabled bool) bool {
	key := strings.ToLower(strings.TrimSpace(conflict.key))
	switch key {
	case "options", "ignore", "ignorethinktime", "limitflag":
		return true
	case "factor":
		return thinkMode == "MULTIPLY"
	case "thinktimerandomlow", "thinktimerandomhigh":
		return thinkMode == "RANDOM"
	case "limit":
		return limitEnabled
	case "multiplier", "multiplyrecordedthinktimeby":
		return thinkMode == ""
	case "limitms", "limitthinktimems":
		return !hasFormalLimitFlag
	default:
		return false
	}
}

func runtimeCfgPacingConflictIsActive(conflict runtimeConfigCaseFoldConflict, selectedPacingType string) bool {
	key := strings.ToLower(strings.TrimSpace(conflict.key))
	switch key {
	case "mode", "pacingmode", "iterationpace":
		return true
	case "delayms", "delay":
		return selectedPacingType == "fixed_delay"
	case "mindelayms", "mindelay", "maxdelayms", "maxdelay":
		return selectedPacingType == "random_delay"
	case "intervalms", "interval":
		return selectedPacingType == "fixed_interval"
	case "minintervalms", "mininterval", "maxintervalms", "maxinterval":
		return selectedPacingType == "random_interval"
	case "startevery":
		return selectedPacingType == "fixed_interval" || selectedPacingType == "fixed_delay"
	case "randommin", "randommax":
		return selectedPacingType == "random_interval" || selectedPacingType == "random_delay"
	default:
		return false
	}
}

func resetRuntimeThinkTime(rc *RuntimeConfig) {
	if rc == nil {
		return
	}
	rc.ThinkTimeType = "recorded"
	rc.ThinkTimeIgnore = false
	rc.ThinkTimeMultiplier = 1
	rc.ThinkTimeLimitMs = 0
}

func runtimeSupportedConflictEntry(conflict runtimeConfigCaseFoldConflict) (RuntimeSettingSupportEntry, bool) {
	for _, definitions := range [][]runtimeSettingSupportDefinition{
		sourceRTSSupportDefinitions,
		javaVuserRTSSupportDefinitions,
	} {
		for _, definition := range definitions {
			if definition.entry.Status != RuntimeSettingStatusExecuted &&
				definition.entry.ID != "java_vm_xbootclasspath" {
				continue
			}
			for _, matcher := range definition.matchers {
				for _, occurrence := range conflict.occurrences {
					if runtimeMatcherMatchesOccurrence(matcher, occurrence) {
						return definition.entry, true
					}
				}
			}
		}
	}
	return RuntimeSettingSupportEntry{}, false
}

func runtimeMatcherMatchesOccurrence(matcher runtimeSettingMatcher, occurrence runtimeConfigOccurrence) bool {
	return matcher.matchesSection(occurrence.section) &&
		matcher.matchesKey(occurrence.key) &&
		matcher.matchesValue(occurrence.value)
}

func discardRuntimeIssueForConflict(rc *RuntimeConfig, id string, conflict runtimeConfigCaseFoldConflict) {
	if rc == nil || len(rc.RuntimeIssues) == 0 {
		return
	}
	section := runtimeConfigCanonicalSection(conflict.section)
	key := runtimeConfigCanonicalKey(section, conflict.key)
	kept := rc.RuntimeIssues[:0]
	for _, issue := range rc.RuntimeIssues {
		if issue.ID == id && issue.sourceKey != "" && issue.sourceKey == key {
			continue
		}
		kept = append(kept, issue)
	}
	rc.RuntimeIssues = kept
}

func resetRuntimeSupportedConflict(rc *RuntimeConfig, id string, conflict runtimeConfigCaseFoldConflict) {
	if rc == nil {
		return
	}
	section := runtimeConfigCanonicalSection(conflict.section)
	key := runtimeConfigCanonicalKey(section, conflict.key)
	switch id {
	case "http_transport":
		switch key {
		case "connecttimeout":
			rc.ConnectTimeoutSec = 0
		case "receivetimeout":
			rc.ReceiveTimeoutSec = 0
		case "stepdownloadtimeout":
			rc.StepTimeoutSec = 0
		case "httpversion":
			rc.HTTPVersion = "1.1"
		case "keepalive":
			rc.KeepAlive = true
		case "maxconnectionsperhost":
			rc.MaxConnsPerHost = 0
		case "enablehttp2":
			rc.EnableHTTP2 = nil
			rc.EnableHTTP2Raw = ""
		case "maxredirects":
			rc.MaxRedirects = 10
		}
	case "proxy":
		disableRuntimeProxy(rc)
	case "request_headers":
		switch key {
		case "useragent":
			rc.UserAgent = ""
		case "acceptlanguage":
			rc.AcceptLanguage = ""
			rc.BELanguage = ""
		case "acceptencoding":
			rc.AcceptEncoding = ""
			rc.UseGzip = false
		case "usecustomagent":
			rc.UserAgent = ""
			rc.BEUseCustomUA = nil
			rc.BEUseCustomUARaw = ""
			rc.BEUseCustomUAParseError = ""
		}
	case "browser_selection":
		switch key {
		case "browsertype":
			rc.BEBrowserType = ""
			rc.BEBrowserTypeRaw = ""
		case "browserversion":
			rc.BEBrowserVersion = ""
		case "browserplatform":
			rc.BEPlatform = ""
			rc.BEPlatformRaw = ""
		}
	case "download_non_html_resources":
		rc.BEDownloadNonHTML = nil
		rc.BEDownloadNonHTMLRaw = ""
		rc.BEDownloadNonHTMLParseError = ""
	case "tls":
		rc.SSLVersion = ""
	case "authentication":
		rc.AuthUser = ""
		rc.AuthPass = ""
	case "run_logic_auto_transactions":
		rc.AutoTransactions = ""
	case "run_logic_block_scheduling":
		rc.BlockScheduling = ""
	case "run_logic_action_sequence":
		rc.ActionSequence = nil
	case "logging":
		switch key {
		case "level", "logoptions":
			rc.LogLevel = ""
		case "onerroronly":
			rc.OnErrorOnly = false
		default:
			rc.LogExtendedOptions = nil
		}
	case "continue_on_error":
		rc.ContinueOnError = false
	case "snapshot_on_error":
		rc.SnapshotOnError = false
	case "browser_device_emulation":
		switch key {
		case "engine":
			rc.BrowserEngine = ""
		case "deviceprofile":
			rc.BrowserDeviceProfile = ""
		case "touchmode":
			rc.BrowserTouchMode = false
		case "orientation":
			rc.BrowserOrientation = ""
		case "clientmetrics":
			rc.BrowserClientMetrics = false
		case "slowmoms":
			rc.BrowserSlowMoMs = 0
		case "viewportwidth":
			rc.BrowserViewportWidth = 0
		case "viewportheight":
			rc.BrowserViewportHeight = 0
		}
	case "browser_cache_enabled":
		rc.CacheEnabled = false
		rc.BESimulateCache = nil
		rc.BESimulateCacheRaw = ""
		rc.BESimulateCacheParseError = ""
	case "browser_cache_content_urls":
		rc.CacheContentURLs = false
		rc.BECacheContentURLs = nil
		rc.BECacheContentURLsRaw = ""
		rc.BECacheContentURLsParseError = ""
	case "browser_check_newer_versions":
		rc.CheckNewerVersions = false
		rc.BECheckNewerVersions = nil
		rc.BECheckNewerVersionsRaw = ""
		rc.BECheckNewerParseError = ""
	case "browser_new_user_per_iteration":
		rc.NewUserPerIteration = false
		rc.BENewUserEachIteration = nil
		rc.BENewUserEachIterationRaw = ""
		rc.BENewUserParseError = ""
	case "browser_download_resources_all", "browser_download_resources_none", "browser_download_resources_recorded":
		rc.DownloadResources = ""
	case "simulate_prefetch_prerender":
		rc.BESimulatePrefetch = nil
		rc.BESimulatePrefetchRaw = ""
		rc.BESimulatePrefetchParseError = ""
	case "download_filters":
		rc.DownloadFilterMode = ""
		rc.DownloadFilterHosts = nil
	case "content_checks":
		rc.ContentChecks = nil
	case "network_simulation":
		switch key {
		case "mode":
			rc.NetworkSimulationMode = ""
		case "preset":
			rc.NetworkSimulationPreset = ""
		case "bandwidth":
			rc.NetworkBandwidthKbps = 0
		case "latencyms":
			rc.NetworkLatencyMs = 0
		}
	case "streaming":
		switch key {
		case "mode":
			rc.StreamingMode = ""
		case "buffersize":
			rc.StreamingBufferSizeSeconds = 0
		case "fixedbitrate":
			rc.StreamingFixedBitrateKbps = 0
		case "bandwidthestimation":
			rc.StreamingBandwidthEstimation = ""
		}
	case "data_formatting_auto_detect":
		rc.DataFormattingAutoDetect = false
	case "data_formatting_chain":
		rc.DataFormattingChain = nil
	case "java_vm_jdk":
		rc.JavaVMJDKSelection = ""
		rc.JavaVMJDKSelectionRaw = ""
		rc.JavaVMJDKSelectionParseError = ""
		rc.JavaVMJDKHome = ""
	case "java_vm_parameters":
		rc.JavaVMAdditionalParameters = nil
		rc.JavaVMParametersRaw = ""
		rc.JavaVMParameterRows = nil
		rc.JavaVMParametersParseError = ""
	case "java_vm_classpath":
		rc.JavaVMClasspath = nil
		rc.JavaVMClasspathRaw = ""
		rc.JavaVMClasspathParseError = ""
	case "java_vm_dedicated_class_loader":
		rc.JavaVMDedicatedClassLoader = nil
		rc.JavaVMDedicatedClassLoaderRaw = ""
		rc.JavaVMDedicatedParseError = ""
	case "java_vm_xbootclasspath":
		rc.JavaVMUseXBootClasspath = nil
		rc.JavaVMUseXBootClasspathRaw = ""
		rc.JavaVMUseXBootParseError = ""
	case "java_vm_skip_parameter_evaluation":
		rc.JavaVMSkipParameterEvaluation = nil
		rc.JavaVMSkipParameterEvaluationRaw = ""
		rc.JavaVMSkipParamEvalParseError = ""
	}
}

// sectionValueCaseInsensitive selects the first alias deterministically while
// tolerating the inconsistent key casing emitted across runtime.cfg versions.
func sectionValueCaseInsensitive(section map[string]string, aliases ...string) (string, bool) {
	_, value, ok := sectionEntryCaseInsensitive(section, aliases...)
	return value, ok
}

func runtimeSectionValue(section map[string]string, aliases ...string) string {
	value, _ := sectionValueCaseInsensitive(section, aliases...)
	return value
}

// runtimeSectionFirstNonBlank preserves the source alias precedence while
// treating INI keys case-insensitively. Passing every alias to
// sectionValueCaseInsensitive at once would let a present-but-empty preferred
// alias hide the next non-empty fallback.
func runtimeSectionFirstNonBlank(section map[string]string, aliases ...string) string {
	for _, alias := range aliases {
		if value, ok := sectionValueCaseInsensitive(section, alias); ok &&
			strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// sectionEntryCaseInsensitive is sectionValueCaseInsensitive with the matched
// key, for callers that name the source line in a parse error.
func sectionEntryCaseInsensitive(section map[string]string, aliases ...string) (string, string, bool) {
	for _, alias := range aliases {
		if value, ok := section[alias]; ok {
			return alias, value, true
		}
		matchingKeys := make([]string, 0, 1)
		for key := range section {
			if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(alias)) {
				matchingKeys = append(matchingKeys, key)
			}
		}
		if len(matchingKeys) > 0 {
			sort.Strings(matchingKeys)
			return matchingKeys[0], section[matchingKeys[0]], true
		}
	}
	return "", "", false
}

type javaVMParameterRow struct {
	key   string
	value string
	line  int
}

type javaVMParameterRejection struct {
	sourceKey  string
	sourceLine int
	tokenIndex int
	category   string
}

type javaVMParameterTokenProvenance struct {
	sourceKey  string
	sourceLine int
}

// javaVMParameterKeyAliases lists the base keys that carry the "Additional VM
// Parameters" line. LoadRunner spills a long line across numbered companion
// keys (Java_VM_Params_extra_arg_1, _extra_arg_2, ...), so reading only the
// base key silently drops every argument past the first row.
//
// Like the other Java VM aliases in this file, the non-canonical spellings and
// the numbered suffix are documented best guesses pending a real LR Java
// default.cfg sample; the forgiving list costs nothing when a key is absent.
var javaVMParameterKeyAliases = []string{"Java_VM_Params", "java_vm_parameters", "AdditionalVmParams", "ExtraVmParams", "VmParams"}

// orderedJavaVMParameterRows returns the base row followed by the numbered
// spill rows in source order. sectionValueCaseInsensitive matches whole keys
// with EqualFold, so the base lookup cannot swallow a numbered key and the
// numbered scan cannot swallow the base one.
func orderedJavaVMParameterRows(section map[string]string) []javaVMParameterRow {
	rows := make([]javaVMParameterRow, 0, 1)
	if key, value, ok := sectionEntryCaseInsensitive(section, javaVMParameterKeyAliases...); ok {
		rows = append(rows, javaVMParameterRow{key: key, value: value})
	}

	type indexedRow struct {
		index int
		row   javaVMParameterRow
	}
	indexed := make([]indexedRow, 0, len(section))
	for key, value := range section {
		trimmedKey := strings.TrimSpace(key)
		index, ok := javaVMParameterExtraArgIndex(trimmedKey)
		if !ok {
			continue
		}
		indexed = append(indexed, indexedRow{index: index, row: javaVMParameterRow{key: trimmedKey, value: value}})
	}
	// Sort on the parsed number, not the key text: an ASCII sort would place
	// _extra_arg_10 before _extra_arg_2 and reorder the command line.
	sort.Slice(indexed, func(left, right int) bool {
		if indexed[left].index != indexed[right].index {
			return indexed[left].index < indexed[right].index
		}
		return indexed[left].row.key < indexed[right].row.key
	})
	for _, item := range indexed {
		rows = append(rows, item.row)
	}
	return rows
}

// javaVMParameterExtraArgIndex reports the 1-based row number for a numbered
// spill key such as Java_VM_Params_extra_arg_3.
func javaVMParameterExtraArgIndex(key string) (int, bool) {
	const marker = "_extra_arg_"
	// Index the lowercased form consistently. strings.ToLower is NOT
	// byte-length preserving (U+023A is 2 bytes, its lowercase U+2C65 is 3), so
	// locating the marker in the lowercased string and then slicing the original
	// panics on a key built from such runes — reachable from any uploaded
	// runtime.cfg. Slicing the lowercased string is safe here because the base
	// is compared with EqualFold and the suffix must be ASCII digits anyway.
	lowerKey := strings.ToLower(key)
	markerAt := strings.LastIndex(lowerKey, marker)
	if markerAt <= 0 {
		return 0, false
	}
	base := lowerKey[:markerAt]
	matched := false
	for _, alias := range javaVMParameterKeyAliases {
		if strings.EqualFold(base, alias) {
			matched = true
			break
		}
	}
	if !matched {
		return 0, false
	}
	index, err := strconv.Atoi(lowerKey[markerAt+len(marker):])
	if err != nil || index <= 0 {
		return 0, false
	}
	return index, true
}

// runtimeConfigSourceSettingLine locates the source line used by the merged
// Java VM section. Value matching prevents an overwritten duplicate in another
// section alias from receiving the wrong provenance.
func runtimeConfigSourceSettingLine(content, key, value string) int {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	section := ""
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if match := reSectionHeader.FindStringSubmatch(line); len(match) > 1 {
			section = match[1]
			continue
		}
		if runtimeConfigCanonicalSection(section) != "javavm" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 ||
			!strings.EqualFold(strings.TrimSpace(parts[0]), strings.TrimSpace(key)) ||
			strings.TrimSpace(parts[1]) != value {
			continue
		}
		return lineNumber
	}
	return 0
}

// validateImportedJavaVMParameters layers import-time disclosure recognition
// over the governed runner's structural policy. Sensitive values are review
// findings, not structural rejections: the importing operator already has the
// value and exact argv fidelity requires retaining it in the destination
// script. Unsafe JVM process-control options remain rejected separately.
func validateImportedJavaVMParameters(parameters []string) []javarunner.ValidationIssue {
	issues := javarunner.ValidateVMParameters(parameters)
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		seen[fmt.Sprintf("%d\x00%s", issue.Index, issue.Category)] = struct{}{}
	}
	for index, parameter := range parameters {
		sensitive := strings.Contains(parameter, credential.Redacted)
		if strings.HasPrefix(parameter, "-D") {
			assignment := parameter[2:]
			if separator := strings.IndexByte(assignment, '='); separator > 0 {
				sensitive = sensitive ||
					credential.SensitiveName(strings.TrimSpace(assignment[:separator]))
			}
		}
		projected := scrub.Value(parameter, scrub.Policy{})
		// A textual rewrite alone is not evidence of disclosure: structured
		// scrubbers may normalize or compact safe JSON. Only an actual redaction
		// sentinel (or an explicitly credential-bearing URL) rejects the token.
		sensitive = sensitive ||
			strings.Contains(projected, credential.Redacted) ||
			credentialpolicy.ContainsCredentialURL(parameter)
		if !sensitive {
			continue
		}
		identity := fmt.Sprintf("%d\x00%s", index, javarunner.VMParameterSensitiveProperty)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		issues = append(issues, javarunner.ValidationIssue{
			Index:    index,
			Category: javarunner.VMParameterSensitiveProperty,
		})
	}
	sort.SliceStable(issues, func(left, right int) bool {
		if issues[left].Index != issues[right].Index {
			return issues[left].Index < issues[right].Index
		}
		return issues[left].Category < issues[right].Category
	})
	return issues
}

func javaVMParameterIssuesBySensitivity(
	issues []javarunner.ValidationIssue,
	sensitive bool,
) []javarunner.ValidationIssue {
	filtered := make([]javarunner.ValidationIssue, 0, len(issues))
	for _, issue := range issues {
		if (issue.Category == javarunner.VMParameterSensitiveProperty) != sensitive {
			continue
		}
		filtered = append(filtered, issue)
	}
	return filtered
}

func validateImportedJavaVMParameterStructure(parameters []string) []javarunner.ValidationIssue {
	return javaVMParameterIssuesBySensitivity(
		validateImportedJavaVMParameters(parameters),
		false,
	)
}

func executableJavaVMParameters(parameters []string) []string {
	if len(parameters) == 0 || len(validateImportedJavaVMParameters(parameters)) > 0 {
		return nil
	}
	return append([]string(nil), parameters...)
}

func javaVMParametersWithoutStructuralIssues(
	parameters []string,
	issues []javarunner.ValidationIssue,
) []string {
	if len(parameters) == 0 {
		return nil
	}
	rejected := make(map[int]struct{}, len(issues))
	for _, issue := range issues {
		if issue.Category == javarunner.VMParameterSensitiveProperty {
			continue
		}
		if issue.Index >= 0 && issue.Index < len(parameters) {
			rejected[issue.Index] = struct{}{}
		}
	}
	if len(rejected) == 0 {
		return append([]string(nil), parameters...)
	}
	out := make([]string, 0, len(parameters)-len(rejected))
	for index, parameter := range parameters {
		if _, omit := rejected[index]; omit {
			continue
		}
		out = append(out, parameter)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func disclosureSafeJavaVMParameterRow(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	parameters, err := splitQuotedRuntimeArguments(value)
	if err != nil || len(validateImportedJavaVMParameters(parameters)) > 0 {
		return credential.Redacted
	}
	return value
}

// parseRawRuntimeConfigSections preserves values exactly apart from the
// whitespace around the key/value delimiter. ParseConfigSections intentionally
// performs C-string normalization that is useful for most source settings but
// unsafe for argv and classpath lists.
func parseRawRuntimeConfigSections(content string) map[string]map[string]string {
	sections := make(map[string]map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if match := reSectionHeader.FindStringSubmatch(line); len(match) > 1 {
			section = match[1]
			if _, ok := sections[section]; !ok {
				sections[section] = make(map[string]string)
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if _, ok := sections[section]; !ok {
			sections[section] = make(map[string]string)
		}
		sections[section][strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	if len(sections[""]) == 0 {
		delete(sections, "")
	}
	return sections
}

// cleanRawRuntimeScalar removes optional grouping quotes without interpreting
// C escapes. Runtime CFG path values are literal platform paths, so decoding a
// quoted Windows path such as C:\tools would corrupt "\t" into a tab.
func cleanRawRuntimeScalar(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 {
		return trimmed
	}
	first := trimmed[0]
	last := trimmed[len(trimmed)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	return trimmed
}

func normalizeJavaVMJDKSelection(value string) string {
	switch normalizeEnum(value) {
	case "search_jdk", "search", "internal", "internal_logic", "false", "0":
		return "internal"
	case "specified_jdk", "specified", "user_specified", "true", "1":
		return "specified"
	default:
		return ""
	}
}

// splitQuotedRuntimeArguments tokenizes the source JVM parameter string
// without shell expansion. Quotes group spaces and are removed; backslashes
// only escape whitespace or quotes so UNC paths and Java escape sequences
// retain every path separator.
func splitQuotedRuntimeArguments(value string) ([]string, error) {
	arguments := make([]string, 0)
	var token strings.Builder
	var quote byte
	tokenStarted := false
	flush := func() {
		if !tokenStarted {
			return
		}
		arguments = append(arguments, token.String())
		token.Reset()
		tokenStarted = false
	}

	for index := 0; index < len(value); index++ {
		character := value[index]
		if quote != 0 {
			if character == quote {
				quote = 0
				continue
			}
			if character == '\\' && quote != '\'' && index+1 < len(value) && runtimeArgumentEscapable(value[index+1]) {
				index++
				token.WriteByte(value[index])
				tokenStarted = true
				continue
			}
			token.WriteByte(character)
			tokenStarted = true
			continue
		}

		switch {
		case character == '\'' || character == '"':
			quote = character
			tokenStarted = true
		case character == '\\' && index+1 < len(value) && runtimeArgumentEscapable(value[index+1]):
			index++
			token.WriteByte(value[index])
			tokenStarted = true
		case character == ' ' || character == '\t' || character == '\r' || character == '\n':
			flush()
		default:
			token.WriteByte(character)
			tokenStarted = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted JVM parameter")
	}
	flush()
	return arguments, nil
}

func runtimeArgumentEscapable(value byte) bool {
	return value == '\'' || value == '"' || value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func splitJavaVMClasspath(value string) ([]string, error) {
	classpath := make([]string, 0)
	var entry strings.Builder
	var quote byte
	flush := func() {
		if trimmed := strings.TrimSpace(entry.String()); trimmed != "" {
			classpath = append(classpath, trimmed)
		}
		entry.Reset()
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if quote != 0 {
			if character == quote {
				quote = 0
				continue
			}
			if character == '\\' && index+1 < len(value) && value[index+1] == quote {
				index++
				entry.WriteByte(value[index])
				continue
			}
			entry.WriteByte(character)
			continue
		}
		switch character {
		case '"':
			quote = character
		case '\'':
			// Apostrophes are valid path characters. Treat a single quote as
			// grouping syntax only at the beginning of a delimited entry.
			if strings.TrimSpace(entry.String()) == "" {
				quote = character
			} else {
				entry.WriteByte(character)
			}
		case ';', '\r', '\n':
			// LoadRunner Java Vuser classpaths are Windows-semicolon-delimited.
			// Deliberately do not split ':' so drive letters survive Linux import.
			flush()
		default:
			entry.WriteByte(character)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted Java VM classpath entry")
	}
	flush()
	return classpath, nil
}

func cloneRuntimeBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}

func parseRuntimeCfgIterations(rc *RuntimeConfig, sections map[string]map[string]string) {
	if rc == nil {
		return
	}
	runLogicSection := findMergedSection(sections, "Run Logic", "RunLogic")
	iterationsSection, hasIterationsSection := runtimeConfigSection(sections, "Iterations")
	raw, present := firstRuntimeSectionValue(
		[]map[string]string{runLogicSection, iterationsSection},
		"Iterations", "NumberOfIterations", "NumOfIterations",
	)
	if !present {
		if hasIterationsSection {
			rc.Iterations = 1
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "run_logic_iterations", Area: "Run Logic",
				Notes: "The cfg [Iterations] section does not define an iteration count; using 1 iteration and requiring review.",
			})
		}
		return
	}
	raw = strings.TrimSpace(raw)
	value, err := strconv.Atoi(raw)
	if raw == "" || err != nil || value < 1 || value > dsl.MaxRunIterations {
		rc.Iterations = 1
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "run_logic_iterations", Area: "Run Logic",
			Notes: fmt.Sprintf("The cfg iteration count %q is missing, invalid, or outside 1..%d; using 1 iteration and requiring review.", raw, dsl.MaxRunIterations),
		})
		return
	}
	rc.Iterations = value
}

func parseRuntimeCfgPacing(rc *RuntimeConfig, sections map[string]map[string]string) {
	if rc == nil {
		return
	}
	pacingSection, present := runtimeConfigSection(sections, "Pacing")
	if !present {
		parseLoadRunnerIterationsPacing(rc, sections)
		return
	}
	resetRuntimePacing(rc)
	rawMode, hasMode := sectionValueCaseInsensitive(pacingSection, "Mode", "PacingMode")
	mode := mapPacingType(rawMode)
	if !hasMode || strings.TrimSpace(rawMode) == "" {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: "The cfg [Pacing] section does not define Mode/PacingMode; using no pacing and requiring review.",
		})
		return
	}

	switch mode {
	case "none":
		return
	case "fixed_delay":
		value, ok := parseRuntimeCfgPacingMilliseconds(rc, pacingSection, "fixed delay", "DelayMs", "Delay")
		if ok {
			rc.PacingType = mode
			rc.DelayMs = value
		}
	case "random_delay":
		low, lowOK := parseRuntimeCfgPacingMilliseconds(rc, pacingSection, "random delay minimum", "MinDelayMs", "MinDelay")
		high, highOK := parseRuntimeCfgPacingMilliseconds(rc, pacingSection, "random delay maximum", "MaxDelayMs", "MaxDelay")
		if lowOK && highOK && validateRuntimeCfgPacingBounds(rc, "random delay", low, high) {
			rc.PacingType = mode
			rc.MinDelayMs = low
			rc.MaxDelayMs = high
		}
	case "fixed_interval":
		value, ok := parseRuntimeCfgPacingMilliseconds(rc, pacingSection, "fixed interval", "IntervalMs", "Interval")
		if ok {
			rc.PacingType = mode
			rc.IntervalMs = value
		}
	case "random_interval":
		low, lowOK := parseRuntimeCfgPacingMilliseconds(rc, pacingSection, "random interval minimum", "MinIntervalMs", "MinInterval")
		high, highOK := parseRuntimeCfgPacingMilliseconds(rc, pacingSection, "random interval maximum", "MaxIntervalMs", "MaxInterval")
		if lowOK && highOK && validateRuntimeCfgPacingBounds(rc, "random interval", low, high) {
			rc.PacingType = mode
			rc.MinIntervalMs = low
			rc.MaxIntervalMs = high
		}
	default:
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("The cfg pacing mode %q is unsupported; using no pacing and requiring review.", rawMode),
		})
	}
}

func parseLoadRunnerIterationsPacing(rc *RuntimeConfig, sections map[string]map[string]string) {
	iterations, present := runtimeConfigSection(sections, "Iterations")
	if !present {
		return
	}
	rawMode, hasMode := sectionValueCaseInsensitive(iterations, "IterationPace")
	rawStartEvery, hasStartEvery := sectionValueCaseInsensitive(iterations, "StartEvery")
	rawRandomMin, hasRandomMin := sectionValueCaseInsensitive(iterations, "RandomMin")
	rawRandomMax, hasRandomMax := sectionValueCaseInsensitive(iterations, "RandomMax")
	if !hasMode && !hasStartEvery && !hasRandomMin && !hasRandomMax {
		return
	}

	resetRuntimePacing(rc)
	mode := compactRuntimeEnum(rawMode)
	if !hasMode || mode == "" {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: "The cfg [Iterations] pacing fields do not define IterationPace; using no pacing and requiring review.",
		})
		return
	}

	switch mode {
	case "iterationasap", "asap", "none":
		return
	case "iterationfixed", "iterationconst", "iterationfixedinterval", "fixed", "const", "fixedinterval", "startevery":
		value, ok := runtimeCfgPacingSecondsToMS(rc, "StartEvery", rawStartEvery)
		if ok {
			rc.PacingType = "fixed_interval"
			rc.IntervalMs = value
		}
	case "iterationrandom", "iterationrandominterval", "random", "randominterval":
		low, lowOK := runtimeCfgPacingSecondsToMS(rc, "RandomMin", rawRandomMin)
		high, highOK := runtimeCfgPacingSecondsToMS(rc, "RandomMax", rawRandomMax)
		if lowOK && highOK && validateRuntimeBounds(rc, "cfg [Iterations]", "random interval", &low, &high) {
			rc.PacingType = "random_interval"
			rc.MinIntervalMs = low
			rc.MaxIntervalMs = high
		}
	case "iterationfixedafter", "iterationconstafter", "iterationfixeddelay", "fixedafter", "constafter", "fixeddelay":
		value, ok := runtimeCfgPacingSecondsToMS(rc, "StartEvery", rawStartEvery)
		if ok {
			rc.PacingType = "fixed_delay"
			rc.DelayMs = value
		}
	case "iterationafter", "iterationrandomafter", "iterationrandomdelay", "after", "randomafter", "randomdelay":
		low, lowOK := runtimeCfgPacingSecondsToMS(rc, "RandomMin", rawRandomMin)
		high, highOK := runtimeCfgPacingSecondsToMS(rc, "RandomMax", rawRandomMax)
		if lowOK && highOK && validateRuntimeBounds(rc, "cfg [Iterations]", "random delay", &low, &high) {
			rc.PacingType = "random_delay"
			rc.MinDelayMs = low
			rc.MaxDelayMs = high
		}
	default:
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("The cfg [Iterations] IterationPace=%q is unsupported; using no pacing and requiring review.", strings.TrimSpace(rawMode)),
		})
	}
}

func compactRuntimeEnum(value string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
}

func runtimeCfgPacingSecondsToMS(rc *RuntimeConfig, field, raw string) (int, bool) {
	return runtimeSecondsToMS(rc, "cfg [Iterations]", field, raw)
}

func parseRuntimeCfgPacingMilliseconds(rc *RuntimeConfig, section map[string]string, label string, aliases ...string) (int, bool) {
	raw, present := sectionValueCaseInsensitive(section, aliases...)
	raw = strings.TrimSpace(raw)
	value, err := strconv.Atoi(raw)
	if !present || raw == "" || err != nil || value < 0 || value > dsl.MaxConfiguredProtocolTimeoutMS {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "pacing", Area: "Pacing",
			Notes: fmt.Sprintf("The cfg %s value %q is missing, invalid, or outside 0..%dms; using no pacing and requiring review.", label, raw, dsl.MaxConfiguredProtocolTimeoutMS),
		})
		return 0, false
	}
	return value, true
}

func validateRuntimeCfgPacingBounds(rc *RuntimeConfig, label string, low, high int) bool {
	if low <= high {
		return true
	}
	rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
		ID: "pacing", Area: "Pacing",
		Notes: fmt.Sprintf("The cfg %s bounds are invalid (%dms..%dms); using no pacing and requiring review.", label, low, high),
	})
	return false
}

func parseLegacyRuntimeThinkTimeValues(rc *RuntimeConfig, section map[string]string, formalMode string) {
	if rc == nil {
		return
	}
	_, hasIgnore := sectionValueCaseInsensitive(section, "Ignore", "IgnoreThinkTime")
	rawMultiplier, hasMultiplier := sectionValueCaseInsensitive(section, "Multiplier", "MultiplyRecordedThinkTimeBy")
	rawLimit, hasLegacyLimit := sectionValueCaseInsensitive(section, "LimitMs", "LimitThinkTimeMs")
	_, hasFormalLimitFlag := sectionValueCaseInsensitive(section, "LimitFlag")
	legacyConfigured := hasIgnore || hasMultiplier || (hasLegacyLimit && !hasFormalLimitFlag)
	if strings.TrimSpace(formalMode) == "" && legacyConfigured {
		rc.ThinkTimeType = "recorded"
		if rc.ThinkTimeMultiplier == 0 && !hasMultiplier {
			rc.ThinkTimeMultiplier = 1
		}
	}

	if rc.ThinkTimeMultiplier == 0 && hasMultiplier {
		rawMultiplier = strings.TrimSpace(rawMultiplier)
		value, err := strconv.ParseFloat(rawMultiplier, 64)
		if rawMultiplier == "" || err != nil || math.IsNaN(value) || math.IsInf(value, 0) ||
			value <= 0 || value > dsl.MaxThinkTimeMultiplier {
			rc.ThinkTimeType = "recorded"
			rc.ThinkTimeMultiplier = 1
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("The legacy cfg think-time multiplier %q is missing, invalid, or outside 0..%d; using recorded think time at 100%% and requiring review.", rawMultiplier, dsl.MaxThinkTimeMultiplier),
			})
		} else {
			rc.ThinkTimeType = "recorded"
			rc.ThinkTimeMultiplier = value
		}
	}
	if hasLegacyLimit && !hasFormalLimitFlag {
		rawLimit = strings.TrimSpace(rawLimit)
		value, err := strconv.ParseFloat(rawLimit, 64)
		if rawLimit == "" || err != nil || math.IsNaN(value) || math.IsInf(value, 0) ||
			value < 0 || value > dsl.MaxConfiguredProtocolTimeoutMS {
			rc.ThinkTimeLimitMs = 0
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "think_time", Area: "Think Time",
				Notes: fmt.Sprintf("The legacy cfg think-time limit %q is missing, invalid, or outside 0..%dms; applying no cap and requiring review.", rawLimit, dsl.MaxConfiguredProtocolTimeoutMS),
			})
		} else {
			rc.ThinkTimeLimitMs = value
		}
	}
}

func runtimeConfigSection(sections map[string]map[string]string, aliases ...string) (map[string]string, bool) {
	for _, alias := range aliases {
		if values, ok := sections[alias]; ok {
			return values, true
		}
		matchingNames := make([]string, 0, 1)
		for name := range sections {
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(alias)) {
				matchingNames = append(matchingNames, name)
			}
		}
		if len(matchingNames) > 0 {
			sort.Strings(matchingNames)
			return sections[matchingNames[0]], true
		}
	}
	return map[string]string{}, false
}

func firstNonZeroInt(values ...string) int {
	for _, value := range values {
		if parsed := parseInt(value); parsed != 0 {
			return parsed
		}
	}
	return 0
}

func parseRuntimeProxySettings(rc *RuntimeConfig, sections map[string]map[string]string) {
	if rc == nil {
		return
	}
	proxySection := findSection(sections, "Proxy")
	webSection := findSection(sections, "WEB", "Web")
	sourceSections := []map[string]string{proxySection, webSection}

	modeRaw, _ := firstRuntimeSectionValue(sourceSections, "Mode", "ProxyMode", "Type")
	var validMode bool
	rc.ProxyMode, validMode = mapRuntimeProxyMode(modeRaw)
	rc.ProxyURL, _ = firstRuntimeSectionValue(sourceSections, "ProxyServer", "ProxyURL", "Server")
	rc.ProxyURL = strings.TrimSpace(rc.ProxyURL)
	endpointIssue := ""
	if rc.ProxyURL == "" {
		rc.ProxyURL, endpointIssue = resolveRuntimeProxyEndpoints(sourceSections)
	}
	rc.ProxyURL = normalizeRuntimeProxyURL(rc.ProxyURL)
	rc.ProxyUser, _ = firstRuntimeSectionValue(sourceSections, "ProxyUserName", "UserName", "ProxyUser")
	rc.ProxyUser = strings.TrimSpace(rc.ProxyUser)
	rc.ProxyPass, _ = firstRuntimeSectionValue(sourceSections, "ProxyPassword", "Password")
	rc.ProxyPass = strings.TrimSpace(rc.ProxyPass)
	bypassIssue := ""
	rc.ProxyNoProxy, bypassIssue = parseRuntimeProxyBypass(sourceSections)

	type proxyFlag struct {
		section string
		key     string
		raw     string
	}
	flags := make([]proxyFlag, 0, 4)
	for _, candidate := range []struct {
		name    string
		section map[string]string
	}{
		{name: "Proxy", section: proxySection},
		{name: "WEB", section: webSection},
	} {
		for _, alias := range []string{"ProxyUseProxy", "ProxyUseProxyServer"} {
			if key, raw, ok := sectionEntryCaseInsensitive(candidate.section, alias); ok {
				flags = append(flags, proxyFlag{section: candidate.name, key: key, raw: raw})
			}
		}
	}
	if len(flags) == 0 {
		if !validMode {
			markUnsupportedRuntimeProxyMode(rc, modeRaw)
			return
		}
		finalizeRuntimeProxySelection(rc, false, endpointIssue, bypassIssue)
		return
	}

	var enabled bool
	var enabledSet bool
	var malformed []string
	var conflicting []string
	for _, flag := range flags {
		value, valid := parseBoolExplicit(flag.raw)
		evidence := fmt.Sprintf("[%s] %s=%q", flag.section, flag.key, runtimeProxyDiagnosticValue(flag.raw))
		if !valid {
			malformed = append(malformed, evidence)
			continue
		}
		if !enabledSet {
			enabled = value
			enabledSet = true
			continue
		}
		if enabled != value {
			conflicting = append(conflicting, evidence)
		}
	}
	if len(malformed) > 0 || len(conflicting) > 0 || !enabledSet {
		evidence := make([]string, 0, len(flags))
		for _, flag := range flags {
			evidence = append(evidence, fmt.Sprintf("[%s] %s=%q", flag.section, flag.key, runtimeProxyDiagnosticValue(flag.raw)))
		}
		disableRuntimeProxyForReview(rc)
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "proxy", Area: "Proxy",
			Notes: fmt.Sprintf("Malformed or conflicting proxy enable flags (%s); using the APEX no-proxy default and requiring review.", strings.Join(evidence, ", ")),
		})
		return
	}
	if !enabled {
		// VuGen commonly leaves the last proxy host and credentials in the cfg
		// after the operator disables proxying. The explicit enable flag is
		// authoritative; importing those dormant fields would silently reroute
		// traffic through a proxy the source script did not use.
		disableRuntimeProxy(rc)
		return
	}
	if !validMode {
		markUnsupportedRuntimeProxyMode(rc, modeRaw)
		return
	}

	finalizeRuntimeProxySelection(rc, true, endpointIssue, bypassIssue)
}

func finalizeRuntimeProxySelection(rc *RuntimeConfig, explicitlyEnabled bool, endpointIssue, bypassIssue string) {
	if rc == nil {
		return
	}
	if rc.ProxyMode == "none" {
		if explicitlyEnabled {
			disableRuntimeProxyForReview(rc)
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "proxy", Area: "Proxy",
				Notes: "ProxyUseProxy/ProxyUseProxyServer enables proxying but ProxyMode is none; using the APEX no-proxy default and requiring review.",
			})
		} else {
			disableRuntimeProxy(rc)
		}
		return
	}
	if rc.ProxyMode == "system" {
		if bypassIssue != "" {
			failRuntimeProxySelection(rc, bypassIssue)
			return
		}
		rc.ProxyURL = ""
		rc.ProxyUser = ""
		rc.ProxyPass = ""
		return
	}
	if rc.ProxyMode == "" && rc.ProxyURL == "" && !explicitlyEnabled {
		// No proxy is selected. Bypass controls and credentials are dormant
		// source UI state and must not create executable proxy configuration.
		rc.ProxyUser = ""
		rc.ProxyPass = ""
		rc.ProxyNoProxy = nil
		return
	}
	if issue := joinRuntimeProxyIssues(endpointIssue, bypassIssue); issue != "" {
		failRuntimeProxySelection(rc, issue)
		return
	}
	if rc.ProxyURL == "" {
		if rc.ProxyMode == "custom" || explicitlyEnabled {
			disableRuntimeProxyForReview(rc)
			rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
				ID: "proxy", Area: "Proxy",
				Notes: "The selected custom proxy has no proxy server URL; using the APEX no-proxy default and requiring review.",
			})
			return
		}
		// With no mode, enable flag, or endpoint, any retained credentials are
		// dormant source UI state rather than an executable proxy selection.
		rc.ProxyUser = ""
		rc.ProxyPass = ""
		rc.ProxyNoProxy = nil
		return
	}
	if rc.ProxyMode == "" {
		rc.ProxyMode = "custom"
	}
	if !validRuntimeCustomProxyURL(rc.ProxyURL) {
		disableRuntimeProxyForReview(rc)
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "proxy", Area: "Proxy",
			Notes: "The selected custom proxy URL is not an absolute http, https, socks5, or socks5h URL with a host; using the APEX no-proxy default and requiring review.",
		})
	}
}

func failRuntimeProxySelection(rc *RuntimeConfig, notes string) {
	disableRuntimeProxyForReview(rc)
	rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
		ID: "proxy", Area: "Proxy",
		Notes: notes + " Using the APEX no-proxy default and requiring review.",
	})
}

func joinRuntimeProxyIssues(issues ...string) string {
	var kept []string
	for _, issue := range issues {
		if issue = strings.TrimSpace(issue); issue != "" {
			kept = append(kept, issue)
		}
	}
	return strings.Join(kept, " ")
}

func validRuntimeCustomProxyURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
		port := parsed.Port()
		if port == "" {
			return true
		}
		value, err := strconv.Atoi(port)
		return err == nil && value >= 1 && value <= 65535
	default:
		return false
	}
}

func markUnsupportedRuntimeProxyMode(rc *RuntimeConfig, raw string) {
	disableRuntimeProxyForReview(rc)
	rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
		ID: "proxy", Area: "Proxy",
		Notes: fmt.Sprintf("Unsupported proxy mode %q; using the APEX no-proxy default and requiring review.", runtimeProxyDiagnosticValue(raw)),
	})
}

func runtimeProxyDiagnosticValue(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return credential.Redacted
}

func mapRuntimeProxyMode(raw string) (string, bool) {
	switch normalizeEnum(raw) {
	case "":
		return "", true
	case "none", "off", "disabled", "no_proxy":
		return "none", true
	case "system", "system_proxy", "use_system_proxy":
		return "system", true
	case "custom", "custom_proxy", "manual":
		return "custom", true
	default:
		return "", false
	}
}

func firstRuntimeSectionValue(sections []map[string]string, aliases ...string) (string, bool) {
	for _, section := range sections {
		if value, ok := sectionValueCaseInsensitive(section, aliases...); ok {
			return value, true
		}
	}
	return "", false
}

func disableRuntimeProxy(rc *RuntimeConfig) {
	rc.ProxyMode = "none"
	rc.ProxyURL = ""
	rc.ProxyUser = ""
	rc.ProxyPass = ""
	rc.proxyReviewUser = ""
	rc.proxyReviewPass = ""
	rc.ProxyNoProxy = nil
}

func disableRuntimeProxyForReview(rc *RuntimeConfig) {
	if rc == nil {
		return
	}
	_, username, password := splitEnterpriseProxyUserinfo(
		rc.ProxyURL,
		rc.ProxyUser,
		rc.ProxyPass,
	)
	if rc.proxyReviewUser != "" {
		username = rc.proxyReviewUser
	}
	if rc.proxyReviewPass != "" {
		password = rc.proxyReviewPass
	}
	disableRuntimeProxy(rc)
	rc.proxyReviewUser = username
	rc.proxyReviewPass = password
}

func resolveRuntimeProxyEndpoints(sections []map[string]string) (string, string) {
	httpHost, hasHTTPHost := firstRuntimeSectionValue(sections, "ProxyHTTPHost")
	httpPort, hasHTTPPort := firstRuntimeSectionValue(sections, "ProxyHTTPPort")
	httpsHost, hasHTTPSHost := firstRuntimeSectionValue(sections, "ProxyHTTPSHost")
	httpsPort, hasHTTPSPort := firstRuntimeSectionValue(sections, "ProxyHTTPSPort")
	hasHTTP := (hasHTTPHost && strings.TrimSpace(httpHost) != "") || (hasHTTPPort && strings.TrimSpace(httpPort) != "")
	hasHTTPS := (hasHTTPSHost && strings.TrimSpace(httpsHost) != "") || (hasHTTPSPort && strings.TrimSpace(httpsPort) != "")
	if !hasHTTP && !hasHTTPS {
		return "", ""
	}

	httpURL, httpIssue := buildRuntimeProxyEndpoint(httpHost, httpPort, hasHTTPPort, "http", "HTTP")
	httpsURL, httpsIssue := buildRuntimeProxyEndpoint(httpsHost, httpsPort, hasHTTPSPort, "https", "HTTPS")
	buildIssue := joinRuntimeProxyIssues(httpIssue, httpsIssue)

	useSameRaw, hasUseSame := firstRuntimeSectionValue(sections, "ProxyUseSame")
	if hasUseSame && strings.TrimSpace(useSameRaw) != "" {
		useSame, valid := parseBoolExplicit(useSameRaw)
		if !valid {
			return "", joinRuntimeProxyIssues(buildIssue,
				fmt.Sprintf("ProxyUseSame=%q is malformed.", runtimeProxyDiagnosticValue(useSameRaw)))
		}
		if useSame {
			if hasHTTP {
				return httpURL, httpIssue
			}
			return httpsURL, httpsIssue
		}
		if !hasHTTP || !hasHTTPS || !runtimeProxyURLsEquivalent(httpURL, httpsURL) {
			return "", joinRuntimeProxyIssues(buildIssue,
				"ProxyUseSame is disabled and the separate HTTP/HTTPS proxy endpoints cannot be represented by one APEX custom proxy URL.")
		}
		return httpURL, buildIssue
	}

	if hasHTTP && hasHTTPS {
		if !runtimeProxyURLsEquivalent(httpURL, httpsURL) {
			return "", joinRuntimeProxyIssues(buildIssue,
				"The configured HTTP and HTTPS proxy endpoints differ and cannot be represented by one APEX custom proxy URL.")
		}
		return httpURL, buildIssue
	}
	if hasHTTP {
		return httpURL, httpIssue
	}
	return httpsURL, httpsIssue
}

func buildRuntimeProxyEndpoint(hostRaw, portRaw string, hasPort bool, defaultScheme, label string) (string, string) {
	host := strings.TrimSpace(hostRaw)
	portRaw = strings.TrimSpace(portRaw)
	if host == "" {
		if hasPort && portRaw != "" {
			return "", fmt.Sprintf("%s proxy port is configured without a host.", label)
		}
		return "", ""
	}
	candidate := host
	if !strings.Contains(candidate, "://") {
		candidate = defaultScheme + "://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Sprintf("%s proxy endpoint is malformed.", label)
	}
	if hasPort && portRaw != "" {
		port, err := strconv.Atoi(portRaw)
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Sprintf("%s proxy port %q is invalid or outside 1..65535.", label, runtimeProxyDiagnosticValue(portRaw))
		}
		if embeddedPort := parsed.Port(); embeddedPort != "" && embeddedPort != portRaw {
			return "", fmt.Sprintf("%s proxy host and port fields conflict.", label)
		} else if embeddedPort == "" {
			parsed.Host = net.JoinHostPort(parsed.Hostname(), portRaw)
		}
	}
	return normalizeRuntimeProxyURL(parsed.String()), ""
}

func runtimeProxyURLsEquivalent(left, right string) bool {
	left = strings.TrimSpace(normalizeRuntimeProxyURL(left))
	right = strings.TrimSpace(normalizeRuntimeProxyURL(right))
	return left != "" && right != "" && strings.EqualFold(left, right)
}

func parseRuntimeProxyBypass(sections []map[string]string) ([]string, string) {
	rawBypass, _ := firstRuntimeSectionValue(sections, "ProxyBypass")
	var bypass []string
	for _, value := range strings.FieldsFunc(rawBypass, func(r rune) bool {
		return r == ',' || r == ';'
	}) {
		value = strings.TrimSpace(value)
		if value != "" {
			bypass = appendIfMissing(bypass, value)
		}
	}

	rawNoLocal, hasNoLocal := firstRuntimeSectionValue(sections, "ProxyNoLocal")
	if !hasNoLocal || strings.TrimSpace(rawNoLocal) == "" {
		return bypass, ""
	}
	noLocal, valid := parseBoolExplicit(rawNoLocal)
	if !valid {
		return bypass, fmt.Sprintf("ProxyNoLocal=%q is malformed.", runtimeProxyDiagnosticValue(rawNoLocal))
	}
	if noLocal {
		return bypass, "ProxyNoLocal is enabled, but LoadRunner's complete local-address bypass semantics have no exact APEX no_proxy equivalent."
	}
	return bypass, ""
}

func normalizeRuntimeProxyURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return trimmed
	}
	switch parsed.Port() {
	case "3128", "8080", "8888":
		parsed.Scheme = "http"
		return parsed.String()
	default:
		return trimmed
	}
}

func parseBool(value string) bool {
	parsed, ok := parseBoolExplicit(value)
	return ok && parsed
}

func parseBoolExplicit(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true, true
	case "0", "false", "no", "off", "disabled":
		return false, true
	default:
		return false, false
	}
}

func hasExplicitFalse(value string) bool {
	parsed, ok := parseBoolExplicit(value)
	return ok && !parsed
}

func splitList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := normalizeEnum(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitDelimitedItems(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseDFETransform(value string) dsl.DFETransform {
	tokens := strings.Fields(strings.TrimSpace(value))
	if len(tokens) == 0 {
		return dsl.DFETransform{}
	}

	transform := dsl.DFETransform{Type: normalizeEnum(tokens[0])}
	options := make(map[string]string)
	for _, token := range tokens[1:] {
		key, optionValue, found := strings.Cut(token, "=")
		if !found {
			continue
		}
		key = normalizeEnum(key)
		optionValue = strings.TrimSpace(optionValue)
		if key == "" || optionValue == "" {
			continue
		}
		options[key] = optionValue
	}
	if len(options) > 0 {
		transform.Options = options
	}
	return transform
}

func normalizeEnum(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	trimmed = strings.ReplaceAll(trimmed, " ", "_")
	trimmed = strings.ReplaceAll(trimmed, "-", "_")
	return trimmed
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func normalizeRuntimeAcceptLanguage(value string) string {
	trimmed := strings.TrimSpace(value)
	switch normalizeEnum(trimmed) {
	case "", "none", "disabled", "off":
		return ""
	default:
		return trimmed
	}
}

func normalizeRuntimeAcceptEncoding(value string) string {
	trimmed := strings.TrimSpace(value)
	switch normalizeEnum(trimmed) {
	case "", "none", "no_compression", "disabled", "off":
		return ""
	}
	parts := strings.Split(trimmed, ",")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		token := strings.ToLower(strings.TrimSpace(part))
		if token == "" {
			continue
		}
		normalized = appendIfMissing(normalized, token)
	}
	return strings.Join(normalized, ", ")
}

func appendNativeLogClassOption(rc *RuntimeConfig, section map[string]string, sourceKey, option string) {
	raw, ok := sectionValueCaseInsensitive(section, sourceKey)
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	enabled, valid := parseBoolExplicit(raw)
	if !valid {
		rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
			ID: "logging", Area: "Logging",
			sourceKey: runtimeConfigCanonicalKey("logging", sourceKey),
			Notes:     fmt.Sprintf("Invalid %s=%q for LogOptions=LogExtended; the option was omitted and review is required.", sourceKey, raw),
		})
		return
	}
	if enabled {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, option)
	}
}

type knownUnsupportedRuntimeSetting struct {
	section string
	keys    []string
	active  func(string) bool
}

// appendKnownUnsupportedRuntimeIssues reports bounded, behaviorally active
// source settings for which APEX has no exact destination field. It deliberately
// avoids treating every non-zero stock cfg row as a gap: recorder metadata,
// inactive child settings, UI formatting, and disabled options remain available
// in preserved runtime_files without blocking an otherwise exact import.
func appendKnownUnsupportedRuntimeIssues(rc *RuntimeConfig, sections map[string]map[string]string) {
	if rc == nil || len(sections) == 0 {
		return
	}
	boolTrue := func(raw string) bool {
		value, valid := parseBoolExplicit(raw)
		return valid && value
	}
	positiveNumber := func(raw string) bool {
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		return err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
	}
	nonSentinel := func(raw string) bool {
		switch normalizeEnum(raw) {
		case "", "none", "disabled", "off", "auto", "automatic", "default":
			return false
		default:
			return true
		}
	}

	specs := []knownUnsupportedRuntimeSetting{
		{section: "General", keys: []string{"FailTransOnErrorMsg"}, active: boolTrue},
		{section: "General", keys: []string{"AccessVTSPortByQueryString"}, active: boolTrue},
		{section: "Log", keys: []string{"IncludeEnvInfo"}, active: boolTrue},
		{section: "Log", keys: []string{"PrintTimeStamp"}, active: boolTrue},
		{section: "WEB", keys: []string{"DeleteCacheUnreferencedIterations"}, active: positiveNumber},
		{section: "WEB", keys: []string{"EnableSnapshotsInReplay"}, active: boolTrue},
		{section: "WEB", keys: []string{"IgnoreContentCachingTypes"}, active: boolTrue},
		{section: "WEB", keys: []string{"IPVersionPolicy"}, active: nonSentinel},
		{section: "WEB", keys: []string{"KeepAliveTimeout"}, active: positiveNumber},
		{section: "WEB", keys: []string{"KeepNonTextMimeType"}, active: nonSentinel},
		{section: "WEB", keys: []string{"MaxErrorMatchesAsERRORS"}, active: positiveNumber},
		{section: "WEB", keys: []string{"MaxSelfMetaRefreshCount"}, active: positiveNumber},
		{section: "WEB", keys: []string{"NetBufSize"}, active: positiveNumber},
		{section: "WEB", keys: []string{"PageDownloadTimeout"}, active: positiveNumber},
		{section: "WEB", keys: []string{"ParseHtmlContentType"}, active: nonSentinel},
		{section: "WEB", keys: []string{"ResetContext"}, active: boolTrue},
		{section: "WEB", keys: []string{"SSLVersionKey"}, active: nonSentinel},
		{section: "WEB", keys: []string{"ZlibHeadersInCompressedRequestBody"}, active: boolTrue},
	}

	evidence := make([]string, 0)
	for _, spec := range specs {
		section := findSection(sections, spec.section)
		if len(section) == 0 {
			continue
		}
		for _, alias := range spec.keys {
			key, raw, ok := sectionEntryCaseInsensitive(section, alias)
			if !ok || !spec.active(raw) {
				continue
			}
			evidence = append(evidence, fmt.Sprintf("[%s] %s=%q", spec.section, key, runtimeSettingEvidenceValue(key, raw)))
		}
	}
	if len(evidence) == 0 {
		return
	}
	sort.Strings(evidence)
	rc.RuntimeIssues = append(rc.RuntimeIssues, RuntimeMigrationIssue{
		ID: "runtime_unmapped", Area: "Runtime Settings",
		Notes: "Active source runtime settings have no exact APEX equivalent and require review: " +
			strings.Join(evidence, "; ") + ". Raw source sections remain preserved.",
	})
}

func runtimeSettingEvidenceValue(key, value string) string {
	normalizedKey := strings.ToLower(strings.TrimSpace(key))
	for _, sensitive := range []string{"password", "passwd", "secret", "token", "credential"} {
		if strings.Contains(normalizedKey, sensitive) {
			return "<redacted>"
		}
	}
	return strings.TrimSpace(value)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mapPacingType(value string) string {
	switch normalizeEnum(value) {
	case "", "none", "immediate", "start_new_iteration_immediately":
		return "none"
	case "fixed_delay", "after_previous_iteration_ends", "delay":
		return "fixed_delay"
	case "random_delay", "after_previous_iteration_ends_random":
		return "random_delay"
	case "fixed_interval", "interval", "start_new_iteration_at_fixed_intervals":
		return "fixed_interval"
	case "random_interval", "start_new_iteration_at_random_intervals":
		return "random_interval"
	case "tps":
		return "tps"
	default:
		return normalizeEnum(value)
	}
}

func mapLogLevel(value string) string {
	switch normalizeEnum(value) {
	case "", "standard":
		return strings.TrimSpace(strings.ToLower(firstNonBlank(value, "")))
	case "disable", "disabled":
		return "disabled"
	case "extended", "verbose":
		return "extended"
	default:
		return normalizeEnum(value)
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

// mapSSLVersion converts source SSL version identifiers to standard version strings.
func mapSSLVersion(v string) string {
	mapped, _ := mapSSLVersionExact(v)
	return mapped
}

func mapSSLVersionExact(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "tls1.0", "tls 1.0", "tls10":
		return "1.0", true
	case "tls1.1", "tls 1.1", "tls11":
		return "1.1", true
	case "tls1.2", "tls 1.2", "tls12", "tls":
		return "1.2", true
	case "tls1.3", "tls 1.3", "tls13":
		return "1.3", true
	default:
		return "", false
	}
}
