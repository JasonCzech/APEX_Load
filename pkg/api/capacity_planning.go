package api

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

type CapacityResourceEstimate struct {
	CPUCores    float64 `json:"cpu_cores"`
	MemoryMB    float64 `json:"memory_mb"`
	NetworkMbps float64 `json:"network_mbps"`
}

type CapacityDimensionRecommendations struct {
	CPU     int `json:"cpu"`
	Memory  int `json:"memory"`
	Network int `json:"network"`
	VUsers  int `json:"vusers"`
}

type CapacityAdvisory struct {
	TotalVUsers                   int                              `json:"total_vusers"`
	DurationSeconds               int                              `json:"duration_seconds"`
	BaselineWindowEnabled         bool                             `json:"baseline_window_enabled"`
	HeadroomPct                   float64                          `json:"headroom_pct"`
	ComplexityScore               float64                          `json:"complexity_score"`
	Bottleneck                    string                           `json:"bottleneck"`
	AvailabilitySource            string                           `json:"availability_source,omitempty"`
	ConfiguredAvailableGenerators int                              `json:"configured_available_generators,omitempty"`
	LiveAvailableGenerators       *int                             `json:"live_available_generators,omitempty"`
	RecommendedGenerators         int                              `json:"recommended_generators"`
	AvailableGenerators           int                              `json:"available_generators"`
	ShortfallGenerators           int                              `json:"shortfall_generators,omitempty"`
	RequiresOverride              bool                             `json:"requires_override,omitempty"`
	EstimatedPerVUser             CapacityResourceEstimate         `json:"estimated_per_vuser"`
	EstimatedTotal                CapacityResourceEstimate         `json:"estimated_total"`
	UsablePerGenerator            CapacityResourceEstimate         `json:"usable_per_generator"`
	DimensionRecommendations      CapacityDimensionRecommendations `json:"dimension_recommendations"`
	Warnings                      []string                         `json:"warnings,omitempty"`
}

type CapacityAvailabilitySnapshot struct {
	Source                        string
	ConfiguredAvailableGenerators int
	LiveAvailableGenerators       *int
	EffectiveAvailableGenerators  int
	Warnings                      []string
}

func cloneCapacityAdvisory(in *CapacityAdvisory) *CapacityAdvisory {
	if in == nil {
		return nil
	}
	out := *in
	if in.Warnings != nil {
		out.Warnings = append([]string(nil), in.Warnings...)
	}
	if in.LiveAvailableGenerators != nil {
		v := *in.LiveAvailableGenerators
		out.LiveAvailableGenerators = &v
	}
	return &out
}

func estimateCapacityAdvisory(settings SystemSettings, groups []TestGroup, totalVUsers int, duration time.Duration, resourceBaseline bool, runtimeSettings *RuntimeSettingsOverride) *CapacityAdvisory {
	availability := resolveCapacityAvailability(settings)
	return estimateCapacityAdvisoryWithAvailability(settings, availability, groups, totalVUsers, duration, resourceBaseline, runtimeSettings)
}

