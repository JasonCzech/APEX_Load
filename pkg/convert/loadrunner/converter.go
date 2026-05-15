package loadrunner

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	browserprotocol "github.com/jasonczech/stormrunner/pkg/protocol/browser"
)

// ConvertResult holds the output of a LoadRunner to Stormrunner DSL conversion.
type ConvertResult struct {
	Script                       *dsl.Script  `json:"script"`
	Warnings                     []string     `json:"warnings,omitempty"`
	Stats                        ConvertStats `json:"stats"`
	RecordedResourceURLs         []string     `json:"recorded_resource_urls,omitempty"`
	RecordedResourceURLsCaptured bool         `json:"recorded_resource_urls_captured,omitempty"`
}

// ConvertStats provides conversion statistics for reporting.
type ConvertStats struct {
	TotalFunctions   int      `json:"total_functions"`
	ConvertedActions int      `json:"converted_actions"`
	Extractors       int      `json:"extractors"`
	ThinkTimes       int      `json:"think_times"`
	Transactions     int      `json:"transactions"`
	Skipped          int      `json:"skipped"`
	UnsupportedFuncs []string `json:"unsupported_funcs,omitempty"`
}

// ConvertOptions controls the conversion behavior.
type ConvertOptions struct {
	ScriptName           string  // Override the generated script name
	BaseURL              string  // Override base URL (otherwise auto-detected from first URL)
	DefaultThinkTimeMs   float64 // Default think time if none in script (0 = no default)
	IncludeAssertions    bool    // Add default status_code assertions
	PreserveTransactions bool    // Wrap actions in transaction markers (as comments in name)

	// HTTP runtime settings (mapped from VuGen runtime config)
	HTTPConfig      *dsl.HTTPConfig   // Pre-built HTTP config to embed in script
	RuntimeSettings *dsl.ScriptConfig // Broader runtime settings mapped from default.cfg
}

