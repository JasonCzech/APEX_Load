package enterprisearchive

// config_merge.go — split from converter.go (same package, behavior-preserving).

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

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
		// Runtime files own transport/simulation settings, while source actions
		// contribute ordered global checks and compatibility attributes. Preserve
		// both instead of replacing the action-derived evidence wholesale.
		contentChecks := cloneContentCheckConfigs(dst.WebRuntime.ContentChecks)
		attributes := cloneStringMap(dst.WebRuntime.AdditionalAttributes)
		dst.WebRuntime = cloneWebRuntimeConfig(src.WebRuntime)
		dst.WebRuntime.ContentChecks = append(dst.WebRuntime.ContentChecks, contentChecks...)
		if len(attributes) > 0 && dst.WebRuntime.AdditionalAttributes == nil {
			dst.WebRuntime.AdditionalAttributes = map[string]string{}
		}
		for key, value := range attributes {
			if _, exists := dst.WebRuntime.AdditionalAttributes[key]; !exists {
				dst.WebRuntime.AdditionalAttributes[key] = value
			}
		}
	}
	if src.BrowserEmulation != nil {
		dst.BrowserEmulation = cloneBrowserEmulationConfig(src.BrowserEmulation)
	}
}

func cloneWebRuntimeConfig(src dsl.WebRuntimeConfig) dsl.WebRuntimeConfig {
	clone := src
	clone.AdditionalAttributes = cloneStringMap(src.AdditionalAttributes)
	clone.DownloadFilters.Hosts = append([]string(nil), src.DownloadFilters.Hosts...)
	clone.ContentChecks = cloneContentCheckConfigs(src.ContentChecks)
	clone.DataFormatting.Chain = append([]dsl.DFETransform(nil), src.DataFormatting.Chain...)
	for index := range clone.DataFormatting.Chain {
		clone.DataFormatting.Chain[index].Options = cloneStringMap(src.DataFormatting.Chain[index].Options)
	}
	clone.SourceIPs = append([]string(nil), src.SourceIPs...)
	clone.NetworkProfiles = append([]dsl.NetworkProfileWeight(nil), src.NetworkProfiles...)
	return clone
}

func cloneContentCheckConfigs(src []dsl.ContentCheckConfig) []dsl.ContentCheckConfig {
	if len(src) == 0 {
		return nil
	}
	clone := append([]dsl.ContentCheckConfig(nil), src...)
	for index := range clone {
		clone[index].FailIfFound = cloneRuntimeBoolPointer(src[index].FailIfFound)
	}
	return clone
}

func cloneBrowserEmulationConfig(src *dsl.BrowserEmulationConfig) *dsl.BrowserEmulationConfig {
	if src == nil {
		return nil
	}
	clone := *src
	if src.Cache != nil {
		cache := dsl.BrowserCacheSimulationConfig{
			SimulateCache:        cloneRuntimeBoolPointer(src.Cache.SimulateCache),
			CacheContentURLs:     cloneRuntimeBoolPointer(src.Cache.CacheContentURLs),
			CheckNewerVersions:   cloneRuntimeBoolPointer(src.Cache.CheckNewerVersions),
			NewUserEachIteration: cloneRuntimeBoolPointer(src.Cache.NewUserEachIteration),
			SimulatePrefetch:     cloneRuntimeBoolPointer(src.Cache.SimulatePrefetch),
			DownloadNonHTML:      cloneRuntimeBoolPointer(src.Cache.DownloadNonHTML),
		}
		clone.Cache = &cache
	}
	return &clone
}

// mergeJavaVuserVMSettings copies only source-owned Java VM settings. Runner
// enablement, command, harness, work directory, and capacity remain operator
// owned, and imported settings must not replace the runner draft defaults.
func mergeJavaVuserVMSettings(dst *dsl.JavaVuserConfig, src dsl.JavaVuserConfig) {
	if dst == nil {
		return
	}
	if src.JDKSelection != "" {
		dst.JDKSelection = src.JDKSelection
	}
	if src.JDKHome != "" {
		dst.JDKHome = src.JDKHome
	}
	if len(src.AdditionalVMParameters) > 0 {
		dst.AdditionalVMParameters = append([]string(nil), src.AdditionalVMParameters...)
	}
	if src.UseXBootClasspath != nil {
		dst.UseXBootClasspath = cloneRuntimeBoolPointer(src.UseXBootClasspath)
	}
	if src.DedicatedClassLoader != nil {
		dst.DedicatedClassLoader = cloneRuntimeBoolPointer(src.DedicatedClassLoader)
	}
	if len(src.Classpath) > 0 {
		dst.Classpath = append([]string(nil), src.Classpath...)
	}
	if len(src.ClasspathAssets) > 0 {
		dst.ClasspathAssets = append([]dsl.JavaClasspathAsset(nil), src.ClasspathAssets...)
	}
	if src.SkipParameterEvaluation != nil {
		dst.SkipParameterEvaluation = cloneRuntimeBoolPointer(src.SkipParameterEvaluation)
	}
}

func mergeJavaVuserRuntimeSettingsIntoResult(result *ConvertResult, src *dsl.ScriptConfig) {
	if result == nil || src == nil {
		return
	}
	for _, script := range result.effectiveScripts() {
		if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
			continue
		}
		mergeJavaVuserVMSettings(&script.Config.JavaVuser, src.JavaVuser)
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
	return len(cfg.AdditionalAttributes) == 0 && cfg.DownloadFilters.Mode == "" && len(cfg.DownloadFilters.Hosts) == 0 && len(cfg.ContentChecks) == 0 && cfg.NetworkSimulation.Mode == "" && cfg.NetworkSimulation.Preset == "" && cfg.NetworkSimulation.BandwidthKbps == 0 && cfg.NetworkSimulation.LatencyMs == 0 && cfg.NetworkSimulation.JitterMs == 0 && cfg.NetworkSimulation.PacketLossPct == 0 && cfg.Streaming.Mode == "" && cfg.Streaming.BufferSizeSeconds == 0 && cfg.Streaming.FixedBitrateKbps == 0 && cfg.Streaming.BandwidthEstimation == "" && !cfg.DataFormatting.AutoDetect && len(cfg.DataFormatting.Chain) == 0 && len(cfg.SourceIPs) == 0 && len(cfg.NetworkProfiles) == 0
}
