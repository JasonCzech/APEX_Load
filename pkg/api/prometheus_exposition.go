package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	availability := resolveCapacityAvailability(settings)
	tests := s.runner.ListTests()
	regionStats := s.generatorRegionStats(r.Context())
	scalingStats := scalingSignalStats(tests)
	statusCounts := make(map[string]int)
	for _, test := range tests {
		if test == nil || strings.TrimSpace(test.Status) == "" {
			continue
		}
		statusCounts[test.Status]++
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var body strings.Builder
	body.WriteString("# HELP apexload_up Whether the APEX Load controller is healthy.\n")
	body.WriteString("# TYPE apexload_up gauge\n")
	body.WriteString("apexload_up 1\n")
	body.WriteString("# HELP apexload_websocket_clients Number of connected websocket clients.\n")
	body.WriteString("# TYPE apexload_websocket_clients gauge\n")
	fmt.Fprintf(&body, "apexload_websocket_clients %d\n", s.hub.ClientCount())
	body.WriteString("# HELP apexload_test_count Number of tracked tests in memory.\n")
	body.WriteString("# TYPE apexload_test_count gauge\n")
	fmt.Fprintf(&body, "apexload_test_count %d\n", len(tests))
	body.WriteString("# HELP apexload_tests_by_status Number of tracked tests by status.\n")
	body.WriteString("# TYPE apexload_tests_by_status gauge\n")
	for status, count := range statusCounts {
		fmt.Fprintf(&body, "apexload_tests_by_status{status=%q} %d\n", status, count)
	}
	body.WriteString("# HELP apexload_scaling_vusers VUsers by autoscaling demand state.\n")
	body.WriteString("# TYPE apexload_scaling_vusers gauge\n")
	fmt.Fprintf(&body, "apexload_scaling_vusers{state=%q} %d\n", "queued", scalingStats.QueuedVUsers)
	fmt.Fprintf(&body, "apexload_scaling_vusers{state=%q} %d\n", "active", scalingStats.ActiveVUsers)
	body.WriteString("# HELP apexload_scaling_tests Tests by autoscaling demand state.\n")
	body.WriteString("# TYPE apexload_scaling_tests gauge\n")
	fmt.Fprintf(&body, "apexload_scaling_tests{state=%q} %d\n", "queued", scalingStats.QueuedTests)
	fmt.Fprintf(&body, "apexload_scaling_tests{state=%q} %d\n", "active", scalingStats.ActiveTests)
	body.WriteString("# HELP apexload_scaling_protocol_vusers VUsers by autoscaling demand state and protocol.\n")
	body.WriteString("# TYPE apexload_scaling_protocol_vusers gauge\n")
	writeScalingProtocolVUsers(&body, "queued", scalingStats.QueuedProtocolVUsers)
	writeScalingProtocolVUsers(&body, "active", scalingStats.ActiveProtocolVUsers)
	body.WriteString("# HELP apexload_capacity_available_generators Available generators from capacity planning.\n")
	body.WriteString("# TYPE apexload_capacity_available_generators gauge\n")
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "configured", availability.ConfiguredAvailableGenerators)
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "effective", availability.EffectiveAvailableGenerators)
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "reserved", availability.ReservedGenerators)
	fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "shared", availability.SharedAvailableGenerators)
	if availability.LiveAvailableGenerators != nil {
		fmt.Fprintf(&body, "apexload_capacity_available_generators{scope=%q} %d\n", "live", *availability.LiveAvailableGenerators)
	}
	if len(regionStats) > 0 {
		body.WriteString("# HELP apexload_generator_region_count Number of configured generator inventory entries by region.\n")
		body.WriteString("# TYPE apexload_generator_region_count gauge\n")
		body.WriteString("# HELP apexload_generator_region_available_capacity Available VUser capacity reported by generators by region.\n")
		body.WriteString("# TYPE apexload_generator_region_available_capacity gauge\n")
		body.WriteString("# HELP apexload_generator_region_active_vusers Active VUsers reported by generators by region.\n")
		body.WriteString("# TYPE apexload_generator_region_active_vusers gauge\n")
		body.WriteString("# HELP apexload_generator_capacity_state_count Number of generators by region and capacity state.\n")
		body.WriteString("# TYPE apexload_generator_capacity_state_count gauge\n")
		body.WriteString("# HELP apexload_generator_drain_state_count Number of generators by region and drain state.\n")
		body.WriteString("# TYPE apexload_generator_drain_state_count gauge\n")
		body.WriteString("# HELP apexload_generator_compatibility_state_count Number of generators by region and compatibility state.\n")
		body.WriteString("# TYPE apexload_generator_compatibility_state_count gauge\n")
		regions := make([]string, 0, len(regionStats))
		for region := range regionStats {
			regions = append(regions, region)
		}
		sort.Strings(regions)
		for _, region := range regions {
			stats := regionStats[region]
			fmt.Fprintf(&body, "apexload_generator_region_count{region=%q} %d\n", region, stats.Count)
			fmt.Fprintf(&body, "apexload_generator_region_available_capacity{region=%q} %d\n", region, stats.AvailableCapacity)
			fmt.Fprintf(&body, "apexload_generator_region_active_vusers{region=%q} %d\n", region, stats.ActiveVUsers)
			writeLabeledCounts(&body, "apexload_generator_capacity_state_count", "region", region, "state", stats.CapacityStates)
			writeLabeledCounts(&body, "apexload_generator_drain_state_count", "region", region, "state", stats.DrainStates)
			writeLabeledCounts(&body, "apexload_generator_compatibility_state_count", "region", region, "state", stats.CompatibilityStates)
		}
	}
	body.WriteString("# HELP apexload_max_concurrent_tests Configured maximum concurrent tests.\n")
	body.WriteString("# TYPE apexload_max_concurrent_tests gauge\n")
	fmt.Fprintf(&body, "apexload_max_concurrent_tests %d\n", settings.General.MaxConcurrentTests)
	body.WriteString("# HELP apexload_metrics_interval_seconds Configured metrics publish interval in seconds.\n")
	body.WriteString("# TYPE apexload_metrics_interval_seconds gauge\n")
	fmt.Fprintf(&body, "apexload_metrics_interval_seconds %d\n", settings.General.MetricsIntervalSeconds)
	githubSettings := s.currentGitHubSettings()
	body.WriteString("# HELP apexload_github_integration_enabled Whether GitHub Enterprise integration is enabled.\n")
	body.WriteString("# TYPE apexload_github_integration_enabled gauge\n")
	fmt.Fprintf(&body, "apexload_github_integration_enabled %d\n", boolMetric(githubSettings.Enabled))
	body.WriteString("# HELP apexload_github_auth_configured GitHub auth methods configured by type.\n")
	body.WriteString("# TYPE apexload_github_auth_configured gauge\n")
	fmt.Fprintf(&body, "apexload_github_auth_configured{mode=%q} %d\n", "oauth", boolMetric(githubSettings.oauthConfigured()))
	fmt.Fprintf(&body, "apexload_github_auth_configured{mode=%q} %d\n", "app", boolMetric(githubSettings.appConfigured()))
	fmt.Fprintf(&body, "apexload_github_auth_configured{mode=%q} %d\n", "pat", boolMetric(githubSettings.patConfigured()))
	if store, ok := s.githubEnterpriseStore(); ok {
		if jobs, err := store.ListGitHubSyncJobs("", 100); err == nil {
			counts := map[string]int{}
			for _, job := range jobs {
				counts[job.Status]++
			}
			body.WriteString("# HELP apexload_github_sync_jobs Recent GitHub sync jobs by status.\n")
			body.WriteString("# TYPE apexload_github_sync_jobs gauge\n")
			for status, count := range counts {
				fmt.Fprintf(&body, "apexload_github_sync_jobs{status=%q} %d\n", status, count)
			}
		}
	}
	_, _ = w.Write([]byte(body.String()))
}

