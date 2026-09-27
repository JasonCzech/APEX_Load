package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	observedOwner := s.runner.leadership.executionFenceToken()
	observationComplete := true
	settings := s.runner.GetSystemSettings()
	availability := resolveCapacityAvailabilityWithLiveAt(settings, s.runner.liveAvailableGeneratorCount(r.Context()), time.Now())
	tests := s.runner.listOperationalTestSnapshots()
	regionStats := s.generatorRegionStats(r.Context())
	scalingStats := scalingSignalStats(tests)
	podReplicaFloor := 0
	reservedResources := protocol.CapacityVector{}
	queuedResources := protocol.CapacityVector{}
	for _, test := range tests {
		if test == nil || (test.Status != "PENDING" && test.Status != "QUEUED" && test.Status != "RESERVED") {
			continue
		}
		if groups, err := podReservationGroups(test); err == nil {
			for _, group := range groups {
				queuedResources = queuedResources.Add(podGroupDemand(group))
			}
		}
	}
	if store, ok := s.runner.testStore.(podCapacityStore); ok {
		if claims, err := store.ListPodCapacityReservations(); err == nil {
			reserved := 0
			for _, claim := range claims {
				reserved += int(claim.Demand.VUsers)
				reservedResources = reservedResources.Add(claim.Demand)
			}
			scalingStats.ActiveVUsers = max(scalingStats.ActiveVUsers, reserved)
			if browser := int(math.Ceil(reservedResources.Protocols["browser"])); browser > 0 {
				scalingStats.ActiveProtocolVUsers["browser"] = max(scalingStats.ActiveProtocolVUsers["browser"], browser)
			}
			podReplicaFloor = reservedReplicaFloor(claims, settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators)
		} else {
			observationComplete = false
			entries, _ := s.runner.cachedGeneratorInventorySnapshot()
			unknown := make([]storage.PodCapacityReservation, 0, len(entries))
			for _, entry := range entries {
				unknown = append(unknown, storage.PodCapacityReservation{Target: entry.Target})
			}
			podReplicaFloor = max(settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators, reservedReplicaFloor(unknown, len(entries)))
		}
	}
	statusCounts := make(map[string]int)
	for _, test := range tests {
		if test == nil || strings.TrimSpace(test.Status) == "" {
			continue
		}
		statusCounts[test.Status]++
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var body strings.Builder
	s.writeFleetAutoscalingMetrics(&body, observedOwner, scalingStats, regionStats, podReplicaFloor, observationComplete)
	body.WriteString("# HELP apexload_up Whether the APEX Load controller is healthy.\n")
	body.WriteString("# TYPE apexload_up gauge\n")
	body.WriteString("apexload_up 1\n")
	body.WriteString("# HELP apexload_pod_reserved_replica_floor Minimum StatefulSet replicas protecting retained peak reservations.\n# TYPE apexload_pod_reserved_replica_floor gauge\n")
	fmt.Fprintf(&body, "apexload_pod_reserved_replica_floor %d\n", podReplicaFloor)
	body.WriteString("# HELP apexload_pod_capacity_demand Estimated peak resources held or awaiting admission.\n# TYPE apexload_pod_capacity_demand gauge\n")
	writePodCapacityDemand(&body, "reserved", reservedResources)
	writePodCapacityDemand(&body, "queued", queuedResources)
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
		body.WriteString("# HELP apexload_generator_run_supervision_expirations_total Generator runs fenced after controller supervision expired.\n")
		body.WriteString("# TYPE apexload_generator_run_supervision_expirations_total counter\n")
		body.WriteString("# HELP apexload_generator_run_supervision_escalations_total Generator supervision expirations that required process self-termination.\n")
		body.WriteString("# TYPE apexload_generator_run_supervision_escalations_total counter\n")
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
			sort.Slice(stats.RunSupervisionCounters, func(i, j int) bool {
				return stats.RunSupervisionCounters[i].Target < stats.RunSupervisionCounters[j].Target
			})
			for _, counters := range stats.RunSupervisionCounters {
				fmt.Fprintf(&body, "apexload_generator_run_supervision_expirations_total{region=%q,generator_id=%q,target=%q} %d\n", region, counters.GeneratorID, counters.Target, counters.Expirations)
				fmt.Fprintf(&body, "apexload_generator_run_supervision_escalations_total{region=%q,generator_id=%q,target=%q} %d\n", region, counters.GeneratorID, counters.Target, counters.Escalations)
			}
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
	body.WriteString("# HELP apexload_notification_delivery_failures_total Notification webhook deliveries that failed after retry.\n")
	body.WriteString("# TYPE apexload_notification_delivery_failures_total counter\n")
	fmt.Fprintf(&body, "apexload_notification_delivery_failures_total %d\n", notificationDeliveryFailures())
	liveSnapshot := s.runner.liveMetricsSnapshot()
	writeTelemetryPipelineMetrics(&body, liveSnapshot)
	writeMessagingMetrics(&body, liveSnapshot)
	_, _ = w.Write([]byte(body.String()))
}