// Convert transforms parsed LoadRunner action files into a Stormrunner DSL Script.
func Convert(actions []*ParsedAction, opts ConvertOptions) (*ConvertResult, error) {
	result := &ConvertResult{
		Warnings: make([]string, 0),
		Script: &dsl.Script{
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "stormrunner-loadrunner-converter",
			GeneratedFrom: "loadrunner",
			Config:        dsl.ScriptConfig{},
			Actions:       make([]dsl.Action, 0),
		},
	}

	// Collect all functions across action files in order
	var allFunctions []ParsedFunction
	for _, a := range actions {
		allFunctions = append(allFunctions, a.Functions...)
		result.Stats.TotalFunctions += len(a.Functions)
	}

	if len(allFunctions) == 0 {
		return nil, fmt.Errorf("no LoadRunner functions found in action files")
	}

	protocolName, soapConfig, protocolWarning := detectImportProtocol(allFunctions)
	result.Script.Protocol = protocolName
	if soapConfig != nil {
		result.Script.Config.SOAP = *soapConfig
	}
	if protocolWarning != "" {
		result.Warnings = append(result.Warnings, protocolWarning)
	}

	// Auto-detect base URL from first web_ call
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = detectBaseURL(allFunctions)
	}
	result.Script.Config.BaseURL = baseURL
	result.RecordedResourceURLs = collectRecordedResourceURLs(allFunctions)
	result.RecordedResourceURLsCaptured = true

	// Set script name
	if opts.ScriptName != "" {
		result.Script.Name = opts.ScriptName
	} else {
		result.Script.Name = "Converted_LR_Script"
	}

	// Process functions — correlations/extractors come BEFORE the request they apply to
	var pendingExtractors []dsl.Extractor
	var pendingAssertions []dsl.Assertion
	var pendingHeaders map[string]string
	var currentTransaction string
	var thinkTimes []float64

	actionNameCounts := make(map[string]int)

	for _, fn := range allFunctions {
		switch fn.Name {
		case "web_reg_save_param":
			ext := convertExtractor(fn)
			if ext != nil {
				pendingExtractors = append(pendingExtractors, *ext)
				result.Stats.Extractors++
			}

		case "web_reg_find":
			assertion, warning := convertFindAssertion(fn)
			if assertion != nil {
				pendingAssertions = append(pendingAssertions, *assertion)
			}
			if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}

		case "web_add_header", "web_add_auto_header":
			if pendingHeaders == nil {
				pendingHeaders = make(map[string]string)
			}
			for k, v := range fn.Headers {
				pendingHeaders[k] = v
			}

		case "lr_start_transaction":
			currentTransaction = fn.StepName
			result.Stats.Transactions++

		case "lr_end_transaction":
			currentTransaction = ""

		case "lr_think_time":
			sec, ok := ExtractThinkTimeSeconds(fn)
			if ok {
				thinkTimes = append(thinkTimes, sec)
				result.Stats.ThinkTimes++
			}

		case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_service_call":
			action, warnings := convertAction(fn, baseURL, currentTransaction, opts)
			if action != nil {
				if len(pendingAssertions) > 0 {
					action.Assertions = append(action.Assertions, pendingAssertions...)
					pendingAssertions = nil
				}

				// Attach pending extractors
				if len(pendingExtractors) > 0 {
					action.Extractors = append(action.Extractors, pendingExtractors...)
					pendingExtractors = nil
				}

				// Attach pending headers
				if len(pendingHeaders) > 0 {
					if action.Headers == nil {
						action.Headers = make(map[string]string)
					}
					for k, v := range pendingHeaders {
						if _, exists := action.Headers[k]; !exists {
							action.Headers[k] = v
						}
					}
					pendingHeaders = nil
				}

				applySOAPImportHints(action, fn)

				// Default assertions
				if opts.IncludeAssertions {
					action.Assertions = append(action.Assertions, dsl.Assertion{
						Type:     "status_code",
						Expected: 200,
					})
				}

				// Deduplicate action names
				actionNameCounts[action.Name]++
				if actionNameCounts[action.Name] > 1 {
					action.Name = fmt.Sprintf("%s_%d", action.Name, actionNameCounts[action.Name])
				}

				result.Script.Actions = append(result.Script.Actions, *action)
				result.Stats.ConvertedActions++
			}
			result.Warnings = append(result.Warnings, warnings...)

		default:
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
		}
	}

	// Configure think time from collected lr_think_time values
	if len(thinkTimes) > 0 {
		mean, std := computeThinkTimeStats(thinkTimes)
		result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
			Type:   "normal",
			MeanMs: mean * 1000,
			StdMs:  std * 1000,
			MinMs:  math.Max(0, (mean-2*std)*1000),
			MaxMs:  (mean + 2*std) * 1000,
		}
	} else if opts.DefaultThinkTimeMs > 0 {
		result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
			Type:   "fixed",
			MeanMs: opts.DefaultThinkTimeMs,
		}
	}

	// Mark actions with think_time_after (all except last)
	for i := range result.Script.Actions {
		if i < len(result.Script.Actions)-1 {
			result.Script.Actions[i].ThinkTimeAfter = true
		}
	}

	if len(result.Script.Actions) == 0 {
		return nil, fmt.Errorf("no request actions could be converted from the LoadRunner script")
	}

	// Apply HTTP runtime config if provided
	if opts.HTTPConfig != nil {
		result.Script.Config.HTTP = *opts.HTTPConfig
	}
	if opts.RuntimeSettings != nil {
		mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
	}

	return result, nil
}