func estimateCapacityAdvisoryWithAvailability(settings SystemSettings, availability CapacityAvailabilitySnapshot, groups []TestGroup, totalVUsers int, duration time.Duration, resourceBaseline bool, runtimeSettings *RuntimeSettingsOverride) *CapacityAdvisory {
	if totalVUsers <= 0 || len(groups) == 0 {
		return nil
	}

	headroomFraction := settings.CapacityPlanning.HeadroomPct / 100
	usableCPU := settings.CapacityPlanning.Generator.CPUCores * (1 - headroomFraction)
	usableMemory := float64(settings.CapacityPlanning.Generator.MemoryMB) * (1 - headroomFraction)
	usableNetwork := settings.CapacityPlanning.Generator.NetworkMbps * (1 - headroomFraction)
	usableVUsers := int(math.Floor(float64(settings.CapacityPlanning.Generator.MaxVUsers) * (1 - headroomFraction)))
	if usableVUsers < 1 {
		usableVUsers = 1
	}

	complexityScore, warnings := calculateScenarioComplexity(groups, runtimeSettings)
	perVUser := estimatePerVUserFootprint(complexityScore)
	totalEstimate := CapacityResourceEstimate{
		CPUCores:    perVUser.CPUCores * float64(totalVUsers),
		MemoryMB:    perVUser.MemoryMB * float64(totalVUsers),
		NetworkMbps: perVUser.NetworkMbps * float64(totalVUsers),
	}

	recommendations := CapacityDimensionRecommendations{
		CPU:     estimateRequiredGenerators(totalEstimate.CPUCores, usableCPU),
		Memory:  estimateRequiredGenerators(totalEstimate.MemoryMB, usableMemory),
		Network: estimateRequiredGenerators(totalEstimate.NetworkMbps, usableNetwork),
		VUsers:  estimateRequiredGenerators(float64(totalVUsers), float64(usableVUsers)),
	}

	recommended := recommendations.CPU
	bottleneck := "cpu"
	if recommendations.Memory > recommended {
		recommended = recommendations.Memory
		bottleneck = "memory"
	}
	if recommendations.Network > recommended {
		recommended = recommendations.Network
		bottleneck = "network"
	}
	if recommendations.VUsers > recommended {
		recommended = recommendations.VUsers
		bottleneck = "vusers"
	}
	if recommended < 1 {
		recommended = 1
	}

	available := availability.EffectiveAvailableGenerators
	shortfall := 0
	if recommended > available {
		shortfall = recommended - available
	}

	warnings = append(warnings, availability.Warnings...)
	if resourceBaseline {
		warnings = append(warnings, "Baseline windows increase total test timeline but do not increase peak generator demand.")
	}

	return &CapacityAdvisory{
		TotalVUsers:                   totalVUsers,
		DurationSeconds:               int(duration.Seconds()),
		BaselineWindowEnabled:         resourceBaseline,
		HeadroomPct:                   settings.CapacityPlanning.HeadroomPct,
		ComplexityScore:               roundCapacityValue(complexityScore),
		Bottleneck:                    bottleneck,
		AvailabilitySource:            availability.Source,
		ConfiguredAvailableGenerators: availability.ConfiguredAvailableGenerators,
		LiveAvailableGenerators:       availability.LiveAvailableGenerators,
		RecommendedGenerators:         recommended,
		AvailableGenerators:           available,
		ShortfallGenerators:           shortfall,
		RequiresOverride:              shortfall > 0,
		EstimatedPerVUser:             roundCapacityEstimate(perVUser),
		EstimatedTotal:                roundCapacityEstimate(totalEstimate),
		UsablePerGenerator:            roundCapacityEstimate(CapacityResourceEstimate{CPUCores: usableCPU, MemoryMB: usableMemory, NetworkMbps: usableNetwork}),
		DimensionRecommendations:      recommendations,
		Warnings:                      warnings,
	}
}

