package enterprisearchive

// runtime_config.go — split from converter.go (same package, behavior-preserving).

import (
	"bufio"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// RuntimeConfig holds parsed source runtime settings from a .cfg file.
type RuntimeConfig struct {
	ConnectTimeoutSec                int
	ReceiveTimeoutSec                int
	StepTimeoutSec                   int
	HTTPVersion                      string // "1.0" or "1.1"
	KeepAlive                        bool
	MaxConnsPerHost                  int
	ProxyMode                        string
	ProxyURL                         string
	ProxyUser                        string
	ProxyPass                        string
	UserAgent                        string
	AcceptLanguage                   string
	UseGzip                          bool
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
	ThinkTimeMultiplier              float64
	ThinkTimeLimitMs                 float64
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
	JavaVMParametersParseError       string
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
	httpSection := findSection(sections, "HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web")
	rc.ConnectTimeoutSec = parseInt(httpSection["ConnectTimeout"])
	rc.ReceiveTimeoutSec = parseInt(httpSection["ReceiveTimeout"])
	rc.StepTimeoutSec = parseInt(httpSection["StepDownloadTimeout"])
	if version := strings.TrimSpace(firstNonBlank(httpSection["HTTPVersion"], httpSection["HTTPVer"], httpSection["HttpVer"])); version != "" {
		rc.HTTPVersion = version
	}
	if keepAlive, ok := parseBoolExplicit(httpSection["KeepAlive"]); ok {
		rc.KeepAlive = keepAlive
	}
	if redirects := parseInt(httpSection["MaxRedirections"]); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	if redirects := parseInt(httpSection["MaxRedirectionDepth"]); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	rc.MaxConnsPerHost = firstNonZeroInt(httpSection["MaxConnectionsPerHost"], httpSection["MaxConnections"], httpSection["MaxConnectionsPerHostHTTP"])

	proxySection := findSection(sections, "Proxy", "WEB", "Web")
	rc.ProxyMode = normalizeEnum(strings.TrimSpace(firstNonBlank(proxySection["Mode"], proxySection["ProxyMode"], proxySection["Type"])))
	rc.ProxyURL = strings.TrimSpace(firstNonBlank(proxySection["ProxyServer"], proxySection["ProxyURL"], proxySection["Server"]))
	if rc.ProxyURL == "" {
		rc.ProxyURL = buildRuntimeProxyURL(proxySection)
	}
	rc.ProxyURL = normalizeRuntimeProxyURL(rc.ProxyURL)
	rc.ProxyUser = strings.TrimSpace(firstNonBlank(proxySection["ProxyUserName"], proxySection["UserName"], proxySection["ProxyUser"]))
	rc.ProxyPass = strings.TrimSpace(firstNonBlank(proxySection["ProxyPassword"], proxySection["Password"]))

	browserSection := findSection(sections, "Browser Emulation", "Browser", "WEB", "Web")
	// rc.UserAgent is sourced inside parseBrowserEmulationRuntimeSettings so it
	// shares the same browser-section→[WEB] fallback as the browser-emulation
	// toggles; sourcing it here from browserSection alone would silently lose a
	// custom UA that lives in [WEB] when an APEX-native [Browser Emulation]
	// section is also present.
	rc.AcceptLanguage = strings.TrimSpace(firstNonBlank(browserSection["AcceptLanguage"], browserSection["BrowserAcceptLanguage"], browserSection["NavigatorBrowserLanguage"], browserSection["NavigatorUserLanguage"]))
	acceptEncoding := firstNonBlank(browserSection["AcceptEncoding"], browserSection["BrowserAcceptEncoding"])
	rc.UseGzip = strings.Contains(strings.ToLower(acceptEncoding), "gzip")
	rc.CacheEnabled = parseBool(browserSection["CacheEnabled"])
	rc.CacheContentURLs = parseBool(firstNonBlank(browserSection["CacheContentURLs"], browserSection["CacheURLsRequiringContent"]))
	rc.CheckNewerVersions = parseBool(firstNonBlank(browserSection["CheckNewerVersions"], browserSection["CheckForNewerPages"]))
	rc.NewUserPerIteration = parseBool(firstNonBlank(browserSection["NewUserPerIteration"], browserSection["SimulateNewUser"]))
	rc.BrowserEngine = normalizeEnum(strings.TrimSpace(firstNonBlank(browserSection["Engine"], browserSection["BrowserEngine"])))
	rc.BrowserDeviceProfile = normalizeEnum(strings.TrimSpace(firstNonBlank(browserSection["DeviceProfile"], browserSection["MobileProfile"], browserSection["EmulatedDevice"])))
	rc.BrowserTouchMode = parseBool(firstNonBlank(browserSection["TouchMode"], browserSection["TouchEnabled"]))
	rc.BrowserOrientation = normalizeEnum(strings.TrimSpace(browserSection["Orientation"]))
	rc.BrowserClientMetrics = parseBool(firstNonBlank(browserSection["ClientMetrics"], browserSection["CollectClientMetrics"]))
	rc.BrowserSlowMoMs = firstNonZeroInt(browserSection["SlowMoMs"], browserSection["SlowMotionMs"])
	rc.BrowserViewportWidth = firstNonZeroInt(browserSection["ViewportWidth"], browserSection["ScreenWidth"])
	rc.BrowserViewportHeight = firstNonZeroInt(browserSection["ViewportHeight"], browserSection["ScreenHeight"])
	// DownloadNonHtmlResources in its bool form (1/0/yes/no) feeds the
	// browser_emulation cache flag, not this all/none/recorded enum — passing a
	// bool through here would yield an invalid config.browser.download_resources
	// value like "1".
	rc.DownloadResources = normalizeEnum(strings.TrimSpace(firstNonBlank(browserSection["DownloadResources"], enumDownloadResourcesOnly(browserSection["DownloadNonHtmlResources"]))))
	parseBrowserEmulationRuntimeSettings(rc, browserSection, findSection(sections, "WEB", "Web"))

	sslSection := findSection(sections, "SSL", "Security")
	rc.SSLVersion = strings.TrimSpace(firstNonBlank(sslSection["SSLVersion"], sslSection["TLSVersion"]))

	authSection := findSection(sections, "Authentication")
	rc.AuthUser = strings.TrimSpace(firstNonBlank(authSection["UserName"], authSection["Username"]))
	rc.AuthPass = strings.TrimSpace(authSection["Password"])

	runLogicSection := findSection(sections, "Run Logic", "RunLogic")
	generalSection := findSection(sections, "General")
	rc.Iterations = parseInt(firstNonBlank(runLogicSection["Iterations"], runLogicSection["NumberOfIterations"]))
	rc.AutoTransactions = normalizeEnum(runLogicSection["AutoTransactions"])
	if rc.AutoTransactions == "" && parseBool(firstNonBlank(generalSection["AutomaticTransactions"], generalSection["AutoTransactions"])) {
		rc.AutoTransactions = "per_action"
	}
	rc.BlockScheduling = normalizeEnum(runLogicSection["BlockScheduling"])
	rc.ActionSequence = splitList(firstNonBlank(runLogicSection["ActionSequence"], runLogicSection["Sequence"]))

	pacingSection := findSection(sections, "Pacing")
	rc.PacingType = mapPacingType(firstNonBlank(pacingSection["Mode"], pacingSection["PacingMode"]))
	rc.DelayMs = firstNonZeroInt(pacingSection["DelayMs"], pacingSection["Delay"])
	rc.MinDelayMs = firstNonZeroInt(pacingSection["MinDelayMs"], pacingSection["MinDelay"])
	rc.MaxDelayMs = firstNonZeroInt(pacingSection["MaxDelayMs"], pacingSection["MaxDelay"])
	rc.IntervalMs = firstNonZeroInt(pacingSection["IntervalMs"], pacingSection["Interval"])
	rc.MinIntervalMs = firstNonZeroInt(pacingSection["MinIntervalMs"], pacingSection["MinInterval"])
	rc.MaxIntervalMs = firstNonZeroInt(pacingSection["MaxIntervalMs"], pacingSection["MaxInterval"])

	thinkTimeSection := findSection(sections, "Think Time", "ThinkTime")
	rc.ThinkTimeIgnore = parseBool(firstNonBlank(thinkTimeSection["Ignore"], thinkTimeSection["IgnoreThinkTime"]))
	if strings.Contains(normalizeEnum(thinkTimeSection["Options"]), "nothink") {
		rc.ThinkTimeIgnore = true
	}
	rc.ThinkTimeMultiplier = parseFloat(firstNonBlank(thinkTimeSection["Multiplier"], thinkTimeSection["MultiplyRecordedThinkTimeBy"]))
	rc.ThinkTimeLimitMs = parseFloat(firstNonBlank(thinkTimeSection["LimitMs"], thinkTimeSection["LimitThinkTimeMs"]))

	logSection := findSection(sections, "Log", "Logging")
	rc.LogLevel = mapLogLevel(firstNonBlank(logSection["Level"], logSection["LogLevel"]))
	rc.OnErrorOnly = parseBool(firstNonBlank(logSection["OnErrorOnly"], logSection["LogOnErrorOnly"]))
	rc.LogExtendedOptions = splitList(firstNonBlank(logSection["ExtendedOptions"], logSection["ExtendedLogOptions"]))
	if parseBool(logSection["ParameterSubstitution"]) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "parameter_substitution")
	}
	if parseBool(logSection["DataReturned"]) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "data_returned")
	}
	if parseBool(logSection["AdvancedTrace"]) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "advanced_trace")
	}

	miscSection := findSection(sections, "Misc", "Miscellaneous")
	webSection := findSection(sections, "WEB", "Web")
	rc.ContinueOnError = parseBool(firstNonBlank(miscSection["ContinueOnError"], miscSection["ErrorContinue"], generalSection["ContinueOnError"]))
	rc.SnapshotOnError = parseBool(firstNonBlank(miscSection["SnapshotOnError"], miscSection["GenerateSnapshotOnError"], webSection["SnapshotOnErrorActive"]))

	attrsSection := findSection(sections, "Additional Attributes", "Attributes")
	for key, value := range attrsSection {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		rc.AdditionalAttributes[trimmedKey] = strings.TrimSpace(value)
	}

	filtersSection := findSection(sections, "Download Filters", "DownloadFilters")
	rc.DownloadFilterMode = normalizeEnum(firstNonBlank(filtersSection["Mode"], filtersSection["FilterMode"]))
	rc.DownloadFilterHosts = splitList(firstNonBlank(filtersSection["Hosts"], filtersSection["IncludeHosts"], filtersSection["ExcludeHosts"]))

	contentCheckSection := findSection(sections, "Content Check", "ContentCheck")
	if text := strings.TrimSpace(firstNonBlank(contentCheckSection["Text"], contentCheckSection["SearchText"])); text != "" {
		failIfFound := !hasExplicitFalse(contentCheckSection["FailIfFound"])
		rc.ContentChecks = append(rc.ContentChecks, dsl.ContentCheckConfig{
			Text:        text,
			Scope:       normalizeSourceContentCheckScope(firstNonBlank(contentCheckSection["Scope"], "any_response")),
			FailIfFound: &failIfFound,
		})
	}

	networkSection := findSection(sections, "Network Speed Simulation", "NetworkSimulation")
	rc.NetworkSimulationMode = normalizeEnum(firstNonBlank(networkSection["Mode"], networkSection["SimulationMode"]))
	rc.NetworkSimulationPreset = normalizeEnum(networkSection["Preset"])
	rc.NetworkBandwidthKbps = firstNonZeroInt(networkSection["BandwidthKbps"], networkSection["Bandwidth"])
	rc.NetworkLatencyMs = parseInt(networkSection["LatencyMs"])

	streamingSection := findSection(sections, "Streaming")
	rc.StreamingMode = normalizeEnum(streamingSection["Mode"])
	rc.StreamingBufferSizeSeconds = firstNonZeroInt(streamingSection["BufferSizeSeconds"], streamingSection["BufferSize"])
	rc.StreamingFixedBitrateKbps = firstNonZeroInt(streamingSection["FixedBitrateKbps"], streamingSection["FixedBitrate"])
	rc.StreamingBandwidthEstimation = normalizeEnum(streamingSection["BandwidthEstimation"])

	dataFormattingSection := findSection(sections, "Data Formatting", "DataFormatting")
	rc.DataFormattingAutoDetect = parseBool(firstNonBlank(dataFormattingSection["AutoDetect"], dataFormattingSection["DFEAutoDetect"]))
	for _, item := range splitDelimitedItems(firstNonBlank(dataFormattingSection["Chain"], dataFormattingSection["DFEChain"])) {
		transform := parseDFETransform(item)
		if transform.Type == "" {
			continue
		}
		rc.DataFormattingChain = append(rc.DataFormattingChain, transform)
	}

	javaVMSectionAliases := []string{"JavaVM:Options", "JavaVM", "Java VM", "Java Environment Settings", "Java:Options"}
	javaVMSection := findSection(sections, javaVMSectionAliases...)
	// Parse executable list values from the original CFG text. The general
	// config parser decodes adjacent C string literals for source-code fields;
	// applying that normalization here would concatenate separately quoted JVM
	// arguments and could silently change the imported command line.
	rawJavaVMSection := findSection(parseRawRuntimeConfigSections(content), javaVMSectionAliases...)
	javaVMJDKSelectionExplicit := false
	if selection, ok := sectionValueCaseInsensitive(javaVMSection, "use_jdk", "jdk_selection", "UseSpecifiedJdk"); ok {
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
	if raw, ok := sectionValueCaseInsensitive(rawJavaVMSection, "Java_VM_Params", "java_vm_parameters", "AdditionalVmParams", "ExtraVmParams", "VmParams"); ok {
		rc.JavaVMParametersRaw = raw
		if strings.TrimSpace(raw) != "" {
			parameters, err := splitQuotedRuntimeArguments(raw)
			if err != nil {
				rc.JavaVMParametersParseError = err.Error()
			} else {
				rc.JavaVMAdditionalParameters = parameters
			}
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
	if rc.ThinkTimeIgnore || rc.ThinkTimeMultiplier != 0 || rc.ThinkTimeLimitMs != 0 {
		config.ThinkTime = dsl.ThinkTimeConfig{
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

	cfg.Version = rc.HTTPVersion
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
		if rc.ProxyMode == "" {
			cfg.ProxyMode = "custom"
		} else {
			cfg.ProxyMode = rc.ProxyMode
		}
		cfg.Proxy = dsl.ProxyConfig{
			URL:      rc.ProxyURL,
			Username: rc.ProxyUser,
			Password: rc.ProxyPass,
		}
	} else if rc.ProxyMode != "" {
		cfg.ProxyMode = rc.ProxyMode
	}

	// When browser emulation resolves a mode, the User-Agent lives in
	// config.browser_emulation (authoritative at replay); duplicating it here
	// would leave two editable sources of the same header.
	emulation := rc.ToBrowserEmulationConfig()
	if rc.UserAgent != "" && (emulation == nil || emulation.Mode == "") {
		cfg.UserAgent = rc.UserAgent
	}
	if rc.AcceptLanguage != "" {
		cfg.AcceptLanguage = rc.AcceptLanguage
	}
	if rc.UseGzip {
		cfg.AcceptEncoding = "gzip, deflate"
	}
	if rc.NewUserPerIteration {
		clearCookies := true
		cfg.ClearCookiesPerIter = &clearCookies
	}

	if rc.SSLVersion != "" {
		cfg.TLS = dsl.TLSConfig{
			MinVersion: mapSSLVersion(rc.SSLVersion),
		}
	}

	if rc.AuthUser != "" {
		cfg.Auth = dsl.AuthConfig{
			Type:     "basic",
			Username: rc.AuthUser,
			Password: rc.AuthPass,
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
		// CacheHtmlPages=1 / CacheAlwaysCheckForNewerPages=1. Those children have
		// no effect while the cache is off and the validator rejects the pairing,
		// so drop them when the cache is explicitly disabled (raw values stay in
		// preserved metadata).
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
		if values, ok := sections[alias]; ok {
			return values
		}
		matchingNames := make([]string, 0, 1)
		for name := range sections {
			if strings.EqualFold(name, alias) {
				matchingNames = append(matchingNames, name)
			}
		}
		if len(matchingNames) > 0 {
			sort.Strings(matchingNames)
			return sections[matchingNames[0]]
		}
	}
	return map[string]string{}
}

// sectionValueCaseInsensitive selects the first alias deterministically while
// tolerating the inconsistent key casing emitted across runtime.cfg versions.
func sectionValueCaseInsensitive(section map[string]string, aliases ...string) (string, bool) {
	for _, alias := range aliases {
		if value, ok := section[alias]; ok {
			return value, true
		}
		matchingKeys := make([]string, 0, 1)
		for key := range section {
			if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(alias)) {
				matchingKeys = append(matchingKeys, key)
			}
		}
		if len(matchingKeys) > 0 {
			sort.Strings(matchingKeys)
			return section[matchingKeys[0]], true
		}
	}
	return "", false
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

func firstNonZeroInt(values ...string) int {
	for _, value := range values {
		if parsed := parseInt(value); parsed != 0 {
			return parsed
		}
	}
	return 0
}

func buildRuntimeProxyURL(section map[string]string) string {
	host := strings.TrimSpace(firstNonBlank(section["ProxyHTTPSHost"], section["ProxyHTTPHost"]))
	if host == "" {
		return ""
	}
	port := firstNonZeroInt(section["ProxyHTTPSPort"], section["ProxyHTTPPort"])
	scheme := "http"
	if strings.TrimSpace(section["ProxyHTTPSHost"]) != "" {
		scheme = "https"
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		if port > 0 && !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://"), ":") {
			return strings.TrimRight(host, "/") + ":" + strconv.Itoa(port)
		}
		return host
	}
	if port > 0 {
		return fmt.Sprintf("%s://%s:%d", scheme, host, port)
	}
	return scheme + "://" + host
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
	switch strings.ToLower(v) {
	case "tls1.0", "tls 1.0", "tls10":
		return "1.0"
	case "tls1.1", "tls 1.1", "tls11":
		return "1.1"
	case "tls1.2", "tls 1.2", "tls12", "tls":
		return "1.2"
	case "tls1.3", "tls 1.3", "tls13":
		return "1.3"
	default:
		return "1.2" // safe default
	}
}
