package engine

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func buildProtoConfig(script *dsl.Script) map[string]any {
	return buildProtoConfigWithSecretResolver(script, newDefaultSecretResolver())
}

// normalizeSourceIPs trims, drops blanks, and de-duplicates a source-IP pool.
func normalizeSourceIPs(ips []string) []string {
	if len(ips) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ips))
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		trimmed := strings.TrimSpace(ip)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// vuserProtoConfig returns the proto config a specific VUser should initialize
// with. Per-VUser overrides are layered on the shared base: a source-IP pool
// binds each VUser to a stable client IP, and a network-profile mix assigns each
// VUser a network condition by weighted share. Without either it returns the
// shared base config unchanged.
func vuserProtoConfig(base map[string]any, vuserID int) map[string]any {
	pool, hasPool := base["web_source_ips"].([]string)
	profiles, hasProfiles := base["web_network_profiles"].([]dsl.NetworkProfileWeight)
	if (!hasPool || len(pool) == 0) && (!hasProfiles || len(profiles) == 0) {
		return base
	}
	clone := make(map[string]any, len(base)+4)
	for k, v := range base {
		clone[k] = v
	}
	if hasPool && len(pool) > 0 {
		idx := vuserID % len(pool)
		if idx < 0 {
			idx += len(pool)
		}
		clone["web_source_ip"] = pool[idx]
	}
	if hasProfiles && len(profiles) > 0 {
		if profile, ok := assignNetworkProfile(profiles, vuserID); ok {
			injectNetworkSimulation(clone, profile)
			clone["web_network_profile_label"] = profile.Name
		}
	}
	return clone
}

// normalizeNetworkProfiles drops profiles with a blank name or non-positive
// weight and trims names.
func normalizeNetworkProfiles(profiles []dsl.NetworkProfileWeight) []dsl.NetworkProfileWeight {
	out := make([]dsl.NetworkProfileWeight, 0, len(profiles))
	for _, profile := range profiles {
		name := strings.TrimSpace(profile.Name)
		if name == "" || profile.Weight <= 0 {
			continue
		}
		profile.Name = name
		out = append(out, profile)
	}
	return out
}

// assignNetworkProfile deterministically maps a VUser id to a profile in
// proportion to the profiles' weights (a 30/70 split puts 30% of ids on the
// first profile), so the population mix is stable and evenly distributed.
func assignNetworkProfile(profiles []dsl.NetworkProfileWeight, vuserID int) (dsl.NetworkProfileWeight, bool) {
	normalized := normalizeNetworkProfiles(profiles)
	if len(normalized) == 0 {
		return dsl.NetworkProfileWeight{}, false
	}
	total := 0
	for _, profile := range normalized {
		total += profile.Weight
	}
	if total <= 0 {
		return dsl.NetworkProfileWeight{}, false
	}
	pos := vuserID % total
	if pos < 0 {
		pos += total
	}
	for _, profile := range normalized {
		if pos < profile.Weight {
			return profile, true
		}
		pos -= profile.Weight
	}
	return normalized[len(normalized)-1], true
}

// injectNetworkSimulation writes a profile's shaping values into the proto
// config using the same keys buildProtoConfig emits for NetworkSimulation.
func injectNetworkSimulation(cfg map[string]any, profile dsl.NetworkProfileWeight) {
	sim := profile.NetworkSimulationConfig
	if sim.Mode != "" {
		cfg["web_network_simulation_mode"] = sim.Mode
	}
	if sim.Preset != "" {
		cfg["web_network_simulation_preset"] = sim.Preset
	}
	if sim.BandwidthKbps > 0 {
		cfg["web_network_simulation_bandwidth_kbps"] = float64(sim.BandwidthKbps)
	}
	if sim.LatencyMs > 0 {
		cfg["web_network_simulation_latency_ms"] = float64(sim.LatencyMs)
	}
	if sim.JitterMs > 0 {
		cfg["web_network_simulation_jitter_ms"] = float64(sim.JitterMs)
	}
	if sim.PacketLossPct > 0 {
		cfg["web_network_simulation_packet_loss_pct"] = sim.PacketLossPct
	}
}

func buildProtoConfigWithSecretResolver(script *dsl.Script, resolver SecretResolver) map[string]any {
	cfg := map[string]any{
		"base_url": script.Config.BaseURL,
	}

	appendHTTPConfig(cfg, script)
	appendWebSocketConfig(cfg, script)
	appendGRPCConfig(cfg, script)
	appendJavaHTTPConfig(cfg, script)
	appendJavaVuserConfig(cfg, script)
	appendSOAPConfig(cfg, script)
	appendDatabaseConfig(cfg, script)
	appendNetworkConfig(cfg, script)
	appendBrowserConfig(cfg, script)
	appendJMSConfig(cfg, script)
	appendKafkaConfig(cfg, script)
	appendEventHubsConfig(cfg, script)
	appendHTTPSecurityConfig(cfg, script)

	return resolveProtoConfigSecrets(cfg, resolver).(map[string]any)
}

func flattenContentChecks(checks []dsl.ContentCheckConfig) []map[string]any {
	flattened := make([]map[string]any, 0, len(checks))
	for _, check := range checks {
		item := map[string]any{
			"text":  check.Text,
			"scope": check.Scope,
		}
		if check.FailIfFound != nil {
			item["fail_if_found"] = *check.FailIfFound
		}
		if check.CaseInsensitive {
			item["case_insensitive"] = true
		}
		flattened = append(flattened, item)
	}
	return flattened
}

func flattenDFEChain(chain []dsl.DFETransform) []map[string]any {
	flattened := make([]map[string]any, 0, len(chain))
	for _, transform := range chain {
		item := map[string]any{
			"type": transform.Type,
		}
		if len(transform.Options) > 0 {
			item["options"] = transform.Options
		}
		flattened = append(flattened, item)
	}
	return flattened
}

func recordedResourceURLs(script *dsl.Script) ([]string, bool) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil, false
	}
	preserved := script.Metadata.Import.Preserved
	rawAvailable, ok := preserved["recorded_resource_urls_available"]
	if !ok {
		return nil, false
	}
	available, ok := rawAvailable.(bool)
	if !ok || !available {
		return nil, false
	}
	rawURLs, ok := preserved["recorded_resource_urls"]
	if !ok {
		return []string{}, true
	}
	switch urls := rawURLs.(type) {
	case []string:
		return append([]string(nil), urls...), true
	case []any:
		result := make([]string, 0, len(urls))
		for _, value := range urls {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				continue
			}
			result = append(result, text)
		}
		return result, true
	default:
		return []string{}, true
	}
}