func boolMetric(value bool) int {
	if value {
		return 1
	}
	return 0
}

type generatorRegionStats struct {
	Count               int
	AvailableCapacity   int64
	ActiveVUsers        int64
	CapacityStates      map[string]int
	DrainStates         map[string]int
	CompatibilityStates map[string]int
}

type scalingSignals struct {
	QueuedTests          int
	ActiveTests          int
	QueuedVUsers         int
	ActiveVUsers         int
	QueuedProtocolVUsers map[string]int
	ActiveProtocolVUsers map[string]int
}

func scalingSignalStats(tests []*ManagedTest) scalingSignals {
	stats := scalingSignals{
		QueuedProtocolVUsers: make(map[string]int),
		ActiveProtocolVUsers: make(map[string]int),
	}
	for _, test := range tests {
		if test == nil {
			continue
		}
		switch test.Status {
		case "QUEUED", "PENDING", "RESERVED":
			stats.QueuedTests++
			stats.QueuedVUsers += test.TotalVUsers
			addProtocolVUsers(stats.QueuedProtocolVUsers, test.Groups)
		case "INITIALIZING", "RUNNING", "PAUSED":
			stats.ActiveTests++
			stats.ActiveVUsers += test.TotalVUsers
			addProtocolVUsers(stats.ActiveProtocolVUsers, test.Groups)
		}
	}
	return stats
}

func addProtocolVUsers(out map[string]int, groups []TestGroup) {
	for _, group := range groups {
		out[usageKey(group.Protocol, "unknown")] += group.VUsers
	}
}

func writeScalingProtocolVUsers(body *strings.Builder, state string, protocolVUsers map[string]int) {
	protocols := make([]string, 0, len(protocolVUsers))
	for protocol := range protocolVUsers {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	for _, protocol := range protocols {
		fmt.Fprintf(body, "apexload_scaling_protocol_vusers{state=%q,protocol=%q} %d\n", state, protocol, protocolVUsers[protocol])
	}
}

func (s *Server) generatorRegionStats(ctx context.Context) map[string]generatorRegionStats {
	inventory := s.systemInventory(ctx)
	if len(inventory.Generators) == 0 {
		return nil
	}
	stats := make(map[string]generatorRegionStats)
	for _, generator := range inventory.Generators {
		region := strings.TrimSpace(generator.Region)
		if region == "" {
			region = "unassigned"
		}
		current := stats[region]
		if current.CapacityStates == nil {
			current.CapacityStates = make(map[string]int)
		}
		if current.DrainStates == nil {
			current.DrainStates = make(map[string]int)
		}
		if current.CompatibilityStates == nil {
			current.CompatibilityStates = make(map[string]int)
		}
		current.Count++
		current.AvailableCapacity += int64(generator.AvailableCapacity)
		current.ActiveVUsers += int64(generator.ActiveVUsers)
		current.CapacityStates[usageKey(generator.CapacityState, "unknown")]++
		current.DrainStates[usageKey(generator.DrainState, "active")]++
		current.CompatibilityStates[usageKey(generator.CompatibilityState, "compatible")]++
		stats[region] = current
	}
	return stats
}

func writeLabeledCounts(body *strings.Builder, metricName string, labelA string, valueA string, labelB string, counts map[string]int) {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(body, "%s{%s=%q,%s=%q} %d\n", metricName, labelA, valueA, labelB, key, counts[key])
	}
}