func mergeScriptConfig(dst *dsl.ScriptConfig, src *dsl.ScriptConfig) {
	if dst == nil || src == nil {
		return
	}
	if src.ThinkTime.Type != "" || src.ThinkTime.MeanMs != 0 || src.ThinkTime.StdMs != 0 || src.ThinkTime.MinMs != 0 || src.ThinkTime.MaxMs != 0 || src.ThinkTime.Multiplier != 0 || src.ThinkTime.LimitMs != 0 || src.ThinkTime.Ignore {
		if src.ThinkTime.Type != "" {
			dst.ThinkTime.Type = src.ThinkTime.Type
		}
		if src.ThinkTime.MeanMs != 0 {
			dst.ThinkTime.MeanMs = src.ThinkTime.MeanMs
		}
		if src.ThinkTime.StdMs != 0 {
			dst.ThinkTime.StdMs = src.ThinkTime.StdMs
		}
		if src.ThinkTime.MinMs != 0 {
			dst.ThinkTime.MinMs = src.ThinkTime.MinMs
		}
		if src.ThinkTime.MaxMs != 0 {
			dst.ThinkTime.MaxMs = src.ThinkTime.MaxMs
		}
		if src.ThinkTime.Multiplier != 0 {
			dst.ThinkTime.Multiplier = src.ThinkTime.Multiplier
		}
		if src.ThinkTime.LimitMs != 0 {
			dst.ThinkTime.LimitMs = src.ThinkTime.LimitMs
		}
		if src.ThinkTime.Ignore {
			dst.ThinkTime.Ignore = true
		}
	}
	if !isEmptyPacingConfig(src.Pacing) {
		dst.Pacing = src.Pacing
	}
	if !isEmptyRunLogicConfig(src.RunLogic) {
		dst.RunLogic = src.RunLogic
	}
	if !isEmptyLogConfig(src.Log) {
		dst.Log = src.Log
	}
	if !isEmptyHTTPConfig(src.HTTP) {
		dst.HTTP = src.HTTP
	}
	if !isEmptyBrowserConfig(src.Browser) {
		dst.Browser = src.Browser
	}
	if !isEmptyErrorHandlingConfig(src.ErrorHandling) {
		dst.ErrorHandling = src.ErrorHandling
	}
	if !isEmptyWebRuntimeConfig(src.WebRuntime) {
		dst.WebRuntime = src.WebRuntime
	}
}

func isEmptyPacingConfig(cfg dsl.PacingConfig) bool {
	return cfg.Type == "" && cfg.DelayMs == 0 && cfg.MinDelayMs == 0 && cfg.MaxDelayMs == 0 && cfg.IntervalMs == 0 && cfg.MinIntervalMs == 0 && cfg.MaxIntervalMs == 0 && cfg.TPS == 0
}

func isEmptyRunLogicConfig(cfg dsl.RunLogicConfig) bool {
	return cfg.Iterations == 0 && cfg.AutoTransactions == "" && cfg.BlockScheduling == "" && len(cfg.ActionSequence) == 0
}

func isEmptyLogConfig(cfg dsl.LogConfig) bool {
	return cfg.Level == "" && !cfg.OnErrorOnly && len(cfg.ExtendedOptions) == 0
}

func isEmptyHTTPConfig(cfg dsl.HTTPConfig) bool {
	return cfg.ConnectTimeoutMs == 0 && cfg.RequestTimeoutMs == 0 && cfg.TLSTimeoutMs == 0 && cfg.IdleTimeoutMs == 0 && cfg.Version == "" && cfg.MaxConnsPerHost == 0 && cfg.MaxIdleConns == 0 && cfg.KeepAlive == nil && cfg.ProxyMode == "" && cfg.UserAgent == "" && cfg.AcceptEncoding == "" && cfg.AcceptLanguage == "" && cfg.MaxRedirects == nil && len(cfg.DefaultHeaders) == 0 && cfg.Proxy.URL == "" && cfg.Proxy.Username == "" && cfg.Proxy.Password == "" && len(cfg.Proxy.NoProxy) == 0 && cfg.Auth.Type == "" && cfg.Auth.Username == "" && cfg.Auth.Password == "" && cfg.Auth.Token == "" && cfg.Auth.Domain == "" && cfg.TLS.MinVersion == "" && cfg.TLS.MaxVersion == "" && !cfg.TLS.InsecureSkipVerify && cfg.TLS.ClientCertFile == "" && cfg.TLS.ClientKeyFile == "" && cfg.TLS.CACertFile == ""
}

