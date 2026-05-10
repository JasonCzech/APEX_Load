package api

import (
	"encoding/json"
	"fmt"
)

const systemSettingsStoreKey = "system_settings"

type SystemSettings struct {
	General          GeneralSettings          `json:"general"`
	Thresholds       ThresholdSettings        `json:"thresholds"`
	CapacityPlanning CapacityPlanningSettings `json:"capacity_planning"`
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

type CapacityPlanningSettings struct {
	HeadroomPct  float64                   `json:"headroom_pct"`
	Generator    GeneratorCapacitySettings `json:"generator"`
	Availability AvailabilitySettings      `json:"availability"`
}

type GeneratorCapacitySettings struct {
	CPUCores    float64 `json:"cpu_cores"`
	MemoryMB    int     `json:"memory_mb"`
	NetworkMbps float64 `json:"network_mbps"`
	MaxVUsers   int     `json:"max_vusers"`
}

type AvailabilitySettings struct {
	Source                        string `json:"source"`
	ConfiguredAvailableGenerators int    `json:"configured_available_generators"`
}

const (
	capacityAvailabilityConfigured         = "configured"
	capacityAvailabilityConfiguredPlusLive = "configured_plus_live"
)

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
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
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

func normalizeLoadedSystemSettings(in SystemSettings, defaultMaxConcurrent int) SystemSettings {
	defaults := defaultSystemSettings(defaultMaxConcurrent)
	out := cloneSystemSettings(in)

	if out.General.APIEndpoint == "" {
		out.General.APIEndpoint = defaults.General.APIEndpoint
	}
	if out.General.MaxConcurrentTests == 0 {
		out.General.MaxConcurrentTests = defaults.General.MaxConcurrentTests
	}
	if out.General.MetricsIntervalSeconds == 0 {
		out.General.MetricsIntervalSeconds = defaults.General.MetricsIntervalSeconds
	}
	if out.CapacityPlanning.HeadroomPct == 0 {
		out.CapacityPlanning.HeadroomPct = defaults.CapacityPlanning.HeadroomPct
	}
	if out.CapacityPlanning.Generator.CPUCores == 0 {
		out.CapacityPlanning.Generator.CPUCores = defaults.CapacityPlanning.Generator.CPUCores
	}
	if out.CapacityPlanning.Generator.MemoryMB == 0 {
		out.CapacityPlanning.Generator.MemoryMB = defaults.CapacityPlanning.Generator.MemoryMB
	}
	if out.CapacityPlanning.Generator.NetworkMbps == 0 {
		out.CapacityPlanning.Generator.NetworkMbps = defaults.CapacityPlanning.Generator.NetworkMbps
	}
	if out.CapacityPlanning.Generator.MaxVUsers == 0 {
		out.CapacityPlanning.Generator.MaxVUsers = defaults.CapacityPlanning.Generator.MaxVUsers
	}
	if out.CapacityPlanning.Availability.Source == "" {
		out.CapacityPlanning.Availability.Source = defaults.CapacityPlanning.Availability.Source
	}
	if out.CapacityPlanning.Availability.ConfiguredAvailableGenerators == 0 {
		out.CapacityPlanning.Availability.ConfiguredAvailableGenerators = defaults.CapacityPlanning.Availability.ConfiguredAvailableGenerators
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
	if settings.CapacityPlanning.HeadroomPct < 0 || settings.CapacityPlanning.HeadroomPct >= 100 {
		return fmt.Errorf("capacity_planning.headroom_pct must be between 0 and 100")
	}
	if settings.CapacityPlanning.Generator.CPUCores <= 0 {
		return fmt.Errorf("capacity_planning.generator.cpu_cores must be > 0")
	}
	if settings.CapacityPlanning.Generator.MemoryMB <= 0 {
		return fmt.Errorf("capacity_planning.generator.memory_mb must be > 0")
	}
	if settings.CapacityPlanning.Generator.NetworkMbps <= 0 {
		return fmt.Errorf("capacity_planning.generator.network_mbps must be > 0")
	}
	if settings.CapacityPlanning.Generator.MaxVUsers <= 0 {
		return fmt.Errorf("capacity_planning.generator.max_vusers must be > 0")
	}
	if settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators <= 0 {
		return fmt.Errorf("capacity_planning.availability.configured_available_generators must be > 0")
	}
	switch settings.CapacityPlanning.Availability.Source {
	case capacityAvailabilityConfigured, capacityAvailabilityConfiguredPlusLive:
	default:
		return fmt.Errorf("capacity_planning.availability.source must be one of %q or %q", capacityAvailabilityConfigured, capacityAvailabilityConfiguredPlusLive)
	}
	return nil
}

func (tr *TestRunner) loadSystemSettings(defaultMaxConcurrent int) {
	tr.settings = defaultSystemSettings(defaultMaxConcurrent)
	if tr.settingsStore == nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}

	raw, err := tr.settingsStore.GetSetting(systemSettingsStoreKey)
	if err != nil || raw == "" {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}

	var loaded SystemSettings
	if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
		tr.maxTests = tr.settings.General.MaxConcurrentTests
		return
	}
	loaded = normalizeLoadedSystemSettings(loaded, defaultMaxConcurrent)
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

	if tr.settingsStore != nil {
		if err := tr.settingsStore.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
			return SystemSettings{}, fmt.Errorf("persist settings: %w", err)
		}
	}

	tr.mu.Lock()
	tr.settings = normalized
	tr.maxTests = normalized.General.MaxConcurrentTests
	tr.mu.Unlock()

	return normalized, nil
}
