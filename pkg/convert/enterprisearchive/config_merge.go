package enterprisearchive

// config_merge.go — split from converter.go (same package, behavior-preserving).

import "github.com/jasonczech/apexload/pkg/dsl"

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