func isEmptyBrowserConfig(cfg dsl.BrowserConfig) bool {
	return cfg.Engine == "" && cfg.Headless == nil && cfg.SlowMoMs == 0 && cfg.UserAgent == "" && cfg.DeviceProfile == "" && cfg.TouchMode == nil && cfg.Orientation == "" && cfg.ClientMetrics == nil && cfg.CaptureScreenshots == "" && cfg.CacheEnabled == nil && cfg.CacheContentURLs == nil && cfg.CheckNewerVersions == nil && cfg.NewUserPerIter == nil && cfg.DownloadResources == "" && cfg.Viewport.Width == 0 && cfg.Viewport.Height == 0
}

func isEmptyErrorHandlingConfig(cfg dsl.ErrorHandlingConfig) bool {
	return !cfg.ContinueOnError && cfg.MaxRetries == 0 && cfg.RetryDelayMs == 0 && cfg.RetryBackoff == "" && !cfg.SnapshotOnError
}

func isEmptyWebRuntimeConfig(cfg dsl.WebRuntimeConfig) bool {
	return len(cfg.AdditionalAttributes) == 0 && cfg.DownloadFilters.Mode == "" && len(cfg.DownloadFilters.Hosts) == 0 && len(cfg.ContentChecks) == 0 && cfg.NetworkSimulation.Mode == "" && cfg.NetworkSimulation.Preset == "" && cfg.NetworkSimulation.BandwidthKbps == 0 && cfg.NetworkSimulation.LatencyMs == 0 && cfg.Streaming.Mode == "" && cfg.Streaming.BufferSizeSeconds == 0 && cfg.Streaming.FixedBitrateKbps == 0 && cfg.Streaming.BandwidthEstimation == "" && !cfg.DataFormatting.AutoDetect && len(cfg.DataFormatting.Chain) == 0
}

func detectImportProtocol(functions []ParsedFunction) (string, *dsl.SOAPConfig, string) {
	requestCount := 0
	soapCount := 0
	soapVersion := ""

	for _, fn := range functions {
		if !isRequestFunction(fn.Name) {
			continue
		}
		requestCount++
		if !isSOAPFunction(fn) {
			continue
		}
		soapCount++
		if version := detectSOAPVersion(fn); version == "1.2" {
			soapVersion = "1.2"
		} else if soapVersion == "" {
			soapVersion = "1.1"
		}
	}

	if requestCount == 0 || soapCount == 0 {
		return "http", nil, ""
	}
	if soapCount != requestCount {
		return "http", nil, fmt.Sprintf("detected %d SOAP-style requests and %d non-SOAP requests; keeping HTTP protocol because the imported script is mixed", soapCount, requestCount-soapCount)
	}
	if soapVersion == "" {
		soapVersion = "1.1"
	}
	return "soap", &dsl.SOAPConfig{
		Version: soapVersion,
		FaultHandling: dsl.SOAPFaultHandlingConfig{
			FailOnFault: true,
		},
	}, ""
}

func isRequestFunction(name string) bool {
	switch name {
	case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_service_call":
		return true
	default:
		return false
	}
}

func isSOAPFunction(fn ParsedFunction) bool {
	if fn.Name == "web_service_call" {
		return true
	}
	if strings.TrimSpace(firstNonEmptyArg(fn, "SOAPAction")) != "" {
		return true
	}
	for key := range fn.Headers {
		if strings.EqualFold(key, "SOAPAction") {
			return true
		}
	}
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(fn.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return true
	}
	body := strings.ToLower(strings.TrimSpace(fn.Body))
	if strings.Contains(body, ":envelope") || strings.Contains(body, "<envelope") {
		return true
	}
	if strings.Contains(contentType, "text/xml") && body != "" {
		return strings.Contains(body, "soap") || strings.Contains(body, "<envelope")
	}
	return false
}

func detectSOAPVersion(fn ParsedFunction) string {
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(fn.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return "1.2"
	}
	body := strings.ToLower(strings.TrimSpace(fn.Body))
	if strings.Contains(body, "http://www.w3.org/2003/05/soap-envelope") {
		return "1.2"
	}
	if isSOAPFunction(fn) {
		return "1.1"
	}
	return ""
}

func firstHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func applySOAPImportHints(action *dsl.Action, fn ParsedFunction) {
	if action == nil || !isSOAPFunction(fn) {
		return
	}
	if action.Method == "" || strings.EqualFold(action.Method, "GET") {
		action.Method = "POST"
		action.Type = "POST"
	}
	soapAction := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPAction"))
	if soapAction == "" {
		soapAction = strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction"))
	}
	operation := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPMethod"))
	if operation == "" {
		operation = strings.TrimSpace(firstNonEmptyArg(fn, "Operation"))
	}
	if operation == "" && soapAction != "" {
		operation = strings.Trim(soapAction, `"`)
	}
	if operation == "" {
		operation = action.Name
	}
	bodyMode := "generated"
	if strings.TrimSpace(action.Body) != "" {
		bodyMode = "raw_xml"
	}
	action.SOAP = &dsl.SOAPActionConfig{
		Action:    strings.Trim(soapAction, `"`),
		Operation: operation,
		BodyMode:  bodyMode,
	}
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}
	if contentType := firstHeaderValue(action.Headers, "Content-Type"); contentType == "" {
		if detectSOAPVersion(fn) == "1.2" {
			action.Headers["Content-Type"] = "application/soap+xml"
		} else {
			action.Headers["Content-Type"] = "text/xml"
		}
	}
}

// convertAction converts a single web_ function to a DSL Action.
func convertAction(fn ParsedFunction, baseURL, transaction string, opts ConvertOptions) (*dsl.Action, []string) {
	var warnings []string

	action := &dsl.Action{
		Headers: make(map[string]string),
	}

	// Determine name
	name := sanitizeName(fn.StepName)
	if opts.PreserveTransactions && transaction != "" {
		name = sanitizeName(transaction) + "_" + name
	}
	action.Name = name

	// Set method and type
	action.Method = fn.Method
	action.Type = fn.Method
	if action.Type == "" {
		action.Type = "GET"
		action.Method = "GET"
	}

	// Determine target URL (relative to base)
	rawURL := fn.URL
	if rawURL == "" {
		warnings = append(warnings, fmt.Sprintf("line %d: %s '%s' has no URL, skipping", fn.LineNumber, fn.Name, fn.StepName))
		return nil, warnings
	}

	target := makeRelative(rawURL, baseURL)
	action.Target = target

	// Apply headers
	for k, v := range fn.Headers {
		action.Headers[k] = v
	}

	// Apply body
	if fn.Body != "" {
		action.Body = fn.Body
		// Convert LoadRunner parameter syntax {ParamName} → template syntax {{var.ParamName}}
		action.Body = convertParamReferences(action.Body)
	}

	// Convert parameter references in URL/target too
	action.Target = convertParamReferences(action.Target)

	return action, warnings
}

// convertExtractor converts a web_reg_save_param to a DSL Extractor.
func convertExtractor(fn ParsedFunction) *dsl.Extractor {
	paramName := fn.StepName
	if paramName == "" {
		return nil
	}

	lb := fn.Args["LB"]
	rb := fn.Args["RB"]

	if lb == "" && rb == "" {
		// Try positional args: web_reg_save_param("Name", "LB=...", "RB=...", ...)
		for _, raw := range fn.RawArgs {
			if strings.HasPrefix(raw, "LB=") {
				lb = strings.TrimPrefix(raw, "LB=")
			}
			if strings.HasPrefix(raw, "RB=") {
				rb = strings.TrimPrefix(raw, "RB=")
			}
		}
	}

	ext := &dsl.Extractor{
		Name:   paramName,
		Source: "body",
	}

	// If boundaries look like JSON path markers, use jsonpath
	if strings.Contains(lb, "\"") && strings.Contains(rb, "\"") {
		ext.Type = "regex"
		ext.Expression = regexp.QuoteMeta(lb) + "(.+?)" + regexp.QuoteMeta(rb)
	} else if lb != "" || rb != "" {
		ext.Type = "boundary"
		ext.Expression = fmt.Sprintf("LB=%s|RB=%s", lb, rb)
	} else {
		ext.Type = "regex"
		ext.Expression = "(.+?)"
	}

	return ext
}

