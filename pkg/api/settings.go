package api

import (
	"encoding/json"
	"fmt"
)

const systemSettingsStoreKey = "system_settings"

type SystemSettings struct {
	General    GeneralSettings    `json:"general"`
	Thresholds ThresholdSettings  `json:"thresholds"`
}

type GeneralSettings struct {
	APIEndpoint            string `json:"api_endpoint"`
	MaxConcurrentTests     int    `json:"max_concurrent_tests"`
	MetricsIntervalSeconds int    `json:"metrics_interval_seconds"`
}

type ThresholdSettings struct {
	MaxErrorRatePct *float64 `json:"max_error_rate_pct,omitempty"`
	MaxP99LatencyMs *float64 `json:"max_p99_latency_ms,omitempty"`
}

func defaultSystemSettings(maxConcurrent int) SystemSettings {
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	return SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     maxConcurrent,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
	}
}

func cloneSystemSettings(in SystemSettings) SystemSettings {
	out := in
	if in.Thresholds.MaxErrorRatePct != nil {
		v := *in.Thresholds.MaxErrorRatePct
		out.Thresholds.MaxErrorRatePct = &v
	}
	if in.Thresholds.MaxP99LatencyMs != nil {
		v := *in.Thresholds.MaxP99LatencyMs
		out.Thresholds.MaxP99LatencyMs = &v
	}
	return out
}

func validateSystemSettings(settings SystemSettings) error {
	if settings.General.APIEndpoint == "" {
		return fmt.Errorf("general.api_endpoint is required")
	}
	if settings.General.MaxConcurrentTests < 1 {
		return fmt.Errorf("general.max_concurrent_tests must be >= 1")
	}
	if settings.General.MetricsIntervalSeconds < 1 {
		return fmt.Errorf("general.metrics_interval_seconds must be >= 1")
	}
	if settings.Thresholds.MaxErrorRatePct != nil {
		if *settings.Thresholds.MaxErrorRatePct < 0 || *settings.Thresholds.MaxErrorRatePct > 100 {
			return fmt.Errorf("thresholds.max_error_rate_pct must be between 0 and 100")
		}
	}
	if settings.Thresholds.MaxP99LatencyMs != nil {
		if *settings.Thresholds.MaxP99LatencyMs <= 0 {
			return fmt.Errorf("thresholds.max_p99_latency_ms must be > 0")
		}
	}
	return nil
}

func (tr *TestRunner) loadSystemSettings(defaultMaxConcurrent int) {
	tr.settings = defaultSystemSettings(defaultMaxConcurrent)
	if tr.store == nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}

	raw, err := tr.store.GetSetting(systemSettingsStoreKey)
	if err != nil || raw == "" {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}

	var loaded SystemSettings
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}
	if err := validateSystemSettings(loaded); err != nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}
	tr.settings = cloneSystemSettings(loaded)
	tr.maxTests = tr.settings.General.MaxConcurrentTests
}

func (tr *TestRunner) GetSystemSettings() SystemSettings {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return cloneSystemSettings(tr.settings)
}

func (tr *TestRunner) UpdateSystemSettings(settings SystemSettings) (SystemSettings, error) {
	if err := validateSystemSettings(settings); err != nil {
		return SystemSettings{}, err
	}

	normalized := cloneSystemSettings(settings)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return SystemSettings{}, fmt.Errorf("encode settings: %w", err)
	}

	if tr.store != nil {
		if err := tr.store.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
			return SystemSettings{}, fmt.Errorf("persist settings: %w", err)
		}
	}

	tr.mu.Lock()
	tr.settings = normalized
	tr.maxTests = normalized.General.MaxConcurrentTests
	tr.mu.Unlock()

	return normalized, nil
}
