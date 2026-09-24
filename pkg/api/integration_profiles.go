package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/telemetry"
)

type APMIntegrationProfilesResponse struct {
	GeneratedAt        time.Time               `json:"generated_at"`
	ConfiguredCount    int                     `json:"configured_count"`
	Profiles           []APMIntegrationProfile `json:"profiles"`
	GenericCorrelation APMCorrelationContract  `json:"generic_correlation"`
}

type APMIntegrationProfile struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Status            string   `json:"status"`
	Capabilities      []string `json:"capabilities"`
	SupportedSignals  []string `json:"supported_signals"`
	CorrelationFields []string `json:"correlation_fields"`
	RequiredSettings  []string `json:"required_settings,omitempty"`
	DryRunEndpoint    string   `json:"dry_run_endpoint,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
	SetupActions      []string `json:"setup_actions,omitempty"`
}

type APMCorrelationContract struct {
	Status            string   `json:"status"`
	Propagation       string   `json:"propagation"`
	CorrelationFields []string `json:"correlation_fields"`
	Evidence          []string `json:"evidence,omitempty"`
}

type InfrastructureMonitorPacksResponse struct {
	GeneratedAt time.Time                   `json:"generated_at"`
	Packs       []InfrastructureMonitorPack `json:"packs"`
	Evidence    []string                    `json:"evidence,omitempty"`
}

type InfrastructureMonitorPack struct {
	ID           string                       `json:"id"`
	Name         string                       `json:"name"`
	Status       string                       `json:"status"`
	Source       string                       `json:"source"`
	ResourceType string                       `json:"resource_type"`
	Signals      []string                     `json:"signals"`
	Profiles     []MonitoringReadinessProfile `json:"profiles,omitempty"`
	Evidence     []string                     `json:"evidence,omitempty"`
	SetupActions []string                     `json:"setup_actions,omitempty"`
}

type ReportProfilesResponse struct {
	GeneratedAt     time.Time                 `json:"generated_at"`
	DefaultProfile  string                    `json:"default_profile"`
	Profiles        []RunArchiveReportProfile `json:"profiles"`
	DownloadFormats []string                  `json:"download_formats"`
}

func (s *Server) handleListAPMIntegrationProfiles(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	profiles := buildAPMIntegrationProfiles(settings, s.tracing)
	configured := 0
	for _, profile := range profiles {
		if profile.Status == "configured" {
			configured++
		}
	}
	writeJSON(w, http.StatusOK, APMIntegrationProfilesResponse{
		GeneratedAt:     time.Now().UTC(),
		ConfiguredCount: configured,
		Profiles:        profiles,
		GenericCorrelation: APMCorrelationContract{
			Status:            "supported",
			Propagation:       "W3C trace-context",
			CorrelationFields: []string{"traceparent", "trace_id", "span_id", "correlation_id", "dependency_type", "dependency_target"},
			Evidence:          []string{"pkg/api/archive.go", "docs/COMPETITIVE_COMPARISON.md#gap-analysis"},
		},
	})
}

func buildAPMIntegrationProfiles(settings SystemSettings, tracing telemetry.Status) []APMIntegrationProfile {
	dynatraceConfigured := false
	for _, webhook := range settings.Notifications.Webhooks {
		if webhook.Enabled && strings.EqualFold(webhook.Format, notificationFormatDynatrace) {
			dynatraceConfigured = true
			break
		}
	}
	statusFor := func(id string) string {
		if id == "opentelemetry" {
			if tracing.CorrelationOnly {
				return "correlation_only"
			}
			if tracing.Enabled && tracing.Ready {
				return "configured"
			}
			if tracing.Enabled {
				return "unavailable"
			}
			return "available"
		}
		if id == "dynatrace" && dynatraceConfigured {
			return "configured"
		}
		return "available"
	}
	baseCapabilities := []string{"trace_correlation", "dependency_tags", "run_lifecycle_events", "archived_external_metrics"}
	baseSignals := []string{"transactions", "errors", "resource_samples", "interval_summaries", "server_telemetry"}
	baseFields := []string{"traceparent", "trace_id", "span_id", "correlation_id"}
	otelCapabilities := []string{"native_otlp_export", "w3c_trace_context", "http_server_spans", "test_start_spans", "local_execution_spans", "distributed_launch_spans", "action_spans", "grpc_client_server_spans"}
	otelSignals := []string{"traces"}
	otelRequiredSettings := []string{"APEXLOAD_OTEL_ENABLED=true", "OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"}
	otelSetupActions := []string{"Configure an OTLP collector endpoint and explicitly enable tracing on controller and generator processes.", "Choose OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf or grpc and an OTEL_TRACES_SAMPLER policy."}
	if tracing.CorrelationOnly {
		otelCapabilities = []string{"w3c_trace_context"}
		otelSignals = []string{"correlation_headers"}
		otelRequiredSettings = nil
		otelSetupActions = []string{"Use the standalone controller and generator binaries for native OTLP span export; the desktop surface retains correlation headers only."}
	}
	profiles := []APMIntegrationProfile{
		{
			ID:                "opentelemetry",
			Name:              "OpenTelemetry",
			Status:            statusFor("opentelemetry"),
			Capabilities:      otelCapabilities,
			SupportedSignals:  otelSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  otelRequiredSettings,
			Evidence:          []string{"pkg/telemetry/tracing.go", "pkg/telemetry/http.go", "docs/OBSERVABILITY_INTEGRATIONS.md"},
			SetupActions:      otelSetupActions,
		},
		{
			ID:                "dynatrace",
			Name:              "Dynatrace",
			Status:            statusFor("dynatrace"),
			Capabilities:      append([]string{"problem_event_webhook", "entity_selector", "dry_run_payload_export"}, baseCapabilities...),
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"notifications.webhooks[].format=dynatrace", "notifications.webhooks[].api_token", "notifications.webhooks[].entity_selector"},
			DryRunEndpoint:    "/api/v1/integrations/dynatrace/dry-run",
			Evidence:          []string{"pkg/api/notifications.go", "docs/COMPETITIVE_COMPARISON.md#gap-analysis"},
			SetupActions:      []string{"Preview Dynatrace Events API v2 payloads with the dry-run endpoint before enabling delivery.", "Enable a Dynatrace notification webhook in system settings.", "Pass W3C trace-context headers from scripts to the service under test."},
		},
		{
			ID:                "elastic",
			Name:              "Elastic (ELK)",
			Status:            "available",
			Capabilities:      []string{"controller_side_log_polling", "cumulative_log_and_error_counts", "numeric_aggregations", "bounded_redacted_samples", "archive_evidence"},
			SupportedSignals:  []string{"logs", "errors", "numeric_log_fields"},
			CorrelationFields: []string{"test_id", "trace_id", "timestamp"},
			RequiredSettings:  []string{"project ELK connection", "Elasticsearch read privileges", "project-secret authentication references", "per-test opt-in"},
			Evidence:          []string{"pkg/api/elk_monitoring.go", "docs/OBSERVABILITY_INTEGRATIONS.md"},
			SetupActions:      []string{"Create an intentionally incomplete project connection in Monitoring → Telemetry.", "Add endpoint, field mappings, and project-secret references when connectivity is available.", "Test the connection, enable it, and opt individual tests into capture."},
		},
		{
			ID:                "datadog",
			Name:              "Datadog",
			Status:            statusFor("datadog"),
			Capabilities:      baseCapabilities,
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"external metrics or OpenTelemetry collector route"},
			Evidence:          []string{"pkg/api/external_metrics.go", "pkg/api/archive.go"},
			SetupActions:      []string{"Route APEX Load OpenTelemetry signals through a Datadog-enabled collector.", "Map Datadog trace IDs into archived transaction trace fields."},
		},
		{
			ID:                "newrelic",
			Name:              "New Relic",
			Status:            statusFor("newrelic"),
			Capabilities:      baseCapabilities,
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"external metrics or OpenTelemetry collector route"},
			Evidence:          []string{"pkg/api/external_metrics.go", "pkg/api/archive.go"},
			SetupActions:      []string{"Route APEX Load OpenTelemetry signals through a New Relic-enabled collector.", "Map span and transaction attributes into New Relic entities."},
		},
		{
			ID:                "appdynamics",
			Name:              "AppDynamics",
			Status:            statusFor("appdynamics"),
			Capabilities:      baseCapabilities,
			SupportedSignals:  baseSignals,
			CorrelationFields: baseFields,
			RequiredSettings:  []string{"external metrics or OpenTelemetry collector route"},
			Evidence:          []string{"pkg/api/external_metrics.go", "pkg/api/archive.go"},
			SetupActions:      []string{"Route APEX Load OpenTelemetry signals through an AppDynamics-capable collector.", "Map dependency target names to AppDynamics application nodes."},
		},
	}
	return profiles
}

func (s *Server) handleListInfrastructureMonitorPacks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, InfrastructureMonitorPacksResponse{
		GeneratedAt: time.Now().UTC(),
		Packs: []InfrastructureMonitorPack{
			{
				ID:           "kubernetes",
				Name:         "Kubernetes",
				Status:       "supported",
				Source:       "generator inventory, fleet capacity, resource samples",
				ResourceType: "cluster",
				Signals:      []string{"pod saturation", "active slots", "reserved slots", "generator health", "version mismatch"},
				Evidence:     []string{"/api/v1/system/inventory", "/api/v1/fleet/alerts", "/api/v1/fleet/capacity"},
			},
			{
				ID:           "linux-hosts",
				Name:         "Linux Hosts",
				Status:       "supported",
				Source:       "archived resource samples",
				ResourceType: "host",
				Signals:      []string{"cpu_process_pct", "cpu_free_pct", "mem_process_mb", "net_sent_kbps", "net_recv_kbps", "disk_read_kbps", "disk_write_kbps"},
				Evidence:     []string{"/api/v1/tests/{id}/archive/resources", "pkg/api/archive.go"},
			},
			{
				ID:           "jvm",
				Name:         "JVM",
				Status:       "integration_contract",
				Source:       "external metrics",
				ResourceType: "runtime",
				Signals:      []string{"heap used", "gc pause", "thread count", "class loading", "jvm error rate"},
				Evidence:     []string{"pkg/api/external_metrics.go", "/api/v1/tests/{id}/archive/root-cause"},
				SetupActions: []string{"Publish JVM metrics into archive resource external_metrics with a jvm.* prefix."},
			},
			{
				ID:           "database",
				Name:         "Database",
				Status:       "integration_contract",
				Source:       "external metrics and dependency telemetry",
				ResourceType: "database",
				Signals:      []string{"query latency", "connection pool saturation", "lock waits", "deadlocks", "replica lag", "Oracle wait and session metrics"},
				Profiles:     []MonitoringReadinessProfile{oracleDBMonitoringProfile()},
				Evidence:     []string{"pkg/api/archive.go", "/api/v1/tests/{id}/archive/root-cause"},
				SetupActions: []string{"Map database dependency targets and publish db.* external metrics.", "For Oracle Database, publish the oracle.db.* canonical metric contract through a Prometheus-format endpoint before connecting SaaS monitoring."},
			},
			{
				ID:           "web-server",
				Name:         "Web Server",
				Status:       "integration_contract",
				Source:       "server telemetry and external metrics",
				ResourceType: "service",
				Signals:      []string{"server processing", "transfer time", "5xx rate", "connection reuse", "response payload size"},
				Evidence:     []string{"pkg/api/runner.go", "pkg/api/archive.go"},
				SetupActions: []string{"Enable server telemetry extraction in protocol actions and export web.* external metrics when available."},
			},
			{
				ID:           "queue-brokers",
				Name:         "Queue Brokers",
				Status:       "supported",
				Source:       "JMS/gRPC protocol telemetry and external metrics",
				ResourceType: "messaging",
				Signals:      []string{"message latency", "consumer lag", "messages sent", "messages received", "broker saturation"},
				Evidence:     []string{"pkg/api/runner.go", "docs/ROADMAP.md#protocol-telemetry-parity"},
				SetupActions: []string{"Publish broker metrics with queue.* or broker.* prefixes for root-cause correlation."},
			},
		},
		Evidence: []string{"docs/COMPETITIVE_COMPARISON.md#k6", "docs/COMPETITIVE_COMPARISON.md#support-matrix"},
	})
}

func (s *Server) handleListReportProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ReportProfilesResponse{
		GeneratedAt:     time.Now().UTC(),
		DefaultProfile:  "engineer",
		Profiles:        buildRunArchiveReportProfiles(),
		DownloadFormats: []string{"html", "json", "csv", "pdf", "jsonl", "parquet"},
	})
}
