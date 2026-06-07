package api

import (
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
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

type ProtocolDensityEnvelope struct {
	Protocol                string  `json:"protocol"`
	VUsers                  int     `json:"vusers"`
	MaxVUsersPerGenerator   int     `json:"max_vusers_per_generator"`
	CPUCoresPerVUser        float64 `json:"cpu_cores_per_vuser"`
	MemoryMBPerVUser        float64 `json:"memory_mb_per_vuser"`
	NetworkMbpsPerVUser     float64 `json:"network_mbps_per_vuser"`
	CompatibilityConfidence string  `json:"compatibility_confidence,omitempty"`
	BenchmarkProfile        string  `json:"benchmark_profile,omitempty"`
	BenchmarkEvidence       string  `json:"benchmark_evidence,omitempty"`
	CalibrationSource       string  `json:"calibration_source,omitempty"`
	CalibratedAt            string  `json:"calibrated_at,omitempty"`
}

type CapacityAdvisory struct {
	TotalVUsers                   int                              `json:"total_vusers"`
	DurationSeconds               int                              `json:"duration_seconds"`
	BaselineWindowEnabled         bool                             `json:"baseline_window_enabled"`
	WorkloadClass                 string                           `json:"workload_class,omitempty"`
	BrowserVUsers                 int                              `json:"browser_vusers,omitempty"`
	MaxBrowserVUsersPerGenerator  int                              `json:"max_browser_vusers_per_generator,omitempty"`
	ProtocolDensities             []ProtocolDensityEnvelope        `json:"protocol_densities,omitempty"`
	HeadroomPct                   float64                          `json:"headroom_pct"`
	ComplexityScore               float64                          `json:"complexity_score"`
	Bottleneck                    string                           `json:"bottleneck"`
	AvailabilitySource            string                           `json:"availability_source,omitempty"`
	ConfiguredAvailableGenerators int                              `json:"configured_available_generators,omitempty"`
	LiveAvailableGenerators       *int                             `json:"live_available_generators,omitempty"`
	ReservationProjectID          string                           `json:"reservation_project_id,omitempty"`
	ReservedGenerators            int                              `json:"reserved_generators,omitempty"`
	SharedAvailableGenerators     int                              `json:"shared_available_generators,omitempty"`
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
	ReservationProjectID          string
	ReservedGenerators            int
	SharedAvailableGenerators     int
	Warnings                      []string
}

type capacityWorkloadProfile struct {
	Class                        string
	BrowserVUsers                int
	MaxBrowserVUsersPerGenerator int
	BrowserMemoryMBPerVUser      float64
	BrowserCPUCoresPerVUser      float64
	BrowserNetworkMbpsPerVUser   float64
	ProtocolDensities            []ProtocolDensityEnvelope
	Warnings                     []string
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
	if in.ProtocolDensities != nil {
		out.ProtocolDensities = append([]ProtocolDensityEnvelope(nil), in.ProtocolDensities...)
	}
	return &out
}

func estimateCapacityAdvisory(settings SystemSettings, groups []TestGroup, totalVUsers int, duration time.Duration, resourceBaseline bool, runtimeSettings *RuntimeSettingsOverride) *CapacityAdvisory {
	availability := resolveCapacityAvailability(settings)
	return estimateCapacityAdvisoryWithAvailability(settings, availability, groups, totalVUsers, duration, resourceBaseline, runtimeSettings)
}

func estimateCapacityAdvisoryForProject(settings SystemSettings, projectID string, groups []TestGroup, totalVUsers int, duration time.Duration, resourceBaseline bool, runtimeSettings *RuntimeSettingsOverride) *CapacityAdvisory {
	availability := resolveCapacityAvailabilityForProject(settings, projectID)
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
	workloadProfile := classifyCapacityWorkload(groups, runtimeSettings)
	if workloadProfile.BrowserVUsers > 0 && workloadProfile.BrowserVUsers < totalVUsers {
		workloadProfile.Class = "mixed"
	}
	warnings = append(warnings, workloadProfile.Warnings...)
	perVUser, totalEstimate := estimateCapacityFootprint(complexityScore, workloadProfile, totalVUsers)

	vuserRecommendation := estimateRequiredGenerators(float64(totalVUsers), float64(usableVUsers))
	for _, density := range workloadProfile.ProtocolDensities {
		usableProtocolVUsers := int(math.Floor(float64(density.MaxVUsersPerGenerator) * (1 - headroomFraction)))
		if usableProtocolVUsers < 1 {
			usableProtocolVUsers = 1
		}
		protocolRecommendation := estimateRequiredGenerators(float64(density.VUsers), float64(usableProtocolVUsers))
		vuserRecommendation = maxInt(vuserRecommendation, protocolRecommendation)
	}
	if workloadProfile.MaxBrowserVUsersPerGenerator > 0 {
		browserDensityRecommendation := estimateRequiredGenerators(float64(workloadProfile.BrowserVUsers), float64(workloadProfile.MaxBrowserVUsersPerGenerator))
		vuserRecommendation = maxInt(vuserRecommendation, browserDensityRecommendation)
	}

	recommendations := CapacityDimensionRecommendations{
		CPU:     estimateRequiredGenerators(totalEstimate.CPUCores, usableCPU),
		Memory:  estimateRequiredGenerators(totalEstimate.MemoryMB, usableMemory),
		Network: estimateRequiredGenerators(totalEstimate.NetworkMbps, usableNetwork),
		VUsers:  vuserRecommendation,
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
		WorkloadClass:                 workloadProfile.Class,
		BrowserVUsers:                 workloadProfile.BrowserVUsers,
		MaxBrowserVUsersPerGenerator:  workloadProfile.MaxBrowserVUsersPerGenerator,
		ProtocolDensities:             cloneProtocolDensityEnvelopes(workloadProfile.ProtocolDensities),
		HeadroomPct:                   settings.CapacityPlanning.HeadroomPct,
		ComplexityScore:               roundCapacityValue(complexityScore),
		Bottleneck:                    bottleneck,
		AvailabilitySource:            availability.Source,
		ConfiguredAvailableGenerators: availability.ConfiguredAvailableGenerators,
		LiveAvailableGenerators:       availability.LiveAvailableGenerators,
		ReservationProjectID:          availability.ReservationProjectID,
		ReservedGenerators:            availability.ReservedGenerators,
		SharedAvailableGenerators:     availability.SharedAvailableGenerators,
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
	return resolveCapacityAvailabilityAt(settings, time.Now())
}

func resolveCapacityAvailabilityAt(settings SystemSettings, now time.Time) CapacityAvailabilitySnapshot {
	configured := settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators
	snapshot := CapacityAvailabilitySnapshot{
		Source:                        settings.CapacityPlanning.Availability.Source,
		ConfiguredAvailableGenerators: configured,
		EffectiveAvailableGenerators:  configured,
	}

	if settings.CapacityPlanning.Availability.Source != capacityAvailabilityConfiguredPlusLive {
		return applyGlobalCapacityReservationSummaryAt(settings, snapshot, now)
	}

	rawLive := strings.TrimSpace(os.Getenv("APEXLOAD_LIVE_AVAILABLE_GENERATORS"))
	if rawLive == "" {
		snapshot.Warnings = append(snapshot.Warnings, "Live generator availability is enabled but no runtime snapshot was provided; using configured generator inventory.")
		return applyGlobalCapacityReservationSummaryAt(settings, snapshot, now)
	}

	live, err := strconv.Atoi(rawLive)
	if err != nil || live < 0 {
		snapshot.Warnings = append(snapshot.Warnings, "Live generator availability snapshot is invalid; using configured generator inventory.")
		return applyGlobalCapacityReservationSummaryAt(settings, snapshot, now)
	}

	snapshot.LiveAvailableGenerators = &live
	snapshot.EffectiveAvailableGenerators = minInt(configured, live)
	return applyGlobalCapacityReservationSummaryAt(settings, snapshot, now)
}

func applyGlobalCapacityReservationSummary(settings SystemSettings, snapshot CapacityAvailabilitySnapshot) CapacityAvailabilitySnapshot {
	return applyGlobalCapacityReservationSummaryAt(settings, snapshot, time.Now())
}

func applyGlobalCapacityReservationSummaryAt(settings SystemSettings, snapshot CapacityAvailabilitySnapshot, now time.Time) CapacityAvailabilitySnapshot {
	totalReserved, _ := capacityReservationTotalsAt(settings, "", now)
	if totalReserved <= 0 {
		return snapshot
	}
	if totalReserved > snapshot.EffectiveAvailableGenerators {
		totalReserved = snapshot.EffectiveAvailableGenerators
	}
	snapshot.ReservedGenerators = totalReserved
	snapshot.SharedAvailableGenerators = snapshot.EffectiveAvailableGenerators - totalReserved
	if snapshot.SharedAvailableGenerators < 0 {
		snapshot.SharedAvailableGenerators = 0
	}
	return snapshot
}

func resolveCapacityAvailabilityForProject(settings SystemSettings, projectID string) CapacityAvailabilitySnapshot {
	return resolveCapacityAvailabilityForProjectAt(settings, projectID, time.Now())
}

func resolveCapacityAvailabilityForProjectAt(settings SystemSettings, projectID string, now time.Time) CapacityAvailabilitySnapshot {
	snapshot := resolveCapacityAvailabilityAt(settings, now)
	normalizedProjectID := normalizeProjectID(strings.TrimSpace(projectID))
	totalReserved, projectReserved := capacityReservationTotalsAt(settings, normalizedProjectID, now)
	if totalReserved == 0 {
		return snapshot
	}

	if totalReserved > snapshot.EffectiveAvailableGenerators {
		totalReserved = snapshot.EffectiveAvailableGenerators
	}
	shared := snapshot.EffectiveAvailableGenerators - totalReserved
	if shared < 0 {
		shared = 0
	}
	effective := shared + projectReserved
	if effective > snapshot.EffectiveAvailableGenerators {
		effective = snapshot.EffectiveAvailableGenerators
	}

	snapshot.ReservationProjectID = normalizedProjectID
	snapshot.ReservedGenerators = projectReserved
	snapshot.SharedAvailableGenerators = shared
	snapshot.EffectiveAvailableGenerators = effective
	if projectReserved > 0 {
		snapshot.Warnings = append(snapshot.Warnings, "Capacity reservation applied: project "+normalizedProjectID+" has "+strconv.Itoa(projectReserved)+" reserved generator"+pluralSuffix(projectReserved)+".")
	} else {
		snapshot.Warnings = append(snapshot.Warnings, strconv.Itoa(totalReserved)+" generator"+pluralSuffix(totalReserved)+" reserved for other projects; using shared pool capacity.")
	}
	return snapshot
}

func capacityReservationTotals(settings SystemSettings, projectID string) (int, int) {
	return capacityReservationTotalsAt(settings, projectID, time.Now())
}

// capacityReservationTotalsAt counts reserved generators (total, and for the
// given project) considering only reservations whose timeslot is active at the
// supplied instant. Split out for deterministic testing of timeslot windows.
func capacityReservationTotalsAt(settings SystemSettings, projectID string, now time.Time) (int, int) {
	normalizedProjectID := normalizeProjectID(strings.TrimSpace(projectID))
	totalReserved := 0
	projectReserved := 0
	for _, reservation := range normalizeCapacityReservations(settings.CapacityPlanning.Reservations) {
		if reservation.ReservedGenerators < 0 || !reservationActiveAt(reservation, now) {
			continue
		}
		totalReserved += reservation.ReservedGenerators
		if normalizedProjectID != "" && reservation.ProjectID == normalizedProjectID {
			projectReserved += reservation.ReservedGenerators
		}
	}
	return totalReserved, projectReserved
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
		protocol := canonicalCapacityProtocol(group)
		switch protocol {
		case "http", "grpc", "websocket", "soap", "java_http", "jms", "browser":
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
			scriptScore += runtimeOverheadScore(group.script.Config, nil)
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

func classifyCapacityWorkload(groups []TestGroup, runtimeSettings *RuntimeSettingsOverride) capacityWorkloadProfile {
	profile := capacityWorkloadProfile{Class: "http"}
	protocolVUsers := make(map[string]int)
	for _, group := range groups {
		if isBrowserCapacityGroup(group) {
			profile.BrowserVUsers += group.VUsers
			profile.Class = "browser"
			deviceName := browserDeviceProfileForCapacity(group, runtimeSettings)
			device, ok := browserprotocol.LookupDevice(deviceName)
			if !ok {
				if strings.TrimSpace(deviceName) != "" {
					profile.Warnings = append(profile.Warnings, "Browser capacity advisor could not resolve the configured device profile; using conservative mobile browser density.")
				}
				profile = mergeBrowserCapacityProfile(profile, browserCapacityProfileForTier("unknown"))
				continue
			}
			profile = mergeBrowserCapacityProfile(profile, browserCapacityProfileForTier(device.DeviceTier))
			continue
		}
		protocol := canonicalCapacityProtocol(group)
		if protocol == "" {
			protocol = "unknown"
		}
		protocolVUsers[protocol] += group.VUsers
	}
	profile.ProtocolDensities = protocolDensityEnvelopesForVUsers(protocolVUsers)
	if profile.BrowserVUsers == 0 {
		if len(profile.ProtocolDensities) == 1 {
			profile.Class = profile.ProtocolDensities[0].Protocol
		} else if len(profile.ProtocolDensities) > 1 {
			profile.Class = "mixed"
		}
		return profile
	}
	if len(profile.ProtocolDensities) > 0 {
		profile.Class = "mixed"
	}
	profile.Warnings = append(profile.Warnings, "Browser VUsers launch real browser runtimes; capacity guidance uses diagnostic canary density rather than HTTP goroutine density.")
	if profile.MaxBrowserVUsersPerGenerator == 0 {
		profile = mergeBrowserCapacityProfile(profile, browserCapacityProfileForTier("unknown"))
	}
	return profile
}

func canonicalCapacityProtocol(group TestGroup) string {
	protocol := strings.ToLower(strings.TrimSpace(group.Protocol))
	if protocol == "" && group.script != nil {
		protocol = strings.ToLower(strings.TrimSpace(group.script.Protocol))
	}
	switch protocol {
	case "https", "http/1.1", "http/2", "web_http", "web":
		return "http"
	case "java-http", "java_http", "java over http", "java-over-http":
		return "java_http"
	case "jms", "jms-solace", "jms_solace", "solace", "ibm-mq", "ibm_mq", "mq":
		return "jms"
	case "ws", "websocket":
		return "websocket"
	default:
		return protocol
	}
}

func protocolDensityEnvelopesForVUsers(protocolVUsers map[string]int) []ProtocolDensityEnvelope {
	if len(protocolVUsers) == 0 {
		return nil
	}
	protocols := make([]string, 0, len(protocolVUsers))
	for protocol, vusers := range protocolVUsers {
		if vusers <= 0 {
			continue
		}
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	out := make([]ProtocolDensityEnvelope, 0, len(protocols))
	for _, protocol := range protocols {
		envelope := protocolDensityEnvelope(protocol)
		envelope.VUsers = protocolVUsers[protocol]
		out = append(out, envelope)
	}
	return out
}

func protocolDensityEnvelope(protocol string) ProtocolDensityEnvelope {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "http":
		return protocolDensityEnvelopeWithBenchmark(ProtocolDensityEnvelope{Protocol: "http", MaxVUsersPerGenerator: 10000, CPUCoresPerVUser: 0.00025, MemoryMBPerVUser: 0.35, NetworkMbpsPerVUser: 0.08, CompatibilityConfidence: "measured_target"}, "http_10k_vusers_4vcpu_8gb")
	case "grpc":
		return protocolDensityEnvelopeWithBenchmark(ProtocolDensityEnvelope{Protocol: "grpc", MaxVUsersPerGenerator: 12000, CPUCoresPerVUser: 0.00022, MemoryMBPerVUser: 0.30, NetworkMbpsPerVUser: 0.06, CompatibilityConfidence: "calibrated_model"}, "grpc_12k_unary_4vcpu_8gb")
	case "websocket":
		return protocolDensityEnvelopeWithBenchmark(ProtocolDensityEnvelope{Protocol: "websocket", MaxVUsersPerGenerator: 6000, CPUCoresPerVUser: 0.00030, MemoryMBPerVUser: 0.50, NetworkMbpsPerVUser: 0.04, CompatibilityConfidence: "calibrated_model"}, "websocket_6k_idle_ping_4vcpu_8gb")
	case "soap":
		return protocolDensityEnvelopeWithBenchmark(ProtocolDensityEnvelope{Protocol: "soap", MaxVUsersPerGenerator: 5000, CPUCoresPerVUser: 0.00040, MemoryMBPerVUser: 0.65, NetworkMbpsPerVUser: 0.12, CompatibilityConfidence: "calibrated_model"}, "soap_5k_xml_2kb_4vcpu_8gb")
	case "java_http":
		return protocolDensityEnvelopeWithBenchmark(ProtocolDensityEnvelope{Protocol: "java_http", MaxVUsersPerGenerator: 7000, CPUCoresPerVUser: 0.00032, MemoryMBPerVUser: 0.45, NetworkMbpsPerVUser: 0.09, CompatibilityConfidence: "calibrated_model"}, "java_http_7k_client_runtime_4vcpu_8gb")
	case "jms":
		return protocolDensityEnvelopeWithBenchmark(ProtocolDensityEnvelope{Protocol: "jms", MaxVUsersPerGenerator: 3000, CPUCoresPerVUser: 0.00050, MemoryMBPerVUser: 0.80, NetworkMbpsPerVUser: 0.05, CompatibilityConfidence: "calibrated_model"}, "jms_3k_session_send_4vcpu_8gb")
	default:
		return protocolDensityEnvelopeWithBenchmark(ProtocolDensityEnvelope{Protocol: "unknown", MaxVUsersPerGenerator: 4000, CPUCoresPerVUser: 0.00045, MemoryMBPerVUser: 0.75, NetworkMbpsPerVUser: 0.10, CompatibilityConfidence: "conservative"}, "unknown_conservative_4vcpu_8gb")
	}
}

func protocolDensityEnvelopeWithBenchmark(envelope ProtocolDensityEnvelope, profile string) ProtocolDensityEnvelope {
	envelope.BenchmarkProfile = profile
	envelope.BenchmarkEvidence = "docs/ENTERPRISE_AUTOSCALING_SIGNALS.md#protocol-density-envelopes"
	envelope.CalibrationSource = "apexload_benchmark_catalog"
	envelope.CalibratedAt = "2026-05-18"
	return envelope
}

func isBrowserCapacityGroup(group TestGroup) bool {
	if strings.EqualFold(strings.TrimSpace(group.Protocol), "browser") {
		return true
	}
	if group.script == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(group.script.Protocol), "browser") {
		return true
	}
	return strings.TrimSpace(group.script.Config.Browser.Engine) != "" || strings.TrimSpace(group.script.Config.Browser.DeviceProfile) != ""
}

func browserDeviceProfileForCapacity(group TestGroup, runtimeSettings *RuntimeSettingsOverride) string {
	if group.RuntimeSettings != nil && group.RuntimeSettings.Browser != nil && strings.TrimSpace(group.RuntimeSettings.Browser.DeviceProfile) != "" {
		return strings.TrimSpace(group.RuntimeSettings.Browser.DeviceProfile)
	}
	if runtimeSettings != nil && runtimeSettings.Browser != nil && strings.TrimSpace(runtimeSettings.Browser.DeviceProfile) != "" {
		return strings.TrimSpace(runtimeSettings.Browser.DeviceProfile)
	}
	if group.script == nil {
		return ""
	}
	return strings.TrimSpace(group.script.Config.Browser.DeviceProfile)
}

func browserCapacityProfileForTier(tier string) capacityWorkloadProfile {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "low":
		return capacityWorkloadProfile{MaxBrowserVUsersPerGenerator: 40, BrowserMemoryMBPerVUser: 180, BrowserCPUCoresPerVUser: 0.055, BrowserNetworkMbpsPerVUser: 0.25}
	case "mid":
		return capacityWorkloadProfile{MaxBrowserVUsersPerGenerator: 32, BrowserMemoryMBPerVUser: 220, BrowserCPUCoresPerVUser: 0.07, BrowserNetworkMbpsPerVUser: 0.35}
	case "high":
		return capacityWorkloadProfile{MaxBrowserVUsersPerGenerator: 25, BrowserMemoryMBPerVUser: 260, BrowserCPUCoresPerVUser: 0.085, BrowserNetworkMbpsPerVUser: 0.45}
	default:
		return capacityWorkloadProfile{MaxBrowserVUsersPerGenerator: 20, BrowserMemoryMBPerVUser: 280, BrowserCPUCoresPerVUser: 0.09, BrowserNetworkMbpsPerVUser: 0.5}
	}
}

func mergeBrowserCapacityProfile(left capacityWorkloadProfile, right capacityWorkloadProfile) capacityWorkloadProfile {
	if right.MaxBrowserVUsersPerGenerator > 0 && (left.MaxBrowserVUsersPerGenerator == 0 || right.MaxBrowserVUsersPerGenerator < left.MaxBrowserVUsersPerGenerator) {
		left.MaxBrowserVUsersPerGenerator = right.MaxBrowserVUsersPerGenerator
	}
	if right.BrowserMemoryMBPerVUser > left.BrowserMemoryMBPerVUser {
		left.BrowserMemoryMBPerVUser = right.BrowserMemoryMBPerVUser
	}
	if right.BrowserCPUCoresPerVUser > left.BrowserCPUCoresPerVUser {
		left.BrowserCPUCoresPerVUser = right.BrowserCPUCoresPerVUser
	}
	if right.BrowserNetworkMbpsPerVUser > left.BrowserNetworkMbpsPerVUser {
		left.BrowserNetworkMbpsPerVUser = right.BrowserNetworkMbpsPerVUser
	}
	return left
}

func estimateCapacityFootprint(complexityScore float64, workloadProfile capacityWorkloadProfile, totalVUsers int) (CapacityResourceEstimate, CapacityResourceEstimate) {
	total := CapacityResourceEstimate{}
	for _, density := range workloadProfile.ProtocolDensities {
		perProtocolVUser := protocolDensityFootprint(complexityScore, density)
		total.CPUCores += perProtocolVUser.CPUCores * float64(density.VUsers)
		total.MemoryMB += perProtocolVUser.MemoryMB * float64(density.VUsers)
		total.NetworkMbps += perProtocolVUser.NetworkMbps * float64(density.VUsers)
	}
	if workloadProfile.BrowserVUsers > 0 {
		browserPerVUser := estimatePerVUserFootprint(complexityScore, workloadProfile)
		total.CPUCores += browserPerVUser.CPUCores * float64(workloadProfile.BrowserVUsers)
		total.MemoryMB += browserPerVUser.MemoryMB * float64(workloadProfile.BrowserVUsers)
		total.NetworkMbps += browserPerVUser.NetworkMbps * float64(workloadProfile.BrowserVUsers)
	}
	if totalVUsers < 1 {
		totalVUsers = 1
	}
	if total.CPUCores == 0 && total.MemoryMB == 0 && total.NetworkMbps == 0 {
		perVUser := estimatePerVUserFootprint(complexityScore, workloadProfile)
		return perVUser, CapacityResourceEstimate{
			CPUCores:    perVUser.CPUCores * float64(totalVUsers),
			MemoryMB:    perVUser.MemoryMB * float64(totalVUsers),
			NetworkMbps: perVUser.NetworkMbps * float64(totalVUsers),
		}
	}
	return CapacityResourceEstimate{
		CPUCores:    total.CPUCores / float64(totalVUsers),
		MemoryMB:    total.MemoryMB / float64(totalVUsers),
		NetworkMbps: total.NetworkMbps / float64(totalVUsers),
	}, total
}

func protocolDensityFootprint(complexityScore float64, density ProtocolDensityEnvelope) CapacityResourceEstimate {
	if complexityScore < 0.7 {
		complexityScore = 0.7
	}
	return CapacityResourceEstimate{
		CPUCores:    math.Max(density.CPUCoresPerVUser, density.CPUCoresPerVUser*complexityScore),
		MemoryMB:    math.Max(density.MemoryMBPerVUser, density.MemoryMBPerVUser*complexityScore),
		NetworkMbps: math.Max(density.NetworkMbpsPerVUser, density.NetworkMbpsPerVUser*complexityScore),
	}
}

func estimatePerVUserFootprint(complexityScore float64, workloadProfile capacityWorkloadProfile) CapacityResourceEstimate {
	if complexityScore < 0.7 {
		complexityScore = 0.7
	}
	if workloadProfile.BrowserVUsers > 0 {
		return CapacityResourceEstimate{
			CPUCores:    math.Max(0.055, workloadProfile.BrowserCPUCoresPerVUser*complexityScore),
			MemoryMB:    math.Max(180, workloadProfile.BrowserMemoryMBPerVUser),
			NetworkMbps: math.Max(0.25, workloadProfile.BrowserNetworkMbpsPerVUser),
		}
	}
	return CapacityResourceEstimate{
		CPUCores:    math.Max(0.00018, 0.00025*complexityScore),
		MemoryMB:    math.Max(0.20, 0.35*complexityScore),
		NetworkMbps: math.Max(0.02, 0.08*complexityScore),
	}
}

func cloneProtocolDensityEnvelopes(in []ProtocolDensityEnvelope) []ProtocolDensityEnvelope {
	if len(in) == 0 {
		return nil
	}
	return append([]ProtocolDensityEnvelope(nil), in...)
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
