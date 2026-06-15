package enterprisearchive

// runtime_config.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// RuntimeConfig holds parsed source runtime settings from a .cfg file.
type RuntimeConfig struct {
	ConnectTimeoutSec            int
	ReceiveTimeoutSec            int
	StepTimeoutSec               int
	HTTPVersion                  string // "1.0" or "1.1"
	KeepAlive                    bool
	MaxConnsPerHost              int
	ProxyMode                    string
	ProxyURL                     string
	ProxyUser                    string
	ProxyPass                    string
	UserAgent                    string
	AcceptLanguage               string
	UseGzip                      bool
	SSLVersion                   string
	AuthUser                     string
	AuthPass                     string
	MaxRedirects                 int
	Iterations                   int
	AutoTransactions             string
	BlockScheduling              string
	ActionSequence               []string
	PacingType                   string
	DelayMs                      int
	MinDelayMs                   int
	MaxDelayMs                   int
	IntervalMs                   int
	MinIntervalMs                int
	MaxIntervalMs                int
	ThinkTimeIgnore              bool
	ThinkTimeMultiplier          float64
	ThinkTimeLimitMs             float64
	LogLevel                     string
	OnErrorOnly                  bool
	LogExtendedOptions           []string
	ContinueOnError              bool
	SnapshotOnError              bool
	CacheEnabled                 bool
	CacheContentURLs             bool
	CheckNewerVersions           bool
	NewUserPerIteration          bool
	BrowserEngine                string
	BrowserDeviceProfile         string
	BrowserTouchMode             bool
	BrowserOrientation           string
	BrowserClientMetrics         bool
	BrowserSlowMoMs              int
	BrowserViewportWidth         int
	BrowserViewportHeight        int
	DownloadResources            string
	AdditionalAttributes         map[string]string
	DownloadFilterMode           string
	DownloadFilterHosts          []string
	ContentChecks                []dsl.ContentCheckConfig
	NetworkSimulationMode        string
	NetworkSimulationPreset      string
	NetworkBandwidthKbps         int
	NetworkLatencyMs             int
	StreamingMode                string
	StreamingBufferSizeSeconds   int
	StreamingFixedBitrateKbps    int
	StreamingBandwidthEstimation string
	DataFormattingAutoDetect     bool
	DataFormattingChain          []dsl.DFETransform
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
	rc.UserAgent = strings.TrimSpace(firstNonBlank(browserSection["CustomUserAgent"], browserSection["UserAgent"], browserSection["BrowserUserAgent"]))
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
	rc.DownloadResources = normalizeEnum(strings.TrimSpace(firstNonBlank(browserSection["DownloadResources"], browserSection["DownloadNonHtmlResources"])))

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

	if rc.UserAgent != "" {
		cfg.UserAgent = rc.UserAgent
	}
	if rc.AcceptLanguage != "" {
		cfg.AcceptLanguage = rc.AcceptLanguage
	}
	if rc.UseGzip {
		cfg.AcceptEncoding = "gzip, deflate"
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

func findSection(sections map[string]map[string]string, aliases ...string) map[string]string {
	for _, alias := range aliases {
		for name, values := range sections {
			if strings.EqualFold(name, alias) {
				return values
			}
		}
	}
	return map[string]string{}
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