func writePodCapacityDemand(body *strings.Builder, state string, vector protocol.CapacityVector) {
	dimensions := vector.Dimensions()
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(body, "apexload_pod_capacity_demand{state=%q,resource=%q} %g\n", state, key, dimensions[key])
	}
}

// writeMessagingMetrics exposes per-test, per-protocol queue/stream telemetry
// (JMS, Kafka, Event Hubs) as Prometheus gauges. Messaging server-telemetry is
// dependency-scoped, so it is read from each running test's dependency_health,
// labeled by test_id, protocol, and target (event hub / topic / queue).
// testLiveMetrics pairs a test id with its LiveMetrics pointer captured under the
// runner lock. The publish loop swaps test.LiveMetrics under tr.mu (metrics.go),
// so a concurrent /metrics scrape must read the pointer under the lock; the
// pointed-to LiveMetrics is rebuilt fresh each cycle and never mutated in place,
// so once captured it can be read without further locking.
type testLiveMetrics struct {
	testID  string
	status  string
	metrics *LiveMetrics
}

func (tr *TestRunner) liveMetricsSnapshot() []testLiveMetrics {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	out := make([]testLiveMetrics, 0, len(tr.tests))
	for _, t := range tr.tests {
		if t == nil {
			continue
		}
		out = append(out, testLiveMetrics{testID: t.ID, status: t.Status, metrics: t.LiveMetrics})
	}
	return out
}

func writeTelemetryPipelineMetrics(body *strings.Builder, snapshot []testLiveMetrics) {
	type row struct {
		testID    string
		telemetry *TelemetryHealth
	}
	rows := make([]row, 0, len(snapshot))
	for _, snap := range snapshot {
		if snap.metrics == nil || snap.metrics.Telemetry == nil || prometheusTerminalTestStatus(snap.status) {
			continue
		}
		rows = append(rows, row{testID: snap.testID, telemetry: snap.metrics.Telemetry})
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].testID < rows[j].testID })
	writeGauge := func(name, help string, pick func(*TelemetryHealth) float64) {
		fmt.Fprintf(body, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		for _, r := range rows {
			fmt.Fprintf(body, "%s{test_id=%q} %g\n", name, r.testID, pick(r.telemetry))
		}
	}
	writeCounter := func(name, help string, pick func(*TelemetryHealth) float64) {
		fmt.Fprintf(body, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
		for _, r := range rows {
			fmt.Fprintf(body, "%s{test_id=%q} %g\n", name, r.testID, pick(r.telemetry))
		}
	}
	writeCounter("apexload_telemetry_dropped_results_total", "Collector metric samples dropped by test.", func(t *TelemetryHealth) float64 { return float64(t.DroppedResults) })
	writeCounter("apexload_telemetry_shedded_arrivals_total", "Open-workload arrivals shed by test.", func(t *TelemetryHealth) float64 { return float64(t.SheddedArrivals) })
	writeCounter("apexload_telemetry_dropped_subscriber_batches_total", "Generator metrics batches dropped because stream subscribers were full.", func(t *TelemetryHealth) float64 { return float64(t.DroppedSubscriberBatches) })
	writeCounter("apexload_telemetry_missing_metric_windows_total", "Live generator metric windows inferred missing from batch-sequence gaps.", func(t *TelemetryHealth) float64 { return float64(t.MissingMetricWindows) })
	writeCounter("apexload_websocket_dropped_messages_total", "WebSocket messages dropped by test.", func(t *TelemetryHealth) float64 { return float64(t.WebsocketDroppedMessages) })
	writeCounter("apexload_websocket_dropped_events_total", "WebSocket broadcast events dropped by test.", func(t *TelemetryHealth) float64 { return float64(t.WebsocketDroppedEvents) })
	writeGauge("apexload_telemetry_sample_age_ms", "Age of the latest telemetry sample in milliseconds.", func(t *TelemetryHealth) float64 { return t.SampleAgeMs })
	writeGauge("apexload_telemetry_generator_processing_lag_ms", "Generator-side telemetry processing lag in milliseconds.", func(t *TelemetryHealth) float64 { return t.GeneratorProcessingLagMs })
	writeGauge("apexload_telemetry_transport_lag_ms", "Generator to controller telemetry transport lag in milliseconds.", func(t *TelemetryHealth) float64 { return t.TransportLagMs })
	writeGauge("apexload_telemetry_controller_fanout_lag_ms", "Controller to dashboard fan-out lag in milliseconds.", func(t *TelemetryHealth) float64 { return t.ControllerFanoutLagMs })
}

func writeMessagingMetrics(body *strings.Builder, snapshot []testLiveMetrics) {
	type row struct {
		testID   string
		protocol string
		target   string
		st       *ServerTelemetrySummary
	}
	var rows []row
	for _, snap := range snapshot {
		if snap.metrics == nil || prometheusTerminalTestStatus(snap.status) {
			continue
		}
		for _, dep := range snap.metrics.DependencyHealth {
			if dep == nil || dep.ServerTelemetry == nil {
				continue
			}
			protocol, ok := messagingProtocolLabel(dep.DependencyType)
			if !ok {
				continue
			}
			rows = append(rows, row{testID: snap.testID, protocol: protocol, target: dep.DependencyTarget, st: dep.ServerTelemetry})
		}
	}
	if len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].testID != rows[j].testID {
			return rows[i].testID < rows[j].testID
		}
		if rows[i].protocol != rows[j].protocol {
			return rows[i].protocol < rows[j].protocol
		}
		return rows[i].target < rows[j].target
	})

	writeGauge := func(name, help string, pick func(*ServerTelemetrySummary) float64) {
		fmt.Fprintf(body, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
		for _, r := range rows {
			fmt.Fprintf(body, "%s{test_id=%q,protocol=%q,target=%q} %g\n", name, r.testID, r.protocol, r.target, pick(r.st))
		}
	}
	writeGauge("apexload_messaging_consumer_lag_ms", "Average consumer lag in milliseconds by test, protocol, and target.", func(st *ServerTelemetrySummary) float64 { return st.AvgConsumerLagMs })
	writeGauge("apexload_messaging_consumer_offset_lag", "Average consumer offset/sequence backlog by test, protocol, and target.", func(st *ServerTelemetrySummary) float64 { return st.AvgConsumerOffsetLag })
	writeGauge("apexload_messaging_e2e_latency_ms", "Average producer->observed-consume end-to-end latency in milliseconds (Event Hubs probe).", func(st *ServerTelemetrySummary) float64 { return st.AvgEndToEndLatencyMs })
	writeGauge("apexload_messaging_partition_skew_pct", "Per-partition produce skew (coefficient of variation, percent).", func(st *ServerTelemetrySummary) float64 { return st.PartitionSkewPct })
	writeGauge("apexload_messaging_throttled_total", "Throttle / ServerBusy / quota responses (run total) by test, protocol, and target.", func(st *ServerTelemetrySummary) float64 {
		return float64(st.TotalThrottledRequests + st.TotalServerBusyCount + st.TotalQuotaExceededCount)
	})
	writeGauge("apexload_messaging_dead_lettered_total", "Dead-lettered messages (run total) by test, protocol, and target.", func(st *ServerTelemetrySummary) float64 { return float64(st.DeadLetteredCount) })
	writeGauge("apexload_messaging_messages_sent_per_sec", "Average messages sent per second by test, protocol, and target.", func(st *ServerTelemetrySummary) float64 { return st.AvgMessagesSentPerSec })
	writeGauge("apexload_messaging_messages_received_per_sec", "Average messages received per second by test, protocol, and target.", func(st *ServerTelemetrySummary) float64 { return st.AvgMessagesReceivedPerSec })
}

func prometheusTerminalTestStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

// messagingProtocolLabel maps a dependency type to a coarse messaging protocol
// label, returning false for non-messaging dependencies.
func messagingProtocolLabel(dependencyType string) (string, bool) {
	switch {
	case strings.HasPrefix(dependencyType, "jms_"):
		return "jms", true
	case strings.HasPrefix(dependencyType, "kafka"):
		return "kafka", true
	case strings.HasPrefix(dependencyType, "eventhubs"):
		return "eventhubs", true
	default:
		return "", false
	}
}

func boolMetric(value bool) int {
	if value {
		return 1
	}
	return 0
}

type generatorRegionStats struct {
	Count                  int
	AvailableCapacity      int64
	ActiveVUsers           int64
	CapacityStates         map[string]int
	DrainStates            map[string]int
	CompatibilityStates    map[string]int
	RunSupervisionCounters []generatorRunSupervisionCounters
}

type generatorRunSupervisionCounters struct {
	Target      string
	GeneratorID string
	Expirations uint64
	Escalations uint64
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
			groups := test.Groups
			if peak, err := podReservationGroups(test); err == nil {
				groups = peak
			}
			peakVUsers := 0
			for _, group := range groups {
				peakVUsers += group.VUsers
			}
			stats.QueuedVUsers += max(test.TotalVUsers, peakVUsers)
			addProtocolVUsers(stats.QueuedProtocolVUsers, groups)
		case "INITIALIZING", "STARTING", "SHAKEOUT", "SHAKEOUT_REVIEW", "RUNNING", "PAUSED", "STOPPING":
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
		if strings.EqualFold(strings.TrimSpace(generator.Status), "connected") && stringSliceContainsEqualFold(generator.CompatibleFeatures, generatorrpc.FeatureRunSupervisionV1) {
			current.RunSupervisionCounters = append(current.RunSupervisionCounters, generatorRunSupervisionCounters{
				Target: generator.Target, GeneratorID: generator.GeneratorID,
				Expirations: generator.RunSupervisionExpirationsTotal,
				Escalations: generator.RunSupervisionEscalationsTotal,
			})
		}
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