func convertFindAssertion(fn ParsedFunction) (*dsl.Assertion, string) {
	text := firstNonEmptyArg(fn, "Text")
	if text == "" {
		return nil, fmt.Sprintf("line %d: web_reg_find '%s' — manual review recommended because no Text argument was found", fn.LineNumber, fn.StepName)
	}

	assertionType := "body_contains"
	failMode := strings.ToLower(firstNonEmptyArg(fn, "Fail"))
	if failMode == "found" {
		assertionType = "body_not_contains"
	}

	warnings := make([]string, 0, 2)
	if saveCount := firstNonEmptyArg(fn, "SaveCount"); saveCount != "" {
		warnings = append(warnings,
			fmt.Sprintf("line %d: web_reg_find '%s' SaveCount=%s was not preserved; mapped to %s only", fn.LineNumber, fn.StepName, saveCount, assertionType))
	}

	return &dsl.Assertion{
		Type:  assertionType,
		Value: convertParamReferences(text),
	}, strings.Join(warnings, "; ")
}

func firstNonEmptyArg(fn ParsedFunction, key string) string {
	if value := fn.Args[key]; value != "" {
		return value
	}

	prefix := key + "="
	for _, raw := range fn.RawArgs {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimPrefix(raw, prefix)
		}
	}

	return ""
}

// detectBaseURL finds the common base URL from all web_ function calls.
func detectBaseURL(functions []ParsedFunction) string {
	var urls []string
	for _, fn := range functions {
		if fn.URL != "" && strings.HasPrefix(fn.URL, "http") {
			urls = append(urls, fn.URL)
		}
	}

	if len(urls) == 0 {
		return "https://example.com"
	}

	// Parse first URL to get base
	parsed, err := url.Parse(urls[0])
	if err != nil {
		return urls[0]
	}

	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

// makeRelative strips the base URL from an absolute URL to create a relative path.
func makeRelative(rawURL, baseURL string) string {
	if baseURL != "" && strings.HasPrefix(rawURL, baseURL) {
		rel := strings.TrimPrefix(rawURL, baseURL)
		if rel == "" {
			rel = "/"
		}
		return rel
	}

	// Try to extract just the path
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	path := parsed.Path
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path == "" {
		path = "/"
	}
	return path
}

func collectRecordedResourceURLs(functions []ParsedFunction) []string {
	urls := make([]string, 0)
	seen := make(map[string]struct{})
	for _, fn := range functions {
		if !isRequestFunction(fn.Name) || firstNonEmptyArg(fn, "Resource") != "1" {
			continue
		}
		rawURL := strings.TrimSpace(fn.URL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(fn.Action)
		}
		if rawURL == "" {
			continue
		}
		if _, ok := seen[rawURL]; ok {
			continue
		}
		seen[rawURL] = struct{}{}
		urls = append(urls, rawURL)
	}
	return urls
}

// sanitizeName converts a LoadRunner step name to a valid DSL action name.
func sanitizeName(name string) string {
	if name == "" {
		return "unnamed_action"
	}

	// Replace spaces and special chars with underscores
	replacer := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		"/", "_",
		"\\", "_",
		"(", "",
		")", "",
		"[", "",
		"]", "",
		"'", "",
		"\"", "",
		",", "",
		":", "_",
	)

	result := replacer.Replace(name)

	// Remove consecutive underscores
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}

	// Trim leading/trailing underscores
	result = strings.Trim(result, "_")

	if result == "" {
		return "unnamed_action"
	}

	return result
}

// convertParamReferences converts LoadRunner {ParamName} to Stormrunner {{var.ParamName}}.
func convertParamReferences(s string) string {
	// Match {ParamName} but not {{already.converted}}
	re := regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	return re.ReplaceAllString(s, "{{var.$1}}")
}

