package engine

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func buildProtoConfig(script *dsl.Script) map[string]any {
	return buildProtoConfigWithSecretResolver(script, defaultSecretResolverForScript(script))
}

func buildProtoConfigForRunMode(script *dsl.Script, runMode string) map[string]any {
	return buildProtoConfigForRunModeWithSecretResolver(script, runMode, defaultSecretResolverForScript(script))
}

func buildProtoConfigForRunModeWithSecretResolver(script *dsl.Script, runMode string, resolver SecretResolver) map[string]any {
	cfg := buildProtoConfigWithSecretResolver(script, resolver)
	runMode = strings.TrimSpace(runMode)
	if runMode != "" {
		cfg["apexload_run_mode"] = runMode
	}
	return cfg
}

func buildGroupProtoConfig(group GroupConfig, runMode string) map[string]any {
	cfg := buildProtoConfigForRunModeWithSecretResolver(group.Script, runMode, group.SecretResolver)
	if group.sourceSamplers != nil {
		group.sourceSamplers.bindProtocolConfig(cfg, group.Script)
	}
	if scenarioID := strings.TrimSpace(group.ScenarioID); scenarioID != "" {
		cfg["scenario_id"] = scenarioID
	}
	if len(group.RunCorrelationOrigins) > 0 {
		cfg["apexload_run_correlation_id"] = group.RunID
		cfg["apexload_run_correlation_origins"] = append([]string(nil), group.RunCorrelationOrigins...)
		if len(group.CorrelationCarriers) > 0 {
			cfg["apexload_correlation_carriers"] = append([]string(nil), group.CorrelationCarriers...)
		}
		if name := strings.TrimSpace(group.CorrelationTestName); name != "" {
			cfg["apexload_correlation_test_name"] = name
		}
		if group.Script != nil {
			if name := strings.TrimSpace(group.Script.Name); name != "" {
				cfg["apexload_correlation_script_name"] = name
			}
		}
	}
	return cfg
}

// containsPlatformSecretReference reports whether an authored protocol-config
// field defers to the project secret store rather than carrying a literal the
// operator can already read in the script.
//
// It must recognize exactly what interpolateSecretReferences resolves, including
// the whitespace-tolerant "{{ secret.NAME }}" spelling. A stricter test here
// would silently declassify a genuine project secret.
func containsPlatformSecretReference(raw string) bool {
	if raw == "" {
		return false
	}
	for _, token := range templatePattern.FindAllString(raw, -1) {
		if strings.HasPrefix(strings.TrimSpace(token[2:len(token)-2]), "secret.") {
			return true
		}
	}
	return false
}

// runtimeProtocolAuthCompositeValues returns the composite credential spellings
// carried by SCRIPT-CONFIG fields — the surfaces whose resolved values already
// exist when an engine, replay, shakeout or group is constructed.
//
// It is one of the two enumeration sites listed in
// platform_managed_credential_composites.go; that file holds the surface list,
// the origin test, and the derivation. Everything this function does is decide
// which config fields are in play and where their resolved halves live.
func runtimeProtocolAuthCompositeValues(script *dsl.Script, resolver SecretResolver) []string {
	if script == nil {
		return nil
	}
	config := buildProtoConfigWithSecretResolver(script, resolver)
	return appendPlatformManagedCompositeValues(
		make([]string, 0, 8),
		scriptCredentialCompositeSurfaces(script, config)...,
	)
}

// runtimeActionCompositeValues returns the composite credential spellings
// carried by a single action — the surfaces whose resolved values do not exist
// until resolveAction has interpolated them. It is the second of the two
// enumeration sites; it shares the origin test and the derivation with the
// config-level one above.
//
// authored is the script's own action, which is the only place the origin of a
// credential can still be read; resolved is what resolveAction produced from it,
// which is the only place a registrable value exists.
func runtimeActionCompositeValues(authored *dsl.Action, resolved *protocol.Action) []string {
	return appendPlatformManagedCompositeValues(
		make([]string, 0, 8),
		actionCredentialCompositeSurfaces(authored, resolved)...,
	)
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
	isJavaVuser := strings.EqualFold(fmt.Sprint(base["protocol"]), "java_vuser")
	if (!hasPool || len(pool) == 0) && (!hasProfiles || len(profiles) == 0) && !isJavaVuser {
		return base
	}
	clone := make(map[string]any, len(base)+4)
	for k, v := range base {
		clone[k] = v
	}
	if isJavaVuser {
		clone["vuser_id"] = float64(vuserID)
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
		"base_url":            script.Config.BaseURL,
		"project_id":          script.ProjectID,
		"protocol":            script.Protocol,
		"scenario_id":         script.ID,
		"script_name":         script.Name,
		"ignore_think_time":   script.Config.ThinkTime.Ignore,
		"think_time_limit_ms": script.Config.ThinkTime.LimitMs,
	}
	if script.Metadata != nil && script.Metadata.RuntimePolicy != nil && len(script.Metadata.RuntimePolicy.AllowedTargetHosts) > 0 {
		cfg[protocol.TargetPolicyConfigKey] = append([]string(nil), script.Metadata.RuntimePolicy.AllowedTargetHosts...)
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
	if carrier := strings.TrimSpace(script.Config.Observability.TraceCarrier); carrier != "" {
		cfg[protocol.ConfigMessagingTraceCarrier] = strings.ToLower(carrier)
	}
	appendHTTPSecurityConfig(cfg, script)

	return resolveProtoConfigSecrets(cfg, resolver).(map[string]any)
}

func flattenContentChecks(checks []dsl.ContentCheckConfig) []map[string]any {
	flattened := make([]map[string]any, 0, len(checks))
	for _, check := range checks {
		item := map[string]any{
			"left_boundary": check.LeftBoundary, "right_boundary": check.RightBoundary,
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