func resolveCapacityAvailability(settings SystemSettings) CapacityAvailabilitySnapshot {
	configured := settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators
	snapshot := CapacityAvailabilitySnapshot{
		Source:                        settings.CapacityPlanning.Availability.Source,
		ConfiguredAvailableGenerators: configured,
		EffectiveAvailableGenerators:  configured,
	}

	if settings.CapacityPlanning.Availability.Source != capacityAvailabilityConfiguredPlusLive {
		return snapshot
	}

	rawLive := strings.TrimSpace(os.Getenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS"))
	if rawLive == "" {
		snapshot.Warnings = append(snapshot.Warnings, "Live generator availability is enabled but no runtime snapshot was provided; using configured generator inventory.")
		return snapshot
	}

	live, err := strconv.Atoi(rawLive)
	if err != nil || live < 0 {
		snapshot.Warnings = append(snapshot.Warnings, "Live generator availability snapshot is invalid; using configured generator inventory.")
		return snapshot
	}

	snapshot.LiveAvailableGenerators = &live
	snapshot.EffectiveAvailableGenerators = minInt(configured, live)
	return snapshot
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func calculateScenarioComplexity(groups []TestGroup, runtimeSettings *RuntimeSettingsOverride) (float64, []string) {
	score := 1.0
	unknownProtocol := false

	for _, group := range groups {
		scriptScore := 1.0
		protocol := strings.ToLower(group.Protocol)
		switch protocol {
		case "http", "http/1.1", "http/2", "https":
			scriptScore += 0
		case "":
			unknownProtocol = true
			scriptScore += 0.15
		default:
			unknownProtocol = true
			scriptScore += 0.15
		}

		if group.script != nil {
			scriptScore += math.Min(float64(len(group.script.Actions))*0.04, 0.6)
			scriptScore += math.Min(float64(len(group.script.Init)+len(group.script.Teardown))*0.02, 0.2)
			scriptScore += countActionComplexity(group.script.Actions)
			scriptScore += runtimeOverheadScore(group.script.Config, runtimeSettings)
		}
		if group.Initialization == "at_once" {
			scriptScore += 0.08
		}
		score += float64(group.VUsers) * scriptScore
	}

	weightedScore := score / float64(totalScenarioVUsers(groups))
	warnings := make([]string, 0, 2)
	if unknownProtocol {
		warnings = append(warnings, "Unknown protocol detected; the advisor applied conservative per-VUser defaults.")
	}
	return weightedScore, warnings
}

func totalScenarioVUsers(groups []TestGroup) int {
	total := 0
	for _, group := range groups {
		total += group.VUsers
	}
	if total < 1 {
		return 1
	}
	return total
}

func countActionComplexity(actions []dsl.Action) float64 {
	var score float64
	for _, action := range actions {
		score += math.Min(float64(len(action.Assertions))*0.02, 0.12)
		score += math.Min(float64(len(action.Extractors))*0.025, 0.15)
		if len(action.Body) > 0 {
			score += 0.06
		}
		if len(action.Headers) > 6 {
			score += 0.02
		}
		if action.TimeoutMs > 0 {
			score += 0.01
		}
		if !action.ThinkTime.Ignore && action.ThinkTime.Type != "" {
			score -= 0.02
		}
	}
	return score
}

func runtimeOverheadScore(config dsl.ScriptConfig, runtimeSettings *RuntimeSettingsOverride) float64 {
	score := 0.0
	if config.ErrorHandling.SnapshotOnError {
		score += 0.05
	}
	if strings.EqualFold(config.Log.Level, "extended") {
		score += 0.07
	}
	if strings.EqualFold(config.RunLogic.AutoTransactions, "per_action") {
		score += 0.03
	}
	if config.Pacing.Type != "" && config.Pacing.Type != "none" {
		score -= 0.04
	}
	if config.ThinkTime.Type != "" && !config.ThinkTime.Ignore {
		score -= 0.03
	}
	if runtimeSettings == nil {
		return score
	}
	if runtimeSettings.ErrorHandling != nil && runtimeSettings.ErrorHandling.SnapshotOnError {
		score += 0.05
	}
	if strings.EqualFold(runtimeSettings.LogLevel, "extended") {
		score += 0.07
	}
	if strings.EqualFold(runtimeSettings.AutoTransactions, "per_action") {
		score += 0.03
	}
	if runtimeSettings.Pacing != nil && runtimeSettings.Pacing.Type != "" && runtimeSettings.Pacing.Type != "none" {
		score -= 0.04
	}
	if runtimeSettings.ThinkTime != nil && runtimeSettings.ThinkTime.Type != "" && !runtimeSettings.ThinkTime.Ignore {
		score -= 0.03
	}
	return score
}

func estimatePerVUserFootprint(complexityScore float64) CapacityResourceEstimate {
	if complexityScore < 0.7 {
		complexityScore = 0.7
	}
	return CapacityResourceEstimate{
		CPUCores:    math.Max(0.00018, 0.00025*complexityScore),
		MemoryMB:    math.Max(0.20, 0.35*complexityScore),
		NetworkMbps: math.Max(0.02, 0.08*complexityScore),
	}
}

func estimateRequiredGenerators(total float64, usablePerGenerator float64) int {
	if usablePerGenerator <= 0 {
		return 0
	}
	return int(math.Ceil(total / usablePerGenerator))
}

func roundCapacityEstimate(in CapacityResourceEstimate) CapacityResourceEstimate {
	return CapacityResourceEstimate{
		CPUCores:    roundCapacityCPU(in.CPUCores),
		MemoryMB:    roundCapacityValue(in.MemoryMB),
		NetworkMbps: roundCapacityValue(in.NetworkMbps),
	}
}

func roundCapacityCPU(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func roundCapacityValue(v float64) float64 {
	return math.Round(v*100) / 100
}