// computeThinkTimeStats calculates mean and standard deviation for think times.
func computeThinkTimeStats(times []float64) (mean, std float64) {
	if len(times) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, t := range times {
		sum += t
	}
	mean = sum / float64(len(times))

	if len(times) == 1 {
		return mean, 0
	}

	variance := 0.0
	for _, t := range times {
		diff := t - mean
		variance += diff * diff
	}
	variance /= float64(len(times) - 1)
	std = math.Sqrt(variance)

	return mean, std
}

// RuntimeConfig holds parsed VuGen runtime settings from a .cfg file.
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

// ParseRuntimeCfg parses a VuGen default.cfg / runtime settings .cfg file.
func ParseRuntimeCfg(content string) *RuntimeConfig {
	rc := &RuntimeConfig{
		KeepAlive:            true,
		HTTPVersion:          "1.1",
		MaxRedirects:         10,
		AdditionalAttributes: make(map[string]string),
	}

	sections := ParseConfigSections(content)
	httpSection := findSection(sections, "HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences")
	rc.ConnectTimeoutSec = parseInt(httpSection["ConnectTimeout"])
	rc.ReceiveTimeoutSec = parseInt(httpSection["ReceiveTimeout"])
	rc.StepTimeoutSec = parseInt(httpSection["StepDownloadTimeout"])
	if version := strings.TrimSpace(httpSection["HTTPVersion"]); version != "" {
		rc.HTTPVersion = version
	}
	if keepAlive, ok := parseBoolExplicit(httpSection["KeepAlive"]); ok {
		rc.KeepAlive = keepAlive
	}
	if redirects := parseInt(httpSection["MaxRedirections"]); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	rc.MaxConnsPerHost = firstNonZeroInt(httpSection["MaxConnectionsPerHost"], httpSection["MaxConnections"], httpSection["MaxConnectionsPerHostHTTP"])

	proxySection := findSection(sections, "Proxy")
	rc.ProxyMode = normalizeEnum(strings.TrimSpace(firstNonBlank(proxySection["Mode"], proxySection["ProxyMode"], proxySection["Type"])))
	rc.ProxyURL = strings.TrimSpace(firstNonBlank(proxySection["ProxyServer"], proxySection["ProxyURL"], proxySection["Server"]))
	rc.ProxyUser = strings.TrimSpace(firstNonBlank(proxySection["ProxyUserName"], proxySection["UserName"]))
	rc.ProxyPass = strings.TrimSpace(firstNonBlank(proxySection["ProxyPassword"], proxySection["Password"]))

	browserSection := findSection(sections, "Browser Emulation", "Browser")
	rc.UserAgent = strings.TrimSpace(browserSection["UserAgent"])
	rc.AcceptLanguage = strings.TrimSpace(browserSection["AcceptLanguage"])
	rc.UseGzip = strings.Contains(strings.ToLower(browserSection["AcceptEncoding"]), "gzip")
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
	rc.Iterations = parseInt(firstNonBlank(runLogicSection["Iterations"], runLogicSection["NumberOfIterations"]))
	rc.AutoTransactions = normalizeEnum(runLogicSection["AutoTransactions"])
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
	rc.ContinueOnError = parseBool(firstNonBlank(miscSection["ContinueOnError"], miscSection["ErrorContinue"]))
	rc.SnapshotOnError = parseBool(firstNonBlank(miscSection["SnapshotOnError"], miscSection["GenerateSnapshotOnError"]))

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
			Scope:       normalizeEnum(firstNonBlank(contentCheckSection["Scope"], "any_response")),
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

var loadRunnerBrowserDeviceAliases = map[string]string{
	"ipad_pro": "ipad_air",
}

func resolveImportedBrowserProfile(profile string) (string, string) {
	normalized := normalizeEnum(profile)
	if normalized == "" {
		return "", ""
	}
	if alias, ok := loadRunnerBrowserDeviceAliases[normalized]; ok {
		normalized = alias
	}
	device, ok := browserprotocol.LookupDevice(normalized)
	if !ok {
		return normalized, ""
	}
	return normalized, device.BrowserFamily
}

// ToHTTPConfig converts parsed VuGen runtime settings into a Stormrunner HTTPConfig.
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

// mapSSLVersion converts VuGen SSL version identifiers to standard version strings.
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
